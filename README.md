# chatim

Hạ tầng chat dùng chung (CPaaS nội bộ) cho nhiều sản phẩm: quản lý room, tin nhắn, tương tác realtime hiệu năng cao; lấy lịch sử cực nhanh ở bất kỳ vị trí nào; phát event mạnh tới các app khác kết nối vào. Multi-tenant về mặt logic.

> Trạng thái: **M0–M1 (nền tảng + PoC) đã xong trên máy dev**; đang chờ chạy PoC trên môi trường prod-like trước khi làm M2 (core). Kết quả: [docs/poc/README.md](docs/poc/README.md).

## Kiến trúc

Monorepo Go, mỗi app một container.

| App | Vai trò | Phase |
|---|---|---|
| `core` | Room, member, message, tương tác; cấp seq; lưu MongoDB; phát event lên NATS JetStream | 1 |
| `gateway` | WebSocket (gws) cho client SDK; nhận event từ NATS và đẩy realtime | 1 |
| `api`, `events`, `push`, `auth`, `migrator` | Public API, event stream cho app ngoài, push notification, xác thực, migrate/dual-write từ hệ thống cũ | Sau |

Hạ tầng: MongoDB (replica set), Redis, NATS JetStream.

## Cấu trúc

```
apps/{core,gateway}/     # mỗi app: main.go + internal/ (hiện có apps/core/internal/slot)
pkg/                     # thư viện dùng chung (hiện có keys, ids, slotmap; M2 thêm pb, mongox, redisx, natsx, config, logx, telemetry)
proto/chatim/v1/         # định nghĩa protobuf (buf), từ M2
tools/poc/               # công cụ đo PoC: mongobench, natsbench, wsbench
scripts/                 # script hỗ trợ (wait-mongo-primary.sh)
deploy/docker/           # 1 Dockerfile, chọn chương trình bằng ARG TARGET
deploy/compose/          # hạ tầng dev
docs/                    # nghiên cứu, thiết kế, plan, kết quả PoC
```

## Chạy hạ tầng dev

Chỉ cần Docker; Go chạy trong container `golang:1.26` qua `make`.

    cp .env.example .env        # đổi MONGO_ROOT_PASSWORD (chữ, số, - hoặc _)
    make infra-up               # mongo rs0 :27117, redis :6380, nats :4223 (monitor :8223)
    make test                   # go test -race ./... trong container
    make go ARGS="vet ./..."    # lệnh go bất kỳ
    make infra-down             # dừng; make infra-reset để xoá cả dữ liệu

Image chạy cho một chương trình Go bất kỳ: `make image TARGET=tools/poc/natsbench` (distroless, không cần Go trên máy chạy).

Công cụ PoC nằm ở `tools/poc/`, chạy bằng `make poc TOOL=<tên> ARGS="…"` — cách chạy và kết quả: [docs/poc/README.md](docs/poc/README.md).

