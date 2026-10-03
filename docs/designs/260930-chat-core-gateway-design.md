# chatim — Thiết kế Phase 1: `core` + `gateway`

> Ngày: 2026-09-30 · Trạng thái: **đã duyệt qua brainstorm** — chưa implement; cần PoC R1–R5 trước khi code toàn bộ
> Nghiên cứu nền: [../research/260930-opensource-chat-architecture-research.md](../research/260930-opensource-chat-architecture-research.md)

## Tóm tắt

`chatim` là hạ tầng chat dùng chung (CPaaS nội bộ) cho nhiều sản phẩm, multi-tenant về mặt logic. Monorepo Go, mỗi app một container. Phase 1 gồm:
- **`core`**: room, member, message, tương tác, cấp seq, lưu trữ, phát event.
- **`gateway`**: giữ kết nối WebSocket, đẩy event realtime tới client.

Ba ý chính:
1. **MongoDB clustered collection** với `_id = room│thread│seq` → tin của một room nằm liền nhau trên đĩa, đọc trang bất kỳ (kể cả cũ nhất) là 1 lần quét liên tục.
2. **Slot + actor + flush gộp**: mỗi room được ưu tiên xử lý ở một core (quyền sở hữu mềm trên Redis), seq giữ trong RAM, nhiều room gộp chung 1 `insertMany` → ~1.5K round trip/s thay vì ~20K.
3. **Publish 1 lần, gateway tự nhân bản**: event vào NATS JetStream, gateway chỉ subscribe room mà client của nó là member, dùng gws Broadcaster encode 1 lần.

```
 Client SDK (web/mobile)                          App backend (bot/AI/CRM) — qua app `events` (sau)
        │ WebSocket · protobuf                                         ▲
        ▼                                                              │
 ┌──────────── gateway × N ─────────────┐                              │
 │ gws · JWT · roomIndex · Broadcaster  │◄── live.{t}.room.{rid}.> ◄── NATS ◄── RePublish ──┐
 └──────┬──────────────────────────────┘                                                    │
        │ gRPC (chọn core theo slot)                                                        │
        ▼                                                                                   │
 ┌──────────── core × N ────────────────┐     publish evt.{t}.… ──► JetStream CHATIM_EVT ───┘
 │ slot ownership · actor/room · flusher│
 └──────┬───────────────┬──────────────┘
        ▼               ▼
   MongoDB RS (3)    Redis state (slot lease, heartbeat, watermark, presence) + Redis dedupe (cid)
```

## 1. Phạm vi

**Làm ở Phase 1**
- Room: DM (1-1), group ≤5K member, channel ~200K subscriber (chỉ admin post).
- Tin nhắn: gửi, sửa (**có lịch sử sửa**), xoá (cho mọi người / phía tôi), trả lời, chuyển tiếp.
- Tương tác: reaction, read receipt + unread, typing, presence, thread, @mention, ghim, bookmark, đánh dấu room chưa đọc.
- Event stream nội bộ cho các app khác trong monorepo.

**Không làm ở Phase 1**: app auth / api / events / push / migrator (dual-write từ MongoDB cũ), API import tin, lưu file/media, search, E2EE, webhook, voice/video, admin UI, cầu nối Kafka.

**Quy mô**: ~100K kết nối đồng thời, 5–10K tin/s lúc cao điểm, 5–20 tỷ tin sau 2 năm.

## 2. Assumptions (yêu cầu phi chức năng)

| # | Giả định |
|---|---|
| A1 | p99: ack ≤30ms · tới người nhận online ≤100ms · trang 50 tin ở bất kỳ vị trí (kể cả cũ nhất) ≤20ms |
| A2 | Đã ack = đã lưu `w:majority`; thứ tự trong room luôn đúng; giao at-least-once, client bỏ trùng theo `(room, pts)` |
| A3 | 99.95%, không điểm chết đơn lẻ: core ≥2, gateway ≥2 (3 cho 100K), MongoDB **replica set 3 member, không sharding**, NATS 3, Redis sentinel |
| A4 | Tin nhắn và lịch sử sửa lưu vĩnh viễn · `room_events` 30 ngày · `CHATIM_EVT` 7 ngày |
| A5 | JWT do app auth cấp (EdDSA/RS256, JWKS theo tenant, claim `tenant`/`sub`/`sid`/`exp`); mTLS nội bộ; không E2EE |
| A6 | Text ≤16KB · meta ≤8KB · ≤50 mention · ≤10 tham chiếu file đính kèm |
| A7 | Lịch sử sửa xem được bởi member có quyền đọc room; tenant có thể tắt |

## 3. Monorepo và triển khai

```
chatim/
├── go.mod                  # 1 module
├── apps/
│   ├── core/               # main.go + internal/{domain,actor,slot,store,flush,grpcsrv,publish}
│   └── gateway/            # main.go + internal/{ws,session,interest,fanout,authn}
├── pkg/                    # dùng chung, KHÔNG chứa nghiệp vụ
│   ├── pb/                 # code sinh từ proto (buf)
│   ├── slotmap/            # hash(room)→slot + client đọc bảng định tuyến
│   ├── mongox/ redisx/ natsx/
│   └── config/ logx/ telemetry/
├── proto/chatim/v1/        # common, room, message, core_service, realtime, events
├── deploy/
│   ├── docker/Dockerfile   # multi-stage, ARG TARGET → distroless
│   └── compose/            # mongo:8.2 replica set 1 node (rs0), redis, nats×3, core×2, gateway×2
└── docs/  Makefile  buf.yaml
```

- `internal/` của Go chặn app import code nội bộ của app khác. App mới (api, events, push, auth, migrator) thêm vào `apps/`.
- 1 Dockerfile cho mọi chương trình Go: `make image TARGET=apps/core` (hoặc bất kỳ package main nào, vd `tools/poc/natsbench`).
- Giao tiếp: `gateway → core` gRPC unary · `core → JetStream` · `gateway ← NATS`. Không gọi ngược.
- Thư viện: gws, mongo-go-driver v2, go-redis v9, nats.go + jetstream, grpc-go, buf, OpenTelemetry, prometheus/client_golang, testcontainers-go, goleak. Go stable mới nhất (≥1.26).
- Quy ước dev: mọi lệnh Go (build, test, chạy) chạy trong container `golang:1.26` qua `make`, máy dev chỉ cần Docker. Code không có comment; giải thích nằm trong tài liệu và commit message.
- Mỗi container: `/healthz`, `/readyz`, dừng gọn khi SIGTERM, cấu hình qua env.
- **MongoDB dev** theo quy ước team: `mongo:8.2`, replica set 1 node `rs0` (`--replSet rs0 --keyFile`, keyfile tự sinh trong volume khi khởi động lần đầu), healthcheck `mongosh ping`, container `mongodb-init` chạy `rs.initiate` một lần. User/password lấy từ `.env` (đã gitignore), không ghi cứng trong compose. Prod: replica set 3 member, `w:majority`.

