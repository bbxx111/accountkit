# Tasks

## 1. 发布材料

- [x] 1.1 盘点 v0.5.0..develop 的归档变更、公开 API/配置/HTTP/指标/审计/依赖与 0001 差异，编写 `docs/releases/v0.6.0.md`。
- [x] 1.2 在发布检查表增加 v0.6.0 门禁并更新当前待办；同步 README、`docs/` 版本表述与两份 OpenAPI `info.version`。
- [x] 1.3 发布说明与迁移/回退边界经独立审查，处理实质问题。

## 2. 准备快照验证

- [x] 2.1 运行 `openspec validate --all --strict`、`check-generated.sh`、`check-migrations.sh`、OpenAPI 检查、`git diff --check` 与本地链接检查。
- [x] 2.2 在 Linux 验证镜像中对准备快照执行 `verify.sh`（build/vet/race/必需门禁/恢复三阶段）与 `verify-accountsvc.sh`，结果写入 `verification.md`。

## 3. 最终候选与发布（须分别明确授权）

- [x] 3.1 提交发布材料，以 `--no-ff` 合入 develop 与 main 并推送，记录最终候选 SHA。
- [x] 3.2 核验最终候选在线 CI 全部通过。
- [x] 3.3 创建并推送 v0.6.0 annotated tag，发布 GitHub Release。
- [x] 3.4 两个独立宿主从远端无 replace 消费 v0.6.0，补记发布事实与长期文档状态。

## Workflow follow-up

- 产品环境部署和本发布变更归档另行明确执行。
