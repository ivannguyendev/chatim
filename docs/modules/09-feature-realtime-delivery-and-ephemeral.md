# Module 09: Thời gian thực — Phân phối Realtime & Tín hiệu tức thời (Typing/Presence)

> **Trạng thái:** App `websocket` (M4) **đã xây phần 1/5** trên `feat/m4` (2026-10-21): kết nối, token, chuyển lệnh tới core. Giao thức thật ở [§7](#7-giao-thức-kết-nối-m4-phần-1-đã-xây); quyết định D183–D188. Phần core đã có: event trên `CHATIM_EVT` và RePublish sang NATS core `live.{t}.{loại dữ liệu}.{rid}.evt.{kind}` (D108), hợp đồng consumer D156 (§3.5). Đẩy event xuống máy (phần 2), đã đọc/đã xem (phần 3), typing/online (phần 4), reconnect và vận hành (phần 5) vẫn là thiết kế dự kiến; chỗ nào khác §7 thì §7 đúng.
>
> **Mục tiêu module:** Đẩy các thông điệp và biến động dữ liệu đến người dùng đang online với độ trễ siêu thấp (p99 ≤100ms) qua kết nối WebSocket hai chiều, hỗ trợ các tương tác sinh động tức thời như "Đang gõ..." (Typing Indicator) và trạng thái trực tuyến (Presence) mà không làm suy giảm hiệu năng cơ sở dữ liệu.

---

## 1. Các Use Case nghiệp vụ (Business Use Cases)

| Mã Use Case | Tên Use Case | Tác nhân | Mô tả & Kết quả kỳ vọng |
|---|---|---|---|
| **UC-RT-01** | **Nhận tin nhắn & biến động tức thì (Realtime Push)** | Người dùng online | Khi có ai đó gửi tin nhắn, sửa tin, thả reaction hay đổi tên nhóm, giao diện của tôi lập tức cập nhật theo thời gian thực (độ trễ ≤100ms) mà không cần phải bấm tải lại. |
| **UC-RT-02** | **Hiển thị "Đang nhập tin nhắn..." (Typing Indicator)** | Người gửi | Khi tôi bắt đầu gõ bàn phím, những người khác đang mở phòng chat đó sẽ nhìn thấy thông báo *"Ivan Nguyen đang nhập tin..."*. Khi tôi dừng gõ vài giây, thông báo tự động biến mất. |
| **UC-RT-03** | **Trạng thái Trực tuyến (Presence)** | Hệ thống | Hiển thị dấu chấm xanh báo hiệu bạn bè đang Online, Vắng mặt (Away) hoặc Ngoại tuyến (Offline). |
| **UC-RT-04** | **Tin nhắn hệ thống tự động (System Messages)** | Hệ thống | Khi có các biến cố như thêm thành viên, đổi chủ nhóm hay ghim tin nhắn, một tin nhắn thông báo dạng hệ thống sẽ tự động xuất hiện trên timeline của mọi người. |

---

## 2. Luồng hoạt động chi tiết (How It Works)

### 2.1. Kiến trúc Cổng App websocket & Phân phối qua NATS Interest Routing

Hệ thống App websocket đóng vai trò làm lá chắn kết nối tập trung (Connection Hub) cho hàng trăm nghìn Client:

```mermaid
flowchart TD
    ClientA["Client A (Người gửi)"]
    ClientB["Client B (Đang mở phòng 101)"]
    ClientC["Client C (Không mở phòng 101)"]

    subgraph WebsocketLayer["Cụm App websocket (WebSocket gws)"]
        GW1["App websocket Node 1<br/>(Giữ kết nối Client A & B)"]
        GW2["App websocket Node 2<br/>(Giữ kết nối Client C)"]
    end

    subgraph EventBus["Hạ tầng NATS JetStream"]
        NATSLive["NATS Core Pub/Sub (RePublish từ CHATIM_EVT)<br/>Subject: live.tenant1.*.101.>"]
        NATSEph["NATS Ephemeral Bus (dự kiến)<br/>Subject: live.tenant1.eph.101.>"]
    end

    subgraph CoreLayer["Cụm Core Service"]
        CoreSrv["Core Service (Lưu trữ MongoDB)"]
    end

    %% Luồng gửi tin chính thức
    ClientA -- "1. Gửi tin (WS Frame)" --> GW1
    GW1 -- "2. gRPC SendMessage" --> CoreSrv
    CoreSrv -- "3. Commit Mongo & Publish" --> NATSLive

    %% Phân phối Realtime theo mối quan tâm
    NATSLive -- "4. Phân phối theo phòng" --> GW1
    GW1 -- "5. Push tin nhắn tức thời" --> ClientB

    %% Luồng gõ bàn phím (Bỏ qua DB hoàn toàn)
    ClientA -. "Gõ bàn phím (Typing)" .-> GW1
    GW1 -. "Bypass Core: Publish trực tiếp" .-> NATSEph
    NATSEph -. "Broadcast tức thì" .-> GW1
    GW1 -. "Hiển thị 'Đang gõ...'" .-> ClientB

    %% Client C không đăng ký phòng 101 nên không nhận
    NATSLive -. "Không chuyển vì không có Interest" .x GW2
```

### 2.2. Vòng đời Kết nối WebSocket & Cơ chế Xử lý Mã lỗi (Connection Lifecycle & Close Codes)

Để đảm bảo hàng trăm nghìn kết nối đồng thời hoạt động ổn định và tự phục hồi khi có sự cố, Client SDK vận hành theo máy trạng thái kết nối có tích hợp xử lý mã đóng (Close Codes):

```mermaid
stateDiagram-v2
    [*] --> Disconnected: Khởi động Client SDK

    Disconnected --> Connecting: Bắt đầu bắt tay WebSocket (WS Handshake)
    Connecting --> Authenticating: Gửi frame auth (JWT) đầu tiên, trong 5s

    state Authenticating {
        [*] --> ValidateJWT: App websocket kiểm chữ ký bằng file JWKS nạp lúc khởi động
        ValidateJWT --> AuthFailed: Token hết hạn / Không hợp lệ
        ValidateJWT --> AuthSuccess: Xác thực thành công
    }

    AuthFailed --> Disconnected: Đóng mã 4001 (token sai hoặc hết hạn)<br/>Client lấy token mới rồi mới nối lại
    Authenticating --> Disconnected: Đóng mã 4002 (không gửi auth kịp 5s)<br/>Client nối lại bình thường

    AuthSuccess --> Connected: Kênh kết nối sẵn sàng (Protobuf Stream)

    state Connected {
        [*] --> ActiveStreaming: Lắng nghe Interest các phòng đang mở
        ActiveStreaming --> EphemeralBypass: Gửi / nhận Typing & Presence
        ActiveStreaming --> BufferCongested: Mạng Client chậm, Socket Outbound Buffer đầy
    }

    BufferCongested --> Dropped: Phần 1: cắt socket không gửi mã (máy thấy 1006)<br/>Phần 2 dự kiến mã 4008 (SLOW_CLIENT)
    Connected --> ServerMaintenance: Server đóng mã 1001 (node đang tắt)
    Connected --> NetworkDrop: 15s không có frame nào (heartbeat, lệnh hay ping), server đóng 1000

    Dropped --> Reconnecting: Exponential Backoff + Full Jitter
    ServerMaintenance --> Reconnecting: Tự động chuyển node Gateway khác
    NetworkDrop --> Reconnecting: Chờ phục hồi mạng

    Reconnecting --> Connecting: Kết nối lại sau thời gian chờ T_wait
```

---

## 3. Cơ chế bảo đảm tính đúng đắn (Guarantees & Mechanisms)

### 3.1. Định tuyến theo mối quan tâm động (Dynamic Interest-Based Routing D108)
- **Vấn đề cần giải quyết:** Nếu mỗi tin nhắn đều phát tán (Broadcast) bừa bãi cho toàn bộ 100,000 kết nối WebSocket, server App websocket sẽ cạn kiệt băng thông mạng trong vài giây (Broadcast Storm).
- **Cơ chế bảo đảm:**
  - Client chỉ nhận sự kiện của **những phòng chat mà người đó đang thực sự mở xem trên màn hình**.
  - Khi người dùng bấm vào xem Phòng 101, App websocket đăng ký lắng nghe (Subscribe) chủ đề trên NATS:
    $$\text{live}.\{\text{tenant\_id}\}.*.\{\text{room\_101}\}.>$$
  - Khi người dùng thoát khỏi phòng hoặc chuyển sang phòng khác, App websocket lập tức huỷ đăng ký (Unsubscribe). Nhờ đó, lưu lượng mạng phân phối luôn được tối ưu hoá chính xác theo nhu cầu thực tế.

### 3.2. Đường truyền tắt cho Tín hiệu tạm thời (Ephemeral Bypass — Tiết kiệm 100% DB IOPS)
- **Vấn đề:** Các tín hiệu như "Đang gõ tin" (Typing) hay "Nhịp tim trực tuyến" (Presence Ping) diễn ra liên tục hàng nghìn lần mỗi giây nhưng không có giá trị lưu trữ lâu dài. Nếu ghi các tín hiệu này vào MongoDB, cơ sở dữ liệu sẽ sập ngay lập tức vì quá tải ghi đĩa.
- **Cơ chế bảo đảm:**
  - Toàn bộ gói tin Ephemeral được gán chủ đề riêng (dự kiến): `live.*.eph.*`.
  - App websocket nhận gói tin từ Client sẽ **bỏ qua hoàn toàn dịch vụ Core và MongoDB**, đẩy thẳng trực tiếp vào kênh phân phối siêu tốc của NATS.
  - Các tín hiệu này chỉ sống trong vài giây trong bộ nhớ RAM và tự động tiêu biến. Nếu người dùng rớt mạng, tín hiệu mất đi một cách tự nhiên mà không để lại bất kỳ rác dữ liệu nào trong cơ sở dữ liệu.

### 3.3. Kỹ thuật Phân tán Không cấp phát bộ nhớ (Zero-Allocation Fanout)
- Trong một phòng chat lớn có 5,000 người đang online cùng lúc:
  - Khi App websocket nhận được một sự kiện tin nhắn từ NATS, App websocket thực hiện **mã hoá nhị phân định dạng Protobuf đúng 1 lần duy nhất** vào một bộ đệm dùng chung (Shared Buffer Pool).
  - Sau đó, con trỏ của bộ đệm này được gửi trực tiếp vào các kết nối socket của 5,000 người dùng mà không tạo thêm 5,000 bản sao chuỗi trong bộ nhớ RAM. Kỹ thuật này giúp App websocket đạt hiệu năng cực đại với mức tiêu thụ RAM tối thiểu.

### 3.4. Sinh tin nhắn hệ thống tất định (Deterministic SysMsg CID, D132)
- Khi có các sự kiện hệ thống (như Ghim tin, Thêm thành viên):
  - Một app SysMsg ngoài core (chưa xây) lắng nghe sự kiện từ NATS và gọi `SendMessage(kind = system, skip_unread, meta)` với mã định danh tất định:
    $$\text{cid} = \text{"sys-"}\{\text{event\_id}\}$$
  - Core chỉ cung cấp đường gửi: tin `system` bỏ kiểm membership, không cho link hay mention (D132).
  - Nhờ cơ chế chống trùng CID (Module 02), ngay cả khi có 2 node cùng xử lý sự kiện này trong cửa sổ `CID_COMMITTED_TTL` (15 phút), chỉ có đúng 1 tin nhắn hệ thống được ghi vào phòng chat.

### 3.5. Hợp đồng chống trùng và lấp lỗ (D156, bắt buộc cho websocket M4)
- Từ M3.1 event được giao **ít nhất một lần** (cờ `_state` + sweeper, Module 10) và bản phát bù có thể tới **sau** event mới hơn. Ví dụ: client nhận tin 46 trước, vài giây sau sweeper phát bù tin 45.
- Bỏ trùng theo id event; với event đổi trạng thái, so version của doc (`ver`, `read_ver`, `pin_ver`, `info_ver`, `preference_ver`, `rx.v`/`rc.v`, `member_count_ver`): bản lớn hơn thắng.
- `msg_created`: đã có đúng seq đó thì bỏ; thấy lỗ (seq > seq cuối + 1) thì gọi `GetHistory` để lấp. **Không bao giờ** bỏ theo luật "seq ≤ seq cuối đã thấy", vì như vậy tin 45 ở ví dụ trên sẽ mất.
- Chi tiết: [kiến trúc §8.1](../architecture.md).

---

## 4. Kỹ thuật & Công nghệ triển khai (Technologies)

### 4.1. Thư viện Go WebSocket `gws` hiệu năng cao
- App websocket sử dụng thư viện `github.com/lxzan/gws` được viết riêng cho Go để tối ưu hoá khả năng chịu tải cao:
  - Hỗ trợ quản lý hàng trăm nghìn kết nối đồng thời với lượng goroutine tối thiểu.
  - Hỗ trợ nén `permessage-deflate`, nhưng **tắt ở phần 1** (D185): nén tốn CPU khi có 100K kết nối; bật hay không quyết ở phần 5 khi có số đo.
  - Quản lý bộ đệm theo cơ chế tái sử dụng `sync.Pool`, triệt tiêu áp lực dọn rác của Go Garbage Collector (GC Pauses).

### 4.2. Định dạng Gói tin Protobuf Nhị phân
- Toàn bộ dữ liệu trao đổi giữa Client và App websocket qua WebSocket đều được đóng gói dưới dạng nhị phân **Protocol Buffers (Protobuf)** thay vì văn bản JSON.
- Giúp giảm từ **40% – 60% kích thước gói tin** truyền trên mạng di động 4G/5G, tiết kiệm pin cho thiết bị và tăng tốc độ giải mã.
- **Không có kênh JSON** (D185 bỏ phần "JSON debug" của D17): frame chữ bị đóng 1003; debug bằng `corecli` hoặc `pkg/wsclient`.

### 4.3. Hợp đồng Mã lỗi Kết nối & WebSocket Close Codes

Bảng mã đóng đầy đủ của phần 1 nằm ở [§7.4](#74-mã-đóng). Tóm tắt hành vi Client SDK:

| Mã | Ý nghĩa | Hành vi Client SDK |
|---|---|---|
| `4001` | Token sai, hết hạn, hoặc `auth` gia hạn khác người/tenant | Không nối lại ngay; lấy token mới rồi mới kết nối lại |
| `4002` | Không gửi `auth` trong 5 giây | Nối lại bình thường, không cần đổi token |
| `1001` | Node websocket đang tắt | Nối lại (qua LB sang node khác, phần 5) |
| `4008` | Dự kiến phần 2: máy nhận event quá chậm | Chưa dùng ở phần 1 |

---

## 5. Hạn chế kỹ thuật & Đánh đổi (Limitations & Trade-offs)

1. **Ứng xử với Khách hàng Mạng chậm (Slow Client Handling):**
   - *Hạn chế:* Nếu kết nối internet của người dùng quá yếu, hàng đợi gửi gói tin (Socket Outbound Buffer) trên App websocket bị đầy:
   - *Đánh đổi:* Để bảo vệ App websocket không bị cạn kiệt RAM, App websocket sẽ chủ động **đóng kết nối (Force Disconnect)** của client đó. Phần 1: hàng đợi gửi đầy (`WS_SEND_QUEUE` 256 frame) hoặc một lần ghi quá `WS_WRITE_TIMEOUT` (10s) thì cắt thẳng socket, không gửi close frame (máy thấy 1006). Khi mạng ổn định, Client sẽ kết nối lại và kích hoạt quy trình đồng bộ trạng thái (Module 08).
2. **Không bảo đảm sống sót của Ephemeral:**
   - Các tín hiệu Typing và Presence không có cơ chế gửi lại khi mất gói tin (Best-effort unacknowledged). Nếu rớt mạng, client chỉ đơn giản là không thấy báo đang gõ.
3. **Chế độ Xoá chặt cho Tenant đặc thù (Privacy Strict Mode):**
   - Chưa có: event core phát luôn mang snapshot hiện tại kèm nội dung văn bản; chưa có cấu hình tenant để event chỉ chứa metadata và ID.

---

## 6. Phụ thuộc kỹ thuật & Bán kính sự cố (Dependencies & Blast Radius)

| Thành phần phụ thuộc | Mục đích phụ thuộc | Ứng xử khi thành phần gặp sự cố (Failure Behavior) |
|---|---|---|
| **Cụm NATS Core Pub/Sub** | Truyền tải dữ liệu thời gian thực | Nếu NATS cluster bị nghẽn: Người dùng vẫn gửi được tin (do Core ghi Mongo thành công), nhưng tin nhắn mới đến các người nhận khác sẽ bị trễ; event chưa vào JetStream được sweeper slot 0 phát bù khi NATS sống lại (Module 10). |
| **File JWKS (khoá công khai)** | Xác thực JWT khi mở kết nối và khi gia hạn | Phần 1: file JWKS nạp một lần lúc khởi động (`WS_JWT_KEYS_FILE`), không gọi dịch vụ nào lúc kiểm token; file sai thì node không khởi động. Xoay khoá bằng rolling restart (D183). `identity` Phase 2 sẽ phát JWKS. |
| **Redis state** | Đọc bảng slot để chọn core (chỉ đọc) | Redis chết: node giữ bảng slot đang có và mọi kết nối gRPC tới core; lần nạp không thấy core nào cũng không đóng kết nối (D186). Lúc khởi động node chờ bảng slot rồi mới bật `/readyz`. |
| **Core Service (gRPC Unary)** | Xử lý mọi lệnh của máy | App websocket gọi Core qua gRPC theo bảng slot, hạn `CORE_REQUEST_DEADLINE` (5s), **không tự gửi lại** (luật không retry nội bộ). Core lỗi `UNAVAILABLE` thì app trả nguyên mã cho máy và kích làm mới bảng slot (gom, ≥ 250ms một lần); máy gửi lại cùng `cid`/`request_id` và lần sau đi đúng core mới. |

### 6.1. Chỉ số Sức khoẻ Vận hành & Cảnh báo SRE (Key Metrics & Alerts)

> **Đã có từ phần 1** (trên `/metrics` cổng admin, tiền tố `chatim_websocket_`): `connections`, `unauthenticated_connections` (gauge), `auth_failures_total{reason}`, `closes_total{code}` (chỉ mã do server đóng; nhãn 1000 gồm cả hết hạn heartbeat), `commands_rejected_total{reason=rate|inflight|unimplemented|shutdown}`, `unknown_frames_total`, `commands_total{command,code}`, histogram `core_call_seconds{command}`. Chưa có luật alert nào cho websocket.
>
> **Dự kiến, chưa xây:** các metric và alert dưới đây là thiết kế cho phần 2–5; chưa có trong code và chưa có trong `deploy/prometheus/alerts.yml`.

- **`chatim_websocket_connected_clients`:** Tổng số lượng kết nối WebSocket đang hoạt động đồng thời (CCU).
- **`chatim_realtime_delivery_latency_seconds`:** Độ trễ từ lúc tin được ghi vào DB đến khi Client nhận được frame WebSocket.
  - *Ngưỡng SLA:* p99 ≤ 100ms.
  - *Luật cảnh báo dự kiến (`ChatimDeliveryLatencyHigh`, chưa có):* Cảnh báo nếu p99 > 100ms trong 3 phút liên tục.
- **`chatim_websocket_slow_clients_dropped_total`:** Số lượng kết nối client bị App websocket ngắt cưỡng bức do nghẽn buffer.

---

## 7. Giao thức kết nối (M4 phần 1, đã xây)

> Xây trên `feat/m4` (commit `4363410`…`ae11be4`), theo [tóm tắt phần 1](../plans/2026-10-20-m4-1-ket-noi-va-lenh-summary.md); quyết định D183–D188 ở [decision log §17.7](../decision-log.md). Code: `apps/websocket` (app), `pkg/jwtverify` (kiểm token), `pkg/coreroute` (chọn core, không retry), `pkg/wsclient` (client Go mẫu), `proto/chatim/v1/ws.proto` (frame).

### 7.1. Mở kết nối

- Máy gọi `GET /ws` (cổng `WS_ADDR`, mặc định `:8080`; dev: `ws://127.0.0.1:8081/ws`) với subprotocol bắt buộc **`chatim.v1`**. Thiếu subprotocol → HTTP 400.
- Header HTTP phải tới trong 5 giây và ≤ 16KB (chặn máy gửi header chậm).
- Số kết nối chưa đưa token mỗi node ≤ `WS_UNAUTH_MAX` (10.000), giữ chỗ trước rồi mới kiểm; vượt → HTTP 503. Node đang tắt cũng trả 503.
- Cổng admin `WS_ADMIN_ADDR` (`:9090`, không publish ra ngoài): `/livez`, `/readyz` (chỉ true khi đã có bảng slot), `/metrics`.

### 7.2. Frame

- Mọi frame là **nhị phân protobuf** (D185): máy gửi `ClientFrame`, app gửi `ServerFrame`. Frame chữ → đóng 1003. Không có kênh JSON, chưa bật nén.
- `ClientFrame` (oneof): `auth{token}` (10), `heartbeat{}` (11), `request{id, oneof request}` (12).
- `ServerFrame` (oneof): `response{id, code, message, oneof result}` (10).
- Số field 10–19 dành cho phần 1, 20–39 cho phần 2–4 (event, vị trí đọc, typing, online, resync); chỉ thêm, không đổi số cũ.
- Sau khi đã auth, frame loại app chưa biết (máy mới hơn app) bị bỏ qua và đếm ở `unknown_frames_total`, không đóng kết nối. Frame không giải mã được → đóng 1008.

### 7.3. Token, gia hạn, heartbeat

1. Frame đầu tiên phải là `auth{token}`, trong `WS_AUTH_TIMEOUT` (5s); không kịp → đóng **4002**. Gửi frame khác trước khi auth → đóng 1008.
2. Token là JWT `EdDSA` (Ed25519), khoá chọn theo `kid` trong file JWKS; kiểm `iss`, `aud`, `sub` (user), `tid` (tenant), `exp`, `nbf` (lệch giờ 30s). Sai → đóng **4001** (D183). Máy chỉ thấy mã; lý do vào log và metric, token không bao giờ vào log.
3. **Gia hạn trong kết nối:** trước khi hết hạn, máy gửi lại `auth` với token mới, cùng `sub` và `tid` (khác → 4001). Không gia hạn thì tới `exp + 30s` app đóng 4001.
4. **Heartbeat:** máy gửi `heartbeat` mỗi 5–10 giây (`pkg/wsclient` chọn ngẫu nhiên 5–8 giây để còn ≥ 7 giây dư). Mọi frame của máy đều tính là còn sống: heartbeat, lệnh, `auth`, và cả WebSocket ping (app trả pong). 15 giây (`WS_HEARTBEAT_TIMEOUT`) không có frame nào → app đóng (mã 1000).

Ví dụ: token của Lan hết hạn 11:00. Lúc 10:55 SDK lấy token mới và gửi `auth`; kết nối chạy tiếp. Nếu SDK quên, 11:00:30 kết nối bị đóng 4001 và SDK phải lấy token mới trước khi nối lại.

### 7.4. Mã đóng

| Mã | Khi nào | Máy làm gì |
|---|---|---|
| `4001` | Token sai, hết hạn, hoặc `auth` gia hạn khác người/tenant | Lấy token mới rồi mới nối lại |
| `4002` | Không gửi `auth` trong 5 giây | Nối lại bình thường, không đổi token |
| `1008` | Vi phạm giao thức: frame khác `auth` khi chưa auth, frame không giải mã được, `auth`/`heartbeat`/ping vượt giới hạn tốc độ | Sửa client; nối lại có backoff |
| `1003` | Gửi frame chữ thay vì nhị phân | Sửa client |
| `1009` | Frame lớn hơn `WS_FRAME_MAX` (128KB) | Sửa client (chia nhỏ lệnh) |
| `1001` | Node đang tắt | Nối lại (sang node khác qua LB, phần 5) |
| `1011` | Lỗi lạ (panic) khi xử lý kết nối này; chỉ kết nối này bị đóng, node vẫn chạy | Nối lại có backoff |
| `1000` | 15 giây không có frame nào | Nối lại; kiểm lại nhịp heartbeat |
| `1006` (máy tự thấy) | App cắt socket không gửi close frame: hàng đợi gửi đầy hoặc một lần ghi quá 10 giây | Nối lại có backoff |
| `4008` | Dành cho phần 2 (máy nhận event quá chậm) | Chưa dùng |
| `1013` | Dành cho phần 5 (node quá tải) | Chưa dùng |

### 7.5. Lệnh và câu trả lời

- Máy gửi `request{id, <một lệnh>}`; app trả `response{id, code, message, result}` với cùng `id`. `id` do máy đặt; app không kiểm trùng. `code` là mã gRPC (0 = OK). Result số N đi với request số N.
- **Danh sách cho phép 33 lệnh** (D184): room (`CreateRoom`, `OpenDirectRoom`, `ListMyRooms`, `GetRooms`, `UpdateRoom`, `SetRoomPreferences`), tin (`SendMessage`, `GetHistory`, `GetMessages`, `EditMessage`, `DeleteMessage`, `HideMessage`, `ClearHistory`, `GetEditHistory`), react/ghim/thread (`ReactMessage`, `PinMessage`, `UnpinMessage`, `ListPins`, `GetReactionSettings`, `GetReactions`, `SetBookmark`, `GetReplies`, `ListThreads`, `ListBookmarks`), thành viên (`AddMembers`, `RemoveMember`, `LeaveRoom`, `ChangeMemberRole`, `SetMemberPriority`, `ListMembers`, `MarkUnread`), mention (`ListMentions`, `ListRoomGroups`). Không có: `MarkRead` (phần 3, frame riêng), `SetUserGroups`, `SetGroups` (hệ thống user/nhóm gọi). `ListMentions` vẫn nhận `groups` máy gửi (D188).
- Field app không biết bị bỏ ở mọi tầng, không bao giờ tới core. `SendMessage` chỉ nhận `kind` mặc định hoặc `TEXT`; `kind` khác, `skip_unread` hay `meta` → `INVALID_ARGUMENT`. Lệnh rỗng (máy mới hơn app) → `UNIMPLEMENTED`, không đóng kết nối.
- Tenant và user gửi tới core (`x-chatim-tenant`, `x-chatim-user`) **chỉ lấy từ token**.
- Chọn core (D186): lệnh có `room_id` → core đang giữ slot của room (`room_id` sai dạng → `INVALID_ARGUMENT` "invalid room_id"); lệnh không thuộc room (`CreateRoom`, `OpenDirectRoom`, `ListMyRooms`, `GetRooms`, `GetReactionSettings`, `ListBookmarks`, `ListMentions`) → core bất kỳ. Không có core sống → `UNAVAILABLE`.
- Một lần gọi, hạn `CORE_REQUEST_DEADLINE` (5s). **App không tự gửi lại**; gặp `UNAVAILABLE` thì kích làm mới bảng slot rồi trả lỗi; máy gửi lại cùng `cid`/`request_id`.
- **Lời lỗi** (D184): giữ lời của core cho `INVALID_ARGUMENT`, `NOT_FOUND`, `FAILED_PRECONDITION`, `PERMISSION_DENIED`, `ALREADY_EXISTS`, `RESOURCE_EXHAUSTED`; câu cố định cho `UNAVAILABLE` ("core unavailable, retry later"), `DEADLINE_EXCEEDED` ("core did not answer in time"), `CANCELED` ("request canceled"), `UNIMPLEMENTED` ("request not supported"); mã khác → "request failed". Lời gốc chỉ vào log.
- **Thứ tự** (D187): các `SendMessage` của cùng kết nối vào cùng room chạy lần lượt theo thứ tự nhận. Một lần gửi lỗi `UNAVAILABLE`, `DEADLINE_EXCEEDED` hoặc `CANCELED` thì các lần gửi đang chờ phía sau của room đó trả cùng lỗi ngay, không gọi core; máy gửi lại theo đúng thứ tự. Kết nối đóng → bỏ các lần đang chờ. Lệnh khác chạy song song.

Ví dụ: máy gửi `request{id 17, SendMessage{room "77", cid "c-9", text "Ok anh"}}` rồi ngay sau đó `request{id 18, SendMessage{room "77", cid "c-10", text "Mai gặp"}}`. App gọi core cho id 17 trước; nếu core trả `UNAVAILABLE`, id 18 nhận `UNAVAILABLE` luôn mà không gửi, máy gửi lại 17 rồi 18 với cùng cid nên không sinh tin trùng và không đảo thứ tự.

### 7.6. Giới hạn mỗi kết nối

| Giới hạn | Mặc định | Vượt thì |
|---|---|---|
| Tốc độ frame (`WS_COMMAND_RATE`, `WS_COMMAND_BURST`), tính cả `auth`, `heartbeat`, ping | 50/s, dồn 100 | Lệnh: `response` `RESOURCE_EXHAUSTED` "too many requests", không gọi core. `auth`/`heartbeat`/ping: đóng 1008 |
| Lệnh đang chạy + đang chờ (`WS_COMMAND_INFLIGHT`) | 32 | `RESOURCE_EXHAUSTED` "too many requests in flight" |
| Cỡ frame (`WS_FRAME_MAX`) | 128KB | Đóng 1009 |
| Hàng đợi gửi (`WS_SEND_QUEUE`), một goroutine ghi mỗi kết nối | 256 frame | Cắt socket (máy thấy 1006) |
| Hạn mỗi lần ghi (`WS_WRITE_TIMEOUT`) | 10s | Cắt socket (máy thấy 1006) |

Khi node tắt: `/readyz` false → chờ `WS_DRAIN_DELAY` (2s) → thôi nhận kết nối mới → lệnh mới nhận `UNAVAILABLE` "server shutting down, retry later", lệnh đang chạy vẫn được trả lời (trong `CORE_REQUEST_DEADLINE`) → đóng mọi kết nối 1001; cả quá trình trong `WS_SHUTDOWN_BUDGET` (15s).
