## Context

动机见 [proposal.md](proposal.md)。现有实现仍是一个 Go module，HTTP 入口分别由 `httpapi/enduser/handler.go`、`httpapi/admin/handler.go` 注册；`docs/openapi/enduser.json`、`docs/openapi/admin.json`、`docs/http-client-flows.md` 和领域规格共同约束客户端协议。SDK 是新增分发单元，不要求 Go 使用方安装 Flutter 或 Node。

本设计以服务 `v1.0.0` 为首个兼容基线。核对实际路由得到消费者 24 个、管理端 11 个操作；既有应用封装不能代替此清单。特别需要覆盖身份换绑、扩展资料、当前会话，以及开放的身份 kind。宿主可以不配置部分可选能力，SDK 必须保留其明确的 503 reason。

现有客户端流程文档规定普通 403 不自动刷新。SDK 遵循这一契约：绑定或恢复账号成功后可以主动同步 scope，普通 `INSUFFICIENT_SCOPE` 不触发隐式重试。重新认证只替换 Access Token；身份解绑或换绑成功可能同时撤销当前会话。这些区别决定会话运行时不能只是一个通用拦截器。

## Goals / Non-Goals

**Goals:**

- 将 HTTP 契约适配、消费者会话所有权和可测试的异步流程集中在对应 SDK 中，同时让应用明确控制平台授权、展示和管理端登录。
- 同时适配独立 accountsvc 和实现同一 HTTP 契约的嵌入式宿主；通过地址与挂载前缀配置完成接入。
- 让 Git 子目录本身成为完整的可消费包；用隔离安装和真实服务测试证明边界。

**Non-Goals:**

- 不建立跨语言共享的网络框架、第三个认证包或生成所有客户端的通用代码生成器。
- 不将 UserInfo、OIDC 回调/续期、角色映射、页面准入或导航放入管理 SDK；不从浏览器解析的 Token 推导服务端授权结论。
- 不实现第三方原生登录 UI/SDK，不添加服务端路由、Go API、数据库迁移或下游迁移代码。

## Decisions

### D1. 固定包身份，按协议人工维护薄客户端

| 分发单元 | 目录 | 包名 | 公共入口 |
| --- | --- | --- | --- |
| Flutter 消费者 | `sdks/accountkit-flutter` | `accountkit_flutter` | `lib/accountkit_flutter.dart` |
| TypeScript 管理端 | `sdks/accountkit-admin-ts` | `@accountkit/admin-client` | `src/index.ts` |

Flutter 公开 `AccountApi`、`AccountClient`、`AccountController`；测试入口为 `lib/testing.dart`。TS 公开 `createAccountAdminClient` 及 DTO、请求选项、错误与 filter 类型。名称中不带具体应用、认证提供方或服务部署方式。

两个包分别包含 manifest、README、LICENSE、CHANGELOG、源码和测试，内部使用相对引用。Flutter 不声明 `resolution: workspace`，TS 不使用 `workspace:` 依赖或仓库根目录路径别名。包开发工具配置也在各包内，不改变根目录 Go module 的独立性。

采用人工维护 DTO/方法与机器可检查覆盖登记的组合。OpenAPI 负责发现端点漂移，handler 和真实契约测试负责校验语义。直接生成全部客户端不能表达会话竞争、补全期限和成功操作后的同步结果；直接搬运应用代码则容易漏掉正式契约。

### D2. 覆盖清单与地址模型

配置分别为服务 `baseUrl` 和 `apiPrefix`，默认前缀 `/v1`、`/admin/v1`。baseUrl 表示服务 origin，可选反向代理基础路径；规范化只执行一次。禁止查询串、fragment、嵌入式用户名密码以及会造成路径逃逸的输入；固定 API 前缀不能被资源名覆盖。远程部署使用 HTTPS，本机测试可明确使用 HTTP。

消费者方法映射如下，路径均相对消费者前缀：

