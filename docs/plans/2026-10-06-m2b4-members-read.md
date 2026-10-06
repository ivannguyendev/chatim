# M2b.4 — Member + vị trí đọc — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: dùng `subagent-driven-development` (cùng phiên) hoặc `separate-driven-development` (phiên riêng) để thực thi plan này theo từng task. Implementer dùng skill `go-lang`.
>
> **Tóm tắt cho owner:** [2026-10-06-m2b4-members-read-summary.md](2026-10-06-m2b4-members-read-summary.md)

**Goal:** Thay đổi member (thêm/xoá/rời/đổi role) theo **fact + projection** (§4): fact `member_actions` `room│mv` với mv dày, projection `members` + `user_rooms` (guard từng doc `mv < k`) rồi CAS đầu room `rooms.mv/mc`; số member gập thẳng từ fact. Lệnh "settle" projection trước khi kiểm quyền. Event `member_added`/`member_removed`/`member_role_changed` có bản cho room và bản cho từng user bị ảnh hưởng (envelope `recipient` → subject user, RePublish mở rộng). Vị trí đọc `members.rs {s, v}` theo lớp **tần suất cao gộp được**: `MarkRead` chỉ nâng, `MarkUnread` chỉ hạ, mỗi lần đổi `v+1`; event `read_updated` qua coalescer `readcast`. Actor quên cache member theo thế hệ + TTL. Feed thêm insert của `member_actions`; ba effect trên work stream; resync quét thêm `member_actions`.

**Architecture:**
- `pkg/keys`: `MemberAction` (16B `room│mv`), `UserRoom` (`tenant 0x00 user 0x00 room(8)`), `UserRoomPrefix`.
- `domain`: `RoleAdmin`, `ParseRole`, `ReadPos`; `Member` thêm `Removed`, `MV`, `Read`; `Room` thêm `MemberVersion`; `MemberOp`, `MemberChange`, `MemberAction` (+ `Applied`), `InitialMembers`, `Successor`, `ReadUpdate`; lỗi `ErrDirectRoom`, `ErrLastOwner`, `ErrMemberNotFound`, `ErrTooManyMembers`; bỏ trần 5000 của `NewRoom` (Task 11).
- `store`: port `MemberActions` (fact), `MemberProjector` (apply + CAS đầu), `MemberReader` (`MembersOf`, `Successor`, `Owners`, `UserRooms`), `ReadPositions`; `ChangeKind` `MemberInserted` (6), `Change.Member`. Mongo: clustered `member_actions` (index `{r, ts}`) và `user_rooms`; `members.st/mv/rs` + index `{r, st, ro, ja, u}`; `rooms.mv`; accessor `st.MemberActions()`.
- Package mới `memberproj` (`Settle`, `Project`) dùng chung cho `mutate` và `effects`; package mới `readcast` (coalescer một goroutine, bước dừng riêng).
- `actor`: `Router.ForgetMembers(room)` + TTL cache member 10s.
- `access`: action `add_members`, `remove_member`, `leave_room`, `change_member_role`, `mark_read`; `Request.Target`, `Request.Role`; `Checker.AdmitRoom`; `DefaultPolicy` theo owner chốt.
- `mutate`: `AddMembers`, `RemoveMember`, `LeaveRoom`, `ChangeMemberRole`, `MarkRead`, `MarkUnread`; `Limits.MemberBatch`.
- `pbconv` + proto + `publish`: id/event member và read, `members.proto`, envelope `recipient = 10`, oneof 28–31, subject user, RePublish `evt.*.*.*.*`.
- `grpcsrv`: 6 RPC; `CreateRoom` phát `member_added` và chặn theo `MEMBER_BATCH_MAX`.
- `effects`: `member_projection` (0), `member_event` (`RECONCILE_DELAY`); không luật alert mới (vẫn 16).
- `config`: `MEMBER_BATCH_MAX`, `READ_RECEIPT_WINDOW`, `READ_RECEIPT_MAX_MEMBERS`; kế hoạch dừng 26.2s → 27.2s.
- `/app resync` quét `member_actions` theo `{r, ts}`; route + `corecli` + e2e có member và đọc.

**Tech Stack:** Go 1.26 trong Docker qua `make`; buf; mongo-driver v2 (clustered collection, `BulkWrite` upsert có guard, `FindOneAndUpdate`, change stream); nats.go jetstream (RePublish); `testing/synctest`; goleak.

**Nguồn quyết định:** [thiết kế](../designs/261005-chatim-architecture.md) §4, §5, §5.1, §6.3, §6.4, §8.3, §9.3, §10, §12, D62, D67, D72, D79–D95; [roadmap](../roadmap.md) dòng M2b.4. Owner chốt 2026-10-06:
- Quyền mặc định qua `access.Policy` (như D86): owner làm mọi việc; admin thêm người và xoá member thường; chỉ owner đổi role, xoá admin/owner; ai cũng tự rời được.
- Role owner/admin/member; một group được nhiều owner; owner cuối không tự hạ role; owner cuối rời → admin vào sớm nhất, không có thì member vào sớm nhất.
- Người mới (hoặc thêm lại) thấy toàn bộ lịch sử, tin cũ coi như đã đọc; rời/bị xoá mất quyền đọc, thêm lại thì đọc lại được.
- DM cố định 2 người: không thêm, xoá, rời, đổi role.
- "Đánh dấu chưa đọc" = lùi vị trí đọc về trước một tin (kiểu Slack).
- "Đã xem": DM và group ≤ `READ_RECEIPT_MAX_MEMBERS` (100) phát cho cả room, gộp tối đa 1 lần mỗi vài giây; group lớn hơn chỉ đồng bộ giữa các thiết bị của chính user.
- Core không giới hạn số member mỗi group.

Quyết định mới, Task 19 ghi vào Decision Log:
- **D96** Fact member `member_actions` clustered `_id = room│mv`, mv dày (head + 1, khoá là CAS); fact mang op, danh sách thay đổi `{u, ro, pr, rs}`, `by`, `ts`, `n` (số member sau fact), `so` (owner kế nhiệm); `CreateRoom` = fact mv 1; no-op không ghi fact; trùng khoá → cùng ý định là kết quả, khác thì thử lại ≤3 rồi `ErrRetryLater`.
- **D97** Projection settle-first: lệnh gọi `memberproj.Settle` trước khi đọc caller/target và `Allow`; áp fact bằng upsert từng doc guard `mv < k` trên `members` và `user_rooms` (trùng khoá = fact mới hơn đã áp) rồi CAS đầu `rooms {mv == p} → {mv, mc}`; fast path và worker dùng chung; xoá = tombstone `st`.
- **D98** `member_count` gập từ `n` của fact (tinh chỉnh D67): không recount, không witness.
- **D99** Action `add_members`, `remove_member`, `leave_room`, `change_member_role`, `mark_read` (cả `MarkUnread`); `access.Request` thêm `Target`, `Role`; `DefaultPolicy` theo owner chốt; nhiều owner.
- **D100** DM → `FAILED_PRECONDITION` (`ErrDirectRoom`); `RemoveMember(self)` → `INVALID_ARGUMENT`; owner cuối tự hạ → `FAILED_PRECONDITION` (`ErrLastOwner`); owner cuối rời → kế nhiệm (admin `ja` sớm nhất, rồi member, hoà theo user id) ghi trong fact; `LeaveRoom` chỉ kiểm tenant khi admit.
- **D101** `user_rooms` clustered `_id = tenant 0x00 user 0x00 room(8)` (thay `u│r` của D72), tombstone `st`; shard key tương lai `{_id: 1}`.
- **D102** Event member: bản room `{room}-m{mv}` + bản user `{room}-m{mv}-{u}` (envelope `recipient = 10` → `evt.{t}.user.{u}.{type}`); RePublish `evt.*.*.*.*` → `live.{1}.{2}.{3}.evt.{4}`; không ack mark.
- **D103** Cache member của actor: thế hệ (`Router.ForgetMembers`) + TTL 10s.
- **D104** Vị trí đọc `members.rs {s, v}`: `MarkRead` chỉ nâng (seq 0 = tin mới nhất), `MarkUnread(seq)` chỉ hạ về `seq−1`, mỗi lần đổi `v+1` (ai tới sau thắng); kẹp theo seq cuối thật; vào/thêm lại nâng `rs.s` lên seq cuối lúc vào (ghi trong fact); không phải fact, không reconciler; đếm unread để M3.
- **D105** `read_updated` qua `readcast`: gửi ngay lần đầu, gộp phần đuôi trong `READ_RECEIPT_WINDOW` (3s); subject room khi DM hoặc `mc ≤ READ_RECEIPT_MAX_MEMBERS` (100), không thì subject user; id `{room}-rd-{u}-v{v}`; best-effort; bước dừng 1s (26.2s → 27.2s).
- **D106** Kind `MemberInserted` (6), record id `g:{room}-m{mv}`; effect `room_activity` (0), `member_projection` (0), `member_event` (`RECONCILE_DELAY`); resync quét `member_actions` theo `{r, ts}`.
- **D107** Bỏ trần 5000 trong `domain.NewRoom`; trần mỗi lệnh `MEMBER_BATCH_MAX` (500, 2..1000; 1 sẽ chặn mọi DM) cho `CreateRoom` và `AddMembers`; thay đổi member trong một room đi tuần tự (~100–200 lệnh/s), gom cho channel để milestone Channel.

---

## Quy tắc chung (đọc trước khi làm task nào)

- **Không chạy `go` trên host.** Test một package: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/<pkg>/..."`. Tra API: `make -s go ARGS="doc <pkg> <Symbol>"`.
- **Không viết comment** trong Go, test, YAML, shell, Makefile, proto mới.
- File code dưới 200 dòng; `wc -l` sau mỗi lần sửa file lớn.
- `gosec`: không chuyển `int`/`int64` → `uint*` (và ngược lại) khi chưa chặn biên (G115). Mongo lưu `mv`, `n`, `rs.s`, `rs.v`, `r` dưới dạng int64; đọc khoan dung `AsInt64OK` (int32/int64/double), âm hoặc vượt kiểu Go → `errCorrupt`. `mv`/`rs.v` > `MaxInt64` bị store từ chối.
- Trước mỗi commit: `make fmt-check`, `make vet`, `make lint` + test các package đã đụng. Goroutine/channel/lock mới: thêm `-count=5` trên package đó.
- Sửa lint/vet thuần idiom/format có hành vi y hệt: được áp và ghi vào báo cáo. **Plan này chưa từng được biên dịch**: sửa biên dịch thuần cơ học (import thiếu, tên biến trùng, kiểu trả về lệch một chữ) được áp, phải ghi vào báo cáo. Lệch khác, kết quả khác "Expected", test cũ fail: **dừng và báo cáo**.
- Đổi adapter Mongo/NATS hoặc wiring `apps/core`: `make itest` một lần cuối task (cần `make infra-up`). `make itest` **không nhận `ARGS`**; muốn chạy riêng một test thì tạo makefile trong scratchpad (không commit), chạy từ gốc repo:

  ```make
  include Makefile
  itest-one: check-env
  	$(GO_RUN) --network $(NETWORK) -e CHATIM_IT_MONGO_URI -e CHATIM_IT_REDIS_ADDR=chatim-redis:6379 -e CHATIM_IT_REDIS_PASSWORD -e CHATIM_IT_REDIS_DEDUPE_ADDR=chatim-redis-dedupe:6379 -e CHATIM_IT_REDIS_DEDUPE_PASSWORD -e CHATIM_IT_NATS_URL=nats://chatim-nats:4222 $(GO_IMAGE) go test -race -count=1 -run '$(RUN)' $(PKG)
  ```

  `make -f <scratchpad>/itest-one.mk itest-one RUN=TestX PKG=./apps/core/...`.
- `INDEXES.csv`: field có dấu phẩy (thường là `key_symbols`, `decisions`) **phải đặt trong ngoặc kép**. Sau mỗi lần sửa: `python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"` → `{7}`.
- Commit theo Conventional Commits, không nhắc AI, không có dòng `Co-Authored-By`. Nhánh `feat/m2b`. **Push sau Task 7, Task 14 và Task 20** (`git push origin feat/m2b`); không push ở task khác.
- Review: Task 3, 5, 6, 8, 9, 10, 11, 12, 14, 15 rủi ro → mỗi task 1 reviewer (spec + chất lượng một lượt, tối đa `-count=3` trên package đụng). Task khác controller kiểm nhanh. Lỗi Minor ghi vào "Kết quả thực thi".

## Bảng tính năng → lớp dữ liệu (quy tắc roadmap)

| Tính năng | Lớp (§4) | Fact / ghi | Idempotent | Effect + chính sách | Quyền | View | Event (id) | Khuếch đại nhóm 5K | Khuếch đại channel 200K | Guarantee + detector |
|---|---|---|---|---|---|---|---|---|---|---|
| Thêm/xoá/rời/đổi role | Fact + projection | insert `member_actions {_id: room│mv}` mv = head+1; projection: upsert guard `mv < k` từng doc `members`/`user_rooms`, rồi CAS `rooms {mv == p} → {mv, mc}` | Lệnh = trạng thái mong muốn; đúng sẵn = thành công không fact; trùng khoá → `At(mv)` cùng op/người làm/tập user = kết quả, khác → thử lại ≤3 → `ErrRetryLater` | fast path: settle → Allow → append → `Project` → `ForgetMembers` → enqueue; worker `room_activity` (0) + `member_projection` (0, 1 `Project`/room/lô) + `member_event` (`RECONCILE_DELAY`, không mark) | `add_members`, `remove_member`, `leave_room`, `change_member_role` | `Admit` từ chối người đã rời/bị xoá (GetHistory, gửi tin sau ≤10s TTL actor) | bản room `{room}-m{mv}`, bản user `{room}-m{mv}-{u}` | thêm k ≤ 500: admit 2 read + settle (1 `Get` + 1 range fact) + 1 `MembersOf` (k+1 khoá) + 1 `Last` + 1 insert + 2 bulk k upsert + 1 CAS + (1+k) event; xoá/rời/role: như trên với k = 1, rời của owner cuối thêm ≤3 read index (`Owners`, `Successor`); worker lặp lại projection (mọi upsert bị guard chặn) + (1+k) publish | như 5K mỗi lệnh, không có bước O(N). **⚠ room nóng**: mọi lệnh member của một room đi tuần tự (~100–200/s); bão người vào channel 200K gặp `ErrRetryLater`; gom lệnh **để milestone Channel** | MB1 (mới): projection = fold fact → `effect_dropped_total{effect="member_projection"\|"member_event"}`, `work_failures_total`, `reconcile_republished_total{effect="member_event"}`; alert `ChatimEffectDropping`, `ChatimWorkFailing`, `ChatimRepublishSurge` (có sẵn) |
| Số member | Aggregate gập từ fact | `n` trong fact; `rooms.mc` ghi cùng CAS đầu | gập, không recount (D98) | như dòng trên | — | `Room.member_count` | trong payload event member | O(1) | O(1) | MB1 |
| `user_rooms` | Projection | doc `{_id: t│0│u│0│r, t, u, r, ro, ja, mv, st}` cùng `ApplyMembers` | guard `mv < k` | như dòng trên | — | M3 `ListMyRooms` (`UserRooms`) | — | k upsert mỗi lệnh | k upsert mỗi lệnh; danh sách phòng của user là range trên `_id` | MB1 |
| Vị trí đọc | Tần suất cao gộp được | `FindOneAndUpdate members {r, u, st: null, rs.s < seq}`: `rs.s = seq`, `rs.v + 1` | chỉ nâng; gửi lại = no-op | không effect, không reconciler | `mark_read` | trả `{read_seq, read_version}` | `read_updated` `{room}-rd-{u}-v{v}` qua `readcast` | 2 read admit + ≤1 `Last` + 1 write + ≤2 event/W/(room, user); nhóm > 100 → chỉ subject user | như 5K; không bao giờ phát cho cả channel | best-effort, không guarantee; `read_events_unbatched_total` (chẩn đoán), `publish_dropped_total` |
| Đánh dấu chưa đọc | Tần suất cao gộp được | như trên với `rs.s > seq−1` → `rs.s = seq−1`, `rs.v + 1` | chỉ hạ; version LWW | như trên | `mark_read` | như trên | như trên | như trên | như trên | như trên |
| `read_updated` | (event của dòng trên) | coalescer một goroutine, map `(room, user)` có trần | id theo `v`; trùng do stream bỏ | gửi ngay lần đầu; phần đuôi `v` lớn nhất sau W | — | — | `{room}-rd-{u}-v{v}` | ≤2 event/W/(room, user) | như 5K | như trên |

## Hợp đồng chung (mọi task phải khớp đúng chữ ký này)

Struct/interface viết gọn một dòng ở đây chỉ là ký hiệu; code thật để `gofmt` dàn dòng, giữ đúng tên, kiểu và thứ tự field.

### `pkg/keys` (Task 2)

```go
const (
	MemberActionLen = 16
	MaxUserRoomLen  = 32 + 1 + 64 + 1 + 8
)

func MemberAction(room, mv uint64) []byte
func ParseMemberAction(b []byte) (room, mv uint64, err error)
func UserRoom(tenant, user string, room uint64) []byte
func UserRoomPrefix(tenant, user string) []byte
func ParseUserRoom(b []byte) (tenant, user string, room uint64, err error)
```

- `MemberAction` = room(8) mv(8) big-endian; `ParseMemberAction` cần đúng `MemberActionLen`.
- `UserRoom` = `tenant` + `0x00` + `user` + `0x00` + room(8). `UserRoomPrefix` = phần trước room. `ParseUserRoom`: độ dài ≤ `MaxUserRoomLen`, đúng hai byte `0x00` (tenant, user khác rỗng), đuôi đúng 8 byte; không thì `ErrLength`. Ident hợp lệ không chứa `0x00` nên thứ tự byte giữ thứ tự (tenant, user, room) và không tiền tố nào trùng tiền tố khác (test bảng: `ab` không khớp `abc`).

### `apps/core/internal/domain` (Task 2; trần Task 11)

```go
const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
	MaxMemberChanges = 1000
)

type ReadPos struct{ Seq, Version uint64 }

type Room struct { /* field cũ */ ; MemberVersion uint64 }
type Member struct { /* field cũ */ ; Removed bool; MV uint64; Read ReadPos }

type MemberOp uint8

const (
	MemberOpAdd MemberOp = iota + 1
	MemberOpRemove
	MemberOpLeave
	MemberOpRole
)

type MemberChange struct{ User string; Role, Prev Role; ReadSeq uint64 }

type MemberAction struct {
	Room, MV  uint64
	Tenant    string
	Op        MemberOp
	Changes   []MemberChange
	By        string
	At        time.Time
	Count     int
	Successor string
}

type ReadUpdate struct {
	Room    uint64
	Tenant  string
	Type    RoomType
	Members int
	User    string
	Pos     ReadPos
	At      time.Time
}

var (
	ErrDirectRoom     = fmt.Errorf("direct room members are fixed: %w", apperr.ErrFailedPrecondition)
	ErrLastOwner      = fmt.Errorf("last owner cannot step down: %w", apperr.ErrFailedPrecondition)
	ErrMemberNotFound = fmt.Errorf("member %w", apperr.ErrNotFound)
	ErrTooManyMembers = fmt.Errorf("too many members in one request: %w", apperr.ErrInvalidArgument)
)

func ParseRole(s string) (Role, error)
func InitialMembers(r Room, members []Member) MemberAction
func Successor(candidates []Member) (Member, bool)
func (a MemberAction) Applied(cur Member, user string) Member
```

- Field mới thêm **cuối** struct. `Member.Removed == false` là đang ở trong room (doc cũ không có `st` là active). `MemberOp` 1..4 là giá trị lưu `member_actions.op`.
- `MemberChange`: add → `Role` = role được gán (`member`, hay role ban đầu của `InitialMembers`), `Prev = ""`, `ReadSeq` = seq cuối lúc vào; remove/leave → `Role = ""`, `Prev` = role trước; role → `Role` mới, `Prev` cũ. Remove, leave, role có đúng 1 change; add có 1..`MaxMemberChanges`.
- `InitialMembers(r, members)` = `{Room: r.ID, MV: 1, Tenant, Op: MemberOpAdd, Changes: [{u, m.Role, "", 0}…] theo thứ tự members, By: r.CreatedBy, At: r.CreatedAt, Count: len(members)}`. Adapter và `grpcsrv.CreateRoom` cùng gọi nó nên event fast path = event worker (RC2).
- `Successor`: chọn trong ứng viên `Removed == false` và role ≠ owner: admin trước member, rồi `JoinedAt` sớm nhất, rồi `User` nhỏ nhất; không có → `false`.
- `Applied(cur, user)` (thuần, định nghĩa ngữ nghĩa projection cho mọi adapter): add → `Removed=false`, `Role=c.Role`, `JoinedAt=a.At`, `MV=a.MV`, `Read={max(cur.Read.Seq, c.ReadSeq), cur.Read.Version+1}`, giữ `ClearedBeforeSeq`; remove/leave → `Removed=true`, `MV=a.MV`, giữ phần còn lại; role → `Role=c.Role`, `MV=a.MV`; `user == a.Successor` → `Role=RoleOwner`, `MV=a.MV`. `Room/Tenant/User` luôn lấy từ fact. User không có trong fact → trả `cur`.
- `ParseRole`: `owner|admin|member`, khác → `invalid("role")`.
- Task 11 bỏ `maxGroupMembers` khỏi `validate.go` (group không còn trần trong domain; DM vẫn đúng 2).

### `apps/core/internal/store` (Task 4; Mongo Task 5; feed Task 6)

```go
const MaxMemberScan, MaxUserRoomsLimit = 1000, 1000

var (
	ErrMemberActionExists   = fmt.Errorf("member version %w", apperr.ErrAlreadyExists)
	ErrMemberActionNotFound = fmt.Errorf("member action %w", apperr.ErrNotFound)
)

type MemberActions interface {
	Append(ctx context.Context, a domain.MemberAction) error
	At(ctx context.Context, room, mv uint64) (domain.MemberAction, error)
	After(ctx context.Context, room, mv uint64, limit int) ([]domain.MemberAction, error)
	Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.MemberAction, error)
}

type MemberProjector interface {
	ApplyMembers(ctx context.Context, a domain.MemberAction) error
	AdvanceMembers(ctx context.Context, room, base, mv uint64, count int) (bool, error)
}

type MemberReader interface {
	MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error)
	Successor(ctx context.Context, room uint64) (domain.Member, bool, error)
	Owners(ctx context.Context, room uint64, limit int) ([]domain.Member, error)
	UserRooms(ctx context.Context, tenant, user string, after uint64, limit int) ([]domain.Member, error)
}

type ReadPositions interface {
	MarkRead(ctx context.Context, room uint64, user string, seq uint64) (domain.ReadPos, bool, error)
	MarkUnread(ctx context.Context, room uint64, user string, to uint64) (domain.ReadPos, bool, error)
}

func ValidateMemberAction(a domain.MemberAction) error
```

- File mới `store/member.go`. `Rooms.Get` trả thêm `MemberVersion` (`rooms.mv`, thiếu = 0). `Rooms.Member` trả `domain.ErrNotMember` cho doc `Removed`. `HistoryClearer.ClearHistory` chỉ áp cho member active (removed → `ErrNotMember`).
- `Rooms.Create(r, members)` giữ chữ ký, đổi ngữ nghĩa (mọi adapter): insert room (`mc = len(members)`, không `mv`) → `Append(InitialMembers(r, members))` → `ApplyMembers` → `AdvanceMembers(r.ID, 0, 1, len)`. Room trùng → `ErrRoomExists` như cũ; lỗi ở bước sau trả lỗi (room mồ côi không ai biết id; fact đã ghi thì worker vá).
- `ValidateMemberAction`: room ≠ 0, MV 1..`MaxInt64`, `ValidTenant`, op 1..4, số change đúng theo op (add 1..`MaxMemberChanges`, khác đúng 1), mọi `ValidUser`, user không lặp, role hợp lệ cho add/role (`Role ≠ ""`), `ValidUser(By)`, `Count ≥ 0`, `Successor` rỗng hoặc (`op == Leave` và `ValidUser`).
- `Append`: validate; trùng khoá → `ErrMemberActionExists`. `At` không có → `ErrMemberActionNotFound`. `After`: fact `MV > mv` tăng dần, `limit` 1..`MaxMemberScan` (`ValidateLimit`). `Between`: fact có `ts ∈ [from, to]`, sắp `ts` rồi `_id`.
- `ApplyMembers(a)`: với mỗi user trong `a.Changes` và `a.Successor`: ghi `a.Applied(cur, user)` vào `members` **chỉ khi** doc chưa có hoặc `mv < a.MV` (thiếu `mv` = 0); cùng guard cho `user_rooms` (`{t, u, r, ro, ja, mv, st}`). Doc đã có `mv ≥ a.MV` → bỏ qua, không lỗi. Idempotent; không đụng `rooms`.
- `AdvanceMembers(room, base, mv, count)`: `$set mv, mc` chỉ khi `rooms.mv == base` (`base == 0` → `mv` không tồn tại); cần `mv > base` (`ValidateVersionBump`). Room không có hoặc CAS trượt → `(false, nil)`.
- `MembersOf`: doc (kể cả removed) của các user, bỏ user không có doc; ≤ `MaxMemberChanges + 1` user. `Successor`: như `domain.Successor` trên member active của room. `Owners(limit)`: owner active, sắp `ja` rồi `u`, `limit` 1..`MaxMemberScan`. `UserRooms`: membership active của (tenant, user), room id > `after` tăng dần, `limit` 1..`MaxUserRoomsLimit`.
- `MarkRead(room, user, seq)`: member active và `rs.s < seq` → `rs = {seq, v+1}` → `(mới, true, nil)`; không đổi → `(hiện tại, false, nil)`; không active → `ErrNotMember`. `MarkUnread(room, user, to)`: như trên với điều kiện `rs.s > to`. Cả hai cần `seq`/`to` ≤ `MaxInt64`.
- Feed (Task 4 khai báo, Task 6 phát): `MemberInserted ChangeKind = 6`; `Change` thêm `Member domain.MemberAction` (cuối, sau `Pin`).
- Write contract (`write_contract_test.go`): `MemberActions.Append` = `insert-unique`; `At/After/Between` = `read`; `MemberProjector.ApplyMembers` = `monotonic-cas`; `AdvanceMembers` = `cas`; `MemberReader.*` = `read`; `ReadPositions.MarkRead/MarkUnread` = `version-bump`. Thêm 4 port vào danh sách reflect.
- memstore (Task 4): `*Rooms` cài `MemberProjector`, `MemberReader`, `ReadPositions`; `func (s *Rooms) MemberActions() *MemberActions` (log fact nằm trong `Rooms`, `NewRooms()` không đổi chữ ký). Task 6: `Rooms.attach` gắn luôn log của `MemberActions` vào feed (không thêm `FeedOption`).
- storetest: `type MemberRooms interface { store.Rooms; store.HistoryClearer; store.MemberProjector; store.MemberReader; store.ReadPositions }`; `RunMembers(t, open func(*testing.T) (MemberRooms, store.MemberActions))` (Task 4); `RunMemberFeed(t, open func(*testing.T) (store.MemberActions, store.ChangeFeed))` (Task 6). Chạy trên memstore và mongostore (itest, mở `(s, s.MemberActions())`).

### Mongo (Task 5; feed Task 6)

- Hằng `memberActionsCollection = "member_actions"`, `userRoomsCollection = "user_rooms"`. `Store` thêm `memberActions *MemberActions` (collection primary) và `userRooms` (primary). `*Store` **không** cài `store.MemberActions` (trùng `Append/At/Between` của `Edits`); kiểu con `mongostore.MemberActions` qua `func (s *Store) MemberActions() *MemberActions`. `*Store` cài `MemberProjector`, `MemberReader`, `ReadPositions`.
- `member_actions` (clustered): `{_id: keys.MemberAction(room, mv), r: int64, t, op: int32, ch: [{u, ro, pr, rs: int64}], by, ts, n: int64, so (omitempty)}`. Index `{r: 1, ts: 1}` (`roomTimeIndexes`).
- `user_rooms` (clustered): `{_id: keys.UserRoom(t, u, r), t, u, r: int64, ro, ja, mv: int64, st: int32 omitempty}`; không index phụ.
- `members` thêm `st: int32` (chỉ ghi `1` khi removed; active = thiếu field, query `st: null`), `mv: int64`, `rs: {s: int64, v: int64}` (omitempty). Index mới `{r: 1, st: 1, ro: 1, ja: 1, u: 1}` (`Successor`, `Owners`); giữ `{r, u}` unique và `{t, u, r}`.
- `rooms` thêm `mv: int64` (omitempty); `decodeRoom` đọc `mv`; `Rooms.Get` giữ projection loại `pins`, `pv`.
- `ApplyMembers` = hai `BulkWrite(ordered: false)` (`members`, rồi `user_rooms`), mỗi model `UpdateOne(filter {r, u, mv: {$not: {$gte: k}}} / {_id, mv: {$not: {$gte: k}}}, pipeline [{$set …}], upsert)`; lỗi chỉ gồm trùng khoá → coi là đã áp. Re-add dùng `$$REMOVE` cho `st`, giữ `cb`, `rs.s = max`, `rs.v + 1`.
- `codec.go` (~187 dòng): Task 5 chuyển `memberDoc`/`encodeMember`/`decodeMember` sang file mới `member_codec.go` cùng codec fact/user_rooms/rs.
- `Bootstrap`: `member_actions`, `user_rooms` vào danh sách `ensureClustered`; index `{memberActionsCollection, roomTimeIndexes()}`; `memberIndexes()` thêm index mới.
- Feed `$match` (Task 6): `facts` thêm `memberActionsCollection` (chỉ insert). `decodeChange` case `memberActionsCollection` → `decodeMemberChange` (file mới `feed_member_change.go`) → `Change{Kind: MemberInserted, Member: a, CommittedAt: wallTime}`. Update của `members`, `user_rooms`, `rooms` (`mv/mc`) vẫn bị loại bởi `ns.coll`/`operationType`.

### `apps/core/internal/work` + `reconcile` (Task 4 case, Task 6)

- `RecordOf` (Task 4, do lint `exhaustive`): `MemberInserted` → `Room = c.Member.Room`, `Seq = c.Member.MV` (thread 0, version 0, không user).
- Task 6: `KnownKind` nhận `MemberInserted`; `ID()` → `"g:" + pbconv.MemberEventID(r.Room, r.Seq)`; `checkKind` sẵn có đã cấm đuôi user cho kind khác `ReactionChanged`. Reader forward kind mới (không đổi code reader ngoài test). `effects_wiring.go` đăng ký tạm `store.MemberInserted: {activity.Effect()}`; Task 15 thay bằng registry cuối.

### `apps/core/internal/memberproj` (Task 7, package mới)

```go
type Facts interface {
	After(ctx context.Context, room, mv uint64, limit int) ([]domain.MemberAction, error)
}
type Rooms interface {
	Get(ctx context.Context, id uint64) (domain.Room, error)
	store.MemberProjector
}

const MaxTries = 5

var ErrContended = fmt.Errorf("member projection contended: %w", apperr.ErrUnavailable)

func New(facts Facts, rooms Rooms) (*Projector, error)
func (p *Projector) Settle(ctx context.Context, room uint64) (domain.Room, error)
func (p *Projector) Project(ctx context.Context, room, target uint64) (domain.Room, error)
```

- `Settle`: tối đa `MaxTries` lượt: `Get` → `After(room, r.MemberVersion, MaxMemberScan)`; rỗng → trả `r`; mỗi fact theo thứ tự `ApplyMembers`; `AdvanceMembers(room, r.MemberVersion, last.MV, last.Count)` khớp → trang đầy thì lặp trang sau, không thì trả `r` với `MemberVersion/MemberCount` mới; trượt → lượt sau. Hết lượt → `ErrContended`. Fact có `MV` không liền (`≠ head+1`) → dừng, `store.ErrStaleRead`.
- `Project(room, target)`: như `Settle` nhưng `r.MemberVersion ≥ target` → trả ngay; sau khi hết fact mà `< target` → `store.ErrStaleRead`. Lỗi store bọc `%w` (room không có → `domain.ErrRoomNotFound`).
- `mutate` gọi `Settle`; `effects.member_projection` gọi `Project`. Không giữ trạng thái; dựng hai lần (service + effects).

### `apps/core/internal/actor` (Task 8)

```go
const memberCacheTTL = 10 * time.Second

func (r *Router) ForgetMembers(room uint64)
```

- Actor thêm `memberGen atomic.Uint64` và `seenGen uint64`; `ForgetMembers` giữ `r.mu.RLock`, actor có thì `memberGen.Add(1)`, không có thì thôi. `member()`: `memberGen.Load() != seenGen` → dựng LRU mới, cập nhật `seenGen`; entry LRU là `{m domain.Member, at time.Time}`, quá `memberCacheTTL` → đọc lại store. Không cache kết quả "không phải member". Test bằng `synctest`; `-count=5` trên `actor`.

### `apps/core/internal/access` (Task 9)

```go
const (
	AddMembers       Action = "add_members"
	RemoveMember     Action = "remove_member"
	LeaveRoom        Action = "leave_room"
	ChangeMemberRole Action = "change_member_role"
	MarkRead         Action = "mark_read"
)

type Request struct { /* field cũ */ ; Target domain.Member; Role domain.Role }

func (c *Checker) AdmitRoom(ctx context.Context, action Action, tenant, user string, room uint64) (Request, error)
```

- `AdmitRoom`: như `Admit` nhưng `ErrNotMember` → `Request` với `Member` zero, không lỗi (dùng cho `LeaveRoom`).
- `DefaultPolicy.Check` (luật cũ edit/delete giữ nguyên): `AddMembers` → caller `owner|admin`; `RemoveMember` → owner, hoặc admin khi `Target.Role == member`; `ChangeMemberRole` → chỉ owner; `LeaveRoom`, `MarkRead` → cho phép. Sai → `ErrDenied`. `LockedKinds` không áp cho action member/read. `AllowMembers` không đổi.

### `apps/core/internal/mutate` (Task 10: member; Task 13: read)

```go
type MemberProjector interface {
	Settle(ctx context.Context, room uint64) (domain.Room, error)
	Project(ctx context.Context, room, target uint64) (domain.Room, error)
}
type MemberForgetter interface{ ForgetMembers(room uint64) }
type ReadNotifier interface{ Offer(u domain.ReadUpdate) }

const (
	DefaultMemberBatch = 500
	MaxMemberBatch     = domain.MaxMemberChanges
)

type AddMembersCmd struct{ Tenant, User string; Room uint64; Users []string }
type RemoveMemberCmd struct{ Tenant, User string; Room uint64; Target string }
type LeaveRoomCmd struct{ Tenant, User string; Room uint64 }
type ChangeRoleCmd struct{ Tenant, User string; Room uint64; Target string; Role domain.Role }
type MemberResult struct{ Version uint64; Count int; Changed bool; Added []string; Successor string; Prev domain.Role }
type ReadCmd struct{ Tenant, User string; Room, Seq uint64 }

func (m *Mutator) AddMembers(ctx context.Context, c AddMembersCmd) (MemberResult, error)
func (m *Mutator) RemoveMember(ctx context.Context, c RemoveMemberCmd) (MemberResult, error)
func (m *Mutator) LeaveRoom(ctx context.Context, c LeaveRoomCmd) (MemberResult, error)
func (m *Mutator) ChangeMemberRole(ctx context.Context, c ChangeRoleCmd) (MemberResult, error)
func (m *Mutator) MemberBatch() int
func (m *Mutator) MarkRead(ctx context.Context, c ReadCmd) (domain.ReadPos, error)
func (m *Mutator) MarkUnread(ctx context.Context, c ReadCmd) (domain.ReadPos, error)
```

- `Limits` thêm `MemberBatch int` (Task 10): `cmp.Or(…, DefaultMemberBatch)`, 1..`MaxMemberBatch`, sai → `ErrInvalidArgument`.
- `Deps` thêm `MemberActions store.MemberActions`, `MemberReader store.MemberReader`, `MemberProj MemberProjector`, `Forget MemberForgetter` (Task 10) và `Reads store.ReadPositions`, `ReadCast ReadNotifier` (Task 13). `New` bắt buộc (nil → `errMissingDeps`, sửa thông điệp). Task nào thêm dep thì sửa `mutate/fixtures_test.go`, rig `grpcsrv` và `apps/core/service_wiring.go` trong cùng task.
- Thứ tự lệnh member: validate (`ValidUser`; Users khử trùng, 1..`MemberBatch` → không thì `ErrTooManyMembers`, rỗng → `invalid("users")`; `RemoveMember` với `Target == User` → `ErrInvalidArgument`) → `Admit` (`LeaveRoom` dùng `AdmitRoom`) → `room.Type == dm` → `ErrDirectRoom` → (add: `Messages.Last(room, 0)` một lần) → tối đa 3 lượt: `MemberProj.Settle` → `MembersOf(caller + targets)` → caller không active → `ErrNotMember` (`LeaveRoom`: no-op thành công) → `Allow(Request{Member: caller, Target, Role})` → no-op (add: mọi user đã active; remove: target không active; role: target không active → `ErrMemberNotFound`, cùng role → no-op) → kiểm owner cuối (role: tự hạ khi `Owners(room, 2)` chỉ còn mình → `ErrLastOwner`; leave của owner khi chỉ còn mình là owner → `Successor`) → `Append(fact MV = head+1, Count = head.Count ± Δ, At = now())` → `ErrMemberActionExists`: `At(room, mv)` cùng `Op`, `By`, tập user → kết quả, khác → lượt sau. Hết lượt → `domain.ErrRetryLater`.
- Sau append: `MemberProj.Project(room, mv)` (lỗi bỏ qua) → `Forget.ForgetMembers(room)` → `Events.Enqueue(room, pbconv.MemberEvents(room.Type, fact))` (lỗi bỏ qua) → `MemberResult` từ fact. No-op trả `{Version: head, Count: head.Count, Changed: false}`.
- `MarkRead`: `Admit(MarkRead)` → seq 0 hoặc `> room.LastSeq` → `Messages.Last(room, 0)`; seq 0 hoặc lớn hơn last → last; last 0 → trả `Read` hiện tại không ghi → `Reads.MarkRead`. `MarkUnread`: seq 0 → `invalid("seq")`; kẹp seq như trên → `Reads.MarkUnread(room, user, seq−1)`. Đổi (`true`) → `ReadCast.Offer(ReadUpdate{Room, Tenant, Type, Members: room.MemberCount, User, Pos, At: now()})`. Trả `ReadPos`.

### `apps/core/internal/readcast` (Task 12, package mới)

```go
type Publisher interface{ Enqueue(room uint64, events []*chatimv1.Event) error }

const (
	DefaultWindow     = 3 * time.Second
	MinWindow         = 100 * time.Millisecond
	MaxWindow         = time.Minute
	DefaultMaxMembers = 100
	MaxMembersCap     = 1000
	DefaultMaxPending = 65536
)

type Config struct{ Window time.Duration; MaxMembers, MaxPending int }

func (c Config) Validate() error
func New(pub Publisher, cfg Config) (*Caster, error)
func (c *Caster) Offer(u domain.ReadUpdate)
func (c *Caster) Run(ctx context.Context) error
func (c *Caster) Close(ctx context.Context) error
func (c *Caster) Unbatched() uint64
```

- Zero → mặc định (`cmp.Or`); `Window` trong `[MinWindow, MaxWindow]`, `MaxMembers` 1..`MaxMembersCap`, `MaxPending ≥ 1`.
- `Offer` an toàn đồng thời, không bao giờ chặn: khoá `(room, user)` chưa có hoặc đã quá W kể từ lần gửi → gửi ngay (leading edge), ghi `sentAt`; còn trong W → giữ bản `Pos.Version` lớn nhất làm pending. Map đầy (`MaxPending`) với khoá mới, hoặc sau `Close` → gửi thẳng, `Unbatched()++`.
- `Run`: ticker W/2; gửi pending đã đủ W, bỏ khoá rảnh quá W. `Close`: dừng `Run`, xả mọi pending vào `Enqueue`, idempotent.
- Gửi = `pub.Enqueue(u.Room, []*Event{pbconv.ReadUpdated(u, roomWide)})`, `roomWide = u.Type == RoomDM || u.Members <= MaxMembers`; lỗi enqueue bỏ qua (publisher đã đếm). Test `synctest` + goleak, `-count=5`.

### `apps/core/internal/pbconv` + proto + publish (Task 3)

```go
func MemberEventID(room, mv uint64) string
func MemberUserEventID(room, mv uint64, user string) string
func ReadEventID(room uint64, user string, version uint64) string
func MemberRole(r domain.Role) chatimv1.MemberRole
func DomainMemberRole(r chatimv1.MemberRole) (domain.Role, error)
func MemberEvents(roomType domain.RoomType, a domain.MemberAction) []*chatimv1.Event
func ReadUpdated(u domain.ReadUpdate, roomWide bool) *chatimv1.Event
```

- Id: `MemberEventID` = `RoomID(room) + "-m" + mv`; `MemberUserEventID` = `MemberEventID(...) + "-" + user`; `ReadEventID` = `RoomID(room) + "-rd-" + user + "-v" + version`.
- `MemberEvents`: phần tử đầu là bản room (`Recipient ""`), sau đó bản user theo thứ tự `Changes`, rồi `Successor` (nếu có, payload `member_removed` cùng fact). Envelope: `Tenant a.Tenant`, `RoomId`, `RoomType`, `Actor a.By`, `Ts a.At`, `Seq 0`. Payload theo `Op`: add → `MemberAdded{members, member_version, member_count}` (bản user chỉ chứa user đó); remove/leave → `MemberRemoved{user, reason REMOVED|LEFT, new_owner = a.Successor, member_version, member_count}`; role → `MemberRoleChanged{user, role, previous_role, member_version}`.
- `ReadUpdated`: id `ReadEventID(u.Room, u.User, u.Pos.Version)`, `Tenant`, `RoomId`, `RoomType`, `Actor u.User`, `Ts u.At`, `Recipient` = `""` khi `roomWide`, không thì `u.User`; payload `{user, seq, version}`.
- File proto mới `proto/chatim/v1/members.proto` (không comment):

```proto
enum MemberRole { MEMBER_ROLE_UNSPECIFIED = 0; MEMBER_ROLE_OWNER = 1; MEMBER_ROLE_ADMIN = 2; MEMBER_ROLE_MEMBER = 3; }
enum MemberRemovedReason { MEMBER_REMOVED_REASON_UNSPECIFIED = 0; MEMBER_REMOVED_REASON_REMOVED = 1; MEMBER_REMOVED_REASON_LEFT = 2; }
message RoomMember { string user = 1; MemberRole role = 2; }
message AddMembersRequest { string room_id = 1; repeated string users = 2; }
message AddMembersResponse { uint64 member_version = 1; int32 member_count = 2; repeated string added = 3; }
message RemoveMemberRequest { string room_id = 1; string user = 2; }
message RemoveMemberResponse { uint64 member_version = 1; int32 member_count = 2; bool changed = 3; }
message LeaveRoomRequest { string room_id = 1; }
message LeaveRoomResponse { uint64 member_version = 1; int32 member_count = 2; bool changed = 3; string new_owner = 4; }
message ChangeMemberRoleRequest { string room_id = 1; string user = 2; MemberRole role = 3; }
message ChangeMemberRoleResponse { uint64 member_version = 1; int32 member_count = 2; bool changed = 3; MemberRole previous_role = 4; }
message MarkReadRequest { string room_id = 1; uint64 seq = 2; }
message MarkReadResponse { uint64 read_seq = 1; uint64 read_version = 2; }
message MarkUnreadRequest { string room_id = 1; uint64 seq = 2; }
message MarkUnreadResponse { uint64 read_seq = 1; uint64 read_version = 2; }
```

- `core.proto`: import `members.proto`; service thêm `AddMembers`, `RemoveMember`, `LeaveRoom`, `ChangeMemberRole`, `MarkRead`, `MarkUnread`. `Room` không đổi.
- `events.proto`: import `members.proto`; `Event` thêm `string recipient = 10;`; oneof `MemberAdded member_added = 28; MemberRemoved member_removed = 29; MemberRoleChanged member_role_changed = 30; ReadUpdated read_updated = 31;` với `message MemberAdded { repeated RoomMember members = 1; uint64 member_version = 2; int32 member_count = 3; }`, `message MemberRemoved { string user = 1; MemberRemovedReason reason = 2; string new_owner = 3; uint64 member_version = 4; int32 member_count = 5; }`, `message MemberRoleChanged { string user = 1; MemberRole role = 2; MemberRole previous_role = 3; uint64 member_version = 4; }`, `message ReadUpdated { string user = 1; uint64 seq = 2; uint64 version = 3; }`.
- `publish`: hằng `memberAdded = "member_added"`, `memberRemoved = "member_removed"`, `memberRoleChanged = "member_role_changed"`, `readUpdated = "read_updated"` (`stream.go`) + `eventKind`. `Message`: `ev.GetRecipient() != ""` → phải `validToken`, subject `userSubject(root, tenant, recipient, kind)` = `{root}.{t}.user.{u}.{kind}`; rỗng → `roomSubject` như cũ. RePublish: `Source: root + ".*.*.*.*"`, `Destination: live + ".{{wildcard(1)}}.{{wildcard(2)}}.{{wildcard(3)}}.evt.{{wildcard(4)}}"` (subject room ra đúng như trước). `markKey` không mark bốn loại mới. Itest `publish/nats_integration_test.go`: stream tạo với luật cũ → `EnsureStream` luật mới không lỗi → event có recipient tới `live.{t}.user.{u}.evt.member_added`. NATS từ chối sửa → dừng, báo controller (dev dùng `make infra-reset`).

### `apps/core/internal/grpcsrv` (Task 11, 14)

- Task 11 `create_room.go`: `len(req.GetMembers()) > s.mutator.MemberBatch()` → `domain.ErrTooManyMembers` trước `NewRoom`; sau `Create` thành công enqueue một lô `[RoomCreated(room)] + MemberEvents(room.Type, domain.InitialMembers(room, members))` (lỗi bỏ qua).
- Task 14: file mới `grpcsrv/members.go` (4 RPC) và `grpcsrv/read.go` (2 RPC), dùng `callerAndRoom` rồi `s.mutator.*`; role qua `pbconv.DomainMemberRole` (`UNSPECIFIED` → `ErrInvalidArgument`). Response: `{MemberVersion: res.Version, MemberCount: int32 (kẹp như pbconv), Changed/Added, NewOwner: res.Successor, PreviousRole}`, `{ReadSeq: pos.Seq, ReadVersion: pos.Version}`. **`grpcsrv.Deps` không thêm field.**

### `apps/core/internal/effects` (Task 15)

```go
const (
	MemberProjectionName = "member_projection"
	MemberEventName      = "member_event"
)

type MemberFacts interface{ At(ctx context.Context, room, mv uint64) (domain.MemberAction, error) }
type MemberProjecter interface{ Project(ctx context.Context, room, target uint64) (domain.Room, error) }
type MemberForgetter interface{ ForgetMembers(room uint64) }

type MemberEventDeps struct{ Members MemberFacts; Rooms RoomReader; JS publish.JetStream }

func NewMemberProjection(p MemberProjecter, forget MemberForgetter) (*MemberProjection, error)
func NewMemberEvent(deps MemberEventDeps, cfg MessageChangedConfig) (*MemberEvent, error)
```

- `member_projection` (delay 0): mỗi room `Project(room, max Seq)`; thành công → `ForgetMembers(room)`; chỉ `ErrRoomNotFound` → dropped (giống `pin_projection`, M2b.3: lỗi dữ liệu không bị nuốt); lỗi khác (kể cả `ErrStaleRead`) → lỗi cho mọi record của room. `Dropped()`.
- `member_event` (`RECONCILE_DELAY`, không mark): `At(room, rec.Seq)` → loại room → `pbconv.MemberEvents` → publish **mọi** bản; record chỉ nil khi mọi bản PubAck; fact/room không còn → dropped. `Republished()` đếm từng bản PubAck không trùng. `eventPublisher` được mở rộng cho builder trả nhiều event (Part C chọn cách, hành vi effect cũ không đổi).
- Registry cuối: `store.MemberInserted: {activity, memberProjection, memberEvent}`. `effectSet.counters()` thêm `member_projection` (dropped) và `member_event` (republished + dropped). Không luật alert mới: MB1 dùng `ChatimEffectDropping`, `ChatimWorkFailing`, `ChatimRepublishSurge` (luật theo nhãn chung) → vẫn **16 luật**.
- Wiring trong file mới `apps/core/member_effects_wiring.go`; `wireEffects` thêm tham số `forget effects.MemberForgetter` (router) ngay trước `log`.

### `apps/core/internal/config` + lifecycle (Task 14)

| Env | Field | Mặc định | Kiểm |
|---|---|---|---|
| `MEMBER_BATCH_MAX` | `Config.Limits.MemberBatch` | 500 | 2..1000 (`mutate.Limits.Validate`; 1 sẽ chặn mọi DM) |
| `READ_RECEIPT_WINDOW` | `Config.ReadCast.Window` | 3s | 100ms..1m (`readcast.Config.Validate`) |
| `READ_RECEIPT_MAX_MEMBERS` | `Config.ReadCast.MaxMembers` | 100 | 1..1000 (`readcast.Config.Validate`) |

- `Config` thêm `ReadCast readcast.Config` (cuối); `componentErrors`: khoá `"REACTION_EMOJIS, PIN_LIMIT, MEMBER_BATCH_MAX"` cho `Limits`, thêm `{"READ_RECEIPT_*", c.ReadCast.Validate()}`. `StopPlan` thêm `ReadEvents time.Duration` (= `CloseTimeout`), có trong `total()`: mặc định 26.2s → 27.2s < 28s (không đổi `CORE_SHUTDOWN_BUDGET`, compose 33s).
- `app` thêm `reads drainer`; `serve`: `newSupervisor(ctx, 10)`, task `"read events"` khởi động ngay sau `publisher`; `shutdown`: `s.step("read events", plan.ReadEvents, a.reads.Close, t.reads)` ngay sau bước `grpc`. `stop_order_test.go` chờ `["read events", "reconciler", "workers", "router", "cid batcher", "flusher", "publisher"]`.
- `metrics_wiring.go`: `probes.readUnbatched func() uint64` → `read_events_unbatched_total` (help: "Read receipts sent without coalescing because the coalescer was full or closed."), không luật. README bảng env cập nhật.
- `wireService(st *mongostore.Store, router *actor.Router, pub *publish.Publisher, reads *readcast.Caster, lockedKinds []domain.Kind, limits mutate.Limits, log *slog.Logger)` (Task 13 đổi chữ ký, `wire` dựng `readcast.New(pub, readcast.Config{})`; Task 14 đổi sang `cfg.ReadCast`, gắn vào `app` và vòng đời).

### `apps/core/internal/resync` (Task 16)

```go
type Members interface {
	Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.MemberAction, error)
}

var ErrMemberPageFull = errors.New("resync: one instant holds more member actions than a member page")
```

- `Deps` thêm `Members Members` (cuối, trước `Pub`); `Report` thêm `MemberRecords int`; `String()` thêm `member_records=%d` ngay trước `dry_run`. Mỗi room: … → pins → members qua `scanByTime` (trang `store.MaxMemberScan`). Record `{Kind: MemberInserted, Room: a.Room, Seq: a.MV, CommittedAt: a.At}`. `resync_command.go` truyền `Members: st.MemberActions()`. `scan_test.go` (187 dòng) tách trước khi thêm case.

### Route + corecli (Task 17)

- `tools/internal/route/members.go`: `AddMembers`, `RemoveMember`, `LeaveRoom`, `ChangeMemberRole`, `MarkRead`, `MarkUnread`, dạng `func (c *Client) X(ctx, *chatimv1.XRequest) (*chatimv1.XResponse, Stats, error)` qua `inRoom` (mọi lệnh là trạng thái mong muốn, retry an toàn). `fakeCore` cài sáu method.
- `corecli`: lệnh `add-members` (`-room -users a,b`), `remove-member` (`-room -user`), `leave` (`-room`), `set-role` (`-room -user -role owner|admin|member`), `read`, `unread` (`-room -seq`); `watch` thêm `-user` (subject `live.{t}.user.{u}.>`). e2e thêm pha member + đọc (Part C chốt chi tiết): người bị xoá gửi tin và đọc lịch sử đều `PERMISSION_DENIED`; event `member_removed` tới subject user của họ; owner cuối rời → `new_owner` đúng; `MarkRead`/`MarkUnread` trả `read_version` tăng.

## Thứ tự task

| # | Task | Rủi ro | Phụ thuộc | Phần |
|---|---|---|---|---|
| 1 | Baseline: đồng bộ `feat/m2b`, quy tắc commit, `fmt-check vet lint test` xanh | — | — | A |
| 2 | `domain` (role, `ReadPos`, field mới, `MemberAction`, `Applied`, `InitialMembers`, `Successor`, lỗi) + `pkg/keys` | thấp | 1 | A |
| 3 | ★ Proto (`members.proto`, 6 RPC, `recipient = 10`, oneof 28–31) + `make proto` + pbconv + publish (subject user, RePublish) + itest RePublish | **cao** | 2 | A |
| 4 | Store port + `MemberInserted`/`Change.Member` + `RecordOf` + memstore (fact, projection, reader, read, `Create` mới, active filter) + storetest `RunMembers` + write contract | trung bình | 2, 3 | A |
| 5 | ★ mongostore: 2 collection clustered + index, `member_codec.go`, `Create` mới, `Member`/`ClearHistory` lọc removed, ports + itest contract | **cao** | 4 | A |
| 6 | ★ Feed (`$match`, `feed_member_change.go`, memstore attach) + `RunMemberFeed` + `KnownKind`/`ID` + reader forward + registry tạm | **cao** | 4, 5 | A |
| 7 | Package `memberproj` (`Settle`/`Project`) với test trên memstore. **Push** | trung bình | 4 | A |
| 8 | ★ Actor: thế hệ cache member + TTL, `ForgetMembers` | **cao** | 2 | B |
| 9 | ★ `access`: 5 action, `Target`/`Role`, `AdmitRoom`, `DefaultPolicy` | **cao** | 2 | B |
| 10 | ★ `mutate`: thêm/xoá/rời/đổi role, kế nhiệm owner, `Limits.MemberBatch`, Deps + wiring tạm | **cao** | 3, 4, 7, 8, 9 | B |
| 11 | ★ `CreateRoom`: event `member_added`, trần mỗi lệnh, bỏ trần 5000 của domain | **cao** | 10 | B |
| 12 | ★ Package `readcast` | **cao** | 3 | B |
| 13 | `mutate.MarkRead`/`MarkUnread` + Deps (`Reads`, `ReadCast`) + `wireService` nhận `readcast` | trung bình | 10, 12 | B |
| 14 | ★ Config (3 env) + `grpcsrv/members.go`, `read.go` + vòng đời `readcast` (bước dừng, StopPlan) + metric + README. **Push** | **cao** | 11, 13 | B |
| 15 | ★ Effect `member_projection`, `member_event` + registry + counters + wiring router | **cao** | 6, 7, 8, 14 | C |
| 16 | Resync `member_actions` | trung bình | 5, 6 | C |
| 17 | Route + corecli + e2e member/đọc | thấp | 14 | C |
| 18 | Itest: core chết giữa fact và projection; hai admin xoá nhau; event trên subject user; cập nhật RePublish trên stream có sẵn; người bị xoá gửi/đọc bị từ chối | trung bình | 14–16 | C |
| 19 | Docs: thiết kế (§4, §5, §5.1, §6.3, §6.4, §8.3, §9.3, §11, §12 MB1, D72 sửa, D96–D107), roadmap, CLAUDE.md, INDEXES, "Kết quả thực thi" | thấp | tất cả | C |
| 20 | Kiểm chứng cuối (`fmt-check vet lint vuln test itest`, image + `core-up` + e2e, `/metrics`, `alerts-check` 16 luật, resync dry-run, corebench ngắn). **Push** | — | tất cả | C |

Ghi chú phụ thuộc:
- Task 4 đổi `Rooms.Create` của memstore sang đường fact; Task 5 làm cùng cho Mongo. Từ Task 4 mọi test tạo room qua memstore đi đường mới (`MemberCount` vẫn = số member).
- Task 6 đăng ký tạm `MemberInserted → [room_activity]`, nên record member tới worker từ Task 6 không bị bỏ trôi; projection/event của worker có từ Task 15.
- Task 10 và 13 làm `mutate.New` chặt hơn, nên chính task đó sửa `mutate/fixtures_test.go`, rig `grpcsrv` và `service_wiring.go`. Task 10 truyền `router` làm `Forget`, `memberproj.New(st.MemberActions(), st)`.
- Task 8, 9, 12 khác file, có thể review song song khi làm task kế.
- Task 15 cần `ForgetMembers` (Task 8), `memberproj` (Task 7), wiring Task 14 xong.

---

## Ghi chú tích hợp (controller, khi ghép 3 phần)

- **Commit có pathspec.** `git add` đúng file mới, `git commit -m "..." -- <paths>`; cấm `add -A`, `commit -a`, `stash`. `git show --stat HEAD` chỉ liệt kê file của task. File chung đang có thay đổi chưa commit của người khác (`INDEXES.csv`, `README.md`, `CLAUDE.md`, docs) → **dừng, báo controller**.
- **Tên phải giống hệt giữa các phần:** `store.MemberInserted` (6), `store.MemberActions/MemberProjector/MemberReader/ReadPositions`, `store.ErrMemberActionExists/NotFound`, `(*mongostore.Store).MemberActions()`, `(*memstore.Rooms).MemberActions()`, `memberproj.Projector.Settle/Project`, `actor.Router.ForgetMembers`, `access.AdmitRoom`, `mutate.*Cmd/MemberResult/ReadCmd`, `readcast.Caster`, `pbconv.MemberEvents/ReadUpdated/MemberEventID/MemberUserEventID/ReadEventID`, tên effect và hằng ở trên. Part B/C không đổi chữ ký của Part A; cần đổi thì báo controller để sửa hợp đồng.
- **Bất biến xuyên phần:**
  1. Đầu `rooms.mv` chỉ tiến sau khi mọi doc của fact đã áp (`ApplyMembers` trước `AdvanceMembers`); không bao giờ CAS đầu khi chưa áp.
  2. Quyền của lệnh member kiểm **sau** settle, trên doc caller/target ở đúng `mv` của đầu; `Admit` chỉ là cổng sớm, có thể cũ.
  3. Doc `members`/`user_rooms` không bao giờ lùi `mv`; tombstone không bao giờ xoá (giữ `mv`, `cb`, `rs`).
  4. `rs.v` chỉ tăng (vào lại cũng `+1`), nên id `read_updated` không lặp.
  5. Fast path không báo lỗi vì effect: lỗi `Project`, `ForgetMembers`, enqueue bỏ qua sau khi fact commit; worker hội tụ. Lỗi của chính lệnh (validate, quyền, `ErrDirectRoom`, `ErrLastOwner`, `ErrMemberNotFound`, `ErrTooManyMembers`, `ErrRetryLater`) trả về client qua `pkg/grpcserver`.
  6. Bản user chỉ gửi cho user bị thêm, bị xoá/rời, đổi role, và owner kế nhiệm; bản room luôn có.
- **Id event và record (không trùng nhau trong cửa sổ bỏ trùng của stream):**

  | Loại | Event id | Record id |
  |---|---|---|
  | tin mới | `{room}-{th}-{seq}` | `m:` + event id |
  | sửa/xoá | `{room}-{th}-{seq}-v{ver}` | `e:` + event id |
  | reaction | `{room}-{th}-{seq}-{u}-n{n}` | `x:` + event id |
  | số reaction | `{room}-{th}-{seq}-reactions-v{v}` | — |
  | ghim | `{room}-p{pv}` | `p:` + event id |
  | room | `{room}-created` | `r:{room}` |
  | member, bản room | `{room}-m{mv}` | `g:` + event id (một record cho cả fact) |
  | member, bản user | `{room}-m{mv}-{u}` | — (cùng record `g:`) |
  | đã đọc | `{room}-rd-{u}-v{v}` | — (không record) |

  Token thứ hai sau `{room}-` phân biệt loại: số (tin, sửa, reaction, số reaction), `created`, `p\d`, `m\d`, `rd-`. Bản room `-m{mv}` không có `-` sau mv; bản user có `-{u}`. Tiền tố record `g:` chưa dùng. Task 3 có test bảng chốt các dạng không trùng nhau (user `m1`, `rd`, `v1`, `a-b`). Id dài nhất ~100 ký tự.
- **Thứ tự effect** (Task 15 viết, Task 6 viết dòng tạm): `MemberInserted → [room_activity (0), member_projection (0), member_event (RECONCILE_DELAY)]`; delay tăng dần. `room_activity` để `Seq = 0` cho kind 6 (đã đúng từ D91), nên fact member chỉ đẩy `lc/ab`.
- **Không ack mark** cho event member và `read_updated`; worker gửi lại event member, stream bỏ trùng theo id; `read_updated` không có đường bù.
- **Ai sửa file chung (tuần tự, không hai task cùng lúc):**

  | File | Task |
  |---|---|
  | `apps/core/service_wiring.go` | 10 → 13 → 14 |
  | `apps/core/wiring.go` | 13 → 14 → 15 |
  | `apps/core/effects_wiring.go` | 6 → 15 |
  | `apps/core/member_effects_wiring.go` (mới) | 15 |
  | `apps/core/metrics_wiring.go`, `lifecycle.go`, `shutdown.go`, `stop_order_test.go` | 14 |
  | `apps/core/resync_command.go` | 16 |
  | `apps/core/internal/config/*` | 14 |
  | `apps/core/internal/mutate/mutator.go`, `fixtures_test.go` | 10 → 13 |
  | `apps/core/internal/mutate/limits.go` | 10 |
  | `apps/core/internal/grpcsrv/harness_test.go`, `fake_dependencies_test.go` | 10 → 11 → 13 → 14 |
  | `apps/core/internal/grpcsrv/create_room.go`, `domain/validate.go` | 11 |
  | `apps/core/internal/store/write_contract_test.go`, `store/feed.go`, `work/record.go` (`RecordOf`) | 4 (`work/record.go` tiếp 6) |
  | `apps/core/internal/store/mongostore/codec.go`, `bootstrap.go`, `mongostore.go`, `rooms.go` | 5 |
  | `mongostore/feed.go`, `feed_change.go`, `memstore/feed.go`, `memstore/change_log.go` | 6 |
  | `apps/core/internal/effects/ports.go` | 15 |
  | `proto/chatim/v1/*.proto`, `pkg/pb/chatim/v1/*`, `publish/message.go`, `publish/stream.go` | 3 |
  | `apps/core/internal/actor/*` | 8 |
  | `apps/core/internal/access/*` | 9 |
  | `tools/internal/route/*`, `tools/corecli/*`, `scripts/e2e.sh` | 17 |
  | `README.md` | 14 (env) → 19 |
  | `INDEXES.csv` | mọi task theo danh sách dưới |

- **Dòng `INDEXES.csv` mỗi task sửa:** 2: `domain`, `pkg/keys`; 3: dòng mới `proto/chatim/v1/members.proto`, `core.proto`, `events.proto`, `pkg/pb/chatim/v1`, `pbconv`, `publish`; 4: `store`, `store/memstore`, `store/storetest`, `work`; 5: `store/mongostore`; 6: `store/mongostore`, `store/memstore`, `store/storetest`, `work`, `reconcile`, `apps/core`; 7: dòng mới `apps/core/internal/memberproj`; 8: `actor`; 9: `access`; 10: `mutate`, `apps/core`; 11: `grpcsrv`, `domain`; 12: dòng mới `apps/core/internal/readcast`; 13: `mutate`, `apps/core`; 14: `config`, `grpcsrv`, `apps/core`, `README.md`; 15: `effects`, `apps/core`; 16: `resync`, `apps/core`; 17: `tools/internal/route`, `tools/corecli`, `tools/corecli/internal/e2e`, `scripts/e2e.sh`; 19: thiết kế, roadmap, plan này và bản tóm tắt (dòng mới), `CLAUDE.md`. Cột decisions thêm `D96`–`D107` đúng chỗ (trong ngoặc kép khi có dấu phẩy).
- **Khoảng trống tạm (chỉ dev):**
  - Task 5 → 6: Mongo ghi fact nhưng feed chưa xem `member_actions`.
  - Task 6 → 15: worker chỉ chạy `room_activity` cho record member rồi ack; projection và event chỉ từ fast path (lệnh có từ Task 10, RPC từ Task 14).
  - Task 8 → 10: `ForgetMembers` chưa ai gọi; chỉ TTL 10s.
  - Task 13 → 14: `readcast` được dựng trong `wire` nhưng chưa chạy `Run`/`Close` (pending chỉ xả khi map đầy); không chạy core ở khoảng này.
  - Task 10 → 11: `CreateRoom` chưa phát `member_added`.
- **Rolling deploy (prod chưa live, ghi vào D102/D106 ở Task 19):**
  - `EnsureStream` chạy mỗi lần core khởi động và ghi đè RePublish: core cũ khởi động lại sẽ trả luật về `evt.*.room.*.*`, bản user ngừng tới `live.*.user.*` cho tới khi core mới khởi động. Nâng mọi core cùng lúc. Dev: NATS từ chối sửa RePublish → `make infra-reset`.
  - Core cũ `Nak` record kind 6 (D91); `ChatimWorkFailing` kêu trong lúc còn core cũ.
  - Core cũ không lọc `members.st`: người đã bị xoá vẫn gửi được qua core cũ. Cửa sổ phiên bản lẫn phải ngắn.
  - Room tạo trước M2b.4 không có `mv`, không có `user_rooms`: lệnh member đầu tiên ghi mv 1, `mc` cũ vẫn đúng; `UserRooms` thiếu các room cũ. Dev nên `make infra-reset` trước Task 20.
- **Detector:** sau Task 15, `/metrics` có `effect_dropped_total{effect}` cho `member_projection`, `member_event`, `reconcile_republished_total{effect="member_event"}`; sau Task 14 có `read_events_unbatched_total`. `make alerts-check` vẫn báo 16 luật; Task 19 thêm dòng MB1 vào §12.
- **File gần 200 dòng (kiểm `wc -l` ở task tương ứng):** `mongostore/codec.go` (187, Task 5 tách member codec), `mongostore/codec_test.go` (192), `mongostore/bootstrap_integration_test.go` (188, test mới để file riêng), `resync/scan_test.go` (187, Task 16 tách), `apps/core/wiring.go` (150, Task 14/15 cân nhắc tách `wireProbes`), `actor/room_actor.go` (170, Task 8 đặt logic cache trong file mới `member_cache.go`), `effects_wiring.go` (~110).

### Ghi chú controller khi ghép (2026-10-06)

- `MEMBER_BATCH_MAX` hợp lệ 2..1000 (1 sẽ chặn mọi DM vì `CreateRoom` đếm cả hai người).
- `member_projection` chỉ drop khi `domain.ErrRoomNotFound`, giống `pin_projection` (M2b.3); lỗi dữ liệu (`ErrInvalidArgument`) được retry để hiện ở `work_failures_total`. Task 15 dùng đúng code và test đã sửa trong plan; file `member_projection.go` phải import `errors` và `domain` nếu chưa có.
- Hai script sửa INDEXES cùng tồn tại, khác đường dẫn: Task 8–14 dùng `<scratchpad>/indexes_edit.py` (Task 8 Step 0 tạo), Task 15–19 dùng `bin/indexes_edit.py` (gitignored, Task 15 tạo). Không commit script nào.
- corecli dùng `-target` cho người đích của `remove-member`/`set-role` (`-user` là người gọi).
- "Hai admin xoá nhau" không xảy ra được theo D99; test chạy đua dùng hai owner xoá nhau.
- D101: Mongo so `_id` binary theo độ dài trước rồi mới tới byte; quét theo (tenant, user) vẫn đúng vì mọi khoá của cùng tenant+user dài bằng nhau, nhưng thiết kế không được nói "thứ tự byte giữ (tenant, user, room)" xuyên độ dài. Task 19 sửa câu đó.
- Plan commit (controller) đã thêm hai dòng INDEXES `docs/plans/2026-10-06-m2b4-members-read.md` và `...-summary.md`; Task 19 chỉ sửa purpose của chúng (grep phải ra đúng 2 dòng).
- Task 14 thêm vào bảng `validate_test.go` một ca `MEMBER_BATCH_MAX=1` → lỗi khoá `REACTION_EMOJIS, PIN_LIMIT, MEMBER_BATCH_MAX` (khớp code `mutate.Limits` đã sửa 2..1000).
- **Task 10, `LeaveRoom` của người chưa từng là member (sửa khi ghép, bảo mật):** nếu caller không có doc member nào (không phải tombstone) → `domain.ErrNotMember` (`PERMISSION_DENIED`), không trả `member_version`/`member_count`, để người cùng tenant không dò được room. Doc tombstone (đã rời/bị xoá) → no-op như plan. Implementer thêm nhánh này vào `changeMembers`/plan của `LeaveRoom` và một test; ghi là lệch có chủ ý.
- **Task 10, admin xoá người chưa từng là member:** nhận `PERMISSION_DENIED` (owner nhận no-op) vì `Allow` chạy trước kiểm no-op với `Target` rỗng. Giữ nguyên, có test; Task 19 ghi vào D100.
- **Task 17, bước 2 e2e:** khẳng định không có `read_updated` nào của bob trước bước 7, để thứ tự v2 (ngay) / v3 (sau cửa sổ) không phụ thuộc may rủi.
- `member_event` chỉ drop khi room không còn hoặc fact không còn (`ErrMemberActionNotFound`); dữ liệu hỏng retry, giống `member_projection` (đã sửa trong code Task 15).
- Part A đã chạy thật trên bản sao repo (`gofmt`, vet, lint, test, `make itest` sau Task 5 và 6): NATS 2.15 nhận đổi RePublish trên stream có sẵn, dev không cần `infra-reset`.

---

### Task 1: Baseline

Toàn bộ M2b dùng chung nhánh `feat/m2b`. Plan này đã được controller commit trước khi thực thi. Task này đồng bộ nhánh, chốt quy tắc commit và chụp baseline xanh, để lỗi ở task sau không lẫn với lỗi có sẵn.

**Step 1: Đồng bộ nhánh**

```bash
git switch feat/m2b
git pull --ff-only
git branch --show-current
git log --oneline -1
git status --short
```

Expected: `feat/m2b`; HEAD là commit plan M2b.4 (con của `3442a6c`). `git status --short` không có dòng nào thuộc `apps/`, `proto/`, `pkg/`, `tools/`, `deploy/`, `scripts/` hoặc `INDEXES.csv`. Owner có thể đang có thay đổi doc riêng (`docs/...`, `CLAUDE.md`, `README.md`): không đụng, không stage, không stash, không revert.

Nếu `INDEXES.csv` có thay đổi chưa commit (` M INDEXES.csv` hoặc `M  INDEXES.csv`): **dừng và báo controller**. Mọi task dưới đây sửa `INDEXES.csv` và commit theo pathspec, nên sẽ cuốn luôn thay đổi của owner trong file đó.

**Quy tắc commit cho mọi task của M2b.4:**

- File **mới**: `git add <đúng file mới>` (không add thư mục).
- Commit: `git commit -m "<message>" -- <mọi path task sửa hoặc tạo>`. Có pathspec thì git commit đúng các path đó; file owner đã stage vẫn ở nguyên trong index.
- Cấm `git add -A`, `git add .`, `git commit -a`, `git stash`.
- Sau mỗi commit: `git show --stat HEAD` chỉ được liệt kê file của task.
- Không `git add INDEXES.csv`: pathspec của `git commit` đã gồm file đó. Sau mỗi lần sửa CSV: `python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"` → Expected `{7}`.
- Message Conventional Commits, không nhắc AI, không có dòng `Co-Authored-By`.

**Step 2: Baseline**

Run: `make fmt-check && make vet && make lint && make test`
Expected: tất cả xanh. Có gì đỏ thì dừng và báo cáo, không bắt đầu Task 2.

Task này không commit.

---

### Task 2: `domain` (role, `ReadPos`, field mới, `MemberAction`, `Applied`, `InitialMembers`, `Successor`, lỗi) + `pkg/keys` (`MemberAction`, `UserRoom`)

Đặt kiểu dữ liệu chung cho mọi task sau. `MemberAction` là fact bất biến của `member_actions` (D96): mỗi fact có `MV` dày, op, danh sách thay đổi, người làm, thời điểm, số member sau fact (`Count`, D98) và người kế nhiệm owner (`Successor`, chỉ khi rời). `Applied` là hàm thuần định nghĩa projection của **một** user cho mọi adapter (memstore Task 4, Mongo Task 5 viết lại đúng ngữ nghĩa này bằng pipeline), nên fast path và worker cho cùng kết quả.

Ngữ nghĩa `Applied(cur, user)` (hợp đồng):
- add: hồi sinh (`Removed=false`), role của change, `JoinedAt = a.At`, `Read = {max(cur.Read.Seq, c.ReadSeq), cur.Read.Version+1}` (vào lại không bao giờ hạ vị trí đọc; `v` luôn tăng nên id `read_updated` không lặp, D104), giữ `ClearedBeforeSeq`;
- remove/leave: tombstone `Removed=true`, giữ mọi thứ khác;
- role: chỉ đổi role;
- `user == a.Successor`: role thành owner;
- mọi nhánh đặt `MV = a.MV`, `Room/Tenant/User` từ fact; user không có trong fact và không phải successor → trả nguyên `cur`.

`Room` và `Member` vẫn so sánh được bằng `==` (field mới là `uint64`, `bool`, `ReadPos` struct số), nên test cũ dùng `==`/`slices.Equal` trên hai kiểu này không vỡ. `MemberAction` có slice nên **không** so sánh được bằng `==` (test dùng `reflect.DeepEqual`).

`UserRoom` = `tenant 0x00 user 0x00 room(8)` (D101). Ident hợp lệ không chứa `0x00`, nên tiền tố của user `ab` không bao giờ là tiền tố khoá của user `abc`, và mọi room của một user nằm liền nhau theo id.

**Files:**
- Create: `pkg/keys/member.go`, `pkg/keys/member_test.go`
- Modify: `apps/core/internal/domain/room.go`, `apps/core/internal/domain/errors.go`, `apps/core/internal/domain/errors_test.go`
- Create: `apps/core/internal/domain/member.go`, `apps/core/internal/domain/member_test.go`, `apps/core/internal/domain/member_applied_test.go`
- Modify: `INDEXES.csv` (dòng `apps/core/internal/domain`, `pkg/keys`)

**Step 1: Test**

`pkg/keys/member_test.go`:

```go
package keys

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestMemberActionKeysRoundTripAndSortByVersion(t *testing.T) {
	b := MemberAction(9, 77)
	room, mv, err := ParseMemberAction(b)
	if len(b) != MemberActionLen || MemberActionLen != 16 || err != nil || room != 9 || mv != 77 {
		t.Fatalf("ParseMemberAction(MemberAction(9, 77)) = %d %d %v from %d bytes", room, mv, err, len(b))
	}
	if bytes.Compare(MemberAction(9, 1), MemberAction(9, 2)) >= 0 || bytes.Compare(MemberAction(9, math.MaxUint64), MemberAction(10, 0)) >= 0 {
		t.Fatal("member action keys must sort by room then version")
	}
}

func TestUserRoomKeyIsTenantUserAndRoom(t *testing.T) {
	want := append([]byte("acme\x00bob\x00"), 0, 0, 0, 0, 0, 0, 1, 2)
	if got := UserRoom("acme", "bob", 258); !bytes.Equal(got, want) {
		t.Fatalf("UserRoom(acme, bob, 258) = %x, want %x", got, want)
	}
	if prefix := UserRoomPrefix("acme", "bob"); !bytes.Equal(prefix, want[:len(want)-8]) {
		t.Fatalf("UserRoomPrefix(acme, bob) = %x, want %x", prefix, want[:len(want)-8])
	}
	widest := UserRoom(strings.Repeat("t", 32), strings.Repeat("U", 64), math.MaxUint64)
	if len(widest) != MaxUserRoomLen || MaxUserRoomLen != 106 {
		t.Fatalf("widest user room key = %d bytes, MaxUserRoomLen %d; want 106", len(widest), MaxUserRoomLen)
	}
	for _, c := range []struct {
		tenant, user string
		room         uint64
	}{
		{"a", "b", 1},
		{"acme", "bob", 258},
		{"acme", "a-b_C", 0},
		{strings.Repeat("t", 32), strings.Repeat("U", 64), math.MaxUint64},
	} {
		tenant, user, room, err := ParseUserRoom(UserRoom(c.tenant, c.user, c.room))
		if err != nil || tenant != c.tenant || user != c.user || room != c.room {
			t.Fatalf("ParseUserRoom(UserRoom(%q, %q, %d)) = %q %q %d %v", c.tenant, c.user, c.room, tenant, user, room, err)
		}
	}
}

func TestUserRoomKeysKeepUsersAndTenantsApart(t *testing.T) {
	if bytes.HasPrefix(UserRoom("acme", "abc", 1), UserRoomPrefix("acme", "ab")) {
		t.Fatal("the prefix of user ab matches a key of user abc")
	}
	if bytes.HasPrefix(UserRoom("acme", "x", 1), UserRoomPrefix("ac", "me")) {
		t.Fatal("the prefix of tenant ac, user me matches a key of tenant acme")
	}
	if bytes.Compare(UserRoom("acme", "ab", math.MaxUint64), UserRoom("acme", "abc", 0)) >= 0 {
		t.Fatal("every room of user ab must sort before the rooms of user abc")
	}
	if bytes.Compare(UserRoom("acme", "ab", 1), UserRoom("acme", "ab", 2)) >= 0 || bytes.Compare(UserRoom("acme", "ab", 255), UserRoom("acme", "ab", 256)) >= 0 {
		t.Fatal("the rooms of one user must sort by id")
	}
}

func TestParseUserRoomAndMemberActionRejectMalformedKeys(t *testing.T) {
	room := []byte{0, 0, 0, 0, 0, 0, 0, 1}
	cases := map[string][]byte{
		"empty":            nil,
		"room only":        room,
		"no separators":    append([]byte("acmebobxx"), room...),
		"empty tenant":     append([]byte("\x00bob\x00"), room...),
		"empty user":       append([]byte("acme\x00\x00"), room...),
		"three separators": append([]byte("acme\x00b\x00b\x00"), room...),
		"too long":         append([]byte(strings.Repeat("a", 33)+"\x00"+strings.Repeat("b", 64)+"\x00"), room...),
	}
	for name, b := range cases {
		if _, _, _, err := ParseUserRoom(b); !errors.Is(err, ErrLength) {
			t.Errorf("ParseUserRoom(%s) err = %v, want ErrLength", name, err)
		}
	}
	for _, n := range []int{0, MemberActionLen - 1, MemberActionLen + 1} {
		if _, _, err := ParseMemberAction(make([]byte, n)); !errors.Is(err, ErrLength) {
			t.Errorf("ParseMemberAction(%d bytes) err = %v, want ErrLength", n, err)
		}
	}
}
```

`apps/core/internal/domain/errors_test.go`, trong `TestDomainErrorsWrapAppKinds` thay:

```go
		{domain.ErrTooManyPins, apperr.ErrFailedPrecondition},
	}
```

bằng:

```go
		{domain.ErrTooManyPins, apperr.ErrFailedPrecondition},
		{domain.ErrDirectRoom, apperr.ErrFailedPrecondition},
		{domain.ErrLastOwner, apperr.ErrFailedPrecondition},
		{domain.ErrMemberNotFound, apperr.ErrNotFound},
		{domain.ErrTooManyMembers, apperr.ErrInvalidArgument},
	}
```

`apps/core/internal/domain/member_test.go` (`assertInvalid` có sẵn ở `validate_test.go`):

```go
package domain_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

var joinedAt = time.Unix(1_700_000_000, 0).UTC()

func TestMemberOpsAreTheStoredValues(t *testing.T) {
	ops := []domain.MemberOp{domain.MemberOpAdd, domain.MemberOpRemove, domain.MemberOpLeave, domain.MemberOpRole}
	for i, op := range ops {
		if int(op) != i+1 {
			t.Fatalf("member op %d = %d, want %d: ops are stored in member_actions.op", i, op, i+1)
		}
	}
	if domain.MaxMemberChanges != 1000 {
		t.Fatalf("MaxMemberChanges = %d, want 1000", domain.MaxMemberChanges)
	}
}

func TestParseRole(t *testing.T) {
	for _, r := range []domain.Role{domain.RoleOwner, domain.RoleAdmin, domain.RoleMember} {
		if got, err := domain.ParseRole(string(r)); err != nil || got != r {
			t.Fatalf("ParseRole(%q) = %q, %v; want %q", r, got, err, r)
		}
	}
	for _, s := range []string{"", "Owner", "guest", "admin "} {
		_, err := domain.ParseRole(s)
		assertInvalid(t, err, "role")
	}
}

func TestInitialMembersIsFactOneOfTheNewRoom(t *testing.T) {
	room, members, err := domain.NewRoom("acme", "alice", domain.RoomGroup, "Team", []string{"alice", "bob"}, joinedAt, 42)
	if err != nil {
		t.Fatalf("NewRoom: %v", err)
	}
	want := domain.MemberAction{
		Room: 42, MV: 1, Tenant: "acme", Op: domain.MemberOpAdd, By: "alice", At: joinedAt, Count: 2,
		Changes: []domain.MemberChange{{User: "alice", Role: domain.RoleOwner}, {User: "bob", Role: domain.RoleMember}},
	}
	if got := domain.InitialMembers(room, members); !reflect.DeepEqual(got, want) {
		t.Fatalf("InitialMembers = %+v, want %+v", got, want)
	}
}

func candidate(user string, role domain.Role, joined time.Duration) domain.Member {
	return domain.Member{Room: 42, Tenant: "acme", User: user, Role: role, JoinedAt: joinedAt.Add(joined)}
}

func TestSuccessorPicksTheEarliestAdminThenTheEarliestMember(t *testing.T) {
	gone := candidate("aaron", domain.RoleAdmin, 0)
	gone.Removed = true
	cases := []struct {
		name string
		in   []domain.Member
		want string
	}{
		{"an admin beats an earlier member", []domain.Member{candidate("bob", domain.RoleMember, 0), candidate("dave", domain.RoleAdmin, time.Hour)}, "dave"},
		{"the earliest admin", []domain.Member{candidate("erin", domain.RoleAdmin, 2*time.Hour), candidate("dave", domain.RoleAdmin, time.Hour)}, "dave"},
		{"the earliest member without admins", []domain.Member{candidate("carol", domain.RoleMember, time.Hour), candidate("bob", domain.RoleMember, 0)}, "bob"},
		{"ties go to the smaller user id", []domain.Member{candidate("carl", domain.RoleMember, 0), candidate("bob", domain.RoleMember, 0)}, "bob"},
		{"owners and removed members never succeed", []domain.Member{candidate("alice", domain.RoleOwner, 0), gone, candidate("zed", domain.RoleMember, 3*time.Hour)}, "zed"},
		{"nobody is left", []domain.Member{candidate("alice", domain.RoleOwner, 0), gone}, ""},
		{"no candidates", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := domain.Successor(c.in)
			if ok != (c.want != "") || got.User != c.want {
				t.Fatalf("Successor = %+v, %v; want %q", got, ok, c.want)
			}
		})
	}
}
```

`apps/core/internal/domain/member_applied_test.go`:

```go
package domain_test

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

var factAt = joinedAt.Add(10 * time.Minute)

func memberFact(op domain.MemberOp, changes ...domain.MemberChange) domain.MemberAction {
	return domain.MemberAction{Room: 42, MV: 5, Tenant: "acme", Op: op, Changes: changes, By: "alice", At: factAt, Count: 3}
}

func edited(m domain.Member, edit func(*domain.Member)) domain.Member {
	edit(&m)
	return m
}

func TestAppliedFollowsTheOpOfTheFact(t *testing.T) {
	bob := domain.Member{
		Room: 42, Tenant: "acme", User: "bob", Role: domain.RoleMember, JoinedAt: joinedAt,
		ClearedBeforeSeq: 4, MV: 2, Read: domain.ReadPos{Seq: 9, Version: 3},
	}
	gone := edited(bob, func(m *domain.Member) { m.Removed, m.MV = true, 5 })
	leave := memberFact(domain.MemberOpLeave, domain.MemberChange{User: "alice", Prev: domain.RoleOwner})
	leave.Successor = "bob"
	cases := []struct {
		name string
		fact domain.MemberAction
		cur  domain.Member
		user string
		want domain.Member
	}{
		{
			"add revives a removed member with the new role, the join time and a raised read position",
			memberFact(domain.MemberOpAdd, domain.MemberChange{User: "bob", Role: domain.RoleAdmin, ReadSeq: 12}), gone, "bob",
			edited(bob, func(m *domain.Member) {
				m.Role, m.JoinedAt, m.MV, m.Read = domain.RoleAdmin, factAt, 5, domain.ReadPos{Seq: 12, Version: 4}
			}),
		},
		{
			"add never lowers the read position",
			memberFact(domain.MemberOpAdd, domain.MemberChange{User: "bob", Role: domain.RoleMember, ReadSeq: 2}), bob, "bob",
			edited(bob, func(m *domain.Member) { m.JoinedAt, m.MV, m.Read = factAt, 5, domain.ReadPos{Seq: 9, Version: 4} }),
		},
		{
			"add of a new user starts from the fact",
			memberFact(domain.MemberOpAdd, domain.MemberChange{User: "carol", Role: domain.RoleMember, ReadSeq: 7}), domain.Member{}, "carol",
			domain.Member{Room: 42, Tenant: "acme", User: "carol", Role: domain.RoleMember, JoinedAt: factAt, MV: 5, Read: domain.ReadPos{Seq: 7, Version: 1}},
		},
		{"remove keeps everything but the state", memberFact(domain.MemberOpRemove, domain.MemberChange{User: "bob", Prev: domain.RoleMember}), bob, "bob", gone},
		{"leave keeps everything but the state", memberFact(domain.MemberOpLeave, domain.MemberChange{User: "bob", Prev: domain.RoleMember}), bob, "bob", gone},
		{
			"a role change sets only the role",
			memberFact(domain.MemberOpRole, domain.MemberChange{User: "bob", Role: domain.RoleAdmin, Prev: domain.RoleMember}), bob, "bob",
			edited(bob, func(m *domain.Member) { m.Role, m.MV = domain.RoleAdmin, 5 }),
		},
		{"the successor becomes owner", leave, bob, "bob", edited(bob, func(m *domain.Member) { m.Role, m.MV = domain.RoleOwner, 5 })},
		{"the leaver is removed", leave, edited(bob, func(m *domain.Member) { m.User = "alice" }), "alice", edited(gone, func(m *domain.Member) { m.User = "alice" })},
		{"a user outside the fact is left alone", memberFact(domain.MemberOpRemove, domain.MemberChange{User: "carol", Prev: domain.RoleMember}), bob, "bob", bob},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.fact.Applied(c.cur, c.user); got != c.want {
				t.Fatalf("Applied(%s) = %+v, want %+v", c.user, got, c.want)
			}
		})
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./pkg/keys/... ./apps/core/internal/domain/..."`
Expected: FAIL biên dịch: `undefined: MemberAction`, `undefined: ParseMemberAction`, `undefined: MemberActionLen`, `undefined: UserRoom`, `undefined: UserRoomPrefix`, `undefined: MaxUserRoomLen`, `undefined: ParseUserRoom` (keys); `undefined: domain.ErrDirectRoom`, `undefined: domain.MemberOp`, `undefined: domain.RoleAdmin`, `undefined: domain.ParseRole`, `undefined: domain.MemberAction`, `undefined: domain.ReadPos`, `unknown field MV in struct literal of type domain.Member`, `unknown field Read in struct literal of type domain.Member` (domain; Go dừng sau 10 lỗi với `too many errors`, nên danh sách thật có thể chỉ là một phần của danh sách này).

**Step 3: Code**

`pkg/keys/member.go`:

```go
package keys

import (
	"bytes"
	"slices"
)

const (
	MemberActionLen = 16
	MaxUserRoomLen  = 32 + 1 + 64 + 1 + 8

	minUserRoomLen = 1 + 1 + 1 + 1 + 8
)

func MemberAction(room, mv uint64) []byte {
	b := make([]byte, MemberActionLen)
	be.PutUint64(b[0:8], room)
	be.PutUint64(b[8:16], mv)
	return b
}

func ParseMemberAction(b []byte) (room, mv uint64, err error) {
	if len(b) != MemberActionLen {
		return 0, 0, ErrLength
	}
	return be.Uint64(b[0:8]), be.Uint64(b[8:16]), nil
}

func UserRoom(tenant, user string, room uint64) []byte {
	return be.AppendUint64(UserRoomPrefix(tenant, user), room)
}

func UserRoomPrefix(tenant, user string) []byte {
	b := make([]byte, 0, len(tenant)+len(user)+2+8)
	b = append(b, tenant...)
	b = append(b, 0)
	b = append(b, user...)
	return append(b, 0)
}

func ParseUserRoom(b []byte) (tenant, user string, room uint64, err error) {
	if len(b) < minUserRoomLen || len(b) > MaxUserRoomLen || b[len(b)-9] != 0 {
		return "", "", 0, ErrLength
	}
	t, u, ok := bytes.Cut(b[:len(b)-9], []byte{0})
	if !ok || len(t) == 0 || len(u) == 0 || slices.Contains(u, 0) {
		return "", "", 0, ErrLength
	}
	return string(t), string(u), be.Uint64(b[len(b)-8:]), nil
}
```

`apps/core/internal/domain/room.go`:
- thay khối role:

```go
const (
	RoleOwner  Role = "owner"
	RoleMember Role = "member"
)
```

bằng:

```go
const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)
```

- trong `type Room struct` thay:

```go
	LastChangeAt time.Time
}
```

bằng:

```go
	LastChangeAt  time.Time
	MemberVersion uint64
}
```

(gofmt căn lại cột tên field của cả struct.)

- trong `type Member struct` thay:

```go
	ClearedBeforeSeq uint64
}
```

bằng:

```go
	ClearedBeforeSeq uint64
	Removed          bool
	MV               uint64
	Read             ReadPos
}
```

`apps/core/internal/domain/errors.go`, thay:

```go
	ErrEmojiNotAllowed = fmt.Errorf("emoji not allowed: %w", apperr.ErrInvalidArgument)
)
```

bằng:

```go
	ErrEmojiNotAllowed = fmt.Errorf("emoji not allowed: %w", apperr.ErrInvalidArgument)
	ErrDirectRoom      = fmt.Errorf("direct room members are fixed: %w", apperr.ErrFailedPrecondition)
	ErrLastOwner       = fmt.Errorf("last owner cannot step down: %w", apperr.ErrFailedPrecondition)
	ErrMemberNotFound  = fmt.Errorf("member %w", apperr.ErrNotFound)
	ErrTooManyMembers  = fmt.Errorf("too many members in one request: %w", apperr.ErrInvalidArgument)
)
```

`apps/core/internal/domain/member.go`:

```go
package domain

import (
	"cmp"
	"slices"
	"time"
)

const MaxMemberChanges = 1000

type ReadPos struct{ Seq, Version uint64 }

type MemberOp uint8

const (
	MemberOpAdd MemberOp = iota + 1
	MemberOpRemove
	MemberOpLeave
	MemberOpRole
)

type MemberChange struct {
	User       string
	Role, Prev Role
	ReadSeq    uint64
}

type MemberAction struct {
	Room, MV  uint64
	Tenant    string
	Op        MemberOp
	Changes   []MemberChange
	By        string
	At        time.Time
	Count     int
	Successor string
}

type ReadUpdate struct {
	Room    uint64
	Tenant  string
	Type    RoomType
	Members int
	User    string
	Pos     ReadPos
	At      time.Time
}

func ParseRole(s string) (Role, error) {
	switch r := Role(s); r {
	case RoleOwner, RoleAdmin, RoleMember:
		return r, nil
	default:
		return "", invalid("role")
	}
}

func InitialMembers(r Room, members []Member) MemberAction {
	changes := make([]MemberChange, len(members))
	for i, m := range members {
		changes[i] = MemberChange{User: m.User, Role: m.Role}
	}
	return MemberAction{
		Room: r.ID, MV: 1, Tenant: r.Tenant, Op: MemberOpAdd, Changes: changes,
		By: r.CreatedBy, At: r.CreatedAt, Count: len(members),
	}
}

func Successor(candidates []Member) (Member, bool) {
	var best Member
	found := false
	for _, m := range candidates {
		if m.Removed || m.Role == RoleOwner {
			continue
		}
		if !found || succeeds(m, best) {
			best, found = m, true
		}
	}
	return best, found
}

func succeeds(a, b Member) bool {
	return cmp.Or(
		cmp.Compare(successionRank(a.Role), successionRank(b.Role)),
		a.JoinedAt.Compare(b.JoinedAt),
		cmp.Compare(a.User, b.User),
	) < 0
}

func successionRank(r Role) int {
	if r == RoleAdmin {
		return 0
	}
	return 1
}

func (a MemberAction) Applied(cur Member, user string) Member {
	i := slices.IndexFunc(a.Changes, func(c MemberChange) bool { return c.User == user })
	if i < 0 && user != a.Successor {
		return cur
	}
	next := cur
	next.Room, next.Tenant, next.User, next.MV = a.Room, a.Tenant, user, a.MV
	if i >= 0 {
		next = a.applyChange(next, a.Changes[i])
	}
	if user == a.Successor {
		next.Role = RoleOwner
	}
	return next
}

func (a MemberAction) applyChange(m Member, c MemberChange) Member {
	switch a.Op {
	case MemberOpAdd:
		m.Removed, m.Role, m.JoinedAt = false, c.Role, a.At
		m.Read = ReadPos{Seq: max(m.Read.Seq, c.ReadSeq), Version: m.Read.Version + 1}
	case MemberOpRemove, MemberOpLeave:
		m.Removed = true
	case MemberOpRole:
		m.Role = c.Role
	}
	return m
}
```

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./pkg/keys/... ./apps/core/internal/domain/..."`
Expected: PASS.

Run: `make vet`
Expected: sạch. Mọi struct literal của `domain.Room`/`domain.Member` trong repo đều có tên field, nên field mới ở cuối không làm vỡ biên dịch; `Room`/`Member` vẫn so sánh được (`room != wantRoom` ở `room_test.go`, `slices.Equal(members, …)` vẫn đúng). Có lỗi biên dịch ở đâu khác → dừng, báo cáo.

`wc -l pkg/keys/member.go apps/core/internal/domain/member.go apps/core/internal/domain/room.go apps/core/internal/domain/errors.go` lần lượt 50, 125, 53, 34; mọi file test mới < 200.

**Step 5: Commit**

INDEXES.csv:
- dòng `apps/core/internal/domain`: trong cột purpose thay `Member.ClearedBeforeSeq;` bằng `Member.ClearedBeforeSeq; roles owner/admin/member (ParseRole); Room.MemberVersion; Member.Removed (tombstone)/MV/Read (ReadPos seq + version); MemberAction fact (MemberOpAdd/Remove/Leave/Role stored as 1..4; MemberChange user/role/prev/read seq; by, at, count after the fact, successor of the last owner) with Applied (pure projection of one user shared by every adapter: add revives with the role, join time = fact time, read seq raised to max and read version + 1, clear kept; remove/leave tombstone; role sets the role; successor becomes owner; MV = fact MV), InitialMembers (fact 1 of a new room), Successor (earliest admin, else earliest member, ties by user; never owners or removed), ReadUpdate; MaxMemberChanges; ErrDirectRoom/ErrLastOwner (failed precondition), ErrMemberNotFound (not found), ErrTooManyMembers (invalid argument);`; trong cột key_symbols thay `FoldPins;ErrTooManyPins;ErrEmojiNotAllowed` bằng `FoldPins;ErrTooManyPins;ErrEmojiNotAllowed;RoleAdmin;ParseRole;ReadPos;MemberOp;MemberOpAdd;MemberOpRemove;MemberOpLeave;MemberOpRole;MemberChange;MemberAction;MemberAction.Applied;InitialMembers;Successor;ReadUpdate;MaxMemberChanges;ErrDirectRoom;ErrLastOwner;ErrMemberNotFound;ErrTooManyMembers`; cột decisions thay `D7;D35;D62;D63;D87;D88;D89;D92;D95` bằng `D7;D35;D62;D63;D87;D88;D89;D92;D95;D96;D97;D98;D100;D104`.
- dòng `pkg/keys`: trong cột purpose thay `Pin = room|pv (16B);` bằng `Pin = room|pv (16B); MemberAction = room|mv (16B); UserRoom = tenant 0x00 user 0x00 room(8) (idents never hold 0x00, so one user's prefix never matches another user's keys and a user's rooms sort by id);`; trong cột key_symbols thay `PinLen;MaxReactionUser;` bằng `PinLen;MaxReactionUser;MemberAction;ParseMemberAction;MemberActionLen;UserRoom;UserRoomPrefix;ParseUserRoom;MaxUserRoomLen;`; cột decisions thay `D10;D88;D92` bằng `D10;D88;D92;D96;D101`.

```bash
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
make fmt-check && make vet && make lint
git add pkg/keys/member.go pkg/keys/member_test.go apps/core/internal/domain/member.go apps/core/internal/domain/member_test.go apps/core/internal/domain/member_applied_test.go
git commit -m "feat(domain): add member facts, roles and read positions with their keys" -- pkg/keys/ apps/core/internal/domain/ INDEXES.csv
```

Expected: CSV in `{7}`; `git show --stat HEAD` chỉ có 2 file `pkg/keys`, 7 file `domain` và `INDEXES.csv`.

---

### Task 3: ★ Proto (`members.proto`, 6 RPC, `recipient = 10`, oneof 28–31) + pbconv (id, event member/read) + publish (subject user, RePublish mở rộng) + itest RePublish

Proto thêm file `members.proto` (role, lý do rời, `RoomMember`, request/response của 6 RPC), 6 RPC vào `CoreService`, field envelope `string recipient = 10` và bốn payload `member_added = 28`, `member_removed = 29`, `member_role_changed = 30`, `read_updated = 31` (số tiếp theo còn trống: `Event` đang dùng 1–9 với 7 reserved, oneof tới 27). `pbconv` dựng id, role và danh sách event của một fact member (bản room + bản user, D102), event `read_updated` (D105). Publisher định tuyến event có `recipient` sang `{root}.{t}.user.{u}.{kind}`; luật RePublish của stream mở rộng thành `{root}.*.*.*.*` → `{live}.{1}.{2}.{3}.evt.{4}`, nên subject room ra **đúng như trước** (`live.{t}.room.{rid}.evt.{kind}`) và subject user ra `live.{t}.user.{u}.evt.{kind}`. Không ack mark cho bốn loại mới (`markKey` giữ nguyên, chỉ `msg_created`).

Không cần sửa caller: `grpcsrv.Service` nhúng `chatimv1.UnimplementedCoreServiceServer` (6 RPC trả `Unimplemented` tới Task 14), `tools/internal/route/fakes_test.go` `fakeCore` nhúng `chatimv1.CoreServiceClient`, `tools/corecli/internal/e2e.EventOf` trả `ok=false` cho payload lạ.

**Id không trùng nhau** (bảng ở "Ghi chú tích hợp"): token thứ hai sau `{room}-` phân biệt loại. Bản room của member là `{room}-m{mv}` (không có `-` sau mv), bản user là `{room}-m{mv}-{u}`, đã đọc là `{room}-rd-{u}-v{v}`. Tin, sửa, reaction, số đếm có token thứ hai là số; ghim `p\d`, room `created`. User `[A-Za-z0-9_-]{1,64}` có thể là `m1`, `rd`, `v1`, `a-b` mà không làm hai loại trùng nhau; test bảng ở Step 2 chốt điều đó.

**RePublish trên stream đã có (rủi ro chính của task):** `EnsureStream` gọi `CreateOrUpdateStream`, nên core mới sửa luật của stream cũ. Itest Step 7 đặt luật M2b.3 bằng `UpdateStream`, gọi `EnsureStream`, rồi đọc lại cấu hình và gửi một fact member: bản room phải tới `live.acme.room.101.evt.member_added`, bản user tới `live.acme.user.bob.evt.member_added`. NATS 2.15 từ chối sửa RePublish (lỗi từ `UpdateStream` hoặc `EnsureStream` nhắc tới republish) → **dừng, báo controller** kèm lỗi nguyên văn; không đổi test cho qua (dev khi đó sẽ cần `make infra-reset`, ghi vào D102 ở Task 19).

**Files:**
- Create: `proto/chatim/v1/members.proto`
- Modify: `proto/chatim/v1/core.proto`, `proto/chatim/v1/events.proto`
- Regenerate: `pkg/pb/chatim/v1/core.pb.go`, `core_grpc.pb.go`, `events.pb.go`; tạo mới `pkg/pb/chatim/v1/members.pb.go` (`make proto`)
- Create: `apps/core/internal/pbconv/member.go`, `apps/core/internal/pbconv/read.go`
- Create: `apps/core/internal/pbconv/member_test.go`, `apps/core/internal/pbconv/read_test.go`, `apps/core/internal/pbconv/member_event_id_test.go`
- Modify: `apps/core/internal/publish/message.go`, `apps/core/internal/publish/stream.go`, `apps/core/internal/publish/stream_test.go`
- Create: `apps/core/internal/publish/member_read_event_test.go`, `apps/core/internal/publish/nats_republish_integration_test.go`
- Modify: `INDEXES.csv` (dòng mới `proto/chatim/v1/members.proto`; dòng `proto/chatim/v1/core.proto`, `proto/chatim/v1/events.proto`, `pkg/pb/chatim/v1`, `apps/core/internal/pbconv`, `apps/core/internal/publish`)

**Step 1: Proto**

`proto/chatim/v1/members.proto`:

```proto
syntax = "proto3";

package chatim.v1;

enum MemberRole {
  MEMBER_ROLE_UNSPECIFIED = 0;
  MEMBER_ROLE_OWNER = 1;
  MEMBER_ROLE_ADMIN = 2;
  MEMBER_ROLE_MEMBER = 3;
}

enum MemberRemovedReason {
  MEMBER_REMOVED_REASON_UNSPECIFIED = 0;
  MEMBER_REMOVED_REASON_REMOVED = 1;
  MEMBER_REMOVED_REASON_LEFT = 2;
}

message RoomMember {
  string user = 1;
  MemberRole role = 2;
}

message AddMembersRequest {
  string room_id = 1;
  repeated string users = 2;
}

message AddMembersResponse {
  uint64 member_version = 1;
  int32 member_count = 2;
  repeated string added = 3;
}

message RemoveMemberRequest {
  string room_id = 1;
  string user = 2;
}

message RemoveMemberResponse {
  uint64 member_version = 1;
  int32 member_count = 2;
  bool changed = 3;
}

message LeaveRoomRequest {
  string room_id = 1;
}

message LeaveRoomResponse {
  uint64 member_version = 1;
  int32 member_count = 2;
  bool changed = 3;
  string new_owner = 4;
}

message ChangeMemberRoleRequest {
  string room_id = 1;
  string user = 2;
  MemberRole role = 3;
}

message ChangeMemberRoleResponse {
  uint64 member_version = 1;
  int32 member_count = 2;
  bool changed = 3;
  MemberRole previous_role = 4;
}

message MarkReadRequest {
  string room_id = 1;
  uint64 seq = 2;
}

message MarkReadResponse {
  uint64 read_seq = 1;
  uint64 read_version = 2;
}

message MarkUnreadRequest {
  string room_id = 1;
  uint64 seq = 2;
}

message MarkUnreadResponse {
  uint64 read_seq = 1;
  uint64 read_version = 2;
}
```

`proto/chatim/v1/core.proto`:
- thay `import "chatim/v1/reactions_pins.proto";` bằng:

```proto
import "chatim/v1/members.proto";
import "chatim/v1/reactions_pins.proto";
```

- thay:

```proto
  rpc GetReactionSettings(GetReactionSettingsRequest) returns (GetReactionSettingsResponse);
}
```

bằng:

```proto
  rpc GetReactionSettings(GetReactionSettingsRequest) returns (GetReactionSettingsResponse);
  rpc AddMembers(AddMembersRequest) returns (AddMembersResponse);
  rpc RemoveMember(RemoveMemberRequest) returns (RemoveMemberResponse);
  rpc LeaveRoom(LeaveRoomRequest) returns (LeaveRoomResponse);
  rpc ChangeMemberRole(ChangeMemberRoleRequest) returns (ChangeMemberRoleResponse);
  rpc MarkRead(MarkReadRequest) returns (MarkReadResponse);
  rpc MarkUnread(MarkUnreadRequest) returns (MarkUnreadResponse);
}
```

`proto/chatim/v1/events.proto`:
- thay `import "chatim/v1/core.proto";` bằng:

```proto
import "chatim/v1/core.proto";
import "chatim/v1/members.proto";
```

- thay `  google.protobuf.Timestamp ts = 9;` bằng:

```proto
  google.protobuf.Timestamp ts = 9;
  string recipient = 10;
```

- thay `    MessageUnpinned message_unpinned = 27;` bằng:

```proto
    MessageUnpinned message_unpinned = 27;
    MemberAdded member_added = 28;
    MemberRemoved member_removed = 29;
    MemberRoleChanged member_role_changed = 30;
    ReadUpdated read_updated = 31;
```

- thêm vào cuối file (sau `message MessageUnpinned { … }`, cách một dòng trống):

```proto

message MemberAdded {
  repeated RoomMember members = 1;
  uint64 member_version = 2;
  int32 member_count = 3;
}

message MemberRemoved {
  string user = 1;
  MemberRemovedReason reason = 2;
  string new_owner = 3;
  uint64 member_version = 4;
  int32 member_count = 5;
}

message MemberRoleChanged {
  string user = 1;
  MemberRole role = 2;
  MemberRole previous_role = 3;
  uint64 member_version = 4;
}

message ReadUpdated {
  string user = 1;
  uint64 seq = 2;
  uint64 version = 3;
}
```

Run: `make proto && make buf-lint`
Expected: không lỗi (`buf format -d --exit-code` không in diff); `git status --short pkg/pb` có `M` ở `core.pb.go`, `core_grpc.pb.go`, `events.pb.go` và `??` ở `members.pb.go`. Kiểm: `grep -l "coreServiceClient) MarkUnread" pkg/pb/chatim/v1/core_grpc.pb.go`, `grep -l "type Event_MemberAdded struct" pkg/pb/chatim/v1/events.pb.go`, `grep -l "func (x \*Event) GetRecipient" pkg/pb/chatim/v1/events.pb.go`, `grep -l "MemberRole_MEMBER_ROLE_ADMIN" pkg/pb/chatim/v1/members.pb.go` đều in tên file. `wc -l proto/chatim/v1/core.proto` = 178 (từ 171).

Run: `make vet`
Expected: sạch. Lỗi biên dịch ở đâu đó → dừng, báo cáo.

**Step 2: Test pbconv + publish**

`apps/core/internal/pbconv/member_event_id_test.go`:

```go
package pbconv_test

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
)

func TestMemberAndReadEventIDs(t *testing.T) {
	cases := []struct{ name, got, want string }{
		{"member room copy", pbconv.MemberEventID(42, 3), "42-m3"},
		{"member user copy", pbconv.MemberUserEventID(42, 3, "bob"), "42-m3-bob"},
		{"read", pbconv.ReadEventID(42, "bob", 7), "42-rd-bob-v7"},
		{
			"widest member user copy",
			pbconv.MemberUserEventID(math.MaxInt64, math.MaxInt64, strings.Repeat("u", 64)),
			"9223372036854775807-m9223372036854775807-" + strings.Repeat("u", 64),
		},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestMemberAndReadEventIDsNeverCollideWithOtherKinds(t *testing.T) {
	users := []string{"m1", "rd", "v1", "a-b", "1", "created", "p1", "m1-v1", "rd-bob-v1", "reactions"}
	seen := map[string]string{}
	add := func(id, what string) {
		t.Helper()
		if prev, dup := seen[id]; dup {
			t.Fatalf("%s and %s share the id %q", prev, what, id)
		}
		seen[id] = what
	}
	for _, room := range []uint64{1, 42} {
		add(pbconv.RoomCreatedEventID(room), fmt.Sprintf("room %d created", room))
		for _, n := range []uint64{1, 2, 12} {
			at := fmt.Sprintf("%d/%d", room, n)
			add(pbconv.PinEventID(room, n), "pin "+at)
			add(pbconv.MemberEventID(room, n), "member room copy "+at)
			add(pbconv.MessageEventID(room, 0, n), "message "+at)
			add(pbconv.MessageChangeEventID(room, 0, n, 1), "change "+at)
			add(pbconv.ReactionCountsEventID(room, 0, n, 1), "counts "+at)
			for _, u := range users {
				add(pbconv.MemberUserEventID(room, n, u), "member user copy "+at+" "+u)
				add(pbconv.ReadEventID(room, u, n), "read "+at+" "+u)
				add(pbconv.ReactionEventID(room, 0, n, u, 1), "reaction "+at+" "+u)
			}
		}
	}
}
```

`apps/core/internal/pbconv/member_test.go`:

```go
package pbconv_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var memberAt = time.Date(2026, 10, 6, 9, 30, 0, 0, time.UTC)

const memberRoom = "9007199254740993"

func memberFact(op domain.MemberOp, changes ...domain.MemberChange) domain.MemberAction {
	return domain.MemberAction{Room: 9_007_199_254_740_993, MV: 4, Tenant: "acme", Op: op, Changes: changes, By: "alice", At: memberAt, Count: 3}
}

func memberEnvelope(id, recipient string) *chatimv1.Event {
	return &chatimv1.Event{
		Id: id, Tenant: "acme", RoomId: memberRoom, RoomType: chatimv1.RoomType_ROOM_TYPE_GROUP,
		Actor: "alice", Ts: timestamppb.New(memberAt), Recipient: recipient,
	}
}

func assertEventList(t *testing.T, got, want []*chatimv1.Event) {
	t.Helper()
	if !slices.EqualFunc(got, want, func(a, b *chatimv1.Event) bool { return proto.Equal(a, b) }) {
		t.Fatalf("events = %v,\nwant %v", got, want)
	}
}

func TestMemberAddedHasARoomCopyAndOneCopyPerAddedUser(t *testing.T) {
	bob := &chatimv1.RoomMember{User: "bob", Role: chatimv1.MemberRole_MEMBER_ROLE_MEMBER}
	carol := &chatimv1.RoomMember{User: "carol", Role: chatimv1.MemberRole_MEMBER_ROLE_ADMIN}
	added := func(id, recipient string, members ...*chatimv1.RoomMember) *chatimv1.Event {
		ev := memberEnvelope(id, recipient)
		ev.Payload = &chatimv1.Event_MemberAdded{MemberAdded: &chatimv1.MemberAdded{Members: members, MemberVersion: 4, MemberCount: 3}}
		return ev
	}
	fact := memberFact(domain.MemberOpAdd,
		domain.MemberChange{User: "bob", Role: domain.RoleMember, ReadSeq: 9},
		domain.MemberChange{User: "carol", Role: domain.RoleAdmin},
	)
	assertEventList(t, pbconv.MemberEvents(domain.RoomGroup, fact), []*chatimv1.Event{
		added(memberRoom+"-m4", "", bob, carol),
		added(memberRoom+"-m4-bob", "bob", bob),
		added(memberRoom+"-m4-carol", "carol", carol),
	})
}

func TestMemberRemovedReachesTheRoomTheUserAndTheNewOwner(t *testing.T) {
	removed := func(id, recipient, user string, reason chatimv1.MemberRemovedReason, newOwner string) *chatimv1.Event {
		ev := memberEnvelope(id, recipient)
		ev.Payload = &chatimv1.Event_MemberRemoved{MemberRemoved: &chatimv1.MemberRemoved{
			User: user, Reason: reason, NewOwner: newOwner, MemberVersion: 4, MemberCount: 3,
		}}
		return ev
	}
	byOwner := chatimv1.MemberRemovedReason_MEMBER_REMOVED_REASON_REMOVED
	left := chatimv1.MemberRemovedReason_MEMBER_REMOVED_REASON_LEFT
	remove := memberFact(domain.MemberOpRemove, domain.MemberChange{User: "bob", Prev: domain.RoleMember})
	assertEventList(t, pbconv.MemberEvents(domain.RoomGroup, remove), []*chatimv1.Event{
		removed(memberRoom+"-m4", "", "bob", byOwner, ""),
		removed(memberRoom+"-m4-bob", "bob", "bob", byOwner, ""),
	})
	leave := memberFact(domain.MemberOpLeave, domain.MemberChange{User: "alice", Prev: domain.RoleOwner})
	leave.Successor = "dave"
	assertEventList(t, pbconv.MemberEvents(domain.RoomGroup, leave), []*chatimv1.Event{
		removed(memberRoom+"-m4", "", "alice", left, "dave"),
		removed(memberRoom+"-m4-alice", "alice", "alice", left, "dave"),
		removed(memberRoom+"-m4-dave", "dave", "alice", left, "dave"),
	})
}

func TestMemberRoleChangedCarriesBothRoles(t *testing.T) {
	changed := func(id, recipient string) *chatimv1.Event {
		ev := memberEnvelope(id, recipient)
		ev.Payload = &chatimv1.Event_MemberRoleChanged{MemberRoleChanged: &chatimv1.MemberRoleChanged{
			User: "bob", Role: chatimv1.MemberRole_MEMBER_ROLE_ADMIN, PreviousRole: chatimv1.MemberRole_MEMBER_ROLE_MEMBER, MemberVersion: 4,
		}}
		return ev
	}
	fact := memberFact(domain.MemberOpRole, domain.MemberChange{User: "bob", Role: domain.RoleAdmin, Prev: domain.RoleMember})
	assertEventList(t, pbconv.MemberEvents(domain.RoomGroup, fact), []*chatimv1.Event{
		changed(memberRoom+"-m4", ""),
		changed(memberRoom+"-m4-bob", "bob"),
	})
}

func TestMemberRolesMapBothWays(t *testing.T) {
	for _, r := range []domain.Role{domain.RoleOwner, domain.RoleAdmin, domain.RoleMember} {
		back, err := pbconv.DomainMemberRole(pbconv.MemberRole(r))
		if err != nil || back != r {
			t.Fatalf("DomainMemberRole(MemberRole(%q)) = %q, %v", r, back, err)
		}
	}
	if got := pbconv.MemberRole("guest"); got != chatimv1.MemberRole_MEMBER_ROLE_UNSPECIFIED {
		t.Fatalf("MemberRole(guest) = %v, want unspecified", got)
	}
	for _, r := range []chatimv1.MemberRole{chatimv1.MemberRole_MEMBER_ROLE_UNSPECIFIED, chatimv1.MemberRole(9)} {
		if _, err := pbconv.DomainMemberRole(r); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("DomainMemberRole(%v) = %v, want ErrInvalidArgument", r, err)
		}
	}
}
```

`apps/core/internal/pbconv/read_test.go`:

```go
package pbconv_test

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestReadUpdatedGoesToTheRoomOrOnlyToTheReader(t *testing.T) {
	u := domain.ReadUpdate{Room: 42, Tenant: "acme", Type: domain.RoomDM, Members: 2, User: "bob", Pos: domain.ReadPos{Seq: 7, Version: 3}, At: memberAt}
	want := &chatimv1.Event{
		Id: "42-rd-bob-v3", Tenant: "acme", RoomId: "42", RoomType: chatimv1.RoomType_ROOM_TYPE_DM, Actor: "bob", Ts: timestamppb.New(memberAt),
		Payload: &chatimv1.Event_ReadUpdated{ReadUpdated: &chatimv1.ReadUpdated{User: "bob", Seq: 7, Version: 3}},
	}
	if got := pbconv.ReadUpdated(u, true); !proto.Equal(got, want) {
		t.Fatalf("ReadUpdated(room wide) = %v, want %v", got, want)
	}
	want.Recipient = "bob"
	if got := pbconv.ReadUpdated(u, false); !proto.Equal(got, want) {
		t.Fatalf("ReadUpdated(reader only) = %v, want %v", got, want)
	}
}
```

`apps/core/internal/publish/stream_test.go`, trong `TestEnsureStreamConfiguresDedupeAndRepublish` thay:

```go
	case c.RePublish == nil || c.RePublish.Source != "evt.*.room.*.*" ||
		c.RePublish.Destination != "live.{{wildcard(1)}}.room.{{wildcard(2)}}.evt.{{wildcard(3)}}":
```

bằng:

```go
	case c.RePublish == nil || c.RePublish.Source != "evt.*.*.*.*" ||
		c.RePublish.Destination != "live.{{wildcard(1)}}.{{wildcard(2)}}.{{wildcard(3)}}.evt.{{wildcard(4)}}":
```

`apps/core/internal/publish/member_read_event_test.go` (`roomA`, `tenant`, `sentAt` có sẵn ở `harness_test.go`):

```go
package publish_test

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func memberEvents(op domain.MemberOp, c domain.MemberChange) []*chatimv1.Event {
	a := domain.MemberAction{Room: roomA, MV: 2, Tenant: tenant, Op: op, Changes: []domain.MemberChange{c}, By: "alice", At: sentAt, Count: 2}
	return pbconv.MemberEvents(domain.RoomGroup, a)
}

func TestMemberAndReadEventsGoToTheRoomOrTheUserSubjectWithoutAMark(t *testing.T) {
	added := memberEvents(domain.MemberOpAdd, domain.MemberChange{User: "bob", Role: domain.RoleMember})
	removed := memberEvents(domain.MemberOpRemove, domain.MemberChange{User: "bob", Prev: domain.RoleMember})
	changed := memberEvents(domain.MemberOpRole, domain.MemberChange{User: "bob", Role: domain.RoleAdmin, Prev: domain.RoleMember})
	read := domain.ReadUpdate{Room: roomA, Tenant: tenant, Type: domain.RoomGroup, Members: 2, User: "bob", Pos: domain.ReadPos{Seq: 7, Version: 3}, At: sentAt}
	cases := map[string]struct {
		ev          *chatimv1.Event
		subject, id string
	}{
		"added, room copy":        {added[0], "evt.acme.room.101.member_added", "101-m2"},
		"added, user copy":        {added[1], "evt.acme.user.bob.member_added", "101-m2-bob"},
		"removed, room copy":      {removed[0], "evt.acme.room.101.member_removed", "101-m2"},
		"removed, user copy":      {removed[1], "evt.acme.user.bob.member_removed", "101-m2-bob"},
		"role changed, user copy": {changed[1], "evt.acme.user.bob.member_role_changed", "101-m2-bob"},
		"read, room wide":         {pbconv.ReadUpdated(read, true), "evt.acme.room.101.read_updated", "101-rd-bob-v3"},
		"read, reader only":       {pbconv.ReadUpdated(read, false), "evt.acme.user.bob.read_updated", "101-rd-bob-v3"},
	}
	for name, c := range cases {
		msg, err := publish.Message("evt", roomA, c.ev)
		if err != nil {
			t.Fatalf("%s: Message: %v", name, err)
		}
		if msg.Subject != c.subject || publishtest.MsgID(msg) != c.id {
			t.Fatalf("%s: subject %q msg id %q, want %s and %s", name, msg.Subject, publishtest.MsgID(msg), c.subject, c.id)
		}
		if key, ok := publish.MarkKey(roomA, c.ev); ok {
			t.Fatalf("%s: MarkKey = %v, true; want no mark", name, key)
		}
	}
}

func TestARecipientMustBeOneSubjectToken(t *testing.T) {
	read := domain.ReadUpdate{Room: roomA, Tenant: tenant, Type: domain.RoomGroup, User: "bob", Pos: domain.ReadPos{Seq: 1, Version: 1}, At: sentAt}
	for _, to := range []string{"b.b", "b*", "b>", "b b"} {
		ev := pbconv.ReadUpdated(read, false)
		ev.Recipient = to
		if msg, err := publish.Message("evt", roomA, ev); err == nil {
			t.Fatalf("Message(recipient %q) = %q, want an error", to, msg.Subject)
		}
	}
}
```

`apps/core/internal/publish/nats_republish_integration_test.go` (`realStream`, `fastSetup` có sẵn ở `nats_integration_test.go`/`harness_test.go`):

```go
package publish_test

import (
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestRealJetStreamUpdatesTheRePublishRuleAndRoutesUserCopies(t *testing.T) {
	it := realStream(t, fastSetup)
	s, err := it.js.Stream(t.Context(), it.cfg.Name)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	info, err := s.Info(t.Context())
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	legacy := info.Config
	legacy.RePublish = &jetstream.RePublish{
		Source:      it.cfg.SubjectRoot + ".*.room.*.*",
		Destination: it.cfg.LiveRoot + ".{{wildcard(1)}}.room.{{wildcard(2)}}.evt.{{wildcard(3)}}",
	}
	if _, err := it.js.UpdateStream(t.Context(), legacy); err != nil {
		t.Fatalf("put back the M2b.3 RePublish rule: %v", err)
	}
	if err := publish.EnsureStream(t.Context(), it.js, it.cfg); err != nil {
		t.Fatalf("EnsureStream over the M2b.3 rule: %v", err)
	}
	if info, err = s.Info(t.Context()); err != nil {
		t.Fatalf("stream info after EnsureStream: %v", err)
	}
	wantSource := it.cfg.SubjectRoot + ".*.*.*.*"
	wantDest := it.cfg.LiveRoot + ".{{wildcard(1)}}.{{wildcard(2)}}.{{wildcard(3)}}.evt.{{wildcard(4)}}"
	if rp := info.Config.RePublish; rp == nil || rp.Source != wantSource || rp.Destination != wantDest {
		t.Fatalf("RePublish after EnsureStream = %+v, want %s -> %s", rp, wantSource, wantDest)
	}
	subscribe := func(subject string) *nats.Subscription {
		sub, err := it.nc.SubscribeSync(subject)
		if err != nil {
			t.Fatalf("subscribe %s: %v", subject, err)
		}
		return sub
	}
	room := subscribe(it.cfg.LiveRoot + ".acme.room.101.evt.member_added")
	user := subscribe(it.cfg.LiveRoot + ".acme.user.bob.evt.member_added")
	if err := it.nc.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	fact := domain.MemberAction{
		Room: roomA, MV: 2, Tenant: tenant, Op: domain.MemberOpAdd, By: "alice", At: sentAt, Count: 2,
		Changes: []domain.MemberChange{{User: "bob", Role: domain.RoleMember}},
	}
	for _, ev := range pbconv.MemberEvents(domain.RoomGroup, fact) {
		msg, err := publish.Message(it.cfg.SubjectRoot, roomA, ev)
		if err != nil {
			t.Fatalf("Message(%s): %v", ev.GetId(), err)
		}
		if _, err := it.js.PublishMsg(t.Context(), msg); err != nil {
			t.Fatalf("publish %s on %s: %v", ev.GetId(), msg.Subject, err)
		}
	}
	for name, c := range map[string]struct {
		sub           *nats.Subscription
		id, recipient string
	}{
		"room copy": {room, "101-m2", ""},
		"user copy": {user, "101-m2-bob", "bob"},
	} {
		msg, err := c.sub.NextMsg(2 * time.Second)
		if err != nil {
			t.Fatalf("%s: live message: %v", name, err)
		}
		got := &chatimv1.Event{}
		if err := proto.Unmarshal(msg.Data, got); err != nil {
			t.Fatalf("%s: decode: %v", name, err)
		}
		if got.GetId() != c.id || got.GetRecipient() != c.recipient || msg.Header.Get(jetstream.MsgIDHeader) != c.id {
			t.Fatalf("%s: live event %s for %q (msg id %q), want %s for %q", name, got.GetId(), got.GetRecipient(), msg.Header.Get(jetstream.MsgIDHeader), c.id, c.recipient)
		}
	}
}
```

**Step 3: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/pbconv/... ./apps/core/internal/publish/..."`
Expected: FAIL biên dịch ở `pbconv`: `undefined: pbconv.MemberEventID`, `undefined: pbconv.MemberUserEventID`, `undefined: pbconv.ReadEventID`, `undefined: pbconv.MemberEvents`, `undefined: pbconv.MemberRole`, `undefined: pbconv.DomainMemberRole`, `undefined: pbconv.ReadUpdated`; `publish` fail biên dịch vì cùng các symbol của `pbconv`.

**Step 4: Code pbconv**

`apps/core/internal/pbconv/member.go`:

```go
package pbconv

import (
	"fmt"
	"strconv"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errUnknownRole = fmt.Errorf("%w: role", apperr.ErrInvalidArgument)

func MemberEventID(room, mv uint64) string { return RoomID(room) + "-m" + strconv.FormatUint(mv, 10) }

func MemberUserEventID(room, mv uint64, user string) string {
	return MemberEventID(room, mv) + "-" + user
}

func MemberRole(r domain.Role) chatimv1.MemberRole {
	switch r {
	case domain.RoleOwner:
		return chatimv1.MemberRole_MEMBER_ROLE_OWNER
	case domain.RoleAdmin:
		return chatimv1.MemberRole_MEMBER_ROLE_ADMIN
	case domain.RoleMember:
		return chatimv1.MemberRole_MEMBER_ROLE_MEMBER
	default:
		return chatimv1.MemberRole_MEMBER_ROLE_UNSPECIFIED
	}
}

func DomainMemberRole(r chatimv1.MemberRole) (domain.Role, error) {
	switch r {
	case chatimv1.MemberRole_MEMBER_ROLE_OWNER:
		return domain.RoleOwner, nil
	case chatimv1.MemberRole_MEMBER_ROLE_ADMIN:
		return domain.RoleAdmin, nil
	case chatimv1.MemberRole_MEMBER_ROLE_MEMBER:
		return domain.RoleMember, nil
	default:
		return "", errUnknownRole
	}
}

func MemberEvents(roomType domain.RoomType, a domain.MemberAction) []*chatimv1.Event {
	out := make([]*chatimv1.Event, 0, len(a.Changes)+2)
	out = append(out, memberEvent(roomType, a, "", a.Changes))
	for _, c := range a.Changes {
		out = append(out, memberEvent(roomType, a, c.User, []domain.MemberChange{c}))
	}
	if a.Successor != "" {
		out = append(out, memberEvent(roomType, a, a.Successor, a.Changes))
	}
	return out
}

func memberEvent(roomType domain.RoomType, a domain.MemberAction, recipient string, changes []domain.MemberChange) *chatimv1.Event {
	id := MemberEventID(a.Room, a.MV)
	if recipient != "" {
		id = MemberUserEventID(a.Room, a.MV, recipient)
	}
	ev := &chatimv1.Event{
		Id: id, Tenant: a.Tenant, RoomId: RoomID(a.Room), RoomType: RoomType(roomType),
		Actor: a.By, Ts: timestamppb.New(a.At), Recipient: recipient,
	}
	setMemberPayload(ev, a, changes)
	return ev
}

func setMemberPayload(ev *chatimv1.Event, a domain.MemberAction, changes []domain.MemberChange) {
	var c domain.MemberChange
	if len(changes) > 0 {
		c = changes[0]
	}
	switch a.Op {
	case domain.MemberOpAdd:
		ev.Payload = &chatimv1.Event_MemberAdded{MemberAdded: &chatimv1.MemberAdded{
			Members: roomMembers(changes), MemberVersion: a.MV, MemberCount: memberCount(a.Count),
		}}
	case domain.MemberOpRemove, domain.MemberOpLeave:
		ev.Payload = &chatimv1.Event_MemberRemoved{MemberRemoved: &chatimv1.MemberRemoved{
			User: c.User, Reason: removedReason(a.Op), NewOwner: a.Successor, MemberVersion: a.MV, MemberCount: memberCount(a.Count),
		}}
	case domain.MemberOpRole:
		ev.Payload = &chatimv1.Event_MemberRoleChanged{MemberRoleChanged: &chatimv1.MemberRoleChanged{
			User: c.User, Role: MemberRole(c.Role), PreviousRole: MemberRole(c.Prev), MemberVersion: a.MV,
		}}
	}
}

func removedReason(op domain.MemberOp) chatimv1.MemberRemovedReason {
	if op == domain.MemberOpLeave {
		return chatimv1.MemberRemovedReason_MEMBER_REMOVED_REASON_LEFT
	}
	return chatimv1.MemberRemovedReason_MEMBER_REMOVED_REASON_REMOVED
}

func roomMembers(changes []domain.MemberChange) []*chatimv1.RoomMember {
	out := make([]*chatimv1.RoomMember, len(changes))
	for i, c := range changes {
		out[i] = &chatimv1.RoomMember{User: c.User, Role: MemberRole(c.Role)}
	}
	return out
}
```

Fact có `Op` ngoài 1..4 (không qua được `store.ValidateMemberAction`) cho event không payload; publisher bỏ nó như event hỏng (đếm drop, một dòng log).

`apps/core/internal/pbconv/read.go`:

```go
package pbconv

import (
	"strconv"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func ReadEventID(room uint64, user string, version uint64) string {
	return RoomID(room) + "-rd-" + user + "-v" + strconv.FormatUint(version, 10)
}

func ReadUpdated(u domain.ReadUpdate, roomWide bool) *chatimv1.Event {
	ev := &chatimv1.Event{
		Id:       ReadEventID(u.Room, u.User, u.Pos.Version),
		Tenant:   u.Tenant,
		RoomId:   RoomID(u.Room),
		RoomType: RoomType(u.Type),
		Actor:    u.User,
		Ts:       timestamppb.New(u.At),
		Payload:  &chatimv1.Event_ReadUpdated{ReadUpdated: &chatimv1.ReadUpdated{User: u.User, Seq: u.Pos.Seq, Version: u.Pos.Version}},
	}
	if !roomWide {
		ev.Recipient = u.User
	}
	return ev
}
```

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/pbconv/..."`
Expected: PASS.

**Step 5: Code publish**

`apps/core/internal/publish/stream.go`:
- thay:

```go
	msgPinned       = "msg_pinned"
	msgUnpinned     = "msg_unpinned"
)
```

bằng:

```go
	msgPinned         = "msg_pinned"
	msgUnpinned       = "msg_unpinned"
	memberAdded       = "member_added"
	memberRemoved     = "member_removed"
	memberRoleChanged = "member_role_changed"
	readUpdated       = "read_updated"
)
```

(gofmt căn lại các dòng const phía trên trong cùng khối.)

- trong `jetstream()` thay:

```go
		RePublish: &jetstream.RePublish{
			Source:      c.SubjectRoot + ".*.room.*.*",
			Destination: c.LiveRoot + ".{{wildcard(1)}}.room.{{wildcard(2)}}.evt.{{wildcard(3)}}",
		},
```

bằng:

```go
		RePublish: &jetstream.RePublish{
			Source:      c.SubjectRoot + ".*.*.*.*",
			Destination: c.LiveRoot + ".{{wildcard(1)}}.{{wildcard(2)}}.{{wildcard(3)}}.evt.{{wildcard(4)}}",
		},
```

- thêm vào cuối file:

```go

func userSubject(root, tenant, user, kind string) string {
	return root + "." + tenant + ".user." + user + "." + kind
}
```

`apps/core/internal/publish/message.go`:
- thay:

```go
var errMalformed = errors.New("event needs an id, a subject-safe tenant and a known payload")
```

bằng:

```go
var errMalformed = errors.New("event needs an id, a known payload and a subject-safe tenant and recipient")
```

- thay:

```go
	data, err := proto.Marshal(ev)
	if err != nil {
		return nil, fmt.Errorf("marshal event: %w", err)
	}
	m := &nats.Msg{Subject: roomSubject(root, ev.GetTenant(), room, kind), Data: data, Header: nats.Header{}}
```

bằng:

```go
	subject := roomSubject(root, ev.GetTenant(), room, kind)
	if to := ev.GetRecipient(); to != "" {
		if !validToken(to) {
			return nil, errMalformed
		}
		subject = userSubject(root, ev.GetTenant(), to, kind)
	}
	data, err := proto.Marshal(ev)
	if err != nil {
		return nil, fmt.Errorf("marshal event: %w", err)
	}
	m := &nats.Msg{Subject: subject, Data: data, Header: nats.Header{}}
```

- trong `eventKind` thay:

```go
	case *chatimv1.Event_MessageUnpinned:
		return msgUnpinned, true
```

bằng:

```go
	case *chatimv1.Event_MessageUnpinned:
		return msgUnpinned, true
	case *chatimv1.Event_MemberAdded:
		return memberAdded, true
	case *chatimv1.Event_MemberRemoved:
		return memberRemoved, true
	case *chatimv1.Event_MemberRoleChanged:
		return memberRoleChanged, true
	case *chatimv1.Event_ReadUpdated:
		return readUpdated, true
```

`ack_mark_policy.go` giữ nguyên: `markKey` chỉ có case `Event_MessageCreated`; bốn payload mới rơi vào `default` (không mark). Test Step 2 chốt điều đó.

**Step 6: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/pbconv/... ./apps/core/internal/publish/... ./apps/core/internal/grpcsrv/... ./apps/core/internal/effects/... ./tools/internal/route/... ./tools/corecli/..."`
Expected: PASS (nats integration skip). `TestEnsureStreamConfiguresDedupeAndRepublish` dùng luật mới; `TestMalformedEventsAreDroppedWithOneLogLineAndOthersPublished` vẫn xanh (event không recipient đi đường cũ). `wc -l apps/core/internal/pbconv/member.go apps/core/internal/pbconv/read.go apps/core/internal/publish/message.go apps/core/internal/publish/stream.go` lần lượt 107, 30, 66, 104.

**Step 7: Integration**

Run (tạo `<scratchpad>/itest-one.mk` như ở "Quy tắc chung" nếu chưa có): `make infra-up && make -f <scratchpad>/itest-one.mk itest-one RUN='TestRealJetStream' PKG=./apps/core/internal/publish/`
Expected: PASS gồm `TestRealJetStreamUpdatesTheRePublishRuleAndRoutesUserCopies` (luật M2b.3 đặt lại được bằng `UpdateStream`, `EnsureStream` đổi sang `.*.*.*.*`, bản room tới `live.…room.101.evt.member_added`, bản user tới `live.…user.bob.evt.member_added` với `recipient` `bob`) và `TestRealJetStreamDedupesByMsgIDAndRepublishesLive` (subject room giữ nguyên). NATS từ chối sửa RePublish → **dừng, báo controller** (xem đầu task).

Run: `make itest`
Expected: PASS toàn repo (itest của `apps/core` dựng stream tên ngẫu nhiên với luật mới; mọi subscription `live.{t}.room.{rid}.>` vẫn nhận event như cũ).

**Step 8: Commit**

INDEXES.csv:
- thêm dòng mới ngay sau dòng `proto/chatim/v1/reactions_pins.proto`:

```csv
proto/chatim/v1/members.proto,proto,"Member and read position messages (same package chatim.v1, imported by core.proto and events.proto): MemberRole (owner/admin/member), MemberRemovedReason (removed/left), RoomMember; AddMembers/RemoveMember/LeaveRoom/ChangeMemberRole requests and responses (member_version, member_count, changed, added, new_owner, previous_role); MarkRead/MarkUnread requests and responses (read_seq, read_version)",MemberRole;MemberRemovedReason;RoomMember;AddMembersRequest;AddMembersResponse;RemoveMemberRequest;RemoveMemberResponse;LeaveRoomRequest;LeaveRoomResponse;ChangeMemberRoleRequest;ChangeMemberRoleResponse;MarkReadRequest;MarkReadResponse;MarkUnreadRequest;MarkUnreadResponse,pkg/pb/chatim/v1,buf-lint,D99;D102;D104
```

- dòng `proto/chatim/v1/core.proto`: trong cột purpose thay `UnpinMessage/GetReactionSettings (reaction, pin and reaction settings messages in reactions_pins.proto);` bằng `UnpinMessage/GetReactionSettings/AddMembers/RemoveMember/LeaveRoom/ChangeMemberRole/MarkRead/MarkUnread (reaction, pin and reaction settings messages in reactions_pins.proto; member and read position messages in members.proto);`; cột decisions thay `D17;D48;D90;D92;D94;D95` bằng `D17;D48;D90;D92;D94;D95;D99;D104`.
- dòng `proto/chatim/v1/events.proto`: trong cột purpose thay `MessagePinned (26)/MessageUnpinned (27); additive changes only` bằng `MessagePinned (26)/MessageUnpinned (27)/MemberAdded (28)/MemberRemoved (29)/MemberRoleChanged (30)/ReadUpdated (31); recipient (10): empty = the room subject, a user id = that user's subject; additive changes only`; cột key_symbols thay `Event;MessageCreated;ReactionChanged;CountsChanged;MessagePinned;MessageUnpinned` bằng `Event;MessageCreated;ReactionChanged;CountsChanged;MessagePinned;MessageUnpinned;MemberAdded;MemberRemoved;MemberRoleChanged;ReadUpdated`; cột decisions thay `D17;D48;D93` bằng `D17;D48;D93;D102;D105`.
- dòng `pkg/pb/chatim/v1`: trong cột key_symbols thay `MessagePinned;MessageUnpinned;Message;` bằng `MessagePinned;MessageUnpinned;MemberAdded;MemberRemoved;MemberRoleChanged;ReadUpdated;MemberRole;RoomMember;Message;`.
- dòng `apps/core/internal/pbconv`: trong cột purpose thay `PinChanged picks by op and drops the text of a deleted target; Pins` bằng `PinChanged picks by op and drops the text of a deleted target; Pins; MemberEventID {room}-m{mv} (room copy), MemberUserEventID {room}-m{mv}-{user} (user copy), ReadEventID {room}-rd-{user}-v{version} (table test: no two kinds share an id); MemberRole/DomainMemberRole (unspecified -> ErrInvalidArgument); MemberEvents = the room copy, then one copy per changed user, then the successor (recipient set; member_added of a user copy carries only that user; member_removed with reason removed/left and new_owner; member_role_changed with both roles); ReadUpdated (actor = reader; recipient = reader unless room wide)`; trong cột key_symbols thay `PinChanged;Pins;` bằng `PinChanged;Pins;MemberEventID;MemberUserEventID;ReadEventID;MemberRole;DomainMemberRole;MemberEvents;ReadUpdated;`; cột decisions thay `D48;D83;D90;D93` bằng `D48;D83;D90;D93;D102;D105`.
- dòng `apps/core/internal/publish`: trong cột purpose thay `EnsureStream with RePublish to live.* and a 5m duplicate window;` bằng `EnsureStream with RePublish {root}.*.*.*.* -> {live}.{1}.{2}.{3}.evt.{4} (room and user subjects; rewrites the rule of an existing stream) and a 5m duplicate window;` và thay `publishes msg_created/room_created/msg_edited/msg_deleted/reaction_changed/counts_changed/msg_pinned/msg_unpinned;` bằng `publishes msg_created/room_created/msg_edited/msg_deleted/reaction_changed/counts_changed/msg_pinned/msg_unpinned/member_added/member_removed/member_role_changed/read_updated; an event with a recipient goes to {root}.{tenant}.user.{recipient}.{kind} (recipient must be one subject token), else to the room subject;`; trong cột tests thay `async err handler on real NATS)` bằng `async err handler; RePublish rule rewritten on an existing stream and user copies live on real NATS)`; cột decisions thay `D65;D76;D83;D93` bằng `D65;D76;D83;D93;D102;D105`.

```bash
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
make fmt-check && make vet && make lint && make buf-lint
git add proto/chatim/v1/members.proto pkg/pb/chatim/v1/members.pb.go apps/core/internal/pbconv/member.go apps/core/internal/pbconv/read.go apps/core/internal/pbconv/member_test.go apps/core/internal/pbconv/read_test.go apps/core/internal/pbconv/member_event_id_test.go apps/core/internal/publish/member_read_event_test.go apps/core/internal/publish/nats_republish_integration_test.go
git commit -m "feat(proto): add member and read events with user subjects" -- proto/chatim/v1/ pkg/pb/chatim/v1/ apps/core/internal/pbconv/ apps/core/internal/publish/ INDEXES.csv
```

Expected: CSV in `{7}`; `git show --stat HEAD` chỉ có file proto, `pkg/pb`, `pbconv`, `publish` và `INDEXES.csv`.

---

### Task 4: Store port + `MemberInserted`/`Change.Member` + `RecordOf` + memstore (fact, projection, reader, vị trí đọc, `Create` mới, lọc active) + storetest `RunMembers` + write contract

Thêm bốn port theo hợp đồng, mỗi port là interface riêng (không thêm vào `Rooms`), nên `mongostore.Store` và mọi fake của `store.Rooms` (actor, grpcsrv, effects, resync, mutate, access) không vỡ ở task này. memstore: kiểu mới `MemberActions` (log fact nằm trong `Rooms`, lấy bằng `Rooms.MemberActions()`; `NewRooms()` không đổi chữ ký); `*Rooms` cài `MemberProjector`, `MemberReader`, `ReadPositions`. Mongo cài ở Task 5.

**`Rooms.Create` đổi ngữ nghĩa (mọi adapter):** insert room (`mc = len(members)`, không `mv`) → `Append(domain.InitialMembers(r, members))` → `ApplyMembers` → `AdvanceMembers(r.ID, 0, 1, len)`. Fact được validate **trước** khi ghi room, nên input sai không để lại room. Hệ quả cho mọi test dựng room qua memstore từ task này:
- `Get` trả `MemberVersion == 1`; `Member` trả `MV == 1`, `Read == {0, 1}` (`Applied` của add tăng `rs.v` từ 0), `JoinedAt == r.CreatedAt` (mọi fixture đã đặt `JoinedAt == CreatedAt`).
- storetest thêm hai helper `created(room)` và `joined(member)` cho kỳ vọng sau `Create`; các chỗ so `assertRoom`/`assertMember` sau `Create` dùng chúng (bảng dưới).
- `Create` giờ từ chối member trùng user (`ValidateMemberAction`); trước đây memstore giữ bản đầu và Mongo bỏ qua trùng khoá. `domain.NewRoom` đã khử trùng nên không caller nào bị ảnh hưởng.
- Room có hơn `domain.MaxMemberChanges` (1000) member không tạo được nữa (`ErrInvalidArgument`) cho tới khi Task 11 đặt trần mỗi lệnh 500 và bỏ trần 5000 của `domain.NewRoom` (khoảng trống chỉ dev).

| File | Chỗ | Sửa |
|---|---|---|
| `storetest/rooms_cases.go` | `roomsCreate`, `roomsCreateExisting`, `roomsMembership` | `assertRoom(…, created(room))`, `assertMember(…, joined(m))` |
| `storetest/apply_cases.go:126` | `assertMember(t, s.rooms, members[0])` | `joined(members[0])` |
| `storetest/viewer_cases.go` `clearForward` | `alice := members[0]`; `assertMember(t, s.rooms, members[1])` | `joined(members[0])`; `joined(members[1])` |
| `storetest/pin_state_cases.go:70` | `assertRoom(t, s.rooms, room)` | `created(room)` |

Thêm ngoài hợp đồng (dùng chung cho hai adapter, để luật kiểm nằm một chỗ; Task 19 ghi vào INDEXES như dưới):
- `store.ValidateMemberAdvance(base, mv uint64, count int) error` = `ValidateVersionBump` + `count ≥ 0` (`invalid("member count")`).
- `store.ValidateMemberUsers(users []string) error`: ≤ `MaxMemberChanges + 1` (`invalid("users")`).
- `store.ValidateUserRoomsQuery(tenant, user string, limit int) error`: `ValidateLimit(limit, MaxUserRoomsLimit)`, `ValidTenant`, `ValidUser`.
- `store.ValidateReadSeq(seq uint64) error`: ≤ `MaxInt64` (`invalid("seq")`).
- `ValidateMemberAction` kiểm thêm `ReadSeq ≤ MaxInt64` của mỗi change (`invalid("read seq")`), để memstore và Mongo cùng từ chối giá trị Mongo không lưu được.

`MembersOf` trả doc theo thứ tự user tăng dần, mỗi user một lần (hợp đồng không nói; chốt để hai adapter giống nhau). `UserRooms` trả `domain.Member` chỉ gồm field của `user_rooms` (`Room, Tenant, User, Role, JoinedAt, MV`), không có `Read`/`ClearedBeforeSeq`; memstore suy ra từ map member (không có map riêng) và bỏ hai field đó.

`ChangeKind` thêm `MemberInserted = 6` (giá trị đi vào byte đầu của work record, chỉ được thêm cuối). `Change` thêm `Member` (cuối, sau `Pin`). Lint `exhaustive` sẽ báo `work.RecordOf` thiếu case: task này thêm case chỉ lấy khoá (`Room = c.Member.Room`, `Seq = c.Member.MV`). `KnownKind` và id `g:` để Task 6, nên chưa record nào đi qua.

**Files:**
- Create: `apps/core/internal/store/member.go`, `apps/core/internal/store/member_validate.go`, `apps/core/internal/store/member_test.go`
- Modify: `apps/core/internal/store/feed.go` (`ChangeKind`, `Change`), `apps/core/internal/store/reaction_test.go` (`TestChangeKindsOnlyGrowAtTheEnd`), `apps/core/internal/store/write_contract_test.go`
- Modify: `apps/core/internal/work/record.go` (`RecordOf`), `apps/core/internal/work/record_test.go`
- Create: `apps/core/internal/store/memstore/member_actions.go`, `member_projection.go`, `member_reads.go`, `read_positions.go`
- Modify: `apps/core/internal/store/memstore/rooms.go`, `apps/core/internal/store/memstore/memstore_test.go`
- Create: `apps/core/internal/store/storetest/members.go`, `member_fact_cases.go`, `member_projection_cases.go`, `member_read_cases.go`, `read_position_cases.go`
- Modify: `apps/core/internal/store/storetest/rooms_cases.go`, `apply_cases.go`, `viewer_cases.go`, `pin_state_cases.go`
- Modify: `INDEXES.csv` (dòng `apps/core/internal/store`, `apps/core/internal/store/memstore`, `apps/core/internal/store/storetest`, `apps/core/internal/work`)

**Step 1: Test store + work**

`apps/core/internal/store/member_test.go` (`assertField` có sẵn ở `reaction_test.go`):

```go
package store_test

import (
	"errors"
	"math"
	"strconv"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestMemberErrorsAndLimits(t *testing.T) {
	if !errors.Is(store.ErrMemberActionExists, apperr.ErrAlreadyExists) || !errors.Is(store.ErrMemberActionNotFound, apperr.ErrNotFound) {
		t.Fatalf("ErrMemberActionExists = %v, ErrMemberActionNotFound = %v; want already exists and not found", store.ErrMemberActionExists, store.ErrMemberActionNotFound)
	}
	if store.MaxMemberScan != 1000 || store.MaxUserRoomsLimit != 1000 {
		t.Fatalf("MaxMemberScan = %d, MaxUserRoomsLimit = %d; want 1000 and 1000", store.MaxMemberScan, store.MaxUserRoomsLimit)
	}
}

func TestValidateMemberAction(t *testing.T) {
	add := domain.MemberAction{
		Room: 1, MV: 1, Tenant: "acme", Op: domain.MemberOpAdd, By: "alice", Count: 1,
		Changes: []domain.MemberChange{{User: "bob", Role: domain.RoleMember}},
	}
	many := make([]domain.MemberChange, domain.MaxMemberChanges+1)
	for i := range many {
		many[i] = domain.MemberChange{User: "u" + strconv.Itoa(i), Role: domain.RoleMember}
	}
	only := func(op domain.MemberOp, c domain.MemberChange) func(*domain.MemberAction) {
		return func(a *domain.MemberAction) { a.Op, a.Changes = op, []domain.MemberChange{c} }
	}
	leave := func(successor string) func(*domain.MemberAction) {
		return func(a *domain.MemberAction) {
			a.Op, a.Successor, a.Changes = domain.MemberOpLeave, successor, []domain.MemberChange{{User: "bob", Prev: domain.RoleOwner}}
		}
	}
	for name, c := range map[string]struct {
		mutate func(*domain.MemberAction)
		field  string
	}{
		"add":                     {func(*domain.MemberAction) {}, ""},
		"max int64 version":       {func(a *domain.MemberAction) { a.MV = math.MaxInt64 }, ""},
		"a full batch":            {func(a *domain.MemberAction) { a.Changes = many[:domain.MaxMemberChanges] }, ""},
		"remove":                  {only(domain.MemberOpRemove, domain.MemberChange{User: "bob", Prev: domain.RoleMember}), ""},
		"role":                    {only(domain.MemberOpRole, domain.MemberChange{User: "bob", Role: domain.RoleAdmin, Prev: domain.RoleMember}), ""},
		"leave with a successor":  {leave("carol"), ""},
		"zero room":               {func(a *domain.MemberAction) { a.Room = 0 }, "room"},
		"zero version":            {func(a *domain.MemberAction) { a.MV = 0 }, "member version"},
		"version above max int64": {func(a *domain.MemberAction) { a.MV = math.MaxInt64 + 1 }, "member version"},
		"zero op":                 {func(a *domain.MemberAction) { a.Op = 0 }, "member op"},
		"op past role":            {func(a *domain.MemberAction) { a.Op = domain.MemberOpRole + 1 }, "member op"},
		"negative count":          {func(a *domain.MemberAction) { a.Count = -1 }, "member count"},
		"successor on an add":     {func(a *domain.MemberAction) { a.Successor = "carol" }, "successor"},
		"bad successor":           {leave("c.c"), "user"},
		"bad tenant":              {func(a *domain.MemberAction) { a.Tenant = "Acme" }, "tenant"},
		"bad actor":               {func(a *domain.MemberAction) { a.By = "a.a" }, "user"},
		"no changes":              {func(a *domain.MemberAction) { a.Changes = nil }, "member changes"},
		"too many changes":        {func(a *domain.MemberAction) { a.Changes = many }, "member changes"},
		"two changes on a remove": {func(a *domain.MemberAction) { a.Op, a.Changes = domain.MemberOpRemove, many[:2] }, "member changes"},
		"same user twice":         {func(a *domain.MemberAction) { a.Changes = []domain.MemberChange{many[0], many[0]} }, "member changes"},
		"bad user":                {only(domain.MemberOpAdd, domain.MemberChange{User: "b b", Role: domain.RoleMember}), "user"},
		"read seq above max":      {only(domain.MemberOpAdd, domain.MemberChange{User: "bob", Role: domain.RoleMember, ReadSeq: math.MaxInt64 + 1}), "read seq"},
		"add without a role":      {only(domain.MemberOpAdd, domain.MemberChange{User: "bob"}), "role"},
		"role change to a stray":  {only(domain.MemberOpRole, domain.MemberChange{User: "bob", Role: "boss"}), "role"},
	} {
		a := add
		c.mutate(&a)
		assertField(t, "ValidateMemberAction("+name+")", store.ValidateMemberAction(a), c.field)
	}
}

func TestMemberHelperValidators(t *testing.T) {
	assertField(t, "ValidateMemberAdvance(ok)", store.ValidateMemberAdvance(1, 2, 0), "")
	assertField(t, "ValidateMemberAdvance(no move)", store.ValidateMemberAdvance(2, 2, 1), "version")
	assertField(t, "ValidateMemberAdvance(negative count)", store.ValidateMemberAdvance(1, 2, -1), "member count")
	assertField(t, "ValidateMemberUsers(max)", store.ValidateMemberUsers(make([]string, domain.MaxMemberChanges+1)), "")
	assertField(t, "ValidateMemberUsers(too many)", store.ValidateMemberUsers(make([]string, domain.MaxMemberChanges+2)), "users")
	assertField(t, "ValidateUserRoomsQuery(ok)", store.ValidateUserRoomsQuery("acme", "Bob", store.MaxUserRoomsLimit), "")
	assertField(t, "ValidateUserRoomsQuery(zero limit)", store.ValidateUserRoomsQuery("acme", "bob", 0), "limit")
	assertField(t, "ValidateUserRoomsQuery(bad tenant)", store.ValidateUserRoomsQuery("Acme", "bob", 1), "tenant")
	assertField(t, "ValidateUserRoomsQuery(bad user)", store.ValidateUserRoomsQuery("acme", "b b", 1), "user")
	assertField(t, "ValidateReadSeq(max)", store.ValidateReadSeq(math.MaxInt64), "")
	assertField(t, "ValidateReadSeq(above max)", store.ValidateReadSeq(math.MaxInt64+1), "seq")
}
```

`apps/core/internal/store/reaction_test.go`, trong `TestChangeKindsOnlyGrowAtTheEnd` thay:

```go
	kinds := []store.ChangeKind{store.MessageInserted, store.RoomInserted, store.EditInserted, store.ReactionChanged, store.PinInserted}
```

bằng:

```go
	kinds := []store.ChangeKind{store.MessageInserted, store.RoomInserted, store.EditInserted, store.ReactionChanged, store.PinInserted, store.MemberInserted}
```

`apps/core/internal/store/write_contract_test.go`:
- thay:

```go
	"PinProjector.PinState":          "read",
	"PinProjector.ApplyPins":         "cas",
}
```

bằng:

```go
	"PinProjector.PinState":          "read",
	"PinProjector.ApplyPins":         "cas",

	"MemberActions.Append":           "insert-unique",
	"MemberActions.At":               "read",
	"MemberActions.After":            "read",
	"MemberActions.Between":          "read",
	"MemberProjector.ApplyMembers":   "monotonic-cas",
	"MemberProjector.AdvanceMembers": "cas",
	"MemberReader.MembersOf":         "read",
	"MemberReader.Successor":         "read",
	"MemberReader.Owners":            "read",
	"MemberReader.UserRooms":         "read",
	"ReadPositions.MarkRead":         "version-bump",
	"ReadPositions.MarkUnread":       "version-bump",
}
```

- thay:

```go
		reflect.TypeFor[store.PinProjector](),
	}
```

bằng:

```go
		reflect.TypeFor[store.PinProjector](),
		reflect.TypeFor[store.MemberActions](),
		reflect.TypeFor[store.MemberProjector](),
		reflect.TypeFor[store.MemberReader](),
		reflect.TypeFor[store.ReadPositions](),
	}
```

`apps/core/internal/work/record_test.go`, trong `TestRecordOfKeepsOnlyKeysAndCommitTime` thay:

```go
	if got, want := work.RecordOf(pin), (work.Record{Kind: store.PinInserted, Room: 42, Seq: 6, CommittedAt: committed}); got != want {
		t.Fatalf("RecordOf(pin) = %+v, want %+v", got, want)
	}
}
```

bằng:

```go
	if got, want := work.RecordOf(pin), (work.Record{Kind: store.PinInserted, Room: 42, Seq: 6, CommittedAt: committed}); got != want {
		t.Fatalf("RecordOf(pin) = %+v, want %+v", got, want)
	}
	fact := domain.MemberAction{Room: 42, MV: 5, Tenant: "acme", Op: domain.MemberOpAdd, Changes: []domain.MemberChange{{User: "bob", Role: domain.RoleMember}}, By: "alice"}
	member := store.Change{Kind: store.MemberInserted, Member: fact, CommittedAt: committed}
	if got, want := work.RecordOf(member), (work.Record{Kind: store.MemberInserted, Room: 42, Seq: 5, CommittedAt: committed}); got != want {
		t.Fatalf("RecordOf(member) = %+v, want %+v", got, want)
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/ ./apps/core/internal/work/..."`
Expected: FAIL biên dịch: `undefined: store.ErrMemberActionExists`, `undefined: store.ErrMemberActionNotFound`, `undefined: store.MaxMemberScan`, `undefined: store.MaxUserRoomsLimit`, `undefined: store.ValidateMemberAction`, `undefined: store.MemberInserted`, `undefined: store.MemberActions`, `undefined: store.MemberProjector` (store); `undefined: store.MemberInserted`, `unknown field Member in struct literal of type store.Change` (work). Go dừng sau 10 lỗi mỗi package.

**Step 3: Code store + work**

`apps/core/internal/store/member.go`:

```go
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const MaxMemberScan, MaxUserRoomsLimit = 1000, 1000

var (
	ErrMemberActionExists   = fmt.Errorf("member version %w", apperr.ErrAlreadyExists)
	ErrMemberActionNotFound = fmt.Errorf("member action %w", apperr.ErrNotFound)
)

type MemberActions interface {
	Append(ctx context.Context, a domain.MemberAction) error
	At(ctx context.Context, room, mv uint64) (domain.MemberAction, error)
	After(ctx context.Context, room, mv uint64, limit int) ([]domain.MemberAction, error)
	Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.MemberAction, error)
}

type MemberProjector interface {
	ApplyMembers(ctx context.Context, a domain.MemberAction) error
	AdvanceMembers(ctx context.Context, room, base, mv uint64, count int) (bool, error)
}

type MemberReader interface {
	MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error)
	Successor(ctx context.Context, room uint64) (domain.Member, bool, error)
	Owners(ctx context.Context, room uint64, limit int) ([]domain.Member, error)
	UserRooms(ctx context.Context, tenant, user string, after uint64, limit int) ([]domain.Member, error)
}

type ReadPositions interface {
	MarkRead(ctx context.Context, room uint64, user string, seq uint64) (domain.ReadPos, bool, error)
	MarkUnread(ctx context.Context, room uint64, user string, to uint64) (domain.ReadPos, bool, error)
}
```

`apps/core/internal/store/member_validate.go`:

```go
package store

import (
	"math"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func ValidateMemberAction(a domain.MemberAction) error {
	switch {
	case a.Room == 0:
		return invalid("room")
	case a.MV == 0 || a.MV > math.MaxInt64:
		return invalid("member version")
	case a.Op < domain.MemberOpAdd || a.Op > domain.MemberOpRole:
		return invalid("member op")
	case a.Count < 0:
		return invalid("member count")
	case a.Successor != "" && a.Op != domain.MemberOpLeave:
		return invalid("successor")
	}
	if err := domain.ValidTenant(a.Tenant); err != nil {
		return err
	}
	if err := domain.ValidUser(a.By); err != nil {
		return err
	}
	if a.Successor != "" {
		if err := domain.ValidUser(a.Successor); err != nil {
			return err
		}
	}
	return validateMemberChanges(a.Op, a.Changes)
}

func validateMemberChanges(op domain.MemberOp, changes []domain.MemberChange) error {
	n := len(changes)
	if n == 0 || n > domain.MaxMemberChanges || (op != domain.MemberOpAdd && n != 1) {
		return invalid("member changes")
	}
	seen := make(map[string]struct{}, n)
	for _, c := range changes {
		if err := domain.ValidUser(c.User); err != nil {
			return err
		}
		if _, dup := seen[c.User]; dup {
			return invalid("member changes")
		}
		seen[c.User] = struct{}{}
		if c.ReadSeq > math.MaxInt64 {
			return invalid("read seq")
		}
		if op == domain.MemberOpAdd || op == domain.MemberOpRole {
			if _, err := domain.ParseRole(string(c.Role)); err != nil {
				return err
			}
		}
	}
	return nil
}

func ValidateMemberAdvance(base, mv uint64, count int) error {
	if err := ValidateVersionBump(base, mv); err != nil {
		return err
	}
	if count < 0 {
		return invalid("member count")
	}
	return nil
}

func ValidateMemberUsers(users []string) error {
	if len(users) > domain.MaxMemberChanges+1 {
		return invalid("users")
	}
	return nil
}

func ValidateUserRoomsQuery(tenant, user string, limit int) error {
	if err := ValidateLimit(limit, MaxUserRoomsLimit); err != nil {
		return err
	}
	if err := domain.ValidTenant(tenant); err != nil {
		return err
	}
	return domain.ValidUser(user)
}

func ValidateReadSeq(seq uint64) error {
	if seq > math.MaxInt64 {
		return invalid("seq")
	}
	return nil
}
```

`apps/core/internal/store/feed.go`:
- thay:

```go
	ReactionChanged
	PinInserted
)
```

bằng:

```go
	ReactionChanged
	PinInserted
	MemberInserted
)
```

- trong `type Change struct` thay:

```go
	Pin         domain.PinAction
	CommittedAt time.Time
```

bằng:

```go
	Pin         domain.PinAction
	Member      domain.MemberAction
	CommittedAt time.Time
```

`apps/core/internal/work/record.go`, trong `RecordOf` thay:

```go
	case store.PinInserted:
		r.Room, r.Seq = c.Pin.Room, c.Pin.PV
	}
	return r
```

bằng:

```go
	case store.PinInserted:
		r.Room, r.Seq = c.Pin.Room, c.Pin.PV
	case store.MemberInserted:
		r.Room, r.Seq = c.Member.Room, c.Member.MV
	}
	return r
```

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/ ./apps/core/internal/work/..."`
Expected: PASS (`TestKnownKindsAreTheFiveChangeKinds` vẫn đúng: `KnownKind(6)` vẫn false tới Task 6; `TestEveryPortMethodHasAWriteContract` thấy đủ 12 method mới).

**Step 4: Test storetest + memstore**

`apps/core/internal/store/storetest/rooms_cases.go`:
- thêm ngay sau hàm `teamOf`:

```go
func created(r domain.Room) domain.Room {
	r.MemberVersion = 1
	return r
}

func joined(m domain.Member) domain.Member {
	m.MV, m.Read = 1, domain.ReadPos{Version: 1}
	return m
}
```

- trong `roomsCreate` và `roomsCreateExisting` thay `assertRoom(t, s, room)` bằng `assertRoom(t, s, created(room))` và `assertMember(t, s, m)` bằng `assertMember(t, s, joined(m))` (hai hàm, mỗi hàm một chỗ mỗi loại).
- trong `roomsMembership` thay `assertMember(t, s, member(roomB, "carol", domain.RoleOwner))` bằng `assertMember(t, s, joined(member(roomB, "carol", domain.RoleOwner)))`.

`apps/core/internal/store/storetest/apply_cases.go`, trong `editsCancelled` thay `assertMember(t, s.rooms, members[0])` bằng `assertMember(t, s.rooms, joined(members[0]))`.

`apps/core/internal/store/storetest/viewer_cases.go`, trong `clearForward` thay `alice := members[0]` bằng `alice := joined(members[0])` và `assertMember(t, s.rooms, members[1])` bằng `assertMember(t, s.rooms, joined(members[1]))`.

`apps/core/internal/store/storetest/pin_state_cases.go`, trong `pinStateRoomUnchanged` thay `assertRoom(t, s.rooms, room)` bằng `assertRoom(t, s.rooms, created(room))`.

`apps/core/internal/store/storetest/members.go`:

```go
package storetest

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type MemberRooms interface {
	store.Rooms
	store.HistoryClearer
	store.MemberProjector
	store.MemberReader
	store.ReadPositions
}

type memberStores struct {
	rooms MemberRooms
	facts store.MemberActions
}

type memberCase struct {
	name string
	run  func(t *testing.T, s memberStores)
}

func RunMembers(t *testing.T, open func(t *testing.T) (MemberRooms, store.MemberActions)) {
	t.Helper()
	for _, c := range slices.Concat(memberFactCases(), memberProjectionCases(), memberReadCases(), readPositionCases()) {
		t.Run(c.name, func(t *testing.T) {
			rooms, facts := open(t)
			c.run(t, memberStores{rooms: rooms, facts: facts})
		})
	}
}

func memberAct(room uint64, mv uint32, count int, op domain.MemberOp, changes ...domain.MemberChange) domain.MemberAction {
	return domain.MemberAction{
		Room: room, MV: uint64(mv), Tenant: tenant, Op: op, Changes: changes, By: "alice",
		At: baseTime.Add(time.Duration(mv) * time.Minute), Count: count,
	}
}

func addChange(user string, role domain.Role, readSeq uint64) domain.MemberChange {
	return domain.MemberChange{User: user, Role: role, ReadSeq: readSeq}
}

func dropChange(user string, prev domain.Role) domain.MemberChange {
	return domain.MemberChange{User: user, Prev: prev}
}

func roleChange(user string, role, prev domain.Role) domain.MemberChange {
	return domain.MemberChange{User: user, Role: role, Prev: prev}
}

func crowd(id uint64, users ...string) (domain.Room, []domain.Member) {
	out := make([]domain.Member, len(users))
	for i, u := range users {
		role := domain.RoleMember
		if i == 0 {
			role = domain.RoleOwner
		}
		out[i] = member(id, u, role)
	}
	return group(id, "Crowd", len(users)), out
}

func userRoom(m domain.Member) domain.Member {
	return domain.Member{Room: m.Room, Tenant: m.Tenant, User: m.User, Role: m.Role, JoinedAt: m.JoinedAt, MV: m.MV}
}

func mustAppendActs(t *testing.T, s store.MemberActions, acts ...domain.MemberAction) {
	t.Helper()
	for _, a := range acts {
		if err := s.Append(t.Context(), a); err != nil {
			t.Fatalf("Append(room %d v%d): %v", a.Room, a.MV, err)
		}
	}
}

func mustApplyActs(t *testing.T, s MemberRooms, acts ...domain.MemberAction) {
	t.Helper()
	for _, a := range acts {
		if err := s.ApplyMembers(t.Context(), a); err != nil {
			t.Fatalf("ApplyMembers(room %d v%d): %v", a.Room, a.MV, err)
		}
	}
}

func mustAdvance(t *testing.T, s MemberRooms, room, base, mv uint64, count int, want bool) {
	t.Helper()
	if ok, err := s.AdvanceMembers(t.Context(), room, base, mv, count); err != nil || ok != want {
		t.Fatalf("AdvanceMembers(%d, %d -> %d, %d) = %v, %v; want %v", room, base, mv, count, ok, err, want)
	}
}

func sameMemberAction(a, b domain.MemberAction) bool {
	at, bt, ac, bc := a.At, b.At, a.Changes, b.Changes
	a.At, b.At, a.Changes, b.Changes = time.Time{}, time.Time{}, nil, nil
	return reflect.DeepEqual(a, b) && at.Equal(bt) && slices.Equal(ac, bc)
}

func assertMemberActions(t *testing.T, op string, got, want []domain.MemberAction) {
	t.Helper()
	if !slices.EqualFunc(got, want, sameMemberAction) {
		t.Fatalf("%s = %+v,\nwant %+v", op, got, want)
	}
}

func sameMember(a, b domain.Member) bool {
	at, bt := a.JoinedAt, b.JoinedAt
	a.JoinedAt, b.JoinedAt = time.Time{}, time.Time{}
	return a == b && at.Equal(bt)
}

func assertMembers(t *testing.T, op string, got []domain.Member, err error, want []domain.Member) {
	t.Helper()
	if err != nil || !slices.EqualFunc(got, want, sameMember) {
		t.Fatalf("%s = %+v, %v;\nwant %+v", op, got, err, want)
	}
}

func assertUserRooms(t *testing.T, s MemberRooms, user string, after uint64, limit int, want []domain.Member) {
	t.Helper()
	got, err := s.UserRooms(t.Context(), tenant, user, after, limit)
	assertMembers(t, "UserRooms("+user+")", got, err, want)
}
```

`apps/core/internal/store/storetest/member_fact_cases.go`:

```go
package storetest

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func memberFactCases() []memberCase {
	return []memberCase{
		{"member facts append and read back by version", memberFactsAppendAt},
		{"an existing member version is refused and keeps the first fact", memberFactsExisting},
		{"member facts between two times sort by time then version", memberFactsBetween},
		{"invalid member facts and limits are rejected and not stored", memberFactsInvalid},
		{"create writes the initial members as fact 1", memberFactsCreate},
		{"member facts with a cancelled context", memberFactsCancelled},
	}
}

func memberFactsAppendAt(t *testing.T, s memberStores) {
	first := memberAct(roomB, 1, 2, domain.MemberOpAdd, addChange("alice", domain.RoleOwner, 0), addChange("bob", domain.RoleMember, 3))
	second := memberAct(roomB, 2, 1, domain.MemberOpRemove, dropChange("bob", domain.RoleMember))
	third := memberAct(roomB, 3, 1, domain.MemberOpRole, roleChange("alice", domain.RoleAdmin, domain.RoleOwner))
	leave := memberAct(roomB, 4, 0, domain.MemberOpLeave, dropChange("alice", domain.RoleAdmin))
	leave.Successor = "carol"
	other := memberAct(roomB+1, 1, 1, domain.MemberOpAdd, addChange("dave", domain.RoleOwner, 0))
	mustAppendActs(t, s.facts, first, second, third, leave, other)
	for _, want := range []domain.MemberAction{second, leave, other} {
		got, err := s.facts.At(t.Context(), want.Room, want.MV)
		if err != nil || !sameMemberAction(got, want) {
			t.Fatalf("At(%d, %d) = %+v, %v; want %+v", want.Room, want.MV, got, err, want)
		}
	}
	_, err := s.facts.At(t.Context(), roomB, 9)
	assertErrorIs(t, "At(missing)", err, store.ErrMemberActionNotFound)
	for _, step := range []struct {
		room, after uint64
		limit       int
		want        []domain.MemberAction
	}{
		{roomB, 0, 10, []domain.MemberAction{first, second, third, leave}},
		{roomB, 1, 2, []domain.MemberAction{second, third}},
		{roomB, 4, 10, nil},
		{roomB + 1, 0, 10, []domain.MemberAction{other}},
		{roomA, 0, 10, nil},
	} {
		got, err := s.facts.After(t.Context(), step.room, step.after, step.limit)
		if err != nil {
			t.Fatalf("After(%d, %d, %d): %v", step.room, step.after, step.limit, err)
		}
		assertMemberActions(t, "After", got, step.want)
	}
}

func memberFactsExisting(t *testing.T, s memberStores) {
	first := memberAct(roomB, 1, 1, domain.MemberOpAdd, addChange("alice", domain.RoleOwner, 0))
	mustAppendActs(t, s.facts, first)
	err := s.facts.Append(t.Context(), memberAct(roomB, 1, 2, domain.MemberOpAdd, addChange("bob", domain.RoleMember, 0)))
	assertErrorIs(t, "Append(existing version)", err, store.ErrMemberActionExists)
	assertErrorIs(t, "Append(existing version)", err, apperr.ErrAlreadyExists)
	got, err := s.facts.At(t.Context(), roomB, 1)
	if err != nil || !sameMemberAction(got, first) {
		t.Fatalf("At(1) = %+v, %v; want the first fact %+v", got, err, first)
	}
}

func memberFactsBetween(t *testing.T, s memberStores) {
	acts := []domain.MemberAction{
		memberAct(roomB, 1, 1, domain.MemberOpAdd, addChange("alice", domain.RoleOwner, 0)),
		memberAct(roomB, 2, 2, domain.MemberOpAdd, addChange("bob", domain.RoleMember, 0)),
		memberAct(roomB, 3, 1, domain.MemberOpRemove, dropChange("bob", domain.RoleMember)),
		memberAct(roomB, 4, 2, domain.MemberOpAdd, addChange("carol", domain.RoleMember, 0)),
		memberAct(roomB+1, 1, 1, domain.MemberOpAdd, addChange("dave", domain.RoleOwner, 0)),
	}
	acts[2].At = acts[0].At
	mustAppendActs(t, s.facts, acts...)
	at := func(minutes int) time.Time { return baseTime.Add(time.Duration(minutes) * time.Minute) }
	for _, step := range []struct {
		from, to time.Time
		limit    int
		want     []domain.MemberAction
	}{
		{at(1), at(3), 10, []domain.MemberAction{acts[0], acts[2], acts[1]}},
		{at(1), at(3), 2, []domain.MemberAction{acts[0], acts[2]}},
		{at(2), at(4), 10, []domain.MemberAction{acts[1], acts[3]}},
		{at(5), at(9), 10, nil},
	} {
		got, err := s.facts.Between(t.Context(), roomB, step.from, step.to, step.limit)
		if err != nil {
			t.Fatalf("Between(%v, %v, %d): %v", step.from, step.to, step.limit, err)
		}
		assertMemberActions(t, "Between", got, step.want)
	}
}

func memberFactsInvalid(t *testing.T, s memberStores) {
	for name, mutate := range map[string]func(*domain.MemberAction){
		"zero room":               func(a *domain.MemberAction) { a.Room = 0 },
		"zero version":            func(a *domain.MemberAction) { a.MV = 0 },
		"version above max int64": func(a *domain.MemberAction) { a.MV = math.MaxInt64 + 1 },
		"unknown op":              func(a *domain.MemberAction) { a.Op = domain.MemberOpRole + 1 },
		"no changes":              func(a *domain.MemberAction) { a.Changes = nil },
		"same user twice":         func(a *domain.MemberAction) { a.Changes = []domain.MemberChange{a.Changes[0], a.Changes[0]} },
		"add without a role":      func(a *domain.MemberAction) { a.Changes = []domain.MemberChange{addChange("bob", "", 0)} },
		"successor on an add":     func(a *domain.MemberAction) { a.Successor = "bob" },
	} {
		a := memberAct(roomB, 1, 1, domain.MemberOpAdd, addChange("alice", domain.RoleOwner, 0))
		mutate(&a)
		assertErrorIs(t, "Append("+name+")", s.facts.Append(t.Context(), a), apperr.ErrInvalidArgument)
	}
	if got, err := s.facts.After(t.Context(), roomB, 0, 10); err != nil || len(got) != 0 {
		t.Fatalf("After after invalid appends = %+v, %v; want nothing stored", got, err)
	}
	for _, limit := range []int{0, store.MaxMemberScan + 1} {
		_, err := s.facts.After(t.Context(), roomB, 0, limit)
		assertErrorIs(t, "After(bad limit)", err, apperr.ErrInvalidArgument)
		_, err = s.facts.Between(t.Context(), roomB, baseTime, baseTime, limit)
		assertErrorIs(t, "Between(bad limit)", err, apperr.ErrInvalidArgument)
	}
}

func memberFactsCreate(t *testing.T, s memberStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	got, err := s.facts.At(t.Context(), roomA, 1)
	if want := domain.InitialMembers(room, members); err != nil || !sameMemberAction(got, want) {
		t.Fatalf("At(1) after Create = %+v, %v; want %+v", got, err, want)
	}
	if later, err := s.facts.After(t.Context(), roomA, 1, 10); err != nil || len(later) != 0 {
		t.Fatalf("After(1) after Create = %+v, %v; want no other fact", later, err)
	}
}

func memberFactsCancelled(t *testing.T, s memberStores) {
	ctx := cancelledContext(t)
	a := memberAct(roomB, 1, 1, domain.MemberOpAdd, addChange("alice", domain.RoleOwner, 0))
	assertErrorIs(t, "Append", s.facts.Append(ctx, a), context.Canceled)
	_, err := s.facts.At(ctx, roomB, 1)
	assertErrorIs(t, "At", err, context.Canceled)
	_, err = s.facts.After(ctx, roomB, 0, 10)
	assertErrorIs(t, "After", err, context.Canceled)
	_, err = s.facts.Between(ctx, roomB, baseTime, baseTime, 10)
	assertErrorIs(t, "Between", err, context.Canceled)
	if got, err := s.facts.After(t.Context(), roomB, 0, 10); err != nil || len(got) != 0 {
		t.Fatalf("After after a cancelled Append = %+v, %v; want nothing stored", got, err)
	}
}
```

`apps/core/internal/store/storetest/member_projection_cases.go`:

```go
package storetest

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func memberProjectionCases() []memberCase {
	return []memberCase{
		{"create projects the initial members and moves the head to 1", projectCreate},
		{"apply writes every touched member and leaves the head alone", projectApply},
		{"a fact at or below a member's version never touches it", projectGuard},
		{"remove leaves a tombstone and a re-add revives it", projectTombstone},
		{"a leave hands the owner role to the successor", projectSuccessor},
		{"advance moves the head only from its base", projectAdvance},
		{"invalid facts, invalid head moves and a cancelled context write nothing", projectInvalid},
	}
}

func projectCreate(t *testing.T, s memberStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	assertRoom(t, s.rooms, created(room))
	for _, m := range members {
		assertMember(t, s.rooms, joined(m))
	}
	assertUserRooms(t, s.rooms, "bob", 0, 10, []domain.Member{userRoom(joined(members[1]))})
}

func projectApply(t *testing.T, s memberStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	add := memberAct(roomA, 2, 3, domain.MemberOpAdd, addChange("carol", domain.RoleMember, 7))
	mustApplyActs(t, s.rooms, add)
	carol := domain.Member{Room: roomA, Tenant: tenant, User: "carol", Role: domain.RoleMember, JoinedAt: add.At, MV: 2, Read: domain.ReadPos{Seq: 7, Version: 1}}
	assertMember(t, s.rooms, carol)
	assertMember(t, s.rooms, joined(members[0]))
	assertRoom(t, s.rooms, created(room))
	mustAdvance(t, s.rooms, roomA, 1, 2, 3, true)
	head := created(room)
	head.MemberVersion, head.MemberCount = 2, 3
	assertRoom(t, s.rooms, head)
	assertUserRooms(t, s.rooms, "carol", 0, 10, []domain.Member{userRoom(carol)})
}

func projectGuard(t *testing.T, s memberStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	promote := memberAct(roomA, 3, 2, domain.MemberOpRole, roleChange("bob", domain.RoleAdmin, domain.RoleMember))
	remove := memberAct(roomA, 2, 1, domain.MemberOpRemove, dropChange("bob", domain.RoleMember))
	stale := memberAct(roomA, 1, 2, domain.MemberOpAdd, addChange("bob", domain.RoleMember, 50))
	mustApplyActs(t, s.rooms, promote, remove, promote, stale)
	bob := joined(members[1])
	bob.Role, bob.MV = domain.RoleAdmin, 3
	assertMember(t, s.rooms, bob)
	assertUserRooms(t, s.rooms, "bob", 0, 10, []domain.Member{userRoom(bob)})
	add := memberAct(roomA, 4, 3, domain.MemberOpAdd, addChange("carol", domain.RoleMember, 2))
	mustApplyActs(t, s.rooms, add, add)
	assertMember(t, s.rooms, domain.Member{Room: roomA, Tenant: tenant, User: "carol", Role: domain.RoleMember, JoinedAt: add.At, MV: 4, Read: domain.ReadPos{Seq: 2, Version: 1}})
}

func projectTombstone(t *testing.T, s memberStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	if n, err := s.rooms.ClearHistory(t.Context(), roomA, "bob", 4); err != nil || n != 4 {
		t.Fatalf("ClearHistory(bob, 4) = %d, %v", n, err)
	}
	if pos, moved, err := s.rooms.MarkRead(t.Context(), roomA, "bob", 3); err != nil || !moved || pos != (domain.ReadPos{Seq: 3, Version: 2}) {
		t.Fatalf("MarkRead(bob, 3) = %+v, %v, %v", pos, moved, err)
	}
	mustApplyActs(t, s.rooms, memberAct(roomA, 2, 1, domain.MemberOpRemove, dropChange("bob", domain.RoleMember)))
	gone := joined(members[1])
	gone.Removed, gone.MV, gone.ClearedBeforeSeq, gone.Read = true, 2, 4, domain.ReadPos{Seq: 3, Version: 2}
	assertNotMember(t, s.rooms, roomA, "bob")
	got, err := s.rooms.MembersOf(t.Context(), roomA, []string{"bob"})
	assertMembers(t, "MembersOf(removed bob)", got, err, []domain.Member{gone})
	assertUserRooms(t, s.rooms, "bob", 0, 10, nil)
	_, err = s.rooms.ClearHistory(t.Context(), roomA, "bob", 9)
	assertErrorIs(t, "ClearHistory(removed)", err, domain.ErrNotMember)
	_, _, err = s.rooms.MarkRead(t.Context(), roomA, "bob", 9)
	assertErrorIs(t, "MarkRead(removed)", err, domain.ErrNotMember)
	back := memberAct(roomA, 3, 2, domain.MemberOpAdd, addChange("bob", domain.RoleMember, 9))
	mustApplyActs(t, s.rooms, back)
	bob := gone
	bob.Removed, bob.JoinedAt, bob.MV, bob.Read = false, back.At, 3, domain.ReadPos{Seq: 9, Version: 3}
	assertMember(t, s.rooms, bob)
	assertUserRooms(t, s.rooms, "bob", 0, 10, []domain.Member{userRoom(bob)})
	mustApplyActs(t, s.rooms,
		memberAct(roomA, 4, 1, domain.MemberOpLeave, dropChange("bob", domain.RoleMember)),
		memberAct(roomA, 5, 2, domain.MemberOpAdd, addChange("bob", domain.RoleMember, 1)),
	)
	bob.JoinedAt, bob.MV, bob.Read = baseTime.Add(5*time.Minute), 5, domain.ReadPos{Seq: 9, Version: 4}
	assertMember(t, s.rooms, bob)
}

func projectSuccessor(t *testing.T, s memberStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	leave := memberAct(roomA, 2, 1, domain.MemberOpLeave, dropChange("alice", domain.RoleOwner))
	leave.Successor = "bob"
	mustApplyActs(t, s.rooms, leave)
	assertNotMember(t, s.rooms, roomA, "alice")
	bob := joined(members[1])
	bob.Role, bob.MV = domain.RoleOwner, 2
	assertMember(t, s.rooms, bob)
	assertUserRooms(t, s.rooms, "bob", 0, 10, []domain.Member{userRoom(bob)})
	assertUserRooms(t, s.rooms, "alice", 0, 10, nil)
}

func projectAdvance(t *testing.T, s memberStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	mustAdvance(t, s.rooms, roomA, 0, 2, 5, false)
	mustAdvance(t, s.rooms, roomA, 2, 3, 5, false)
	mustAdvance(t, s.rooms, roomA, 1, 3, 5, true)
	mustAdvance(t, s.rooms, roomA, 1, 4, 6, false)
	mustAdvance(t, s.rooms, roomB, 0, 1, 1, false)
	head := created(room)
	head.MemberVersion, head.MemberCount = 3, 5
	assertRoom(t, s.rooms, head)
	assertNoRoom(t, s.rooms, roomB)
}

func projectInvalid(t *testing.T, s memberStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	stray := memberAct(roomA, 2, 2, domain.MemberOpRole, roleChange("bob", "boss", domain.RoleMember))
	assertErrorIs(t, "ApplyMembers(stray role)", s.rooms.ApplyMembers(t.Context(), stray), apperr.ErrInvalidArgument)
	for _, c := range []struct {
		base, mv uint64
		count    int
	}{{1, 1, 2}, {2, 1, 2}, {1, math.MaxInt64 + 1, 2}, {1, 2, -1}} {
		_, err := s.rooms.AdvanceMembers(t.Context(), roomA, c.base, c.mv, c.count)
		assertErrorIs(t, "AdvanceMembers", err, apperr.ErrInvalidArgument)
	}
	ctx := cancelledContext(t)
	promote := memberAct(roomA, 2, 2, domain.MemberOpRole, roleChange("bob", domain.RoleAdmin, domain.RoleMember))
	assertErrorIs(t, "ApplyMembers(cancelled)", s.rooms.ApplyMembers(ctx, promote), context.Canceled)
	_, err := s.rooms.AdvanceMembers(ctx, roomA, 1, 2, 2)
	assertErrorIs(t, "AdvanceMembers(cancelled)", err, context.Canceled)
	assertRoom(t, s.rooms, created(room))
	assertMember(t, s.rooms, joined(members[1]))
}
```

`apps/core/internal/store/storetest/member_read_cases.go`:

```go
package storetest

import (
	"context"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func memberReadCases() []memberCase {
	return []memberCase{
		{"members of returns stored docs, removed included, sorted by user", readMembersOf},
		{"successor is the earliest admin, else the earliest member, ties by user", readSuccessor},
		{"owners are the active owners by join time then user", readOwners},
		{"user rooms are the active rooms of one tenant and user, paged by room", readUserRooms},
		{"member reads reject bad input", readMembersInvalid},
		{"member reads with a cancelled context", readMembersCancelled},
	}
}

func readMembersOf(t *testing.T, s memberStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	mustApplyActs(t, s.rooms, memberAct(roomA, 2, 1, domain.MemberOpRemove, dropChange("bob", domain.RoleMember)))
	bob := joined(members[1])
	bob.Removed, bob.MV = true, 2
	got, err := s.rooms.MembersOf(t.Context(), roomA, []string{"zed", "bob", "alice", "bob"})
	assertMembers(t, "MembersOf(roomA)", got, err, []domain.Member{joined(members[0]), bob})
	got, err = s.rooms.MembersOf(t.Context(), roomB, []string{"alice"})
	assertMembers(t, "MembersOf(roomB)", got, err, nil)
	got, err = s.rooms.MembersOf(t.Context(), roomA, nil)
	assertMembers(t, "MembersOf(nobody)", got, err, nil)
}

func assertSuccessor(t *testing.T, s MemberRooms, room uint64, want string) {
	t.Helper()
	got, ok, err := s.Successor(t.Context(), room)
	if err != nil || ok != (want != "") || got.User != want || (ok && (got.Removed || got.Role == domain.RoleOwner)) {
		t.Fatalf("Successor(%d) = %+v, %v, %v; want %q", room, got, ok, err, want)
	}
}

func readSuccessor(t *testing.T, s memberStores) {
	room, members := crowd(roomA, "alice", "carl", "bob")
	mustCreate(t, s.rooms, room, members)
	assertSuccessor(t, s.rooms, roomA, "bob")
	mustApplyActs(t, s.rooms,
		memberAct(roomA, 2, 4, domain.MemberOpAdd, addChange("dave", domain.RoleAdmin, 0)),
		memberAct(roomA, 3, 5, domain.MemberOpAdd, addChange("erin", domain.RoleAdmin, 0)),
	)
	assertSuccessor(t, s.rooms, roomA, "dave")
	mustApplyActs(t, s.rooms, memberAct(roomA, 4, 4, domain.MemberOpRemove, dropChange("dave", domain.RoleAdmin)))
	assertSuccessor(t, s.rooms, roomA, "erin")
	mustApplyActs(t, s.rooms, memberAct(roomA, 5, 3, domain.MemberOpRemove, dropChange("erin", domain.RoleAdmin)))
	assertSuccessor(t, s.rooms, roomA, "bob")
	solo, owner := roomAt(roomB, tenant, baseTime)
	mustCreate(t, s.rooms, solo, owner)
	assertSuccessor(t, s.rooms, roomB, "")
}

func readOwners(t *testing.T, s memberStores) {
	room, members := crowd(roomA, "alice", "bob", "carol")
	mustCreate(t, s.rooms, room, members)
	mustApplyActs(t, s.rooms,
		memberAct(roomA, 2, 3, domain.MemberOpRole, roleChange("carol", domain.RoleOwner, domain.RoleMember)),
		memberAct(roomA, 3, 3, domain.MemberOpRole, roleChange("bob", domain.RoleOwner, domain.RoleMember)),
	)
	alice, bob, carol := joined(members[0]), joined(members[1]), joined(members[2])
	bob.Role, bob.MV, carol.Role, carol.MV = domain.RoleOwner, 3, domain.RoleOwner, 2
	got, err := s.rooms.Owners(t.Context(), roomA, 2)
	assertMembers(t, "Owners(2)", got, err, []domain.Member{alice, bob})
	got, err = s.rooms.Owners(t.Context(), roomA, 10)
	assertMembers(t, "Owners(10)", got, err, []domain.Member{alice, bob, carol})
	mustApplyActs(t, s.rooms, memberAct(roomA, 4, 2, domain.MemberOpLeave, dropChange("alice", domain.RoleOwner)))
	got, err = s.rooms.Owners(t.Context(), roomA, 10)
	assertMembers(t, "Owners after alice left", got, err, []domain.Member{bob, carol})
}

func readUserRooms(t *testing.T, s memberStores) {
	for _, id := range []uint64{roomA, roomB} {
		room, members := crowd(id, "alice", "bob")
		mustCreate(t, s.rooms, room, members)
	}
	third, thirdMembers := crowd(roomB+1, "alice", "bobby")
	mustCreate(t, s.rooms, third, thirdMembers)
	foreign, foreignMembers := crowd(roomB+2, "alice", "bob")
	foreign.Tenant = "other"
	for i := range foreignMembers {
		foreignMembers[i].Tenant = "other"
	}
	mustCreate(t, s.rooms, foreign, foreignMembers)
	mustApplyActs(t, s.rooms, memberAct(roomB, 2, 1, domain.MemberOpRemove, dropChange("bob", domain.RoleMember)))
	in := func(id uint64, user string, role domain.Role) domain.Member {
		return userRoom(joined(member(id, user, role)))
	}
	assertUserRooms(t, s.rooms, "bob", 0, 10, []domain.Member{in(roomA, "bob", domain.RoleMember)})
	alice := []domain.Member{in(roomA, "alice", domain.RoleOwner), in(roomB, "alice", domain.RoleOwner), in(roomB+1, "alice", domain.RoleOwner)}
	assertUserRooms(t, s.rooms, "alice", 0, 10, alice)
	assertUserRooms(t, s.rooms, "alice", 0, 2, alice[:2])
	assertUserRooms(t, s.rooms, "alice", roomB, 2, alice[2:])
	assertUserRooms(t, s.rooms, "alice", roomB+1, 2, nil)
	got, err := s.rooms.UserRooms(t.Context(), "other", "bob", 0, 10)
	assertMembers(t, "UserRooms(other, bob)", got, err, []domain.Member{userRoom(joined(foreignMembers[1]))})
}

func readMembersInvalid(t *testing.T, s memberStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	_, err := s.rooms.MembersOf(t.Context(), roomA, make([]string, domain.MaxMemberChanges+2))
	assertErrorIs(t, "MembersOf(too many users)", err, apperr.ErrInvalidArgument)
	for _, limit := range []int{0, store.MaxMemberScan + 1} {
		_, err = s.rooms.Owners(t.Context(), roomA, limit)
		assertErrorIs(t, "Owners(bad limit)", err, apperr.ErrInvalidArgument)
	}
	for name, q := range map[string]struct {
		tenant, user string
		limit        int
	}{
		"zero limit":      {tenant, "bob", 0},
		"limit above max": {tenant, "bob", store.MaxUserRoomsLimit + 1},
		"bad tenant":      {"Acme", "bob", 10},
		"bad user":        {tenant, "b b", 10},
	} {
		_, err = s.rooms.UserRooms(t.Context(), q.tenant, q.user, 0, q.limit)
		assertErrorIs(t, "UserRooms("+name+")", err, apperr.ErrInvalidArgument)
	}
}

func readMembersCancelled(t *testing.T, s memberStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	ctx := cancelledContext(t)
	_, err := s.rooms.MembersOf(ctx, roomA, []string{"bob"})
	assertErrorIs(t, "MembersOf", err, context.Canceled)
	_, _, err = s.rooms.Successor(ctx, roomA)
	assertErrorIs(t, "Successor", err, context.Canceled)
	_, err = s.rooms.Owners(ctx, roomA, 10)
	assertErrorIs(t, "Owners", err, context.Canceled)
	_, err = s.rooms.UserRooms(ctx, tenant, "bob", 0, 10)
	assertErrorIs(t, "UserRooms", err, context.Canceled)
}
```

`apps/core/internal/store/storetest/read_position_cases.go`:

```go
package storetest

import (
	"context"
	"math"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func readPositionCases() []memberCase {
	return []memberCase{
		{"mark read only raises and bumps the version", readMarkRead},
		{"mark unread only lowers and bumps the version", readMarkUnread},
		{"read positions of non members and out of range seqs are refused", readPositionRefused},
		{"read positions with a cancelled context", readPositionCancelled},
	}
}

type readStep struct {
	unread bool
	seq    uint64
	want   domain.ReadPos
	moved  bool
}

func runReadSteps(t *testing.T, s MemberRooms, user string, steps []readStep) {
	t.Helper()
	for _, st := range steps {
		move, name := s.MarkRead, "MarkRead"
		if st.unread {
			move, name = s.MarkUnread, "MarkUnread"
		}
		pos, moved, err := move(t.Context(), roomA, user, st.seq)
		if err != nil || moved != st.moved || pos != st.want {
			t.Fatalf("%s(%s, %d) = %+v, %v, %v; want %+v, %v", name, user, st.seq, pos, moved, err, st.want, st.moved)
		}
	}
}

func readMarkRead(t *testing.T, s memberStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	runReadSteps(t, s.rooms, "bob", []readStep{
		{seq: 5, want: domain.ReadPos{Seq: 5, Version: 2}, moved: true},
		{seq: 3, want: domain.ReadPos{Seq: 5, Version: 2}},
		{seq: 5, want: domain.ReadPos{Seq: 5, Version: 2}},
		{seq: 0, want: domain.ReadPos{Seq: 5, Version: 2}},
		{seq: 8, want: domain.ReadPos{Seq: 8, Version: 3}, moved: true},
	})
	bob := joined(members[1])
	bob.Read = domain.ReadPos{Seq: 8, Version: 3}
	assertMember(t, s.rooms, bob)
	assertMember(t, s.rooms, joined(members[0]))
}

func readMarkUnread(t *testing.T, s memberStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	runReadSteps(t, s.rooms, "bob", []readStep{
		{seq: 9, want: domain.ReadPos{Seq: 9, Version: 2}, moved: true},
		{unread: true, seq: 4, want: domain.ReadPos{Seq: 4, Version: 3}, moved: true},
		{unread: true, seq: 6, want: domain.ReadPos{Seq: 4, Version: 3}},
		{unread: true, seq: 4, want: domain.ReadPos{Seq: 4, Version: 3}},
		{unread: true, seq: 0, want: domain.ReadPos{Seq: 0, Version: 4}, moved: true},
	})
	runReadSteps(t, s.rooms, "alice", []readStep{{unread: true, seq: 0, want: domain.ReadPos{Version: 1}}})
}

func readPositionRefused(t *testing.T, s memberStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	for _, c := range []struct {
		room uint64
		user string
	}{{roomA, "carol"}, {roomB, "alice"}} {
		_, _, err := s.rooms.MarkRead(t.Context(), c.room, c.user, 1)
		assertErrorIs(t, "MarkRead(non member)", err, domain.ErrNotMember)
		_, _, err = s.rooms.MarkUnread(t.Context(), c.room, c.user, 0)
		assertErrorIs(t, "MarkUnread(non member)", err, domain.ErrNotMember)
	}
	_, _, err := s.rooms.MarkRead(t.Context(), roomA, "bob", math.MaxInt64+1)
	assertErrorIs(t, "MarkRead(seq above max int64)", err, apperr.ErrInvalidArgument)
	_, _, err = s.rooms.MarkUnread(t.Context(), roomA, "bob", math.MaxInt64+1)
	assertErrorIs(t, "MarkUnread(seq above max int64)", err, apperr.ErrInvalidArgument)
	assertNotMember(t, s.rooms, roomA, "carol")
	assertMember(t, s.rooms, joined(members[1]))
}

func readPositionCancelled(t *testing.T, s memberStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	ctx := cancelledContext(t)
	_, _, err := s.rooms.MarkRead(ctx, roomA, "bob", 5)
	assertErrorIs(t, "MarkRead", err, context.Canceled)
	_, _, err = s.rooms.MarkUnread(ctx, roomA, "bob", 0)
	assertErrorIs(t, "MarkUnread", err, context.Canceled)
	assertMember(t, s.rooms, joined(members[1]))
}
```

`apps/core/internal/store/memstore/memstore_test.go`, thêm vào cuối file:

```go

func TestMembersContract(t *testing.T) {
	storetest.RunMembers(t, func(*testing.T) (storetest.MemberRooms, store.MemberActions) {
		rooms := memstore.NewRooms()
		return rooms, rooms.MemberActions()
	})
}
```

**Step 5: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/..."`
Expected: package `storetest` biên dịch được; FAIL biên dịch ở `memstore_test.go`: `rooms.MemberActions undefined (type *memstore.Rooms has no field or method MemberActions)` và `cannot use rooms (variable of type *memstore.Rooms) as storetest.MemberRooms value in return statement: *memstore.Rooms does not implement storetest.MemberRooms (missing method AdvanceMembers)`.

**Step 6: Code memstore**

`apps/core/internal/store/memstore/rooms.go` (thay cả file):

```go
package memstore

import (
	"context"
	"fmt"
	"sync"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var (
	_ store.Rooms          = (*Rooms)(nil)
	_ store.HistoryClearer = (*Rooms)(nil)
)

type memberKey struct {
	room uint64
	user string
}

type Rooms struct {
	mu      sync.RWMutex
	rooms   map[uint64]domain.Room
	members map[memberKey]domain.Member
	pins    map[uint64]domain.PinState
	actions *MemberActions
	log     *Messages
}

func NewRooms() *Rooms {
	return &Rooms{
		rooms: make(map[uint64]domain.Room), members: make(map[memberKey]domain.Member),
		pins: make(map[uint64]domain.PinState), actions: newMemberActions(),
	}
}

func (s *Rooms) MemberActions() *MemberActions { return s.actions }

func (s *Rooms) Create(ctx context.Context, r domain.Room, members []domain.Member) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ValidateRoom(r, members); err != nil {
		return err
	}
	fact := domain.InitialMembers(r, members)
	if err := store.ValidateMemberAction(fact); err != nil {
		return err
	}
	if err := s.insertRoom(r, len(members)); err != nil {
		return err
	}
	if err := s.actions.Append(ctx, fact); err != nil {
		return fmt.Errorf("create room %d: %w", r.ID, err)
	}
	if err := s.ApplyMembers(ctx, fact); err != nil {
		return fmt.Errorf("create room %d: %w", r.ID, err)
	}
	if _, err := s.AdvanceMembers(ctx, r.ID, 0, fact.MV, fact.Count); err != nil {
		return fmt.Errorf("create room %d: %w", r.ID, err)
	}
	return nil
}

func (s *Rooms) insertRoom(r domain.Room, members int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rooms[r.ID]; ok {
		return fmt.Errorf("create room %d: %w", r.ID, store.ErrRoomExists)
	}
	r.MemberCount, r.MemberVersion = members, 0
	s.rooms[r.ID] = r
	if s.log != nil {
		s.log.appendFact(logged{kind: store.RoomInserted, room: r})
	}
	return nil
}

func (s *Rooms) Get(ctx context.Context, id uint64) (domain.Room, error) {
	if err := ctx.Err(); err != nil {
		return domain.Room{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.rooms[id]
	if !ok {
		return domain.Room{}, domain.ErrRoomNotFound
	}
	return r, nil
}

func (s *Rooms) Member(ctx context.Context, room uint64, user string) (domain.Member, error) {
	if err := ctx.Err(); err != nil {
		return domain.Member{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.members[memberKey{room, user}]
	if !ok || m.Removed {
		return domain.Member{}, domain.ErrNotMember
	}
	return m, nil
}

func (s *Rooms) ClearHistory(ctx context.Context, room uint64, user string, seq uint64) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := memberKey{room, user}
	m, ok := s.members[k]
	if !ok || m.Removed {
		return 0, domain.ErrNotMember
	}
	m.ClearedBeforeSeq = max(m.ClearedBeforeSeq, seq)
	s.members[k] = m
	return m.ClearedBeforeSeq, nil
}
```

`apps/core/internal/store/memstore/member_actions.go`:

```go
package memstore

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.MemberActions = (*MemberActions)(nil)

type MemberActions struct {
	mu    sync.RWMutex
	facts map[uint64][]domain.MemberAction
}

func newMemberActions() *MemberActions {
	return &MemberActions{facts: make(map[uint64][]domain.MemberAction)}
}

func (s *MemberActions) Append(ctx context.Context, a domain.MemberAction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ValidateMemberAction(a); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	line := s.facts[a.Room]
	i, found := slices.BinarySearchFunc(line, a.MV, byMV)
	if found {
		return fmt.Errorf("append member v%d of room %d: %w", a.MV, a.Room, store.ErrMemberActionExists)
	}
	s.facts[a.Room] = slices.Insert(line, i, cloneAction(a))
	return nil
}

func (s *MemberActions) At(ctx context.Context, room, mv uint64) (domain.MemberAction, error) {
	if err := ctx.Err(); err != nil {
		return domain.MemberAction{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	line := s.facts[room]
	if i, ok := slices.BinarySearchFunc(line, mv, byMV); ok {
		return cloneAction(line[i]), nil
	}
	return domain.MemberAction{}, fmt.Errorf("member v%d of room %d: %w", mv, room, store.ErrMemberActionNotFound)
}

func (s *MemberActions) After(ctx context.Context, room, mv uint64, limit int) ([]domain.MemberAction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateLimit(limit, store.MaxMemberScan); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	line := s.facts[room]
	i, found := slices.BinarySearchFunc(line, mv, byMV)
	if found {
		i++
	}
	return cloneActions(line[i:min(len(line), i+limit)]), nil
}

func (s *MemberActions) Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.MemberAction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateLimit(limit, store.MaxMemberScan); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.MemberAction{}
	for _, a := range s.facts[room] {
		if !a.At.Before(from) && !a.At.After(to) {
			out = append(out, cloneAction(a))
		}
	}
	slices.SortFunc(out, func(a, b domain.MemberAction) int { return cmp.Or(a.At.Compare(b.At), cmp.Compare(a.MV, b.MV)) })
	return out[:min(len(out), limit)], nil
}

func byMV(a domain.MemberAction, mv uint64) int { return cmp.Compare(a.MV, mv) }

func cloneAction(a domain.MemberAction) domain.MemberAction {
	a.Changes = slices.Clone(a.Changes)
	return a
}

func cloneActions(acts []domain.MemberAction) []domain.MemberAction {
	out := make([]domain.MemberAction, len(acts))
	for i, a := range acts {
		out[i] = cloneAction(a)
	}
	return out
}
```

`apps/core/internal/store/memstore/member_projection.go`:

```go
package memstore

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.MemberProjector = (*Rooms)(nil)

func (s *Rooms) ApplyMembers(ctx context.Context, a domain.MemberAction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ValidateMemberAction(a); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range touchedUsers(a) {
		k := memberKey{a.Room, user}
		cur, ok := s.members[k]
		if ok && cur.MV >= a.MV {
			continue
		}
		s.members[k] = a.Applied(cur, user)
	}
	return nil
}

func (s *Rooms) AdvanceMembers(ctx context.Context, room, base, mv uint64, count int) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := store.ValidateMemberAdvance(base, mv, count); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[room]
	if !ok || r.MemberVersion != base {
		return false, nil
	}
	r.MemberVersion, r.MemberCount = mv, count
	s.rooms[room] = r
	return true, nil
}

func touchedUsers(a domain.MemberAction) []string {
	users := make([]string, 0, len(a.Changes)+1)
	for _, c := range a.Changes {
		users = append(users, c.User)
	}
	if a.Successor != "" && !slices.Contains(users, a.Successor) {
		users = append(users, a.Successor)
	}
	return users
}
```

`apps/core/internal/store/memstore/member_reads.go`:

```go
package memstore

import (
	"cmp"
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.MemberReader = (*Rooms)(nil)

func (s *Rooms) MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateMemberUsers(users); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.Member{}
	for _, u := range users {
		if m, ok := s.members[memberKey{room, u}]; ok {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, byUser)
	return slices.CompactFunc(out, func(a, b domain.Member) bool { return a.User == b.User }), nil
}

func (s *Rooms) Successor(ctx context.Context, room uint64) (domain.Member, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.Member{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := domain.Successor(s.activeLocked(func(m domain.Member) bool { return m.Room == room }))
	return m, ok, nil
}

func (s *Rooms) Owners(ctx context.Context, room uint64, limit int) ([]domain.Member, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateLimit(limit, store.MaxMemberScan); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	owners := s.activeLocked(func(m domain.Member) bool { return m.Room == room && m.Role == domain.RoleOwner })
	slices.SortFunc(owners, func(a, b domain.Member) int { return cmp.Or(a.JoinedAt.Compare(b.JoinedAt), byUser(a, b)) })
	return owners[:min(len(owners), limit)], nil
}

func (s *Rooms) UserRooms(ctx context.Context, tenant, user string, after uint64, limit int) ([]domain.Member, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateUserRoomsQuery(tenant, user, limit); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	rooms := s.activeLocked(func(m domain.Member) bool { return m.Tenant == tenant && m.User == user && m.Room > after })
	slices.SortFunc(rooms, func(a, b domain.Member) int { return cmp.Compare(a.Room, b.Room) })
	out := make([]domain.Member, 0, min(len(rooms), limit))
	for _, m := range rooms[:min(len(rooms), limit)] {
		out = append(out, domain.Member{Room: m.Room, Tenant: m.Tenant, User: m.User, Role: m.Role, JoinedAt: m.JoinedAt, MV: m.MV})
	}
	return out, nil
}

func (s *Rooms) activeLocked(keep func(domain.Member) bool) []domain.Member {
	out := []domain.Member{}
	for _, m := range s.members {
		if !m.Removed && keep(m) {
			out = append(out, m)
		}
	}
	return out
}

func byUser(a, b domain.Member) int { return cmp.Compare(a.User, b.User) }
```

`apps/core/internal/store/memstore/read_positions.go`:

```go
package memstore

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.ReadPositions = (*Rooms)(nil)

func (s *Rooms) MarkRead(ctx context.Context, room uint64, user string, seq uint64) (domain.ReadPos, bool, error) {
	return s.moveRead(ctx, room, user, seq, func(at uint64) bool { return at < seq })
}

func (s *Rooms) MarkUnread(ctx context.Context, room uint64, user string, to uint64) (domain.ReadPos, bool, error) {
	return s.moveRead(ctx, room, user, to, func(at uint64) bool { return at > to })
}

func (s *Rooms) moveRead(ctx context.Context, room uint64, user string, to uint64, moves func(uint64) bool) (domain.ReadPos, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.ReadPos{}, false, err
	}
	if err := store.ValidateReadSeq(to); err != nil {
		return domain.ReadPos{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := memberKey{room, user}
	m, ok := s.members[k]
	switch {
	case !ok || m.Removed:
		return domain.ReadPos{}, false, domain.ErrNotMember
	case !moves(m.Read.Seq):
		return m.Read, false, nil
	}
	m.Read = domain.ReadPos{Seq: to, Version: m.Read.Version + 1}
	s.members[k] = m
	return m.Read, true, nil
}
```

**Step 7: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/store/..."`
Expected: PASS (mongostore integration skip). `-count=5` vì có lock mới. `TestContract` (Rooms: create/existing/membership với `created`/`joined`), `TestEditsContract` (`clearForward`, `editsCancelled`), `TestPinsContract` (`pinStateRoomUnchanged`) và `TestMembersContract` (23 subtest) đều xanh.

Run: `make vet && make test`
Expected: vet sạch (`mongostore.Store` và mọi fake của `store.Rooms` biên dịch không đổi; `mongostore` chưa cài port mới, chưa ai đòi); `make test` PASS toàn repo: mọi test dựng room qua memstore (actor, access, grpcsrv, mutate, effects, reconcile, resync, pinproj) đi đường `Create` mới mà không đổi kỳ vọng (không test nào so nguyên `Room`/`Member` đọc lại từ memstore ngoài storetest). Test đỏ ở package khác vì so sánh nguyên struct → sửa cùng cách (`created`/`joined` hoặc so field) nếu chỉ là phép so sánh, còn lại dừng và báo cáo.

`wc -l` các file mới/sửa: `store/member.go` 41, `store/member_validate.go` 94, `store/member_test.go` 87, `store/write_contract_test.go` 107; `memstore/rooms.go` 120, `memstore/member_actions.go` 106, `memstore/member_projection.go` 60, `memstore/member_reads.go` 85, `memstore/read_positions.go` 40, `memstore/memstore_test.go` 68; `storetest/members.go` 131, `storetest/member_fact_cases.go` 152, `storetest/member_projection_cases.go` 148, `storetest/member_read_cases.go` 143, `storetest/read_position_cases.go` 100, `storetest/rooms_cases.go` 176; `work/record.go` 98, `work/record_test.go` 173. Mọi file < 200.

Không chạy `make itest` ở task này: `TestMongoStoreContract`, `TestMongoEditsContract`, `TestMongoPinsContract` sẽ đỏ tới Task 5 (Mongo `Create` chưa ghi fact nên `Get`/`Member` chưa có `MemberVersion`/`MV`/`Read` mà `created`/`joined` chờ).

**Step 8: Commit**

INDEXES.csv:
- dòng `apps/core/internal/store`: trong cột purpose thay `PinProjector (PinState: ErrRoomNotFound or empty; ApplyPins CAS on pv);` bằng `PinProjector (PinState: ErrRoomNotFound or empty; ApplyPins CAS on pv); member ports MemberActions (Append insert-unique by room|mv -> ErrMemberActionExists; At -> ErrMemberActionNotFound; After ascending up to MaxMemberScan; Between), MemberProjector (ApplyMembers writes domain.MemberAction.Applied to every touched members and user_rooms doc only while its mv is below the fact, never touches rooms; AdvanceMembers CAS of rooms.mv/mc, base 0 = missing), MemberReader (MembersOf with removed docs, sorted by user; Successor; Owners by join time then user; UserRooms up to MaxUserRoomsLimit, user_rooms fields only) and ReadPositions (MarkRead only raises, MarkUnread only lowers, each move bumps the version; removed or missing -> ErrNotMember); Rooms.Create = room, then fact 1 (domain.InitialMembers), then ApplyMembers, then AdvanceMembers 0 -> 1 in every adapter; Rooms.Member/ClearHistory see only active members; ValidateMemberAction (also read seq <= MaxInt64), ValidateMemberAdvance, ValidateMemberUsers, ValidateUserRoomsQuery, ValidateReadSeq;` và thay `ReactionChanged with Reaction or PinInserted with Pin;` bằng `ReactionChanged with Reaction, PinInserted with Pin or MemberInserted with Member;`; trong cột key_symbols thay `Pins;PinProjector;` bằng `Pins;PinProjector;MemberActions;MemberProjector;MemberReader;ReadPositions;ErrMemberActionExists;ErrMemberActionNotFound;MaxMemberScan;MaxUserRoomsLimit;ValidateMemberAction;ValidateMemberAdvance;ValidateMemberUsers;ValidateUserRoomsQuery;ValidateReadSeq;` và `ReactionChanged;PinInserted;` bằng `ReactionChanged;PinInserted;MemberInserted;`; cột decisions thay `D10;D30;D52;D62;D69;D72;D88;D89;D90;D92` bằng `D10;D30;D52;D62;D69;D72;D88;D89;D90;D92;D96;D97;D98;D101;D104`.
- dòng `apps/core/internal/store/memstore`: trong cột purpose thay `Rooms.PinState/ApplyPins (pin state beside the room, CAS on pv);` bằng `Rooms.PinState/ApplyPins (pin state beside the room, CAS on pv); member facts in Rooms.MemberActions() (facts per room sorted by mv); Rooms.Create through fact 1 + ApplyMembers + AdvanceMembers; ApplyMembers (domain.MemberAction.Applied per touched user, guarded by the member's mv), AdvanceMembers (CAS on the room head), MembersOf/Successor/Owners/UserRooms (user rooms derived from the members map, without read and clear state), MarkRead/MarkUnread; removed members stay as tombstones;`; trong cột key_symbols thay `Reactions;Pins;` bằng `Reactions;Pins;MemberActions;Rooms.MemberActions;`; cột decisions thay `D52` bằng `D52;D96;D97;D104`.
- dòng `apps/core/internal/store/storetest`: trong cột purpose thay `room activity cases (monotonic, thread, missing room, active range/tenant/paging)` bằng `room activity cases (monotonic, thread, missing room, active range/tenant/paging); members (RunMembers over MemberRooms + MemberActions: facts append/at/after/between, existing version, invalid facts and limits, Create writes fact 1 and moves the head to 1, apply guarded by mv, tombstone and re-add with the read position raised and clear kept, successor, advance CAS, MembersOf/Successor/Owners/UserRooms, MarkRead/MarkUnread, cancelled context); expectations after Create carry head 1, member version 1 and read version 1 (created/joined)`; trong cột key_symbols thay `PinnableRooms` (cuối cột) bằng `PinnableRooms;RunMembers;MemberRooms`; cột decisions thay `D52;D88;D89;D90;D92` bằng `D52;D88;D89;D90;D92;D96;D97;D101;D104`.
- dòng `apps/core/internal/work`: trong cột purpose thay `PinInserted carries pv as seq;` bằng `PinInserted carries pv as seq; RecordOf maps MemberInserted to room + mv as seq (KnownKind and the g: id come with the feed);`.

```bash
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
make fmt-check && make vet && make lint
git add apps/core/internal/store/member.go apps/core/internal/store/member_validate.go apps/core/internal/store/member_test.go apps/core/internal/store/memstore/member_actions.go apps/core/internal/store/memstore/member_projection.go apps/core/internal/store/memstore/member_reads.go apps/core/internal/store/memstore/read_positions.go apps/core/internal/store/storetest/members.go apps/core/internal/store/storetest/member_fact_cases.go apps/core/internal/store/storetest/member_projection_cases.go apps/core/internal/store/storetest/member_read_cases.go apps/core/internal/store/storetest/read_position_cases.go
git commit -m "feat(store): add member fact, projection and read position ports" -- apps/core/internal/store/member.go apps/core/internal/store/member_validate.go apps/core/internal/store/member_test.go apps/core/internal/store/feed.go apps/core/internal/store/reaction_test.go apps/core/internal/store/write_contract_test.go apps/core/internal/store/memstore/ apps/core/internal/store/storetest/ apps/core/internal/work/record.go apps/core/internal/work/record_test.go INDEXES.csv
```

Expected: CSV in `{7}`; `git show --stat HEAD` chỉ có file `store`, `memstore`, `storetest`, `work` và `INDEXES.csv` của task.

---

### Task 5: ★ mongostore — `member_actions` + `user_rooms` clustered, index member, `member_codec.go`, `Create` mới, lọc removed, bốn port + itest contract

Mongo cài đúng ngữ nghĩa memstore của Task 4 và chạy chung `storetest.RunMembers`. Điểm chính:
- `member_actions` (clustered, `_id = keys.MemberAction(room, mv)`, index `{r: 1, ts: 1}` cho `Between`/resync) và `user_rooms` (clustered, `_id = keys.UserRoom(t, u, r)`, không index phụ) tạo trong `Bootstrap`. `members` thêm index `{r, st, ro, ja, u}` cho `Successor`/`Owners`.
- `*Store` **không** cài `store.MemberActions` (trùng `Append/At/Between` của `Edits`): kiểu con `mongostore.MemberActions` qua `Store.MemberActions()`, như `Pins` (GAP-1 của M2b.3). `*Store` cài `MemberProjector`, `MemberReader`, `ReadPositions`.
- `ApplyMembers` = hai `BulkWrite(ordered: false)` (`members` rồi `user_rooms`), mỗi model là `UpdateOne` upsert với pipeline `$set` và filter có guard `mv: {$not: {$gte: k}}` (thiếu `mv` = 0). Doc đã có `mv ≥ k`: filter không khớp, upsert chèn doc mới trùng khoá (`{r, u}` unique của `members`, `_id` của `user_rooms`) → lỗi trùng khoá = "fact mới hơn đã áp", bỏ qua (`onlyDuplicateKeys`). Filter không phải phép bằng thuần trên khoá unique nên server **không** tự retry upsert; đó là điều ta muốn. Hai projector cùng chèn một doc thiếu chỉ xảy ra với **cùng** fact (projector áp fact k+1 đã áp mọi fact ≤ k của user đó trước, vì đầu `rooms.mv` chỉ tiến sau khi mọi doc đã áp).
- Pipeline theo `domain.MemberAction.Applied`: add → `ro`, `ja = ts fact`, `st: "$$REMOVE"`, `rs.s = $max(ifNull(rs.s, 0), readSeq)`, `rs.v = ifNull(rs.v, 0) + 1` (chỉ `members`); remove/leave → `st: 1`; role → `ro`; successor → `ro: owner`; mọi model đặt `t`, `mv` (và `u`, `r` cho `user_rooms`). Số luôn là `int64`.
- `members.st` chỉ ghi `1` khi removed; active = thiếu field, query `st: null` (null khớp field thiếu). Doc cũ không có `st` là active. `Member`, `ClearHistory`, `MarkRead/MarkUnread` lọc active.
- `MarkRead/MarkUnread` = một `FindOneAndUpdate` trên member active với `rs.s: {$not: {$gte: seq}}` (thiếu `rs` = 0) / `rs.s: {$gt: to}`, pipeline `rs = {s, v + 1}`, `ReturnDocument After`; không khớp → đọc lại bằng `Member` (không active → `ErrNotMember`, có → vị trí hiện tại, `false`).
- `UserRooms` đọc khoảng `_id` (`$gt UserRoom(t, u, after)`, `$lte UserRoom(t, u, MaxUint64)`) với `st: null`. BSON so BinData theo độ dài trước rồi mới tới byte, nên thứ tự byte của D101 chỉ đúng giữa các khoá cùng độ dài; mọi khoá của một (tenant, user) cùng độ dài nên khoảng này đúng và không lẫn user khác (ghi vào D101 ở Task 19).
- `codec.go` (187 dòng) chuyển `memberDoc`/`decodeMember` sang file mới `member_codec.go` (cùng codec `user_rooms` và `rs`) và thêm `roomDoc.MemberVersion` (`mv`, omitempty). Codec fact để ở file riêng `member_action_codec.go` (gộp một file sẽ sát 200 dòng). `encodeMember` bỏ hẳn: `Create` ghi member qua `ApplyMembers`.

Thêm ngoài hợp đồng: không có (dùng các validator Task 4 đã thêm).

**Files:**
- Create: `apps/core/internal/store/mongostore/member_codec.go`, `member_action_codec.go`, `member_codec_test.go`
- Modify: `apps/core/internal/store/mongostore/codec.go`, `codec_test.go`, `edit_codec_test.go`
- Create: `apps/core/internal/store/mongostore/member_actions.go`, `member_projection.go`, `member_updates.go`, `member_reads.go`, `read_positions.go`
- Modify: `apps/core/internal/store/mongostore/mongostore.go`, `rooms.go`, `bootstrap.go`
- Create: `apps/core/internal/store/mongostore/members_integration_test.go`, `bootstrap_members_integration_test.go`
- Modify: `INDEXES.csv` (dòng `apps/core/internal/store/mongostore`)

**Step 1: Test codec (unit)**

`apps/core/internal/store/mongostore/codec_test.go`:
- xoá cả hàm `TestMemberCodecRoundTrip` (và dòng trống trước nó);
- trong `TestRoomIDAboveMaxInt64IsRejected` xoá ba dòng:

```go
	if _, err := decodeMember(memberDoc{Room: -1}); err == nil {
		t.Fatal("decodeMember accepted a negative room")
	}
```

`apps/core/internal/store/mongostore/edit_codec_test.go`: xoá cả hàm `TestMemberCodecReadsClearedBefore` (và dòng trống trước nó). Hai test này chuyển sang `member_codec_test.go` dưới dạng "negative room", "negative cleared" và round trip có `cb`.

`apps/core/internal/store/mongostore/member_codec_test.go`:

```go
package mongostore

import (
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func sampleLeave() domain.MemberAction {
	return domain.MemberAction{
		Room: 7_340_000_001, MV: 3, Tenant: "acme", Op: domain.MemberOpLeave, By: "alice", At: codecTime, Count: 2, Successor: "bob",
		Changes: []domain.MemberChange{{User: "alice", Prev: domain.RoleOwner}},
	}
}

func TestMemberActionCodecRoundTrip(t *testing.T) {
	add := domain.MemberAction{
		Room: 7_340_000_001, MV: 2, Tenant: "acme", Op: domain.MemberOpAdd, By: "alice", At: codecTime, Count: 3,
		Changes: []domain.MemberChange{{User: "bob", Role: domain.RoleMember, ReadSeq: 42}, {User: "carol", Role: domain.RoleAdmin}},
	}
	for _, a := range []domain.MemberAction{add, sampleLeave()} {
		doc, err := encodeMemberAction(a)
		if err != nil {
			t.Fatalf("encodeMemberAction(v%d): %v", a.MV, err)
		}
		back, raw := roundTrip(t, doc)
		want := []string{"_id", "r", "t", "op", "ch", "by", "ts", "n"}
		if a.Successor != "" {
			want = append(want, "so")
		}
		if got := fieldNames(t, raw); !slices.Equal(got, want) {
			t.Fatalf("fields of v%d = %v, want %v", a.MV, got, want)
		}
		if a.Op == domain.MemberOpAdd {
			if got := fieldNames(t, raw.Lookup("ch", "0").Document()); !slices.Equal(got, []string{"u", "ro", "rs"}) {
				t.Fatalf("first change fields = %v, want u ro rs", got)
			}
			if got := fieldNames(t, raw.Lookup("ch", "1").Document()); !slices.Equal(got, []string{"u", "ro"}) {
				t.Fatalf("second change fields = %v, want u ro", got)
			}
		}
		got, err := decodeMemberAction(back)
		if err != nil || !reflect.DeepEqual(got, a) {
			t.Fatalf("decodeMemberAction = %+v, %v; want %+v", got, err, a)
		}
	}
}

func TestMemberActionCodecRejectsBadFactsAndCorruptDocs(t *testing.T) {
	bad := sampleLeave()
	bad.MV = 0
	if _, err := encodeMemberAction(bad); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("encodeMemberAction(version 0) = %v, want ErrInvalidArgument", err)
	}
	good, err := encodeMemberAction(sampleLeave())
	if err != nil {
		t.Fatalf("encodeMemberAction: %v", err)
	}
	for name, mutate := range map[string]func(*memberActionDoc){
		"short id":          func(d *memberActionDoc) { d.ID = d.ID[:8] },
		"unknown op":        func(d *memberActionDoc) { d.Op = 5 },
		"negative count":    func(d *memberActionDoc) { d.Count = -1 },
		"negative read seq": func(d *memberActionDoc) { d.Changes = []memberChangeDoc{{User: "bob", ReadSeq: -1}} },
	} {
		d := good
		mutate(&d)
		if _, err := decodeMemberAction(d); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: decodeMemberAction = %v, want errCorrupt", name, err)
		}
	}
}

func TestMemberCodecReadsStateVersionAndReadPosition(t *testing.T) {
	d := memberDoc{
		Room: 7_340_000_001, User: "bob", Tenant: "acme", Role: domain.RoleMember, JoinedAt: codecTime,
		ClearedBefore: 42, State: memberRemoved, MV: 3, Read: &readDoc{Seq: 9, Version: 4},
	}
	back, raw := roundTrip(t, d)
	if got, want := fieldNames(t, raw), []string{"r", "u", "t", "ro", "ja", "cb", "st", "mv", "rs"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	want := domain.Member{
		Room: 7_340_000_001, Tenant: "acme", User: "bob", Role: domain.RoleMember, JoinedAt: codecTime,
		ClearedBeforeSeq: 42, Removed: true, MV: 3, Read: domain.ReadPos{Seq: 9, Version: 4},
	}
	got, err := decodeMember(back)
	if err != nil || !got.JoinedAt.Equal(want.JoinedAt) {
		t.Fatalf("decodeMember = %+v, %v; want %+v", got, err, want)
	}
	got.JoinedAt = want.JoinedAt
	if got != want {
		t.Fatalf("decoded %+v, want %+v", got, want)
	}
	active := memberDoc{Room: 7_340_000_001, User: "bob", Tenant: "acme", Role: domain.RoleMember, JoinedAt: codecTime}
	if _, raw := roundTrip(t, active); !slices.Equal(fieldNames(t, raw), []string{"r", "u", "t", "ro", "ja"}) {
		t.Fatalf("active member fields = %v, want r u t ro ja", fieldNames(t, raw))
	}
	if m, err := decodeMember(active); err != nil || m.Removed || m.MV != 0 || m.Read != (domain.ReadPos{}) {
		t.Fatalf("decodeMember(doc without st, mv, rs) = %+v, %v; want an active member at version 0", m, err)
	}
}

func TestMemberCodecRejectsCorruptDocs(t *testing.T) {
	for name, mutate := range map[string]func(*memberDoc){
		"negative room":         func(d *memberDoc) { d.Room = -1 },
		"negative cleared":      func(d *memberDoc) { d.ClearedBefore = -1 },
		"unknown state":         func(d *memberDoc) { d.State = 2 },
		"negative version":      func(d *memberDoc) { d.MV = -1 },
		"negative read seq":     func(d *memberDoc) { d.Read = &readDoc{Seq: -1} },
		"negative read version": func(d *memberDoc) { d.Read = &readDoc{Version: -1} },
	} {
		d := memberDoc{Room: 7_340_000_001, User: "bob", Tenant: "acme", Role: domain.RoleMember, JoinedAt: codecTime}
		mutate(&d)
		if _, err := decodeMember(d); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: decodeMember = %v, want errCorrupt", name, err)
		}
	}
}

func TestUserRoomCodec(t *testing.T) {
	good := userRoomDoc{
		ID: keys.UserRoom("acme", "bob", 7_340_000_001), Tenant: "acme", User: "bob", Room: 7_340_000_001,
		Role: domain.RoleAdmin, JoinedAt: codecTime, MV: 4,
	}
	if _, raw := roundTrip(t, good); !slices.Equal(fieldNames(t, raw), []string{"_id", "t", "u", "r", "ro", "ja", "mv"}) {
		t.Fatalf("user room fields = %v, want _id t u r ro ja mv", fieldNames(t, raw))
	}
	want := domain.Member{Room: 7_340_000_001, Tenant: "acme", User: "bob", Role: domain.RoleAdmin, JoinedAt: codecTime, MV: 4}
	if got, err := decodeUserRoom(good); err != nil || got != want {
		t.Fatalf("decodeUserRoom = %+v, %v; want %+v", got, err, want)
	}
	for name, mutate := range map[string]func(*userRoomDoc){
		"bad id":           func(d *userRoomDoc) { d.ID = []byte("acme") },
		"negative version": func(d *userRoomDoc) { d.MV = -1 },
		"unknown state":    func(d *userRoomDoc) { d.State = 3 },
	} {
		d := good
		mutate(&d)
		if _, err := decodeUserRoom(d); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: decodeUserRoom = %v, want errCorrupt", name, err)
		}
	}
}

func TestRoomCodecReadsTheMemberHead(t *testing.T) {
	d := roomDoc{ID: 7_340_000_001, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: codecTime, MemberCount: 3, MemberVersion: 5}
	if got, err := decodeRoom(d); err != nil || got.MemberVersion != 5 || got.MemberCount != 3 {
		t.Fatalf("decodeRoom = %+v, %v; want member version 5 and 3 members", got, err)
	}
	d.MemberVersion = -1
	if _, err := decodeRoom(d); !errors.Is(err, errCorrupt) {
		t.Fatalf("decodeRoom(negative member version) = %v, want errCorrupt", err)
	}
	big := sampleLeave()
	big.Op, big.Successor = domain.MemberOpAdd, ""
	big.Changes = []domain.MemberChange{{User: "bob", Role: domain.RoleMember, ReadSeq: math.MaxInt64 + 1}}
	if _, err := encodeMemberAction(big); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("encodeMemberAction(read seq above max int64) = %v, want ErrInvalidArgument", err)
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/mongostore/..."`
Expected: FAIL biên dịch: `undefined: encodeMemberAction`, `undefined: decodeMemberAction`, `undefined: memberActionDoc`, `undefined: memberChangeDoc`, `unknown field State in struct literal of type memberDoc`, `unknown field MV in struct literal of type memberDoc`, `unknown field Read in struct literal of type memberDoc`, `undefined: readDoc`, `undefined: memberRemoved`, `undefined: userRoomDoc` (Go dừng sau 10 lỗi với `too many errors`).

**Step 3: Code codec**

`apps/core/internal/store/mongostore/codec.go`:
- xoá cả khối `type memberDoc struct { … }` (và dòng trống trước nó), xoá hai hàm `encodeMember` và `decodeMember` (và dòng trống trước mỗi hàm);
- trong `type roomDoc struct` thay:

```go
	LastChangeAt time.Time       `bson:"lc,omitempty"`
}
```

bằng:

```go
	LastChangeAt  time.Time       `bson:"lc,omitempty"`
	MemberVersion int64           `bson:"mv,omitempty"`
}
```

(gofmt căn lại cột của cả struct.)

- trong `decodeRoom` thay:

```go
	lastSeq, err := toUint64("room last seq", d.LastSeq)
	if err != nil {
		return domain.Room{}, err
	}
```

bằng:

```go
	lastSeq, err := toUint64("room last seq", d.LastSeq)
	if err != nil {
		return domain.Room{}, err
	}
	memberVersion, err := toUint64("room member version", d.MemberVersion)
	if err != nil {
		return domain.Room{}, err
	}
```

- trong literal trả về của `decodeRoom` thay `		LastChangeAt: d.LastChangeAt,` bằng:

```go
		LastChangeAt:  d.LastChangeAt,
		MemberVersion: memberVersion,
```

(gofmt căn lại cột của literal.)

`apps/core/internal/store/mongostore/member_codec.go`:

```go
package mongostore

import (
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

const memberRemoved int32 = 1

type memberDoc struct {
	Room          int64       `bson:"r"`
	User          string      `bson:"u"`
	Tenant        string      `bson:"t"`
	Role          domain.Role `bson:"ro"`
	JoinedAt      time.Time   `bson:"ja"`
	ClearedBefore int64       `bson:"cb,omitempty"`
	State         int32       `bson:"st,omitempty"`
	MV            int64       `bson:"mv,omitempty"`
	Read          *readDoc    `bson:"rs,omitempty"`
}

type readDoc struct {
	Seq     int64 `bson:"s"`
	Version int64 `bson:"v"`
}

type userRoomDoc struct {
	ID       []byte      `bson:"_id"`
	Tenant   string      `bson:"t"`
	User     string      `bson:"u"`
	Room     int64       `bson:"r"`
	Role     domain.Role `bson:"ro"`
	JoinedAt time.Time   `bson:"ja"`
	MV       int64       `bson:"mv"`
	State    int32       `bson:"st,omitempty"`
}

func decodeMember(d memberDoc) (domain.Member, error) {
	room, roomErr := toUint64("member room", d.Room)
	cleared, clearedErr := toUint64("member cleared before seq", d.ClearedBefore)
	mv, mvErr := toUint64("member version", d.MV)
	removed, stateErr := decodeMemberState(d.State)
	read, readErr := decodeReadPos(d.Read)
	if err := firstErr(roomErr, clearedErr, mvErr, stateErr, readErr); err != nil {
		return domain.Member{}, err
	}
	return domain.Member{
		Room: room, Tenant: d.Tenant, User: d.User, Role: d.Role, JoinedAt: d.JoinedAt,
		ClearedBeforeSeq: cleared, Removed: removed, MV: mv, Read: read,
	}, nil
}

func decodeMembers(docs []memberDoc) ([]domain.Member, error) {
	out := make([]domain.Member, len(docs))
	for i, d := range docs {
		m, err := decodeMember(d)
		if err != nil {
			return nil, err
		}
		out[i] = m
	}
	return out, nil
}

func decodeReadPos(d *readDoc) (domain.ReadPos, error) {
	if d == nil {
		return domain.ReadPos{}, nil
	}
	seq, seqErr := toUint64("read seq", d.Seq)
	version, versionErr := toUint64("read version", d.Version)
	if err := firstErr(seqErr, versionErr); err != nil {
		return domain.ReadPos{}, err
	}
	return domain.ReadPos{Seq: seq, Version: version}, nil
}

func decodeMemberState(st int32) (bool, error) {
	switch st {
	case 0:
		return false, nil
	case memberRemoved:
		return true, nil
	default:
		return false, fmt.Errorf("%w: member state %d", errCorrupt, st)
	}
}

func decodeUserRoom(d userRoomDoc) (domain.Member, error) {
	tenant, user, room, err := keys.ParseUserRoom(d.ID)
	if err != nil {
		return domain.Member{}, fmt.Errorf("%w: user room _id: %w", errCorrupt, err)
	}
	mv, mvErr := toUint64("user room member version", d.MV)
	removed, stateErr := decodeMemberState(d.State)
	if err := firstErr(mvErr, stateErr); err != nil {
		return domain.Member{}, err
	}
	return domain.Member{Room: room, Tenant: tenant, User: user, Role: d.Role, JoinedAt: d.JoinedAt, Removed: removed, MV: mv}, nil
}

func decodeUserRooms(docs []userRoomDoc) ([]domain.Member, error) {
	out := make([]domain.Member, len(docs))
	for i, d := range docs {
		m, err := decodeUserRoom(d)
		if err != nil {
			return nil, err
		}
		out[i] = m
	}
	return out, nil
}
```

`apps/core/internal/store/mongostore/member_action_codec.go`:

```go
package mongostore

import (
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type memberActionDoc struct {
	ID        []byte            `bson:"_id"`
	Room      int64             `bson:"r"`
	Tenant    string            `bson:"t"`
	Op        int32             `bson:"op"`
	Changes   []memberChangeDoc `bson:"ch"`
	By        string            `bson:"by"`
	At        time.Time         `bson:"ts"`
	Count     int64             `bson:"n"`
	Successor string            `bson:"so,omitempty"`
}

type memberChangeDoc struct {
	User    string      `bson:"u"`
	Role    domain.Role `bson:"ro,omitempty"`
	Prev    domain.Role `bson:"pr,omitempty"`
	ReadSeq int64       `bson:"rs,omitempty"`
}

var memberOps = []domain.MemberOp{domain.MemberOpAdd, domain.MemberOpRemove, domain.MemberOpLeave, domain.MemberOpRole}

func encodeMemberAction(a domain.MemberAction) (memberActionDoc, error) {
	if err := store.ValidateMemberAction(a); err != nil {
		return memberActionDoc{}, err
	}
	room, err := toInt64("room id", a.Room)
	if err != nil {
		return memberActionDoc{}, err
	}
	changes := make([]memberChangeDoc, len(a.Changes))
	for i, c := range a.Changes {
		seq, err := toInt64("read seq", c.ReadSeq)
		if err != nil {
			return memberActionDoc{}, err
		}
		changes[i] = memberChangeDoc{User: c.User, Role: c.Role, Prev: c.Prev, ReadSeq: seq}
	}
	return memberActionDoc{
		ID: keys.MemberAction(a.Room, a.MV), Room: room, Tenant: a.Tenant, Op: int32(a.Op), Changes: changes,
		By: a.By, At: a.At, Count: int64(a.Count), Successor: a.Successor,
	}, nil
}

func decodeMemberAction(d memberActionDoc) (domain.MemberAction, error) {
	room, mv, err := keys.ParseMemberAction(d.ID)
	if err != nil {
		return domain.MemberAction{}, fmt.Errorf("%w: member action _id: %w", errCorrupt, err)
	}
	op, err := decodeMemberOp(d.Op)
	if err != nil {
		return domain.MemberAction{}, err
	}
	if d.Count < 0 {
		return domain.MemberAction{}, fmt.Errorf("%w: negative member count", errCorrupt)
	}
	changes := make([]domain.MemberChange, len(d.Changes))
	for i, c := range d.Changes {
		seq, err := toUint64("member change read seq", c.ReadSeq)
		if err != nil {
			return domain.MemberAction{}, err
		}
		changes[i] = domain.MemberChange{User: c.User, Role: c.Role, Prev: c.Prev, ReadSeq: seq}
	}
	return domain.MemberAction{
		Room: room, MV: mv, Tenant: d.Tenant, Op: op, Changes: changes,
		By: d.By, At: d.At, Count: int(d.Count), Successor: d.Successor,
	}, nil
}

func decodeMemberOp(op int32) (domain.MemberOp, error) {
	for _, known := range memberOps {
		if op == int32(known) {
			return known, nil
		}
	}
	return 0, fmt.Errorf("%w: member op %d", errCorrupt, op)
}

func decodeMemberActions(docs []memberActionDoc) ([]domain.MemberAction, error) {
	out := make([]domain.MemberAction, len(docs))
	for i, d := range docs {
		a, err := decodeMemberAction(d)
		if err != nil {
			return nil, err
		}
		out[i] = a
	}
	return out, nil
}
```

`int(d.Count)` chỉ chạy sau khi đã chặn âm; `int` là 64 bit trên mọi nền tảng build của repo. `make lint` báo G115 ở đây → dừng, báo controller (không thêm `//nolint`).

**Step 4: Kiểm codec đã đủ**

Run: `make -s go ARGS="vet ./apps/core/internal/store/mongostore/"`
Expected: FAIL biên dịch chỉ còn `undefined: encodeMember` ở `rooms.go` (`Create` cũ, Step 6 viết lại); mọi symbol codec đã có. Lỗi khác → dừng, báo cáo. Test codec chạy ở Step 7.

**Step 5: Test integration**

`apps/core/internal/store/mongostore/members_integration_test.go` (`itStore`, `itClient`, `itRoom`, `codecTime`, `fieldNames` có sẵn trong package):

```go
package mongostore

import (
	"slices"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/storetest"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func TestMongoMembersContract(t *testing.T) {
	client := itClient(t)
	storetest.RunMembers(t, func(t *testing.T) (storetest.MemberRooms, store.MemberActions) {
		s, _ := itStore(t, client)
		return s, s.MemberActions()
	})
}

func rawDoc(t *testing.T, coll *mongo.Collection, filter bson.D) bson.Raw {
	t.Helper()
	raw, err := coll.FindOne(t.Context(), filter).Raw()
	if err != nil {
		t.Fatalf("FindOne raw in %s: %v", coll.Name(), err)
	}
	return raw
}

func sortedFields(t *testing.T, raw bson.Raw) []string {
	t.Helper()
	return slices.Sorted(slices.Values(fieldNames(t, raw)))
}

func TestMemberDocumentLayout(t *testing.T) {
	s, db := itStore(t, itClient(t))
	ctx := t.Context()
	room := domain.Room{ID: itRoom, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: codecTime, MemberCount: 2}
	members := []domain.Member{
		{Room: itRoom, Tenant: "acme", User: "alice", Role: domain.RoleOwner, JoinedAt: codecTime},
		{Room: itRoom, Tenant: "acme", User: "bob", Role: domain.RoleMember, JoinedAt: codecTime},
	}
	if err := s.Create(ctx, room, members); err != nil {
		t.Fatalf("Create: %v", err)
	}
	leave := domain.MemberAction{
		Room: itRoom, MV: 2, Tenant: "acme", Op: domain.MemberOpLeave, By: "alice", At: codecTime.Add(time.Minute), Count: 1, Successor: "bob",
		Changes: []domain.MemberChange{{User: "alice", Prev: domain.RoleOwner}},
	}
	if err := s.MemberActions().Append(ctx, leave); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := s.ApplyMembers(ctx, leave); err != nil {
		t.Fatalf("ApplyMembers: %v", err)
	}
	fact := rawDoc(t, db.Collection(memberActionsCollection), bson.D{{Key: "_id", Value: keys.MemberAction(itRoom, 2)}})
	if got, want := fieldNames(t, fact), []string{"_id", "r", "t", "op", "ch", "by", "ts", "n", "so"}; !slices.Equal(got, want) {
		t.Fatalf("member_actions fields = %v, want %v", got, want)
	}
	for _, f := range []string{"r", "n"} {
		if typ := fact.Lookup(f).Type; typ != bson.TypeInt64 {
			t.Fatalf("member_actions.%s is %v, want int64", f, typ)
		}
	}
	memberOf := func(user string) bson.Raw {
		return rawDoc(t, db.Collection(membersCollection), bson.D{{Key: "r", Value: int64(itRoom)}, {Key: "u", Value: user}})
	}
	alice, bob := memberOf("alice"), memberOf("bob")
	if got, want := sortedFields(t, alice), []string{"_id", "ja", "mv", "r", "ro", "rs", "st", "t", "u"}; !slices.Equal(got, want) {
		t.Fatalf("members fields of alice = %v, want %v", got, want)
	}
	if st, ok := alice.Lookup("st").Int32OK(); !ok || st != 1 {
		t.Fatalf("alice st = %s, want int32 1", alice.Lookup("st"))
	}
	if got, want := sortedFields(t, bob), []string{"_id", "ja", "mv", "r", "ro", "rs", "t", "u"}; !slices.Equal(got, want) {
		t.Fatalf("members fields of bob = %v, want %v", got, want)
	}
	if mv, v := bob.Lookup("mv"), bob.Lookup("rs", "v"); mv.Type != bson.TypeInt64 || mv.Int64() != 2 || v.Type != bson.TypeInt64 || v.Int64() != 1 {
		t.Fatalf("bob mv = %s, rs.v = %s; want int64 2 and int64 1", mv, v)
	}
	if ro := bob.Lookup("ro").StringValue(); ro != "owner" {
		t.Fatalf("bob ro = %q, want owner (successor)", ro)
	}
	userRoomOf := func(user string) bson.Raw {
		return rawDoc(t, db.Collection(userRoomsCollection), bson.D{{Key: "_id", Value: keys.UserRoom("acme", user, itRoom)}})
	}
	if got, want := sortedFields(t, userRoomOf("bob")), []string{"_id", "ja", "mv", "r", "ro", "t", "u"}; !slices.Equal(got, want) {
		t.Fatalf("user_rooms fields of bob = %v, want %v", got, want)
	}
	if got, want := sortedFields(t, userRoomOf("alice")), []string{"_id", "ja", "mv", "r", "ro", "st", "t", "u"}; !slices.Equal(got, want) {
		t.Fatalf("user_rooms fields of alice = %v, want %v", got, want)
	}
	head, err := s.Get(ctx, itRoom)
	if err != nil || head.MemberVersion != 1 || head.MemberCount != 2 {
		t.Fatalf("room head = %+v, %v; want version 1 and 2 members (only Create advanced it)", head, err)
	}
}
```

`apps/core/internal/store/mongostore/bootstrap_members_integration_test.go`:

```go
package mongostore

import "testing"

func TestBootstrapCreatesMemberCollectionsAndTheSuccessionIndex(t *testing.T) {
	db := itDatabase(t, itClient(t))
	for i := range 2 {
		if err := Bootstrap(t.Context(), db); err != nil {
			t.Fatalf("Bootstrap #%d: %v", i+1, err)
		}
	}
	assertClusteredLayout(t, db, memberActionsCollection)
	assertClusteredLayout(t, db, userRoomsCollection)
	if got := indexKeys(t, db.Collection(memberActionsCollection)); !hasIndex(got, "r:1,ts:1", false) {
		t.Fatalf("member_actions indexes = %v, want non-unique r:1,ts:1", got)
	}
	got := indexKeys(t, db.Collection(membersCollection))
	if !hasIndex(got, "r:1,st:1,ro:1,ja:1,u:1", false) || !hasIndex(got, "r:1,u:1", true) {
		t.Fatalf("members indexes = %v, want non-unique r:1,st:1,ro:1,ja:1,u:1 beside unique r:1,u:1", got)
	}
	for k := range indexKeys(t, db.Collection(userRoomsCollection)) {
		if k != "_id:1" {
			t.Fatalf("user_rooms has index %s, want only the clustered _id", k)
		}
	}
}
```

**Step 6: Code adapter**

`apps/core/internal/store/mongostore/mongostore.go`:
- trong khối const thay:

```go
	pinActionsCollection      = "pin_actions"
)
```

bằng:

```go
	pinActionsCollection      = "pin_actions"
	memberActionsCollection   = "member_actions"
	userRoomsCollection       = "user_rooms"
)
```

- trong khối `var` thay:

```go
	_ store.PinProjector      = (*Store)(nil)
	_ store.Reactions         = (*Reactions)(nil)
	_ store.Pins              = (*Pins)(nil)
)
```

bằng:

```go
	_ store.PinProjector      = (*Store)(nil)
	_ store.MemberProjector   = (*Store)(nil)
	_ store.MemberReader      = (*Store)(nil)
	_ store.ReadPositions     = (*Store)(nil)
	_ store.Reactions         = (*Reactions)(nil)
	_ store.Pins              = (*Pins)(nil)
	_ store.MemberActions     = (*MemberActions)(nil)
)
```

- trong `type Store struct` thay:

```go
	reactions *Reactions
	pins      *Pins
}
```

bằng:

```go
	reactions     *Reactions
	pins          *Pins
	userRooms     *mongo.Collection
	memberActions *MemberActions
}
```

- trong literal của `New` thay:

```go
		pins:      &Pins{coll: db.Collection(pinActionsCollection, primary)},
	}
```

bằng:

```go
		pins:          &Pins{coll: db.Collection(pinActionsCollection, primary)},
		userRooms:     db.Collection(userRoomsCollection, primary),
		memberActions: &MemberActions{coll: db.Collection(memberActionsCollection, primary)},
	}
```

(gofmt căn lại cột của struct và literal.)

- thêm vào cuối file:

```go

func (s *Store) MemberActions() *MemberActions { return s.memberActions }
```

`apps/core/internal/store/mongostore/bootstrap.go`:
- thay `	for _, name := range []string{messagesCollection, editsCollection, reactionsCollection, pinActionsCollection} {` bằng `	for _, name := range []string{messagesCollection, editsCollection, reactionsCollection, pinActionsCollection, memberActionsCollection, userRoomsCollection} {`;
- thay `		{pinActionsCollection, roomTimeIndexes()},` bằng:

```go
		{pinActionsCollection, roomTimeIndexes()},
		{memberActionsCollection, roomTimeIndexes()},
```

- trong `memberIndexes` thay:

```go
		{Keys: bson.D{{Key: "t", Value: 1}, {Key: "u", Value: 1}, {Key: "r", Value: 1}}},
	}
```

bằng:

```go
		{Keys: bson.D{{Key: "t", Value: 1}, {Key: "u", Value: 1}, {Key: "r", Value: 1}}},
		{Keys: bson.D{{Key: "r", Value: 1}, {Key: "st", Value: 1}, {Key: "ro", Value: 1}, {Key: "ja", Value: 1}, {Key: "u", Value: 1}}},
	}
```

`apps/core/internal/store/mongostore/rooms.go`:
- thay cả hàm `Create` bằng:

```go
func (s *Store) Create(ctx context.Context, r domain.Room, members []domain.Member) error {
	if err := store.ValidateRoom(r, members); err != nil {
		return err
	}
	fact := domain.InitialMembers(r, members)
	if err := store.ValidateMemberAction(fact); err != nil {
		return err
	}
	room, err := encodeRoom(r)
	if err != nil {
		return err
	}
	room.MemberCount = len(members)
	if _, err := s.rooms.InsertOne(ctx, room); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return fmt.Errorf("create room %d: %w", r.ID, store.ErrRoomExists)
		}
		return fmt.Errorf("create room %d: %w", r.ID, err)
	}
	if err := s.memberActions.Append(ctx, fact); err != nil {
		return fmt.Errorf("create room %d: %w", r.ID, err)
	}
	if err := s.ApplyMembers(ctx, fact); err != nil {
		return fmt.Errorf("create room %d: %w", r.ID, err)
	}
	if _, err := s.AdvanceMembers(ctx, r.ID, 0, fact.MV, fact.Count); err != nil {
		return fmt.Errorf("create room %d: %w", r.ID, err)
	}
	return nil
}
```

- trong `Member` thay:

```go
	filter := bson.D{{Key: "r", Value: key}, {Key: "u", Value: user}}
	if err := findOne(ctx, s.members, filter, &d, domain.ErrNotMember); err != nil {
```

bằng:

```go
	if err := findOne(ctx, s.members, activeMember(key, user), &d, domain.ErrNotMember); err != nil {
```

- trong `ClearHistory` thay `	filter := bson.D{{Key: "r", Value: key}, {Key: "u", Value: user}}` bằng `	filter := activeMember(key, user)`.

`apps/core/internal/store/mongostore/member_actions.go`:

```go
package mongostore

import (
	"context"
	"fmt"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type MemberActions struct {
	coll *mongo.Collection
}

func (m *MemberActions) Append(ctx context.Context, a domain.MemberAction) error {
	doc, err := encodeMemberAction(a)
	if err != nil {
		return err
	}
	if _, err := m.coll.InsertOne(ctx, doc); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return fmt.Errorf("append member v%d of room %d: %w", a.MV, a.Room, store.ErrMemberActionExists)
		}
		return fmt.Errorf("append member v%d of room %d: %w", a.MV, a.Room, err)
	}
	return nil
}

func (m *MemberActions) At(ctx context.Context, room, mv uint64) (domain.MemberAction, error) {
	var d memberActionDoc
	if err := findOne(ctx, m.coll, bson.D{{Key: "_id", Value: keys.MemberAction(room, mv)}}, &d, store.ErrMemberActionNotFound); err != nil {
		return domain.MemberAction{}, fmt.Errorf("member v%d of room %d: %w", mv, room, err)
	}
	return decodeMemberAction(d)
}

func (m *MemberActions) After(ctx context.Context, room, mv uint64, limit int) ([]domain.MemberAction, error) {
	if err := store.ValidateLimit(limit, store.MaxMemberScan); err != nil {
		return nil, err
	}
	filter := bson.D{{Key: "_id", Value: bson.D{
		{Key: "$gt", Value: keys.MemberAction(room, mv)},
		{Key: "$lte", Value: keys.MemberAction(room, math.MaxUint64)},
	}}}
	return m.find(ctx, filter, bson.D{{Key: "_id", Value: 1}}, limit)
}

func (m *MemberActions) Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.MemberAction, error) {
	if err := store.ValidateLimit(limit, store.MaxMemberScan); err != nil {
		return nil, err
	}
	rid, err := toInt64("room id", room)
	if err != nil {
		return nil, err
	}
	filter := bson.D{{Key: "r", Value: rid}, {Key: "ts", Value: bson.D{{Key: "$gte", Value: from}, {Key: "$lte", Value: to}}}}
	return m.find(ctx, filter, bson.D{{Key: "ts", Value: 1}, {Key: "_id", Value: 1}}, limit)
}

func (m *MemberActions) find(ctx context.Context, filter, sort bson.D, limit int) ([]domain.MemberAction, error) {
	cur, err := m.coll.Find(ctx, filter, options.Find().SetSort(sort).SetLimit(int64(limit)))
	if err != nil {
		return nil, fmt.Errorf("find member actions: %w", err)
	}
	var docs []memberActionDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("find member actions: %w", err)
	}
	return decodeMemberActions(docs)
}
```

`apps/core/internal/store/mongostore/member_projection.go`:

```go
package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (s *Store) ApplyMembers(ctx context.Context, a domain.MemberAction) error {
	if err := store.ValidateMemberAction(a); err != nil {
		return err
	}
	members, userRooms, err := memberModels(a)
	if err != nil {
		return err
	}
	if err := upsertAll(ctx, s.members, members); err != nil {
		return fmt.Errorf("apply member v%d of room %d to members: %w", a.MV, a.Room, err)
	}
	if err := upsertAll(ctx, s.userRooms, userRooms); err != nil {
		return fmt.Errorf("apply member v%d of room %d to user rooms: %w", a.MV, a.Room, err)
	}
	return nil
}

func (s *Store) AdvanceMembers(ctx context.Context, room, base, mv uint64, count int) (bool, error) {
	if err := store.ValidateMemberAdvance(base, mv, count); err != nil {
		return false, err
	}
	key, keyErr := toInt64("room id", room)
	from, fromErr := toInt64("member base version", base)
	to, toErr := toInt64("member version", mv)
	if err := firstErr(keyErr, fromErr, toErr); err != nil {
		return false, err
	}
	filter := bson.D{{Key: "_id", Value: key}, versionIs("mv", from)}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "mv", Value: to}, {Key: "mc", Value: count}}}}
	res, err := s.rooms.UpdateOne(ctx, filter, update)
	if err != nil {
		return false, fmt.Errorf("advance members of room %d to v%d: %w", room, mv, err)
	}
	return res.MatchedCount == 1, nil
}

func upsertAll(ctx context.Context, coll *mongo.Collection, models []mongo.WriteModel) error {
	if len(models) == 0 {
		return ctx.Err()
	}
	if _, err := coll.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false)); err != nil && !onlyDuplicateKeys(err) {
		return err
	}
	return nil
}
```

`apps/core/internal/store/mongostore/member_updates.go`:

```go
package mongostore

import (
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func memberModels(a domain.MemberAction) ([]mongo.WriteModel, []mongo.WriteModel, error) {
	room, roomErr := toInt64("room id", a.Room)
	mv, mvErr := toInt64("member version", a.MV)
	if err := firstErr(roomErr, mvErr); err != nil {
		return nil, nil, err
	}
	below := bson.E{Key: "mv", Value: bson.D{{Key: "$not", Value: bson.D{{Key: "$gte", Value: mv}}}}}
	users := touchedUsers(a)
	members := make([]mongo.WriteModel, 0, len(users))
	userRooms := make([]mongo.WriteModel, 0, len(users))
	for _, user := range users {
		shared, read, err := appliedFields(a, user)
		if err != nil {
			return nil, nil, err
		}
		head := bson.D{{Key: "t", Value: literal(a.Tenant)}, {Key: "mv", Value: mv}}
		ids := bson.D{{Key: "u", Value: literal(user)}, {Key: "r", Value: room}}
		members = append(members, upsertModel(bson.D{{Key: "r", Value: room}, {Key: "u", Value: user}, below}, slices.Concat(head, shared, read)))
		userRooms = append(userRooms, upsertModel(bson.D{{Key: "_id", Value: keys.UserRoom(a.Tenant, user, a.Room)}, below}, slices.Concat(head, ids, shared)))
	}
	return members, userRooms, nil
}

func appliedFields(a domain.MemberAction, user string) (bson.D, bson.D, error) {
	var shared, read bson.D
	if i := slices.IndexFunc(a.Changes, func(c domain.MemberChange) bool { return c.User == user }); i >= 0 {
		var err error
		if shared, read, err = changeFields(a.Op, a.At, a.Changes[i]); err != nil {
			return nil, nil, err
		}
	}
	if user == a.Successor {
		shared = append(shared, bson.E{Key: "ro", Value: literal(string(domain.RoleOwner))})
	}
	return shared, read, nil
}

func changeFields(op domain.MemberOp, at time.Time, c domain.MemberChange) (bson.D, bson.D, error) {
	switch op {
	case domain.MemberOpAdd:
		seq, err := toInt64("read seq", c.ReadSeq)
		if err != nil {
			return nil, nil, err
		}
		shared := bson.D{{Key: "ro", Value: literal(string(c.Role))}, {Key: "ja", Value: at}, {Key: "st", Value: "$$REMOVE"}}
		read := bson.D{{Key: "rs", Value: bson.D{{Key: "s", Value: raised("$rs.s", seq)}, {Key: "v", Value: bumped("$rs.v")}}}}
		return shared, read, nil
	case domain.MemberOpRemove, domain.MemberOpLeave:
		return bson.D{{Key: "st", Value: memberRemoved}}, nil, nil
	case domain.MemberOpRole:
		return bson.D{{Key: "ro", Value: literal(string(c.Role))}}, nil, nil
	}
	return nil, nil, nil
}

func raised(field string, to int64) bson.D {
	return bson.D{{Key: "$max", Value: bson.A{ifMissing(field), to}}}
}

func bumped(field string) bson.D {
	return bson.D{{Key: "$add", Value: bson.A{ifMissing(field), int64(1)}}}
}

func ifMissing(field string) bson.D {
	return bson.D{{Key: "$ifNull", Value: bson.A{field, int64(0)}}}
}

func upsertModel(filter, set bson.D) mongo.WriteModel {
	return mongo.NewUpdateOneModel().SetFilter(filter).SetUpdate(mongo.Pipeline{{{Key: "$set", Value: set}}}).SetUpsert(true)
}

func touchedUsers(a domain.MemberAction) []string {
	users := make([]string, 0, len(a.Changes)+1)
	for _, c := range a.Changes {
		users = append(users, c.User)
	}
	if a.Successor != "" && !slices.Contains(users, a.Successor) {
		users = append(users, a.Successor)
	}
	return users
}
```

`apps/core/internal/store/mongostore/member_reads.go`:

```go
package mongostore

import (
	"context"
	"fmt"
	"math"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func activeMember(room int64, user string) bson.D {
	return bson.D{{Key: "r", Value: room}, {Key: "u", Value: user}, {Key: "st", Value: nil}}
}

func (s *Store) MembersOf(ctx context.Context, room uint64, users []string) ([]domain.Member, error) {
	if err := store.ValidateMemberUsers(users); err != nil {
		return nil, err
	}
	key, err := toInt64("room id", room)
	if err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return []domain.Member{}, ctx.Err()
	}
	filter := bson.D{{Key: "r", Value: key}, {Key: "u", Value: bson.D{{Key: "$in", Value: users}}}}
	return s.findMembers(ctx, filter, options.Find().SetSort(bson.D{{Key: "u", Value: 1}}))
}

func (s *Store) Successor(ctx context.Context, room uint64) (domain.Member, bool, error) {
	key, err := toInt64("room id", room)
	if err != nil {
		return domain.Member{}, false, err
	}
	for _, role := range []domain.Role{domain.RoleAdmin, domain.RoleMember} {
		found, err := s.activeByRole(ctx, key, role, 1)
		if err != nil {
			return domain.Member{}, false, fmt.Errorf("successor in room %d: %w", room, err)
		}
		if len(found) > 0 {
			return found[0], true, nil
		}
	}
	return domain.Member{}, false, nil
}

func (s *Store) Owners(ctx context.Context, room uint64, limit int) ([]domain.Member, error) {
	if err := store.ValidateLimit(limit, store.MaxMemberScan); err != nil {
		return nil, err
	}
	key, err := toInt64("room id", room)
	if err != nil {
		return nil, err
	}
	owners, err := s.activeByRole(ctx, key, domain.RoleOwner, limit)
	if err != nil {
		return nil, fmt.Errorf("owners of room %d: %w", room, err)
	}
	return owners, nil
}

func (s *Store) UserRooms(ctx context.Context, tenant, user string, after uint64, limit int) ([]domain.Member, error) {
	if err := store.ValidateUserRoomsQuery(tenant, user, limit); err != nil {
		return nil, err
	}
	filter := bson.D{
		{Key: "_id", Value: bson.D{
			{Key: "$gt", Value: keys.UserRoom(tenant, user, after)},
			{Key: "$lte", Value: keys.UserRoom(tenant, user, math.MaxUint64)},
		}},
		{Key: "st", Value: nil},
	}
	opts := options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(int64(limit))
	cur, err := s.userRooms.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("rooms of %q: %w", user, err)
	}
	var docs []userRoomDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("rooms of %q: %w", user, err)
	}
	return decodeUserRooms(docs)
}

func (s *Store) activeByRole(ctx context.Context, room int64, role domain.Role, limit int) ([]domain.Member, error) {
	filter := bson.D{{Key: "r", Value: room}, {Key: "st", Value: nil}, {Key: "ro", Value: string(role)}}
	opts := options.Find().SetSort(bson.D{{Key: "ja", Value: 1}, {Key: "u", Value: 1}}).SetLimit(int64(limit))
	return s.findMembers(ctx, filter, opts)
}

func (s *Store) findMembers(ctx context.Context, filter bson.D, opts *options.FindOptionsBuilder) ([]domain.Member, error) {
	cur, err := s.members.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("find members: %w", err)
	}
	var docs []memberDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("find members: %w", err)
	}
	return decodeMembers(docs)
}
```

`apps/core/internal/store/mongostore/read_positions.go`:

```go
package mongostore

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func (s *Store) MarkRead(ctx context.Context, room uint64, user string, seq uint64) (domain.ReadPos, bool, error) {
	to, err := toInt64("seq", seq)
	if err != nil {
		return domain.ReadPos{}, false, err
	}
	return s.moveRead(ctx, room, user, to, bson.D{{Key: "$not", Value: bson.D{{Key: "$gte", Value: to}}}})
}

func (s *Store) MarkUnread(ctx context.Context, room uint64, user string, to uint64) (domain.ReadPos, bool, error) {
	at, err := toInt64("seq", to)
	if err != nil {
		return domain.ReadPos{}, false, err
	}
	return s.moveRead(ctx, room, user, at, bson.D{{Key: "$gt", Value: at}})
}

func (s *Store) moveRead(ctx context.Context, room uint64, user string, to int64, moves bson.D) (domain.ReadPos, bool, error) {
	key, err := toInt64("room id", room)
	if err != nil {
		return domain.ReadPos{}, false, fmt.Errorf("read position of %q in room %d: %w", user, room, domain.ErrNotMember)
	}
	filter := append(activeMember(key, user), bson.E{Key: "rs.s", Value: moves})
	set := bson.D{{Key: "rs", Value: bson.D{{Key: "s", Value: to}, {Key: "v", Value: bumped("$rs.v")}}}}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var d memberDoc
	err = s.members.FindOneAndUpdate(ctx, filter, mongo.Pipeline{{{Key: "$set", Value: set}}}, opts).Decode(&d)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		m, err := s.Member(ctx, room, user)
		return m.Read, false, err
	case err != nil:
		return domain.ReadPos{}, false, fmt.Errorf("read position of %q in room %d: %w", user, room, err)
	}
	m, err := decodeMember(d)
	if err != nil {
		return domain.ReadPos{}, false, err
	}
	return m.Read, true, nil
}
```

**Step 7: Chạy unit, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/..."` rồi `make vet`
Expected: PASS (integration skip); vet sạch (`onlyDuplicateKeys` giờ chỉ dùng ở `upsertAll`; `options.InsertMany` không còn ở `rooms.go`). `TestRoomCodecRoundTrip` vẫn đúng 7 field (`mv` omitempty, `encodeRoom` không đặt). `wc -l apps/core/internal/store/mongostore/*.go` mỗi file < 200: `codec.go` 168, `codec_test.go` 173, `edit_codec_test.go` 126, `member_codec.go` 114, `member_action_codec.go` 100, `member_codec_test.go` 166, `member_actions.go` 77, `member_projection.go` 59, `member_updates.go` 94, `member_reads.go` 106, `read_positions.go` 53, `rooms.go` 105, `mongostore.go` 86, `bootstrap.go` 138, `members_integration_test.go` 101, `bootstrap_members_integration_test.go` 26.

**Step 8: Integration**

Run: `make infra-up && make itest`
Expected: PASS, gồm `TestMongoMembersContract` (23 subtest của `RunMembers`, kể cả nhánh trùng khoá của guard: `a fact at or below a member's version never touches it`), `TestMemberDocumentLayout`, `TestBootstrapCreatesMemberCollectionsAndTheSuccessionIndex`, `TestMongoStoreContract`/`TestMongoEditsContract`/`TestMongoPinsContract` (kỳ vọng `created`/`joined` sau `Create`), `TestBootstrapIsIdempotent` và itest của `apps/core` (mọi `CreateRoom` qua gRPC giờ ghi fact 1 + projection; feed chưa xem `member_actions` tới Task 6). Case Mongo khác memstore → dừng, báo cáo (không sửa contract cho vừa adapter). `ApplyMembers` báo lỗi khác trùng khoá ở nhánh guard (ví dụ Mongo tự retry upsert và báo `matched 0`) → dừng, báo cáo kèm lỗi nguyên văn.

**Step 9: Commit**

INDEXES.csv, dòng `apps/core/internal/store/mongostore`:
- trong cột purpose thay `and pin_actions (index {r:1, ts:1}), hidden` bằng `pin_actions (index {r:1, ts:1}), member_actions (index {r:1, ts:1}) and user_rooms (_id tenant 0x00 user 0x00 room, no secondary index), hidden`;
- thay `+ rooms/members indexes` bằng `+ rooms/members indexes (members {r:1, st:1, ro:1, ja:1, u:1} for Successor/Owners)`;
- thay `room before members;` bằng `Create = room insert (mc, no mv) then member fact 1 (domain.InitialMembers) then ApplyMembers then AdvanceMembers 0 -> 1; member facts via Store.MemberActions() (own type: Append/At/Between clash with Edits; Append insert -> ErrMemberActionExists, At, After clustered _id range, Between {r, ts}; codec in member_action_codec.go, n int64); ApplyMembers = two unordered BulkWrites (members, then user_rooms) of pipeline upserts guarded by mv not >= k (a duplicate key means a newer fact already applied; add revives with $$REMOVE of st, sets ro/ja, rs.s = $max, rs.v + 1; remove/leave set st 1; role sets ro; the successor gets owner); AdvanceMembers = UpdateOne CAS on rooms.mv (0 = missing) setting mv/mc; MembersOf (removed included, sorted by user), Successor/Owners (active by role, ja then u), UserRooms (active user_rooms _id range of one tenant and user; BinData sorts by length first, so only same-length keys keep byte order); MarkRead/MarkUnread = FindOneAndUpdate on the active member with rs.s not >= seq / rs.s > to and rs.v + 1, no match -> re-read; Member/ClearHistory see only active members (st missing); member codec (members st/mv/rs, user_rooms) in member_codec.go;`;
- trong cột key_symbols thay `Store.Pins;Reactions;Pins;` bằng `Store.Pins;Store.MemberActions;Reactions;Pins;MemberActions;`;
- cột tests thay `unit;contract;feed contract (itest);itest (explain shows bounded clustered scan)` bằng `unit;contract;feed contract (itest);itest (explain shows bounded clustered scan);itest (member and user_rooms document layout)`;
- cột decisions thay `D92;D91` bằng `D92;D91;D96;D97;D98;D101;D104`.

```bash
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
make fmt-check && make vet && make lint
git add apps/core/internal/store/mongostore/member_codec.go apps/core/internal/store/mongostore/member_action_codec.go apps/core/internal/store/mongostore/member_codec_test.go apps/core/internal/store/mongostore/member_actions.go apps/core/internal/store/mongostore/member_projection.go apps/core/internal/store/mongostore/member_updates.go apps/core/internal/store/mongostore/member_reads.go apps/core/internal/store/mongostore/read_positions.go apps/core/internal/store/mongostore/members_integration_test.go apps/core/internal/store/mongostore/bootstrap_members_integration_test.go
git commit -m "feat(mongostore): store member facts, projections and read positions" -- apps/core/internal/store/mongostore/ INDEXES.csv
```

Expected: CSV in `{7}`; `git show --stat HEAD` chỉ có file `mongostore` của task và `INDEXES.csv`.

---

### Task 6: ★ Feed — `MemberInserted` (memstore + Mongo) + `RunMemberFeed` + `KnownKind`/id `g:` + reader chuyển kind mới + registry tạm

Feed thêm insert của `member_actions` (D106). Update của `members`, `user_rooms` và `rooms` (`mv/mc`) vẫn bị loại bởi `ns.coll`/`operationType` như mọi projection khác; itest feed-skip chốt điều đó. memstore: `Rooms.attach` (đã gọi trong `NewFeed(msgs, rooms, …)`) gắn luôn log của `MemberActions`, không thêm `FeedOption`. `work.KnownKind` nhận `MemberInserted`; id record `g:` + `pbconv.MemberEventID(room, mv)` (một record cho cả fact; `checkKind` có sẵn đã cấm đuôi user cho kind khác `ReactionChanged`). Reader chuyển kind mới mà không đổi code (`work.KnownKind` + `work.RecordOf`). `effects_wiring.go` đăng ký tạm `store.MemberInserted: {activity.Effect()}`, nên record member tới worker từ task này chỉ đẩy `lc/ab` của room (`room_activity` để `Seq = 0` cho kind khác `MessageInserted`) rồi ack; Task 15 thay bằng registry cuối.

Hệ quả cho test có sẵn: mỗi `Create` qua feed giờ sinh hai change (room rồi fact 1).
- `storetest.feedRooms` chờ 6 change `[Room, Member, Message, Room, Member, Message]` và so fact với `domain.InitialMembers`.
- `mongostore.TestFeedSkipsSummaryPinActivityEditHideAndClearWrites` chờ `[Room, Member, Message]` trước khi ghi, và ghi thêm `MarkRead`, `ApplyMembers`, `AdvanceMembers` (đều phải bị loại).
- `mongostore.TestFeedPipelineLetsOnlyReactionUpdatesThrough` chờ 6 collection insert, cuối là `member_actions`.
- `reconcile`: `TestForwardsARoomInsertAsARoomRecord` chờ `[r:777, g:777-m1]`; `TestStatsTrackTermsAndForwards` chờ 4 forwarded (2 tin + room + fact).
- Room của `reconcile.newRig` được tạo **trước** `NewFeed` nên fact 1 của nó không vào log; mốc `rg.base` không đổi.

**Files:**
- Modify: `apps/core/internal/store/memstore/change_log.go`, `member_actions.go`, `feed.go`, `memstore_test.go`
- Create: `apps/core/internal/store/storetest/feed_member_cases.go`
- Modify: `apps/core/internal/store/storetest/feed_room_cases.go`, `feed_cases.go` (tên case)
- Modify: `apps/core/internal/store/mongostore/feed.go`, `feed_change.go`, `feed_reaction_change_test.go`, `feed_integration_test.go`, `feed_skip_integration_test.go`
- Create: `apps/core/internal/store/mongostore/feed_member_change.go`, `feed_member_change_test.go`
- Modify: `apps/core/internal/work/record.go`, `record_test.go`, `record_tail_test.go`
- Create: `apps/core/internal/reconcile/member_forward_test.go`
- Modify: `apps/core/internal/reconcile/forward_test.go`, `stats_test.go`
- Modify: `apps/core/effects_wiring.go`
- Modify: `INDEXES.csv` (dòng `apps/core/internal/store/mongostore`, `apps/core/internal/store/memstore`, `apps/core/internal/store/storetest`, `apps/core/internal/work`, `apps/core/internal/reconcile`, `apps/core`)

**Step 1: Test**

`apps/core/internal/store/storetest/feed_member_cases.go`:

```go
package storetest

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func RunMemberFeed(t *testing.T, open func(t *testing.T) (store.MemberActions, store.ChangeFeed)) {
	t.Helper()
	t.Run("member facts come out in commit order with their content and a refused append adds none", func(t *testing.T) {
		facts, feed := open(t)
		cur := openCursor(t, feed)
		first := memberAct(roomA, 1, 2, domain.MemberOpAdd, addChange("alice", domain.RoleOwner, 0), addChange("bob", domain.RoleMember, 0))
		second := memberAct(roomA, 2, 1, domain.MemberOpLeave, dropChange("alice", domain.RoleOwner))
		second.Successor = "bob"
		other := memberAct(roomB, 1, 1, domain.MemberOpAdd, addChange("carol", domain.RoleOwner, 0))
		mustAppendActs(t, facts, first)
		again := memberAct(roomA, 1, 1, domain.MemberOpAdd, addChange("dave", domain.RoleOwner, 0))
		assertErrorIs(t, "Append(existing version)", facts.Append(t.Context(), again), store.ErrMemberActionExists)
		mustAppendActs(t, facts, second, other)
		got := nextChanges(t, cur, 3)
		acts := make([]domain.MemberAction, len(got))
		for i, c := range got {
			if c.Kind != store.MemberInserted || c.Msg.Seq != 0 || c.Room.ID != 0 || c.Pin.PV != 0 || c.Reaction.N != 0 {
				t.Fatalf("change %d = %+v, want only a member fact", i, c)
			}
			acts[i] = c.Member
		}
		assertMemberActions(t, "member changes", acts, []domain.MemberAction{first, second, other})
	})
}
```

`apps/core/internal/store/storetest/feed_cases.go`, trong `feedCases` thay `{"room inserts come out in commit order with their content", feedRooms},` bằng `{"room inserts and their first member fact come out in commit order with their content", feedRooms},`.

`apps/core/internal/store/storetest/feed_room_cases.go`, thay cả hàm `feedRooms` bằng:

```go
func feedRooms(t *testing.T, msgs store.Messages, rooms store.Rooms, feed store.ChangeFeed) {
	cur := openCursor(t, feed)
	first, firstMembers := teamOf(roomA)
	second, secondMembers := teamOf(roomB)
	mustCreate(t, rooms, first, firstMembers)
	insertEach(t, msgs, msg(roomA, mainThread, 1))
	mustCreate(t, rooms, second, secondMembers)
	insertEach(t, msgs, msg(roomB, mainThread, 1))
	got := nextChanges(t, cur, 6)
	kinds := []store.ChangeKind{
		store.RoomInserted, store.MemberInserted, store.MessageInserted,
		store.RoomInserted, store.MemberInserted, store.MessageInserted,
	}
	for i, c := range got {
		if c.Kind != kinds[i] {
			t.Fatalf("change %d kind = %d, want %d", i, c.Kind, kinds[i])
		}
	}
	assertChangedRoom(t, got[0], first)
	assertChangedRoom(t, got[3], second)
	assertMemberActions(t, "member changes", []domain.MemberAction{got[1].Member, got[4].Member},
		[]domain.MemberAction{domain.InitialMembers(first, firstMembers), domain.InitialMembers(second, secondMembers)})
	msgChanges := []store.Change{got[2], got[5]}
	assertMessages(t, messagesOf(msgChanges), []domain.Message{msg(roomA, mainThread, 1), msg(roomB, mainThread, 1)})
	for _, c := range msgChanges {
		if c.Room != (domain.Room{}) {
			t.Fatalf("message change carries room %+v, want none", c.Room)
		}
	}
}
```

`apps/core/internal/store/memstore/memstore_test.go`, thêm vào cuối file:

```go

func TestMemberFeedContract(t *testing.T) {
	storetest.RunMemberFeed(t, func(*testing.T) (store.MemberActions, store.ChangeFeed) {
		rooms := memstore.NewRooms()
		return rooms.MemberActions(), memstore.NewFeed(memstore.NewMessages(), rooms, nil)
	})
}
```

`apps/core/internal/store/mongostore/feed_integration_test.go`, thêm vào cuối file:

```go

func TestMongoMemberFeedContract(t *testing.T) {
	client := itClient(t)
	storetest.RunMemberFeed(t, func(t *testing.T) (store.MemberActions, store.ChangeFeed) {
		s, db := itStore(t, client)
		return s.MemberActions(), NewFeed(db)
	})
}
```

`apps/core/internal/store/mongostore/feed_skip_integration_test.go`:
- thay `	for _, want := range []store.ChangeKind{store.RoomInserted, store.MessageInserted} {` bằng `	for _, want := range []store.ChangeKind{store.RoomInserted, store.MemberInserted, store.MessageInserted} {`;
- thay:

```go
	if _, err := s.ClearHistory(ctx, itRoom, "alice", 1); err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
```

bằng:

```go
	if _, err := s.ClearHistory(ctx, itRoom, "alice", 1); err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
	if _, _, err := s.MarkRead(ctx, itRoom, "alice", 1); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	promote := domain.MemberAction{
		Room: itRoom, MV: 2, Tenant: "acme", Op: domain.MemberOpRole, By: "alice", At: codecTime, Count: 1,
		Changes: []domain.MemberChange{{User: "alice", Role: domain.RoleAdmin, Prev: domain.RoleOwner}},
	}
	if err := s.ApplyMembers(ctx, promote); err != nil {
		t.Fatalf("ApplyMembers: %v", err)
	}
	if ok, err := s.AdvanceMembers(ctx, itRoom, 1, 2, 1); err != nil || !ok {
		t.Fatalf("AdvanceMembers = %v, %v", ok, err)
	}
```

- thay `want only the reaction after the summary, pin, activity, edit, hide and clear writes` bằng `want only the reaction after the summary, pin, activity, edit, hide, clear, read and member projection writes`.

`apps/core/internal/store/mongostore/feed_reaction_change_test.go`, trong `TestFeedPipelineLetsOnlyReactionUpdatesThrough` thay:

```go
	if op := raw.Lookup("$match", "$or", "0", "operationType").StringValue(); op != "insert" || len(inserts) != 5 {
		t.Fatalf("insert branch = %s, want inserts of the 5 fact collections", raw.Lookup("$match", "$or", "0"))
	}
```

bằng:

```go
	if op := raw.Lookup("$match", "$or", "0", "operationType").StringValue(); op != "insert" || len(inserts) != 6 || inserts[5].StringValue() != memberActionsCollection {
		t.Fatalf("insert branch = %s, want inserts of the 6 fact collections ending with member_actions", raw.Lookup("$match", "$or", "0"))
	}
```

`apps/core/internal/store/mongostore/feed_member_change_test.go` (`changeOn`, `codecTime` có sẵn):

```go
package mongostore

import (
	"errors"
	"reflect"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestDecodeChangeReadsMemberFacts(t *testing.T) {
	a := sampleLeave()
	doc, err := encodeMemberAction(a)
	if err != nil {
		t.Fatalf("encodeMemberAction: %v", err)
	}
	got, err := decodeChange(changeOn(t, memberActionsCollection, doc))
	if err != nil || got.Kind != store.MemberInserted || !got.CommittedAt.Equal(codecTime) || got.Msg.Room != 0 || got.Room.ID != 0 || got.Pin.PV != 0 {
		t.Fatalf("member change = %+v, %v; want only a member fact", got, err)
	}
	if !reflect.DeepEqual(got.Member, a) {
		t.Fatalf("member fact = %+v, want %+v", got.Member, a)
	}
}

func TestDecodeChangeRejectsBrokenMemberFacts(t *testing.T) {
	good, err := encodeMemberAction(sampleLeave())
	if err != nil {
		t.Fatalf("encodeMemberAction: %v", err)
	}
	badOp, negative := good, good
	badOp.Op, negative.Count = 9, -1
	for name, ev := range map[string]changeDoc{
		"bad id":         changeOn(t, memberActionsCollection, bson.D{{Key: "_id", Value: []byte{1, 2}}}),
		"unknown op":     changeOn(t, memberActionsCollection, badOp),
		"negative count": changeOn(t, memberActionsCollection, negative),
		"no document":    {WallTime: codecTime, NS: changeNS{Coll: memberActionsCollection}},
	} {
		if _, err := decodeChange(ev); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: decodeChange = %v, want errCorrupt", name, err)
		}
	}
	if _, err := decodeChange(changeOn(t, membersCollection, bson.D{{Key: "u", Value: "bob"}, {Key: "st", Value: int32(1)}})); !errors.Is(err, errCorrupt) {
		t.Fatalf("decodeChange(members) = %v, want errCorrupt: projections are never on the feed", err)
	}
}
```

`apps/core/internal/work/record_test.go`:
- trong `TestRecordRoundTripsThroughThirtySevenBytes` thay:

```go
		{Kind: store.EditInserted, Room: 42, Thread: 3, Seq: 9, Version: math.MaxUint32, CommittedAt: committed},
	} {
```

bằng:

```go
		{Kind: store.EditInserted, Room: 42, Thread: 3, Seq: 9, Version: math.MaxUint32, CommittedAt: committed},
		{Kind: store.MemberInserted, Room: 42, Seq: 7, CommittedAt: committed},
	} {
```

- trong `TestIDsAreNaturalKeys` thay `		"pin":             {work.Record{Kind: store.PinInserted, Room: 42, Seq: 5}, "p:42-p5"},` bằng:

```go
		"pin":             {work.Record{Kind: store.PinInserted, Room: 42, Seq: 5}, "p:42-p5"},
		"member":          {work.Record{Kind: store.MemberInserted, Room: 42, Seq: 3}, "g:42-m3"},
```

- thay cả hàm `TestKnownKindsAreTheFiveChangeKinds` bằng:

```go
func TestKnownKindsAreTheSixChangeKinds(t *testing.T) {
	for k := range store.ChangeKind(9) {
		want := k >= store.MessageInserted && k <= store.MemberInserted
		if got := work.KnownKind(k); got != want {
			t.Errorf("KnownKind(%d) = %v, want %v", k, got, want)
		}
	}
}
```

`apps/core/internal/work/record_tail_test.go`, trong `TestDecodeRejectsMalformedTails` thay:

```go
		"room record with a user":      append(slices.Clone(room), 1, 'a'),
```

bằng:

```go
		"room record with a user":      append(slices.Clone(room), 1, 'a'),
		"member record with a user":    append(work.Encode(work.Record{Kind: store.MemberInserted, Room: 42, Seq: 3, CommittedAt: committed}), 1, 'a'),
```

`apps/core/internal/reconcile/forward_test.go`, trong `TestForwardsARoomInsertAsARoomRecord` thay:

```go
		if got := storedIDs(rg.js); !slices.Equal(got, []string{roomRecordID(otherRoom)}) {
			t.Fatalf("stored = %v, want only %s", got, roomRecordID(otherRoom))
		}
```

bằng:

```go
		if got := storedIDs(rg.js); !slices.Equal(got, []string{roomRecordID(otherRoom), "g:777-m1"}) {
			t.Fatalf("stored = %v, want %s then the record of its first member fact", got, roomRecordID(otherRoom))
		}
```

`apps/core/internal/reconcile/stats_test.go`, trong `TestStatsTrackTermsAndForwards` thay:

```go
		if s := rg.Stats(); !s.Running || s.Terms != 1 || s.Forwarded != 3 || s.Dropped != 0 {
			t.Fatalf("stats = %+v, want running, 1 term, 3 forwarded, 0 dropped", s)
		}
```

bằng:

```go
		if s := rg.Stats(); !s.Running || s.Terms != 1 || s.Forwarded != 4 || s.Dropped != 0 {
			t.Fatalf("stats = %+v, want running, 1 term, 4 forwarded (2 messages, a room and its first member fact), 0 dropped", s)
		}
```

`apps/core/internal/reconcile/member_forward_test.go`:

```go
package reconcile_test

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

func (rg *rig) memberFact(t *testing.T, r, mv uint64) {
	t.Helper()
	a := domain.MemberAction{
		Room: r, MV: mv, Tenant: tenant, Op: domain.MemberOpAdd, By: "alice", At: time.Now().UTC(), Count: 2,
		Changes: []domain.MemberChange{{User: "bob", Role: domain.RoleMember}},
	}
	if err := rg.rooms.MemberActions().Append(t.Context(), a); err != nil {
		t.Fatalf("member fact %d v%d: %v", r, mv, err)
	}
}

func TestForwardsAMemberFactAsAMemberRecord(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		committed := time.Now()
		rg.memberFact(t, room, 2)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, []string{"g:4242-m2"}) {
			t.Fatalf("stored = %v, want the member record g:4242-m2", got)
		}
		stored := rg.js.Stored()
		if want := work.Subject(setup.SubjectRoot, work.Partition(room, setup.Partitions)); stored[0].Subject != want {
			t.Fatalf("subject = %q, want %q", stored[0].Subject, want)
		}
		got, err := work.Decode(stored[0].Data)
		if err != nil || got.Kind != store.MemberInserted || got.Room != room || got.Thread != 0 || got.Seq != 2 || got.Version != 0 || got.User != "" || !got.CommittedAt.Equal(committed) {
			t.Fatalf("record = %+v, %v; want member fact v2 of room %d committed at %v", got, err, room, committed)
		}
		if got := rg.Stats().Dropped; got != 0 {
			t.Fatalf("dropped = %d, want 0", got)
		}
	})
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/... ./apps/core/internal/work/... ./apps/core/internal/reconcile/..."`
Expected:
- `memstore`: FAIL ở `TestFeedContract/room_inserts_and_their_first_member_fact_…` (chỉ có 4 change, `Next after 4 changes: context deadline exceeded` sau 10s) và `TestMemberFeedContract` (`Next after 0 changes: context deadline exceeded`).
- `mongostore`: FAIL ở `TestDecodeChangeReadsMemberFacts` và `TestDecodeChangeRejectsBrokenMemberFacts` (`change on collection "member_actions"` là `errCorrupt` nên test đọc fail; các case hỏng có thể đã đúng vì lý do sai) và `TestFeedPipelineLetsOnlyReactionUpdatesThrough` (5 collection).
- `work`: FAIL ở `TestRecordRoundTripsThroughThirtySevenBytes` (`unknown kind 6`), `TestIDsAreNaturalKeys` (`member: ID = "", want "g:42-m3"`), `TestKnownKindsAreTheSixChangeKinds` (`KnownKind(6) = false, want true`), `TestDecodeRejectsMalformedTails` (`member record with a user: Decode = … unknown kind 6, want ErrBadRecord and not ErrUnknownKind`).
- `reconcile`: FAIL ở `TestForwardsAMemberFactAsAMemberRecord` (stored rỗng: reader bỏ kind 6), `TestForwardsARoomInsertAsARoomRecord` và `TestStatsTrackTermsAndForwards` (chưa có change member).

**Step 3: Code memstore**

`apps/core/internal/store/memstore/change_log.go`:
- trong `type logged struct` thay:

```go
	pin      domain.PinAction
	at       time.Time
```

bằng:

```go
	pin      domain.PinAction
	member   domain.MemberAction
	at       time.Time
```

- thay cả hàm `func (s *Rooms) attach(log *Messages)` bằng:

```go
func (s *Rooms) attach(log *Messages) {
	s.mu.Lock()
	s.log = log
	s.mu.Unlock()
	s.actions.attach(log)
}

func (s *MemberActions) attach(log *Messages) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = log
}
```

`apps/core/internal/store/memstore/member_actions.go`:
- trong `type MemberActions struct` thay:

```go
	facts map[uint64][]domain.MemberAction
}
```

bằng:

```go
	facts map[uint64][]domain.MemberAction
	log   *Messages
}
```

- trong `Append` thay:

```go
	s.facts[a.Room] = slices.Insert(line, i, cloneAction(a))
	return nil
}
```

bằng:

```go
	s.facts[a.Room] = slices.Insert(line, i, cloneAction(a))
	if s.log != nil {
		s.log.appendFact(logged{kind: store.MemberInserted, member: cloneAction(a)})
	}
	return nil
}
```

`apps/core/internal/store/memstore/feed.go`, trong `cursor.Next` thay:

```go
				Kind: l.kind, Msg: l.msg, Room: l.room, Edit: l.edit, Reaction: l.reaction, Pin: l.pin,
```

bằng:

```go
				Kind: l.kind, Msg: l.msg, Room: l.room, Edit: l.edit, Reaction: l.reaction, Pin: l.pin, Member: l.member,
```

**Step 4: Code Mongo**

`apps/core/internal/store/mongostore/feed.go`, trong `feedPipeline` thay:

```go
	facts := bson.A{messagesCollection, roomsCollection, editsCollection, reactionsCollection, pinActionsCollection}
```

bằng:

```go
	facts := bson.A{messagesCollection, roomsCollection, editsCollection, reactionsCollection, pinActionsCollection, memberActionsCollection}
```

`apps/core/internal/store/mongostore/feed_change.go`, trong `decodeChange` thay:

```go
	case pinActionsCollection:
		return decodePinChange(ev)
```

bằng:

```go
	case pinActionsCollection:
		return decodePinChange(ev)
	case memberActionsCollection:
		return decodeMemberChange(ev)
```

`apps/core/internal/store/mongostore/feed_member_change.go`:

```go
package mongostore

import (
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func decodeMemberChange(ev changeDoc) (store.Change, error) {
	var d memberActionDoc
	if err := bson.Unmarshal(ev.FullDocument, &d); err != nil {
		return store.Change{}, fmt.Errorf("%w: member action document: %w", errCorrupt, err)
	}
	a, err := decodeMemberAction(d)
	if err != nil {
		return store.Change{}, err
	}
	return store.Change{Kind: store.MemberInserted, Member: a, CommittedAt: ev.WallTime}, nil
}
```

**Step 5: Code work + registry tạm**

`apps/core/internal/work/record.go`:
- trong `KnownKind` thay:

```go
	case store.MessageInserted, store.RoomInserted, store.EditInserted, store.ReactionChanged, store.PinInserted:
```

bằng:

```go
	case store.MessageInserted, store.RoomInserted, store.EditInserted, store.ReactionChanged, store.PinInserted, store.MemberInserted:
```

- trong `ID` thay:

```go
	case store.PinInserted:
		return "p:" + pbconv.PinEventID(r.Room, r.Seq)
```

bằng:

```go
	case store.PinInserted:
		return "p:" + pbconv.PinEventID(r.Room, r.Seq)
	case store.MemberInserted:
		return "g:" + pbconv.MemberEventID(r.Room, r.Seq)
```

`apps/core/effects_wiring.go`, trong `registry` thay:

```go
		store.PinInserted:     {activity.Effect(), fx.pinProjection.Effect(), fx.pinEvent.Effect()},
	}
```

bằng:

```go
		store.PinInserted:     {activity.Effect(), fx.pinProjection.Effect(), fx.pinEvent.Effect()},
		store.MemberInserted:  {activity.Effect()},
	}
```

**Step 6: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/store/... ./apps/core/internal/reconcile/..."`
Expected: PASS (mongostore integration skip). `-count=5` vì log chung có thêm một kho ghi và reader có goroutine. Các test feed cũ (`TestFeedWithoutRoomsSeesOnlyMessages`, `TestEditFeedContract`, `TestReactionFeedContract`, `TestPinFeedContract`, `TestDecodeChangeReadsMessagesAndRooms`, `TestDecodeChangeRejectsOtherCollectionsAndBrokenDocuments`) không đổi.

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/work/... ./apps/core/internal/effects/... ./apps/core/internal/resync/... ./apps/core/..."`
Expected: PASS (nats/mongo integration skip). `TestDecodeDefersWellFormedUnknownKinds` vẫn đúng (kind 9 vẫn lạ). `make lint` sạch. `wc -l apps/core/internal/store/memstore/change_log.go apps/core/internal/store/memstore/member_actions.go apps/core/internal/store/storetest/feed_room_cases.go apps/core/internal/store/mongostore/feed_skip_integration_test.go apps/core/internal/work/record.go apps/core/internal/work/record_test.go apps/core/effects_wiring.go` lần lượt 84, 110, 50, 76, 100, 175, 94; mọi file < 200.

**Step 7: Integration**

Run: `make itest`
Expected: PASS, gồm `TestMongoMemberFeedContract`, `TestMongoFeedContract` (room → fact 1 → tin, đúng thứ tự commit), `TestFeedSkipsSummaryPinActivityEditHideAndClearWrites` (update `members`/`user_rooms`/`rooms` không lên feed) và itest của `apps/core` (reader chuyển record `g:{room}-m1` của mỗi room tạo qua gRPC, worker chạy `room_activity` rồi ack; `work_failures_total` không tăng). Event update hay replace của `members` lọt lên feed (decode báo corrupt, `reconcile_dropped_total` tăng) → dừng, báo cáo kèm event thô.

**Step 8: Commit**

INDEXES.csv:
- dòng `apps/core/internal/store/mongostore`: trong cột purpose thay `(inserts into messages, rooms, message_edits, reactions and pin_actions plus` bằng `(inserts into messages, rooms, message_edits, reactions, pin_actions and member_actions plus` và `MessageInserted/RoomInserted/EditInserted/ReactionChanged/PinInserted; a reaction update` bằng `MessageInserted/RoomInserted/EditInserted/ReactionChanged/PinInserted/MemberInserted (feed_member_change.go); a reaction update`; cột decisions thêm `;D106` vào cuối.
- dòng `apps/core/internal/store/memstore`: trong cột purpose thay `plus reaction changes and pin facts with the WithReactions/WithPins options` bằng `plus reaction changes and pin facts with the WithReactions/WithPins options and the member facts of the given rooms (Rooms.attach also attaches Rooms.MemberActions())`; cột decisions thêm `;D106` vào cuối.
- dòng `apps/core/internal/store/storetest`: trong cột purpose thay `pin feed (RunPinFeed: facts in commit order with their content, a refused append adds none);` bằng `pin feed (RunPinFeed: facts in commit order with their content, a refused append adds none); member feed (RunMemberFeed: facts in commit order with their content, a refused append adds none); each room insert on the room feed is followed by its fact 1;`; trong cột key_symbols thay `PinnableRooms;RunMembers;MemberRooms` bằng `PinnableRooms;RunMembers;MemberRooms;RunMemberFeed`; cột decisions thêm `;D106` vào cuối.
- dòng `apps/core/internal/work`: trong cột purpose thay `RecordOf maps MemberInserted to room + mv as seq (KnownKind and the g: id come with the feed);` bằng `MemberInserted carries mv as seq;`, thay `(message, room, edit, reaction, pin);` bằng `(message, room, edit, reaction, pin, member);` và `and p:{room}-p{pv} become Nats-Msg-Id` bằng `p:{room}-p{pv} and g:{room}-m{mv} become Nats-Msg-Id`; cột decisions thay `D66;D79;D80;D84;D91` bằng `D66;D79;D80;D84;D91;D106`.
- dòng `apps/core/internal/reconcile`: trong cột purpose thay `MessageInserted/RoomInserted/EditInserted/ReactionChanged/PinInserted change` bằng `MessageInserted/RoomInserted/EditInserted/ReactionChanged/PinInserted/MemberInserted change`; cột decisions thay `D51;D52;D66;D76;D79;D80;D91` bằng `D51;D52;D66;D76;D79;D80;D91;D106`.
- dòng `apps/core`: trong cột purpose thay `PinInserted -> room_activity, pin_projection, pin_event (reaction_pin_effects_wiring.go builds the four new effects with their own counter and pin projector)` bằng `PinInserted -> room_activity, pin_projection, pin_event (reaction_pin_effects_wiring.go builds the four new effects with their own counter and pin projector); MemberInserted -> room_activity only until the member effects land`; cột decisions thay `D90;D92;D93;D95` (cuối cột) bằng `D90;D92;D93;D95;D106`.

```bash
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
make fmt-check && make vet && make lint
git add apps/core/internal/store/storetest/feed_member_cases.go apps/core/internal/store/mongostore/feed_member_change.go apps/core/internal/store/mongostore/feed_member_change_test.go apps/core/internal/reconcile/member_forward_test.go
git commit -m "feat(reconcile): forward member facts to the work stream" -- apps/core/internal/store/memstore/ apps/core/internal/store/storetest/ apps/core/internal/store/mongostore/ apps/core/internal/work/ apps/core/internal/reconcile/ apps/core/effects_wiring.go INDEXES.csv
```

Expected: CSV in `{7}`; `git show --stat HEAD` chỉ có file của task.

---

### Task 7: Package `memberproj` (`Settle`/`Project`), test trên memstore. **Push**

`memberproj` là projection dùng chung cho fast path (`mutate` gọi `Settle` trước khi kiểm quyền, D97) và effect `member_projection` (gọi `Project(room, mv)`, Task 15). Không giữ trạng thái; mỗi nơi dựng một `Projector`.

Thuật toán (hợp đồng):
- Tối đa `MaxTries` lượt. Mỗi lượt: `Get` → `After(room, head, MaxMemberScan)`; rỗng → trả room (Project: `head < target` → `store.ErrStaleRead`).
- Có fact: `ApplyMembers` từng fact theo thứ tự; fact có `MV ≠ head+1` (khe) → dừng, `store.ErrStaleRead`, không CAS đầu.
- Sau khi áp cả trang: `AdvanceMembers(room, head đã đọc, last.MV, last.Count)`.
  - Khớp: trang đầy → lặp trang sau (không tốn lượt); không đầy → trả room với `MemberVersion/MemberCount` mới (Project: `< target` → `ErrStaleRead`).
  - Trượt (người khác đã tiến đầu) → lượt sau đọc lại.
- Hết lượt → `ErrContended` (`UNAVAILABLE`).
- `Project(room, target)` trả ngay khi đầu đã `≥ target`.

Đầu `rooms.mv` chỉ tiến sau khi mọi doc của mọi fact tới đó đã áp (bất biến 1 của "Ghi chú tích hợp"); trượt CAS không làm hỏng gì vì `ApplyMembers` idempotent nhờ guard `mv`.

**Files:**
- Create: `apps/core/internal/memberproj/memberproj.go`, `apps/core/internal/memberproj/fixtures_test.go`, `apps/core/internal/memberproj/memberproj_test.go`
- Modify: `INDEXES.csv` (dòng mới `apps/core/internal/memberproj`)

**Step 1: Test**

`apps/core/internal/memberproj/fixtures_test.go`:

```go
package memberproj_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/memberproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

const room uint64 = 42

var (
	at      = time.UnixMilli(1_700_000_000_000).UTC()
	errBoom = errors.New("boom")
)

func newRooms(t *testing.T) *memstore.Rooms {
	t.Helper()
	rooms := memstore.NewRooms()
	r := domain.Room{ID: room, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: at, MemberCount: 2}
	members := []domain.Member{
		{Room: room, Tenant: "acme", User: "alice", Role: domain.RoleOwner, JoinedAt: at},
		{Room: room, Tenant: "acme", User: "bob", Role: domain.RoleMember, JoinedAt: at},
	}
	if err := rooms.Create(t.Context(), r, members); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return rooms
}

func act(mv uint64, op domain.MemberOp, count int, c domain.MemberChange) domain.MemberAction {
	return domain.MemberAction{Room: room, MV: mv, Tenant: "acme", Op: op, Changes: []domain.MemberChange{c}, By: "alice", At: at, Count: count}
}

func addCarol(mv uint64) domain.MemberAction {
	return act(mv, domain.MemberOpAdd, 3, domain.MemberChange{User: "carol", Role: domain.RoleMember, ReadSeq: 4})
}

func appendActs(t *testing.T, rooms *memstore.Rooms, acts ...domain.MemberAction) {
	t.Helper()
	for _, a := range acts {
		if err := rooms.MemberActions().Append(t.Context(), a); err != nil {
			t.Fatalf("Append(v%d): %v", a.MV, err)
		}
	}
}

func projector(t *testing.T, facts memberproj.Facts, rooms memberproj.Rooms) *memberproj.Projector {
	t.Helper()
	p, err := memberproj.New(facts, rooms)
	if err != nil {
		t.Fatalf("memberproj.New: %v", err)
	}
	return p
}

func head(t *testing.T, rooms *memstore.Rooms) domain.Room {
	t.Helper()
	r, err := rooms.Get(t.Context(), room)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return r
}

type countingRooms struct {
	*memstore.Rooms
	advances, lose int
}

func (c *countingRooms) AdvanceMembers(ctx context.Context, id, base, mv uint64, count int) (bool, error) {
	c.advances++
	if c.lose > 0 {
		c.lose--
		return false, nil
	}
	return c.Rooms.AdvanceMembers(ctx, id, base, mv, count)
}

type rival struct {
	*memstore.Rooms
	raced bool
}

func (r *rival) AdvanceMembers(ctx context.Context, id, base, mv uint64, count int) (bool, error) {
	if !r.raced {
		r.raced = true
		if _, err := r.Rooms.AdvanceMembers(ctx, id, base, mv, count); err != nil {
			return false, err
		}
		return false, nil
	}
	return r.Rooms.AdvanceMembers(ctx, id, base, mv, count)
}

type failingRooms struct{ *memstore.Rooms }

func (failingRooms) ApplyMembers(context.Context, domain.MemberAction) error { return errBoom }
```

`apps/core/internal/memberproj/memberproj_test.go`:

```go
package memberproj_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/memberproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestSettleOfAProjectedRoomWritesNothing(t *testing.T) {
	rooms := &countingRooms{Rooms: newRooms(t)}
	got, err := projector(t, rooms.MemberActions(), rooms).Settle(t.Context(), room)
	if err != nil || got.MemberVersion != 1 || got.MemberCount != 2 || rooms.advances != 0 {
		t.Fatalf("Settle = %+v, %v after %d advances; want head 1 with 2 members and no write", got, err, rooms.advances)
	}
}

func TestSettleProjectsEveryPendingFactAndMovesTheHead(t *testing.T) {
	rooms := newRooms(t)
	appendActs(t, rooms, addCarol(2), act(3, domain.MemberOpRemove, 2, domain.MemberChange{User: "bob", Prev: domain.RoleMember}))
	got, err := projector(t, rooms.MemberActions(), rooms).Settle(t.Context(), room)
	if err != nil || got.MemberVersion != 3 || got.MemberCount != 2 {
		t.Fatalf("Settle = %+v, %v; want head 3 with 2 members", got, err)
	}
	if stored := head(t, rooms); stored.MemberVersion != 3 || stored.MemberCount != 2 {
		t.Fatalf("stored head = %+v, want version 3 and 2 members", stored)
	}
	if m, err := rooms.Member(t.Context(), room, "carol"); err != nil || m.MV != 2 || m.Read != (domain.ReadPos{Seq: 4, Version: 1}) {
		t.Fatalf("carol = %+v, %v; want added at v2 with read seq 4", m, err)
	}
	if _, err := rooms.Member(t.Context(), room, "bob"); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("bob = %v, want ErrNotMember after the remove", err)
	}
}

func TestProjectReturnsAtOnceAtOrPastTheTarget(t *testing.T) {
	rooms := &countingRooms{Rooms: newRooms(t)}
	appendActs(t, rooms.Rooms, addCarol(2))
	p := projector(t, rooms.MemberActions(), rooms)
	for _, target := range []uint64{2, 1, 2} {
		if got, err := p.Project(t.Context(), room, target); err != nil || got.MemberVersion != 2 {
			t.Fatalf("Project(%d) = %+v, %v; want head 2", target, got, err)
		}
	}
	if rooms.advances != 1 {
		t.Fatalf("AdvanceMembers ran %d times, want once", rooms.advances)
	}
}

func TestProjectIsStaleWhenTheFactsStopBelowTheTarget(t *testing.T) {
	rooms := newRooms(t)
	appendActs(t, rooms, addCarol(2))
	_, err := projector(t, rooms.MemberActions(), rooms).Project(t.Context(), room, 4)
	if !errors.Is(err, store.ErrStaleRead) || head(t, rooms).MemberVersion != 2 {
		t.Fatalf("Project(4) with facts up to 2 = %v, head %d; want ErrStaleRead after moving to 2", err, head(t, rooms).MemberVersion)
	}
}

func TestAGapInTheFactsIsStaleAndWritesNothing(t *testing.T) {
	rooms := newRooms(t)
	appendActs(t, rooms, act(3, domain.MemberOpRemove, 1, domain.MemberChange{User: "bob", Prev: domain.RoleMember}))
	_, err := projector(t, rooms.MemberActions(), rooms).Settle(t.Context(), room)
	if !errors.Is(err, store.ErrStaleRead) || head(t, rooms).MemberVersion != 1 {
		t.Fatalf("Settle over a gap = %v, head %d; want ErrStaleRead at head 1", err, head(t, rooms).MemberVersion)
	}
	if _, err := rooms.Member(t.Context(), room, "bob"); err != nil {
		t.Fatalf("bob = %v, want still a member: a fact after a gap is not applied", err)
	}
}

func TestALostAdvanceRereadsTheHead(t *testing.T) {
	rooms := newRooms(t)
	appendActs(t, rooms, addCarol(2))
	got, err := projector(t, rooms.MemberActions(), &rival{Rooms: rooms}).Project(t.Context(), room, 2)
	if err != nil || got.MemberVersion != 2 || got.MemberCount != 3 {
		t.Fatalf("Project after a rival advance = %+v, %v; want head 2 with 3 members", got, err)
	}
}

func TestSettleGivesUpAfterMaxTries(t *testing.T) {
	rooms := &countingRooms{Rooms: newRooms(t), lose: memberproj.MaxTries}
	appendActs(t, rooms.Rooms, addCarol(2))
	_, err := projector(t, rooms.MemberActions(), rooms).Settle(t.Context(), room)
	if !errors.Is(err, memberproj.ErrContended) || !errors.Is(err, apperr.ErrUnavailable) || rooms.advances != memberproj.MaxTries {
		t.Fatalf("Settle = %v after %d advances, want ErrContended after %d", err, rooms.advances, memberproj.MaxTries)
	}
}

func TestSettlePagesThroughMoreThanOneScan(t *testing.T) {
	rooms := &countingRooms{Rooms: newRooms(t)}
	last := uint64(store.MaxMemberScan + 2)
	for mv := uint64(2); mv <= last; mv++ {
		role, prev := domain.RoleAdmin, domain.RoleMember
		if mv%2 == 1 {
			role, prev = domain.RoleMember, domain.RoleAdmin
		}
		appendActs(t, rooms.Rooms, act(mv, domain.MemberOpRole, 2, domain.MemberChange{User: "bob", Role: role, Prev: prev}))
	}
	got, err := projector(t, rooms.MemberActions(), rooms).Settle(t.Context(), room)
	if err != nil || got.MemberVersion != last || rooms.advances != 2 {
		t.Fatalf("Settle over %d facts = %+v, %v after %d advances; want head %d after 2", last-1, got, err, rooms.advances, last)
	}
	if m, err := rooms.Member(t.Context(), room, "bob"); err != nil || m.Role != domain.RoleAdmin || m.MV != last {
		t.Fatalf("bob = %+v, %v; want admin at v%d", m, err, last)
	}
}

func TestStoreErrorsAndMissingDeps(t *testing.T) {
	empty := memstore.NewRooms()
	p := projector(t, empty.MemberActions(), empty)
	if _, err := p.Settle(t.Context(), room); !errors.Is(err, domain.ErrRoomNotFound) {
		t.Fatalf("Settle(missing room) = %v, want ErrRoomNotFound", err)
	}
	if _, err := p.Project(t.Context(), room, 1); !errors.Is(err, domain.ErrRoomNotFound) {
		t.Fatalf("Project(missing room) = %v, want ErrRoomNotFound", err)
	}
	rooms := newRooms(t)
	appendActs(t, rooms, addCarol(2))
	if _, err := projector(t, rooms.MemberActions(), failingRooms{rooms}).Settle(t.Context(), room); !errors.Is(err, errBoom) || head(t, rooms).MemberVersion != 1 {
		t.Fatalf("Settle with a failing apply = %v, head %d; want errBoom at head 1", err, head(t, rooms).MemberVersion)
	}
	if _, err := memberproj.New(nil, rooms); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("New(nil facts) = %v, want ErrInvalidArgument", err)
	}
	if _, err := memberproj.New(rooms.MemberActions(), nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("New(nil rooms) = %v, want ErrInvalidArgument", err)
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/memberproj/..."`
Expected: FAIL: `no non-test Go files in …/apps/core/internal/memberproj` (hoặc `undefined: memberproj.New`, `undefined: memberproj.Facts`).

**Step 3: Code**

`apps/core/internal/memberproj/memberproj.go`:

```go
package memberproj

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const MaxTries = 5

type Facts interface {
	After(ctx context.Context, room, mv uint64, limit int) ([]domain.MemberAction, error)
}

type Rooms interface {
	Get(ctx context.Context, id uint64) (domain.Room, error)
	store.MemberProjector
}

var (
	ErrContended = fmt.Errorf("member projection contended: %w", apperr.ErrUnavailable)

	errMissingDeps = fmt.Errorf("%w: member projector needs facts and rooms", apperr.ErrInvalidArgument)
)

type Projector struct {
	facts Facts
	rooms Rooms
}

func New(facts Facts, rooms Rooms) (*Projector, error) {
	if facts == nil || rooms == nil {
		return nil, errMissingDeps
	}
	return &Projector{facts: facts, rooms: rooms}, nil
}

func (p *Projector) Settle(ctx context.Context, room uint64) (domain.Room, error) {
	return p.project(ctx, room, 0)
}

func (p *Projector) Project(ctx context.Context, room, target uint64) (domain.Room, error) {
	return p.project(ctx, room, target)
}

func (p *Projector) project(ctx context.Context, room, target uint64) (domain.Room, error) {
	for range MaxTries {
		r, err := p.rooms.Get(ctx, room)
		if err != nil {
			return domain.Room{}, fmt.Errorf("project members of room %d: %w", room, err)
		}
		got, done, err := p.catchUp(ctx, r, target)
		if err != nil || done {
			return got, err
		}
	}
	return domain.Room{}, fmt.Errorf("project members of room %d: %w", room, ErrContended)
}

func (p *Projector) catchUp(ctx context.Context, r domain.Room, target uint64) (domain.Room, bool, error) {
	for {
		if target > 0 && r.MemberVersion >= target {
			return r, true, nil
		}
		facts, err := p.facts.After(ctx, r.ID, r.MemberVersion, store.MaxMemberScan)
		if err != nil {
			return domain.Room{}, true, fmt.Errorf("member facts of room %d after v%d: %w", r.ID, r.MemberVersion, err)
		}
		if len(facts) == 0 {
			return reached(r, target)
		}
		next, advanced, err := p.apply(ctx, r, facts)
		switch {
		case err != nil:
			return domain.Room{}, true, err
		case !advanced:
			return domain.Room{}, false, nil
		case len(facts) < store.MaxMemberScan:
			return reached(next, target)
		}
		r = next
	}
}

func (p *Projector) apply(ctx context.Context, r domain.Room, facts []domain.MemberAction) (domain.Room, bool, error) {
	head := r.MemberVersion
	for _, a := range facts {
		if a.MV != head+1 {
			return domain.Room{}, false, fmt.Errorf("member fact v%d of room %d follows v%d: %w", a.MV, r.ID, head, store.ErrStaleRead)
		}
		if err := p.rooms.ApplyMembers(ctx, a); err != nil {
			return domain.Room{}, false, fmt.Errorf("apply member fact v%d of room %d: %w", a.MV, r.ID, err)
		}
		head = a.MV
	}
	last := facts[len(facts)-1]
	ok, err := p.rooms.AdvanceMembers(ctx, r.ID, r.MemberVersion, last.MV, last.Count)
	if err != nil {
		return domain.Room{}, false, fmt.Errorf("advance members of room %d to v%d: %w", r.ID, last.MV, err)
	}
	r.MemberVersion, r.MemberCount = last.MV, last.Count
	return r, ok, nil
}

func reached(r domain.Room, target uint64) (domain.Room, bool, error) {
	if r.MemberVersion < target {
		return domain.Room{}, true, fmt.Errorf("members of room %d reach v%d, want v%d: %w", r.ID, r.MemberVersion, target, store.ErrStaleRead)
	}
	return r, true, nil
}
```

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/memberproj/..."`
Expected: PASS. Không có goroutine mới, không cần `-count=5`. `wc -l apps/core/internal/memberproj/*.go` lần lượt `fixtures_test.go` 102, `memberproj.go` 113, `memberproj_test.go` 132.

**Step 5: INDEXES + commit + push**

INDEXES.csv, thêm dòng mới ngay sau dòng `apps/core/internal/pinproj`:

```csv
apps/core/internal/memberproj,package,"Member projection shared by the mutate fast path (Settle before any permission check) and the member_projection effect (Project), D97: up to MaxTries rounds of Rooms.Get, member facts after the head page by page (MaxMemberScan), ApplyMembers of each fact in mv order (a gap -> store.ErrStaleRead, no head move), then AdvanceMembers CAS of rooms.mv/mc from the head read (a lost CAS rereads; MaxTries lost -> ErrContended, unavailable); Project(room, target) returns at once when the head is at or past target and is store.ErrStaleRead when the facts stop below target; a missing room is domain.ErrRoomNotFound; store errors wrapped with %w; stateless",Facts;Rooms;Projector;New;Projector.Settle;Projector.Project;MaxTries;ErrContended,apps/core/internal/mutate;apps/core/internal/effects;apps/core,unit (memstore),D96;D97;D98
```

```bash
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
make fmt-check && make vet && make lint
git add apps/core/internal/memberproj/memberproj.go apps/core/internal/memberproj/fixtures_test.go apps/core/internal/memberproj/memberproj_test.go
git commit -m "feat(memberproj): add the shared member projector" -- apps/core/internal/memberproj/ INDEXES.csv
git push origin feat/m2b
```

Expected: CSV in `{7}`; push thành công; `git status --short` không còn file nào của Part A.

### Task 8: ★ Actor: cache member theo thế hệ + TTL, `Router.ForgetMembers`

Hôm nay `actor.member()` giữ member trong LRU 1024 phần tử, không hết hạn, không ai xoá: một room bận không bao giờ quên người đã bị xoá, nên người đó vẫn gửi tin được (validation, lỗ hổng 1). D103 sửa bằng hai lớp:
- **Thế hệ.** Mỗi actor có `memberGen atomic.Uint64` và `seenGen uint64`. `Router.ForgetMembers(room)` giữ `r.mu.RLock`, actor của room có thì `memberGen.Add(1)`, không có thì thôi (actor mới dựng sau đó có cache rỗng). Đầu `member()`, actor so `memberGen.Load()` với `seenGen`: khác → dựng LRU mới, ghi `seenGen`. Đọc thế hệ **trước** khi đọc store, nên một lần đọc cũ chen giữa projection và `ForgetMembers` chỉ sống tới lệnh kế tiếp.
- **TTL.** Entry LRU là `cachedMember{m, at}`; quá `memberCacheTTL` (10s) thì đọc lại store. TTL chặn trường hợp lệnh member chạy trên core khác (core đó gọi `ForgetMembers` của chính nó, actor nằm ở core này).
- Kết quả "không phải member" không bao giờ được cache (giữ hành vi cũ, thêm `remove` để một entry cũ không sống tiếp).

Logic cache đặt trong file mới `member_cache.go` (`room_actor.go` đã 170 dòng). `room_state.go` bỏ hàm `member` cũ. Từ Task 8 tới Task 10 chưa ai gọi `ForgetMembers`; chỉ TTL có tác dụng.

Test dùng `rooms.ApplyMembers` của memstore (Task 4) để xoá bob trực tiếp khỏi projection, không qua `mutate`.

**Files:**
- Create: `apps/core/internal/actor/member_cache.go`
- Modify: `apps/core/internal/actor/room_state.go`, `room_actor.go`, `export_test.go`
- Create: `apps/core/internal/actor/member_cache_test.go`
- Modify: `INDEXES.csv`

**Step 0: Công cụ sửa `INDEXES.csv` cho Part B (một lần, không commit)**

Các task 8–14 sửa `INDEXES.csv` bằng một script đọc JSON từ stdin, chỉ ghi lại đúng các dòng được sửa (các dòng khác giữ nguyên byte, kể cả ngoặc kép thừa), tự đặt ngoặc kép cho field có dấu phẩy, và in tập số cột (phải là `{7}`). Tạo `<scratchpad>/indexes_edit.py` (`<scratchpad>` là thư mục scratchpad của phiên thực thi):

```python
import csv, io, json, sys

COLS = {"kind": 1, "purpose": 2, "key_symbols": 3, "used_by": 4, "tests": 5, "decisions": 6}
ops = json.load(sys.stdin)
lines = open("INDEXES.csv", newline="").read().split("\n")


def find(path):
    hits = [i for i, l in enumerate(lines) if l and next(csv.reader([l]))[0] == path]
    return hits[0] if hits else None


def dump(row):
    buf = io.StringIO()
    csv.writer(buf, lineterminator="").writerow(row)
    return buf.getvalue()


for op in ops:
    i = find(op["path"])
    if "row" in op:
        if i is not None:
            sys.exit("row exists: " + op["path"])
        j = find(op["after"])
        if j is None:
            sys.exit("no row to insert after: " + op["after"])
        lines.insert(j + 1, dump([op["path"]] + op["row"]))
        continue
    if i is None:
        sys.exit("no row: " + op["path"])
    row = next(csv.reader([lines[i]]))
    c = COLS[op["col"]]
    if "replace" in op:
        old, new = op["replace"]
        if old not in row[c]:
            sys.exit("text not found in %s %s: %s" % (op["path"], op["col"], old))
        row[c] = row[c].replace(old, new, 1)
    else:
        row[c] += op["append"]
    lines[i] = dump(row)

open("INDEXES.csv", "w", newline="").write("\n".join(lines))
print({len(r) for r in csv.reader(open("INDEXES.csv", newline="")) if r})
```

Cách dùng: `python3 <scratchpad>/indexes_edit.py <<'EOF'` + mảng JSON + `EOF`, chạy từ gốc repo. Script dừng với thông báo nếu không thấy dòng hoặc đoạn cần thay; khi đó dừng task và báo controller (có thể Part A/C đã đổi dòng đó).

Kiểm nhanh script không đổi file khi không có thao tác:

```bash
echo '[]' | python3 <scratchpad>/indexes_edit.py && git diff --stat INDEXES.csv
```

Expected: in `{7}`; `git diff --stat` rỗng.

**Step 1: Test**

`apps/core/internal/actor/export_test.go` (thay cả file):

```go
package actor

const MemberCacheTTL = memberCacheTTL

func (r *Router) ActorCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.actors)
}
```

`apps/core/internal/actor/member_cache_test.go`:

```go
package actor_test

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func removeMember(t *testing.T, rg *rig, room uint64, user string, mv uint64) {
	t.Helper()
	a := domain.MemberAction{
		Room: room, MV: mv, Tenant: tenant, Op: domain.MemberOpRemove, By: "alice", At: time.Now().UTC(), Count: 1,
		Changes: []domain.MemberChange{{User: user, Prev: domain.RoleMember}},
	}
	if err := rg.rooms.ApplyMembers(context.Background(), a); err != nil {
		t.Fatalf("remove %s from room %d: %v", user, room, err)
	}
}

func TestForgetMembersDropsARemovedMemberAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig).start(t)
		mustSend(t, rg.Router, cmd(roomA, "bob", "b1"))
		mustSend(t, rg.Router, cmd(roomB, "bob", "b1"))
		removeMember(t, rg, roomA, "bob", 2)
		mustSend(t, rg.Router, cmd(roomA, "bob", "b2"))
		rg.ForgetMembers(roomB)
		mustSend(t, rg.Router, cmd(roomA, "bob", "b3"))
		rg.ForgetMembers(roomA)
		_, err := rg.Send(t.Context(), cmd(roomA, "bob", "b4"))
		expectErr(t, err, domain.ErrNotMember)
		if ack := mustSend(t, rg.Router, cmd(roomA, "alice", "a1")); ack.Seq != 4 {
			t.Fatalf("alice got seq %d after the forget, want 4", ack.Seq)
		}
		if ack := mustSend(t, rg.Router, cmd(roomB, "bob", "b2")); ack.Seq != 2 {
			t.Fatalf("bob in room %d got seq %d, want 2 (forgetting room %d keeps him there)", roomB, ack.Seq, roomA)
		}
	})
}

func TestCachedMembershipExpiresAfterTheTTL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig).start(t)
		mustSend(t, rg.Router, cmd(roomA, "bob", "b1"))
		removeMember(t, rg, roomA, "bob", 2)
		time.Sleep(actor.MemberCacheTTL - time.Millisecond)
		mustSend(t, rg.Router, cmd(roomA, "bob", "b2"))
		if n := rg.rooms.memberCalls(); n != 1 {
			t.Fatalf("membership read %d times inside the TTL, want 1", n)
		}
		time.Sleep(time.Millisecond)
		_, err := rg.Send(t.Context(), cmd(roomA, "bob", "b3"))
		expectErr(t, err, domain.ErrNotMember)
		if n := rg.rooms.memberCalls(); n != 2 {
			t.Fatalf("membership read %d times, want 2 (read again once the TTL passed)", n)
		}
	})
}

func TestForgetMembersWithoutAnActorDoesNothing(t *testing.T) {
	rg := newRig(t, baseConfig)
	rg.ForgetMembers(roomA)
	rg.start(t)
	rg.ForgetMembers(roomA)
	mustSend(t, rg.Router, cmd(roomA, "alice", "c1"))
	mustSend(t, rg.Router, cmd(roomA, "alice", "c2"))
	if n := rg.rooms.memberCalls(); n != 1 {
		t.Fatalf("membership read %d times, want 1 (a forget before the actor existed changes nothing)", n)
	}
}

func TestForgetMembersIsSafeAlongsideSends(t *testing.T) {
	rg := started(t, baseConfig)
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 200 {
			rg.ForgetMembers(roomA)
			rg.ForgetMembers(roomB)
		}
	})
	for i := range 20 {
		if ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c"+strconv.Itoa(i))); ack.Seq != uint64(i+1) {
			t.Fatalf("send %d got seq %d", i, ack.Seq)
		}
	}
	wg.Wait()
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/actor/..."`
Expected: FAIL biên dịch: `undefined: memberCacheTTL` (`export_test.go`); có thể kèm `rg.ForgetMembers undefined (type *rig has no field or method ForgetMembers)`.

**Step 3: Code**

`apps/core/internal/actor/member_cache.go`:

```go
package actor

import (
	"context"
	"errors"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

const memberCacheTTL = 10 * time.Second

type cachedMember struct {
	m  domain.Member
	at time.Time
}

func (r *Router) ForgetMembers(room uint64) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if a := r.actors[room]; a != nil {
		a.memberGen.Add(1)
	}
}

func (a *actor) member(ctx context.Context, user string) (domain.Member, error) {
	if gen := a.memberGen.Load(); gen != a.seenGen {
		a.members, a.seenGen = newLRU[string, cachedMember](memberCacheSize), gen
	}
	now := time.Now()
	if c, ok := a.members.get(user); ok && now.Sub(c.at) < memberCacheTTL {
		return c.m, nil
	}
	m, err := a.r.rooms.Member(ctx, a.id, user)
	switch {
	case err == nil:
		a.members.put(user, cachedMember{m: m, at: now})
		return m, nil
	case errors.Is(err, domain.ErrNotMember):
		a.members.remove(user)
		return domain.Member{}, domain.ErrNotMember
	default:
		a.r.log.WarnContext(ctx, "membership check failed", "room", a.id, "err", err)
		return domain.Member{}, errUnavailable
	}
}
```

`apps/core/internal/actor/room_state.go`: xoá nguyên hàm `func (a *actor) member(...)` ở cuối file (16 dòng). Import giữ nguyên (`errors`, `fmt`, `access`, `domain`, `store` vẫn được `load`, `refresh`, `admit` dùng).

`apps/core/internal/actor/room_actor.go`:
- import thêm `"sync/atomic"` (khối stdlib, sau `"context"`; `"time"` giữ cuối khối).
- trong `type actor struct`, thay dòng `members *lru[string, domain.Member]` bằng:

```go
	members   *lru[string, cachedMember]
	memberGen atomic.Uint64
	seenGen   uint64
```

- trong `newActor`, thay `members: newLRU[string, domain.Member](memberCacheSize),` bằng `members: newLRU[string, cachedMember](memberCacheSize),`.

Chạy `make -s go ARGS="fmt ./apps/core/internal/actor/"` để gofmt căn cột khối field.

**Step 4: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/actor/..."
make vet
```

Expected: PASS 5 lần (test cũ `TestSendChecksMembershipAndCachesOnlyMembers` vẫn pass: trong 10s alice vẫn được cache). `wc -l apps/core/internal/actor/room_actor.go apps/core/internal/actor/room_state.go apps/core/internal/actor/member_cache.go apps/core/internal/actor/member_cache_test.go`: ~173, ~83, ~48, ~105.

**Step 5: INDEXES + commit**

```bash
python3 <scratchpad>/indexes_edit.py <<'EOF'
[
 {"path": "apps/core/internal/actor", "col": "purpose", "append": "; caches members per actor for memberCacheTTL (10s) and drops the whole cache when Router.ForgetMembers bumps the actor's generation, so a removed member stops sending at once on the core that ran the member command and within 10s on any other core (D103)"},
 {"path": "apps/core/internal/actor", "col": "key_symbols", "append": ";Router.ForgetMembers"},
 {"path": "apps/core/internal/actor", "col": "decisions", "append": ";D103"}
]
EOF
make -s go ARGS="fmt ./apps/core/..."
make fmt-check && make vet && make lint
git add apps/core/internal/actor/member_cache.go apps/core/internal/actor/member_cache_test.go
git commit -m "feat(actor): forget cached members by generation and after a TTL" -- apps/core/internal/actor/ INDEXES.csv
```

Expected: script in `{7}`; lint sạch; `git show --stat HEAD` có đúng 6 file (`member_cache.go`, `member_cache_test.go`, `room_state.go`, `room_actor.go`, `export_test.go`, `INDEXES.csv`).

Task rủi ro: một reviewer (đọc thế hệ trước khi đọc store, `ForgetMembers` chỉ giữ `RLock`, không cache "không phải member", TTL so bằng `time.Now()` của actor; tối đa `-count=3` trên `actor`).

---

### Task 9: ★ `access`: 5 action member/đọc, `Request.Target`/`Role`, `DefaultPolicy`, `AdmitRoom`

Quyền member đi qua `access.Policy` như mọi action khác (D86, D99). `mutate` (Task 10) hỏi policy **sau** settle, với doc caller (`Request.Member`) và doc đích (`Request.Target`, zero khi đích chưa từng là member) ở đúng `mv` của đầu room, cùng role được yêu cầu (`Request.Role`).

Luật `DefaultPolicy` (owner chốt 2026-10-06):

| Action | Cho phép |
|---|---|
| `add_members` | caller owner hoặc admin |
| `remove_member` | caller owner; hoặc caller admin và `Target.Role == member` |
| `change_member_role` | chỉ caller owner (kể cả hạ owner khác; luật owner cuối nằm ở `mutate`) |
| `leave_room`, `mark_read` | luôn cho phép |
| `edit_message`, `delete_message` | như cũ: tác giả, loại tin không bị khoá |
| còn lại | cho phép |

`LockedKinds` chỉ áp cho sửa/xoá. `AllowMembers` không đổi. Admin xoá một user chưa từng là member (`Target` zero) bị từ chối, owner thì được (lệnh thành no-op ở `mutate`); đúng chữ hợp đồng "`Target.Role == member`".

`Checker.AdmitRoom` (D100) giống `Admit` nhưng `ErrNotMember` không phải lỗi: trả `Request` có `Room`, `User`, `Action` và `Member` zero. `LeaveRoom` dùng nó để một lần rời gửi lại thành no-op thay vì `PERMISSION_DENIED`. Tenant sai và room không có vẫn là `ErrRoomNotFound`; lỗi store khác vẫn trả về. `Admit` và `AdmitRoom` dùng chung bước `enter` (đọc room + kiểm tenant).

**Files:**
- Modify: `apps/core/internal/access/policy.go`, `checker.go`
- Create: `apps/core/internal/access/member_policy_test.go`, `admit_room_test.go`
- Modify: `INDEXES.csv`

**Step 1: Test**

`apps/core/internal/access/member_policy_test.go`:

```go
package access_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func TestMemberActionNames(t *testing.T) {
	for action, name := range map[access.Action]string{
		access.AddMembers: "add_members", access.RemoveMember: "remove_member", access.LeaveRoom: "leave_room",
		access.ChangeMemberRole: "change_member_role", access.MarkRead: "mark_read",
	} {
		if string(action) != name {
			t.Fatalf("action %q, want %q", action, name)
		}
	}
}

func TestDefaultPolicyMemberRules(t *testing.T) {
	const owner, admin, member, none = domain.RoleOwner, domain.RoleAdmin, domain.RoleMember, domain.Role("")
	cases := []struct {
		name           string
		action         access.Action
		caller, target domain.Role
		want           error
	}{
		{"owner adds", access.AddMembers, owner, none, nil},
		{"admin adds", access.AddMembers, admin, none, nil},
		{"member adds", access.AddMembers, member, none, access.ErrDenied},
		{"owner removes an owner", access.RemoveMember, owner, owner, nil},
		{"owner removes an admin", access.RemoveMember, owner, admin, nil},
		{"owner removes a member", access.RemoveMember, owner, member, nil},
		{"owner removes a non-member", access.RemoveMember, owner, none, nil},
		{"admin removes a member", access.RemoveMember, admin, member, nil},
		{"admin removes an admin", access.RemoveMember, admin, admin, access.ErrDenied},
		{"admin removes an owner", access.RemoveMember, admin, owner, access.ErrDenied},
		{"admin removes a non-member", access.RemoveMember, admin, none, access.ErrDenied},
		{"member removes a member", access.RemoveMember, member, member, access.ErrDenied},
		{"owner changes a role", access.ChangeMemberRole, owner, member, nil},
		{"owner demotes an owner", access.ChangeMemberRole, owner, owner, nil},
		{"admin changes a role", access.ChangeMemberRole, admin, member, access.ErrDenied},
		{"member changes a role", access.ChangeMemberRole, member, member, access.ErrDenied},
		{"owner leaves", access.LeaveRoom, owner, none, nil},
		{"member leaves", access.LeaveRoom, member, none, nil},
		{"non-member leaves", access.LeaveRoom, none, none, nil},
		{"member marks read", access.MarkRead, member, none, nil},
	}
	policies := map[string]access.DefaultPolicy{
		"nothing locked": {},
		"text locked":    {LockedKinds: []domain.Kind{domain.KindText}},
	}
	for _, c := range cases {
		for label, p := range policies {
			req := access.Request{
				Action: c.action, User: "alice", Kind: domain.KindText, Role: domain.RoleAdmin,
				Member: domain.Member{User: "alice", Role: c.caller}, Target: domain.Member{User: "bob", Role: c.target},
			}
			if err := p.Check(t.Context(), req); !errors.Is(err, c.want) {
				t.Fatalf("%s (%s) = %v, want %v", c.name, label, err, c.want)
			}
		}
	}
}

func TestAllowMembersAllowsMemberActions(t *testing.T) {
	for _, action := range []access.Action{access.AddMembers, access.RemoveMember, access.ChangeMemberRole} {
		req := access.Request{Action: action, User: "bob", Member: domain.Member{User: "bob", Role: domain.RoleMember}}
		if err := (access.AllowMembers{}).Check(t.Context(), req); err != nil {
			t.Fatalf("AllowMembers %s = %v, want nil", action, err)
		}
	}
}
```

`apps/core/internal/access/admit_room_test.go`:

```go
package access_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

type brokenMembers struct{ *memstore.Rooms }

func (brokenMembers) Member(context.Context, uint64, string) (domain.Member, error) {
	return domain.Member{}, errors.New("members collection down")
}

func withRemovedBob(t *testing.T) *memstore.Rooms {
	t.Helper()
	rs := rooms(t)
	at := time.Now().UTC()
	for _, a := range []domain.MemberAction{
		{Room: room, MV: 2, Tenant: "acme", Op: domain.MemberOpAdd, By: "alice", At: at, Count: 2, Changes: []domain.MemberChange{{User: "bob", Role: domain.RoleMember}}},
		{Room: room, MV: 3, Tenant: "acme", Op: domain.MemberOpRemove, By: "alice", At: at, Count: 1, Changes: []domain.MemberChange{{User: "bob", Prev: domain.RoleMember}}},
	} {
		if err := rs.ApplyMembers(t.Context(), a); err != nil {
			t.Fatalf("apply member fact %d: %v", a.MV, err)
		}
	}
	return rs
}

func TestAdmitRoomLetsANonMemberInWithoutAMember(t *testing.T) {
	asked := 0
	deny := access.PolicyFunc(func(context.Context, access.Request) error { asked++; return access.ErrDenied })
	c, err := access.NewChecker(withRemovedBob(t), deny)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	for _, user := range []string{"bob", "mallory"} {
		req, err := c.AdmitRoom(t.Context(), access.LeaveRoom, "acme", user, room)
		if err != nil || req.Room.ID != room || req.User != user || req.Action != access.LeaveRoom || req.Member.User != "" {
			t.Fatalf("AdmitRoom(%s) = %+v, %v; want the room without a member", user, req, err)
		}
		if _, err := c.Admit(t.Context(), access.LeaveRoom, "acme", user, room); !errors.Is(err, domain.ErrNotMember) {
			t.Fatalf("Admit(%s) = %v, want ErrNotMember", user, err)
		}
	}
	req, err := c.AdmitRoom(t.Context(), access.LeaveRoom, "acme", "alice", room)
	if err != nil || req.Member.User != "alice" || req.Member.Role != domain.RoleOwner {
		t.Fatalf("AdmitRoom(alice) = %+v, %v; want her owner membership", req, err)
	}
	cases := map[string]struct {
		tenant string
		room   uint64
	}{
		"unknown room": {"acme", 1},
		"other tenant": {"other", room},
	}
	for name, tc := range cases {
		if _, err := c.AdmitRoom(t.Context(), access.LeaveRoom, tc.tenant, "alice", tc.room); !errors.Is(err, domain.ErrRoomNotFound) {
			t.Fatalf("%s: AdmitRoom = %v, want ErrRoomNotFound", name, err)
		}
	}
	if asked != 0 {
		t.Fatalf("policy asked %d times by AdmitRoom, want 0", asked)
	}
}

func TestAdmitRoomPassesStoreFailuresOn(t *testing.T) {
	c, err := access.NewChecker(brokenMembers{rooms(t)}, nil)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	if _, err := c.AdmitRoom(t.Context(), access.LeaveRoom, "acme", "alice", room); err == nil || !strings.Contains(err.Error(), "members collection down") {
		t.Fatalf("AdmitRoom with a broken member store = %v, want that failure", err)
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/access/..."`
Expected: FAIL biên dịch (trình biên dịch dừng ở 10 lỗi, `too many errors`), các lỗi thuộc nhóm: `undefined: access.AddMembers`, `undefined: access.RemoveMember`, `undefined: access.LeaveRoom`, `undefined: access.ChangeMemberRole`, `undefined: access.MarkRead`, `unknown field Role in struct literal of type access.Request`, `unknown field Target in struct literal of type access.Request`, `c.AdmitRoom undefined (type *access.Checker has no field or method AdmitRoom)`.

**Step 3: Code**

`apps/core/internal/access/policy.go` (thay cả file):

```go
package access

import (
	"context"
	"fmt"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type Action string

const (
	ReadHistory      Action = "read_history"
	SendMessage      Action = "send_message"
	EditMessage      Action = "edit_message"
	DeleteMessage    Action = "delete_message"
	HideMessage      Action = "hide_message"
	ClearHistory     Action = "clear_history"
	ReadEditHistory  Action = "read_edit_history"
	ReactMessage     Action = "react_message"
	PinMessage       Action = "pin_message"
	UnpinMessage     Action = "unpin_message"
	AddMembers       Action = "add_members"
	RemoveMember     Action = "remove_member"
	LeaveRoom        Action = "leave_room"
	ChangeMemberRole Action = "change_member_role"
	MarkRead         Action = "mark_read"
)

var ErrDenied = fmt.Errorf("action denied: %w", apperr.ErrPermissionDenied)

type Request struct {
	Action Action
	User   string
	Author string
	Kind   domain.Kind
	Room   domain.Room
	Member domain.Member
	Target domain.Member
	Role   domain.Role
}

type Policy interface {
	Check(ctx context.Context, req Request) error
}

type PolicyFunc func(ctx context.Context, req Request) error

func (f PolicyFunc) Check(ctx context.Context, req Request) error { return f(ctx, req) }

type AllowMembers struct{}

func (AllowMembers) Check(context.Context, Request) error { return nil }

type DefaultPolicy struct {
	LockedKinds []domain.Kind
}

func (p DefaultPolicy) Check(_ context.Context, req Request) error {
	caller := req.Member.Role
	switch req.Action {
	case EditMessage, DeleteMessage:
		return allowIf(!slices.Contains(p.LockedKinds, req.Kind) && req.Author == req.User)
	case AddMembers:
		return allowIf(caller == domain.RoleOwner || caller == domain.RoleAdmin)
	case RemoveMember:
		return allowIf(caller == domain.RoleOwner || (caller == domain.RoleAdmin && req.Target.Role == domain.RoleMember))
	case ChangeMemberRole:
		return allowIf(caller == domain.RoleOwner)
	default:
		return nil
	}
}

func allowIf(ok bool) error {
	if ok {
		return nil
	}
	return ErrDenied
}
```

`apps/core/internal/access/checker.go` (thay cả file):

```go
package access

import (
	"context"
	"errors"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type Rooms interface {
	Get(ctx context.Context, id uint64) (domain.Room, error)
	Member(ctx context.Context, room uint64, user string) (domain.Member, error)
}

type Checker struct {
	rooms  Rooms
	policy Policy
}

func NewChecker(rooms Rooms, policy Policy) (*Checker, error) {
	if rooms == nil {
		return nil, fmt.Errorf("%w: access checker needs a room store", apperr.ErrInvalidArgument)
	}
	if policy == nil {
		policy = DefaultPolicy{}
	}
	return &Checker{rooms: rooms, policy: policy}, nil
}

func (c *Checker) Admit(ctx context.Context, action Action, tenant, user string, room uint64) (Request, error) {
	req, err := c.enter(ctx, action, tenant, user, room)
	if err != nil {
		return Request{}, err
	}
	m, err := c.rooms.Member(ctx, room, user)
	if err != nil {
		return Request{}, err
	}
	req.Member = m
	return req, nil
}

func (c *Checker) AdmitRoom(ctx context.Context, action Action, tenant, user string, room uint64) (Request, error) {
	req, err := c.enter(ctx, action, tenant, user, room)
	if err != nil {
		return Request{}, err
	}
	m, err := c.rooms.Member(ctx, room, user)
	switch {
	case err == nil:
		req.Member = m
	case !errors.Is(err, domain.ErrNotMember):
		return Request{}, err
	}
	return req, nil
}

func (c *Checker) Allow(ctx context.Context, req Request) error {
	return c.policy.Check(ctx, req)
}

func (c *Checker) Authorize(ctx context.Context, action Action, tenant, user string, room uint64) (Request, error) {
	req, err := c.Admit(ctx, action, tenant, user, room)
	if err != nil {
		return Request{}, err
	}
	return req, c.Allow(ctx, req)
}

func (c *Checker) enter(ctx context.Context, action Action, tenant, user string, room uint64) (Request, error) {
	r, err := c.rooms.Get(ctx, room)
	if err != nil {
		return Request{}, err
	}
	if err := domain.CheckTenant(r, tenant); err != nil {
		return Request{}, err
	}
	return Request{Action: action, User: user, Room: r}, nil
}
```

**Step 4: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/internal/access/... ./apps/core/internal/actor/... ./apps/core/internal/mutate/... ./apps/core/internal/grpcsrv/..."
make vet
```

Expected: PASS (test cũ của `access`, actor, `mutate`, `grpcsrv` không đổi hành vi: edit/delete giữ luật cũ, action khác vẫn cho qua). `wc -l apps/core/internal/access/*.go`: `policy.go` ~83, `checker.go` ~82, `member_policy_test.go` ~85, `admit_room_test.go` ~90.

**Step 5: INDEXES + commit**

```bash
python3 <scratchpad>/indexes_edit.py <<'EOF'
[
 {"path": "apps/core/internal/access", "col": "purpose", "append": "; member actions add_members, remove_member, leave_room, change_member_role and mark_read (also used by MarkUnread); Request.Target is the target member doc (zero when never a member) and Request.Role the requested role; DefaultPolicy: owners do everything, admins add members and remove plain members, only owners change roles or remove admins and owners, leave_room and mark_read are always allowed, LockedKinds apply only to edit/delete (D99); Checker.AdmitRoom checks room and tenant and returns a zero Member for a non-member instead of ErrNotMember (LeaveRoom, D100)"},
 {"path": "apps/core/internal/access", "col": "key_symbols", "append": ";AddMembers;RemoveMember;LeaveRoom;ChangeMemberRole;MarkRead;Request.Target;Request.Role;Checker.AdmitRoom"},
 {"path": "apps/core/internal/access", "col": "decisions", "append": ";D99;D100"}
]
EOF
make -s go ARGS="fmt ./apps/core/..."
make fmt-check && make vet && make lint
git add apps/core/internal/access/member_policy_test.go apps/core/internal/access/admit_room_test.go
git commit -m "feat(access): member and read actions with owner and admin rules" -- apps/core/internal/access/ INDEXES.csv
```

Expected: `{7}`; `git show --stat HEAD` có 5 file.

Task rủi ro: một reviewer (bảng luật đúng chữ owner chốt, `LockedKinds` chỉ cho sửa/xoá, `AdmitRoom` không nuốt lỗi store, `Admit` giữ hành vi cũ; tối đa `-count=3` trên `access`).

---

### Task 10: ★ `mutate`: thêm/xoá/rời/đổi role, kế nhiệm owner, `Limits.MemberBatch`, Deps + wiring

Bốn lệnh member theo fact + projection (D96, D97, D99, D100). Lệnh là **trạng thái mong muốn**: đúng sẵn thì thành công, không ghi fact, không event. Thứ tự chung (engine `changeMembers` trong `member_commit.go`):
1. Validate riêng từng lệnh: `AddMembers` khử trùng `Users` giữ thứ tự, mỗi user `ValidUser`, rỗng → `invalid("users")`, quá `Limits.MemberBatch` user khác nhau → `ErrTooManyMembers`; `RemoveMember`/`ChangeMemberRole` kiểm `ValidUser(Target)`; `RemoveMember` chính mình → `ErrInvalidArgument` (dùng `LeaveRoom`); `ChangeMemberRole` kiểm `ParseRole`.
2. `Admit` (cổng sớm, có thể cũ); `LeaveRoom` dùng `AdmitRoom` (chỉ tenant).
3. Room DM → `ErrDirectRoom`.
4. `AddMembers`: đọc `Messages.Last(room, 0)` **một lần**; mỗi user được thêm có `ReadSeq` = giá trị đó (người mới thấy toàn bộ lịch sử, tin cũ coi như đã đọc; D104).
5. Tối đa `memberTries = 3` lượt: `MemberProj.Settle` → `MembersOf(caller + targets)` → caller không active → `ErrNotMember` (`LeaveRoom`: no-op thành công, không hỏi policy) → `Allow(Request{Member: caller, Target, Role})` → kế hoạch của lệnh (no-op, lỗi nghiệp vụ, hoặc fact) → `Append` fact `MV = head + 1`, `Count = head.Count ± Δ`, `At = now()` → trùng khoá: `At(room, mv)` có cùng `Op`, `By` và cùng tập `(user, role)` của `Changes` → đó là kết quả; khác → lượt sau.
6. Hết lượt → `domain.ErrRetryLater`.
7. Sau append: `MemberProj.Project(room, mv)` (lỗi bỏ qua) → `Forget.ForgetMembers(room)` → `Events.Enqueue(room, pbconv.MemberEvents(type, fact))` (lỗi bỏ qua) → `MemberResult` từ fact. No-op trả `{Version: head, Count: head.Count}`.

Kế hoạch từng lệnh:
- **Add**: mọi user chưa có doc hoặc đã bị xoá → change `{u, member, "", last}`; không ai → no-op. Thêm lại giữ `cb`, `rs.v` (projection nâng `rs.s` lên `ReadSeq`, `rs.v + 1`).
- **Remove**: đích không có doc hoặc đã bị xoá → no-op; không thì change `{u, "", role cũ}`, `Count − 1` (không âm).
- **Leave**: change `{caller, "", role cũ}`; caller là owner và `Owners(room, 2)` chỉ còn mình → `Successor(room)` (admin `ja` sớm nhất, rồi member, hoà theo user id) ghi vào `fact.Successor`; không còn ai thì không có kế nhiệm (room rỗng, `Count` 0).
- **Role**: đích không active → `ErrMemberNotFound`; cùng role → no-op (`Prev` = role đó); hạ một owner khi `Owners(room, 2)` chỉ còn một → `ErrLastOwner` (owner cuối không tự hạ được); không thì change `{u, role mới, role cũ}`, `Count` giữ nguyên.

Tập so khớp khi trùng khoá gồm cả role (không chỉ user), để hai lệnh đổi role của cùng một người về hai role khác nhau không nhận nhầm kết quả của nhau.

Đồng thời an toàn nhờ mv dày + settle-first (validation): hai admin cùng thêm → bên thua thử lại và chỉ thêm người chưa active; hai owner xoá nhau → bên thua đọc lại chính mình (đã bị xoá) → `ErrNotMember`; rời đua với đổi role → bên rời trượt khoá, tính lại người kế nhiệm. Test giả lập cuộc đua bằng `racingFacts`: bọc `store.MemberActions`, ngay trước `Append` của lệnh thì ghi một fact "đối thủ" ở đúng `mv` đó (projector vẫn đọc log thật nên lượt sau settle thấy fact đối thủ).

`Limits.MemberBatch` (`MEMBER_BATCH_MAX`, Task 14): `cmp.Or(…, DefaultMemberBatch)` (500), hợp lệ 2..`MaxMemberBatch` (1 sẽ chặn mọi DM) (= `domain.MaxMemberChanges` 1000). `Mutator.MemberBatch()` trả giá trị đã điền mặc định (Task 11 dùng cho `CreateRoom`).

`mutate.New` bắt buộc thêm `MemberActions`, `MemberReader`, `MemberProj`, `Forget`, nên task này sửa rig `mutate`, `TestNewRequiresEveryDependency`, rig `grpcsrv` (`fake_dependencies_test.go`: router của rig làm `Forget` khi có, không thì `nopForgetter`) và `apps/core/service_wiring.go` (`memberproj.New(st.MemberActions(), st)`, `Forget: router`). `grpcsrv/harness_test.go` (185 dòng) không đổi.

**Files:**
- Modify: `apps/core/internal/mutate/limits.go`, `mutator.go`
- Create: `apps/core/internal/mutate/members.go`, `member_roles.go`, `member_commit.go`
- Modify: `apps/core/internal/mutate/fixtures_test.go`, `delete_test.go`, `limits_test.go`
- Create: `apps/core/internal/mutate/member_helpers_test.go`, `members_test.go`, `member_rules_test.go`, `member_succession_test.go`, `member_race_test.go`, `member_failures_test.go`
- Modify: `apps/core/internal/grpcsrv/fake_dependencies_test.go`
- Modify: `apps/core/service_wiring.go`
- Modify: `INDEXES.csv`

**Step 1: Rig và test cũ**

`apps/core/internal/mutate/fixtures_test.go` (thay cả file):

```go
package mutate_test

import (
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/counter"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/memberproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	tenant        = "acme"
	room   uint64 = 4242
)

var (
	created = time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	errBoom = errors.New("boom")
)

type recordingEvents struct {
	mu     sync.Mutex
	rooms  []uint64
	events []*chatimv1.Event
	err    error
}

func (r *recordingEvents) Enqueue(room uint64, events []*chatimv1.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ev := range events {
		r.rooms = append(r.rooms, room)
		r.events = append(r.events, ev)
	}
	return r.err
}

func (r *recordingEvents) list() ([]uint64, []*chatimv1.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.rooms), slices.Clone(r.events)
}

type rig struct {
	m         *mutate.Mutator
	msgs      *memstore.Messages
	rooms     *memstore.Rooms
	edits     *memstore.Edits
	hidden    *memstore.Hidden
	reactions *memstore.Reactions
	pins      *memstore.Pins
	events    *recordingEvents
	forgets   *forgetSpy
	now       time.Time
}

func newRig(t *testing.T, policy access.Policy) *rig {
	t.Helper()
	rg := &rig{
		msgs: memstore.NewMessages(), rooms: memstore.NewRooms(), edits: memstore.NewEdits(), hidden: memstore.NewHidden(),
		reactions: memstore.NewReactions(), pins: memstore.NewPins(), events: &recordingEvents{}, forgets: &forgetSpy{},
		now: created.Add(time.Minute + 1500*time.Microsecond),
	}
	r := domain.Room{ID: room, Tenant: tenant, Type: domain.RoomGroup, Name: "team", CreatedBy: "alice", CreatedAt: created, MemberCount: 3}
	members := []domain.Member{
		{Room: room, Tenant: tenant, User: "alice", Role: domain.RoleOwner, JoinedAt: created},
		{Room: room, Tenant: tenant, User: "bob", Role: domain.RoleMember, JoinedAt: created},
		{Room: room, Tenant: tenant, User: "carol", Role: domain.RoleMember, JoinedAt: created},
	}
	if err := rg.rooms.Create(t.Context(), r, members); err != nil {
		t.Fatalf("create room: %v", err)
	}
	rg.m = rg.build(t, rg.deps(t, policy))
	return rg
}

func (rg *rig) deps(t *testing.T, policy access.Policy) mutate.Deps {
	t.Helper()
	checker, err := access.NewChecker(rg.rooms, policy)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	counts, err := counter.New(rg.msgs, rg.reactions)
	if err != nil {
		t.Fatalf("counter.New: %v", err)
	}
	projector, err := pinproj.New(rg.pins, rg.rooms)
	if err != nil {
		t.Fatalf("pinproj.New: %v", err)
	}
	members, err := memberproj.New(rg.rooms.MemberActions(), rg.rooms)
	if err != nil {
		t.Fatalf("memberproj.New: %v", err)
	}
	return mutate.Deps{
		Access: checker, Messages: rg.msgs, Edits: rg.edits, Hidden: rg.hidden, Rooms: rg.rooms, Events: rg.events,
		Reactions: rg.reactions, Counter: counts, Pins: rg.pins, Projector: projector, Now: func() time.Time { return rg.now },
		MemberActions: rg.rooms.MemberActions(), MemberReader: rg.rooms, MemberProj: members, Forget: rg.forgets,
	}
}

func (rg *rig) build(t *testing.T, d mutate.Deps) *mutate.Mutator {
	t.Helper()
	m, err := mutate.New(d)
	if err != nil {
		t.Fatalf("mutate.New: %v", err)
	}
	return m
}

func (rg *rig) mutator(t *testing.T, policy access.Policy, edits store.Edits) *mutate.Mutator {
	t.Helper()
	d := rg.deps(t, policy)
	d.Edits = edits
	return rg.build(t, d)
}

func (rg *rig) send(t *testing.T, seq uint64, from, text string) domain.Message {
	t.Helper()
	m := domain.Message{Room: room, Seq: seq, Tenant: tenant, From: from, Kind: domain.KindText, Text: text, CID: "c-" + strconv.FormatUint(seq, 10), CreatedAt: created}
	if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert seq %d: %+v", seq, res)
	}
	return m
}

func (rg *rig) stored(t *testing.T, seq uint64) domain.Message {
	t.Helper()
	found, err := rg.msgs.Find(t.Context(), room, []store.MsgKey{key(seq)})
	if err != nil || len(found) != 1 {
		t.Fatalf("find seq %d = %v, %v", seq, found, err)
	}
	return found[0]
}

func (rg *rig) facts(t *testing.T, seq uint64) []domain.Edit {
	t.Helper()
	got, err := rg.edits.History(t.Context(), key(seq), 0, store.MaxEditPage)
	if err != nil {
		t.Fatalf("history of seq %d: %v", seq, err)
	}
	return got
}

func (rg *rig) at() time.Time { return rg.now.Truncate(time.Millisecond) }

func key(seq uint64) store.MsgKey { return store.MsgKey{Room: room, Seq: seq} }

func edit(user string, seq uint64, base uint32, text string) mutate.EditCmd {
	return mutate.EditCmd{Tenant: tenant, User: user, Room: room, Seq: seq, BaseVersion: base, Text: text}
}

func del(user string, seq uint64, base uint32) mutate.DeleteCmd {
	return mutate.DeleteCmd{Tenant: tenant, User: user, Room: room, Seq: seq, BaseVersion: base}
}

func sameEdit(a, b domain.Edit) bool {
	at := a.At.Equal(b.At)
	a.At, b.At = time.Time{}, time.Time{}
	return at && a == b
}
```

`apps/core/internal/mutate/delete_test.go`, trong map của `TestNewRequiresEveryDependency`, sau dòng `"no emojis": ...` thêm:

```go
		"no member actions":   func(d *mutate.Deps) { d.MemberActions = nil },
		"no member reader":    func(d *mutate.Deps) { d.MemberReader = nil },
		"no member projector": func(d *mutate.Deps) { d.MemberProj = nil },
		"no forgetter":        func(d *mutate.Deps) { d.Forget = nil },
		"bad member batch":    func(d *mutate.Deps) { d.Limits = mutate.Limits{MemberBatch: mutate.MaxMemberBatch + 1} },
```

`apps/core/internal/mutate/limits_test.go`, trong bảng `cases` của `TestLimitsFillDefaultsAndCheckBounds`, sau dòng `{"negative pin limit", ...}` thêm:

```go
		{"member batch at the cap", mutate.Limits{MemberBatch: mutate.MaxMemberBatch}, ""},
		{"member batch over the cap", mutate.Limits{MemberBatch: mutate.MaxMemberBatch + 1}, "1001"},
		{"negative member batch", mutate.Limits{MemberBatch: -1}, "member batch -1"},
		{"member batch below a direct room", mutate.Limits{MemberBatch: 1}, "member batch 1"},
```

và cuối file thêm:

```go
func TestMemberBatchDefaultsTo500(t *testing.T) {
	rg := newRig(t, nil)
	if got := rg.m.MemberBatch(); got != mutate.DefaultMemberBatch || mutate.DefaultMemberBatch != 500 || mutate.MaxMemberBatch != 1000 {
		t.Fatalf("MemberBatch() = %d with default %d and cap %d, want 500 and 1000", got, mutate.DefaultMemberBatch, mutate.MaxMemberBatch)
	}
	d := rg.deps(t, nil)
	d.Limits = mutate.Limits{MemberBatch: 7}
	if got := rg.build(t, d).MemberBatch(); got != 7 {
		t.Fatalf("MemberBatch() = %d, want the configured 7", got)
	}
}
```

**Step 2: Test lệnh member**

`apps/core/internal/mutate/member_helpers_test.go`:

```go
package mutate_test

import (
	"slices"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type forgetSpy struct {
	mu    sync.Mutex
	rooms []uint64
	seen  func(room uint64)
}

func (f *forgetSpy) ForgetMembers(room uint64) {
	f.mu.Lock()
	f.rooms = append(f.rooms, room)
	seen := f.seen
	f.mu.Unlock()
	if seen != nil {
		seen(room)
	}
}

func (f *forgetSpy) calls() []uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.rooms)
}

func addCmd(by string, users ...string) mutate.AddMembersCmd {
	return mutate.AddMembersCmd{Tenant: tenant, User: by, Room: room, Users: users}
}

func removeCmd(by, target string) mutate.RemoveMemberCmd {
	return mutate.RemoveMemberCmd{Tenant: tenant, User: by, Room: room, Target: target}
}

func leaveCmd(by string) mutate.LeaveRoomCmd {
	return mutate.LeaveRoomCmd{Tenant: tenant, User: by, Room: room}
}

func roleCmd(by, target string, role domain.Role) mutate.ChangeRoleCmd {
	return mutate.ChangeRoleCmd{Tenant: tenant, User: by, Room: room, Target: target, Role: role}
}

func (rg *rig) add(t *testing.T, by string, users ...string) mutate.MemberResult {
	t.Helper()
	res, err := rg.m.AddMembers(t.Context(), addCmd(by, users...))
	if err != nil {
		t.Fatalf("%s adds %v: %v", by, users, err)
	}
	return res
}

func (rg *rig) remove(t *testing.T, by, target string) mutate.MemberResult {
	t.Helper()
	res, err := rg.m.RemoveMember(t.Context(), removeCmd(by, target))
	if err != nil {
		t.Fatalf("%s removes %s: %v", by, target, err)
	}
	return res
}

func (rg *rig) leave(t *testing.T, by string) mutate.MemberResult {
	t.Helper()
	res, err := rg.m.LeaveRoom(t.Context(), leaveCmd(by))
	if err != nil {
		t.Fatalf("%s leaves: %v", by, err)
	}
	return res
}

func (rg *rig) setRole(t *testing.T, by, target string, role domain.Role) mutate.MemberResult {
	t.Helper()
	res, err := rg.m.ChangeMemberRole(t.Context(), roleCmd(by, target, role))
	if err != nil {
		t.Fatalf("%s sets %s to %s: %v", by, target, role, err)
	}
	return res
}

func (rg *rig) memberFacts(t *testing.T, id uint64) []domain.MemberAction {
	t.Helper()
	got, err := rg.rooms.MemberActions().After(t.Context(), id, 0, store.MaxMemberScan)
	if err != nil {
		t.Fatalf("member facts of room %d: %v", id, err)
	}
	return got
}

func (rg *rig) lastFact(t *testing.T) domain.MemberAction {
	t.Helper()
	facts := rg.memberFacts(t, room)
	return facts[len(facts)-1]
}

func (rg *rig) member(t *testing.T, user string) (domain.Member, bool) {
	t.Helper()
	got, err := rg.rooms.MembersOf(t.Context(), room, []string{user})
	if err != nil {
		t.Fatalf("MembersOf(%s): %v", user, err)
	}
	if len(got) == 0 {
		return domain.Member{}, false
	}
	return got[0], true
}

func (rg *rig) head(t *testing.T, id uint64) domain.Room {
	t.Helper()
	r, err := rg.rooms.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("Get(%d): %v", id, err)
	}
	return r
}

func (rg *rig) eventsAfter(n int) []*chatimv1.Event {
	_, events := rg.events.list()
	return events[min(n, len(events)):]
}

func sameResult(a, b mutate.MemberResult) bool {
	return a.Version == b.Version && a.Count == b.Count && a.Changed == b.Changed && slices.Equal(a.Added, b.Added) &&
		a.Successor == b.Successor && a.Prev == b.Prev
}

func sameFact(a, b domain.MemberAction) bool {
	return a.Room == b.Room && a.MV == b.MV && a.Tenant == b.Tenant && a.Op == b.Op && a.By == b.By && a.At.Equal(b.At) &&
		a.Count == b.Count && a.Successor == b.Successor && slices.Equal(a.Changes, b.Changes)
}

func sameEvents(got, want []*chatimv1.Event) bool {
	return slices.EqualFunc(got, want, func(a, b *chatimv1.Event) bool { return proto.Equal(a, b) })
}
```

`apps/core/internal/mutate/members_test.go`:

```go
package mutate_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestAddMembersAppendsProjectsForgetsAndAnnounces(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "a")
	rg.send(t, 2, "bob", "b")
	rg.forgets.seen = func(uint64) {
		if m, ok := rg.member(t, "dave"); !ok || m.Removed {
			t.Errorf("member cache forgotten before dave was projected: %+v, %v", m, ok)
		}
	}
	got := rg.add(t, "alice", "dave", "erin", "dave")
	want := mutate.MemberResult{Version: 2, Count: 5, Changed: true, Added: []string{"dave", "erin"}}
	if !sameResult(got, want) {
		t.Fatalf("AddMembers = %+v, want %+v", got, want)
	}
	fact := rg.lastFact(t)
	wantFact := domain.MemberAction{
		Room: room, MV: 2, Tenant: tenant, Op: domain.MemberOpAdd, By: "alice", At: rg.at(), Count: 5,
		Changes: []domain.MemberChange{{User: "dave", Role: domain.RoleMember, ReadSeq: 2}, {User: "erin", Role: domain.RoleMember, ReadSeq: 2}},
	}
	if !sameFact(fact, wantFact) {
		t.Fatalf("fact = %+v, want %+v", fact, wantFact)
	}
	dave, _ := rg.member(t, "dave")
	if dave.Removed || dave.Role != domain.RoleMember || dave.MV != 2 || dave.Read.Seq != 2 || !dave.JoinedAt.Equal(rg.at()) {
		t.Fatalf("dave = %+v, want an active member at mv 2 who has read seq 2", dave)
	}
	if h := rg.head(t, room); h.MemberVersion != 2 || h.MemberCount != 5 {
		t.Fatalf("room head = mv %d count %d, want 2 and 5", h.MemberVersion, h.MemberCount)
	}
	if calls := rg.forgets.calls(); !slices.Equal(calls, []uint64{room}) {
		t.Fatalf("forgot rooms %v, want [%d]", calls, room)
	}
	events := rg.eventsAfter(0)
	if !sameEvents(events, pbconv.MemberEvents(domain.RoomGroup, fact)) || len(events) != 3 {
		t.Fatalf("events = %v, want the room copy and one copy each for dave and erin", events)
	}
	if events[0].GetId() != pbconv.MemberEventID(room, 2) || events[1].GetRecipient() != "dave" || events[2].GetRecipient() != "erin" {
		t.Fatalf("event ids %q %q %q", events[0].GetId(), events[1].GetId(), events[2].GetId())
	}
}

func TestAddingActiveMembersIsANoOp(t *testing.T) {
	rg := newRig(t, nil)
	got := rg.add(t, "alice", "bob", "alice", "carol")
	if !sameResult(got, mutate.MemberResult{Version: 1, Count: 3}) {
		t.Fatalf("AddMembers of active members = %+v, want version 1, count 3, unchanged", got)
	}
	if n, events, forgets := len(rg.memberFacts(t, room)), rg.eventsAfter(0), rg.forgets.calls(); n != 1 || len(events) != 0 || len(forgets) != 0 {
		t.Fatalf("a no-op wrote %d facts, %d events, %d forgets; want 1, 0, 0", n, len(events), len(forgets))
	}
}

func TestRemoveMemberLeavesATombstoneAndRetriesAsANoOp(t *testing.T) {
	rg := newRig(t, nil)
	got := rg.remove(t, "alice", "bob")
	if !sameResult(got, mutate.MemberResult{Version: 2, Count: 2, Changed: true}) {
		t.Fatalf("RemoveMember = %+v, want version 2, count 2", got)
	}
	bob, ok := rg.member(t, "bob")
	if !ok || !bob.Removed || bob.Role != domain.RoleMember || bob.MV != 2 {
		t.Fatalf("bob = %+v (found %v), want a tombstone at mv 2 keeping his role", bob, ok)
	}
	if _, err := rg.rooms.Member(t.Context(), room, "bob"); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("Member(bob) = %v, want ErrNotMember", err)
	}
	fact := rg.lastFact(t)
	if fact.Op != domain.MemberOpRemove || !slices.Equal(fact.Changes, []domain.MemberChange{{User: "bob", Prev: domain.RoleMember}}) || fact.Count != 2 {
		t.Fatalf("fact = %+v, want bob removed leaving 2", fact)
	}
	if !sameEvents(rg.eventsAfter(0), pbconv.MemberEvents(domain.RoomGroup, fact)) {
		t.Fatalf("events = %v, want the events of the remove fact", rg.eventsAfter(0))
	}
	for _, target := range []string{"bob", "zed"} {
		if again := rg.remove(t, "alice", target); !sameResult(again, mutate.MemberResult{Version: 2, Count: 2}) {
			t.Fatalf("removing %s who is not a member = %+v, want an unchanged version 2", target, again)
		}
	}
	if n := len(rg.memberFacts(t, room)); n != 2 {
		t.Fatalf("%d facts, want 2", n)
	}
}

func TestReAddingKeepsClearedHistoryAndRaisesTheReadPosition(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "a")
	rg.send(t, 2, "alice", "b")
	if _, err := rg.rooms.ClearHistory(t.Context(), room, "bob", 2); err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
	if _, _, err := rg.rooms.MarkRead(t.Context(), room, "bob", 1); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	before, _ := rg.member(t, "bob")
	rg.remove(t, "alice", "bob")
	for seq := uint64(3); seq <= 5; seq++ {
		rg.send(t, seq, "alice", "later")
	}
	rg.now = rg.now.Add(time.Hour)
	if got := rg.add(t, "alice", "bob"); !sameResult(got, mutate.MemberResult{Version: 3, Count: 3, Changed: true, Added: []string{"bob"}}) {
		t.Fatalf("re-add = %+v, want bob added at version 3", got)
	}
	after, _ := rg.member(t, "bob")
	if after.Removed || after.ClearedBeforeSeq != 2 || after.Read.Seq != 5 || after.Read.Version != before.Read.Version+1 ||
		after.MV != 3 || !after.JoinedAt.Equal(rg.at()) {
		t.Fatalf("bob after re-add = %+v (before %+v), want cleared 2 kept, read raised to 5 with version +1", after, before)
	}
	mine, err := rg.rooms.UserRooms(t.Context(), tenant, "bob", 0, 10)
	if err != nil || len(mine) != 1 || mine[0].Room != room {
		t.Fatalf("UserRooms(bob) = %+v, %v; want room %d", mine, err, room)
	}
}

func TestLeaveRoomIsDesiredState(t *testing.T) {
	rg := newRig(t, nil)
	if got := rg.leave(t, "carol"); !sameResult(got, mutate.MemberResult{Version: 2, Count: 2, Changed: true}) {
		t.Fatalf("LeaveRoom = %+v, want version 2, count 2", got)
	}
	fact := rg.lastFact(t)
	if fact.Op != domain.MemberOpLeave || fact.Successor != "" || !slices.Equal(fact.Changes, []domain.MemberChange{{User: "carol", Prev: domain.RoleMember}}) {
		t.Fatalf("fact = %+v, want carol leaving without a successor", fact)
	}
	for _, user := range []string{"carol", "mallory"} {
		if got := rg.leave(t, user); !sameResult(got, mutate.MemberResult{Version: 2, Count: 2}) {
			t.Fatalf("%s leaving again = %+v, want an unchanged version 2", user, got)
		}
	}
	if events := rg.eventsAfter(0); !sameEvents(events, pbconv.MemberEvents(domain.RoomGroup, fact)) || len(events) != 2 {
		t.Fatalf("events = %v, want the room copy and carol's copy", events)
	}
}

func TestChangeMemberRoleIsDesiredState(t *testing.T) {
	rg := newRig(t, nil)
	if got := rg.setRole(t, "alice", "bob", domain.RoleAdmin); !sameResult(got, mutate.MemberResult{Version: 2, Count: 3, Changed: true, Prev: domain.RoleMember}) {
		t.Fatalf("ChangeMemberRole = %+v, want version 2 from member", got)
	}
	if bob, _ := rg.member(t, "bob"); bob.Role != domain.RoleAdmin || bob.MV != 2 {
		t.Fatalf("bob = %+v, want admin at mv 2", bob)
	}
	want := []domain.MemberChange{{User: "bob", Role: domain.RoleAdmin, Prev: domain.RoleMember}}
	if fact := rg.lastFact(t); fact.Op != domain.MemberOpRole || !slices.Equal(fact.Changes, want) || fact.Count != 3 {
		t.Fatalf("fact = %+v, want bob from member to admin", fact)
	}
	if got := rg.setRole(t, "alice", "bob", domain.RoleAdmin); !sameResult(got, mutate.MemberResult{Version: 2, Count: 3, Prev: domain.RoleAdmin}) {
		t.Fatalf("same role again = %+v, want unchanged with the current role", got)
	}
	rg.remove(t, "alice", "carol")
	for _, target := range []string{"zed", "carol"} {
		if _, err := rg.m.ChangeMemberRole(t.Context(), roleCmd("alice", target, domain.RoleAdmin)); !errors.Is(err, domain.ErrMemberNotFound) || !errors.Is(err, apperr.ErrNotFound) {
			t.Fatalf("role of %s = %v, want ErrMemberNotFound", target, err)
		}
	}
}
```

`apps/core/internal/mutate/member_rules_test.go`:

```go
package mutate_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestMemberCommandsFollowTheDefaultPolicy(t *testing.T) {
	rg := newRig(t, nil)
	rg.add(t, "alice", "dave", "erin")
	rg.setRole(t, "alice", "bob", domain.RoleAdmin)
	rg.setRole(t, "alice", "dave", domain.RoleAdmin)
	ctx := t.Context()
	denied := map[string]func() error{
		"member adds":             func() error { _, err := rg.m.AddMembers(ctx, addCmd("carol", "frank")); return err },
		"member removes a member": func() error { _, err := rg.m.RemoveMember(ctx, removeCmd("carol", "erin")); return err },
		"admin removes an admin":  func() error { _, err := rg.m.RemoveMember(ctx, removeCmd("bob", "dave")); return err },
		"admin removes the owner": func() error { _, err := rg.m.RemoveMember(ctx, removeCmd("bob", "alice")); return err },
		"admin changes a role": func() error {
			_, err := rg.m.ChangeMemberRole(ctx, roleCmd("bob", "carol", domain.RoleAdmin))
			return err
		},
		"member promotes itself": func() error {
			_, err := rg.m.ChangeMemberRole(ctx, roleCmd("carol", "carol", domain.RoleAdmin))
			return err
		},
	}
	for name, call := range denied {
		if err := call(); !errors.Is(err, access.ErrDenied) || !errors.Is(err, apperr.ErrPermissionDenied) {
			t.Fatalf("%s = %v, want ErrDenied", name, err)
		}
	}
	if n := len(rg.memberFacts(t, room)); n != 4 {
		t.Fatalf("%d facts after refused commands, want 4", n)
	}
	rg.add(t, "bob", "frank")
	rg.remove(t, "bob", "erin")
	rg.remove(t, "alice", "dave")
	rg.leave(t, "carol")
	if _, err := rg.m.AddMembers(ctx, addCmd("mallory", "zed")); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("a stranger adds = %v, want ErrNotMember", err)
	}
	if h := rg.head(t, room); h.MemberVersion != 8 || h.MemberCount != 3 {
		t.Fatalf("room head = mv %d count %d, want 8 and 3 (alice, bob, frank)", h.MemberVersion, h.MemberCount)
	}
}

func TestMemberCommandsAskThePolicyAfterSettling(t *testing.T) {
	var asked []access.Request
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error { asked = append(asked, r); return access.ErrDenied })
	rg := newRig(t, deny)
	if _, err := rg.m.ChangeMemberRole(t.Context(), roleCmd("alice", "bob", domain.RoleAdmin)); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("ChangeMemberRole = %v, want PermissionDenied", err)
	}
	if len(asked) != 1 {
		t.Fatalf("policy asked %d times, want 1", len(asked))
	}
	r := asked[0]
	if r.Action != access.ChangeMemberRole || r.User != "alice" || r.Member.Role != domain.RoleOwner || r.Target.User != "bob" ||
		r.Target.Role != domain.RoleMember || r.Role != domain.RoleAdmin || r.Room.ID != room || r.Room.MemberVersion != 1 {
		t.Fatalf("policy saw %+v, want alice (owner) asking to make member bob an admin at mv 1", r)
	}
	if got, err := rg.m.LeaveRoom(t.Context(), leaveCmd("mallory")); err != nil || got.Changed {
		t.Fatalf("a stranger leaves = %+v, %v; want an unchanged success", got, err)
	}
	if len(asked) != 1 || len(rg.memberFacts(t, room)) != 1 || len(rg.eventsAfter(0)) != 0 {
		t.Fatalf("asked %d times, %d facts, %d events; want 1, 1, 0", len(asked), len(rg.memberFacts(t, room)), len(rg.eventsAfter(0)))
	}
}

func TestDirectRoomsKeepTheirTwoMembers(t *testing.T) {
	rg := newRig(t, nil)
	const dm uint64 = 77
	r := domain.Room{ID: dm, Tenant: tenant, Type: domain.RoomDM, CreatedBy: "alice", CreatedAt: created, MemberCount: 2}
	members := []domain.Member{
		{Room: dm, Tenant: tenant, User: "alice", Role: domain.RoleOwner, JoinedAt: created},
		{Room: dm, Tenant: tenant, User: "bob", Role: domain.RoleMember, JoinedAt: created},
	}
	if err := rg.rooms.Create(t.Context(), r, members); err != nil {
		t.Fatalf("create dm: %v", err)
	}
	ctx := t.Context()
	calls := map[string]func() error{
		"add": func() error {
			_, err := rg.m.AddMembers(ctx, mutate.AddMembersCmd{Tenant: tenant, User: "alice", Room: dm, Users: []string{"carol"}})
			return err
		},
		"remove": func() error {
			_, err := rg.m.RemoveMember(ctx, mutate.RemoveMemberCmd{Tenant: tenant, User: "alice", Room: dm, Target: "bob"})
			return err
		},
		"leave": func() error {
			_, err := rg.m.LeaveRoom(ctx, mutate.LeaveRoomCmd{Tenant: tenant, User: "bob", Room: dm})
			return err
		},
		"role": func() error {
			_, err := rg.m.ChangeMemberRole(ctx, mutate.ChangeRoleCmd{Tenant: tenant, User: "alice", Room: dm, Target: "bob", Role: domain.RoleAdmin})
			return err
		},
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, domain.ErrDirectRoom) || !errors.Is(err, apperr.ErrFailedPrecondition) {
			t.Fatalf("%s on a dm = %v, want ErrDirectRoom", name, err)
		}
	}
	if n := len(rg.memberFacts(t, dm)); n != 1 {
		t.Fatalf("dm has %d member facts, want only the first", n)
	}
}

func TestMemberCommandsRejectBadInputFirst(t *testing.T) {
	rg := newRig(t, nil)
	d := rg.deps(t, nil)
	d.Limits = mutate.Limits{MemberBatch: 2}
	rg.m = rg.build(t, d)
	ctx := t.Context()
	add := func(c mutate.AddMembersCmd) func() error {
		return func() error { _, err := rg.m.AddMembers(ctx, c); return err }
	}
	remove := func(c mutate.RemoveMemberCmd) func() error {
		return func() error { _, err := rg.m.RemoveMember(ctx, c); return err }
	}
	role := func(c mutate.ChangeRoleCmd) func() error {
		return func() error { _, err := rg.m.ChangeMemberRole(ctx, c); return err }
	}
	elsewhere := addCmd("alice", "dave")
	elsewhere.Tenant = "other"
	unknown := addCmd("alice", "dave")
	unknown.Room = 9
	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"no users", add(addCmd("alice")), apperr.ErrInvalidArgument},
		{"invalid user", add(addCmd("alice", "dave", "e e")), apperr.ErrInvalidArgument},
		{"over the batch", add(addCmd("alice", "dave", "erin", "frank")), domain.ErrTooManyMembers},
		{"remove yourself", remove(removeCmd("alice", "alice")), apperr.ErrInvalidArgument},
		{"remove an invalid user", remove(removeCmd("alice", "z z")), apperr.ErrInvalidArgument},
		{"unknown role", role(roleCmd("alice", "bob", "boss")), apperr.ErrInvalidArgument},
		{"empty role", role(roleCmd("alice", "bob", "")), apperr.ErrInvalidArgument},
		{"other tenant", add(elsewhere), domain.ErrRoomNotFound},
		{"unknown room", add(unknown), domain.ErrRoomNotFound},
		{"stranger", add(addCmd("mallory", "dave")), domain.ErrNotMember},
	}
	for _, c := range cases {
		if err := c.call(); !errors.Is(err, c.want) {
			t.Fatalf("%s = %v, want %v", c.name, err, c.want)
		}
	}
	if n := len(rg.memberFacts(t, room)); n != 1 {
		t.Fatalf("refused commands wrote %d facts, want only the first", n)
	}
	if got := rg.add(t, "alice", "dave", "erin", "dave"); len(got.Added) != 2 || rg.m.MemberBatch() != 2 {
		t.Fatalf("two distinct users in a batch of 2 = %+v, want both added", got)
	}
}
```

`apps/core/internal/mutate/member_succession_test.go`:

```go
package mutate_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func (rg *rig) expectRole(t *testing.T, user string, want domain.Role) {
	t.Helper()
	if m, ok := rg.member(t, user); !ok || m.Removed || m.Role != want {
		t.Fatalf("%s = %+v (found %v), want an active %s", user, m, ok, want)
	}
}

func TestTheLastOwnerLeavingHandsOverToTheEarliestAdmin(t *testing.T) {
	rg := newRig(t, nil)
	rg.add(t, "alice", "dave")
	rg.now = rg.now.Add(time.Minute)
	rg.add(t, "alice", "erin")
	rg.setRole(t, "alice", "erin", domain.RoleAdmin)
	rg.setRole(t, "alice", "dave", domain.RoleAdmin)
	got := rg.leave(t, "alice")
	if !sameResult(got, mutate.MemberResult{Version: 6, Count: 4, Changed: true, Successor: "dave"}) {
		t.Fatalf("LeaveRoom = %+v, want dave (the earliest admin) as the new owner", got)
	}
	rg.expectRole(t, "dave", domain.RoleOwner)
	rg.expectRole(t, "erin", domain.RoleAdmin)
	fact := rg.lastFact(t)
	events := pbconv.MemberEvents(domain.RoomGroup, fact)
	if fact.Successor != "dave" || events[len(events)-1].GetRecipient() != "dave" {
		t.Fatalf("fact %+v with events %v, want dave named and told", fact, events)
	}
	all := rg.eventsAfter(0)
	if len(all) < len(events) || !sameEvents(all[len(all)-len(events):], events) {
		t.Fatalf("enqueued events %v do not end with the leave events %v", all, events)
	}
}

func TestWithoutAdminsTheEarliestMemberTakesOverAndTiesGoByUserID(t *testing.T) {
	rg := newRig(t, nil)
	rg.add(t, "alice", "aaron")
	got := rg.leave(t, "alice")
	if !sameResult(got, mutate.MemberResult{Version: 3, Count: 3, Changed: true, Successor: "bob"}) {
		t.Fatalf("LeaveRoom = %+v, want bob (joined first, before carol by id; aaron joined later)", got)
	}
	rg.expectRole(t, "bob", domain.RoleOwner)
	rg.expectRole(t, "carol", domain.RoleMember)
}

func TestAnOwnerLeavingBesideAnotherOwnerNamesNoSuccessor(t *testing.T) {
	rg := newRig(t, nil)
	rg.setRole(t, "alice", "bob", domain.RoleOwner)
	if got := rg.leave(t, "alice"); !sameResult(got, mutate.MemberResult{Version: 3, Count: 2, Changed: true}) {
		t.Fatalf("LeaveRoom = %+v, want no successor while bob owns the room", got)
	}
	rg.expectRole(t, "bob", domain.RoleOwner)
	rg.expectRole(t, "carol", domain.RoleMember)
}

func TestTheOnlyMemberLeavingEmptiesTheRoom(t *testing.T) {
	rg := newRig(t, nil)
	const solo uint64 = 88
	r := domain.Room{ID: solo, Tenant: tenant, Type: domain.RoomGroup, Name: "solo", CreatedBy: "alice", CreatedAt: created, MemberCount: 1}
	if err := rg.rooms.Create(t.Context(), r, []domain.Member{{Room: solo, Tenant: tenant, User: "alice", Role: domain.RoleOwner, JoinedAt: created}}); err != nil {
		t.Fatalf("create solo room: %v", err)
	}
	got, err := rg.m.LeaveRoom(t.Context(), mutate.LeaveRoomCmd{Tenant: tenant, User: "alice", Room: solo})
	if err != nil || !sameResult(got, mutate.MemberResult{Version: 2, Count: 0, Changed: true}) {
		t.Fatalf("LeaveRoom = %+v, %v; want an empty room at version 2", got, err)
	}
	if h := rg.head(t, solo); h.MemberVersion != 2 || h.MemberCount != 0 {
		t.Fatalf("solo head = mv %d count %d, want 2 and 0", h.MemberVersion, h.MemberCount)
	}
}

func TestTheLastOwnerCannotStepDown(t *testing.T) {
	rg := newRig(t, nil)
	if _, err := rg.m.ChangeMemberRole(t.Context(), roleCmd("alice", "alice", domain.RoleMember)); !errors.Is(err, domain.ErrLastOwner) || !errors.Is(err, apperr.ErrFailedPrecondition) {
		t.Fatalf("the last owner steps down = %v, want ErrLastOwner", err)
	}
	if n := len(rg.memberFacts(t, room)); n != 1 {
		t.Fatalf("%d facts after a refused step-down, want 1", n)
	}
	rg.setRole(t, "alice", "bob", domain.RoleOwner)
	if got := rg.setRole(t, "alice", "alice", domain.RoleAdmin); !sameResult(got, mutate.MemberResult{Version: 3, Count: 3, Changed: true, Prev: domain.RoleOwner}) {
		t.Fatalf("an owner beside another steps down = %+v, want version 3 from owner", got)
	}
	if _, err := rg.m.ChangeMemberRole(t.Context(), roleCmd("bob", "bob", domain.RoleMember)); !errors.Is(err, domain.ErrLastOwner) {
		t.Fatalf("bob, now the last owner, steps down = %v, want ErrLastOwner", err)
	}
}
```

`apps/core/internal/mutate/member_race_test.go`:

```go
package mutate_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type racingFacts struct {
	store.MemberActions
	mu      sync.Mutex
	rivals  []func(mv uint64) domain.MemberAction
	appends int
}

func (r *racingFacts) Append(ctx context.Context, a domain.MemberAction) error {
	r.mu.Lock()
	r.appends++
	var next func(uint64) domain.MemberAction
	if len(r.rivals) > 0 {
		next, r.rivals = r.rivals[0], r.rivals[1:]
	}
	r.mu.Unlock()
	if next != nil {
		if err := r.MemberActions.Append(ctx, next(a.MV)); err != nil {
			return err
		}
	}
	return r.MemberActions.Append(ctx, a)
}

func (r *racingFacts) appended() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.appends
}

func (rg *rig) withRivals(t *testing.T, rivals ...func(mv uint64) domain.MemberAction) *racingFacts {
	t.Helper()
	d := rg.deps(t, nil)
	race := &racingFacts{MemberActions: d.MemberActions, rivals: rivals}
	d.MemberActions = race
	rg.m = rg.build(t, d)
	return race
}

func rival(op domain.MemberOp, by string, count int, changes ...domain.MemberChange) func(uint64) domain.MemberAction {
	return func(mv uint64) domain.MemberAction {
		return domain.MemberAction{Room: room, MV: mv, Tenant: tenant, Op: op, Changes: changes, By: by, At: created.Add(time.Second), Count: count}
	}
}

func TestTwoAdminsAddingAtOnceEachKeepTheirOwnUsers(t *testing.T) {
	rg := newRig(t, nil)
	rg.setRole(t, "alice", "bob", domain.RoleAdmin)
	rg.setRole(t, "alice", "carol", domain.RoleAdmin)
	before := len(rg.eventsAfter(0))
	race := rg.withRivals(t, rival(domain.MemberOpAdd, "bob", 4, domain.MemberChange{User: "erin", Role: domain.RoleMember}))
	got := rg.add(t, "carol", "erin", "frank")
	if !sameResult(got, mutate.MemberResult{Version: 5, Count: 5, Changed: true, Added: []string{"frank"}}) {
		t.Fatalf("losing add = %+v, want only frank added at version 5", got)
	}
	facts := rg.memberFacts(t, room)
	if race.appended() != 2 || len(facts) != 5 || facts[3].By != "bob" || facts[4].By != "carol" {
		t.Fatalf("%d appends, facts %+v; want bob's fact at mv 4 and carol's retry at mv 5", race.appended(), facts)
	}
	rg.expectRole(t, "erin", domain.RoleMember)
	if !sameEvents(rg.eventsAfter(before), pbconv.MemberEvents(domain.RoomGroup, facts[4])) {
		t.Fatalf("events = %v, want only the events of carol's fact", rg.eventsAfter(before))
	}
}

func TestTwoAdminsRemovingTheSameMemberLeaveOneFact(t *testing.T) {
	rg := newRig(t, nil)
	rg.setRole(t, "alice", "bob", domain.RoleAdmin)
	rg.withRivals(t, rival(domain.MemberOpRemove, "bob", 2, domain.MemberChange{User: "carol", Prev: domain.RoleMember}))
	if got := rg.remove(t, "alice", "carol"); !sameResult(got, mutate.MemberResult{Version: 3, Count: 2}) {
		t.Fatalf("losing remove = %+v, want an unchanged version 3", got)
	}
	if facts := rg.memberFacts(t, room); len(facts) != 3 || facts[2].By != "bob" {
		t.Fatalf("facts = %+v, want only bob's remove", facts)
	}
}

func TestOwnersRemovingEachOtherLeaveTheWinnerInCharge(t *testing.T) {
	rg := newRig(t, nil)
	rg.add(t, "alice", "dave")
	rg.setRole(t, "alice", "dave", domain.RoleOwner)
	rg.withRivals(t, rival(domain.MemberOpRemove, "alice", 3, domain.MemberChange{User: "dave", Prev: domain.RoleOwner}))
	if _, err := rg.m.RemoveMember(t.Context(), removeCmd("dave", "alice")); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("dave removes alice after losing the race = %v, want ErrNotMember", err)
	}
	rg.expectRole(t, "alice", domain.RoleOwner)
	if dave, _ := rg.member(t, "dave"); !dave.Removed {
		t.Fatalf("dave = %+v, want removed", dave)
	}
	if n := len(rg.memberFacts(t, room)); n != 4 {
		t.Fatalf("%d facts, want 4", n)
	}
}

func TestALeaveRacingARoleChangeRecomputesTheSuccessor(t *testing.T) {
	rg := newRig(t, nil)
	rg.withRivals(t, rival(domain.MemberOpRole, "alice", 3, domain.MemberChange{User: "carol", Role: domain.RoleAdmin, Prev: domain.RoleMember}))
	got := rg.leave(t, "alice")
	if !sameResult(got, mutate.MemberResult{Version: 3, Count: 2, Changed: true, Successor: "carol"}) {
		t.Fatalf("LeaveRoom = %+v, want carol (made admin by the rival fact) instead of bob", got)
	}
	rg.expectRole(t, "carol", domain.RoleOwner)
	rg.expectRole(t, "bob", domain.RoleMember)
}

func TestTheSameIntentAtTheVersionIsTheResult(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "a")
	race := rg.withRivals(t, rival(domain.MemberOpAdd, "alice", 4, domain.MemberChange{User: "erin", Role: domain.RoleMember, ReadSeq: 1}))
	got := rg.add(t, "alice", "erin")
	if !sameResult(got, mutate.MemberResult{Version: 2, Count: 4, Changed: true, Added: []string{"erin"}}) {
		t.Fatalf("AddMembers = %+v, want the stored fact as the result", got)
	}
	stored := rg.lastFact(t)
	if race.appended() != 1 || len(rg.memberFacts(t, room)) != 2 || !stored.At.Equal(created.Add(time.Second)) {
		t.Fatalf("%d appends, last fact %+v; want one append answered by the stored fact", race.appended(), stored)
	}
	rg.expectRole(t, "erin", domain.RoleMember)
	if !sameEvents(rg.eventsAfter(0), pbconv.MemberEvents(domain.RoomGroup, stored)) || !slices.Equal(rg.forgets.calls(), []uint64{room}) {
		t.Fatalf("events %v, forgets %v; want the stored fact announced once", rg.eventsAfter(0), rg.forgets.calls())
	}
}

func TestMemberChangesGiveUpAfterThreeLostVersions(t *testing.T) {
	rg := newRig(t, nil)
	other := func(mv uint64) domain.MemberAction {
		u := "x" + strconv.FormatUint(mv, 10)
		return domain.MemberAction{
			Room: room, MV: mv, Tenant: tenant, Op: domain.MemberOpAdd, By: "bob", At: created, Count: int(mv) + 2,
			Changes: []domain.MemberChange{{User: u, Role: domain.RoleMember}},
		}
	}
	race := rg.withRivals(t, other, other, other)
	if _, err := rg.m.AddMembers(t.Context(), addCmd("alice", "erin")); !errors.Is(err, domain.ErrRetryLater) || !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("AddMembers = %v, want ErrRetryLater", err)
	}
	if _, ok := rg.member(t, "erin"); ok || race.appended() != 3 {
		t.Fatalf("erin written (%v) after %d appends, want nothing after 3", ok, race.appended())
	}
	if len(rg.eventsAfter(0)) != 0 || len(rg.forgets.calls()) != 0 {
		t.Fatalf("a refused command enqueued events or forgot members")
	}
}
```

`apps/core/internal/mutate/member_failures_test.go`:

```go
package mutate_test

import (
	"context"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
)

type failingProjection struct {
	mutate.MemberProjector
	err error
}

func (p failingProjection) Project(context.Context, uint64, uint64) (domain.Room, error) {
	return domain.Room{}, p.err
}

func TestProjectionAndEventFailuresDoNotFailTheCommand(t *testing.T) {
	rg := newRig(t, nil)
	d := rg.deps(t, nil)
	d.MemberProj = failingProjection{MemberProjector: d.MemberProj, err: errBoom}
	rg.m = rg.build(t, d)
	if got := rg.add(t, "alice", "erin"); !sameResult(got, mutate.MemberResult{Version: 2, Count: 4, Changed: true, Added: []string{"erin"}}) {
		t.Fatalf("AddMembers with a failing projection = %+v, want version 2", got)
	}
	if _, ok := rg.member(t, "erin"); ok || rg.head(t, room).MemberVersion != 1 {
		t.Fatalf("erin projected although Project failed")
	}
	if len(rg.forgets.calls()) != 1 || len(rg.eventsAfter(0)) != 2 {
		t.Fatalf("forgets %v, %d events; want one forget and two events anyway", rg.forgets.calls(), len(rg.eventsAfter(0)))
	}
	rg.m = rg.build(t, rg.deps(t, nil))
	if got := rg.add(t, "alice", "erin"); !sameResult(got, mutate.MemberResult{Version: 2, Count: 4}) {
		t.Fatalf("the next command = %+v, want it to settle erin in and find nothing to do", got)
	}
	rg.expectRole(t, "erin", domain.RoleMember)
	rg.events.err = errBoom
	if got := rg.add(t, "alice", "frank"); got.Version != 3 || !got.Changed {
		t.Fatalf("AddMembers with refused events = %+v, want version 3", got)
	}
}
```

**Step 3: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/mutate/..."`
Expected: FAIL biên dịch (trình biên dịch dừng ở 10 lỗi, `too many errors`), các lỗi thuộc nhóm: `unknown field MemberActions in struct literal of type mutate.Deps`, `unknown field MemberReader …`, `unknown field MemberProj …`, `unknown field Forget …`, `unknown field MemberBatch in struct literal of type mutate.Limits`, `undefined: mutate.MaxMemberBatch`, `undefined: mutate.AddMembersCmd`, `undefined: mutate.MemberResult`, `undefined: mutate.MemberProjector`, `rg.m.AddMembers undefined (type *mutate.Mutator has no field or method AddMembers)`.

**Step 4: Code `mutate`**

`apps/core/internal/mutate/limits.go`:
- khối `const`, sau `FastTouchTries  = 3` thêm:

```go
	DefaultMemberBatch = 500
	MaxMemberBatch     = domain.MaxMemberChanges
```

- `type Limits struct`, sau `PinLimit int` thêm `MemberBatch int` (gofmt căn cột).
- `withDefaults`, trước `return l` thêm `l.MemberBatch = cmp.Or(l.MemberBatch, DefaultMemberBatch)`.
- `validate`, trước `return nil` cuối thêm:

```go
	if l.MemberBatch < 2 || l.MemberBatch > MaxMemberBatch {
		return fmt.Errorf("%w: member batch %d must be 2 to %d", apperr.ErrInvalidArgument, l.MemberBatch, MaxMemberBatch)
	}
```

Chạy `make -s go ARGS="fmt ./apps/core/internal/mutate/"`.

`apps/core/internal/mutate/members.go`:

```go
package mutate

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var (
	errNoUsers    = fmt.Errorf("%w: users", apperr.ErrInvalidArgument)
	errRemoveSelf = fmt.Errorf("%w: user (leave the room instead of removing yourself)", apperr.ErrInvalidArgument)
)

type MemberProjector interface {
	Settle(ctx context.Context, room uint64) (domain.Room, error)
	Project(ctx context.Context, room, target uint64) (domain.Room, error)
}

type MemberForgetter interface {
	ForgetMembers(room uint64)
}

type AddMembersCmd struct {
	Tenant, User string
	Room         uint64
	Users        []string
}

type RemoveMemberCmd struct {
	Tenant, User string
	Room         uint64
	Target       string
}

type LeaveRoomCmd struct {
	Tenant, User string
	Room         uint64
}

type ChangeRoleCmd struct {
	Tenant, User string
	Room         uint64
	Target       string
	Role         domain.Role
}

type MemberResult struct {
	Version   uint64
	Count     int
	Changed   bool
	Added     []string
	Successor string
	Prev      domain.Role
}

func (m *Mutator) MemberBatch() int { return m.d.Limits.MemberBatch }

func (m *Mutator) AddMembers(ctx context.Context, c AddMembersCmd) (MemberResult, error) {
	users, err := m.distinctUsers(c.Users)
	if err != nil {
		return MemberResult{}, err
	}
	var last uint64
	return m.changeMembers(ctx, memberCall{
		tenant: c.Tenant, user: c.User, room: c.Room, action: access.AddMembers, others: users,
		prepare: func(ctx context.Context) error {
			seq, err := m.d.Messages.Last(ctx, c.Room, 0)
			last = seq
			return err
		},
		plan: func(_ context.Context, r domain.Room, docs map[string]domain.Member) (domain.MemberAction, bool, error) {
			var changes []domain.MemberChange
			for _, u := range users {
				if d, ok := docs[u]; !ok || d.Removed {
					changes = append(changes, domain.MemberChange{User: u, Role: domain.RoleMember, ReadSeq: last})
				}
			}
			fact := domain.MemberAction{Op: domain.MemberOpAdd, Changes: changes, Count: r.MemberCount + len(changes)}
			return fact, len(changes) > 0, nil
		},
	})
}

func (m *Mutator) RemoveMember(ctx context.Context, c RemoveMemberCmd) (MemberResult, error) {
	if err := domain.ValidUser(c.Target); err != nil {
		return MemberResult{}, err
	}
	if c.Target == c.User {
		return MemberResult{}, errRemoveSelf
	}
	return m.changeMembers(ctx, memberCall{
		tenant: c.Tenant, user: c.User, room: c.Room, action: access.RemoveMember, target: c.Target, others: []string{c.Target},
		plan: func(_ context.Context, r domain.Room, docs map[string]domain.Member) (domain.MemberAction, bool, error) {
			t, ok := docs[c.Target]
			if !ok || t.Removed {
				return domain.MemberAction{}, false, nil
			}
			return departure(domain.MemberOpRemove, t, r), true, nil
		},
	})
}

func (m *Mutator) distinctUsers(users []string) ([]string, error) {
	seen := make(map[string]struct{}, min(len(users), m.d.Limits.MemberBatch))
	out := make([]string, 0, min(len(users), m.d.Limits.MemberBatch))
	for _, u := range users {
		if err := domain.ValidUser(u); err != nil {
			return nil, err
		}
		if _, dup := seen[u]; dup {
			continue
		}
		if len(out) == m.d.Limits.MemberBatch {
			return nil, domain.ErrTooManyMembers
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	if len(out) == 0 {
		return nil, errNoUsers
	}
	return out, nil
}
```

`apps/core/internal/mutate/member_roles.go`:

```go
package mutate

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func (m *Mutator) LeaveRoom(ctx context.Context, c LeaveRoomCmd) (MemberResult, error) {
	return m.changeMembers(ctx, memberCall{
		tenant: c.Tenant, user: c.User, room: c.Room, action: access.LeaveRoom,
		plan: func(ctx context.Context, r domain.Room, docs map[string]domain.Member) (domain.MemberAction, bool, error) {
			me := docs[c.User]
			fact := departure(domain.MemberOpLeave, me, r)
			if me.Role != domain.RoleOwner {
				return fact, true, nil
			}
			heir, err := m.heir(ctx, c.Room)
			if err != nil {
				return domain.MemberAction{}, false, err
			}
			fact.Successor = heir
			return fact, true, nil
		},
	})
}

func (m *Mutator) ChangeMemberRole(ctx context.Context, c ChangeRoleCmd) (MemberResult, error) {
	if err := domain.ValidUser(c.Target); err != nil {
		return MemberResult{}, err
	}
	if _, err := domain.ParseRole(string(c.Role)); err != nil {
		return MemberResult{}, err
	}
	res, err := m.changeMembers(ctx, memberCall{
		tenant: c.Tenant, user: c.User, room: c.Room, action: access.ChangeMemberRole, target: c.Target, role: c.Role,
		others: []string{c.Target},
		plan: func(ctx context.Context, r domain.Room, docs map[string]domain.Member) (domain.MemberAction, bool, error) {
			t, ok := docs[c.Target]
			if !ok || t.Removed {
				return domain.MemberAction{}, false, domain.ErrMemberNotFound
			}
			return m.roleChange(ctx, c, r, t)
		},
	})
	if err == nil && !res.Changed {
		res.Prev = c.Role
	}
	return res, err
}

func (m *Mutator) roleChange(ctx context.Context, c ChangeRoleCmd, r domain.Room, t domain.Member) (domain.MemberAction, bool, error) {
	if t.Role == c.Role {
		return domain.MemberAction{}, false, nil
	}
	if t.Role == domain.RoleOwner {
		last, err := m.lastOwner(ctx, c.Room)
		if err != nil {
			return domain.MemberAction{}, false, err
		}
		if last {
			return domain.MemberAction{}, false, domain.ErrLastOwner
		}
	}
	change := domain.MemberChange{User: t.User, Role: c.Role, Prev: t.Role}
	return domain.MemberAction{Op: domain.MemberOpRole, Changes: []domain.MemberChange{change}, Count: r.MemberCount}, true, nil
}

func (m *Mutator) heir(ctx context.Context, room uint64) (string, error) {
	last, err := m.lastOwner(ctx, room)
	if err != nil || !last {
		return "", err
	}
	next, ok, err := m.d.MemberReader.Successor(ctx, room)
	if err != nil || !ok {
		return "", err
	}
	return next.User, nil
}

func (m *Mutator) lastOwner(ctx context.Context, room uint64) (bool, error) {
	owners, err := m.d.MemberReader.Owners(ctx, room, 2)
	if err != nil {
		return false, err
	}
	return len(owners) < 2, nil
}

func departure(op domain.MemberOp, who domain.Member, r domain.Room) domain.MemberAction {
	change := domain.MemberChange{User: who.User, Prev: who.Role}
	return domain.MemberAction{Op: op, Changes: []domain.MemberChange{change}, Count: max(r.MemberCount-1, 0)}
}
```

`apps/core/internal/mutate/member_commit.go`:

```go
package mutate

import (
	"context"
	"errors"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const memberTries = 3

type memberPlan func(ctx context.Context, r domain.Room, docs map[string]domain.Member) (domain.MemberAction, bool, error)

type memberCall struct {
	tenant, user string
	room         uint64
	action       access.Action
	target       string
	role         domain.Role
	others       []string
	prepare      func(ctx context.Context) error
	plan         memberPlan
}

func (m *Mutator) changeMembers(ctx context.Context, c memberCall) (MemberResult, error) {
	if err := m.admitMembers(ctx, c); err != nil {
		return MemberResult{}, err
	}
	if c.prepare != nil {
		if err := c.prepare(ctx); err != nil {
			return MemberResult{}, err
		}
	}
	users := withCaller(c.user, c.others)
	for range memberTries {
		r, docs, err := m.settled(ctx, c.room, users)
		if err != nil {
			return MemberResult{}, err
		}
		fact, changed, err := m.draft(ctx, c, r, docs)
		switch {
		case err != nil:
			return MemberResult{}, err
		case !changed:
			return MemberResult{Version: r.MemberVersion, Count: r.MemberCount}, nil
		}
		fact.Room, fact.MV, fact.Tenant, fact.By, fact.At = c.room, r.MemberVersion+1, r.Tenant, c.user, m.now()
		stored, ok, err := m.appendMember(ctx, fact)
		if err != nil {
			return MemberResult{}, err
		}
		if ok {
			return m.finishMembers(ctx, r.Type, stored), nil
		}
	}
	return MemberResult{}, domain.ErrRetryLater
}

func (m *Mutator) admitMembers(ctx context.Context, c memberCall) error {
	admit := m.d.Access.Admit
	if c.action == access.LeaveRoom {
		admit = m.d.Access.AdmitRoom
	}
	grant, err := admit(ctx, c.action, c.tenant, c.user, c.room)
	if err != nil {
		return err
	}
	if grant.Room.Type == domain.RoomDM {
		return domain.ErrDirectRoom
	}
	return nil
}

func (m *Mutator) settled(ctx context.Context, room uint64, users []string) (domain.Room, map[string]domain.Member, error) {
	r, err := m.d.MemberProj.Settle(ctx, room)
	if err != nil {
		return domain.Room{}, nil, err
	}
	found, err := m.d.MemberReader.MembersOf(ctx, room, users)
	if err != nil {
		return domain.Room{}, nil, err
	}
	docs := make(map[string]domain.Member, len(found))
	for _, d := range found {
		docs[d.User] = d
	}
	return r, docs, nil
}

func (m *Mutator) draft(ctx context.Context, c memberCall, r domain.Room, docs map[string]domain.Member) (domain.MemberAction, bool, error) {
	caller, ok := docs[c.user]
	if !ok || caller.Removed {
		if c.action == access.LeaveRoom {
			return domain.MemberAction{}, false, nil
		}
		return domain.MemberAction{}, false, domain.ErrNotMember
	}
	req := access.Request{Action: c.action, User: c.user, Room: r, Member: caller, Target: docs[c.target], Role: c.role}
	if err := m.d.Access.Allow(ctx, req); err != nil {
		return domain.MemberAction{}, false, err
	}
	return c.plan(ctx, r, docs)
}

func (m *Mutator) appendMember(ctx context.Context, fact domain.MemberAction) (domain.MemberAction, bool, error) {
	err := m.d.MemberActions.Append(ctx, fact)
	if !errors.Is(err, store.ErrMemberActionExists) {
		return fact, err == nil, err
	}
	got, err := m.d.MemberActions.At(ctx, fact.Room, fact.MV)
	if err != nil {
		return domain.MemberAction{}, false, err
	}
	return got, sameIntent(got, fact), nil
}

func (m *Mutator) finishMembers(ctx context.Context, typ domain.RoomType, fact domain.MemberAction) MemberResult {
	_, _ = m.d.MemberProj.Project(ctx, fact.Room, fact.MV)
	m.d.Forget.ForgetMembers(fact.Room)
	_ = m.d.Events.Enqueue(fact.Room, pbconv.MemberEvents(typ, fact))
	res := MemberResult{Version: fact.MV, Count: fact.Count, Changed: true, Successor: fact.Successor}
	if fact.Op == domain.MemberOpAdd {
		for _, c := range fact.Changes {
			res.Added = append(res.Added, c.User)
		}
	}
	if fact.Op == domain.MemberOpRole && len(fact.Changes) == 1 {
		res.Prev = fact.Changes[0].Prev
	}
	return res
}

func sameIntent(a, b domain.MemberAction) bool {
	return a.Op == b.Op && a.By == b.By && slices.Equal(intents(a), intents(b))
}

func intents(a domain.MemberAction) []string {
	out := make([]string, len(a.Changes))
	for i, c := range a.Changes {
		out[i] = c.User + "\x00" + string(c.Role)
	}
	slices.Sort(out)
	return out
}

func withCaller(user string, others []string) []string {
	out := make([]string, 1, len(others)+1)
	out[0] = user
	for _, u := range others {
		if u != user {
			out = append(out, u)
		}
	}
	return out
}
```

`apps/core/internal/mutate/mutator.go`:
- thay dòng `errMissingDeps` bằng:

```go
var errMissingDeps = fmt.Errorf("%w: mutator needs access, messages, edits, hidden, rooms, events, reactions, a counter, pins, a pin projector, member actions, a member reader, a member projector and a member forgetter", apperr.ErrInvalidArgument)
```

- trong `type Deps struct`, sau `Projector PinProjector` thêm (gofmt căn cột):

```go
	MemberActions store.MemberActions
	MemberReader  store.MemberReader
	MemberProj    MemberProjector
	Forget        MemberForgetter
```

- trong `New`, thay điều kiện `if d.Access == nil || ... || d.Pins == nil || d.Projector == nil {` bằng:

```go
	if d.Access == nil || d.Messages == nil || d.Edits == nil || d.Hidden == nil || d.Rooms == nil || d.Events == nil ||
		d.Reactions == nil || d.Counter == nil || d.Pins == nil || d.Projector == nil ||
		d.MemberActions == nil || d.MemberReader == nil || d.MemberProj == nil || d.Forget == nil {
```

Chạy `make -s go ARGS="fmt ./apps/core/internal/mutate/"`.

**Step 5: Caller của `mutate.New` ngoài package**

`apps/core/internal/grpcsrv/fake_dependencies_test.go`:
- import thêm `".../internal/memberproj"`;
- sau `func (nopPublisher) Enqueue(...)` thêm:

```go
type nopForgetter struct{}

func (nopForgetter) ForgetMembers(uint64) {}
```

- trong `newMutator`, ngay trước `var events mutate.EventPublisher = nopPublisher{}` thêm:

```go
	members, err := memberproj.New(rg.rooms.MemberActions(), rg.rooms)
	if err != nil {
		t.Fatalf("memberproj.New: %v", err)
	}
	var forget mutate.MemberForgetter = nopForgetter{}
	if router, ok := o.sender.(*actor.Router); ok {
		forget = router
	}
```

- trong literal `mutate.Deps{...}`, sau `Reactions: rg.reactions, Counter: counts, Pins: rg.pins, Projector: projector, Limits: o.limits,` thêm dòng:

```go
		MemberActions: rg.rooms.MemberActions(), MemberReader: rg.rooms, MemberProj: members, Forget: forget,
```

(`newRig` gán `o.sender = startRouter(t, rg)` trước khi gọi `newMutator`, nên rig thật dùng router làm `Forget`; `harness_test.go` không đổi.)

`apps/core/service_wiring.go`:
- import thêm `".../internal/memberproj"`;
- trước `mut, err := mutate.New(...)` thêm:

```go
	members, err := memberproj.New(st.MemberActions(), st)
	if err != nil {
		return nil, fmt.Errorf("wire member projector: %w", err)
	}
```

- trong literal `mutate.Deps{...}` thêm dòng `MemberActions: st.MemberActions(), MemberReader: st, MemberProj: members, Forget: router,`.

**Step 6: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/internal/mutate/... ./apps/core/internal/grpcsrv/... ./apps/core/"
make vet
```

Expected: PASS (`apps/core` chỉ chạy unit, itest bỏ qua khi thiếu `CHATIM_IT_*`). `wc -l apps/core/internal/mutate/*.go apps/core/internal/grpcsrv/fake_dependencies_test.go apps/core/service_wiring.go`: mỗi file < 200 (`fixtures_test.go` ~172, `members_test.go` ~175, `member_rules_test.go` ~165, `member_race_test.go` ~165, `member_failures_test.go` ~45, `member_succession_test.go` ~100, `member_helpers_test.go` ~145, `member_commit.go` ~160, `members.go` ~127, `member_roles.go` ~95, `mutator.go` ~120, `fake_dependencies_test.go` ~103, `service_wiring.go` ~50).

**Step 7: Itest**

```bash
make infra-up && make itest
```

Expected: mọi package `ok` (wiring mới dựng `memberproj` trên Mongo thật; chưa có RPC member, itest cũ không đổi).

**Step 8: INDEXES + commit**

```bash
python3 <scratchpad>/indexes_edit.py <<'EOF'
[
 {"path": "apps/core/internal/mutate", "col": "purpose", "append": "; AddMembers/RemoveMember/LeaveRoom/ChangeMemberRole (D96-D100): validate (users deduped, 1..Limits.MemberBatch else ErrTooManyMembers; RemoveMember of oneself is InvalidArgument), Admit (LeaveRoom: AdmitRoom), DM -> ErrDirectRoom, then up to 3 tries of memberproj Settle -> MembersOf(caller + targets) -> Allow with caller, target and role -> desired-state no-op, ErrMemberNotFound or ErrLastOwner, a last owner's leave names the successor -> Append at head+1 (a duplicate mv with the same op, actor and user/role set is the result), else ErrRetryLater; after the fact: Project, ForgetMembers, enqueue pbconv.MemberEvents (failures ignored, the worker converges); added users carry the latest seq as their read position; Limits.MemberBatch (MEMBER_BATCH_MAX 2..1000, default 500)"},
 {"path": "apps/core/internal/mutate", "col": "key_symbols", "append": ";MemberProjector;MemberForgetter;DefaultMemberBatch;MaxMemberBatch;AddMembersCmd;RemoveMemberCmd;LeaveRoomCmd;ChangeRoleCmd;MemberResult;Mutator.AddMembers;Mutator.RemoveMember;Mutator.LeaveRoom;Mutator.ChangeMemberRole;Mutator.MemberBatch"},
 {"path": "apps/core/internal/mutate", "col": "decisions", "append": ";D96;D97;D98;D99;D100;D107"},
 {"path": "apps/core", "col": "purpose", "append": "; service_wiring.go builds memberproj.New(st.MemberActions(), st) for the mutator and passes the router as its member forgetter (D97, D103)"},
 {"path": "apps/core", "col": "decisions", "append": ";D97;D103"}
]
EOF
make -s go ARGS="fmt ./apps/core/..."
make fmt-check && make vet && make lint
git add apps/core/internal/mutate/members.go apps/core/internal/mutate/member_roles.go apps/core/internal/mutate/member_commit.go \
  apps/core/internal/mutate/member_helpers_test.go apps/core/internal/mutate/members_test.go apps/core/internal/mutate/member_rules_test.go \
  apps/core/internal/mutate/member_succession_test.go apps/core/internal/mutate/member_race_test.go apps/core/internal/mutate/member_failures_test.go
git commit -m "feat(mutate): add, remove, leave and change role as member facts" -- apps/core/internal/mutate/ apps/core/internal/grpcsrv/fake_dependencies_test.go apps/core/service_wiring.go INDEXES.csv
```

Expected: `{7}`; `git show --stat HEAD` có 17 file: 14 trong `mutate` (`limits.go`, `mutator.go`, `fixtures_test.go`, `delete_test.go`, `limits_test.go`, 3 file code mới, 6 file test mới), `grpcsrv/fake_dependencies_test.go`, `apps/core/service_wiring.go`, `INDEXES.csv`.

Task rủi ro: một reviewer (thứ tự settle → đọc doc → `Allow` → kế hoạch → append; no-op không ghi gì; nhận dạng trùng khoá theo op/người làm/tập (user, role); owner cuối; `Project`/`ForgetMembers`/enqueue chỉ sau khi fact commit và lỗi bị bỏ qua; tối đa `-count=3` trên `mutate`).

---

### Task 11: ★ `CreateRoom`: event `member_added` cho từng user, trần mỗi lệnh `MEMBER_BATCH_MAX`, bỏ trần 5000 của domain

Từ Task 4/5, `Rooms.Create` ghi fact `mv 1` = `domain.InitialMembers(room, members)` rồi projection. Fast path của `CreateRoom` giờ phát cùng lô với `room_created` các event của chính fact đó: `pbconv.MemberEvents(room.Type, domain.InitialMembers(room, members))` = bản room `{room}-m1` + một bản user `{room}-m1-{u}` cho mỗi member (envelope `recipient = u` → `evt.{t}.user.{u}.member_added`). Gateway cần bản user vì chưa ai sub subject của room mới (validation). Worker `member_event` (Task 15) dựng lại đúng các event đó từ fact (cùng hàm, cùng `At = CreatedAt`), stream bỏ trùng theo id (RC2). Lỗi enqueue vẫn bị bỏ qua như cũ.

Trần (D107): owner chốt "core không giới hạn số member mỗi group", nên `domain.NewRoom` bỏ `maxGroupMembers = 5000` (DM vẫn đúng 2). Giới hạn chuyển thành **mỗi lệnh**: `CreateRoom` từ chối `len(req.Members) > s.mutator.MemberBatch()` bằng `domain.ErrTooManyMembers` (`INVALID_ARGUMENT`) trước `NewRoom`, đếm thô cả user lặp (chặn bộ nhớ trước khi khử trùng); `AddMembers` đã chặn ở Task 10. Fact ban đầu luôn ≤ `MaxMemberChanges` (1000) nên `ValidateMemberAction` của store không bao giờ từ chối một room hợp lệ.

Ba test `grpcsrv` cũ so đúng danh sách event sau `createGroup` (`create_room_event_test.go`, `change_message_test.go`, `react_pin_test.go`) phải thêm các id member; helper `createdIDs` nằm trong file mới để `harness_test.go` (185 dòng) không lớn thêm.

**Files:**
- Modify: `apps/core/internal/domain/validate.go`, `room_test.go`
- Modify: `apps/core/internal/grpcsrv/create_room.go`
- Modify: `apps/core/internal/grpcsrv/create_room_event_test.go`, `create_room_test.go`, `change_message_test.go`, `react_pin_test.go`
- Create: `apps/core/internal/grpcsrv/room_created_ids_test.go`
- Modify: `INDEXES.csv`

**Step 1: Test domain**

`apps/core/internal/domain/room_test.go`, trong bảng `TestNewRoomRules`:
- thay dòng `{"group of 5001", "acme", "alice", domain.RoomGroup, "Big", users(5001), "members"},` bằng `{"group of 5001", "acme", "alice", domain.RoomGroup, "Big", users(5001), ""},`;
- sau dòng đó thêm `{"group of 20000", "acme", "alice", domain.RoomGroup, "Big", users(20000), ""},`.

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/domain/..."`
Expected: FAIL: `TestNewRoomRules/group_of_5001` và `TestNewRoomRules/group_of_20000` báo `NewRoom = invalid argument: members, want nil`.

**Step 3: Code domain**

`apps/core/internal/domain/validate.go`:
- xoá dòng `maxGroupMembers = 5000` khỏi khối `const` (gofmt căn lại);
- thay nguyên `func distinctMembers` bằng:

```go
func distinctMembers(typ RoomType, creator string, members []string) ([]string, error) {
	seen := make(map[string]struct{}, len(members))
	users := make([]string, 0, len(members))
	for _, u := range members {
		if err := ValidUser(u); err != nil {
			return nil, err
		}
		if _, dup := seen[u]; dup {
			continue
		}
		seen[u] = struct{}{}
		users = append(users, u)
	}
	if _, ok := seen[creator]; !ok || (typ == RoomDM && len(users) != 2) {
		return nil, invalid("members")
	}
	return users, nil
}
```

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/domain/..."`
Expected: PASS (`dm of one`, `dm of three`, `creator missing`, `no members` vẫn báo `members`).

**Step 4: Test `grpcsrv`**

`apps/core/internal/grpcsrv/room_created_ids_test.go`:

```go
package grpcsrv_test

import "github.com/ivannguyendev/chatim/apps/core/internal/pbconv"

func createdIDs(room uint64, users ...string) []string {
	out := []string{pbconv.RoomCreatedEventID(room), pbconv.MemberEventID(room, 1)}
	for _, u := range users {
		out = append(out, pbconv.MemberUserEventID(room, 1, u))
	}
	return out
}
```

`apps/core/internal/grpcsrv/create_room_event_test.go`:
- import thêm `".../internal/domain"`;
- thay nguyên `TestCreateRoomEnqueuesRoomCreated` bằng:

```go
func TestCreateRoomEnqueuesRoomCreatedAndTheFirstMembers(t *testing.T) {
	clock := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	newID, _ := idSequence(42)
	events := &recordingEvents{}
	rg := newRig(t, options{sender: &fakeSender{}, newID: newID, now: func() time.Time { return clock }, events: events})
	if _, err := rg.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "Team", Members: []string{"alice", "bob"},
	}); err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	stored, err := rg.rooms.Get(t.Context(), 42)
	if err != nil {
		t.Fatalf("Get(42): %v", err)
	}
	fact, err := rg.rooms.MemberActions().At(t.Context(), 42, 1)
	if err != nil {
		t.Fatalf("member fact 1: %v", err)
	}
	want := slices.Concat([]*chatimv1.Event{pbconv.RoomCreated(stored)}, pbconv.MemberEvents(domain.RoomGroup, fact))
	rooms, got := events.enqueued()
	if !slices.Equal(rooms, []uint64{42, 42, 42, 42}) || len(got) != len(want) {
		t.Fatalf("enqueued %v %v, want %d events for room 42", rooms, got, len(want))
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Fatalf("event %d = %v, want %v (the events the worker builds from fact 1)", i, got[i], want[i])
		}
	}
	var ids, recipients []string
	for _, ev := range got {
		ids, recipients = append(ids, ev.GetId()), append(recipients, ev.GetRecipient())
	}
	if !slices.Equal(ids, createdIDs(42, "alice", "bob")) || !slices.Equal(recipients, []string{"", "", "alice", "bob"}) {
		t.Fatalf("ids %v to %q, want room_created, the room copy and one copy per member", ids, recipients)
	}
}
```

- trong `TestCreateRoomEnqueuesOnlyForTheStoredRoom`, thay `if rooms, _ := events.enqueued(); !slices.Equal(rooms, []uint64{3}) {` bằng `if rooms, _ := events.enqueued(); !slices.Equal(rooms, []uint64{3, 3, 3, 3}) {` và thông điệp `t.Fatalf("enqueued for rooms %v, want only 3", rooms)` giữ nguyên.

`apps/core/internal/grpcsrv/create_room_test.go`: import thêm `"strconv"` và `".../internal/mutate"`; thêm cuối file:

```go
func TestCreateRoomCapsTheMembersOfOneRequest(t *testing.T) {
	members := func(n int) []string {
		out := []string{"alice"}
		for i := 1; i < n; i++ {
			out = append(out, "u"+strconv.Itoa(i))
		}
		return out
	}
	group := chatimv1.RoomType_ROOM_TYPE_GROUP
	rg := newRig(t, options{sender: &fakeSender{}})
	_, err := rg.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{Type: group, Name: "Big", Members: members(501)})
	expectCode(t, err, codes.InvalidArgument)
	resp, err := rg.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{Type: group, Name: "Big", Members: members(500)})
	if err != nil || resp.GetRoom().GetMemberCount() != 500 {
		t.Fatalf("CreateRoom with 500 members = %v, %v; want a room of 500", resp, err)
	}
	small := newRig(t, options{sender: &fakeSender{}, limits: mutate.Limits{MemberBatch: 3}})
	for _, req := range [][]string{members(4), {"alice", "bob", "bob", "carol"}} {
		_, err := small.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{Type: group, Name: "T", Members: req})
		expectCode(t, err, codes.InvalidArgument)
	}
	if _, err := small.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{Type: group, Name: "T", Members: members(3)}); err != nil {
		t.Fatalf("CreateRoom at the cap: %v", err)
	}
}
```

`apps/core/internal/grpcsrv/change_message_test.go`, trong `TestEditAndDeleteThroughTheService`, thay dòng
`want := []string{pbconv.RoomCreatedEventID(id), pbconv.MessageChangeEventID(id, 0, 1, 1), pbconv.MessageChangeEventID(id, 0, 1, 2)}`
bằng (file đã import `slices`):

```go
	want := slices.Concat(createdIDs(id, "alice", "bob"), []string{pbconv.MessageChangeEventID(id, 0, 1, 1), pbconv.MessageChangeEventID(id, 0, 1, 2)})
```

`apps/core/internal/grpcsrv/react_pin_test.go`, trong `TestReactAndPinThroughTheService`, thay khối `wantIDs := []string{ ... }` bằng:

```go
	wantIDs := slices.Concat(createdIDs(id, "alice", "bob"), []string{
		pbconv.ReactionEventID(id, 0, 1, "bob", 1), pbconv.ReactionCountsEventID(id, 0, 1, 1),
		pbconv.ReactionEventID(id, 0, 1, "alice", 1), pbconv.ReactionCountsEventID(id, 0, 1, 2),
		pbconv.PinEventID(id, 1), pbconv.PinEventID(id, 2),
	})
```

**Step 5: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/grpcsrv/..."`
Expected: FAIL:
- `TestCreateRoomEnqueuesRoomCreatedAndTheFirstMembers`: `enqueued [42] [...], want 4 events for room 42`;
- `TestCreateRoomEnqueuesOnlyForTheStoredRoom`: `enqueued for rooms [3], want only 3`;
- `TestEditAndDeleteThroughTheService` và `TestReactAndPinThroughTheService`: `enqueued [...] , want [...]` (thiếu các id `-m1`);
- `TestCreateRoomCapsTheMembersOfOneRequest`: `status = (OK, ""), want (InvalidArgument, "invalid argument")` (501 member vẫn được tạo vì chưa có trần mỗi lệnh).

**Step 6: Code `grpcsrv`**

`apps/core/internal/grpcsrv/create_room.go`:
- import thêm `"slices"`;
- ngay sau khối `typ, err := pbconv.DomainRoomType(req.GetType())` / `if err != nil { return nil, err }` thêm:

```go
	if len(req.GetMembers()) > s.mutator.MemberBatch() {
		return nil, domain.ErrTooManyMembers
	}
```

- trong `case err == nil:` thay dòng `_ = s.events.Enqueue(room.ID, []*chatimv1.Event{pbconv.RoomCreated(room)})` bằng:

```go
			events := slices.Concat([]*chatimv1.Event{pbconv.RoomCreated(room)}, pbconv.MemberEvents(room.Type, domain.InitialMembers(room, members)))
			_ = s.events.Enqueue(room.ID, events)
```

**Step 7: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/internal/grpcsrv/... ./apps/core/internal/domain/..."
make vet
```

Expected: PASS. `wc -l apps/core/internal/grpcsrv/create_room.go apps/core/internal/grpcsrv/create_room_test.go apps/core/internal/grpcsrv/create_room_event_test.go apps/core/internal/grpcsrv/harness_test.go`: ~53, ~142, ~108, 185 (không đổi).

**Step 8: INDEXES + commit**

```bash
python3 <scratchpad>/indexes_edit.py <<'EOF'
[
 {"path": "apps/core/internal/grpcsrv", "col": "purpose", "append": "; CreateRoom refuses more than Mutator.MemberBatch() members per request (raw count, ErrTooManyMembers, D107) and enqueues room_created plus pbconv.MemberEvents of domain.InitialMembers (the room copy and one user copy per member, the same events the worker builds from member fact 1) in one batch (D102)"},
 {"path": "apps/core/internal/grpcsrv", "col": "decisions", "append": ";D102;D107"},
 {"path": "apps/core/internal/domain", "col": "purpose", "append": "; NewRoom no longer caps the size of a group (a DM stays exactly 2); the cap is per request, MEMBER_BATCH_MAX in grpcsrv.CreateRoom and mutate.AddMembers (D107)"},
 {"path": "apps/core/internal/domain", "col": "decisions", "append": ";D107"}
]
EOF
make -s go ARGS="fmt ./apps/core/..."
make fmt-check && make vet && make lint
git add apps/core/internal/grpcsrv/room_created_ids_test.go
git commit -m "feat(grpcsrv): announce the first members of a new room and cap members per request" -- apps/core/internal/grpcsrv/ apps/core/internal/domain/ INDEXES.csv
```

Expected: `{7}`; `git show --stat HEAD` có 9 file (`validate.go`, `room_test.go`, `create_room.go`, 4 test sửa, `room_created_ids_test.go`, `INDEXES.csv`).

Task rủi ro: một reviewer (trần kiểm trước `NewRoom`, event fast path giống hệt event worker dựng từ fact 1, `NewRoom` vẫn giữ luật DM; tối đa `-count=3` trên `grpcsrv`).

---

### Task 12: ★ Package `readcast` (coalescer `read_updated`)

`read_updated` là event tần suất cao gộp được (D105): mỗi lần `MarkRead`/`MarkUnread` đổi vị trí đọc, `mutate` (Task 13) gọi `Caster.Offer`. Caster giữ một map có trần theo khoá `(room, user)`:
- **Leading edge:** khoá chưa có, hoặc lần gửi trước đã quá `Window` → gửi ngay (trong chính `Offer`, không chặn: `Publisher.Enqueue` không bao giờ chặn), ghi `sentAt` và version đã gửi. Có bản pending cũ hơn thì gửi bản version lớn nhất.
- **Trong cửa sổ:** giữ bản `Pos.Version` lớn nhất làm pending. Bản có version ≤ version đã gửi là cũ (hai lệnh đọc đua nhau) và bị bỏ.
- **Trailing edge:** `Run` có ticker `Window/2`; khoá có pending đã đủ `Window` kể từ lần gửi → gửi pending; khoá không pending rảnh quá `Window` → xoá khỏi map. Độ trễ phần đuôi tối đa `1.5 × Window`.
- **Có trần:** map đầy (`MaxPending`) với khoá mới, hoặc sau `Close` → gửi thẳng, `Unbatched()++` (metric `read_events_unbatched_total`, Task 14).
- **Subject:** `roomWide = Type == dm || Members <= MaxMembers` → `pbconv.ReadUpdated(u, roomWide)`: bản room (DM và group nhỏ) hoặc `recipient = user` (group lớn: chỉ đồng bộ các thiết bị của chính user).
- **Dừng:** `Close` (idempotent) dừng `Run` (trả `nil`), xả mọi pending vào `Enqueue` một lần; `Run` trả `ctx.Err()` khi context hết. Lỗi `Enqueue` bị bỏ qua (publisher đã đếm `publish_dropped_total`). Best-effort, không reconciler.

Một goroutine (`Run`) + mutex; thứ tự gửi giữa các khoá khác nhau không được bảo đảm, trong một khoá các lần gửi cách nhau ≥ `Window` (trừ đường gửi thẳng). Test dùng `synctest` (thời gian ảo, ticker chạy đúng nhịp) + goleak; chạy `-count=5`.

**Files:**
- Create: `apps/core/internal/readcast/config.go`, `caster.go`
- Create: `apps/core/internal/readcast/helpers_test.go`, `config_test.go`, `caster_test.go`, `close_test.go`
- Modify: `INDEXES.csv`

**Step 1: Test**

`apps/core/internal/readcast/helpers_test.go`:

```go
package readcast_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/readcast"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }

var stamp = time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)

type recorder struct {
	mu     sync.Mutex
	rooms  []uint64
	events []*chatimv1.Event
}

func (r *recorder) Enqueue(room uint64, events []*chatimv1.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ev := range events {
		r.rooms = append(r.rooms, room)
		r.events = append(r.events, ev)
	}
	return nil
}

func (r *recorder) list() ([]uint64, []*chatimv1.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.rooms), slices.Clone(r.events)
}

func (r *recorder) ids() []string {
	_, events := r.list()
	out := make([]string, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.GetId())
	}
	return out
}

func read(room uint64, user string, seq, version uint64) domain.ReadUpdate {
	return domain.ReadUpdate{
		Room: room, Tenant: "acme", Type: domain.RoomGroup, Members: 3, User: user,
		Pos: domain.ReadPos{Seq: seq, Version: version}, At: stamp,
	}
}

func id(room uint64, user string, version uint64) string {
	return pbconv.ReadEventID(room, user, version)
}

func newCaster(t *testing.T, pub readcast.Publisher, cfg readcast.Config) *readcast.Caster {
	t.Helper()
	c, err := readcast.New(pub, cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func running(t *testing.T, c *readcast.Caster) func() error {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	return func() error {
		cancel()
		return <-done
	}
}
```

`apps/core/internal/readcast/config_test.go`:

```go
package readcast_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/readcast"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestConfigFillsDefaultsAndChecksBounds(t *testing.T) {
	if readcast.DefaultWindow != 3*time.Second || readcast.DefaultMaxMembers != 100 || readcast.DefaultMaxPending != 65536 ||
		readcast.MinWindow != 100*time.Millisecond || readcast.MaxWindow != time.Minute || readcast.MaxMembersCap != 1000 {
		t.Fatalf("defaults and bounds changed")
	}
	cases := []struct {
		name string
		cfg  readcast.Config
		ok   bool
	}{
		{"defaults", readcast.Config{}, true},
		{"smallest", readcast.Config{Window: readcast.MinWindow, MaxMembers: 1, MaxPending: 1}, true},
		{"largest", readcast.Config{Window: readcast.MaxWindow, MaxMembers: readcast.MaxMembersCap}, true},
		{"window too short", readcast.Config{Window: readcast.MinWindow - time.Millisecond}, false},
		{"window too long", readcast.Config{Window: readcast.MaxWindow + time.Millisecond}, false},
		{"negative window", readcast.Config{Window: -time.Second}, false},
		{"too many members", readcast.Config{MaxMembers: readcast.MaxMembersCap + 1}, false},
		{"negative members", readcast.Config{MaxMembers: -1}, false},
		{"negative pending", readcast.Config{MaxPending: -1}, false},
	}
	for _, c := range cases {
		err := c.cfg.Validate()
		if (err == nil) != c.ok || (err != nil && !errors.Is(err, apperr.ErrInvalidArgument)) {
			t.Fatalf("%s: Validate() = %v, want ok=%v", c.name, err, c.ok)
		}
		if _, err := readcast.New(&recorder{}, c.cfg); (err == nil) != c.ok {
			t.Fatalf("%s: New = %v, want ok=%v", c.name, err, c.ok)
		}
	}
	if _, err := readcast.New(nil, readcast.Config{}); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("New without a publisher = %v, want ErrInvalidArgument", err)
	}
}
```

`apps/core/internal/readcast/caster_test.go`:

```go
package readcast_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/readcast"
)

const window = readcast.DefaultWindow

func TestTheFirstReceiptOfAKeyGoesOutAtOnce(t *testing.T) {
	rec := &recorder{}
	c := newCaster(t, rec, readcast.Config{})
	u := read(1, "bob", 5, 2)
	c.Offer(u)
	rooms, events := rec.list()
	if !slices.Equal(rooms, []uint64{1}) || len(events) != 1 || !proto.Equal(events[0], pbconv.ReadUpdated(u, true)) {
		t.Fatalf("sent %v %v, want one room-wide read_updated for room 1", rooms, events)
	}
	if c.Unbatched() != 0 {
		t.Fatalf("Unbatched() = %d, want 0", c.Unbatched())
	}
}

func TestReceiptsInsideTheWindowCollapseIntoTheNewest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		c := newCaster(t, rec, readcast.Config{})
		stop := running(t, c)
		for _, v := range []uint64{1, 3, 4, 2} {
			c.Offer(read(1, "bob", v, v))
		}
		c.Offer(read(1, "carol", 1, 7))
		time.Sleep(window - time.Millisecond)
		synctest.Wait()
		if got := rec.ids(); !slices.Equal(got, []string{id(1, "bob", 1), id(1, "carol", 7)}) {
			t.Fatalf("sent %v before the window closed, want only the two leading edges", got)
		}
		time.Sleep(window/2 + time.Millisecond)
		synctest.Wait()
		if got := rec.ids(); !slices.Equal(got, []string{id(1, "bob", 1), id(1, "carol", 7), id(1, "bob", 4)}) {
			t.Fatalf("sent %v after the window, want bob's newest (v4) as the trailing edge", got)
		}
		if err := stop(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v, want context.Canceled", err)
		}
	})
}

func TestStaleReceiptsAreDroppedAndAnIdleKeyLeadsAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		c := newCaster(t, rec, readcast.Config{})
		c.Offer(read(1, "bob", 5, 5))
		c.Offer(read(1, "bob", 3, 3))
		time.Sleep(window)
		c.Offer(read(1, "bob", 4, 4))
		c.Offer(read(1, "bob", 6, 6))
		if got := rec.ids(); !slices.Equal(got, []string{id(1, "bob", 5), id(1, "bob", 6)}) {
			t.Fatalf("sent %v, want v5 then v6 at once after the window, stale v3 and v4 dropped", got)
		}
		if err := c.Close(t.Context()); err != nil || len(rec.ids()) != 2 {
			t.Fatalf("Close = %v with %v sent, want nothing pending", err, rec.ids())
		}
	})
}

func TestAFullMapSendsAtOnceAndCountsIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		c := newCaster(t, rec, readcast.Config{MaxPending: 1})
		stop := running(t, c)
		c.Offer(read(1, "bob", 1, 1))
		c.Offer(read(1, "carol", 1, 1))
		c.Offer(read(1, "carol", 2, 2))
		c.Offer(read(1, "bob", 2, 2))
		want := []string{id(1, "bob", 1), id(1, "carol", 1), id(1, "carol", 2)}
		if got := rec.ids(); !slices.Equal(got, want) || c.Unbatched() != 2 {
			t.Fatalf("sent %v with %d unbatched, want %v with 2", got, c.Unbatched(), want)
		}
		time.Sleep(window + window/2)
		synctest.Wait()
		want = append(want, id(1, "bob", 2))
		if got := rec.ids(); !slices.Equal(got, want) {
			t.Fatalf("sent %v, want bob's pending v2 after the window", got)
		}
		time.Sleep(2 * window)
		synctest.Wait()
		c.Offer(read(1, "carol", 3, 3))
		c.Offer(read(1, "carol", 4, 4))
		want = append(want, id(1, "carol", 3))
		if got := rec.ids(); !slices.Equal(got, want) || c.Unbatched() != 2 {
			t.Fatalf("sent %v with %d unbatched, want carol tracked once bob went idle", got, c.Unbatched())
		}
		if err := stop(); !errors.Is(err, context.Canceled) {
			t.Fatalf("Run = %v, want context.Canceled", err)
		}
		if err := c.Close(t.Context()); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if got := rec.ids(); !slices.Equal(got, append(want, id(1, "carol", 4))) {
			t.Fatalf("sent %v, want carol's pending v4 flushed by Close", got)
		}
	})
}

func TestTheSubjectFollowsTheRoomSize(t *testing.T) {
	rec := &recorder{}
	c := newCaster(t, rec, readcast.Config{MaxMembers: 2})
	small, big, dm := read(1, "bob", 1, 1), read(2, "bob", 1, 1), read(3, "bob", 1, 1)
	small.Members, big.Members = 2, 3
	dm.Type, dm.Members = domain.RoomDM, 3
	for _, u := range []domain.ReadUpdate{small, big, dm} {
		c.Offer(u)
	}
	_, events := rec.list()
	want := []bool{true, false, true}
	if len(events) != len(want) {
		t.Fatalf("sent %d events, want %d", len(events), len(want))
	}
	for i, u := range []domain.ReadUpdate{small, big, dm} {
		if !proto.Equal(events[i], pbconv.ReadUpdated(u, want[i])) {
			t.Fatalf("event for room %d = %v, want room-wide %v", u.Room, events[i], want[i])
		}
	}
	if events[0].GetRecipient() != "" || events[1].GetRecipient() != "bob" || events[2].GetRecipient() != "" {
		t.Fatalf("recipients %q %q %q, want the room, bob only, the room", events[0].GetRecipient(), events[1].GetRecipient(), events[2].GetRecipient())
	}
}
```

`apps/core/internal/readcast/close_test.go`:

```go
package readcast_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/ivannguyendev/chatim/apps/core/internal/readcast"
)

func TestCloseFlushesPendingReceiptsAndStopsRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		c := newCaster(t, rec, readcast.Config{})
		done := make(chan error, 1)
		go func() { done <- c.Run(t.Context()) }()
		c.Offer(read(1, "bob", 1, 1))
		c.Offer(read(1, "bob", 2, 2))
		c.Offer(read(2, "carol", 1, 1))
		c.Offer(read(2, "carol", 3, 3))
		if err := c.Close(context.Background()); err != nil {
			t.Fatalf("Close: %v", err)
		}
		if err := <-done; err != nil {
			t.Fatalf("Run after Close = %v, want nil", err)
		}
		want := []string{id(1, "bob", 1), id(1, "bob", 2), id(2, "carol", 1), id(2, "carol", 3)}
		got := rec.ids()
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("sent %v, want both leading edges and both pending receipts", got)
		}
		if err := c.Close(context.Background()); err != nil || len(rec.ids()) != 4 {
			t.Fatalf("second Close = %v with %d sent, want a no-op", err, len(rec.ids()))
		}
		c.Offer(read(1, "bob", 3, 3))
		if len(rec.ids()) != 5 || c.Unbatched() != 1 {
			t.Fatalf("an offer after Close sent %d in total with %d unbatched, want 5 and 1", len(rec.ids()), c.Unbatched())
		}
	})
}

func TestOffersFromManyGoroutinesEndOnTheNewestVersion(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		c := newCaster(t, rec, readcast.Config{})
		users := []string{"bob", "carol", "dave", "erin"}
		var wg sync.WaitGroup
		for _, u := range users {
			for part := range 4 {
				wg.Go(func() {
					for v := uint64(part + 1); v <= 40; v += 4 {
						c.Offer(read(7, u, v, v))
					}
				})
			}
		}
		wg.Wait()
		if err := c.Close(t.Context()); err != nil {
			t.Fatalf("Close: %v", err)
		}
		last, sent := map[string]uint64{}, map[string]int{}
		_, events := rec.list()
		for _, ev := range events {
			r := ev.GetReadUpdated()
			if r.GetVersion() <= last[r.GetUser()] {
				t.Fatalf("%s went back from v%d to v%d", r.GetUser(), last[r.GetUser()], r.GetVersion())
			}
			last[r.GetUser()], sent[r.GetUser()] = r.GetVersion(), sent[r.GetUser()]+1
		}
		for _, u := range users {
			if last[u] != 40 || sent[u] > 2 {
				t.Fatalf("%s: last v%d after %d sends, want v40 within 2 sends", u, last[u], sent[u])
			}
		}
	})
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/readcast/..."`
Expected: FAIL build: `no non-test Go files in /src/apps/core/internal/readcast` (package mới chỉ có file test).

**Step 3: Code**

`apps/core/internal/readcast/config.go`:

```go
package readcast

import (
	"cmp"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	DefaultWindow     = 3 * time.Second
	MinWindow         = 100 * time.Millisecond
	MaxWindow         = time.Minute
	DefaultMaxMembers = 100
	MaxMembersCap     = 1000
	DefaultMaxPending = 65536
)

type Config struct {
	Window     time.Duration
	MaxMembers int
	MaxPending int
}

func (c Config) Validate() error { return c.withDefaults().validate() }

func (c Config) withDefaults() Config {
	c.Window = cmp.Or(c.Window, DefaultWindow)
	c.MaxMembers = cmp.Or(c.MaxMembers, DefaultMaxMembers)
	c.MaxPending = cmp.Or(c.MaxPending, DefaultMaxPending)
	return c
}

func (c Config) validate() error {
	if c.Window < MinWindow || c.Window > MaxWindow || c.MaxMembers < 1 || c.MaxMembers > MaxMembersCap || c.MaxPending < 1 {
		return fmt.Errorf("%w: read receipts need a window of %v to %v, 1 to %d room-wide members and a positive pending bound, got %+v",
			apperr.ErrInvalidArgument, MinWindow, MaxWindow, MaxMembersCap, c)
	}
	return nil
}
```

`apps/core/internal/readcast/caster.go`:

```go
package readcast

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errNoPublisher = fmt.Errorf("%w: read receipts need a publisher", apperr.ErrInvalidArgument)

type Publisher interface {
	Enqueue(room uint64, events []*chatimv1.Event) error
}

type key struct {
	room uint64
	user string
}

type entry struct {
	sentAt  time.Time
	sent    uint64
	pending domain.ReadUpdate
	waiting bool
}

type Caster struct {
	pub       Publisher
	cfg       Config
	mu        sync.Mutex
	entries   map[key]*entry
	closed    bool
	stop      chan struct{}
	unbatched atomic.Uint64
}

func New(pub Publisher, cfg Config) (*Caster, error) {
	if pub == nil {
		return nil, errNoPublisher
	}
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Caster{pub: pub, cfg: cfg, entries: make(map[key]*entry), stop: make(chan struct{})}, nil
}

func (c *Caster) Offer(u domain.ReadUpdate) {
	if out, ok := c.admit(u, time.Now()); ok {
		c.send(out)
	}
}

func (c *Caster) Run(ctx context.Context) error {
	tick := time.NewTicker(c.cfg.Window / 2)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.stop:
			return nil
		case <-tick.C:
			for _, u := range c.due(time.Now()) {
				c.send(u)
			}
		}
	}
}

func (c *Caster) Close(_ context.Context) error {
	c.mu.Lock()
	var out []domain.ReadUpdate
	if !c.closed {
		c.closed = true
		close(c.stop)
		for _, e := range c.entries {
			if e.waiting {
				out = append(out, e.pending)
			}
		}
		c.entries = nil
	}
	c.mu.Unlock()
	for _, u := range out {
		c.send(u)
	}
	return nil
}

func (c *Caster) Unbatched() uint64 { return c.unbatched.Load() }

func (c *Caster) admit(u domain.ReadUpdate, now time.Time) (domain.ReadUpdate, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := key{room: u.Room, user: u.User}
	e, ok := c.entries[k]
	switch {
	case c.closed || (!ok && len(c.entries) >= c.cfg.MaxPending):
		c.unbatched.Add(1)
		return u, true
	case !ok:
		c.entries[k] = &entry{sentAt: now, sent: u.Pos.Version}
		return u, true
	}
	if e.waiting && e.pending.Pos.Version > u.Pos.Version {
		u = e.pending
	}
	if u.Pos.Version <= e.sent {
		return domain.ReadUpdate{}, false
	}
	if now.Sub(e.sentAt) < c.cfg.Window {
		e.pending, e.waiting = u, true
		return domain.ReadUpdate{}, false
	}
	e.sentAt, e.sent, e.waiting = now, u.Pos.Version, false
	return u, true
}

func (c *Caster) due(now time.Time) []domain.ReadUpdate {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []domain.ReadUpdate
	for k, e := range c.entries {
		if now.Sub(e.sentAt) < c.cfg.Window {
			continue
		}
		if !e.waiting {
			delete(c.entries, k)
			continue
		}
		out = append(out, e.pending)
		e.sentAt, e.sent, e.waiting = now, e.pending.Pos.Version, false
	}
	return out
}

func (c *Caster) send(u domain.ReadUpdate) {
	roomWide := u.Type == domain.RoomDM || u.Members <= c.cfg.MaxMembers
	_ = c.pub.Enqueue(u.Room, []*chatimv1.Event{pbconv.ReadUpdated(u, roomWide)})
}
```

**Step 4: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/readcast/..."
make vet
```

Expected: PASS 5 lần, goleak sạch. `wc -l apps/core/internal/readcast/*.go`: `caster.go` ~150, `config.go` ~45, `helpers_test.go` ~85, `config_test.go` ~50, `caster_test.go` ~150, `close_test.go` ~85.

**Step 5: INDEXES + commit**

```bash
python3 <scratchpad>/indexes_edit.py <<'EOF'
[
 {"path": "apps/core/internal/readcast", "after": "apps/core/internal/publish/publishtest", "row": ["package", "Coalescer for read_updated events (D105): Caster.Offer sends the first receipt of a (room, user) at once and keeps only the newest version inside READ_RECEIPT_WINDOW (stale versions dropped); Run ticks every Window/2 to send the trailing receipt and forget idle keys; the key map is bounded by MaxPending (a full map or a closed caster sends at once and counts Unbatched); DM rooms and groups of at most READ_RECEIPT_MAX_MEMBERS get the room subject, larger groups only the reader's user subject (pbconv.ReadUpdated); Close stops Run and flushes pending receipts into the publisher; best-effort, no reconciler", "Publisher;Config;Config.Validate;DefaultWindow;MinWindow;MaxWindow;DefaultMaxMembers;MaxMembersCap;DefaultMaxPending;Caster;New;Caster.Offer;Caster.Run;Caster.Close;Caster.Unbatched", "", "unit;synctest;goleak", "D104;D105"]}
]
EOF
make -s go ARGS="fmt ./apps/core/..."
make fmt-check && make vet && make lint
git add apps/core/internal/readcast/
git commit -m "feat(readcast): coalesce read receipts per room and user" -- apps/core/internal/readcast/ INDEXES.csv
```

Expected: `{7}`; `git show --stat HEAD` có 7 file. (`used_by` để trống: Task 13 thêm `apps/core`, Task 14 thêm `apps/core/internal/config`.)

Task rủi ro: một reviewer (leading/trailing edge, bỏ version cũ, map có trần và xoá khoá rảnh, `Close` idempotent và xả pending đúng một lần, `Run` dừng theo cả `Close` lẫn context, không gửi trong khi giữ mutex; tối đa `-count=3` trên `readcast`).

---

### Task 13: `mutate.MarkRead`/`MarkUnread` + Deps (`Reads`, `ReadCast`) + `wireService` nhận `readcast`

Vị trí đọc thuộc lớp **tần suất cao gộp được** (D104): không fact, không reconciler, mỗi lần đổi `rs.v + 1` (ai tới sau thắng). Thứ tự:
1. `MarkUnread` với `seq == 0` → `invalid("seq")` (phải chỉ ra tin muốn đánh dấu chưa đọc).
2. `Authorize(mark_read)`: `Admit` (tenant + member active; người đã rời/bị xoá → `ErrNotMember`) rồi hỏi policy (`DefaultPolicy` luôn cho).
3. Kẹp seq theo seq cuối **thật**: `0 < seq ≤ room.LastSeq` (đã nạp cùng `Admit`, `ls` không bao giờ vượt seq thật) → dùng luôn, không đọc thêm; còn lại → `Messages.Last(room, 0)`; `seq == 0` hoặc `seq > last` → `last`. Không bao giờ kẹp theo `ls` đang trễ.
4. Kết quả kẹp là 0 (room chưa có tin) → trả `Read` hiện tại, không ghi.
5. `MarkRead` → `Reads.MarkRead(room, user, seq)` (chỉ nâng); `MarkUnread` → `Reads.MarkUnread(room, user, seq − 1)` (chỉ hạ; đặt thẳng `seq − 1` có thể làm vị trí tiến lên, validation).
6. Đổi (`changed`) → `ReadCast.Offer(ReadUpdate{Room, Tenant, Type, Members: room.MemberCount, User, Pos, At: now()})`. Trả `ReadPos` (đổi hay không).

DM được đánh dấu đọc bình thường (luật DM cố định chỉ áp cho lệnh member).

`mutate.New` bắt buộc thêm `Reads` và `ReadCast`: sửa rig `mutate`, `TestNewRequiresEveryDependency`, rig `grpcsrv` (dựng `readcast.New(events, readcast.Config{})` thật, `Close` ở cleanup) và `wireService` (chữ ký hợp đồng `wireService(st, router, pub, reads *readcast.Caster, lockedKinds, limits, log)`; `wire` dựng `readcast.New(pub, readcast.Config{})`). Từ task này tới Task 14, `readcast` trong core chưa chạy `Run`/`Close` (chỉ leading edge và đường gửi thẳng); không chạy core ở khoảng này.

**Files:**
- Create: `apps/core/internal/mutate/read.go`
- Modify: `apps/core/internal/mutate/mutator.go`, `fixtures_test.go`, `delete_test.go`
- Create: `apps/core/internal/mutate/read_helpers_test.go`, `read_test.go`
- Modify: `apps/core/internal/grpcsrv/fake_dependencies_test.go`
- Modify: `apps/core/service_wiring.go`, `apps/core/wiring.go`
- Modify: `INDEXES.csv`

**Step 1: Test**

`apps/core/internal/mutate/fixtures_test.go`:
- trong `type rig struct`, sau `forgets   *forgetSpy` thêm `reads     *readSpy` (gofmt căn cột);
- trong `newRig`, literal `rig{...}`: thay `events: &recordingEvents{}, forgets: &forgetSpy{},` bằng `events: &recordingEvents{}, forgets: &forgetSpy{}, reads: &readSpy{},`;
- trong `deps`, sau dòng `MemberActions: rg.rooms.MemberActions(), MemberReader: rg.rooms, MemberProj: members, Forget: rg.forgets,` thêm dòng `Reads: rg.rooms, ReadCast: rg.reads,`.

`apps/core/internal/mutate/delete_test.go`, trong map của `TestNewRequiresEveryDependency`, sau `"no forgetter": ...` thêm:

```go
		"no read positions":  func(d *mutate.Deps) { d.Reads = nil },
		"no read notifier":   func(d *mutate.Deps) { d.ReadCast = nil },
```

`apps/core/internal/mutate/read_helpers_test.go`:

```go
package mutate_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
)

type readSpy struct {
	mu      sync.Mutex
	updates []domain.ReadUpdate
}

func (s *readSpy) Offer(u domain.ReadUpdate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.updates = append(s.updates, u)
}

func (s *readSpy) list() []domain.ReadUpdate {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.updates)
}

type countingMessages struct {
	mutate.Messages
	mu    sync.Mutex
	lasts int
}

func (c *countingMessages) Last(ctx context.Context, room, thread uint64) (uint64, error) {
	c.mu.Lock()
	c.lasts++
	c.mu.Unlock()
	return c.Messages.Last(ctx, room, thread)
}

func (c *countingMessages) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lasts
}

func readCmd(user string, seq uint64) mutate.ReadCmd {
	return mutate.ReadCmd{Tenant: tenant, User: user, Room: room, Seq: seq}
}

func (rg *rig) markRead(t *testing.T, user string, seq uint64) domain.ReadPos {
	t.Helper()
	pos, err := rg.m.MarkRead(t.Context(), readCmd(user, seq))
	if err != nil {
		t.Fatalf("%s marks seq %d read: %v", user, seq, err)
	}
	return pos
}

func (rg *rig) markUnread(t *testing.T, user string, seq uint64) domain.ReadPos {
	t.Helper()
	pos, err := rg.m.MarkUnread(t.Context(), readCmd(user, seq))
	if err != nil {
		t.Fatalf("%s marks seq %d unread: %v", user, seq, err)
	}
	return pos
}

func (rg *rig) readUpdate(user string, pos domain.ReadPos) domain.ReadUpdate {
	return domain.ReadUpdate{Room: room, Tenant: tenant, Type: domain.RoomGroup, Members: 3, User: user, Pos: pos, At: rg.at()}
}

func sameUpdate(a, b domain.ReadUpdate) bool {
	at := a.At.Equal(b.At)
	a.At, b.At = time.Time{}, time.Time{}
	return at && a == b
}
```

`apps/core/internal/mutate/read_test.go`:

```go
package mutate_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func (rg *rig) sendThree(t *testing.T) uint64 {
	t.Helper()
	for seq := uint64(1); seq <= 3; seq++ {
		rg.send(t, seq, "alice", "m")
	}
	bob, _ := rg.member(t, "bob")
	return bob.Read.Version
}

func TestMarkReadOnlyMovesForward(t *testing.T) {
	rg := newRig(t, nil)
	v := rg.sendThree(t)
	steps := []struct {
		seq  uint64
		want domain.ReadPos
	}{
		{2, domain.ReadPos{Seq: 2, Version: v + 1}},
		{1, domain.ReadPos{Seq: 2, Version: v + 1}},
		{0, domain.ReadPos{Seq: 3, Version: v + 2}},
		{9, domain.ReadPos{Seq: 3, Version: v + 2}},
	}
	for _, s := range steps {
		if got := rg.markRead(t, "bob", s.seq); got != s.want {
			t.Fatalf("MarkRead(%d) = %+v, want %+v", s.seq, got, s.want)
		}
	}
	if bob, _ := rg.member(t, "bob"); bob.Read != (domain.ReadPos{Seq: 3, Version: v + 2}) {
		t.Fatalf("stored read position = %+v, want seq 3 at version %d", bob.Read, v+2)
	}
	want := []domain.ReadUpdate{rg.readUpdate("bob", domain.ReadPos{Seq: 2, Version: v + 1}), rg.readUpdate("bob", domain.ReadPos{Seq: 3, Version: v + 2})}
	if got := rg.reads.list(); !slices.EqualFunc(got, want, sameUpdate) {
		t.Fatalf("offered %+v, want only the two changes %+v", got, want)
	}
}

func TestMarkUnreadOnlyMovesBack(t *testing.T) {
	rg := newRig(t, nil)
	v := rg.sendThree(t)
	rg.markRead(t, "bob", 0)
	steps := []struct {
		seq  uint64
		want domain.ReadPos
	}{
		{2, domain.ReadPos{Seq: 1, Version: v + 2}},
		{3, domain.ReadPos{Seq: 1, Version: v + 2}},
		{1, domain.ReadPos{Seq: 0, Version: v + 3}},
		{9, domain.ReadPos{Seq: 0, Version: v + 3}},
	}
	for _, s := range steps {
		if got := rg.markUnread(t, "bob", s.seq); got != s.want {
			t.Fatalf("MarkUnread(%d) = %+v, want %+v", s.seq, got, s.want)
		}
	}
	if _, err := rg.m.MarkUnread(t.Context(), readCmd("bob", 0)); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("MarkUnread(0) = %v, want ErrInvalidArgument", err)
	}
	if n := len(rg.reads.list()); n != 3 {
		t.Fatalf("offered %d updates, want 3 (one read, two unreads)", n)
	}
}

func TestAnEmptyRoomKeepsTheReadPosition(t *testing.T) {
	rg := newRig(t, nil)
	before, _ := rg.member(t, "bob")
	if got := rg.markRead(t, "bob", 0); got != before.Read {
		t.Fatalf("MarkRead in an empty room = %+v, want %+v", got, before.Read)
	}
	if got := rg.markUnread(t, "bob", 4); got != before.Read {
		t.Fatalf("MarkUnread in an empty room = %+v, want %+v", got, before.Read)
	}
	if after, _ := rg.member(t, "bob"); after.Read != before.Read || len(rg.reads.list()) != 0 {
		t.Fatalf("read position %+v with %d offers, want %+v untouched", after.Read, len(rg.reads.list()), before.Read)
	}
}

func TestReadNeedsAnActiveMember(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "m")
	rg.remove(t, "alice", "bob")
	for _, user := range []string{"bob", "mallory"} {
		if _, err := rg.m.MarkRead(t.Context(), readCmd(user, 1)); !errors.Is(err, domain.ErrNotMember) {
			t.Fatalf("%s MarkRead = %v, want ErrNotMember", user, err)
		}
		if _, err := rg.m.MarkUnread(t.Context(), readCmd(user, 1)); !errors.Is(err, domain.ErrNotMember) {
			t.Fatalf("%s MarkUnread = %v, want ErrNotMember", user, err)
		}
	}
	elsewhere := readCmd("carol", 1)
	elsewhere.Tenant = "other"
	if _, err := rg.m.MarkRead(t.Context(), elsewhere); !errors.Is(err, domain.ErrRoomNotFound) {
		t.Fatalf("MarkRead from another tenant = %v, want ErrRoomNotFound", err)
	}
	if n := len(rg.reads.list()); n != 0 {
		t.Fatalf("refused reads offered %d updates", n)
	}
}

func TestTheClampReadsTheLastSeqOnlyPastTheRoomHead(t *testing.T) {
	rg := newRig(t, nil)
	rg.sendThree(t)
	if err := rg.rooms.TouchActivity(t.Context(), []store.Activity{{Room: room, Seq: 3, At: created}}); err != nil {
		t.Fatalf("TouchActivity: %v", err)
	}
	d := rg.deps(t, nil)
	counted := &countingMessages{Messages: d.Messages}
	d.Messages = counted
	rg.m = rg.build(t, d)
	rg.markRead(t, "bob", 2)
	if n := counted.count(); n != 0 {
		t.Fatalf("Last read %d times for a seq below the room head, want 0", n)
	}
	rg.markRead(t, "bob", 0)
	rg.markUnread(t, "bob", 5)
	if n := counted.count(); n != 2 {
		t.Fatalf("Last read %d times, want 2 (seq 0 and a seq past the head)", n)
	}
}

func TestDirectRoomsTrackReadPositionsToo(t *testing.T) {
	rg := newRig(t, nil)
	const dm uint64 = 77
	r := domain.Room{ID: dm, Tenant: tenant, Type: domain.RoomDM, CreatedBy: "alice", CreatedAt: created, MemberCount: 2}
	members := []domain.Member{
		{Room: dm, Tenant: tenant, User: "alice", Role: domain.RoleOwner, JoinedAt: created},
		{Room: dm, Tenant: tenant, User: "bob", Role: domain.RoleMember, JoinedAt: created},
	}
	if err := rg.rooms.Create(t.Context(), r, members); err != nil {
		t.Fatalf("create dm: %v", err)
	}
	msg := domain.Message{Room: dm, Seq: 1, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "hi", CID: "c-1", CreatedAt: created}
	if res := rg.msgs.Insert(t.Context(), []domain.Message{msg}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert: %+v", res)
	}
	pos, err := rg.m.MarkRead(t.Context(), mutate.ReadCmd{Tenant: tenant, User: "bob", Room: dm})
	offers := rg.reads.list()
	if err != nil || pos.Seq != 1 || len(offers) != 1 || offers[0].Type != domain.RoomDM || offers[0].Members != 2 || offers[0].Room != dm {
		t.Fatalf("MarkRead in a dm = %+v, %v with offers %+v; want seq 1 offered as a dm of 2", pos, err, offers)
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/mutate/..."`
Expected: FAIL biên dịch (có thể dừng ở 10 lỗi, `too many errors`), các lỗi thuộc nhóm: `unknown field Reads in struct literal of type mutate.Deps`, `unknown field ReadCast in struct literal of type mutate.Deps`, `undefined: mutate.ReadCmd`, `rg.m.MarkRead undefined (type *mutate.Mutator has no field or method MarkRead)`, `rg.m.MarkUnread undefined (type *mutate.Mutator has no field or method MarkUnread)`.

**Step 3: Code**

`apps/core/internal/mutate/read.go`:

```go
package mutate

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var errNoSeq = fmt.Errorf("%w: seq", apperr.ErrInvalidArgument)

type ReadNotifier interface {
	Offer(u domain.ReadUpdate)
}

type ReadCmd struct {
	Tenant, User string
	Room, Seq    uint64
}

func (m *Mutator) MarkRead(ctx context.Context, c ReadCmd) (domain.ReadPos, error) {
	grant, seq, err := m.readTarget(ctx, c)
	if err != nil || seq == 0 {
		return grant.Member.Read, err
	}
	pos, changed, err := m.d.Reads.MarkRead(ctx, c.Room, c.User, seq)
	return m.announceRead(grant, pos, changed, err)
}

func (m *Mutator) MarkUnread(ctx context.Context, c ReadCmd) (domain.ReadPos, error) {
	if c.Seq == 0 {
		return domain.ReadPos{}, errNoSeq
	}
	grant, seq, err := m.readTarget(ctx, c)
	if err != nil || seq == 0 {
		return grant.Member.Read, err
	}
	pos, changed, err := m.d.Reads.MarkUnread(ctx, c.Room, c.User, seq-1)
	return m.announceRead(grant, pos, changed, err)
}

func (m *Mutator) readTarget(ctx context.Context, c ReadCmd) (access.Request, uint64, error) {
	grant, err := m.d.Access.Authorize(ctx, access.MarkRead, c.Tenant, c.User, c.Room)
	if err != nil {
		return access.Request{}, 0, err
	}
	if c.Seq != 0 && c.Seq <= grant.Room.LastSeq {
		return grant, c.Seq, nil
	}
	last, err := m.d.Messages.Last(ctx, c.Room, 0)
	if err != nil {
		return access.Request{}, 0, err
	}
	if c.Seq == 0 || c.Seq > last {
		return grant, last, nil
	}
	return grant, c.Seq, nil
}

func (m *Mutator) announceRead(grant access.Request, pos domain.ReadPos, changed bool, err error) (domain.ReadPos, error) {
	if err != nil {
		return domain.ReadPos{}, err
	}
	if changed {
		r := grant.Room
		m.d.ReadCast.Offer(domain.ReadUpdate{Room: r.ID, Tenant: r.Tenant, Type: r.Type, Members: r.MemberCount, User: grant.User, Pos: pos, At: m.now()})
	}
	return pos, nil
}
```

`apps/core/internal/mutate/mutator.go`:
- thay dòng `errMissingDeps` bằng:

```go
var errMissingDeps = fmt.Errorf("%w: mutator needs access, messages, edits, hidden, rooms, events, reactions, a counter, pins, a pin projector, member actions, a member reader, a member projector, a member forgetter, read positions and a read notifier", apperr.ErrInvalidArgument)
```

- trong `type Deps struct`, sau `Forget        MemberForgetter` thêm:

```go
	Reads         store.ReadPositions
	ReadCast      ReadNotifier
```

- trong `New`, dòng điều kiện cuối `d.MemberActions == nil || d.MemberReader == nil || d.MemberProj == nil || d.Forget == nil {` thay bằng `d.MemberActions == nil || d.MemberReader == nil || d.MemberProj == nil || d.Forget == nil || d.Reads == nil || d.ReadCast == nil {`.

Chạy `make -s go ARGS="fmt ./apps/core/internal/mutate/"`.

**Step 4: Caller ngoài package**

`apps/core/internal/grpcsrv/fake_dependencies_test.go`:
- import thêm `".../internal/readcast"`;
- trong `newMutator`, ngay sau khối `if o.events != nil { events = o.events }` thêm:

```go
	reads, err := readcast.New(events, readcast.Config{})
	if err != nil {
		t.Fatalf("readcast.New: %v", err)
	}
	t.Cleanup(func() { _ = reads.Close(context.Background()) })
```

- trong literal `mutate.Deps{...}`, cuối thêm dòng `Reads: rg.rooms, ReadCast: reads,`.

`apps/core/service_wiring.go`:
- import thêm `".../internal/readcast"`;
- chữ ký thành `func wireService(st *mongostore.Store, router *actor.Router, pub *publish.Publisher, reads *readcast.Caster, lockedKinds []domain.Kind, limits mutate.Limits, log *slog.Logger) (*grpcsrv.Service, error) {`;
- trong literal `mutate.Deps{...}` thêm dòng `Reads: st, ReadCast: reads,`.

`apps/core/wiring.go`:
- import thêm `".../internal/readcast"`;
- thay dòng `svc, err := wireService(st, router, pub, cfg.LockedMessageKinds, cfg.Limits, log)` bằng:

```go
	reads, err := readcast.New(pub, readcast.Config{})
	if err != nil {
		return nil, fmt.Errorf("wire read receipts: %w", err)
	}
	svc, err := wireService(st, router, pub, reads, cfg.LockedMessageKinds, cfg.Limits, log)
```

**Step 5: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/internal/mutate/... ./apps/core/internal/grpcsrv/... ./apps/core/"
make vet
```

Expected: PASS. `wc -l apps/core/internal/mutate/read.go apps/core/internal/mutate/read_test.go apps/core/internal/mutate/read_helpers_test.go apps/core/internal/mutate/fixtures_test.go apps/core/internal/mutate/mutator.go apps/core/wiring.go`: ~80, ~165, ~90, ~174, ~124, ~154.

**Step 6: Itest**

```bash
make infra-up && make itest
```

Expected: mọi package `ok` (`Reads: st` là adapter Mongo của Task 5; chưa có RPC đọc).

**Step 7: INDEXES + commit**

```bash
python3 <scratchpad>/indexes_edit.py <<'EOF'
[
 {"path": "apps/core/internal/mutate", "col": "purpose", "append": "; MarkRead raises and MarkUnread lowers the read position (D104): Authorize mark_read (active members only), clamp the seq to the real last seq (rooms.ls when 0 < seq <= ls, else Messages.Last; seq 0 = latest; MarkUnread(0) is InvalidArgument), MarkUnread(seq) moves to seq-1 and only lowers, an empty room or no change returns the stored position; a change bumps rs.v and is offered to the ReadNotifier (readcast, D105)"},
 {"path": "apps/core/internal/mutate", "col": "key_symbols", "append": ";ReadNotifier;ReadCmd;Mutator.MarkRead;Mutator.MarkUnread"},
 {"path": "apps/core/internal/mutate", "col": "decisions", "append": ";D104;D105"},
 {"path": "apps/core", "col": "purpose", "append": "; wire builds readcast.New(pub, ...) and wireService passes it with the store's read positions to the mutator"},
 {"path": "apps/core", "col": "decisions", "append": ";D104;D105"},
 {"path": "apps/core/internal/readcast", "col": "used_by", "append": "apps/core"}
]
EOF
make -s go ARGS="fmt ./apps/core/..."
make fmt-check && make vet && make lint
git add apps/core/internal/mutate/read.go apps/core/internal/mutate/read_test.go apps/core/internal/mutate/read_helpers_test.go
git commit -m "feat(mutate): mark read and unread with a versioned read position" -- apps/core/internal/mutate/ apps/core/internal/grpcsrv/fake_dependencies_test.go apps/core/service_wiring.go apps/core/wiring.go INDEXES.csv
```

Expected: `{7}`; `git show --stat HEAD` có 10 file.

Task controller kiểm nhanh (không reviewer): luật chỉ nâng/chỉ hạ, kẹp theo seq thật, `Offer` chỉ khi đổi.

---

### Task 14: ★ Config (3 env) + `grpcsrv/members.go`, `read.go` + vòng đời `readcast` (bước dừng, `StopPlan`) + metric + README. **Push**

Ba biến môi trường mới (hợp đồng, mục config):

| Env | Field | Mặc định | Kiểm |
|---|---|---|---|
| `MEMBER_BATCH_MAX` | `Config.Limits.MemberBatch` | 500 | `p.count` (> 0) + `mutate.Limits.Validate` (2..1000; 1 sẽ chặn mọi DM) |
| `READ_RECEIPT_WINDOW` | `Config.ReadCast.Window` | 3s | `p.span` (> 0) + `readcast.Config.Validate` (100ms..1m) |
| `READ_RECEIPT_MAX_MEMBERS` | `Config.ReadCast.MaxMembers` | 100 | `p.count` (> 0) + `readcast.Config.Validate` (≤ 1000) |

`StopPlan` thêm `ReadEvents = CloseTimeout` (1s) ngay sau gRPC: kế hoạch dừng mặc định 26.2s → **27.2s** < 28s, không đổi `CORE_SHUTDOWN_BUDGET` hay `stop_grace_period` (33s). Bảng override của `env_test.go` có kế hoạch 19.6s → 20.6s, vượt `CORE_SHUTDOWN_BUDGET=20s` của chính bảng đó, nên task này nâng override lên `22s` (chỉ dữ liệu test).

Sáu RPC mỏng (`grpcsrv.Deps` không thêm field): `callerAndRoom` → `Mutator.*` → response. `ChangeMemberRole` đổi role qua `pbconv.DomainMemberRole` (`UNSPECIFIED` → `INVALID_ARGUMENT`) sau khi đã kiểm caller. `member_count` kẹp về int32 như `pbconv`. Mã lỗi đi qua `pkg/grpcserver` như cũ: `ErrDirectRoom`, `ErrLastOwner` → `FailedPrecondition`; `ErrMemberNotFound`, `ErrRoomNotFound` → `NotFound`; `ErrNotMember`, `access.ErrDenied` → `PermissionDenied`; `ErrTooManyMembers`, `RemoveMember` chính mình, `MarkUnread(0)` → `InvalidArgument`; `ErrRetryLater` → `Unavailable`.

Vòng đời: `app.reads` (`drainer`) = `readcast.Caster`; `serve` dùng `newSupervisor(ctx, 10)` và chạy task `"read events"` ngay sau `"publisher"`; `shutdown` thêm `s.step("read events", plan.ReadEvents, a.reads.Close, t.reads)` ngay sau bước `"grpc"` (không còn RPC nào gọi `Offer` sau đó; `Close` xả pending vào publisher, lúc đó publisher còn mở). Metric `read_events_unbatched_total` (chẩn đoán, không luật alert: vẫn 16 luật).

Ba commit: config; RPC; vòng đời + metric + README. Push cuối task.

**Files:**
- Modify: `apps/core/internal/config/config.go`, `components.go`, `validate.go`
- Modify: `apps/core/internal/config/env_test.go`, `load_test.go`, `validate_test.go`, `parse_test.go`
- Create: `apps/core/internal/grpcsrv/members.go`, `read.go`
- Create: `apps/core/internal/grpcsrv/members_test.go`, `read_test.go`
- Modify: `apps/core/internal/grpcsrv/caller_identity_test.go`
- Modify: `apps/core/wiring.go`, `lifecycle.go`, `shutdown.go`, `metrics_wiring.go`
- Modify: `apps/core/stop_order_test.go`, `startup_gate_test.go`, `metrics_wiring_test.go`
- Modify: `README.md` (bảng env)
- Modify: `INDEXES.csv`

**Step 1: Test config**

`apps/core/internal/config/env_test.go`:
- `envKeys`: sau dòng `"REACTION_EMOJIS", "PIN_LIMIT", "REACTION_COUNT_DELAY",` thêm dòng `"MEMBER_BATCH_MAX", "READ_RECEIPT_WINDOW", "READ_RECEIPT_MAX_MEMBERS",`.
- `overrides`: thay `"CORE_SHUTDOWN_BUDGET": "20s"` bằng `"CORE_SHUTDOWN_BUDGET": "22s"`; sau dòng `"REACTION_EMOJIS":      "🎉,👍", "PIN_LIMIT": "10", "REACTION_COUNT_DELAY": "2s",` thêm dòng `"MEMBER_BATCH_MAX": "200", "READ_RECEIPT_WINDOW": "2s", "READ_RECEIPT_MAX_MEMBERS": "50",`.

`apps/core/internal/config/load_test.go`: import thêm `".../internal/readcast"`;
- `TestLoadDefaults`: thay `Limits:             mutate.Limits{Emojis: mutate.DefaultEmojis, PinLimit: 50},` bằng `Limits:             mutate.Limits{Emojis: mutate.DefaultEmojis, PinLimit: 50, MemberBatch: 500},`; sau `ReactionCountDelay: time.Second,` thêm `ReadCast:           readcast.Config{Window: 3 * time.Second, MaxMembers: 100},`.
- `TestLoadOverrides`: thay `ShutdownBudget: 20 * time.Second,` bằng `ShutdownBudget: 22 * time.Second,`; thay `Limits:             mutate.Limits{Emojis: []string{"🎉", "👍"}, PinLimit: 10},` bằng `Limits:             mutate.Limits{Emojis: []string{"🎉", "👍"}, PinLimit: 10, MemberBatch: 200},`; sau `ReactionCountDelay: 2 * time.Second,` thêm `ReadCast:           readcast.Config{Window: 2 * time.Second, MaxMembers: 50},`.

`apps/core/internal/config/validate_test.go`, trong bảng `TestLoadValidation`:
- thay bốn dòng ngân sách dừng:

```go
		{"stop phases fill the budget", map[string]string{"CORE_SHUTDOWN_BUDGET": "27200ms"}, "CORE_SHUTDOWN_BUDGET"},
		{"stop phases just fit the budget", map[string]string{"CORE_SHUTDOWN_BUDGET": "27201ms"}, ""},
		{"cid batch drain follows the redis op timeout", map[string]string{"REDIS_OP_TIMEOUT": "300ms", "CORE_SHUTDOWN_BUDGET": "27600ms"}, "2 x REDIS_OP_TIMEOUT (cid batcher drain) + FLUSH_INSERT_TIMEOUT"},
		{"worker drain counts in the stop plan", map[string]string{"WORK_DRAIN": "1001ms", "CORE_SHUTDOWN_BUDGET": "27201ms"}, "WORK_DRAIN + 1s"},
		{"read events count in the stop plan", map[string]string{"CORE_SHUTDOWN_BUDGET": "26201ms"}, "CORE_GRPC_SHUTDOWN + 1s (read events) + RECONCILE_DRAIN"},
```

- trước dòng `{"stop phases overflow", ...` thêm:

```go
		{"member batch above the cap", map[string]string{"MEMBER_BATCH_MAX": "1001"}, "REACTION_EMOJIS, PIN_LIMIT, MEMBER_BATCH_MAX"},
		{"member batch at the cap", map[string]string{"MEMBER_BATCH_MAX": "1000"}, ""},
		{"read receipt window below the minimum", map[string]string{"READ_RECEIPT_WINDOW": "99ms"}, "READ_RECEIPT_*"},
		{"read receipt window at the minimum", map[string]string{"READ_RECEIPT_WINDOW": "100ms"}, ""},
		{"read receipt window above the maximum", map[string]string{"READ_RECEIPT_WINDOW": "61s"}, "READ_RECEIPT_*"},
		{"read receipt members above the cap", map[string]string{"READ_RECEIPT_MAX_MEMBERS": "1001"}, "READ_RECEIPT_*"},
		{"read receipt members at the cap", map[string]string{"READ_RECEIPT_MAX_MEMBERS": "1000"}, ""},
```

`apps/core/internal/config/parse_test.go`, trong bảng `TestLoadRejectsNonPositiveValues`, sau `{"REACTION_COUNT_DELAY", "0s"},` thêm:

```go
		{"MEMBER_BATCH_MAX", "0"},
		{"READ_RECEIPT_WINDOW", "0s"},
		{"READ_RECEIPT_MAX_MEMBERS", "0"},
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/config/..."`
Expected: FAIL biên dịch: `unknown field ReadCast in struct literal of type config.Config`.

**Step 3: Code config**

`apps/core/internal/config/config.go`: import thêm `"github.com/ivannguyendev/chatim/apps/core/internal/readcast"`;
- trong `type Config struct`, sau `ReactionCountDelay  time.Duration` thêm `ReadCast            readcast.Config`;
- trong `type StopPlan struct`, sau `GRPC       time.Duration` thêm `ReadEvents time.Duration`;
- trong literal `Limits: mutate.Limits{...}` của `Load`, sau `PinLimit: p.count("PIN_LIMIT", mutate.DefaultPinLimit),` thêm `MemberBatch: p.count("MEMBER_BATCH_MAX", mutate.DefaultMemberBatch),`;
- trong `StopPlan()`, sau `GRPC:       c.GRPCShutdown,` thêm `ReadEvents: CloseTimeout,`;
- trong `total()`, thay danh sách `[]time.Duration{s.DrainDelay, s.GRPC, s.Reconciler, ...}` bằng `[]time.Duration{s.DrainDelay, s.GRPC, s.ReadEvents, s.Reconciler, s.Workers, s.Router, s.CIDBatch, s.Flusher, s.Publisher, s.Slots, s.Close}`.

`apps/core/internal/config/components.go`: import thêm `".../internal/readcast"`; trong `components`, ngay trước dòng cuối `p.workerConfig(c)` thêm:

```go
	c.ReadCast = readcast.Config{
		Window:     p.span("READ_RECEIPT_WINDOW", readcast.DefaultWindow),
		MaxMembers: p.count("READ_RECEIPT_MAX_MEMBERS", readcast.DefaultMaxMembers),
	}
```

`apps/core/internal/config/validate.go`:
- thay hằng `stopPhases` bằng:

```go
const stopPhases = "CORE_DRAIN_DELAY + CORE_GRPC_SHUTDOWN + 1s (read events) + RECONCILE_DRAIN + 1s + WORK_DRAIN + 1s + CORE_REQUEST_DEADLINE (router drain) + " +
	"2 x REDIS_OP_TIMEOUT (cid batcher drain) + FLUSH_INSERT_TIMEOUT (flusher drain) + CORE_PUBLISHER_DRAIN + slot release + client close"
```

- trong `parts` của `componentErrors`, thay `{"REACTION_EMOJIS, PIN_LIMIT", c.Limits.Validate()},` bằng:

```go
		{"REACTION_EMOJIS, PIN_LIMIT, MEMBER_BATCH_MAX", c.Limits.Validate()},
		{"READ_RECEIPT_*", c.ReadCast.Validate()},
```

Chạy `make -s go ARGS="fmt ./apps/core/internal/config/"`.

**Step 4: Chạy, thấy pass + commit**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/config/..."`
Expected: PASS (`TestOverridesCoverEveryKey` thấy 3 khoá mới ở cả hai bảng). `wc -l apps/core/internal/config/*.go`: mỗi file < 200 (`load_test.go` ~184, `config.go` ~163, `validate_test.go` ~135).

```bash
python3 <scratchpad>/indexes_edit.py <<'EOF'
[
 {"path": "apps/core/internal/config", "col": "purpose", "append": "; MEMBER_BATCH_MAX -> Limits.MemberBatch (default 500, 2..1000 by mutate.Limits.Validate, D107); READ_RECEIPT_WINDOW/READ_RECEIPT_MAX_MEMBERS -> ReadCast (readcast.Config, defaults 3s and 100, checked by readcast.Config.Validate: 100ms..1m and 1..1000, D105); StopPlan.ReadEvents = 1s right after gRPC, default stop plan 27.2s of 28s"},
 {"path": "apps/core/internal/config", "col": "key_symbols", "append": ";Config.ReadCast;StopPlan.ReadEvents"},
 {"path": "apps/core/internal/config", "col": "decisions", "append": ";D105;D107"},
 {"path": "apps/core/internal/readcast", "col": "used_by", "append": ";apps/core/internal/config"}
]
EOF
make -s go ARGS="fmt ./apps/core/..."
make fmt-check && make vet && make lint
git commit -m "feat(config): member batch cap, read receipt window and room-wide limit" -- apps/core/internal/config/ INDEXES.csv
```

Expected: `{7}`; `make vet` sạch (`apps/core` vẫn biên dịch: `StopPlan` chỉ thêm field); `git show --stat HEAD` có 8 file.

**Step 5: Test `grpcsrv`**

`apps/core/internal/grpcsrv/caller_identity_test.go`, trong map `rpcs` của `TestEveryRPCChecksCallerIdentityFirst`, sau `"UnpinMessage"` thêm:

```go
		"AddMembers": func(ctx context.Context) error {
			_, err := rg.client.AddMembers(ctx, &chatimv1.AddMembersRequest{RoomId: "42", Users: []string{"bob"}})
			return err
		},
		"RemoveMember": func(ctx context.Context) error {
			_, err := rg.client.RemoveMember(ctx, &chatimv1.RemoveMemberRequest{RoomId: "42", User: "bob"})
			return err
		},
		"LeaveRoom": func(ctx context.Context) error {
			_, err := rg.client.LeaveRoom(ctx, &chatimv1.LeaveRoomRequest{RoomId: "42"})
			return err
		},
		"ChangeMemberRole": func(ctx context.Context) error {
			_, err := rg.client.ChangeMemberRole(ctx, &chatimv1.ChangeMemberRoleRequest{RoomId: "42", User: "bob"})
			return err
		},
		"MarkRead": func(ctx context.Context) error {
			_, err := rg.client.MarkRead(ctx, &chatimv1.MarkReadRequest{RoomId: "42", Seq: 1})
			return err
		},
		"MarkUnread": func(ctx context.Context) error {
			_, err := rg.client.MarkUnread(ctx, &chatimv1.MarkUnreadRequest{RoomId: "42", Seq: 1})
			return err
		},
```

(`ChangeMemberRole` gửi role `UNSPECIFIED` có chủ ý: caller phải bị kiểm trước role.)

`apps/core/internal/grpcsrv/members_test.go`:

```go
package grpcsrv_test

import (
	"context"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestMemberChangesThroughTheService(t *testing.T) {
	events := &recordingEvents{}
	rg := newRig(t, options{events: events})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, carol := as(t, "acme", "alice"), as(t, "acme", "carol")
	added, err := rg.client.AddMembers(alice, &chatimv1.AddMembersRequest{RoomId: room, Users: []string{"carol", "dave", "bob"}})
	if err != nil || added.GetMemberVersion() != 2 || added.GetMemberCount() != 4 || !slices.Equal(added.GetAdded(), []string{"carol", "dave"}) {
		t.Fatalf("AddMembers = %v, %v; want carol and dave added at version 2", added, err)
	}
	promoted, err := rg.client.ChangeMemberRole(alice, &chatimv1.ChangeMemberRoleRequest{RoomId: room, User: "carol", Role: chatimv1.MemberRole_MEMBER_ROLE_ADMIN})
	if err != nil || !promoted.GetChanged() || promoted.GetMemberVersion() != 3 || promoted.GetMemberCount() != 4 ||
		promoted.GetPreviousRole() != chatimv1.MemberRole_MEMBER_ROLE_MEMBER {
		t.Fatalf("ChangeMemberRole = %v, %v; want carol promoted from member at version 3", promoted, err)
	}
	for i, changed := range []bool{true, false} {
		removed, err := rg.client.RemoveMember(carol, &chatimv1.RemoveMemberRequest{RoomId: room, User: "dave"})
		if err != nil || removed.GetChanged() != changed || removed.GetMemberVersion() != 4 || removed.GetMemberCount() != 3 {
			t.Fatalf("RemoveMember #%d = %v, %v; want version 4, count 3, changed %v", i+1, removed, err, changed)
		}
	}
	left, err := rg.client.LeaveRoom(alice, &chatimv1.LeaveRoomRequest{RoomId: room})
	if err != nil || !left.GetChanged() || left.GetMemberVersion() != 5 || left.GetMemberCount() != 2 || left.GetNewOwner() != "carol" {
		t.Fatalf("LeaveRoom = %v, %v; want carol (the only admin) as the new owner at version 5", left, err)
	}
	stranger, err := rg.client.LeaveRoom(as(t, "acme", "mallory"), &chatimv1.LeaveRoomRequest{RoomId: room})
	if err != nil || stranger.GetChanged() || stranger.GetMemberVersion() != 5 {
		t.Fatalf("a stranger leaves = %v, %v; want an unchanged success", stranger, err)
	}
	id := roomNumber(t, room)
	_, got := events.enqueued()
	ids := make([]string, 0, len(got))
	for _, ev := range got {
		ids = append(ids, ev.GetId())
	}
	want := slices.Concat(createdIDs(id, "alice", "bob"), []string{
		pbconv.MemberEventID(id, 2), pbconv.MemberUserEventID(id, 2, "carol"), pbconv.MemberUserEventID(id, 2, "dave"),
		pbconv.MemberEventID(id, 3), pbconv.MemberUserEventID(id, 3, "carol"),
		pbconv.MemberEventID(id, 4), pbconv.MemberUserEventID(id, 4, "dave"),
		pbconv.MemberEventID(id, 5), pbconv.MemberUserEventID(id, 5, "alice"), pbconv.MemberUserEventID(id, 5, "carol"),
	})
	if !slices.Equal(ids, want) {
		t.Fatalf("enqueued %v, want %v", ids, want)
	}
}

func TestARemovedMemberCanNeitherSendNorReadUntilAddedBack(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	rg.send(t, bob, room, "b-1", "hi")
	if _, err := rg.client.RemoveMember(alice, &chatimv1.RemoveMemberRequest{RoomId: room, User: "bob"}); err != nil {
		t.Fatalf("RemoveMember: %v", err)
	}
	oldest := chatimv1.HistoryAnchor_HISTORY_ANCHOR_OLDEST
	calls := map[string]func() error{
		"send": func() error {
			_, err := rg.client.SendMessage(bob, &chatimv1.SendMessageRequest{RoomId: room, Cid: "b-2", Text: "still here?"})
			return err
		},
		"history": func() error {
			_, err := rg.client.GetHistory(bob, &chatimv1.GetHistoryRequest{RoomId: room, Anchor: oldest})
			return err
		},
		"read": func() error {
			_, err := rg.client.MarkRead(bob, &chatimv1.MarkReadRequest{RoomId: room})
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) { expectCode(t, call(), codes.PermissionDenied) })
	}
	if _, err := rg.client.AddMembers(alice, &chatimv1.AddMembersRequest{RoomId: room, Users: []string{"bob"}}); err != nil {
		t.Fatalf("AddMembers: %v", err)
	}
	page, err := rg.client.GetHistory(bob, &chatimv1.GetHistoryRequest{RoomId: room, Anchor: oldest})
	if err != nil || len(page.GetMessages()) != 1 {
		t.Fatalf("history after re-add = %v, %v; want the one earlier message", page, err)
	}
	rg.send(t, bob, room, "b-3", "back")
}

func TestMemberErrorsKeepTheirCodes(t *testing.T) {
	rg := newRig(t, options{limits: mutate.Limits{MemberBatch: 2}})
	room := rg.createGroup(t, "acme", "alice", "bob")
	dm, err := rg.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{Type: chatimv1.RoomType_ROOM_TYPE_DM, Members: []string{"alice", "bob"}})
	if err != nil {
		t.Fatalf("CreateRoom dm: %v", err)
	}
	alice, bob, mallory := as(t, "acme", "alice"), as(t, "acme", "bob"), as(t, "acme", "mallory")
	add := func(ctx context.Context, roomID string, users ...string) func() error {
		return func() error {
			_, err := rg.client.AddMembers(ctx, &chatimv1.AddMembersRequest{RoomId: roomID, Users: users})
			return err
		}
	}
	role := func(ctx context.Context, user string, r chatimv1.MemberRole) func() error {
		return func() error {
			_, err := rg.client.ChangeMemberRole(ctx, &chatimv1.ChangeMemberRoleRequest{RoomId: room, User: user, Role: r})
			return err
		}
	}
	leave := func(ctx context.Context, roomID string) func() error {
		return func() error {
			_, err := rg.client.LeaveRoom(ctx, &chatimv1.LeaveRoomRequest{RoomId: roomID})
			return err
		}
	}
	removeSelf := func() error {
		_, err := rg.client.RemoveMember(alice, &chatimv1.RemoveMemberRequest{RoomId: room, User: "alice"})
		return err
	}
	cases := []struct {
		name string
		call func() error
		code codes.Code
	}{
		{"add to a dm", add(alice, dm.GetRoom().GetId(), "carol"), codes.FailedPrecondition},
		{"leave a dm", leave(alice, dm.GetRoom().GetId()), codes.FailedPrecondition},
		{"last owner steps down", role(alice, "alice", chatimv1.MemberRole_MEMBER_ROLE_MEMBER), codes.FailedPrecondition},
		{"remove yourself", removeSelf, codes.InvalidArgument},
		{"unspecified role", role(alice, "bob", chatimv1.MemberRole_MEMBER_ROLE_UNSPECIFIED), codes.InvalidArgument},
		{"too many users", add(alice, room, "c1", "c2", "c3"), codes.InvalidArgument},
		{"no users", add(alice, room), codes.InvalidArgument},
		{"bad room id", leave(alice, "x"), codes.InvalidArgument},
		{"role of a stranger", role(alice, "zed", chatimv1.MemberRole_MEMBER_ROLE_ADMIN), codes.NotFound},
		{"unknown room", leave(alice, "999"), codes.NotFound},
		{"other tenant", add(as(t, "other", "alice"), room, "carol"), codes.NotFound},
		{"member adds", add(bob, room, "carol"), codes.PermissionDenied},
		{"member changes a role", role(bob, "alice", chatimv1.MemberRole_MEMBER_ROLE_MEMBER), codes.PermissionDenied},
		{"stranger adds", add(mallory, room, "carol"), codes.PermissionDenied},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { expectCode(t, c.call(), c.code) })
	}
}
```

`apps/core/internal/grpcsrv/read_test.go`:

```go
package grpcsrv_test

import (
	"strconv"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestReadPositionThroughTheService(t *testing.T) {
	events := &recordingEvents{}
	rg := newRig(t, options{events: events})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	for i := range 3 {
		rg.send(t, alice, room, "c-"+strconv.Itoa(i), "hi")
	}
	id := roomNumber(t, room)
	docs, err := rg.rooms.MembersOf(t.Context(), id, []string{"bob"})
	if err != nil || len(docs) != 1 {
		t.Fatalf("MembersOf(bob) = %+v, %v", docs, err)
	}
	v := docs[0].Read.Version
	steps := []struct {
		name         string
		unread       bool
		seq, wantSeq uint64
		wantVersion  uint64
	}{
		{"read seq 2", false, 2, 2, v + 1},
		{"read the latest", false, 0, 3, v + 2},
		{"read an older seq", false, 1, 3, v + 2},
		{"unread from seq 2", true, 2, 1, v + 3},
	}
	for _, s := range steps {
		var gotSeq, gotVersion uint64
		if s.unread {
			resp, err := rg.client.MarkUnread(bob, &chatimv1.MarkUnreadRequest{RoomId: room, Seq: s.seq})
			if err != nil {
				t.Fatalf("%s: %v", s.name, err)
			}
			gotSeq, gotVersion = resp.GetReadSeq(), resp.GetReadVersion()
		} else {
			resp, err := rg.client.MarkRead(bob, &chatimv1.MarkReadRequest{RoomId: room, Seq: s.seq})
			if err != nil {
				t.Fatalf("%s: %v", s.name, err)
			}
			gotSeq, gotVersion = resp.GetReadSeq(), resp.GetReadVersion()
		}
		if gotSeq != s.wantSeq || gotVersion != s.wantVersion {
			t.Fatalf("%s = seq %d version %d, want seq %d version %d", s.name, gotSeq, gotVersion, s.wantSeq, s.wantVersion)
		}
	}
	_, err = rg.client.MarkUnread(bob, &chatimv1.MarkUnreadRequest{RoomId: room})
	expectCode(t, err, codes.InvalidArgument)
	_, err = rg.client.MarkRead(as(t, "acme", "mallory"), &chatimv1.MarkReadRequest{RoomId: room})
	expectCode(t, err, codes.PermissionDenied)
	_, err = rg.client.MarkRead(as(t, "other", "bob"), &chatimv1.MarkReadRequest{RoomId: room})
	expectCode(t, err, codes.NotFound)
	_, got := events.enqueued()
	var first *chatimv1.Event
	for _, ev := range got {
		if ev.GetReadUpdated() != nil {
			first = ev
			break
		}
	}
	if first.GetId() != pbconv.ReadEventID(id, "bob", v+1) || first.GetRecipient() != "" || first.GetReadUpdated().GetSeq() != 2 || first.GetActor() != "bob" {
		t.Fatalf("first read_updated = %v, want bob's seq 2 at version %d for the whole room", first, v+1)
	}
}
```

**Step 6: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/grpcsrv/..."`
Expected: FAIL (biên dịch được vì Task 3 đã sinh client/server cho 6 RPC; `Service` nhúng `UnimplementedCoreServiceServer`):
- `TestEveryRPCChecksCallerIdentityFirst/AddMembers/no_metadata` (và mọi case của 6 RPC mới): `status = (Unimplemented, "method AddMembers not implemented"), want (Unauthenticated, "unauthenticated")`;
- `TestMemberChangesThroughTheService`: `AddMembers = <nil>, rpc error: code = Unimplemented desc = method AddMembers not implemented; want carol and dave added at version 2`;
- `TestARemovedMemberCanNeitherSendNorReadUntilAddedBack`: `RemoveMember: rpc error: code = Unimplemented desc = method RemoveMember not implemented`;
- `TestMemberErrorsKeepTheirCodes/*`: `status = (Unimplemented, "method … not implemented"), want (…)`;
- `TestReadPositionThroughTheService`: `read seq 2: rpc error: code = Unimplemented desc = method MarkRead not implemented`.

**Step 7: Code `grpcsrv`**

`apps/core/internal/grpcsrv/members.go`:

```go
package grpcsrv

import (
	"context"
	"math"

	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) AddMembers(ctx context.Context, req *chatimv1.AddMembersRequest) (*chatimv1.AddMembersResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	res, err := s.mutator.AddMembers(ctx, mutate.AddMembersCmd{Tenant: who.tenant, User: who.user, Room: room, Users: req.GetUsers()})
	if err != nil {
		return nil, err
	}
	return &chatimv1.AddMembersResponse{MemberVersion: res.Version, MemberCount: memberCount(res.Count), Added: res.Added}, nil
}

func (s *Service) RemoveMember(ctx context.Context, req *chatimv1.RemoveMemberRequest) (*chatimv1.RemoveMemberResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	res, err := s.mutator.RemoveMember(ctx, mutate.RemoveMemberCmd{Tenant: who.tenant, User: who.user, Room: room, Target: req.GetUser()})
	if err != nil {
		return nil, err
	}
	return &chatimv1.RemoveMemberResponse{MemberVersion: res.Version, MemberCount: memberCount(res.Count), Changed: res.Changed}, nil
}

func (s *Service) LeaveRoom(ctx context.Context, req *chatimv1.LeaveRoomRequest) (*chatimv1.LeaveRoomResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	res, err := s.mutator.LeaveRoom(ctx, mutate.LeaveRoomCmd{Tenant: who.tenant, User: who.user, Room: room})
	if err != nil {
		return nil, err
	}
	return &chatimv1.LeaveRoomResponse{
		MemberVersion: res.Version, MemberCount: memberCount(res.Count), Changed: res.Changed, NewOwner: res.Successor,
	}, nil
}

func (s *Service) ChangeMemberRole(ctx context.Context, req *chatimv1.ChangeMemberRoleRequest) (*chatimv1.ChangeMemberRoleResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	role, err := pbconv.DomainMemberRole(req.GetRole())
	if err != nil {
		return nil, err
	}
	res, err := s.mutator.ChangeMemberRole(ctx, mutate.ChangeRoleCmd{Tenant: who.tenant, User: who.user, Room: room, Target: req.GetUser(), Role: role})
	if err != nil {
		return nil, err
	}
	return &chatimv1.ChangeMemberRoleResponse{
		MemberVersion: res.Version, MemberCount: memberCount(res.Count), Changed: res.Changed, PreviousRole: pbconv.MemberRole(res.Prev),
	}, nil
}

func memberCount(n int) int32 {
	switch {
	case n < 0:
		return 0
	case n > math.MaxInt32:
		return math.MaxInt32
	default:
		return int32(n)
	}
}
```

`apps/core/internal/grpcsrv/read.go`:

```go
package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) MarkRead(ctx context.Context, req *chatimv1.MarkReadRequest) (*chatimv1.MarkReadResponse, error) {
	cmd, err := readCmdOf(ctx, req.GetRoomId(), req.GetSeq())
	if err != nil {
		return nil, err
	}
	pos, err := s.mutator.MarkRead(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return &chatimv1.MarkReadResponse{ReadSeq: pos.Seq, ReadVersion: pos.Version}, nil
}

func (s *Service) MarkUnread(ctx context.Context, req *chatimv1.MarkUnreadRequest) (*chatimv1.MarkUnreadResponse, error) {
	cmd, err := readCmdOf(ctx, req.GetRoomId(), req.GetSeq())
	if err != nil {
		return nil, err
	}
	pos, err := s.mutator.MarkUnread(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return &chatimv1.MarkUnreadResponse{ReadSeq: pos.Seq, ReadVersion: pos.Version}, nil
}

func readCmdOf(ctx context.Context, roomID string, seq uint64) (mutate.ReadCmd, error) {
	who, room, err := callerAndRoom(ctx, roomID)
	if err != nil {
		return mutate.ReadCmd{}, err
	}
	return mutate.ReadCmd{Tenant: who.tenant, User: who.user, Room: room, Seq: seq}, nil
}
```

**Step 8: Chạy, thấy pass + commit**

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/internal/grpcsrv/..."
make vet
```

Expected: PASS. `wc -l apps/core/internal/grpcsrv/*.go`: mỗi file < 200 (`members.go` ~85, `read.go` ~45, `members_test.go` ~175, `read_test.go` ~90, `caller_identity_test.go` ~136, `harness_test.go` 185 không đổi).

```bash
python3 <scratchpad>/indexes_edit.py <<'EOF'
[
 {"path": "apps/core/internal/grpcsrv", "col": "purpose", "append": "; AddMembers/RemoveMember/LeaveRoom/ChangeMemberRole (members.go: role through pbconv.DomainMemberRole after the caller check, member_count clamped to int32) and MarkRead/MarkUnread (read.go) are thin calls into mutate"},
 {"path": "apps/core/internal/grpcsrv", "col": "key_symbols", "append": ";Service.AddMembers;Service.RemoveMember;Service.LeaveRoom;Service.ChangeMemberRole;Service.MarkRead;Service.MarkUnread"},
 {"path": "apps/core/internal/grpcsrv", "col": "decisions", "append": ";D99;D100;D104"}
]
EOF
make -s go ARGS="fmt ./apps/core/..."
make fmt-check && make vet && make lint
git add apps/core/internal/grpcsrv/members.go apps/core/internal/grpcsrv/read.go apps/core/internal/grpcsrv/members_test.go apps/core/internal/grpcsrv/read_test.go
git commit -m "feat(grpcsrv): member and read position RPCs" -- apps/core/internal/grpcsrv/ INDEXES.csv
```

Expected: `{7}`; `git show --stat HEAD` có 6 file.

**Step 9: Test vòng đời `apps/core`**

`apps/core/stop_order_test.go`:
- trong literal `app{...}`, thay dòng `slots:  idle{}, workers: order.drainer("workers"), reconciler: order.drainer("reconciler"),` bằng:

```go
		slots:  idle{}, workers: order.drainer("workers"), reconciler: order.drainer("reconciler"), reads: order.drainer("read events"),
```

- thay `want := []string{"reconciler", "workers", "router", "cid batcher", "flusher", "publisher"}` bằng `want := []string{"read events", "reconciler", "workers", "router", "cid batcher", "flusher", "publisher"}`.

`apps/core/startup_gate_test.go`, trong `startGate`, thay dòng `publisher: idle{}, flusher: idle{}, cidBatch: idle{}, router: router, slots: slots, workers: idle{},` bằng `publisher: idle{}, flusher: idle{}, cidBatch: idle{}, router: router, slots: slots, workers: idle{}, reads: idle{},`.

`apps/core/metrics_wiring_test.go`:
- trong `fakeProbes`, sau `oplogWindow:  func() float64 { return 0 },` thêm `readUnbatched: func() uint64 { return 4 },` (gofmt căn cột);
- trong `everyCore` của `TestCoreMetricSourcesCoverEveryGuarantee`, sau `"counter_repaired_total",` thêm `"read_events_unbatched_total",`;
- trong `want` của `TestEffectMetricsReadTheirEffectByLabel`, thêm cặp `"read_events_unbatched_total": 4,`.

**Step 10: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/"`
Expected: FAIL biên dịch: `unknown field reads in struct literal of type app`, `unknown field readUnbatched in struct literal of type probes`.

**Step 11: Code vòng đời**

`apps/core/wiring.go`:
- trong `type app struct`, sau `publisher  drainer` thêm `reads      drainer`;
- thay `reads, err := readcast.New(pub, readcast.Config{})` (Task 13) bằng `reads, err := readcast.New(pub, cfg.ReadCast)`;
- ngay sau khối `if err != nil { return nil, fmt.Errorf("wire read receipts: %w", err) }` đó thêm `a.reads = reads`;
- trong literal `p := probes{...}`, sau `oplogWindow:    oplogWindowSeconds(cl.mongo),` thêm `readUnbatched:  reads.Unbatched,` (gofmt căn cột).

`apps/core/metrics_wiring.go`:
- trong `type probes struct`, sau `oplogWindow    func() float64` thêm `readUnbatched  func() uint64`;
- trong `metricSources`, sau dòng `{Name: "mongo_oplog_window_seconds", ...},` thêm:

```go
		{Name: "read_events_unbatched_total", Help: "Read receipts sent without coalescing because the coalescer was full or closed.", Read: func() float64 { return float64(p.readUnbatched()) }},
```

`apps/core/lifecycle.go`:
- `type tasks struct` thành `admin, publisher, reads, flusher, cidBatch, router, slots, workers, reconciler, grpc *task`;
- trong `serve`: `sup := newSupervisor(ctx, 10)`; trong literal `tasks{...}`, sau `publisher: sup.start("publisher", a.publisher.Run),` thêm `reads:     sup.start("read events", a.reads.Run),`.

`apps/core/shutdown.go`: ngay sau `s.step("grpc", 0, nil, t.grpc)` thêm `s.step("read events", plan.ReadEvents, a.reads.Close, t.reads)`.

Chạy `make -s go ARGS="fmt ./apps/core/"`.

**Step 12: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/ ./apps/core/internal/..."
make vet
```

Expected: PASS (`stop_order_test` thấy `read events` đầu danh sách; itest bỏ qua khi thiếu `CHATIM_IT_*`). `wc -l apps/core/wiring.go apps/core/lifecycle.go apps/core/shutdown.go apps/core/metrics_wiring.go apps/core/startup_gate_test.go apps/core/stop_order_test.go`: ~156, ~89, ~82, ~125, 174, ~87.

**Step 13: README**

`README.md`, bảng env:
- dòng `CORE_SHUTDOWN_BUDGET`: thay `Mặc định các mốc là 26.2s (gồm `RECONCILE_DRAIN + 1s` của reader,` bằng `Mặc định các mốc là 27.2s (gồm 1s để `readcast` xả các `read_updated` đang gộp, `RECONCILE_DRAIN + 1s` của reader,`.
- ngay sau dòng `REACTION_COUNT_DELAY` thêm:

```markdown
| `MEMBER_BATCH_MAX` | `500` | Số user tối đa trong một lệnh `CreateRoom` (đếm cả user lặp) hoặc `AddMembers` (sau khi khử trùng), 2–1000 (1 sẽ chặn mọi DM); vượt → `INVALID_ARGUMENT`. Core không giới hạn tổng số member của group; thay đổi member trong một room đi tuần tự (D107) |
| `READ_RECEIPT_WINDOW` | `3s` | Cửa sổ gộp event `read_updated` mỗi (room, user): lần đầu gửi ngay, các lần sau trong cửa sổ gộp thành một bản có version mới nhất, gửi trễ tối đa 1,5 lần cửa sổ (100ms–1m, D105) |
| `READ_RECEIPT_MAX_MEMBERS` | `100` | DM và group có số member không quá giá trị này nhận `read_updated` trên subject room; group lớn hơn chỉ nhận trên subject của chính user đọc (1–1000, D105) |
```

**Step 14: Itest**

```bash
make infra-up && make itest
```

Expected: mọi package `ok` (core thật chạy task `read events`, dừng đúng bước mới; `shutdown_integration_test` vẫn dừng trong `itStopLimit`).

**Step 15: INDEXES + commit**

```bash
python3 <scratchpad>/indexes_edit.py <<'EOF'
[
 {"path": "apps/core", "col": "purpose", "append": "; readcast.Caster (cfg.ReadCast) runs as the 'read events' task started right after the publisher and stops right after gRPC within StopPlan.ReadEvents (1s), flushing pending read receipts into the still open publisher; /metrics read_events_unbatched_total"},
 {"path": "README.md", "col": "purpose", "replace": ["(includes REACTION_EMOJIS, PIN_LIMIT, REACTION_COUNT_DELAY)", "(includes REACTION_EMOJIS, PIN_LIMIT, REACTION_COUNT_DELAY, MEMBER_BATCH_MAX, READ_RECEIPT_WINDOW, READ_RECEIPT_MAX_MEMBERS; stop plan 27.2s)"]},
 {"path": "README.md", "col": "decisions", "append": ";D105;D107"}
]
EOF
make -s go ARGS="fmt ./apps/core/..."
make fmt-check && make vet && make lint
git commit -m "feat(core): run the read receipt coalescer and stop it right after gRPC" -- apps/core/wiring.go apps/core/lifecycle.go apps/core/shutdown.go apps/core/metrics_wiring.go apps/core/stop_order_test.go apps/core/startup_gate_test.go apps/core/metrics_wiring_test.go README.md INDEXES.csv
```

Expected: `{7}`; `git show --stat HEAD` có 9 file; `README.md` chỉ đổi 1 dòng và thêm 3 dòng (nếu `README.md` có thay đổi chưa commit của người khác: dừng, báo controller).

**Step 16: Push**

```bash
git log --oneline origin/feat/m2b..HEAD
git push origin feat/m2b
```

Expected: log liệt kê các commit Task 8–14; push thành công.

Task rủi ro: một reviewer (luật kiểm config và ngân sách dừng 27.2s, thứ tự dừng `grpc → read events → reconciler`, 6 RPC kiểm caller trước mọi thứ, mã lỗi, `read_events_unbatched_total`; tối đa `-count=3` trên `config`, `grpcsrv`, `apps/core`).

### Task 15: ★ Effect `member_projection`, `member_event` + registry + metrics + wiring router

Lưới an toàn của fast path Task 10/11, chạy trên record `MemberInserted` (Task 6), theo thứ tự registry với delay tăng dần:

```
MemberInserted → room_activity (0) → member_projection (0) → member_event (RECONCILE_DELAY)
```

1. `member_projection` (D97, D103, delay 0): gom record của lô theo room (`groupRecords`, `recordRoom`), mỗi room một `memberproj.Projector.Project(room, max mv của room trong lô)` (`Seq` của record = mv). Thành công → `Router.ForgetMembers(room)` (actor trên core này quên cache member; core khác dựa vào TTL 10s, D103). Chỉ room không còn (`errors.Is(err, domain.ErrRoomNotFound)`) → drop, đếm theo số record; lỗi khác (kể cả `ErrInvalidArgument` khi fact không giải mã được, `ErrStaleRead`, `ErrContended`) → lỗi cho mọi record của room, retry và hiện ở `work_failures_total` (giống `pin_projection`, M2b.3; controller sửa khi ghép).
2. `member_event` (D102, D106, delay `RECONCILE_DELAY`, không ack mark): mỗi record `MemberActions.At(room, mv)` → loại room (cache) → `pbconv.MemberEvents(type, fact)` (bản room `{room}-m{mv}` rồi một bản user `{room}-m{mv}-{u}` cho mỗi user trong fact và owner kế nhiệm) → publish **mọi** bản; record chỉ nil khi mọi bản có PubAck (một bản lỗi → Nak cả record, lần sau gửi lại cả fact, stream bỏ bản đã có theo id). Fact hoặc room không còn → bỏ, `Dropped`. `Republished` đếm từng bản có PubAck không phải bản trùng.

`eventPublisher` (M2b.3) có vòng `each` một event mỗi record. Task này thêm `eachMany` (builder trả nhiều event; mọi bản của record `i` ghi lỗi vào `errs[i]`, nên một bản lỗi là record lỗi) và viết `each` thành lớp bọc mỏng của `eachMany`: hành vi của `reaction_event`, `pin_event` giữ nguyên (test cũ là lưới an toàn).

**Metrics:** `effectSet.counters()` thêm `member_projection` (chỉ dropped) và `member_event` (republished + dropped). Không luật alert mới (D106): MB1 dùng `ChatimEffectDropping` (`sum by (effect)`), `ChatimWorkFailing`, `ChatimRepublishSurge`, cả ba đã theo nhãn chung → `make alerts-check` vẫn `16 rules`.

**Wiring:** file mới `apps/core/member_effects_wiring.go` (`effectSet.wireMemberEffects`) dựng `memberproj.New(st.MemberActions(), st)` riêng (không giữ trạng thái; bản thứ hai cạnh `service_wiring.go` là vô hại) và hai effect. `wireEffects` thêm tham số `forget effects.MemberForgetter` ngay trước `log`; `wire` truyền `router`.

Trước khi bắt đầu, kiểm tên của Part A/B (chỉ đọc):

```bash
grep -n "MemberInserted" apps/core/internal/store/feed.go apps/core/effects_wiring.go
grep -n "^func New\|^func (p \*Projector) \(Settle\|Project\)" apps/core/internal/memberproj/*.go
grep -n "func (r \*Router) ForgetMembers" apps/core/internal/actor/*.go
grep -n "func (s \*Rooms) MemberActions\|func (s \*Store) MemberActions" apps/core/internal/store/memstore/*.go apps/core/internal/store/mongostore/*.go
grep -n "^func MemberEvents\|^func MemberEventID\|^func MemberUserEventID" apps/core/internal/pbconv/*.go
grep -n "ErrMemberActionNotFound\|MaxMemberScan" apps/core/internal/store/*.go
git diff --quiet -- INDEXES.csv; echo "INDEXES dirty=$?"
```

Expected: `MemberInserted ChangeKind = 6` trong `store/feed.go` và đúng một dòng registry tạm `store.MemberInserted: {activity.Effect()}` trong `effects_wiring.go` (Task 6); `memberproj.New(facts Facts, rooms Rooms) (*Projector, error)`, `Settle`, `Project(ctx, room, target uint64) (domain.Room, error)`; `ForgetMembers(room uint64)`; hai accessor `MemberActions()`; `MemberEvents(roomType domain.RoomType, a domain.MemberAction) []*chatimv1.Event` và hai hàm id; `ErrMemberActionNotFound`, `MaxMemberScan`; `INDEXES dirty=0`. Tên khác thì chỉ đổi tên trong snippet dưới và ghi vào báo cáo; thiếu hẳn thì dừng và báo cáo. `dirty=1` → dừng, hỏi controller.

**Files:**
- Modify: `apps/core/internal/effects/ports.go`
- Modify (thay cả file): `apps/core/internal/effects/event_publisher.go`
- Create: `apps/core/internal/effects/member_projection.go`
- Create: `apps/core/internal/effects/member_event.go`
- Create: `apps/core/internal/effects/member_fixtures_test.go`
- Create: `apps/core/internal/effects/member_projection_test.go`
- Create: `apps/core/internal/effects/member_event_test.go`
- Create: `apps/core/internal/effects/member_constructors_test.go`
- Modify (thay cả file): `apps/core/effects_wiring.go`
- Create: `apps/core/member_effects_wiring.go`
- Modify (thay cả file): `apps/core/effects_wiring_test.go`
- Modify: `apps/core/wiring.go` (một dòng), `apps/core/metrics_wiring.go` (một hằng help), `apps/core/metrics_wiring_test.go` (thêm dòng)
- Modify: `INDEXES.csv`

`harness_test.go` (199 dòng), `effect_spies_test.go`, `reaction_fixtures_test.go` không đổi; fixture member nằm ở `member_fixtures_test.go`. `deploy/prometheus/alerts.yml` không đổi.

**Step 0: Helper sửa `INDEXES.csv` (không commit)**

Các task 15–19 sửa những dòng `INDEXES.csv` rất dài và có dấu phẩy. Sửa tay dễ làm lệch ngoặc kép, nên dùng một helper chỉ viết lại đúng dòng được chọn (dòng khác giữ nguyên từng byte). Đặt ở `bin/` (gitignored, không commit):

`bin/indexes_edit.py`:

```python
import csv
import io
import sys

COLUMNS = {"purpose": 2, "key_symbols": 3, "used_by": 4, "tests": 5, "decisions": 6}


def encode(fields):
    out = io.StringIO()
    csv.writer(out, lineterminator="").writerow(fields)
    return out.getvalue()


def main():
    row, col, mode, text = sys.argv[1:5]
    new = sys.argv[5] if len(sys.argv) > 5 else None
    with open("INDEXES.csv", encoding="utf-8") as f:
        lines = f.read().split("\n")
    hits = [i for i, line in enumerate(lines) if line.startswith(row + ",")]
    if len(hits) != 1:
        sys.exit(f"{row}: {len(hits)} rows")
    if mode == "after":
        fields = next(csv.reader([text]))
        if len(fields) != 7:
            sys.exit(f"new row: {len(fields)} fields")
        lines.insert(hits[0] + 1, encode(fields))
    else:
        fields = next(csv.reader([lines[hits[0]]]))
        if len(fields) != 7:
            sys.exit(f"{row}: {len(fields)} fields")
        k = COLUMNS[col]
        if mode == "+":
            fields[k] += text
        elif mode == "=":
            fields[k] = text
        elif mode == "~":
            n = fields[k].count(text)
            if n != 1:
                sys.exit(f"{row} {col}: {n} matches of {text!r}")
            fields[k] = fields[k].replace(text, new)
        else:
            sys.exit(f"unknown mode {mode}")
        lines[hits[0]] = encode(fields)
    with open("INDEXES.csv", "w", encoding="utf-8") as f:
        f.write("\n".join(lines))
    print(f"{row} {col} {mode} ok")


main()
```

Cách dùng (chạy ở gốc repo): `python3 bin/indexes_edit.py <path> <cột> + "<nối thêm>"`, `… <cột> = "<giá trị mới>"`, `… <cột> ~ "<đoạn cũ>" "<đoạn mới>"` (đoạn cũ phải xuất hiện đúng một lần), `python3 bin/indexes_edit.py <path> - after '<dòng csv mới>'` (chèn dòng mới ngay sau dòng `<path>`). Helper thoát với lỗi khi không thấy đúng một dòng hay đúng một chỗ khớp: khi đó đọc dòng (`grep -n "^<path>," INDEXES.csv`) và sửa lệnh, không sửa tay. Sau mỗi lần dùng: `python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"` → `{7}`.

**Step 1: Port + fixture test**

`apps/core/internal/effects/ports.go`, thêm cuối file:

```go
type MemberFacts interface {
	At(ctx context.Context, room, mv uint64) (domain.MemberAction, error)
}

type MemberProjecter interface {
	Project(ctx context.Context, room, target uint64) (domain.Room, error)
}

type MemberForgetter interface {
	ForgetMembers(room uint64)
}
```

`apps/core/internal/effects/member_fixtures_test.go`:

```go
package effects_test

import (
	"context"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/memberproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

var joinedAt = time.Date(2026, 10, 6, 10, 0, 0, 0, time.UTC)

type spyMemberProjector struct {
	inner effects.MemberProjecter
	fail  map[uint64]error
	calls []projectCall
}

func (s *spyMemberProjector) Project(ctx context.Context, r, target uint64) (domain.Room, error) {
	s.calls = append(s.calls, projectCall{room: r, target: target})
	if err := s.fail[r]; err != nil {
		return domain.Room{}, err
	}
	return s.inner.Project(ctx, r, target)
}

type forgetSpy struct{ rooms []uint64 }

func (f *forgetSpy) ForgetMembers(r uint64) { f.rooms = append(f.rooms, r) }

type brokenMembers struct{}

func (brokenMembers) At(context.Context, uint64, uint64) (domain.MemberAction, error) {
	return domain.MemberAction{}, errBoom
}

type memberRig struct {
	rooms    *memstore.Rooms
	facts    *memstore.MemberActions
	js       *publishtest.JetStream
	projects *spyMemberProjector
	forgets  *forgetSpy
	proj     *effects.MemberProjection
	event    *effects.MemberEvent
}

func newMemberRig(t *testing.T) *memberRig {
	t.Helper()
	rooms := memstore.NewRooms()
	createRoom(t, rooms, room)
	rg := &memberRig{rooms: rooms, facts: rooms.MemberActions(), js: &publishtest.JetStream{}, forgets: &forgetSpy{}}
	proj, err := memberproj.New(rg.facts, rooms)
	if err != nil {
		t.Fatalf("memberproj.New: %v", err)
	}
	rg.projects = &spyMemberProjector{inner: proj}
	if rg.proj, err = effects.NewMemberProjection(rg.projects, rg.forgets); err != nil {
		t.Fatalf("NewMemberProjection: %v", err)
	}
	events := effects.MessageChangedConfig{SubjectRoot: "evt", Delay: delay, RoomCache: 16}
	if rg.event, err = effects.NewMemberEvent(effects.MemberEventDeps{Members: rg.facts, Rooms: rooms, JS: rg.js}, events); err != nil {
		t.Fatalf("NewMemberEvent: %v", err)
	}
	return rg
}

func (rg *memberRig) append(t *testing.T, a domain.MemberAction) domain.MemberAction {
	t.Helper()
	if err := rg.facts.Append(t.Context(), a); err != nil {
		t.Fatalf("append member fact %d/%d: %v", a.Room, a.MV, err)
	}
	return a
}

func addFact(r, mv uint64, count int, users ...string) domain.MemberAction {
	changes := make([]domain.MemberChange, len(users))
	for i, u := range users {
		changes[i] = domain.MemberChange{User: u, Role: domain.RoleMember}
	}
	return domain.MemberAction{Room: r, MV: mv, Tenant: tenant, Op: domain.MemberOpAdd, Changes: changes, By: "alice", At: joinedAt, Count: count}
}

func leaveFact(r, mv uint64, count int, user string, prev domain.Role, successor string) domain.MemberAction {
	return domain.MemberAction{
		Room: r, MV: mv, Tenant: tenant, Op: domain.MemberOpLeave, Changes: []domain.MemberChange{{User: user, Prev: prev}},
		By: user, At: joinedAt, Count: count, Successor: successor,
	}
}

func memberRec(r, mv uint64) work.Record {
	return work.Record{Kind: store.MemberInserted, Room: r, Seq: mv, CommittedAt: time.Now()}
}
```

`createRoom` (fixtures_test.go) tạo room `alice` owner qua `memstore.Rooms.Create`, nên từ Task 4 room đã có fact mv 1 và đầu `mv 1, mc 1`. `projectCall`, `errBoom`, `delay`, `tenant`, `room`, `otherRoom`, `allNil`, `storedEventIDs`, `brokenStore` đã có trong package test.

**Step 2: Test effect**

`apps/core/internal/effects/member_projection_test.go`:

```go
package effects_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestMemberProjectionDeclaresItsPolicy(t *testing.T) {
	e := newMemberRig(t).proj.Effect()
	if e.Name != effects.MemberProjectionName || e.Delay != 0 || e.Run == nil {
		t.Fatalf("effect = %q delay %v, want %q delay 0", e.Name, e.Delay, effects.MemberProjectionName)
	}
}

func TestMemberProjectionProjectsEachRoomOnceUpToItsNewestFactAndForgetsIt(t *testing.T) {
	rg := newMemberRig(t)
	createRoom(t, rg.rooms, otherRoom)
	rg.append(t, addFact(room, 2, 2, "bob"))
	rg.append(t, addFact(room, 3, 3, "carol"))
	rg.append(t, addFact(otherRoom, 2, 2, "dave"))
	recs := []work.Record{memberRec(room, 2), memberRec(otherRoom, 2), memberRec(room, 3), memberRec(999, 1)}
	if errs := rg.proj.Effect().Run(t.Context(), recs); !allNil(errs, 4) {
		t.Fatalf("errs = %v", errs)
	}
	want := []projectCall{{room: room, target: 3}, {room: otherRoom, target: 2}, {room: 999, target: 1}}
	if !slices.Equal(rg.projects.calls, want) {
		t.Fatalf("projects = %+v, want one per room up to its newest member version", rg.projects.calls)
	}
	head, err := rg.rooms.Get(t.Context(), room)
	if err != nil || head.MemberVersion != 3 || head.MemberCount != 3 {
		t.Fatalf("room = %+v, %v; want member version 3 with 3 members", head, err)
	}
	for user, mv := range map[string]uint64{"bob": 2, "carol": 3} {
		if m, err := rg.rooms.Member(t.Context(), room, user); err != nil || m.Role != domain.RoleMember || m.MV != mv {
			t.Fatalf("member %s = %+v, %v; want an active member at member version %d", user, m, err, mv)
		}
	}
	if !slices.Equal(rg.forgets.rooms, []uint64{room, otherRoom}) || rg.proj.Dropped() != 1 {
		t.Fatalf("forgot %v, dropped %d; want both projected rooms forgotten and the unknown room dropped", rg.forgets.rooms, rg.proj.Dropped())
	}
}

func TestMemberProjectionRetriesARoomBehindItsRecord(t *testing.T) {
	rg := newMemberRig(t)
	rg.append(t, addFact(room, 2, 2, "bob"))
	errs := rg.proj.Effect().Run(t.Context(), []work.Record{memberRec(room, 2), memberRec(room, 4)})
	if len(errs) != 2 || !errors.Is(errs[0], store.ErrStaleRead) || !errors.Is(errs[1], store.ErrStaleRead) {
		t.Fatalf("errs = %v, want a stale read for every record of the room", errs)
	}
	if len(rg.forgets.rooms) != 0 || rg.proj.Dropped() != 0 {
		t.Fatalf("forgot %v, dropped %d; want neither for a room still behind", rg.forgets.rooms, rg.proj.Dropped())
	}
}

func TestMemberProjectionRetriesOnlyTheRoomThatFailed(t *testing.T) {
	rg := newMemberRig(t)
	createRoom(t, rg.rooms, otherRoom)
	rg.append(t, addFact(room, 2, 2, "bob"))
	rg.append(t, addFact(otherRoom, 2, 2, "dave"))
	rg.projects.fail = map[uint64]error{room: errBoom}
	errs := rg.proj.Effect().Run(t.Context(), []work.Record{memberRec(room, 2), memberRec(otherRoom, 2), memberRec(room, 2)})
	if len(errs) != 3 || !errors.Is(errs[0], errBoom) || errs[1] != nil || !errors.Is(errs[2], errBoom) {
		t.Fatalf("errs = %v, want both records of the failed room retried", errs)
	}
	if !slices.Equal(rg.forgets.rooms, []uint64{otherRoom}) || rg.proj.Dropped() != 0 {
		t.Fatalf("forgot %v, dropped %d; want only the projected room forgotten", rg.forgets.rooms, rg.proj.Dropped())
	}
}

func TestMemberProjectionRetriesAFactThatCannotBeDecoded(t *testing.T) {
	rg := newMemberRig(t)
	rg.projects.fail = map[uint64]error{room: fmt.Errorf("decode member fact: %w", apperr.ErrInvalidArgument)}
	errs := rg.proj.Effect().Run(t.Context(), []work.Record{memberRec(room, 2), memberRec(room, 3)})
	if len(errs) != 2 || !errors.Is(errs[0], apperr.ErrInvalidArgument) || !errors.Is(errs[1], apperr.ErrInvalidArgument) {
		t.Fatalf("errs = %v, want both records retried so the bad fact shows in work failures", errs)
	}
	if rg.proj.Dropped() != 0 || len(rg.forgets.rooms) != 0 {
		t.Fatalf("dropped %d, forgot %v; want 0 and none", rg.proj.Dropped(), rg.forgets.rooms)
	}
}
```

`apps/core/internal/effects/member_event_test.go`:

```go
package effects_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestMemberEventDeclaresItsPolicy(t *testing.T) {
	e := newMemberRig(t).event.Effect()
	if e.Name != effects.MemberEventName || e.Delay != delay || e.Run == nil {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.MemberEventName, delay)
	}
}

func TestMemberEventPublishesTheRoomCopyAndOneCopyPerUser(t *testing.T) {
	rg := newMemberRig(t)
	added := rg.append(t, addFact(room, 2, 3, "bob", "carol"))
	if errs := rg.event.Effect().Run(t.Context(), []work.Record{memberRec(room, 2)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	ids := []string{pbconv.MemberEventID(room, 2), pbconv.MemberUserEventID(room, 2, "bob"), pbconv.MemberUserEventID(room, 2, "carol")}
	if got := storedEventIDs(rg.js); !slices.Equal(got, ids) {
		t.Fatalf("stored = %v, want %v", got, ids)
	}
	subjects := []string{"evt.acme.room.4242.member_added", "evt.acme.user.bob.member_added", "evt.acme.user.carol.member_added"}
	for i, m := range rg.js.Stored() {
		if m.Subject != subjects[i] {
			t.Fatalf("copy %d went to %q, want %q", i, m.Subject, subjects[i])
		}
	}
	events, err := rg.js.Events()
	fast := pbconv.MemberEvents(domain.RoomGroup, added)
	if err != nil || len(events) != len(fast) {
		t.Fatalf("events = %v, %v; want %d", events, err, len(fast))
	}
	for i := range fast {
		if !proto.Equal(events[i], fast[i]) {
			t.Fatalf("copy %d = %v, want the fast path copy %v", i, events[i], fast[i])
		}
	}
	if rg.event.Republished() != 3 || rg.event.Dropped() != 0 {
		t.Fatalf("republished %d, dropped %d; want 3 and 0", rg.event.Republished(), rg.event.Dropped())
	}
}

func TestMemberEventSendsTheSuccessorItsCopy(t *testing.T) {
	rg := newMemberRig(t)
	rg.append(t, addFact(room, 2, 2, "bob"))
	rg.append(t, leaveFact(room, 3, 1, "alice", domain.RoleOwner, "bob"))
	if errs := rg.event.Effect().Run(t.Context(), []work.Record{memberRec(room, 3)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	ids := []string{pbconv.MemberEventID(room, 3), pbconv.MemberUserEventID(room, 3, "alice"), pbconv.MemberUserEventID(room, 3, "bob")}
	if got := storedEventIDs(rg.js); !slices.Equal(got, ids) {
		t.Fatalf("stored = %v, want %v", got, ids)
	}
	if subj := rg.js.Stored()[2].Subject; subj != "evt.acme.user.bob.member_removed" {
		t.Fatalf("successor copy went to %q", subj)
	}
	events, err := rg.js.Events()
	if err != nil || len(events) != 3 {
		t.Fatalf("events = %v, %v", events, err)
	}
	r := events[2].GetMemberRemoved()
	if events[2].GetRecipient() != "bob" || r.GetUser() != "alice" || r.GetNewOwner() != "bob" || r.GetReason() != chatimv1.MemberRemovedReason_MEMBER_REMOVED_REASON_LEFT {
		t.Fatalf("successor copy = %v, want alice left with bob as the new owner, sent to bob", events[2])
	}
}

func TestMemberEventCountsOnlyCopiesTheStreamLacked(t *testing.T) {
	rg := newMemberRig(t)
	added := rg.append(t, addFact(room, 2, 3, "bob", "carol"))
	fast, err := publish.Message("evt", room, pbconv.MemberEvents(domain.RoomGroup, added)[1])
	if err != nil {
		t.Fatalf("publish.Message: %v", err)
	}
	if _, err := rg.js.PublishMsgAsync(fast); err != nil {
		t.Fatalf("fast path publish: %v", err)
	}
	if errs := rg.event.Effect().Run(t.Context(), []work.Record{memberRec(room, 2)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if len(rg.js.Stored()) != 3 || len(rg.js.Attempts()) != 4 || rg.event.Republished() != 2 {
		t.Fatalf("stored %d, attempts %d, republished %d; want 3, 4 and 2", len(rg.js.Stored()), len(rg.js.Attempts()), rg.event.Republished())
	}
}

func TestMemberEventRetriesARecordUntilEveryCopyIsStored(t *testing.T) {
	rg := newMemberRig(t)
	rg.append(t, addFact(room, 2, 3, "bob", "carol"))
	carol := pbconv.MemberUserEventID(room, 2, "carol")
	rg.js.RefuseWhen(func(m *nats.Msg) error {
		if publishtest.MsgID(m) == carol {
			return errBoom
		}
		return nil
	})
	errs := rg.event.Effect().Run(t.Context(), []work.Record{memberRec(room, 2)})
	if len(errs) != 1 || !errors.Is(errs[0], errBoom) {
		t.Fatalf("errs = %v, want the record retried while a copy is missing", errs)
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.MemberEventID(room, 2), pbconv.MemberUserEventID(room, 2, "bob")}) {
		t.Fatalf("stored = %v", got)
	}
	rg.js.RefuseWhen(nil)
	if errs := rg.event.Effect().Run(t.Context(), []work.Record{memberRec(room, 2)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if len(rg.js.Stored()) != 3 || rg.event.Republished() != 3 || rg.event.Dropped() != 0 {
		t.Fatalf("stored %d, republished %d, dropped %d; want 3, 3 and 0", len(rg.js.Stored()), rg.event.Republished(), rg.event.Dropped())
	}
}

func TestMemberEventDropsWhatIsGone(t *testing.T) {
	rg := newMemberRig(t)
	rg.append(t, addFact(999, 2, 2, "bob"))
	if errs := rg.event.Effect().Run(t.Context(), []work.Record{memberRec(room, 9), memberRec(999, 2)}); !allNil(errs, 2) {
		t.Fatalf("errs = %v, want nil so dropped records are not retried", errs)
	}
	if len(rg.js.Attempts()) != 0 || rg.event.Dropped() != 2 {
		t.Fatalf("attempts %d, dropped %d; want 0 and 2 (no fact, no room)", len(rg.js.Attempts()), rg.event.Dropped())
	}
}

func TestMemberEventRetriesWhenTheStoreFails(t *testing.T) {
	eff, err := effects.NewMemberEvent(
		effects.MemberEventDeps{Members: brokenMembers{}, Rooms: brokenStore{}, JS: &publishtest.JetStream{}},
		effects.MessageChangedConfig{SubjectRoot: "evt"},
	)
	if err != nil {
		t.Fatalf("NewMemberEvent: %v", err)
	}
	if errs := eff.Effect().Run(t.Context(), []work.Record{memberRec(room, 2)}); len(errs) != 1 || !errors.Is(errs[0], errBoom) || eff.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want a retryable failure", errs, eff.Dropped())
	}
}
```

`apps/core/internal/effects/member_constructors_test.go`:

```go
package effects_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestNewMemberEffectsRejectBadInput(t *testing.T) {
	rooms, js := memstore.NewRooms(), &publishtest.JetStream{}
	facts, proj, forget := rooms.MemberActions(), &spyMemberProjector{}, &forgetSpy{}
	events := effects.MessageChangedConfig{SubjectRoot: "evt"}
	deps := effects.MemberEventDeps{Members: facts, Rooms: rooms, JS: js}
	bad := map[string]func() error{
		"member_projection without a projector": func() error { _, err := effects.NewMemberProjection(nil, forget); return err },
		"member_projection without a forgetter": func() error { _, err := effects.NewMemberProjection(proj, nil); return err },
		"member_event without facts": func() error {
			_, err := effects.NewMemberEvent(effects.MemberEventDeps{Rooms: rooms, JS: js}, events)
			return err
		},
		"member_event without rooms": func() error {
			_, err := effects.NewMemberEvent(effects.MemberEventDeps{Members: facts, JS: js}, events)
			return err
		},
		"member_event without js": func() error {
			_, err := effects.NewMemberEvent(effects.MemberEventDeps{Members: facts, Rooms: rooms}, events)
			return err
		},
		"member_event without a root": func() error {
			_, err := effects.NewMemberEvent(deps, effects.MessageChangedConfig{})
			return err
		},
	}
	for name, build := range bad {
		if err := build(); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s = %v, want ErrInvalidArgument", name, err)
		}
	}
	ev, err := effects.NewMemberEvent(deps, events)
	if err != nil || ev.Effect().Delay != effects.DefaultDelay {
		t.Fatalf("member_event with defaults = %v, %v; want delay %v", ev, err, effects.DefaultDelay)
	}
}
```

**Step 3: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/effects/..."`
Expected: FAIL biên dịch: `undefined: effects.MemberProjection`, `undefined: effects.MemberEvent`, `undefined: effects.NewMemberProjection`, `undefined: effects.NewMemberEvent`, `undefined: effects.MemberEventDeps`, `undefined: effects.MemberProjectionName`, `undefined: effects.MemberEventName`. (Port `MemberProjecter` đã thêm ở Step 1 nên không báo.)

**Step 4: Code effect**

`apps/core/internal/effects/event_publisher.go` (thay cả file; `eventConfig`, `init`, `queue`, hai accessor giữ nguyên):

```go
package effects

import (
	"cmp"
	"context"
	"fmt"
	"sync/atomic"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type eventPublisher struct {
	js          publish.JetStream
	root        string
	types       *roomTypes
	republished atomic.Uint64
	dropped     atomic.Uint64
}

func eventConfig(name string, cfg MessageChangedConfig) (MessageChangedConfig, error) {
	cfg.Delay = cmp.Or(cfg.Delay, DefaultDelay)
	cfg.RoomCache = cmp.Or(cfg.RoomCache, DefaultRoomCache)
	if cfg.SubjectRoot == "" || cfg.Delay < 0 || cfg.RoomCache < 0 {
		return MessageChangedConfig{}, fmt.Errorf("%w: %s config %+v needs a subject root, a delay and a room cache", apperr.ErrInvalidArgument, name, cfg)
	}
	return cfg, nil
}

func (p *eventPublisher) init(js publish.JetStream, root string, rooms RoomReader, cache int) {
	p.js, p.root, p.types = js, root, newRoomTypes(rooms, cache)
}

func (p *eventPublisher) Republished() uint64 { return p.republished.Load() }

func (p *eventPublisher) Dropped() uint64 { return p.dropped.Load() }

func (p *eventPublisher) each(ctx context.Context, recs []work.Record, build func(context.Context, work.Record) (*chatimv1.Event, error), drop func(error) bool) []error {
	return p.eachMany(ctx, recs, func(ctx context.Context, r work.Record) ([]*chatimv1.Event, error) {
		ev, err := build(ctx, r)
		if err != nil || ev == nil {
			return nil, err
		}
		return []*chatimv1.Event{ev}, nil
	}, drop)
}

func (p *eventPublisher) eachMany(ctx context.Context, recs []work.Record, build func(context.Context, work.Record) ([]*chatimv1.Event, error), drop func(error) bool) []error {
	errs := make([]error, len(recs))
	var pending []pendingAck
	for i, r := range recs {
		evs, err := build(ctx, r)
		switch {
		case drop(err):
			p.dropped.Add(1)
		case err != nil:
			errs[i] = err
		default:
			for _, ev := range evs {
				pending = p.queue(pending, errs, i, r.Room, ev)
			}
		}
	}
	awaitAcks(ctx, pending, errs, countStored(&p.republished))
	return errs
}

func (p *eventPublisher) queue(pending []pendingAck, errs []error, i int, room uint64, ev *chatimv1.Event) []pendingAck {
	msg, err := publish.Message(p.root, room, ev)
	if err != nil {
		p.dropped.Add(1)
		return pending
	}
	return send(p.js, msg, i, errs, pending)
}
```

Nhiều `pendingAck` cùng `index` là hợp lệ: `awaitAcks` chỉ ghi `errs[index]` khi một future lỗi, không bao giờ xoá lỗi đã có, nên một bản lỗi là cả record lỗi.

`apps/core/internal/effects/member_projection.go`:

```go
package effects

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const MemberProjectionName = "member_projection"

type MemberProjection struct {
	proj    MemberProjecter
	forget  MemberForgetter
	dropped atomic.Uint64
}

func NewMemberProjection(p MemberProjecter, forget MemberForgetter) (*MemberProjection, error) {
	if p == nil || forget == nil {
		return nil, fmt.Errorf("%w: %s needs a member projector and a member cache to forget", apperr.ErrInvalidArgument, MemberProjectionName)
	}
	return &MemberProjection{proj: p, forget: forget}, nil
}

func (e *MemberProjection) Effect() Effect {
	return Effect{Name: MemberProjectionName, Run: e.run}
}

func (e *MemberProjection) Dropped() uint64 { return e.dropped.Load() }

func (e *MemberProjection) run(ctx context.Context, recs []work.Record) []error {
	errs := make([]error, len(recs))
	for _, g := range groupRecords(recs, recordRoom) {
		var target uint64
		for _, i := range g.indexes {
			target = max(target, recs[i].Seq)
		}
		_, err := e.proj.Project(ctx, g.key, target)
		switch {
		case errors.Is(err, domain.ErrRoomNotFound):
			g.drop(&e.dropped)
		case err != nil:
			g.fail(errs, err)
		default:
			e.forget.ForgetMembers(g.key)
		}
	}
	return errs
}
```

`apps/core/internal/effects/member_event.go`:

```go
package effects

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const MemberEventName = "member_event"

type MemberEventDeps struct {
	Members MemberFacts
	Rooms   RoomReader
	JS      publish.JetStream
}

type MemberEvent struct {
	eventPublisher
	members MemberFacts
	delay   time.Duration
}

func NewMemberEvent(deps MemberEventDeps, cfg MessageChangedConfig) (*MemberEvent, error) {
	if deps.Members == nil || deps.Rooms == nil || deps.JS == nil {
		return nil, fmt.Errorf("%w: %s needs member facts, rooms and a jetstream client", apperr.ErrInvalidArgument, MemberEventName)
	}
	cfg, err := eventConfig(MemberEventName, cfg)
	if err != nil {
		return nil, err
	}
	e := &MemberEvent{members: deps.Members, delay: cfg.Delay}
	e.init(deps.JS, cfg.SubjectRoot, deps.Rooms, cfg.RoomCache)
	return e, nil
}

func (e *MemberEvent) Effect() Effect {
	return Effect{Name: MemberEventName, Delay: e.delay, Run: e.run}
}

func (e *MemberEvent) run(ctx context.Context, recs []work.Record) []error {
	return e.eachMany(ctx, recs, e.events, memberGone)
}

func memberGone(err error) bool {
	return errors.Is(err, domain.ErrRoomNotFound) || errors.Is(err, store.ErrMemberActionNotFound)
}

func (e *MemberEvent) events(ctx context.Context, r work.Record) ([]*chatimv1.Event, error) {
	fact, err := e.members.At(ctx, r.Room, r.Seq)
	if err != nil {
		return nil, err
	}
	typ, err := e.types.get(ctx, r.Room)
	if err != nil {
		return nil, err
	}
	return pbconv.MemberEvents(typ, fact), nil
}
```

**Step 5: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/internal/effects/..."
make -s go ARGS="test -race -count=3 ./apps/core/internal/effects/..."
wc -l apps/core/internal/effects/*.go | sort -n | tail -8
```

Expected: PASS cả hai lần (test cũ của `msg_created`, `msg_changed`, `room_created`, `edit_projection`, `reaction_*`, `pin_*`, workers không đổi: `each` giờ đi qua `eachMany`). Mỗi file < 200 (`event_publisher.go` ~80, `member_event.go` ~70, `member_projection.go` ~50, `member_event_test.go` ~150, `member_projection_test.go` ~90, `member_fixtures_test.go` ~100, `harness_test.go` vẫn 199).

**Step 6: Wiring, metrics**

`apps/core/member_effects_wiring.go`:

```go
package main

import (
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/memberproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

func (fx *effectSet) wireMemberEffects(cfg config.Config, cl *clients, st *mongostore.Store, forget effects.MemberForgetter) error {
	facts := st.MemberActions()
	projector, err := memberproj.New(facts, st)
	if err != nil {
		return fmt.Errorf("wire member projector: %w", err)
	}
	if fx.memberProjection, err = effects.NewMemberProjection(projector, forget); err != nil {
		return fmt.Errorf("wire member_projection effect: %w", err)
	}
	events := effects.MessageChangedConfig{SubjectRoot: cfg.Stream.SubjectRoot, Delay: cfg.EffectDelay, RoomCache: cfg.EffectRoomCache}
	if fx.memberEvent, err = effects.NewMemberEvent(effects.MemberEventDeps{Members: facts, Rooms: st, JS: cl.effectsJS}, events); err != nil {
		return fmt.Errorf("wire member_event effect: %w", err)
	}
	return nil
}
```

`apps/core/effects_wiring.go` (thay cả file; so với HEAD sau Task 6 chỉ khác: hai field mới, tham số `forget`, lời gọi `wireMemberEffects`, dòng registry `MemberInserted` cuối và hai dòng `counters`; nếu file sau Task 6 còn khác gì ngoài dòng registry tạm thì dừng và báo):

```go
package main

import (
	"fmt"
	"log/slog"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/eventmark"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

type effectSet struct {
	workers          *effects.Workers
	msgCreated       *effects.MessageCreated
	roomCreated      *effects.RoomCreated
	editProjection   *effects.EditProjection
	msgChanged       *effects.MessageChanged
	reactionCounter  *effects.ReactionCounter
	reactionEvent    *effects.ReactionEvent
	pinProjection    *effects.PinProjection
	pinEvent         *effects.PinEvent
	memberProjection *effects.MemberProjection
	memberEvent      *effects.MemberEvent
}

func wireEffects(cfg config.Config, cl *clients, st *mongostore.Store, marks *eventmark.Store, owner effects.Owner, forget effects.MemberForgetter, log *slog.Logger) (effectSet, error) {
	fx := effectSet{}
	var err error
	fx.msgCreated, err = effects.NewMessageCreated(
		effects.MessageCreatedDeps{Marks: marks, Messages: st, Rooms: st, JS: cl.effectsJS},
		effects.MessageCreatedConfig{SubjectRoot: cfg.Stream.SubjectRoot, Delay: cfg.EffectDelay, RoomCache: cfg.EffectRoomCache},
	)
	if err != nil {
		return effectSet{}, fmt.Errorf("wire msg_created effect: %w", err)
	}
	fx.roomCreated, err = effects.NewRoomCreated(
		effects.RoomCreatedDeps{Rooms: st, JS: cl.effectsJS},
		effects.RoomCreatedConfig{SubjectRoot: cfg.Stream.SubjectRoot, Delay: cfg.EffectDelay},
	)
	if err != nil {
		return effectSet{}, fmt.Errorf("wire room_created effect: %w", err)
	}
	fx.editProjection, err = effects.NewEditProjection(effects.EditProjectionDeps{Edits: st, Messages: st, Purger: st})
	if err != nil {
		return effectSet{}, fmt.Errorf("wire edit_projection effect: %w", err)
	}
	fx.msgChanged, err = effects.NewMessageChanged(
		effects.MessageChangedDeps{Edits: st, Messages: st, Rooms: st, JS: cl.effectsJS},
		effects.MessageChangedConfig{SubjectRoot: cfg.Stream.SubjectRoot, Delay: cfg.EffectDelay, RoomCache: cfg.EffectRoomCache},
	)
	if err != nil {
		return effectSet{}, fmt.Errorf("wire msg_changed effect: %w", err)
	}
	if err = fx.wireReactionPinEffects(cfg, cl, st); err != nil {
		return effectSet{}, err
	}
	if err = fx.wireMemberEffects(cfg, cl, st, forget); err != nil {
		return effectSet{}, err
	}
	activity := effects.NewRoomActivity(st)
	registry := effects.Registry{
		store.MessageInserted: {activity.Effect(), fx.msgCreated.Effect()},
		store.RoomInserted:    {fx.roomCreated.Effect()},
		store.EditInserted:    {activity.Effect(), fx.editProjection.Effect(), fx.msgChanged.Effect()},
		store.ReactionChanged: {activity.Effect(), fx.reactionCounter.Effect(), fx.reactionEvent.Effect()},
		store.PinInserted:     {activity.Effect(), fx.pinProjection.Effect(), fx.pinEvent.Effect()},
		store.MemberInserted:  {activity.Effect(), fx.memberProjection.Effect(), fx.memberEvent.Effect()},
	}
	fx.workers, err = effects.New(effects.Deps{
		Queue:    func(p int) work.Queue { return work.NewQueue(cl.effectsJS, cfg.Work.Name, p, cfg.Effects.RetryDelay) },
		Owner:    owner,
		Registry: registry,
	}, cfg.Effects, log)
	if err != nil {
		return effectSet{}, fmt.Errorf("wire effect workers: %w", err)
	}
	return fx, nil
}

func (fx effectSet) counters() map[string]effectCounters {
	return map[string]effectCounters{
		fx.msgCreated.Effect().Name:       {republished: fx.msgCreated.Republished, dropped: fx.msgCreated.Dropped},
		fx.roomCreated.Effect().Name:      {republished: fx.roomCreated.Republished, dropped: fx.roomCreated.Dropped},
		fx.msgChanged.Effect().Name:       {republished: fx.msgChanged.Republished, dropped: fx.msgChanged.Dropped},
		fx.editProjection.Effect().Name:   {dropped: fx.editProjection.Dropped},
		fx.reactionCounter.Effect().Name:  {republished: fx.reactionCounter.Republished, dropped: fx.reactionCounter.Dropped},
		fx.reactionEvent.Effect().Name:    {republished: fx.reactionEvent.Republished, dropped: fx.reactionEvent.Dropped},
		fx.pinEvent.Effect().Name:         {republished: fx.pinEvent.Republished, dropped: fx.pinEvent.Dropped},
		fx.pinProjection.Effect().Name:    {dropped: fx.pinProjection.Dropped},
		fx.memberProjection.Effect().Name: {dropped: fx.memberProjection.Dropped},
		fx.memberEvent.Effect().Name:      {republished: fx.memberEvent.Republished, dropped: fx.memberEvent.Dropped},
	}
}

func (fx effectSet) counterRepairs() map[string]func() uint64 {
	return map[string]func() uint64{pbconv.ReactionsCounter: fx.reactionCounter.Repaired}
}
```

`apps/core/wiring.go`: dòng `fx, err := wireEffects(cfg, cl, st, marks, slots, log)` → `fx, err := wireEffects(cfg, cl, st, marks, slots, router, log)` (`router` đã dựng ở trên, `*actor.Router` có `ForgetMembers` từ Task 8). Không đổi dòng nào khác.

`apps/core/metrics_wiring.go`, trong `workerSources`, thay hằng `dropHelp` bằng:

```go
	const dropHelp = "Work records an effect gave up on (missing room, message, edit, reaction, pin or member fact, or a corrupt document)."
```

`apps/core/metrics_wiring_test.go` (sửa tại chỗ, không thay cả file vì Task 14 đã thêm `readUnbatched`):
- Trong `fakeProbes`, map `effectCounts`, thêm sau dòng `"pin_projection": …`:

```go
			"member_projection": {dropped: func() uint64 { return 9 }},
			"member_event":      {republished: func() uint64 { return 10 }, dropped: func() uint64 { return 11 }},
```

- Trong `TestEffectMetricsReadTheirEffectByLabel`, map `want` thêm:

```go
		"effect_dropped_total{member_projection}":   9,
		"reconcile_republished_total{member_event}": 10, "effect_dropped_total{member_event}": 11,
```

- Cùng test, `for _, silent := range []string{"edit_projection", "pin_projection"}` → `[]string{"edit_projection", "pin_projection", "member_projection"}`.

(gofmt căn lại cột của hai map; chạy `make fmt-check`.)

`apps/core/effects_wiring_test.go` (thay cả file):

```go
package main

import (
	"context"
	"maps"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/counter"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/memberproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

type noMarks struct{}

func (noMarks) Acked(_ context.Context, keys []store.MsgKey) ([]bool, error) {
	return make([]bool, len(keys)), nil
}

type noForget struct{}

func (noForget) ForgetMembers(uint64) {}

func built[T any](v T, err error) func(*testing.T) T {
	return func(t *testing.T) T {
		t.Helper()
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		return v
	}
}

func TestEffectSetExportsEveryEffect(t *testing.T) {
	msgs, rooms, edits := memstore.NewMessages(), memstore.NewRooms(), memstore.NewEdits()
	reactions, pins, js := memstore.NewReactions(), memstore.NewPins(), &publishtest.JetStream{}
	events := effects.MessageChangedConfig{SubjectRoot: "evt"}
	fx := effectSet{
		msgCreated: built(effects.NewMessageCreated(
			effects.MessageCreatedDeps{Marks: noMarks{}, Messages: msgs, Rooms: rooms, JS: js}, effects.MessageCreatedConfig{SubjectRoot: "evt"}))(t),
		roomCreated:    built(effects.NewRoomCreated(effects.RoomCreatedDeps{Rooms: rooms, JS: js}, effects.RoomCreatedConfig{SubjectRoot: "evt"}))(t),
		editProjection: built(effects.NewEditProjection(effects.EditProjectionDeps{Edits: edits, Messages: msgs, Purger: edits}))(t),
		msgChanged:     built(effects.NewMessageChanged(effects.MessageChangedDeps{Edits: edits, Messages: msgs, Rooms: rooms, JS: js}, events))(t),
		reactionCounter: built(effects.NewReactionCounter(
			effects.ReactionCounterDeps{Messages: msgs, Counter: built(counter.New(msgs, reactions))(t), Rooms: rooms, JS: js},
			effects.ReactionCounterConfig{SubjectRoot: "evt"}))(t),
		reactionEvent:    built(effects.NewReactionEvent(effects.ReactionEventDeps{Reactions: reactions, Rooms: rooms, JS: js}, events))(t),
		pinProjection:    built(effects.NewPinProjection(built(pinproj.New(pins, rooms))(t)))(t),
		pinEvent:         built(effects.NewPinEvent(effects.PinEventDeps{Pins: pins, Messages: msgs, Rooms: rooms, JS: js}, events))(t),
		memberProjection: built(effects.NewMemberProjection(built(memberproj.New(rooms.MemberActions(), rooms))(t), noForget{}))(t),
		memberEvent:      built(effects.NewMemberEvent(effects.MemberEventDeps{Members: rooms.MemberActions(), Rooms: rooms, JS: js}, events))(t),
	}
	counters := fx.counters()
	want := []string{
		effects.EditProjectionName, effects.MemberEventName, effects.MemberProjectionName, effects.MessageChangedName, effects.MessageCreatedName,
		effects.PinEventName, effects.PinProjectionName, effects.ReactionCounterName, effects.ReactionEventName, effects.RoomCreatedName,
	}
	if got := slices.Sorted(maps.Keys(counters)); !slices.Equal(got, want) {
		t.Fatalf("effects with metrics = %v, want %v", got, want)
	}
	for name, c := range counters {
		silent := name == effects.EditProjectionName || name == effects.PinProjectionName || name == effects.MemberProjectionName
		if c.dropped == nil || (c.republished == nil) != silent {
			t.Errorf("%s: dropped set %v, republished set %v; want dropped always and republished only when it publishes", name, c.dropped != nil, c.republished != nil)
		}
	}
	if repairs := fx.counterRepairs(); len(repairs) != 1 || repairs[pbconv.ReactionsCounter] == nil {
		t.Errorf("counter repairs = %v, want only %q", repairs, pbconv.ReactionsCounter)
	}
}
```

(`want` đã sắp theo chữ: `edit_projection` < `member_event` < `member_projection` < `msg_changed` < `msg_created` < `pin_event` < `pin_projection` < `reaction_counter` < `reaction_event` < `room_created`.)

**Step 7: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/internal/effects/... ./apps/core/"
make alerts-check
wc -l apps/core/*.go | sort -n | tail -6
```

Expected: PASS (itest skip); `make alerts-check` in `SUCCESS: 16 rules found` (không đổi file luật). Mỗi file < 200 (`effects_wiring.go` ~105, `member_effects_wiring.go` ~30, `effects_wiring_test.go` ~80, `metrics_wiring_test.go` ~120; `wiring.go` theo Task 14, ghi số dòng vào báo cáo, ≥ 190 thì dừng và báo controller).

**Step 8: Itest**

```bash
make infra-up && make itest
```

Expected: mọi package `ok` (registry cuối dựng hai effect member trên Mongo/NATS thật; record member của mọi room tạo trong itest giờ chạy `member_projection` (mọi upsert bị guard chặn, CAS trượt vì đầu đã đúng) và `member_event` (mọi bản là bản trùng của fast path); itest riêng cho member là Task 16 và 18).

**Step 9: INDEXES + commit + review**

```bash
python3 bin/indexes_edit.py apps/core/internal/effects purpose + "; member_projection effect (delay 0) for MemberInserted: one memberproj Project per room up to the newest member version of the batch, then Router.ForgetMembers; only a missing room is dropped and counted; every other error (invalid data, stale read, contention) retries every record of the room; member_event effect (delay RECONCILE_DELAY, no ack mark): MemberActions.At(room, mv), room type cache, pbconv.MemberEvents (room copy {room}-m{mv}, then one user copy {room}-m{mv}-{user} per changed user and the successor), publishes every copy and acks the record only when every copy has a PubAck; counts only PubAcks the stream had not stored; missing fact or room dropped; eventPublisher.eachMany publishes several events per record and each wraps it"
python3 bin/indexes_edit.py apps/core/internal/effects key_symbols + ";MemberProjection;NewMemberProjection;MemberProjection.Effect;MemberProjection.Dropped;MemberProjectionName;MemberEvent;NewMemberEvent;MemberEvent.Effect;MemberEventDeps;MemberEventName;MemberFacts;MemberProjecter;MemberForgetter"
python3 bin/indexes_edit.py apps/core/internal/effects decisions + ";D97;D102;D103;D106"
python3 bin/indexes_edit.py apps/core purpose "~" "MemberInserted -> room_activity only until the member effects land" "MemberInserted -> room_activity, member_projection, member_event"
python3 bin/indexes_edit.py apps/core purpose "~" "(reaction_pin_effects_wiring.go builds the four new effects with their own counter and pin projector)" "(reaction_pin_effects_wiring.go builds the reaction and pin effects with their own counter and pin projector; member_effects_wiring.go builds the member effects with their own memberproj.Projector and the router as the member cache to forget)"
python3 bin/indexes_edit.py apps/core purpose "~" "(edit_projection, pin_projection)" "(edit_projection, pin_projection, member_projection)"
python3 bin/indexes_edit.py apps/core key_symbols + ";effectSet.wireMemberEffects"
python3 bin/indexes_edit.py apps/core decisions + ";D103;D106"
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
make fmt-check && make vet && make lint
git add apps/core/internal/effects/member_projection.go apps/core/internal/effects/member_event.go apps/core/internal/effects/member_fixtures_test.go apps/core/internal/effects/member_projection_test.go apps/core/internal/effects/member_event_test.go apps/core/internal/effects/member_constructors_test.go apps/core/member_effects_wiring.go
git commit -m "feat(effects): project member facts and republish their room and user events" -- apps/core/internal/effects/ apps/core/effects_wiring.go apps/core/member_effects_wiring.go apps/core/effects_wiring_test.go apps/core/metrics_wiring.go apps/core/metrics_wiring_test.go apps/core/wiring.go INDEXES.csv
git show --stat HEAD
```

Expected: mỗi lệnh helper in `… ok` (lệnh `~` đầu tiên thoát lỗi nghĩa là Task 6 viết đoạn registry khác: `grep -n "^apps/core," INDEXES.csv` rồi thay đúng đoạn Task 6 đã viết, ghi vào báo cáo); `{7}`; fmt/vet/lint sạch; `git show --stat HEAD` chỉ có các file trên (14 file + `INDEXES.csv`). Không push (push ở Task 20).

Task rủi ro: một reviewer (thứ tự và delay trong registry; `ForgetMembers` chỉ sau `Project` thành công; chỉ drop khi `ErrRoomNotFound` (giống `pin_projection`, M2b.3); lỗi dữ liệu được retry và hiện ở `work_failures_total`; `eachMany` không đổi hành vi `each` cũ (một lỗi bản nào cũng là lỗi record, `Republished` chỉ đếm PubAck không trùng); `wireEffects` nhận router; metric + vẫn 16 luật; tối đa `-count=3` trên `effects`).

---

### Task 16: `/app resync` quét `member_actions`

Resync (D81) quét timeline chính, `message_edits`, `reactions`, `pin_actions` (M2b.2–M2b.3). Fact member không đổi `last_seq`, nên phải quét riêng theo `{r, ts}` như fact ghim (D106): sau `pin_actions`, mỗi room quét `Members.Between(room, from, to, store.MaxMemberScan)` qua `scanByTime` (phân trang theo `ts`, bỏ trùng mép trang theo id record, một thời điểm đầy cả trang → `ErrMemberPageFull`). Record `{Kind: MemberInserted, Room: a.Room, Seq: a.MV, CommittedAt: a.At}`; worker `member_projection` áp + CAS đầu, `member_event` phát mọi bản.

Thứ tự trong một room: room record → tin (ngược) → fact sửa → reaction → ghim → member. Worker không dựa vào thứ tự này.

Hệ quả cần biết (ảnh hưởng test cũ): từ Task 4, `memstore.Rooms.Create` ghi fact mv 1 với `At = CreatedAt`, nên room **tạo trong khoảng** giờ cho thêm đúng một record member (`g:{room}-m1`) ngay sau record room của nó. Test cũ của `resync` có `newRoom` tạo ở `lostFrom + 10m` nên báo cáo và danh sách id của chúng đổi theo (`MemberRecords: 1`, thêm `g:{newRoom}-m1` sau `r:{newRoom}`). Diễn tập itest cũng đếm fact mv 1 của room vừa tạo.

Giới hạn giống reaction/ghim (D91): resync chọn room theo `ab`/`ca`; room chỉ có fact member trong khoảng mất (không tin, không sửa nào sau đó nâng `ab`) không được chọn, phải chạy `-room`. Task 19 ghi vào §8.3 và README.

Trước khi bắt đầu, kiểm tên của Part A (chỉ đọc):

```bash
grep -n "func (s \*Rooms) MemberActions\|func (m \*MemberActions) Between\|func (a \*MemberActions) Between" apps/core/internal/store/memstore/*.go
grep -n "func (s \*Store) MemberActions\|) Between(ctx context.Context, room uint64, from, to time.Time, limit int) (\[\]domain.MemberAction" apps/core/internal/store/mongostore/*.go
grep -n "MaxMemberScan\|MemberInserted" apps/core/internal/store/*.go
grep -n '"g:"' apps/core/internal/work/*.go
git diff --quiet -- INDEXES.csv; echo "INDEXES dirty=$?"
```

Expected: accessor `MemberActions()` ở cả memstore và mongostore, kiểu con có `Between(ctx, room, from, to, limit) ([]domain.MemberAction, error)`; `store.MaxMemberScan`, `store.MemberInserted`; `work.Record.ID()` có nhánh `"g:"` (Task 6); `dirty=0`. Khác thì dừng và báo cáo (tên khác chỉ đổi trong snippet, ghi vào báo cáo).

**Files:**
- Create: `apps/core/internal/resync/world_test.go` (tách từ `scan_test.go`, Step 1)
- Modify: `apps/core/internal/resync/scan_test.go`
- Create: `apps/core/internal/resync/members.go`
- Create: `apps/core/internal/resync/members_test.go`
- Modify: `apps/core/internal/resync/scan.go` (`Deps`, `Report`, `room`)
- Modify: `apps/core/internal/resync/edits_test.go`, `apps/core/internal/resync/reactions_pins_test.go` (báo cáo có `member_records`)
- Modify: `apps/core/resync_command.go`
- Modify: `apps/core/resync_integration_test.go` (diễn tập có thêm một fact member)
- Modify: `INDEXES.csv`

**Step 1: Tách `scan_test.go` (chỉ di chuyển, không đổi hành vi)**

`scan_test.go` đang 187 dòng. Chuyển phần dựng thế giới sang file mới; các test ở lại.

`apps/core/internal/resync/world_test.go`:

```go
package resync_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

const (
	busyRoom    uint64 = 7_340_000_001
	newRoom     uint64 = 7_340_000_002
	staleRoom   uint64 = 7_340_000_003
	foreignRoom uint64 = 7_340_000_004
)

var (
	lostFrom   = time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	lostTo     = lostFrom.Add(time.Hour)
	errPublish = errors.New("publish failed")
	target     = resync.Target{SubjectRoot: "work", Partitions: 32}
)

type publishSpy struct {
	mu  sync.Mutex
	ids []string
	err error
}

func (p *publishSpy) PublishMsg(_ context.Context, m *nats.Msg, _ ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
	if p.err != nil {
		return nil, p.err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ids = append(p.ids, m.Header.Get(jetstream.MsgIDHeader))
	return &jetstream.PubAck{}, nil
}

func (p *publishSpy) published() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.ids)
}

type world struct {
	rooms     *memstore.Rooms
	msgs      *memstore.Messages
	edits     *memstore.Edits
	reactions *memstore.Reactions
	pins      *memstore.Pins
}

func newWorld(t *testing.T) world {
	t.Helper()
	w := world{
		rooms: memstore.NewRooms(), msgs: memstore.NewMessages(), edits: memstore.NewEdits(),
		reactions: memstore.NewReactions(), pins: memstore.NewPins(),
	}
	old := lostFrom.Add(-48 * time.Hour)
	w.room(t, busyRoom, "acme", old)
	w.room(t, newRoom, "acme", lostFrom.Add(10*time.Minute))
	w.room(t, staleRoom, "acme", old)
	w.room(t, foreignRoom, "other", lostFrom.Add(20*time.Minute))
	w.messages(t, busyRoom, 150, lostFrom.Add(-30*time.Minute))
	w.messages(t, staleRoom, 5, lostFrom.Add(-3*time.Hour))
	acts := []store.Activity{
		{Room: busyRoom, Seq: 150, At: lostFrom.Add(2 * time.Hour)},
		{Room: staleRoom, Seq: 5, At: lostFrom.Add(-3 * time.Hour)},
	}
	if err := w.rooms.TouchActivity(t.Context(), acts); err != nil {
		t.Fatalf("TouchActivity: %v", err)
	}
	return w
}

func (w world) room(t *testing.T, id uint64, tenant string, created time.Time) {
	t.Helper()
	r := domain.Room{ID: id, Tenant: tenant, Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: created, MemberCount: 1}
	owner := domain.Member{Room: id, Tenant: tenant, User: "alice", Role: domain.RoleOwner, JoinedAt: created}
	if err := w.rooms.Create(t.Context(), r, []domain.Member{owner}); err != nil {
		t.Fatalf("Create(%d): %v", id, err)
	}
}

func (w world) messages(t *testing.T, room uint64, n int, first time.Time) {
	t.Helper()
	msgs := make([]domain.Message, n)
	for i := range msgs {
		seq := uint64(i + 1)
		msgs[i] = domain.Message{
			Room: room, Seq: seq, Tenant: "acme", From: "alice", Kind: domain.KindText, Text: "hi",
			CID: "c" + strconv.FormatUint(seq, 10), CreatedAt: first.Add(time.Duration(i) * time.Minute),
		}
	}
	for i, r := range w.msgs.Insert(t.Context(), msgs) {
		if r.Outcome != store.Inserted {
			t.Fatalf("insert %d/%d: %+v", room, i+1, r)
		}
	}
}

func (w world) deps(pub resync.Publisher) resync.Deps {
	return resync.Deps{Rooms: w.rooms, Pages: w.msgs, Edits: w.edits, Reactions: w.reactions, Pins: w.pins, Pub: pub}
}
```

`apps/core/internal/resync/scan_test.go`: xoá khối hằng, biến, `publishSpy`, `world`, `newWorld`, `room`, `messages`, `deps` (dòng 24–119 ở HEAD), giữ nguyên năm test; khối import còn lại:

```go
import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)
```

Nếu Part A đã sửa `world.room` (ví dụ đổi cách dựng member ban đầu), giữ bản của Part A khi chuyển.

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/internal/resync/..."
wc -l apps/core/internal/resync/scan_test.go apps/core/internal/resync/world_test.go
make fmt-check && make vet && make lint
git add apps/core/internal/resync/world_test.go
git commit -m "test(resync): move the scan test world into its own file" -- apps/core/internal/resync/scan_test.go apps/core/internal/resync/world_test.go
```

Expected: PASS y hệt trước (không đổi test nào); `scan_test.go` ~72 dòng, `world_test.go` ~120; commit chỉ hai file.

**Step 2: Test**

`apps/core/internal/resync/world_test.go`, hàm `deps` (thêm `Members`):

```go
func (w world) deps(pub resync.Publisher) resync.Deps {
	return resync.Deps{Rooms: w.rooms, Pages: w.msgs, Edits: w.edits, Reactions: w.reactions, Pins: w.pins, Members: w.rooms.MemberActions(), Pub: pub}
}
```

`apps/core/internal/resync/scan_test.go`, ba chỗ:
- `TestResyncPublishesRecordsOfTheLostRangeOnly`: sau dòng `want = append(want, work.Record{Kind: store.RoomInserted, Room: newRoom}.ID())` thêm `want = append(want, work.Record{Kind: store.MemberInserted, Room: newRoom, Seq: 1}.ID())`; điều kiện báo cáo thành `rep != (resync.Report{Rooms: 2, RoomRecords: 1, MessageRecords: 61, MemberRecords: 1})` và thông điệp `"report = %+v, want 2 rooms, 1 room record, 61 message records and the new room's first member fact"`.
- `TestResyncDryRunCountsWithoutPublishingOrPacing`: `resync.Report{Rooms: 2, RoomRecords: 1, MessageRecords: 61, DryRun: true}` → `resync.Report{Rooms: 2, RoomRecords: 1, MessageRecords: 61, MemberRecords: 1, DryRun: true}`.
- Ba test còn lại không đổi (`staleRoom` tạo ở `lostFrom − 48h`, fact mv 1 của nó ngoài khoảng; test lỗi publish dừng ở record đầu).

`apps/core/internal/resync/edits_test.go`, `TestResyncPublishesEditsOfTheLostRangeAfterTheTimeline`, thay phần từ `if want := …` tới hết hàm:

```go
	if want := (resync.Report{Rooms: 2, RoomRecords: 1, MessageRecords: 61, EditRecords: 1, MemberRecords: 1}); rep != want {
		t.Fatalf("report = %+v, want %+v", rep, want)
	}
	got := pub.published()
	roomRecord := work.Record{Kind: store.RoomInserted, Room: newRoom}.ID()
	memberRecord := work.Record{Kind: store.MemberInserted, Room: newRoom, Seq: 1}.ID()
	if len(got) != 64 || got[61] != inRange || got[62] != roomRecord || got[63] != memberRecord {
		t.Fatalf("published %d ids ending %v, want 61 messages, then %s, %s and %s", len(got), got[max(0, len(got)-3):], inRange, roomRecord, memberRecord)
	}
	if rep.String() != "resync rooms=2 room_records=1 message_records=61 edit_records=1 reaction_records=0 pin_records=0 member_records=1 dry_run=false" {
		t.Fatalf("report line = %q", rep.String())
	}
}
```

`apps/core/internal/resync/reactions_pins_test.go`, `TestResyncPublishesReactionsAndPinsOfTheLostRangeAfterTheEdits`, thay phần từ `want := resync.Report{…}` tới hết hàm:

```go
	want := resync.Report{Rooms: 2, RoomRecords: 1, MessageRecords: 61, EditRecords: 1, ReactionRecords: 2, PinRecords: 1, MemberRecords: 1}
	if rep != want {
		t.Fatalf("report = %+v, want %+v", rep, want)
	}
	got := pub.published()
	tail := []string{
		edit, changed, removed, pinned,
		work.Record{Kind: store.RoomInserted, Room: newRoom}.ID(), work.Record{Kind: store.MemberInserted, Room: newRoom, Seq: 1}.ID(),
	}
	if len(got) != 67 || !slices.Equal(got[61:], tail) {
		t.Fatalf("published %d ids ending %v, want 61 messages then %v", len(got), got[max(0, len(got)-6):], tail)
	}
	if rep.String() != "resync rooms=2 room_records=1 message_records=61 edit_records=1 reaction_records=2 pin_records=1 member_records=1 dry_run=false" {
		t.Fatalf("report line = %q", rep.String())
	}
}
```

`apps/core/internal/resync/members_test.go`:

```go
package resync_test

import (
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

func (w world) member(t *testing.T, room, mv uint64, at time.Time) string {
	t.Helper()
	a := domain.MemberAction{
		Room: room, MV: mv, Tenant: "acme", Op: domain.MemberOpAdd,
		Changes: []domain.MemberChange{{User: "u" + strconv.FormatUint(mv, 10), Role: domain.RoleMember}},
		By:      "alice", At: at, Count: 2,
	}
	if err := w.rooms.MemberActions().Append(t.Context(), a); err != nil {
		t.Fatalf("Append(%d m%d): %v", room, mv, err)
	}
	return work.Record{Kind: store.MemberInserted, Room: room, Seq: mv}.ID()
}

func TestResyncPublishesMemberFactsOfTheLostRangeLast(t *testing.T) {
	w := newWorld(t)
	inRange := w.member(t, busyRoom, 2, lostFrom.Add(5*time.Minute))
	w.member(t, busyRoom, 3, lostTo.Add(time.Minute))
	pinned := w.pin(t, busyRoom, 1, 40, lostFrom.Add(6*time.Minute))
	pub := &publishSpy{}
	rep, err := resync.Run(t.Context(), w.deps(pub), target, resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Rate: resync.MaxRate})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := resync.Report{Rooms: 2, RoomRecords: 1, MessageRecords: 61, PinRecords: 1, MemberRecords: 2}
	if rep != want {
		t.Fatalf("report = %+v, want %+v", rep, want)
	}
	got := pub.published()
	tail := []string{
		pinned, inRange,
		work.Record{Kind: store.RoomInserted, Room: newRoom}.ID(), work.Record{Kind: store.MemberInserted, Room: newRoom, Seq: 1}.ID(),
	}
	if len(got) != 65 || !slices.Equal(got[61:], tail) {
		t.Fatalf("published %d ids ending %v, want 61 messages then %v", len(got), got[max(0, len(got)-4):], tail)
	}
	if rep.String() != "resync rooms=2 room_records=1 message_records=61 edit_records=0 reaction_records=0 pin_records=1 member_records=2 dry_run=false" {
		t.Fatalf("report line = %q", rep.String())
	}
}

func TestResyncPagesMemberFactsByTimeWithoutRepeatingThePageEdge(t *testing.T) {
	w := newWorld(t)
	for i := range uint64(1200) {
		w.member(t, staleRoom, i+2, lostFrom.Add(time.Duration((i+1)/3)*time.Millisecond))
	}
	opts := resync.Options{From: lostFrom, To: lostTo, Room: staleRoom, Rate: 1, DryRun: true}
	rep, err := resync.Run(t.Context(), w.deps(&publishSpy{}), target, opts)
	if err != nil || rep != (resync.Report{Rooms: 1, MemberRecords: 1200, DryRun: true}) {
		t.Fatalf("Run = %+v, %v; want 1200 member records, each once", rep, err)
	}
}

func TestResyncStopsWhenOneInstantHoldsMoreMemberFactsThanAPage(t *testing.T) {
	w := newWorld(t)
	at := lostFrom.Add(time.Minute)
	for mv := range uint64(1001) {
		w.member(t, staleRoom, mv+2, at)
	}
	opts := resync.Options{From: lostFrom, To: lostTo, Room: staleRoom, Rate: 1, DryRun: true}
	rep, err := resync.Run(t.Context(), w.deps(&publishSpy{}), target, opts)
	if !errors.Is(err, resync.ErrMemberPageFull) || rep.MemberRecords != 1000 {
		t.Fatalf("Run = %+v, %v; want ErrMemberPageFull after one full page", rep, err)
	}
}
```

`w.pin` có sẵn ở `reactions_pins_test.go`. Fact mv 2.. của `staleRoom` chỉ nằm trong log fact (resync không cần projection). `Count: 2` là số tuỳ ý hợp lệ (`≥ 0`); không suy từ `mv` để khỏi đổi kiểu `uint64 → int`.

**Step 3: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/resync/..."`
Expected: FAIL biên dịch: `unknown field Members in struct literal of type resync.Deps`, `unknown field MemberRecords in struct literal of type resync.Report`, `undefined: resync.ErrMemberPageFull`.

**Step 4: Code**

`apps/core/internal/resync/members.go`:

```go
package resync

import (
	"context"
	"errors"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

var ErrMemberPageFull = errors.New("resync: one instant holds more member actions than a member page")

type Members interface {
	Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.MemberAction, error)
}

func (s *scanner) members(ctx context.Context, room uint64) error {
	return scanByTime(ctx, s, room, timeScan[domain.MemberAction]{
		name:    "member actions",
		limit:   store.MaxMemberScan,
		full:    ErrMemberPageFull,
		between: s.deps.Members.Between,
		record:  memberRecord,
		counted: &s.rep.MemberRecords,
	})
}

func memberRecord(a domain.MemberAction) work.Record {
	return work.Record{Kind: store.MemberInserted, Room: a.Room, Seq: a.MV, CommittedAt: a.At}
}
```

`apps/core/internal/resync/scan.go`, ba chỗ:

```go
type Deps struct {
	Rooms     Rooms
	Pages     Pages
	Edits     Edits
	Reactions Reactions
	Pins      Pins
	Members   Members
	Pub       Publisher
}
```

```go
type Report struct {
	Rooms, RoomRecords, MessageRecords, EditRecords, ReactionRecords, PinRecords, MemberRecords int
	DryRun                                                                                      bool
}

func (r Report) String() string {
	return fmt.Sprintf("resync rooms=%d room_records=%d message_records=%d edit_records=%d reaction_records=%d pin_records=%d member_records=%d dry_run=%t",
		r.Rooms, r.RoomRecords, r.MessageRecords, r.EditRecords, r.ReactionRecords, r.PinRecords, r.MemberRecords, r.DryRun)
}
```

Trong `room`: `[]func(context.Context, uint64) error{s.timeline, s.edits, s.reactions, s.pins}` → `[]func(context.Context, uint64) error{s.timeline, s.edits, s.reactions, s.pins, s.members}`.

(gofmt căn cột `DryRun`; chạy `make fmt-check`.)

**Step 5: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/internal/resync/..."
wc -l apps/core/internal/resync/*.go
```

Expected: PASS, gồm ba test mới và các test cũ đã sửa ở Step 2. Mỗi file < 200 (`scan.go` ~166, `members.go` ~32, `members_test.go` ~85, `scan_test.go` ~75, `world_test.go` ~120).

**Step 6: Wiring + diễn tập**

`apps/core/resync_command.go`: dòng `deps := resync.Deps{Rooms: st, Pages: st, Edits: st, Reactions: st.Reactions(), Pins: st.Pins(), Pub: c.js}` → `deps := resync.Deps{Rooms: st, Pages: st, Edits: st, Reactions: st.Reactions(), Pins: st.Pins(), Members: st.MemberActions(), Pub: c.js}`.

`apps/core/resync_integration_test.go` (`TestRealInfraResyncDrillRepublishesWritesTheReaderMissed`), ba chỗ:

1. Ngay sau khối `if err := itPins(st).Append(t.Context(), pin); err != nil { … }`, thêm:

```go
	member := domain.MemberAction{
		Room: room, MV: 2, Tenant: itTenant, Op: domain.MemberOpAdd,
		Changes: []domain.MemberChange{{User: "migrated", Role: domain.RoleMember, ReadSeq: 3}}, By: "migrator", At: marked, Count: 3,
	}
	if err := st.MemberActions().Append(t.Context(), member); err != nil {
		t.Fatalf("append a member fact the reader missed: %v", err)
	}
```

   và trong dòng `want = append(want, pbconv.ReactionEventID(…), pbconv.ReactionCountsEventID(…), pbconv.PinEventID(room, 1))` thêm `pbconv.MemberEventID(room, 2)` vào cuối danh sách đối số.
2. Chuỗi báo cáo mong đợi thành `"resync rooms=1 room_records=1 message_records=3 edit_records=1 reaction_records=1 pin_records=1 member_records=2 dry_run=false"` và thông điệp lỗi `"resync output = %q, want one room, its room record, three message, one edit, one reaction, one pin and two member records (the room's creation and the missed add)"`.
3. Cuối hàm (sau kiểm `pins after resync`), thêm:

```go
	head, err := st.Get(t.Context(), room)
	if err != nil || head.MemberVersion != 2 || head.MemberCount != 3 {
		t.Fatalf("room after resync = %+v, %v; want the missed member fact projected at member version 2 with 3 members", head, err)
	}
	if m, err := st.Member(t.Context(), room, "migrated"); err != nil || m.MV != 2 || m.Read.Seq != 3 {
		t.Fatalf("member migrated after resync = %+v, %v; want active at member version 2 reading from seq 3", m, err)
	}
```

`createRoom` tạo room `alice` (owner) + `bob` qua gRPC, nên fact mv 1 (2 member) ghi trong khoảng resync và fact tay ở mv 2 nâng lên 3 member. Reader tắt (`RECONCILE_ENABLED=false`) và không có lệnh member nào, nên fact mv 2 chỉ được áp khi resync đẩy record; `-m2` tới live **sau** projection vì `member_event` (delay `RECONCILE_DELAY`) chạy sau `member_projection` (delay 0) trong cùng lô. Room tạo bằng gRPC đã có fact mv 1 nên `member_records=2`; bản room `{room}-m1` và các bản user của nó đã có từ fast path `CreateRoom` (Task 11), worker gửi lại là bản trùng.

**Step 7: Chạy**

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/ ./apps/core/internal/resync/..."
make infra-up && make itest
```

Expected: PASS (itest skip ở lệnh đầu); `make itest` mọi package `ok`, gồm `TestRealInfraResyncDrillRepublishesWritesTheReaderMissed` (log `live events [… <room>-p1 <room>-m2] arrived …`). `wc -l apps/core/resync_integration_test.go` → ~105.

**Step 8: INDEXES.csv + commit**

```bash
python3 bin/indexes_edit.py apps/core/internal/resync purpose "~" "PinInserted for pin_actions facts (Seq = pv; ErrPinPageFull)" "PinInserted for pin_actions facts (Seq = pv; ErrPinPageFull), MemberInserted for member_actions facts (Seq = mv; ErrMemberPageFull)"
python3 bin/indexes_edit.py apps/core/internal/resync key_symbols + ";Members;ErrMemberPageFull"
python3 bin/indexes_edit.py apps/core/internal/resync decisions + ";D106"
python3 bin/indexes_edit.py apps/core tests "~" "itest (resync drill republishes messages, an edit, a reaction and a pin the reader missed)" "itest (resync drill republishes messages, an edit, a reaction, a pin and a member fact the reader missed, and the workers project the member fact)"
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
make fmt-check && make vet && make lint
git add apps/core/internal/resync/members.go apps/core/internal/resync/members_test.go
git commit -m "feat(resync): replay member facts of the lost range" -- apps/core/internal/resync/ apps/core/resync_command.go apps/core/resync_integration_test.go INDEXES.csv
git show --stat HEAD
```

Expected: các lệnh helper in `ok`; `{7}`; sạch; commit có 9 file (`members.go`, `members_test.go`, `scan.go`, `scan_test.go`, `world_test.go`, `edits_test.go`, `reactions_pins_test.go`, `resync_command.go`, `resync_integration_test.go`) + `INDEXES.csv`.

Task trung bình: controller kiểm nhanh (thứ tự quét, `scanByTime` dùng chung, các test cũ chỉ đổi đúng phần `member_records`), không reviewer.

---

### Task 17: Route + corecli + e2e member và vị trí đọc

Ba phần, hai commit: (1) `tools/internal/route` có sáu lệnh mới; (2) `tools/corecli` có sáu lệnh tay, `watch -user`, và bước e2e `members` (phase 5 của `scripts/e2e.sh`) kiểm cả reply lẫn event trên subject room và subject user.

**Hợp đồng chỉnh (Part C):** hợp đồng chung ghi `remove-member (-room -user)` và `set-role (-room -user -role …)`, nhưng `-user` đã là cờ người gọi của mọi lệnh corecli (`addOptions`). Hai lệnh này dùng `-target` cho user đích. `watch` không dùng `addOptions` nên giữ `-user` đúng hợp đồng.

Mọi lệnh member và đọc là trạng thái mong muốn (thêm người đã có, xoá người đã đi, rời lần nữa, đặt role đang có, `MarkRead` chỉ nâng, `MarkUnread` chỉ hạ), nên route gửi qua `inRoom` và retry như các lệnh idempotent khác (`Unavailable`, `ResourceExhausted`, `DeadlineExceeded`, `Aborted`). Retry sau một lần đã thành công nhưng mất reply chỉ thấy kết quả no-op (`added` rỗng, `changed` false): chấp nhận cho lệnh trạng thái mong muốn.

**Kịch bản e2e phase 5 (`corecli e2e members`)**, chạy trên room e2e của phase 1 (chỉ có `e2e-user` là owner, mv 1), sau phase 4. N = seq cuối đã ack (80 với mặc định). Bước tự kết nối NATS (`-nats`, `-live-root`) và subscribe trước khi làm gì: subject room `live.e2e.room.{room}.>` và subject user của `e2e-user`, `e2e-bob`, `e2e-carol` (`live.e2e.user.{u}.>`).

| # | Hành động (người gọi) | Reply mong đợi | Event mong đợi (id → subject) |
|---|---|---|---|
| 0 | `e2e-user` tạo DM với `e2e-bob`; trên DM: thêm `e2e-carol`, xoá `e2e-bob`, rời, đổi role `e2e-bob` | cả bốn `FAILED_PRECONDITION` | — |
| 1 | `e2e-user` thêm `e2e-bob`, `e2e-carol`; lặp lại | `{mv 2, count 3, added [bob, carol]}`; lặp: `{2, 3, added []}` | `{room}-m2` → room; `{room}-m2-e2e-bob`, `{room}-m2-e2e-carol` → user của từng người (`member_added`) |
| 2 | `e2e-bob` đọc toàn bộ lịch sử (như `e2e check`); `MarkRead(0)` | lịch sử đủ N tin; `{read_seq N, read_version 1}` (vào ở tin mới nhất: không còn unread, không đổi) | — |
| 3 | `e2e-user` đặt `e2e-bob` = admin; lặp lại | `{3, 3, changed, previous member}`; lặp: `{3, 3}` | `{room}-m3` → room, `{room}-m3-e2e-bob` → user bob (`member_role_changed`, role admin) |
| 4 | `e2e-bob` (admin) xoá `e2e-user` (owner) | `PERMISSION_DENIED` | — |
| 5 | `e2e-bob` xoá `e2e-carol`; lặp lại | `{4, 2, changed}`; lặp: `{4, 2}` | `{room}-m4` → room, `{room}-m4-e2e-carol` → user carol (`member_removed`, reason removed) |
| 6 | `e2e-carol` gửi tin, đọc lịch sử, `MarkRead` | cả ba `PERMISSION_DENIED` | — |
| 7 | `e2e-bob` `MarkUnread(N)`, rồi `MarkRead(0)` | `{N−1, 2}`, rồi `{N, 3}` | `{room}-rd-e2e-bob-v2` (gửi ngay) và `{room}-rd-e2e-bob-v3` (phần đuôi sau `READ_RECEIPT_WINDOW`) → room (`read_updated`; room 2 member ≤ 100) |
| 8 | `e2e-user` (owner cuối) rời; lặp lại | `{5, 1, changed, new_owner e2e-bob}`; lặp: `{5, 1}`; sau đó `e2e-user` đọc lịch sử → `PERMISSION_DENIED` | `{room}-m5` → room, `{room}-m5-e2e-user` → user e2e-user, `{room}-m5-e2e-bob` → user bob (`member_removed`, reason left, new_owner e2e-bob) |
| 9 | `e2e-bob` (owner mới) thêm lại `e2e-user`, rồi đặt `e2e-user` = owner (chỉ owner đổi role được); lặp từng lệnh | `{6, 2, added [e2e-user]}`/`{6, 2}`; `{7, 2, changed, previous member}`/`{7, 2}` | `{room}-m6` → room, `{room}-m6-e2e-user` → user; `{room}-m7` → room, `{room}-m7-e2e-user` → user |
| 10 | `e2e-user` `MarkRead(0)` | `{N, 2}` (vào lại nâng vị trí đọc lên tin mới nhất; v 1 lúc tạo room, 2 lúc vào lại) | — |

Sau bảng: chờ đủ 16 event (đúng id, kind, subject và payload; bản trùng và event khác id bỏ qua) trong `-wait` (45s). Rồi phase 5 check (`e2e check` như cũ, người gọi `e2e-user` đã vào lại nên đọc được toàn bộ lịch sử). Watcher room của phase 1 vẫn ghi `events.jsonl`; `EventOf` giờ giải mã event member/đọc nhưng `CheckEvents`, `CheckChangeEvents`, `CheckMarkEvents` bỏ qua các kind đó.

Ghi chú:
- Bước 2 chứng minh "người mới thấy toàn bộ lịch sử, tin cũ coi như đã đọc" (D104): fact thêm mang `ReadSeq = N` nên `rs = {N, 1}`; `MarkRead(0)` không đổi gì và trả đúng vị trí đó. Unread = 0 tính được ở M3 từ `rs`.
- Bước 6 dùng `e2e-carol` chưa từng gửi (không có trong cache actor); đường cache của actor có itest riêng (Task 18).
- Bước 8–10 chứng minh owner cuối rời → admin vào sớm nhất thành owner, người rời mất quyền đọc, thêm lại thì đọc lại được và vị trí đọc nâng lên tin mới nhất.

Trước khi bắt đầu, kiểm tên proto của Part A (chỉ đọc):

```bash
grep -n "AddMembers\|RemoveMember\|LeaveRoom\|ChangeMemberRole\|MarkRead\|MarkUnread" pkg/pb/chatim/v1/core_grpc.pb.go | grep "func (c \*coreServiceClient)"
grep -n "MemberRole_MEMBER_ROLE_\(OWNER\|ADMIN\|MEMBER\)\b\|MemberRemovedReason_MEMBER_REMOVED_REASON_LEFT\b" pkg/pb/chatim/v1/*.pb.go | head
grep -n "func (x \*Event) GetRecipient\|func (x \*Event) GetMemberAdded\|func (x \*Event) GetReadUpdated" pkg/pb/chatim/v1/events.pb.go
git diff --quiet -- INDEXES.csv; echo "INDEXES dirty=$?"
```

Expected: sáu method client, ba hằng role, hằng reason, ba getter; `dirty=0`. Khác → dừng và báo cáo.

**Files:**
- Create: `tools/internal/route/members.go`
- Create: `tools/internal/route/fakes_members_test.go`
- Modify: `tools/internal/route/changes_test.go`
- Create: `tools/corecli/internal/e2e/members.go`
- Create: `tools/corecli/internal/e2e/members_check.go`
- Create: `tools/corecli/internal/e2e/members_test.go`
- Modify: `tools/corecli/internal/e2e/events.go`
- Create: `tools/corecli/cmd_members.go`
- Modify: `tools/corecli/cmd_watch.go`, `tools/corecli/main.go`, `tools/corecli/cmd_e2e.go`
- Create: `tools/corecli/e2e_members.go`, `tools/corecli/e2e_member_steps.go`, `tools/corecli/e2e_member_calls.go`, `tools/corecli/e2e_member_live.go`
- Modify: `scripts/e2e.sh`
- Modify: `INDEXES.csv`

**Step 1: Test route**

`tools/internal/route/changes_test.go`, map `changeCalls` thêm sáu mục (sau `"unpin"`):

```go
	"add-members": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.AddMembers(ctx, &chatimv1.AddMembersRequest{RoomId: room, Users: []string{"bob", "carol"}}))
	},
	"remove-member": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.RemoveMember(ctx, &chatimv1.RemoveMemberRequest{RoomId: room, User: "carol"}))
	},
	"leave": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.LeaveRoom(ctx, &chatimv1.LeaveRoomRequest{RoomId: room}))
	},
	"set-role": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.ChangeMemberRole(ctx, &chatimv1.ChangeMemberRoleRequest{RoomId: room, User: "bob", Role: chatimv1.MemberRole_MEMBER_ROLE_ADMIN}))
	},
	"read": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.MarkRead(ctx, &chatimv1.MarkReadRequest{RoomId: room, Seq: 9}))
	},
	"unread": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.MarkUnread(ctx, &chatimv1.MarkUnreadRequest{RoomId: room, Seq: 9}))
	},
```

map `changeReplies` thêm:

```go
	"add-members":   &chatimv1.AddMembersResponse{MemberVersion: 2, MemberCount: 3, Added: []string{"bob", "carol"}},
	"remove-member": &chatimv1.RemoveMemberResponse{MemberVersion: 3, MemberCount: 2, Changed: true},
	"leave":         &chatimv1.LeaveRoomResponse{MemberVersion: 4, MemberCount: 1, Changed: true, NewOwner: "bob"},
	"set-role":      &chatimv1.ChangeMemberRoleResponse{MemberVersion: 5, MemberCount: 2, Changed: true, PreviousRole: chatimv1.MemberRole_MEMBER_ROLE_MEMBER},
	"read":          &chatimv1.MarkReadResponse{ReadSeq: 9, ReadVersion: 2},
	"unread":        &chatimv1.MarkUnreadResponse{ReadSeq: 8, ReadVersion: 3},
```

(gofmt căn lại cột của cả map.) Hai test cũ (`TestChangeCallsRouteByRoomAndRetryAttemptTimeouts`, `TestChangeCallsKeepConflictsAndBadRoomIDsLocal`) chạy mọi mục của map, nên phủ cả sáu lệnh mới: route theo room, retry khi attempt timeout, room id sai bị từ chối tại chỗ.

`tools/internal/route/fakes_members_test.go`:

```go
package route_test

import (
	"context"

	"google.golang.org/grpc"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (f *fakeCore) AddMembers(ctx context.Context, in *chatimv1.AddMembersRequest, _ ...grpc.CallOption) (*chatimv1.AddMembersResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.AddMembersResponse{MemberVersion: 2, MemberCount: 3, Added: in.GetUsers()}, nil
}

func (f *fakeCore) RemoveMember(ctx context.Context, _ *chatimv1.RemoveMemberRequest, _ ...grpc.CallOption) (*chatimv1.RemoveMemberResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.RemoveMemberResponse{MemberVersion: 3, MemberCount: 2, Changed: true}, nil
}

func (f *fakeCore) LeaveRoom(ctx context.Context, _ *chatimv1.LeaveRoomRequest, _ ...grpc.CallOption) (*chatimv1.LeaveRoomResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.LeaveRoomResponse{MemberVersion: 4, MemberCount: 1, Changed: true, NewOwner: "bob"}, nil
}

func (f *fakeCore) ChangeMemberRole(ctx context.Context, _ *chatimv1.ChangeMemberRoleRequest, _ ...grpc.CallOption) (*chatimv1.ChangeMemberRoleResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.ChangeMemberRoleResponse{MemberVersion: 5, MemberCount: 2, Changed: true, PreviousRole: chatimv1.MemberRole_MEMBER_ROLE_MEMBER}, nil
}

func (f *fakeCore) MarkRead(ctx context.Context, in *chatimv1.MarkReadRequest, _ ...grpc.CallOption) (*chatimv1.MarkReadResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.MarkReadResponse{ReadSeq: in.GetSeq(), ReadVersion: 2}, nil
}

func (f *fakeCore) MarkUnread(ctx context.Context, in *chatimv1.MarkUnreadRequest, _ ...grpc.CallOption) (*chatimv1.MarkUnreadResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.MarkUnreadResponse{ReadSeq: in.GetSeq() - 1, ReadVersion: 3}, nil
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./tools/internal/route/..."`
Expected: FAIL biên dịch: `c.AddMembers undefined (type *route.Client has no field or method AddMembers)` (và `RemoveMember`, `LeaveRoom`, `ChangeMemberRole`, `MarkRead`, `MarkUnread`).

**Step 3: Code route**

`tools/internal/route/members.go`:

```go
package route

import (
	"context"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (c *Client) AddMembers(ctx context.Context, req *chatimv1.AddMembersRequest) (*chatimv1.AddMembersResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.AddMembersResponse, error) {
		return api.AddMembers(ctx, req)
	})
}

func (c *Client) RemoveMember(ctx context.Context, req *chatimv1.RemoveMemberRequest) (*chatimv1.RemoveMemberResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.RemoveMemberResponse, error) {
		return api.RemoveMember(ctx, req)
	})
}

func (c *Client) LeaveRoom(ctx context.Context, req *chatimv1.LeaveRoomRequest) (*chatimv1.LeaveRoomResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.LeaveRoomResponse, error) {
		return api.LeaveRoom(ctx, req)
	})
}

func (c *Client) ChangeMemberRole(ctx context.Context, req *chatimv1.ChangeMemberRoleRequest) (*chatimv1.ChangeMemberRoleResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.ChangeMemberRoleResponse, error) {
		return api.ChangeMemberRole(ctx, req)
	})
}

func (c *Client) MarkRead(ctx context.Context, req *chatimv1.MarkReadRequest) (*chatimv1.MarkReadResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.MarkReadResponse, error) {
		return api.MarkRead(ctx, req)
	})
}

func (c *Client) MarkUnread(ctx context.Context, req *chatimv1.MarkUnreadRequest) (*chatimv1.MarkUnreadResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.MarkUnreadResponse, error) {
		return api.MarkUnread(ctx, req)
	})
}
```

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./tools/internal/route/..."`
Expected: PASS (`TestChangeCallsRouteByRoomAndRetryAttemptTimeouts` với 14 lệnh, 28 lần hỏi locator). `wc -l tools/internal/route/changes_test.go tools/internal/route/members.go` → ~145 và ~45.

**Step 5: Commit route**

```bash
python3 bin/indexes_edit.py tools/internal/route purpose "~" "ReactMessage, PinMessage, UnpinMessage)" "ReactMessage, PinMessage, UnpinMessage, AddMembers, RemoveMember, LeaveRoom, ChangeMemberRole, MarkRead, MarkUnread)"
python3 bin/indexes_edit.py tools/internal/route purpose "~" "(cid, base_version, desired state, upsert, \$max or a read)" "(cid, base_version, desired state, upsert, \$max, a read position that only rises or only falls, or a read)"
python3 bin/indexes_edit.py tools/internal/route key_symbols + ";Client.AddMembers;Client.RemoveMember;Client.LeaveRoom;Client.ChangeMemberRole;Client.MarkRead;Client.MarkUnread"
python3 bin/indexes_edit.py tools/internal/route decisions + ";D99;D104"
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
make fmt-check && make vet && make lint
git add tools/internal/route/members.go tools/internal/route/fakes_members_test.go
git commit -m "feat(route): route member and read position calls by room" -- tools/internal/route/ INDEXES.csv
```

Expected: `ok` từng lệnh helper, `{7}`, sạch; commit 3 file + `INDEXES.csv`.

**Step 6: Test package e2e**

`tools/corecli/internal/e2e/members_test.go`:

```go
package e2e_test

import (
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

var scope = e2e.Scope{Root: "live", Tenant: "e2e", Room: "42"}

func TestMemberIDsAndSubjects(t *testing.T) {
	got := []string{
		e2e.MemberEventID("42", 3), e2e.MemberUserEventID("42", 3, "bob"), e2e.ReadEventID("42", "bob", 2),
		e2e.RoomSubject("live", "e2e", "42"), e2e.UserSubject("live", "e2e", "bob"),
		e2e.RoleName(chatimv1.MemberRole_MEMBER_ROLE_OWNER), e2e.RoleName(chatimv1.MemberRole_MEMBER_ROLE_UNSPECIFIED),
		e2e.JoinUsers([]string{"carol", "bob"}),
	}
	want := []string{"42-m3", "42-m3-bob", "42-rd-bob-v2", "live.e2e.room.42", "live.e2e.user.bob", "owner", "", "bob,carol"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("value %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestEventOfReadsMemberAndReadEvents(t *testing.T) {
	member := func(user string, role chatimv1.MemberRole) *chatimv1.RoomMember { return &chatimv1.RoomMember{User: user, Role: role} }
	cases := []struct {
		subject string
		ev      *chatimv1.Event
		want    e2e.Event
	}{
		{
			"live.e2e.room.42.evt.member_added",
			&chatimv1.Event{Id: "42-m2", RoomId: "42", Payload: &chatimv1.Event_MemberAdded{MemberAdded: &chatimv1.MemberAdded{
				Members: []*chatimv1.RoomMember{member("carol", chatimv1.MemberRole_MEMBER_ROLE_MEMBER), member("bob", chatimv1.MemberRole_MEMBER_ROLE_MEMBER)}, MemberVersion: 2, MemberCount: 3,
			}}},
			scope.Added(2, 3, "", "bob", "carol"),
		},
		{
			"live.e2e.user.bob.evt.member_added",
			&chatimv1.Event{Id: "42-m2-bob", RoomId: "42", Recipient: "bob", Payload: &chatimv1.Event_MemberAdded{MemberAdded: &chatimv1.MemberAdded{
				Members: []*chatimv1.RoomMember{member("bob", chatimv1.MemberRole_MEMBER_ROLE_MEMBER)}, MemberVersion: 2, MemberCount: 3,
			}}},
			scope.Added(2, 3, "bob", "bob"),
		},
		{
			"live.e2e.user.bob.evt.member_removed",
			&chatimv1.Event{Id: "42-m5-bob", RoomId: "42", Recipient: "bob", Payload: &chatimv1.Event_MemberRemoved{MemberRemoved: &chatimv1.MemberRemoved{
				User: "e2e-user", Reason: chatimv1.MemberRemovedReason_MEMBER_REMOVED_REASON_LEFT, NewOwner: "bob", MemberVersion: 5, MemberCount: 1,
			}}},
			scope.Removed(5, 1, "e2e-user", e2e.ReasonLeft, "bob", "bob"),
		},
		{
			"live.e2e.room.42.evt.member_role_changed",
			&chatimv1.Event{Id: "42-m3", RoomId: "42", Payload: &chatimv1.Event_MemberRoleChanged{MemberRoleChanged: &chatimv1.MemberRoleChanged{
				User: "bob", Role: chatimv1.MemberRole_MEMBER_ROLE_ADMIN, PreviousRole: chatimv1.MemberRole_MEMBER_ROLE_MEMBER, MemberVersion: 3,
			}}},
			scope.RoleChanged(3, "bob", "admin", ""),
		},
		{
			"live.e2e.room.42.evt.read_updated",
			&chatimv1.Event{Id: "42-rd-bob-v2", RoomId: "42", Payload: &chatimv1.Event_ReadUpdated{ReadUpdated: &chatimv1.ReadUpdated{User: "bob", Seq: 79, Version: 2}}},
			scope.ReadUpdated("bob", 79, 2),
		},
	}
	for _, c := range cases {
		got, ok := e2e.EventOf(c.subject, c.ev)
		if !ok || got != c.want || got.IsCreated() || got.IsChange() || got.IsMark() {
			t.Fatalf("EventOf(%s) = %+v, %v; want %+v and no message kind", c.ev.GetId(), got, ok, c.want)
		}
	}
}

func TestCheckLiveFindsEachWantedEventByID(t *testing.T) {
	added, removed := scope.Added(2, 3, "", "bob"), scope.Removed(4, 2, "carol", e2e.ReasonRemoved, "", "carol")
	want := []e2e.Event{added, removed}
	other := scope.ReadUpdated("bob", 7, 9)
	missing, err := e2e.CheckLive(want, []e2e.Event{added, other, added})
	if err != nil || len(missing) != 1 || missing[0] != removed.ID {
		t.Fatalf("CheckLive = %v, %v; want only %s missing (duplicates and other ids ignored)", missing, err, removed.ID)
	}
	if missing, err := e2e.CheckLive(want, []e2e.Event{removed, added}); err != nil || len(missing) != 0 {
		t.Fatalf("CheckLive = %v, %v; want nothing missing", missing, err)
	}
	wrong := removed
	wrong.Text = e2e.ReasonLeft
	if _, err := e2e.CheckLive(want, []e2e.Event{added, wrong}); err == nil {
		t.Fatal("CheckLive accepted a wanted id with the wrong payload")
	}
}

func TestCheckMemberReply(t *testing.T) {
	want := e2e.MemberReply{Version: 5, Count: 1, Changed: true, NewOwner: "bob"}
	if err := e2e.CheckMemberReply("leave", want, want); err != nil {
		t.Fatalf("equal replies: %v", err)
	}
	if err := e2e.CheckMemberReply("leave", e2e.MemberReply{Version: 5, Count: 1}, want); err == nil {
		t.Fatal("CheckMemberReply accepted a no-op where a change was wanted")
	}
}
```

**Step 7: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./tools/corecli/internal/e2e/..."`
Expected: FAIL biên dịch: `undefined: e2e.Scope`, `undefined: e2e.MemberEventID`, `undefined: e2e.CheckLive`, `undefined: e2e.MemberReply`, `undefined: e2e.ReasonLeft` (và các hàm khác của file).

**Step 8: Code package e2e**

`tools/corecli/internal/e2e/events.go`, hai chỗ:
- `type Event` (thêm bốn field trước `Subject`; gofmt căn tag):

```go
type Event struct {
	Kind          string `json:"kind,omitempty"`
	Room          string `json:"room"`
	ID            string `json:"id"`
	Seq           uint64 `json:"seq"`
	CID           string `json:"cid"`
	User          string `json:"user,omitempty"`
	Version       uint32 `json:"version,omitempty"`
	Text          string `json:"text,omitempty"`
	MemberVersion uint64 `json:"member_version,omitempty"`
	Count         int32  `json:"count,omitempty"`
	NewOwner      string `json:"new_owner,omitempty"`
	ReadVersion   uint64 `json:"read_version,omitempty"`
	Subject       string `json:"subject,omitempty"`
}
```

- Cuối `EventOf`, dòng `return markOf(subject, ev)` →

```go
	if out, ok := markOf(subject, ev); ok {
		return out, true
	}
	return memberOf(subject, ev)
```

`tools/corecli/internal/e2e/members.go`:

```go
package e2e

import (
	"slices"
	"strconv"
	"strings"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	KindMemberAdded   = "member_added"
	KindMemberRemoved = "member_removed"
	KindRoleChanged   = "member_role_changed"
	KindReadUpdated   = "read_updated"

	ReasonRemoved = "removed"
	ReasonLeft    = "left"
)

type Scope struct {
	Root   string
	Tenant string
	Room   string
}

func RoomSubject(root, tenant, room string) string { return root + "." + tenant + ".room." + room }

func UserSubject(root, tenant, user string) string { return root + "." + tenant + ".user." + user }

func MemberEventID(room string, mv uint64) string { return room + "-m" + strconv.FormatUint(mv, 10) }

func MemberUserEventID(room string, mv uint64, user string) string {
	return MemberEventID(room, mv) + "-" + user
}

func ReadEventID(room, user string, version uint64) string {
	return room + "-rd-" + user + "-v" + strconv.FormatUint(version, 10)
}

func RoleName(r chatimv1.MemberRole) string {
	if r == chatimv1.MemberRole_MEMBER_ROLE_UNSPECIFIED {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(r.String(), "MEMBER_ROLE_"))
}

func JoinUsers(users []string) string { return strings.Join(slices.Sorted(slices.Values(users)), ",") }

func (s Scope) Added(mv uint64, count int32, to string, users ...string) Event {
	ev := s.member(KindMemberAdded, mv, to)
	ev.User, ev.Count = JoinUsers(users), count
	return ev
}

func (s Scope) Removed(mv uint64, count int32, user, reason, newOwner, to string) Event {
	ev := s.member(KindMemberRemoved, mv, to)
	ev.User, ev.Text, ev.NewOwner, ev.Count = user, reason, newOwner, count
	return ev
}

func (s Scope) RoleChanged(mv uint64, user, role, to string) Event {
	ev := s.member(KindRoleChanged, mv, to)
	ev.User, ev.Text = user, role
	return ev
}

func (s Scope) ReadUpdated(user string, seq, version uint64) Event {
	return Event{
		Kind: KindReadUpdated, Room: s.Room, ID: ReadEventID(s.Room, user, version), Seq: seq, User: user, ReadVersion: version,
		Subject: RoomSubject(s.Root, s.Tenant, s.Room) + ".evt." + KindReadUpdated,
	}
}

func (s Scope) member(kind string, mv uint64, to string) Event {
	ev := Event{Kind: kind, Room: s.Room, ID: MemberEventID(s.Room, mv), MemberVersion: mv, Subject: RoomSubject(s.Root, s.Tenant, s.Room) + ".evt." + kind}
	if to != "" {
		ev.ID, ev.Subject = MemberUserEventID(s.Room, mv, to), UserSubject(s.Root, s.Tenant, to)+".evt."+kind
	}
	return ev
}

func memberOf(subject string, ev *chatimv1.Event) (Event, bool) {
	out := Event{Room: ev.GetRoomId(), ID: ev.GetId(), Subject: subject}
	switch {
	case ev.GetMemberAdded() != nil:
		a := ev.GetMemberAdded()
		users := make([]string, len(a.GetMembers()))
		for i, m := range a.GetMembers() {
			users[i] = m.GetUser()
		}
		out.Kind, out.User, out.MemberVersion, out.Count = KindMemberAdded, JoinUsers(users), a.GetMemberVersion(), a.GetMemberCount()
	case ev.GetMemberRemoved() != nil:
		r := ev.GetMemberRemoved()
		out.Kind, out.User, out.Text, out.NewOwner = KindMemberRemoved, r.GetUser(), reasonName(r.GetReason()), r.GetNewOwner()
		out.MemberVersion, out.Count = r.GetMemberVersion(), r.GetMemberCount()
	case ev.GetMemberRoleChanged() != nil:
		c := ev.GetMemberRoleChanged()
		out.Kind, out.User, out.Text, out.MemberVersion = KindRoleChanged, c.GetUser(), RoleName(c.GetRole()), c.GetMemberVersion()
	case ev.GetReadUpdated() != nil:
		r := ev.GetReadUpdated()
		out.Kind, out.User, out.Seq, out.ReadVersion = KindReadUpdated, r.GetUser(), r.GetSeq(), r.GetVersion()
	default:
		return Event{}, false
	}
	return out, true
}

func reasonName(r chatimv1.MemberRemovedReason) string {
	return strings.ToLower(strings.TrimPrefix(r.String(), "MEMBER_REMOVED_REASON_"))
}
```

`tools/corecli/internal/e2e/members_check.go`:

```go
package e2e

import (
	"fmt"
	"maps"
	"slices"
)

type MemberReply struct {
	Version  uint64
	Count    int32
	Changed  bool
	Users    string
	NewOwner string
	Prev     string
}

func CheckMemberReply(what string, got, want MemberReply) error {
	if got != want {
		return fmt.Errorf("%s returned %+v, want %+v", what, got, want)
	}
	return nil
}

func CheckLive(want, got []Event) ([]string, error) {
	byID := make(map[string]Event, len(want))
	for _, w := range want {
		byID[w.ID] = w
	}
	seen := make(map[string]bool, len(want))
	for _, ev := range got {
		w, ok := byID[ev.ID]
		if !ok {
			continue
		}
		if ev != w {
			return nil, fmt.Errorf("live event %s = %+v, want %+v", ev.ID, ev, w)
		}
		seen[ev.ID] = true
	}
	var missing []string
	for _, id := range slices.Sorted(maps.Keys(byID)) {
		if !seen[id] {
			missing = append(missing, id)
		}
	}
	return missing, nil
}
```

**Step 9: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./tools/corecli/..."`
Expected: PASS cho `tools/corecli/internal/e2e` (gồm test cũ `TestEventOfKeepsMessageEventsOnly`, `TestEventOfReadsTheCreatedMessage`; so sánh struct `Event` vẫn đúng vì field mới bằng 0 ở event cũ); `tools/corecli` (main) vẫn biên dịch. `wc -l tools/corecli/internal/e2e/*.go` → mỗi file < 200 (`members.go` ~120, `members_test.go` ~120, `members_check.go` ~50, `events.go` ~80).

**Step 10: Code corecli**

`tools/corecli/cmd_members.go`:

```go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

var (
	memberRoles = map[string]chatimv1.MemberRole{
		"owner":  chatimv1.MemberRole_MEMBER_ROLE_OWNER,
		"admin":  chatimv1.MemberRole_MEMBER_ROLE_ADMIN,
		"member": chatimv1.MemberRole_MEMBER_ROLE_MEMBER,
	}
	errTargetRequired = errors.New("-target is required")
	errUsersRequired  = errors.New("-users is required")
)

type roomCall func(ctx context.Context, cl *route.Client) (proto.Message, route.Stats, error)

func replyOf[T proto.Message](resp T, st route.Stats, err error) (proto.Message, route.Stats, error) {
	return resp, st, err
}

func roomFlags(name string) (*flag.FlagSet, *options, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	return fs, addOptions(fs), fs.String("room", "", "room id")
}

func runRoomCall(ctx context.Context, o *options, name, room string, do roomCall) error {
	if room == "" {
		return errRoomRequired
	}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
		resp, st, err := do(ctx, s.client)
		if err != nil {
			return err
		}
		report(name, st)
		return printJSON(resp)
	})
}

func addMembersCmd(ctx context.Context, args []string) error {
	fs, o, room := roomFlags("add-members")
	users := fs.String("users", "", "comma-separated users to add")
	if err := fs.Parse(args); err != nil {
		return err
	}
	list := splitList(*users)
	if len(list) == 0 {
		return errUsersRequired
	}
	req := &chatimv1.AddMembersRequest{RoomId: *room, Users: list}
	return runRoomCall(ctx, o, "add-members", *room, func(ctx context.Context, cl *route.Client) (proto.Message, route.Stats, error) {
		return replyOf(cl.AddMembers(ctx, req))
	})
}

func removeMemberCmd(ctx context.Context, args []string) error {
	fs, o, room := roomFlags("remove-member")
	target := fs.String("target", "", "user to remove; use leave to remove yourself")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *target == "" {
		return errTargetRequired
	}
	req := &chatimv1.RemoveMemberRequest{RoomId: *room, User: *target}
	return runRoomCall(ctx, o, "remove-member", *room, func(ctx context.Context, cl *route.Client) (proto.Message, route.Stats, error) {
		return replyOf(cl.RemoveMember(ctx, req))
	})
}

func leaveCmd(ctx context.Context, args []string) error {
	fs, o, room := roomFlags("leave")
	if err := fs.Parse(args); err != nil {
		return err
	}
	req := &chatimv1.LeaveRoomRequest{RoomId: *room}
	return runRoomCall(ctx, o, "leave", *room, func(ctx context.Context, cl *route.Client) (proto.Message, route.Stats, error) {
		return replyOf(cl.LeaveRoom(ctx, req))
	})
}

func setRoleCmd(ctx context.Context, args []string) error {
	fs, o, room := roomFlags("set-role")
	target := fs.String("target", "", "member whose role changes")
	role := fs.String("role", "", "owner, admin or member")
	if err := fs.Parse(args); err != nil {
		return err
	}
	r, ok := memberRoles[*role]
	switch {
	case *target == "":
		return errTargetRequired
	case !ok:
		return fmt.Errorf("-role %q: want owner, admin or member", *role)
	}
	req := &chatimv1.ChangeMemberRoleRequest{RoomId: *room, User: *target, Role: r}
	return runRoomCall(ctx, o, "set-role", *room, func(ctx context.Context, cl *route.Client) (proto.Message, route.Stats, error) {
		return replyOf(cl.ChangeMemberRole(ctx, req))
	})
}

func readCmd(ctx context.Context, args []string) error { return readPositionCmd(ctx, "read", args) }

func unreadCmd(ctx context.Context, args []string) error { return readPositionCmd(ctx, "unread", args) }

func readPositionCmd(ctx context.Context, name string, args []string) error {
	fs, o, room := roomFlags(name)
	seq := fs.Uint64("seq", 0, "read: last read seq, 0 for the latest message; unread: the first seq to show as unread")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if name == "unread" && *seq == 0 {
		return errSeqRequired
	}
	return runRoomCall(ctx, o, name, *room, func(ctx context.Context, cl *route.Client) (proto.Message, route.Stats, error) {
		if name == "read" {
			return replyOf(cl.MarkRead(ctx, &chatimv1.MarkReadRequest{RoomId: *room, Seq: *seq}))
		}
		return replyOf(cl.MarkUnread(ctx, &chatimv1.MarkUnreadRequest{RoomId: *room, Seq: *seq}))
	})
}
```

`tools/corecli/cmd_watch.go`, trong `watchCmd`:
- Cờ: `tenant := fs.String("tenant", "e2e", "tenant of the room")` → `"tenant of the room or user"`; sau `room := …` thêm `user := fs.String("user", "", "watch this user's own subject (member and read events) instead of a room")`.
- Thay khối từ `if _, err := ids.ParseRoomID(*room); err != nil {` tới hết `return errors.New("-tenant and -live-root must be single subject tokens")` + `}` bằng:

```go
	subject, err := watchSubject(*liveRoot, *tenant, *room, *user)
	if err != nil {
		return err
	}
```

- Xoá dòng `subject := *liveRoot + "." + *tenant + ".room." + *room + ".>"`; dòng `return errors.Join(watch(ctx, *url, subject, w), w.Close())` → `return errors.Join(watch(ctx, *url, subject+".>", w), w.Close())`.
- Thêm cuối file:

```go
func watchSubject(root, tenant, room, user string) (string, error) {
	switch {
	case root == "" || tenant == "":
		return "", errors.New("-tenant and -live-root are required")
	case strings.ContainsAny(root+tenant+user, ".*> "):
		return "", errors.New("-tenant, -user and -live-root must be single subject tokens")
	case (room == "") == (user == ""):
		return "", errors.New("watch exactly one of -room or -user")
	case user != "":
		return e2e.UserSubject(root, tenant, user), nil
	}
	if _, err := ids.ParseRoomID(room); err != nil {
		return "", fmt.Errorf("-room: %w", err)
	}
	return e2e.RoomSubject(root, tenant, room), nil
}
```

(Import của file giữ nguyên: `errors`, `fmt`, `strings`, `ids`, `e2e` vẫn dùng.)

`tools/corecli/main.go`:
- `usage`: thay dòng `  watch              print live events of a room from NATS` bằng các dòng sau, và dòng `  e2e …` thành `  e2e                end-to-end scenario steps: setup, send, change, react-pin, members, check`:

```
  add-members        add users to a group (-users a,b); owners and admins
  remove-member      remove a user from a group (-target); admins remove plain members only
  leave              leave a group; the last owner hands over to the earliest admin, else member
  set-role           set a member's role (-target, -role owner|admin|member); owners only
  read               raise the caller's read position to -seq (0 = the latest message)
  unread             lower the caller's read position to just before -seq
  watch              print live events of a room (-room) or of a user (-user) from NATS
```

- Map `commands` thêm: `"add-members": addMembersCmd, "remove-member": removeMemberCmd, "leave": leaveCmd, "set-role": setRoleCmd, "read": readCmd, "unread": unreadCmd,` (gofmt căn cột).

`tools/corecli/cmd_e2e.go`: `e2eUsage` → `"usage: corecli e2e setup|send|change|react-pin|members|check [flags]"`; map `steps` thêm `"members": e2eMembers`.

`tools/corecli/e2e_members.go`:

```go
package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

var errFewAcks = errors.New("the members phase needs at least two acked messages")

type memberScenario struct {
	cl          *route.Client
	base        context.Context
	st          e2e.State
	scope       e2e.Scope
	peer, other string
	last        uint64
	want        []e2e.Event
}

func (m *memberScenario) as(user string) context.Context {
	return route.WithCaller(m.base, m.st.Tenant, user)
}

func (m *memberScenario) expect(evs ...e2e.Event) { m.want = append(m.want, evs...) }

func (m *memberScenario) subjects() []string {
	out := []string{e2e.RoomSubject(m.scope.Root, m.st.Tenant, m.st.Room) + ".>"}
	for _, u := range []string{m.st.User, m.peer, m.other} {
		out = append(out, e2e.UserSubject(m.scope.Root, m.st.Tenant, u)+".>")
	}
	return out
}

func e2eMembers(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("e2e members", flag.ContinueOnError)
	o := addOptions(fs)
	dir := fs.String("state", "/state", "directory holding the scenario state")
	url := fs.String("nats", cmp.Or(os.Getenv("NATS_URL"), "nats://chatim-nats:4222"), "NATS URL (env NATS_URL)")
	liveRoot := fs.String("live-root", "live", "live subject root, EVT_LIVE_ROOT of the cores")
	peer := fs.String("peer", "e2e-bob", "user added, made admin, then owner when the owner leaves")
	other := fs.String("other", "e2e-carol", "user added then removed by the admin")
	wait := fs.Duration("wait", 45*time.Second, "wait this long for every member and read event")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := e2e.Load(statePath(*dir))
	if err != nil {
		return err
	}
	if len(st.Acks) < 2 {
		return errFewAcks
	}
	o.tenant, o.user = st.Tenant, st.User
	m := &memberScenario{
		base: ctx, st: st, scope: e2e.Scope{Root: *liveRoot, Tenant: st.Tenant, Room: st.Room},
		peer: *peer, other: *other, last: st.Acks[len(st.Acks)-1].Seq,
	}
	live, err := openLive(*url, m.subjects())
	if err != nil {
		return err
	}
	defer live.close()
	err = withSession(ctx, o, func(_ context.Context, s *session) error {
		m.cl = s.client
		return m.run()
	})
	if err != nil {
		return err
	}
	if err := live.await(ctx, m.want, *wait); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "members ok: %s and %s added at member version 2 reading from seq %d, %s admin at 3, %s removed at 4 and denied, read position %d then %d, the owner left to %s at 5 and came back at 6 and 7, direct room members fixed; %d live events on the room and user subjects\n",
		m.peer, m.other, m.last, m.peer, m.other, m.last-1, m.last, m.peer, len(m.want))
	return nil
}

func (m *memberScenario) run() error {
	steps := []struct {
		name string
		do   func() error
	}{
		{"direct room", m.directRoomIsFixed},
		{"add", m.addTwo},
		{"new member reads", m.newMemberReadsAll},
		{"promote", m.promotePeer},
		{"admin removes", m.adminRemovesOther},
		{"removed member", m.removedIsDenied},
		{"read position", m.markUnreadThenRead},
		{"last owner leaves", m.ownerLeaves},
		{"owner comes back", m.ownerComesBack},
	}
	for _, s := range steps {
		if err := s.do(); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	return nil
}
```

`m.base` là `ctx` gốc (chưa có metadata người gọi): `withSession` truyền vào closure một context đã gắn `e2e-user`, nên mỗi lệnh tự gắn người gọi bằng `m.as(user)` (`metadata.AppendToOutgoingContext` gắn hai lần sẽ thành hai giá trị header).

`tools/corecli/e2e_member_steps.go`:

```go
package main

import (
	"fmt"

	"google.golang.org/grpc/codes"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

const memberHistoryPage int32 = 50

func (m *memberScenario) directRoomIsFixed() error {
	owner := m.st.User
	resp, _, err := m.cl.CreateRoom(m.as(owner), &chatimv1.CreateRoomRequest{Type: chatimv1.RoomType_ROOM_TYPE_DM, Members: []string{owner, m.peer}})
	if err != nil {
		return fmt.Errorf("create a direct room: %w", err)
	}
	dm := resp.GetRoom().GetId()
	_, _, addErr := m.cl.AddMembers(m.as(owner), &chatimv1.AddMembersRequest{RoomId: dm, Users: []string{m.other}})
	_, _, removeErr := m.cl.RemoveMember(m.as(owner), &chatimv1.RemoveMemberRequest{RoomId: dm, User: m.peer})
	_, _, leaveErr := m.cl.LeaveRoom(m.as(owner), &chatimv1.LeaveRoomRequest{RoomId: dm})
	_, _, roleErr := m.cl.ChangeMemberRole(m.as(owner), &chatimv1.ChangeMemberRoleRequest{RoomId: dm, User: m.peer, Role: chatimv1.MemberRole_MEMBER_ROLE_ADMIN})
	for what, got := range map[string]error{"add": addErr, "remove": removeErr, "leave": leaveErr, "set role": roleErr} {
		if err := wantCode(what+" on direct room "+dm, got, codes.FailedPrecondition); err != nil {
			return err
		}
	}
	return nil
}

func (m *memberScenario) addTwo() error {
	req := &chatimv1.AddMembersRequest{RoomId: m.st.Room, Users: []string{m.peer, m.other}}
	call := func() (*chatimv1.AddMembersResponse, route.Stats, error) { return m.cl.AddMembers(m.as(m.st.User), req) }
	both := e2e.JoinUsers([]string{m.peer, m.other})
	if err := twice("add-members", call, addReply, e2e.MemberReply{Version: 2, Count: 3, Changed: true, Users: both}, e2e.MemberReply{Version: 2, Count: 3}); err != nil {
		return err
	}
	m.expect(m.scope.Added(2, 3, "", m.peer, m.other), m.scope.Added(2, 3, m.peer, m.peer), m.scope.Added(2, 3, m.other, m.other))
	return nil
}

func (m *memberScenario) newMemberReadsAll() error {
	if err := checkHistory(m.as(m.peer), m.cl, m.st, memberHistoryPage); err != nil {
		return fmt.Errorf("history as %s: %w", m.peer, err)
	}
	return m.read(m.peer, 0, m.last, 1)
}

func (m *memberScenario) promotePeer() error {
	req := &chatimv1.ChangeMemberRoleRequest{RoomId: m.st.Room, User: m.peer, Role: chatimv1.MemberRole_MEMBER_ROLE_ADMIN}
	call := func() (*chatimv1.ChangeMemberRoleResponse, route.Stats, error) { return m.cl.ChangeMemberRole(m.as(m.st.User), req) }
	if err := twice("set-role admin", call, roleReply, e2e.MemberReply{Version: 3, Count: 3, Changed: true, Prev: "member"}, e2e.MemberReply{Version: 3, Count: 3}); err != nil {
		return err
	}
	m.expect(m.scope.RoleChanged(3, m.peer, "admin", ""), m.scope.RoleChanged(3, m.peer, "admin", m.peer))
	return nil
}

func (m *memberScenario) adminRemovesOther() error {
	_, _, denied := m.cl.RemoveMember(m.as(m.peer), &chatimv1.RemoveMemberRequest{RoomId: m.st.Room, User: m.st.User})
	if err := wantCode("admin removes the owner", denied, codes.PermissionDenied); err != nil {
		return err
	}
	req := &chatimv1.RemoveMemberRequest{RoomId: m.st.Room, User: m.other}
	call := func() (*chatimv1.RemoveMemberResponse, route.Stats, error) { return m.cl.RemoveMember(m.as(m.peer), req) }
	if err := twice("remove-member", call, removeReply, e2e.MemberReply{Version: 4, Count: 2, Changed: true}, e2e.MemberReply{Version: 4, Count: 2}); err != nil {
		return err
	}
	m.expect(m.scope.Removed(4, 2, m.other, e2e.ReasonRemoved, "", ""), m.scope.Removed(4, 2, m.other, e2e.ReasonRemoved, "", m.other))
	return nil
}

func (m *memberScenario) removedIsDenied() error {
	ctx := m.as(m.other)
	_, _, sendErr := m.cl.SendMessage(ctx, &chatimv1.SendMessageRequest{RoomId: m.st.Room, Cid: "e2e-removed", Text: "sent after the removal"})
	_, _, historyErr := m.cl.GetHistory(ctx, &chatimv1.GetHistoryRequest{RoomId: m.st.Room, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST, Limit: 1})
	_, _, readErr := m.cl.MarkRead(ctx, &chatimv1.MarkReadRequest{RoomId: m.st.Room})
	for what, got := range map[string]error{"send": sendErr, "history": historyErr, "read": readErr} {
		if err := wantCode(what+" as removed "+m.other, got, codes.PermissionDenied); err != nil {
			return err
		}
	}
	return nil
}

func (m *memberScenario) markUnreadThenRead() error {
	if err := m.unread(m.peer, m.last, m.last-1, 2); err != nil {
		return err
	}
	if err := m.read(m.peer, 0, m.last, 3); err != nil {
		return err
	}
	m.expect(m.scope.ReadUpdated(m.peer, m.last-1, 2), m.scope.ReadUpdated(m.peer, m.last, 3))
	return nil
}

func (m *memberScenario) ownerLeaves() error {
	owner := m.st.User
	req := &chatimv1.LeaveRoomRequest{RoomId: m.st.Room}
	call := func() (*chatimv1.LeaveRoomResponse, route.Stats, error) { return m.cl.LeaveRoom(m.as(owner), req) }
	if err := twice("leave", call, leaveReply, e2e.MemberReply{Version: 5, Count: 1, Changed: true, NewOwner: m.peer}, e2e.MemberReply{Version: 5, Count: 1}); err != nil {
		return err
	}
	_, _, gone := m.cl.GetHistory(m.as(owner), &chatimv1.GetHistoryRequest{RoomId: m.st.Room, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST, Limit: 1})
	if err := wantCode("history after leaving", gone, codes.PermissionDenied); err != nil {
		return err
	}
	m.expect(
		m.scope.Removed(5, 1, owner, e2e.ReasonLeft, m.peer, ""),
		m.scope.Removed(5, 1, owner, e2e.ReasonLeft, m.peer, owner),
		m.scope.Removed(5, 1, owner, e2e.ReasonLeft, m.peer, m.peer),
	)
	return nil
}

func (m *memberScenario) ownerComesBack() error {
	owner := m.st.User
	add := &chatimv1.AddMembersRequest{RoomId: m.st.Room, Users: []string{owner}}
	addCall := func() (*chatimv1.AddMembersResponse, route.Stats, error) { return m.cl.AddMembers(m.as(m.peer), add) }
	if err := twice("re-add", addCall, addReply, e2e.MemberReply{Version: 6, Count: 2, Changed: true, Users: owner}, e2e.MemberReply{Version: 6, Count: 2}); err != nil {
		return err
	}
	role := &chatimv1.ChangeMemberRoleRequest{RoomId: m.st.Room, User: owner, Role: chatimv1.MemberRole_MEMBER_ROLE_OWNER}
	roleCall := func() (*chatimv1.ChangeMemberRoleResponse, route.Stats, error) { return m.cl.ChangeMemberRole(m.as(m.peer), role) }
	if err := twice("set-role owner", roleCall, roleReply, e2e.MemberReply{Version: 7, Count: 2, Changed: true, Prev: "member"}, e2e.MemberReply{Version: 7, Count: 2}); err != nil {
		return err
	}
	if err := m.read(owner, 0, m.last, 2); err != nil {
		return err
	}
	m.expect(m.scope.Added(6, 2, "", owner), m.scope.Added(6, 2, owner, owner), m.scope.RoleChanged(7, owner, "owner", ""), m.scope.RoleChanged(7, owner, "owner", owner))
	return nil
}
```

`tools/corecli/e2e_member_calls.go`:

```go
package main

import (
	"fmt"
	"strconv"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

type readPosition interface {
	GetReadSeq() uint64
	GetReadVersion() uint64
}

func twice[T any](what string, call func() (T, route.Stats, error), reply func(T) e2e.MemberReply, first, again e2e.MemberReply) error {
	for i, want := range []e2e.MemberReply{first, again} {
		resp, st, err := call()
		if err == nil {
			err = e2e.CheckMemberReply(what, reply(resp), want)
		}
		if err != nil {
			return fmt.Errorf("%s %d: %w", what, i+1, err)
		}
		report(what+" "+strconv.Itoa(i+1), st)
	}
	return nil
}

func wantCode(what string, err error, code codes.Code) error {
	if status.Code(err) != code {
		return fmt.Errorf("%s = %w, want %s", what, err, code)
	}
	return nil
}

func (m *memberScenario) read(user string, seq, wantSeq, wantVersion uint64) error {
	resp, _, err := m.cl.MarkRead(m.as(user), &chatimv1.MarkReadRequest{RoomId: m.st.Room, Seq: seq})
	return checkPosition("read as "+user, resp, err, wantSeq, wantVersion)
}

func (m *memberScenario) unread(user string, seq, wantSeq, wantVersion uint64) error {
	resp, _, err := m.cl.MarkUnread(m.as(user), &chatimv1.MarkUnreadRequest{RoomId: m.st.Room, Seq: seq})
	return checkPosition("unread as "+user, resp, err, wantSeq, wantVersion)
}

func checkPosition(what string, got readPosition, err error, seq, version uint64) error {
	switch {
	case err != nil:
		return fmt.Errorf("%s: %w", what, err)
	case got.GetReadSeq() != seq || got.GetReadVersion() != version:
		return fmt.Errorf("%s returned seq %d version %d, want seq %d version %d", what, got.GetReadSeq(), got.GetReadVersion(), seq, version)
	default:
		return nil
	}
}

func addReply(r *chatimv1.AddMembersResponse) e2e.MemberReply {
	return e2e.MemberReply{Version: r.GetMemberVersion(), Count: r.GetMemberCount(), Changed: len(r.GetAdded()) > 0, Users: e2e.JoinUsers(r.GetAdded())}
}

func removeReply(r *chatimv1.RemoveMemberResponse) e2e.MemberReply {
	return e2e.MemberReply{Version: r.GetMemberVersion(), Count: r.GetMemberCount(), Changed: r.GetChanged()}
}

func leaveReply(r *chatimv1.LeaveRoomResponse) e2e.MemberReply {
	return e2e.MemberReply{Version: r.GetMemberVersion(), Count: r.GetMemberCount(), Changed: r.GetChanged(), NewOwner: r.GetNewOwner()}
}

func roleReply(r *chatimv1.ChangeMemberRoleResponse) e2e.MemberReply {
	return e2e.MemberReply{Version: r.GetMemberVersion(), Count: r.GetMemberCount(), Changed: r.GetChanged(), Prev: e2e.RoleName(r.GetPreviousRole())}
}
```

`tools/corecli/e2e_member_live.go`:

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

type liveEvents struct {
	nc  *nats.Conn
	ch  chan *nats.Msg
	got []e2e.Event
}

func openLive(url string, subjects []string) (*liveEvents, error) {
	nc, err := nats.Connect(url, nats.Name("chatim-corecli-e2e-members"))
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}
	l := &liveEvents{nc: nc, ch: make(chan *nats.Msg, 1024)}
	for _, s := range subjects {
		if _, err := nc.ChanSubscribe(s, l.ch); err != nil {
			nc.Close()
			return nil, fmt.Errorf("subscribe %s: %w", s, err)
		}
	}
	if err := nc.FlushTimeout(flushTimeout); err != nil {
		nc.Close()
		return nil, fmt.Errorf("confirm subscriptions %v: %w", subjects, err)
	}
	return l, nil
}

func (l *liveEvents) close() { l.nc.Close() }

func (l *liveEvents) await(ctx context.Context, want []e2e.Event, wait time.Duration) error {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		missing, err := e2e.CheckLive(want, l.got)
		switch {
		case err != nil:
			return fmt.Errorf("live events: %w", err)
		case len(missing) == 0:
			return nil
		}
		select {
		case msg := <-l.ch:
			if err := l.add(msg); err != nil {
				return err
			}
		case <-deadline.C:
			return fmt.Errorf("after %v these member and read events never arrived: %v", wait, missing)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (l *liveEvents) add(msg *nats.Msg) error {
	var ev chatimv1.Event
	if err := proto.Unmarshal(msg.Data, &ev); err != nil {
		return fmt.Errorf("decode event on %s: %w", msg.Subject, err)
	}
	if got, ok := e2e.EventOf(msg.Subject, &ev); ok {
		l.got = append(l.got, got)
	}
	return nil
}
```

`scripts/e2e.sh`, hai chỗ:
- Dòng `echo "e2e PASS: …"` → (một dòng):

```bash
    echo "e2e PASS: $first messages before and $after after killing $victim, no loss, no duplicate, every acked seq live; seq 1 edited and seq 2 deleted on history, edit history and live; seq 3 reacted with the first then the second emoji from GetReactionSettings and an unlisted emoji refused, seq 4 pinned, on replies, history and live; members added with the full history and the read position at the latest, an admin promoted who removes a member then denied send, history and read, unread then read with read_updated, the last owner leaving to the admin and re-added, direct room members fixed, on replies and live room and user subjects"
```

- Sau dòng `cli e2e check -state /state` của phase 4 (ngay trước `result=PASS`), thêm:

```bash

step="phase 5 members and read position"
echo "phase 5: add e2e-bob and e2e-carol, promote e2e-bob, e2e-bob removes e2e-carol, unread then read, the owner leaves to e2e-bob and comes back, direct room members fixed"
cli e2e members -state /state
step="phase 5 check"
cli e2e check -state /state
```

**Step 11: Chạy**

```bash
make -s go ARGS="test -race -shuffle=on ./tools/..."
make -s go ARGS="vet ./tools/..."
wc -l tools/corecli/*.go tools/corecli/internal/e2e/*.go | sort -n | tail -8
```

Expected: PASS; vet sạch; mỗi file < 200 (`e2e_member_steps.go` ~140, `cmd_members.go` ~135, `e2e_members.go` ~110, `e2e_member_calls.go` ~80, `e2e_member_live.go` ~80, `cmd_watch.go` ~115, `main.go` ~85).

Run (cần Task 14 đã wiring RPC và `readcast`; Task 15 cho event bù, không bắt buộc vì fast path đã phát mọi bản):

```bash
make infra-reset && make image TARGET=apps/core && make core-up && make e2e
```

(`make infra-reset` để stream `CHATIM_EVT` có luật RePublish mới và DB không còn room trước M2b.4; xem ghi chú rolling deploy của hợp đồng chung.)

Expected: các dòng `phase 1`…`phase 4` như M2b.3; `phase 5: add e2e-bob and e2e-carol, …`; các dòng `add-members 1 via …`, `add-members 2 …`, `set-role admin 1 …`, `set-role admin 2 …`, `remove-member 1 …`, `remove-member 2 …`, `leave 1 …`, `leave 2 …`, `re-add 1 …`, `re-add 2 …`, `set-role owner 1 …`, `set-role owner 2 …`; `members ok: e2e-bob and e2e-carol added at member version 2 reading from seq 80, e2e-bob admin at 3, e2e-carol removed at 4 and denied, read position 79 then 80, the owner left to e2e-bob at 5 and came back at 6 and 7, direct room members fixed; 16 live events on the room and user subjects`; ở phase 5 check: `history ok: 80 messages …` và `live ok: an event for each of 80 seq, …`; dòng cuối là PASS mới ở trên. Bước nào khác → dừng, báo (ghi lỗi in ra; ví dụ `read as e2e-bob returned seq 80 version 2, want … version 1` nghĩa là fact thêm không mang `ReadSeq` (Task 10) hoặc `Applied` không cộng `v` (Task 2)).

Thử nhanh lệnh tay (room lấy từ log e2e; `REDIS_PASSWORD` export từ `.env`, không in ra):

```bash
docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev add-members -room <ROOM> -user e2e-user -users e2e-dave
docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev unread -room <ROOM> -user e2e-dave -seq 80
docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev set-role -room <ROOM> -user e2e-dave -target e2e-user -role member
```

Expected: lệnh đầu in protojson có `"memberVersion":"8"`, `"memberCount":3`, `"added":["e2e-dave"]`; lệnh hai `"readSeq":"79"`, `"readVersion":"2"`; lệnh ba (member thường đổi role) → `permission denied`, exit 1. (Số có thể lệch nếu đã chạy lệnh tay khác; ghi số thật vào báo cáo.)

**Step 12: INDEXES.csv + commit**

```bash
python3 bin/indexes_edit.py tools/corecli purpose "~" "/pin/unpin/watch/slots/e2e (steps setup, send, change, react-pin, check;" "/pin/unpin/add-members/remove-member (-target)/leave/set-role (-target, -role)/read/unread/watch (-room or -user)/slots/e2e (steps setup, send, change, react-pin, members, check;"
python3 bin/indexes_edit.py tools/corecli purpose + "; e2e members connects NATS itself (-nats, -live-root), subscribes the room and three user subjects, then checks that direct room member commands are FAILED_PRECONDITION, add/promote/remove/leave/re-add replies and their no-op repeats, the removed user's send/history/read PERMISSION_DENIED, the read position (new member at the latest, unread then read, re-add raises it), and every member_added/member_removed/member_role_changed/read_updated copy by id, subject and payload"
python3 bin/indexes_edit.py tools/corecli decisions + ";D99;D100;D102;D104;D105"
python3 bin/indexes_edit.py tools/corecli/internal/e2e purpose + "; member and read events decoded by EventOf (member_added users sorted, member_removed reason and new owner, member_role_changed role, read_updated seq and version); Scope builds the wanted room and user copies with ids {room}-m{mv}, {room}-m{mv}-{user}, {room}-rd-{user}-v{v} and their live subjects; CheckLive matches wanted events by id (other ids and duplicates ignored); CheckMemberReply compares member command replies"
python3 bin/indexes_edit.py tools/corecli/internal/e2e key_symbols + ";Scope;Scope.Added;Scope.Removed;Scope.RoleChanged;Scope.ReadUpdated;RoomSubject;UserSubject;MemberEventID;MemberUserEventID;ReadEventID;RoleName;JoinUsers;CheckLive;MemberReply;CheckMemberReply;KindMemberAdded;KindMemberRemoved;KindRoleChanged;KindReadUpdated;ReasonRemoved;ReasonLeft"
python3 bin/indexes_edit.py tools/corecli/internal/e2e decisions + ";D102;D104;D105"
python3 bin/indexes_edit.py scripts/e2e.sh purpose "~" "; corecli containers get -e REDIS_PASSWORD" "; phase 5 runs e2e members (direct room members fixed; e2e-bob and e2e-carol added; e2e-bob promoted to admin removes e2e-carol, who is then denied; unread then read with read_updated; the last owner leaves to e2e-bob and is re-added) then e2e check as the re-added owner; corecli containers get -e REDIS_PASSWORD"
python3 bin/indexes_edit.py scripts/e2e.sh decisions + ";D99;D100;D104"
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
make fmt-check && make vet && make lint
git add tools/corecli/cmd_members.go tools/corecli/e2e_members.go tools/corecli/e2e_member_steps.go tools/corecli/e2e_member_calls.go tools/corecli/e2e_member_live.go tools/corecli/internal/e2e/members.go tools/corecli/internal/e2e/members_check.go tools/corecli/internal/e2e/members_test.go
git commit -m "feat(corecli): add member and read position commands, watch a user and an e2e member phase" -- tools/corecli/ scripts/e2e.sh INDEXES.csv
```

Expected: `ok` từng lệnh helper, `{7}`, sạch; commit 14 file + `INDEXES.csv`.

Task thấp: controller kiểm nhanh (kịch bản khớp bảng ở đầu task; PASS line; `-target` thay `-user` được ghi vào báo cáo), không reviewer.

---

### Task 18: Itest end-to-end member và vị trí đọc

Khẳng định trên hạ tầng thật (skip khi thiếu `CHATIM_IT_*`), dùng helper sẵn có (`realInfra`, `startCore`, `dialCore`, `createRoom` (room `alice` owner + `bob`), `createRoomWith`, `caller`, `callerAs`, `sendAs` (qua `sendRetrying`), `historyAs`, `parseRoom`, `subscribeLive`, `itStore`, `itFastEffects`, `awaitLiveEvents`, `awaitStored`, `assertNoLiveIDs`, `retryingUnavailable`) và helper mới ở `it_members_test.go`:

- (a) **Core chết giữa fact và projection, worker sửa**: fact `member_actions` mv 2 (thêm `dave`) ghi thẳng vào Mongo, không qua lệnh (giả lập core chết ngay sau insert) → feed → work stream → `member_projection` áp `members`/`user_rooms`, CAS đầu `rooms.mv/mc` → `member_event` phát bản room và bản user của `dave`; `dave` đọc được lịch sử; lệnh thêm sau đó đi tiếp từ mv 3.
- (a') **Settle-first khi không có worker**: cùng fact nhưng reader tắt (`RECONCILE_ENABLED=false`): trước lệnh, `dave` chưa là member; lệnh `AddMembers(erin)` settle fact mv 2 trước khi ghi mv 3, nên cả hai được áp và đầu ở mv 3, 4 member (bất biến 2 của hợp đồng chung).
- (b) **Hai owner xoá nhau cùng lúc**: đúng một thành công, người kia `PERMISSION_DENIED` (lượt thử lại settle thấy mình đã bị xoá); room còn đúng một owner. Với `DefaultPolicy` (D99) admin không xoá được admin, nên "hai admin xoá nhau" là hai `PERMISSION_DENIED`; test kiểm cả hai.
- (c) **Thêm đồng thời**: 6 lệnh `AddMembers` chồng nhau (9 user, mỗi user nằm trong 2 lệnh) từ owner và admin xen kẽ, client gửi lại khi `UNAVAILABLE` như route: mỗi user được thêm đúng một lần trên mọi reply, số fact = số reply có `added`, mv dày, đầu và `n` của fact cuối = 11 member, `user_rooms` có room cho mọi user.
- (d) **Subject user qua RePublish**: bản user của `member_added`/`member_removed` tới `live.{t}.user.{u}.evt.{kind}` với `recipient = u`; bản room tới subject room; bản user không xuất hiện trên subject room; luật RePublish của stream là `evt.*.*.*.*`.
- (e) **Người bị xoá bị từ chối ngay qua cache actor**: `bob` gửi hai tin (cache actor có `bob`), `alice` xoá `bob` (cùng core, `ForgetMembers`), lệnh gửi kế tiếp của `bob` → `PERMISSION_DENIED` ngay (không đợi TTL 10s), `GetHistory` và `MarkRead` cũng vậy; `alice` vẫn gửi được.
- (f) **Thêm lại giữ `cb` và nâng vị trí đọc**: `bob` đọc tới seq 3 (`read_updated` v2 trên subject room), clear tới seq 2, bị xoá, room có thêm seq 4–5, được thêm lại → doc `members` active, `mv` 3, `cb` 2 giữ nguyên, `rs = {5, 3}`, `ja` mới; lịch sử của `bob` ẩn seq 1–2, hiện 3–5; `MarkRead(0)` trả `{5, 3}` (không đổi); `user_rooms` của `bob` có room.

Phần cập nhật RePublish trên stream đã có từ trước (`EnsureStream` ghi đè luật cũ) do itest của Task 3 (`publish/nats_integration_test.go`) phủ, không lặp ở đây.

Chờ trạng thái lưu (`rooms.mv`, doc `members`) dùng `awaitStored` (poll `itActivityPoll` có hạn `itLiveLimit`) như M2b.3: hội tụ của worker không có tín hiệu nào khác để chờ. Live event chờ theo id.

Không có bước "thấy fail": các test xác nhận hành vi của Task 2–17. Test fail ở lần chạy đầu là lỗi của task trước: dừng và báo (không sửa test cho qua). Đặc biệt:
- (a) không thấy `{room}-m2` → feed chưa phát `MemberInserted` (Task 6) hoặc registry chưa có `member_event` (Task 15); thấy event nhưng `rooms.mv` kẹt ở 1 → `member_projection`/`memberproj.Project` (Task 7/15).
- (a') reply mv 2 thay vì 3 → lệnh không settle trước khi tính đầu (Task 10).
- (b) hai thành công → kiểm quyền chạy trên doc cũ (`Admit`) thay vì sau settle (Task 10, bất biến 2).
- (c) một user được thêm hai lần → nhận diện trùng khoá hay lọc user đã active sai (Task 10).
- (d) bản user không tới → `recipient`/subject user/RePublish (Task 3).
- (e) gửi lần ba thành công → `ForgetMembers` chưa gọi hoặc thế hệ cache sai (Task 8/10).
- (f) `rs.v` 2 thay vì 3, hoặc `cb` mất → `Applied`/`ApplyMembers` vào lại (Task 2/5).

Kiểm `git diff --quiet -- INDEXES.csv` trước khi sửa (như Task 15).

**Files:**
- Create: `apps/core/it_members_test.go` (helper)
- Create: `apps/core/member_integration_test.go` (a, a')
- Create: `apps/core/member_events_integration_test.go` (d)
- Create: `apps/core/member_race_integration_test.go` (b, c)
- Create: `apps/core/member_access_integration_test.go` (e, f)
- Modify: `INDEXES.csv`

**Step 1: Helper**

`apps/core/it_members_test.go`:

```go
package main

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func subscribeUser(t *testing.T, it *itInfra, cfg config.Config, user string) <-chan *nats.Msg {
	t.Helper()
	live := make(chan *nats.Msg, 256)
	sub, err := it.nc.ChanSubscribe(cfg.Stream.LiveRoot+"."+itTenant+".user."+user+".>", live)
	if err != nil {
		t.Fatalf("subscribe user %s: %v", user, err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	if err := it.nc.Flush(); err != nil {
		t.Fatalf("flush the subscription of %s: %v", user, err)
	}
	return live
}

func awaitLiveMsg(t *testing.T, live <-chan *nats.Msg, id string) (*nats.Msg, *chatimv1.Event) {
	t.Helper()
	deadline := time.After(itLiveLimit)
	for {
		select {
		case m := <-live:
			if m.Header.Get(jetstream.MsgIDHeader) != id {
				continue
			}
			ev := &chatimv1.Event{}
			if err := proto.Unmarshal(m.Data, ev); err != nil {
				t.Fatalf("decode live event %s: %v", id, err)
			}
			return m, ev
		case <-deadline:
			t.Fatalf("live event %s did not arrive within %v", id, itLiveLimit)
		}
	}
}

func memberOf(t *testing.T, st *mongostore.Store, room uint64, user string) domain.Member {
	t.Helper()
	found, err := st.MembersOf(t.Context(), room, []string{user})
	if err != nil || len(found) != 1 {
		t.Fatalf("MembersOf(%d, %s) = %+v, %v; want one doc", room, user, found, err)
	}
	return found[0]
}

func addMembersAs(t *testing.T, client chatimv1.CoreServiceClient, as, roomID string, users ...string) *chatimv1.AddMembersResponse {
	t.Helper()
	resp, err := retryingUnavailable(callerAs(t.Context(), as), func(ctx context.Context) (*chatimv1.AddMembersResponse, error) {
		return client.AddMembers(ctx, &chatimv1.AddMembersRequest{RoomId: roomID, Users: users})
	})
	if err != nil {
		t.Fatalf("AddMembers(%v) as %s: %v", users, as, err)
	}
	return resp
}

func setRoleAs(t *testing.T, client chatimv1.CoreServiceClient, as, roomID, user string, role chatimv1.MemberRole) {
	t.Helper()
	resp, err := retryingUnavailable(callerAs(t.Context(), as), func(ctx context.Context) (*chatimv1.ChangeMemberRoleResponse, error) {
		return client.ChangeMemberRole(ctx, &chatimv1.ChangeMemberRoleRequest{RoomId: roomID, User: user, Role: role})
	})
	if err != nil || !resp.GetChanged() {
		t.Fatalf("ChangeMemberRole(%s, %s) as %s = %v, %v; want a change", user, role, as, resp, err)
	}
}

func removeAs(t *testing.T, client chatimv1.CoreServiceClient, as, roomID, user string) (*chatimv1.RemoveMemberResponse, error) {
	return retryingUnavailable(callerAs(t.Context(), as), func(ctx context.Context) (*chatimv1.RemoveMemberResponse, error) {
		return client.RemoveMember(ctx, &chatimv1.RemoveMemberRequest{RoomId: roomID, User: user})
	})
}

func readAs(t *testing.T, client chatimv1.CoreServiceClient, as, roomID string, seq uint64) *chatimv1.MarkReadResponse {
	t.Helper()
	resp, err := retryingUnavailable(callerAs(t.Context(), as), func(ctx context.Context) (*chatimv1.MarkReadResponse, error) {
		return client.MarkRead(ctx, &chatimv1.MarkReadRequest{RoomId: roomID, Seq: seq})
	})
	if err != nil {
		t.Fatalf("MarkRead(%d) as %s: %v", seq, as, err)
	}
	return resp
}
```

Tên helper không được trùng helper Part A/B có thể đã thêm vào package `main` test (`grep -n "func subscribeUser\|func memberOf\|func addMembersAs\|func setRoleAs\|func removeAs\|func readAs\|func awaitLiveMsg" apps/core/*_test.go` trước khi tạo file → chỉ thấy file này sau khi tạo); trùng thì đổi tên cục bộ, ghi vào báo cáo.

**Step 2: Test**

`apps/core/member_integration_test.go`:

```go
package main

import (
	"errors"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
)

func outsideAdd(room uint64, user string) domain.MemberAction {
	return domain.MemberAction{
		Room: room, MV: 2, Tenant: itTenant, Op: domain.MemberOpAdd,
		Changes: []domain.MemberChange{{User: user, Role: domain.RoleMember}}, By: itUser,
		At: time.Now().UTC().Truncate(time.Millisecond), Count: 3,
	}
}

func TestRealInfraWorkersProjectAMemberFactWrittenOutsideTheCore(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)
	dave := subscribeUser(t, it, core.cfg, "dave")
	core.awaitTerm(t)

	st := itStore(it, core)
	if err := st.MemberActions().Append(t.Context(), outsideAdd(room, "dave")); err != nil {
		t.Fatalf("append a member fact outside the core: %v", err)
	}
	roomCopy, daveCopy := pbconv.MemberEventID(room, 2), pbconv.MemberUserEventID(room, 2, "dave")
	added := awaitLiveEvents(t, live, roomCopy)[roomCopy].GetMemberAdded()
	if added.GetMemberVersion() != 2 || added.GetMemberCount() != 3 || len(added.GetMembers()) != 1 || added.GetMembers()[0].GetUser() != "dave" {
		t.Fatalf("member_added from the workers = %v, want dave at member version 2 with 3 members", added)
	}
	if ev := awaitLiveEvents(t, dave, daveCopy)[daveCopy]; ev.GetRecipient() != "dave" || ev.GetMemberAdded().GetMemberVersion() != 2 {
		t.Fatalf("dave's copy = %v, want member version 2 addressed to dave", ev)
	}
	read := func() (domain.Room, error) { return st.Get(t.Context(), room) }
	head := awaitStored(t, "member head of the room", read, func(r domain.Room) bool { return r.MemberVersion >= 2 })
	if head.MemberVersion != 2 || head.MemberCount != 3 {
		t.Fatalf("room = %+v, want member version 2 with 3 members", head)
	}
	if m := memberOf(t, st, room, "dave"); m.Removed || m.MV != 2 || m.Role != domain.RoleMember {
		t.Fatalf("dave = %+v, want an active member at member version 2", m)
	}
	if history := historyAs(t, client, "dave", roomID); len(history) != 0 {
		t.Fatalf("dave reads %v, want the empty history of a new room", slices.Sorted(maps.Keys(history)))
	}
	resp := addMembersAs(t, client, itUser, roomID, "erin")
	if resp.GetMemberVersion() != 3 || resp.GetMemberCount() != 4 || !slices.Equal(resp.GetAdded(), []string{"erin"}) {
		t.Fatalf("AddMembers(erin) = %v, want member version 3 with 4 members after the healed fact", resp)
	}
}

func TestRealInfraMemberCommandSettlesAFactNoWorkerProjected(t *testing.T) {
	it := realInfra(t)
	env := maps.Clone(itFastEffects)
	env["RECONCILE_ENABLED"] = "false"
	core := startCore(t, it, env)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)

	st := itStore(it, core)
	if err := st.MemberActions().Append(t.Context(), outsideAdd(room, "dave")); err != nil {
		t.Fatalf("append a member fact outside the core: %v", err)
	}
	if _, err := st.Member(t.Context(), room, "dave"); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("dave before any command = %v, want ErrNotMember (no reader, nothing projected)", err)
	}
	resp := addMembersAs(t, client, itUser, roomID, "erin")
	if resp.GetMemberVersion() != 3 || resp.GetMemberCount() != 4 {
		t.Fatalf("AddMembers(erin) = %v, want member version 3 with 4 members: the command settles mv 2 first", resp)
	}
	head, err := st.Get(t.Context(), room)
	if err != nil || head.MemberVersion != 3 || head.MemberCount != 4 {
		t.Fatalf("room = %+v, %v; want member version 3 with 4 members", head, err)
	}
	for user, mv := range map[string]uint64{"dave": 2, "erin": 3} {
		if m := memberOf(t, st, room, user); m.Removed || m.MV != mv {
			t.Fatalf("%s = %+v, want active at member version %d", user, m, mv)
		}
	}
	historyAs(t, client, "dave", roomID)
}
```

`apps/core/member_events_integration_test.go`:

```go
package main

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestRealInfraUserCopiesOfMemberEventsReachTheUserSubject(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)
	frank := subscribeUser(t, it, core.cfg, "frank")
	userSubject := core.cfg.Stream.LiveRoot + "." + itTenant + ".user.frank.evt."
	roomSubject := core.cfg.Stream.LiveRoot + "." + itTenant + ".room." + roomID + ".evt."

	if resp := addMembersAs(t, client, itUser, roomID, "frank"); resp.GetMemberVersion() != 2 {
		t.Fatalf("AddMembers(frank) = %v, want member version 2", resp)
	}
	msg, ev := awaitLiveMsg(t, frank, pbconv.MemberUserEventID(room, 2, "frank"))
	members := ev.GetMemberAdded().GetMembers()
	if msg.Subject != userSubject+"member_added" || ev.GetRecipient() != "frank" || len(members) != 1 || members[0].GetUser() != "frank" {
		t.Fatalf("frank's copy on %q = %v, want member_added for frank on his own subject", msg.Subject, ev)
	}
	if msg, _ := awaitLiveMsg(t, live, pbconv.MemberEventID(room, 2)); msg.Subject != roomSubject+"member_added" {
		t.Fatalf("room copy on %q, want the room subject", msg.Subject)
	}
	assertNoLiveIDs(t, live, time.Second, pbconv.MemberUserEventID(room, 2, "frank"))

	if resp, err := removeAs(t, client, itUser, roomID, "frank"); err != nil || !resp.GetChanged() || resp.GetMemberVersion() != 3 {
		t.Fatalf("RemoveMember(frank) = %v, %v; want a change at member version 3", resp, err)
	}
	msg, ev = awaitLiveMsg(t, frank, pbconv.MemberUserEventID(room, 3, "frank"))
	r := ev.GetMemberRemoved()
	if msg.Subject != userSubject+"member_removed" || r.GetUser() != "frank" || r.GetReason() != chatimv1.MemberRemovedReason_MEMBER_REMOVED_REASON_REMOVED {
		t.Fatalf("frank's removal on %q = %v, want member_removed (removed) on his own subject", msg.Subject, ev)
	}

	stream, err := it.js.Stream(t.Context(), core.cfg.Stream.Name)
	if err != nil {
		t.Fatalf("stream %s: %v", core.cfg.Stream.Name, err)
	}
	info, err := stream.Info(t.Context())
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	if rp := info.Config.RePublish; rp == nil || rp.Source != core.cfg.Stream.SubjectRoot+".*.*.*.*" {
		t.Fatalf("stream RePublish = %+v, want source %s.*.*.*.*", rp, core.cfg.Stream.SubjectRoot)
	}
}
```

`apps/core/member_race_integration_test.go`:

```go
package main

import (
	"context"
	"strconv"
	"sync"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestRealInfraTwoOwnersRemovingEachOtherLeaveOneOwner(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoomWith(t, client, []string{itUser, "bob", "carol", "dave"})
	room := parseRoom(t, roomID)
	setRoleAs(t, client, itUser, roomID, "bob", chatimv1.MemberRole_MEMBER_ROLE_OWNER)
	setRoleAs(t, client, itUser, roomID, "carol", chatimv1.MemberRole_MEMBER_ROLE_ADMIN)
	setRoleAs(t, client, itUser, roomID, "dave", chatimv1.MemberRole_MEMBER_ROLE_ADMIN)
	for _, pair := range [][2]string{{"carol", "dave"}, {"dave", "carol"}} {
		if _, err := removeAs(t, client, pair[0], roomID, pair[1]); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("admin %s removes admin %s = %v, want PermissionDenied", pair[0], pair[1], err)
		}
	}

	pairs := [][2]string{{itUser, "bob"}, {"bob", itUser}}
	errs := make([]error, len(pairs))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, p := range pairs {
		wg.Go(func() {
			<-start
			_, errs[i] = removeAs(t, client, p[0], roomID, p[1])
		})
	}
	close(start)
	wg.Wait()
	won := -1
	for i, err := range errs {
		switch status.Code(err) {
		case codes.OK:
			if won >= 0 {
				t.Fatalf("both owners removed each other: %v", errs)
			}
			won = i
		case codes.PermissionDenied:
		default:
			t.Fatalf("%s removes %s = %v, want success or PermissionDenied", pairs[i][0], pairs[i][1], err)
		}
	}
	if won < 0 {
		t.Fatalf("neither removal succeeded: %v", errs)
	}
	winner, loser := pairs[won][0], pairs[won][1]
	st := itStore(it, core)
	if m := memberOf(t, st, room, loser); !m.Removed {
		t.Fatalf("loser %s = %+v, want removed", loser, m)
	}
	owners, err := st.Owners(t.Context(), room, store.MaxMemberScan)
	if err != nil || len(owners) != 1 || owners[0].User != winner {
		t.Fatalf("owners = %+v, %v; want only %s", owners, err, winner)
	}
	head, err := st.Get(t.Context(), room)
	if err != nil || head.MemberVersion != 5 || head.MemberCount != 3 {
		t.Fatalf("room = %+v, %v; want member version 5 (create, three roles, one removal) with 3 members", head, err)
	}
}

func TestRealInfraConcurrentAddsAddEachUserOnce(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoomWith(t, client, []string{itUser, "bob"})
	room := parseRoom(t, roomID)
	setRoleAs(t, client, itUser, roomID, "bob", chatimv1.MemberRole_MEMBER_ROLE_ADMIN)
	batches := [][]string{{"u1", "u2", "u3"}, {"u3", "u4", "u5"}, {"u5", "u6", "u1"}, {"u7", "u2", "u8"}, {"u8", "u9", "u4"}, {"u6", "u9", "u7"}}
	added := make([][]string, len(batches))
	errs := make([]error, len(batches))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, users := range batches {
		by := []string{itUser, "bob"}[i%2]
		req := &chatimv1.AddMembersRequest{RoomId: roomID, Users: users}
		wg.Go(func() {
			<-start
			resp, err := retryingUnavailable(callerAs(t.Context(), by), func(ctx context.Context) (*chatimv1.AddMembersResponse, error) {
				return client.AddMembers(ctx, req)
			})
			added[i], errs[i] = resp.GetAdded(), err
		})
	}
	close(start)
	wg.Wait()
	seen, facts := map[string]int{}, 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("AddMembers(%v): %v", batches[i], err)
		}
		for _, u := range added[i] {
			seen[u]++
		}
		if len(added[i]) > 0 {
			facts++
		}
	}
	st := itStore(it, core)
	for n := range 9 {
		u := "u" + strconv.Itoa(n+1)
		if seen[u] != 1 {
			t.Fatalf("%s added %d times across %v, want exactly once", u, seen[u], added)
		}
		if m := memberOf(t, st, room, u); m.Removed || m.Role != domain.RoleMember {
			t.Fatalf("%s = %+v, want an active member", u, m)
		}
		rooms, err := st.UserRooms(t.Context(), itTenant, u, 0, 10)
		if err != nil || len(rooms) != 1 || rooms[0].Room != room {
			t.Fatalf("user rooms of %s = %+v, %v; want only room %d", u, rooms, err, room)
		}
	}
	list, err := st.MemberActions().After(t.Context(), room, 2, store.MaxMemberScan)
	if err != nil || len(list) != facts {
		t.Fatalf("member facts after mv 2 = %d, %v; want one per reply that added someone (%d)", len(list), err, facts)
	}
	next := uint64(3)
	for _, a := range list {
		if a.MV != next {
			t.Fatalf("member facts %+v are not dense from mv 3", list)
		}
		next++
	}
	head, err := st.Get(t.Context(), room)
	if err != nil || head.MemberVersion != next-1 || head.MemberCount != 11 || list[len(list)-1].Count != 11 {
		t.Fatalf("room = %+v, %v, last fact count %d; want member version %d with 11 members", head, err, list[len(list)-1].Count, next-1)
	}
}
```

`apps/core/member_access_integration_test.go`:

```go
package main

import (
	"strconv"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestRealInfraRemovedMemberIsDeniedAtOnceThroughTheActorCache(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	sendAs(t, client, "bob", roomID, "bob-1", "warm the member cache")
	sendAs(t, client, "bob", roomID, "bob-2", "still a member")
	if resp, err := removeAs(t, client, itUser, roomID, "bob"); err != nil || !resp.GetChanged() {
		t.Fatalf("RemoveMember(bob) = %v, %v; want a change", resp, err)
	}
	bob := callerAs(t.Context(), "bob")
	_, err := client.SendMessage(bob, &chatimv1.SendMessageRequest{RoomId: roomID, Cid: "bob-3", Text: "sent after the removal"})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("send after the removal = %v, want PermissionDenied at once, not after the 10s cache TTL", err)
	}
	_, err = client.GetHistory(bob, &chatimv1.GetHistoryRequest{RoomId: roomID, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST, Limit: 10})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("history after the removal = %v, want PermissionDenied", err)
	}
	if _, err := client.MarkRead(bob, &chatimv1.MarkReadRequest{RoomId: roomID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("read after the removal = %v, want PermissionDenied", err)
	}
	if seq := sendAs(t, client, itUser, roomID, "alice-1", "after the removal"); seq != 3 {
		t.Fatalf("alice's send after the removal got seq %d, want 3", seq)
	}
}

func TestRealInfraReAddKeepsClearedHistoryAndRaisesTheReadPosition(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)
	st := itStore(it, core)
	for i := range 3 {
		sendAs(t, client, itUser, roomID, "before-"+strconv.Itoa(i+1), "before the removal")
	}
	if r := readAs(t, client, "bob", roomID, 0); r.GetReadSeq() != 3 || r.GetReadVersion() != 2 {
		t.Fatalf("bob reads = %v, want seq 3 at version 2", r)
	}
	readID := pbconv.ReadEventID(room, "bob", 2)
	if u := awaitLiveEvents(t, live, readID)[readID].GetReadUpdated(); u.GetUser() != "bob" || u.GetSeq() != 3 || u.GetVersion() != 2 {
		t.Fatalf("read_updated = %v, want bob at seq 3 version 2 on the room subject", u)
	}
	cleared, err := client.ClearHistory(callerAs(t.Context(), "bob"), &chatimv1.ClearHistoryRequest{RoomId: roomID, UpToSeq: 2})
	if err != nil || cleared.GetClearedBeforeSeq() != 2 {
		t.Fatalf("ClearHistory(2) = %v, %v", cleared, err)
	}
	before := memberOf(t, st, room, "bob")
	if resp, err := removeAs(t, client, itUser, roomID, "bob"); err != nil || resp.GetMemberVersion() != 2 {
		t.Fatalf("RemoveMember(bob) = %v, %v; want member version 2", resp, err)
	}
	sendAs(t, client, itUser, roomID, "after-1", "while bob is out")
	sendAs(t, client, itUser, roomID, "after-2", "while bob is out")
	if resp := addMembersAs(t, client, itUser, roomID, "bob"); resp.GetMemberVersion() != 3 || resp.GetMemberCount() != 2 {
		t.Fatalf("re-add = %v, want member version 3 with 2 members", resp)
	}
	after := memberOf(t, st, room, "bob")
	if after.Removed || after.MV != 3 || after.ClearedBeforeSeq != 2 || after.Read != (domain.ReadPos{Seq: 5, Version: 3}) || !after.JoinedAt.After(before.JoinedAt) {
		t.Fatalf("bob after the re-add = %+v, want active at mv 3, cb 2 kept, read {5, 3}, a later join time than %v", after, before.JoinedAt)
	}
	history := historyAs(t, client, "bob", roomID)
	for seq := uint64(1); seq <= 5; seq++ {
		if m, ok := history[seq]; !ok || m.GetHidden() != (seq <= 2) {
			t.Fatalf("seq %d in bob's history = %v (present %v), want hidden only up to the kept clear point 2", seq, m, ok)
		}
	}
	if r := readAs(t, client, "bob", roomID, 0); r.GetReadSeq() != 5 || r.GetReadVersion() != 3 {
		t.Fatalf("bob reads after the re-add = %v, want seq 5 version 3 unchanged: the re-add already put him at the latest", r)
	}
	rooms, err := st.UserRooms(t.Context(), itTenant, "bob", 0, 10)
	if err != nil || len(rooms) != 1 || rooms[0].Room != room || rooms[0].Removed {
		t.Fatalf("user rooms of bob = %+v, %v; want the room, active", rooms, err)
	}
}
```

Ghi chú (f): fact tạo room có `ReadSeq 0`, nên `rs` của `bob` lúc tạo là `{0, 1}`; `MarkRead(0)` sau ba tin → `{3, 2}`; rời giữ `rs`; thêm lại khi seq cuối là 5 → `{max(3, 5), 3} = {5, 3}`.

**Step 3: Biên dịch**

Run: `make -s go ARGS="vet ./apps/core/"` rồi `make -s go ARGS="test -race -shuffle=on ./apps/core/"`
Expected: vet sạch; test PASS (itest skip). Lỗi tên (`st.MemberActions()`, `st.MembersOf`, `st.Owners`, `st.UserRooms`, getter proto) nghĩa là Part A/B đặt tên khác hợp đồng: chỉ sửa tên trong test, ghi vào báo cáo. `wc -l apps/core/*member*_test.go apps/core/it_members_test.go` → mỗi file < 200 (`member_race_integration_test.go` ~145, `member_access_integration_test.go` ~95, `member_integration_test.go` ~95, `member_events_integration_test.go` ~60, `it_members_test.go` ~100).

**Step 4: Chạy trên hạ tầng thật**

Run một test trước bằng makefile scratchpad của "Quy tắc chung" (ví dụ `make -f <scratchpad>/itest-one.mk itest-one RUN='TestRealInfra.*Member|TestRealInfraUserCopies|TestRealInfraTwoOwners|TestRealInfraConcurrentAdds|TestRealInfraReAdd' PKG=./apps/core/`), rồi cả repo:

```bash
make infra-up && make itest
```

Expected: mọi package `ok`, gồm 7 itest mới (`TestRealInfraWorkersProjectAMemberFactWrittenOutsideTheCore`, `TestRealInfraMemberCommandSettlesAFactNoWorkerProjected`, `TestRealInfraUserCopiesOfMemberEventsReachTheUserSubject`, `TestRealInfraTwoOwnersRemovingEachOtherLeaveOneOwner`, `TestRealInfraConcurrentAddsAddEachUserOnce`, `TestRealInfraRemovedMemberIsDeniedAtOnceThroughTheActorCache`, `TestRealInfraReAddKeepsClearedHistoryAndRaisesTheReadPosition`), drill resync (Task 16), itest RePublish của Task 3, contract `storetest` Mongo (`RunMembers`, `RunMemberFeed`), các itest M2b.2/M2b.3. Không skip (kiểm log `--- PASS` cho 7 tên trên). Chạy `-count=1` mặc định; flaky ở (b)/(c) thì chạy riêng test đó 3 lần bằng makefile scratchpad, ghi kết quả, báo controller (không thêm sleep).

**Step 5: INDEXES.csv + commit**

```bash
python3 bin/indexes_edit.py apps/core tests + ";itest (workers project and publish a member fact written straight to Mongo; with the reader off, a member command settles it first);itest (user copies of member events reach live.{t}.user.{u} through the widened RePublish, room copies stay on the room subject);itest (two owners removing each other leave exactly one owner, two admins cannot remove each other; overlapping concurrent adds add each user once with dense member versions);itest (a removed member is denied at once through the actor cache; a re-add keeps cb and raises the read position)"
python3 bin/indexes_edit.py apps/core decisions + ";D97;D101;D102;D104"
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
make fmt-check && make vet && make lint
git add apps/core/it_members_test.go apps/core/member_integration_test.go apps/core/member_events_integration_test.go apps/core/member_race_integration_test.go apps/core/member_access_integration_test.go
git commit -m "test(core): cover member facts, races, user subjects and the read position on real infra" -- apps/core/it_members_test.go apps/core/member_integration_test.go apps/core/member_events_integration_test.go apps/core/member_race_integration_test.go apps/core/member_access_integration_test.go INDEXES.csv
```

Expected: `ok`, `{7}`, sạch; commit 5 file + `INDEXES.csv`.

Task trung bình: controller kiểm nhanh (test khớp danh sách (a)–(f), không sửa code sản phẩm), không reviewer.

---

### Task 19: Docs

Task docs, rủi ro thấp: controller kiểm nhanh, không reviewer. Không đổi code Go hay luật alert.

**Step 0: Dừng nếu cây làm việc còn thay đổi chưa commit của người khác**

```bash
git status --porcelain -- CLAUDE.md README.md INDEXES.csv docs/designs/261005-chatim-architecture.md docs/roadmap.md docs/plans/2026-10-06-m2b4-members-read.md
```

Expected: không in gì. Có dòng nào → **dừng, hỏi controller**; không commit chung thay đổi của người khác, không `git stash`/`checkout` chúng.

**Files:**
- Modify: `docs/designs/261005-chatim-architecture.md`
- Modify: `docs/roadmap.md`
- Modify: `README.md`
- Modify: `CLAUDE.md`
- Modify: `INDEXES.csv`
- Modify: `docs/plans/2026-10-06-m2b4-members-read.md` (mục "Kết quả thực thi" thêm ở cuối file)

**Step 1: Kiểm luật alert và metric (không sửa)**

```bash
grep -c "^      - alert:" deploy/prometheus/alerts.yml
make alerts-check
grep -n "read_events_unbatched_total\|member_projection\|member_event" apps/core/metrics_wiring.go apps/core/effects_wiring.go
```

Expected: `16`; `SUCCESS: 16 rules found`; `read_events_unbatched_total` trong `metrics_wiring.go` (Task 14), `memberProjection`/`memberEvent` trong `effects_wiring.go` (Task 15). Khác → dừng, báo (task trước chưa xong).

**Step 2: Thiết kế**

`docs/designs/261005-chatim-architecture.md`:

- **§4**, bảng lớp:
  - Dòng `Fact bất biến`: cột Ví dụ `fact member` → `fact member (\`member_actions\`, D96)`; cột Ghi `ghim không mang \`base_pv\`, server ghi pv hiện tại + 1 (D92)` → `ghim không mang \`base_pv\`, server ghi pv hiện tại + 1 (D92); lệnh member là trạng thái mong muốn, server ghi mv hiện tại + 1 (D96)`.
  - Dòng `State có version (projection)`: cột Ví dụ `` `messages` hiện tại, `rooms.pins`, `user_rooms`, room activity `` → `` `messages` hiện tại, `rooms.pins`, `members`, `user_rooms`, room activity ``; cột Ghi `(ghim: fold fact sau \`pv\` + CAS \`pv == p\`, D92)` → `(ghim: fold fact sau \`pv\` + CAS \`pv == p\`, D92; member: upsert từng doc guard \`mv < k\` rồi CAS đầu \`rooms.mv == p\`, lệnh settle trước khi kiểm quyền, D97)`.
  - Dòng `Aggregate theo target`: cột Ví dụ `` số reaction theo emoji, `thread_count`, `member_count` `` → `` số reaction theo emoji, `thread_count` (`member_count` gập từ `n` của fact member, không recount, D98) ``.
  - Thay cả dòng `Tần suất cao gộp được` bằng:

```markdown
| Tần suất cao gộp được | vị trí đọc, đánh dấu chưa đọc | `members.rs {s, v}`: `MarkRead` chỉ nâng, `MarkUnread` chỉ hạ, mỗi lần đổi `v+1` (ai tới sau thắng, D104) | `read_updated` id `{room}-rd-{u}-v{v}` qua `readcast`: lần đổi đầu gửi ngay, phần đuôi gộp trong `READ_RECEIPT_WINDOW` mỗi (room, user) (D105) | Không ack mark, không reconciler (best-effort) |
```

- **§5**, bảng collection:
  - Dòng `rooms`: cột Trường chính `member_count \`{n, ver}\`` → `\`mc\` (member_count, gập từ fact member, D98) + \`mv\` (đầu fact member đã áp, CAS, D97)`; cột Trạng thái `pins/pv, M2b.3)` → `pins/pv, M2b.3; mv/mc theo fact member, M2b.4)`.
  - Thay cả dòng `members` bằng:

```markdown
| `members` | ObjectId | Projection từ fact member + vị trí đọc | `r`, `t`, `u`, `ro` (owner/admin/member), `ja`, `mv` (fact cuối đã áp), `st` (1 = đã rời/bị xoá; tombstone, doc không bao giờ xoá), `cb` (cleared_before_seq), `rs {s, v}` (vị trí đọc, D104); muted_until chưa có | `{r, u}` unique; `{t, u, r}`; `{r, st, ro, ja, u}` (kế nhiệm owner, `Owners`) | Đã xây (tạo; `cb` M2b.2; `st/mv/rs` M2b.4) |
```

  - Thay cả dòng `user_rooms` bằng:

```markdown
| `user_rooms` (clustered) | `tenant 0x00 user 0x00 room(8)` (D101) | Projection từ fact member | `t`, `u`, `r`, `ro`, `ja`, `mv`, `st` (tombstone) | — (danh sách phòng của user là range trên `_id`) | Đã xây (projection + `MemberReader.UserRooms`, M2b.4; RPC `ListMyRooms` ở M3) |
```

  - Thêm dòng ngay sau dòng `pin_actions`:

```markdown
| `member_actions` (clustered) | 16B `room│mv` | Fact | `r`, `t`, `op` (1 thêm, 2 xoá, 3 rời, 4 đổi role), `ch [{u, ro, pr, rs}]`, `by`, `ts`, `n` (số member sau fact), `so` (owner kế nhiệm); mv dày (D96) | `{r, ts}` (D70) | Đã xây (M2b.4) |
```

- **§5.1**:
  - Dòng đầu: `Collection lớn (\`messages\`, \`message_edits\`, \`pin_actions\`, \`reactions\`)` → `Collection lớn (\`messages\`, \`message_edits\`, \`pin_actions\`, \`reactions\`, \`member_actions\`)`.
  - Thay cả dòng `| \`user_rooms\` shard theo \`u\` | …` bằng:

```markdown
| `user_rooms` clustered, `_id = tenant 0x00 user 0x00 room`, shard key `{_id: 1}` (D101, thay `u│r` của D72) | "Phòng của tôi" là một range trên `_id` trên một shard; user chỉ duy nhất trong tenant; ident hợp lệ không chứa `0x00` nên thứ tự byte giữ (tenant, user, room) và user `ab` không khớp tiền tố của `abc` |
```

- **§6.2**, mục 7 `7. \`CreateRoom\` ghi room trước, member sau (D35).` → `7. \`CreateRoom\` ghi room trước (\`mc\` = số member, chưa \`mv\`), rồi fact member mv 1 (\`domain.InitialMembers\`), projection \`members\`/\`user_rooms\`, CAS đầu \`mv = 1\` (D35, D96); số member mỗi lệnh ≤ \`MEMBER_BATCH_MAX\` (D107); fast path phát \`room_created\` + \`member_added\` (bản room và bản user). Bước sau lỗi thì worker vá từ fact.`
- **§6.3**:
  - Tiêu đề `### 6.3 Lệnh đổi: fact + projection [Đã xây sửa/xoá M2b.2, ghim M2b.3]` → `### 6.3 Lệnh đổi: fact + projection [Đã xây sửa/xoá M2b.2, ghim M2b.3, member M2b.4]`.
  - Thêm đoạn ngay sau đoạn `**Đã xây (M2b.3, D92, D94):** …`:

```markdown
**Đã xây (M2b.4, D96–D100, D103, D107):** `AddMembers`/`RemoveMember`/`LeaveRoom`/`ChangeMemberRole` → `mutate`: validate (user hợp lệ; `AddMembers` khử trùng, 1..`MEMBER_BATCH_MAX`, không thì `INVALID_ARGUMENT` (`ErrTooManyMembers`); `RemoveMember(self)` → `INVALID_ARGUMENT`) → `Admit` (`LeaveRoom` dùng `AdmitRoom`: chỉ tenant, nên rời lần nữa là no-op) → DM → `FAILED_PRECONDITION` (`ErrDirectRoom`) → (thêm: `Messages.Last` một lần, làm `ReadSeq` của người vào) → tối đa 3 lượt: `memberproj.Settle` (áp mọi fact sau `rooms.mv`, CAS đầu) → `MembersOf(caller + đích)` trên đúng mv của đầu → caller không active → `PERMISSION_DENIED` (`LeaveRoom`: no-op) → `Allow` (`add_members`, `remove_member`, `leave_room`, `change_member_role`; `Request.Target`, `Request.Role`; mặc định: owner mọi việc, admin thêm người và xoá member thường, chỉ owner đổi role hay xoá admin/owner, ai cũng tự rời; nhiều owner, D99) → đúng trạng thái sẵn → thành công, không fact, không event (thêm người đã active, xoá người đã đi; đổi role của người không phải member → `NOT_FOUND`) → owner cuối tự hạ → `FAILED_PRECONDITION` (`ErrLastOwner`, `Owners(room, 2)`); owner cuối rời → kế nhiệm (`Successor`: admin `ja` sớm nhất, rồi member, hoà theo user id) ghi vào fact (D100) → `Append` fact mv = đầu + 1 với `n` = số member sau fact → trùng khoá: fact ở mv đó cùng op, người làm, tập user là kết quả, khác thì lượt sau; hết lượt → `UNAVAILABLE` (`ErrRetryLater`). Sau append: `Project(room, mv)` (lỗi bỏ qua), `Router.ForgetMembers(room)` (cache actor, D103), enqueue event member (§6.4, D102), trả `{member_version, member_count, changed, added | new_owner | previous_role}`. Projection (D97): áp một fact = hai `BulkWrite(ordered: false)` upsert từng doc `members` rồi `user_rooms` guard `mv < k` (trùng khoá = fact mới hơn đã áp), rồi CAS đầu `rooms {mv == p} → {mv, mc: n}` (D98); đầu chỉ tiến sau khi mọi doc đã áp, nên đọc `rooms.mv` là biết projection đủ. Xoá/rời = tombstone `st: 1`; thêm lại bỏ `st`, đặt role, `ja` = thời điểm fact, `mv`, giữ `cb`, nâng `rs.s` lên `ReadSeq` của fact, `rs.v + 1`. Ngân sách thêm k ≤ 500 ở group 5K: 2 read admit + settle (1 `Get` + 1 range fact) + 1 `MembersOf` (k+1 khoá) + 1 `Last` + 1 insert + 2 bulk k upsert + 1 CAS + (1+k) event; xoá/rời/đổi role như trên với k = 1 (owner cuối rời thêm ≤ 3 read index). Mọi lệnh member của một room đi tuần tự qua mv dày (~100–200 lệnh/s); bão người vào channel 200K gặp `ErrRetryLater`, gom lệnh để milestone Channel (D107). Đã biết: core chết giữa fact và projection → quyền cũ còn tới khi worker `member_projection` hoặc lệnh member sau settle (thường vài giây).
```

- **§6.4**:
  - Tiêu đề `### 6.4 Tập và vị trí đọc [Đã xây ẩn + clear M2b.2, reaction M2b.3; còn lại chưa]` → `### 6.4 Tập và vị trí đọc [Đã xây ẩn + clear M2b.2, reaction M2b.3, member + vị trí đọc M2b.4]`.
  - Thay gạch `- Member: fact thêm/bớt/rời/đổi role; effect cập nhật \`user_rooms\`, touch \`member_count\`, event \`member_added\`/\`member_removed\` trên subject user để gateway sub/unsub.` bằng:

```markdown
- Member [Đã xây, M2b.4, D96–D103]: fact `member_actions` + projection (§6.3). Event `member_added {members, member_version, member_count}`, `member_removed {user, reason removed|left, new_owner, member_version, member_count}`, `member_role_changed {user, role, previous_role, member_version}`: bản room id `{room}-m{mv}` trên subject room, và bản user id `{room}-m{mv}-{u}` cho mỗi user bị thêm/xoá/rời/đổi role và owner kế nhiệm, envelope `recipient = u` → subject `evt.{t}.user.{u}.{type}` (gateway sub/unsub room theo bản user; chưa ai sub subject của room mới). Stream RePublish `evt.*.*.*.*` → `live.{1}.{2}.{3}.evt.{4}` (JetStream chỉ một luật mỗi stream; subject room ra như cũ). Không ack mark; worker `member_event` gửi lại mọi bản, stream bỏ trùng theo id (D102). Người rời/bị xoá mất quyền đọc (`Admit` từ chối doc có `st`), được thêm lại thì đọc lại toàn bộ lịch sử; gửi tin bị từ chối ngay trên core chạy lệnh (`ForgetMembers`) và trong ≤ 10s ở core khác (TTL cache actor, D103).
```

  - Thay gạch `- Vị trí đọc: \`members {$max read_seq}\`; event \`read_updated\` coalesce. …` (tới hết gạch, kể cả câu về SDK) bằng:

```markdown
- Vị trí đọc [Đã xây, M2b.4, D104, D105]: `members.rs {s, v}`. `MarkRead(seq)` = `FindOneAndUpdate({r, u, st: null, rs.s < seq}, {rs.s = seq, rs.v + 1})` (chỉ nâng; seq 0 hoặc quá seq cuối = tin mới nhất); `MarkUnread(seq)` cùng dạng với `rs.s > seq−1` → `rs.s = seq−1` (chỉ hạ, kiểu Slack); không đổi → trả vị trí hiện tại, không ghi, không event; mỗi lần đổi `v+1` nên ai tới sau thắng và id event không lặp. Kẹp theo seq cuối thật (`rooms.ls` của `Admit` khi seq ≤ nó, không thì `Messages.Last`; không bao giờ kẹp theo `ls` coalesce). Vào/thêm lại: `rs.s` = max với seq cuối lúc vào (ghi trong fact), nên người mới thấy toàn bộ lịch sử và tin cũ coi như đã đọc. Quyền `mark_read` (cả `MarkUnread`). Không phải fact, không reconciler. Event `read_updated {user, seq, version}` id `{room}-rd-{u}-v{v}` qua `readcast` (một goroutine, map `(room, user)` có trần 65536): lần đổi đầu gửi ngay, phần đuôi gộp giữ `v` lớn nhất và gửi sau `READ_RECEIPT_WINDOW` (3s); subject room khi DM hoặc `mc ≤ READ_RECEIPT_MAX_MEMBERS` (100), không thì chỉ subject user (đồng bộ thiết bị của chính user); map đầy hoặc sau `Close` → gửi thẳng, đếm `read_events_unbatched_total` (chẩn đoán, không luật). Best-effort: event mất không có đường bù. Đếm unread để M3 (§9.3). SDK nâng `read_seq` khi user gửi tin, để tin của chính mình không tích sau `rs`.
```

- **§7**: `Áp cho số reaction theo emoji, \`thread_count\`, \`member_count\` (D67). Không áp cho unread.` → `Áp cho số reaction theo emoji, \`thread_count\` (D67). Không áp cho unread, cũng không cho \`member_count\`: số member gập thẳng từ \`n\` của fact member (D98).`
- **§8.2**: `\`reactions\`, \`pin_actions\` và update/replace của \`reactions\`` → `\`reactions\`, \`pin_actions\`, \`member_actions\` và update/replace của \`reactions\``; `\`p:{room}-p{pv}\`)` (trong danh sách id record) → `\`p:{room}-p{pv}\`, \`g:{room}-m{mv}\` (kind \`MemberInserted\` 6, \`Seq = mv\`, D106))`. Thêm câu cuối đoạn: `Update của \`members\`, \`user_rooms\` và đầu \`rooms.mv/mc\` không vào feed: fact là nguồn duy nhất.`
- **§8.3**:
  - Tiêu đề `### 8.3 Effect engine [Đã xây phần M2b.1, M2b.2, M2b.3]` → `### 8.3 Effect engine [Đã xây phần M2b.1–M2b.4]`.
  - Bảng chính sách, dòng `delay`: cuối cột Ví dụ thêm `; projection member 0, \`member_event\` = \`RECONCILE_DELAY\``. Dòng `coalesce`: `\`read_updated\` N giây` → `\`read_updated\`: lần đổi đầu gửi ngay, phần đuôi gộp trong \`READ_RECEIPT_WINDOW\` (3s) mỗi (room, user), ngoài work stream (\`readcast\`, D105)`.
  - Topo (a) mục 1: `(\`m:\`, \`r:\`, \`e:\`, \`x:\`, \`p:\`; §8.2)` → `(\`m:\`, \`r:\`, \`e:\`, \`x:\`, \`p:\`, \`g:\`; §8.2)`.
  - Bảng registry: thêm ba dòng ngay sau dòng `   | \`PinInserted\` | \`pin_event\` | …`:

```markdown
   | `MemberInserted` | `room_activity` | 0 | — | `Activity{Seq: 0}`: chỉ nâng `lc`/`ab` |
   | `MemberInserted` | `member_projection` | 0 | — | mỗi room một lần mỗi lô: `memberproj.Project(room, max mv)` (áp từng fact: upsert guard `mv < k`, rồi CAS đầu), rồi `Router.ForgetMembers`; chỉ room không còn → drop có đếm; dữ liệu hỏng hoặc fact chưa thấy → Nak (hiện ở `work_failures_total`) |
   | `MemberInserted` | `member_event` | `RECONCILE_DELAY` | không | `At` fact + loại room → bản room + mọi bản user (`pbconv.MemberEvents`); record chỉ ack khi mọi bản có PubAck; chỉ đếm republish khi PubAck không phải bản trùng; fact/room không còn → drop có đếm |
```

  - Đoạn **Resync (D69)**: sau câu `… cùng cách phân trang (\`ErrReactionPageFull\`, \`ErrPinPageFull\`).` thêm ` M2b.4: cuối cùng quét \`member_actions\` (record \`MemberInserted\`, \`Seq = mv\`, \`ErrMemberPageFull\`); room tạo trong khoảng cho thêm record fact mv 1 của nó.`; `Fact sửa, reaction và ghim cũng chạy \`room_activity\`` → `Fact sửa, reaction, ghim và member cũng chạy \`room_activity\``; `room **chỉ** có reaction/ghim trong khoảng đó` → `room **chỉ** có reaction/ghim/fact member trong khoảng đó`.
- **§9.3**: `- **Phòng của tôi:** đọc \`user_rooms\` theo prefix \`u\` (D72).` → `- **Phòng của tôi:** đọc \`user_rooms\` theo prefix \`tenant 0x00 user 0x00\` (D101; projection và \`MemberReader.UserRooms\` có từ M2b.4, RPC \`ListMyRooms\` ở M3); unread đếm từ \`members.rs\` (D104).`
- **§10**: cuối gạch `- Dữ liệu riêng của user chỉ đi subject user (H8). \`member_removed\` → unsubscribe ngay; khoảng rò bằng độ trễ thu hồi.` thêm ` Core phát bản user của event member (\`recipient\`, D102) và \`read_updated\` của group lớn (D105) từ M2b.4.`
- **§11**:
  - `- \`CHATIM_EVT\`: subject \`evt.>\`, R3, lưu file, 7 ngày, chống trùng 5m (phải dài hơn delay lớn nhất của effect).` → `- \`CHATIM_EVT\`: subject \`evt.>\`, R3, lưu file, 7 ngày, chống trùng 5m (phải dài hơn delay lớn nhất của effect); RePublish \`evt.*.*.*.*\` → \`live.{1}.{2}.{3}.evt.{4}\` (subject room và subject user, D102).`
  - `\`chatim.events.v1.Event{id, tenant, room_id, room_type, thread, seq, type, actor, ts, oneof payload}\`` → `\`chatim.events.v1.Event{id, tenant, room_id, room_type, thread, seq, type, actor, ts, recipient, oneof payload}\` (\`recipient\` khác rỗng → subject user \`evt.{t}.user.{u}.{type}\`, D102)`.
- **§12**: thêm dòng ngay sau dòng CT1 (câu đầu giữ `(16 luật)`):

```markdown
| MB1 | Projection member (`members`, `user_rooms`, đầu `rooms.mv/mc`) cuối cùng bằng fold mọi fact `member_actions`; mỗi fact có bản room và bản user trên stream | Lệnh settle trước khi kiểm quyền; worker `member_projection` (delay 0) chạy lại projection từ mọi fact rồi `ForgetMembers`; `member_event` phát lại mọi bản; room hoặc fact không còn → drop có đếm, fact không giải mã được → retry (hiện ở `work_failures_total`); core chết giữa fact và projection → quyền cũ tới lần settle/worker kế (vài giây) | `effect_dropped_total{effect="member_projection"\|"member_event"}`, `reconcile_republished_total{effect="member_event"}`, `work_failures_total`; alert `ChatimEffectDropping`, `ChatimWorkFailing`, `ChatimRepublishSurge` (luật theo nhãn chung, không luật mới) |
```

- **§13**, gạch Vòng đời core: `Khởi động: publisher → flusher` → `Khởi động: publisher → read events (\`readcast\`) → flusher`; `Dừng: \`/readyz\` false → drain → gRPC → reader` → `Dừng: \`/readyz\` false → drain → gRPC → read events (1s, xả \`read_updated\` đang gộp vào publisher) → reader`; `(26.2s/28s;` → `(27.2s/28s;`.
- **§14**: dòng `| Core chết giữa fact và projection | … reaction: worker \`reaction_counter\` đếm lại sau \`REACTION_COUNT_DELAY\` |` → thêm cuối cột `; member: lệnh member sau settle hoặc worker \`member_projection\` (delay 0) áp fact, quyền cũ còn tới lúc đó (D97)`.
- **§16**: thêm dòng ngay trước dòng `| Soft ownership không fencing | …`:

```markdown
| Bão người vào một room (channel 200K) | Trung bình | Thay đổi member một room đi tuần tự qua mv dày (~100–200 lệnh/s, `ErrRetryLater` khi tranh); gom lệnh ở milestone Channel (D107) |
```

- **§17.2**:
  - Dòng D67: cuối cột Quyết định `(tinh chỉnh bởi D90: witness thay \`afterClusterTime\`)` → `(tinh chỉnh bởi D90: witness thay \`afterClusterTime\`; \`member_count\` không recount mà gập từ fact, D98)`.
  - Dòng D72: `\`user_rooms {u│r}\`` → `\`user_rooms {u│r}\` (khoá thay bởi D101: \`tenant 0x00 user 0x00 room\`)`.
  - Thêm mười hai dòng ngay sau dòng `| D95 | …`:

```markdown
| D96 | Fact member `member_actions` clustered `_id = room│mv` (16B), mv dày = đầu + 1 (khoá là CAS); fact mang `op` (1 thêm, 2 xoá, 3 rời, 4 đổi role), `ch [{u, ro, pr, rs}]`, `by`, `ts`, `n` (số member sau fact), `so` (owner kế nhiệm); `CreateRoom` = fact mv 1 (`domain.InitialMembers`); lệnh là trạng thái mong muốn: đúng sẵn → thành công, không fact, không event; trùng khoá → fact ở mv đó cùng op, người làm, tập user là kết quả, khác thì thử lại ≤3 rồi `ErrRetryLater` | Ghi thẳng `members` không fact; `base_mv` từ client; fact riêng cho từng user | Fact cho worker sửa projection, dựng lại event và resync theo `{r, ts}`; mv dày làm khoá thành CAS giữa các lệnh đồng thời (như ghim, D92) nên kiểm quyền và owner cuối chính xác; một fact cho cả lô thêm giữ 1 insert mỗi lệnh |
| D97 | Projection settle-first: lệnh gọi `memberproj.Settle` (áp mọi fact sau `rooms.mv`) trước khi đọc caller/đích và hỏi `Allow`; áp fact = upsert từng doc `members` rồi `user_rooms` guard `mv < k` (trùng khoá = fact mới hơn đã áp), rồi CAS đầu `rooms {mv == p} → {mv, mc}`; fast path và worker `member_projection` dùng chung `memberproj`; xoá = tombstone `st`, doc không bao giờ xoá; lỗi projection sau khi fact commit không làm lệnh lỗi | Fold trong RAM rồi kiểm; chỉ CAS đầu, không guard từng doc; xoá doc khi rời; transaction | Quyền phải kiểm trên đúng trạng thái ở mv của đầu (`Admit` chỉ là cổng sớm, có thể cũ); thiếu guard từng doc thì projector chậm thêm lại người đã bị xoá; đầu chỉ tiến sau khi mọi doc đã áp; tombstone giữ `cb`, `rs`, `mv` cho lần vào lại. Đã biết: core chết giữa fact và projection → quyền cũ còn tới lần settle/worker kế (vài giây) |
| D98 | `member_count` (`rooms.mc`) gập từ `n` của fact, ghi cùng CAS đầu; không recount, không witness (tinh chỉnh D67 cho member) | Recount CAS-ver với witness (D67/D90); `$inc` | Mỗi fact là một thay đổi thật, kiểm trên trạng thái đúng ở mv trước, nên `n` chính xác; recount O(N) vô ích ở group 5K và channel 200K |
| D99 | Action `add_members`, `remove_member`, `leave_room`, `change_member_role`, `mark_read` (cả `MarkUnread`); `access.Request` thêm `Target` (member đích, zero khi không phải member) và `Role` (role yêu cầu); `DefaultPolicy`: owner mọi việc, admin thêm người và xoá member thường, chỉ owner đổi role hay xoá admin/owner, ai cũng tự rời, `mark_read` mọi member; một group nhiều owner; `MESSAGE_LOCKED_KINDS` không áp | Luật cứng trong `mutate`; một owner duy nhất | Owner chốt 2026-10-06, như D86: luật nằm ở `access.Policy`, module policy chat (Phase 2) thay được |
| D100 | DM cố định 2 người: thêm/xoá/rời/đổi role → `FAILED_PRECONDITION` (`ErrDirectRoom`); `RemoveMember(self)` → `INVALID_ARGUMENT` (rời dùng `LeaveRoom`); owner cuối tự hạ role → `FAILED_PRECONDITION` (`ErrLastOwner`); owner cuối rời → kế nhiệm = admin `ja` sớm nhất, không có thì member `ja` sớm nhất, hoà theo user id (`domain.Successor`), ghi trong fact (`so`) và event (`new_owner`); `LeaveRoom` chỉ kiểm tenant khi admit (`Checker.AdmitRoom`) nên rời lần nữa là no-op | Room không còn owner; cấm owner cuối rời; kế nhiệm theo thứ tự chữ | Owner chốt 2026-10-06; ghi kế nhiệm trong fact để fold tất định và event mang owner mới; rời lặp lại không thành `PERMISSION_DENIED` |
| D101 | `user_rooms` clustered `_id = tenant 0x00 user 0x00 room(8)` (`keys.UserRoom`), doc `{t, u, r, ro, ja, mv, st}`, tombstone `st`; danh sách phòng của user là range trên `_id` (`MemberReader.UserRooms`); shard key tương lai `{_id: 1}`; thay khoá `u│r` của D72 | `u│r` (D72); ObjectId + index `{t, u, r}` | User chỉ duy nhất trong tenant; tiền tố độ dài thay đổi làm user `ab` khớp `abc…`; ident hợp lệ không chứa `0x00` nên thứ tự byte giữ (tenant, user, room) và không tiền tố nào trùng tiền tố khác |
| D102 | Event member `member_added`, `member_removed` (reason removed/left, `new_owner`), `member_role_changed`: bản room id `{room}-m{mv}` + bản user id `{room}-m{mv}-{u}` cho mỗi user bị thêm/xoá/rời/đổi role và owner kế nhiệm; envelope `recipient = 10` → subject `evt.{t}.user.{u}.{type}`; RePublish `evt.*.*.*.*` → `live.{1}.{2}.{3}.evt.{4}`; không ack mark. Rolling deploy (prod chưa live): `EnsureStream` chạy mỗi lần core khởi động nên core cũ khởi động lại trả luật về `evt.*.room.*.*` và bản user ngừng tới `live.*.user.*`; nâng mọi core cùng lúc; dev bị NATS từ chối sửa luật thì `make infra-reset` | Chỉ bản room; cùng id cho bản room và bản user; stream hoặc luật riêng cho user | Gateway cần bản user để sub/unsub room (chưa ai sub subject của room mới); JetStream chỉ một luật RePublish mỗi stream; cùng id thì stream bỏ bản sau trong cửa sổ chống trùng |
| D103 | Cache member của actor: thế hệ `memberGen` (`Router.ForgetMembers(room)` tăng khi actor có; lệnh member và worker `member_projection` gọi sau projection) + TTL 10s mỗi entry; không cache "không phải member" | Cache không hết hạn (trước M2b.4); đọc store mỗi lần gửi; pub/sub huỷ cache giữa các core | Người bị xoá phải mất quyền gửi ngay trên core chạy lệnh; TTL chặn trường hợp lệnh hay worker chạy trên core khác (slot đổi chủ, partition work khác slot của room) mà không thêm read nào trên đường gửi |
| D104 | Vị trí đọc `members.rs {s, v}`: `MarkRead(seq)` chỉ nâng (seq 0 hoặc quá seq cuối = tin mới nhất), `MarkUnread(seq)` chỉ hạ về `seq−1`, mỗi lần đổi `v+1` (ai tới sau thắng); kẹp theo seq cuối thật (`Messages.Last`, không theo `ls` coalesce); vào/thêm lại: `rs.s = max(rs.s, seq cuối lúc vào)` ghi trong fact, `v+1`; không phải fact, không reconciler; đếm unread để M3 | `$max read_seq` + cờ `marked_unread {v}` riêng; đặt `seq−1` vô điều kiện; reconciler cho vị trí đọc | Owner chốt 2026-10-06: người mới thấy toàn bộ lịch sử, tin cũ coi như đã đọc; "chưa đọc" kiểu Slack; đặt vô điều kiện có thể đẩy vị trí lên; `v` chỉ tăng (vào lại cũng `+1`) nên id event không lặp |
| D105 | Event `read_updated {user, seq, version}` id `{room}-rd-{u}-v{v}` qua package `readcast`: một goroutine, lần đổi đầu mỗi (room, user) gửi ngay, phần đuôi gộp (giữ `v` lớn nhất) trong `READ_RECEIPT_WINDOW` (3s, 100ms..1m); subject room khi DM hoặc `mc ≤ READ_RECEIPT_MAX_MEMBERS` (100, 1..1000), không thì subject user (`recipient`); map có trần 65536, đầy hoặc sau `Close` → gửi thẳng, đếm `read_events_unbatched_total`; best-effort; bước dừng `read events` 1s ngay sau gRPC (kế hoạch dừng 26.2s → 27.2s của 28s) | Phát mọi lần đổi; event riêng mỗi thiết bị; reconciler cho read | Owner chốt 2026-10-06: "đã xem" cho DM và group ≤ 100; group lớn chỉ đồng bộ thiết bị của chính user; gộp giữ ≤ 2 event mỗi cửa sổ mỗi (room, user) |
| D106 | Feed thêm insert của `member_actions`: kind `MemberInserted` (6), `Change.Member`, record `Seq = mv`, id `g:{room}-m{mv}`; registry `room_activity` (0, chỉ `lc/ab`), `member_projection` (0, một `Project` mỗi room mỗi lô, rồi `ForgetMembers`), `member_event` (`RECONCILE_DELAY`, mọi bản; record chỉ ack khi mọi bản có PubAck; chỉ đếm republish khi PubAck không phải bản trùng); resync quét `member_actions` theo `{r, ts}` (`ErrMemberPageFull`); không luật alert mới (MB1 dùng `ChatimEffectDropping`, `ChatimWorkFailing`, `ChatimRepublishSurge`); core cũ `Nak` kind 6 (D91) nên nâng mọi core cùng lúc | Effect riêng cho mỗi bản user; feed update của `members`; luật alert riêng cho member | Fact là nguồn duy nhất nên chỉ cần insert; một record mỗi fact giữ work stream nhỏ; luật theo nhãn chung đã phủ effect mới |
| D107 | Bỏ trần 5000 member trong `domain.NewRoom`; trần mỗi lệnh `MEMBER_BATCH_MAX` (500, 2..1000; 1 sẽ chặn mọi DM) cho `CreateRoom` và `AddMembers` (`ErrTooManyMembers`, `INVALID_ARGUMENT`); thay đổi member trong một room đi tuần tự (mv dày, ~100–200 lệnh/s); gom lệnh cho channel để milestone Channel | Trần cứng số member mỗi group; gom lệnh ngay | Owner chốt 2026-10-06: core không giới hạn số member; trần mỗi lệnh chặn fact và event quá lớn; bão người vào channel 200K cần gom, group chưa cần |
```

**Step 3: Roadmap**

`docs/roadmap.md`:
- Dòng đầu: `> Cập nhật: 2026-10-06 (M2b.3 xong; viết lại 2026-10-05 sau 2 vòng phản biện cơ chế hệ thống)` → `> Cập nhật: <ngày> (M2b.4 xong, M2b.0–M2b.4 chờ một PR merge \`main\`; viết lại 2026-10-05 sau 2 vòng phản biện cơ chế hệ thống)` với `<ngày>` = `date +%Y-%m-%d`; phần link sau giữ nguyên.
- Thay cả dòng bắt đầu bằng `| 1 | M2b.4 — Member + vị trí đọc |` bằng:

```markdown
| 1 | M2b.4 — Member + vị trí đọc | Fact member `member_actions` mv dày, lệnh là trạng thái mong muốn (D96); projection settle-first `members`/`user_rooms` guard `mv < k` + CAS đầu `rooms.mv/mc` (D97); `member_count` gập từ fact (D98, tinh chỉnh D67); quyền owner/admin/member, nhiều owner, kế nhiệm owner cuối, DM cố định (D99, D100); `user_rooms {tenant│user│room}` (D101, thay khoá D72); event member bản room + bản user, RePublish `evt.*.*.*.*` (D102); cache member của actor theo thế hệ + TTL (D103); vị trí đọc `rs {s, v}` chỉ nâng/chỉ hạ (D104); `read_updated` gộp qua `readcast` (D105); feed/effect/resync member (D106); bỏ trần 5000, `MEMBER_BATCH_MAX` (D107) | ✅ `dev-done` (trên `feat/m2b`, chưa merge `main`) — [plan](plans/2026-10-06-m2b4-members-read.md), [tóm tắt](plans/2026-10-06-m2b4-members-read-summary.md); 6 RPC, package `memberproj` + `readcast`, 2 effect mới, `read_events_unbatched_total`, vẫn 16 luật alert, resync quét `member_actions`, e2e phase 5 (D96–D107) |
```

- Dòng M2c: cột Trạng thái `Chưa` → `⏭ Tiếp theo — cần plan (sau khi merge \`feat/m2b\` vào \`main\`)`.
- Dòng M3: `\`ListMyRooms\` qua \`user_rooms\`` → `\`ListMyRooms\` qua \`user_rooms\` (projection + \`MemberReader.UserRooms\` có từ M2b.4, D101); unread tính từ \`members.rs\` (D104)`.
- Dòng M4: `\`member_removed\` → unsubscribe` → `\`member_removed\` → unsubscribe (bản user của event member và \`read_updated\` có từ M2b.4, D102, D105)`.
- Dòng M5: `mục mang sang từ M2b.0, M2b.1, M2b.2 và M2b.3 (xem dưới)` → `mục mang sang từ M2b.0–M2b.4 (xem dưới)`.
- Dòng Channel: sau `bắt buộc trước khi channel go-live)` thêm `; gom lệnh member khi bão người vào (thay đổi member của một room đi tuần tự ~100–200 lệnh/s, D107)`.
- Mục "Thứ tự phụ thuộc", sau gạch `(owner 2026-10-05) M2b.0 → M2b.4 làm trên một nhánh chung …` thêm gạch: `- M2b.4 xong (\`dev-done\`): mở một PR \`feat/m2b\` → \`main\` (M2b.0–M2b.4, merge commit, mức \`dev-done\`) khi owner đồng ý; nhánh của M2c tạo từ \`main\` sau khi merge.`
- Mục "Mục mang sang M5 (hardening)", sau khối "Từ M2b.3 …" thêm:

```markdown
Từ M2b.4, chi tiết ở [plan, mục Kết quả thực thi](plans/2026-10-06-m2b4-members-read.md#kết-quả-thực-thi):

- Core chết giữa fact member và projection: quyền cũ (người vừa bị xoá vẫn đọc/gửi) còn tới khi worker `member_projection` hoặc lệnh member sau settle, thường vài giây (D97).
- Cache member của actor ở core khác core chạy lệnh (slot đổi chủ, worker trên partition khác) chỉ hết theo TTL 10s (D103).
- Rolling deploy: core cũ khởi động lại ghi đè RePublish về `evt.*.room.*.*` (bản user ngừng tới `live.*.user.*`), `Nak` record kind 6 và không lọc `members.st`; nâng mọi core cùng lúc (D102, D106).
- Room tạo trước M2b.4 không có `mv`/`user_rooms`: lệnh member đầu tiên ghi mv 1 trên `mc` cũ; `UserRooms` thiếu các room đó cho tới khi có công cụ backfill (prod chưa live).
- Mọi lệnh member của một room đi tuần tự (~100–200 lệnh/s); bão người vào channel gặp `ErrRetryLater` (D107, milestone Channel).
- `read_updated` best-effort, không đường bù; map `readcast` đầy thì gửi không gộp (`read_events_unbatched_total`).
- Resync: `ErrMemberPageFull` khi một thời điểm có ≥ 1000 fact member của một room; room chỉ có fact member trong khoảng mất cần `-room`.
- (các Minor controller ghi ở mục Kết quả thực thi)
```

- Mục "Mức sẵn sàng": sau câu `M0–M2a.3: \`dev-done\`, đã merge vào \`main\` (M2a.2 + M2a.3 cùng PR #11).` thêm ` M2b.0–M2b.4: \`dev-done\` trên \`feat/m2b\`, chờ một PR merge vào \`main\`.`

**Step 4: README**

`README.md`:
- Dòng "Trạng thái": `lịch sử sửa) và M2b.3 (` → `lịch sử sửa), M2b.3 (`; `projection) xong trên nhánh \`feat/m2b\`; tiếp theo là M2b.4 theo` → `projection) và M2b.4 (member theo fact \`member_actions\` + projection \`members\`/\`user_rooms\`, event member trên subject room và user, vị trí đọc chỉ nâng/chỉ hạ + "đã xem" gộp) xong trên nhánh \`feat/m2b\`, chờ một PR merge vào \`main\`; tiếp theo là M2c theo`.
- Bảng "Kiến trúc", dòng `core`: `ghim là fact \`pin_actions\` + projection \`rooms.pins\`; reader đọc change stream (\`messages\`, \`rooms\`, \`message_edits\`, \`reactions\`, \`pin_actions\`) đẩy record vào work stream, worker ở mọi core chạy effect (event bù, room activity, projection sửa/xoá/ghim, đếm lại reaction)` → `ghim là fact \`pin_actions\` + projection \`rooms.pins\`; member là fact \`member_actions\` + projection \`members\`/\`user_rooms\` (owner/admin/member, event bản room và bản user), vị trí đọc \`members.rs\` + \`read_updated\` gộp; reader đọc change stream (\`messages\`, \`rooms\`, \`message_edits\`, \`reactions\`, \`pin_actions\`, \`member_actions\`) đẩy record vào work stream, worker ở mọi core chạy effect (event bù, room activity, projection sửa/xoá/ghim/member, đếm lại reaction)`.
- "Cấu trúc": `internal/{actor,mutate,counter,pinproj,view,access,` → `internal/{actor,mutate,counter,pinproj,memberproj,readcast,view,access,`.
- "Lệnh hay dùng": dòng `make e2e`: `phase 4 react seq 3 rồi đổi emoji, ghim seq 4)` → `phase 4 react seq 3 rồi đổi emoji, ghim seq 4; phase 5 thêm/nâng admin/xoá member, chưa đọc rồi đã đọc, owner cuối rời rồi vào lại, DM cố định)`; thêm ngay sau dòng `docker run … react -room ID …`:

```
    docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev add-members -room ID -users a,b   # thêm member; remove-member -room ID -target U, leave -room ID, set-role -room ID -target U -role admin, read -room ID [-seq N], unread -room ID -seq N; watch -user U xem event riêng của user; -tenant/-user chọn người gọi
```

- Đoạn `/app resync`: `Quét timeline chính rồi \`message_edits\`, \`reactions\` (doc hiện tại) và \`pin_actions\` của từng room (theo \`{r, ts}\`).` → `Quét timeline chính rồi \`message_edits\`, \`reactions\` (doc hiện tại), \`pin_actions\` và \`member_actions\` của từng room (theo \`{r, ts}\`).`; `room chỉ có reaction/ghim trong khoảng mất phải chạy với \`-room\` (D91)` → `room chỉ có reaction/ghim/fact member trong khoảng mất phải chạy với \`-room\` (D91, D106)`.
- Bảng env: kiểm ba dòng Task 14 đã thêm (`grep -n "MEMBER_BATCH_MAX\|READ_RECEIPT_WINDOW\|READ_RECEIPT_MAX_MEMBERS" README.md` → 3 dòng). Thiếu → báo controller (Task 14 quên), thêm theo bảng config của hợp đồng chung.

**Step 5: CLAUDE.md**

- Mục Project, sau gạch `- M2b.3 (plan …)` thêm:

```markdown
- M2b.4 (plan `docs/plans/2026-10-06-m2b4-members-read.md`): member changes as dense `member_actions` facts (`room│mv`, desired-state commands; D96) with a settle-first projection on `members` and `user_rooms` (per-doc `mv < k` guard, then a head CAS on `rooms.mv/mc`; `member_count` folded from the fact; D97, D98); owner/admin/member policy with many owners, last-owner rules and succession, DM members fixed (D99, D100); `user_rooms` keyed `tenant 0x00 user 0x00 room` (D101); member events with a room copy and user copies on `evt.{t}.user.{u}` through the envelope `recipient` and a widened RePublish (D102); actor member cache by generation + 10s TTL (D103); read position `members.rs {s, v}` (`MarkRead` raises, `MarkUnread` lowers; D104) and `read_updated` through the `readcast` coalescer (D105); feed kind `MemberInserted`, effects `member_projection`/`member_event`, resync of `member_actions` (D106); `MEMBER_BATCH_MAX` per command, no group size cap (D107); corecli/e2e member phase.
```

- Câu `built on a data-class framework (§4) with decisions D61–D95` → `… D61–D107`. Thay câu đang nói việc tiếp theo (`M2b.3 (reactions + pins) is done on \`feat/m2b\`; next is M2b.4 …`, hoặc bản commit plan đã đổi) bằng `M2b.4 (members + read position) is done on \`feat/m2b\`; next is one PR that merges \`feat/m2b\` (M2b.0–M2b.4) into \`main\` once the owner agrees, then M2c (threads and extras), whose plan is not written yet.`
- Mục Commands: dòng `make e2e …` đổi chú thích thành `# tools/corecli: create, send, history, kill core-1, verify no loss/dup, live events, then edit seq 1 and delete seq 2, then react to seq 3 and pin seq 4, then add/promote/remove members, unread/read and the last owner leaving and coming back`.
- **Key encoding**: thêm hai gạch sau gạch `- pin_actions = \`room│pv\` (16B)` (nếu Part A chưa thêm):

```markdown
- member_actions = `room│mv` (16B)
- user_rooms = tenant bytes, `0x00`, user bytes, `0x00`, room (8B), so one user's rooms are one range (valid tenants and users never contain `0x00`)
```

- **Counters**: thêm gạch cuối: `- \`mv\` is a room's dense member version (\`rooms.mv\` is the projected head; \`members.mv\` and \`user_rooms.mv\` are the last fact applied to each doc); \`rs.v\` versions a member's read position and only rises.`
- **Storage ports**: `\`store.PinProjector\` (\`PinState\`, \`ApplyPins\`) and \`store.ChangeFeed\`` → `\`store.PinProjector\` (\`PinState\`, \`ApplyPins\`), \`store.MemberActions\` (\`Store.MemberActions()\`), \`store.MemberProjector\` (\`ApplyMembers\`, \`AdvanceMembers\`), \`store.MemberReader\` (\`MembersOf\`, \`Successor\`, \`Owners\`, \`UserRooms\`), \`store.ReadPositions\` (\`MarkRead\`, \`MarkUnread\`) and \`store.ChangeFeed\``.
- **Effect engine**:
  - Tiêu đề `D83, D84, D91).**` → `D83, D84, D91, D106).**`.
  - `\`ReactionChanged\`, \`PinInserted\`); the reader imports no driver` → `\`ReactionChanged\`, \`PinInserted\`, \`MemberInserted\`); the reader imports no driver`; `inserts into \`messages\`, \`rooms\`, \`message_edits\`, \`reactions\` and \`pin_actions\`,` → `inserts into \`messages\`, \`rooms\`, \`message_edits\`, \`reactions\`, \`pin_actions\` and \`member_actions\`,`.
  - `\`x:{room}-{thread}-{seq}-{user}-n{n}\` or \`p:{room}-p{pv}\`;` → `\`x:{room}-{thread}-{seq}-{user}-n{n}\`, \`p:{room}-p{pv}\` or \`g:{room}-m{mv}\`;`.
  - Trong gạch Workers, sau `then \`pin_event\` (delay \`RECONCILE_DELAY\`) for \`PinInserted\`.` thêm ` \`room_activity\`, \`member_projection\` (delay 0, one \`memberproj.Project\` per room per batch, then \`Router.ForgetMembers\`) then \`member_event\` (delay \`RECONCILE_DELAY\`, the room copy and every user copy; the record is acked only when every copy has a PubAck) for \`MemberInserted\`.`
  - `- \`CreateRoom\` publishes \`room_created\` on the fast path too;` → `- \`CreateRoom\` publishes \`room_created\` and the \`member_added\` copies of its first member fact on the fast path too;`.
  - `\`reactions\` (current docs) and \`pin_actions\` by \`{r, ts}\`` → `\`reactions\` (current docs), \`pin_actions\` and \`member_actions\` by \`{r, ts}\``.
- Thêm khối mới ngay trước `**Permission hook and reader pipeline (\`access\`, \`view\`).**`:

```markdown
**Members and read position (`mutate`, `memberproj`, `readcast`; D96–D107).**
- `AddMembers`, `RemoveMember`, `LeaveRoom`, `ChangeMemberRole`, `MarkRead` and `MarkUnread` run in `mutate`. Member commands are the desired state: validate → `Admit` (`LeaveRoom` uses `AdmitRoom`, tenant only) → a DM is `FAILED_PRECONDITION` (`ErrDirectRoom`) → up to 3 rounds of `memberproj.Settle` → `MembersOf(caller + targets)` → `Allow` (actions `add_members`, `remove_member`, `leave_room`, `change_member_role`; `Request.Target`, `Request.Role`) → no-op check → last-owner check → append `member_actions {_id: room│mv}` with mv = head + 1 (dense; the key is the CAS); a duplicate key with the same op, actor and users is the result, else the next round, then `ErrRetryLater`. After the append: `Project`, `Router.ForgetMembers`, enqueue events; errors after the fact commit are ignored (the workers converge).
- Default policy (D99): owners do everything; admins add people and remove plain members; only owners change roles or remove admins and owners; anyone leaves. Many owners are allowed; the last owner cannot step down (`ErrLastOwner`); when the last owner leaves, the earliest admin, else the earliest member (ties by user id), becomes owner, recorded in the fact (`so`). `RemoveMember(self)` is `INVALID_ARGUMENT`. `MEMBER_BATCH_MAX` (500) caps `CreateRoom` and `AddMembers`; there is no group size limit (D100, D107).
- Projection (D97, D98): applying a fact bulk-upserts each `members` and `user_rooms` doc guarded by `mv < k` (a duplicate key means a newer fact already applied), then CASes the head `rooms {mv == p} → {mv, mc}`; `mc` is the fact's `n`, never recounted. Removal is the tombstone `st: 1`; a re-add keeps `cb` and raises `rs` to the last seq at joining. `CreateRoom` writes the room, the fact mv 1 (`domain.InitialMembers`), the projection, then the head. `user_rooms` `_id` = `tenant 0x00 user 0x00 room(8)` (D101).
- Read position (D104): `members.rs {s, v}`; `MarkRead` only raises (seq 0 = latest), `MarkUnread(seq)` only lowers to `seq − 1`, each change `v+1`; clamped to the real last seq. `read_updated` (`{room}-rd-{user}-v{v}`) goes through the `readcast` coalescer: the first change of a (room, user) at once, the newest of the rest after `READ_RECEIPT_WINDOW` (3s); the room subject for DMs and groups up to `READ_RECEIPT_MAX_MEMBERS` (100), else the user's own subject (D105). Best-effort, no reconciler.
- Events (D102, no ack marks): `member_added`, `member_removed` (reason removed/left, `new_owner`), `member_role_changed`; a room copy `{room}-m{mv}` plus a user copy `{room}-m{mv}-{user}` for each changed user and the successor, sent with the envelope `recipient` to `evt.{t}.user.{u}.{type}`; the stream RePublishes `evt.*.*.*.*` to `live.{1}.{2}.{3}.evt.{4}`. An old core restarting rewrites the RePublish rule, so upgrade every core together.
- Actor member cache (D103): a generation bumped by `Router.ForgetMembers` plus a 10s TTL per entry; a removed user is denied at once on the core that ran the command and within 10s elsewhere.
- Wiring: `service_wiring.go` builds `memberproj` and passes the router as the forgetter and `readcast` as the read notifier; `member_effects_wiring.go` builds the member effects.
```

- **Permission hook and reader pipeline**: `\`Checker.Admit\` checks tenant and membership only,` → `\`Checker.Admit\` checks tenant and membership only (\`Checker.AdmitRoom\` checks the tenant only and returns a zero member for a non-member, for \`LeaveRoom\`),`; `every other action for any member (D86)` → `every other message action for any member (D86); member actions follow D99`; `edit/delete/hide/clear/react/pin/unpin in \`mutate\`` → `edit/delete/hide/clear/react/pin/unpin, member commands and the read position in \`mutate\``.
- **Detectors**: sau câu `… \`counter_repaired_total{counter}\` counts summaries the workers rewrote.` thêm ` Member effects add \`member_projection\` (only \`effect_dropped_total\`) and \`member_event\` (both); \`read_events_unbatched_total\` counts read receipts sent without coalescing (diagnostic, no rule).`
- **Process lifecycle**: `Start order: publisher → flusher` → `Start order: publisher → read events (\`readcast\`) → flusher`; `→ gRPC → reader (\`RECONCILE_DRAIN + 1s\`)` → `→ gRPC → read events (\`CloseTimeout\` 1s, flushes pending \`read_updated\` into the publisher) → reader (\`RECONCILE_DRAIN + 1s\`)`; `the default plan is 26.2s of 28s` → `the default plan is 27.2s of 28s`.
- **Shard-readiness**: `Small fact collections (\`message_edits\`, \`pin_actions\`)` → `Small fact collections (\`message_edits\`, \`pin_actions\`, \`member_actions\`)`; thêm gạch sau gạch `reactions`: `- \`member_actions\` is clustered by \`room│mv\`; \`user_rooms\` is clustered by \`tenant 0x00 user 0x00 room\`, so one user's rooms stay one range under the future shard key \`{_id: 1}\` (D101); \`members\` keeps \`{r, u}\` unique, \`{t, u, r}\` and \`{r, st, ro, ja, u}\`.`
- **Docs**: `new D61–D95` → `new D61–D107`; trong gạch `docs/plans/`, cuối câu thêm ` M2b.4: \`docs/plans/2026-10-06-m2b4-members-read.md\` (executed; results and known issues at its end).`

**Step 6: INDEXES.csv**

Mỗi task trước sửa dòng của mình trong commit của nó; bước này kiểm, vá chỗ thiếu và sửa các dòng docs.

```bash
for p in apps/core apps/core/internal/memberproj apps/core/internal/readcast apps/core/internal/domain apps/core/internal/store apps/core/internal/store/memstore apps/core/internal/store/mongostore apps/core/internal/store/storetest apps/core/internal/work apps/core/internal/reconcile apps/core/internal/pbconv apps/core/internal/publish apps/core/internal/actor apps/core/internal/access apps/core/internal/mutate apps/core/internal/grpcsrv apps/core/internal/effects apps/core/internal/resync apps/core/internal/config pkg/keys proto/chatim/v1/members.proto tools/internal/route tools/corecli tools/corecli/internal/e2e scripts/e2e.sh; do printf '%s ' "$p"; grep -c "^$p," INDEXES.csv; done
grep -n "^docs/plans/2026-10-06-m2b4" INDEXES.csv | cut -c1-120
grep -n "MemberInserted\|member_actions\|ForgetMembers\|readcast\|MarkUnread\|AddMembers" INDEXES.csv | cut -c1-80
grep -n "D96\|D101\|D107" INDEXES.csv | cut -c1-60
```

Expected: mỗi path đúng `1`; hai dòng plan + tóm tắt M2b.4 (có từ commit plan); grep thứ ba có kết quả ở `store`, `work`, `effects`, `actor`, `mutate`, `grpcsrv`, `readcast`, `resync`, `tools/internal/route`; grep cuối có kết quả. Thiếu dòng nào → thêm/sửa theo hợp đồng chung bằng helper và ghi vào báo cáo task nào đã quên.

Sửa các dòng docs:

```bash
python3 bin/indexes_edit.py docs/designs/261005-chatim-architecture.md purpose = "Single source of truth: requirements (R17 revised) and assumptions needing real numbers; principles P1-P8; data-class framework for every plan; data model and shard rules; built write path; fact + projection mutations (edit/delete built in M2b.2, pins in M2b.3, members in M2b.4: dense member_actions, settle-first projection on members/user_rooms with a head CAS, folded member_count, owner/admin/member policy); reaction set + counter recount CAS-ver with witness (M2b.3); read position rs {s, v} raised by MarkRead, lowered by MarkUnread, read_updated coalesced (M2b.4); effect engine (reader -> work stream -> workers); read path, room list, unread, sync token; gateway; guarantees with detectors (MB1 member projection); fixed reaction emoji list (D95); Decision Log D1-D60 kept, D61-D107 new"
python3 bin/indexes_edit.py docs/designs/261005-chatim-architecture.md decisions = "D1-D107"
python3 bin/indexes_edit.py docs/roadmap.md purpose = "Plan-writing rules (each plan has an owner summary file <plan>-summary.md); milestones (M0-M2a.3 merged; M2b.0 mechanism foundations, M2b.1 effect engine, M2b.2 edit/delete, M2b.3 reactions/pins, M2b.4 members/read position dev-done on feat/m2b awaiting one merge to main; next threads and extras (M2c), read path, gateway, hardening); dependencies; readiness; M5 carry-over list"
python3 bin/indexes_edit.py docs/plans/2026-10-06-m2b4-members-read.md purpose = "M2b.4 implementation plan: dense member_actions facts (desired-state commands, retry on a duplicate key) with a settle-first projection on members and user_rooms (per-doc mv guard, head CAS on rooms.mv/mc, folded member_count); owner/admin/member policy with many owners, last-owner rules and succession; DM members fixed; user_rooms keyed tenant 0x00 user 0x00 room; member events with a room copy and user copies on evt.{t}.user.{u} (envelope recipient, widened RePublish); actor member cache by generation + TTL; read position rs {s, v} with MarkRead/MarkUnread and read_updated through the readcast coalescer; feed kind MemberInserted, effects member_projection and member_event; resync scans member_actions; MEMBER_BATCH_MAX replaces the 5000 cap; corecli + e2e member phase; execution results and known issues at its end"
python3 bin/indexes_edit.py docs/plans/2026-10-06-m2b4-members-read.md decisions = "D67;D72;D96;D97;D98;D99;D100;D101;D102;D103;D104;D105;D106;D107"
python3 bin/indexes_edit.py deploy/prometheus/alerts.yml purpose + "; MB1 (member projection and events) uses the work failing, effect dropping and republish surge rules"
python3 bin/indexes_edit.py deploy/prometheus/alerts.yml decisions + ";D106"
grep -n "^README.md," INDEXES.csv
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
```

Expected: `ok` từng lệnh; dòng `README.md` đã nhắc `MEMBER_BATCH_MAX`, `READ_RECEIPT_WINDOW`, `READ_RECEIPT_MAX_MEMBERS` (Task 14); chưa có thì `python3 bin/indexes_edit.py README.md purpose "~" "REACTION_COUNT_DELAY)" "REACTION_COUNT_DELAY, MEMBER_BATCH_MAX, READ_RECEIPT_WINDOW, READ_RECEIPT_MAX_MEMBERS)"` và `… README.md decisions + ";D105;D107"`, ghi vào báo cáo; `{7}`. Dòng khác còn mô tả cũ (vd. `domain` còn nhắc trần 5000, `pkg/keys` thiếu `UserRoom`) thì sửa theo hợp đồng chung và ghi vào báo cáo task nào đã quên.

**Step 7: Kết quả thực thi**

Thêm **ở cuối file** `docs/plans/2026-10-06-m2b4-members-read.md` (sau Task 20, sau dòng `---` cuối) một heading cấp 2 viết đúng `## Kết quả thực thi` (dòng thật, không nằm trong code fence), rồi các đoạn theo dàn ý dưới, điền từ nhật ký thực thi của controller (sha, lệch, Minor theo task); phần "Kiểm chứng cuối" để trống, Task 20 điền. Dàn ý cố ý không chứa dòng heading, để `grep '^## Kết quả thực thi'` chỉ thấy bản thật (bài học M2b.3: khung mẫu nằm trong code fence, bản thật phải thêm lại ở cuối).

```markdown
Commit từng task (nhánh `feat/m2b`):

- Chuẩn bị: `<sha>` plan + tóm tắt (owner duyệt <ngày>). T1: baseline `fmt-check`/`vet`/`lint`/`test` xanh.
- T2 `<sha>` domain + keys; T3 `<sha>` proto + pbconv + publish (recipient, RePublish); T4 `<sha>` port store + memstore + `storetest`; T5 `<sha>` mongostore; T6 `<sha>` feed + work + reader; T7 `<sha>` `memberproj` (push `<sha>..<sha>`).
- T8 `<sha>` actor; T9 `<sha>` access; T10 `<sha>` `mutate` member; T11 `<sha>` `CreateRoom`; T12 `<sha>` `readcast`; T13 `<sha>` `MarkRead`/`MarkUnread`; T14 `<sha>` config + grpcsrv + vòng đời (push `<sha>..<sha>`).
- T15 `<sha>` effect; T16 `<sha>` (tách test), `<sha>` resync; T17 `<sha>` route, `<sha>` corecli/e2e; T18 `<sha>` itest; T19 commit docs này.

Quyết định của owner trong lúc làm:

- (theo nhật ký; không có thì "Không có ngoài các quyết định 2026-10-06 ở đầu plan.")

Lệch so với plan:

- T17: `remove-member`/`set-role` dùng `-target` cho user đích (hợp đồng chung ghi `-user`, trùng cờ người gọi của `addOptions`; Part C chỉnh trước khi thực thi).
- T18: "hai admin xoá nhau" thành "hai owner xoá nhau" (D99: admin không xoá được admin; test kiểm cả hai).
- (idiom gofmt/vet/lint đã áp; sửa biên dịch cơ học; tên khác hợp đồng; …)

Lỗi đã biết, đã sửa:

- (theo nhật ký)

Lỗi Minor còn mở:

1. (theo task, do controller/reviewer ghi)
2. File gần 200 dòng (lần sửa sau phải tách): (liệt kê `wc -l` ≥ 180 của các file đã đụng)

Kiểm chứng trong lúc làm: (itest xanh ở task nào; `make e2e` PASS ở T17; `make alerts-check` 16 luật ở T15/T19)

Kiểm chứng cuối (Task 20, <YYYY-MM-DD>, trên `<sha>`):
```

Cập nhật luôn khối "Từ M2b.4" của roadmap (Step 3) bằng các Minor đáng mang sang M5, thay dòng `(các Minor controller ghi …)`.

**Step 8: Kiểm**

```bash
grep -c '^## Kết quả thực thi' docs/plans/2026-10-06-m2b4-members-read.md
grep -n '^## Kết quả thực thi\|^### Task 20' docs/plans/2026-10-06-m2b4-members-read.md
grep -n "Chưa xây\]" docs/designs/261005-chatim-architecture.md | grep "6.4\|6.3"
grep -n "member_count \`{n, ver}\`\|touch \`member_count\`\|{\$max read_seq}\|26.2s\|D61–D95\|next is M2b.4\|tiếp theo là M2b.4" CLAUDE.md README.md docs/designs/261005-chatim-architecture.md docs/roadmap.md
grep -c "^| D\(9[6-9]\|10[0-7]\) |" docs/designs/261005-chatim-architecture.md
grep -n "u│r" CLAUDE.md README.md docs/designs/261005-chatim-architecture.md docs/roadmap.md
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
```

Expected: `1`; dòng `## Kết quả thực thi` nằm sau dòng `### Task 20`; lệnh ba không in gì; lệnh bốn không in gì (`26.2s` chỉ còn trong D105 dạng `26.2s → 27.2s`: nếu grep in đúng dòng D105 thì chấp nhận); `12`; `u│r` chỉ còn ở dòng D72 (kèm "thay bởi D101"), §5.1 (`thay \`u│r\` của D72`), D101 (phương án bị loại) và dòng M2b.4 của roadmap; `{7}`.

**Step 9: Commit**

```bash
git commit -m "docs: record the M2b.4 members and read position design and decisions D96-D107" -- docs/designs/261005-chatim-architecture.md docs/roadmap.md README.md CLAUDE.md INDEXES.csv docs/plans/2026-10-06-m2b4-members-read.md
git show --stat HEAD
```

Expected: đúng 6 file. Không push (push ở Task 20).

---

### Task 20: Kiểm chứng cuối milestone, push và chuẩn bị merge `feat/m2b` → `main`

Chạy một lần ở cuối, theo bảng "Verification and review budget" (mốc cuối milestone). M2b.4 không phải milestone perf: không sweep corebench trước/sau; chỉ một lần chạy ngắn để chắc đường gửi (cache member của actor có thế hệ + TTL) không lỗi. Đây cũng là mốc cuối của cả nhánh `feat/m2b` (M2b.0–M2b.4), nên task kết thúc bằng checklist Definition of Done của `docs/git-workflow.md` và bản nháp PR. **Không mở PR**: controller hỏi owner trước.

**Step 0: Kích thước file (sửa trước khi kiểm, như bài học M2b.3 với `core.proto`)**

```bash
git diff --name-only 3442a6c..HEAD -- '*.go' '*.proto' '*.sh' '*.yml' '*.yaml' | xargs wc -l | sort -n | tail -15
wc -l proto/chatim/v1/*.proto
```

Expected: không file nào > 200 dòng (code và proto; `pkg/pb` sinh ra được miễn). Có file > 200 → **dừng, báo controller**; khi được duyệt, tách đúng như M2b.3 (commit `refactor(...)` riêng, không đổi tên hay số field proto, `make proto && make buf-lint`, rồi chạy lại Step 1–3 dù trước đó đã xanh). File ≥ 180 ghi vào "Lỗi Minor còn mở" (dòng "File gần 200 dòng").

**Step 1:** `make fmt-check && make vet && make lint && make vuln`
Expected: sạch; vuln: `Your code is affected by 0 vulnerabilities` (có thể kèm cảnh báo cấp module GO-2026-5932, có từ trước).

**Step 2:** `make test`
Expected: mọi package `ok`, gồm `apps/core/internal/{memberproj,readcast,actor,access,mutate,effects,work,resync,pbconv,publish,grpcsrv,config,store/...}`, `pkg/keys`, `tools/internal/route`, `tools/corecli/internal/e2e`.

**Step 3:** `make infra-up && make itest`
Expected: mọi package `ok`, gồm contract `storetest` cho Mongo (`RunMembers`, `RunMemberFeed`), `TestBootstrapIsIdempotent` (collection `member_actions`, `user_rooms` + index), itest RePublish của Task 3, drill resync (Task 16), 7 itest của Task 18, các itest M2b.2/M2b.3. Không cần chạy lại nếu sau lần xanh cuối (Task 18) chỉ đổi docs và Step 0 không tách file nào.

**Step 4:** stack sạch rồi e2e

```bash
make core-down
make infra-reset
make image TARGET=apps/core && make core-up && make e2e
```

`make infra-reset` để stream `CHATIM_EVT` có luật RePublish `evt.*.*.*.*` (D102) và DB không còn room trước M2b.4 (không có `mv`/`user_rooms`).

Expected: dòng cuối là PASS của Task 17 (`e2e PASS: 40 messages before and 40 after killing core-1, …; members added with the full history and the read position at the latest, an admin promoted who removes a member then denied send, history and read, unread then read with read_updated, the last owner leaving to the admin and re-added, direct room members fixed, on replies and live room and user subjects`), có dòng `members ok: … 16 live events on the room and user subjects`.

**Step 5:** `/metrics` trên cả hai core:

```bash
for c in chatim-core-1 chatim-core-2; do echo "== $c"; docker run --rm --network chatim_default curlimages/curl:8.11.1 -s http://$c:9090/metrics | grep -E '^chatim_core_(reconcile_running|reconcile_republished_total|effect_dropped_total|read_events_unbatched_total|work_processed_total|work_failures_total)[ {]'; done
```

Expected:
- đúng một core có `chatim_core_reconcile_running 1`;
- cả hai core có `effect_dropped_total{effect="member_projection"}` và `{effect="member_event"}` (cùng các nhãn cũ), tất cả `0`; `reconcile_republished_total{effect="member_event"}` (không có nhãn `member_projection`); `chatim_core_read_events_unbatched_total 0`; `chatim_core_work_failures_total 0`;
- tổng hai core của `reconcile_republished_total{effect="member_event"}` = 0 sau e2e (fast path đã phát mọi bản room và bản user; bản worker gửi lại là bản trùng nên không đếm). Khác 0 thì ghi lại (fast path đã lỡ một bản), không phải lỗi.

**Step 6:** `make alerts-check`
Expected: `SUCCESS: 16 rules found`.

**Step 7:** Resync dry-run trên compose (phạm vi 15 phút, có room e2e vừa đổi member):

```bash
FROM=$(date -u -v-15M +%Y-%m-%dT%H:%M:%SZ); TO=$(date -u -v+1M +%Y-%m-%dT%H:%M:%SZ)
docker exec chatim-core-1 /app resync -from "$FROM" -to "$TO" -dry-run
```

Expected: một dòng `resync rooms=… room_records=R message_records=… edit_records=E reaction_records=X pin_records=P member_records=M dry_run=true` với `E ≥ 2`, `X ≥ 1`, `P ≥ 1`, `R ≥ 2` (room e2e và DM của phase 5, cộng room thừa mà `e2e setup` tạo khi tìm slot), `M ≥ 8` (fact mv 1–7 của room e2e, mv 1 của DM, cộng mv 1 của mỗi room thừa và fact của lệnh tay ở Task 17 nếu có), exit 0. (`date -v` là cú pháp macOS; Linux: `date -u -d '-15 min' +%FT%TZ`.)

**Step 8:** corebench ngắn (chỉ kiểm không lỗi):

```bash
pmset -g therm | grep CPU_Speed_Limit
make poc TOOL=corebench ARGS="-rate 1000 -duration 30s -watch 20"
```

Expected: dòng `sends due=… sent=… acked=… failed=0 …` và `live events on 20 watched rooms: … missing=0 duplicates=0 …`. Ghi p99 ack để tham khảo, không so sánh (không phải milestone perf); `CPU_Speed_Limit` < 100 thì ghi kèm.

**Step 9: Kết quả thực thi + push**

Cuối mục "Kết quả thực thi" của `docs/plans/2026-10-06-m2b4-members-read.md` (Task 19 đã tạo ở cuối file), dưới dòng `Kiểm chứng cuối (Task 20, …)`, điền:

```markdown
- `make fmt-check`, `make vet`, `make lint` sạch; `make vuln`: <kết quả>.
- `make test`: mọi package `ok` (<n> package).
- `make itest`: <xanh sau Task 18 / chạy lại>.
- `make e2e`: `e2e PASS: … direct room members fixed, on replies and live room and user subjects` (phase 5: <số lần thử mỗi lệnh, core xử lý>; `members ok: … 16 live events …`).
- `/metrics` hai core: <reconcile_running; effect_dropped_total và reconcile_republished_total của member_projection, member_event; read_events_unbatched_total; work_failures_total; work_processed_total>.
- `make alerts-check`: `SUCCESS: 16 rules found`.
- resync dry-run: `<dòng in ra>`.
- corebench 1000/s 30s: failed=<n>, missing=<n>, duplicates=<n>, p99 ack <ms>, CPU_Speed_Limit <giá trị>.
- Mức sẵn sàng: `dev-done` trên `feat/m2b`; M2b.0–M2b.4 chờ một PR merge `main` (owner duyệt).
```

Nếu Step 5 thấy republished khác 0, hoặc Step 0–8 có lệch, thêm vào "Lỗi Minor còn mở" và (nếu đáng mang sang) khối "Từ M2b.4" của roadmap.

```bash
git commit -m "docs: record M2b.4 execution results" -- docs/plans/2026-10-06-m2b4-members-read.md docs/roadmap.md
git push origin feat/m2b
git status --short
git log origin/feat/m2b -1 --oneline
```

Expected: push thành công; `git status` sạch; `git log origin/feat/m2b -1` là commit này.

**Step 10: Checklist merge `feat/m2b` → `main` (không mở PR)**

Theo `docs/git-workflow.md` (Definition of Done) và roadmap ("merge vào `main` một lần khi M2b.4 đạt Definition of Done"). Chỉ đọc và soạn; không `gh pr create`, không merge.

```bash
git fetch origin
git log --oneline origin/feat/m2b..origin/main | wc -l
git merge-tree --write-tree origin/main origin/feat/m2b > /dev/null; echo "merge-tree exit=$?"
git log --oneline origin/main..origin/feat/m2b | wc -l
git diff --stat origin/main...origin/feat/m2b | tail -1
grep -n '^## Kết quả thực thi' docs/plans/2026-10-05-m2b0-mechanism-foundations.md docs/plans/2026-10-05-m2b1-effect-engine.md docs/plans/2026-10-05-m2b2-edit-delete.md docs/plans/2026-10-06-m2b3-reactions-pins.md docs/plans/2026-10-06-m2b4-members-read.md
grep -n "Critical\|Important" docs/plans/2026-10-06-m2b4-members-read.md | tail -5
```

Expected: lệnh hai `0` (main không có commit nào ngoài nhánh; khác 0 → ghi số, controller quyết merge `main` vào `feat/m2b` trước hay không); `merge-tree exit=0` (không xung đột); số commit và thống kê diff ghi vào nháp PR; năm plan đều có mục kết quả (M2b.0 dùng heading có ngày, `grep` vẫn khớp đầu dòng `## Kết quả thực thi`); không còn finding Critical/Important mở (chỉ có trong mục "đã sửa").

Ghi checklist vào cuối mục "Kết quả thực thi" của plan M2b.4 (sau khối Task 20), cùng commit Step 9 hoặc một commit docs nhỏ ngay sau (`docs: add the feat/m2b merge checklist`) rồi push lại:

```markdown
Chuẩn bị merge `feat/m2b` → `main` (Definition of Done, `docs/git-workflow.md`):

- [x] 1. Mọi task của plan M2b.0, M2b.1, M2b.2, M2b.3, M2b.4 đã xong; bước lệch "Expected" đã báo và xử lý (mục Kết quả thực thi của từng plan).
- [x] 2. Xanh trên `<sha>`: `make fmt-check`, `vet`, `lint`, `test`, `itest`, `core-up && e2e` (Task 20 ở trên); CI job `checks` sẽ chạy lại trên PR.
- [x] 3. Không còn finding Critical/Important; Minor ghi trong plan và khối "Từ M2b.x" của roadmap.
- [x] 4. Cùng nhánh đã cập nhật `docs/roadmap.md` (trạng thái, mức sẵn sàng), Decision Log D61–D107, `INDEXES.csv`, mục Done/Next của `CLAUDE.md`.
- [ ] 5. Mô tả PR ghi mức sẵn sàng và những gì còn thiếu để go-live (nháp ở `bin/m2b-pr.md`, chưa mở PR: chờ owner).
- Nhánh: <n> commit trên `main`, `merge-tree` không xung đột, `main` <không có / có n> commit mới; merge bằng merge commit (`gh pr merge --merge`), không squash, xoá nhánh sau khi merge.
```

Soạn nháp PR ở `bin/m2b-pr.md` (`bin/` gitignored, không commit) cho controller:

```markdown
feat: M2b mechanisms, effect engine, edits, reactions, pins, members and read position (M2b.0–M2b.4)

## Tóm tắt

- M2b.0: ack mark chỉ cho `msg_created`, actor tự rút khi tranh seq, permission hook + reader pipeline, detector `/metrics` + luật alert (D65, D76–D78).
- M2b.1: reader trên slot 0 → work stream `CHATIM_WORK` → worker mọi core, `room_created`, room activity, `/app resync` (D79–D81).
- M2b.2: sửa/xoá theo fact `message_edits` + projection, ẩn/clear phía người đọc, `GetEditHistory`, quyền qua `access.Policy` (D82–D87).
- M2b.3: reaction một emoji mỗi user + số đếm recount CAS, danh sách emoji cố định, ghim theo fact `pin_actions` (D88–D95).
- M2b.4: member theo fact `member_actions` + projection `members`/`user_rooms`, quyền owner/admin/member, event member trên subject room và user, vị trí đọc + `read_updated` gộp (D96–D107).

Plan và kết quả từng phần: `docs/plans/2026-10-05-m2b0-mechanism-foundations.md`, `…-m2b1-effect-engine.md`, `…-m2b2-edit-delete.md`, `docs/plans/2026-10-06-m2b3-reactions-pins.md`, `…-m2b4-members-read.md` (mục "Kết quả thực thi" ở cuối mỗi file). Thiết kế: `docs/designs/261005-chatim-architecture.md`.

## Mức sẵn sàng

`dev-done`: `make fmt-check vet lint test itest`, `make core-up && make e2e` xanh trên máy dev (<sha>, <ngày>); 16 luật alert; corebench 1000/s không lỗi.

## Còn thiếu để go-live

- PoC prod-like (P1–P10, `docs/poc/README.md`) và số thật thay giả định thiết kế §2.3.
- Oplog `minRetentionHours` ≥ 24h, alert RC5 và lag trên hạ tầng thật, đo bộ nhớ NATS cho map chống trùng.
- M3 (đường đọc), M4 (gateway), M5 (hardening, gồm danh sách "Mục mang sang M5" của roadmap từ M2b.0–M2b.4).
- Rolling deploy: nâng mọi core cùng lúc (D91, D102, D106); room tạo trước M2b.4 chưa có `user_rooms`.

## Kiểm

- [ ] CI `checks` xanh trên PR.
```

Controller dùng nháp này sau khi owner đồng ý: `gh pr create --base main --head feat/m2b --title "<dòng đầu>" --body-file bin/m2b-pr.md` (bỏ dòng tiêu đề khỏi body). Không chạy lệnh này trong task.

Expected: checklist có trong plan và đã push; `bin/m2b-pr.md` tồn tại, không nằm trong `git status`; báo controller: số commit, kết quả `merge-tree`, mục còn mở (chỉ DoD 5: mở PR khi owner duyệt).

Nếu sau đó owner yêu cầu sửa (như M2b.3), thêm mục con `### Sửa theo owner (<ngày>)` ở cuối mục "Kết quả thực thi", ghi từng sửa với sha, lý do và quyết định thiết kế đã đổi; các task ở trên giữ nguyên làm lịch sử.
