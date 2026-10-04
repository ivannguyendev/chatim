# Roadmap — chatim

> Cập nhật: 2026-10-05 (viết lại sau 2 vòng phản biện cơ chế hệ thống). Thiết kế: [designs/261005-chatim-architecture.md](designs/261005-chatim-architecture.md) · Tổng hợp phản biện: [research/261004-system-mechanisms-synthesis.md](research/261004-system-mechanisms-synthesis.md) · PoC: [poc/README.md](poc/README.md) · Bản cũ (roadmap trước, plan M0–M2a.3, thiết kế Phase 1): [archive/](archive/README.md)

## Quy tắc viết plan

- Mỗi milestone có plan riêng trong `docs/plans/`, viết **trước khi code**, theo skill `writing-plans`: từng bước TDD, file, test, lệnh chạy, "Expected". Người thực thi gặp chỗ plan không nói tới thì dừng và báo.
- Plan mở đầu bằng bảng **tính năng → lớp dữ liệu** ([thiết kế §4](designs/261005-chatim-architecture.md#4-khung-lớp-dữ-liệu-bắt-buộc-cho-mọi-plan)). Mỗi tính năng khai báo: fact (lệnh atomic nào, doc nào), cách idempotent (cid hoặc `base_ver`), effect + chính sách, quyền (permission hook), view theo người đọc, dòng trong danh mục event, ngân sách khuếch đại ở nhóm 5K và channel 200K, guarantee + detector. Tính năng không khớp lớp nào thì dừng, thiết kế lớp đó trước.
- Bộ test chung mỗi PR: fact sinh đúng effect; no-op không sinh effect; lỗi không sinh effect; parity fast path / reconciler; chạy effect hai lần cho cùng kết quả.

## Milestone

| Phase | Milestone | Nội dung | Trạng thái |
|---|---|---|---|
| 1 | M0–M2a.3 — Nền tảng, PoC dev, gửi tin + lịch sử | Monorepo, Go qua Docker, hạ tầng dev; slot ownership mềm; PoC R1–R5 trên dev; `CreateRoom`/`SendMessage`/`GetHistory` qua gRPC, actor + flusher, chống trùng cid 3 tầng gom giữa các room, ack trước Commit; event best-effort với id tự nhiên; reconciler từ change stream của `messages` + ack mark bitmap; 2 core trong compose; corebench và bench-cell | ✅ `dev-done`, đã merge vào `main` (PR #2, #5, #8–#11) |
| 1 | **M2b.0 — Nền cơ chế** | Sửa lỗi mark của publisher (mark theo event id + loại, D65); `RECONCILE_DELAY` theo effect, `msg_created` ~5s; actor tự rút khi va doc core khác + retry backoff/jitter (D77); reader pipeline + permission hook, áp vào `GetHistory` (thiết kế §9.2); detector/alert cho guarantee đang có RC1–RC5, CD1–CD3 (D76); contract test: mọi write method của adapter là insert unique, CAS, upsert hoặc bump version | ⏭ Tiếp theo — [plan](plans/2026-10-05-m2b0-mechanism-foundations.md) sẵn sàng |
| 1 | M2b.1 — Effect engine | Reader trên slot 0 → JetStream work stream theo slot, id tự nhiên, worker ở mọi core, vị trí xác nhận theo work stream (D66); registry effect + chính sách (D65); chuyển reconcile `msg_created` sang engine; `room_created` là effect mới đầu tiên; room activity (`last_seq`, `last_msg_at`, `last_change_at`, `act_bucket`, ghi gom, D69); công cụ resync thủ công + diễn tập; đo xả backlog | Chưa |
| 1 | M2b.2 — Sửa + xoá | Fact `message_edits` + projection `messages` (D62); `base_ver`, `prev` ở v1 (D63); ack sau projection (D64); delay xoá 2–3s; hợp đồng xoá (D75); `GetEditHistory`; quyền tác giả/owner qua permission hook; ẩn phía tôi (`hidden` thưa) và clear history (`cleared_before_seq`) qua reader pipeline; index `{room, ts}` (D70) | Chưa |
| 1 | M2b.3 — Reaction + ghim | Tập reaction `{k, emoji, u}` + `n` (D68); counter recount CAS-ver (D67); `pin_actions` + `base_pv` + kiểm ≤50 pin trước commit; event cho SysMsg (cid `sys:{event_id}`) | Chưa |
| 1 | M2b.4 — Member + vị trí đọc | Fact member (thêm/bớt/rời/đổi role); `user_rooms {u│r}` (D72); `member_count`; event member trên subject user; vị trí đọc `$max` + event coalesce; đánh dấu chưa đọc (version + LWW) | Chưa |
| 1 | M2c — Thread & tiện ích | Thread (`thread_count` là counter), mention, reply/forward (cid), bookmark | Chưa |
| 1 | M3 — Đường đọc | **Trước khi viết plan: hỏi owner số thật** (thiết kế §2.3). `ListMyRooms` qua `user_rooms`; "có tin mới" đọc từ `messages`; room-tail cache; unread theo R17 mới (exact trong S, không thì `approx`, D73); sync token `room → last_seq` + full sync (D74); SDK reconnect jitter; API caller nội bộ cho SysMsg (`kind` + cờ unread); đồng bộ đa thiết bị qua subject user; `GetMessages`/`GetReactions`/`ListPins`/`ListBookmarks`; test sẵn sàng sharding trên cluster 2 shard | Chưa |
| 1 | M4 — Gateway | WebSocket (gws), JWT/JWKS, frame protobuf, fanout theo interest, hàng đợi gửi có giới hạn, định tuyến theo slot; typing/presence ephemeral không qua core; subject user cho dữ liệu riêng; `member_removed` → unsubscribe; chế độ event không text cho tenant xoá chặt; không chuyển `kind`/cờ unread từ client; hằng số header tenant/user vào `pkg` | Chưa |
| 1 | M5 — Hardening | Load test 100K kết nối, chaos test (kể cả R5 thật), OTel/Prometheus/Grafana, CI đầy đủ, ghim digest image, container theo uid, Sentinel (`FailoverClient`) cho cả hai Redis | Chưa |
| 1 | Channel | Room type channel (~200K subscriber, chỉ admin post): quyền post, fanout, counter lớn (bucket) | Chưa — chốt milestone sau M3 |
| 1 | PoC prod-like (song song) | **Trước khi chạy: hỏi owner số thật.** Mongo rs 3 member NVMe, NATS 3 node, 2 host Linux, dữ liệu thật ≥1M tin; P1–P10 ở [poc/README.md](poc/README.md) → chốt database, go/no-go A1 | Chờ hạ tầng |
| 2 | App `auth`, `api`, `events`, `push`, `migrator` | JWT/JWKS theo tenant; REST/BFF; gRPC stream cho app ngoài; push (badge xấp xỉ, D73); migrator dual-write/import (chế độ import im lặng để reconciler không phát hàng tỷ event) | Sau Phase 1 |
| 2 | Policy quyền | Chính sách quyền cắm vào permission hook (M2b.0) | Sau Phase 1 |

## Thứ tự phụ thuộc

- M2b.0 → M2b.1 → M2b.2 → M2b.3 → M2b.4 → M2c → M3 → M4 → M5.
- M2b.0 là điều kiện của mọi milestone sau: permission hook, reader pipeline và detector dùng chung.
- (owner 2026-10-05) M2b.0 → M2b.4 làm trên một nhánh chung `feat/m2b` (tạo từ `docs/system-mechanisms`), không PR từng phần; merge vào `main` một lần khi M2b.4 đạt Definition of Done.
- M2b.1 phải xong trước mọi tính năng có fact mới, vì effect của chúng chạy trên engine.
- PoC prod-like chạy song song, xong trước M5 (chốt database). Storage nằm sau port nên PostgreSQL chỉ cần thêm adapter.
- Phase 2 bắt đầu khi M4 xong.

## Mức sẵn sàng

Theo [git-workflow.md](git-workflow.md#mức-sẵn-sàng): `dev-done` → `prod-like validated` → `go-live`. M0–M2a.3: `dev-done`, đã merge vào `main` (M2a.2 + M2a.3 cùng PR #11). Go-live còn cần: oplog `minRetentionHours` ≥ 24h, alert RC5 và lag, đo bộ nhớ NATS, đo lại trên prod-like. Chưa milestone nào `prod-like validated`; chưa có tag release.
