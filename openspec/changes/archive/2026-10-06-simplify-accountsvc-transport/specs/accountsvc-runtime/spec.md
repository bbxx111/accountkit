## MODIFIED Requirements

### Requirement: 配置校验与安全启动

服务 SHALL 保留 `ACCOUNTKIT_` 库配置并使用 `ACCOUNTSVC_` 服务配置，默认 production。serve SHALL 支持 `ACCOUNTSVC_TLS_ENABLED`；未设置或为空时，production SHALL 要求服务端 HTTP TLS，development SHALL 保持未配证书使用 HTTP、配置任一证书项则要求完整有效证书对的既有行为。显式 true SHALL 在所有模式下要求完整有效证书对；显式 false SHALL 在所有模式下使用 HTTP，并在配置任一 HTTP 证书项时拒绝启动。非法布尔值 SHALL 被拒绝，不自动降级。生产 SHALL 继续要求真实 SMTP、SMTP TLS 和内省调用凭据，不使用日志验证码、固定默认秘密或明文邮件降级。非法配置 SHALL 阻止监听且不在错误中泄露配置值。

#### Scenario: 配置缺失
- **WHEN** serve 缺少 SMTP、数据库、Redis、内省客户端必填配置，或在有效 TLS 配置要求启用时缺少证书/私钥
- **THEN** 在接收请求前以非零退出并指出配置项名称，不泄露秘密

#### Scenario: 生产明文配置
- **WHEN** production 未显式关闭 HTTP TLS 却没有完整有效证书，或请求使用明文 SMTP
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

### Requirement: HTTP 边界与可信请求元信息

serve SHALL 仅使用 `ACCOUNTSVC_HTTP_ADDR` 创建一个监听器，默认绑定 `127.0.0.1:8080`，挂载消费者 `/v1`、管理员 `/admin/v1`、内省 `/v1/introspect` 与 `/healthz`、`/readyz`。不同身份体系和处理器 SHALL 保持独立。服务 SHALL 不再创建内部监听器；serve 遇到非空 `ACCOUNTSVC_INTERNAL_ADDR` SHALL 非零退出并明确提示迁移配置，旧内省路径 SHALL 返回404而非别名或重定向。网关和网络策略 SHALL 承担对公网暴露范围控制，不把路径名称视为网络隔离。服务 SHALL 保留有限 HTTP 超时、请求体上限并按显式 TLS 配置决定传输；来源 IP SHALL 只信任明确配置 CIDR 的代理链，请求 ID SHALL 校验或生成并回传。

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

### Requirement: 存活与就绪分离

单监听器上的 `GET /healthz` SHALL 不访问外部依赖；`GET /readyz` SHALL 仅在启动完成、未停机且数据库/Redis 在2秒总预算内可用时返回200，否则返回503。探针 SHALL 不引入消费者或管理员凭据要求，访问范围由部署网络和网关控制。响应 SHALL 不包含依赖地址和内部错误。SMTP 和运行期 OIDC 故障 SHALL 不单独改变整体 readiness。

#### Scenario: Redis 不可用
- **WHEN** 已启动进程的 Redis 探测失败
- **THEN** healthz 仍为200，readyz 为503，响应不暴露连接串

#### Scenario: 探针跟随监听器传输配置
- **WHEN** 服务端 TLS 启用或显式关闭
- **THEN** 探针分别通过同一地址的 HTTPS 或 HTTP 访问，部署说明要求同步更新探针端口和协议

### Requirement: 优雅关闭与失败退出

服务 SHALL 响应 SIGINT/SIGTERM 和监听器异常，先撤销就绪、排空单监听器上的全部在途请求，再停止库后台任务与刷新审计，最后关闭自建连接。关闭 SHALL 有总预算；超时或监听器异常 SHALL 非零退出并说明失败阶段，不宣称未完成的写入已成功。

#### Scenario: 在途请求关闭
- **WHEN** 进程收到终止信号且存在正在执行的请求
- **THEN** 不再接受新业务请求，在预算内等待旧请求完成后关闭审计和连接，不因先取消后台上下文而提前丢弃在途请求审计
