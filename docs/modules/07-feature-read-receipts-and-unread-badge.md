# Module 07: Vị trí đọc — Trạng thái đã đọc, Đánh dấu chưa đọc & Badge đếm

> **Mục tiêu module:** Theo dõi chính xác vị trí tin nhắn mà từng thành viên đã đọc trong phòng chat, đồng bộ trạng thái đọc tức thì trên mọi thiết bị cá nhân (Điện thoại, Máy tính, Web), và tính toán số lượng tin nhắn chưa đọc (Unread Badge) với hiệu năng cao mà không làm quá tải cơ sở dữ liệu.

---

## 1. Các Use Case nghiệp vụ (Business Use Cases)

| Mã Use Case | Tên Use Case | Tác nhân | Mô tả & Kết quả kỳ vọng |
|---|---|---|---|
| **UC-READ-01** | **Đánh dấu đã đọc (Mark Read)** | Người dùng | Khi người dùng mở xem phòng chat, client tự động gửi vị trí `read_seq` mới nhất. Huy hiệu chưa đọc của phòng đó biến mất trên giao diện. |
| **UC-READ-02** | **Đánh dấu chưa đọc (Mark as Unread)** | Người dùng | Người dùng chủ động đánh dấu một phòng chat là "Chưa đọc" để ghi nhớ cần xử lý sau. Huy hiệu chưa đọc xuất hiện trở lại bên cạnh tên phòng. |
| **UC-READ-03** | **Hiển thị số tin chưa đọc (Unread Badge)** | Hệ thống | Trên danh sách phòng chat, hiển thị số lượng tin nhắn mới mà tôi chưa đọc (ví dụ: `3`, `12`, hoặc `99+`). |
| **UC-READ-04** | **Đồng bộ đa thiết bị (Multi-Device Sync)** | Người dùng | Khi tôi đọc tin nhắn trên điện thoại, ứng dụng trên máy tính của tôi lập tức tắt huy hiệu chưa đọc mà không cần phải bấm làm mới. |

---

## 2. Luồng hoạt động chi tiết (How It Works)

### 2.1. Sơ đồ Thuật toán Đếm Chưa đọc Có chặn & Đồng bộ Đa thiết bị (Bounded Scan Flow)

Sơ đồ khối dưới đây trực quan hoá thuật toán tính toán số tin chưa đọc có ngưỡng chặn an toàn (đếm tới 100, quét tối đa 300 tin đuôi phòng) và luồng đồng bộ tức thời đa thiết bị:

```mermaid
flowchart TD
    subgraph UpdatePhase["1. Luồng Ghi nhận Đã đọc (MarkRead)"]
        UserAction["Người dùng lướt đọc tin đến vị trí seq"] --> CheckSeq{"seq mới > read_seq hiện tại?"}
        CheckSeq -- "Không (lướt ngược lên)" --> NoOp["Bỏ qua (Giữ nguyên read_seq)"]
        CheckSeq -- "Có (tiến về tương lai)" --> AtomicUpdate["FindOneAndUpdate MongoDB (members)<br/>filter: {_id, state: 1, read_seq < seq}<br/>set: {read_seq: seq, read_ver: read_ver + 1}<br/>kèm cờ _state bit 2 (read_updated chờ phát)"]
        AtomicUpdate --> FanoutNATS["Publish qua NATS: read_updated"]
        FanoutNATS --> SyncOtherDev["Đồng bộ tức thì: Xoá huy hiệu (badge) trên máy tính/tablet"]
    end

    subgraph CalcPhase["2. Thuật toán Đếm Unread Có Chặn (Bounded Scan)"]
        ReqBadge["Client mở danh sách hội thoại (ListMyRooms / GetRooms)"] --> QueryMongo["Đuôi phòng từ roomcache (≤ 300 tin gọn, TTL 30s)<br/>đếm từ mới về cũ: seq > read_seq, người khác gửi,<br/>chưa xoá, không skip_unread, không bị ẩn/clear"]
        QueryMongo --> BranchCount{"Dừng ở đâu?"}

        BranchCount -- "Gặp read_seq" --> ReturnExact["Trả về số chính xác (ví dụ: 15)<br/>approx = false -> Hiển thị '15'"]
        BranchCount -- "Đếm đủ 100" --> ReturnCap["Dừng ở 100<br/>Hiển thị '99+'"]
        BranchCount -- "Hết 300 tin đuôi mà chưa tới read_seq" --> ReturnApprox["approx = true: số thật ít nhất bằng số đã đếm"]
    end
```

