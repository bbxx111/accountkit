## Why

根 Go 包名称与 accountkit 模块名称不一致，文档还包含无关项目的背景叙述。需要统一公共包名称和独立项目定位，并明确 accountsvc 是可选的官方宿主。

## What Changes

- **BREAKING**：根 Go 包从 `authserver` 重命名为 `accountkit`，同步源码文件名、测试、示例及诊断前缀；module 路径不变。
- 清理仓库文档中的其他项目名称、路径和依赖叙述，以本项目契约及冻结基线描述兼容性。
- 明确直接嵌入库与可选 accountsvc 服务两种使用方式；本变更只记录服务定位，不实现服务运行时。
- 保留冻结 SQL、原始 manifest、协议与存储格式，以及维护锁的历史标识。

## Capabilities

### New Capabilities

- `accountkit-package-identity`: 根包命名、文档独立性和可选服务边界。

### Modified Capabilities

无已归档主规格；同步修订现有未归档变更中的项目叙述，保留历史任务记录及变更标识。

## Impact

影响根包、外部测试包、示例、代码注释、诊断消息及仓库文档。调用方默认导入后使用 `accountkit` 标识符，也可显式使用旧别名继续编译。无数据库迁移，无认证行为变更，无提交、推送或发布动作。
