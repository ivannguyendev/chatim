# App `core`

Core giữ room, member, tin nhắn và tương tác: cấp seq, lưu MongoDB, phát event lên NATS JetStream. Mention App (`modules/mention`) chạy nhúng trong core và đọc chung config. Cơ chế: [architecture.md](../architecture.md), [module 01–10](../modules/README.md), [danh mục event](../events.md).

## Biến môi trường

Toàn bộ đọc qua `apps/core/internal/config`; thiếu thì dùng giá trị mặc định, sai định dạng hoặc vi phạm luật khởi động thì `Load()` báo gộp mọi lỗi và core không chạy.

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
| `REDIS_DEDUPE_ADDR`, `REDIS_DEDUPE_DB` | `chatim-redis-dedupe:6379` / `0` | Redis dedupe: key `chatim:cid:*`, `request_id` của `AddMembers` `chatim:req:*` (D99) và của `CreateRoom` `chatim:req:create:*` (D118) (D44) |
| `REDIS_DEDUPE_PASSWORD`, `REDIS_DEDUPE_PASSWORD_FILE` | rỗng (không AUTH) | Như `REDIS_PASSWORD`, cho Redis dedupe |
| `NATS_URL` | `nats://chatim-nats:4222` | |
| `CORE_CONNECT_TIMEOUT` | `10s` | Timeout nối Mongo/Redis/NATS lúc khởi động |
| `CORE_REQUEST_DEADLINE` | `3s` | Deadline phía server cho mỗi unary RPC (D42), cũng là group deadline của actor |
| `CORE_SLOW_RPC` | `500ms` | RPC chạy lâu hơn mức này được log là chậm (interceptor log của `pkg/grpcserver`); phải dương |
| `CORE_QUEUE_WAIT`, `CORE_MAX_INFLIGHT` | `25ms` / `2048` | Giới hạn chờ và số RPC đang xử lý đồng thời (load shedding) |
| `CORE_DRAIN_DELAY` | `2s` | Chờ trước khi gRPC graceful stop, để LB ngừng gửi request mới |
| `CORE_GRPC_SHUTDOWN` | `5s` | Hạn graceful stop của gRPC server |
| `CORE_PUBLISHER_DRAIN` | `5s` | Hạn đẩy hết hàng đợi publisher và chờ `PublishAsyncComplete` lúc dừng; hết hạn thì bỏ phần còn lại (D49, D50) |
| `CORE_SHUTDOWN_BUDGET` | `30s` | Tổng ngân sách toàn bộ chuỗi dừng (D39); `Load()` từ chối nếu các mốc dừng cộng lại không nhỏ hơn. Mặc định các mốc là 29.7s (gồm 1s của sweeper, `max(PROJECTION_FETCH_WAIT, MENTION_FETCH_WAIT)` + 1s = 4s của consumer `projections` và consumer `mention` dừng song song, 500ms + 1s = 1.5s của consumer phiếu hẹn (lần fetch dở không huỷ được: đọc tới khi server hết hạn, chậm nhất thêm 1s nếu mất tín hiệu 408), `2 × REDIS_OP_TIMEOUT` để cid batcher gửi hết Commit và 1s để hàng xoá cờ xả sau publisher), nên tăng mốc nào cũng phải tăng ngân sách này và `stop_grace_period` của compose (33s) |
| `CID_PENDING_TTL`, `CID_COMMITTED_TTL` | `10s` / `15m` | TTL key chống trùng cid ở Redis (D34); actor nhớ lần ghi chưa rõ theo cid trong `CID_PENDING_TTL` (D119) |
| `REDIS_OP_TIMEOUT`, `REDIS_COOLDOWN` | `100ms` / `1s` | Timeout mỗi lệnh Redis và thời gian chờ trước khi probe lại sau khi suy giảm |
| `CID_BATCH_SHARDS`, `CID_BATCH_MAX_KEYS`, `CID_BATCH_QUEUE` | `4` / `256` / `4096` | cid batcher: số shard (theo slot, tối đa 1024), số key tối đa mỗi lượt Reserve/Commit, hàng đợi mỗi shard (Commit/Abort đầy thì bỏ). Pool Redis dedupe = `2 × CID_BATCH_SHARDS + 4` kết nối, giữ ấm `2 × CID_BATCH_SHARDS` (D58, D60) |
| `FLUSH_SHARDS`, `FLUSH_WINDOW`, `FLUSH_MAX_BATCH`, `FLUSH_QUEUE`, `FLUSH_INSERT_TIMEOUT` | `4` / `2ms` / `256` / `1024` / `1s` | Flusher: số shard, cửa sổ gộp batch, batch tối đa, hàng đợi mỗi shard, timeout insert |
| `ACTOR_MAILBOX`, `ACTOR_IDLE`, `ACTOR_MAX_GROUP`, `ACTOR_MAX` | `1024` / `5m` / `64` / `100000` | Hàng đợi mỗi actor, thời gian nghỉ trước khi tự dừng, số lệnh gộp 1 nhóm, số actor tối đa 1 core |
| `PUB_SHARDS`, `PUB_QUEUE`, `PUB_MAX_PENDING`, `PUB_ACK_TIMEOUT` | `4` / `1024` / `256` / `2s` | Publisher JetStream: sharding theo slot, hàng đợi mỗi shard, số publish đang chờ ack mỗi shard (nats.go giới hạn `2 × shards × max pending`), timeout ack; mỗi shard phát theo thứ tự nhận, không chờ ack (D50) |
| `EVT_STREAM`, `EVT_SUBJECT_ROOT`, `EVT_LIVE_ROOT`, `EVT_STREAM_REPLICAS`, `EVT_STREAM_MAX_AGE`, `EVT_STREAM_DUPLICATES` | `CHATIM_EVT` / `evt` / `live` / `1` / `168h` / `5m` | Cấu hình stream `CHATIM_EVT` và RePublish sang `live.*`; `EVT_STREAM_DUPLICATES` phải dài hơn `SWEEP_MIN_AGE + SWEEP_INTERVAL` |
| `TIMER_STREAM`, `TIMER_SUBJECT_ROOT` | `CHATIM_TIMER` / `timer` | Stream phiếu hẹn WorkQueue (D111, M3.1): phiếu đặt ở `{root}.arm.{room}.{op}` hoặc `{root}.arm.{room}.{thread}.{seq}.{counter}.{op}`, tới hạn thì sang `{root}.due`; một durable consumer `timer-due` dùng chung mọi core (fetch 64/500ms, `AckWait` 30s), replicas = `EVT_STREAM_REPLICAS`, `MaxAge` 2h, chống trùng 2m. Tên và gốc subject phải khác stream event. Consumer dừng trong 500ms + 1s (1,5s) |
| `SLOT_TICK`, `SLOT_HEARTBEAT_TTL`, `SLOT_LEASE_TTL`, `SLOT_HOOK_TIMEOUT` | `1s` / `5s` / `10s` / `Tick/2` | Nhịp slot manager; `HookTimeout` suy ra từ `SLOT_TICK` nếu không đặt riêng (D26) |
| `STATE_CLEAR_FLUSH`, `STATE_CLEAR_BATCH`, `STATE_CLEAR_QUEUE`, `STATE_CLEAR_TIMEOUT` | `2s` / `16384` / `65536` / `5s` | Hàng xoá cờ `_state` (M3.1, D141+): sau PubAck OK publisher đưa doc vào hàng, hàng gom và xoá theo lô `STATE_CLEAR_FLUSH` sau doc đầu của lô hoặc khi đủ `STATE_CLEAR_BATCH`; mỗi lần xoá có hạn `STATE_CLEAR_TIMEOUT`. Cả lô xếp theo `_id` trước rồi mới chia: các seq liền nhau đã ack của một room thành một lệnh `UpdateMany` theo khoảng, seq lẻ là `UpdateOne`, mỗi `BulkWrite` tối đa 500 lệnh và không cắt đôi khoảng nào. Hàng đầy thì bỏ lượt xoá (đếm `state_clear_dropped_total`), doc giữ cờ và sweeper phát lại, không chặn lệnh. Dừng sau publisher, xả trong 1s |
| `READ_EVENT_WINDOW` | `1s` | Cửa sổ gom vị trí đã đọc (M4 phần 3): `MarkRead`/`MarkUnread` ghi doc rồi đưa vị trí vào hàng `event/readbatch`; mỗi cửa sổ core phát một event `read_positions` mỗi room (mỗi user một dòng, bản `read_ver` lớn nhất, tối đa 1.000 dòng mỗi event). Hợp lệ 100ms..3s. Đóng ngay trước bước xả publisher, trong cùng hạn `CORE_PUBLISHER_DRAIN` |
| `SWEEP_INTERVAL`, `SWEEP_MIN_AGE`, `SWEEP_LOOKBACK` | `2s` / `15s` / `60s` | Sweeper trên core giữ slot 0: mỗi `SWEEP_INTERVAL` quét doc còn cờ `_state` cũ hơn `SWEEP_MIN_AGE`, từ mốc lần trước lùi `SWEEP_LOOKBACK`, dựng lại event từ doc hiện tại và phát lại qua JetStream client riêng (D147). Luật khởi động: `SWEEP_MIN_AGE` phải dài hơn `PUB_ACK_TIMEOUT + STATE_CLEAR_FLUSH + STATE_CLEAR_TIMEOUT + READ_EVENT_WINDOW` (mặc định 2s + 2s + 5s + 1s = 10s < 15s); `SWEEP_MIN_AGE + SWEEP_INTERVAL` phải ngắn hơn `EVT_STREAM_DUPLICATES`; `SWEEP_LOOKBACK` phải dài hơn cả `CORE_REQUEST_DEADLINE + 10s` lẫn `SWEEP_MIN_AGE + 10s`. Dừng trong 1s |
| `SWEEP_BATCH`, `SWEEP_RATE` | `500` / `5000` | Cỡ trang mỗi lần đọc (1–1000) và số event phát lại tối đa mỗi giây (D154); client JetStream của sweeper cho tối đa `2 × SWEEP_BATCH` publish đang bay, timeout `PUB_ACK_TIMEOUT` |
| `PROJECTION_FETCH_BATCH`, `PROJECTION_FETCH_WAIT` | `8192` / `3s` | Một consumer durable `projections` trên `CHATIM_EVT` (lọc `evt.>`, ack từng tin, `AckWait` 30s, `MaxAckPending` = 4 × lô; ba durable cũ `projection-*` bị xoá lúc khởi động): số event tối đa mỗi lô (1–8192) và thời gian chờ lô (tối đa 3s). Cửa sổ 3s gom nhiều event mỗi room vào một lệnh ghi `rooms` (2K event/s ≈ 6K event một lô, nên cửa sổ đóng lô chứ không phải cỡ lô), đưa CPU Mongo về ngang engine cũ (D160). Khi dừng, core đợi lần fetch dở hết hạn rồi trả các tin nhận được bằng `Nak(0)`, không đánh rơi tin nào; mốc dừng là `PROJECTION_FETCH_WAIT` + 1s. Chỉ core giữ slot 0 fetch (xét lại mỗi `SLOT_TICK`); mỗi lô chia cho `room_activity` (13 kind, một `BulkWrite` `$max` mỗi room-thread) và `reply_index` (`msg_created`/`msg_deleted`). Ổn hay room/doc không còn thì `Ack`, không decode được thì `Term`, lỗi thì `Nak` riêng tin đó theo bậc thang 5s, 30s, 2m, 10m (cố định); lần giao thứ 6 trở đi đếm vào `projection_stuck_total`. Mất slot 0 thì làm xong lô đang ghi, `Nak(0)` lô vừa nhận. `PROJECTION_ACTIVITY_FETCH_*` cũ bị bỏ qua. Dừng trong `PROJECTION_FETCH_WAIT` + 1s (mặc định 4s) |
| `MESSAGE_LOCKED_KINDS` | (rỗng) | Danh sách loại tin, cách nhau bằng dấu phẩy, mà policy mặc định không cho ai sửa/xoá, kể cả tác giả; tên hợp lệ: `text`; tên lạ thì core không khởi động (D87) |
| `REACTION_EMOJIS` | `👍,❤️,😂,😮,😢,🙏` | Danh sách emoji cố định được phép react, cách nhau bằng dấu phẩy, giữ thứ tự; để trống thì dùng mặc định. Core không khởi động nếu danh sách rỗng, có emoji không hợp lệ (`ValidateEmoji`), có emoji lặp, hoặc quá 100 emoji; lỗi nêu tên biến và emoji sai. `ReactMessage` với emoji ngoài danh sách trả `INVALID_ARGUMENT`; gỡ reaction (emoji rỗng) luôn được. Frontend đọc danh sách qua `GetReactionSettings` và chỉ cho chọn trong đó (D95) |
| `PIN_LIMIT` | `50` | Số tin ghim tối đa mỗi room (1–1000), chính xác vì pv dày; ghim lại tin đã ghim vẫn thành công (D92) |
| `MEMBER_BATCH_MAX` | `500` | Số người tối đa mỗi lệnh `CreateRoom` hoặc `AddMembers` (đếm sau khi bỏ trùng, 2–1000); quá thì `INVALID_ARGUMENT`. Core không giới hạn tổng số member của room (D107) |
| `MENTION_TARGETS_MAX` | `50` | Số đích mention tối đa mỗi tin và mỗi lần sửa tin (đếm sau khi bỏ trùng, yêu cầu A6); quá thì `INVALID_ARGUMENT`. `@all` không tính là đích, nó qua policy `mention_all` |
| `MEMBER_COUNT_CHECK_DELAY` | `5s` | Hạn của phiếu hẹn đếm lại `member_count` (message schedule NATS trên stream `CHATIM_TIMER`, D102, D111): lệnh member có thể đổi số đặt phiếu trước khi ghi và xoá phiếu sau `$inc`; lỗi hay core chết ở giữa thì phiếu bật sau hạn này (làm tròn lên giây). Phải dài hơn `CORE_REQUEST_DEADLINE`; không thêm bước dừng |
| `MESSAGE_COUNT_CHECK_DELAY` | `5s` | Hạn của phiếu hẹn đếm lại số trên tin (`rx` reaction, `rc` trả lời; D113): ghi tương tác đặt phiếu trước, `$inc` số rồi xoá phiếu; lỗi hay core chết ở giữa thì phiếu bật sau hạn này (làm tròn lên giây) và consumer phiếu hẹn (`count_repair`) đếm lại + CAS `v`. Phải dài hơn `CORE_REQUEST_DEADLINE`; không thêm bước dừng |
| `ROOM_LIST_JOIN_MAX` | `1500` | Điểm chuyển đường của `ListMyRooms` (≥ 1, không phải giới hạn): user có ≤ số room này thì một aggregate `members → rooms` ở DB, nhiều hơn thì đọc từng bước rồi xếp ở core; hai đường cùng kết quả, quyết định cache 60s theo user (D136) |
| `ROOM_TAIL_CACHE_BYTES` | `268435456` (256MB) | Số byte tối đa của cache đuôi room trong RAM mỗi core (LRU theo byte, ≥ 1048576 = 1MB; một đuôi 300 tin + 10 preview ≈ 14KB); entry hết hạn sau 30s. Cache ảnh chụp danh sách (64MB, TTL 30s) và cache chọn đường (100.000 user, TTL 60s) có cỡ cố định, không env |
| `ROOM_INFO_CACHE_ENTRIES` | `100000` | Số room giữ thông tin (tên, loại, số member…) trong cache RAM mỗi core (≥ 1, TTL 60s); `room_updated` và `member_count_changed` trên core NATS xoá entry của room ở mọi core |

