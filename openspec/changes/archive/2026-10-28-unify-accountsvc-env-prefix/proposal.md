## Why

accountsvc 现在同时使用两个环境变量前缀：库配置 `ACCOUNTKIT_`、服务配置 `ACCOUNTSVC_`。部署方需要记住每个变量属于哪一层，模板与文档也分成两套。库的前缀本来就由宿主选择，accountsvc 作为宿主用自己的名字作唯一前缀最自然；目前尚无正式部署，适合现在切换。

## What Changes

- **BREAKING** accountsvc 的库配置改为以 `ACCOUNTSVC_` 读取（`ConfigFromEnv("ACCOUNTSVC_")`），例如 `ACCOUNTKIT_JWT_KEYS` 改为 `ACCOUNTSVC_JWT_KEYS`；服务配置名不变。
- serve 与 migrate 在发现任何非空 `ACCOUNTKIT_` 变量时 SHALL 启动失败并指出变量名（不回显值），避免可选配置在改名后被静默忽略。
- 增加测试保证服务配置名与库配置名不重叠，且服务配置不使用库按 `ALIPAY_`、`OIDC_` 动态生成的名字空间。
- 更新部署模板、accountsvc 文档、相关提供方文档中 accountsvc 的变量名，并在兼容指南写迁移说明。
- 直接嵌入库的宿主不受影响：`ConfigFromEnv(prefix)` 仍由宿主选择前缀，示例与库文档继续以 `ACCOUNTKIT_` 为示例前缀。
- 进行中的 `add-service-bearer-auth` 设计中引用的消费者 issuer/audience 变量名同步更新。

## Capabilities

### New Capabilities

（无）

### Modified Capabilities
- `accountsvc-runtime`：「配置校验与安全启动」改为单一 `ACCOUNTSVC_` 前缀并拒绝旧前缀；其余引用 `ACCOUNTKIT_` 变量名的服务配置 requirement（身份策略、Facebook、OIDC、QQ、微博、支付宝、资料补填等）更新为新变量名。

## Impact

- 代码：`internal/accountsvc/config.go`、`run.go` 及其测试，`cmd/accountsvc` 测试；库代码不变。
- 部署：`deploy/accountsvc/compose.yaml`、`.env.example`；接入方（ai-food）须改名。
- 文档：`docs/accountsvc.md`、`docs/alipay-sign-in.md`、`docs/qq-sign-in.md`、`docs/weibo-sign-in.md`、`docs/compatibility.md`。
- 不涉及数据库、令牌或协议。v0.6.0 已发布的 accountsvc 使用旧前缀，升级须按迁移说明改名。
