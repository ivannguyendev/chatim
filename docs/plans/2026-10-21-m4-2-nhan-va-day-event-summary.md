# M4 phần 2: nhận và đẩy event xuống máy

> **Trạng thái:** chờ owner duyệt. Phần 2 trong 5 phần của M4. Phần 1 (kết nối, token, lệnh) đã xong trên `feat/m4`.
> **Năm phần:** (1) kết nối, token, lệnh · **(2) nhận và đẩy event** · (3) đã đọc, đã xem · (4) typing, online · (5) reconnect, vận hành, kiểm thử.
> **Nguồn:** hướng M4 owner duyệt 2026-10-20 (`.claude/plans/m4-websocket_design.md`), review tải NATS (vòng 3), [events.md](../events.md) (D108, D156), quyết định ai nhận event của owner 2026-10-20.

## 1. Phần này làm gì

Sau phần 1, máy gọi được core nhưng chưa thấy gì mới nếu không hỏi. Phần 2 làm cho cái mới tự hiện ra:

1. Websocket nghe event core phát lên NATS.
2. Websocket biết mỗi người đang online ở những room nào.
3. Có event của room nào thì đẩy xuống đúng các máy cần nhận, không lộ event riêng của người này sang người khác.

Ví dụ:
- Lan kết nối. Websocket hỏi core "Lan ở những room nào" một lần và ghi vào RAM: Kho HCM, Sale Q4, DM với Minh…
- Bình gửi "Hàng về chưa?" vào Kho HCM. Core lưu tin và phát event. Websocket thấy Lan ở Kho HCM, đẩy xuống máy Lan: danh sách hội thoại nhảy lên.
- Lan đang mở Kho HCM. Minh react 👍: Lan thấy ngay. Ở Sale Q4 (Lan không mở) có người react thì Lan không nhận, vì danh sách hội thoại không đổi.
- Lan được thêm vào nhóm mới "Kế hoạch Q1": nhóm hiện ngay trên danh sách của Lan.
- Lan bị xoá khỏi Sale Q4: Lan nhận event "bị xoá", rồi từ đó không nhận gì của Sale Q4 nữa.
- Có người `@all` trong nhóm 5.000 người: Mention App phát 5 gói tin (mỗi gói nội dung một lần kèm 1.000 người nhận) thay vì 5.000 tin; websocket đẩy cho những người đang online. Room nào Lan đã tắt thông báo thì máy Lan tự không hiện.

Phần 2 chưa gồm "đã xem" của cả nhóm (phần 3), typing và online (phần 4), bão reconnect (phần 5).

## 2. Trong và ngoài phần 2

| Việc | Ai làm | Ghi chú |
|---|---|---|
| Nghe event, biết ai ở room nào, đẩy đúng người, đọc bù khi lỡ event | `apps/websocket` | Không database, tất cả trong RAM |
| API mới `ListMyRoomIDs`: mọi room của một người | `core` | Chỉ đọc; dùng index sẵn có của `members`, thêm 1 field `ver` |
| Đổi index bằng lệnh chạy tay `/app indexes` | `core` | Owner 2026-10-20: không tự động lúc khởi động |
| Thu hẹp subject của stream event, bật đọc nhanh theo số thứ tự | `core` | Đổi cấu hình stream `CHATIM_EVT` |
| Client mẫu áp event theo hợp đồng D156 | `pkg/wsclient` | Bỏ trùng theo id và version, lỗ seq thì gọi `GetHistory` |
| Thông báo `@` thành gói tin chuẩn: nội dung một lần + danh sách người nhận | Mention App (`modules/mention`) | Owner 2026-10-21; thay cho mỗi người một tin |
| Ẩn thông báo của room đang tắt | máy người dùng | Owner 2026-10-21: Phase 1 không lọc ở server; lọc (tắt, không làm phiền…) là việc của app notification Phase 2 |
| "Đã xem" cả nhóm | — | Phần 3 |
| User NATS riêng cho websocket có quyền hẹp | — | M5: NATS dev hiện chưa bật xác thực cho ai cả |

## 3. Websocket nghe event

Thiết bị **không** nối vào NATS. Chỉ mỗi node websocket nối, mỗi node một kết nối:

