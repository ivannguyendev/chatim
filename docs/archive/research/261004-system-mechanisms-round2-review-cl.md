# Phản biện vòng 2: cơ chế hệ thống chatim (trả lời CL)

Tiếp nối `261004-system-mechanisms-round2-cl.md`. Mục 2 của tài liệu gốc (đã chấp nhận) không nhắc lại. Các câu 4.1–4.4 được trả lời trong từng mục 3.A–3.D tương ứng.

## Lập trường tóm tắt

| Mục | Lập trường |
|---|---|
| 3.A | Đồng ý hướng fact + projection. Sửa 5 chỗ: `base_ver`, bản gốc bị mất, ack sau projection, xóa riêng tư, validate pin. |
| 3.B | **Bác** `$inc` ở fast path. Giữ recount CAS-ver, fast path cũng chỉ là touch. |
| 3.C | Đồng ý phương án (a). Sửa index, phạm vi resync, và không dùng `rooms.last_seq` làm phép kiểm "có tin mới". |
| 3.D | Lazy ổn nếu có room-tail cache. Sửa token, `hidden`, badge, và có một lỗ mất tin khi reconnect. |

---

## 3.A Fact bất biến + projection (gồm 4.4)

**Đồng ý:** sửa và xóa chung không gian `ver` thì "sửa thua xóa" là kết quả của CAS, và race "history record đến sau khi xóa" của thiết kế cũ biến mất. Feed chỉ cần insert cũng là cải thiện thật.

**Sửa 1: lệnh đổi phải mang `base_ver`, desired-state thuần bị ABA.** Đây cũng là câu trả lời cho câu hỏi 2.

1. Fact cuối là v3.
2. Client gửi `edit(X)` tới core A. A insert v4=X (majority) rồi chết trước khi ack.
3. Client không thấy ack, user sửa tiếp thành Y: v5=Y.
4. Retry cũ `edit(X)` tới core B. "Fact cuối" là v5 (Y) khác X, nên B cấp v6=X. Tin quay về X vĩnh viễn, và đây là edit hợp lệ về CAS.

Với `base_ver=3`, retry thử insert v4 và gặp duplicate key. B đọc v4, thấy cùng tác giả, cùng text, nên trả thành công và không phát event. Áp tương tự cho delete và pin (`base_pv`).

**Sửa 2: bản gốc (v0) bị mất.** Fact lưu bản mới, `messages` bị projection ghi đè, còn tạo tin không ghi fact v0 (và không nên, vì thêm 1 write mỗi tin). Sau edit đầu tiên, text gốc không còn ở đâu. Fix: fact v1 mang thêm `prev` = text đọc từ `messages` lúc thắng CAS. Cách này an toàn vì chưa edit nào khác thắng được v1, nên projection còn nguyên bản gốc.

**Sửa 3 (câu 1): stale tới D chấp nhận được với edit, không với delete.**

- Fast path chết giữa insert fact và projection thì mọi người, kể cả người sửa, thấy text cũ tới D + lag.
- Với edit (hiếm): ack sau projection, tốn thêm 1 majority RT, và edit không nằm trong A1. Cửa sổ crash chấp nhận.
- Với delete-for-everyone: tin đã xóa vẫn hiện 30s+ thì không ổn. Dùng delay riêng theo effect (2–3s), đúng cơ chế đã chấp nhận ở vòng 1.
- Ngân sách: giả sử edit chiếm 1–3% tin, tức 100–300/s ở đỉnh. Mỗi edit là 1 reverse scan + 2 majority write không gom, tức thêm khoảng 200–600 RT/s so với ngân sách 1,5K RT/s của send path (suy luận từ số trong báo cáo). Cần cho đi qua flusher hoặc chấp nhận.

**Sửa 4 (câu 3): `$unset text` không phá thứ bất biến quan trọng, nhưng chưa xóa được gì thật.**

- Thứ bất biến cần giữ là khóa và event id, không phải nội dung. Miễn feed `message_edits` chỉ lấy insert thì `$unset` không sinh effect.
- Lỗ thật nằm ở chỗ khác: (a) oplog, vì insert mang full doc, tồn tại tới khi bị cuốn đi; (b) JetStream 7 ngày, vì event mang snapshot; (c) backup.
- Cần chốt hợp đồng "xóa": xóa ở lưu trữ chính cộng hủy hiển thị, còn stream và oplog là log vận hành hết hạn. Tenant cần xóa chặt thì bật chế độ event không mang text (client fetch).
- Mã hóa theo tin rồi hủy khóa thì không đáng: 5–20 tỷ khóa.

**Sửa 5: validate giới hạn 50 pin phải xảy ra trước khi commit fact.**

