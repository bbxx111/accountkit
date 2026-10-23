# 支付宝 App 极简登录 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 accountkit 和 accountsvc 增加支付宝 App 极简授权码登录、固定身份模式及可靠的空资料补填。

**Architecture:** 在 `user/idp` 内完成普通公钥配置校验、RSA2 换码及可降级资料查询，不接触数据库。领域服务复用已有事务与资料 helper，仅为 ALIPAY 的成功登录补填空字段；库与 accountsvc 沿用统一凭证入口。

**Tech Stack:** Go 1.26.5，标准库 RSA/HTTP/JSON，现有 pgx/PostgreSQL、Redis、OpenSpec、观测适配器。

**Spec:** [OpenSpec design](../../../openspec/changes/archive/2026-10-24-add-alipay-app-sign-in/design.md)、[proposal](../../../openspec/changes/archive/2026-10-24-add-alipay-app-sign-in/proposal.md)、该目录下五份 `specs/**/spec.md`。规格优先；本计划补充实施接口与测试拆分。

## Global Constraints

- 仅 App 极简版；统一 `kind=ALIPAY`、`credential={app_id,code}`，不增加 authInfo、Web 回调或客户端项目。
- 默认 UNIONID；subject 为 `unionid:<union_id>` 或 `openid:<app_id>:<open_id>`，不自动回退、迁移或合并。
- 普通公钥模式；RSA 至少 2048 位，PEM 私钥 PKCS#1/PKCS#8、公钥 PKCS#1/SPKI；构造无 I/O。
- 换码预算 10 秒，资料预算独立 3 秒，每份响应最大 64 KiB，禁止重定向与自动重复兑换。
- 原始响应节点字节验签；歧义/重复字段拒绝；不信任未验签的身份或错误分类。
- 资料失败允许已验证身份继续；外层上下文取消则终止；不保存或泄露 code、token、密钥、签名或原始响应。
- 只有 ALIPAY 后续成功登录补填账号空字段；并发非空值优先；pending 保留净化证明，完成/重放不调用上游。
- 保留 FACEBOOK 未归档能力及其他提供方既有资料语义；无 schema、迁移或冻结源修改。
- 使用 GOWORK=off；任务先失败测试再最小实现；任务完成须有测试证据和独立评审。
- 项目任务追踪以 OpenSpec `tasks.md` 为准；2026-10-24 用户确认以 Task 5 的库侧 HTTP 契约替代原 6.2/6.3，真实 App/分组转为宿主清单且不得标为通过。提交不代表推送或归档。

## Review Focus

- RSA2 对编码前参数签名，验签原始节点而不是重编码 JSON；Task 2 覆盖空格、斜线转义、重复键和多节点。
- 被消费 code 不能在网络/事务重试中重用；Task 2/3 断言请求次数与 DB 唯一冲突重试。
- 资料超时可降级但外层取消不能创建会话；Task 2 测上下文，Task 3 测领域结果。
- 用户主动清空可补填，但用户并发提交的非空资料不能覆盖；Task 3 用真实 DB 锁与事务测试。
- migrate 未配置不需要支付宝，但显式非法库配置不忽略；Task 1/3 验证与现有配置解析兼容。

---

### Task 1: 类型和多应用配置

**Files:**
- Create: `user/idp/alipay_config.go`、`user/idp/alipay_config_test.go`、`config_alipay_test.go`
- Modify: `enum/enum.go`、`enum/enum_test.go`、`config.go`、`README.md`

**Interfaces:**
- Produces `idp.AlipayApp { AppID, PrivateKey, AlipayPublicKey string }`。
- Produces `idp.AlipaySubjectMode` 与 `AlipaySubjectModeUnionID/AlipaySubjectModeOpenID`。
- Produces `idp.AlipayConfig { Apps []AlipayApp; SubjectMode AlipaySubjectMode; GatewayURL string }`。
- Produces `idp.ValidateAlipayConfig(config AlipayConfig) error`，内部解析结果可由 Task 2 构造器复用。
- Root package 公开同名 App/模式别名与常量；Config 增加 `AlipayApps`、`AlipaySubjectMode`、`AlipayGatewayURL`，增加私有 `alipayConfig() idp.AlipayConfig` 作为后续装配接口。
- Config.providerKinds 追加 ALIPAY；Task 3 才实现具体提供方装配。

