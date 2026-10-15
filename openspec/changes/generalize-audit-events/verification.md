# 提案核验记录

日期：2026-10-15。范围：仅规划工件；未修改 Go、SQL、生成代码或运行配置，未实施、提交、推送或归档。

## 工件与范围

- [提案](proposal.md)、[详细设计](design.md)、[任务](tasks.md) 以及四份规格增量已生成。
- 分支为 docs/generalize-audit-events；工作区新增内容仅位于本 change。
- OpenSpec 状态为全部规划工件 done；这是文件/依赖状态，不代表实现完成。26 项实施任务均为未勾选。

## 实际检查

| 检查 | 结果 | 边界 |
|---|---|---|
| openspec status --change generalize-audit-events --json | 退出码 0；proposal/specs/design/tasks 均 done，isPlanningComplete=true | 仅证明规划工件齐全 |
| openspec validate --all --strict | 退出码 0；19 passed，0 failed | 结构验证不等于代码或运行行为已通过 |
| git diff --check | 退出码 0 | 未跟踪文档另作逐文件检查 |
| 新增 Markdown 空白、末尾换行和相对链接检查 | 无尾随空白、缺失末尾换行或失效本地链接 | 外部链接源于本会话调研，未在本次离线文件检查中逐个重访 |
| 工作区范围与任务检查 | 仅本 change 新增；26 未完成、0 已完成任务 | 其他未归档变更和主规格未改动 |

## 自审结论

- 已将最终选择写入设计：10 列、原生 UUIDv7、VARCHAR(128)、create_time、无公共 schema_version、namespace + id 主体识别、认证专属字段归入 data。
- 明确列出公开 Go 类型、审计资源名与旧游标的破坏性边界；保留管理员授权和非审计 ID/令牌契约。
- 修正现有规格中 e_ 永久不变及最新版本永远为 1 的冲突，保留冻结初始 SQL 和历史基线拒绝要求。
- 已识别 baseline 对 flow_id 的固定依赖、非空历史转换、外部读写错配、迟到审计恢复隐私及通用宿主错误泄露风险，并提供对应任务/验收场景。
- 提案新增的具体工程决策包括历史 staging 预备转换、外部模式显式 source/namespace 和仅写入查询 503；这些属于本次待评审方案，未执行任何数据转换或改变线上行为。

## 未验证项

- 未运行 Go 构建、单元测试、vet、race、sqlc 或数据库测试：本轮无实现改动。
- UUIDv7 具体 Go 依赖、已有合法字段长度、认证事件映射矩阵、锁序证明、JSONB 执行计划及迁移窗口尚待实施阶段核验。
- 未执行真实数据库升级、故障恢复、外部审计服务对接或匿名化并发测试；不得将本记录作为发布验收证据。
- 未归档；后续实施按 [tasks.md](tasks.md) 更新实际证据。

## 2026-10-16 namespace 案例补充

- 在 [设计文档](design.md) 的主体章节补充单 App 自建账号、多产品共享账号中心、直接使用外部 OIDC 主体三个 To C 案例。
- 明确 source 与 namespace 的区别，以及外部登录映射到本地账号后沿用本地主体；示例不引入新的消费者 OIDC 登录能力或角色字段。
- 核对现有 proposal、specs 和 tasks：案例解释现有身份契约，无需调整需求范围或实施任务，26 项任务仍未勾选。
- 实际检查：`openspec validate --all --strict` 退出码 0，19 项通过、0 项失败；`git diff --check` 退出码 0；三个 JSON 示例均成功解析并包含 USER、namespace、id。
- 本轮仅修改设计说明及本核验记录；未修改实现代码，未运行 Go 或数据库测试，未提交或改变原有暂存内容。
