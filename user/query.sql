-- user_account

-- name: CreateUser :one
INSERT INTO user_account (id, state) VALUES ($1, $2)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM user_account WHERE id = $1;

-- name: BatchPublicProfiles :many
-- 宿主授权的业务页批量读取最小公开资料；包含注销状态，由领域层清空其显示名。
SELECT id, display_name, state FROM user_account WHERE id = ANY(@ids::text[]);

-- name: LockUserByID :one
-- 同设备重登前对用户行加锁：并发登录在此串行化，第二个事务等到第一个提交（含会话吊销/新建）
-- 后再读，从而看到刚吊销的旧会话，不会与它一起并存两条活跃会话。
SELECT * FROM user_account WHERE id = $1 FOR UPDATE;

-- name: UpdateUserDisplayName :one
UPDATE user_account SET display_name = $2, update_time = now() WHERE id = $1
RETURNING *;

-- name: SoftDeleteUser :one
-- 软删除（AIP-164）：调用方须已持有行锁并确认 state = ACTIVE。
UPDATE user_account
SET state = @state, delete_time = @now::timestamptz, purge_time = @purge_time::timestamptz, update_time = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: UndeleteUser :one
-- 取消注销：调用方须已持有行锁并确认 state = PENDING_DELETION。
UPDATE user_account
SET state = @state, delete_time = NULL, purge_time = NULL, update_time = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: ListUsersDueForPurge :many
-- purge 任务取批。谓词写死 state = 3（PENDING_DELETION）而非绑定参数：它必须与 idx_user_account_purge_time
-- 的部分索引谓词逐字一致——通用计划下绑定参数无法证明蕴含部分索引谓词，会退化为全表扫描。
SELECT id FROM user_account
WHERE state = 3 AND purge_time <= @now::timestamptz
ORDER BY purge_time, id
LIMIT @batch_size::int;

-- name: PurgeUser :execrows
-- 匿名化账号行：展示名与冻结快照置 NULL，state → DELETED，purge_time 改写为实际执行时刻（delete_time 保留）。
-- WHERE 带 state 做二次保险：调用方持锁并已复核状态，这里再钉一次避免把非 PENDING_DELETION 的行写成 DELETED。
UPDATE user_account
SET state = @deleted_state, display_name = NULL, freeze_time = NULL, freeze_reason = NULL,
    freeze_actor_subject = NULL, freeze_actor_username = NULL,
    purge_time = @now::timestamptz, update_time = @now::timestamptz
WHERE id = @id AND state = @pending_state;

-- name: FreezeUser :one
-- 管理端 :freeze：调用方持行锁并已确认 state = ACTIVE；快照管理员 sub/username（issuer 进审计）。
UPDATE user_account
SET state = @state, freeze_time = @now::timestamptz, freeze_reason = @reason::text,
    freeze_actor_subject = @actor_subject::text, freeze_actor_username = @actor_username::text,
    update_time = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: UnfreezeUser :one
-- 管理端 :unfreeze：调用方持行锁并已确认 state = FROZEN；快照清空，历史留在审计。
UPDATE user_account
SET state = @state, freeze_time = NULL, freeze_reason = NULL, freeze_actor_subject = NULL, freeze_actor_username = NULL,
    update_time = @now::timestamptz
WHERE id = @id
RETURNING *;

