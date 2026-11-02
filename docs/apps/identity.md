# App `identity`

Chat không giữ danh tính (owner 2026-11-02, D239): không mật khẩu, đăng nhập, phiên, refresh token, role. `identity` là cầu nối: tin issuer của từng tenant, đổi JWT do backend tenant ký lấy token chat Ed25519 5 phút (`POST /v1/token`, D240–D241), giữ bản sao user và nhóm (danh bạ trong RAM mỗi replica, `DirectoryService` cho client, feed thay đổi cho app khác trên cổng nội bộ, D226–D231), thu hồi theo tenant và khoá ký tới websocket/gateway qua HTTPS, không qua NATS (D219–D225), audit log. `identity` không đọc bảng của core, core không đọc bảng của `identity`. Store hiện là Postgres riêng; phase PM chuyển sang MongoDB (D244), nên các mục Postgres dưới đây chỉ đúng tới PM. Thiết kế: [tóm tắt PA](../plans/2026-11-02-m6-pa-identity-doi-token-summary.md), `docs/decision-log.md` §17.15–§17.18.

## Biến môi trường

Đọc qua `apps/identity/internal/config`; sai thì node không khởi động và báo gộp mọi lỗi. Biến `*_FILE` đọc secret từ file (compose secret).

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `IDENTITY_ADDR` | `:8080` | HTTP/1.1 và h2c công khai (chỉ cổng này đứng sau LB); đặt rỗng là tắt |
| `IDENTITY_INTERNAL_ADDR` | `:8081` | Cổng nội bộ cho node websocket/gateway (danh sách thu hồi), **không** đứng sau LB; đặt rỗng là tắt. Phải bật ít nhất một trong hai cổng |
| `IDENTITY_NODE_SECRET` / `_FILE` | — (bắt buộc khi bật cổng nội bộ) | Secret node gửi trong `Authorization: Bearer`, tối thiểu 32 byte (`openssl rand -hex 32`); so bằng hằng thời gian |
| `IDENTITY_ADMIN_ADDR` | `:9090` | `/livez`, `/readyz`, `/metrics`, pprof; **không bao giờ public**; `/app probe` hỏi địa chỉ này |
| `IDENTITY_PG_URI` | — (bắt buộc) | URI Postgres **không** kèm mật khẩu |
| `IDENTITY_PG_PASSWORD` / `_FILE` | — | Mật khẩu role `identity` |
| `IDENTITY_PG_MAX_CONNS` | `20` | Kết nối tối đa của pool |
| `IDENTITY_ISSUER`, `IDENTITY_AUDIENCE` | — (bắt buộc) | `iss`, `aud` của access token; phải khớp `WS_JWT_*`/`GW_JWT_*` |
| `IDENTITY_TOKEN_TTL` | `5m` | Hạn token chat do `identity` cấp; cũng là cửa sổ giữ khoá ký nghỉ và mục thu hồi (`IDENTITY_TOKEN_TTL` + 1m, phải ≤ 2h). Thay `IDENTITY_ACCESS_TTL` (không còn đọc) |
| `IDENTITY_EXCHANGE_AUDIENCE` | — (bắt buộc) | `aud` mà token của tenant phải mang khi đổi lấy token chat; phải khác `IDENTITY_AUDIENCE`, để token chat không bao giờ được nhận như token tenant; compose dev `chatim` |
| `IDENTITY_EXCHANGE_MAX_LIFETIME` | `10m` | `exp` − `iat` tối đa của token tenant; dài hơn thì từ chối |
| `IDENTITY_TRUST_RELOAD` | `10s` | Nhịp nạp lại issuer và khoá tin cậy từ Postgres; tối đa `1m` |
| `IDENTITY_EXCHANGE_IP_RATE`, `IDENTITY_EXCHANGE_IP_BURST` | `50` / `500` | Lần đổi token mỗi IP mỗi node (token bucket); quá thì `429`. Ước tính, chưa có nguồn số: văn phòng NAT 2.000 người nối lại trong 1 phút ≈ 33/s (controller Q4 2026-11-02); compose dev đọc `IDENTITY_EXCHANGE_IP_RATE` từ `.env` để bench ghi đè |
| `IDENTITY_EXCHANGE_IP_TRACKED` | `100000` | Số IP giữ bộ đếm (LRU) của giới hạn đổi token |
| `IDENTITY_KEY_ENCRYPTION_KEY` / `_FILE` | — (bắt buộc) | 32 byte base64, mã hoá khoá ký riêng (AES-256-GCM); sinh bằng `openssl rand -base64 32` |
| `IDENTITY_KEY_RELOAD` | `30s` | Nhịp nạp lại khoá ký, tự kích hoạt khoá chờ đã đủ giờ, kiểm readiness và xoá dòng thu hồi đã hết hạn; compose dev `5s` |
| `IDENTITY_KEY_ACTIVATE_LEAD` | `2m` | Khoá chờ được `identity` tự kích hoạt khi đủ tuổi này; `key-activate` từ chối khoá chưa đủ tuổi. Phải ≥ `IDENTITY_KEY_RELOAD` + 1m (khoảng hỏi khoá tối đa của node), sai thì `identity` không khởi động; compose dev `65s` |
| `IDENTITY_BODY_MAX` | `16384` | Cỡ body tối đa |
| `IDENTITY_CORS_ORIGINS` | rỗng | Origin trình duyệt được phép, đúng từng origin |
| `IDENTITY_CLIENT_IP_HEADER` | rỗng | Header IP do edge ghi đè (`X-Real-IP`); `X-Forwarded-For` lấy phần tử cuối; rỗng = `RemoteAddr` |
| `IDENTITY_DRAIN_DELAY`, `IDENTITY_SHUTDOWN_BUDGET` | `5s` / `25s` | Dừng: `/readyz` false, drain, `http.Shutdown`, đóng pool; budget > drain + write timeout + 1s |
| `IDENTITY_READ_HEADER_TIMEOUT`, `IDENTITY_READ_TIMEOUT`, `IDENTITY_WRITE_TIMEOUT`, `IDENTITY_IDLE_TIMEOUT` | `5s` / `10s` / `15s` / `60s` | Hạn của HTTP server |
| `IDENTITY_MAX_HEADER_BYTES` | `16384` | Cỡ header tối đa |
| `IDENTITY_FEED_WAIT_MAX` | `10s` | Thời gian chờ tối đa của một long poll trên feed danh bạ (cổng nội bộ); từ `1s` đến `60s` |
| `IDENTITY_DIRECTORY_LOAD_SLOTS` | `4` | Số replica được nạp toàn bộ danh bạ cùng lúc (khoá advisory trên Postgres); ≥ 1 và `IDENTITY_DIRECTORY_LOAD_SLOTS` + 2 ≤ `IDENTITY_PG_MAX_CONNS`, sai thì không khởi động, để lệnh ghi và danh sách thu hồi luôn còn kết nối |
| `IDENTITY_INFLIGHT_MAX` | `10000` | Lời gọi `DirectoryService` chạy cùng lúc mỗi node; quá thì `unavailable` |
| `IDENTITY_USER_RATE`, `IDENTITY_USER_BURST` | `50` / `100` | Lời gọi `DirectoryService` mỗi người dùng mỗi node (token bucket); quá thì `resource_exhausted` |
| `IDENTITY_USER_INFLIGHT` | `32` | Lời gọi `DirectoryService` chạy cùng lúc của một người dùng; quá thì `resource_exhausted` |
| `IDENTITY_USER_LIMITERS` | `100000` | Số người dùng giữ bộ đếm (LRU) của `DirectoryService`; phải ≥ `IDENTITY_INFLIGHT_MAX` |

