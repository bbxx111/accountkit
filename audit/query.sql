-- name: InsertAuditEvent :execrows
INSERT INTO audit_event (id, source, action, result, actor, targets, context, data, occur_time)
VALUES (@id, @source, @action, sqlc.narg('result')::smallint, @actor, @targets, @context, @data, @occur_time::timestamptz)
ON CONFLICT (id) DO NOTHING;

-- name: GetAuditEvent :one
SELECT * FROM audit_event WHERE id = @id;

-- name: DeleteAuditEventsBefore :execrows
WITH candidates AS MATERIALIZED (
 SELECT id FROM audit_event WHERE source = @source::text AND occur_time < @before::timestamptz
 ORDER BY occur_time, id LIMIT @batch_size::int
), locked AS MATERIALIZED (
 SELECT e.id FROM audit_event e JOIN candidates c ON e.id = c.id
 ORDER BY e.id FOR UPDATE OF e
)
DELETE FROM audit_event WHERE id IN (SELECT id FROM locked);

-- name: ListLiveAuditFlows :many
SELECT id FROM identity_flow WHERE user_id = @user_id::text OR proof_user_ids @> ARRAY[@user_id::text];

-- name: ListAuditFlowEvidence :many
SELECT * FROM audit_event e
WHERE e.source = @source::text AND e.result = 1
 AND e.action IN ('SIGN_IN_COMPLETION_PROOF_VERIFIED','SIGN_IN_COMPLETION_COMPLETED')
 AND (e.actor @> @actor::jsonb OR e.targets @> @resource::jsonb OR e.context->'related_resources' @> @resource::jsonb)
ORDER BY occur_time, id;

-- name: ListAuditEventsByResource :many
SELECT * FROM audit_event e
WHERE e.source = @source::text
 AND (
  ( @resource_type::text = 'USER' AND e.actor @> @actor::jsonb)
  OR e.targets @> @resource::jsonb OR e.context->'related_resources' @> @resource::jsonb
  OR (
   @resource_type::text = 'USER'
   AND NOT (e.actor->>'type' = 'USER' AND e.actor->>'namespace' = @namespace::text)
   AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(e.targets || COALESCE(e.context->'related_resources','[]'::jsonb)) r
                   WHERE r->>'type' = 'USER' AND r->>'namespace' = @namespace::text)
   AND EXISTS (SELECT 1 FROM jsonb_array_elements(e.targets || COALESCE(e.context->'related_resources','[]'::jsonb)) r
               WHERE r->>'type' = 'IDENTITY_FLOW' AND r->>'namespace' = @namespace::text AND r->>'id' = ANY(@flow_ids::text[]))
  )
 )
 AND (sqlc.narg('after_time')::timestamptz IS NULL OR (e.occur_time,e.id) > (sqlc.narg('after_time')::timestamptz,sqlc.narg('after_id')::uuid))
ORDER BY occur_time,id LIMIT @page_limit::int;
-- name: SnapshotAuditFlows :many
SELECT id, user_id, proof_user_ids FROM identity_flow
WHERE id = ANY(@flow_ids::text[]) ORDER BY id;

-- name: LockAuditUsers :many
SELECT id, state FROM user_account
WHERE id = ANY(@user_ids::text[]) ORDER BY id FOR UPDATE;

-- name: LockAuditFlows :many
SELECT id, user_id, proof_user_ids, expire_time FROM identity_flow
WHERE id = ANY(@flow_ids::text[]) ORDER BY id FOR UPDATE;
