## Why

accountkit 提供独立的账号认证 Go 包，以稳定的模块、宿主契约和发布流程供不同应用集成，避免重复实现和部署耦合。

## What Changes

- 建立完整独立模块，包括代码、测试、内置迁移、sqlc 配置及生成代码，保持固定基线的认证行为。
- 保留验证码、微信和 Apple 登录、身份绑定、令牌、会话、重新认证、用户生命周期、管理接口、隐私加密、审计和维护任务。
- 保留依赖注入及可配置 PostgreSQL schema、Redis 前缀、令牌域和密钥；宿主负责发送器、管理员验证器和业务匿名化实现。
- 建立独立构建、测试、集成示例和版本发布检查；生产首发依赖 harden-migration-safety 完成。
- **BREAKING**：Go module 导入路径改为新仓库地址，调用方需调整导入；HTTP 协议和数据库格式不因模块调整而改变。根包现统一为 accountkit，包名迁移由 rename-accountkit-package 记录。
- 本变更不包含 MySQL、统一账号中心、服务运行时、前端 SDK、产品页面或宿主数据迁移。accountsvc 定位为后续可选官方宿主，不是嵌入库的依赖。

## Capabilities

### New Capabilities

- `embedded-auth-package`: 独立 Go 包、完整功能兼容、宿主扩展与部署隔离。
- `versioned-auth-storage`: 内置数据库定义、版本化迁移以及旧数据库兼容。

### Modified Capabilities

无；accountkit 尚无已归档规格。

## Impact

目标是 accountkit 根模块及其测试、生成配置、文档和持续集成。固定基线及校验值保存在 tests/testdata/source-baseline/。模块路径为 github.com/bbxx111/accountkit；变更范围限于本仓库。