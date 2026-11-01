## Scope and Baseline

本记录汇总 2026-11-01 本轮实施证据，替代实施期间的临时状态。变更为 `add-account-client-sdks`，分支 `feat/add-account-client-sdks`，基线 HEAD 为 `6e17c77ba1c45a0ccedbf449cfae6b4e69654476`，服务基线为 `v1.0.0`。按[实现计划](../../../docs/superpowers/plans/2026-11-01-add-account-client-sdks.md)实施与独立复核。

范围仅 accountkit：新增两个 SDK、测试宿主、覆盖与消费检查、SDK CI 和文档。未修改 ai-food、生产 Go 业务契约、OpenAPI、迁移或生成代码；既有 Go workflow 不变。验收完成后，用户单独授权提交、推送任务分支并创建 [PR #20](https://github.com/bbxx111/accountkit/pull/20)，目标为 develop。没有创建、移动或推送发布 tag，也未归档本变更。验收使用的测试 Git 提交仅存在于一次性独立快照仓库。

实际工具：Flutter **3.47.4**、Dart **3.13.3**、Node **24.21.0**、pnpm **11.24.0**、TypeScript **5.8.3**、Vite **6.4.1**、Vitest **3.2.4**、Prettier **3.6.2**、Go **1.26.5**、sqlc **1.31.1**、PostgreSQL **17**（恢复客户端 **17.11**）、Redis **7**、OpenSpec CLI **1.13.0**。精确 SDK 清单见 `sdk/tool-versions.json`。初始设计提及的 Flutter 3.47.1 / Dart 3.13.1 未实测。

## Executed Checks

下列命令均为本轮实际执行结果。原始输出保留在被忽略的 `.test-output/`；日志用于追溯，不能作为未来代码或线上 CI 自动通过的证明。

| 层次 | 命令 / 实际结果 | 证据文件（相对 `.test-output/`） |
| --- | --- | --- |
| HTTP 覆盖 | `node scripts/check-sdk-coverage.mjs`：消费端 24、管理端 11 操作与 OpenAPI 一致；方法、模型及测试源关联检查通过 | `sdk-coverage-unit.log`、`sdk-scripts-linux-final.log` |
| 验证脚本 | Linux Node 24.21.0 执行 `node --test scripts/*sdk*.test.mjs`：22 通过、0 失败、0 跳过，含真实 Git 克隆及 TERM/KILL 清理 | `sdk-scripts-linux-final.log` |
| TypeScript 包 | 独立冻结安装、`pnpm format:check`、`pnpm typecheck`、`pnpm test`：55 项通过 | `admin-sdk-report.md` |
| Flutter 包 | `flutter pub get --enforce-lockfile`、format、`flutter analyze`、`flutter test`：73 项通过，无分析问题 | `flutter-sdk-report.md` |
| 测试服务 | fixture 单元、vet 与 `go test -count=1 -tags integration ./sdk/testsupport/fixture` 通过；accountsvc 启动/迁移/SMTP 烟测通过；必需依赖缺失明确失败 | `service-fixture-report.md` |
| 管理 SDK 真实契约 | `pnpm test:integration`：1 个真实场景通过，公开 SDK 实际调用全部 11 操作；覆盖角色 403、无效凭据 401、生命周期、分页/filter、reveal、会话和审计 nullable/flow_id | `admin-integration-report.md`、`admin-sdk-report.md` |
| Flutter 真实契约 | `flutter test test/integration/service_contract.dart`：3 个场景通过；另执行 `policy_seed.dart` 与 `policy_contract.dart` 各 1 个场景通过 | `flutter-integration-report.md` |
| Flutter Git + Android | `node scripts/verify-sdk-consumers.mjs --sdk flutter --platform android`：仓库外固定 SHA + 子目录安装、冻结安装、analyze、公开入口行为测试及 Android debug APK 构建全部通过 | `sdk-flutter-android-consumer.log` |
| TS Git + Vite | 完整入口内 `node scripts/verify-sdk-consumers.mjs --sdk all --platform none`：仓库外 Git SHA + path 安装、冻结安装、类型/行为检查与 Vite 生产构建通过；原始 TS 直接消费，无 dist/安装构建脚本 | `sdk-full-gate.log` |
| 完整 SDK 入口 | 隔离 Linux 源码副本中 `bash scripts/verify-client-sdks.sh` 退出 0：22 脚本测试、35 操作覆盖、TS 55 项、Flutter 73 项、真实 accountsvc/嵌入式策略和两包固定 Git 消费全部通过 | `sdk-full-gate.log`、`sdk-ci/` |
| 缺依赖失败 | 同镜像不提供数据库配置时，完整入口退出 1，明确提示 required/cannot skip；没有成功跳过 | `sdk-missing-deps.log` |
| 完整 Go 门禁 | 本任务 Linux 镜像中 `GOWORK=off bash scripts/verify.sh` 退出 0：独立 build、vet、race、真实数据库、必需测试、历史迁移及 PG17 dump/restore 恢复演练通过 | `sdk-go-full-verification.log`、`tests.jsonl`、`recovery.Mx3hLD/` |
| sqlc 一致性 | `bash scripts/check-generated.sh` 退出 0，无生成差异 | `sdk-go-generated.log` |
| OpenSpec / 差异 | `openspec validate --all --strict`：42 passed、0 failed；`git diff --check` 通过 | `sdk-openspec-final.log` |

Git 快照是独立测试仓库的 SHA，不是工作分支提交：Flutter Android 为 `f2d5ccb4e888824204ca4cfa563c7102b5f92e92`，包含 Flutter 最终核心修复；最终 Linux 完整入口两包共用快照为 `3123e405092f906acaae68e815807cbf756c29a7`，包含最后非法 token 错误分类修复。两包均经完整 SHA + 子目录安装，冻结安装、分析/类型、公开入口行为测试通过，TS/Vite 生产构建通过。

完整 SDK 入口使用独立源码副本、新空数据库和单独 Redis，未复用宿主依赖目录或已运行 fixture。最终源码还通过全部 89 个新增路径中的文本 UTF-8、BOM、行尾空白与冲突标记检查；候选 Flutter/TS manifest/tag/compatibility 检查均通过，不创建 tag。

## Real Contract Details

真实测试使用本任务 PostgreSQL/Redis 和合成用户。默认场景通过 accountsvc 正式装配运行，嵌入式宿主补充自定义前缀及可选能力；Go 烟测与 SDK 实际请求分别记录。

- Flutter 覆盖邮箱 Controller 登录、刷新、重新认证、删除/恢复；资料、身份、会话及换绑成功后的 `sessionEnded`。默认 profile 未配置返回 503，同 kind EMAIL 上限返回 409，保持生产限制。
- 受控提供方覆盖 10 种 credential 形状和 `TEST_OIDC`，经过 SDK 实际登录及必要补全。这不代表微信、Apple 等平台原生授权成功。
- 独立 custom-extended schema/Redis prefix 先以 Required=[] 通过 SDK 建立合成第三方账号；停止后以 Required=EMAIL 重启。同一 user_id 重登为 NeedsBinding，绑定 EMAIL 后 confirmed/SignedIn；验证自定义前缀及扩展资料 merge patch/null 删除。步骤固化在 Flutter integration README 和完整入口中。
- 管理测试使用受控 JWKS 的 operator/super-admin，真实调用 SDK；错误、角色差异和审计字段由响应断言核验，不以手写 HTTP 替代 SDK 方法。

Go 门禁采用三个本任务专用数据库，恢复源/目标实际不同且初始化为空；保留恢复资料与源数据未变验证。普通套件的 TestRecoveryFixture 预期跳过，恢复脚本强制 seed/verify/source-unchanged 三阶段。早期无 DSN 快速测试不作为数据库证据；Windows 沙箱 Git pipe 失败经正常权限重跑通过，最终以 Linux 完整入口为准。

## Review and Fixes

最终整体审查和限定复审均为 **Approve**，没有遗留 Critical / Important / Minor 实现问题。报告位于 `.test-output/`：

- `admin-sdk-review.md`：安全错误打印、request ID 校验、带时区 filter 已修复。默认 message/stack 固定安全，服务消息仅显式只读 serverMessage。
- `flutter-sdk-review.md`：6 个反例先失败后通过，另补相邻时序。修复跨账号 body 重建重放、reauth/refresh 交错、主动刷新后的 reauth、注入 Dio 默认 Bearer 继承、补全过期后提交及初始化失败永久缓存。最终 73 项通过，R1–R6 全部关闭。
- `sdk-infrastructure-review.md`：覆盖源关联、HTTP 就绪取消、消费者信号清理和 Bash 探测超时已关闭；Linux 实测包含忽略 TERM 后 KILL 的进程。
- `sdk-integration-review.md`：任务 6.1–6.5 Spec/Quality Approve，核对实际 SDK 调用、配置隔离和缺依赖失败。
- `sdk-final-review.md`：唯一最终 Minor 为非 ByteString 管理 token 漏出原生 TypeError。真实 Headers 反例先 1 失败/54 通过，局部捕获为安全 AccountAdminTokenProviderError 后 55 项及真实集成通过；限定复审关闭 M1，最终 Spec/Quality Approve。

实施确认已记入 design.md：沿用根仓库保留权利许可；不扩展既有 filter 语法；错误诊断安全；Flutter 注入 Dio 仅借用 adapter 并隔离配置，调用方负责借用 adapter 生命周期；工具补丁版本按实测记录。

## Task Mapping and Remaining Work

- 1.x：独立包、锁文件、实际工具和 35 操作覆盖；2.x/5.x：公开协议与全部 HTTP 操作。
- 3.x/4.x：单一会话所有者、存储/刷新时序、四态/补全、验证码、设备、业务请求重放、取消及 testing.dart；包内回归与真实场景共同验证。
- 6.x：真实 accountsvc、嵌入式策略变化、受控 credential、管理权限/审计。
- 7.1/7.3：独立 Git 消费；7.4：Linux 完整 SDK 门禁及缺依赖失败；7.5：独立 Go 全量门禁；8.1–8.4：文档、候选版本检查、远端固定 ref 入口及本记录与严格校验。
- **7.2 仍未完成**：Android 实际构建已通过；iOS 无签名构建入口和 macOS workflow 已实现，本机 Windows 无 macOS/Xcode，尚未执行。不能以纯逻辑或 Android 替代。workflow 的 Linux Android/macOS iOS runner 本身尚未在线运行。

最终任务进度 **40/41**，仅 7.2 保持未勾选。已停止本任务宿主 fixture，测试容器与一次性数据库在验收后清理；原始日志和恢复材料保留在 `.test-output/`。

正式 SDK tag 尚未发布，真实远程 Git 固定 tag 消费待发布后单独执行。参数校验、临时 Git fixture 和入口已有测试，任务 8.3 不表示远端已成功。GitHub Actions 完整通过、iOS 原生构建、真实第三方平台授权仍待验证；兼容矩阵 verified 仅表示本记录列出的真实服务协议组合，不代表所有平台、分发和发布门禁均已满足。

## Pull Request CI Follow-up

PR #20 首轮 [SDK CI](https://github.com/bbxx111/accountkit/actions/runs/41260235990) 的三个 job 均在精确工具链预检失败，尚未进入原生编译。全新 Flutter checkout 首次执行 `--version --machine` 时会在 JSON 之前输出 SDK 下载/初始化进度，旧检查直接 JSON.parse 全部 stdout；本地预热工具链未触发此路径。

新增回归先复现同样的 `Unexpected token '%'`（1 失败、2 通过），修复只提取行首 JSON 对象开始的完整后缀，精确版本比较不变；缺失/畸形 JSON、尾部垃圾及版本不匹配仍拒绝。修复后针对测试 3 通过，Linux 全部脚本测试 **24 通过、0 跳过**。限定规格/质量复核通过，无遗留问题。日志：`.test-output/sdk-ci-first-failure.log`、`sdk-toolchain-ci-red.log`、`sdk-ci-regression.log`。下一轮线上结果另行登记。
