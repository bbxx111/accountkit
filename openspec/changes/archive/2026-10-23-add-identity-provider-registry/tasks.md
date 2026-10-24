# Tasks

## 1. 字符串身份类型与 0001

- [x] 1.1 将 `enum.IdentityKind` 改为字符串底型：内置常量、格式 `Valid()`、`IsBuiltin()`、显式集合的 `IsAnchor`/`IsChannel`、只校验格式的 `ParseIdentityKind`；更新 `enum` 包注释；`go test ./enum/` 覆盖格式边界、空值与内置判定
- [x] 1.2 修改当前 `0001_init.up.sql`：`identity.kind` 为 `VARCHAR(32) COLLATE "C" NOT NULL` 加 `identity_kind_format` CHECK，更新文件头与列注释；索引定义文本与 down 不变；`user/query.sql` 中锁键 `@kinds::text[]` 与管理筛选 `::text`；运行 `sqlc generate` 并用 `bash scripts/check-generated.sh` 确认无多余差异
- [x] 1.3 运行时预检要求 `identity.kind` 为 `VARCHAR(32)` 且 C 排序；新增迁移测试：新空库 version=1/dirty=false 且 kind 定义与 CHECK 符合；SMALLINT kind 的 v1（空/非空）无写拒绝、dirty 优先、重复拒绝不泄漏锁；非法格式写入被 CHECK 拒绝
- [x] 1.4 更新当前 `initialFrozen`，确认 v0.4.0/v0.5.0 已发布定义及冻结源哈希不变；`bash scripts/check-migrations.sh` 通过
- [x] 1.5 修复全部编译点（领域、HTTP、审计、管理筛选、补全载荷、观测、测试中的数字字面量与 `int16(...)`），`GOWORK=off go build ./... && go vet ./...` 通过；相关包单元测试通过
- [x] 1.6 AGENTS.md：枚举规则写明 IdentityKind 文本例外；迁移安全一节新增本次直接修改 0001 的限定授权记录；`docs/migrations.md` 当前初始基线同步说明；检查链接与路径

## 2. 注册表与统一 Provider 契约

- [x] 2.1 在 `user/idp` 新增 `Provider` 接口与 `ErrMalformedCredential`，移除 `WeChatVerifier`/`AppleVerifier`/`GoogleVerifier`；单测覆盖错误哨兵
- [x] 2.2 微信、Apple、Google 改为实现 `Provider`：各自严格解码凭证（未知字段、缺字段返回 `ErrMalformedCredential` 且不发网络请求、不登记 nonce）；Google 补 `PolicyFingerprint`；原有 idp 单测改写后全部通过并新增畸形凭证用例
- [x] 2.3 `user.Deps.Providers` 取代三个字段，`NewService` 构建不可变注册表并校验格式、非锚点、不重复、非 nil；`IdentityPolicy` 默认启用全部已登记类型，能力校验改为查注册表、拒绝未登记类型；单测覆盖重复、占用 PHONE/EMAIL、显式启用未登记类型
- [x] 2.4 `verifyIdp` 改为注册表分派，`requireIdentityEnabled` 的未配置分支改为“Enabled 未显式且未登记 → IDP_APP_NOT_ALLOWED”；`user.IdpCredential` 改为 `{Kind, Credential}`；`ErrMalformedCredential` 映射 400 `CREDENTIAL_INCOMPLETE` 且不写失败审计；未分类错误固定 500；服务层测试覆盖各分支
- [x] 2.5 资料声明与首次填充去掉 GOOGLE 条件（重命名首次填充函数）；测试确认微信/Apple 不写 profile，任意返回 name/picture 的提供方按 Google 规则写入
- [x] 2.6 补全配置指纹纳入全部已登记提供方（按类型排序）；测试确认更换 Google client ID 后旧 pending 流程被拒绝
- [x] 2.7 未登记历史类型：集成测试确认该类身份可列出、解绑、匿名化、管理查询并输出原类型名，但不能登录或新绑定
- [x] 2.8 `accountkit.go` 装配改为构建 Provider 列表；`examples/embedded` 与 `examples/` 下受影响代码编译通过

