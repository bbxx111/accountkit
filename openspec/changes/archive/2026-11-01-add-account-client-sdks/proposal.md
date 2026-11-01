## Why

accountkit 已提供正式的消费者与管理 HTTP 协议，但接入方仍需各自维护客户端、令牌刷新与登录补全流程，容易出现协议覆盖不齐、错误处理不一致和升级不同步。由协议维护方提供两个可独立消费的官方 SDK，并以 Git 固定版本和仓库子目录分发，可以让应用复用经过验证的账号接入行为。

## What Changes

- 新增 Flutter 消费者 SDK，目录为 `sdks/accountkit-flutter`，Dart 包名为 `accountkit_flutter`，入口为 `lib/accountkit_flutter.dart`；提供 `AccountApi`、`AccountClient`、`AccountController`，覆盖登录与补全、账号与扩展资料、重新认证、身份绑定/解绑/换绑、会话查询与撤销及注销/恢复。
- 将消费者会话运行时纳入 SDK：安全凭据存储、并发刷新合并、scope 驱动的四态、验证码与补全流程生命周期、取消和账号切换后的迟到响应隔离、类型化错误，以及可注入的设备信息和平台适配。业务请求通过明确的 Dio 认证适配入口复用同一会话，限制凭据发送范围和请求重放。
- 新增 TypeScript 管理 SDK，目录为 `sdks/accountkit-admin-ts`，包名为 `@accountkit/admin-client`，源码入口为 `src/index.ts`，提供 `createAccountAdminClient`；覆盖用户查询与生命周期操作、身份明文查看、会话管理、认证审计查询和分页/filter 构造。
- 管理 SDK 接收调用方提供的可用 Access Token 和可选标准 Fetch 实现，统一处理请求、DTO、请求 ID 和协议错误；取令牌失败不降级为匿名请求，不在 SDK 中登录、刷新、退出或导航，不自动重试管理写操作。HTTP 非成功响应由 SDK 解析，避免与调用方重复消费响应体。
- 两个 SDK 以正式 HTTP 契约及 `v1.0.0` 为首个兼容基线，建立逐端点覆盖表，核对身份换绑及已注册第三方提供方的凭证支持，不把既有接入方实现当成完整协议。默认适配 accountsvc 的 `/v1` 与 `/admin/v1`，允许配置服务地址及挂载前缀以接入遵守同一契约的嵌入式宿主；不依赖 Go 源码、数据库或特定网关。
- 采用 Git＋path 分发：Flutter 使用 Git URL、包路径和固定 ref；管理 SDK 使用 pnpm 的 Git ref＋`path:` 子目录依赖，直接导出 TS 源码，由消费项目的 Vite 等工具转换。正式接入不依赖本地工作区，不要求提交 `dist`、安装时构建或向 pub.dev/npm registry 发布。
- 两个 SDK 独立使用 SemVer，分别采用 `sdk/flutter/v<version>`、`sdk/admin-ts/v<version>` tag，维护服务兼容矩阵和升级说明；固定版本不可重写。Git 分发下的 npm scope 作为包标识，不以取得公共 registry 命名空间为前提。
- 增加协议/状态机测试、Flutter 测试替身、真实账号服务契约验证，以及独立消费项目从固定 Git ref 和子目录安装后的类型检查、测试与生产构建验证；扩展 CI、接入文档和项目指南，同时保持现有 Go 独立构建与发布门禁。

本次不新增管理员认证 SDK。管理员登录、续期和标准 UserInfo 由接入应用使用 `oidc-client-ts` 等 OIDC 客户端完成；角色映射、页面准入、路由与错误展示由应用负责。消费者第三方原生授权 SDK 的调用也留在应用侧，账号 SDK 只接收协议凭证。

本次不改变现有服务端 HTTP、Go API、认证授权或存储契约，不增加 UserInfo/管理员探针、内省或 purge-feed 客户端，不迁移数据库。下游应用切换依赖、删除旧包或探针由下游独立变更处理；不为下游旧包名提供兼容转发层。

## Capabilities

### New Capabilities

- `accountkit-flutter-sdk`：官方 Flutter 消费者账号 API、会话与流程运行时、业务请求认证适配、平台注入及测试替身的行为契约。
- `accountkit-admin-ts-sdk`：官方 TypeScript 管理 API、外部令牌提供、HTTP/DTO/错误处理、分页过滤及与应用认证生命周期的职责边界。
- `client-sdk-distribution`：两个 SDK 的名称和独立包边界、Git 子目录消费、TS 源码分发、独立版本与兼容矩阵，以及脱离源码工作区的安装和集成验证。

### Modified Capabilities

无。既有 `embedded-auth-package`、`accountkit-package-identity`、`accountsvc-admin-authentication`、身份/会话/资料等规格继续定义服务端行为；新 SDK 消费这些契约，不改变其要求。

## Impact

- 新增两个 SDK 目录及各自包清单、源码、测试、独立接入示例和文档。Flutter 引入 Dart/Flutter、Dio 和必要的平台存储/设备适配依赖；管理 SDK 使用 TypeScript 与标准 Fetch，不依赖 React、Keycloak 或 OIDC 客户端库。
- 扩展 `.github/workflows/` 及相关验证入口，声明并固定验证使用的 Flutter、Dart、Node、TypeScript、pnpm 和消费构建工具版本；保持 Go module、服务构建及现有数据库测试边界。
- 更新 README、接入/兼容性文档及 AGENTS，说明 SDK 与嵌入式 Go 库、accountsvc 的关系；记录各 SDK 的 HTTP 覆盖范围、可选宿主能力未配置时的错误，以及经过真实验证的服务版本。
- 下游以固定 Git tag 或 commit 引用包并提交依赖锁文件。SDK 发布与下游迁移分别验收；管理员 UserInfo 成功不代替各 API 服务的令牌校验和逐路由授权。
