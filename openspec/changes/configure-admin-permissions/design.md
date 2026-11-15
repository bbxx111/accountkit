# Design

## Context

动机与范围见 [proposal.md](proposal.md)。本次为工件准备，以下内容是待实施设计，不是当前已支持功能。

当前 `httpapi/admin/handler.go` 的 11 条路由只调用 `RequireRole`，`internal/accountsvc/adminauth/claims.go` 将 super-admin 隐式补成 operator。`accountkit.AdminVerifier` 是已发布接口别名，`Deps.AdminVerifier` / `AdminPrincipal` 成对校验；直接替换接口会破坏外部 Go 宿主。`adminauth` 已有独立 issuer/audience/profile、可信上下文与拒绝审计；本次复用这些边界。

`config_service_clients.go` 已有别名列表、完整预期变量名集合和监听前校验，可参考其结构，但其权限去重、秘密、scope 和服务目录语义不得被管理员重构改变。单文件 Compose 已通过 env_file 注入动态变量。OpenAPI 目前使用 `x-required-role`，服务与 SDK 夹具依赖内置角色，均需要同步。

## Goals / Non-Goals

**Goals:**

- 外部提供方负责角色签发，部署策略负责角色到操作权限的映射，路由只声明具体管理权限。
- 明确空集合、通配符、升级扩权和配置快照的行为；保留 JWT、错误分类、审计与领域事务约束。
- accountsvc 不保留内置授权回退；已发布 Go 宿主通过加法接口升级。
- 同语义契约值和重复逻辑在所属模块统一维护，并以独立的协议断言验证稳定值。

**Non-Goals:**

- 不做角色管理页面、对象/组织范围授权、角色继承图、动态策略数据库、远程权限查询、热更新或管理员 scope 模式。
- 不改变服务目录或服务 scope 匹配，不新增服务客户端 SDK；不更改 SQL、迁移、令牌格式、管理路径和 DTO。
- 不借本次重构调整既有超时/大小限制或全仓库提取常量。

## Decisions

### 1. 授权模型与选项

采用轻量的显式角色映射。Casbin 可支持复杂策略，但当前只需有限权限并集；OpenFGA 的对象关系与远程一致性超出本次需求。只借鉴其角色与权限分离，不新增依赖。路由要求不可由部署配置改写；配置只决定哪些角色获得已定义权限。

用户已确认：`.` 分隔、不加 account 命名空间、支持资源组通配符且接受未来同组扩权、环境变量别名分项、无 builtin/custom 开关、原角色仅作示例。以下嵌入兼容方式及角色名的256字节限制为依据现有公开契约和配置边界作出的设计选择。

### 2. 10 项权限与 11 个操作

在 `httpapi/admin` 定义 `Permission` 字符串类型及 `PermissionUsersRead` 等常量，提供返回独立副本的 `Permissions()` 目录，根包通过类型/接口别名便于宿主使用。权限是协议能力标识，不是数据库枚举，不使用 enum 生命周期类型或新增存储。

| 管理相对路由 | 具体权限 |
|---|---|
| GET /users | users.read |
| GET /users/{user} | users.read |
| POST /users/{user}:freeze | users.freeze |
| POST /users/{user}:unfreeze | users.unfreeze |
| DELETE /users/{user} | users.delete |
| POST /users/{user}:undelete | users.undelete |
| GET /users/{user}/identities/{identity}:reveal | identities.reveal |
| GET /users/{user}/sessions | sessions.read |
| DELETE /users/{user}/sessions/{session} | sessions.revoke |
| POST /users/{user}/sessions:revokeAll | sessions.revoke_all |
| GET /users/{user}/auditEvents | audit_events.read |

资源组是权限目录中的组，不根据 URL 前缀推断。新增权限必须更新目录、路由登记、OpenAPI 元数据和行为测试。目录仅管理面使用，`identities.*` 不包含服务 `identities.resolve`。

### 3. Go 宿主兼容与拒绝优先

在 `httpapi/admin` 新增可选接口，根门面提供 `AdminPermissionVerifier` / `AdminPermission` 别名：

```go
type PermissionVerifier interface {
    RequirePermission(Permission) func(http.Handler) http.Handler
}
```

不改变 `Verifier.RequireRole(string)`、既有 Deps 字段类型和成对注入校验。具备新能力的宿主在原验证器上额外实现该接口；路由构造时一次判定，支持则全部路由使用 `RequirePermission`，不支持才使用原角色要求。该选择不取决于策略是否为空、请求角色、错误或授权结果。新路径绝不在拒绝后回退旧接口，也不并行运行两条授权链。

accountsvc 的 `adminauth.Verifier` 必须实现两接口，运行时始终被新路径识别；保留 `RequireRole` 仅为满足已有接口形状，按显式 JWT 角色精确判断，不做 super-admin 继承，不参与 accountsvc 管理路由授权。`RoleOperator` / `RoleSuperAdmin` 公开常量及旧路由映射仅用于嵌入兼容，不是 accountsvc 默认策略。这是对先前讨论中“移除角色常量”的兼容性限定：删除 internal adminauth 中的特殊授权，保留正式公共符号。

