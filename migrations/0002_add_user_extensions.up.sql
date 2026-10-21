-- Small, schema-validated profile data shares the existing account row lock.
-- JSON business rules remain in Go; these checks protect storage metadata only.
ALTER TABLE user_account
    ADD COLUMN profile_attributes JSONB NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN profile_attributes_schema_version TEXT,
    ADD COLUMN profile_attributes_schema_fingerprint TEXT,
    ADD COLUMN profile_attributes_update_time TIMESTAMPTZ,
    ADD CONSTRAINT user_account_profile_attributes_schema_pair CHECK ((profile_attributes_schema_version IS NULL) = (profile_attributes_schema_fingerprint IS NULL)),
    ADD CONSTRAINT user_account_profile_attributes_schema_version_format CHECK (profile_attributes_schema_version ~ '^[A-Za-z0-9._-]{1,64}$'),
    ADD CONSTRAINT user_account_profile_attributes_schema_fingerprint_format CHECK (profile_attributes_schema_fingerprint ~ '^[0-9a-f]{64}$');
