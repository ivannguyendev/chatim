# App `gateway`

HTTPS (Connect trên HTTP/1.1 và h2c) cho tải dữ liệu và quản lý room: 22 lệnh `corecall.Request()` (14 lệnh đọc và `CreateRoom`, `OpenDirectRoom`, `UpdateRoom`, `AddMembers`, `RemoveMember`, `LeaveRoom`, `ChangeMemberRole`, `SetMemberPriority`), JWT như websocket, gọi core bất kỳ (round robin), giới hạn theo user. Không có database, không dùng Redis hay NATS. Cơ chế: [module 11](../modules/11-feature-https-gateway.md).

## Biến môi trường

Đọc qua `apps/gateway/internal/config`; sai định dạng hoặc vi phạm luật thì node không khởi động và báo gộp mọi lỗi.

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `GW_ADDR` | `:8080` | Lắng nghe HTTP/1.1 và h2c; đường dẫn `/chatim.v1.CoreService/{Lệnh}`, `/chatim.v1.MentionService/{Lệnh}` (Connect, JSON hoặc protobuf, `POST`) |
| `GW_ADMIN_ADDR` | `:9090` | `/livez`, `/readyz` (có ít nhất một core READY và chưa tắt), `/metrics` (`chatim_gateway_*`); `/app probe` hỏi địa chỉ này; không publish ra host |
| `GW_CORE_ADDRS` | — (bắt buộc) | Danh sách `host:port` của core, cách nhau dấu phẩy; một kết nối gRPC round robin, không Redis, không retry của mình; compose đặt `chatim-core-1:9000,chatim-core-2:9000` |
| `GW_JWT_KEYS_FILE`, `GW_JWT_ISSUER`, `GW_JWT_AUDIENCE` | — (bắt buộc) | Như websocket: JWKS công khai đọc một lần lúc khởi động (đổi khoá = khởi động lại); compose dùng cùng secret `ws_jwks` và `chatim-dev` / `chatim-websocket` |
| `GW_JWT_LEEWAY` | `30s` | Dung sai giờ cho `exp`/`nbf` (≥ 0) |
| `GW_BODY_MAX` | `131072` | Cỡ body tối đa; quá thì `resource_exhausted` |
| `GW_USER_RATE`, `GW_USER_BURST` | `50` / `100` | Lệnh mỗi giây và burst mỗi (tenant, user) trên node; quá thì `resource_exhausted` (429) |
| `GW_USER_INFLIGHT` | `32` | Lệnh đang chạy tối đa mỗi (tenant, user) trên node |
| `GW_USER_LIMITERS` | `100000` | Số người giữ bộ đếm trong RAM (LRU, chỉ đẩy người không có lệnh đang chạy; ≥ `GW_INFLIGHT_MAX`) |
| `GW_INFLIGHT_MAX` | `10000` | Lệnh đang chạy tối đa cả node, kiểm trước token; quá thì `unavailable` (503) |
| `GW_CORS_ORIGINS` | rỗng | Origin trình duyệt được phép, đúng từng origin (`https://chat.example.com`), không `*`; rỗng = không cho trình duyệt khác origin; compose dev đặt `http://localhost:3000` (e2e phase 10 kiểm preflight từ origin này) |
| `GW_DRAIN_DELAY`, `GW_SHUTDOWN_BUDGET` | `2s` / `15s` | Dừng: `/readyz` false, chờ drain, chờ lệnh đang chạy; budget phải lớn hơn `GW_DRAIN_DELAY + CORE_REQUEST_DEADLINE`; compose `stop_grace_period` 18s |
| `GW_READ_HEADER_TIMEOUT`, `GW_READ_TIMEOUT` | `5s` / `10s` | Hạn đọc header và cả request |
| `GW_WRITE_TIMEOUT` | `CORE_REQUEST_DEADLINE + 5s` | Hạn ghi trả lời (không nhỏ hơn mặc định) |
| `GW_IDLE_TIMEOUT` | `60s` | Giữ kết nối rảnh |
| `GW_MAX_HEADER_BYTES` | `16384` | Cỡ header tối đa; header `Authorization` dài quá 8KB bị từ chối |
| `GW_H2_MAX_STREAMS` | `250` | Số stream HTTP/2 đồng thời mỗi kết nối |
| `CORE_REQUEST_DEADLINE` | `5s` | Hạn một lần gọi core |

## Cổng và admin

- HTTP `GW_ADDR` (`:8080`); compose publish `gateway-1` ở `127.0.0.1:8082` (`GW1_PORT`), `GW_CORS_ORIGINS=http://localhost:3000`, cả hai core.
- Admin `GW_ADMIN_ADDR` (`:9090`, không publish): `/livez`, `/readyz`, `/metrics` (`chatim_gateway_*`, bảng ở [module 11 §6.1](../modules/11-feature-https-gateway.md)). Alert `ChatimGatewayCoreUnavailable` (> 5% `Unavailable`).
- Log: `make gw-logs`. Image: `make image TARGET=apps/gateway` (`chatim/gateway:dev`).

## Gọi thử

Token dev như websocket ([websocket.md](websocket.md#khoá-và-token-dev)); không in giá trị `.env`:

    curl -H "Authorization: Bearer $(make -s ws-token ARGS='-tenant e2e -user alice')" -H 'Content-Type: application/json' -d '{}' http://127.0.0.1:8082/chatim.v1.CoreService/ListMyRooms

Không token → 401 `unauthenticated`; lệnh realtime (vd `SendMessage`) hoặc nội bộ (`ListMyRoomIDs`, `SetUserGroups`, `SetGroups`) → `unimplemented`. Lỗi của middleware là một HTTP status kèm body JSON của Connect.
