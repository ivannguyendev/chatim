# M6 RF3 — đổi tên app `core` thành `chat`, gateway tìm node chat qua Redis (tóm tắt)

> Ngày 2026-11-05 · Nhánh `feat/m6` · Trạng thái: **chờ owner duyệt bản sửa** (bản đầu duyệt 2026-11-05; owner 2026-11-05 thêm: rà env theo instance, gateway đọc sổ node trong Redis) · Phase 3/3 của đợt refactor trước M7 (RF1 `pkg/core` ✅ → RF2 `pkg/auth` + `pkg/chatrpc` ✅ → **RF3**), cùng PR M6 (owner 2026-11-04). Đổi tên không đổi hành vi nghiệp vụ, dữ liệu Mongo hay subject NATS; một thay đổi hành vi có chủ đích: gateway bỏ danh sách địa chỉ cố định (§5).

## 1. Scope và ranh giới

| Việc | Trong scope | Ai giữ |
| :- | :- | :- |
| `apps/core` → `apps/chat` (886 file, chỉ chuyển chỗ + đổi import) | Có | app `chat` |
| Proto `CoreService` → `ChatService`, `core.proto` → `chat.proto`, `core_service.proto` → `chat_service.proto`, sinh lại `pkg/pb` | Có (owner 2026-11-04 câu 1a) | `proto/`, `pkg/pb` |
| Env, metric, alert, Redis key, tên client Mongo/Redis/NATS, compose, Makefile, scripts, tools, symbol Go, chuỗi log/lỗi (bảng §4) | Có | mọi app, `pkg/slotmap`, `pkg/chatrpc`, `modules/mention`, `tools/`, `deploy/` |
| Gateway bỏ `GW_CORE_ADDRS`, đọc sổ node chat trong Redis như websocket (§5) | Có (owner 2026-11-05) | `apps/gateway` |
| Dọn biến cổng dev đánh số và trỏ identity qua một tên chung (§6) | Có (owner 2026-11-05) | `deploy/compose`, `.env.example`, README |
| `.golangci.yaml` (73 chỗ `apps/core`), `INDEXES.csv`, doc đang dùng, `.memory/` | Có | luật lint, doc |
| Một D mới ở `docs/decision-log.md` gom RF1–RF3, bảng đổi tên và quyết định gateway (thay phần (3) và (7) của D198) | Có | decision log |
| `pkg/core` và chữ "core" không phải tên app: "core NATS" (28 chỗ), `score`, `ZRANGEBYSCORE`, `underscore` | Không đổi | giữ nguyên |
| Lịch sử: D1–D260, `docs/plans/*-summary.md`, `docs/poc/results/`, `docs/research/` | Không sửa chữ | chỉ sửa link trỏ `docs/apps/core.md` |
| Database Mongo, collection, stream `CHATIM_*`, durable, subject `evt.*`/`eph.*`, frame websocket | Không đổi (không chứa `core`) | — |
| Repo client SDK sinh lại từ proto mới; mTLS giữa các service | Không | repo client (D206); M5 |

Lớp dữ liệu §4, event, field DB: **không áp dụng**.

## 2. Thuật ngữ

- **App chat**: chương trình hôm nay tên `core` (room, member, message, seq, event).
- **Node chat**: một bản đang chạy của app chat (`chat-1`, `chat-2`), giống "node websocket" (`WS_NODE_ID`).
- **Sổ node**: danh sách node chat đang sống trong Redis state. Mỗi node tự ghi tên + địa chỉ và gia hạn đều; node chết thì hết hạn và rơi khỏi sổ. Websocket đọc sổ này từ M4 (D186).
- **Đổi theo danh sách**: chỉ đổi tên có trong bảng §4 và plan, không `sed core→chat` toàn repo.

## 3. Bức tranh đổi tên

Hôm nay người vận hành gõ `make core-up`, thấy container `chatim-core-1`, đặt `CORE_ID=core-1`, xem metric `chatim_core_counter_repaired_total`; client gọi `POST /chatim.v1.CoreService/ListMyRooms`. Sau RF3: `make app-up`, `chatim-chat-1`, `CHAT_NODE_ID=chat-1`, `chatim_chat_counter_repaired_total`, `POST /chatim.v1.ChatService/ListMyRooms`.

Quy tắc: chữ `core` chỉ **app** → `chat`; chỉ **một bản đang chạy** → `chat node`/`Node`. Lý do: trong sản phẩm chat, `chatim:chats` hay `CHAT_ID=chat-1` đọc như "danh sách cuộc chat", "id một cuộc chat".

## 4. Bảng đổi tên

