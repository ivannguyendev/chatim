# M7.1 phase 2: tệp của room — tóm tắt

> Ngày: 2026-11-11 → 2026-11-12 · Nhánh: `feat/m7-1` · Trạng thái: `dev-done` (owner duyệt summary 2026-11-11, viết lại theo bản đã xây 2026-11-12)
> Nguồn yêu cầu: web đang xây web app + SDK (owner chuyển 2026-11-08); phase owner duyệt 2026-11-10 (`docs/roadmap.md`, dòng M7.1: "`ListRoomFiles` (ảnh/video · file, phân trang) trong module media, kiểm quyền trước khi đọc").

## Scope và ranh giới

| Việc | Trong phase 2? | Thuộc về |
|---|---|---|
| Lệnh `MediaService.ListRoomFiles`: tệp đã gửi trong một room, tab ảnh/video hoặc tab file, mới nhất trước, phân trang, kèm URL tải và thumb | Có | module `media` (dữ liệu `media_message_files`) |
| Cặp tin–file thêm `room_id`, `kind`, `sent_at` và một index để đọc theo room | Có | module `media` (consumer `media` ghi) |
| Kiểm quyền đọc room và đọc tin theo người xem (ẩn, xoá, xoá lịch sử) | Có | chat, qua cổng `ChatReader` sẵn có của media |
| Route gateway cho lệnh mới | Có | `pkg/chatrpc` (bảng lệnh), giới hạn theo user chung của app |
| Docs và e2e 16 | Có | `docs/features/12`, `architecture.md`, `docs/apps/{chat,gateway}.md`, `docs/e2e.md`, `README.md`, `INDEXES.csv` (module 11 không liệt kê lệnh media, không đổi) |
| Cách hiện tab, lưới ảnh, tải hay mở file | Không | client (memory `server-leaves-display-to-clients`) |
| Tổng số tệp của room | Không | chưa có yêu cầu (A6) |
| `avatar_users`, ảnh nhóm | Không | phase 3, 4 |

## Tính năng → lớp dữ liệu (architecture §4)

| Tính năng | Lớp | Ghi | Phục hồi |
|---|---|---|---|
| `room_id`, `kind`, `sent_at` trên cặp tin–file | Tập theo event của module (khuôn `mentions`, D115), dòng "Phiên upload" của §4 | Consumer `media` upsert cặp như nay, thêm `$set` ba field (giá trị không đổi theo thời gian) | JetStream giao lại; `/app resync` phát lại `msg_created` → `$set` ghi lại cùng giá trị |
| `ListRoomFiles` | Chỉ đọc, theo người xem | — | — |

## 1. Thuật ngữ

| Từ | Nghĩa |
|---|---|
| Cặp tin–file | Doc của `media_message_files`: tin nào dùng file nào (M7 P3); `_id` = `{khoá tin}│{khoá file}`, khoá tin = `room-thread-seq` |
| Tab | `MEDIA` = `image/*` và `video/*`; `FILE` = mọi loại khác (pdf, html, audio…) |
| `cleared_at` | Lúc người xem bấm "xoá lịch sử" của room; tin gửi trước đó không hiện với họ |
| Cổng chat | `mediaapp.ChatReader`: media hỏi chat qua interface, không import chat (D182) |

## 2. Bức tranh chung

Ví dụ: nhóm "Kho HCM" (room `9036108543124204147`). An gửi tin seq 41 kèm `ảnh-kệ.png` và `bao-gia.pdf`. Consumer `media` nhận `msg_created`, ghi hai cặp như nay, nay thêm room, tab và giờ gửi:

```
_id: "9036108543124204147-0-41|acme/an/20261111/0cq5…h8"  tenant_id: "acme"  state: 1
room_id: "9036108543124204147"  kind: "media"  sent_at: 09:01:00.512
_id: "9036108543124204147-0-41|acme/an/20261111/0cq6…k2"  … kind: "file"  sent_at: 09:01:00.512
```

