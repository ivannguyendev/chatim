# Roadmap — chatim

> Cập nhật: 2026-10-15 (M3 xây xong trên `feat/m3`, chờ PR; 2026-10-14: hoãn sửa effect engine và sharding, owner thảo luận cuối M3; 2026-10-13: core xếp danh sách hội thoại và đếm unread, client gọi thẳng core; app "Inbox/sync" đổi tên Mention App; 2026-10-12: thu hẹp M3 thành API truy vấn của core; M2c merge `main` PR #14 2026-10-13; M2b.0–M2b.4 đã merge `main`, PR #12; M2b.3 xong 2026-10-06; viết lại 2026-10-05 sau 2 vòng phản biện cơ chế hệ thống). Thiết kế: [architecture.md](architecture.md) · Báo cáo nghiên cứu cơ chế: [research/261005-system-mechanisms-synthesis-report.md](research/261005-system-mechanisms-synthesis-report.md) · PoC: [poc/README.md](poc/README.md)

## Quy tắc viết plan

- `docs/plans/` chỉ chứa tài liệu để owner review và duyệt (`<plan>-summary.md`); plan execute chi tiết cho AI nằm ở `.claude/plans/<plan>.md` (local, gitignore; owner chốt 2026-10-09). Mỗi milestone có plan riêng, viết **trước khi code**. Plan **không chứa code** (owner chốt 2026-10-07; thay cho hướng dẫn "code đầy đủ" của skill `writing-plans`): mỗi task ghi mục tiêu, file tạo/sửa, todo, kỹ thuật (API, toán tử Mongo, index, thứ tự, mã lỗi), tên và hợp đồng mới viết bằng lời, test cần viết và mỗi test chứng minh gì, lệnh chạy, tiêu chí xong, commit. Người thực thi tự viết code theo TDD; gặp chỗ plan không nói tới thì dừng và báo.
- Plan mỗi phase/milestone mở đầu bằng bảng **scope và ranh giới** (owner chốt 2026-10-12): việc nào trong scope và service nào giữ; việc nào ngoài scope và thuộc về đâu (core, websocket, app nào). Core cung cấp API dữ liệu (gửi, đọc, sửa, xoá, lịch sử, reaction, vị trí đọc…) và phát event, một lần gọi mỗi màn hình; giá trị theo người xem tính lúc đọc (unread, danh sách hội thoại đã xếp) thuộc core từ 2026-10-13; trạng thái lưu theo người xem và sync là việc của app.
- Plan mở đầu bằng bảng **tính năng → lớp dữ liệu** ([thiết kế §4](architecture.md#4-khung-lớp-dữ-liệu-data-class-framework)). Mỗi tính năng khai báo: fact (lệnh atomic nào, doc nào), cách idempotent (cid hoặc `base_ver`), effect + chính sách, quyền (permission hook), view theo người đọc, dòng trong danh mục event, ngân sách khuếch đại ở nhóm 5K và channel 200K, guarantee + detector. Tính năng không khớp lớp nào thì dừng, thiết kế lớp đó trước.
- Dữ liệu suy ra (counter, projection) ghi ngay trong lệnh mà không gộp được với lần ghi chính vào một transaction: dùng **phiếu hẹn sinh tồn** ([thiết kế §7.1](architecture.md), D111), không thiết kế lại từ đầu.
- Bộ test chung mỗi PR: fact sinh đúng effect; no-op không sinh effect; lỗi không sinh effect; parity fast path / reconciler; chạy effect hai lần cho cùng kết quả.
- Mỗi plan có file **tóm tắt kỹ thuật** đi kèm `docs/plans/<plan>-summary.md` (owner chốt 2026-10-06, sửa 2026-10-07), tiếng Việt, không có code Go, viết **trước khi thực thi**; owner phản biện và duyệt bản này, plan chi tiết chỉ dành cho AI. Thực thi xong, cập nhật bản tóm tắt theo cái đã xây (lệch so với plan, known issues) để làm tài liệu lưu trữ. Nội dung bắt buộc:
  1. bảng thuật ngữ;
  2. bức tranh chung có sơ đồ;
  3. bảng field đầy đủ (tên, nghĩa, ví dụ, dùng để làm gì);
  4. luồng xử lý từng use case có sơ đồ sequence, ghi rõ đọc/ghi gì, kiểm gì, mã lỗi gRPC;
  5. cơ chế đúng đắn và các ca chạy đua;
  6. event và subject;
  7. thư viện và hạ tầng;
  8. quyết định kèm phương án bị loại và lý do;
  9. chi phí và tải;
  10. rủi ro và giới hạn;
  11. điểm đội tự chọn để owner phản biện;
  12. kiểm thử, mỗi mục chứng minh gì.

  Chỗ nào làm việc phải xếp hàng (đánh số liên tục, khoá chung) phải nêu rõ.
- Tên field DB (owner chốt 2026-10-07): tiếng Anh đầy đủ, từ cơ bản, cho mọi collection trừ `messages` (giữ tên ngắn, có bảng tra trong thiết kế §5); audit `updated_at` / `updated_by`; bộ đếm chỉ tăng, có lỗ đuôi `_ver`; id chống gửi lại `request_id`.

## Milestone

| Phase | Milestone | Nội dung | Trạng thái |
|---|---|---|---|
| 1 | M0–M2a.3 — Nền tảng, PoC dev, gửi tin + lịch sử | Monorepo, Go qua Docker, hạ tầng dev; slot ownership mềm; PoC R1–R5 trên dev; `CreateRoom`/`SendMessage`/`GetHistory` qua gRPC, actor + flusher, chống trùng cid 3 tầng gom giữa các room, ack trước Commit; event best-effort với id tự nhiên; reconciler từ change stream của `messages` + ack mark bitmap; 2 core trong compose; corebench và bench-cell | ✅ `dev-done`, đã merge vào `main` (PR #2, #5, #8–#11) |
| 1 | **M2b.0 — Nền cơ chế** | Sửa lỗi mark của publisher (chỉ mark event có chính sách ack mark, hiện chỉ `msg_created`; mark vẫn là bitmap theo seq, D65); `RECONCILE_DELAY` theo effect, `msg_created` ~5s; actor tự rút khi va doc core khác + retry backoff/jitter (D77); reader pipeline + permission hook, áp vào `GetHistory` (thiết kế §9.2); detector/alert cho guarantee đang có RC1–RC5, CD1–CD3 (D76); contract test: mọi write method của adapter là insert unique, CAS, upsert hoặc bump version | ✅ `dev-done`, đã merge `main` (PR #12) — [tóm tắt kỹ thuật](plans/2026-10-05-m2b0-mechanism-foundations-summary.md); gồm `GET /metrics` và 13 luật alert (D78) |
| 1 | M2b.1 — Effect engine | Reader trên slot 0 → JetStream work stream theo slot, id tự nhiên, worker ở mọi core, vị trí xác nhận theo work stream (D66); registry effect + chính sách (D65); chuyển reconcile `msg_created` sang engine; `room_created` là effect mới đầu tiên; room activity (`last_seq`, `last_message_at`, `last_change_at`, `activity_bucket` theo tên D96, ghi gom, D69); công cụ resync thủ công + diễn tập; đo xả backlog | ✅ `dev-done`, đã merge `main` (PR #12) — [tóm tắt kỹ thuật](plans/2026-10-05-m2b1-effect-engine-summary.md); work stream 32 partition, worker mọi core, `room_created`, room activity, `/app resync`, 15 luật alert (D79–D81) |
| 1 | M2b.2 — Sửa + xoá | Fact `message_edits` + projection `messages` (D62); `base_ver`, `prev` ở v1 (D63); ack sau projection (D64); delay xoá 2–3s; hợp đồng xoá (D75); `GetEditHistory`; quyền sửa/xoá do `access.Policy` quyết (mặc định chỉ tác giả, D86); ẩn phía tôi (`hidden` thưa) và clear history (`cleared_before_seq`, từ M2b.4 là `members.cleared_at`) qua reader pipeline; index `{room_id, created_at}` (D70) | ✅ `dev-done`, đã merge `main` (PR #12) — [tóm tắt kỹ thuật](plans/2026-10-05-m2b2-edit-delete-summary.md); `message_edits` + projection, 5 RPC, ẩn/clear ở reader pipeline (không event), effect `edit_projection` + `msg_changed` (thay delay xoá 2–3s), quyền sửa/xoá chỉ qua `access.Policy`, mặc định chỉ tác giả, loại tin khoá theo config `MESSAGE_LOCKED_KINDS` (mặc định không khoá), resync quét `message_edits` (D82–D87) |
| 1 | M2b.3 — Reaction + ghim | Tập reaction một emoji mỗi (user, tin), `reactions {_id: k│u}` + `n` (D88, D89, thay D68); counter recount CAS-ver với witness (D90, tinh chỉnh D67); `pin_actions` pv dày, bỏ `base_pv`, `PIN_LIMIT` chính xác (D92); event `reaction_changed`/`counts_changed`/`msg_pinned`/`msg_unpinned`, cid SysMsg `sys-{event_id}` (D93); quyền member (D94); emoji chỉ trong danh sách cố định `REACTION_EMOJIS`, frontend đọc qua `GetReactionSettings` (D95) | ✅ `dev-done`, đã merge `main` (PR #12) — [tóm tắt kỹ thuật](plans/2026-10-06-m2b3-reactions-pins-summary.md); 4 RPC (thêm `GetReactionSettings`), `Message.reactions` trong `GetHistory`, package `counter` + `pinproj`, feed update/replace của `reactions`, 4 effect mới, `counter_repaired_total` + 16 luật alert, resync quét `reactions` + `pin_actions` (D88–D95) |
| 1 | M2b.4 — Member + vị trí đọc | Đổi tên field các collection đã làm (trừ `messages`) và field proto `version` → `ver`; xoá lịch sử theo thời gian (`cleared_at`); `message_edits` mỗi dòng một `text`; member lớp tập (mỗi (room, user) một doc, `ver` riêng), danh sách room của user là index của `members`, `request_id` chống gửi lại; lệnh đụng owner chạy trong một transaction Mongo, xung đột trả `UNAVAILABLE` ngay, không thử lại nội bộ; `member_count` `$inc` trong lệnh + `/app recount`; người kế nhiệm theo role > `priority` (app đặt) > vào sớm nhất; vị trí đọc + chưa đọc; mọi event được bảo đảm phát (worker phát lại), kể cả ẩn tin và xoá lịch sử; subject theo loại dữ liệu `room`/`member`/`message`, chuyển tới ai do app khác thiết kế sau; mọi core nghe event member để quên cache (D96–D110) | ✅ `dev-done`, đã merge `main` (PR #12) — [tóm tắt kỹ thuật](plans/2026-10-06-m2b4-members-read-summary.md); 7 RPC member/đọc, `members` clustered + transaction owner, `$inc` số member + phiếu hẹn sinh tồn (D111), 6 effect mới, `memberwatch`, `/app recount`, resync quét `members` + `hidden`, e2e phase 5, 16 luật alert (D96–D111) |
| 1 | M2c — Thread & tiện ích | Liên kết gắn vào tin theo 2 dạng: tương tác (reaction, trả lời có trích, bookmark) chung collection `message_interactions` (chuyển reaction M2b.3 sang), mention theo đích (`mentions`) + `ListMentions` hai đường (theo tên; `@all`/`@nhóm` qua room mình đang ở) với cờ đã đọc theo vị trí đọc; mọi số trên tin (`rx`, `rc`) `$inc` + phiếu hẹn sinh tồn, delta không kẹp; không có nhánh thread riêng, chặn xoá tin còn trả lời; forward (cid); `OpenDirectRoom` (sổ `room_dms`) tách khỏi `CreateRoom` (group, `request_id`); [việc tồn đọng R1–R5](#việc-tồn-đọng-đưa-vào-m2c-owner-chốt-2026-10-09--đã-đóng) | ✅ `dev-done`, đã merge `main` (PR #14, 2026-10-13) — [tóm tắt kỹ thuật](plans/2026-10-10-m2c-threads-mentions-summary.md); 5 RPC mới (`OpenDirectRoom`, `SetBookmark`, `GetReplies`, `ListBookmarks`, `ListMentions`), 4 effect mới (`reply_mention_index`, `count_repair`, `count_event`, `bookmark_event`), bỏ `reaction_counter` và package `counter`, kind phiếu 11–12, resync quét `message_interactions` + phiếu đếm lại, e2e phase 6, 17 luật alert (thêm `ChatimReconcilePublishFailing`) (D112–D119); [2 câu hỏi của owner đã đóng](#câu-hỏi-của-owner-từ-m2c--đã-đóng) |
| 1 | M3 — Core: API truy vấn còn thiếu | **Scope (owner chốt 2026-10-12, sửa 2026-10-13):** mỗi màn hình một lần gọi core; danh sách hội thoại do core xếp (ghim trước, rồi tin mới nhất, kể cả tin hệ thống), mỗi room kèm xem trước và unread R17, không dấu `@` (`ListMyRooms` con trỏ theo khoá xếp, hai đường theo ngưỡng `ROOM_LIST_JOIN_MAX` (2.000, owner hạ còn 1.500 ngày 2026-10-15 theo số đo): join `members → rooms` ở DB hoặc đọc từng bước rồi xếp ở core; cache thông tin room và cache đuôi room ~300 tin trong RAM core dùng chung cho mọi người đọc; client gọi qua websocket, không qua Mention App, tự áp event); ghim/tắt thông báo/lưu trữ hội thoại (`SetRoomPreferences`); `GetHistory` neo `AROUND`; `GetMessages` (theo danh sách seq); `ListThreads` (tin có trả lời gần đây, collection `reply_threads`); `GetReactions` (ai react); `ListPins`; `ListMembers`; `UpdateRoom` (tên, ảnh nhóm, `room_updated`); emoji và bookmark của người đọc trên `GetHistory`/`GetMessages`/`GetReplies`; `SendMessage` cho caller nội bộ có `kind` (`system`) + cờ `skip_unread` + `meta` (R23, P7); test sẵn sàng sharding hoãn, owner thảo luận riêng (D120–D140). [Tóm tắt kỹ thuật (owner duyệt 2026-10-13, viết lại theo cái đã xây)](plans/2026-10-12-m3-core-query-apis-summary.md). **Ngoài scope:** sync token, room-tail cache, đồng bộ đa thiết bị → owner xác định lại nơi làm; gom MarkRead, reconnect jitter → M4. Mentions + `ListMentions`, bookmarks đang ở core: không chuyển ở M3, owner quyết sau; `MENTION_HIDE_READ_TENANTS` giữ tới lúc đó. Số thật (owner 2026-10-12): 99% user dưới 200 room, 1% user quan trọng ở rất nhiều room (thiết kế theo nhóm này, mốc ~5.000); ~100K kết nối, kết nối lại trải ≥ 1 phút | Xây xong trên `feat/m3`, chờ PR (`dev-done` khi merge) — [tóm tắt kỹ thuật](plans/2026-10-12-m3-core-query-apis-summary.md); 9 RPC mới (33 tổng), package `api/roomlist` + `api/roomcache` (cache thông tin room, đuôi room TTL 30s, ảnh chụp, đường), collection `reply_threads`, phiếu kind 13/14 + effect `room_event`, `preferences_event`, tin `system`, luật tra `_id` có cận trên collection clustered (D139), `roomlistbench` (trang đầu lạnh 25–31 truy vấn, nóng 2–8), e2e phase 7; 17 luật alert. Hoãn chờ owner (D140): sửa effect engine chặn đầu hàng (danh sách hội thoại trễ ~5s) và sharding — [hai chủ đề thảo luận](plans/2026-10-15-m3-open-topics.md) |
| 1 | M4 — WebSocket | App `websocket` (đổi tên từ `gateway` 2026-10-12: giữ kết nối WebSocket, đẩy realtime, chuyển lệnh chat tới core; không phải cửa vào duy nhất). WebSocket (gws), JWT/JWKS, frame protobuf, fanout theo interest, hàng đợi gửi có giới hạn, định tuyến theo slot; gom `MarkRead`: SDK gửi vị trí mới nhất tối đa ~2s một lần và khi rời room, app websocket chặn thêm theo (kết nối, room), giữ seq lớn nhất, chuyển nốt khi rời room/mất kết nối, rồi gọi thẳng core (một tin tới group có 1.000 người đang xem không được sinh 1.000 lượt ghi); SDK reconnect jitter; typing/presence ephemeral không qua core; sub `live.{t}.*.{rid}.>` theo room (D108); chọn người nhận event member/đọc qua app phân phối (thiết kế sau, D104); `member_removed` → unsubscribe; chế độ event không text cho tenant xoá chặt; không chuyển `kind`/cờ unread từ client; hằng số header tenant/user vào `pkg` | Chưa |
| 1 | Mention App | App riêng (đổi tên từ "Inbox/sync" 2026-10-13), chỉ dùng API core + event `CHATIM_EVT`. Danh sách hội thoại và unread **không** thuộc app này: client lấy thẳng từ core (`ListMyRooms`, `GetRooms`, M3; owner 2026-10-13). Việc còn lại để owner xác định: badge `@` từng room (`last_all_seq > read_seq`, owner 2026-10-11), sync token (D74), đồng bộ đa thiết bị, có chuyển mentions/bookmarks sang không. Thiết kế riêng trước khi xây | Chưa — sau M3, owner xác định vai trò |
| 1 | M5 — Hardening | Load test 100K kết nối, chaos test (kể cả R5 thật), OTel/Prometheus/Grafana, CI đầy đủ, ghim digest image, container theo uid, Sentinel (`FailoverClient`) cho cả hai Redis; mục mang sang từ M2b.0–M2b.4 (xem dưới) | Chưa |
| 1 | Channel | Room type channel (~200K subscriber, chỉ admin post): quyền post, fanout, số trên tin nóng (gom `counts_changed` của tin nhận nhiều react/s; bắt buộc trước khi channel go-live) | Chưa — chốt milestone sau M3 |
| 1 | PoC prod-like (song song) | **Trước khi chạy: hỏi owner số thật.** Mongo rs 3 member NVMe, NATS 3 node, 2 host Linux, dữ liệu thật ≥1M tin; P1–P10 ở [poc/README.md](poc/README.md) → chốt database, go/no-go A1 | Chờ hạ tầng |
| 2 | App `auth`, `api`, `events`, `push`, `migrator` | JWT/JWKS theo tenant; REST/BFF; gRPC stream cho app ngoài; push (badge xấp xỉ, D73); migrator dual-write/import (chế độ import im lặng để reconciler không phát hàng tỷ event) | Sau Phase 1 |
| 2 | Policy quyền | Module policy chat: ánh xạ user → role → quyền (owner/moderator xoá tin người khác, giới hạn thời gian sửa/xoá theo tenant…), cắm vào `access.Policy` (M2b.0) thay `access.DefaultPolicy` (sửa/xoá chỉ tác giả, D86; loại tin khoá qua `Request.Kind`, D87) | Sau Phase 1 |

## Thứ tự phụ thuộc

- M2b.0 → M2b.1 → M2b.2 → M2b.3 → M2b.4 → M2c → M3 → M4 → M5; Mention App sau M3 (dùng API của M3), song song M4 được. Hai chủ đề hoãn từ M3 (effect engine, sharding) chờ owner chọn milestone ([open topics](plans/2026-10-15-m3-open-topics.md)).
- M2b.0 là điều kiện của mọi milestone sau: permission hook, reader pipeline và detector dùng chung.
- (owner 2026-10-05) M2b.0 → M2b.4 làm trên một nhánh chung `feat/m2b` (tạo từ `docs/system-mechanisms`), không PR từng phần; merge vào `main` một lần khi M2b.4 đạt Definition of Done. **2026-10-08: M2b.0–M2b.4 `dev-done`, đã merge `main` (PR #12)**. **2026-10-13: M2c `dev-done`, đã merge `main` (PR #14)**; M3 làm trên `feat/m3`. Từ nay mỗi milestone một nhánh chung `feat/<milestone>`, một PR (chuẩn trong [git-workflow.md](git-workflow.md#nhánh-và-pr)).
- M2b.1 phải xong trước mọi tính năng có fact mới, vì effect của chúng chạy trên engine.
- PoC prod-like chạy song song, xong trước M5 (chốt database). Storage nằm sau port nên PostgreSQL chỉ cần thêm adapter.
- Phase 2 bắt đầu khi M4 xong.

## Việc tồn đọng đưa vào M2c (owner chốt 2026-10-09) — đã đóng

Đóng hết ở M2c (2026-10-10), chi tiết ở [tóm tắt M2c](plans/2026-10-10-m2c-threads-mentions-summary.md) mục 4.7 và D113, D117–D119.

- **R1 — Bỏ vòng thử lại nội bộ:** đóng. Ghim một lượt (append trùng `pin_ver` → `UNAVAILABLE`; `Project` một lượt, fast path trả fold cục bộ, worker Nak; D119); counter reaction không còn touch vì mọi số trên tin là `$inc` + phiếu hẹn (D113). Owner 2026-10-10 chốt luật tuyệt đối (code của mình không thử lại, kể cả thao tác idempotent): actor không gửi lại insert `Unknown`, không xếp lại nhóm `ErrNotSent`, chỉ một `Find`; reader reconcile publish lỗi thì kết thúc term, term sau đọc lại từ vị trí đã xác nhận (D119).
- **R2 — Hai known issue của M2b.2:** đóng. `Config.LogValue` log mọi field không bí mật (duyệt tự động, có test bắt field mới); `GetEditHistory` trả rỗng khi fact mới nhất là xoá.
- **R3 — `room_created` đếm republish sai:** đóng. Chỉ đếm PubAck không trùng.
- **R4 — Cách sinh room id:** đóng. Giữ id 63-bit ngẫu nhiên; DM qua sổ `room_dms` + `OpenDirectRoom` (D117); `CreateRoom` chỉ group, `request_id`, rút id một lần, trùng → `UNAVAILABLE` (D118).
- **R5 — Điểm mở D77:** đóng. Owner chọn bỏ: va doc core khác → `UNAVAILABLE` + nhường room ngay, không gán lại seq (D119); slot R5 `-count=20` xanh.

## Câu hỏi của owner từ M2c — đã đóng

- **(a) `ListMentions` khi user có nhiều room/nhóm:** đóng 2026-10-11. Hai đường đọc: `@mình` theo tên; `@all`/`@nhóm` chỉ qua các room mình đang ở, lô 200 room, bỏ room thừa và dừng sớm nhờ `rooms.last_shared_mention_at`; `@nhóm` ở room mình không ở thì không thấy; chỉ thấy tin sau lúc vào room/clear; trang 20; đã đọc = vị trí đọc của room, tenant `mark`/`hide` (D115).
- **(b) Gửi lại cùng seq khi insert `Unknown`:** đóng 2026-10-10. Bỏ theo luật tuyệt đối "code của mình không thử lại": một lần `Find`, chưa rõ thì `UNAVAILABLE`, giữ cid; actor nhớ lần ghi chưa rõ theo cid để lần gửi lại của client nhận đúng seq gốc (D119).

## Mục mang sang M5 (hardening)

Hai mục đầu cần owner thảo luận trước khi làm (D140); vấn đề, ví dụ, số đo, phương án và câu hỏi cần chốt ở [2026-10-15-m3-open-topics.md](plans/2026-10-15-m3-open-topics.md); owner có thể chọn làm ở milestone riêng trước M4.

Từ M3 (owner 2026-10-14, cần thảo luận trước khi làm): **sharding**. Thử trên 2 shard (`bin/m3-shard-profile.patch`): `members`, `message_interactions`, `mentions` có `_id` dài khác nhau nên shard theo `{_id: 1}` (design §5.1) không giữ một room/tin trên một shard, `ListMembers`/`GetReactions` hỏi mọi shard; hướng có thể: shard theo `{room_id: 1}` hoặc khoá dài cố định. Change feed qua mongos trễ tới ~10s khi một shard rảnh (`periodicNoopIntervalSecs`). Transaction owner (D100) và feed vẫn chạy qua mongos.

Từ M3 (owner 2026-10-14): **effect engine chặn đầu hàng** — effect có delay (`msg_created`, `RECONCILE_DELAY` 5s) giữ cả lô, effect delay 0 (`room_activity`, `reply_mention_index`, `pin_projection`, `mention_read`, count repair) trễ 5–20s, thông lượng ~1,6K phiếu/s mỗi core; phiếu lỗi Nak 5s mãi (không MaxDeliver/BackOff) có thể làm nghẹn partition; `Fetch` chờ đủ lô tới 0,9s; `reconcile_lag_seconds` không tách giai đoạn nên alert không thấy trễ 5s. Thử nghiệm hai giai đoạn ở M3 sửa được trễ ở 1K nhưng làm 5K tệ hơn (lô nhỏ: gấp 5 lần ghi `rooms`, giai đoạn có delay chạy từng lô nhỏ); bản vá và số đo trong `bin/m3-engine-two-stage.patch`, `bin/bench/m3-engine-*`. Hướng tiếp: gộp lô đã tới hạn cùng kind/effect + cửa sổ gom ngắn (`WORK_FETCH_LINGER` ~50ms), phiếu lỗi giãn dần bằng `NakWithDelay` + alert (owner đã chọn), lưu ý work stream `MaxAge` 2h vẫn xoá phiếu kẹt (khôi phục bằng `/app resync`).

Từ M3, chi tiết ở [tóm tắt kỹ thuật, mục 10](plans/2026-10-12-m3-core-query-apis-summary.md#10-rủi-ro):

- Chưa đo đợt kết nối lại hàng loạt và core lạnh sau deploy (PoC P7); ước lượng byte của entry cache đuôi dùng hằng.
- Resync: room chỉ đổi tên hoặc chỉ đổi cài đặt hội thoại trong khoảng mất cần `-room`; lần đổi tên đầu của room tạo trước M3 không phát lại.
- Feed `members` xếp loại theo `ver` → `read_ver` → `cleared_at` → `preference_ver`: lệnh sau này ghi `read_ver` và `preference_ver` trong một update sẽ che phiếu `PreferenceChanged`.
- Tra `_id` 1.000 khoá rời: `$or` 1.000 nhánh, 61–90ms trên dev.
- Tin `system` không kiểm membership tới khi có mTLS; policy `send_system_message` nhận `Member` rỗng.
- Khoá singleflight của cache đuôi gồm `last_seq` (ít gộp hơn khi bão); `Watch.Stop` không đợi callback đang chạy; `preferences_event` của member đã rời vẫn phát event.

Từ M2b.0, chi tiết ở [tóm tắt kỹ thuật, mục Kết quả thực thi](plans/2026-10-05-m2b0-mechanism-foundations-summary.md#12-kết-quả-thực-thi-2026-10-05):

- `room_yields_total` có thể đếm thừa khi hai entry cùng hết lượt trong một group hoặc slot đã yêu cầu retire.
- Event còn trong hàng đợi khi publisher bị abort cưỡng bức không được đếm.
- `reconcile_republished_total` đếm số lần gửi, nên thay đổi phát lại sau khi term restart bị đếm lại.
- `ack_marks_dropped_total{reason=marker_failed}` là cận trên (đếm cả batch).
- Chưa có test trực tiếp cho counter `MarkQueueFull` và `Malformed`.
- Probe oplog đọc primary hai lần mỗi lần scrape, không cache.
- Metric reconcile lấy một snapshot `Stats` cho mỗi metric.
- `make vuln`: một cảnh báo cấp module GO-2026-5932 (`golang.org/x/crypto/openpgp`), có từ trước, code không gọi tới; xử lý khi nâng phụ thuộc.

Từ M2b.1, chi tiết ở [tóm tắt kỹ thuật, mục Kết quả thực thi](plans/2026-10-05-m2b1-effect-engine-summary.md#13-kết-quả-thực-thi) (danh sách Minor đầy đủ theo task nằm trong plan execute `.claude/plans/`, local):

- Effect `Run` panic không được recover, làm sập core.
- Effect trả kết quả sai độ dài không được log, record bị retry im lặng mỗi `WORK_RETRY_DELAY`.
- Bước dừng worker (`WORK_DRAIN + 1s` = 2s) có thể ngắn hơn thời gian chờ PubAck, nên dưới tải thấy "shutdown incomplete" (record bị `Nak`, không mất); cân nhắc luật `WORK_DRAIN` so với `PUB_ACK_TIMEOUT`.
- Resync dừng ở tin cũ đầu tiên (giả định `CreatedAt` tăng đơn điệu theo seq; migrator hoặc lệch đồng hồ có thể làm dừng sớm).
- Change stream cấp database cần quyền `changeStream` trên cả database (role prod).

Từ M2b.2, chi tiết ở [tóm tắt kỹ thuật, mục Kết quả thực thi](plans/2026-10-05-m2b2-edit-delete-summary.md#13-kết-quả-thực-thi):

- Resync: `ErrEditPageFull` khi một thời điểm có ≥ 1000 fact sửa của một room (chỉ báo lỗi, chưa phân trang theo `_id`).
- `msg_changed` làm `At` + `Find` cho từng record, chưa gom theo room (đủ ở 100–300 lệnh đổi/s; cần gom khi xả backlog lớn).
- `ApplyEdit` trả nil cả khi tin không còn, nên nhánh `ErrMessageNotFound` của `edit_projection` không bao giờ chạy; fact mồ côi chỉ bị `msg_changed` drop có đếm sau `RECONCILE_DELAY`.
- Retry chỉ được nhận khi version hiện tại = `base+1`; tác giả sửa tiếp giữa lúc mất ack và lúc retry thì retry nhận `FAILED_PRECONDITION` (phạm vi D63).
- Record work 33 byte còn tồn trong work stream lúc deploy bản 37 byte bị Term như bản ghi hỏng (`BadRecordsError`, đếm vào metric failures); chưa có dữ liệu prod, khi có thì xả work stream trước khi nâng cấp hoặc `/app resync` khoảng đó.

Từ M2b.3, chi tiết ở [tóm tắt kỹ thuật, mục Kết quả thực thi](plans/2026-10-06-m2b3-reactions-pins-summary.md#13-kết-quả-thực-thi):

- Target nóng: mỗi touch đếm lại O(số reaction của tin) và các CAS `rx.v` tranh nhau; cửa sổ gom W + bucket `hash(u) % K` để milestone Channel (D90).
- Rolling deploy: core cũ `Term` record 38+ byte và record kind 5 (`PinInserted`); nâng mọi core trước khi có record mới (D91). Core M2b.3 `Nak` kind lạ không giới hạn số lần nên `ChatimWorkFailing` kêu trong lúc còn core cũ; một fetch có thể đếm cùng record nhiều lần khi `WORK_FETCH_WAIT` > `WORK_RETRY_DELAY`.
- `ReactMessage` không có cid: retry trễ có thể đưa reaction về trạng thái cũ (D89).
- Bốn event mới không có ack mark: event reaction trung gian có thể mất, chỉ trạng thái cuối được bảo đảm (D93).
- Resync: `ErrReactionPageFull`/`ErrPinPageFull` khi một thời điểm đầy một trang 1000 (như `ErrEditPageFull`); chọn room theo `activity_bucket` nên room chỉ có reaction/ghim trong khoảng mất cần `-room` (D91); `{room_id, updated_at}` của `reactions` không có prefix `_id` nên `Between` sẽ scatter-gather khi đã shard (D88).
- `effects`: lỗi `publish.Message` đếm drop một lần mỗi nhóm chứ không mỗi record; ba cache loại room mới theo effect (bộ nhớ); `reaction_counter` drop có đếm cả lỗi vĩnh viễn `ErrInvalidArgument`.
- Lỗi của `Nak`/`Term` trong work queue bị bỏ qua (record giao lại sau `AckWait`).

Từ M2b.4, chi tiết ở [tóm tắt kỹ thuật, mục Kết quả thực thi](plans/2026-10-06-m2b4-members-read-summary.md#13-kết-quả-thực-thi):

- `memberwatch` dùng NATS core: event `member_removed` mất khi rớt kết nối không được phát lại; chỉ TTL 10s chặn (cả lúc router dừng).
- Lệch đồng hồ giữa core xử lý clear (`cleared_at`) và actor gán `ts` có thể để sót/ẩn thừa tin gửi sát lúc clear.
- Mongo `Hide`/`ClearHistory` nên cắt ms rõ ràng như memstore; `time.Now()` trong `read_position.go` không tiêm được.
- Transaction owner: abort/`EndSession` dùng `WithoutCancel` không hạn (nên thêm timeout); lỗi mạng/deadline không nhãn trả nguyên thay vì `ErrRetryLater`; query owner sắp trong RAM.
- `MembersBetween` quét mọi member của room rồi sắp (cần index `{room_id, last_change_at}` nếu resync room lớn chậm); resync phát lại `ReadChanged` cho mọi member vừa vào.
- `/app recount` vẫn ghi và tăng `member_count_ver` khi số đã đúng (worker thì không).
- Bản ghi `ReadChanged` bị chặn ở `MaxUint32` không bao giờ phát lại (sau 4 tỷ lần đổi vị trí đọc).
- `GetEditHistory` trang đầu có thể trả `limit+1` dòng (thêm dòng `ver 0`).
- Itest đua hiếm `TestReactionSetOfTheSameUserRacingOnANewReactionNeverFails` (Mongo `DurationOverflow`), theo dõi.
- Test còn thiếu: `Begin`/`Finish` song song của `dedupe.Requests`; nối `Config.Limits.MemberBatch` vào `Mutator`; `Load()` của `MEMBER_COUNT_CHECK_DELAY`; watch start trước gRPC.

Từ M2c, chi tiết ở [tóm tắt kỹ thuật, mục 10](plans/2026-10-10-m2c-threads-mentions-summary.md#10-rủi-ro):

- `msg_created` còn đếm mọi PubAck (cả bản trùng) vào `reconcile_republished_total`; `LockedMessageKinds` log dạng số.
- Sau va seq, các lệnh Duplicate khác trong nhóm vẫn chờ xác nhận tới hạn nhóm.
- Resync chỉ quét timeline chính; dedupe phiếu đếm lại theo room trong RAM.
- Chưa có itest đầu-cuối "`$inc` lỗi → phiếu bật sửa" và "chết giữa `AddReply` và `$inc`" (unit phủ).
- React như cũ vẫn hẹn + gỡ phiếu; mỗi phiếu sửa tốn một lần đọc đích mention; mỗi trả lời một lượt hẹn/ghi/`$inc`/phát tuần tự trong worker (đo thông lượng ở PoC prod-like).
- `domain.MsgKey` trùng hình dạng với `store.MsgKey`.

## Mức sẵn sàng

Theo [git-workflow.md](git-workflow.md#mức-sẵn-sàng): `dev-done` → `prod-like validated` → `go-live`. M0–M2a.3: `dev-done`, đã merge vào `main` (M2a.2 + M2a.3 cùng PR #11). M2b.0–M2b.4: `dev-done`, đã merge vào `main` (PR #12, 2026-10-08). M2c: `dev-done`, đã merge vào `main` (PR #14, 2026-10-13). M3: xây xong trên `feat/m3` (2026-10-15), `dev-done` khi PR merge. Go-live còn cần: oplog `minRetentionHours` ≥ 24h, alert RC5 và lag, đo bộ nhớ NATS, đo lại trên prod-like. Chưa milestone nào `prod-like validated`; chưa có tag release.
