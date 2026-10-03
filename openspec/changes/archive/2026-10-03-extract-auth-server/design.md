## Context

动机见 proposal.md。固定快照标识为 7b4c4ecfdba4a05d54810aa2152b40d5c7da00c2，清单与 SQL 保存在 tests/testdata/source-baseline/。根门面提供 Config/Deps/New、Users、ConsumerHandler/AdminHandler、RequireScope/RequireRecentAuth、PrincipalFrom、Migrate/Start/Close、RunMaintenanceOnce/PoolConfig 等。go.mod 为 Go 1.26.5，使用 Chi、pgx/v5、go-redis/v9、sqlc 和 golang-migrate。现有测试使用 SERVER_TEST_DB_DSN，缺失时会跳过数据库测试。

## Goals / Non-Goals

**Goals:** 完整搬迁模块并通过功能对照和真实数据库接管测试证明兼容；使构建不依赖外部工作区的 go.work。
**Non-Goals:** 不改认证模型，不新建统一账号中心，不在本变更中调整产品数据或引入批量资料查询、业务删除前置钩子等新能力。

## Decisions

1. **根目录为单 Go module。** 相比先拆 core/HTTP/storage 多模块，沿用目录可减少导出类型和生成代码变动。当前 module 路径为 github.com/bbxx111/accountkit，根包统一为 accountkit；原先保留历史包名的决定由 rename-accountkit-package 取代，调用迁移见 docs/compatibility.md。
2. **固定源快照，机械提取后验证。** 保留完整子目录 anonymize、audit、email、enum、httpapi、ids、maintenance、migrations、phone、pii、session、tokens、user，以及根配置、门面、测试、go.mod/go.sum、sqlc.yaml。导入、sqlc enum override 和生成文件同步更新；不只替换手写代码。README/doc.go 重写仓库关系，源 SQL 不修改。
3. **行为以源代码及测试为兼容基线。** 新增 docs/compatibility.md，列出源路由、公开 API、配置默认值、错误码、状态、存储格式和对应测试。消费者挂载 /v1、管理端挂载 /admin/v1。特别保留 X-Device-Id、重新认证不返回 refresh token、恢复专用 scope、可选管理员接口行为。
4. **保持依赖注入。** 保留短信/邮件、HTTPClient、ClientIP、RequestID、Audit、AdminVerifier/AdminPrincipal、Anonymizers。具体阿里云/SMTP 服务商实现、Keycloak 和产品配置由宿主负责；服务装配与核心库分离；可选 accountsvc 通过库的公开接口装配，运行时由独立变更实现。匿名化回调与业务表须同一数据库，并共用 pgx.Tx；schema 隔离不妨碍这一事务契约。
5. **保持已有失效语义。** Redis 吊销读取异常的 fail-open、审计队列失败不阻断认证仍沿用源行为，写入兼容说明并保留测试；不在提取中悄悄改成 fail-closed。不同实例必须主动设置不同 schema、Redis 前缀、issuer/audience 和密钥，默认值不提供自动隔离。
6. **存储基线不重建。** 0001 SQL 与版本表名称不变，增加从源 SQL 创建、带已有账号和令牌的接管测试。Auth.Migrate 的 search_path、Redis Ping、数据库升级、历史密钥检查顺序保持兼容；数据库迁移成功不代表后续依赖检查必然成功。
7. **持续集成入口先与托管平台解耦。** scripts/verify.sh 在 Linux CI 上执行 GOWORK=off 的构建、单元和 race 集成测试，要求 SERVER_TEST_DB_DSN，检查必须执行的测试不能 skip；托管平台接线在远程地址确定后完成。复制已有测试而不是为机械搬迁重写实现。

## Risks / Trade-offs

- [源测试中可能包含工作区相对路径] → 扫描所有导入、生成设置、测试资源和示例，在独立工作目录验证。
- [相同版本号但表结构被手工修改] → 支持范围限定为未漂移的固定源基线，接管前核对结构；不把版本号当成结构校验和。
- [测试用第三方凭证无法代表线上接入] → 包级使用本地 HTTP 假服务，产品侧另做真实短信、微信、Apple、OIDC 联调。
- [仅包提取通过就被当成生产就绪] → README 和发布清单明确 harden-migration-safety 是生产发布前置。

## Migration Plan

先记录源文件清单与 SQL 哈希，再复制完整模块，替换 module/import/sqlc 路径，补独立示例和 CI，运行完整兼容验证。之后执行 harden-migration-safety。包版本按语义版本发布，首次采用 v0.1.0 候选，实际 tag/push 不属于本规划执行动作。宿主接入另立变更，账号和业务关联数据迁移单独设计。仅模块路径改变时不要求用户重新登录；数据库、Redis 或密钥更换时另行评估。

## Open Questions

已确认远程仓库 https://github.com/bbxx111/accountkit.git，Go module 为 github.com/bbxx111/accountkit，CI 使用 GitHub Actions。