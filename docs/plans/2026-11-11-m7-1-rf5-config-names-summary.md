# M7.1 RF5: tên config chung giữa các app — tóm tắt

> Ngày: 2026-11-11 · Nhánh `feat/m7-1` · Trạng thái: **`dev-done` 2026-11-11** (`1452556` → `5fe2956`, đóng ở commit kế), viết lại theo cái đã xây · Dựa trên: owner 2026-11-10 (một giới hạn body mỗi app, tên chung `APP_BODY_SIZE_MAX`, muốn mức riêng thì đặt ở env container), owner 2026-11-11 (áp rule thì sửa hết chỗ lệch; không đặt env thì dùng mặc định của app; làm sau P1, trước P2; một phase)
> **M7.1:** P1 giới hạn cho client ✅ · **RF5** ✅ phase này · P2 tệp của room · P3 id 4 member đầu · P4 ảnh nhóm

## Scope và ranh giới

| Việc | Trong RF5? | Thuộc về |
|---|---|---|
| Đổi tên biến env cùng nghĩa ở nhiều app sang một tên chung (bảng §3) | Có | config chat, gateway, websocket, `identity`; `pkg/auth` |
| Compose, `.env.example`, chatcli, chatbench, itest chat, docs vận hành và tính năng | Có | như cột trái |
| Rule tên config vào CLAUDE.md | Có, khi đổi tên xong | CLAUDE.md §Architecture |
| Mặc định, kiểm hợp lệ, hành vi của mỗi app | Không đổi | mỗi app giữ mặc định của mình |
| Biến chỉ có ở một app | Không đổi tên | giữ tên hiện có |
| Đọc song song tên cũ | Không | chưa có production |
| Lịch sử (`docs/decisions/`, summary cũ) | Không sửa | giữ như đã viết |

Lớp dữ liệu (architecture §4), event, field DB: không áp dụng.

## 1. Thuật ngữ

| Từ | Nghĩa |
|---|---|
| Cùng nghĩa | Hai biến điều khiển cùng một việc ở hai app, kể cả khi mặc định khác nhau (drain 2s ở chat, 5s ở `identity`) |
| Tên chung | Một tên mọi app cùng đọc; giá trị khác nhau theo app đặt ở env của từng container |
| Bốn họ tên | `APP_*` (thứ app nào cũng có), tên tài nguyên (`MONGO_*`, `REDIS_*`, `SHARED_REDIS_*`, `NATS_URL`), tên service được gọi (`CHAT_REQUEST_DEADLINE`, `IDENTITY_*`, `WS_PUBLISH_*`), `JWT_*` (token chat) |

## 2. Bức tranh chung

Hôm nay muốn đổi thời gian drain của cả hệ thống, người vận hành phải nhớ bốn tên: `CHAT_DRAIN_DELAY`, `GW_DRAIN_DELAY`, `WS_DRAIN_DELAY`, `IDENTITY_DRAIN_DELAY`. Issuer của token chat đặt ba lần dưới ba tên (`IDENTITY_ISSUER`, `GW_JWT_ISSUER`, `WS_JWT_ISSUER`) và phải trùng nhau; sai một chỗ thì mọi token bị từ chối. Sau RF5: một tên `APP_DRAIN_DELAY`, mỗi container đặt giá trị của nó; issuer chỉ còn `JWT_ISSUER`, compose đặt một lần ở phần env chung.

## 3. Bảng đổi tên