## 3. C 端统一凭证格式

- [x] 3.1 `httpapi/enduser/request.go`：凭证改为顶层 `{kind, credential}` 信封，删除 `phone`/`email`/`wechat`/`apple`/`google` 成员与 `CREDENTIAL_ONEOF`；按端点允许类别（锚点/第三方/两者）校验 kind，PHONE/EMAIL 严格解码 `{target, code_id, code}` 并保留原错误码，第三方 credential 为对象且 ≤16 KiB；HTTP 测试覆盖旧成员 400、缺 kind/credential 为 `CREDENTIAL_INCOMPLETE`、跨类别 `CREDENTIAL_KIND_NOT_ALLOWED`
- [x] 3.2 `signInWithCode`、`signInWithIdp`、`completeSignIn`、`reauthenticate`、绑定、换绑的处理器与 HTTP 测试改用新格式，验证码与三种内置登录端到端结果与原行为一致
- [x] 3.3 更新 `docs/openapi/enduser.json` 与 openapi 一致性测试、README 请求示例、`docs/compatibility.md` 写出 Go API 与请求格式迁移说明；验证文档中的示例 JSON 可被新解析器接受

## 4. 通用 OIDC 验证器

- [x] 4.1 抽出内部 `jwksCache`（懒加载、60 秒节流、缓存降级场景参数），Apple 与 Google 改用；现有 Apple/Google 缓存与降级测试不变通过
- [x] 4.2 实现 OIDC 校验核心并让 Google 作为预设复用：RS256、issuer 集合精确匹配、aud/azp、exp 与 30 秒偏差、原文 nonce 常量时间比较、按类型 scope 登记 nonce；新增 `idp.NewOIDC`；httptest JWKS 单测覆盖签发者、受众、算法、过期、nonce 重放、JWKS 不可用 503、缓存降级、email_verified 提示、name/picture
- [x] 4.3 `Config.OIDCProviders` 与校验（类型格式、非内置名、不重复、issuer/JWKS 绝对 http(s)、client ID 非空、NonceTTL 默认、`NameClaims`/`PictureClaims` 的 nil 默认/空切片关闭/元素非空不重复）；OIDC 校验器按映射顺序取顶层字符串声明；`ConfigFromEnv` 读取 `OIDC_PROVIDERS` 与 `OIDC_<KIND>_*`（含 `_NAME_CLAIMS`、`_PICTURE_CLAIMS` 未设置与空串的区别）；单测覆盖缺项报错指出变量名且不泄露值、映射回退、关闭回填、非字符串声明跳过
- [x] 4.4 观测新增 `MethodOIDC = "oidc"` 与 `oidc_cached_key_fallback`，metricdata 规范化接受新值；观测测试确认类型名不进入标签、审计记录具体类型名
- [x] 4.5 accountsvc：读取 `ACCOUNTKIT_OIDC_*`，显式 Enabled 含未配置第三方时指出 `ACCOUNTKIT_ENABLED_IDENTITY_KINDS`，仅 OIDC 且 Required 为空时无 SMTP 可启动，migrate 不读取；更新 `deploy/accountsvc/.env.example`、`docs/accountsvc.md`、README 配置表；accountsvc 配置测试通过
- [x] 4.6 数据库集成测试：OIDC 首次登录注册（Required 为空）、pending 补全、绑定、再次登录命中同一身份

## 5. 集成验证

- [x] 5.1 `GOWORK=off go build ./... && go vet ./... && go test -count=1 ./...`（含 `SERVER_TEST_DB_DSN`）通过；新增必需顶层测试同步 `scripts/required-tests.txt`
- [x] 5.2 `bash scripts/verify.sh` 完整验证（race 在 Linux 镜像中执行）与 `openspec validate --all --strict` 通过；结果、审查结论与未验证项写入本变更 `verification.md`

## Workflow follow-up

- 按项目流程合入 develop 后单独执行归档。
- 第二期（宿主自写 Provider、能力声明、`provider_meta` 扩展区）另起 OpenSpec 变更。
