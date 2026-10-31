# v1.0.0 基线收尾 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development 按任务实施与独立审查。任务只在实际验证后勾选。

**Goal:** 完成 consolidate-pre-1-0-history 的遗留测试、文档和 13 项 API 盘点必要调整。

**Architecture:** 保留公开领域服务及不透明 Repo 装配，隐藏 sqlc 查询与 HTTP 内部工具。删除无实际用途的开发期公开入口，保留审计读取、凭据和认证事务行为；公开调用变更提供迁移说明。

**Tech Stack:** Go、PostgreSQL 17、Redis、sqlc 1.31.1、OpenSpec。

**Spec:** [当前变更 design](../../../openspec/changes/archive/2026-10-31-consolidate-pre-1-0-history/design.md)、同目录 specs 与 tasks；本计划续接 2026-10-30 的已完成计划。

## Global Constraints

- 不改 0001 字节、SQL 查询、数据库结构、JWT/refresh/密文/缓存 JSON 格式。
- New 不做 I/O；宿主 Pool/Redis 生命周期不变；领域 READ COMMITTED、行锁及事务回滚保持。
- 用户已明确将 13 项 API 必要调整纳入本变更；保留项必须给具体理由，不以“待决定”代替评估。
- 使用已有任务分支继续；不提交、推送、合并、删除 tag/Release 或归档。生成代码只通过 sqlc 生成。
- 原始日志放 .test-output；每个任务报告与审查放本计划独立 SDD workspace；验收记录统一到当前 change。

## Review Focus

- dirty 与版本超前拒绝后 catalog、数据、锁及连接完整：Task 1 验证。
- 修改覆盖登记副本不会污染后续读取：Task 2 行为测试先失败再修复。
- grace 字段改名后旧 ae/re 缓存仍可读：Task 2 手工旧 JSON 测试。
- 外部消费者不能导入内部生成包，生产公开签名不泄露生成类型：Task 3 外部模块编译检查。
- 同一事务、READ COMMITTED 与独立 migrate 密钥检查仍成立：Task 3 保留对应行为测试，Task 4 完整门禁。

### Task 1: 遗留测试与相邻说明（OpenSpec 10.1）

**Files:** migrations/baseline_test.go、pii/format_test.go、examples/httpflows/flows_test.go、migrations/reset.go、docs/http-client-flows.md、docs/migrations.md；仅必要的合成 fixture 命名及测试整理。

**Interfaces:** 不改生产签名和行为；测试使用现有外层与引擎锁标识。

- [x] 补 dirty_precedence catalog/业务记录原值不变断言；为两条拒绝路径用可取消超时及显式锁/连接释放核验替代一秒耗时猜测。复用既有锁检查方式，不加只验证实现细节的测试。
- [x] 并发刷新加入 ready/start 屏障；pii 构造错误逐项检查；将仅与本变更相关的 Rename/Source/legacy 测试命名改为当前语义、展开单用例表驱动，合成 fixture 引用成对更新。
- [x] 补解绑示例身份策略、第二邮箱与 MaxIdentitiesPerKind=2 的前提及默认失败原因；修正 ErrDestructiveOperation 注释和 Down 文档的当前状态表述。
- [x] Go 文件 gofmt；真实 PostgreSQL 运行 migrations、pii、examples/httpflows 和受影响的种子/来源测试。报告具体命令、结果及 skip；无生产行为修复不要求人为制造红灯。

### Task 2: 收敛公开符号（OpenSpec 10.2）

**Files:** audit/authentication、ids、user/code、user/idp/wechat*、audit/store.go、audit/audit.go、user/repo.go、user/service_purge*、session/grace、user/sender/smtp、相应调用与测试、scripts/required-tests.txt。

**Interfaces:** PurgeCoveredTables() map[string]string；NewWeChat(WeChatConfig,*http.Client,*slog.Logger)(*WeChat,error)；smtp.ChineseRenderer；grace.Pair.AccessExpireTime/RefreshExpireTime。MapLegacy、ids.AuditEvent、code.ErrExhausted、Store.ListByUser 和旧 WeChat 构造签名不再公开；retentionBatchSize/isUniqueViolation 私有。

