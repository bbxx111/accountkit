# generalize-audit-events 实施前只读盘点

日期：2026-10-16。基线分支：`feat/generalize-audit-events`。已读取 AGENTS.md 与本 change 的 proposal、design、tasks、verification、四份 specs（及 .openspec.yaml）。本记录只提出实现与验收建议，未修改 Go、SQL、生成代码或规划工件，未执行迁移或测试。

## 评审分流：语义/兼容决策与实现问题

以下两项必须在详细计划评审中明确解决；在未获授权的新语义下不应擅自改产品实现：

1. **已验证管理员字段超限。** 当前合法issuer/sub可以超过512字节，username可以超过256rune，与新通用结构上限及“HTTP输入不静默收紧”同时满足不了。实现者不能自行截断稳定身份、哈希替换身份、修改认证结果、把已知管理员改成Anonymous/Unknown或默默整条丢弃。可供评审选择的方向包括：明确接受新的审计兼容边界并规定超限事件安全拒收及可观察性、调整通用字段限制、或另行设计保留完整已验证身份的受控映射。Name可以有独立处理，但issuer/sub稳定身份不能仅靠省略展示字段解决。历史迁移也需要对应的拒绝/适配策略。
2. **flow历史关联的持久性与幂等。** 要同时保证原事件引用不可变、flow物理过期后账号可查到早期事件、外部结果可复核，需要选择关联表示与授权验证契约。若只使用现有十列，应定义历史事件之间的可信flow闭包、边界及外部reader关联证据；若增设持久关系表/墓碑则超出现有“简单十列默认存储”实施细节，应在design明确范围与清理寿命。不可由Store悄悄追加稳定resources绕过去重，也不可用未知flow“一律拒绝结果”删减已确认历史覆盖。

其余例如账号→flow→audit统一锁序、整批锁集合排序、锁后陈旧快照重试、无效IP安全省略、从已取得identity行填target、固定DTO投影与安全错误分类，均可在现有已确认语义内作为实施方案解决；不必为每个锁实现选择单独向用户索取许可。device/request_id兼容边界应在计划中明确，其中正常登录64字节范围已有确定证据；自定义无上限request_id的新限制需与管理员限制一起记录公开迁移说明。

## 必须先落实的设计细节

### 1. 迟到写入不能直接增加 flow 锁而保留当前 scrub 顺序

当前领域锁序证据：

- `user/state.go:56` 明确用户行先于身份/会话行；`user/query.sql:275` 明确既有账号排序加锁后再锁 flow。
- `user/service_signin_completion.go:219` 的 `proofOwnerIDs` 排序并去重；`:233` 的 `lockProofOwners` 逐账号加锁；`:311`/`:315` 证明持久化新证明执行账号 → flow。
- `user/service_signin_completion.go:419`/`:423` 完成流程同样账号 → flow；`:435` 在新快照发现先前未锁账号时退出事务并重试，明确禁止持 flow 再补账号锁。
- `user/service_purge.go:81`/`:91`/`:94` 当前 purge 为账号 → **scrub audit** → 删除 flow；`user/service_lifecycle.go:54`/`:76`/`:79` 软删除也是账号 → **scrub audit** → 删除 flow。
- `user/query.sql:323` 的 `ScrubIdentityFlowAuditForUser` 更新审计行但只读取流程归属，不锁 flow；`:315` 的 `DeleteIdentityFlowsForUser` 才按 id 锁 flow。

因此新 writer 若按账号 → flow → audit 写入/幂等比较，而删除仍按账号 → audit → flow，会在不共享已锁账号的情况下引入环。例如 writer 在无账号旧快照上锁到 flow F，或只锁 flow 另一个证明账号 B；purge A 先锁被 F 关联的审计行 E，再等 F；writer 持 F 再访问同 ID 已存在 E 的去重比较/插入，等待 E。全量可信账号锁能阻止部分交错，但不能用这一点跳过未知 flow 与陈旧归属的证明。