### Mention App

| Biến | Mặc định | Ghi chú |
|---|---|---|
| `MENTION_ENABLED` | `true` | Bật Mention App nhúng trong core (D163). `false`: không tạo consumer `mention`, không đăng ký `MentionService`, không watch, không ghi mention, không thông báo |
| `MENTION_FETCH_BATCH`, `MENTION_FETCH_WAIT` | `512` / `250ms` | Lô của consumer `mention` mỗi core (queue group, mọi core cùng kéo, D171); `MENTION_FETCH_WAIT` ≤ `PROJECTION_FETCH_WAIT` vì hai consumer dừng song song |
| `MENTION_WORKERS` | `8` | Số event một core xử lý cùng lúc (≥ 1); event chưa xong được gửi `InProgress` mỗi 10s |
| `MENTION_MAX_ACK_PENDING` | `16384` | `MaxAckPending` của durable `mention`, tổng cho mọi core (≥ `MENTION_FETCH_BATCH`) |
| `MENTION_NOTIFY_MAX_AGE` | `5m` | Event cũ hơn thì vẫn ghi mention nhưng không gửi thông báo (D169); ≤ cửa sổ bỏ trùng 6 phút của stream gói |
| `MENTION_WS_PUBLISH_STREAM`, `MENTION_WS_PUBLISH_ROOT` | `CHATIM_WS` / `websocket.publish` | Stream và subject gói `WsPublish` `{root}.{tenant}` (giữ 24h, bỏ trùng 6 phút, cấu hình trong `pkg/wspublish`); stream phải khác `EVT_STREAM`/`TIMER_STREAM`, root không bắt đầu bằng subject root khác. Số bản sao theo `EVT_STREAM_REPLICAS` |
| `MENTION_NOTIFY_TARGETS_PAGE` | `1000` | Số người nhận tối đa mỗi gói `WsPublish` (1..5.000); trần 5.000 giữ gói dưới 1MB kể cả khi `max_payload` của NATS bị hạ |
| `MENTION_HIDE_READ_TENANTS` | (rỗng) | Danh sách tenant id, cách nhau bằng dấu phẩy, mà `ListMentions` của Mention App ẩn mention đã đọc (giờ mention không muộn hơn tin tại `read_seq` của room); tenant khác thấy mọi mention kèm cờ `read`; tenant id sai định dạng thì core không khởi động (D115, nay thuộc config Mention App) |
| `MENTION_ROOM_GROUPS_SYNC_MAX` | `5000` | Room tới cỡ này thì `ListRoomGroups` dựng tập nhóm ngay khi được hỏi, lớn hơn thì dựng ở nền (D172); không phải giới hạn dữ liệu |
| `MENTION_ROOM_GROUPS_TTL`, `MENTION_ROOM_GROUPS_REBUILD` | `60s` / `10m` | Tập nhóm của room nhỏ hết hạn sau TTL (và quên khi có `member_added`/`member_removed`); room lớn dựng lại ở nền khi có người hỏi và tập cũ hơn `REBUILD` |