| Subscription | Nhận gì |
|---|---|
| `live.*.*.*.evt.*` | Mọi event core phát (tin, react, thành viên, đọc, ghim…), mỗi event có số thứ tự trong stream |
| `notify.*.mention` | Gói thông báo của Mention App (§6), mỗi gói kèm danh sách người nhận |

Phần 4 thêm subscription riêng cho typing và online, để chúng không chiếm chỗ của event chat.

Tải NATS không phụ thuộc số thiết bị, chỉ phụ thuộc số event/giây × số node. Ví dụ: 20K event/s × 1KB × 3 node ≈ 60MB/s, nhỏ so với sức NATS. Cách này đủ tới khoảng 10 node hoặc 50K event/s; lớn hơn thì chia node theo tenant.

**Không để node chậm làm rơi event:**
- Nhận được event, node chỉ đọc subject rồi chuyển ngay vào hàng xử lý theo room (giữ thứ tự từng room). Việc ghi ra hàng nghìn kết nối do hàng xử lý làm.
- Bộ đệm của thư viện NATS đặt 256MB (mặc định chỉ đủ ~3 giây), có metric độ đầy, alert ở 50% (phần 5).
- Node vẫn chậm sau khi đọc bù (trễ > 30 giây) thì từ chối bớt kết nối mới thay vì đọc bù mãi.

## 4. Biết ai ở room nào

1. Lan kết nối và đưa token xong. Node đã nghe event từ trước, rồi mới gọi `ListMyRoomIDs` (trả toàn bộ room của Lan một lần, kèm `ver` của từng tư cách thành viên). Người ở 5.000 room nhận khoảng 150KB.
2. Event thành viên của Lan đến trong lúc đang tải được giữ lại, tải xong thì áp theo "`ver` lớn hơn thắng". Ví dụ: Lan vừa kết nối thì được thêm vào "Kế hoạch Q1" (ver 1). Danh sách trả về đã có nhóm đó ver 1 thì event bị bỏ; chưa có thì event thêm nhóm vào.
3. Sau đó node gửi frame `ready`. Máy gọi `ListMyRooms` để vẽ danh sách.

Vì sao cần `ver`: hiếm khi một event cũ tới sau event mới (sweeper phát bù, hoặc đọc bù sau khi lỡ). Không so `ver` thì event "Lan vào nhóm" cũ có thể làm Lan nhận lại tin của nhóm đã rời. Vì vậy:
- Lúc bắt đầu tải danh sách, node ghi số thứ tự event cuối đã thấy; event thành viên cũ hơn mốc đó bị bỏ, vì danh sách đã mới hơn.
- Khi Lan rời một nhóm, node giữ dấu "đã rời" kèm `ver` chừng nào Lan còn kết nối; event "Lan vào nhóm" cũ tới muộn có `ver` không lớn hơn dấu đó bị bỏ.
- Người được nhận diện bằng cặp (tenant, user): Lan của tenant A và Lan của tenant B là hai người khác nhau.
- Nhiều thiết bị của Lan trên cùng một node dùng chung một danh sách, chỉ gọi core một lần.

**Hai mức quan tâm:**

| Mức | Khi nào | Nhận gì |
|---|---|---|
| Thành viên | Mọi room trong danh sách của người | Chỉ event làm danh sách hội thoại đổi |
| Đang mở | Máy gửi `open_room`, tới khi `close_room`; chỉ mở được room mình là thành viên | Mọi event của room |

`open_room` gửi trước `ready` thì được giữ tới khi `ready`. Mở một nhóm vừa tạo (trả lời `CreateRoom` về trước event `member_added`) thì node chờ tối đa 10 giây cho event đó; quá hạn mà vẫn không phải thành viên thì gửi `room_closed{not_member}`. Bị xoá khỏi room đang mở thì nhận `room_closed{removed}`.

## 5. Ai nhận event nào (owner 2026-10-20)

| Event | Mức thành viên | Mức đang mở | Chỉ chủ nhân |
|---|---|---|---|
| `msg_created`, `msg_edited`, `msg_deleted` | ✓ | ✓ | |
| `room_created`, `room_updated` | ✓ | ✓ | |
| `member_added`, `member_removed` | ✓ khi là chính mình | ✓ | |
| `member_role_changed`, `member_priority_changed`, `member_count_changed` | | ✓ | |
| `reaction_changed`, `counts_changed`, `msg_pinned`, `msg_unpinned` | | ✓ | |
| `read_updated` | | | ✓ phần 2; phần 3 thêm "đã xem" cho người đang mở room |
| `message_hidden`, `history_cleared`, `bookmark_changed`, `room_preferences_changed` | | | ✓ chỉ máy của người trong event |

