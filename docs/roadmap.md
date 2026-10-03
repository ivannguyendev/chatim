# Roadmap — chatim

> Cập nhật: 2026-10-01. Thiết kế: [designs/260930-chat-core-gateway-design.md](designs/260930-chat-core-gateway-design.md) · Plan M0–M1: [plans/2026-09-30-phase1-foundation-and-poc.md](plans/2026-09-30-phase1-foundation-and-poc.md) · Kết quả PoC: [poc/README.md](poc/README.md), [poc/260930-mongodb-vs-postgresql.md](poc/260930-mongodb-vs-postgresql.md)

| Phase | Milestone | Nội dung | Trạng thái |
|---|---|---|---|
| 1 | M0 — Nền tảng | Go module, Makefile chạy Go qua Docker, hạ tầng dev (Mongo rs0, Redis, NATS, Postgres), `pkg/keys`, `pkg/ids`, `pkg/slotmap`, Dockerfile runtime | ✅ Xong |
| 1 | M1 — PoC R1–R5 | Slot ownership trên Redis (code production), mongobench/natsbench/wsbench/postgresbench, kết quả dev, so sánh MongoDB/PostgreSQL | ✅ Xong trên dev · ⏳ prod-like chờ hạ tầng |
| 1 | M2a — Core: Send + History | CreateRoom/SendMessage/GetHistory qua gRPC, actor + flusher, chống trùng cid, JetStream + watermark, publish bù khi core chết, core ×2 trong compose, corebench | ⏭ Tiếp theo |
| 1 | M2b — Core: thay đổi tin | Sửa (+ lịch sử sửa), xoá, reaction, ghim, read receipt, cấp `pts` an toàn cho mọi event | Chưa |
| 1 | M2c — Core: thread & tiện ích | Thread, mention, reply/forward, bookmark, đánh dấu chưa đọc | Chưa |
| 1 | M3 — Core: đường đọc | ListMyRoomIDs, ListMyRooms (unread), Sync, GetReactions/Pins/Bookmarks, cache RAM trang mới nhất, test sẵn sàng sharding trên cluster 2 shard | Chưa |
| 1 | M4 — Gateway | WebSocket (gws), JWT/JWKS, frame protobuf, subscribe theo room, hàng đợi gửi có giới hạn, typing/presence, định tuyến theo slot | Chưa |
| 1 | M5 — Hardening | Load test 100K kết nối, chaos test (kể cả R5 thật), OTel/Prometheus/Grafana, CI, ghim digest image, chạy container theo uid trên Linux | Chưa |
| 1 | PoC prod-like (song song) | Mongo rs 3 member NVMe vs PostgreSQL, NATS 3 node, 2 host Linux, dữ liệu thật ≥1M tin → chốt database | Chờ hạ tầng |
| 2 | App `auth` | Cấp JWT/JWKS theo tenant | Sau Phase 1 |
| 2 | App `api` | Public REST/BFF cho các sản phẩm | Sau Phase 1 |
| 2 | App `events` | gRPC stream cho app ngoài, cô lập tenant | Sau Phase 1 |
| 2 | App `push` | Thông báo đẩy cho user offline | Sau Phase 1 |
| 2 | App `migrator` | Dual-write / import từ MongoDB cũ | Sau Phase 1 |

## Thứ tự phụ thuộc

- M2a → M2b → M2c → M3 → M4 → M5.
- PoC prod-like chạy song song và phải xong trước M5, vì đó là lúc chốt database. Tầng lưu trữ của core đặt sau interface, nên nếu PoC chọn PostgreSQL thì chỉ thêm adapter.
- Phase 2 bắt đầu khi M4 xong, vì các app vệ tinh cần gateway và event stream ổn định.