### 2.2. Luồng Cập nhật Vị trí đọc & Thuật toán Đếm Unread có giới hạn (Sequence Diagram)

```mermaid
sequenceDiagram
    autonumber
    participant Client as Client SDK (Điện thoại)
    participant Core as Core Service
    participant Members as MongoDB (members)
    participant Msgs as MongoDB (messages)
    participant NATS as NATS JetStream
    participant WebClient as Client SDK (Máy tính)

    Client->>Core: MarkRead(room_id, seq=10042)

    Note over Core,Members: 1. Cập nhật vị trí đọc trên doc của chính tôi
    Core->>Members: FindOneAndUpdate(filter: {_id: room│user, state: 1, read_seq < 10042},<br/>update pipeline: read_seq = 10042, read_ver + 1, _state bit 2)
    Members-->>Core: Update thành công (read_ver tăng)

    Note over Core,NATS: 2. Phát tán đồng bộ sang các thiết bị khác của tôi
    Core->>NATS: Publish read_updated(room_id, user_id, read_seq=10042)
    NATS-->>WebClient: Realtime push: Xoá huy hiệu chưa đọc trên Web
    Note over Core,NATS: PubAck thì xoá bit 2 nếu read_ver còn bằng, rớt thì sweeper phát bù. Cờ đã đọc của mention không lưu: Mention App tính lúc đọc ListMentions từ read_seq (D165)

    Core-->>Client: Xác nhận thành công

    Note over Client,Msgs: 3. Khi mở lại danh sách phòng: Tính Unread lúc đọc (M3, R17)
    Client->>Core: ListMyRooms (hoặc GetRooms)
    Core->>Msgs: TailWindow: đuôi ≤ 300 tin gọn mỗi phòng (cache 30s, đối chiếu last_seq/last_edit_at)
    alt Gặp read_seq trước khi đếm đủ 100
        Core-->>Client: unread = 12, approx = false
    else Đếm đủ 100
        Core-->>Client: unread = 100 (Hiển thị 99+)
    else Hết 300 tin đuôi mà chưa gặp read_seq
        Core-->>Client: unread = số đã đếm, approx = true
    end
```

---

## 3. Cơ chế bảo đảm tính đúng đắn (Guarantees & Mechanisms)

### 3.1. Vị trí đọc nhẹ tải (Lightweight Read Receipt — Không khoá bảng tin)
- **Vấn đề cần giải quyết:** Trong một nhóm có 5,000 thành viên, nếu mỗi lần một người đọc tin đều cập nhật một danh sách `read_by_users` ngay trên document tin nhắn, document đó sẽ bị ghi đè liên tục hàng nghìn lần (Hot document lock), làm tê liệt toàn bộ hệ thống.
- **Cơ chế bảo đảm:**
  - Vị trí đọc được lưu **riêng biệt trên document thành viên của người đó** tại collection `members`:
    $$\text{members.read\_seq} = \text{Số seq cao nhất đã đọc}$$
  - Thao tác `MarkRead` chỉ là một lệnh cập nhật cực nhẹ trên đúng 1 document cá nhân:
    $$\text{Update: } \{\$set: \{\text{read\_seq}: S\}, \$inc: \{\text{read\_ver}: 1\}\} \text{ where } \text{read\_seq} < S$$
  - Thao tác này hoàn toàn độc lập với bảng `messages`, không sinh ra bất kỳ sự tranh chấp tài nguyên nào giữa các thành viên trong phòng.

