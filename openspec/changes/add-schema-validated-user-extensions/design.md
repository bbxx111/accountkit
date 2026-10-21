# Design

## Context

动机见 [proposal.md](proposal.md)。当前 `user/service_me.go` 只承载显示名资料，`BatchPublicProfiles` 明确限定 ID、DisplayName 和 State。宿主通过 `Auth.Users()` 使用领域服务；`New` 不做 I/O，领域事务使用既有账号锁及锁后最新状态规则。资料新增不能扩大 HTTP 必需接口或改变认证热路径。

`user/client_info_json.go` 已有 JSON 精度、容量和深拷贝经验，但客户端描述与账号资料的来源、大小及生命周期不同，不能直接复用其整套规则。当前匿名化通过 `PurgeUser` 与宿主回调共享事务。

v0.4.0 已固定发布。本变更的 `0002_add_user_extensions` 尚未发布，本轮保留文件名并修订其定义；旧迁移预检拒绝多代同号结构，并会检查迁移源是否存在当前版本。新增合法 `0002` 后，必须额外防止历史撤回的 version 2 因源文件存在而被误接管。

## Goals / Non-Goals

**Goals:**

- 用标准 Schema 校验器提供明确类型和条件验证，以账号资料使用约束补足白名单及资源上限。
- 宿主授权与领域数据约束分工清晰，新增接口具有可测试的并发和生命周期行为。
- 公开 API 不依赖具体校验器类型，规划阶段只产生可评审工件。

**Non-Goals:**

- 任意 JSON 存储、动态 Schema 注册中心、在线热更新、自动旧资料转换。
- 内置 HTTP 字段授权、表单生成、媒体上传、通用按扩展属性筛选及新增 JSONB 索引。
- 自动把微信等提供方资料写入扩展，或将资料当作权限、会员权益和认证依据。
- 修改现有显示名语义、客户端描述契约及已发布迁移。

## Decisions

### 1. 使用 Draft 2020-12 校验器和固定属性目录

采用宿主提供 Schema 的方案；相较自行设计 Go 字段类型表，可以直接表达类型、枚举、边界、数组和字段条件。采用 `github.com/santhosh-tekuri/jsonschema/v6` 作为首选实现候选，实施第一步验证其精度、并发复用、格式断言、加载器及正则行为，固定通过验证的版本后再接入。若候选不能满足规格，应先更新设计，不能放宽验收。

首版为标准 Draft 2020-12 的受支持使用范围，不宣称支持任意 Schema：

- 必须声明 `$schema` 为 Draft 2020-12。根是对象并能验证 `{}`；启用所有约定 format 的断言，不做默认值填充和类型强制转换。
- 资料结构由主声明树的 `type`、`properties`、`items`、`$defs` 和本地 `$ref` 表达；每个结构节点解析引用后有单一明确类型。允许 object、array、string、boolean、integer、number，拒绝 null、类型联合及无约束结构节点；条件验证片段无需重复 type。
- 每个声明对象具有固定 `properties` 与 `additionalProperties: false`。属性名匹配 `^[a-z][a-z0-9]*(_[a-z0-9]+)*$`，长度不超过 64 字节；顶层保留 `id`、`name`、`state`、`display_name`、`create_time`、`update_time`、`delete_time`、`purge_time`、`profile_attributes`。
- 数组使用单一 `items` Schema，不支持 tuple/prefixItems、动态属性名、patternProperties、外部引用、递归引用或动态引用。局部 `$ref` 只引用本文件 `$defs` 中的定义，引用节点不混合其他结构声明。
- 结构树外，支持 `enum`、`const`、`required`、字符串长度和 pattern、数值界限及 multipleOf、数组数量及 uniqueItems、对象属性数量与 dependentRequired，以及 `allOf`、`anyOf`、`oneOf`、`not`、`if/then/else` 条件验证。
- 条件及组合分支只约束主声明树中已经存在的属性，不引入新属性或改变结构类型。例如在条件分支中可对已声明 `locale` 限定 enum，或要求同对象另一属性出现。初始化检查这一使用范围，具体条件真假仍交给标准校验器。
- 实施澄清：`$defs` 可复用仅包含约束的 Schema，作为主结构声明使用时仍必须解析为明确类型和闭合结构；作为验证片段使用时不要求重复 type。boolean Schema 只允许验证片段及其复用定义，不允许替代主结构声明；未使用的定义也检查关键字、引用安全及预算。
- 支持 title、description、default、examples、readOnly、writeOnly、deprecated、`$comment` 注解及根 `$id`；根 `$id` 只标识本文件，不触发外部加载，嵌套 `$id` 拒绝以避免改变引用作用域。未知关键字及自定义 vocabulary 拒绝，避免拼错关键字被忽略。
- 首版 format 白名单为 date、date-time、email、uri、uuid；未知 format 拒绝。uri 只表示语法，不证明资源可信或归属正确。正则使用 Go 标准 regexp 可表达的子集，在初始化时验证，明确不支持回溯和后向引用等扩展语法。