权限目录和旧路由映射的覆盖性由11操作表验证。所有授权仍位于协议业务解析前。`RecordAdminForbidden` 宿主接入和内部 OnForbidden 保持一次拒绝一次调用；不能因新旧接口共存重复写审计。

### 4. 配置结构及规范化

以下变量统一带 `ACCOUNTSVC_` 前缀：

| 变量 | 默认与行为 |
|---|---|
| ADMIN_ENABLED | false；原关闭行为保持 |
| ADMIN_ISSUER / ADMIN_AUDIENCE | 启用时必填；原安全与隔离校验保持 |
| ADMIN_TOKEN_PROFILE | default；可选 rfc9068，原校验保持 |
| ADMIN_ROLES_CLAIM | /roles；角色字符串数组的 JSON Pointer |
| ADMIN_USERNAME_CLAIM | /preferred_username；仅展示与审计身份快照 |
| ADMIN_ROLE_BINDINGS | 别名 CSV；未设置、空、全空白均为空策略 |
| ADMIN_ROLE_<ALIAS>_NAME | 实际角色标量，按原字符串精确匹配 |
| ADMIN_ROLE_<ALIAS>_PERMISSIONS | 权限 CSV；具体权限与资源组通配符 |

别名 `[A-Z][A-Z0-9_]{0,31}`。真实角色名1–256字节、有效UTF-8、无首尾空白或控制字符，允许内部空格和逗号；不做大小写变换或把 NAME 当CSV。令牌角色仍沿用字符串数组解析，不因新增配置约束而拒绝其他合法但未匹配的角色。

主列表CSV裁剪元素空白，拒绝空项及重复别名；权限CSV裁剪空白、拒绝空项，允许重复与通配重叠并去重。两种CSV语义不同，不能直接复用服务客户端“所有重复项均拒绝”的函数而改变约定。只在策略可显式区分时共享底层分割校验逻辑。

按别名生成完整的 NAME/PERMISSIONS 预期变量集合，再扫描环境；不以最后一个下划线猜别名。`ADMIN_ROLE_BINDINGS` 本身须从动态前缀扫描中排除。未知分项或未声明别名的分项即使空值也拒绝。配置扫描/错误输出排序稳定，仅报变量名与安全分类。

不定义 ADMIN_AUTHORIZATION_MODE；serve 对该项非空白值明确报不支持，空白占位可忽略，不称其为已发布旧配置。ADMIN_ENABLED=false 时，非空主列表或任何角色分项拒绝；认证字段延续现有半配置校验。migrate 不进入管理员读取路径，包括此不支持项检查。

### 5. 策略编译、通配符与请求判定

配置先转换为显式角色绑定结构，再由 adminauth 的无 I/O 构造边界独立校验、深拷贝并编译；直接 Go 构造不能绕过校验。空输入编译成非 nil 空策略，绝不生成 operator 或 super-admin 默认项。策略不存库，不修改传入 map/slice，不返回内部可写映射。

只允许精确目录项或 `<已知资源组>.*`。用目录展开通配符，而非对请求做字符串前缀匹配；拒绝 `*`、`*.read`、`users.fr*`、`users.**`、排除式与未知组。无需正则执行器、目录版本锁或通配符开关。

每次 JWT 验证通过后，从可信角色集合取得配置权限并集，放入验证器绑定的请求上下文；RequirePermission 检查具体权限。未知/未匹配角色为无权限，认证成功但权限不足403；失去可信上下文或来自不同验证器上下文保持401。JWT profile、签名及claims失败仍401；密钥依赖失败仍503。scope无论缺失或取何字符串均不参与管理授权，不套用服务scope解析。

新二进制启动按新目录展开 `users.*`，自动获得该组新增能力；旧进程策略不热更新。枚举具体权限则不扩大。通配符不是跨组或跨身份面的授权，也不绕过领域状态、审计和资源格式规则。

### 6. 重复字面量与逻辑整理

范围：`httpapi/admin`、`internal/accountsvc/adminauth`、相关配置解析/装配，及被这些路径直接复用的 `oidcjwt`、`httpcredentials`、环境读取和错误构造。修改共享代码必须覆盖其服务调用方回归，不借机改变服务协议。

| 类别 | 处理原则 |
|---|---|
| 权限标识与资源组 | 类型化常量与单一目录；禁止路由、解析器各自维护不同白名单 |
| profile、scheme、JWT字段、错误reason | 多处同语义引用时在所属包统一，优先使用已有标准库或项目定义 |
| 环境前缀、字段后缀、完整名称构造 | 复用 envPrefix 和有明确归属的构造/校验帮助函数 |
| 时间、长度、数量限制 | 同一契约值提取具名常量，保持现有数值；语义不同的相同值不合并 |
| 重复解析、错误响应、拒绝审计 | 提取小范围函数，不以单个字符串常量掩盖流程重复 |
| 角色业务名 | 新授权路径从配置读取；只在文档/夹具和公开旧接口兼容部分保留 |
| 测试中的协议预期 | 保留明确字面值或独立表，验证改错常量不能让全部测试同步误通过 |

