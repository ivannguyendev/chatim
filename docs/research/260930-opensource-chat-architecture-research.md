# Nghiên cứu kiến trúc chat mã nguồn mở

> Ngày: 2026-09-30 · Phạm vi: tinode/chat, teamgram, chattocorp/chatto, lxzan/gws
> Mục đích: rút ra mô hình dữ liệu và kiến trúc cho `chatim` (chat core hiệu năng cao, fanout mạnh, lấy tin cũ nhanh).
> Thiết kế đã chốt dựa trên nghiên cứu này: [../designs/260930-chat-core-gateway-design.md](../designs/260930-chat-core-gateway-design.md)

Cách thu thập: đọc source qua GitHub (WebFetch/REST API, không clone). Một số trích dẫn là tóm tắt của công cụ fetch, không phải nguyên văn từng byte.

## Kết luận nhanh

| | tinode/chat | teamgram | chattocorp/chatto | lxzan/gws |
|---|---|---|---|---|
| Loại | Chat server Go | Server tương thích Telegram (Go, MTProto) | Team chat server Go + Svelte | Thư viện WebSocket Go |
| Storage | MySQL/PG/Mongo, `UNIQUE(topic, seqid)` | MySQL, **1 row cho mỗi người nhận** | NATS JetStream (event sourcing), index trong RAM | — |
| Cấp seq | SeqId per topic trong goroutine của topic | Redis `INCR` (pts, message box) per user | Stream seq của JetStream + OCC | — |
| Fanout | Topic goroutine → session; 1 broadcast / node | Kafka `Inbox-T` → copy từng member → `Sync-T` | JetStream republish `live.evt.>` → mọi node tự lọc | Broadcaster encode 1 lần |
| Cluster | Hash ring tĩnh, failover "experimental" | go-zero + etcd, nhưng ship 1 container | Stateless + NATS cluster, không sticky | 1 goroutine / connection |
| Tích hợp ngoài | gRPC plugin **đồng bộ** | Không có | ConnectRPC, bot, webhook, MCP | — |
| Lấy tin cũ | Index seek nhanh | Thiếu index → chậm | Đọc theo seq chính xác; projection trong RAM | — |

## 1. tinode/chat

**Mô hình dữ liệu** (`server/store/types/types.go`)
- Entity: User, Topic (`me`, `fnd`, `p2p`, `grp`, channel = group có reader), Subscription, Message.
- `SeqId` per topic, bắt đầu từ 1, tăng 1 — gán trong goroutine của topic (`t.lastID + 1`).
- `DelId`: counter riêng cho thao tác xoá; xoá lưu theo **khoảng** `(low, hi)` trong bảng `dellog`.
- Subscription giữ `RecvSeqId`, `ReadSeqId` → unread = `topic.SeqId − ReadSeqId`, không ghi riêng cho từng người mỗi khi có tin.

**Storage**
- `messages`: `UNIQUE INDEX (topic, seqid)` → đọc khoảng `[since, before)` là 1 index seek.
- Query sắp `DESC`: muốn lấy trang cũ nhất phải gửi cửa sổ có giới hạn (`since=1&before=21`).
- Mỗi tin = 3 lần ghi DB (update hot row `topics.seqid`, insert message, update subscription). `seqid` là `INT` 32-bit. Không cache, không shard DB.

**Realtime**
- 1 goroutine cho hub, 1 goroutine cho mỗi topic; pub flow: kiểm quyền → **lưu DB đồng bộ** → gọi plugin gRPC **đồng bộ** → broadcast → push.
- Hàng đợi gửi mỗi session 128 message; đầy thì bỏ session.
- Group mặc định tối đa 128 member; channel reader không giới hạn, không gửi presence.

**Cluster**
- Consistent hash ring (CRC32), mỗi topic có 1 master node; node khác tạo proxy topic; master gửi 1 broadcast cho mỗi node proxy.
- Danh sách node tĩnh trong config; failover kiểu Raft "experimental"; khi rehash, message đang chờ bị bỏ.

