## 1. 发布准备

- [x] 1.1 集成整合归档，完成发布说明、版本专属检查表及调用迁移/回退边界；核对无 Go/SQL/依赖变化，独立审查无未关闭实质问题。
- [x] 1.2 当前准备快照通过 OpenSpec strict、sqlc、迁移历史、OpenAPI、Linux 完整库/服务 race 与必需门禁、真实恢复三阶段、非 root 权限补验；记录实际输出和未验证项。

## 2. 候选与正式发布

- [ ] 2.1 发布准备分支以独立 merge commit 合入 develop，再将 develop 以独立 merge commit 合入 main 并推送；记录最终候选 SHA，确认最终候选在线 CI 全部通过且 head_sha 精确一致。
- [ ] 2.2 按用户明确顺序允许旧 v0 暂存，确认远端没有同名 v1.0.0 tag，创建并推送 annotated v1.0.0 tag，核对对象/解析提交一致，发布同一说明的 GitHub Release。
- [ ] 2.3 两个独立无 replace、GOWORK=off 的远端消费者 go get v1.0.0 并编译根门面/直接适配器；核对模块版本、公开符号与初始 SQL 哈希。

## 3. 发布补记

- [ ] 3.1 v1.0.0 发布和远端消费完成后，保存旧 tag/Release 元信息，按已获授权删除九个 v0 本地/远端 tag 和现存八个 Release；核对远端列表及当前文档状态一致。
- [ ] 3.2 将当前文档状态补记为已发布，保存 tag、候选 CI 与消费证据；补记以 merge commit 合入 develop/main 并推送，tag 不移动，验证最终分支状态；本变更归档留待单独明确请求。
