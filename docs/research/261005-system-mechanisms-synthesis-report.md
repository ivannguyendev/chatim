# Nghiên cứu Cơ chế Hệ thống Chatim: Tổng hợp Phản biện Đa chiều và Chuẩn hoá Kiến trúc Phân tán

> **Mã tài liệu:** `RES-261005-SYS-MECH`  
> **Ngày hoàn thành:** 2026-10-05  
> **Trạng thái:** Báo cáo Nghiên cứu Chính thức (Canonical Research Whitepaper)  
> **Tác giả:** Nhóm Kiến trúc chatim & Ban Đánh giá Độc lập  
> **Tài liệu nguồn tổng hợp:** [Báo cáo gốc](../archive/research/261004-system-mechanisms-report.md) · [Phản biện CL](../archive/research/261004-system-mechanisms-review-cl.md) · [Phản biện GE](../archive/research/261004-system-mechanisms-review-ge.md) · [Vòng 2 CL](../archive/research/261004-system-mechanisms-round2-cl.md) / [GE](../archive/research/261004-system-mechanisms-round2-ge.md) · [Kết quả vòng 2 CL](../archive/research/261004-system-mechanisms-round2-review-cl.md) / [GE](../archive/research/261004-system-mechanisms-round2-review-ge.md) · [Bản tổng hợp quyết định](../archive/research/261004-system-mechanisms-synthesis.md) · [Kiến trúc chuẩn hoá](../designs/261005-chatim-architecture.md).

---

## Tóm tắt Điều hành (Executive Summary)

Hệ thống **chatim** được định vị là hạ tầng nhắn tin dùng chung (Internal CPaaS) đa người thuê (multi-tenant) với mục tiêu vận hành tại quy mô **100.000 kết nối đồng thời (CCU)**, lưu lượng đỉnh **5.000 – 10.000 tin nhắn/giây**, và lưu trữ tích lũy **5 – 20 tỷ tin nhắn** trong vòng 2 năm trên ngăn xếp công nghệ: **Go + MongoDB Replica Set (3 node) + NATS JetStream**.

Để bảo đảm tính sống còn và tính đúng đắn toán học của hệ thống trước khi bước vào giai đoạn sản xuất (production), một quy trình **thẩm định cơ chế hệ thống (system mechanisms stress-testing)** kéo dài 2 vòng độc lập đã được thực hiện giữa hai bên phản biện độc lập (CL - Claude và GE - Gemini) cùng kiến trúc sư trưởng (Owner).

Nghiên cứu này tổng hợp toàn diện các phát hiện, giải phẫu những ngụy biện kỹ thuật thường gặp trong hệ phân tán, phân tích sự chuyển dịch mô hình tư duy từ cập nhật trực tiếp (*In-place Mutation*) sang mô hình kép **Fact Bất Biến & State Projection**, đồng thời thiết lập các đảm bảo hình thức (*formal guarantees*) cho toàn bộ các luồng: cấp thứ tự, chống trùng lặp, hội tụ bộ đếm, đường ống bắt sự kiện thay đổi dữ liệu (CDC Reader Pipeline), và khả năng phục hồi sau thảm họa bão kết nối lại (Reconnect Storm).

---

## 1. Đặt Vấn đề & Phương pháp Luận (Introduction & Methodology)

### 1.1 Thách thức Cốt lõi của Hệ thống Nhắn tin Phân tán

Xây dựng hệ thống chat ở quy mô lớn là bài toán xử lý đồng thời cực độ với các ràng buộc mâu thuẫn:
1. **Tính tuần tự nghiêm ngặt (Strict Per-Room Linearizability):** Trong mỗi phòng chat, mọi người tham gia phải quan sát thứ tự tin nhắn nhất quán tuyệt đối.
2. **Thông lượng ghi cao với độ trễ thấp (Sub-100ms Write Latency @ 10K msg/s):** Phải lưu trữ bền vững (`w:majority`) trước khi phản hồi (ack), không được phép mất tin nhắn khi node gặp sự cố.
3. **Khuếch đại Đọc/Phát tán (Fanout & Read Amplification):** Một tin nhắn gửi vào nhóm 5.000 thành viên hoặc kênh thông báo 200.000 subscriber đòi hỏi xử lý fanout tức thì mà không làm tê liệt bộ nhớ gateway hay đường truyền mạng.
4. **Hội tụ Tương tác (State & Counter Convergence):** Các hành động cập nhật diễn ra đồng thời: sửa nội dung, xoá tin, thả cảm xúc (reactions), cập nhật trạng thái đã đọc (read receipts), thay đổi số lượng chưa đọc (unread counters).

