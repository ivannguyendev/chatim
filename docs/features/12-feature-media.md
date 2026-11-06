# Module 12: Media — upload nhiều phần (M7 P1)

> Phạm vi: M7 phase P1 ([tóm tắt](../plans/2026-11-06-m7-p1-upload-summary.md)). Gắn file vào tin (P2) và chuyển, dọn file (P3) chưa có ở đây. Module `modules/media` nhúng trong chat; gateway chuyển `MediaService` và `ChatService.GetClientConfig` tới node chat bất kỳ. Vận hành: [chat](../apps/chat.md#media-module-modulesmedia), [gateway](../apps/gateway.md).

## 1. Use case

| Mã | Use case | Ai | Mô tả |
|---|---|---|---|
| UC-MEDIA-00 | Hỏi config | Máy | `GetClientConfig` lúc mở app: `media.enabled` (bật và kho object đang trả lời), `max_file_bytes`, `max_parallel_uploads`, giới hạn tin (`max_attachments` 10) |
| UC-MEDIA-01 | Upload file tới vài GB | Thành viên | Chia phần do server quyết, byte đi thẳng từ máy lên S3 bằng URL ký, server không đọc byte nào |
| UC-MEDIA-02 | Huỷ upload | Thành viên | `AbortUpload` xoá các phần đã lên |

## 2. Luồng (spec client)

1. Gọi `GetClientConfig`. `media.enabled = false` → ẩn nút đính kèm; file lớn hơn `max_file_bytes` → báo ngay, không gọi server.
2. `StartUpload{file_name, content_type, size, upload_bytes_per_second}`. `upload_bytes_per_second` là tốc độ một luồng đo ở lần upload trước (0 = chưa biết); server dùng nó để chọn cỡ phần khoảng 8 giây mỗi phần. Response: `key`, `part_size`, `parts` (mọi phần `{part_number, size}`), `urls` (tối đa `max_parallel_uploads` URL dùng ngay), `expires_at` của các URL đó.
3. Chạy `max_parallel_uploads` luồng (mặc định 4). Mỗi luồng PUT đúng `size` byte của phần lên URL (URL ghim `Content-Length`: sai một byte là 403), giữ header `ETag` của response. PUT lỗi thì làm lại đúng phần đó (xin URL mới nếu URL đã hết hạn).
4. Xin URL theo vòng: một `GetPartURLs{key, part_numbers}` cho tối đa `max_parallel_uploads` phần kế, gọi khi bắt đầu vòng hiện tại để không phải chờ. Gateway giới hạn mỗi thiết bị (`sid`) 1 lần/5 giây, mỗi (user, IP) 1 lần/giây, mỗi user 2 lần/giây; vượt → `resource_exhausted`, chờ rồi gọi lại.
5. `CompleteUpload{key, parts: [{part_number, etag}]}` với đủ mọi phần. Hết hạn hoặc mạng rớt giữa chừng → gọi lại đúng lệnh đó: server nhận ra S3 đã đóng phiên và trả thành công.
6. Dùng `key` khi gửi tin (P2).

Trình duyệt: bucket phải cho CORS `PUT` và `ExposeHeaders: ETag`, nếu không máy không đọc được ETag.

## 3. Chia phần

Server quyết, máy không chọn: phần 16–128 MiB (trừ phần cuối), tối đa 2.000 phần, số phần là bội của số luồng khi file đủ lớn để cả 4 luồng xong cùng lúc. Ví dụ: 40 MiB → 16 + 16 + 8; 100 MiB → 4 × 25; 4 GiB (chưa biết tốc độ) → 128 × 32 MiB; 4 GiB ở 20 MB/s mỗi luồng → 32 × 128 MiB; 250 GiB → 2.000 × 128 MiB. Công thức: [tóm tắt §4](../plans/2026-11-06-m7-p1-upload-summary.md).

## 4. Lỗi

| Mã | Khi nào |
|---|---|
| `invalid_argument` | Tên rỗng/quá 255 byte/ký tự điều khiển, `content_type` không dạng `type/subtype`, cỡ ngoài 1..`max_file_bytes`, xin quá `max_parallel_uploads` phần hoặc phần ngoài 1..n, danh sách ETag thiếu, trùng, thừa |
| `not_found` | Khoá không phải của caller hoặc không tồn tại |
| `failed_precondition` | Media tắt; phiên đã đóng/huỷ (`GetPartURLs`, huỷ sau khi đã xong); S3 từ chối ETag; cỡ thật lệch cỡ khai (object bị xoá, phiên thành `aborted`); phiên đã mất (quá 1 ngày) |
| `unavailable` | Media chưa sẵn sàng (config, bucket, key, S3); S3 lỗi; hai lệnh đổi trạng thái cùng lúc — gọi lại |
| `resource_exhausted` | Vượt giới hạn `GetPartURLs` hoặc giới hạn theo user của gateway |

## 5. Cơ chế

- **Lớp dữ liệu:** "Phiên upload" (`media_uploads`, một doc mỗi object, `state` 1 `uploading` → 2 `uploaded` hoặc 3 `aborted`, CAS trên `state` + `ver`) và "Object ghi một lần" (S3). Không event NATS ở P1.
- **Không vượt cỡ khai:** server chỉ ký phần 1..n, mỗi URL ghim `Content-Length`; S3 chấp nhận đóng phiên với một tập con các phần nên server tự kiểm đủ 1..n rồi `HeadObject` kiểm tổng cỡ.
- **Gọi lại an toàn:** `CompleteUpload` gặp `NoSuchUpload` thì `HeadObject`; đúng cỡ → `uploaded`. `AbortUpload` thua một lần đóng đã xong thì trả `failed_precondition`, không đổi trạng thái.
- **Sẵn sàng thay vì chặn boot:** module `HeadBucket` hai bucket lúc khởi động và mỗi `MEDIA_READY_CHECK_INTERVAL`; config sai hay S3 lỗi chỉ làm media chưa sẵn sàng, chat vẫn chạy.
- **Bucket ngoài code:** dev `make media-buckets`, production hạ tầng tạo `processing` (huỷ phiên dở 1 ngày, xoá object 30 ngày) và `completed`.

## 6. Hạn chế

- URL ký là bearer: ai cầm URL trong 15 phút PUT được đúng phần đó (đúng cỡ).
- Giới hạn `GetPartURLs` của một thiết bị trần tốc độ ≈ 4 phần mỗi 5 giây (≈ 25 MiB/s với phần 32 MiB, ≈ 100 MiB/s với phần 128 MiB).
- Mỗi node tự kiểm sẵn sàng nên trong tối đa 30 giây các node có thể trả `media.enabled` khác nhau.
- Chưa quét mã độc (sau go-live); file đã gửi vẫn ở `processing` tới khi có P3.
