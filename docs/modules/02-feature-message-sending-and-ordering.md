# Module 02: Tin nhắn — Gửi tin & Thứ tự Timeline

> **Mục tiêu module:** Tiếp nhận tin nhắn từ người dùng, chống gửi trùng lặp tuyệt đối khi mạng chập chờn, cấp số thứ tự liên tục (monotonic sequence) để đảm bảo mọi người trong phòng thấy cùng một thứ tự hiển thị, và trả phản hồi cực nhanh (p99 ≤30ms).

---

## 1. Các Use Case nghiệp vụ (Business Use Cases)

| Mã Use Case | Tên Use Case | Tác nhân | Mô tả & Kết quả kỳ vọng |
|---|---|---|---|
| **UC-MSG-01** | **Gửi tin nhắn thông thường** | Thành viên | Gửi tin nhắn văn bản (kèm liên kết media, tệp đính kèm hoặc metadata). Tin nhắn được lưu trữ vĩnh viễn và phát tán tức thì tới những người đang online. |
| **UC-MSG-02** | **Đảm bảo thứ tự Timeline** | Tất cả thành viên | Tất cả mọi người trong phòng chat (kể cả người gửi và người nhận) đều thấy các tin nhắn sắp xếp theo **cùng một thứ tự duy nhất** trên màn hình. Không có hiện tượng tin nhắn nhảy lộn xộn. |
| **UC-MSG-03** | **Chống gửi trùng tin (Deduplication)** | Client SDK | Khi người dùng gửi tin nhưng mạng bị nghẽn (không nhận được ack phản hồi) và SDK tự động gửi lại nhiều lần với cùng một `cid` (Client Message ID), hệ thống chỉ ghi nhận và lưu **đúng 1 bản tin duy nhất**, không bao giờ sinh ra tin nhắn bị nhân đôi. |
| **UC-MSG-04** | **Phản hồi gửi tin cực nhanh (Fast Ack)** | Client SDK | Người gửi nhận được xác nhận (Ack thành công kèm `seq` và `message_id`) trong vòng ≤30ms (p99) ngay khi tin đã an toàn trong cơ sở dữ liệu. |

---

## 2. Luồng hoạt động chi tiết (How It Works)

### 2.1. Luồng xử lý Pipeline Gửi tin (Send Pipeline)

Pipeline gửi tin được tối ưu hoá theo mô hình gom nhóm (Batching) và phản hồi nhanh (Fast Ack):

```mermaid
sequenceDiagram
    autonumber
    participant Client as Client SDK
    participant GW as Gateway
    participant Core as Core (api/grpcsrv)
    participant Actor as Room Actor (send/actor)
    participant Dedupe as Redis Dedupe (EVALSHA)
    participant Flush as Flusher (send/flush)
    participant Mongo as MongoDB (messages)
    participant Pub as Publisher (event/publish)
    participant NATS as NATS JetStream

    Client->>GW: Gửi tin (room_id, cid="c-123", text="Xin chào")
    GW->>Core: gRPC SendMessage
    Core->>Actor: Đẩy vào hàng đợi Mailbox của Actor phòng

    Note over Actor,Dedupe: 1. Kiểm tra chống trùng (Reserve Phase)
    Actor->>Dedupe: Reserve(cid) qua Redis Lua (gom batch nhiều phòng)
    Dedupe-->>Actor: Hợp lệ (New CID)

    Note over Actor: 2. Cấp số thứ tự độc quyền: seq = last_seq + 1
    Actor->>Flush: Bàn giao Doc {room_id, seq, cid, text}

    Note over Flush,Mongo: 3. Ghi dữ liệu đồng loạt (Group Commit)
    Flush->>Mongo: insertMany(ordered: false, w: majority)
    Mongo-->>Flush: Ghi thành công

    Flush-->>Actor: Xác nhận đã ghi đĩa
    Actor-->>Core: Xác nhận hoàn tất
    Core-->>GW: Trả gRPC Response (seq, message_id)
    GW-->>Client: Trả Ack cho người gửi (p99 ≤ 30ms)

    Note over Actor,NATS: 4. Phát tán bất đồng bộ & Cam kết CID
    par Phát tán sự kiện (Best-effort)
        Actor->>Pub: Enqueue event msg_created
        Pub->>NATS: PublishMsgAsync(CHATIM_EVT)
    and Cam kết chống trùng (Commit Phase)
        Actor->>Dedupe: Commit(cid) (chuyển trạng thái sang Committed)
    end
```

