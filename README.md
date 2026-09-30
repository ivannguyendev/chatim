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

## Tài liệu

- [Thiết kế Phase 1: core + gateway](docs/designs/260930-chat-core-gateway-design.md)
- [Nghiên cứu kiến trúc chat mã nguồn mở (tinode, teamgram, chatto, gws)](docs/research/260930-opensource-chat-architecture-research.md)
- [Kết quả PoC](docs/poc/README.md)
- [Plan M0–M1](docs/plans/2026-09-30-phase1-foundation-and-poc.md)