`CORE_GOGC` (mặc định 100) chỉ compose đọc lúc `make core-up` và đưa vào `GOGC` của core, dùng khi đo; không có trong `.env.example`.

## Kết nối và bí mật

- **Hai Redis (D44, D45).** State `chatim-redis` chỉ cho slot manager (`chatim:core:*`, `chatim:cores`, `chatim:slot:*`, pub/sub `chatim:slots:changed`), AOF everysec, `noeviction`, một primary (Lua nhiều khoá nên không Redis Cluster). Dedupe `chatim-redis-dedupe` giữ `chatim:cid:*`, `chatim:req:{room}:{user}:{request_id}` (`AddMembers`) và `chatim:req:create:{tenant}:{user}:{request_id}` (`CreateRoom`), không lưu đĩa, `REDIS_DEDUPE_MAXMEMORY` (512mb), `allkeys-lru`; Redis này chết thì chống trùng lùi về LRU trong RAM, event không phụ thuộc nó.
- **Client Redis dedupe (D60).** `PoolSize` = `MaxActiveConns` = `2 × CID_BATCH_SHARDS + 4`, `MinIdleConns` = `2 × CID_BATCH_SHARDS`, `MaxRetries -1` (Reserve không idempotent), `DialerRetries 1`. Client state giữ pool 4 và retry mặc định (đổi nó đụng lease, cần chạy R5). Cả hai đặt `DisableIdentity`. go-redis vẫn bỏ kết nối sau timeout để không đọc nhầm trả lời đến muộn.
- **Không bao giờ `SCAN` hay `KEYS`**: key cid lấp đầy keyspace.
- **Mật khẩu.** Cả ba Redis bắt AUTH. Compose lấy mật khẩu từ `.env` qua secret; core đọc `REDIS_PASSWORD_FILE`/`REDIS_DEDUPE_PASSWORD_FILE`, tool đọc `-redis-password` hoặc `REDIS_PASSWORD`. Mật khẩu không bao giờ nằm ở argv, `docker inspect`, dòng make in ra hay log. Container không có `REDISCLI_AUTH`: dùng `make redis-cli [INSTANCE=dedupe|presence] ARGS=…`.
- **Mongo (D46).** Secret `mongo_password` lấy từ `MONGO_ROOT_PASSWORD`; core nhận `MONGO_URI` không credential cùng `MONGO_USER` và `MONGO_PASSWORD_FILE` (`MONGO_AUTH_SOURCE` mặc định `admin`). URI có credential vẫn chạy (itest, chạy ngoài compose) nhưng không đi cùng `MONGO_USER`. mongosh không bao giờ nhận `-p`: script xác thực trong `--eval` bằng `db.getSiblingDB("admin").auth(user, require("fs").readFileSync("/run/secrets/mongo_password", "utf8").trim())`.
- Mọi dòng log đi qua handler che credential của `MONGO_URI`, `NATS_URL`, mật khẩu Mongo và hai mật khẩu Redis.

