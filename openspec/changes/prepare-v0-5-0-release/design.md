# Design

## Context

三项能力分别在 add-schema-validated-user-extensions、add-self-profile-http、add-user-avatar-url 归档。头像代码已完成完整本地库/race/恢复验证，HTTP 阶段有服务进程 race 证据；本轮只改文档和发布工件，不重复构造运行时功能。v0.5.0 已发布，两个独立宿主已完成远端消费验证；剩余工作是发布记录提交、集成和归档。

## Goals / Non-Goals

**Goals:** 固定 v0.5.0 的范围与候选，确保最终 main SHA 的在线生成、库/服务和恢复门禁通过，发布并验证远端消费。

**Non-Goals:** 旧数据库迁移、产品部署、额外运行时功能、移动历史 tag、发布镜像或二进制。

## Decisions

- 使用 v0.5.0：用户已明确选择，三个新增能力包含新增公开类型和响应字段、当前初始结构变化，区别于修复版本。
- 本地所有集成都用 --no-ff。先合入 develop 并推送，再将 develop 合入 main，推送最终候选；仅将精确通过 CI 的 main SHA 标记为 v0.5.0。
- 使用既有 GitHub Actions 对最终候选运行 check-generated.sh、verify.sh 和 verify-accountsvc.sh。此前本地完整验证是准备证据，最终候选 CI 为正式发布门禁；若失败先定位并修正，不改门禁或称旧日志通过。
- 当前 0001 已含 profile_attributes 和 avatar_url，安装 version=1/dirty=false。用户确认无正式部署，本轮提供新实例安装说明，不新增旧数据迁移工作；保留既有无写拒绝及历史保护。
- Release 正文由审查后的本地发布说明生成，链接固定到 tag/候选；已通过 GitHub API 核对 Release 的 tag、目标提交、非草稿/非预发布状态。两个独立宿主从远端 go get 固定 v0.5.0，无 replace，编译新增 Go/可选 HTTP 能力，并核验模块来源和初始 SQL 哈希。
- 发布后的事实以单独文档提交补记，保持 tag 不移动。归档须单独明确执行。

## Risks / Trade-offs

- 已发布标签不可改写：v0.5.0 已固定到精确通过 CI 的候选，后续只重试消费验证，不移动或重建 tag。
- Windows 共享目录构建可能很慢：正式候选使用 Linux 在线 CI 的本地检出；不以超时或 skip 作为通过。
- 产品服务商和目标环境未联调：继续由各产品承担，发布库不代表产品已上线。