Bình mở tab "Ảnh/Video": web gọi `ListRoomFiles{room_id, kind: MEDIA, page_size: 30}` qua gateway → chat kiểm Bình còn trong nhóm → media đọc 30 cặp mới nhất của tab qua index → chat lọc tin Bình không được thấy → media ký URL → trả 30 ảnh và `next_before`.

```
web ──HTTPS──▶ gateway ──gRPC──▶ chat node ──▶ module media
                                   ▲               │ 1. cổng chat: còn trong room? cleared_at?
                                   └───────────────┤ 2. media_message_files (index theo room)
                                                   │ 3. cổng chat: tin nào người xem thấy
                                                   │ 4. media_uploads (bucket) → ký URL
```

## 3. Dữ liệu

| Field | Nghĩa | Ví dụ | Dùng để |
|---|---|---|---|
| `media_message_files.room_id` | Room của tin (chuỗi như `MessageRef.room_id`) | `"9036108543124204147"` | Lọc theo room |
| `media_message_files.kind` | Tab, tính từ mime của file trong tin (cùng nguồn chọn thumb và hiện cho client) | `"media"`, `"file"` | Lọc theo tab |
| `media_message_files.sent_at` | `created_at` của tin (mili giây), chat trả trong `MessageState` | `09:01:00.512` | Xếp mới nhất trước, lọc `cleared_at` |
| Index `{tenant_id: 1, room_id: 1, kind: 1, state: 1, sent_at: -1, _id: -1}` | Đọc một trang chỉ từ index, không đọc doc | — | Không quét collection, không sắp xếp trong RAM |
| `ListRoomFilesRequest` | `room_id`, `kind` (`ROOM_FILE_KIND_MEDIA`/`_FILE`; `UNSPECIFIED` → `invalid_argument`), `before` (con trỏ mờ), `page_size` 1–100 như `ListUploads` | `{room, MEDIA, "", 30}` | — |
| `RoomFile` | `message` (`MessageRef`: room, thread, seq), `sender_id`, `sent_at`, `file` (proto `Attachment` sẵn có: khoá, tên, mime, cỡ, `dim`, `dur`, `hash`), `url`, `thumbnail_url` | ảnh 1920×1080 của An lúc 09:01 | Hiện lưới ảnh, danh sách file, nhảy tới tin |
| `ListRoomFilesResponse` | `files`, `next_before` (rỗng = hết), `expires_at` của URL | — | Trang sau, làm mới URL |

Tên, mime, cỡ và `kind` đều lấy từ đính kèm của tin (như `GetAttachmentURLs`). Đính kèm không đổi sau khi gửi (sửa tin chỉ đổi text).

## 4. Luồng `ListRoomFiles`

1. Gateway kiểm token, chuyển tới node chat bất kỳ (`Router.Any`). Media chưa sẵn sàng → `UNAVAILABLE`.
2. Kiểm request: `page_size` ngoài 1–100, `kind` lạ, `before` giải mã lỗi → `INVALID_ARGUMENT`.
3. Cổng chat `RoomReadAccess(tenant, user, room)` (mới): `message.Reader.ReadAccess`, cùng checker của `GetHistory` (`ReadHistory`); interface riêng `mediaReader` cho media, interface `visibleReader` của Mention không đổi. Không phải member → `PERMISSION_DENIED`; room không có hoặc của tenant khác → `NOT_FOUND`; trả `cleared_at` và room id dạng chuẩn (`"007"` → `"7"`). **Chưa đọc cặp nào trước bước này.** Con trỏ cũ hơn `cleared_at` (người xem xoá lịch sử sau khi lấy con trỏ) → trang rỗng, hết.
4. Store `ListRoom`: `{tenant_id (token), room_id, kind, state: 1, sent_at > cleared_at}`, con trỏ `(sent_at, _id)` bằng `$or` hai nhánh, mỗi nhánh giữ đủ phép bằng (D139), xếp `sent_at` rồi `_id` giảm, `limit page_size`; chỉ đọc index, không đọc doc.
5. Cổng chat `VisibleAttachments` (sẵn có, nay trả thêm người gửi, giờ gửi, cỡ, `dim`, `dur`, `hash`): đọc tin theo khoá (`mongobound`, D139), bỏ tin đã xoá, đã ẩn hoặc trước `cleared_at` của người xem.
6. `download.Buckets` (tách từ `GetAttachmentURLs`, nhận khoá file, bỏ trùng) đọc lô `media_uploads` lấy bucket của các tệp giữ lại, ký URL file và thumb bằng `download.Signer`. Cặp mang khoá tin hỏng bị bỏ qua (không thể có: consumer ghi khoá từ `MessageRef.Key`), không đếm.
7. Trả theo thứ tự cặp; `next_before` = cặp cuối của lần đọc DB khi đủ `page_size` dòng, nên trang có thể ngắn hơn `page_size` mà vẫn có trang sau.

