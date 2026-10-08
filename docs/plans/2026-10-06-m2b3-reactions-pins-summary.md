# M2b.3 — Reaction + ghim: tóm tắt cho owner

> Plan chi tiết (cho AI): [2026-10-06-m2b3-reactions-pins.md](2026-10-06-m2b3-reactions-pins.md). Trạng thái: `dev-done` trên nhánh `feat/m2b`, chưa merge `main` (merge một lần sau M2b.4).

## Người dùng thấy gì

**Reaction**
- Mỗi người thả **một emoji** trên một tin. Chọn emoji khác thì thay emoji cũ; gỡ được bất cứ lúc nào.
- Chỉ chọn được trong **danh sách cố định** do hệ thống cấu hình, mặc định 👍 ❤️ 😂 😮 😢 🙏. FE lấy danh sách qua API `GetReactionSettings`; emoji ngoài danh sách bị từ chối.
- Mỗi tin hiện số đếm theo từng emoji. Tin đã xoá, đã ẩn hay nằm trong phần đã clear thì không hiện số đếm.
- Không thả reaction mới lên tin đã xoá được, nhưng vẫn gỡ được reaction cũ.

**Ghim**
- Mọi thành viên room được ghim và bỏ ghim. Module policy chat sau này có thể giới hạn theo role.
- Tối đa **50 tin ghim** mỗi room (cấu hình `PIN_LIMIT`); giới hạn này chính xác kể cả khi nhiều người ghim cùng lúc.
- Ghim tin đã ghim, hay bỏ ghim tin chưa ghim, thì không có lỗi và không đổi gì.
- Không ghim được tin đã xoá, nhưng vẫn bỏ ghim được.
- Mỗi lần ghim hay bỏ ghim phát một event. Sau này module tin hệ thống dùng event đó để sinh tin "X đã ghim…".

**Realtime:** event `reaction_changed`, `counts_changed`, `msg_pinned`, `msg_unpinned`. Event bị mất thì hệ thống tự phát lại sau vài giây.

## Owner đã chốt (2026-10-06)

| Chốt | Nội dung |
|---|---|
| Reaction | Một emoji mỗi người trên một tin. "Nhiều trên một người" là reply và mention (M2c), không phải reaction |
| Emoji | Danh sách cố định trong config của core, mặc định 6 emoji; core có API cho FE đọc |
| Quyền ghim | Mặc định mọi thành viên |
| Giới hạn ghim | 50 mỗi room |
| Ghim không cần version phía client | Server tự xử lý tranh chấp |
| Số đếm | Cập nhật ngay khi thả, rồi hệ thống kiểm lại sau |
| Lịch sử tin | Chỉ trả số đếm; "emoji của tôi" để M3 |
| Ghi reaction | Dựa vào khoá unique, không làm phức tạp |
| Quy tắc mới | Mỗi plan có bản tóm tắt như file này để owner duyệt |

## Chi tiết kỹ thuật đội tự chọn (mỗi mục một câu)

- Mỗi reaction là một doc riêng, khoá theo (tin, người); mỗi lần đổi tăng một số đếm thay đổi, dùng làm id event chống trùng.
- Số đếm theo emoji không cộng/trừ dồn mà đếm lại rồi ghi có kiểm phiên bản, nên không bao giờ lệch vĩnh viễn.
- Lần ghim/bỏ ghim là fact bất biến đánh số liên tục; danh sách ghim của room được dựng lại từ các fact đó.
- Bốn việc chạy nền mới: đếm lại reaction, phát lại event reaction, dựng lại danh sách ghim, phát lại event ghim.
- Có metric và alert mới khi việc chạy nền phải sửa số đếm quá nhiều (16 luật alert).
- Công cụ `/app resync` phát lại cả reaction và ghim trong khoảng thời gian bị mất.

## Giới hạn còn lại

- `/app resync` chỉ tìm thấy room có tin hoặc sửa/xoá trong khoảng mất. Room chỉ có reaction hay ghim thì phải chạy kèm `-room`.
- Event reaction trung gian có thể mất (ví dụ 👍 rồi ❤️ thì có thể chỉ thấy event ❤️); trạng thái cuối luôn đúng.
- Khi nâng cấp từng core một, alert lỗi worker kêu cho tới khi mọi core lên bản mới.
- Bài đăng channel 200K người với rất nhiều reaction mỗi giây cần thêm cơ chế gom; để milestone Channel.
- Reaction và ghim hiện chỉ cho timeline chính; thread ở M2c.

## Đã kiểm

Unit test, integration test trên Mongo/NATS/Redis thật, e2e trên cụm hai core (thả reaction, đổi emoji, emoji ngoài danh sách bị từ chối, ghim, kill một core không mất tin), 16 luật alert, đo tải ngắn 1000 tin/s.
