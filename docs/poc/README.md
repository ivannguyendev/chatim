# Kết quả PoC — chatim Phase 1

> Thiết kế: [../designs/260930-chat-core-gateway-design.md](../designs/260930-chat-core-gateway-design.md) (mục 13 — Rủi ro)
> Mỗi lần chạy ghi 1 dòng. Chạy trên máy dev chỉ để kiểm tra công cụ; kết luận R1–R4 phải dựa trên máy giống production.

## Môi trường

| Lần chạy | Ngày | Máy (CPU / RAM / disk) | MongoDB | NATS | Ghi chú |
|---|---|---|---|---|---|
| dev | 2026-09-30 | MacBook Pro 15,1 (2018), macOS 15.7.9 / Intel Core i7-8850H 2.60GHz, 6 core 12 thread / 16GB / SSD NVMe Apple AP0512M 500GB. VM Docker: OrbStack 2.2.3 (Docker Engine 29.4.0, kernel 7.0.14-orbstack), cấp 10 CPU, 11.75GiB RAM | 8.2.12, 1 node rs0, WiredTiger cache 3GB (1GB cho R1b), oplog 4096MB | 2.15.0, 1 node, JetStream file storage | OrbStack (không phải Docker Desktop). DB, broker và công cụ đo chạy chung 1 VM nên tranh CPU/RAM với nhau; chỉ kiểm tra công cụ, số liệu không đại diện production |
| prod-like | TBD — điền khi chạy | TBD — điền khi chạy | 8.2, rs 3 member | 2.15, 3 node | Chưa chạy |

## Tiêu chí và kết quả