### 3.2. Thuật toán Đếm Unread có chặn ngưỡng (Bounded Scan — Quy tắc R17 / D73)
- **Cơ chế tính toán On-Demand:** Hệ thống **cố tình không lưu sẵn biến đếm unread** trong cơ sở dữ liệu. Số tin chưa đọc được tính lúc đọc trong `ListMyRooms`/`GetRooms` (`domain.CountUnread`), từ đuôi phòng mà `api/roomcache` giữ (≤ 300 tin gọn mỗi phòng, TTL 30s, singleflight, đối chiếu `last_seq`/`last_edit_at`), không có RPC đếm unread riêng.
- **Quy tắc bảo đảm hiệu năng:**
  - Để tránh quét hàng nghìn tin đối với những người dùng lâu ngày không mở ứng dụng, phép đếm có hai chặn: `UnreadMax = 100` và cửa sổ `UnreadWindow = 300` tin:
    - Gặp `read_seq` trước: trả số chính xác, `approx = false`.
    - Đếm đủ 100: dừng và trả 100; giao diện hiển thị `99+`.
    - Hết 300 tin đuôi mà vẫn chưa tới `read_seq`: trả số đã đếm kèm `approx = true` (số thật ít nhất bằng chừng đó).
  - **Quy tắc đếm chuẩn nghiệp vụ:** Chỉ đếm các tin nhắn trên timeline chính:
    1. Do người khác gửi (`From != user`).
    2. Chưa bị xoá cho mọi người.
    3. Chưa bị người này ẩn (`seq` không nằm trong `hidden`) và gửi sau `cleared_at`.
    4. Không mang cờ `skip_unread` (`messages.nu`, ví dụ tin hệ thống im lặng, D132).

### 3.3. Đánh dấu chưa đọc linh hoạt (`MarkUnread`)
- Khi người dùng muốn đánh dấu một phòng là chưa đọc:
  - `MarkUnread(seq)` hạ `read_seq` về `seq − 1` (chỉ khi `read_seq` đang lớn hơn); `seq = 0` bị từ chối `INVALID_ARGUMENT`, `seq` vượt cuối phòng được kẹp về seq cuối.
  - Tăng số đếm phiên bản `read_ver = read_ver + 1` để đảm bảo event đồng bộ có số version lớn hơn, giúp các thiết bị khác luôn tôn trọng trạng thái mới nhất ("Version cao hơn luôn thắng").

---

## 4. Kỹ thuật & Công nghệ triển khai (Technologies)

### 4.1. Cấu trúc lưu trữ Vị trí đọc trên Collection `members`

```javascript
{
  "_id": BinData(0, "..."),            // Clustered Key: {room_id, user_id}
  "room_id": NumberLong("8492049201948201"),
  "user_id": "usr_1001",
  "read_seq": NumberLong(10042),       // Mốc tin nhắn đã đọc
  "read_ver": NumberLong(28),          // Version cập nhật vị trí đọc
  "last_change_at": ISODate("2026-10-10T10:25:00Z") // MarkRead/MarkUnread ghi mốc này, không đụng ver
}
```

### 4.2. Đuôi phòng trong RAM thay cho truy vấn đếm
- Unread không chạy `countDocuments` trên `messages`. `RoomTails.TailWindow` đọc đuôi phòng bằng một range scan trên clustered `_id` (room│0│seq) rồi `api/roomcache` giữ bản gọn (seq, người gửi, đã xoá, `skip_unread`, thời điểm gửi) trong RAM, giới hạn tổng theo `ROOM_TAIL_CACHE_BYTES`.
- Bản cache được dùng lại khi `last_seq` và `last_edit_at` của phòng chưa đổi; một trang danh sách hội thoại chỉ đọc lại đuôi của những phòng đã đổi (bench `roomlistbench`: trang đầu lạnh 25–31 truy vấn, ấm 2–8).

### 4.3. Hợp đồng Mã lỗi gRPC (Error Matrix)

| Mã lỗi gRPC | Tên lỗi domain | Tình huống kích hoạt | Hành vi Client SDK yêu cầu |
|---|---|---|---|
| `InvalidArgument` | `errUnreadFromZero` | `MarkUnread` với `seq = 0`. (`MarkRead` với `seq = 0` hoặc vượt cuối phòng không lỗi: được kẹp về seq cuối.) | Không retry; gửi seq của tin muốn đánh dấu chưa đọc. |
| `PermissionDenied` | `ERR_NOT_IN_ROOM` | Người gọi cố tình cập nhật vị trí đọc của phòng mình không tham gia. | Không retry; bỏ qua thao tác trên UI. |
| `Unavailable` | `ERR_DATABASE_BUSY` | MongoDB Replica Set đang tạm thời quá tải hoặc failover. | Có retry; Client tự động backoff và thử lại trong lần cuộn sau. |

