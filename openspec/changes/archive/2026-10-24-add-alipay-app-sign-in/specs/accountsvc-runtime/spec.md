# Spec Delta

## ADDED Requirements

### Requirement: 服务支付宝配置与装配
serve SHALL 读取 ACCOUNTKIT_ALIPAY_APP_IDS 及每个 AppID 的 ACCOUNTKIT_ALIPAY_<APPID>_PRIVATE_KEY / _PUBLIC_KEY，和 ACCOUNTKIT_ALIPAY_SUBJECT_MODE、ACCOUNTKIT_ALIPAY_GATEWAY_URL。配置 SHALL 遵循 alipay-sign-in 的校验要求；模式缺省 UNIONID，应用列表为空不启用。显式启用 ALIPAY 而无应用配置时 SHALL 启动失败并指出 ACCOUNTKIT_ALIPAY_APP_IDS，缺少密钥时指出对应变量名，不输出值。服务 SHALL 复用既有登录和绑定路由，不提供 authInfo 或授权回调入口。

#### Scenario: 多应用服务
- **WHEN** 配置两个有效 AppID 及各自密钥，其他服务配置合法
- **THEN** serve 正常装配两个允许应用，均使用实例固定身份模式

#### Scenario: 应用缺少公钥
- **WHEN** APP_IDS 含某 AppID，但对应 PUBLIC_KEY 未设置
- **THEN** 监听前拒绝并指出该变量名，不输出私钥或其他配置内容

#### Scenario: 非法列表和多行 PEM
- **WHEN** APP_IDS 有重复项或空项，或者密钥内容不是可解析 PEM
- **THEN** 拒绝启动；合法包含真实换行的 PEM 可读取，不将冒号、逗号或文本反斜杠转义作为密钥分隔协议

#### Scenario: 无授权发起端点
- **WHEN** App 请求支付宝登录
- **THEN** 按既有第三方登录协议提交 code，不要求调用新的服务端授权发起或签名端点

### Requirement: 支付宝身份策略与迁移隔离
仅启用 ALIPAY、Required 显式为空且支付宝配置完整时，serve SHALL 可在未配置 SMTP 时启动；Required 缺省与非空时 SHALL 保持既有手机号/邮箱补全要求。migrate SHALL 不要求配置支付宝或访问支付宝；已提供的支付宝配置 SHALL 与其他库配置一样接受静态格式校验，非法时拒绝，不为迁移静默忽略。身份策略语法/集合校验及迁移安全检查 SHALL 保持。

#### Scenario: 纯支付宝服务
- **WHEN** Enabled 为 ALIPAY，Required 为 []，应用与其他服务配置有效且未配置 SMTP
- **THEN** serve 可启动并完成纯第三方登录，不需要邮件发送器

#### Scenario: 登录仍需邮箱补全
- **WHEN** Enabled 含 EMAIL、ALIPAY，Required 为 EMAIL，新支付宝身份没有邮箱证明
- **THEN** 按既有 pending 流程补全，不能因支付宝资料查询成功绕过 Required

#### Scenario: migrate 不需要支付宝配置
- **WHEN** 运行 migrate，支付宝变量未设置，身份策略语法和集合合法且迁移配置有效
- **THEN** 迁移只按原边界校验，不要求支付宝应用密钥，不调用上游

#### Scenario: migrate 遇到非法已配置密钥
- **WHEN** 运行 migrate，明确列出支付宝 AppID 但密钥缺失或格式非法
- **THEN** 共享静态配置校验拒绝并指出配置项，与已提供但非法的其他 IdP 配置保持一致
