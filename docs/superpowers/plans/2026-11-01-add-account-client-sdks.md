# Account Client SDKs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现两个独立 Git 子目录分发的官方客户端，以及协议、真实服务和独立消费验证。

**Architecture:** Flutter 的 AccountClient 独占会话运行时，AccountController 协调内存流程。管理 TS 客户端只依赖外部 token provider 与标准 Fetch。覆盖检查、测试宿主和隔离消费者留在包外，不成为运行时依赖。

**Tech Stack:** Flutter/Dart、Dio、安全存储，TypeScript 5.8、Vite 6、pnpm 11.24.0，现有 Go accountsvc/PostgreSQL/Redis。

**Spec:** [proposal](../../../openspec/changes/add-account-client-sdks/proposal.md)、[design](../../../openspec/changes/add-account-client-sdks/design.md)、[tasks](../../../openspec/changes/add-account-client-sdks/tasks.md) 及该变更三个 specs。需求和完成状态以 OpenSpec 为准，不在此重写需求。

## Global Constraints

- 本轮仅 accountkit 上游；不修改 ai-food，不创建或推送发布 tag，不提交或推送工作分支。
- 沿用已选 `feat/add-account-client-sdks` 和用户未提交规划；不重置或迁移它们。
- 两包首版 `1.0.0`；名称 `accountkit_flutter` 与 `@accountkit/admin-client`，默认前缀 `/v1` 与 `/admin/v1`。
- TS exports/types 指向 `./src/index.ts`；无 dist、prepare/postinstall、workspace 依赖。Flutter 无 workspace resolution。
- 不变更服务端契约、Go module 或冻结迁移。所有凭据仅存测试内存/忽略目录，错误打印不含正文。
- 每步先行为测试并观察失败，再实现和运行包套件。只在完整实现及验证后勾选 tasks；真实服务、平台与远端分发证据分别报告。
- 本机 Flutter/Dart 补丁版本若与设计初始值不同，记录实际值及未验证组合；不伪造版本证据或修改全局工具链。

## Review Focus

- URL 规范化前的点段、编码斜线和父资源混用：两包请求前拒绝，网络计数为零。
- token provider/存储延迟后取消：不发送迟到请求，不覆盖新会话。
- 204/空响应与畸形成功 JSON：按端点解析，不泛化为成功。
- 成功写操作后依赖不可用：业务成功与会话同步失败分开，写请求计数保持一次。
- 包内测试通过但 Git 安装遗漏源码：隔离消费者只通过 Git SHA/path 引用并冻结重装。

## Task 1: Flutter 包边界、协议与运行时（OpenSpec 1.1、2.1–4.6）

**Files:** 新增 `sdks/accountkit-flutter/pubspec.yaml`、`analysis_options.yaml`、`LICENSE`、`lib/accountkit_flutter.dart`、`lib/testing.dart`；内部按 `models.dart`、`errors.dart`、`transport.dart`、`session.dart`、`client.dart`、`controller.dart`、`platform.dart` 分责；测试位于 `test/`，示例位于 `example/`。

**Interfaces:** 对外 AccountApi、AccountClient、AccountController；方法和路由逐一遵循 D2。TokenStore 整条记录 read/write/clear；时钟、设备、HTTP 可注入。后续测试宿主只用公开入口。

- [ ] 读取 enduser OpenAPI、handler/DTO、客户端流程与相关领域规格，按正式字段写模型/路由测试；运行 `flutter test` 观察缺失实现失败。
- [ ] 建立独立包并 `flutter pub get`；实现开放 credential、严格模型、错误与 URL 校验，运行 `flutter analyze` 和协议测试。
- [ ] 写延迟存储/网络的竞争测试，覆盖登录、切换、刷新、重新认证、退出的 epoch/revision；实现唯一运行时及完整记录提交。
- [ ] 写四态、验证码、补全取消/期限/原请求重试测试；实现 Controller 与 FakeAccountApi。
- [ ] 写可信路径、一次401重放、流拒绝、取消和资源所有权测试；实现 Dio 适配与平台适配。
- [ ] 运行 `dart format --output=none --set-exit-if-changed lib test example`、`flutter analyze`、`flutter test`；提交给独立审查，保存分项证据。

## Task 2: 管理 TS 独立包和全部操作（OpenSpec 1.2、5.1–5.6）

**Files:** 新增 `sdks/accountkit-admin-ts/{package.json,pnpm-lock.yaml,tsconfig.json,vitest.config.ts,LICENSE}`；`src/{index,client,models,errors,filter}.ts`；`test/client.test.ts`。

**Interfaces:** createAccountAdminClient(config)；配置 baseUrl/apiPrefix/getAccessToken/fetch/requestId。方法名遵循 D2；方法尾参数接受 signal/requestId。DTO 保持 wire snake_case。

- [ ] 读取管理 OpenAPI、handler/DTO/filter 及相关规格；写全部11操作的请求/响应测试和 provider/取消测试，运行 `pnpm test` 确认失败。
- [ ] 实现独立源码包和逐请求 provider/Fetch 传输；实现结构解码、六类错误、URL/层级校验。
- [ ] 实现用户、身份 reveal、会话、审计及 filter；增加 nullable、未知 state、非JSON、空响应和单次执行测试。
- [ ] 运行 `pnpm install --frozen-lockfile`、`pnpm typecheck`、`pnpm test`；独立审查，保存分项证据。

