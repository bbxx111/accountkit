# wechat-sign-in Specification

## Purpose

定义微信 OAuth 与小程序两类服务端凭证验证、实例级固定身份识别模式及元数据持久化，使部署可以选择跨应用 UnionID 或应用内 OpenID，同时避免隐式身份迁移与账号合并。

## Requirements

### Requirement: 两组应用和全局标识模式
系统 SHALL 提供 WECHAT_APPS、WECHAT_MINI_PROGRAM_APPS 两组 `appid:secret,...` 配置和全局 WECHAT_SUBJECT_MODE。模式缺省 SHALL 为 UNIONID，显式值 SHALL 仅接受 UNIONID 或 OPENID；非法值、空凭据、任一组内或跨组重复 AppID SHALL 在启动前拒绝且错误不含秘密。任一组非空 SHALL 提供 WECHAT 验证能力，只有小程序的 accountsvc SHALL 能按身份策略启动。

#### Scenario: 仅小程序
- **WHEN** 仅配置小程序凭据、Enabled 为 WECHAT、Required 为空
- **THEN** 库和服务可以装配微信登录能力

#### Scenario: 配置拒绝
- **WHEN** 两组使用同一 AppID 或模式为 AUTO、空白或小写 openid
- **THEN** 启动失败并指出配置项，错误不包含 Secret

### Requirement: 根据服务端配置分派验证接口
系统 SHALL 根据 AppID 所属配置组调用 OAuth `/sns/oauth2/access_token`（code 参数）或小程序 `/sns/jscode2session`（js_code 参数），请求均包含对应 appid、secret 和 authorization_code grant_type。客户端 SHALL 继续提交 `kind=WECHAT` 和 credential 中的 app_id/code，不能指定接口或模式。未知 AppID SHALL 在外部请求前拒绝；不得跟随重定向泄露凭据。

#### Scenario: 小程序验证
- **WHEN** 凭证 app_id 属于小程序组
- **THEN** 仅调用 jscode2session 并传 js_code，不调用 OAuth 或额外用户资料接口

### Requirement: 固定模式决定唯一主标识
UNIONID 模式 SHALL 使用 `unionid:<unionid>`，OPENID 模式 SHALL 使用 `openid:<appid>:<openid>`。模式所需标识缺失 SHALL 返回配置/提供方响应错误并拒绝认证；UNIONID 缺失沿用 500。其他验证错误 SHALL 保留既有错误分类。系统 SHALL 不自动升级、降级、通过元数据查找登录、转换历史 subject 或合并账号，已有唯一索引 SHALL 继续约束 kind/subject。

#### Scenario: OpenID 后续取得 UnionID
- **WHEN** OPENID 模式首次只取得 OpenID，后续响应同时含相同 OpenID 和 UnionID
- **THEN** 登录命中同一身份和账号，主标识与身份 ID 不变，只补充元数据

#### Scenario: 跨应用 OpenID
- **WHEN** 两个 AppID 返回相同 OpenID 文本
- **THEN** OPENID 主标识不同，不能自动视为同一身份

#### Scenario: UnionID 不能降级
- **WHEN** UNIONID 模式响应只有有效 OpenID
- **THEN** 登录失败，不创建 OpenID 身份

### Requirement: 白名单元数据和合并规则
系统 SHALL 将非空已验证 UnionID 保存为 provider_meta.unionid，将 OpenID 按 AppID 保存在 provider_meta.openids；现有 profile 规则 SHALL 保留。缺失字段 SHALL 不清除旧值；同一 AppID 的 OpenID 或既有非空 UnionID 发生矛盾 SHALL 拒绝写入并回滚。code、access_token、refresh_token、session_key、Secret、任意原始响应 SHALL 不进入元数据、日志、审计或公开响应。绑定、直接注册和 pending 完成都 SHALL 保存相同白名单元数据。

#### Scenario: 秘密不持久化
- **WHEN** 微信响应含 unionid、openid、access_token、refresh_token 或 session_key
- **THEN** 持久化仅保留身份标识，响应和日志不暴露秘密

#### Scenario: 元数据冲突
- **WHEN** OPENID 身份已有 UnionID，新的可信响应为不同 UnionID
- **THEN** 拒绝本次操作，不覆盖旧值、不创建会话

### Requirement: 配置指纹与文档契约
登录补全绑定的微信配置指纹 SHALL 包含两组 AppID 的归属、全局模式和 API 地址，SHALL 不包含 Secret 且不受列表顺序影响。文档 SHALL 说明前缀 subject 与历史裸 UnionID 不兼容、模式变更可能产生不同身份，且没有旧数据迁移工具。

#### Scenario: pending 期间切换模式
- **WHEN** 未完成流程产生后部署切换模式或 AppID 所属验证组
- **THEN** 原流程被拒绝并要求重新开始