- [x] **Step 1: 编写失败测试。** `TestAlipayConfigValidation` 断言重复/缺密钥、弱/非 RSA、证书 PEM、坏模式、非回环 HTTP 网关拒绝且错误不含合成密钥；合法 PKCS#1/PKCS#8/SPKI 接受。`TestAlipayConfigFromEnv` 断言自定义前缀、APP_IDS、多行 PEM、默认模式、显式空模式拒绝及 OIDC 禁止占用 ALIPAY。
- [x] **Step 2: 运行 RED。** `go test ./enum ./user/idp . -run 'Alipay|Identity.*Builtin' -count=1`，记录新接口缺失或新增断言失败。
- [x] **Step 3: 实现接口。** 校验 AppID 为 16 位数字；应用切片复制；用独立逐 AppID 环境变量传 PEM，不增加文件读取；规范网关后默认生产 URL。保留现有其他配置和错误映射习惯。
- [x] **Step 4: 运行 GREEN 与相关回归。** `go test ./enum ./user/idp . -count=1`，缺数据库用例的跳过需明确记录；检查 README 配置表、根包公开类型与测试一致。
- [x] **Step 5: 提交和报告。** `feat(config): add alipay app configuration`；报告 RED/GREEN、公开接口及未完成的装配范围。

### Task 2: 支付宝验证器、签名和资料降级

**Files:**
- Create: `user/idp/alipay.go`、`user/idp/alipay_api.go`、`user/idp/alipay_json.go` 及对应 `*_test.go`
- Modify: `user/idp/policy.go`、`user/idp/idp.go`、`observability/classification.go`；复用 Task 1 已完成的 `alipay_config.go`，无需为复用强制修改该文件。

**Interfaces:**
- Consumes Task 1 的 `AlipayConfig` 和密钥解析。
- Produces `AlipayOptions { Config AlipayConfig; HTTP *http.Client; Logger *slog.Logger; Observer observability.Observer; Now func() time.Time }`。
- Produces `NewAlipay(options AlipayOptions) (*Alipay,error)`，`*Alipay` 实现 Provider。
- Produces固定 `MethodAlipay="alipay"`、`DependencyVerifyAlipay="verify_alipay"`、`DegradationAlipayProfileUnavailable="alipay_profile_unavailable"`，Task 3 接入领域映射及投影。
- `alipay.go` 负责凭证分派、身份模式、资料关联和降级；`alipay_api.go` 负责 RSA2、表单、HTTP 和安全错误；`alipay_json.go` 负责有界严格 JSON/原始节点提取，避免通用框架化。

- [x] **Step 1: 编写协议失败测试。** 本地服务独立验证 POST form 原始值的规范签名，独立签名响应；覆盖两个 App、两模式、缺标识、换码 `error_response`、不重试、重定向、64 KiB 上限、HTTP 故障、原始节点空白/转义、重复键/双节点/坏签名及无签名错误不能判用户凭证无效。用官方接口错误表核对固定合成样本。
- [x] **Step 2: 运行 RED。** `go test ./user/idp -run Alipay -count=1`，记录缺提供方导致的预期失败。
- [x] **Step 3: 实现换码与指纹。** 采用设计规定的字段、UTC+08:00 timestamp、RSA2；禁止不可信请求目的地与跳转；按已解析公钥 SPKI 指纹排序计算配置指纹，不含私钥。
- [x] **Step 4: 编写并运行资料失败测试 RED。** 有效签名/OpenID匹配才采信昵称头像；正常缺字段不降级；资料超时、权限、坏签名、缺可关联 OpenID/token 允许身份成功，外层取消返回取消。测试使用短外层 deadline 或受控 Transport，避免每次等待 10 秒；独立预算的值用明确调用方 deadline 断言覆盖。
- [x] **Step 5: 实现资料流程。** `auth_token` 只在函数栈用于一次本次查询；不进入 Identity、错误或日志。返回已验证 Identity 后由领域净化。更新包注释及观测固定枚举。
- [x] **Step 6: GREEN、自查与提交。** `go test ./user/idp ./observability/... -count=1`，检查合成秘密在日志/错误/JSON中不可见；提交 `feat(idp): verify alipay app authorization codes`。