Mã lỗi gRPC/Connect: `INVALID_ARGUMENT`, `PERMISSION_DENIED`, `NOT_FOUND`, `UNAVAILABLE` (media chưa sẵn sàng, Mongo/S3 lỗi); gateway giới hạn theo user chung của app → `RESOURCE_EXHAUSTED`.

## 5. Đúng đắn và chạy đua

- **Quyền trước dữ liệu:** bước 3 chạy trước bước 4, lọc tenant lấy từ token, room là phép bằng; con trỏ của room khác chỉ là một mốc vị trí, không mở dữ liệu room khác.
- **Tin xoá giữa bước 4 và 5:** bước 5 đọc tin lúc gọi nên bỏ; cặp được nhả (`state = 2`) khi consumer xử lý `msg_deleted`.
- **Xoá lịch sử:** lọc trong truy vấn (bước 4) nên không có chuỗi trang rỗng dài; bước 5 kiểm lại.
- **Bị kick giữa chừng:** lần gọi kế tiếp `PERMISSION_DENIED`.
- **Hai file cùng tin:** cùng `sent_at`, phân thứ tự bằng `_id`, không lặp không sót qua trang.
- **Forward:** tin forward có cặp riêng trong room đích, `sent_at` của tin forward.
- **Giao lại event:** `$set` cùng giá trị, idempotent; không bit `_state`, không event mới.
- Không có chỗ xếp hàng mới: đọc thuần, consumer giữ nguyên cách chạy.

## 6. Event

Không event mới, không đổi subject. Consumer `media` vẫn nghe `msg_created` (`media`, `mention`) và `msg_deleted` (`docs/events.md`). Tệp mới hiện trong danh sách sau khi consumer xử lý (vài giây, `chatim_media_lag_seconds`).

## 7. Thư viện và hạ tầng

Không thêm thư viện. Index mới khai trong danh mục index của media, tạo qua `/app indexes` (D191). `make proto` sinh code; `buf-lint`.

## 8. Quyết định và phương án bị loại

| Quyết định | Lý do | Phương án bị loại |
|---|---|---|
| `ListRoomFiles` ở module media trên cặp tin–file (không thành dòng decision log: áp dụng D267, D262; owner 2026-11-12) | Dữ liệu tin nào dùng file nào đã có từ M7 P3; một trang = một truy vấn index | Hỏi `messages` theo file: cần index phụ trên `messages` (D148 cấm). Client lật `GetHistory` lọc tin có file: N+1, nhóm 10K tin phải đọc hết |
| Hai tab `MEDIA`/`FILE`, lưu `kind` hai giá trị | Đúng hai tab web cần; một phép bằng trên index | Dùng lại `FileKind` ba loại của `ListUploads`: `$in` trên index thành 3–6 lần quét gộp, chưa ai cần lọc riêng video |
| Kiểm quyền trước, lọc `cleared_at` trong truy vấn | Review độc lập 2026-11-08: đọc cặp trước kiểm quyền lộ khoá tin qua con trỏ; lọc sau đọc làm trang rỗng không trần | Lọc sau đọc; kiểm quyền từng tin |
| Ba field ghi bằng `$set` (cặp cũ vẫn `$setOnInsert`) | Giao lại hoặc `/app resync` tự điền cặp cũ, không cần script | `$setOnInsert`: cặp tạo trước P2 không bao giờ có field |

