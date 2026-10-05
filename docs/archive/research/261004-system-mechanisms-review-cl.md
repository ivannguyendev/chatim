# Phản biện báo cáo cơ chế hệ thống chatim

Tài liệu gốc: `261004-system-mechanisms-report.md` (04/10/2026). Review dựa trên báo cáo, chưa đọc code. Số liệu trong báo cáo chỉ đo trên môi trường dev.

## 1. Nhận định chung

Hướng đi đúng ở nhiều điểm: DB là nguồn sự thật, event id tự nhiên, bỏ counter toàn phòng, thay watermark/sweeper bằng reconciler. Việc tự gắn nhãn Built/Proposed và tự tìm ra bug ack-mark cũng là tín hiệu tốt.

Điểm yếu cốt lõi liên quan đúng mục tiêu "không vá từng task": **các cơ chế C1–C10 đang được chia theo kỹ thuật (CAS, effect, counter, view…), trong khi tính năng khác nhau theo lớp dữ liệu** (khối lượng, số người đọc, mức chịu mất). Chưa có phân lớp này thì unread, read receipt, typing, edit history vẫn mỗi cái đòi một biến thể riêng, tức là lại vá.

## 2. Những điểm nghiêm trọng nhất

### 2.1 "Effect là hàm thuần của document hiện tại" (5.2) không đủ

- Reconciler đọc thay đổi v2 nhưng doc đã là v3 thì phát event v3, event v2 không bao giờ tồn tại. Với client thì ổn ("higher version wins"). Nhưng P7 (system message "X pinned") cần biết ai làm gì ở lần đổi đó: A pin, B unpin ngay thì thông báo "A pinned" không bao giờ sinh ra.
- Edit history không phải hàm thuần của doc hiện tại, vì text cũ đã mất. D51 và P4 ("mọi effect dựng lại được từ doc") sai đúng chỗ cần nhất.
- Edit cộng ghi history là hai document, nên P2 đang bị vi phạm ngầm. Báo cáo đã ngầm thừa nhận khi nói về "history record landing after delete".
- Với current-doc, stream chỉ là thông báo đổi trạng thái, không phải log. Q2 hỏi downstream cần "every event" thì câu trả lời là chưa đạt.
- **Đề xuất:** tách hai lớp.
  - Fact bất biến (insert với unique key: message, edit version, pin action) có event id là chính key đó. Feed chỉ cần insert, không cần lookup hay ảnh trước/sau, thứ tự không mơ hồ.
  - State hiện tại (message, pins) là projection, cập nhật idempotent kiểu `set where ver < v`.
  - Hoặc bật pre/post-image cho vài collection cần thiết thay vì dùng current-doc cho tất cả.

### 2.2 P5 gộp hai thứ khác bản chất

- Count theo target (reaction, reply, member) là một số cho mọi người, cỡ O(số target). Recount được.
- Unread là O(target × người đọc). Chỉ cần 1% lưu lượng đỉnh rơi vào nhóm 5K người: 100 msg/s × 5K = 500K recount/s.
- Cap 99+ chỉ chặn chi phí khi hầu hết doc thỏa điều kiện. Nhưng "chưa ẩn với tôi" nằm ở collection khác, tức là join, trái P2. Nhiều tin đã xóa hoặc của chính mình thì scan vượt 100.
- R17 đòi "exact", mà counter eager chỉ eventually exact. Chỉ tính lazy lúc đọc mới exact.
- **Đề xuất (trả lời Q8):** unread tính lazy khi lấy room list (range scan `seq > readSeq` trên clustered key, có limit), cache ngắn. Event chỉ để client tăng cục bộ. Counter eager chỉ giữ cho aggregate theo target.

### 2.3 Guard bằng cluster time (5.3): nên test trước khi duyệt

- Theo hiểu biết của mình, `operationTime` của majority read là mốc node đã apply, chưa chắc là mốc snapshot mà read dùng. Sau failover, primary mới có thể có mốc thấp hơn primary cũ (entry chưa replicate bị rollback). Khi đó một count cũ mang nhãn lớn hơn có thể chặn count mới đúng. "Tự lành ở lần touch kế" chỉ đúng nếu còn fact mới đến.
- Chưa kiểm chứng trên MongoDB thật. Cần test failover có chủ đích hoặc đọc kỹ docs.
- Cách này đưa ngữ nghĩa riêng của Mongo vào cơ chế chung, nên các "ports" bị rò (Q10).
- **Phương án không cần đồng hồ (Q4):** optimistic CAS bằng `ver` trên counter doc. Đọc counter (ver=k), recount sau đó trong cùng causal session, rồi `set n, inc ver where ver=k`. CAS fail thì recount lại. Touch sinh ra sau fact cuối cùng luôn đọc sau fact đó, nên kết quả cuối đúng. Hoặc single-writer: chỉ worker phân vùng theo hash(target) được ghi counter.
- Delay 30s của reconciler chỉ có lý do với event creation (nhường fast path). Nó nên là tham số theo từng effect, không phải hằng số toàn cục.

