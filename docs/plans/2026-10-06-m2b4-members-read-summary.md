# M2b.4 — Member + vị trí đọc: tóm tắt cho owner

> Plan chi tiết (cho AI): [2026-10-06-m2b4-members-read.md](2026-10-06-m2b4-members-read.md). Trạng thái: **chờ owner duyệt bản này rồi mới thực thi.** M2b.4 là phần cuối của nhánh `feat/m2b`; xong thì mở PR merge `main` (hỏi owner trước).

## Người dùng thấy gì

**Thành viên group**
- Owner và admin thêm người vào group, mỗi lần tối đa 500 người (cấu hình). Không giới hạn tổng số member.
- Owner xoá được mọi người; admin chỉ xoá được member thường.
- Chỉ owner đổi role (owner, admin, member). Một group có thể có nhiều owner.
- Ai cũng tự rời group được. Owner cuối cùng rời thì quyền owner tự chuyển cho admin vào sớm nhất; không có admin thì cho member vào sớm nhất. Owner cuối cùng không tự hạ role được.
- Người mới vào thấy toàn bộ tin cũ, nhưng tin cũ coi như **đã đọc**.
- Người rời hoặc bị xoá **không gửi và không đọc** được nữa, có hiệu lực ngay. Được thêm lại thì đọc lại được.
- DM (chat 1-1) cố định 2 người: không thêm, xoá, rời hay đổi role.
- Lệnh gửi lại (do mất mạng) không gây lỗi: thêm người đã có, hay rời khi đã rời, thì không đổi gì.

**Đọc tin**
- Đánh dấu đã đọc tới một tin; vị trí đọc chỉ tiến lên, trừ khi người dùng chủ động đánh dấu chưa đọc.
- "Đánh dấu chưa đọc" lùi vị trí đọc về trước một tin (kiểu Slack). Hai thiết bị đổi cùng lúc thì lệnh tới sau thắng.
- "Đã xem": DM và group ≤ 100 người (cấu hình) thấy "đã xem" của nhau, gộp tối đa 1 lần mỗi vài giây. Group lớn hơn chỉ đồng bộ giữa các thiết bị của chính mình, để tránh bão event.
- Số tin chưa đọc trong danh sách room làm ở M3.

**Realtime**
- Event `member_added`, `member_removed` (kèm lý do và owner mới nếu có), `member_role_changed` phát cho cả room.
- Riêng người bị ảnh hưởng nhận thêm một bản trên kênh riêng của mình. Nhờ đó gateway biết để bắt đầu hoặc ngừng nhận tin của room cho người đó, kể cả khi họ vừa được thêm và chưa nghe room.
- Event `read_updated` cho "đã xem".
- Event bị mất thì hệ thống tự phát lại; riêng "đã xem" là best-effort, không phát lại.

## Owner đã chốt (2026-10-06)

| Chốt | Nội dung |
|---|---|
| Quyền mặc định | Owner/admin quản lý; admin chỉ thêm người và xoá member thường; chỉ owner đổi role, xoá admin/owner; ai cũng tự rời |
| Role | owner, admin, member; được nhiều owner |
| Owner cuối rời | Tự chuyển cho admin vào sớm nhất, không có thì member vào sớm nhất |
| Người mới | Thấy toàn bộ lịch sử; tin cũ coi như đã đọc |
| Sau khi rời/bị xoá | Mất quyền đọc |
| DM | Cố định 2 người |
| Đánh dấu chưa đọc | Lùi vị trí đọc |
| "Đã xem" | DM và group nhỏ (≤ 100) phát cho room; group lớn chỉ thiết bị của mình |
| Số member | Core không giới hạn |

## Chi tiết kỹ thuật đội tự chọn (mỗi mục một câu)

- Mỗi thay đổi member là một fact bất biến đánh số liên tục theo room, giống ghim; tạo room cũng ghi fact đầu tiên.
- Danh sách member, danh sách room của từng user và số member được cập nhật ngay trong lệnh, nên quyền có hiệu lực tức thì; việc chạy nền sửa lại nếu core chết giữa chừng.
- Số member gập thẳng từ fact, không đếm lại, nên đúng và rẻ cả với channel lớn.
- Xoá member không xoá dữ liệu: đánh dấu đã rời; thêm lại thì giữ mốc clear cũ của họ.
- Danh sách room của user khoá theo (tenant, user, room), sửa khoá cũ trong thiết kế vốn bỏ quên tenant.
- Sửa lỗi có sẵn: core cache danh sách member mãi mãi, nên người bị xoá vẫn gửi được. Nay xoá cache ngay khi đổi member, kèm hạn 10 giây.
- Event giờ gửi được lên kênh riêng của từng user; luật chuyển tiếp của stream được mở rộng.
- Mỗi lần thêm hoặc tạo room tối đa 500 người (cấu hình, trần 1000), thay giới hạn cứng 5000 member cũ.
- Phần gộp "đã xem" có thêm một bước khi tắt core (1 giây), vẫn trong ngân sách dừng 28 giây.
- `/app resync` phát lại cả thay đổi member trong khoảng thời gian bị mất.

## Giới hạn còn lại

- Thay đổi member trong một room đi tuần tự (khoảng 100–200 lệnh/giây mỗi room). Bão người vào một channel 200K cần cơ chế gom; để milestone Channel.
- Nếu core chết đúng lúc giữa ghi fact và cập nhật danh sách, quyền cũ còn vài giây cho tới khi việc chạy nền sửa.
- Khi nâng cấp từng core một, core cũ chưa biết trạng thái "đã rời" và có thể ghi đè luật chuyển tiếp khi khởi động lại. Cần nâng hết các core trước khi dùng tính năng member. Prod chưa chạy nên chưa ảnh hưởng.
- "Đã xem" có thể mất nếu core chết đúng lúc; lần đọc sau sẽ phát lại vị trí mới.
- Đếm số tin chưa đọc và danh sách room của tôi làm ở M3.

## Sẽ kiểm

Unit test; integration test trên Mongo/NATS/Redis thật, gồm:
- core chết giữa chừng;
- hai admin xoá nhau cùng lúc;
- event tới kênh riêng của user;
- người bị xoá không gửi được.

E2e trên cụm hai core:
- thêm người: họ nhận event và thấy lịch sử, không có tin chưa đọc;
- đổi role;
- đánh dấu đọc và chưa đọc;
- xoá người: họ không gửi hay đọc được nữa;
- owner cuối rời: quyền owner tự chuyển;
- DM từ chối mọi lệnh member.
