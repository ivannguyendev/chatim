# Phản biện vòng 2 — cơ chế hệ thống chatim (gửi GE)

> Ngày: 2026-10-04. Tiếp nối báo cáo `261004-system-mechanisms-report.md` và bản phản biện vòng 1 của bạn.
> Hai reviewer nhận **cùng** mục 0–3 để so sánh câu trả lời; mục 4 chỉ dành cho bạn. Từ vòng 1 của bạn, các ý reader pipeline, phân loại lệnh, unread lazy, cid tất định cho tin hệ thống và sync token đã được nhận. Mục 4 gồm các khẳng định cần dẫn chứng và các đề xuất của reviewer kia cần bạn phản bác.

## 0. Cách trả lời

- Với mỗi điểm: **đồng ý / bác / sửa**, kèm kịch bản thất bại cụ thể (chuỗi bước, trạng thái từng bên, kết quả sai) và số liệu ở quy mô thật: 10K tin/s đỉnh, 100K kết nối, nhóm 5K, channel 200K, 5–20 tỷ tin.
- Khẳng định về hành vi MongoDB hoặc NATS cần trích docs; nếu là suy luận thì ghi rõ "suy luận".
- Không cần nhắc lại mục 2 (đã chấp nhận).
- Cuối bài: danh sách điểm bạn chưa chắc.

## 1. Bối cảnh ngắn

- `messages` là clustered collection, `_id` = `room│thread│seq` (big-endian), **không có secondary index**. Mọi query có prefix room. Không transaction nhiều doc. Mongo là replica set 3 member chưa shard, nhưng phải shard được chỉ bằng cấu hình (shard key tương lai `{_id: 1}`).
- Room actor cấp seq trong RAM; flusher gom `insertMany(w:majority)`; unique `_id` làm CAS. Ack trả sau insert.
- Event best-effort lên JetStream, `Nats-Msg-Id` = id tự nhiên, cửa sổ chống trùng 5m. Reconciler (một instance, trên core giữ slot 0) đọc change stream của `messages`, chờ D = 30s, publish bù tin chưa có ack mark trên Redis (bitmap, TTL 1h).
- Yêu cầu liên quan: thứ tự trong timeline giống nhau cho mọi người, không cần nhân quả (R5); client không phát hiện mất event bằng cách đếm, reconnect thì lấy trạng thái mới nhất (R10); unread chính xác, chặn 99+, chỉ đếm tin của người khác, chưa xoá, chưa ẩn với tôi, kind có cờ đếm (R17); tin và lịch sử sửa giữ mãi (A4); member đọc được lịch sử sửa, tenant có thể tắt (A7).

## 2. Đã chấp nhận từ vòng 1 (không bàn lại)

- Khung chính: bảng **lớp dữ liệu** (fact bất biến; state có version; tập (target, user); aggregate theo target; giá trị theo người đọc; tần suất cao gộp được; ephemeral) + **reader pipeline** dùng chung cho mọi API đọc (quyền, ẩn, mặt nạ xoá) + **registry effect** với chính sách khai báo theo từng effect (delay, coalesce, có ack mark hay không, durable hay ephemeral).
- Mỗi tính năng khai báo lớp dữ liệu và ngân sách khuếch đại (write, read, lookup, event mỗi thao tác ở nhóm 5K, channel 200K).
- Mỗi guarantee có detector + alert ngay khi định nghĩa.
- Unread tính lazy lúc đọc, không eager trên đường ghi.
- Event thay đổi mang đủ trạng thái hiện tại; receiver upsert theo version.
- Tin hệ thống dùng cid tất định `sys:{event_id}`.
- Effect không hội tụ (push, tin hệ thống, webhook) idempotent ở consumer theo event id.
- `RECONCILE_DELAY` thành tham số theo effect. Bỏ luật "hole cũ hơn 5s là void".
- CD2, CD3 chấp nhận; gộp phía đọc theo (sender, cid); alert chế độ degraded.
- Chưa cần fencing; actor tự rút khi va doc của core khác, retry có backoff + jitter.
- Typing/presence ephemeral qua NATS core; vị trí đọc là upsert đơn điệu + event coalesce.
- Không dùng transaction nhiều doc (kể cả "single-shard": hai collection shard độc lập).

## 3. Bốn đề xuất cần phản biện

### 3.A Sửa / xoá / ghim: fact bất biến + projection

**Đề xuất**

