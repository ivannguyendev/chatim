# M4 phần 2: nhận và đẩy event xuống máy

> **Trạng thái:** đã xây trên `feat/m4` (2026-10-22, commit `2b3c6c9`…`ec75155`); owner duyệt hướng 2026-10-21. Phần 2 trong 5 phần của M4; phần 1 (kết nối, token, lệnh) đã xong. Quyết định D189–D194, kết quả ở §13.
> **Chi tiết cho người code:** `.claude/plans/2026-10-21-m4-2-nhan-va-day-event.md`.

## 1. Phần này làm gì

Sau phần 1, máy gọi được core nhưng không tự thấy cái mới. Phần 2 làm cái mới tự hiện ra:

- Bình gửi tin vào nhóm Kho HCM → danh sách hội thoại của Lan nhảy lên ngay.
- Lan đang mở Kho HCM → thấy cả react, ghim. Nhóm Lan không mở thì chỉ nhận cái làm danh sách thay đổi.
- Lan được thêm vào nhóm mới → nhóm hiện ngay. Lan bị xoá khỏi nhóm → không nhận gì của nhóm đó nữa.
- Có người `@all` → Lan thấy thông báo trong app.
- Mạng chập vài giây → không mất tin nào.

Chưa gồm: "đã xem" của cả nhóm (phần 3), typing và online (phần 4), bão kết nối lại (phần 5).

## 2. Trong và ngoài

| Việc | Ai làm |
|---|---|
| Nghe event, biết ai ở room nào, đẩy đúng người, đọc bù khi lỡ | websocket (không database, tất cả trong RAM) |
| API `ListMyRoomIDs` (mọi room của một người) và lệnh đổi index `/app indexes` | core |
| Thông báo `@` thành gói tin có danh sách người nhận | Mention App |
| Lọc room đang tắt thông báo | Phase 1: máy tự ẩn. Phase 2: app notification |
| Quyền NATS riêng cho websocket | M5 (chặn go-live) |

## 3. Websocket nghe gì

Thiết bị **không** nối vào NATS. Chỉ mỗi node websocket nối, mỗi node một kết nối, đọc hai stream theo thứ tự:

| Nghe | Là gì |
|---|---|
| Stream event `CHATIM_EVT` | Mọi event core phát (tin, react, thành viên, đọc…) |
| Hộp thư `websocket.publish.*` (stream `CHATIM_WS`) | Gói tin các app gửi tới máy (§6) |

Mỗi node nhận **toàn bộ** event, nhưng chỉ đẩy cho máy đang nối vào chính nó. Ví dụ Lan ở ws-1, Bình ở ws-2: tin của Kho HCM tới cả hai node; ws-1 đẩy cho Lan, ws-2 đẩy cho Bình. Không cần ai biết "Lan đang ở node nào".

Tải NATS chỉ phụ thuộc số event và số node, không phụ thuộc số thiết bị: 20K event/s × 3 node ≈ 60MB/s, nhẹ. Đủ tới khoảng 10 node; lớn hơn thì chia node theo tenant.

## 4. Biết ai ở room nào

1. Lan kết nối → node hỏi core `ListMyRoomIDs` một lần, ghi vào RAM: Lan đang ở Kho HCM, Sale Q4…, và **các nhóm Lan đã rời kèm phiên bản** (người ở 5.000 room ≈ 150KB). Nhiều máy của Lan trên cùng node dùng chung một lần hỏi. **Đây là một trong hai lúc duy nhất websocket nạp từ core: lúc login và lúc node khởi động lại** (owner 2026-10-22).
2. Lan mở một nhóm (`open_room`) → nhận mọi event của nhóm đó. Nhóm không mở → chỉ nhận event làm danh sách hội thoại đổi.
3. Lan được thêm vào nhóm → node thấy event "Lan vào nhóm" và thêm nhóm vào RAM. Lan bị xoá → node gửi Lan event "bị xoá" rồi **kick Lan khỏi nhóm ngay trong RAM**: từ đó không đẩy gì của nhóm đó cho Lan nữa.

