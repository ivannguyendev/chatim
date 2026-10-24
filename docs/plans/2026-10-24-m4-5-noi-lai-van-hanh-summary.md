# M4 phần 5: giữ kết nối, chống bão, vận hành — tóm tắt

> Ngày: 2026-10-24 · Nhánh `feat/m4` · Trạng thái: **owner duyệt 2026-10-24, đã xây** trên `feat/m4` (2026-10-25, quyết định D202–D206; kết quả ở mục cuối)
> **Các phần của M4:** (1) kết nối, token, lệnh ✅ · (2) nhận và đẩy event ✅ · (3) đã đọc, đã xem ✅ · (HTTPS) app `gateway` ✅ · (4) đang gõ, online ✅ · **(5) giữ kết nối, chống bão, vận hành** ✅.

## 1. Phần này làm gì

Repo chatim **chỉ là server** (owner 2026-10-24). SDK ReactJS ở repo khác. Server đặc tả đủ sự kiện và cơ chế, và chặn client làm sai bằng giới hạn và lỗi theo từng tin.

Một ngày của Lan:
- **09:00** token của Lan hết hạn mà máy chưa kịp gia hạn. Server **không ngắt**: kết nối chuyển sang "chờ đăng nhập", Lan tạm không nhận tin, server gửi `auth_required{EXPIRED}`. Máy gửi token mới trên chính kết nối đó, server nạp lại room, gửi `ready`, Lan nhận tin tiếp.
- **10:00** deploy `websocket-1`, 50.000 máy bị đóng 1001 và nối lại. Node kia chỉ cho 1.000 lần đăng nhập mỗi giây. Máy thừa được trả `retry_later{17 giây}` và **giữ kết nối**, 17 giây sau gửi `auth` lại.
- **11:00** máy cũ của Lan gửi một frame hỏng. Server trả `frame_error{INVALID}` cho đúng frame đó, kết nối vẫn chạy.
- **12:00** token của Lan bị lộ, kẻ xấu mở kết nối thứ 11 vào cùng node. Kết nối đó bị đóng 4009.
- Có gì hỏng (NATS, Redis presence, tin tới chậm, đăng nhập bị từ chối nhiều), Prometheus báo theo metric.

## 2. Trong và ngoài

| Trong phần 5 | Ai làm |
|---|---|
| Trạng thái "chờ đăng nhập" 3 phút; `auth_required`, `frame_error`, `retry_later` | websocket |
| Giới hạn tốc độ đăng nhập mỗi node; trần 10 kết nối mỗi người mỗi node | websocket |
| Đặc tả cho client: mã đóng, frame mới, cách nối lại, gia hạn token, gửi lại lệnh chưa có trả lời, `resync`/`ready` | docs (module 09) |
| Bỏ SDK `pkg/wsclient`, thay bằng client test nhỏ `internal/wstest` (gốc module) | tools |
| Metric và luật alert (Prometheus đọc `/metrics`), ghi chú NTP, thứ tự deploy | websocket, `deploy/prometheus/alerts.yml`, docs |
| e2e phase 12 (script tắt node bằng `docker stop`); bài tải `wsload` 10K trên dev | `tools/corecli`, `scripts/e2e.sh`, `tools/poc/wsload` |

| Ngoài | Ở đâu |
|---|---|
| SDK ReactJS: tự nối lại, gia hạn token, gửi lại lệnh | repo client, theo đặc tả ở mục 7 |
| Quyền NATS, mTLS | M5 |
| 100K kết nối | PoC prod-like |
| Thu hồi token | `identity` Phase 2 |

## 3. Hai trạng thái của kết nối

```mermaid
stateDiagram-v2
  [*] --> Cho: mở kết nối
  Cho: Chờ đăng nhập (tối đa 3 phút)
  Chay: Đang chạy
  Cho --> Chay: auth hợp lệ, còn suất đăng nhập, chưa quá 10 kết nối
  Cho --> Cho: auth sai → auth_required{INVALID}; hết suất hoặc auth dồn quá 1 lần/giây → retry_later{5–30 giây}
  Chay --> Chay: gia hạn đúng người → chạy tiếp; token sai hoặc của người khác → auth_required{INVALID}, vẫn chạy bằng token cũ
  Chay --> Cho: token hết hạn → auth_required{EXPIRED}
  Cho --> [*]: quá 3 phút chưa đăng nhập → đóng 4001
  Cho --> [*]: kết nối thứ 11 của người đó → đóng 4009
```