## 4. Mô hình dữ liệu (MongoDB, WiredTiger `zstd`)

**Hai loại số thứ tự**
- `seq`: vị trí tin trong **timeline**. Timeline chính của room và mỗi thread có dãy riêng.
- `pts`: vị trí của **mọi event được lưu** trong room (tin mới, sửa, xoá, reaction, ghim, member, thread cập nhật). Dùng để phát hiện event bị thiếu và đồng bộ phần chênh.

**Khoá nhị phân**: các số ghép big-endian `uint64` → thứ tự byte trùng thứ tự số. `room_id` là số 63-bit ngẫu nhiên (trả client dạng string) để không đoán được id, không lộ số lượng room, và để khi shard thì ghi phân tán đều giữa các shard. `thread_root = 0` là timeline chính. Seq bắt đầu từ 1; giá trị `math.MaxUint64` được giữ lại, không dùng làm seq, vì khoảng "cả timeline" `MsgRange(room, thread, 0, MaxUint64)` là khoảng nửa mở.

| Collection | `_id` / khoá | Trường chính | Index |
|---|---|---|---|
| `rooms` | `room_id` | tenant, type (dm/group/channel), name, settings (ai được post, bật thread), member_count, last_seq, last_pts, last_msg_at, dm_key, pins (≤50) | `{t, dm_key}` unique (partial, chỉ DM) |
| `members` | ObjectId | r, u, role, read_seq, mention_unread, marked_unread, muted_until, cleared_seq | `{r,u}` unique · `{t,u,r}` |
| `messages` (**clustered**) | 24B `room│thread_root│seq` | f, p (pts), kind, text, attachments, mentions, reply_to, forward_from, thread_count, thread_last_seq, reaction_summary, ver, edited_at, deleted, cid, ts, meta | **không có index phụ** |
| `message_edits` (clustered) | 28B `room│thread│seq│version` | bản bị thay thế: text, attachments, meta, editor, ts | — |
| `room_events` (clustered) | 16B `room│pts` | type, seq đích, payload thay đổi, ts | TTL 30 ngày trên `ts` |
| `reactions` | ObjectId | k (`room│thread│seq`), u, emoji | `{k,u,emoji}` unique |
| `thread_subs` | ObjectId | r, thread_root, u | `{r,thread_root,u}` unique |
| `bookmarks` | ObjectId | t, u, r, thread_root, seq, note, ts | `{t,u,ts:-1}` |
| `hidden` (xoá phía tôi) | ObjectId | u, r, thread_root, seq | `{u,r,thread_root,seq}` |

**Quy tắc**
- Timeline chính = quét `R│0│*`; thread = quét `R│root│*`. Insert tin mới chỉ ghi 1 cấu trúc.
- Tin mới **không** ghi vào `room_events` (tin đã mang `p`); `room_events` chỉ chứa thay đổi.
- Unread = `rooms.last_seq − members.read_seq` → không ghi cho từng member khi có tin mới.
- Reaction: số đếm theo emoji nằm trong tin (`$inc`), danh sách người thả ở `reactions` → tin trong channel lớn không phình.
- Xoá cho mọi người = tombstone (giữ `_id`, xoá nội dung) → không tạo lỗ seq.
- Mỗi reply trong thread sinh `room_event` `thread.updated` (seq tin gốc, số reply, seq cuối).

### 4.1 Sẵn sàng sharding — chỉ đổi cấu hình, không sửa code

Phase 1 chạy replica set. Khi cần shard, app chỉ đổi `MONGO_URI` sang mongos; phần còn lại là thao tác vận hành.

**Đã kiểm chứng trên `mongo:8.2.12`** (cluster 2 shard trong Docker, 2026-09-30, kiểm tra chức năng — chưa phải benchmark):
- Clustered collection với `_id` 24B `room│thread│seq` **shard được** theo `{_id: 1}`.
- Di chuyển chunk giữa 2 shard thành công; shard nhận vẫn giữ clustered index.
- Truy vấn khoảng của 1 room qua mongos → `SINGLE_SHARD`, `CLUSTERED_IXSCAN` trên đúng shard chứa room.
- `_id` vẫn unique trên toàn cluster (`E11000`) → cơ chế CAS ở 5.2 giữ nguyên; `insertMany(ordered:false)` trải nhiều shard vẫn trả lỗi riêng từng doc và ghi các doc còn lại.

**Quy tắc code bắt buộc từ Phase 1** (vi phạm = sau này phải sửa code):

| Quy tắc | Lý do |
|---|---|
| Collection lớn (`messages`, `message_edits`, `room_events`) dùng `_id` có prefix `room_id`; shard key tương lai = `{_id: 1}` | Một room nằm trọn trên một shard; unique `_id` toàn cluster |
| `reactions`: shard key tương lai = `{k: 1}`; unique index `{k,u,emoji}` bắt đầu bằng `k` | Unique index trên collection đã shard phải có shard key làm prefix |
| Mọi truy vấn vào collection lớn phải có prefix room trong `_id` (khoảng hoặc `$in`) | mongos gửi thẳng tới 1 shard, không phải hỏi tất cả shard |
| Không dùng transaction nhiều document, không `$lookup` vào collection lớn | Tránh 2PC xuyên shard và hạn chế của `$lookup` |
| Collection metadata (`rooms`, `members`, `bookmarks`, `hidden`, `thread_subs`) giữ không shard | Nhỏ hơn nhiều; nếu cần giãn tải thì `moveCollection` sang shard khác (vận hành, không sửa code) |
| `MONGO_URI`, read preference, write concern lấy từ cấu hình; lệnh tạo collection nằm trong bước bootstrap, không nằm rải rác trong nghiệp vụ | Đổi môi trường không đụng code |

**Các bước khi cần shard** (vận hành):
1. Chuyển replica set hiện tại thành shard đầu tiên (thủ tục chính thức "Convert a Replica Set to a Sharded Cluster"; từ 8.0 có thể dùng config shard), thêm mongos.
2. Đổi `MONGO_URI` của core sang danh sách mongos, rolling restart.
3. `sh.shardCollection` cho các collection lớn theo shard key ở bảng trên; thêm shard mới để balancer chia dữ liệu.

## 5. Đường ghi

### 5.1 Slot và quyền sở hữu mềm (Redis)

