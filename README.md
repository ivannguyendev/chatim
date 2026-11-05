# chatim

Hạ tầng chat dùng chung (CPaaS nội bộ) cho nhiều sản phẩm: quản lý room, tin nhắn, tương tác realtime hiệu năng cao; lấy lịch sử cực nhanh ở bất kỳ vị trí nào; phát event mạnh tới các app khác kết nối vào. Multi-tenant về mặt logic.

> Trạng thái từng milestone và việc tiếp theo: [roadmap](docs/roadmap.md). Kết quả đo: [docs/poc/README.md](docs/poc/README.md). Bản đồ code: [INDEXES.csv](INDEXES.csv). Tài liệu dự án viết bằng tiếng Việt.

## Kiến trúc

Monorepo Go, mỗi app một container. Thiết kế: [architecture.md](docs/architecture.md).

| App | Vai trò | Vận hành |
|---|---|---|
| `chat` | Room, member, tin nhắn, tương tác; cấp seq; lưu MongoDB; mỗi lần ghi bật cờ `_state` trên doc, publish lên NATS JetStream sau ack, sweeper phát bù cờ còn sót | [docs/apps/chat.md](docs/apps/chat.md) |
| `mention` (Mention App) | Nhúng trong chat (`modules/mention`): chỉ mục mention, `ListMentions`, nhóm của user, nhóm được tag, dữ liệu gợi ý `@nhóm`, thông báo dạng gói `WsPublish` trên `CHATIM_WS`. Giữ nhúng (D177) cho tới khi có team, SLA, nhịp release hay nhu cầu scale riêng có số đo, hoặc database riêng + CDC; khi tách thì app tự giữ dữ liệu và chỉ gọi chat qua API, không đọc bảng của chat | [docs/apps/chat.md](docs/apps/chat.md#mention-app) |
| `websocket` | WebSocket (gws): các lệnh ghi realtime (`chatrpc.Realtime()`), đẩy event và thông báo, đang gõ, online, giới hạn đăng nhập, giữ kết nối khi token hết hạn. Repo chỉ có server (D206): SDK client ở repo khác, theo [module 09 §11](docs/modules/09-feature-realtime-delivery-and-ephemeral.md) | [docs/apps/websocket.md](docs/apps/websocket.md) |
| `gateway` | HTTPS (Connect): các lệnh đọc và quản lý room (`chatrpc.Request()`), JWT, gọi node chat bất kỳ (round robin), giới hạn theo user | [docs/apps/gateway.md](docs/apps/gateway.md) |
| `identity` | Cầu danh tính, không phải IAM (D239; tên cũ `iam`, D243): chat không giữ mật khẩu, đăng nhập, phiên hay role. Giữ issuer tin cậy theo tenant cùng khoá công khai của họ (EdDSA/ES256/RS256, thêm bằng CLI); đổi JWT do backend tenant ký lấy token chat Ed25519 5 phút ở `POST /v1/token` (RFC 8693, D240–D241); giữ bản sao user ghi ngay lúc đổi token từ `name`/`picture` (D242), cây nhóm, thu hồi theo tenant, khoá ký và JWKS. Danh bạ (`GetUsers`, `GetMyProfile`) phục vụ từ bản sao trong RAM mỗi replica, nạp theo trang dưới lease `slots` và theo kịp bằng change stream MongoDB (D245–D246, D252); không có feed danh bạ cho app khác (D257). Lưu ở database MongoDB riêng `chatim_identity`, user `identity`, trên replica set của chat, không Postgres (D244, D253). Không đọc dữ liệu chat, chat cũng không đọc bảng của nó | [docs/apps/identity.md](docs/apps/identity.md) |
| media, `notification` + push, search | Phase 2 (D207) | — |

Hạ tầng: MongoDB (replica set; `identity` dùng database riêng `chatim_identity`), Redis (state, dedupe, presence), NATS JetStream.

Còn mở (chi tiết ở [roadmap](docs/roadmap.md)): quyền NATS riêng cho websocket (M5, chặn go-live); ngày có hạ tầng prod-like; ai chọn người nhận push cho tin thường (D104).

## Cấu trúc

```
apps/{chat,websocket,gateway,identity}/ # chương trình chạy được: main.go + internal/
modules/mention/                 # Mention App, module nhúng trong chat (D182)
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
    make app-up                # build và chạy identity-mongo-init, identity-1, identity-2, chat-1, chat-2, websocket-1, websocket-2, gateway-1; app-down để dừng
    make e2e                    # các phase ở docs/e2e.md

Lệnh khác: `make vet`, `fmt-check`, `lint`, `vuln`, `itest`, `proto`, `buf-lint`, `alerts-check`, `redis-cli [INSTANCE=dedupe|presence] ARGS=…`, `image TARGET=…`, `poc TOOL=…` ([docs/poc/README.md](docs/poc/README.md)). Token dev và gọi thử gateway:

    curl -H "Authorization: Bearer $(make -s ws-token ARGS='-tenant e2e -user alice')" -H 'Content-Type: application/json' -d '{}' http://127.0.0.1:8082/chatim.v1.ChatService/ListMyRooms

## Cổng dev

App chỉ publish trên loopback (caller còn được tin qua metadata tới khi có mTLS, M5); cổng admin `9090` của mọi app không publish ra host.

| Cổng | Dịch vụ |
|---|---|
| `27117`, `6380`, `6381`, `6382`, `4223` | Mongo (replica set `rs0`), Redis state, Redis dedupe, Redis presence, NATS |
| `8223` | NATS monitor |
| `127.0.0.1:8081` | WebSocket `/ws` `websocket-1` |
| `127.0.0.1:8082` | HTTP Connect `gateway-1` |
| `127.0.0.1:8084` | HTTP `identity-1` (đổi token `POST /v1/token`, JWKS, Connect `DirectoryService`) |

## Tài liệu

- [Kiến trúc (nguồn sự thật)](docs/architecture.md) · [Module tính năng](docs/modules/README.md) · [Decision log](docs/decision-log.md) · [Danh mục event](docs/events.md)
- Vận hành từng app (biến môi trường, cổng, metric, lệnh): [chat](docs/apps/chat.md) · [websocket](docs/apps/websocket.md) · [gateway](docs/apps/gateway.md) · [identity](docs/apps/identity.md)
- [Roadmap](docs/roadmap.md) · [Quy trình git](docs/git-workflow.md) · [Kiểm thử e2e](docs/e2e.md) · [Kết quả PoC](docs/poc/README.md)
- Nghiên cứu: [cơ chế hệ thống](docs/research/261005-system-mechanisms-synthesis-report.md) · [kiến trúc chat mã nguồn mở](docs/research/260930-opensource-chat-architecture-research.md)
