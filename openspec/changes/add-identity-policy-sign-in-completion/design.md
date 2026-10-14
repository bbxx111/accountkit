## Context

动机见 [proposal.md](proposal.md)。当前实现使用 users HTTP 资源、固定 PHONE/EMAIL 锚点及登录即注册；自动关联已有账号尚未实现。本次按已确认的简化方案收敛：Required 只包含 PHONE/EMAIL，补全只使用验证码，独立发码、统一 accounts 路径，票据字段命名为 sign_in_completion_token。

本工件描述拟议实现；实际产品代码和主规格本轮均未修改。验收以本 change 的八个 delta spec 为准。

## Goals / Non-Goals

**Goals:**

- 一份不可变身份策略用于库、领域直接调用、两套 HTTP 面及 accountsvc。
- 需要补全时不提前创建用户，验证码通过后自动关联已有账号或注册。
- 两个新增业务端点即完成补全，不引入通用第三方证明框架。
- 显式迁移 accounts 资源名和登录结果，保持令牌、会话、限流及匿名化安全边界。

**Non-Goals:**

- 已有 A 与 B 合并、身份迁移、业务数据合并或 MERGED 状态。
- 将第三方 Kind 设为 Required、补充另一个第三方身份、延后已满足条件的注册、新的第三方重新认证或恢复流程。
- 取消现有手机/邮箱或管理员注销恢复；该生命周期变更不属于此次简化。
- 新提供方、前端、短信服务商、配置热加载或同步强依赖审计。

## Decisions

### 1. 两个平铺配置及边界

Go 字段为 `[]enum.IdentityKind`。nil 表示未设置，非 nil 空切片表示显式空；环境变量沿宿主前缀读取 ENABLED_IDENTITY_KINDS / REQUIRED_IDENTITY_KINDS。非空列表为逗号分隔的 UPPER_SNAKE_CASE，`[]` 表示空列表，已设置但空白为格式错误。文档 YAML 仅作语义示例，不新增加载器。

| 配置 | 未配置 | 显式配置 |
|---|---|---|
| Enabled | 原选择：PHONE、EMAIL 和已配置应用的 IdP | 只能包含已实现的 PHONE/EMAIL/WECHAT/APPLE，非空 |
| Required | PHONE、EMAIL 二选一 | 仅 PHONE、EMAIL 的子集；空列表表示不限定类型 |

Required 必须是最终 Enabled 的子集；不能配置 WECHAT/APPLE，即使它们已启用。未知、重复、UNSPECIFIED、显式空 Enabled 均拒绝。显式 Enabled=[EMAIL] 配合缺省 Required 会失败，须显式 Required=[EMAIL]，不自动放宽默认要求。

Config.Validate 只做静态校验；New 无 I/O 检查显式启用类型的发送器/验证器。禁用发送器可缺省；显式启用 PHONE/EMAIL 不能使用 Disabled。未设置 Enabled 的旧宿主保留原发送器必填与 Disabled 装配兼容。配置复制为不可变快照，依赖暂时不可用不改变完整性条件。

令 E 为 Enabled，R 为 Required，U 为已验证且未软删除的身份集合：

`complete = (U ∩ E 非空) AND (R 为空 OR U ∩ R 非空)`。

Required 决定账号完整性，不决定每次必须用哪个 Kind 登录，也不是 MFA 策略。普通绑定允许 E；补全界面优先提供 R 中的手机/邮箱。非必需但已启用的手机/邮箱仍可用于识别已有账号，不能因为配置收紧就丢掉识别旧账号的途径。IsChannel 仍只代表 PHONE/EMAIL 验证码渠道，不扩大为第三方。

### 2. accounts 是统一 HTTP 账号资源

C 端及管理端账号路径整体从 /users 改为 /accounts，包括自定义登录动作、me、identities、sessions 和管理端账号操作。响应 name 同步为 accounts/{user_id}、accounts/{user_id}/identities/{identity_id}、accounts/{user_id}/sessions/{session_id}。

两套 handler 继续独立，相对路由由宿主分别挂载，例如 /v1/accounts 与 /admin/v1/accounts。/token、/revoke、/introspect、探针及非账号资源不因本次改名改变。

