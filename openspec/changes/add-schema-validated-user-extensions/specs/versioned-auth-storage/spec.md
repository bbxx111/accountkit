# Spec Delta

## MODIFIED Requirements

### Requirement: 包内数据库定义

包 SHALL 携带 v0.4.0 已发布的完整 `0001` 及后续递增迁移，schema 可配置。本次 SHALL 修订尚未发布的生产 `0002_add_user_extensions`，保留文件名，向 `user_account` 增加 profile_attributes JSONB、Schema 版本、指纹及独立更新时间四列，新安装依次执行至 version 2。`0001`、其固定哈希、冻结源和全部已发布标签 SHALL 保持。新增存储 SHALL 不增加外键、级联、宿主业务列或通用 JSON 查询索引，JSON Schema 与属性业务校验在 Go 执行，数据库保留必要基础结构约束。

#### Scenario: 空数据库安装
- **WHEN** 对合法新空 schema 执行迁移
- **THEN** 依次执行原样 `0001` 和用户资料属性 `0002`，仍为七张业务表，version=2/dirty=false；旧表、单份客户端描述及来源结构完整，不影响其他 schema

#### Scenario: 非法 schema
- **WHEN** schema 不符合 `^[a-z][a-z0-9_]{0,62}$`
- **THEN** 返回配置错误，不执行数据库变更

#### Scenario: 已发布初始定义保持
- **WHEN** 检查 v0.3.0、v0.4.0 tag 与当前 `0001`
- **THEN** 分别保持已发布提交及 SQL；当前 `0001` 与 v0.4.0 字节相同，不再引用发布前简化授权改写初始定义

#### Scenario: 直接定义仅调整名称
- **WHEN** 追溯 v0.3.0 发布前流程改名
- **THEN** 历史名称映射和 catalog 等价证据保持，当前用户资料属性通过新迁移实现，不改写旧记录

#### Scenario: 整合结构等价
- **WHEN** 对比 v0.4.0 version 1 与本次 version 2
- **THEN** 仅新增本次用户资料属性结构及其基础约束，原表列、索引、来源和客户端描述结构保持；此前 b414fef 会话简化的历史差异仍单独追溯

#### Scenario: 历史与当前分别固定
- **WHEN** 校验完整 Git 历史及全部可达正式 tag
- **THEN** 固定源、v0.3.0、v0.4.0 及各已发布增量独立核验，移动标签或改写历史 SQL 均失败，不以本次新增迁移更新旧哈希

#### Scenario: 既有账号的初始扩展
- **WHEN** 非空 v0.4.0 数据库完成 `0002`
- **THEN** 原账号、身份和会话数据及原创建/更新时间保持，资料属性为 `{}`，Schema 版本、指纹与独立更新时间为 NULL

### Requirement: 整合基线的兼容边界

迁移 SHALL 接受完整 v0.4.0 初始结构及与当前生产迁移对应的合法后续结构。本次合法 version 2 SHALL 同时具备完整 v0.4.0 基础结构及用户资料属性列、默认值和基础约束；所有已撤回历史 version 2、旧 v0.3.0 v1、b414fef 双快照 v1、旧流程及审计结构继续拒绝。预检 SHALL 在任何写入前完成，dirty 优先；拒绝保留数据、catalog、version/dirty，不重放 `0001`、Force、自动转换、改版本或清库。

#### Scenario: 旧已发布会话结构
- **WHEN** 旧 v0.3.0 version 1 缺少完整会话上下文，无论是否非空
- **THEN** Up 无写拒绝，不把当前新增 `0002` 当作旧库会话升级脚本

#### Scenario: 旧初始库版本同为一
- **WHEN** 旧 version 1 有旧流程名、审计或会话结构
- **THEN** 明确拒绝并保留记录、结构和版本，不重放 `0001`

#### Scenario: 旧开发增量库
- **WHEN** 连接已撤回的历史 version 2，包括流程改名或原会话 `0002`
- **THEN** 即使迁移源现在存在同号 `0002`，仍因结构不兼容拒绝，不降版本、清库或重放

#### Scenario: 双表并存
- **WHEN** sign_in_completion_flow 和 identity_flow 同时存在
- **THEN** 拒绝并保留数据、结构及版本

#### Scenario: 撤回会话增量
- **WHEN** 原会话 `0002` 的 version 2 连接当前生产源
- **THEN** 无写拒绝，不因同号版本或部分相似结构将其接管为本次用户资料属性 version 2

#### Scenario: 完整新基线重复执行
- **WHEN** 完整 v0.4.0 version 1 包含账号、身份、来源及单份描述，再次执行本次 Up
- **THEN** 只执行未应用的用户资料属性迁移至 version 2，不重建基础结构或推断来源

#### Scenario: 新基线重复执行
- **WHEN** 本次合法 version 2 已安装且非空
- **THEN** 重复迁移保持结构、版本、数据和资源 ID，不影响其他 schema

#### Scenario: 历史数据格式验证
- **WHEN** 固定历史合成凭据装载到当前结构并显式建立合法来源、映射旧审计
- **THEN** 非审计 ID、密文/摘要和 token 格式保持，审计 legacy_event_id 可追溯；不把合成夹具补关联称为生产自动迁移，不改冻结源

#### Scenario: 正式会话结构递增升级
- **WHEN** 旧 v0.3.0 v1 请求沿已撤回会话 `0002` 使用新结构
- **THEN** 拒绝且保留旧库，保留数据需求仍须另行设计迁移

#### Scenario: 版本同号但结构不同
- **WHEN** 对比旧 v0.3.0 v1、b414fef 双快照 v1、v0.4.0 v1 以及不同历史 version 2
- **THEN** 仅 v0.4.0 v1 可进入本次升级，只有本次完整扩展 version 2 可重复执行，其他同号库拒绝且数据与版本保持

#### Scenario: 历史格式与失败清理
- **WHEN** 装载合成历史凭据或重复拒绝旧结构
- **THEN** 正确区分格式验证和升级，连接及锁释放且 dirty 优先；来源缺失仍按既有契约拒绝

#### Scenario: 前一整合基线拒绝
- **WHEN** b414fef v1 含 known 标记、snapshot_kind 或旧平坦快照列
- **THEN** 空或非空均无写拒绝，重复拒绝仍保持 catalog、数据及 version/dirty，不静默删列或选 LATEST 转换

#### Scenario: 不完整扩展结构不能冒充已升级
- **WHEN** version 2 缺少扩展列或列类型、默认值、非空与基础约束不符合本次定义
- **THEN** 预检无写拒绝，不把无待执行 SQL 当作升级成功

#### Scenario: version 一存在手工扩展残留
- **WHEN** version 1 已存在本次扩展的部分或全部列
- **THEN** 预检拒绝不一致状态，不通过 IF NOT EXISTS 或手工改版本接管

#### Scenario: 未发布临时五列结构
- **WHEN** 连接 54afa4b 临时实现的五列 version 2（含 extensions_revision）
- **THEN** 无写拒绝并保留 catalog、数据及 version/dirty，不自动转换、Force、清库或改版本；本轮只修订未发布 0002，文件名保持，0001 与历史不变

#### Scenario: 四列资料属性结构
- **WHEN** 核对本轮合法 version 2
- **THEN** 具有 profile_attributes、profile_attributes_schema_version、profile_attributes_schema_fingerprint、profile_attributes_update_time，保留 Schema 版本、指纹及独立更新时间，不包含 revision 列