建议将所有相关路径统一为：**事务开始前发现关联集合 → 账号按 id 排序加锁 → flow 按 id 排序加锁 → 锁后复核归属 → scrub/插入/去重**。当前 purge/softDelete 应显式锁住关联 flow 后再 scrub；不能简单把 flow 删除移到 scrub 之前，否则丢失 `identity_flow` 的归属信息。对新发现的未锁账号，释放事务、重新发现并重试，参照补全已有实现；不能在持 flow 时补锁。过期清理只锁 flow、从不再锁账号，仍可保持无反向边。

**批量陷阱：**Store 一批多个事件若只对每条内部排序，仍可能形成事务一先锁 A 再锁 B，事务二先锁 B 再锁 A。应对整个事务的关联账号/flow 集合排序，或明确每事件独立事务并相应调整旧整批原子语义。`audit/store.go:37` 及 `TestStoreInsertBatchIsAllOrNothing` 当前承诺整批回滚，不能静默改成部分成功。

### 2. flow 后来归属账号，与不可变事件引用存在潜在冲突

STARTED、CODE_*（补全）、部分 PROOF_VERIFIED/REJECTED 产生时没有账号，只存在 flow ID。`user/service_signin_completion.go:128`、`:365`、`user/service_signin_completion_audit.go:55`；最终完成时才有账号事件 `:533`/`:555`。`identity_flow.proof_user_ids` 保存成功证明已有账号，`user_id` 保存最终结果；流程仅短期存在（默认 5 分钟，`user/service_signin_completion_store.go:25`）。

当前两种清理关联均需保留：

1. `user/query.sql:323` 在删除 live flow 前通过 `user_id` 或 `proof_user_ids` 清理它此前所有审计；即使最终账号事件仍在异步队列，也能清理。
2. `user/query.sql:252` 通过**历史审计**中该账号的 flow 引用清理同流程早期事件；即使 flow 已过期物理删除也能清理。

若 Store 在后来写入/重试时把新发现账号追加到原事件 related_resources，会改变规范明确要求幂等比较的稳定资源引用，造成相同 UUID 原始副本与存储副本冲突；这不是允许忽略的隐私字段差异。应把原始事件的 flow 引用保持不变，以可信 flow→账号闭包做查询/清理或维护独立可重建关联；若选择其他方案须在 design 写明并证明去重。

外部 reader 范围复核也必须理解这种关系：不能要求每条返回事件直接包含用户引用，否则早期 flow 审计会被拒绝；不能仅信任 data 中任意 `flow_id` 或 reader 自称归属。需明确定义可信关联证明、可验证闭包及 flow 消失时的行为。资源归属扩展是查询依据，不能随意升级为 actor 身份。

### 3. 新长度限制会影响当前合法宿主输入

| 字段 | 当前实际接受范围/引用 | 新设计与建议 |
|---|---|---|
| device.ID/Name，普通登录 | `user/service.go:39`、`:278`：非空 ID，ID/Name 各 `len <= 64`，是 **64 字节**而非 rune；`SignInWithCode` 与 `SignInWithIdp` 先验证 | 正常事件无需将 HTTP 上限放宽到256；64字节兼容原范围 |
| completion 拒绝的 device.ID | `decodeFlow` 仅比较可选设备值（`user/service_signin_completion_store.go:110`）；拒绝事件直接记录请求 `dev.ID`（`service_signin_completion_audit.go:55`），无长度检查 | 攻击/失败请求可提交超长值。应保证整条关键拒绝事件可安全记录，显式省略不可信超限设备字段并观测，不能因此修改认证响应或把正常64字节规则放宽 |
| request_id 默认 resolver | `accountkit.go:61`、`:389`：`^[A-Za-z0-9._-]{1,64}$`，非法时重新生成 | 保留默认规则 |
| request_id 自定义 resolver/直接领域调用 | `httpapi/reqid/reqid.go:11` 直接接收 resolver 字符串；`user.Meta`（`user/service.go:196`）及 RecordAdminForbidden 直接接收，没有64/128字节校验 | 设计中“原有64字符规则”只对默认 resolver 成立。128字节是宿主接口的新边界，必须明确兼容说明与事件失败策略 |
| admin issuer/subject/username | `user/admin.go:12`、`httpapi/admin/handler.go:111`、`user/service_lifecycle.go:20`：字符串直接进入事件；旧列 TEXT，无长度上限 | 512字节 namespace/id、256 rune name 会拒绝旧合法值。不得截断 issuer/sub 破坏主体身份；应明确破坏性边界、适配验证和安全降级/失败观测 |
| accountsvc admin claims | `internal/accountsvc/adminauth/claims.go:11`：sub仅要求非空非空白；username 任意 string。`verifier.go:110` token 总长16KiB，不是512/256字段限额 | 存在实际可通过现有 HTTP 验证但新审计拒绝的长 claim，应补真实调用测试，不能假设外部OIDC天然满足上限 |
| freeze/unfreeze reason | `user/service_admin.go:19`：TrimSpace、最多200 rune、拒绝控制/格式字符；required仅freeze | 保留原范围，统一写 data.reason；不能将其收紧为200字节 |
| subject_hint | `audit.Hint` (`audit/audit.go:51`) 取前8**字节**；摘要通常ASCII，provider subject未限制ASCII | 新 JSON编码对无效UTF-8的行为需明确；不得把完整摘要/provider subject装进data绕过脱敏 |
| IP | 宿主 Meta 无格式验证；`audit/store.go:146` 无效地址转NULL | 通用字段校验合法IP与旧“只省略IP仍存事件”有差异。认证构建器先安全省略无效IP可保留认证与审计事实 |
| 任意手工旧 Event | 公开 Event、Recorder、Store 未统一校验文本/总容量（旧DB多数TEXT） | 64KiB、32资源、JSON重复键等都是明确的新契约，历史迁移必须预检/拒绝不兼容行，不能隐式截断 |

