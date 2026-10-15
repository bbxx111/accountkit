-- DEVELOPMENT/TEST UnsafeReset only; this destroys audit history. Public Down
-- remains disabled. Production rollback requires a separately verified backup.
DROP TABLE IF EXISTS audit_event;
DROP FUNCTION IF EXISTS audit_context_valid(jsonb);
DROP FUNCTION IF EXISTS audit_actor_valid(jsonb);
DROP FUNCTION IF EXISTS audit_resources_valid(jsonb);
DROP FUNCTION IF EXISTS audit_resource_valid(jsonb);
DROP FUNCTION IF EXISTS audit_json_bytes(jsonb);
