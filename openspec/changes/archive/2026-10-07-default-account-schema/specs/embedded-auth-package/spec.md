## MODIFIED Requirements

### Requirement: 产品部署隔离

包 SHALL 允许宿主分别设置 PostgreSQL schema、Redis 前缀、签发者、受众、密钥、发送器及管理员验证器。默认 schema 为 account，Redis 前缀为 auth:；默认值不代表自动获得跨产品隔离。未指定或为空的 Schema SHALL 采用 account，显式合法 Schema SHALL 保持用户配置；环境变量名称 AUTH_SCHEMA SHALL 保持不变。

#### Scenario: 两个实例共享基础设施
- **WHEN** 两个实例显式使用不同 schema、Redis 前缀、签发者、受众和密钥
- **THEN** 验证码、身份、会话、吊销与维护互不影响，一个实例拒绝另一个实例签发的令牌

#### Scenario: 默认账号 schema
- **WHEN** 库宿主或 accountsvc 未指定 Schema 或将其设置为空
- **THEN** 使用 account schema，首次迁移将包表及迁移记录建在其中，不自动创建旧默认 auth schema

#### Scenario: 显式 schema
- **WHEN** 宿主通过配置或 AUTH_SCHEMA 环境变量指定合法 schema
- **THEN** 沿用该值，连接的 search_path 与迁移位置一致
