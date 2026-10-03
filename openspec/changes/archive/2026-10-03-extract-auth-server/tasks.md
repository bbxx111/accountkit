## 1. 固定来源与模块身份

- [x] 1.1 在 docs/compatibility.md 记录源提交 7b4c4ecfdba4a05d54810aa2152b40d5c7da00c2、完整源文件清单和公开 API/路由/默认配置/错误码到原测试的映射；逐项覆盖本变更全部规格，任何遗漏视为未完成。
- [x] 1.2 获取用户指定的远程仓库 URL，确定可访问的正式 module 路径并记入 README；验证路径与托管规则一致，再进行任务 2.1，不代替用户创建远程或发布。

## 2. 提取完整模块

- [x] 2.1 将固定基线快照 的全部生产代码、测试、go.mod/go.sum、SQL 和 sqlc.yaml 提取到仓库根目录，保留当时的包名及子目录（后续包名调整见 rename-accountkit-package）；比较文件清单与源 0001 哈希，确认没有遗漏和 SQL 改写。
- [x] 2.2 使用 1.2 确定的路径修改 go.mod、自引用 import、sqlc override 和生成代码；以 GOWORK=off 执行 go list -deps ./... 和 go build ./...，确认不再依赖外部源码模块或 go.work。
- [x] 2.3 更新 doc.go/README 的导入和构建说明，执行 sqlc generate 验证生成配置；确认生成结果不含旧 module 路径，且查询、模型和枚举语义未变化。
- [x] 2.4 在独立 PostgreSQL 环境设置 SERVER_TEST_DB_DSN，运行 GOWORK=off go test -race -count=1 ./...；保留所有源测试并核对真实数据库测试实际执行，记录功能清单对应结果。

## 3. 接管和宿主契约验证

- [x] 3.1 在 tests/testdata/source-baseline/ 保存源 0001 SQL、来源记录及合成旧数据/令牌夹具，在 compatibility_test.go 新增 TestSourceDatabaseTakeover；由源格式建立 version=1 非空库，验证重复 Migrate 后账号、身份、会话、审计不变且旧访问/刷新令牌仍可用；执行 go test -run TestSourceDatabaseTakeover -count=1 . 通过。
- [x] 3.2 在 isolation_test.go 新增 TestIndependentInstances，复用真实 PostgreSQL 与受控 Redis 测试两个 schema/前缀/令牌域/密钥；断言验证码、刷新宽限、撤销、用户和维护隔离，以及跨实例令牌被拒绝；执行 go test -run TestIndependentInstances -count=1 . 通过。
- [x] 3.3 在 accountkit_db_test.go 中核对或补充管理员未配置返回 503、普通消费者登录仍可用、匿名化回调失败同时回滚业务表与认证表的测试；只补现有测试未覆盖的行为，运行对应测试通过。
- [x] 3.4 在独立临时宿主 module 编译 examples/embedded/main.go 示例，展示 PoolConfig、Config/Deps、Migrate、Start、挂载两个 Handler 和 Close；用本地 replace 仅定位 accountkit，验证不需要外部源码工作区、go.work 或 Keycloak。

## 4. 构建与发布准备

- [x] 4.1 创建 scripts/verify.sh 和明确列出必需数据库测试的检查入口，执行 GOWORK=off 构建及 go test -race -json -count=1 ./...；正常配置返回零，缺少 DB 配置、必需测试缺失或 skip 返回非零。
- [x] 4.2 按已确定托管平台接入上述验证入口，文档说明 Go 1.26.5、PostgreSQL/Redis 与 sqlc 的可复现准备步骤；在干净环境验证独立构建和生成检查通过。
- [x] 4.3 完成 docs/release-checklist.md，明确 harden-migration-safety 未完成不能首次生产发布、服务商实机验证由宿主负责；交付功能对照和实际测试结果，不创建 tag/push，修改范围限于本仓库。

## 验收记录

本次提取与迁移安全加固的联合包级验收证据统一保存在[迁移安全变更的 verification.md](../2026-10-03-harden-migration-safety/verification.md)。