-- name: ListUsersAdmin :many
-- 管理端 GET /users：可空参数实现可选过滤（sqlc.narg），keyset 分页 ORDER BY create_time, id（§3.4），
-- 由 idx_user_account_create_time_id 提供索引序，避免每页全表排序。
-- state NOT IN (3, 4) 写死数值（PENDING_DELETION / DELETED，与 enum 钉死），与 purge 索引谓词同理。
-- 身份条件落在同一条活跃身份行（单个 EXISTS）；identity.phone/email 的 digests 是 AllDigests 的结果。
-- 注意：EXISTS 子查询里的 ($n IS NULL OR col = $n) 形式在 PostgreSQL 通用计划（generic plan）下可能不走
-- identity_kind_* 的部分索引；若观测到这类退化，优先在连接池上设 plan_cache_mode = force_custom_plan，
-- 或对本查询 SET LOCAL。
SELECT u.* FROM user_account u
WHERE (sqlc.narg('state')::smallint IS NULL OR u.state = sqlc.narg('state')::smallint)
  AND (@include_deleted::bool OR u.state NOT IN (3, 4))
  AND (sqlc.narg('create_time_min')::timestamptz IS NULL OR u.create_time >= sqlc.narg('create_time_min')::timestamptz)
  AND (sqlc.narg('create_time_max')::timestamptz IS NULL OR u.create_time <  sqlc.narg('create_time_max')::timestamptz)
  AND (NOT @by_identity::bool OR EXISTS (
        SELECT 1 FROM identity i
        WHERE i.user_id = u.id AND i.delete_time IS NULL
          AND (sqlc.narg('identity_kind')::smallint IS NULL OR i.kind = sqlc.narg('identity_kind')::smallint)
          AND (cardinality(@digests::text[]) = 0 OR i.subject_digest = ANY(@digests::text[]))
          AND (sqlc.narg('hint_prefix')::text IS NULL OR i.hint_prefix = sqlc.narg('hint_prefix')::text)
          AND (sqlc.narg('hint_suffix')::text IS NULL OR i.hint_suffix = sqlc.narg('hint_suffix')::text)))
  AND (sqlc.narg('after_time')::timestamptz IS NULL
       OR (u.create_time, u.id) > (sqlc.narg('after_time')::timestamptz, sqlc.narg('after_id')::text))
ORDER BY u.create_time, u.id
LIMIT @page_limit::int;

-- identity

-- name: CreateIdentity :one
INSERT INTO identity (id, user_id, kind, subject_digest, digest_key_version, provider_subject,
                      subject_ciphertext, cipher_key_version, hint_prefix, hint_suffix, provider_meta)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
RETURNING *;

-- name: FindActiveIdentityByDigests :one
-- 多版本查找：@digests 是 pii.Digester.AllDigests 的结果（active 在前）。漏掉任一活跃版本
-- 会让该版本下写入的行查不到，同一手机号就会被再次注册。命中 uk_identity_kind_subject_digest。
SELECT * FROM identity
WHERE kind = @kind AND subject_digest = ANY(@digests::text[]) AND delete_time IS NULL
LIMIT 1;

-- name: ListActiveIdentitiesByUser :many
SELECT * FROM identity WHERE user_id = $1 AND delete_time IS NULL ORDER BY create_time, id;

-- name: FindActiveIdentityByProviderSubject :one
SELECT * FROM identity WHERE kind = $1 AND provider_subject = $2 AND delete_time IS NULL;

-- name: UpdateIdentityProviderMeta :execrows
UPDATE identity SET provider_meta = @provider_meta, update_time = @update_time::timestamptz WHERE id = @id AND delete_time IS NULL;

-- name: GetActiveIdentityByIDAndUser :one
SELECT * FROM identity WHERE id = $1 AND user_id = $2 AND delete_time IS NULL;

-- name: SoftDeleteIdentity :execrows
UPDATE identity SET delete_time = @now::timestamptz, update_time = @now::timestamptz
WHERE id = @id AND user_id = @user_id AND delete_time IS NULL;

-- name: ListIdentitiesByUserIncludingDeleted :many
-- purge 专用：含已解绑（软删）的行——它们仍带原 subject 的摘要/密文/hint，必须一并匿名化。命中 idx_identity_user_id。
SELECT * FROM identity WHERE user_id = $1 ORDER BY create_time, id;

