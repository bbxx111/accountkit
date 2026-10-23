# Spec Delta

## ADDED Requirements

### Requirement: 服务 QQ 配置与装配
serve SHALL 读取 ACCOUNTKIT_QQ_APP_IDS、ACCOUNTKIT_QQ_SUBJECT_MODE 和 ACCOUNTKIT_QQ_API_BASE_URL。配置 SHALL 遵循 qq-sign-in 的校验要求；模式缺省 UNIONID，应用列表为空不启用。显式启用 QQ 而无应用配置时 SHALL 启动失败并指出 ACCOUNTKIT_QQ_APP_IDS，不输出配置值。服务 SHALL 复用既有登录和绑定路由，不提供授权发起或回调入口，也不要求 QQ AppKey。

#### Scenario: 多应用服务
- **WHEN** 配置两个有效 QQ AppID，其他服务配置合法
- **THEN** serve 正常装配两个允许应用，均使用实例固定身份模式

#### Scenario: 启用但未配置应用
- **WHEN** Enabled 含 QQ，而 ACCOUNTKIT_QQ_APP_IDS 未设置或为空
- **THEN** 监听前拒绝并指出 ACCOUNTKIT_QQ_APP_IDS

#### Scenario: 非法列表或模式
- **WHEN** APP_IDS 有重复项、空项或非数字项，或 SUBJECT_MODE 为 AUTO
- **THEN** 拒绝启动并指出对应变量名，不输出变量内容

### Requirement: QQ 身份策略与迁移隔离
仅启用 QQ、Required 显式为空且 QQ 配置完整时，serve SHALL 可在未配置 SMTP 时启动；Required 缺省与非空时 SHALL 保持既有手机号/邮箱补全要求。migrate SHALL 不要求配置 QQ 或访问 QQ；已提供的 QQ 配置 SHALL 接受静态格式校验，非法时拒绝，不为迁移静默忽略。

#### Scenario: 纯 QQ 服务
- **WHEN** Enabled 为 QQ，Required 为 []，应用与其他服务配置有效且未配置 SMTP
- **THEN** serve 可启动并完成纯第三方登录，不需要邮件发送器

#### Scenario: 登录仍需邮箱补全
- **WHEN** Enabled 含 EMAIL、QQ，Required 为 EMAIL，新 QQ 身份没有邮箱证明
- **THEN** 按既有 pending 流程补全，不能因 QQ 资料查询成功绕过 Required

#### Scenario: migrate 不需要 QQ 配置
- **WHEN** 运行 migrate，QQ 变量未设置，身份策略与迁移配置有效
- **THEN** 迁移只按原边界校验，不调用 QQ

#### Scenario: migrate 遇到非法已配置项
- **WHEN** 运行 migrate，ACCOUNTKIT_QQ_APP_IDS 含非数字项
- **THEN** 共享静态配置校验拒绝并指出该配置项
