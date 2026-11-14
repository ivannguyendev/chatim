# M7.1 phase 4: ảnh nhóm — tóm tắt

> Ngày: 2026-11-13 · Nhánh: `feat/m7-1` · Trạng thái: `dev-done` 2026-11-14 (`4f59e80` → `7088d2e`)
> Nguồn yêu cầu: web cần ảnh nhóm do admin đặt (owner chuyển 2026-11-08); owner 2026-11-10: không có ảnh hiện lâu dài, cần hình nào ký hình đó, chat không gọi media; owner 2026-11-13: file media có hai loại, **public** (đăng nhập là xem được) và **private** (kiểm quyền như nay); tin nhắn chỉ nhận file private, ảnh nhóm chỉ nhận file public; mỗi lệnh lấy URL chỉ ký đúng một loại; owner 2026-11-14: "File của tôi" không hiện file public.

## Scope và ranh giới

| Việc | Trong phase 4? | Thuộc về |
|---|---|---|
| Upload file public: `StartUpload{public: true}` cấp khoá có đoạn `pub` | Có | module `media` |
| `ListUploads` ("File của tôi") chỉ trả file private | Có | module `media` (gallery) |
| `Room.avatars` (khoá file public) thay `Room.avatar` (chuỗi); `UpdateRoom` đặt/xoá ảnh, kiểm khoá trong RAM | Có | chat (room) |
| Tin nhắn từ chối khoá public | Có | chat (message) |
| Giữ file ảnh nhóm: nghe `room_updated`, chuyển ảnh sang `completed`, đánh dấu `retired` ảnh cũ | Có | module `media` (collection mới `media_room_avatars`) |
| `MediaService.GetPublicURLs`: ký URL file public theo lô, chỉ cần đăng nhập cùng tenant | Có | module `media` |
| Route gateway, e2e, docs | Có | `pkg/chatrpc`, `tools/chatcli`, `docs/` |
| Ghép ảnh khi nhóm chưa có ảnh | Không | client, từ `avatar_users` (P3) + `GetUsers` |
| Cắt, thu nhỏ ảnh | Không | client trước khi upload; thumb do Lambda (M7) |
| Chính sách xoá file đã `retired` | Không | chưa có (M7 P3) |
| DM | Không đổi | `UpdateRoom` vẫn từ chối DM |

## Tính năng → lớp dữ liệu (architecture §4)

| Tính năng | Lớp | Ghi | Phục hồi |
|---|---|---|---|
| Khoá public trong `media_uploads` | Doc upload sẵn có (M7 P1), không field mới: loại file nằm trong khoá | `StartUpload` | như upload thường |
| `rooms.avatars` | Thông tin room (CAS `info_ver`, bit 4, `room_updated`), như tên nhóm | `UpdateRoom` | Sweeper phát lại `room_updated`; `/app resync` |
| `media_room_avatars` (mỗi room một doc) | Tập theo event của module | Consumer `media` nghe `room_updated`, đọc room hiện tại qua cổng chat, ghi khi `info_ver` mới hơn | JetStream giao lại; sweeper/resync phát lại `room_updated` |
| `GetPublicURLs` | Chỉ đọc | — | — |

## 1. Thuật ngữ

| Từ | Nghĩa |
|---|---|
| Khoá private | `{tenant}/{user}/{yyyymmdd}/{26 ký tự}` (như trước); dùng trong tin nhắn; xem được khi đọc được tin |
| Khoá public | `{tenant}/{user}/pub/{yyyymmdd}/{26 ký tự}`; chỉ ảnh; dùng làm ảnh nhóm; ai đăng nhập cùng tenant và biết khoá là xin được URL |
| `info_ver` | Version thông tin room, tăng mỗi lần `UpdateRoom` đổi gì đó (D133) |
| `retired_at` | Dấu "không còn ai dùng file" trên `media_uploads` (M7 P3); chỉ là dấu, chưa xoá, URL vẫn ký được |

## 2. Bức tranh chung

