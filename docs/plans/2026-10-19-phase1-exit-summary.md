# Exit Phase 1: còn làm gì

> Ngày: 2026-10-19 · Trạng thái: **owner duyệt 2026-10-19** (D177–D181) · Thay cho RFC 2026-10-18 và hai vòng phản biện (đã gom vào đây)
> Nguồn: `docs/roadmap.md`, `docs/git-workflow.md`, `docs/poc/README.md`, `docs/decision-log.md` §17.5

## 1. Mục tiêu

Exit Phase 1 nghĩa là `core` (có Mention App nhúng bên trong) và `websocket` đạt mức `prod-like validated` (`docs/git-workflow.md`, D178). Go-live (`v1.0.0`) **không** thuộc exit Phase 1, vì go-live cần tài khoản và token thật, mà identity nằm ở Phase 2.

Ví dụ khi exit xong: trên cụm 3 Mongo NVMe + 3 NATS, một client thật kết nối websocket, gửi tin vào nhóm 5.000 người, người được `@` thấy thông báo trong app, và 100K kết nối rớt cùng lúc đều vào lại được trong vòng 1 phút mà core không quá tải.

## 2. Phạm vi và ranh giới

| Việc | Trong Phase 1, app sở hữu | Ngoài Phase 1, làm ở đâu |
|---|---|---|
| Room, member, tin, seq, event | `core` | — |
| Mention, nhóm, `@nhóm`, thông báo `notify.*` | Mention App, **nhúng trong core** (D177) | — |
| Giữ kết nối client, đẩy realtime, gom `MarkRead`, đưa thông báo `notify.*` vào app | `websocket` | — |
| Kiểm tra JWT | `websocket`, bằng một package dùng chung trong `pkg/`, khoá ký dev | `identity-verifier` bọc lại package đó (Phase 2) |
| Tài khoản, đăng nhập, SSO, cấp token | — | `identity` (Phase 2, D179) |
| Push APNs/FCM, lọc mute | — | `notification` (Phase 2) |
| Tìm kiếm | — | `search` (Phase 2) |
| REST quản trị, webhook | — | `gateway` (Phase 2) |
| Nạp dữ liệu hệ cũ, chạy dual | — | `migrator` (Phase 2, test trên dev với data thật, D180) |
| File đính kèm | — | media qua presigned URL (Phase 2) |
| Channel 200K người; sync token D74 | — | backlog (D74 chờ số đo reconnect ở PoC) |

**Vì sao Mention App giữ nhúng** (D177). Quy tắc enterprise: một team, một nhịp release thì để module trong cùng tiến trình (modular monolith). Chỉ tách khi module cần team, SLA, nhịp release riêng, cần scale riêng có số đo, hoặc có database riêng + CDC. Khi đã tách thì app phải tự sở hữu dữ liệu và chỉ gọi core qua API, không đọc chung bảng. Ví dụ: một service riêng đọc thẳng `messages` thì khi core đổi tên field `mt`, mention hỏng ngay, nên hai bên vẫn phải deploy cùng nhau. Hôm nay chưa có điều kiện nào ở trên.

## 3. Số thật dùng để thiết kế và đo (owner 2026-10-19, D181)

| Số | Giá trị | Hệ quả |
|---|---|---|
| Lượt đọc (`MarkRead`) | ~5.000/s (số production) | Mỗi lượt là một lần ghi `members` + một event `read_updated`. Chỉ chạy mức này ở PoC prod-like, cùng tải gửi; websocket gom theo (kết nối, room) ~2s, PoC đo cả trước và sau khi gom. |
| Bench trên dev | tối đa 2K/s | Máy dev không chịu hơn. Mọi bench dev dừng ở 2K; số dev chỉ để so trước/sau và kiểm tra tool. |
| Tỉ lệ sửa tin | 5% | Ở đỉnh 5–10K tin/s là 250–500 lần sửa/s, mỗi lần một fact `message_edits` + projection + event. |
| Room hoạt động cùng lúc | 1.000 | 1.000 actor sống cùng lúc, chia trên 1.024 slot. Bench phải rải tải ra 1.000 room, không dồn vào vài room. |
| Kết nối | dev 10K, prod-like 100K | Số dev chỉ để kiểm tra tool, không suy ra 100K. |
| Dữ liệu PoC | ≥ 1M tin | Sinh bằng tool bench hiện có; migrator ở lại Phase 2. |

## 4. Ba milestone còn lại

Mỗi milestone có một nhánh `feat/<milestone>` và một PR, xong theo Definition of Done của `docs/git-workflow.md`. Mỗi milestone có summary riêng để owner duyệt trước khi code.