| 方法名 | HTTP 路由 |
| --- | --- |
| sendSignInCode | POST /users:sendSignInCode |
| signInWithCode | POST /users:signInWithCode |
| signInWithIdp | POST /users:signInWithIdp |
| sendSignInCompletionCode | POST /users:sendSignInCompletionCode |
| completeSignIn | POST /users:completeSignIn |
| refreshToken | POST /token |
| revokeToken | POST /revoke |
| getMe / updateMe / deleteMe | GET / PATCH / DELETE /users/me |
| undeleteMe | POST /users/me:undelete |
| sendReauthenticationCode | POST /users/me:sendReauthenticationCode |
| reauthenticate | POST /users/me:reauthenticate |
| listSessions | GET /users/me/sessions |
| revokeSession | DELETE /users/me/sessions/{session} |
| revokeOtherSessions | POST /users/me/sessions:revokeOthers |
| sendBindCode | POST /users/me:sendBindCode |
| listIdentities / bindIdentity | GET / POST /users/me/identities |
| unbindIdentity | DELETE /users/me/identities/{identity} |
| replaceIdentity | POST /users/me/identities/{identity}:replace |
| getCurrentSession | GET /users/me/sessions/current |
| getMyProfile / patchMyProfile | GET / PATCH /users/me/profile |

管理端方法映射如下，路径均相对管理前缀；角色栏只记录服务端要求，不在 SDK 内做授权替代：

| 方法名 | HTTP 路由 | 服务端角色 |
| --- | --- | --- |
| listUsers | GET /users | operator / super-admin |
| getUser | GET /users/{user} | operator / super-admin |
| deleteUser | DELETE /users/{user} | super-admin |
| freezeUser / unfreezeUser | POST /users/{user}:freeze / :unfreeze | operator / super-admin |
| undeleteUser | POST /users/{user}:undelete | super-admin |
| revealIdentity | GET /users/{user}/identities/{identity}:reveal | super-admin |
| listSessions | GET /users/{user}/sessions | operator / super-admin |
| revokeSession | DELETE /users/{user}/sessions/{session} | operator / super-admin |
| revokeAllSessions | POST /users/{user}/sessions:revokeAll | operator / super-admin |
| listAuditEvents | GET /users/{user}/auditEvents | operator / super-admin |

新增 `sdks/http-coverage.json`，每条记录含 surface、operationId、method、path、SDK 方法、测试标识、兼容基线和可选能力说明。`scripts/check-sdk-coverage.mjs` 将登记与两个 OpenAPI 对照；端点数量只是当前快照，不用固定数量掩盖未来新增操作。内省和 purge-feed 不属于这两个 SDK。

公开 DTO 保持 wire 字段含义；Dart 可用惯用属性名并显式序列化 snake_case，TS 保持 snake_case。资源 ID 和分页 token 均不透明。资源名参数只允许预期层级，嵌套资源必须匹配给定父资源；逐段编码，拒绝绝对 URL、点段和编码后的路径逃逸。

### D3. 消费者凭证、模型与错误以正式协议为准

`AccountApi` 定义账号操作和只读会话观察契约；`AccountClient` 是 HTTP 实现，测试替身实现相同契约。HTTP 编解码留在内部，所有会改变凭据的方法必须经过同一个会话运行时，避免调用底层登录/刷新方法后遗漏持久化。

身份凭据使用 `kind` / `credential` envelope，提供 PHONE、EMAIL 和已注册第三方的类型化构造器，也允许明确指定自定义 kind 与 JSON credential。kind 不是封闭枚举；保留未知字符串以免丢失新增身份。内置构造器覆盖 APPLE/GOOGLE/OIDC 的 id_token＋nonce、WECHAT/ALIPAY 的 app_id＋code、QQ/WEIBO 的 app_id＋access_token，以及 FACEBOOK 的正式互斥凭据形式。构造器检查形状，不执行第三方网络授权，也不以 JWT 解码代替服务端验证。

账号基本资料更新保留字段未提供与空字符串的差别；扩展资料 patch 保留 JSON merge patch 中 null 删除键的语义。当前会话按正式的平坦 DTO 解析，包含认证时间与认证身份；不得套用旧应用自定义的嵌套响应。时间字段统一解析，原始标识和未知枚举仍能往返保留。审慎保留 null 与省略，不进行全局“清理空值”。

错误分为 AIP API、OAuth、网络、协议、存储、取消和需要重新认证等类别；API 错误保留 HTTP 状态、reason、服务端等待时间及 request id。应用按 reason 本地化。对非 JSON 或缺失字段使用安全的固定协议错误，不把原始响应体、credential、Token 或完整身份标识拼进异常字符串。`SESSION_QUERY_NOT_CONFIGURED`、`IDENTITY_REPLACEMENT_NOT_CONFIGURED`、`PROFILE_ATTRIBUTES_NOT_CONFIGURED` 等按服务响应传递，不能伪造成功。

### D4. Flutter 仅有一个会话所有者