这是一项明确破坏性 HTTP 升级：新版只提供 accounts 规范路径，旧 users 路径返回原格式 404，不做隐式重定向或两套资源名混用。客户端、网关清单、管理调用和响应解析同步升级。若部署者需要过渡，应在宿主 API 版本边界独立安排，不能在库内写死产品前缀。

该选择只改变 HTTP 资源词汇：user_id、u_…、JWT sub/sid/iss/aud、scope=user/user:bind/user:undelete、Go user 包及 Auth.Users() 门面、数据库 user_account 表均不随之改名。

备选方案是保留 users 或只改认证动作；本次按用户确定采用 accounts，因此资源路径和 name 一起迁移，避免同一资源两套语义。

### 3. 初始登录与自动分流

先检查 Enabled，再验证凭证、查身份归属。已有账号先检查生命周期，ACTIVE 按完整性返回 user 或 user:bind。普通手机/邮箱冷静期登录与现有恢复契约保持；普通 IdP 登录仍拒绝 PENDING_DELETION。

未知身份已经满足要求时直接注册；不满足时返回登录补全票据，暂不创建 user_account、identity 或 session。接口不提供强制延后注册的参数。Required=[] 的未知第三方身份会直接注册；此时服务端不能猜测另一个邮箱账号的归属。已有用户想添加第三方登录方式，先登录原账号，再从账号设置绑定。

补充手机/邮箱证明后：
- 目标属于活动账号 B：原子关联尚未占用的暂存身份到 B，沿用 B 的 user_id；按关联后的身份集合返回 user 或 user:bind。
- 目标无归属：满足 R 后原子创建账号、身份和会话；仍不足时保持同一待完成流程，不续期。
- 目标账号不可用或身份分属两个已有账号：拒绝，不新建账号绕过状态，也不做账号合并。

接入页面只有“验证并继续”即可，但要说明关联本次身份及未注册时自动注册的效果。第三方 hint_email 不算 EMAIL identity，也不能作为自动关联依据。

### 4. 精确端点与参数

本次新增业务端点仅为：

```http
POST /accounts:sendSignInCompletionCode
POST /accounts:completeSignIn
```

| 操作 | 参数 | 结果 |
|---|---|---|
| POST /accounts:sendSignInCode | channel、target | 普通 SIGN_IN 轮次 |
| POST /accounts:signInWithCode | phone 或 email 凭证 oneof | token 或 pending |
| POST /accounts:signInWithIdp | wechat 或 apple 凭证 oneof | token 或 pending |
| POST /accounts:sendSignInCompletionCode | sign_in_completion_token、channel、target | SIGN_IN_COMPLETE 轮次 |
| POST /accounts:completeSignIn | sign_in_completion_token，加 phone 或 email 凭证 oneof | token 或继续 pending |

PHONE/EMAIL 凭证保持 `{target, code_id, code}`。新增发码 channel 只接受已启用的 PHONE/EMAIL；completeSignIn 恰好接受一个手机或邮箱凭证，拒绝空凭证或第三方成员。普通 sendSignInCode 不接收补全票据或公开 purpose，不能通过可选参数切换成补全发码。用途由端点在服务端固定确定。

登录成功保留现有顶层 RFC 6749 token 字段、user_id、scope、is_new_user。pending 只有 next_action=COMPLETE_IDENTITY、sign_in_completion_token、expire_time、enabled_identity_kinds、required_identity_kinds，不返回 user_id、scope 或伪 token。Go DTO 字段为 SignInCompletionToken；只支持这一票据字段名，不提供通用继续令牌字段的别名。两类响应均 HTTP 200、no-store。

```json
{
  "sign_in_completion_token": "opaque-token",
  "channel": "EMAIL",
  "target": "user@example.test"
}
```

```json
{
  "sign_in_completion_token": "opaque-token",
  "email": {
    "target": "user@example.test",
    "code_id": "0123456789abcdef0123456789abcdef",
    "code": "123456"
  }
}
```

