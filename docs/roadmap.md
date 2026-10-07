# Roadmap — chatim

> Cập nhật: 2026-10-06 (M2b.3 xong; viết lại 2026-10-05 sau 2 vòng phản biện cơ chế hệ thống). Thiết kế: [designs/261005-chatim-architecture.md](designs/261005-chatim-architecture.md) · Báo cáo nghiên cứu cơ chế: [research/261005-system-mechanisms-synthesis-report.md](research/261005-system-mechanisms-synthesis-report.md) · PoC: [poc/README.md](poc/README.md) · Bản cũ (roadmap trước, plan M0–M2a.3, thiết kế Phase 1, 8 file phản biện cũ): [archive/](archive/README.md)

## Quy tắc viết plan

- Mỗi milestone có plan riêng trong `docs/plans/`, viết **trước khi code**. Plan **không chứa code** (owner chốt 2026-10-07; thay cho hướng dẫn "code đầy đủ" của skill `writing-plans`): mỗi task ghi mục tiêu, file tạo/sửa, todo, kỹ thuật (API, toán tử Mongo, index, thứ tự, mã lỗi), tên và hợp đồng mới viết bằng lời, test cần viết và mỗi test chứng minh gì, lệnh chạy, tiêu chí xong, commit. Người thực thi tự viết code theo TDD; gặp chỗ plan không nói tới thì dừng và báo.
- Plan mở đầu bằng bảng **tính năng → lớp dữ liệu** ([thiết kế §4](designs/261005-chatim-architecture.md#4-khung-lớp-dữ-liệu-bắt-buộc-cho-mọi-plan)). Mỗi tính năng khai báo: fact (lệnh atomic nào, doc nào), cách idempotent (cid hoặc `base_ver`), effect + chính sách, quyền (permission hook), view theo người đọc, dòng trong danh mục event, ngân sách khuếch đại ở nhóm 5K và channel 200K, guarantee + detector. Tính năng không khớp lớp nào thì dừng, thiết kế lớp đó trước.
- Bộ test chung mỗi PR: fact sinh đúng effect; no-op không sinh effect; lỗi không sinh effect; parity fast path / reconciler; chạy effect hai lần cho cùng kết quả.
- Mỗi plan có file **tóm tắt kỹ thuật** đi kèm `docs/plans/<plan>-summary.md` (owner chốt 2026-10-06, sửa 2026-10-07), tiếng Việt, không có code Go, viết **trước khi thực thi**; owner phản biện và duyệt bản này, plan chi tiết chỉ dành cho AI. Nội dung bắt buộc:
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
| 1 | **M2b.0 — Nền cơ chế** | Sửa lỗi mark của publisher (mark theo event id + loại, D65); `RECONCILE_DELAY` theo effect, `msg_created` ~5s; actor tự rút khi va doc core khác + retry backoff/jitter (D77); reader pipeline + permission hook, áp vào `GetHistory` (thiết kế §9.2); detector/alert cho guarantee đang có RC1–RC5, CD1–CD3 (D76); contract test: mọi write method của adapter là insert unique, CAS, upsert hoặc bump version | ✅ `dev-done` (trên `feat/m2b`, chưa merge `main`) — [plan](plans/2026-10-05-m2b0-mechanism-foundations.md); gồm `GET /metrics` và 13 luật alert (D78) |
| 1 | M2b.1 — Effect engine | Reader trên slot 0 → JetStream work stream theo slot, id tự nhiên, worker ở mọi core, vị trí xác nhận theo work stream (D66); registry effect + chính sách (D65); chuyển reconcile `msg_created` sang engine; `room_created` là effect mới đầu tiên; room activity (`last_seq`, `last_msg_at`, `last_change_at`, `act_bucket`, ghi gom, D69); công cụ resync thủ công + diễn tập; đo xả backlog | ✅ `dev-done` (trên `feat/m2b`, chưa merge `main`) — [plan](plans/2026-10-05-m2b1-effect-engine.md); work stream 32 partition, worker mọi core, `room_created`, room activity, `/app resync`, 15 luật alert (D79–D81) |
| 1 | M2b.2 — Sửa + xoá | Fact `message_edits` + projection `messages` (D62); `base_ver`, `prev` ở v1 (D63); ack sau projection (D64); delay xoá 2–3s; hợp đồng xoá (D75); `GetEditHistory`; quyền sửa/xoá do `access.Policy` quyết (mặc định chỉ tác giả, D86); ẩn phía tôi (`hidden` thưa) và clear history (`cleared_before_seq`) qua reader pipeline; index `{room, ts}` (D70) | ✅ `dev-done` (trên `feat/m2b`, chưa merge `main`) — [plan](plans/2026-10-05-m2b2-edit-delete.md); `message_edits` + projection, 5 RPC, ẩn/clear ở reader pipeline (không event), effect `edit_projection` + `msg_changed` (thay delay xoá 2–3s), quyền sửa/xoá chỉ qua `access.Policy`, mặc định chỉ tác giả, loại tin khoá theo config `MESSAGE_LOCKED_KINDS` (mặc định không khoá), resync quét `message_edits` (D82–D87) |
| 1 | M2b.3 — Reaction + ghim | Tập reaction một emoji mỗi (user, tin), `reactions {_id: k│u}` + `n` (D88, D89, thay D68); counter recount CAS-ver với witness (D90, tinh chỉnh D67); `pin_actions` pv dày, bỏ `base_pv`, `PIN_LIMIT` chính xác (D92); event `reaction_changed`/`counts_changed`/`msg_pinned`/`msg_unpinned`, cid SysMsg `sys-{event_id}` (D93); quyền member (D94); emoji chỉ trong danh sách cố định `REACTION_EMOJIS`, frontend đọc qua `GetReactionSettings` (D95) | ✅ `dev-done` (trên `feat/m2b`, chưa merge `main`) — [plan](plans/2026-10-06-m2b3-reactions-pins.md); 4 RPC (thêm `GetReactionSettings`), `Message.reactions` trong `GetHistory`, package `counter` + `pinproj`, feed update/replace của `reactions`, 4 effect mới, `counter_repaired_total` + 16 luật alert, resync quét `reactions` + `pin_actions` (D88–D95) |
| 1 | M2b.4 — Member + vị trí đọc | Đổi tên field các collection đã làm (trừ `messages`) và field proto `version` → `ver`; xoá lịch sử theo thời gian (`cleared_at`); `message_edits` mỗi dòng một `text`; member lớp tập (mỗi (room, user) một doc, `ver` riêng), danh sách room của user là index của `members`, `request_id` chống gửi lại; lệnh đụng owner chạy trong một transaction Mongo, xung đột trả `UNAVAILABLE` ngay, không thử lại nội bộ; `member_count` `$inc` trong lệnh + `/app recount`; vị trí đọc + chưa đọc; mọi event được bảo đảm phát (worker phát lại), subject theo loại dữ liệu `room`/`member`/`message`, chuyển tới ai do app khác thiết kế sau (D96–D108) | ⏭ Tiếp theo — [plan](plans/2026-10-06-m2b4-members-read.md) + [tóm tắt kỹ thuật](plans/2026-10-06-m2b4-members-read-summary.md), chờ owner duyệt tóm tắt |
| 1 | M2c — Thread & tiện ích | Thread (`thread_count` là counter), mention, reply/forward (cid), bookmark | Chưa |
| 1 | M3 — Đường đọc | **Trước khi viết plan: hỏi owner số thật** (thiết kế §2.3). `ListMyRooms` qua `user_rooms`; "có tin mới" đọc từ `messages`; room-tail cache; unread theo R17 mới (exact trong S, không thì `approx`, D73); sync token `room → last_seq` + full sync (D74); SDK reconnect jitter; API caller nội bộ cho SysMsg (`kind` + cờ unread); đồng bộ đa thiết bị qua subject user; `GetMessages`/`GetReactions`/`ListPins`/`ListBookmarks`; test sẵn sàng sharding trên cluster 2 shard | Chưa |
| 1 | M4 — Gateway | WebSocket (gws), JWT/JWKS, frame protobuf, fanout theo interest, hàng đợi gửi có giới hạn, định tuyến theo slot; typing/presence ephemeral không qua core; subject user cho dữ liệu riêng; `member_removed` → unsubscribe; chế độ event không text cho tenant xoá chặt; không chuyển `kind`/cờ unread từ client; hằng số header tenant/user vào `pkg` | Chưa |
| 1 | M5 — Hardening | Load test 100K kết nối, chaos test (kể cả R5 thật), OTel/Prometheus/Grafana, CI đầy đủ, ghim digest image, container theo uid, Sentinel (`FailoverClient`) cho cả hai Redis; mục mang sang từ M2b.0, M2b.1, M2b.2 và M2b.3 (xem dưới) | Chưa |
| 1 | Channel | Room type channel (~200K subscriber, chỉ admin post): quyền post, fanout, counter lớn (cửa sổ gom W + bucket `hash(u) % K` cho reaction, D90; bắt buộc trước khi channel go-live) | Chưa — chốt milestone sau M3 |
| 1 | PoC prod-like (song song) | **Trước khi chạy: hỏi owner số thật.** Mongo rs 3 member NVMe, NATS 3 node, 2 host Linux, dữ liệu thật ≥1M tin; P1–P10 ở [poc/README.md](poc/README.md) → chốt database, go/no-go A1 | Chờ hạ tầng |
| 2 | App `auth`, `api`, `events`, `push`, `migrator` | JWT/JWKS theo tenant; REST/BFF; gRPC stream cho app ngoài; push (badge xấp xỉ, D73); migrator dual-write/import (chế độ import im lặng để reconciler không phát hàng tỷ event) | Sau Phase 1 |
| 2 | Policy quyền | Module policy chat: ánh xạ user → role → quyền (owner/moderator xoá tin người khác, giới hạn thời gian sửa/xoá theo tenant…), cắm vào `access.Policy` (M2b.0) thay `access.DefaultPolicy` (sửa/xoá chỉ tác giả, D86; loại tin khoá qua `Request.Kind`, D87) | Sau Phase 1 |

## Thứ tự phụ thuộc

- M2b.0 → M2b.1 → M2b.2 → M2b.3 → M2b.4 → M2c → M3 → M4 → M5.
- M2b.0 là điều kiện của mọi milestone sau: permission hook, reader pipeline và detector dùng chung.
- (owner 2026-10-05) M2b.0 → M2b.4 làm trên một nhánh chung `feat/m2b` (tạo từ `docs/system-mechanisms`), không PR từng phần; merge vào `main` một lần khi M2b.4 đạt Definition of Done.
- M2b.1 phải xong trước mọi tính năng có fact mới, vì effect của chúng chạy trên engine.
- PoC prod-like chạy song song, xong trước M5 (chốt database). Storage nằm sau port nên PostgreSQL chỉ cần thêm adapter.
- Phase 2 bắt đầu khi M4 xong.

## Mục mang sang M5 (hardening)

Từ M2b.0, chi tiết ở [plan, mục Kết quả thực thi](plans/2026-10-05-m2b0-mechanism-foundations.md#kết-quả-thực-thi-2026-10-05):

- `room_yields_total` có thể đếm thừa khi hai entry cùng hết lượt trong một group hoặc slot đã yêu cầu retire.
- Pause contention không thức dậy khi retire; mỗi actor bị tranh có thể dùng tới 200ms của `HookTimeout` slot.
- `contentionWait` không reset sau chu kỳ tranh mà không có gì để retry.
- Event còn trong hàng đợi khi publisher bị abort cưỡng bức không được đếm.
- `reconcile_republished_total` đếm số lần gửi, nên thay đổi phát lại sau khi term restart bị đếm lại.
- `ack_marks_dropped_total{reason=marker_failed}` là cận trên (đếm cả batch).
- Chưa có test trực tiếp cho counter `MarkQueueFull` và `Malformed`.
- Probe oplog đọc primary hai lần mỗi lần scrape, không cache.
- Metric reconcile lấy một snapshot `Stats` cho mỗi metric.
- `make vuln`: một cảnh báo cấp module GO-2026-5932 (`golang.org/x/crypto/openpgp`), có từ trước, code không gọi tới; xử lý khi nâng phụ thuộc.

Từ M2b.1, chi tiết ở [plan, mục Kết quả thực thi](plans/2026-10-05-m2b1-effect-engine.md#kết-quả-thực-thi) (đủ danh sách Minor theo task):

- Effect `Run` panic không được recover, làm sập core.
- Effect trả kết quả sai độ dài không được log, record bị retry im lặng mỗi `WORK_RETRY_DELAY`.
- Bước dừng worker (`WORK_DRAIN + 1s` = 2s) có thể ngắn hơn thời gian chờ PubAck, nên dưới tải thấy "shutdown incomplete" (record bị `Nak`, không mất); cân nhắc luật `WORK_DRAIN` so với `PUB_ACK_TIMEOUT`.
- Resync dừng ở tin cũ đầu tiên (giả định `CreatedAt` tăng đơn điệu theo seq; migrator hoặc lệch đồng hồ có thể làm dừng sớm).
- Change stream cấp database cần quyền `changeStream` trên cả database (role prod).

Từ M2b.2, chi tiết ở [plan, mục Kết quả thực thi](plans/2026-10-05-m2b2-edit-delete.md#kết-quả-thực-thi):

- Resync: `ErrEditPageFull` khi một thời điểm có ≥ 1000 fact sửa của một room (chỉ báo lỗi, chưa phân trang theo `_id`).
- `msg_changed` làm `At` + `Find` cho từng record, chưa gom theo room (đủ ở 100–300 lệnh đổi/s; cần gom khi xả backlog lớn).
- `ApplyEdit` trả nil cả khi tin không còn, nên nhánh `ErrMessageNotFound` của `edit_projection` không bao giờ chạy; fact mồ côi chỉ bị `msg_changed` drop có đếm sau `RECONCILE_DELAY`.
- Retry chỉ được nhận khi version hiện tại = `base+1`; tác giả sửa tiếp giữa lúc mất ack và lúc retry thì retry nhận `FAILED_PRECONDITION` (phạm vi D63).
- Giữa insert fact xoá và projection (core chết, worker chưa sửa), `GetEditHistory` còn trả text cũ; cửa sổ có hạn (`edit_projection` delay 0).
- Record work 33 byte còn tồn trong work stream lúc deploy bản 37 byte bị Term như bản ghi hỏng (`BadRecordsError`, đếm vào metric failures); chưa có dữ liệu prod, khi có thì xả work stream trước khi nâng cấp hoặc `/app resync` khoảng đó.
- `MESSAGE_LOCKED_KINDS` không có trong `Config.LogValue`.

Từ M2b.3, chi tiết ở [plan, mục Kết quả thực thi](plans/2026-10-06-m2b3-reactions-pins.md#kết-quả-thực-thi):

- Target nóng: mỗi touch đếm lại O(số reaction của tin) và các CAS `rx.v` tranh nhau; cửa sổ gom W + bucket `hash(u) % K` để milestone Channel (D90).
- Rolling deploy: core cũ `Term` record 38+ byte và record kind 5 (`PinInserted`); nâng mọi core trước khi có record mới (D91). Core M2b.3 `Nak` kind lạ không giới hạn số lần nên `ChatimWorkFailing` kêu trong lúc còn core cũ; một fetch có thể đếm cùng record nhiều lần khi `WORK_FETCH_WAIT` > `WORK_RETRY_DELAY`.
- `ReactMessage` không có cid: retry trễ có thể đưa reaction về trạng thái cũ (D89).
- Bốn event mới không có ack mark: event reaction trung gian có thể mất, chỉ trạng thái cuối được bảo đảm (D93).
- Resync: `ErrReactionPageFull`/`ErrPinPageFull` khi một thời điểm đầy một trang 1000 (như `ErrEditPageFull`); chọn room theo `ab` nên room chỉ có reaction/ghim trong khoảng mất cần `-room` (D91); `{r, ts}` của `reactions` không có prefix `_id` nên `Between` sẽ scatter-gather khi đã shard (D88).
- `effects`: lỗi `publish.Message` đếm drop một lần mỗi nhóm chứ không mỗi record; ba cache loại room mới theo effect (bộ nhớ); `reaction_counter` drop có đếm cả lỗi vĩnh viễn `ErrInvalidArgument`.
- Lỗi của `Nak`/`Term` trong work queue bị bỏ qua (record giao lại sau `AckWait`).

## Mức sẵn sàng

Theo [git-workflow.md](git-workflow.md#mức-sẵn-sàng): `dev-done` → `prod-like validated` → `go-live`. M0–M2a.3: `dev-done`, đã merge vào `main` (M2a.2 + M2a.3 cùng PR #11). Go-live còn cần: oplog `minRetentionHours` ≥ 24h, alert RC5 và lag, đo bộ nhớ NATS, đo lại trên prod-like. Chưa milestone nào `prod-like validated`; chưa có tag release.