- [x] 先加覆盖登记隔离行为测试并见其失败，再以私有登记和副本函数实现；增加手工旧 ae/re JSON 解码的行为测试，保持短标签和缓存编码不变。
- [x] 删除 MapLegacy 和写侧 legacyID 分支，保留私有 legacy_event_id 快照的现存读取/隐私清理兼容。测试用手工通用事件固定旧读取行为；保留在线 action/actor、管理员完整性、UUIDv7、脱敏测试。
- [x] 删除无用 e 常量及 ErrExhausted 和不可达映射；保留通用 ids.Kind 行为、实际最后一次错误验证码/过期/限流测试与已有观测枚举。ListByUser 使用显式 Source/Namespace 的 ListByResource 替代。
- [x] 私有化批量参数、唯一冲突 helper；必要外部测试用测试专用桥接或直接 PgError 断言；不新增生产导出替代。WeChat 配置构造器改为正式 NewWeChat，保留配置错误、空配置、两种 subject 模式等行为。
- [x] SMTP 与 grace 仅改 Go 名称，更新全部运行时代码、示例及当前文档；不改历史归档/历史计划正文。保留 IdP 其他构造器和协议专用 Verify 方法，其统一抽象是现有 Provider.Verify。
- [x] gofmt、相关单元/数据库测试和 vet；更新必需测试路径/名称，删除条目时在报告列明被替代测试或确实退役原因。

### Task 3: 封装数据访问与 HTTP 工具（OpenSpec 10.3）

**Files:** sqlc.yaml、user/db 与 audit/db（生成）、internal/userdb、internal/auditdb、user/repo.go 与测试、httpapi/jsonbody、httpapi/reqid、httpapi/internal、scripts/check-generated.sh、required-tests.txt、所有实际 import。

**Interfaces:** user.Repo/NewRepo、user.Deps.Repo/Codes/Revocation/Grace、两种 CheckKeyVersions 保留；Repo.Q/WithTx/WithTxRaw 私有，不再以生产签名暴露 db.Queries。

- [x] 将 sqlc 输出改到根 internal/userdb 与 internal/auditdb，保留生成 package db；运行 sqlc generate 后移除旧生成输出，更新 imports 和检查脚本。不手改生成内容。
- [x] Q/WithTx/WithTxRaw 私有化；外部测试通过 user/export_test.go 的明确 Test 前缀访问器（或包内测试）获取 fixture 查询/事务，不在生产库保留测试入口。保留原有事务 rollback/panic/宿主默认隔离与内部领域 READ COMMITTED 测试，并说明后者才是公开领域契约。
- [x] HTTP 工具移到 httpapi/internal/jsonbody、httpapi/internal/reqid，保持严格 JSON 和请求 ID 行为；同步 required-tests 包路径。
- [x] 独立临时外部 module 使用 replace 指向本地源码，仅测试材料在 .test-output；公开装配与直接领域服务可编译，旧内部包不能导入，公开 Repo 不再提供 Q/WithTx/WithTxRaw。新测试应基于可见性契约，不写大范围 AST 镜像检查。
- [x] check-generated、go build/vet、相关事务与 accountsvc migrate 测试通过；日志报告不把 skip 当通过。

### Task 4: 工件同步与最终验证（OpenSpec 10.4、10.5）

**Files:** 当前 change proposal/design/tasks/specs/verification、相关 README/docs/AGENTS、openspec/specs。

**Interfaces:** 按 D12 记录 13 项已评估决策及迁移替换；同步 specs 不归档。

- [x] 统一 tag 尚未删除等状态、逐项落实 API 决策和迁移表；修改公开仓储事务旧规格，明确隐藏查询不改变领域事务和宿主传入 pgx.Tx 契约；保留项不宣称全仓 API 已发布冻结。
- [x] 按 openspec-sync-specs 同步 delta；校验全部规格和 Markdown 链接，保留历史验收原文并追加本次结果。
- [x] 运行 sqlc 一致性、迁移哈希历史、OpenAPI；Linux 完整 verify.sh（新建两独立空恢复库）和 verify-accountsvc.sh，补非 root 文件权限用例；检查必需门禁和全部异常 skip。
- [x] 每任务独立审查，最终对完整新增差异及当前分支与规格的一致性复核；实质问题修复后记录 evidence 和剩余外部动作，不自动合并/发布/归档。
