## 1. 迁移门禁收缩（第 3 层：工具）

- [ ] 1.1 在 `internal/migrationcheck/main.go` 删除 `frozen`、`legacyReleases`、`publishedInitialReleases` 及 `sourceBaseline` 逻辑，`releasedHistory` 跳过主版本为 0 的 tag；保留 `initialFrozen`、可达 tag 迁移不可改写与版本递增三项检查。验证：`bash scripts/check-migrations.sh` 在仍带 v0 tag 的检出上通过。
- [ ] 1.2 重写 `internal/migrationcheck/main_test.go` 与 `published_initial_test.go`：删除源快照与各代 pin 用例，新增“v0 tag 被忽略”“v1.x tag 改写迁移失败”“当前 0001 哈希不一致失败”“新版本号必须递增”用例。验证：`GOWORK=off go test ./internal/migrationcheck/...` 通过。
- [ ] 1.3 删除 `tests/testdata/source-baseline/` 整目录与 `migrations/testdata/legacy_integrated_v1.sql`；核对仓库内无残留引用。验证：`rg 'source-baseline|legacy_integrated_v1' --glob '!openspec/changes/archive/**' --glob '!docs/superpowers/**'` 无结果。

## 2. 运行时预检收缩（第 3 层：库）

- [ ] 2.1 重写 `migrations/baseline.go` 的 `checkBaseline`：只保留 dirty 优先与“版本不在当前源”两条，删除流程/审计内联 SQL 及对四个子预检的调用；错误文案改为描述版本超出当前源。删除 `identity_kind_baseline.go`、`profile_attributes_baseline.go`、`purged_user_baseline.go` 及 `checkSessionContextBaseline`。验证：`GOWORK=off go build ./... && go vet ./...` 通过。
- [ ] 2.2 按 design D6 逐测试函数三分 `migrations/*_test.go`：删除只构造旧结构并断言拒绝的用例；保留新安装、重复 Up、并发首次部署、锁等待/取消、dirty 阻断、latest+1 升级与失败 probe、UnsafeReset 用例；混合用例拆出当前行为部分。新增“版本超出当前源被无写拒绝”与“dirty 优先”用例。验证：`SERVER_TEST_DB_DSN` 下 `GOWORK=off go test -count=1 ./migrations/...` 通过。
- [ ] 2.3 同步 `scripts/required-tests.txt`：删除退役用例条目，加入新用例；在 verification.md 附表列出每条删除的测试名、类别与替代覆盖。验证：`bash scripts/verify.sh` 的必需测试检查通过，无 skip 掩盖。

## 3. 原项目格式测试退役与格式覆盖核对

- [ ] 3.1 删除根 `compatibility_test.go` 中读取 source-baseline fixture 的用例；不读 fixture 的用例按实际目的归入对应包或保留。验证：`GOWORK=off go vet ./...` 通过，`required-tests.txt` 已同步。
- [ ] 3.2 核对 `pii`、`tokens`、`user/code` 的单元测试覆盖版本化 AES-GCM 密文、HMAC 摘要匹配、refresh 哈希与密钥轮换；缺口补单元测试。验证：覆盖核对结果写入 verification.md，相关包测试通过。

## 4. Deprecated 兼容入口删除

- [ ] 4.1 删除 `ids.IdentityFlow`、`user.Deps.IdentityFlowContext`（含回退读取逻辑）、`(*user.Service).CleanupIdentityFlows`、`sender.SMSSender`、`sender.EmailSender`、`sender.Message`；更新所有内部引用、`examples/` 与 `cmd/accountsvc`。验证：`GOWORK=off go build ./... && go vet ./...` 通过，`rg 'IdentityFlow\b|IdentityFlowContext|CleanupIdentityFlows|SMSSender\b|EmailSender\b|sender\.Message\b' --type go` 只剩 `Deps.SMSSender/EmailSender` 字段名。
- [ ] 4.2 删除或改写依赖别名的测试（含 `TestCodeSenderCompatibility`、`TestSignInCompletionContextCompatibility` 等），改为断言正式名称行为；同步 `required-tests.txt`。验证：`GOWORK=off go test -count=1 ./user/... ./ids/... .` 相关用例通过。

## 5. 公开 Go API 冻结前盘点

- [ ] 5.1 用 `GOWORK=off go doc -all` 导出 design D8 列出的各包公开面，逐包记录命名不一致、不应导出、文档缺失三类发现，形成盘点清单（放 `.test-output/` 原始输出，结论写入 design.md 附录“接受的 API 现状”）。验证：清单覆盖全部列出的包。
- [ ] 5.2 处理盘点中决定修改的项（仅命名/导出范围/文档，不加功能）；涉及规格要求的补对应 delta。验证：`go build/vet` 通过，修改项与接受项在 design.md 附录一一对应。

