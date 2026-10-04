# 开发与验证

要求 Go 1.26.5、sqlc 1.31.1、PostgreSQL 17。Redis 快速测试使用 miniredis；验证码轮换集成测试使用真实 Redis。先设置 SERVER_TEST_DB_DSN 指向一次性测试数据库。

快速检查：GOWORK=off go test ./...。未提供数据库时部分数据库测试会跳过，不能据此宣称生产发布验证通过。

完整发布检查：还需 ACCOUNTSVC_TEST_REDIS_URL 指向一次性真实 Redis（如 `redis://127.0.0.1:6379/0`），以及 ACCOUNTKIT_RECOVERY_SOURCE_DSN 和 ACCOUNTKIT_RECOVERY_TARGET_DSN，分别指向两个专用空数据库，再运行 bash scripts/verify.sh。脚本执行迁移历史校验、独立构建、vet、race 测试、必需用例核验及恢复演练。缺少配置、所需测试 skip、历史 SQL 改写或恢复失败均返回非零。

轮换定向验证：提供 SERVER_TEST_DB_DSN 与 ACCOUNTSVC_TEST_REDIS_URL 后运行 `GOWORK=off go test -count=1 . ./user/code -run 'TestCode(KeyRotationIntegration|RotationRedisIntegration)'`。两个集成用例仅操作各自随机 schema/Redis 前缀并局部清理，不调用 FLUSHDB。普通测试缺少真实依赖时明确 skip；完整验证与 CI 把这些用例列为必需，不允许缺验。

Windows 没有 C 编译器时，可用 tests/Dockerfile 构建基于 golang:1.26.5、带 PostgreSQL 17 客户端的 Linux 验证镜像。CI 使用同一镜像；宿主需先安装 sqlc 1.31.1 并运行 bash scripts/check-generated.sh。恢复演练需要客户端读取集群标识的权限，限用于一次性测试库。

TestRecoveryFixture 是跨进程恢复演练的合成数据工具，普通测试运行时会明确 skip，不在常规必需用例列表中；verify-recovery.sh 分别执行 seed、verify、source-unchanged 三个阶段，每阶段均单独检查该用例实际通过。

独立宿主示例：examples/embedded/main.go。设置 ACCOUNTKIT_DEMO=1、ACCOUNTKIT_DATABASE_URL、ACCOUNTKIT_REDIS_ADDR 及 README 配置表中的 ACCOUNTKIT_ 前缀配置，再 go run ./examples/embedded。仅监听 127.0.0.1:8080；日志发送器仅供开发，管理端未注入验证器时返回 503。

正式可选服务入口为 `cmd/accountsvc`，支持 `serve` 与 `migrate`；配置和本地 Compose 步骤见 [accountsvc 服务手册](accountsvc.md)。服务不读取开发示例的 DATABASE_URL/REDIS_ADDR 变量，连接使用 `ACCOUNTSVC_DATABASE_URL` 与 `ACCOUNTSVC_REDIS_URL`，库参数仍使用 `ACCOUNTKIT_`。

服务严格验证执行 `bash scripts/verify-accountsvc.sh`，要求 `SERVER_TEST_DB_DSN` 与 `ACCOUNTSVC_TEST_REDIS_URL` 指向一次性测试依赖。SMTP 和 OIDC 使用隔离协议 fixture，不需要真实提供方凭据。Linux 验证实际 SIGTERM；Windows 普通测试不能代替该进程验证。

源码清单与兼容对照见 docs/compatibility.md，迁移操作与恢复限制见 docs/migrations.md。宿主实际部署、服务商实机联调和发布 tag/push 独立进行。
