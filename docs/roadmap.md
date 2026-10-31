# Roadmap — chatim

> Cập nhật 2026-10-25: Phase 1 (M0 → M4) đã merge `main`; Phase 2 theo D207 (M6 identity → M7 media → M8 notification + push → M9 search người/nhóm → go-live `v1.0.0`). Lịch sử từng milestone nằm ở các [tóm tắt](plans/) và [Decision Log](decision-log.md). Thiết kế: [architecture.md](architecture.md) · PoC: [poc/README.md](poc/README.md)

## Quy tắc viết plan

- Chia phase trước khi viết summary (owner chốt 2026-10-25): sau khi lưu decision log, một reviewer độc lập đề xuất cách chia milestone thành phase (mục tiêu, sản phẩm test được riêng, phụ thuộc, yêu cầu nào thuộc phase nào); owner duyệt cách chia và danh sách phase ghi vào dòng milestone dưới đây. Summary và plan chi tiết chỉ viết cho **một phase**, phase sau viết khi phase trước xong. Quy trình đủ ở `CLAUDE.md` §Workflows.
- **Bảng phủ** bắt buộc trong mọi summary và plan chi tiết (owner chốt 2026-10-25): yêu cầu → phase/thành phần → task → test → bằng chứng (điền sau khi thực thi). Ô trống = chia thiếu, không duyệt. Mọi khẳng định ghi nguồn (yêu cầu §2, D-id, câu trả lời của owner kèm ngày, `file:dòng`, output lệnh); chưa có nguồn thì ghi "chưa có nguồn" và hỏi, không đoán.
- Đóng mỗi phase: cập nhật dòng milestone, báo phase vừa xong (kèm bằng chứng), phase tiếp theo và các phase còn lại; rà xem có rule mới owner đã xác nhận không.
- `docs/plans/` chỉ chứa tài liệu để owner review và duyệt (`<plan>-summary.md`); plan execute chi tiết cho AI nằm ở `.claude/plans/<plan>.md` (local, gitignore; owner chốt 2026-10-09); khi đóng phase, Minor của plan gộp vào summary hoặc "Mục mang sang M5", decision log gộp vào Decision Log, rồi xoá cả hai (owner 2026-10-25). Mỗi milestone có plan riêng, viết **trước khi code**. Plan **không chứa code** (owner chốt 2026-10-07; thay cho hướng dẫn "code đầy đủ" của skill `writing-plans`): mỗi task ghi mục tiêu, file tạo/sửa, todo, kỹ thuật (API, toán tử Mongo, index, thứ tự, mã lỗi), tên và hợp đồng mới viết bằng lời, test cần viết và mỗi test chứng minh gì, lệnh chạy, tiêu chí xong, commit. Người thực thi tự viết code theo TDD; gặp chỗ plan không nói tới thì dừng và báo.
- Plan mỗi phase/milestone mở đầu bằng bảng **scope và ranh giới** (owner chốt 2026-10-12): việc nào trong scope và service nào giữ; việc nào ngoài scope và thuộc về đâu (core, websocket, app nào). Core cung cấp API dữ liệu (gửi, đọc, sửa, xoá, lịch sử, reaction, vị trí đọc…) và phát event, một lần gọi mỗi màn hình; giá trị theo người xem tính lúc đọc (unread, danh sách hội thoại đã xếp) thuộc core từ 2026-10-13; trạng thái lưu theo người xem và sync là việc của app.
- Plan mở đầu bằng bảng **tính năng → lớp dữ liệu** ([thiết kế §4](architecture.md#4-khung-lớp-dữ-liệu-data-class-framework)). Mỗi tính năng khai báo: fact (lệnh atomic nào, doc nào), cách idempotent (cid hoặc `base_ver`), effect + chính sách, quyền (permission hook), view theo người đọc, dòng trong danh mục event, ngân sách khuếch đại ở nhóm 5K (channel 200K khi Channel ra khỏi backlog), guarantee + detector. Tính năng không khớp lớp nào thì dừng, thiết kế lớp đó trước.
- Dữ liệu suy ra (counter, projection) ghi ngay trong lệnh mà không gộp được với lần ghi chính vào một transaction: dùng **phiếu hẹn sinh tồn** ([thiết kế §7.1](architecture.md), D111), không thiết kế lại từ đầu.
- Bộ test chung mỗi PR: fact sinh đúng effect; no-op không sinh effect; lỗi không sinh effect; parity fast path / reconciler; chạy effect hai lần cho cùng kết quả.
- Mỗi plan có file **tóm tắt kỹ thuật** đi kèm `docs/plans/<plan>-summary.md` (owner chốt 2026-10-06, sửa 2026-10-07), tiếng Việt, không có code Go, viết **trước khi thực thi**; owner phản biện và duyệt bản này, plan chi tiết chỉ dành cho AI. Thực thi xong, cập nhật bản tóm tắt theo cái đã xây (lệch so với plan, known issues) để làm tài liệu lưu trữ. Nội dung bắt buộc:
  1. bảng thuật ngữ;
  2. bức tranh chung có sơ đồ;
  3. bảng field đầy đủ (tên, nghĩa, ví dụ, dùng để làm gì);
  4. luồng xử lý từng use case có sơ đồ sequence, ghi rõ đọc/ghi gì, kiểm gì, mã lỗi gRPC;
  5. cơ chế đúng đắn và các ca chạy đua;
  6. event và subject;
  7. thư viện và hạ tầng;
  8. quyết định kèm phương án bị loại và lý do;
  9. chi phí và tải;
  10. rủi ro và giới hạn;
  11. điểm đội tự chọn để owner phản biện;
  12. kiểm thử, mỗi mục chứng minh gì.

  Chỗ nào làm việc phải xếp hàng (đánh số liên tục, khoá chung) phải nêu rõ.
- Tên field DB (owner chốt 2026-10-07): tiếng Anh đầy đủ, từ cơ bản, cho mọi collection trừ `messages` (giữ tên ngắn, có bảng tra trong thiết kế §5); audit `updated_at` / `updated_by`; bộ đếm chỉ tăng, có lỗ đuôi `_ver`; id chống gửi lại `request_id`.

## Milestone

| Phase | Milestone | Nội dung | Trạng thái |
|---|---|---|---|
| 1 | M0–M2a.3 — Nền tảng, gửi tin + lịch sử | Monorepo, Go qua Docker, hạ tầng dev, slot ownership mềm, PoC R1–R5; `CreateRoom`/`SendMessage`/`GetHistory` qua gRPC, actor + flusher, chống trùng cid, event id tự nhiên (D1–D60) | ✅ `dev-done`, merge `main` (PR #2, #5, #8–#11) |
| 1 | M2b.0 — Nền cơ chế | Permission hook `access.Policy`, reader pipeline `view`, detector + alert, contract test của store (D61–D78) | ✅ PR #12 — [tóm tắt](plans/2026-10-05-m2b0-mechanism-foundations-summary.md) |
| 1 | M2b.1 — Effect engine | Engine đọc change feed + work stream (đã bỏ ở M3.1), `room_created`, room activity, `/app resync` (D79–D81) | ✅ PR #12 — [tóm tắt](plans/2026-10-05-m2b1-effect-engine-summary.md) |
| 1 | M2b.2 — Sửa + xoá | Fact `message_edits` + projection `messages`, ẩn/clear phía người đọc, `GetEditHistory`, quyền sửa/xoá qua `access.Policy` (D82–D87) | ✅ PR #12 — [tóm tắt](plans/2026-10-05-m2b2-edit-delete-summary.md) |
| 1 | M2b.3 — Reaction + ghim | Một emoji mỗi (user, tin), danh sách emoji cố định, ghim là fact `pin_actions` + projection `rooms.pins` (D88–D95) | ✅ PR #12 — [tóm tắt](plans/2026-10-06-m2b3-reactions-pins-summary.md) |
| 1 | M2b.4 — Member + vị trí đọc | Member mỗi (room, user) một doc, transaction đổi owner, `member_count` `$inc` + phiếu hẹn, vị trí đọc, subject theo loại dữ liệu (D96–D111) | ✅ PR #12 — [tóm tắt](plans/2026-10-06-m2b4-members-read-summary.md) |
| 1 | M2c — Thread & tiện ích | `message_interactions` (reaction, trả lời có trích, bookmark), mention theo đích, forward, `OpenDirectRoom`, `CreateRoom` có `request_id` (D112–D119) | ✅ PR #14 — [tóm tắt](plans/2026-10-10-m2c-threads-mentions-summary.md) |
| 1 | M3 — API truy vấn của core | `ListMyRooms`/`GetRooms` (core xếp, đếm unread), cài đặt hội thoại, `UpdateRoom`, `AROUND`, `GetMessages`, `ListThreads`, `GetReactions`, `ListPins`, `ListMembers`, tin hệ thống (D120–D140) | ✅ PR #15 — [tóm tắt](plans/2026-10-12-m3-core-query-apis-summary.md), [chủ đề hoãn](plans/2026-10-15-m3-open-topics.md) |
| 1 | M3.1 — Event theo trạng thái doc | Bỏ effect engine; cờ `_state` cùng lệnh ghi, xoá sau PubAck, sweeper slot 0, consumer `projections`, phiếu hẹn trên `CHATIM_TIMER` (D141–D160) | ✅ PR #15 — [tóm tắt](plans/2026-10-16-m3-1-state-driven-events-summary.md) |
| 1 | Mention App | `modules/mention` nhúng trong core: chỉ mục mention, `ListMentions`, nhóm được tag, `@nhóm`, thông báo `WsPublish` (D162–D177) | ✅ PR #16 — [tóm tắt](plans/2026-10-17-mention-app-summary.md) |
| 1 | Refactor core theo domain | Gom package của core theo domain, `modules/` cho module nhúng (D182) | ✅ PR #18 — [tóm tắt](plans/2026-10-19-core-domain-layout-summary.md) |
| 1 | M4 — WebSocket + gateway | `apps/websocket` (kết nối, token, 12 lệnh ghi realtime, đẩy event, đã đọc, đang gõ, online, giữ kết nối khi token hết hạn) và `apps/gateway` (HTTPS cho 22 lệnh) (D183–D206) | ✅ PR #19 — tóm tắt [phần 1](plans/2026-10-20-m4-1-ket-noi-va-lenh-summary.md), [2](plans/2026-10-21-m4-2-nhan-va-day-event-summary.md), [3](plans/2026-10-22-m4-3-da-doc-va-da-xem-summary.md), [gateway](plans/2026-10-23-m4-gateway-https-summary.md), [4](plans/2026-10-24-m4-4-typing-va-online-summary.md), [5](plans/2026-10-24-m4-5-noi-lai-van-hanh-summary.md) |
| 2 | **M9 — Search App** (go-live: người/nhóm) | **Owner 2026-10-19: sang Phase 2 (trước: Phase 1, chưa lên lịch); D207: tìm người/nhóm thuộc go-live, làm sau M8, tên lấy từ danh bạ của M6; tìm nội dung tin sau go-live.** Gợi ý người/nhóm làm trước (Typesense/Meilisearch), tìm nội dung sau. App riêng `apps/search` theo khuôn Mention App: consumer riêng chỉ lọc subject cần (`msg_created` cả loại `message` lẫn `mention`, `msg_edited`, `msg_deleted`, `member_added`, `member_removed`), ghi vào search engine, bỏ trùng theo version (D156), dựng lại chỉ mục bằng `/app resync`; tên người lấy từ hệ thống user/nhóm (core không lưu tên). Phục vụ: tìm người theo tên trong room lớn (channel 200K người), tìm nhóm, tìm nội dung tin. Điểm khó: quyền lúc tìm (room đang ở, sau `joined_at`/`cleared_at`, bỏ tin ẩn/xoá, đúng tenant), tiếng Việt (bỏ dấu, `đ` → `d`, tách từ), chọn engine theo quy mô (gợi ý người/nhóm: Typesense/Meilisearch; nội dung 5–20 tỷ tin: OpenSearch/Elasticsearch cluster, chi phí hạ tầng lớn) | Chưa |
| 1 | PoC prod-like (song song) | Mongo rs 3 member NVMe, NATS 3 node, 2 host Linux; ≥ 1M tin sinh bằng tool bench hiện có (owner 2026-10-19, migrator giữ ở Phase 2). Số thật (owner 2026-10-19): ~5.000 `MarkRead`/s, 5% tin bị sửa, 1.000 room hoạt động cùng lúc. Danh sách đo ở [poc/README.md](poc/README.md) phải cập nhật trước khi chạy: bỏ P3, P4, P8 (đo effect engine/change stream, đã bỏ ở M3.1); thêm tốc độ sweeper bắt kịp sau sự cố NATS, lag `projections`, lag `mention`; P7 đo bão reconnect 100K qua websocket để quyết D74 → chốt database, go/no-go A1 | Chờ hạ tầng (đường găng của exit Phase 1) |
| 1 | M5 — Hardening | Load test 10K kết nối trên dev, 100K trên prod-like; bench trên dev tối đa 2K/s (owner 2026-10-19), chaos test (kể cả R5 thật), OTel/Prometheus/Grafana, CI đầy đủ, ghim digest image, container theo uid, Sentinel (`FailoverClient`) cho cả hai Redis; điều kiện go-live (oplog `minRetentionHours` ≥ 24h, alert RC5 và lag, đo bộ nhớ NATS); mục mang sang (xem dưới): sửa, hoặc chấp nhận và ghi lý do | Chưa |
| Backlog | Channel | Room type channel (~200K subscriber, chỉ admin post): quyền post, fanout, số trên tin nóng (gom `counts_changed` của tin nhận nhiều react/s; bắt buộc trước khi channel go-live). Owner 2026-10-19: vào backlog, làm khi có yêu cầu nghiệp vụ | Backlog |
| Backlog | Sync token (D74), đa thiết bị, dấu `@` trên danh sách hội thoại | Chờ số đo bão reconnect 100K (PoC P7) | Backlog |
| Backlog | Tenant ẩn nội dung tin (D75) | Owner 2026-10-20: tách khỏi M4, làm khi có khách cần. Tenant có chính sách ẩn nội dung thì websocket bỏ text khỏi event trước khi đẩy xuống máy, không đọc lại từ core. Khi làm: reviewer xem lại thiết kế (bản event trong `CHATIM_EVT` vẫn giữ text 7 ngày) | Backlog |
| 2 | **M6 — `iam`** (go-live) | (D179, D207; owner 2026-10-25 đổi tên `identity` → `iam`, IAM tổng quát kiểu Keycloak tự xây bằng Go trên Postgres, chatim chỉ là một client; bỏ service `identity-verifier`, kiểm token nhúng trong node websocket/gateway) Khái niệm: tenant (realm), account, client, role, permission, permission_group, IdP, session, token, exchange. Đăng nhập go-live: mật khẩu Argon2id (admin cấp account, admin đặt lại mật khẩu, chưa cho đăng ký, chưa gửi email) + OIDC qua IdP ngoài; token exchange id_token IdP ngoài → token `iam` (RFC 8693); SAML, MFA sau go-live. Phase (owner duyệt 2026-10-25, làm trước đăng nhập mật khẩu và phiên): **P1** đăng nhập mật khẩu + phiên (`apps/iam`, Postgres, ký Ed25519 bằng `golang-jwt` sẵn có, DPoP gắn refresh token, lệnh admin cấp account, login/refresh/logout, refresh xoay vòng, liệt kê và thu hồi phiên, audit log; node vẫn đọc JWKS từ file do `iam` xuất) · **P2** thu hồi tới websocket/gateway (`sid`/`iat`, `pkg/revocation`; owner 2026-10-27 "ko nên phụ thuộc NATS": node hỏi `iam` qua HTTPS cả danh sách thu hồi lẫn JWKS, không outbox, không NATS KV) · **P3** quyền + danh bạ, chia 3 phần (owner duyệt 2026-10-31 sau review chia phase; thay "outbox + event": feed thay đổi HTTP long-poll trên cổng nội bộ, không NATS): **P3a** danh bạ trong `iam` (profile, cây nhóm, `GetUsers`, feed `directory/changes`, CLI nhóm) · **P3b** đồng bộ nhóm sang Mention (core giữ slot 0 kéo feed, gọi `SetUserGroups`/`SetGroups`) · **P3c** role, permission, API admin · **P4** OIDC provider + IdP ngoài + exchange (`zitadel/oidc` v3, spike EdDSA trước) · **P5** sẵn sàng go-live (OpenID conformance suite, security review, vận hành Postgres) | **P1 ✅** `dev-done` trên `feat/m6` (2026-10-27, D209–D218): fmt/vet/lint/test xanh, itest Postgres xanh, `make e2e` PASS 13 phase (phase 13 `iam`), số đo băm ~40 ms, kiểm token ~121 µs, xoay refresh ~1,7 ms — [tóm tắt](plans/2026-10-25-m6-1-dang-nhap-mat-khau-va-phien-summary.md). **P2 ✅** `dev-done` trên `feat/m6` (2026-10-31, D219–D225): fmt/vet/lint/test xanh, `make itest` xanh, `make alerts-check` 33 luật, `make e2e` PASS 14 phase (phase 14: thu hồi tới websocket-1 và gateway 1,7s, `websocket-2` bị chặn `iam` nhận qua Redis, `key-rotate` một lệnh đổi khoá không restart, khoá tenant đóng `4003`) — [tóm tắt](plans/2026-10-27-m6-2-thu-hoi-toi-node-summary.md). **P3a ✅** `dev-done` trên `feat/m6` (2026-10-31, D226–D231): `make test`/`make itest` 107 package xanh, lint 0 issue, `make alerts-check` 39 luật, `make e2e` PASS 15 phase (phase 15: long-poll trả tên mới ≤ 60ms sau khi đổi, `GetUsers` từ RAM thấy sau 1ms, khoá account tới feed 2ms); bench dev: 100K người 18,6 MB RAM, nạp 0,73s ở ca xấu nhất, 10 replica 3,2s, `GetUsers` ~1,1 core cho 1.700 lần/giây — [tóm tắt](plans/2026-10-31-m6-3a-danh-ba-iam-summary.md). **Tiếp:** P3b đồng bộ nhóm sang Mention (D232). P3c (D233), P4–P5: chưa |
| 2 | **M7 — Media** (go-live) | (D207) Presigned URL S3/MinIO qua `apps/gateway`, field đính kèm trong `messages` (≤ 10 tham chiếu, A6), URL GET cấp sau `Admit` của core. Còn phải thiết kế (phản biện 2026-10-19): prefix object theo tenant, dọn file đã upload mà không gửi, giới hạn dung lượng, quét mã độc. Sửa core (field + proto) nên làm trước lần đo PoC cuối | Chưa |
| 2 | **M8 — `notification` + push** (go-live) | (D207) Push APNs/FCM (badge xấp xỉ, D73), danh sách máy theo session của M6, giữ bảng mute từ event `room_preferences_changed`, chỉ push khi offline; hộp thư `notify.{t}.{kind}` (`CHATIM_NOTIFY`). Còn mở, hỏi owner khi brainstorm: ai chọn người nhận push cho tin thường (D104; Mention App chỉ chọn cho mention) | Chưa |
| 2 | **Go-live `v1.0.0`** | (D207) Khi Phase 1 `prod-like validated` và M6–M9 xong; identity cần security review riêng trước go-live | Chưa |
| 2 (sau go-live) | API cho hệ thống khác + webhook | (D207) API cho backend tenant/bot là nhóm route xác thực bằng API key trong `apps/gateway`; webhook là worker riêng (gọi endpoint ngoài chậm, retry ra ngoài, không kéo chậm đường của máy client) | Sau go-live |
| 2 (sau go-live) | `migrator` | (D180, dời sau go-live theo D207) Chạy dual với data thật cả trên dev; mỗi room một nơi cấp seq; import lịch sử im lặng (không bật `_state`) rồi `/app resync -rate` + `/app recount` dựng lại dẫn xuất; room chưa chuyển chỉ đọc trên chatim; sửa trước lỗi resync dừng sớm (mục mang sang M2b.1). Hệ nguồn: Node.js + MongoDB (doc lớn, BullMQ, Socket.IO). Plan cũ 2026-10-13 (bị thay bởi D180) còn ý dùng được: công cụ đối soát (đếm, checksum, seq liền nhau), không đụng prod cũ, tắt hệ cũ sau 14 ngày chạy ổn, id tin cũ làm cid. Hỏi owner khi brainstorm: dữ liệu cũ có trường nào mã hoá (hệ cũ có tiện ích AES-256-GCM/RSA-OAEP) không | Sau go-live |
| 2 (sau go-live) | Policy quyền | Module policy chat: ánh xạ user → role → quyền (owner/moderator xoá tin người khác, giới hạn thời gian sửa/xoá theo tenant…), cắm vào `access.Policy` (M2b.0) thay `access.DefaultPolicy` (sửa/xoá chỉ tác giả, D86; loại tin khoá qua `Request.Kind`, D87) | Sau go-live |

## Thứ tự phụ thuộc

- M2b.0 → M2b.1 → M2b.2 → M2b.3 → M2b.4 → M2c → M3 → M3.1 → Mention App → M4 → PoC prod-like → M5 (owner 2026-10-19: Mention App giữ nhúng trong core, D177; PoC cần M4 để đo bão reconnect và cần hạ tầng; exit Phase 1 = mọi milestone Phase 1 `prod-like validated`, go-live sau khi có `identity`; owner 2026-10-17: Mention App trước M4; M4 định tuyến `MentionService` và đăng ký `notify.*` theo hợp đồng ở [events.md](events.md)). Chủ đề effect engine hoãn từ M3 đã giải quyết ở M3.1 (owner bỏ engine 2026-10-15); sharding đã đóng: core không shard, người triển khai tự chọn (D161, [open topics](plans/2026-10-15-m3-open-topics.md)).
- M2b.0 là điều kiện của mọi milestone sau: permission hook, reader pipeline và detector dùng chung.
- Từ M3.1, mọi tính năng có event mới cần: một bit `_state` ghi cùng lệnh, một đích xoá cờ (`publish.StateTargetsOf`) và một rebuilder của sweeper; dữ liệu dẫn xuất ghi bằng projection consumer trên `CHATIM_EVT` hoặc trong lệnh dưới phiếu hẹn.
- PoC prod-like chạy song song, xong trước M5 (chốt database). Storage nằm sau port nên PostgreSQL chỉ cần thêm adapter.
- Phase 2 bắt đầu khi M4 xong. Thứ tự Phase 2 (owner 2026-10-25, D207): PR M4 (đã merge, PR #19) → M6 identity → M7 media → M8 notification + push → M9 search người/nhóm → go-live `v1.0.0`; PoC prod-like và M5 chạy song song khi có hạ tầng; sau go-live: API cho hệ thống khác + webhook, policy quyền, tìm nội dung, migrator.

## Mục mang sang M5 (hardening)

Từ M6 phần 1, chi tiết ở [tóm tắt kỹ thuật](plans/2026-10-25-m6-1-dang-nhap-mat-khau-va-phien-summary.md#kết-quả-đã-xây-2026-10-27):

- e2e phase 2 (có từ trước M6) hỏng một lần trên dev: `corecli` gửi lại ngay sau `docker kill` core-1 gặp `grpc: the client connection is closing` (route client của `tools/internal/route` đang đóng kết nối cũ); các lần chạy sau qua.
Từ M6 phần 2, chi tiết ở [tóm tắt](plans/2026-10-27-m6-2-thu-hoi-toi-node-summary.md#12-kết-quả):

- Thu hồi hàng loạt bằng script làm body danh sách lớn (vài MB × 15 node mỗi 2s, Redis ghi cả khi 304): đo ở prod-like.
- Listener websocket mở trước khi đồng bộ `iam` xong (chưa `ready` nên LB chưa gửi, nhưng kết nối thẳng vẫn vào được).
- Thử hai replica `iam` cùng tự kích hoạt khoá và lệch nhịp nạp lại ở prod-like.
- Xoay `IAM_NODE_SECRET` (nhận hai secret cùng lúc) để phần 5.

- `iam`: chưa có histogram `hash_seconds`/`refresh_seconds`; chưa audit lần bị chặn theo IP/chờ và account lạ; thiếu itest giữ khoá tenant song song với tạo phiên; chưa có cách xoay KEK; Postgres chưa có HA, backup, alert (phần 5).

Sharding không phải việc của core (owner 2026-10-17, D161): core giữ quy tắc shard-ready, người triển khai tự chọn shard key theo nghiệp vụ; số đo 2 shard để tham khảo ở [2026-10-15-m3-open-topics.md](plans/2026-10-15-m3-open-topics.md) §2. Mục "effect engine chặn đầu hàng" đã giải quyết ở M3.1 (bỏ engine) nên không còn trong danh sách. Từ M3.1, các mục chỉ thuộc effect engine (reader, work stream, worker, ack mark, change feed) ở các milestone cũ đã được gỡ khỏi danh sách dưới.

Từ M3, tham khảo cho người triển khai (không làm trong core, D161): **sharding**. Thử trên 2 shard (bản vá thử không lưu lại): `members`, `message_interactions`, `mentions` có `_id` dài khác nhau nên shard theo `{_id: 1}` (design §5.1) không giữ một room/tin trên một shard, `ListMembers`/`GetReactions` hỏi mọi shard; hướng có thể: shard theo `{room_id: 1}` hoặc khoá dài cố định. Transaction owner (D100) vẫn chạy qua mongos. Từ M3.1 không còn change feed (điểm trễ ~10s qua mongos hết); sweeper đọc partial index `state_pending` sẽ hỏi mọi shard (index nhỏ, 2s một lần).

Từ M3.1, chi tiết ở [tóm tắt kỹ thuật](plans/2026-10-16-m3-1-state-driven-events-summary.md):

- Sweeper dựng lại event tuần tự, một lần đọc mỗi doc-bit: bắt kịp sau sự cố NATS dài có thể mất vài phút; diễn tập trên bản cuối ~1.600 doc/s (NATS chết ~2 phút 45 giây, cờ về 0 sau 3 phút 11 giây); gom đọc theo room nếu cần nhanh hơn.
- `projection_lag_seconds` đo theo `ts` nghiệp vụ, nên event do sweeper phát lại làm lag trông cao; event phát lại cũng nâng `last_change_at`/`activity_bucket` lên giờ hiện tại (thêm một lần nạp cache, resync quét thêm room).
- Consumer projection `DeliverAll`: đổi danh sách kind của một durable không backfill, khi đổi filter thì đổi tên durable.
- Consumer `projections` và sweeper chỉ chạy trên core giữ slot 0: thông lượng projection bị chặn bởi một core, danh sách hội thoại trễ tới ~3s (cửa sổ gom owner chọn 2026-10-17); một lô chờ handler chậm nhất, `reply_index` xử lý tuần tự (phần mention đã ra consumer riêng chia cho mọi core, nên trễ đỉnh ở bench 2K giảm từ 12,2s xuống 4,3s).
- Consumer phiếu hẹn xử lý tuần tự trong lô 64.
- Phiếu đặt trên `CHATIM_WORK` ngay trước deploy M3.1 không có người nhận (chưa go-live, chấp nhận); DB cũ còn collection `reconciler_state` (vô hại, xoá tay).
- `stateclear.Queue.Close` trả nil khi hết ngân sách xả (phần mất đã đếm, sweeper bù).
- Ghi tay vào DB phải tự bật `_state` + `_state_at` (kiến trúc §7.3).
- `stateclear`: `<-done` chờ mãi nếu `Clear` bỏ qua ctx; `Add` đi qua RWMutex; một chunk `BulkWrite` lỗi thì bỏ các chunk sau và đếm thừa `failures`. Luật boot không tính lần flush phải chờ sau một flush chậm.
- Một lô `TouchActivity` 8192 lệnh có thể vượt bước dừng của projections.
- `errDocGone`/`unknownBit` bọc `ErrInvalidArgument`, nên `eventkit.Gone` nuốt cả lỗi lập trình; sweeper chạy lại `Project` cho mỗi doc ghim.

Từ M3, chi tiết ở [tóm tắt kỹ thuật, mục 10](plans/2026-10-12-m3-core-query-apis-summary.md#10-rủi-ro):

- Chưa đo đợt kết nối lại hàng loạt và core lạnh sau deploy (PoC P7); ước lượng byte của entry cache đuôi dùng hằng.
- Resync: room chỉ đổi tên hoặc chỉ đổi cài đặt hội thoại trong khoảng mất cần `-room`; lần đổi tên đầu của room tạo trước M3 không phát lại.
- Tra `_id` 1.000 khoá rời: `$or` 1.000 nhánh, 61–90ms trên dev.
- Tin `system` không kiểm membership tới khi có mTLS; policy `send_system_message` nhận `Member` rỗng.
- Khoá singleflight của cache đuôi gồm `last_seq` (ít gộp hơn khi bão); `Watch.Stop` không đợi callback đang chạy; `preferences_event` của member đã rời vẫn phát event.

Từ M2b.0, chi tiết ở [tóm tắt kỹ thuật, mục Kết quả thực thi](plans/2026-10-05-m2b0-mechanism-foundations-summary.md#12-kết-quả-thực-thi-2026-10-05):

- `room_yields_total` có thể đếm thừa khi hai entry cùng hết lượt trong một group hoặc slot đã yêu cầu retire.
- Event còn trong hàng đợi khi publisher bị abort cưỡng bức không được đếm.
- Chưa có test trực tiếp cho counter `Malformed`.
- Probe oplog đọc primary hai lần mỗi lần scrape, không cache.
- `make vuln`: một cảnh báo cấp module GO-2026-5932 (`golang.org/x/crypto/openpgp`), có từ trước, code không gọi tới; xử lý khi nâng phụ thuộc.

Từ M2b.1, chi tiết ở [tóm tắt kỹ thuật, mục Kết quả thực thi](plans/2026-10-05-m2b1-effect-engine-summary.md#13-kết-quả-thực-thi):

- Resync dừng ở tin cũ đầu tiên (giả định `CreatedAt` tăng đơn điệu theo seq; migrator hoặc lệch đồng hồ có thể làm dừng sớm).

Từ M2b.2, chi tiết ở [tóm tắt kỹ thuật, mục Kết quả thực thi](plans/2026-10-05-m2b2-edit-delete-summary.md#13-kết-quả-thực-thi):

- Resync: `ErrEditPageFull` khi một thời điểm có ≥ 1000 fact sửa của một room (chỉ báo lỗi, chưa phân trang theo `_id`).
- Retry chỉ được nhận khi version hiện tại = `base+1`; tác giả sửa tiếp giữa lúc mất ack và lúc retry thì retry nhận `FAILED_PRECONDITION` (phạm vi D63).

Từ M2b.3, chi tiết ở [tóm tắt kỹ thuật, mục Kết quả thực thi](plans/2026-10-06-m2b3-reactions-pins-summary.md#13-kết-quả-thực-thi):

- Target nóng: mỗi touch đếm lại O(số reaction của tin) và các CAS `rx.v` tranh nhau; cửa sổ gom W + bucket `hash(u) % K` để milestone Channel (D90).
- `ReactMessage` không có cid: retry trễ có thể đưa reaction về trạng thái cũ (D89).
- Event reaction trung gian có thể không bao giờ được phát bù, chỉ trạng thái cuối được bảo đảm (D93).
- Resync: `ErrReactionPageFull`/`ErrPinPageFull` khi một thời điểm đầy một trang 1000 (như `ErrEditPageFull`); chọn room theo `activity_bucket` nên room chỉ có reaction/ghim trong khoảng mất cần `-room` (D91); `{room_id, updated_at}` của `reactions` không có prefix `_id` nên `Between` sẽ scatter-gather khi đã shard (D88).

Từ M2b.4, chi tiết ở [tóm tắt kỹ thuật, mục Kết quả thực thi](plans/2026-10-06-m2b4-members-read-summary.md#13-kết-quả-thực-thi):

- `memberwatch` dùng NATS core: event `member_removed` mất khi rớt kết nối không được phát lại; chỉ TTL 10s chặn (cả lúc router dừng).
- Lệch đồng hồ giữa core xử lý clear (`cleared_at`) và actor gán `ts` có thể để sót/ẩn thừa tin gửi sát lúc clear.
- Mongo `Hide`/`ClearHistory` nên cắt ms rõ ràng như memstore; `time.Now()` trong `read_position.go` không tiêm được.
- Transaction owner: abort/`EndSession` dùng `WithoutCancel` không hạn (nên thêm timeout); lỗi mạng/deadline không nhãn trả nguyên thay vì `ErrRetryLater`; query owner sắp trong RAM.
- `MembersBetween` quét mọi member của room rồi sắp (cần index `{room_id, last_change_at}` nếu resync room lớn chậm); resync bật lại bit vị trí đọc (bit 2, phát `read_positions`) cho mọi member vừa vào.
- `/app recount` vẫn ghi và tăng `member_count_ver` khi số đã đúng (`member_count_repair` thì không).
- `GetEditHistory` trang đầu có thể trả `limit+1` dòng (thêm dòng `ver 0`).
- Itest đua hiếm `TestReactionSetOfTheSameUserRacingOnANewReactionNeverFails` (Mongo `DurationOverflow`), theo dõi.
- Test còn thiếu: `Begin`/`Finish` song song của `dedupe.Requests`; nối `Config.Limits.MemberBatch` vào `Mutator`; `Load()` của `MEMBER_COUNT_CHECK_DELAY`; watch start trước gRPC.
- `dedupe.Requests`: ctx huỷ sau `Reserved` giữ khoá pending 10s; `Finish`/`Cancel` bỏ qua lỗi; chuỗi log dùng chung với cid.
- Itest transaction owner có thể treo (thiếu timeout); `AddMemberCount`/`SetMemberCount` báo lỗi khác nhau khi room id > MaxInt64; `Allow` nil gây panic; `ParseUint` nhận số 0 đứng đầu.

Từ M2c, chi tiết ở [tóm tắt kỹ thuật, mục 10](plans/2026-10-10-m2c-threads-mentions-summary.md#10-rủi-ro):

- `LockedMessageKinds` log dạng số.
- Sau va seq, các lệnh Duplicate khác trong nhóm vẫn chờ xác nhận tới hạn nhóm.
- Resync chỉ quét timeline chính; dedupe phiếu đếm lại theo room trong RAM.
- Chưa có itest đầu-cuối "`$inc` lỗi → phiếu bật sửa" và "chết giữa `AddReply` và `$inc`" (unit phủ).
- React như cũ vẫn hẹn + gỡ phiếu; mỗi phiếu sửa tốn một lần đọc đích mention; mỗi trả lời một lượt hẹn/ghi/`$inc`/phát tuần tự trong projection `reply_index` (đo thông lượng ở PoC prod-like).
- `domain.MsgKey` trùng hình dạng với `store.MsgKey`.
- `view.HideForViewer` so tin bị ẩn chỉ theo seq, bỏ thread (`apps/core/internal/message/view/masks.go`): đúng khi mọi timeline là thread 0, phải sửa trước khi có timeline thread ≠ 0 (kiến trúc, giới hạn đã biết).

Từ Mention App, chi tiết ở [tóm tắt kỹ thuật, mục 10](plans/2026-10-17-mention-app-summary.md):

- Test `slot` `TestStepCommandsIgnoreUnrelatedKeys` chập chờn khi chạy song song dưới tải.

Từ M4, chi tiết ở các tóm tắt phần 1–5 và phần gateway:

- **Chặn go-live:** quyền NATS riêng cho websocket (tới lúc đó ai vào được NATS là giả được gói đẩy tới người dùng, module 09 §8); mTLS giữa app với core (tin `system` không kiểm membership tới khi có mTLS).
- Kiểm `Origin` trên `GET /ws`: owner 2026-10-25 chọn allowlist chính xác `WS_ALLOWED_ORIGINS`, giống `GW_CORS_ORIGINS` của gateway. Chưa làm vì token đi trong frame, không phải cookie.
- Chưa đo nạp lại hàng loạt `WS_RELOAD_RATE` (PoC P12); `wsload` chưa có kịch bản nhảy.
- Alert websocket/gateway chưa có nhãn `guarantee` như alert của core.
- Owner 2026-10-22: giới hạn tốc độ lệnh chỉ theo kết nối (`WS_COMMAND_RATE`/`BURST`/`INFLIGHT`), không thêm theo user.
- Nợ code: alias `command.Outcome`/`CommandLatency` còn trong `apps/websocket/internal/command/executor.go`; `Forwarder.Commands()` chép lại mỗi request; thiếu test ghim `errWrongRequest`; hàm histogram lặp ở `interest` và `presence` (lần thứ ba thì chuyển vào `pkg/metrics`).

## Mức sẵn sàng

Theo [git-workflow.md](git-workflow.md#mức-sẵn-sàng): `dev-done` → `prod-like validated` → `go-live`. M0–M2a.3: `dev-done`, đã merge vào `main` (M2a.2 + M2a.3 cùng PR #11). M2b.0–M2b.4: `dev-done`, đã merge vào `main` (PR #12, 2026-10-08). M2c: `dev-done`, đã merge vào `main` (PR #14, 2026-10-13). M3 và M3.1: `dev-done`, đã merge vào `main` (PR #15, 2026-10-17). Mention App: `dev-done`, đã merge vào `main` (PR #16, 2026-10-17). Go-live còn cần: oplog `minRetentionHours` ≥ 24h, alert RC5 và lag, đo bộ nhớ NATS, đo lại trên prod-like. Chưa milestone nào `prod-like validated`; chưa có tag release.
