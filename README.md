# chatim

Hạ tầng chat dùng chung (CPaaS nội bộ) cho nhiều sản phẩm: quản lý room, tin nhắn, tương tác realtime hiệu năng cao; lấy lịch sử cực nhanh ở bất kỳ vị trí nào; phát event mạnh tới các app khác kết nối vào. Multi-tenant về mặt logic.

> Trạng thái: Phase 1 (M0 → M4: `core`, Mention App, `websocket`, `gateway`) đã xong và merge `main` (`dev-done`, PR #19, 2026-10-25). Phase 2 đang làm: M6 `identity` (đóng ở PA + PM trên `feat/m6`, chờ PR: đổi token, store MongoDB; PB server API theo lô dời tới khi tenant đầu tiên tích hợp, D260) → M7 media → M8 notification + push → M9 tìm người/nhóm → go-live `v1.0.0` (D207); PoC prod-like và M5 chạy song song khi có hạ tầng. Chi tiết: [roadmap](docs/roadmap.md), kết quả đo: [docs/poc/README.md](docs/poc/README.md), bản đồ code: [INDEXES.csv](INDEXES.csv).

## Kiến trúc

Monorepo Go, mỗi app một container. Thiết kế: [architecture.md](docs/architecture.md).

| App | Vai trò | Vận hành |
|---|---|---|
| `core` | Room, member, tin nhắn, tương tác; cấp seq; lưu MongoDB; mỗi lần ghi bật cờ `_state` trên doc, publish lên NATS JetStream sau ack, sweeper phát bù cờ còn sót | [docs/apps/chat.md](docs/apps/chat.md) |
| `mention` (Mention App) | Nhúng trong core (`modules/mention`): chỉ mục mention, `ListMentions`, nhóm được tag, gợi ý `@nhóm`, thông báo `WsPublish` | [docs/apps/chat.md](docs/apps/chat.md#mention-app) |
| `websocket` | WebSocket (gws): 12 lệnh ghi realtime, đẩy event, đang gõ, online, giữ kết nối khi token hết hạn; SDK client ở repo khác, theo [module 09 §11](docs/modules/09-feature-realtime-delivery-and-ephemeral.md) | [docs/apps/websocket.md](docs/apps/websocket.md) |
| `gateway` | HTTPS (Connect): 22 lệnh đọc và quản lý room, JWT, gọi core bất kỳ (round robin), giới hạn theo user | [docs/apps/gateway.md](docs/apps/gateway.md) |
| `identity` | Cầu danh tính (M6, tên cũ `iam`): chat không giữ đăng nhập (owner 2026-11-02); đổi JWT do backend tenant ký (EdDSA/ES256/RS256, issuer tin cậy theo tenant) lấy token chat Ed25519 5 phút, bản sao user và nhóm (danh bạ), thu hồi theo tenant; lưu ở database MongoDB riêng `chatim_identity` (user `identity`, phase PM) | [docs/apps/identity.md](docs/apps/identity.md) |
| media, `notification` + push, search | Phase 2 (D207) | — |

Hạ tầng: MongoDB (replica set; `identity` dùng database riêng `chatim_identity`), Redis (state, dedupe, presence), NATS JetStream.

## Cấu trúc

```
apps/{core,websocket,gateway,identity}/ # chương trình chạy được: main.go + internal/
modules/mention/                 # Mention App, module nhúng trong core (D182)
pkg/                             # code dùng chung, không có nghiệp vụ; wstest: client websocket chỉ cho test, e2e, wsload
proto/chatim/{v1,identity/v1}/   # protobuf (buf) → pkg/pb
tools/                           # chatcli, devtoken; poc/: chatbench, wsload, …
scripts/                         # e2e.sh, bench-cell.sh, ws-nats-drill.sh, wsload-kill.sh
deploy/                          # Dockerfile (ARG TARGET), compose dev, luật alert Prometheus
docs/                            # thiết kế, module, vận hành từng app, plan, kết quả PoC
```

Bản đồ từng package: [INDEXES.csv](INDEXES.csv); hướng phụ thuộc: [architecture.md §6.2](docs/architecture.md).

## Bắt đầu

Chỉ cần Docker; Go chạy trong container `golang:1.26.10` qua `make`.

    cp .env.example .env        # đặt các mật khẩu (chữ, số, - hoặc _); IDENTITY_KEY_ENCRYPTION_KEY: openssl rand -base64 32; IDENTITY_NODE_SECRET: openssl rand -hex 32
    make infra-up               # mongo :27117, redis :6380/:6381/:6382, nats :4223; infra-down / infra-reset
    make test                   # unit test trong container
    make core-up                # build và chạy identity-mongo-init, identity-1, identity-2, core-1, core-2, websocket-1, websocket-2, gateway-1; core-down để dừng
    make e2e                    # 14 phase, xem docs/e2e.md

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
| `127.0.0.1:8084`, `127.0.0.1:8085` | HTTP `identity-1`, `identity-2` (đổi token `POST /v1/token`, JWKS, Connect `DirectoryService`) |

## Tài liệu

- [Kiến trúc (nguồn sự thật)](docs/architecture.md) · [11 module tính năng](docs/modules/README.md) · [Decision log D1–D218](docs/decision-log.md) · [Danh mục event](docs/events.md)
- Vận hành từng app (biến môi trường, cổng, metric, lệnh): [core](docs/apps/chat.md) · [websocket](docs/apps/websocket.md) · [gateway](docs/apps/gateway.md) · [identity](docs/apps/identity.md)
- [Roadmap](docs/roadmap.md) · [Quy trình git](docs/git-workflow.md) · [Kiểm thử e2e](docs/e2e.md) · [Kết quả PoC](docs/poc/README.md)
- Nghiên cứu: [cơ chế hệ thống](docs/research/261005-system-mechanisms-synthesis-report.md) · [kiến trúc chat mã nguồn mở](docs/research/260930-opensource-chat-architecture-research.md)
