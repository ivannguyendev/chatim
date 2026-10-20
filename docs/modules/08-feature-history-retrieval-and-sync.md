# Module 08: Đọc dữ liệu — Xem lịch sử tin nhắn & Đồng bộ khi Reconnect

> **Mục tiêu module:** Cho phép người dùng cuộn xem lịch sử trò chuyện mượt mà ở bất kỳ vị trí nào với độ trễ cực thấp (p99 ≤20ms cho 50 tin), bảo vệ tính riêng tư qua bộ lọc hiển thị động (Reader View Pipeline), và khôi phục trạng thái tức thì sau khi rớt mạng mà không gây quá tải hệ thống (Chống Reconnect Storm).

---

## 1. Các Use Case nghiệp vụ (Business Use Cases)

| Mã Use Case | Tên Use Case | Tác nhân | Mô tả & Kết quả kỳ vọng |
|---|---|---|---|
| **UC-HIST-01** | **Cuộn xem lịch sử tin nhắn (Pagination)** | Người dùng | Cuộn ngược lên để xem các tin nhắn cũ hơn, hoặc cuộn xuống để xem các tin nhắn mới hơn. Dữ liệu tải về nhanh chóng (50 tin/lần), không bị giật lag hay nhảy lộn xộn. |
| **UC-HIST-02** | **Nhảy đến tin nhắn chỉ định (Jump to Message)** | Người dùng | Bấm vào một tin nhắn được trích dẫn (Quote), kết quả tìm kiếm hoặc thông báo nhắc tên để nhảy thẳng tới vị trí tin nhắn đó trong timeline và tải ngữ cảnh các tin xung quanh. |
| **UC-HIST-03** | **Khôi phục trạng thái sau khi mất mạng (Reconnect Sync)** | Client SDK | Khi điện thoại đi vào vùng mất sóng (hoặc tắt ứng dụng) rồi kết nối lại mạng, Client tự động kéo các tin nhắn và thay đổi mới phát sinh trong thời gian offline mà không cần tải lại toàn bộ phòng chat từ đầu. |
| **UC-HIST-04** | **Bảo vệ quyền riêng tư lúc đọc (View Pipeline Filtering)** | Hệ thống | Người đọc không thấy nội dung các tin nhắn mà mình đã chủ động ẩn hay gửi trước mốc "Xoá lịch sử" (tin còn chỗ với cờ `hidden`), và chỉ thấy tin đã thu hồi dưới dạng `deleted` không nội dung. |

---

## 2. Luồng hoạt động chi tiết (How It Works)

### 2.1. Sơ đồ Đường ống Chế biến Dữ liệu Đọc (Reader View Pipeline)

Trước khi dữ liệu thô từ cơ sở dữ liệu được trả về cho Client, nó bắt buộc phải đi qua **Reader View Pipeline** trong bộ nhớ của Core để thi hành các chính sách riêng tư cá nhân hoá:

```mermaid
flowchart LR
    RawMongo[("MongoDB (messages)<br/>Clustered _id: room│thread│seq")] --> View["Reader View Pipeline (message/view)"]

    subgraph PipelineSteps["view.Default() rồi trang trí trong grpcsrv"]
        F1["1. CollapseRetried<br/>Bỏ bản trùng cùng (người gửi, cid),<br/>giữ seq nhỏ nhất"] --> F2["2. MaskDeleted<br/>Tin deleted: bỏ text, mention,<br/>@all, forward, rx, rc (giữ rp)"]
        F2 --> F3["3. HideForViewer<br/>created_at <= cleared_at hoặc seq trong hidden:<br/>hidden = true, bỏ nội dung, giữ seq"]
        F3 --> F4["4. Trang trí (grpcsrv)<br/>reply_preview, my_reaction, bookmarked"]
    end

    View --> F1
    F4 --> CleanDTO["Clean Client DTO<br/>(An toàn, chuẩn bảo mật)"]
```

### 2.2. Luồng Phân trang & Xử lý qua Reader View Pipeline (Sequence Diagram)

Toàn bộ dữ liệu thô từ cơ sở dữ liệu đều phải đi qua **Đường ống chế biến giao diện (Reader View Pipeline)** trước khi trả về cho người đọc:

