## Context

动机见 proposal.md。现状：

- `user.Service.purgeOne` 在单个事务内完成匿名化：锁账号，处理身份、账号行、会话、审计与宿主 Anonymizer，提交后才写 `USER_PURGED` 审计。
- `PurgeDueUsers` 由维护任务调用，维护任务用 advisory lock 保证同一时刻只有一个副本在执行；但它也是导出方法，宿主可以直接调用。
- 当前 `0001` 与 v0.6.0 已发布版本逐字节相同，v0.6.0 尚未单独 pin。历次“直接改 0001”的先例包括：新增独立预检（`*_baseline.go`），pin 前一已发布版本，更新 `initialFrozen`，在 AGENTS.md 记录限定授权。
- 用户已确认：直接改 0001，不新增 0002；当前无正式部署，不提供旧数据迁移；默认长期保留、可配置、下限 30 天；使用独立的服务凭据。

## Goals / Non-Goals

**Goals：**
- 已清理记录与匿名化原子一致，拉取方按游标读取不会漏读。
- 业务方可以在约 7 天的自身故障后补拉（默认长期保留）。
- 旧 version 1 数据库被无写拒绝，迁移历史检查继续保护全部已发布定义。

**Non-Goals：**
- 不做推送、webhook 或确认（ack）语义。
- 不改变 purge 与匿名化本身的语义或时机。
- 不提供旧库补表或数据迁移；不提供管理面过滤方案。
- R3 外部鉴权契约文档是纯文档，不在本变更内，另行提交。

## Decisions

### D1 表结构与游标：使用自增 `seq`，而非“时间 + ID”键集

```sql
-- 已清理用户记录：外部业务服务按 seq 游标拉取，按 user_id 幂等清理自身数据。
CREATE TABLE purged_user (
    user_id    TEXT COLLATE "C" PRIMARY KEY
               CONSTRAINT purged_user_user_id_format CHECK (user_id ~ '^u_[0-9a-hjkmnp-tv-z]{13}$'),
    seq        BIGINT NOT NULL GENERATED ALWAYS AS IDENTITY CONSTRAINT uk_purged_user_seq UNIQUE,
    purge_time TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
-- 保留期清理：按写入时间删除过期记录
CREATE INDEX idx_purged_user_purge_time ON purged_user (purge_time);
```

项目惯例是按“时间字段 + 唯一 ID”做键集分页，资源表不另加自增 ID。这里是有意偏离。原因是拉取接口的正确性依赖“游标顺序与提交可见顺序一致”：

- `now()` 是事务开始时间，`clock_timestamp()` 也可能受时钟调整影响。时间键集无法排除这样的情况：较早时间戳的事务较晚提交，被已经越过它的游标跳过。
- `seq` 不是资源 ID，不对外暴露、不做查找，只用作游标；主键仍是 `user_id`，与资源 ID 一一对应。

### D2 写入顺序：在表锁下分配序号

在 `purgeOne` 事务的最后一步（宿主 Anonymizer 成功之后）执行 `LOCK TABLE purged_user IN SHARE ROW EXCLUSIVE MODE`，再执行 `INSERT`。

- 该锁模式与自身冲突，因此并发的清理事务在“取序号到提交”这一段完全串行：序号小的记录一定先提交。
- 读取只持有 ACCESS SHARE，不被阻塞。
- 锁放在事务末尾，持有时间只覆盖插入与提交。
- 回滚产生的序号空洞不影响正确性。

不依赖维护任务的 advisory lock，是因为宿主可能直接调用 `PurgeDueUsers`。

### D3 库 API

```go
type PurgedUser struct {
	UserID    string
	PurgeTime time.Time
}

// after 为上一次返回的游标（从头开始传 0）；返回按 seq 升序的记录与新游标。
func (s *Service) ListPurgedUsers(ctx context.Context, after int64, limit int) ([]PurgedUser, int64, error)
```

没有新记录时，新游标等于 `after`。`limit` 取值范围为 1 到 1000。

### D4 HTTP 契约（accountsvc）

- 路由：`GET /v1/purgedUsers?page_token=&page_size=`，集合名采用 AIP 的 lowerCamel 形式。
- `page_token`：不透明的 base64url 字符串（内部为带版本前缀的序号）；省略表示从头开始；非法时返回 400 `INVALID_ARGUMENT` / `INVALID_PAGE_TOKEN`。
- `page_size`：默认 100，最大 1000。
- 响应：`{"purged_users":[{"user_id":"u_…","purge_time":"RFC3339"}],"next_page_token":"…"}`，设置 `no-store`。
- `next_page_token` 始终返回当前位置。这一点偏离 AIP“末页 token 为空”的约定：拉取方需要一个可以持久保存的位置，用来下次续拉。
- 错误体使用 AIP 格式（`apierror`）。这不是 OAuth 端点，因此不沿用内省的 OAuth 错误体。401 带 `WWW-Authenticate: Basic realm="accountsvc-purge-feed"`。