`/readyz` lần đầu true khi ping được Postgres, schema đúng phiên bản migration mới nhất trong binary, có đúng một khoá đang ký, danh bạ đã nạp xong vào RAM và issuer/khoá tin cậy đã nạp lần đầu (kiểm ngay khi mỗi thứ nạp xong, không chờ `IDENTITY_KEY_RELOAD`). Sau lần ready đầu, chỉ còn cần khoá ký và danh bạ; issuer/khoá tin cậy nạp lại lỗi thì giữ bản cũ và replica vẫn ready (`trust_reload_errors_total` tăng) (không ping Postgres và hỏi schema nữa, chỉ nạp lại khoá): Postgres mất thì replica vẫn ready vì `GetUsers`, `GetMyProfile` và JWKS trả từ RAM. Mỗi lần kiểm (nạp khoá, ping, schema), kích hoạt khoá đến hạn và dọn mục thu hồi hết hạn đều có hạn 3s: Postgres treo (mất gói) thì lần kiểm đó tính là lỗi, không treo đến timeout TCP. Việc kiểm chạy trong goroutine riêng sau khi admin đã phục vụ, nên lúc khởi động `/livez` trả ngay và SIGTERM dừng được; nạp danh bạ xong chỉ báo goroutine đó kiểm lại ngay, không chờ, nên follower chạy luôn. Khởi động khi Postgres chưa lên thì process không thoát: `/livez` OK, `/readyz` false, nạp danh bạ thử lại có backoff đến khi Postgres lên. `GOMEMLIMIT` chưa đặt thì log cảnh báo một lần lúc khởi động và metric `memory_limit_set` = 0.

## API công khai

