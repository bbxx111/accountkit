-- Public Down remains disabled; this definition is not production deletion authorization.
ALTER TABLE user_account
    DROP COLUMN profile_attributes,
    DROP COLUMN profile_attributes_schema_version,
    DROP COLUMN profile_attributes_schema_fingerprint,
    DROP COLUMN profile_attributes_update_time;
