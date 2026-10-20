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
SELECT id FROM sign_in_completion_flow
WHERE id = ANY(@flow_ids::text[])
 AND (user_id = @user_id::text OR proof_user_ids @> ARRAY[@user_id::text]);

-- name: ListAuditFlowEvidence :many
-- One candidate per unresolved page flow, independent of the page's action/time
-- range. Go validates authentication projections and advances only rejected flows.
WITH requested AS MATERIALIZED (
 SELECT f->>'id' AS flow_id, (f->>'after_id')::uuid AS after_id,
 jsonb_build_array(jsonb_build_object('type','IDENTITY_FLOW','namespace',@namespace::text,'id',f->>'id')) AS resource
 FROM jsonb_array_elements(@flow_cursors::jsonb) f
)
SELECT r.flow_id::text AS flow_id, e.* FROM requested r
CROSS JOIN LATERAL (
 SELECT id FROM (
  SELECT a.id FROM audit_event a
  WHERE a.source = @source::text AND a.result = 1
   AND a.action IN ('SIGN_IN_COMPLETION_PROOF_VERIFIED','SIGN_IN_COMPLETION_COMPLETED')
   AND ((a.actor->>'type' = 'USER' AND a.actor->>'namespace' = @namespace::text AND a.actor->>'id' = @user_id::text)
    OR a.targets @> @resource::jsonb OR a.context->'related_resources' @> @resource::jsonb)
   AND (a.targets @> r.resource OR a.context->'related_resources' @> r.resource)
   AND (r.after_id IS NULL OR a.id > r.after_id)
  OFFSET 0
 ) candidates ORDER BY id LIMIT 1
) chosen
CROSS JOIN LATERAL (SELECT a.* FROM audit_event a WHERE a.id = chosen.id OFFSET 0) e;

-- name: ListAuditEventsByResource :many
-- Historical ownership stays inside the database and is never cursor/action-bound.
-- GIN membership is materialized BEFORE Top-N to fence off source-timeline walks.
-- Only (id,occur_time) candidates are merged; the final lateral lookup fetches at
-- most page_limit complete events even with a generic plan. History scans remain.
WITH ownership_evidence AS MATERIALIZED (
 SELECT e.targets, e.context->'related_resources' AS related_resources
 FROM audit_event e
 WHERE @resource_type::text = 'USER' AND e.source = @source::text AND e.result = 1
  AND e.action IN ('SIGN_IN_COMPLETION_PROOF_VERIFIED','SIGN_IN_COMPLETION_COMPLETED')
  AND ((e.actor->>'type' = 'USER' AND e.actor->>'namespace' = @namespace::text AND e.actor->>'id' = @resource_id::text)
   OR e.targets @> @resource::jsonb OR e.context->'related_resources' @> @resource::jsonb)
), owned_flows AS MATERIALIZED (
 SELECT id FROM sign_in_completion_flow
 WHERE @resource_type::text = 'USER' AND (user_id = @resource_id::text OR proof_user_ids @> ARRAY[@resource_id::text])
 UNION
 SELECT f->>'id' FROM ownership_evidence e
 CROSS JOIN LATERAL jsonb_array_elements(e.targets || COALESCE(e.related_resources,'[]'::jsonb)) f
 WHERE f->>'type' = 'IDENTITY_FLOW' AND f->>'namespace' = @namespace::text
), resource_matches AS MATERIALIZED (
 SELECT a.id, a.occur_time FROM audit_event a
 WHERE a.source = @source::text
  AND (a.targets @> @resource::jsonb OR a.context->'related_resources' @> @resource::jsonb)
  AND (COALESCE(cardinality(@actions::text[]),0) = 0 OR a.action = ANY(@actions::text[]))
  AND (sqlc.narg('after_time')::timestamptz IS NULL OR (a.occur_time,a.id) > (sqlc.narg('after_time')::timestamptz,sqlc.narg('after_id')::uuid))
), flow_matches AS MATERIALIZED (
 SELECT DISTINCT a.id, a.occur_time FROM owned_flows f
 CROSS JOIN LATERAL (
  -- Fence membership before cursor/action predicates too: a same-time cursor
  -- can be estimated as highly selective and otherwise choose a source-timeline
  -- scan once per owned flow instead of the exact resource GIN lookup.
  SELECT e.id, e.occur_time, e.action FROM audit_event e
  WHERE e.source = @source::text
   AND (e.targets @> jsonb_build_array(jsonb_build_object('type','IDENTITY_FLOW','namespace',@namespace::text,'id',f.id))
    OR e.context->'related_resources' @> jsonb_build_array(jsonb_build_object('type','IDENTITY_FLOW','namespace',@namespace::text,'id',f.id)))
   AND NOT (e.actor->>'type' = 'USER' AND e.actor->>'namespace' = @namespace::text)
   AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(e.targets || COALESCE(e.context->'related_resources','[]'::jsonb)) r
                   WHERE r->>'type' = 'USER' AND r->>'namespace' = @namespace::text)
  OFFSET 0
 ) a
 WHERE (COALESCE(cardinality(@actions::text[]),0) = 0 OR a.action = ANY(@actions::text[]))
  AND (sqlc.narg('after_time')::timestamptz IS NULL OR (a.occur_time,a.id) > (sqlc.narg('after_time')::timestamptz,sqlc.narg('after_id')::uuid))
), candidates AS MATERIALIZED (
 (SELECT a.id, a.occur_time FROM audit_event a
  WHERE a.source = @source::text AND @resource_type::text = 'USER'
   AND a.actor->>'type' = 'USER' AND a.actor->>'namespace' = @namespace::text AND a.actor->>'id' = @resource_id::text
   AND (COALESCE(cardinality(@actions::text[]),0) = 0 OR a.action = ANY(@actions::text[]))
   AND (sqlc.narg('after_time')::timestamptz IS NULL OR (a.occur_time,a.id) > (sqlc.narg('after_time')::timestamptz,sqlc.narg('after_id')::uuid))
  ORDER BY a.occur_time, a.id LIMIT @page_limit::int)
 UNION
 (SELECT id, occur_time FROM resource_matches ORDER BY occur_time, id LIMIT @page_limit::int)
 UNION
 (SELECT id, occur_time FROM flow_matches ORDER BY occur_time, id LIMIT @page_limit::int)
), page AS MATERIALIZED (
 SELECT id, occur_time FROM candidates ORDER BY occur_time, id LIMIT @page_limit::int
)
SELECT e.* FROM page p
CROSS JOIN LATERAL (SELECT a.* FROM audit_event a WHERE a.id = p.id OFFSET 0) e
ORDER BY p.occur_time, p.id;
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
