# 版本发布与产品上线检查

发布 module：github.com/bbxx111/accountkit；远程：https://github.com/bbxx111/accountkit.git。

## v0.1.0 发布范围

初版面向自有产品接入，交付 Go module 和可选 accountsvc 源码。库集成无需运行 accountsvc。发布说明见 [v0.1.0](releases/v0.1.0.md)；tag 发布、GitHub Release、服务镜像/二进制分发和产品部署分别执行。`v0.1.0` 已发布，下面发布阶段的勾选仅对应该版本；后续版本须重新验证自己的候选提交。

本检查表列出三种状态：历史实现验证、待发布版本的门禁、各产品的上线验收。产品环境验收可以在产品接入过程中完成；库版本发布后，产品仍须完成对应上线条件。当前没有开源 LICENSE，本次面向自有产品的发布不替外部使用方定义复用许可；后续开放外部使用时再明确许可方式。

## 历史实现验证

完整功能提取、独立构建、临时宿主消费、race、必需数据库测试、sqlc 一致性、迁移安全和备份恢复已有本地通过记录。最近的[终端用户入口验收](../openspec/changes/archive/2026-10-06-rename-consumer-to-enduser/verification.md)记录了 410 个库顶层测试、338 项库必需测试、65 个服务顶层测试及恢复三阶段通过；P0 迁移安全依据见[恢复验证记录](../openspec/changes/archive/2026-10-03-harden-migration-safety/verification.md)。

这些记录用于追溯已验证源码和限制，不能自动勾选下方待发布版本的门禁，也不等于在线 CI 或产品验收通过。

## 发布前门禁

- [x] 在 `main` 上确定最终候选提交 SHA，确认工作区干净且 `v0.1.0` 在本地与远端均不存在；记录候选 SHA 和 CI 链接。
- [x] 最终候选提交的 CI 完成且全部通过：`check-generated.sh`、`verify.sh` 和 `verify-accountsvc.sh`。核对完整历史/tags、独立构建、vet、race、全部必需用例及真实恢复结果；缺依赖或必需测试 skip 不视为通过。普通套件的 `TestRecoveryFixture` 允许按既有规则 skip，但恢复脚本的 seed、verify、source-unchanged 必须通过。
- [x] 核对发布说明中的功能范围、Go/存储要求、当前 Go 入口、accountsvc 限制、宿主责任和产品环境未验证项；声明面向自有产品，未将源码公开可见描述成已经授予开源许可。
- [x] 发布说明和其余待交付文件均包含在最终候选提交内；后续改动产生新候选提交时，重新核对该提交对应的门禁。

本地复核使用[开发与验证入口](development.md)，测试数据库权限限定到一次性数据库；生产密钥不进入示例或测试。各次变更的实际结果保存在对应 `openspec/changes/.../verification.md`，原始日志和恢复材料保留于 ignored `.test-output/`。

## 发布动作与消费确认

上述发布前门禁通过后，按明确授权执行版本发布。Go 版本使用完整的 `v0.1.0`，tag 指向已验证的最终候选提交；实际发布流程见 [Go 模块发布说明](https://go.dev/doc/modules/publishing)。

- [x] 创建并推送指向最终候选 SHA 的 `v0.1.0` tag；核对远端 tag 解析到同一提交。发布后不移动、覆盖或重建该 tag；修复通过新版本交付。
- [x] tag 推送后，在独立临时宿主中以 `GOWORK=off` 执行 `go get github.com/bbxx111/accountkit@v0.1.0`，不使用本地 replace；分别编译根 `EndUserHandler` 与直接 `httpapi/enduser` 消费，并核对 `go list -m -json` 返回的版本。临时宿主和输出只放 ignored `.test-output/`。
- [x] 远端消费确认后，将发布说明的状态改为已发布，补充 tag/提交、候选 CI 和消费确认依据。需要 GitHub Release 时使用同一份说明；无需另维护一套版本内容。

推送 tag 后版本已可供 Go 使用，因此消费确认属于发布后的检查。若发现缺陷，保留版本和证据，通过修复版本处理；不能回写已发布 tag 来掩盖问题。

## 宿主账号契约新版本门禁

以下条目用于 `add-host-account-contracts` 的 `v0.2.0` 发布，历史 `v0.1.0` 勾选不能替代。发布授权已取得，说明见 [v0.2.0](releases/v0.2.0.md)，本轮实际执行结果与最终 SHA/CI 集中在[变更验收记录](../openspec/changes/add-host-account-contracts/verification.md)。

- [x] PHONE/EMAIL 的登录、绑定、重新认证和换绑全部要求 `code_id`，Go/HTTP 调用者和 accountsvc 同步升级，旧无标识验证旁路已移除。
- [x] 核对默认每轮 5 次、跨用途/轮次 10 次/15m 预算，Redis 原子消费、重发/清理竞争、多版本窗口和别名去重用例实际通过。
- [x] 批量资料只有公开字段，两个注销入口共用 `BeforeDelete`，ACTIVE 账号锁序、回调错误/panic、导入冲突和同事务回滚在真实 PostgreSQL 执行。
- [x] 完整库及服务 build/vet/race、sqlc 生成、迁移历史、必需测试门禁和恢复 seed/verify/source-unchanged 对最终候选代码通过，未以 skip 代替；main 候选 e19af85 的在线 verify #37814180599 全部成功。
- [x] 保存 Go/HTTP/Redis 升级与回退步骤：同实例停止旧验证码写/校验、同步客户端、不清空整个 Redis；HMAC 退役等待失败预算窗口。
- [x] 单独取得新固定版本发布授权，不修改 `v0.1.0`；v0.2.0 tag 固定到 e19af8542f099f14103627140c0fe23f14c99b7e，两个独立宿主从远端无 replace 消费通过。

## 嵌入式产品上线

- [ ] 产品侧按启用能力确认发送器、微信/Apple 配置、管理员身份验证、可信代理 IP、schema/Redis/issuer/audience/密钥隔离，以及业务数据匿名化回调。
- [ ] 按[网关接入契约](gateway-integration.md)落实路由保护、TLS、可信 IP、入口频率/并发限制和日志脱敏。
- [ ] 产品侧在自己的预发布环境验证实际认证链路、备份恢复、升级窗口和回退安排。

以上是各产品生产上线的前置条件，发布库版本不自动完成这些验收。实际发送、第三方身份提供方和设备联调由宿主负责。

## accountsvc 产品上线

accountsvc 与库使用同一版本源码；运行配置见[服务手册](accountsvc.md)，网关统一契约见[网关接入手册](gateway-integration.md)，旧部署成套切换与回退见[兼容说明](compatibility.md#服务单监听与传输迁移)。运行该服务的产品在目标环境确认：

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

初始服务历史结果见[accountsvc 验收记录](../openspec/changes/archive/2026-10-03-add-accountsvc/verification.md)，传输结果见[传输验收记录](../openspec/changes/archive/2026-10-06-simplify-accountsvc-transport/verification.md)。本地代理、SMTP/OIDC fixtures 不等于真实网关/网格或服务商验收，以上实际部署项保持待验收。