| Tên mới | Tên cũ | App |
|---|---|---|
| `APP_ADDR` | `CHAT_GRPC_ADDR`, `GW_ADDR`, `WS_ADDR`, `IDENTITY_ADDR` | 4 |
| `APP_ADMIN_ADDR` | `CHAT_`, `GW_`, `WS_`, `IDENTITY_ADMIN_ADDR` | 4 |
| `APP_NODE_ID` | `CHAT_NODE_ID`, `WS_NODE_ID` | chat, websocket |
| `APP_DRAIN_DELAY`, `APP_SHUTDOWN_BUDGET` | `CHAT_`, `GW_`, `WS_`, `IDENTITY_` + cùng hậu tố | 4 |
| `APP_REQUEST_DEADLINE` | `CHAT_REQUEST_DEADLINE` của chat (hạn phía server) | chat |
| `APP_INFLIGHT_MAX` | `CHAT_MAX_INFLIGHT`, `GW_INFLIGHT_MAX`, `IDENTITY_INFLIGHT_MAX` | chat, gateway, `identity` |
| `APP_READ_HEADER_TIMEOUT`, `APP_READ_TIMEOUT`, `APP_WRITE_TIMEOUT`, `APP_IDLE_TIMEOUT`, `APP_MAX_HEADER_BYTES`, `APP_CORS_ORIGINS` | `GW_`, `IDENTITY_` + cùng hậu tố | gateway, `identity` |
| `APP_USER_RATE`, `APP_USER_BURST`, `APP_USER_INFLIGHT`, `APP_USER_LIMITERS` | `GW_`, `IDENTITY_` + cùng hậu tố | gateway, `identity` |
| `MONGO_URI`, `MONGO_DB`, `MONGO_USER`, `MONGO_PASSWORD`(`_FILE`), `MONGO_AUTH_SOURCE` | `IDENTITY_MONGO_*` | `identity` (chat đã dùng) |
| `SHARED_REDIS_ADDR`, `_DB`, `_PASSWORD`(`_FILE`), `_TIMEOUT`, `_POOL` | `GW_SHARED_REDIS_*`, `WS_PRESENCE_REDIS_*` | gateway, websocket |
| `IDENTITY_INTERNAL_URL`, `IDENTITY_JWKS_URL`, `IDENTITY_KEYS_INTERVAL`, `IDENTITY_REVOCATIONS_INTERVAL`, `IDENTITY_READY_TIMEOUT`, `IDENTITY_ALLOW_HTTP` | `GW_IDENTITY_*`, `WS_IDENTITY_*` | gateway, websocket |
| `IDENTITY_NODE_SECRET` | `GW_IDENTITY_SECRET`, `WS_IDENTITY_SECRET` | gateway, websocket (`identity` đã dùng) |
| `JWT_ISSUER`, `JWT_AUDIENCE` | `GW_JWT_*`, `WS_JWT_*`, `IDENTITY_ISSUER`, `IDENTITY_AUDIENCE` | gateway, websocket, `identity` |
| `JWT_KEYS_FILE`, `JWT_LEEWAY` | `GW_JWT_*`, `WS_JWT_*` | gateway, websocket |
| `WS_PUBLISH_STREAM`, `WS_PUBLISH_ROOT` | `MENTION_WS_PUBLISH_*` | chat (websocket đã dùng) |

**Giữ tên, kèm lý do:** `GW_CLIENT_IP_HEADER` và `IDENTITY_CLIENT_IP_HEADER` (gateway chỉ tin header từ proxy trong `GW_TRUSTED_PROXIES`, `identity` tin từ mọi nơi; gộp thì giá trị đặt cho gateway làm `identity` nhận IP giả); `WS_WRITE_TIMEOUT` (hạn ghi một frame websocket, khác hạn ghi response HTTP); `CHAT_REQUEST_DEADLINE` ở gateway và websocket (hạn khi gọi chat, 5s; khác hạn phía server của chat, 3s); biến compose đọc từ `.env` (`IDENTITY_MONGO_PASSWORD`, `GW_CORS_ORIGINS`, `IDENTITY_CORS_ORIGINS`: đầu vào của compose để đặt giá trị từng service, không phải biến app); `CHAT_QUEUE_WAIT` và mọi biến chỉ một app đọc.

## 4. Luồng

Không đổi luồng nào. Mỗi app đọc tên mới thay tên cũ ở đúng chỗ đang đọc, cùng mặc định và cùng luật kiểm; thông báo lỗi lúc khởi động, help metric, log và summary alert nêu tên mới. `pkg/auth` bỏ tham số tiền tố của hàm đọc cấu hình kết nối `identity`. Compose đặt `JWT_ISSUER`, `JWT_AUDIENCE` một lần trong anchor `x-jwt-env`, gộp vào env của `identity`, websocket và gateway.

## 5. Đúng đắn

- Các giá trị bắt buộc trùng nhau giữa app (issuer, audience, secret node, stream `WS_PUBLISH_*`, Redis chung) giờ một tên, compose đặt ở một chỗ: bớt một kiểu cấu hình lệch.
- Không đọc tên cũ: máy dev hay môi trường nào còn tên cũ thì biến bắt buộc thiếu làm app không khởi động và báo tên mới; biến không bắt buộc dùng mặc định.

## 6–7. Event, thư viện, hạ tầng

Không event mới, không thư viện mới. Compose đổi tên biến, không đổi service.

## 8. Quyết định và phương án bị loại

| Quyết định | Lý do | Phương án bị loại |
|---|---|---|
| Bốn họ tên | `APP_` theo tên owner chọn; tài nguyên và service được gọi đã có tiền lệ (`REDIS_ADDR`, `CHAT_REQUEST_DEADLINE`) | Một tiền tố `APP_` cho mọi thứ: `APP_MONGO_URI` đọc như Mongo của riêng app |
| Một tên không mang hai nghĩa | `CHAT_REQUEST_DEADLINE` đang là hai hạn khác nhau (3s server, 5s client) | Giữ: người vận hành đổi một số mà tưởng đổi cả hai |
| Một phase, bốn commit | Đổi tên cơ học; compose, code, docs phải cùng đổi thì `app-up` mới chạy | Mỗi app một phase: thêm summary và review mà không giảm rủi ro |
| Không đọc tên cũ | Chưa có production | Đọc song song: thêm code để xoá sau |

