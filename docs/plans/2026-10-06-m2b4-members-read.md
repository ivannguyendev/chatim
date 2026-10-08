# M2b.4 — Member (lớp tập) + vị trí đọc — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: dùng `subagent-driven-development` (cùng phiên) hoặc `separate-driven-development` (phiên riêng) để thực thi plan này theo từng task. Implementer dùng skill `go-lang`. Plan này **không có code**: mỗi task mô tả mục tiêu, file, việc cần làm, kỹ thuật, test và lệnh; implementer tự viết code theo TDD.
>
> **Tóm tắt kỹ thuật cho owner (nguồn quyết định, thắng mọi chỗ khác khi lệch):** [2026-10-06-m2b4-members-read-summary.md](2026-10-06-m2b4-members-read-summary.md)

**Goal:** Trước hết đổi tên field của mọi collection đã xây (trừ `messages`) sang từ tiếng Anh đầy đủ, đổi tên field proto `version` sang `ver`, đổi clear history sang mốc thời gian `cleared_at`, và đổi `message_edits` sang "mỗi dòng một phiên bản nội dung" (dòng `ver 0` = nội dung lúc gửi). Sau đó làm member theo **lớp tập**: mỗi (room, user) một doc `members` clustered `_id = room│user` với `ver` riêng (chỉ tăng, có lỗ), rời/xoá để tombstone `state = 2`. `AddMembers` mang `request_id` (dedupe như cid). Lệnh đụng owner chạy trong **một transaction MongoDB**, một lần, tăng `rooms.owners_ver` làm điểm va chạm. `member_count` cộng/trừ bằng `$inc` ngay trong lệnh, theo số doc thực sự đổi trạng thái (không transaction, trừ đường owner); lệch thì sửa bằng `/app recount`. Vị trí đọc `read_seq/read_ver` chỉ cập nhật doc của người đọc (không đụng `ver`) và phát `read_updated`. **Mọi thay đổi đều phát event và được bảo đảm phát** (worker phát lại từ change feed): mỗi thay đổi một event trên subject **theo loại dữ liệu** (`room`, `member`, `message`); core không quyết định ai nhận — chuyển tới user nào, bao nhiêu user/room là việc của app phân phối thiết kế sau.

**Architecture (mỗi package một dòng):**
- `proto/chatim/v1`: đổi tên `version`→`ver` (Task 1); `members.proto` mới, 7 RPC, payload 28–35 (Task 3).
- `pkg/keys`: `Member(room, user)` / `ParseMember` (Task 2).
- `pkg/lru`: LRU generic chuyển từ `actor` (Task 7).
- `apps/core/internal/domain`: `RoleAdmin`, `MemberState`, field member mới, `Join`, `MemberCount`, `ReadPosition`, `EditOriginal`, lỗi member (Task 1, 2, 12).
- `apps/core/internal/store`: port `MemberWriter`, `MemberReader`, `OwnerChanges` (transaction), `MemberCounts` (`$inc` + recount), `ReadPositions`; `ChangeKind` `MemberChanged` (6), `ReadChanged` (7) (Task 4).
- `store/memstore`, `store/storetest`: cài và hợp đồng của 5 port, kể cả ngữ nghĩa transaction (Task 4, 6).
- `store/mongostore`: tên field mới (Task 1); `members` clustered, `BulkWrite` upsert pipeline, transaction đổi owner (Task 5); feed member (Task 6).
- `apps/core/internal/work` + `reconcile`: record `MemberChanged` (id `g:`), `ReadChanged` (id `d:`), `MessageHidden`, `HistoryCleared`, đuôi user (Task 4, 6); `work.Timers` hẹn phiếu `MemberCountCheck` bằng message schedule của NATS (Task 6).
- `apps/core/internal/pbconv` + `publish`: id/event member, số member, vị trí đọc; subject theo loại dữ liệu `evt.{t}.{room|member|message}.{rid}.{kind}` cho **mọi** event, kể cả event đã có; RePublish `evt.*.*.*.*` (Task 3).
- `apps/core/internal/dedupe`: `Space`, `RequestKey`, `Requests` trên namespace `chatim:req:` (Task 7).
- `apps/core/internal/actor`: cache member theo thế hệ + TTL 10s, `Router.ForgetMembers` (Task 8).
- `apps/core/internal/memberwatch` (mới): mọi core nghe event member trên `live.*` và quên cache member của room (Task 16).
- `apps/core/internal/access`: 6 action mới, `Request.Target/Role`, `DefaultPolicy` (Task 9).
- `apps/core/internal/ownership` (mới): luật owner thuần (kế nhiệm, owner cuối, lập kế hoạch ghi) chạy bên trong transaction (Task 10).
- `apps/core/internal/mutate`: 5 lệnh member (thêm `SetMemberPriority`) + `$inc` số member có phiếu hẹn đếm lại (Task 11), event ẩn tin và xoá lịch sử, `MarkRead`/`MarkUnread` (Task 13), clear history theo thời gian và dòng `ver 0` (Task 1).
- `apps/core/internal/grpcsrv`: `CreateRoom` phát `member_added` cho người tạo (Task 12), 7 RPC (Task 14).
- `apps/core/internal/config`: `MEMBER_BATCH_MAX`, `MEMBER_COUNT_CHECK_DELAY` (Task 14).
- `apps/core/internal/effects`: `member_event`, `member_count_event`, `read_event`, `hidden_event`, `history_cleared_event`, `member_count_repair`, bỏ qua dòng `ver 0` ở `edit_projection`/`msg_changed` (Task 1, 15).
- `apps/core/internal/resync` + `apps/core`: quét doc `members` theo `last_change_at` và `hidden` theo `created_at`; subcommand `/app recount` (Task 17).
- `tools/internal/route`, `tools/corecli`, `scripts/e2e.sh`: lệnh member/đọc, phase e2e 5 (Task 18).

**Tech Stack:** Go 1.26 trong Docker qua `make`; buf; mongo-driver v2 (clustered collection, `BulkWrite` upsert pipeline `$cond`, `UpdateOne`/`FindOneAndUpdate` có điều kiện, session causal, transaction một lần `StartTransaction`/`CommitTransaction`, change stream lọc `updatedFields.ver` / `updatedFields.read_ver`); go-redis (Lua dedupe có sẵn); nats.go jetstream (RePublish); `testing/synctest`; goleak.

**Nguồn:** [thiết kế](../designs/261005-chatim-architecture.md) §4, §5, §5.1, §6, §7, §8, §9, §12, §17.2; [roadmap](../roadmap.md) dòng M2b.4; bản tóm tắt ở trên.

## Quyết định (D96–D110)

**D96–D107 của plan cũ (commit `a560214`, chưa từng thực thi, chưa vào Decision Log) bị huỷ toàn bộ.** Bản `0698fbf` dùng D96–D108 cho thiết kế có `pending_owner_change`, `owner_guard`, `readcast`; các phần đó cũng bị huỷ. Số id dùng lại từ D96 với nghĩa dưới đây; D108 nay là subject theo loại dữ liệu. Các dòng không số ở bảng §6 của bản tóm tắt được ghi vào D gần nhất (chỉ ra ở từng dòng).

- **D96** Tên field đầy đủ cho mọi collection trừ `messages` (bảng đổi tên ở dưới); counter đuôi `_ver`; audit `updated_at/updated_by`; fact `created_at/created_by`; `reconciler_state {resume_token, cluster_time}`; bỏ đường đọc vị trí feed cũ `_id: "messages"`. Field proto đã có đổi tên `version`→`ver`, `base_version`→`base_ver`, `after_version`→`after_ver`, `pin_version`→`pin_ver` (giữ số field). Ghim giữ đánh số liên tục `pin_ver` (dòng không số). `message_edits` mỗi dòng là một phiên bản nội dung: chỉ `text` (cùng `kind`, `created_by`, `created_at`), theo `ver`; dòng `ver 0` = nội dung lúc gửi, ghi ở lần **sửa** đầu (insert-if-absent; xoá đầu tiên không ghi dòng gốc vì text bị xoá ngay); bỏ `previous_text`/`p` và `domain.Edit.Prev` (dòng không số; sửa D75 phần `p`). Không có đường nâng cấp tại chỗ.
- **D97** Clear history theo thời gian: `members.cleared_at = max(cũ, giờ server lúc nhận lệnh)` (ms), ẩn tin có `ts ≤ cleared_at` trên mọi timeline với riêng người đó; chỉ member active. `ClearHistoryRequest.up_to_seq` reserved; response trả `cleared_at`. Sửa D72/D85 phần `cleared_before_seq`.
- **D98** Member lớp tập: `members` clustered `_id = keys.Member(room, user)`; `state` 1/2 luôn ghi rõ, tombstone không bao giờ xoá; `ver` (≤ MaxUint32) tăng mỗi lần đổi membership (vào, rời, vào lại, đổi role, đổi `priority`); `priority` (int32, mặc định 0, app đặt bằng `SetMemberPriority`, vào lại đặt về 0); `previous_role/previous_state/previous_priority`, `request_id`, `updated_at/updated_by`; `last_change_at` = lúc doc đổi lần cuối vì bất kỳ lý do gì (membership, đọc, clear; dùng cho resync); đọc/clear không đụng `ver`. Index `{room_id: 1, state: 1, role: 1, priority: -1, joined_at: 1, user_id: 1}` và `{tenant, user_id, state, room_id}`. Không có collection `user_rooms`: "các room của user" là index thứ hai (dòng không số). Không quét khoảng `_id` của `members`.
- **D99** `AddMembers`: `request_id` bắt buộc, dedupe `chatim:req:{room}:{user}:{request_id}` (RAM LRU + Redis dedupe, TTL `CID_COMMITTED_TTL`); một `BulkWrite(ordered:false)` upsert pipeline `$cond` (doc active giữ nguyên, không oplog); kết quả suy từ doc sau ghi (`domain.AddedBy`); người vào lại luôn role `member`; `read_seq = max(cũ, seq cuối)`; giữ `cleared_at`. Giới hạn: chỉ trong 15 phút và khi Redis còn khoá.
- **D100** Lệnh đụng owner (owner rời, xoá owner, hạ owner, nâng lên owner) chạy trong **một transaction MongoDB**: đọc owner và các doc, quyết định bằng hàm thuần (`ownership.Plan`), nâng người kế nhiệm trước rồi hạ/cho rời, `$inc rooms.owners_ver` (điểm va chạm chống write skew), commit. Chạy đúng một lần bằng `StartTransaction`/`CommitTransaction` (không `WithTransaction`); va chạm → `ErrRetryLater` (`UNAVAILABLE`). memstore cho cùng ngữ nghĩa. Đây là **ngoại lệ duy nhất** của §5.1. Lệnh thường (xoá/rời/đổi role không đụng owner) là một update có điều kiện `ver`; trượt → `ErrRetryLater` ngay. **Không có vòng thử lại bên trong core** ở mọi code mới (dòng không số). Không `pending_owner_change`, không `owner_guard`.
- **D101** Quyền mặc định: owner làm mọi việc; admin thêm người và xoá người có role `member`; chỉ owner đổi role; ai cũng tự rời. DM cố định (`FAILED_PRECONDITION`). Policy được hỏi **trước** khi lộ việc đích có tồn tại hay không, với role giả `member` khi đích không có doc **hoặc** đã rời (tombstone); xoá người đã rời là thành công không làm gì, kể cả khi họ từng là admin. Chỉ owner đặt `priority` (action `set_member_priority`). Bảng mã lỗi ở "Hợp đồng chung → mutate".
- **D102** `member_count` đổi bằng `$inc {member_count: n, member_count_ver: 1}` (một `FindOneAndUpdate` trên `rooms`, trả doc sau ghi) ngay trong lệnh member, với `n` = số doc **thực sự đổi trạng thái** (thêm: `UpsertedCount + ModifiedCount` của `BulkWrite`; xoá/rời doc active: −1; đổi role/priority: 0). Không transaction; riêng đường owner, `$inc` nằm trong transaction sẵn có. **Phiếu hẹn đếm lại** (phiếu hẹn sinh tồn, cơ chế chung D111 / thiết kế §7.1, owner chốt 2026-10-07): lệnh có thể đổi số (thêm; xoá/rời đường thường) **trước** khi ghi member hẹn một message schedule của NATS (`WithScheduleAt(now + MEMBER_COUNT_CHECK_DELAY)`, mặc định 5s, phải > `CORE_REQUEST_DEADLINE`) trên subject `work.timer.{room}.{op}` của `CHATIM_WORK`, target `work.p{slot % 32}`, payload record `MemberCountCheck` (10) id `k:{room}-{op}`; hẹn lỗi → `ErrRetryLater`, chưa ghi gì. Ghi member → `$inc` → xoá phiếu (`DeleteMsg` theo seq của PubAck). Lỗi hay core chết ở giữa → phiếu bật, worker `member_count_repair` đếm lại (phủ index `{room_id, state}`); bằng số đang lưu → chỉ phát lại event; khác → hẹn phiếu mới **trước**, rồi ghi có điều kiện `member_count_ver`; trượt CAS → Nak (sửa sau review Task 15: bỏ bước đọc lại). Xoá phiếu lỗi → phiếu bật, đếm lại vô hại. Sửa đếm vào `counter_repaired_total{counter="members"}` (alert `ChatimCounterRepairSurge` sẵn có; alert giữ 16 luật). `Rooms.Create` ghi `member_count` = số người lúc tạo, `member_count_ver = 1`. Event `member_count_changed` id `{room}-members-v{member_count_ver}`; worker `member_count_event` phát lại số hiện tại. `/app recount` giữ cho vận hành tay. Phương án bị loại: Redis set; worker đếm lại sau mỗi đổi; `$inc` trong transaction; số dự kiến trên Redis; cờ "cần đếm lại" (không bắt được core chết). Sửa D67 cho `member_count`.
- **D103** Feed thêm insert/replace của `members`, update có `updatedFields.ver` (kind `MemberChanged` (6), version = `ver`, record id `g:{room}-mb-{user}-v{ver}`) và update có `updatedFields.read_ver` mà không có `ver` (kind `ReadChanged` (7), version = `read_ver`, record id `d:{room}-rd-{user}-v{read_ver}`); cả hai có đuôi user. Registry `MemberChanged → room_activity (0) → member_event (RECONCILE_DELAY) → member_count_event (RECONCILE_DELAY)`; `ReadChanged → read_event (RECONCILE_DELAY)`. `cleared_at` vẫn không vào feed. Resync quét doc member theo `updated_at` (cho cả hai kind).
- **D104** Mỗi thay đổi doc member phát **một** event `member_added`/`member_removed`/`member_role_changed` id `{room}-mb-{user}-v{ver}` trên subject `evt.{t}.member.{rid}.{kind}`, kể cả doc tạo cùng room. Core không có subject user, không `recipient`: chuyển event tới ai (user, bao nhiêu user/room, notification) là việc của app phân phối thiết kế sau; payload mang `user` để app đó định tuyến. Payload không có `new_owner`; không ack mark. Bảo đảm phát: fast path + worker `member_event` (bản của `ver` hiện tại; bản trung gian bị vượt có thể mất, chỉ trạng thái cuối được bảo đảm — như reaction, D93).
- **D105** Vị trí đọc: `MarkRead`/`MarkUnread` chỉ cập nhật doc của người đọc (`read_seq`, `read_ver`) bằng toán tử thường, không đụng `ver`; mỗi lần vị trí thực sự đổi phát **một** `read_updated` id `{room}-rd-{user}-v{read_ver}` trên subject `member` (vị trí đọc nằm trên doc member). Bảo đảm phát: update có `read_ver` vào feed (kind `ReadChanged`), worker `read_event` phát lại bản của `read_ver` hiện tại (trạng thái cuối). Ai nhận "đã đọc/đã xem" do app phân phối quyết định sau; không `READ_RECEIPT_*`, không bước dừng mới (kế hoạch dừng giữ 26.2s). Chi phí: mỗi lần đổi vị trí đọc thêm một record work stream (cần số thật lượt đọc/s trước khi định cỡ).
- **D108** Subject theo loại dữ liệu (owner chốt 2026-10-07): `evt.{t}.room.{rid}.{kind}` cho dữ liệu của room (`room_created`, `msg_pinned`/`msg_unpinned` vì danh sách ghim nằm trên doc room, `member_count_changed` vì số member là field của room); `evt.{t}.member.{rid}.{kind}` cho dữ liệu riêng của member (`member_added`, `member_removed`, `member_role_changed`, `member_priority_changed`, `read_updated`, `message_hidden`, `history_cleared`); `evt.{t}.message.{rid}.{kind}` cho tin (`msg_created`, `msg_edited`, `msg_deleted`, `reaction_changed`, `counts_changed`). Cùng 5 token nên một luật RePublish `evt.*.*.*.*` → `live.{1}.{2}.{3}.evt.{4}`; muốn mọi event của một room thì sub `live.{t}.*.{rid}.>`. Đổi subject của event đã có (trước đây mọi event ở `…room…`); chưa go-live nên không cần chuyển tiếp. Sửa D49/§11 phần subject.
- **D109** Ẩn tin và xoá lịch sử phát event (owner chốt 2026-10-07: ghi mọi event, app khác có thể cần làm log), trên subject `member`: `message_hidden` id `{room}-hd-{user}-{thread}-{seq}`, `history_cleared` id `{room}-cl-{user}-{cleared_at unix ms}`. Bảo đảm phát: insert vào `hidden` → kind `MessageHidden` (8), record id `h:` + event id; update `members` chỉ có `cleared_at` (không `ver`, không `read_ver`) → kind `HistoryCleared` (9), record id `c:{room}-cl-{user}-{CommittedAt unix ms}`; worker `hidden_event`, `history_cleared_event` phát lại trạng thái hiện tại. `hidden` thêm `created_at` (`$setOnInsert`) và index `{room_id, created_at}` cho resync. Sửa D85 (trước đây ẩn/clear không có event).
- **D110** Quên cache member xuyên core: mỗi core nghe `{live}.*.member.*.evt.member_removed` và `….member_role_changed` bằng NATS core (không JetStream), lấy room id từ token thứ 4 của subject và gọi `Router.ForgetMembers(room)`; TTL 10s (D106) giữ làm chốt chặn khi event chậm hoặc mất. Package `memberwatch`.
- **D106** Cache member của actor: thế hệ (`Router.ForgetMembers`, quên ngay trên core xử lý lệnh) + TTL 10s (giới hạn xuyên core). Không cache "không phải member".
- **D107** `MEMBER_BATCH_MAX` (mặc định 500, hợp lệ 2..1000) cho `CreateRoom` và `AddMembers`; bỏ trần 5000 trong `domain.NewRoom`; core không giới hạn tổng số member.

---

## Quy tắc chung (đọc trước khi làm task nào)

- **Không chạy `go` trên host.** Test một package: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/<pkg>/..."`. Tra API: `make -s go ARGS="doc <pkg> <Symbol>"`.
- **Không viết comment** trong Go, test, YAML, shell, Makefile, proto mới (`pkg/pb` sinh ra được miễn).
- File code dưới 200 dòng; chạy `wc -l` sau mỗi lần sửa file lớn. File đang sát trần được liệt kê ở "Ghi chú tích hợp".
- `gosec` G115: không chuyển `int`/`int64` ↔ `uint*` khi chưa chặn biên. Mongo lưu `ver`, `read_seq`, `read_ver`, `member_count_ver`, `owners_ver`, `room_id` dạng int64; số âm hoặc vượt kiểu Go khi đọc → `errCorrupt`; ghi giá trị vượt (room id > MaxInt64, `ver` > MaxUint32) → `ErrInvalidArgument`.
- **TDD:** mỗi task viết test trước, chạy thấy fail đúng lý do, viết code, chạy thấy pass.
- **Trước mỗi commit:** `make fmt-check`, `make vet`, `make lint` và test các package đã đụng. Goroutine, channel hoặc lock mới: thêm `-count=5` trên package đó.
- **`make itest`** khi đổi adapter Mongo/Redis/NATS hoặc wiring `apps/core` (cần `make infra-up`), một lần cuối task. `make itest` không nhận `ARGS`; muốn chạy một itest riêng, tạo (không commit) file `<scratchpad>/itest-one.mk` gồm một dòng `include Makefile` và target `itest-one: check-env` có lệnh y hệt target `itest` trong `Makefile` nhưng thay đoạn `go test … ./...` bằng `go test -race -count=1 -run '$(RUN)' $(PKG)`; chạy từ gốc repo `make -f <scratchpad>/itest-one.mk itest-one RUN=TestX PKG=./apps/core/...`.
- **Reset dữ liệu dev:** sau commit Task 1 và sau commit Task 5 chạy `make infra-reset` rồi `make infra-up` trước `make itest`/`core-up` (tên field đổi; `members` chuyển sang clustered nên `Bootstrap` báo `ErrNotClustered` trên collection cũ). Core đang chạy phải build lại image. Itest dùng database `chatim_it_*` mới mỗi lần nên không phụ thuộc reset; reset là cho database `chatim` của `core-up`/e2e.
- **`INDEXES.csv`:** field có dấu phẩy (thường `purpose`, `key_symbols`, `decisions`) phải nằm trong ngoặc kép. Sau mỗi lần sửa chạy `python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"` → phải in `{7}`. Cột `decisions` thêm đúng D của task. Trước task đầu kiểm `git status --short INDEXES.csv` rỗng; có thay đổi của owner → dừng và hỏi controller.
- **Commit:** Conventional Commits, không nhắc AI, **không** dòng `Co-Authored-By`. File mới `git add <đúng file>`; commit bằng `git commit -m "<message>" -- <mọi path task sửa, tạo hoặc xoá>`. Cấm `git add -A`, `git add .`, `git commit -a`, `git stash`. Sau commit, `git show --stat HEAD` chỉ được có file của task. Nhánh `feat/m2b`.
- **Push:** sau Task 6, sau Task 14, cuối Task 20 (`git push origin feat/m2b`).
- **Review:** task đánh dấu ★ (1, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16) có **một** reviewer: kiểm spec + chất lượng một lượt trên diff, không chạy lại suite đã xanh, tối đa `-count=3` trên package đụng. Task khác controller kiểm nhanh. Critical/Important sửa trong một commit tiếp; re-review chỉ sau Critical và chỉ diff đó. Minor ghi vào mục "Kết quả thực thi" cuối plan.
- **Dừng và báo cáo** khi một bước cho kết quả khác "Done khi", khi test cũ fail không thuộc phạm vi task, hoặc khi tên trong code thật khác hợp đồng mà không phải lỗi cơ học. Không vá cho qua.
- **Sửa cơ học được phép** (import thiếu, tên biến trùng, kiểu trả về lệch một chữ, đổi tên getter proto do Task 1, idiom lint có hành vi y hệt) và **phải ghi vào báo cáo** của task.

## Bảng đổi tên field (Task 1)

`messages` **giữ nguyên** tên ngắn: `_id`, `t` (tenant), `f` (người gửi), `k` (loại tin), `x` (text), `c` (cid), `ts` (lúc gửi), `v` (version sửa), `d` (đã xoá), `ea` (lúc sửa cuối), `rx` (số reaction `{c: [{e, n}], v}`). Tên Go (struct domain, field Go) không đổi, trừ `Member.ClearedBeforeSeq` → `ClearedAt` và việc bỏ `Edit.Prev`. Giá trị `_id` không đổi. Không id event/record nào đổi (đuôi `-n{n}` của reaction là id, không phải field).

| Collection | Cũ → mới |
|---|---|
| `rooms` | `t`→`tenant`, `ty`→`type`, `n`→`name`, `cb`→`created_by`, `ca`→`created_at`, `mc`→`member_count`, `ls`→`last_seq`, `lm`→`last_message_at`, `lc`→`last_change_at`, `ab`→`activity_bucket`, `pv`→`pin_ver`; `pins[]` giữ tên mảng, phần tử `{th, s, by, ts, pv}` → `{thread_root, seq, pinned_by, pinned_at, pin_ver}` |
| `members` | `r`→`room_id`, `u`→`user_id`, `t`→`tenant`, `ro`→`role`, `ja`→`joined_at`, `cb` (int64 seq) → `cleared_at` (date) |
| `message_edits` | `r`→`room_id`, `t`→`tenant`, `k`→`kind`, `by`→`created_by`, `x`→`text`, `ts`→`created_at`; **bỏ** `p` (không có `previous_text`) |
| `hidden` | `u`→`user_id`, `r`→`room_id`, `th`→`thread_root`, `s`→`seq` |
| `reactions` | `k`→`message_key`, `r`→`room_id`, `t`→`tenant`, `u`→`user_id`, `e`→`emoji`, `pe`→`previous_emoji`, `n`→`ver`, `ts`→`updated_at` |
| `pin_actions` | `r`→`room_id`, `t`→`tenant`, `op`→`action`, `th`→`thread_root`, `s`→`seq`, `by`→`created_by`, `ts`→`created_at` |
| `reconciler_state` | `token`→`resume_token`, `at`→`cluster_time` (doc `_id: "changes"`); bỏ đọc/xoá doc cũ `_id: "messages"` |

