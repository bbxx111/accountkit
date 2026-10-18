# Go 集成与 HTTP 契约文档验证

日期：2026-10-18。运行时代码基准：`9b8d626`；工作分支：`docs/go-integration-openapi`。

## 范围与执行方式

用户要求先完善 Go 集成文档和示例，再处理 OpenAPI。本轮按既有契约的文档维护直接顺序执行；新增 Go 文件仅为示例包说明与可编译示例，不修改库、HTTP handler、SQL、配置或依赖。通过 CLI 创建本记录所在变更并设置 `skip_specs: true`，没有新增行为 delta；不适用架构设计文档条件，未另建重复实现计划。

交付包括 [Go 集成指南](../../../../docs/go-integration.md)、[调用片段](../../../../examples/gointegration/example_test.go)、[OpenAPI 使用与维护说明](../../../../docs/openapi.md)、消费者/管理面两份 JSON，以及 [源码对应检查](../../../../scripts/check-openapi.cjs)。无客户端 SDK、服务端代码生成或在线文档托管。

## 实际验证

以下命令在当前工作树执行，Go 设置 `GOWORK=off`；使用仓库忽略目录作为 GOCACHE。

| 检查 | 实际结果与范围 |
|---|---|
| `go test -count=1 -v ./examples/gointegration` | 两个带 Output 的合成示例通过，其余接入片段编译通过且未执行 I/O |
| `go test -count=1 ./examples/... ./httpapi/...` | 9 个有测试的包通过，audit/embedded 两个示例包无测试文件；覆盖既有 HTTP 协议回归 |
| `go vet ./examples/... ./httpapi/...` | 退出 0 |
| `go build ./...` | 退出 0，独立 module 构建通过 |
| `node scripts/check-openapi.cjs` | 消费者 21、管理面 11 个操作与 Router 一致；文档内引用、路径参数及主要响应 DTO 字段/必填/可空检查通过 |
| `@apidevtools/swagger-parser@12.1.0` | 两份文档的 OpenAPI 3.1 结构与引用校验通过 |
| Ajv 8.17.1 / ajv-formats 3.0.1 | 35 个合成正反例通过：凭证 oneOf/null、未知字段、轮次格式、补全票据、token/pending、access-only、scope、nullable 与 optional、两种错误体、管理 DTO、UUIDv7 审计名 |
| `openspec validate --all --strict` | 22 项通过，0 失败；使用本次忽略目录中安装的 1.11.0 CLI |
| 文档与格式 | 73 个本地链接存在，新指南中的本地锚点有效；修改文件空白/末尾换行、`git diff --check` 与 gofmt 检查通过 |

合成 schema 检查脚本保存在忽略目录 `.test-output/validate-openapi-samples.cjs`；起草辅助脚本 `.test-output/build-openapi.cjs` 不是生产生成入口，正式 JSON 直接维护。检查没有把正反例或源码静态比对当成真实业务集成验证。

## 审查与修正

独立只读审查对照公开 Go 类型、领域实现和 HTTP 路由/DTO 进行核验，发现并修正三项说明问题：

1. 明确直接 Go 调用 `DeleteMe` / `UnbindIdentity` 前的 scope 与近期认证由宿主执行，`Undelete` 必须使用专用恢复 scope；不能误以为所有领域方法均执行 HTTP 中间件的检查。
2. 补全示例处理 `CompleteSignIn` 继续返回 Pending 的情况，并提醒 token 仍可能为 `user:bind`。
3. 错误表区分输入错误、已知状态/策略/IdP 业务拒绝与未知内部错误，避免把所有未列举错误映射成 500。

复审确认上述修正，并逐项核对 21/11 个 operation、scope/role、oneOf/null、token/pending/reauth、状态码、分页、资源 ID 与审计 DTO，未发现实质协议问题；当时待落地的 OpenAPI 说明页已补齐。

最终脚本审查发现路由提取未包含 Trace：审查者在内存注入 Trace 路由后仍通过。已补齐 Trace，并对 Route/Mount/Method/Handle 和非字面量路径显式失败；文档明确跨文件或辅助函数注册须调整提取范围。修复后在内存分别注入 Trace、Method 与变量路径，三个探针均确认检查失败，未修改源码；正常检查仍覆盖 21/11 个既有操作。当前无未处理的实质审查问题。

## 环境问题及处理