### 4. 旧事件没有身份资源 ID，历史不能凭 kind/hint 逆推

`audit.Event` 只有 UserID/SessionID/FlowID/IdentityKind，无 IdentityID。绑定当前已取得 `info.ID`（`user/service_identity.go:192`、`:291`），解绑有 `target.ID`（`:354`），reveal有 `row.ID`（`service_admin.go:279`），换绑有旧ID与新 `info.ID`（`service_identity_replacement.go:138`/`:143`），补全创建有 `created.ID` 但 `bound` 只存 Kind（`service_signin_completion.go:501`/`:552`）。

新生产事件可以在这些位置补明确 IDENTITY target，当前历史迁移则只能使用已保存的 USER/SESSION/FLOW 引用与 identity_kind，不能按提示或当前活跃身份猜测历史 ID。需写清历史退化投影，不宣称全部历史都能恢复完整身份 target。

### 5. actor 不可由旧 ActorUser 默认值直接机械映射

`user/service.go:286` 默认所有未指定 actor 的事件为旧 USER，其中包括匿名发码、失败验证码、未知 refresh、尚无账号补全流程。新“已知主体必须 namespace+id”不能要求此类事件伪造账号。

建议：确已验证消费者主体的 USER 以配置 namespace + u_ID；已验证管理员以原完整 issuer + sub；系统维护以固定可信 SYSTEM namespace/id；未验证登录/发码为 ANONYMOUS；已知被访问会话但请求者可能为攻击者的 refresh 重放/无效凭证用 UNKNOWN 或 ANONYMOUS，账号放 target/related 而非自动赋给 actor。旧 actor_kind 快照独立保留，以维持管理HTTP投影USER/ADMIN/SYSTEM，不能用通用actor.type猜旧ADMIN。

旧 ADMIN issuer/sub可能为空（接口没有校验），历史转换必须保持缺失而非伪造已知主体；此时可用UNKNOWN +受控actor_kind快照保留历史语义。

## 全部26种动作映射建议

符号：`U`=配置namespace中的USER资源；`S`=SESSION；`I`=IDENTITY；`F`=IDENTITY_FLOW。Known user为已验证消费者主体，Admin为完整issuer+subject，System为稳定系统主体，Anonymous/Unknown均不携带猜测的namespace/id。所有动作统一补source、occur_time、请求元数据；旧actor_kind、identity_kind、session_id（仅背景时）、flow_id、subject_hint放受控data.accountkit，reason放data.reason。

“清理 U/F”表示账号purge覆盖直接U关系及可信flow归属闭包；flow删除前清理请求侧隐私。敏感字段清理至少client_ip/device_id/subject_hint及被明确列入契约的展示name；稳定引用、action/source/occur_time保留。没有可确认账号/flow的事件仍受retention；消失flow的迟到写入保守移除隐私。