`slot = hash(room_id) % 1024`.

```
chatim:core:{id}      = "<grpc addr>"  TTL 5s   ← heartbeat, gia hạn mỗi 1s
chatim:cores          = ZSET core id → hạn heartbeat (unix ms, giờ Redis)  ← danh sách core đang sống
chatim:slot:{0..1023} = "<core id>"    PX 10s   ← quyền sở hữu slot
chatim:slots:changed  (pub/sub)                 ← báo gateway nạp lại bảng định tuyến
```

Mỗi core chạy vòng lặp 1s (có jitter):
0. Heartbeat bằng một script Lua: `SET chatim:core:{id} PX 5s`, `ZADD chatim:cores <now+5s> <id>`, xoá member có hạn ≤ now, rồi trả về các member có hạn > now (tối đa 256). `now` lấy từ `TIME` của Redis. Không dùng `SCAN`/`KEYS` để tìm core vì chúng đi qua toàn bộ keyspace, gồm cả các key `chatim:cid:*` (D25).
1. Gia hạn slot đang giữ (Lua: giá trị vẫn là mình → `PEXPIRE`; khác → coi như mất).
2. Mục tiêu = `ceil(1024 / số core đang sống)`.
3. Thiếu → nhận slot trống hoặc slot của core hết heartbeat (`SET NX` / Lua), chọn theo thứ tự rendezvous hash.
4. Dư → nhả bớt: ngừng nhận lệnh, flush xong batch đang dở, xoá key nếu vẫn là của mình.

Slot manager dùng một Redis client riêng (pool riêng, không chung với chống trùng cid, watermark và active mark) để gia hạn lease không bao giờ phải xếp hàng sau lưu lượng ghi tin (D28).

**Hook theo batch, không chặn `Step`.** `slot.Config` có 3 hook nhận **nhiều slot một lần**, không bao giờ chạy goroutine riêng: `BeforeRelease(ctx, slots)` — slot sắp nhả trong `Step` (nhả dư hoặc `ReleaseAll` lúc shutdown), gọi sau khi `Owns` đã false và trước khi xoá key lease; `AfterClaim(ctx, slots)` — slot vừa nhận trong `Step`; `AfterLose(ctx, slots)` — slot bị core khác ghi đè khi gia hạn thất bại. Cả 3 hook của cùng một `Step` dùng chung một context, hết hạn sau `HookTimeout` (mặc định `Tick/2`, phải nhỏ hơn `Tick`) tính từ lúc `Step` bắt đầu — nên một hook chặn không bao giờ kéo một `Step` dài quá một tick; hook phải tôn trọng ctx. Hook `nil` bị bỏ qua, batch rỗng không gọi (D26).

Router dùng `AfterLose` + `AfterClaim` để cho nghỉ (`EvictSlots`) mọi actor của room thuộc các slot vừa đổi chủ: actor đang nghỉ từ chối lệnh mới bằng `ErrRetryLater`, chờ nhóm ghi đang bay (nếu có) kết thúc bình thường rồi tự xoá; lệnh `Send` kế tiếp dựng actor mới, nạp lại `last_seq` và cache cid từ DB thay vì tiếp tục tranh chấp với core chủ mới bằng seq cũ (D27).

**Nguyên tắc**: *core nào cũng xử lý đúng được mọi room*. Chủ slot chỉ là nơi được ưu tiên để gộp batch, giữ thứ tự và dùng cache. Trạng thái trong RAM của actor chỉ là cache; mọi thay đổi có ý nghĩa đi qua lệnh atomic của DB. Vì vậy Redis sai (failover, 2 core cùng giữ một slot) không làm mất hay trùng tin.

**Mốc sở hữu an toàn** (chỉnh qua review khi implement): core chỉ coi mình còn giữ slot khi lease mới hơn `min(LeaseTTL, HeartbeatTTL) − Tick`, tính từ đầu vòng lặp (trước lệnh ghi heartbeat). Lý do: core khác được phép nhận slot ngay khi heartbeat hết hạn (5s), không phải đợi lease (10s). Nhờ vậy khoảng thời gian hai core cùng tưởng giữ một slot không quá khoảng một tick. `LeaseTTL` và `HeartbeatTTL` đều phải lớn hơn `2×Tick`.

### 5.2 Actor và flusher

```
Gateway ─gRPC Send{room, thread, cid, content}─► core chủ slot (không phải chủ vẫn xử lý đúng)
core
 ├─ router: room → actor (tạo khi có lệnh, dừng sau 5 phút không hoạt động)
 ├─ actor(room): 1 goroutine, hàng đợi ≤1024 (đầy → RESOURCE_EXHAUSTED)
 │    cache: last_seq từng timeline, last_pts, member/role, cid LRU 10 phút, 100 tin gần nhất
 │    ① kiểm quyền (đúng tenant, là member, channel chỉ admin post)
 │    ② chống trùng cid: LRU RAM → Redis SET NX
 │    ③ seq = last_seq+1, pts = last_pts+1 → chuyển doc sang flusher
 └─ flusher (vài worker / core): flush mỗi 2ms hoặc khi đủ 256 doc
      insertMany(ordered:false, w:majority) → kết quả từng doc
        ✔ thành công    → publish JetStream (async) → ack gateway {seq, pts}
        ✘ duplicate key → core khác vừa ghi room này: actor nạp lại last_seq/pts từ DB, gán lại, thử lại
        ✘ timeout       → đọc lại khoảng seq theo cid để biết doc nào đã ghi, thử lại phần thiếu
 mỗi 1s: bulkWrite rooms {$max last_seq, last_pts, last_msg_at} + read_seq của người gửi
```