1. Sửa = insert fact `message_edits {_id: room│th│seq│ver, kind: "edit", text, by, ts}` chứa **bản mới** (thiết kế cũ lưu bản bị thay thế). Unique `_id` chính là CAS theo version: hai lệnh sửa cùng `ver` thì một bên nhận duplicate key → trả conflict kèm trạng thái hiện tại.
2. Xoá cho mọi người = insert cùng không gian khoá với `kind: "delete"`. Sửa và xoá tranh **cùng một** `ver`, nên "sửa thua xoá" là kết quả của CAS, không cần code dọn riêng.
3. Cấp `ver` và kiểm quyền (đúng tác giả, chưa bị xoá) đọc từ **fact cuối** trong range của tin (một reverse scan trên clustered key), không đọc từ projection.
4. `messages` là **projection**: effect `updateOne({_id: msg, v: {$lt: ver}}, {$set: {text, v: ver, d}})`. Fast path chạy ngay sau insert fact; reconciler chạy lại từ feed insert của `message_edits` (cùng cơ chế mark + D như tạo tin).
5. Event `msg_edited` / `msg_deleted` có id `{room}-{th}-{seq}-v{ver}`, mang snapshot.
6. Lịch sử sửa = range scan `message_edits` theo prefix tin.
7. Ghim = insert `pin_actions {_id: room│pv, op: pin|unpin, target, by}`; projection `rooms.pins` + `rooms.pv` với `where pv < v`. "A đã ghim" sinh từ fact, nên A ghim rồi B bỏ ghim ngay vẫn có đủ hai thông báo.
8. Reaction và member là **tập** (target, user), có bật/tắt nên không bất biến: upsert theo khoá unique + change number `n`; event theo `n`; feed update đọc doc hiện tại (state-sync, bản mới thắng).

**Lý do:** lịch sử đúng; tin hệ thống biết ai làm gì ở từng lần đổi; feed của fact chỉ cần insert, không cần `updateLookup` hay pre/post-image; một không gian version cho sửa và xoá.

**Câu hỏi**

1. Projection bị mất ở fast path thì `GetHistory` trả text cũ tới D. Chấp nhận được không, hay đọc lịch sử phải ghép fact cuối (thêm một range scan mỗi tin đã sửa trong trang)?
2. Kiểm quyền + cấp `ver` từ fact cuối (mục 3) còn lỗ nào khi hai core cùng xử lý, có retry, hoặc failover?
3. Xoá cho mọi người phải xoá nội dung các bản sửa cũ (riêng tư). Dọn bằng `$unset text` trên fact edit cũ (giữ khoá) có phá tính "bất biến" ở chỗ nào quan trọng không?
4. So với `changeStreamPreAndPostImages` của MongoDB: cách nào rẻ hơn ở 10K tin/s, cách nào khoá chặt vào Mongo hơn?

**Phản biện tốt là:** một chuỗi lệnh đồng thời (2 core, retry, failover) làm projection hoặc lịch sử sai **vĩnh viễn**.

### 3.B Counter theo target: `$inc` ở fast path + recount CAS-ver ở reconciler

Áp cho số reaction theo emoji, số reply của thread, số member. Unread không thuộc đây.

**Đề xuất**

1. Summary nằm trên doc đích: `{n, ver}` mỗi counter.
2. Fast path, ngay sau fact: `$inc: {n: ±1, ver: 1}`. Có thể lệch khi retry gặp kết quả không rõ hoặc core chết giữa fact và `$inc`.
3. Reconciler, sau D, gom theo target (mỗi target tối đa một recount mỗi cửa sổ W):
   - đọc counter, được `ver = k`;
   - đếm fact bằng majority read, **sau** lần đọc counter, trong cùng causal session;
   - `updateOne({_id, ver: k}, {$set: {n: count}, $inc: {ver: 1}})`;
   - CAS fail (có `$inc` chen vào) → đọc lại, đếm lại, tối đa R lần; hết R thì để lần touch sau.
4. **Lập luận hội tụ:** mỗi fact sinh một touch của reconciler xảy ra sau nó; touch cuối cùng đọc sau fact cuối; CAS bảo đảm không `$inc` nào bị ghi đè giữa lúc đếm và lúc ghi. Không cần cluster time.
5. Target nóng: post viral 200K reactor → mỗi recount scan 200K khoá; W giới hạn tần suất (ví dụ 1 lần/30s). Partition `hash(user) % K`, tổng = Σ bucket, chỉ bật khi đo thấy cần.

