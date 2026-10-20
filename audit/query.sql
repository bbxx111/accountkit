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
SELECT id FROM sign_in_completion_flow WHERE user_id = @user_id::text OR proof_user_ids @> ARRAY[@user_id::text];

-- name: ListAuditFlowEvidence :many
-- Every OR arm must be index-backed (actor expression index, resource GIN indexes) so
-- the planner can combine them with BitmapOr. Actor membership uses expression
-- equality because idx_audit_event_actor cannot serve jsonb containment.
SELECT * FROM audit_event e
WHERE e.source = @source::text AND e.result = 1
 AND e.action IN ('SIGN_IN_COMPLETION_PROOF_VERIFIED','SIGN_IN_COMPLETION_COMPLETED')
 AND ((e.actor->>'type' = 'USER' AND e.actor->>'namespace' = @namespace::text AND e.actor->>'id' = @user_id::text)
  OR e.targets @> @resource::jsonb OR e.context->'related_resources' @> @resource::jsonb)
ORDER BY occur_time, id;

-- name: ListAuditEventsByResource :many
-- Each membership arm is fenced in a materialized CTE so the outer ORDER BY/LIMIT
-- cannot turn it into a walk of the source timeline, including under generic plans.
-- The actor arm reads idx_audit_event_actor in (occur_time, id) order and stops at the
-- page size; resource arms use the GIN indexes and hold only rows naming the resource.
-- Arms return whole rows so no join back to audit_event is planned. @flow_resources
-- holds one single-element IDENTITY_FLOW resource array per flow; empty outside USER.
WITH matched AS MATERIALIZED (
 (SELECT a.* FROM audit_event a
  WHERE a.source = @source::text AND @resource_type::text = 'USER'
   AND a.actor->>'type' = 'USER' AND a.actor->>'namespace' = @namespace::text AND a.actor->>'id' = @resource_id::text
   AND (COALESCE(cardinality(@actions::text[]),0) = 0 OR a.action = ANY(@actions::text[]))
   AND (sqlc.narg('after_time')::timestamptz IS NULL OR (a.occur_time,a.id) > (sqlc.narg('after_time')::timestamptz,sqlc.narg('after_id')::uuid))
  ORDER BY a.occur_time, a.id LIMIT @page_limit::int)
 UNION
 SELECT a.* FROM audit_event a
 WHERE a.source = @source::text
  AND (a.targets @> @resource::jsonb OR a.context->'related_resources' @> @resource::jsonb)
  AND (COALESCE(cardinality(@actions::text[]),0) = 0 OR a.action = ANY(@actions::text[]))
  AND (sqlc.narg('after_time')::timestamptz IS NULL OR (a.occur_time,a.id) > (sqlc.narg('after_time')::timestamptz,sqlc.narg('after_id')::uuid))
 UNION
 SELECT a.* FROM audit_event a
 WHERE a.source = @source::text AND @resource_type::text = 'USER'
  AND (a.targets @> ANY(@flow_resources::jsonb[]) OR a.context->'related_resources' @> ANY(@flow_resources::jsonb[]))
  AND NOT (a.actor->>'type' = 'USER' AND a.actor->>'namespace' = @namespace::text)
  AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(a.targets || COALESCE(a.context->'related_resources','[]'::jsonb)) r
                  WHERE r->>'type' = 'USER' AND r->>'namespace' = @namespace::text)
  AND (COALESCE(cardinality(@actions::text[]),0) = 0 OR a.action = ANY(@actions::text[]))
  AND (sqlc.narg('after_time')::timestamptz IS NULL OR (a.occur_time,a.id) > (sqlc.narg('after_time')::timestamptz,sqlc.narg('after_id')::uuid))
)
SELECT id, source, action, result, actor, targets, context, data, occur_time, create_time FROM matched
ORDER BY occur_time, id LIMIT @page_limit::int;
-- name: SnapshotAuditFlows :many
SELECT id, user_id, proof_user_ids FROM sign_in_completion_flow
WHERE id = ANY(@flow_ids::text[]) ORDER BY id;

-- name: LockAuditUsers :many
SELECT id, state FROM user_account
WHERE id = ANY(@user_ids::text[]) ORDER BY id FOR UPDATE;

-- name: LockAuditFlows :many
SELECT id, user_id, proof_user_ids, expire_time FROM sign_in_completion_flow
WHERE id = ANY(@flow_ids::text[]) ORDER BY id FOR UPDATE;

-- name: ListAuditPrivacyFlowUsers :many
-- Privacy follows every complete same-source USER/FLOW co-occurrence, unlike
-- Reader authorization, which accepts only approved ownership evidence.
WITH flow_resources AS MATERIALIZED (
 SELECT jsonb_build_array(jsonb_build_object('type','IDENTITY_FLOW','namespace',@namespace::text,'id',f)) AS resource
 FROM unnest(@flow_ids::text[]) f
)
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
WHERE e.source = @source::text
 AND (e.targets @> ANY(ARRAY(SELECT resource FROM flow_resources))
      OR e.context->'related_resources' @> ANY(ARRAY(SELECT resource FROM flow_resources)))
 AND f->>'type' = 'IDENTITY_FLOW'
 AND f->>'namespace' = @namespace::text AND f->>'id' = ANY(@flow_ids::text[])
ORDER BY flow_id, user_id;

-- name: ListAuditPrivacyCleanupIDs :many
WITH flow_resources AS MATERIALIZED (
 SELECT jsonb_build_array(jsonb_build_object('type','IDENTITY_FLOW','namespace',@namespace::text,'id',f)) AS resource
 FROM unnest(@flow_ids::text[]) f
)
SELECT e.id FROM audit_event e
WHERE e.source = @source::text
 AND (e.targets @> ANY(ARRAY(SELECT resource FROM flow_resources))
      OR e.context->'related_resources' @> ANY(ARRAY(SELECT resource FROM flow_resources)))
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
 'accountkit.audit.privacy.flow', 'sign_in_completion_flow'::regclass::oid,
 @source::text, @namespace::text, f.flow_id)::text, 0)::bigint AS lock_key
FROM unnest(@flow_ids::text[]) AS f(flow_id)
ORDER BY lock_key;

-- name: LockAuditFlowScope :exec
SELECT pg_advisory_xact_lock(@lock_key::bigint);
