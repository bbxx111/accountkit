# Verification

## Planning verification — 2026-10-23

本记录仅覆盖 OpenSpec 规划工件。本轮没有修改实现代码，没有提交、推送或归档。

- `openspec status --change add-alipay-app-sign-in --json`：proposal、specs、design、tasks 均为 done，`isPlanningComplete=true`。该状态表示工件齐备，不表示实现完成。
- `openspec validate --all --strict --json`：33/33 通过，包含 31 份主规格、支付宝与 Facebook 两个变更；无 issues。
- 工件覆盖与内容检查：proposal 列出的 5 个 capability 均有对应 delta，结构标题与场景格式已检查；24 项任务全部未勾选。
- 独立 builder-check 只读评审：核对本变更全部工件及相关实现，结论为无实施前必须补决策的重要遗漏。重点覆盖原始响应验签、固定标识模式、资料降级和取消、pending/并发补填、migrate 静态校验及 Facebook 增量兼容。
- 本地引用、变更目录边界及 Markdown 空白检查：通过；修改范围限定于本变更目录。

## Unverified implementation at planning time — 2026-10-23

- 尚未实现 Alipay Provider、配置装配、资料补填或观测取值，24 项任务均待执行。
- 未运行 Go build/vet/unit/race、数据库并发测试、生成检查或恢复演练；规划文档不能替代实现验收。
- 未进行真实支付宝应用权限、应用分组、App 极简 SDK 或昵称/头像联调；不能据此宣称端到端登录可用。
- 后续实施结果、审查结论和未验证项继续追加到本文件，不用规划校验替代真实验证证据。

## Implementation and delivery checks — 2026-10-24

Task 1–3 已完成类型/配置、RSA2 验证器和领域装配，并分别经过独立评审。Task 4 修改接入文档、无秘密配置模板、必需测试清单和本验收记录；另按 root 核对既有规格后的授权，修正一个既有并发换绑测试的输家错误断言。未修改生产代码、SQL、生成文件、迁移或冻结源。此处记录实际执行证据，最终 whole-branch 评审与任务勾选由 root 后续确认，不宣称已完成真实 App 验收。

### 环境与命令

运行日期为北京时间 2026-10-24，JSON 内 UTC 时间从 2026-10-23T16:04 开始。工作树基于 `34e953e7ab5625eec1976b395c4e1cbc1dad3b04`，分支 `feat/add-alipay-app-sign-in`。验证镜像 `accountkit-verify:local` 提供 Go 1.26.5、GCC 14.2 与 PostgreSQL 17.11 客户端；全部 Go 命令设置 `GOWORK=off`。Linux runner 绑定当前仓库 `/src`，连接任务专用网络 `ak-alipay-20261023-apply`，只使用该任务的 PostgreSQL/Redis 与 Go 缓存卷。

runner 的共同参数如下（连接口令是一次性任务容器的合成值，不用于产品）：

```powershell
$common = @(
    '--network', 'ak-alipay-20261023-apply', '-e', 'GOWORK=off',
    '-e', 'SERVER_TEST_DB_DSN=postgres://postgres:alipay_test_only@ak-alipay-20261023-pg:5432/accountkit_test?sslmode=disable',
    '-e', 'ACCOUNTSVC_TEST_REDIS_URL=redis://ak-alipay-20261023-redis:6379/0',
    '-e', 'GIT_CONFIG_COUNT=1', '-e', 'GIT_CONFIG_KEY_0=safe.directory', '-e', 'GIT_CONFIG_VALUE_0=/src',
    '--mount', 'type=bind,source=D:/code/bbxx111/accountkit,target=/src',
    '--mount', 'type=volume,source=ak-alipay-20261023-go-cache,target=/root/.cache/go-build',
    '--mount', 'type=volume,source=ak-alipay-20261023-go-mod,target=/go/pkg/mod', '-w', '/src'
)
docker run --name ak-alipay-20261024-task4-verify @common `
    -e 'ACCOUNTKIT_RECOVERY_SOURCE_DSN=postgres://postgres:alipay_test_only@ak-alipay-20261023-pg:5432/accountkit_recovery_source?sslmode=disable' `
    -e 'ACCOUNTKIT_RECOVERY_TARGET_DSN=postgres://postgres:alipay_test_only@ak-alipay-20261023-pg:5432/accountkit_recovery_target?sslmode=disable' `
    accountkit-verify:local bash -o pipefail -c 'bash scripts/verify.sh > .test-output/alipay-task4-verify.log 2>&1'
