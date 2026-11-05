# Decision Log D207–D208: Phạm vi Phase 2

## 17.13 Tập go-live Phase 2 (2026-10-25, owner duyệt)

| # | Quyết định | Lý do |
|---|---|---|
| D207 | (Owner 2026-10-25) **Go-live `v1.0.0` cho một product mới = Phase 1 `prod-like validated` (D178) + `identity` (danh bạ user: profile và tra theo lô; thu hồi tới `websocket` và `gateway`) + media (field đính kèm trong core, presigned URL qua `apps/gateway`) + `notification` + push + `search` người/nhóm.** Sau go-live: API cho hệ thống khác (nhóm route API key trong `apps/gateway`) + webhook (worker riêng), policy quyền, tìm nội dung tin, `migrator` (D180 giữ hiệu lực, chỉ dời lịch). Thứ tự milestone ở [roadmap](../roadmap.md) | Không đưa `migrator` vào go-live: người dùng đầu tiên là product mới, không có user hệ cũ để chuyển. Không làm search trước identity: core không lưu tên, nên gợi ý người cần danh bạ của identity. Media đứng trước notification: media sửa core (field + proto) nên phải xong trước lần đo PoC prod-like cuối, không thì phải đo lại. Không gộp webhook vào tiến trình `gateway`: gọi endpoint ngoài có thể chậm, có retry ra ngoài, không được kéo chậm đường của máy client. Không làm `gateway` thành "REST quản trị + webhook, không auth" như roadmap cũ: `apps/gateway` đã là HTTPS cho máy có JWT (D198) |

## 17.14 Vị trí client test websocket (2026-10-25, owner duyệt)

| # | Quyết định | Lý do |
|---|---|---|
| D208 | (Owner 2026-10-25; thay phần vị trí của D206) **Client test websocket nằm ở `pkg/wstest`.** Lỗi của `pkg/wsclient` là cái tên (nghe như SDK), không phải vị trí `pkg/`. `pkg/wstest` dùng được cho test của `apps/websocket`, e2e của `tools/corecli` và `tools/poc/wsload`; luật depguard `wstest-tests-only` cấm code không phải test trong `apps/`, `modules/`, `pkg/` import nó. Repo vẫn chỉ là server (D206): không nối lại, không `Applier`, SDK ở repo khác. | Không giữ `internal/wstest` ở gốc module: một thư mục `internal/` chỉ cho một package. Không đặt ở `apps/websocket/internal/wstest` hay `tools/internal/wstest`: luật `internal/` của Go chặn một phía. Không đặt ở `apps/websocket/wstest`: phá layout D182. Tên `wstest` nói rõ đây là client cho test; `pkg/` là chỗ code dùng chung; depguard chặn dùng nhầm ở production |
