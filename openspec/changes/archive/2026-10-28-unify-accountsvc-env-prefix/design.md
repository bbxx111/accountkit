## Context

`internal/accountsvc/config.go` 用 `accountkit.ConfigFromEnv("ACCOUNTKIT_")` 读库配置，用 `env()`（`ACCOUNTSVC_` 前缀）读服务配置；部分校验错误直接拼出 `ACCOUNTKIT_` 变量名。当前两组变量去掉前缀后不重名：库约 42 个固定名加 `ALIPAY_<AppID>_*`、`OIDC_<KIND>_*` 动态名；服务约 30 个（`DATABASE_URL`、`REDIS_URL`、`HTTP_ADDR`、`MODE`、`TLS_*`、`SMTP_*`、`SMS_*`、`ADMIN_*`、`METRICS_*`、`INTROSPECTION_CLIENTS`、`PURGE_FEED_CLIENTS` 等）。

## Goals / Non-Goals

**Goals:** accountsvc 只读一个前缀；旧前缀配置不会被静默忽略；以后新增配置时自动发现重名。

**Non-Goals:** 不改库 `ConfigFromEnv` 行为，不改嵌入示例与库文档的示例前缀，不提供旧名回退读取，不改测试基础设施变量（`ACCOUNTKIT_RECOVERY_*`、`SERVER_TEST_DB_DSN`、`ACCOUNTSVC_TEST_*`）。

## Decisions

### D1 直接切换，不做旧名回退

尚无正式部署（用户确认），双读会让「哪个值生效」变得含糊，且旧名会长期残留。改为启动时拒绝：`LoadConfig` 在最开始（serve 与 migrate 都执行）扫描环境，任何非空的 `ACCOUNTKIT_*` 变量都使启动失败，错误为 `accountsvc: ACCOUNTKIT_ variables are no longer read; rename <NAME> to ACCOUNTSVC_<SUFFIX>`，只含变量名。

理由：必填项缺失本来会报错，但可选项（如保留期、资料补填开关、身份策略）改名后会被静默忽略，行为悄悄变化。选择「非空」而不是「存在」，与现有 `ACCOUNTSVC_INTERNAL_ADDR` 的处理一致，也允许编排模板中留空占位。

备选：只拒绝库能识别的旧名——需要库暴露变量清单，且像 `ACCOUNTKIT_RECOVERY_SOURCE_DSN` 这种测试变量会被放过而产生误导；但与 accountsvc 同进程运行的场景本就不应设置这些变量。进程级测试夹具会在启动子进程前剔除 `ACCOUNTKIT_*` 变量。

### D2 前缀常量

`config.go` 定义 `envPrefix = "ACCOUNTSVC_"`，`env()`、`ConfigFromEnv`、错误名映射和 `safeLibraryConfigError` 的匹配都使用它，不再出现硬编码的 `ACCOUNTKIT_`。

### D3 重名检测测试

测试从 `internal/accountsvc` 非测试源码中收集服务配置名（`env`/`defaultEnv`/`durationEnv` 的参数、`"ACCOUNTSVC_…"` 字面量、短信模板后缀），然后：

- 每个服务名都不以 `ALIPAY_`、`OIDC_` 开头；
- 对每个服务名设置一个探测值后调用 `accountkit.ConfigFromEnv("ACCOUNTSVC_")`，结果与未设置时相同（库不读取该名字）。

这样库或服务任一侧新增同名配置都会让测试失败。反射比较若遇到函数字段，测试改为比较错误与关键字段，实施时按实际类型选择。

### D4 文档与模板

- `docs/accountsvc.md` 变量表和示例全部改为 `ACCOUNTSVC_`；说明库配置项的名字与 README 表相同、只是前缀为 `ACCOUNTSVC_`。
- 支付宝/QQ/微博文档中「accountsvc 固定为 `ACCOUNTKIT_`」改为 `ACCOUNTSVC_`，示例变量同步改名。
- `docs/compatibility.md` 新增迁移小节：改名规则（把 `ACCOUNTKIT_` 换成 `ACCOUNTSVC_`）、启动拒绝行为、嵌入宿主不受影响。
- `deploy/accountsvc/compose.yaml`、`.env.example` 改名。

## Risks / Trade-offs

- [接入方遗漏改名] → 启动拒绝会指出具体变量；通知 ai-food。
- [同一进程环境中有其他用途的 `ACCOUNTKIT_*` 变量] → 视为错误并要求移除；accountsvc 进程不应与嵌入宿主共享环境。
- [将来库与服务新增同名配置] → D3 测试发现。

## Migration Plan

部署方把所有 `ACCOUNTKIT_` 变量改为 `ACCOUNTSVC_`（后缀不变），删除旧变量后重启。回退到旧版本需要把名字改回。不涉及数据。