| # | Câu hỏi | Tiêu chí đạt | Kết quả dev | Kết quả prod-like | Kết luận |
|---|---|---|---|---|---|
| R1a | Dung lượng/tin trên đĩa (dữ liệu thật) | Dự phóng 20 tỷ tin vừa 1 replica set (ghi con số TB) | **Chỉ văn bản giả (synthetic text only — không dùng để tính dung lượng).** `seed -rooms 10000 -per-room 1000 -reset` (không có `-text-file`): `seeded 10000000 messages in 1m54s`; `docs=10000000 logical=3137.4MB on-disk=932.1MB ratio=3.37x logical/doc=329B disk/doc=98B`; `projected on-disk for 5 billion messages: 0.49 TB`, `for 20 billion messages: 1.95 TB (before oplog, indexes, replicas)` | Chờ: cần xuất ≥1M tin thật từ Mongo cũ (Step 3) | Chưa kết luận |
| R1b | Trang cũ nhất / ngẫu nhiên / mới nhất khi dữ liệu > WiredTiger cache | p99 ≤ 20ms, plan `CLUSTERED_IXSCAN` | `MONGO_CACHE_GB=1`, tạo lại container rồi restart. Dữ liệu logical 3137.4MB > cache 1GB, nhưng on-disk chỉ 932.1MB (sau restart, `mongosh` `db.getSiblingDB("chatim_poc").messages.stats()` in `storageSize MB= 947.2`) nên vừa page cache của VM (11.75GiB, không xoá được ở lần này) → đọc có thể không chạm đĩa, điều kiện "lạnh" chưa thật sự đạt. **oldest:** `plan (oldest page): LIMIT > CLUSTERED_IXSCAN`; `mode=oldest pages=151763 (2529/s) empty=0 errors=0 latency: n=151763 p50=4.967976ms p95=16.186169ms p99=23.357931ms max=95.904614ms` → `empty=0`, `errors=0` nhưng p99 > 20ms, **không đạt trên dev**. **random, latest: chưa chạy được do giới hạn quyền của môi trường chạy tự động lúc đó (không phải lỗi mongobench)**; cần chạy lại — xem [Chạy lại R1b](#chạy-lại-r1b) bên dưới | Chờ: rs 3 member, dữ liệu thật, xoá page cache | Chưa kết luận |
| R2 | `insertMany` w:majority ở 10K tin/s | Thời gian chờ ack p99 ≤ 30ms, errors = 0 | 1 node rs0 (w:majority chỉ gồm 1 member). `target=10000/s achieved=9999/s docs=599987 batches=37687 avg-batch=15.9 errors=0`; `arrival->commit: n=599987 p50=8.122084ms p95=25.01663ms p99=213.521741ms max=549.40756ms`; `insertMany w:majority round trip: n=37687 p50=5.756332ms p95=8.901774ms p99=26.816186ms max=523.148044ms` → errors=0 nhưng p99 chờ ack > 30ms, **không đạt trên dev** | Chờ: replica set 3 member | Chưa kết luận |
| R3a | RePublish đổi subject | `republish transform: PASS` | `republish transform: PASS` → **đạt trên dev** | Chờ: NATS 3 node | Đạt trên dev; xác nhận lại trên cluster 3 node |
| R3b | 1M interest sub + 5K event/s | publish→gateway p99 ≤ 10ms; RAM server (ghi MB) | `-subs 1000000 -conns 4 -rate 5000 -duration 60s`: `subscribed 1000000 rooms on 4 connections in 5.101s; server mem=925MB subscriptions=1000066`; `target=5000/s published=294325 (4905/s) failed=0 received=294325 dropped=0; server mem=1715MB`; `jetstream publish ack: p50=456.473µs p95=799.798µs p99=1.261449ms max=279.127487ms`; `publish -> gateway: n=294325 p50=445.129µs p95=773.474µs p99=1.196376ms max=279.285952ms` → p99 ≤ 10ms, `failed=0`, `dropped=0`, **đạt trên dev** (tốc độ thực tế 4905/s, thấp hơn 5000/s mục tiêu) | Chờ: NATS 3 node | Chưa kết luận |
| R4 | gws 50K connection | ≤ 40KB/conn; lần ghi cuối của 1 broadcast ≤ 100ms | Biến thể dev: server và client là 2 container trên `chatim_default` trong cùng VM, 4 port, `nofile=200000`, `-dial-rate 2000`, client `-duration 90s`, server `-every 5s`. Client: `connected=50000 failed=0`. RAM server (idle, trước broadcast): ≤ 10.3KB/conn (đỉnh ở 44756 conn; 9.2–10.1KB ở 50000 conn, heap 342→381MB, stack 105→114MB, sys 565→574MB). `last write done` ở 50000 conn ổn định (10 lần): 154.62581ms – 244.613828ms (lần đầu ngay sau khi dial xong: 461.139046ms), `write errors=0`, không có dòng `overlap`. Đang dial: 44756 conn → 861.286222ms, 48153 conn → 3.786643582s → RAM **đạt**, lần ghi cuối **không đạt trên dev** | Chờ: 2 host Linux, `--network host` | Chưa kết luận |
| R5 | Soft-ownership khi Redis mất dữ liệu | `go test -race -count=20 ./apps/core/internal/slot/` pass | `make -s fmt-check`, `make -s vet` sạch; `ok github.com/ivannguyendev/chatim/apps/core/internal/slot 233.328s`; `make -s test`: mọi package có test đều `ok` → **đạt ở mức unit**: miniredis giả lập Redis mất dữ liệu (`FlushAll`) và core chết, chạy `-race -count=20` | Chờ: test chaos mục 13 của thiết kế (kill Redis master khi đang tải) chưa chạy | Đạt ở mức unit; còn test chaos |

## Chạy lại R1b

Database `chatim_poc` đã được xoá sau lần dev, nên phải seed lại rồi chạy cả 3 mode trong cùng một điều kiện cache:

```bash
make -s poc TOOL=mongobench ARGS="seed -rooms 10000 -per-room 1000 -reset"
# đặt MONGO_CACHE_GB=1 trong .env
make infra-down && make infra-up
docker restart chatim-mongodb && ./scripts/wait-mongo-primary.sh
make -s poc TOOL=mongobench ARGS="read -mode oldest -duration 60s"
make -s poc TOOL=mongobench ARGS="read -mode random -duration 60s"
make -s poc TOOL=mongobench ARGS="read -mode latest -duration 60s"
# đặt lại MONGO_CACHE_GB=3 trong .env
make infra-down && make infra-up
```

Seed 10M tin mất khoảng 2 phút trên máy dev. Trên Linux host, chạy thêm `sync; echo 3 | sudo tee /proc/sys/vm/drop_caches` trước khi đọc.

## Chạy prod-like

Các bước đầy đủ nằm ở [plan Phase 1, Task 12, Step 3–8](../plans/2026-09-30-phase1-foundation-and-poc.md#task-12-chạy-poc-r1r5-và-ghi-kết-quả); phần này chỉ tóm tắt điều kiện cần.

- **Hạ tầng cần:** MongoDB replica set 3 member (compose trong repo chỉ có 1 node `rs0` — cần dựng riêng cho lần đo này), NATS 3 node, 2 host Linux cho R4 (đồng bộ NTP), dữ liệu thật ≥1M tin.
- **R1a:** xuất dữ liệu thật bằng `mongoexport` trong image `mongo:8.2` (lệnh ở Step 3 của plan). **Cảnh báo:** `real-texts.txt` chứa dữ liệu khách hàng — chỉ để trên máy chạy PoC, không commit (đã có trong `.gitignore`), xoá ngay sau khi đo.
- **R4 và các lần chạy trên host không có Go:** build image `make image TARGET=tools/poc/<tool>`, chuyển bằng `docker save … | ssh <host> docker load`, chạy với `--network host` (Step 8).

## Quyết định sau PoC

Chỉ có quan sát trên máy dev; **chưa có quyết định giữ/đổi thiết kế**. Quyết định go/no-go cho R1–R4 cần cột prod-like: MongoDB replica set 3 member, dữ liệu thật (≥1M tin từ Mongo cũ), NATS 3 node, 2 host Linux cho gws.

- **Đạt trên dev:** R3a (RePublish đổi subject), R3b (1M sub, publish→gateway p99 1.196376ms, `failed=0 dropped=0`), RAM của R4 (≤ 10.3KB/conn, đỉnh ở 44756 conn; 9.2–10.1KB ở 50000 conn), R5 ở mức unit (miniredis giả lập Redis mất dữ liệu bằng `FlushAll` và core chết, `-race -count=20`).
- **Không đạt trên dev:**
  - R1b oldest: p99 23.357931ms > 20ms, dù dữ liệu on-disk (932.1MB) có thể nằm hết trong page cache của VM. Vì lượt đọc có thể được phục vụ từ page cache của VM thay vì đĩa, 23.36ms là con số lạc quan; trên prod-like với dữ liệu thật lớn hơn RAM, p99 nhiều khả năng cao hơn chứ không thấp hơn, nên R1b là phép đo prod-like cần ưu tiên nhất.
  - R2: `insertMany` p99 26.816186ms nhưng thời gian chờ ack p99 213.521741ms > 30ms; max `insertMany` 523.148044ms cho thấy có các lần ghi bị nghẽn khoảng 0.5s, tin đến trong lúc đó xếp hàng sau flusher. Trên prod-like cần xem các lần nghẽn này còn không, và thử "Nếu không đạt" của R2 (kích thước batch, cửa sổ flush, số flusher, disk/IOPS).
  - R4: lần ghi cuối 154.62581ms – 244.613828ms > 100ms ở 50K conn; server và client dùng chung 10 vCPU nên kết quả dev bi quan. Trên máy dev, lần ghi cuối còn ≤ 100ms ở 28973 conn (98.157828ms, lúc client vẫn đang dial).
- **Chưa đo được trên dev:** R1a với dữ liệu thật; R1b mode random và latest (chưa chạy được do giới hạn quyền của môi trường chạy tự động lúc đó (không phải lỗi mongobench); xem [Chạy lại R1b](#chạy-lại-r1b)); `w:majority` trên 3 member; test chaos R5 (kill Redis master khi đang tải, mục 13 của thiết kế).
- Văn bản giả lần này nén 3.37x (không phải ~17x như lần chạy thử trước), càng cho thấy dung lượng R1a phải đo bằng dữ liệu thật.
- Chưa cập nhật mục 13 / Decision Log của thiết kế: các tiêu chí không đạt ở trên chỉ là số dev, cần xác nhận trên prod-like trước khi áp dụng cột "Nếu không đạt".