### 2.4 P3 "mọi thay đổi dữ liệu thì đúng 1 event" quá tuyệt đối

- Read position: mỗi tin trong nhóm 5K có thể kéo theo tới 5K `read_updated`.
- Typing và presence thuộc phase 1 nhưng chưa nằm trong bản đồ cơ chế nào.
- Đừng làm "cơ chế riêng cho read receipt" (đúng như kế hoạch đầu bị bác), cũng đừng đối xử mọi thứ như nhau. Registry cần tham số chính sách theo từng effect: cửa sổ coalesce, delay, có ack-mark hay không, ephemeral hay durable. Một cơ chế, nhiều chính sách.

### 2.5 Commit log là đường bảo đảm duy nhất, nhưng không có đường sửa khi mất (RC5)

- B1 cấm secondary index trên messages, nên không có cách quét "thay đổi từ thời điểm T" ngoài oplog. Position rơi khỏi oplog thì chỉ còn full-scan hàng tỷ doc. "Restart from now" nghĩa là mất vĩnh viễn, trong khi alert chưa có.
- "Retention ≥ 24h" nên tính theo byte. Oplog là capped theo dung lượng: ở đỉnh liên tục 10K msg/s × 1–2KB là khoảng 0,9–1,7TB/ngày, nên cửa sổ co lại đúng lúc cao điểm.
- Cần ba thứ:
  1. Quy trình resync có định nghĩa và có test, ví dụ quét đuôi các room có hoạt động trong khoảng mất.
  2. Tiêu chí tốc độ xả backlog ≥ ~3× peak ingest, nếu không sau sự cố 1 giờ sẽ không bao giờ đuổi kịp.
  3. Alert đi kèm từng guarantee, không để đến M5.
- Q6: nút nghẽn đầu tiên nhiều khả năng là một cursor change stream cộng lookup, không phải publish (phỏng đoán, cần đo). Nên tách một reader nhẹ (đọc từ secondary) đẩy bản ghi thô vào work stream phân vùng theo slot, để worker ở mọi core chạy effect. Nhiều cursor song song thì mỗi cursor vẫn phải đọc oplog.

### 2.6 Quy tắc "hole cũ hơn 5s là void" (Q11): bỏ luôn, đừng chỉnh ngưỡng

- R10 và P8 đã nói client không phát hiện mất bằng cách đếm. Hole chỉ là số seq không dùng.
- Nếu client chèn tin theo seq (R5) thì tin đến muộn 7s vẫn hiện đúng chỗ. Ngưỡng chỉ cần khi có thứ gì đó dựa vào tính liên tục của seq, và thứ đó mới là cái cần xem lại.

### 2.7 CD3 (Q7) chỉ "hẹp" ở phòng ít tin

- Điều kiện "ngoài 100 tin gần nhất" gần như luôn thỏa ở phòng vài chục tin/s, vì 100 tin chỉ mất vài giây, ngắn hơn cửa sổ pending 10s.
- Commit bị drop khi queue đầy xảy ra lúc quá tải, đúng lúc client retry nhiều nhất và có thể retry sang core khác. Nên CD3 tương quan với tải.
- Vẫn chấp nhận được với chat. Nhưng nên ghi đúng mức rủi ro, và che ở phía đọc: cid đã nằm trong record, nên read API và client gộp hai tin cùng (sender, cid), thay vì thêm tầng ghi.
- CD2 giữ nguyên, kèm alert chế độ degraded.

## 3. Các nguyên tắc còn lại

- **P1:** đúng và quan trọng nhất. Cần lint hoặc contract test bắt buộc "mọi write path mới là CAS/upsert". Fencing (Q1) chưa cần nếu P1 giữ tuyệt đối. Rủi ro thật là sự cố tự khuếch đại: double ownership gây va chạm seq, renumber tối đa 3 lần, rồi "retry later", client retry nhiều hơn. Cần backoff có jitter và actor tự rút khi thấy doc của core khác.
- **P2:** sửa câu chữ cho trung thực. Mỗi command có một fact chính làm điểm tuần tự hóa, mọi thứ khác là effect idempotent.
- **P4:** cần phân loại effect. Loại hội tụ (event, counter, cleanup) chạy lại an toàn. Loại không hội tụ (push, system message, webhook) phải idempotent ở consumer theo event id, ví dụ system message ghi bằng unique key theo event id. P7 chưa nói điều này.
- **P6:** ổn. `counts_changed` có id chứa T (thời điểm đếm), lệch nhẹ với "id không phụ thuộc lúc nào"; chấp nhận được nhưng nên ghi là ngoại lệ có chủ ý.
- **P8:** đúng, nhưng 100K client reconnect cùng lúc là thundering herd lên read path chưa có cache.