编译器内置 meta-schema，本地加载器拒绝所有外部资源。Schema 原始输入先按 64 KiB/32 层限制解析；无环展开后的属性声明最多 128 个，展开校验节点最多 1024 个，超限立即退出，不先展开再计数。`Config.Validate` 可独立纯计算校验；每个实际 Service 实例在构造期间编译一次并只读复用，显式的重复 Validate 调用不视为实例缓存。

属性声明预算按展开后的主结构树位置计数；条件片段对已声明属性的重复约束只消耗验证节点预算，不作为新属性声明。安全扫描不能导致同一实际展开被重复计费，未使用定义仍受关键字、引用及节点上限保护。

固定目录使补丁中出现的路径可独立检查，特别是删除不存在属性的补丁。没有必要根据整个 Schema 做可满足性推断，也不依赖最终结果中是否还存在该键。

### 2. 公开配置与 Go 调用形状

拟新增以下公开类型和方法，具体注释与错误哨兵在实现阶段按规格固定：

```go
// user 包；Config.ProfileAttributes 与 user.Deps.ProfileAttributes 使用可选指针。
type ProfileAttributesConfig struct {
    SchemaVersion string
    Schema        json.RawMessage
}

type ProfileAttributesView struct {
    Data              json.RawMessage
    SchemaVersion     *string
    SchemaFingerprint *string
    UpdateTime        *time.Time
}

type ProfileAttributesPatch struct {
    Document json.RawMessage
}

func (s *Service) GetProfileAttributes(ctx context.Context, userID string) (ProfileAttributesView, error)
func (s *Service) PatchProfileAttributes(ctx context.Context, userID string, patch ProfileAttributesPatch, meta Meta) (ProfileAttributesView, error)
```

Patch 仅含 Document；View 不含 Revision，不要求调用者先读取或提交并发版本。版本标识限定为 1–64 个 ASCII 字符 `[A-Za-z0-9._-]`。Schema 内容和请求/响应字节均复制；公开类型不出现校验器对象、SQL 行或可变的编译缓存。

宿主自行读取文件或嵌入 Schema 并传入配置。首版不增加 Schema 文件路径环境变量或 accountsvc 配置加载逻辑，也不自动将扩展加入现有 HTTP 响应。

API 是宿主信任边界内的能力，不接受客户端自报“我是管理员”。宿主调用前决定目标用户与可修改字段，删除和父对象整体替换也须纳入宿主权限检查。Schema 的 readOnly/writeOnly 仅保留注解语义；库只验证数据声明和账号状态，不在首版再建立一套角色权限系统。

扩展是普通 JSONB，不提供新的字段级加密。宿主不得把验证码、令牌、密钥或身份联系方式的明文副本放入扩展，手机号和邮箱继续使用原身份密文/摘要模型。宿主字段白名单需覆盖用途及隐私；校验通过不代表任意字符串值已通过隐私识别。

### 3. 存储放在已有账号行

修订未发布生产迁移 `0002_add_user_extensions.up.sql` / `.down.sql`，在 `user_account` 增加：

| 列 | 定义及语义 |
|---|---|
| `profile_attributes` | JSONB NOT NULL DEFAULT `'{}'::jsonb` |
| `profile_attributes_schema_version` | 可空 TEXT，首次有变化写入后记录 |
| `profile_attributes_schema_fingerprint` | 可空 TEXT，定义指纹；与 version 成对 |
| `profile_attributes_update_time` | 可空 TIMESTAMPTZ，首次实际变化前为 NULL |

