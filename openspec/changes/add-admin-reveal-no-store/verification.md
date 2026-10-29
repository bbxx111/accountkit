# add-admin-reveal-no-store 验收记录

日期：2026-10-29。分支 `fix/admin-reveal-no-store`，基线 develop `ad467a5`。来源：接入方（ai-food）v0.7.1 验收时观察到 `:reveal` 响应缺少 `Cache-Control: no-store`，经核对属实（处理器直接以 `apierror.WriteJSON` 写出）。

## 实现

- `httpapi/admin/identities.go`：成功路径写出前设置 `Cache-Control: no-store`；错误路径沿用 `apierror.Write` 的 no-store。
- `docs/openapi/admin.json`：`adminRevealIdentity` 200 响应声明 `Cache-Control` 头（`const: no-store`），描述改为“成功响应带 Cache-Control: no-store；宿主应对敏感响应实施日志保护”。
- `docs/openapi.md`：补充管理面 `:reveal` 响应带 no-store。
- 规格增量：`embedded-auth-package`「管理接口及审计兼容」补充要求与场景。

## 测试（TDD）

`TestRevealIdentity` 先增加头断言并确认失败（`Cache-Control=""`），实现后通过。

| 检查 | 结果 |
|---|---|
| `GOWORK=off go build ./...` / `go vet ./httpapi/...` | 通过 |
| `GOWORK=off go test -count=1 ./httpapi/...` | 全部通过 |
| `node scripts/check-openapi.cjs` | 通过（该脚本不校验响应头，头由处理器测试断言） |
| `openspec validate --all --strict` | 41 passed, 0 failed |
| `gofmt -l httpapi`、`git diff --check` | 无输出 / 通过 |

## 独立审查

待填（与同批的 caller_cancel 用例调整、真实环境验证补记一并审查）。

## 未验证项

- 未经真实网关验证响应头透传；接入方（Kong，无缓存插件）确认无需额外处理。