Field mới: `hidden` thêm `created_at` (Task 1); `members` thêm `state`, `ver`, `priority`, `previous_role`, `previous_state`, `previous_priority`, `request_id`, `updated_at`, `updated_by`, `last_change_at`, `read_seq`, `read_ver` (Task 5); `rooms` thêm `member_count_ver`, `owners_ver`.

Index (tên tự sinh theo khoá; `Bootstrap` tạo mới sau reset):

| Collection | Cũ → mới |
|---|---|
| `rooms` | `{ab:1}` → `{activity_bucket:1}`; `{ca:1}` → `{created_at:1}` |
| `members` | Task 1: `{r:1,u:1}` unique → `{room_id:1,user_id:1}` unique; `{t:1,u:1,r:1}` → `{tenant:1,user_id:1,room_id:1}`. Task 5 thay cả hai bằng `{room_id:1,state:1,role:1,priority:-1,joined_at:1,user_id:1}` và `{tenant:1,user_id:1,state:1,room_id:1}` |
| `message_edits`, `pin_actions` | `{r:1,ts:1}` → `{room_id:1,created_at:1}` (`roomTimeIndexes`) |
| `reactions` | `{k:1,e:1}` → `{message_key:1,emoji:1}`; `{r:1,ts:1}` → `{room_id:1,updated_at:1}` (hàm riêng, không dùng `roomTimeIndexes`) |
| `hidden` | `{u,r,th,s}` unique → `{user_id:1,room_id:1,thread_root:1,seq:1}` unique; thêm `{room_id:1,created_at:1}` (field mới `created_at`, D109) |

Chỗ dùng tên thô ngoài codec (đổi cùng, trong `apps/core/internal/store/mongostore/`): `rooms.go` (projection loại `pins`, `pin_ver`; filter member; `$max cleared_at`), `pin_state.go` (projection + CAS `pin_ver`), `room_activity.go` (`$max last_change_at/activity_bucket/last_seq/last_message_at`; `ActiveRooms` lọc `activity_bucket`, `created_at`, `tenant`), `edits.go` (`Between` lọc/sắp `room_id, created_at`; `PurgeText` `$unset text`), `pins.go` (`Between`), `hidden.go`, `reactions.go` (pipeline `$cond` trên `emoji`, `$ifNull ver`), `reaction_count.go` (witness projection `{user_id, ver}`, aggregate `$match {message_key, emoji: {$gt: ""}}`, `$group` theo `$emoji`, `Between` theo `updated_at`), `feed.go` (vị trí feed), `feed_reaction_change.go` (**`updatedFields.n` → `updatedFields.ver`**), `bootstrap.go` (`$ifNull ["$cluster_time", …]`). Test có tên thô: `feed_anchor_integration_test.go`, `feed_change_test.go`, `feed_reaction_change_test.go`, `bootstrap*_integration_test.go`, `reactions_test.go`, các `*_codec_test.go`.

**Đổi tên field proto (Task 1, giữ số field, không thêm `reserved`):**
- `core.proto`: `Message.version` → `ver`; `MessageVersion.version` → `ver`; `EditMessageRequest.base_version`, `DeleteMessageRequest.base_version` → `base_ver`; `GetEditHistoryRequest.after_version` → `after_ver`. Tên message `MessageVersion` và field `GetEditHistoryResponse.versions` **giữ nguyên** (không phải counter).
- `events.proto`: `MessageEdited.version`, `MessageDeleted.version` → `ver`; `MessagePinned.pin_version`, `MessageUnpinned.pin_version` → `pin_ver`.
- `reactions_pins.proto`: `ReactionSummary.version` → `ver`; `Pin.pin_version`, `PinMessageResponse.pin_version`, `UnpinMessageResponse.pin_version` → `pin_ver`.
- `core.proto` clear history: `ClearHistoryRequest` giữ `room_id = 1`, `reserved 2` và `reserved "up_to_seq"`; `ClearHistoryResponse` có `reserved 1`, `reserved "cleared_before_seq"`, thêm `google.protobuf.Timestamp cleared_at = 2`.
- Wire tương thích (số field giữ); tên JSON và getter Go đổi (`GetVersion` → `GetVer`, `BaseVersion` → `BaseVer`, `AfterVersion` → `AfterVer`, `PinVersion` → `PinVer`), nên sửa mọi chỗ gọi trong `apps/`, `tools/`, test. Tên Go nội bộ (`domain.Message.Version`, `mutate.EditCmd.BaseVersion`…) giữ nguyên.

---

## Hợp đồng chung (mọi task phải khớp đúng tên này)

Tên dưới đây là hợp đồng giữa các task. Field mới của struct đã có thêm **cuối** struct trừ khi ghi khác. Task sau không đổi chữ ký của task trước; cần đổi thì dừng và báo controller.

### `domain` (Task 1, 2; trần Task 12)

| Tên | Hợp đồng |
|---|---|
| `Member.ClearedAt time.Time` (Task 1) | thay `ClearedBeforeSeq`; zero = chưa clear |
| `EditOriginal EditKind` (Task 1) | giá trị thứ ba sau `EditText`, `EditDelete`; chỉ dòng `ver 0` mang kind này |
| `Edit` (Task 1) | bỏ field `Prev` |
| `RoleAdmin Role = "admin"` | cạnh `RoleOwner`, `RoleMember` |
| `MemberState` (int32), `MemberActive = 1`, `MemberRemoved = 2` | |
| `Member` thêm `State`, `Ver uint32`, `Priority int32`, `PreviousRole`, `PreviousState`, `PreviousPriority int32`, `RequestID`, `UpdatedAt`, `UpdatedBy`, `LastChangeAt`, `ReadSeq`, `ReadVer uint64` | `Member` vẫn so sánh được bằng `==` |
| `Room.MemberCountVer uint64` | 1 lúc tạo; +1 mỗi lần `member_count` đổi; 0 = room cũ chưa có field |
| `Join{Room uint64; Tenant, RequestID, By string; At time.Time; ReadSeq uint64}` | tham số chung của một lệnh thêm |
| `MemberCount{Count int; Ver uint64}`, `ReadPosition{Seq, Ver uint64}` | |
| `MaxMemberBatch = 1000` | trần cứng của `MEMBER_BATCH_MAX` |
| `ErrDirectRoom` (FailedPrecondition), `ErrLastOwner` (FailedPrecondition), `ErrMemberNotFound` (NotFound), `ErrTooManyMembers` (InvalidArgument) | bọc `apperr` như lỗi domain cũ |
| `ParseRole(s) (Role, error)` | `owner|admin|member`, khác → `invalid("role")` |
| `CreationRequestID(room) string` | `"{room thập phân}-created"`, hợp lệ theo `ValidCID` |
| `(Member) Active() bool` | `State == MemberActive` |
| `(Member) Next(role, state, priority, requestID, by, at) Member` | bản sao với role/state/priority mới, `PreviousRole/PreviousState/PreviousPriority` = giá trị cũ, `Ver + 1`, `RequestID`, `UpdatedBy`, `UpdatedAt`, `LastChangeAt = at`; giữ `JoinedAt`, `ClearedAt`, `ReadSeq`, `ReadVer` |
| `(Join) Apply(cur Member, user string) Member` | `cur` active → trả `cur` y nguyên. Không thì doc active, role `member`, `JoinedAt = At`, `Ver = cur.Ver+1`, `Previous* = cur.*`, `RequestID`, `UpdatedBy = By`, `UpdatedAt = At`, `LastChangeAt = At`, `Priority = 0`, `ReadSeq = max(cur.ReadSeq, j.ReadSeq)`, `ReadVer = cur.ReadVer+1`, giữ `ClearedAt` |
| `AddedBy(m, requestID, by) bool` | `m.Active() && m.RequestID == requestID && m.UpdatedBy == by` |
| `NewRoom` (Task 2) | mỗi member có `State: MemberActive, Ver: 1, RequestID: CreationRequestID(id), UpdatedAt: now, UpdatedBy: creator`; Task 12 bỏ trần 5000 (DM vẫn đúng 2, group ≥ 1) |

`Next` và `Join.Apply` là ngữ nghĩa chuẩn; memstore và Mongo viết lại đúng như vậy.

### `pkg/keys` (Task 2)

- `MaxMemberUser = 64`; `Member(room, user) []byte` = room 8 byte big-endian nối byte của user (giống `keys.Reaction`); `ParseMember(b)` nhận `8 < len ≤ 8 + MaxMemberUser`, không thì `ErrLength`.
- **Không quét khoảng `_id` của `members`:** Mongo so BinData theo độ dài trước rồi mới tới byte, nên khoảng byte không phải khoảng tiền tố room. Truy vấn theo room đi qua index `room_id`; theo người dùng `_id` bằng hoặc `$in`.

### `store` (Task 1 edits; port Task 4; Mongo Task 5; feed Task 6)

- `ValidateEdit` (Task 1): `Version ≤ MaxInt32`; `Version == 0` ⇔ `Kind == EditOriginal`; kind ∈ {text, delete, original}. `Edits.Append` của dòng `ver 0` trùng khoá trả `ErrEditExists` như mọi dòng (người gọi coi là đã có). `Edits.Latest` và `Edits.History(after)` chỉ trả dòng `ver ≥ 1` (như cũ: khoảng `> after`). `Edits.At(key, 0)` đọc được dòng `ver 0`. `Edits.PurgeText(key, upTo)` xoá `text` của các dòng `0..upTo` (kể cả `upTo == 0`).
- `HistoryClearer.ClearHistory(ctx, room, user, at time.Time) (time.Time, error)` (Task 1): `$max cleared_at = at`, trả giá trị sau ghi; không có doc → `domain.ErrNotMember`. Task 5 thêm điều kiện `state: 1` (Task 4 cho memstore).
- `MaxMemberScan = 1000`; `MemberChanged ChangeKind = 6`; `ReadChanged ChangeKind = 7`; `MessageHidden ChangeKind = 8` (`Change.Hidden` = user, thread, seq); `HistoryCleared ChangeKind = 9` (`Change.Member` có `Room`, `User`); `MemberCountCheck ChangeKind = 10` (không bao giờ đến từ feed, chỉ từ phiếu hẹn); `Change.Member domain.Member` (ngay sau `Pin`; với `ReadChanged` chỉ có `Room`, `User`, `ReadVer`).
- Port mới (file `store/member.go`):

| Port | Method | Hợp đồng | Write contract |
|---|---|---|---|
| `MemberWriter` | `AddMembers(ctx, j domain.Join, users []string) (store.JoinResult, error)` | mỗi user ghi `j.Apply(cur, user)`; doc active không ghi gì; `JoinResult{Members []domain.Member; Changed int}`: doc **sau ghi** theo thứ tự `users`, `Changed` = số doc lần ghi này thực sự tạo hoặc đổi (Mongo: `UpsertedCount + ModifiedCount`); không đụng `rooms` | `version-bump` |
| | `ApplyMember(ctx, cur, next domain.Member) (bool, error)` | `ValidateMemberChange`; ghi khi doc vẫn có `ver == cur.Ver` → `true`; trượt hoặc không có doc → `false, nil`; chỉ ghi các field `Next` đổi | `cas` |
| `MemberReader` | `MembersOf(ctx, room, users []string) ([]domain.Member, error)` | doc mọi state, theo thứ tự `users`; user không có doc bị bỏ; 1..`MaxMemberBatch+1` user | `read` |
| | `MembersBetween(ctx, room, from, to time.Time, limit int) ([]domain.Member, error)` | doc mọi state có `last_change_at ∈ [from, to]`, sắp `last_change_at` rồi `_id`; limit 1..`MaxMemberScan` | `read` |
| `OwnerChanges` | `ChangeOwners(ctx, room uint64, users []string, decide OwnerDecision) ([]domain.Member, error)` | xem dưới | `transaction` (kind mới) |
| `MemberCounts` | `AddMemberCount(ctx, room, delta int) (domain.MemberCount, error)` | `delta ≠ 0` (0 → `ErrInvalidArgument`); `$inc member_count delta, member_count_ver 1`, trả giá trị sau ghi; room không có → `ErrRoomNotFound` | `version-bump` |
| | `CountMembers(ctx, room) (int, error)` | số doc `state == 1` (phủ index), đọc majority | `read` |
| | `SetMemberCount(ctx, room, base uint64, count int) (domain.MemberCount, bool, error)` | `count ≥ 0`; khi `member_count_ver == base`: `$set member_count`, `$inc member_count_ver 1`, trả giá trị sau ghi và `true`; trượt → `false`; dùng cho worker `member_count_repair` và `/app recount` | `cas` |
| `ReadPositions` | `MarkRead(ctx, room, user, seq) (domain.ReadPosition, bool, error)` | member active và `read_seq < seq` → `read_seq = seq`, `read_ver + 1`, trả vị trí mới và `true`; không đổi → vị trí hiện tại, `false`; không active → `ErrNotMember`. Mỗi lần đổi sinh một change `ReadChanged` | `version-bump` |
| | `MarkUnread(ctx, room, user, to) (domain.ReadPosition, bool, error)` | như trên với điều kiện `read_seq > to`, đặt `read_seq = to` | `version-bump` |

- **`ChangeOwners`** (transaction, D100). Kiểu đi kèm: `OwnerView{OwnersVer uint64; Docs, Owners, Candidates []domain.Member}`, `MemberWrite{Cur, Next domain.Member}`, `OwnerDecision func(OwnerView) ([]MemberWrite, error)`. Trong **một** transaction:
  1. đọc `rooms` (không có → `domain.ErrRoomNotFound`) lấy `owners_ver` (thiếu = 0);
  2. `Docs` = doc của `users` (mọi state, theo thứ tự `users`, thiếu thì bỏ);
  3. `Owners` = tối đa 2 owner active, sắp `joined_at` rồi `user_id`;
  4. `Candidates` = admin active đứng đầu (nếu có) rồi member active đứng đầu (nếu có), mỗi loại sắp `priority` giảm dần, rồi `joined_at`, rồi `user_id`;
  5. gọi `decide(view)`; lỗi → huỷ, trả nguyên lỗi, không ghi gì; danh sách rỗng → huỷ, trả `nil, nil`, không tăng `owners_ver`;
  6. ghi từng `MemberWrite` theo đúng thứ tự (kế nhiệm trước) bằng CAS `ver == Cur.Ver` (`ValidateMemberChange`; 1..2 write, user khác nhau); một write trượt → huỷ, `domain.ErrRetryLater`;
  7. `$inc rooms.owners_ver 1` và, khi kế hoạch làm số member active đổi (`delta` = số write chuyển active → removed, âm), cùng lệnh đó `$inc member_count delta, member_count_ver 1`; commit. Va chạm ghi hoặc lỗi commit không rõ kết quả → `domain.ErrRetryLater`. Trả `store.OwnerResult{Written []domain.Member; Count domain.MemberCount; CountChanged bool}`: các `Next` đã ghi theo thứ tự và số member sau ghi (khi đổi).
  `decide` phải thuần (không I/O ngoài closure policy). memstore: chụp view dưới mutex, nhả mutex khi gọi `decide`, rồi dưới mutex kiểm `owners_ver` và `ver` của mọi doc sắp ghi vẫn như lúc chụp; lệch → `ErrRetryLater`, không ghi gì. Nhờ vậy cả hai adapter cho "một transaction lồng trong `decide` commit trước → transaction ngoài nhận `ErrRetryLater`".
- Validator (file `store/member_validate.go`): `ValidateJoin(j, users)` (room ≠ 0, `ValidTenant`, `ValidCID(RequestID)`, `ValidUser(By)`, `At` khác zero, `ReadSeq ≤ MaxInt64`, users 1..`domain.MaxMemberBatch`, mỗi `ValidUser`, không lặp); `ValidateMemberChange(cur, next)` (cùng room/user, `cur.Ver ≥ 1`, `next.Ver == cur.Ver+1 ≤ MaxUint32`, state 1|2, role hợp lệ, `ValidCID(next.RequestID)`, `UpdatedBy` hợp lệ);  `ValidateReadSeq(seq)` (`≤ MaxInt64`). `CreationMember(r domain.Room, m domain.Member) domain.Member`: doc tạo cùng room (active, `Ver 1`, `RequestID = CreationRequestID`, `UpdatedAt/UpdatedBy` của member nếu có, không thì `r.CreatedAt/r.CreatedBy`).
- `Rooms.Member`: doc không có **hoặc** `state ≠ 1` → `domain.ErrNotMember`. `Rooms.Get` đọc thêm `member_count_ver`. `Rooms.Create(r, members)` giữ chữ ký, ghi doc qua `CreationMember` (fixture cũ chỉ có `Room/Tenant/User/Role/JoinedAt` vẫn tạo được member active); ghi `member_count_ver = 1` (cùng `member_count` như cũ), không ghi `owners_ver`.
- `storetest`: `MemberRooms` = `store.Rooms` + `HistoryClearer` + 5 port mới; `RunMembers(t, open)` (Task 4), `RunMemberFeed(t, open)` (Task 6); chạy trên memstore (unit) và mongostore (itest).

### Mongo (Task 5, 6)

- Doc `members`: `{_id, room_id, tenant, user_id, role, state, priority, joined_at, ver, previous_role, previous_state, previous_priority, request_id, updated_at, updated_by, last_change_at, cleared_at (omitempty), read_seq, read_ver}`; `state`, `priority`, `ver`, `read_seq`, `read_ver` **luôn ghi** (filter `$lt`/`$gt` không khớp field thiếu).
- `rooms` thêm `member_count_ver` (omitempty; `$inc` cùng `member_count`), `owners_ver` (chỉ `$inc` trong transaction). Update trên `rooms` không vào feed (feed chỉ xem insert của `rooms`).
- Đọc/clear/vị trí đọc dùng toán tử thường (`$set`, `$inc`, `$max`), **không bao giờ** pipeline, replace hay upsert, **không bao giờ** đụng `ver`; mọi lần đổi thật đặt `last_change_at`. Clear chỉ đặt `last_change_at` khi `cleared_at` thực sự tăng (filter `cleared_at < at` hoặc thiếu).
- `hidden`: `HideMessage` = `UpdateOne({user_id, room_id, thread_root, seq}, $setOnInsert {created_at}, upsert)`; ẩn lại không ghi gì. `Hidden.Between(ctx, room, from, to, limit)` theo `{room_id, created_at}` cho resync.
- Feed `$match` = `$or` của: insert trên `[messages, rooms, message_edits, reactions, pin_actions, members]`; update/replace trên `reactions`; replace trên `members`; update trên `members` có `updateDescription.updatedFields.ver`, `…read_ver` **hoặc** `…cleared_at`; insert trên `hidden`. Decoder: có `ver` → `MemberChanged`; không `ver` mà có `read_ver` → `ReadChanged`; chỉ có `cleared_at` → `HistoryCleared`; insert `hidden` → `MessageHidden`.

### `work` + `reconcile` (Task 4, 6)

- `RecordOf`: `MemberChanged` → `Room`, `User = Member.User`, `Version = Member.Ver`; `ReadChanged` → `Room`, `User`, `Version = Member.ReadVer` (thread, seq 0).
- `KnownKind` nhận `MemberChanged`, `ReadChanged`; `checkKind` bắt buộc đuôi user hợp lệ cho `ReactionChanged`, `MemberChanged`, `ReadChanged`, cấm cho kind khác; `ID()` → `"g:" + pbconv.MemberEventID(room, user, ver)` và `"d:" + pbconv.ReadEventID(room, user, read_ver)`.
- `MessageHidden` → `Room`, `Thread`, `Seq`, `User` (đuôi user), version 0, id `"h:" + pbconv.HiddenEventID(…)`; `HistoryCleared` → `Room`, `User`, id `"c:{room}-cl-{user}-{CommittedAt unix ms}"`. `KnownKind`/`checkKind` nhận cả hai, đuôi user bắt buộc.
- `MemberCountCheck` → chỉ `Room`, `Version` = op ngẫu nhiên (uint32), không đuôi user, id `"k:{room}-{op}"`.
- `work.Timers` (Task 6): `NewTimers(js, streamName, subjectRoot, partitions, delay)`; `Arm(ctx, room) (Timer{Seq uint64}, error)` publish record `MemberCountCheck` lên `{root}.timer.{room}.{op}` với `WithScheduleAt(now + delay)` và `WithScheduleTarget({root}.p{slot(room) % partitions})`, chờ PubAck; `Disarm(ctx, t)` = `DeleteMsg(stream, t.Seq)`, lỗi chỉ log (phiếu bật thì đếm lại vô hại). Stream `CHATIM_WORK` bật `AllowMsgSchedules` trong `EnsureStream`.
- Reader forward kind mới không đổi code. Dòng `message_edits` `ver 0` đi như một record sửa bình thường (`e:{room}-{th}-{seq}-v0`); worker bỏ qua.

### `pbconv` + proto + `publish` (Task 3)

| Hàm | Hợp đồng |
|---|---|
| `MemberEventID(room, user, ver uint32)` | `{room}-mb-{user}-v{ver}` |
| `MemberCountEventID(room, ver uint64)` | `{room}-members-v{ver}` |
| `HiddenEventID(room, user, thread, seq)` | `{room}-hd-{user}-{thread}-{seq}` |
| `ClearedEventID(room, user, at time.Time)` | `{room}-cl-{user}-{at unix ms}` |
| `MessageHidden(r domain.Room, user string, thread, seq uint64, at)`, `HistoryCleared(r domain.Room, user string, clearedAt, at)` | `Actor = user`, subject `member` |
| `ReadEventID(room, user, readVer uint64)` | `{room}-rd-{user}-v{readVer}` |
| `MemberRole(domain.Role)`, `DomainMemberRole(chatimv1.MemberRole)` | ánh xạ hai chiều; `UNSPECIFIED` → `ErrInvalidArgument` |
| `MemberEvent(roomType, m domain.Member) *chatimv1.Event` | theo doc sau ghi: active và trước đó không active → `member_added`; removed và trước đó active → `member_removed` (lý do `LEFT` khi `UpdatedBy == User`, không thì `REMOVED`); active cả hai và role đổi → `member_role_changed`; active cả hai, role giữ, priority đổi → `member_priority_changed`; khác → `nil`. Một event, subject `member`. Envelope `Tenant`, `RoomId`, `RoomType`, `Actor = UpdatedBy`, `Ts = UpdatedAt`, `Seq 0`. Fast path và worker cùng gọi |
| `MemberCountChanged(r domain.Room, c domain.MemberCount, actor string, at)` | id `MemberCountEventID(r.ID, c.Ver)`, `Actor = actor`, subject `room`, payload `{member_count (kẹp int32), member_count_ver}` |
| `ReadUpdated(r domain.Room, user string, pos domain.ReadPosition, at)` | id `ReadEventID`, `Actor = user`, subject `member`, payload `{user, read_seq, read_ver}` |
| `MessageVersions(rows []domain.Edit)` (Task 1) | chỉ ánh xạ từng dòng (`ver`, kind, text, by, at); `EditOriginal` → `EDIT_KIND_ORIGINAL`; không tự dựng dòng gốc |

- `members.proto` (mới): enum `MemberRole {UNSPECIFIED, OWNER, ADMIN, MEMBER}`, enum `MemberRemovedReason {UNSPECIFIED, REMOVED, LEFT}`; request/response của 7 RPC (bảng ở Task 3); payload `MemberAdded {user, role, joined_at, ver, request_id, read_seq, read_ver}` (vào room đổi cả vị trí đọc; feed chỉ cho `MemberChanged` nên vị trí đọc mới đi trong event này), `MemberRemoved {user, reason, previous_role, ver, request_id}`, `MemberRoleChanged {user, role, previous_role, ver, request_id}`, `MemberPriorityChanged {user, int32 priority, int32 previous_priority, ver, request_id}`, `MessageHidden {user, thread_root, seq}`, `HistoryCleared {user, cleared_at}`, `MemberCountChanged {member_count int32, member_count_ver uint64}`, `ReadUpdated {user, read_seq, read_ver}`. Field proto mới dùng tên DB (`ver`, `read_ver`, `member_count_ver`, `request_id`).
- `events.proto`: oneof `member_added = 28`, `member_removed = 29`, `member_role_changed = 30`, `member_count_changed = 31`, `read_updated = 32`, `member_priority_changed = 33`, `message_hidden = 34`, `history_cleared = 35`.
- `publish`: `subjectFor(root, tenant, room, kind)` chọn loại dữ liệu theo kind (bảng D108; kind lạ → `ErrInvalidArgument`, không publish); thay `roomSubject`. Stream giữ `Subjects: evt.>`; RePublish `Source = {root}.*.*.*.*`, `Destination = {live}.{{wildcard(1)}}.{{wildcard(2)}}.{{wildcard(3)}}.evt.{{wildcard(4)}}`; `EnsureStream` (`CreateOrUpdateStream`) sửa luật của stream cũ. `markKey` không mark loại mới.