## 6. HTTP 调用流程文档与示例测试

- [ ] 6.1 新增 `examples/httpflows` 示例测试：httptest 挂载 `EndUserHandler()`，合成发送器与 miniredis，覆盖首次登录到 pending 补全、并发刷新协调与宽限内同一 pair、重放失败退出、重新认证只换 access、注销/`:revokeOthers`/解绑导致当前会话退出；断言结构与状态机，不断言随机值。验证：`SERVER_TEST_DB_DSN` 下 `GOWORK=off go test -count=1 ./examples/httpflows/...` 通过并加入 `required-tests.txt`。
- [ ] 6.2 编写 `docs/http-client-flows.md`：四条流程的时序图与合成值请求/响应片段，引用 6.1 的测试名；只讲协议层状态机，不替平台决定存储。验证：文档内链接有效，README 与 `docs/openapi.md` 指向该文。

## 7. 文档重写为 v1.0.0 基线（第 1 层）

- [ ] 7.1 将九份 `docs/releases/v0.*.md` 压缩为 `docs/releases/pre-1.0-history.md`（每个 tag 的提交、日期、一句话摘要，以及未正式使用、不提供升级路径、tag 已删除的声明），删除原九份。验证：`rg 'releases/v0\.' --glob '!openspec/changes/archive/**' --glob '!docs/superpowers/**'` 无结果。
- [ ] 7.2 重写 `docs/migrations.md`：只描述当前初始基线、预检两条规则、升级前检查、诊断失败、回退选择、UnsafeReset、历史检查与恢复演练；删除逐轮授权叙述。验证：文内无 v0.x tag、SHA 或“本轮授权”字样。
- [ ] 7.3 重写 `docs/compatibility.md`：只保留功能对照表与当前 Go/HTTP 契约边界；删除开发版之间升级章节与三节“历史提取索引”。验证：文件内无指向 v0 发布说明或源快照的链接。
- [ ] 7.4 重写 `docs/release-checklist.md` 为可复用通用检查表（发布前门禁、发布动作、消费确认、状态措辞约定、产品上线清单），加入“远端不存在 v0 tag”的人工核对项；版本专属门禁留待发布准备变更追加。验证：无 v0.x 版本门禁勾选记录。
- [ ] 7.5 重写 README「仓库与发布状态」及各功能节、`docs/features.md`、`docs/openapi.md`、`docs/go-integration.md`、`docs/gateway-integration.md`、`docs/accountsvc.md`、`docs/development.md`、`docs/metrics.md` 与各提供方文档的版本表述，改为描述当前状态（v1.0.0 候选），删除“在 vX 之上新增”的增量叙述；两份 OpenAPI 的 `info.version`/`info.description` 改为 v1.0.0（候选）。验证：`rg 'v0\.[0-9]+\.[0-9]+' README.md docs --glob '!docs/releases/pre-1.0-history.md' --glob '!docs/superpowers/**'` 无结果；`node scripts/check-openapi.cjs` 通过。
- [ ] 7.6 修复 `openspec/changes/archive/` 与 `docs/superpowers/plans/` 中因文档重写失效的相对链接（只改链接，不改正文）。验证：对 `docs/` 与 `openspec/` 运行本地 Markdown 链接检查无失效链接。

## 8. 代理规则改写

- [ ] 8.1 按 design D9 重写 AGENTS.md「迁移安全」节，删除源快照冻结、v0 tag 不可移动及全部逐轮“直接改 0001”授权记录；修正其他节中引用源快照、旧 tag 或旧预检语义的句子；「技术栈与目录」表删除 `tests/testdata/source-baseline/` 行。验证：`rg 'source-baseline|v0\.[0-9]+\.[0-9]+|直接改 0001' AGENTS.md` 无结果。

## 9. 完整验证与交付

- [ ] 9.1 运行 `openspec validate --all --strict`、`bash scripts/check-generated.sh`、`bash scripts/check-migrations.sh`、`node scripts/check-openapi.cjs`。验证：全部通过并记录输出。
- [ ] 9.2 在 Linux 验证镜像（`tests/Dockerfile`）执行 `bash scripts/verify.sh`、`bash scripts/verify-accountsvc.sh` 与 `bash scripts/verify-recovery.sh`（三个一次性数据库）。验证：完整库/服务 race、必需测试门禁、恢复 seed/verify/source-unchanged 全部通过。
- [ ] 9.3 编写本变更 `verification.md`：实际运行的检查、测试删除附表、格式覆盖核对、API 盘点结论、未验证项；明确远端 v0 tag/Release 删除与 v1.0.0 发布准备为后续单独确认的动作。验证：记录与实际输出一致。
