# M4 phần 1: kết nối, token và chuyển lệnh tới core

> **Trạng thái:** chờ owner duyệt. Phần 1 trong 5 phần của M4; phần sau chỉ viết khi phần này đã duyệt.
> **Năm phần:** (1) kết nối, token, lệnh · (2) nhận và đẩy event · (3) đã đọc, đã xem · (4) typing, online · (5) reconnect, vận hành, kiểm thử.
> **Nguồn:** hướng M4 owner duyệt 2026-10-20 (`.claude/plans/m4-websocket_design.md`), hàng M4 [roadmap](../roadmap.md), [exit Phase 1](2026-10-19-phase1-exit-summary.md).

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
5. **Giữ kết nối sống:** máy gửi `heartbeat` mỗi 5–10 giây (client mẫu chọn 5–8 giây để còn dư). 15 giây không có frame nào → app đóng kết nối. Phần 4 dùng chính heartbeat này cho online/offline.
6. **Máy không chịu đọc:** app ghi trả lời qua một hàng đợi riêng của kết nối, mỗi lần ghi có hạn 10 giây. Máy không đọc (hoặc mạng treo) thì app cắt kết nối, để một máy hỏng không giữ tài nguyên của node mãi.
7. **Một lỗi lạ do máy gây ra** chỉ làm đóng kết nối đó, không làm sập node đang giữ hàng nghìn kết nối khác.

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
| Tin | `SendMessage`, `GetHistory`, `GetMessages`, `EditMessage`, `DeleteMessage`, `HideMessage`, `ClearHistory`, `GetEditHistory` | Trong `SendMessage`: `kind`, `skip_unread`, `meta` (chỉ app nội bộ gửi tin hệ thống) |
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

Frame mã hoá protobuf và dùng lại đúng message request/response sẵn có của core. Không có kênh JSON (bỏ D17); khi cần debug thì dùng `corecli` để giải mã. Chưa bật nén (`permessage-deflate`): nén tốn CPU khi có 100K kết nối; bật hay không quyết ở phần 5 khi có số đo.

## 6. Config

| Biến | Mặc định | Vì sao |
|---|---|---|
| `WS_LISTEN`, `ADMIN_LISTEN` | `:8080`, `:9090` | cổng WebSocket; `/livez`, `/readyz`, `/metrics` |
| `REDIS_ADDR`, `REDIS_PASSWORD_FILE` | — | đọc bảng slot (chỉ đọc) |
| `WS_JWT_KEYS_FILE`, `WS_JWT_ISSUER`, `WS_JWT_AUDIENCE`, `WS_JWT_LEEWAY` | —, —, —, 30s | kiểm token |
| `WS_AUTH_TIMEOUT` | 5s | kết nối không gửi token thì đóng |
| `WS_UNAUTH_MAX` | 10.000 | số kết nối chưa đưa token mỗi node, chống mở kết nối rác |
| `WS_FRAME_MAX` | 128KB | lệnh lớn nhất: `AddMembers`/`CreateRoom` 1.000 người × id 64 byte ≈ 66KB |
| `WS_HEARTBEAT_TIMEOUT` | 15s | không có frame nào thì đóng (owner) |
| `WS_COMMAND_RATE`, `WS_COMMAND_BURST` | 50/s, 100 | chống lạm dụng mỗi kết nối (D112); tính cả `auth` và `heartbeat` |
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