---

## 3. Cơ chế bảo đảm tính đúng đắn (Guarantees & Mechanisms)

### 3.1. Single-Writer per Room (Mô hình Actor)
- **Vấn đề cần giải quyết:** Nếu nhiều người cùng bấm gửi tin vào 1 phòng chat tại cùng 1 mili-giây trên nhiều luồng khác nhau, làm sao cấp số thứ tự `seq` tăng dần liên tục mà không bị nhảy cóc (gap) hoặc tranh chấp khoá (lock contention)?
- **Cơ chế bảo đảm:**
  - Mỗi phòng chat đang hoạt động được quản lý bởi **duy nhất một Goroutine Actor** trong bộ nhớ của node Core nắm giữ Slot.
  - Mọi yêu cầu gửi tin vào phòng đó đều xếp hàng tuần tự qua một kênh Go channel (Mailbox).
  - Actor là người duy nhất nắm giữ biến `current_seq` trong RAM. Việc cấp `seq = current_seq + 1` diễn ra trong 0 microsecond mà không cần gọi câu lệnh khoá phân tán nặng nề nào.

### 3.2. Cơ chế chống trùng 3 tầng (3-Tier Deduplication)
- **Cơ chế:** Để đảm bảo tính Idempotency khi Client retry, hệ thống áp dụng bộ lọc 3 tầng:
  1. **Tầng 1 — In-Memory LRU Cache:** Actor lưu 1,000 `cid` gần nhất của phòng trong RAM để từ chối ngay lập tức các gói tin gửi lặp tức thì (độ trễ 0ms).
  2. **Tầng 2 — Redis Sharded Dedupe (Chu trình 2 pha Reserve-Commit):**
     - **Pha 1 (Reserve):** Đặt chỗ CID bằng Redis Lua Script với TTL 60 giây. Nếu CID đang ở trạng thái `Pending` (đang ghi dở), lệnh gửi sau sẽ chờ; nếu đã `Committed`, trả về kết quả cũ ngay lập tức.
     - **Pha 2 (Commit):** Sau khi MongoDB ghi thành công, CID được chuyển sang trạng thái `Committed` với TTL 24 giờ.
  3. **Tầng 3 — MongoDB Unique Index:** Chỉ mục unique kép `{room_id: 1, cid: 1}` tại cơ sở dữ liệu làm chốt chặn cuối cùng. Nếu Redis bị lỗi hoặc reset, MongoDB sẽ từ chối bản ghi trùng và ném lỗi `DuplicateKey`.

### 3.3. Nhóm ghi đồng loạt (Group Commit / Batch Flusher)
- Thay vì mỗi tin nhắn gọi một lệnh `InsertOne` xuống MongoDB làm quá tải IOPS đĩa:
  - Actor đẩy doc vào hàng đợi của **Flusher**.
  - Flusher gom các tin nhắn từ nhiều phòng chat khác nhau trong một cửa sổ cực ngắn (ví dụ: tối đa 50 tin hoặc 2ms) và thực thi **một lệnh duy nhất**:
    $$\text{insertMany}(\text{ordered} = \text{false}, w = \text{"majority"})$$
  - Cơ chế này giúp hệ thống đạt throughput 5,000 – 10,000 tin/giây mà vẫn bảo đảm độ an toàn dữ liệu trên cả 3 node MongoDB Replica Set.

### 3.4. Fast Ack & Best-Effort Async Publish
- **Nguyên tắc:** Trả Ack cho người gửi ngay sau khi Mongo ghi nhận `w:majority`. Không bắt người gửi phải chờ đến khi tin được phát tán qua NATS JetStream.
- Việc phát sự kiện `msg_created` qua NATS là hoàn toàn bất đồng bộ (Best-effort). Nếu tiến trình Core bị tắt đột ngột trước khi phát event, **Reconciler nền (Module 10)** sẽ đọc Change Stream của MongoDB và phát bù sự kiện.