### `pkg/lru` + `dedupe` (Task 7)

- `lru.New[K, V](limit)`, `Get`, `Put`, `Remove`, `Len`; không khoá (người gọi giữ khoá).
- `dedupe.Space` (`SpaceCID = 0`, `SpaceRequest = 1`) là field cuối của `Key`; `Key.String()` của `SpaceRequest` = `chatim:req:{room}:{user}:{request_id}`, `SpaceCID` giữ `chatim:cid:…`. Interface `registry` của batcher đổi tên thành `Registry` (cùng ba method).
- `RequestKey(room, user, requestID) Key`; `RequestStatus` (`RequestNew`, `RequestDone`, `RequestBusy`); `RequestCacheSize = 4096`; `NewRequests(reg Registry, ttl) (*Requests, error)`; `Begin(ctx, key) (RequestStatus, error)`; `Finish(ctx, key, rec Record)`; `Cancel(ctx, key)`. Với khoá request, `Record.Seq` = số user của lệnh (≥ 1), `CreatedAt` = giờ lệnh.

### `actor` (Task 8)

- `memberCacheTTL = 10 * time.Second`; `(*Router) ForgetMembers(room uint64)`: actor của room có thì tăng thế hệ cache member; không có thì thôi.

### `memberwatch` (Task 16)

- `New(conn *nats.Conn, liveRoot string, forget func(room uint64), log *slog.Logger) (*Watch, error)`; `Start(ctx) error` subscribe `{liveRoot}.*.member.*.evt.member_removed` và `{liveRoot}.*.member.*.evt.member_role_changed`; mỗi message: token 4 của subject là room id thập phân (sai → bỏ, tăng bộ đếm `Malformed()`), gọi `forget(room)`; không giải mã payload. `Stop()` = `Unsubscribe` (không thêm thời gian vào kế hoạch dừng). Start sau router, Stop trước router.

### `access` (Task 9)

- Action mới (6): `AddMembers = "add_members"`, `RemoveMember = "remove_member"`, `LeaveRoom = "leave_room"`, `ChangeMemberRole = "change_member_role"`, `SetMemberPriority = "set_member_priority"`, `MarkRead = "mark_read"` (`MarkUnread` dùng `mark_read`).
- `Request` thêm `Target domain.Member` và `Role domain.Role` (cuối struct).
- `DefaultPolicy`: `add_members` → caller owner hoặc admin; `remove_member` → caller owner, hoặc caller admin và `Target.Role == member`; `change_member_role`, `set_member_priority` → chỉ caller owner; `leave_room`, `mark_read` → luôn cho; luật sửa/xoá tin giữ nguyên; sai → `ErrDenied`.

### `ownership` (Task 10, package mới, thuần)

- `Action` (`Leave = "leave"`, `Remove = "remove"`, `ChangeRole = "change_role"`).
- `Request{Action; Caller, Target string; Role domain.Role; RequestID string; At time.Time; Allow func(caller, target domain.Member) error}`.
- `Affects(target domain.Member, a Action, role domain.Role) bool`: đích active role owner, hoặc `ChangeRole` sang owner → lệnh phải đi đường transaction.
- `Successor(candidates []domain.Member) (domain.Member, bool)`: admin trước member, rồi `Priority` lớn nhất, rồi `JoinedAt` sớm nhất, rồi user id nhỏ nhất.
- `Plan(r Request, v store.OwnerView) ([]store.MemberWrite, error)`: hàm `decide` của `ChangeOwners` (chi tiết ở Task 10).

### `mutate` (Task 11, 13; Task 1 clear)

- `ClearCmd{Tenant, User string; Room uint64}`; `ClearHistory(ctx, c) (time.Time, error)` (Task 1).
- `Limits.MemberBatch int`: 0 → `DefaultMemberBatch` (500); hợp lệ `MinMemberBatch` (2) .. `domain.MaxMemberBatch`; `(*Mutator) MemberBatch() int`.
- `Deps` thêm (Task 11) `Members MemberStore` (= `store.MemberWriter` + `MembersOf` + `store.OwnerChanges` + `AddMemberCount`), `Log *slog.Logger` (nil → `slog.Default()`), `Requests RequestDedupe` (`Begin/Finish/Cancel` của `dedupe.Requests`), `Timers CountTimers` (`work.Timers`), `Forget MemberForgetter` (`ForgetMembers(room)`), `NewRequestID func() string` (nil → 17 byte ngẫu nhiên dạng hex); (Task 13) `Reads store.ReadPositions`. Thiếu dep bắt buộc → `errMissingDeps` (sửa thông điệp).
- Số member (D102): `Deps.Timers CountTimers` (`Arm(ctx, room) (Timer, error)`, `Disarm(ctx, Timer)`). Lệnh có thể đổi số (AddMembers đường `RequestNew`; Remove/Leave đường thường khi đích active) gọi `Arm` **trước** ghi member; lỗi → `Requests.Cancel` (AddMembers) và `ErrRetryLater`. Sau ghi: `delta ≠ 0` → `AddMemberCount(room, delta)`; thành công → `Disarm` và event `MemberCountChanged(room, c, caller, now)` (sau event member); `delta = 0` → `Disarm`; `$inc` lỗi → log, **không** `Disarm` (phiếu sẽ sửa), lệnh vẫn thành công. Đường owner không `Arm` (đã trong transaction).
- Lệnh: `AddMembersCmd{Tenant, User string; Room uint64; Users []string; RequestID string}` → `[]domain.Member`; `RemoveMemberCmd{…; Target string}`, `LeaveRoomCmd{Tenant, User; Room}`, `ChangeRoleCmd{…; Target string; Role domain.Role}`, `SetPriorityCmd{…; Target string; Priority int32}` → `MemberResult{Member domain.Member; Changed bool; Successor string; PreviousRole domain.Role; PreviousPriority int32}`; `ReadCmd{Tenant, User string; Room, Seq uint64}` → `MarkRead`/`MarkUnread` trả `domain.ReadPosition`.
- Mã lỗi:

| Trường hợp | Lỗi | gRPC |
|---|---|---|
| `request_id` thiếu/sai, users rỗng, user sai, xoá chính mình, role `UNSPECIFIED`/sai, `MarkUnread(0)` | `ErrInvalidArgument` | `INVALID_ARGUMENT` |
| quá `MEMBER_BATCH_MAX` người | `ErrTooManyMembers` | `INVALID_ARGUMENT` |
| room DM | `ErrDirectRoom` | `FAILED_PRECONDITION` |
| owner cuối tự hạ | `ErrLastOwner` | `FAILED_PRECONDITION` |
| caller không active, policy từ chối, người chưa từng là member gọi rời | `ErrNotMember`, `access.ErrDenied` | `PERMISSION_DENIED` |
| xoá người chưa từng là member (sau khi policy cho), đổi role/priority người không active | `ErrMemberNotFound` | `NOT_FOUND` |
| CAS trượt, transaction va chạm, request đang chạy ở core khác | `ErrRetryLater` | `UNAVAILABLE` |
| xoá tombstone, người đã rời gọi rời, đổi về đúng role/priority hiện tại | thành công, `Changed = false` | `OK` |

### `config` (Task 14)

| Env | Field | Mặc định | Kiểm |
|---|---|---|---|
| `MEMBER_BATCH_MAX` | `Config.Limits.MemberBatch` | 500 | > 0 khi parse; `mutate.Limits` 2..1000 |
| `MEMBER_COUNT_CHECK_DELAY` | `Config.MemberCountCheckDelay` | 5s | > `CORE_REQUEST_DEADLINE` |

Kế hoạch dừng không đổi (26.2s / 28s). Không env `READ_RECEIPT_*`.

### `effects` (Task 1, 15)

- `edit_projection` và `msg_changed`: record `EditInserted` có `Version == 0` → trả nil ngay (không đọc store, không drop, không event).
- `MemberEventName = "member_event"`, `MemberCountEventName = "member_count_event"`, `ReadEventName = "read_event"`.
- Registry cuối: `store.MemberChanged` → `room_activity` (0) → `member_event` (`RECONCILE_DELAY`) → `member_count_event` (`RECONCILE_DELAY`); `store.ReadChanged` → `read_event`; `store.MessageHidden` → `hidden_event`; `store.HistoryCleared` → `history_cleared_event` (cả ba `RECONCILE_DELAY`); `work.MemberCountCheck` → `member_count_repair` (delay 0, phiếu đã chờ đủ). Tên: `HiddenEventName = "hidden_event"`, `HistoryClearedEventName = "history_cleared_event"`, `MemberCountRepairName = "member_count_repair"`.
- Metric: `effect_dropped_total{effect}` và `reconcile_republished_total{effect}` cho các effect mới (qua `counters()`); `member_count_repair` đếm sửa vào `counter_repaired_total{counter="members"}`. Không luật alert mới (16 luật; `ChatimCounterRepairSurge` sẵn có phủ).

### `resync` (Task 17)

- `Deps.Members` (port `MembersBetween`, đặt trước `Pub`), `Report.MemberRecords` (in `member_records=%d` trước `dry_run`), `ErrMemberPageFull`.
- `/app recount -room ID [-dry-run]` (`apps/core/recount_command.go`, dispatch trong `main.go` cạnh `resync`): Mongo + NATS, cùng config và redaction như resync; đọc room (`ErrRoomNotFound` → exit lỗi), `CountMembers`; in `room=… stored=… counted=…`; `-dry-run` dừng ở đó; không thì `SetMemberCount(room, ver đã đọc, n)` (trượt → in và exit lỗi, chạy lại) rồi publish `MemberCountChanged(room, c, "", now)` và chờ PubAck, in `member_count_ver=…`. Lệnh này cho vận hành tay; bình thường phiếu hẹn tự sửa (D102).

### Route + corecli (Task 18)

- `tools/internal/route`: `AddMembers`, `RemoveMember`, `LeaveRoom`, `ChangeMemberRole`, `SetMemberPriority`, `MarkRead`, `MarkUnread`, cùng dạng các lệnh có sẵn (trả response, `Stats`, error), retry giữ nguyên request.
- `corecli`: `add-members -room -users a,b [-request-id]`, `remove-member -room -target`, `leave -room`, `set-role -room -target -role`, `set-priority -room -target -priority`, `read -room [-seq]`, `unread -room -seq`; `watch -room` subscribe `live.{t}.*.{rid}.>` (Task 3); `clear` đã bỏ `-up-to` ở Task 1.

---

## Thứ tự task

| # | Task | Rủi ro | Phụ thuộc | Review |
|---|---|---|---|---|
| 1 | Baseline; đổi tên field proto `ver`; đổi tên field 7 collection; clear history theo thời gian; `message_edits` dòng `ver 0`. Sau commit: `make infra-reset` | cao | — | ★ |
| 2 | `domain` (role admin, `MemberState`, field member, `Join`, lỗi, `NewRoom` điền field) + `keys.Member` | thấp | 1 | — |
| 3 | Proto `members.proto` + `make proto` + `pbconv` + `publish` (subject theo loại dữ liệu cho mọi event) + itest RePublish | cao | 2 | ★ |
| 4 | Store port + memstore + storetest `RunMembers` (cả `ChangeOwners`) + write contract + `RecordOf` | cao | 2, 3 | ★ |
| 5 | mongostore: `members` clustered, codec, index, 5 port (`$inc` số member), transaction đổi owner. Sau commit: `make infra-reset` | cao | 4 | ★ |
| 6 | Feed member + vị trí đọc + `RunMemberFeed` + `work` + phiếu hẹn `work.Timers` + registry tạm + itest feed-skip. **Push** | cao | 4, 5 | ★ |
| 7 | `pkg/lru` (chuyển từ `actor`) + `dedupe.Requests` | cao | 1 | ★ |
| 8 | Actor: cache member theo thế hệ + TTL, `ForgetMembers` | cao | 7 | ★ |
| 9 | `access`: 6 action, `Target`/`Role`, `DefaultPolicy` | cao | 2 | ★ |
| 10 | Package `ownership` (luật thuần: `Affects`, `Successor`, `Plan`) | cao | 4 | ★ |
| 11 | `mutate`: `AddMembers`, `RemoveMember`, `LeaveRoom`, `ChangeMemberRole`, `SetMemberPriority`, `$inc` số member, `Limits.MemberBatch`, wiring | cao | 3, 4, 7–10 | ★ |
| 12 | `CreateRoom`: trần mỗi lệnh, `member_added` cho từng người tạo, bỏ trần 5000 | cao | 11 | ★ |
| 13 | `mutate.MarkRead`/`MarkUnread` + `read_updated`; event ẩn tin, xoá lịch sử | trung bình | 3, 11 | ★ |
| 14 | Config (2 env) + 7 RPC `grpcsrv` + README. **Push** | cao | 12, 13 | ★ |
| 15 | Effect `member_event`, `member_count_event`, `read_event`, `hidden_event`, `history_cleared_event`, `member_count_repair` + registry + metric + wiring | cao | 6, 14 | ★ |
| 16 | `memberwatch`: mọi core nghe event member, quên cache | trung bình | 3, 8, 15 | ★ |
| 17 | Resync member + `/app recount` | trung bình | 5, 6 | — |
| 18 | Route + corecli + e2e member/đọc | thấp | 14 | — |
| 19 | Itest xuyên phần | trung bình | 14–17 | — |
| 20 | Docs + kiểm chứng cuối + checklist merge. **Push** | thấp | tất cả | — |

Task 7, 9, 10 khác file nhau: review task này được trong khi task kế đang làm. Task 8 phải sau Task 7 (cùng `actor`).

---

### Task 1: ★ Baseline + đổi tên field proto và 7 collection + clear history theo thời gian + `message_edits` dòng `ver 0`

**Mục tiêu.** Đặt nền cho mọi task sau (D96, D97): tên field đầy đủ ở DB và tên `ver` ở proto; clear history theo mốc thời gian; `message_edits` mỗi dòng một phiên bản nội dung. Ba commit trong một task.

**Files.**
- Proto: `proto/chatim/v1/core.proto`, `events.proto`, `reactions_pins.proto`; sinh lại `pkg/pb/chatim/v1/*.pb.go` (`make proto`).
- Caller của getter proto đổi tên (trình biên dịch chỉ ra): `apps/core/internal/pbconv/*`, `apps/core/internal/grpcsrv/*` (kể cả test), `tools/internal/route/*`, `tools/corecli/*` (kể cả `internal/e2e`), các itest `apps/core/*_integration_test.go` có dùng.
- Đổi tên field: trong `apps/core/internal/store/mongostore/` các file `bootstrap.go`, `codec.go`, `edit_codec.go`, `edits.go`, `feed.go`, `feed_reaction_change.go`, `hidden.go`, `pin_codec.go`, `pin_state.go`, `pins.go`, `reaction_codec.go`, `reaction_count.go`, `reactions.go`, `room_activity.go`, `rooms.go` và test: `codec_test.go`, `edit_codec_test.go`, `pin_codec_test.go`, `reaction_codec_test.go`, `reactions_test.go`, `feed_change_test.go`, `feed_reaction_change_test.go`, `bootstrap_integration_test.go`, `bootstrap_edits_integration_test.go`, `bootstrap_reactions_integration_test.go`, `feed_anchor_integration_test.go`, `feed_skip_integration_test.go`, `reactions_integration_test.go`, `reaction_count_integration_test.go`.
- Clear history: `apps/core/internal/domain/room.go`, `store/ports.go`, `store/memstore/rooms.go`, `view/pipeline.go`, `view/masks.go`, `mutate/mutator.go`, `mutate/hide_clear.go`, `grpcsrv/get_history.go`, `grpcsrv/change_message.go`, `tools/corecli/cmd_change.go`; test `view/masks_test.go`, `view/reaction_masks_test.go`, `mutate/hide_clear_test.go`, `grpcsrv/change_message_test.go`, `grpcsrv/history_masks_test.go`, `store/storetest/rooms_cases.go`, `store/storetest/viewer_cases.go`, `apps/core/edit_access_integration_test.go`.
- `message_edits` dòng `ver 0`: `domain/edit.go`, `domain/edit_test.go`, `store/edit.go`, `store/edit_test.go`, `store/memstore` (file cài `Edits`), `store/storetest/fact_cases.go`, `store/storetest/apply_cases.go`, `mongostore/edit_codec.go`, `mongostore/edits.go`, `mutate/change.go`, `mutate/edit_test.go`, `mutate/delete_test.go`, `pbconv/message_change.go` (+ test), `grpcsrv/edit_history.go` (+ test), `effects/edit_projection.go`, `effects/message_changed.go` (+ test).
- `INDEXES.csv`: dòng `apps/core/internal/store/mongostore`, `apps/core/internal/store`, `apps/core/internal/store/memstore`, `apps/core/internal/store/storetest`, `apps/core/internal/domain`, `apps/core/internal/view`, `apps/core/internal/mutate`, `apps/core/internal/grpcsrv`, `apps/core/internal/pbconv`, `apps/core/internal/effects`, `proto/chatim/v1/core.proto`, `proto/chatim/v1/events.proto`, `proto/chatim/v1/reactions_pins.proto`, `tools/corecli`.
- `README.md` dòng `clear -room ID [-up-to N]` để Task 20 sửa.

**Todo.**
1. Đồng bộ: `git switch feat/m2b`, `git pull --ff-only`; `git status --short` không có dòng nào trong `apps/`, `proto/`, `pkg/`, `tools/`, `deploy/`, `scripts/`, `INDEXES.csv` (thay đổi docs của owner thì không đụng, không stage).
2. Baseline: `make fmt-check && make vet && make lint && make test`, rồi `make infra-up && make itest`. Đỏ → dừng, báo cáo, không sửa.
3. **Commit A — proto `ver`.** Đổi tên field theo "Đổi tên field proto" (trừ clear history, làm ở commit B), `make proto`, `make buf-lint`, rồi sửa mọi getter/field Go do trình biên dịch báo (`make vet` liệt kê hết). Không đổi hành vi, không đổi tên Go nội bộ.
4. **Commit B — field DB + clear history.**
   - Đổi tag `bson` trong codec và mọi tên thô theo bảng; index theo bảng; `feed_reaction_change.go` đọc `updatedFields.ver`; bỏ hẳn đường đọc/xoá doc vị trí cũ `_id: "messages"` (hằng và hàm khởi tạo của nó, test carry-over); `Forget` chỉ xoá doc `changes`; bootstrap anchor dùng `cluster_time`.
   - `domain.Member.ClearedBeforeSeq` → `ClearedAt time.Time`; port `HistoryClearer.ClearHistory(ctx, room, user, at) (time.Time, error)` với `$max cleared_at` (Mongo `FindOneAndUpdate` After; memstore giữ `max`).
   - `mutate.ClearHistory` dùng `m.now()` (đã cắt ms), không đọc `Messages.Last`; trả `time.Time`.
   - `view.Viewer.ClearedAt`; thêm method `(Viewer) Cleared(createdAt) bool` = mốc khác zero và `createdAt` không sau mốc; `HideForViewer` và `grpcsrv.viewerOf` (bỏ đọc `HiddenIn` khi tin mới nhất của trang đã bị clear) dùng chung nó.
   - Proto clear history theo bảng; `grpcsrv.ClearHistory` trả `cleared_at`. `corecli clear` bỏ cờ `-up-to`, in `cleared_at`.
   - `hidden`: thêm `created_at` bằng `$setOnInsert` (ẩn lại không ghi gì) và index `{room_id: 1, created_at: 1}` (D109); port `store.Hidden` thêm `Between(ctx, room, from, to, limit)` (memstore + Mongo + storetest) cho resync.
5. **Commit C — `message_edits` dòng `ver 0`.**
   - `domain`: thêm `EditOriginal`, bỏ `Edit.Prev`. `store.ValidateEdit` theo hợp đồng. Codec Mongo bỏ `p`; kind lưu như cũ.
   - `mutate.commit`: sau mọi kiểm conflict và ngay trước `Append` của `ver 1`, gọi `Edits.Append` một dòng gốc `{ver 0, EditOriginal, Tenant, By = msg.From, Text = msg.Text, At = msg.CreatedAt}`; `ErrEditExists` = đã có, đi tiếp. Chỉ áp cho **sửa**; xoá (kể cả xoá đầu tiên) không ghi dòng gốc. Bỏ việc điền `Prev`.
   - `PurgeText(key, upTo)` xoá `text` của dòng `0..upTo` trên cả hai adapter (bỏ nhánh trả sớm khi `upTo == 0`), nên xoá tin ở `ver 1` xoá luôn text của dòng gốc.
   - `pbconv.MessageVersions(rows)` chỉ ánh xạ; `grpcsrv.GetEditHistory`: tin đã xoá → rỗng (như cũ); `History(key, after_ver, limit)`; nếu trang rỗng → trả rỗng; nếu `after_ver == 0` thì đọc thêm `Edits.At(key, 0)` và đặt dòng đó lên đầu (thiếu → bỏ qua). Nhờ vậy dòng gốc mồ côi (core chết giữa ghi `ver 0` và `ver 1`) không bao giờ hiện, và kết quả luôn theo thứ tự `ver`.
   - `effects`: `edit_projection`, `msg_changed` trả nil ngay cho record `Version == 0`.
6. INDEXES, commit từng phần; sau commit C chạy `make infra-reset`, `make infra-up`, rồi `make itest`.

**Kỹ thuật.**
- Tìm tên thô bằng `grep -rn` trên chuỗi tên ngắn trong ngoặc kép trong `mongostore` (ví dụ `"ab"`, `"ls"`, `"ts"`); mọi filter, update, pipeline, projection, sort phải đổi cùng codec.
- `cleared_at` lưu kiểu date, ms; so sánh ẩn tin theo `!createdAt.After(clearedAt)` (tin cùng ms với mốc bị ẩn, D97).
- Dòng `ver 0` không cần trường hợp đặc biệt ở feed: insert vào `message_edits` vẫn thành `EditInserted` với `Version 0`; record id `e:{room}-{th}-{seq}-v0` hợp lệ; `checkKind` không chặn version 0.
- `Edits.Latest` và `History` giữ khoảng `> after` nên tự loại dòng `ver 0`; memstore phải cho cùng kết quả.

