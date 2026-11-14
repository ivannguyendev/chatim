# Decision Log D271–D273: M7.1: hỗ trợ web app và SDK

## 17.25 M7.1 phase 1 — giới hạn cho client (2026-11-10 → 2026-11-11, owner duyệt)

| # | Quyết định | Lý do |
|---|---|---|
| D271 | (Owner 2026-11-10; sửa D229) **Danh bạ trả hồ sơ theo lô nhỏ hơn trước: mỗi lần gọi `GetUsers` nhận ít người hơn, client cần nhiều hơn thì chia thành nhiều lần gọi** | Lô nhỏ giảm lượng dữ liệu một lần gọi trả ra mà không lộ thêm gì, vì id user không đoán được (D269); một trang danh sách hội thoại mặc định vẫn vừa một lần gọi. Không giữ lô lớn cũ: một lần gọi có thể trả tới cả megabyte link ảnh. Không hạ thấp hơn nữa: trang danh sách hội thoại mặc định phải gọi hai lần |

## 17.26 M7.1 phase 3–4 — ảnh nhóm (2026-11-12 → 2026-11-14, owner duyệt)

| # | Quyết định | Lý do |
|---|---|---|
| D272 | (Owner 2026-11-13, 2026-11-14; sửa D263, D267) **File media có hai loại tách hẳn, loại nằm ngay trong khoá file: file riêng chỉ dùng trong tin nhắn và chỉ ai đọc được tin mới xem được; file công khai chỉ dùng làm ảnh nhóm và ai đăng nhập cùng tenant cũng xem được** | Ảnh nhóm phải hiện cho người đang lướt danh sách hội thoại mà mỗi lần xem không phải hỏi chat ai là thành viên; tách hẳn để file trong tin cũ không lộ cho người đã rời nhóm; loại nằm trong khoá để chat kiểm được mà không gọi media, và danh sách file của chính mình chỉ hiện file gửi được vào tin. Không chọn một loại file kiểm theo thành viên nhóm: mỗi lần xem ảnh phải hỏi chat và đọc danh sách thành viên. Không cho tin nhắn nhận file công khai: lộ file cho người đã rời nhóm. Không ghi loại vào bản ghi upload: chat phải hỏi media mới biết |
| D273 | (Owner 2026-11-10, 2026-11-12, 2026-11-13) **Ảnh nhóm do owner hoặc admin đặt bằng một file công khai, thay cho chuỗi ảnh tự do trước đây; nhóm chưa có ảnh thì client tự ghép từ vài thành viên bất kỳ còn trong nhóm do chat trả kèm room, không theo thứ tự** | Một nguồn ảnh rõ ràng và link chỉ sống trong hạn ký; ảnh ghép không cần thứ tự nên chat không phải sắp xếp hay ghi lại khi đổi vai trò. Không giữ chuỗi ảnh tự do song song: hai nguồn ảnh làm client rối và link không bao giờ hết hạn. Không trả thành viên theo vai trò hay lúc vào nhóm: tốn thêm truy vấn và phải cập nhật khi đổi vai trò. Không để client tự hỏi thành viên từng nhóm: mỗi nhóm một lần gọi |