**Khi chờ đăng nhập:**
- Không nhận event, typing hay online, và không được đóng dấu online. Node gỡ người đó khỏi bảng room của kết nối này.
- Lệnh trả `UNAUTHENTICATED`. Frame khác `auth`/heartbeat nhận `auth_required`, tối đa 1 lần mỗi giây.
- Heartbeat vẫn bắt buộc: 15 giây im lặng thì đóng như hôm nay.
- Bỏ mốc "phải `auth` trong 5 giây" (4002): kết nối mới có heartbeat được chờ tới 3 phút.

**Gia hạn bằng token của người khác hoặc token sai khi đang chạy:** trả `auth_required{INVALID}`, kết nối vẫn chạy bằng token hiện tại. Một kết nối không đổi người; muốn đổi người thì mở kết nối mới.

**Lệnh đang xếp hàng lúc token hết hạn:** chạy bằng danh tính lúc server nhận lệnh.

**Từ chờ sang chạy:** server nạp room như lúc đăng nhập lần đầu, rồi gửi `ready`.

## 4. Lúc nào còn đóng kết nối

| Đóng vì | Mã |
|---|---|
| Quá 3 phút chưa đăng nhập | 4001 |
| Kết nối thứ 11 của một người trên node (owner: 10 mỗi người mỗi node, đếm trong RAM của node) | 4009 |
| 15 giây không có frame nào (kết nối chết) | 1000 |
| Node tắt (deploy) | 1001 |
| Frame lớn hơn 128KB (gws từ chối ngay từ phần đầu frame, không đọc tiếp được) | 1009 |
| Lỗi giao thức WebSocket ở tầng gws (frame sai chuẩn) | 1002 |
| Node đang đóng bảng room (lúc tắt) | 1013 |
| Lỗi lạ phía server khi xử lý kết nối đó; ghi socket lỗi hoặc quá hạn | 1011 / 1006 |

**Không còn đóng nữa:**

| Trường hợp | Trả về |
|---|---|
| Token sai, token hết hạn | `auth_required` |
| Frame chữ, frame không giải mã được, gửi dồn dập | `frame_error{UNSUPPORTED/INVALID/RATE}`, bỏ tin đó |
| Hết suất đăng nhập | `retry_later` |
| Máy nhận chậm, hàng gửi đầy (trước là 4008) | server ngừng đẩy; khi hàng xuống dưới một nửa thì gửi `resync{CLIENT_BACKLOG}` rồi đẩy tiếp. Cơ chế này có từ phần 2 cho lúc đuổi theo stream, nay dùng cho mọi lúc. Frame trạng thái (`ready`, `resync`, `room_closed`, `load_failed`) không vào được hàng thì cũng theo đường này, không bị mất im lặng |

## 5. Giới hạn tốc độ đăng nhập

- Mỗi node một bộ đếm: 1.000 lần đăng nhập mỗi giây, cho dồn tối đa 2.000. Chỉ tính lần đăng nhập có **token hợp lệ**, kiểm trước bước tốn nhất: nạp room từ core. Token rác không chiếm được suất của người thật.
- Mỗi kết nối đang chờ được gửi `auth` tối đa 1 lần mỗi giây; gửi dồn hơn thì nhận `retry_later` mà server không kiểm token (chặn kẻ xấu đốt CPU bằng token rác).
- Hết suất: trả `retry_later{ms}`, `ms` ngẫu nhiên đều trong 5–30 giây, và giữ kết nối ở trạng thái chờ. Không đóng nên không có frame nào bị mất.
- Kết nối đang chờ vẫn tính vào trần `WS_UNAUTH_MAX` (10.000 mỗi node, khoảng 10KB mỗi kết nối). Quá trần thì node trả HTTP 503 khi bắt tay, như hôm nay.

## 6. Trần kết nối mỗi người

- 10 kết nối mỗi người **mỗi node**, node tự đếm trong RAM, không Redis (owner chọn). Lý do không đếm toàn cụm: số đếm chung kẹt khi node chết; Lan chỉ có 3 máy mà vẫn bị chặn hẳn.
- Kết nối thứ 11 bị đóng 4009 ngay sau `auth` hợp lệ, không gọi core. Kết nối cũ không bị đá.
- Kết nối chết sau khi rớt mạng vẫn bị tính cho tới 15 giây heartbeat.