**Test (viết trước).**
- `mutate/hide_clear_test.go`: `TestClearHistoryMarksTheServerTimeAndOnlyRaisesIt` — mốc = giờ `Now` giả (cắt ms), gọi lại với giờ sớm hơn không hạ; người không active → `PERMISSION_DENIED`.
- `view/masks_test.go`: `TestClearedHidesEveryMessageUpToTheMarkTime` — tin `CreatedAt` trước và bằng mốc bị ẩn ở mọi thread, tin sau mốc hiện; mốc zero không ẩn gì.
- `storetest` (`rooms_cases.go`, `viewer_cases.go`): clear chỉ nâng, theo từng member; doc khác không đổi. `hidden_cases`: ẩn lại giữ `created_at` lần đầu; `Between` theo thời gian, limit.
- `mongostore/codec_test.go`: `TestMemberCodecReadsClearedAt`; `TestReactionDocumentLayout` (tên field mới theo đúng thứ tự); test layout cho `rooms`, `pins[]`, `hidden`, `pin_actions`, `message_edits` (không còn `p`).
- `mongostore/feed_anchor_integration_test.go`: `TestFeedAnchorUsesFullFieldNames` (`resume_token`, `cluster_time`); `TestForgetClearsThePosition` chỉ còn doc `changes`.
- `feed_reaction_change_test.go`: update có `updatedFields.ver` được giải mã; `n` không còn.
- `grpcsrv`: `TestHideAndClearHistoryThroughTheService` (response có `cleared_at`, tin trước mốc ẩn, tin gửi sau hiện), `TestHistoryShowsPlaceholdersPerViewer`.
- `apps/core/edit_access_integration_test.go`: clear theo thời gian, tin gửi sau mốc vẫn hiện với người đã clear.
- `store/edit_test.go`: `TestValidateEditTiesVerZeroToTheOriginalKind` — `ver 0` chỉ với `EditOriginal` và ngược lại; không còn ca `prev`.
- `storetest/fact_cases.go`: dòng gốc ghi hai lần → lần hai `ErrEditExists`, nội dung giữ lần đầu; `History(after 0)` không chứa `ver 0`, `At(0)` có; `PurgeText(upTo 0)` xoá text dòng 0; `PurgeText(upTo 2)` xoá 0..2, giữ 3.
- `mutate/edit_test.go`: `TestFirstEditWritesTheOriginalRowOnce` (dòng 0 = text lúc gửi, by = người gửi, at = lúc gửi), `TestARetriedFirstEditKeepsOneOriginalRow`, `TestLaterEditsWriteNoOriginalRow`. `mutate/delete_test.go`: `TestAFirstDeleteWritesNoOriginalRow`, `TestADeleteAfterAnEditPurgesTheOriginalRow`.
- `pbconv`: `TestMessageVersionsMapsEachRowInVerOrder` (dòng 0 → `EDIT_KIND_ORIGINAL`, `ver 0`).
- `grpcsrv` edit history: `TestEditHistoryStartsWithTheOriginalRow`, `TestEditHistoryHidesALoneOriginalRow` (chỉ có dòng 0 → rỗng), `TestEditHistoryAfterAVerSkipsTheOriginalRow`.
- `effects`: `TestEditProjectionSkipsTheOriginalRow`, `TestMessageChangedSkipsTheOriginalRow` (không đọc store, không event, không drop).

**Lệnh.** Test các package đụng bằng `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/... ./tools/..."`; `make fmt-check`, `make vet`, `make lint`; sau commit C: `make infra-reset && make infra-up && make itest`.

**Done khi.** Ba commit xanh; `grep -rn '"ab"\|"ls"\|"lc"\|"mc"\|"pv"\|"cb"\|"ja"\|"ro"\|"pe"\|"op"' apps/core/internal/store/mongostore` không còn tên field cũ (trừ `messages`); `make itest` xanh sau reset; không còn `ClearedBeforeSeq`, `Prev` (của `Edit`), `GetVersion` của proto trong repo.

**Commit.**
- A: `refactor(proto): rename version fields to ver` — `proto/chatim/v1/ pkg/pb/chatim/v1/` + mọi file caller + `INDEXES.csv`.
- B: `refactor(store): use full field names and clear history by time` — `proto/chatim/v1/core.proto pkg/pb/chatim/v1/ apps/core/internal/ apps/core/edit_access_integration_test.go tools/ INDEXES.csv` (đúng các path đã sửa).
- C: `feat(store): keep each message content version as one edit row from ver 0` — `apps/core/internal/domain/ apps/core/internal/store/ apps/core/internal/mutate/ apps/core/internal/pbconv/ apps/core/internal/grpcsrv/ apps/core/internal/effects/ INDEXES.csv`.

**Review:** có (★).

---

### Task 2: `domain` + `keys.Member`

**Mục tiêu.** Kiểu dữ liệu chung của lớp tập member (D98, D99, D101, D105). `Next` và `Join.Apply` là hai hàm thuần định nghĩa ngữ nghĩa cho mọi adapter.

**Files.** Tạo `pkg/keys/member.go`, `pkg/keys/member_test.go`, `apps/core/internal/domain/member.go`, `apps/core/internal/domain/read.go`, `apps/core/internal/domain/member_test.go`. Sửa `domain/room.go`, `domain/errors.go`, `domain/validate.go` (`NewRoom`), `domain/room_test.go`, `domain/errors_test.go`, `INDEXES.csv` (dòng `apps/core/internal/domain`, `pkg/keys`).

**Todo.**
1. `keys.Member`, `ParseMember`, `MaxMemberUser` theo hợp đồng.
2. `domain`: mọi tên trong bảng `domain` của hợp đồng (trừ phần Task 1 và việc bỏ trần ở Task 12).
3. `NewRoom` điền `State`, `Ver 1`, `RequestID`, `UpdatedAt`, `UpdatedBy` cho từng member; trần 5000 giữ tới Task 12.

**Kỹ thuật.** `Member`, `Room` vẫn so sánh bằng `==` (chỉ thêm số, chuỗi, `time.Time`). Lỗi mới bọc `apperr` như các lỗi domain cũ để `pkg/grpcserver` ánh xạ mã. `CreationRequestID` dùng `strconv.FormatUint`.

**Test.**
- `TestMemberKeyIsTheRoomThenTheUser` (8 byte big-endian rồi byte user), `TestParseMemberRejectsWrongLengths` (≤ 8, > 8+64).
- `TestParseRoleKnowsThreeRoles`; `TestCreationRequestIDIsAValidCID`.
- `TestNextMovesMembershipAndKeepsReaderState` — `Ver+1`, `Previous*` đúng, giữ `JoinedAt/ClearedAt/ReadSeq/ReadVer`.
- `TestJoinApplyAddsReAddsAndKeepsActiveMembers` — chưa có doc → `Ver 1`, role member; tombstone admin → active role member, `Ver+1`, `ReadSeq = max`, `ReadVer+1`, giữ `ClearedAt`; active → y nguyên.
- `TestAddedByNeedsActiveSameRequestAndCaller`.
- `room_test.go`: `NewRoom` điền đủ field cho mọi member; `errors_test.go`: mã apperr của bốn lỗi mới.

**Lệnh.** `make -s go ARGS="test -race -shuffle=on ./pkg/keys/... ./apps/core/internal/domain/..."`; fmt/vet/lint.

**Done khi.** Test xanh; `make test` không đỏ ở package khác (adapter chưa lưu field mới là bình thường, chưa có test đòi).

**Commit.** `feat(domain): add the member set doc, join and read position types` — `pkg/keys/ apps/core/internal/domain/ INDEXES.csv`.

**Review:** không (controller kiểm nhanh).

---

### Task 3: ★ Proto members + `pbconv` + `publish` (subject theo loại dữ liệu)

**Mục tiêu.** Hợp đồng wire cho member, priority, số member, vị trí đọc, ẩn tin, xoá lịch sử (D100, D102, D104, D105, D109); mọi event, cũ và mới, đi subject theo loại dữ liệu (D108).

**Files.** Tạo `proto/chatim/v1/members.proto` (+ sinh `pkg/pb/chatim/v1/members.pb.go`); sửa `core.proto`, `events.proto` (sinh lại `core.pb.go`, `core_grpc.pb.go`, `events.pb.go`). Tạo `apps/core/internal/pbconv/member.go`, `member_count.go`, `read.go`, `member_event_id_test.go`, `member_test.go`, `member_count_test.go`, `read_test.go`. Sửa `apps/core/internal/publish/message.go` (hằng kind, `subjectFor`), `stream.go` (RePublish), `stream_test.go` và mọi test có subject `….room.…` của tin/reaction (`publish/*_test.go`, `effects/*_test.go`, `apps/core/it_core_test.go` helper `subscribeLive` → `live.{t}.*.{rid}.>`, `publish/nats_integration_test.go`); `tools/corecli/cmd_watch.go`, `tools/corecli/internal/e2e/*_test.go`, `tools/poc/corebench/live.go` (sub `live.{t}.*.{rid}.>` hoặc `live.{t}.message.>`). Tìm hết bằng `grep -rn '\.room\.' apps tools`. Tạo `publish/member_read_event_test.go`, `publish/nats_republish_integration_test.go`. `INDEXES.csv`: dòng mới `proto/chatim/v1/members.proto`; dòng `core.proto`, `events.proto`, `pkg/pb/chatim/v1`, `pbconv`, `publish`.

**Todo.**
1. `members.proto` theo hợp đồng; request/response:

| RPC | Request | Response |
|---|---|---|
| `AddMembers` | `room_id = 1`, `repeated users = 2`, `request_id = 3` | `repeated AddedMember added = 1` (`AddedMember {user = 1, uint32 ver = 2}`) |
| `RemoveMember` | `room_id = 1`, `user = 2` | `changed = 1`, `uint32 ver = 2` |
| `LeaveRoom` | `room_id = 1` | `changed = 1`, `uint32 ver = 2`, `new_owner = 3` |
| `ChangeMemberRole` | `room_id = 1`, `user = 2`, `MemberRole role = 3` | `changed = 1`, `uint32 ver = 2`, `MemberRole previous_role = 3` |
| `SetMemberPriority` | `room_id = 1`, `user = 2`, `int32 priority = 3` | `changed = 1`, `uint32 ver = 2`, `int32 previous_priority = 3` |
| `MarkRead` | `room_id = 1`, `uint64 seq = 2` | `uint64 read_seq = 1`, `uint64 read_ver = 2` |
| `MarkUnread` | `room_id = 1`, `uint64 seq = 2` | `uint64 read_seq = 1`, `uint64 read_ver = 2` |

2. `core.proto` import `members.proto`, service thêm 7 RPC; `events.proto` import, payload 28–35. `make proto`, `make buf-lint`.
3. `pbconv` theo bảng hợp đồng (id, role, `MemberEvent`, `MemberCountChanged`, `ReadUpdated`, `MessageHidden`, `HistoryCleared`).
4. `publish`: hằng kind mới (giá trị = tên payload snake_case) trong `eventKind`; `subjectFor` theo bảng D108 cho mọi kind; luật RePublish mới; `markKey` không đổi.
5. Sửa mọi chỗ subscribe/so subject cũ (danh sách ở Files); itest RePublish trên stream đã có.

**Kỹ thuật.**
- Không cần sửa caller: `grpcsrv.Service` nhúng `UnimplementedCoreServiceServer` (7 RPC trả `Unimplemented` tới Task 14); `fakeCore` của route nhúng client; `e2e.EventOf` trả `ok=false` với payload lạ.
- Id phân biệt nhờ token thứ hai sau `{room}-`: tin/sửa/reaction/số đếm là số; ghim `p\d`; room `created`; member `mb`; số member `members-v`; đọc `rd`. User chỉ có `[A-Za-z0-9_-]`, nên đuôi `-v{số}` quyết định duy nhất.
- `EnsureStream` dùng `CreateOrUpdateStream`, nên core mới sửa luật của stream cũ. Itest: đặt lại luật M2b.3 bằng `UpdateStream`, gọi `EnsureStream`, đọc lại cấu hình, publish một `msg_created`, một `room_created`, một `member_added`; chúng tới lần lượt `live.acme.message.101.evt.msg_created`, `live.acme.room.101.evt.room_created`, `live.acme.member.101.evt.member_added`. NATS từ chối sửa RePublish → **dừng, báo** kèm lỗi nguyên văn.

**Test.**
- `TestMemberCountAndReadEventIDs`; `TestMemberEventIDsNeverCollideWithOtherKinds` (bảng user `mb`, `v1`, `a-v1`, `members`, `rd`, `mb-bob-v1` so với id tin, sửa, reaction, ghim, room).
- `TestMemberAddedIsOneMemberEventWithTheReadPosition`; `TestMembersCreatedWithTheRoomAreAnnouncedToo`; `TestMemberRemovedSaysWhetherTheUserLeft`; `TestMemberRoleChangedCarriesBothRolesAndOtherDocsCarryNothing`; `TestMemberPriorityChangedCarriesBothPriorities`; `TestHiddenAndClearedEventsCarryTheUserAndTheirIDs`; `TestMemberRolesMapBothWays` (`UNSPECIFIED` lỗi).
- `TestReadUpdatedCarriesTheReaderAndItsReadVer` (actor = user, id theo `read_ver`).
- `TestMemberCountChangedCarriesTheCountAndItsVer` (kẹp int32).
- `publish`: `TestEachKindGoesToTheSubjectOfItsData` (bảng mọi kind → `room`/`member`/`message`; kind lạ → lỗi, không publish); `TestOnlyMessageCreatedIsMarked`; `stream_test.go` kiểm luật RePublish mới.
- Itest `TestRealJetStreamUpdatesTheRePublishRuleForDataSubjects`.

**Lệnh.** Test `./apps/core/internal/pbconv/... ./apps/core/internal/publish/... ./apps/core/internal/effects/... ./tools/...`; itest riêng bằng `itest-one` với `RUN=TestRealJetStreamUpdatesTheRePublishRuleForDataSubjects PKG=./apps/core/internal/publish/`; cuối task `make itest`.

**Done khi.** Test và itest xanh; `grep -rn '\.room\.' apps tools` chỉ còn subject của event room và ephemeral.

**Commit.** `feat(publish): route events by data subject and add member, member count and read position events` — `proto/chatim/v1/ pkg/pb/chatim/v1/ apps/core/internal/pbconv/ apps/core/internal/publish/ INDEXES.csv`.

**Review:** có (★).

---

### Task 4: ★ Store port + memstore + storetest `RunMembers` + write contract + `RecordOf`

**Mục tiêu.** Năm port của lớp tập member và ngữ nghĩa chuẩn qua storetest, cài trên memstore; Mongo cài ở Task 5 và chạy cùng bộ case.

**Files.** Tạo `store/member.go`, `store/member_validate.go`, `store/member_validate_test.go`; sửa `store/feed.go` (`MemberChanged`, `Change.Member`), `store/write_contract_test.go`. Sửa `work/record.go`, `work/record_test.go`. Tạo `store/memstore/members.go`, `owner_changes.go`, `member_count.go`, `read_position.go`; sửa `memstore/rooms.go`, `memstore/memstore_test.go`. Tạo `store/storetest/member_cases.go`, `member_change_cases.go`, `owner_change_cases.go`, `member_count_cases.go`, `read_position_cases.go`; sửa `storetest/rooms_cases.go`. `INDEXES.csv`: `store`, `store/memstore`, `store/storetest`, `work`.

**Todo.**
1. Port, kiểu `OwnerView`/`MemberWrite`/`OwnerDecision`, validator, `CreationMember`, `MaxMemberScan`, `MemberChanged`, `Change.Member` theo hợp đồng.
2. Write contract: thêm kind `transaction` vào danh sách cho phép (và vào thông điệp lỗi), phân loại mọi method mới, thêm 5 port vào danh sách reflect.
3. `work.RecordOf` có case `MemberChanged`, `ReadChanged`, `MessageHidden`, `HistoryCleared` (switch không `default`, lint `exhaustive`). `KnownKind`/`checkKind`/`ID()` để Task 6.
4. memstore cài 5 port trên `*Rooms`; `Rooms.Member`, `ClearHistory` lọc active; `Create` ghi doc qua `CreationMember`.
5. storetest `RunMembers` + gọi từ test memstore.

**Kỹ thuật.**
- `MembersBetween` cùng thời điểm phải sắp như Mongo so BinData `_id` (độ dài trước): memstore sắp `(LastChangeAt, len(User), User)`. `MarkRead`/`MarkUnread`/`ClearHistory` khi đổi thật đặt `LastChangeAt`.
- `ChangeOwners` memstore: chụp view (đọc `owners_ver`, docs, owners, candidates) dưới mutex; gọi `decide` khi **không** giữ mutex; lấy lại mutex, kiểm `owners_ver` và `ver` của mọi `Cur` sắp ghi; lệch → `domain.ErrRetryLater`; khớp → ghi lần lượt, `owners_ver + 1`. Không vòng lặp.
- `MembersOf` kiểm số user bằng `ValidateLimit(len(users), MaxMemberBatch+1)`.
- `MarkRead`/`MarkUnread` không đổi `ver`, `updated_*`.

**Test (storetest, chạy trên memstore; Mongo ở Task 5).**
- `member_cases.go`: create ghi doc tạo active `ver 1` `{room}-created` (kể cả fixture thiếu field); `Rooms.Member` từ chối tombstone; `MembersOf` theo thứ tự, bỏ user không có doc; `AddMembers` thêm mới, thêm lại tombstone (role member, `read_seq` max, giữ `cleared_at`), giữ nguyên doc active (không đổi `ver`, `updated_at`), trả doc sau ghi theo thứ tự; validate join sai → `ErrInvalidArgument`.
- `member_change_cases.go`: `ApplyMember` khớp ghi đúng field (gồm `priority`, `previous_priority`, `last_change_at`); `ver` cũ → `false`; không có doc → `false`; chỉ đổi field membership. `MembersBetween` khoảng kín, thứ tự, limit.
- `owner_change_cases.go`: (a) view đúng (`Docs` theo thứ tự, `Owners` ≤ 2 sắp `joined_at`/user, `Candidates` = admin đứng đầu rồi member đứng đầu theo `priority` giảm dần, `joined_at`, user); (b) kế hoạch 2 write ghi cả hai, đúng thứ tự, `owners_ver + 1`; (c) `decide` lỗi → không ghi gì, `owners_ver` giữ; (d) write thứ hai có `Cur.Ver` cũ → `ErrRetryLater`, write thứ nhất **không** được áp; (d2) owner cuối rời → `member_count − 1`, `member_count_ver + 1` cùng commit, `CountChanged`; đổi role không đổi số; (e) kế hoạch rỗng → không ghi, `owners_ver` giữ; (f) một `ChangeOwners` khác commit ngay bên trong `decide` → transaction ngoài `ErrRetryLater`, không ghi gì của nó; (g) room không có → `ErrRoomNotFound`.
- `member_count_cases.go`: room mới có `member_count` = số người tạo, `member_count_ver 1`; `AddMemberCount(+2)` rồi `(−1)` trả đúng số và `ver` 2, 3; `delta 0` → `ErrInvalidArgument`; room không có → `ErrRoomNotFound`; `CountMembers` chỉ đếm doc active; `SetMemberCount(base)` đặt số và `ver + 1` khi `ver == base`, `base` sai → `false`, số âm → `ErrInvalidArgument`.
- `member_cases.go` thêm: `AddMembers` trả `Changed` = số doc mới + tombstone được thêm lại; gọi lại cùng danh sách → `Changed 0`.
- `read_position_cases.go`: `MarkRead` chỉ nâng, `MarkUnread` chỉ hạ, mỗi lần đổi `read_ver + 1`; không đổi → `false`; tombstone → `ErrNotMember`; sau ba lần đổi vị trí, `ApplyMember` với `ver 1` vẫn khớp (vị trí đọc không đụng `ver`); clear history chỉ member active.
- `member_validate_test.go`: `TestValidateJoin`, `TestValidateMemberChangeAcceptsOneStep`, `TestValidateReadSeq`, `TestCreationMemberIsActiveAtVerOne`.
- `work/record_test.go`: `RecordOf` của change member và change đọc.
- `write_contract_test.go` xanh với kind `transaction`.

**Lệnh.** `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/... ./apps/core/internal/work/..."`, thêm `-count=5` cho `memstore` (lock mới); fmt/vet/lint. Không chạy `make itest` (Mongo chưa cài; các case `RunMembers` và vài case `Rooms` cũ của Mongo đỏ tạm tới Task 5, ghi vào báo cáo).

**Done khi.** Unit xanh; danh sách case Mongo đỏ tạm được ghi lại.

**Commit.** `feat(store): add the member set ports with the memstore adapter and contract` — `apps/core/internal/store/ apps/core/internal/work/record.go apps/core/internal/work/record_test.go INDEXES.csv`.

**Review:** có (★, ngữ nghĩa transaction).

---

### Task 5: ★ mongostore — `members` clustered, 5 port, transaction đổi owner

**Mục tiêu.** Mongo cài đúng ngữ nghĩa storetest Task 4 (D98–D100, D102, D105).

**Files.** Tạo `mongostore/member_codec.go` (chuyển `memberDoc`, `encodeMember`, `decodeMember` ra khỏi `codec.go`), `member_join.go`, `members.go`, `owner_changes.go`, `member_count.go`, `read_position.go`; sửa `codec.go`, `mongostore.go`, `bootstrap.go`, `rooms.go`. Test: tạo `member_codec_test.go`, `members_integration_test.go`, `owner_changes_integration_test.go`, `bootstrap_members_integration_test.go`; sửa `codec_test.go`, `bootstrap_integration_test.go`. `INDEXES.csv`: `store/mongostore`.

**Todo.**
1. `members` clustered (`ensureClustered`); index theo hợp đồng thay hai index cũ; `Bootstrap` gặp `members` không clustered → `ErrNotClustered`.
2. Codec theo thứ tự field hợp đồng; số ghi int64; đọc: `ver > MaxUint32`, số âm, `state ∉ {1,2}`, `previous_state ∉ {0,1,2}` → `errCorrupt`.
3. `AddMembers` = **một** `BulkWrite(ordered:false)` các `UpdateOne({_id}, pipeline, upsert)`, `Changed = UpsertedCount + ModifiedCount`, rồi `MembersOf` (primary) trả doc sau ghi.
4. `ApplyMember` = `UpdateOne({_id, ver: cur.Ver}, $set role/state/priority/previous_role/previous_state/previous_priority/request_id/updated_by/updated_at/last_change_at, $inc ver 1)`.
5. `MarkRead`/`MarkUnread` = `FindOneAndUpdate({_id, state: 1, read_seq: {$lt|$gt}}, $set read_seq + last_change_at, $inc read_ver, After)`; không khớp → `FindOne({_id, state: 1})` phân biệt "không active" (`ErrNotMember`) với "không đổi". `ClearHistory` thêm `state: 1`. `Rooms.Member` = `FindOne({_id, state: 1})`.
6. `AddMemberCount` = `FindOneAndUpdate({_id: room}, {$inc: {member_count, member_count_ver}}, After)`, không có doc → `ErrRoomNotFound`; `CountMembers` = `CountDocuments({room_id, state: 1})` read concern majority; `SetMemberCount` = `FindOneAndUpdate({_id, member_count_ver: base}, $set member_count + $inc member_count_ver, After)`, không khớp → `false`. `Rooms.Get` đọc `member_count_ver`; `Rooms.Create` ghi `member_count_ver: 1`.
7. `ChangeOwners` bằng transaction một lần.

**Kỹ thuật.**
- **Pipeline join:** một stage `$set`; `active := {$eq: ["$state", 1]}`; `room_id`, `tenant`, `user_id` đặt thẳng, `state` đặt 1; mọi field còn lại là `{$cond: [active, "$<field>", <mới>]}` nên doc active giữ nguyên byte (Mongo không ghi oplog cho update không đổi); `previous_role` = `$ifNull ["$role", ""]`, `previous_state` = `$ifNull ["$state", 0]` (trong nhánh không active); `ver`, `read_ver` = `$ifNull + 1` (int64); `read_seq` = `$max [$ifNull ["$read_seq", 0], j.ReadSeq]`; `cleared_at` không đụng; mọi chuỗi người dùng bọc `$literal`. Filter chỉ `_id` bằng nên server tự thử lại upsert trùng khoá.
- **Transaction:** `client := db.Client()` giữ trong `Store`; `StartSession`, `StartTransaction` với read concern `snapshot`, write concern majority, read preference primary; trong session context: `FindOne rooms` (projection `owners_ver`), `Find members {_id: {$in}}`, `Find {room_id, state: 1, role: "owner"}` sort `{joined_at: 1, user_id: 1}` limit 2, hai `Find` limit 1 cho admin và member (sort `{priority: -1, joined_at: 1, user_id: 1}`, đi đúng index); gọi `decide`; mỗi write `UpdateOne({_id, ver: Cur.Ver}, …)` như `ApplyMember`, `MatchedCount == 0` → `AbortTransaction`, `ErrRetryLater`; `UpdateOne rooms {$inc: {owners_ver: 1}}`; `CommitTransaction`. Lỗi có label `TransientTransactionError` hoặc `UnknownTransactionCommitResult`, hoặc mã `WriteConflict` → `AbortTransaction` (bỏ qua lỗi abort) và `ErrRetryLater`. Không `WithTransaction`, không vòng lặp. Luôn `EndSession` bằng context không huỷ.
- Nếu driver từ chối read/write concern ở mức collection bên trong transaction, dùng handle collection không concern cho các thao tác trong transaction; không được thì dừng và báo.
- **Số member trong transaction:** `delta` tính từ kế hoạch (write có `Cur` active và `Next` removed); `delta ≠ 0` thì `UpdateOne rooms` gộp `$inc owners_ver 1, member_count delta, member_count_ver 1`, rồi `FindOne` projection `{member_count, member_count_ver}` trong cùng transaction để trả `OwnerResult.Count`.
- Mọi `uint64 → int64` qua `toInt64`.