---

## 5. Hạn chế kỹ thuật & Đánh đổi (Limitations & Trade-offs)

1. **Không hiển thị số chính xác khi chưa đọc quá 99 tin:**
   - *Hạn chế:* Người dùng không thể thấy con số chính xác như "Bạn có 4,812 tin chưa đọc" (chỉ thấy 99+).
   - *Lý do đánh đổi:* Tiết kiệm 99.9% tài nguyên quét đĩa của cơ sở dữ liệu. Đây là tiêu chuẩn thiết kế thực tế đã được chứng minh trên Slack, Telegram, WhatsApp và Discord.
2. **Không lưu sẵn biến đếm Unread vào DB (No Pre-computed Unread):**
   - *Hạn chế:* Khi người dùng có 100 phòng chat cùng lúc, Client cần gửi yêu cầu lấy unread cho các phòng đó lúc khởi động.
   - *Lý do đánh đổi:* Tránh hoàn toàn thảm hoạ **Write Amplification**: nếu lưu trước unread count, mỗi khi có 1 tin nhắn gửi vào nhóm 5,000 người, hệ thống sẽ phải thực thi 5,000 lệnh ghi đĩa để tăng số đếm cho 5,000 người.
3. **Huy hiệu ứng dụng ngoài màn hình chính (Push Badge):**
   - Số đếm trên icon ứng dụng ở màn hình điện thoại (APNs/FCM badge) chỉ mang tính chất xấp xỉ, do dịch vụ thông báo đẩy (Push Service) tự duy trì riêng biệt, không phản ánh thời gian thực 100%.

---

## 6. Phụ thuộc kỹ thuật & Bán kính sự cố (Dependencies & Blast Radius)

| Thành phần phụ thuộc | Mục đích phụ thuộc | Ứng xử khi thành phần gặp sự cố (Failure Behavior) |
|---|---|---|
| **Collection `members`** | Lưu trạng thái `read_seq` | Nếu ghi thất bại: Client sẽ thử lại trong lần cuộn màn hình tiếp theo. Người dùng chỉ bị hiện tượng tạm thời thấy phòng chưa được đánh dấu đã đọc. |
| **NATS JetStream** | Phát sự kiện `read_updated` đồng bộ đa thiết bị | Nếu NATS quá tải: Thiết bị hiện tại vẫn hiển thị đã đọc bình thường; doc `members` giữ cờ `_state` bit 2 và sweeper slot 0 phát bù `read_updated` của vị trí hiện tại khi NATS sống lại (Module 10), thiết bị thứ hai đồng bộ lúc đó hoặc khi mở lại ứng dụng. |
| **`api/roomcache` + `messages`** | Đuôi phòng cho phép đếm unread | Đọc đuôi phòng từ Mongo lỗi: `ListMyRooms`/`GetRooms` trả `UNAVAILABLE` (lỗi store), không có giá trị unread dự phòng. |

### 6.1. Chỉ số Sức khoẻ Vận hành & Cảnh báo SRE (Key Metrics & Alerts)

Chưa có metric đếm số lệnh `MarkRead`, thời gian tính unread hay tỷ lệ `approx`. Các metric thực có liên quan:

- **`chatim_core_room_cache_hits_total{cache="tail"}`**, **`chatim_core_room_cache_misses_total{cache="tail"}`:** Lượt đọc đuôi phòng từ RAM hay từ store (chỉ để xem tốc độ, không có alert).
- **`chatim_core_room_tail_cache_bytes`:** Dung lượng ước tính của cache đuôi phòng.
- **`chatim_core_event_pending_oldest_seconds{collection="members"}`:** Tuổi cờ chờ phát cũ nhất trên `members` (gồm `read_updated`).
  - *Luật cảnh báo (`ChatimEventPendingStuck`):* cờ cũ nhất > 60s trong 5 phút.