## 7. Đặc tả cho client (repo client làm, server không quản)

| Việc | Client phải làm |
|---|---|
| Nối lại | 1001 → chờ ngẫu nhiên 0–60 giây; mất mạng/1000 → 0–3 giây; 4009 → 30–60 giây; bắt tay lỗi/503 → chờ tăng dần 1, 2, 4… tối đa 60 giây |
| Gia hạn token | Ở mốc ngẫu nhiên 70–85% của `exp − iat` tính từ lúc nhận token, không theo đồng hồ máy; gửi `auth` trên kết nối đang mở |
| Lệnh chưa có trả lời lúc rớt kết nối | Không gửi lại (owner 2026-10-24); kết quả coi như chưa rõ. Nếu app muốn gửi lại thì dùng cùng `cid`/`request_id` (core chống trùng trong 15 phút) |
| Sau **mọi** `ready` (kết nối mới, hoặc sau `auth_required` trên cùng kết nối) | Gửi lại `open_room` các room đang xem và danh sách hỏi online, rồi tải lại dữ liệu qua HTTPS. `resync` tới trước `ready` thì coi như `ready` |
| `resync` trên kết nối đang mở | Chỉ tải lại dữ liệu; server vẫn nhớ room đang mở và danh sách hỏi online |
| `auth_required` / `retry_later` / `frame_error` | Gửi token mới / chờ đúng số giây rồi `auth` lại / sửa tin đó |

Client làm sai thì server chặn: hết suất đăng nhập, quá 10 kết nối, `frame_error`.

## 8. Client test thay cho SDK

- `pkg/wsclient` (SDK Go) bị bỏ, vì repo này không làm SDK.
- Thay bằng `internal/wstest` (gốc module, để cả test của websocket và công cụ e2e dùng được): chỉ nối, `auth`, gửi lệnh và chờ `response`, gửi `open_room`/`typing`/`watch_presence`, nhận frame. Dùng cho e2e, diễn tập và bài tải.
- Các test và e2e đang dùng `pkg/wsclient` (3 file test websocket, 5 file `corecli`) chuyển sang client test.

## 9. Metric và alert

Prometheus đọc `/metrics` của mỗi node; luật nằm trong `alerts.yml`, không đi qua NATS.

| Alert | Khi nào |
|---|---|
| `ChatimWebsocketNATSDown` | `nats_connected` = 0 quá 1 phút |
| `ChatimWebsocketBehind` | sau đuôi stream quá 30 giây trong 2 phút |
| `ChatimWebsocketJumpSuppressed` | có lần nhảy bị chặn trong 10 phút |
| `ChatimWebsocketReloadFailing` | nạp room lỗi liên tục 5 phút |
| `ChatimWebsocketDeliverySlow` | p99 độ trễ giao tin (mọi tin, từ lúc NATS lưu tới lúc vào hàng gửi) > 100ms trong 5 phút |
| `ChatimPresenceRedisDown` | Redis presence lỗi quá 1 phút |
| `ChatimWebsocketAuthRateLimited` | hơn 10% lần đăng nhập bị trả `retry_later` trong 10 phút |
| `ChatimWebsocketTokenExpiredSurge` | số kết nối rơi vào chờ đăng nhập vì hết hạn tăng vọt (client không gia hạn) |
| `ChatimGatewayCoreUnavailable` | hơn 5% lệnh qua gateway trả "không có core" trong 5 phút |

Metric mới:
- `event_delivery_seconds`
- `auth_total{result=ok|invalid|expired|rate|user_limit}`
- `frame_errors_total{reason}`

Docs vận hành thêm hai mục: NTP bắt buộc (token cho lệch 30 giây), và deploy từng node, chờ healthy rồi mới tới node kế.

## 10. Kiểm thử

