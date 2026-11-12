# M7.1 phase 3: id 4 thành viên đầu của nhóm — tóm tắt

> Ngày: 2026-11-12 · Nhánh: `feat/m7-1` · Trạng thái: `dev-done` (owner duyệt 2026-11-12, viết lại theo bản đã xây)
> Nguồn yêu cầu: web cần ảnh ghép của nhóm chưa có ảnh trong danh sách hội thoại mà không gọi `ListMembers` từng nhóm (owner chuyển 2026-11-08); owner 2026-11-10: id 4 user có sẵn trong danh sách hội thoại, đọc theo index, không sắp xếp, không realtime; owner 2026-11-12: không sắp xếp nên không ràng buộc thứ tự, không đòi admin hay owner lên đầu; phase owner duyệt 2026-11-10 (`docs/roadmap.md`, dòng M7.1).

## Scope và ranh giới

| Việc | Trong phase 3? | Thuộc về |
|---|---|---|
| Field `avatar_users` (tối đa 4 user id thành viên bất kỳ còn trong nhóm, không thứ tự) trên doc `rooms` của nhóm, trả trong `Room` của `GetRooms`, `ListMyRooms` | Có | chat (room) |
| Handler thứ ba `avatar_users` của consumer `projections`: điền khi thêm người lúc danh sách chưa đủ 4, đọc lại khi có người bị xoá hoặc rời | Có | chat (event/projection) |
| Phản ứng với đổi role, đổi priority | Không cần: không có thứ tự nên danh sách vẫn đúng | — |
| Docs, e2e | Có | `docs/features/01`, `09` (spec client), `docs/events.md`, `architecture.md`, `docs/e2e.md`, `INDEXES.csv` |
| Ghép ảnh, lấy tên/ảnh từng user (`GetUsers` của identity), cache | Không | client |
| DM | Không đổi | client dùng `direct_users` sẵn có |
| Đẩy thay đổi realtime | Không (owner 2026-11-10) | client thấy ở lần tải room kế tiếp |
| Ảnh nhóm admin upload, `avatars` thay `avatar` | Không | phase 4 |

## Tính năng → lớp dữ liệu (architecture §4)

| Tính năng | Lớp | Ghi | Phục hồi |
|---|---|---|---|
| `rooms.avatar_users` | State có version (Projection), như `rooms.last_seq` của `room_activity` | Consumer `projections` đọc 4 member còn trong nhóm qua index, `$set` có điều kiện theo bộ đếm `avatar_ver` (tăng mỗi lần ghi); không bit `_state`, không event, không tăng `info_ver` | JetStream giao lại (Nak theo bậc lùi); sweeper phát lại `member_*` còn cờ; `/app resync -room ID` bật lại cờ member → event phát lại → tính lại |

## 1. Thuật ngữ

| Từ | Nghĩa |
|---|---|
| `avatar_users` | Id tối đa 4 thành viên bất kỳ còn trong nhóm, không thứ tự, client dùng để ghép ảnh nhóm |
| `projections` | Consumer JetStream của chat (chỉ node giữ slot 0, lô tới 8192 event mỗi 3s) đang chạy `room_activity` và `reply_index` |

## 2. Bức tranh chung

Ví dụ: An (owner) tạo nhóm "Kho HCM" với Bình, Chi, Dũng, Em, Phương. Chat ghi 6 doc `members` và phát 6 `member_added`. Consumer `projections` gom cả 6 trong một lô, thấy một room chưa có ai trong `avatar_users`, đọc 4 thành viên còn trong nhóm qua index (Mongo trả 4 người nào thì lấy 4 người đó, ví dụ Bình, Chi, Dũng, Em); ghi:

```
rooms { _id: 9036108543124204147, type: "group", name: "Kho HCM", member_count: 6,
        avatar_users: ["u_binh", "u_chi", "u_dung", "u_em"] }
```

Web gọi `ListMyRooms` → mỗi nhóm có `avatar_users`; gom id của 20 nhóm (≤ 80 id) vào một `GetUsers` (≤ 100 id, P1) để lấy tên và ảnh. An thêm Giang → danh sách đã đủ 4 → không đọc, không ghi, ảnh ghép giữ nguyên. An kick Chi → `member_removed` → handler đọc lại 4 người còn trong nhóm, ví dụ Bình, Dũng, Em, Phương → ghi. An đổi Phương thành admin → handler không nghe event này, không đổi gì.

```
lệnh member ──▶ members (+ event member_*) ──▶ JetStream ──▶ projections (slot 0)
                                                          ├─ room_activity (sẵn có)
                                                          ├─ reply_index (sẵn có)
                                                          └─ avatar_users (mới) ──▶ rooms.avatar_users
GetRooms / ListMyRooms ──▶ cache room (≤ 60s) ──▶ rooms ──▶ Room.avatar_users
```

