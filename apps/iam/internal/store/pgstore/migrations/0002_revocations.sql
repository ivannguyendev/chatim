CREATE TABLE revoked_sessions (
  session_id text PRIMARY KEY,
  tenant_id text NOT NULL,
  revoked_at timestamptz NOT NULL,
  expires_at timestamptz NOT NULL
);

CREATE INDEX revoked_sessions_expires ON revoked_sessions (expires_at);

ALTER TABLE tenants ADD COLUMN sessions_revoked_at timestamptz;