Go SignInResult 保持 Tokens/Pending 显式联合，恰有一个成员。既有 SignIn 方法返回签名和 handler Service 随协议升级；新增补全接口使用可选能力，缺能力返回 503，不伪装完成。

初始登录继续要求 X-Device-Id，X-Device-Name 可选；流程绑定首次设备元信息，后续请求不能覆盖设备，若再次提供须一致。设备 ID 不是秘密，授权依赖随机票据及本流程验证码。

错误约定：显式 Kind 策略拒绝为 400 FAILED_PRECONDITION / IDENTITY_KIND_NOT_ENABLED；原 Disabled 发码仍为 CHANNEL_NOT_ENABLED。补全票据缺失、无效或过期为 400 SIGN_IN_COMPLETION_INVALID；普通绑定占用维持 409 IDENTITY_ALREADY_BOUND；无效 Bearer 为 401；依赖故障 503；500 不泄露内部错误。已完成但结果不可重放为 400 SIGN_IN_ALREADY_COMPLETED，不引入第三种模糊成功体。/token、/revoke 的 OAuth 格式不变。

### 5. 补全票据与持久化

identity_flow 仅用于本次登录补全，不扩展为第三方挑战、重新认证或恢复框架。其 f_ TSID 是内部流程标识；sign_in_completion_token 独立使用至少 256 bit 随机值，数据库仅存 SHA-256 查找摘要。既有 u_/i_/s_/e_ 格式不变。

保存流程状态、首次设备信息、策略指纹、时间、已验证初始身份、追加的手机/邮箱证明、最终账号/会话引用、请求指纹和完成结果。完整目标、provider subject/openids/hint、token pair 使用现有版本化加密设施及流程/实例密文上下文保护。原始验证码、IdP code/id_token、外部 token 不保存。

PENDING 可以持有已验证身份并等待验证码；VERIFIED 表示证明已持久化且可尝试最终事务；COMPLETED 记录成功结果，EXPIRED/REJECTED 终止。TTL 默认 5 分钟，取已知原证明更早期限，追加验证和重试不延长。规范化 E/R 与提供方配置标识组成策略指纹；策略变化使旧流程拒绝继续，不接受任意副本不同规则。

使用短期数据库记录，使身份、会话与“完成一次”处于同一提交点。新表采用递增迁移，无外键；SQL 经 sqlc 生成，不改冻结 SQL 或既有用户数据。

### 6. 独立验证码用途及事务

追加 SIGN_IN_COMPLETE 用途，Binding 增加流程维度；不能用 flow id 冒充 user_id。普通 SIGN_IN、BIND、REAUTH 与补全码不可互换，跨用途仍共享原冷却、目标/IP 额度和累计失败预算。发码前验证票据及 Kind，但不预先向客户端暴露目标归属。验证码轮次和完成操作的有效期均受流程原期限限制。

初始 IdP 校验沿用现有授权流程、应用允许列表、nonce 校验及重放保护，不新增准备端点。后续补全仅验证手机/邮箱码。Redis 消费不在数据库事务内，已经消费的证明不因 SQL 失败恢复；只有持久化成功的证明可继续使用。

最终流程：
1. 只读流程、证明及可能的账号归属，确定需要锁定的已有用户集合。
2. 按 user_id 排序锁已有用户，再锁流程，复核策略、期限、状态和请求。
3. 锁后再次查询身份归属；若发现未锁的已有用户则回滚重试或拒绝，不反向追加账号锁。完全未占用时才新建用户。
4. 复核账号状态、身份数量、E/R；必要的新用户、身份、会话及完成记录一起提交。同设备会话规则复用现有实现。
5. 提交后更新吊销缓存及异步审计，失败不能把已提交业务伪装成回滚。

统一顺序为已有 user（排序）→ flow（排序）→ identity/session。引用已有用户的证明落库同样遵守该顺序，防止匿名化后再写入该用户的证明快照。IdP 登录的“先查身份再锁用户”路径也必须补锁后归属复核。维护任务不能持有 flow 锁反向申请用户锁。

### 7. 幂等、隐私及清理

