# chatim — Decision Log

> Ngày cập nhật: 2026-11-07
> Trạng thái: nhật ký quyết định chính thức, chỉ gồm quyết định đã được owner duyệt.
> Tài liệu liên quan: [Kiến trúc hệ thống SSOT](./architecture.md) · [Module tính năng](./features/README.md).

Quyết định nằm trong `docs/decisions/`, mỗi milestone một file tên `D<đầu>-<milestone>.md` (số 3 chữ số). D-id đánh số chung cho cả repo; tìm một quyết định theo D-id bằng tên file hoặc `grep '| D161 |' docs/decisions/`. Tiêu chí và format dòng: `CLAUDE.md` §Docs.

| File | Mục | Thời gian |
|---|---|---|
| [D001–D140 Phase 1: nền tảng, M2b, M2c, M3](decisions/D001-phase1-foundations.md) | §17.1–17.2 | → 2026-10-15 |
| [D141–D162 M3.1: event theo trạng thái doc](decisions/D141-m3-1-state-driven-events.md) | §17.3 | 2026-10-16 |
| [D163–D176 Mention App](decisions/D163-mention-app.md) | §17.4 | 2026-10-17 |
| [D177–D182 Exit Phase 1, layout apps/modules](decisions/D177-phase1-exit-layout.md) | §17.5–17.6 | 2026-10-19 → 2026-10-20 |
| [D183–D206 M4: websocket và gateway](decisions/D183-m4-websocket-gateway.md) | §17.7–17.12 | 2026-10-21 → 2026-10-25 |
| [D207–D208 Phạm vi Phase 2](decisions/D207-phase2-scope.md) | §17.13–17.14 | 2026-10-25 |
| [D209–D261 M6: identity](decisions/D209-m6-identity.md) | §17.15–17.21 | 2026-10-25 → 2026-11-05 |
| [D262–D265 M7: media](decisions/D262-m7-media.md) | §17.22 | 2026-11-06 → 2026-11-07 |

## Ghi chú chung

`gateway` trong các quyết định cũ là app `websocket` (đổi tên 2026-10-12); app HTTPS mới mang tên `gateway` theo D197, D198. `core` trong các quyết định cũ là app `chat` (đổi tên 2026-11-05, [RF3](plans/2026-11-05-m6-rf3-rename-core-to-chat-summary.md)). Mention App (D162–D176) chạy nhúng trong chat, `MentionService` đăng ký trên gRPC server của chat; `MarkRead` ở lại chat.

Quyết định D1–D60 giữ id cũ; D96–D110 (M2b.4) sửa D49 (subject), D67 (`member_count`), D72 (`user_rooms`, `cleared_before_seq`), D75 (`p`), D85 (ẩn/clear có event, mốc thời gian); chi tiết và phương án bị loại của Phase 1 lưu trong Git history. D53–D57 (nháp M2b) bị bỏ, thay bởi D61–D78. D112–D119 (M2c, 2026-10-10) thay phần lưu của D88, toàn bộ D90, phần gán lại seq của D31/D77 và phần thử lại của D92.
