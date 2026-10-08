# So sánh MongoDB và PostgreSQL cho kho tin nhắn — chatim PoC

> Ngày: 2026-09-30 · Máy dev (MacBook Intel 2018 + OrbStack, 10 CPU / 11.75GiB cho VM). Số liệu dùng để **so sánh tương đối** giữa hai database trên cùng một máy, không phải số production.
> Công cụ: `tools/poc/mongobench`, `tools/poc/postgresbench`, dùng chung bộ sinh dữ liệu (`tools/poc/internal/seedload`, `writeload`).

## Cách đo

| | MongoDB 8.2.12 | PostgreSQL 18.6 |
|---|---|---|
| Topology | replica set 1 node `rs0` | 1 instance |
| Bảng/collection | clustered collection, `_id` 24B `room│thread│seq`, nén `zstd` | bảng `messages`, `PRIMARY KEY (room_id, thread_root, seq)` |
| Bộ nhớ đệm | WiredTiger cache 3GB (lạnh: 1GB) | `shared_buffers` 3GB (lạnh: 1GB) |
| Cấu hình khác | `--oplogSize 4096` | `wal_compression=lz4`, `max_wal_size=8GB`, `checkpoint_timeout=15min`, `checkpoint_completion_target=0.9`, `wal_buffers=64MB`, `random_page_cost=1.1`, `effective_io_concurrency=200`, `default_toast_compression=lz4`, `jit=off`, `io_method=worker` |
| Ghi bền | `w:majority` (1 node ⇒ chờ journal) | `synchronous_commit=on` (chờ WAL flush) |
| Ghi không bền | `w:1` | `synchronous_commit=off` |

- Dữ liệu: 10.000 room × 1.000 tin = 10M tin, văn bản giả; nạp **xen kẽ** giữa các room (`-order interleaved`), giống thứ tự tin đến thật. Nạp theo từng room sẽ xếp dữ liệu liền nhau một cách giả tạo và có lợi cho cả hai.
- Đọc: trang 50 tin của timeline chính, 16 luồng, 30s mỗi chế độ; "lạnh" = cache 1GB + restart container. Page cache của OS trong VM không xoá được ở cả hai bên.
- Ghi: 10.000 tin/s, 60s, 5.000 room mới, 6 flusher, gom batch 2ms / 256 tin; Mongo `insertMany(ordered:false)`, Postgres `INSERT … SELECT unnest(…) ON CONFLICT DO NOTHING`.
- Chỉ chạy **một** database trong lúc đo.
- Sửa công cụ trước khi đo: `mongobench read` từng dùng `Cursor.All` (driver decode từng document bằng reflection, tốn khoảng 36% CPU phía client, throughput chỉ khoảng 2.6K trang/s). Đổi sang duyệt cursor không decode (giống `RawValues` bên Postgres) thì lên khoảng 9.6K trang/s. **Số R1b cũ (p99 23.36ms) bị đội bởi lỗi này.**

## Kết quả

### Nạp dữ liệu và dung lượng

| | MongoDB | PostgreSQL |
|---|---|---|
| Nạp 10M tin | 8m15s | 1m10s |
| On-disk | 1237.2MB (130B/tin) | 3752.4MB (393B/dòng: heap 326B + index 68B) |
| Dự phóng 5 tỷ / 20 tỷ tin | 0.65TB / 2.59TB | 1.97TB / 7.87TB |

PostgreSQL không nén dòng nhỏ (TOAST chỉ nén giá trị > ~2KB), nên tốn gấp 3 lần. Văn bản giả nén khác văn bản thật — cần đo lại với dữ liệu thật (R1a).

### Đọc (p99; throughput trong ngoặc)

| Chế độ | MongoDB nóng | PostgreSQL nóng | MongoDB lạnh | PostgreSQL lạnh |
|---|---|---|---|---|
| oldest | 6.85ms (9.6K/s) | 5.11ms (14.1K/s) | 8.86ms (7.6K/s) | 3.75ms (24.8K/s) |
| random | 7.75ms (7.7K/s) | 10.15ms (7.7K/s) | **8.44ms** (6.2K/s) | **35.02ms** (2.7K/s, max 2.41s) |
| latest | 6.69ms (9.5K/s) | 6.11ms (12.3K/s) | 7.57ms (7.4K/s) | 4.01ms (22.2K/s) |

- Trang **ngẫu nhiên khi dữ liệu lớn hơn cache** là nơi hai bên khác nhau rõ nhất: clustered collection giữ tin của một room liền nhau; heap của Postgres xếp theo thứ tự ghi nên một trang 50 tin nằm rải rác khắp file. Đây đúng là yêu cầu "lấy tin cũ bất kỳ cực nhanh".
- Trang `oldest`/`latest` của Postgres nhanh vì các vùng seq đầu và cuối được ghi gần nhau về thời gian và nằm trong page cache của OS.