### 1.2 Quy trình Phản biện Biện chứng 2 Vòng (Dialectical Peer Review)

Thay vì thiết kế đơn tuyến, đề án cơ chế hệ thống được đưa qua mô hình đối kháng biện chứng:
- **Tác giả (System Authors):** Đưa ra báo cáo cơ chế chi tiết với 18 yêu cầu nghiêm ngặt (R1–R18), 13 nguyên tắc thiết kế (D1–D13), và 8 đảm bảo cốt lõi (RC1–RC5, CD1–CD3).
- **Phản biện CL (Claude) & GE (Gemini):** Độc lập tấn công vào các giả định ẩn, tìm kiếm kẽ hở tương tranh (race conditions), phân tích rủi ro bão tải (reconnect storm, bão duplicate), và đánh giá độ bền của Oplog.
- **Vòng 2 (Deep Divergence Resolution):** Cô lập 4 mâu thuẫn then chốt không thể dung hòa bằng suy luận cảm tính, chất vấn bằng phản ví dụ cụ thể, đối chiếu tài liệu nội tại của cơ sở dữ liệu, và chốt quyết định kỹ thuật bằng chứng minh hội tụ.

---

## 2. Sự Chuyển Dịch Mô Hình Tư Duy Cốt Lõi (Core Paradigm Shifts)

Qua quá trình phản biện, hệ thống đã thực hiện 4 bước ngoặt mô hình tư duy có tính quyết định:

```
[Mô hình Cũ: Đột biến Trực tiếp]                 [Mô hình Mới: Fact-Projection Duality]
Client ──► Update messages (In-place)             Client ──► Append Fact Bất biến (Unique CAS)
                 │                                                │
       Oplog updateLookup (Nặng I/O)                      CDC Reader (Chỉ bắt Insert)
                 │                                                │
       Tranh chấp lock, mất snapshot                      Chiếu ra Projection `messages` (Idempotent)
```

### 2.1 Fact-State Duality: Fact Bất Biến Tách Rời State Projection

* **Vấn đề của mô hình cũ:** Khi người dùng sửa tin, xoá tin hoặc ghim tin, hệ thống cũ cố gắng cập nhật trực tiếp (`$set`) vào document trong collection `messages`. Để downstream (reconciler, publisher) phát hiện được nội dung cũ và mới, hệ sinh thái buộc phải bật `updateLookup` hoặc `pre/post-image` của MongoDB Change Streams. Điều này gây suy thoái I/O nghiêm trọng, phụ thuộc chặt chẽ vào engine Mongo, và gây sai lệch lịch sử sửa (ghi đè nội dung phiên bản mới vào slot phiên bản cũ).
* **Mô hình chuẩn hoá:** Mọi sự kiện biến đổi trạng thái trong hệ thống là **một Fact bất biến duy nhất (Immutable Fact)** được append tuần tự:
  - `message_edits`: Lưu từng phiên bản sửa tin với khóa `{room_id, msg_id, edit_ver}`.
  - `message_deletions`: Lưu sự kiện xoá tin `{room_id, msg_id, deleted_at, scope}`.
  - `room_pins`: Lưu sự kiện ghim tin `{room_id, msg_id, pinned_at}`.
* **Lợi ích kiến trúc:**
  - Collection `messages` chỉ đóng vai trò là một **State Projection (Materialized View)** phục vụ truy vấn đọc tối ưu của client.
  - Change Streams/CDC chỉ cần lắng nghe thao tác `insert` trên các collection Fact, hoàn toàn loại bỏ nhu cầu `updateLookup` và `pre-image`.
  - Dễ dàng truy vết kiểm toán (audit log) và tái tạo toàn bộ trạng thái phòng chat từ chuỗi fact bất biến.

### 2.2 Khung Phân Lớp Dữ Liệu & Ngân Sách Khuếch Đại (7-Class Taxonomy)

Toàn bộ các thực thể dữ liệu trong hệ thống được phân rã thành 7 lớp với các ràng buộc về độ bền, tính tuần tự, và độ phức tạp lan truyền:

| Lớp Dữ liệu | Thực thể Đại diện | Cơ chế Tuần tự & Khóa Chống trùng | Chính sách Lan truyền (Effect Policy) |
|---|---|---|---|
| **1. Tin nhắn (Core Message)** | Nội dung chat, tệp đính kèm | Khóa ngoại phân vùng theo Room, tuần tự hóa qua Sequence Counter | Persist `w:majority` $\to$ JetStream Work Stream $\to$ Live Fanout |
| **2. Fact Đổi trạng thái** | Sửa tin, Xoá tin, Ghim tin | Khóa CAS Version (`base_ver`), Idempotent Append | Persist Fact $\to$ Update Projection $\to$ Ack Client $\to$ Live Broadcast |
| **3. Tương tác Tích lũy (Counters)** | Số lượng Reaction, Số reply | Không tuần tự hóa đơn chiếc; Recount CAS-ver theo chu kỳ debouncing | Batch/Coalesce trong bộ nhớ $\to$ Ghi đè CAS version cao hơn $\to$ Coalesce Event |
| **4. Con trỏ Vị trí (Read Pointer)** | `read_seq` của thành viên | Không đơn điệu nghiêm ngặt (Last-Writer-Wins); Không lưu lịch sử | Coalesce theo người dùng $\to$ Update in-place $\to$ Broadcast nội bộ phòng |
| **5. Trạng thái Chưa đọc (Unread)** | Số tin chưa đọc mỗi phòng | Không lưu trữ denormalized; Tính toán lười (Lazy Evaluation) lúc đọc | Lazy Scan có chặn giới hạn $S$ (R17) $\to$ Trả về cận dưới kèm cờ `approx` nếu vượt ngưỡng |
| **6. Trạng thái Mềm (Ephemeral)** | Đang gõ (Typing), Hiện diện (Presence) | Hoàn toàn không lưu database; Không cần số thứ tự tuần tự | Trực tiếp qua NATS Core Pub/Sub; Bỏ qua nếu nghẽn hoặc mất kết nối |
| **7. Tin nhắn Hệ thống (System Fact)** | "A đã thêm B", "Tin nhắn đã ghim" | Sinh CID tất định: `cid = "sys:" + event_id` | Tái sử dụng pipeline chống trùng của tin nhắn thông thường |

### 2.3 Phá vỡ Ngụy biện `$inc` và Định lý Hội tụ Bộ đếm (Counter Convergence Invariant)

Một trong những tranh luận gay gắt nhất giữa hai bên phản biện là bài toán duy trì số lượng tương tác (Reaction count / Reply count):
- **Đề xuất ban đầu (và GE bảo vệ):** Sử dụng `$inc: { count: 1 }` trực tiếp tại Fast-path để tối ưu độ trễ, sau đó để Reconciler quét nền chạy `countDocuments()` định kỳ sửa sai.
- **Lập luận bác bỏ của CL:** **Toán tử `$inc` và thao tác ghi đè kết quả đếm (Recount overwrite) KHÔNG CÓ TÍNH GIAO HOÁN (Non-commutative).**

#### Chứng minh Phản ví dụ (The Divergence Anomaly):
Giả sử bộ đếm reaction đang ở giá trị $C=10$.
1. Thời điểm $t_1$: Client $A$ gửi reaction mới. Lệnh `$inc` được phát đi nhưng gặp phân vùng mạng/nghẽn thread tại worker.
2. Thời điểm $t_2$: Reconciler kích hoạt do timeout, thực hiện scan chính xác trong DB: tìm thấy 10 document reaction hợp lệ $\to$ Ghi đè $C = 10$.
3. Thời điểm $t_3$: Lệnh `$inc` bị trễ từ thời điểm $t_1$ cuối cùng cũng cập bến DB và thực thi `$inc: 1$`.
4. **Kết quả:** $C = 11$, trong khi thực tế chỉ có 10 bản ghi reaction. Bộ đếm bị lệch vĩnh viễn $+1$ cho đến kỳ recount tiếp theo (hoặc vĩnh viễn nếu không có trigger tiếp theo).

```
   Fast-path $inc (trễ do lag mạng) ───┐
                                       ▼ (đến muộn)
t0 (C=10) ──► t1 (Recount: C=10) ──► t2 (Áp dụng $inc: C=11)  ===> SAI LỆCH VĨNH VIỄN
```