### Task 3: 领域补填、库服务装配和指标

**Files:**
- Modify: `accountkit.go`、`user/service_idp.go`、`user/service_signin_completion.go`、`user/observability.go`、`user/identity_policy.go`、`internal/accountsvc/config.go`、`observability/internal/metricdata/project.go`
- Create/Modify tests: `user/service_alipay_test.go`、`accountkit_alipay_test.go`、`internal/accountsvc/config_test.go`、`observability/internal/metricdata/project_test.go`

**Interfaces:**
- Consumes Task 2 的 `NewAlipay(AlipayOptions)` 与三个观测枚举；无 Provider 或 HTTP DTO 接口变化。
- Reuses `providerProfileFrom`、`seedEmptyProviderProfile`、`refreshProviderMeta`、现有资料事务锁与完成证明。
- Produces完全可用的库/accountsvc装配，仍无 authInfo 或回调端点。

- [x] **Step 1: 编写失败领域测试。** 在真实 PostgreSQL fixture 中用合成 Provider 验证首次失败后登录补填、两个字段各自空值判断、无历史profile兜底、主动清空后补填、其他 IdP 无后续补填、已有身份与 pending 已归属分支补填。另测并发 PATCH 非空值胜出、状态/匿名化拒绝、后段故障回滚、双 App UnionID 并发唯一与完成重放不重复网络调用。
- [x] **Step 2: 运行 RED。** `go test ./user -run Alipay -count=1`，必须带真实一次性 DB DSN 才可认定事务断言已执行。
- [x] **Step 3: 实现最小领域修改。** ALIPAY结果在进入 pending 前净化；只在成功登录及完成的已有归属分支调用空字段helper，新身份创建保持已有行为；重复绑定只更新元数据。使用本次证明且不在事务中联网。
- [x] **Step 4: 编写装配/观测失败测试并实现。** 配置到Provider、启用策略、纯ALIPAY无SMTP、动态安全错误名、migrate边界、禁用历史身份、指纹失效；同步metrics允许列表与归一化。断言资料降级身份依赖成功、最终认证依领域结果计数，取消/缺字段不误记降级。
- [x] **Step 5: GREEN与相关回归。** `go test ./user . ./internal/accountsvc ./observability/... -count=1`，记录真实DB与仅单元测试范围，保留现有Facebook/OIDC/Google、会话和生命周期测试。
- [x] **Step 6: 提交和报告。** `feat(user): integrate alipay sign-in and profile backfill`，列出锁、事务、pending和观测的证据。

### Task 4: 集成说明、门禁与交付验证

**Files:**
- Modify: `README.md`、`docs/go-integration.md`、`docs/accountsvc.md`、`docs/compatibility.md`、`deploy/accountsvc/.env.example`、相应指标文档及 `scripts/required-tests.txt`
- Create: `docs/alipay-sign-in.md`
- Update: OpenSpec `tasks.md`、`verification.md`；综合验证发现既有并发换绑测试遗漏规格允许的404结果时，仅修正 `user/service_identity_replacement_transaction_test.go` 的输家断言并保留成功数/身份不变量。

**Interfaces:**
- Consumes Tasks 1–3的实际公开类型、配置和验证行为，以实际实现核对所有示例。
- Produces无秘密客户端/宿主说明及完整的实际验证记录；按后续 Task 5 的职责修正，真实 App 条件缺失时保持宿主清单未执行，库任务使用自动契约证据完成。

