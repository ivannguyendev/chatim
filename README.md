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
| `iam` | IAM tổng quát kiểu Keycloak (M6): account do admin cấp, đăng nhập mật khẩu, token Ed25519, refresh gắn DPoP, phiên; Postgres riêng | [docs/apps/iam.md](docs/apps/iam.md) |
| media, `notification` + push, search | Phase 2 (D207) | — |

Hạ tầng: MongoDB (replica set), Redis (state, dedupe, presence), NATS JetStream; Postgres cho `iam`.

## Cấu trúc

```
apps/{core,websocket,gateway,iam}/   # chương trình chạy được: main.go + internal/
modules/mention/                 # Mention App, module nhúng trong core (D182)
pkg/                             # code dùng chung, không có nghiệp vụ; wstest: client websocket chỉ cho test, e2e, wsload
proto/chatim/{v1,iam/v1}/        # protobuf (buf) → pkg/pb
tools/                           # corecli, devtoken; poc/: corebench, wsload, …
scripts/                         # e2e.sh, bench-cell.sh, ws-nats-drill.sh, wsload-kill.sh
deploy/                          # Dockerfile (ARG TARGET), compose dev, luật alert Prometheus
docs/                            # thiết kế, module, vận hành từng app, plan, kết quả PoC
```

Bản đồ từng package: [INDEXES.csv](INDEXES.csv); hướng phụ thuộc: [architecture.md §6.2](docs/architecture.md).

## Bắt đầu

Chỉ cần Docker; Go chạy trong container `golang:1.26.10` qua `make`.

    cp .env.example .env        # đặt các mật khẩu (chữ, số, - hoặc _); IAM_KEY_ENCRYPTION_KEY: openssl rand -base64 32
    make infra-up               # mongo :27117, redis :6380/:6381/:6382, nats :4223; infra-down / infra-reset
    make test                   # unit test trong container
    make core-up                # build và chạy postgres, iam-1, core-1, core-2, websocket-1, websocket-2, gateway-1; core-down để dừng
    make e2e                    # 13 phase, xem docs/e2e.md

Lệnh khác: `make vet`, `fmt-check`, `lint`, `vuln`, `itest`, `proto`, `buf-lint`, `alerts-check`, `redis-cli [INSTANCE=dedupe|presence] ARGS=…`, `image TARGET=…`, `poc TOOL=…` ([docs/poc/README.md](docs/poc/README.md)). Token dev và gọi thử gateway:

    curl -H "Authorization: Bearer $(make -s ws-token ARGS='-tenant e2e -user alice')" -H 'Content-Type: application/json' -d '{}' http://127.0.0.1:8082/chatim.v1.CoreService/ListMyRooms

## Cổng dev

App chỉ publish trên loopback (caller còn được tin qua metadata tới khi có mTLS, M5); cổng admin `9090` của mọi app không publish ra host.

| Cổng | Dịch vụ |
|---|---|
| `27117`, `6380`, `6381`, `6382`, `4223` | Mongo, Redis state, Redis dedupe, Redis presence, NATS |
| `127.0.0.1:9001`, `127.0.0.1:9002` | gRPC `core-1`, `core-2` |
| `127.0.0.1:8081`, `127.0.0.1:8083` | WebSocket `/ws` `websocket-1`, `websocket-2` |
| `127.0.0.1:8082` | HTTP Connect `gateway-1` |
| `127.0.0.1:8084` | HTTP `iam-1` (Connect `AuthService`, `/realms/{tenant}/protocol/openid-connect/token`) |
| `5433` | Postgres (`chatim_poc`, `chatim_iam`) |

## Tài liệu

- [Kiến trúc (nguồn sự thật)](docs/architecture.md) · [11 module tính năng](docs/modules/README.md) · [Decision log D1–D207](docs/decision-log.md) · [Danh mục event](docs/events.md)
- Vận hành từng app (biến môi trường, cổng, metric, lệnh): [core](docs/apps/core.md) · [websocket](docs/apps/websocket.md) · [gateway](docs/apps/gateway.md) · [iam](docs/apps/iam.md)
- [Roadmap](docs/roadmap.md) · [Quy trình git](docs/git-workflow.md) · [Kiểm thử e2e](docs/e2e.md) · [Kết quả PoC](docs/poc/README.md)
- Nghiên cứu: [cơ chế hệ thống](docs/research/261005-system-mechanisms-synthesis-report.md) · [kiến trúc chat mã nguồn mở](docs/research/260930-opensource-chat-architecture-research.md)
