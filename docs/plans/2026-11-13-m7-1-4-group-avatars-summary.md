# M7.1 phase 4: ảnh nhóm — tóm tắt

> Ngày: 2026-11-13 · Nhánh: `feat/m7-1` · Trạng thái: owner duyệt 2026-11-14, chưa xây
> Nguồn yêu cầu: web cần ảnh nhóm do admin đặt (owner chuyển 2026-11-08); owner 2026-11-10: không có ảnh hiện lâu dài, cần hình nào ký hình đó, chat không gọi media; owner 2026-11-13: file media có hai loại, **public** (đăng nhập là xem được) và **private** (kiểm quyền như nay); tin nhắn chỉ nhận file private, ảnh nhóm chỉ nhận file public; mỗi lệnh lấy URL chỉ ký đúng một loại. Dòng M7.1 trong `docs/roadmap.md` ("`GetRoomAvatarURLs` … chỉ cho người còn ở nhóm") sửa theo quyết định này lúc đóng phase.

## Scope và ranh giới

| Việc | Trong phase 4? | Thuộc về |
|---|---|---|
| Upload file public: `StartUpload{public: true}` cấp khoá có dấu public | Có | module `media` |
| `ListUploads` ("File của tôi") chỉ trả file private | Có | module `media` (gallery) |
| `Room.avatars` (khoá file public) thay `Room.avatar` (chuỗi); `UpdateRoom` đặt/xoá ảnh, kiểm khoá trong RAM | Có | chat (room) |
| Tin nhắn từ chối khoá public | Có (chỉ thêm test, luật sẵn có đã chặn) | chat (message) |
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
| `media_room_avatars` (mỗi room một doc) | Tập theo event của module (khuôn `media_message_files`, D115) | Consumer `media` nghe `room_updated`, đọc room hiện tại qua cổng chat, ghi khi `info_ver` mới hơn | JetStream giao lại; sweeper/resync phát lại `room_updated` |
| `GetPublicURLs` | Chỉ đọc | — | — |

## 1. Thuật ngữ

| Từ | Nghĩa |
|---|---|
| Khoá private | `{tenant}/{user}/{yyyymmdd}/{26 ký tự}` (như nay); dùng trong tin nhắn; xem được khi đọc được tin |
| Khoá public | `{tenant}/{user}/pub/{yyyymmdd}/{26 ký tự}`; dùng làm ảnh nhóm; ai đăng nhập cùng tenant và biết khoá là xin được URL |
| `info_ver` | Version thông tin room, tăng mỗi lần `UpdateRoom` đổi gì đó (D133) |
| `retired_at` | Dấu "không còn ai dùng file" trên `media_uploads` (M7 P3); chỉ là dấu, chưa xoá, URL vẫn ký được |

## 2. Bức tranh chung

Ví dụ: An (owner nhóm "Kho HCM") chọn ảnh `kho.png`:

1. Web gọi `StartUpload{…, public: true}` → khoá `acme/an/pub/20261113/0cr1…k9`, upload như file thường.
2. `UpdateRoom{room_id, avatars: {keys: ["acme/an/pub/20261113/0cr1…k9"]}}` → chat kiểm khoá trong RAM (dạng public, tenant `acme`, người upload là An, upload trong 24 giờ), ghi room `info_ver` 3 → 4, phát `room_updated`.
3. Consumer `media` nhận `room_updated`, hỏi chat room hiện tại (`avatars`, `info_ver 4`), doc `media_room_avatars` của room đang ở `info_ver 3` → ghi `{file_key: …k9, info_ver: 4}`, chuyển `kho.png` sang bucket `completed`; ảnh cũ (nếu có) không còn nhóm nào dùng → `retired_at`.
4. Bình mở danh sách hội thoại: `ListMyRooms` trả `avatars` của 20 nhóm; web gom các khoá vào một `GetPublicURLs{keys: [...]}` → nhận URL ký (1 giờ). Nhóm không có ảnh dùng `avatar_users` (P3).
5. An thử gửi `kho.png` (khoá public) trong tin → `INVALID_ARGUMENT`. An thử đặt `hopdong.pdf` (khoá private) làm ảnh nhóm → `INVALID_ARGUMENT`.

