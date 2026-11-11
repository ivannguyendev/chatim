# Module 12: Media — upload, đính kèm trong tin, URL tải, gallery, chuyển file (M7 P1–P3)

> Phạm vi: M7 phase P1 ([tóm tắt](../plans/2026-11-06-m7-p1-upload-summary.md)), P2 ([tóm tắt](../plans/2026-11-07-m7-p2-attachments-summary.md)) và P3 ([tóm tắt](../plans/2026-11-08-m7-p3-move-files-summary.md)). File mới upload nằm ở bucket `processing`; file đã gắn vào tin được chuyển sang bucket `completed` (§8). Chưa có chính sách xoá file không còn tin nào dùng. Module `modules/media` nhúng trong chat; gateway chuyển `MediaService` và `ChatService.GetClientConfig` tới node chat bất kỳ; tin kèm file gửi qua websocket như tin thường. Vận hành: [chat](../apps/chat.md#media-module-modulesmedia), [gateway](../apps/gateway.md).

## 1. Use case

| Mã | Use case | Ai | Mô tả |
|---|---|---|---|
| UC-MEDIA-00 | Hỏi config | Máy | `GetClientConfig` lúc mở app: `media.enabled` (bật và kho object đang trả lời), `max_file_bytes`, `max_parallel_uploads`, `media.thumbnails` (server có sinh thumb), giới hạn tin (`max_attachments` 10), giới hạn room (`room.max_pins`, `room.max_members_per_call`) |
| UC-MEDIA-01 | Upload file tới vài GB | Thành viên | Chia phần do server quyết, byte đi thẳng từ máy lên S3 bằng URL ký, server không đọc byte nào |
| UC-MEDIA-02 | Huỷ upload | Thành viên | `AbortUpload` xoá các phần đã lên |
| UC-MEDIA-03 | Gửi tin kèm file | Thành viên, backend tenant | `SendMessage` qua websocket với tối đa 10 đính kèm; text được rỗng khi có đính kèm; forward chép file của tin nguồn |
| UC-MEDIA-04 | Xem và tải file trong tin | Thành viên đọc được tin | `GetAttachmentURLs` một lần cho cả màn hình (≤ 100 tin), nhận URL tải và URL thumb |
| UC-MEDIA-05 | Gallery "file tôi đã upload" | Người upload | `ListUploads`: mọi file mình đã upload xong, mới nhất trước, lọc ảnh/video/khác, nhãn `expired` khi kho đã xoá |
| UC-MEDIA-06 | Thumb ảnh, video | Hạ tầng | Lambda production sinh `{key}{hậu tố}` từ file gốc, cạnh file ở bucket nào cũng vậy; server chỉ suy khoá và ký URL (§7) |
| UC-MEDIA-07 | Giữ file đã gửi | Hệ thống | File đã gắn vào tin được chuyển sang `completed` nên không bị luật 30 ngày của `processing` xoá; biết tin nào đang dùng file nào; file không còn tin nào dùng chỉ được đánh dấu (§8) |

## 2. Luồng upload (spec client)

1. Gọi `GetClientConfig`. `media.enabled = false` → ẩn nút đính kèm; file lớn hơn `max_file_bytes` → báo ngay, không gọi server.
2. `StartUpload{file_name, content_type, size, upload_bytes_per_second, dim, dur}`. `upload_bytes_per_second` là tốc độ một luồng đo ở lần upload trước (0 = chưa biết); server dùng nó để chọn cỡ phần khoảng 8 giây mỗi phần. `dim` = `[rộng, cao]` của ảnh/video (bỏ trống nếu không có), `dur` = thời lượng video/audio tính bằng mili giây. `content_type` được chuyển chữ thường (`Image/PNG` → `image/png`). Response: `key`, `part_size`, `parts` (mọi phần `{part_number, size}`), `urls` (tối đa `max_parallel_uploads` URL dùng ngay), `expires_at` của các URL đó.
3. Chạy `max_parallel_uploads` luồng (mặc định 4). Mỗi luồng PUT đúng `size` byte của phần lên URL (URL ghim `Content-Length`: sai một byte là 403), giữ header `ETag` của response. PUT lỗi thì làm lại đúng phần đó (xin URL mới nếu URL đã hết hạn). Trong lúc đọc file để PUT, máy tính luôn SHA-256 của cả file (không phải đọc file hai lần).
4. Xin URL theo vòng: một `GetPartURLs{key, part_numbers}` cho tối đa `max_parallel_uploads` phần kế, gọi khi bắt đầu vòng hiện tại để không phải chờ. Gateway giới hạn mỗi thiết bị (`sid`) 1 lần/5 giây, mỗi (user, IP) 1 lần/giây, mỗi user 2 lần/giây; vượt → `resource_exhausted`, chờ rồi gọi lại.
5. `CompleteUpload{key, parts: [{part_number, etag}], hash}` với đủ mọi phần; `hash` = `sha256:` + 64 ký tự hex thường (tuỳ chọn). Hết hạn hoặc mạng rớt giữa chừng → gọi lại đúng lệnh đó: server nhận ra S3 đã đóng phiên và trả thành công.
6. Dùng `key` khi gửi tin (§4), **trong 24 giờ** kể từ `StartUpload`.

Trình duyệt: bucket phải cho CORS `PUT` và `ExposeHeaders: ETag`, nếu không máy không đọc được ETag.

Khoá object: `{tenant}/{user}/{yyyymmdd}/{26 ký tự}`, ví dụ `acme/an/20261107/0cq5tnm8k0a1b2c3d4e5f6g7h8`. 26 ký tự là base32hex chữ thường (`0-9a-v`) của 6 byte mili giây lúc `StartUpload` + 10 byte ngẫu nhiên, nên khoá cùng người xếp theo thời gian và chat đọc được tuổi khoá. Tenant và user lấy từ token.

## 3. Chia phần

Server quyết, máy không chọn: phần 16–128 MiB (trừ phần cuối), tối đa 2.000 phần, số phần là bội của số luồng khi file đủ lớn để cả 4 luồng xong cùng lúc. Ví dụ: 40 MiB → 16 + 16 + 8; 100 MiB → 4 × 25; 4 GiB (chưa biết tốc độ) → 128 × 32 MiB; 4 GiB ở 20 MB/s mỗi luồng → 32 × 128 MiB; 250 GiB → 2.000 × 128 MiB. Công thức: [tóm tắt P1 §4](../plans/2026-11-06-m7-p1-upload-summary.md).

## 4. Gửi tin kèm file

Ví dụ: An upload 2 ảnh, gửi qua websocket `SendMessage{room_id, cid, text: "", attachments: [ảnh 1, ảnh 2]}`. Bình nhận `msg_created` có `Message.attachments` (chỉ khoá, tên, loại, cỡ…, không có byte và URL), rồi xin URL theo §5.

**Đính kèm** (proto `Attachment`, `chatim/v1/attachment.proto`; `SendMessageRequest.attachments`, `Message.attachments`). Máy lấy các giá trị từ lúc upload và gửi lại trong tin; lúc gửi chat không đọc `media_uploads` và không gọi S3:

| Field | Kiểu | Nghĩa | Ví dụ |
|---|---|---|---|
| `key` | string | Khoá object từ `StartUpload` | `acme/an/20261107/0cq5tnm8k0a1b2c3d4e5f6g7h8` |
| `name` | string | Tên file hiện cho người xem | `hoa-don.pdf` |
| `mime` | string | Loại file (lời máy khai) | `application/pdf` |
| `size` | int64 | Cỡ (byte) | `183204` |
| `dim` | repeated int32 | `[rộng, cao]` (ảnh/video), rỗng nếu không có | `[1920, 1080]` |
| `dur` | int64 | Thời lượng video/audio, mili giây; 0 nếu không có | `95400` |
| `hash` | string | `sha256:` + 64 hex thường, SHA-256 cả file do máy gửi tính; rỗng nếu không có | `sha256:9f86d081…` |

Trong doc `messages`, đính kèm nằm ở field `files: [{key, name, mime, size, dim, dur, hash}]` (bỏ `dim`, `dur`, `hash` khi rỗng; tin không đính kèm không có `files`).

**Luật lúc gửi** (sai một luật → `invalid_argument`, cả tin bị từ chối):

| Luật | Ví dụ bị từ chối |
|---|---|
| Tối đa 10 đính kèm (`max_attachments`) | 11 ảnh trong một tin |
| Khoá đúng dạng `{tenant}/{người gửi}/{yyyymmdd}/{26 ký tự 0-9a-v}`, ≤ 512 byte, cùng tenant **và** của chính người gửi | Bình gửi khoá `acme/an/…` của An; khoá thumb `…h8.thumb` |
| Khoá không cũ hơn 24 giờ và không sớm hơn giờ server quá 5 phút, tính chính xác từ mốc mili giây trong khoá; ngày `yyyymmdd` phải khớp mốc đó (UTC) | Upload hôm qua lúc 9:00, gửi hôm nay lúc 9:01 |
| Không trùng khoá trong một tin | cùng ảnh đính hai lần |
| Tên 1..255 byte UTF-8, không ký tự điều khiển, không ký tự đổi chiều chữ (bidi, vd. U+202E làm `gpj.exe` hiện thành `exe.jpg`) | tên rỗng, tên có `\n` |
| `mime` dạng `type/subtype` ≤ 255 byte; server chuyển chữ thường trước khi lưu | `pdf`, `image/` |
| `size` ≥ 1; `dim` rỗng hoặc đúng 2 số ≥ 1; `dur` ≥ 0; `hash` rỗng hoặc đúng `sha256:<64 hex thường>` | `dim: [1920]`, `hash: "sha256:ABC…"` |
| Text được rỗng (`""`) **chỉ khi** có ≥ 1 đính kèm; text chỉ có dấu cách luôn bị từ chối, kể cả khi có đính kèm (gửi `""` thay vì `" "`) | tin rỗng không file; `" "` kèm ảnh |
| Tin hệ thống (`kind = system`) không kèm file (owner 2026-11-08); `grpcsrv` từ chối trước khi đọc gì, actor kiểm lại | backend của tenant gửi tin hệ thống "Lan đã rời nhóm" kèm ảnh |

**Forward:** đính kèm chép từ tin nguồn (cả `hash`), đính kèm máy gửi kèm trong request bị bỏ im lặng. Không kiểm chủ khoá và tuổi khoá (khoá là của người gửi gốc, có thể đã cũ), chỉ kiểm dạng và cùng tenant. Hai tin trỏ cùng object; người trong room nhận forward tải được file qua tin forward, kể cả khi tin gốc đã bị xoá.

**Sửa:** không đổi đính kèm; text khi sửa vẫn phải khác rỗng (thêm chú thích cho tin chỉ có ảnh được, xoá hết chú thích thì không).

**Xoá cho mọi người:** `files` bị xoá cùng text; event `msg_deleted` có `attachments` rỗng. Object trong kho không bị xoá; file không còn tin nào dùng chỉ được đánh dấu `retired_at` (§8).

**Ẩn / xoá lịch sử (theo người xem):** tin ẩn hoặc trước `cleared_at` trả về không có `attachments` và `meta`.

**Danh sách hội thoại:** `ListMyRooms` trả `LastPreview.file_mimes` (vd. `["image/jpeg", "image/jpeg"]`) để máy hiện "📷 2 ảnh" khi text rỗng; preview chỉ có loại, không có khoá.

**Hash:** máy nhận tải xong tự tính SHA-256 và so với `hash`; lệch thì báo "file đã bị thay đổi". Server không đọc byte nên không kiểm hash, cỡ, `dim` khớp file.

## 5. URL tải (`MediaService.GetAttachmentURLs`)

1. Máy gom các tin có `attachments` đang hiện trên màn hình (≤ 100 tin) và gọi **một lần** qua gateway: `GetAttachmentURLs{messages: [{room_id, thread, seq}, …]}`. Máy chỉ gửi khoá tin, không bao giờ gửi khoá object.
2. Module media hỏi chat (cổng in-process `VisibleAttachments`, cùng pipeline đọc lịch sử: quyền đọc room, tin không bị xoá, không bị ẩn, sau `cleared_at`; một lần đọc mỗi room) tin nào người hỏi đọc được, đọc `bucket` của các file đó trong `media_uploads` theo lô khoá (lọc `tenant_id` của người hỏi, 200 khoá mỗi lệnh), rồi ký URL GET cho từng file từ đúng bucket file đang nằm (không thấy doc upload thì ký từ `processing`).
3. Response: `messages: [{room_id, thread, seq, files: [{key, url, thumbnail_url}]}]`, `expires_at`. Tin không đọc được, không còn hoặc không có file thì im lặng không có trong kết quả (không lộ tin tồn tại). Khoá tin trùng chỉ tính một lần.
4. Máy cache URL tới `expires_at` (`MEDIA_DOWNLOAD_URL_TTL`, mặc định 1 giờ), hết hạn thì gọi lại.

URL tải là URL GET ký thường, không kèm tham số đổi header trả về: S3 trả file với đúng `Content-Type` máy khai lúc upload. Server không quyết mở hay tải; app/web tự quyết theo `mime` và `name` của đính kèm (vd. ảnh hiện trong khung chat, PDF có nút tải). Máy muốn lưu file với tên gốc thì tự đặt tên từ `name` khi lưu.

`thumbnail_url`:

- Chỉ có khi `media.thumbnails = true` **và** `mime` là `image/*` hoặc `video/*`; còn lại rỗng. Không bao giờ thay bằng URL file gốc.
- Ký từ khoá `{key}{MEDIA_THUMBNAIL_SUFFIX}` ở **cùng bucket với file** mà không hỏi S3 object có chưa: Lambda chưa xong hoặc không sinh được thì URL trả **404**; máy hiện ảnh gốc hoặc biểu tượng và hỏi lại sau.
- URL thumb cũng là URL ký thường; S3 trả loại Lambda đặt, máy mở thẳng URL đó làm ảnh.

## 6. Gallery "file tôi đã upload" (`MediaService.ListUploads`)

`ListUploads{kinds, before, page_size}` → `{files: [UploadedFile], next_before, expires_at}`; `UploadedFile` = `{key, name, mime, size, dim, dur, hash, created_at, expired, url, thumbnail_url}`.

- **Chỉ file của chính caller:** lọc bằng phép bằng trên `tenant_id`, `user_id` (lấy từ token) và `state = uploaded` qua index `{tenant_id: 1, user_id: 1, state: 1, _id: -1}`; không bao giờ lọc quyền bằng tiền tố khoá.
- **Mọi file đã upload xong**, đã gắn tin hay chưa, kể cả file của tin đã xoá; phiên đang upload hoặc đã huỷ không hiện.
- **Mới nhất trước:** xếp `_id` giảm dần (khoá xếp theo lúc `StartUpload`).
- **Trang:** `page_size` 1..100. Trang đủ `page_size` mục thì có `next_before` (khoá mục cuối); gọi lại với `before = next_before`. `next_before` rỗng là hết (trang cuối có thể rỗng). `before` phải là khoá upload của chính caller (kiểm field `tenant_id`/`user_id` của doc), nếu không → `invalid_argument`.
- **`kinds`:** `FILE_KIND_IMAGE` = `image/*`, `FILE_KIND_VIDEO` = `video/*`, `FILE_KIND_OTHER` = mọi loại khác; rỗng (hoặc đủ cả ba) = tất cả; `FILE_KIND_UNSPECIFIED` hay số lạ → `invalid_argument`.
- **`expired`:** chỉ cho file **còn ở `processing`** có `created_at + MEDIA_PROCESSING_RETENTION` (mặc định 30 ngày) đã qua → `expired = true`, không có `url`/`thumbnail_url`; máy hiện "file đã hết hạn". File đã chuyển sang `completed` không bao giờ `expired`. Module tính từ doc, không gọi S3. `created_at` là lúc `StartUpload`, không muộn hơn lúc S3 mở multipart, nên nhãn có thể bật sớm hơn lúc object thật sự mất, không bao giờ trễ hơn.
- File còn hạn có `url`, `thumbnail_url` theo đúng luật của §5 (ký từ bucket file đang nằm), hạn tới `expires_at`.

Ví dụ: An mở tab "Ảnh của tôi": `ListUploads{kinds: [FILE_KIND_IMAGE], page_size: 30}` → 30 ảnh mới nhất và `next_before`; cuộn xuống gọi lại với `before`.

## 7. Thumb và hợp đồng Lambda (cho hạ tầng)

App chỉ upload file gốc. Thumb do AWS Lambda của hạ tầng sinh ở production; dev không có Lambda nên để `MEDIA_THUMBNAILS=false` và máy không nhận `thumbnail_url`. Chat và module media không sinh thumb, không lưu gì về thumb trong DB, chỉ suy khoá thumb từ khoá file, ký URL từ bucket của file, và khi chuyển file (§8) thì chuyển luôn thumb nếu nó đã có.

Luật chung: **file ở đâu thì thumb sinh ở đó.** Ví dụ: An upload ảnh X lúc 9:00, Lambda sinh `X.thumb` ở `processing` lúc 9:00:02; An gửi X lúc 9:01 → module copy cả X và `X.thumb` sang `completed`. Nếu An gửi ngay lúc 9:00:01 (Lambda chưa xong) → module chỉ copy X; bản copy tạo object X ở `completed`, Lambda chạy lần nữa ở đó và sinh `X.thumb` cạnh X; thumb muộn ở `processing` bị luật 30 ngày dọn.

| Mục | Hợp đồng |
|---|---|
| Đầu vào | Event `ObjectCreated:*` của **cả hai bucket** `processing` và `completed`. Phải là `ObjectCreated:*`, không chỉ `Put`/`CompleteMultipartUpload`: file chuyển sang `completed` được tạo bằng `CopyObject` (file ≤ 5 GiB) hoặc multipart copy (lớn hơn) |
| Bỏ qua | Mọi khoá kết thúc bằng hậu tố thumb (nếu không mỗi thumb lại kích Lambda một lần, vòng lặp tốn tiền), và khoá đã có thumb cạnh nó (file đã chuyển kèm thumb thì không sinh lại) |
| Đầu ra | Object `{key}{MEDIA_THUMBNAIL_SUFFIX}` (mặc định `.thumb`, vd. `acme/an/20261107/0cq5…h8.thumb`) **cùng bucket** với file đã kích nó |
| Content-Type | Một loại ảnh raster cố định do Lambda đặt (vd. `image/webp`); không SVG, không HTML |
| Nội dung | Đã xoá EXIF và metadata khác (vị trí GPS, máy chụp) |
| Luật sinh (cỡ, chất lượng, khung hình video, loại file nhận) | Của Lambda; module media không cần biết |
| Không sinh được (loại không hỗ trợ, file hỏng) | Không ghi gì; URL thumb trả 404, app hiện biểu tượng |
| Hậu tố | Phải bằng đúng `MEDIA_THUMBNAIL_SUFFIX` của chat (bắt đầu bằng `.`, 2..32 ký tự `a-z0-9.`) |
| Vòng đời | Thumb nằm cùng bucket nên luật lifecycle của `processing` (xoá sau 30 ngày) áp cho cả thumb; `completed` không có luật xoá. Luật xoá phải tính từ lúc **tạo object** (S3 lifecycle `Expiration` theo ngày tính từ lúc tạo; với multipart là lúc mở phiên), cùng mốc với nhãn `expired` (§6) và bằng `MEDIA_PROCESSING_RETENTION` |

**Domain phục vụ file (bắt buộc với hạ tầng).** File của người dùng được trả với đúng `Content-Type` máy khai lúc upload; server không đổi header trả về. Vì vậy domain của bucket hoặc CDN phục vụ file (`MEDIA_S3_PUBLIC_ENDPOINT`) phải **tách hẳn** khỏi domain của app: không phải subdomain của app, không cùng site (khác domain đăng ký). Nhờ vậy một file HTML hay SVG độc mà ai đó upload, khi mở ra, chỉ chạy trên domain của kho, không đọc được cookie hay gọi API của app. Ví dụ: app ở `chat.acme.com`; endpoint mặc định của AWS S3 (`*.s3.amazonaws.com`) đạt yêu cầu; CDN riêng phải dùng domain đăng ký khác như `acme-files.net`, không được dùng `files.acme.com`.

## 8. Chuyển file đã gắn tin sang `completed` (P3)

Ví dụ: An gửi tin R/42 kèm ảnh X và Y. Module hỏi chat: R/42 còn sống, có `[X, Y]`. Module ghi hai cặp (R/42, X), (R/42, Y), copy X, Y (và thumb nếu Lambda đã sinh) sang `completed`, đặt `bucket = completed` trong doc upload rồi xoá bản ở `processing`. Bình forward R/42 sang S/7: thêm cặp (S/7, X), (S/7, Y), không copy lại. An xoá R/42: hai cặp của R/42 thành `state = 2`; X, Y còn cặp sống của S/7 nên giữ nguyên, người trong S vẫn tải được. Bình xoá S/7: không còn cặp sống nào → X, Y được đặt `retired_at` (chỉ là dấu, object vẫn còn).

**Ai làm.** Consumer durable `media` trên `CHATIM_EVT` (tạo lần đầu chỉ nhận event mới: trước module media không tin nào có file, và phát lại 7 ngày stream ở 2K tin/s không bao giờ đuổi kịp), chạy trong mọi node chat (mọi node kéo chung một durable, mỗi event tới một node), chỉ kéo khi media sẵn sàng; media tắt thì không có consumer. Lọc 3 subject: `evt.*.media.*.msg_created` (tin có file mà không mention, [events §1](../events.md#1-stream-và-subject)), `evt.*.mention.*.msg_created` (tin có mention nằm ở loại data `mention`, kể cả khi có file) và `evt.*.message.*.msg_deleted`; tin text thường (loại `message`) không tới consumer. Mỗi node nhận event liên tục (không chờ lô, không đợi cả lô xong): mỗi lần kéo xin một nửa `MEDIA_CONSUMER_MAX_PENDING` (1024) và kéo tiếp khi chỗ đã xin còn dưới một nửa, nên ở 2K tin/s chỉ vài lần kéo mỗi giây. Một node giữ tối đa `MEDIA_CONSUMER_MAX_PENDING` event chưa Ack (cũng là `MaxAckPending` của durable); đầy thì ngừng nhận, không Nak. Mỗi event chạy trong goroutine riêng và Ack/Nak ngay khi xong, nên một lần copy S3 dài chỉ chiếm một chỗ, `msg_created` không file vẫn đi qua. Event của cùng một tin chạy lần lượt theo thứ tự giao, tin khác nhau chạy song song. Mỗi event có hạn `MEDIA_CONSUMER_SYNC_TIMEOUT` (15 phút, đủ copy file lớn); event nào còn giữ quá một phần ba `MEDIA_CONSUMER_ACK_WAIT` (30s) mới được gửi `InProgress`, lặp lại mỗi một phần ba, để không bị giao lại. Media mất sẵn sàng thì consumer ngừng kéo, xử lý nốt event đã nhận rồi chờ. Khi dừng node, consumer ngừng kéo, để event đang chạy xong trong 1,25s, Nak(0) event đã nhận mà chưa chạy. Consumer không nằm trên đường gửi tin, nên gửi tin không chậm đi.

**Mỗi event:**

- `msg_created` không có `attachments` → Ack ngay, không đọc gì (sửa tin không bao giờ thêm file).
- `msg_created` có file → hỏi chat **trạng thái hiện tại** của tin (một lần đọc doc tin theo `_id`, cổng in-process `MessageFiles`), không tin nội dung event. Không thấy tin, hoặc tin khác tenant với event → bỏ. Tin đã xoá → nhánh xoá. Tin còn sống → nhánh gắn với các khoá file trong doc tin.
- `msg_deleted` → thẳng nhánh xoá, không hỏi chat.

**Nhánh gắn** (≤ 10 file mỗi tin, lệnh theo lô):

1. Đọc lô doc upload của các khoá, lọc `tenant_id` của tin; khoá không mang tiền tố tenant của tin bị bỏ. Từng file: không có doc hoặc `aborted` → bỏ file đó; `uploading` → **cả event Nak, chờ** upload xong (bậc lùi 5s, 30s, 2m, 10m; không tính là kẹt), nhưng phiên `uploading` quá 24 giờ từ `StartUpload` (luật bucket đã huỷ phiên dở) → bỏ file đó; `uploaded` → gắn.
2. Ghi lô các cặp tin–file bằng upsert `$setOnInsert` theo `_id` cặp: cặp đã có, kể cả đã xoá mềm, giữ nguyên.
3. Gỡ `retired_at` của các file vừa gắn (một lệnh cho cả lô).
4. File còn ở `processing` thì chuyển; file đã ở `completed` (vd. forward) thì thôi:
   1. Copy server-side sang `completed`, cùng khoá: cỡ ≤ `MEDIA_COPY_SINGLE_MAX` (5 GiB, trần của S3) thì một lệnh `CopyObject`; lớn hơn thì multipart copy, mỗi phần `MEDIA_COPY_PART_BYTES` (1 GiB). `Content-Type` đặt từ doc upload. Copy xong đọc cỡ object ở `completed`; lệch cỡ trong doc → lỗi, Nak.
   2. Thumb bật và file là ảnh/video: nếu `{key}{hậu tố}` đã có ở `processing` thì copy theo (giữ loại Lambda đặt); chưa có thì thôi, Lambda sinh ở `completed` (§7).
   3. CAS `{_id, ver}` trên doc upload: `bucket = completed`, `attached_at` (giữ mốc lần đầu).
   4. Xoá bản ở `processing` (file, rồi thumb). Xoá lỗi chỉ ghi log và đếm `cleanup_error`; luật 30 ngày dọn sau.

Thứ tự **copy → đổi `bucket` → xoá bản cũ**: URL ký từ `processing` trước lúc đổi vẫn tải được tới lúc xoá; app gặp 404 đúng khoảnh khắc chuyển thì xin URL lại và nhận URL từ `completed`. Lỗi trước bước 3 thì doc vẫn trỏ `processing`, lần giao sau copy lại (ghi đè cùng byte).

**Nhánh xoá:**

1. Đọc khoá file của các cặp `state = 1` của tin (index `{tenant_id, message_key, state}`). Không có → xong.
2. Một lệnh `distinct` tìm trong các file đó file nào còn cặp `state = 1` của tin khác (index `{file_key, state, message_key}`, chỉ đọc index).
3. File không còn tin nào khác dùng → đặt `retired_at` (chỉ khi chưa có).
4. `updateMany` các cặp của tin → `state = 2`, `deleted_at`.

Đánh dấu trước, xoá mềm sau: lỗi giữa chừng thì lần giao sau làm lại đủ bước và ra cùng kết quả.

**Kết quả mỗi event:** xong hoặc bỏ → Ack; event hỏng dạng → Term; S3 lỗi, CAS thua, Mongo lỗi, chat chưa trả lời, quá hạn → Nak theo bậc lùi 5s, 30s, 2m, 10m, không retry nội bộ. Lần giao thứ 6 trở lên vẫn lỗi được đếm là kẹt (`chatim_media_stuck_total`, alert `ChatimMediaFailing`); consumer trễ (`chatim_media_lag_seconds` > 10 phút trong 15 phút) → `ChatimMediaLagging` (bảo đảm MD2, [architecture §8](../architecture.md#8-cam-kết-đúng-đắn-guarantees--detectors)).

**Event lệch thứ tự:** xoá tới trước tạo → chưa có cặp nào để xoá; tạo tới sau hỏi chat thấy tin đã xoá → không ghi cặp, không chuyển file. `msg_created` cũ tới lại sau khi cặp đã xoá mềm → `$setOnInsert` giữ `state = 2`. Phát lại (`/app resync`) không đổi kết quả.

**`media_message_files`** (mới, chỉ module media ghi, clustered):

| Field | Nghĩa | Ví dụ |
|---|---|---|
| `_id` | `{khoá tin}\|{khoá file}`, chống ghi trùng | `8812-0-42\|acme/an/20261108/0f9a…` |
| `message_key` | Khoá tin `{room}-{thread}-{seq}` | `8812-0-42` |
| `file_key` | Khoá file | `acme/an/20261108/0f9a…` |
| `tenant_id` | Tenant | `acme` |
| `state` | 1 tin còn dùng file, 2 tin đã bị xoá (xoá mềm, doc vẫn giữ) | `1` |
| `created_at`, `deleted_at` | Lúc ghi cặp, lúc tin bị xoá | |

Index `{tenant_id, message_key, state}` (cặp của một tin) và `{file_key, state, message_key}` (tin khác còn dùng file không); tra cứu luôn bằng phép bằng trên field, tạo qua `/app indexes` (D191).

**`media_uploads`** thêm 3 field:

| Field | Nghĩa |
|---|---|
| `bucket` | `processing` hoặc `completed`; thiếu = `processing`. URL tải, URL thumb và gallery ký từ bucket này |
| `attached_at` | Lúc file được chuyển lần đầu vì gắn tin |
| `retired_at` | Không còn cặp `state = 1` nào. **Chỉ là dấu**: không xoá object, URL vẫn ký được; cặp mới (vd. forward một tin cũ còn hiện) gỡ dấu. Chính sách xoá file đã `retired` làm sau |

## 9. Lỗi

| Mã | Khi nào |
|---|---|
| `invalid_argument` | Upload: tên rỗng/quá 255 byte/ký tự điều khiển hoặc bidi, `content_type` không dạng `type/subtype`, cỡ ngoài 1..`max_file_bytes`, `dim` không phải 0 hay 2 số ≥ 1, `dur` < 0, `hash` sai dạng, xin quá `max_parallel_uploads` phần hoặc phần ngoài 1..n, danh sách ETag thiếu, trùng, thừa. `SendMessage`: vi phạm luật §4. `GetAttachmentURLs`: 0 hoặc > 100 tin, `room_id` sai, `seq` = 0. `ListUploads`: `page_size` ngoài 1..100, `before` không phải upload của caller, `kinds` lạ |
| `not_found` | Upload: khoá không phải của caller hoặc không tồn tại |
| `failed_precondition` | Media tắt (`MEDIA_ENABLED=false`, mọi lệnh `MediaService`); phiên đã đóng/huỷ (`GetPartURLs`, huỷ sau khi đã xong); S3 từ chối ETag; cỡ thật lệch cỡ khai (object bị xoá, phiên thành `aborted`); phiên đã mất (quá 1 ngày) |
| `unavailable` | Media chưa sẵn sàng (config, bucket, key, S3); S3 lỗi; hai lệnh đổi trạng thái cùng lúc; node chat chưa nối xong cổng đọc cho media — gọi lại |
| `resource_exhausted` | Vượt giới hạn `GetPartURLs` hoặc giới hạn theo user của gateway (`GetAttachmentURLs`, `ListUploads` dùng giới hạn chung, không có limiter riêng) |
| `internal` | `ListUploads` trên DB thiếu index gallery: chạy `/app indexes -apply` một lần ([chat](../apps/chat.md#app-indexes-d191)) |

## 10. Cơ chế

- **Lớp dữ liệu:** "Phiên upload" (`media_uploads`, một doc mỗi object, `state` 1 `uploading` → 2 `uploaded` hoặc 3 `aborted`, CAS trên `state` + `ver`; `dim`, `dur` ghi lúc `StartUpload`, `hash` ghi trong cùng lệnh CAS sang `uploaded`) và "Object ghi một lần" (S3); P3 thêm `bucket`, `attached_at` (CAS `{_id, ver}`), `retired_at` (đặt khi chưa có, gỡ khi có cặp mới) vào phiên upload và cặp tin–file `media_message_files` theo khuôn `mentions` (D115: upsert `$setOnInsert`, xoá mềm `state = 2`). Đính kèm là một phần của **fact tin** (`messages.files`): ghi cùng lệnh insert của tin, bất biến khi sửa, `$unset` khi xoá; đi trong `Message` của `msg_created`/`msg_edited`/`msg_deleted`, phục hồi như tin (cờ `_state`, sweeper). URL tải và gallery không lưu gì. Không event mới; `msg_created` có file mà không mention đi loại data `media` (owner 2026-11-08) để consumer `media` không nhận tin text thường.
- **Quyền tải đi theo tin, không theo khoá:** URL chỉ được ký cho khoá nằm trong tin người hỏi đọc được, qua `Reader.VisibleMessages` của chat (chat cài cổng `mediaapp.ChatReader`). Gallery chỉ trả file có `tenant_id`, `user_id` của caller.
- **Không đính file người khác:** khoá phải mang `{tenant}/{người gửi}/`, user id không chứa `/`.
- **Đường gửi không chậm đi:** chat chỉ kiểm đính kèm trong RAM, không gọi S3 hay module media; khoá giả hoặc file chưa upload xong chỉ làm hỏng tin của chính người gửi (URL tải trả lỗi từ S3).
- **Không vượt cỡ khai:** server chỉ ký phần 1..n, mỗi URL ghim `Content-Length`; S3 chấp nhận đóng phiên với một tập con các phần nên server tự kiểm đủ 1..n rồi `HeadObject` kiểm tổng cỡ.
- **Gọi lại an toàn:** `CompleteUpload` gặp `NoSuchUpload` thì `HeadObject`; đúng cỡ → `uploaded`. `AbortUpload` thua một lần đóng đã xong thì trả `failed_precondition`, không đổi trạng thái.
- **URL ký trong RAM:** URL tải, URL thumb và URL phần ký bằng HMAC với endpoint public, không gọi S3; hạn trong khoảng S3 nhận (1 giây..7 ngày). URL ký không bao giờ vào log.
- **Sẵn sàng thay vì chặn boot:** module `HeadBucket` hai bucket lúc khởi động và mỗi `MEDIA_READY_CHECK_INTERVAL`; config sai hay S3 lỗi chỉ làm media chưa sẵn sàng, chat vẫn chạy.
- **Bucket ngoài code:** dev `make media-buckets`, production hạ tầng tạo `processing` (huỷ phiên dở 1 ngày, xoá object 30 ngày) và `completed` (không luật xoá). Key S3 của chat cần đọc, ghi (kể cả multipart) trên cả hai bucket và xoá ở `processing` ([chat](../apps/chat.md#media-module-modulesmedia)).
- **Không 1+N:** tin có file tốn một đọc tin, một đọc lô doc upload, một ghi lô cặp, một lệnh gỡ `retired_at`, rồi mỗi file lần đầu gắn một copy, một CAS, một xoá S3; forward file đã chuyển chỉ tốn đọc tin và ghi cặp. Xoá tin tốn một đọc cặp, một `distinct`, một lệnh đặt `retired_at`, một `updateMany`.

## 11. Hạn chế

- URL ký là bearer: ai cầm URL phần trong 15 phút PUT được đúng phần đó (đúng cỡ); ai cầm URL tải trong `MEDIA_DOWNLOAD_URL_TTL` tải được file dù không ở trong room.
- Tin bị xoá hoặc ẩn sau khi đã cấp URL: URL đã cấp vẫn dùng được tới hết hạn; lần hỏi sau không còn URL.
- File upload mà không gửi trong tin nào ở lại `processing` và bị xoá sau 30 ngày (gallery hiện `expired`).
- Consumer `media` trễ quá 30 ngày (vd. S3 lỗi kéo dài) thì luật của `processing` xoá cả file đã gắn tin trước khi kịp chuyển; `ChatimMediaLagging` (trễ > 10 phút trong 15 phút) báo sớm hơn rất nhiều.
- `retired_at` có thể sai trong hai chạy đua hẹp: thiếu dấu (tin bị xoá đúng lúc đang ghi cặp: thừa một cặp `state = 1`) hoặc thừa dấu (forward mới ghi cặp đúng lúc tin cuối của file bị xoá). Người dùng không thấy gì khác vì mọi đường đọc đi theo tin; chính sách xoá file sau này phải đếm lại cặp, không tin dấu.
- Tin gửi lúc file còn đang upload: event chờ (Nak) tới khi upload xong; phiên bị huỷ hoặc dở quá 24 giờ thì file bị bỏ, tin trỏ tới file không tồn tại và URL tải trả 404.
- Đúng khoảnh khắc chuyển file, URL vừa ký từ `processing` có thể trả 404; xin URL lại thì nhận URL từ `completed`.
- `media_message_files` chỉ lớn lên (xoá mềm không thu nhỏ), mỗi cặp tin–file từng có một doc nhỏ.
- Node dừng giữa lúc multipart copy file > 5 GiB để lại phần copy dở ở `completed`; `make media-buckets` chỉ đặt luật huỷ multipart dở cho `processing`.
- Tin gửi trước P3 không tự được chuyển; cần thì phát lại `msg_created` bằng `/app resync`.
- Chỉ đính được file upload trong 24 giờ; file cũ hơn trong gallery vẫn tải được nhưng muốn gửi lại thì forward tin cũ hoặc upload lại.
- `name`, `mime`, `size`, `dim`, `dur`, `hash` là lời máy khai, chỉ để hiển thị và để máy nhận tự kiểm; S3 trả file với đúng loại máy khai, nên domain phục vụ file phải tách khỏi domain của app (§7). Tin không có `hash` thì máy nhận không kiểm được file.
- URL thumb có thể 404 (Lambda chưa xong, file không sinh được thumb); dev không có thumb.
- Gallery xếp theo lúc mở upload: file mở trước nhưng xong sau khi con trỏ trang đã đi qua sẽ không hiện ở các trang sau (tải lại từ đầu thì thấy). Lọc `kinds` chạy sau index, nên người có nhiều file loại khác thì một trang quét nhiều doc hơn số trả về (chỉ trong file của chính người đó).
- `ReplyPreview` của tin trả lời một tin chỉ có ảnh có text rỗng; máy tự hiện "📷 Ảnh" khi cần.
- Giới hạn `GetPartURLs` của một thiết bị trần tốc độ ≈ 4 phần mỗi 5 giây (≈ 25 MiB/s với phần 32 MiB, ≈ 100 MiB/s với phần 128 MiB).
- Mỗi node tự kiểm sẵn sàng nên trong tối đa 30 giây các node có thể trả `media.enabled` khác nhau.
- Chưa quét mã độc (sau go-live).