采用明确命名的基础 CHECK：Schema 标识成对、版本及指纹格式；不把宿主 Schema、JSON 业务约束放进 SQL。约束名遵守 63 字节上限，例如 `user_account_profile_attributes_schema_pair`、`user_account_profile_attributes_schema_version_format`、`user_account_profile_attributes_schema_fingerprint_format`。不增加外键、索引、触发器或影子用户 ID。

结构预检同时核对本函数覆盖的账号时间列与扩展更新时间为有效精度 6，防止 `TIMESTAMPTZ(0)` 等同类型低精度列冒充完整结构并丢失微秒。默认 TIMESTAMPTZ 和显式 TIMESTAMPTZ(6) 均接受；漂移只无写拒绝，不自动 ALTER 或修复数据。

选择账号行是因为数据小、更新低频，状态和扩展可共享原子行锁。独立资料表可以减轻大文档更新的竞争，但会引入引用完整性和额外清理路径，当前没有必要。所有新查询写在 `user/query.sql` 并经 sqlc 生成，不通过通用动态 SQL 更新 JSON 路径。

普通内容变化同时更新账号原 `update_time` 和 `profile_attributes_update_time`。现有显示名更新不改变资料属性的独立更新时间；冻结、软删除与恢复不改变扩展元数据。数据库升级只增加空扩展初值，不改旧行原时间。

### 4. 精确 JSON 与 Merge Patch

原始 Schema、补丁和存储 JSON 必须经过拒绝重复键、尾随文档和非法 Unicode 的解析，不使用 JSON 解码默认替换错误字符的行为。数字以精确十进制表示保留，在 Schema 校验、合并、相等比较与 SQL 参数间不经过 float64。数值 `1`、`1.0` 和 `1e0` 按同一值比较。

通用 JSONB 可存储检查覆盖 NUL、无效代理项及数值范围。规范化数字时先限制展开长度，避免短指数输入导致大内存分配；16 KiB 的原始补丁与结果限额分别检查，不能靠移除空白绕过请求上限。最大深度 8、512 个值节点包括根和容器，键不另计节点。

真实 PostgreSQL 集成澄清：JSONB 输出会加入格式空格，不能把 16 KiB 的规范化资料限额直接当作数据库返回文本的原始字节限额。存储表示解码采用有界 64 KiB 输入，仍限定 8 层/512 值节点，并在规范化后强制 16 KiB；外部补丁继续执行原始 16 KiB 限制。公开返回资料为规范化表示。

补丁递归遵循 RFC 7396。顶层只接受对象；对象成员的 null 用于删除，但数组整体替换内容中的 null 仍按存储禁用规则拒绝。验证目录时包括 null 指向的属性、嵌套对象及替换数组内的对象成员。删除未知属性拒绝，删除不存在但已声明的属性无变化。对已有对象应用 `{}` 保留内容；对不存在的已声明对象应用 `{}` 会创建空对象，随后按其 Schema 检查 required。

替代方案：

- 整体替换：实现更小，但宿主必须回传全部资料，容易覆盖其他属性。
- JSON Patch：能表达显式 null 与数组逐项编辑，但操作路径和权限更复杂。当前资料不保存 null，数组整体替换足够，因此选 Merge Patch。
- PostgreSQL `jsonb ||`：不具有所需递归及删除语义，不用于实现本协议。

### 5. 事务、状态与错误优先级

新增写路径复用内部领域事务的 Read Committed 与既有锁序：

1. 检查功能配置、user ID 和原始补丁解析/容量；Patch 只包含 Document。
2. 在内部 Read Committed 领域事务中获取数据库账号行锁，并在锁内读取最新状态与完整资料；不存在返回既有 not-found，非 ACTIVE 返回状态错误。
3. 比较已存 Schema 版本及指纹；不匹配无写拒绝。
4. 检查补丁声明路径，对锁内最新资料的副本执行 RFC 7396 Merge Patch，完整 Schema 校验和存储边界校验后写入。
5. 内容无变化时返回当前视图，不初始化 Schema 标识、不推进时间、不发成功修改事件。
6. 实际变化时更新完整结果、Schema 标识、独立资料更新时间及账号更新时间；事务成功提交后返回视图并尽力记录事件。

