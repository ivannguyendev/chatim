# M4 phần 1: kết nối, token và chuyển lệnh tới core

> **Trạng thái:** owner duyệt 2026-10-20; **đã xây xong** trên `feat/m4` ngày 2026-10-21 (kết quả ở §10, quyết định D183–D188 ở [decision log §17.7](../decision-log.md), giao thức ở [Module 09 §7](../modules/09-feature-realtime-delivery-and-ephemeral.md)). Phần 1 trong 5 phần của M4.
> **Năm phần:** (1) kết nối, token, lệnh · (2) nhận và đẩy event · (3) đã đọc, đã xem · (4) typing, online · (5) reconnect, vận hành, kiểm thử.
> **Nguồn:** hướng M4 owner duyệt 2026-10-20 (D183–D188), hàng M4 [roadmap](../roadmap.md), [exit Phase 1](2026-10-19-phase1-exit-summary.md).

## 1. Phần này làm gì

Điện thoại và trình duyệt chưa gọi được core: core chỉ nhận gRPC nội bộ và tin header "tôi là Lan" mà không kiểm. Phần 1 xây cửa vào:

1. Máy mở kết nối WebSocket tới app `websocket` và đưa token.
2. App kiểm token để biết người dùng là ai, thuộc tenant nào.
3. Mọi lệnh của máy (gửi tin, xem lịch sử, react…) đi qua app tới đúng core, rồi kết quả trả về máy.

Ví dụ:
- Lan mở app, máy kết nối `wss://chat…/ws` và gửi token (người dùng `lan`, tenant `hasaki`).
- App kiểm token hợp lệ.
- Lan gửi "Ok anh" vào nhóm 77. App gắn `tenant=hasaki`, `user=lan` lấy từ token, gọi `SendMessage` tới core đang giữ nhóm 77, rồi trả kết quả (seq 124) về máy Lan.
- Lan sửa máy để gửi lệnh mạo danh Bình: không được, vì người dùng chỉ lấy từ token.

Phần 1 chưa gồm đẩy tin mới xuống máy (phần 2), đã đọc/đã xem (phần 3), typing/online (phần 4), bão reconnect (phần 5).

## 2. Trong và ngoài phần 1

| Việc | Ai làm | Ghi chú |
|---|---|---|
| App mới `apps/websocket`: nhận kết nối, kiểm token, chuyển lệnh | `apps/websocket` | Không database |
| Kiểm JWT | `pkg/jwtverify` (mới, dùng chung) | `identity-verifier` Phase 2 bọc lại |
| Chọn core theo room + giữ kết nối gRPC | `pkg/coreroute` (chuyển từ `tools/internal/route`) | Phần retry ở lại `tools`, vì chỉ tool được retry |
| Khuôn frame | `proto/chatim/v1/ws.proto` (mới) | |
| Client Go mẫu | `pkg/wsclient` (mới) | Dùng cho e2e và làm mẫu cho frontend |
| Cấp token thật, đăng nhập | — | `identity` Phase 2; phần 1 dùng khoá ký dev |
| Core | không đổi | Core vẫn nhận gRPC như hôm nay |

## 3. Kết nối và token

1. Máy mở kết nối. Frame đầu tiên phải là `auth{token}`, trong vòng 5 giây, không thì app đóng mã 4002 (máy nối lại bình thường, không cần đổi token).
   - Token nằm trong frame, không nằm trên URL, để không lọt vào log của proxy.
2. App kiểm token:
   - chữ ký bằng khoá dev Ed25519, chỉ nhận thuật toán `EdDSA`;
   - khoá có mã `kid` nằm trong file khoá (nhiều khoá để xoay vòng);
   - đúng nơi cấp (`iss`) và nơi nhận (`aud`);
   - còn hạn (`nbf`, `exp`, cho lệch giờ 30 giây);
   - `sub` (người dùng) và `tid` (tenant) đúng bộ ký tự id của chatim.
   Sai bất kỳ điều nào → đóng mã 4001.
