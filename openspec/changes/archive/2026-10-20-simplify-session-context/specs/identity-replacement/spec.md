## MODIFIED Requirements

### Requirement: 保留当前会话并撤销其他会话

成功换绑 SHALL 继续撤销同账号其余会话并标记 IDENTITY_REPLACED。当前会话 SHALL 仅在非空认证来源不含旧身份时保留，其refresh、auth_time与来源不变；含旧身份则同时撤销。当前来源缺失 SHALL 在身份修改前作为内部不变量错误拒绝，不能解释为unknown、空集合安全或以新证明修补。BIND证明 SHALL 不改变来源，成功仍返回新身份资源而不发pair。被撤销会话的refresh含宽限重试 SHALL 受数据库限制；正常Redis下access/内省受吊销限制，Redis故障保持原fail-open/告警，不伪装已提交事务回滚。

#### Scenario: 多设备换绑
- **WHEN** 账号多个会话中一个完成换绑
- **THEN** 其他会话不能刷新；当前非空来源不含旧身份时保留，否则撤销；当前来源缺失则整个换绑拒绝并不改身份/会话

#### Scenario: 提交后吊销缓存失败
- **WHEN** 数据库事务提交后Redis吊销写入失败
- **THEN** 换绑仍成功，refresh受数据库限制，记录告警并保持既有access故障边界

#### Scenario: 用保留身份重新认证后换绑
- **WHEN** 当前会话先通过邮箱B重新认证，随后换绑手机号A
- **THEN** 当前来源B和refresh保持，其他会话仍撤销

#### Scenario: BIND 证明不能保住旧来源会话
- **WHEN** 当前来源为旧手机号A，用新手机号C的BIND码完成换绑
- **THEN** 当前会话撤销，不把来源转移到C；需重新登录取得会话
