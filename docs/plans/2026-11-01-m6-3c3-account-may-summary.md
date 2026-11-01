# M6 phần 3c.3: account máy — tóm tắt

> Ngày: 2026-11-01 · Nhánh `feat/m6` · Trạng thái: **owner duyệt 2026-11-01** · Quyết định: D238 (owner chọn cách A 2026-10-31), dựa trên D235–D236 · Mô hình: [nghiên cứu](../research/261031-iam-authorization-models.md) §4
> **Các phần của 3c:** (3c.1) role và kiểm quyền ✅ · (3c.2) quản trị account, tạo theo lô · **(3c.3) account máy**. Code sau 3c.2 (chung proto `AdminService`, `api`, bảng `accounts`).

## 1. Phần này làm gì

- **Lan tạo account máy** `svc-acme` cho backend acme và gán role `provisioner` (`iam:accounts.create`, `iam:groups.members.write`).
- **Đội acme sinh cặp khoá** ở máy của họ và gửi khoá công khai (JWK). Lan gọi `AddMachineKey(svc-acme, jwk, hạn 90 ngày)`. Khoá riêng không bao giờ đi qua `iam`.
- **Backend acme lấy token:** ký một JWT ngắn (assertion) bằng khoá riêng, gửi tới `/realms/acme/protocol/openid-connect/token` với `grant_type=urn:ietf:params:oauth:grant-type:jwt-bearer`. iam trả access token 15 phút, không có refresh token. Hết hạn thì ký assertion mới.
- **Backend gọi `CreateAccounts`** bằng token đó, như một admin người.
- **Token máy không dùng được để chat:** gửi lên websocket hay gateway bị từ chối (`aud` khác).
- **Lộ khoá:** Lan gọi `RevokeMachineKey` → token đang sống của khoá đó bị cắt ngay (danh sách thu hồi trong RAM), assertion mới bị từ chối.

## 2. Yêu cầu

| # | Yêu cầu | Nguồn |
|---|---|---|
| R8 | Không sửa app khác | D234 |
| R11 | Cấu hình sai thì không chạy | D225 |
| R12 | Account máy là loại riêng: không mật khẩu, đăng nhập bằng khoá | owner 2026-10-31 (cách A); Keycloak, Zitadel |
| R15 | Tác động lên account X chỉ khi quyền(X) ⊆ quyền người gọi | D236 |
| R17 | Máy không làm hỏng danh bạ 3a: không vào `GetUsers`, feed, nhóm | D238 (review C2) |

## 3. Trong và ngoài

| Trong 3c.3 | Ai làm |
|---|---|
| `accounts.kind`, `machine_keys`, `machine_assertions`, `sessions.machine_key_id` (migration 0006) | `iam` |
| Grant JWT bearer (RFC 7523) ở endpoint token của tenant | `iam` cổng public |
| `aud` riêng cho token máy; `AdminService` nhận cả hai `aud` | `iam` |
| RPC `CreateMachine`, `SetMachineState`, `AddMachineKey`, `RevokeMachineKey`, `ListMachineKeys` | `iam` `AdminService` |
| CLI `machine-create`, `machine-key-add`, `machine-key-revoke`, `machine-keygen` (chỉ dev) | `iam` admincmd |
| e2e phase 18 | `tools/corecli`, `scripts/` |

| Ngoài 3c.3 | Ở đâu |
|---|---|
| Máy gọi API chat (bot) | Chưa có use case; token máy bị websocket/gateway từ chối |
| Secret, PAT | Không làm (D238) |
| OIDC client cho app ngoài | Phần 4 |

## 4. Dữ liệu (migration 0006)

| Bảng / cột | Field chính | Ví dụ |
|---|---|---|
| `accounts.kind` | `1` người, `2` máy; máy luôn có `password_hash` NULL (`CHECK`; cột được NULL từ 3c.2) | `svc-acme`, 2 |
| `machine_keys` | `tenant_id`, `account_id`, `key_id`, `public_key` (JWK), `algorithm`, `state`, `expires_at`, `created_at`, `created_by`, `revoked_at` | `acme`, `svc-acme`, `k1`, EdDSA |
| `machine_assertions` | `tenant_id`, `key_id`, `jti`, `expires_at` | chống dùng lại assertion |
| `sessions.machine_key_id` | khoá đã sinh phiên | `k1` |

- **Máy không vào danh bạ:** hai trigger danh bạ 3a bỏ qua `kind = 2`; câu nạp danh bạ lọc `kind = 1` **bên trong** câu con có `LIMIT`. Lọc bên ngoài thì một máy nằm giữa trang 5.000 dòng làm bộ nạp tưởng hết dữ liệu, dừng sớm và mất mọi account phía sau.
- **Máy không có nhóm, không có mật khẩu:** `SetAccountGroups`, `SendPasswordReset`, `DisablePassword`, `CompletePasswordReset`, `SetPassword` từ chối máy (`failed_precondition`; `CompletePasswordReset` trả lỗi chung như mã sai); đăng nhập mật khẩu coi máy là account không hợp lệ nhưng vẫn băm giả, để không lộ account có tồn tại (D215).
- Quyền của máy chỉ đến từ role gán thẳng (`account_roles`).
- Lớp dữ liệu: account máy là "cấu hình phân quyền" (3c.1); khoá và assertion là dữ liệu phiên (phần 1).

## 5. Lấy token

Assertion phải thoả, sai một điều thì `invalid_grant`:

