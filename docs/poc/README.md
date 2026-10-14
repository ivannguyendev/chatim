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

### M3 — `ListMyRooms` trên replica set (2026-10-15)

Công cụ `tools/poc/roomlistbench`, rs0 1 node, core-1 + core-2, mặc định `ROOM_LIST_JOIN_MAX` 2000, trang 20 room. Số thô: `bin/bench/m3-roomlist-rs0/`.

- **Seed qua API core** (`-mode seed`, 4 lời gọi song song, mỗi lời gọi tối đa 2 phút, thử lại giữ `request_id`/cid): 60 user (20 cho mỗi cỡ 200, 2.000, 5.000), 4.960 room nền dùng chung (1 tin mỗi room) và 40 room riêng mỗi user (5 tin của người kia, chưa đọc, mới nhất) → trang đầu và trang sau đều là room riêng. 7.360 room, 151.360 member doc trong 5m36s, 16.960 tin trong 36s; chờ `rooms.last_seq` đủ ở mọi room (6s) rồi Mongo dưới 40 op/s trong 3s.
- **Lạnh** = user mới (room riêng của user chỉ nằm trong trang của nó nên cache info, đuôi, đường và ảnh chụp đều trượt; cache WiredTiger của Mongo vẫn nóng); không khởi động lại core. Mỗi user: trang đầu + trang sau lạnh, rồi 5 cặp nóng (trong TTL 30s).
- **Truy vấn/trang** = delta `opcounters` query + command của `serverStatus` quanh từng lời gọi (trừ lần đọc `serverStatus` của chính nó; `getmore` riêng); khung rỗng 20ms trước mỗi cỡ đo được 0 truy vấn. Lệnh bắt tay của kết nối Mongo mới (hiếm, thấy ở max) bị tính vào; `top` theo collection dùng để tách nguồn. Cache = delta `room_cache_hits/misses_total` của core-1.
- **CPU_Speed_Limit** = 100 trước và trong cả ba ô (29/29 mẫu, mỗi 3s); seed làm máy hạ xung xuống 76 nên mỗi ô chờ về 100 trước khi đo.

| Room | Đường | Trang | n | p50 | p99 | Truy vấn (trung vị) | Dự kiến §9 | Cache/lời gọi |
|---|---|---|---|---|---|---|---|---|
| 200 | join | đầu, lạnh | 20 | 30.6ms | 50.2ms | 44 | 24 (Task 9: ~44) | info 20 miss, đuôi 20 miss, đường miss |
| 200 | join | sau, lạnh | 20 | 12.5ms | 76.7ms | 44 | 23 | ảnh chụp hit, info/đuôi 20 miss |
| 200 | join | đầu, nóng | 100 | 25.9ms | 34.3ms | 2 | 2–3 | info/đuôi 20 hit, đường hit |
| 200 | join | sau, nóng | 100 | 11.2ms | 68.5ms | 3 | 2 | ảnh chụp hit, info/đuôi 20 hit |
| 2.000 | join | đầu, lạnh | 20 | 209ms | 227ms | 44 | 24 (~44) | như 200 |
| 2.000 | join | sau, lạnh | 20 | 56.2ms | 71.2ms | 44 | 23 | |
| 2.000 | join | đầu, nóng | 100 | 202ms | 268ms | 2 | 2–3 | |
| 2.000 | join | sau, nóng | 100 | 53.1ms | 66.2ms | 3 | 2 | |
| 5.000 | từng bước | đầu, lạnh | 20 | 134ms | 158ms | 50 (+6 getmore) | 30 | như 200 |
| 5.000 | từng bước | sau, lạnh | 20 | 60.2ms | 68.4ms | 44 | 23 | |
| 5.000 | từng bước | đầu, nóng | 100 | 134ms | 153ms | 8 (+6 getmore) | 9 | |
| 5.000 | từng bước | sau, nóng | 100 | 57.9ms | 72.6ms | 3 | 2 | |

Kết luận dev:
- Cache nóng đạt: trang đầu 2 truy vấn (join: aggregate + `hidden`) và 8 (từng bước: `members` phủ + 5 lô `rooms` + `members` của trang + `hidden`; §9 tính thêm lần đếm, nay đã cache); trang sau 3 (`members` + `rooms` hoạt động + `hidden`; §9 ghi 2 vì quên lần đọc hoạt động của các room trong ảnh chụp).
- Cache lạnh đúng như lệch Task 9: mỗi lần nạp đuôi = `TailWindow` + `TailPreviews` riêng → 40 truy vấn `messages` cho 20 room, nên trang lạnh 44–50 thay vì 23–30. Gộp `TailPreviews` cả trang còn ~25–31.
- Độ trễ do đọc thứ tự: join 2.000 room tốn ~200ms ở cả lạnh lẫn nóng (`top` thấy 2.000 lượt đọc `rooms` của `$lookup` mỗi lời gọi), chậm hơn đường từng bước ở 5.000 room (~134ms). Trên dev này điểm chuyển 2.000 quá cao; chưa đo đường từng bước ở 2.000 room để chọn `ROOM_LIST_JOIN_MAX` mới (việc của owner).

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
