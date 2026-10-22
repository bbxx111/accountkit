# Spec Delta

## MODIFIED Requirements

### Requirement: 配置校验与安全启动

服务 SHALL 保留 `ACCOUNTKIT_` 库配置并使用 `ACCOUNTSVC_` 服务配置，默认 production。serve SHALL 在监听前完成配置校验；非法配置 SHALL 非零退出并指出配置项名称，错误不得泄露配置值。显式启用 GOOGLE 但没有 client ID 时 SHALL 指出 `ACCOUNTKIT_GOOGLE_CLIENT_IDS`。仅启用 GOOGLE、Required 为空且 client ID 有效时，无 SMTP SHALL 仍可启动。服务 SHALL 分别遵守下述身份策略、HTTP TLS、生产依赖与 migrate 依赖边界。

#### Scenario: 配置缺失
- **WHEN** serve 缺少已启用 EMAIL 所需的 SMTP、数据库、Redis、内省客户端必填配置，或在有效 TLS 配置要求启用时缺少证书/私钥
- **THEN** 在接收请求前以非零退出并指出配置项名称，不泄露秘密

#### Scenario: 生产明文配置
- **WHEN** production 未显式关闭 HTTP TLS 却没有完整有效证书，或在 EMAIL 启用时请求使用明文 SMTP
- **THEN** 启动失败；HTTP TLS 开关不允许生产 SMTP 降级

#### Scenario: 外部终止模式
- **WHEN** production 显式设置 TLS_ENABLED=false，未配置 HTTP 证书项且其他必需配置有效
- **THEN** 单监听器以 HTTP 启动，认证与授权仍生效，启动日志明确本进程未启用 HTTP TLS；部署方负责外部传输保护，服务不自动推断网格存在

#### Scenario: 显式 TLS 与冲突配置
- **WHEN** TLS_ENABLED=true 但证书无效，TLS_ENABLED=false 却配置证书或私钥，或开关不是有效布尔值
- **THEN** 拒绝启动，不忽略冲突、不回退其他模式、不在错误中输出秘密

#### Scenario: 原开发默认与 migrate 保持兼容
- **WHEN** development 未设置开关且未配证书，或运行不读取服务专用设置的 migrate
- **THEN** 前者仍可使用 HTTP；后者不要求或解析监听地址、TLS 开关、证书、SMTP 或内省客户端配置

#### Scenario: 纯第三方服务
- **WHEN** 显式 Enabled 为 WECHAT 或 APPLE、Required 为 []，提供方配置有效且没有 SMTP 设置
- **THEN** serve 可按其他原安全条件启动，并完成正常登录及重新登录；不因此新增第三方重新认证或恢复能力；没有发送邮件的运行依赖

#### Scenario: 仅 Google 的服务
- **WHEN** 显式 Enabled 为 GOOGLE、Required 为 []、client ID 有效且没有 SMTP 设置
- **THEN** serve 可按其他原安全条件启动并完成 Google 登录，不要求邮件发送

#### Scenario: Google 缺少 client ID
- **WHEN** 显式 Enabled 包含 GOOGLE 但 client ID 为空
- **THEN** 启动失败并指出 `ACCOUNTKIT_GOOGLE_CLIENT_IDS`，错误不泄露其他配置值

#### Scenario: 服务不能凭开关获得短信能力
- **WHEN** serve 显式 Enabled 包含 PHONE
- **THEN** 启动失败并指出缺少服务支持；直接嵌入库的宿主仍可注入真实短信发送器

#### Scenario: migrate 不受认证依赖阻塞
- **WHEN** 执行 migrate，身份集合本身合法而 SMTP 或 IdP 运行配置未提供
- **THEN** 不监听、不构造认证处理器、不要求这些运行依赖；迁移锁及密钥版本检查仍执行

### Requirement: 服务身份策略与发送能力

serve SHALL 读取 ACCOUNTKIT_ENABLED_IDENTITY_KINDS 和 ACCOUNTKIT_REQUIRED_IDENTITY_KINDS，对显式策略执行子集与依赖校验；未设置时保留原默认装配。EMAIL 显式禁用时 SHALL 不要求或解析 SMTP 设置。首版无短信服务商，显式启用 PHONE SHALL 拒绝启动，不使用日志或伪发送器替代。显式启用 GOOGLE 时 SHALL 要求至少一个 client ID，缺失时指出 `ACCOUNTKIT_GOOGLE_CLIENT_IDS`。

#### Scenario: 身份集合与服务发送能力一致
- **WHEN** serve 读取显式身份策略
- **THEN** 校验集合及运行依赖；EMAIL 禁用不解析 SMTP，PHONE 启用因服务缺少短信能力而拒绝，直接嵌入库仍可注入真实发送器

#### Scenario: 启用 Google 但未配置客户端
- **WHEN** serve 的显式 Enabled 包含 GOOGLE，且 Google client ID 为空
- **THEN** 启动失败并指出 `ACCOUNTKIT_GOOGLE_CLIENT_IDS`