| Đường | Ghi chú |
|---|---|
| `GET /.well-known/jwks.json` | Khoá công khai (chờ, đang ký, nghỉ chưa quá `IDENTITY_TOKEN_TTL` + 30s, mặc định 5m30s), `Cache-Control: public, max-age=60`; chưa có khoá → 503. Node prod lấy khoá **chỉ** từ đường này qua HTTPS của LB |
| `POST /v1/token` | Đổi token tenant lấy token chat (RFC 8693, mục dưới) |
| `POST /chatim.identity.v1.DirectoryService/GetUsers` | Connect, Bearer access token của `identity`. 1–500 `user_ids` mỗi lần (id trùng gộp lại); trả `id`, `display_name`, `avatar_url`, `state` (`ACTIVE`/`DISABLED`) **từ RAM**, không chạm Postgres; **không bao giờ** có email hay username. Id lạ hoặc của tenant khác bị bỏ qua, không báo lỗi. Ví dụ: Lan gửi 300 id người trong danh sách hội thoại, nhận lại 300 tên |
| `POST /chatim.identity.v1.DirectoryService/GetMyProfile` | Bearer; hồ sơ của chính mình (`id`, `display_name`, `avatar_url`) đọc **từ RAM** theo `tid`, `sub` của token, không chạm Postgres; không có trong danh bạ → `not_found`. `UpdateMyProfile` đã bỏ; đổi tên, ảnh bằng lệnh `user-set` |

**Đổi token `POST /v1/token`** (cổng public `IDENTITY_ADDR`). Body `application/x-www-form-urlencoded`, đúng một lần mỗi tham số: `grant_type=urn:ietf:params:oauth:grant-type:token-exchange`, `subject_token=<JWT do backend hoặc IdP của tenant ký>`, `subject_token_type=urn:ietf:params:oauth:token-type:jwt`; tham số khác bỏ qua. Thứ tự kiểm: content type và form → `grant_type` → giới hạn IP (`IDENTITY_EXCHANGE_IP_RATE`/`_BURST`, IP lấy từ `IDENTITY_CLIENT_IP_HEADER` khi đặt, không thì địa chỉ TCP) → `subject_token_type`, `subject_token` → kiểm token tenant (summary §4.1 bước 1–7: `kid`, `alg` đúng khoá, issuer và khoá active, chữ ký, `aud` = `IDENTITY_EXCHANGE_AUDIENCE`, `exp`/`iat` ≤ `IDENTITY_EXCHANGE_MAX_LIFETIME`, tenant active và `tid` khớp, `sub` theo `pkg/ids`) → user trong danh bạ RAM không bị khoá → khoá ký đang dùng → JIT hồ sơ (tạo user mới, sửa `display_name`/`avatar_url` khi claim `name`/`picture` hợp lệ khác bản RAM; `picture` rỗng hay sai, `name` sai coi như vắng; giống thì không ghi; CAS thua vẫn cấp) → ký token chat (`sid` mới, hạn `IDENTITY_TOKEN_TTL`). Khoá tenant (`tenant-disable`) phát `NOTIFY rev`, follower danh bạ đánh thức việc nạp lại issuer ngay nên lần đổi kế tiếp bị từ chối trong vài ms, không chờ `IDENTITY_TRUST_RELOAD`; token chat đã cấp trước đó sống tới hết hạn (≤ 5 phút) hoặc bị node chặn theo `sessions_revoked_at`.

| Kết quả | Trả về |
|---|---|
| Thành công | `200 {"access_token", "issued_token_type": "urn:ietf:params:oauth:token-type:access_token", "token_type": "Bearer", "expires_in"}` (`expires_in` = giây còn lại tới `exp`, làm tròn lên, thường `300`), `Cache-Control: no-store` |
| Thiếu, lặp tham số, sai content type, `subject_token_type` khác `…:jwt` | `400 {"error": "invalid_request"}` |
| `grant_type` khác token exchange | `400 {"error": "unsupported_grant_type"}` |
| Token tenant sai (mọi bước 1–7), tenant hay user bị khoá | `400 {"error": "invalid_grant"}`, body, `Content-Length` và header giống hệt nhau mọi ca; lý do (`reason`, `iss`, `kid`, `ip`, không bao giờ token) chỉ ở một dòng log Info `token exchange refused` |
| Body quá `IDENTITY_BODY_MAX` | `413 {"error": "invalid_request"}` |
| Quá giới hạn IP | `429 {"error": "temporarily_unavailable"}` + `Retry-After: 1` |
| Danh bạ hay issuer chưa nạp, không có khoá ký, Postgres lỗi khi JIT cần ghi | `503 {"error": "temporarily_unavailable"}` + `Retry-After: 1`; nguyên nhân ở một dòng log Warn `token exchange unavailable`. Không có khoá ký thì không ghi JIT |

Mọi body lỗi có thêm `error_description` cố định theo mã. Ví dụ dev: `curl -s localhost:8084/v1/token -d grant_type=urn:ietf:params:oauth:grant-type:token-exchange -d subject_token_type=urn:ietf:params:oauth:token-type:jwt -d subject_token=$TENANT_JWT`.

