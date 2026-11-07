# Module 12: Media — upload, đính kèm trong tin, URL tải, gallery (M7 P1–P2)

> Phạm vi: M7 phase P1 ([tóm tắt](../plans/2026-11-06-m7-p1-upload-summary.md)) và P2 ([tóm tắt](../plans/2026-11-07-m7-p2-attachments-summary.md)). Chuyển và dọn file (P3) chưa có ở đây: mọi file còn ở bucket `processing`. Module `modules/media` nhúng trong chat; gateway chuyển `MediaService` và `ChatService.GetClientConfig` tới node chat bất kỳ; tin kèm file gửi qua websocket như tin thường. Vận hành: [chat](../apps/chat.md#media-module-modulesmedia), [gateway](../apps/gateway.md).

## 1. Use case

| Mã | Use case | Ai | Mô tả |
|---|---|---|---|
| UC-MEDIA-00 | Hỏi config | Máy | `GetClientConfig` lúc mở app: `media.enabled` (bật và kho object đang trả lời), `max_file_bytes`, `max_parallel_uploads`, `media.thumbnails` (server có sinh thumb), giới hạn tin (`max_attachments` 10) |
| UC-MEDIA-01 | Upload file tới vài GB | Thành viên | Chia phần do server quyết, byte đi thẳng từ máy lên S3 bằng URL ký, server không đọc byte nào |
| UC-MEDIA-02 | Huỷ upload | Thành viên | `AbortUpload` xoá các phần đã lên |
| UC-MEDIA-03 | Gửi tin kèm file | Thành viên, backend tenant | `SendMessage` qua websocket với tối đa 10 đính kèm; text được rỗng khi có đính kèm; forward chép file của tin nguồn |
| UC-MEDIA-04 | Xem và tải file trong tin | Thành viên đọc được tin | `GetAttachmentURLs` một lần cho cả màn hình (≤ 100 tin), nhận URL tải và URL thumb |
| UC-MEDIA-05 | Gallery "file tôi đã upload" | Người upload | `ListUploads`: mọi file mình đã upload xong, mới nhất trước, lọc ảnh/video/khác, nhãn `expired` khi kho đã xoá |
| UC-MEDIA-06 | Thumb ảnh, video | Hạ tầng | Lambda production sinh `{key}{hậu tố}` từ file gốc; server chỉ suy khoá và ký URL (§7) |

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

**Forward:** đính kèm chép từ tin nguồn (cả `hash`), đính kèm máy gửi kèm trong request bị bỏ im lặng. Không kiểm chủ khoá và tuổi khoá (khoá là của người gửi gốc, có thể đã cũ), chỉ kiểm dạng và cùng tenant. Hai tin trỏ cùng object; người trong room nhận forward tải được file qua tin forward, kể cả khi tin gốc đã bị xoá.

**Sửa:** không đổi đính kèm; text khi sửa vẫn phải khác rỗng (thêm chú thích cho tin chỉ có ảnh được, xoá hết chú thích thì không).

**Xoá cho mọi người:** `files` bị xoá cùng text; event `msg_deleted` có `attachments` rỗng. Object trong kho không bị xoá.

**Ẩn / xoá lịch sử (theo người xem):** tin ẩn hoặc trước `cleared_at` trả về không có `attachments` và `meta`.

**Danh sách hội thoại:** `ListMyRooms` trả `LastPreview.file_mimes` (vd. `["image/jpeg", "image/jpeg"]`) để máy hiện "📷 2 ảnh" khi text rỗng; preview chỉ có loại, không có khoá.

**Hash:** máy nhận tải xong tự tính SHA-256 và so với `hash`; lệch thì báo "file đã bị thay đổi". Server không đọc byte nên không kiểm hash, cỡ, `dim` khớp file.

## 5. URL tải (`MediaService.GetAttachmentURLs`)

1. Máy gom các tin có `attachments` đang hiện trên màn hình (≤ 100 tin) và gọi **một lần** qua gateway: `GetAttachmentURLs{messages: [{room_id, thread, seq}, …]}`. Máy chỉ gửi khoá tin, không bao giờ gửi khoá object.
2. Module media hỏi chat (cổng in-process `VisibleAttachments`, cùng pipeline đọc lịch sử: quyền đọc room, tin không bị xoá, không bị ẩn, sau `cleared_at`; một lần đọc mỗi room) tin nào người hỏi đọc được, rồi ký URL GET cho từng file.
3. Response: `messages: [{room_id, thread, seq, files: [{key, url, thumbnail_url}]}]`, `expires_at`. Tin không đọc được, không còn hoặc không có file thì im lặng không có trong kết quả (không lộ tin tồn tại). Khoá tin trùng chỉ tính một lần.
4. Máy cache URL tới `expires_at` (`MEDIA_DOWNLOAD_URL_TTL`, mặc định 1 giờ), hết hạn thì gọi lại.

URL tải là URL GET ký thường, không kèm tham số đổi header trả về: S3 trả file với đúng `Content-Type` máy khai lúc upload. Server không quyết mở hay tải; app/web tự quyết theo `mime` và `name` của đính kèm (vd. ảnh hiện trong khung chat, PDF có nút tải). Máy muốn lưu file với tên gốc thì tự đặt tên từ `name` khi lưu.

`thumbnail_url`:

- Chỉ có khi `media.thumbnails = true` **và** `mime` là `image/*` hoặc `video/*`; còn lại rỗng. Không bao giờ thay bằng URL file gốc.
- Ký từ khoá `{key}{MEDIA_THUMBNAIL_SUFFIX}` mà không hỏi S3 object có chưa: Lambda chưa xong hoặc không sinh được thì URL trả **404**; máy hiện ảnh gốc hoặc biểu tượng và hỏi lại sau.
- URL thumb cũng là URL ký thường; S3 trả loại Lambda đặt, máy mở thẳng URL đó làm ảnh.

## 6. Gallery "file tôi đã upload" (`MediaService.ListUploads`)

`ListUploads{kinds, before, page_size}` → `{files: [UploadedFile], next_before, expires_at}`; `UploadedFile` = `{key, name, mime, size, dim, dur, hash, created_at, expired, url, thumbnail_url}`.

- **Chỉ file của chính caller:** lọc bằng phép bằng trên `tenant_id`, `user_id` (lấy từ token) và `state = uploaded` qua index `{tenant_id: 1, user_id: 1, state: 1, _id: -1}`; không bao giờ lọc quyền bằng tiền tố khoá.
- **Mọi file đã upload xong**, đã gắn tin hay chưa, kể cả file của tin đã xoá; phiên đang upload hoặc đã huỷ không hiện.
- **Mới nhất trước:** xếp `_id` giảm dần (khoá xếp theo lúc `StartUpload`).
- **Trang:** `page_size` 1..100. Trang đủ `page_size` mục thì có `next_before` (khoá mục cuối); gọi lại với `before = next_before`. `next_before` rỗng là hết (trang cuối có thể rỗng). `before` phải là khoá upload của chính caller (kiểm field `tenant_id`/`user_id` của doc), nếu không → `invalid_argument`.
- **`kinds`:** `FILE_KIND_IMAGE` = `image/*`, `FILE_KIND_VIDEO` = `video/*`, `FILE_KIND_OTHER` = mọi loại khác; rỗng (hoặc đủ cả ba) = tất cả; `FILE_KIND_UNSPECIFIED` hay số lạ → `invalid_argument`.
- **`expired`:** `created_at + MEDIA_PROCESSING_RETENTION` (mặc định 30 ngày) đã qua → `expired = true`, không có `url`/`thumbnail_url`; máy hiện "file đã hết hạn". Module tính từ doc, không gọi S3. `created_at` là lúc `StartUpload`, không muộn hơn lúc S3 mở multipart, nên nhãn có thể bật sớm hơn lúc object thật sự mất, không bao giờ trễ hơn.
- File còn hạn có `url`, `thumbnail_url` theo đúng luật của §5, hạn tới `expires_at`.

Ví dụ: An mở tab "Ảnh của tôi": `ListUploads{kinds: [FILE_KIND_IMAGE], page_size: 30}` → 30 ảnh mới nhất và `next_before`; cuộn xuống gọi lại với `before`.

## 7. Thumb và hợp đồng Lambda (cho hạ tầng)

App chỉ upload file gốc. Thumb do AWS Lambda của hạ tầng sinh ở production; dev không có Lambda nên để `MEDIA_THUMBNAILS=false` và máy không nhận `thumbnail_url`. Chat và module media không sinh thumb, không lưu gì về thumb trong DB, chỉ suy khoá thumb từ khoá file và ký URL.

| Mục | Hợp đồng |
|---|---|
| Đầu vào | Event `ObjectCreated` của bucket `processing`. Lambda **phải bỏ qua** mọi khoá kết thúc bằng hậu tố thumb, nếu không mỗi thumb lại kích Lambda một lần (vòng lặp tốn tiền) |
| Đầu ra | Object `{key}{MEDIA_THUMBNAIL_SUFFIX}` (mặc định `.thumb`, vd. `acme/an/20261107/0cq5…h8.thumb`) **cùng bucket** với file gốc |
| Content-Type | Một loại ảnh raster cố định do Lambda đặt (vd. `image/webp`); không SVG, không HTML |
| Nội dung | Đã xoá EXIF và metadata khác (vị trí GPS, máy chụp) |
| Luật sinh (cỡ, chất lượng, khung hình video, loại file nhận) | Của Lambda; module media không cần biết |
| Không sinh được (loại không hỗ trợ, file hỏng) | Không ghi gì; URL thumb trả 404, app hiện biểu tượng |
| Hậu tố | Phải bằng đúng `MEDIA_THUMBNAIL_SUFFIX` của chat (bắt đầu bằng `.`, 2..32 ký tự `a-z0-9.`) |
| Vòng đời | Thumb nằm cùng bucket nên luật lifecycle của `processing` (xoá sau 30 ngày) áp cho cả thumb. Luật xoá phải tính từ lúc **tạo object** (S3 lifecycle `Expiration` theo ngày tính từ lúc tạo; với multipart là lúc mở phiên), cùng mốc với nhãn `expired` (§6) và bằng `MEDIA_PROCESSING_RETENTION` |

**Domain phục vụ file (bắt buộc với hạ tầng).** File của người dùng được trả với đúng `Content-Type` máy khai lúc upload; server không đổi header trả về. Vì vậy domain của bucket hoặc CDN phục vụ file (`MEDIA_S3_PUBLIC_ENDPOINT`) phải **tách hẳn** khỏi domain của app: không phải subdomain của app, không cùng site (khác domain đăng ký). Nhờ vậy một file HTML hay SVG độc mà ai đó upload, khi mở ra, chỉ chạy trên domain của kho, không đọc được cookie hay gọi API của app. Ví dụ: app ở `chat.acme.com`; endpoint mặc định của AWS S3 (`*.s3.amazonaws.com`) đạt yêu cầu; CDN riêng phải dùng domain đăng ký khác như `acme-files.net`, không được dùng `files.acme.com`.

## 8. Lỗi

| Mã | Khi nào |
|---|---|
| `invalid_argument` | Upload: tên rỗng/quá 255 byte/ký tự điều khiển hoặc bidi, `content_type` không dạng `type/subtype`, cỡ ngoài 1..`max_file_bytes`, `dim` không phải 0 hay 2 số ≥ 1, `dur` < 0, `hash` sai dạng, xin quá `max_parallel_uploads` phần hoặc phần ngoài 1..n, danh sách ETag thiếu, trùng, thừa. `SendMessage`: vi phạm luật §4. `GetAttachmentURLs`: 0 hoặc > 100 tin, `room_id` sai, `seq` = 0. `ListUploads`: `page_size` ngoài 1..100, `before` không phải upload của caller, `kinds` lạ |
| `not_found` | Upload: khoá không phải của caller hoặc không tồn tại |
| `failed_precondition` | Media tắt (`MEDIA_ENABLED=false`, mọi lệnh `MediaService`); phiên đã đóng/huỷ (`GetPartURLs`, huỷ sau khi đã xong); S3 từ chối ETag; cỡ thật lệch cỡ khai (object bị xoá, phiên thành `aborted`); phiên đã mất (quá 1 ngày) |
| `unavailable` | Media chưa sẵn sàng (config, bucket, key, S3); S3 lỗi; hai lệnh đổi trạng thái cùng lúc; node chat chưa nối xong cổng đọc cho media — gọi lại |
| `resource_exhausted` | Vượt giới hạn `GetPartURLs` hoặc giới hạn theo user của gateway (`GetAttachmentURLs`, `ListUploads` dùng giới hạn chung, không có limiter riêng) |
| `internal` | `ListUploads` trên DB thiếu index gallery: chạy `/app indexes -apply` một lần ([chat](../apps/chat.md#app-indexes-d191)) |

## 9. Cơ chế

- **Lớp dữ liệu:** "Phiên upload" (`media_uploads`, một doc mỗi object, `state` 1 `uploading` → 2 `uploaded` hoặc 3 `aborted`, CAS trên `state` + `ver`; `dim`, `dur` ghi lúc `StartUpload`, `hash` ghi trong cùng lệnh CAS sang `uploaded`) và "Object ghi một lần" (S3). Đính kèm là một phần của **fact tin** (`messages.files`): ghi cùng lệnh insert của tin, bất biến khi sửa, `$unset` khi xoá; đi trong `Message` của `msg_created`/`msg_edited`/`msg_deleted`, phục hồi như tin (cờ `_state`, sweeper). URL tải và gallery không lưu gì. Không event hay subject mới.
- **Quyền tải đi theo tin, không theo khoá:** URL chỉ được ký cho khoá nằm trong tin người hỏi đọc được, qua `Reader.VisibleMessages` của chat (chat cài cổng `mediaapp.ChatReader`). Gallery chỉ trả file có `tenant_id`, `user_id` của caller.
- **Không đính file người khác:** khoá phải mang `{tenant}/{người gửi}/`, user id không chứa `/`.
- **Đường gửi không chậm đi:** chat chỉ kiểm đính kèm trong RAM, không gọi S3 hay module media; khoá giả hoặc file chưa upload xong chỉ làm hỏng tin của chính người gửi (URL tải trả lỗi từ S3).
- **Không vượt cỡ khai:** server chỉ ký phần 1..n, mỗi URL ghim `Content-Length`; S3 chấp nhận đóng phiên với một tập con các phần nên server tự kiểm đủ 1..n rồi `HeadObject` kiểm tổng cỡ.
- **Gọi lại an toàn:** `CompleteUpload` gặp `NoSuchUpload` thì `HeadObject`; đúng cỡ → `uploaded`. `AbortUpload` thua một lần đóng đã xong thì trả `failed_precondition`, không đổi trạng thái.
- **URL ký trong RAM:** URL tải, URL thumb và URL phần ký bằng HMAC với endpoint public, không gọi S3; hạn trong khoảng S3 nhận (1 giây..7 ngày). URL ký không bao giờ vào log.
- **Sẵn sàng thay vì chặn boot:** module `HeadBucket` hai bucket lúc khởi động và mỗi `MEDIA_READY_CHECK_INTERVAL`; config sai hay S3 lỗi chỉ làm media chưa sẵn sàng, chat vẫn chạy.
- **Bucket ngoài code:** dev `make media-buckets`, production hạ tầng tạo `processing` (huỷ phiên dở 1 ngày, xoá object 30 ngày) và `completed`.

## 10. Hạn chế

- URL ký là bearer: ai cầm URL phần trong 15 phút PUT được đúng phần đó (đúng cỡ); ai cầm URL tải trong `MEDIA_DOWNLOAD_URL_TTL` tải được file dù không ở trong room.
- Tin bị xoá hoặc ẩn sau khi đã cấp URL: URL đã cấp vẫn dùng được tới hết hạn; lần hỏi sau không còn URL.
- Mọi file còn ở `processing` tới khi có P3: file của tin quá 30 ngày bị xoá, URL tải trả 404 ("file không còn").
- Chỉ đính được file upload trong 24 giờ; file cũ hơn trong gallery vẫn tải được nhưng muốn gửi lại thì forward tin cũ hoặc upload lại.
- `name`, `mime`, `size`, `dim`, `dur`, `hash` là lời máy khai, chỉ để hiển thị và để máy nhận tự kiểm; S3 trả file với đúng loại máy khai, nên domain phục vụ file phải tách khỏi domain của app (§7). Tin không có `hash` thì máy nhận không kiểm được file.
- URL thumb có thể 404 (Lambda chưa xong, file không sinh được thumb); dev không có thumb.
- Gallery xếp theo lúc mở upload: file mở trước nhưng xong sau khi con trỏ trang đã đi qua sẽ không hiện ở các trang sau (tải lại từ đầu thì thấy). Lọc `kinds` chạy sau index, nên người có nhiều file loại khác thì một trang quét nhiều doc hơn số trả về (chỉ trong file của chính người đó).
- `ReplyPreview` của tin trả lời một tin chỉ có ảnh có text rỗng; máy tự hiện "📷 Ảnh" khi cần.
- Giới hạn `GetPartURLs` của một thiết bị trần tốc độ ≈ 4 phần mỗi 5 giây (≈ 25 MiB/s với phần 32 MiB, ≈ 100 MiB/s với phần 128 MiB).
- Mỗi node tự kiểm sẵn sàng nên trong tối đa 30 giây các node có thể trả `media.enabled` khác nhau.
- Chưa quét mã độc (sau go-live).