3. Hợp lệ → app nhớ (người, tenant, hạn token) cho kết nối này.
4. **Gia hạn:** trước khi token hết hạn, máy gửi `auth` với token mới, cùng người và tenant. Không gia hạn thì quá hạn (cộng 30 giây lệch giờ) app đóng 4001; máy lấy token mới rồi mới kết nối lại.
5. **Giữ kết nối sống:** máy gửi `heartbeat` mỗi 5–10 giây (client mẫu chọn 5–8 giây để còn dư). Mọi frame đều tính là còn sống, kể cả WebSocket ping. 15 giây không có frame nào → app đóng kết nối (mã 1000). Phần 4 dùng chính heartbeat này cho online/offline.
6. **Máy không chịu đọc:** app ghi trả lời qua một hàng đợi riêng của kết nối, mỗi lần ghi có hạn 10 giây. Máy không đọc (hoặc mạng treo) thì app cắt kết nối, để một máy hỏng không giữ tài nguyên của node mãi.
7. **Một lỗi lạ do máy gây ra** chỉ làm đóng kết nối đó (mã 1011), không làm sập node đang giữ hàng nghìn kết nối khác.

Ví dụ token hết hạn lúc 11:00. Lúc 10:55 máy Lan lấy token mới và gửi `auth`, kết nối tiếp tục, không rớt.

## 4. Chuyển lệnh tới core

1. Máy gửi `request{id: 17, SendMessage{room 77, cid "c-9", text "Ok anh"}}`. `id` do máy đặt để khớp câu trả lời.
2. App kiểm giới hạn tốc độ của kết nối (mặc định 50 lệnh/giây, dồn tối đa 100), để chống lạm dụng.
3. App kiểm lệnh có trong **danh sách cho phép** (bảng dưới, 33 lệnh). Field cấm có giá trị → trả `INVALID_ARGUMENT`. Field app không biết bị bỏ, không bao giờ tới core. Một test ghim toàn bộ field (kể cả field lồng bên trong và các giá trị enum) của từng lệnh: core thêm field mới thì test đỏ, phải có người quyết field đó có cho máy dùng không. Vì vậy, field mới của core không tự lọt ra cho máy dùng, kể cả lúc core đã nâng cấp mà websocket chưa.
4. App gắn `x-chatim-tenant` và `x-chatim-user` lấy từ token. Máy không bao giờ đặt được hai header này.
5. App chọn core:
   - lệnh của một room → core đang giữ slot của room (bảng slot đọc từ Redis state, như tool hôm nay);
   - lệnh không thuộc room (`CreateRoom`, `OpenDirectRoom`, `ListMyRooms`, `GetRooms`, `GetReactionSettings`, `ListBookmarks`, `ListMentions`) → core bất kỳ.
6. Gọi core với hạn 5 giây, rồi trả `response{id: 17, mã, kết quả}`.

**Lỗi:** core trả mã gì thì app trả đúng mã đó. Lời lỗi nghiệp vụ (sai tham số, không tìm thấy, không có quyền…) giữ nguyên; lời lỗi hạ tầng (core không sẵn sàng, quá hạn) thay bằng câu cố định, vì lời gốc có đường dẫn nội bộ và địa chỉ IP. **App không tự gửi lại** (luật không retry nội bộ). Riêng khi gặp `UNAVAILABLE`, app làm mới bảng slot để lần máy gửi lại đi đúng core. Máy gửi lại cùng `cid` nên không sinh tin trùng.

**Thứ tự:** hai `SendMessage` liên tiếp của cùng một kết nối vào cùng một room đi theo thứ tự gửi, để "A" rồi "B" không thành "B" rồi "A". Nếu A lỗi do core không sẵn sàng hoặc quá hạn, B đang chờ phía sau cũng trả cùng lỗi ngay mà không gửi; máy gửi lại A rồi B theo đúng thứ tự. Kết nối đóng thì các tin đang chờ bị bỏ (máy gửi lại khi nối lại, cùng `cid`). Các lệnh khác chạy song song.

