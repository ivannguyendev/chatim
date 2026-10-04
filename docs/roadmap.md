# Roadmap — chatim

> Cập nhật: 2026-10-04. Thiết kế: [designs/260930-chat-core-gateway-design.md](designs/260930-chat-core-gateway-design.md) · Plan M0–M1: [plans/2026-09-30-phase1-foundation-and-poc.md](plans/2026-09-30-phase1-foundation-and-poc.md) · Plan M2a.1: [plans/2026-10-03-m2a1-event-identity.md](plans/2026-10-03-m2a1-event-identity.md) · Plan M2a.2: [plans/2026-10-04-m2a2-event-reconcile.md](plans/2026-10-04-m2a2-event-reconcile.md) · Kết quả PoC: [poc/README.md](poc/README.md), [poc/260930-mongodb-vs-postgresql.md](poc/260930-mongodb-vs-postgresql.md)

| Phase | Milestone | Nội dung | Trạng thái |
|---|---|---|---|
| 1 | M0 — Nền tảng | Go module, Makefile chạy Go qua Docker, hạ tầng dev (Mongo rs0, Redis, NATS, Postgres), `pkg/keys`, `pkg/ids`, `pkg/slotmap`, Dockerfile runtime | ✅ Xong |
| 1 | M1 — PoC R1–R5 | Slot ownership trên Redis (code production), mongobench/natsbench/wsbench/postgresbench, kết quả dev, so sánh MongoDB/PostgreSQL | ✅ Xong trên dev · ⏳ prod-like chờ hạ tầng |
| 1 | M2a — Core: Send + History | CreateRoom/SendMessage/GetHistory qua gRPC, actor + flusher, chống trùng cid, JetStream + watermark, publish bù khi core chết (watermark và publish bù thay bởi M2a.1), core ×2 trong compose, corebench | ✅ Xong trên dev |
| 1 | M2a.1 — Sửa hướng đánh số & publish | Bỏ `pts` toàn room; id event tự nhiên `{room}-{thread}-{seq}`; event best-effort; publisher chỉ còn hàng đợi, dùng cơ chế async của nats.go (D50 viết lại); bỏ watermark, active mark, sweeper và vòng khôi phục của actor (D47–D51) | ✅ Xong trên dev |
| 1 | M2a.2 — Reconcile event | Component trong core, bật mặc định, chạy trên core giữ slot 0: đọc change stream của `messages` (nguồn sự thật) qua port `ChangeFeed` có contract chung để đổi DB được (Postgres dùng logical replication); publisher đánh dấu tin đã ack lên Redis dedupe bằng bitmap `chatim:evtack:*` (TTL 1h, ngoài đường ack) để reconciler chỉ publish bù tin chưa có dấu sau D = 30s, qua JetStream client riêng; dựng lại event từ doc với đúng id; lưu vị trí ở `reconciler_state` sau khi có ack; `EVT_STREAM_DUPLICATES` 5m. Chưa bù tin ghi trước lần đầu reconciler mở feed (DB mới hoặc sau khi mất lịch sử) (D47, D51, D52) | ✅ Xong trên dev |
| 1 | M2a.3 — Perf đường ghi | Gom lệnh chống trùng cid trên Redis (Reserve/Commit) giữa nhiều room trong mỗi core, gửi kiểu động (rảnh gửi ngay, đang bay thì gom); trả ack ngay sau insert, Commit gửi sau; thử `GOGC`; đo lại cùng quy trình corebench + pprof (decision log `.claude/plans/m2a3-write-path-perf_design.md`) | Chưa (sau M2a.2) |
| 1 | M2b — Core: thay đổi tin | Sửa (+ lịch sử sửa), xoá (cho mọi người / phía tôi), reaction, ghim, read receipt; id event theo version của doc (D53–D57 trong decision log M2b) | Chưa (bắt đầu sau M2a.3) |
| 1 | M2c — Core: thread & tiện ích | Thread, mention, reply/forward, bookmark, đánh dấu chưa đọc | Chưa |
| 1 | M3 — Core: đường đọc | ListMyRoomIDs, ListMyRooms (unread chính xác, chặn ở 99+, chỉ đếm tin có cờ đếm), Sync thiết kế lại (không phát lại, reconnect lấy mới nhất), đồng bộ trạng thái giữa các thiết bị, GetReactions/Pins/Bookmarks, cache RAM trang mới nhất + singleflight đọc lịch sử, test sẵn sàng sharding trên cluster 2 shard | Chưa |
| 1 | M4 — Gateway | WebSocket (gws), JWT/JWKS, frame protobuf, subscribe theo room, hàng đợi gửi có giới hạn, typing/presence, định tuyến theo slot, gateway đánh dấu core địa chỉ đang lỗi, hằng số header tenant/user chuyển vào `pkg`, cân nhắc client gRPC tự re-resolve DNS, gateway không chuyển `kind`/cờ đếm unread từ client | Chưa |
| 1 | M5 — Hardening | Load test 100K kết nối, chaos test (kể cả R5 thật), OTel/Prometheus/Grafana, CI đầy đủ (CI tối thiểu đã có từ 2026-10-03: fmt-check/vet/lint/test trên PR), ghim digest image, chạy container theo uid trên Linux, Sentinel (`FailoverClient`) cho cả hai Redis (state và dedupe), dev cùng mô hình | Chưa |
| 1 | PoC prod-like (song song) | Mongo rs 3 member NVMe vs PostgreSQL, NATS 3 node, 2 host Linux, dữ liệu thật ≥1M tin → chốt database | Chờ hạ tầng |
| 2 | App `auth` | Cấp JWT/JWKS theo tenant | Sau Phase 1 |
| 2 | App `api` | Public REST/BFF cho các sản phẩm | Sau Phase 1 |
| 2 | App `events` | gRPC stream cho app ngoài, cô lập tenant | Sau Phase 1 |
| 2 | App `push` | Thông báo đẩy cho user offline | Sau Phase 1 |
| 2 | App `migrator` | Dual-write / import từ MongoDB cũ | Sau Phase 1 |
| 2 | Policy quyền | Chính sách quyền theo tính năng/app; trước đó core chỉ giữ bất biến tenant + membership và ghi actor của mọi thay đổi | Sau Phase 1 |

