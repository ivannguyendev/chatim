# chatim

Hạ tầng chat dùng chung (CPaaS nội bộ) cho nhiều sản phẩm: quản lý room, tin nhắn, tương tác realtime hiệu năng cao; lấy lịch sử cực nhanh ở bất kỳ vị trí nào; phát event mạnh tới các app khác kết nối vào. Multi-tenant về mặt logic.

> Trạng thái: **M0–M1 (nền tảng + PoC) và M2a (core: CreateRoom/SendMessage/GetHistory qua gRPC, chống trùng cid, publish JetStream, 2 core trong compose) đã xong trên máy dev**. M2a.1 (bỏ `pts` toàn room, id event tự nhiên, publisher chỉ còn hàng đợi trên cơ chế async của nats.go, event best-effort — D47–D51) xong trên máy dev. M2a.2 (reconciler trên core giữ slot 0 đọc change stream của `messages`, publish bù event chưa có mark đã ack — D52) xong trên máy dev. M2a.3 (perf đường ghi: gom lệnh chống trùng cid giữa các room, ack trước Commit, client Redis dedupe pool nhỏ và ấm, mark đã ack gom trong cửa sổ 10ms — D58–D60) xong trên máy dev; M2a.2 và M2a.3 đã merge cùng lúc (PR #11). Rà soát cơ chế hệ thống đã chốt (2026-10-05): [thiết kế kiến trúc](docs/architecture.md); M2b.0, M2b.1 (effect engine: reader → work stream → worker mọi core, `room_created`, room activity, `/app resync`), M2b.2 (sửa/xoá theo fact `message_edits` + projection, ẩn/clear phía người đọc, lịch sử sửa), M2b.3 (reaction một emoji mỗi user + số đếm recount, ghim theo fact `pin_actions` + projection) và M2b.4 (member lớp tập, transaction đổi owner, `member_count` `$inc` + phiếu hẹn sinh tồn, vị trí đọc, subject theo loại dữ liệu, tên field đầy đủ) xong và đã merge vào `main` (PR #12, 2026-10-08). M2c (tương tác với tin chung `message_interactions`: reaction, trả lời có trích, bookmark; mention theo đích + `ListMentions`; mọi số trên tin `$inc` + phiếu hẹn sinh tồn; forward; `OpenDirectRoom` + `CreateRoom` chỉ group có `request_id`) đã merge vào `main` (PR #14, 2026-10-13). M3 (API truy vấn của core: danh sách hội thoại `ListMyRooms`/`GetRooms` do core xếp và đếm unread, cài đặt hội thoại, `UpdateRoom`, `GetHistory` neo `AROUND`, `GetMessages`, `ListThreads`, `GetReactions`, `ListPins`, `ListMembers`, tin hệ thống; [tóm tắt](docs/plans/2026-10-12-m3-core-query-apis-summary.md)) xây xong 2026-10-15, đã merge `main` (PR #15). M3.1 (bỏ effect engine: mỗi lệnh ghi doc kèm cờ `_state` chờ phát, publish ngay sau ack và xoá cờ sau PubAck, sweeper trên core giữ slot 0 phát bù cờ còn sót, một consumer `projections` (slot 0) nghe `CHATIM_EVT` không delay, phiếu hẹn trên `CHATIM_TIMER`, `/app resync` bật lại cờ — D141–D160; [tóm tắt](docs/plans/2026-10-16-m3-1-state-driven-events-summary.md)) xây xong 2026-10-16, đã merge `main` (PR #15); danh sách hội thoại cập nhật sau trung vị 1,6s, chậm nhất ~3,6s (bench 2K; engine cũ 2,7s / 5,7s), CPU không cao hơn engine cũ. Mention App (app riêng `modules/mention`, chạy nhúng trong core: chỉ mục mention, `ListMentions` trên `MentionService`, nhóm theo người và danh mục nhóm được tag, gợi ý `@nhóm`, thông báo theo người trên `CHATIM_NOTIFY`; consumer `mention` chia tải trên mọi core — D162–D176; [tóm tắt](docs/plans/2026-10-17-mention-app-summary.md), [danh mục event](docs/events.md)) xây xong 2026-10-17, đã merge `main` (PR #16); danh sách hội thoại cập nhật sau trung vị 1,7s, chậm nhất 4,3s ở bench 2K có 5% tin mention (trước 5,5s / 11,9s). Hai chủ đề hoãn từ M3 ([open topics](docs/plans/2026-10-15-m3-open-topics.md)): chặn đầu hàng của engine đã giải quyết ở M3.1, sharding đã đóng (core không shard, D161); M4 (websocket) đang làm trên `feat/m4`: phần 1 (kết nối, token, lệnh, D183–D188, [tóm tắt](docs/plans/2026-10-20-m4-1-ket-noi-va-lenh-summary.md)) và phần 2 (nhận và đẩy event, gói thông báo `WsPublish`, `ListMyRoomIDs`, `/app indexes`, D189–D195, [tóm tắt](docs/plans/2026-10-21-m4-2-nhan-va-day-event-summary.md)) đã xây, rồi phần 3 (đã đọc, đã xem, D196–D197, [tóm tắt](docs/plans/2026-10-22-m4-3-da-doc-va-da-xem-summary.md)) và phần gateway (app `gateway` HTTPS cho 22 lệnh đọc và quản lý, websocket còn 12 lệnh ghi realtime, D198, [tóm tắt](docs/plans/2026-10-23-m4-gateway-https-summary.md)); còn phần 4–5 theo [roadmap](docs/roadmap.md); quyết định go/no-go chờ PoC prod-like. Kết quả đo: [docs/poc/README.md](docs/poc/README.md). Bản đồ code: [INDEXES.csv](INDEXES.csv).

## Kiến trúc

Monorepo Go, mỗi app một container.

| App | Vai trò | Phase |
|---|---|---|
| `core` | Room, member, message, tương tác; cấp seq; lưu MongoDB; phát event best-effort lên NATS JetStream (id event tự nhiên, thứ tự từng room); sửa/xoá tin là fact `message_edits` + projection `messages`, ẩn/clear theo người đọc; tương tác với tin chung `message_interactions` (reaction một emoji mỗi user, bookmark, trả lời có trích ở timeline chính), đích mention trên tin (kèm giờ của từng đích), forward; số trên tin (`rx` theo emoji, `rc` số trả lời) `$inc` + phiếu hẹn đếm lại; ghim là fact `pin_actions` + projection `rooms.pins`; DM qua `OpenDirectRoom` + sổ `room_dms`, group qua `CreateRoom` có `request_id`; member mỗi (room, user) một doc, lệnh đụng owner trong một transaction, `member_count` `$inc` + phiếu hẹn đếm lại, vị trí đọc; event trên subject theo loại dữ liệu `evt.{t}.{loại}.{rid}.{kind}` với loại `room`, `member` hoặc `message` (RePublish sang `live.{t}.{loại}.{rid}.evt.{kind}`; nghe mọi event của room: `live.{t}.*.{rid}.>`), core không chọn người nhận; mỗi lần ghi bật cờ `_state` trên doc, publisher xoá cờ sau PubAck, sweeper trên core giữ slot 0 phát lại event của doc còn cờ (và áp projection sửa/xoá/ghim còn thiếu), một consumer `projections` trên `CHATIM_EVT` chạy ở core giữ slot 0, chia mỗi lô cho hai handler (room activity, doc trả lời), consumer phiếu hẹn sửa số trên tin và số member (M3.1); tin có mention phát ở loại dữ liệu `mention` | 1 |
| `mention` (Mention App) | Chạy nhúng trong core (`modules/mention/mentionapp`): consumer `mention` (queue group mọi core) ghi `mentions`/`mention_rooms` theo event tự đủ, bung `@nhóm`/`@all`, gửi thông báo dạng gói `WsPublish` (nội dung một lần + danh sách người nhận) lên `websocket.publish.{tenant}` (`CHATIM_WS`); `MentionService` (`ListMentions`, `SetUserGroups`, `SetGroups`, `ListRoomGroups`) trên gRPC server của core | 1 |
| `websocket` | WebSocket (gws) cho client SDK; nhận event từ NATS và đẩy realtime | 1 |
| `gateway` | HTTPS (Connect trên HTTP/1.1 và h2c) cho tải dữ liệu và quản lý room: 22 lệnh `corecall.Request`, JWT như websocket, gọi core bất kỳ (round robin), giới hạn theo user | 1 |
| `api`, `events`, `push`, `auth`, `migrator` | Public API, event stream cho app ngoài, push notification, xác thực, migrate/dual-write từ hệ thống cũ | Sau |

Hạ tầng: MongoDB (replica set), Redis, NATS JetStream.

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
pkg/                     # dùng chung: keys, ids, lru, slotmap, apperr, envconfig, resilience, admin, grpcserver, grpcclient, backoff, jsconsume, mongobound, callerid, pb, corecall, coreroute, jwtverify, wsclient, wspublish
proto/chatim/v1/         # định nghĩa protobuf (buf) → pkg/pb
tools/                   # corecli, internal/route; poc/: corebench, mongobench, postgresbench, natsbench, wsbench
scripts/                 # e2e.sh, các script chờ hạ tầng sẵn sàng, bench-cell.sh / bench-sample.sh (đo corebench)
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
    make core-up                 # sinh khoá dev nếu chưa có, build image apps/core + apps/websocket + apps/gateway, chạy core-1 + core-2 + websocket-1 + websocket-2 + gateway-1 (profile app), chờ cả năm healthy
    make core-down               # dừng và xoá gateway-1, websocket-1, websocket-2, core-1, core-2
    make dev-keys                # sinh khoá ký dev vào .local/ws-keys/ (private.jwk 0600, jwks.json 0644) nếu chưa có; muốn thay khoá: xoá thư mục rồi chạy lại
    make -s ws-token ARGS="-tenant e2e -user alice"   # in token dev (mặc định -ttl 15m, iss chatim-dev, aud chatim-websocket) để nối ws://127.0.0.1:8081/ws hoặc gọi http://127.0.0.1:8082
    make ws-logs                 # docker logs -f chatim-websocket-1; make ws-logs NODE=2 cho chatim-websocket-2
    make gw-logs                 # docker logs -f chatim-gateway-1
    make alerts-check            # promtool kiểm deploy/prometheus/alerts.yml (trong Docker)
    make e2e                     # build tools/corecli, chạy scripts/e2e.sh (route theo slot, kill core-1, kiểm tra; phase 3 sửa seq 1, xoá seq 2; phase 4 react seq 3 rồi đổi emoji, ghim seq 4; phase 5 member: DM từ chối, thêm, đổi role, priority, xoá, vị trí đọc, ẩn và clear, owner cuối rời rồi vào lại, kiểm reply và event trên subject `room`/`member`/`message`; phase 6 liên kết: mở DM hai lần ra một room, tạo group hai lần cùng `request_id`, react, hai trả lời + `GetReplies`, chặn xoá tin cha, xoá một trả lời, mention user/nhóm/`@all` + `ListMentions`, `@nhóm`/`@all` gửi trước khi vào group không hiện, forward, bookmark + `ListBookmarks`, 22 event live; phase 7 danh sách hội thoại và API đọc: tin hệ thống, unread sau gửi/đọc/xoá/ẩn, ghim và lưu trữ, trang 2, `GetRooms`, `UpdateRoom`, `AROUND`, `GetMessages`, thread, người react, ghim, member, forward có `skip_unread`/`meta`, 34 event live; phase 8 Mention App: nhóm được tag và nhóm tắt, `SetUserGroups` cho 3 người rồi chuyển 1 người, `@nhóm` báo đúng người còn trong room, `@nhóm` tắt không ai nhận, sửa thêm rồi bỏ `@user` (`mention_notified`, `mention_retracted`, `ListMentions`), xoá tin, `@all`, `ListRoomGroups`, `SetUserGroups` với `ver` cũ; kiểm 12 thông báo đọc từ gói `WsPublish` trên `websocket.publish.{tenant}`; phase 9 websocket: lệnh qua websocket-1 so với gRPC, 4001/4002, rồi `corecli e2e ws-events` hai người A/B: nhóm mới hiện ngay, react chỉ khi mở room, không lọt event riêng, `@B`/`@all` đúng một lần, `member_removed` rồi `room_closed` và im lặng, 0 trùng 0 lỗ; không token nào trong log websocket; phase 10 gateway; phase 11 hai node websocket: `corecli e2e ws-ephemeral` (đang gõ từ node 1 tới người đang mở nhóm ở node 2 trong 1s, không tới ai khác, người ngoài nhóm gõ bị bỏ, online, offline trong 35s sau khi đóng, nối lại sau 3s không offline) và `ws-ephemeral-kill` (`docker kill -s KILL websocket-2`, người ở đó offline trên máy xem ở node 1); không token nào trong log websocket-2)
    docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev edit -room ID -seq N -base V -text "..."   # sửa tin; cùng cờ: delete -room ID -seq N -base V, hide -room ID -seq N, clear -room ID, edits -room ID -seq N [-after V]; -tenant/-user chọn người gọi; image do make e2e build
    docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev react -room ID -seq N -emoji "👍"   # đặt/đổi reaction của người gọi (-emoji "" gỡ); pin -room ID -seq N, unpin -room ID -seq N; -tenant/-user chọn người gọi
    docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev add-members -room ID -users a,b [-request-id R]   # thêm người (cùng -request-id = gửi lại đúng một lần); cùng cờ: remove-member -room ID -target U, leave -room ID, set-role -room ID -target U -role owner|admin|member, set-priority -room ID -target U -priority N, read -room ID [-seq N] (0 = tin cuối), unread -room ID -seq N; -tenant/-user chọn người gọi
    docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev open-dm -other U   # mở (hoặc lấy lại) room DM với U; create-room -name N -members a,b [-request-id R] tạo group (cùng -request-id = cùng group)
    docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev set-user-groups -user U -groups g1,g2 -ver N   # Mention App: đặt đủ nhóm của người (-groups rỗng = không nhóm nào; -ver phải lớn hơn bản đang giữ); set-groups -group G -name "Tên" -ver N [-inactive] khai báo nhóm được tag; room-groups -room ID liệt kê nhóm có người trong room
    docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev watch -room ID [-tenant T]   # in event live của room: live.{t}.*.{rid}.> (subject room, member, message)
    docker exec chatim-core-1 /app resync -from 2026-10-05T08:00:00Z -to 2026-10-05T09:00:00Z [-tenant T] [-room ID] [-rate 500] [-dry-run]   # bật lại cờ `_state` của các doc trong khoảng để sweeper phát lại event từ trạng thái hiện tại (stream mất event, dựng lại projection; D155)
    docker exec chatim-core-1 /app recount -room ID [-dry-run]   # đếm lại member_count của một room, in room=… stored=… counted=…; không -dry-run thì ghi CAS theo member_count_ver (kèm cờ `_state` bit 2) rồi publish member_count_changed, PubAck OK thì xoá cờ, lỗi thì để cờ cho sweeper (trượt CAS → exit lỗi, chạy lại; D102)
    docker exec chatim-core-1 /app indexes [-dry-run|-apply|-drop-old]   # đổi index chỉ bằng lệnh này (D191): -dry-run (mặc định) in index thiếu, đang build, cũ, lạ, xung đột của core và Mention App; -apply tạo bản thiếu bên cạnh bản cũ, không xoá; -drop-old xoá bản cũ chỉ khi mọi bản thay đã build xong; có xung đột tuỳ chọn thì từ chối toàn bộ
    make poc TOOL=corebench ARGS="-rate 5000 -duration 60s -watch 20"   # tải mở-vòng vào cụm core, cần core-up trước
    ./scripts/ws-nats-drill.sh 10 cut            # diễn tập websocket: cắt websocket-1 khỏi NATS 10s (iptables trong sidecar), tải corebench + corecli e2e ws-drill; ./scripts/ws-nats-drill.sh 180 cut (quá 2 phút → nhảy + resync), ./scripts/ws-nats-drill.sh 0 restart-nats; cần core-up; kết quả bin/drill/ws-nats-*/ (summary.txt do scripts/ws-drill-summary.sh METRICS START RESTORED in)

Khởi động core chỉ build index trên collection rỗng; collection có dữ liệu mà thiếu hoặc lệch index thì core log `warn` "index outdated, run /app indexes" và đặt gauge `chatim_core_index_outdated{collection}` = 1, không tự build hay xoá. Chạy `-dry-run`, rồi `-apply`, chờ build xong (dry-run hết "building"), rồi `-drop-old`.

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
| `PROJECTION_FETCH_BATCH`, `PROJECTION_FETCH_WAIT` | `8192` / `3s` | Một consumer durable `projections` trên `CHATIM_EVT` (lọc `evt.>`, ack từng tin, `AckWait` 30s, `MaxAckPending` = 4 × lô; ba durable cũ `projection-*` bị xoá lúc khởi động): số event tối đa mỗi lô (1–8192) và thời gian chờ lô (tối đa 3s). Cửa sổ 3s gom nhiều event mỗi room vào một lệnh ghi `rooms` (2K event/s ≈ 6K event một lô, nên cửa sổ đóng lô chứ không phải cỡ lô), đưa CPU Mongo về ngang engine cũ (D160). Khi dừng, core đợi lần fetch dở hết hạn rồi trả các tin nhận được bằng `Nak(0)`, không đánh rơi tin nào; mốc dừng là `PROJECTION_FETCH_WAIT` + 1s. Chỉ core giữ slot 0 fetch (xét lại mỗi `SLOT_TICK`); mỗi lô chia cho `room_activity` (13 kind, một `BulkWrite` `$max` mỗi room-thread), `mention_reply_index` (`msg_created/edited/deleted`) và `mention_read` (`read_updated`). Ổn hay room/doc không còn thì `Ack`, không decode được thì `Term`, lỗi thì `Nak` riêng tin đó theo bậc thang 5s, 30s, 2m, 10m (cố định); lần giao thứ 6 trở đi đếm vào `projection_stuck_total`. Mất slot 0 thì làm xong lô đang ghi, `Nak(0)` lô vừa nhận. `PROJECTION_ACTIVITY_FETCH_*` cũ bị bỏ qua. Dừng trong `PROJECTION_FETCH_WAIT` + 1s (mặc định 4s) |
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

## Biến môi trường `apps/websocket`

Đọc qua `apps/websocket/internal/config`; sai định dạng hoặc vi phạm luật thì node không khởi động và báo gộp mọi lỗi.

| Biến | Mặc định | Ghi chú |
|---|---|---|
| `WS_NODE_ID` | hostname | Định danh node, nằm trong tên client Redis `chatim-websocket-{node}` |
| `WS_ADDR` | `:8080` | Lắng nghe WebSocket, đường dẫn `/ws`, subprotocol `chatim.v1` |
| `WS_ADMIN_ADDR` | `:9090` | `/livez`, `/readyz`, `/metrics`; `/app probe` hỏi địa chỉ này; không publish ra host |
| `REDIS_ADDR`, `REDIS_DB` | `chatim-redis:6379` / `0` | Redis state của core, chỉ đọc bảng slot để route lệnh tới core giữ room |
| `REDIS_PASSWORD`, `REDIS_PASSWORD_FILE` | rỗng | Như của core; `_FILE` thắng. Compose trỏ `_FILE` vào secret `redis_password` |
| `WS_JWT_KEYS_FILE` | — (bắt buộc) | JWKS công khai (Ed25519, có `kid`); file có khoá riêng (`d`) bị từ chối. Đọc một lần lúc khởi động |
| `WS_JWT_ISSUER`, `WS_JWT_AUDIENCE` | — (bắt buộc) | `iss`, `aud` phải khớp; compose dev đặt `chatim-dev` / `chatim-websocket` |
| `WS_JWT_LEEWAY` | `30s` | Dung sai giờ cho `exp`/`nbf` (≥ 0) |
| `WS_AUTH_TIMEOUT` | `5s` | Không gửi frame auth trong thời gian này thì đóng 4002 |
| `WS_UNAUTH_MAX` | `10000` | Số kết nối chưa xác thực tối đa mỗi node; quá thì 503 |
| `WS_FRAME_MAX` | `131072` | Cỡ frame tối đa (≥ 4096); quá thì đóng 1009 |
| `WS_HEARTBEAT_TIMEOUT` | `15s` | Không nhận frame nào trong thời gian này thì đóng (≥ 10s; với mặc định presence phải ≥ 13s vì `WS_PRESENCE_TTL` ≤ `WS_HEARTBEAT_TIMEOUT + WS_PRESENCE_STAMP_EVERY`) |
| `WS_COMMAND_RATE`, `WS_COMMAND_BURST` | `50` / `100` | Giới hạn frame mỗi giây và burst mỗi kết nối |
| `WS_COMMAND_INFLIGHT` | `32` | Số lệnh đang chạy tối đa mỗi kết nối; quá thì `RESOURCE_EXHAUSTED` |
| `WS_SEND_QUEUE` | `256` | Hàng frame chờ ghi mỗi kết nối; đầy thì đóng 4008 sau các frame đã xếp (D194) |
| `WS_SEND_QUEUE_BYTES` | `1048576` | Byte chờ ghi mỗi kết nối (≥ `WS_FRAME_MAX`); vượt thì đóng 4008 sau các frame đã xếp; lúc node đang đuổi theo stream thì gửi `resync{CLIENT_BACKLOG}` thay vì đóng. Không có ngân sách toàn node: compose đặt `mem_limit 2g`, `GOMEMLIMIT 1600MiB` |
| `WS_WRITE_TIMEOUT` | `10s` | Hạn ghi một frame |
| `CORE_REQUEST_DEADLINE` | `5s` | Deadline mỗi lệnh gọi core |
| `WS_DRAIN_DELAY`, `WS_SHUTDOWN_BUDGET` | `2s` / `15s` | Chuỗi dừng: `WS_DRAIN_DELAY + CORE_REQUEST_DEADLINE + 2s` (dừng feed NATS) `+ 1s` (đóng kết nối) phải ≤ budget, mặc định 10s; compose `stop_grace_period` = budget + 3s = 18s |
| `NATS_URL` | — (bắt buộc) | NATS của core; credential bị che trong log. Lần sẵn sàng đầu cần NATS; mất NATS sau đó `/readyz` vẫn true, gauge `nats_connected` = 0 |
| `EVT_STREAM`, `EVT_SUBJECT_ROOT` | `CHATIM_EVT` / `evt` | Stream event của core và gốc subject; mỗi node đọc `{root}.>` bằng một ordered consumer, bắt đầu ở seq cuối + 1 (D190). Phải khớp config core |
| `WS_PUBLISH_STREAM`, `WS_PUBLISH_ROOT` | `CHATIM_WS` / `websocket.publish` | Hộp thư của websocket: gói `WsPublish` trên `{root}.{tenant}` (D192); websocket tạo hoặc cập nhật stream lúc khởi động (cấu hình chung `pkg/wspublish`). Phải khớp `MENTION_WS_PUBLISH_*` của core; stream khác `EVT_STREAM`, root không bắt đầu bằng token đầu của `EVT_SUBJECT_ROOT` |
| `EVT_STREAM_REPLICAS` | `1` | Số bản sao khi websocket tạo `CHATIM_WS` |
| `WS_FANOUT_WORKERS`, `WS_FANOUT_QUEUE` | số CPU / `4096` | Worker theo room (`hash(room)`, giữ thứ tự từng room) và hàng event mỗi worker; đầy thì vòng nhận chờ |
| `WS_CONSUME_BATCH`, `WS_CONSUME_BYTES` | `500` / `8388608` | Lô kéo của ordered consumer theo số tin và byte (byte ≥ 1MB) |
| `WS_REPLAY_RATE` | `20000` | Tin/giây tối đa khi đang đuổi theo (giờ lưu tin cũ hơn 5s) |
| `WS_REPLAY_MAX_AGE` | `2m` | Trễ quá mức này, đo bằng giờ JetStream lưu tin (> 5s), thì coi như node khởi động lại: bỏ đoạn cũ, đọc từ hiện tại, tạm ngừng đẩy cho mọi người trên node tới khi người đó được nạp lại rồi gửi `resync`; cũng là thời gian nghỉ tối thiểu giữa hai lần nhảy vì trễ |
| `WS_RELOAD_RATE` | `1000` | Số người nạp lại mỗi giây mỗi node sau một lần nhảy (thiết bị đang chờ `ready` trước, rồi người đang mở room, rồi còn lại); nạp lỗi thì máy nhận `load_failed`, kết nối vẫn giữ, server không tự nạp lại; máy gửi `load_rooms` (lên đầu hàng) hoặc nối lại. `WS_REPLAY_MAX_AGE` nên lớn hơn số người trên node / tốc độ này |
| `WS_TYPING_RATE`, `WS_TYPING_BURST` | `2` / `4` | Giới hạn frame `typing` mỗi giây và burst mỗi kết nối (sau giới hạn frame chung); quá thì bỏ frame, đếm `typing_dropped_total{reason="rate"}`, không đóng kết nối |
| `WS_TYPING_QUEUE` | `8192` | Hàng chờ tín hiệu `typing` nhận từ node khác (core NATS `eph.{tenant}.{room}.typing`); đầy thì bỏ, đếm `reason="queue_full"` |
| `WS_TYPING_PENDING_MSGS` | `65536` | Số tin tối đa chờ trong subscription `eph.*.*.typing` của thư viện NATS (kèm 64MB); vượt thì NATS bỏ tin |
| `WS_PRESENCE_REDIS_ADDR`, `WS_PRESENCE_REDIS_DB` | `chatim-redis-presence:6379` / `0` | Redis presence riêng: mỗi người online là một key `chatim:presence:{tenant}:{user}` = `1` có `EX` = `WS_PRESENCE_TTL`; còn key là online, mất key là offline. Chỉ `SET … EX` (và `MGET` sau), không `SCAN`/`KEYS`, không xoá. Node không chờ Redis này khi khởi động, `/readyz` không phụ thuộc nó; gauge `presence_redis_up` |
| `WS_PRESENCE_REDIS_PASSWORD`, `WS_PRESENCE_REDIS_PASSWORD_FILE` | rỗng | Như `REDIS_PASSWORD`; `_FILE` thắng. Compose trỏ `_FILE` vào secret `redis_presence_password` |
| `WS_PRESENCE_REDIS_TIMEOUT` | `500ms` | Dial/đọc/ghi và hạn của mỗi lệnh pipeline, tính cả lần thử lại (`MaxRetries 1`) |
| `WS_PRESENCE_REDIS_POOL` | `64` | Số kết nối tối đa của client presence (`chatim-websocket-presence-{node}`) |
| `WS_PRESENCE_TTL` | `15s` | TTL của key online; phải lớn hơn `WS_PRESENCE_STAMP_EVERY + 10s + 2 × WS_PRESENCE_FLUSH + WS_PRESENCE_REDIS_TIMEOUT` và không quá `WS_HEARTBEAT_TIMEOUT + WS_PRESENCE_STAMP_EVERY` |
| `WS_PRESENCE_STAMP_EVERY` | `2s` | Một người được đóng dấu lại khi lần đóng dấu trước trên node đã cũ hơn mức này; mọi frame (heartbeat, ping) sau auth đều tính, nhiều kết nối cùng người chỉ ghi một lần |
| `WS_PRESENCE_FLUSH` | `1s` | Chu kỳ ghi những người chờ đóng dấu; batch lỗi đầu tiên dừng lượt, người chưa ghi chờ lượt sau; người không có frame quá `WS_PRESENCE_TTL` bị bỏ khỏi RAM và không ghi lại; dừng node không ghi lượt cuối |
| `WS_PRESENCE_BATCH` | `500` | Số key mỗi pipeline, `1..1000` |
| `WS_PRESENCE_CACHE` | `5s` | Kết quả online đọc từ Redis được giữ trong RAM ngần này (offline không bao giờ giữ); trong thời gian đó hỏi lại người đó không đọc Redis. Phải nhỏ hơn `WS_PRESENCE_TTL`; lượt ghi mỗi `WS_PRESENCE_FLUSH` cũng xoá mục cũ hơn mức này |
| `WS_PRESENCE_WATCH_MAX` | `200` | Số người tối đa trong danh sách hỏi `watch_presence` của một kết nối (bỏ id sai và trùng, giữ 200 người đầu, đếm `presence_watch_truncated_total`). Mỗi `watch_presence`, heartbeat hay ping có danh sách khác rỗng được trả cả danh sách trong một frame `presence` (một `MGET` cho người chưa có trong cache); mỗi kết nối chỉ một lần trả đang chạy, lần khác lúc đó bị bỏ (`reason="busy"`), hàng gửi quá nửa thì bỏ (`reason="send_full"`), Redis lỗi thì không gửi gì |
| `WS_ROOM_IDS_INFLIGHT` | `64` | Số lần gọi `ListMyRoomIDs` đang chạy tối đa mỗi node; lỗi thì gửi `load_failed` cho các kết nối của người đó |
| `WS_OPEN_PENDING` | `10s` | `open_room` của room chưa có trong danh sách (vừa tạo) chờ `member_added` tối đa ngần này, rồi `room_closed{NOT_MEMBER}` |

Khoá dev: `make dev-keys` chạy `corecli dev-keys -out /keys` trong image `chatim/corecli:dev` với uid của máy, ghi `.local/ws-keys/private.jwk` (0600, chỉ nằm trong `.local/`, đã gitignore) và `.local/ws-keys/jwks.json` (0644). Compose chỉ đưa `jwks.json` vào websocket-1 làm secret `ws_jwks` (`WS_JWT_KEYS_FILE=/run/secrets/ws_jwks`); khoá riêng không bao giờ mount vào container websocket. Lấy token:

    make -s ws-token ARGS="-tenant e2e -user alice -ttl 10m"
    docker run --rm --user "$(id -u):$(id -g)" -v "$PWD/.local/ws-keys/private.jwk":/keys/private.jwk:ro chatim/corecli:dev token -key /keys/private.jwk -tenant e2e -user alice

Client nối `ws://127.0.0.1:8081/ws` (subprotocol `chatim.v1`), frame đầu là auth mang token; `pkg/wsclient` làm sẵn việc này.

`pkg/wsclient` gửi đang gõ bằng `Typing(room)` (lần đầu gửi ngay, sau đó tối đa một lần mỗi 3s mỗi room), `StopTyping(room)` (chỉ gửi khi đã gửi `typing` mà chưa dừng; gửi `SendMessage` vào room cũng tính là dừng, không gửi `stopped`), nhận qua `OnTyping(room, user, typing)` (`false` khi có `stopped`, khi 6s không có tin mới của người đó, hoặc khi `msg_created` của người đó trong room tới). `WatchPresence(users)` thay cả danh sách hỏi online trên server và kết quả tới `OnPresence(entries)` ở mỗi heartbeat. Danh sách này chỉ sống theo kết nối và client chưa tự nối lại: sau mỗi lần nối lại, app phải gọi lại `WatchPresence` với danh sách đầy đủ.

Đổi khoá (dev và prod, không cắt kết nối đang sống): thêm khoá mới (`kid` mới) vào JWKS bên cạnh khoá cũ → rolling restart các node websocket (JWKS chỉ đọc lúc khởi động) → chuyển bên ký sang khoá mới → chờ token cũ hết hạn (TTL + `WS_JWT_LEEWAY`) → bỏ khoá cũ khỏi JWKS và rolling restart lần nữa. Nên giữ TTL token ≤ 15 phút để bước chờ ngắn và token lộ hết hạn nhanh.

## Biến môi trường `apps/gateway`

Đọc qua `apps/gateway/internal/config`; sai định dạng hoặc vi phạm luật thì node không khởi động và báo gộp mọi lỗi. Gateway không dùng Redis hay NATS.

| Biến | Mặc định | Ý nghĩa |
|---|---|---|
| `GW_ADDR` | `:8080` | Lắng nghe HTTP/1.1 và h2c; đường dẫn `/chatim.v1.CoreService/{Lệnh}`, `/chatim.v1.MentionService/{Lệnh}` (Connect, JSON hoặc protobuf, `POST`) |
| `GW_ADMIN_ADDR` | `:9090` | `/livez`, `/readyz` (có ít nhất một core READY và chưa tắt), `/metrics` (`chatim_gateway_*`); `/app probe` hỏi địa chỉ này; không publish ra host |
| `GW_CORE_ADDRS` | — (bắt buộc) | Danh sách `host:port` của core, cách nhau dấu phẩy; một kết nối gRPC round robin, không Redis, không retry của mình; compose đặt `chatim-core-1:9000,chatim-core-2:9000` |
| `GW_JWT_KEYS_FILE`, `GW_JWT_ISSUER`, `GW_JWT_AUDIENCE` | — (bắt buộc) | Như websocket: JWKS công khai đọc một lần lúc khởi động (đổi khoá = khởi động lại); compose dùng cùng secret `ws_jwks` và `chatim-dev` / `chatim-websocket` |
| `GW_JWT_LEEWAY` | `30s` | Dung sai giờ cho `exp`/`nbf` (≥ 0) |
| `GW_BODY_MAX` | `131072` | Cỡ body tối đa; quá thì `resource_exhausted` |
| `GW_USER_RATE`, `GW_USER_BURST` | `50` / `100` | Lệnh mỗi giây và burst mỗi (tenant, user) trên node; quá thì `resource_exhausted` (429) |
| `GW_USER_INFLIGHT` | `32` | Lệnh đang chạy tối đa mỗi (tenant, user) trên node |
| `GW_USER_LIMITERS` | `100000` | Số người giữ bộ đếm trong RAM (LRU, chỉ đẩy người không có lệnh đang chạy; ≥ `GW_INFLIGHT_MAX`) |
| `GW_INFLIGHT_MAX` | `10000` | Lệnh đang chạy tối đa cả node, kiểm trước token; quá thì `unavailable` (503) |
| `GW_CORS_ORIGINS` | rỗng | Origin trình duyệt được phép, đúng từng origin (`https://chat.example.com`), không `*`; rỗng = không cho trình duyệt khác origin; compose dev đặt `http://localhost:3000` (e2e phase 10 kiểm preflight từ origin này) |
| `GW_DRAIN_DELAY`, `GW_SHUTDOWN_BUDGET` | `2s` / `15s` | Dừng: `/readyz` false, chờ drain, chờ lệnh đang chạy; budget phải lớn hơn `GW_DRAIN_DELAY + CORE_REQUEST_DEADLINE`; compose `stop_grace_period` 18s |
| `GW_READ_HEADER_TIMEOUT`, `GW_READ_TIMEOUT` | `5s` / `10s` | Hạn đọc header và cả request |
| `GW_WRITE_TIMEOUT` | `CORE_REQUEST_DEADLINE + 5s` | Hạn ghi trả lời (không nhỏ hơn mặc định) |
| `GW_IDLE_TIMEOUT` | `60s` | Giữ kết nối rảnh |
| `GW_MAX_HEADER_BYTES` | `16384` | Cỡ header tối đa; header `Authorization` dài quá 8KB bị từ chối |
| `GW_H2_MAX_STREAMS` | `250` | Số stream HTTP/2 đồng thời mỗi kết nối |
| `CORE_REQUEST_DEADLINE` | `5s` | Hạn một lần gọi core |

Gọi thử (token dev, không in giá trị `.env`):

    curl -H "Authorization: Bearer $(make -s ws-token ARGS='-tenant e2e -user alice')" -H 'Content-Type: application/json' -d '{}' http://127.0.0.1:8082/chatim.v1.CoreService/ListMyRooms

Không token → 401 `unauthenticated`; lệnh realtime (vd `SendMessage`) hoặc nội bộ (`ListMyRoomIDs`, `SetUserGroups`, `SetGroups`) → `unimplemented`.

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
- [Nhật ký quyết định kiến trúc D1–D198](docs/decision-log.md)
- [Danh mục event (core và Mention App phát gì, ai nhận gì)](docs/events.md)
- [Roadmap](docs/roadmap.md)
- [Kết quả PoC](docs/poc/README.md)
- [Báo cáo nghiên cứu cơ chế hệ thống](docs/research/261005-system-mechanisms-synthesis-report.md)
- [Nghiên cứu kiến trúc chat mã nguồn mở (tinode, teamgram, chatto, gws)](docs/research/260930-opensource-chat-architecture-research.md)