**Giới hạn và mã lỗi của `DirectoryService`** (cả hai lệnh): body tối đa 64 KiB; mỗi người dùng `IDENTITY_USER_RATE`/`IDENTITY_USER_BURST` lời gọi và `IDENTITY_USER_INFLIGHT` lời gọi chạy cùng lúc mỗi node; cả node tối đa `IDENTITY_INFLIGHT_MAX` lời gọi chạy cùng lúc. Token kiểm bằng khoá ký và danh sách thu hồi **trong RAM** (cùng chuông `NOTIFY` với danh bạ), không đọc bảng `sessions`, nên phiên vừa bị thu hồi còn đọc được thêm vài ms (tối đa ~2s khi kết nối `LISTEN` đứt).

| Mã | Khi nào |
|---|---|
| `invalid_argument` | Không có id, quá 500 id, id sai dạng |
| `unauthenticated` | Thiếu hoặc hỏng token (`unauthenticated`); token đã thu hồi (`session revoked`) |
| `not_found` | `GetMyProfile` của user chưa có trong danh bạ |
| `resource_exhausted` | Quá giới hạn theo người dùng; chờ rồi gọi lại |
| `unavailable` + `Retry-After: 1` | Replica chưa nạp xong danh bạ (`directory is loading`); node đầy `IDENTITY_INFLIGHT_MAX` |

**Cổng nội bộ** (`IDENTITY_INTERNAL_ADDR`): `GET /internal/v1/revocations` và `GET /internal/v1/directory/changes` (cả hai cần header `Authorization: Bearer <IDENTITY_NODE_SECRET>`; sai/thiếu → 401). Trả mọi mục còn hạn: `sessions[{session_id, tenant_id, expires_at}]` (bảng `revoked_sessions`; hiện không có đường nào ghi, vòng dọn vẫn chạy; PB quyết định) và `tenants[{tenant_id, revoked_before, expires_at}]` (khoá tenant, giờ Postgres). Mục giữ `IDENTITY_TOKEN_TTL` + 1 phút. `ETag` theo băm body, `If-None-Match` khớp → 304. Đường khác → 404, kể cả JWKS.

**Feed danh bạ** `GET /internal/v1/directory/changes?after=&limit=&wait=` (long poll, cho app khác: Mention sau khi `identity` xong (D234), M8, M9; xem [events.md §8](../events.md)):

| Tham số | Ý nghĩa |
|---|---|
| `after` | Vị trí đã đọc tới (chuỗi `next` của lần trước, giữ nguyên, không tự tạo); rỗng hoặc không có = đọc từ đầu, tức toàn bộ danh bạ |
| `limit` | 1–1.000 bản ghi mỗi trang, mặc định 500 |
| `wait` | Số giây nguyên ≥ 0, cắt ở `IDENTITY_FEED_WAIT_MAX`; không có hoặc `0` = trả ngay. Không có gì mới thì `identity` giữ request tới khi có thay đổi (chuông `NOTIFY`, xem lại mỗi 2s) hoặc hết `wait` |

Trả 200 `{"changes": [...], "next": "…"}`, `Cache-Control: no-store`. Hết `wait` hoặc `identity` đang tắt → `changes` rỗng và `next` = `after` cũ. Bản ghi:
- `{"kind":"user","tenant_id","id","ver","state":"active"|"disabled","display_name","avatar_url","groups":[nhóm trực tiếp]}`;
- `{"kind":"group","tenant_id","id","ver","state":"live"|"deleted","name","parent_id","mentionable"}`.

Không có email và username. Mã lỗi: sai hoặc thiếu secret → 401; `after`, `limit`, `wait` sai dạng hoặc lặp tham số → 400 `{"error": …}`; Postgres lỗi → 503 + `Retry-After: 1`; `HEAD` → 405. Bên gọi giữ **một** kết nối keep-alive và gọi lại ngay với `after = next`, không mở kết nối mới mỗi lần. Số bên gọi dự kiến nhỏ (mỗi app tiêu thụ một request đang chờ), nên không có trần số request đang chờ: cổng nội bộ nằm sau `IDENTITY_NODE_SECRET`.