Mỗi tư cách thành viên có một số phiên bản (vào nhóm v1, rời v2, vào lại v3). Node chỉ nhận thay đổi có phiên bản lớn hơn, để một event cũ tới muộn không làm Lan "vào lại" nhóm đã rời. Vì lúc login đã biết "Lan rời X ở v2", một tin cũ "Lan vào X v1" do sweeper phát bù muộn luôn bị bỏ, kể cả khi Lan vừa mới kết nối. Người được nhận diện bằng cặp (tenant, user): Lan của tenant A và Lan của tenant B là hai người khác nhau.

## 5. Ai nhận event nào (owner 2026-10-20)

| Event | Ai nhận |
|---|---|
| Tin mới, sửa, xoá; đổi tên/ảnh nhóm | Mọi thành viên đang online |
| Mình được thêm / bị xoá | Chính mình |
| React, ghim, đổi vai trò, số thành viên | Người đang mở nhóm |
| Ẩn tin, xoá lịch sử, bookmark, ghim/tắt thông báo/lưu trữ hội thoại, đọc tới đâu | Chỉ chủ nhân ("đã xem" cho cả nhóm ở phần 3) |
| Loại event mới chưa có trong bảng | Không ai (mặc định chặn, để event riêng tư sau này không lọt) |

## 6. Thông báo `@`: một gói tin, nhiều người nhận (owner 2026-10-21)

**Hôm nay:** `@all` trong nhóm 5.000 người → Mention App phát 5.000 tin, mỗi tin chép lại nội dung (≈ 80MB).

**Sau phần 2:** Mention App phát gói tin chuẩn: nội dung **một lần**, kèm danh sách người nhận, mỗi gói tối đa 1.000 người. `@all` 5.000 người → 5 gói (≈ 400KB). Websocket đọc danh sách, ai đang online trên node mình thì đẩy.

**Tách vai:**

| Bên | Việc | Hộp thư |
|---|---|---|
| Websocket | Chỉ là đường truyền tới máy đang online | `websocket.publish.{tenant}` |
| App notification (Phase 2) | Lọc tắt thông báo, không làm phiền; push khi offline, chuyển sang websocket khi online | `notify.{tenant}.{kind}` |
| Mention App, app khác | Phát nội dung kèm danh sách người nhận | Phase 1 phát thẳng vào hộp thư websocket; Phase 2 đổi một dòng config sang `notify` |

Khuôn gói nằm trong package dùng chung `pkg/wspublish`, học theo OneSignal: danh sách người nhận hôm nay theo user, để sẵn chỗ cho theo thiết bị, theo chủ đề. Mất thông báo lúc rớt mạng thì vẫn thấy trong `ListMentions`.

## 7. Lỡ event và đọc bù (owner 2026-10-21)

JetStream đánh số liên tiếp cho mọi event (1001, 1002, 1003…) và lưu 7 ngày. Mỗi node đọc stream **theo thứ tự** bằng tính năng có sẵn của thư viện NATS ("ordered consumer"). Node không tự đếm, không lưu gì.

Ví dụ mạng của ws-2 chập 2 giây: ws-2 đã nhận tới 1002 thì đứt. Khi mạng lại, thư viện thấy thiếu nên tự bỏ phần đang cầm và đọc lại **từ 1003 trở đi, liên tục**: 1003, 1004, …, 1500, 1501… cho tới khi đuổi kịp hiện tại. Tin tới máy đúng thứ tự, không mất tin nào; người dùng thấy trễ chưa tới một giây. ws-1, ws-3 không bị ảnh hưởng.

Hai cái phanh để không hại core:
1. Đang đuổi theo thì đọc tối đa 20.000 event/giây. Trễ quá 2 phút → bỏ đoạn cũ, nhảy tới hiện tại. **Trước khi đọc tiếp, node đọc lại riêng các tin "vào nhóm"/"bị xoá" của đoạn bị bỏ qua** (JetStream lọc được theo loại, số lượng ít) và áp vào RAM: ai bị xoá trong lúc đó bị kick ngay. Không gọi core. Rồi bảo máy "tải lại" để máy tự lấy những tin nhắn bị bỏ qua. Nếu không đọc lại được (stream bị tạo lại, đoạn đó quá hạn lưu 7 ngày, hoặc lỗi) thì coi như node khởi động lại: đóng các kết nối rải đều, máy login lại và nạp lại.
2. Máy nào nhận không kịp lúc đang đuổi theo → bảo riêng máy đó "tải lại", không cắt kết nối.

