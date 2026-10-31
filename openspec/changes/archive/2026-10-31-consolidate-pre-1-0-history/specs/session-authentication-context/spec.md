## ADDED Requirements

### Requirement: 来源数据生命周期

当前结构 SHALL 不接受没有来源的可用会话；使用来源的领域动作 SHALL 将缺失作为内部不变量错误，不反推或补造来源。来源关联 SHALL 随有界会话清理删除，匿名化覆盖全部历史关联并与宿主回调同事务。JWT、refresh、scope 与普通 Authenticate 热路径 SHALL 保持原格式和数据库访问边界；内省只按 sid 读取会话吊销状态，不读取来源。

#### Scenario: 重新认证建立来源
- **WHEN** 当前库中人为损坏的会话没有来源，再尝试重新认证
- **THEN** 拒绝为内部不变量错误，不用重新认证隐式修补；正常新登录仍原子建立非空来源

#### Scenario: 清理和匿名化回滚
- **WHEN** 会话到清理边界或账号匿名化
- **THEN** 来源不留孤儿，宿主回调失败时清理整体回滚

#### Scenario: 普通鉴权不补查来源
- **WHEN** 宿主调用 Authenticate 或远程内省
- **THEN** Authenticate 保留 JWT/Redis 路径；内省只读取会话吊销状态；两者都不查询或写入来源

## REMOVED Requirements

### Requirement: 来源数据生命周期与兼容
**Reason**: 由「来源数据生命周期」取代，内容相同，仅移除对旧 v0.3.0 v1 与前一整合 v1 的迁移拒绝场景；这些开发期结构从未被正式使用，预检边界由 `versioned-auth-storage` 的「基线预检边界」统一描述。
**Migration**: 无。