### Ghi 10.000 tin/s (chờ ack)

| Lượt | MongoDB `w:majority` p50 / p95 / p99 | PostgreSQL `sync=on` p50 / p95 / p99 |
|---|---|---|
| 1 | 14.71ms / 859.57ms / 1078.74ms | 4.81ms / 15.13ms / 35.26ms |
| 2 | 7.41ms / 28.40ms / 274.02ms | 5.20ms / 40.64ms / 250.44ms |
| 3 | 8.03ms / 428.02ms / 1422.99ms | — |

| Không bền | MongoDB `w:1` | PostgreSQL `sync=off` |
|---|---|---|
| p50 / p99 | 4.23ms / 34.26ms | 2.96ms / 14.88ms |

- p99 của cả hai dao động mạnh giữa các lượt vì fsync/checkpoint trên đĩa ảo của OrbStack; p50 và p95 ổn định hơn và **PostgreSQL tốt hơn rõ**.
- Lý do cấu trúc: clustered collection là một B-tree chứa toàn bộ dữ liệu, nên chèn vào room nào cũng cần trang lá của room đó trong cache (đọc trước khi ghi) và làm bẩn nhiều trang; heap Postgres chỉ ghi nối vào cuối, phần B-tree cần giữ nóng chỉ là index (644.6MB).

## Kết luận tạm thời (dev)

| Tiêu chí | Bên có lợi | Mức chênh |
|---|---|---|
| Đọc trang bất kỳ khi dữ liệu > RAM (yêu cầu cốt lõi) | **MongoDB** | p99 8.44ms vs 35.02ms |
| Dung lượng (1 replica set, không sharding) | **MongoDB** | 3 lần nhỏ hơn |
| Sẵn sàng sharding chỉ bằng cấu hình | **MongoDB** | đã kiểm chứng; Postgres cần Citus hoặc chia ở tầng app |
| Độ trễ ghi bền (p50/p95) | **PostgreSQL** | p50 ~5ms vs 7–15ms; p95 15–41ms vs 28–860ms |
| Nạp dữ liệu hàng loạt (migrate từ hệ thống cũ) | **PostgreSQL** | nhanh 7 lần |

**Khuyến nghị:** giữ MongoDB (clustered collection) cho kho tin nhắn, vì nó thắng đúng các yêu cầu khó thay thế nhất: đọc trang cũ bất kỳ nhanh khi dữ liệu vượt RAM, dung lượng nhỏ 3 lần trên một replica set, và sharding chỉ bằng cấu hình. Rủi ro lớn nhất của MongoDB là **đuôi độ trễ ghi bền** — phải kiểm chứng trên prod-like trước M2:

1. Replica set 3 member trên đĩa NVMe thật, đo lại `w:majority` (`mongobench write`) nhiều lượt; thử cache WiredTiger lớn hơn working set của các room đang hoạt động.
2. Chạy song song `postgresbench` trên cùng phần cứng (cùng lệnh, cùng dữ liệu thật) để quyết định cuối cùng.
3. Nếu `w:majority` p99 trên prod-like vẫn > 30ms còn Postgres đạt: cân nhắc PostgreSQL với index phủ `INCLUDE (f, p, kind, body, ts)` để đọc theo thứ tự index (gần giống clustered), chấp nhận dung lượng khoảng gấp đôi — cần đo trước khi chọn.

## Chạy lại

```bash
make pg-down && make infra-up
make -s poc TOOL=mongobench ARGS="seed -rooms 10000 -per-room 1000 -reset"
make -s poc TOOL=mongobench ARGS="read -mode random -duration 30s"
make -s poc TOOL=mongobench ARGS="write -rate 10000 -duration 60s -rooms 5000 -flushers 6 -w majority"

docker stop chatim-mongodb && make pg-up
make -s poc TOOL=postgresbench ARGS="seed -rooms 10000 -per-room 1000 -reset"
make -s poc TOOL=postgresbench ARGS="read -mode random -duration 30s"
make -s poc TOOL=postgresbench ARGS="write -rate 10000 -duration 60s -rooms 5000 -flushers 6 -sync on"
```

Đo lạnh: đặt `MONGO_CACHE_GB=1` / `PG_SHARED_BUFFERS=1GB` trong `.env`, tạo lại container (`make infra-down && make infra-up` hoặc `make pg-up`), restart container, rồi đọc. Log gốc của lần đo này nằm ngoài repo (scratchpad của phiên chạy).