- `_id` unique đóng vai trò CAS → không cần ghi counter trên đường gửi tin.
- Lỗi ghi dở giữa chừng (rất hiếm) có thể để lại lỗ seq/pts; client và API coi lỗ quá 5s là void.
- Mỗi actor chỉ giữ **một nhóm ghi đang bay**: số bị từ chối lại thử trước, lệnh mới xếp sau. Nhận nhóm thứ hai trước khi biết kết quả nhóm đầu không an toàn, vì `last_seq` trong cache có thể lùi lại nếu nhóm đầu hoá ra trùng (D29).
- Kết quả insert có 4 trạng thái: `Inserted`, `Duplicate` (lỗi khoá trùng), `Unknown` (timeout/lỗi transport — không rõ đã ghi hay chưa) và `Rejected` (lỗi ghi khác, không phải trùng khoá). `Duplicate` và `Unknown` được đối chiếu bằng `Find` (read concern majority) theo `(from, cid)`: doc của chính mình → coi như đã ghi — kể cả khi `Find` chưa thấy ngay (ghi majority chưa kịp lan, thử lại `Find` trong cùng deadline) — còn doc của core khác → nạp lại `last_seq`/`last_pts` rồi gán lại (tối đa 3 lần); `Unknown` mà `Find` không thấy gì ở đúng seq cũ → gửi lại đúng seq đó (tối đa 3 lần); hết cả hai hạn mức mà vẫn chưa rõ → `ErrRetryLater`, buộc actor nạp lại `last_seq`/`last_pts` và cache cid trước nhóm kế tiếp (D30).
- Mỗi entry có **ngân sách tuyệt đối** tính từ lúc đặt chỗ cid (`Reserve`), bị chặn bởi TTL đặt chỗ (`ReservationTTL`, cấu hình bằng `CID_PENDING_TTL`): hết ngân sách trước khi hết số lần thử lại cũng coi như hết hạn mức, để một entry không sống qua nhiều lần group deadline trong lúc key `pending` 10s đã hết hạn và một core khác đã ghi trùng (D32).
- Flusher nhận `Deadline` theo từng nhóm: không gửi một nhóm nếu không thể xong trước deadline (`now + InsertTimeout` vượt deadline) mà trả `Unknown` kèm `ErrNotSent` (bọc `ErrRetryLater`); phần còn lại của batch vẫn gửi bình thường. Context insert, và qua đó `maxTimeMS` phía server, cũng cắt đúng ở deadline này (D33).
- Chống trùng cid theo 3 tầng: LRU RAM (10 phút / 4096 entry, nạp lại từ 100 tin gần nhất lúc actor khởi động) → Redis dedupe `pending` (10s, một `EVALSHA` cho cả nhóm) → Redis `committed` (15 phút, ghi đè không điều kiện ngay sau khi DB commit, vì DB là nguồn sự thật). Redis lỗi → chỉ dùng LRU, tạm ngừng gọi Redis 1s (`cooldown`, một probe mỗi cooldown, log một lần lúc vào và một lần lúc ra chế độ suy giảm) (D34).
- `CreateRoom` tạo room trước rồi mới tạo member: `_id` trùng (va chạm room id ngẫu nhiên) phải bị chặn ngay ở bước đầu; tạo member trước có thể gắn người vào room của người khác nếu bước tạo room sau đó mới thất bại (D35).

### 5.3 Thay đổi khác

- Sửa, xoá, reaction, ghim, member, room: qua actor để lấy `pts`; ghi bằng lệnh atomic có điều kiện + insert `room_events`; gộp vào cùng flusher (bulkWrite theo collection).
- **Sửa tin**: `updateOne({_id, ver: N}, {$set: nội dung mới, $inc: {ver: 1}})` + insert bản cũ vào `message_edits`.
- **Read receipt**: trạng thái riêng từng user, không cần `pts`: `members {$max read_seq}` gộp mỗi 1s; gateway giới hạn 1 lần/s/room/user.

### 5.4 Publish không mất event (at-least-once)

1. **Trước khi flush một nhóm ghi**, actor đánh dấu room đang hoạt động và ghim mốc publish nếu chưa có — cả hai trong **một** lệnh Lua: `ZADD chatim:active:{slot} <now ms> <room>` (tối đa 1 lần/5s/room) và `SET chatim:pubwm:{room} <min(mốc đã publish, seq thấp nhất của nhóm sắp ghi − 1)> NX PX 7d` (đã có thì chỉ gia hạn TTL, không bao giờ hạ hay ghi đè). Đánh dấu/ghim phải đi **trước** insert, không phải sau publish: nếu core chết ngay sau commit mà trước publish, sweeper cần thấy room này trong `active:{slot}` ngay cả khi publish chưa từng chạy (D36).
2. Ack client ngay khi DB commit. Publish với `Nats-Msg-Id = room│pts` (JetStream bỏ trùng trong 2 phút). Watermark trong RAM chỉ tiến qua dải `pts` **liên tục** đã nhận `PubAck`, không phải theo pts lớn nhất đã publish: một `pts` publish lỗi (hoặc một lỗ vĩnh viễn báo bằng `Skip`) giữ watermark đứng đúng tại đó để chờ khôi phục, thay vì để lỗ tuột qua (D37).
3. **Sweeper khôi phục** (`recovery.Sweeper`) chạy khi core vừa nhận slot và mỗi `Interval` (mặc định 30s) cho mọi slot đang giữ. Với mỗi room trong `active:{slot}`: `last_pts` (RAM actor) lớn hơn watermark Redis → room sau mốc → `Router.Recover` từ watermark, nhưng chỉ khi tin ngay sau watermark đã cũ hơn `StaleAfter` (mặc định 5s) — tin trẻ hơn coi như publisher đang xử lý, bỏ qua lượt này. Room đã bắt kịp mốc chỉ bị xoá khỏi `active:{slot}` khi mốc hoạt động cũ hơn `RemoveAfter` (mặc định 15s, phải ≥ chu kỳ đánh dấu + group deadline + 1s lệch đồng hồ) **và** điều kiện đó vẫn đúng ngay lúc xoá (so khớp điểm số trong cùng một lệnh Lua, theo batch) — một lần ghi mới tự đánh dấu lại entry thì không bị xoá nhầm (D38).
4. **Lỗ còn sót lại đã biết, chấp nhận ở M2a**: Redis lỗi đúng lúc đánh dấu/ghim (actor vẫn ghi tiếp ở chế độ suy giảm, không có mốc để khôi phục); client đọc từ RAM actor cũ rồi actor đó bị rollback do thua CAS; một core cũ còn tưởng mình chủ slot (stale double-owner) lấp đúng một lỗ mà core mới đã bỏ qua; giả định `pts == seq` của M2a — M2b phải sửa lại cách quét theo pts khi sửa/xoá/reaction sinh pts mà không sinh seq.

## 6. Đường đọc

Lệnh đọc gắn với một room đi về core chủ slot (dùng RAM actor); lệnh còn lại chia đều.

