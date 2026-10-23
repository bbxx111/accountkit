# 验收记录：add-identity-provider-registry

分支 `feat/identity-provider-registry`，最后核验提交 `dd92291`。本记录只写实际运行的检查；未完成项单列。

## 当前固定哈希

- 当前 `migrations/0001_init.up.sql`：`4c6b565e336aba88db5f6bf219407b026a7c1b438057b61470377005ed2509c5`
- 当前 `migrations/0001_init.down.sql`：`2f26c7dc5f5c5cfe2ee5a9967884fc70c7f213418a602fb2535621904f251f9b`（未变）
- 冻结源、v0.3.0、v0.4.0 已发布定义的固定值未变；develop 当前不包含发布 tag，`check-migrations.sh` 对当前基线的检查通过。

## 已运行的检查

| 检查 | 环境 | 结果 |
|---|---|---|
| `GOWORK=off go build ./... && go vet ./...` | Windows 宿主 | 通过 |
| `go test -count=1 ./...`（含 `SERVER_TEST_DB_DSN` 与 `ACCOUNTSVC_TEST_REDIS_URL`） | Windows 宿主，一次性 PostgreSQL 17.5 / Redis 7 容器 | 通过；见下方偶发项 |
| `bash scripts/check-generated.sh` | Windows 宿主，sqlc 1.31.1 | 通过 |
| `bash scripts/check-migrations.sh` | Windows 宿主与 Linux 验证镜像 | 通过 |
| `node scripts/check-openapi.cjs` | Node 24 | 通过（同时补上 Google 变更遗留的 `PendingSignIn.hint_email`） |
| `openspec validate --all --strict` | — | 29 项通过 |
| `go test -race -json -count=1 ./...` | Linux 验证镜像（Go 1.26.5、gcc、pg_dump 17），专用 Docker 网络连接一次性 PostgreSQL/Redis，提交 `c966c2b` | 0 失败；测试级跳过仅 `TestRecoveryFixture`（普通套件预期跳过）与 `TestProfileAttributesUnreadableFile`（容器以 root 运行，文件权限不可模拟） |
| `go run ./internal/testgate scripts/required-tests.txt` | 对上一行的 race 结果，使用 `dd92291` 的清单 | 通过 |

必需测试清单：新增 12 个依赖数据库的测试；`httpapi/enduser` 的 `TestCredentialOneof`、`TestCredentialIdp`、`TestCredentialExplicitNullIsAbsent` 随旧请求格式删除，分别由 `TestCredentialAnchor`、`TestCredentialProvider`、`TestDocumentedCredentialExamplesDecode` 取代（一对一替换，未减少条目）。

## 过程中发现并修复的问题

- `TestCodeKeyRotationIntegration` 仍用旧请求体：该用例依赖真实 Redis，初期本地快速测试缺少 Redis 而跳过，在 race 运行中暴露；已改为信封格式（`9938a84`）。
- 三轮代码复核的实质问题均已处理：预检改为物化限定 schema 后再反解析约束；宿主 Provider 的非哨兵错误隔离为固定 500；畸形凭证计为客户端拒绝；结果类型/subject 不一致按凭证无效统一审计并计为依赖拒绝；OIDC 空 issuer 不再可被缺失 `iss` 的 token 匹配；策略输出保持内置类型历史顺序。设计补充见 design.md“实施补充”。
- 规格收窄：`accountsvc-runtime` 明确“未设置任何 OIDC 变量时 migrate 不受影响；已列出类型但配置不完整时 migrate 与 serve 同样失败并指出变量名”，并有测试固定。

## 偶发与环境问题（如实记录）

- 前两轮 race 运行经 Docker Desktop 的 `host.docker.internal` 连库，分别有 `TestMigrateAuditConfigurationMinimumPoolCapacity/old_v1_empty` 与 `TestAuditFailureDoesNotChangeAuthentication/backend_error` 出现连接超时（`dial error: timeout`）；改用专用 Docker 网络后第三轮 0 失败。
- Windows 宿主一次全量并行运行中出现 `pii`、`session/grace` 构建失败（`panic during panic`）及 `TestReplacementConcurrentMutations` 时序断言失败；单独重跑（后者 `-count=3`）均通过，判断为本机负载下的偶发，未改代码。

## 未完成项

- **任务 5.2 未完成**：`scripts/verify.sh` 尚未一次性完整跑通。第三轮在 race 与测试之后因必需测试清单仍含旧名而在门禁处退出，恢复演练（`verify-recovery.sh`，pg_dump/pg_restore）没有执行；修正清单后的第四轮在运行中因宿主内存不足被会话中止，结果无效。需在内存充足时于新的空恢复库上重跑完整脚本。
- `scripts/verify-accountsvc.sh`（服务严格验证，含真实 SIGTERM）本次未运行；`internal/accountsvc` 的单元与集成测试已在上述全量与 race 运行中通过。
- 未在 GitHub Actions 上运行；以上均为本地结果。
