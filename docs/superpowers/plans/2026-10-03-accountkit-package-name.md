# accountkit 包名与独立文档实施计划

> 执行方式：按仓库默认采用 subagent-driven-development；包名修改和文档修改无共享文件时并行，随后统一审查验证。

**Goal:** 完成用户明确要求的包名重命名、文档清理和可选服务定位。
**Architecture:** 根模块继续提供独立可嵌入库；accountsvc 是可选宿主。本次为命名维护，不实现认证行为或服务运行时。
**Tech Stack:** Go 1.26.5、OpenSpec、Markdown。
**Spec:** [变更设计](../../../openspec/changes/rename-accountkit-package/design.md)、[规格](../../../openspec/changes/rename-accountkit-package/specs/accountkit-package-identity/spec.md)。

## Global Constraints

- module 保持 github.com/bbxx111/accountkit，根包为 accountkit。
- 不修改冻结 SQL、manifest、生成代码、HTTP/环境配置/令牌格式、维护锁历史值。
- 保留现有历史 change 标识和验证记录日期；不提交或推送。

## Review Focus

- 默认无别名导入是否使用 accountkit：通过嵌入示例构建与包名查询核验。
- 旧调用方显式别名是否仍可用：迁移说明和独立宿主编译核验。
- 混合版本维护锁是否一致：精确检查锁值差异。
- 基线是否被误改：冻结检查及 git diff 核验。
- 历史任务、文件引用和服务状态是否误导：全文搜索与独立审查。

## Task 1: Go 包重命名

文件：根 Go 文件、examples/embedded/main.go、相关可修改注释。
- [x] 更新声明和使用点，删除冗余导入别名，重命名门面及测试文件，gofmt。
- [x] 运行构建、vet、测试；确认 go list 输出根包 accountkit。

## Task 2: 文档清理

文件：README.md、AGENTS.md、docs、tests/testdata/source-baseline/README.md、现有 openspec/changes 工件。
- [x] 用独立项目表述替换外部产品叙述；同步新文件名，添加包名调用迁移说明。
- [x] 明确可选 accountsvc 定位；保留历史验证语境和不可变基线。
- [x] 检查命名检索、路径链接和 OpenSpec 严格校验。

## Task 3: 统一验证

- [x] 运行历史迁移检查与 git diff --check，独立审查后处理问题。
- [x] 仅按实际证据勾选变更 tasks，交付时列出数据库验证限制。
