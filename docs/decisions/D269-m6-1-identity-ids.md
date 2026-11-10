# Decision Log D269–D270: M6.1: `identity` tự cấp id user

## 17.24 M6.1 phase P1 — id user do `identity` cấp, realm của issuer (2026-11-08 → 2026-11-10, owner duyệt)

| # | Quyết định | Lý do |
|---|---|---|
| D269 | (Owner 2026-11-08) **`identity` tự cấp id user ngẫu nhiên; mã user của tenant (`sub`) chỉ nằm trong `identity`.** Chat, token chat và danh bạ chỉ thấy id này; tìm người để chat qua search riêng | Id do tenant đặt có thể đoán được hoặc là dữ liệu cá nhân (số điện thoại): hồ sơ bị dò qua danh bạ và mã lộ cho mọi người ở chung room. Không giữ `sub` kèm quy định cho tenant: không ai kiểm được tenant có làm đúng. Không dùng HMAC của `sub`: ai biết `sub` là tính ra id. Không cho trình duyệt đổi mã sang id: mở lại lỗ dò |
| D270 | (Owner 2026-11-09) **Mỗi issuer thuộc một realm; user được nhận diện theo tenant, realm và `sub`.** Realm mặc định là chính issuer; issuer thay cho IdP cũ được đặt vào realm của issuer cũ | `sub` chỉ duy nhất trong một hệ thống ký. Không gộp theo tenant: hai người của hai hệ thống thành một. Không tách cố định theo issuer: đổi IdP mất hết lịch sử chat. Realm nằm ở cấu hình như tenant, không trong claim, để tenant không tự khai |
