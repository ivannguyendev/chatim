# App `iam`

IAM tổng quát kiểu Keycloak (M6, owner 2026-10-25), tự xây bằng Go trên Postgres; chatim chỉ là một client. Phần 1: account do admin cấp, đăng nhập mật khẩu (Argon2id), access token Ed25519 15 phút, refresh token xoay vòng gắn khoá máy bằng DPoP (RFC 9449), quản lý và thu hồi phiên, audit log. `iam` không đọc bảng của core, core không đọc bảng của `iam`. Phần 2: thu hồi tới websocket/gateway và khoá ký qua HTTPS, không qua NATS (owner 2026-10-27). Thiết kế: [tóm tắt M6 phần 1](../plans/2026-10-25-m6-1-dang-nhap-mat-khau-va-phien-summary.md), [phần 2](../plans/2026-10-27-m6-2-thu-hoi-toi-node-summary.md).

## Biến môi trường

Đọc qua `apps/iam/internal/config`; sai thì node không khởi động và báo gộp mọi lỗi. Biến `*_FILE` đọc secret từ file (compose secret).

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `IAM_ADDR` | `:8080` | HTTP/1.1 và h2c công khai (chỉ cổng này đứng sau LB); đặt rỗng là tắt |
| `IAM_INTERNAL_ADDR` | `:8081` | Cổng nội bộ cho node websocket/gateway (danh sách thu hồi), **không** đứng sau LB; đặt rỗng là tắt. Phải bật ít nhất một trong hai cổng |
| `IAM_NODE_SECRET` / `_FILE` | — (bắt buộc khi bật cổng nội bộ) | Secret node gửi trong `Authorization: Bearer`, tối thiểu 32 byte (`openssl rand -hex 32`); so bằng hằng thời gian |
| `IAM_ADMIN_ADDR` | `:9090` | `/livez`, `/readyz`, `/metrics`, pprof; **không bao giờ public**; `/app probe` hỏi địa chỉ này |
| `IAM_PUBLIC_URL` | — (bắt buộc khi bật cổng public) | URL công khai, gồm cả tiền tố đường mà LB thêm/bớt; `htu` của DPoP so với URL dựng từ biến này, không bao giờ từ `Host`/`X-Forwarded-*`. Compose dev: `http://chatim-iam-1:8080` |
| `IAM_PG_URI` | — (bắt buộc) | URI Postgres **không** kèm mật khẩu |
| `IAM_PG_PASSWORD` / `_FILE` | — | Mật khẩu role `iam` |
| `IAM_PG_MAX_CONNS` | `20` | Kết nối tối đa của pool |
| `IAM_ISSUER`, `IAM_AUDIENCE` | — (bắt buộc) | `iss`, `aud` của access token; phải khớp `WS_JWT_*`/`GW_JWT_*` |
| `IAM_ACCESS_TTL` | `15m` | Hạn access token |
| `IAM_SESSION_IDLE`, `IAM_SESSION_MAX` | `720h` / `2160h` | Phiên hết hạn sau 30 ngày không dùng, tối đa 90 ngày |
| `IAM_REFRESH_GRACE` | `30s` | Gửi lại refresh token vừa thay trong cửa sổ này vẫn được xoay |
| `IAM_DPOP_WINDOW` | `60s` | `iat` của DPoP proof được lệch tối đa |
| `IAM_HASH_CONCURRENCY` | `16` | Phép băm Argon2id chạy cùng lúc mỗi node (~19 MiB mỗi phép); quá thì `unavailable` |
| `IAM_LOGIN_FREE_FAILURES`, `IAM_LOGIN_MAX_DELAY` | `5` / `1m` | Sai 5 lần thì bắt chờ 1s, 2s, 4s… tối đa 1 phút |
| `IAM_LOGIN_IP_RATE`, `IAM_LOGIN_IP_BURST`, `IAM_LOGIN_IP_TRACKED` | `20` / `200` / `100000` | Lần kiểm mật khẩu mỗi IP mỗi node (token bucket, LRU); đủ cho văn phòng ~200 người chung một IP (NAT) cùng đăng nhập (owner 2026-10-27) |
| `IAM_PASSWORD_MIN` | `12` | Độ dài tối thiểu (tối đa 128) |
| `IAM_KEY_ENCRYPTION_KEY` / `_FILE` | — (bắt buộc) | 32 byte base64, mã hoá khoá ký riêng (AES-256-GCM); sinh bằng `openssl rand -base64 32` |
| `IAM_KEY_RELOAD` | `30s` | Nhịp nạp lại khoá ký, tự kích hoạt khoá chờ đã đủ giờ, kiểm readiness và xoá dòng thu hồi đã hết hạn; compose dev `5s` |
| `IAM_KEY_ACTIVATE_LEAD` | `2m` | Khoá chờ được `iam` tự kích hoạt khi đủ tuổi này; `key-activate` từ chối khoá chưa đủ tuổi. Phải ≥ `IAM_KEY_RELOAD` + 1m (khoảng hỏi khoá tối đa của node), sai thì `iam` không khởi động; compose dev `65s` |
| `IAM_BODY_MAX` | `16384` | Cỡ body tối đa |
| `IAM_CORS_ORIGINS` | rỗng | Origin trình duyệt được phép, đúng từng origin; header `DPoP` được phép |
| `IAM_CLIENT_IP_HEADER` | rỗng | Header IP do edge ghi đè (`X-Real-IP`); `X-Forwarded-For` lấy phần tử cuối; rỗng = `RemoteAddr` |
| `IAM_DRAIN_DELAY`, `IAM_SHUTDOWN_BUDGET` | `5s` / `25s` | Dừng: `/readyz` false, drain, `http.Shutdown`, đóng pool; budget > drain + write timeout + 1s |
| `IAM_READ_HEADER_TIMEOUT`, `IAM_READ_TIMEOUT`, `IAM_WRITE_TIMEOUT`, `IAM_IDLE_TIMEOUT` | `5s` / `10s` / `15s` / `60s` | Hạn của HTTP server |
| `IAM_MAX_HEADER_BYTES` | `16384` | Cỡ header tối đa |

