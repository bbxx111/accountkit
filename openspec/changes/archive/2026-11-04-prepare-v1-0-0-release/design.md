## Context

动机见 proposal。功能变更 consolidate-pre-1-0-history 已归档，代码提交 c9a3bf2 的本地完整 Linux 验证和在线 CI 已通过；本次仍为新的发布准备快照及最终 main SHA 获取证据。当前有九个 v0 tag、八个对应 Release，v1.0.0 尚不存在。

## Goals / Non-Goals

**Goals:** 发布可从远端独立获取的 v1.0.0，明确首次正式版本的契约、运行要求、迁移边界及产品验证责任。

**Non-Goals:** 不修改 Go/SQL/依赖/认证行为，不提供开发期数据库升级，不部署产品，不替换既有宿主，不自动归档本变更。

## Decisions

1. 按已有发布检查表执行，不另设计发布机制。保留任务分支→develop→main 的独立 merge commit；候选内容包含候选态发布说明。最终 CI 的 head_sha 必须与 tag 指向的 main SHA 相同。备选复用旧 CI 不满足候选证明，故不用。
2. 本变更 `skip_specs: true`，因为只执行既有契约的发布和文档维护；不为发布虚构新的规格需求。没有代码修改，不新增镜像实现的测试。
3. 用户已明确确认先发布 v1.0.0，完成远端消费后再删除 v0；本次授权覆盖默认“发布前无 v0”顺序要求，不改变迁移历史检查静默忽略 v0 的行为。删除前保存 tag 对象/提交及 Release 元信息到 `.test-output/v1-release/`，精确删除九个 tag 和现存八个 Release；不重写提交历史，不触碰其他标签/仓库。发布前允许已退役 v0 标签存在，同名 v1.0.0 标签仍必须不存在。
4. 0001 保持现有哈希，首个正式 tag 起冻结，后续从 0002 递增。此次不新增源码 pin（tag SHA 尚不存在，加入会改变候选）；以 annotated tag、远端解析结果、发布验收记录固定证据。既有工具仅核对可达 tag 内容，不能侦测 tag 被删除；维护方负责不移动/删除正式 tag，不宣称已启用远端保护规则。
5. 完整验证使用独立 Docker 网络、一次性 PostgreSQL 17/Redis 7、三个新库；`verify.sh` 内含恢复演练，无需对已用恢复库重复执行。以非 root 单独补验文件权限测试。外部提供方/网关不具备真实配置，维持已记录边界。
6. 发布同一份 `docs/releases/v1.0.0.md` 为 GitHub Release 正文；最终候选通过后不为改“候选”措辞增加提交再打 tag。发布成功及两个消费模块验证后，在补记分支将当前文档更新为已发布、记录对象 SHA/CI/消费证据，再以 merge commit 合入 develop/main，不移动 tag。
7. 两个消费模块均为独立 go.mod，`GOWORK=off`、无 replace、从远端 `go get ...@v1.0.0`：一个编译根 Auth.EndUserHandler，另一个编译 httpapi/enduser.New。同时核对公开正式符号和两个迁移 SQL 的 SHA256；不把本地 replace 或下载失败当消费成功。

## Risks / Trade-offs

- 旧标签删除影响仍引用开发版本的接入方 → 发布说明明确版本切换和 API 调用替换，提交历史保留，不承诺代理缓存清除。
- tag 已公开后发现错误 → 保留 tag，通过修复版本交付；数据库恢复使用独立空库核验，不能回滚为开发期结构或清 Redis 来宣称安全恢复。
- 文档补记产生不同 SHA → 精确区分已验证的正式 tag 候选与仅补记的分支 HEAD，不把 CI 结果挪用到不同提交。
- 新增提交触发 CI 失败 → 先诊断修复再重新验证新候选，禁止以本地通过或旧 CI 代替。

## Migration Plan

集成归档 → 准备发布工件与说明 → 完整门禁与独立审查 → 提交、合入 develop/main 并推送 → 最终 main CI 通过 → annotated tag/Release → 远端消费 → 清理旧标签/Release → 已发布状态补记并集成。无生产数据库迁移；新 schema 直接安装 version 1。发布后不提供退回 v0 的路径。