**Máy mới hơn app:** máy gửi loại lệnh app chưa biết thì nhận `UNIMPLEMENTED`, không bị đóng kết nối (app di động thường cập nhật trước server).

| Nhóm | Cho phép | Chặn |
|---|---|---|
| Room | `CreateRoom`, `OpenDirectRoom`, `ListMyRooms`, `GetRooms`, `UpdateRoom`, `SetRoomPreferences` | — |
| Tin | `SendMessage`, `GetHistory`, `GetMessages`, `EditMessage`, `DeleteMessage`, `HideMessage`, `ClearHistory`, `GetEditHistory` | Trong `SendMessage`: `kind` khác mặc định và `TEXT`, `skip_unread`, `meta` (chỉ app nội bộ gửi tin hệ thống) |
| React, ghim, thread | `ReactMessage`, `PinMessage`, `UnpinMessage`, `ListPins`, `GetReactionSettings`, `GetReactions`, `SetBookmark`, `GetReplies`, `ListThreads`, `ListBookmarks` | — |
| Thành viên | `AddMembers`, `RemoveMember`, `LeaveRoom`, `ChangeMemberRole`, `SetMemberPriority`, `ListMembers`, `MarkUnread` | `MarkRead`: đi qua frame riêng ở phần 3 |
| Mention | `ListMentions`, `ListRoomGroups` | `SetUserGroups`, `SetGroups`: do hệ thống user/nhóm gọi |

## 5. Frame của phần 1

| Frame | Hướng | Nội dung |
|---|---|---|
| `auth` | máy → app | token |
| `heartbeat` | máy → app | rỗng, mỗi 5–10 giây |
| `request` | máy → app | `id` + một request trong danh sách cho phép |
| `response` | app → máy | `id`, mã gRPC, kết quả hoặc lời báo lỗi |
| đóng 4001 | app → máy | token sai hoặc hết hạn: lấy token mới rồi mới nối lại |
| đóng 4002 | app → máy | không gửi token kịp 5 giây: nối lại bình thường |
| đóng 1008 / 1003 / 1009 | app → máy | sai giao thức / gửi frame chữ thay vì nhị phân / frame quá lớn |
| đóng 1001 | app → máy | node đang tắt: nối lại |
| đóng 1011 | app → máy | lỗi lạ khi xử lý kết nối này: nối lại |
| đóng 1000 | app → máy | 15 giây không có frame nào: nối lại |

Frame mã hoá protobuf và dùng lại đúng message request/response sẵn có của core. Không có kênh JSON (bỏ D17); khi cần debug thì dùng `corecli` để giải mã. Chưa bật nén (`permessage-deflate`): nén tốn CPU khi có 100K kết nối; bật hay không quyết ở phần 5 khi có số đo.

## 6. Config

