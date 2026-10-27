## 1. 配置读取

- [x] 1.1 先写失败测试：非空 `ACCOUNTKIT_*` 变量使 serve 与 migrate 的 `LoadConfig` 失败、错误含旧名与新名且不含值；以 `ACCOUNTSVC_` 提供库配置时可正常加载。验证：测试在改动前失败
- [x] 1.2 `config.go` 引入 `envPrefix`，`ConfigFromEnv`、`env()`、错误名映射、`safeLibraryConfigError` 改用该前缀；`LoadConfig` 开头拒绝旧前缀；`run.go` 错误名改名。验证：1.1 测试通过，`go vet ./internal/accountsvc/...` 通过
- [x] 1.3 重名检测测试（design D3）：收集服务配置名，断言不以 `ALIPAY_`/`OIDC_` 开头且库 `ConfigFromEnv("ACCOUNTSVC_")` 不读取它们；对一个已知库名做反向对照证明测试有效。验证：测试通过，临时加入同名配置时失败
- [x] 1.4 accountsvc 与 `cmd/accountsvc` 测试中的库配置变量改名；`configEnvironment` 与进程夹具 `environment()` 继续清除 `ACCOUNTKIT_*`。验证：`GOWORK=off go test -count=1 ./internal/accountsvc/... ./cmd/...` 通过（无数据库时数据库用例 skip，另见 3.1）

## 2. 部署与文档

- [x] 2.1 `deploy/accountsvc/compose.yaml`、`.env.example` 改名。验证：`grep ACCOUNTKIT_ deploy/` 无结果
- [x] 2.2 `docs/accountsvc.md`、`docs/alipay-sign-in.md`、`docs/qq-sign-in.md`、`docs/weibo-sign-in.md` 中 accountsvc 变量改名，库示例前缀保持；`docs/compatibility.md` 增加迁移小节；`add-service-bearer-auth` 设计中的消费者 issuer/audience 变量名同步。验证：人工核对，`grep -rn ACCOUNTKIT_` 剩余结果均为库/示例/测试基础设施用途

## 3. 验收

- [x] 3.1 Linux 镜像 + 一次性 PG/Redis 运行 `scripts/verify-accountsvc.sh`；运行 `go build ./...`、`go vet ./...`、`openspec validate --all --strict`；结果写入 `verification.md`