1. Pins thật là 50, nhưng projection (trễ do crash window) còn 49.
2. Fast path validate trên projection, insert `pin_actions` pv+1, thành pin thứ 51.
3. Effect projection (`size < 50`) từ chối. Fact nói "đã ghim", thông báo "X pinned" sinh từ fact, nhưng `rooms.pins` không có.

Quy tắc: fact đã commit thì projection không được phép từ chối. Hoặc validate bằng fold fact, hoặc bảo đảm projection đã tới pv hiện tại trước khi validate.

**Câu 4 (pre/post-image):** pre-image được ghi vào collection hệ thống riêng và bị xóa khi event tương ứng rời oplog, bất kể thời gian retention đặt thế nào. Nên nó không thể là nguồn lịch sử "giữ mãi" (A4), lại thêm write cho mỗi update và khóa chặt vào Mongo. Chỉ nên cân nhắc khi cần event trung thực cho state-sync. History và pins giữ fact.

---

## 3.B Counter (gồm 4.1)

**Bác `$inc` ở fast path.** Lập luận hội tụ của 3.B chặn được `$inc` chen giữa lúc đọc và lúc ghi của recount, nhưng không chặn được `$inc` đến sau khi recount đã đếm fact đó.

1. Fact f commit. Counter n=10, ver=7.
2. `$inc` của f bị trễ quá D (core pause, retry backoff khi failover 5–12s cộng hàng đợi). Hiếm, nhưng tương quan với sự cố.
3. Reconciler touch: đọc ver=7, đếm được 11 (đã có f), CAS thành n=11, ver=8. Đúng.
4. `$inc` trễ đến nơi: n=12, ver=9. Thừa 1, và touch cuối đã chạy xong.

Post yên tĩnh thì sai vĩnh viễn. `$inc` và recount-ghi-đè không giao hoán.

**Sửa:** fast path cũng là touch (recount, coalesce W), không `$inc`. Độ trễ UX là W + một recount (W=50ms thì cỡ vài chục ms), không phải D. `$inc` không cần cho độ trễ thấp, D chỉ là lưới an toàn. Client cộng lạc quan từ `reaction_changed`.

Chi tiết:

- **Đọc đếm** dùng `afterClusterTime` = clusterTime của change event (reconciler), hoặc operationTime của lần ghi fact (fast path). Đây đúng là việc `afterClusterTime` làm: read trả về dữ liệu thỏa cả mức read concern lẫn mốc afterClusterTime. Khác với guard-nhãn vòng 1 vì ở đây không dùng làm nhãn ghi.
- **Câu 1 (không hội tụ):**
  - Fast path giới hạn R lần. Reconciler retry không giới hạn, có jitter.
  - Sau fact cuối chỉ còn hữu hạn touch dở dang (≤ số core + 1), nên touch cuối không bị chen mãi và ghi được.
  - Target nóng không bao giờ yên, nhưng chỉ lệch tối đa một chu kỳ recount.
- **Câu 2:** `ver` vẫn cần vì là token CAS. Bỏ qua ghi khi `count == n` (không bump `ver`) là an toàn.
- **Câu 3 (500 `$inc`/s):** với recount-only, ghi tối đa 1/W nên câu hỏi tự biến mất. Mình không có số write-conflict cho hot doc, chưa kiểm chứng.
- **Chi phí recount 200K reactor:** giả định 1–2M key/s/core cho count trên range index (suy luận, cần đo), tức khoảng 100–200ms CPU mỗi recount. W=1s tốn 10–20% một core, W=5s tốn 2–4%. Muốn lag thấp hơn thì bật bucket K=16 (mỗi bucket khoảng 12,5K key).
- **Câu 4 (index):** unique `{k,u,emoji}` bắt `k` rồi `u`, nên đếm theo emoji phải quét cả `k`. Đổi thành unique `{k, emoji, u}`: vẫn unique theo (message, user, emoji), prefix `k` cho shard, đếm một emoji là range count phủ index. "Tôi đã react gì" thành ≤E tra cứu điểm (nên cap E).
- **Event id** `counts_changed` dùng `{target}-{counter}-v{ver}` thay cho T, bỏ luôn ngoại lệ P6 mình nêu ở vòng 1.
- **So với single-writer (4.1):** soft ownership có chồng chéo khoảng 1 tick nên single-writer vẫn cần CAS-ver. Nó chỉ giảm CAS fail, là tối ưu chứ không thay thế. CAS-ver ít trạng thái hơn.

---

## 3.C Resync + topo reconciler (gồm 4.2)

**Đồng ý (a).**