| Nhóm | Cũ → mới |
| :- | :- |
| Path | `apps/core` → `apps/chat`; `docs/apps/core.md` → `docs/apps/chat.md`; `scripts/wait-core-healthy.sh` → `scripts/wait-healthy.sh` |
| Proto, URL | `CoreService` → `ChatService`; gateway `POST /chatim.v1.ChatService/{Lệnh}`. Frame websocket không đổi |
| Env app chat | `CORE_*` → `CHAT_*`; riêng `CORE_ID` → `CHAT_NODE_ID` (để trống thì lấy hostname, như hôm nay) |
| Env app khác | `CORE_REQUEST_DEADLINE` (websocket, gateway) → `CHAT_REQUEST_DEADLINE`; `GW_CORE_ADDRS` **bỏ** (§5) |
| Makefile, compose | `make core-up/core-down` → `make app-up/app-down` (bật cả 4 app); `CORE_COMPOSE` → `APP_COMPOSE`, `CORES` → `CHAT_NODES`, `CORECLI_IMAGE` → `CHATCLI_IMAGE`, `CORE_GOGC` → `CHAT_GOGC`, `CORE_WAIT_SECONDS` → `HEALTHY_WAIT_SECONDS`; image `chatim/chat:dev`, service `chat-1/2`, container `chatim-chat-1/2` |
| Metric | `chatim_core_*` → `chatim_chat_*`; `chatim_{websocket,gateway}_core_call_seconds` → `..._chat_call_seconds` |
| Alert | `chatim-core-guarantees` → `chatim-chat-guarantees`, `ChatimGatewayCoreUnavailable` → `ChatimGatewayChatUnavailable` |
| Redis | `chatim:cores` → `chatim:chat-nodes`, `chatim:core:<id>` → `chatim:chat-node:<id>` |
| Tên client | `chatim-core*` → `chatim-chat*`; `chatim-corecli`/`chatim-corebench` → `chatim-chatcli`/`chatim-chatbench` |
| Tools | `corecli` → `chatcli`, `corebench` → `chatbench`; cờ `-cores` → `-nodes`; `E2E_KILL=chat-1\|chat-2` |
| Symbol Go | app → `Chat...` (`ChatServiceClient`, `Clients.Chat`, `ChatCallLatency`, `mentionapp.ChatReader`); bản chạy → `Node...` (`NodeID`, `slotmap.Route.Node`, `NodeKey`, `MaxNodes`) |
| Chuỗi | `core ready` → `chat ready`; thông điệp client `core did not answer in time` → `chat did not answer in time` (mã gRPC giữ) |

## 5. Gateway tìm node chat qua sổ Redis

**Hôm nay:** gateway nhận HTTPS từ điện thoại (tải danh sách phòng, lịch sử, tạo phòng…) rồi chuyển từng yêu cầu sang chat qua gRPC, chia lần lượt cho các địa chỉ ghi sẵn trong `GW_CORE_ADDRS=chatim-core-1:9000,chatim-core-2:9000` (D198 (3)). Chạy 10 node thì phải ghi đủ 10 địa chỉ; bật node thứ 11 thì gateway không biết, phải sửa env và khởi động lại; tắt node thì gateway vẫn gọi vào địa chỉ đã tắt.

**Sau RF3:** gateway đọc sổ node trong Redis state, đúng cách websocket đang làm (`chatrpc.OpenRouter`), mỗi yêu cầu gọi `Router.Any()` để xoay vòng qua các node đang sống. Ví dụ: bật `chat-11` → nó tự ghi vào sổ, Redis báo thay đổi, gateway đọc lại sau vài giây và bắt đầu gửi yêu cầu tới `chat-11`; env không đổi.

- **Env mới của gateway**: `REDIS_ADDR`, `REDIS_DB`, `REDIS_PASSWORD` (Redis state, cùng tên với websocket và chat). Bỏ `GW_CORE_ADDRS`.
- **Node chết đột ngột**: node còn trong sổ tới khi hết hạn gia hạn (5s). Gateway bỏ qua node có kết nối đang hỏng khi chọn (như hôm nay `round_robin` của gRPC tự né), nên chỉ yêu cầu đang chạy trên node đó trả `UNAVAILABLE` (không retry); gateway kích đọc lại sổ, máy gửi lại và đi node khác. Dừng êm khi deploy: node tự gỡ khỏi sổ và vẫn phục vụ trong thời gian drain, không lỗi.
- **Redis state chết**: gateway giữ sổ đang có và mọi kết nối, vẫn phục vụ (như websocket, D186).
- **Sẵn sàng (`/readyz`)**: lúc khởi động chờ đọc được sổ (và identity như hôm nay) rồi mới bật, giống websocket. Đang chạy mà sổ không còn node nào thì `/readyz` vẫn bật, lệnh trả `UNAVAILABLE`: tắt mọi gateway khỏi load balancer chỉ làm lỗi tệ hơn (lý do của D186). Đổi so với D198 (7) "không core → 503".
- Lệnh của gateway (đọc thẳng Mongo, quản lý không qua actor) đúng ở node nào cũng được (D198), nên chỉ cần `Any()`, không chọn theo slot.