| API (core gRPC) | Cách lấy | p99 |
|---|---|---|
| `GetHistory(room, thread, anchor, dir, limit≤100)`, anchor = `latest`/`oldest`/`before_seq`/`after_seq`/`around_seq` | Quét khoảng `_id` clustered. Trang mới nhất từ RAM actor; trang cũ đọc `secondaryPreferred` | ≤5ms (RAM) · ≤20ms (bất kỳ) |
| `GetMessages(room, [{thread, seq}])` | `$in` trên `_id` (xem trước reply, pin, bookmark) | ≤10ms |
| `ListMyRoomIDs(user)` | Covered query trên `{t,u,r}`; gateway gọi khi client kết nối | ≤10ms |
| `ListMyRooms(user, cursor)` | members → rooms (`$in`) → sắp theo `last_msg_at`; kèm unread, mention_unread, marked_unread, tin cuối | ≤30ms |
| `Sync([{room, pts, seq}])` | Tin có `seq >` của client + `room_events` có `pts >` của client (≤200). Thiếu >200 hoặc quá 30 ngày → `too_long`, client tải lại trang mới nhất | ≤50ms / 20 room |
| `GetEditHistory` · `GetReactions(msg, emoji, cursor)` · `ListPins` · `ListBookmarks` | Quét theo prefix / index có sẵn | ≤20ms |

- Lọc theo người đọc: ẩn seq ≤ `cleared_seq`, ẩn tin trong `hidden` (cache theo user–room), tin xoá trả placeholder `deleted`.
- Nhiều client cùng mở một room (vd channel vừa có bài) → singleflight trong actor, mỗi trang chỉ đọc Mongo 1 lần.
- Không cache tin nhắn trong Redis.

## 7. Gateway realtime

**Kết nối**
- gws, 1 goroutine/connection, `ParallelEnabled=false`, tắt nén, frame ≤64KB, ping 25s, 60s không phản hồi thì đóng.
- JWT trong subprotocol hoặc frame đầu (≤5s), kiểm tra bằng JWKS có cache.
- Giao thức protobuf: `ClientFrame{id, oneof: send | edit | delete | react | read | typing | history | sync | sub_presence …}`, `ServerFrame{reply{id} | event | heartbeat}`. Subprotocol JSON cho debug.
- Hàng đợi gửi mỗi connection có giới hạn (256 frame / 1MB): đầy → bỏ typing/presence trước → vẫn đầy thì đóng mã `4008 slow consumer`, client kết nối lại và `Sync`.

**Fanout theo interest**
```
core ─► JetStream CHATIM_EVT   evt.{t}.room.{rid}.{type} | evt.{t}.user.{uid}.{type}
          └─ RePublish ─► live.{t}.room.{rid}.evt.{type}          (core NATS, không lưu)
gateway ─► live.{t}.room.{rid}.eph.typing                          (không qua core, không vào DB)

gateway: roomIndex room→{conn}; 1 NATS sub mỗi room (đếm tham chiếu)
  client kết nối  → ListMyRoomIDs → sub live.{t}.room.{rid}.> + live.{t}.user.{uid}.>
  nhận event      → lọc theo người nhận → Broadcaster encode 1 lần → hàng đợi từng conn
  member.added/removed (subject user) → thêm/bớt sub room
```
- Channel 200K người: 1 publish → ~3 gateway → mỗi gateway broadcast cho subscriber online của nó.
- Typing: gateway kiểm membership bằng `roomIndex`, giới hạn 1 lần/3s, không có trong channel.
- Presence: Redis `pres:{t}:{uid}` đếm connection theo gateway; 0→1 online, 1→0 offline (chờ 5s chống nhấp nháy); client chỉ đăng ký presence của ≤200 user đang hiển thị.
- Tải: ~50K connection/gateway (~1.5GB RAM); 3 gateway cho 100K.
- Gateway không sắp lại thứ tự; SDK client theo dõi `pts` từng room, thấy lỗ thì gọi `Sync`.

## 8. Event stream

- Stream `CHATIM_EVT`: subject `evt.>`, R3, lưu file, giữ 7 ngày, cửa sổ chống trùng 2 phút.
- Envelope `chatim.events.v1.Event{id, tenant, room_id, room_type, thread, seq, pts, type, actor, ts, oneof payload}`. Chỉ thêm field (buf breaking check).
- Consumer nội bộ: durable pull consumer, `FilterSubjects` theo tenant/loại event, ack từng event, bỏ trùng theo `(room, pts)`. Thứ tự trong một room được giữ.
- Typing/presence không bao giờ vào stream.
- **NATS chỉ dùng nội bộ** (app trong monorepo). App bên ngoài đi qua app `events` (làm sau): kiểm tra token, buộc lọc theo tenant của app, bọc thành gRPC `WatchEvents(filter, cursor)`.

## 9. Xử lý sự cố

| Sự cố | Phản ứng |
|---|---|
| Mongo đổi primary (5–12s) | Flusher thử lại có backoff, lệnh chờ trong hàng đợi actor; quá deadline 3s → UNAVAILABLE, SDK gửi lại cùng `cid` |
| NATS chết toàn bộ | DB vẫn ghi; watermark tụt lại, publish bù khi NATS sống lại; gateway báo `degraded`, client `Sync` mỗi 5s |
| Redis state chết (`chatim-redis`) | Dùng bảng slot gần nhất (lease hết hạn thì `Owns()` false, mọi core vẫn ghi đúng nhờ `_id` làm CAS); watermark/active mark tạm ngừng, sweeper publish bù khi Redis sống lại; tạm tắt presence/typing |
| Redis dedupe chết (`chatim-redis-dedupe`) | Chống trùng chỉ còn LRU RAM actor (redisguard cooldown); slot, watermark và publish không bị ảnh hưởng. Mất key cid khi khởi động lại là chấp nhận được vì instance này không lưu đĩa (D44) |
| Core chết | Core khác nhận slot trong ~5s; trong lúc chờ, core bất kỳ vẫn xử lý đúng; publish bù theo watermark |
| Core bị kill giữa chừng một lần ghi | DB là nguồn sự thật: nhóm đã `Inserted` ở Mongo coi như xong dù core chết trước khi ack/publish; core nhận slot mới nạp lại `last_seq`/`last_pts` từ DB, sweeper publish bù theo watermark (5.4); không có 2PC nên không có trạng thái treo giữa DB và publish |
| Redis chậm hoặc suy giảm (vượt `REDIS_OP_TIMEOUT`) | Mỗi client Redis (chống trùng, watermark/active mark, slot lease) tự chuyển chế độ suy giảm độc lập: chống trùng dùng LRU RAM, watermark/active mark tạm ngừng cập nhật (sends continue without recovery marks), slot renew dùng lease gần nhất; mỗi client log 1 lần vào và 1 lần ra chế độ suy giảm, 1 probe mỗi `cooldown` (1s) |
| NATS gián đoạn kéo dài qua hết TTL watermark (> 7 ngày) | Phần timeline chưa publish vẫn còn trong DB; watermark `chatim:pubwm:{room}` hết TTL thì mất mốc, sweeper coi room như chưa publish gì và quét lại từ `pts` 1 — tốn hơn nhưng không mất event; biết trước và chấp nhận ở M2a (room phải im lặng > 7 ngày) |
| Gateway chết / kết nối lại hàng loạt | Client backoff + jitter 0–10s; gateway giới hạn handshake/s; client `Sync` theo pts |
| Room nóng | Hàng đợi actor có giới hạn, singleflight khi đọc, rate limit post theo user |

