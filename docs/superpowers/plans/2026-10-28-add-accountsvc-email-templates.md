# accountsvc 邮件模板清单实现计划

> 需求与验收以 OpenSpec 变更为准：[proposal](../../../openspec/changes/archive/2026-10-28-add-accountsvc-email-templates/proposal.md)、[design](../../../openspec/changes/archive/2026-10-28-add-accountsvc-email-templates/design.md)、[tasks](../../../openspec/changes/archive/2026-10-28-add-accountsvc-email-templates/tasks.md)。本计划只记录实施顺序与执行方式，不重复规格。

**目标：** accountsvc 通过 `ACCOUNTSVC_SMTP_TEMPLATES_FILE` 读取一份多语言清单，用内置布局渲染 HTML 加纯文本的验证码邮件，并按请求的 `Accept-Language` 选择语言；未配置时行为逐字不变。

**来源：** ai-food 会话转来的 R7（可配置文案）与 R8（多语言）。设计经用户多轮评审后收敛：内置布局、清单只配文字、品牌只在清单、远程 HTTPS Logo、`check` 预览命令、简繁英三种语言。

**执行方式：** 本会话按顺序直接实施（TDD），完成后由一个独立审查子代理审查整个分支。各任务共享 `config.go` 与 `run.go`，依赖紧密，不适合并行拆分。

## 顺序

1. `internal/mailtext` 与 `smtp.ValidateBrand` 共享规则（首版已完成）。
2. `internal/accountsvc/emailtemplates`：先写解析/渲染/匹配测试，再实现；内置布局用 `embed`；严格 JSON 复用 `internal/boundedjson`；语言匹配用 `golang.org/x/text/language`。
3. accountsvc 装配：`LoadConfig` 加载清单并与品牌互斥；多语言时给消费者路由套 `Accept-Language` 中间件；启动 INFO；`email-templates check` 子命令。
4. 样例清单 `deploy/accountsvc/email-templates.example.json` 与 `TestExampleManifest`。
5. 文档、部署模板、必需测试清单。
6. 验证：Linux 镜像中运行 `scripts/verify-accountsvc.sh`；`go test -race ./...` 加 `testgate scripts/required-tests.txt`；`openspec validate --all --strict`；分支审查。
