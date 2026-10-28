# 数据库迁移与恢复

包通过 migrations.Up 或 Auth.Migrate 应用未执行版本。SQL、schema_migrations 均随包管理并落入指定 schema；当前版本默认 account。迁移过程按数据库和 schema 加锁，从 schema 创建开始覆盖初始化和 SQL 执行。外层初始化锁和兼容旧工具的引擎锁，每次等待最多 15 秒，均尊重调用 context 的取消和更短期限；超时返回可重试错误，不意味着迁移已完成。取消等待不会执行 DDL；已经执行中的迁移 SQL 沿用底层引擎语义，不承诺取消会自动回滚。

Auth.Migrate 仍按 search_path 检查、Redis Ping、数据库迁移、历史身份密钥检查的顺序执行。最后的密钥检查失败不代表数据库迁移没有成功，排障时需同时检查当前版本和实际表结构。

## 当前头像初始定义：version 1

该完整初始定义已随 [v0.5.0](releases/v0.5.0.md) 发布，tag 固定为 `102315c3069273f7a8c3ad253c1f4ae01255f8c6`。以下为发布前的有限授权记录；v0.5.0 tag 内的 0001 字节保持。其后当前 0001 又按 `add-google-sign-in`、`add-identity-provider-registry` 的明确授权调整（见[当前初始基线](#当前初始基线)），已随 v0.6.0 发布；`add-purged-user-feed` 的 `purged_user` 表已随 v0.7.0 发布（tag 固定到 `6f3b072a8372b83be75b508d1ea521f368c80723`）；后续真实结构变更从 0002 递增。

本轮用户明确要求“不新增 0002 迁移，改现有 0001”，范围仅为在当前 `user_account` 增加 `avatar_url TEXT NULL`，无默认值、业务 CHECK 或索引。同步 sqlc、当前 `initialFrozen` 与预检，新空 schema 安装仍为 version=1/dirty=false；0001 down 的整表删除定义已覆盖头像列，无需改动。此前资料单列整合与更早授权分别保留历史事实。

预检要求完整头像列类型、可空性和无默认值。前一含 `profile_attributes` 但缺 `avatar_url` 的 v1（无论空或非空）、其他历史 v1 和撤回 v2 均无写拒绝，dirty 优先，保留 catalog、数据与 version/dirty。重复 Up 不补列、不重放同号 SQL；本轮不提供旧数据迁移或升级承诺，不 Force、改版本或清库。

仅当前初始哈希更新；v0.4.0 精确 tag `62f0799b6c6ef1603b33f81834d39033dd0d0c4d`、其他已发布 tag/SQL 和冻结源保持。后续真实增量从 0002 开始，测试 probe 仍为 latest+1。新部署使用经核验的新空资源；有数据保留需求时另行制定并验证迁移，不能据此假定旧代码混跑或回退安全。本次实际检查与未验证项集中在[头像验收记录](../openspec/changes/archive/2026-10-21-add-user-avatar-url/verification.md)。

## 当前用户资料属性初始整合：version 1

本节保留头像变更之前的资料单列整合背景；当前完整初始定义另包含上一节的头像列。

本轮用户明确要求“0002 并入 0001，无需考虑旧数据迁移”，仅授权把 `profile_attributes JSONB NOT NULL DEFAULT '{}'::jsonb` 单列合入当前工作树 0001，撤回生产 0002；新空 schema 安装 version=1/dirty=false，其他列、索引和业务行为保持。当前 initialFrozen 新哈希与 v0.4.0 已发布旧定义分别固定；v0.4.0 精确 tag `62f0799b6c6ef1603b33f81834d39033dd0d0c4d`、其他发布 tag/SQL 和冻结源保持。旧 v0.4.0 v1 缺资料列、其他历史 v1 及各代撤回 v2（含单列、四列、五列及混合资料 v2）均无写拒绝，dirty 优先，保留 catalog、数据及 version/dirty；不转换旧数据、不重放同号 SQL、不 Force、改版本或清库。本轮不提供旧数据迁移工具或升级承诺；后续真实增量从 0002 递增，测试 probe 使用 latest+1（当前为 2）。

预检核对完整基础结构、资料单列类型/默认值/非空性、无旧资料元数据及账号原五个时间列精度。当前完整 v1 重复 Up 不改变 catalog、数据或时间；仅资料列整合，不新增 JSON 业务 CHECK、索引、外键或触发器。公共 API、账号行锁、Schema 演进和认证生命周期保持。

新部署仅用人工核验的新空资源，不承诺 v0.4.0、54afa4b 或旧单列增量实现混跑/回退。公共 Down 无 I/O 拒绝。宿主 Schema 配置兼容调整、收紧修复及未知 null 删除规则保持，配置演进不等于旧数据库转换。

固定测试文件 testdata/0002_success.up.sql 和 0002_failure.up.sql 仅作 latest+1 测试源。真实恢复覆盖 version 1、资料值/精确数字、账号时间、认证/匿名化和源库不变。[本轮验收记录](../openspec/changes/archive/2026-10-21-add-schema-validated-user-extensions/verification.md)顶部列当前状态，下文旧日志仅证明原阶段。以下会话整合保留历史事实。

## 会话认证依据初始整合（v0.4.0 发布历史）

此变更随 v0.4.0 发布。发布前，会话0002先按用户明确要求合入当时未发布的0001，又经明确批准简化：session不再包含known标记，`session_authentication_identity`保存必有的具体来源，`session_client_info`以session_id为单列主键保存一份`client_info JSONB NOT NULL`及服务器observe_time/create_time/update_time。没有来源未知正常态或双快照类型，没有外键；数据库保留主键、ID格式及非空约束，不增加JSON对象/业务CHECK，内容由业务读写统一校验。同步up/down、baseline、固定哈希和sqlc，无生产0002，新空库安装仍为version=1/dirty=false。已发布tag/SQL及冻结源快照不变，当前验收状态见[简化记录](../openspec/changes/archive/2026-10-20-simplify-session-context/verification.md)。

**下列历史数据库不能直接升级。** 已发布v0.3.0的v1缺少会话上下文；前一`b414fef`的v1仍含known标记、snapshot_kind和平坦双快照，原会话v2也已撤回。三者均返回ErrIncompatibleBaseline，保留catalog、数据、version/dirty，不自动补表、转换、重放0001、降版本或清库。新部署使用人工核验的新空数据库/schema；需要保留数据时必须另行制定并验证迁移方案，不能只因版本号都是1就直接接管。

当前会话来源是非空领域不变量，不能用空集合表示安全，也不从身份列表或尽力审计推断。新建/重新认证在事务内验证来源身份归属和有效状态；当前详情、重新认证、换绑使用来源时检查有效性。解绑会检查同账号全部未吊销会话（含自然过期但未清理者），任一缺少有效来源则内部错误并整事务回滚；合法时只撤销命中来源的会话。普通JWT验证不增加数据库查询；内省只按 sid 读取会话吊销状态，不读取来源。客户端描述为单份完整观察，有效非空观察整体替换，nil/空观察保持原值与时间，读取非法JSON为内部错误。

1. 核对目标 schema、实际结构、版本、密钥、Redis 前缀和备份。新安装先确认目标为空；有旧数据时先完成独立迁移方案，不对旧库 Force 或改版本。
2. 进入维护窗口，暂停并排空旧认证写请求和相关维护任务，或统一切换全部认证副本。旧副本不得继续建无来源会话、重新认证不替换来源或执行旧解绑/换绑撤销规则；不可让新旧副本混承接身份变更。
3. 该次 v0.4.0 对核验后的新空目标执行迁移至 version=1/dirty=false 和完整七表；当前新安装使用本页资料初始整合 version 1 流程，不提供旧库升级。全部新副本就绪后再恢复入口。网关明确放行当前详情 GET 和实际使用的可选头/CORS，验证旧客户端、解绑 204 可能退出与新客户端重查流程。
4. 观察查询与撤销行为，保持原JWT/refresh/Redis故障模式；Migrate重复执行不重建来源。保留密钥与Redis状态，数据库备份并不包括它们。

回退优先前向修复。旧v0.3.0的预检只检查流程和审计，可能接受同为v1的新结构，不能依赖旧迁移器阻止启动；部署必须人工阻止混跑或未经核验的回退。旧sqlc使用显式列，读取可用也不证明具备新来源与撤销规则；前一开发版本的known/双快照写法同样不兼容。不能绕过Migrate将旧身份写路径重新上线。必要应用回退先停写/排空，另行证明结构、存量来源与安全行为兼容；公共Down无I/O拒绝，不能通过Force、改version、自动清库或清Redis回退。数据库恢复必须先落独立空库，核对两张上下文表、每个会话有效非空来源、单份nested客户端JSON及extensions/原观察时间、版本和源库未变，再按产品维护流程切换，不能承诺零数据损失。

## 当前初始基线

当前初始结构在 v0.4.0 七表会话定义之上，按各次限定授权合入资料单列、头像列，以及 `add-purged-user-feed` 的 `purged_user` 表（共八表）；生产目录只有 0001 up/down，version=1/dirty=false。0001 down 覆盖八表，公共 Down 拒绝；审计流程关联仍为 data.accountkit.flow_id。此前审计、流程改名、索引和会话简化授权各自限于当时范围。

`add-purged-user-feed` 经用户明确要求“直接改 0001，不新增 0002”，在当前 0001 末尾新增 `purged_user`（`user_id` 主键与格式 CHECK、`seq BIGINT NOT NULL GENERATED ALWAYS AS IDENTITY` 唯一约束 `uk_purged_user_seq`、`purge_time` 默认 `clock_timestamp()`）及 `idx_purged_user_purge_time`，0001 down 最先删除该表。其他表、列、索引与约束不变，不新增 0002，新安装仍为 version=1/dirty=false。v0.6.0 发布的 0001 与此前当前定义逐字节相同，现作为独立 pin（tag `ec6802cd221db3ce107e79bac92174bc35a0ba7d`），当前 initialFrozen 更新为新哈希。已安装 v0.6.0 或更早版本的同号数据库缺少该表，预检无写拒绝（`migrations.ErrIncompatibleBaseline`，dirty 优先）；不补表、不 Force、不改版本、不清库，使用新版本须经人工核验后改用新空资源。**旧二进制（v0.6.0 及本变更前的 develop）的预检不检查这张新表，会接受新 version 1 结构；若让旧二进制或旧嵌入宿主对新结构运行维护任务，其 purge 不写已清理记录，该账号永远不会出现在拉取清单中，外部业务数据也就不会被清理。部署须人工阻止新旧版本混跑，以及在新结构上回退到旧二进制。**数据库从备份恢复或重新部署后，已清理记录的序号可能回退到业务方已保存的游标之下：拉取接口会对这类超前游标返回 400 `INVALID_PAGE_TOKEN`，业务方须清空游标从头重拉（清理按 user_id 幂等）。

`add-google-sign-in` 只改当前 0001 up 中 `identity.kind` 与 `provider_meta` 的注释，并更新当前 initialFrozen。不新增列、索引、CHECK 或 0002。已发布 tag、冻结源和 0001 down 不变。同号数据库不会重放这段注释。旧二进制不能解释 kind 5。后续真实结构增量仍从 0002 开始。

`add-identity-provider-registry` 经用户明确要求“直接改 0001”，只把当前 0001 中 `identity.kind` 改为 `VARCHAR(32) COLLATE "C" NOT NULL` 并加格式 CHECK `identity_kind_format`（`^[A-Z][A-Z0-9_]{1,31}$`），同步注释、sqlc、运行时预检与当前 initialFrozen。四个以 kind 开头的索引定义文本、其他列与约束、0001 down 不变，不新增 0002。`identity.kind` 仍为 SMALLINT 的 version 1（v0.5.0 及其后、本变更前的安装）与该列漂移的结构均无写拒绝，dirty 优先；不转换列、不重放同号 SQL、不 Force、改版本或清库，也不提供旧数据迁移。旧二进制（v0.5.0 及本变更前的 develop）的预检不认识这一列变化，可能接受新 version 1 后在读写 kind 时失败；部署须人工阻止新旧版本混跑，以及在新结构上回退到旧二进制。实际当前哈希记于[本变更验收](../openspec/changes/archive/2026-10-23-add-identity-provider-registry/verification.md)。

基线预检要求完整 sign_in_completion_flow、十列 UUIDv7 审计、必有来源/单份描述、当前资料单列、头像列、文本身份类型列和完整 purged_user 表。旧缺头像资料 v1、v0.4.0 v1、v0.3.0 v1、双快照/旧流程/审计、双表及各代撤回 v2 均返回可由 errors.Is 识别的 migrations.ErrIncompatibleBaseline，保持 catalog、数据和 version/dirty，dirty 优先。UnsafeReset 沿用相同预检，不接受旧基线；重复 Up 不补列或重放同号 SQL。

冻结源、全部发布 tag/SQL、v0.4.0 旧定义与当前 initialFrozen 分别固定。头像轮当时的哈希和校验结果记于[该轮验收](../openspec/changes/archive/2026-10-21-add-user-avatar-url/verification.md)。后续真实增量从 0002 递增；旧发布说明和归档保留原文，当前操作以本节为准。

此前索引命名维护按用户明确要求未新增迁移：直接将未发布 0001 的 22 个普通索引命名为 `idx_<表名>_<字段或用途>`、4 个独立唯一索引命名为 `uk_<表名>_<字段或用途>`；主键索引保留 `<表名>_pkey`，索引列、唯一性、谓词及访问方法保持。当时同步更新固定校验，初始版本仍为 1；历史 SQL 和源快照未改。已有同号数据库不会重放 0001，因此不会自动得到该次命名调整；不能靠修改版本或自动重建绕过基线检查。

此前审计校验职责调整同样经用户明确要求直接修改未发布 0001：移除五个自定义审计校验函数及业务 CHECK，保留 UUIDv7、JSON 基本形状、非空与唯一性等基础结构；业务内容、数量、64 KiB 上限和时间范围统一由 Go 校验。当时同步更新 up/down 固定哈希，初始版本仍为 1，其他表 CHECK、索引和历史源快照未改。该次调整没有为同号实例自动移除旧审计函数或 CHECK；此前仍使用 identity_flow 的完整十列同号库现在也明确拒绝，不会自动升级。采用当前定义须由操作者核验后准备新空资源，不修改版本或自动清库。直接 SQL 的导入与修数须复用 Go 校验，细节见[兼容说明](compatibility.md#事件生产与写侧)，实际验证见[当次记录](../openspec/changes/archive/2026-10-18-audit-validation-in-go/verification.md)。

数据库设计评审的索引调整同样经用户明确要求不新增 change、直接修改未发布 0001，范围仅限索引：密钥回填索引改为 `idx_identity_digest_key_version_create_time_id` / `idx_identity_cipher_key_version_create_time_id`（版本、create_time、id 复合并排除 NULL 版本）；新增部分唯一索引 `uk_session_user_id_device_id`（`WHERE revoke_time IS NULL`）作为同设备单一未吊销会话的数据库兜底，并取代 `idx_session_user_id`；移除包内无查询使用的 `idx_audit_event_occur_time_id`、`idx_audit_event_request_id`、`idx_audit_event_flow_id` 与 `idx_sign_in_completion_flow_session_id`。表、列、约束、down 与初始版本 1 保持，同步更新当前固定哈希；历史 SQL、v0.3.0 标签和源快照未改。基线预检不检查这些索引，已有同号数据库不会重放 0001，因此仍保留旧索引、也不会得到新唯一兜底；新查询在旧索引下语义不变，仅缺少相应性能与唯一兜底。需要新定义时由操作者核验后使用新空资源，或另行设计并验证 0002 增量，不 Force、改版本或自动清库。此授权不构成日常改写初始 SQL 的开关。

审计 ID、公开 Event/Reader 和旧分页游标同时升级，见[审计兼容说明](compatibility.md#通用审计公开接口与协议迁移)。旧结构库的备份应先恢复到独立空库核验，恢复本身不会把十七列审计变成当前十列；需要保留旧数据时须单独评估并验证转换，不能将备份直接覆盖当前库或修改版本号绕过拒绝。历史合成数据的 UUIDv7 映射及 legacy_event_id 仅证明指定格式可保留，不是生产库转换工具或原地升级保证。

## 升级前

1. 核对包版本、目标数据库/schema、旧版本、密钥版本、Redis 前缀及业务匿名化配置。
2. 保留能够恢复的数据库备份与对应密钥，记录备份时间点；确认应用兼容窗口与停止写入安排。
3. 在预发布环境验证新空库安装 version=2、非空 v0.4.0 v1 升级、合法 v2 重复 Up 与实例隔离，以及旧 v0.3.0 v1、撤回会话 v2、旧流程/审计、双表和 dirty 拒绝；不支持历史旧库直接接管。测试 probe 使用 latest+1。
4. 运行发布检查，确认固定历史发布和当前基线均通过；本次明确整合完成后迁移只追加，禁止重新编号或编辑已发布文件。

## 登录补全流程命名调整

以下为 v0.3.0 发布前的历史操作。用户当时明确要求“不新增 0002，直接改 0001 中的表定义”，仅限流程对象改名。当时重命名 0002 已撤回，v0.3.0 安装版本为 1；本轮会话整合依据新的明确授权，与该历史授权分开记录。冻结源快照、已发布历史 SQL 和标签不变。

1. 核对目标数据库/schema、实际表名与版本、备份及对应密钥。旧表名 v1、双表和旧 v2 均不得直接交由新代码接管。
2. 采用新定义时，人工核验后准备新的空数据库/schema；需要保留旧数据时，先另行制定并验证迁移方案，本包不提供自动转换。
3. 该次历史安装对新空资源执行 `Auth.Migrate`，确认 version=1、dirty=false，仅存在 sign_in_completion_flow。v0.4.0 也安装为 1，但额外要求完整会话上下文；版本数字相同不能代替结构核验。
4. 使用匹配的新代码与实例配置启动应用并核验登录补全、维护任务；将原 `cleanup_identity_flows` 的指标、日志检索和告警规则更新为 `cleanup_sign_in_completion_flows`。

重复 Up 不自动改名、重放 0001 或修改版本，不使用 Force、清 dirty 或自动清库接管旧实例。旧应用仍查询 identity_flow，不能直接使用新结构；二进制回退须另行核验结构兼容。配套 0001 down 按新名称定义清理对象，公共 `migrations.Down` 继续无 I/O 返回 `ErrDestructiveOperation`。本次改名保持 AAD、实例上下文值、`f_`、审计 `IDENTITY_FLOW` 与 JSON `flow_id` 的格式兼容，但不因此提供旧库自动迁移或有效票据跨库接管保证。Go 入口兼容规则见[兼容说明](compatibility.md#登录补全流程重命名)。

## 诊断失败

使用明确 schema 限定名查询（示例 account，请替换为已经核验的合法 schema）：

```sql
SELECT version, dirty FROM account.schema_migrations;
SELECT tablename FROM pg_tables WHERE schemaname = 'account' ORDER BY tablename;
```

首次失败可能发生在版本表建立前，此时没有记录表；不要据此声称数据库未发生任何变化。dirty=true 时停止自动重试，保留失败 SQL/版本、日志和备份，检查实际结构及数据。包不自动 Force、不清除 dirty、不盲目重放失败版本。不要把手工将 dirty 改为 false 当成修复。

核实失败事务的实际效果，再选择前向修复或备份恢复。确有必要用外部迁移工具修复版本标记时，必须先证明实际结构与对应版本一致；只改标记不会恢复丢失的数据。

## 回退选择

- 应用回退：只有旧代码已验证兼容当前数据库结构时才可回退二进制。
- 前向修复：通过新的版本修正问题，保持历史 SQL 不变。
- 数据库恢复：先恢复到独立数据库核对版本、数据、身份解密和认证，再按产品维护流程切换；恢复会丢失备份时间点之后的写入，产品方需确定可接受的数据损失窗口。

数据库备份不包含密钥存储和 Redis。恢复时须保留对应的历史签名/HMAC/加密密钥，协调 session 数据、吊销集、刷新宽限缓存及验证码有效期；无法证明状态一致时，在产品切换流程中明确撤销受影响会话并要求重新登录，不能把清空 Redis 当成安全会话恢复。包内演练使用合成账号和独立 Redis，不代替产品生产灾备验收。

## BREAKING：全量 Down 默认禁用

原 migrations.Down(ctx, connCfg, schema) 签名保留，但始终返回 ErrDestructiveOperation，并且不访问数据库。常规生产升级与回退均不得调用它。

一次性开发测试数据库清理须显式调用：

```go
err := migrations.UnsafeReset(ctx, connCfg, schema,
    migrations.UnsafeResetOptions{ConfirmSchema: schema})
```

此操作删除目标 schema 的认证迁移对象，保留 schema 本身；确认值必须完全一致。它不是权限控制，也不是生产回滚。现有调用 Down 的测试清理应迁移到该接口或使用测试自己持有的临时 schema 清理逻辑。

## 历史检查与恢复演练

当前sign_in_completion_flow保存加密的短期登录证明和完成结果，含pending首次客户端描述及原观察时间。备份恢复须覆盖其密文、cipher_version、原expire_time、账号/会话引用和version/dirty，并保留相应旧密钥。旧流程、旧v0.3.0 v1、前一`b414fef`双快照v1或撤回v2的备份先恢复到独立库核验，需使用当前代码时另行制定并验证数据迁移方案，不能重复Up、改版本或清库接管。v0.4.0 备份应为完整七表、version=1，包含有效非空来源及单份 nested 客户端 JSON（可含客户端描述 extensions）；当前 version 2 备份还包含账号资料 JSON 及账号原时间，不含 Schema/revision/独立时间元数据；v0.3.0备份也标为1，但缺少会话上下文。恢复演练验证数据/凭证格式和流程记录保持，不是旧库原地升级。源库和目标库必须为两个独立一次性空库。

Redis 验证码状态不在数据库备份中；恢复后的 PENDING 流程缺少验证码轮次时必须拒绝，不能仅凭数据库票据放行。COMPLETED 的相同请求只有在原期限内、账号和会话有效且 refresh 未轮换时可读取原结果；期限自然经过后应拒绝票据，仍可验证已完成会话的 refresh。演练不延长5分钟流程期限，也不宣称数据库备份实现零数据损失。

bash scripts/check-migrations.sh 校验固定源快照、v0.1.0/v0.2.0 的精确提交与原 SQL、v0.3.0 的精确提交与旧初始 SQL，v0.4.0、v0.5.0、v0.6.0 的精确提交与原始 SQL，以及当前整合基线和全部可达新正式 vX.Y.Z tag，包括合并分支及 HEAD。各代基线使用独立固定 LF 字节哈希；发布过的增量不能被后续 tag 覆盖或删除，新标签不能借用历史基线例外。浅克隆须获取完整历史和 tags。

bash scripts/verify-recovery.sh需要psql/pg_dump/pg_restore 17、Go和两个明确指定的独立空测试库。脚本通过集群system_identifier和实际数据库名称判断同库，要求具备读取pg_control_system的测试权限，并拒绝非空库。只生成合成数据，不删除或覆盖既有库。恢复后检查七个业务表、版本、解密和旧访问/刷新令牌；核对每个会话有效非空来源、双身份来源、单份nested客户端描述的app/browser并存、制造商/build、extensions及原观察时间。完整备份包含两个上下文子表，并在source-unchanged阶段验证源库未改变。普通TestRecoveryFixture仍预期skip，只有脚本三阶段强制运行才是恢复演练证据。历史凭证夹具须显式建立有效来源，不能将测试补关联描述成生产自动迁移。证据保留在被Git忽略的.test-output/recovery.*下；重跑需提供新的空库。当前简化版本的实际门禁结果见[验收记录](../openspec/changes/archive/2026-10-20-simplify-session-context/verification.md)，历史记录不代表当前验证通过。
