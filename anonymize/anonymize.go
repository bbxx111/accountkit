// Package anonymize 定义宿主业务域接入账号 purge 的契约（设计文档 §3.5、§6.3、§6.4）。
//
// purge 由 auth-server 的维护任务在单个数据库事务内执行：先匿名化库自己的表（identity、
// user_account、session、audit_event），再按 Deps.Anonymizers 的顺序调用宿主注册的每个 Anonymizer；
// 任一返回错误则整个事务回滚，该账号留在 PENDING_DELETION，下一轮重试。因此业务表必须与库表
// 在同一数据库中（tx 是同一条连接）。
//
// 匿名化方向是"账号域调业务域"；将来拆分服务时原地替换为事件订阅（§6.4）。
package anonymize

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Anonymizer 切断一个业务域与某个账号（自然人）的关联。
type Anonymizer interface {
	// Name 用于日志与错误信息；同一进程内须唯一（authserver.New 校验）。
	Name() string
	// Tables 是本处理器负责的表名（不带 schema），供宿主的覆盖性测试核对：宿主每张含 user_id 的表
	// 必须被某个 Anonymizer 的 Tables 覆盖，或在宿主自己的豁免清单中逐表写明理由。
	Tables() []string
	// Anonymize 在 tx 内切断 userID（"u_…" 形式的账号 id）与本域数据的关联。
	// 不得提交/回滚 tx，不得在 tx 之外另开连接写同一批数据；返回错误即整账号回滚。
	Anonymize(ctx context.Context, tx pgx.Tx, userID string) error
}
