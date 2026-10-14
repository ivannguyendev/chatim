# M3 — hai chủ đề cần thảo luận cuối milestone

> Ngày: 2026-10-15. Owner yêu cầu thảo luận trước khi làm (2026-10-14). Không có quyết định nào trong tài liệu này; mỗi chủ đề nêu vấn đề, ví dụ, số đo, các phương án và câu hỏi cần chốt.

## 1. Effect engine bị chặn đầu hàng

### 1.1 Vấn đề

Mỗi core có worker cho 32 partition của work stream. Một partition lấy một lô phiếu (tối đa 256), chạy các effect theo thứ tự đăng ký, và **chỉ lấy lô kế tiếp khi lô này xong**. Một số effect phải chờ `RECONCILE_DELAY` (5s) trước khi chạy, ví dụ `msg_created` (để fast path phát event trước, worker chỉ phát bù cái bị mất, D65). Trong 5s đó cả partition đứng yên, kể cả các effect không cần chờ.

Ví dụ: Lan gửi tin 45 vào nhóm 777 lúc 10:00:00. Phiếu của tin 45 rơi vào lô đang chờ `msg_created` của các tin trước. `room_activity` (nâng `rooms.last_seq`) chỉ chạy lúc ~10:00:05. Trong 5s đó:
- `ListMyRooms` có thể trả tin cuối và unread cũ cho room 777 (cache đuôi room kiểm bằng `last_seq`), và hai core có thể trả khác nhau;
- tab thread (`reply_threads`), số trả lời (`rc`), chỉ mục mention cũng trễ 5s.

Có từ M2b.1 (D79–D81); M3 làm lộ ra vì danh sách hội thoại dựa vào `last_seq`/`last_edit_at`. M3 chấp nhận trễ này (owner 2026-10-14); client đã áp event tại chỗ nên màn hình đúng.

### 1.2 Reviewer độc lập tìm thêm (2026-10-14)

| Mức | Vấn đề | Hậu quả |
|---|---|---|
| Critical | Chặn đầu hàng (như trên); trễ 5–6s thường, 10–20s khi PubAck chậm | Thông lượng ~51 phiếu/s mỗi partition, ~1,6K phiếu/s mỗi core; tải cao hơn thì **mọi** effect trễ ~5s cố định |
| Important | Phiếu lỗi được Nak 5s mãi, không giới hạn số lần giao, vẫn chiếm `MaxAckPending` 1.024 | ~1.024 phiếu lỗi vĩnh viễn làm nghẹn cả partition (32 slot); effect phát event phát lại cùng event mỗi 5s sau 5 phút |
| Important | `Fetch` chờ đủ lô tới 0,9s khi tải thấp | Thêm tới 0,9s trễ; test đơn vị không thấy (broker giả trả ngay) |
| Minor | `reconcile_lag_seconds` một gauge, không tách giai đoạn | Alert 120s không thấy trễ 5s; một phiếu lỗi cũ làm alert sai |
| Minor | Quyền slot chỉ kiểm trước khi lấy lô; message đã giao chưa đọc không được Nak khi dừng; effect không có hạn riêng; delay phụ thuộc đồng hồ | Làm thừa khi đổi chủ slot; phiếu chờ `AckWait` 35s; có thể log "shutdown step failed" |

### 1.3 Thử nghiệm ở M3 (không giữ)

Hai giai đoạn trong một goroutine mỗi partition: effect không chờ chạy ngay khi lấy lô; effect có delay chờ riêng trong hàng giữ lô, partition vẫn lấy lô tiếp (trần `WORK_MAX_INFLIGHT` 1.024, `MaxAckPending` 4.096); ack từng phiếu; Nak(0) mọi lô giữ khi mất slot/dừng; phiếu lỗi giãn dần bằng `NakWithDelay` (5s, 30s, 2m, 10m); fetch trả ngay khi có phiếu. Mọi test, itest, e2e xanh, nhưng bench:

| Ô | Trước | Sau |
|---|---|---|
| 1K tin/s: trễ effect | TB 5,7s, max 11,1s | TB 0,5s, max 1,5s |
| 1K tin/s: số lần ghi `rooms` | 12.170 | 60.869 (×5) |
| 5K tin/s: trễ effect không chờ | TB 4,8s, max 8,8s | TB 7,2s, max 17,5s |
| 5K tin/s: effect xong mỗi giây | 4.165 | 3.396 (−18%) |
| 5K tin/s: ack p99 | 835ms | 1.440ms |

(Ô 1K-sau máy bị hạ xung, `CPU_Speed_Limit` 37–44: chỉ tin số đếm Mongo.)

Nguyên nhân 5K tệ hơn: lấy phiếu ngay nên lô rất nhỏ; mất phần gom mà lô 256 phiếu trước đây vô tình làm. `room_activity` ghi một bulk write mỗi lô nhỏ (Mongo tải ×5, làm chậm cả đường gửi tin); giai đoạn có delay chạy từng lô nhỏ một (mỗi lô một pipeline Redis, một `Find`, một lần chờ PubAck), chiếm vòng lặp, effect không chờ bị đói.

Bản vá: `bin/m3-engine-two-stage.patch` (38 file, áp lại được). Số đo: `bin/bench/m3-engine-*`.