## 3. Dữ liệu

| Field | Nghĩa | Ví dụ | Dùng để |
|---|---|---|---|
| `rooms.avatar_users` | 0–4 user id thành viên còn trong nhóm, không thứ tự; nhóm 3 người có 3 id; DM không có | `["u_binh","u_chi","u_dung","u_em"]` | Ghép ảnh nhóm chưa có ảnh |
| `rooms.avatar_ver` | Bộ đếm nội bộ, tăng mỗi lần ghi `avatar_users`; không lộ qua API | `3` | Ghi có điều kiện |
| `Room.avatar_users` (proto, `repeated string`, số field mới) | Như trên, có trong `GetRooms`, `ListMyRooms` và trong `Room` của event `room_created`/`room_updated` | như trên | như trên |

Truy vấn đọc: `members` `{room_id, state: 1}`, `limit 4`, không sort, chỉ lấy `user_id`, đi qua index `{room_id, state, …}` sẵn có (không index mới, không đọc doc).

## 4. Luồng

**Ghi (handler `avatar_users`, mỗi lô):**
1. Giữ event `member_added`, `member_removed` của nhóm (loại room có sẵn trong event, DM bỏ ngay); gom theo room.
2. Đọc doc `rooms` của các room trong lô theo khúc ≤ 1.000 room (trần của lệnh đọc room; quá trần thì lỗi bị coi là event hỏng và mất); room không có → bỏ, ack.
3. Room chỉ có `member_added` và đã đủ 4 id → không làm gì. Còn lại: một truy vấn 4 thành viên còn trong nhóm. Chỉ có `member_added` và cùng tập người với giá trị cũ → không ghi. Có `member_removed` hoặc khác tập → `updateOne {_id: room, avatar_ver: <đã đọc>}` `$set avatar_users`, `$inc avatar_ver` (có `member_removed` thì ghi cả khi cùng tập, xem §5).
4. Không khớp (node khác vừa ghi) → `ErrRetryLater` cho các event của room đó → Nak; các room khác vẫn ack. Mongo lỗi → Nak như vậy.

**Đọc:** `GetRooms`/`ListMyRooms` không đổi đường đọc; field nằm trong doc `rooms` nên đi cùng thông tin room (cache room tới 60s).

## 5. Đúng đắn và chạy đua

- **Đọc lại từ trạng thái hiện tại, không cộng dồn:** event tới lệch thứ tự, giao hai lần hay sweeper phát bù chỉ gây thêm một lần đọc; người đã rời không bao giờ được ghi vào vì truy vấn chỉ lấy `state: 1`.
- **Bỏ qua khi đủ 4 là an toàn:** danh sách chỉ sai khi một người trong đó rời, và lúc đó luôn có `member_removed` (phát sau khi ghi `members`, D141) làm handler đọc lại.
- **Hai node cùng ghi** (lúc đổi chủ slot 0, review 2026-11-12): node cũ đọc danh sách [An…], lấy mẫu có X rồi chưa ghi; X rời, node mới xử lý `member_removed` và **luôn ghi** (tăng `avatar_ver`) dù ra cùng tập; node cũ ghi theo bộ đếm cũ nên trượt, Nak, đọc lại. Nếu chỉ so mảng cũ thì node cũ ghi đè thành công và X nằm lại trong danh sách.
- **Lô 500 người của `AddMembers`:** gom theo room nên tính một lần, không 500 lần.
- **Thêm người khi đã đủ 4:** không đọc, không ghi; ảnh ghép ổn định.
- **Event phát trước khi doc member đổi xong:** không có, event phát sau khi ghi `members` (D141).
- Không thêm chỗ xếp hàng mới: handler chạy song song với hai handler kia trong cùng lô; không đổi cách kéo của consumer (lô, cửa sổ, giới hạn đang chạy giữ nguyên).

## 6. Event

Không event mới, không subject mới, không đổi consumer. `avatar_users` đổi không phát gì. `room_created`/`room_updated` mang `Room` từ doc lúc phát, nên `avatar_users` trong event có thể cũ hơn hoặc mới hơn bản client đã đọc; field không có version riêng, client không dùng event để thay giá trị đã đọc (spec module 09).

## 7. Thư viện và hạ tầng

Không thêm thư viện, không index mới. `make proto`, `buf-lint`.

## 8. Quyết định và phương án bị loại

