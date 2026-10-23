# Spec Delta

## Purpose

处理由第三方平台按其用户标识发起的数据删除请求：校验平台签名，清除该第三方身份带来的数据，必要时转入账号注销，并向宿主返回可展示给平台的确认码与处理状态。

## ADDED Requirements

### Requirement: Facebook 签名请求校验
系统 SHALL 提供 Facebook `signed_request` 校验：两段均按无填充 base64url 解码，算法 SHALL 为 HMAC-SHA256，签名 SHALL 以配置的 App Secret 常量时间比较。格式、算法或签名不符 SHALL 返回可区分的签名无效错误，且不改动任何数据。校验通过 SHALL 得到 Facebook 用户 id 与请求签发时间。

#### Scenario: 签名错误
- **WHEN** signed_request 的签名不是以配置的 App Secret 计算的
- **THEN** 返回签名无效错误，不查找或修改身份

#### Scenario: 未启用 Facebook
- **WHEN** 未配置 Facebook App ID 时调用数据删除入口
- **THEN** 返回未启用错误，不修改数据

### Requirement: 处理结果
每次处理 SHALL 同步返回确认码与状态。状态 SHALL 为以下之一：DELETED（该第三方数据已清除或本就不存在）、SCHEDULED（账号已处于注销冷静期，并返回预定匿名化时间）、BLOCKED（未改动数据，需要宿主跟进）。确认码 SHALL 是每次调用唯一的不透明字符串，不包含第三方用户 id。系统 SHALL 不提供 HTTP 路由或状态页，也不持久化请求状态。

#### Scenario: 重复请求
- **WHEN** 同一用户 id 的删除请求在已处理后再次到达
- **THEN** 返回 DELETED 和新的确认码，不报错

#### Scenario: 从未登录过的用户
- **WHEN** 请求中的用户 id 没有任何活动身份
- **THEN** 返回 DELETED，不修改数据

### Requirement: 仍有其他身份时只清除该身份
若去掉该第三方身份后账号仍有至少一个有效的 Enabled 身份，系统 SHALL 不检查 Required、近期认证或会话，直接解除该身份：替换其 subject、清除其提供方元数据与提示信息，并吊销以该身份为认证来源的会话。账号、其他身份及账号资料 SHALL 保持。账号随后不满足 Required 时 SHALL 进入既有补全流程。

#### Scenario: 同时绑定手机
- **WHEN** 账号有有效 PHONE 身份和 FACEBOOK 身份，收到该 FACEBOOK 用户的删除请求
- **THEN** FACEBOOK 身份被解除且库中不再保留其原用户 id，PHONE 身份与账号保持，返回 DELETED

#### Scenario: 剩余身份不满足 Required
- **WHEN** Required 为 PHONE、EMAIL，账号只有 FACEBOOK 与 WECHAT 两个有效身份
- **THEN** 解除 FACEBOOK 身份并返回 DELETED；该账号下次签发令牌时作用域为 user:bind

#### Scenario: 吊销来源会话
- **WHEN** 账号有一个以该 FACEBOOK 身份登录的会话和一个以 PHONE 登录的会话
- **THEN** 前者被吊销，后者保持有效

#### Scenario: 冻结账号仍有其他身份
- **WHEN** 账号处于冻结状态且仍有其他有效身份
- **THEN** 解除该 FACEBOOK 身份，账号保持冻结，返回 DELETED

### Requirement: 唯一身份时转入账号注销
若去掉该第三方身份后账号没有有效的 Enabled 身份，ACTIVE 账号 SHALL 按既有注销规则进入冷静期并返回 SCHEDULED；宿主注销前检查拒绝时 SHALL 整体回滚并返回 BLOCKED。冻结账号 SHALL 返回 BLOCKED 且不改动数据。已在冷静期的账号 SHALL 返回 SCHEDULED 且不改动数据。宿主检查返回其他错误时 SHALL 返回错误，不得表示为 BLOCKED。

#### Scenario: 只有 Facebook 身份
- **WHEN** ACTIVE 账号只有 FACEBOOK 一个有效身份，宿主未阻止注销
- **THEN** 账号进入冷静期、全部会话被吊销，返回 SCHEDULED 和预定匿名化时间

#### Scenario: 宿主阻止注销
- **WHEN** 账号只有 FACEBOOK 身份，宿主注销前检查返回阻止
- **THEN** 返回 BLOCKED，账号、身份、会话均不改动

#### Scenario: 冻结账号只有 Facebook 身份
- **WHEN** 冻结账号只有 FACEBOOK 一个有效身份
- **THEN** 返回 BLOCKED，不注销，不解除身份

### Requirement: 已解除的历史身份同样清除
请求签发之前创建、已被解除但仍保存原第三方用户 id 的同类型身份行 SHALL 在本次处理中被抹除 subject、提供方元数据与提示信息，不论所属账号状态与本次返回状态。该清除 SHALL 不改变账号、其他身份与会话，不产生新的解绑审计。清除失败 SHALL 返回错误，重试 SHALL 幂等。

#### Scenario: 先解绑后请求删除
- **WHEN** 用户已用 PHONE 账号解绑了 FACEBOOK 身份，随后收到该 Facebook 用户的删除请求
- **THEN** 返回 DELETED，库中不再保留该 Facebook 用户 id，PHONE 身份与会话保持

#### Scenario: 被阻止时仍清除历史行
- **WHEN** 账号曾解绑过一次 FACEBOOK 身份后又重新绑定，当前为唯一身份且宿主阻止注销
- **THEN** 返回 BLOCKED，当前活动身份保持，历史解除行的用户 id 被抹除

### Requirement: 判定与执行的一致性
分支判定与对应写入 SHALL 在同一事务内并在账号行锁下完成，与并发的绑定、解绑、注销及登录互不产生不一致的中间状态。身份在请求签发之后才创建的 SHALL 视为与该请求无关，返回 DELETED 且不修改。

#### Scenario: 请求后重新绑定
- **WHEN** 用户在删除请求签发之后用 Facebook 重新登录产生了新身份，随后该旧请求才到达或被重放
- **THEN** 新身份保持，返回 DELETED

#### Scenario: 并发绑定手机
- **WHEN** 删除请求处理与同一账号绑定 PHONE 并发
- **THEN** 结果等同于两者按某一顺序串行执行，不出现账号被注销而新 PHONE 身份仍活动的状态

### Requirement: 数据删除审计
每次处理 SHALL 产生一条数据删除审计事件，其事件 ID 与确认码一致，操作者为服务，记录身份类型、状态与脱敏提示，SHALL 不记录完整第三方用户 id。解除身份、吊销会话与注销 SHALL 同时产生各自既有的审计事件。审计写入失败 SHALL 不改变处理结果。

#### Scenario: 解除身份的审计
- **WHEN** 删除请求导致解除 FACEBOOK 身份
- **THEN** 出现一条状态为 DELETED 的数据删除事件和一条身份解绑事件，均不含完整用户 id

#### Scenario: 被阻止的审计
- **WHEN** 删除请求返回 BLOCKED
- **THEN** 出现一条状态为 BLOCKED 的数据删除事件，不出现身份解绑或注销事件