**Test.**
- `member_codec_test.go`: `TestMemberCodecRoundTrip`, `TestMemberCodecRejectsOutOfRangeValues`, `TestRoomIDAboveMaxInt64IsRejected`, `TestJoinPipelineKeepsActiveDocsAndTakesStringsLiterally` (kiểm cấu trúc pipeline, user `$x` được `$literal`).
- `bootstrap_members_integration_test.go`: `TestBootstrapClustersMembers`, `TestBootstrapRejectsUnclusteredMembers`; `bootstrap_integration_test.go` liệt kê index mới.
- `members_integration_test.go`: `TestMongoMembersContract` (chạy `storetest.RunMembers`); `TestMemberDocumentLayoutAndNoOpAdd` (thứ tự field; thêm người active không đổi `ver`/`updated_at`, `Changed 0`, và không tạo oplog entry mới cho doc đó); `TestMemberCountIsCoveredByTheRoomStateIndex` (explain: dùng index `room_id_1_state_1_role_1_joined_at_1_user_id_1`, không `FETCH`).
- `owner_changes_integration_test.go`: `TestOwnerTransactionWritesNothingWhenTheSecondWriteFails` (Mongo thật), `TestTwoOwnerTransactionsOnOneRoomLetOnlyOneCommit` (hai goroutine, mỗi `decide` chờ nhau qua channel để chắc chồng thời gian; đúng một nil, một `ErrRetryLater`; `member_count` giảm đúng một lần).
- Mọi case Mongo đỏ tạm từ Task 4 xanh lại.

**Lệnh.** Unit `./apps/core/internal/store/mongostore/...`; `make itest`; sau commit `make infra-reset && make infra-up`.

**Done khi.** `make itest` xanh; `wc -l` mọi file `mongostore` < 200.

**Commit.** `feat(mongostore): store members as a clustered set with owner transactions, member count increments and read positions` — `apps/core/internal/store/mongostore/ INDEXES.csv`.

**Review:** có (★).

---

### Task 6: ★ Feed member + vị trí đọc + `RunMemberFeed` + `work` + phiếu hẹn `work.Timers` + registry tạm + itest feed-skip. **Push**

**Mục tiêu.** Mọi đổi membership (đổi `ver`) vào feed thành `MemberChanged`, mọi đổi vị trí đọc thành `ReadChanged` (D103), mỗi lần clear thực sự nâng mốc thành `HistoryCleared`, mỗi lần ẩn tin mới thành `MessageHidden` (D109); số member và `owners_ver` **không bao giờ** vào. Thêm phiếu hẹn đếm lại số member bằng message schedule của NATS (D102).

**Files.** Sửa `memstore/change_log.go`, `feed.go`, `rooms.go`, `members.go`, `owner_changes.go`, `memstore_test.go`; sửa `mongostore/feed.go`, `feed_change.go`; tạo `mongostore/feed_member_change.go`, `feed_member_change_test.go`; sửa `mongostore/feed_change_test.go`, `feed_reaction_change_test.go`, `feed_integration_test.go`, `feed_skip_integration_test.go`. Tạo `storetest/feed_member_cases.go`; sửa `storetest/feed_room_cases.go`. Sửa `work/record.go`, `record_codec.go`, `record_test.go`, `record_tail_test.go`, `work/stream.go` (`AllowMsgSchedules`), `stream_test.go`; tạo `work/timers.go`, `timers_test.go`, `timers_integration_test.go`; `reconcile/forward_test.go`, `reconcile/stats_test.go`; `apps/core/effects_wiring.go`. `INDEXES.csv`: `store/mongostore`, `store/memstore`, `store/storetest`, `work`, `reconcile`, `apps/core`.

**Todo.**
1. Mongo `$match` theo hợp đồng; `feed_member_change.go`: insert/replace → giải mã `fullDocument`; update → `keys.ParseMember(documentKey._id)`; có `updatedFields.ver` (int32/int64/double, 1..`MaxUint32`, user hợp lệ, không thì `errCorrupt`) → `Change{Kind: MemberChanged, Member: {Room, User, Ver}}`; không có `ver` mà có `updatedFields.read_ver` (≥ 1) → `Change{Kind: ReadChanged, Member: {Room, User, ReadVer}}`; chỉ có `cleared_at` → `Change{Kind: HistoryCleared, Member: {Room, User}}`. Insert `hidden` → `Change{Kind: MessageHidden, Hidden: {User, Room, Thread, Seq}}`.
2. memstore: thêm field member vào bản ghi log; ghi log `MemberChanged` ở mọi ghi đổi `ver`: doc tạo trong `Create` (ngay sau room), user đổi trong `AddMembers`, `ApplyMember` khớp, mỗi write của `ChangeOwners` theo thứ tự; ghi log `ReadChanged` ở mỗi `MarkRead`/`MarkUnread` thực sự đổi, `HistoryCleared` ở mỗi clear thực sự nâng mốc, `MessageHidden` ở mỗi lần ẩn mới. Đọc dữ liệu, ẩn lại, clear không nâng mốc, `owners_ver`, `member_count` (`AddMemberCount`, `SetMemberCount`) không log.
3. `work`: `KnownKind`, `checkKind`, `ID()` theo hợp đồng.
4. `effects_wiring.go` đăng ký tạm `store.MemberChanged: {activity.Effect()}` và `store.ReadChanged`, `store.MessageHidden`, `store.HistoryCleared` với danh sách effect rỗng (không đăng ký thì worker Nak kind 6–9 mãi; nếu registry từ chối danh sách rỗng thì dừng và báo); Task 15 thêm effect thật.
5. storetest `RunMemberFeed`; Mongo chạy cùng bộ.
6. `work.Timers` theo hợp đồng; `EnsureStream` của `CHATIM_WORK` bật `AllowMsgSchedules` (`CreateOrUpdateStream` sửa stream cũ). Kind `MemberCountCheck` đăng ký tạm với danh sách effect rỗng tới Task 15.

**Kỹ thuật.** Change stream trả từng thao tác trong transaction như event riêng; decoder không cần biết transaction. Upsert chèn → `insert`; pipeline update trên tombstone → `update` có `ver` (và `read_ver`) trong `updatedFields` → chỉ `MemberChanged` (giữ nhánh `replace` cho an toàn). `MarkRead`/`MarkUnread` (`$set read_seq`, `$inc read_ver`) → `update` chỉ có `read_seq`, `read_ver` → `ReadChanged`.

**Test.**
- `storetest/feed_member_cases.go`: create → change member của từng doc tạo; `AddMembers` (mới, thêm lại) → change mỗi doc đổi; thêm người active → không change; `ApplyMember` → change; `ChangeOwners` → hai change theo thứ tự kế nhiệm rồi đích; `MarkRead`, `MarkUnread` thực sự đổi → một change `ReadChanged` mang `read_ver` mới, không đổi → không change; `ClearHistory` nâng mốc → một `HistoryCleared`, không nâng → không; `HideMessage` mới → một `MessageHidden`, ẩn lại → không; `AddMemberCount`, `SetMemberCount` → không change.
- `feed_member_change_test.go`: `TestDecodeChangeReadsMemberUpdatesFromTheKeyAndVer`, `TestDecodeChangeReadsMemberInsertsAndReplacesFromTheDocument`, `TestDecodeChangeReadsReadPositionUpdates` (chỉ `read_ver` → `ReadChanged`; có cả `ver` → `MemberChanged`), `TestDecodeChangeRejectsBrokenMemberChanges`.
- `feed_change_test.go`: `TestFeedPipelineLetsOnlyReactionAndMemberChangesThrough`; ca "members insert" của test collection lạ đổi sang `hidden`.
- `feed_skip_integration_test.go`: `TestFeedSkipsSummaryPinActivityAndCountWrites` (bỏ ca ẩn/clear khỏi danh sách bỏ qua; thêm ẩn lại, clear không nâng mốc, `AddMembers` no-op, `AddMemberCount`, `SetMemberCount`, `$inc owners_ver`).
- `TestMongoMemberFeedContract`, `TestMemberFeedContract` (memstore).
- `work`: `TestKnownKindsAreTheNineChangeKinds`; record member/đọc/ẩn/clear phải có user hợp lệ, kind khác không được có user; id `g:777-mb-alice-v1`, `d:777-rd-alice-v3`, `h:777-hd-alice-0-5`, `c:777-cl-alice-…`.
- `reconcile`: `TestForwardsARoomInsertAndItsCreationMember`, `TestForwardsAReadPositionChange`; stats đếm thêm record member và đọc.

- `timers_test.go`: record `MemberCountCheck` mã hoá/giải mã, id `k:777-…`; `Arm` dùng đúng subject, thời điểm, target theo slot.
- `timers_integration_test.go` (NATS 2.15 thật): `TestRealJetStreamFiresAnArmedTimerAfterItsDelay` (delay 1s trong test → record tới `work.p{n}` sau ~1s, không sớm hơn); `TestRealJetStreamNeverFiresADisarmedTimer` (Arm rồi Disarm → chờ delay + 1s, không có gì tới); `TestEnsureStreamTurnsOnSchedulesOnAnOldWorkStream`. NATS từ chối `AllowMsgSchedules` trên stream WorkQueue, hoặc phiếu đã xoá vẫn bật → **dừng, báo** kèm lỗi nguyên văn.

**Lệnh.** Unit các package đụng; `make itest`; rồi `git push origin feat/m2b`.

**Done khi.** Unit + itest xanh; push xong.

**Commit.** `feat(store): feed membership, read position, hide and clear changes into the work stream`; rồi `feat(work): arm and disarm member count check timers with nats message schedules` — `apps/core/internal/work/ INDEXES.csv` — `apps/core/internal/store/ apps/core/internal/work/ apps/core/internal/reconcile/forward_test.go apps/core/internal/reconcile/stats_test.go apps/core/effects_wiring.go INDEXES.csv`.

**Review:** có (★).

---

### Task 7: ★ `pkg/lru` + `dedupe.Requests`

**Mục tiêu.** Máy dedupe cho lệnh `AddMembers` (D99): gửi lại cùng `request_id` trả kết quả cũ và không bao giờ áp lại; dùng lại nguyên máy cid (Lua, batcher, TTL).

**Files.** Tạo `pkg/lru/lru.go`, `pkg/lru/lru_test.go`. Xoá `apps/core/internal/actor/lru_cache.go`; đổi tên `actor/lru_cache_internal_test.go` → `actor/cid_cache_internal_test.go` (bỏ phần test LRU, giữ test cid cache); sửa `actor/cid_dedupe.go`, `room_actor.go`, `room_state.go`. Sửa `dedupe/records.go`, `batcher.go`, `batcher_harness_test.go`; tạo `dedupe/requests.go`, `requests_harness_test.go`, `requests_test.go`, `requests_redis_test.go`, `requests_integration_test.go`. `INDEXES.csv`: dòng mới `pkg/lru`; `dedupe`, `actor`.

**Todo.**
1. Chuyển LRU generic của `actor` sang `pkg/lru` với API exported theo hợp đồng; `actor` dùng `lru.Cache`.
2. `dedupe.Key.Space`, `Key.String()`, `Registry` exported, `RequestKey`, `Requests` theo hợp đồng.
3. `Begin` theo bảng dưới; `Finish` = `Commit` + nạp LRU; `Cancel` = `Abort`; cả hai không chặn (batcher xếp hàng).

| Verdict từ `Reserve([key])` | `Begin` trả |
|---|---|
| LRU cục bộ có mục còn hạn | `RequestDone` |
| `Reserved`, `Absent` | `RequestNew` |
| `Committed` | nạp LRU với `Record.CreatedAt`, `RequestDone` |
| `PendingHere`, `PendingElsewhere` | `RequestBusy` |
| lỗi Redis, `ErrDegraded`, batcher đóng, thiếu verdict | `RequestNew` (chỉ còn LRU, như CD2) |

**Kỹ thuật.** LRU của `Requests`: mutex + `lru.Cache[Key, time.Time]`, cỡ `RequestCacheSize`; mục còn hiệu lực khi tuổi (tính từ giờ lệnh) < `ttl`, nên hết hạn cùng lúc khoá Redis. `Begin` chỉ trả lỗi khi `ctx` đã huỷ. `parseCommitted` từ chối seq 0, nên `Record.Seq` của lệnh = số user (≥ 1). `SpaceCID` là zero value nên mọi literal `Key{Room, User, CID}` cũ giữ nghĩa.

**Test.**
- `lru_test.go`: `TestCacheEvictsTheLeastRecentlyUsed`, `TestRemoveForgetsOneKey`, `TestLenCountsLiveEntries`.
- `requests_test.go` (registry giả, `synctest` cho TTL): `TestRequestKeysLiveInTheirOwnNamespace`, `TestNewRequestsNeedsARegistryAndAPositiveTTL`, `TestBeginMapsTheRegistryVerdict` (bảng trên), `TestFinishAnswersFromMemoryUntilTheTTL`, `TestACommittedVerdictIsRememberedUntilItsCommandExpires`, `TestCancelAbortsTheReservation`, `TestBeginFailsOnlyWhenTheCallerGaveUp`.
- `requests_redis_test.go` (miniredis, `Store` thật): `TestRequestsOnRedisKeepTheirOwnKeys` (khoá `chatim:req:` tách `chatim:cid:`), `TestARestartedCoreSeesTheCommittedRequest` (`Requests` mới, cùng Redis → `RequestDone`), `TestTwoCoresSeeABusyRequest`, `TestRequestsFallBackToMemoryWhileRedisIsDown`.
- `requests_integration_test.go`: `TestRealRedisRequestLifecycle` (skip khi thiếu `CHATIM_IT_REDIS_DEDUPE_ADDR`).
- Test cid cũ của `actor` và `dedupe` xanh nguyên.

**Lệnh.** Unit `./pkg/lru/... ./apps/core/internal/dedupe/... ./apps/core/internal/actor/...` (thêm `-count=5` cho `dedupe`); `make itest`.

**Done khi.** Xanh; `actor/lru_cache.go` không còn; `Requests` chưa ai gọi (tới Task 11).

**Commit.** `feat(dedupe): request dedupe on its own key space and a shared lru package` — `pkg/lru/ apps/core/internal/dedupe/ apps/core/internal/actor/ INDEXES.csv`.

**Review:** có (★).

---

### Task 8: ★ Actor: cache member theo thế hệ + TTL, `Router.ForgetMembers`

**Mục tiêu.** Người bị xoá mất quyền gửi ngay trên core xử lý lệnh, và tối đa 10s trên core khác (D106). Hôm nay `actor` giữ member trong LRU không hết hạn, không ai xoá.

**Files.** Tạo `actor/member_cache.go`, `actor/member_cache_test.go`; sửa `actor/room_state.go` (bỏ hàm `member` cũ), `room_actor.go`, `export_test.go`, `router.go` (hoặc file router phù hợp còn chỗ). `INDEXES.csv`: `actor`.

**Todo.**
1. Actor có bộ đếm thế hệ atomic và thế hệ đã thấy; `Router.ForgetMembers(room)` giữ khoá đọc của router, actor của room có thì tăng thế hệ.
2. Đầu mỗi lần tra member: thế hệ khác → bỏ cache cũ (LRU mới), ghi thế hệ đã thấy; thế hệ được đọc **trước** khi đọc store.
3. Mục cache `{member, lúc đọc}`; quá `memberCacheTTL` → đọc lại.
4. Kết quả "không phải member" (`ErrNotMember` từ `Rooms.Member`, đã lọc `state`) không cache; mục cũ của user đó bị `Remove`.

**Kỹ thuật.** Đọc thế hệ trước khi đọc store bảo đảm một lần đọc cũ chen giữa lệnh member và `ForgetMembers` chỉ sống tới lệnh kế tiếp của actor. TTL đo bằng đồng hồ của actor (`time.Now`, giả được trong `synctest`). `room_actor.go` đang 170 dòng: logic để trong `member_cache.go`.

**Test (`synctest` + goleak, `-count=5`).** Xoá/thêm bob thẳng trên memstore qua port Task 4.
- `TestForgetMembersDropsARemovedMemberAtOnce` — bob gửi (cache), tombstone bob, `ForgetMembers` → lần gửi sau `PERMISSION_DENIED`.
- `TestCachedMembershipExpiresAfterTheTTL` — không `ForgetMembers`: trong 10s vẫn gửi được, sau 10s bị từ chối.
- `TestARemovedMemberIsNeverCachedSoAReAddWorksAtOnce`.
- `TestForgetMembersWithoutAnActorDoesNothing`.
- `TestForgetMembersIsSafeAlongsideSends` (`-race`).

**Lệnh.** `make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/actor/..."`; fmt/vet/lint. Không đụng `slot` nên không cần R5.

**Done khi.** Xanh; test actor cũ không đổi hành vi.

**Commit.** `feat(actor): forget cached members by generation and after a ttl` — `apps/core/internal/actor/ INDEXES.csv`.

**Review:** có (★).

---

### Task 9: ★ `access`: 6 action, `Request.Target`/`Role`, `DefaultPolicy`

**Mục tiêu.** Quyền member đi qua `access.Policy` như mọi action khác (D101).

**Files.** Sửa `access/policy.go`; tạo `access/member_policy_test.go`. `INDEXES.csv`: `access`.

**Todo.** Action (6 action mới, gồm `set_member_priority`), field `Request`, luật `DefaultPolicy` theo hợp đồng. `LockedKinds` chỉ áp sửa/xoá tin. `AllowMembers` không đổi (cho mọi thứ). `Checker` không đổi (`Admit` dựa `Rooms.Member` đã lọc `state`).

**Kỹ thuật.** Policy chỉ nhìn `Request.Member` (caller), `Request.Target`, `Request.Role`; không I/O. Người gọi truyền đích giả role `member` khi đích không có doc hoặc là tombstone (Task 11), nên admin xoá "người lạ" được cho qua policy rồi mới nhận `NOT_FOUND`, còn member thường nhận `PERMISSION_DENIED`.

**Test.**
- `TestMemberActionNames` (giá trị chuỗi).
- `TestDefaultPolicyMemberRules` — bảng: owner/admin/member × add/remove (đích owner/admin/member)/change_role/set_priority/leave/mark_read.
- `TestDefaultPolicyKeepsItsMessageRulesNextToMemberRules` — luật sửa/xoá cũ không đổi.
- `TestAllowMembersAllowsMemberActions`.

**Lệnh.** Unit `./apps/core/internal/access/...`; fmt/vet/lint.

**Done khi.** Xanh.

**Commit.** `feat(access): member and read actions with owner and admin rules` — `apps/core/internal/access/ INDEXES.csv`.

**Review:** có (★).

---

### Task 10: ★ Package `ownership` (luật owner thuần)

**Mục tiêu.** Luật owner (D100) là hàm thuần chạy bên trong `ChangeOwners`: chọn kế nhiệm, chặn owner cuối tự hạ, lập kế hoạch ghi với kế nhiệm trước. Bất biến: **group còn member active thì còn owner active**.

**Files.** Tạo `apps/core/internal/ownership/plan.go`, `successor.go`, `plan_test.go`, `successor_test.go`, `race_test.go`. `INDEXES.csv`: dòng mới `apps/core/internal/ownership`.

**Todo.** `Action`, `Request`, `Affects`, `Successor`, `Plan` theo hợp đồng. `Plan(r, v)`:
1. Tìm caller và đích trong `v.Docs` (rời: caller = đích). Caller không có hoặc không active → `domain.ErrNotMember`.
2. Gọi `r.Allow(caller, target)` với đích thật khi đích active, hoặc đích giả `{User: r.Target, Role: member}` khi không có doc hoặc tombstone; lỗi trả nguyên.
3. Đích không có doc → `ErrMemberNotFound` (xoá, đổi role). Đích không active: xoá/rời → kế hoạch rỗng (no-op); đổi role → `ErrMemberNotFound`. Đổi role về đúng role hiện tại → rỗng.
4. Trạng thái mới của đích: rời/xoá → `Next(role giữ, removed, …)`; đổi role → `Next(r.Role, active, …)`; `RequestID`, `UpdatedBy = Caller`, `UpdatedAt = At`.
5. Nếu đích là owner active và trạng thái mới không còn là owner active: các owner khác = `v.Owners` trừ đích. Còn ít nhất một → chỉ ghi đích. Không còn: đổi role → `ErrLastOwner`; rời/xoá → `Successor(v.Candidates)`; có → write kế nhiệm `Next(owner, active, …)` **trước**, rồi đích; không có → chỉ ghi đích (group rỗng).
6. Còn lại (nâng lên owner, hoặc đích không phải owner) → chỉ ghi đích.

**Kỹ thuật.** Không I/O, không đồng hồ: `At` và `RequestID` đến từ `Request`. `v.Owners` có tối đa 2 phần tử là đủ để biết "đích có phải owner cuối". Mọi write mang `Cur` là doc trong view để store CAS theo `ver`.

**Test.**
- `successor_test.go`: `TestSuccessorPrefersAnAdmin`, `TestAmongOneRoleTheHighestPriorityWins`, `TestEqualPrioritiesGoToTheEarliestJoinThenTheSmallestUserID`, `TestNoCandidateMeansNoSuccessor`.
- `plan_test.go`: `TestTheLastOwnerLeavingPromotesTheSuccessorFirst` (thứ tự write, field của cả hai); `TestAnOwnerLeavingBesideAnotherOwnerNamesNoSuccessor`; `TestTheOnlyMemberLeavingEmptiesTheGroup`; `TestTheLastOwnerCannotStepDown` (`ErrLastOwner`); `TestPromotingToOwnerWritesOnlyTheTarget`; `TestPlanIsDesiredState` (rời tombstone, đổi về cùng role → rỗng); `TestPlanChecksTheCallerAndAsksAllowWithTheFreshDocs` (caller tombstone → `ErrNotMember`; `Allow` nhận đúng doc; đích thiếu → đích giả role member trước `ErrMemberNotFound`; đích tombstone từng là admin → đích giả role member, kế hoạch rỗng); `TestAffectsOnlyOwnerTargetsAndPromotions`.
- `race_test.go` (memstore + `ChangeOwners` thật): `TestTwoLastOwnersLeavingAtOnceKeepAnOwner` — `decide` của A chạy trọn `ChangeOwners` của B bên trong → A nhận `ErrRetryLater`, B thắng; gọi lại A → A thấy mình là owner cuối, kế nhiệm được nâng trước; cuối cùng còn đúng một owner. `TestOwnersRemovingEachOtherLeaveOneOwner` — cùng cách; bên thua gọi lại → `ErrNotMember`. `TestConcurrentOwnerLeavesNeverOrphanAGroup` — 24 room × 2 goroutine thật, mỗi goroutine gọi lại khi `ErrRetryLater` (vòng gọi lại nằm trong **test**, đóng vai app), `-race`; mọi room còn member thì còn owner.

**Lệnh.** `make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/ownership/..."`; fmt/vet/lint.

**Done khi.** Xanh; package không import driver hay `mutate`.

**Commit.** `feat(ownership): plan owner changes with the successor first` — `apps/core/internal/ownership/ INDEXES.csv`.

**Review:** có (★).

---

### Task 11: ★ `mutate`: `AddMembers`, `RemoveMember`, `LeaveRoom`, `ChangeMemberRole`, `SetMemberPriority`

**Mục tiêu.** Năm lệnh member trên lớp tập (D98–D102). Lệnh là **trạng thái mong muốn**: đúng sẵn thì thành công, không ghi, không event. Không vòng thử lại.

**Files.** Sửa `mutate/limits.go`, `mutator.go`; tạo `mutate/members.go`, `member_change.go`, `member_owner_path.go`; sửa `mutate/fixtures_test.go`, `delete_test.go`, `limits_test.go`; tạo `mutate/member_helpers_test.go`, `members_test.go`, `member_change_test.go`, `member_rules_test.go`, `member_race_test.go`. Sửa `grpcsrv/fake_dependencies_test.go`; `apps/core/service_wiring.go`, `apps/core/wiring.go`. `INDEXES.csv`: `mutate`, `apps/core`.

