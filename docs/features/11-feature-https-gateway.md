# Module 11: Cổng HTTPS — Tải dữ liệu & Quản lý room (App `gateway`)

> **Trạng thái:** đã xây trên `feat/m4` (M4 phần gateway, 2026-10-23), theo [tóm tắt](../plans/2026-10-23-m4-gateway-https-summary.md); quyết định D197–D198 ở [decision log §17.9–17.10](../decision-log.md). Code: `apps/gateway` (app), `pkg/chatrpc` (chuyển lệnh dùng chung với websocket), `chatrpc.Router` (sổ node chat trong Redis state, theo vòng), `pkg/pb/chatim/v1/chatimv1connect` (code Connect sinh từ proto).
>
> **Mục tiêu module:** Cho máy tải dữ liệu (danh sách hội thoại, lịch sử, thành viên...) và quản lý room (tạo nhóm, thêm người, đổi tên...) qua HTTPS bình thường, còn WebSocket ([module 09](./09-feature-realtime-delivery-and-ephemeral.md)) chỉ lo ghi realtime và đẩy event.

---

## 1. Các Use Case nghiệp vụ (Business Use Cases)

| Mã Use Case | Tên Use Case | Tác nhân | Mô tả & Kết quả kỳ vọng |
|---|---|---|---|
| **UC-GW-01** | **Mở app, thấy danh sách hội thoại** | Người dùng | Máy gọi `ListMyRooms` qua HTTPS (trước đó chờ trả lời mọi `MarkRead` đang gửi trên WebSocket để unread đúng, [Module 09 §11](./09-feature-realtime-delivery-and-ephemeral.md)); nhận danh sách đã xếp và đếm unread. |
| **UC-GW-02** | **Cuộn lịch sử, xem thành viên, bookmark, thread** | Người dùng | 14 lệnh đọc (`GetHistory`, `ListMembers`, `ListBookmarks`...) qua HTTPS, kết quả bằng đúng gọi gRPC thẳng chat. |
| **UC-GW-03** | **Tạo nhóm, thêm/xoá người, đổi tên, đổi quyền** | Chủ/admin nhóm | 8 lệnh quản lý qua HTTPS; người khác đang online nhận thay đổi realtime qua event trên WebSocket. |
| **UC-GW-04** | **App web gọi từ trình duyệt** | Frontend | CORS chỉ cho origin trong `GW_CORS_ORIGINS`; preflight không cần token. |

Ví dụ: Lan tạo nhóm "Kho HCM" có Bình bằng `POST /chatim.v1.ChatService/CreateRoom`; máy Bình đang nối WebSocket nhận `member_added` ngay, mở nhóm thì thấy `room_updated` khi Lan đổi tên.

---

## 2. Luồng hoạt động chi tiết (How It Works)

### 2.1. Chia lệnh giữa hai đường (D198)

| Đường | Lệnh |
|---|---|
| WebSocket, 12 lệnh ghi realtime (`chatrpc.Realtime()`) | `SendMessage`, `EditMessage`, `DeleteMessage`, `HideMessage`, `ReactMessage`, `PinMessage`, `UnpinMessage`, `SetBookmark`, `MarkRead`, `MarkUnread`, `ClearHistory`, `SetRoomPreferences` |
| HTTPS, 14 lệnh đọc (`chatrpc.Request()`) | `ListMyRooms`, `GetRooms`, `GetHistory`, `GetMessages`, `GetEditHistory`, `GetReplies`, `ListThreads`, `ListPins`, `GetReactions`, `GetReactionSettings`, `ListBookmarks`, `ListMembers`, `ListMentions`, `ListRoomGroups` |
| HTTPS, 8 lệnh quản lý (`chatrpc.Request()`) | `CreateRoom`, `OpenDirectRoom`, `UpdateRoom`, `AddMembers`, `RemoveMember`, `LeaveRoom`, `ChangeMemberRole`, `SetMemberPriority` |
| Không mở cho máy | `ListMyRoomIDs` (websocket gọi), `SetUserGroups`, `SetGroups` (app nội bộ) |

Test trong `pkg/chatrpc` kiểm hai tập không trùng và cộng lại đúng 34 field của `ClientRequest`. Gửi lệnh HTTPS qua WebSocket → `UNIMPLEMENTED` "request not supported"; gửi `SendMessage` qua gateway → `unimplemented`.

### 2.2. Một lệnh đi qua gateway

