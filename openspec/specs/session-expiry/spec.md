# session-expiry Specification

## Purpose

统一账号会话在刷新期限边界的可续用判断，使设备列表、管理员计数、刷新轮换和重新认证对自然过期给出一致结果，并防止等待锁或缓存期间使用旧时间绕过到期，同时保留已发访问令牌和明确撤销的既有兼容行为。

## Requirements

### Requirement: 一致的会话期限边界

用于续期、重新认证、换绑和活跃会话展示的会话 SHALL 同时未吊销且刷新期限严格晚于操作判定时刻；相等 SHALL 视为过期，不增加JWT时间容差。判定时刻 SHALL 采用应用时钟并统一到UTC微秒精度，领域判断与数据库过滤使用相同的时间值。

#### Scenario: 微秒边界
- **WHEN** 未吊销会话的刷新期限分别位于判定时间前1微秒、恰好相等和后1微秒
- **THEN** 前两者视为过期，仅最后一者满足会话期限条件

#### Scenario: 过期不是账号状态迁移
- **WHEN** 会话自然到达刷新期限
- **THEN** 不自动改变账号状态、写入撤销标记或立即删除会话记录

### Requirement: 活跃会话列表和计数排除过期记录

消费者与管理员会话列表、管理员用户详情 active_session_count SHALL 只包含判定时间上未吊销且未到刷新期限的记录。结果 SHALL 保持用户隔离、原排序、DTO和空集合语义；读取 SHALL 不执行撤销或清理。

#### Scenario: 混合会话
- **WHEN** 同一账号存在有效、已吊销、已到期及恰好到期的会话
- **THEN** 两个会话列表仅返回有效记录，固定相同时间且无并发写入时管理计数与列表数量一致

#### Scenario: 读取不改变保留数据
- **WHEN** 列表过滤掉到期会话
- **THEN** 原会话行、撤销字段和审计保持不变，其他账号记录不被计入或返回

### Requirement: 刷新轮换锁后重新判期

刷新 SHALL 在取得会话写锁并完成必要状态读取后，以新的当前时间复核到期条件。等待期间到期的会话 SHALL 返回既有400 OAuth invalid_grant，不更新会话、签发新令牌或写入新宽限结果。有效轮换 SHALL 从锁后的判定时间计算滑动新期限和签发时刻，不使用请求开始时刻。

#### Scenario: 等待锁跨过到期时间
- **WHEN** refresh 请求进入时未过期，但取得会话锁后的判定时间已到刷新期限
- **THEN** 请求失败，refresh哈希、rotate_time、refresh_expire_time和last_used_time不变，记录 SESSION_EXPIRED 拒绝原因

#### Scenario: 等待后仍有效
- **WHEN** 请求经过锁等待后会话仍可续用
- **THEN** 正常轮换，新期限从实际轮换判定时刻开始，默认滑动30天的配置语义不变

### Requirement: 宽限重试保持有效性与时间一致

宽限重试 SHALL 保留相同token pair，不再次延长会话期限。宽限资格 SHALL 使用会话读取后的当前时间；缓存返回后 SHALL 使用新的时间复核会话和pair期限并计算剩余有效期。会话已过期时优先拒绝，不按重放泄漏处理；会话仍有效但资格判定时已经超出宽限 SHALL 沿用原重放规则。缓存等待本身 SHALL 不将原本合格的请求重分类为泄漏；缓存故障或pair自身已过期 SHALL 拒绝且不误吊销，不返回负的剩余有效期。

#### Scenario: 正常并发重试
- **WHEN** 多个请求竞争同一refresh，胜者完成轮换，其他请求在宽限内且会话仍有效
- **THEN** 其他请求取得胜者同一token pair，没有额外轮换或续期；elapsed等于配置宽限仍按既有包含边界处理

#### Scenario: 等待缓存跨过刷新期限
- **WHEN** previous-refresh 请求读取会话时仍有效，但取得缓存结果后的判定时间已到刷新期限
- **THEN** 返回 invalid_grant 和 SESSION_EXPIRED 审计原因，不返回缓存令牌、不额外吊销或续期

#### Scenario: 合格重试等待缓存跨过宽限
- **WHEN** 会话读取后的资格判定在宽限内，缓存返回时跨过宽限但会话与pair仍有效
- **THEN** 返回同一token pair并按返回时刻计算剩余期限，不将等待延迟认定为重放，不重复续期

#### Scenario: 缓存失败或缓存令牌过期
- **WHEN** 会话仍可续用且位于宽限内，但缓存读取失败、结果缺失或pair有效期已到
- **THEN** 按既有 GRACE_UNAVAILABLE 路径拒绝，不把依赖/缓存故障记作已遏制的凭证重放

### Requirement: 到期会话不能重新认证

重新认证 SHALL 在原身份及REAUTH证明有效的前提下，取得用户和会话锁后重新取判定时间并复核会话期限。已到期 SHALL 返回既有401 TOKEN_INVALID 与Bearer challenge，记录 REAUTHENTICATION_FAILED / SESSION_EXPIRED；不改变auth_time、刷新期限或签出新access。关闭敏感操作新鲜度检查 SHALL 不绕过会话到期约束。

#### Scenario: 等待中到期
- **WHEN** 有效REAUTH证明已被消费，但等待锁后会话已到刷新期限
- **THEN** 重新认证失败且数据库会话不变，验证码不因失败恢复；客户端需要重新登录

#### Scenario: 有效重新认证
- **WHEN** 取得锁后的会话仍可续用，其他原有校验均通过
- **THEN** 按锁后时间更新auth_time并返回原格式的新access，不返回refresh、不延长refresh期限

### Requirement: 保留访问令牌与显式撤销边界

刷新期限到达或本次过期拒绝 SHALL 不自动让已签发access失效；Authenticate和内省 SHALL 保留JWT时效及Redis吊销验证，不新增数据库查询或改变fail-open。显式单个/批量撤销、同设备登录清理、冻结/注销/换绑的撤销集合 SHALL 继续覆盖尚未清理、未吊销但已到刷新期限的会话，保留既有返回码、计数与审计。

#### Scenario: access仍在自身有效期内
- **WHEN** 数据库refresh期限已到，但access本身有效且未被明确吊销
- **THEN** 设备列表不展示该会话，refresh/reauth不能续用，但access及内省仍按既有验证规则处理

#### Scenario: 撤销已经到期的会话
- **WHEN** 使用已知会话ID、refresh凭据或合法管理/批量操作明确撤销未清理的到期会话
- **THEN** 沿用原撤销规则，正常Redis下对应未过期access失效；不能因为活跃列表过滤而漏掉撤销目标

#### Scenario: 清理保留期不变
- **WHEN** 到期行尚未达到既有30天清理保留窗口
- **THEN** 不因新列表/计数规则而提前硬删除；原清理边界和匿名化流程保持
