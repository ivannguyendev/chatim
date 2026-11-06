# App `gateway`

HTTPS (Connect trên HTTP/1.1 và h2c) cho tải dữ liệu và quản lý room: 27 lệnh `chatrpc.Request()` (`GetClientConfig`, 4 lệnh upload của `MediaService`, 14 lệnh đọc và `CreateRoom`, `OpenDirectRoom`, `UpdateRoom`, `AddMembers`, `RemoveMember`, `LeaveRoom`, `ChangeMemberRole`, `SetMemberPriority`), JWT như websocket, gọi node chat bất kỳ (round robin), giới hạn theo user. Không có database, không dùng NATS; Redis chỉ (tuỳ chọn) cho bản danh sách thu hồi dùng chung (M6 phần 2). Cơ chế: [module 11](../features/11-feature-https-gateway.md).

## Biến môi trường

Đọc qua `apps/gateway/internal/config`; sai định dạng hoặc vi phạm luật thì node không khởi động và báo gộp mọi lỗi.

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `GW_ADDR` | `:8080` | Lắng nghe HTTP/1.1 và h2c; đường dẫn `/chatim.v1.ChatService/{Lệnh}`, `/chatim.v1.MentionService/{Lệnh}` (Connect, JSON hoặc protobuf, `POST`) |
| `GW_ADMIN_ADDR` | `:9090` | `/livez`, `/readyz` (đã đọc sổ node chat trong Redis state, identity đã đồng bộ lần đầu và chưa tắt; sổ rỗng lúc đang chạy không tắt `/readyz`, lệnh trả `unavailable`), `/metrics` (`chatim_gateway_*`); `/app probe` hỏi địa chỉ này; không publish ra host |
| `REDIS_ADDR`, `REDIS_DB`, `REDIS_PASSWORD` (`_FILE`) | `chatim-redis:6379` / `0` / — | Redis **state**: sổ node chat sống (`chatim:chat-nodes`, như websocket). Mỗi lệnh đi node kế tiếp theo vòng, bỏ qua node có kết nối đang hỏng; không chọn theo slot, không retry của mình. Khác Redis chia sẻ `GW_SHARED_REDIS_*` (bản sao thu hồi của identity). Không đọc được sổ trong 10s lúc khởi động → thoát lỗi. Compose dùng secret `redis_password` |
| `GW_JWT_KEYS_FILE`, `GW_JWT_ISSUER`, `GW_JWT_AUDIENCE` | — (bắt buộc) | Như websocket: JWKS công khai đọc một lần lúc khởi động (đổi khoá = khởi động lại); compose dùng cùng secret `ws_jwks` và `chatim-dev` / `chatim-websocket` |
| `GW_JWT_LEEWAY` | `30s` | Dung sai giờ cho `exp`/`nbf` (0–1 phút) |
| `GW_IDENTITY_*` | như websocket | Đồng bộ danh sách thu hồi và khoá với `identity` ([identity.md](identity.md)); phiên bị thu hồi hoặc tenant bị khoá → 401 (`auth_failures_total{reason="revoked"\|"tenant_revoked"}`), `kid` chưa lấy kịp → 503 (`key_pending`). `ready` khi đã đọc sổ node chat **và** đã đồng bộ lần đầu (hoặc quá `GW_IDENTITY_READY_TIMEOUT`); URL và secret bắt buộc, `identity` trả 4xx → không `ready` |
| `GW_SHARED_REDIS_ADDR`, `GW_SHARED_REDIS_DB`, `GW_SHARED_REDIS_PASSWORD`/`_FILE`, `GW_SHARED_REDIS_TIMEOUT` | rỗng / `0` / — / `500ms` | Redis chung (compose: `chatim-redis-presence`) giữ bản danh sách thu hồi cho node không hỏi được `identity`; rỗng = không dùng |
| `GW_PART_URLS_USER_LIMIT`, `GW_PART_URLS_USER_IP_LIMIT`, `GW_PART_URLS_DEVICE_LIMIT` | `2/1s` / `1/1s` / `1/5s` | Giới hạn `MediaService/GetPartURLs` (owner 2026-11-06) theo user, user + IP, user + IP + thiết bị (`sid` của token); dạng `SỐ/THỜI_LƯỢNG`; đếm chung mọi node trong Redis chung (khoá `chatim:ratelimit:GetPartURLs:*` hết hạn theo cửa sổ). Redis chung rỗng hoặc lỗi: đếm riêng trên từng node, vẫn đủ ba tầng (owner 2026-11-07; tối đa `3 × GW_USER_LIMITERS` khoá trong RAM), log và `command_rate_limit_fallbacks_total` |
| `GW_CLIENT_IP_HEADER`, `GW_TRUSTED_PROXIES` | `X-Forwarded-For` / rỗng | IP máy lấy từ header chỉ khi kết nối đến từ một CIDR trong `GW_TRUSTED_PROXIES` (lấy hop cuối không tin cậy); rỗng = dùng địa chỉ kết nối |
| `GW_BODY_MAX` | `131072` | Cỡ body tối đa; quá thì `resource_exhausted` |
| `GW_USER_RATE`, `GW_USER_BURST` | `50` / `100` | Lệnh mỗi giây và burst mỗi (tenant, user) trên node; quá thì `resource_exhausted` (429) |
| `GW_USER_INFLIGHT` | `32` | Lệnh đang chạy tối đa mỗi (tenant, user) trên node |
| `GW_USER_LIMITERS` | `100000` | Số người giữ bộ đếm trong RAM (LRU, chỉ đẩy người không có lệnh đang chạy; ≥ `GW_INFLIGHT_MAX`) |
| `GW_INFLIGHT_MAX` | `10000` | Lệnh đang chạy tối đa cả node, kiểm trước token; quá thì `unavailable` (503) |
| `GW_CORS_ORIGINS` | rỗng | Origin trình duyệt được phép, đúng từng origin (`https://chat.example.com`), không `*`; rỗng = không cho trình duyệt khác origin; compose dev đặt `http://localhost:3000` (e2e phase 10 kiểm preflight từ origin này) |
| `GW_DRAIN_DELAY`, `GW_SHUTDOWN_BUDGET` | `2s` / `15s` | Dừng: `/readyz` false, chờ drain, chờ lệnh đang chạy; budget phải lớn hơn `GW_DRAIN_DELAY + CHAT_REQUEST_DEADLINE`; compose `stop_grace_period` 18s |
| `GW_READ_HEADER_TIMEOUT`, `GW_READ_TIMEOUT` | `5s` / `10s` | Hạn đọc header và cả request |
| `GW_WRITE_TIMEOUT` | `CHAT_REQUEST_DEADLINE + 5s` | Hạn ghi trả lời (không nhỏ hơn mặc định) |
| `GW_IDLE_TIMEOUT` | `60s` | Giữ kết nối rảnh |
| `GW_MAX_HEADER_BYTES` | `16384` | Cỡ header tối đa; header `Authorization` dài quá 8KB bị từ chối |
| `GW_H2_MAX_STREAMS` | `250` | Số stream HTTP/2 đồng thời mỗi kết nối |
| `CHAT_REQUEST_DEADLINE` | `5s` | Hạn một lần gọi chat |