| Chuyện xảy ra | Websocket làm gì | Người dùng thấy |
|---|---|---|
| Mạng chập 2 giây | Thư viện đọc lại từ số cuối, đuổi kịp trong ~0,2 giây | Tin tới trễ một chút |
| Node bị cắt khỏi NATS 5 phút, Lan bị xoá khỏi X trong lúc đó | Nhảy tới hiện tại, đọc lại tin vào/bị xoá của 5 phút đó, kick Lan khỏi X ngay, bảo máy tải lại | Lan không nhận thêm gì của X; danh sách hội thoại tự làm mới |

"Trễ 2 phút" đo bằng giờ JetStream lưu tin, không bằng giờ ghi trong event (sweeper có thể phát lại event cũ, mang giờ cũ nhưng vừa mới lưu).

Vì sao không luôn bảo máy tải lại cho đơn giản: mỗi lần như vậy 33.000 máy trên node cùng gọi `ListMyRooms`; mạng chập 1 giây mà tốn thế là quá đắt.

Mất NATS sau khi node đã chạy thì node vẫn báo sẵn sàng (nếu không, NATS chập là cả 3 node bị load balancer gỡ cùng lúc); có metric để cảnh báo.

## 8. Máy nhận chậm

Mỗi máy có hàng chờ gửi tối đa 1MB. Đầy → đóng mã 4008, máy nối lại sau. Một event chỉ mã hoá một lần rồi gửi cho mọi máy cần nhận.

## 9. Thay đổi ở core và Mention App

- **`ListMyRoomIDs`:** trả mọi room của người gọi kèm phiên bản, một lần. Chỉ websocket gọi, máy không gọi được.
- **Index `members`:** thêm 1 field `ver` để `ListMyRoomIDs` chỉ đọc index. Đổi bằng lệnh `/app indexes` chạy tay (xem trước / tạo bản mới / xoá bản cũ khi bản mới xong), không tự động lúc khởi động. Lệnh phủ cả index của Mention App.
- **Mention App:** phát gói tin có danh sách người nhận vào `websocket.publish.{tenant}` (stream mới `CHATIM_WS`) thay vì mỗi người một tin; phần bung người nhận, ngưỡng 5 phút, chống trùng giữ nguyên.

Core không đổi stream, event, đường ghi hay proto event nào.

## 10. Quyết định

| Chọn | Bỏ, vì sao |
|---|---|
| Mỗi node nghe toàn bộ, lọc trong RAM | Nghe theo từng room: hàng triệu lần đăng ký khi bão kết nối lại, và không thấy được nhóm mới |
| Hai mức: thành viên / đang mở | Mọi event cho mọi room: tốn băng thông (react của 5.000 room) |
| Đọc stream theo thứ tự bằng ordered consumer của NATS; lỡ thì thư viện đọc lại từ số cuối (owner) | Tự đếm số và tự xin lại từng đoạn: nhiều code, phải thu hẹp subject stream ở core. Luôn tải lại: mạng chập ngắn cũng gây 33.000 lần gọi core |
| Gói tin + danh sách người nhận (owner) | Mỗi người một tin: 5.000 bản sao nội dung |
| Websocket có hộp thư riêng, app notification có hộp thư riêng (owner) | Websocket nghe `notify`: trộn vai |
| Phase 1 không lọc tắt thông báo ở server (owner) | Websocket lọc rồi Phase 2 lại chuyển đi |
| Đổi index bằng lệnh chạy tay (owner) | Tự build lúc khởi động: thao tác nặng không ai chủ động chạy |

## 11. Rủi ro

- Node nhận toàn bộ event: ổn tới ~10 node; quá thì chia node theo tenant.
- RAM mỗi node ~1,3GB ở 33K kết nối có người ở 5.000 room; container ≥ 2GB.
- Quyền NATS riêng cho websocket để M5: tới lúc đó ai vào được NATS là giả được tin đẩy tới người dùng. Chặn go-live.

## 12. Kiểm thử