- Tin có `@` core phát ở loại data `mention`. Node coi như `msg_created` thường để danh sách hội thoại không lỡ tin có `@`.
- Chỉ event loại `member` mới phải giải mã để biết người; các loại khác node chuyển nguyên bytes, mã hoá frame một lần rồi ghi cho N máy.
- **Thêm vào room mới:** node thấy `member_added` của Lan (vì nghe toàn bộ) và thêm room vào quan tâm của Lan nếu `ver` lớn hơn.
- **Bị xoá khỏi room:** gửi `member_removed` cho Lan trước, rồi bỏ room khỏi quan tâm và đóng room nếu đang mở.

## 6. Thông báo `@`: một gói tin, nhiều người nhận (owner 2026-10-21)

**Hôm nay:** Mention App bung `@all`/`@nhóm` thành từng người rồi phát **mỗi người một tin** (`notify.{tenant}.{user}.mention`), mỗi tin mang trọn nội dung tin nhắn (tới 16KB). `@all` trong nhóm 5.000 người là 5.000 tin, khoảng 80MB ghi vào JetStream (×3 bản sao ở prod), và mọi node websocket nhận đủ 5.000 tin.

**Sau phần 2:** một gói tin chuẩn mang nội dung **một lần**, kèm danh sách người nhận đã bung sẵn.

1. Mention App vẫn bung `@nhóm`/`@all` thành người (nó giữ nhóm), vẫn chỉ thông báo cho event mới hơn 5 phút.
2. Mention App phát gói `Delivery`:

| Field | Nghĩa | Ví dụ |
|---|---|---|
| `id` | id thông báo (theo tin và phiên bản) | `77-0-121-m0` |
| `tenant` | tenant | `hasaki` |
| `kind` | loại | `mention_notified`, `mention_retracted` |
| `payload` | nội dung thông báo, một lần (tin nhắn, đích được nhắc) | tin "`@all` họp lúc 3h" |
| `targets.users` | danh sách người nhận | `[lan, minh, ha, …]` |
| `part`, `parts` | phần thứ mấy trên tổng số phần | `2`, `5` |

3. **Chia phần:** mỗi gói tối đa 1.000 người, để dưới giới hạn 1MB mỗi tin của NATS (1.000 id × 64 byte + nội dung 16KB < 100KB). `@all` 5.000 người thành 5 gói, khoảng 400KB thay vì 80MB. Đây là cỡ trang, không phải giới hạn: channel 200K người sau này thành 200 gói.
4. Subject theo tenant và loại: `notify.{tenant}.mention`. Thông báo bên trong gói có id chung cho mọi trang (máy dùng để bỏ trùng). Mỗi gói có `Nats-Msg-Id` = id đó + số phần + mã băm danh sách người của trang, để stream bỏ trùng khi phát lại, và nếu danh sách thành viên đổi giữa hai lần phát thì trang mới không bị bỏ nhầm (không ai bị sót; ai nhận hai lần thì máy bỏ trùng).
5. **Websocket:** nhận gói qua `notify.*.mention`, kiểm tenant trong gói khớp tenant trong subject, duyệt `targets.users`, tra xem người nào đang nối vào node mình rồi đẩy frame `notification` (mã hoá một lần mỗi gói). Phase 1 không lọc room đang tắt: máy tự ẩn (owner 2026-10-21, chờ app notification).
6. **App notification Phase 2:** đọc cùng stream bằng consumer bền, bung người ra thiết bị, áp tắt thông báo và không làm phiền, rồi push. Học theo OneSignal, `targets` để dành chỗ cho `topics`, `devices`, `tags`; Phase 1 chỉ dùng `users`.

Giữ nguyên: chống trùng theo `message_ver` (bản lớn hơn thắng), thông báo rút lại khi tin bị sửa bỏ `@` hoặc bị xoá (D173). Mất đi: đọc lại thông báo của riêng một người trong 24 giờ theo subject; không ai dùng, vì trong app đã có `ListMentions` và app push dùng consumer bền. Mất thông báo lúc rớt mạng thì vẫn thấy trong `ListMentions`.

