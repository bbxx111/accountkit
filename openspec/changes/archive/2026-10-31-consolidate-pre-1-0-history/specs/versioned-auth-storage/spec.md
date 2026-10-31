## ADDED Requirements

### Requirement: 当前初始定义

包 SHALL 以单一 0001 定义完整认证结构：`user_account`（含 `profile_attributes JSONB NOT NULL DEFAULT '{}'::jsonb` 与 `avatar_url TEXT NULL`）、`identity`（`kind` 为 `VARCHAR(32) COLLATE "C" NOT NULL` 加格式 CHECK）、`session`、`session_authentication_identity`、`session_client_info`、`audit_event`（十列 UUIDv7）、`sign_in_completion_flow`、`purged_user` 及 `schema_migrations`。新空 schema 安装 SHALL 得到 version=1/dirty=false。JSON Schema 与属性业务校验 SHALL 在 Go 执行，不设外键、JSON 业务 CHECK、触发器或逐行 Schema 元数据；0001 down 删除全部上述表。当前定义 SHALL 以固定哈希保护；后续结构变更从 0002 递增，不提供从 v1.0.0 之前开发期结构升级的工具或承诺。

#### Scenario: 空数据库安装
- **WHEN** 对合法新空 schema 执行 Up
- **THEN** 只执行当前 0001，得到完整八表，version=1/dirty=false，不影响其他 schema

#### Scenario: 非法 schema
- **WHEN** schema 不符合 `^[a-z][a-z0-9_]{0,62}$`
- **THEN** 返回配置错误，不执行数据库变更

#### Scenario: 当前定义固定
- **WHEN** 校验当前工作树 0001 up/down
- **THEN** 与记录的固定哈希一致；不一致时发布检查失败，不能通过改写 0001 采用新结构

### Requirement: 基线预检边界

迁移预检 SHALL 在任何写入前执行且只保留两条规则：`schema_migrations` 标记 dirty 时优先返回 dirty 错误；数据库记录的版本不存在于当前迁移源时返回可用 `errors.Is` 识别的不兼容基线错误。两种拒绝 SHALL 保持 catalog、数据与 version/dirty，释放连接与锁，不 Force、不改版本、不清库。预检 SHALL 不再反查 catalog 识别开发期旧结构；版本 1 的结构等价于当前 0001 由迁移引擎保证。未来增量从 0002 开始，测试 probe 为 latest+1。

#### Scenario: 完整新基线重复执行
- **WHEN** 当前完整 version 1 已安装且含资料及认证数据，再次 Up
- **THEN** catalog、数据、账号原时间、版本和资源 ID 保持，连接及锁释放

#### Scenario: dirty 优先
- **WHEN** 目标 schema 的版本记录带 dirty 标记
- **THEN** 返回 dirty 错误，不执行任何 SQL，不清除 dirty

#### Scenario: 版本超出当前源
- **WHEN** 目标 schema 记录的版本高于当前迁移源的最大版本（例如由更新的二进制迁移过的库被旧二进制接管）
- **THEN** 无写拒绝并返回不兼容基线错误，保留 catalog、数据及 version/dirty

#### Scenario: 未来测试增量
- **WHEN** 测试源对完整当前基线提供 latest+1（当前为 2）的成功或失败 probe
- **THEN** 正常验证未来增量及失败/dirty 阻断，测试 SQL 不进入生产源

#### Scenario: 开发测试清库
- **WHEN** 调用显式危险清库入口并准确确认目标 schema
- **THEN** 只清除该 schema 的认证迁移对象并保留 schema 本身；确认值不一致时在 I/O 前拒绝

## REMOVED Requirements

### Requirement: 包内数据库定义
**Reason**: 由「当前初始定义」取代。原要求以资料单列整合授权、v0.3.0/v0.4.0 pin、流程改名等价证据与冻结源描述结构；这些开发期历史不再作为规格要求。
**Migration**: 无运行时迁移。结构事实完整保留在「当前初始定义」。

### Requirement: 整合基线的兼容边界
**Reason**: 由「基线预检边界」取代。原要求的 catalog 反查与各代旧结构（v0.3.0 v1、b414fef 双快照、缺资料/头像列、撤回 v2、双表）的拒绝场景只对从未被正式使用的开发期数据库有意义；历史合成凭据格式验证随原项目源快照退役，凭证格式由各包单元测试保障。
**Migration**: 无运行时迁移。仍需读取开发期结构的场景不存在；相关 SQL 可从提交历史查阅。

### Requirement: 头像列初始定义限定授权
**Reason**: 已完成的开发期限定授权，头像列是当前 0001 的一部分。
**Migration**: 无。

### Requirement: 旧头像结构无写拒绝
**Reason**: 反查头像列的预检随「基线预检边界」移除。
**Migration**: 无。

### Requirement: Google 身份注释并入当前初始定义
**Reason**: 已完成的开发期限定授权，注释是当前 0001 的一部分。
**Migration**: 无。

### Requirement: 身份类型文本列并入当前初始定义
**Reason**: 已完成的开发期限定授权，列定义由「当前初始定义」描述。
**Migration**: 无。

### Requirement: 整数身份类型结构无写拒绝
**Reason**: 反查身份类型列的预检随「基线预检边界」移除。
**Migration**: 无。
