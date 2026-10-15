BEGIN;
SET LOCAL TIME ZONE 'UTC';
LOCK TABLE audit_event IN ACCESS EXCLUSIVE MODE;

-- Preparation is external to the migration engine. Empty tables need no tool;
-- nonempty tables must have a complete, unchanged, explicitly configured stage.
CREATE TABLE IF NOT EXISTS audit_event_upgrade_meta (
 singleton boolean PRIMARY KEY CHECK(singleton), format integer NOT NULL,
 schema_name text NOT NULL, source text NOT NULL, namespace text NOT NULL, config_digest bytea NOT NULL
);
CREATE TABLE IF NOT EXISTS audit_event_upgrade_stage (
 legacy_id text COLLATE "C" PRIMARY KEY, id uuid NOT NULL UNIQUE,
 original jsonb NOT NULL, event jsonb NOT NULL, event_digest bytea NOT NULL
);
LOCK TABLE audit_event_upgrade_meta,audit_event_upgrade_stage IN ACCESS EXCLUSIVE MODE;
DO $$
DECLARE meta audit_event_upgrade_meta%ROWTYPE;
BEGIN
 IF EXISTS(SELECT 1 FROM audit_event) OR EXISTS(SELECT 1 FROM audit_event_upgrade_meta) OR EXISTS(SELECT 1 FROM audit_event_upgrade_stage) THEN
  IF (SELECT count(*) FROM audit_event_upgrade_meta) <> 1 THEN
   RAISE EXCEPTION 'audit conversion requires controlled preparation';
  END IF;
  SELECT * INTO STRICT meta FROM audit_event_upgrade_meta;
  IF NOT meta.singleton OR meta.format<>1 OR meta.schema_name<>current_schema()
   OR meta.source !~ '^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$'
   OR octet_length(meta.namespace) NOT BETWEEN 1 AND 512
   OR meta.config_digest IS DISTINCT FROM sha256(convert_to(jsonb_build_array(meta.format,meta.schema_name,meta.source,meta.namespace)::text,'UTF8')) THEN
   RAISE EXCEPTION 'audit conversion configuration is inconsistent';
  END IF;
  IF EXISTS(SELECT 1 FROM audit_event a FULL JOIN audit_event_upgrade_stage s ON s.legacy_id=a.id
    WHERE a.id IS NULL OR s.legacy_id IS NULL OR s.original IS DISTINCT FROM to_jsonb(a)) THEN
   RAISE EXCEPTION 'audit conversion source differs from preparation';
  END IF;
  IF EXISTS(SELECT 1 FROM audit_event_upgrade_stage s WHERE
    s.event_digest IS DISTINCT FROM sha256(convert_to(s.event::text,'UTF8'))
    OR s.event->>'id' IS DISTINCT FROM s.id::text
    OR s.event->>'source' IS DISTINCT FROM meta.source
    OR s.event#>>'{data,accountkit,legacy_event_id}' IS DISTINCT FROM s.legacy_id
    OR (s.event->>'occur_time')::timestamptz IS DISTINCT FROM (s.original->>'occur_time')::timestamptz
    OR s.event->>'result' IS DISTINCT FROM CASE (s.original->>'result')::smallint WHEN 1 THEN 'SUCCESS' WHEN 2 THEN 'FAILURE' WHEN 0 THEN NULL ELSE 'INVALID' END
    OR (SELECT count(*) FROM jsonb_object_keys(s.event))<>9
    OR EXISTS(SELECT 1 FROM jsonb_array_elements((s.event->'targets') || coalesce(s.event#>'{context,related_resources}','[]'::jsonb)) r
      WHERE r->>'namespace' IS DISTINCT FROM meta.namespace)
  ) THEN RAISE EXCEPTION 'audit conversion stage is inconsistent'; END IF;
 END IF;
END $$;

-- These immutable validators also protect direct SQL writers. Application
-- validation additionally freezes canonical ordering and rejects duplicate keys.
CREATE FUNCTION audit_resource_valid(r jsonb) RETURNS boolean LANGUAGE plpgsql IMMUTABLE SET search_path FROM CURRENT AS $$
BEGIN
 RETURN coalesce(jsonb_typeof(r)='object'
  AND jsonb_typeof(r->'type')='string' AND (r->>'type') COLLATE "C" ~ '^[A-Z][A-Z0-9_]{0,127}$'
  AND jsonb_typeof(r->'namespace')='string' AND octet_length(r->>'namespace') BETWEEN 1 AND 512
  AND jsonb_typeof(r->'id')='string' AND octet_length(r->>'id') BETWEEN 1 AND 512
  AND (NOT r ? 'name' OR (jsonb_typeof(r->'name')='string' AND char_length(r->>'name')<=256))
  AND NOT EXISTS(SELECT 1 FROM jsonb_object_keys(r) k WHERE k NOT IN ('type','namespace','id','name')),false);
END $$;
CREATE FUNCTION audit_resources_valid(rs jsonb) RETURNS boolean LANGUAGE plpgsql IMMUTABLE SET search_path FROM CURRENT AS $$
BEGIN
 IF jsonb_typeof(rs) IS DISTINCT FROM 'array' THEN RETURN false; END IF;
 RETURN jsonb_array_length(rs)<=32 AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(rs) r WHERE NOT audit_resource_valid(r));
END $$;
CREATE FUNCTION audit_actor_valid(a jsonb) RETURNS boolean LANGUAGE plpgsql IMMUTABLE SET search_path FROM CURRENT AS $$
BEGIN
 IF jsonb_typeof(a) IS DISTINCT FROM 'object' THEN RETURN false; END IF;
 IF a->>'type' IN ('USER','SERVICE','SYSTEM') THEN RETURN audit_resource_valid(a); END IF;
 RETURN coalesce(a->>'type' IN ('ANONYMOUS','UNKNOWN') AND NOT a ? 'namespace' AND NOT a ? 'id'
  AND (NOT a ? 'name' OR (jsonb_typeof(a->'name')='string' AND char_length(a->>'name')<=256))
  AND NOT EXISTS(SELECT 1 FROM jsonb_object_keys(a) k WHERE k NOT IN ('type','name')),false);
END $$;
CREATE FUNCTION audit_context_valid(c jsonb) RETURNS boolean LANGUAGE plpgsql IMMUTABLE SET search_path FROM CURRENT AS $$
DECLARE k text; v jsonb; bound integer;
BEGIN
 IF jsonb_typeof(c) IS DISTINCT FROM 'object' THEN RETURN false; END IF;
 FOR k,v IN SELECT * FROM jsonb_each(c) LOOP
  IF k='related_resources' THEN
   IF NOT audit_resources_valid(v) THEN RETURN false; END IF;
  ELSE
   IF jsonb_typeof(v)<>'string' THEN RETURN false; END IF;
   bound:=CASE k WHEN 'request_id' THEN 128 WHEN 'device_id' THEN 256 WHEN 'user_agent' THEN 2048 WHEN 'client_ip' THEN 64 WHEN 'trace_id' THEN 32 WHEN 'span_id' THEN 16 ELSE 0 END;
   IF bound=0 OR octet_length(c->>k)>bound THEN RETURN false; END IF;
  END IF;
 END LOOP;
 IF c ? 'trace_id' AND ((c->>'trace_id') !~ '^[0-9a-f]{32}$' OR c->>'trace_id'=repeat('0',32)) THEN RETURN false; END IF;
 IF c ? 'span_id' AND (NOT c ? 'trace_id' OR (c->>'span_id') !~ '^[0-9a-f]{16}$' OR c->>'span_id'=repeat('0',16)) THEN RETURN false; END IF;
 IF c ? 'client_ip' THEN
  IF position('/' IN c->>'client_ip')>0 THEN RETURN false; END IF;
  PERFORM (c->>'client_ip')::inet;
 END IF;
 RETURN true;
EXCEPTION WHEN invalid_text_representation THEN RETURN false;
END $$;
-- Count the public Go encoding/json representation, without jsonb separator
-- spaces. JSON strings escape HTML and U+2028/U+2029; numeric values use the
-- same exact expanded decimal normalization as event.Freeze (never float64).
CREATE FUNCTION audit_json_bytes(j jsonb) RETURNS bigint LANGUAGE plpgsql IMMUTABLE SET search_path FROM CURRENT AS $$
DECLARE total bigint; n bigint; k text; v jsonb; s text;
BEGIN
 CASE jsonb_typeof(j)
 WHEN 'object' THEN
  total:=2; n:=0;
  FOR k,v IN SELECT * FROM jsonb_each(j) LOOP total:=total+audit_json_bytes(to_jsonb(k))+1+audit_json_bytes(v); n:=n+1; END LOOP;
  RETURN total+greatest(n-1,0);
 WHEN 'array' THEN
  total:=2; n:=0;
  FOR v IN SELECT * FROM jsonb_array_elements(j) LOOP total:=total+audit_json_bytes(v); n:=n+1; END LOOP;
  RETURN total+greatest(n-1,0);
 WHEN 'string' THEN
  s:=j#>>'{}';
  RETURN octet_length(j::text)
   + 5*(char_length(s)-char_length(replace(replace(replace(s,'<',''),'>',''),'&','')))
   + 3*(char_length(s)-char_length(replace(replace(s,chr(8232),''),chr(8233),'')));
 WHEN 'number' THEN RETURN octet_length(trim_scale((j#>>'{}')::numeric)::text);
 ELSE RETURN octet_length(j::text);
 END CASE;
END $$;

ALTER TABLE audit_event RENAME TO audit_event_legacy;
ALTER TABLE audit_event_legacy RENAME CONSTRAINT audit_event_pkey TO audit_event_legacy_pkey;
CREATE TABLE audit_event (
 id uuid CONSTRAINT audit_event_pkey PRIMARY KEY
  CONSTRAINT audit_event_id_v7 CHECK (get_byte(uuid_send(id),6)>>4=7 AND get_byte(uuid_send(id),8)&192=128),
 source varchar(128) COLLATE "C" NOT NULL CONSTRAINT audit_event_source_format CHECK(source ~ '^[A-Za-z0-9][A-Za-z0-9._/-]*$'),
 action varchar(128) COLLATE "C" NOT NULL CONSTRAINT audit_event_action_format CHECK(action ~ '^[A-Z][A-Z0-9_]*$'),
 -- Enum membership is checked by the public model, not frozen in a SQL CHECK.
 result smallint,
 actor jsonb NOT NULL CONSTRAINT audit_event_actor_valid CHECK(audit_actor_valid(actor)),
 targets jsonb NOT NULL DEFAULT '[]' CONSTRAINT audit_event_targets_valid CHECK(audit_resources_valid(targets)),
 context jsonb NOT NULL DEFAULT '{}' CONSTRAINT audit_event_context_valid CHECK(audit_context_valid(context)),
 data jsonb NOT NULL DEFAULT '{}' CONSTRAINT audit_event_data_object CHECK(jsonb_typeof(data)='object'),
 occur_time timestamptz(6) NOT NULL CONSTRAINT audit_event_occur_time_valid CHECK(
  isfinite(occur_time) AND occur_time>'0001-01-01 00:00:00+00'::timestamptz
  AND occur_time<'10000-01-01 00:00:00+00'::timestamptz),
 create_time timestamptz(6) NOT NULL DEFAULT clock_timestamp(),
 CONSTRAINT audit_event_size CHECK(audit_json_bytes(jsonb_build_object(
  'id',id,'source',source,'action',action,
  'result',CASE result WHEN 1 THEN to_jsonb('SUCCESS'::text) WHEN 2 THEN to_jsonb('FAILURE'::text) ELSE to_jsonb(result) END,
  'actor',actor,'targets',targets,'context',context,'data',data,
  'occur_time',to_char(occur_time AT TIME ZONE 'UTC','YYYY-MM-DD"T"HH24:MI:SS')
   ||coalesce('.'||nullif(rtrim(to_char(occur_time AT TIME ZONE 'UTC','US'),'0'),''),'')||'Z'))<=65536)
);
INSERT INTO audit_event(id,source,action,result,actor,targets,context,data,occur_time)
 SELECT id,event->>'source',event->>'action',CASE event->>'result' WHEN 'SUCCESS' THEN 1 WHEN 'FAILURE' THEN 2 END,event->'actor',event->'targets',event->'context',event->'data',(event->>'occur_time')::timestamptz
 FROM audit_event_upgrade_stage;
DROP TABLE audit_event_legacy;
DROP TABLE audit_event_upgrade_stage;
DROP TABLE audit_event_upgrade_meta;

-- Stable timeline, source-bound retention, and exact resource membership.
CREATE INDEX audit_event_occur_time_id_idx ON audit_event(occur_time,id);
CREATE INDEX audit_event_source_occur_time_id_idx ON audit_event(source,occur_time,id);
CREATE INDEX audit_event_actor_idx ON audit_event(source,(actor->>'namespace'),(actor->>'id'),occur_time,id);
CREATE INDEX audit_event_targets_idx ON audit_event USING gin(targets jsonb_path_ops);
CREATE INDEX audit_event_related_resources_idx ON audit_event USING gin((context->'related_resources') jsonb_path_ops);
CREATE INDEX audit_event_request_id_idx ON audit_event(source,(context->>'request_id'));
-- Historical flow closure remains queryable after identity_flow cleanup.
CREATE INDEX audit_event_flow_id_idx ON audit_event(source,(data#>>'{accountkit,flow_id}'));
COMMIT;