```
web ─StartUpload{public}─▶ media: khoá …/pub/…
web ─UpdateRoom─▶ gateway ─▶ chat: kiểm khoá public trong RAM, CAS info_ver, room_updated ─▶ JetStream
                      module media (consumer media) ◀──────── room.*.room_updated ──────────┘
                       hỏi chat room hiện tại → media_room_avatars (CAS info_ver) → move completed / retire cũ
web ─GetPublicURLs─▶ gateway ─▶ media: khoá public + tenant từ token → media_uploads → ký URL (không hỏi chat)
```

## 3. Dữ liệu

| Field | Nghĩa | Ví dụ | Dùng để |
|---|---|---|---|
| `StartUploadRequest.public` (bool, field mới) | `true` = cấp khoá public; chỉ nhận `image/*` | `true` | Upload ảnh nhóm |
| `rooms.avatars` | 0–1 khoá public; thay field `avatar` | `["acme/an/pub/20261113/0cr1…k9"]` | Client biết nhóm có ảnh và khoá nào |
| `Room.avatars` (proto, số field mới); `Room.avatar` = `reserved 8` | như trên, trong `GetRooms`, `ListMyRooms`, `UpdateRoom`, event | | |
| `UpdateRoomRequest.avatars` (`optional RoomAvatars {repeated string keys}`); `avatar` = `reserved 3` | Không gửi = giữ nguyên; `keys: []` = xoá ảnh; 1 khoá = đặt ảnh | | Phân biệt "không đổi" và "xoá" |
| `media_room_avatars` (clustered, `_id` = room id) | `tenant_id`, `file_key` (rỗng = không có ảnh), `info_ver`, `updated_at`; index `{file_key}` | `{_id: "903…147", file_key: "…k9", info_ver: 4}` | Biết ảnh cũ để retire; biết khoá còn nhóm khác dùng |
| `GetPublicURLsRequest.keys` | 1–100 khoá | 20 khoá | |
| `GetPublicURLsResponse` | `files: [FileURL{key, url, thumbnail_url}]` (dùng lại message sẵn có), `expires_at` | | Khoá private, tenant khác, chưa upload xong, không có: không có trong kết quả |

## 4. Luồng

**`StartUpload` (media):** thêm `public`; `public` mà `content_type` không phải `image/*` → `INVALID_ARGUMENT`. Khoá public sinh cùng cách, thêm đoạn `pub`. `GetPartURLs`, `CompleteUpload`, `AbortUpload` nhận cả hai loại khoá (file của chính người gọi).

**`ListUploads` ("File của tôi", media):** chỉ trả file private, tức file gửi được vào tin (owner 2026-11-14). Ví dụ: An upload ảnh nhóm và 3 ảnh chụp; "File của tôi" chỉ hiện 3 ảnh chụp, mới nhất trước như nay. Cách làm: khoá private `acme/an/2026…` luôn xếp trước khoá public `acme/an/pub/…`, nên trang đầu đọc `_id < "acme/an/pub/"` trên index sẵn có, không thêm index, không lọc sau khi đọc (trang không bị ngắn). `before` là khoá public → `INVALID_ARGUMENT`.

**`UpdateRoom` (chat):** như nay, thêm: nếu có `avatars` và khác giá trị hiện tại thì mỗi khoá phải là khoá public, đúng tenant, người gọi là người upload, tạo trong 24 giờ (luật thời gian của đính kèm, D266) → sai thì `INVALID_ARGUMENT`; quá 1 khoá → `INVALID_ARGUMENT`. Quyền như nay (owner/admin; DM → `FAILED_PRECONDITION`). Không gọi media.

**`SendMessage`/forward (chat):** luật khoá đính kèm hiện tại chỉ nhận đúng 4 đoạn, nên khoá public (5 đoạn) đã bị từ chối; phase này thêm test ghim điều đó.