- [x] **Step 1: 完成各组配套文档。** 写明App极简 SDK、auth_user/state/回跳/未安装/取消、code一次性、Flutter极简桥接、普通公钥、分组和模式切换、资料降级与空字段语义，引用官方入口，示例用合成值。
- [x] **Step 2: 更新必需测试门禁。** 新增必须真实运行的顶层集成测试加入 required-tests，保留全部原条目；执行 generated和迁移历史检查，确认无生产SQL差异。
- [x] **Step 3: 运行全套验证。** `GOWORK=off go build ./...`、`go vet ./...`、`go test -count=1 ./...`；使用一次性真实数据库运行事务/并发，再按 `scripts/verify.sh` 完成race与恢复演练，Windows缺CGO时使用项目Docker验证路径。
- [x] **Step 4: 核对验收职责。** 按后续 Task 5 修正的范围，OpenSpec 6.2/6.3 以库 HTTP 自动契约验收；真实 App/分组仍属于宿主未执行清单，不把模拟接口当作真实平台端到端完成证据。
- [x] **Step 5: 独立最终评审、记录与提交。** 处理实质发现，更新verification实际命令/结果，运行OpenSpec全量strict、git diff --check；提交本任务文件，不推送/合并/归档。

### Task 5: 库侧公共 HTTP 契约验收（2026-10-24 职责边界修正）

**Files:**
- Create: `accountkit_alipay_http_test.go`（外部 `accountkit_test` 包）
- Modify: `scripts/required-tests.txt`，仅追加两项选择器，保留全部原条目。
- Root updates: 本变更 proposal/design/specs/tasks/verification 与 `docs/alipay-sign-in.md` 的库/宿主验收边界。

**Interfaces:**
- 使用现有 `minimal()`、`integrationInstance`、`Auth.New/Migrate/Start/Close` 与公开 `EndUserHandler`，不调用私有领域服务替代 HTTP 登录，不注入假的 Provider。
- 本地签名网关独立校验每个应用的 RSA2 请求并签署真实结构响应；真实 PostgreSQL/Redis，schema/Redis 前缀按测试隔离，不调用 FLUSHDB。缺依赖普通套件明确 skip，完整门禁必须实际 PASS。
- 测试名称：`TestAlipayHTTPLoginContract`、`TestAlipayHTTPMultiAppIdentityContract`。此任务补充现有行为的回归覆盖，无生产修改；测试可立即通过，不人为制造 RED。

- [x] **Step 1: 单 App 公共入口。** 真实 facade 装配并挂载 `/v1`，携带设备头 POST `/v1/users:signInWithIdp`，验证账号、JWT 读取 `/v1/users/me` 以及 `/v1/token` 刷新；独立网关验证请求签名且消费 code 一次。覆盖错误签名/重复code拒绝、资料故障仍成功、后续空值补填和非空字段保留；通过HTTP可见结果及必要只读SQL验证，不打印token或原始签名。
- [x] **Step 2: 多 App 与模式。** 配置两套独立应用私钥及支付宝验签公钥，网关按 AppID 校验签名；UNIONID 同值时经两次HTTP登录进入同账号、只一条活跃ALIPAY身份并合并两个OpenID；OPENID 模式同文本跨App分离，后续收到UnionID不升级；跨App登录仍遵循首次资料失败后补填空字段及保留非空值。
- [x] **Step 3: 实际验证与门禁。** 在真实PG/Redis下执行两项定向HTTP测试及root包回归，执行新增测试的Linux race；把两项完整包名追加required-tests，运行testgate确认不是skip/no-tests。root运行OpenSpec strict与文档引用检查；无生产/schema变化不重复恢复演练。
- [x] **Step 4: 提交与评审。** 提交本任务测试/门禁，报告准确环境、命令、结果及库侧能证明的范围。root进行独立评审后记录库验收完成，真实SDK、权限、回跳和控制台分组检查移到宿主清单且保留未执行事实，不冒充已验证真实平台。