## Cổng và admin

- gRPC `CORE_GRPC_ADDR` (`:9000`); compose publish `core-1` ở `127.0.0.1:9001`, `core-2` ở `127.0.0.1:9002`, chỉ loopback vì caller còn được tin qua metadata `x-chatim-tenant`/`x-chatim-user` tới khi có mTLS (M5).
- Admin `CORE_ADMIN_ADDR` (`:9090`, không publish): `/healthz`, `/readyz`, `/metrics`, pprof. `/app probe` là healthcheck của container.

## Metric và alert

Mọi metric của core có tiền tố `chatim_core_`, của Mention App `chatim_mention_`; đăng ký pull-based trên registry riêng (`pkg/metrics`, wiring ở `apps/core/internal/app/metrics_wiring.go`, `event_metrics_wiring.go`). Bảng cam kết và detector: [architecture.md §8](../architecture.md#8-cam-kết-đúng-đắn-guarantees--detectors).

- Đường ghi: `publish_dropped_total{reason}`, `redis_degraded{client}`, `cid_settle_dropped_total`, `cid_pending_elsewhere_total`, `grpc_load_shed_total`, `room_yields_total`.
- Event chờ phát: `state_clear_cleared_total`, `state_clear_dropped_total`, `state_clear_failures_total`, `publish_state_track_full_total`, `event_pending_oldest_seconds{collection}` (chỉ core giữ slot 0 có giá trị), `sweep_republished_total{collection}`, `sweep_dropped_total{collection}`, `sweep_publish_failures_total`, `sweep_failures_total`, `sweep_passes_total`.
- Projection: `projection_lag_seconds{projection}` (tính theo `ts` nghiệp vụ, nên tin sweeper phát lại hiện lag cao), `projection_failures_total`, `projection_dropped_total`, `projection_stuck_total`.
- Phiếu hẹn: `timer_failures_total`, `timer_dropped_total`, `counter_repaired_total{counter="reactions"|"replies"|"members"}`.
- Cache và watcher: `member_cache_forgets_total`, `member_watch_malformed_total`, `room_cache_hits_total`/`room_cache_misses_total{cache=info|tail|snapshot|path}`, `room_tail_cache_bytes`, `room_watch_forgets_total`, `room_watch_malformed_total` (chỉ để xem tốc độ, không alert). Index: `index_outdated{collection}`. `mongo_oplog_window_seconds` là NaN khi không đọc được oplog (cần quyền đọc `local`).
- Mention App: `lag_seconds`, `failures_total`, `dropped_total`, `stuck_total`, `notified_total`, `retracted_total`, `notify_failures_total`, `notify_skipped_total{reason="stale"|"superseded"}`, `room_groups_hits_total`, `room_groups_misses_total`, `room_groups_rebuild_failures_total`, `room_groups_watch_forgets_total`, `room_groups_watch_malformed_total`.

16 luật alert của core và Mention App trong `deploy/prometheus/alerts.yml` (kiểm bằng `make alerts-check`): `ChatimOplogWindowShort`, `ChatimOplogWindowCritical`, `ChatimOplogWindowUnknown`, `ChatimEventsDropped`, `ChatimRedisDegraded`, `ChatimCIDSettleDropped`, `ChatimCIDPendingElsewhere`, `ChatimSeqContention`, `ChatimCounterRepairSurge`, `ChatimEventPendingStuck` (cờ cũ nhất > 60s trong 5 phút), `ChatimProjectionLagging` (> 10s trong 5 phút), `ChatimProjectionFailing` (`projection_stuck_total` tăng trong 15 phút), `ChatimSweeperIdle` (10 phút không có lượt quét), `ChatimTimerFailing`, `ChatimMentionLagging` (> 10s trong 5 phút), `ChatimMentionFailing` (`chatim_mention_stuck_total` tăng trong 15 phút). Cam kết mới phải có metric và luật trong cùng PR.

## Vòng đời

Thứ tự khởi động và dừng: [architecture.md §7.2](../architecture.md#72-vòng-đời-core-lifecycle--graceful-shutdown). Kế hoạch dừng mặc định 29,7s trong `CORE_SHUTDOWN_BUDGET` 30s (log `core shutting down` kèm `stop_plan`); tăng mốc dừng nào cũng phải tăng budget và `stop_grace_period` của compose (33s). gRPC chỉ mở sau khi khởi động sạch.

## Lệnh vận hành

Các lệnh `/app` dùng config và secret của core, in lỗi và thoát khác 0 khi thất bại.

    docker exec chatim-core-1 /app resync -from 2026-10-05T08:00:00Z -to 2026-10-05T09:00:00Z [-tenant T] [-room ID] [-rate 500] [-dry-run]
    docker exec chatim-core-1 /app recount -room ID [-dry-run]
    docker exec chatim-core-1 /app indexes [-dry-run|-apply|-drop-old]

### `/app indexes` (D191)

Đổi index chỉ bằng lệnh này. `-dry-run` (mặc định) in index thiếu, đang build, cũ, lạ và xung đột của core và Mention App; `-apply` tạo bản thiếu bên cạnh bản cũ, không xoá; `-drop-old` xoá bản cũ chỉ khi mọi bản thay đã build xong; có xung đột tuỳ chọn thì từ chối toàn bộ.

Khởi động core chỉ build index trên collection rỗng; collection có dữ liệu mà thiếu hoặc lệch index thì core log `warn` "index outdated, run /app indexes" và đặt gauge `chatim_core_index_outdated{collection}` = 1, không tự build hay xoá. Chạy `-dry-run`, rồi `-apply`, chờ build xong (dry-run hết "building"), rồi `-drop-old`.

### `/app recount` (D102)

Đếm lại `member_count` của một room, in `room=… stored=… counted=…`. Không có `-dry-run` thì ghi CAS theo `member_count_ver` (kèm bit 2 của `_state` trên `rooms`), publish `member_count_changed`, PubAck OK thì xoá cờ, lỗi thì để cờ cho sweeper. Trượt CAS thì thoát lỗi, chạy lại. Lệnh luôn ghi, kể cả khi số đã đúng.

### `/app resync` (D155)

`/app resync` dùng config và secret của core, chỉ nối Mongo (đọc doc, bật cờ) và NATS (đặt phiếu hẹn) (M3.1, D155). Chọn room như trước: theo `activity_bucket` từ `-from` hoặc tạo trong khoảng, hoặc một room với `-room`. Với từng room, resync đọc doc hiện tại trong khoảng và bật lại bit `_state` bằng `EventStates.Mark` (doc đang chờ giữ `_state_at` cũ, không thì lấy giờ hiện tại; doc không còn hoặc đã đổi version thì bỏ qua), theo lô tối đa 200 doc cùng collection, nhịp `-rate` doc/giây: `rooms` bit 1 nếu tạo trong khoảng, bit 2 nếu `member_count_ver ≥ 1`, bit 4 nếu `info_ver ≥ 2` và sửa trong khoảng; tin của timeline chính bit 1; `message_edits` mỗi fact `ver ≥ 1`; `message_interactions` reaction và bookmark ở `ver` hiện tại; `pin_actions` ở `pin_ver`; `members` theo `last_change_at`: bit 1 ở `ver`, thêm bit 2 khi `read_ver ≥ 1`, bit 4 khi có `cleared_at`, bit 8 khi `preference_ver ≥ 1`; `hidden` bit 1. Sweeper slot 0 phát lại các doc này từ trạng thái hiện tại và các projection chạy lại (idempotent). Mỗi tin có reaction và mỗi tin cha có doc reply trong khoảng nhận một phiếu hẹn đếm lại (`reactions`/`replies`, D111). Kết quả in số doc theo collection và bit, ví dụ `resync rooms=1 marked rooms.created=1 rooms.member_count=1 rooms.info=0 messages=3 message_edits=1 … hidden=1 count_checks=2 dry_run=false`; `-dry-run` chỉ đếm, không bật cờ và không đặt phiếu. Giới hạn: chưa có thread; chỉ trạng thái cuối được phát lại (bản trung gian, ví dụ `member_count_changed` của version cũ, thì không); stream bỏ bản trùng theo `Nats-Msg-Id` trong `EVT_STREAM_DUPLICATES` (5m), kể cả sau khi purge stream, nên event cùng id phát trong cửa sổ đó bị bỏ, chạy resync sau khi cửa sổ qua (stream tạo lại thì không bị); chọn `-from` rộng hơn khoảng mất vài phút; room chỉ có reaction/ghim/đổi member/ẩn tin trong khoảng mất phải chạy với `-room`. Chạy `-dry-run` trước.

## `corecli`

Image `chatim/corecli:dev` do `make e2e` build; chạy trong mạng compose, `-tenant`/`-user` chọn người gọi.

    docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev <lệnh> [cờ]

| Nhóm | Lệnh |
|---|---|
| Room | `open-dm -other U` (mở hoặc lấy lại DM); `create-room -name N -members a,b [-request-id R]` (cùng `-request-id` = cùng group) |
| Tin | `edit -room ID -seq N -base V -text "..."`, `delete -room ID -seq N -base V`, `hide -room ID -seq N`, `clear -room ID`, `edits -room ID -seq N [-after V]` |
| Tương tác | `react -room ID -seq N -emoji "👍"` (`-emoji ""` gỡ), `pin -room ID -seq N`, `unpin -room ID -seq N` |
| Member | `add-members -room ID -users a,b [-request-id R]`, `remove-member -room ID -target U`, `leave -room ID`, `set-role -room ID -target U -role owner\|admin\|member`, `set-priority -room ID -target U -priority N`, `read -room ID [-seq N]` (0 = tin cuối), `unread -room ID -seq N` |
| Mention App | `set-user-groups -user U -groups g1,g2 -ver N` (`-groups` rỗng = không nhóm; `-ver` phải lớn hơn bản đang giữ), `set-groups -group G -name "Tên" -ver N [-inactive]`, `room-groups -room ID` |
| Event | `watch -room ID [-tenant T]`: in event live của room (`live.{t}.*.{rid}.>`) |