## 7. Lỡ event và đọc bù

Core NATS không lưu `live.*`. Node lỡ event khi rớt mạng NATS hoặc nhận không kịp.

1. Mỗi event `live.*` có header `Nats-Sequence` (số thứ tự trong stream `CHATIM_EVT`). Node nghe toàn bộ nên thấy đủ chuỗi số. Nhận 8812345 rồi 8812350 nghĩa là lỡ 8812346–8812349.
2. Mọi lỗ đi một đường: mỗi node chỉ **một** lượt đọc bù một lúc, do máy chủ chính của stream phục vụ (không đọc từ bản sao có thể đang trễ). Lỗ mới gộp vào lượt đang chạy. Đọc theo lô với tốc độ tối đa 20.000 event/s (gấp đôi đỉnh nhận, để bù kịp mà không dồn JetStream), dừng đúng ở cuối lỗ rồi xoá consumer ngay. Trước khi bắt đầu chờ ngẫu nhiên 0–5 giây, để 3 node không đè JetStream cùng lúc.
3. Lỗ quá trần (cũ hơn 2 phút tính từ event cuối nhận được, hoặc hơn 2,4 triệu event) hoặc đọc bù lỗi: không đọc bù nữa. Event thành viên trong lỗ có thể đã mất, nên node **nạp lại danh sách room** của từng người (rải trong 60 giây) rồi gửi `resync`; máy tự lấy lại danh sách hội thoại và lịch sử room đang mở. Nhờ vậy người bị xoá khỏi nhóm trong lúc lỗ không tiếp tục nhận tin của nhóm đó.
4. Một máy nhận bù quá nhiều (sắp đầy hàng gửi 1MB): gửi `resync` riêng cho máy đó thay vì cắt kết nối, để nó không nối lại rồi gọi `ListMyRooms` đắt cùng lúc với người khác.
5. Số thứ tự lùi lại (stream bị tạo lại, hoặc NATS mất phần đuôi chưa ghi): coi là bắt đầu lại, không phải lỗ khổng lồ. Sau khi nối lại NATS, node hỏi số thứ tự cuối của stream một lần để phát hiện lỗ ngay cả khi không có event mới.

Event đọc bù có thể tới sau event mới hơn. Máy xử lý theo hợp đồng D156: bỏ trùng theo id và version; `msg_created` có lỗ seq thì gọi `GetHistory`; không bao giờ "bỏ nếu seq ≤ seq đã thấy".

**Giữ kết nối NATS:** nối lại không giới hạn số lần (như core); ping NATS mỗi 10 giây để phát hiện kết nối chết nửa vời; lần đầu khởi động phải có NATS mới báo sẵn sàng, còn sau đó mất NATS vẫn báo sẵn sàng (nếu không, NATS chập là cả 3 node bị load balancer gỡ cùng lúc rồi mọi người ùa vào khi NATS lên lại) và có metric `nats_connected` để cảnh báo; không đệm tin gửi lúc mất kết nối.

## 8. Máy nhận chậm

Phần 1 đã có hàng ghi riêng mỗi kết nối và hạn ghi 10 giây. Phần 2 thêm:
- Hàng đợi gửi tính theo byte, mặc định 1MB mỗi kết nối, để vài máy chậm không ăn hết RAM. Không có ngân sách toàn node: một frame mã hoá một lần dùng chung cho hàng nghìn kết nối, đếm theo từng kết nối sẽ phóng đại RAM; RAM node được giữ bằng `GOMEMLIMIT` (container ≥ 2GB, ước tính ~1,3GB cho 33K kết nối có người ở 5.000 room).
- Đầy → đóng mã 4008. Máy nối lại sau và lấy snapshot.

## 9. Thay đổi ở core

1. **RPC `ListMyRoomIDs`:** trả mọi room `state 1` của người gọi, mỗi room `room_id` + `ver`, một lần (không phân trang). Chỉ websocket gọi; không mở cho máy. Đọc trên index user của `members` (đã có 9 field cho `ListMyRooms`), thêm field `ver` ở cuối để không phải mở từng doc.
2. **Lệnh `/app indexes [-dry-run] [-apply] [-drop-old]`** (owner 2026-10-20, cùng kiểu `resync`/`recount`):
   - `-dry-run`: in index nào thiếu hoặc cũ.
   - `-apply`: tạo index mới bên cạnh index cũ. Core vẫn chạy; `ListMyRoomIDs` chỉ chậm hơn tới khi xong.
   - `-drop-old`: bước riêng, xoá index cũ khi bản mới đã có.
   - Lúc khởi động core chỉ tạo index trên database mới; gặp index cũ thì giữ nguyên và log cảnh báo "chạy /app indexes". Cơ chế tự xoá index cũ đang có (`bootstrap_replaced_indexes`) chuyển sang lệnh này.
