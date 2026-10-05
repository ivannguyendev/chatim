# chatim

Hạ tầng chat dùng chung (CPaaS nội bộ) cho nhiều sản phẩm: quản lý room, tin nhắn, tương tác realtime hiệu năng cao; lấy lịch sử cực nhanh ở bất kỳ vị trí nào; phát event mạnh tới các app khác kết nối vào. Multi-tenant về mặt logic.

> Trạng thái: **M0–M1 (nền tảng + PoC) và M2a (core: CreateRoom/SendMessage/GetHistory qua gRPC, chống trùng cid, publish JetStream, 2 core trong compose) đã xong trên máy dev**. M2a.1 (bỏ `pts` toàn room, id event tự nhiên, publisher chỉ còn hàng đợi trên cơ chế async của nats.go, event best-effort — D47–D51) xong trên máy dev. M2a.2 (reconciler trên core giữ slot 0 đọc change stream của `messages`, publish bù event chưa có mark đã ack — D52) xong trên máy dev. M2a.3 (perf đường ghi: gom lệnh chống trùng cid giữa các room, ack trước Commit, client Redis dedupe pool nhỏ và ấm, mark đã ack gom trong cửa sổ 10ms — D58–D60) xong trên máy dev; M2a.2 và M2a.3 đã merge cùng lúc (PR #11). Rà soát cơ chế hệ thống đã chốt (2026-10-05): [thiết kế kiến trúc](docs/designs/261005-chatim-architecture.md); tiếp theo là M2b.0 theo [roadmap](docs/roadmap.md); quyết định go/no-go chờ PoC prod-like. Kết quả đo: [docs/poc/README.md](docs/poc/README.md). Bản đồ code: [INDEXES.csv](INDEXES.csv).

## Kiến trúc

Monorepo Go, mỗi app một container.

| App | Vai trò | Phase |
|---|---|---|
| `core` | Room, member, message, tương tác; cấp seq; lưu MongoDB; phát event best-effort lên NATS JetStream (id event tự nhiên, thứ tự từng room); reconciler bù event bị bỏ từ change stream | 1 |
| `gateway` | WebSocket (gws) cho client SDK; nhận event từ NATS và đẩy realtime | 1 |
| `api`, `events`, `push`, `auth`, `migrator` | Public API, event stream cho app ngoài, push notification, xác thực, migrate/dual-write từ hệ thống cũ | Sau |

Hạ tầng: MongoDB (replica set), Redis, NATS JetStream.

## Cấu trúc

```
apps/core/               # main.go + internal/{actor,flush,dedupe,publish,eventmark,reconcile,slot,grpcsrv,store,pbconv,config,domain,…}
pkg/                     # dùng chung: keys, ids, slotmap, apperr, envconfig, resilience, admin, grpcserver, grpcclient, backoff, pb
proto/chatim/v1/         # định nghĩa protobuf (buf) → pkg/pb
tools/                   # corecli, internal/route; poc/: corebench, mongobench, postgresbench, natsbench, wsbench
scripts/                 # e2e.sh, các script chờ hạ tầng sẵn sàng, bench-cell.sh / bench-sample.sh (đo corebench)
deploy/docker/           # 1 Dockerfile, chọn chương trình bằng ARG TARGET
deploy/compose/          # hạ tầng dev
docs/                    # nghiên cứu, thiết kế, plan, kết quả PoC
```

## Chạy hạ tầng dev

Chỉ cần Docker; Go chạy trong container `golang:1.26` qua `make`.

    cp .env.example .env        # đổi MONGO_ROOT_PASSWORD, REDIS_PASSWORD, REDIS_DEDUPE_PASSWORD (chữ, số, - hoặc _)
    make infra-up               # mongo rs0 :27117, redis state :6380, redis dedupe :6381, nats :4223 (monitor :8223)
    make redis-cli ARGS="info memory"                      # redis-cli trong container, mật khẩu lấy từ secret
    make redis-cli INSTANCE=dedupe ARGS="dbsize"           # INSTANCE=state (mặc định) hoặc dedupe
    make test                   # go test -race ./... trong container
    make go ARGS="vet ./..."    # lệnh go bất kỳ
    make infra-down             # dừng; make infra-reset để xoá cả dữ liệu

Image chạy cho một chương trình Go bất kỳ: `make image TARGET=tools/poc/natsbench` (distroless, không cần Go trên máy chạy).

Công cụ PoC nằm ở `tools/poc/`, chạy bằng `make poc TOOL=<tên> ARGS="…"` — cách chạy và kết quả: [docs/poc/README.md](docs/poc/README.md).

Một ô đo corebench (tuỳ chọn nghỉ 5 phút + `infra-reset`, latency monitor Redis dedupe, profiler Mongo, số liệu theo giây qua `scripts/bench-sample.sh`, kết quả vào `bin/bench/NAME/`):

    ./scripts/bench-cell.sh base-3k 3000 reset pprof
    RECONCILE_ENABLED=false ./scripts/bench-cell.sh base-3k-norec 3000 reset
    CORE_GOGC=200 ./scripts/bench-cell.sh after-3k-gogc200 3000 reset

Compose đọc `CORE_GOGC` (mặc định 100, thành `GOGC` của core) và `RECONCILE_ENABLED` (mặc định `true`) lúc `make core-up`, chỉ để đo; không có trong `.env.example`.

## Lệnh hay dùng

    make proto                  # sinh code từ proto/chatim/v1 (buf generate, ghi vào pkg/pb)
    make buf-lint                # buf lint + buf format -d --exit-code
    make lint                    # golangci-lint run ./...
    make vuln                    # go tool govulncheck ./...
    make itest                   # go test -race -shuffle=on -count=1 ./... với Mongo/Redis/NATS thật (cần infra-up)
    make core-up                 # build image apps/core, chạy core-1 + core-2 (profile app), chờ /readyz healthy
    make core-down               # dừng và xoá core-1, core-2
    make alerts-check            # promtool kiểm deploy/prometheus/alerts.yml (trong Docker)
    make e2e                     # build tools/corecli, chạy scripts/e2e.sh (route theo slot, kill core-1, kiểm tra)
    make poc TOOL=corebench ARGS="-rate 5000 -duration 60s -watch 20"   # tải mở-vòng vào cụm core, cần core-up trước

R5 (soft-ownership khi Redis mất dữ liệu), chạy nhiều lần cho chắc:

    make go ARGS="test -race -count=20 -timeout 20m ./apps/core/internal/slot/"

## Biến môi trường `apps/core`

Toàn bộ đọc qua `apps/core/internal/config`; thiếu thì dùng giá trị mặc định, sai định dạng thì `Load()` báo gộp mọi lỗi.

| Biến | Mặc định | Ghi chú |
|---|---|---|
| `CORE_ID` | hostname | Định danh core, cũng là id gửi lên `chatim:cores` |
| `CORE_GRPC_ADDR` | `:9000` | Địa chỉ lắng nghe gRPC |
| `CORE_ADVERTISE_ADDR` | `<CORE_ID>:<cổng của CORE_GRPC_ADDR>` | Địa chỉ core tự quảng cáo cho slot lease |
| `CORE_ADMIN_ADDR` | `:9090` | `/healthz`, `/readyz`, `/metrics` (Prometheus), pprof — không công khai ra ngoài mạng compose |
| `MONGO_URI`, `MONGO_DB` | — / `chatim` | Bắt buộc có `MONGO_URI`. Compose truyền URI không kèm credential |
| `MONGO_USER`, `MONGO_AUTH_SOURCE` | rỗng / `admin` | Nếu đặt `MONGO_USER` thì core xác thực bằng user này; không được đặt cùng lúc với credential trong `MONGO_URI` (D46) |
| `MONGO_PASSWORD`, `MONGO_PASSWORD_FILE` | rỗng | Mật khẩu Mongo, cần `MONGO_USER`; `_FILE` đọc từ file (bỏ newline cuối) và thắng biến thường. Compose trỏ `_FILE` vào secret `mongo_password` (D46) |
| `REDIS_ADDR`, `REDIS_DB` | `chatim-redis:6379` / `0` | Redis state: chỉ slot manager dùng — heartbeat, slot lease, pub/sub (D44, D49) |
| `REDIS_PASSWORD`, `REDIS_PASSWORD_FILE` | rỗng (không AUTH) | Mật khẩu Redis state; nếu đặt `_FILE` thì đọc từ file đó (bỏ newline cuối) và file thắng biến thường. Compose dùng `_FILE` trỏ vào secret (D45) |
| `REDIS_DEDUPE_ADDR`, `REDIS_DEDUPE_DB` | `chatim-redis-dedupe:6379` / `0` | Redis dedupe: key `chatim:cid:*` và bitmap mark đã ack `chatim:evtack:*` (D44, D52) |
| `REDIS_DEDUPE_PASSWORD`, `REDIS_DEDUPE_PASSWORD_FILE` | rỗng (không AUTH) | Như `REDIS_PASSWORD`, cho Redis dedupe |
| `NATS_URL` | `nats://chatim-nats:4222` | |
| `CORE_CONNECT_TIMEOUT` | `10s` | Timeout nối Mongo/Redis/NATS lúc khởi động |
| `CORE_REQUEST_DEADLINE` | `3s` | Deadline phía server cho mỗi unary RPC (D42), cũng là group deadline của actor |
| `CORE_QUEUE_WAIT`, `CORE_MAX_INFLIGHT` | `25ms` / `2048` | Giới hạn chờ và số RPC đang xử lý đồng thời (load shedding) |
| `CORE_DRAIN_DELAY` | `2s` | Chờ trước khi gRPC graceful stop, để LB ngừng gửi request mới |
| `CORE_GRPC_SHUTDOWN` | `5s` | Hạn graceful stop của gRPC server |
| `CORE_PUBLISHER_DRAIN` | `5s` | Hạn đẩy hết hàng đợi publisher và chờ `PublishAsyncComplete` lúc dừng; hết hạn thì bỏ phần còn lại (D49, D50) |
| `CORE_SHUTDOWN_BUDGET` | `25s` | Tổng ngân sách toàn bộ chuỗi dừng (D39); `Load()` từ chối nếu các mốc dừng cộng lại không nhỏ hơn. Mặc định các mốc là 24.2s (gồm `RECONCILE_DRAIN + 1s` của reconciler và `2 × REDIS_OP_TIMEOUT` để cid batcher gửi hết Commit), nên tăng mốc nào cũng phải tăng ngân sách này và `stop_grace_period` của compose (30s) |
| `CID_PENDING_TTL`, `CID_COMMITTED_TTL` | `10s` / `15m` | TTL key chống trùng cid ở Redis (D34) |
| `REDIS_OP_TIMEOUT`, `REDIS_COOLDOWN` | `100ms` / `1s` | Timeout mỗi lệnh Redis và thời gian chờ trước khi probe lại sau khi suy giảm |
| `CID_BATCH_SHARDS`, `CID_BATCH_MAX_KEYS`, `CID_BATCH_QUEUE` | `4` / `256` / `4096` | cid batcher: số shard (theo slot, tối đa 1024), số key tối đa mỗi lượt Reserve/Commit, hàng đợi mỗi shard (Commit/Abort đầy thì bỏ). Pool Redis dedupe = `2 × CID_BATCH_SHARDS + 4` kết nối, giữ ấm `2 × CID_BATCH_SHARDS` (D58, D60) |
| `FLUSH_SHARDS`, `FLUSH_WINDOW`, `FLUSH_MAX_BATCH`, `FLUSH_QUEUE`, `FLUSH_INSERT_TIMEOUT` | `4` / `2ms` / `256` / `1024` / `1s` | Flusher: số shard, cửa sổ gộp batch, batch tối đa, hàng đợi mỗi shard, timeout insert |
| `ACTOR_MAILBOX`, `ACTOR_IDLE`, `ACTOR_MAX_GROUP`, `ACTOR_MAX` | `1024` / `5m` / `64` / `100000` | Hàng đợi mỗi actor, thời gian nghỉ trước khi tự dừng, số lệnh gộp 1 nhóm, số actor tối đa 1 core |
| `PUB_SHARDS`, `PUB_QUEUE`, `PUB_MAX_PENDING`, `PUB_ACK_TIMEOUT` | `4` / `1024` / `256` / `2s` | Publisher JetStream: sharding theo slot, hàng đợi mỗi shard, số publish đang chờ ack mỗi shard (nats.go giới hạn `2 × shards × max pending`), timeout ack; mỗi shard phát theo thứ tự nhận, không chờ ack (D50) |
| `EVT_STREAM`, `EVT_SUBJECT_ROOT`, `EVT_LIVE_ROOT`, `EVT_STREAM_REPLICAS`, `EVT_STREAM_MAX_AGE`, `EVT_STREAM_DUPLICATES` | `CHATIM_EVT` / `evt` / `live` / `1` / `168h` / `5m` | Cấu hình stream `CHATIM_EVT` và RePublish sang `live.*`; `EVT_STREAM_DUPLICATES` phải dài hơn `RECONCILE_DELAY` |
| `WORK_STREAM`, `WORK_SUBJECT_ROOT`, `WORK_PARTITIONS`, `WORK_MAX_AGE`, `WORK_DUPLICATES` | `CHATIM_WORK` / `work` / `32` / `2h` / `2m` | Work stream WorkQueue (D79): subject `{root}.p{n}`, mỗi partition một durable pull consumer `work-p{n}` (`AckWait` = `RECONCILE_DELAY` + 30s, `MaxAckPending` 1024, replicas = `EVT_STREAM_REPLICAS`). Partition = `slot % WORK_PARTITIONS` (1–1024), do core giữ slot p tiêu thụ. Tên và gốc subject phải khác stream event; đổi số partition khi đang chạy phải xả hàng trước |
| `SLOT_TICK`, `SLOT_HEARTBEAT_TTL`, `SLOT_LEASE_TTL`, `SLOT_HOOK_TIMEOUT` | `1s` / `5s` / `10s` / `Tick/2` | Nhịp slot manager; `HookTimeout` suy ra từ `SLOT_TICK` nếu không đặt riêng (D26) |
| `EVT_ACK_MARK_TTL` | `1h` | TTL bitmap mark đã ack trên Redis dedupe, làm mới theo chunk 8192 tin; dùng chung `REDIS_OP_TIMEOUT`/`REDIS_COOLDOWN` (D52) |
| `RECONCILE_ENABLED` | `true` | Bật reader trên core giữ slot 0: đọc nhật ký commit, đẩy record vào work stream (D66, D79). Tắt thì publisher vẫn ghi mark (D52) |
| `RECONCILE_DELAY` | `5s` | D của effect `msg_created`/`room_created`: worker chạy effect sau `CommittedAt + D`; phải dài hơn `PUB_ACK_TIMEOUT` + 10ms + 1s (cửa sổ và timeout của mark) và ngắn hơn `EVT_STREAM_DUPLICATES`, kiểm cả khi `RECONCILE_ENABLED=false`; với mặc định 5s, `PUB_ACK_TIMEOUT` phải dưới khoảng 3,99s |
| `RECONCILE_WINDOW`, `RECONCILE_BATCH` | `1024` / `256` | Số record đang bay tối đa từ reader tới work stream (JetStream client riêng); bộ đệm change giữa cursor và reader |
| `RECONCILE_CONFIRM_EVERY` | `1s` | Nhịp lưu vị trí đã xác nhận (prefix record work stream đã ack) và kiểm lại slot 0; `WORK_DUPLICATES` phải dài hơn `RECONCILE_CONFIRM_EVERY + RECONCILE_DRAIN` |
| `RECONCILE_DRAIN` | `1s` | Lúc dừng: chờ publish đang bay rồi lưu vị trí lần cuối; bước dừng reconciler chiếm `RECONCILE_DRAIN + 1s` |
| `RECONCILE_ROOM_CACHE` | `65536` | Số room giữ `room_type` trong cache của effect `msg_created` |

## Cổng

Với app, compose publish ra host chỉ gRPC của core, và chỉ trên loopback — vì caller hiện được tin cậy qua metadata tới khi có mTLS (M5). Hạ tầng dev publish cổng riêng (`.env`):

| Cổng | Dịch vụ | Ghi chú |
|---|---|---|
| `6380` | `redis` state (`REDIS_PORT`) | AOF everysec, `noeviction`, bắt buộc AUTH (`REDIS_PASSWORD`) |
| `6381` | `redis-dedupe` (`REDIS_DEDUPE_PORT`) | Không lưu đĩa, `maxmemory` `REDIS_DEDUPE_MAXMEMORY` (512mb), `allkeys-lru`, bắt buộc AUTH (`REDIS_DEDUPE_PASSWORD`) |
| `127.0.0.1:9001` | `core-1` gRPC (`CORE1_GRPC_PORT`) | |
| `127.0.0.1:9002` | `core-2` gRPC (`CORE2_GRPC_PORT`) | |
| — | admin (`/healthz`, `/readyz`, `/metrics`, pprof) mỗi core, cổng `9090` | Chỉ trong mạng compose, không publish ra host |

## Tài liệu

- [Kiến trúc hệ thống (nguồn sự thật)](docs/designs/261005-chatim-architecture.md)
- [Roadmap](docs/roadmap.md)
- [Kết quả PoC](docs/poc/README.md)
- [Tổng hợp phản biện cơ chế hệ thống](docs/research/261004-system-mechanisms-synthesis.md)
- [Nghiên cứu kiến trúc chat mã nguồn mở (tinode, teamgram, chatto, gws)](docs/research/260930-opensource-chat-architecture-research.md)
- [Archive: thiết kế Phase 1, plan M0–M2a.3, ghi chú PoC cũ](docs/archive/README.md)
