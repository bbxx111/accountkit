-- auth-server 初始 schema。所有对象位于连接 search_path 首位的 schema（由 migrations.Up 设置），
-- 因此表名不带前缀。规则见设计文档 §3.1–3.2：TSID 文本主键带类型前缀，_time 时间戳，
-- SMALLINT 枚举无 CHECK（值由 Go 定义），全部表不设外键（一致性由 service 层与测试保证）。
--
-- 尚未部署任何环境前允许原地修改本文件；一旦部署，后续变更从 0002 起新增文件。

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
CREATE INDEX user_account_purge_time_idx ON user_account (purge_time) WHERE state = 3;
-- 管理端 GET /users（ListUsersAdmin）：ORDER BY create_time, id 的 keyset 分页；没有它每页都是全表排序
CREATE INDEX user_account_create_time_id_idx ON user_account (create_time, id);

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
CREATE UNIQUE INDEX identity_kind_subject_digest_uidx ON identity (kind, subject_digest) WHERE delete_time IS NULL;
CREATE UNIQUE INDEX identity_kind_provider_subject_uidx ON identity (kind, provider_subject) WHERE delete_time IS NULL;
-- users/me/identities 列表与 per-kind 计数（delete_time IS NULL 由查询自己过滤）；
-- purge 匿名化按 user_id 取**全部**行（含已解绑的软删行，§3.5），因此不能是 delete_time IS NULL 的部分索引
CREATE INDEX identity_user_id_idx ON identity (user_id);
-- 管理端 filter：identity.phone_suffix / email_domain
CREATE INDEX identity_kind_hint_suffix_idx ON identity (kind, hint_suffix) WHERE delete_time IS NULL;
-- 管理端 filter：identity.phone_prefix / email_prefix
CREATE INDEX identity_kind_hint_prefix_idx ON identity (kind, hint_prefix) WHERE delete_time IS NULL;
-- 维护任务 rekey_digests / reencrypt_subjects：按旧密钥版本取活跃锚点行；Migrate 的 CheckKeyVersions 取 DISTINCT 版本
CREATE INDEX identity_digest_key_version_idx ON identity (digest_key_version) WHERE delete_time IS NULL;
CREATE INDEX identity_cipher_key_version_idx ON identity (cipher_key_version) WHERE delete_time IS NULL;

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
CREATE UNIQUE INDEX session_refresh_token_hash_uidx ON session (refresh_token_hash);
-- 宽限外重放检测：按旧哈希命中
CREATE INDEX session_previous_refresh_token_hash_idx ON session (previous_refresh_token_hash) WHERE previous_refresh_token_hash IS NOT NULL;
-- users/me/sessions 列表、revokeOthers、冻结/删除时吊销全部
CREATE INDEX session_user_id_idx ON session (user_id) WHERE revoke_time IS NULL;
-- 维护任务 cleanup_sessions：清理吊销 30 天以上的会话（§3.2、§5.6 #2）
CREATE INDEX session_revoke_time_idx ON session (revoke_time) WHERE revoke_time IS NOT NULL;
-- 维护任务 cleanup_sessions：清理 refresh 过期 30 天以上且从未被吊销的会话
CREATE INDEX session_refresh_expire_time_idx ON session (refresh_expire_time) WHERE revoke_time IS NULL;

-- 审计事件（追加写，无外键：日志生命周期独立于主体）
CREATE TABLE audit_event (
    id              TEXT COLLATE "C" CONSTRAINT audit_event_pkey PRIMARY KEY
                    CONSTRAINT audit_event_id_format CHECK (id ~ '^e_[0-9a-hjkmnp-tv-z]{13}$'),
    event_type      SMALLINT NOT NULL,
    actor_kind      SMALLINT NOT NULL,                  -- USER / ADMIN / SYSTEM
    user_id         TEXT COLLATE "C",
    admin_issuer    TEXT,
    admin_subject   TEXT,
    admin_username  TEXT,
    session_id      TEXT COLLATE "C",
    identity_kind   SMALLINT,
    subject_hint    TEXT,                               -- digest 或 provider_subject 前 8 位
    result          SMALLINT NOT NULL,                  -- SUCCESS / FAILURE
    reason          TEXT,
    ip              INET,
    device_id       TEXT,
    request_id      TEXT,
    occur_time      TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- 管理端 users/{user}/auditEvents 分页：ORDER BY occur_time, id（§3.4 勘误：本表没有 create_time；
-- 同一事务/同一毫秒可能多条事件，id 作 tiebreaker 才能做稳定游标）
CREATE INDEX audit_event_user_id_occur_time_idx ON audit_event (user_id, occur_time, id);
-- 保留期清理
CREATE INDEX audit_event_occur_time_idx ON audit_event (occur_time);