-- name: AnonymizeIdentity :execrows
-- purge：用随机值替换 subject_digest / provider_subject（哪个非空替换哪个，保持 identity_subject_exactly_one），
-- 其余 PII 与密钥版本置 NULL；未软删的行同时软删。
UPDATE identity
SET subject_digest     = CASE WHEN subject_digest   IS NULL THEN NULL ELSE @replacement::text END,
    provider_subject   = CASE WHEN provider_subject IS NULL THEN NULL ELSE @replacement::text END,
    digest_key_version = NULL, cipher_key_version = NULL, subject_ciphertext = NULL,
    provider_meta = NULL, hint_prefix = NULL, hint_suffix = NULL,
    delete_time = COALESCE(delete_time, @now::timestamptz), update_time = @now::timestamptz
WHERE id = @id AND user_id = @user_id;

-- name: ListIdentitiesByDigestKeyVersion :many
-- 维护任务 rekey_digests：仍用旧 HMAC 版本的活跃锚点身份；命中 idx_identity_digest_key_version。
SELECT * FROM identity
WHERE delete_time IS NULL AND digest_key_version = @version::smallint
ORDER BY create_time, id
LIMIT @batch_size::int;

-- name: UpdateIdentityDigest :execrows
-- CAS：只有版本仍为 old_version 的活跃行才改写（并发副本/绑定已改则 0 行，调用方跳过不计）。
UPDATE identity
SET subject_digest = @digest::text, digest_key_version = @version::smallint, update_time = @now::timestamptz
WHERE id = @id AND digest_key_version = @old_version::smallint AND delete_time IS NULL;

-- name: ListIdentitiesByCipherKeyVersion :many
-- 维护任务 reencrypt_subjects：仍用旧加密版本的活跃锚点身份；命中 idx_identity_cipher_key_version。
SELECT * FROM identity
WHERE delete_time IS NULL AND cipher_key_version = @version::smallint
ORDER BY create_time, id
LIMIT @batch_size::int;

-- name: UpdateIdentityCiphertext :execrows
UPDATE identity
SET subject_ciphertext = @ciphertext, cipher_key_version = @version::smallint, update_time = @now::timestamptz
WHERE id = @id AND cipher_key_version = @old_version::smallint AND delete_time IS NULL;

-- name: ListActiveDigestKeyVersions :many
-- 启动期校验（Migrate → CheckKeyVersions）：活跃锚点身份实际使用的 HMAC 版本集合。
SELECT DISTINCT digest_key_version::smallint AS version FROM identity
WHERE delete_time IS NULL AND digest_key_version IS NOT NULL
ORDER BY 1;

-- name: ListActiveCipherKeyVersions :many
SELECT DISTINCT cipher_key_version::smallint AS version FROM identity
WHERE delete_time IS NULL AND cipher_key_version IS NOT NULL
ORDER BY 1;

-- session

-- name: CreateSession :one
INSERT INTO session (id, user_id, device_id, device_name, auth_time, refresh_token_hash, refresh_expire_time, last_used_time)
VALUES ($1, $2, $3, $4, $5, $6, $7, $5)
RETURNING *;

-- name: GetSessionByRefreshHash :one
SELECT * FROM session WHERE refresh_token_hash = $1;

-- name: GetSessionByPreviousRefreshHash :one
SELECT * FROM session WHERE previous_refresh_token_hash = $1;

-- name: LockSessionByID :one
-- 轮换前对会话行加锁：并发刷新在此串行化，CAS 失败者等到胜者提交（含宽限缓存写入）后再走宽限路径。
SELECT * FROM session WHERE id = $1 FOR UPDATE;

-- name: GetActiveSessionByUserDevice :one
-- 同一设备再次登录：先吊销旧会话（REPLACED_BY_RELOGIN）再建新会话。
-- 此处定位未吊销记录，包含刷新期限已到但尚未清理的会话。
SELECT * FROM session WHERE user_id = $1 AND device_id = $2 AND revoke_time IS NULL
ORDER BY create_time DESC, id DESC LIMIT 1;