| Biến | Mặc định | Vì sao |
|---|---|---|
| `WS_ADDR`, `WS_ADMIN_ADDR` | `:8080`, `:9090` | cổng WebSocket; `/livez`, `/readyz`, `/metrics` |
| `REDIS_ADDR`, `REDIS_PASSWORD_FILE` | — | đọc bảng slot (chỉ đọc) |
| `WS_NODE_ID` | hostname | tên node trong log và tên client Redis `chatim-websocket-{node}` |
| `WS_JWT_KEYS_FILE`, `WS_JWT_ISSUER`, `WS_JWT_AUDIENCE`, `WS_JWT_LEEWAY` | —, —, —, 30s | kiểm token |
| `WS_AUTH_TIMEOUT` | 5s | kết nối không gửi token thì đóng |
| `WS_UNAUTH_MAX` | 10.000 | số kết nối chưa đưa token mỗi node, chống mở kết nối rác |
| `WS_FRAME_MAX` | 128KB | lệnh lớn nhất: `AddMembers`/`CreateRoom` 1.000 người × id 64 byte ≈ 66KB |
| `WS_HEARTBEAT_TIMEOUT` | 15s | không có frame nào thì đóng (owner) |
| `WS_COMMAND_RATE`, `WS_COMMAND_BURST` | 50/s, 100 | chống lạm dụng mỗi kết nối; tính cả `auth`, `heartbeat` và ping (ba loại này vượt thì đóng 1008) |
| `WS_COMMAND_INFLIGHT` | 32 | số lệnh đang chạy + đang chờ mỗi kết nối, chặn bộ nhớ và goroutine |
| `WS_SEND_QUEUE`, `WS_WRITE_TIMEOUT` | 256 frame, 10s | máy không đọc không được giữ tài nguyên node mãi |
| `CORE_REQUEST_DEADLINE` | 5s | hạn mỗi lệnh gọi core |
| `WS_DRAIN_DELAY`, `WS_SHUTDOWN_BUDGET` | 2s, 15s | khi tắt: chờ load balancer thôi gửi kết nối mới, rồi trả nốt lệnh đang chạy |

## 7. Quyết định

| Chọn | Bỏ, vì sao |
|---|---|
| Token trong frame `auth` đầu tiên | Trên URL: lọt vào log proxy. Trong header: trình duyệt không đặt được header cho WebSocket |
| Danh sách cho phép, chép từng field | Danh sách chặn: field mới của core tự lọt ra máy |
| App không retry | Retry trong app trái luật không retry nội bộ; máy đã có `cid` để gửi lại an toàn |
| Giữ thứ tự `SendMessage` theo (kết nối, room) | Để máy tự chờ: mọi SDK phải tự làm, dễ sai |
| Bỏ kênh JSON (D17) | Giữ: test gấp đôi chỉ để debug |
| `pkg/coreroute` chuyển từ tool, bỏ phần retry | Viết mới: trùng code; giữ retry: trái luật cho app |

## 8. Rủi ro

- **Khoá ký dev:** chạy đúng nhưng chưa đủ điều kiện go-live cho tới khi có `identity`.
- **App ↔ core vẫn là mạng tin cậy:** ai vào được mạng nội bộ thì gọi thẳng core được. mTLS làm ở M5.
- **Không thu hồi token:** token bị lộ dùng được tới khi hết hạn. Khuyến nghị token sống ≤ 15 phút; `identity` Phase 2 lo việc cấp lại.
- **Xoay khoá ký cần khởi động lại node lần lượt:** thêm khoá mới vào file khoá → khởi động lại từng node → bên ký chuyển sang khoá mới → bỏ khoá cũ.
- **Websocket giữ mật khẩu Redis state** (chỉ để đọc bảng slot): tách user Redis chỉ đọc ở M5.

**Để phần sau:** trần số kết nối mỗi người và trần lần gọi core của cả node (phần 5).

## 9. Kiểm thử phần 1

| Test | Chứng minh |
|---|---|
| Unit `pkg/jwtverify` | Từ chối thuật toán khác `EdDSA`, sai `kid`/`iss`/`aud`, hết hạn, `sub`/`tid` sai ký tự; gia hạn phải cùng người và tenant |
| Unit kết nối | Không `auth` trong 5 giây → 4002; quá `WS_UNAUTH_MAX` → từ chối; 15 giây không frame → đóng; token hết hạn không gia hạn → 4001; máy không đọc → bị cắt, không rò tài nguyên; panic trong một lệnh → chỉ đóng kết nối đó |
| Unit lệnh | 33 lệnh đi đúng core; field cấm → `INVALID_ARGUMENT`; field lạ bị bỏ; ghim field đệ quy + enum; header lấy từ token; không retry; lời lỗi hạ tầng không lộ địa chỉ; thứ tự `SendMessage` và cách trượt khi lỗi; giới hạn tốc độ |
| Unit `pkg/coreroute` | Chọn đúng core theo slot; lệnh không thuộc room đi core bất kỳ; tự bỏ địa chỉ core cũ; nghìn lần làm mới dồn lại chỉ thành ≤ 2 lần đọc Redis |
| e2e (`pkg/wsclient`, 2 core + 1 node) | Kết nối, gửi tin, xem lịch sử, sửa, react qua websocket ra cùng kết quả như gọi gRPC trực tiếp; lệnh bị chặn trả đúng mã |

