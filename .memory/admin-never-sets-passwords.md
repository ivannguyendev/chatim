---
name: admin-never-sets-passwords
description: "Owner 2026-11-01 (M6 3c.2) - no admin ever sets or sees another user's password; admins only disable the current password or have iam send a server-generated one-time reset link through the tenant's notify webhook; ops CLI temp passwords stay as break-glass"
metadata:
  type: feedback
---

In the 3c.2 summary I had `ResetPassword(ha, temp password)` with the caller choosing the temporary password, and `CreateAccounts` taking a temp password per row from the backend. The owner rejected it: "ResetPassword: server tự tạo mk tạm, gửi qua các channel notify được tích hợp. tất cả admin ko có quyền đặt pass giùm, chỉ có thể vô hiệu pass cũ hoặc gửi link reset pass." Then "ok" to: iam calls the tenant's integrated notify system by webhook, sends a one-time link (never a temp password), and the ops CLI keeps printing temp passwords as the operators' break-glass path.

This reverses part of D212 ("quên mật khẩu do admin đặt lại, chưa gửi email").

**Why:** an admin who can set or read a password can log in as that person; Keycloak and Zitadel send an action link instead.

**How to apply:**
- No API path takes a password from an admin or returns one. Admin actions: disable the current password (revoke sessions) or request a reset/activation link.
- iam generates the one-time token, stores only its hash, and hands it to the tenant's notify webhook; the user sets the password in the product app, which calls iam's public API with the token.
- Batch-created accounts start without a password and get an activation link, so batches never hash.
- Ops CLI (`account-create`, `password-reset`) still prints a temporary password: operators, not tenant admins, and it is audited.

Related: [[owner-communication]], [[ops-setup-hard-to-get-wrong]].