**Consumer `media` (`room_updated`):**
1. Durable `media` thêm subject `evt.*.room.*.room_updated`; giải mã nhánh riêng (event room không có seq), chuỗi xử lý theo room.
2. Hỏi chat room hiện tại (`avatars`, `info_ver`, tenant) — đọc thẳng DB, không cache. Room không có hoặc khác tenant → bỏ.
3. Doc `media_room_avatars` có `info_ver` ≥ room hiện tại → ack (event cũ hoặc đã làm). Room chưa từng có ảnh và nay cũng không có (vd. đổi tên) → ack, không ghi.
4. Room đang có ảnh (mỗi event, kể cả chỉ đổi tên): đọc `media_uploads` (tenant). Đang upload → Nak (chờ, như tin có file); huỷ, hết hạn, không có → thôi; đã upload → gỡ `retired_at`, chuyển sang `completed` nếu chưa (cùng hàm của M7 P3, kèm thumb). Làm trước khi ghi doc để giao lại thì làm lại được; làm mỗi event để file hoàn tất muộn vẫn được chuyển ở lần đổi room sau.
5. Ghi doc CAS `{_id, info_ver: cũ}` (thay cả doc); trượt → `ErrRetryLater`, Nak.
6. Khoá cũ khác khoá mới: không còn doc nào trỏ khoá đó (`media_room_avatars` theo `file_key`, tính cả chính room vừa ghi) → `retired_at`. Làm sau khi ghi doc: hỏng giữa chừng chỉ thiếu dấu, không đánh dấu nhầm. Không cần hỏi cặp tin–file vì file public không nằm trong tin nào.

**`GetPublicURLs` (media):** chưa sẵn sàng → `UNAVAILABLE`; 0 hoặc > 100 khoá, khoá sai dạng → `INVALID_ARGUMENT`; khoá private hoặc tenant khác token → bỏ qua, không truy vấn; một `media_uploads.GetMany` lọc `tenant_id` từ token, đã upload xong → ký URL file + thumb từ bucket của file (hạn `MEDIA_DOWNLOAD_URL_TTL`). Không hỏi chat, không xét thành viên.

**Lệnh private sẵn có** (`GetAttachmentURLs`, `ListRoomFiles`) không đổi: chúng đi qua tin nhắn, mà tin nhắn chỉ có khoá private, nên không bao giờ trả file public.

## 5. Đúng đắn và chạy đua

- **Đổi ảnh hai lần nhanh** (A → B): event `info_ver 4` và `5` tới lệch thứ tự hay gộp: consumer luôn đọc room hiện tại, chỉ ghi khi `info_ver` mới hơn doc → kết quả là B, A được xét retire.
- **A → B → A:** doc giữ một khoá mỗi room theo `info_ver`. Hai node: node 1 ghi B rồi định retire A, trong lúc đó node 2 đã ghi lại A; câu hỏi "còn doc nào trỏ A" tính cả chính room nên thấy A và không retire. Nếu dấu vẫn lọt vào giữa lúc hỏi và lúc ghi dấu, bước 4 của event sau gỡ dấu.
- **Hai node cùng xử lý một room:** CAS theo `info_ver` của doc; bên thua Nak rồi đọc lại.
- **Một ảnh làm ảnh hai nhóm** (An đặt cùng khoá cho hai nhóm): retire chỉ khi không còn doc nào trỏ khoá đó. Hai nhóm cùng đổi đúng lúc có thể để dấu `retired_at` trên file vẫn dùng; dấu này chỉ là dấu (URL vẫn ký được, chưa có chính sách xoá), event sau của nhóm đó gỡ dấu.
- **Upload xong muộn** (phiên upload kéo quá 24 giờ, consumer đã bỏ qua): file nằm ở `processing`; lần đổi room kế tiếp chuyển nó sang `completed`.
- **Khoá giả hoặc của người khác:** chat chặn ở `UpdateRoom` (dạng, tenant, người upload); `GetPublicURLs` chỉ ký khoá public của tenant trong token.
- Không thêm chỗ xếp hàng: consumer `media` giữ cách kéo (giới hạn đang chạy, chuỗi theo khoá); event room xếp theo room.

## 6. Event

Không event mới. `room_updated` mang `Room` có `avatars`. Durable `media` thêm subject `evt.*.room.*.room_updated` (`docs/events.md`); đổi filter chỉ áp event mới (deliver new), room đã có ảnh trước đó (chỉ dev) phát lại bằng `/app resync`.

## 7. Thư viện và hạ tầng

Không thêm thư viện. Collection mới `media_room_avatars` + index `{file_key: 1}` tạo qua bootstrap/`/app indexes` (D191). Bucket S3 không đổi: khoá public nằm cùng `processing`/`completed`, luật 30 ngày và Lambda thumb áp như file thường. `make proto`, `buf-lint`.

## 8. Quyết định và phương án bị loại

