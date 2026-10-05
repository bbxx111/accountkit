# 单监听与显式 HTTP TLS 验收记录

日期：2026-10-05。运行时及测试基线为 `c00e004`，包含 Task1 `4634dd1` 和 Task2 `c00e004`；Task3 仅更新部署模板和文档。详细实现计划见[计划](../../../../docs/superpowers/plans/2026-10-05-accountsvc-transport.md)。本记录区分本地行为、完整库门禁、严格服务门禁和产品部署，不以历史记录或普通测试 skip 代替本次证据。

## 范围与行为

- serve 仅使用 HTTP_ADDR（默认 `127.0.0.1:8080`），独立消费者、管理员、内省和探针同址；新内省 `/v1/introspect` 仍要求 Basic，旧路径404，无别名/重定向。非空旧 INTERNAL_ADDR 拒绝启动。
- TLS_ENABLED 未设/空保留原默认；true 强制完整有效证书，false 明确使用HTTP且与任一HTTP证书项冲突；非法布尔值拒绝。最低TLS1.2、生产SMTP TLS/OIDC HTTPS和独立认证链保留，migrate不解析服务专用设置。
- remoteauth接口不变，HTTP须调用方显式AllowHTTP；HTTPS证书验证、禁止重定向、不缓存成功和依赖失败语义保持。
- 网关职责与[迁移说明](../../../../docs/compatibility.md#服务单监听与传输迁移)已更新，统一[网关接入契约](../../../../docs/gateway-integration.md)覆盖入口、直连防绕过、三种TLS部署、可信来源/身份、频率/突发/并发、协议透传、不重试及日志保护。跨主机Basic/token仍需加密，单纯VPC/内网不是满足TLS契约的证据。
- 未修改根库公共API、SQL、生成结果、迁移基线、数据库结构、令牌格式或依赖；未修改历史归档工件以重写旧架构事实。OpenSpec未归档。

## TDD 与分步验收

Task1先补配置和行为测试，再修改实现：

| 命令 | RED | GREEN |
|---|---|---|
| `go test -count=1 ./internal/accountsvc -run 'TestConfig'` | exit1：production false、证书冲突、非法开关和旧地址行为不符 | exit0 |
| `go test -count=1 ./internal/accountsvc -run 'TestHTTPBoundaryAndHealth\|TestHTTPIntrospectionBoundary\|TestRuntimeSingleListener'` | exit1：新内省被mount吞掉、实际绑定2次 | 后续配置/HTTP/Runtime组合命令exit0 |
| `go test -count=1 ./internal/accountsvc -run TestConfigRejectsInternalAddress` | exit1：纯空格旧配置被忽略 | 修正原始非空判断后exit0 |

Task1 Windows相关单元/协议与vet通过，8个真实服务顶层用例因未注入依赖并要求Linux而skip；这部分不作为实际进程验收。Task2 Windows相关测试同样只作单元/协议检查。实际进程证据以以下Linux结果为准。Task3是部署模板和文档维护，未新增镜像实现的测试。

## Linux 严格服务入口

使用一次性 `transport-verify-20261005-pg` / `transport-verify-20261005-redis`、隔离SMTP/OIDC fixture、`accountkit-verify:local`，`GOWORK=off`、`CGO_ENABLED=1`、`ACCOUNTSVC_TEST_RACE=1`；真实子进程二进制也启用race。只清理随机schema和Redis前缀，未操作产品数据库或共享缓存。

1. 定向 `go test -race -count=1 -timeout=5m -run 'TestServiceIntegration(TransportModes|GatewayBoundary)$' -v ./internal/accountsvc` exit0，74.983s，无skip/race。后续补充可信代理正向链和失败清理，由最终严格回归覆盖。
2. 最终 `bash scripts/verify-accountsvc.sh` 原生Docker/脚本exit0：构建、相关vet、5包race测试和15项必需testgate通过。解析最终JSON：65个顶层PASS、0 test fail、0 test skip；accountsvc包238.387s，无race报告。

既有8项实际服务测试全部保留并通过：EmailLifecycle、AdminLifecycle、DependencyFailure、StartupSafety、ProcessSIGTERM、IdentityReplacement、SessionExpiry、CodeKeyRotation。另含RotationQuotaWindow、Task1配置/单监听用例与新增TransportModes/GatewayBoundary。

新增真实进程验证默认production HTTPS、production显式HTTP、实际implicit TLS SMTP、完整邮件登录/刷新/内省、同址探针、新旧路径、身份不可混用及旧配置/开关/证书冲突的非零退出和错误脱敏。本地TLS代理验证精确公网阻断、未转发计数、内部Basic、HTTP回源、设备/请求ID/挑战/no-store/Retry-After透传、可信/不可信来源及取消/清理；不提供生产限流或网格实现。

原始证据：`.test-output/transport-task2-targeted.log`、`transport-task2-strict-writable-cache.log`、`transport-task2-final-tests.jsonl`、`transport-task2-docker-exit.log`（独立docker wait结果0）。这些与首轮证据分开：首轮JSON测试通过但宿主包装返回1且未独立捕获Docker退出，不能据此称整个首轮入口成功。stderr最小probe证明PowerShell包装可能误报，最终入口使用明确原生状态及临时可写下载缓存复验；没有放宽门禁。

## 完整库、历史与恢复入口

协调者在 `c00e004` 执行 `bash scripts/verify.sh`，原生exit0：GOWORK=off独立 `go build ./...`、`go vet ./...`、完整Linux `go test -race -json -count=1 ./...`、必需testgate及真实pg_dump/pg_restore演练通过。全库JSON独立解析为410个顶层PASS、0 test fail、唯一普通test skip为预期的 `TestRecoveryFixture`；无测试包的package级skip不计为用例跳过。

恢复使用实际不同的专用空库 `transport_recovery_source` / `transport_recovery_target`，PostgreSQL客户端17.11。`.test-output/recovery.BMEFUm/{seed,verify,source-unchanged}.jsonl` 三阶段各有一个 `TestRecoveryFixture` PASS，覆盖版本、四表快照、解密、access/refresh与源库保持；不把普通套件的fixture skip当作恢复通过。恢复材料留在ignored目录，未提交备份或合成数据。

全库日志 `.test-output/transport-full-verification.log`，JSON `.test-output/tests.jsonl`；原日志仍有只读模块缓存stderr提示，但明确捕获的原生exit0及全套/恢复证据有效，没有把该提示改写为已证实测试失败。严格服务65个PASS与全库410个PASS为不同入口，不能相加宣称独立用例总数。

协调者另核对：

- `bash scripts/check-generated.sh` exit0，sqlc结果一致。
- `bash scripts/check-migrations.sh` exit0，冻结源及可达正式tag迁移历史检查通过（完整验证亦执行）。
- `GOWORK=off go list -deps .` 根库不依赖internal/accountsvc；`d4456aa..HEAD`受保护SQL/生成结果/go.mod/go.sum/根配置/pii无变化。
- `openspec validate --all --strict` exit0，12/12项通过。最终整体审查及续办收尾核验见下节。

## Compose 单端口部署

独立项目 `accountsvc-transport-20261005`，先检查18082/18027未占用；本任务随机独立数据库密码、JWT/HMAC/AES和Basic秘密只写入ignored `.test-output/transport-compose.env`，未打印或提交。Compose保持development/回环映射，并显式选择TLS_ENABLED=false；这不是服务未设开关的默认值。

实际命令从仓库根目录执行，PowerShell每个Docker命令捕获 `$LASTEXITCODE` 并显式exit，避免stderr包装的误判：

```powershell
docker compose -p accountsvc-transport-20261005 --env-file .test-output/transport-compose.env -f deploy/accountsvc/compose.yaml config --format json
docker compose -p accountsvc-transport-20261005 --env-file .test-output/transport-compose.env -f deploy/accountsvc/compose.yaml build accountsvc
docker compose -p accountsvc-transport-20261005 --env-file .test-output/transport-compose.env -f deploy/accountsvc/compose.yaml up -d --wait --wait-timeout 90
docker compose -p accountsvc-transport-20261005 --env-file .test-output/transport-compose.env -f deploy/accountsvc/compose.yaml ps --format json
docker compose -p accountsvc-transport-20261005 --env-file .test-output/transport-compose.env -f deploy/accountsvc/compose.yaml logs --no-color accountsvc
docker image inspect accountsvc-transport-20261005-accountsvc --format '{{json .Config.ExposedPorts}}'
docker compose -p accountsvc-transport-20261005 --env-file .test-output/transport-compose.env -f deploy/accountsvc/compose.yaml down --volumes
```

上述最终命令均exit0。配置JSON检查只有 `127.0.0.1:18082 -> 8080`，无INTERNAL_ADDR，数据库/Redis不映射，镜像ExposedPorts仅8080/tcp。服务日志明确本进程HTTP TLS关闭；同一地址的实际HTTP检查为：

| 请求 | 结果 |
|---|---|
| `GET http://127.0.0.1:18082/healthz` | 200 |
| `GET http://127.0.0.1:18082/readyz` | 200 |
| `POST http://127.0.0.1:18082/internal/v1/introspect` | 404 |
| `POST http://127.0.0.1:18082/v1/introspect`（无凭据） | 401，Basic challenge |

清理前按 `com.docker.compose.project=accountsvc-transport-20261005` 核对四个容器、一个default网络和一个pgdata volume均归本项目；`down --volumes`仅清理它们。项目构建镜像保留为本地构建缓存，不操作既有postgres/redis或协调者手工验证资源。清理后分别通过 `docker ps -aq`、`docker network ls -q`、`docker volume ls -q` 的该项目标签过滤确认容器/网络/volume均为0，exit0，摘要见 `.test-output/transport-compose-cleanup.log`。

原始日志为 `.test-output/transport-compose-{config-check,build,up,probes,service,down}.log`及对应stderr、项目标签与镜像端口摘要；渲染JSON含本地合成秘密，仅保留ignored目录，不作为可公开附件。准备阶段曾因PowerShell数组表达式把env拼成一行，首次config返回1；修正为8行后渲染exit0，未启动失败资源。标签模板首次受PowerShell引号转换影响返回1，改为不含内嵌引号的Labels输出后exit0；均为验收脚本准备问题，未修改服务行为。

## 审查与未验证项

- Task1独立审查：规格符合，Critical/Important/Minor均无，Approved；当时真实服务未验收部分已由最终严格入口补齐。
- Task2独立审查：Approved，无Critical/Important；原一项非阻断Minor为新增remoteauth默认证书拒绝测试输出预期TLS握手日志，已于2026-10-06通过 `f1a257e` 处理并通过范围复审，证书拒绝断言保持。
- Task3自审逐项核对增量规格与当前配置/路由、三态默认、完整迁移/回退和部署职责；7份Markdown共56个本地路径/锚点核对无问题（`.test-output/transport-doc-links.log`），本步范围 `git diff --check` exit0。没有修改协调者plan/tasks；Task3独立审查已通过（Approved，无新增问题）；最终全分支审查覆盖 `d4456aa..9546a96` 并获 Approved；`9546a96..f1a257e` 的范围复审确认唯一Minor已解决且无新问题。
- 真实网关/网格配置、后端直连防绕过、产品证书链与身份策略、流量阈值/集群计数和并发压力、全链路日志保护、实际SMTP送达/OIDC提供方、产品备份恢复与部署窗口继续延期。[发布清单](../../../../docs/release-checklist.md#可选-accountsvc-发布)对应项保持未勾选。
- 未执行push、tag、发布、产品部署或OpenSpec归档；本地验证不表示在线GitHub Actions已经通过。

## 续办收尾（2026-10-06）

最终审查曾因子代理额度限制中断，用户resume后恢复并完成，未重复派发已完成实施任务。原2026-10-05的全库410项、严格服务65项和恢复/Compose证据保持原日期。最终修复仅调整 TestDefaultHTTPSCertificateVerification 的局部日志夹具：服务器启动前配置ErrorLog缓冲，关闭后仅过滤预期证书拒绝诊断，其他诊断仍可见；生产代码、TLS校验及原有安全断言未改动。

修复后 GOWORK=off 的 remoteauth 包完整verbose测试、vet及Linux race通过（race包1.686s），无需重跑未改变的完整服务/恢复套件。最终范围复审结论 Ready / Approved，Critical、Important、Minor均无遗留。本change的13项任务已完成；归档、合并和推送仍作为独立操作。

本任务手工创建的 transport-verify-20261005-pg、transport-verify-20261005-redis 和专用网络已在核对所有权标签后清理；Compose项目资源此前已清理。既有postgres/redis容器保留，本地构建镜像及ignored原始证据保留。
