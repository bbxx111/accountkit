# add-admin-reveal-no-store 验收记录

日期：2026-10-29。分支 `fix/admin-reveal-no-store`，基线 develop `ad467a5`。来源：接入方（ai-food）v0.7.1 验收时观察到 `:reveal` 响应缺少 `Cache-Control: no-store`，经核对属实（处理器直接以 `apierror.WriteJSON` 写出）。

## 实现

- `httpapi/admin/identities.go`：处理器入口设置 `Cache-Control: no-store`，成功与错误响应一律带该头（`apierror.Write` 本身不设置 Cache-Control）。
- `docs/openapi/admin.json`：`adminRevealIdentity` 200 响应声明 `Cache-Control` 头（`const: no-store`），描述改为“成功响应带 Cache-Control: no-store；宿主应对敏感响应实施日志保护”。
- `docs/openapi.md`：补充管理面 `:reveal` 响应带 no-store。
- 规格增量：`embedded-auth-package`「管理接口及审计兼容」补充要求与场景。

## 测试（TDD）

`TestRevealIdentity` 先增加头断言并确认失败（`Cache-Control=""`），实现后通过；审查后把设置点移到处理器入口并补 404 响应断言。

| 检查 | 结果 |
|---|---|
| `GOWORK=off go build ./...` / `go vet ./httpapi/...` | 通过 |
| `GOWORK=off go test -count=1 ./httpapi/...` | 全部通过 |
| `node scripts/check-openapi.cjs` | 通过（新增 `adminRevealIdentity` 200 `Cache-Control` 常量断言） |
| `openspec validate --all --strict` | 41 passed, 0 failed |
| `gofmt -l httpapi`、`git diff --check` | 无输出 / 通过 |

## 独立审查

与 `caller_cancel` 用例调整、真实环境验收补记一并由审查子代理审查。本分支结论：Important 1、Minor 2。

1. Important：代码注释、design、proposal、规格增量与本记录称“错误响应沿用 `apierror.Write` 的 no-store”，但 `apierror.Write` 并不设置该头（审查以 404/400 探针确认为空）。已修：头改在处理器入口设置，成功与错误响应一律 no-store；测试补 404 断言；各处文字改为“该路由所有响应”。
2. Minor：design 称 `check-openapi.cjs` 不校验响应头，实际已对当前会话操作断言。已修：脚本增加 `adminRevealIdentity` 的同样断言，design 措辞更正。
3. Minor（范围说明，不扩展）：其他管理面响应（用户列表/详情、会话、审计）不含明文手机号/邮箱，维持 Non-Goals。

## 未验证项

- 未经真实网关验证响应头透传；接入方（Kong，无缓存插件）确认无需额外处理。