| Quyết định | Lý do | Phương án bị loại |
|---|---|---|
| File media có hai loại public/private; tin nhắn chỉ nhận private, ảnh nhóm chỉ nhận public; mỗi lệnh lấy URL chỉ ký một loại (owner 2026-11-13; ứng viên decision log) | Ảnh nhóm không cần kiểm thành viên nên lệnh lấy URL không hỏi chat; tách hẳn để người bị kick không mở được file trong tin cũ | Một loại, kiểm thành viên nhóm (`GetRoomAvatarURLs` theo room): cần cổng chat và truy vấn `members` mỗi lần. Cho tin nhắn nhận file public: lộ file trong tin cho người đã rời nhóm |
| Loại file nằm trong khoá (`…/pub/…`), không chỉ là field của doc upload | Chat đọc được khoá mà không gọi media nên kiểm được trong RAM; khoá đã cấp không đổi loại, file đã gửi trong tin không thể thành public | Field `public` trong `media_uploads`: chat phải hỏi media mới biết |
| `avatars` (khoá file public) thay `avatar` (chuỗi tự do) (ứng viên decision log, gộp với `avatar_users` P3 thành "hợp đồng ảnh nhóm") | Owner 2026-11-10: không có ảnh hiện lâu dài; hai nguồn ảnh làm client rối; chưa có dữ liệu production | Giữ cả hai field. Link công khai cố định không cần đăng nhập: owner không chọn |
| Chat kiểm khoá trong RAM, không gọi media | Một chiều chat → media (D182, D262); `UpdateRoom` không chậm vì media | Chat hỏi media "file đã upload chưa" |
| Media giữ một doc mỗi room, CAS theo `info_ver` | Biết ảnh cũ để retire; đúng khi event lệch thứ tự, gộp, A → B → A | Cặp room–file theo từng file: sai khi A → B → A và khi hai node cùng xử lý (review design 2026-11-10) |

## 9. Chi phí và tải

`UpdateRoom`, `SendMessage`: không thêm I/O. Consumer `media`: mỗi `room_updated` một đọc room, một đọc doc ảnh; nhóm có ảnh thêm một đọc upload và một ghi doc; khi đổi ảnh thêm một copy S3 (ảnh nhỏ, một lần), một tìm theo `file_key` cho retire. Nhóm chưa từng có ảnh đổi tên: không ghi gì. `GetPublicURLs` 100 khoá: một `GetMany` ≤ 100 khoá trên `media_uploads`, ≤ 200 chữ ký trong RAM, không đọc chat.

## 10. Rủi ro và giới hạn

- Ai đăng nhập cùng tenant và còn giữ khoá ảnh nhóm (vd. người đã rời nhóm) vẫn xin được URL ảnh đó, kể cả sau khi admin gỡ ảnh, tới khi có chính sách xoá file (owner chấp nhận 2026-11-13). Người chưa từng thấy khoá thì không đoán được (80 bit ngẫu nhiên).
- Ảnh vừa đặt có thể ký từ bucket `processing` vài giây trước khi consumer chuyển; vẫn tải được.
- Ảnh đặt bằng khoá chưa upload xong rồi bị huỷ: `avatars` có khoá nhưng không có URL; client coi như không có ảnh (spec module 09).
- Đổi hợp đồng: `Room.avatar` và `UpdateRoomRequest.avatar` bỏ (reserved); client web cập nhật cùng bản (chưa có production).

## 11. Điểm đội tự chọn (owner phản biện)

- `avatars` nhận 0–1 khoá; dạng danh sách để sau thêm biến thể (nhiều cỡ) mà không đổi hợp đồng.
- Khoá public dạng `{tenant}/{user}/pub/{yyyymmdd}/{26 ký tự}`: giữ tiền tố `{tenant}/{user}/` để kiểm người upload như nay.
- File public chỉ nhận `image/*`: dùng duy nhất cho ảnh nhóm, nên không cần kiểm loại file ở consumer hay lúc ký.
- `GetPublicURLs` tối đa 100 khoá (bằng `GetAttachmentURLs`); gateway dùng giới hạn theo user chung của app.

## 12. Kiểm thử (dự kiến)