## 10. Kết quả thực thi

Xây xong trên `feat/m4` ngày 2026-10-21, 12 task, đã push. Task rủi ro (2, 3, 6, 7, 8) có một reviewer một lượt; mọi lỗi Critical/Important đã sửa trong commit tiếp theo.

### 10.1. Commit theo task

| Task | Việc | Commit |
|---|---|---|
| 0 | Nâng trần file code lên 350 dòng (owner); bản tóm tắt này + dòng backlog "tenant ẩn nội dung tin" | `4363410`, `bc1d3aa` |
| 1 | Trích từ core ra `pkg/`: `pkg/metrics`, `pkg/redact`, `envconfig.Secret`, `admin.Probe` | `48d3ab3`, `f800887`, `cbd88df`, `7911a1a` |
| 2 | `pkg/coreroute` (chọn core, không retry); sửa sau review: giữ kết nối khi bảng slot rỗng, làm mới cách nhau ≥ 250ms; sửa một dòng `INDEXES.csv` | `bb97785`, `5911ecf`, `f768a19` |
| 3 | `pkg/jwtverify` (JWKS Ed25519) và `tools/internal/devtoken` | `8652d7d` |
| 4 | `ws.proto`: frame `auth`, `heartbeat`, `request`/`response`, 33 lệnh ở số field 10–42 | `d4dc32b` |
| 5 | Config của websocket + luật depguard | `f865350`, `ad75f14`, `4a4e4a3` |
| 9 | `pkg/wsclient` (client Go mẫu) | `b3b49df` |
| 6 | Chuyển lệnh tới core (danh sách cho phép, lời lỗi, metric); sửa sau review | `d2a5106`, `98d689e` |
| 7 | Kết nối: auth, heartbeat, giới hạn, thứ tự `SendMessage`; sửa sau review | `3744aa3`, `46bfdbb` |
| 8 | Histogram trong `pkg/metrics`; nối app (admin, metric, tắt êm); sửa sau review | `08a2d90`, `9c5bda2`, `08dd5e3` |
| 10 | Node `websocket-1` trong compose, khoá ký dev (`make dev-keys`, `make ws-token`, `make ws-logs`) | `797395c` |
| 11 | e2e phase 9 qua websocket | `ae11be4` |
| 12 | Tài liệu (decision log D183–D188, Module 09 §7, roadmap, `INDEXES.csv`, `CLAUDE.md`) | commit docs của task này |

### 10.2. Khác với bản duyệt (đã sửa vào thân bài ở trên)

- **`SendMessage` nhận `kind = TEXT`:** core coi `TEXT` như mặc định nên chặn nó chỉ làm khó client; vẫn chặn `SYSTEM` và mọi giá trị khác.
- **Tên biến:** `WS_ADDR`/`WS_ADMIN_ADDR` (thay `WS_LISTEN`/`ADMIN_LISTEN`) cho giống core; thêm `WS_NODE_ID` (mặc định hostname).
- **WebSocket ping cũng tính là còn sống** (sửa sau review Task 7). 15 giây không frame nào → đóng mã 1000.
- **Vượt tốc độ ở `auth`/`heartbeat`/ping → đóng 1008** (không có `id` để trả `RESOURCE_EXHAUSTED`).
- **Panic chỉ đóng kết nối đó với mã 1011**, có recover ở goroutine ghi, hàng theo room và các hẹn giờ.
- **`room_id` sai dạng trả câu cố định** "invalid room_id", không lặp lại chuỗi máy gửi.
- **Máy không đọc:** hàng đợi đầy hoặc ghi quá 10 giây thì cắt thẳng socket, không gửi close frame (máy thấy 1006); mã 4008 để phần 2.
- **Client mẫu** gửi heartbeat ngẫu nhiên 5–8 giây (dùng `crypto/rand`).
- **Chép request bằng Marshal + Unmarshal bỏ field lạ** (thay vì chỉ bỏ field lạ ở frame ngoài), nên field lạ bị bỏ ở mọi tầng lồng.