Ví dụ: An (owner nhóm "Kho HCM") chọn ảnh `kho.png`:

1. Web gọi `StartUpload{…, public: true}` → khoá `acme/an/pub/20261113/0cr1…k9`, upload như file thường.
2. `UpdateRoom{room_id, avatars: {keys: ["acme/an/pub/20261113/0cr1…k9"]}}` → chat kiểm khoá trong RAM (dạng public, tenant `acme`, người upload là An, upload trong 24 giờ), ghi room `info_ver` 3 → 4, phát `room_updated`.
3. Consumer `media` nhận `room_updated`, hỏi chat room hiện tại (`avatars`, `info_ver 4`), chuyển `kho.png` sang bucket `completed`, ghi `media_room_avatars {file_key: …k9, info_ver: 4}`; ảnh cũ (nếu có) không còn nhóm nào dùng → `retired_at`.
4. Bình mở danh sách hội thoại: `ListMyRooms` trả `avatars` của 20 nhóm; web gom các khoá vào một `GetPublicURLs{keys: [...]}` → nhận URL ký. Nhóm không có ảnh dùng `avatar_users` (P3).
5. An thử gửi `kho.png` (khoá public) trong tin → `INVALID_ARGUMENT`. An thử đặt `hopdong.pdf` (khoá private) làm ảnh nhóm → `INVALID_ARGUMENT`. "File của tôi" của An không có `kho.png`.

```
web ─StartUpload{public}─▶ media: khoá …/pub/…
web ─UpdateRoom─▶ gateway ─▶ chat: kiểm khoá public trong RAM, CAS info_ver, room_updated ─▶ JetStream
                      module media (consumer media) ◀──────── room.*.room_updated ──────────┘
                       hỏi chat room hiện tại → move completed → media_room_avatars (CAS info_ver) → retire cũ
web ─GetPublicURLs─▶ gateway ─▶ media: khoá public + tenant từ token → media_uploads → ký URL (không hỏi chat)
```

## 3. Dữ liệu

| Field | Nghĩa | Ví dụ | Dùng để |
|---|---|---|---|
| `StartUploadRequest.public` (field 7) | `true` = cấp khoá public; chỉ nhận `image/*` | `true` | Upload ảnh nhóm |
| `rooms.avatars` | 0–1 khoá public; thay field `avatar` (không migration, chưa có production) | `["acme/an/pub/20261113/0cr1…k9"]` | Client biết nhóm có ảnh và khoá nào |
| `Room.avatars` (field 12); `Room.avatar` = `reserved 8` | như trên, trong `GetRooms`, `ListMyRooms`, `UpdateRoom`, event | | |
| `UpdateRoomRequest.avatars` = `RoomAvatars {repeated string keys}` (field 4, message nên có/không có phân biệt được); `avatar` = `reserved 3` | Không gửi = giữ nguyên; `keys: []` (JSON `"avatars": {}`) = xoá ảnh; 1 khoá = đặt ảnh | | Phân biệt "không đổi" và "xoá" |
| `media_room_avatars` (clustered, `_id` = room id) | `tenant_id`, `file_key` (không có = không có ảnh), `info_ver`, `updated_at`; index `{file_key}` | `{_id: "903…147", file_key: "…k9", info_ver: 4}` | Biết ảnh cũ để retire; biết khoá còn nhóm khác dùng |
| `GetPublicURLsRequest.keys` | 1–100 khoá | 20 khoá | |
| `GetPublicURLsResponse` | `files: [FileURL{key, url, thumbnail_url}]` (message sẵn có), `expires_at` | | Khoá private, tenant khác, chưa upload xong, không có: không có trong kết quả |

## 4. Luồng

**`StartUpload` (media):** thêm `public`; `public` mà `content_type` (đã chuyển chữ thường) không phải `image/*` → `INVALID_ARGUMENT`. Khoá public sinh cùng cách, thêm đoạn `pub`. `ParseKey` là một chỗ đọc khoá (tenant, user, loại); `GetPartURLs`, `CompleteUpload`, `AbortUpload` nhận cả hai loại khoá của chính người gọi.

