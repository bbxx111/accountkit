# accountsvc-runtime Specification

## Purpose

为 accountkit 提供可选、可直接启动和部署的官方宿主，定义服务配置、网络边界、启动迁移、健康状态、关闭和运维验收，使独立服务与直接嵌入库两种方式共享认证能力并保持各自清晰的资源责任。

## Requirements

### Requirement: 可选服务与库独立消费

项目 SHALL 提供可独立构建的 accountsvc 可执行程序；直接集成 accountkit 的宿主 SHALL 无需运行该服务。两种方式 SHALL 使用同一新版消费者协议；保留 users 资源、身份数据、成功令牌格式和库生命周期契约，登录待完成结果及相关 Go/HTTP 升级 SHALL 有明确迁移说明。

#### Scenario: 独立使用库
- **WHEN** 外部 Go 宿主只依赖 accountkit 并自行注入依赖
- **THEN** 在 GOWORK=off 下能够构建和运行，不要求 accountsvc 配置或网络连接

#### Scenario: 服务消费既有认证能力
- **WHEN** accountsvc 挂载消费者和管理面
- **THEN** 分别使用 `/v1` 和 `/admin/v1`，请求响应、刷新宽限、角色检查及领域规则沿用库契约

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

### Requirement: 服务 HTTP TLS 配置决策

serve SHALL 支持 ACCOUNTSVC_TLS_ENABLED。未设置或为空时，production SHALL 要求 HTTP TLS；development 无证书项时使用 HTTP，配置任一证书项则要求完整有效证书对。显式 true SHALL 在所有模式要求完整有效证书对；显式 false SHALL 在所有模式使用 HTTP，并在配置任一 HTTP 证书项时拒绝启动。非法布尔值 SHALL 拒绝，不自动降级。

#### Scenario: TLS 模式与证书组合
- **WHEN** 分别使用未设置、true、false 或非法 TLS 开关及不同证书组合
- **THEN** 按模式和开关决定 HTTP/HTTPS；要求 TLS 时证书不完整或无效、关闭 TLS 却配置证书、或开关非法时均在监听前失败，不自动选择其他模式

### Requirement: 生产发送与内省凭据要求

production SHALL 在 EMAIL 启用时要求真实 SMTP 与 SMTP TLS，并始终要求内省调用凭据。production SHALL 不使用日志验证码、固定默认秘密或明文邮件降级；HTTP TLS 开关不得使生产 SMTP 降级。

#### Scenario: 生产发送和内省依赖不可降级
- **WHEN** production 缺少必需的真实 SMTP/TLS 或内省调用凭据
- **THEN** 拒绝启动，不用日志验证码、默认秘密或明文 SMTP 替代；显式关闭 HTTP TLS 不豁免 SMTP TLS

### Requirement: migrate 静态校验与依赖边界

migrate SHALL 只执行静态身份策略语法/集合校验及迁移必要检查，不监听、不构造认证处理器，不要求发送器或第三方运行时依赖。migrate SHALL 不要求或解析服务监听地址、HTTP TLS 开关、证书、SMTP 或内省客户端配置；迁移锁与密钥版本检查仍执行。

#### Scenario: 迁移独立于服务运行配置
- **WHEN** migrate 的身份集合合法且迁移必要依赖有效，未提供服务运行配置
- **THEN** 迁移可执行，不读取无关监听/TLS/发送/内省设置，仍核对迁移锁和存储引用密钥

### Requirement: 有界迁移与资源回收

服务 SHALL 提供 serve 和 migrate 命令，无参数等同 serve；serve SHALL 在监听前执行有超时的幂等迁移、依赖连通性及密钥版本检查。migrate SHALL 不监听、不启动维护任务且不要求邮件或管理员配置。失败 SHALL 阻止服务就绪并回收已创建资源，不自动 Force、回滚清库或清除 dirty。

