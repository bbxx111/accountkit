# accountsvc 本地验收记录

日期：2026-10-03。对应 [add-accountsvc](tasks.md)。本记录只覆盖本地运行结果，不表示 GitHub Actions 已在线通过或已发布服务。

## 环境

- Go 1.26.5；Linux 验证镜像提供 GCC 14、PostgreSQL 17.11 客户端。
- 本次创建的一次性 PostgreSQL 17、Redis 7；普通测试数据库与恢复源/目标数据库分离。
- 服务进程测试使用隔离 TLS SMTP、HTTPS OIDC/JWKS fixtures；开发 Compose 使用 Mailpit v1.31.3。
- 数据均为合成数据；随机配置与原始日志保存在被忽略的 `.test-output/`，不提交凭据、备份或邮件正文。

## 已执行检查

| 检查 | 结果 |
|---|---|
| `bash scripts/check-generated.sh` | sqlc 生成结果一致 |
| `bash scripts/check-migrations.sh` | 固定基线及可达发布历史校验通过 |
| `bash scripts/verify.sh` | 独立构建、vet、Linux race、原有必需测试门禁及真实备份恢复通过 |
| 完整库套件 | 344 个顶层测试通过；5 项服务进程测试交给独立严格入口；恢复 fixture 在恢复脚本中执行 |
| `bash scripts/verify-accountsvc.sh` | 5 个包的 race 全部通过；54 个顶层测试通过，0 fail/skip；实际服务子进程也以 `-race` 构建 |
| 服务五项必需测试 | 邮件生命周期、管理员生命周期、依赖故障、启动安全、SIGTERM 全部通过 |
| 备份恢复 | seed、verify、source-unchanged 三阶段通过；校验迁移版本、四表快照、解密、access/refresh 与源库保留 |
| 独立临时宿主 | 从临时 module 引用本地 accountkit，编译嵌入式示例通过；根库依赖图不含服务内部包 |
| Compose | 配置解析、镜像构建、启动、就绪、邮件捕获→登录→内省、正常停止退出0通过 |
| 镜像边界 | 用户65532:65532；公开/内部端口仅绑定宿主回环；嵌套合成密钥和备份未进入 Docker 构建上下文 |
| 门禁负例 | 缺数据库或 Redis 配置明确失败；服务必需测试 skip 被 testgate 拒绝 |
| 文档 | 修改文档的本地路径引用及差异格式检查通过 |
| `openspec validate --all --strict` | 4 个变更通过，0 失败 |

完整库入口保留原有行为：普通套件中的 `TestRecoveryFixture` 预期跳过，由恢复脚本分别强制执行三个阶段。服务进程测试缺 Redis 配置时可在普通套件跳过；发布服务必须另行执行严格服务入口，不能只引用上表的库套件结果。

服务测试验证了生产 TLS 配置、邮箱登录→刷新宽限→内省→重新认证→注销、重启后令牌兼容、短信禁用、管理员关闭、身份边界、冻结/解冻、敏感查看与审计脱敏。故障测试验证 Redis 中断时 readyz=503 与吊销查询 fail-open 并存，SMTP 故障保留额度且不改变整体 readiness；启动测试覆盖并发/重复迁移、dirty 和未知密钥阻断；实际 SIGTERM 验证正常排空及超出 HTTP 排空预算的非零退出。

首次服务 fixture 误用了库配置变量名，已改为 `ACCOUNTKIT_AUTH_SCHEMA`/`ACCOUNTKIT_AUTH_KEY_PREFIX` 并增加实际配置解析预检；此前合成数据只写入本轮专用一次性数据库/Redis，未操作其他项目资源。修正后重新执行全部服务测试及严格 race 门禁。

## 审查结论

发送器、管理员验证、消费者内省、运行层、真实服务测试及部署文件均经过非作者审查，整分支复核未留待处理问题。审查发现的共享 JWKS 刷新被单个请求取消影响的问题已修复并补并发回归；Docker 构建上下文的嵌套密钥排除规则已修正并用实际构建验证。公开消费者消息体超限保持既有400错误语义，并以真实 handler 的普通/chunked 请求验证没有发码副作用。

受会话并发席位限制，复用了现有代理进行不同任务的交叉审查；没有用作者自审代替独立任务审查。整分支审查对审查者自己实现的部分采用另一位审查者的结果。没有更改数据库迁移、冻结基线、生成数据库代码、Go module 依赖或消费者令牌格式。

## 部署验收边界

尚未使用真实 SMTP 服务商验证最终收件，未与实际 OIDC 管理员提供方联调，也未执行生产网络、证书、密钥托管或发布操作。这些项目由 [发布检查表](../../../../docs/release-checklist.md) 跟踪。

消费者 Redis 吊销查询仍沿用库的 fail-open 语义；SMTP 接受不代表最终送达；服务不执行使用方业务数据匿名化。具体接入责任见 [服务手册](../../../../docs/accountsvc.md)。
