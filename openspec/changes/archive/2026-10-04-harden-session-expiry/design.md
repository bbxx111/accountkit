## Context

动机见 [proposal.md](proposal.md)。相关约束来自 [嵌入式库](../../../specs/embedded-auth-package/spec.md)、[消费者内省](../../../specs/consumer-token-introspection/spec.md)、[身份换绑](../../../specs/identity-replacement/spec.md) 和 [迁移安全](../../../specs/migration-safety/spec.md)。

当前刷新在进入事务前读取 now，rotate 等待 session 行锁后仍复用它；重新认证会锁 user/session，但只复核归属和 revoke_time。ListActiveSessionsByUser 和 CountActiveSessionsByUser 不含 refresh_expire_time 条件。换绑已经检查刷新期限并在取得锁后重新取时钟，可作为对齐依据。

access 验证只检查签名/claims、ID和 Redis 吊销，不查询数据库。刷新期限到达不会自动吊销此前已发出的 access；重新认证接近刷新期限时也可能签出超出该期限的 access。本变更明确保留此边界，而非把两个到期时刻混为一个。

## Goals / Non-Goals

**Goals:** 列表、计数、续期和重新认证使用一致的刷新期限边界；关键写入不能依赖锁等待前的旧时间；修复保持既有有效会话、并发刷新宽限及明确撤销的行为。

**Non-Goals:** 新端点或DTO字段、会话历史查询、设备数量上限、绝对生命周期、自动吊销到期 access、JWT格式/有效期变更、fail-closed改造、时钟回拨防护系统、数据库结构迁移和实际外部环境验收。

## Decisions

### 1. 区分三个判断范围

| 范围 | 判定 | 使用位置 |
|---|---|---|
| 可续用会话 | 未吊销且 refresh_expire_time 严格大于判定时间 | 设备列表、管理计数、刷新、重新认证、换绑当前会话 |
| 可显式撤销的已存储会话 | 未吊销；可以已到刷新期限 | 单个撤销、批量撤销、同设备登录清理旧会话、冻结/注销/换绑的撤销集合 |
| 可接受的 access | 既有JWT规则和Redis吊销规则 | Authenticate、Bearer、中间件及内省 |

相等边界视为过期，刷新期限不额外叠加 JWT leeway。刷新到期不改变账号 ACTIVE 等状态，也不主动写 revoke_time、Redis吊销键或删除会话行。

相较统一给所有名为 Active 的查询添加期限过滤，这种划分保留了撤销覆盖：已到刷新期限的 session 仍可能对应未到JWT期限的 access，显式撤销必须仍能使它失效。内部保留查找未吊销记录的查询，补充准确注释，不借本次改动大规模重命名公开/生成接口。

### 2. 时间来源与精度

使用既有领域时钟 `s.now()`，不新增 Config/Deps 参数。参与刷新期限判断和相关SQL条件的判定时间统一为 UTC、截断到微秒；PostgreSQL timestamptz 保存微秒精度，领域判断和查询参数使用同一值，测试覆盖期限前1微秒、相等和后1微秒。辅助函数保持领域内部，集中未吊销/未到期判断及时间归一化。

列表或计数每次调用读取一个时间快照并传入SQL；不同请求之间若有并发刷新或时间推进，不承诺列表与计数完全相同。写路径在取得所需行锁、完成必要状态读取后重新采样，以最终写入前的时间为判定点。不能把请求开始、验证码消费前或事务开始时刻用作锁后判断依据。

这定义的是操作的判定点，不保证响应送达客户端时会话仍未过期。应用时钟与数据库时钟的校准仍由运行环境负责，不改用数据库事务开始时固定的 now()。

### 3. 列表与计数

给 `ListActiveSessionsByUser`、`CountActiveSessionsByUser` 增加明确的 now 参数及 `refresh_expire_time > @now` 条件，保留 user_id 隔离和原排序。同步 ListSessions、AdminListSessions、管理员用户详情计数及仓储测试的调用；查询结果与DTO不新增字段，保持原空集合和404语义。

通过 sqlc generate 更新生成代码，不手改 user/db。该变化只过滤结果，不写 revoke_time，不产生撤销审计，不删除历史行。暂不增加索引或迁移；沿用现有用户索引，若实施中出现有证据的性能问题再明确评估。

### 4. 刷新轮换和宽限返回

当前 refresh 的初次判断仍用于快速拒绝；取得 session 行锁后再次检查未吊销、当前hash及账号状态，在签发/轮换前采样新的时间并检查刷新期限。若已到期，返回既有 ErrInvalidGrant，HTTP为400 OAuth invalid_grant，审计 REFRESH_REJECTED / SESSION_EXPIRED；不修改两个refresh哈希、rotate_time、refresh_expire_time、last_used_time或宽限缓存。