## 4. Thiếu trong bản đồ cơ chế (ngoài H1–H8)

- **Inbox/room list:** send path cố ý không ghi gì lên room. Vậy last message, last activity và unread lấy từ đâu: N reverse-scan mỗi lần tải, hay có projection? Đây là read đắt nhất lúc reconnect storm.
- **Thu hồi quyền so với live fan-out:** user bị xóa khỏi phòng nhưng gateway còn subscribe và event mang nội dung. Khoảng rò bằng độ trễ thu hồi. H8 mới nói event riêng tư.
- **Chế độ import im lặng:** migrator ghi thẳng DB thì reconciler thấy hết, sinh hàng tỷ event. Cần cờ import và policy riêng.
- **Ép buộc version bump:** feed chỉ lấy update khi version đổi. Dev quên bump thì mất event mà không ai biết. Cần contract test cho mọi write method của adapter.
- **Câu hỏi mở ở 5.1:** desired-state đủ cho lệnh "set" và giá trị đơn điệu. Không đủ cho lệnh tạo mới (forward, reply cần cid hoặc id tất định) và giá trị không đơn điệu (mark unread là hạ read position, mâu thuẫn "raise to at least N"; cần version hoặc last-writer-wins theo command).

## 5. Đề xuất để không phải vá nữa

Mỗi tính năng khai báo nó thuộc lớp nào, và lớp đó có đúng một cách ghi, một cách phát event, một cách sửa lỗi:

| Lớp | Ví dụ | Ghi / event / sửa lỗi |
|---|---|---|
| Fact bất biến | message, edit version, pin action | insert unique key; event id = key; reconciler từ insert |
| State có version | message hiện tại, pins ở room | CAS theo ver; event theo ver; current-doc OK (state-sync) |
| Tập (target, user) | reaction, member | upsert unique + change number |
| Aggregate theo target | reaction/reply/member count | counter CAS-ver hoặc single-writer; touch lại để lành |
| Giá trị theo người đọc | unread, mention count, hidden | tính lazy lúc đọc, có cap; không event |
| Tần suất cao, gộp được | read position | upsert đơn điệu + coalesce; event tối đa 1 lần/N giây |
| Ephemeral | typing, presence | không lưu; NATS core |

Thêm hai luật vào quy trình "feature → mechanism" đã có:

1. Bảng này có thêm hai cột: lớp dữ liệu, và ngân sách khuếch đại (số write, read, lookup, event cho mỗi thao tác ở quy mô lớn nhất: nhóm 5K, channel 200K). Cột này bắt được lỗi 2.2 và 2.4 từ sớm.
2. Mỗi guarantee (RC1–RC5, CD1–CD3) phải đi kèm detector và alert ngay khi được định nghĩa.

## 6. Trả lời nhanh các câu hỏi còn lại ở mục 9 của báo cáo

- **Q3:** chạy hai lần là ổn với effect hội tụ. Current-doc ổn cho state-sync, không ổn cho history và system message.
- **Q5:** CAS đơn doc ngoài actor ổn nếu receiver nhận change-trước-create (báo cáo đã nêu) và chốt xong edit history (2.1).
- **Q9:** thiết kế trước phân lớp dữ liệu, rồi H3/H4, rồi inbox, sau đó mới H1.
- **Q10:** clustered key cộng unique-insert CAS thay được bằng store range-sharded khác. Chỗ khóa chặt vào Mongo là cluster-time guard, `updateLookup` và ngữ nghĩa resume token/oplog.

## 7. Các điểm chưa chắc

- **Mục 2.3 (cluster time):** suy luận của mình về hành vi `operationTime` của majority read sau failover. Chưa kiểm chứng trên MongoDB thật.
- **Mục 2.5 (oplog, nút nghẽn reconciler):** con số 0,9–1,7TB/ngày chỉ là ước tính ở đỉnh liên tục; nút nghẽn đầu tiên là phỏng đoán, cần đo bằng replay backlog.

Đề xuất bắt đầu thảo luận từ mục 2.1 (fact bất biến so với state), vì nó quyết định hình dạng của cả 5.2 và 5.3.