**Câu hỏi**

1. Có kịch bản không hội tụ? Ví dụ: target nóng làm CAS fail R lần liên tiếp đúng ở touch cuối.
2. `ver++` trong `$inc` có cần không? Hay bỏ hẳn `$inc` (counter chỉ đúng sau recount, trễ D)?
3. 500 reaction/s vào một post = 500 `$inc`/s trên một doc. WiredTiger có write conflict / retry đáng kể không? Có cần gom `$inc` theo cửa sổ?
4. Unique index của reaction là `{k, u, emoji}` (bắt đầu bằng `k` cho shard). Đếm theo emoji cần thêm index nào, hay đếm cả `k` rồi group?

**Phản biện tốt là:** chuỗi thao tác cụ thể làm counter sai vĩnh viễn, hoặc ước lượng chi phí vượt ngân sách.

### 3.C Resync khi mất oplog + topo reconciler

**Bối cảnh:** reconciler theo vị trí change stream. Vị trí rơi khỏi oplog → hiện tại log lỗi và "bắt đầu lại từ bây giờ", khoảng mất là mất vĩnh viễn. `messages` không có secondary index nên không quét được "thay đổi từ thời điểm T". Oplog capped theo byte: đỉnh 10K tin/s × 1–2KB ≈ 0,9–1,7TB/ngày.

**Đề xuất**

1. Projection **room activity** trên `rooms`: `last_seq`, `last_msg_at` (trường đã có trong mô hình nhưng chưa ghi), là effect coalesce (mỗi room tối đa một write mỗi W_r, ví dụ 1s). Dùng cho hai việc: inbox (sắp room theo hoạt động) và chỉ mục resync.
2. Resync khoảng mất [T1, T2]: quét room có `last_msg_at ≥ T1` (cần index `{last_msg_at}` trên `rooms`, query không có prefix room); với mỗi room, scan ngược `messages` từ `last_seq` tới khi `ts < T1`; publish bù tin chưa có ack mark và chạy lại effect projection.
3. Topo reconciler, hai phương án:
   - (a) Một reader nhẹ đọc change stream (từ secondary), không lookup, đẩy bản ghi thô vào JetStream work stream phân vùng theo slot; worker ở mọi core chạy effect.
   - (b) N change stream, mỗi cái lọc `$mod` theo room; mỗi cursor vẫn quét toàn oplog phía server.
     Đề xuất mặc định (a), quyết định sau khi replay backlog ở PoC prod-like với tiêu chí xả backlog ≥ 3× ingest đỉnh.

**Câu hỏi**

1. Room 100 tin/s: coalesce 1s có đủ tránh hot doc? Index `{last_msg_at}` trên `rooms` (không prefix room) có chấp nhận được khi `rooms` lên hàng chục triệu doc và sau này shard?
2. Quy trình resync có lỗ không? Ví dụ: write room activity cũng mất trong khoảng đó; ack mark hết TTL 1h nên tin cũ hơn 1h bị publish lại ngoài cửa sổ chống trùng 5m của JetStream; fact `message_edits` không có thời gian trong khoá nên không quét theo thời gian được.
3. (a) hay (b) trước khi đo? Work stream thêm một hop JetStream: độ trễ và chi phí lưu thế nào?
4. `minRetentionHours` + alert lag có đủ để bỏ hẳn resync không?

**Phản biện tốt là:** kịch bản mất event vĩnh viễn sau resync, hoặc ước lượng thông lượng của từng topo.

### 3.D Room list + unread lúc reconnect storm

**Bối cảnh:** vòng 1 thống nhất unread tính lazy. Hiện đường gửi không ghi gì lên `rooms`. "Xoá phía tôi" là collection riêng `hidden`, index `{u, r, thread_root, seq}`.

**Đề xuất nháp**

1. `ListMyRooms`: đọc các member doc của user (có vị trí đọc `read_seq`, gọi tắt `rs`), lấy room activity (3.C) để sắp xếp và biết room nào có `last_seq > rs`. Chỉ room có tin mới mới cần đếm.
2. Unread một room: scan ngược `messages` từ `last_seq` về `rs`, lọc from/deleted/kind trong doc, dừng khi đủ 100 hoặc chạm giới hạn scan S (ví dụ 300 doc); `hidden` lấy bằng range scan `{u, r, thread_root, seq > rs}`.
3. Cache (user, room) → (last_seq lúc đếm, count); còn hợp lệ khi `last_seq` không đổi. Event tin mới chỉ để client tự tăng cục bộ.
4. Reconnect: client gửi sync token; server chỉ trả room có thay đổi sau token.

