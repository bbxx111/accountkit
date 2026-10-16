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

-- name: ListAuditPrivacyFlowUsers :many
-- Privacy follows every complete same-source USER/FLOW co-occurrence, unlike
-- Reader authorization, which accepts only approved ownership evidence.
SELECT DISTINCT (f->>'id')::text AS flow_id, u.user_id::text AS user_id
FROM audit_event e
CROSS JOIN LATERAL jsonb_array_elements(e.targets || COALESCE(e.context->'related_resources','[]'::jsonb)) f
CROSS JOIN LATERAL (
 SELECT e.actor->>'id' AS user_id
 WHERE e.actor->>'type' = 'USER' AND e.actor->>'namespace' = @namespace::text
 UNION
 SELECT r->>'id' AS user_id
 FROM jsonb_array_elements(e.targets || COALESCE(e.context->'related_resources','[]'::jsonb)) r
 WHERE r->>'type' = 'USER' AND r->>'namespace' = @namespace::text
) u
WHERE e.source = @source::text AND f->>'type' = 'IDENTITY_FLOW'
 AND f->>'namespace' = @namespace::text AND f->>'id' = ANY(@flow_ids::text[])
ORDER BY flow_id, user_id;

-- name: ListAuditPrivacyCleanupIDs :many
SELECT e.id FROM audit_event e
WHERE e.source = @source::text
 AND EXISTS (SELECT 1 FROM jsonb_array_elements(e.targets || COALESCE(e.context->'related_resources','[]'::jsonb)) f
 WHERE f->>'type' = 'IDENTITY_FLOW' AND f->>'namespace' = @namespace::text AND f->>'id' = ANY(@flow_ids::text[]))
ORDER BY e.id;

-- name: LockAuditEvent :one
SELECT * FROM audit_event WHERE id = @id FOR UPDATE;

-- name: ScrubAuditEventPrivacy :exec
-- The caller holds this row lock and derives only the controlled privacy
-- projection from that row. Event facts, identity and storage time stay intact.
UPDATE audit_event SET actor = @actor, targets = @targets, context = @context, data = @data
WHERE id = @id;

-- name: ListAuditFlowLockKeys :many
-- Bind locks to the resolved local relation, not the first search_path schema.
-- Hash collisions only serialize more work; callers lock actual keys in order.
SELECT DISTINCT hashtextextended(jsonb_build_array(
 'accountkit.audit.privacy.flow', 'identity_flow'::regclass::oid,
 @source::text, @namespace::text, f.flow_id)::text, 0)::bigint AS lock_key
FROM unnest(@flow_ids::text[]) AS f(flow_id)
ORDER BY lock_key;

-- name: LockAuditFlowScope :exec
SELECT pg_advisory_xact_lock(@lock_key::bigint);
