# Spec Delta

## ADDED Requirements

### Requirement: 服务微博配置与装配
serve SHALL 读取 ACCOUNTKIT_WEIBO_APP_IDS 和 ACCOUNTKIT_WEIBO_API_BASE_URL。配置 SHALL 遵循 weibo-sign-in 的校验要求；应用列表为空不启用。显式启用 WEIBO 而无应用配置时 SHALL 启动失败并指出 ACCOUNTKIT_WEIBO_APP_IDS，不输出配置值。服务 SHALL 复用既有登录和绑定路由，不提供授权发起、回调或取消授权通知入口，也不要求微博 AppSecret。

#### Scenario: 多应用服务
- **WHEN** 配置两个有效微博 AppKey，其他服务配置合法
- **THEN** serve 正常装配两个允许应用，两者的同一 uid 命中同一身份

#### Scenario: 启用但未配置应用
- **WHEN** Enabled 含 WEIBO，而 ACCOUNTKIT_WEIBO_APP_IDS 未设置或为空
- **THEN** 监听前拒绝并指出 ACCOUNTKIT_WEIBO_APP_IDS

#### Scenario: 非法列表或地址
- **WHEN** APP_IDS 有重复项、空项或非数字项，或 API_BASE_URL 为 HTTP 非回环地址
- **THEN** 拒绝启动并指出对应变量名，不输出变量内容

### Requirement: 微博身份策略与迁移隔离
仅启用 WEIBO、Required 显式为空且微博配置完整时，serve SHALL 可在未配置 SMTP 时启动；Required 缺省与非空时 SHALL 保持既有手机号/邮箱补全要求。migrate SHALL 不要求配置微博或访问微博；已提供的微博配置 SHALL 接受静态格式校验，非法时拒绝，不为迁移静默忽略。

#### Scenario: 纯微博服务
- **WHEN** Enabled 为 WEIBO，Required 为 []，应用与其他服务配置有效且未配置 SMTP
- **THEN** serve 可启动并完成纯第三方登录，不需要邮件发送器

#### Scenario: 登录仍需邮箱补全
- **WHEN** Enabled 含 EMAIL、WEIBO，Required 为 EMAIL，新微博身份没有邮箱证明
- **THEN** 按既有 pending 流程补全，不能因微博资料查询成功绕过 Required

#### Scenario: migrate 不需要微博配置
- **WHEN** 运行 migrate，微博变量未设置，身份策略与迁移配置有效
- **THEN** 迁移只按原边界校验，不调用微博

#### Scenario: migrate 遇到非法已配置项
- **WHEN** 运行 migrate，ACCOUNTKIT_WEIBO_APP_IDS 含非数字项
- **THEN** 共享静态配置校验拒绝并指出该配置项