```mermaid
sequenceDiagram
    autonumber
    participant Client as Client SDK
    participant Core as Core (api/grpcsrv)
    participant View as Reader View Pipeline (message/view)
    participant Msgs as MongoDB (messages)
    participant Members as MongoDB (members)
    participant Hidden as MongoDB (hidden)

    Client->>Core: GetHistory(room_id, anchor=BEFORE, seq=10042, limit=50)

    Note over Core,Msgs: 1. Đọc dữ liệu thô từ Clustered Index (p99 ≤ 20ms)
    Core->>Msgs: Find({_id: {$gte: room│0│0, $lt: room│0│10042}}).sort({_id: -1}).limit(50)
    Msgs-->>Core: 50 documents thô

    Note over Core,View: 2. Đưa vào Reader View Pipeline chế biến
    Core->>View: Process(raw_messages, reader_user_id)

    Note over Core,Members: cleared_at lấy từ doc member khi kiểm quyền (Authorize)
    Core->>Hidden: HiddenIn: tin ẩn của người đọc trong khoảng seq này

    loop Với từng tin nhắn
        alt Tin nhắn có created_at <= cleared_at HOẶC nằm trong hidden
            Note over View: hidden = true, bỏ nội dung (giữ seq, version)
        else Tin nhắn có cờ deleted == true
            Note over View: deleted = true, bỏ text, mention, forward, reaction, reply count
        else Tin nhắn hợp lệ bình thường
            Note over View: Giữ nguyên nội dung, đính kèm tóm tắt reaction (rx)
        end
    end

    View-->>Core: Danh sách tin nhắn đã che
    Core-->>Client: Trả về tối đa 50 tin, trang sau gọi BEFORE với seq nhỏ nhất vừa nhận
```

---

## 3. Cơ chế bảo đảm tính đúng đắn (Guarantees & Mechanisms)

### 3.1. Phân trang bằng Con trỏ tuần tự (Cursor-Based Pagination — Không dùng Offset)
- **Vấn đề cần giải quyết:** Nếu dùng phân trang kiểu truyền thống `OFFSET 50 LIMIT 50`, khi người dùng đang cuộn xem lịch sử mà trong phòng có người gửi thêm tin mới, toàn bộ vị trí offset sẽ bị trôi, dẫn đến việc người dùng bị nhìn thấy tin nhắn trùng lặp hoặc bị sót tin. Ngoài ra, câu lệnh `OFFSET` lớn làm database quét bảng cực kỳ chậm ($O(N)$).
- **Cơ chế bảo đảm:**
  - Hệ thống phân trang bằng mốc (`anchor`) và chính số thứ tự `seq`: `LATEST`, `OLDEST`, `BEFORE seq`, `AFTER seq`, `AROUND seq` (`ceil(limit/2)` tin về trước kể cả seq đó, hai lượt quét song song).
  - Truy vấn phân trang luôn tận dụng Clustered Index:
    $$\text{db.messages.find}(\{\text{\_id}: \{\$lt: \text{key}(\text{RID}, \text{thread}, \text{SEQ})\}\}).\text{sort}(\{\text{\_id}: -1\}).\text{limit}(50)$$
  - Tốc độ truy vấn luôn là $O(\log N)$ bất kể phòng chat có 100 tin hay 10 triệu tin, đảm bảo đạt chuẩn phi chức năng **A1: tải 50 tin ở bất kỳ vị trí nào chỉ mất ≤20ms**.