## 6. Dọn env theo instance (chỉ compose dev, app không đọc)

| Biến | Có ai dùng không | Sau RF3 |
| :- | :- | :- |
| `CORE1_GRPC_PORT`, `CORE2_GRPC_PORT` | không | xoá, không mở cổng gRPC chat ra host (caller còn được tin qua header tới M5) |
| `WS2_PORT` (8083), `IDENTITY2_PORT` (8085) | không, chỉ README nhắc | xoá, không mở cổng ra host |
| `WS1_PORT` (8081), `GW1_PORT` (8082), `IDENTITY1_PORT` (8084) | có (curl, ví dụ trong doc) | giữ cổng loopback cố định trong compose, bỏ biến |
| `WS_/GW_IDENTITY_INTERNAL_URL`, `..._JWKS_URL` trỏ `chatim-identity-1` | identity-1 chết thì dev không ai trả lời | trỏ tên chung `chatim-identity` (alias mạng cho cả hai identity), giống URL service ở môi trường thật |

`CHAT_NODE_ID`/`WS_NODE_ID` trong compose giữ để log và e2e dễ đọc; môi trường thật để trống, mọi pod chung một bộ env.

## 7. Tính đúng

- Đổi tên không đổi logic; key `_id`, collection, subject, stream không chứa `core`; dữ liệu Mongo cũ đọc như cũ.
- Key Redis sổ node, tên method gRPC và env đổi → mọi app chạy cùng một bản (cùng image tag). Dev: `make infra-reset` trước e2e.
- Gateway dùng lại `chatrpc.Router` đã chạy ở websocket; không hàng đợi, khoá hay goroutine mới ngoài hai goroutine của router (đọc sổ, làm mới).
- Luật lint trỏ path không còn tồn tại vẫn qua lint nhưng mất tác dụng → đổi đủ 73 chỗ, đếm lại và kiểm âm.

## 8. Quyết định và phương án bị loại

| Quyết định | Loại | Lý do loại |
| :- | :- | :- |
| Gateway đọc sổ node trong Redis | Giữ danh sách cố định `GW_CORE_ADDRS` | không theo kịp khi thêm/bớt node |
| Gateway đọc sổ node trong Redis | Headless DNS (`dns:///chatim-chat:9000`) | gRPC Go chỉ hỏi lại DNS khi kết nối đứt: node mới chờ tới lúc làm mới kết nối (30 phút hôm nay); cần dựng headless service/alias riêng từng môi trường; lệch với websocket |
| Gateway đọc sổ node trong Redis | Istio chia tải | thêm proxy mỗi pod và một hạ tầng lớn; mặc định tự gửi lại khi `UNAVAILABLE`, trái luật không retry; code không được phụ thuộc mesh. mTLS cân nhắc ở M5 |
| `/readyz` không tắt khi sổ rỗng | Giữ D198 (7) | tắt mọi gateway khỏi load balancer khi chat chập chờn làm lỗi tệ hơn (D186) |
| `chat` cho app, `chat node` cho bản chạy | `chat` cho mọi thứ (`chatim:chats`, `CHAT_ID`) | đọc như danh sách/id cuộc chat |
| `make app-up/app-down` | `chat-up/chat-down` | target bật cả 4 app |
| Đổi theo danh sách + grep allowlist | `sed core→chat` toàn repo | phá `pkg/core`, "core NATS", `score`, lịch sử |
| Đổi key Redis, `make infra-reset` | Đọc cả key cũ và mới một thời gian | chưa go-live, không có dữ liệu cần giữ |

## 9. Chi phí và tải

Không đổi chi phí chạy. Gateway thêm một kết nối Redis state (đọc sổ, nghe thông báo thay đổi) và một kết nối gRPC mỗi node chat, như websocket. Diff khoảng 1.100 file (886 chỉ chuyển chỗ), 4 commit:

1. `refactor(chat): move apps/core to apps/chat` — chuyển chỗ, sửa import, path trong lint, Makefile, scripts, doc nhắc tên đã chuyển.
2. `refactor(chat): rename core to chat in names, env, metrics and keys` — proto, symbol, env, metric, alert, Redis key, compose, dọn env §6, toàn bộ doc đang dùng.
3. `feat(gateway): find chat nodes through the redis node table` — §5, test, doc gateway.
4. `docs(m6): record the core to chat rename` — `.memory/`, D mới.

