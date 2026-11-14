# accountsvc 认证强化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 按已确认顺序强化三类认证，服务 Bearer 强制注册权限与 scope 取交集。

**Architecture:** 保留三类主体和授权，共享 HTTP 凭据解析与外部 JWT 验签；管理员依赖在请求边界 fail-closed。服务身份绑定稳定 sub，profile 明确选择，不从请求猜测。

**Tech Stack:** Go 1.26.5、Chi、golang-jwt/v5，既有 PostgreSQL/Redis 与测试夹具。

**Spec:** [变更设计](../../../openspec/changes/harden-accountsvc-authentication/design.md) 及同目录 specs；用户已确认仅实施通用方案，不加入 Keycloak 专用逻辑。

## Global Constraints

- 不改消费者 JWT、数据库和已冻结迁移；不合并 Principal，不自动认证回退。
- HTTP URL、错误体、认证先于业务解析保持；不推送、提交、归档或发布。
- 在当前任务分支实施，保留用户工作；先写失败行为测试，再实现、gofmt、相关测试及独立复核。
- scope 强制交集，不提供 registry-only 开关；Basic 为注册权限。

## Review Focus

- 重复 Authorization 不得只取第一项（Task 1）。
- 实际挂载与服务权限清单不漂移，认证必须先于方法检查（Task 2）。
- 首个等待者取消不得令其他管理员请求失败，预加载与请求不得重复抓取（Task 3）。
- 已登记客户端的人类 sub、缺失 scope 不能放行；包含正确 audience 的多受众 token 仍须通过同样主体和权限校验（Task 4）。
- 新环境变量与旧夹具、SDK测试服务、文档例子须一致，缺依赖明确报告（Task 5）。

### Task 1: HTTP 凭据解析
**Files:** 新增 internal/httpcredentials/credentials.go 与 credentials_test.go；修改 httpapi/authn/authn.go、internal/accountsvc/adminauth/verifier.go、serviceauth/serviceauth.go 与各自测试、docs/accountsvc.md。
**Interfaces:** 解析器 `Parse(r *http.Request) (scheme, credential string, ok bool)`；最大 Bearer 长度常量16384；只解析一个头、不做认证。
- [x] 写消费者重复头不会调用 Authenticator、三入口格式/大小写/16KiB边界测试并观察失败。
- [x] 实现公共解析与三入口适配；ASCII 空格允许多空格，拒绝tab/控制字符/嵌入凭据分隔；保留 BearerAttempted 和挑战语义。
- [x] 运行 `go test ./internal/httpcredentials ./httpapi/authn ./internal/accountsvc/adminauth ./internal/accountsvc/serviceauth` 和对应 vet；独立复核。

### Task 2: 路由与权限清单
**Files:** 新增 internal/accountsvc/routes.go 与 routes_test.go；修改 http.go、run.go 中服务权限/路径引用、docs/gateway-integration.md。
**Interfaces:** 由不可变内部声明表达路径、方法、类别及服务权限；服务路由实际挂载及服务认证装配消费同一声明，避免仅测试镜像常量。
- [x] 写三个服务路由凭据隔离及路径/权限声明与实际处理匹配的失败测试。
- [x] 实现清单接入，保留 Handle 让 handler 先认证再判方法；消费者和管理员保留原 Mount。
- [x] 更新网关表及 Basic-only 旧注释；运行 `go test ./internal/accountsvc -run 'Route|HTTP|Gateway|Unconfigured'` 及 vet；独立复核。

### Task 3: 管理员故障隔离
**Files:** internal/accountsvc/adminauth/verifier.go、oidcjwt/verifier.go、run.go 及关联 cache/run/shutdown 测试、docs/accountsvc.md。
**Interfaces:** 保留 adminauth.New 参数并移除I/O；提供 `Load(ctx context.Context) error` 预加载入口，与请求共享刷新/冷却；app 管理 admin 和 service 预加载生命周期。
- [x] 先写 New 不访问失败提供方、服务就绪不等待提供方、恢复成功和并发预加载只抓取一次测试，观察失败。
- [x] 静态构造即挂管理员中间件；监听后预加载；等待最多3秒；保留管理员60秒/服务10秒冷却、1小时key寿命、关闭回收。
- [x] 运行 `go test ./internal/accountsvc/...`、相关 race 与 vet；记录夹具旧启动行为的必要调整，独立复核。

### Task 4: 强制服务授权和令牌契约
**Files:** serviceauth/registry.go、serviceauth.go、config_service_clients.go、config.go、oidcjwt/verifier.go、adminauth/config.go、提供方测试夹具、三服务端点测试与部署文档。
**Interfaces:** ClientConfig 增加 BearerSubjects；服务验证后按明确 profile 提取客户端、检查 sub 与 scope。配置名称以 design.md 为准。所有 bearer 均强制交集，不保留宽松路径。
- [x] 写缺失/空scope403、非法scope401、未知scope不扩权、错误sub401、未知客户端403、不同profile不回退测试并观察失败。
- [x] 实现严格配置与声明规则，更新夹具使成功路径签发真实满足新契约的令牌；不得用移除断言掩盖失败。
- [x] 更新文档、示例模板与HTTP覆盖登记（契约受影响部分），运行 serviceauth/oidcjwt/adminauth/accountsvc 三端点和配置测试及 vet；独立复核。

### Task 5: 综合验证
**Files:** openspec/changes/harden-accountsvc-authentication/verification.md、tasks.md，必要的既有验证入口。
- [x] 运行 GOWORK=off go build ./...、go vet ./...、go test -count=1 ./...；认证并发运行 race，真实服务门禁按 docs/development.md 准备一次性资源。
- [x] 运行 openspec validate --all --strict；完整审查后修复实质问题。
- [x] verification.md 记录实际命令、退出状态、skip与未验证部署项；只勾选真正完成且验证的任务。

### Task 6: Profile 模式值改名
**Files:** internal/accountsvc/oidcjwt/verifier.go、config.go及profile/config/serviceauth测试，部署示例、对应文档和当前变更工件。
**Interfaces:** 有效profile为缺省/空、default、rfc9068；缺省解析为default；旧azp值拒绝。default服务身份声明仍为azp，管理员不新增azp要求；RFC9068逻辑不变。
- [x] 先修改配置和profile回归测试，确认default被旧实现拒绝及azp仍被接受的RED。
- [x] 精确替换模式名、缺省值及描述，保留JWT字段azp和历史验收结果；同步配置示例和规格。
- [x] 运行相关accountsvc测试/vet、OpenAPI/coverage检查与变更strict校验，独立复核后追加验收记录。