#### Scenario: dirty 或未知密钥版本
- **WHEN** 迁移遇到 dirty，或存储引用未配置的密钥版本
- **THEN** 服务非零退出且保留可诊断状态，不接受认证请求

#### Scenario: 多副本及重复启动
- **WHEN** 多个副本首次启动或重复执行 migrate
- **THEN** 沿用既有迁移锁和维护互斥，迁移无重复应用，超时/取消后释放占用资源

### Requirement: HTTP 边界与可信请求元信息

serve SHALL 仅使用 ACCOUNTSVC_HTTP_ADDR 创建一个监听器，默认绑定 127.0.0.1:8080，挂载消费者 /v1、管理员 /admin/v1、内省 /v1/introspect 与 /healthz、/readyz。不同身份体系和处理器 SHALL 独立。网关与网络策略 SHALL 控制公网暴露范围，不把路径名称视为网络隔离；单监听器不自动区分公网和内网来源。

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

### Requirement: 旧监听配置与内部路径拒绝

服务 SHALL 不再创建内部监听器。serve 遇到非空 ACCOUNTSVC_INTERNAL_ADDR SHALL 非零退出并明确提示迁移配置，不静默忽略。旧内省路径 SHALL 返回404，不注册别名或重定向。

#### Scenario: 废弃内部监听配置不能静默上线
- **WHEN** serve 接收到非空 INTERNAL_ADDR，或调用者访问旧内省路径
- **THEN** 前者拒绝启动并指出迁移项，后者返回404，不自动把旧边界映射到新监听器

### Requirement: 服务账号资源与补全路由

消费者和管理账号集合 SHALL 沿用 users，挂载后分别为 /v1/users 和 /admin/v1/users。消费者补全端点 SHALL 为 /v1/users:sendSignInCompletionCode 与 /v1/users:completeSignIn。此前未发布的 accounts 路径 SHALL 返回404，不注册别名或重定向；token/revoke、内省和探针路径 SHALL 保持。

#### Scenario: 账号路径与既有协议保持一致
- **WHEN** 服务挂载两面账号路由和登录补全端点
- **THEN** 使用 users 及两个明确补全动作；accounts 返回404，token/revoke、内省与探针路径不变

### Requirement: HTTP 资源上限

服务 SHALL 保留有限的 HTTP 超时和请求体上限，并按显式 HTTP TLS 配置决定传输方式。

#### Scenario: 监听器保留请求资源约束
- **WHEN** 服务按有效 HTTP 或 HTTPS 配置监听
- **THEN** 既有超时和请求体上限仍生效，不因传输方式切换而移除

### Requirement: 可信代理与请求 ID

来源 IP SHALL 只信任明确配置 CIDR 的代理链；不可信对端的转发头不能覆盖直接对端地址，可信链畸形时回退直接对端。请求 ID SHALL 校验或生成并回传，用于关联请求与审计。

#### Scenario: 代理来源及请求关联可核对
- **WHEN** 收到不可信或畸形代理链，或缺失/非法请求 ID
- **THEN** 按可信代理契约取得来源 IP，并校验或生成可回传的请求 ID，不信任客户端任意转发值

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

### Requirement: 匿名化责任与可重复验收

服务 SHALL 只承诺自身认证数据匿名化，文档 SHALL 明示业务表/跨库清理责任及不可与依赖业务回调的宿主混跑维护任务。交付 SHALL 包含二进制/容器运行方式、开发依赖环境和严格服务集成入口，缺少必需依赖 SHALL 验收失败而非跳过后报成功。

#### Scenario: 含业务数据的接入方
- **WHEN** 使用方有额外 user_id 关联表
- **THEN** 接入说明要求其选择嵌入式同库回调或独立业务清理方案，不把 accountsvc 启动成功当作业务匿名化验收

#### Scenario: 完整服务验收
- **WHEN** 执行服务集成验证
- **THEN** 使用真实 PostgreSQL/Redis 和隔离邮件接收器验证邮件登录、刷新、内省、注销以及实际进程退出；缺依赖即失败

