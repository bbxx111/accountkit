## Why

ai-food 的 auth-server 已具备完整账号认证能力，但模块路径和发布流程仍绑定产品仓库。accountkit 将其作为独立 Go 包供两个无关产品分别集成，避免重复实现和相互耦合。

## What Changes

- 从 ai-food 提取完整 packages/auth-server，包括代码、测试、内置迁移、sqlc 配置及生成代码，保持现有认证行为。
- 保留验证码、微信和 Apple 登录、身份绑定、令牌、会话、重新认证、用户生命周期、管理接口、隐私加密、审计和维护任务。
- 保留依赖注入及可配置 PostgreSQL schema、Redis 前缀、令牌域和密钥；宿主负责发送器、管理员验证器和业务匿名化实现。
- 建立独立构建、测试、集成示例和版本发布检查；生产首发依赖 harden-migration-safety 完成。
- **BREAKING**：Go module 导入路径改为新仓库地址，调用方需调整导入；提取阶段保留根包名 authserver，HTTP 协议和数据库格式不因改名而改变。
- 不包含 MySQL、统一账号服务、独立微服务、前端 SDK、产品页面或两个产品的正式迁移。

## Capabilities

### New Capabilities

- `embedded-auth-package`: 独立 Go 包、完整功能兼容、宿主扩展与部署隔离。
- `versioned-auth-storage`: 内置数据库定义、版本化迁移以及旧数据库兼容。

### Modified Capabilities

无；accountkit 尚无已归档规格。

## Impact

目标是 accountkit 根模块及其测试、生成配置、文档和持续集成。源基线为 ai-food 提交 7b4c4ecfdba4a05d54810aa2152b40d5c7da00c2 中 packages/auth-server。源仓库和 duopandian 不在本变更写入范围。远程仓库 URL 和正式 module 路径须在实现导入路径替换之前确定，不能伪造已存在的远程地址。