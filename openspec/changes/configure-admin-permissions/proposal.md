# Proposal

## Why

accountsvc 当前把 11 个管理操作固定分配给 `operator`、`super-admin`，部署方无法独立配置客服、风控与审计职责。需要将操作权限与外部角色分离，同时保留独立管理员身份、领域约束和拒绝审计，并集中维护相关代码中的重复契约值与校验逻辑。

## What Changes

- 定义管理面专属的类型化权限目录，使用 `资源.操作`，暂不增加 `account` 命名空间；管理路由按具体权限检查。
- 支持精确权限及 `<资源组>.*`，启动时展开；通配符在升级后包含该组新增管理操作，不匹配其他资源组或服务调用权限。不支持全局 `*`、任意 glob、排除式和正则。
- **BREAKING**：accountsvc 移除 `operator` / `super-admin` 隐式授权及继承。仅通过 `ACCOUNTSVC_ADMIN_ROLE_BINDINGS`、`ACCOUNTSVC_ADMIN_ROLE_<ALIAS>_NAME` 和 `_PERMISSIONS` 显式映射 JWT 角色；不增加 `ADMIN_AUTHORIZATION_MODE`，没有 builtin/custom 或默认映射回退。原内置角色仅作为部署示例。
- 主列表未设置、空或全空白且无孤立分项时生成空权限策略；管理认证启用时，有效管理员访问管理操作返回 403，关闭时仍为 503。缺字段、未知权限、非法通配符和孤立分项在监听前拒绝。
- 新增可选的管理权限验证接口，保留已发布 `AdminVerifier.RequireRole` 及角色常量的嵌入宿主兼容；accountsvc 始终使用新接口，权限拒绝不回退旧角色授权。
- 整理管理员认证授权、配置解析及其直接复用公共代码中的同语义重复字面量与重复逻辑。权限、协议选项、错误分类、环境变量构造和有意义的限制值集中到所属模块；不同语义不因字面值相同而合并。
- 更新配置示例、迁移说明、OpenAPI 授权元数据、协议检查和真实服务/SDK 测试夹具；保留 HTTP 路由、DTO、错误码、管理员 scope 不参与授权等契约。

## Capabilities

### New Capabilities

- 无；复用现有管理员认证与嵌入宿主能力边界。

### Modified Capabilities

- `accountsvc-admin-authentication`：显式角色权限映射、权限目录、资源通配符、逐操作授权、空策略及拒绝审计。
- `accountsvc-runtime`：管理员分项环境变量校验、启动快照、migrate 隔离、单文件 Compose 注入与部署迁移。
- `embedded-auth-package`：类型化管理权限与可选验证扩展；保持既有 Go 宿主和旧角色接口兼容，防止新旧授权叠加。

## Impact

- 主要实现区域：`httpapi/admin`、`accountkit.go`、`internal/accountsvc/adminauth`、`internal/accountsvc/config*.go`、`internal/accountsvc/run.go`，以及直接共享的 `oidcjwt` / `httpcredentials` / 配置帮助函数。
- 配套更新：`deploy/accountsvc`、`README.md`、`docs/accountsvc.md`、`docs/go-integration.md`、`docs/compatibility.md`、`docs/openapi*`、`scripts/check-openapi.cjs`、`sdks/http-coverage.json`（核对并在登记受影响时同步）、服务与 SDK 装配夹具、相关测试及适用的 AGENTS.md 授权契约表述。
- 旧 accountsvc 部署必须补充角色映射才能恢复管理访问；发布版本和发布动作另行决定。旧 Go 嵌入宿主不强制迁移，新增接口采用加法扩展；SDK 不新增角色判定、scope 获取或自动刷新职责。
- 不新增数据库迁移、管理员账号系统、动态策略存储、热更新、外部授权引擎、对象级授权、服务 SDK、管理员 scope 模式或服务权限通配符；不扩展为全仓库机械常量替换。
- 本次 ff 仅准备工件；实施验证结果统一记录在本变更 `verification.md`，不将工件齐备视为实现或运行测试通过。