## 9. Chi phí và tải

Một lần gọi (trang 100; mọi tin nằm ở dòng tin chính vì trả lời là tin trích dẫn trên dòng chính, D114): 1 kiểm member, 1 truy vấn chỉ đọc index ≤ 100 mục, 4 lượt đọc của chat cho tin (room, member, tin theo khoá, tin ẩn), 1 `GetMany` upload, tới 200 chữ ký URL (tính trong RAM, không gọi S3). Ngang `GetAttachmentURLs` 100 tin cộng một truy vấn index. Ghi: mỗi file đã gửi thêm một mục index (~150 byte), ghi một lần lúc consumer upsert.

## 10. Rủi ro và giới hạn

- Tệp mới hiện trễ vài giây (consumer `media`).
- Trang có thể ít hơn `page_size`, kể cả rỗng, mà vẫn có `next_before`. Ví dụ: Bình ẩn 30 tin ảnh mới nhất, gọi `page_size: 30` → media đọc đúng 30 cặp đó, chat bỏ cả 30 vì Bình đã ẩn → `files: []`, `next_before` trỏ tới cặp thứ 30; lần gọi sau ra 30 ảnh cũ hơn. Tin ẩn nằm ở collection `hidden` của chat, theo từng người, media không lọc trong truy vấn được (D182); `cleared_at` chỉ là một mốc giờ nên lọc được. Web: `next_before` rỗng mới là hết, còn thì gọi tiếp khi màn hình chưa đủ (như `ListMentions`). Server không tự đọc thêm cho đủ trang; chỉ thêm khi web báo gặp thật.
- Cặp tạo trước P2 (chỉ dev, chưa có production) không hiện tới khi chạy `/app resync` cho khoảng đó (resync chỉ phát lại dòng tin chính).
- Bản mới phải chạy `/app indexes` trước khi nhận lệnh (D191); thiếu index thì `ListRoomFiles` lỗi, như `ListUploads` hiện nay.
- Nếu sau này shard theo `_id`, truy vấn theo room của `media_message_files` hỏi mọi shard, như `mentions` (ghi vào luật shard của architecture §5).

## 11. Điểm đội tự chọn (owner phản biện)

- Trả `sender_id` và `sent_at` mỗi tệp: tab file thường hiện "ai gửi, lúc nào"; lấy từ tin đã đọc ở bước 5, không tốn thêm truy vấn.
- `page_size` bắt buộc 1–100, không có mặc định, như `ListUploads`.
- Use case ở package mới `modules/media/internal/roomfiles`, dùng lại `download.Signer` và `download.Buckets`.
- Test `pkg/chatrpc` `TestHTTPSOnlyCallsAreRequestCommands` bỏ điều kiện "lệnh chỉ qua HTTPS không có `room_id`": chỉ websocket đọc `room_id` của lệnh để chọn node theo slot, gateway gọi node bất kỳ; test nay kiểm lệnh nằm trong tập request và không trong tập realtime (reviewer: an toàn).

## 12. Kiểm thử (như đã chạy)