## Lệnh hay dùng

    make proto                  # sinh code từ proto/chatim/v1 (buf generate, ghi vào pkg/pb)
    make buf-lint                # buf lint + buf format -d --exit-code
    make lint                    # golangci-lint run ./...
    make vuln                    # go tool govulncheck ./...
    make itest                   # go test -race -shuffle=on -count=1 ./... với Mongo/Redis/NATS thật (cần infra-up)
    make core-up                 # build image apps/core, chạy core-1 + core-2 (profile app), chờ /readyz healthy
    make core-down               # dừng và xoá core-1, core-2
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
| `CORE_ADMIN_ADDR` | `:9090` | `/healthz`, `/readyz`, pprof — không công khai ra ngoài mạng compose |
| `MONGO_URI`, `MONGO_DB` | — / `chatim` | Bắt buộc có `MONGO_URI` |
| `REDIS_ADDR`, `REDIS_DB` | `chatim-redis:6379` / `0` | |
| `NATS_URL` | `nats://chatim-nats:4222` | |
| `CORE_CONNECT_TIMEOUT` | `10s` | Timeout nối Mongo/Redis/NATS lúc khởi động |
| `CORE_REQUEST_DEADLINE` | `3s` | Deadline phía server cho mỗi unary RPC (D42), cũng là group deadline của actor và sweeper |
| `CORE_QUEUE_WAIT`, `CORE_MAX_INFLIGHT` | `25ms` / `2048` | Giới hạn chờ và số RPC đang xử lý đồng thời (load shedding) |
| `CORE_DRAIN_DELAY` | `2s` | Chờ trước khi gRPC graceful stop, để LB ngừng gửi request mới |
| `CORE_GRPC_SHUTDOWN` | `5s` | Hạn graceful stop của gRPC server |
| `CORE_PUBLISHER_DRAIN` | `5s` | Hạn drain hàng đợi publisher lúc dừng |
| `CORE_SHUTDOWN_BUDGET` | `25s` | Tổng ngân sách toàn bộ chuỗi dừng (D39); `Load()` từ chối nếu các mốc trên cộng lại vượt quá |
| `CID_PENDING_TTL`, `CID_COMMITTED_TTL` | `10s` / `15m` | TTL key chống trùng cid ở Redis (D34) |
| `REDIS_OP_TIMEOUT`, `REDIS_COOLDOWN` | `100ms` / `1s` | Timeout mỗi lệnh Redis và thời gian chờ trước khi probe lại sau khi suy giảm |
| `FLUSH_SHARDS`, `FLUSH_WINDOW`, `FLUSH_MAX_BATCH`, `FLUSH_QUEUE`, `FLUSH_INSERT_TIMEOUT` | `4` / `2ms` / `256` / `1024` / `1s` | Flusher: số shard, cửa sổ gộp batch, batch tối đa, hàng đợi mỗi shard, timeout insert |
| `ACTOR_MAILBOX`, `ACTOR_IDLE`, `ACTOR_MAX_GROUP`, `ACTOR_MAX` | `1024` / `5m` / `64` / `100000` | Hàng đợi mỗi actor, thời gian nghỉ trước khi tự dừng, số lệnh gộp 1 nhóm, số actor tối đa 1 core |
| `PUB_SHARDS`, `PUB_QUEUE`, `PUB_MAX_PENDING`, `PUB_ACK_TIMEOUT`, `PUB_FLUSH_EVERY`, `PUB_WATERMARK_TTL` | `4` / `1024` / `256` / `2s` / `50ms` / `168h` (7 ngày) | Publisher JetStream: sharding theo slot, hàng đợi, số ack đang chờ, timeout ack, chu kỳ flush watermark, TTL key `pubwm` (D36) |
| `EVT_STREAM`, `EVT_SUBJECT_ROOT`, `EVT_LIVE_ROOT`, `EVT_STREAM_REPLICAS`, `EVT_STREAM_MAX_AGE`, `EVT_STREAM_DUPLICATES` | `CHATIM_EVT` / `evt` / `live` / `1` / `168h` / `2m` | Cấu hình stream `CHATIM_EVT` và RePublish sang `live.*` |
| `RECOVERY_INTERVAL`, `RECOVERY_REMOVE_AFTER`, `RECOVERY_STALE_AFTER` | `30s` / `15s` / `5s` | Sweeper khôi phục (5.4, D38) |
| `SLOT_TICK`, `SLOT_HEARTBEAT_TTL`, `SLOT_LEASE_TTL`, `SLOT_HOOK_TIMEOUT` | `1s` / `5s` / `10s` / `Tick/2` | Nhịp slot manager; `HookTimeout` suy ra từ `SLOT_TICK` nếu không đặt riêng (D26) |

## Cổng

Compose publish ra host chỉ gRPC của core, và chỉ trên loopback — vì caller hiện được tin cậy qua metadata tới khi có mTLS (M5):

| Cổng | Dịch vụ | Ghi chú |
|---|---|---|
| `127.0.0.1:9001` | `core-1` gRPC (`CORE1_GRPC_PORT`) | |
| `127.0.0.1:9002` | `core-2` gRPC (`CORE2_GRPC_PORT`) | |
| — | admin (`/healthz`, `/readyz`, pprof) mỗi core, cổng `9090` | Chỉ trong mạng compose, không publish ra host |

## Tài liệu

- [Thiết kế Phase 1: core + gateway](docs/designs/260930-chat-core-gateway-design.md)
- [Nghiên cứu kiến trúc chat mã nguồn mở (tinode, teamgram, chatto, gws)](docs/research/260930-opensource-chat-architecture-research.md)
- [Kết quả PoC](docs/poc/README.md)
- [Roadmap](docs/roadmap.md)
- [Plan M0–M1](docs/plans/2026-09-30-phase1-foundation-and-poc.md)