| Test | Chứng minh |
|---|---|
| Unit `media/domain`, `upload`: khoá public đúng dạng, người upload sở hữu được; `public` + không phải ảnh → `INVALID_ARGUMENT`; `CompleteUpload`/`AbortUpload` nhận khoá public | Upload public |
| Unit `gallery`: ảnh public của chính mình không có trong trang đầu lẫn trang sau; `before` là khoá public → `INVALID_ARGUMENT`; unit `media/domain`: mọi khoá private xếp trước mốc `{tenant}/{user}/pub/`, mọi khoá public từ mốc trở đi | "File của tôi" |
| Unit chat `domain`/`room`: khoá public hợp lệ đặt được; khoá private, của người khác, tenant khác, quá 24 giờ, sai dạng, 2 khoá → `INVALID_ARGUMENT`; `keys: []` xoá; không gửi giữ nguyên; cùng khoá với hiện tại không kiểm tuổi, không đổi `info_ver` | `UpdateRoom` |
| Unit chat `domain`: khoá public trong `SendMessage` và forward → `INVALID_ARGUMENT` | Tách hẳn |
| Storetest chat: `avatars` ghi/xoá qua CAS `info_ver` | Store chat |
| Unit consumer media: giải mã `room_updated` (không seq), event khác vẫn như cũ; A → B → A một node và hai node xen kẽ (A không bị retire); event cũ bị bỏ; nhóm không ảnh đổi tên không ghi; đổi tên nhóm có ảnh không copy lại; upload xong muộn được chuyển ở event sau; đang upload → Nak; ảnh cũ retire chỉ khi không còn nhóm dùng; CAS trượt → Nak, không retire | Luồng §4, §5 |
| Storetest media `media_room_avatars` (mem + mongo): CAS `info_ver`, đếm theo `file_key`, tenant khác không thấy | Store media |
| Unit `GetPublicURLs`: khoá private không có URL, tenant khác không có URL và không truy vấn, 101 khoá → `INVALID_ARGUMENT`, chưa upload xong bỏ qua, ký từ bucket của file; itest `explain` của `GetMany` | Quyền, IDOR |
| e2e 16: A upload PNG public, đặt làm ảnh nhóm qua gateway; B `GetPublicURLs` tải đúng byte và SHA-256; khoá public trong tin → `INVALID_ARGUMENT`; khoá private làm ảnh nhóm → `INVALID_ARGUMENT`; `GetPublicURLs` với khoá private → không có URL; `ListUploads` của A không có khoá public; A xoá ảnh → room không còn `avatars` | Qua gateway thật |

## Bảng phủ

| Yêu cầu / quyết định | Thành phần | Task | Test | Bằng chứng |
|---|---|---|---|---|
| File public/private, loại nằm trong khoá (owner 2026-11-13) | proto `StartUpload`, `media/domain`, `upload` | T1 | Unit media domain/upload, e2e 16 | |
| "File của tôi" chỉ file private (owner 2026-11-14) | `gallery`, `media/domain` (mốc khoá public) | T1 | Unit domain (mọi khoá private xếp trước mốc), unit `gallery`, e2e 16 | |
| Admin đặt ảnh nhóm (roadmap M7.1 P4) | proto, chat `domain`, `room.UpdateRoom`, store chat | T2 | Unit `room`, storetest, e2e 16 | |
| `avatars` thay `avatar` (roadmap; design 2026-11-10) | proto reserved, codec, `pbconv`, chatcli, ws field pin | T2 | Unit `pbconv`, field pin, e2e đọc room | |
| Tin nhắn chỉ private, ảnh nhóm chỉ public (owner 2026-11-13) | chat `domain` | T2 | Unit `SendMessage`/forward/`UpdateRoom`, e2e 16 | |
| Chat không gọi media (roadmap) | kiểm khoá trong RAM | T2 | Unit `room` (không cổng media) | |
| Giữ file ảnh nhóm, retire ảnh cũ (design 2026-11-10) | consumer media, `media_room_avatars`, cổng chat đọc room | T3 | Unit consumer, storetest media, itest | |
| Ký URL public: chỉ cần đăng nhập cùng tenant (owner 2026-11-13) | `GetPublicURLs` | T4 | Unit, itest `explain`, e2e 16; reviewer IDOR MongoDB ở bước plan | |
| Lọc tenant từ token, tenant khác không thấy gì (CLAUDE.md, IDOR) | `GetPublicURLs`, `GetMany` | T4 | Unit + storetest tenant khác | |
| Route gateway | `pkg/chatrpc` | T4 | Unit đếm lệnh, e2e 16 | |
| Docs, `INDEXES.csv`, dòng roadmap | `docs/features/01`, `09`, `12`, `docs/apps/gateway.md`, `events.md`, `architecture.md`, `apps/chat.md`, `e2e.md`, `roadmap.md` | T5 | `make fmt-check`, đọc lại | |
