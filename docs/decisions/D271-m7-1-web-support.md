# Decision Log D271: M7.1: hỗ trợ web app và SDK

## 17.25 M7.1 phase 1 — giới hạn cho client (2026-11-10 → 2026-11-11, owner duyệt)

| # | Quyết định | Lý do |
|---|---|---|
| D271 | (Owner 2026-11-10; sửa D229) **Danh bạ trả hồ sơ theo lô nhỏ hơn trước: mỗi lần gọi `GetUsers` nhận ít người hơn, client cần nhiều hơn thì chia thành nhiều lần gọi** | Lô nhỏ giảm lượng dữ liệu một lần gọi trả ra mà không lộ thêm gì, vì id user không đoán được (D269); một trang danh sách hội thoại mặc định vẫn vừa một lần gọi. Không giữ lô lớn cũ: một lần gọi có thể trả tới cả megabyte link ảnh. Không hạ thấp hơn nữa: trang danh sách hội thoại mặc định phải gọi hai lần |