### 3.2. Đồng bộ khi kết nối lại bằng Snapshot (Nguyên tắc R10 / D74)
- **Vấn đề cần giải quyết (Reconnect Storm):** Khi một sự cố mạng diện rộng kết thúc, hàng trăm nghìn thiết bị cùng kết nối lại hệ thống tại cùng một thời điểm. Nếu hệ thống cố gắng phát lại (Replay) toàn bộ các sự kiện NATS mà thiết bị đã bỏ lỡ, hệ thống sẽ sập nguồn vì quá tải băng thông và CPU (Thundering Herd).
- **Cơ chế bảo đảm:**
  - **Không phát lại Event Stream (Nguyên tắc P8):** Thiết bị kết nối lại **không kéo event stream**, mà kéo **Snapshot trạng thái mới nhất**.
  - **Những gì core có hôm nay:** `ListMyRooms` trả danh sách hội thoại kèm unread và tin xem trước (`preview.seq` là tin mới nhất người đọc thấy) của từng phòng; client so với `seq` cuối mình có rồi gọi `GetHistory(anchor=AFTER, seq=seq cuối)` cho những phòng có tin mới (hoặc `LATEST` nếu khoảng hở quá lớn).
  - **Sync token** (một bảng `{room: last_seq}` gửi một lần) chưa có trong core: vai trò này thuộc Mention App và còn mở (kiến trúc §1.2).
  - Mỗi doc tin nhắn trả về luôn mang **toàn bộ snapshot trạng thái hiện tại** (nội dung mới nhất, cờ đã xoá, tóm tắt reaction `rx`, số replies `rc`). Nhờ đó, Client chỉ cần đè snapshot này lên giao diện là đạt trạng thái đồng bộ tuyệt đối mà không cần tính toán chuỗi sự kiện phức tạp.

```mermaid
flowchart TD
    ClientOnline["Client có mạng trở lại (Reconnect)"] --> CalcGap["Tính độ lệch tin: gap = preview.seq (ListMyRooms) - client_last_seq"]
    CalcGap --> CheckGap{"Khoảng hở tin nhắn (gap)?"}

    CheckGap -- "gap nhỏ (ngưỡng do client chọn, ví dụ 500)" --> FastDelta["Delta Catchup:<br/>- GetHistory AFTER client_last_seq, từng trang ≤ 100<br/>- Nhận trực tiếp Snapshot mới nhất (ver, rx, rc)"]
    FastDelta --> ApplyLocal["Đè snapshot lên Local DB -> Sẵn sàng tức thì"]

    CheckGap -- "gap lớn (Rớt mạng quá lâu)" --> SnapshotReset["Snapshot Reset (Đặt lại góc nhìn):<br/>- Đánh dấu vùng nhớ cục bộ là out-of-date<br/>- Kéo 50 tin mới nhất quanh màn hình hiện tại<br/>- Kéo mới danh mục phòng chat"]
    SnapshotReset --> FreshView["Render giao diện tươi mới, tránh nghẽn I/O"]
```

### 3.3. Client Jitter (Rải đều tải khi Reconnect)
- Để bảo vệ cụm Core lúc mạng vừa phục hồi:
  - Client SDK được tích hợp thuật toán **Exponential Backoff with Full Jitter**:
    $$T_{\text{wait}} = \text{random}(0, \min(T_{\text{max}}, T_{\text{base}} \times 2^{\text{attempt}}))$$
  - Các kết nối Reconnect được rải đều tự nhiên trong khoảng thời gian từ vài giây đến vài chục giây, triệt tiêu hoàn toàn đỉnh nhọn lưu lượng (Traffic Spike).

---

## 4. Kỹ thuật & Công nghệ triển khai (Technologies)

### 4.1. Hiệu năng vượt trội từ MongoDB Clustered Collection
- Nhờ khoá chính `_id` 24 bytes gồm `(8 bytes room_id + 8 bytes thread_root + 8 bytes seq)`, toàn bộ các tin nhắn của cùng một phòng được lưu trữ vật lý nằm liền kề nhau trên các trang nhớ (Data Pages) của công cụ lưu trữ WiredTiger.
- Khi truy vấn 50 tin nhắn theo `seq`, MongoDB chỉ cần nạp 1–2 trang nhớ từ ổ cứng NVMe vào bộ nhớ đệm (WiredTiger Cache), giúp câu lệnh hoàn thành trong vòng **2–5ms**.

### 4.2. Bộ nhớ đệm Room-Tail Cache trên Core (M3)
- `GetHistory` **không** dùng cache: nó đọc thẳng store mỗi lần (không qua actor), rồi chạy view pipeline.
- `room/roomcache` trên mỗi core giữ đuôi phòng (≤ 300 tin gọn + 10 tin xem trước, TTL 30s, singleflight, đối chiếu `last_seq`/`last_edit_at`) chỉ để phục vụ danh sách hội thoại (`ListMyRooms`/`GetRooms`: unread, tin xem trước), xem Module 07.

