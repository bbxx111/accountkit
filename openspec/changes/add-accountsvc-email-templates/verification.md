# add-accountsvc-email-templates 验收记录

日期：2026-10-28。分支 `feat/accountsvc-email-templates`，基于 develop `27ed492`。实施计划见 [plan](../../../docs/superpowers/plans/2026-10-28-add-accountsvc-email-templates.md)。

## 执行方式

- 本会话按顺序直接实施，采用 TDD：先写测试，确认失败，再实现。
- 实施完成后，由一个独立审查子代理审查整个分支。
- 需求来自 ai-food 会话转来的 R7。设计经用户评审后确定：
  - 内置文案不变；
  - 解析器放在 internal；
  - 正文上限 4096 个码位；
  - 启动时记录 INFO 日志；
  - 示例文件纳入测试。

## 已运行的检查

Linux 验证镜像基于 `tests/Dockerfile`（golang:1.26.5）。测试用的是一次性的 PostgreSQL 17.4 和 Redis 7 容器，用完即清理。

| 检查 | 结果 |
|---|---|
| `bash scripts/verify-accountsvc.sh`（race、真实进程、TLS、伪 SMTP、必需测试门禁） | 通过：776 个测试通过，0 个失败。新增的 `TestServiceIntegrationEmailTemplates` 在首轮失败，原因是断言写错：夹具的 quoted-printable 解码会把 CRLF 还原为 LF。修正断言后通过，实现未改动。 |
| `go build ./...`、`go vet ./...` | 通过 |
| `go test -race -json -count=1 ./...`（`SERVER_TEST_DB_DSN` 和 `ACCOUNTSVC_TEST_REDIS_URL` 均已设置） | 通过：3682 个测试通过，0 个失败 |
| `go run ./internal/testgate scripts/required-tests.txt` | 通过。第一次运行时漏设 Redis 变量，导致 `TestAlipayHTTPLoginContract` 被跳过、门禁失败；补设后重跑通过。 |
| gofmt | 无差异 |
| `openspec validate --all --strict` | 41 项全部通过 |
| 审查后补充的 `ValidateBrand` 回归用例：`go test ./user/sender/smtp -run TestLegacyChineseRendererBrand` | 通过 |

## 覆盖要点

- **解析器**：
  - 结构方面：version、四种用途是否齐全、未知字段、重复键、尾随内容、null、非法 UTF-8 和孤立代理项；
  - 占位符方面：未知、单独的花括号、空名称、未闭合、转义、单次展开；
  - 字符方面：CR、LF、TAB、U+202E、U+200B、U+2028、U+2029、首尾空白；
  - 边界值：64/65、4096/4097、8/9 个占位符、64 KiB；
  - 所有错误信息都用哨兵字符串断言不回显内容。
- **渲染**：
  - 四种用途逐字比对；
  - TTL 向上取整（1s、59s、60s、61s、5m、90m）；
  - ctx 取消、非法 CodeMessage、零值和 nil 都返回 `ErrUnavailable`；
  - race 下并发渲染。
- **配置**：
  - 未设置时使用内置渲染器；
  - 合法文件可用，摘要为 12 位十六进制；
  - 品牌为空却引用 `{brand}` 时报错；
  - 文件不存在、是目录、为空、超限、JSON 非法、标题含 `{code}`、用途不全时都报错，错误只含变量名，不含路径或内容；
  - migrate 时，或 EMAIL 禁用的 serve，都会忽略非法路径。
- **启动日志**：
  - `built-in` 和 `custom` 两种情况，后者含 version 与 sha256；
  - EMAIL 禁用时不记录；
  - 不含模板原文、品牌或路径。
  - 集成测试把模板原文和路径登记为不得出现在进程输出中的秘密，并做了断言。
- **示例文件**：`TestExampleFile` 直接加载 `deploy/accountsvc/email-templates.example.json`，渲染四种用途；断言补全用途的标题为“食方登录补全验证码”，且品牌为空时会拒绝。
- **回归**：
  - `ValidateBrand` 改用共享规则后，原有品牌测试全部通过，并新增 C1 控制字符、U+2029、非法 UTF-8 三个用例；
  - 资料 Schema loader 提取为共用函数后，错误文本逐字不变（审查已核对）。

## 审查结论

独立审查子代理认为可以合并：没有 Critical 或 Important 问题。

已核实：
- 标题注入防护；
- 单次展开；
- 错误和日志不回显内容；
- 编译器在 `{{{`、`}}}`、`{a{b}`、多字节字符旁花括号等边界上的行为；
- 实现与规格一致；
- 两处回归点。

对 Minor 意见的处理：
1. 补写本 verification.md。
2. `ValidateBrand` 拒绝表补了 C1、U+2029、非法 UTF-8 三个用例。
3. design 中的类型名统一为 `emailtemplates.Renderer`；把“不含占位符名”改为“不回显文件中出现的占位符名”。
4. 文档、规格、proposal、tasks 统一写明“subject 与 text 各自最多 8 个占位符”。
5. 文档示例注明正文已省略、不能直接使用，并指向完整示例文件；注明文件须为不带 BOM 的 UTF-8。
6. 带 BOM 的文件和嵌套过深的未知字段都只报 `invalid JSON`，诊断不够精确。按审查建议接受，不改代码。

## 未验证项

- 没有在 Windows 上运行 race：本机缺少 CGO 工具链，race 在 Linux 镜像中运行。
- 没有运行 `scripts/check-migrations.sh` 和恢复演练（`verify-recovery.sh`）：本次变更不涉及迁移、SQL 或数据结构，这两项留到发布前的完整验证。
- 没有向真实邮件服务商投递；只用伪 SMTP 验证了 MIME 编码和内容。
- 线上 CI 尚未运行，分支还未推送。