**Tích hợp**: gRPC `Plugin` service (`FireHose` chặn/sửa message, event Account/Topic/Subscription/Message), chatbot qua `MessageLoop`, push adapter (FCM, TNPG). Không có webhook, không có message bus.

**Đánh giá**: mô hình seq + read marker + delete-range rất tốt. Điểm yếu: đường ghi đồng bộ trong 1 goroutine, plugin chậm làm treo topic, không có event stream bền vững để replay.

## 2. teamgram (teamgram-server + teamgram/proto)

**Tách service** (go-zero `zrpc` + etcd, Kafka topic `Inbox-T`, `Sync-T`)
- `gnetway` (MTProto gateway) → `session` → `bff` (~28 module API) → `msg` (+ inbox consumer) → `sync` → đẩy về gateway.
- Service khác: `biz`, `idgen`, `status` (session online trong Redis), `authsession`, `dfs` (MinIO), `media`.

**Luồng gửi tin**: idempotency `random_id` trong Redis → `idgen` cấp id → ghi outbox của người gửi → Kafka `Inbox-T` cho từng người nhận → inbox consumer ghi **bản sao** cho người nhận + `user_pts_updates` → Kafka `Sync-T` → sync đẩy tới session của người nhận.

**Mô hình dữ liệu**
- `messages`: 1 row mỗi người sở hữu, `UNIQUE(user_id, user_message_box_id)`; sharding `user_id % N`, mặc định N = 1.
- pts/seq/qts: Redis `INCR` → mất Redis là mất counter, làm hỏng sync.
- Gap recovery: `updates.getDifference` gộp các `user_pts_updates` có pts lớn hơn của client (tối đa 5000).

**Lấy lịch sử**: query theo `user_message_box_id < ?` nhưng index không chứa cột này → dialog lớn phải quét và sort. Không cache lịch sử.

**Fanout**: group thường = write-fanout (mỗi member 1 bản sao). Channel/megagroup (read-fanout) bị khoá ở bản community.

**teamgram/proto**: TL schema của Telegram → sinh `.proto` → Go types + gRPC service. Bản `v2/` chuyển sang CloudWeGo Kitex.

**Deploy**: tất cả binary chạy trong **1 container** (`runall-docker.sh`), dù code tách `cmd/` từng module.

**Đánh giá**: phân tầng gateway/session/logic/async và cơ chế pts + getDifference đáng học. Tránh: write-fanout per user, counter chỉ nằm trong Redis, thiếu index lịch sử.

## 3. chattocorp/chatto

- Go 1.26, AGPL-3.0 (module `pkg/*` Apache-2.0), còn beta (`v0.5.0-beta.10`). Monorepo `go.work` + pnpm + turbo.
- **Storage**: NATS JetStream là database duy nhất. Stream `EVT` là nguồn sự thật; KV + object store cho state/snapshot. Mọi publish dùng OCC (khai báo sequence cuối đã thấy).
- **Subject**: `evt.{aggregateType}.{aggregateID}.{eventType}`, ví dụ `evt.room.{roomID}.*`; stream RePublish sang `live.evt.>` để mọi process nhận realtime.
- **Index**: projection trong RAM, mỗi process tự dựng lại từ `EVT` (có snapshot). Đọc nội dung tin theo đúng sequence.
- **Realtime**: WebSocket `/api/realtime`, frame protobuf nhị phân: `snapshot | event | caughtUp | heartbeat | close`; heartbeat ~15s; resume cursor 15 phút.
- **Tách durable vs ephemeral**: typing, presence, read state đi core NATS `live.sync.>`, **không bao giờ lưu vào EVT**.
- **Tích hợp**: ConnectRPC (30+ service), bot là account thường, webhook vào (tương thích Slack) và ra (HMAC-SHA256, retry), MCP server.
- **Scale**: nhiều process sau load balancer round-robin, không cần session affinity, NATS cluster ≥3 node.

**Đánh giá**: cách đặt subject, tách durable/ephemeral, node stateless, giao thức snapshot/event/caughtUp đáng học. Tránh: JetStream làm DB duy nhất + projection trong RAM ở quy mô chục tỷ tin.

