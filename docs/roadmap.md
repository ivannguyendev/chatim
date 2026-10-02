# Roadmap — chatim

> Cập nhật: 2026-10-01. Thiết kế: [designs/260930-chat-core-gateway-design.md](designs/260930-chat-core-gateway-design.md) · Plan M0–M1: [plans/2026-09-30-phase1-foundation-and-poc.md](plans/2026-09-30-phase1-foundation-and-poc.md) · Kết quả PoC: [poc/README.md](poc/README.md), [poc/260930-mongodb-vs-postgresql.md](poc/260930-mongodb-vs-postgresql.md)

| Phase | Milestone | Nội dung | Trạng thái |
|---|---|---|---|
| 1 | M0 — Nền tảng | Go module, Makefile chạy Go qua Docker, hạ tầng dev (Mongo rs0, Redis, NATS, Postgres), `pkg/keys`, `pkg/ids`, `pkg/slotmap`, Dockerfile runtime | ✅ Xong |
| 1 | M1 — PoC R1–R5 | Slot ownership trên Redis (code production), mongobench/natsbench/wsbench/postgresbench, kết quả dev, so sánh MongoDB/PostgreSQL | ✅ Xong trên dev · ⏳ prod-like chờ hạ tầng |
| 1 | M2a — Core: Send + History | CreateRoom/SendMessage/GetHistory qua gRPC, actor + flusher, chống trùng cid, JetStream + watermark, publish bù khi core chết, core ×2 trong compose, corebench | ✅ Xong trên dev |
| 1 | M2b — Core: thay đổi tin | Sửa (+ lịch sử sửa), xoá, reaction, ghim, read receipt, cấp `pts` an toàn cho mọi event, paging theo `pts` (không còn giả định `pts == seq`) | Chưa |
| 1 | M2c — Core: thread & tiện ích | Thread, mention, reply/forward, bookmark, đánh dấu chưa đọc | Chưa |
| 1 | M3 — Core: đường đọc | ListMyRoomIDs, ListMyRooms (unread), Sync, GetReactions/Pins/Bookmarks, cache RAM trang mới nhất + singleflight đọc lịch sử, test sẵn sàng sharding trên cluster 2 shard | Chưa |
| 1 | M4 — Gateway | WebSocket (gws), JWT/JWKS, frame protobuf, subscribe theo room, hàng đợi gửi có giới hạn, typing/presence, định tuyến theo slot, gateway đánh dấu core địa chỉ đang lỗi, hằng số header tenant/user chuyển vào `pkg`, cân nhắc client gRPC tự re-resolve DNS | Chưa |
| 1 | M5 — Hardening | Load test 100K kết nối, chaos test (kể cả R5 thật), OTel/Prometheus/Grafana, CI, ghim digest image, chạy container theo uid trên Linux, bật Redis AUTH | Chưa |
| 1 | PoC prod-like (song song) | Mongo rs 3 member NVMe vs PostgreSQL, NATS 3 node, 2 host Linux, dữ liệu thật ≥1M tin → chốt database | Chờ hạ tầng |
| 2 | App `auth` | Cấp JWT/JWKS theo tenant | Sau Phase 1 |
| 2 | App `api` | Public REST/BFF cho các sản phẩm | Sau Phase 1 |
| 2 | App `events` | gRPC stream cho app ngoài, cô lập tenant | Sau Phase 1 |
| 2 | App `push` | Thông báo đẩy cho user offline | Sau Phase 1 |
| 2 | App `migrator` | Dual-write / import từ MongoDB cũ | Sau Phase 1 |

## M2a — tóm tắt

Xong trên máy dev: `CreateRoom`/`SendMessage`/`GetHistory` qua gRPC, actor theo room + flusher gộp batch, chống trùng cid 3 tầng (LRU → Redis pending → Redis committed), publish JetStream với watermark liên tục và sweeper khôi phục khi core chết hoặc NATS gián đoạn, slot hook theo batch để actor nghỉ đúng lúc đổi chủ, core ×2 chạy trong compose và `corebench` đo tải mở-vòng tới cụm. Trên dev, `corebench` giữ đủ tải tới 5K tin/s (`shed_by_client=0`, `failed=0`) nhưng ack p99 ở mức ms (không mức nào đạt A1 ≤30ms, kể cả 500/s); 10K/s ban đầu phải bỏ bớt 5.7–24.5% lượt gửi. Sau khi bỏ Redis `SCAN` toàn keyspace (ZSET `chatim:cores`, D25), đo lại: 10K/s đạt 9452/s, bỏ 5.5%, `failed=0`, p99 818ms; 5K/s p99 292ms; phần còn lại do VM dev bão hoà CPU. Quyết định go/no-go chờ chạy prod-like. Chi tiết: [poc/README.md#c1-corebench-2-core-qua-grpc-dev-2026-10-02](poc/README.md#c1-corebench-2-core-qua-grpc-dev-2026-10-02).

## Thứ tự phụ thuộc

- M2a → M2b → M2c → M3 → M4 → M5.
- PoC prod-like chạy song song và phải xong trước M5, vì đó là lúc chốt database. Tầng lưu trữ của core đặt sau interface, nên nếu PoC chọn PostgreSQL thì chỉ thêm adapter.
- Phase 2 bắt đầu khi M4 xong, vì các app vệ tinh cần gateway và event stream ổn định.