**`ListUploads` ("File của tôi", media):** chỉ trả file private. Khoá private `acme/an/2026…` luôn xếp trước khoá public `acme/an/pub/…`, nên trang đầu đọc `_id < "acme/an/pub/"` trên index sẵn có (itest `explain`: IXSCAN, không SORT, không COLLSCAN), không lọc sau khi đọc. `before` bắt đầu bằng `{tenant}/{user}/pub/` của caller → `INVALID_ARGUMENT`; store không đổi.

**`UpdateRoom` (chat):** như trước, thêm: `avatars` quá 1 khoá → `INVALID_ARGUMENT` trước mọi đọc; sau kiểm quyền (owner/admin; DM → `FAILED_PRECONDITION`), nếu `avatars` khác giá trị hiện tại thì mỗi khoá phải là khoá public, đúng tenant, người gọi là người upload, ngày trong khoá khớp mốc giờ, tạo trong `[bây giờ − 24 giờ, bây giờ + 5 phút]` (luật thời gian của đính kèm, D266) → sai thì `INVALID_ARGUMENT`. Gửi lại đúng khoá đang dùng không kiểm (admin khác đổi tên vẫn được). Không gọi media.

**`SendMessage`/forward (chat):** chat đọc khoá bằng một hàm chung cho đính kèm và ảnh nhóm; đính kèm (cả forward) từ chối khoá public.

**Consumer `media` (`room_updated`):**
1. Durable `media` thêm subject `evt.*.room.*.room_updated`; giải mã cần tenant, room id, `ts`, không cần seq (tin nhắn vẫn cần seq); khoá chuỗi `{room}-0-0` nên event của một room chạy lần lượt.
2. Hỏi chat room hiện tại (`avatars`, `info_ver`, tenant, loại) — đọc thẳng DB, không cache. Room không có → bỏ (`no_room`); khác tenant → bỏ (`tenant_mismatch`); DM → Ack.
3. Doc `media_room_avatars` có `info_ver` ≥ room → Ack. Khoá trong `avatars` không phải khoá public của tenant room → coi như không có ảnh. Nhóm chưa từng có doc và nay không có ảnh (vd. đổi tên) → Ack, không ghi.
4. Room có ảnh (mỗi event, kể cả chỉ đổi tên): đọc upload; đang upload (< 24 giờ) → Nak chờ; huỷ, hết hạn, không có → đếm drop, đi tiếp; đã upload → gỡ `retired_at`, chuyển sang `completed` nếu chưa (cùng hàm của M7 P3, kèm thumb). Làm trước khi ghi doc nên giao lại thì làm lại được.
5. Ghi cả doc bằng `ReplaceOne` upsert lọc `{_id, info_ver: cũ}` (cũ = 0 → `info_ver` chưa có); trùng khoá → `ErrRetryLater`, Nak. Doc thiếu room, tenant hoặc `info_ver` bị từ chối ở cả hai store.
6. Sau khi ghi: gỡ `retired_at` của ảnh hiện tại lần nữa. Khoá cũ khác khoá mới và không doc nào trỏ tới (tính cả room vừa ghi) → `retired_at`, rồi hỏi lại; doc nào đã trỏ tới khoá đó trong lúc ấy → gỡ dấu.

**`GetPublicURLs` (media, package `publicfiles`):** chưa sẵn sàng → `UNAVAILABLE`; 0 hoặc > 100 khoá, khoá sai dạng → `INVALID_ARGUMENT`; bỏ trùng, giữ thứ tự; khoá private hoặc tenant khác token → bỏ, không còn khoá nào thì không truy vấn; một `media_uploads.GetMany` lọc `tenant_id` từ token; doc `uploaded` cùng tenant → ký URL file + thumb từ bucket của file (hạn `MEDIA_DOWNLOAD_URL_TTL`); lỗi ký → `ErrRetryLater`. Không hỏi chat.