`AccountClient` 内部拥有会话运行时：TokenStore、已提交 token pair、session epoch、token revision、单个 refresh future 以及串行持久化队列。`AccountController` 依赖 `AccountApi`，订阅会话快照，协调登录补全和验证码等临时流程；它不持有第二份可写 TokenStore，也不要求调用方手工串接互相依赖的回调。

TokenStore 以一条完整记录保存 access/refresh、scope、用户标识及两类到期信息，避免多键写入产生半个会话。平台默认适配安全存储，可注入测试实现。恢复时校验记录；损坏记录不得形成登录态。登录只有在持久化成功后才发布已登录事件。所有写入按序提交，写前检查 epoch/revision；退出或换账号使旧网络响应失效，并确保之后的清除不会被旧写入覆盖。持久化失败产生可见的存储错误，不默默声称已持久化或退出完成。

四个公开认证状态为 `SignedOut`、`NeedsBinding`、`PendingDeletion`、`SignedIn`，由正式响应中存储的 scope 决定，不解析 JWT。未知或相互冲突的 scope 为协议错误，不降级为 SignedIn，也不新增第五种认证状态。恢复尚未完成属于控制器初始化状态，不可作为已登录依据。

主动刷新、到期检查和并发 401 使用同一 refresh future。请求记录发送时的 revision：若另一个请求已刷新，重试时使用新 token，不再重复刷新。最多一次认证重试；重试后确认 401 只清除对应旧会话。刷新 invalid_grant 结束对应会话并保留被冻结等 reason；网络、429、503 保留凭据，避免将暂时故障当退出。refresh 有界，不循环重试；普通 403/400 不触发刷新。重新认证替换 access 及其到期数据，保留原 refresh 和 refresh 到期数据。

`deleteMe` 成功结束本地会话；撤销当前 session、明确的本地 signOut 和服务端确认失效也结束对应会话。其他 session 的撤销不清除本地登录。signOut 先使 epoch 失效并清理本地记录，远程 revoke 失败以单独的远程撤销结果报告，不恢复本地凭据。

绑定/恢复后主动刷新以同步 scope；解绑/换绑后最多一次当前会话确认。上述成功写操作返回 `AccountMutationResult<T>`：业务 value 与 `sessionSync` 分开，后者为 confirmed、sessionEnded 或 unconfirmed，可附安全的同步错误。同步失败不改写已经成功的业务结果，不诱导重做换绑。确认预算默认为 5 秒，迟到结果仍受 epoch/revision 检查；未确认不会冒充已同步。调用方后续可显式同步或重新登录。

相比把所有逻辑写进 Controller，这一所有权分配确保直接使用 AccountClient 与使用 Controller 的持久化和竞争语义一致；相比每个业务客户端各自拦截刷新，它防止旋转 refresh token 被并发消费。

### D5. 临时流程和网络传输显式隔离

登录补全保存在 Controller 的独立内存对象中，关联 flow generation、原始到期时间、原始设备头快照和可重试的原始请求。它不进入 TokenStore、认证状态、URL、日志或可打印调试字段。取消、换账号、外部会话更新或过期立即失效；迟到响应不能写入 TokenStore。未知 next_action 或没有已知可执行 channel 为可恢复协议错误。

验证码关联 purpose、规范化目标、code_id、到期时间和 generation。等待以服务端返回的 429 秒数为准，不猜剩余次数，不自动重发验证码。登录补全发生网络/5xx 未知结果时，只允许有效期限内由应用显式重试原请求；不能重建流程延长期限。普通替换身份发生未知结果则先重新读取状态，不自动重做写操作。

设备 ID 持久生成；每次需要发送的设备头由统一 provider 提供。可选 client-info 采集有 1 秒默认预算、大小限制和缺省降级；它只是显示数据。原生第三方授权由应用调用，SDK 提供 nonce 工具并接收结果，不依赖各平台的登录插件。

账号 HTTP 客户端拒绝重定向；匿名登录、补全、refresh、revoke 路径不注入 Bearer、不进入自动认证重试。所有请求有总超时和取消入口，已取消的结果不得继续变更会话；取消不意味着服务端回滚。

业务方通过 `createAuthenticatedDio` 使用同一运行时，创建由 SDK 管理的认证 Dio 实例并显式声明允许的 origin 和路径范围。默认不跨 origin 发送 Token，路径匹配按完整段边界，绝不根据任意绝对 URL 自动扩展信任。禁止重定向。GET/HEAD 在明确 401 后可重放一次；其他方法必须显式声明可重放且提供可重建 body，流或已消费 FormData 不可盲目复制。未知网络错误、5xx、认证流程本身不进入自动重放。请求 ID 不被当作幂等键。