#### Giải pháp Chuẩn hoá: Recount CAS-Version
- Bỏ hoàn toàn `$inc` ở fast path.
- Khi có thao tác reaction mới, Core chỉ ghi nhận fact reaction (với unique index `{room_id, msg_id, emoji, user_id}`).
- Fast-path chỉ gửi tín hiệu *Touch* (thông báo cần kiểm kê) vào bộ gom (Coalescer) được ghim theo slot của room.
- Worker thực hiện tính toán: `new_count = count(reactions)`.
- Ghi cập nhật bằng lệnh CAS có kiểm tra phiên bản:
  $$\text{UPDATE } \text{counters SET } count = \text{new\_count}, ver = ver + 1 \text{ WHERE } target\_id = T \text{ AND } ver = \text{base\_ver}$$
- Nếu xung đột phiên bản (`MatchedCount == 0`), worker tự động đọc lại phiên bản mới nhất và thử lại với Exponential Backoff + Jitter. Tính hội tụ được đảm bảo tuyệt đối.

---

## 3. Giải Phẫu Các Ngụy Biện Kỹ Thuật (Anatomy of Fallacies & Edge Cases)

Quá trình phản biện vòng 1 và vòng 2 đã bóc tách nhiều giả định sai lầm phổ biến khi thiết kế hệ phân tán:

### 3.1 Ngụy biện: "Lệnh Sửa (Edit) có thể vào DB trước khi Tin nhắn được Tạo (Create)"
- **Nhận định sai (GE):** Cảnh báo rằng nếu mạng đảo thứ tự, thao tác Edit có thể đến Core và ghi vào MongoDB trước khi thao tác Create kịp lưu.
- **Bác bỏ thực tế:** Thao tác Edit bắt buộc phải có `msg_id` và `seq`. Theo giao thức của client chatim: Client chỉ có được `seq` sau khi nhận được Write Acknowledgment (`w:majority`) của thao tác gửi tin. Do đó, về mặt nhân quả (*causal dependency*), thao tác lưu DB của Edit **không bao giờ** có thể diễn ra trước thao tác Create trong DB.
- **Điểm đúng duy nhất:** Chỉ có **Sự kiện (Events)** trên message bus (JetStream) là có thể bị trễ hoặc đảo thứ tự đến các Consumer bất đồng bộ. Điều này được giải quyết bằng cách: Event biến đổi luôn mang theo snapshot trạng thái đầy đủ hoặc consumer thực hiện upsert theo version logic.

### 3.2 Ngụy biện: "Giao dịch Single-Shard vì chung Shard Key"
- **Nhận định sai (GE):** Đề xuất bọc thao tác ghi vào `messages` và `message_edits` trong một MongoDB Multi-document Transaction với lý do "hai collection cùng dùng chung Shard Key `{room_id: 1}` nên là transaction cục bộ trên 1 shard, không tốn chi phí".
- **Bác bỏ thực tế:** Dù hai collection có cùng shard key pattern, MongoDB Sharded Cluster phân bổ các chunk của các collection khác nhau một cách hoàn toàn độc lập. Chunk chứa room $X$ của `messages` hoàn toàn có thể nằm ở Shard 1, trong khi chunk chứa room $X$ của `message_edits` lại nằm ở Shard 2. Do đó, đây vẫn là một Distributed Transaction đa node với giao thức 2-Phase Commit (2PC) đắt đỏ, vi phạm nghiêm trọng nguyên tắc **Shard-Readiness (D13)**.

### 3.3 Ngụy biện: "ClusterTime của MongoDB là Đơn Điệu Tuyệt Đối qua Failover"
- **Nhận định sai:** Dùng `clusterTime` của MongoDB làm hàng rào logic thời gian (time guard) tuyệt đối để xác định tính hợp lệ của sự kiện.
- **Bác bỏ thực tế:** Khi Primary node bị crash và Replica Set tiến hành bầu cử (election), nếu node mới lên làm Primary có sự chênh lệch nhỏ về clock hoặc phân bổ timestamp trong Oplog chưa hoàn tất nhân bản, các giả định về thời gian vật lý/hybrid logic có thể bị xâm phạm.
- **Giải pháp:** Loại bỏ toàn bộ sự phụ thuộc vào time guard. Mọi trạng thái phân tán chỉ dựa trên **Sequence Counter tuyến tính theo Room** (`seq`), **CAS Version** (`ver`), và **ID tự nhiên bất biến** của fact.

