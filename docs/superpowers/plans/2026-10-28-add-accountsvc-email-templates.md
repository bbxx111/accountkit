# accountsvc 邮件模板文件实现计划

> 需求与验收以 OpenSpec 变更为准：[proposal](../../../openspec/changes/add-accountsvc-email-templates/proposal.md)、[design](../../../openspec/changes/add-accountsvc-email-templates/design.md)、[tasks](../../../openspec/changes/add-accountsvc-email-templates/tasks.md)。本计划只记录实施顺序与执行方式，不重复规格。

**目标：** accountsvc 通过 `ACCOUNTSVC_SMTP_TEMPLATES_FILE` 读取四种用途的纯文本邮件模板；未配置时行为逐字不变。

**来源：** ai-food 会话转来的 R7 需求，设计经用户评审：内置文案不变、解析器放 internal、正文上限 4096 个码位、启动时记录 INFO 日志、示例文件纳入测试。

**执行方式：** 本会话按顺序直接实施（TDD），完成后由一个独立审查子代理审查整个分支。各任务共享 `config.go` 与 `run.go`，依赖紧密，不适合并行拆分。

## 顺序

1. `internal/mailtext`：先写字符规则测试，再实现；`smtp.ValidateBrand` 改为调用共享规则，现有品牌测试作为回归。
2. `internal/accountsvc/emailtemplates`：先写解析/渲染矩阵测试（含 64/65、4096/4097、8/9 边界，并用哨兵字符串断言错误不回显内容），再实现。JSON 严格解码复用 `internal/boundedjson`（重复键、尾随内容、非法 Unicode、大小与深度限制）。
3. accountsvc 装配：
   - 把资料 Schema 的有界文件读取提取为 `readConfigFile`；
   - `LoadConfig` 在 SMTP 品牌校验之后加载模板；
   - `run.go` 二选一注入渲染器，并在监听启动日志之后调用 `logEmailTemplates`。
4. 示例文件 `deploy/accountsvc/email-templates.example.json`，以及 `TestExampleFile`。
5. 文档、部署模板、必需测试清单。
6. 验证：Linux 镜像中运行 `scripts/verify-accountsvc.sh`；`go test -race ./...` 加 `testgate scripts/required-tests.txt`；`openspec validate --all --strict`；分支审查。
