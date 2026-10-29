## 1. 实现

- [x] 1.1 `TestRevealIdentity` 先断言 200 响应 `Cache-Control: no-store`（红），再在 `revealIdentity` 成功路径设置该头（绿）。验证：`go test ./httpapi/admin/` 通过
- [x] 1.2 `docs/openapi/admin.json` 的 `:reveal` 200 响应声明 `Cache-Control` 头（`const: no-store`），描述去掉对宿主缓存保护的依赖；`docs/openapi.md` 补一句。验证：`node scripts/check-openapi.cjs` 通过

## 2. 验收

- [x] 2.1 `GOWORK=off go build ./... && go vet ./... && go test -count=1 ./httpapi/...`、`openspec validate --all --strict`、`git diff --check`；结果写入 `verification.md`