| action | 当前生产来源 | actor建议 | target / related建议 | 旧字段/清理要点与已有测试依据 |
|---|---|---|---|---|
| CODE_SENT | `service_signin.go:87`，三个用途及补全共用 | SIGN_IN/COMPLETE为Anonymous；BIND/REAUTH为Known user | 有binding.UserID则U；flow用途F；session binding只作相关S | reason=purpose；kind+hint，IP/request，无验证码/CodeID；清理U/F；`TestSendBindCodeUsesBindPurposeAndQuota` |
| CODE_SEND_REJECTED | `service_signin.go:49/59` | 同上 | 同上 | 禁用渠道/限额dimension；清理U/F；`TestSignInValidationAndRateLimitAndRedisDown` |
| CODE_SEND_FAILED | `service_signin.go:80` | 同上 | 同上 | SEND_FAILED，不能带sender原错误；清理U/F；发码测试与sender安全错误回归 |
| SIGN_IN | `service.go:409`，已验证直接登录/补全完成后 | Known user | 新S为直接target，U相关；有F则相关F | NEW_USER仅首次；旧SessionID为新会话；清理U/F；`TestSignInCreatesUserThenReusesIt`、补全生命周期 |
| SIGN_IN_FAILED | `service_signin.go:143`、`service_idp.go:34`、`service_signin_completion.go:120` | 无效证明Anonymous；证明有效且账号冻结等可Known user | 只在确知账号时相关U；有已知F时相关F；不能用手机号/邮箱当资源ID | CODE/IDP安全原因、kind/hint；清理U/F；`TestSignInWrongCodeAndCrossChannel`、`TestSignInFrozenAndPendingDeletion` |
| REAUTHENTICATED | `service_me.go:255` | Known user | 当前S target，U相关 | 当前session_id+kind/hint；清理U；`TestReauthenticateUpdatesAuthTimeOnlyForAnchor` |
| REAUTHENTICATION_FAILED | `service_me.go:172/183/241`等 | 已验证access主体Known user | 当前S target，U相关 | 错码/NOT_ANCHOR/SESSION_EXPIRED/REVOKED/USER_FROZEN；清理U；`TestReauthenticateFrozenUserAudited` |
| TOKEN_REFRESHED | `service_session.go:37/86` | 有效refresh正常使用可Known user | S target，U相关 | GRACE_REPLAY保留相同token pair语义；清理U；`TestRefreshRotatesAndOldTokenEntersGrace` |
| REFRESH_REJECTED | `service_session.go:48/82/98/103/133/140` | 未知tokenAnonymous；已知会话但无效凭证Unknown | 已知则S target、U相关；未知target为空 | UNKNOWN_TOKEN、GRACE_UNAVAILABLE、会话/账号状态；清理U；`TestRefreshRejectsUnknownRevokedAndExpired` |
| REFRESH_REUSE_DETECTED | `service_session.go:67` | Unknown，不能把攻击者归为合法user | 被吊销S target，U相关 | TOKEN_REUSE；当前不另记SESSION_REVOKED，不能顺手增加事件改变计数；清理U；`TestRefreshReplayAfterGraceRevokesSession`与遏制失败回归 |
| SESSION_REVOKED | `service.go:407`、`service_session.go:247/319`、`service_lifecycle.go:142` | 按起因Known user/Admin/System | 被撤销S target，U相关；有F时相关F | session_id指被撤销会话，不一定是actor会话；登录替换/换绑/冻结/注销/purge等原因保留；清理U/F；会话、生命周期、admin、purge测试 |
| IDENTITY_BOUND | `service_identity.go:192/291`、`service_identity_replacement.go:143`、`service_signin_completion.go:552` | Known user；流程中新号创建完成后Known user | 新/已有I target，U相关；当前S/F相关 | ALREADY_BOUND/IDENTITY_REPLACED保留；历史缺I；清理U/F；`TestBindWithCodeHappyPathIdempotentAndScopeAfterRefresh`、`TestBindWithIdpHappyIdempotentConflictAndLimit`、`TestReplaceIdentity` |
| IDENTITY_BIND_REJECTED | `service_identity.go:150/187/235/243/286` | Known user | U作为被尝试修改账号；只在确认当前归属时放I，不能泄露另一账号身份 | CODE/IDP/冲突/上限等；kind+hint；清理U；绑定失败/归属测试 |
| IDENTITY_UNBOUND | `service_identity.go:354`、`service_identity_replacement.go:138` | Known user | 旧I target，U相关；当前S相关 | 换绑reason=IDENTITY_REPLACED；清理U；`TestUnbindIdentityGuardsLastAnchorAndOwnership`、`TestReplaceIdentity` |
| IDENTITY_REVEALED | `service_admin.go:279` | Admin | I target，U相关 | 只kind+hint进入审计，明文只HTTP授权响应；清理U；`TestGetUserDetailAndRevealIdentity` |
| USER_DELETED | `service_lifecycle.go:90` | Known user或Admin | U target；消费者当前S只是相关 | 软删除提交后记录，并逐S另记撤销；先清理F再删F；`TestDeleteMeSoftDeletesRevokesAllSessionsAndAudits`、`TestAdminDeleteAndUndeleteShareConsumerRules` |
| USER_UNDELETED | `service_lifecycle.go:135` | Known user或Admin | U target；当前S相关 | 不新建/撤销会话；清理U；`TestUndeleteRestoresActiveAndRefreshWorksAgain` |
| USER_PURGED | `service_purge.go:135` | System | U target | purge提交后记录；自身无请求侧PII；清理U/F；`TestPurgeAnonymizesAccountIdentitiesSessionsAuditAndHostTables` |
| USER_FROZEN | `service_admin.go:76` | Admin | U target | reason为管理员最多200rune输入；另记S撤销；清理U；`TestFreezeRevokesSessionsSnapshotsAndAudits` |
| USER_UNFROZEN | `service_admin.go:108` | Admin | U target | reason可缺失，旧HTTP应为null而非伪造空对象文本；清理U；同admin测试 |
| ADMIN_FORBIDDEN | `accountkit.go:279`，**绕过user.Service.record** | Admin | 当前没有账号/路径目标，保留空targets；不能从任意请求path猜合法资源归属 | ROLE_REQUIRED；现有UserID为空，账号列表不可见；无U清理，仅retention/宿主管理员治理；`accountkit_test.go:491 TestRecordAdminForbidden` |
| IDENTITY_REPLACE_REJECTED | `service_identity_replacement.go:37` | 已验证principal为Known user；无法信任principal时Unknown | 确认归属的旧I可target，U相关；格式错误/未确认归属不得造I | 仅安全业务原因，基础设施不误记拒绝；kind+新目标hint；清理U；`TestReplaceIdentityPreflight`、`TestReplaceIdentityNewTargetProof` |
| SIGN_IN_COMPLETION_STARTED | `service_signin_completion.go:128` | Anonymous（无本地账号） | F target | kind/hint/IP/device/request；后续关联账号不追改原actor；清理F历史闭包；`TestSignInCompletionAuditLifecycleExists` |
| SIGN_IN_COMPLETION_PROOF_VERIFIED | `service_signin_completion.go:365` | 成功证明有归属可Known user，否则Anonymous | F target；已确认proof owner U及I可related | proof.UserID可以空；当前仅此单个proof归属，不代表整个流程归属；清理F/所有proof owners；补全生命周期、清理测试 |
| SIGN_IN_COMPLETION_COMPLETED | `service_signin_completion.go:554` | Known user | F target，U/S相关；新建I可由单独BOUND表示 | 当前之后再记录SIGN_IN，二者是不同事件，重放完成结果不重复记已提交生命周期；清理U/F；补全生命周期 |
| SIGN_IN_COMPLETION_REJECTED | `service_signin_completion_audit.go:13/55` | Anonymous/Unknown，不因知道flow就认定账号actor | 已解析F target；无效token没有F；不能从原始token造资源引用 | 安全reason涵盖INVALID、ALREADY_COMPLETED、码/策略/限额/UNAVAILABLE/INTERNAL；不是所有拒绝都业务错误；清理F历史闭包；生命周期/补全review测试 |

