# Tasks

基线：远端 develop `ac3eff1f076f52d4b58b8e43269939342c4a0416`。已按仓库 apply 工作流实施，完成分步及整体独立审查；实际测试、环境跳过项和既有主规格问题见 [verification.md](verification.md)。任务仅在完成并核对证据后勾选。

## 1. 配置解析及兼容模式

- [x] 1.1 在 internal/accountsvc/config.go 及新的 service_clients 配置单元实现统一/旧模式选择和孤立字段检测；先补失败测试，验证未设置、显式空注册表、新旧混用（含 []/{}）、空白旧占位及旧模式原样通过。
- [x] 1.2 实现别名与 ID、CSV 方法/权限、必填字段和未知字段校验；表驱动测试覆盖重复、空项、大小写、下划线别名、ID 冒号/空白/长度、未知 identities.resolve 和通配符拒绝，错误只含变量名。
- [x] 1.3 接入规范 Base64 秘密、跨客户端重复拒绝及重叠轮换；测试短秘密、非规范编码、方法未启用却有秘密、同客户端双秘密及跨客户端冲突。
- [x] 1.4 复用 issuer/audience 隔离校验并适配统一 Bearer 客户端集合；在 config_service_token_test.go/config_development_issuer_test.go 中验证 Basic-only、半配置、production HTTPS、development WARN、audience 冲突及 migrate 完全忽略新增字段。
- [x] 1.5 更新 deploy/accountsvc/.env.example 和 docs/accountsvc.md 的分项变量语法；以合成凭据解析 Basic-only/Bearer-only/双方法示例，验证示例只包含实际注册权限及旧配置互斥说明。

## 2. 共用认证与能力授权

- [x] 2.1 扩展 internal/accountsvc/serviceauth 的不可变客户端注册表及 RequiredPermission 决策，旧配置路径保持独立；先写认证矩阵测试，覆盖同身份双方法双权限、Basic 未知/错误、有效 Bearer 未登记、已登记但方式关闭、缺权限403及零业务调用。
- [x] 2.2 复用 basicclients 固定长度常数时间秘密匹配及 oidcjwt，避免重新实现 JWT/缓存；验证配置快照不随输入集合修改、轮换遍历、错误 issuer/audience/kid/iat/exp、算法拒绝及日志脱敏。
- [x] 2.3 实现统一模式挑战生成：全注册表方法并集、无凭据/多头、无效 Bearer、方法关闭、Basic/Bearer403、503；验证 exact WWW-Authenticate、no-store，并保留旧模式挑战回归。
- [x] 2.4 更新 docs/accountsvc.md 服务认证决策表与配置迁移说明；核对身份凭据和接口权限分离、有效 Basic 缺权限403、禁用方法401、未知有效 azp403及不自动降级规则与测试一致。

## 3. 接口与运行时装配

- [x] 3.1 在 run.go 的 serviceAuthenticators 中共享注册表及 OIDC verifier，为 introspection/purgefeed 分别声明权限；使用 run_service_token_test.go 验证 Basic-only 无 OIDC I/O、提供方失败不阻止启动、共享刷新和旧模式回归。
- [x] 3.2 接入 introspection/handler.go，保留 OAuth 请求解析及数据库会话内省；handler 测试验证未配置503、已认证Basic缺权限403、双方法成功、认证在方法/内容/业务解析之前，以及被查询消费者 token 无效与调用方无权的区别。
- [x] 3.3 接入 purgefeed/handler.go，保留 AIP 分页和数据库读取契约；测试同客户端访问双端点、只授内省的客户端被拒、未配置503、JWKS503/Retry-After及分页错误不变。
- [x] 3.4 补齐启动弃用/未配置能力警告与配置快照行为；在 run_unconfigured_test.go、config_service_token_test.go 等验证一次性日志不含秘密、空注册表正常启动、单能力授权、重启撤权与 readiness 隔离。
- [x] 3.5 更新 Compose 的变量透传、examples/remoteauth 与 docs/compatibility.md；通过配置/示例测试验证旧模式原授权保留，新模式启用、只旧配置回退与同 ID 跨接口合并需显式授权，文档不承诺自动回退或热加载。

## 4. 综合验收

- [x] 4.1 使用现有隔离 OIDC fixture 与真实 PostgreSQL/Redis，扩展 service_token_integration_test.go 和 purge_feed_integration_test.go；验证同一身份双方法双端点、拒绝矩阵、JWKS故障/轮换、配置迁移、数据库吊销和 feed 清理事务保持，记录环境与结果。
- [x] 4.2 运行 GOWORK=off 下 go vet ./...、go test -count=1 ./... 及 scripts/verify-accountsvc.sh；发布前按仓库要求执行 scripts/verify.sh 的 race、DB与恢复门禁。缺依赖或 skip 分项记录为未验证，不宣称完整通过。
- [x] 4.3 核对 sdks/http-coverage.json 对服务内部接口的覆盖/排除登记与协议测试；无消费者或管理员接口变化时记录无需修改 SDK 的证据，不增加客户端运行依赖。
- [x] 4.4 运行 openspec validate --all --strict（已知基线有 7 份主规格的长度警告，须分别记录基线问题与本变更结果，不擅自扩大范围），逐项对照四份 delta、设计决策和实际差异；完成代码审查、更新 verification.md 并核对仅实际完成的任务已勾选。

## Workflow follow-up

- 已按用户 apply 指令完成实现与验收；18 项任务证据见 verification.md。
- 实现和验收完成后，按用户单独指令归档；提交、推送、发布、部署均不由本任务自动执行。