`/readyz` true khi ping được Postgres, schema đúng phiên bản migration mới nhất trong binary, và có đúng một khoá đang ký.

## API công khai

| Đường | Ghi chú |
|---|---|
| `POST /chatim.iam.v1.AuthService/Login` | Connect; bắt buộc đúng một header `DPoP` (proof ký cho đường này); trả access token (Bearer), refresh token, `session_id`; `Cache-Control: no-store`. Sai mật khẩu, account lạ hoặc khoá, tenant khoá, account đang bị chờ → cùng `unauthenticated` "invalid credentials"; mật khẩu tạm → `failed_precondition` |
| `POST /chatim.iam.v1.AuthService/ChangePassword` | Không cần token; đổi xong thu hồi mọi phiên |
| `POST /chatim.iam.v1.AuthService/{ListSessions,RevokeSession}` | Bearer access token của `iam`; phiên của token phải còn chạy |
| `POST /realms/{tenant}/protocol/openid-connect/token` | RFC 6749 §6, form, `grant_type=refresh_token`, header `DPoP`; lỗi JSON §5.2 luôn HTTP 400 (`invalid_request`, `unsupported_grant_type`, `invalid_grant`, `invalid_dpop_proof`); lỗi tạm thời → 503 `server_error` + `Retry-After` (lệch có chủ đích khỏi §5.2); body quá lớn → 413 `invalid_request` |
| `POST /realms/{tenant}/protocol/openid-connect/revoke` | RFC 7009; token lạ → 200; access token → 400 `unsupported_token_type` |
| `GET /.well-known/jwks.json` | Khoá công khai (chờ, đang ký, nghỉ chưa quá 15m30s), `Cache-Control: public, max-age=60`; chưa có khoá → 503. Node prod lấy khoá **chỉ** từ đường này qua HTTPS của LB |

**Cổng nội bộ** (`IAM_INTERNAL_ADDR`): chỉ `GET /internal/v1/revocations` (header `Authorization: Bearer <IAM_NODE_SECRET>`; sai/thiếu → 401). Trả mọi mục còn hạn: `sessions[{session_id, tenant_id, expires_at}]` (một dòng mỗi phiên bị thu hồi: người dùng, admin, khoá account, đổi/đặt lại mật khẩu, dùng lại refresh token) và `tenants[{tenant_id, revoked_before, expires_at}]` (khoá tenant, giờ Postgres). Mục giữ `IAM_ACCESS_TTL` + 1 phút. `ETag` theo băm body, `If-None-Match` khớp → 304. Đường khác → 404, kể cả JWKS. Access token lấy `iat` từ giờ của transaction cấp (Postgres) nên token cấp trước lúc khoá tenant luôn có `iat` ≤ giờ khoá.