### 3.5. Hợp đồng Snapshot & Xử lý Đến lệch thứ tự (Out-of-order Resilience — Quy tắc R6/R7)
- **Vấn đề thực tế mạng di động:** Do NATS JetStream và kết nối mạng di động, Client SDK có thể nhận được sự kiện `msg_changed` (sửa tin) **trước khi** nhận được sự kiện `msg_created` (tạo tin ban đầu).
- **Cơ chế bảo đảm:**
  - Mọi sự kiện phát tán qua NATS đều mang **toàn bộ Snapshot hiện tại của tin nhắn** kèm phiên bản `ver`.
  - Client SDK áp dụng quy tắc: *"Phiên bản `ver` cao hơn luôn thắng"*.
  - Nếu nhận được `msg_changed` cho một tin nhắn chưa có trên màn hình, Client SDK lưu snapshot này vào bộ nhớ và hiển thị trực tiếp. Khi sự kiện `msg_created` (mang `ver: 1`) đến sau, Client so sánh thấy phiên bản hiện tại đã là `ver: 2` nên sẽ bỏ qua gói tin cũ một cách an toàn.

---

## 4. Kỹ thuật & Công nghệ triển khai (Technologies)

### 4.1. Cấu trúc bảng MongoDB Clustered Collection `messages`

Để tối ưu hoá đọc ghi theo trình tự thời gian, collection `messages` được cấu hình dạng **Clustered Index**:

```javascript
{
  "_id": BinData(0, "...16 bytes..."), // 8 bytes room_id + 8 bytes seq (Big-Endian)
  "room_id": NumberLong("8492049201948201"),
  "seq": NumberLong(10042),            // Số thứ tự tăng dần liên tục trong phòng
  "cid": "client-uuid-generated-123",  // Client Message ID chống trùng
  "sender_id": "usr_1001",
  "kind": 1,                           // 1: Text thường, 2: Media, 3: System Msg
  "text": "Nội dung tin nhắn trao đổi",
  "media_refs": ["https://s3.example.com/file.pdf"],
  "meta": {"client_timestamp": 1773291823},
  "created_at": ISODate("2026-10-10T10:05:00.123Z"),
  "ver": NumberLong(1),                // Version nội dung (tăng khi sửa)
  "deleted": false,                    // Đánh dấu xoá cho mọi người
  "rx": {"👍": 3, "❤️": 1},            // Tóm tắt reaction (bộ đếm dẫn xuất)
  "rc": 0                              // Số lượng tin trả lời trong thread
}
```

- **Khoá chính Clustered `_id`:** Ghép 8 byte `room_id` và 8 byte `seq`. Dữ liệu của cùng một phòng chat nằm liền kề nhau trên ổ đĩa SSD, giúp việc đọc phân trang lịch sử tin nhắn đạt hiệu năng tối đa (p99 ≤20ms cho 50 tin).
- **Index chống trùng:** Unique Index trên `{room_id: 1, cid: 1}`.

### 4.2. Kỹ thuật Redis Lua Script (CID Dedupe)
- Sử dụng lệnh `EVALSHA` gọi trước mã SHA1 của script Lua trên Redis để kiểm tra và đặt chỗ CID nguyên tử, tránh round-trip mạng nhiều lần giữa Core và Redis.
- Khi Redis gặp sự cố, thành phần `platform/redisguard` tự động kích hoạt chế độ bảo vệ (cooldown circuit breaker), bypass tầng cache Redis và dựa hoàn toàn vào Unique Index của MongoDB để hệ thống không bị gián đoạn.

### 4.3. Hợp đồng Mã lỗi gRPC (Error Matrix)

| Mã lỗi gRPC | Tên lỗi domain | Tình huống kích hoạt | Hành vi Client SDK yêu cầu |
|---|---|---|---|
| `InvalidArgument` | `ERR_PAYLOAD_TOO_LARGE` | Văn bản gửi vượt quá 16KB hoặc metadata > 8KB. | Không retry; hiển thị thông báo tin nhắn quá dài. |
| `InvalidArgument` | `ERR_INVALID_CID` | Mã `cid` gửi lên rỗng hoặc chứa ký tự cấm (ví dụ chứa dấu `:`). | Không retry; sửa cách sinh CID trên Client. |
| `Unavailable` | `ERR_SLOT_UNAVAILABLE` | Node Core vừa mất quyền sở hữu slot hoặc MongoDB đang failover bầu cử Primary. | Có retry; Client tự động backoff jitter (50ms–200ms) và gọi lại. |
| `PermissionDenied` | `ERR_NOT_IN_ROOM` | Người gửi không phải là thành viên hợp lệ của phòng chat. | Không retry; chặn ô nhập tin nhắn trên giao diện. |