并发控制仅依赖数据库账号行锁；没有 Redis 锁、revision、expected_revision 或版本冲突。不同且不重叠属性的并发请求均可提交并保留彼此结果；同一属性以后获得锁并执行的事务为准。数组整体替换、父对象 null 删除仍按 RFC 7396，后执行补丁可能替换数组或删除先写入的子树，不能承诺所有重叠路径均保留。所有请求须基于锁内最新资料校验完整结果。提交结果不确定时返回错误，宿主重新读取核对，不承诺确定未提交或盲目重放。

读取只允许 ACTIVE/FROZEN；PENDING_DELETION/DELETED 拒绝。读取验证通用存储边界和成对元数据，发现损坏报告内部不变量错误，不静默清空。历史资料不按当前 Schema 重新判合法；认证路径不新增扩展查询、解析或校验，不将扩展作为认证结果依据。

实施规划核对说明：现有账号查询使用 `SELECT *` / `RETURNING *`，新增列后 sqlc 行类型会包含扩展原始列。这不表示新增独立查询或公开序列化；原登录路径保持既有查询结构，普通 JWT/内省不增加数据库访问，明确 DTO 继续排除扩展。这里澄清原“认证热路径也不加载扩展”为“不新增扩展查询或校验”，避免承诺现有宽行查询无法满足的列级排除。

错误类别为：未配置、参数非法、Schema 校验失败、账号状态拒绝、Schema 标识不匹配，以及内部/依赖失败。Schema 校验失败可通过 `errors.Is(..., ErrInvalidArgument)` 和专用结构化错误区分；最多返回 16 条受控路径与关键字原因，不包装可能包含原始值的校验器错误。非法或未知键只报告最近合法父路径。

本次没有内置 HTTP 路由，因此不为错误提前新增 HTTP 映射或改变现有 FAILED_PRECONDITION 的 400 行为。

### 6. Schema 标识及升级边界

指纹为规范化 Schema JSON 的 SHA-256 小写十六进制。规范化去除非字符串空白、按对象键排序、保持数组顺序及精确数值；注解也纳入指纹。它是保守的定义身份，不证明语义等价，也不是机密保护。相同版本修改定义需要受控迁移，不能仅凭字段看似兼容就复用旧版本。

初始 `{}` 的资料无标识；首次有变化写入绑定当前定义。绑定后即使内容又变回 `{}`，仍保留标识，防止旧实例借空对象重新接管。

首版正常写 API 不跨 Schema 标识。宿主 Schema 升级流程为：协调所有写入者停写，备份，使用另行评审的专门数据迁移验证/转换完整资料、条件更新标识与独立更新时间，核验后部署一致配置再恢复写入。该流程不由账号库生产 SQL 迁移自动完成；本次不提供在线批量迁移接口，也不指导直接改版本绕过校验。

仅凭逐行指纹无法阻止错误配置实例首次初始化不同空账号。因此同一数据库实例使用一致配置是宿主部署契约；逐行标识只是拒绝已有资料被另一配置覆盖，不宣称全实例自动发现混跑。关闭扩展配置不删除既有资料，重新启用需要原定义或完成专项迁移。

### 7. 生命周期、导入与审计

`PurgeUser` 同事务将 profile_attributes 改为 `{}`、清除 Schema 版本及指纹。曾使用资料记录清理时间；从未使用的空资料保持未初始化元数据。已 DELETED 不再次执行清理；宿主回调失败恢复所有原字段。不依赖资料 Schema 配置或普通更新校验。

迁移与现有 `ImportAccounts` 均依赖新增列默认值。首版不向 ImportAccount 增加扩展字段；导入 ACTIVE 后，宿主可通过新增接口写入；两次调用不宣称同一批事务。DELETED 导入保持空资料且不能调用新增写接口。需要批量原子导入扩展属于以后单独设计的能力。

新增成功操作审计动作 `USER_PROFILE_ATTRIBUTES_UPDATED`，通过现有通用 Recorder 及配置的 source/namespace 投递。记录目标账号、存储 Schema 标识；不保存数据值、补丁、属性名清单或 Schema 全文。宿主 Go 操作使用明确的服务 actor `accountkit-host`（类型 SERVICE，namespace 为当前审计配置），不把目标账号冒充为操作人；request_id 取既有 Meta，首版不额外采集 IP/设备资料。若将来需要具体宿主操作者，另设计可信 actor 注入。

事件在提交后尽力投递，失败不影响修改成功；不创建事务 outbox。无变化及失败请求不发出成功事件。新增动作的枚举适配若需要，只追加不重编号；资料本身可含隐私，审计、日志、错误和公开查询都必须有泄露回归测试。