`dispose` 停止定时器、监听器和 SDK 创建的传输资源，并使待处理流程失效；调用方注入的网络资源由调用方关闭。公开 `testing.dart` 提供内存 TokenStore、可控时钟/设备信息及 FakeAccountApi，使测试不依赖 MethodChannel。默认平台适配首先支持 Android/iOS；其他 Flutter 平台须注入适配器并单独验证，不宣称自动兼容。

### D6. 管理 SDK 是无认证状态的 Fetch 客户端

构造配置为 `baseUrl`、可选 `apiPrefix`、必填 `getAccessToken` 和可选 `fetch` / 请求 ID 生成器。token provider 在每次调用时执行，可异步取得应用已经管理的可用令牌；provider 负责其自有续期策略，SDK 不保存 Token。provider 抛错、空令牌或请求在等待期间被取消，均不发匿名请求或迟到请求。

每个方法接受 `signal` 和可选 requestId，SDK 设置 Bearer、X-Request-Id 及必要的 JSON headers。使用标准 Fetch 的语义：自定义 fetch 必须返回含非 2xx 状态的原始 Response，不能先消费 body，也不能自带 401 重试。SDK 解析响应一次，将 API 错误转换为 `AccountAdminApiError`；局部配置、provider、取消、网络、协议失败分别有可区分错误。不存在 SDK 级自动 HTTP 重试，包括 GET 和写操作。

401、403、429、503 原样交给应用决策；SDK 不删除会话，不导航，不弹通知。服务端响应 request id 优先用于诊断，缺少时保留本次请求 id。SDK 校验响应的关键结构，保留未知枚举和 nullable 字段，不能凭 TS 类型断言把畸形响应当成功。204 不解析 JSON；非 JSON 错误不吞掉 HTTP 状态。

分页只发送 page_size、page_token、show_deleted 等明确参数并返回当页结果；不自动拼接列表。filter 提供受支持字段和操作符的类型化组合器以及明确的原始 filter 入口；字符串正确转义，最终仍由服务端解析。审计只建模正式认证 DTO，保留 result 和 nullable 字段；flow_id 缺省保持省略。审计 INVALID_PAGE_TOKEN 的清空旧列表、回到第一页和迟到响应处理由应用负责，SDK 不暗中重复请求。

所有管理请求使用 `cache: no-store`，明确禁止重定向，不设置 SDK 结果缓存、持久存储或默认日志。身份 reveal 结果只返回给本次调用。package 导入不访问 window、不创建 UserManager、不发网络请求；标准 Fetch/AbortController 是运行时边界，无 React/Keycloak 依赖。相比注入应用的通用 apiFetch，这避免错误体被两次读取和隐式刷新造成写请求重放。

### D7. Git 子目录与 TS 源码是正式分发方式

发布标识和 SDK 的 manifest 版本相互对应：首版分别为 `1.0.0`，tag 为 `sdk/flutter/v1.0.0`、`sdk/admin-ts/v1.0.0`；它们与服务 `v1.0.0` 属于不同版本轴。两个 SDK 的后续版本可独立演进。发布不可移动或重写 tag；修复另发版本。以下为发布后的引用形式，并不声明 tag 已存在：

```yaml
dependencies:
  accountkit_flutter:
    git:
      url: https://github.com/bbxx111/accountkit.git
      ref: sdk/flutter/v1.0.0
      path: sdks/accountkit-flutter
```

```json
{
  "dependencies": {
    "@accountkit/admin-client": "git+https://github.com/bbxx111/accountkit.git#sdk/admin-ts/v1.0.0&path:/sdks/accountkit-admin-ts"
  }
}
```

