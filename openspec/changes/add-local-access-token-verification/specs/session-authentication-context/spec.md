## MODIFIED Requirements

### Requirement: 并发撤销和令牌边界

身份变更、重新认证来源替换和会话状态检查 SHALL 遵循一致事务与锁序，不能使已撤销会话重新变为可用。数据库提交后 SHALL 按既有 sid 吊销和异步审计契约处理 access；refresh、宽限刷新及已完成 pending 的结果重放 SHALL 拒绝数据库已撤销会话。Redis 故障时，Authenticate SHALL 保留现有 fail-open 与告警，不宣称所有 access 即时失效，不回滚已成功的身份变更；内省 SHALL 以数据库撤销结果为准，不受 Redis 故障影响。

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
- **THEN** 身份操作保持成功、refresh 被数据库拒绝，Authenticate 沿用已有故障边界并记录告警，内省对该会话返回 active:false

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
- **THEN** 返回401，普通 Authenticate 边界保持；内省对同一令牌返回 active:false

#### Scenario: 未知不是解绑安全
- **WHEN** 当前结构中的会话缺少来源
- **THEN** 返回固定安全500错误，不返回空来源成功，不伪造身份或暴露数据库细节

#### Scenario: 并发替换不返回混合版本
- **WHEN** 查询与来源替换并发
- **THEN** 返回提交前或提交后完整快照，不组合新 auth_time 与旧集合

### Requirement: 来源数据生命周期与兼容

当前结构 SHALL 不接受没有来源的可用会话；旧库 SHALL 按当前迁移边界无写拒绝，不反推或补造来源，不自动退出或转换旧库。使用来源的领域动作 SHALL 将缺失作为内部不变量错误；来源关联 SHALL 随有界会话清理删除，匿名化覆盖全部历史关联并与宿主回调同事务。JWT、refresh、scope 与普通 Authenticate 热路径 SHALL 保持原格式和数据库访问边界；内省只按 sid 读取会话吊销状态，不读取来源。

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
- **THEN** Authenticate 保留 JWT/Redis 路径；内省只读取会话吊销状态；两者都不查询或写入来源
