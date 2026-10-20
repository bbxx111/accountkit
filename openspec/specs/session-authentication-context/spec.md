# session-authentication-context Specification

## Purpose

建立会话与非空具体认证身份集合之间可更新、可查询的关联，使身份移除能准确撤销相关会话，并通过服务端最新状态展示认证来源与退出提示；明确来源缺失的不变量错误、重新认证、并发、数据清理及既有令牌的兼容边界。

## Requirements

### Requirement: 当前认证依据为具体身份集合

每个新会话 SHALL 保存本次成功认证的非空具体身份集合，不保存来源已知标记。普通登录 SHALL 只记录本次验证的身份；登录补全 SHALL 记录参与该次成功流程且最终归属同一账号的全部已验证身份并去重。未参与认证的已有绑定 SHALL 不自动加入。引用 SHALL 在会话写入同事务内复核存在性、归属、启用与有效状态；客户端 SHALL 不能自行指定来源。

#### Scenario: 普通登录仅依赖登录身份
- **WHEN** 账号绑定微信 A、邮箱 B，通过 A 登录
- **THEN** 新会话集合仅为 A，不自动加入 B，也不写入 known 标记

#### Scenario: 补全关联或注册
- **WHEN** pending 先验证微信 A，再验证邮箱 B，成功关联或注册
- **THEN** 来源包含最终同一账号下 A/B 的实际身份资源，不将其描述为 MFA 要求

#### Scenario: 多个同类型身份
- **WHEN** 账号有两个 EMAIL 身份但只验证其中一个
- **THEN** 集合只指向实际验证身份，不按类型扩大关联

#### Scenario: 提交前身份已解绑
- **WHEN** 登录或补全读取身份后，解绑先提交
- **THEN** 锁后复核拒绝旧归属证明，不创建依赖已移除身份的会话

### Requirement: 重新认证原子替换当前依据

成功重新认证 SHALL 在同事务内用本次验证的单个有效身份整体替换非空来源集合并更新 auth_time，不设置 known 标记。接口、用途、新鲜度窗口、sid 与 refresh 保持原契约。刷新、普通绑定及 BIND 证明 SHALL 不改变来源。失败 SHALL 不改变集合或 auth_time；已撤销或过期会话 SHALL 不被恢复，来源缺失 SHALL 作为内部不变量错误拒绝而非修补。第三方近期认证 SHALL 沿用普通重新登录。

#### Scenario: 从多来源切换为邮箱
- **WHEN** 来源 A/B 的会话通过 B 重新认证
- **THEN** 来源恰为 B、auth_time 更新，sid/refresh 保持，不追溯修改旧 JWT

#### Scenario: 绑定或刷新不提升为来源
- **WHEN** 来源 A 的会话新增绑定 B、正常刷新或宽限重试
- **THEN** 来源仍为 A，宽限重试返回原 pair

#### Scenario: 重新认证失败
- **WHEN** 凭证错误、归属失效、会话过期、来源缺失或事务失败
- **THEN** 原集合和 auth_time 保持，已消费验证码不恢复

### Requirement: 解绑联动撤销相关及未知会话

解绑 SHALL 校验完整 user scope、配置要求的新鲜度、ACTIVE 账号、有效当前会话及解绑前后身份完整性，Go/HTTP 一致。关闭新鲜度检查 SHALL 不跳过其他校验。成功解绑 SHALL 同事务软删除身份并撤销同账号尚未吊销且来源命中该具体身份的会话，包含当前及 refresh 已过期未清理的会话；命中任一来源即撤销整体，未命中者保留。涉及的未吊销会话来源缺失 SHALL 拒绝整个操作为内部不变量错误，不当作 unknown 正常态。

#### Scenario: 任一来源命中
- **WHEN** S1 来源 A/B，S2 来源 B，解绑 A
- **THEN** S1 撤销且 S2 保留，S1 为当前会话时也一样

#### Scenario: 未知存量会话
- **WHEN** 同账号未吊销 S0 没有来源，另有 S2 来源 B，尝试解绑 A
- **THEN** 报内部不变量错误，身份与会话都不变；不自动撤销 S0 或推断来源

#### Scenario: 解绑当前认证身份
- **WHEN** 当前会话来源命中目标且剩余身份满足策略
- **THEN** 返回原 204 成功并撤销当前会话，不伪装为认证错误

#### Scenario: 失败保持原状态
- **WHEN** 完整性、scope、新鲜度、会话或来源不变量失败，或事务回滚
- **THEN** 不删除身份、不撤销会话、不产生成功解绑审计

#### Scenario: 自然到期但 access 尚有效
- **WHEN** 来源命中的会话 refresh 已过期但 access 尚有效
- **THEN** 仍显式撤销并进入既有吊销流程，不因活跃列表过滤漏掉