-- name: RotateSession :execrows
-- CAS：只有当前哈希仍等于调用方读到的旧哈希时才轮换；并发刷新中只有一个成功，
-- 其余走宽限路径。旧哈希进入 previous_refresh_token_hash 供重放识别。
UPDATE session
SET refresh_token_hash = @new_hash, previous_refresh_token_hash = @old_hash,
    rotate_time = @now::timestamptz, refresh_expire_time = @refresh_expire_time,
    last_used_time = @now::timestamptz, update_time = @now::timestamptz
WHERE id = @id AND refresh_token_hash = @old_hash AND revoke_time IS NULL
  AND refresh_expire_time > @now::timestamptz;

-- name: RevokeSession :execrows
UPDATE session SET revoke_time = @now::timestamptz, revoke_reason = @reason, update_time = @now::timestamptz
WHERE id = @id AND revoke_time IS NULL;

-- name: RevokeSessionsByUser :many
-- 吊销该用户全部未吊销会话（包含到期行），可排除一个（revokeOthers 保留当前）。返回被吊销的 sid 供写入吊销集。
UPDATE session SET revoke_time = @now::timestamptz, revoke_reason = @reason, update_time = @now::timestamptz
WHERE user_id = @user_id AND revoke_time IS NULL AND (sqlc.narg('except_id')::text IS NULL OR id <> sqlc.narg('except_id')::text)
RETURNING id;

-- name: ListActiveSessionsByUser :many
SELECT * FROM session
WHERE user_id = @user_id AND revoke_time IS NULL AND refresh_expire_time > @now::timestamptz
ORDER BY create_time, id;

-- name: GetActiveSessionByIDAndUser :one
-- 明确撤销定位未吊销记录，不能因为刷新期限已到而遗漏仍有效的 access。
SELECT * FROM session WHERE id = $1 AND user_id = $2 AND revoke_time IS NULL;

-- name: UpdateSessionAuthTime :execrows
UPDATE session SET auth_time = @auth_time, update_time = @auth_time
WHERE id = @id AND revoke_time IS NULL AND refresh_expire_time > @auth_time::timestamptz;

-- name: DeleteStaleSessions :execrows
-- Lock a bounded batch before deleting context and parents atomically. Retention
-- is based on the original revoke/expiry time, never on snapshot observations.
WITH targets AS MATERIALIZED (
 SELECT id FROM session
 WHERE (revoke_time IS NOT NULL AND revoke_time < @before::timestamptz)
    OR (revoke_time IS NULL AND refresh_expire_time < @before::timestamptz)
 ORDER BY id LIMIT @batch_size::int FOR UPDATE
), removed_identities AS (
 DELETE FROM session_authentication_identity WHERE session_id IN (SELECT id FROM targets)
 RETURNING session_id
), removed_snapshots AS (
 DELETE FROM session_client_info WHERE session_id IN (SELECT id FROM targets)
 RETURNING session_id
)
DELETE FROM session WHERE id IN (SELECT id FROM targets);

-- name: CountActiveSessionsByUser :one
-- 管理端用户详情：活跃会话数；命中 idx_session_user_id。
SELECT count(*) FROM session
WHERE user_id = @user_id AND revoke_time IS NULL AND refresh_expire_time > @now::timestamptz;

-- audit_event