### 4.3. Hợp đồng Mã lỗi gRPC (Error Matrix)

| Mã lỗi gRPC | Tên lỗi domain | Tình huống kích hoạt | Hành vi Client SDK yêu cầu |
|---|---|---|---|
| `InvalidArgument` | `ERR_PAGE_LIMIT_EXCEEDED` | Yêu cầu số lượng tin nhắn trên 1 trang vượt quá 100 tin (`limit = 0` → 50). | Không retry; Client điều chỉnh lại `limit <= 100`. |
| `PermissionDenied` | `ERR_NOT_IN_ROOM` | Người đọc không thuộc phòng chat cần xem lịch sử. | Không retry; hiển thị thông báo không có quyền truy cập. |
| `Unavailable` | `ERR_STORAGE_UNAVAILABLE` | Cụm MongoDB Secondary/Primary đang failover hoặc quá tải. | Có retry; Client tự động backoff retry sau 1–2s. |

---

## 5. Hạn chế kỹ thuật & Đánh đổi (Limitations & Trade-offs)

1. **Giới hạn số lượng tin nhắn trên mỗi trang:**
   - Mặc định mỗi lần tải là **50 tin**, tối đa không quá **100 tin/lần**.
   - Ngăn chặn việc Client cố tình yêu cầu tải hàng nghìn tin nhắn cùng lúc làm cạn kiệt RAM của server và gây đơ giao diện điện thoại.
2. **Không hỗ trợ phân trang kiểu ngẫu nhiên (No Random Page Jumping):**
   - Không thể bấm "Nhảy tới trang số 42" như trên website thương mại điện tử; chỉ hỗ trợ cuộn liên tục (Infinite Scroll) dựa trên `seq` hoặc nhảy tới một `seq` cụ thể (`AROUND`); chưa có mốc theo thời gian.
3. **Đánh đổi của Reconnect Snapshot:**
   - Client không biết được chuỗi biến động trung gian (ví dụ: tin nhắn bị sửa 3 lần trong lúc mất mạng), client chỉ nhận được kết quả cuối cùng của lần sửa thứ 3. Đây là sự đánh đổi hoàn toàn hợp lý trong nghiệp vụ ứng dụng chat.

---

## 6. Phụ thuộc kỹ thuật & Bán kính sự cố (Dependencies & Blast Radius)

| Thành phần phụ thuộc | Mục đích phụ thuộc | Ứng xử khi thành phần gặp sự cố (Failure Behavior) |
|---|---|---|
| **MongoDB Replica Set (Primary)** | Phục vụ đọc lịch sử | Store đọc từ Primary (read concern `majority` hoặc `local` tuỳ collection). Mongo failover: lệnh đọc trả `UNAVAILABLE`, client backoff rồi gọi lại. |
| **Reader View Pipeline** | Che giấu dữ liệu theo người đọc | Chạy hoàn toàn in-memory trên tiến trình Core, không truy vấn thêm (quyền và `cleared_at` đã có từ bước `Authorize`). |
| **Room-Tail Cache (`room/roomcache`)** | Tăng tốc danh sách hội thoại | Là cache tạm thời (In-memory). Nếu Core khởi động lại, cache sẽ tự động nạp lại từ MongoDB trong lần truy vấn đầu tiên mà không làm mất dữ liệu. |

### 6.1. Chỉ số Sức khoẻ Vận hành & Cảnh báo SRE (Key Metrics & Alerts)

Core chưa xuất metric độ trễ `GetHistory` hay lưu lượng reconnect; mục tiêu p99 ≤ 20ms (A1) chưa có detector. Metric thực có liên quan:

- **`chatim_core_room_cache_hits_total{cache}`**, **`chatim_core_room_cache_misses_total{cache}`** (`cache` = `info`, `tail`, `snapshot`, `path`): Lượt đọc cache danh sách hội thoại từ RAM hay từ store; tỷ lệ trúng tính bằng PromQL. Chỉ để xem tốc độ, không có alert.
- **`chatim_core_grpc_load_shed_total`:** Lệnh gRPC bị từ chối do quá số lệnh đang chạy (dấu hiệu reconnect dồn dập).