### 3.4 Ngụy biện: "Coi Hole cũ hơn 5 giây là Void (Vô hiệu)"
- **Nhận định sai ban đầu (D13):** Nếu phát hiện khoảng trống sequence (ví dụ: client nhận seq 10 rồi seq 12, thiếu seq 11), sau 5 giây nếu không thấy seq 11 thì coi như seq 11 bị void (bị huỷ) và bỏ qua.
- **Bác bỏ thực tế:** Việc sequence nhảy cóc có thể do tin nhắn bị filter ở tầng server (ví dụ tin nhắn vi phạm chính sách kiểm duyệt bị loại bỏ trước khi ghi, hoặc tin nhắn ở nhánh retry). Client không thể tự suy diễn rằng tin nhắn bị mất mạng hay bị void chỉ bằng cách đếm số. Giải pháp là huỷ bỏ hoàn toàn luật void 5s; tính toàn vẹn của stream do Reader và Reconciler đảm bảo tại server.

---

## 4. Đặc Tả Cơ Chế Hệ Thống Đã Chuẩn Hoá (System Mechanisms Specification)

Dưới đây là các cơ chế cốt lõi đã được phê duyệt và đưa vào bản thiết kế chính thức của `chatim`:

```
                       ┌──────────────────────────────────────────────┐
                       │           CORE PIPELINE ARCHITECTURE          │
                       └──────────────────────────────────────────────┘
                                        
 [Client SDK] ──1. Send Message (CID)──► [Gateway] ──2. gRPC (Hash Slot)──► [Core Actor]
                                                                                │
      ▲ 6. Ack (seq, cid)                                      3. Atomic Inc Seq & Insert
      │                                                                         │
      ├─────────────────────────────────────────────────────────────────────────┼──────────┐
      │                                                                         ▼          │
 [Client Local Store]                                                   [MongoDB Primary]  │
                                                                         (messages, facts) │
                                                                                │          │ 4. Local JS
                                                                          Oplog │             Publish
                                                                                ▼          ▼
                                                                        [Slot 0 CDC Reader]
                                                                                │
                                                                    Work Stream │ (Partitioned)
                                                                                ▼
                                                                        [Worker Reconciler]
                                                                                │
                                                                       Broadcast│ (live.*)
                                                                                ▼
                                                                            [Gateway]
                                                                                │
                                                                   5. Realtime WS Push
                                                                                ▼
                                                                       [Other Subscribed SDKs]
```

### 4.1 Cơ chế Chống trùng Lặp và Chuẩn hóa Thứ tự Ghi (Idempotency & Sequence Invariant)

1. **Khóa chống trùng (Client Message ID - CID):**
   - Mọi tin nhắn gửi từ client bắt buộc đính kèm UUID `cid`.
   - Core duy trì LRU cache bộ nhớ và unique index `{room_id: 1, cid: 1}` trên MongoDB.
   - Nếu client bị mất kết nối và retry lại tin nhắn mang cùng `cid`: Hệ thống phát hiện trùng lặp ngay tại tầng Core, không cấp `seq` mới, và trả về đúng bản ghi `seq` đã lưu trước đó.
2. **Tin nhắn Hệ thống (System-generated Messages):**
   - Không dùng UUID ngẫu nhiên mà sử dụng **Deterministic CID**:
     $$\text{CID}_{\text{sys}} = \text{"sys:"} + \text{source\_event\_id}$$
   - Cơ chế này cho phép tái sử dụng 100% đường ống chống trùng lặp tự nhiên mà không cần bổ sung bảng dữ liệu riêng.
3. **Chiến lược Phản hồi Xác nhận (Write Ack Strategy):**
   - Đối với lệnh gửi tin thông thường (Type A1): Ack ngay sau khi ghi thành công vào Fact/Messages với `w:majority`.
   - Đối với lệnh sửa tin / xoá tin: **Chỉ trả Ack sau khi hoàn tất cập nhật vào Projection** (chấp nhận thêm 1 Round-trip độ trễ mạng để đổi lấy tính nhất quán tức thì tuyệt đối: *Read-Your-Own-Writes*).
   - Xử lý Retry an toàn: Client gửi kèm `base_ver`. Nếu gặp lỗi trùng khóa (`duplicate key`), Core kiểm tra nếu tác giả và nội dung trùng khớp thì coi như thành công và trả về trạng thái hiện tại.