-- name: ScrubAuditEventsByUser :execrows
-- Materialize the user's historical flow closure once. JSONB containment uses
-- the existing resource GIN indexes without correlating a history scan per row.
WITH user_events AS MATERIALIZED (
 SELECT id, targets, context FROM audit_event
 WHERE source = @source::text AND (
  (actor->>'type' = 'USER' AND actor->>'namespace' = @namespace::text AND actor->>'id' = @user_id::text)
  OR targets @> jsonb_build_array(jsonb_build_object('type','USER','namespace',@namespace::text,'id',@user_id::text))
  OR context->'related_resources' @> jsonb_build_array(jsonb_build_object('type','USER','namespace',@namespace::text,'id',@user_id::text)))
), flow_resources AS MATERIALIZED (
 SELECT DISTINCT jsonb_build_array(jsonb_build_object('type','IDENTITY_FLOW','namespace',@namespace::text,'id',f->>'id')) AS resource
 FROM user_events linked
 CROSS JOIN LATERAL jsonb_array_elements(linked.targets || COALESCE(linked.context->'related_resources','[]'::jsonb)) f
 WHERE f->>'type' = 'IDENTITY_FLOW' AND f->>'namespace' = @namespace::text
), cleanup_ids AS MATERIALIZED (
 SELECT id FROM user_events
 UNION
 SELECT id FROM audit_event
 WHERE source = @source::text
 AND (targets @> ANY(ARRAY(SELECT resource FROM flow_resources))
      OR context->'related_resources' @> ANY(ARRAY(SELECT resource FROM flow_resources)))
)
UPDATE audit_event e SET
 actor = e.actor - 'name',
 targets = (SELECT COALESCE(jsonb_agg(r - 'name'),'[]'::jsonb) FROM jsonb_array_elements(e.targets) r),
 context = jsonb_set(e.context - 'client_ip' - 'device_id' - 'user_agent', '{related_resources}',
  (SELECT COALESCE(jsonb_agg(r - 'name'),'[]'::jsonb) FROM jsonb_array_elements(COALESCE(e.context->'related_resources','[]'::jsonb)) r)),
 data = CASE WHEN e.action = ANY(@authentication_actions::text[]) AND jsonb_typeof(e.data->'accountkit') = 'object'
  THEN e.data #- '{accountkit,subject_hint}' ELSE e.data END
WHERE e.id IN (SELECT id FROM cleanup_ids)
 AND (e.actor ? 'name' OR e.context ? 'client_ip' OR e.context ? 'device_id' OR e.context ? 'user_agent'
 OR (e.action = ANY(@authentication_actions::text[]) AND jsonb_typeof(e.data->'accountkit') = 'object' AND e.data->'accountkit' ? 'subject_hint')
 OR EXISTS (SELECT 1 FROM jsonb_array_elements(e.targets || COALESCE(e.context->'related_resources','[]'::jsonb)) p WHERE p ? 'name'));

-- name: ImportUser :exec
-- 受控离线导入：保存调用者提供的原 ID/时间及匿名墓碑，不执行 upsert。
INSERT INTO user_account (id, state, display_name, create_time, update_time, delete_time, purge_time)
VALUES (@id, @state, sqlc.narg('display_name'), @create_time::timestamptz, @update_time::timestamptz,
        sqlc.narg('delete_time')::timestamptz, sqlc.narg('purge_time')::timestamptz);

-- name: ImportIdentity :exec
-- 离线 PHONE/EMAIL 锚点使用库当前密钥，保留历史时间；冲突不得静默跳过。
INSERT INTO identity (id, user_id, kind, subject_digest, digest_key_version,
                      subject_ciphertext, cipher_key_version, hint_prefix, hint_suffix, create_time, update_time)
VALUES (@id, @user_id, @kind, sqlc.narg('subject_digest')::text, sqlc.narg('digest_key_version')::smallint,
        @subject_ciphertext, sqlc.narg('cipher_key_version')::smallint,
        sqlc.narg('hint_prefix')::text, sqlc.narg('hint_suffix')::text,
        @create_time::timestamptz, @update_time::timestamptz);

-- sign_in_completion_flow: callers lock existing users in sorted order before locking flows.
-- State constants: PENDING=1, VERIFIED=2, COMPLETED=3, EXPIRED=4, REJECTED=5.

-- name: CreateSignInCompletionFlow :one
INSERT INTO sign_in_completion_flow (
    id, token_digest, state, policy_fingerprint, payload, cipher_version,
    proof_user_ids, create_time, update_time, expire_time
)
VALUES (
    @id, @token_digest, @state, @policy_fingerprint, @payload, @cipher_version,
    @proof_user_ids::text[], @create_time::timestamptz, @create_time::timestamptz,
    @expire_time::timestamptz
)
RETURNING *;