**Todo.**
1. `Limits.MemberBatch`, `MemberBatch()`, `Deps` mới, lệnh và kết quả theo hợp đồng.
2. **`AddMembers`**:
   1. `ValidCID(RequestID)`; từng user `ValidUser`; khử trùng giữ thứ tự; rỗng → invalid users; quá `MemberBatch` → `ErrTooManyMembers`.
   2. `Admit(add_members)` → DM → `ErrDirectRoom` → `Allow`.
   3. `Requests.Begin(RequestKey(room, caller, request_id))`: `Busy` → `ErrRetryLater`; `Done` → `MembersOf(room, users)` lọc `AddedBy(doc, request_id, caller)` → trả (không ghi, không event); `New` → bước 4.
   4. `Timers.Arm(room)` (lỗi → `Requests.Cancel`, `ErrRetryLater`) → `Messages.Last(room, 0)` một lần → `Members.AddMembers(Join{…, At: now, ReadSeq: last}, users)`; lỗi → `Requests.Cancel`, trả lỗi (phiếu để nguyên, bật thì đếm lại vô hại).
   5. `Forget.ForgetMembers(room)` → `JoinResult.Changed > 0` thì `AddMemberCount(room, Changed)` (thành công → `Disarm`; lỗi → log, không `Disarm`, đi tiếp); `Changed == 0` → `Disarm` → lọc `AddedBy` → **một** `Events.Enqueue` gồm `pbconv.MemberEvent` của từng người được thêm rồi `MemberCountChanged` (khi `$inc` thành công) (lỗi enqueue bỏ qua) → `Requests.Finish(key, Record{Seq: len(users), CreatedAt: now})` → trả doc đã thêm.
3. **`RemoveMember`, `LeaveRoom`, `ChangeMemberRole`, `SetMemberPriority`**:
   1. Validate: xoá chính mình → `ErrInvalidArgument`; đích `ValidUser`; role hợp lệ.
   2. `Admit(action)`. `LeaveRoom` gặp `ErrNotMember` → `MembersOf` có tombstone → `{Member: doc, Changed: false}`; không có doc → `ErrNotMember`. Room DM → `ErrDirectRoom`.
   3. Một `NewRequestID()` và một `now` cho cả lệnh.
   4. `MembersOf(room, [target])` (rời: đích = caller doc từ `Admit`).
   5. `Allow(Request{Member: caller, Target: doc active hoặc đích giả role member (không có doc / tombstone), Role})` **trước** mọi kết luận về đích.
   6. Đích thiếu → `ErrMemberNotFound`; tombstone: xoá → no-op, đổi role/priority → `ErrMemberNotFound`; cùng role/priority → no-op (`PreviousRole`/`PreviousPriority` = giá trị hiện tại).
   6b. `SetMemberPriority` không bao giờ đụng owner nên luôn đi đường thường (`ownership.Affects` trả false); DM → `ErrDirectRoom`; mọi `int32` hợp lệ.
   7. `ownership.Affects(doc, action, role)` → đường transaction: `Members.ChangeOwners(room, [caller, target], decide)` với `decide` là closure gọi `ownership.Plan` (truyền `Allow` = gọi lại `Access.Allow` với doc mới). Kết quả rỗng → no-op; có → `Successor` là write đầu khi có hai write; số member lấy từ `OwnerResult.Count` khi `CountChanged`.
   8. Không thì: xoá/rời một doc active → `Timers.Arm` trước (lỗi → `ErrRetryLater`); `ApplyMember(doc, doc.Next(…))`; `false` → `Disarm`, `ErrRetryLater` **ngay**; khớp → `AddMemberCount(room, −1)` → thành công thì `Disarm`, lỗi thì log, không `Disarm`, vẫn thành công. Đổi role/priority không `Arm`.
   9. Đổi → `Forget` → một `Enqueue` gồm `MemberEvent` của từng doc đã ghi theo thứ tự (kế nhiệm trước), rồi `MemberCountChanged` khi số đã đổi → `MemberResult`.
4. `mutate.New` bắt buộc các dep mới; sửa rig `mutate`, `TestNewRequiresEveryDependency`, rig `grpcsrv` (`fake_dependencies_test.go`: router của rig làm `Forget` nếu có, không thì một forgetter rỗng; `dedupe.NewRequests` trên registry chấp nhận mọi thứ), và `apps/core/service_wiring.go` chuyển sang một struct tham số `serviceDeps` (store, router, publisher, cid batcher, config, logger) để dựng `dedupe.NewRequests(cidBatcher, cfg.Dedupe.CommittedTTL)`, truyền router làm `Forget` và logger (đã redaction) làm `Log`.

**Kỹ thuật.**
- `Last` đọc một lần trước ghi; người vào thấy toàn bộ lịch sử, tin cũ coi như đã đọc.
- Khe kiểm-rồi-ghi ở đường thường được chấp nhận (§8 bản tóm tắt): admin bị hạ sau khi `Admit` nhưng trước ghi thì lần xoá vẫn rơi. Đường transaction kiểm lại caller và policy trên doc đọc trong transaction.
- `ErrRetryLater` chỉ sinh từ CAS trượt, transaction va chạm hoặc `RequestBusy`; core không tự gọi lại.
- `$inc` số member nằm **sau** ghi member và không bao giờ biến lệnh thành lỗi: gửi lại sau khi member đã ghi luôn có `Changed 0`, nên trả lỗi cũng không sửa được số. Phiếu hẹn (đặt trước ghi member, chỉ xoá khi `$inc` xong) bật sau `MEMBER_COUNT_CHECK_DELAY` và worker `member_count_repair` sửa (Task 15), kể cả khi core chết giữa chừng.
- Test member dùng room riêng tạo bằng `domain.NewRoom` để doc có đủ field; room fixture cũ không dùng cho member. Dedupe dùng `dedupe.Requests` thật trên một registry trong bộ nhớ có ngữ nghĩa Redis (pending/committed/abort), để test được "core khởi động lại" (`Requests` mới, cùng registry).

**Test.**
- `limits_test.go`: `TestMemberBatchDefaultsTo500AndChecksBounds` (1 và 1001 lỗi).
- `members_test.go`: `TestAddMembersWritesForgetsAnnouncesAndCommitsTheRequest`; `TestAddingActiveMembersChangesNothing`; `TestReAddingRestoresAccessAsAMemberAndRaisesTheReadPosition`; `TestARetryWithTheSameRequestNeverReAddsSomeoneRemovedSince` (kể cả sau "khởi động lại"); `TestARequestStillRunningElsewhereIsRetryLater`; `TestAFailedAddCancelsTheRequestAndEventFailuresAreIgnored`; `TestABatchOfTheMaximumSizeIsOneWrite`; `TestAddMembersRaisesTheCountByTheDocsItChanged` (thêm 3 trong đó 1 đã active → +2, event số member sau event member; gửi lại → không `$inc`); `TestAFailedCountIncrementStillSucceedsAndLeavesTheTimerArmed` (store giả lỗi `AddMemberCount` → reply OK, phiếu không bị xoá, không event số member); `TestTheTimerIsArmedBeforeTheMemberWriteAndDisarmedAfterTheCount` (thứ tự gọi); `TestAFailedArmWritesNothingAndIsRetryLater`.
- `member_change_test.go`: `TestRemoveMemberLeavesATombstoneAndRepeatsAsANoOp` (gồm admin xoá một cựu admin đã rời → OK, `Changed = false`); `TestLeaveRoomIsDesiredState` (tombstone → no-op; chưa từng là member → `PERMISSION_DENIED`); `TestChangeMemberRoleIsDesiredState`; `TestSetMemberPriorityIsOwnerOnlyAndDesiredState` (owner đặt → event `member_priority_changed`, `ver + 1`; admin → `PERMISSION_DENIED`; cùng số → no-op; không đổi số member); `TestOwnerChangesGoThroughTheOwnerTransaction` (owner cuối rời → kế nhiệm admin, `Successor` trong kết quả, event kế nhiệm trước đích, số member −1 từ transaction, không gọi `AddMemberCount`, không `Arm`); `TestRemovingAnActiveMemberLowersTheCountOnce` (xoá lại tombstone → không `Arm`, không `$inc`; đổi role → không `Arm`).
- `member_rules_test.go`: `TestMemberCommandsFollowTheDefaultPolicy` (gồm "member removes zed" và "member sets role of zed" → `PERMISSION_DENIED`, owner/admin xoá zed → `NOT_FOUND`); `TestMemberCommandsAskThePolicyWithCallerTargetAndRole`; `TestDirectRoomsKeepTheirTwoMembers`; `TestMemberCommandsRejectBadInputFirst`; `TestTheLastOwnerCannotStepDown`.
- `member_race_test.go` (wrapper store chạy "đối thủ" ngay trước `ApplyMember`/trong `decide`): `TestALostCASIsRetryLaterAndWritesNothing`; `TestAnAdminDemotedJustBeforeTheWriteStillRemoves` (khe chấp nhận); `TestATargetPromotedMeanwhileMakesThePlainWriteFail` (CAS trượt → `ErrRetryLater`; gọi lại → đi đường transaction); `TestTwoOwnersLeavingAtOnceOneGetsRetryLater`.
- Test `mutate` cũ, rig `grpcsrv` xanh.

**Lệnh.** Unit `./apps/core/internal/mutate/... ./apps/core/internal/grpcsrv/... ./apps/core/`; fmt/vet/lint; `make itest` (wiring đổi).

**Done khi.** Xanh; `mutate` không có vòng lặp thử lại nào (`grep -n 'for .*tries\|MemberTries' apps/core/internal/mutate` rỗng).

**Commit.** `feat(mutate): add, remove, leave, change role and priority on member docs with the member count` — `apps/core/internal/mutate/ apps/core/internal/grpcsrv/fake_dependencies_test.go apps/core/service_wiring.go apps/core/wiring.go INDEXES.csv`.

**Review:** có (★).

---

### Task 12: ★ `CreateRoom`: trần mỗi lệnh, `member_added` cho từng người tạo, bỏ trần 5000

**Mục tiêu.** Mọi người vào room, kể cả lúc tạo, đều có event `member_added` (D104); giới hạn chuyển thành mỗi lệnh (D107).

**Files.** Sửa `domain/validate.go`, `domain/room_test.go`; `grpcsrv/create_room.go`; `grpcsrv/create_room_event_test.go`, `create_room_test.go`, `change_message_test.go`, `react_pin_test.go`; tạo `grpcsrv/room_created_ids_test.go` (helper `createdIDs`, để `harness_test.go` 185 dòng không lớn thêm). `INDEXES.csv`: `grpcsrv`, `domain`.

**Todo.**
1. `domain.NewRoom` bỏ `maxGroupMembers`; DM vẫn đúng 2, group ≥ 1 và có người tạo.
2. `CreateRoom`: `len(req.Members) > mutator.MemberBatch()` (đếm thô, cả user lặp) → `ErrTooManyMembers` trước `NewRoom`.
3. Sau `Create`, **một** `Enqueue` gồm `RoomCreated(room)` rồi `MemberEvent(room.Type, m)` của từng member (doc `ver 1`); lỗi enqueue bỏ qua như cũ. Không phát `member_count_changed` lúc tạo (`room_created` đã mang số member; `member_count_ver = 1`).

**Kỹ thuật.** Worker `member_event` (Task 15) dựng lại đúng các event đó từ doc; stream bỏ trùng theo id.

**Test.**
- `room_test.go`: group 6000 member tạo được; DM vẫn đúng 2.
- `TestCreateRoomEnqueuesRoomCreatedAndTheFirstMembers` — đúng id `{room}-created` rồi `{room}-mb-{u}-v1` cho mỗi member, `room_created` trên subject `room`, `member_added` trên subject `member`.
- `TestCreateRoomCapsTheMembersOfOneRequest` — 501 → `INVALID_ARGUMENT`, 500 → OK.
- Ba test cũ so danh sách event sau tạo group thêm id `member_added` của từng người.

**Lệnh.** Unit `./apps/core/internal/domain/... ./apps/core/internal/grpcsrv/...`; fmt/vet/lint.

**Done khi.** Xanh.

**Commit.** `feat(grpcsrv): announce the first members of a new room and cap members per request` — `apps/core/internal/grpcsrv/ apps/core/internal/domain/ INDEXES.csv`.

**Review:** có (★).

---

### Task 13: ★ `mutate.MarkRead`/`MarkUnread` + `read_updated`; event ẩn tin và xoá lịch sử

**Mục tiêu.** Vị trí đọc chỉ đổi doc của người đọc và phát **một** `read_updated` trên subject `member` mỗi lần vị trí thực sự đổi (D105). Không bao giờ đụng `ver`; vào feed thành `ReadChanged` (Task 6) để worker phát lại (Task 15).

**Files.** Tạo `mutate/read.go`, `mutate/read_helpers_test.go`, `mutate/read_test.go`; sửa `mutate/hide_clear.go`, `mutate/hide_clear_test.go`; sửa `mutate/mutator.go` (`Deps.Reads`), `fixtures_test.go`, `delete_test.go`; `grpcsrv/fake_dependencies_test.go`; `apps/core/service_wiring.go`. `INDEXES.csv`: `mutate`, `apps/core`.

**Todo.**
1. `MarkUnread` với `seq == 0` → `ErrInvalidArgument`.
2. `Authorize(mark_read)` (tenant + member active).
3. Kẹp theo seq cuối thật: `0 < seq ≤ room.LastSeq` (đã nạp cùng `Admit`) → dùng luôn; còn lại → `Messages.Last(room, 0)`; `seq == 0` hoặc `seq > last` → `last`.
4. Kết quả kẹp là 0 (room chưa có tin) → trả vị trí hiện tại của doc caller, không ghi.
5. `MarkRead` → `Reads.MarkRead(room, user, seq)`; `MarkUnread` → `Reads.MarkUnread(room, user, seq − 1)`.
6. Đổi → `Events.Enqueue(room, [pbconv.ReadUpdated(room, user, pos, now)])` (lỗi bỏ qua). Trả vị trí (đổi hay không).
7. **Ẩn tin** (D109): port `Hidden.Hide` trả thêm `bool` "mới ẩn"; mới → enqueue `MessageHidden(room, user, thread, seq, now)`; ẩn lại → không event. **Xoá lịch sử:** `ClearHistory` trả thêm `bool` "mốc đã tăng"; tăng → enqueue `HistoryCleared(room, user, clearedAt, now)`. Lỗi enqueue bỏ qua; worker Task 15 phát lại. (Đổi chữ ký port ở Task 1 nếu gọn hơn; ghi vào báo cáo.)

**Kỹ thuật.** DM đánh dấu đọc bình thường (luật DM cố định chỉ cho lệnh member). Không gọi `ForgetMembers`, không event member. Mất event ở đường nhanh thì worker `read_event` phát lại (Task 15).

**Test.**
- `TestMarkReadOnlyMovesForwardAndNeverTouchesTheMembership` (doc giữ mọi field trừ `read_seq/read_ver`).
- `TestMarkUnreadOnlyMovesBack` (`seq − 1`, seq 0 → `INVALID_ARGUMENT`).
- `TestEachChangeSendsOneReadUpdated` (một event subject `member`, actor = reader, id theo `read_ver`; không đổi → không event).
- `TestHidingSendsOneMessageHiddenAndHidingAgainSendsNothing`; `TestClearingSendsHistoryClearedOnlyWhenTheMarkRises`.
- `TestAnEmptyRoomKeepsTheReadPosition`; `TestReadNeedsAnActiveMember`; `TestTheClampReadsTheLastSeqOnlyPastTheRoomHead`; `TestDirectRoomsTrackReadPositionsToo`.

**Lệnh.** Unit `./apps/core/internal/mutate/... ./apps/core/internal/grpcsrv/... ./apps/core/`; fmt/vet/lint.

**Done khi.** Xanh.

**Commit.** `feat(mutate): mark read and unread, and announce hidden messages and cleared history` — `apps/core/internal/mutate/ apps/core/internal/grpcsrv/fake_dependencies_test.go apps/core/service_wiring.go INDEXES.csv`.

**Review:** có (★).


---

### Task 14: ★ Config (2 env) + 7 RPC `grpcsrv` + README. **Push**

**Mục tiêu.** Mở 7 RPC cho client và env `MEMBER_BATCH_MAX` (D107), `MEMBER_COUNT_CHECK_DELAY` (D102). Không thêm bước dừng, kế hoạch dừng giữ 26.2s.

**Files.** Sửa `config/config.go`, `components.go`, `validate.go`; test `config/env_test.go`, `load_test.go`, `parse_test.go`, `validate_test.go`. Tạo `grpcsrv/members.go`, `grpcsrv/read.go`, `grpcsrv/members_test.go`, `grpcsrv/read_test.go`; sửa `grpcsrv/caller_identity_test.go`. Sửa `apps/core/wiring.go` (truyền `Limits.MemberBatch`). `README.md` bảng env. `INDEXES.csv`: `config`, `grpcsrv`, `apps/core`, `README.md`.

**Todo.**
1. Config theo bảng hợp đồng: `MEMBER_BATCH_MAX` → `Config.Limits.MemberBatch` (parse > 0, rồi `mutate.Limits` kiểm 2..1000); `MEMBER_COUNT_CHECK_DELAY` → `Config.MemberCountCheckDelay` (luật chéo `> CORE_REQUEST_DEADLINE`), truyền vào `work.NewTimers` ở wiring. Khoá lỗi thành phần gộp `MEMBER_BATCH_MAX` cạnh `REACTION_EMOJIS, PIN_LIMIT`.
2. `grpcsrv/members.go` (5 RPC) và `grpcsrv/read.go` (2 RPC): `callerAndRoom` → `Mutator.*` → response theo bảng Task 3. `ChangeMemberRole` đổi role qua `pbconv.DomainMemberRole` **sau** khi kiểm caller. `LeaveRoom` trả `new_owner` = `MemberResult.Successor`. `grpcsrv.Deps` không thêm field.
3. README: hai dòng env mới (mặc định, ràng buộc).
4. Push sau commit thứ hai.

**Kỹ thuật.** Mã lỗi đi qua `pkg/grpcserver` như cũ (bảng mã lỗi ở hợp đồng `mutate`). Client nhận `UNAVAILABLE` phải tự gọi lại; route client trong `tools` đã làm vậy.

**Test.**
- Config: `TestMemberBatchMaxDefaultsAndBounds` (0, 1, 1001 lỗi; 2, 1000 nhận); `TestMemberCountCheckDelayMustOutliveARequest`; bảng env của `env_test.go` có hai env mới; kế hoạch dừng mặc định vẫn 26.2s.
- `members_test.go`: `TestMemberChangesThroughTheService` (thêm, đổi role, đặt priority, xoá, rời với `new_owner`); `TestARemovedMemberCanNeitherSendNorReadUntilAddedBack`; `TestARetriedAddNeverBringsBackSomeoneRemovedSince`; `TestMemberErrorsKeepTheirCodes` (bảng: DM, owner cuối, người lạ, member xoá người lạ → `PERMISSION_DENIED`, admin xoá người lạ → `NOT_FOUND`, xoá chính mình, role `UNSPECIFIED`, quá batch, CAS trượt → `UNAVAILABLE`).
- `read_test.go`: `TestReadPositionThroughTheService` (`MarkRead(0)`, kẹp, `MarkUnread(0)` → `INVALID_ARGUMENT`; event `read_updated` có actor = caller).
- `caller_identity_test.go`: 7 RPC mới từ chối metadata thiếu tenant/user.

**Lệnh.** Unit `./apps/core/internal/config/... ./apps/core/internal/grpcsrv/... ./apps/core/`; fmt/vet/lint; `make itest`; `git push origin feat/m2b`.

**Done khi.** Xanh; push xong.

**Commit.**
- `feat(config): member batch cap and member count check delay` — `apps/core/internal/config/ INDEXES.csv`.
- `feat(grpcsrv): member and read position rpcs` — `apps/core/internal/grpcsrv/ apps/core/wiring.go README.md INDEXES.csv`.

**Review:** có (★).

---

### Task 15: ★ Effect `member_event`, `member_count_event`, `read_event`, `hidden_event`, `history_cleared_event`, `member_count_repair` + registry + metric + wiring

**Mục tiêu.** Bảo đảm mọi event member, số member, vị trí đọc, ẩn tin và xoá lịch sử được phát (D102, D104, D105, D109): worker phát lại trạng thái hiện tại từ record `MemberChanged`/`ReadChanged`; worker `member_count_repair` sửa số member khi phiếu hẹn bật.

**Files.** Sửa `effects/ports.go` (`MemberLookup`, `RoomLookup` nếu chưa có), `effects/event_publisher.go` (giữ `each`); tạo `effects/member_event.go`, `member_count_event.go`, `read_event.go`, `hidden_event.go`, `history_cleared_event.go`, `member_count_repair.go`, `member_count_repair_test.go`, `member_fixtures_test.go`, `member_event_test.go`, `member_count_event_test.go`, `read_event_test.go`, `member_constructors_test.go`. Sửa `apps/core/effects_wiring.go`, `effects_wiring_test.go`, `metrics_wiring.go`, `metrics_wiring_test.go`, `wiring.go` (một dòng nếu cần); tạo `apps/core/member_effects_wiring.go`. `INDEXES.csv`: `effects`, `apps/core`, `deploy/prometheus/alerts.yml`.

**Todo.**
1. **`member_event`** (delay `RECONCILE_DELAY`): mỗi record `MembersOf(room, [user])`; không có doc → drop; `doc.Ver > rec.Version` → nil (record mới hơn lo); `<` → `store.ErrStaleRead` (Nak); `==` → loại room (cache `roomTypes`) → `pbconv.MemberEvent` → publish (nil event → nil), chờ PubAck. `Republished` chỉ đếm PubAck không phải bản trùng. Drop khi room không còn hoặc doc hỏng (`ErrInvalidArgument`), như `reaction_event`.
2. **`member_count_event`** (delay `RECONCILE_DELAY`): gom record của lô theo room (dùng lại `groupRecords`/`recordRoom`); mỗi room một `Rooms.Get`; `MemberCountVer ≥ 1` → publish `MemberCountChanged(room, {MemberCount, MemberCountVer}, "", now)` và chờ PubAck (stream bỏ trùng theo id nếu đường nhanh đã phát); room không có → drop theo số record; lỗi → lỗi cho mọi record của room. Không đếm, không ghi.
3. **`read_event`** (delay `RECONCILE_DELAY`) cho `ReadChanged`: `MembersOf(room, [user])`; không có doc → drop; `doc.ReadVer > rec.Version` → nil; `<` → `ErrStaleRead`; `==` → publish `ReadUpdated(room, user, {doc.ReadSeq, doc.ReadVer}, now)`, chờ PubAck.
3b. **`hidden_event`**: `Hidden` có doc (user, room, thread, seq) → publish `MessageHidden` (giờ = `created_at` của doc); không có → drop. **`history_cleared_event`**: đọc doc member; `cleared_at` zero hoặc không có doc → drop; có → publish `HistoryCleared` với mốc **hiện tại** (id theo mốc, nên mốc cũ hơn đã bị vượt không phát lại).
4. Registry cuối thay dòng tạm Task 6 (bảng hợp đồng `effects`); wiring trong `member_effects_wiring.go` (`wireEffects` không đổi chữ ký); `counters()` thêm năm effect (republished + dropped). Câu help `dropHelp` trong `workerSources` thêm "member doc".
5. **`member_count_repair`** (record `MemberCountCheck`, delay 0): `Rooms.Get` (ver `v`) → `CountMembers` → bằng số đang lưu: publish lại `MemberCountChanged` hiện tại (khi `ver ≥ 1`), nil → khác: `Timers.Arm(room)` **trước** (lỗi → Nak, chưa ghi gì), rồi `SetMemberCount(room, v, n)`; `false` → lỗi (Nak, phiếu mới để nguyên); `true` → publish `MemberCountChanged`, tăng `counter_repaired_total{counter="members"}`. Không đọc lại, không vòng lặp. Room không có → drop.

**Kỹ thuật.** Không vòng lặp trong effect: lỗi → Nak, record quay lại. Chỉ trạng thái cuối được bảo đảm (event trung gian bị doc mới hơn vượt thì không phát lại), như `reaction_event`. `harness_test.go` của `effects` đang 199 dòng: fixture mới để ở `member_fixtures_test.go`.

