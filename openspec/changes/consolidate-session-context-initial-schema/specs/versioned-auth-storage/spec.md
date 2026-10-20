## MODIFIED Requirements

### Requirement: 包内数据库定义

包 SHALL 携带用户、身份、会话、十列 UUIDv7 审计、sign_in_completion_flow、会话认证关联及客户端快照的完整 0001 初始定义，目标 schema SHALL 可配置。用户此次明确授权将未发布会话 0002 合入当前 0001，初始化 SHALL 为 version=1，生产目录 SHALL 不再携带该 0002；后续真实增量 SHALL 从 0002 开始。新增引用 SHALL 不使用外键或级联删除。v0.1.0/v0.2.0 的固定源、v0.3.0 精确标签提交及历史 SQL SHALL 独立固定并保持原字节，当前整合初始 SQL SHALL 使用新的固定哈希保护，不提供跳过历史检查开关。

#### Scenario: 空数据库安装
- **WHEN** 在合法新空 schema 执行迁移
- **THEN** 仅执行完整 0001 建立七张业务表，version=1 且 dirty=false，不改变其他 schema

#### Scenario: 非法 schema
- **WHEN** schema 名不符合 ^[a-z][a-z0-9_]{0,62}$
- **THEN** 返回配置错误，不执行由该名称组成的数据库变更

#### Scenario: 已发布初始定义保持
- **WHEN** 检查 v0.3.0 tag 和当前初始 SQL
- **THEN** tag 中的已发布定义和精确提交保持原样，当前定义仅按本次明确授权整合会话结构并独立固定新哈希，后续变更使用递增迁移

#### Scenario: 直接定义仅调整名称
- **WHEN** 追溯 v0.3.0 发布前的流程名称调整
- **THEN** 历史 catalog 对照按授权名称映射并保留原列、主键、CHECK 与索引；当前新加入的会话结构另按本轮整合前后完整 catalog 等价核验，不将历史改名授权扩展为任意重写

#### Scenario: 整合结构等价
- **WHEN** 比较当前 0001 与整合前 0001+0002 的 catalog
- **THEN** 所有列及其顺序、约束、默认值和索引保持相同，标识符符合命名及长度约束

#### Scenario: 历史与当前分别固定
- **WHEN** 检查完整 Git 历史和可达正式标签
- **THEN** 三代基线独立验证，拒绝移动历史标签、改写历史 SQL、改写当前初始定义或未来正式版本复用旧初始 SQL，非初始增量仍严格递增且不可变

### Requirement: 整合基线的兼容边界

迁移 SHALL 只接受完整当前初始结构及其合法后续递增版本，预检 SHALL 要求完整 sign_in_completion_flow、无 identity_flow、十列 UUID 审计及完整会话上下文；旧 v0.3.0 v1、旧流程/审计结构和已撤回 v2 SHALL 返回明确不兼容错误。拒绝 SHALL 保留数据、catalog 和 version/dirty，dirty SHALL 优先阻断；不得自动重放 0001、改名、降版本、Force、转换或清库。

#### Scenario: 旧已发布会话结构
- **WHEN** 旧 v0.3.0 version=1 缺少会话上下文，含或不含业务数据
- **THEN** Up 拒绝且数据、catalog 和版本不变，不执行原 0002

#### Scenario: 旧初始库版本同为一
- **WHEN** 旧 version=1 仍使用 identity_flow、缺少完整流程、旧审计或会话上下文
- **THEN** 明确拒绝，不重放 0001，保留原记录、结构与版本

#### Scenario: 旧开发增量库
- **WHEN** 连接已撤回旧 version=2，包括流程改名或原会话 0002 产生的实例
- **THEN** 明确拒绝，不降版本、清库或自动重放 0001

#### Scenario: 双表并存
- **WHEN** 同时存在 sign_in_completion_flow 和 identity_flow
- **THEN** 明确拒绝，保留数据、结构与版本

#### Scenario: 撤回会话增量
- **WHEN** 已执行本分支原会话 0002 的完整 version=2 实例连接当前生产源
- **THEN** Up 拒绝且数据、catalog 和版本不变，不按结构相似把它降为 version=1

#### Scenario: 完整新基线重复执行
- **WHEN** 当前完整 version=1 含账号、身份、会话、来源关联和快照，再次 Up
- **THEN** 成功且数据、结构及版本保持，不重新创建或推断来源

#### Scenario: 新基线重复执行
- **WHEN** 完整当前初始结构及合法后续递增版本已安装且包含数据，再次迁移
- **THEN** 结构、版本、数据和资源 ID 保持，不影响其他 schema

#### Scenario: 历史数据格式验证
- **WHEN** 将固定历史合成账号、身份密文和令牌直接装载到当前结构，并明确映射旧审计记录
- **THEN** 非审计 ID、密文、摘要与有效 access/refresh 格式保持，审计映射保留 legacy_event_id；不把格式验证称为旧库原地升级，不修改冻结源

#### Scenario: 正式会话结构递增升级
- **WHEN** 旧 v0.3.0 v1 请求按已撤回会话 0002 升级到当前结构
- **THEN** 明确拒绝并保留旧库，原递增路径已由本轮明确授权撤回，数据保留需求须另行设计迁移

#### Scenario: 版本同号但结构不同
- **WHEN** 对比同为 version=1 的旧 v0.3.0 和当前完整七表结构
- **THEN** 仅接受完整当前结构，旧结构拒绝且数据与版本不变，不添加 Force 或跳过检查开关

#### Scenario: 历史格式与失败清理
- **WHEN** 装载合成历史凭据或对旧结构重复尝试迁移
- **THEN** 格式验证不被描述为原地升级，拒绝后连接和锁释放，dirty 仍保持原优先级；数据保留需求须另行制定迁移方案