| Test | Chứng minh |
|---|---|
| Unit định tuyến | Đúng bảng §5; event riêng không lọt; cùng id người ở hai tenant không lẫn |
| Unit thành viên | Nhóm mới hiện ngay; bị xoá thì ngừng nhận; event cũ tới muộn không làm "vào lại" |
| Unit đọc bù | Lỡ tin thì nhận đủ và đúng thứ tự; hai cái phanh (trễ quá 2 phút, máy không kịp) |
| Unit gói thông báo | 2.500 người → 3 gói, mỗi người đúng một lần; phát lại không sót ai |
| Test `ListMyRoomIDs` và `/app indexes` | Chỉ đọc index; lệnh không xoá bản cũ khi bản mới chưa xong |
| e2e | Hai máy: gửi/nhận, mở/không mở nhóm, thêm/xoá thành viên, event riêng, `@` tới đúng người |
| Diễn tập | Cắt riêng một node khỏi NATS 10 giây (đuổi kịp, không tải lại) và 3 phút, có người bị xoá trong lúc cắt (bị kick ngay sau khi nối lại, không gọi core) |

## 13. Kết quả thực thi

**Đã xây:** websocket đọc `CHATIM_EVT` và hộp thư `CHATIM_WS` bằng ordered consumer, bảng quan tâm (tenant, user) → room trong RAM, frame `ready`/`open_room`/`close_room`/`event`/`notification`/`resync`/`room_closed`, hai phanh, giới hạn 1MB mỗi máy; core có `ListMyRoomIDs` và `/app indexes`; Mention App phát gói `WsPublish`; `pkg/wspublish`, `pkg/mongoindex`; `pkg/wsclient` áp event theo D156.

**Đổi trong lúc xây:**
- Bắt đầu đọc ở "seq cuối của stream + 1" thay vì "từ bây giờ": nếu thư viện tạo lại consumer trước tin đầu tiên, "bây giờ" đã trôi và sót tin.
- Watchdog nhớ lúc bắt đầu thấy trễ (`behindSince`), nên stream im lặng lâu rồi có tin mới không bị coi là trễ 2 phút; hai lần nhảy cách nhau ít nhất 60s, để giờ node lệch NATS không gây nhảy liên tục.
- Hàng ghi đầy theo số frame (256) cũng đóng 4008, không cắt socket không mã (1006) như phần 1.
- `pkg/wsclient` thêm `CloseAsync()` để đóng từ trong handler; `Close()` chờ tối đa 5s rồi báo `ErrHandlerStillRunning`.
- Diễn tập tìm ra một lỗi nặng: watchdog đọc vị trí stream không giữ khoá nên nhảy nhầm `consumer_lost` (2 lần trong 60s ở 2K tin/s). Đã sửa (`ec75155`), review lại đạt.

**Kiểm thử:** unit, `make itest`, e2e phase 1–9 xanh; phase 9 có hai người A/B qua websocket, 9 bước, 0 trùng, 0 lỗ.

| Diễn tập (~200 tin/s, client giữ kết nối) | Kết quả |
|---|---|
| Cắt websocket-1 khỏi NATS 10s | Đuổi kịp 5s sau khi nối lại; 0 nhảy, 0 `resync`; 2.000/2.000 tin, không mất |
| Cắt 3 phút | 1 nhảy `lag_too_old`, mỗi kết nối 1 `resync`; 3.731 tin thiếu đều nằm trong lỗ đã báo, 0 mất âm thầm |
| Restart NATS (sau khi sửa) | 0 nhảy, 0 `resync`; 2.997/2.997 tin, 0 trùng, 0 lỗ |
| 2K tin/s đều 60s (sau khi sửa) | 0 nhảy, 0 `resync`, 0 nạp lại; trễ tối đa 2,3ms |

**Rủi ro (a), (b) — owner chốt cách sửa 2026-10-22, đang sửa:**
- (a) Tin cũ "vào nhóm" do sweeper phát bù muộn có thể làm nhóm đã rời "quay lại" cho một phiên → sửa bằng `ListMyRoomIDs` trả cả nhóm đã rời kèm phiên bản (§4).
- (b) Sau khi nhảy, người bị xoá ở đoạn bị bỏ qua có thể còn nhận tin tới lượt nạp lại → sửa bằng đọc lại tin vào/bị xoá của đoạn bị bỏ qua từ JetStream rồi kick ngay; bỏ nạp lại qua core (§7).

**Rủi ro còn lại:**
- (c) Quyền NATS riêng cho websocket ở M5: tới lúc đó ai vào được NATS là giả được gói đẩy tới người dùng. Chặn go-live.
- (d) Giờ node lệch NATS > 2 phút: cooldown chặn nhảy liên tục nhưng cần alert `catchup_jump_suppressed_total` (phần 5).
