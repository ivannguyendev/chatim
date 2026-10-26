---
name: m6-iam-scope
description: Owner 2026-10-25 - M6 builds apps/iam, a general Keycloak-like IAM built in-house on Postgres; token checks embedded in websocket/gateway nodes; OIDC + password login; IdP token exchange; admin password reset
metadata:
  type: project
---

What the owner answered in the M6 brainstorm on 2026-10-25:
- "Đừng quan tâm ai đang dùng module này, hãy làm identity tổng quát như keycloak, có thể linh hoạt sử dụng". Then "phát triển từ app identity này… đổi tên app này thành IAM luôn cũng đc". So M6 builds `apps/iam` in-house, and chatim is only one client of it.
- Concepts the owner listed: account, tenant, permission, permission_groups, oidc, idp, session, token, exchange.
- Login at go-live: OIDC with an external IdP, plus passwords held by chatim (Argon2id). SAML comes later.
- Exchange means an external IdP's id_token is traded for an `iam` token (RFC 8693).
- Forgotten password: an admin resets it. No emails at go-live.
- Token checks are embedded in each websocket/gateway node, and there is no `identity-verifier` service (this replaces part of D179).
- Database: "postgres nha". Core stays on Mongo.
- Order (after seeing a 6-phase split and finding it "cũng to đấy"): "cho phần login password trước, chưa cho đăng ký, admin cấp trước, có token, refresh token, quản lý phiên trước, thu hồi phiên. sắp xếp phát triển phần khác sau". Password login with admin-provisioned accounts, tokens, refresh, sessions and session revocation come first. Permissions, directory, the OIDC provider, external IdPs and exchange come later.

**Why:** these are the owner's own scope choices for go-live. Do not re-ask them.

**How to apply:**
- Treat `iam` as generic: permissions are strings that apps define, and `iam` never learns chat concepts.
- Core's shard-ready rules (no transactions, uniqueness only through `_id`) do not apply to `iam`. Record that as a new decision when the decision log is folded.
- Delete this note once these decisions are in `docs/decision-log.md`.

Related: [[phase2-go-live-scope]], [[no-shared-tables-across-services]].