## 10. Bảo mật

- JWT: chỉ chấp nhận EdDSA/RS256; kiểm tra `iss`, `aud`, `exp` theo JWKS của tenant.
- `gateway → core`: mTLS nội bộ. Core chỉ nhận `tenant`/`user` từ metadata của gateway và **kiểm tra lại room thuộc tenant đó ở mọi thao tác**.
- Kiểm tra đầu vào theo A6; text phải là UTF-8 hợp lệ.
- Rate limit ở gateway: ~10 tin/s/user (burst 20) + quota theo tenant trong Redis.
- Không log nội dung tin (PII); NATS và Redis dùng TLS + credential riêng cho từng service.
- Compose dev truyền mật khẩu Mongo qua tham số `mongosh -p` (thấy được trong danh sách process của máy dev): chấp nhận cho dev, theo cách team đang làm; mongosh không đọc mật khẩu từ biến môi trường. Production dùng secret của orchestrator.

## 11. Kiểm thử (70 / 20 / 10)

- **Unit**: mã hoá key nhị phân (thứ tự byte), tính slot, actor gán lại seq khi duplicate, flusher gộp batch, kiểm quyền, bộ phát hiện gap phía client, codec frame.
- **Integration** (testcontainers: Mongo RS, Redis, NATS): 2 core cùng ghi 1 room, chuyển slot, kill core rồi kiểm tra publish bù, trang cũ nhất, CAS khi sửa, `Sync` trả `too_long`.
- **Giữ sẵn sàng sharding**: một bộ integration test chạy trên cluster 2 shard, kiểm tra mọi truy vấn vào collection lớn có `explain` là `SINGLE_SHARD` → chặn sớm code vi phạm quy tắc 4.1.
- **E2E**: docker compose + client WS viết bằng Go chạy kịch bản đầy đủ.
- **Load**: 100K connection / 3 gateway; 10K tin/s (70% DM, 25% group, 5% channel); đo p99 ack, tới người nhận, trang cũ nhất trên tập 100M tin.
- **Chaos**: kill core, `rs.stepDown()`, failover Redis, kill node NATS → mọi `cid` đã ack có đúng 1 bản trong DB; consumer nhận đủ mọi pts.

## 12. Quan sát hệ thống

- Metric: kích thước/độ trễ flush, số lần thử lại do duplicate, độ sâu hàng đợi actor, slot đang giữ, độ trễ publish (`pts` DB − watermark), số connection, frame bị bỏ, số lần đóng 4008, độ trễ tới người nhận.
- OTel trace gateway → core → Mongo/NATS; log JSON (slog); dashboard Grafana; `/healthz`, `/readyz`.

## 13. Rủi ro — cần PoC trước khi code toàn bộ

Kết quả trên máy dev và hướng dẫn chạy prod-like: [../poc/README.md](../poc/README.md). Chưa có quyết định go/no-go: trên dev (Intel Mac + OrbStack), R1b đạt sau khi sửa công cụ đo; R2 (p99 chờ ack) và lần ghi cuối của R4 chưa đạt, còn R5 mới kiểm ở mức unit; tất cả cần đo lại trên prod-like. So sánh MongoDB với PostgreSQL trên cùng máy: [../poc/260930-mongodb-vs-postgresql.md](../poc/260930-mongodb-vs-postgresql.md) — MongoDB thắng đọc trang ngẫu nhiên khi dữ liệu > RAM và dung lượng, PostgreSQL thắng độ trễ ghi bền; D9 giữ nguyên, chờ đo prod-like.

| # | Rủi ro | Cách kiểm chứng | Nếu không đạt |
|---|---|---|---|
| R1 | Một replica set chứa toàn bộ dữ liệu: 5–20 tỷ tin ≈ 1.5–10TB thô trước nén; working set so với WiredTiger cache; range query `_id` 24B | Benchmark 100M doc: đo dung lượng sau `zstd`, p99 trang cũ nhất khi dữ liệu không nằm trong cache | Scale dọc (disk/RAM); chính sách lưu trữ/archive tin cũ theo tenant; chuyển sang sharded cluster chỉ bằng cấu hình (4.1) |
| R2 | p99 `insertMany` `w:majority` ở 10K doc/s | Benchmark trên phần cứng thật | Chỉnh kích thước batch / cửa sổ flush, tăng worker flusher, nâng disk/IOPS của primary |
| R3 | NATS ~1M interest sub + RePublish đổi subject | Load test sub/unsub + throughput RePublish | Gom sub theo prefix tenant, lọc ở gateway |
| R4 | gws 50K connection: RAM + độ trễ broadcast | Load generator Go | Giảm connection/gateway, cân nhắc nbio |
| R5 | Soft-ownership đúng khi Redis failover | Test chaos (kill master Redis khi đang tải) | Thêm fencing epoch trong Mongo |

## 14. Decision Log