### 10.3. Kiểm thử

- `make fmt-check`, `make vet`, `make lint`, `make test` xanh; `make itest` xanh khi chạy lại (một test `mongostore` chập chờn, xem 10.4).
- `make core-up && make e2e`: **phase 1–9 xanh**. Phase 9 qua `websocket-1`: token dev kết nối, gửi tin, xem lịch sử, sửa, react, bookmark ra **cùng kết quả** với đọc gRPC trực tiếp; `SendMessage` `kind system` → `INVALID_ARGUMENT`; token ký bằng khoá lạ và token hết hạn → 4001; kết nối im lặng → 4002 sau 5 giây; log của websocket không chứa token nào đã dùng.
- Không bench ở phần 1 (tải 10K kết nối và bão reconnect ở phần 5).

### 10.4. Còn lại

- **`make vuln`:** owner đồng ý nâng Go 1.26.9 → 1.26.10 (2026-10-21) để sửa GO-2026-6809 (`crypto/x509`); sau khi nâng, `make vuln` không còn lỗ hổng nào code chạm tới. GO-2026-5932 (`x/crypto`) vẫn nằm trong module nhưng code không gọi tới, chưa có bản vá.
- **Test chập chờn:** `mongostore` `TestReactionSetOfTheSameUserRacingOnANewReactionNeverFails` lỗi một lần trong `make itest` (Mongo `DurationOverflow`), chạy lại 3/3 qua; cần xem riêng.
- **Việc nhỏ ghi lại (Minor, không chặn):**
  - `pkg/coreroute`: dọn địa chỉ cũ đóng kết nối ngay, nên lệnh đang chạy tới core vừa rời bảng bị huỷ (máy gửi lại); `Room()` có thể dial lại địa chỉ vừa bị dọn (lần nạp sau dọn tiếp); dial chạy trong lúc giữ `Pool.mu` (ổn vì gRPC dial lười, không chờ mạng).
  - `pkg/jwtverify` (làm khi `identity-verifier` dùng lại): giới hạn độ dài token trong thư viện (websocket đã chặn frame 128KB); từ chối header `crit`; `devtoken.KeyPair` nên có `String()` không in khoá riêng; chưa từ chối token thiếu `exp` (`ExpiresAt` nil) và `iat` ở tương lai; `ConstantTimeCompare` thay được bằng `bytes.Equal`.
  - Kết nối: mã đóng do gws tự chọn (1009/1002/1000) có thể tới máy thành 1006 khi hạn ghi đã hết; chưa kiểm `Origin` (token nằm trong frame nên không phải rủi ro auth; owner 2026-10-25: allowlist `WS_ALLOWED_ORIGINS` ở M5); nhãn 1000 của `closes_total` gồm cả hết hạn heartbeat.
  - Lệnh rỗng (`UNIMPLEMENTED`) đếm ở `commands_rejected_total`, chưa ở `commands_total`.
  - `router.Close()` không nhận ctx nên có thể vượt ngân sách tắt (core cũng vậy).
  - Luật alert cho websocket: đã thêm ở phần 5 (`deploy/prometheus/alerts.yml`).
- **Để phần sau như đã duyệt:** trần kết nối mỗi người và trần lần gọi core của cả node (phần 5); user Redis chỉ đọc và mTLS app ↔ core (M5); `ListMentions` tin `groups` máy gửi (D188).