### 4.2 Đường ống Bắt Thay đổi Dữ liệu (CDC Reader Pipeline - Topology a)

Để đưa dữ liệu từ cơ sở dữ liệu sang NATS JetStream phân phối thời gian thực, hệ thống chốt mô hình **Topology (a)**:

```
[MongoDB Replica Set] 
       │ (Single Change Stream Cursor)
       ▼
 [Core Node (Slot 0 Reader)]  <── Khóa bầu chọn phân tán (Lease-based Leader Election)
       │
       ├─► Extract Natural Fact ID (Nats-Msg-Id: "evt:" + fact_id)
       │
       ▼
[JetStream CHATIM_WORK Stream] 
       │ (Phân vùng theo Room Slot ID)
       ├─────────────────────────┬─────────────────────────┐
       ▼                         ▼                         ▼
 [Core Worker Slot 1]      [Core Worker Slot 2]      [Core Worker Slot N]
 (Xử lý Projection)        (Xử lý Counter/Badge)     (Phát sóng live.*)
```

- **Một Reader Duy Nhất:** Chỉ có Core node sở hữu Slot 0 mở cursor lắng nghe Change Stream từ MongoDB Oplog. Tránh tình trạng mở N stream gây cạn kiệt tài nguyên replica set.
- **Checkpoint Bền vững bằng ID Tự nhiên:** Vị trí xác nhận (Ack) không phụ thuộc vào chuỗi `resume_token` bí truyền của Mongo (vốn dễ bị vô hiệu hóa khi nâng cấp phiên bản server), mà sử dụng chính ID tự nhiên của Fact được ghi nhận vào NATS JetStream.
- **Dung lượng Oplog & Kế hoạch Phục hồi:**
  - Ở mức tải đỉnh $10.000\text{ tin/s}$, dung lượng Oplog sinh ra ước tính $0,9 – 1,7\text{ TB/ngày}$.
  - Cấu hình Oplog retention tối thiểu 48–72 giờ.
  - Xây dựng công cụ **Resync có kiểm soát (Scoped Rate-limited Resync Tool)**: Quét lại theo bảng `room_activity` với tốc độ xả backlog $\ge 3\times$ tốc độ ingest thực tế.

### 4.3 Đồng bộ Kết nối sau Thảm họa (Reconnect Storm & Sync Token)

Khi 100.000 client đồng loạt kết nối lại (sau khi mạng phục hồi hoặc gateway khởi động lại), việc truy vấn DB để lấy tin nhắn sót sẽ tạo ra hiệu ứng bầy đàn (*Thundering Herd*) làm tê liệt hệ thống.

```
       [Client Reconnecting]
                 │
                 │ 1. Gửi Sync Token: { room_A: seq_150, room_B: seq_80 }
                 ▼
             [Gateway]
                 │
                 ├─► 2. Kiểm tra Tail-Cache trong RAM (50 tin gần nhất / room)
                 │         │
                 │         ├─► [Cache Hit]: Trả về ngay mảng tin nhắn [seq_151..seq_155]
                 │         │
                 │         └─► [Cache Miss]: Fallback query MongoDB có chặn Limit
                 │
                 └─► 3. Trả về Room List & Badge
                           (Áp dụng R17: Tính Lazy Unread với Bound S)
```

1. **Cấu trúc Sync Token dạng Vector:**
   - Bác bỏ Token thời gian (`timestamp`). Vì tin nhắn đến bất đồng bộ, một tin có timestamp $t_0$ có thể commit vào DB sau $t_1$. Nếu client sync theo $t_1$, tin nhắn tại $t_0$ sẽ bị bỏ sót vĩnh viễn.
   - Chuẩn hoá: Sync Token là bản đồ rút gọn:
     $$\text{Token} = \text{Map}\langle \text{RoomID}, \text{LastReceivedSeq} \rangle$$
   - Giới hạn TTL của token là 7 ngày. Nếu client ngắt kết nối quá 7 ngày $\to$ Yêu cầu Full Sync toàn bộ phòng.
