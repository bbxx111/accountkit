# Verification

本文记录 `add-accountsvc-sms-and-email-brand` 在分支 `feat/accountsvc-sms-and-email-brand` 上实际执行的检查（2026-10-27）。原始输出保存在被忽略的 `.test-output/verify/sms-*`、`.test-output/tests.jsonl` 与 `.test-output/accountsvc-tests.jsonl` 中。

## 执行方式

- 按[实现计划](../../../../docs/superpowers/plans/2026-10-27-add-accountsvc-sms-and-email-brand.md)在本会话内顺序实施，未使用逐任务子代理：号码支持能力 → 阿里云发送器 → accountsvc 装配 → 邮件品牌之间依赖紧密。
- 每组都先写失败测试，再写实现（TDD）。
- 全部实现完成后，由一个独立审查代理复核整条分支。
- 依赖使用本任务专门创建的一次性 Docker 资源：PostgreSQL 17.4 容器 `accountkit-verify-pg-sms`（合成凭据，只绑定回环端口；库 `accountkit_strict`、`accountkit_recsrc`、`accountkit_recdst`）和 Redis 7 容器 `accountkit-verify-redis-sms`。Linux 验证镜像由 `tests/Dockerfile` 构建（golang:1.26.5，PostgreSQL 17.11 客户端）。
- 结束后上述容器、网络、卷与镜像均已删除；没有使用本机其他项目的容器。

## 签名固定向量

阿里云官方文档“V2 版本 RPC 风格 API 请求的签名机制”（https://help.aliyun.com/zh/sdk/product-overview/rpc-mechanism）给出了完整示例，结果为 Signature `9NaGiOspFP5UPcwX8Iwt2YJXXuk=`。

- 该值先用独立的 Python 实现在本地复算，结果一致；
- 之后作为 `TestSignOfficialVector` 的期望值。

## 已运行检查

| 检查 | 结果 |
|---|---|
| `GOWORK=off go build ./...`、`go vet ./...` | 通过 |
| `go test -count=1 -json ./...`（Windows，连接真实数据库） | 1099 通过，0 失败，15 skip。skip 包括需在 Linux 上运行的 14 个进程级测试（含新增的 `TestServiceIntegrationAliyunSMS`），以及预期 skip 的 `TestRecoveryFixture`。审查修复后，受影响的数据库测试已复跑并通过。 |
| `bash scripts/check-generated.sh`（sqlc v1.31.1） | 退出码 0，生成结果无差异 |
| `bash scripts/check-migrations.sh` | 通过 |
| `openspec validate --all --strict` | 38 项全部通过 |
| `node scripts/check-openapi.cjs` | 通过：enduser 24 个、admin 11 个操作 |
| `bash scripts/verify.sh`（Linux 镜像，最终代码） | 退出码 0：race 下 1115 通过、无数据竞争，必需测试门禁通过，pg_dump/pg_restore 恢复演练三阶段通过。2 个 skip：预期 skip 的 `TestRecoveryFixture`，以及 `TestProfileAttributesUnreadableFile`（容器以 root 运行，无法构造不可读文件）。 |
| `bash scripts/verify-accountsvc.sh`（Linux 镜像，最终代码） | 退出码 0：race 下 118 通过，必需测试门禁通过。 |

`TestServiceIntegrationAliyunSMS` 在两套门禁中均实际执行并通过，验证了以下行为：

- 生产模式进程使用指向本地 HTTPS 假服务的阿里云配置；
- 品牌邮件标题为“食方登录验证码”，正文以“【食方】您正在进行登录。”开头；
- PHONE 发码与登录成功，内省返回 active；
- `+44` 号码返回 400 `TARGET_NOT_SUPPORTED`，且未请求服务商；
- 服务商频控返回 503；
- 进程输出不含 Secret、手机号或验证码。

新增必需测试已写入 `scripts/required-tests.txt` 与 `scripts/accountsvc-required-tests.txt`。

## 审查结论

独立审查代理复核整条分支：无 Critical 问题。确认以下内容正确：签名方式（与阿里云 dysms 官方 SignatureHelper 示例一致的表单 POST）、隐私处理、四种发码流程中“先预检、后占额度”的顺序，以及 accountsvc 配置矩阵。

以下意见已全部修复，提交为 `fc5f7b1`：

**Important**

1. 登录补全拒绝审计落入 `INTERNAL`。改为 `TARGET_NOT_SUPPORTED`，并在测试中断言两条审计事件。
2. 服务商错误码不可见：发码流程把投递错误收敛为通用错误，非 200 响应体也没有解析。处理如下：
   - 阿里云发送器新增可选 `Logger`，以 Warn 记录阶段、HTTP 状态、错误码与 RequestId，accountsvc 注入服务日志器；
   - 同时解析非 200 网关错误中的 `Code` 与 `RequestId`；
   - 不在库的发码流程中记录宿主发送器的错误文本，因为宿主错误可能含敏感信息。
3. Compose 模板没有把新变量传入容器。已添加 `${VAR:-}` 透传，身份策略也改为可覆盖（默认值仍为 EMAIL）。

**Minor**

1. 固定电话被视为支持的号码。改为只接受 1 开头的手机号。
2. 嵌入式宿主的非法品牌要到渲染时才失败。改为在 `smtp.NewWithRenderer` 构造时校验。
3. 品牌另拒绝格式字符与行/段分隔符，防止标题伪装。
4. README 注明：装饰器须转发 `SupportsTarget` 与 `Enabled`。
5. 设置了 provider 时，拼写错误的 `SMS_ALIYUN_*` 变量被拒绝。

以上修复均有对应测试，design 与 specs 已同步。

## 设计偏离

- 任务 5.1 原计划在 OpenAPI 中列出 `TARGET_NOT_SUPPORTED`。现有 OpenAPI 使用通用的 400 响应，不逐端点枚举 reason，因此改为在四个发码操作的描述中说明，没有修改结构。

## 未验证项

- **真实阿里云发送**：需要真实 AccessKey、已审核的签名与模板，须由部署方（ai-food）在接入时验收。本地只验证了签名算法（官方向量）、请求参数与错误处理。