- Docs nói change stream không dùng được index (không tạo được index trên oplog) và khuyên tránh mở nhiều change stream đích hẹp vì ảnh hưởng hiệu năng server. Một issue riêng cho thấy filter `ns` lớn làm xử lý change stream chậm đáng kể. Suy luận: N cursor `$mod` nhân N lần phần đọc và lọc oplog.
- Lợi ích lớn hơn đề xuất nêu: work stream chuyển cửa sổ retention từ oplog sang thứ mình tự định cỡ. Báo cáo cho 1,5–10TB / 5–20 tỷ tin, tức khoảng 300–500B/tin. Ingest đỉnh khoảng 3–5MB/s, giữ 2h khoảng 22–36GB (R3 nhân 3). Khi đó RC5 chỉ còn rủi ro khi reader downtime quá cửa sổ oplog, và reader thì đơn giản, có hot standby.
- Vị trí xác nhận = work stream đã ack.
- Rủi ro secondary: theo hiểu biết (chưa trích docs), change stream chỉ trả thay đổi đã majority-committed nên rollback không làm event quay lui; secondary lag chỉ tăng độ trễ.
- Chưa có số cho hop JetStream. PoC cũ chỉ đo publish→subscriber 1,2ms, chưa phải publish ack R3. Cần đo.

**Sửa 1: index.** `{last_msg_at}` trên field đổi liên tục nghĩa là mỗi ghi activity là một delete+insert trên index (khoảng 3–5K/s nếu 3–5K phòng active, W_r=1s). Dùng `act_bucket = floor(ts/1h)` rồi index trên đó, chỉ đổi tối đa 1 lần/giờ/phòng. Scatter-gather khi shard thì chấp nhận được vì chỉ resync dùng. Phía ghi: 3–5K doc/s phải gom bulk (256/batch, khoảng 12–20 RT/s) hoặc đặt W_r=5s.

**Sửa 2 (câu 2): ba lỗ của resync.**

1. *Activity write mất cùng khoảng mất.* Core crash giữa insert và update `rooms`, đồng thời có oplog gap. Phòng đó không có tin sau đó nên trông "không hoạt động", tin đuôi không bao giờ được resync. Không đóng hẳn được bằng index. Giảm xác suất: flush coalesce khi shutdown/retire actor. Ghi rõ rủi ro còn lại.
2. *Ack mark hết TTL 1h.* Resync republish toàn bộ gap, ngoài cửa sổ 5m của JetStream nên thành duplicate thật trong stream 7 ngày. Theo số trung bình của báo cáo (khoảng 7–27M tin/ngày), gap 6h là khoảng 1,7–6,8M tin; ở đỉnh là khoảng 100M. Resync phải có phạm vi (tenant/room do consumer yêu cầu) và rate limit, không tự động toàn cục.
3. *`message_edits` không có thời gian trong khóa.* Sửa tin cũ không đổi `last_seq`, nên quét đuôi không thấy. Fix: collection fact nhỏ (1–3% volume theo giả định) được phép có index thứ cấp `{room, ts}`. Lệnh cấm secondary index của B1 chỉ cần cho `messages`. Và `rooms` cần `last_change_at` cho mọi fact, không chỉ `last_msg_at`.

**Câu 4:** không bỏ resync. Alert báo sau khi mất, resync mới là phục hồi. Với (a) nó thành đường hiếm: giữ công cụ thủ công, rate-limited, diễn tập định kỳ.

**Câu 3:** mặc định (a). Đo cursor đơn với `$match` đơn giản (op + `ns.coll`), tiêu chí xả backlog ≥ 3× ingest đỉnh.

---

## 3.D Room list + unread (gồm 4.3)

Giả định của owner: 100K user reconnect trong 60s là khoảng 1.667 user/s × 50 phòng = 83K phép kiểm "có tin mới"/s, và 16,7K phép tính unread/s (10 phòng có tin mới).

**Lỗ mất tin khi reconnect (nghiêm trọng nhất của 3.D).**

1. Tin m ack lúc t, event publish lúc t.
2. Client reconnect lúc t+2s. Gateway subscribe live subject rồi client gọi `ListMyRooms`.
3. `rooms.last_seq` (coalesce 1–5s) hoặc secondary lag chưa có m, nên phòng trông "không đổi".
4. Event của m đã publish trước khi subscribe và live subject không lưu, nên client không bao giờ nhận m cho tới tin sau.

"Subscribe rồi fetch" không cứu được vì m nằm trước subscribe.

Fix: phép kiểm "có tin mới" lấy `last_seq` từ `messages` (reverse scan limit 1, nguồn thật), không từ projection coalesced. Cache theo `(room, last_seq)`, không theo TTL. Với secondary lag cỡ ms: đọc primary, hoặc "pass 2" sau khoảng 3s chỉ cho phòng có `last_msg_at` trong 10s gần nhất (tập nhỏ). Hệ quả cho 3.C: `rooms.last_seq` chỉ để sắp xếp và resync, không để quyết định client đã đủ.