| Quyết định | Lý do | Phương án bị loại |
|---|---|---|
| 4 thành viên bất kỳ còn trong nhóm, không thứ tự, một truy vấn `limit 4` không sort (ứng viên decision log lúc đóng: field room mới cho client) | Owner 2026-11-10 và 2026-11-12: không sắp xếp, không ràng buộc thứ tự | Thứ tự `ListMembers` (owner trước): tới 3 truy vấn, owner không chọn. Thứ tự index (admin trước): phải nghe thêm đổi role/priority và ghi lại, owner không cần. Client tự gọi member từng nhóm: N+1 |
| Projection trong consumer `projections`, không phải ghi trong lệnh member | Lệnh member không chậm thêm; một lô 500 người tính một lần | Ghi trong lệnh `AddMembers`/`RemoveMember`: thêm một truy vấn và một ghi vào mọi lệnh, hai lệnh song song cùng room giành nhau |
| Không realtime, không tăng `info_ver` | Owner 2026-11-10 | Phát `room_updated`: mỗi lần thêm/kick thành hai event, client không cần |
| Sửa dữ liệu cũ bằng `/app resync -room ID` sẵn có | Resync bật lại cờ member → event phát lại → handler tính lại; không thêm lệnh | Thêm vào `/app recount` (bản design): thêm code cho việc resync đã làm được |
| Handler riêng `avatar_users`, không gộp vào `room_activity` | Code và test tách bạch; lỗi một room chỉ Nak event của room đó. Event member cũng do `room_activity` giữ nên khi Nak thì `room_activity` chạy lại, vô hại vì ghi `$max` | Gộp: một hàm làm hai việc khác nhau |
| Ghi có điều kiện theo bộ đếm `avatar_ver`, không theo mảng cũ | Chặn node dẫn cũ ghi lại người đã rời (§5) | So mảng cũ: ghi "cùng giá trị" không làm node cũ trượt |

## 9. Chi phí và tải

Mỗi lô có event thêm/xoá member: 1 đọc `rooms` cho mọi room trong lô; room đã đủ 4 mà chỉ có thêm người thì dừng ở đó; room còn lại 1 truy vấn index trả ≤ 4 id, tối đa 1 ghi khi tập người đổi. Lệnh member hiếm so với tin (2K tin/s, member vài lần/phút); lệnh gửi tin không đổi. Doc `rooms` thêm ≤ 4 id (≤ 260 byte). `GetRooms`/`ListMyRooms` không thêm truy vấn.

## 10. Rủi ro và giới hạn

- 4 người bất kỳ: không bảo đảm có owner hay admin (owner 2026-11-12).
- Trễ: vài giây tới khi consumer xử lý, cộng cache room tới 60s; người vừa bị kick còn trong ảnh ghép khoảng một phút.
- Nhóm tạo trước P3 (chỉ dev) có `avatar_users` rỗng tới lần thêm/xoá thành viên kế tiếp hoặc `/app resync -room ID -from <trước lúc tạo nhóm> -to <bây giờ>` (phát lại `member_added` và `member_removed` trong khoảng; thành viên có thay đổi cuối là đổi role/priority không được phát lại thành thêm/xoá, vô hại vì danh sách chỉ sai khi có người rời).
- Người bị khoá ở `identity` vẫn là thành viên với chat nên vẫn có thể nằm trong danh sách; client thấy trạng thái qua `GetUsers`.

## 11. Điểm đội tự chọn (owner phản biện)

- Tên field `avatar_users` (theo design); số 4 là hằng trong code (owner 2026-11-10), không thành biến env.
- Room không có hoặc là DM: bỏ event, không lỗi.
- Ghi field cả khi rỗng (nhóm còn 0 người hoạt động) để điều kiện so giá trị cũ luôn rõ.

## 12. Kiểm thử (như đã chạy)

| Test | Chứng minh |
|---|---|
| Unit handler (store giả): nhóm < 4 → thêm người được ghi; đủ 4 rồi thêm người → không đọc member, không ghi; kick người trong danh sách → có người thay; kick người ngoài → vẫn ghi, `avatar_ver` tăng; đổi role/priority và DM không được giữ; room không có bị bỏ; ghi trượt → `ErrRetryLater` chỉ cho event của room đó; 1001 room trong lô → đọc hai khúc, không event nào mất; 500 `member_added` một room → một lần đọc; giao lại cho cùng kết quả | Luồng §4, §5 |
| Storetest mem + mongo: tối đa 4 user id còn trong nhóm (so như tập), bỏ người đã rời, nhóm 2 người → 2 id; ghi có điều kiện theo `avatar_ver` (lần đầu, khớp, trượt, room không có) | Hợp đồng store |
| Itest mongostore `explain`: truy vấn 4 member là `IXSCAN` trên index `members` sẵn có, không `COLLSCAN`, không đọc doc | Đúng index |
| Itest projection (NATS thật, store bộ nhớ): thêm 5 người → doc room có 4 id còn trong nhóm; kick một người trong 4 → 4 id, không có người bị kick. Đường Mongo do storetest Mongo và e2e kiểm | Đường event thật |
| Unit `pbconv`/`grpcsrv`: `Room.avatar_users` có trong `GetRooms`, `ListMyRooms`; DM rỗng | Field tới client |
| e2e phase 10: nhóm A, B, C qua gateway có đủ 3 id (chờ 4s rồi hỏi lại tới 75s = cache room 60s + 15s); thêm D, E → 4 id; kick một trong C, D, E đang có trong danh sách → đúng 4 người còn lại trong 75s | Qua gateway thật |