## 4. lxzan/gws

- Apache-2.0, bản mới nhất v1.10.2 (2026-09), pass toàn bộ Autobahn test suite.
- API: `OnOpen / OnClose / OnPing / OnPong / OnMessage`; option `ParallelEnabled` (mặc định tắt; bật thì **không giữ thứ tự**), `ReadMaxPayloadSize`, `Authorize`, `HandshakeTimeout`.
- Ghi tuần tự mỗi connection; hàng đợi ghi **không giới hạn** → phải tự chặn client chậm.
- **Broadcaster**: build frame 1 lần (có/không nén), đẩy vào hàng đợi từng connection, đếm tham chiếu.
- permessage-deflate: `BestSpeed`, threshold 512B; broadcast nén không dùng shared dictionary.
- Benchmark (lesismal, 10k conn): gws ~760k TPS / 217MB, gorilla ~764k / 288MB, nbio ~717k / 95MB. Ở 1M connection trên 1 node thì nbio/greatws (epoll) phù hợp hơn.

**Đánh giá**: phù hợp gateway 100–300K connection/node khi dùng Broadcaster, `ParallelEnabled=false`, hàng đợi gửi có giới hạn tự làm.

## 5. Tham chiếu bổ sung

- **Discord** (tham chiếu chung, không nằm trong 4 repo): lưu tin trong Cassandra/ScyllaDB, partition theo `(channel, bucket)` → đọc trang bất kỳ chỉ chạm 1 partition. Ý tưởng "dữ liệu một room nằm liền nhau theo thứ tự" được áp dụng cho `chatim` bằng MongoDB clustered collection.
- **MongoDB clustered collection** (từ 5.3): document lưu theo thứ tự `_id` trong 1 file WiredTiger, "one write for inserts… and one read for queries"; clustered key bắt buộc `{ _id: 1 }`, unique; cho phép index phụ; không chuyển đổi qua lại với collection thường. Tài liệu không nói rõ có shard được không; **đã tự kiểm chứng trên `mongo:8.2.12`**: shard được theo `{_id: 1}`, chuyển chunk giữa shard giữ nguyên clustered index, truy vấn khoảng theo room chạy `SINGLE_SHARD` + `CLUSTERED_IXSCAN` (chi tiết ở mục 4.1 của tài liệu thiết kế).

## 6. Áp dụng cho chatim

**Lấy**
- Seq liên tục theo room + read marker (tinode) → unread không cần ghi riêng từng người.
- Xoá lưu tombstone/khoảng (tinode); idempotency theo id client (teamgram).
- pts + đồng bộ phần chênh (teamgram getDifference) → client phát hiện và vá gap.
- 1 broadcast cho mỗi node gateway (tinode), subject `evt.{…}` + RePublish realtime (chatto).
- Typing/presence không lưu (chatto); node stateless, không sticky (chatto).
- Broadcaster encode 1 lần, giữ thứ tự, hàng đợi gửi có giới hạn (gws).

**Tránh**
- Gọi plugin/consumer đồng bộ trong đường ghi (tinode).
- Seq 32-bit (tinode); counter chỉ nằm trong Redis (teamgram).
- Write-fanout per user với group/channel lớn (teamgram).
- Thiếu index cho truy vấn lịch sử (teamgram).
- JetStream làm DB duy nhất với index trong RAM (chatto).
- Đóng gói nhiều service vào 1 container (teamgram).

## Nguồn

- tinode/chat: https://github.com/tinode/chat
- teamgram-server: https://github.com/teamgram/teamgram-server · teamgram/proto: https://github.com/teamgram/proto
- chatto: https://github.com/chattocorp/chatto
- gws: https://github.com/lxzan/gws · benchmark: https://github.com/lesismal/go-websocket-benchmark
- MongoDB Clustered Collections: https://www.mongodb.com/docs/manual/core/clustered-collections/
- MongoDB `db.createCollection` (clusteredIndex): https://www.mongodb.com/docs/manual/reference/method/db.createCollection/
- MongoDB Shard Keys: https://www.mongodb.com/docs/manual/core/sharding-shard-key/
