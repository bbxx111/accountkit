# v0.5.0 发布验收

日期：2026-10-21。用户要求处理 v0.4.0 后剩余收尾，确认尚无正式部署、无需旧数据库迁移，并明确选择“按 v0.5.0 正式发布”。当前结论：v0.5.0 已正式发布，最终候选 CI 已通过；两个独立宿主的远端固定版本消费已验证通过。发布记录已提交并以独立 merge commit 集成，归档为后续独立动作。

## 已完成的发布事实

- 三项新增功能任务已完成并归档：资料属性、本人资料 HTTP/accountsvc 配置、头像。
- 发布候选为 `102315c3069273f7a8c3ad253c1f4ae01255f8c6`；本地 `main`、`origin/main` 与 `v0.5.0` 解引用目标一致。
- GitHub Actions [run 39587858534](https://github.com/bbxx111/accountkit/actions/runs/39587858534) 的 `head_sha` 精确为该候选，结论为 `success`，作业 `test` 成功，覆盖生成检查、完整库/服务、race、必需用例和真实恢复。
- `v0.5.0` annotated tag 对象为 `8c15ee57aaa1db2b0c083db52b2a621d1e215b6a`，解引用为最终候选；远端 tags 核对结果与本地一致。
- GitHub Release [v0.5.0](https://github.com/bbxx111/accountkit/releases/tag/v0.5.0)（ID `419455513`）已于 2026-10-21T11:04:43Z 发布，`draft=false`、`prerelease=false`，目标为最终候选。
- 发布记录分支为 `docs/record-v0-5-0-publication`。相对头像实施提交 `4e84a78`，发布记录提交为 `d82bcef`，首轮 `--no-ff` 集成为 `develop` `6d3e1a7`、`main` `73407fd`；当前分支仅维护文档和本 change，不把本轮文档整理称为重新运行完整测试。

## 远端消费验证（通过）

- `.test-output/release-v050/hosts/facade` 与 `hosts/enduser` 两个独立临时宿主使用 `GOWORK=off`、无 replace；`go mod tidy`、`go vet ./...`、`go build` 和消费者执行均成功。
- facade 输出 `facade: profile configuration and avatar public contracts OK`；enduser 输出 `enduser: optional profile interfaces and unauthenticated profile boundary OK`，覆盖根门面资料/头像能力和直接 enduser 可选接口、认证边界。
- 两个宿主执行 `go list -m -json github.com/bbxx111/accountkit` 均返回 `v0.5.0`，`Replace` 为空，且 `go.mod` 无 replace 指令。
- 两个宿主从远端模块读取的 `migrations/0001_init.up.sql` SHA256 均为 `491068d7577ca33a94dc9d3f74b02acdbc95662aa954c693b63a3fd7b196b12c`，与预期一致。
- 为避开用户目录缓存不可写，验证使用 `.test-output/release-v050/gopath` 下的 `GOPATH/GOMODCACHE` 和独立 `GOCACHE`；`GOPROXY=https://goproxy.cn,direct`、`GOSUMDB=sum.golang.org`，未关闭依赖校验。
- 证据包括 `facade-module.json`、`enduser-module.json`、`facade-run.log` 与 `enduser-run.log`。此前代理/sumdb 缓存失败属于执行环境问题，最终消费结果不受影响；已发布 tag 不移动，也不重新发布版本。

## 验证策略与准备证据

- 发布材料准备阶段执行了 `openspec validate --all --strict` 28/28、`check-generated.sh`、`check-migrations.sh`、`git diff --check`、两份 OpenAPI 3.1.0/v0.5.0 解析、消费者 24/管理面 11 操作及 DTO 核对，并检查本地文件链接。
- 收尾文档修改后再次执行 `openspec validate --all --strict`，结果为 28 项通过、0 失败；`git diff --check` 通过，变更文件的相对 Markdown 链接均存在。
- 头像定版已运行完整库验证：44 包、894 个顶层测试、793 项必需门禁、race 和 pg_dump/pg_restore 均通过，权限文件用例有非 root 补验；见[头像验收](../archive/2026-10-21-add-user-avatar-url/verification.md)。
- 本人资料 HTTP 阶段另有完整服务进程 race 及 29 项服务必需门禁证据，见[HTTP 验收](../archive/2026-10-21-add-self-profile-http/verification.md)。这些是发布准备证据，不替代最终候选在线 CI 或远端消费。

## 未验证范围

产品部署、实际服务商与设备联调、网关/监控平台和目标环境验收未完成，不作为库版本发布已完成的消费证据。本轮没有旧数据库迁移任务，不修改既有历史保护；没有授权发布镜像或二进制。发布记录提交/集成已完成；变更归档仍是后续明确动作。