---

## 5. Hạn chế kỹ thuật & Đánh đổi (Limitations & Trade-offs)

1. **Nút thắt cổ chai phòng chat siêu nóng (Hot-Room Bottleneck):**
   - *Hạn chế:* Do mỗi phòng chat chỉ có 1 Goroutine Actor xử lý tuần tự, thông lượng tối đa của một phòng chat đơn lẻ bị giới hạn ở mức **~5,000 tin/giây**.
   - *Đánh đổi:* Chấp nhận giới hạn này để đổi lấy tính đúng đắn tuyệt đối về thứ tự `seq` mà không cần đến các thuật toán đồng thuận phức tạp (như Paxos/Raft trên từng phòng).
2. **Không đảm bảo đồng hồ vật lý toàn cục giữa các phòng khác nhau:**
   - *Hạn chế:* Hệ thống chỉ đảm bảo thứ tự `seq` tăng dần và nhất quán giữa các người dùng **trong cùng một phòng chat**. Thứ tự giữa tin nhắn của Phòng A và Phòng B không có quan hệ nhân quả toàn cục.
3. **Giới hạn kích thước gói tin (Payload Quota):**
   - Độ dài văn bản tối đa: **16 KB**.
   - Dữ liệu metadata tối đa: **8 KB**.
   - Số lượng tệp/tham chiếu đính kèm tối đa: **10 tệp**.

---

## 6. Phụ thuộc kỹ thuật & Bán kính sự cố (Dependencies & Blast Radius)

| Thành phần phụ thuộc | Mục đích phụ thuộc | Ứng xử khi thành phần gặp sự cố (Failure Behavior) |
|---|---|---|
| **MongoDB Clustered Collection** | Lưu trữ tin vĩnh viễn (`messages`) | Nếu Mongo Replica Set mất kết nối hoặc quá tải: Pipeline flush bị nghẽn, Actor từ chối nhận thêm tin và trả `UNAVAILABLE` cho Client. |
| **Redis Dedupe Instance** | Lưu trữ tạm CID để chống trùng nhanh | Nếu Redis Dedupe bị chậm hoặc ngắt kết nối: `redisguard` kích hoạt mạch ngắt. Hệ thống bỏ qua Redis và để MongoDB unique key bắt trùng (độ trễ tăng nhẹ từ 5ms lên 15ms nhưng không chết dịch vụ). |
| **NATS JetStream (CHATIM_EVT)** | Kênh phát tán sự kiện tin mới | Nếu NATS quá tải: Fast-path publish thất bại nhưng Core vẫn trả Ack thành công cho người gửi. Event sẽ được Reconciler phát bù qua Change Stream sau 5s. |
| **Platform Slot Manager** | Đảm bảo duy nhất 1 Core sở hữu phòng | Nếu Core bị mất hợp đồng thuê Slot: Actor của phòng tự động từ chối các lệnh gửi tin mới (`AfterLose`), hoàn tất các tin đang ghi dở rồi giải phóng tài nguyên. |

### 6.1. Chỉ số Sức khoẻ Vận hành & Cảnh báo SRE (Key Metrics & Alerts)

- **`chatim_send_flush_duration_seconds`:** Biểu đồ histogram đo độ trễ p99 khi Flusher ghi batch xuống MongoDB.
  - *Ngưỡng SLA:* p99 ≤ 30ms.
  - *Luật cảnh báo (`ChatimFlushLatencyHigh`):* Cảnh báo nếu p99 > 30ms kéo dài trên 5 phút.
- **`chatim_cid_dedupe_hits_total{tier="lru|redis|mongo"}`:** Đo lường số lượng bản tin trùng lặp bị bắt tại từng tầng lọc.
- **`chatim_actor_mailbox_queue_depth`:** Độ sâu hàng đợi tin nhắn trong RAM của các Actor phòng. Báo động nếu có phòng tích tụ >1,000 tin chưa kịp xử lý.
