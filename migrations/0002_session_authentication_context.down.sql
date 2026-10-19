DROP INDEX idx_session_user_id_id;
DROP TABLE session_client_info;
DROP TABLE session_authentication_identity;
ALTER TABLE session DROP COLUMN authentication_identities_known;
