# Phản biện vòng 2 — Các điểm cần thảo luận & phản biện tiếp (từ GE)

> Ngày: 2026-10-05.  
> Tài liệu tham chiếu: `261004-system-mechanisms-round2-ge.md`, `261004-system-mechanisms-round2-cl.md`.  
> Mục đích: Tổng hợp các điểm đã thống nhất và nêu rõ **4 vấn đề kỹ thuật chuyên sâu cần tiếp tục phản biện và thảo luận** với reviewer / owner trước khi chốt spec hoàn toàn.

---

## 1. Tóm Tắt Các Điểm Đã Thống Nhất (Sẵn Sàng Chốt Spec)

1. **Fact bất biến + Projection (3.A):** Thống nhất dùng mô hình `message_edits` fact + projection `messages`. Phương án này ưu việt hơn MongoDB Pre/Post-Image vì chỉ tốn thêm I/O cho 1%–3% tin nhắn có sửa/xoá, không làm phình gấp đôi dung lượng oplog và không bị khóa chặt vào MongoDB 6.0+.
2. **Counter Reaction (3.B):** Giữ `$inc` ở fast path để đảm bảo UX tức thì. Dùng aggregation `$group` trên target `k` để đếm tất cả emoji trong một lần scan duy nhất, tránh việc scan lặp lại nhiều lần.
3. **Topo Reconciler (3.C):** Chọn **Topo (a)** (1 Reader đọc change stream đẩy vào JetStream work-queue phân vùng theo slot). Phương án này loại bỏ hoàn toàn việc MongoDB Primary phải scan oplog $N$ lần như phương án N stream `$mod`. Tăng retention của oplog lên 48–72h để đảm bảo an toàn vận hành, không cần hệ thống resync tự động phức tạp.
4. **Đường đọc & Unread (3.D):** Unread **bắt buộc tính Lazy lúc đọc** với query `limit(101)`. Bác bỏ hoàn toàn projection unread eager (vì sẽ sinh ra hàng triệu write updates/s ở nhóm lớn). Embed danh sách `hidden` nhỏ trực tiếp vào `members` doc để tiết kiệm 50% số lượng network query.

---

## 2. Bốn Điểm Cần Phản Biện & Thảo Luận Tiếp

### Điểm 1: Đánh đổi ACK của lệnh Sửa — Chờ Projection (2 RTT) hay chỉ chờ Fact (1 RTT)?

- **Bản chất vấn đề:**
  - Ở Vòng 2, có đề xuất: *Client ACK của lệnh Sửa chỉ trả về sau khi Projection trên `messages` đã cập nhật xong*.
  - Tuy nhiên, điều này buộc luồng Edit phải thực hiện **2 round-trips mạng tuần tự** tới MongoDB với `writeConcern: majority`:
    1. `insertOne` vào `message_edits` (Fact).
    2. `updateOne` vào `messages` (Projection).
  - Độ trễ của lệnh Edit sẽ tăng lên **20–40ms** (gấp đôi so với Send chỉ tốn 1 round-trip gom batch ~5–10ms).
  - **Kịch bản lỗi biên:** Nếu Fact đã ghi thành công nhưng lệnh update Projection bị timeout mạng, Core trả lỗi cho Client. Client retry sẽ gặp lỗi `Duplicate Key` vì version đó đã tồn tại trong DB!

- **Hai phương án lựa chọn để thảo luận:**
  - **Phương án A (Ưu tiên Latency & Phù hợp Nguyên tắc P4):**
    - Trả ACK cho Client ngay sau khi Fact được ghi thành công (1 RTT ~10–15ms).
    - Lệnh update Projection được gửi bất đồng bộ ở Fast-path (ngay trong memory/goroutine của Core) và Reconciler là lưới bảo hiểm sau 3s–5s.
    - *Đánh đổi:* Nếu Core chết đúng lúc sau Fact insert, người gửi thấy sửa thành công nhưng người nhận có thể thấy text cũ trong tối đa 3s (cho đến khi Reconciler chạy).
  - **Phương án B (Ưu tiên Tính Nhất Quán Tuyệt Đối):**
    - Chấp nhận 2 RTT tuần tự, client chỉ nhận ACK khi cả Fact và Projection đều đã nằm trên đĩa MongoDB.
  - 👉 **Đề xuất từ GE:** Chọn **Phương án A**. Trong domain Chat, latency p99 < 20ms của thao tác sửa quan trọng hơn; cửa sổ trễ 3s chỉ xuất hiện khi Core crash (xác suất < 0.01%), hoàn toàn nằm trong dung sai chấp nhận được của P4.

---

### Điểm 2: Định tuyến Reaction — Đi thẳng DB hay đi qua Slot Router để gom `$inc`?