### Requirement: 并发撤销和令牌边界

身份变更、重新认证来源替换和会话状态检查 SHALL 遵循一致事务与锁序，不能使已撤销会话重新变为可用。数据库提交后 SHALL 按既有 sid 吊销和异步审计契约处理 access；refresh、宽限刷新及已完成 pending 的结果重放 SHALL 拒绝数据库已撤销会话。Redis 故障 SHALL 保留现有 fail-open 与告警，不宣称所有 access 即时失效，不回滚已成功的身份变更。

#### Scenario: 重新认证先提交
- **WHEN** 来源 A 的当前会话先通过 B 重新认证提交，随后解绑 A
- **THEN** 解绑按集合 B 计算，该会话保留

#### Scenario: 解绑先提交
- **WHEN** 解绑 A 先提交并撤销来源 A 的会话，随后该会话重新认证
- **THEN** 重新认证锁后返回认证失效，不通过更新集合恢复会话

#### Scenario: 缓存仍有旧结果
- **WHEN** 身份移除已撤销会话，Redis 中仍有旧 refresh 宽限结果或流程仍有完成结果
- **THEN** 后续刷新或完成重试拒绝返回可继续使用的旧 pair，不重新建立会话

#### Scenario: 吊销缓存故障
- **WHEN** 数据库撤销已提交但 Redis 写入失败
- **THEN** 身份操作保持成功、refresh 被数据库拒绝，access/内省沿用已有故障边界并记录告警

### Requirement: 当前会话详情查询

系统 SHALL 保留相对 GET /users/me/sessions/current，以已验证账号/sid 查询，支持 user、user:bind，拒绝 user:undelete 为 403 INSUFFICIENT_SCOPE。响应 SHALL 返回原真实会话、设备和时间字段、is_current=true、数据库最新 RFC3339 auth_time 与按身份 ID 稳定排序的非空 name/kind 摘要，不输出 authentication_identities_known。摘要 SHALL 不泄露目标、密文、摘要、subject 或凭据。查询 SHALL 保持一致快照且 no-store；会话无效返回带 Bearer challenge 的401，依赖不可用503，可选能力缺失503 SESSION_QUERY_NOT_CONFIGURED，来源缺失为安全的500内部错误。

#### Scenario: 根据当前会话展示身份类型
- **WHEN** 会话来源含 WECHAT A 和 EMAIL B，查询当前会话
- **THEN** 返回两个真实资源名与类型，不输出 known 字段

#### Scenario: 查询重新认证后的状态
- **WHEN** 重新认证已提交，客户端仍持有较早有效 access
- **THEN** 返回最新来源和 auth_time，不改写 JWT 或赋予旧 JWT 新鲜证明

#### Scenario: Redis 故障且数据库会话已撤销
- **WHEN** Bearer 因原 fail-open 放行，详情发现数据库已撤销
- **THEN** 返回401，普通 Authenticate/内省边界保持

#### Scenario: 未知不是解绑安全
- **WHEN** 当前结构中的会话缺少来源
- **THEN** 返回固定安全500错误，不返回空来源成功，不伪造身份或暴露数据库细节

#### Scenario: 并发替换不返回混合版本
- **WHEN** 查询与来源替换并发
- **THEN** 返回提交前或提交后完整快照，不组合新 auth_time 与旧集合

### Requirement: 来源数据生命周期与兼容

当前结构 SHALL 不接受没有来源的可用会话；旧库 SHALL 按当前迁移边界无写拒绝，不反推或补造来源，不自动退出或转换旧库。使用来源的领域动作 SHALL 将缺失作为内部不变量错误；来源关联 SHALL 随有界会话清理删除，匿名化覆盖全部历史关联并与宿主回调同事务。JWT、refresh、scope 与普通鉴权热路径 SHALL 保持原格式和数据库访问边界。

#### Scenario: 非空升级继续使用
- **WHEN** 旧 v0.3.0 v1 或前一整合 v1 请求使用新定义
- **THEN** 迁移拒绝且原记录、token 数据与版本保持，不建立 unknown 兼容态；保留数据需独立迁移方案

#### Scenario: 重新认证建立来源
- **WHEN** 当前库中人为损坏的会话没有来源，再尝试重新认证
- **THEN** 拒绝为内部不变量错误，不用重新认证隐式修补；正常新登录仍原子建立非空来源

#### Scenario: 清理和匿名化回滚
- **WHEN** 会话到清理边界或账号匿名化
- **THEN** 来源不留孤儿，宿主回调失败时清理整体回滚

#### Scenario: 普通鉴权不补查来源
- **WHEN** 宿主调用 Authenticate 或远程内省
- **THEN** 保留 JWT/Redis 路径，不新增来源数据库查询或写入
