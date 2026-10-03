-- audit_event：审计事件的写入与保留期清理（读侧的管理端分页在阶段 6）。
-- 排序约定（§3.4 勘误）：ORDER BY occur_time, id；索引 audit_event_user_id_occur_time_idx (user_id, occur_time, id)。

-- name: InsertAuditEvents :batchexec
INSERT INTO audit_event (id, event_type, actor_kind, user_id, admin_issuer, admin_subject, admin_username,
                         session_id, identity_kind, subject_hint, result, reason, ip, device_id, request_id, occur_time)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16);

-- name: DeleteAuditEventsBefore :execrows
-- 维护任务 audit_retention：按 occur_time 分批删除超过保留期的行；命中 audit_event_occur_time_idx。
DELETE FROM audit_event
WHERE id IN (
    SELECT id FROM audit_event WHERE occur_time < @before::timestamptz ORDER BY occur_time LIMIT @batch_size::int
);

-- name: ListAuditEventsByUser :many
-- 管理端 GET /users/{user}/auditEvents：keyset 分页 ORDER BY occur_time, id；命中 audit_event_user_id_occur_time_idx。
SELECT * FROM audit_event
WHERE user_id = @user_id::text
  AND (sqlc.narg('after_time')::timestamptz IS NULL
       OR (occur_time, id) > (sqlc.narg('after_time')::timestamptz, sqlc.narg('after_id')::text))
ORDER BY occur_time, id
LIMIT @page_limit::int;
