# 首次发布检查

发布 module：github.com/bbxx111/accountkit；远程：https://github.com/bbxx111/accountkit.git。

- [x] extract-auth-server 所有必需任务完成，源文件清单、路由、默认配置和公开接口已对照。
- [x] GOWORK=off 独立构建和独立临时宿主编译通过。
- [x] Go 1.26.5 race 全套测试通过，必需数据库测试无 skip。
- [x] sqlc 1.31.1 生成结果一致。
- [x] harden-migration-safety 所有 P0 任务完成：升级、失败、并发、防误清库、历史 SQL 冻结、备份恢复。
- [ ] 产品侧确认发送器、微信/Apple 配置、管理员身份验证、可信代理 IP、schema/Redis/密钥隔离。
- [ ] 产品侧在自己的预发布环境验证备份恢复和升级窗口。

本地验证不等于 GitHub Actions 已在线运行，也不等于宿主已经完成接入。实际 tag、push、发布和产品部署另行执行。测试数据库权限应限定到一次性数据库；生产密钥不进入示例或测试。
包级验证已于 2026-10-03 通过：286 个常规顶层测试通过，恢复演练三个阶段通过。详细证据与限制见 [恢复验证记录](recovery-verification.md)。上面两项产品验收仍是实际生产接入的前置条件。

## 可选 accountsvc 发布

accountsvc 与库使用同一版本源码；发布服务不改变嵌入式接入方式。运行配置及集成边界见 [服务手册](accountsvc.md)。发布服务前还需确认：

- [ ] 在目标环境配置双监听器 TLS、独立内省客户端秘密及网络访问范围。
- [ ] 使用实际 SMTP 提供方验证认证、证书、投递及收件；测试邮件捕获不代表最终送达。
- [ ] 若启用管理面，使用实际 OIDC 提供方验证 RS256 access token、管理员 API audience、角色映射与换钥。
- [ ] 明确业务数据匿名化责任；不与依赖业务回调的宿主混跑同一实例的维护任务。
- [ ] 在目标环境验证启动迁移、探针、SIGTERM、终止宽限和备份恢复。

本地服务验收必须执行 `scripts/verify-accountsvc.sh`，使用一次性 PostgreSQL/Redis 和隔离 SMTP/OIDC fixtures。缺依赖或必需测试 skip 不能视为通过；仍须执行原有库验证和恢复入口。本次实际结果见 [accountsvc 验收记录](accountsvc-verification.md)，以上部署验收尚未执行。
