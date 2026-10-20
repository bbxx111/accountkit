-- Public Down remains disabled; this definition is not production deletion authorization.
ALTER TABLE user_account
    DROP COLUMN extensions,
    DROP COLUMN extensions_schema_version,
    DROP COLUMN extensions_schema_fingerprint,
    DROP COLUMN extensions_revision,
    DROP COLUMN extensions_update_time;
