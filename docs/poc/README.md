# PoC — chatim

> Thiết kế: [../designs/261005-chatim-architecture.md](../designs/261005-chatim-architecture.md) · Chi tiết lần đo dev, lệnh và cấu hình: [archive/poc/README.md](../archive/poc/README.md), [so sánh MongoDB/PostgreSQL](../archive/poc/260930-mongodb-vs-postgresql.md).
> **Số dev chỉ để kiểm công cụ.** Máy dev là MacBook Intel 2018 + OrbStack (10 vCPU, 11.75GiB), DB, broker và công cụ đo chung một VM, Mongo 1 node. Go/no-go cần prod-like.

## Kết quả dev (2026-09-30 → 2026-10-05)

| # | Câu hỏi | Tiêu chí | Dev | Kết luận dev |
|---|---|---|---|---|
| R1a | Dung lượng/tin với dữ liệu thật | Dự phóng 20 tỷ tin vừa một rs | Chỉ có văn bản giả (98–130B/tin on-disk) | Chưa kết luận, cần dữ liệu thật |
| R1b | Trang cũ nhất/ngẫu nhiên/mới nhất khi dữ liệu > cache | p99 ≤ 20ms, `CLUSTERED_IXSCAN` | 10M tin, cache 1GB: 8.86 / 8.44 / 7.57ms | Đạt (sau khi sửa công cụ) |
| R2 | `insertMany` w:majority ở 10K tin/s | Chờ ack p99 ≤ 30ms | p99 213ms (1 node) | Không đạt trên dev |
| R3a | RePublish đổi subject | PASS | PASS | Đạt |
| R3b | 1M interest sub + 5K event/s | publish→gateway p99 ≤ 10ms | p99 1.2ms, 0 drop | Đạt |
| R4 | gws 50K connection | ≤ 40KB/conn; lần ghi cuối ≤ 100ms | ≤ 10.3KB/conn; 155–245ms | RAM đạt, độ trễ không đạt trên dev |
| R5 | Soft ownership khi Redis mất dữ liệu | `-race -count=20` pass | pass ở mức unit | Còn chaos test |
| C1 | corebench 2 core qua gRPC | Ack p99 ≤ 30ms (A1) | 5K/s đủ tải, p99 292ms; 10K/s đạt 9452/s, bỏ 5.5%, p99 818ms | Không đạt trên dev (VM bão hoà CPU) |
| C1-M2a.3 | Perf đường ghi ở 5K/s | Trước/sau | Lệnh Redis/tin 6.35 → 4.74; CPU redis-dedupe 89% → 69% | Chỉ so trước/sau |
| W1 | Xả backlog work stream (reader slot 0 + worker 2 core), M2b.1 | ≥ 3× ingest đỉnh (D66; đo thật ở P3) | 36000 record (1000 `RoomInserted` + 35000 `MessageInserted`) trong ~4s ≈ ≥ 9000 record/s (cận dưới, mẫu cách nhau 1–2s) sau 35s ghi 1000 tin/s với reader tắt; lag worker tối đa 52.4s (backlog tích luỹ); `work_failures_total` 0; republish `room_created` 1000 (không mark, stream bỏ trùng), `msg_created` 0 (fast path đã mark); CPU_Speed_Limit 100 | Chỉ kiểm công cụ |
| DB | MongoDB vs PostgreSQL | — | Mongo thắng đọc ngẫu nhiên khi dữ liệu > RAM (8.4 vs 35ms) và dung lượng (130 vs 393B/tin); Postgres thắng độ trễ ghi bền | Giữ MongoDB (D9), chờ prod-like |

## Đo prod-like (chưa chạy)

**Trước khi chạy: hỏi owner số thật** thay cho giả định ở thiết kế §2.3 (room/user, tỉ lệ sửa, room active, số member doc).

Hạ tầng: Mongo rs 3 member NVMe, NATS 3 node, 2 host Linux, dữ liệu thật ≥ 1M tin (xuất bằng `mongoexport`; `real-texts*.txt` là dữ liệu khách hàng, không commit, xoá sau khi đo).

| # | Đo gì | Tiêu chí / dùng cho |
|---|---|---|
| P1 | R1a, R1b, R2, R4, C1 lặp lại trên prod-like | Go/no-go A1; chốt database (D9) |
| P2 | Chaos R5: kill Redis master khi đang tải | Mọi cid đã ack có đúng một bản |
| P3 | Thông lượng một cursor change stream + hop work stream, replay backlog | Xả backlog ≥ 3× ingest đỉnh (D66) |
| P4 | JetStream publish ack R3 và độ trễ hop work stream | Delay effect, retention work stream |
| P5 | Kích thước oplog theo byte ở đỉnh, cửa sổ oplog (giờ) | `minRetentionHours`, alert RC5 |
| P6 | Key/s của count trên index; write conflict trên hot doc | W và bucket K của counter (D67) |
| P7 | Tải đọc lúc reconnect storm, cache lạnh/ấm, có/không jitter | Room-tail cache, sync token (D72, D74) |
| P8 | `clusterTime` của change event dùng làm `afterClusterTime` | Đúng đắn của touch counter |
| P9 | Tỉ lệ tin không đếm unread sau `rs` | Giới hạn S của R17 (D73) |
| P10 | Bộ nhớ NATS cho map chống trùng 5m (~3M id ở 10K tin/s) | Cỡ NATS |
