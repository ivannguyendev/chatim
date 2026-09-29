# chatim

Hạ tầng chat dùng chung (CPaaS nội bộ) cho nhiều sản phẩm: quản lý room, tin nhắn, tương tác realtime hiệu năng cao; lấy lịch sử cực nhanh ở bất kỳ vị trí nào; phát event mạnh tới các app khác kết nối vào. Multi-tenant về mặt logic.

> Trạng thái: **thiết kế Phase 1 đã chốt, chưa implement**. Bước tiếp theo: PoC các rủi ro R1–R5 trong tài liệu thiết kế.

## Kiến trúc

Monorepo Go, mỗi app một container.

| App | Vai trò | Phase |
|---|---|---|
| `core` | Room, member, message, tương tác; cấp seq; lưu MongoDB; phát event lên NATS JetStream | 1 |
| `gateway` | WebSocket (gws) cho client SDK; nhận event từ NATS và đẩy realtime | 1 |
| `api`, `events`, `push`, `auth`, `migrator` | Public API, event stream cho app ngoài, push notification, xác thực, migrate/dual-write từ hệ thống cũ | Sau |

Hạ tầng: MongoDB (replica set), Redis, NATS JetStream.

## Cấu trúc dự kiến

```
apps/{core,gateway}/     # mỗi app: main.go + internal/
pkg/                     # thư viện dùng chung (pb, slotmap, mongox, redisx, natsx, config, logx, telemetry)
proto/chatim/v1/         # định nghĩa protobuf (buf)
deploy/docker/           # 1 Dockerfile, build theo ARG APP
deploy/compose/          # môi trường dev
docs/                    # nghiên cứu, thiết kế
```

## Tài liệu

- [Thiết kế Phase 1: core + gateway](docs/designs/260930-chat-core-gateway-design.md)
- [Nghiên cứu kiến trúc chat mã nguồn mở (tinode, teamgram, chatto, gws)](docs/research/260930-opensource-chat-architecture-research.md)