每个验证码轮次绑定自己的目标；未完成流程可追加手机/邮箱证明，不能替换已验证身份。最终请求记录秘密绑定的指纹；相同请求可在原票据期限内读取相同完成结果，不重复建号或建立会话。

结果读取先复核账号状态、会话期限/吊销及 refresh 是否已轮换。过期、已撤销或已轮换结果不可重新发放，返回 SIGN_IN_ALREADY_COMPLETED，客户端重新登录。票据本身已过期则返回 SIGN_IN_COMPLETION_INVALID。提交结果未知时通过相同完成请求确认状态，不另建账号；证明消费后持久化失败需在原额度下重新取证。

票据和凭证只放请求体，响应 no-store；日志、审计、URL 和代理追踪禁止完整票据、验证码、目标、摘要或 IdP 原始凭证。匿名用户请求的频率/并发由宿主入口限制，验证码额度仍由库执行。

到期立即不可用，并有界清理密文与结果。有效流程密文加入启动密钥版本检查；轮换保留旧 key 至活跃流程到期或清理。账号删除/匿名化撤销或清理其关联流程，匿名化同库回调继续使用同一事务。数据库备份恢复覆盖新表，同时明确不包含 Redis 证明状态；缺少所需证明不能降级放行。

流程审计使用内部 `f_` 资源 ID 作为 `audit.Event.FlowID`，经 `audit_event.flow_id` 可空列持久化；它不是随机票据或凭证摘要。独立请求通过同一 flow_id 关联，原 RequestID 仍逐请求保留。未识别的无效票据不产生伪造 flow_id。管理审计 DTO 仅新增可选 `flow_id`，不返回票据、证明、完整目标或摘要。

事件枚举在既有值之后追加 SIGN_IN_COMPLETION_STARTED、SIGN_IN_COMPLETION_PROOF_VERIFIED、SIGN_IN_COMPLETION_COMPLETED、SIGN_IN_COMPLETION_REJECTED。前三种成功事件分别在流程创建、证明持久化、最终完成的对应事务提交后发出；最终事务回滚不会产生 COMPLETED、SIGN_IN 或 IDENTITY_BOUND 成功事件，先前已独立提交的 proof 可以准确保留 PROOF_VERIFIED。补全发码、关联身份、会话替换及最终登录也携带同一 flow_id；错误验证码、票据无效、归属冲突及账号拒绝记录固定原因的 REJECTED。沿用有界异步审计，落库失败仅计数/告警，不改变业务提交。审计记录保留期独立于短期流程，因此 flow_id 不设外键，并以 `(flow_id, occur_time, id)` 支持运营关联检索。

删除关联 flow 前依据 identity_flow.user_id/proof_user_ids 清除已落库流程事件的 IP、设备和身份提示，覆盖最终用户关联事件仍在异步队列中的情况；匿名化还按已落库的 user_id→flow_id 关联清理更早无 user_id 的历史事件。晚到异步写入仍遵循既有审计契约，不宣称本次实现跨异步队列的零 PII 留存保证。

### 8. 普通绑定、近期认证与恢复保留原能力

普通绑定移至 /accounts/me/identities：未占用可绑定、本人已有幂等、他人占用 409。user 与 user:bind 均可添加 E 中的身份；补齐后通过最新 refresh 的新一轮轮换取得 user。解绑或换绑后仍需至少一个可用身份并满足 R。

现有手机/邮箱重新认证端点随资源前缀迁移，仍验证已绑定身份、当前会话和期限，只更新 auth_time、重签 access，不延长或返回 refresh。

只有第三方身份的账号需要近期认证时，重新发起正常第三方登录。它会新建会话并按同设备规则替换旧会话、返回新 token pair；不等同于保留当前会话的重新认证。客户端更新会话并确认仍为同一 user_id，不能在账号改变时自动重放原敏感操作。原提供方验证和防重放完整保留，不声称新发 token 等于强制用户重新输入密码或完成 MFA。

