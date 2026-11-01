CREATE TABLE clients (
  id text PRIMARY KEY,
  name text NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  updated_by text NOT NULL
);

CREATE TABLE client_permissions (
  permission text PRIMARY KEY,
  client_id text NOT NULL REFERENCES clients (id),
  description text NOT NULL,
  CONSTRAINT client_permissions_client_prefix CHECK (starts_with(permission, client_id || ':'))
);

INSERT INTO clients (id, name, created_at, updated_at, updated_by)
VALUES ('iam', 'IAM', now(), now(), 'iam');

INSERT INTO client_permissions (permission, client_id, description) VALUES
  ('iam:roles.read', 'iam', 'Read roles and their permissions'),
  ('iam:roles.write', 'iam', 'Create, update and delete roles'),
  ('iam:roles.assign', 'iam', 'Assign roles to accounts and groups'),
  ('iam:groups.read', 'iam', 'Read groups'),
  ('iam:groups.write', 'iam', 'Create, update and delete groups'),
  ('iam:groups.members.write', 'iam', 'Set the groups of an account'),
  ('iam:accounts.read', 'iam', 'Read accounts'),
  ('iam:accounts.create', 'iam', 'Create accounts'),
  ('iam:accounts.update', 'iam', 'Update accounts'),
  ('iam:accounts.reset_password', 'iam', 'Reset account passwords'),
  ('iam:accounts.sessions.revoke', 'iam', 'Revoke account sessions'),
  ('iam:machines.write', 'iam', 'Manage machine accounts');

CREATE TABLE roles (
  tenant_id text NOT NULL REFERENCES tenants (id),
  id text NOT NULL,
  name text NOT NULL,
  builtin boolean NOT NULL,
  ver bigint NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  updated_by text NOT NULL,
  PRIMARY KEY (tenant_id, id)
);

CREATE TABLE role_permissions (
  tenant_id text NOT NULL,
  role_id text NOT NULL,
  permission text NOT NULL,
  PRIMARY KEY (tenant_id, role_id, permission),
  FOREIGN KEY (tenant_id, role_id) REFERENCES roles (tenant_id, id),
  CONSTRAINT role_permissions_permission_fkey FOREIGN KEY (permission) REFERENCES client_permissions (permission)
);

CREATE INDEX role_permissions_permission ON role_permissions (permission);

CREATE TABLE account_roles (
  tenant_id text NOT NULL,
  account_id text NOT NULL,
  role_id text NOT NULL,
  created_at timestamptz NOT NULL,
  created_by text NOT NULL,
  PRIMARY KEY (tenant_id, account_id, role_id),
  FOREIGN KEY (tenant_id, account_id) REFERENCES accounts (tenant_id, id),
  FOREIGN KEY (tenant_id, role_id) REFERENCES roles (tenant_id, id)
);

CREATE INDEX account_roles_role ON account_roles (tenant_id, role_id);

CREATE TABLE group_roles (
  tenant_id text NOT NULL,
  group_id text NOT NULL,
  role_id text NOT NULL,
  created_at timestamptz NOT NULL,
  created_by text NOT NULL,
  PRIMARY KEY (tenant_id, group_id, role_id),
  FOREIGN KEY (tenant_id, group_id) REFERENCES groups (tenant_id, id),
  FOREIGN KEY (tenant_id, role_id) REFERENCES roles (tenant_id, id)
);

CREATE INDEX group_roles_role ON group_roles (tenant_id, role_id);

CREATE FUNCTION tenants_add_tenant_admin() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO roles (tenant_id, id, name, builtin, ver, created_at, updated_at, updated_by)
  VALUES (NEW.id, 'tenant-admin', 'Tenant admin', true, 1, NEW.created_at, NEW.created_at, 'iam');
  RETURN NULL;
END
$$;

CREATE TRIGGER tenants_tenant_admin AFTER INSERT ON tenants
FOR EACH ROW EXECUTE FUNCTION tenants_add_tenant_admin();

INSERT INTO roles (tenant_id, id, name, builtin, ver, created_at, updated_at, updated_by)
SELECT id, 'tenant-admin', 'Tenant admin', true, 1, created_at, created_at, 'iam' FROM tenants
ON CONFLICT DO NOTHING;

ALTER TABLE groups ADD COLUMN ancestors text[] NOT NULL DEFAULT '{}';

WITH RECURSIVE paths (tenant_id, id, ancestors) AS (
  SELECT tenant_id, id, '{}'::text[] FROM groups WHERE parent_id IS NULL
  UNION ALL
  SELECT g.tenant_id, g.id, p.ancestors || p.id
  FROM groups g JOIN paths p ON g.tenant_id = p.tenant_id AND g.parent_id = p.id
)
UPDATE groups g SET ancestors = p.ancestors
FROM paths p
WHERE g.tenant_id = p.tenant_id AND g.id = p.id AND g.ancestors IS DISTINCT FROM p.ancestors;

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

CREATE INDEX groups_ancestors ON groups USING gin (ancestors);