有效轮换的 rotate_time、滑动新期限、access签发时刻和宽限pair的到期元数据以该锁后时间为基准；SQL RotateSession 的CAS条件同时校验 `refresh_expire_time > @now`。不能只换SQL谓词后把0行误当成未知token，领域路径应先明确分类过期。

若锁内发现另一个请求已经轮换，继续原 previous-hash 宽限路径，不把并发重试统一当失败。该路径在读取会话后更新资格判定时间，以 `elapsed <= RefreshGrace` 判断宽限（等号边界保持）；缓存返回后再次取时间复核会话与pair期限并计算剩余有效期，不使用 Refresh 入口的旧 now。资格判定已在宽限内的请求，不因缓存等待期间跨过宽限就改判为重放；只要会话和pair仍有效，返回同一对token且不再次延长数据库期限。

若会话先到刷新期限，优先按 SESSION_EXPIRED 拒绝，不把自然到期误归因为重放。会话仍可续用且会话读取后的资格判定时间已经超出宽限时，走原重放处理；缓存读取失败仍仅拒绝、不误吊销。若缓存中的pair已无正的access/refresh剩余期限，则按既有 GRACE_UNAVAILABLE 拒绝，不返回负 expires_in，不把缓存失效当成凭证泄漏。保持同一token pair、Redis失败日志和审计结果不重复记录。

本轮不把宽限读取扩展为跨数据库/Redis事务，也不承诺所有并发撤销在响应返回前全局线性化；已有数据库撤销检查和access吊销边界保持。

### 5. 重新认证与换绑

重新认证继续先校验原锚点及REAUTH码，在 user → session 锁内复核身份关系、归属和吊销后，使用新的判定时间拒绝刷新期限已到的会话。拒绝返回与 ErrInvalidToken 兼容的错误，HTTP沿用401 TOKEN_INVALID 与 Bearer challenge；审计单独使用 REAUTHENTICATION_FAILED / SESSION_EXPIRED，不误记SESSION_REVOKED。

到期拒绝不更新 auth_time/update_time，不签出新access，不改变refresh期限，也不新建会话。验证码可能已经消费，不能因数据库回滚恢复；客户端重新登录，不通过重新认证复活到期会话。显式关闭 SensitiveOpVerification 只影响敏感操作新鲜度，不能允许到期会话重新认证。

有效重新认证使用锁后时间更新auth_time并签发access，仍不返回refresh、不延长refresh期限、不改变access TTL。SQL UpdateSessionAuthTime 增加以该auth_time为判定点的未过期条件。新私有错误分类或内部结果可区分过期审计，不新增公共HTTP错误码或更改旧会话吊销审计分类。

换绑复用统一的微秒判定规则，保持事务外预检和锁后复核、当前会话保留、其他会话撤销与原错误映射。不得在复用辅助函数时丢掉scope、user_id、认证新鲜度或旧身份复核。

### 6. 明确撤销与保留策略不变

GetActiveSessionByIDAndUser、GetActiveSessionByUserDevice 及撤销更新仍可取得/处理未吊销但已到刷新期限的行；它们不能被新的列表过滤规则替代。已知旧ID的消费者/管理员单个撤销、revokeOthers、管理员revokeAll、RFC7009及冻结/注销/换绑仍覆盖这些行，并沿用原返回码、revoked_count和审计语义。

保留既有30天会话清理窗口，不因为列表不再展示到期行就立即硬删除。已签发的有效access即使其数据库refresh期限已到，Authenticate/内省仍按原规则判断；只有明确吊销、JWT自身到期等既有条件改变其有效性。尤其不能因一次过期refresh/reauth拒绝额外写吊销集，暗中改变这一契约。

## Risks / Trade-offs

- [升级后设备列表数量减少] → 明确是过滤到期会话，数据仍在保留窗口内；不等于全部已发access被吊销。
- [旧客户端在到期会话上继续重新认证] → 401按既有认证失效处理，要求重新登录；不引入反复重认证的恢复循环。
- [时间采样或数据库精度不一致] → 微秒归一化、显式SQL时间参数、等号边界及锁等待回归测试。
- [共用查询误伤撤销或换绑] → 分离可续用查询与未吊销定位，保留过期会话明确撤销的回归。
- [一次性码已消费但锁后到期] → 如实记录，保留计数及原码消费规则，不伪造跨存储回滚。

## Migration Plan

无schema迁移、数据回填或清库；部署更新后的库或accountsvc即可。根 Config、环境变量、公开 Service 方法、HTTP DTO和默认TTL均不变；仓库内sqlc查询签名及测试调用随生成结果更新。

兼容说明标出过滤列表/计数和到期reauth拒绝两项边界变化。滚动部署时旧实例仍可能使用旧判断，应在完成升级后再宣称全实例已加固；回退二进制会恢复这些旧边界行为，不影响存储可读性，也不会恢复已吊销会话。验证使用可控时钟、一次性PG/Redis与本地fixtures，真实提供方验收继续暂缓。
