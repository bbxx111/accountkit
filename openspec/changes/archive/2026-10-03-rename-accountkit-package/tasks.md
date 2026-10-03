## 1. 包名迁移

- [x] 1.1 根包、外部测试、示例、诊断与源码文件名统一为 accountkit；用 GOWORK=off go build ./...、go vet ./... 和默认导入宿主编译核验。
- [x] 1.2 保留维护锁历史标识与冻结 SQL/manifest；通过差异检查和 go run ./internal/migrationcheck 核验。

## 2. 文档独立性

- [x] 2.1 清理全部仓库 Markdown 文档中的外部产品叙述，更新文件引用及迁移说明；用全文检索和链接检查核验。
- [x] 2.2 在 README、AGENTS 及相关规格明确 accountsvc 可选、库集成仍独立可用、服务运行时尚待实现；人工核对叙述一致。

## 3. 交付验证

- [x] 3.1 运行 GOWORK=off go test -count=1 ./...、OpenSpec 严格校验、git diff --check；记录通过项与数据库用例跳过情况，完成独立审查。

## 验证记录

2026-10-03 本轮实际完成：

- `GOWORK=off go build ./...`、`go vet ./...`、`go test -count=1 ./...` 返回 0；Go 缓存使用被忽略的 `.test-output/rename-go-cache`。
- `go list -f '{{.Name}}' .` 返回 `accountkit`；独立临时 module 的默认导入与显式旧别名宿主均编译通过。
- `go run ./internal/migrationcheck`、`openspec validate --all --strict`（3 个变更通过）及 `git diff --check` 通过。
- 29 份仓库 Markdown 文档的指定外部产品引用和本地链接检查通过；独立审查的规格符合性及代码/文档质量均通过。
- 未配置 `SERVER_TEST_DB_DSN`，数据库用例跳过；本轮未执行真实数据库集成、race 或完整恢复演练。历史记录不作为本轮通过依据。
- 修改保留在任务分支，未提交、推送或归档；accountsvc 本轮仅明确可选服务定位。