```mermaid
sequenceDiagram
  participant M as Máy
  participant G as gateway-1
  participant C as node chat bất kỳ
  M->>G: POST /chatim.v1.ChatService/GetHistory<br/>Authorization: Bearer token, JSON hoặc protobuf
  Note over G: CORS → trần toàn node → kiểm token<br/>→ giới hạn theo user → giới hạn body
  G->>C: gRPC GetHistory (round robin, tenant/user từ token)
  C-->>G: kết quả hoặc mã lỗi
  G-->>M: 200 + body, hoặc lỗi Connect
```

Chuỗi xử lý trong `apps/gateway/internal/api`, theo thứ tự:

1. **CORS:** origin có trong `GW_CORS_ORIGINS` thì trả `Access-Control-Allow-Origin` đúng origin đó, `Vary: Origin`; preflight (`OPTIONS` có `Access-Control-Request-Method`) trả 204 với `Allow-Methods: POST`, `Allow-Headers: Authorization, Content-Type, Connect-Protocol-Version, Connect-Timeout-Ms`, `Max-Age: 600`, không cần token; origin khác → 403 không header CORS. Không bao giờ có `Access-Control-Allow-Credentials`, không `*`.
2. **Trần toàn node:** quá `GW_INFLIGHT_MAX` (10.000) lệnh đang chạy → 503 `unavailable` "gateway busy, retry later", trước khi kiểm token.
3. **Kiểm token:** đúng một header `Authorization: Bearer <token>`, ký tự ASCII in được, ≤ 8KB; `pkg/auth` như websocket (`EdDSA`, `iss`, `aud`, hạn, `sub`/`tid`). Sai → 401 `unauthenticated`, đếm theo lý do, log tối đa 1 dòng/giây chỉ ghi lý do.
4. **Giới hạn theo (tenant, user) trên node:** 50 lệnh/giây, dồn 100, tối đa 32 lệnh đang chạy → 429 `resource_exhausted`.
5. **Body:** quá `APP_BODY_SIZE_MAX` (mặc định 128KB) → `resource_exhausted`; không nhận body nén.
6. **Handler Connect:** một handler cho mỗi lệnh của `chatrpc.Request()`, sinh từ descriptor, cùng một hàm chuyển: lấy claims, gọi `chatrpc.Forwarder.Forward` (chép field biết, tenant/user chỉ từ token, hạn `CHAT_REQUEST_DEADLINE`, một lần gọi). Đường khác → `unimplemented`, đếm `commands_total{command="unknown"}`.

### 2.3. Gọi chat

Gateway đọc sổ node chat sống trong Redis state (`chatrpc.OpenRouter`, như websocket; D261 thay D198 (3)), giữ một kết nối gRPC mỗi node và gửi mỗi lệnh tới node kế tiếp theo vòng (`Router.Any`), bỏ qua node có kết nối đang hỏng. Ví dụ: bật `chat-11`, nó tự ghi vào sổ, Redis báo thay đổi, gateway đọc lại sau vài giây và bắt đầu gửi lệnh tới `chat-11`; không sửa env, không khởi động lại. Không chọn theo slot: lệnh đọc đọc thẳng Mongo, lệnh quản lý không qua actor, nên node chat nào cũng đúng. Không retry của mình: node chat chết giữa lệnh → máy nhận `unavailable` sau đúng một lần gọi và gateway kích đọc lại sổ; lần sau chọn node khác. Redis state chết lúc đang chạy: giữ sổ và kết nối đang có. `/readyz` bật khi đã đọc sổ lần đầu; sổ rỗng lúc chạy không tắt `/readyz` (thay D198 (7), lý do như D186).

---

## 3. Cơ chế bảo đảm tính đúng đắn (Guarantees & Mechanisms)

- **Danh tính chỉ từ token:** header `x-chatim-*` máy gửi bị bỏ; metadata gửi chat được đặt lại từ claims.
- **Field lạ không tới chat:** JSON đọc bằng protojson `DiscardUnknown`, protobuf đi qua `chatrpc.CopyKnown`.
- **Lỗi một-một:** mã gRPC của chat → `connect.Code` cùng số; giữ lời chat cho `invalid_argument`, `not_found`, `failed_precondition`, `permission_denied`, `already_exists`, `resource_exhausted`; câu cố định cho lỗi hạ tầng (`unavailable` "chat unavailable, retry later", `deadline_exceeded`, `canceled`, `unimplemented` "request not supported"); không lộ địa chỉ chat.
- **Lỗi của tầng chặn** (401, 429, 503, 404 đường lạ) luôn là mã HTTP + thân JSON `{"code","message"}` kiểu Connect cho mọi giao thức, để client streaming/gRPC cũng thấy đúng mã.
- **Giải phóng chắc chắn:** handler panic hay máy huỷ giữa chừng vẫn trả chỗ của trần toàn node và của user (test).
- **Thay đổi tới người khác qua event:** gateway không phát event; chat phát như mọi lệnh, websocket đẩy xuống.

