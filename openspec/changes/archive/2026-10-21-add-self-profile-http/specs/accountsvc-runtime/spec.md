## ADDED Requirements

### Requirement: 可选本人资料 Schema 装配

serve SHALL 支持可选 ACCOUNTSVC_PROFILE_ATTRIBUTES_SCHEMA_FILE，在监听及运行依赖创建前有界读取本地普通文件并以现有资料 Schema 规则校验。空设置 SHALL 保持禁用；非空设置读取失败、非普通文件、空文件、超过 64 KiB 或内容无效 SHALL 拒绝启动。错误 SHALL 仅含配置项名称及安全分类，不含路径、内容或校验器原文。SHALL 不支持 URL 或热更新，库 New/ConfigFromEnv 保持无文件 I/O，migrate SHALL 不读取或校验此服务设置。

#### Scenario: 启动后本人读写
- **WHEN** serve 使用合法 Schema 文件启动且其他依赖满足
- **THEN** `/v1/users/me/profile` 提供同一消费者协议，Schema 声明的属性全部对本人可读写，原接口保持

#### Scenario: 缺失配置与错误文件
- **WHEN** 配置为空，或非空但文件不可读/非法/超限
- **THEN** 前者启动不受影响且认证后的合法资料请求返回 503；后者监听前失败且不泄露路径或文件内容

#### Scenario: migrate 隔离
- **WHEN** migrate 设置了不存在或非法的资料 Schema 文件路径
- **THEN** 忽略此服务设置，迁移行为只受既有迁移配置及依赖约束

#### Scenario: 配置变更
- **WHEN** 运行期间文件被修改
- **THEN** 当前实例规则不变化，重启后加载新内容；多副本配置一致性由部署方保证，不自动转换旧资料
