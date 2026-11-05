---
name: chat-mirrors-users-tenant-owns-identity
description: "Owner 2026-11-02 - chat owns no identity (no passwords, login, sessions, SSO); it keeps a user mirror, groups, per-tenant trusted issuers and revocation, and exchanges a tenant- or IdP-signed JWT for its own chat token; reverses D179, D209 and the 2026-11-01 Keycloak choice"
metadata:
  type: project
---

Path of the decision: D179 (2026-10-19, chat owns accounts) → D209 (2026-10-25, self-built Keycloak-like IAM; parts 1, 2, 3a, 3c.1 built on feat/m6) → 2026-11-01 owner: "các phần đang xây … có trên các IAM có sẵn", worried about volume, safety of a self-written IAM, time to go-live, and a closed system ("IAM enterprise thường sẽ là hệ riêng độc lập, khả năng tích hợp quan trọng hơn") → Keycloak mandatory, realm per tenant, no plugins → 2026-11-02 owner asked to shrink to "user + groups + cấp jwt", then what chat should keep with no IAM at all, and said "ok" to this model.

**Why:** CPaaS chat platforms (Stream, Sendbird, Twilio) let the customer own identity; the tenant's backend or IdP signs, chat trusts per tenant. Integration with any IAM becomes configuration, not code.

**How to apply:**
- Chat keeps: per-tenant trust (issuer, public keys or JWKS URL, user-id claim, aud), a user mirror (tenant's id, name, avatar, state, ver), groups, revocation, and its own Ed25519 chat token.
- Token exchange: a valid external JWT becomes a 5-minute chat token (owner 2026-11-02 "token của chat chỉ 5 phút") with tid and sid; websocket/gateway and pkg/auth stay unchanged. Built in M6 PA (D239–D243); the store moves to MongoDB in PM ([[identity-store-on-mongodb]]).
- The tenant always comes from the issuer config, never from a token claim.
- User/group writes: JIT from claims, then a batch server API (authenticated by a JWT the tenant backend signs), SCIM only when an IdP tenant needs it.
- Never write plugins for an external IAM (owner 2026-11-01). Room roles stay in core `members`.
- The earlier password rules (admins never set passwords, notify webhook) no longer apply: chat stores no passwords.
- Assumed (owner ok to the proposal built on it): the go-live product has its own backend login, so go-live needs no IAM.