也可将 ref 换成完整 commit SHA。pnpm 的子目录及参数组合能力见 [官方 package sources 文档](https://github.com/pnpm/pnpm.io/blob/main/docs/package-sources.md)。这属于已选 pnpm 的能力，不承诺 npm/yarn 具有同一语法。真实环境使用仓库实际可访问的 Git URL，认证由 Git/CI 环境提供，依赖 URL 不包含密钥。

Flutter manifest 可声明 `publish_to: none`；TS package 使用 ESM，exports/types 指向 `./src/index.ts`，禁止消费者依赖私有深层路径。TS 使用可独立转换的语法、严格类型检查，不依赖生成类型声明、dist、prepare/postinstall 或安装时构建。依赖方使用 TS/Vite 进行类型检查和生产转换；不承诺将 node_modules 中的原始 TS 直接交给任意 Node/浏览器运行。包名 scope 仅是 Git 依赖标识，不要求公共 registry 命名空间。

Flutter 开发使用独立 pubspec/锁文件；管理包使用独立 package.json、pnpm 锁文件和 tsconfig。消费应用提交自己的锁文件并在 CI 冻结安装。锁文件不能代替固定 ref。实现阶段将 Flutter/Dart、Node、pnpm、TS、Vite 的实测精确版本写入工具版本清单和 CI；初始支持组合为 Flutter 3.47.1/Dart 3.13.1 与现有消费者采用的 pnpm 11.24.0、TS 5.8、Vite 6 系列，Node 使用满足该 pnpm 版本 engines 的 LTS 版本。具体 Node 补丁及开发依赖补丁由首次安装验证后锁定，不把未执行的安装描述成兼容证据。

拒绝以根目录 npm 包＋prepare 复制子目录或提交 dist 作为默认分发：它们增加安装执行、构建同步和仓库根配置耦合。以后如需 registry/纯 JS 分发，应另行评估消费者需求。

### D8. 协议、真实服务、隔离消费分别验收

验证分四层，并在本变更 `verification.md` 分别记载命令、环境、结果和限制，原始日志进入 `.test-output/`：

1. 包内静态检查与单元测试：Flutter 模型、状态机、竞争、失败注入与 fake；TS 路由/编码、响应、错误、取消、token provider 和无自动重试。覆盖登记检查独立运行。
2. 真实服务契约测试：使用现有 accountsvc 启动/迁移方式和独立 PostgreSQL/Redis，测试验证码 sender、受控 IdP verifier 与管理员 OIDC JWKS 测试提供方。两种 SDK 实际发 HTTP 请求，不用手写 HTTP 冒充 SDK；种子数据优先通过公开 API 建立。必要的服务 fixture 放在 `sdks/testsupport`，沿用根 Go module，只作为测试工具。默认 accountsvc 契约与配置可选能力的嵌入式测试宿主分别报告。真实第三方授权不纳入自动门禁。
3. Git 隔离消费：构造仓库外的临时 Flutter 应用和 TS/Vite 应用，以完整 SHA＋子目录依赖安装，禁止 path override/workspace link。PR 阶段可将待验内容制成临时 Git 仓库的固定提交，经仅监听本机的临时 Git 服务提供给两种包管理器；不创建真实发布 tag、不提交到工作分支。再次执行冻结安装，并完成 analyze/typecheck、测试、Vite 生产构建和 Flutter 平台构建。Linux 验证 Android debug，macOS 验证 iOS 无签名构建；包内纯逻辑测试不冒充原生构建。
4. 原有 Go 门禁仍独立执行。SDK CI 不能删除或降低既有 Go 单元/数据库/服务测试要求。

新增 SDK workflow 和 `scripts/verify-client-sdks.sh`，版本显式固定。触发条件包含 SDK、HTTP handler、OpenAPI 和影响契约的领域代码；push 与 pull_request 沿用 Go workflow 的 paths-ignore，仅排除 `openspec/**` 和 `docs/superpowers/**`，保留 workflow_dispatch。PR 过滤依据整个 PR 差异，含代码的 PR 不因最后一次提交只有文档而跳过。强制门禁缺少工具或数据库等依赖应失败并解释，不跳过后报成功。

Go workflow 在上述文档排除之外，仅额外排除 `sdks/accountkit-flutter/**` 与 `sdks/accountkit-admin-ts/**`，避免纯客户端改动重复执行完整 Go 门禁。`sdks/testsupport` 属于根 Go module，不排除整个 sdks 目录；Go fixture、服务端和共享契约变化继续触发。目录统一为 sdks，Git 子目录引用同步变更；公开包名及 sdk/flutter/v…、sdk/admin-ts/v… tag 前缀保持独立。

真实验收至少包含：验证码登录、补全、刷新与 scope 变化、资料及 merge patch、身份绑定/换绑/解绑、当前/其他 session、删除/恢复；管理端包含分页/filter、角色403、reveal、session revoke、审计字段及错误形态。mock 单独覆盖所有 35 个操作的请求/响应形状及真实场景不易稳定制造的网络、取消、并发和依赖故障。受控第三方凭据需覆盖各内置 credential 形状，自定义 kind 需证明不会被 SDK 的封闭枚举拒绝。

远程 tag 可获取性在正式发布后再用真实 Git URL验证，必须与发布前的本地 Git 隔离验收区分。服务兼容矩阵只登记实际测试结果，不从 SDK 版本号推断兼容。测试 fixture 不能成为 SDK 运行时依赖。

## Risks / Trade-offs

### 实施时对既有契约的确认（2026-11-01）

- 根 README 明确当前代码仅供自有产品接入，尚未授予外部复用许可。两个 SDK 的 LICENSE 沿用这一保留权利边界；管理包标记 `UNLICENSED`。本变更不擅自增加 MIT 等开源授权，未来对外授权需单独决定。
- 管理 filter 的正式 tokenizer 不支持引号内转义。组合器对可表达值加引号并通过 URL 参数编码保留特殊字符；包含双引号等无法按当前协议表达的值在本地拒绝，不生成服务端无法解析的反斜线转义，也不修改服务端语法。原始 filter 入口仍保留。
- 管理 API 错误的默认 `Error.message` 与 stack 使用固定安全描述；原服务端 message 通过只读、不可枚举的 `serverMessage` 显式读取，避免 Node 对象检查绕过 `toString()` 而打印敏感正文。HTTP status、reason、等待值和 request ID 仍可直接读取。
- Flutter 注入 Dio 时仅借用其 `httpClientAdapter`，SDK 使用独立 Dio 配置、headers 与拦截器，避免匿名账号端点继承调用方默认 Bearer；不继承其 BaseOptions、interceptors 或 transformer。借用 adapter 的生命周期仍由调用方负责，SDK 释放不关闭它。请求级自定义应通过 SDK 的公开 options/provider 入口完成。
- 本机实际为 Flutter 3.47.4 / Dart 3.13.3、Node 24.21.0。采用实际补丁组合完成本机验证并记录精确依赖；设计初始 Flutter 3.47.1 / Dart 3.13.1 组合不据此宣称已经验证。
- PR 原生 CI 确认锁定的 device_info_plus 13.3.0 需要 Xcode >=26.1.1；macos-15 runner 默认 Xcode 16.4 无法编译其系统 API。iOS CI 从工具清单显式选择并校验 Xcode 26.1.1，真实无签名构建已通过；构建成功不等于真机运行或签名发布验收。

- 手工模型与服务迭代可能漂移 → operationId 覆盖登记、关键结构解码测试及真实服务门禁共同约束；不依赖单一快照测试。
- refresh 旋转和持久化存在竞争 → 单一所有者、epoch/revision、串行存储及可控时钟/延迟写测试；失败必须可观察。
- 会话同步失败会造成暂时旧 scope → 成功结果与同步状态分开，应用可显式恢复；不自动重做已成功的敏感操作。
- Git 子目录和原始 TS 增加对工具链的要求 → 明确支持 pnpm/TS/Vite 组合并以隔离消费测试验收，其他组合不作未经验证的承诺。
- 平台存储和元数据插件增加构建成本 → 核心可注入、平台适配可替换，分别验证纯逻辑、Android 和 iOS。
- UserInfo 成功可能被应用误当作所有服务准入 → 接入文档明确各服务仍验证 issuer/audience/有效期与角色；本 SDK 的 401/403 由应用处理。
- 真实服务测试需要多依赖 → 重用既有服务验证基础设施并提供一条入口；必需依赖不可用时报告失败，不用 mock 替代真实验收。

## Migration Plan

1. 在本分支增加两包、覆盖登记、独立消费者夹具、SDK CI 和文档，保持 Go 使用方的构建入口。
2. 完成全部 SDK 与既有 Go 验收，记录真实服务与本地 Git 隔离证据；本次 OpenSpec 文档阶段不把任务标为实现完成。
3. 合并后按仓库发布流程分别发布固定 SDK tag，执行真实远程 Git 消费验证并记录兼容矩阵。创建/推送 tag 是独立发布动作，不由实现任务自动触发。
4. 下游用另一变更切换包名/import/配置、接入 OIDC UserInfo 并移除旧封装和探针。上游不提供旧包名兼容层。
5. 回退时消费方将 ref 和锁文件恢复到已验证的 SDK 版本；不移动发布 tag，不回滚或更改数据库。首次接入失败可暂缓下游切换。
