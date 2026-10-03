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