**Node đồng bộ** (`pkg/identitysync`, websocket và gateway như nhau, tiền tố `WS_`/`GW_`): `*_IDENTITY_INTERNAL_URL`, `*_IDENTITY_SECRET`/`_FILE` và `*_IDENTITY_JWKS_URL` **bắt buộc** (JWKS phải `https://` trừ khi `*_IDENTITY_ALLOW_HTTP=true`, chỉ cho dev), `*_IDENTITY_REVOCATIONS_INTERVAL` (`2s`), `*_IDENTITY_KEYS_INTERVAL` (`1m`, tối đa `1m`, dev `10s`), `*_IDENTITY_READY_TIMEOUT` (`10s`). Node hỏi danh sách mỗi 2s, gộp vào RAM (chỉ thêm), ghi bản đó lên Redis chung `chatim:identity:revocations:<issuer>` (hạn 60s; issuer = `*_JWT_ISSUER`, nên staging và prod chung Redis không trộn nhau); hỏi `identity` lỗi thì đọc bản Redis do node khác ghi. Khoá ký không bao giờ đi qua Redis. `kid` lạ → hỏi JWKS ngay (tối đa 1 lần/5s), trong lúc chờ websocket trả `retry_later`, gateway 503. Node chỉ `ready` sau lần đồng bộ đầu hoặc sau `*_IDENTITY_READY_TIMEOUT` (khi `identity` không với tới). `identity` trả 4xx (trừ 429), ví dụ sai secret (401) hay sai đường (404) → node **không ready**, log lỗi `identity refused this node`, metric `identity_refused{kind}` = mã lỗi, kể cả khi đọc được bản Redis; `identity` nhận lại thì node `ready`. `*_JWT_LEEWAY` tối đa 1 phút. `IDENTITY_TOKEN_TTL` + 1m phải ≤ 2h (node bỏ mục thu hồi hạn xa hơn), sai thì `identity` không khởi động.

## Lệnh admin

Chạy trong container (`make identity-admin ARGS="…"` = `docker exec chatim-identity-1 /app …`); kết quả ra stdout, log ra stderr; sai cú pháp thoát 2, lỗi thoát 1 (kể cả tên, id nhóm, URL ảnh sai: kiểm bằng hàm của `directory` trước khi chạm store); mọi thay đổi ghi audit `admin-cli` (`tenant_create`, `tenant_disable`, `tenant_enable`, `user_disable`, `user_enable`; nhóm: `group_create`, `group_update`, `group_delete` kèm `detail.group`; `user_groups_set`, `user_set`; issuer: `issuer_add`, `issuer_disable`, `issuer_enable`, `issuer_key_add`, `issuer_key_remove` kèm `detail.issuer`, `detail.kid`, ghi theo tenant của issuer), cột `audit_events.user_id` ghi user bị đổi. User chỉ được tạo bằng đổi token (JIT ở `POST /v1/token`; itest chèn thẳng dòng `users`).