## Risks / Trade-offs

- Schema 使用范围小于完整标准 → 明确公开支持清单，启动时拒绝不支持的写法，禁止静默忽略；复杂扩展另提变更。
- Schema 可以声明隐私文本，但不能判断任意字符串中的隐私 → 宿主维护资料用途和字段权限；库限制暴露并整份匿名化，不把黑名单称作完整隐私检测。
- 小数据仍会竞争账号行锁 → 限制大小与复杂度，测试代表性和上限输入的校验时间及锁等待；目前不引入独立表。
- 同属性按账号行锁取得并执行的顺序覆盖；数组替换及父对象删除可能覆盖重叠子树 → 宿主授权需覆盖整个补丁影响范围，不能按属性自动合并所有竞争。
- 没有自动 Schema 迁移 → 旧资料可读但普通写入阻断，部署必须停写并执行单独验证的数据迁移。
- 新增 version 2 与历史撤回版本同号 → 预检同时核对基础结构和扩展结构，回归所有历史拒绝夹具。
- 升级后旧应用使用 `SELECT *` 或旧结构预检可能不兼容 → 部署默认不混跑，应用回退须独立测试，不以新增可空列推断兼容。
- 提交时断连可能造成结果不确定 → 返回错误并要求读取核对，不把技术错误描述为确定未落库。

## Migration Plan

1. 保持 v0.4.0 标签、当前 `0001` 字节及其固定校验不变；修订本变更未发布 `0002`、查询及生成结果。
2. 预检明确 v1/v2：完整 v0.4.0 v1 且无扩展残留才升级；v2 需基础结构和扩展列/默认值/约束完整；dirty 仍先拒绝。保留外层锁、引擎锁及连接释放行为。
3. 在隔离 PostgreSQL 17 验证空安装、非空 v0.4.0 升级、重复执行、同库多实例、并发迁移、失败/取消和各代历史结构无写拒绝。测试下一版本使用生产最新版本之后的测试源，不覆盖本次正式 `0002`。
4. 在扩展未启用状态验证升级应用及既有认证，再部署宿主 Schema 并验证新增 Go 接口。先停止旧应用写入，不能假定 v0.4.0 与新结构可同时运行。
5. 更新 sqlc、兼容/迁移说明、真实恢复夹具及必需测试清单；执行发布完整入口并将证据记录于 [verification.md](verification.md)。
6. 恢复在独立空库验证备份与旧应用兼容后进行。公共 Down 继续无 I/O 拒绝；`.down.sql` 不是授权生产删除扩展数据的操作入口。

## References

- [JSON Schema 对象及未知属性](https://json-schema.org/understanding-json-schema/reference/object)
- [JSON Schema 注解与 default/readOnly/writeOnly](https://json-schema.org/understanding-json-schema/reference/annotations)
- [JSON Schema 数值语义](https://json-schema.org/understanding-json-schema/reference/numeric)
- [JSON Merge Patch — RFC 7396](https://www.rfc-editor.org/info/rfc7396/)
- [Go 校验器候选文档](https://pkg.go.dev/github.com/santhosh-tekuri/jsonschema/v6@v6.0.3)

## 本轮修订裁定

公开配置及 Deps 字段统一为 `ProfileAttributes`，公开类型为 `ProfileAttributesConfig/View/Patch`，读取和更新为 `GetProfileAttributes/PatchProfileAttributes`。稳定错误为 `ErrProfileAttributesNotConfigured`、`ErrProfileAttributesSchemaMismatch`，不提供 Conflict。最终存储仅四列：`profile_attributes`、`profile_attributes_schema_version`、`profile_attributes_schema_fingerprint`、`profile_attributes_update_time`。用户资料属性只允许用户资料，不承载宿主业务状态、权益或权限。Schema 版本、指纹及独立更新时间继续保留。54afa4b 临时五列 version 2 与本轮四列结构不同，预检必须无写拒绝并保持 catalog、数据及 version/dirty；不自动转换、Force、清库或改版本。`0001` 和已发布历史字节不变。旧详细计划的受影响步骤由[本轮计划](../../../docs/superpowers/plans/2026-10-21-profile-attributes-row-lock.md)替代；旧验收记录不作为本轮证据。
