# Proposal

## Why

v0.4.0 之后的资料属性、本人资料 HTTP 和头像均已完成并归档。用户要求完成剩余收尾，并明确选择“按 v0.5.0 正式发布”；需把已验证功能集成到发布分支，完成最终 CI、不可变标签、Release 和独立宿主消费确认。

## What Changes

- 修复三项主规格正文长度问题，保留全部业务语义；已严格校验通过并将头像以 merge commit 合入 develop。
- 编写 v0.5.0 发布说明及版本专属门禁，更新当前文档版本，说明新空实例安装、资料全字段本人访问和头像宿主责任。
- 在最终 main 候选的在线 CI 验证生成、完整库/服务、race、恢复和必需测试，再创建 v0.5.0 tag 与 GitHub Release。
- 通过根门面和直接 enduser 两个独立宿主从远端固定版本消费，不使用 replace，记录发布事实。

## Capabilities

### New Capabilities

无。本次为发布与文档维护，设置 skip_specs，不新增运行时行为。

### Modified Capabilities

无。三处规格修改仅压缩长文本并移放既有细节，已独立审查，不改变契约。

## Impact

范围为 release/README/Go/HTTP/兼容说明、版本清单与发布工件，以及已获授权的本地合并、远端推送、tag/Release。用户确认尚无正式部署，本轮无需旧数据库迁移工具，不安排产品部署。历史发布 tag、SQL、冻结源和生产代码保持；归档仍是独立明确动作。