| Lệnh | Việc |
|---|---|
| `migrate [-apply]` | Mặc định chỉ liệt kê migration chờ; `-apply` chạy dưới khoá advisory. Chỉ có một migration `0001_identity.sql` (gộp 1–4 cũ ở M6 PA): `tenants` (kèm `sessions_revoked_at`), `users`, `trusted_issuers`, `trusted_keys` (`algorithm` chỉ `EdDSA`, `ES256`, `RS256`), `signing_keys`, `audit_events`, `revoked_sessions`, `groups` (kèm `ancestors`), `user_groups`, `directory_changes`; `NOTIFY` kênh `identity_changes`. Database đã áp migration cũ 1–4 (`chatim_identity`) không dùng được: health thấy version 4 ≠ 1, node không ready; dùng database mới |
| `tenant-create -id -name`, `tenant-disable -id`, `tenant-enable -id` | Khoá tenant ghi `sessions_revoked_at`: mọi token cấp trước giờ đó bị node từ chối |
| `user-disable`, `user-enable` (`-tenant -user <id>`) | Đổi `state`, tăng `ver`; user không có → `not found`, thoát 1 |
| `key-rotate [-activate-if-none]`, `key-activate -kid` | Tạo khoá chờ, in giờ `identity` sẽ tự kích hoạt; `key-activate` kích hoạt tay (khoá cũ và khoá chờ cũ hơn sang nghỉ, luôn đúng một khoá đang ký), từ chối khoá chưa đủ `IDENTITY_KEY_ACTIVATE_LEAD` và khoá đã nghỉ |
| `jwks-export -out file [-merge file]` | Ghi JWKS công khai (khoá chờ, đang ký, nghỉ chưa quá `IDENTITY_TOKEN_TTL` + 30s, mặc định 5m30s), ghi file tạm rồi đổi tên |
| `bootstrap -jwks file` | Dev: migrate, tạo khoá đầu nếu chưa có, gộp vào file JWKS |
| `group-create -tenant -id -name [-parent] [-mentionable=true]` | Tạo nhóm; `-parent` phải là nhóm còn sống; `-mentionable=false` để không gắn `@nhóm` được. Tenant hay nhóm cha không có → lỗi, thoát 1 |
| `group-update -tenant -id [-name] [-parent] [-mentionable=BOOL]` | Chỉ đổi cờ có ghi trên lệnh; `-parent ""` chuyển nhóm lên gốc; chuyển vào chính nó hoặc nhóm con → lỗi `group cycle`, thoát 1; không ghi cờ nào để đổi → thoát 2 |
| `group-delete -tenant -id` | Xoá mềm nhóm, gỡ mọi thành viên; còn nhóm con sống → lỗi, thoát 1 |
| `user-groups -tenant -user <id> -groups a,b` | Thay **toàn bộ** nhóm của user; tách theo dấu phẩy, bỏ khoảng trắng hai đầu; phần rỗng, id sai, id lặp hay nhóm không có → lỗi, không đổi gì; `-groups ""` (phải ghi rõ) gỡ mọi nhóm |
| `user-set -tenant -user <id> [-name] [-avatar URL]` | Đổi tên hiển thị và/hoặc ảnh, luôn tăng `users.ver` (feed `directory_changes` chỉ tăng khi giá trị thật sự đổi) (phải ít nhất một cờ, không thì thoát 2); ảnh chỉ nhận `https://`, `-avatar ""` xoá ảnh; giá trị sai hay user không có → thoát 1, không đổi gì |
| `issuer-add -issuer I -tenant T` | Tin issuer `I` (giá trị claim `iss` của token tenant) cho tenant `T`; một issuer chỉ thuộc một tenant. `I` 1–255 byte ASCII in được, không khoảng trắng (sai → thoát 2); `I` bằng `IDENTITY_ISSUER` của chính node → từ chối, thoát 1 (token chat không dùng làm token tenant được); tenant không có hay issuer đã có → thoát 1 |
| `issuer-disable -issuer I`, `issuer-enable -issuer I` | Tắt/bật issuer; tắt thì token ký bởi mọi khoá của nó bị từ chối ở lần nạp lại kế tiếp. Issuer không có → thoát 1 |
| `issuer-key-add -issuer I -kid K -alg EdDSA\|ES256\|RS256 -jwk '<JSON công khai>'` | Thêm khoá công khai. `-alg` khác ba giá trị trên → thoát 2. JWK kiểm bằng `trust.ParsePublicJWK` trước khi chạm store: EdDSA = `OKP`/`Ed25519`, ES256 = `EC`/`P-256` (điểm trên đường cong), RS256 = `RSA` 2048–4096 bit, `e` 65537; có thành phần khoá riêng (`d`, `p`, …), `alg`/`use` lệch, hay `kid` trong JWK khác `-kid` → thoát 1, không ghi gì. Lưu JSON chuẩn hoá (`kty`, `crv`, `x`, `y`, `n`, `e`, `kid`, `alg`), không lưu JSON gốc. Nhận đúng output `devtoken` khoá công khai. `-kid` trùng khoá đã có (kể cả đã gỡ) → thoát 1 |
| `issuer-key-remove -issuer I -kid K` | Gỡ khoá (`state` 2, giữ dòng); khoá không có hay đã gỡ → thoát 1 |
| `issuer-keys -issuer I` | Bảng `KID`, `ALG`, `STATE` (`active`/`removed`), `CREATED_AT`; không in khoá |

**Lệnh nhóm.** Lệnh CLI không kiểm quyền: ai vào được container là người vận hành. `group-create`, `group-update`, `group-delete` và `user-groups` chạy một transaction SERIALIZABLE (`pgstore/group_tx.go`), một lần, không retry. Đụng lệnh khác cùng lúc ghi cùng dòng thì lệnh có thể thoát 1 với lỗi `unavailable`; chạy lại là được.

**Xoay khoá:** chỉ một lệnh `key-rotate`. `identity` công bố khoá chờ qua JWKS ở lần nạp lại kế tiếp (≤ `IDENTITY_KEY_RELOAD`), node lấy về trong ≤ 1m, và `identity` tự kích hoạt khoá khi đủ `IDENTITY_KEY_ACTIVATE_LEAD` (nhiều replica cùng thử, Postgres khoá dòng nên chỉ một lần; metric `signing_keys_activated_total`, log `signing key activated`). Không ai phải canh giờ, không khởi động lại node. Lỡ có node chưa có khoá, client chỉ nhận `retry_later`/503 vài giây trong lúc node hỏi JWKS ngay. File JWKS của node chỉ là dự phòng lúc khởi động; khoá đã nằm trong file chỉ gỡ được bằng cách sửa file và khởi động lại node.

**Lưu ý khi setup** (mọi số khác để mặc định; đặt sai thì app không khởi động hoặc node không `ready` kèm log rõ):

