---
name: tenant-apis-wait-for-a-tenant
description: "Owner 2026-11-04 - M6 closed at PA + PM; PB (batch server API for users/groups/members, revoke-before, audit from the change stream), external IAM and group sync to Mention wait until the first tenant integrates, still before go-live (D260)"
metadata:
  type: project
---

Owner 2026-11-04, asked what comes after phase PM: "chắc chưa có tenant tích hợp" (there is probably no tenant integrating yet), then "ok, dọn dẹp sạch sẽ để kết thúc, chuẩn bị M7" to closing M6 at PA + PM and moving on to M7 media (D260).

**Why:** the caller of PB is a tenant's backend and none exists yet. Batch size, whether a tenant pushes its whole directory or each change, and per-row error reporting depend on that caller; guessing risks a rewrite (no limit or API without a use case, A6). The CLI already covers dev and e2e. M7 changes core, so it should land before the last PoC measurement (D207).

**How to apply:** do not start PB, external IAM (JWKS URL, introspection, back-channel logout, SCIM) or the group sync to Mention ([[milestone-keeps-its-own-scope]], D234) until the first tenant (the new product of D207, [[phase2-go-live-scope]]) starts integrating; then run PB between milestones, before go-live, writing on `mongostore` ([[identity-store-on-mongodb]]). The identity security review stays a go-live gate.