| Test | Chứng minh |
|---|---|
| Storetest mem + mongo `ListRoom`: đúng tenant, room, tab, `state 1`, mới nhất trước, hai file cùng `sent_at` qua trang không lặp không sót, `cleared_at` lọc, tenant khác cùng room id không thấy gì, con trỏ của room khác chỉ trả room đang hỏi, giao lại cặp cũ điền đủ field | Hợp đồng store, IDOR ở tầng store |
| Itest mongostore `TestListRoomUsesTheRoomIndexWithoutReadingDocuments` (50 tin × 3 cặp): trang đầu `LIMIT → PROJECTION_COVERED → IXSCAN`, trang có con trỏ + `cleared_at` `LIMIT → PROJECTION_DEFAULT → SORT_MERGE → 2 IXSCAN`; 0 doc đọc, ≤ 11 / ≤ 22 khoá | Đúng index, chi phí có trần |
| Unit `attach` `TestPairsCarryTheRoomTheTabFromTheMessageMimeAndTheSendTime`: png, mp4 → media; pdf, html → file; `room_id`, `sent_at` (cắt mili giây) đúng, giao lại cùng event cho cùng kết quả | Ghi đúng field |
| Unit `domain`: tab theo mime, con trỏ qua lại ở mili giây (khoá file có `|`), 6 dạng con trỏ hỏng → `invalid_argument`, khoá tin parse ngược | Con trỏ và khoá |
| Unit `roomfiles`: không phải member → `PERMISSION_DENIED` và store không bị gọi; store nhận tenant của caller; con trỏ cũ hơn `cleared_at` → trang rỗng; `before` hỏng, `page_size` 0/101, `kind` lạ → `INVALID_ARGUMENT`; tin ẩn, đã xoá bị bỏ; trang ngắn vẫn có `next_before`; tệp của tin forward có mặt; media chưa sẵn sàng → `UNAVAILABLE` | Luồng §4, quyền trước dữ liệu |
| Unit cổng chat (`app`): `RoomReadAccess` chưa bind → `UNAVAILABLE`, `cleared_at` của member, `"077"` → `"77"`, người ngoài `PERMISSION_DENIED`, tenant khác `NOT_FOUND`, room hỏng `INVALID_ARGUMENT`; `MessageFiles` trả giờ gửi và mime; `VisibleAttachments` trả người gửi, giờ gửi, cỡ, `dim`, `hash`. Unit `message`: `ReadAccess` | Cổng chat đúng |
| Unit `grpcsrv`: map tab, field ra proto (`Attachment`), người ngoài, tab lạ; `Refusing` cho lệnh mới | Transport |
| e2e 16 bước 18: B gọi `ListRoomFiles` qua gateway: tab `MEDIA` chỉ PNG, tab `FILE` chỉ HTML (seq, người gửi, giờ gửi, đính kèm, URL ở bucket completed, không `next_before`); C ngoài room → `permission_denied`. Bước 23: sau khi A xoá tin gốc, room gốc không còn tệp; trong 30s room A–C cho C đúng PNG ở seq của tin forward | Qua gateway thật, quyền thật, consumer thật |

Kết quả: `make fmt-check`, `vet`, `lint` (0 issues), `buf-lint` sạch; test các package đã chạm (`modules/media/...`, `pkg/chatrpc`, `apps/chat/internal/app`, `apps/chat/internal/message/...`, `apps/gateway/...`, `apps/websocket/...`, `tools/chatcli/...`) ok; `make itest` 116 package ok, 1 fail `TestSingleChatNodeClaimsAllSlots` (`platform/slot`, P2 không chạm; lần đó máy chạy song song reviewer, chạy lại riêng ok 1,9s, mang sang); `make app-up`, `/app indexes -apply` (tạo `tenant_id_1_room_id_1_kind_1_state_1_sent_at_-1__id_-1`), `E2E_ONLY=16 make e2e` PASS 23 bước. Reviewer code độc lập: không Critical, không Important.

## 13. Lệch so với plan và Minor mang sang