| # | Milestone | Việc chính | Xong khi |
|---|---|---|---|
| 1 | **M4: WebSocket** | Như hàng M4 trong roadmap: gws, frame protobuf, fanout theo room, hàng đợi gửi có giới hạn, định tuyến theo slot, gom `MarkRead` ~2s, SDK reconnect có jitter. Thêm: nhận `notify.{tenant}.{user}.*` để hiện thông báo trong app; gọi `MentionService` trên gRPC của core; kiểm tra JWT bằng package dùng chung. | e2e websocket xanh; 10K kết nối trên dev ổn định; ghi số đo bão reconnect và gom `MarkRead` trên dev |
| 2 | **PoC prod-like** | Cập nhật danh sách đo (`docs/poc/README.md`): bỏ P3, P4, P8 (đo effect engine/change stream, đã bỏ ở M3.1); thêm tốc độ sweeper bắt kịp sau sự cố NATS, độ trễ `projections`, độ trễ `mention`, tải `MarkRead` 5.000/s; P7 đo bão reconnect 100K để quyết D74. Tải theo mục 3. | mọi mục đạt ngưỡng → chốt database, go/no-go A1 |
| 3 | **M5: Hardening** | Như hàng M5 trong roadmap: load test 10K kết nối trên dev, 100K trên prod-like; chaos R5 thật; OTel/Prometheus/Grafana; CI đủ; ghim digest image; container theo uid; Sentinel cho cả hai Redis. Thêm: các điều kiện go-live (oplog `minRetentionHours` ≥ 24h, alert RC5 và lag, đo bộ nhớ NATS); xử lý danh sách "Mục mang sang M5" của roadmap (sửa, hoặc chấp nhận và ghi lý do). | không còn lỗi Critical/Important; mọi milestone Phase 1 `prod-like validated` |

**Việc bị nối tiếp:**
- PoC cần M4, vì bão reconnect và gom `MarkRead` phải đo qua websocket. PoC cũng cần hạ tầng prod-like, hiện chưa có ngày → đây là đường găng của Phase 1.
- Chuẩn bị hạ tầng PoC chạy song song với M4.
- M5 làm sau PoC.

## 5. Đã chốt

| # | Quyết định |
|---|---|
| D177 | Giữ Mention App nhúng trong core (D163 giữ hiệu lực); chỉ tách khi có điều kiện ở mục 2, và khi tách thì không đọc chung bảng |
| D178 | Exit Phase 1 = `prod-like validated`; M4 ở lại Phase 1; Search sang Phase 2; Channel và D74 vào backlog |
| D179 | Chatim tự sở hữu tài khoản (`identity`, `identity-verifier`); SSO bên ngoài chỉ là tích hợp; JWT không mang role theo room |
| D180 | Migrator chạy dual với data thật, cả trên dev; mỗi room một nơi cấp seq |
| D181 | Dev bench ≤ 2K/s, dev 10K / prod-like 100K kết nối; dữ liệu PoC sinh bằng tool bench; số thật ở mục 3 |

Ví dụ cho D179: user ở 5.000 room thì token không thể chứa 5.000 role, và khi owner hạ quyền một admin thì quyền mới phải có hiệu lực ngay. Vì vậy role ở lại `members` trong core.

## 6. Điều kiện exit Phase 1

- [ ] M4, PoC, M5 đều `dev-done` và đã merge `main`
- [ ] Mọi mục đo của PoC (danh sách mới) đạt ngưỡng ở tải mục 3
- [ ] `docs/roadmap.md`, `docs/decision-log.md`, `CLAUDE.md`, `INDEXES.csv`, `docs/events.md` khớp với code
- [ ] Tag `v0.x` đầu tiên trên `main`; `v1.0.0` (go-live) để sau khi có identity

## 7. Rủi ro

- **`MarkRead` 5.000/s chỉ đo được ở PoC.** Dev dừng ở 2K, nên nếu ghi `members` hoặc `read_updated` thành nút thắt thì chỉ biết ở PoC; khi đó có thể phải đổi cách gom ở websocket.
- **PoC chưa có ngày hạ tầng:** ngày có hạ tầng quyết định ngày exit Phase 1.
- **Khoá ký dev ở Phase 1:** websocket chạy đúng nhưng chưa đủ điều kiện go-live cho tới khi có identity.

## 8. Còn mở

- Ngày có hạ tầng prod-like (3 Mongo NVMe, 3 NATS, 2 host Linux).