## 10. Rủi ro thật

- Sót tên trong script, compose, alert, e2e → bắt bằng `make app-up && make e2e`, `make alerts-check`, grep cuối phase.
- Gateway khởi động khi Redis state đang chết thì thoát lỗi và được khởi động lại tới khi đọc được sổ (hôm nay gateway không cần Redis state) → giống websocket; gateway đang chạy thì không ảnh hưởng.
- URL gateway đổi: repo client SDK phải sinh lại từ proto mới và đổi cùng lúc (owner đã chấp nhận 2026-11-04).

## 11. Điểm đội tự chọn (owner phản biện)

- Quy tắc app/node §3 và các tên `CHAT_NODE_ID`, `chatim:chat-nodes`, `Route.Node`, cờ `-nodes`.
- Gateway dùng env `REDIS_*` không tiền tố, cùng tên với websocket và chat.
- `/readyz` của gateway theo websocket (§5).
- Doc viết app là `chat`; chỗ dễ nhầm viết "app chat"/"node chat".

## 12. Kiểm thử

| Kiểm | Chứng minh |
| :- | :- |
| `make proto`, `make buf-lint`, `make fmt-check`, `make vet`, `make lint` | proto, import, tên, luật lint trỏ path mới |
| `make test` | test unit chạy như cũ dưới tên mới |
| Test gateway với Redis giả (miniredis) và node chat giả | gateway chia yêu cầu qua mọi node trong sổ; node chết → `UNAVAILABLE` một lần; sổ rỗng → `UNAVAILABLE`, `/readyz` vẫn bật; Redis không tới được lúc khởi động → gateway thoát lỗi, không bật `/readyz`. Node mới vào/rơi khỏi sổ đã có test ở `pkg/chatrpc` (websocket dùng chung) |
| Test chọn node của `pkg/chatrpc` | bỏ qua node có kết nối hỏng; mọi node hỏng thì vẫn xoay vòng và trả `UNAVAILABLE` |
| `make infra-reset`, `make itest` | wiring chat, websocket, gateway với Mongo/Redis/NATS thật, key Redis mới |
| R5 một lần (`./apps/chat/internal/platform/slot/`) | slot và `pkg/slotmap` đổi tên vẫn đúng |
| `make alerts-check` | alert trỏ metric mới |
| `make app-up && make e2e` | compose, Makefile, scripts, URL gateway, gateway đọc sổ, alias identity, kill `chat-1` chạy hết 14 phase |
| Kiểm âm depguard trên cây cũ rồi cây mới | luật ranh giới theo path mới vẫn đỏ, cùng tên luật |
| `git grep -P` chữ `core` (tự kiểm trên cây cũ trước) | chỉ còn allowlist: lịch sử, `pkg/core`, "core NATS", `score` |

## 13. Bảng phủ

| Yêu cầu | Thành phần | Task | Test | Bằng chứng |
| :- | :- | :- | :- | :- |
| DL#7 đổi app, proto, env, metric, Redis, compose, tools | mọi app, `pkg/slotmap`, `pkg/chatrpc`, `deploy/`, `tools/` | commit 1, 2 | `test`, `itest`, `e2e`, `alerts-check` | điền sau khi thực thi |
| DL#8 doc đang dùng đổi, lịch sử giữ, D mới | doc, `.memory/`, `docs/decision-log.md` | commit 1, 2, 4 | grep allowlist | điền sau khi thực thi |
| DL#9 không sed hàng loạt | toàn phase | commit 1–2 | grep hai chiều | điền sau khi thực thi |
| DL#11 symbol Go | Go | commit 2 | `vet`, `lint`, `test` | điền sau khi thực thi |
| DL#12 `wait-healthy.sh` | `scripts/`, Makefile | commit 1 | `make app-up` | điền sau khi thực thi |
| DL#13 luật lint theo `apps/chat` | `.golangci.yaml` | commit 1 | `make lint` + đếm 73 + kiểm âm | điền sau khi thực thi |
| Gateway đọc sổ node (owner 2026-11-05) | `apps/gateway`, `pkg/chatrpc` | commit 3 | test gateway, `itest`, `e2e` | điền sau khi thực thi |
| Dọn env theo instance, alias identity (owner 2026-11-05) | compose, `.env.example`, README | commit 2 | `make app-up`, `e2e` | điền sau khi thực thi |
| `pkg/slotmap` đổi → R5 | `apps/chat/internal/platform/slot` | commit 2 | R5 | điền sau khi thực thi |