### Requirement: 可选本人资料 Schema 装配

serve SHALL 支持可选 ACCOUNTSVC_PROFILE_ATTRIBUTES_SCHEMA_FILE，在监听及运行依赖创建前有界读取本地普通文件并以现有资料 Schema 规则校验。空设置 SHALL 保持禁用；非空设置读取失败、非普通文件、空文件、超过 64 KiB 或内容无效 SHALL 拒绝启动。错误 SHALL 仅含配置项名称及安全分类，不含路径、内容或校验器原文。SHALL 不支持 URL 或热更新，库 New/ConfigFromEnv 保持无文件 I/O，migrate SHALL 不读取或校验此服务设置。

#### Scenario: 启动后本人读写
- **WHEN** serve 使用合法 Schema 文件启动且其他依赖满足
- **THEN** `/v1/users/me/profile` 提供同一消费者协议，Schema 声明的属性全部对本人可读写，原接口保持

#### Scenario: 缺失配置与错误文件
- **WHEN** 配置为空，或非空但文件不可读/非法/超限
- **THEN** 前者启动不受影响且认证后的合法资料请求返回 503；后者监听前失败且不泄露路径或文件内容

#### Scenario: migrate 隔离
- **WHEN** migrate 设置了不存在或非法的资料 Schema 文件路径
- **THEN** 忽略此服务设置，迁移行为只受既有迁移配置及依赖约束

#### Scenario: 配置变更
- **WHEN** 运行期间文件被修改
- **THEN** 当前实例规则不变化，重启后加载新内容；多副本配置一致性由部署方保证，不自动转换旧资料

### Requirement: 服务 OIDC 提供方配置
serve SHALL 从 `ACCOUNTKIT_OIDC_PROVIDERS` 读取逗号分隔的 OIDC 类型名，并为每个类型读取 `ACCOUNTKIT_OIDC_<KIND>_ISSUER`、`_JWKS_URL`、`_CLIENT_IDS` 和可选的 `_NONCE_TTL`、`_NAME_CLAIMS`、`_PICTURE_CLAIMS`（变量未设置为默认映射，设置为空为不读取）。缺失或非法时 SHALL 在监听前非零退出并指出具体变量名，不泄露配置值。显式 Enabled 包含未在 `ACCOUNTKIT_OIDC_PROVIDERS` 中配置的第三方类型时 SHALL 指出 `ACCOUNTKIT_ENABLED_IDENTITY_KINDS`。仅启用 OIDC 类型且 Required 为空时，无 SMTP SHALL 仍可启动。未设置任何 OIDC 变量时 migrate SHALL 不要求它们；已列出 OIDC 类型但其配置不完整或非法时，migrate 与 serve SHALL 同样启动失败并指出变量名，与其他已提供但格式非法的库配置一致。

#### Scenario: 缺少 issuer
- **WHEN** `ACCOUNTKIT_OIDC_PROVIDERS=CORP_SSO` 而未设置 `ACCOUNTKIT_OIDC_CORP_SSO_ISSUER`
- **THEN** serve 启动失败并指出 `ACCOUNTKIT_OIDC_CORP_SSO_ISSUER`

#### Scenario: 仅 OIDC 的服务
- **WHEN** 显式 Enabled 为 CORP_SSO、Required 为 []、该类型配置有效且没有 SMTP 设置
- **THEN** serve 可按其他原安全条件启动并完成 CORP_SSO 登录

#### Scenario: migrate 不需要 OIDC 配置
- **WHEN** 执行 migrate 且未设置任何 OIDC 变量
- **THEN** 迁移照常执行，不因 OIDC 配置失败

#### Scenario: migrate 遇到不完整的 OIDC 配置
- **WHEN** 执行 migrate，`ACCOUNTKIT_OIDC_PROVIDERS` 列出 CORP_SSO 但缺少其 issuer
- **THEN** 启动失败并指出 `ACCOUNTKIT_OIDC_CORP_SSO_ISSUER`