3. **Stream `CHATIM_EVT`:** thu hẹp subject từ `evt.>` còn đúng `evt.*.*.*.*`, để không event nào được lưu mà không phát ra `live.*` (nếu có, node sẽ thấy một lỗ giả và đọc bù vô ích). Core vẫn cập nhật cấu hình stream lúc khởi động như hôm nay (`CreateOrUpdateStream`); đổi này chỉ là cấu hình, không chép lại dữ liệu.

Core không đổi event, subject phát ra, đường ghi hay proto event nào.

**Thay đổi ở Mention App** (§6): phần phát thông báo (`modules/mention/internal/notify`) đổi từ mỗi người một tin sang gói `Delivery` chia phần; stream `CHATIM_NOTIFY` đổi subject sang `notify.{tenant}.{kind}`; proto thêm `Delivery`/`Targets`. Phần bung người nhận, ngưỡng 5 phút, chống trùng giữ nguyên. Đổi một phần D168 (dạng subject và gói tin).

## 10. Frame mới

| Frame | Hướng | Nội dung |
|---|---|---|
| `open_room` / `close_room` | máy → app | room id |
| `ready` | app → máy | đã tải xong danh sách room |
| `event` | app → máy | đúng bytes `Event` core phát |
| `notification` | app → máy | nội dung của gói `Delivery` (không kèm danh sách người nhận) |
| `resync` | app → máy | lý do; máy lấy lại snapshot |
| `room_closed` | app → máy | room id và lý do (`not_member`: mở room không phải thành viên; `removed`: bị xoá khỏi room) |
| đóng 4008 | app → máy | máy nhận chậm, hàng đợi đầy: nối lại sau |

## 11. Config mới

| Biến | Mặc định | Vì sao |
|---|---|---|
| `NATS_URL` | — | kết nối NATS |
| `EVT_STREAM`, `EVT_LIVE_ROOT`, `NOTIFY_ROOT` | `CHATIM_EVT`, `live`, `notify` | giống core |
| `WS_ROOM_IDS_INFLIGHT` | 64 | số lần gọi `ListMyRoomIDs` cùng lúc mỗi node, bảo vệ core |
| `WS_FANOUT_WORKERS` | số CPU | số hàng xử lý theo room |
| `WS_NATS_PENDING_BYTES` | 256MB | bộ đệm mỗi subscription |
| `WS_SEND_QUEUE_BYTES` | 1MB | máy chậm không ăn hết RAM |
| `WS_FANOUT_QUEUE` | 4.096 event mỗi hàng | hứng một nhịp dồn ~1 giây; đầy thì bộ đệm NATS hứng |
| `WS_REPLAY_RATE` | 20.000 event/s | gấp đôi đỉnh nhận: bù kịp mà không dồn JetStream |
| `WS_OPEN_PENDING` | 10s | `open_room` của nhóm vừa tạo chờ event `member_added` tới |
| `WS_REPLAY_MAX_AGE`, `WS_REPLAY_MAX` | 2m, 2.400.000 (= tốc độ × thời gian) | quá trần thì nạp lại + `resync` thay vì đọc bù |
| `WS_RESYNC_WINDOW` | 60s | rải `resync` cho máy |
| `MENTION_NOTIFY_TARGETS_PAGE` (Mention App) | 1.000 | số người mỗi gói thông báo, để mỗi tin NATS dưới 1MB |

## 12. Quyết định