- 默认 Go 构建缓存目录不可写，首次三条命令未成功；改用 `.test-output/go-integration-build-cache` 后通过。最终构建曾提示模块统计缓存 `.info` 不可写，但命令退出 0；未修改系统目录权限或项目依赖。
- PATH 中没有 OpenSpec CLI，版本 1.11.0 和 OpenAPI 校验工具经权限审查后仅安装到 `.test-output/contract-tools`，未全局安装，未新增根 package.json 或修改 go.mod/go.sum。
- 首次 OpenAPI 校验拒绝服务器变量的标准 `description` 字段。定位到校验器附带 meta-schema 将该属性拼为 `descriptions`；未修改标准或校验器，将相同说明移到合法的 Server Object description 后，两份文档通过原校验。

## 未验证与交付边界

本轮未运行真实数据库/Redis 集成、race、备份恢复、完整发布入口、在线 CI、实际服务商联调或产品部署。没有改认证运行时代码，也没有将既有验收记录冒充本次通过。Go 片段的 nil/零值依赖声明仅表示宿主应替换的上下文，未宣称它们是可独立运行的完整客户端。

静态源码对应检查不证明状态迁移或安全授权行为；OpenAPI schema 合法不证明任意 SDK 生成器、网关或 Mock 工具兼容。管理员外部 verifier 的拒绝响应仍由宿主定义。实施验收阶段未执行版本发布、提交、推送、合并和归档。

## 提交与本地合并前复核

用户随后明确要求“提交，合并”。2026-10-18 在任务分支重新执行 examples/httpapi 测试与 vet、全库 build、OpenAPI 结构与 35 个 schema 正反例、21/11 路由/DTO 对应、73 个本地链接及 OpenSpec 22 项严格校验，全部通过。develop 仍指向原基准 `9b8d626`，没有额外运行时代码差异；集成采用 `git merge --no-ff` 保留独立合并提交。该授权不包含推送、发布或归档。

## 归档核验（2026-10-18）

用户明确要求归档已完成 change，本次在 chore/archive-completed-changes 分支完成归档，目录为 openspec/changes/archive/2026-10-18-document-go-integration-and-http-contracts。本节补记后续归档动作，前文实施、提交和验证边界保留为当时记录。

本变更声明 skip_specs: true，无增量规格；design 指令仅在存在架构、依赖、数据模型、安全或迁移等设计需求时要求创建设计文档，本变更按原验收记录的纯文档维护范围保持条件跳过。CLI 状态仍将 design 显示为 ready，此项不代表存在未完成实现。

本批三个变更共 24 项任务均已完成，归档保留 .openspec.yaml、提案、任务及已有设计、规格和验收记录，修复迁移后的相对链接及外部引用。实际归档检查：

- 使用仓库忽略目录中的 OpenSpec 1.11.0 CLI 执行 validate --specs --strict 与 validate --all --strict，均为 19 项通过、0 失败；list --json 返回空活动变更列表。
- 逐项复核两份增量的 7 个要求均已同步，原有场景和未涉及要求保持。
- 归档后的 13 份非验收工件与 HEAD 原路径内容一致（按 Git 工作树换行转换规范化 CRLF/LF 比较）；检查 19 份 Markdown 的 45 个本地链接，目标均存在，未遗留旧活动目录引用。
- git diff --check 通过。

本次仅同步规格、移动工件及维护引用，未修改运行时代码、SQL 或固定源快照；未重跑 Go、数据库、race 或恢复演练，也不将历史验证记录视为本次运行结果。归档阶段未提交、合并、推送或发布。

## 合并前复核（2026-10-19）

用户明确要求本地合并。本轮重新执行 OpenSpec 全量严格校验（19 项通过、0 失败）、活动变更查询（空）、归档内容与本地链接检查（13 份非验收工件保持、19 份 Markdown 的 45 个链接均存在）及 git diff --check，均通过。develop 与任务分支基点同为 7a829c1，没有额外待整合的代码差异。按仓库约定提交归档改动后使用 git merge --no-ff 合入 develop；本次授权不包含推送或发布。纯文档和规格维护未重跑运行时测试。

## 中文记录恢复（2026-10-19）

后续标准化指标提案检查发现，前两节通过默认 us-ascii 的 PowerShell 管道传给 Node，新增中文已被替换为问号。此处按原操作与验收证据恢复中文，之前的实施记录保持；不代表重新执行了历史运行时测试。