| # | Quyết định | Phương án khác | Lý do |
|---|---|---|---|
| D1 | CPaaS nội bộ đa app | Messenger end-user, livechat, team chat | Mục tiêu sản phẩm |
| D2 | Go | NestJS, lai Go + TS | Cùng hệ với repo tham chiếu; RAM/connection thấp |
| D3 | ~100K online | 1M, <20K | Quy mô 1–2 năm tới |
| D4 | DM + group ≤5K + channel lớn | Chỉ group ≤500; supergroup ≥50K | Buộc read-fanout, không write-fanout per user |
| D5 | Event stream + gRPC stream + client WS; không webhook | Webhook | Cách các app khác nhận dữ liệu |
| D6 | Phase 1 = core + gateway; auth/api/events/push là app riêng | Core monolith có WS | Scale connection độc lập với scale ghi |
| D7 | Multi-tenant logic | Single-tenant, cho chat chéo tenant | Cô lập dữ liệu giữa sản phẩm |
| D8 | Migrate MongoDB cũ bằng app riêng, làm sau | Migrate trong core | Giữ core gọn |
| D9 | MongoDB cho toàn bộ core | ScyllaDB, Mongo + Postgres | Team chưa vận hành Scylla; đã vận hành Mongo; 1 DB ít vận hành nhất |
| D10 | `messages` clustered, `_id` = room│thread│seq | Bucket document, collection thường | Đọc trang bất kỳ = 1 lần quét liên tục |
| D11 | Slot + actor + flush `insertMany` gộp nhiều room | Stateless (A), hard-ownership (B), log-first (C) | ~1.5K thay vì ~20K round trip/s; đúng thứ tự; cache RAM |
| D12 | Quyền sở hữu mềm trên Redis, core tự nhận/nhả slot | App coordinator + NATS KV, Kafka consumer group, etcd | Không thêm app điều phối; đúng đắn không phụ thuộc Redis |
| D13 | Seq trong RAM, `_id` unique làm CAS, lỗ hiếm → void sau 5s | `$inc` counter mỗi tin; seq liên tục tuyệt đối | Bỏ ghi counter khỏi đường gửi tin |
| D14 | Lịch sử sửa: `message_edits` + CAS theo `ver` | Không lưu | Yêu cầu tính năng |
| D15 | Không có API import trong core Phase 1 | Import API trong core | Để app migrator làm sau |
| D16 | NATS chỉ nội bộ; cô lập tenant ở app `events` | Cấp credential NATS cho từng app ngoài | Đơn giản, không lộ bus |
| D17 | Protobuf qua WS + JSON để debug | Chỉ JSON | Nhỏ, nhanh, dùng chung proto với gRPC |
| D18 | Không cache tin trong Redis | Redis tail cache | RAM actor + WiredTiger đủ; tránh lệch khi sửa/reaction |
| D19 | Ack sau DB commit; publish at-least-once + watermark publish bù | Transactional outbox, log-first | Ack nhanh, không mất event |
| D20 | 1 `go.mod`, 1 Dockerfile `ARG TARGET` | `go.work` nhiều module | Ít phức tạp dependency và CI |
| D21 | MongoDB replica set, chưa sharding; code giữ sẵn sàng shard để bật bằng cấu hình | Sharded ngay từ đầu; replica set không tính tới shard | Quy ước vận hành của team; khi vượt ngưỡng chỉ đổi `MONGO_URI` + chạy `shardCollection` (đã kiểm chứng trên 8.2.12) |
| D22 | Build/test/chạy Go qua Docker (`golang:1.26`) bằng `make` | Cài Go trên từng máy | Đồng nhất phiên bản (máy dev có 1.25, nats.go v1.54 cần 1.26); yêu cầu của team |
| D23 | Không viết comment trong code | Doc comment theo chuẩn Go | Yêu cầu của team; ràng buộc cần giải thích ghi ở tài liệu và commit message |
| D24 | Mốc `Owns` = `min(LeaseTTL, HeartbeatTTL) − Tick`, tính từ đầu vòng lặp | `LeaseTTL − Tick`, tính sau round trip Redis | Heartbeat mới quyết định lúc core khác được nhận slot; cách cũ để hai core cùng giữ slot khoảng 4s (phát hiện khi review) |
| D25 | Core đang sống lấy từ ZSET `chatim:cores` (score = hạn heartbeat theo `TIME` của Redis); slot manager và resolver đều đọc ZSET này | `SCAN chatim:core:*`; tách key cid sang DB/instance khác; dùng đồng hồ của từng core | `SCAN` đi qua cả keyspace (corebench: Redis bận 31% lúc nghỉ chỉ để quét ~312K key cid). Giờ Redis là một đồng hồ chung, nên member hết hạn đúng lúc key heartbeat hết TTL và phân tích D24 giữ nguyên; đồng hồ lệch giữa các core sẽ làm một core thấy core khác chết sớm hơn và nới rộng khoảng hai core cùng giữ slot |
| D26 | Hook slot nhận cả batch (`BeforeRelease`/`AfterClaim`/`AfterLose`), dùng chung 1 `HookTimeout` cho mọi hook của 1 `Step` | Hook đồng bộ theo từng slot | Drain tuần tự hàng trăm slot có thể kéo dài hơn khoảng thời gian còn sở hữu; batch + 1 deadline chung giữ mọi `Step` trong 1 tick |
| D27 | Router cho actor nghỉ (`EvictSlots`) khi slot của room đổi chủ (`AfterLose`/`AfterClaim`) | Giữ actor chạy tiếp, để nó tự phát hiện mất quyền qua lỗi ghi | Actor cũ tranh chấp core mới bằng `last_seq` cũ gây thử lại/gán lại không cần thiết; actor mới nạp lại từ DB sạch hơn |
| D28 | Slot manager dùng Redis client/pool riêng | Dùng chung client với chống trùng cid, watermark, active mark | Gia hạn lease không bị xếp hàng sau lưu lượng ghi tin ở core bận |
| D29 | Mỗi actor chỉ 1 nhóm ghi đang bay | Pipeline nhiều nhóm song song | Cache `last_seq` sau 1 duplicate có thể lùi lại, không an toàn để đánh số nhóm tiếp theo trước khi biết kết quả nhóm trước |
| D30 | 4 trạng thái insert (`Inserted`/`Duplicate`/`Unknown`/`Rejected`), đối chiếu `Duplicate`/`Unknown` bằng `Find` majority theo `(from, cid)`, kể cả khi `Duplicate` chưa thấy ngay trong `Find` | Coi mọi lỗi ghi là thất bại hoặc là trùng | Tránh lưu trùng 1 tin đã thử lại và tránh ack 1 tin chưa chắc đã ghi |
| D31 | Giới hạn 3 lần gán lại seq (doc người khác) và 3 lần gửi lại cùng seq (`Unknown` + `Find` rỗng), tách riêng hai bộ đếm | 1 bộ đếm chung, hoặc không giới hạn | Hai tình huống khác bản chất (seq sai vs. chưa rõ có ghi); giới hạn riêng tránh vòng lặp vô hạn mà vẫn đủ thử lại |
| D32 | Ngân sách tuyệt đối mỗi entry = `ReservationTTL` tính từ lúc `Reserve`, không chỉ đếm số lần thử | Chỉ giới hạn theo số lần thử | 1 entry có thể sống qua ~7 lần group deadline trong khi key `pending` 10s đã hết hạn, để core khác ghi trùng |
| D33 | Flusher có `Deadline` theo nhóm; nhóm không kịp xong thì không gửi, trả `Unknown`+`ErrNotSent` | Luôn gửi, để context hết hạn giữa chừng | Biết trước là không kịp thì khỏi tốn 1 lượt insert; phần batch còn lại không bị kéo theo |
| D34 | Cid dedupe committed TTL 15 phút (cấu hình được), không phải 24h | TTL 24h | Ở 10K tin/s, 24h ≈ 860M key / 150GB trên đúng Redis đang giữ slot lease; đã tách Redis riêng cho chống trùng (D44) |
| D35 | `CreateRoom` ghi room trước, member sau | Ghi member trước, room sau | `_id` trùng (va chạm room id ngẫu nhiên) phải chặn ngay ở bước đầu; ngược lại có thể gắn người vào room của người khác |
| D36 | `ZADD active:{slot}` **trước** insert, ghim `pubwm` NX trong cùng 1 Lua (sửa bản nháp mục 5.4 ban đầu) | Đánh dấu/ghim sau khi publish thành công | Core chết giữa commit và publish vẫn phải lọt vào tầm quét của sweeper dù publish chưa từng chạy |
| D37 | Watermark RAM chỉ tiến qua dải pts liên tục đã có `PubAck` | Mốc kiểu max-set (pts lớn nhất đã thấy) | Max-set bỏ qua đúng pts publish lỗi, không bao giờ được khôi phục |
| D38 | Sweeper chạy lúc nhận slot + mỗi 30s; trễ khôi phục 1 tin non bằng `StaleAfter`; xoá khỏi `active:{slot}` cần mốc cũ hơn `RemoveAfter` **và** còn đúng lúc xoá (so khớp điều kiện trong Lua) | Sweeper chỉ chạy lúc nhận slot; xoá ngay khi bắt kịp mốc | Bỏ sót trường hợp nhả slot êm trong lúc NATS gián đoạn; xoá ngay có thể xoá nhầm 1 room vừa ghi lại ngay sau lần đọc `last_pts` |
| D39 | Thứ tự dừng có ngân sách từng bước trong `CORE_SHUTDOWN_BUDGET`: tắt readiness → gRPC graceful → sweeper → router → flusher → publisher → nhả slot → admin → đóng client; `Router.Close` có khoảng chờ êm riêng, publisher dừng cứng (abort) dựa vào sweeper khôi phục phần còn lại | Dừng đồng thời mọi thành phần; publisher chờ drain hết hàng đợi | Dừng tuần tự giữ đúng phụ thuộc (không nhận request mới khi DB sắp đóng); publisher không cần đợi hết vì sweeper đã đảm bảo khôi phục |
| D40 | gRPC chỉ mở cổng và báo `SERVING` sau khi router chạy và không thành phần nào lỗi lúc khởi động | Mở cổng ngay, báo lỗi sau | Tránh `/readyz` báo sẵn sàng trong lúc router/flusher/publisher chưa chạy |
| D41 | Mọi logger che giấu thông tin nhạy cảm (mật khẩu, query bí mật trong `MONGO_URI`/`NATS_URL`) qua 1 slog handler chung, kể cả lỗi bất đồng bộ của NATS/JetStream | Che ở từng điểm log riêng lẻ | 1 điểm che chắc chắn hơn; bỏ sót log bất đồng bộ (disconnect, publish lỗi) từng lộ credential |
| D42 | Mỗi unary RPC bị giới hạn bởi `CORE_REQUEST_DEADLINE` ở phía server | Chỉ dựa vào deadline phía client | Client lỗi hoặc không đặt deadline không được phép giữ tài nguyên server vô thời hạn |
| D43 | Client gRPC tắt service config từ DNS (`grpc.WithDisableServiceConfig`) | Giữ mặc định grpc-go (tra `_grpc_config` TXT trước khi dùng địa chỉ) | DNS host chậm làm mọi RPC đầu tiên chờ tới hết hạn (tái hiện được); ảnh hưởng mọi client gRPC nội bộ, kể cả gateway ở M4 |
| D44 | Hai instance Redis: **state** (`chatim-redis`: `chatim:core:*`, `chatim:cores`, `chatim:slot:*`, pub/sub `chatim:slots:changed`, `chatim:pubwm:*`, `chatim:active:*`; AOF everysec, `maxmemory-policy noeviction`) và **dedupe** (`chatim-redis-dedupe`: chỉ `chatim:cid:*`; không lưu đĩa, `maxmemory` theo `REDIS_DEDUPE_MAXMEMORY`, `allkeys-lru`). Cùng mô hình ở dev và prod; Sentinel để M5. Cỡ bộ nhớ dedupe ≈ tốc độ gửi × `CID_COMMITTED_TTL` × ~150B × 1.5 (10K tin/s × 15 phút ≈ 9M key ≈ 2GB; mặc định dev 512mb đủ ~2.3K tin/s, vượt thì LRU bỏ bản ghi committed cũ nhất, cửa sổ chống trùng ngắn lại) | Một Redis chung; dedupe dùng `volatile-ttl` | Key cid (~9M ở 10K tin/s) không được phép làm đói bộ nhớ hay đẩy slot lease ra ngoài. `allkeys-lru` bỏ bản ghi committed cũ nhất trước; `volatile-ttl` sẽ bỏ key pending 10s trước và phá đặt chỗ giữa các core. Dedupe chết thì core đã tự suy giảm về LRU qua redisguard. `pubwm` và `active` ở lại state vì `ActivityMarks` ghi cả hai trong một Lua script |
| D45 | Redis bắt buộc AUTH. Mật khẩu lấy từ `.env` (`REDIS_PASSWORD`, `REDIS_DEDUPE_PASSWORD`) qua compose secrets: script khởi động Redis đọc `/run/secrets/…`, ghi `requirepass` vào file conf 0600 của user redis rồi `exec docker-entrypoint.sh redis-server <conf>`; core đọc `REDIS_PASSWORD_FILE`/`REDIS_DEDUPE_PASSWORD_FILE` (file thắng biến thường). `redis-cli` tay dùng `make redis-cli INSTANCE=state\|dedupe ARGS=…`, healthcheck đặt `REDISCLI_AUTH` từ file ngay trong lệnh | Mật khẩu trong `environment`/`REDISCLI_AUTH` hoặc `--requirepass` | Mật khẩu không bao giờ nằm trong argv, `docker inspect`, dòng lệnh `make` in ra hay log (core chỉ log `redis_auth=true\|false` và xoá hai mật khẩu khỏi mọi dòng log). Biến môi trường hiện ở `docker inspect`, nên chỉ dùng biến thường cho lần chạy ngoài compose và cho tools (`-redis-password` hoặc `REDIS_PASSWORD`) |

## 15. Câu hỏi còn mở

- Ngưỡng dung lượng cho một replica set và chính sách lưu trữ/archive khi vượt ngưỡng — chốt sau PoC R1.
- Hạ tầng production tự vận hành hay dùng dịch vụ managed — chưa chặn Phase 1.
- Timeline và nhân sự Phase 1 — cần khi viết implementation plan.
