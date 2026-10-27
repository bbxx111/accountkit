## MODIFIED Requirements

### Requirement: 保留访问令牌与显式撤销边界

刷新期限到达或本次过期拒绝 SHALL 不自动让已签发access失效。Authenticate SHALL 保留JWT时效及Redis吊销验证，不新增数据库查询或改变fail-open。内省 SHALL 以数据库会话的吊销状态为准，不按刷新期限判定access。显式单个/批量撤销、同设备登录清理、冻结/注销/换绑的撤销集合 SHALL 继续覆盖尚未清理、未吊销但已到刷新期限的会话，保留既有返回码、计数与审计。

#### Scenario: access仍在自身有效期内
- **WHEN** 数据库refresh期限已到，但access本身有效且未被明确吊销
- **THEN** 设备列表不展示该会话，refresh/reauth不能续用，但Authenticate、本地验签及内省都仍判定该access有效

#### Scenario: 撤销已经到期的会话
- **WHEN** 使用已知会话ID、refresh凭据或合法管理/批量操作明确撤销未清理的到期会话
- **THEN** 沿用原撤销规则；正常Redis下Authenticate对应的未过期access失效，内省在提交后即失效，不受Redis影响；不能因为活跃列表过滤而漏掉撤销目标

#### Scenario: 清理保留期不变
- **WHEN** 到期行尚未达到既有30天清理保留窗口
- **THEN** 不因新列表/计数规则而提前硬删除；原清理边界和匿名化流程保持
