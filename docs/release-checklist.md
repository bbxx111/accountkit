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
包级验证的历史证据与限制见 [恢复验证记录](../openspec/changes/archive/2026-10-03-harden-migration-safety/verification.md)，各次变更的实际结果保存在其目录下的 `verification.md`。上面两项产品验收仍是实际生产接入的前置条件；发布前应针对候选版本重新执行门禁。

## 可选 accountsvc 发布

accountsvc 与库使用同一版本源码；发布服务不改变嵌入式接入方式。运行配置见[服务手册](accountsvc.md)，网关统一契约见[网关接入手册](gateway-integration.md)，旧部署成套切换与回退见[兼容说明](compatibility.md#服务单监听与传输迁移)。发布服务前还需在目标环境确认：

- [ ] 删除旧 INTERNAL_ADDR/内部端口，更新统一内省和探针地址；保存旧二进制、配置、证书、调用方及网关策略的成套回退材料。
- [ ] 配置明确公网路由范围和独立管理策略，公网内省/探针被拒绝且不进入后端；授权内部调用可访问，未授权直连不能绕过。
- [ ] 选择服务HTTPS、网关HTTPS回源或受控外部TLS方式，验证全部链路证书/信任边界；显式false时移除HTTP证书项，并核对调用方AllowHTTP与探针协议。
- [ ] 配置独立内省客户端秘密及重叠轮换，保留服务Basic、消费者/管理员验证及角色/scope/资源归属检查。
- [ ] 核对可信代理CIDR与实际链路，验证伪造转发头不能改变额度和审计IP，请求ID关联正确。
- [ ] 按登录/发码、token/revoke、管理和内省记录频率、突发及在途并发策略、负责人、身份可信前提、每副本/集群计数范围及故障策略；分别压力验收，超限429/Retry-After可识别。
- [ ] 验证认证/设备/内容类型/请求ID和challenge/Retry-After/no-store透传，认证与写操作不自动重试；日志、追踪、错误采集和审计不包含完整凭据/隐私。
- [ ] 使用实际 SMTP 提供方验证认证、证书、投递及收件；测试邮件捕获不代表最终送达。
- [ ] 若启用管理面，使用实际 OIDC 提供方验证 RS256 access token、管理员 API audience、角色映射与换钥。
- [ ] 明确业务数据匿名化责任；不与依赖业务回调的宿主混跑同一实例的维护任务。
- [ ] 在目标环境验证启动迁移、同址探针协议、摘流/连接排空、SIGTERM、终止宽限和备份恢复。

本地服务验收必须执行 `scripts/verify-accountsvc.sh`，使用一次性 PostgreSQL/Redis 和隔离 SMTP/OIDC fixtures。缺依赖或必需测试 skip 不能视为通过；仍须执行原有库验证和恢复入口。初始服务历史结果见[accountsvc 验收记录](../openspec/changes/archive/2026-10-03-add-accountsvc/verification.md)，本次结果见[传输验收记录](../openspec/changes/simplify-accountsvc-transport/verification.md)。本地代理 fixture 不等于真实网关/网格验收，以上实际部署项全部保持待验收。
