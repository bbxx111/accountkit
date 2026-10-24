# Tasks

## 1. 配置项

- [ ] 1.1 `config.go` 增加 `Config.ProviderProfileBackfill *bool`（`nil` 默认 `true`）与 `ConfigFromEnv` 的 `PROVIDER_PROFILE_BACKFILL` 解析；`config_test.go` 覆盖默认值、`false`、非法值报告 `<prefix>PROVIDER_PROFILE_BACKFILL`，`go test -run Config .` 通过
- [ ] 1.2 `user.Deps.ProviderProfileBackfill *bool`（`nil` 视为 `true`），`Service` 保存解析值；`accountkit.go` 把 `Config` 值传入 `user.Deps`；装配测试确认 `false` 能传到服务，`go test .` 与 `go test ./user/` 单元部分通过
- [ ] 1.3 accountsvc：确认 `ACCOUNTKIT_PROVIDER_PROFILE_BACKFILL` 非法值在 serve 与 migrate 前报告该配置名且不泄露值，补充 `internal/accountsvc` 测试；`deploy/accountsvc/.env.example` 列出该项与默认值；`go test ./internal/accountsvc/` 通过

## 2. 统一补填逻辑

- [ ] 2.1 先写失败测试（真实 PostgreSQL）：配置开启时 GOOGLE、OIDC、FACEBOOK 已有身份再次登录补填空显示名/头像且不覆盖非空值；配置关闭时 ALIPAY/QQ/WEIBO/GOOGLE 已有身份登录、`BindWithIdp`（新建身份与同账号重复绑定）、补全关联已有账号均不写账号资料但仍更新 `provider_meta.profile`；配置关闭时直接注册与补全新建账号仍填充；运行确认这些测试在改动前失败
- [ ] 2.2 实现：新增 `backfillProviderProfile(newAccount)`；`createProviderIdentity` 不再调用 `seedEmptyProviderProfile`，改由 `signInVerified`、`finalizeSignIn`、`BindWithIdp`（含同账号分支）按“账号是否本次新建”调用；删除 `refillsProfileOnSignIn`，`verifyIdp` 对所有提供方统一净化声明；2.1 的测试通过
- [ ] 2.3 更新原先断言“集合外提供方不补填”的测试（如 `TestGoogleProfileFillHintAndOpenIDCoexistence` 及支付宝/QQ/微博对照用例）为按配置判定；保留并发 PATCH、冻结/注销竞争、pending 并发、最后阶段失败回滚等原子性用例并扩展到至少一个非集合提供方；`SERVER_TEST_DB_DSN` 下 `go test -count=1 ./user/ .` 全部通过且相关用例未 skip
- [ ] 2.4 新增必需顶层测试追加到 `scripts/required-tests.txt`，`go vet ./...` 与 gofmt 无输出

## 3. 文档

- [ ] 3.1 README 配置表加入 `PROVIDER_PROFILE_BACKFILL`（默认 `true`），资料补填说明改为统一规则；`docs/go-integration.md`、`docs/accountsvc.md` 同步；核对链接与配置名一致
- [ ] 3.2 `docs/compatibility.md` 新增本次行为变更段落：默认开启后 GOOGLE/OIDC/FACEBOOK 后续登录补填、主动清空会被重新填充、关闭后绑定与已有账号登录均不补填、新建账号始终填充；修正第 48、58 行附近“登录补填提供方集合”的现行描述
- [ ] 3.3 `docs/alipay-sign-in.md`、QQ、微博接入指南中“后续登录补填”的描述改为引用统一规则并注明受 `PROVIDER_PROFILE_BACKFILL` 控制；`rg "登录补填提供方"` 只剩历史记录中的内容

## 4. 集成验证

- [ ] 4.1 `GOWORK=off go build ./... && go vet ./... && go test -count=1 ./...`（带 `SERVER_TEST_DB_DSN`）通过；`openspec validate --all --strict` 通过
- [ ] 4.2 运行完整 `bash scripts/verify.sh`（Linux 验证镜像，含 race 与恢复演练），结果与未验证项写入 `verification.md`

## Workflow follow-up

- 通过 PR（Create a merge commit）合入 develop 后，单独执行归档。
