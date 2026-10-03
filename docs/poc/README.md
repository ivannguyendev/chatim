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
| R1b | Trang cũ nhất / ngẫu nhiên / mới nhất khi dữ liệu > WiredTiger cache | p99 ≤ 20ms, plan `CLUSTERED_IXSCAN` | `MONGO_CACHE_GB=1`, tạo lại container rồi restart. Dữ liệu logical 3137.4MB > cache 1GB, nhưng on-disk chỉ 932.1MB (sau restart, `mongosh` `db.getSiblingDB("chatim_poc").messages.stats()` in `storageSize MB= 947.2`) nên vừa page cache của VM (11.75GiB, không xoá được ở lần này) → đọc có thể không chạm đĩa, điều kiện "lạnh" chưa thật sự đạt. **oldest:** `plan (oldest page): LIMIT > CLUSTERED_IXSCAN`; `mode=oldest pages=151763 (2529/s) empty=0 errors=0 latency: n=151763 p50=4.967976ms p95=16.186169ms p99=23.357931ms max=95.904614ms` → `empty=0`, `errors=0` nhưng p99 > 20ms, **không đạt trên dev**. **random, latest: chưa chạy được do giới hạn quyền của môi trường chạy tự động lúc đó (không phải lỗi mongobench)**; cần chạy lại — xem [Chạy lại R1b](#chạy-lại-r1b) bên dưới. **Cập nhật 2026-09-30:** số trên bị đội bởi chi phí decode của `mongobench` (đã sửa); đo lại với 10M tin nạp xen kẽ, cache 1GB sau restart: p99 oldest 8.864532ms, random 8.437079ms, latest 7.569245ms → **đạt trên dev** (chi tiết: [so sánh MongoDB/PostgreSQL](260930-mongodb-vs-postgresql.md)) | Chờ: rs 3 member, dữ liệu thật, xoá page cache | Đạt trên dev (sau khi sửa công cụ) |
| R2 | `insertMany` w:majority ở 10K tin/s | Thời gian chờ ack p99 ≤ 30ms, errors = 0 | 1 node rs0 (w:majority chỉ gồm 1 member). `target=10000/s achieved=9999/s docs=599987 batches=37687 avg-batch=15.9 errors=0`; `arrival->commit: n=599987 p50=8.122084ms p95=25.01663ms p99=213.521741ms max=549.40756ms`; `insertMany w:majority round trip: n=37687 p50=5.756332ms p95=8.901774ms p99=26.816186ms max=523.148044ms` → errors=0 nhưng p99 chờ ack > 30ms, **không đạt trên dev** | Chờ: replica set 3 member | Chưa kết luận |
| R3a | RePublish đổi subject | `republish transform: PASS` | `republish transform: PASS` → **đạt trên dev** | Chờ: NATS 3 node | Đạt trên dev; xác nhận lại trên cluster 3 node |
| R3b | 1M interest sub + 5K event/s | publish→gateway p99 ≤ 10ms; RAM server (ghi MB) | `-subs 1000000 -conns 4 -rate 5000 -duration 60s`: `subscribed 1000000 rooms on 4 connections in 5.101s; server mem=925MB subscriptions=1000066`; `target=5000/s published=294325 (4905/s) failed=0 received=294325 dropped=0; server mem=1715MB`; `jetstream publish ack: p50=456.473µs p95=799.798µs p99=1.261449ms max=279.127487ms`; `publish -> gateway: n=294325 p50=445.129µs p95=773.474µs p99=1.196376ms max=279.285952ms` → p99 ≤ 10ms, `failed=0`, `dropped=0`, **đạt trên dev** (tốc độ thực tế 4905/s, thấp hơn 5000/s mục tiêu) | Chờ: NATS 3 node | Chưa kết luận |
| R4 | gws 50K connection | ≤ 40KB/conn; lần ghi cuối của 1 broadcast ≤ 100ms | Biến thể dev: server và client là 2 container trên `chatim_default` trong cùng VM, 4 port, `nofile=200000`, `-dial-rate 2000`, client `-duration 90s`, server `-every 5s`. Client: `connected=50000 failed=0`. RAM server (idle, trước broadcast): ≤ 10.3KB/conn (đỉnh ở 44756 conn; 9.2–10.1KB ở 50000 conn, heap 342→381MB, stack 105→114MB, sys 565→574MB). `last write done` ở 50000 conn ổn định (10 lần): 154.62581ms – 244.613828ms (lần đầu ngay sau khi dial xong: 461.139046ms), `write errors=0`, không có dòng `overlap`. Đang dial: 44756 conn → 861.286222ms, 48153 conn → 3.786643582s → RAM **đạt**, lần ghi cuối **không đạt trên dev** | Chờ: 2 host Linux, `--network host` | Chưa kết luận |
| C1 | Core ×2 qua gRPC ở 10K tin/s (corebench, open-loop, M2a) | Ack p99 ≤ 30ms (A1, trên dev chỉ để tham khảo), failed = 0, shed_by_client = 0 | `-rate 10000 -duration 60s -watch 20`, 2 lần: `acked=9426/s` và `7546/s`, `shed_by_client=34429` và `147085`, `failed=2` và `152`, ack p99 `1.018039844s` và `2.368397163s` → **không đạt trên dev**. 5K/s vẫn giữ đủ tải (`shed_by_client=0 failed=0`) nhưng p99 `504.567349ms`; không mức nào đạt 30ms, kể cả 500/s (p99 `85.278617ms`). Chi tiết ở [C1](#c1-corebench-2-core-qua-grpc-dev-2026-10-02) | Chờ: rs 3 member, core và corebench trên host riêng | Chưa kết luận |
| R5 | Soft-ownership khi Redis mất dữ liệu | `go test -race -count=20 ./apps/core/internal/slot/` pass | `make -s fmt-check`, `make -s vet` sạch; `ok github.com/ivannguyendev/chatim/apps/core/internal/slot 233.328s`; `make -s test`: mọi package có test đều `ok` → **đạt ở mức unit**: miniredis giả lập Redis mất dữ liệu (`FlushAll`) và core chết, chạy `-race -count=20` | Chờ: test chaos mục 13 của thiết kế (kill Redis master khi đang tải) chưa chạy | Đạt ở mức unit; còn test chaos |

### R2: so sánh write concern (dev, 2026-09-30)

Cùng điều kiện R2 (`write -rate 10000 -duration 60s -rooms 5000 -flushers 6`), đổi `-w` / `-j`:

| Chế độ | Chờ ack p50 | Chờ ack p99 | `insertMany` p50 | `insertMany` p99 | max |
|---|---|---|---|---|---|
| `w:majority` | 8.431187ms | 60.946999ms | 6.054299ms | 25.872161ms | 291.900378ms |
| `w:1` | 3.17691ms | 10.083733ms | 955.086µs | 4.079057ms | 110.504033ms |
| `w:1`, `j:true` | 9.787944ms | 42.291314ms | 7.227682ms | 27.88114ms | 275.213302ms |

- Trên replica set 1 node, `w:majority` gần bằng `w:1` + `j:true`: phần tốn thời gian là chờ ghi journal xuống đĩa (fsync của đĩa ảo OrbStack), không phải replication.
- `w:1` đạt tiêu chí (p99 10.08ms ≤ 30ms) nhưng **không bền**: server ack khi mới ghi vào bộ nhớ, journal flush sau tối đa khoảng 100ms; mongod crash hoặc primary failover có thể làm mất tin đã ack. Điều này trái với giả định A2 của thiết kế (đã ack = đã lưu `w:majority`), nên không dùng `w:1` cho tin nhắn.
- `w:1` phù hợp cho các lần ghi có thể dựng lại hoặc idempotent: cập nhật chậm `rooms.last_seq` / `read_seq` bằng `$max` (thiết kế mục 5.2–5.3).
- Lần chạy `w:majority` này có p99 60.95ms, lần R2 trước là 213.52ms: độ dao động giữa các lần chạy trên dev lớn. Cần đo `w:majority` trên rs 3 member với đĩa NVMe thật trước khi kết luận.

### C1: corebench, 2 core qua gRPC (dev, 2026-10-02)

**Máy và điều kiện.** Cùng máy dev ở bảng Môi trường (OrbStack, VM 10 vCPU / 11.75GiB). MongoDB 1 node `rs0` (`w:majority` chỉ gồm 1 member), NATS 1 node, Redis 1 node. Hai core `chatim-core-1`, `chatim-core-2` (image `chatim/core:dev`, `mem_limit 1g`, `GOMEMLIMIT=920MiB`), mỗi core giữ 512/1024 slot. corebench chạy trong container `golang:1.26` trên cùng VM, nên công cụ đo, 2 core, MongoDB, NATS và Redis tranh nhau 10 vCPU. **Chỉ để tham khảo; quyết định cần prod-like.**

**Lệnh.** `make -s poc TOOL=corebench ARGS="-rate <R> -duration 60s -watch 20"`. Các giá trị mặc định của corebench: 1000 room group, mỗi room 4 member (người gửi chọn ngẫu nhiên trong room); chọn room đều (không `-zipf`); warmup 5s không tính vào kết quả; `-max-inflight 4096`; deadline của 1 lần gửi 10s (gồm cả thử lại), mỗi attempt 5s; văn bản giả (`msgtext.Synthetic`, 4096 câu); mỗi lần gửi một cid riêng. `-watch 20` theo dõi subject live của 20 room đầu.

**Config core.** Compose không đặt biến nào, nên core chạy với giá trị mặc định: `FLUSH_SHARDS=4`, `FLUSH_WINDOW=2ms`, `FLUSH_MAX_BATCH=256`, `FLUSH_QUEUE=1024`, `FLUSH_INSERT_TIMEOUT=1s`, `ACTOR_MAX_GROUP=64`, `ACTOR_MAILBOX=1024`, `CORE_REQUEST_DEADLINE=3s`, `CORE_MAX_INFLIGHT=2048`, `CORE_QUEUE_WAIT=25ms`, `REDIS_OP_TIMEOUT=100ms`, `CID_COMMITTED_TTL=15m`. corebench in các biến này từ env của chính nó (`default` nghĩa là không đặt), không đọc từ core.

**Cách đo.**
- Open-loop: lần gửi thứ i được hẹn lúc `start + i/rate`. Độ trễ ack tính từ **thời điểm hẹn**, nên khi core (hoặc chính corebench) bị nghẽn, mọi lần gửi đến hạn trong lúc đó đều mang phần chờ này (không bị coordinated omission).
- Khi đã có `max-inflight` lần gửi đang chờ, lần đến hạn tiếp theo bị bỏ và đếm vào `shed_by_client`; pacer không bao giờ bị chặn.
- `pacer lag` = lúc thực sự gửi − lúc hẹn, và nằm trong độ trễ ack. Từ 7.5K/s trở lên, pacer lag p99 khoảng 90–100ms, tức chính máy đo đã thiếu CPU.
- Live lag = lúc corebench nhận event − `ts` của event. `ts` là lúc core gán seq (trước khi insert), nên live lag gồm cả insert, commit Redis, publish JetStream và RePublish. Mọi container dùng chung kernel của VM nên chung đồng hồ; tuy vậy clocksource `tsc` của VM thỉnh thoảng lùi khoảng 1ms (log core có `duration_ms` âm, `time.Since` trong container trả về -1ms), nên sai số cỡ ±1ms.
- Trước mỗi lần chạy, trừ 2 lần đầu: xoá key `chatim:cid:*:cb*` của các lần bench trước và `CONFIG RESETSTAT`. Các key này chỉ là bản ghi chống trùng của tin bench đã commit, và bench không bao giờ gửi lại cid cũ. Lý do phải xoá nằm ở mục "Nơi tốn thời gian" bên dưới.

| Lần | Rate mục tiêu | Redis trước khi chạy (`dbsize`) | acked/s | shed_by_client | failed (mã) | Lần gửi có thử lại / max attempts | Ack p50 | Ack p95 | Ack p99 | Ack p99.9 | Ack max | Live lag p50 / p99 | Pacer lag p99 | CPU TB: core-1 / core-2 / mongod / nats / redis / corebench |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | 10000/s | Còn key của các lần chạy thử (không đếm) | 9426 | 34429 (5.7%) | 2 (DeadlineExceeded 1, Unavailable 1) | 2365 / 3 | 206.346966ms | 565.572519ms | 1.018039844s | 7.0439397s | 9.990229438s | 111.62872ms / 452.946079ms | 99.721744ms | 296 / 277 / 230 / 70 / 67 / 225% |
| 2 | 2000/s | ~200K+ còn lại từ lần 1 (334030 key sau lần chạy) | 2000 | 0 | 0 | 0 / 1 | 20.881907ms | 157.959367ms | 474.07051ms | 769.357027ms | 891.887076ms | 18.171719ms / 304.02808ms | 48.320631ms | 171 / 194 / 199 / 60 / 107 / 111% |
| 3 | 2000/s | Sạch (3666 key tổng) | 2000 | 0 | 0 | 0 / 1 | 16.099827ms | 59.118942ms | 138.900973ms | 291.535306ms | 382.397221ms | 14.562295ms / 136.204916ms | 8.169424ms | 192 / 185 / 187 / 65 / 72 / 104% |
| 4 | 5000/s (kèm pprof core-1 20s) | Sạch (4984) | 5000 | 0 | 0 | 0 / 1 | 48.17521ms | 235.147348ms | 504.567349ms | 813.577644ms | 1.244848535s | 35.296855ms / 265.114557ms | 25.184324ms | 291 / 275 / 255 / 76 / 109 / 154% |
| 5 | 1000/s | Sạch (5666) | 1000 | 0 | 0 | 0 / 1 | 11.538815ms | 34.38055ms | 89.253917ms | 151.685016ms | 177.18554ms | 10.863902ms / 85.347745ms | 5.325191ms | 129 / 116 / 163 / 53 / 50 / 91% |
| 6 | 500/s | Sạch (7986) | 500 | 0 | 0 | 0 / 1 | 9.778728ms | 30.413015ms | 85.278617ms | 162.880243ms | 189.491981ms | 9.164631ms / 78.11343ms | 10.867065ms | 87 / 87 / 137 / 35 / 34 / 74% |
| 7 | 7500/s | Sạch (9308) | 7319 | 10845 (2.4%) | 0 | 1253 / 2 | 223.789716ms | 617.622743ms | 885.549854ms | 4.669676444s | 6.141840591s | 121.77568ms / 465.47567ms | 96.876784ms | 323 / 317 / 258 / 85 / 75 / 202% |
| 8 | 10000/s | Sạch (10363) | 7546 | 147085 (24.5%) | 152 (Unavailable 151, DeadlineExceeded 1) | 10911 / 4 | 360.433224ms | 1.077556943s | 2.368397163s | 5.705564249s | 9.731086459s | 176.271873ms / 744.911857ms | 89.18838ms | 303 / 328 / 278 / 112 / 60 / 237% |
| 9 | 2000/s (sau D25) | Không xoá (10749) | 2000 | 0 | 0 | 0 / 1 | 14.226624ms | 41.824847ms | 118.922064ms | 231.421387ms | 322.743537ms | 13.480464ms / 85.979191ms | 5.053541ms | không đo |
| 10 | 5000/s (sau D25) | Không xoá (141423) | 5000 | 0 | 0 | 0 / 1 | 36.52522ms | 144.46064ms | 291.830813ms | 438.348353ms | 566.035187ms | 28.95389ms / 177.786792ms | 15.466907ms | không đo |
| 11 | 10000/s (sau D25) | Không xoá (459137) | 9452 | 32893 (5.5%) | 0 | 3698 / 5 | 198.460026ms | 573.013277ms | 818.120106ms | 1.009077709s | 1.239543751s | 122.133479ms / 492.715092ms | 96.098976ms | không đo |
| 12 | 5000/s (M2a.1, 2026-10-03) | Không xoá, không đếm | 4890 | 6583 (2.2%) | 0 | 338 / 3 | 70.133333ms | 600.528096ms | 1.453632333s | 2.188710997s | 2.744326407s | 52.472982ms / 752.517726ms | 82.412071ms | không đo |
| 13 | 5000/s (M2a, infra sạch, cặp 1) | Sạch (`infra-reset`) | 5000 | 0 | 0 | 0 / 1 | 28.896202ms | 89.555403ms | 153.011724ms | 234.786292ms | 338.173241ms | 24.088281ms / 112.836673ms | 10.777864ms | không đo |
| 14 | 5000/s (M2a.1, infra sạch, cặp 1) | Sạch (`infra-reset`) | 5000 | 0 | 0 | 0 / 1 | 29.072126ms | 90.857375ms | 198.174281ms | 400.949174ms | 630.267991ms | 24.057524ms / 172.362238ms | 14.213491ms | không đo |
| 15 | 5000/s (M2a.1, infra sạch, cặp 2) | Sạch (`infra-reset`) | 5000 | 0 | 0 | 0 / 1 | 31.342515ms | 110.285368ms | 196.28116ms | 285.697254ms | 418.830724ms | 25.023772ms / 145.312213ms | 10.629624ms | không đo |
| 16 | 5000/s (M2a, infra sạch, cặp 2) | Sạch (`infra-reset`) | 5000 | 0 | 0 | 0 / 1 | 28.706354ms | 85.008436ms | 170.207129ms | 414.161622ms | 677.922281ms | 23.788106ms / 104.818311ms | 12.767881ms | không đo |
| 17 | 5000/s (M2a, infra sạch, kèm pprof core-1 20s) — **nhiễu, không dùng để so** | Sạch (`infra-reset`) | 4966 | 2037 (0.7%) | 0 | 0 / 1 | 111.895078ms | 554.772468ms | 990.578992ms | 1.236784984s | 1.595409136s | 79.256045ms / 547.48658ms | 75.17808ms | không đo |
| 18 | 5000/s (M2a.1 publisher chỉ còn hàng đợi, infra sạch, kèm pprof core-1 20s) | Sạch (`infra-reset`) | 5000 | 0 | 0 | 0 / 1 | 19.43453ms | 57.926439ms | 133.153562ms | 250.622058ms | 385.895037ms | 16.989489ms / 86.409949ms | 7.497122ms | không đo |
| 19 | 5000/s (M2a.1 publisher chỉ còn hàng đợi, infra sạch) | Sạch (`infra-reset`) | 5000 | 0 | 0 | 0 / 1 | 37.562724ms | 127.422924ms | 271.477498ms | 428.603507ms | 587.618481ms | 29.359084ms / 147.487225ms | 18.357325ms | không đo |
| 20 | 5000/s (M2a, infra sạch) | Sạch (`infra-reset`) | 5000 | 0 | 0 | 0 / 1 | 26.560117ms | 133.339161ms | 233.851627ms | 391.134177ms | 528.871241ms | 22.119786ms / 163.490035ms | 16.000114ms | không đo |

CPU là trung bình `docker stats` lấy mẫu mỗi 3s trong lúc chạy (100% = 1 vCPU). Ở mọi lần chạy, event live trên các room được theo dõi đều `missing=0 duplicates=0`. Ngoài bảng còn 1 lần 2K/s 20s chỉ để lấy pprof của corebench (`-cpuprofile`).

Lần 12 (M2a.1: bỏ pts, bỏ watermark/active mark/sweeper, publisher theo thứ tự từng room) kém hơn lần 10 cùng mức 5K/s (p99 1.45s so với 292ms, bỏ 2.2%). Hai lần không cùng điều kiện, nên không kết luận đây là hồi quy:
- Lần 10 chạy trước khi tách Redis dedupe và bật AUTH (D44, D45).
- Lần 12 chạy ngay sau `make core-up` và e2e có kill core, Redis không được dọn.
- Thay đổi của M2a.1 không nằm trên đường ack: nó bỏ một lệnh Redis trước insert và chỉ đổi phần publish sau ack.

**So sánh cùng điều kiện (lần 13–16, 2026-10-03).** Mỗi lần đều chạy sau `make infra-reset && make infra-up`, với core vừa khởi động. M2a build từ `main` @ `9994753`, M2a.1 build từ branch `fix/m2a1-event-identity`. Chạy hai cặp, cặp 2 đảo thứ tự.
- Cả 4 lần đều giữ đủ 5000/s: `shed_by_client=0`, `failed=0`, không lần gửi nào phải thử lại; live `missing=0 duplicates=0`.
- Lần 12 kém chủ yếu vì điều kiện bẩn. Khi chạy sạch, cả hai bản đều tốt hơn lần 10 và lần 12.
- M2a.1 chậm hơn M2a một chút và nhất quán:
  - ack p50 30.2ms so với 28.8ms (+5%);
  - p95 100.6ms so với 87.3ms (+15%);
  - p99 197ms so với 161.5ms (+22%), với p99 của M2a.1 rất ổn định (198/196ms);
  - p99.9 thì lẫn lộn giữa hai cặp: 401/286ms so với 235/414ms.
- Live lag p99 cao hơn rõ: 172/145ms so với 113/105ms. Nguyên nhân là bản D50 đầu tiên: batch sau của một room chờ ack của batch trước.
- Mức chênh nằm trong ngưỡng chấp nhận đặt trước (p99 ≤ +25%, không bỏ lượt, không lỗi).

**Sau khi sửa D50 (lần 17–20, 2026-10-04).** Publisher chỉ còn hàng đợi theo shard, gọi `PublishMsgAsync` lần lượt từng event và không chờ ack; theo dõi publish đang bay, timeout ack và gửi lại do nats.go lo. Quy trình như lần 13–16.
- Lần 17 (M2a kèm pprof) bị nhiễu: pacer lag p99 75ms so với khoảng 8–18ms ở các lần khác, tức chính máy đo thiếu CPU. Lần này không dùng để so; profile của nó vẫn được dùng.
- Trung bình các lần sạch:
  - M2a (13, 16, 20): ack p50 / p95 / p99 = 28.1 / 102.5 / 186ms; live lag p99 127ms.
  - M2a.1 bản mới (18, 19): 28.5 / 92.7 / 202ms (p95 −10%, p99 +9%); live lag p99 116.5ms (−8%).
  - Bản D50 cũ (14, 15): live lag p99 158.5ms.
- Bỏ chờ theo room đưa live lag về mức M2a. Ack p99 chênh +9%, nằm trong nhiễu: cùng bản M2a.1 mà lần 18 là 133ms, lần 19 là 271ms. Cả 5 lần sạch đều 0 lượt bỏ, 0 lỗi, live 0 thiếu/0 trùng.
- **pprof** (CPU core-1, 20s ở 5K/s, mỗi core khoảng 2.5K tin/s):
  - Publisher: 8.4% CPU ở M2a (gồm watermark Redis), 4.4% ở M2a.1.
  - Số goroutine: 1214 so với 653.
  - Ở cả hai bản, phần tốn nhất là Redis chống trùng cid (`dedupe.Store.Reserve` + `Commit`): 17% ở M2a, 24% ở M2a.1, theo tỉ lệ trên tổng CPU của từng lần. Tiếp theo là `InsertMany` Mongo (8–12%), GC (8–10%) và syscall write ra mạng.
  - Nếu cần giảm ack latency, đây là các ứng viên: gộp Reserve/Commit theo nhóm ghi, giảm cấp phát.

**Điểm gãy (knee).**
- Thông lượng: giữ đủ tải (`shed_by_client=0 failed=0`, không lần nào phải thử lại) tới 5K/s. 7.5K/s chỉ đạt 7319/s (bỏ 2.4%); 10K/s đạt 7546–9426/s (bỏ 5.7–24.5%; lần 8 có 151 lần gửi kết thúc bằng `Unavailable`). Knee thông lượng trên dev nằm giữa 5K và 7.5K/s.
- Độ trễ: p99 là 85ms (500/s), 89ms (1K), 139ms (2K), 505ms (5K), 886ms (7.5K). p50 khoảng 10–16ms tới 2K/s rồi tăng mạnh khi lên 5K/s (48ms).
- Hai lần 10K/s chênh nhau nhiều (p99 1.02s và 2.37s): số đo trên dev dao động mạnh, giống R2.

**Nơi tốn thời gian.**
1. **VM bão hoà CPU.** Tổng CPU của các container khoảng 805% ở 2K/s, 1160% ở 5K/s và 1165–1320% ở 7.5K–10K/s, trên tổng 10 vCPU. Từ 5K/s, mọi thành phần đều xếp hàng chờ CPU. Riêng corebench dùng 0.7–2.4 vCPU; theo pprof (`-cpuprofile`), phần lớn là việc của gRPC client: framing HTTP/2 và một syscall write cho mỗi RPC.
2. **Pprof core-1 ở 5K/s** (khoảng 2.5K tin/s mỗi core, lấy 20s bằng `curl http://core-1:9090/debug/pprof/profile?seconds=20` từ một container trên `chatim_default`):
   - Redis cho chống trùng cid ~20% (`redisguard.Do`): mỗi tin có 1 `EVALSHA` cho Reserve và 1 pipeline cho Commit, không gộp theo nhóm ghi.
   - GC và cấp phát ~25% (`gcBgMarkWorker` 13.8%, `mallocgc` 12.2%).
   - Log JSON mức INFO cho mỗi RPC ra stdout ~10.6% (`logRPC`).
   - Insert Mongo ~7.5%. Syscall write (mạng và log) chiếm 13.5%.
3. **Redis `SCAN` quét cả keyspace** — phát hiện quan trọng nhất của lần đo này. Mỗi tick (1s), slot manager của mỗi core tìm core còn sống bằng `SCAN chatim:core:*`, và resolver phía client quét lại mỗi lần refresh. `SCAN` đi qua **toàn bộ keyspace**, trong khi keyspace chứa mọi key `chatim:cid:*` (TTL 15 phút).
   - Lúc nghỉ, với khoảng 312K key cid: 2438 lệnh `SCAN`/s, và Redis bận 31% chỉ để quét. Tính từ lúc Redis khởi động (44 giờ, gồm các lần e2e, chạy thử, lần 1 và lần 2), `SCAN` chiếm 60.8s trong khoảng 93s CPU của Redis (403088 lệnh, 150µs/lệnh). Ngay cả ở lần 3 (bắt đầu sạch), `SCAN` vẫn là lệnh tốn CPU Redis nhất (9.7s trong khoảng 19.5s).
   - Lần 2 (còn ~200K+ key cid) có p99 474ms, so với 139ms ở lần 3 (sạch) cùng mức 2K/s.
   - Ở lần 7 và 8 (7.5K và 10K/s), lệnh Redis của core vượt `REDIS_OP_TIMEOUT=100ms`, nên core chuyển chống trùng sang chỉ dùng LRU, bỏ qua active room mark (`sends continue without recovery marks`) và tạm dừng watermark publish. Log của mỗi core có khoảng 27 lần cho mỗi loại trong 2 lần chạy này (lần 1 không có). Sau lần 8, Redis có 114683 key (trước khi chạy là 10363), dù đã ack 452763 tin: các tin gửi lúc suy giảm không có bản ghi chống trùng trong Redis.
   - Resolver của corebench báo `slot table reload failed: scan cores: context deadline exceeded`.
   - Nếu chạy liên tục 10K tin/s, keyspace sẽ có khoảng 9M key cid (10K × 900s). Khi đó mỗi lượt quét cần khoảng 35K lệnh `SCAN` và không thể xong trong 1 tick.
   - **Đề xuất (chưa quyết định, chưa sửa):** lưu danh sách core còn sống ở một key riêng (ví dụ ZSET `chatim:cores` với score là hạn heartbeat) thay cho `SCAN`, hoặc tách key cid sang Redis DB hoặc instance khác. Cần đưa vào thiết kế mục 5.1 và Decision Log. **Cập nhật 2026-10-02:** đã sửa theo D25 (ZSET `chatim:cores`, không còn `SCAN`), kèm log RPC chỉ ghi lỗi/chậm và client bỏ service config từ DNS; đã đo lại ở lần 9–11 (xem "Sau khi sửa D25").
4. **VM đánh thức timer trễ.** Trong một container nhàn rỗi, `time.Sleep` theo nhịp 100µs bị trễ p50 1.1ms, p99 14.9ms, max 36ms. Mỗi hop (client → core → Redis → Mongo → NATS) đều có thể chịu thêm phần trễ này; đây có thể là lý do p99 ở 500/s vẫn khoảng 85ms dù CPU còn dư.

**Sau khi sửa D25 (lần 9–11).** Cùng máy, cùng lệnh, nhưng **không xoá** key cid giữa các lần chạy: lần 11 bắt đầu khi Redis đã có 459137 key (gấp đôi mức ~200K từng làm p99 tệ đi 3× ở lần 2).
- 2K/s: p99 139ms → 119ms (so với lần 3 sạch). 5K/s: p99 505ms → 292ms, p50 48ms → 37ms (so với lần 4).
- 10K/s: đạt 9452/s (trước 7546–9426), bỏ 5.5% (trước 5.7–24.5%), `failed=0` (trước 2–152), p99 818ms (trước 1.02–2.37s). Knee thông lượng trên dev giờ gần 10K/s; phần còn bỏ là do VM hết CPU (pacer lag p99 96ms).
- Event live vẫn `missing=0 duplicates=0` ở cả 3 lần. Số key cid không còn ảnh hưởng tới độ trễ.
- Việc còn lại theo pprof ở trên: GC/cấp phát ~25%; chống trùng cid tốn 1 lệnh Redis/tin vì với 1000 room phân bố đều mỗi nhóm ghi thường chỉ có 1 tin (gộp theo flusher thay vì theo actor là hướng tối ưu sau).

**So với A1 (ack p99 ≤ 30ms, trên dev chỉ để tham khảo).** Không đạt ở mức nào, kể cả 500/s (p50 9.778728ms, p95 30.413015ms, p99 85.278617ms). Sau D25, 10K/s có p99 818ms (trước 1.02–2.37s). Chưa kết luận: cần chạy lại trên prod-like (core và corebench trên host riêng, rs 3 member, Redis sentinel); vấn đề `SCAN` đã xử lý (D25).

**Lỗi lúc setup, đã sửa trong công cụ.** Hai lần chạy (lần smoke đầu tiên và lần thử 10K/s đầu tiên, đều không ghi vào bảng) dừng ở bước tạo room: 32 `CreateRoom` cùng trả `DeadlineExceeded` sau 5s, và core không nhận được request nào. Nguyên nhân nhiều khả năng là resolver DNS của grpc-go: nó tra bản ghi TXT `_grpc_config.<host>` trước khi trả địa chỉ, Docker chuyển truy vấn này ra DNS của host, và khi DNS host chậm thì mọi RPC đầu tiên phải chờ tới hết hạn. Đã tái hiện đúng lỗi này bằng `POC_FLAGS="--dns 10.255.255.1"`, và sửa bằng `grpc.WithDisableServiceConfig()` trong dialer của `tools/internal/route` (service config mặc định khai báo trong code vẫn được dùng). Gateway (M4) gọi core qua `pkg/grpcclient` mặc định cũng sẽ gặp lỗi này.

Chạy lại một mức từ trạng thái sạch:

```bash
docker exec chatim-redis sh -c "redis-cli --scan --pattern 'chatim:cid:*:cb*' --count 5000 | xargs -r -n 1000 redis-cli unlink"
docker exec chatim-redis redis-cli config resetstat
make -s poc TOOL=corebench ARGS="-rate 5000 -duration 60s -watch 20"
docker exec chatim-redis redis-cli info commandstats
```

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
  - R1b oldest (lần đo đầu, trước khi sửa công cụ; lần đo lại đã đạt — xem [so sánh MongoDB/PostgreSQL](260930-mongodb-vs-postgresql.md)): p99 23.357931ms > 20ms, dù dữ liệu on-disk (932.1MB) có thể nằm hết trong page cache của VM. Vì lượt đọc có thể được phục vụ từ page cache của VM thay vì đĩa, 23.36ms là con số lạc quan; trên prod-like với dữ liệu thật lớn hơn RAM, p99 nhiều khả năng cao hơn chứ không thấp hơn, nên R1b là phép đo prod-like cần ưu tiên nhất.
  - R2: `insertMany` p99 26.816186ms nhưng thời gian chờ ack p99 213.521741ms > 30ms; max `insertMany` 523.148044ms cho thấy có các lần ghi bị nghẽn khoảng 0.5s, tin đến trong lúc đó xếp hàng sau flusher. Trên prod-like cần xem các lần nghẽn này còn không, và thử "Nếu không đạt" của R2 (kích thước batch, cửa sổ flush, số flusher, disk/IOPS).
  - C1 (corebench, M2a): 2 core trên dev giữ đủ 5K tin/s nhưng ack p99 504.567349ms; 10K/s bỏ 5.7–24.5% và p99 1.02–2.37s; không mức nào đạt A1 (≤ 30ms), kể cả 500/s (85.278617ms). Phát hiện: `SCAN chatim:core:*` quét cả keyspace chứa key cid, nên Redis chậm dần khi số key cid tăng và core rơi vào chế độ suy giảm. Đã sửa bằng ZSET `chatim:cores` (D25); đo lại: 10K/s đạt 9452/s, bỏ 5.5%, `failed=0`, p99 818ms; 5K/s p99 292ms, dù Redis có tới 459K key cid (xem [C1](#c1-corebench-2-core-qua-grpc-dev-2026-10-02)).
  - R4: lần ghi cuối 154.62581ms – 244.613828ms > 100ms ở 50K conn; server và client dùng chung 10 vCPU nên kết quả dev bi quan. Trên máy dev, lần ghi cuối còn ≤ 100ms ở 28973 conn (98.157828ms, lúc client vẫn đang dial).
- **Chưa đo được trên dev:** R1a với dữ liệu thật; R1b mode random và latest (chưa chạy được do giới hạn quyền của môi trường chạy tự động lúc đó (không phải lỗi mongobench); xem [Chạy lại R1b](#chạy-lại-r1b)); `w:majority` trên 3 member; test chaos R5 (kill Redis master khi đang tải, mục 13 của thiết kế).
- Văn bản giả lần này nén 3.37x (không phải ~17x như lần chạy thử trước), càng cho thấy dung lượng R1a phải đo bằng dữ liệu thật.
- Chưa cập nhật mục 13 / Decision Log của thiết kế: các tiêu chí không đạt ở trên chỉ là số dev, cần xác nhận trên prod-like trước khi áp dụng cột "Nếu không đạt".
