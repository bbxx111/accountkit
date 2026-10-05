## Context

范围见 [proposal.md](proposal.md)，命名与迁移契约见 [增量规格](specs/accountkit-package-identity/spec.md)。本次涉及公共 Go 导入与门面方法的破坏性迁移，因此保留设计工件；运行约束继续遵循 [嵌入式库](../../specs/embedded-auth-package/spec.md) 与 [包标识](../../specs/accountkit-package-identity/spec.md) 主规格。

当前包有19个 Go 文件；根 Auth 保存 `*consumer.Handler` 并通过 ConsumerHandler、RequireScope、RequireRecentAuth 使用它，accountsvc及示例通过根门面接入。必需清单中有37项原包路径条目。用户已确认统一更名并不保留旧导入或旧方法。

## Goals / Non-Goals

**Goals:** 源码、当前指南和可编译示例采用清楚的终端用户命名；支持直接包消费与根门面两种方式，保持所有HTTP与领域行为。

**Non-Goals:** 不重构Service接口或handler职责，不改路由、认证系统、数据、配置、依赖或部署；不建立consumer兼容层，也不追求全仓consumer字样零出现。

## Decisions

### 1. 一次完成包与公开门面迁移

整体移动 `httpapi/consumer` 至 `httpapi/enduser`，同步包声明、外部测试包声明及导入限定符。Handler、Deps、Service、IdentityReplacer、New、Router、AuthnOptions、RequireRecentAuth的结构与行为保持原样；原诊断前缀 `consumer:` 更新为 `enduser:`，仅影响包构造诊断，HTTP业务错误码/正文不变。

根 Auth 的 ConsumerHandler 直接改为 EndUserHandler。与本HTTP面对应的私有字段/参数采用 endUser，保持原路由构造和中间件委托；AdminHandler、Users、RequireScope、RequireRecentAuth等其余公开方法不变。同步accountsvc装配、根包测试及嵌入式示例。一个实施单元内完成引用迁移，避免提交只有目录移动却不能编译的中间状态。

替代方案是新增门面后弃用旧方法，或保留consumer类型别名包；这些都会留下两套名称。用户选择直接迁移，因此不实现兼容层。

### 2. 严格限定命名替换范围

改动针对Go包路径、包标识符、该门面及对应内部字段/注释。领域包 `user`、HTTP `/users`、JWT sub/sid、scope、错误枚举、Redis键、配置变量、迁移锁、固定源清单和module均不变；不重命名 `consumer-token-introspection` 等既有规格目录或与该Go包无关的consumer语义。

当前指南中的可执行示例更新，AGENTS目录表和门面约定同步。docs/compatibility补充迁移表并区分当前入口与明确标注的历史提取索引；历史归档、历史验收数据和已完成实施计划保留原名称，不伪装过去使用了新API。旧名搜索需要按“当前可执行引用、迁移说明、历史事实”分类，不能全局替换或以零命中作为唯一验收。

### 3. 测试选择器与可选能力保持

移动原测试文件并保留断言，外部测试使用 enduser_test 和新导入。必需清单原 `/httpapi/consumer/` 前缀一对一替换为 `/httpapi/enduser/`，比较迁移前后用例集合与数量。原则上保留既有测试标识；若名称直接引用旧门面而需同步调整，应记录旧新映射并更新门禁，不能减少必需项。

可选 IdentityReplacer 不变：已有自定义Service仅改导入，无需添加换绑方法。原未实现能力的503、近期认证、资源归属、错误码和限流用例保留。根门面测试仍覆盖宿主自选前缀、请求ID、认证中间件和独立管理面。

### 4. 验证公开消费，而非只做文本替换

实施前记录基线和测试清单；用临时外部宿主的编译检查表达新导入及 EndUserHandler 的目标，确认改名前新入口不可用，改名后能够在GOWORK=off下编译。直接包消费同时引用 enduser.Service/IdentityReplacer，验证类型结构仍满足原实现。

临时宿主、其指向当前检出的本地replace和原始日志仅放ignored `.test-output/`，不把本地replace写入提交的go.mod。检查旧包目录/旧公开方法确实移除，不增加只镜像实现文本的永久单元测试。

执行新包、根包、accountsvc、embedded示例等相关测试/vet，再运行一次完整库及严格服务门禁确认必需清单无漏验。真实数据库、Redis及实际Linux服务沿用一次性验证设施，RecoveryFixture按既有三阶段单独执行；不增加产品联调。审查重命名diff中的非命名改动，结果存本change的verification.md。

## Risks / Trade-offs

- [外部Go使用方无法编译] → 明确BREAKING且列出导入与方法迁移；不把HTTP兼容等同于Go源码兼容。
- [移动包后测试仍跑了但门禁漏掉旧条目] → 对37项旧选择器逐项映射，现有testgate验证实际pass；旧条目不直接删除了事。
- [全局替换修改协议或历史] → 限定文件/符号范围，保留历史与固定基线，人工核对非命名差异。
- [只验证内部构建遗漏外部类型引用] → 独立临时宿主分别使用根门面和直接enduser类型编译，继续验证库不依赖accountsvc。

## Migration Plan

| 旧调用 | 新调用 |
|---|---|
| `github.com/bbxx111/accountkit/httpapi/consumer` | `github.com/bbxx111/accountkit/httpapi/enduser` |
| `consumer.New` / `consumer.Deps` / `consumer.Handler` | `enduser.New` / `enduser.Deps` / `enduser.Handler` |
| `consumer.Service` / `consumer.IdentityReplacer` | `enduser.Service` / `enduser.IdentityReplacer` |
| `auth.ConsumerHandler()` | `auth.EndUserHandler()` |

1. 宿主更新依赖时同步修改以上导入和调用，重新编译并运行原接入测试；若代码使用显式Go导入别名，可以自行选择局部别名，但旧导入路径本身不再存在。
2. HTTP调用方保持原URL、DTO和凭据；服务环境变量、数据库/Redis及密钥不变，不进行数据迁移或会话清理。
3. 回退到旧库版本时同时恢复旧Go导入和门面调用并重新编译；不调用Down、UnsafeReset或清空Redis。
