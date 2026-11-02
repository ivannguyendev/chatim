CREATE TABLE tenants (
  id text PRIMARY KEY,
  name text NOT NULL,
  state smallint NOT NULL,
  ver bigint NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  updated_by text NOT NULL,
  sessions_revoked_at timestamptz
);

CREATE TABLE users (
  tenant_id text NOT NULL REFERENCES tenants (id),
  id text NOT NULL,
  display_name text NOT NULL,
  avatar_url text NOT NULL DEFAULT '',
  state smallint NOT NULL,
  ver bigint NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  updated_by text NOT NULL,
  PRIMARY KEY (tenant_id, id)
);

CREATE TABLE trusted_issuers (
  issuer text PRIMARY KEY,
  tenant_id text NOT NULL REFERENCES tenants (id),
  state smallint NOT NULL,
  ver bigint NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  updated_by text NOT NULL
);

CREATE TABLE trusted_keys (
  issuer text NOT NULL REFERENCES trusted_issuers (issuer),
  key_id text NOT NULL,
  algorithm text NOT NULL CHECK (algorithm IN ('EdDSA', 'ES256', 'RS256')),
  public_key jsonb NOT NULL,
  state smallint NOT NULL,
  created_at timestamptz NOT NULL,
  created_by text NOT NULL,
  PRIMARY KEY (issuer, key_id)
);

CREATE TABLE signing_keys (
  kid text PRIMARY KEY,
  state smallint NOT NULL,
  public_key bytea NOT NULL,
  private_key_sealed bytea NOT NULL,
  created_at timestamptz NOT NULL,
  activated_at timestamptz,
  retired_at timestamptz
);

CREATE UNIQUE INDEX signing_keys_one_active ON signing_keys (state) WHERE state = 2;

CREATE TABLE audit_events (
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  tenant_id text NOT NULL,
  at timestamptz NOT NULL,
  actor text NOT NULL,
  action text NOT NULL,
  user_id text,
  detail jsonb NOT NULL
);

CREATE INDEX audit_events_tenant_at ON audit_events (tenant_id, at);

CREATE TABLE revoked_sessions (
  session_id text PRIMARY KEY,
  tenant_id text NOT NULL,
  revoked_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL
);

CREATE INDEX revoked_sessions_expires ON revoked_sessions (expires_at);

CREATE TABLE groups (
  tenant_id text NOT NULL REFERENCES tenants (id),
  id text NOT NULL,
  name text NOT NULL,
  parent_id text,
  mentionable boolean NOT NULL DEFAULT true,
  state smallint NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  updated_by text NOT NULL,
  ancestors text[] NOT NULL DEFAULT '{}',
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, parent_id) REFERENCES groups (tenant_id, id)
);

CREATE INDEX groups_ancestors ON groups USING gin (ancestors);

CREATE TABLE user_groups (
  tenant_id text NOT NULL,
  user_id text NOT NULL,
  group_id text NOT NULL,
  PRIMARY KEY (tenant_id, user_id, group_id),
  FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id),
  FOREIGN KEY (tenant_id, group_id) REFERENCES groups (tenant_id, id)
);

CREATE INDEX user_groups_group ON user_groups (tenant_id, group_id);

CREATE TABLE directory_changes (
  kind smallint NOT NULL,
  tenant_id text NOT NULL,
  id text NOT NULL,
  ver bigint NOT NULL,
  change_xid xid8 NOT NULL,
  PRIMARY KEY (kind, tenant_id, id)
);

CREATE INDEX directory_changes_position ON directory_changes (change_xid, kind, tenant_id, id);

CREATE FUNCTION directory_change_upsert(change_kind smallint, change_tenant text, change_id text) RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO directory_changes (kind, tenant_id, id, ver, change_xid)
  VALUES (change_kind, change_tenant, change_id, 1, pg_current_xact_id())
  ON CONFLICT (kind, tenant_id, id) DO UPDATE
  SET ver = directory_changes.ver + 1, change_xid = pg_current_xact_id();
END
$$;

CREATE FUNCTION users_directory_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  PERFORM directory_change_upsert(1::smallint, NEW.tenant_id, NEW.id);
  RETURN NULL;
END
$$;

CREATE TRIGGER users_directory_insert AFTER INSERT ON users
FOR EACH ROW EXECUTE FUNCTION users_directory_change();