### D5 鉴权：独立凭据列表，复用内省的解析与比较逻辑

`ACCOUNTSVC_PURGE_FEED_CLIENTS` 的格式与内省相同。把内省 handler 中的客户端校验（按客户端 ID 选秘密、恒定时间比较、拒绝重复的 Authorization 头）抽成内部共享函数，两个 handler 共用，避免各写一份。配置阶段拒绝同一秘密同时出现在两个列表中。未配置时路由返回 503 `PURGE_FEED_NOT_CONFIGURED`，而不是不挂载：这样调用方能区分“没配置”和“路径错误”。

### D6 保留期

- `Config.PurgedUserRetention`（`time.Duration`），环境变量 `PURGED_USER_RETENTION`。
- 值为 0 表示长期保留（默认）；大于 0 时必须不小于 720h（30 天）。
- 维护任务 `cleanup_purged_users` 始终注册，保留期为 0 时空跑；大于 0 时按 `purge_time` 分批删除过期记录，沿用其他清理任务的批量方式。
- 指标：标准化指标规范把内置任务标签固定为六个，未知名称归入 `other`。本任务不扩展该标签集合（那需要修改指标规范与适配器），因此当前归入 `other`；docs/metrics.md 已注明。需要单独观测时另行变更指标规范。

### D7 迁移安全

- 新增 `checkPurgedUserBaseline`：精确检查表、列类型、排序规则、可空性、identity 列、主键、唯一约束、格式 CHECK 与索引。只读，在 `checkBaseline` 中接在 identity kind 检查之后执行。
- `internal/migrationcheck`：`publishedInitialReleases` 增加 `refs/tags/v0.6.0`（v0.6.0 tag 指向提交 `ec6802cd221db3ce107e79bac92174bc35a0ba7d`），哈希为当前 `initialFrozen` 的旧值；`initialFrozen` 更新为新文件的哈希。
- 测试使用 `git show v0.6.0:migrations/0001_init.up.sql` 安装旧结构，断言 Up 被拒绝，且 catalog、数据、version/dirty 不变；不用今天的 SQL 推导旧结构。
- AGENTS.md 在“迁移安全”中新增本轮限定授权条目，并同步 `docs/migrations.md`。

### D8 游标回退检测（审查后补充）

游标是裸序号，没有实例纪元。数据库从较早备份恢复或重新部署后，identity 序号会回退到业务方已保存的游标之下，之后写入的记录序号不大于旧游标，将被永久跳过。处理方式：`ListPurgedUsers` 在 `after > 0` 时读取序号已分配的最大值（`pg_sequence_last_value`，含回滚空洞），游标超过它即返回 `ErrPurgedUserCursorAhead`，HTTP 映射为 400 `INVALID_PAGE_TOKEN`，业务方清空游标从头重拉（清理幂等）。

- 正常运行时，业务方保存的游标都来自已提交记录，不会超过已分配最大值，因此不会误报。
- 这种检测不是绝对的：恢复后如果新清理的数量已追上旧游标，业务方才拉取，检测会失效。这是在不改表结构（不引入实例纪元）前提下的折中；文档同时要求恢复或重新部署后业务方主动清空游标。
- 预检同时校验 identity 序号的 increment=1、cache=1、no cycle，防止序号选项漂移破坏“序号顺序 = 提交顺序”。

## Risks / Trade-offs

- [已安装 v0.6.0 的数据库无法直接升级] → 用户已确认当前无正式部署；兼容说明与迁移手册写明须使用经人工核验的新空资源，不 Force、不改版本。
- [表锁让并发清理在末尾串行] → 清理是低频的维护操作，锁只覆盖插入到提交；读取不受影响。
- [业务方超过保留期未拉取会遗漏清理] → 默认长期保留；设置保留期时下限为 30 天，并在文档中写明。
- [`next_page_token` 始终非空，与 AIP 不一致] → 这是拉取场景的需要，写入契约与示例。

## Migration Plan

不提供迁移。新部署按新 0001 安装；旧库被拒绝时，使用新的空 schema 或数据库。回退到旧版本时，旧二进制对包含 `purged_user` 的同号库的处理以其自身的预检为准；部署方须阻止新旧版本混跑。