**Node đồng bộ** (`pkg/iamsync`, websocket và gateway như nhau, tiền tố `WS_`/`GW_`): `*_IAM_INTERNAL_URL`, `*_IAM_SECRET`/`_FILE` và `*_IAM_JWKS_URL` **bắt buộc** (JWKS phải `https://` trừ khi `*_IAM_ALLOW_HTTP=true`, chỉ cho dev), `*_IAM_REVOCATIONS_INTERVAL` (`2s`), `*_IAM_KEYS_INTERVAL` (`1m`, tối đa `1m`, dev `10s`), `*_IAM_READY_TIMEOUT` (`10s`). Node hỏi danh sách mỗi 2s, gộp vào RAM (chỉ thêm), ghi bản đó lên Redis chung `chatim:iam:revocations:<issuer>` (hạn 60s; issuer = `*_JWT_ISSUER`, nên staging và prod chung Redis không trộn nhau); hỏi `iam` lỗi thì đọc bản Redis do node khác ghi. Khoá ký không bao giờ đi qua Redis. `kid` lạ → hỏi JWKS ngay (tối đa 1 lần/5s), trong lúc chờ websocket trả `retry_later`, gateway 503. Node chỉ `ready` sau lần đồng bộ đầu hoặc sau `*_IAM_READY_TIMEOUT` (khi `iam` không với tới). `iam` trả 4xx (trừ 429), ví dụ sai secret (401) hay sai đường (404) → node **không ready**, log lỗi `iam refused this node`, metric `iam_refused{kind}` = mã lỗi, kể cả khi đọc được bản Redis; `iam` nhận lại thì node `ready`. `*_JWT_LEEWAY` tối đa 1 phút. `IAM_ACCESS_TTL` + 1m phải ≤ 2h (node bỏ mục thu hồi hạn xa hơn), sai thì `iam` không khởi động.

**Đặc tả client (DPoP):** mỗi máy tạo một cặp khoá ES256 hoặc EdDSA không xuất được (WebCrypto `extractable: false`, Secure Enclave/Keystore), ký **một proof mới cho mỗi request**, kể cả khi gửi lại; `htu` = `IAM_PUBLIC_URL` + đường, `htm` = `POST`. Proof gửi lại nguyên văn → `invalid_dpop_proof`. Refresh token chỉ dùng được với khoá đã đăng nhập.

## Lệnh admin

Chạy trong container (`make iam-admin ARGS="…"` = `docker exec chatim-iam-1 /app …`); kết quả ra stdout, log ra stderr; sai cú pháp thoát 2, lỗi thoát 1; mọi thay đổi ghi audit `admin-cli`.

| Lệnh | Việc |
|---|---|
| `migrate [-apply]` | Mặc định chỉ liệt kê migration chờ; `-apply` chạy dưới khoá advisory |
| `tenant-create -id -name`, `tenant-disable -id`, `tenant-enable -id` | Khoá tenant thu hồi mọi phiên của tenant |
| `account-create -tenant -username [-email]` | In mật khẩu tạm **một lần**; phải đổi ở lần đăng nhập đầu |
| `password-reset -tenant -username` | Mật khẩu tạm mới, thu hồi mọi phiên |
| `account-disable`, `account-enable` (`-tenant -username`) | Khoá thu hồi mọi phiên |
| `session-list -tenant -username`, `session-revoke -session` | Xem và thu hồi phiên |
| `key-rotate [-activate-if-none]`, `key-activate -kid` | Tạo khoá chờ, in giờ `iam` sẽ tự kích hoạt; `key-activate` kích hoạt tay (khoá cũ và khoá chờ cũ hơn sang nghỉ, luôn đúng một khoá đang ký), từ chối khoá chưa đủ `IAM_KEY_ACTIVATE_LEAD` và khoá đã nghỉ |
| `jwks-export -out file [-merge file]` | Ghi JWKS công khai (khoá chờ, đang ký, nghỉ chưa quá 15m30s), ghi file tạm rồi đổi tên |
| `bootstrap -jwks file` | Dev: migrate, tạo khoá đầu nếu chưa có, gộp vào file JWKS |

