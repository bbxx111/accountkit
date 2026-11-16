# Tasks

实施依据 [design.md](design.md) 与三份增量规格。ff 仅生成工件，下列任务全部待实施；apply 阶段按仓库约定形成引用本变更的详细计划，采用 TDD、分步审查和证据核验。只有实际完成且验证后才勾选。记录统一写入 [verification.md](verification.md)。

## 1. 管理权限目录与嵌入兼容

- [x] 1.1 在 `httpapi/admin` 增加10项类型化权限常量、独立副本目录及可选权限验证接口，根包增加对应类型/接口别名；先编写稳定字符串与目录不可被外部修改的行为测试，运行 `GOWORK=off go test -count=1 ./httpapi/admin` 验证。
- [x] 1.2 将11条路由登记为具体权限，新能力存在时只调用新接口，否则保留旧路由角色要求；先写逐路由允许/拒绝、旧允许新拒绝不回退、业务未调用、reveal失败no-store测试，运行管理适配器测试验证。
- [x] 1.3 保留原 AdminVerifier/Principal 成对注入与公开角色符号，补根门面及直接适配器兼容测试；用仓库外或被忽略目录内的独立Go module固定本次Git快照、GOWORK=off且无replace分别编译旧/新宿主，记录来源SHA与结果，不创建发布tag。
- [x] 1.4 更新 `docs/go-integration.md`、README及适用AGENTS.md中的新权限路径与旧宿主兼容说明；核对示例符号可编译，并独立复核新旧授权只选择一次、无拒绝后回退。

## 2. accountsvc 显式策略与认证授权

- [x] 2.1 在 `internal/accountsvc/adminauth` 增加显式角色绑定校验和不可变编译策略，移除super-admin隐式继承与默认授权；先覆盖多角色并集、角色精确匹配、空策略、未知角色、非法角色名、直接Go构造校验及深拷贝，运行adminauth测试验证。
- [x] 2.2 实现精确权限与资源组通配符展开、权限重复/重叠去重；先测试全局/部分/跨组/未知通配符拒绝、users.*与子资源隔离、identities.*与服务目录隔离，以及用测试目录证明未来同组扩权和枚举不扩权，不向生产目录增加假权限。
- [x] 2.3 实现 RequirePermission 并在可信请求上下文中使用配置权限；保留原JWT/profile/issuer/audience验证及401/403/503分类，先测试scope不参与、空策略不回退、上下文隔离、拒绝审计恰好一次、权限检查先于业务解析，运行adminauth与管理路由测试。
- [x] 2.4 更新管理权限文档、错误分类与scope边界说明；复核新策略不绕过领域生命周期/匿名化回调/审计，运行相关既有管理测试并记录本组审查结论。

## 3. 环境变量、部署示例与迁移

- [x] 3.1 在accountsvc配置加载中实现ADMIN_ROLE_BINDINGS和每别名NAME/PERMISSIONS，按完整预期变量集合检查残留；先写未设置/空/全空白、重复别名/角色、含下划线别名、空列表元素、NAME内部逗号/空格、未知字段与空值孤立字段测试，运行配置测试验证，错误不得回显合成秘密。
- [x] 3.2 保持ADMIN_ENABLED及既有认证配置校验，拒绝禁用时残留和非空不支持的ADMIN_AUTHORIZATION_MODE，migrate不读取这些运行变量；补启停/迁移隔离及输入快照测试，运行 `go test -count=1 ./internal/accountsvc/...` 的适用单元用例验证。
- [x] 3.3 更新run装配，确保accountsvc验证器始终具有新权限能力；更新 `fixtures_integration_test.go` 与 `sdks/testsupport/fixture/accountsvc.go` 显式绑定配置，测试空绑定403、关闭503、凭据无效401和依赖503，不靠夹具补入隐式角色继承绕过授权。
- [x] 3.4 更新 `.env.example`、单文件Compose说明、`docs/accountsvc.md`和`docs/compatibility.md`，提供含资源通配符的operator/super-admin、只读与固定枚举示例及升级/回退说明；用配置测试解析示例，并核对 `docker compose --env-file deploy/accountsvc/.env -f deploy/accountsvc/compose.yaml config` 的动态别名透传（只使用任务专用合成配置，不读取产品秘密）。

## 4. 相关重复字面量与协议元数据

- [x] 4.1 在本次涉及的adminauth/admin/config代码及直接共享公共路径中整理同语义重复字面量：优先复用已有权限/profile/scheme/错误/环境变量/限制值定义；同步提取重复解析与错误构造，保留不同CSV去重规则，保持既有数值及服务协议，通过对应回归测试并在verification.md记录归属与处理清单。
- [x] 4.2 将管理OpenAPI的11个操作补充x-required-permission，将旧角色说明明确为x-legacy-required-role，并更新检查器与协议测试；运行 `node scripts/check-openapi.cjs`、`node scripts/check-sdk-coverage.mjs`，核对 `sdks/http-coverage.json` 并同步受影响登记，保持方法/路径/DTO与SDK职责不变。
- [x] 4.3 对共享oidcjwt/httpcredentials或服务配置帮助函数的改动运行服务认证、内省、清理feed及身份解析回归；确认服务权限仍不支持通配符、Bearer仍执行注册权限与scope交集、主体绑定和错误顺序不变，复核测试保留独立协议字面值断言。

## 5. 综合验收与记录

- [x] 5.1 在GOWORK=off下执行 `go build ./...`、`go vet ./...`、`go test -count=1 ./...`，并对管理/共享认证代码执行race；Windows缺CGO时使用既有Linux验证镜像，数据库skip和缺工具链分别记录，不能作为通过。
- [x] 5.2 使用任务专用PostgreSQL/Redis/IdP夹具执行 `bash scripts/verify-accountsvc.sh`，验证真实进程的11操作授权、空策略、配置重启撤权和认证隔离；按既有要求更新必需测试登记，不删除/放宽门禁。涉及发布或需全量验证时执行 `bash scripts/verify.sh`，恢复源/目标须是不同的一次性空库。
- [x] 5.3 按SDK工具版本与现有验证入口执行管理员TS typecheck/单元和真实accountsvc协议测试，以及受影响SDK夹具的Go测试；消费者SDK与平台构建按实际影响验证并分别报告，不能把Go烟测或历史CI当成SDK通过。
- [x] 5.4 完成独立实现审查与问题修复，运行 `openspec validate --all --strict`、文档链接检查和 `git diff --check`；在verification.md列出本次实际命令、审查结论、未验证项及重复字面量处理清单，逐项核对工件后才确认实施完成。

## Workflow follow-up

- 归档、提交、推送、PR合并、tag、Release与产品部署分别按后续明确授权执行；任务全勾选不表示已归档或已发布。