### 1.4 Phương án

| Phương án | Làm gì | Được | Mất |
|---|---|---|---|
| A. Hai giai đoạn + gộp lô + cửa sổ gom | Như thử nghiệm, thêm: gộp các lô đã tới hạn cùng kind/effect vào một lần chạy; giai đoạn nhanh gom phiếu ~50ms (`WORK_FETCH_LINGER`) | Trễ effect không chờ ≤ ~0,1s; giữ gom lô | Thêm ~50ms; phải bench lại 1K/5K |
| B. A + giai đoạn có delay chạy goroutine riêng | Mỗi partition hai goroutine | Giai đoạn nhanh không bao giờ bị đói | Phải giới hạn publish song song, đồng bộ chốt phiếu; phức tạp hơn |
| C. Giữ engine, sửa phía dùng | Cache danh sách hội thoại tự nghe event tin (`msg_created/deleted/edited/hidden`) để biết đuôi room cũ | Danh sách hội thoại đúng ngay; không đụng engine | Ghim, mention, `rc` vẫn trễ 5s; thông lượng vẫn ~1,6K phiếu/s mỗi core |

Phiếu lỗi (owner đã chọn 2026-10-14): giãn dần + alert, không bỏ phiếu. Lưu ý: work stream có `MaxAge` 2h, phiếu kẹt quá 2h vẫn bị xoá; alert báo từ lần giao thứ 6 (~15 phút), phần mất khôi phục bằng `/app resync`.

### 1.5 Câu hỏi cần chốt

1. Chọn A, B hay C (hoặc C trước, A sau)?
2. Làm ở milestone nào (M5 hardening hay milestone riêng trước M4)?
3. Ngân sách trễ chấp nhận được cho danh sách hội thoại và projection (vd. ≤ 1s)?

## 2. Sharding

### 2.1 Vấn đề

Thiết kế giả định (design §5.1, D98, D112, D115): collection lớn có `_id` mang tiền tố room, nên shard theo `{_id: 1}` giữ một room trên một shard. Thử trên cluster 2 shard (2026-10-14) cho thấy giả định chỉ đúng với khoá **dài cố định**.

`members`, `message_interactions`, `mentions` có `_id` kết thúc bằng user id dài 1–64 byte. Mongo so BinData **theo độ dài trước**, rồi mới tới byte. Ví dụ room 777:
- member `lan`: `_id` = `777` (8B) + `lan` (3B) = 11B;
- member `nguyen`: `_id` = `777` + `nguyen` (6B) = 14B.

Mọi khoá 11B của mọi room đứng trước mọi khoá 14B. Nên doc của room 777 rải theo từng nhóm độ dài, và balancer có thể đặt các nhóm đó lên các shard khác nhau.

### 2.2 Kết quả thử (2 shard, `mongo:8.2.12`)

| Truy vấn | Kết quả |
|---|---|
| `GetHistory`, `AROUND`, `TailWindow`, `GetMessages` (`messages`, khoá 24B) | Một shard |
| `ListThreads` (`reply_threads`, 24B), `MyInteractions` (`_id $in`) | Một shard |
| `GetReactions` (`message_interactions` theo `message_key`) | **Mọi shard** |
| `ListMembers` (`members` theo `room_id`) | **Mọi shard** |
| Transaction owner (D100) qua hai shard | Chạy đúng |
| Change feed qua mongos | Chạy, nhưng **trễ tới ~10s** khi một shard rảnh (shard rảnh chỉ ghi no-op mỗi `periodicNoopIntervalSecs`, mặc định 10s) |

Mọi truy vấn theo room trên ba collection này (đếm member, tìm owner, đếm lại reaction, mention đường B…) cũng hỏi mọi shard. Các index theo user/target vốn đã được chấp nhận là hỏi mọi shard.

Bản vá hạ tầng (profile compose `shard`, script, itest kiểm từng truy vấn): `bin/m3-shard-profile.patch` (6 file).

### 2.3 Phương án

| Phương án | Làm gì | Được | Mất |
|---|---|---|---|
| A. Shard ba collection theo `{room_id: 1}` | Đổi shard key, không đổi encoding khoá | Mọi truy vấn theo room nhắm một shard; `_id` vẫn duy nhất vì `room_id` suy ra từ `_id` | Phải thử clustered collection nhận shard key khác `_id`; room rất lớn (channel 200K member) nằm trọn một shard |
| B. Khoá dài cố định | Đệm user id đủ 64 byte (members 72B, interactions 89B, mentions …) | Giữ shard theo `{_id: 1}`; quét khoảng `_id` theo room được | Khoá và index to hơn; đổi `pkg/keys`, reset dữ liệu |
| C. Chấp nhận phân tán | Giữ nguyên | Không đổi gì | Chi phí các truy vấn trên tăng theo số shard |

Feed trễ: đặt `periodicNoopIntervalSecs=1` cho shard (dev và prod), ghi vào design.

### 2.4 Câu hỏi cần chốt

1. Chọn A, B hay C cho ba collection?
2. Khi nào cần shard (số doc/ổ đĩa dự kiến)? Có cần quyết trước go-live không?
3. Channel 200K member: một room nằm trọn một shard có chấp nhận được không?
