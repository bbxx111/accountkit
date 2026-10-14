## MODIFIED Requirements

### Requirement: 可选服务与库独立消费

项目 SHALL 提供可独立构建的 accountsvc 可执行程序；直接集成 accountkit 的宿主 SHALL 无需运行该服务。两种方式 SHALL 使用同一新版消费者协议；保留 users 资源、身份数据、成功令牌格式和库生命周期契约，登录待完成结果及相关 Go/HTTP 升级 SHALL 有明确迁移说明。

#### Scenario: 独立使用库
- **WHEN** 外部 Go 宿主只依赖 accountkit 并自行注入依赖
- **THEN** 在 GOWORK=off 下能够构建和运行，不要求 accountsvc 配置或网络连接

#### Scenario: 服务消费既有认证能力
- **WHEN** accountsvc 挂载消费者和管理面
- **THEN** 分别使用 `/v1` 和 `/admin/v1`，请求响应、刷新宽限、角色检查及领域规则沿用库契约

### Requirement: 配置校验与安全启动

服务 SHALL 保留 `ACCOUNTKIT_` 库配置并使用 `ACCOUNTSVC_` 服务配置，默认 production。serve SHALL 支持 `ACCOUNTSVC_TLS_ENABLED`；未设置或为空时，production SHALL 要求服务端 HTTP TLS，development SHALL 保持未配证书使用 HTTP、配置任一证书项则要求完整有效证书对的既有行为。显式 true SHALL 在所有模式下要求完整有效证书对；显式 false SHALL 在所有模式下使用 HTTP，并在配置任一 HTTP 证书项时拒绝启动。非法布尔值 SHALL 被拒绝，不自动降级。生产 SHALL 在 EMAIL 启用时要求真实 SMTP 和 SMTP TLS，并始终要求内省调用凭据，不使用日志验证码、固定默认秘密或明文邮件降级。非法配置 SHALL 阻止监听且不在错误中泄露配置值。

serve SHALL 读取 ACCOUNTKIT_ENABLED_IDENTITY_KINDS 和 ACCOUNTKIT_REQUIRED_IDENTITY_KINDS，并对显式策略执行子集与依赖校验；未设置新策略时保留原默认装配。EMAIL 显式禁用时 SHALL 不要求或解析 SMTP 设置。首版未提供短信服务商，显式启用 PHONE SHALL 拒绝启动，不使用日志发送器或伪发送器代替。migrate SHALL 只执行静态策略语法/集合校验及迁移必要检查，不要求发送器或第三方运行时依赖。

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

#### Scenario: 服务不能凭开关获得短信能力
- **WHEN** serve 显式 Enabled 包含 PHONE
- **THEN** 启动失败并指出缺少服务支持；直接嵌入库的宿主仍可注入真实短信发送器

#### Scenario: migrate 不受认证依赖阻塞
- **WHEN** 执行 migrate，身份集合本身合法而 SMTP 或 IdP 运行配置未提供
- **THEN** 不监听、不构造认证处理器、不要求这些运行依赖；迁移锁及密钥版本检查仍执行


### Requirement: HTTP 边界与可信请求元信息

serve SHALL 仅使用 `ACCOUNTSVC_HTTP_ADDR` 创建一个监听器，默认绑定 `127.0.0.1:8080`，挂载消费者 `/v1`、管理员 `/admin/v1`、内省 `/v1/introspect` 与 `/healthz`、`/readyz`。不同身份体系和处理器 SHALL 保持独立。服务 SHALL 不再创建内部监听器；serve 遇到非空 `ACCOUNTSVC_INTERNAL_ADDR` SHALL 非零退出并明确提示迁移配置，旧内省路径 SHALL 返回404而非别名或重定向。网关和网络策略 SHALL 承担对公网暴露范围控制，不把路径名称视为网络隔离。服务 SHALL 保留有限 HTTP 超时、请求体上限并按显式 TLS 配置决定传输；来源 IP SHALL 只信任明确配置 CIDR 的代理链，请求 ID SHALL 校验或生成并回传。

消费者和管理账号集合 SHALL 沿用 users，挂载后分别位于 /v1/users 和 /admin/v1/users；新增消费者补全端点 SHALL 为 /v1/users:sendSignInCompletionCode 与 /v1/users:completeSignIn。此前未发布的 accounts 路径 SHALL 返回 404，不注册别名或重定向；token/revoke、内省和探针路径 SHALL 保持。

#### Scenario: 公网请求内部路径
- **WHEN** 公网调用者通过按接入契约配置的网关请求 `/v1/introspect`、`/healthz` 或 `/readyz`
- **THEN** 网关拒绝且请求不进入 accountsvc；单监听器本身不承诺识别公网/内网来源并自动隔离这些路由

#### Scenario: 伪造代理头
- **WHEN** 不可信直接对端发送 X-Forwarded-For
- **THEN** 验证码限流与审计使用直接对端 IP；可信链畸形时同样回退直接对端

#### Scenario: 单地址提供不同身份面
- **WHEN** serve 使用有效配置启动
- **THEN** 只绑定一次，所有路由使用该地址；内省仍要求 Basic，消费者和管理员仍分别验证各自凭据及权限

#### Scenario: 旧部署配置被显式拒绝
- **WHEN** serve 仍提供非空 INTERNAL_ADDR
- **THEN** 启动失败并指出该废弃配置项，不静默将原内部路径暴露到新监听器；删除配置后旧内省路径仍返回404

#### Scenario: 两个新增补全端点
- **WHEN** 服务启用新版消费者协议
- **THEN** 独立提供 users:sendSignInCompletionCode 和 users:completeSignIn，普通发码用途不通过补全票据切换，管理面不暴露未认证的补全操作