| Test | Chứng minh |
|---|---|
| Unit websocket | Token hết hạn → `auth_required`, không nhận tin, gia hạn → `ready` và nhận tin tiếp; quá 3 phút → 4001; frame hỏng → `frame_error`, kết nối còn; hết suất → `retry_later` 5–30 giây, kết nối còn; kết nối 11 → 4009; máy chậm → `resync` thay 4008 |
| e2e phase 12 (script `docker stop`/`start`, owner duyệt) | Client test chạy nền, in "đang ở node N"; `scripts/e2e.sh` đọc dòng đó, `docker stop` đúng node N, chờ client in "đã nối lại ở node M", rồi `docker start` node N (test lỗi thì script vẫn bật lại node). Kiểm: sau khi nối lại room vẫn nhận tin, online đúng; token hết hạn giữa chừng → gia hạn trên cùng kết nối, nhận tin tiếp; kết nối 11 → 4009 |
| `make alerts-check` | Luật alert hợp lệ |
| `wsload` trên dev | 10K kết nối ổn định (độ trễ, RAM, CPU); tắt một node có 5K máy; 10K đăng nhập cùng lúc (số `retry_later`, thời gian tới khi tất cả vào) |

## 11. Quyết định

| Chọn | Bỏ, vì sao |
|---|---|
| Giữ kết nối, chờ đăng nhập 3 phút (owner) | Đóng 4001/4002: máy phải nối lại, token hỏng thành vòng nối liên tục |
| `frame_error` theo tin, chỉ đóng khi frame > 128KB hoặc lỗi lạ phía server (owner) | Đóng 1003/1008: một tin sai làm mất cả kết nối |
| Giới hạn tốc độ **đăng nhập**, trả `retry_later` và giữ kết nối (owner) | Giới hạn lúc bắt tay rồi đóng: trình duyệt không đọc được `Retry-After`, frame bị mất khi đóng |
| 10 kết nối mỗi người mỗi node (owner) | Đếm toàn cụm qua Redis: kẹt khi node chết, hoặc tốn ~15.000 lần ghi/giây |
| Repo chỉ server, client test nhỏ; client làm theo đặc tả (owner) | Giữ SDK Go: trùng việc với repo client |
| Một kết nối không đổi người (token người khác → `auth_required{INVALID}`) | Đổi người trên cùng kết nối: lệnh đang xếp hàng của người cũ có thể chạy nhầm danh tính người mới |
| Alert theo metric (owner giữ) | — |

## 12. Rủi ro

- Kết nối chờ đăng nhập chiếm chỗ trong `WS_UNAUTH_MAX` tối đa 3 phút; bão lớn hơn trần thì máy thừa nhận 503 rồi chờ tăng dần. Kẻ xấu giữ đủ 10.000 chỗ chờ chỉ cần ~55 lần bắt tay mỗi giây cộng heartbeat (trước đây ~2.000/giây); RAM vẫn có trần.
- Token lộ vẫn mở được 10 × số node kết nối; chặn hẳn cần thu hồi token ở Phase 2.
- Bỏ SDK Go thì repo client phải tự kiểm logic nối lại và gửi lại lệnh của họ.

## Kết quả thực thi

Xây trên `feat/m4` từ 2026-10-24 tới 2026-10-25, mỗi task một commit (task rủi ro có thêm commit sửa sau review).

| Task | Commit | Nội dung |
|---|---|---|
| 1. Client test thay SDK | `6bdc5dd` | `internal/wstest` thay `pkg/wsclient` (xoá); e2e tự kiểm trùng/lỗ (`EventCheck`), theo dõi đang gõ (`TypingView`), `load_rooms` sau `load_failed`; `MarkRead` chờ trả lời trước `ListMyRooms` |
| 2. Proto | `2fec916` | `retry_later` (28), `auth_required` (29), `frame_error` (30) |
| 3. Chờ đăng nhập, giới hạn, trần | `2ca56c6`, sửa sau review `0e1538a` | Trạng thái chờ 3 phút, `auth_required`, giới hạn đăng nhập mỗi node sau `Verify`, 1 `auth`/giây mỗi kết nối, 10 kết nối mỗi người mỗi node → 4009, danh tính ghim theo kết nối, lệnh chạy bằng claims lúc nhận |
| 4. `frame_error`, máy chậm | `44e2947`, sửa sau review `4c7e422` | `frame_error` thay 1003/1008; máy chậm → `resync{CLIENT_BACKLOG}` thay 4008; frame trạng thái gửi lại (`owedState`); giới hạn tốc độ trước khi giải mã |
| 5. Metric và alert | `afdf0b1`, `7f810d3`, `fe55adc` | Histogram `event_delivery_seconds`; 9 luật alert websocket/gateway (`ChatimWebsocketNATSDown` critical, còn lại warning) |
| 6. e2e phase 12 | `ede733c`, `2810525` | `corecli e2e ws-resilience` và `ws-node-stop`; node dừng phải đóng đúng 1001 |
| 7. `wsload` | `f79a84d`, `3721b0d` | `tools/poc/wsload` (steady, kill, burst), `scripts/wsload-kill.sh`; kết quả ở `bin/bench/wsload-<kịch bản>-<giờ>/` |
| 8. Docs | commit này | D202–D206, module 09 (§7 viết lại, §6.2 vận hành, §11 đặc tả client), README, CLAUDE.md, roadmap |

