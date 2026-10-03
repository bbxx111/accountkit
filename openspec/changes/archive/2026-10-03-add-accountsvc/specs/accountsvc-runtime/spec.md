## Purpose

为 accountkit 提供可选、可直接启动和部署的官方宿主，定义服务配置、网络边界、启动迁移、健康状态、关闭和运维验收，使独立服务与直接嵌入库两种方式共享认证能力并保持各自清晰的资源责任。

## ADDED Requirements

### Requirement: 可选服务与库独立消费

项目 SHALL 提供可独立构建的 accountsvc 可执行程序；直接集成 accountkit 的宿主 SHALL 无需运行该服务。两种方式 SHALL 保持现有消费者协议、身份数据、令牌格式和库生命周期契约兼容。

#### Scenario: 独立使用库
- **WHEN** 外部 Go 宿主只依赖 accountkit 并自行注入依赖
- **THEN** 在 GOWORK=off 下能够构建和运行，不要求 accountsvc 配置或网络连接

#### Scenario: 服务消费既有认证能力
- **WHEN** accountsvc 挂载消费者和管理面
- **THEN** 分别使用 `/v1` 和 `/admin/v1`，请求响应、刷新宽限、角色检查及领域规则沿用库契约

### Requirement: 配置校验与安全启动

服务 SHALL 保留 `ACCOUNTKIT_` 库配置并使用 `ACCOUNTSVC_` 服务配置，默认 production。生产 SHALL 要求 HTTP TLS、真实 SMTP 及内省调用凭据，不使用日志验证码、固定默认秘密或明文邮件降级。非法配置 SHALL 阻止监听且不在错误中泄露配置值。

#### Scenario: 配置缺失
- **WHEN** serve 缺少 SMTP、数据库、Redis、TLS 或内省客户端必填配置
- **THEN** 在接收请求前以非零退出并指出配置项名称，不泄露秘密

#### Scenario: 生产明文配置
- **WHEN** production 请求禁用 HTTP TLS 或使用明文 SMTP
- **THEN** 启动失败；只有显式 development 才允许测试用明文传输

### Requirement: 有界迁移与资源回收

服务 SHALL 提供 serve 和 migrate 命令，无参数等同 serve；serve SHALL 在监听前执行有超时的幂等迁移、依赖连通性及密钥版本检查。migrate SHALL 不监听、不启动维护任务且不要求邮件或管理员配置。失败 SHALL 阻止服务就绪并回收已创建资源，不自动 Force、回滚清库或清除 dirty。

#### Scenario: dirty 或未知密钥版本
- **WHEN** 迁移遇到 dirty，或存储引用未配置的密钥版本
- **THEN** 服务非零退出且保留可诊断状态，不接受认证请求

#### Scenario: 多副本及重复启动
- **WHEN** 多个副本首次启动或重复执行 migrate
- **THEN** 沿用既有迁移锁和维护互斥，迁移无重复应用，超时/取消后释放占用资源

### Requirement: HTTP 边界与可信请求元信息

公开监听器 SHALL 只提供消费者和管理面；内部监听器 SHALL 提供内省和健康端点，默认二者均绑定回环地址。服务 SHALL 设置有限 HTTP 超时和请求体上限，生产两个监听器均使用 TLS。来源 IP SHALL 只信任明确配置 CIDR 的代理链；请求 ID SHALL 校验或生成并回传。

#### Scenario: 公网请求内部路径
- **WHEN** 客户端在公开监听器请求 `/internal/v1/introspect`、`/healthz` 或 `/readyz`
- **THEN** 返回 404，不转发到内部监听器

#### Scenario: 伪造代理头
- **WHEN** 不可信直接对端发送 X-Forwarded-For
- **THEN** 限流与审计使用直接对端 IP；可信链畸形时同样回退直接对端

### Requirement: 存活与就绪分离

内部 `GET /healthz` SHALL 不访问外部依赖；`GET /readyz` SHALL 仅在启动完成、未停机且数据库/Redis 在 2 秒总预算内可用时返回 200，否则返回 503。响应 SHALL 不包含依赖地址和内部错误。SMTP 和运行期 OIDC 故障 SHALL 不单独改变整体 readiness。

#### Scenario: Redis 不可用
- **WHEN** 已启动进程的 Redis 探测失败
- **THEN** healthz 仍为 200，readyz 为 503，响应不暴露连接串

### Requirement: 优雅关闭与失败退出

服务 SHALL 响应 SIGINT/SIGTERM 和监听器异常，先撤销就绪、排空两个 HTTP 面，再停止库后台任务与刷新审计，最后关闭自建连接。关闭 SHALL 有总预算；超时或监听器异常 SHALL 非零退出并说明失败阶段，不宣称未完成的写入已成功。

#### Scenario: 在途请求关闭
- **WHEN** 进程收到终止信号且存在正在执行的请求
- **THEN** 不再接受新业务请求，在预算内等待旧请求完成后关闭审计和连接，不因先取消后台上下文而提前丢弃在途请求审计

### Requirement: 匿名化责任与可重复验收

服务 SHALL 只承诺自身认证数据匿名化，文档 SHALL 明示业务表/跨库清理责任及不可与依赖业务回调的宿主混跑维护任务。交付 SHALL 包含二进制/容器运行方式、开发依赖环境和严格服务集成入口，缺少必需依赖 SHALL 验收失败而非跳过后报成功。

#### Scenario: 含业务数据的接入方
- **WHEN** 使用方有额外 user_id 关联表
- **THEN** 接入说明要求其选择嵌入式同库回调或独立业务清理方案，不把 accountsvc 启动成功当作业务匿名化验收

#### Scenario: 完整服务验收
- **WHEN** 执行服务集成验证
- **THEN** 使用真实 PostgreSQL/Redis 和隔离邮件接收器验证邮件登录、刷新、内省、注销以及实际进程退出；缺依赖即失败