**Lệnh private sẵn có** (`GetAttachmentURLs`, `ListRoomFiles`) không đổi: chúng đi qua tin nhắn, mà tin nhắn chỉ có khoá private.

## 5. Đúng đắn và chạy đua

- **Đổi ảnh hai lần nhanh** (A → B): consumer luôn đọc room hiện tại, chỉ ghi khi `info_ver` mới hơn doc → kết quả là B, A được xét retire.
- **A → B → A trên hai node:** node 1 ghi B rồi hỏi "còn doc nào trỏ A"; node 2 đã ghi lại A → câu hỏi tính cả chính room nên thấy A, không retire (test `TestTwoNodesInterleavedNeverRetireTheShownAvatar`).
- **Retire tới muộn** (review T3): node Y hỏi "A còn ai dùng" → không, rồi chậm; node Z đặt lại A (gỡ dấu, ghi doc); Y mới đánh dấu A. Sửa: Y hỏi lại sau khi đánh dấu và gỡ dấu nếu A đang được dùng; Z gỡ dấu A lần nữa sau khi ghi doc. Hai bước cùng phủ mọi thứ tự (test `TestARetireThatLandsAfterTheAvatarIsShownAgainIsUndone`).
- **Hai node cùng xử lý một room:** CAS theo `info_ver` của doc; bên thua Nak rồi đọc lại.
- **Một ảnh làm ảnh hai nhóm:** retire chỉ khi không còn doc nào trỏ khoá đó.
- **Upload xong muộn** (phiên kéo quá 24 giờ, consumer đã bỏ qua): lần đổi room kế tiếp chuyển file sang `completed`.
- **Ghi doc trượt sau khi đã chuyển file:** giao lại thấy file đã ở `completed`, không copy lại.
- **Khoá giả hoặc của người khác:** chat chặn ở `UpdateRoom`; `GetPublicURLs` chỉ ký khoá public của tenant trong token.

## 6. Event

Không event mới. `room_updated` mang `Room` có `avatars`. Durable `media` lọc 4 subject (`docs/events.md`); đổi filter chỉ áp event mới. Không cần phát lại gì: trước P4 chưa nhóm nào có ảnh public.

## 7. Thư viện và hạ tầng

Không thêm thư viện. Collection mới `media_room_avatars` + index `{file_key: 1}` do bootstrap tạo (collection rỗng nên lần boot đầu tự build; sau đó qua `/app indexes`, D191). Metric `chatim_media_dropped_total` thêm lý do `no_room`. Bucket S3 không đổi: file public nằm cùng `processing`/`completed`, luật 30 ngày và Lambda thumb áp như file thường.

## 8. Quyết định và phương án bị loại

| Quyết định | Lý do | Phương án bị loại |
|---|---|---|
| File media có hai loại public/private; tin nhắn chỉ nhận private, ảnh nhóm chỉ nhận public; mỗi lệnh lấy URL chỉ ký một loại (owner 2026-11-13; D272) | Ảnh nhóm không cần kiểm thành viên nên lệnh lấy URL không hỏi chat; tách hẳn để người bị kick không mở được file trong tin cũ | Một loại, kiểm thành viên nhóm (`GetRoomAvatarURLs` theo room): cần cổng chat và truy vấn `members` mỗi lần. Cho tin nhắn nhận file public: lộ file trong tin cho người đã rời nhóm |
| Loại file nằm trong khoá (`…/pub/…`), không chỉ là field của doc upload | Chat đọc được khoá mà không gọi media nên kiểm được trong RAM; khoá đã cấp không đổi loại | Field `public` trong `media_uploads`: chat phải hỏi media mới biết |
| `avatars` (khoá file public) thay `avatar` (chuỗi tự do) (D273, gộp với `avatar_users` P3 thành "hợp đồng ảnh nhóm") | Owner 2026-11-10: không có ảnh hiện lâu dài; hai nguồn ảnh làm client rối; chưa có dữ liệu production | Giữ cả hai field. Link công khai cố định không cần đăng nhập: owner không chọn |
| "File của tôi" chỉ file private (owner 2026-11-14) | Danh sách để chọn file gửi vào tin; khoá public xếp sau khoá private nên trộn vào sẽ sai thứ tự | Hiện cả hai: phải đổi dạng khoá và thêm field `public` cho client |
| Chat kiểm khoá trong RAM, không gọi media | Một chiều chat → media (D182, D262); `UpdateRoom` không chậm vì media | Chat hỏi media "file đã upload chưa" |
| Media giữ một doc mỗi room, CAS theo `info_ver` | Biết ảnh cũ để retire; đúng khi event lệch thứ tự, gộp, A → B → A | Cặp room–file theo từng file: sai khi A → B → A và khi hai node cùng xử lý (review design 2026-11-10) |

