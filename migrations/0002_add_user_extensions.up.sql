-- Small, schema-validated profile data shares the existing account row lock.
-- JSON business rules remain in Go; these checks protect storage metadata only.
ALTER TABLE user_account
    ADD COLUMN extensions JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN extensions_schema_version TEXT,
    ADD COLUMN extensions_schema_fingerprint TEXT,
    ADD COLUMN extensions_revision BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN extensions_update_time TIMESTAMPTZ,
    ADD CONSTRAINT user_account_extensions_revision_nonnegative CHECK (extensions_revision >= 0),
    ADD CONSTRAINT user_account_extensions_schema_pair CHECK ((extensions_schema_version IS NULL) = (extensions_schema_fingerprint IS NULL)),
    ADD CONSTRAINT user_account_extensions_schema_version_format CHECK (extensions_schema_version ~ '^[A-Za-z0-9._-]{1,64}$'),
    ADD CONSTRAINT user_account_extensions_schema_fingerprint_format CHECK (extensions_schema_fingerprint ~ '^[0-9a-f]{64}$');