| Kiểm | Giá trị |
|---|---|
| `alg` | EdDSA hoặc ES256, khớp `algorithm` của khoá |
| `kid` | khoá còn hiệu lực của tenant trong đường dẫn |
| `iss` = `sub` | id account máy, máy đang `active` |
| `aud` | `IAM_PUBLIC_URL` + `/realms/{tenant}/protocol/openid-connect/token`, không dựng từ `Host`; assertion của tenant này không dùng được ở tenant khác |
| `exp`, `iat` | có `exp`, `exp − iat` ≤ 5 phút, `iat` lệch tối đa ±60s |
| `jti` | chưa dùng; ghi vào `machine_assertions`, trùng → `invalid_grant` |

- Endpoint chọn đường theo `grant_type` **trước** khi đòi DPoP: refresh của người vẫn cần DPoP như cũ; grant máy không dùng DPoP.
- **Mỗi khoá một phiên:** lần đầu lấy token thì tạo phiên, các lần sau dùng lại (không tạo ~96 phiên/máy/ngày). Phiên hết hạn theo idle như phiên người, và không vượt hạn khoá.
- Thu hồi khoá → thu hồi phiên của khoá qua đường thu hồi có sẵn. Khoá máy hay khoá tenant vốn đã thu hồi phiên.
- Access token có `aud` = `IAM_MACHINE_AUDIENCE`, `iat` lấy giờ transaction (D221).

## 6. `AdminService` nhận token máy

- Verifier của `AdminService` nhận `aud` người **hoặc** máy; `DirectoryService`, `AuthService` chỉ nhận `aud` người.
- Phần sau giống người: kiểm phiên → quyền (role gán thẳng) → permission → chống leo quyền.
- **Lệnh trên máy** cần `iam:machines.write` và quyền(máy) ⊆ người gọi. Ví dụ: it-admin thêm khoá cho máy `svc-ops` đang mang `tenant-admin` → `permission_denied`. Không có luật này thì it-admin tự cấp khoá rồi thành `tenant-admin`.

## 7. Config mới

| Biến | Mặc định | Luật |
|---|---|---|
| `IAM_MACHINE_AUDIENCE` | không có, bắt buộc | khác rỗng, khác `IAM_AUDIENCE`; sai thì không khởi động |
| `IAM_PUBLIC_URL` | đã có | bắt buộc khi có grant máy |

Metric: `chatim_iam_machine_tokens_total{result}` (`ok`, `invalid_grant`, `replay`), `chatim_iam_machine_assertions_rows` (số dòng chống dùng lại đang giữ).

## 8. Đúng đắn

- **Assertion dùng lại:** `jti` ghi trong cùng transaction với phiên; trùng → từ chối. Dòng hết hạn được vòng dọn 30s đã có của `iam` xoá (`app/run.go:163-207`, cùng chỗ dọn danh sách thu hồi).
- **Khoá lộ:** thu hồi có hiệu lực ngay ở `iam` (assertion mới) và trong vài giây ở mọi node (danh sách thu hồi).
- **Danh bạ không thấy máy:** itest đặt một máy giữa trang nạp 5.000 dòng.
- **Không leo quyền qua máy:** như §6.

## 9. Quyết định và phương án bị loại

| Chọn | Bỏ | Vì sao |
|---|---|---|
| Khoá công khai tải lên (RFC 7523) | Secret dùng chung; PAT sống lâu | Lộ secret hay PAT là dùng được tới khi bị xoá; Zitadel khuyên dùng khoá |
| `aud` riêng cho token máy | Chung `aud` với token chat | Token máy dùng được như một lần đăng nhập chat (review C1) |
| Mỗi khoá một phiên | Một phiên mỗi lần lấy token | ~96 dòng/máy/ngày |
| Sinh cặp khoá chỉ ở CLI dev | `iam` sinh và trả khoá riêng | Khoá riêng không bao giờ đi qua `iam` |

## 10. Rủi ro

- Backend làm mất khoá riêng thì phải thêm khoá mới; nên giữ hai khoá chồng hạn để xoay không gián đoạn (ghi trong `docs/apps/iam.md`).

## 11. Kiểm thử

- **Unit:** từng điều kiện của assertion (`alg`, `kid`, `iss`/`sub`, `aud` của tenant khác, `exp − iat`, lệch giờ, `jti` trùng); config `aud` máy.
- **itest:** máy giữa trang nạp 5.000 dòng không làm mất account phía sau; trigger danh bạ bỏ qua máy; máy không vào nhóm, không đặt được mật khẩu; đăng nhập mật khẩu bằng username máy → như account không có; thu hồi khoá thu hồi phiên; mỗi khoá một phiên.
- **e2e phase 18:** `svc-acme` lấy token và nạp lô account; assertion dùng lại và `aud` sai bị từ chối; token máy bị websocket và gateway từ chối; thu hồi khoá → lệnh kế tiếp `unauthenticated`; `GetUsers` và feed không có máy; phase 13–17 vẫn qua.

## Bảng phủ

| Yêu cầu | Thành phần | Test | Bằng chứng |
|---|---|---|---|
| R8 không sửa app khác | `aud` riêng | e2e 18: websocket/gateway từ chối token máy; `git diff --stat` | (điền sau khi xây) |
| R11 cấu hình sai không chạy | luật `IAM_MACHINE_AUDIENCE` | unit config | |
| R12 máy dùng khoá | migration 0006, grant RFC 7523 | unit assertion; e2e 18 | |
| R15 quyền(máy) ⊆ người gọi | `manage` + `authz` | itest it-admin vs `svc-ops` | |
| R17 danh bạ không thấy máy | trigger + câu nạp | itest trang 5.000 dòng; e2e 18 `GetUsers` | |