-- name: GetSignInCompletionFlowByTokenDigest :one
SELECT * FROM sign_in_completion_flow WHERE token_digest = @token_digest;

-- name: LockSignInCompletionFlow :one
SELECT * FROM sign_in_completion_flow WHERE id = @id FOR UPDATE;

-- name: UpdateSignInCompletionFlow :one
UPDATE sign_in_completion_flow
SET state = @state, payload = @payload, cipher_version = @cipher_version,
    proof_user_ids = @proof_user_ids::text[], revision = revision + 1,
    update_time = @now::timestamptz
WHERE id = @id AND revision = @revision
  AND state IN (1, 2) AND expire_time > @now::timestamptz
RETURNING *;

-- name: CompleteSignInCompletionFlow :one
UPDATE sign_in_completion_flow
SET state = 3, payload = @payload, cipher_version = @cipher_version,
    proof_user_ids = @proof_user_ids::text[], user_id = @user_id::text,
    session_id = @session_id::text, revision = revision + 1,
    update_time = @now::timestamptz
WHERE id = @id AND revision = @revision
  AND state IN (1, 2) AND expire_time > @now::timestamptz
RETURNING *;

-- name: DeleteSignInCompletionFlowsForUser :execrows
WITH locked AS (
    SELECT id FROM sign_in_completion_flow
    WHERE user_id = @user_id::text OR proof_user_ids @> ARRAY[@user_id::text]
    ORDER BY id FOR UPDATE
)
DELETE FROM sign_in_completion_flow WHERE id IN (SELECT id FROM locked);

-- name: ScrubSignInCompletionFlowAuditForUser :execrows
WITH flow_resources AS MATERIALIZED (
 SELECT jsonb_build_array(jsonb_build_object('type','IDENTITY_FLOW','namespace',@namespace::text,'id',id)) AS resource
 FROM sign_in_completion_flow WHERE user_id = @user_id::text OR proof_user_ids @> ARRAY[@user_id::text]
)
UPDATE audit_event e SET
 actor = e.actor - 'name',
 targets = (SELECT COALESCE(jsonb_agg(r - 'name'),'[]'::jsonb) FROM jsonb_array_elements(e.targets) r),
 context = jsonb_set(e.context - 'client_ip' - 'device_id' - 'user_agent', '{related_resources}',
  (SELECT COALESCE(jsonb_agg(r - 'name'),'[]'::jsonb) FROM jsonb_array_elements(COALESCE(e.context->'related_resources','[]'::jsonb)) r)),
 data = CASE WHEN e.action = ANY(@authentication_actions::text[]) AND jsonb_typeof(e.data->'accountkit') = 'object'
  THEN e.data #- '{accountkit,subject_hint}' ELSE e.data END
WHERE e.source = @source::text AND (
 e.targets @> ANY(ARRAY(SELECT resource FROM flow_resources))
 OR e.context->'related_resources' @> ANY(ARRAY(SELECT resource FROM flow_resources))
) AND (e.actor ? 'name' OR e.context ? 'client_ip' OR e.context ? 'device_id' OR e.context ? 'user_agent'
 OR (e.action = ANY(@authentication_actions::text[]) AND jsonb_typeof(e.data->'accountkit') = 'object' AND e.data->'accountkit' ? 'subject_hint')
 OR EXISTS (SELECT 1 FROM jsonb_array_elements(e.targets || COALESCE(e.context->'related_resources','[]'::jsonb)) p WHERE p ? 'name'));

-- name: DeleteExpiredSignInCompletionFlows :execrows
DELETE FROM sign_in_completion_flow
WHERE id IN (
    SELECT id FROM sign_in_completion_flow WHERE expire_time <= @now::timestamptz
    ORDER BY expire_time, id LIMIT @batch_size::int FOR UPDATE SKIP LOCKED
);

-- name: RequiredSignInCompletionFlowCipherVersions :many
SELECT DISTINCT cipher_version FROM sign_in_completion_flow
WHERE expire_time > @now::timestamptz ORDER BY cipher_version;

