ALTER TABLE user_account
    ADD COLUMN profile_attributes JSONB NOT NULL DEFAULT '{}'::jsonb;
