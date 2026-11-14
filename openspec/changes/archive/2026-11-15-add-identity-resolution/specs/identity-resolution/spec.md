# Spec Delta

## Purpose

为获授权的业务服务与嵌入式宿主提供通用身份解析能力，通过手机号或邮箱查找当前关联账号及有限基础资料，明确生命周期、查询一致性和隐私边界，由调用方独立决定资料展示与具体业务资格。

## ADDED Requirements

### Requirement: 单条身份解析请求

服务 SHALL 提供 `POST /v1/identities:resolve`，接受 application/json 对象且只含必填字符串 kind、subject；kind SHALL 为 PHONE 或 EMAIL，subject SHALL 按既有手机号默认地区或邮箱规则规范化并精确匹配。SHALL 拒绝批量、第三方类型、空值、未知或重复字段、尾随 JSON 和非法编码。不接受 URL 查询参数承载身份。请求体 SHALL 不超过 4 KiB；授权后错误方法返回 405，错误媒体类型返回 415，超限返回 413，其余输入错误返回 400 INVALID_ARGUMENT。

#### Scenario: 等价标识
- **WHEN** 请求手机号或邮箱与已存身份按现行规则规范化后相同
- **THEN** 解析到同一账号，不引入另一套大小写或地区规则

#### Scenario: 输入非法
- **WHEN** 已授权请求包含未知 kind、null、数组、空 subject、未知或重复字段、多个 JSON 值或任意查询参数
- **THEN** 返回 400 INVALID_ARGUMENT，不执行身份查询，不回显输入

#### Scenario: 协议边界
- **WHEN** 已授权请求使用非 POST 方法、非 JSON 媒体类型，或 POST JSON 请求体超过 4 KiB
- **THEN** 分别返回 405 并带 Allow: POST、415、413，不执行身份查询

### Requirement: 当前身份关联与账号状态

解析 SHALL 只匹配未解绑的当前身份，支持全部已配置摘要密钥版本。关联账号为 ACTIVE、FROZEN、PENDING_DELETION 时 SHALL 返回对应状态；未注册、已解绑或已匿名化且无新关联 SHALL 返回 404 NOT_FOUND。相同标识后来绑定到其他账号时 SHALL 返回当前账号，不返回历史归属。SHALL 不因登录方式当前被禁用或发送器未配置而隐藏仍存在的身份关联。

#### Scenario: 三种可解析账号状态
- **WHEN** 当前身份分别属于 ACTIVE、FROZEN 或 PENDING_DELETION 账号
- **THEN** 返回 200 与该账号真实状态，不判断其能否参与调用方业务

#### Scenario: 解除关联和重新绑定
- **WHEN** 身份已解绑或随账号匿名化，随后没有新绑定或已绑定到另一账号
- **THEN** 前者返回统一 404，后者只返回新账号

#### Scenario: 摘要轮换与禁用登录方式
- **WHEN** 身份使用仍配置的旧摘要密钥，或其 PHONE/EMAIL 登录方式已禁用
- **THEN** 仍可按当前关联解析，无需验证码发送器或第三方提供方

### Requirement: 最小账号资料结果

成功响应 SHALL 恰好包含 user_id、state、display_name、avatar_url；user_id 使用既有 u_ ID，state 使用已有大写枚举字符串。昵称和头像未设置 SHALL 返回空字符串。冻结与注销冷静期内 SHALL 返回保留的昵称与头像。SHALL 不返回身份标识、摘要、密钥版本、身份元数据或扩展资料。该服务授权视图 SHALL 不改变现有公开资料视图的注销隐藏规则。

#### Scenario: 完整和空资料
- **WHEN** 当前账号已设置或未设置昵称与头像
- **THEN** 返回保存值或空字符串，字段均存在；不下载头像或替换为默认头像

#### Scenario: 注销中账号的不同访问面
- **WHEN** 服务持有 identities.resolve 并解析注销冷静期内账号，同时消费者读取其公开资料
- **THEN** resolve 返回保留资料及 PENDING_DELETION；消费者公开资料继续遵循原隐藏规则

### Requirement: 一致快照与只读领域能力

库 SHALL 提供可供宿主复用的身份解析领域能力；嵌入式宿主负责调用授权，服务端 HTTP SHALL 执行独立服务授权。身份关联、账号状态和基础资料 SHALL 来自同一数据库快照。解析 SHALL 不创建账号、登录、绑定、修改资料或更新时间，不生成凭据，不提供后续业务授权保证。跨密钥版本出现多条活跃身份、身份悬空或关联已匿名化账号等不变量异常 SHALL 返回内部错误，不任取一条或伪装未找到。

#### Scenario: 并发解绑或匿名化
- **WHEN** 解析与解绑、换绑、冻结或匿名化并发发生
- **THEN** 结果符合某一已提交一致快照，不拼接旧关联与新账号资料；查询开始前已提交的解绑不返回旧关联

#### Scenario: 数据不变量被破坏
- **WHEN** 多个摘要版本命中多条活跃身份，或活跃身份指向不存在、DELETED 或非法状态的账号
- **THEN** 返回固定 500 INTERNAL 消息并记录脱敏诊断，不泄露内部错误细节

### Requirement: 查询故障分类

成功无匹配 SHALL 与依赖失败区分。数据库不可用或查询超时 SHALL 返回 503 DEPENDENCY_UNAVAILABLE，不返回 404；数据库读取等待 SHALL 不超过 3 秒且支持取消。其余内部错误 SHALL 返回固定 500 INTERNAL 消息。HTTP 业务错误 SHALL 使用既有 apierror 结构，所有响应 SHALL 设置 Cache-Control: no-store。查询 SHALL 不以 Redis 中缓存结果代替数据库当前关联。

#### Scenario: 数据库故障
- **WHEN** 已授权合法请求遇到数据库断开或超过读取期限
- **THEN** 返回 503，不被调用方误认为账号未注册，不回显底层错误

### Requirement: 解析审计与脱敏

通过认证授权且输入合法的解析 SHALL 记录尽力审计，包括服务主体、请求 ID、身份类型和成功、未找到或失败结果；成功时关联目标 user_id。审计 SHALL 不包含 subject、规范化标识、摘要、昵称、头像或凭据，写入失败 SHALL 不改变解析响应。请求与错误日志 SHALL 同样不输出这些数据。认证拒绝 SHALL 沿用服务认证的脱敏观测规则。

#### Scenario: 成功与未找到审计
- **WHEN** 获授权服务的合法查询成功或返回 404
- **THEN** 审计可区分调用服务、请求和结果；只有成功事件关联目标账号，无输入身份及资料副本

#### Scenario: 审计失败
- **WHEN** 查询成功但审计队列满或写入依赖失败
- **THEN** 仍返回原查询结果，并沿用现有审计失败观测，不改成同步强依赖