## 账号、身份、会话与flow关系的实施检查项

- 所有旧UserID必须转换为明确可信用户引用（actor/target/related之一）或历史兼容关联，不能只藏在data导致列表/清理漏扫。
- 所有旧FlowID必须同时有受控flow引用及旧HTTP快照；资源namespace必须与source过滤一起限定实例。
- SessionID有两种角色：SESSION_REVOKED/TOKEN_REFRESHED/REAUTH等直接目标；IDENTITY_BOUND/USER_DELETED等背景会话。不能机械把所有session_id当target。
- 直接登录创建身份当前**没有**独立IDENTITY_BOUND事件；补全创建身份则有。迁移/实现不能因为“统一”而给普通登录额外造事件。
- 已解绑身份的归属通过历史稳定资源引用保留；purge仍处理所有软删身份（`service_purge.go:97`），不能仅查询active identity构造清理范围。
- 删除流程必须覆盖`user_id`和`proof_user_ids`；一个冲突流程可能涉及多个账号，清理任一相关账号均不得留下请求侧PII。
- admin角色属于宿主认证；ADMIN_FORBIDDEN走Auth门面，需要与领域构建器统一source/UUID/校验，不得遗忘此旁路。
- 各旧字段空串→NULL语义见`audit/store.go:130`；HTTP固定DTO见`httpapi/admin/response.go:108`，FlowID为空省略、其他大部分字段输出null；通用data不能直接序列化。
- 旧reader仅按user_id过滤（`audit/query.sql:16`），不是flow闭包；change扩展actor/target/related/flow覆盖时应清楚说明新增结果范围，并验证授权。
- `httpapi/admin/audit.go:27`先UserExists再reader；角色由Router前置执行。只写模式503必须保留这套顺序和未知用户404。
- `accountkit.go:152/211/242`即使注入Recorder仍创建本地Store供管理与retention，证实提案提出的错配确实存在。
- `audit/async.go:161`当前错误日志直接输出err，注释仅假设pgx。接纳宿主批量后端时必须改安全分类，不输出任意宿主文本。

