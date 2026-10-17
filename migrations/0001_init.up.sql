-- accountkit 初始 schema。所有对象位于连接 search_path 首位的 schema（由 migrations.Up 设置），
-- 因此表名不带前缀。账号/身份/会话/流程主键采用带类型前缀的 TSID，审计采用 UUIDv7；_time 时间戳，
-- SMALLINT 枚举无 CHECK（值由 Go 定义），全部表不设外键（一致性由 service 层与测试保证）。
--
-- User-authorized generic audit definition is included in this initial version.
-- Future changes must use version 0002 or higher; historical source snapshots remain frozen.

-- 用户（表名 user_account：user 是 PG 关键字）
CREATE TABLE user_account (
    id                    TEXT COLLATE "C" PRIMARY KEY
                          CONSTRAINT user_account_id_format CHECK (id ~ '^u_[0-9a-hjkmnp-tv-z]{13}$'),
    state                 SMALLINT NOT NULL,            -- ACTIVE / FROZEN / PENDING_DELETION / DELETED，Go 定义
    display_name          TEXT,
    freeze_time           TIMESTAMPTZ,
    freeze_reason         TEXT,
    freeze_actor_subject  TEXT,                         -- 管理员 OIDC sub 快照
    freeze_actor_username TEXT,
    delete_time           TIMESTAMPTZ,                  -- 软删除时刻（AIP-164）
    purge_time            TIMESTAMPTZ,                  -- 计划匿名化时刻；执行后为实际时刻
    create_time           TIMESTAMPTZ NOT NULL DEFAULT now(),
    update_time           TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- purge 维护任务：WHERE state = PENDING_DELETION AND purge_time <= now()
-- 枚举值由 Go 定义并在此钉死：ACTIVE=1, FROZEN=2, PENDING_DELETION=3, DELETED=4（0 为 UNSPECIFIED，不存储）
CREATE INDEX idx_user_account_purge_time ON user_account (purge_time) WHERE state = 3;
-- 管理端 GET /users（ListUsersAdmin）：ORDER BY create_time, id 的 keyset 分页；没有它每页都是全表排序
CREATE INDEX idx_user_account_create_time_id ON user_account (create_time, id);

-- 身份：一行一个。锚点类（PHONE/EMAIL）存 subject_digest + subject_ciphertext；
-- 第三方类（WECHAT/APPLE）存 provider_subject 明文。二者恰好一个非空。
CREATE TABLE identity (
    id                  TEXT COLLATE "C" PRIMARY KEY
                        CONSTRAINT identity_id_format CHECK (id ~ '^i_[0-9a-hjkmnp-tv-z]{13}$'),
    user_id             TEXT COLLATE "C" NOT NULL,
    kind                SMALLINT NOT NULL,              -- PHONE / EMAIL / WECHAT / APPLE，Go 定义
    subject_digest      TEXT COLLATE "C",               -- HMAC-SHA256 hex（锚点类）
    digest_key_version  SMALLINT,
    provider_subject    TEXT COLLATE "C",               -- UnionID / Apple sub（第三方类）
    subject_ciphertext  BYTEA,                          -- AES-GCM 密文（锚点类）
    cipher_key_version  SMALLINT,
    hint_prefix         TEXT COLLATE "C",               -- 掩码可见的前缀（§3.3）
    hint_suffix         TEXT COLLATE "C",               -- 掩码可见的后缀
    provider_meta       JSONB,                          -- 微信：{"openids": {"<appid>": "<openid>"}}
    create_time         TIMESTAMPTZ NOT NULL DEFAULT now(),
    update_time         TIMESTAMPTZ NOT NULL DEFAULT now(),
    delete_time         TIMESTAMPTZ,                    -- 解绑 = 软删
    CONSTRAINT identity_subject_exactly_one CHECK ((subject_digest IS NULL) <> (provider_subject IS NULL))
);
-- 登录查找 + "一个身份只属一个账号"：按 (kind, digest) 精确命中
CREATE UNIQUE INDEX uk_identity_kind_subject_digest ON identity (kind, subject_digest) WHERE delete_time IS NULL;
CREATE UNIQUE INDEX uk_identity_kind_provider_subject ON identity (kind, provider_subject) WHERE delete_time IS NULL;
-- users/me/identities 列表与 per-kind 计数（delete_time IS NULL 由查询自己过滤）；
-- purge 匿名化按 user_id 取**全部**行（含已解绑的软删行，§3.5），因此不能是 delete_time IS NULL 的部分索引
CREATE INDEX idx_identity_user_id ON identity (user_id);
-- 管理端 filter：identity.phone_suffix / email_domain
CREATE INDEX idx_identity_kind_hint_suffix ON identity (kind, hint_suffix) WHERE delete_time IS NULL;
-- 管理端 filter：identity.phone_prefix / email_prefix
CREATE INDEX idx_identity_kind_hint_prefix ON identity (kind, hint_prefix) WHERE delete_time IS NULL;
-- 维护任务 rekey_digests / reencrypt_subjects：按旧密钥版本取活跃锚点行；Migrate 的 CheckKeyVersions 取 DISTINCT 版本
CREATE INDEX idx_identity_digest_key_version ON identity (digest_key_version) WHERE delete_time IS NULL;
CREATE INDEX idx_identity_cipher_key_version ON identity (cipher_key_version) WHERE delete_time IS NULL;

-- 设备会话。id 即 JWT sid。
CREATE TABLE session (
    id                          TEXT COLLATE "C" PRIMARY KEY
                                CONSTRAINT session_id_format CHECK (id ~ '^s_[0-9a-hjkmnp-tv-z]{13}$'),
    user_id                     TEXT COLLATE "C" NOT NULL,
    device_id                   TEXT NOT NULL,
    device_name                 TEXT,
    auth_time                   TIMESTAMPTZ NOT NULL,   -- 最近登录/重新认证时刻，签发 access 时写入 claim
    refresh_token_hash          BYTEA NOT NULL,         -- SHA-256(refresh token)
    previous_refresh_token_hash BYTEA,                  -- 宽限期重放识别
    rotate_time                 TIMESTAMPTZ,
    refresh_expire_time         TIMESTAMPTZ NOT NULL,
    last_used_time              TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoke_time                 TIMESTAMPTZ,
    revoke_reason               SMALLINT,               -- Go 定义
    create_time                 TIMESTAMPTZ NOT NULL DEFAULT now(),
    update_time                 TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- /token 刷新：按当前 refresh 哈希精确命中
CREATE UNIQUE INDEX uk_session_refresh_token_hash ON session (refresh_token_hash);
-- 宽限外重放检测：按旧哈希命中
CREATE INDEX idx_session_previous_refresh_token_hash ON session (previous_refresh_token_hash) WHERE previous_refresh_token_hash IS NOT NULL;
-- users/me/sessions 列表、revokeOthers、冻结/删除时吊销全部
CREATE INDEX idx_session_user_id ON session (user_id) WHERE revoke_time IS NULL;
-- 维护任务 cleanup_sessions：清理吊销 30 天以上的会话（§3.2、§5.6 #2）
CREATE INDEX idx_session_revoke_time ON session (revoke_time) WHERE revoke_time IS NOT NULL;
-- 维护任务 cleanup_sessions：清理 refresh 过期 30 天以上且从未被吊销的会话
CREATE INDEX idx_session_refresh_expire_time ON session (refresh_expire_time) WHERE revoke_time IS NULL;

-- Generic audit events; append-only and independent of resource lifecycle.
-- These immutable validators also protect direct SQL writers. Application
-- validation additionally freezes canonical ordering and rejects duplicate keys.
CREATE FUNCTION audit_resource_valid(r jsonb) RETURNS boolean LANGUAGE plpgsql IMMUTABLE SET search_path FROM CURRENT AS $$
BEGIN
 RETURN coalesce(jsonb_typeof(r)='object'
  AND jsonb_typeof(r->'type')='string' AND (r->>'type') COLLATE "C" ~ '^[A-Z][A-Z0-9_]{0,127}$'
  AND jsonb_typeof(r->'namespace')='string' AND octet_length(r->>'namespace') BETWEEN 1 AND 512
  AND jsonb_typeof(r->'id')='string' AND octet_length(r->>'id') BETWEEN 1 AND 512
  AND (NOT r ? 'name' OR (jsonb_typeof(r->'name')='string' AND char_length(r->>'name')<=256))
  AND NOT EXISTS(SELECT 1 FROM jsonb_object_keys(r) k WHERE k NOT IN ('type','namespace','id','name')),false);
END $$;
CREATE FUNCTION audit_resources_valid(rs jsonb) RETURNS boolean LANGUAGE plpgsql IMMUTABLE SET search_path FROM CURRENT AS $$
BEGIN
 IF jsonb_typeof(rs) IS DISTINCT FROM 'array' THEN RETURN false; END IF;
 RETURN jsonb_array_length(rs)<=32 AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(rs) r WHERE NOT audit_resource_valid(r));
END $$;
CREATE FUNCTION audit_actor_valid(a jsonb) RETURNS boolean LANGUAGE plpgsql IMMUTABLE SET search_path FROM CURRENT AS $$
BEGIN
 IF jsonb_typeof(a) IS DISTINCT FROM 'object' THEN RETURN false; END IF;
 IF a->>'type' IN ('USER','SERVICE','SYSTEM') THEN RETURN audit_resource_valid(a); END IF;
 RETURN coalesce(a->>'type' IN ('ANONYMOUS','UNKNOWN') AND NOT a ? 'namespace' AND NOT a ? 'id'
  AND (NOT a ? 'name' OR (jsonb_typeof(a->'name')='string' AND char_length(a->>'name')<=256))
  AND NOT EXISTS(SELECT 1 FROM jsonb_object_keys(a) k WHERE k NOT IN ('type','name')),false);
END $$;
CREATE FUNCTION audit_context_valid(c jsonb) RETURNS boolean LANGUAGE plpgsql IMMUTABLE SET search_path FROM CURRENT AS $$
DECLARE k text; v jsonb; bound integer;
BEGIN
 IF jsonb_typeof(c) IS DISTINCT FROM 'object' THEN RETURN false; END IF;
 FOR k,v IN SELECT * FROM jsonb_each(c) LOOP
  IF k='related_resources' THEN
   IF NOT audit_resources_valid(v) THEN RETURN false; END IF;
  ELSE
   IF jsonb_typeof(v)<>'string' THEN RETURN false; END IF;
   bound:=CASE k WHEN 'request_id' THEN 128 WHEN 'device_id' THEN 256 WHEN 'user_agent' THEN 2048 WHEN 'client_ip' THEN 64 WHEN 'trace_id' THEN 32 WHEN 'span_id' THEN 16 ELSE 0 END;
   IF bound=0 OR octet_length(c->>k)>bound THEN RETURN false; END IF;
  END IF;
 END LOOP;
 IF c ? 'trace_id' AND ((c->>'trace_id') !~ '^[0-9a-f]{32}$' OR c->>'trace_id'=repeat('0',32)) THEN RETURN false; END IF;
 IF c ? 'span_id' AND (NOT c ? 'trace_id' OR (c->>'span_id') !~ '^[0-9a-f]{16}$' OR c->>'span_id'=repeat('0',16)) THEN RETURN false; END IF;
 IF c ? 'client_ip' THEN
  IF position('/' IN c->>'client_ip')>0 THEN RETURN false; END IF;
  PERFORM (c->>'client_ip')::inet;
 END IF;
 RETURN true;
EXCEPTION WHEN invalid_text_representation THEN RETURN false;
END $$;
-- Count the public Go encoding/json representation, without jsonb separator
-- spaces. JSON strings escape HTML and U+2028/U+2029; numeric values use the
-- same exact expanded decimal normalization as event.Freeze (never float64).
CREATE FUNCTION audit_json_bytes(j jsonb) RETURNS bigint LANGUAGE plpgsql IMMUTABLE SET search_path FROM CURRENT AS $$
DECLARE total bigint; n bigint; k text; v jsonb; s text;
BEGIN
 CASE jsonb_typeof(j)
 WHEN 'object' THEN
  total:=2; n:=0;
  FOR k,v IN SELECT * FROM jsonb_each(j) LOOP total:=total+audit_json_bytes(to_jsonb(k))+1+audit_json_bytes(v); n:=n+1; END LOOP;
  RETURN total+greatest(n-1,0);
 WHEN 'array' THEN
  total:=2; n:=0;
  FOR v IN SELECT * FROM jsonb_array_elements(j) LOOP total:=total+audit_json_bytes(v); n:=n+1; END LOOP;
  RETURN total+greatest(n-1,0);
 WHEN 'string' THEN
  s:=j#>>'{}';
  RETURN octet_length(j::text)
   + 5*(char_length(s)-char_length(replace(replace(replace(s,'<',''),'>',''),'&','')))
   + 3*(char_length(s)-char_length(replace(replace(s,chr(8232),''),chr(8233),'')));
 WHEN 'number' THEN RETURN octet_length(trim_scale((j#>>'{}')::numeric)::text);
 ELSE RETURN octet_length(j::text);
 END CASE;
END $$;

CREATE TABLE audit_event (
 id uuid CONSTRAINT audit_event_pkey PRIMARY KEY
  CONSTRAINT audit_event_id_v7 CHECK (get_byte(uuid_send(id),6)>>4=7 AND get_byte(uuid_send(id),8)&192=128),
 source varchar(128) COLLATE "C" NOT NULL CONSTRAINT audit_event_source_format CHECK(source ~ '^[A-Za-z0-9][A-Za-z0-9._/-]*$'),
 action varchar(128) COLLATE "C" NOT NULL CONSTRAINT audit_event_action_format CHECK(action ~ '^[A-Z][A-Z0-9_]*$'),
 -- Enum membership is checked by the public model, not frozen in a SQL CHECK.
 result smallint,
 actor jsonb NOT NULL CONSTRAINT audit_event_actor_valid CHECK(audit_actor_valid(actor)),
 targets jsonb NOT NULL DEFAULT '[]' CONSTRAINT audit_event_targets_valid CHECK(audit_resources_valid(targets)),
 context jsonb NOT NULL DEFAULT '{}' CONSTRAINT audit_event_context_valid CHECK(audit_context_valid(context)),
 data jsonb NOT NULL DEFAULT '{}' CONSTRAINT audit_event_data_object CHECK(jsonb_typeof(data)='object'),
 occur_time timestamptz(6) NOT NULL CONSTRAINT audit_event_occur_time_valid CHECK(
  isfinite(occur_time) AND occur_time>'0001-01-01 00:00:00+00'::timestamptz
  AND occur_time<'10000-01-01 00:00:00+00'::timestamptz),
 create_time timestamptz(6) NOT NULL DEFAULT clock_timestamp(),
 CONSTRAINT audit_event_size CHECK(audit_json_bytes(jsonb_build_object(
  'id',id,'source',source,'action',action,
  'result',CASE result WHEN 1 THEN to_jsonb('SUCCESS'::text) WHEN 2 THEN to_jsonb('FAILURE'::text) ELSE to_jsonb(result) END,
  'actor',actor,'targets',targets,'context',context,'data',data,
  'occur_time',to_char(occur_time AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS')
   ||coalesce('.'||nullif(rtrim(to_char(occur_time AT TIME ZONE 'UTC','US'),'0'),''),'')||'Z'))<=65536)
);
-- Stable timeline, source-bound retention, and exact resource membership.
CREATE INDEX idx_audit_event_occur_time_id ON audit_event(occur_time,id);
CREATE INDEX idx_audit_event_source_occur_time_id ON audit_event(source,occur_time,id);
CREATE INDEX idx_audit_event_actor ON audit_event(source,(actor->>'namespace'),(actor->>'id'),occur_time,id);
CREATE INDEX idx_audit_event_targets ON audit_event USING gin(targets jsonb_path_ops);
CREATE INDEX idx_audit_event_related_resources ON audit_event USING gin((context->'related_resources') jsonb_path_ops);
CREATE INDEX idx_audit_event_request_id ON audit_event(source,(context->>'request_id'));
-- Historical flow closure remains queryable after identity_flow cleanup.
CREATE INDEX idx_audit_event_flow_id ON audit_event(source,(data#>>'{accountkit,flow_id}'));

-- Short-lived sign-in completion; only token digests and context-bound encrypted payloads are stored.
CREATE TABLE identity_flow (
 id TEXT COLLATE "C" PRIMARY KEY CONSTRAINT identity_flow_id_format CHECK (id ~ '^f_[0-9a-hjkmnp-tv-z]{13}$'),
 token_digest TEXT COLLATE "C" NOT NULL,
 state SMALLINT NOT NULL,
 policy_fingerprint TEXT COLLATE "C" NOT NULL,
 payload BYTEA NOT NULL,
 cipher_version SMALLINT NOT NULL,
 revision BIGINT NOT NULL DEFAULT 1,
 proof_user_ids TEXT[] NOT NULL DEFAULT '{}',
 user_id TEXT COLLATE "C",
 session_id TEXT COLLATE "C",
 create_time TIMESTAMPTZ NOT NULL,
 update_time TIMESTAMPTZ NOT NULL,
 expire_time TIMESTAMPTZ NOT NULL
);
-- Token lookup uses a unique random-secret digest, separate from the internal resource ID.
CREATE UNIQUE INDEX uk_identity_flow_token_digest ON identity_flow(token_digest);
-- Bounded expiry cleanup scans in deterministic order.
CREATE INDEX idx_identity_flow_expire_time_id ON identity_flow(expire_time,id);
-- Account deletion/anonymization removes every proof owner and completed result reference.
CREATE INDEX idx_identity_flow_proof_user_ids ON identity_flow USING GIN(proof_user_ids);
CREATE INDEX idx_identity_flow_user_id ON identity_flow(user_id);
CREATE INDEX idx_identity_flow_session_id ON identity_flow(session_id);