2. **Tính toán Chưa Đọc (Unread Invariant - Quy tắc R17):**
   - Loại bỏ hoàn toàn việc duy trì counter unread lưu sẵn trong database cho từng user/room.
   - Khi render danh sách phòng chat, server thực hiện lazy scan:
     $$\text{Count } \text{messages WHERE } room\_id = R \text{ AND } seq > read\_seq \text{ LIMIT } (S + 1)$$
   - **Quy tắc biên $S$:**
     - Nếu số lượng đếm được $\le S$ (ví dụ $S=100$): Trả về giá trị chính xác tuyệt đối.
     - Nếu số lượng đếm được $> S$: Trả về cận dưới ($100+$) kèm cờ `approx: true`, sau đó đẩy kết quả chính xác xuống qua kênh bất đồng bộ. Điều này bảo vệ MongoDB không bao giờ bị scan toàn bảng khi người dùng có hàng chục ngàn tin chưa đọc.

---

## 5. Bảng Quyết Định Đồng Thuận & Ma Trận Đánh Đổi

### 5.1 Bảng Tổng Hợp Lập Trường & Phán Quyết của Owner

| Chủ đề Kỹ thuật | Quan điểm Phản biện CL | Quan điểm Phản biện GE | Phán quyết Cuối cùng của Owner | Cơ sở Khoa học & Thực tiễn |
|---|---|---|---|---|
| **Cập nhật Bộ đếm** | Bác bỏ `$inc` ở fast-path; Chỉ dùng recount CAS-ver gom theo target | Giữ `$inc` ở fast-path, gom lệnh qua slot router | **Theo CL: Recount CAS-ver** | `$inc` và recount không giao hoán; mạng trễ gây sai lệch vĩnh viễn |
| **Xác nhận Sửa/Xoá** | Ack sau khi hoàn tất projection | Ack ngay sau fact, chiếu bất đồng bộ | **Ack sau Projection** | Ưu tiên tính nhất quán tức thì (Read-your-own-writes); triệt tiêu race condition ở client |
| **Trạng thái Mềm** | Ephemeral qua NATS Core | Chuyển qua slot router của Core | **Ephemeral qua NATS Core** | Typing/Presence không cần lưu trữ hay sắp thứ tự nghiêm ngặt; giảm tải cho Core |
| **Vị trí Checkpoint Reader** | Đánh dấu theo fact_id đã ack ở Work Stream | Dùng `resume_token` của Mongo Change Stream | **Theo ID Tự nhiên của Fact** | Độc lập hoàn toàn với định dạng internal token của MongoDB qua các bản nâng cấp |
| **Cơ chế Sync Token** | Map vector `{room -> last_seq}` | Token thời gian `{t}` + fallback full sync | **Theo CL + Fallback của GE** | Token thời gian làm lọt tin nhắn; Vector map bảo đảm tính toán vẹn nhân quả |
| **Xử lý Tin bị Ẩn (`hidden`)** | Đặt `cleared_before_seq` trên member doc + bảng phụ | Nhúng (embed) danh sách msg_id bị ẩn vào member doc | **Theo CL: Bảng phụ & Ngưỡng seq** | Nhúng mảng vào document sẽ làm document phình to vượt giới hạn 16MB của Mongo |

### 5.2 Ma trận Đánh đổi Hệ thống (System Trade-offs)

```
                       [TÍNH NHẤT QUÁN CAO]
                                ▲
                                │   ★ chatim (Lựa chọn)
                                │   - Ack sau Projection
                                │   - Recount CAS-ver
                                │   - Linear per-room seq
                                │
  [CHI PHÍ/PHỨC TẠP CAO] ──────┼────── [TỐC ĐỘ GHI CỰC ĐẠI]
                                │   - Fire-and-forget
                                │   - $inc bất chấp
                                │   - Bỏ qua Read-your-own-writes
                                ▼
                      [NHẤT QUÁN YẾU / EVENTUAL]
```

- **Đánh đổi 1: Thêm 1 RTT khi Sửa/Xoá $\leftrightarrow$ Triệt tiêu xung đột UI.**  
  Việc chờ cập nhật projection trước khi trả ack cho lệnh sửa/xoá làm tăng độ trễ thêm ~10–15ms, nhưng loại bỏ hoàn toàn hiện tượng client reload lại thấy nội dung cũ (stale read).
