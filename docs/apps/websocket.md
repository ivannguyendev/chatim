# App `websocket`

Giữ kết nối WebSocket (gws) của client: 12 lệnh ghi realtime chuyển tới core theo slot, đẩy event và thông báo, đang gõ, online, giữ kết nối khi token hết hạn. Không có database. Cơ chế, giao thức và đặc tả client: [module 09](../modules/09-feature-realtime-delivery-and-ephemeral.md) (§7 giao thức, §8 đẩy event, §10 đang gõ và online, §11 đặc tả client).

## Biến môi trường

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
| `WS_JWT_LEEWAY` | `30s` | Dung sai giờ cho `exp`/`nbf` (0–1 phút; `iam` giữ mục thu hồi thêm 1 phút) |
| `WS_IAM_INTERNAL_URL`, `WS_IAM_SECRET`/`_FILE`, `WS_IAM_JWKS_URL`, `WS_IAM_REVOCATIONS_INTERVAL`, `WS_IAM_KEYS_INTERVAL`, `WS_IAM_READY_TIMEOUT`, `WS_IAM_ALLOW_HTTP` | bắt buộc / bắt buộc / bắt buộc / `2s` / `1m` (tối đa `1m`) / `10s` / `false` | Đồng bộ danh sách thu hồi và khoá với `iam` ([iam.md](iam.md), node đồng bộ); bản dùng chung nằm trên Redis presence. `iam` trả 4xx (sai secret/URL) → node không `ready` (`iam_refused{kind}`). Phiên bị thu hồi → `auth_required{REVOKED}`, tenant bị khoá → đóng `4003` |
| `WS_AUTH_GRACE` | `3m` | Kết nối mới và kết nối có token hết hạn ở trạng thái chờ đăng nhập: không nhận event, thông báo, typing, online; lệnh nhận `UNAUTHENTICATED`, frame khác (trừ `auth`, heartbeat) nhận `auth_required` tối đa 1 lần/giây. Chờ quá mức này thì đóng 4001 |
| `WS_AUTH_RATE`, `WS_AUTH_BURST` | `1000` / `2000` | Số lần đăng nhập mỗi giây và burst mỗi node; chỉ token hợp lệ mới tốn suất, gia hạn cùng người không tốn. Hết suất thì `retry_later` (đếm `rate`) và giữ kết nối chờ. Mỗi kết nối đang chờ gửi `auth` tối đa 1 lần/giây, dồn hơn thì `retry_later{1000}` mà không kiểm token (đếm `throttled`) |
| `WS_AUTH_RETRY_MIN`, `WS_AUTH_RETRY_MAX` | `5s` / `30s` | `retry_after_ms` của `retry_later` lấy ngẫu nhiên đều trong khoảng này (MIN ≤ MAX) |
| `WS_USER_CONNECTIONS_MAX` | `10` | Số kết nối tối đa của một người trên một node (đếm trong RAM); kết nối vượt bị đóng 4009 ngay sau `auth` hợp lệ, không gọi core |
| `WS_UNAUTH_MAX` | `10000` | Số kết nối đang chờ đăng nhập tối đa mỗi node; quá thì bắt tay nhận 503. Kết nối đang chạy quay lại chờ (token hết hạn) vẫn giữ kết nối dù vượt trần, đếm `unauth_over_cap_total` |
| `WS_FRAME_MAX` | `131072` | Cỡ frame tối đa (≥ 4096); quá thì đóng 1009 |
| `WS_HEARTBEAT_TIMEOUT` | `15s` | Không nhận frame nào trong thời gian này thì đóng (≥ 10s; với mặc định presence phải ≥ 13s vì `WS_PRESENCE_TTL` ≤ `WS_HEARTBEAT_TIMEOUT + WS_PRESENCE_STAMP_EVERY`) |
| `WS_COMMAND_RATE`, `WS_COMMAND_BURST` | `50` / `100` | Giới hạn frame mỗi giây và burst mỗi kết nối (tính cả `auth`, heartbeat, ping); lệnh quá giới hạn nhận `response` `RESOURCE_EXHAUSTED`, frame khác và ping quá giới hạn bị bỏ (ping không có pong), kết nối nhận `frame_error{RATE}`, không đóng |
| `WS_COMMAND_INFLIGHT` | `32` | Số lệnh đang chạy tối đa mỗi kết nối; quá thì `RESOURCE_EXHAUSTED` |
| `WS_SEND_QUEUE` | `256` | Hàng frame chờ ghi mỗi kết nối (≥ 2); đầy thì không đóng: event không vừa làm kết nối ngừng nhận event, khi hàng xuống dưới một nửa `WS_SEND_QUEUE` và dưới một nửa `WS_SEND_QUEUE_BYTES` thì nhận một `resync{CLIENT_BACKLOG}` rồi event chạy lại; `ready` hoặc `load_failed` không vừa thì chính frame đó được gửi lại thay cho `resync` (người chưa nạp xong hoặc nạp lỗi không bao giờ nhận `CLIENT_BACKLOG`), `resync` khác và `room_closed` không vừa dẫn tới `resync{CLIENT_BACKLOG}`; giới hạn đã biết: frame trả nợ này chỉ đi khi kết nối có event tiếp theo hoặc khi node nhận một event live bất kỳ, không có timer; `response` và frame điều khiển (`auth_required`, `retry_later`, `frame_error`, pong) không vừa thì bỏ, đếm `send_dropped_total{kind=response\|control}` |
| `WS_SEND_QUEUE_BYTES` | `1048576` | Byte chờ ghi mỗi kết nối (≥ `WS_FRAME_MAX`); vượt thì xử lý như `WS_SEND_QUEUE` đầy (`resync{CLIENT_BACKLOG}` sau khi hàng xuống dưới một nửa, không đóng). Không có ngân sách toàn node: compose đặt `mem_limit 2g`, `GOMEMLIMIT 1600MiB` |
| `WS_WRITE_TIMEOUT` | `10s` | Hạn ghi một frame; quá hạn hoặc ghi lỗi thì cắt socket (máy thấy 1006) |
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

