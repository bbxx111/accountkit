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
CREATE UNIQUE INDEX identity_flow_token_digest_uidx ON identity_flow(token_digest);
-- Bounded expiry cleanup scans in deterministic order.
CREATE INDEX identity_flow_expire_time_id_idx ON identity_flow(expire_time,id);
-- Account deletion/anonymization removes every proof owner and completed result reference.
CREATE INDEX identity_flow_proof_user_ids_idx ON identity_flow USING GIN(proof_user_ids);
CREATE INDEX identity_flow_user_id_idx ON identity_flow(user_id);
CREATE INDEX identity_flow_session_id_idx ON identity_flow(session_id);
