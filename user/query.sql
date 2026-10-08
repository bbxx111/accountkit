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
-- purge 任务取批。谓词写死 state = 3（PENDING_DELETION）而非绑定参数：它必须与 user_account_purge_time_idx
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
-- 由 user_account_create_time_id_idx 提供索引序，避免每页全表排序。
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
-- 会让该版本下写入的行查不到，同一手机号就会被再次注册。命中 identity_kind_subject_digest_uidx。
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
-- purge 专用：含已解绑（软删）的行——它们仍带原 subject 的摘要/密文/hint，必须一并匿名化。命中 identity_user_id_idx。
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
-- 维护任务 rekey_digests：仍用旧 HMAC 版本的活跃锚点身份；命中 identity_digest_key_version_idx。
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
-- 维护任务 reencrypt_subjects：仍用旧加密版本的活跃锚点身份；命中 identity_cipher_key_version_idx。
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
-- 维护任务 cleanup_sessions：物理删除吊销/过期超过保留期的会话；子查询限批，避免单事务锁太多行。
-- 两个 OR 分支分别命中 session_revoke_time_idx / session_refresh_expire_time_idx。
DELETE FROM session
WHERE id IN (
    SELECT id FROM session
    WHERE (revoke_time IS NOT NULL AND revoke_time < @before::timestamptz)
       OR (revoke_time IS NULL AND refresh_expire_time < @before::timestamptz)
    LIMIT @batch_size::int
);

-- name: CountActiveSessionsByUser :one
-- 管理端用户详情：活跃会话数；命中 session_user_id_idx。
SELECT count(*) FROM session
WHERE user_id = @user_id AND revoke_time IS NULL AND refresh_expire_time > @now::timestamptz;

-- audit_event

-- name: ScrubAuditEventsByUser :execrows
-- purge：去掉可关联到自然人的请求侧字段，保留事件与 user_id（§3.5）。命中 audit_event_user_id_occur_time_idx 前缀。
UPDATE audit_event SET ip = NULL, device_id = NULL, subject_hint = NULL
WHERE user_id = @user_id::text AND (ip IS NOT NULL OR device_id IS NOT NULL OR subject_hint IS NOT NULL);

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
