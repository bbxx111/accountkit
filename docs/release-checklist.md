# 版本发布与产品上线检查

发布 module：github.com/bbxx111/accountkit；远程：https://github.com/bbxx111/accountkit.git。

## 当前状态

当前处于 v1.0.0（候选）阶段，见[发布说明](releases/v1.0.0.md)和[发布准备](../openspec/changes/prepare-v1-0-0-release/proposal.md)。开发期版本的说明与证据索引见 [开发期历史](releases/pre-1.0-history.md)。

### v1.0.0 专属门禁

用户于 2026-10-31 明确指定先发布 v1.0.0，发布及远端消费完成后再删除九个 v0 tag 和现存对应 Release。本次仅调整下方“发布前远端不存在 v0 tag”的执行顺序；同名正式 tag 不存在、最终候选 CI、完整验证及不移动 tag 的要求保持。完成状态与精确 SHA/CI/消费证据统一维护在[本次验收记录](../openspec/changes/prepare-v1-0-0-release/verification.md)，任务进度见[任务清单](../openspec/changes/prepare-v1-0-0-release/tasks.md)。

- 初始 0001 up/down 哈希保持，首次正式版本不提供开发期数据库原地升级；Go 调用切换使用[兼容指南](compatibility.md#v100-调用者须知)。
- 公开 API、HTTP/配置、指标/审计及依赖沿用已归档整合结果，本发布只维护文档和外部版本对象；不新增功能或生产迁移。
- 准备快照完整 Linux 验证、最终 main SHA 的在线 CI、annotated tag 解析、两个无 replace 远端消费模块必须分别留证，不能互相替代。
- 真实提供方、网关、产品环境及 Windows 权限语义仍按已有边界说明，不因正式版本发布自动算验收。

本检查表区分三类内容：可复用的发布前门禁、发布动作与消费确认、各产品的上线验收。产品环境验收可以在产品接入过程中完成；库版本发布后，产品仍须完成对应上线条件。当前没有开源 LICENSE，面向自有产品的发布不替外部使用方定义复用许可；后续开放外部使用时再明确许可方式。

## 发布前门禁

以下为不带版本号的通用检查项；每个版本发布时在其准备变更中逐项勾选并保存证据，历史版本的结论不能替代当前候选。

- [ ] 本版本功能变更已归档并合入 develop；盘点公开 Go API、HTTP/配置、指标/审计、依赖与当前 0001 差异。
- [ ] 发布说明与升级/回退边界完成独立审查，核对功能范围、Go/存储要求、accountsvc 限制、宿主责任和产品环境未验证项。
- [ ] 准备快照在 `tests/Dockerfile` 构建的 Linux 验证镜像中通过：`openspec validate --all --strict`、`bash scripts/check-generated.sh`、`bash scripts/check-migrations.sh`（需完整历史与 tags）、`node scripts/check-openapi.cjs`、`bash scripts/verify.sh`（完整库 race 与必需门禁）、`bash scripts/verify-accountsvc.sh`（服务 race 与进程信号）、`bash scripts/verify-recovery.sh` 的 seed/verify/source-unchanged 三阶段；缺依赖或必需测试 skip 不视为通过（普通套件的 `TestRecoveryFixture` 按既有规则 skip），结果写入该版本准备变更的 `verification.md`。
- [ ] 以 `--no-ff` 合入 develop，再以独立 merge commit 合入 main 并推送；确认工作区干净，记录最终候选 SHA。
- [ ] 最终候选的在线 CI（`.github/workflows/ci.yml`）全部通过，包括 `check-generated.sh`、`verify.sh` 与 `verify-accountsvc.sh` 步骤，且 `head_sha` 与候选 SHA 精确匹配；不以准备快照或旧候选的结果替代。
- [ ] 远端不存在同名 tag，也不存在低于 v1.0.0 的 tag（开发期 tag 计划在 v1.0.0 发布前删除）；人工核对 `git ls-remote --tags origin`。
- [ ] 发布说明及其余待交付文件均包含在最终候选提交内；后续改动产生新候选提交时，重新核对该提交对应的门禁。

本地复核使用[开发与验证入口](development.md)，测试数据库权限限定到一次性数据库；生产密钥不进入示例或测试。各次变更的实际结果保存在对应 `openspec/changes/.../verification.md`，原始日志和恢复材料保留于 ignored `.test-output/`。

## 发布动作与消费确认

**状态措辞约定**：发布准备到打 tag 之前，发布说明、README、兼容/功能/集成/OpenAPI 文档统一写“候选”（如“vX.Y.Z（候选）”、“状态：发布候选”），不写“准备中”，并写明 tag 将固定包含该文的提交及可预知的 Release 链接；tag 内容不会也不需要包含自己的 SHA、CI 与消费证据。tag 与 Release 完成、远端消费验证通过后，以补记提交把状态改为“已发布”并写入 tag 对象、提交 SHA、CI 与消费结果，合入 develop 与 main；历史 tag 不移动。不为改一个状态词在 CI 通过后追加提交再打 tag。

上述发布前门禁通过后，按明确授权执行版本发布；准备授权不扩展为合并 main、推送、tag 或 Release。Go 版本使用完整的 `vX.Y.Z`，实际发布流程见 [Go 模块发布说明](https://go.dev/doc/modules/publishing)。

- [ ] 创建并推送指向最终候选 SHA 的 annotated tag；核对远端 tag 解析到同一提交，并发布 GitHub Release（使用同一份发布说明，不另维护一套内容）。发布后不移动、覆盖或重建该 tag；修复通过新版本交付。
- [ ] tag 推送后，在两个独立临时宿主中以 `GOWORK=off`、无本地 replace 执行 `go get github.com/bbxx111/accountkit@vX.Y.Z`；分别编译根 `EndUserHandler` 与直接 `httpapi/enduser` 消费，并核对 `go list -m -json` 返回的版本、初始 SQL 哈希与本版本新增公开符号。临时宿主和输出只放 ignored `.test-output/`。
- [ ] 远端消费确认后，以补记提交把状态改为已发布，写入 tag/提交、候选 CI 和消费确认依据，合入 develop 与 main；归档发布准备变更按单独明确请求执行。

推送 tag 后版本已可供 Go 使用，因此消费确认属于发布后的检查。若发现缺陷，保留版本和证据，通过修复版本处理；不能回写已发布 tag 来掩盖问题。

## 历史发布与产品责任

开发期各版本的范围、限制与证据索引见 [开发期历史](releases/pre-1.0-history.md)。归档不代表产品上线验收完成；归档任务中仍未勾选的集成方真实联调（如[身份策略与登录补全任务 8.5](../openspec/changes/archive/2026-10-15-add-identity-policy-sign-in-completion/tasks.md) 的微信/Apple 联调）继续保留未验证状态。

以下工作由集成方在自己的环境负责，库侧提供契约、测试工具并配合排查实现问题。应在各产品任务中跟踪负责人、目标环境、采用版本及验收证据；不作为库内尚未实现的功能重复排期。

| 集成方工作 | 验收范围 |
|---|---|
| 微信/Apple 真实联调 | 自有测试应用与配置下的既有登录、普通绑定、首次手机/邮箱补全；对应任务 8.5 |
| 产品接入与网关 | 实例隔离、管理员认证、业务匿名化回调、TLS、可信代理、路由保护、限流及日志脱敏 |
| 实际服务商 | 按启用能力验证短信/SMTP 实际送达，以及管理员 OIDC audience、角色映射和换钥 |
| 监控平台 | 按选用方案对接 Prometheus/OTel 后端，验证采集权限、实例标签和故障期间采集 |
| 部署与恢复 | 在目标环境验证启动迁移、探针、摘流、SIGTERM、备份恢复、升级窗口及回退安排 |

具体条件沿用下方“嵌入式产品上线”和“accountsvc 产品上线”清单。仓库隔离环境测试与产品验收分别保留证据；发现库内缺陷时另行建立对应修复任务。

## 嵌入式产品上线

- [ ] 产品侧按启用能力确认发送器、微信/Apple 配置、管理员身份验证、可信代理 IP、schema/Redis/issuer/audience/密钥隔离，以及业务数据匿名化回调。
- [ ] 按[网关接入契约](gateway-integration.md)落实路由保护、TLS、可信 IP、入口频率/并发限制和日志脱敏。
- [ ] 产品侧在自己的预发布环境验证实际认证链路、备份恢复、升级窗口和回退安排。

以上是各产品生产上线的前置条件，发布库版本不自动完成这些验收。实际发送、第三方身份提供方和设备联调由宿主负责。

## accountsvc 产品上线

accountsvc 与库使用同一版本源码；运行配置见[服务手册](accountsvc.md)，网关统一契约见[网关接入手册](gateway-integration.md)，成套回退要求见[服务手册](accountsvc.md#邮件匿名化与停机)。运行该服务的产品在目标环境确认：

- [ ] 删除旧 INTERNAL_ADDR/内部端口，更新统一内省和探针地址；保存旧二进制、配置、证书、调用方及网关策略的成套回退材料。
- [ ] 配置明确公网路由范围和独立管理策略，公网内省/探针被拒绝且不进入后端；授权内部调用可访问，未授权直连不能绕过。
- [ ] 如启用 Prometheus，先落实 `/metrics` 网关监控来源允许清单与后端端口直连限制，证明公网请求被拒绝且不进入后端、授权监控可抓取；核对同址 TLS/证书与 no-store。
- [ ] 指标默认关闭；启用时设置固定非秘密且符合格式的 `ACCOUNTSVC_METRICS_INSTANCE`，不同逻辑实例不同、多副本保留独立抓取目标标签；验证关闭404、其他方法405、采集错误/超出5并发503、业务依赖故障仍可采集。部署方接受无观测时可空、重启计数重置与停机尾部增量不保证被抓取，回退关闭开关并移除抓取目标。
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
