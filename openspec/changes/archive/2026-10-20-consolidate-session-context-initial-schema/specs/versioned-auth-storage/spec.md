<!-- 同步维护：重叠条目已按后继 simplify-session-context 的已实现最终定义重基；原整合实施记录保留。 -->

## MODIFIED Requirements

### Requirement: 包内数据库定义

包 SHALL 携带用户、身份、会话、十列UUIDv7审计、sign_in_completion_flow、认证关联及单份客户端JSONB描述的完整0001，schema可配置。用户本轮明确授权在前一整合v1上删除known标记和双快照结构；初始化 SHALL 为version=1，不新增生产0002，后续真实增量从0002开始。描述表 SHALL 以session_id为唯一主键，client_info JSONB NOT NULL，无外键/级联删除，只保留基本结构约束。三代历史 SHALL 独立固定：冻结源及v0.1/v0.2、v0.3.0精确标签/SQL、当前定义；只更新当前哈希，不提供跳过历史检查开关。

#### Scenario: 空数据库安装
- **WHEN** 对合法新空schema执行迁移
- **THEN** 仅完整0001建立七张业务表，version=1/dirty=false，单份描述且无known/snapshot_kind，不影响其他schema

#### Scenario: 非法 schema
- **WHEN** schema不符合 ^[a-z][a-z0-9_]{0,62}$
- **THEN** 返回配置错误，不执行数据库变更

#### Scenario: 已发布初始定义保持
- **WHEN** 检查v0.3.0 tag与当前0001
- **THEN** 已发布提交/SQL保持，当前仅按本轮明确批准简化并固定新哈希，不把授权扩展为日常历史改写

#### Scenario: 直接定义仅调整名称
- **WHEN** 追溯v0.3.0发布前流程改名
- **THEN** 历史名称映射/catalog等价证据保持；当前会话简化另按本变更差异核验，不改写旧记录

#### Scenario: 整合结构等价
- **WHEN** 比较当前0001与前一b414fef整合v1
- **THEN** 仅删除known、双快照和平坦描述结构并改为单行JSONB及必要关联查询；其他业务结构、数据格式和索引职责保持，不再声称两份catalog完全相同

#### Scenario: 历史与当前分别固定
- **WHEN** 校验完整Git历史及全部可达正式tag
- **THEN** 三代校验保持，移动标签、错误历史字节或未来发布复用旧初始结构均拒绝；当前授权修改有新固定哈希

### Requirement: 整合基线的兼容边界

迁移 SHALL 只接受完整当前初始结构及合法后续增量；预检要求完整sign_in_completion_flow、无identity_flow、十列UUID审计、无known标记及单行JSONB客户端结构。旧v0.3.0 v1、前一b414fef标记+双快照v1、旧流程/审计与撤回v2 SHALL 明确拒绝。拒绝 SHALL 保留数据、catalog及version/dirty，dirty优先；不重放0001、自动重命名、降版本、Force、转换或清库。

#### Scenario: 旧已发布会话结构
- **WHEN** 旧v0.3.0 version=1缺少完整会话上下文，无论是否非空
- **THEN** Up无写拒绝，不执行撤回0002

#### Scenario: 旧初始库版本同为一
- **WHEN** 旧version=1有旧流程名、审计或会话结构
- **THEN** 明确拒绝并保留原记录/结构/版本，不重放0001

#### Scenario: 旧开发增量库
- **WHEN** 连接已撤回version=2，包括流程改名或原会话0002
- **THEN** 拒绝，不降版本、清库或自动重放

#### Scenario: 双表并存
- **WHEN** sign_in_completion_flow和identity_flow同时存在
- **THEN** 拒绝并保留数据/结构/版本

#### Scenario: 撤回会话增量
- **WHEN** 完整原会话0002的version=2连接当前生产源
- **THEN** 无写拒绝，不因结构相似接管为version=1

#### Scenario: 完整新基线重复执行
- **WHEN** 简化后的完整version=1含账号、身份、来源与唯一描述，再次Up
- **THEN** 数据/catalog/版本保持，不重建或推断来源

#### Scenario: 新基线重复执行
- **WHEN** 当前初始定义及合法递增已安装且非空
- **THEN** 重复迁移保持结构、版本、数据与资源ID，不影响其他schema

#### Scenario: 历史数据格式验证
- **WHEN** 固定历史合成凭据装载到当前结构并显式建立合法来源、映射旧审计
- **THEN** 非审计ID、密文/摘要与token格式保持，审计legacy_event_id可追溯；不把合成夹具补关联称为生产自动迁移，不改冻结源

#### Scenario: 正式会话结构递增升级
- **WHEN** 旧v0.3.0 v1请求沿已撤回0002使用新结构
- **THEN** 拒绝且保留旧库，保留数据需求必须另行设计迁移

#### Scenario: 版本同号但结构不同
- **WHEN** 对比旧v0.3.0 v1、b414fef双快照v1与本次简化v1
- **THEN** 仅接受本次完整结构，拒绝其他同号库且数据/版本不变，不使用Force

#### Scenario: 历史格式与失败清理
- **WHEN** 装载合成历史凭据或重复拒绝旧结构
- **THEN** 正确区分格式验证和升级，连接/锁释放且dirty优先；来源缺失在使用时作为异常拒绝

#### Scenario: 前一整合基线拒绝
- **WHEN** 前一b414fef v1含known标记、snapshot_kind或旧平坦快照列
- **THEN** 空/非空均无写拒绝，重复拒绝仍保持catalog、数据、version/dirty；不静默删列或选LATEST转换