## Cổng và admin

- HTTP `GW_ADDR` (`:8080`); compose publish `gateway-1` ở `127.0.0.1:8082`, `GW_CORS_ORIGINS=http://localhost:3000`, cả hai node chat.
- Admin `GW_ADMIN_ADDR` (`:9090`, không publish): `/livez`, `/readyz`, `/metrics` (`chatim_gateway_*`, bảng ở [module 11 §6.1](../features/11-feature-https-gateway.md)). Alert `ChatimGatewayChatUnavailable` (> 5% `Unavailable`).
- Log: `make gw-logs`. Image: `make image TARGET=apps/gateway` (`chatim/gateway:dev`).

## Gọi thử

Token dev như websocket ([websocket.md](websocket.md#khoá-và-token-dev)); không in giá trị `.env`:

    curl -H "Authorization: Bearer $(make -s ws-token ARGS='-tenant e2e -user alice')" -H 'Content-Type: application/json' -d '{}' http://127.0.0.1:8082/chatim.v1.ChatService/ListMyRooms

Không token → 401 `unauthenticated`; lệnh realtime (vd `SendMessage`) hoặc nội bộ (`ListMyRoomIDs`, `SetUserGroups`, `SetGroups`) → `unimplemented`. Lỗi của middleware là một HTTP status kèm body JSON của Connect.