**Câu hỏi**

1. Owner chưa có số room trung bình mỗi user. Hãy nêu giả định (ví dụ 50 room, 10 room có tin mới) rồi tính số scan/s khi 100K user reconnect trong 60s. Replica set 3 member có chịu được không?
2. `hidden`: range scan riêng mỗi room, hay embed danh sách/bitmap nhỏ trên member doc?
3. Chạm S mà chưa về tới `rs` và chưa đủ 100 thì trả gì để vẫn "chính xác" theo R17?
4. Có cần projection unread có version (ghi khi có tin mới) thay cho lazy không? Xác nhận bằng con số.
5. Sync token tối thiểu cần mang gì?

**Phản biện tốt là:** ước lượng tải cụ thể, hoặc kịch bản unread sai.

## 4. Riêng cho GE

### 4.1 Khẳng định ở vòng 1: giữ (kèm dẫn chứng) hay rút lại

1. **"Edit vào DB trước khi tin được tạo"** (P1, Q5). Edit cần `seq`; client chỉ có `seq` từ ack; ack trả sau insert `w:majority`. Kịch bản nào làm edit tới DB trước insert? Nếu ý bạn là thứ tự **event** có thể đảo thì báo cáo đã nêu (5.2 mục 6) và mục 2 đã nhận "event mang snapshot".
2. **"Transaction single-shard vì cùng shard key"** (P2). `messages` và `message_edits` là hai collection shard độc lập; chunk của cùng một room có thể nằm ở hai shard. Nếu vẫn giữ, trích docs MongoDB nói transaction giữa hai collection như vậy luôn single-shard.
3. **"Bão duplicate xuống consumer"** (P4). JetStream bỏ bản trùng theo `Nats-Msg-Id` trong cửa sổ 5m; reconciler publish bù sau 30s nằm trong cửa sổ. Consumer thấy bản trùng ở bước nào?
4. **"CD3 chỉ xảy ra trong vài ms"** (Q7). Pending key sống tới `CID_PENDING_TTL` 10s. Core chết sau ack, trước Commit → client retry trong 10s sang core khác → tin nằm ngoài LRU 100 tin gần nhất (room vài chục tin/s chỉ mất vài giây) → trùng. Bạn giữ hay sửa mức rủi ro?
5. **"Cluster time an toàn tuyệt đối qua failover"** (Q4). Không cần trả lời nếu 3.B đứng vững (không còn dùng cluster time). Nếu bạn bác 3.B và muốn quay lại guard cluster time thì cần dẫn chứng về `operationTime` của majority read sau rollback.

### 4.2 Phản bác 3.A so với pre/post-image bạn đề xuất

Vòng 1 bạn đề xuất bật `changeStreamPreAndPostImages` trên `messages` để ghi lịch sử sửa đúng. 3.A thay bằng fact bất biến + projection.

- Kịch bản nào 3.A sai mà pre/post-image đúng?
- Chi phí pre-image ở 10K tin/s (ghi thêm vào `config.system.preimages`, oplog) so với một insert fact mỗi lần sửa?
- Cách nào khoá chặt vào Mongo hơn khi cần đổi sang PostgreSQL?

### 4.3 Phản bác topo (a) so với N stream `$mod` của bạn (3.C)

- Phía server, mỗi cursor có lọc `$mod` vẫn phải đọc toàn bộ oplog không? Trích docs nếu có.
- Con số "8.000–15.000 changes/s một tiến trình" ở vòng 1 lấy từ đâu (đo, docs hay ước lượng)?
- Thêm/bớt worker (đổi N) thì vị trí resume của từng stream xử lý thế nào?

### 4.4 D theo từng effect

Vòng 1 bạn đề xuất D = 3–5s và ack mark cho **mọi** effect.

- `PUB_ACK_TIMEOUT` = 2s; mark gom theo cửa sổ 10ms hoặc 256 khoá. D nhỏ nhất an toàn cho `msg_created` là bao nhiêu, và tính theo công thức nào?
- Ack mark cho mọi effect: số lệnh Redis thêm ở 10K tin/s, khi sửa/reaction hiếm hơn nhiều? So với chỉ mark cho effect lưu lượng cao, còn effect hiếm thì để reconciler chạy lại (JetStream bỏ bản trùng)?