docker run --name ak-alipay-20261024-task4-svc @common accountkit-verify:local bash -o pipefail -c 'bash scripts/verify-accountsvc.sh > .test-output/alipay-task4-accountsvc.log 2>&1'
docker run --name ak-alipay-20261024-task4-tests @common accountkit-verify:local bash -o pipefail -c 'go test -count=1 -json ./... > .test-output/alipay-task4-tests.jsonl 2>&1'
docker run --name ak-alipay-20261024-task4-replacement @common accountkit-verify:local bash -o pipefail -c 'go test ./user -run "^TestReplaceIdentityConcurrent$/^same-old-identity$" -count=20 -json > .test-output/alipay-task4-replacement-repeat.jsonl 2>&1 && go test -race ./user -run "^TestReplaceIdentityConcurrent$" -count=5 -json > .test-output/alipay-task4-replacement-race.jsonl 2>&1'
docker run --name ak-alipay-20261024-task4-tests-green @common accountkit-verify:local bash -o pipefail -c 'go test -count=1 -json ./... > .test-output/alipay-task4-tests-green.jsonl 2>&1'
```

首次运行时两个恢复数据库均为空、实际库身份不同。恢复脚本现在已经 seed/restore；后续重跑必须创建新的专用空数据库，不能清除已有数据或复用来伪造通过。

### 实际结果

- `bash scripts/verify.sh`：**exit 0**。依次通过迁移历史检查、`go build ./...`、`go vet ./...`、`go test -race -json -count=1 ./...`、878 项 required-test 门禁与真实恢复演练。race JSON 为 `.test-output/alipay-task4-race-tests.jsonl`（从脚本 `tests.jsonl` 原样复制）；44 个有测试的包通过、1009 个顶层测试通过、无失败，34 个新增 Alipay 顶层测试全部通过。user 包含真实 PostgreSQL 的资料/身份/会话事务、PATCH/状态/匿名化竞争、两个 App UnionID 唯一性及 pending/重放断言。
- `bash scripts/verify-accountsvc.sh`：**exit 0**。Linux CGO/race 与 `ACCOUNTSVC_TEST_RACE=1` 下，服务构建、范围 vet、5 个包/102 个顶层测试及服务必需门禁通过；实际服务 SIGTERM、TLS trust、生命周期、配置和远程鉴权已执行。日志 `.test-output/alipay-task4-accountsvc.log`，原始 JSON 另存 `.test-output/alipay-task4-accountsvc-tests.jsonl`。
- `go test -count=1 -json ./...`：**首次 exit 1**。43 个包/1008 个顶层通过，34 个 Alipay 顶层均通过；仅既有 `user/TestReplaceIdentityConcurrent/same-old-identity` 失败，`service_identity_replacement_transaction_test.go:314` 报 `same identity loser: user: not found`。原始失败证据保留 `.test-output/alipay-task4-tests.jsonl`，未覆盖。preflight 先读会话再读身份，两次无锁读取之间赢家可提交，输家返回 ErrNotFound，而测试只接受 ErrInvalidToken。root 核对 [并发换绑规格](../../specs/identity-replacement/spec.md) 明确允许 404 或认证失效后，授权仅修正 sameUser 输家断言为接受 ErrInvalidToken/ErrNotFound；恰好一次成功和身份数量不变量保持，生产代码不变。该首次运行仍明确为失败，不能用后续 GREEN 覆盖历史结果。
- 修复后定向验证：`go test ./user -run '^TestReplaceIdentityConcurrent$/^same-old-identity$' -count=20 -json` **exit 0**，same-old-identity 实际通过 20 次；`go test -race ./user -run '^TestReplaceIdentityConcurrent$' -count=5 -json` **exit 0**，同旧身份和争抢同新目标各通过 5 次，均使用真实 PostgreSQL、无 skip。证据 `.test-output/alipay-task4-replacement-repeat.jsonl` / `alipay-task4-replacement-race.jsonl`。全套 race/服务/恢复在此 test-only 修复前通过；定向 race 在修复后通过，没有声称修复后重跑过全套 race 或恢复。
- 修复后完整 `go test -count=1 -json ./...`：**exit 0**，44 个有测试包/1009 个顶层测试通过，34 个 Alipay 顶层通过，0 fail；原始 JSON `.test-output/alipay-task4-tests-green.jsonl`。仍只有下述两项测试 skip，未新增允许跳过、未删除门禁。该 GREEN 对应规格允许结果的精确测试修正，不改变生产认证行为。
- 全套 race 和非 race 的真实测试 skip 均只有 `TestRecoveryFixture`（普通套件预期；恢复脚本单独强制三阶段执行）及 `TestProfileAttributesUnreadableFile`（Linux runner 为 root，chmod 000 后仍可读，测试显式 skip，**未把权限拒绝分支当作已通过**）。另外 8 个 package-level skip 对应无测试文件包。服务单独套件仅有上述权限测试 skip，未跳过服务进程集成。无 Alipay skip。
- 恢复脚本使用 PostgreSQL 17.11 的实际 `pg_dump` / `pg_restore`，seed、verify、source-unchanged 三阶段均通过。证据目录 `.test-output/recovery.0NRYWT` 含三份 JSON、合成 state 和数据库备份；核验版本、认证数据、资料属性/头像、会话来源、单份客户端描述、补全快照、解密与 access/refresh、恢复后清理及源库保持。
- `scripts/required-tests.txt` 原 844 项全部保留，新增 34 个支付宝顶层选择器，现为 878 项，无重复。包含配置/协议/秘密与降级边界、装配/指标及全部 17 个新增真实 DB 领域测试；不是仅按名称过滤后把 no-tests-to-run 计为通过。
- `check-generated.sh` 复用 root 在同一实现状态已执行的 **exit 0** 结果；本任务未改查询/schema/sqlc。`scripts/verify.sh` 再次运行 `check-migrations.sh` **exit 0**，输出 `Migration history is immutable; new versions are strictly increasing`。从 `34e953e` 比较 migrations、user/db、audit/db、source-baseline 均无差异，历史 SQL 与冻结源保持。
- `C:/Users/bbxx1/AppData/Roaming/npm/openspec.cmd validate --all --strict --json`：**exit 0，33/33**（31 主规格、2 变更），无 issues；结果 `.test-output/alipay-task4-openspec.json`。本任务文件 `git diff --check` 与六份接入文档的本地 Markdown 目标路径检查通过。

### 文档与未验证项（当时的旧验收范围）

新增 [支付宝接入指南](../../../docs/alipay-sign-in.md)，同步 README、Go/accountsvc 集成、兼容性、指标和 `.env.example`，明确 App 极简 SDK、auth_user/state、已安装客户端、取消/回跳、Flutter 桥接、一次性 code、普通公钥、多 App 分组、模式及指纹、资料降级和主动清空后补填。已对照官方 SDK 参数入口、当前公开 Go 类型及真实路由；Compose 默认 EMAIL，不会自动传递任意 `.env` IdP 变量，文档明确需要部署/override 注入。

**真实支付宝 App/设备、授权权限和应用分组凭据缺失**，未记录平台/SDK 真机结果，也未证明控制台两个 App 实际返回相同 UnionID。当时 6.2/6.3 对应真机任务，因此保持未勾选；后续用户已确认将这两项调整为库侧自动契约，并把真实终端检查移到宿主清单，见最新 Library contract acceptance。合成签名网关、真实 DB、race 与恢复成功均不等于真实支付宝端到端上线验收。最终独立评审见下节。

## Final review and handoff — 2026-10-24（验收职责调整前的记录）

- Task 1–4 均经过独立规格/代码质量评审。配置阶段发现的 HTTP localhost 例外已收紧为数值回环地址，并通过定向测试和复核；协议与领域阶段无未解决的 Critical/Important 问题。交付阶段核实 test-only 换绑断言修正符合既有规格，原 844 项必需测试没有删除。
- 最终整分支只读评审范围：Base `c279db5` 至 Head `b391347`。审查完整差异、需求/设计、调用上下文及原始 JSON，结论为 Critical、Important、Minor 均无新增实现问题，代码集成质量通过。审查者没有修改代码或执行合并。
- 独立解析原始日志再次确认：全 race 和修复后非 race 各 44 个有测试包、1009 个顶层 PASS、34/34 新增支付宝选择器 PASS；服务单独验证 5 包、102 个顶层 PASS。SQL、生成目录及冻结源相对分支起点无差异。
- 对审查中不评判的部分，控制器确认与提案非目标一致：完整版 authInfo、Web/H5/小程序授权托管、支付及证书模式；仓库外 Flutter/原生客户端实现质量。这不排除本计划内真实 SDK/分组验收，6.2/6.3 仍待完成。
- 规划/任务文件清单中重复列出的配置解析文件已修正为复用 Task 1 实现；并发换绑断言修正的范围与依据已写入 design 的 Implementation Notes。没有借测试失败修改生产错误映射、删门禁或新增 skip。
- 按当时将真机任务计入库验收的旧范围，进度为 **22/24**；6.2 与 6.3 未完成。后续用户已确认改为库侧契约验收，最新结果见下节。root 权限下文件不可读分支的额外验证缺口继续保留，未将该 skip 记为 PASS。
- 已核对本任务 PostgreSQL/Redis 容器的完整 ID 后，仅删除任务创建的两依赖容器、五个已退出验证 runner、专用网络、两缓存卷及其匿名测试卷。其他 Docker 资源未操作；原始日志和恢复合成备份继续保留在 `.test-output/`。
- 仅完成本地提交，未推送、合并、归档、打 tag 或部署。后续真实验收和归档仍需分别执行，不能把当前自动验证结果描述为生产端到端验证完成。

## Library contract acceptance — 2026-10-24

用户明确指出终端验收应在宿主执行，并确认把库 6.2/6.3 调整为自动 HTTP 契约验收。原真机、权限、实际 SDK 与控制台分组检查移到 [宿主接入清单](../../../docs/alipay-sign-in.md#宿主上线前接入检查清单)，仍为未执行；没有把旧真机任务直接标为通过。

### 新增证据与范围

- 提交 `c7cdb1d` 新增外部 `accountkit_test` 的 `TestAlipayHTTPLoginContract` 和 `TestAlipayHTTPMultiAppIdentityContract`，仅新增测试与门禁，没有修改生产实现、SQL、schema 或生成代码。正式根门面经 New/Migrate/Start/Close、EndUserHandler 和实际 Alipay Provider，连接独立签名网关及真实 PostgreSQL/Redis；没有 fake Provider 或直接领域登录替代 HTTP。
- 单 App 用例验证登录、独立 JWT 校验、Bearer 本人资料、刷新轮换、错误签名/重复授权码拒绝且无持久化副作用，以及资料故障放行、后续补空和非空资料保护。多 App 用例使用两套独立应用及支付宝密钥，验证 UNIONID 同账号/单身份/OpenID 合并、跨 App 补填；OPENID 同文本跨 App 分离，后续出现 UnionID 仍不升级或合并。
- Native 定向命令：`GOWORK=off go test -json -count=1 . -run '^TestAlipayHTTP(LoginContract|MultiAppIdentityContract)$'`，使用专用真实 PG/Redis，**exit 0，两个顶层及两个子项 PASS，0 skip/fail**；证据 `.test-output/alipay-http-contract-tests.jsonl`。新增两项选择器单独 testgate 通过，不冒称重跑过全清单 gate。
- `GOWORK=off go test -count=1 .` 使用同一真实依赖：**exit 0，31.706s**；`go vet .` **exit 0**。缺 Redis 检查使两个顶层明确 SKIP，此负向依赖检查不计作通过证据。
- Linux 新增用例 race：`GOWORK=off go test -race -count=1 -json . -run '^TestAlipayHTTP(LoginContract|MultiAppIdentityContract)$'`，真实 PostgreSQL/Redis，**exit 0，两个顶层 PASS，0 skip/fail，包 2.343s**；证据 `.test-output/alipay-http-contract-race.jsonl`。root 独立解析 JSON，并将其输入新增选择器 testgate，exit 0。
- 必需清单原 878 项全部保留，新增上述两项，现 880 项。每个 fixture 独立随机 schema/Redis 前缀，仅清理自己的前缀，不调用 FLUSHDB；没有终端、真实支付宝账号或产品凭据依赖。
- `openspec validate --all --strict --json` **33/33 通过**，本地文档引用和 `git diff --check` 通过。此次仅测试/文档/门禁变化，根包回归和新增用例 race 是本次新证据；此前全库、服务、生成、迁移与恢复证据仍按上节原日期和范围保留，没有声称重新执行完整恢复演练。

### 可保证与宿主责任

库侧证据证明：在合法签名协议声明下，公开 HTTP 入口、配置、身份映射、资料、真实存储与会话流程符合契约。模拟两个 App 返回相同 UnionID，不证明支付宝控制台真实分组配置已正确生效。实际 SDK、Flutter/原生桥接、安装/取消/回跳 state、平台授权权限与分组仍由宿主上线前独立验证，当前未执行，不作为库 CI 的设备前提。

### 增量评审结论

独立审查范围为 `9da61ac..5a8d90b` 的新增测试、门禁及验收边界文档。规格合规通过、质量 Approved，无 Critical/Important；审查者独立读取 native/race 原始 JSON，确认两项顶层 PASS、0 skip/fail，且检查正式根门面/handler、实际 Provider、独立逐 App 签名网关、真实存储和清理边界。一个 Minor 为原 Task 4 的计划措辞残留，已改为按 Task 5 执行库侧契约、宿主清单保持未执行，避免新旧指令冲突。

更新后的 6.2/6.3 有对应自动测试与实际通过证据，库侧任务进度为 **24/24**。这是用户确认的职责范围修正后完成库验收，不是补填真实终端或真实控制台分组的通过记录。此前完整实现评审与全量验证结果仍保留原始范围，本次无生产实现修改。

已核对并清理本轮 `ak-alipay-http-20261024-pg`、`ak-alipay-http-20261024-redis` 及专用网络，未操作其他 Docker 资源。新增原始测试日志继续保留在 `.test-output/`。
