# 单一服务客户端配置 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 按用户在 b25ec98 提交后的要求，去掉旧逐接口配置模式，合并 Compose 文件，只保留统一客户端注册表。

**Architecture:** serve 始终加载一个 Registry，未设置/空 SERVICE_CLIENTS 均产生空注册表；旧四项配置非空白时报错。serviceauth 只保留 Registry + RequiredPermission 路径。基础 Compose 直接 env_file: ./.env，动态别名进入容器，无额外 override。

**Tech Stack:** Go 1.26.5、现有 Matcher/OIDC verifier、PostgreSQL 17、Redis 7、Docker Compose。

**Spec:** [现有变更](../../../openspec/changes/archive/2026-11-14-unify-service-client-auth/design.md)，由用户新要求修订 proposal/design/四份 delta；不新建重复 change、不归档。

## Global Constraints

- 仅删除旧配置模式；basic、bearer 和 tokens.introspect、purged_users.read 均保留。认证、JWT、缓存、挑战、权限与 503 语义沿用统一模式。
- SERVICE_CLIENTS 未设置、空、全空白均表示空注册表。存在未声明字段仍拒绝；旧四项非空白变量（含 []/{}）拒绝并仅指出变量名与移除原因，空白旧占位忽略。
- migrate 继续不解析服务运行变量。无客户端时 serve 正常启动，两能力返回 NOT_CONFIGURED 503；无 Bearer 时多余 issuer/audience 仍拒绝。
- 去掉 Config 的旧映射/名单、模式选择、弃用警告及 serviceauth 的旧分支；不只隐藏环境变量入口。Basic-only helper 若保留，必须构造 Registry 并进入相同授权路径。
- 删除失去生产调用者的旧 JSON 解析/跨接口 SharedSecret 辅助；保留 Matcher、轮换、固定长度摘要常数时间比较。旧行为测试迁移为新契约验证，不放宽安全断言。
- 迁移全部活跃测试夹具与示例；历史 archive/release 记录保持原事实。必须测试清单只随已删除旧契约测试作可解释映射，并加入新拒绝/空注册表测试，不通过删项逃避失败。
- 不修改数据库、消费者/管理员协议、依赖版本、发布 tag。当前任务分支原地实施；此前“先提交”授权已由 b25ec98 完成，此轮不自动提交/推送/归档。

## Review Focus

- 仅配置旧变量时不能静默变成无认证接口；监听前明确拒绝（Task 2）。
- 缺失主变量与显式空值行为一致，但孤立别名字段仍失败（Task 2）。
- nil Registry 的内部便利构造不能悄悄恢复旧授权路径（Task 2）。
- Compose 的 service env_file 与 CLI --env-file 必须指向同一文件；基础 environment 中不保留旧变量默认注入（Task 3）。
- 夹具从两份秘密转为注册表不能让原权限拒绝测试失去意义，Basic 缺权限按403断言（Task 2）。

### Task 1: 修订当前变更契约

**Files:** `openspec/changes/archive/2026-11-14-unify-service-client-auth/proposal.md`、`design.md`、四份 `specs/*/spec.md`、`tasks.md`。

**Interfaces:** 最终契约采用 Global Constraints；原已完成18项保留为 b25ec98 历史阶段并明确被新要求覆盖，新增 5.1–5.4 待完成项，覆盖核心实现、夹具回归、Compose文档、最终验收。

- [x] 将最终方案改为单一注册表；移除旧模式可用/混用回退/弃用警告要求，旧变量只拒绝。
- [x] 保留旧主规格 MODIFIED requirement 的精确标题和未变安全要求；新增要求描述旧配置移除与单一 Compose。
- [x] design 记录用户于 b25ec98 后明确改变兼容目标；历史验收记录不改写为本轮通过。
- [x] 运行当前变更严格校验，读取 apply 指令确认新增待办 ready；核对与计划一致。

### Task 2: 去掉全部旧代码路径并迁移测试

**Files:** `internal/accountsvc/config*.go`（Task 3 独占 config_service_clients_examples_test.go）、`run*.go`、`serviceauth/`、`basicclients/`、`introspection/`、`purgefeed/`、现有集成夹具和调用方测试；检查 `sdks/testsupport` 活跃消费；两份 required-tests 清单。

**Interfaces:** Config.ServiceClients 始终为 LoadConfig(serve) 创建的 Registry；ServiceConfig 仅保留 issuer/audience/AllowHTTP 与必要 Bearer ID；serviceauth.Config 只用 Registry、RequiredPermission、BearerVerifier；Basic-only New helper 转换为 ClientConfig 并调用 NewWithAuthenticator。

- [x] 先写失败行为测试：仅旧配置、混合旧配置均拒绝；未设置主列表变成非 nil 空 Registry；无弃用警告；migrate 忽略旧/新非法服务变量。
- [x] 运行定向测试记录 RED，删除旧字段/解析器/装配/认证分支，复用新注册表，不增加另一层模式开关。
- [x] 迁移现有 Basic/Bearer 配置、HTTP/JWT安全与进程夹具，保留全部仍适用的拒绝/轮换/时效/故障断言；用新配置的403替代旧跨接口Basic401，只对授权模型变更的用例调整。
- [x] 真实进程验证旧变量监听前失败、仅空注册表正常启动、双方法权限及原数据库吊销/feed事务保持；旧配置回退测试改为拒绝旧配置。
- [x] 删除确无生产调用的旧 JSON helper 及其旧格式专用测试，报告 mandatory 条目的替代映射；其余门禁保持。
- [x] gofmt、定向 unit/vet 与真实 Linux race 集成后独立审查。

### Task 3: 合并 Compose 与更新接入资料

**Files:** `deploy/accountsvc/compose.yaml`、删除 `compose.service-clients.yaml`、`.env.example`、`docs/accountsvc.md`、`docs/compatibility.md`、README、`examples/remoteauth` 文档/注释、`internal/accountsvc/config_service_clients_examples_test.go`；同步修正 `docs/alipay-sign-in.md`、`docs/qq-sign-in.md`、`docs/weibo-sign-in.md` 中因 env_file 合并而过时的变量透传说明，不改变登录契约。

**Interfaces:** 只用一个 Compose 文件；accountsvc.env_file 为 ./.env，文档标准命令 --env-file deploy/accountsvc/.env。保留 Basic-only/Bearer-only/双方式及轮换示例。

- [x] 先将示例测试改为单文件 Compose、未配置即空 Registry、旧变量明确拒绝，观察失败。
- [x] 合并 env_file、删除 override 和旧四项映射；模板不含可运行旧格式，迁移说明要求显式将客户端与权限转换。
- [x] 清理活跃文档/注释中的模式切换和旧模式恢复承诺；需要回滚时说明须回退旧二进制及匹配配置。
- [x] 真实 Docker Compose config 验证默认空、三种方式、动态下划线别名、旧变量拒绝；不输出凭据，独立审查。

### Task 4: 最终验收与记录

**Files:** 当前 change 的 tasks/verification、此计划的进度；原始日志写 `.test-output/service-clients-only/`。

- [x] 运行 GOWORK=off 的完整 build/vet/race、真实 PostgreSQL/Redis accountsvc 专项及恢复门禁；使用本轮创建的可识别一次性资源。
- [x] 核查 SDK HTTP 覆盖、无数据库/公共协议差异、gofmt/diff check；全量独立审查。
- [x] OpenSpec 当前变更及全库严格校验，已知7份主规格长度问题单列；更新新增5.1–5.4与验收记录，保留历史证据。
- [x] 清理本轮测试容器与卷，保留当前工作区；报告未验证项，不自动提交。