**Sửa 1: token sync (câu 5).** Token thời gian sai vì event best-effort: "đã thấy tới T" không còn đúng.

1. Event m (ts=185) mất, client giữ token 190.
2. Activity write mất, reconciler sửa lúc 215 với `last_msg_at=185` (lấy từ ts của doc).
3. Query `> 190` bỏ sót m.

Token đúng là map `room → last_seq client thực sự có` (50 × khoảng 16B, tức khoảng 1KB). Server so với `last_seq` thật. Cách này idempotent và không phụ thuộc đồng hồ. Có thể kèm `cv` (change version) để biết delete/edit ảnh hưởng unread.

**Sửa 2: room-tail cache (câu 4).** Phần đắt (đọc đuôi) không phụ thuộc người dùng. Cache cửa sổ khoảng 300 tin `{seq, sender, flags, deleted}` theo `(room, last_seq)`, khoảng 10KB/phòng, 50K phòng nóng khoảng 500MB. Phần theo người dùng (rs, tin của mình, hidden) là lọc bộ nhớ. Tải DB giảm từ O(user × phòng) xuống O(phòng có tin mới). Với cách này in-app không cần projection unread có version.

**Badge push:** N người nhận × 50 phòng thì không thể exact. R17 cần chốt chỉ áp cho room list. Badge do push component giữ xấp xỉ (tăng khi gửi, reset khi đọc), khớp với P3 (delivery là việc của component khác).

**Sửa 3: `hidden` (câu 2, 4.3).**

- Tách `cleared_before_seq` (watermark trên member doc, cho "clear history") + bản ghi `hidden` thưa.
- Unread chỉ cần hidden với `seq > max(rs, cleared_before)`. Lấy cho cả lô phòng bằng một query `{u, r ∈ R, seq > min rs}`, không range scan từng phòng. Hidden ≤ rs bỏ qua, nén được.
- Đây là tra cứu điểm hữu hạn (≤ khoảng 200), không phải join vào collection lớn. Nên sửa chữ P2 thành "cấm join không giới hạn".

**Sửa 4: "phòng của tôi" cần truy cập theo user.** `members` khóa theo room. Giả sử 10M user × 50 phòng thì có khoảng 500M member doc, cần index `{u, r}`, và scatter-gather khi shard. Nên có `user_rooms` khóa `{u│r}`, shard theo user, là projection từ fact member (thêm một effect).

**Câu 3 (chạm S):** trong ca xấu (hàng nghìn tin xóa/ẩn/không đếm sau rs) thì không thể vừa exact vừa bị chặn chi phí. Đề xuất R17 = "exact nếu quét xong trong S; nếu không trả cận dưới + cờ `approx`, hoàn tất bất đồng bộ rồi đẩy cập nhật". Giảm ca xấu: SDK nâng rs khi gửi tin, nên tin của chính mình không tích tụ sau rs. Cần đo phân phối tỉ lệ tin không đếm sau rs.

**Câu 1 (tải storm):** khoảng 83K seek + 16,7K tail fill + query hidden theo lô, tức vài chục nghìn đến khoảng 100K thao tác đọc nhỏ/s trong 60s. Khả thi trên 3 member nếu đọc secondary, nhưng sát nếu cache lạnh (suy luận, chưa đo). Cách rẻ nhất để hạ: SDK reconnect jitter trải 60s ra 5 phút, giảm khoảng 5 lần.

---

## Điểm chưa chắc

1. **`operationTime` của majority read:** lần tìm này chưa giải quyết được nghi vấn mình nêu ở vòng 1. Các nguồn tìm được chỉ nói về `afterClusterTime` như cận dưới của read. Cách dùng ở 3.B (làm cận dưới, không làm nhãn ghi) không phụ thuộc nghi vấn đó. Việc clusterTime của change event luôn dùng được làm `afterClusterTime` mình chưa kiểm chứng riêng.
2. **Mọi con số hiệu năng** là suy luận, cần đo: key/s của count trên index, read/s mỗi node, hot-doc write conflict, publish ack R3 của JetStream.
3. **Change stream chỉ trả thay đổi đã majority-committed** là hiểu biết chung, chưa trích docs.
4. **JetStream xóa/purge theo sequence** (cho bài toán xóa riêng tư ở 3.A) chưa kiểm chứng.
5. **Giả định đầu vào:** edit 1–3% tin, 3–5K phòng active, 50 phòng/user, 500M member doc. Đều cần số thật từ owner.
6. **Oplog chứa text tới khi bị cuốn đi** là suy luận trực tiếp (insert mang full doc), chưa trích.