## Task 3: 工具链与覆盖登记（OpenSpec 1.3–1.4）

**Files:** `sdks/tool-versions.json`、`sdks/http-coverage.json`、`scripts/check-sdk-coverage.mjs`、`scripts/check-sdk-coverage.test.mjs`。

**Interfaces:** 覆盖记录 surface/operationId/method/path/sdkMethod/models/tests/baseline/optionalCapability；检查器接受可选清单路径，非零退出报告缺失/额外/重复。

- [ ] 先写真实 OpenAPI 对照及删条/增条负例，`node --test scripts/check-sdk-coverage.test.mjs` 观察失败。
- [ ] 从现有 OpenAPI 建35项登记，关联两包实际模型和测试；实现集合对照，不硬编码数量代替集合比较。
- [ ] 记录实际工具补丁及直接依赖，冻结两包安装；运行覆盖检查与负例测试并审查。

## Task 4: 真实服务契约（OpenSpec 6.1–6.5）

**Files:** `sdks/testsupport/` Go 测试宿主及启动配置；两包 `test/integration/`。

**Interfaces:** 本机测试服务 origin、验证码测试邮箱和管理员 JWKS；服务 fixture 使用现有 New/Migrate/Start/Close 与 accountsvc 装配规则。缺少依赖直接失败。

- [ ] 写启动依赖缺失失败检查；启动独立命名 PostgreSQL/Redis，合成发送器/JWKS。
- [ ] 两包通过公开 SDK API 执行 tasks 6.2–6.5 的真实流程，记录 scope/DTO/会话结果，不另写 HTTP 客户端代替。
- [ ] 区分默认 accountsvc 与嵌入式可选能力 fixture、受控提供方与真实平台授权，审查并记录结果。

## Task 5: Git 隔离消费与 CI（OpenSpec 7.1–7.5）

**Files:** `scripts/verify-client-sdks.sh`、`scripts/verify-sdk-consumers.*`、`sdks/testsupport/consumers/`、`.github/workflows/client-sdks.yml`。

**Interfaces:** 从工作树快照建立临时 Git SHA；本机 Git 服务只监听 loopback。消费者仅 Git URL/ref/path，临时资源归属可核查。

- [ ] 编写依赖缺失非零退出检查；固定工具版本，执行两包静态/行为与真实服务测试。
- [ ] 从仓库外消费者安装并冻结重装；执行 Flutter analyze/test、TS typecheck/test/Vite build。
- [ ] Linux runner 构建 Android debug，macOS runner 构建 iOS 无签名；未在本机执行的平台明确未验证。
- [ ] 保留现有 Go CI，运行构建/vet/单元及可用数据库/race/恢复入口，记录未执行项。

## Task 6: 文档与版本验证（OpenSpec 8.1–8.3）

**Files:** 两包 README/CHANGELOG、`docs/client-sdks.md`、根 README/AGENTS/兼容说明、`scripts/check-sdk-release.*`、发布后远端消费入口。

- [ ] 写 manifest/tag 合法与不匹配测试，执行红灯后实现校验（只校验，不创建 tag）。
- [ ] 远端消费入口要求固定 tag/ref，fixture 检查参数和失败行为；未发布记待执行。
- [ ] 文档引用实际公开 API、平台注入、成功后同步结果和应用认证职责；审查链接及示例。

## Task 7: 汇总验收（OpenSpec 8.4）

**Files:** `openspec/changes/add-account-client-sdks/verification.md`、`tasks.md`。

- [ ] 独立审查实现与全部场景，修复实质问题后重跑受影响验证。
- [ ] `openspec validate --all --strict`、`git diff --check`，更新各任务实测状态；日志放 `.test-output/`。
- [ ] 明确未验证平台/远端/工具组合；不归档、不发布、不改下游。
# 后续执行：CI 范围与目录命名

对应 OpenSpec tasks 9.1–9.2，沿用当前任务分支及未提交的 SDK workflow 过滤修改。

1. 先将 Git 消费与快照测试期望改为 sdks，确认旧路径实现失败。核对源目录及目标都位于本仓库后迁移源码；逐项同步路径引用，保留 sdk/flutter/v…、sdk/admin-ts/v… tag 和两个公开包名。
2. Go workflow 只新增两个客户端包目录的 paths-ignore，保留 testsupport/fixture 及其他 Go 输入。用 YAML 解析对照原配置，检查手动入口、分支、job/concurrency 未改变；SDK workflow 继续保留此前用户要求的文档排除。
3. 执行 Linux 验证脚本、覆盖和发布元数据检查、Go 独立构建及 fixture test/vet、仓库外固定 Git SHA 的 TS/Vite 与 Flutter 消费。原生业务代码不变，不将本次逻辑/Git 检查冒充新的 Android/iOS 构建结果。
4. 核对残留旧路径、Git tag 格式与文件移动内容，更新 verification/tasks，执行 OpenSpec 全量严格校验。历史原始日志和旧提交证据保留原路径语义。