- **Bản chất vấn đề:**
  - Vòng 2 đã chỉ ra 500 reaction/s vào một bài post nóng sẽ gây ra hiện tượng *lock thrashing* và CPU 100% trên WiredTiger do optimistic concurrency control. Bắt buộc phải có **In-Memory Coalescing Buffer** ở tầng Core (cửa sổ 50ms–100ms).
  - Nhưng nếu Reaction **bỏ qua Slot Router và đi thẳng DB** (bất kỳ Core nào cũng nhận request cho bất kỳ room nào):
    - Khi có 4 Core, 500 reaction/s sẽ bị chia nhỏ: Core 1 gom được 2 like, Core 2 gom được 3 like, Core 3 gom được 2 like...
    - Hiệu quả gom batch bị phân tán (diluted), MongoDB vẫn phải chịu 40–80 writes/s trên cùng một document.

- **Đề xuất thảo luận:**
  - **Chỉ định tuyến theo Slot đối với các lệnh ghi tải cao (High-Frequency Writes):**
    - Reaction và Typing/Presence bắt buộc phải đi qua **Slot Router** về Core sở hữu slot của phòng đó.
    - Toàn bộ 500 reaction/s dồn vào **1 buffer duy nhất** trên Core sở hữu slot, gom thành đúng 10–20 lệnh `$inc: {heart: +25}` mỗi giây vào MongoDB.
    - Các lệnh tải thấp (Edit, Delete, Pin) vẫn có thể đi thẳng DB mà không cần qua Slot Router.

---

### Điểm 3: Cơ chế Checkpoint & Khử trùng của Topo Reconciler (a)

- **Bản chất vấn đề:**
  - Trong Topo (a), 1 Reader nhẹ chạy trên Core giữ Slot 0 để tail change stream và đẩy vào JetStream work-queue.
  - Reader cần lưu `resumeToken` định kỳ vào collection `reconciler_state` trên MongoDB.
  - Nếu Reader lưu checkpoint mỗi 1 giây: Khi Core Slot 0 bị crash, Core mới nhận quyền sở hữu Slot 0 sẽ đọc checkpoint cũ và tail lại oplog từ 1 giây trước.
  - Khoảng 1 giây dữ liệu trùng lặp sẽ bị đẩy vào JetStream work-queue.

- **Đề xuất giải pháp kỹ thuật:**
  - Cấu hình JetStream Work Stream với `Nats-Msg-Id: "change:" + resume_token_string`.
  - JetStream có cơ chế deduplication tích hợp: trong cửa sổ 5 phút, mọi event từ đoạn replay 1s nói trên sẽ bị JetStream âm thầm loại bỏ, **không bao giờ bị đẩy xuống Worker Pool lần thứ hai**.
  - Tần suất lưu checkpoint của Reader: Đặt an toàn ở mức **mỗi 1 giây hoặc mỗi 1.000 changes** (bằng 1 lệnh `updateOne` có điều kiện không lùi token).

---

### Điểm 4: Ngưỡng Hết Hạn của Sync Token (Delta Sync vs Full Sync)

- **Bản chất vấn đề:**
  - Mục 3.D đã thống nhất khi Client reconnect sẽ gửi `sync_token = {t: unix_epoch_ms}`. Server chỉ truy vấn những phòng mà user tham gia có `last_msg_at > t`.
  - Nếu một client offline quá lâu (ví dụ 1–3 tháng không mở ứng dụng): Việc chạy query delta qua hàng trăm phòng có thể tốn kém CPU hơn và dữ liệu trên máy client đã quá cũ.

- **Đề xuất quy chuẩn Sync Protocol:**
  - Quy định thời hạn tối đa (**TTL**) của Sync Token là **7 ngày** (khớp với thời gian lưu trữ 7 ngày của stream JetStream).
  - **Nếu `now - token.t <= 7 ngày`:** Thực hiện **Delta Sync** (chỉ trả về danh sách các phòng có tin mới và unread count tương ứng).
  - **Nếu `now - token.t > 7 ngày` (hoặc token không hợp lệ):** Server trả về mã `SYNC_TOKEN_EXPIRED`. Client SDK tự động chuyển sang chế độ **Full Sync** (tải danh sách 50 phòng hoạt động gần nhất qua phân trang `ListMyRooms`).

---

## 3. Kết Luận & Khuyến Nghị Hành Động

Nếu Reviewer / Owner thống nhất với 4 đề xuất trên:
1. **Edit ACK:** Chọn Phương án A (ACK sau Fact, Projection chạy async).
2. **Reaction:** Bắt buộc định tuyến qua Slot Router để buffer gom `$inc` tập trung.
3. **Reconciler Checkpoint:** Dùng JetStream dedupe theo `resume_token` để xử lý handover reader an toàn.
4. **Sync Token:** Giới hạn TTL 7 ngày cho Delta Sync, quá 7 ngày chuyển sang Full Sync.

Toàn bộ khung kiến trúc coi như đã **hoàn chỉnh 100%**, sẵn sàng chuyển sang bước viết Implementation Plan chi tiết cho milestone **M2b.0** và **M2b.1**.