现有手机/邮箱冷静期认证、/accounts/me:undelete、管理端恢复及 user:undelete scope 保留；恢复后按当前 R 推导 user 或 user:bind。普通第三方登录仍拒绝冷静期账号；本次不增加第三方自助恢复。Required=[] 的纯第三方账号没有手机/邮箱自助恢复路径，这是部署需要了解的现有限制，不能宣称已经具备该能力。已有合法管理员代办恢复不受本次简化影响。

### 9. accountsvc 与升级边界

显式 EMAIL 禁用时不解析或要求 SMTP；启用时维持生产 SMTP/TLS/凭据要求。服务没有 SMS 服务商，显式 PHONE 启用失败。未设置新策略的部署继续原装配；新模板显式给出匹配的 E/R。

migrate 只检查静态策略语法/集合及迁移必要依赖，不构造认证 handler、发送器或 IdP 运行依赖；复用原锁与密钥核验，不通过假发送器或放宽 dirty 检查绕过约束。

### 10. 审计批处理连接租约修正

完整 Linux race 验证在登录补全的审计故障场景发现连接复用竞态：sqlc 批量 Exec 自动 Close 后，池已归还连接，随后显式 Close 再次清理 pgx 失败语句缓存，与业务查询并发访问同一连接。审计 Store 在单次批处理开始时显式 Acquire，绑定该连接执行生成查询，待 Exec 和用于读取最终提交错误的 Close 都完成后再 Release。继续保留批内全或无、主键冲突重试一次和异步审计失败不影响认证结果；读侧仍使用原池查询，不新增同步审计依赖。

## Risks / Trade-offs

- accounts 路径和 name 为破坏性升级 → 同步 C 端、管理端、SDK、文档和网关，验证旧路径 404，不能只改登录动作。
- 账号完整性改变会影响存量用户 → 新登录/刷新明确 user:bind，旧 access 沿原有效期/吊销处理，禁用唯一入口前安排迁移。
- Required=[] 时未知第三方身份直接注册 → 不能自动发现另一个既有账号；用户须先登录原账号再主动绑定，不提供已有账号合并。
- 重新登录会更换会话 → 客户端替换 token pair、核对账号，不当作原会话无感续认证。
- Redis 消费与 SQL 持久化存在间隙 → 明确需重新取证的失败边界，不恢复已消费验证码或放宽预算。
- 多流程争用相同身份 → 唯一约束、固定锁序、锁后复核和整事务回滚，不依赖预查询保证唯一性。
- 流程含短期敏感信息 → 加密、TTL、清理、匿名化覆盖和密钥/备份恢复测试。

## Migration Plan

1. 在下一协议升级中明确 accounts 资源迁移、Go SignInResult 及 pending 响应；成功 token 字段、JWT 和设备头保持。新客户端同时支持 token/pending，pending 只使用 sign_in_completion_token。
2. 新增递增迁移及 sqlc 结果，保持冻结基线与历史 tag 检查；分别更新两面路由、资源 name、客户端及网关清单，旧 users 路径不作为别名保留。
3. 全部认证副本升级后再启用新 E/R 与补全流量，不混跑旧登录即建号写路径；策略仅重启/重新装配生效，副本保持一致。
4. 部署收紧要求前核验存量账号的可用身份；保持完整轮换密钥、实例 schema/KeyPrefix。保留旧会话到期、刷新宽限和 Redis 故障契约。
5. 应用回退先停止新流程并排空最长 TTL/在途操作，保留新表与密钥；旧应用若不能等价执行身份策略，不可作为安全回退。客户端和网关必须同步回退，恢复先在独立空库验证。
6. 实际检查与未验证项写入 [verification.md](verification.md)；本次更新仅规划，不执行实现、发布或归档。

## References

- [Authing 账号绑定](https://docs.authing.cn/v2/guides/connections/account-binding.html)：不同登录方式关联已有账号的产品参照。
- [Auth0 账号关联](https://auth0.com/docs/manage-users/user-accounts/user-account-linking/link-user-accounts)：两侧控制权证明。
- [Google 手机号认证](https://docs.cloud.google.com/identity-platform/docs/reference/rest/v1/accounts/signInWithPhoneNumber)：验证后自动登录已有账号或注册的参照；不照搬其数据格式。
