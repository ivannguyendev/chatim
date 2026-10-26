CREATE TABLE tenants (
  id text PRIMARY KEY,
  name text NOT NULL,
  state smallint NOT NULL,
  ver bigint NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  updated_by text NOT NULL
);

CREATE TABLE accounts (
  tenant_id text NOT NULL REFERENCES tenants (id),
  id text NOT NULL,
  username text NOT NULL,
  email text,
  state smallint NOT NULL,
  password_hash text NOT NULL,
  password_must_change boolean NOT NULL,
  password_changed_at timestamptz NOT NULL,
  failed_login_count integer NOT NULL DEFAULT 0,
  next_login_at timestamptz,
  ver bigint NOT NULL,
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  updated_by text NOT NULL,
  PRIMARY KEY (tenant_id, id),
  UNIQUE (tenant_id, username)
);

CREATE UNIQUE INDEX accounts_tenant_email ON accounts (tenant_id, email) WHERE email IS NOT NULL;

CREATE TABLE sessions (
  id text PRIMARY KEY,
  tenant_id text NOT NULL,
  account_id text NOT NULL,
  state smallint NOT NULL,
  device_name text NOT NULL,
  user_agent text NOT NULL,
  created_at timestamptz NOT NULL,
  last_used_at timestamptz NOT NULL,
  idle_expires_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL,
  refresh_ver bigint NOT NULL,
  refresh_hash bytea NOT NULL,
  refresh_history jsonb NOT NULL,
  dpop_jkt text NOT NULL,
  dpop_recent_jti text[] NOT NULL,
  revoked_at timestamptz,
  revoked_by text,
  revoked_reason text,
  FOREIGN KEY (tenant_id, account_id) REFERENCES accounts (tenant_id, id)
);

CREATE INDEX sessions_account ON sessions (tenant_id, account_id, state);

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
  account_id text,
  session_id text,
  ip text,
  detail jsonb NOT NULL
);

CREATE INDEX audit_events_tenant_at ON audit_events (tenant_id, at);