1. Tạo `IDENTITY_KEY_ENCRYPTION_KEY` và `IDENTITY_NODE_SECRET`; secret giống nhau ở `identity`, websocket (`WS_IDENTITY_SECRET`) và gateway (`GW_IDENTITY_SECRET`).
2. `IDENTITY_ISSUER`/`IDENTITY_AUDIENCE` trùng `*_JWT_ISSUER`/`*_JWT_AUDIENCE` của node.
3. Node trỏ `*_IDENTITY_INTERNAL_URL` vào cổng nội bộ (`:8081`) và `*_IDENTITY_JWKS_URL` vào địa chỉ public `https://…/.well-known/jwks.json`.
4. Chạy `bootstrap -jwks` một lần, gắn file đó làm `*_JWT_KEYS_FILE` của node.
5. Websocket và gateway dùng chung một Redis presence; mỗi môi trường một issuer riêng.
6. K8s: liveness trỏ `/livez`, readiness trỏ `/readyz`. Trỏ liveness vào `/readyz` thì pod đang nạp danh bạ bị giết liên tục.
7. Cho `identity` một Postgres server riêng: mốc an toàn của feed (`xmin`) tính trên cả cluster, nên một transaction treo ở **bất kỳ** database nào cũng làm feed và cache đứng.
8. Không đặt PgBouncer chế độ transaction trước `identity` (hỏng `LISTEN` và advisory lock theo phiên). Mỗi replica dùng thêm một kết nối Postgres cho `LISTEN`.
9. Mọi replica đặt cùng `IDENTITY_DIRECTORY_LOAD_SLOTS`.
10. Đặt `GOMEMLIMIT` (khoảng 90% trần RAM của pod); chưa đặt thì có alert `ChatimIdentityMemoryLimitUnset`.

### Sửa cây nhóm bằng tay

Cột `groups.ancestors` (chuỗi tổ tiên từ gốc tới cha) do trigger giữ trong cùng transaction: `groups_ancestors_set` (`BEFORE INSERT OR UPDATE OF parent_id`) đặt đường của dòng từ đường của cha và từ chối vòng (`groups_no_cycle`), `groups_ancestors_cascade` (`AFTER UPDATE OF parent_id`) sửa cả cây con trong một câu. Trigger chỉ đúng khi:

- mọi chỗ ghi `groups.parent_id` (tạo nhóm có cha, đổi cha, xoá nhóm) lấy khoá cây của tenant `pg_advisory_lock(2, hashtext(tenant))` ở mức phiên, **trước** `BEGIN` (ở SERIALIZABLE, snapshot chụp ở câu đầu tiên), và nhả sau commit hay rollback. Code `identity` (CLI) đã làm vậy; script hay migration tự ghi cũng phải làm vậy;
- câu SQL tay đổi `parent_id` **một dòng mỗi câu**: một câu `UPDATE` đổi cha nhiều nhóm cùng lúc cho `ancestors` sai;
- `INSERT` nhiều nhóm trong một câu ghi nhóm cha trước nhóm con: khoá ngoại chỉ kiểm ở cuối câu, nên con đứng trước cha vẫn qua nhưng `ancestors` của con sai.

`ancestors` lỡ sai thì chạy câu dựng lại sau (giữ khoá cây của tenant như trên):

```sql
WITH RECURSIVE paths (tenant_id, id, ancestors) AS (
  SELECT tenant_id, id, '{}'::text[] FROM groups WHERE parent_id IS NULL
  UNION ALL
  SELECT g.tenant_id, g.id, p.ancestors || p.id
  FROM groups g JOIN paths p ON g.tenant_id = p.tenant_id AND g.parent_id = p.id
)
UPDATE groups g SET ancestors = p.ancestors
FROM paths p
WHERE g.tenant_id = p.tenant_id AND g.id = p.id AND g.ancestors IS DISTINCT FROM p.ancestors;
```

## Giới hạn đã biết (đo ở PA, sửa ở phase PM)

- **Đổi token cho user mới ~560/s mỗi node** (`tools/poc/exchangebench`, [docs/poc/README.md](../poc/README.md)): mỗi lần JIT tạo user là một commit có `NOTIFY`, và Postgres xếp hàng các commit đó qua một khoá chung. User đã có thì đạt 1.700/s, p99 ~2,1 ms. Ví dụ: 100K người đăng nhập lần đầu cùng lúc cần khoảng 3 phút với một node.
- **Follower danh bạ chạy liên tục sau đợt ghi lớn:** mỗi `NOTIFY` chạy một lượt đọc feed, và câu feed (prepared statement) có thể bị Postgres chốt kế hoạch chung quét cả `directory_changes`. Postgres giữ gần một core tới khi hết hàng thông báo hoặc replica khởi động lại. Có từ phần 3a, owner 2026-11-02 không sửa trên Postgres vì store chuyển sang MongoDB.

## Cổng, compose, metric