Chọn lúc xây (không vào decision log): `UpdateRoomRequest.avatars` là field message thường (không `optional`, message vốn phân biệt có/không); chat có một hàm đọc khoá file chung cho đính kèm và ảnh nhóm (`file_key.go`); gallery chặn `before` public bằng tiền tố thay vì đọc khoá đầy đủ (test cũ dùng khoá rút gọn); doc `media_room_avatars` có `Validate` ở cả hai store (review T3 Minor).

## 9. Chi phí và tải

`UpdateRoom`, `SendMessage`: không thêm I/O. Consumer `media`: mỗi `room_updated` một đọc room, một đọc doc ảnh; nhóm có ảnh thêm một đọc upload, hai lệnh gỡ dấu, một ghi doc; khi đổi ảnh thêm một copy S3 (một lần), một hoặc hai tìm theo `file_key`. Nhóm chưa từng có ảnh đổi tên: không ghi gì. `GetPublicURLs` 100 khoá: một `GetMany` ≤ 100 khoá, ≤ 200 chữ ký trong RAM, không đọc chat.

## 10. Rủi ro và giới hạn

- Ai đăng nhập cùng tenant và còn giữ khoá ảnh nhóm (vd. người đã rời nhóm) vẫn xin được URL ảnh đó, kể cả sau khi admin gỡ ảnh, tới khi có chính sách xoá file (owner chấp nhận 2026-11-13). Người chưa từng thấy khoá thì không đoán được (80 bit ngẫu nhiên).
- Ảnh vừa đặt có thể ký từ bucket `processing` vài giây trước khi consumer chuyển; vẫn tải được.
- Ảnh đặt bằng khoá chưa upload xong: mọi `room_updated` của nhóm đó Nak chờ tối đa 24 giờ (review T3, đúng spec); huỷ thì `avatars` có khoá nhưng không có URL, client coi như không có ảnh.
- Node đọc room cũ rồi thua CAS có thể đã gỡ dấu hoặc chuyển một ảnh không còn dùng; file đó ở lại `completed` không dấu (review T3 Minor, chỉ là dấu).
- Đổi hợp đồng: `Room.avatar` và `UpdateRoomRequest.avatar` bỏ (reserved); client web cập nhật cùng bản (chưa có production).

## 11. Điểm đội tự chọn (owner phản biện)

- `avatars` nhận 0–1 khoá; dạng danh sách để sau thêm biến thể (nhiều cỡ) mà không đổi hợp đồng.
- Khoá public giữ tiền tố `{tenant}/{user}/` để kiểm người upload như trước.
- File public chỉ nhận `image/*`: dùng duy nhất cho ảnh nhóm.
- `GetPublicURLs` tối đa 100 khoá (bằng `GetAttachmentURLs`); gateway dùng giới hạn theo user chung của app.

## 12. Kiểm thử (như đã xây)