-- name: AuditFlowBelongsToUser :one
SELECT EXISTS (SELECT 1 FROM sign_in_completion_flow WHERE id = @flow_id::text AND (user_id = @user_id::text OR proof_user_ids @> ARRAY[@user_id::text]));
-- name: LockSignInCompletionFlowsForUser :many
SELECT id FROM sign_in_completion_flow
WHERE user_id = @user_id::text OR proof_user_ids @> ARRAY[@user_id::text]
ORDER BY id FOR UPDATE;

-- name: LockAuditEventsForUserCleanup :many
-- Lock both cleanup phases together. Sorting each UPDATE separately would still
-- permit a live-flow event in phase one to precede a smaller ordinary event.
-- Soft deletion locks only live-flow events; purge includes the historical closure.
WITH user_events AS MATERIALIZED (
 SELECT id, targets, context FROM audit_event
 WHERE @include_user::boolean AND source = @source::text AND (
  (actor->>'type' = 'USER' AND actor->>'namespace' = @namespace::text AND actor->>'id' = @user_id::text)
  OR targets @> jsonb_build_array(jsonb_build_object('type','USER','namespace',@namespace::text,'id',@user_id::text))
  OR context->'related_resources' @> jsonb_build_array(jsonb_build_object('type','USER','namespace',@namespace::text,'id',@user_id::text)))
), flow_resources AS MATERIALIZED (
 SELECT jsonb_build_array(jsonb_build_object('type','IDENTITY_FLOW','namespace',@namespace::text,'id',id)) AS resource
 FROM sign_in_completion_flow WHERE user_id = @user_id::text OR proof_user_ids @> ARRAY[@user_id::text]
 UNION
 SELECT jsonb_build_array(jsonb_build_object('type','IDENTITY_FLOW','namespace',@namespace::text,'id',f->>'id')) AS resource
 FROM user_events linked
 CROSS JOIN LATERAL jsonb_array_elements(linked.targets || COALESCE(linked.context->'related_resources','[]'::jsonb)) f
 WHERE f->>'type' = 'IDENTITY_FLOW' AND f->>'namespace' = @namespace::text
), cleanup_ids AS MATERIALIZED (
 SELECT id FROM user_events
 UNION
 SELECT id FROM audit_event
 WHERE source = @source::text
 AND (targets @> ANY(ARRAY(SELECT resource FROM flow_resources))
      OR context->'related_resources' @> ANY(ARRAY(SELECT resource FROM flow_resources)))
)
SELECT e.id FROM audit_event e JOIN cleanup_ids c ON c.id = e.id
ORDER BY e.id FOR UPDATE OF e;

-- session authentication context

-- name: LockCurrentSessionForRead :one
-- Pair with the session row lock used by all context writers in one transaction.
SELECT * FROM session WHERE user_id = @user_id AND id = @session_id FOR SHARE;

-- name: ListSessionAuthenticationIdentities :many
SELECT a.identity_id, i.kind
FROM session_authentication_identity a
JOIN session s ON s.id = a.session_id
JOIN identity i ON i.id = a.identity_id AND i.user_id = s.user_id
WHERE s.user_id = @user_id AND s.id = @session_id
ORDER BY a.identity_id;

-- name: AddSessionAuthenticationIdentity :execrows
-- The service holds the user/session locks and validates enabled identity kinds.
INSERT INTO session_authentication_identity(session_id, identity_id)
SELECT s.id, i.id FROM session s
JOIN identity i ON i.user_id = s.user_id
WHERE s.user_id = @user_id AND s.id = @session_id AND s.revoke_time IS NULL
 AND i.id = @identity_id AND i.delete_time IS NULL
ON CONFLICT (session_id, identity_id) DO NOTHING;

-- name: DeleteSessionAuthenticationIdentities :execrows
DELETE FROM session_authentication_identity a USING session s
WHERE a.session_id = s.id AND s.user_id = @user_id AND s.id = @session_id;

