# 验收记录：终端用户 HTTP 包更名

日期：2026-10-06。变更依据：[proposal](proposal.md)、[design](design.md)、[增量规格](specs/accountkit-package-identity/spec.md)。实施计划：[HTTP API EndUser](../../../docs/superpowers/plans/2026-10-06-httpapi-enduser.md)。

## 验证范围与源码基线

- 分支起点 `ff86c76`，提案/计划 `f1e2105`，Go 迁移 `ec5e24b`，当前文档迁移 `54dcfb8`。
- `httpapi/consumer` 的 19 个 Go 文件整体迁入 `httpapi/enduser`，根门面改为 `Auth.EndUserHandler() http.Handler`；旧包、方法、兼容别名和转发包装均移除。
- Handler、Deps、Service（20 个原方法）、可选 IdentityReplacer 及构造/路由/中间件签名保持；未实现可选换绑能力的原 fake 仍满足 Service，原 503 测试通过。
- 19 个原文件按批准的名称变化规范化并 gofmt 后与新文件逐一一致。调用方 diff 仅包含公开/私有名称、相应注释/测试消息和 gofmt 对齐变化；HTTP、配置、凭据、SQL、依赖及领域行为无改动。
- 根 module/package 与领域 user 保持原名；`go list -deps .` 不包含 accountsvc。仓库 go.mod/go.sum、生成代码、冻结迁移、源基线和历史工件未改。

## 外部宿主 RED / GREEN

四个独立临时模块位于 ignored `.test-output/enduser-rename/hosts/`。各自使用临时 require/replace 指向当前检出，`GOWORK=off`、`GOPROXY=off` 下执行 `go build -mod=mod`；未向仓库 go.mod 加入 replace。

| 检查 | 改名前 | 改名后 |
|---|---|---|
| root-new：仅引用根 EndUserHandler | exit 1，方法 undefined | exit 0，无输出 |
| direct-new：引用 enduser.New/Deps/Handler/Service/IdentityReplacer 及路由/中间件 | exit 1，本地模块缺少 enduser 包 | exit 0，无输出 |
| root-old：调用 ConsumerHandler | 原入口基线 | exit 1，方法 undefined |
| direct-old：导入 httpapi/consumer | 原入口基线 | exit 1，本地模块缺少 consumer 包 |

RED 在修改生产代码前执行，失败原因为缺少目标入口。直接宿主保留 `*user.Service` 对 Service 与 IdentityReplacer 的类型检查。退出码取原生 `$LASTEXITCODE`，没有把 PowerShell 的 stderr 包装视为原生退出码。

原始失败日志和全部退出码保存在 `.test-output/enduser-rename/`。成功且无输出的宿主命令未生成 `.log`，以 `green-root-new.exit.txt`、`green-direct-new.exit.txt` 为证。

## 必需测试清单映射

原 37 项 `github.com/bbxx111/accountkit/httpapi/consumer/<TestName>` 按原顺序一对一映射为 `github.com/bbxx111/accountkit/httpapi/enduser/<TestName>`，TestName 和断言保持。唯一额外测试名迁移为：

`github.com/bbxx111/accountkit/TestConsumerHandlerAndMiddlewareWiring` → `github.com/bbxx111/accountkit/TestEndUserHandlerAndMiddlewareWiring`。

规范化前后全清单一致，没有删条目或允许 skip。逐项原始对照见 ignored `.test-output/enduser-rename/selector-mapping.txt`；19 文件原文、导出声明及规范化结果亦保留于该目录。

## 实际检查

| 检查 | 实际结果 |
|---|---|
| gofmt 修改文件；`GOWORK=off go build ./...` | exit 0；Windows build 有下述环境警告 |
| `GOWORK=off go vet ./...` | exit 0，无输出，`vet.exit.txt` 为证 |
| `go test -json -count=1 ./httpapi/enduser . ./internal/accountsvc/... ./examples/embedded` | 100 个顶层 PASS、0 FAIL、26 SKIP；37 项新包必需测试及新根门面测试均 PASS；embedded 无测试文件，由 build 覆盖 |
| `bash scripts/check-generated.sh` | exit 0，sqlc 生成结果一致 |
| Linux `bash scripts/verify.sh` | exit 0；410 个顶层 PASS、0 FAIL，唯一普通套件 SKIP 为 TestRecoveryFixture；338 项库必需测试全部 PASS，含 37/37 新包必需项与新根门面测试 |
| 真实 PostgreSQL 备份恢复 | seed、verify、source-unchanged 三阶段均 PASS；pg_dump / pg_restore 17.11；材料在 ignored `.test-output/recovery.Cm92Bn/` |
| Linux `bash scripts/verify-accountsvc.sh` | exit 0；65 个顶层 PASS、0 SKIP/FAIL；15 项服务必需用例全部 PASS，实际服务子进程以 race 构建 |
| 当前文档链接、差异检查；`openspec validate --all --strict` | 32 个本地链接、8 个锚点无错误；diff-check 通过；OpenSpec 13/13 PASS |

快速测试跳过的 26 项依赖真实数据库/Redis（恢复 fixture 按普通套件规则跳过），不作为集成验证证据。完整验证结果单独记录。Windows build 出现模块缓存写权限警告 `go: writing stat cache ... Access is denied`，但原生退出码为 0；Linux 门禁使用只读模块源码加临时可写下载元数据层，避免修改宿主缓存。


完整门禁运行于 `accountkit-verify:local`（Go 1.26.5、CGO/GCC、PostgreSQL 17.11 客户端），使用本次独立创建的 PG17 与 Redis 容器；恢复源/目标为不同空数据库。完整库门禁含迁移历史检查、build、vet、全包 race、必需用例检查及真实恢复。严格服务门禁另以 `ACCOUNTSVC_TEST_RACE=1` 覆盖实际服务进程。两个门禁和外层 Docker 的原生退出码均为 0，日志未出现 stat-cache 警告、race 报警或测试失败。

原始日志、JSONL 和退出码位于 ignored `.test-output/enduser-controller/`；恢复材料留在上述独立目录。仅本次创建的 `enduser-rename-20261006-pg`、`enduser-rename-20261006-redis` 及同名隔离网络已清理，原有服务未操作。运行验证基于 `ec5e24b` 的 Go 代码；之后仅修改文档与验收工件。

## 独立审查

- Task 1：独立规格与质量审查 Approved，无 Critical/Important；确认全部 37 项测试实际 PASS。报告中不存在的空输出日志引用已更正并经审查者确认。Windows 缓存警告作为已披露环境项保留。
- Task 2：独立规格与质量审查 Approved，无 Critical/Important/Minor；完整门禁证据由控制器另行核验如上。
- 最终分支审查（`ff86c76..a615124`）：Ready to merge — Yes，无 Critical/Important；独立核对 338 个库必需项、15 个服务必需项和恢复三阶段。唯一 Minor 为设计/兼容指南将诊断前缀影响范围写窄，已通过 `06d3142` 修正；局部复核判定 ADDRESSED，无新问题。没有遗留审查发现或设计偏离。

## 未验证项与交付边界

产品环境、真实短信/邮件服务商、生产网关或服务网格联调继续延期；本次不声称完成在线部署验收。7 项实施任务全部完成。当前变更尚未归档、合并或推送，保留于 `refactor/httpapi-enduser`。