## Bảng phủ

| Yêu cầu / quyết định | Thành phần | Task | Test | Bằng chứng |
|---|---|---|---|---|
| Id 4 member đầu có sẵn trong danh sách hội thoại (owner 2026-11-10) | proto `Room`, `pbconv`, doc `rooms` | T1, T3 | Unit `pbconv`/`grpcsrv`, e2e | Unit `pbconv` `TestRoomGivesAvatarUsersOnlyForGroups`, codec round trip ok; e2e phase 10 bước 4 PASS |
| Một truy vấn theo index, không sắp xếp, không ràng buộc thứ tự (owner 2026-11-10, 2026-11-12) | store 4 member | T1 | Storetest (so như tập), itest `explain` | Storetest mem + Mongo ok; itest `explain`: `LIMIT → PROJECTION_COVERED → IXSCAN` trên index members, 0 doc đọc |
| Cập nhật khi thêm, kick, rời (roadmap M7.1 P3; đổi role/priority không cần, owner 2026-11-12) | handler `avatar_users` | T2 | Unit handler, itest projection, e2e | Unit handler ok; itest `TestAvatarUsersFollowRealMemberEvents` (NATS thật) ok; e2e phase 10 bước 7 PASS |
| Không realtime (owner 2026-11-10) | không event, không `info_ver` | T2 | Unit handler (không phát gì) | Handler chỉ gọi `SetAvatarUsers`, không có cổng phát event |
| Không retry nội bộ, ghi có điều kiện; hai node không ghi lại người đã rời (CLAUDE.md, review 2026-11-12) | handler, store `avatar_ver` | T1, T2 | Unit ghi trượt → Nak, có `member_removed` luôn ghi; storetest CAS | `TestALostWriteFailsOnlyThatRoomsEvents`, `TestARemovalAlwaysWritesAndBumpsTheVer` ok; storetest CAS ok trên Mongo |
| Lô lớn không mất event (review 2026-11-12) | handler đọc room theo khúc | T2 | Unit 1001 room | `TestABatchOverTheRoomReadCapIsReadInChunks` ok |
| Chỉ nhóm, DM dùng `direct_users` (design 2026-11-10) | handler | T2 | Unit DM bị bỏ | `TestAvatarUsersKeepsOnlyGroupMemberAddsAndRemovals`, `TestMissingRoomsAndDirectRoomsAreSkipped` ok |
| Docs, `INDEXES.csv` | `docs/features/01`, `09`, `events.md`, `architecture.md` §4/§8, `e2e.md` | T3 | `make fmt-check`, đọc lại | `make fmt-check` sạch |

## 13. Kết quả, lệch và mang sang

- Bằng chứng: `make fmt-check`, `vet`, `lint` (0 issues), `buf-lint` sạch; `make test` 117 package ok (lần đầu một test websocket `session` chập chờn, chạy lại ok; P3 không chạm websocket); `make itest` 117 package ok; `make app-up` và `make e2e` đầy đủ PASS (phase 10 bước 4 và 7 mới). Reviewer plan: 1 Critical, 3 Important, đã gộp trước khi code; reviewer code: không Critical, không Important. Commit `93dbb98` → `60e6441`.
- Lệch so với plan: không có lệch về hành vi. `domain.Room` có field mảng nên test so `Room` bằng `==` chuyển sang `reflect.DeepEqual` (8 file test); index members gom vào biến `membersRoomKeys` để dùng làm hint (cùng khoá, cùng tên index, `/app indexes` không thấy đổi).
- Minor (không sửa): e2e chờ tối đa 75s, có thể không đủ nếu consumer trễ hơn 15s lúc máy tải nặng; chưa có test riêng cho lỗi "event hỏng" từ `MemberSample`/`SetAvatarUsers` (không đường nào sinh ra lỗi đó).
- Mang sang: hai test websocket `session` chập chờn (`TestRevokedSessionSendsTheRunningConnectionBackToWaiting`, `TestExpiryStopsDeliveryUntilANewTokenGivesReady`) và `TestSingleChatNodeClaimsAllSlots` khi máy tải nặng, sửa ở nhánh `fix/`.