-- name: MarkSessionAuthenticationIdentitiesKnown :execrows
UPDATE session SET authentication_identities_known = true, update_time = @now::timestamptz
WHERE user_id = @user_id AND id = @session_id AND revoke_time IS NULL
 AND EXISTS (SELECT 1 FROM session_authentication_identity a WHERE a.session_id = session.id);

-- name: RevokeSessionsByIdentity :many
-- Include unknown and refresh-expired sessions; lock targets in stable ID order.
WITH targets AS MATERIALIZED (
 SELECT s.id FROM session s
 WHERE s.user_id = @user_id AND s.revoke_time IS NULL
 AND (NOT s.authentication_identities_known OR EXISTS (
  SELECT 1 FROM session_authentication_identity a WHERE a.session_id = s.id AND a.identity_id = @identity_id))
 ORDER BY s.id FOR UPDATE
)
UPDATE session s SET revoke_time = @now::timestamptz, revoke_reason = @reason, update_time = @now::timestamptz
FROM targets t WHERE s.id = t.id AND s.user_id = @user_id AND s.revoke_time IS NULL
RETURNING s.id;

-- name: UpsertSessionClientInfo :execrows
-- Supply the fully merged normalized snapshot. INITIAL is immutable on conflict.
INSERT INTO session_client_info(session_id, snapshot_kind, client_type, device_type,
 os_name, os_version, browser_name, browser_version, app_name, app_version, device_model,
 observe_time, create_time, update_time)
SELECT s.id, @snapshot_kind, sqlc.narg('client_type'), sqlc.narg('device_type'),
 sqlc.narg('os_name'), sqlc.narg('os_version'), sqlc.narg('browser_name'), sqlc.narg('browser_version'),
 sqlc.narg('app_name'), sqlc.narg('app_version'), sqlc.narg('device_model'),
 @observe_time::timestamptz, @now::timestamptz, @now::timestamptz
FROM session s WHERE s.user_id = @user_id AND s.id = @session_id AND s.revoke_time IS NULL
ON CONFLICT (session_id, snapshot_kind) DO UPDATE SET
 client_type = EXCLUDED.client_type, device_type = EXCLUDED.device_type,
 os_name = EXCLUDED.os_name, os_version = EXCLUDED.os_version,
 browser_name = EXCLUDED.browser_name, browser_version = EXCLUDED.browser_version,
 app_name = EXCLUDED.app_name, app_version = EXCLUDED.app_version, device_model = EXCLUDED.device_model,
 observe_time = EXCLUDED.observe_time, update_time = EXCLUDED.update_time
WHERE session_client_info.snapshot_kind <> 1;

-- name: GetSessionClientInfo :one
SELECT c.* FROM session_client_info c JOIN session s ON s.id = c.session_id
WHERE s.user_id = @user_id AND s.id = @session_id AND c.snapshot_kind = @snapshot_kind;

-- name: ListSessionClientInfosByUser :many
-- Limit batch enrichment to the session IDs already selected by the caller.
SELECT c.* FROM session_client_info c JOIN session s ON s.id = c.session_id
WHERE s.user_id = @user_id AND s.id = ANY(@session_ids::text[]) AND c.snapshot_kind = @snapshot_kind
ORDER BY c.session_id;

-- name: DeleteSessionContextByUser :execrows
-- Call before anonymizing/deleting sessions, in the same user-locked transaction.
WITH removed_identities AS (
 DELETE FROM session_authentication_identity a USING session s
 WHERE a.session_id = s.id AND s.user_id = @user_id RETURNING a.session_id
)
DELETE FROM session_client_info c USING session s
WHERE c.session_id = s.id AND s.user_id = @user_id;

-- name: LockSessionsForUserContextCleanup :many
-- Lock historical and current sessions in the same order as revocation, before
-- removing context. Refresh holds a session lock without acquiring a user lock.
SELECT id FROM session WHERE user_id = @user_id ORDER BY id FOR UPDATE;
