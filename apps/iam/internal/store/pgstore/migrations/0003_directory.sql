ALTER TABLE accounts ADD COLUMN display_name text;
ALTER TABLE accounts ADD COLUMN avatar_url text NOT NULL DEFAULT '';

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
  PRIMARY KEY (tenant_id, id),
  FOREIGN KEY (tenant_id, parent_id) REFERENCES groups (tenant_id, id)
);

CREATE TABLE account_groups (
  tenant_id text NOT NULL,
  account_id text NOT NULL,
  group_id text NOT NULL,
  PRIMARY KEY (tenant_id, account_id, group_id),
  FOREIGN KEY (tenant_id, account_id) REFERENCES accounts (tenant_id, id),
  FOREIGN KEY (tenant_id, group_id) REFERENCES groups (tenant_id, id)
);

CREATE INDEX account_groups_group ON account_groups (tenant_id, group_id);

CREATE TABLE directory_changes (
  kind smallint NOT NULL,
  tenant_id text NOT NULL,
  id text NOT NULL,
  ver bigint NOT NULL,
  change_xid xid8 NOT NULL,
  PRIMARY KEY (kind, tenant_id, id)
);

CREATE INDEX directory_changes_position ON directory_changes (change_xid, kind, tenant_id, id);

UPDATE accounts SET display_name = username;
ALTER TABLE accounts ALTER COLUMN display_name SET NOT NULL;

INSERT INTO directory_changes (kind, tenant_id, id, ver, change_xid)
SELECT 1, tenant_id, id, 1, pg_current_xact_id() FROM accounts;

CREATE FUNCTION directory_change_upsert(change_kind smallint, change_tenant text, change_id text) RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO directory_changes (kind, tenant_id, id, ver, change_xid)
  VALUES (change_kind, change_tenant, change_id, 1, pg_current_xact_id())
  ON CONFLICT (kind, tenant_id, id) DO UPDATE
  SET ver = directory_changes.ver + 1, change_xid = pg_current_xact_id();
END
$$;

CREATE FUNCTION accounts_directory_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  PERFORM directory_change_upsert(1::smallint, NEW.tenant_id, NEW.id);
  RETURN NULL;
END
$$;

CREATE TRIGGER accounts_directory_insert AFTER INSERT ON accounts
FOR EACH ROW EXECUTE FUNCTION accounts_directory_change();

CREATE TRIGGER accounts_directory_update AFTER UPDATE ON accounts
FOR EACH ROW
WHEN (OLD.display_name IS DISTINCT FROM NEW.display_name
  OR OLD.avatar_url IS DISTINCT FROM NEW.avatar_url
  OR OLD.state IS DISTINCT FROM NEW.state)
EXECUTE FUNCTION accounts_directory_change();

CREATE FUNCTION account_groups_directory_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF TG_OP = 'DELETE' THEN
    PERFORM directory_change_upsert(1::smallint, OLD.tenant_id, OLD.account_id);
  ELSE
    PERFORM directory_change_upsert(1::smallint, NEW.tenant_id, NEW.account_id);
  END IF;
  RETURN NULL;
END
$$;

CREATE TRIGGER account_groups_directory AFTER INSERT OR DELETE ON account_groups
FOR EACH ROW EXECUTE FUNCTION account_groups_directory_change();

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

CREATE FUNCTION iam_changes_notify() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_notify('iam_changes', TG_ARGV[0]);
  RETURN NULL;
END
$$;

CREATE TRIGGER directory_changes_notify AFTER INSERT OR UPDATE ON directory_changes
FOR EACH STATEMENT EXECUTE FUNCTION iam_changes_notify('dir');

CREATE FUNCTION revoked_sessions_inserted_notify() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF EXISTS (SELECT 1 FROM inserted_revocations) THEN
    PERFORM pg_notify('iam_changes', 'rev');
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
EXECUTE FUNCTION iam_changes_notify('rev');
