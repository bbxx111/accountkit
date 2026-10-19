# accountsvc Prometheus Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 显式启用 accountsvc 的既有 kit Prometheus 指标。

**Architecture:** 服务独占 Registry，向 kit 注入现有 Observer；在原监听器挂 GET /metrics。默认关闭，migrate 忽略指标设置，访问范围由部署网络与网关控制。

**Tech Stack:** Go、Chi、client_golang、现有 accountkit observability/prometheus。

**Spec:** [设计](../../../openspec/changes/add-accountsvc-prometheus/design.md) 与该变更 specs/ 下全部规格。

## Global Constraints

- 使用 feat/accountsvc-prometheus 任务分支；不提交、推送、归档或改动迁移。
- 开关 ACCOUNTSVC_METRICS_ENABLED 默认 false；INSTANCE 为显式非秘密稳定标签，格式 `[A-Za-z0-9][A-Za-z0-9._-]{0,63}`。
- 复用现有单监听器/TLS/关闭流程；无 OTLP、运行时 collectors 或认证行为变化。
- 每步先失败测试再实现；结果写入对应变更 verification.md，原始输出留 .test-output。

## Review Focus

- migrate 收到非法服务指标配置也可装配：配置测试覆盖。
- 同进程多应用无默认 Registry 冲突或数据混入：隔离测试覆盖。
- 业务依赖故障时抓取不增加 I/O：应用测试及依赖计数覆盖。
- 采集错误可能含隐私：错误 gatherer 测试校验安全 503；并发采集超额也有界。
- 公网未透传指标但监控可抓取：代理及进程集成测试覆盖。

### Task 1: 配置、装配与采集

**Files:** internal/accountsvc/config.go、run.go、http.go；新增 metrics.go、metrics_test.go、metrics_config_test.go。
**Interfaces:** Config 增加 MetricsEnabled bool、MetricsInstance string；application 增加 metrics http.Handler；serviceHandler 在 state 参数后增加可选 metrics ...http.Handler 以保留既有调用。newMetrics(cfg Config) (observability.Observer, http.Handler, error) 负责构造；newMetricsHandler(prometheus.Gatherer) http.Handler 负责受限且脱敏采集。

- [x] 先增加 TestConfigMetrics，使用现有 serveEnvironment/configEnvironment，校验缺省/空/false、合法标签、非法布尔/标签、关闭忽略标签、migrate 忽略指标设置；运行 go test -run TestConfigMetrics ./internal/accountsvc 并记录 RED。
- [x] 在配置中实现解析；非法值错误仅包含字段名称；在监听前失败。
- [x] 增加 TestMetricsHTTP、TestMetricsIsolation、TestApplicationMetrics、TestMetricsGatherFailure、TestMetricsConcurrencyLimit：校验启用与关闭、GET/405、no-store、全局 collector 不输出、真实 Authenticate 无效 token 操作产生一次指标、未启用不导出、多应用隔离、依赖计数为零、错误原文不泄露、最多 5 个并发采集；先运行 RED。
- [x] 实现独立 Registry 及 Observer、应用注入、GET 路由、错误脱敏与并发限制；遵守既有指标契约，不拷贝指标目录。
- [x] gofmt；go test ./internal/accountsvc/... 与 go vet ./internal/accountsvc/...；独立审查。

### Task 2: 部署说明与真实服务验收

**Files:** deploy/accountsvc/compose.yaml、.env.example；docs/accountsvc.md、metrics.md、gateway-integration.md、release-checklist.md；internal/accountsvc/metrics_integration_test.go、gateway_integration_test.go；scripts/accountsvc-required-tests.txt。
**Interfaces:** 使用 Task 1 的两个配置变量与 GET /metrics，复用服务 fixture，不新增公开 Go API。

- [x] 更新部署变量默认关闭，文档说明显式实例标签、同监听器传输、无独立认证/必须限网、重启/空序列/关闭尾部观测边界；本地链接和模板变量检查。
- [x] 增加 TestServiceIntegrationMetrics，复用 Linux 进程及 SMTP/PG/Redis fixture，执行真实登录/内省等行为再抓取对应计数/耗时；加入必需测试清单。代理测试明确公网 /metrics 不转发且授权监控可访问。
- [x] 在一次性容器运行 scripts/verify-accountsvc.sh，验证 race、真实服务与现有回归；记录日志和清理本次容器。
- [x] 最终审查，GOWORK=off go build ./...、go vet ./...、go test -count=1 ./...、OpenSpec 全量严格校验、git diff --check；记录未运行的发布/产品环境检查，不用 skip 充当集成成功。