| Test | Chứng minh |
|---|---|
| Unit `media/domain` (`key_test.go`): khoá public 5 đoạn, `ParseKey`, `OwnsKey` cả hai loại, khoá giả; mọi khoá private (ngày 0001 và 9999, random toàn 0 và toàn `v`) xếp trước `{tenant}/{user}/pub/`, khoá của `an2` không mang mốc của `an`; khoá dài nhất | Khoá public, mốc gallery |
| Unit `upload`: `public` + `text/html`/`video/mp4`/`application/pdf` → `INVALID_ARGUMENT`; `Image/PNG` → khoá `/pub/`; người khác `not_found`; `GetPartURLs`, `Complete`, `Abort` với khoá public; grpcsrv mang `public` tới khoá | Upload public |
| Unit `gallery`: ảnh public không có ở trang đầu lẫn trang sau; `before` public → `INVALID_ARGUMENT`; itest `explain` trang đầu dưới mốc public: IXSCAN, không SORT/COLLSCAN | "File của tôi" |
| Unit chat `domain`: `ValidateRoomUpdate` (2 khoá), `Apply` (giữ khi chỉ đổi tên, xoá, đặt mới, bản sao), `ValidateAvatarKeys` (hợp lệ; private, người khác, tenant khác, 25 giờ, 10 phút sau, ngày lệch, `.thumb`, `pub` hai lần, rỗng); đính kèm và forward từ chối khoá public | `UpdateRoom`, tách hẳn |
| Unit `room`: admin đặt khoá của mình → event mang `avatars`; khoá người khác, private, 25 giờ → `INVALID_ARGUMENT`, room và event không đổi; `keys: []` xoá; đổi tên giữ ảnh; admin khác gửi lại khoá hiện tại (đã 25 giờ) cùng tên mới → được | `UpdateRoom` |
| Storetest chat (mem + mongo), codec, `$literal`, `pbconv`, grpcsrv, field pin websocket | Store, hợp đồng |
| Unit `attach` (16 test): ảnh đầu, event cũ, đổi tên không ảnh, đổi tên có ảnh không copy, upload muộn, đang upload, huỷ, room mất/khác tenant/DM, khoá không hợp lệ, retire khi không nhóm nào dùng, A → B → A, hai node xen kẽ, retire tới muộn, gỡ dấu sau khi ghi, ghi trượt rồi giao lại | Luồng §4, §5 |
| Unit consumer: `room_updated` không seq → `RoomUpdated`; tin nhắn không seq → Term; 4 subject; hai `room_updated` một room chạy lần lượt | Consumer |
| Storetest `media_room_avatars` (mem + mongo): CAS, thay cả doc, prev cũ/0 → retry, `InUse` tính room vừa ghi, tenant khác, doc thiếu `info_ver` bị từ chối; itest `explain` `InUse` dùng `{file_key}`; bootstrap clustered; cổng chat `RoomAvatars` với memstore và Mongo thật | Store media, cổng chat |
| Unit `publicfiles`: chỉ ký khoá public đã upload của tenant (bỏ private, tenant khác, đang upload, không có, trùng), bucket và thumb đúng, không đọc khi không còn khoá, doc tenant khác bị bỏ, 0/101 khoá, khoá sai dạng, chưa sẵn sàng, lỗi ký; grpcsrv, `chatrpc` 31/43 lệnh | Quyền, IDOR |
| e2e 16 bước 24–31: public `text/html` bị từ chối; PNG public có `/pub/`; khoá public trong tin → `InvalidArgument`; PNG private làm ảnh nhóm → `invalid_argument`; đặt ảnh → B `GetRooms` thấy; B `GetPublicURLs([public, private])` chỉ có public, đúng byte và SHA-256; `ListUploads` của A không có khoá public; trong 30s URL ở bucket completed; xoá ảnh → B không còn `avatars` | Qua gateway thật |

Kiểm cuối milestone (2026-11-14): `make fmt-check`/`vet`/`lint`/`buf-lint` sạch; `make test` 118 package ok; `make itest` 118 package ok (`tools/chatcli` không có test, một lần build lỗi do sửa file đúng lúc chạy, build lại ok); `make app-up && make e2e` PASS 16 phase, phase 16 đủ 31 bước; chatbench 2K tin/s 60s: 120.000 tin gửi và ack, 0 lỗi, p50 18,9 ms, p99 169 ms, pacer lag p99 3,3 ms, live 2.398/2.398, 0 thiếu, 0 trùng (`bin/bench/m7-1-p4/`).