**Test.**
- `member_event_test.go`: `TestMemberEventDeclaresItsPolicy`; `TestMemberEventRepublishesTheCurrentDoc`; `TestMemberEventAnnouncesMembersCreatedWithTheRoom`; `TestMemberEventSkipsANewerDocAndRetriesAnOlderOne`; `TestMemberEventDropsWhatIsGoneAndRetriesWhenTheStoreFails`.
- `member_count_event_test.go`: `TestMemberCountEventPublishesTheCurrentCountOncePerRoom`; `TestMemberCountEventDropsAGoneRoomAndRetriesWhenTheStoreFails`.
- `read_event_test.go`: `TestReadEventRepublishesTheCurrentPosition`; `TestReadEventSkipsANewerPositionAndRetriesAnOlderOne`.
- `hidden_event_test.go`, `history_cleared_event_test.go`: phát lại trạng thái hiện tại; drop khi không còn.
- `member_count_repair_test.go`: `TestRepairLeavesACorrectCountAlone`; `TestRepairFixesADriftedCountAndAnnouncesIt`; `TestRepairNaksWhenTheCountMovedDuringIt` (CAS trượt); `TestRepairArmsAnotherTimerWhenAnIncrementSlippedIn`; `TestRepairDropsAGoneRoom`.
- `member_constructors_test.go`: `TestNewMemberEffectsRejectBadInput`.
- `effects_wiring_test.go`: `TestEffectSetExportsEveryEffect` (registry `MemberChanged`, `ReadChanged`, `MessageHidden`, `HistoryCleared`, `MemberCountCheck` đúng thứ tự, delay không giảm); `metrics_wiring_test.go`: nhãn các effect mới và `counter_repaired_total{counter="members"}` có trên `/metrics`.
- Test `reaction_event`, `pin_event` cũ xanh nguyên.

**Lệnh.** Unit `./apps/core/internal/effects/... ./apps/core/` (`-count=5` cho `effects`); fmt/vet/lint; `make alerts-check` (16 luật, không đổi); `make itest`.

**Done khi.** Xanh; `make alerts-check` báo 16 luật.

**Commit.** `feat(effects): republish member, read, hidden and cleared events and repair member counts` — `apps/core/internal/effects/ apps/core/effects_wiring.go apps/core/effects_wiring_test.go apps/core/member_effects_wiring.go apps/core/metrics_wiring.go apps/core/metrics_wiring_test.go apps/core/wiring.go INDEXES.csv`.

**Review:** có (★).

---

### Task 16: ★ `memberwatch`: mọi core nghe event member, quên cache

**Mục tiêu.** Người bị xoá hoặc đổi role ở core khác bị actor của core này quên cache ngay khi event tới (D110), thường vài mili-giây; TTL 10s (Task 8) vẫn là chốt chặn.

**Files.** Tạo `apps/core/internal/memberwatch/watch.go`, `watch_test.go`, `watch_integration_test.go`; tạo `apps/core/memberwatch_wiring.go`; sửa `apps/core/wiring.go` (start/stop, một hai dòng), `apps/core/lifecycle*` nếu thứ tự start/stop nằm ở đó. `INDEXES.csv`: dòng mới `apps/core/internal/memberwatch`, `apps/core`.

**Todo.**
1. `memberwatch` theo hợp đồng: subscribe hai subject trên kết nối NATS core sẵn có của publisher (không JetStream, không consumer), tách room id từ token thứ 4, gọi `forget`.
2. Wiring: `forget = router.ForgetMembers`; start sau router, stop (Unsubscribe) trước router. Không thêm bước vào kế hoạch dừng (Unsubscribe tức thì; kế hoạch dừng giữ 26.2s).
3. Metric `chatim_core_member_cache_forgets_total` và `chatim_core_member_watch_malformed_total` (`CounterFunc`); không thêm luật alert.

**Kỹ thuật.** Event của chính core này cũng tới (quên hai lần, vô hại). Mất event hoặc NATS chậm thì TTL 10s chặn. Không giải mã protobuf: room id nằm trên subject.

**Test.**
- `watch_test.go` (`synctest` + goleak, subscriber giả): `TestAMemberRemovedSubjectForgetsItsRoom`, `TestARoleChangedSubjectForgetsItsRoom`, `TestOtherMemberEventsAreIgnored`, `TestABrokenRoomTokenIsCountedAndSkipped`, `TestStopUnsubscribes`.
- `watch_integration_test.go`: NATS thật, publish lên `live.acme.member.101.evt.member_removed` → `forget(101)`.
- Itest xuyên core ở Task 19.

**Lệnh.** Unit `./apps/core/internal/memberwatch/... ./apps/core/` (`-count=5`); fmt/vet/lint; `make itest`.

**Done khi.** Xanh.

**Commit.** `feat(core): forget cached members when another core changes them` — `apps/core/internal/memberwatch/ apps/core/memberwatch_wiring.go apps/core/wiring.go INDEXES.csv` (+ file lifecycle nếu sửa).

**Review:** có (★).

---

### Task 17: `/app resync` quét `members` và `hidden` + `/app recount`

**Mục tiêu.** Đổi member, vị trí đọc, xoá lịch sử và ẩn tin không đổi `last_seq`, nên resync (D81) phải quét riêng (D103, D109); lệnh vận hành sửa số member lệch (D102).

**Files.** Tạo `resync/world_test.go` (tách từ `scan_test.go` 187 dòng, chỉ di chuyển); sửa `resync/scan_test.go`, `edits_test.go`, `reactions_pins_test.go`; tạo `resync/members.go`, `resync/members_test.go`; sửa `resync/scan.go` (`Deps`, `Report`, bước theo room); `apps/core/resync_command.go` (truyền `Members: st`), `apps/core/resync_integration_test.go`. Tạo `apps/core/recount_command.go`, `recount_command_test.go`, `recount_integration_test.go`; sửa `apps/core/main.go`, `main_test.go`. `README.md` (lệnh `recount`). `INDEXES.csv`: `resync`, `apps/core`, `README.md`.

**Todo.**
1. Commit tách test trước (không đổi hành vi).
2. Sau `pin_actions`, mỗi room gọi `MembersBetween(room, from, to, store.MaxMemberScan)` qua `scanByTime` sẵn có (phân trang theo `last_change_at`, bỏ trùng mép trang theo id record; một thời điểm đầy cả trang → `ErrMemberPageFull`).
3. Mỗi doc cho record `{Kind: MemberChanged, Room, User, Version: m.Ver, CommittedAt: m.LastChangeAt}`, thêm `{Kind: ReadChanged, …, Version: m.ReadVer}` nếu `ReadVer ≥ 1`, và `{Kind: HistoryCleared, …}` nếu `ClearedAt` khác zero (doc đổi trong khoảng mất thì phát lại trạng thái hiện tại của cả ba; worker và stream bỏ trùng theo id); `Report.MemberRecords` đếm cả ba.
3b. Sau member, `Hidden.Between(room, from, to)` theo `created_at` (cùng cách phân trang) → record `MessageHidden`; `Report.HiddenRecords` (in `hidden_records=%d`).
4. `/app recount` theo hợp đồng `resync` (dispatch `main.go`, flag `-room` bắt buộc, `-dry-run`).

**Kỹ thuật.** Thứ tự trong một room: room record → tin (ngược) → fact sửa → reaction → ghim → member; worker không dựa vào thứ tự. Doc đổi nhiều lần trong khoảng mất chỉ cho record của `ver` cuối (chỉ trạng thái cuối). Room chỉ có đổi member trong khoảng mất không được chọn theo `activity_bucket`/`created_at`, phải chạy `-room`. Dòng `message_edits` `ver 0` được replay như record sửa thường; worker bỏ qua.

**Test.** `TestResyncPublishesTheCurrentMemberDocsOfTheLostRangeLast` (gồm record `ReadChanged`, `HistoryCleared` khi có); `TestResyncReplaysHiddenMessagesOfTheLostRange`; `TestRecountNeedsARoom` (`main_test.go`: `recount` thiếu `-room` lỗi); itest `TestRealInfraRecountRestoresADriftedCount` (sửa tay `member_count` trong Mongo, `-dry-run` in hai số khác nhau và không ghi; chạy thật → số đúng, `member_count_ver + 1`, event `{room}-members-v{ver}` tới `live.{t}.room.{rid}.evt.member_count_changed`); `TestResyncPagesMemberDocsByTimeWithoutRepeatingThePageEdge`; `TestResyncStopsWhenOneInstantHoldsMoreMemberChangesThanAPage`; itest resync có `member_records` > 0 với room tạo trong khoảng.

**Lệnh.** Unit `./apps/core/internal/resync/...`; `make itest`.

**Done khi.** Xanh; `scan_test.go` < 200 dòng.

**Commit.** `test(resync): move the scan test world into its own file` — `apps/core/internal/resync/scan_test.go apps/core/internal/resync/world_test.go`; rồi `feat(resync): replay member docs and hidden messages changed in the lost range` — `apps/core/internal/resync/ apps/core/resync_command.go apps/core/resync_integration_test.go INDEXES.csv`; rồi `feat(core): recount the members of a room on demand` — `apps/core/recount_command.go apps/core/recount_command_test.go apps/core/recount_integration_test.go apps/core/main.go apps/core/main_test.go README.md INDEXES.csv`.

**Review:** không.

---

### Task 18: Route + corecli + e2e member và vị trí đọc

**Mục tiêu.** Công cụ tay và phase e2e 5 kiểm reply lẫn event trên subject `room`, `member`, `message`.

**Files.** Tạo `tools/internal/route/members.go`, `route/fakes_members_test.go`; sửa `route/changes_test.go`. Tạo `tools/corecli/internal/e2e/members.go`, `members_check.go`, `members_test.go`; sửa `e2e/events.go` (`EventOf` giải mã payload member/số member/đọc). Tạo `tools/corecli/cmd_members.go`, `e2e_members.go` và các file `e2e_member_*.go` cần để mỗi file < 200 dòng; sửa `main.go`, `cmd_e2e.go`; `scripts/e2e.sh` (phase 5). `INDEXES.csv`: `tools/internal/route`, `tools/corecli`, `tools/corecli/internal/e2e`, `scripts/e2e.sh`.

**Todo.**
1. Route: bảy lệnh qua `inRoom`, retry như lệnh idempotent khác (`Unavailable`, `ResourceExhausted`, `DeadlineExceeded`, `Aborted`), giữ nguyên request (cùng `request_id`).
2. corecli: lệnh theo hợp đồng; `-user` đã là cờ người gọi nên đích dùng `-target`.
3. Phase 5 `corecli e2e members` trên room e2e của phase 1 (chỉ `e2e-user` là owner; gọi `c0` = số member lúc tạo, `member_count_ver` 1), sau phase 4; subscribe `live.{t}.*.{rid}.>` của room và DM trước mọi lệnh; "chờ" = chờ theo id, kind, subject, payload trong `-wait`.

| # | Hành động | Reply mong đợi | Event mong đợi |
|---|---|---|---|
| 1 | `e2e-user` tạo DM với `e2e-bob`; trên DM: thêm, xoá, rời, đổi role; gửi một tin; `e2e-bob` `MarkRead(0)` | bốn lệnh member `FAILED_PRECONDITION`; `{read_seq 1, read_ver 1}` | `{dm}-created` (room), `{dm}-mb-e2e-user-v1`, `{dm}-mb-e2e-bob-v1` (member), tin (message), `{dm}-rd-e2e-bob-v1` (member) |
| 2 | thêm `e2e-bob`, `e2e-carol` với `request_id` `e2e-add-1`; lặp lại | cả hai lần `added [bob:1, carol:1]` | `member_added` mỗi người (member); `{room}-members-v2` số `c0+2` (room); lần lặp không có event mới |
| 3 | `e2e-bob` đọc lịch sử; `MarkRead(0)` | đủ N tin; `{N, 1}` (vào room đã coi như đọc hết) | — |
| 4 | đặt `e2e-bob` = admin; lặp; đặt priority `e2e-bob` = 5; lặp | `{changed, ver 2, previous member}`; lặp `{ver 2}`; `{changed, ver 3, previous_priority 0}`; lặp `{ver 3}` | `{room}-mb-e2e-bob-v2` (`member_role_changed`), `{room}-mb-e2e-bob-v3` (`member_priority_changed`) (member) |
| 5 | `e2e-bob` xoá `e2e-user` (owner); `e2e-bob` xoá `e2e-carol`; lặp | `PERMISSION_DENIED`; `{changed, ver 2}`; lặp `{ver 2}` | `{room}-mb-e2e-carol-v2` (member); `{room}-members-v3` số `c0+1` (room) |
| 6 | `e2e-carol` gửi, đọc lịch sử, `MarkRead` | cả ba `PERMISSION_DENIED` | — |
| 7 | gửi lại `AddMembers` cùng `e2e-add-1`; `e2e-carol` đọc lịch sử | `added []`; `PERMISSION_DENIED` | — |
| 8 | `e2e-bob` `MarkUnread(N)`, rồi `MarkRead(0)` | `{N−1, 2}`, rồi `{N, 3}` | `{room}-rd-e2e-bob-v2`, `{room}-rd-e2e-bob-v3` (member) |
| 9 | `e2e-bob` ẩn seq 1; `e2e-bob` `ClearHistory`; `e2e-user` gửi seq N+1; `e2e-bob` đọc 2 tin mới nhất | ẩn OK; có `cleared_at`; seq N ẩn, seq N+1 hiện | `{room}-hd-e2e-bob-0-1` (`message_hidden`), `{room}-cl-e2e-bob-{ms}` (`history_cleared`) (member); tin N+1 (message) |
| 10 | `e2e-user` (owner cuối) rời; lặp; `e2e-user` đọc lịch sử | `{changed, ver 2, new_owner e2e-bob}`; lặp `{ver 2}`; `PERMISSION_DENIED` | `{room}-mb-e2e-bob-v4` (`member_role_changed` owner ← admin) rồi `{room}-mb-e2e-user-v2` (`member_removed` left) (member); `{room}-members-v4` số `c0` (room) |
| 11 | `e2e-bob` thêm lại `e2e-user` (`e2e-add-3`), lặp; đặt `e2e-user` = owner, lặp; `e2e-user` `MarkRead(0)` | `added [e2e-user:3]` hai lần; `{changed, ver 4}`/`{ver 4}`; `{N+1, 1}` | `{room}-mb-e2e-user-v3` (`member_added` mang `read_seq N+1`, `read_ver 1`), `{room}-mb-e2e-user-v4` (member); `{room}-members-v5` số `c0+1` (room) |

Sau bước 11: phase 5 check (`e2e check` như cũ, người gọi `e2e-user`). Reply no-op chỉ so `changed` và `ver`. Mỗi event kiểm cả subject (`live.{t}.{room|member|message}.{rid}.evt.{kind}`).

**Kỹ thuật.** Số và `member_count_ver` tất định vì các lệnh chạy tuần tự và `$inc` nằm trong lệnh. Vào room (lần đầu hay vào lại) đặt `read_seq` = tin cuối và tăng `read_ver` (`Join.Apply`), nên `MarkRead(0)` ngay sau đó không đổi gì; vị trí đọc mới đi trong payload `member_added`. `CheckEvents`, `CheckChangeEvents`, `CheckMarkEvents` lọc theo kind nên bỏ qua event member.

**Test.** Route: `TestMemberCallsRouteByRoomAndRetryWithTheSameRequest`. e2e package: `TestMemberIDsAndSubjects`; `TestEventOfReadsMemberAndReadEvents`; `TestCheckLiveFindsEachWantedEventByID`; `TestCheckMemberReplyAndCleared`.

**Lệnh.** Unit `./tools/...`; `make image TARGET=apps/core`, `make core-up`, `make e2e`.

**Done khi.** `make e2e` xanh cả 5 phase.

**Commit.** `feat(route): route member and read position calls by room` — `tools/internal/route/ INDEXES.csv`; rồi `feat(corecli): add member and read position commands and an e2e member phase` — `tools/corecli/ scripts/e2e.sh INDEXES.csv`.

**Review:** không.

---

### Task 19: Itest xuyên phần member, owner và vị trí đọc

**Mục tiêu.** Khẳng định trên hạ tầng thật (skip khi thiếu `CHATIM_IT_*`), dùng helper sẵn có của `apps/core` (`realInfra`, `startCore`, `dialCore`, `createRoom`, `createRoomWith`, `callerAs`, `sendAs`, `historyAs`, `subscribeLive`, `itStore`, `itFastEffects`, `awaitLiveIDs`, `awaitStored`, `assertNoLiveIDs`, `retryingUnavailable`, `itLiveLimit`, `itTenant`, `itUser`) và helper mới ở `it_members_test.go`.

**Files.** Tạo `apps/core/it_members_test.go`, `member_owner_integration_test.go`, `member_events_integration_test.go`, `member_access_integration_test.go`, `member_feed_integration_test.go`. `INDEXES.csv`: `apps/core`.

**Test (mỗi test chứng minh một điều).**
- `TestRealInfraTwoOwnersLeavingAtOnceKeepAnOwner` — 20 vòng: `alice`, `bob` (owner) cùng rời qua `retryingUnavailable`; luôn còn đúng một owner khi còn member; người kế nhiệm là admin vào sớm nhất; `member_count` bằng số doc active.
- `TestRealInfraTwoOwnersRemovingEachOtherLeaveOneOwner` — đúng một thành công; bên kia sau khi gọi lại nhận `PERMISSION_DENIED`.
- `TestRealInfraEachChangeGoesToTheSubjectOfItsData` — tạo room, gửi tin, thêm người, đánh dấu đọc: `room_created`/`member_count_changed` trên `live.{t}.room.{rid}…`, `member_added`/`read_updated` trên `live.{t}.member.{rid}…`, `msg_created` trên `live.{t}.message.{rid}…`; mỗi thay đổi đúng một event.
- `TestRealInfraMemberCountFollowsEachCommand` — thêm 3 (1 đã ở room) → +2; xoá 1 → −1; `member_count` bằng đếm thật, mỗi event `member_count_changed` mang đúng số và `ver` đã lưu.
- `TestRealInfraACountMissedByACrashIsRepairedByItsTimer` — core với `MEMBER_COUNT_CHECK_DELAY` ngắn (test) và store bọc làm `AddMemberCount` lỗi (giả core chết sau ghi member): thêm 2 người → reply OK, số chưa đổi; sau delay worker sửa đúng số, phát `member_count_changed`, `counter_repaired_total{counter="members"}` tăng 1. Lệnh bình thường không để lại phiếu nào (xoá kịp).
- `TestRealInfraLostMemberAndReadEventsAreRepublished` — core chạy với fast path event không tới stream (cách các itest reconcile M2b.1/M2b.2 đang làm, xem `itFastEffects`); thêm người, đánh dấu đọc → worker phát `member_added`, `member_count_changed`, `read_updated` của trạng thái hiện tại.
- `TestRealInfraRemovedMemberIsDeniedAtOnceThroughTheActorCache` — xoá cùng core → gửi, `GetHistory`, `MarkRead` bị từ chối ngay.
- `TestRealInfraARemovalOnAnotherCoreReachesTheActorThroughTheMemberEvent` — hai core; bob gửi qua core-1 (actor nhớ bob); xoá bob qua core-2 → chờ event `member_removed`, rồi bob gửi qua core-1 bị từ chối (trước khi TTL 10s hết).
- `TestRealInfraHideAndClearPublishMemberEvents` — ẩn tin và clear phát `message_hidden`, `history_cleared` trên subject `member`; ẩn lại không phát.
- `TestRealInfraTheHighestPriorityAdminSucceedsTheLastOwner` — hai admin, admin vào sau có `priority` cao hơn → được nâng khi owner cuối rời.
- `TestRealInfraAddMembersRetryWithTheSameRequestIDNeverReAddsARemovedUser` — thêm `erin` với R, xoá, lặp R → `added` rỗng; `request_id` mới → `erin` ver 3.
- `TestRealInfraClearHistoryHidesByTimeAndSurvivesARejoin`.
- `TestRealInfraReaderStateChangesEnterTheWorkStream` — subscribe subject work stream; thêm `dave` → record `g:`; `MarkRead` → `d:`; `ClearHistory` → `c:`; ẩn tin → `h:`; `AddMemberCount` không sinh record; doc `dave` giữ `ver 1`.

**Kỹ thuật.** Không có bước "thấy fail": test xác nhận hành vi Task 2–17; fail là lỗi task trước → dừng và báo (gợi ý: owner → Task 5/10/11; subject → Task 3; count → Task 5/6/11/15; event phát lại → Task 6/15; quên cache xuyên core → Task 16; cache → Task 8/11; request → Task 7/11; clear → Task 1/5; record thừa → Task 5/6). Trạng thái lưu chờ bằng `awaitStored`; live event chờ theo id. Trước khi tạo file, grep tên helper định dùng để tránh trùng.

**Lệnh.** `make itest`.

**Done khi.** Xanh.

**Commit.** `test(core): cover owner races, data subjects, member counts, republished events and access on real infra` — `apps/core/it_members_test.go apps/core/member_owner_integration_test.go apps/core/member_events_integration_test.go apps/core/member_access_integration_test.go apps/core/member_feed_integration_test.go INDEXES.csv`.

**Review:** không.

---

### Task 20: Docs + kiểm chứng cuối milestone + checklist merge. **Push**

**Mục tiêu.** Mốc cuối của `feat/m2b` (M2b.0–M2b.4). Controller kiểm nhanh, không reviewer (trừ khi phải tách file code: commit tách có một reviewer nhanh). Không phải milestone perf.

**Todo.**
1. Cây làm việc sạch ngoài docs của owner; `wc -l` mọi file code đụng ở M2b.4 < 200.
2. Thiết kế `docs/designs/261005-chatim-architecture.md`: §5 một bảng field cho mỗi collection (tên đầy đủ), `messages` giữ tên ngắn kèm bảng nghĩa; doc `members`, field mới `rooms`, `message_edits` dòng `ver 0`; §4 lớp tập member, vị trí đọc; §5.1 ghi **ngoại lệ duy nhất** (transaction đổi owner); §6–§9 luồng member, owner, số member (`$inc` + recount), event; §10/§11 subject theo loại dữ liệu (`room`, `member`, `message`; gateway sub `live.{t}.*.{rid}.>`; chuyển phát tới user do app phân phối thiết kế sau); §12 guarantee (owner luôn còn khi còn member; mọi event member/số member/đọc được phát lại; số member lệch có metric + alert + recount); Decision Log thêm D96–D110, sửa D49, D67, D72, D75, D85.
3. `docs/roadmap.md`: dòng M2b.4 done, luật bản tóm tắt kỹ thuật, luật tên field.
4. `CLAUDE.md`: mục Done, tên field, Key encoding (`keys.Member`), Storage ports, send path (subject `evt.{t}.message.{rid}.msg_created`), effect engine (registry member/đọc), khối member, priority, vị trí đọc, ẩn/clear có event, `memberwatch`, phiếu hẹn đếm lại số member, Detectors (16 luật), Lifecycle (26.2s không đổi), lệnh `/app recount`, Shard-readiness (ngoại lệ transaction), Docs.
5. `README.md`: dòng `clear -room ID` bỏ `[-up-to N]`; lệnh member mới; subject theo loại dữ liệu.
6. `INDEXES.csv`: dòng của plan và bản tóm tắt; kiểm `{7}`.
7. Mục "Kết quả thực thi" cuối plan: mỗi task một dòng (commit, kết quả, sửa cơ học, Minor).
8. Kiểm chứng: `make fmt-check`, `make vet`, `make lint`, `make vuln`, `make test`; `make infra-reset`, `make infra-up`, `make itest`; `make image TARGET=apps/core`, `make core-up`, `make e2e`; `/metrics` trên cả hai core có nhãn `member_event`, `member_count_event`, `read_event`, `hidden_event`, `history_cleared_event`, `member_count_repair`, `counter_repaired_total{counter="members"}`, `member_cache_forgets_total`; `make alerts-check` 16 luật; `docker exec chatim-core-1 /app resync -from … -to … -dry-run` in `member_records`; `docker exec chatim-core-1 /app recount -room <room e2e> -dry-run` in hai số bằng nhau; corebench ngắn `make poc TOOL=corebench ARGS="-rate 1000 -duration 30s"` không lỗi.
9. Checklist merge theo `docs/git-workflow.md` (plan done; fmt-check, vet, lint, test, itest, e2e xanh; không Critical/Important mở; roadmap, Decision Log, INDEXES, CLAUDE.md cập nhật; mức `dev-done`); nháp mô tả PR vào `bin/` (gitignored). **Không mở PR**: controller hỏi owner.
10. Push.

**Commit.** `docs: record the M2b.4 member set class, data subjects, full field names and decisions D96-D110` — `docs/designs/261005-chatim-architecture.md docs/roadmap.md README.md CLAUDE.md INDEXES.csv docs/plans/2026-10-06-m2b4-members-read.md`; rồi `docs: record M2b.4 execution results` — `docs/plans/2026-10-06-m2b4-members-read.md docs/roadmap.md`.

**Review:** không.

---

## Ghi chú tích hợp (controller)