**Xoay khoá:** chỉ một lệnh `key-rotate`. `iam` công bố khoá chờ qua JWKS ở lần nạp lại kế tiếp (≤ `IAM_KEY_RELOAD`), node lấy về trong ≤ 1m, và `iam` tự kích hoạt khoá khi đủ `IAM_KEY_ACTIVATE_LEAD` (nhiều replica cùng thử, Postgres khoá dòng nên chỉ một lần; metric `signing_keys_activated_total`, log `signing key activated`). Không ai phải canh giờ, không khởi động lại node. Lỡ có node chưa có khoá, client chỉ nhận `retry_later`/503 vài giây trong lúc node hỏi JWKS ngay. File JWKS của node chỉ là dự phòng lúc khởi động; khoá đã nằm trong file chỉ gỡ được bằng cách sửa file và khởi động lại node.

**Lưu ý khi setup** (mọi số khác để mặc định; đặt sai thì app không khởi động hoặc node không `ready` kèm log rõ):

1. Tạo `IAM_KEY_ENCRYPTION_KEY` và `IAM_NODE_SECRET`; secret giống nhau ở `iam`, websocket (`WS_IAM_SECRET`) và gateway (`GW_IAM_SECRET`).
2. `IAM_ISSUER`/`IAM_AUDIENCE` trùng `*_JWT_ISSUER`/`*_JWT_AUDIENCE` của node.
3. Node trỏ `*_IAM_INTERNAL_URL` vào cổng nội bộ (`:8081`) và `*_IAM_JWKS_URL` vào địa chỉ public `https://…/.well-known/jwks.json`.
4. Chạy `bootstrap -jwks` một lần, gắn file đó làm `*_JWT_KEYS_FILE` của node.
5. Websocket và gateway dùng chung một Redis presence; mỗi môi trường một issuer riêng.

## Cổng, compose, metric

- Compose dev: `iam-1` bật cổng nội bộ `:8081` (không publish), websocket/gateway hỏi `http://chatim-iam-1:8081` và JWKS ở `http://chatim-iam-1:8080/.well-known/jwks.json` (`*_IAM_ALLOW_HTTP=true`), Redis chung là `chatim-redis-presence`. `postgres` (profile `app` và `postgres`, `shared_buffers` mặc định 512MB), `postgres-init` (tạo role `iam` và database `chatim_iam`, mật khẩu đọc trong psql, không qua argv), `iam-bootstrap` (chạy bằng uid của host, ghi `.local/ws-keys/jwks.json`), `iam-1` ở `127.0.0.1:8084` (`IAM1_PORT`). Websocket và gateway chờ `iam-bootstrap` xong mới khởi động.
- `.env`: `IAM_PG_PASSWORD`, `IAM_KEY_ENCRYPTION_KEY`, `IAM_NODE_SECRET` (`make iam-env` kiểm); không in giá trị.
- Metric `chatim_iam_*`: `login_total{result}`, `refresh_total{result}`, `sessions_revoked_total`, `password_changes_total`, `store_errors_total`, `hash_inflight`, `signing_key_ready`. `revocation_requests_total{result}`, `revocations_cleaned_total`, `revocation_cleanup_errors_total`, `signing_keys_activated_total`. Alert nhóm `chatim-iam-health`: `ChatimIamHashSaturated`, `ChatimIamRefreshReuse`, `ChatimIamNoSigningKey`, `ChatimIamStoreErrors`. Phía node (`chatim_websocket_*`/`chatim_gateway_*`): `iam_refused{kind}`, `iam_revocations_age_seconds`, `iam_keys_age_seconds`, `iam_sync_errors_total{kind}`, `iam_shared_reads_total{result}`, `iam_shared_write_errors_total`, `iam_boot_timeouts_total`, `jwks_conflicts_total`, `revocations{kind}`, `revocations_dropped_total`; nhóm `chatim-iam-sync`: `ChatimIamSyncStale` (> 30s), `ChatimIamKeysStale` (> 3 phút), `ChatimIamRefused` (node bị `iam` từ chối > 1 phút), `ChatimJwksConflict`.
- Log: `make iam-logs`. Image: `make image TARGET=apps/iam` (`chatim/iam:dev`). itest: `CHATIM_IT_PG_URI` (database tạm `iam_it_*` mỗi lần chạy).