不建立全仓库 constants 包，不机械提取只用一次的普通文本或 struct tag，不为了减少字符串数量制造循环依赖或无意义透传。整理清单与验证证据写入 verification.md。

### 7. 配套文档与夹具

OpenAPI 的11个管理操作增加 `x-required-permission`；旧 `x-required-role` 改为明确的 `x-legacy-required-role` 兼容元数据，更新描述与检查器，避免同时暗示 accountsvc 必须具备固定角色。SDK 方法、DTO 与请求格式不变；核对覆盖登记及协议测试，更新 accountsvc 真实服务夹具以显式注入绑定，不能通过修改 SDK token provider 掩盖服务配置缺失。

单文件 Compose 延用 env_file，不在 environment 中给映射设置默认值或枚举固定别名。示例为：

```dotenv
ACCOUNTSVC_ADMIN_ROLE_BINDINGS=OPERATOR,SUPER_ADMIN
ACCOUNTSVC_ADMIN_ROLE_OPERATOR_NAME=operator
ACCOUNTSVC_ADMIN_ROLE_OPERATOR_PERMISSIONS=users.read,users.freeze,users.unfreeze,sessions.*,audit_events.read
ACCOUNTSVC_ADMIN_ROLE_SUPER_ADMIN_NAME=super-admin
ACCOUNTSVC_ADMIN_ROLE_SUPER_ADMIN_PERMISSIONS=users.*,sessions.*,identities.*,audit_events.*
```

同时提供客服只读、审计及显式枚举示例；说明 operator 示例的 sessions.* 也会扩权。原配置未迁移时不会静默授权，已认证请求403；管理员禁用503与依赖503保持区分。

## Risks / Trade-offs

- [升级后管理面拒绝旧角色] → BREAKING 说明、显式迁移示例和真实进程验收；不能设置内置回退兜底。
- [通配符新版本扩大权限] → 已确认语义；新增目录项必须在发布说明登记，受限角色可改用具体枚举。
- [旧接口兼容形成旁路] → 构造时选择新能力，新拒绝不回退；加入“旧允许、新拒绝”及空策略测试。
- [同名身份资源组误授权服务] → 两个独立目录与验证器，回归 identities.* 不能匹配 identities.resolve。
- [共享重构改变服务认证] → 保持服务scope、主体和权限语义，以现有服务协议测试复核；不扩大重构范围。
- [多副本或旧JWT延迟撤权] → 部署快照只保证新实例策略；排空旧副本，JWT角色撤销不宣称即时生效。
- [审计或no-store回归] → 新接口不改变现有包装顺序，覆盖拒绝一次审计、失败响应no-store和业务不执行断言。

## Migration Plan

1. 实施阶段先增加权限目录与可选接口，并用旧宿主编译/路由测试确认兼容；accountsvc始终装配新能力。
2. 为每个已有管理部署显式列出角色和权限。需要保持当前能力可用上方示例；需要跨版本固定权限时使用完整枚举。没有“自动复制原角色”的运行逻辑。
3. 在测试环境核验11个管理操作的允许/拒绝、缺失映射、JWT profile、管理员/服务凭据隔离，以及嵌入宿主和真实SDK请求。
4. 新二进制与新配置一起滚动发布，全部副本重启/排空后核对有效权限；配置变更不修改数据库或Redis。
5. 若回退旧二进制，恢复匹配的旧角色签发及配置，并确认其固定授权符合当前授权意图。旧版本的 super-admin/operator 可能获得已被新策略移除的权限，不能宣称安全等价回退。
6. ff仅准备工件，实施时按AGENTS.md在可独立验证的步骤完成后进行本地提交。推送、合并、归档、版本号、tag、Release与产品部署分别按授权执行；公开Go契约采用加法兼容，服务配置破坏性变更须明确发布说明。

## Verification Strategy

- 配置矩阵：未设置/空/全空白列表、空元素、重复别名/角色名、未知分项、含下划线别名、空值孤立分项、非法/重叠通配符、NAME精确匹配、禁用管理残留、migrate忽略以及错误不回显合成秘密。
- 权限行为：11路由独立允许/拒绝、权限并集、无内置继承、scope无效用、目录副本与配置深拷贝、未来同组权限扩展。未来操作测试只使用测试目录，不向生产目录添加假权限。
- 兼容行为：旧宿主与新宿主、仅新拒绝不回退、两种profile、身份面隔离、服务scope和未知权限规则不变、拒绝一次审计、no-store与领域拒绝保持。
- 运行门禁：相关Go测试与vet、GOWORK=off构建、真实PostgreSQL/Redis的服务集成及race；OpenAPI/SDK覆盖检查、管理员TS单元和真实服务协议测试，SDK与Go门禁分别报告。共享改动需要的完整验证依既有verify入口执行，缺依赖明确未验证。
- ff阶段只验证工件、相对链接、规格结构和差异；结果写入 [verification.md](verification.md)，不勾选实现任务。