Review: T2 một reviewer, không Critical/Important (Minor: kiểm khoá cả khi lệnh không đổi gì, một mã lỗi chung cho mọi khoá sai — giữ nguyên). T3 một reviewer: một Important (retire tới muộn, sửa ở `4e610f1`, test tái hiện) và ba Minor (doc thiếu `info_ver` — đã sửa; gỡ dấu thừa khi thua CAS, khoá đang upload giữ event của room tới 24 giờ — ghi ở §10).

## Bảng phủ

| Yêu cầu / quyết định | Thành phần | Task | Test | Bằng chứng |
|---|---|---|---|---|
| File public/private, loại nằm trong khoá (owner 2026-11-13) | proto `StartUpload`, `media/domain`, `upload` | T1 | Unit media domain/upload, e2e 16 | `d5e8c6c`; unit domain/upload/grpcsrv ok; e2e bước 24–25 |
| "File của tôi" chỉ file private (owner 2026-11-14) | `gallery`, `media/domain` (mốc khoá public) | T1 | Unit domain, unit `gallery`, itest explain, e2e 16 | `d5e8c6c`, `5dc196d`; itest mongostore media ok; e2e bước 29 |
| Admin đặt ảnh nhóm (roadmap M7.1 P4) | proto, chat `domain`, `room.UpdateRoom`, store chat | T2 | Unit `room`, storetest, e2e 16 | `152a530`; itest store chat ok; e2e bước 28, 31 |
| `avatars` thay `avatar` (roadmap; design 2026-11-10) | proto reserved, codec, `pbconv`, chatcli, ws field pin | T2 | Unit `pbconv`, field pin, codec | `152a530`; unit pbconv/command/mongostore ok |
| Tin nhắn chỉ private, ảnh nhóm chỉ public (owner 2026-11-13) | chat `domain` | T2 | Unit đính kèm/forward/`ValidateAvatarKeys`, e2e 16 | `152a530`; e2e bước 26–27 |
| Chat không gọi media (roadmap) | kiểm khoá trong RAM | T2 | Unit `room` (không cổng media) | `152a530`; rig `room` không có cổng media |
| Giữ file ảnh nhóm, retire ảnh cũ (design 2026-11-10) | consumer media, `media_room_avatars`, cổng chat đọc room | T3 | Unit attach/consumer, storetest, itest, e2e 16 | `3b03209`, `4e610f1`; itest app/mongostore ok; e2e bước 30 |
| Đúng khi lệch thứ tự, A → B → A, hai node (summary §5) | `Put` CAS, thứ tự bước, hỏi lại sau retire | T3 | Unit attach (hai node, retire tới muộn), storetest | `4e610f1`; attach ok |
| Ký URL public: chỉ cần đăng nhập cùng tenant (owner 2026-11-13) | `publicfiles`, grpcsrv | T4 | Unit, e2e 16 | `f0961cd`; e2e bước 29 |
| Lọc tenant từ token, tenant khác không thấy gì (CLAUDE.md, IDOR) | `publicfiles`, `GetMany` | T4 | Unit tenant khác, explain `GetMany` sẵn có | `f0961cd`; unit `publicfiles` ok |
| Route gateway | `pkg/chatrpc` | T4 | Unit đếm lệnh, e2e 16 | `f0961cd`; chatrpc ok; e2e bước 29 qua gateway |
| Docs, `INDEXES.csv`, dòng roadmap | `docs/features/01`, `09`, `12`, `docs/apps/gateway.md`, `chat.md`, `events.md`, `architecture.md`, `e2e.md`, `roadmap.md` | T5 | `make fmt-check`, đọc lại | `0111d37`, `7088d2e` |