CREATE TRIGGER users_directory_update AFTER UPDATE ON users
FOR EACH ROW
WHEN (OLD.display_name IS DISTINCT FROM NEW.display_name
  OR OLD.avatar_url IS DISTINCT FROM NEW.avatar_url
  OR OLD.state IS DISTINCT FROM NEW.state)
EXECUTE FUNCTION users_directory_change();

CREATE FUNCTION user_groups_directory_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    PERFORM directory_change_upsert(1::smallint, OLD.tenant_id, OLD.user_id);
  ELSE
    PERFORM directory_change_upsert(1::smallint, NEW.tenant_id, NEW.user_id);
  END IF;
  RETURN NULL;
END
$$;

CREATE TRIGGER user_groups_directory AFTER INSERT OR DELETE ON user_groups
FOR EACH ROW EXECUTE FUNCTION user_groups_directory_change();

CREATE FUNCTION groups_directory_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  PERFORM directory_change_upsert(2::smallint, NEW.tenant_id, NEW.id);
  RETURN NULL;
END
$$;

CREATE TRIGGER groups_directory_insert AFTER INSERT ON groups
FOR EACH ROW EXECUTE FUNCTION groups_directory_change();

CREATE TRIGGER groups_directory_update AFTER UPDATE ON groups
FOR EACH ROW
WHEN (OLD.name IS DISTINCT FROM NEW.name
  OR OLD.parent_id IS DISTINCT FROM NEW.parent_id
  OR OLD.mentionable IS DISTINCT FROM NEW.mentionable
  OR OLD.state IS DISTINCT FROM NEW.state)
EXECUTE FUNCTION groups_directory_change();

CREATE FUNCTION groups_ancestors_set() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  parent_path text[];
BEGIN
  IF TG_OP = 'UPDATE' AND OLD.parent_id IS NOT DISTINCT FROM NEW.parent_id THEN
    RETURN NEW;
  END IF;
  IF NEW.parent_id IS NULL THEN
    NEW.ancestors := '{}';
    RETURN NEW;
  END IF;
  SELECT ancestors || id INTO parent_path FROM groups WHERE tenant_id = NEW.tenant_id AND id = NEW.parent_id;
  IF NEW.id = NEW.parent_id OR NEW.id = ANY (parent_path) THEN
    RAISE EXCEPTION 'group % cannot move under its own descendant %', NEW.id, NEW.parent_id
      USING ERRCODE = 'check_violation', CONSTRAINT = 'groups_no_cycle';
  END IF;
  NEW.ancestors := coalesce(parent_path, '{}');
  RETURN NEW;
END
$$;

CREATE TRIGGER groups_ancestors_set BEFORE INSERT OR UPDATE OF parent_id ON groups
FOR EACH ROW EXECUTE FUNCTION groups_ancestors_set();

CREATE FUNCTION groups_ancestors_cascade() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  UPDATE groups
  SET ancestors = NEW.ancestors || NEW.id || ancestors[array_position(ancestors, NEW.id) + 1:]
  WHERE tenant_id = NEW.tenant_id AND ancestors @> ARRAY[NEW.id];
  RETURN NULL;
END
$$;

CREATE TRIGGER groups_ancestors_cascade AFTER UPDATE OF parent_id ON groups
FOR EACH ROW
WHEN (OLD.parent_id IS DISTINCT FROM NEW.parent_id)
EXECUTE FUNCTION groups_ancestors_cascade();

CREATE FUNCTION identity_changes_notify() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_notify('identity_changes', TG_ARGV[0]);
  RETURN NULL;
END
$$;

CREATE TRIGGER directory_changes_notify AFTER INSERT OR UPDATE ON directory_changes
FOR EACH STATEMENT EXECUTE FUNCTION identity_changes_notify('dir');

CREATE FUNCTION revoked_sessions_inserted_notify() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM inserted_revocations) THEN
    PERFORM pg_notify('identity_changes', 'rev');
  END IF;
  RETURN NULL;
END
$$;

CREATE TRIGGER revoked_sessions_notify AFTER INSERT ON revoked_sessions
REFERENCING NEW TABLE AS inserted_revocations
FOR EACH STATEMENT EXECUTE FUNCTION revoked_sessions_inserted_notify();

CREATE TRIGGER tenants_sessions_revoked_notify AFTER UPDATE OF sessions_revoked_at ON tenants
FOR EACH ROW
WHEN (OLD.sessions_revoked_at IS DISTINCT FROM NEW.sessions_revoked_at)
EXECUTE FUNCTION identity_changes_notify('rev');