- Compose dev: `identity-1` bật cổng nội bộ `:8081` (không publish), websocket/gateway hỏi `http://chatim-identity-1:8081` và JWKS ở `http://chatim-identity-1:8080/.well-known/jwks.json` (`*_IDENTITY_ALLOW_HTTP=true`), Redis chung là `chatim-redis-presence`. `postgres` (profile `app` và `postgres`, `shared_buffers` mặc định 512MB), `postgres-init` (tạo role `identity` và database `chatim_identity`, mật khẩu đọc trong psql, không qua argv), `identity-bootstrap` (chạy bằng uid của host, ghi `.local/ws-keys/jwks.json`), `identity-1` ở `127.0.0.1:8084` (`IDENTITY1_PORT`). Websocket và gateway chờ `identity-bootstrap` xong mới khởi động.
- `.env`: `IDENTITY_PG_PASSWORD`, `IDENTITY_KEY_ENCRYPTION_KEY`, `IDENTITY_NODE_SECRET` (`make identity-env` kiểm); không in giá trị.
- Metric `chatim_identity_*`: `signing_key_ready`. Đổi token: `exchanges_total{result}` (`ok`, `invalid`, `disabled`, `rate`, `error`; một nhãn mỗi request đã qua bước form, request hỏng form không đếm), `jit_writes_total{result}` (`created`, `updated`, `conflict`, `error`), `trust_reload_errors_total` (lần nạp issuer/khoá tin cậy lỗi cộng khoá tin cậy bị bỏ vì không dùng được), `store_errors_total` (lỗi Postgres khi ghi JIT). `revocation_requests_total{result}`, `revocations_cleaned_total`, `revocation_cleanup_errors_total`, `signing_keys_activated_total`. Alert nhóm `chatim-identity-health`: `ChatimIdentityNoSigningKey` (không cấp được token chat, đổi token trả 503), `ChatimIdentityStoreErrors` (JIT ghi lỗi, đổi token cần ghi trả 503; series `store_errors_total` đếm lỗi ghi JIT), `ChatimIdentityExchangeErrors` (`exchanges_total{result="error"}` tăng trong 5 phút: không khoá ký, danh bạ chưa nạp hoặc Postgres lỗi khi JIT), `ChatimIdentityTrustStale` (suốt 3 phút replica không nạp được issuer/khoá tin cậy hoặc có khoá không dùng được, ví dụ JWK hỏng ghi tay vào DB; nạp lỗi thì khoá vừa gỡ vẫn dùng được ở replica đó). Danh bạ: `directory_ready`, `directory_entries`, `directory_load_seconds`, `directory_load_failures_total`, `directory_lag_seconds` (giây từ lần cuối đuổi kịp feed), `directory_listen_up`, `directory_horizon_stalled_seconds` (giây feed chờ một transaction còn mở dưới mốc), `directory_read_errors_total`, `directory_revocation_errors_total`, `directory_feed_waiting`, `directory_feed_requests_total{result}` (`ok`, `empty`, `bad_request`, `unauthorized`, `error`), `get_users_total{result}` (`ok`, `bad_request`, `unauthenticated`, `limited`, `unavailable`, `error`), `get_users_ids_total`, `user_limited_total`, `inflight_rejected_total`, `memory_limit_set`. Alert nhóm `chatim-identity-directory`: `ChatimIdentityDirectoryNotReady` (replica chưa nạp xong > 1 phút), `ChatimIdentityDirectoryStale` (`directory_lag_seconds` > 30s trong 1 phút), `ChatimIdentityDirectoryFeedStuck` (`directory_horizon_stalled_seconds` > 60s), `ChatimIdentityDirectoryListenDown` (mất `LISTEN` 5 phút), `ChatimIdentityMemoryHigh` (heap > 80% `GOMEMLIMIT` trong 5 phút; chỉ tính khi `GOMEMLIMIT` đã đặt, lọc replica `identity` bằng `memory_limit_set`), `ChatimIdentityMemoryLimitUnset` (10 phút). Phía node (`chatim_websocket_*`/`chatim_gateway_*`): `identity_refused{kind}`, `identity_revocations_age_seconds`, `identity_keys_age_seconds`, `identity_sync_errors_total{kind}`, `identity_shared_reads_total{result}`, `identity_shared_write_errors_total`, `identity_boot_timeouts_total`, `jwks_conflicts_total`, `revocations{kind}`, `revocations_dropped_total`; nhóm `chatim-identity-sync`: `ChatimIdentitySyncStale` (> 30s), `ChatimIdentityKeysStale` (> 3 phút), `ChatimIdentityRefused` (node bị `identity` từ chối > 1 phút), `ChatimJwksConflict`.
- Log: `make identity-logs`. Image: `make image TARGET=apps/identity` (`chatim/identity:dev`). itest: `CHATIM_IT_PG_URI` (database tạm `identity_it_*` mỗi lần chạy). `make infra-up` không bật Postgres: chạy `make pg-up` (hoặc `make core-up`) trước `make itest`. Chỉ chạy package `identity`: `make itest ITEST_ARGS="-race -count=1 ./apps/identity/..."`; `$` trong `ITEST_ARGS` viết `$$`, ví dụ chỉ chạy bench: `make itest ITEST_ARGS="-run '^$$' -bench . ./apps/identity/internal/directory/"`.
