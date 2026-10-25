# chatim

Hạ tầng chat dùng chung (CPaaS nội bộ) cho nhiều sản phẩm: quản lý room, tin nhắn, tương tác realtime hiệu năng cao; lấy lịch sử cực nhanh ở bất kỳ vị trí nào; phát event mạnh tới các app khác kết nối vào. Multi-tenant về mặt logic.

> Trạng thái: Phase 1 (M0 → M4: `core`, Mention App, `websocket`, `gateway`) đã xong và merge `main` (`dev-done`, PR #19, 2026-10-25). Tiếp theo Phase 2: M6 `identity` → M7 media → M8 notification + push → M9 tìm người/nhóm → go-live `v1.0.0` (D207); PoC prod-like và M5 chạy song song khi có hạ tầng. Chi tiết: [roadmap](docs/roadmap.md), kết quả đo: [docs/poc/README.md](docs/poc/README.md), bản đồ code: [INDEXES.csv](INDEXES.csv).

## Kiến trúc

Monorepo Go, mỗi app một container. Thiết kế: [architecture.md](docs/architecture.md).

| App | Vai trò | Vận hành |
|---|---|---|
| `core` | Room, member, tin nhắn, tương tác; cấp seq; lưu MongoDB; mỗi lần ghi bật cờ `_state` trên doc, publish lên NATS JetStream sau ack, sweeper phát bù cờ còn sót | [docs/apps/core.md](docs/apps/core.md) |
| `mention` (Mention App) | Nhúng trong core (`modules/mention`): chỉ mục mention, `ListMentions`, nhóm được tag, gợi ý `@nhóm`, thông báo `WsPublish` | [docs/apps/core.md](docs/apps/core.md#mention-app) |
| `websocket` | WebSocket (gws): 12 lệnh ghi realtime, đẩy event, đang gõ, online, giữ kết nối khi token hết hạn; SDK client ở repo khác, theo [module 09 §11](docs/modules/09-feature-realtime-delivery-and-ephemeral.md) | [docs/apps/websocket.md](docs/apps/websocket.md) |
| `gateway` | HTTPS (Connect): 22 lệnh đọc và quản lý room, JWT, gọi core bất kỳ (round robin), giới hạn theo user | [docs/apps/gateway.md](docs/apps/gateway.md) |
| `identity`, media, `notification` + push, search | Phase 2 (D207) | — |

Hạ tầng: MongoDB (replica set), Redis (state, dedupe, presence), NATS JetStream.

## Cấu trúc

```
apps/core/               # main.go (gọi app.Main); itest/: test tích hợp cả core (cần infra thật)
  internal/app/          # wiring, vòng đời, lệnh serve/probe/resync/recount
  internal/config/       # đọc + kiểm config lúc boot
  internal/api/          # grpcsrv: transport mỏng (caller, proto ↔ domain, một lần gọi use case)
  internal/message/      # Editor, Reader; actor, flush, view (gửi, sửa, xoá, ẩn, đọc tin)
  internal/interaction/  # Service, Reader; pinproj (reaction, bookmark, ghim, trả lời, thread)
  internal/member/       # Service, Reader; ownership, memberwatch (thành viên, vị trí đọc, cài đặt)
  internal/room/         # Service; roomlist, roomcache (tạo room, DM, danh sách hội thoại)
  internal/dedupe/       # cid và request_id trên Redis dedupe
  internal/model/        # domain, pbconv, access (kiểu, chuyển proto, permission hook)
  internal/event/        # publish, stateclear, projection, sweep, timer, eventkit, resync (event theo cờ `_state`)
  internal/store/        # port + memstore, mongostore, storetest
  internal/platform/     # slot, metrics, redisguard, testlog (hạ tầng cụm)
apps/websocket/          # WebSocket (gws): kết nối, token, 12 lệnh ghi realtime, đẩy event, đang gõ, online; internal/{app,config,session,command,feed,interest,frame,typing,presence}
  internal/typing/       # đang gõ: kiểm thành viên, đẩy cho người đang mở room, chuyển giữa node qua core NATS eph.{tenant}.{room}.typing
  internal/presence/     # online: đóng dấu khoá chatim:presence:{tenant}:{user} (EX 15s) trên Redis presence, trả danh sách hỏi mỗi heartbeat, bộ đệm 5s chỉ cho online
apps/gateway/            # HTTPS (Connect): 22 lệnh đọc và quản lý room; internal/{app,config,api,limit}
modules/mention/         # Mention App: module nhúng trong core (D182), không có main.go
  mentionapp/            # bề mặt công khai core import: Config, New, Prepare, Run, Close, Register, Watch, Probes; cổng CoreReader
  internal/              # config, domain, store, plan, recipients, notify, consumer, groups, roomgroups, grpcsrv
internal/wstest/         # client websocket nhỏ chỉ cho test của apps/websocket và e2e của corecli (không phải SDK)
pkg/                     # dùng chung: keys, ids, lru, slotmap, apperr, envconfig, resilience, admin, grpcserver, grpcclient, backoff, jsconsume, mongobound, callerid, pb, corecall, coreroute, jwtverify, wspublish
proto/chatim/v1/         # định nghĩa protobuf (buf) → pkg/pb
tools/                   # corecli, internal/route, internal/devtoken; poc/: corebench, mongobench, postgresbench, natsbench, wsbench, wsload (bài tải websocket R4b)
scripts/                 # e2e.sh, các script chờ hạ tầng sẵn sàng, bench-cell.sh / bench-sample.sh (đo corebench), ws-nats-drill.sh (diễn tập NATS), wsload-kill.sh (tắt một node websocket dưới tải)
deploy/docker/           # 1 Dockerfile, chọn chương trình bằng ARG TARGET
deploy/compose/          # hạ tầng dev
docs/                    # nghiên cứu, thiết kế, plan, kết quả PoC
```

## Chạy hạ tầng dev

Chỉ cần Docker; Go chạy trong container `golang:1.26.10` qua `make`.

    cp .env.example .env        # đổi MONGO_ROOT_PASSWORD, REDIS_PASSWORD, REDIS_DEDUPE_PASSWORD, REDIS_PRESENCE_PASSWORD (chữ, số, - hoặc _)
    make infra-up               # mongo rs0 :27117, redis state :6380, redis dedupe :6381, redis presence :6382, nats :4223 (monitor :8223)
    make redis-cli ARGS="info memory"                      # redis-cli trong container, mật khẩu lấy từ secret
    make redis-cli INSTANCE=dedupe ARGS="dbsize"           # INSTANCE=state (mặc định), dedupe hoặc presence
    make redis-cli INSTANCE=presence ARGS="ttl chatim:presence:e2e:alice"
    make test                   # go test -race ./... trong container
    make go ARGS="vet ./..."    # lệnh go bất kỳ
    make infra-down             # dừng; make infra-reset để xoá cả dữ liệu

Image chạy cho một chương trình Go bất kỳ: `make image TARGET=tools/poc/natsbench` (distroless, không cần Go trên máy chạy).

Công cụ PoC nằm ở `tools/poc/`, chạy bằng `make poc TOOL=<tên> ARGS="…"` — cách chạy và kết quả: [docs/poc/README.md](docs/poc/README.md).

Đo corebench: `./scripts/bench-cell.sh NAME RATE reset [pprof]`, kết quả vào `bin/bench/NAME/` ([docs/poc/README.md](docs/poc/README.md)).

## Lệnh hay dùng

    make proto                  # sinh code từ proto/chatim/v1 (buf generate, ghi vào pkg/pb)
    make buf-lint               # buf lint + buf format -d --exit-code
    make vet; make fmt-check; make lint; make vuln
    make itest                  # test với Mongo/Redis/NATS thật (cần infra-up)
    make core-up                # sinh khoá dev nếu chưa có, build và chạy core-1, core-2, websocket-1, websocket-2, gateway-1, chờ healthy
    make core-down              # dừng và xoá 5 container trên
    make -s ws-token ARGS="-tenant e2e -user alice"   # token dev cho websocket và gateway
    make ws-logs; make ws-logs NODE=2; make gw-logs
    make alerts-check           # promtool kiểm deploy/prometheus/alerts.yml
    make e2e                    # build corecli, chạy scripts/e2e.sh (12 phase, xem docs/e2e.md)
    make poc TOOL=corebench ARGS="-rate 2000 -duration 60s -watch 20"   # tải vào cụm core, cần core-up

Gọi thử gateway:

    curl -H "Authorization: Bearer $(make -s ws-token ARGS='-tenant e2e -user alice')" -H 'Content-Type: application/json' -d '{}' http://127.0.0.1:8082/chatim.v1.CoreService/ListMyRooms

Lệnh vận hành core (`/app resync`, `/app recount`, `/app indexes`) và `corecli`: [docs/apps/core.md](docs/apps/core.md#lệnh-vận-hành). Khoá dev, đổi khoá, `wsload`, diễn tập NATS: [docs/apps/websocket.md](docs/apps/websocket.md).

## Cổng

Với app, compose publish ra host chỉ gRPC của core, WebSocket của websocket-1 và websocket-2 và HTTP của gateway-1, và chỉ trên loopback — vì caller hiện được tin cậy qua metadata tới khi có mTLS (M5). Hạ tầng dev publish cổng riêng (`.env`):

| Cổng | Dịch vụ | Ghi chú |
|---|---|---|
| `6380` | `redis` state (`REDIS_PORT`) | AOF everysec, `noeviction`, bắt buộc AUTH (`REDIS_PASSWORD`) |
| `6381` | `redis-dedupe` (`REDIS_DEDUPE_PORT`) | Không lưu đĩa, `maxmemory` `REDIS_DEDUPE_MAXMEMORY` (512mb), `allkeys-lru`, bắt buộc AUTH (`REDIS_DEDUPE_PASSWORD`) |
| `6382` | `redis-presence` (`REDIS_PRESENCE_PORT`) | Key online của websocket, không lưu đĩa, `noeviction`, bắt buộc AUTH (`REDIS_PRESENCE_PASSWORD`) |
| `127.0.0.1:9001` | `core-1` gRPC (`CORE1_GRPC_PORT`) | |
| `127.0.0.1:9002` | `core-2` gRPC (`CORE2_GRPC_PORT`) | |
| `127.0.0.1:8081` | `websocket-1` WebSocket `/ws` (`WS1_PORT`) | Cần token dev (`make -s ws-token`) |
| `127.0.0.1:8083` | `websocket-2` WebSocket `/ws` (`WS2_PORT`) | Node thứ hai để thử đang gõ và online giữa hai node; cùng token dev |
| `127.0.0.1:8082` | `gateway-1` HTTP Connect (`GW1_PORT`) | Cần token dev (`Authorization: Bearer`) |
| — | admin (`/healthz`, `/readyz`, `/metrics`, pprof) mỗi core, cổng `9090`; admin websocket-1, websocket-2 và gateway-1 (`/livez`, `/readyz`, `/metrics`) cổng `9090` | Chỉ trong mạng compose, không publish ra host |

## Tài liệu

- [Kiến trúc hệ thống (nguồn sự thật)](docs/architecture.md)
- [11 Module kỹ thuật tính năng (How-It-Works)](docs/modules/README.md)
- [Nhật ký quyết định kiến trúc D1–D207](docs/decision-log.md)
- [Danh mục event (core và Mention App phát gì, ai nhận gì)](docs/events.md)
- [Roadmap](docs/roadmap.md)
- Vận hành từng app (biến môi trường, cổng, metric, lệnh): [core](docs/apps/core.md), [websocket](docs/apps/websocket.md), [gateway](docs/apps/gateway.md)
- [Kiểm thử e2e (12 phase)](docs/e2e.md)
- [Quy trình git](docs/git-workflow.md)
- [Kết quả PoC](docs/poc/README.md)
- [Báo cáo nghiên cứu cơ chế hệ thống](docs/research/261005-system-mechanisms-synthesis-report.md)
- [Nghiên cứu kiến trúc chat mã nguồn mở (tinode, teamgram, chatto, gws)](docs/research/260930-opensource-chat-architecture-research.md)