## 9. Chi phí và tải

Không có.

## 10. Rủi ro và giới hạn

- `.env` dev không phải sửa: biến compose đọc từ `.env` để đặt giá trị cho từng service (`GW_CORS_ORIGINS`, `IDENTITY_CORS_ORIGINS`, `IDENTITY_MONGO_PASSWORD`) giữ tên, compose chuyển chúng vào tên app mới; secret và `JWT_*` đến từ compose.
- Mặc định khác nhau theo app (`MONGO_DB` `chatim` / `chatim_identity`, drain, budget): docs mỗi app ghi mặc định của chính nó; cấu hình dùng chung cho nhiều app (ConfigMap) không được đặt `MONGO_DB` cho `identity`.

## 11. Điểm đội tự chọn (owner phản biện)

- `SHARED_REDIS_POOL` gộp vào họ Redis chung dù chỉ websocket đọc, để cả nhóm biến của một Redis cùng tiền tố.
- `APP_INFLIGHT_MAX` của chat giữ mặc định 2048 và hàng chờ `CHAT_QUEUE_WAIT` phía trước, như hôm nay.

## 12. Kiểm thử (như đã chạy)

| Test | Chứng minh |
|---|---|
| Unit config của 4 app và `pkg/auth`: test đang có đổi sang tên mới (mặc định, đọc giá trị, luật kiểm, log) | Đọc đúng tên mới, hành vi không đổi |
| Unit config: `TestAppPrefixedNamesAreNotRead` ở chat, gateway, websocket, `identity`; `TestFromEnvIgnoresAppPrefixedNames` ở `pkg/auth` (tên cũ đặt rác vẫn dùng mặc định; biến bắt buộc chỉ đặt tên cũ thì lỗi nêu tên mới) | Tên cũ không còn được đọc |
| `git grep` tên cũ | Chỉ còn trong `docs/decisions`, `docs/plans` và nguồn secret `IDENTITY_MONGO_PASSWORD` |
| `make fmt-check`, `vet`, `lint`, test các package chạm | Không vỡ chỗ khác |
| `make itest` một lần | Itest chat đặt env theo tên mới |
| `make app-up && make e2e` | Compose đặt đúng tên mới đầu cuối |

Mức review: rủi ro thấp (config, compose): controller kiểm, không reviewer subagent (CLAUDE.md §Verification).

## Bảng phủ

| Yêu cầu / quyết định | Thành phần | Task | Test | Bằng chứng |
|---|---|---|---|---|
| Tên chung cho gateway, websocket (owner 2026-11-11) | `pkg/auth`, config gateway và websocket | T1 | Unit `pkg/auth`, config hai app | `1452556`; test ok |
| Tên chung cho `identity` | config `identity` | T2 | Unit config `identity` | `bee0a27`; test ok, mặc định `MONGO_DB` = `chatim_identity` giữ |
| Tên chung cho chat, tách `APP_REQUEST_DEADLINE` | config chat, `apps/chat/itest`, `tools/poc/chatbench` | T3 | Unit config chat, `make itest` | `756cd6f`; test ok; `make itest` 115 package ok (1 test chập chờn có từ trước, xem Mục mang sang) |
| Compose, chatcli, alert, docs, rule CLAUDE.md | `deploy/compose`, `deploy/prometheus`, `tools/chatcli`, `docs/apps`, `docs/features/05`, `06`, `09`, `10`, `11`, `docs/events.md`, `docs/architecture.md`, CLAUDE.md | T4 | grep tên cũ, `make alerts-check`, `make app-up && make e2e` | `5fe2956` + commit đóng phase; `alerts-check` 42 luật; e2e PASS 16 phase |
| Không đổi hành vi (owner 2026-11-11) | mọi task | T1–T4 | Test hiện có xanh với tên mới, e2e | như trên |

Kiểm cuối (2026-11-11): `make fmt-check`, `vet`, `lint` (0 issues) sạch; `git grep` tên cũ chỉ còn ở test tên cũ, nguồn secret và nội suy `.env` của compose, `CHAT_REQUEST_DEADLINE` phía gateway và websocket; `make itest`; `make app-up` image mới; `make e2e` PASS đủ 16 phase.

Lệch so với plan: không.

## Mục mang sang

- Test chập chờn `TestRevokedSessionSendsTheRunningConnectionBackToWaiting` (`apps/websocket/internal/session`, `synctest`): fail một lần trong `make itest` (seed shuffle `1794373108545490287`, "deadlock: main bubble goroutine has exited"), chạy lại cùng seed 20 lần đều ok; gói không đổi trong RF5. Sửa riêng bằng nhánh `fix/`.
- `.env` của máy dev không phải sửa.