**Review** (một reviewer mỗi task có nhãn, một lượt):
- Task 3: 0 Critical, 2 Important, đã sửa ở `0e1538a`: gỡ đăng ký khỏi bảng quan tâm ngay trong khoá khi về trạng thái chờ (trước đó có khe event lọt tới kết nối đang chờ); test đua thật giữa hết hạn và `join`. Owner chốt thêm: một kết nối không đổi người, danh tính ghim từ lần đăng nhập đầu. Chấp nhận, không sửa: trả lời của lệnh nhận trước lúc hết hạn vẫn tới máy; `auth_required_total` chỉ có nhãn `expired`; cửa sổ micro-giây ở trả lời presence; thêm `pkg/backoff.Between`.
- Task 4: 0 Critical, 1 Important, đã sửa ở `4c7e422`: `ready`/`load_failed` không vào được hàng đầy làm người chưa sẵn sàng kẹt im lặng → `owedState` gửi lại đúng frame đó thay vì `resync`; thêm `WS_SEND_QUEUE ≥ 2`; giới hạn tốc độ chạy trước khi giải mã (frame nhị phân vượt mức vẫn phải giải mã để trả lỗi rate cho lệnh). Sửa thêm 2 test chập chờn (`synctest.Wait` sau `ready`).

**Khác với bản duyệt (đã ghi vào docs):**
- `auth` dồn quá 1 lần/giây trên kết nối đang chờ nhận `retry_later{1000}` (1 giây), không phải 5–30 giây; đếm riêng `auth_total{result="throttled"}`. 5–30 giây chỉ dành cho lúc node hết suất (`result="rate"`).
- Gia hạn khi đang chạy bằng token sai, **đã hết hạn** hay của người khác đều nhận `auth_required{INVALID}` (không phải `EXPIRED`); kết nối chạy tiếp bằng token đang có. Gia hạn thành công không có frame trả lời.
- Giới hạn đã biết: frame nợ của máy chậm chỉ được gửi khi kết nối có lần giao kế tiếp hoặc node nhận một event live bất kỳ, không có hẹn giờ.
- e2e không còn lọc event cũ theo version như `Applier` của SDK cũ: bản phát bù muộn hiện trong feed của e2e, chưa từng bị tính là lỗi.

**Kiểm thử đã chạy:**
- Mỗi task: unit của gói đụng tới (`-race`), `make fmt-check`, `make vet`, `make lint` trước commit; luật alert qua `make alerts-check`.
- `make itest` xanh; `make e2e` phase 1–12 xanh. Phase 12: `auth_required{EXPIRED}` tới 49,4 giây sau khi ký token 20 giây (20 giây + 30 giây lệch giờ); kết nối thứ 11 bị đóng 4009; `docker stop` websocket-1 → kết nối nhận 1001 sau 2,8 giây, nối lại sang websocket-2, mở lại nhóm sau `ready` và nhận tin kế tiếp; log hai node không có token nào của phase 12.
- Số đo wsload trên dev (2026-10-25, 10.000 user, 2 node; chi tiết `docs/poc/README.md` R4b):
  - chạy ổn định 1.000 tin/giây: giao đủ 100%, không trùng, trễ p50 16,1ms, p99 100,4ms (sát ngưỡng 100ms, máy dev chạy chung CPU); ack p99 98,9ms;
  - 10.000 đăng nhập cùng lúc: tất cả vào được, 2.949 lần `retry_later`, máy cuối `ready` sau 32,1 giây;
  - `kill -9` một node có 5.000 máy: cả 5.000 nối lại trong 3,05 giây; node còn lại giữ 10.000 kết nối ở ~516MB.

**Ghi chú môi trường dev:** các tiến trình `agy_acp_server` mồ côi (agent Antigravity của Zed) chiếm hết IO của Docker, làm itest của core lỗi; tắt chúng đi thì itest và e2e xanh lại.