**Bất biến xuyên task.**
1. Group còn member active thì còn owner active: chỉ đường transaction đổi role owner hay cho owner rời, luôn ghi kế nhiệm trước đích, và tăng `owners_ver`; đường thường chỉ ghi doc đã kiểm không phải owner, CAS theo `ver` của đúng doc đó.
2. Đọc, chưa đọc, clear history chỉ dùng toán tử thường, không bao giờ đụng `ver`, không pipeline/replace/upsert. Đổi vị trí đọc vào feed thành `ReadChanged`, clear nâng mốc thành `HistoryCleared`, ẩn mới thành `MessageHidden`; số member và `owners_ver` không vào feed.
3. Người bị xoá/rời mất quyền: `Rooms.Member` lọc `state == 1`; lệnh qua `mutate` từ chối ngay; gửi tin qua actor của core khác bị chặn khi event `member_removed` tới (`memberwatch`), muộn nhất 10s (TTL).
4. `ver`, `read_ver`, `member_count_ver`, `owners_ver` chỉ tăng; tombstone không bao giờ bị xoá, nên id event không lặp.
5. Fast path không báo lỗi vì event: lỗi enqueue và `Forget` bỏ qua sau khi ghi; worker phát lại trạng thái hiện tại. `member_count` chỉ đổi bằng `$inc` trong lệnh member (đường owner trong transaction), `Rooms.Create`, worker `member_count_repair` và `/app recount`; `$inc` lỗi không làm lệnh lỗi. Mọi lệnh có thể đổi số đặt phiếu hẹn **trước** khi ghi member và chỉ xoá phiếu khi `$inc` xong, nên lỗi hay core chết ở giữa luôn được sửa sau `MEMBER_COUNT_CHECK_DELAY`.
6. Mỗi thay đổi đúng một event, trên subject theo loại dữ liệu (D108); core không có subject user, không chọn người nhận.
7. Không vòng thử lại bên trong core ở code mới; vòng gọi lại chỉ ở client (route) và test.

**Id event, record và subject.** Token sau `{room}-` phân biệt loại: số, `created`, `p\d`, `mb-`, `members-v`, `rd-`, `hd-`, `cl-`.

| Loại | Event id | Record id | Subject |
|---|---|---|---|
| tin mới | `{room}-{th}-{seq}` | `m:` + event id | `message` |
| sửa/xoá | `{room}-{th}-{seq}-v{ver}` | `e:` + event id (dòng gốc `-v0` chỉ có record, không event) | `message` |
| reaction | `{room}-{th}-{seq}-{u}-n{n}` | `x:` + event id | `message` |
| số reaction | `{room}-{th}-{seq}-reactions-v{ver}` | — | `message` |
| ghim | `{room}-p{pin_ver}` | `p:` + event id | `room` |
| room | `{room}-created` | `r:{room}` | `room` |
| số member | `{room}-members-v{member_count_ver}` | — (phát lại từ record `g:`) | `room` |
| member | `{room}-mb-{u}-v{ver}` | `g:` + event id | `member` |
| đã đọc | `{room}-rd-{u}-v{read_ver}` | `d:` + event id | `member` |
| ẩn tin | `{room}-hd-{u}-{th}-{seq}` | `h:` + event id | `member` |
| xoá lịch sử | `{room}-cl-{u}-{cleared_at ms}` | `c:{room}-cl-{u}-{CommittedAt ms}` | `member` |
| phiếu hẹn đếm lại số member | — | `k:{room}-{op}` (subject hẹn `work.timer.{room}.{op}`) | — |

**Ai sửa file chung (tuần tự).**

| File | Task |
|---|---|
| `store/mongostore/*codec*.go`, `bootstrap.go`, `rooms.go`, `edits.go` | 1 → 5 |
| `store/mongostore/feed*.go` | 1 → 6 |
| `store/ports.go`, `store/feed.go`, `store/edit.go`, `store/write_contract_test.go` | 1 → 4 |
| `store/memstore/*`, `store/storetest/*` | 1 → 4 → 6 |
| `domain/*` | 1 → 2 → 12 |
| `proto/chatim/v1/*`, `pkg/pb/chatim/v1/*` | 1 → 3 |
| `pbconv/*` | 1 → 3 |
| `publish/*`, `effects/*_test.go` subject, `tools/corecli/cmd_watch.go`, `tools/poc/corebench/live.go` | 3 |
| `work/*` | 4 → 6 (+ `work.Timers`) |
| `pkg/lru`, `dedupe/*` | 7 |
| `actor/*` | 7 → 8 |
| `memberwatch/*`, `apps/core/memberwatch_wiring.go` | 16 |
| `access/*` | 9 |
| `ownership/*` | 10 |
| `mutate/*` | 1 → 11 → 13 |
| `view/*` | 1 |
| `grpcsrv/fake_dependencies_test.go` | 11 → 13 |
| `grpcsrv/*` khác | 1 → 12 → 14 |
| `effects/*` | 1 → 15 |
| `apps/core/service_wiring.go` | 11 → 13 |
| `apps/core/wiring.go` | 11 → 14 → 15 → 16 |
| `apps/core/effects_wiring.go` | 6 → 15 |
| `apps/core/metrics_wiring.go`, `member_effects_wiring.go` | 15 |
| `config/*` | 14 |
| `resync/*`, `apps/core/resync_command.go`, `recount_command.go`, `main.go` | 17 |
| `tools/internal/route/*`, `tools/corecli/*`, `scripts/e2e.sh` | 1 → 3 → 18 |
| `README.md` | 14 → 17 → 20 |

**File sát 200 dòng (kiểm `wc -l`).** `mongostore/codec.go` (187; Task 5 tách member codec, Task 1 tách sớm nếu vượt), `mongostore/codec_test.go`, `mongostore/bootstrap_integration_test.go`, `resync/scan_test.go` (187; Task 17 tách trước), `effects/harness_test.go` (199; không thêm), `grpcsrv/harness_test.go` (185; không thêm), `actor/room_actor.go` (170), `mutate/fixtures_test.go` (165), `apps/core/wiring.go` (150), `metrics_wiring.go` (123).

**Khoảng trống tạm (chỉ dev).**
- Task 1 → 5: `members` chưa clustered, chưa có `state`; `Rooms.Member` như cũ.
- Task 4 → 5: vài case contract Mongo đỏ tạm (đã ghi ở Task 4); không chạy `make itest` ở Task 4.
- Task 5 → 6: Mongo ghi `ver` nhưng feed chưa xem `members`.
- Task 5 → 11: `AddMemberCount` chưa ai gọi. Task 6 → 15: phiếu hẹn bật nhưng worker chưa có effect cho `MemberCountCheck` (danh sách rỗng, ack ngay). Task 6 → 15: worker chỉ chạy `room_activity` cho record member và ack ngay record đọc; event member/đọc chỉ có ở đường nhanh.
- Task 7 → 11: `dedupe.Requests` chưa ai gọi. Task 8 → 11: `ForgetMembers` chưa ai gọi (chỉ TTL).
- Task 11 → 12: `CreateRoom` chưa phát `member_added` cho người tạo. Task 11 → 14: lệnh member và đọc chưa có RPC.

**Rolling deploy.** Tên field đổi và `members` đổi khoá nên không có đường nâng cấp tại chỗ: dev `make infra-reset`; prod go-live thẳng từ bản này. Core cũ khởi động lại sẽ ghi đè luật RePublish (`EnsureStream`; subject `member`/`message` ngừng tới `live.*`) và Nak kind 6–9; subject của tin đổi từ `…room…` sang `…message…` → nâng mọi core và mọi consumer cùng lúc. Transaction cần Mongo replica set (dev rs0, prod giống). Proto đổi tên field giữ số field nên wire tương thích; tên JSON đổi (chưa có client ngoài).

**Detector.** Sau Task 15 `/metrics` có `effect_dropped_total{effect}` và `reconcile_republished_total{effect}` cho `member_event`, `member_count_event`, `read_event`, `hidden_event`, `history_cleared_event`; `work_failures_total` bắt Nak của năm effect; sau Task 16 có `member_cache_forgets_total`, `member_watch_malformed_total`; `member_count_repair` ghi vào `counter_repaired_total{counter="members"}` (alert `ChatimCounterRepairSurge` sẵn có). Không metric hay luật mới cho owner (transaction không để lại trạng thái nửa vời). `make alerts-check` 16 luật.

---

## Kết quả thực thi

(Điền khi thực thi: mỗi task một dòng — commit, kết quả kiểm, sửa cơ học, Minor.)

- **Task 1** — `1fb567b`, `50a243b`, `f480589`.
  - fmt/vet/lint/test xanh; `make itest` sau reset có một lần đỏ hiếm: `TestReactionSetOfTheSameUserRacingOnANewReactionNeverFails` vòng 18, Mongo `DurationOverflow` trong `findAndModify` khi upsert đua cùng `_id`. Không tái hiện được trong ~2.000 vòng; chạy lại xanh. Ghi là flaky đã biết, theo dõi.
  - Lệch plan (controller chấp nhận):
    - `Hidden.Hide(ctx, user, key, at)`; kiểu `domain.HiddenMessage{User, Room, Thread, Seq, At}`; `store.MaxHiddenScan`, `store.ValidateMarkTime`; `mongostore.Hidden` qua `Store.Hidden()`.
    - `store.ValidateProjectedEdit` chặn chiếu dòng `EditOriginal`.
    - Test tách file để < 200 dòng.
  - Review: không Critical/Important. Minor:
    - (1) `grpcsrv/edit_history.go:48-58` trang đầu có thể `limit+1` dòng (thêm dòng 0).
    - (2) Mongo `Hide`/`ClearHistory` nên cắt ms rõ ràng như memstore.
    - (3) Lệch đồng hồ giữa core xử lý clear và actor gán `created_at` có thể để sót tin gửi sát lúc clear; ghi vào thiết kế ở Task 20.
    - (4) README `clear -up-to` sửa ở Task 20.
- **Task 2** — `ef18ff4`.
  - Xanh; controller kiểm nhanh.
  - Lệch: `NewRoom` điền thêm `LastChangeAt`; `Room.MemberCountVer` để 0 ở `NewRoom` (`Rooms.Create` ghi 1, Task 4).
- **Task 3** — `8ba014e`.
  - Unit, itest, lint xanh; NATS nhận đổi luật RePublish trên stream cũ.
  - Lệch (chấp nhận):
    - commit gồm cả chỗ đổi subject trong `effects` test, `it_core_test.go`, `tools/corecli`, `corebench/live.go`, `tools/poc/natsbench`;
    - envelope `message_hidden` mang `thread_root`/`seq` của tin bị ẩn.
  - Review: không Critical/Important. Minor:
    - (1) thêm test mọi subject khớp `Source` của RePublish (5 token);
    - (2) itest RePublish có thể thử thêm một kind mỗi loại;
    - (3) thiết kế §8 (dòng ~224) và §11 (~309) còn subject `…room…` — sửa ở Task 20;
    - (4) kind lạ đếm vào `malformed`;
    - (5) `MemberEvent` chỉ phát `member_role_changed` nếu một lần ghi đổi cả role lẫn priority (chưa lệnh nào làm vậy).
- **Task 4** — `d1e86cb`.
  - `make test` xanh; memstore `-count=5` xanh; các case Mongo đỏ tạm tới Task 5 (đã liệt kê).
  - Lệch và chốt của controller:
    - `ChangeOwners` trả `store.OwnerResult{Written, Count, CountChanged}`; `ClearHistory` trả `(time.Time, bool, error)` (bool = mốc đã tăng).
    - Thêm `ValidateOwnerWrites`, `MemberCountDelta`, `ValidateMemberDelta`, `ValidateMemberCount`.
    - `RecordOf(ReadChanged)` chặn `ReadVer` ở `MaxUint32`.
    - `OwnerResult.Written` chỉ tin field membership.
    - `last_change_at` không bao giờ lùi (`$max`, sửa ở Task 5).
  - Review: không Critical/Important. Minor:
    - (1) `storetest/member_cases.go:104-114` chưa so doc `bob` đã active với bản trước;
    - (2) `MemberCountDelta` dựa `Cur.State` — đúng vì `ver` khớp kéo theo `state` khớp;
    - (3) `ChangeOwners` từ chối danh sách user rỗng — giữ, mọi lệnh truyền người gọi và người đích.
- **Task 5** — `9bf5eea`.
  - Unit, itest (sau `infra-reset`), lint xanh; case Mongo đỏ tạm từ Task 4 đã xanh.
  - Lệch (chấp nhận):
    - tên index có `priority`;
    - transaction cập nhật room bằng một `FindOneAndUpdate`;
    - `ClearHistory` dùng `UpdateOne` lọc `cleared_at` rồi `FindOne`;
    - `last_change_at` dùng `$max` ở mọi lần ghi (cả memstore);
    - `member_count` âm vì lệch đọc như thường, để worker sửa.
  - Review: không Critical/Important. Minor:
    - (1) `TestTwoOwnerTransactionsOnOneRoomLetOnlyOneCommit` có thể treo nếu một bên lỗi trước `decide` — thêm timeout;
    - (2) query owner sắp `{joined_at, user_id}` không đi đúng thứ tự index (sắp trong RAM, ổn khi ít owner);
    - (3) abort/`EndSession` dùng `WithoutCancel` không hạn — nên thêm timeout 5s;
    - (4) lỗi mạng/deadline không nhãn trả nguyên, không thành `ErrRetryLater`;
    - (5) `AddMemberCount`/`SetMemberCount` trả lỗi khác nhau cho room id > MaxInt64;
    - (6) `time.Now()` trong `read_position.go` không tiêm được;
    - (7) `MembersBetween` quét mọi member của room rồi sắp (cần index `{room_id, last_change_at}` nếu resync room lớn chậm);
    - (8) DB dev cũ phải `infra-reset`.
- **Task 6** — `51945c5`, `9143d94` (đã push), sửa `1d0d433`.
  - Unit, itest, lint xanh.
  - NATS 2.15 nhận `AllowMsgSchedules` trên stream WorkQueue; phiếu bật đúng hạn (~1,9s với delay 1s), phiếu đã xoá không bật, stream cũ được bật qua `EnsureStream`.
  - Lệch (chấp nhận):
    - giờ bật làm tròn lên giây (bật trong `[delay, delay+1s]`);
    - `CommittedAt` = giờ bật;
    - `NewTimers` kiểm cấu hình, có `WithTimerLogger`;
    - phiếu mang `Nats-Msg-Id`;
    - `KnownKind` 1..10;
    - tạo room đưa doc member tạo cùng room vào feed.
  - Review: 1 Important đã sửa ở `1d0d433` (`Disarm` không cảnh báo khi phiếu đã bật — `jetstream.ErrMsgNotFound`). Minor:
    - (2) decoder nhận double không nguyên ở `ver`;
    - (3) handle stream cache mãi (ổn);
    - (4) itest "không bật" có thể kiểm thêm `Msgs == 0`.
- **Task 7** — `c407ed6`.
  - Unit (`dedupe` `-count=5`), itest, lint xanh.
  - Lệch: helper `onlyVerdict` (cho lint `nilerr`), thêm `RequestStatus.String()`.
  - Lưu ý cho Task 11:
    - `Finish` cần `Seq` ≥ 1 (số user);
    - dựng `Requests` trên cid `Batcher` để `Finish`/`Cancel` không chặn;
    - kiểm lỗi của `Begin` trước trạng thái;
    - `Busy` → `ErrRetryLater`.
  - Review: không Critical/Important. Minor:
    - (1) ctx huỷ sau `Reserved` để khoá pending 10s (như cid);
    - (2) `Finish`/`Cancel` bỏ qua lỗi registry;
    - (3) chữ log "malformed cid dedupe value" dùng chung cho khoá request;
    - (4) thiếu test `Begin`/`Finish` song song.
- **Task 9** — `64f5b5d`.
  - Unit, vet, lint xanh; không lệch.
  - Review: không finding cần sửa. Minor tuỳ chọn:
    - nhánh `default` cho phép action chưa liệt kê (giữ như cũ);
    - thiếu case role caller rỗng, action lạ, admin xoá người đích giả.
  - Lưu ý Task 11: người đích giả phải gán rõ `Role: member` (role rỗng làm admin bị từ chối).
- **Task 8** — `355dba6`.
  - `actor` `-count=5` xanh.
  - Lệch: thêm `TestAForgetDuringAMemberReadDropsThatRead`; `ForgetMembers` đặt trong `member_cache.go`.
  - Review: không Critical/Important. Minor:
    - TTL đo bằng `time.Now()` (actor không có đồng hồ riêng);
    - test song song chỉ kiểm không race;
    - forget rơi vào actor đang retire vô hại.
- **Task 10** — `bda106d`.
  - `ownership` `-count=5`, lint toàn repo xanh.
  - Lệch (chấp nhận):
    - người đã rời gọi rời → kế hoạch rỗng (thành công, không đổi);
    - action lạ → `ErrInvalidArgument`.
  - Review Task 10: không Critical/Important; bất biến "còn member thì còn owner" giữ ở mọi ca lần theo. Minor:
    - (1) `Plan` tin `Candidates` (không lọc tombstone/đích), dựa CAS và `ValidateOwnerWrites` chặn;
    - (2) role không hợp lệ dựa `mutate` kiểm trước;
    - (3) `Allow` nil sẽ panic;
    - (4) test đua chưa có ca ứng viên rời đường thường khi owner cuối rời (CAS kế nhiệm);
    - (5) assert lời gọi `Allow` hơi lỏng.
- **Task 11** — `5115349`.
  - Unit, itest, lint xanh; không vòng thử lại member mới.
  - Lệch (chấp nhận):
    - `serviceDeps.timerJS` dùng client JetStream của publisher; helper `wireMutator`;
    - `ErrTooManyMembers` đếm sau khi khử trùng;
    - `Forget` chỉ khi đổi thật;
    - lỗi ghi để phiếu nguyên; hẹn lỗi trả `ErrRetryLater` trần;
    - tự thao tác dùng lại doc của `Admit`;
    - transaction rỗng trả doc đích.
  - Review: không Critical/Important. Minor:
    - (1) gọi `Forget` cả khi ghi lỗi không rõ kết quả — sửa kèm Task 13;
    - (2) `Cancel`/`Finish`/`Disarm` dùng context riêng có hạn — sửa kèm Task 13;
    - (3) hằng delay 5s chờ Task 14;
    - (4) thiếu test lỗi `ApplyMember`/`Last`.
- **Task 12** — `b3aafb5`.
  - Unit, lint xanh.
  - Review: không Critical/Important; id của worker trùng id đường nhanh (`{room}-mb-{u}-v1`), e2e cũ không đếm `member_added`. Minor:
    - (1) DM 501 người báo "quá nhiều member" thay vì lỗi kích thước DM (cùng mã);
    - (2) nhánh `ev != nil` trong `creationEvents` không bao giờ sai.
- **Task 13** — `4756e88`; Minor của Task 11 sửa ở `cb3bf91`; Important sửa ở `586629a`.
  - Unit, itest, lint xanh.
  - Lệch (chấp nhận):
    - `Hidden.Hide` trả `(bool, error)`;
    - `join` gọi `Forget` khi ghi lỗi; lỗi `ChangeOwners` luôn `Forget`;
    - `AddMemberCount` chạy trên context dọn dẹp;
    - `settleTimeout` là hằng 2s (chưa đưa vào config).
  - Review: 1 Important đã sửa — context dọn dẹp tạo trước khi ghi member nên lần ghi chậm làm hết hạn; nay tạo sau khi ghi; test ghi chậm 2,5s. Minor:
    - test đua `Hide` trên Mongo;
    - `last_change_at` lấy `time.Now()` khác đồng hồ event.
- **Task 14** — `5cba31f`, `2ea46c7`.
  - Unit, itest, lint xanh.
  - **Push thất bại:** SSH agent không có khoá sau khi phiên khởi động lại; owner nạp lại khoá rồi push.
  - Lệch: `TestMemberErrorsKeepTheirCodes` tách file riêng; CAS trượt tạo bằng `access.PolicyFunc`.
  - Review: không Critical/Important. Minor:
    - chưa có test nối `Config.Limits.MemberBatch` vào `Mutator` ở `apps/core`;
    - không có test `Load()` riêng cho `MEMBER_COUNT_CHECK_DELAY`;
    - thiếu case đổi về đúng role (`changed=false`) qua service.
- **Task 15** — `a5b3582`.
  - Unit, lint, `alerts-check` (16 luật) xanh.
  - `make itest` đỏ một test lỗi thời (`TestRealInfraHideAndClearApplyOnlyToTheReader`, viết trước D104/D109); đang sửa trong commit tiếp.
  - Lệch (chấp nhận):
    - `store.Hidden.Get`;
    - `wireEffects` nhận `timers`;
    - `roomTypes` cache cả tenant;
    - `Now` tiêm được;
    - `work.Timers` dựng một lần trong `wire()`.
  - Review: 1 Important — worker sửa số đua với `$inc` còn đang tới: "đọc lại rồi hẹn" không bắt được. Sửa: hẹn phiếu mới **trước** khi ghi lại số, bỏ bước đọc lại. Đã cập nhật D102, design §7.1 bước 4, bản tóm tắt §3.7. Minor:
    - (2) bản ghi `ReadVer` bị chặn ở `MaxUint32` không bao giờ phát lại (chỉ sau 4 tỷ lần đọc);
    - (3) `member_count_repair` dựng cache `roomTypes` thừa;
    - (4) số đúng vẫn phát lại event (giữ, che event mất).
  - Sửa Task 15 — `febe0f7`:
    - hẹn phiếu trước khi ghi lại số, bỏ `rearm`;
    - số đúng vẫn phát lại event;
    - itest viết lại thành `TestRealInfraHideAndClearApplyOnlyToTheReaderAndAnnounceIt`.
    - `make itest` đầy đủ xanh; `effects` `-count=5` xanh.
- **Task 16** — `e9ead90`.
  - Unit (`memberwatch` `-count=5`), itest, lint xanh.
  - Lệch: thêm `fake_bus_test.go`, `memberwatch_wiring_test.go`; sửa test cổng khởi động, thứ tự dừng, metrics.
  - Review: không Critical/Important; subject khớp chuỗi publish → RePublish. Minor:
    - (1) test chưa khẳng định watch đã start trước khi gRPC mở;
    - (2) bộ đếm `malformed` gần như không bao giờ tăng (chỉ bắt publisher lạ);
    - (3) itest publish thẳng lên `live.*` — Task 19 phải đi qua publisher thật;
    - (4) NATS core không phát lại event mất khi rớt kết nối, chỉ còn TTL 10s chặn;
    - (5) trong lúc router dừng, cache không được quên (tối đa 10s TTL);
    - (6) `ParseUint` nhận số 0 đứng đầu.
- **Task 17** — `52b835b`, `826c96f`, `dd95111`.
  - Unit, itest, lint xanh; controller kiểm nhanh.
  - Lệch (chấp nhận):
    - `826c96f` lỡ chứa dòng INDEXES `tools/internal/route` của Task 18 (cây cuối đúng, không viết lại lịch sử);
    - `scanByTime` có hook `extra`;
    - resync phát lại cả `ReadChanged` cho mọi member vừa vào;
    - id phiếu `HistoryCleared` dùng `LastChangeAt`;
    - itest drill thêm ẩn tin.
  - Minor: `/app recount` vẫn ghi và tăng `member_count_ver` khi số đã đúng (worker thì không) — có thể đổi sang chỉ phát lại event.
- **Task 18** — `55b6907`, `8fa4f68`.
  - Unit, lint xanh; `make e2e` đủ 5 phase ngay lần đầu (phase 5: 11 bước, 23 event member khớp id/kind/subject/payload; phase 2 kill core-1 không mất, không trùng).
  - Controller kiểm nhanh.
  - Lệch (chấp nhận):
    - dòng INDEXES route nằm trong `826c96f`;
    - test route tách `members_test.go`;
    - phase 5 sub `live.{t}.*.*.>` rồi lọc;
    - kiểm nghiêm "không event thừa" toàn cục;
    - payload so dạng văn bản chuẩn hoá;
    - state lưu `c0` và cờ `member_run`.
- **Task 19** — `0be1074`.
  - 13 itest mới xanh; test đua/thời gian `-count=5` xanh (đua rời room đụng xung đột transaction ở mọi vòng); test xuyên core có đối chứng âm (trỏ sai subject → đỏ).
  - `make itest` đầy đủ xanh; không phát hiện lỗi của task trước; không sửa code production.
  - Lệch:
    - ca "ẩn lại không phát event" thành `TestRealInfraHidingAMessageAgainPublishesNoEvent` (đếm publish thô trên `evt`);
    - hai test "ghi bị mất" dựng `Mutator` trực tiếp vì core không có chỗ tiêm store.