---

## 4. Kỹ thuật & Công nghệ triển khai (Technologies)

- **Connect** `connectrpc.com/connect` v1.21.0 (plugin `buf.build/connectrpc/go` v1.21.0 sinh `chatimv1connect`): `POST /chatim.v1.{ChatService,MentionService}/{Lệnh}`, JSON hoặc protobuf; client sinh sẵn cho web, iOS, Android từ cùng proto.
- **HTTP/1.1 và h2c** (`http.Server.Protocols`), TLS ở load balancer. Server: `ReadHeaderTimeout` 5s, `ReadTimeout` 10s, `WriteTimeout` ≥ `CHAT_REQUEST_DEADLINE` + 5s, `IdleTimeout` 60s, header ≤ 16KB, 250 stream HTTP/2 mỗi kết nối.
- **Kho giới hạn theo user** (`pkg/core/userlimit`): token bucket `golang.org/x/time/rate` + bộ đếm đang chạy, LRU `GW_USER_LIMITERS` (100.000) có khoá, chỉ đẩy người không có lệnh đang chạy.
- **Admin** (`pkg/core/admin`): `/livez`, `/readyz` (đã đọc sổ node chat, identity đã đồng bộ lần đầu và chưa tắt; sổ rỗng lúc chạy không tắt `/readyz`), `/metrics`, pprof; `/app probe` cho healthcheck.

---

## 5. Hạn chế kỹ thuật & Đánh đổi (Limitations & Trade-offs)

| Hạn chế | Đánh đổi |
|---|---|
| Không thu hồi token ở Phase 1 (owner 2026-10-23) | Token ngắn (≤ 15 phút); hết hạn → 401 ở lần gọi kế; thu hồi thuộc `identity` (M6 phần 2) |
| JWKS đọc một lần lúc khởi động | Đổi khoá = thêm khoá mới vào JWKS rồi rolling restart, như websocket |
| Giới hạn theo user là của từng node | N node thì một user tối đa N × 50 lệnh/giây; người bị đẩy khỏi LRU rồi quay lại được thêm tối đa một burst |
| Trần toàn node kiểm trước token | Lũ request không token vẫn chiếm chỗ tới trần; chặn lũ là việc của edge/load balancer; không giới hạn theo IP vì sau load balancer IP không tin được |
| Round robin không biết node chat nào đang chết tới khi kết nối báo hỏng | Một vài lệnh có thể nhận `unavailable`; máy gửi lại (lệnh quản lý có `request_id`) |
| Bộ đếm body quá lớn không đếm request giao thức gRPC khai kích thước quá lớn ở đầu tin | Request vẫn bị từ chối, chỉ thiếu số đếm |

---

## 6. Phụ thuộc kỹ thuật & Bán kính sự cố (Dependencies & Blast Radius)

- **Chat:** không node chat nào READY → `/readyz` 503, lệnh trả `unavailable`; một node chat chết → round robin sang node chat còn lại.
- **Không Redis, không NATS, không database:** gateway chết chỉ mất đường tải dữ liệu; WebSocket và ghi realtime vẫn chạy.
- **Tắt:** `/readyz` false → `GW_DRAIN_DELAY` → `http.Server.Shutdown` chờ lệnh đang chạy trong `GW_SHUTDOWN_BUDGET` (> drain + `CHAT_REQUEST_DEADLINE`) → đóng kết nối chat; compose `stop_grace_period` 18s.

### 6.1. Chỉ số vận hành (`chatim_gateway_*`)

| Chỉ số | Ý nghĩa |
|---|---|
| `commands_total{command,code}` | Lệnh đã chuyển theo mã kết quả; `command="unknown"` là đường lạ |
| `chat_call_seconds{command}` | Thời gian gọi chat |
| `auth_failures_total{reason}` | Token thiếu/sai/hết hạn theo lý do |
| `rate_limited_total{limit="rate"\|"inflight"}` | Bị giới hạn theo user |
| `inflight_rejected_total` | Bị trần toàn node từ chối |
| `body_too_large_total` | Body quá `APP_BODY_SIZE_MAX` |
| `inflight` | Lệnh đang chạy trên node |

Cấu hình đầy đủ (`GW_*`) và ví dụ `curl` với token dev: [README](../../README.md) mục "Biến môi trường `apps/gateway`".
