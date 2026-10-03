// Package accountkit 是可嵌入宿主服务的 C 端账号体系：多身份账号、
// 短期 JWT + 轮换 refresh 的会话、软删除与匿名化，以及对应的管理面。
//
// 本包只做装配：Config 与 Deps 进，Auth 出；HTTP handler 与中间件由宿主
// 挂到自己的 router 上。库不绑端口、不建 http.Server、不定义路径前缀。
//
// 生命周期：New（纯构造，无 I/O）→ Migrate（建 schema 与表）→ Start（维护任务）→ Close。
//
// 所有数据库对象位于 Config.Schema 指定的 PostgreSQL schema；所有 Redis 键以
// Config.KeyPrefix 开头。二者都可配置，使同一库/同一 Redis 可并存多个实例。
package accountkit
