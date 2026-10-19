# 审计校验职责调整验收记录

日期：2026-10-18。范围：按用户明确授权直接修改当前未发布 0001，仅调整审计校验职责；其他表 CHECK 保持。

## 当前状态

实施完成。本次本地构建、vet、全套真实数据库 race 测试、必需用例门禁、迁移历史、生成一致性、恢复演练和 OpenSpec 严格校验均通过；独立只读审查无实质问题。2026-10-18 已同步两个主规格并归档至 openspec/changes/archive/2026-10-18-audit-validation-in-go/。

## 实施边界

- 移除五个审计自定义函数及业务 CHECK；保留 UUIDv7、JSON 基本形状、原列类型、非空、默认值、主键与索引。
- Go 复用 event.Freeze，保护入队、Store 直写和 Go 脱敏投影；批量 SQL 清理沿用既有事务和锁序。
- 不新增生产迁移，不修改固定源快照、manifest、历史标签；已有完整十列同号库重复 Up 不自动删除旧函数或 CHECK。
- 用户在实施验收后另行授权归档、提交及本地合并到 develop，集成使用独立 merge commit；未授权推送或部署。提交和合并标识以仓库 Git 日志为准。

## 验证证据

- 修改前基线：Linux Go 1.26.5，audit/event、audit/authentication、internal/migrationcheck 测试通过。
- 数据库职责红绿：先改测试，在旧定义上运行 migrations，容量、时间、业务内容和函数数量四个顶层测试因原约束失败；移除函数和业务 CHECK 后，`go test -count=1 -v ./migrations ./internal/migrationcheck` 通过，无 skip。新增旧同号库测试也通过。
- Go 定向回归：Store 直接调用的非法事件及混合批次均在写入前拒绝，65536 字节事件成功、65537 字节事件拒绝。原有入口已具备完整校验，新增覆盖先通过；投影后 Freeze 为防御性复验，未为制造红测增加生产测试钩子。SQL 清理后的真实 Reader 读回证明事件仍能通过完整校验。
- `sqlc generate`（1.31.1）与 `bash scripts/check-generated.sh`：通过，user/db 和 audit/db 无差异。
- `bash scripts/check-migrations.sh`：通过，覆盖固定历史源、正式标签和当前初始哈希；没有修改历史 frozen 或放宽检查。
- `GOWORK=off go build ./...`、`go vet ./...`：Linux Go 1.26.5 下通过。
- `go test -race -json -count=1 ./...`：配置任务专属真实 PostgreSQL 和 Redis，33 个含测试的包通过，677 个顶层测试、1231 个子测试通过，无失败和 DATA RACE。
- `go run ./internal/testgate scripts/required-tests.txt`：对本次 JSON 输出校验通过；新增三个必需顶层测试已列入。普通套件唯一跳过为预期的 TestRecoveryFixture，恢复脚本随后强制执行其 seed、verify、source-unchanged 三阶段并各自通过门禁。
- `bash scripts/verify-recovery.sh`：独立空源库与空目标库演练通过，pg_dump/pg_restore 客户端为 17.11；认证数据、身份流程快照、密文、access/refresh 和源库未变均通过核验。材料位于 .test-output/recovery.QNh5Pe/。
- `openspec validate --all --strict`：实施前后均为 21 项通过、0 失败。
- 范围核对：与固定实施前提交 c769e3dd6445d3c740e1480022b010f8eb576f4a 比较，0001 中审计块以外的全部 SQL 及 26 个普通/独立唯一索引定义原样，其他表五个 CHECK 保持；固定源快照、manifest、测试历史 SQL 和生成代码无差异。生产迁移仍只有 0001 up/down。
- `git diff --check`：通过。当前 SQL 为 LF、无 BOM。up SHA256 为 `9f2d55f98ff4558bf02237b27da6b394010ecf97a568e685a521a93ce2114432`，down 为 `4ba073412c0303175b312eadf76453cc01f87a3d41ff993272c25c805ade2c18`，与 initialFrozen 一致。

## 独立审查

完整工作树由独立审查代理只读复核，无需修复的实质问题。核对了两处投影校验的事务回滚、锁序与幂等语义，SQL 的基础约束和其他表范围，输入及清理回归，固定历史与文档责任边界。直接修改 0001、无新增迁移、旧同号库不自动移除旧约束属于已确认范围；新测试依赖完整 Git 历史，与仓库现有验证约定一致。

## 环境与日志

使用本任务创建的独立 PostgreSQL 17、Redis 7 容器和 Docker network，名称包含 accountkit-audit-validation 与 20261018；不访问既有运行容器的数据。原始日志放在被忽略的 .test-output/audit-validation-in-go/，包括 task1-red.log、task1-green.log、history.log、build.log、vet.log、tests.jsonl、required-tests.log、recovery.log 和 summary.json。Task 2 中间的广泛测试按协调要求取消，不能单独报告整条通过；最终全仓验证已覆盖这些包并成功退出。

结束前核对专用网络中只有本任务的 PostgreSQL/Redis 两个容器，随后停止并自动移除二者，移除专用网络及本任务模块缓存卷；再次查询无本任务容器残留。保留共享构建缓存、全部日志和恢复材料。

## 未验证项

在线 CI、真实产品部署和宿主外部审计设施不在本次验证范围；本地证据不代表它们已通过。没有执行真实旧数据库的升级或重建，旧同号兼容行为通过固定旧定义和合成数据验证。

## 归档核验

两个 delta 的新增要求及全部场景已逐字核对同步到主规格，主规格保留原有内容且没有 delta 操作标题。归档保留 .openspec.yaml 与 verification.md；兼容文档、迁移手册和实现计划的外部链接已更新并核对存在。此阶段仅同步规格、移动工件及修复引用，没有修改已完成集成验证的 Go/SQL 实现。

归档后的 openspec validate --all --strict 为 20 项通过、0 失败（本变更已退出活动列表）；git diff --check、归档元数据与链接核验、非审计 SQL/全部索引及当前哈希核对均通过。
