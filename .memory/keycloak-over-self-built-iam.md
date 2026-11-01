---
name: keycloak-over-self-built-iam
description: "Owner 2026-11-01 - reverse D209: Keycloak (one realm per tenant) is the IAM and broker, apps/iam becomes a Go bridge (revocation list, directory feed, tenant-realm map); use only built-in Keycloak features, never write Keycloak plugins"
metadata:
  type: project
---

After 3c.1 was built, the owner reversed D209 (self-built Keycloak-like IAM): "các phần đang xây khá quan trọng và có trên các IAM có sẵn". Reasons given: too much volume, safety of a self-written IAM, time to go-live, and a closed system — enterprise IAM is usually a separate independent system, so integration matters more than a complete self-built IAM.

Owner answers 2026-11-01: one Keycloak realm per tenant (a few dozen tenants); "có gì dùng đó, ko viết plugin" — no Keycloak SPI/Java plugins, only built-in features and configuration.

**Why:** integration with customers' IdPs (OIDC, SAML, LDAP), MFA and login pages are the riskiest unbuilt parts; Keycloak has them.

**How to apply:**
- apps/iam is not on the login path; it reads Keycloak admin REST (events, users, groups) on a short poll and serves chat-specific feeds: revocation list for websocket/gateway, directory `GetUsers` + change feed (3a contract), tenant ↔ realm, later group sync to Mention.
- A gap Keycloak lacks is accepted or covered by polling from Go, never by a plugin.
- Passwords: tenant admins only send Keycloak's "execute actions email" link (SMTP); `manage-users` stays with operators if FGAP V2 cannot split password setting. Replaces [[admin-never-sets-passwords]]'s webhook design.
- Spike S1–S7 (decision log) runs before the phase split.

Related: [[phase2-go-live-scope]], [[milestone-keeps-its-own-scope]].
