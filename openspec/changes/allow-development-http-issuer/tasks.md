## 1. oidcjwt 公共层

- [x] 1.1 `Options` 新增 `AllowHTTP`；新增 `ValidPlainIssuer`，`ValidIssuer` 保持只接受 HTTPS；`Verifier` 保存标志，`New`、`fetchJSON`、`discover` 的地址校验据此允许 `http://`，重定向仍拒绝。验证：oidcjwt 测试覆盖未放开时 HTTP issuer 与 HTTP `jwks_uri` 仍拒绝、放开后经纯 HTTP `httptest.Server` 完成 discovery、JWKS 加载与 `iss` 匹配，`TestNewValidatesIssuer` 不改断言通过

## 2. adminauth 与服务配置

- [x] 2.1 `adminauth.Config` 新增 `AllowHTTP`，`Validate` 与 `New` 据此放开；`ServiceConfig` 新增 `AllowHTTP`；`loadAdmin`、`loadService` 以 `Mode == "development"` 赋值，`serviceAuthenticators` 透传。验证：adminauth 测试覆盖放开后 HTTP fixture 启动即加载成功、未放开仍拒绝；config 测试覆盖 development 下两个变量接受 HTTP 并置位，production 下仍以原变量名拒绝且不回显值

## 3. 启动 WARN

- [x] 3.1 run.go 新增 `warnPlainHTTPIssuers`，在 migrate 返回之后、管理员 discovery 之前调用；文案与属性按 design D4。验证：run 测试断言 development + HTTP issuer 时日志含两条 WARN 且不含 issuer 值，HTTPS issuer 时不出现

## 4. 文档与验收

- [x] 4.1 `docs/accountsvc.md` 配置表 `MODE`、`SERVICE_ISSUER`、`ADMIN_ISSUER` 条目与服务调用认证小节补充开发例外；`deploy/accountsvc/.env.example` 注释说明。验证：人工核对与 specs 一致
- [ ] 4.2 `GOWORK=off go build ./... && go vet ./... && go test -count=1 ./internal/accountsvc/...`、`openspec validate --all --strict`、`git diff --check`；独立审查；结果写入 `verification.md`