| Chọn | Bỏ, vì sao |
|---|---|
| Mỗi node một subscription cho toàn bộ event, định tuyến trong RAM | Một subscription mỗi room: hàng triệu lệnh sub/unsub khi bão reconnect, và node không bao giờ thấy `member_added` của room mới |
| Hai mức quan tâm | Chỉ room đang mở: danh sách hội thoại không cập nhật. Mọi event cho mọi room: tốn băng thông (react của 5.000 room) |
| `ListMyRoomIDs` một lần, thêm 1 field index | Phân trang: thêm lượt gọi core mỗi kết nối. Dùng `ListMyRooms`: nặng (xếp, xem trước, unread) |
| Phát hiện lỗ theo `Nats-Sequence` liên tiếp, đọc bù có trần | Gửi `resync` cho mọi máy khi lỡ event: một cú chập NATS gây bão `ListMyRooms`. Đọc bù không trần: làm chậm core vì chung JetStream |
| Thông báo là gói tin chuẩn: nội dung một lần + danh sách người nhận, chia 1.000 người mỗi gói (owner) | Mỗi người một tin: `@all` 5.000 người = 5.000 bản sao nội dung (~80MB), tràn mạng và RAM. Đích là cả room để websocket tự bung: websocket không biết nhóm, không biết người vào sau tin |
| Phase 1 không lọc room đang tắt ở server; chờ app notification (owner) | Websocket lọc: thêm 2 field index và trạng thái trong RAM, rồi Phase 2 lại chuyển sang app notification. Mention App lọc: ngược D168, mọi kênh bị lọc như nhau |
| Đổi index bằng lệnh chạy tay (owner) | Tự build lại index lúc khởi động: một thao tác nặng không ai chủ động chạy |

## 13. Rủi ro

- **Node nhận toàn bộ event:** ổn ở 2–3 node; quá ~10 node hoặc ~50K event/s phải chia node theo tenant.
- **Đổi dạng thông báo là đổi hợp đồng của `CHATIM_NOTIFY`:** hôm nay chỉ e2e đọc stream này nên đổi thẳng, không cần chạy song song hai dạng. Stream cũ có thể còn tin dạng cũ trong 24 giờ; websocket bỏ qua tin không giải mã được và đếm.
- **Thêm field vào index `members`:** index to hơn một chút; build bằng lệnh khi chưa go-live nên rẻ.

## 14. Kiểm thử phần 2

| Test | Chứng minh |
|---|---|
| Unit định tuyến | Đúng bảng §5; event riêng không tới người khác; tin có `@` tới mức thành viên |
| Unit tải room trong lúc có event | Mọi thứ tự giữa `ListMyRoomIDs` và event thành viên cho cùng kết quả |
| Unit thêm/xoá thành viên | Room mới hiện ngay; bị xoá thì ngừng nhận, `open_room` bị từ chối |
| Unit lỗ event | Phát hiện lỗ (cả lỗ ở cuối sau khi nối lại); một lượt đọc bù mỗi node, đúng tốc độ, gộp lỗ, dừng đúng chỗ, xoá consumer; quá trần → nạp lại danh sách room rồi `resync` rải đều; máy nhận bù quá nhiều → `resync` riêng; seq lùi = bắt đầu lại |
| Unit node chậm | Callback NATS không chặn; hàng theo room giữ thứ tự; vượt byte → 4008 |
| Storetest + explain itest `ListMyRoomIDs` | Hai adapter cùng kết quả; truy vấn chỉ đọc index |
| Test lệnh `/app indexes` | `-dry-run` không ghi; `-apply` tạo index mới bên cạnh; `-drop-old` chỉ xoá khi bản mới đã có; khởi động không tự đổi index |
| itest NATS thật | Header `Nats-Sequence` có thật; đọc bù từ `CHATIM_EVT`; subject hẹp vẫn nhận đủ event |
| Unit Mention App gói thông báo | `@all` 2.500 người ra 3 gói, mỗi người đúng một lần, nội dung giống nhau; id và số phần đúng; thông báo rút lại cũng chia phần; ngưỡng 5 phút giữ nguyên |
| Unit websocket nhận gói | Chỉ đẩy cho người trong danh sách đang nối vào node; người không có trong danh sách không nhận; gói lỗi bị bỏ và đếm |
| e2e phase 8 (sửa) | Các kiểm thông báo đọc gói mới: đúng người nhận, đúng số gói |
| e2e phase 9 (thêm) | Máy A gửi, máy B ở room đó nhận event; B không mở room thì không nhận react; thêm vào nhóm mới thì hiện ngay; bị xoá thì ngừng nhận; event riêng không lọt; thông báo `@` tới đúng người qua websocket |
| Diễn tập NATS restart | Lỗ được đọc bù, không `resync` hàng loạt |
