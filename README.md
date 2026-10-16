# chatim

Hạ tầng chat dùng chung (CPaaS nội bộ) cho nhiều sản phẩm: quản lý room, tin nhắn, tương tác realtime hiệu năng cao; lấy lịch sử cực nhanh ở bất kỳ vị trí nào; phát event mạnh tới các app khác kết nối vào. Multi-tenant về mặt logic.

> Trạng thái: **M0–M1 (nền tảng + PoC) và M2a (core: CreateRoom/SendMessage/GetHistory qua gRPC, chống trùng cid, publish JetStream, 2 core trong compose) đã xong trên máy dev**. M2a.1 (bỏ `pts` toàn room, id event tự nhiên, publisher chỉ còn hàng đợi trên cơ chế async của nats.go, event best-effort — D47–D51) xong trên máy dev. M2a.2 (reconciler trên core giữ slot 0 đọc change stream của `messages`, publish bù event chưa có mark đã ack — D52) xong trên máy dev. M2a.3 (perf đường ghi: gom lệnh chống trùng cid giữa các room, ack trước Commit, client Redis dedupe pool nhỏ và ấm, mark đã ack gom trong cửa sổ 10ms — D58–D60) xong trên máy dev; M2a.2 và M2a.3 đã merge cùng lúc (PR #11). Rà soát cơ chế hệ thống đã chốt (2026-10-05): [thiết kế kiến trúc](docs/architecture.md); M2b.0, M2b.1 (effect engine: reader → work stream → worker mọi core, `room_created`, room activity, `/app resync`), M2b.2 (sửa/xoá theo fact `message_edits` + projection, ẩn/clear phía người đọc, lịch sử sửa), M2b.3 (reaction một emoji mỗi user + số đếm recount, ghim theo fact `pin_actions` + projection) và M2b.4 (member lớp tập, transaction đổi owner, `member_count` `$inc` + phiếu hẹn sinh tồn, vị trí đọc, subject theo loại dữ liệu, tên field đầy đủ) xong và đã merge vào `main` (PR #12, 2026-10-08). M2c (tương tác với tin chung `message_interactions`: reaction, trả lời có trích, bookmark; mention theo đích + `ListMentions`; mọi số trên tin `$inc` + phiếu hẹn sinh tồn; forward; `OpenDirectRoom` + `CreateRoom` chỉ group có `request_id`) đã merge vào `main` (PR #14, 2026-10-13). M3 (API truy vấn của core: danh sách hội thoại `ListMyRooms`/`GetRooms` do core xếp và đếm unread, cài đặt hội thoại, `UpdateRoom`, `GetHistory` neo `AROUND`, `GetMessages`, `ListThreads`, `GetReactions`, `ListPins`, `ListMembers`, tin hệ thống; [tóm tắt](docs/plans/2026-10-12-m3-core-query-apis-summary.md)) xây xong trên nhánh `feat/m3` (2026-10-15), chờ PR. M3.1 (bỏ effect engine: mỗi lệnh ghi doc kèm cờ `_state` chờ phát, publish ngay sau ack và xoá cờ sau PubAck, sweeper trên core giữ slot 0 phát bù cờ còn sót, 3 projection nghe `CHATIM_EVT` không delay, phiếu hẹn trên `CHATIM_TIMER`, `/app resync` bật lại cờ — D141–D159; [tóm tắt](docs/plans/2026-10-16-m3-1-state-driven-events-summary.md)) xây xong trên nhánh `feat/m3.1` chồng lên `feat/m3` (2026-10-16), PR sau M3; danh sách hội thoại cập nhật trong khoảng 0–0,5s thay vì ~5s. Hai chủ đề hoãn từ M3 ([open topics](docs/plans/2026-10-15-m3-open-topics.md)): chặn đầu hàng của engine đã giải quyết ở M3.1, sharding còn chờ owner thảo luận; tiếp theo là M4 theo [roadmap](docs/roadmap.md); quyết định go/no-go chờ PoC prod-like. Kết quả đo: [docs/poc/README.md](docs/poc/README.md). Bản đồ code: [INDEXES.csv](INDEXES.csv).

## Kiến trúc

Monorepo Go, mỗi app một container.

| App | Vai trò | Phase |
|---|---|---|
| `core` | Room, member, message, tương tác; cấp seq; lưu MongoDB; phát event best-effort lên NATS JetStream (id event tự nhiên, thứ tự từng room); sửa/xoá tin là fact `message_edits` + projection `messages`, ẩn/clear theo người đọc; tương tác với tin chung `message_interactions` (reaction một emoji mỗi user, bookmark, trả lời có trích ở timeline chính), mention theo đích trong `mentions`, forward; số trên tin (`rx` theo emoji, `rc` số trả lời) `$inc` + phiếu hẹn đếm lại; ghim là fact `pin_actions` + projection `rooms.pins`; DM qua `OpenDirectRoom` + sổ `room_dms`, group qua `CreateRoom` có `request_id`; member mỗi (room, user) một doc, lệnh đụng owner trong một transaction, `member_count` `$inc` + phiếu hẹn đếm lại, vị trí đọc; event trên subject theo loại dữ liệu `evt.{t}.{loại}.{rid}.{kind}` với loại `room`, `member` hoặc `message` (RePublish sang `live.{t}.{loại}.{rid}.evt.{kind}`; nghe mọi event của room: `live.{t}.*.{rid}.>`), core không chọn người nhận; mỗi lần ghi bật cờ `_state` trên doc, publisher xoá cờ sau PubAck, sweeper trên core giữ slot 0 phát lại event của doc còn cờ (và áp projection sửa/xoá/ghim còn thiếu), ba consumer projection trên `CHATIM_EVT` chạy ở mọi core (room activity, dựng doc trả lời và mention, mention đã đọc), consumer phiếu hẹn sửa số trên tin và số member (M3.1) | 1 |
| `websocket` | WebSocket (gws) cho client SDK; nhận event từ NATS và đẩy realtime | 1 |
| `api`, `events`, `push`, `auth`, `migrator` | Public API, event stream cho app ngoài, push notification, xác thực, migrate/dual-write từ hệ thống cũ | Sau |

Hạ tầng: MongoDB (replica set), Redis, NATS JetStream.

## Cấu trúc

```
apps/core/               # main.go (gọi app.Main); itest/: test tích hợp cả core (cần infra thật)
  internal/app/          # wiring, vòng đời, lệnh serve/probe/resync/recount
  internal/config/       # đọc + kiểm config lúc boot
  internal/api/          # grpcsrv, view (đường đọc, biên gRPC)
  internal/model/        # domain, pbconv, access (kiểu, chuyển proto, permission hook)
  internal/send/         # actor, dedupe, flush, memberwatch (gửi tin)
  internal/change/       # mutate, ownership, pinproj (lệnh đổi, projection)
  internal/event/        # publish, stateclear, projection, sweep, timer, eventkit, resync (event theo cờ `_state`)
  internal/store/        # port + memstore, mongostore, storetest
  internal/platform/     # slot, metrics, redisguard, testlog (hạ tầng cụm)
pkg/                     # dùng chung: keys, ids, lru, slotmap, apperr, envconfig, resilience, admin, grpcserver, grpcclient, backoff, pb
proto/chatim/v1/         # định nghĩa protobuf (buf) → pkg/pb
tools/                   # corecli, internal/route; poc/: corebench, mongobench, postgresbench, natsbench, wsbench
scripts/                 # e2e.sh, các script chờ hạ tầng sẵn sàng, bench-cell.sh / bench-sample.sh (đo corebench)
deploy/docker/           # 1 Dockerfile, chọn chương trình bằng ARG TARGET
deploy/compose/          # hạ tầng dev
docs/                    # nghiên cứu, thiết kế, plan, kết quả PoC
```

## Chạy hạ tầng dev

Chỉ cần Docker; Go chạy trong container `golang:1.26.9` qua `make`.

    cp .env.example .env        # đổi MONGO_ROOT_PASSWORD, REDIS_PASSWORD, REDIS_DEDUPE_PASSWORD (chữ, số, - hoặc _)
    make infra-up               # mongo rs0 :27117, redis state :6380, redis dedupe :6381, nats :4223 (monitor :8223)
    make redis-cli ARGS="info memory"                      # redis-cli trong container, mật khẩu lấy từ secret
    make redis-cli INSTANCE=dedupe ARGS="dbsize"           # INSTANCE=state (mặc định) hoặc dedupe
    make test                   # go test -race ./... trong container
    make go ARGS="vet ./..."    # lệnh go bất kỳ
    make infra-down             # dừng; make infra-reset để xoá cả dữ liệu

Image chạy cho một chương trình Go bất kỳ: `make image TARGET=tools/poc/natsbench` (distroless, không cần Go trên máy chạy).

Công cụ PoC nằm ở `tools/poc/`, chạy bằng `make poc TOOL=<tên> ARGS="…"` — cách chạy và kết quả: [docs/poc/README.md](docs/poc/README.md).

Một ô đo corebench (tuỳ chọn nghỉ 5 phút + `infra-reset`, latency monitor Redis dedupe, profiler Mongo, số liệu theo giây qua `scripts/bench-sample.sh`, kết quả vào `bin/bench/NAME/`):

    ./scripts/bench-cell.sh base-3k 3000 reset pprof
    CORE_GOGC=200 ./scripts/bench-cell.sh after-3k-gogc200 3000 reset

Compose đọc `CORE_GOGC` (mặc định 100, thành `GOGC` của core) lúc `make core-up`, chỉ để đo; không có trong `.env.example`.

## Lệnh hay dùng

    make proto                  # sinh code từ proto/chatim/v1 (buf generate, ghi vào pkg/pb)
    make buf-lint                # buf lint + buf format -d --exit-code
    make lint                    # golangci-lint run ./...
    make vuln                    # go tool govulncheck ./...
    make itest                   # go test -race -shuffle=on -count=1 ./... với Mongo/Redis/NATS thật (cần infra-up)
    make core-up                 # build image apps/core, chạy core-1 + core-2 (profile app), chờ /readyz healthy
    make core-down               # dừng và xoá core-1, core-2
    make alerts-check            # promtool kiểm deploy/prometheus/alerts.yml (trong Docker)
    make e2e                     # build tools/corecli, chạy scripts/e2e.sh (route theo slot, kill core-1, kiểm tra; phase 3 sửa seq 1, xoá seq 2; phase 4 react seq 3 rồi đổi emoji, ghim seq 4; phase 5 member: DM từ chối, thêm, đổi role, priority, xoá, vị trí đọc, ẩn và clear, owner cuối rời rồi vào lại, kiểm reply và event trên subject `room`/`member`/`message`; phase 6 liên kết: mở DM hai lần ra một room, tạo group hai lần cùng `request_id`, react, hai trả lời + `GetReplies`, chặn xoá tin cha, xoá một trả lời, mention user/nhóm/`@all` + `ListMentions`, `@nhóm`/`@all` gửi trước khi vào group không hiện, forward, bookmark + `ListBookmarks`, 22 event live; phase 7 danh sách hội thoại và API đọc: tin hệ thống, unread sau gửi/đọc/xoá/ẩn, ghim và lưu trữ, trang 2, `GetRooms`, `UpdateRoom`, `AROUND`, `GetMessages`, thread, người react, ghim, member, forward có `skip_unread`/`meta`, 34 event live)
    docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev edit -room ID -seq N -base V -text "..."   # sửa tin; cùng cờ: delete -room ID -seq N -base V, hide -room ID -seq N, clear -room ID, edits -room ID -seq N [-after V]; -tenant/-user chọn người gọi; image do make e2e build
    docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev react -room ID -seq N -emoji "👍"   # đặt/đổi reaction của người gọi (-emoji "" gỡ); pin -room ID -seq N, unpin -room ID -seq N; -tenant/-user chọn người gọi
    docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev add-members -room ID -users a,b [-request-id R]   # thêm người (cùng -request-id = gửi lại đúng một lần); cùng cờ: remove-member -room ID -target U, leave -room ID, set-role -room ID -target U -role owner|admin|member, set-priority -room ID -target U -priority N, read -room ID [-seq N] (0 = tin cuối), unread -room ID -seq N; -tenant/-user chọn người gọi
    docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev open-dm -other U   # mở (hoặc lấy lại) room DM với U; create-room -name N -members a,b [-request-id R] tạo group (cùng -request-id = cùng group)
    docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev watch -room ID [-tenant T]   # in event live của room: live.{t}.*.{rid}.> (subject room, member, message)
    docker exec chatim-core-1 /app resync -from 2026-10-05T08:00:00Z -to 2026-10-05T09:00:00Z [-tenant T] [-room ID] [-rate 500] [-dry-run]   # bật lại cờ `_state` của các doc trong khoảng để sweeper phát lại event từ trạng thái hiện tại (stream mất event, dựng lại projection; D155)
    docker exec chatim-core-1 /app recount -room ID [-dry-run]   # đếm lại member_count của một room, in room=… stored=… counted=…; không -dry-run thì ghi CAS theo member_count_ver (kèm cờ `_state` bit 2) rồi publish member_count_changed, PubAck OK thì xoá cờ, lỗi thì để cờ cho sweeper (trượt CAS → exit lỗi, chạy lại; D102)
    make poc TOOL=corebench ARGS="-rate 5000 -duration 60s -watch 20"   # tải mở-vòng vào cụm core, cần core-up trước

`/app resync` dùng config và secret của core, chỉ nối Mongo (đọc doc, bật cờ) và NATS (đặt phiếu hẹn) (M3.1, D155). Chọn room như trước: theo `activity_bucket` từ `-from` hoặc tạo trong khoảng, hoặc một room với `-room`. Với từng room, resync đọc doc hiện tại trong khoảng và bật lại bit `_state` bằng `EventStates.Mark` (doc đang chờ giữ `_state_at` cũ, không thì lấy giờ hiện tại; doc không còn hoặc đã đổi version thì bỏ qua), theo lô tối đa 200 doc cùng collection, nhịp `-rate` doc/giây: `rooms` bit 1 nếu tạo trong khoảng, bit 2 nếu `member_count_ver ≥ 1`, bit 4 nếu `info_ver ≥ 2` và sửa trong khoảng; tin của timeline chính bit 1; `message_edits` mỗi fact `ver ≥ 1`; `message_interactions` reaction và bookmark ở `ver` hiện tại; `pin_actions` ở `pin_ver`; `members` theo `last_change_at`: bit 1 ở `ver`, thêm bit 2 khi `read_ver ≥ 1`, bit 4 khi có `cleared_at`, bit 8 khi `preference_ver ≥ 1`; `hidden` bit 1. Sweeper slot 0 phát lại các doc này từ trạng thái hiện tại và các projection chạy lại (idempotent). Mỗi tin có reaction và mỗi tin cha có doc reply trong khoảng nhận một phiếu hẹn đếm lại (`reactions`/`replies`, D111). Kết quả in số doc theo collection và bit, ví dụ `resync rooms=1 marked rooms.created=1 rooms.member_count=1 rooms.info=0 messages=3 message_edits=1 … hidden=1 count_checks=2 dry_run=false`; `-dry-run` chỉ đếm, không bật cờ và không đặt phiếu. Giới hạn: chưa có thread; chỉ trạng thái cuối được phát lại (bản trung gian, ví dụ `member_count_changed` của version cũ, thì không); stream bỏ bản trùng theo `Nats-Msg-Id` trong `EVT_STREAM_DUPLICATES` (5m), kể cả sau khi purge stream, nên event cùng id phát trong cửa sổ đó bị bỏ, chạy resync sau khi cửa sổ qua (stream tạo lại thì không bị); chọn `-from` rộng hơn khoảng mất vài phút; room chỉ có reaction/ghim/đổi member/ẩn tin trong khoảng mất phải chạy với `-room`. Chạy `-dry-run` trước.

R5 (soft-ownership khi Redis mất dữ liệu), chạy nhiều lần cho chắc:

    make go ARGS="test -race -count=20 -timeout 20m ./apps/core/internal/platform/slot/"

## Biến môi trường `apps/core`

Toàn bộ đọc qua `apps/core/internal/config`; thiếu thì dùng giá trị mặc định, sai định dạng thì `Load()` báo gộp mọi lỗi.

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
| `REDIS_DEDUPE_ADDR`, `REDIS_DEDUPE_DB` | `chatim-redis-dedupe:6379` / `0` | Redis dedupe: key `chatim:cid:*`, `request_id` của `AddMembers` `chatim:req:*` (D99) và của `CreateRoom` `chatim:req:create:*` (D118) (D44). Từ M3.1 core không còn ghi bitmap mark `chatim:evtack:*` |
| `REDIS_DEDUPE_PASSWORD`, `REDIS_DEDUPE_PASSWORD_FILE` | rỗng (không AUTH) | Như `REDIS_PASSWORD`, cho Redis dedupe |
| `NATS_URL` | `nats://chatim-nats:4222` | |
| `CORE_CONNECT_TIMEOUT` | `10s` | Timeout nối Mongo/Redis/NATS lúc khởi động |
| `CORE_REQUEST_DEADLINE` | `3s` | Deadline phía server cho mỗi unary RPC (D42), cũng là group deadline của actor |
| `CORE_SLOW_RPC` | `500ms` | RPC chạy lâu hơn mức này được log là chậm (interceptor log của `pkg/grpcserver`); phải dương |
| `CORE_QUEUE_WAIT`, `CORE_MAX_INFLIGHT` | `25ms` / `2048` | Giới hạn chờ và số RPC đang xử lý đồng thời (load shedding) |
| `CORE_DRAIN_DELAY` | `2s` | Chờ trước khi gRPC graceful stop, để LB ngừng gửi request mới |
| `CORE_GRPC_SHUTDOWN` | `5s` | Hạn graceful stop của gRPC server |
| `CORE_PUBLISHER_DRAIN` | `5s` | Hạn đẩy hết hàng đợi publisher và chờ `PublishAsyncComplete` lúc dừng; hết hạn thì bỏ phần còn lại (D49, D50) |
| `CORE_SHUTDOWN_BUDGET` | `28s` | Tổng ngân sách toàn bộ chuỗi dừng (D39); `Load()` từ chối nếu các mốc dừng cộng lại không nhỏ hơn. Mặc định các mốc là 26.2s (gồm 1s của sweeper, 1s của ba consumer projection, 1s của consumer phiếu hẹn, `2 × REDIS_OP_TIMEOUT` để cid batcher gửi hết Commit và 1s để hàng xoá cờ xả sau publisher), nên tăng mốc nào cũng phải tăng ngân sách này và `stop_grace_period` của compose (33s) |
| `CID_PENDING_TTL`, `CID_COMMITTED_TTL` | `10s` / `15m` | TTL key chống trùng cid ở Redis (D34); actor nhớ lần ghi chưa rõ theo cid trong `CID_PENDING_TTL` (D119) |
| `REDIS_OP_TIMEOUT`, `REDIS_COOLDOWN` | `100ms` / `1s` | Timeout mỗi lệnh Redis và thời gian chờ trước khi probe lại sau khi suy giảm |
| `CID_BATCH_SHARDS`, `CID_BATCH_MAX_KEYS`, `CID_BATCH_QUEUE` | `4` / `256` / `4096` | cid batcher: số shard (theo slot, tối đa 1024), số key tối đa mỗi lượt Reserve/Commit, hàng đợi mỗi shard (Commit/Abort đầy thì bỏ). Pool Redis dedupe = `2 × CID_BATCH_SHARDS + 4` kết nối, giữ ấm `2 × CID_BATCH_SHARDS` (D58, D60) |
| `FLUSH_SHARDS`, `FLUSH_WINDOW`, `FLUSH_MAX_BATCH`, `FLUSH_QUEUE`, `FLUSH_INSERT_TIMEOUT` | `4` / `2ms` / `256` / `1024` / `1s` | Flusher: số shard, cửa sổ gộp batch, batch tối đa, hàng đợi mỗi shard, timeout insert |
| `ACTOR_MAILBOX`, `ACTOR_IDLE`, `ACTOR_MAX_GROUP`, `ACTOR_MAX` | `1024` / `5m` / `64` / `100000` | Hàng đợi mỗi actor, thời gian nghỉ trước khi tự dừng, số lệnh gộp 1 nhóm, số actor tối đa 1 core |
| `PUB_SHARDS`, `PUB_QUEUE`, `PUB_MAX_PENDING`, `PUB_ACK_TIMEOUT` | `4` / `1024` / `256` / `2s` | Publisher JetStream: sharding theo slot, hàng đợi mỗi shard, số publish đang chờ ack mỗi shard (nats.go giới hạn `2 × shards × max pending`), timeout ack; mỗi shard phát theo thứ tự nhận, không chờ ack (D50) |
| `EVT_STREAM`, `EVT_SUBJECT_ROOT`, `EVT_LIVE_ROOT`, `EVT_STREAM_REPLICAS`, `EVT_STREAM_MAX_AGE`, `EVT_STREAM_DUPLICATES` | `CHATIM_EVT` / `evt` / `live` / `1` / `168h` / `5m` | Cấu hình stream `CHATIM_EVT` và RePublish sang `live.*`; `EVT_STREAM_DUPLICATES` phải dài hơn `SWEEP_MIN_AGE + SWEEP_INTERVAL` |
| `TIMER_STREAM`, `TIMER_SUBJECT_ROOT` | `CHATIM_TIMER` / `timer` | Stream phiếu hẹn WorkQueue (D111, M3.1): phiếu đặt ở `{root}.arm.{room}.{op}` hoặc `{root}.arm.{room}.{thread}.{seq}.{counter}.{op}`, tới hạn thì sang `{root}.due`; một durable consumer `timer-due` dùng chung mọi core (fetch 64/500ms, `AckWait` 30s), replicas = `EVT_STREAM_REPLICAS`, `MaxAge` 2h, chống trùng 2m. Tên và gốc subject phải khác stream event. Consumer dừng trong 1s |
| `SLOT_TICK`, `SLOT_HEARTBEAT_TTL`, `SLOT_LEASE_TTL`, `SLOT_HOOK_TIMEOUT` | `1s` / `5s` / `10s` / `Tick/2` | Nhịp slot manager; `HookTimeout` suy ra từ `SLOT_TICK` nếu không đặt riêng (D26) |
| `STATE_CLEAR_FLUSH`, `STATE_CLEAR_BATCH`, `STATE_CLEAR_QUEUE` | `100ms` / `500` / `65536` | Hàng xoá cờ `_state` (M3.1, D141+): sau PubAck OK publisher đưa doc vào hàng, hàng gom và xoá theo lô mỗi `STATE_CLEAR_FLUSH` hoặc đủ `STATE_CLEAR_BATCH`; đầy thì bỏ lượt xoá (đếm `state_clear_dropped_total`), doc giữ cờ và sweeper phát lại, không chặn lệnh. Dừng sau publisher, xả trong 1s |
| `SWEEP_INTERVAL`, `SWEEP_MIN_AGE`, `SWEEP_LOOKBACK` | `2s` / `5s` / `30s` | Sweeper trên core giữ slot 0: mỗi `SWEEP_INTERVAL` quét doc còn cờ `_state` cũ hơn `SWEEP_MIN_AGE`, từ mốc lần trước lùi `SWEEP_LOOKBACK`, dựng lại event từ doc hiện tại và phát lại qua JetStream client riêng (D147). `SWEEP_MIN_AGE` phải dài hơn `PUB_ACK_TIMEOUT + STATE_CLEAR_FLUSH + 1s`; `SWEEP_MIN_AGE + SWEEP_INTERVAL` phải ngắn hơn `EVT_STREAM_DUPLICATES`; `SWEEP_LOOKBACK` phải dài hơn `CORE_REQUEST_DEADLINE + 10s`. Dừng trong 1s |
| `SWEEP_BATCH`, `SWEEP_RATE` | `500` / `5000` | Cỡ trang mỗi lần đọc (1–1000) và số event phát lại tối đa mỗi giây (D154); client JetStream của sweeper cho tối đa `2 × SWEEP_BATCH` publish đang bay, timeout `PUB_ACK_TIMEOUT` |
| `PROJECTION_FETCH_BATCH`, `PROJECTION_FETCH_WAIT` | `256` / `500ms` | Ba consumer durable trên `CHATIM_EVT` (`projection-room_activity`, `projection-mention_reply_index`, `projection-mention_read`), dùng chung mọi core: số event tối đa mỗi lô (≤ 4096) và thời gian chờ lô. Lỗi thì `Nak` giãn dần 5s, 30s, 2m, 10m (cố định, không có env); lần giao thứ 6 trở đi đếm vào `projection_stuck_total`. Dừng trong 1s |
| `MESSAGE_LOCKED_KINDS` | (rỗng) | Danh sách loại tin, cách nhau bằng dấu phẩy, mà policy mặc định không cho ai sửa/xoá, kể cả tác giả; tên hợp lệ: `text`; tên lạ thì core không khởi động (D87) |
| `MENTION_HIDE_READ_TENANTS` | (rỗng) | Danh sách tenant id, cách nhau bằng dấu phẩy, mà tab mention (`ListMentions`) ẩn tin đã đọc (mention gửi không muộn hơn tin tại `read_seq` của room); tenant không có trong danh sách thấy mọi mention kèm cờ `read`; tenant id sai định dạng thì core không khởi động. Tạm thay kho cài đặt tenant (M3) (D115) |
| `REACTION_EMOJIS` | `👍,❤️,😂,😮,😢,🙏` | Danh sách emoji cố định được phép react, cách nhau bằng dấu phẩy, giữ thứ tự; để trống thì dùng mặc định. Core không khởi động nếu danh sách rỗng, có emoji không hợp lệ (`ValidateEmoji`), có emoji lặp, hoặc quá 100 emoji; lỗi nêu tên biến và emoji sai. `ReactMessage` với emoji ngoài danh sách trả `INVALID_ARGUMENT`; gỡ reaction (emoji rỗng) luôn được. Frontend đọc danh sách qua `GetReactionSettings` và chỉ cho chọn trong đó (D95) |
| `PIN_LIMIT` | `50` | Số tin ghim tối đa mỗi room (1–1000), chính xác vì pv dày; ghim lại tin đã ghim vẫn thành công (D92) |
| `MEMBER_BATCH_MAX` | `500` | Số người tối đa mỗi lệnh `CreateRoom` hoặc `AddMembers` (đếm sau khi bỏ trùng, 2–1000); quá thì `INVALID_ARGUMENT`. Core không giới hạn tổng số member của room (D107) |
| `MENTION_TARGETS_MAX` | `50` | Số đích mention tối đa mỗi tin và mỗi lần sửa tin (đếm sau khi bỏ trùng, yêu cầu A6); quá thì `INVALID_ARGUMENT`. `@all` không tính là đích, nó qua policy `mention_all` |
| `MEMBER_COUNT_CHECK_DELAY` | `5s` | Hạn của phiếu hẹn đếm lại `member_count` (message schedule NATS trên stream `CHATIM_TIMER`, D102, D111): lệnh member có thể đổi số đặt phiếu trước khi ghi và xoá phiếu sau `$inc`; lỗi hay core chết ở giữa thì phiếu bật sau hạn này (làm tròn lên giây). Phải dài hơn `CORE_REQUEST_DEADLINE`; không thêm bước dừng |
| `MESSAGE_COUNT_CHECK_DELAY` | `5s` | Hạn của phiếu hẹn đếm lại số trên tin (`rx` reaction, `rc` trả lời; D113): ghi tương tác đặt phiếu trước, `$inc` số rồi xoá phiếu; lỗi hay core chết ở giữa thì phiếu bật sau hạn này (làm tròn lên giây) và consumer phiếu hẹn (`count_repair`) đếm lại + CAS `v`. Phải dài hơn `CORE_REQUEST_DEADLINE`; không thêm bước dừng |
| `ROOM_LIST_JOIN_MAX` | `1500` | Điểm chuyển đường của `ListMyRooms` (≥ 1, không phải giới hạn): user có ≤ số room này thì một aggregate `members → rooms` ở DB, nhiều hơn thì đọc từng bước rồi xếp ở core; hai đường cùng kết quả, quyết định cache 60s theo user (D136) |
| `ROOM_TAIL_CACHE_BYTES` | `268435456` (256MB) | Số byte tối đa của cache đuôi room trong RAM mỗi core (LRU theo byte, ≥ 1048576 = 1MB; một đuôi 300 tin + 10 preview ≈ 14KB); entry hết hạn sau 30s. Cache ảnh chụp danh sách (64MB, TTL 30s) và cache chọn đường (100.000 user, TTL 60s) có cỡ cố định, không env |
| `ROOM_INFO_CACHE_ENTRIES` | `100000` | Số room giữ thông tin (tên, loại, số member…) trong cache RAM mỗi core (≥ 1, TTL 60s); `room_updated` và `member_count_changed` trên core NATS xoá entry của room ở mọi core |

## Cổng

Với app, compose publish ra host chỉ gRPC của core, và chỉ trên loopback — vì caller hiện được tin cậy qua metadata tới khi có mTLS (M5). Hạ tầng dev publish cổng riêng (`.env`):

| Cổng | Dịch vụ | Ghi chú |
|---|---|---|
| `6380` | `redis` state (`REDIS_PORT`) | AOF everysec, `noeviction`, bắt buộc AUTH (`REDIS_PASSWORD`) |
| `6381` | `redis-dedupe` (`REDIS_DEDUPE_PORT`) | Không lưu đĩa, `maxmemory` `REDIS_DEDUPE_MAXMEMORY` (512mb), `allkeys-lru`, bắt buộc AUTH (`REDIS_DEDUPE_PASSWORD`) |
| `127.0.0.1:9001` | `core-1` gRPC (`CORE1_GRPC_PORT`) | |
| `127.0.0.1:9002` | `core-2` gRPC (`CORE2_GRPC_PORT`) | |
| — | admin (`/healthz`, `/readyz`, `/metrics`, pprof) mỗi core, cổng `9090` | Chỉ trong mạng compose, không publish ra host |

## Tài liệu

- [Kiến trúc hệ thống (nguồn sự thật)](docs/architecture.md)
- [10 Module kỹ thuật tính năng (How-It-Works)](docs/modules/README.md)
- [Nhật ký quyết định kiến trúc D1–D140](docs/decision-log.md)
- [Roadmap](docs/roadmap.md)
- [Kết quả PoC](docs/poc/README.md)
- [Báo cáo nghiên cứu cơ chế hệ thống](docs/research/261005-system-mechanisms-synthesis-report.md)
- [Nghiên cứu kiến trúc chat mã nguồn mở (tinode, teamgram, chatto, gws)](docs/research/260930-opensource-chat-architecture-research.md)