- Lệch: interface `mediaReader` riêng thay vì mở rộng `visibleReader` (Mention không đổi); `download.Buckets` nhận khoá file thay vì danh sách tin; cặp có khoá tin hỏng bỏ qua không đếm; test `chatrpc` như §11; e2e không kiểm tin ẩn (unit `roomfiles` và `app` đã kiểm).
- Minor review (không sửa): trang cuối vừa đủ `page_size` vẫn có `next_before`, lần gọi sau rỗng (cùng luật `ListUploads`); cặp cũ chỉ có field sau `/app resync`.
- Mang sang: test chập chờn `TestSingleChatNodeClaimsAllSlots` khi máy tải nặng và `TestRevokedSessionSendsTheRunningConnectionBackToWaiting` (từ RF5), sửa ở nhánh `fix/`; báo web `GetUsers` tối đa 100 id (từ P1).

## Bảng phủ

| Yêu cầu / quyết định | Thành phần | Task | Test | Bằng chứng |
|---|---|---|---|---|
| Tab ảnh/video của room (roadmap M7.1 P2, owner 2026-11-10) | proto `media_service`, `roomfiles`, `mongostore.ListRoom`, `attach` | T1, T2, T4 | Storetest, unit `attach`, unit `roomfiles`, e2e 16 | Storetest mem + mongo ok; unit `attach`, `roomfiles` ok; e2e 16 bước 18 PASS |
| Tab file của room (như trên) | như trên | T1, T2, T4 | như trên | như trên |
| Phân trang mới nhất trước (như trên) | `mongostore.ListRoom`, con trỏ `(sent_at, _id)` | T1, T4 | Storetest (cùng `sent_at` qua trang), unit `roomfiles` | Storetest "files of one message page by key", "send times below a millisecond" ok; unit trang ngắn ok |
| Kiểm quyền trước khi đọc (như trên; review 2026-11-08) | cổng chat `RoomReadAccess`, `roomfiles` | T3, T4 | Unit `roomfiles` (store không bị gọi), unit `app`, e2e 16 người ngoài | `TestAnOutsiderIsRefusedBeforeAnyPairIsRead` ok; unit `app` ok; e2e bước 18 `permission_denied` |
| Ẩn, xoá, xoá lịch sử theo người xem (review 2026-11-08) | truy vấn lọc `cleared_at`, `VisibleAttachments` | T1, T3, T4 | Storetest `cleared_at`, unit `roomfiles`, e2e 16 xoá tin | Storetest cleared ok; `TestACursorOlderThanTheClearMark…` ok; e2e bước 23 PASS |
| Lọc tenant từ token, người khác/tenant khác không thấy gì (CLAUDE.md, IDOR) | `mongostore.ListRoom`, `roomfiles`, `RoomReadAccess` | T1, T3, T4 | Storetest tenant khác, con trỏ room khác; unit `roomfiles` tenant caller; reviewer IDOR MongoDB 2026-11-11 (skill `mongo`, không Critical) | Storetest "another tenant", "a cursor taken in another room" ok trên mem và Mongo; reviewer plan và reviewer code không Critical |
| Tệp của tin forward hiện ở room đích (§5) | `roomfiles` | T4 | Unit `roomfiles` | `TestAForwardListsTheFileAtTheForwardTime` ok; e2e bước 23 PASS |
| Đúng index, chi phí có trần (CLAUDE.md, IDOR) | index mới, `page_size` ≤ 100 | T1 | Itest `explain` | Itest explain: `IXSCAN`, không `SORT`/`COLLSCAN`, 0 doc đọc |
| Lệnh tới được qua gateway (architecture §1, D198) | `pkg/chatrpc` bảng lệnh và tập lệnh gateway | T4 | e2e 16 | Unit `chatrpc` 30 lệnh request ok; e2e 16 PASS |
| Docs và `INDEXES.csv` | `docs/features/12`, `architecture.md` §1/§4/§5/§9, `docs/apps/{chat,gateway}.md`, `docs/e2e.md`, `README.md` | T5 | `make fmt-check`, đọc lại | `make fmt-check` sạch |
