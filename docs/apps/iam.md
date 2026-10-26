# App `iam`

IAM tổng quát kiểu Keycloak (M6, owner 2026-10-25), tự xây bằng Go trên Postgres; chatim chỉ là một client. Phần 1: account do admin cấp, đăng nhập mật khẩu (Argon2id), access token Ed25519 15 phút, refresh token xoay vòng gắn khoá máy bằng DPoP (RFC 9449), quản lý và thu hồi phiên, audit log. `iam` không đọc bảng của core, core không đọc bảng của `iam`. Thiết kế: [tóm tắt M6 phần 1](../plans/2026-10-25-m6-1-dang-nhap-mat-khau-va-phien-summary.md).

## Biến môi trường

Đọc qua `apps/iam/internal/config`; sai thì node không khởi động và báo gộp mọi lỗi. Biến `*_FILE` đọc secret từ file (compose secret).

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `IAM_ADDR` | `:8080` | HTTP/1.1 và h2c công khai (chỉ cổng này đứng sau LB) |
| `IAM_ADMIN_ADDR` | `:9090` | `/livez`, `/readyz`, `/metrics`, pprof; **không bao giờ public**; `/app probe` hỏi địa chỉ này |
| `IAM_PUBLIC_URL` | — (bắt buộc) | URL công khai, gồm cả tiền tố đường mà LB thêm/bớt; `htu` của DPoP so với URL dựng từ biến này, không bao giờ từ `Host`/`X-Forwarded-*`. Compose dev: `http://chatim-iam-1:8080` |
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
| `IAM_LOGIN_IP_RATE`, `IAM_LOGIN_IP_BURST`, `IAM_LOGIN_IP_TRACKED` | `1` / `10` / `100000` | Đăng nhập mỗi IP mỗi node (token bucket, LRU) |
| `IAM_PASSWORD_MIN` | `12` | Độ dài tối thiểu (tối đa 128) |
| `IAM_KEY_ENCRYPTION_KEY` / `_FILE` | — (bắt buộc) | 32 byte base64, mã hoá khoá ký riêng (AES-256-GCM); sinh bằng `openssl rand -base64 32` |
| `IAM_KEY_RELOAD` | `30s` | Nhịp nạp lại khoá ký và kiểm readiness |
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
| `key-rotate [-activate-if-none]`, `key-activate -kid` | Tạo khoá chờ; kích hoạt (khoá cũ sang nghỉ, luôn đúng một khoá đang ký) |
| `jwks-export -out file [-merge file]` | Ghi JWKS công khai (khoá chờ, đang ký, nghỉ chưa quá 15m30s), ghi file tạm rồi đổi tên |
| `bootstrap -jwks file` | Dev: migrate, tạo khoá đầu nếu chưa có, gộp vào file JWKS |

**Xoay khoá ở phần 1:** `key-rotate` → `jwks-export -out <jwks> -merge <jwks>` → đưa file cho websocket/gateway và khởi động lại lần lượt từng node (D183) → `key-activate -kid <mới>`. Phần 2 thay bước tay bằng NATS KV.

## Cổng, compose, metric

- Compose dev: `postgres` (profile `app` và `postgres`, `shared_buffers` mặc định 512MB), `postgres-init` (tạo role `iam` và database `chatim_iam`, mật khẩu đọc trong psql, không qua argv), `iam-bootstrap` (chạy bằng uid của host, ghi `.local/ws-keys/jwks.json`), `iam-1` ở `127.0.0.1:8084` (`IAM1_PORT`). Websocket và gateway chờ `iam-bootstrap` xong mới khởi động.
- `.env`: `IAM_PG_PASSWORD`, `IAM_KEY_ENCRYPTION_KEY` (`make iam-env` kiểm); không in giá trị.
- Metric `chatim_iam_*`: `login_total{result}`, `refresh_total{result}`, `sessions_revoked_total`, `password_changes_total`, `store_errors_total`, `hash_inflight`, `signing_key_ready`. Alert nhóm `chatim-iam-health`: `ChatimIamHashSaturated`, `ChatimIamRefreshReuse`, `ChatimIamNoSigningKey`, `ChatimIamStoreErrors`.
- Log: `make iam-logs`. Image: `make image TARGET=apps/iam` (`chatim/iam:dev`). itest: `CHATIM_IT_PG_URI` (database tạm `iam_it_*` mỗi lần chạy).