## Mức sẵn sàng

Theo [git-workflow.md](git-workflow.md#mức-sẵn-sàng): `dev-done` → `prod-like validated` → `go-live`. M0, M1, M2a: `dev-done` (đã merge vào `main` qua PR #2, #5). M2a.1: `dev-done` (PR từ `fix/m2a1-event-identity`). M2a.2: `dev-done` (PR từ `feat/m2a2-event-reconcile`); go-live còn cần oplog `minRetentionHours` ≥ 24h, alert theo log mất lịch sử feed và log lag, đo bộ nhớ NATS prod-like. Chưa milestone nào `prod-like validated`; chưa có tag release.

## M2a — tóm tắt

Xong trên máy dev: `CreateRoom`/`SendMessage`/`GetHistory` qua gRPC, actor theo room + flusher gộp batch, chống trùng cid 3 tầng (LRU → Redis pending → Redis committed), publish JetStream với watermark liên tục và sweeper khôi phục khi core chết hoặc NATS gián đoạn (watermark và sweeper thay bởi M2a.1, D49), slot hook theo batch để actor nghỉ đúng lúc đổi chủ, core ×2 chạy trong compose và `corebench` đo tải mở-vòng tới cụm. Trên dev, `corebench` giữ đủ tải tới 5K tin/s (`shed_by_client=0`, `failed=0`) nhưng ack p99 ở mức ms (không mức nào đạt A1 ≤30ms, kể cả 500/s); 10K/s ban đầu phải bỏ bớt 5.7–24.5% lượt gửi. Sau khi bỏ Redis `SCAN` toàn keyspace (ZSET `chatim:cores`, D25), đo lại: 10K/s đạt 9452/s, bỏ 5.5%, `failed=0`, p99 818ms; 5K/s p99 292ms; phần còn lại do VM dev bão hoà CPU. Quyết định go/no-go chờ chạy prod-like. Chi tiết: [poc/README.md#c1-corebench-2-core-qua-grpc-dev-2026-10-02](poc/README.md#c1-corebench-2-core-qua-grpc-dev-2026-10-02).

## M2a.1 — lý do

- Thiết kế cũ cần `pts` liên tục cho mọi event của room nhưng lưu ở hai nơi (`messages.p`, `room_events._id`) không có khoá unique chung; hai core có thể cấp trùng pts và JetStream bỏ một event qua `Nats-Msg-Id` mà không dấu vết.
- Các bản vá giữ pts liên tục (bộ đếm +1 RTT, thuê khối, id kiểu Snowflake, tách hai dãy, log sự kiện) đều hỏng khi review; rà lại yêu cầu thì không ai cần pts liên tục.
- Hướng mới: event best-effort với id tự nhiên, publisher chỉ còn hàng đợi theo shard và giao phần publish đang bay cho nats.go, client reconnect lấy bản mới nhất, reconcile event (M2a.2, D52) bù phần mất. Quyết định: D47–D52 trong [thiết kế §14](designs/260930-chat-core-gateway-design.md#14-decision-log).

## Thứ tự phụ thuộc

- M2a → M2a.1 → M2a.2 → M2a.3 → M2b → M2c → M3 → M4 → M5.
- PoC prod-like chạy song song và phải xong trước M5, vì đó là lúc chốt database. Tầng lưu trữ của core đặt sau interface, nên nếu PoC chọn PostgreSQL thì chỉ thêm adapter.
- Phase 2 bắt đầu khi M4 xong, vì các app vệ tinh cần gateway và event stream ổn định.