- **Đánh đổi 2: Tốn CPU tính Lazy Unread $\leftrightarrow$ Triệt tiêu Deadlock/Write Amplification.**  
  Không ghi nhận unread counter vào DB khi có tin mới giúp giảm $10.000$ write/s phân tán xuống các member doc. Đổi lại, tầng đọc chấp nhận chi phí scan có giới hạn ($S=100$) trên chỉ mục `seq`.

---

## 6. Kế Hoạch Đo Lường Thực Nghiệm (PoC Validation Plan)

Toàn bộ các giả định và cơ chế lý thuyết trên phải vượt qua 7 bài kiểm thử định lượng trên môi trường giả lập phần cứng sản xuất (3 node MongoDB NVMe, cụm NATS JetStream 3 node):

```
┌────────────────────────────────────────────────────────────────────────────────────────┐
│                        DANH MỤC 7 BÀI ĐO THỰC NGHIỆM BẮT BUỘC                          │
├────┬───────────────────────────────────┬────────────────────────────┬──────────────────┤
│ #  │ Hạng mục Kiểm thử                 │ Chỉ số Mục tiêu (Target)   │ Ngưỡng Cảnh báo  │
├────┼───────────────────────────────────┼────────────────────────────┼──────────────────┤
│ 1  │ Tốc độ đếm trên Index (Count/s)   │ > 50.000 ops/s             │ < 15.000 ops/s   │
│ 2  │ Xung đột ghi Document nóng (CAS)  │ Retry rate < 2% @ 500 rps  │ Retry rate > 5%  │
│ 3  │ Độ trễ Hop Work Stream (JetStream)│ p99 < 8ms                  │ p99 > 20ms       │
│ 4  │ Thông lượng Cursor Change Stream  │ Xả backlog ≥ 3× Ingest     │ Backlog tăng dần │
│ 5  │ Tải Quét Đọc lúc Reconnect Storm  │ p95 < 50ms @ 100K CCU      │ CPU Mongo > 80%  │
│ 6  │ Nhất quán `afterClusterTime`      │ Không gặp Stale Read       │ Phát hiện lệch   │
│ 7  │ Tỉ lệ Tin nhắn Bị Lọc sau `rs`    │ Tỷ lệ đúng 100%            │ Xuất hiện hole ảo│
└────┴───────────────────────────────────┴────────────────────────────┴──────────────────┘
```

---

## 7. Bài Học Thiết Kế Hệ Thống & Kết Luận (Lessons Learned & Conclusion)

Cuộc thẩm định cơ chế hệ thống của `chatim` đem lại 4 bài học nền tảng cho việc thiết kế kiến trúc phân tán hiện đại:

1. **Tính bất biến là chìa khóa của quy mô (Immutability unlocks Scalability):** Việc biến mọi đột biến trạng thái (mutations) thành các Fact bất biến giải phóng hệ thống khỏi các vấn đề deadlock, phụ thuộc database engine độc quyền, và mở đường cho kiến trúc event-driven tinh gọn.
2. **Không kết hợp toán tử gia tăng với toán tử ghi đè:** Tuyệt đối không phối hợp toán tử cộng dồn tự do (`$inc`, `INCR`) ở fast-path với cơ chế sửa sai bằng ghi đè (overwrite recount) ở background nếu không có cơ chế rào chắn phiên bản (fencing / versioning).
3. **Phản biện đa tác nhân triệt tiêu điểm mù:** Các giả định tưởng chừng hiển nhiên của nhóm thiết kế (như "transaction cùng shard key là single-shard", hay "hole 5s là void") đều bị bóc tách và chứng minh sai lầm thông qua quá trình chất vấn phản biện có cấu trúc.
4. **Đơn giản hóa đường ống CDC:** Thay vì cố gắng chia nhỏ luồng CDC phức tạp ở tầng cơ sở dữ liệu (vốn làm cạn kiệt tài nguyên replica set), hãy gom về một Single Stream Reader duy nhất và tận dụng Message Bus chuyên dụng (NATS JetStream) để phân vùng công việc.

Báo cáo nghiên cứu này xác lập nền tảng lý thuyết và cơ chế vững chắc cho toàn bộ quá trình lập trình thực thi (Implementation Phase) của `chatim`. Mọi thay đổi kiến trúc trong tương lai bắt buộc phải đối chiếu và không được vi phạm các nguyên lý bảo đảm đã được chứng minh trong tài liệu này.
