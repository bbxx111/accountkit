-- Preserve the published initial migration; old sessions remain source-unknown.
ALTER TABLE session ADD COLUMN authentication_identities_known BOOLEAN NOT NULL DEFAULT false;

CREATE TABLE session_authentication_identity (
 session_id TEXT COLLATE "C" NOT NULL CONSTRAINT session_authentication_identity_session_id_format CHECK (session_id ~ '^s_[0-9a-hjkmnp-tv-z]{13}$'),
 identity_id TEXT COLLATE "C" NOT NULL CONSTRAINT session_authentication_identity_identity_id_format CHECK (identity_id ~ '^i_[0-9a-hjkmnp-tv-z]{13}$'),
 CONSTRAINT session_authentication_identity_pkey PRIMARY KEY (session_id, identity_id)
);
-- Identity removal locates dependent sessions, including refresh-expired rows.
CREATE INDEX idx_session_authentication_identity_identity_id_session_id ON session_authentication_identity(identity_id, session_id);

CREATE TABLE session_client_info (
 session_id TEXT COLLATE "C" NOT NULL CONSTRAINT session_client_info_session_id_format CHECK (session_id ~ '^s_[0-9a-hjkmnp-tv-z]{13}$'),
 snapshot_kind SMALLINT NOT NULL,
 client_type SMALLINT,
 device_type SMALLINT,
 os_name VARCHAR(64),
 os_version VARCHAR(32),
 browser_name VARCHAR(64),
 browser_version VARCHAR(32),
 app_name VARCHAR(64),
 app_version VARCHAR(32),
 device_model VARCHAR(64),
 observe_time TIMESTAMPTZ NOT NULL,
 create_time TIMESTAMPTZ NOT NULL DEFAULT now(),
 update_time TIMESTAMPTZ NOT NULL DEFAULT now(),
 CONSTRAINT session_client_info_pkey PRIMARY KEY (session_id, snapshot_kind)
);
-- Anonymization also visits revoked historical sessions; the existing user index
-- covers only non-revoked rows. This index also supports stable scoped lock order.
CREATE INDEX idx_session_user_id_id ON session(user_id, id);