## Cổng và admin

- WebSocket `WS_ADDR` (`:8080`), đường dẫn `/ws`, subprotocol `chatim.v1`; compose publish `websocket-1` ở `127.0.0.1:8081` (`WS1_PORT`) và `websocket-2` ở `127.0.0.1:8083` (`WS2_PORT`).
- Admin `WS_ADMIN_ADDR` (`:9090`, không publish): `/livez`, `/readyz`, `/metrics` (`chatim_websocket_*`). Metric và 9 luật alert websocket/gateway: [module 09 §6.1](../modules/09-feature-realtime-delivery-and-ephemeral.md); vận hành (NTP bắt buộc, deploy từng node): §6.2.
- Log: `make ws-logs` (websocket-1), `make ws-logs NODE=2` (websocket-2). Không token nào được nằm trong log.

## Khoá và token dev

`make dev-keys` chạy `corecli dev-keys -out /keys` trong image `chatim/corecli:dev` với uid của máy, ghi `.local/ws-keys/private.jwk` (0600, chỉ nằm trong `.local/`, đã gitignore) và `.local/ws-keys/jwks.json` (0644); muốn thay khoá thì xoá thư mục rồi chạy lại. Compose chỉ đưa `jwks.json` vào websocket và gateway làm secret `ws_jwks` (`WS_JWT_KEYS_FILE=/run/secrets/ws_jwks`); khoá riêng không bao giờ mount vào container. Token dev (mặc định `-ttl 15m`, `iss` `chatim-dev`, `aud` `chatim-websocket`), dùng được cho cả hai node websocket và gateway-1:

    make -s ws-token ARGS="-tenant e2e -user alice -ttl 10m"
    docker run --rm --user "$(id -u):$(id -g)" -v "$PWD/.local/ws-keys/private.jwk":/keys/private.jwk:ro chatim/corecli:dev token -key /keys/private.jwk -tenant e2e -user alice

## Đổi khoá ký

Từ M6 phần 2: khoá của `iam` xoay không cần khởi động lại (xem [iam.md](iam.md)). Khoá dev trong file JWKS vẫn theo cách cũ, không cắt kết nối đang sống: thêm khoá mới (`kid` mới) vào JWKS bên cạnh khoá cũ → rolling restart các node websocket và gateway (JWKS chỉ đọc lúc khởi động) → chuyển bên ký sang khoá mới → chờ token cũ hết hạn (TTL + `WS_JWT_LEEWAY`) → bỏ khoá cũ khỏi JWKS và rolling restart lần nữa. Nên giữ TTL token ≤ 15 phút để bước chờ ngắn và token lộ hết hạn nhanh.

## Bài tải và diễn tập

Cần `make core-up` trước.

    make poc TOOL=wsload POC_FLAGS="--ulimit nofile=65536:65536" ARGS="-scenario steady"
    ./scripts/wsload-kill.sh
    ./scripts/ws-nats-drill.sh 10 cut

- `wsload` (R4b, logic ở `tools/poc/internal/wsload`): `-scenario steady|burst|kill`, `-conns 10000`, `-groups 500`, `-rate`, `-connect-rate`, `-duration`; 10K user khác nhau trên hai node, group tạo qua gateway; kết quả `bin/bench/wsload-<scenario>-<giờ>/` (`summary.txt`, `summary.json`).
- `scripts/wsload-kill.sh` chạy `-scenario kill`: thấy `kill-ready` thì `docker kill -s KILL chatim-websocket-2` (`WSLOAD_KILL_NODE`), đo tới khi mọi máy `ready` lại, bật lại node (trap cả khi lỗi); `WSLOAD_RATE` (100), tham số thêm chuyển cho công cụ.
- `scripts/ws-nats-drill.sh`: cắt websocket-1 khỏi NATS (iptables trong sidecar) dưới tải corebench + `corecli e2e ws-drill`. `10 cut` (không nhảy), `180 cut` (quá `WS_REPLAY_MAX_AGE` → nhảy + `resync`), `0 restart-nats`; kết quả `bin/drill/ws-nats-*/` (`summary.txt` do `scripts/ws-drill-summary.sh METRICS START RESTORED` in).
