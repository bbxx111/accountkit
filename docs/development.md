# 开发与验证

要求 Go 1.26.5、sqlc 1.31.1、PostgreSQL 17。Redis 测试使用 miniredis；实际宿主示例需独立 Redis。先设置 SERVER_TEST_DB_DSN 指向一次性测试数据库。

快速检查：GOWORK=off go test ./...。未提供数据库时部分数据库测试会跳过，不能据此宣称生产发布验证通过。

完整发布检查：还需 ACCOUNTKIT_RECOVERY_SOURCE_DSN 和 ACCOUNTKIT_RECOVERY_TARGET_DSN，分别指向两个专用空数据库，再运行 bash scripts/verify.sh。脚本执行迁移历史校验、独立构建、vet、race 测试、必需用例核验及恢复演练。缺少配置、所需测试 skip、历史 SQL 改写或恢复失败均返回非零。

Windows 没有 C 编译器时，可用 tests/Dockerfile 构建基于 golang:1.26.5、带 PostgreSQL 17 客户端的 Linux 验证镜像。CI 使用同一镜像；宿主需先安装 sqlc 1.31.1 并运行 bash scripts/check-generated.sh。恢复演练需要客户端读取集群标识的权限，限用于一次性测试库。

TestRecoveryFixture 是跨进程恢复演练的合成数据工具，普通测试运行时会明确 skip，不在常规必需用例列表中；verify-recovery.sh 分别执行 seed、verify、source-unchanged 三个阶段，每阶段均单独检查该用例实际通过。

独立宿主示例：examples/embedded/main.go。设置 ACCOUNTKIT_DEMO=1、ACCOUNTKIT_DATABASE_URL、ACCOUNTKIT_REDIS_ADDR 及 README 配置表中的 ACCOUNTKIT_ 前缀配置，再 go run ./examples/embedded。仅监听 127.0.0.1:8080；日志发送器仅供开发，管理端未注入验证器时返回 503。

源码清单与兼容对照见 docs/compatibility.md，迁移操作与恢复限制见 docs/migrations.md。两个产品的实际部署、服务商实机联调和发布 tag/push 独立进行。