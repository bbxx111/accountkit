# allow-development-http-issuer 验收记录

日期：2026-10-29。分支 `feat/development-http-issuer`，基线 develop `1bf43cb`，实现提交 `b415072`，审查修复提交见下。实际执行方式：由主会话顺序实施（范围小、文件集中），独立审查由单独的审查子代理完成。

## 实现范围核对

- `internal/accountsvc/oidcjwt`：`Options.AllowHTTP`；`ValidPlainIssuer` 新增，`ValidIssuer` 仍只接受 HTTPS；`Verifier.fetchJSON` 与 `discover` 的地址校验按标志放开 `http://`；`CheckRedirect` 保持拒绝。
- `internal/accountsvc/adminauth`：`Config.AllowHTTP`；`Validate` 选用对应校验；`New` 透传到 `oidcjwt.Options`。
- `internal/accountsvc/config.go`：`ServiceConfig.AllowHTTP`；`loadAdmin` / `loadService` 以 `Mode == "development"` 赋值（`Mode` 在二者之前解析）；错误信息与变量名不变。
- `internal/accountsvc/run.go`：`warnPlainHTTPIssuers` 在 migrate 返回后、管理员 discovery 前调用；`serviceAuthenticators` 透传 `AllowHTTP`。
- 文档：`docs/accountsvc.md`（`MODE`、`SERVICE_ISSUER`、`ADMIN_ISSUER` 行，新增「本地开发的明文 HTTP 签发方」，管理员认证段落），`deploy/accountsvc/.env.example` 注释。

## 测试（TDD，先红后绿）

新增测试：

| 测试 | 覆盖 |
|---|---|
| `oidcjwt.TestPlainHTTPIssuerRequiresAllowHTTP` | 未放开时 HTTP issuer 拒绝；`ValidPlainIssuer` 仍拒绝凭据/查询/片段/空白/其他 scheme |
| `oidcjwt.TestAllowHTTPVerifiesOverPlainHTTP` | 纯 HTTP `httptest.Server` 上 discovery、JWKS、`Parse` 成功；HTTPS 拼写的 `iss` 仍不匹配 |
| `oidcjwt.TestPlainHTTPJWKSURIRequiresAllowHTTP` | HTTPS issuer + HTTP `jwks_uri`：未放开拒绝且不访问明文端点，放开后成功 |
| `oidcjwt.TestAllowHTTPStillRejectsRedirects` | 放开后重定向仍拒绝 |
| `adminauth.TestAllowHTTPAcceptsPlainIssuer` | 未放开 `Validate`/`New` 拒绝且不取 JWKS；放开后启动即加载并通过角色校验 |
| `accountsvc.TestDevelopmentModeAcceptsPlainHTTPIssuers` | development 两个变量接受 HTTP 并置位；其他格式错误仍指出变量名、不回显值 |
| `accountsvc.TestProductionModeKeepsHTTPSIssuers` | 显式 production 仍拒绝 HTTP 并指出原变量名；HTTPS 下 production 不置位、development 置位 |
| `accountsvc.TestWarnPlainHTTPIssuers` | 两条 WARN 文案与 `settings` 属性；不写值；HTTPS 或功能未启用时不记 |
| `accountsvc.TestRuntimeWarnsPlainHTTPIssuersBeforeAdminDiscovery` | 运行时 WARN 先于 `initAdmin`，日志不含 issuer 值 |

既有测试不改断言：`oidcjwt.TestNewValidatesIssuer`、`adminauth.TestConfigAndStartupRejectUnsafeMetadata`（含 `http-jwks`）、`TestConfigServiceTokenValidation`（`issuer not HTTPS`）、config 的「production HTTP retains ADMIN_ISSUER」全部通过。夹具改造：`oidcjwt.newProviderWith` / `adminauth.newFixtureWith` 增加纯 HTTP 变体，`runWithPreload` 改为基于 `runServe`（配置改动在 `runConfig` 启动前完成）。

## 静态与单元检查

| 检查 | 结果 |
|---|---|
| `GOWORK=off go build ./...` | 通过 |
| `GOWORK=off go vet ./...` | 通过 |
| `GOWORK=off go test -count=1 ./...`（Windows，无 `SERVER_TEST_DB_DSN`，数据库用例按既有规则 skip） | 通过 |
| `GOWORK=off go test -count=1 ./internal/accountsvc/...` | 全部通过 |
| `gofmt -l internal/accountsvc` | 无输出 |
| `git diff --check` | 通过 |
| `openspec validate --all --strict` | 41 passed, 0 failed |

## 独立审查

审查范围 `1bf43cb..b415072`，由独立审查子代理执行（自行运行 build/vet/test、gofmt、`git diff --check`、`openspec validate --all --strict`，均通过）。结论：Critical 0、Important 0、Minor 4，可合并。处理：

1. `warnPlainHTTPIssuers` 以 `strings.HasPrefix(issuer, "http://")` 判断，而配置校验按 `url.Parse` 归一后的 scheme 判断，因此 development 下 `HTTP://…` 能通过校验却不记 WARN。已修：改为按解析后的 scheme 判断（`plainHTTP`），`TestWarnPlainHTTPIssuers` 补混合大小写用例。
2. `AllowHTTP` 放开后，`oidcjwt.New` / `adminauth.Validate` 对格式错误仍报「must be an absolute HTTPS URL」。按 design D5 保持文本不变；accountsvc 对外只报变量名，不受影响。未改。
3. `docs/features.md` 外部管理员 OIDC 行只写「本地 HTTPS discovery/JWKS」测试。已补「及 development 模式纯 HTTP」。
4. 4.2 未勾选、无 verification.md：本记录即为其产出。

修复后复跑 `GOWORK=off go vet ./internal/accountsvc/ && go test -count=1 ./internal/accountsvc/...` 通过，`gofmt -l` 无输出。

## 未验证项

- 未运行 Linux 镜像的完整 `verify.sh` / `verify-accountsvc.sh`（race、必需测试门禁、恢复演练）；本变更不涉及 SQL、生成代码或迁移，发布前按发布检查表执行。
- 未对真实 Keycloak 做端到端验证；接入方需保证容器内外看到同一 issuer 字符串。