## 最低并发与兼容验收建议

1. 受控真实PostgreSQL锁屏障验证：writer先持flow、purge先scrub已存在同ID事件的死锁交错；修正顺序后两者均可完成且隐私被清理。不能只time.Sleep。
2. 账号A/B关联同flow、批内事件顺序A→B与B→A并发写入；账号/flow全局排序，取消后无泄漏连接/锁。
3. writer读到无归属flow，完成事务后发现新owner，必须退出重试；补全、删除、过期清理均无flow→user反向锁。
4. 已发生但未落库的初始flow事件：分别在完成前/完成后/softDelete后/purge后/flow自然过期删除后写入，隐私与账号可查询关系符合契约。
5. 原始同ID事件在scrub后重放：names/IP/device/hint不恢复；稳定resource变化仍报冲突；late owner发现不导致非隐私比较被任意忽略。
6. 回滚宿主匿名化回调后，账号/flow/审计清理一起回滚；迟到写入只能依据已提交状态决定脱敏。
7. 真实调用验证64字节设备、超过128字节自定义requestID、超过512字节issuer/sub、超过256rune username、200rune reason、无效IP、超长补全拒绝设备。记录兼容策略而非修改HTTP认证结果掩盖事件丢失。
8. 历史转换覆盖缺issuer/sub、缺identityID、未来未知枚举、未知/已消失flow；拒绝无法兼容值必须可识别且不改旧表，不猜历史身份。
9. 外部reader返回其他source/用户事件、只包含flow的合法历史事件、未知action与敏感data；分别拒绝越界、保留可信flow事件、保留固定安全DTO。
10. 保留已有清理证据测试：`TestSignInCompletionPurgeScrubsEarlierFlowAudit`、`TestSignInCompletionPurgeScrubsFlowBeforeFinalAuditIsStored`（`service_signin_completion_review_test.go:352/380`）；它们只证明**清理已落库早期事件**，尚未证明真正purge之后才插入事件安全。

本盘点仅阅读代码和工件，不声称以上并发问题已复现或已有测试通过。报告列出的是新增实现必须证明的锁图与兼容边界。
