# M2b.2 — Sửa + xoá — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: dùng `subagent-driven-development` (cùng phiên) hoặc `separate-driven-development` (phiên riêng) để thực thi plan này theo từng task. Implementer dùng skill `go-lang`.

**Goal:** Sửa tin, xoá tin cho mọi người, ẩn tin phía tôi, clear history và đọc lịch sử sửa, theo mô hình fact + projection (D62–D64): lệnh đổi ghi một **fact bất biến** vào `message_edits` (khoá `room│thread│seq│version` là CAS), cập nhật **projection** `messages` rồi mới ack; worker của effect engine (M2b.1) chạy lại projection và phát event `msg_edited`/`msg_deleted` từ feed insert của `message_edits`. Ẩn và clear là fact thưa theo người đọc, áp ở reader pipeline, không phát event (owner 2026-10-05).

**Architecture:**
- `domain`: `Message` thêm `Version`, `Deleted`, `EditedAt`, `Hidden` (cờ chỉ của view, không lưu); kiểu `Edit`; `Member.ClearedBeforeSeq`; lỗi `ErrMessageNotFound`, `ErrMessageDeleted`, `ErrVersionConflict`. Không có lỗi "không phải tác giả": quyền sửa/xoá/ẩn/đọc lịch sử chỉ do `access.Policy` quyết (D86).
- `store`: port mới `Edits`, `Hidden`, `MessageEditor` (`ApplyEdit`, CAS theo version), `HistoryClearer` (`ClearHistory`, `$max`). Mongo: collection clustered `message_edits` (`_id = keys.Edit`, index `{r, ts}` — D70), `hidden` (index unique `{u, r, th, s}`), field `v/d/ea` trên `messages`, `cb` trên `members`.
- Feed thêm `EditInserted`; `work.Record` thêm `Version` (37 byte, id `e:{room}-{th}-{seq}-v{ver}`).
- `access`: `Request.Author`; `Checker.Admit` (tenant + membership) và `Checker.Allow` (policy), `Authorize` = hai bước; `DefaultPolicy` (sửa/xoá chỉ tác giả, còn lại cho member) thay `AllowMembers` làm mặc định (D86).
- Package mới `mutate`: `Edit`, `Delete`, `Hide`, `ClearHistory` (fast path: kiểm quyền qua `access` với tác giả của tin, đọc fact cuối, insert fact, projection, dọn text khi xoá, enqueue event, rồi trả).
- `grpcsrv`: 5 RPC mới (`EditMessage`, `DeleteMessage`, `HideMessage`, `ClearHistory`, `GetEditHistory`); `GetHistory` chạy thêm `view.MaskDeleted` và `view.HideForViewer`.
- `effects`: `edit_projection` (delay 0) + `msg_changed` (delay `RECONCILE_DELAY`, không ack mark) cho `EditInserted`.
- `/app resync` quét thêm `message_edits` theo `{r, ts}`; `corecli` + e2e có sửa/xoá.

**Tech Stack:** Go 1.26 trong Docker qua `make`; buf; mongo-driver v2 (clustered collection, `UpdateOne` có điều kiện, `FindOneAndUpdate`); nats.go jetstream; `testing/synctest`; goleak.

**Nguồn quyết định:** [thiết kế](../designs/261005-chatim-architecture.md) §4, §5, §6.3, §6.4 (ẩn/clear), §8.3, §9.2, D62–D64, D70, D75, D79–D81, D86; [roadmap](../roadmap.md) dòng M2b.2; owner chốt 2026-10-05: phạm vi gồm sửa + xoá + ẩn + clear + GetEditHistory; ẩn/clear **không** phát event (đồng bộ đa thiết bị để M3/M4, H7/H8); **không** giới hạn thời gian sửa/xoá trong core (policy theo tenant cắm qua `access.Policy` ở Phase 2); A7 qua `access.Policy` với action `ReadEditHistory` (mặc định cho phép member). Quyết định mới ghi ở task docs: **D82** lệnh đổi qua package `mutate`, không qua actor, vẫn định tuyến theo slot; **D83** event thay đổi mang snapshot hiện tại + id theo version của fact, không ack mark, projection ở worker delay 0 (thay "xoá delay 2–3s"), event delay `RECONCILE_DELAY`; **D84** record work mang `Version` (37 byte); **D85** view: tin bị ẩn/clear trả placeholder `hidden` không nội dung, tin xoá trả `deleted` không nội dung, seq giữ nguyên; **D86** (owner chốt 2026-10-05) mặc định không ai sửa/xoá tin của người khác; core chỉ hỏi `access.Policy` user có quyền sửa/xoá/ẩn hay không, không có bất biến tác giả/owner trong `mutate`; user → role → quyền là module policy chat (Phase 2).

---

## Quy tắc chung (đọc trước khi làm task nào)

- **Không chạy `go` trên host.** Test một package: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/<pkg>/..."`. Tra API: `make -s go ARGS="doc <pkg> <Symbol>"`.
- **Không viết comment** trong Go, test, YAML, shell, Makefile, proto mới.
- File code dưới 200 lines; `wc -l` sau mỗi lần sửa file lớn.
- `gosec`: không chuyển `int` → `uint*` khi chưa chặn biên (G115); version là `uint32` nhưng lưu int32 trên Mongo: version > `MaxInt32` bị từ chối, `mutate` đổi `BaseVersion >= MaxInt32` thành `ErrVersionConflict`.
- Trước mỗi commit: `make fmt-check`, `make vet`, `make lint` + test các package đã đụng. Goroutine/channel/lock mới: thêm `-count=5`.
- Sửa lint/vet thuần idiom/format (gofmt, `errors.AsType`, range-over-func, `slices.Backward`) có hành vi y hệt: được áp và ghi vào báo cáo. Lệch khác, kết quả khác "Expected", test cũ fail: **dừng và báo cáo**.
- Đổi adapter Mongo/NATS hoặc wiring `apps/core`: `make itest` một lần cuối task (cần `make infra-up`).
- Commit theo Conventional Commits, không nhắc AI, không có dòng `Co-Authored-By`. Nhánh `feat/m2b`; không push từng task.
- Review: Task 4, 7, 9, 11 rủi ro → mỗi task 1 reviewer. Task khác controller kiểm nhanh. Lỗi Minor ghi vào "Kết quả thực thi".

## Bảng tính năng → lớp dữ liệu (quy tắc roadmap)

| Tính năng | Lớp (§4) | Fact | Idempotent | Effect + chính sách | Quyền | View | Event (id) | Khuếch đại | Guarantee + detector |
|---|---|---|---|---|---|---|---|---|---|
| Sửa tin | Fact bất biến + projection | insert `message_edits` v = base+1, kind `edit`, v1 có `prev` | `base_ver` (D63): dup key cùng tác giả + nội dung = thành công | fast path projection + event; worker `edit_projection` (0s) + `msg_changed` (`RECONCILE_DELAY`, không mark) | `access` `EditMessage` (policy mặc định: chỉ tác giả, D86) | `Version`, `EditedAt` | `msg_edited` `{room}-{th}-{seq}-v{ver}` | 1 reverse scan + 2 write majority + 1 event | RC1: `reconcile_republished_total{effect="msg_changed"}`, `work_failures_total` |
| Xoá cho mọi người | như trên, kind `delete` | như trên | như trên | như trên + `PurgeText` các fact ≤ v−1 (D75) | `DeleteMessage` (policy mặc định: chỉ tác giả, D86) | `MaskDeleted` | `msg_deleted` cùng dạng id | + 1 update nhiều doc (dọn text) | như trên |
| Ẩn phía tôi | Giá trị theo người đọc | upsert `hidden {u, r, th, s}` | upsert | không | `HideMessage` (member) | `HideForViewer` | không (owner) | 1 upsert | — |
| Clear history | Giá trị theo người đọc | `$max members.cb` | `$max` | không | `ClearHistory` (member) | `HideForViewer` | không | 1 update | — |
| Lịch sử sửa | Fact (đọc) | — | — | — | `ReadEditHistory` (A7) | tin đã xoá: rỗng | — | 1 range scan | — |

## Hợp đồng chung (mọi task phải khớp đúng chữ ký này)

### `apps/core/internal/domain` (Task 1)

```go
type Message struct {
	Room      uint64
	Thread    uint64
	Seq       uint64
	Tenant    string
	From      string
	Kind      Kind
	Text      string
	CID       string
	CreatedAt time.Time
	Version   uint32
	Deleted   bool
	EditedAt  time.Time
	Hidden    bool
}

type EditKind uint8

const (
	EditText EditKind = iota + 1
	EditDelete
)

type Edit struct {
	Room    uint64
	Thread  uint64
	Seq     uint64
	Version uint32
	Kind    EditKind
	Tenant  string
	By      string
	Text    string
	Prev    string
	At      time.Time
}

var (
	ErrMessageNotFound = fmt.Errorf("message %w", apperr.ErrNotFound)
	ErrMessageDeleted  = fmt.Errorf("message deleted: %w", apperr.ErrFailedPrecondition)
	ErrVersionConflict = fmt.Errorf("message version conflict: %w", apperr.ErrFailedPrecondition)
)
```

- `Member` thêm `ClearedBeforeSeq uint64` (tin có `seq ≤ ClearedBeforeSeq` bị ẩn với member đó).
- `Hidden` chỉ do view đặt; adapter không lưu, không đọc.
- Version: tin gốc `0`; fact đầu tiên `1`; `Edit.Prev` chỉ có ở `Version == 1` (= text gốc). `ValidateText` áp cho `Edit.Text` khi `Kind == EditText`.

### `apps/core/internal/store` (Task 3, 4, 5)

```go
var (
	ErrEditExists   = fmt.Errorf("edit version %w", apperr.ErrAlreadyExists)
	ErrEditNotFound = fmt.Errorf("edit %w", apperr.ErrNotFound)
)

const MaxEditPage = 100

type Edits interface {
	Append(ctx context.Context, e domain.Edit) error
	At(ctx context.Context, key MsgKey, version uint32) (domain.Edit, error)
	Latest(ctx context.Context, key MsgKey) (domain.Edit, bool, error)
	History(ctx context.Context, key MsgKey, after uint32, limit int) ([]domain.Edit, error)
	Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Edit, error)
	PurgeText(ctx context.Context, key MsgKey, upTo uint32) error
}

type Hidden interface {
	Hide(ctx context.Context, user string, key MsgKey) error
	HiddenIn(ctx context.Context, user string, room, thread, from, to uint64) ([]uint64, error)
}
```

- `store.MessageEditor { ApplyEdit(ctx context.Context, e domain.Edit) error }` (interface riêng, không thêm vào `Messages`; memstore `Messages` và `mongostore.Store` cài): `$set` `v = e.Version`, `ea = e.At`, và `x = e.Text, d = false` (edit) hoặc `x = "", d = true` (delete), chỉ khi doc tồn tại và `v` (thiếu = 0) `< e.Version`. Không khớp (doc không có hoặc version đã ≥) → `nil`. Page/Find/Last đọc `v`, `d`, `ea`.
- `store.HistoryClearer { ClearHistory(ctx context.Context, room uint64, user string, seq uint64) (uint64, error) }` (interface riêng, không thêm vào `Rooms`; memstore `Rooms` và `mongostore.Store` cài): `$max cb = seq` trên member, trả giá trị sau cập nhật; không phải member → `domain.ErrNotMember`. `Member` trả `ClearedBeforeSeq`.
- `Edits.Append`: insert theo khoá `keys.Edit(room, thread, seq, version)`; trùng → `ErrEditExists`. `At` không có → `ErrEditNotFound`. `Latest` reverse scan một doc trong range của tin. `History` tăng dần theo version, `version > after`, tối đa `limit` (1..`MaxEditPage`). `Between` các fact của room có `ts ∈ [from, to]`, sắp theo `ts` rồi `_id`, tối đa `limit` (1..1000). `PurgeText` `$unset text, prev` trên fact `version ≤ upTo` của tin.
- `Hidden.Hide`: upsert `{u, r, th, s}`. `HiddenIn`: các seq bị ẩn của user trong `[from, to]` của timeline, tăng dần.
- Write contract (`write_contract_test.go`): thêm kind `purge` vào `allowedKinds`; `MessageEditor.ApplyEdit` = `cas`; `Edits.Append` = `insert-unique`; `Edits.At/Latest/History/Between` = `read`; `Edits.PurgeText` = `purge`; `Hidden.Hide` = `upsert`; `Hidden.HiddenIn` = `read`; `HistoryClearer.ClearHistory` = `monotonic-max`. Thêm `store.Edits`, `store.Hidden`, `store.MessageEditor`, `store.HistoryClearer` vào danh sách port reflect.
- Feed: `EditInserted ChangeKind = 3`; `Change` thêm `Edit domain.Edit`. Mongo feed `$match` thêm `message_edits`; memstore: `Edits` ghi vào log chung khi được attach (`memstore.NewFeed(msgs, rooms, edits)`, `edits` có thể nil).
- BSON: `messages` thêm `v` (int32, omitempty), `d` (bool, omitempty), `ea` (date, omitempty); `members` thêm `cb` (int64, omitempty); `message_edits` `{_id: binary 28B, r: int64, t, k: int32, by, x, p, ts}`; `hidden` `{_id: ObjectId, u, r: int64, th: int64, s: int64}`.

### `apps/core/internal/work` (Task 6)

```go
type Record struct {
	Kind        store.ChangeKind
	Room        uint64
	Thread      uint64
	Seq         uint64
	Version     uint32
	CommittedAt time.Time
}

const RecordSize = 37
```

- Layout: kind(1) room(8) thread(8) seq(8) version(4) unixNano(8), big-endian. `Decode` nhận `MessageInserted`, `RoomInserted`, `EditInserted`. `RecordOf` cho `EditInserted` lấy room/thread/seq/version từ `c.Edit`.
- `ID()`: `EditInserted` → `"e:" + pbconv.MessageChangeEventID(room, thread, seq, version)`.

### `apps/core/internal/pbconv` + proto (Task 2)

```go
func MessageChangeEventID(room, thread, seq uint64, version uint32) string
func MessageEdited(roomType domain.RoomType, m domain.Message, e domain.Edit) *chatimv1.Event
func MessageDeleted(roomType domain.RoomType, m domain.Message, e domain.Edit) *chatimv1.Event
func MessageVersions(m domain.Message, edits []domain.Edit, after uint32) []*chatimv1.MessageVersion
```

- `MessageChangeEventID` = `MessageEventID(room, thread, seq) + "-v" + strconv.FormatUint(uint64(version), 10)`.
- `MessageEdited/Deleted`: `Id = MessageChangeEventID(...e.Version)`, `Actor = e.By`, `Ts = e.At`, payload mang `pbconv.Message(m)` (snapshot hiện tại, có `version` của doc) và `version = e.Version`.
- `MessageVersions`: nếu `after == 0` và fact đầu có `Version == 1` thì thêm phần tử version 0 kind `ORIGINAL` (text = `Prev`, by = `m.From`, at = `m.CreatedAt`); sau đó mỗi fact một phần tử (`TEXT` hoặc `DELETE`).
- proto `core.proto`: `Message` thêm `uint32 version = 10; bool deleted = 11; google.protobuf.Timestamp edited_at = 12; bool hidden = 13;`. RPC mới:

```proto
rpc EditMessage(EditMessageRequest) returns (EditMessageResponse);
rpc DeleteMessage(DeleteMessageRequest) returns (DeleteMessageResponse);
rpc HideMessage(HideMessageRequest) returns (HideMessageResponse);
rpc ClearHistory(ClearHistoryRequest) returns (ClearHistoryResponse);
rpc GetEditHistory(GetEditHistoryRequest) returns (GetEditHistoryResponse);

message EditMessageRequest { string room_id = 1; uint64 thread_root = 2; uint64 seq = 3; uint32 base_version = 4; string text = 5; }
message EditMessageResponse { Message message = 1; }
message DeleteMessageRequest { string room_id = 1; uint64 thread_root = 2; uint64 seq = 3; uint32 base_version = 4; }
message DeleteMessageResponse { Message message = 1; }
message HideMessageRequest { string room_id = 1; uint64 thread_root = 2; uint64 seq = 3; }
message HideMessageResponse {}
message ClearHistoryRequest { string room_id = 1; uint64 up_to_seq = 2; }
message ClearHistoryResponse { uint64 cleared_before_seq = 1; }
message GetEditHistoryRequest { string room_id = 1; uint64 thread_root = 2; uint64 seq = 3; uint32 after_version = 4; uint32 limit = 5; }
message GetEditHistoryResponse { repeated MessageVersion versions = 1; }
enum EditKind { EDIT_KIND_UNSPECIFIED = 0; EDIT_KIND_ORIGINAL = 1; EDIT_KIND_TEXT = 2; EDIT_KIND_DELETE = 3; }
message MessageVersion { uint32 version = 1; EditKind kind = 2; string text = 3; string by = 4; google.protobuf.Timestamp at = 5; }
```

- `events.proto` payload: `MessageEdited message_edited = 22; MessageDeleted message_deleted = 23;` với `message MessageEdited { Message message = 1; uint32 version = 2; }`, `message MessageDeleted { Message message = 1; uint32 version = 2; }`.
- `publish.eventKind`: `msg_edited`, `msg_deleted`; `markKey` không mark hai loại này.

### `apps/core/internal/access` (Task 7)

Thêm action: `EditMessage = "edit_message"`, `DeleteMessage = "delete_message"`, `HideMessage = "hide_message"`, `ClearHistory = "clear_history"`, `ReadEditHistory = "read_edit_history"`.

```go
type Request struct {
	Action Action
	User   string
	Author string
	Room   domain.Room
	Member domain.Member
}

type DefaultPolicy struct{}

func (DefaultPolicy) Check(ctx context.Context, req Request) error

func (c *Checker) Admit(ctx context.Context, action Action, tenant, user string, room uint64) (Request, error)
func (c *Checker) Allow(ctx context.Context, req Request) error
func (c *Checker) Authorize(ctx context.Context, action Action, tenant, user string, room uint64) (Request, error)
```

- Quyền trên một tin (sửa, xoá, ẩn, đọc lịch sử sửa) **chỉ** do `access.Policy` quyết (D86); core không có bất biến tác giả/owner, không có `domain.ErrNotAuthor`. Từ chối = `access.ErrDenied` (bọc `apperr.ErrPermissionDenied` → `PERMISSION_DENIED`).
- `Request.Author`: tác giả của tin đích (`msg.From`); rỗng với action theo room (`ReadHistory`, `SendMessage`, `ClearHistory`).
- `DefaultPolicy`: `EditMessage`/`DeleteMessage` với `Author != User` → `ErrDenied`; mọi action khác → `nil`. `NewChecker(rooms, nil)` dùng `DefaultPolicy{}` (trước là `AllowMembers{}`); `AllowMembers` giữ lại (test dùng). Actor mặc định cũng đổi sang `DefaultPolicy{}`; `SendMessage`/`ReadHistory` không đổi hành vi vì policy mặc định cho phép.
- `Admit`: room (`Rooms.Get`) → `domain.CheckTenant` → membership; **không** hỏi policy; trả `Request{Action, User, Room, Member}`. `Allow`: hỏi policy với `req`. `Authorize` = `Admit` rồi `Allow` (chữ ký giữ nguyên).
- Thứ tự cho mọi action trên **một tin** (Edit, Delete, Hide, GetEditHistory): `Admit` → `Find` tin (không có → `ErrMessageNotFound`) → `Allow(req với Author = msg.From)` → phần còn lại. Action theo room (`ClearHistory`) dùng `Authorize`.

### `apps/core/internal/mutate` (Task 7, 8)

```go
type Messages interface {
	Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error)
	Last(ctx context.Context, room, thread uint64) (uint64, error)
	ApplyEdit(ctx context.Context, e domain.Edit) error
}

type HistoryClearer interface {
	ClearHistory(ctx context.Context, room uint64, user string, seq uint64) (uint64, error)
}

type EventPublisher interface {
	Enqueue(room uint64, events []*chatimv1.Event) error
}

type Deps struct {
	Access   *access.Checker
	Messages Messages
	Edits    store.Edits
	Hidden   store.Hidden
	Rooms    HistoryClearer
	Events   EventPublisher
	Now      func() time.Time
}

type EditCmd struct {
	Tenant, User      string
	Room, Thread, Seq uint64
	BaseVersion       uint32
	Text              string
}

type DeleteCmd struct {
	Tenant, User      string
	Room, Thread, Seq uint64
	BaseVersion       uint32
}

type HideCmd struct {
	Tenant, User      string
	Room, Thread, Seq uint64
}

type ClearCmd struct {
	Tenant, User string
	Room         uint64
	UpToSeq      uint64
}

func New(d Deps) (*Mutator, error)
func (m *Mutator) Edit(ctx context.Context, c EditCmd) (domain.Message, error)
func (m *Mutator) Delete(ctx context.Context, c DeleteCmd) (domain.Message, error)
func (m *Mutator) Hide(ctx context.Context, c HideCmd) error
func (m *Mutator) ClearHistory(ctx context.Context, c ClearCmd) (uint64, error)
```

Luồng `Edit`/`Delete` (thiết kế §6.3):
1. `Access.Admit(ctx, EditMessage|DeleteMessage, tenant, user, room)` → grant (room, member), chưa hỏi policy.
2. `Find` tin (không có → `ErrMessageNotFound`).
3. Policy: `Access.Allow(ctx, grant với Author = msg.From)`; từ chối → `access.ErrDenied` (D86). Hỏi trước nhận diện retry: tác giả gửi lại vẫn được phép.
4. `Edits.Latest` (fact cuối). Version hiện tại = `max(msg.Version, latest.Version)`. `BaseVersion >= MaxInt32` → `ErrVersionConflict`. **Retry trước:** fact cuối có `Version == BaseVersion + 1` và trùng `(By, Kind, Text)` của lệnh → retry thành công, dùng chính fact đã lưu, sang bước 7. Sau đó mới: đã xoá (`msg.Deleted` hoặc fact cuối là delete) → `ErrMessageDeleted`; rồi `BaseVersion != version hiện tại` → `ErrVersionConflict` (trừ khi bước 6 nhận ra retry).
5. Fact `Version = BaseVersion + 1`; `Prev = msg.Text` chỉ khi `Kind == EditText && Version == 1` (fact xoá không bao giờ có `Prev`); `At = Now().UTC().Truncate(ms)`. `Edits.Append`.
6. `ErrEditExists` → `Edits.At(version)`: cùng `By`, `Kind` và `Text` → coi là retry thành công (không lỗi), đi tiếp bước 7; khác → `ErrVersionConflict`. (Bước 4 với retry: nếu `BaseVersion + 1 == version hiện tại` và fact ở version đó trùng nội dung → retry thành công.)
7. `Messages.ApplyEdit(fact)`; xoá thì `Edits.PurgeText(key, Version − 1)`.
8. `Find` lại tin (snapshot sau projection) → `Events.Enqueue(room, MessageEdited|MessageDeleted(grant.Room.Type, msg, fact))` (lỗi enqueue bỏ qua; worker bù) → trả snapshot.

`Hide`: `Admit(HideMessage)` → tin phải tồn tại → `Allow(Author = msg.From)` → `Hidden.Hide`. `ClearHistory`: `Authorize(ClearHistory)`; `UpToSeq == 0` hoặc lớn hơn seq cuối → kẹp về `Messages.Last(room, 0)`; → `HistoryClearer.ClearHistory`.

### `apps/core/internal/view` (Task 10)

```go
type Viewer struct {
	User             string
	Room             domain.Room
	ClearedBeforeSeq uint64
	HiddenSeqs       map[uint64]bool
}

func MaskDeleted(v Viewer, msgs []domain.Message) []domain.Message
func HideForViewer(v Viewer, msgs []domain.Message) []domain.Message
```

- `MaskDeleted`: tin `Deleted` → `Text = ""` (giữ seq, version, cờ).
- `HideForViewer`: tin có `Seq ≤ ClearedBeforeSeq` hoặc trong `HiddenSeqs` → `Hidden = true`, `Text = ""`.
- `Default()` = `New(CollapseRetried, MaskDeleted, HideForViewer)`. Không sửa slice đầu vào.
- `GetHistory` dựng `Viewer` từ grant (`Member.ClearedBeforeSeq`) và `Hidden.HiddenIn(user, room, thread, minSeq, maxSeq)` của trang; `grpcsrv.Deps` thêm `Hidden store.Hidden` và `Mutator *mutate.Mutator` (nil = RPC đổi trả `Unimplemented`? không: bắt buộc khi wiring, test harness truyền thật).

### `apps/core/internal/effects` (Task 11)

```go
const (
	EditProjectionName = "edit_projection"
	MessageChangedName = "msg_changed"
)

type EditReader interface {
	At(ctx context.Context, key store.MsgKey, version uint32) (domain.Edit, error)
}

type EditApplier interface {
	ApplyEdit(ctx context.Context, e domain.Edit) error
}

type TextPurger interface {
	PurgeText(ctx context.Context, key store.MsgKey, upTo uint32) error
}

type EditProjectionDeps struct {
	Edits    EditReader
	Messages EditApplier
	Purger   TextPurger
}

type MessageChangedDeps struct {
	Edits    EditReader
	Messages MessageFinder
	Rooms    RoomReader
	JS       publish.JetStream
}

type MessageChangedConfig struct {
	SubjectRoot string
	Delay       time.Duration
	RoomCache   int
}

func NewEditProjection(deps EditProjectionDeps) (*EditProjection, error)
func (e *EditProjection) Effect() Effect
func (e *EditProjection) Dropped() uint64
func NewMessageChanged(deps MessageChangedDeps, cfg MessageChangedConfig) (*MessageChanged, error)
func (e *MessageChanged) Effect() Effect
func (e *MessageChanged) Republished() uint64
func (e *MessageChanged) Dropped() uint64
```

- `edit_projection` (delay 0): `At(key, version)` → `ApplyEdit`; delete → `PurgeText(key, version − 1)`. `ErrEditNotFound`/undeliverable → dropped (nil).
- `msg_changed` (delay `cfg.Delay`, không ack mark): `At` + `Find` tin + room type → `MessageEdited`/`MessageDeleted` → publish + chờ PubAck (dùng `send`/`awaitAcks`). Tin hoặc room không còn → dropped.
- Registry: `store.EditInserted: {activity.Effect(), editProjection.Effect(), messageChanged.Effect()}` (`room_activity` ánh xạ record sửa thành `Activity{Seq: 0}`). `effectSet.counters()` thêm `msg_changed` (republished + dropped) và `edit_projection` (chỉ dropped; `republished` nil).

## Thứ tự task

| # | Task | Rủi ro | Phụ thuộc |
|---|---|---|---|
| 0 | Branch + baseline | — | — |
| 1 | `domain`: Message fields, Edit, Member.ClearedBeforeSeq, lỗi | thấp | — |
| 2 | Proto + pbconv + publish kinds | thấp | 1 |
| 3 | Store ports + memstore + contract (Edits, Hidden, ApplyEdit, ClearHistory) | trung bình | 1 |
| 4 | Mongostore: `message_edits`, `hidden`, codec `v/d/ea/cb`, ports | **cao** | 3 |
| 5 | Feed `EditInserted` (memstore + Mongo) + reader nhận kind mới | trung bình | 3, 4 |
| 6 | `work.Record` 37 byte + `Version` + id `e:` | thấp | 5 |
| 7 | `access` (actions, `Admit`/`Allow`, `DefaultPolicy`) + `mutate` Edit/Delete | **cao** | 2, 3 |
| 8 | `mutate` Hide/ClearHistory | thấp | 7 |
| 9 | `grpcsrv` 5 RPC + wiring `apps/core` | **cao** | 7, 8 |
| 10 | `view` MaskDeleted + HideForViewer + GetHistory | trung bình | 3, 9 |
| 11 | `effects` edit_projection + msg_changed + registry + metrics | **cao** | 5, 6, 7 |
| 12 | `/app resync` quét `message_edits` | trung bình | 4, 6 |
| 13 | route client + corecli + e2e sửa/xoá | thấp | 9 |
| 14 | Itest end-to-end | trung bình | 9–12 |
| 15 | Docs | thấp | tất cả |
| 16 | Kiểm chứng cuối | — | tất cả |

---

## Ghi chú tích hợp (controller, khi ghép 3 phần)

- **Commit có pathspec.** Working tree có thể có thay đổi docs/INDEXES chưa commit của owner. Mọi commit: `git add` đúng file mới, rồi `git commit -m "..." -- <paths>`; không `add -A`, `commit -a`, không stash. Task nào phải sửa `INDEXES.csv`, `README.md`, `CLAUDE.md` hoặc docs mà file đó đang có thay đổi chưa commit của người khác: **dừng và báo controller**.
- **Hợp đồng đã tinh chỉnh (phần A):** `ApplyEdit` nằm ở `store.MessageEditor`, `ClearHistory` ở `store.HistoryClearer` (không thêm vào `Messages`/`Rooms`); thêm `store.MaxEditScan = 1000`, `store.EditKeyOf`, `store.ValidateEdit`, `store.ValidateLimit`, `work.KnownKind`, `storetest.RunEdits`, `storetest.RunEditFeed`, `storetest.EditableMessages`, `storetest.ClearableRooms`; `memstore.NewEdits()`, `memstore.NewHidden()`, `memstore.NewFeed(msgs, rooms, edits)`. Version > `MaxInt32` bị store từ chối (`messages.v` là int32); `mutate` đổi `BaseVersion >= MaxInt32` thành `ErrVersionConflict`.
- **`Prev` chỉ ở fact sửa version 1** (không bao giờ ở fact xoá), để `PurgeText` luôn xoá được text gốc (D75). `pbconv.Message` không đặt `edited_at` khi `EditedAt` zero.
- **Room activity cho sửa/xoá:** registry `EditInserted → [room_activity, edit_projection, msg_changed]`; `room_activity` ánh xạ record sửa thành `Activity{Seq: 0}`; `TouchActivity` chỉ nâng `ls/lm` khi `Thread == 0 && Seq > 0`, luôn nâng `lc/ab` — nhờ vậy resync (tìm room theo `act_bucket`) thấy cả room chỉ có sửa/xoá.
- **Retry:** `mutate` nhận diện retry (fact cuối = `base+1` cùng `By/Kind/Text`) **trước** kiểm "đã xoá", nên gửi lại lệnh xoá đã thắng vẫn thành công (D63); retry enqueue lại cùng id event, JetStream bỏ trùng (D83 ghi đúng như vậy, không phải "không event").
- **Metrics:** `msg_changed` chỉ đếm `republished` cho PubAck không bị đánh dấu trùng (để `ChatimRepublishSurge` không báo giả theo tốc độ sửa); `edit_projection` chỉ có `effect_dropped_total`. Không thêm luật alert (vẫn 15).
- **Khoảng trống tạm:** từ Task 5 reader còn bỏ change sửa; Task 6 bật forward; từ Task 6 tới Task 11 worker ack record sửa mà chưa chạy effect nào (chỉ dev). Record 33 byte cũ còn trong work stream dev sẽ bị Term sau deploy.
- **grpcsrv wiring** chuyển sang `apps/core/service_wiring.go` (Task 9); route `fakeCore` phải cài mọi RPC mới mà client gọi (Task 13).
- **Quyền (D86, owner chốt 2026-10-05):** core không có luật tác giả/owner; `access.DefaultPolicy` từ chối sửa/xoá tin của người khác, kể cả owner room hay người tạo DM (`RoleOwner`). Owner/moderator xoá tin người khác là việc của module policy chat Phase 2 (cắm qua `access.Policy`).
- **`GetEditHistory` trên tin người đọc đã ẩn/clear:** mặc định vẫn trả lịch sử (view ẩn chỉ áp ở `GetHistory`); policy có thể đổi (`ReadEditHistory` mang `Author`).

---

### Task 0: Branch + baseline

Toàn bộ M2b dùng chung nhánh `feat/m2b`. Plan này đã được controller commit trước khi thực thi. Task này đồng bộ nhánh, chốt quy tắc commit và chụp baseline xanh để lỗi ở task sau không lẫn với lỗi có sẵn.

**Step 1: Đồng bộ nhánh**

```bash
git switch feat/m2b
git pull --ff-only
git branch --show-current
git status --short
```

Expected: `feat/m2b`. `git status --short` không có dòng nào thuộc `apps/`, `proto/`, `pkg/`, `tools/` hoặc `INDEXES.csv`. Owner có thể đang có thay đổi doc riêng (`docs/...`, `CLAUDE.md`, `README.md`), đã stage hoặc chưa: không đụng, không stage, không stash, không revert.

Nếu `INDEXES.csv` có thay đổi chưa commit (` M INDEXES.csv` hoặc `M  INDEXES.csv`): **dừng và báo controller**. Mọi task dưới đây sửa `INDEXES.csv` và commit theo pathspec, nên sẽ cuốn luôn thay đổi của owner trong file đó.

**Quy tắc commit cho mọi task của M2b.2** (thay cho `git add <dir>/ && git commit -m`):

- File **mới**: `git add <đúng file mới>` (không add thư mục).
- Commit: `git commit -m "<message>" -- <mọi path task sửa hoặc tạo>`. Có pathspec thì git commit đúng các path đó (`--only`); file owner đã stage vẫn ở nguyên trong index.
- Cấm `git add -A`, `git add .`, `git commit -a`, `git stash`.
- Sau mỗi commit: `git show --stat HEAD` chỉ được liệt kê file của task.
- Không `git add INDEXES.csv`/`README.md`/`CLAUDE.md`/docs: pathspec của `git commit` đã gồm file đó. Trước mỗi commit có sửa `INDEXES.csv`: nếu file đã có thay đổi của người khác trước khi task bắt đầu thì dừng, báo controller; chạy `python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"` → Expected `{7}`.
- `git pull --ff-only` cần upstream: `feat/m2b` đã track `origin/feat/m2b`.

**Step 2: Baseline**

Run: `make fmt-check && make vet && make lint && make test`
Expected: tất cả xanh. Có gì đỏ thì dừng và báo cáo, không bắt đầu Task 1.

---

### Task 1: `domain` — trạng thái sửa của tin, fact `Edit`, `Member.ClearedBeforeSeq`, lỗi

Đặt kiểu dữ liệu chung cho mọi task sau. `Message` thêm `Version`, `Deleted`, `EditedAt` (projection lưu trong `messages`) và `Hidden` (cờ chỉ của view, adapter không lưu). `Edit` là fact bất biến của `message_edits`. **`Edit.Prev` (text gốc) chỉ có trên fact sửa (`Kind == EditText`) ở `Version == 1`; fact xoá không bao giờ mang `Prev`**, nên `PurgeText(key, v−1)` sau một lần xoá luôn gỡ được text gốc (D75). Xoá một tin chưa từng sửa (fact v1 kind delete) không chép text gốc vào `message_edits`; projection đặt `x = ""`. `store.ValidateEdit` (Task 3) chặn quy tắc này ở mọi adapter. Giá trị `EditKind` là giá trị lưu trong field `k`, nên test chốt `EditText = 1`, `EditDelete = 2`. Không thêm validator mới: `mutate` (Task 7) dùng `domain.ValidateText` cho `Edit.Text` khi `Kind == EditText`.

Đã grep: không có struct literal không khoá (unkeyed) nào của `domain.Message`/`domain.Member`. Các phép `==` trên hai kiểu này (`storetest.sameMessage`, `storetest.assertMember`, `codec_test.go`) vẫn biên dịch, vì field mới đều so sánh được. Giá trị zero của field mới không đổi kết quả cho tới Task 3.

**Files:**
- Modify: `apps/core/internal/domain/message.go`
- Create: `apps/core/internal/domain/edit.go`
- Modify: `apps/core/internal/domain/room.go` (`Member`)
- Modify: `apps/core/internal/domain/errors.go`
- Modify: `apps/core/internal/domain/errors_test.go`
- Create: `apps/core/internal/domain/edit_test.go`
- Modify: `INDEXES.csv` (dòng `apps/core/internal/domain`)

**Step 1: Test**

`apps/core/internal/domain/errors_test.go`, trong `TestDomainErrorsWrapAppKinds` thay:

```go
		{domain.ErrRetryLater, apperr.ErrUnavailable},
	}
```

bằng:

```go
		{domain.ErrRetryLater, apperr.ErrUnavailable},
		{domain.ErrMessageNotFound, apperr.ErrNotFound},
		{domain.ErrMessageDeleted, apperr.ErrFailedPrecondition},
		{domain.ErrVersionConflict, apperr.ErrFailedPrecondition},
	}
```

`apps/core/internal/domain/edit_test.go`:

```go
package domain_test

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func TestEditKindsAreTheStoredValues(t *testing.T) {
	if domain.EditText != 1 || domain.EditDelete != 2 {
		t.Fatalf("EditText = %d, EditDelete = %d; want 1 and 2, the values stored in message_edits.k", domain.EditText, domain.EditDelete)
	}
}

func TestNewMessagesAndMembersCarryNoEditOrClearState(t *testing.T) {
	m := domain.Message{Room: 1, Seq: 1, Text: "hi", CreatedAt: time.UnixMilli(1)}
	if m.Version != 0 || m.Deleted || !m.EditedAt.IsZero() || m.Hidden {
		t.Fatalf("new message %+v carries edit state, want version 0 and no flags", m)
	}
	if (domain.Member{Room: 1, User: "alice"}).ClearedBeforeSeq != 0 {
		t.Fatal("a new member starts with cleared history")
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/domain/..."`
Expected: FAIL biên dịch: `undefined: domain.ErrMessageNotFound`, `undefined: domain.EditText`, `m.Version undefined`, `unknown field ClearedBeforeSeq`.

**Step 3: Code**

`apps/core/internal/domain/message.go`, thay struct `Message`:

```go
type Message struct {
	Room      uint64
	Thread    uint64
	Seq       uint64
	Tenant    string
	From      string
	Kind      Kind
	Text      string
	CID       string
	CreatedAt time.Time
}
```

bằng:

```go
type Message struct {
	Room      uint64
	Thread    uint64
	Seq       uint64
	Tenant    string
	From      string
	Kind      Kind
	Text      string
	CID       string
	CreatedAt time.Time
	Version   uint32
	Deleted   bool
	EditedAt  time.Time
	Hidden    bool
}
```

`apps/core/internal/domain/edit.go`:

```go
package domain

import "time"

type EditKind uint8

const (
	EditText EditKind = iota + 1
	EditDelete
)

type Edit struct {
	Room    uint64
	Thread  uint64
	Seq     uint64
	Version uint32
	Kind    EditKind
	Tenant  string
	By      string
	Text    string
	Prev    string
	At      time.Time
}
```

`apps/core/internal/domain/room.go`, thay struct `Member`:

```go
type Member struct {
	Room     uint64
	Tenant   string
	User     string
	Role     Role
	JoinedAt time.Time
}
```

bằng:

```go
type Member struct {
	Room             uint64
	Tenant           string
	User             string
	Role             Role
	JoinedAt         time.Time
	ClearedBeforeSeq uint64
}
```

`apps/core/internal/domain/errors.go`, thay khối `var (...)`:

```go
var (
	ErrRoomNotFound = fmt.Errorf("room %w", apperr.ErrNotFound)
	ErrNotMember    = fmt.Errorf("not a member: %w", apperr.ErrPermissionDenied)
	ErrBusy         = fmt.Errorf("room busy: %w", apperr.ErrResourceExhausted)
	ErrRetryLater   = fmt.Errorf("retry later: %w", apperr.ErrUnavailable)
)
```

bằng:

```go
var (
	ErrRoomNotFound    = fmt.Errorf("room %w", apperr.ErrNotFound)
	ErrNotMember       = fmt.Errorf("not a member: %w", apperr.ErrPermissionDenied)
	ErrBusy            = fmt.Errorf("room busy: %w", apperr.ErrResourceExhausted)
	ErrRetryLater      = fmt.Errorf("retry later: %w", apperr.ErrUnavailable)
	ErrMessageNotFound = fmt.Errorf("message %w", apperr.ErrNotFound)
	ErrMessageDeleted  = fmt.Errorf("message deleted: %w", apperr.ErrFailedPrecondition)
	ErrVersionConflict = fmt.Errorf("message version conflict: %w", apperr.ErrFailedPrecondition)
)
```

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/domain/..."` rồi `make vet` (biên dịch mọi package và test, để chắc không caller nào vỡ).
Expected: PASS; vet sạch.

**Step 5: Commit**

INDEXES.csv, dòng `apps/core/internal/domain`:
- thay `Room/Member/Message types and validation:` bằng `Room/Member/Message types and validation; Message edit state (Version/Deleted/EditedAt) and view-only Hidden; Edit fact (EditText/EditDelete; Prev on version 1); Member.ClearedBeforeSeq;`
- trong key symbols thay `ErrBusy;ErrRetryLater` bằng `ErrBusy;ErrRetryLater;Edit;EditKind;EditText;EditDelete;ErrMessageNotFound;ErrMessageDeleted;ErrVersionConflict`
- cột decisions thay `D7;D35` bằng `D7;D35;D62;D63`.

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/domain/edit.go apps/core/internal/domain/edit_test.go
git commit -m "feat(domain): add message edit state, edit facts and cleared history" -- apps/core/internal/domain/ INDEXES.csv
```

---

### Task 2: Proto + pbconv + publish kinds

Proto thêm trạng thái sửa vào `Message` (field 10–13), 5 RPC, enum `EditKind`, `MessageVersion` và hai payload event `message_edited = 22`, `message_deleted = 23`. `pbconv` map các field mới (`edited_at` zero → nil để tin chưa sửa không đổi bytes), dựng id `{room}-{th}-{seq}-v{ver}`, envelope `msg_edited`/`msg_deleted` và danh sách phiên bản. Publisher biết hai kind mới (subject `evt.{t}.room.{rid}.msg_edited|msg_deleted`) và **không** đặt ack mark (D83).

`grpcsrv.Service` nhúng `chatimv1.UnimplementedCoreServiceServer` nên 5 RPC mới trả `Unimplemented` tới Task 9, không cần sửa. `tools/internal/route/fakes_test.go` có `fakeCore` tự cài **đủ** `chatimv1.CoreServiceClient` (không nhúng), nên sẽ vỡ biên dịch khi interface thêm method: task này nhúng interface vào `fakeCore` (method chưa cài sẽ panic nếu bị gọi; Task 13 cài các method nó dùng). `tools/corecli/internal/e2e.EventOf` đọc `GetMessageCreated()` và trả `ok=false` cho payload khác (đã có test cho `room_created`), `tools/poc/corebench/live.go` dùng getter nil-safe: không cần sửa.

**Files:**
- Modify: `proto/chatim/v1/core.proto`, `proto/chatim/v1/events.proto`
- Regenerate: `pkg/pb/chatim/v1/core.pb.go`, `pkg/pb/chatim/v1/core_grpc.pb.go`, `pkg/pb/chatim/v1/events.pb.go` (`make proto`)
- Modify: `tools/internal/route/fakes_test.go` (`fakeCore`)
- Modify: `apps/core/internal/pbconv/pbconv.go` (`Message`, thêm `optionalTime`, import `time`)
- Create: `apps/core/internal/pbconv/message_change.go`
- Create: `apps/core/internal/pbconv/message_change_test.go`
- Modify: `apps/core/internal/publish/stream.go` (const), `apps/core/internal/publish/message.go` (`eventKind`)
- Create: `apps/core/internal/publish/message_change_event_test.go`
- Modify: `INDEXES.csv` (dòng `apps/core/internal/pbconv`, `apps/core/internal/publish`, `pkg/pb/chatim/v1`)

**Step 1: Proto**

`proto/chatim/v1/core.proto`:

- thay:

```proto
  rpc GetHistory(GetHistoryRequest) returns (GetHistoryResponse);
}
```

bằng:

```proto
  rpc GetHistory(GetHistoryRequest) returns (GetHistoryResponse);
  rpc EditMessage(EditMessageRequest) returns (EditMessageResponse);
  rpc DeleteMessage(DeleteMessageRequest) returns (DeleteMessageResponse);
  rpc HideMessage(HideMessageRequest) returns (HideMessageResponse);
  rpc ClearHistory(ClearHistoryRequest) returns (ClearHistoryResponse);
  rpc GetEditHistory(GetEditHistoryRequest) returns (GetEditHistoryResponse);
}
```

- ngay sau khối `enum HistoryAnchor { ... }` thêm:

```proto

enum EditKind {
  EDIT_KIND_UNSPECIFIED = 0;
  EDIT_KIND_ORIGINAL = 1;
  EDIT_KIND_TEXT = 2;
  EDIT_KIND_DELETE = 3;
}
```

- trong `message Message`, thay:

```proto
  google.protobuf.Timestamp created_at = 9;
}
```

bằng:

```proto
  google.protobuf.Timestamp created_at = 9;
  uint32 version = 10;
  bool deleted = 11;
  google.protobuf.Timestamp edited_at = 12;
  bool hidden = 13;
}

message MessageVersion {
  uint32 version = 1;
  EditKind kind = 2;
  string text = 3;
  string by = 4;
  google.protobuf.Timestamp at = 5;
}
```

(chuỗi `google.protobuf.Timestamp created_at = 9;\n}` là duy nhất: `Room` dùng `created_at = 6`.)

- thêm vào cuối file:

```proto

message EditMessageRequest {
  string room_id = 1;
  uint64 thread_root = 2;
  uint64 seq = 3;
  uint32 base_version = 4;
  string text = 5;
}

message EditMessageResponse {
  Message message = 1;
}

message DeleteMessageRequest {
  string room_id = 1;
  uint64 thread_root = 2;
  uint64 seq = 3;
  uint32 base_version = 4;
}

message DeleteMessageResponse {
  Message message = 1;
}

message HideMessageRequest {
  string room_id = 1;
  uint64 thread_root = 2;
  uint64 seq = 3;
}

message HideMessageResponse {}

message ClearHistoryRequest {
  string room_id = 1;
  uint64 up_to_seq = 2;
}

message ClearHistoryResponse {
  uint64 cleared_before_seq = 1;
}

message GetEditHistoryRequest {
  string room_id = 1;
  uint64 thread_root = 2;
  uint64 seq = 3;
  uint32 after_version = 4;
  uint32 limit = 5;
}

message GetEditHistoryResponse {
  repeated MessageVersion versions = 1;
}
```

(Viết nhiều dòng như trên, không viết một dòng như hợp đồng: `make buf-lint` chạy `buf format -d --exit-code` và sẽ đòi dạng nhiều dòng.)

`proto/chatim/v1/events.proto`:

- thay:

```proto
    RoomCreated room_created = 21;
  }
}
```

bằng:

```proto
    RoomCreated room_created = 21;
    MessageEdited message_edited = 22;
    MessageDeleted message_deleted = 23;
  }
}
```

- thêm vào cuối file:

```proto

message MessageEdited {
  Message message = 1;
  uint32 version = 2;
}

message MessageDeleted {
  Message message = 1;
  uint32 version = 2;
}
```

Run: `make proto && make buf-lint`
Expected: không lỗi; `git status --short` có `M` ở `pkg/pb/chatim/v1/core.pb.go`, `core_grpc.pb.go`, `events.pb.go`. Kiểm: `grep -l "coreServiceClient) EditMessage" pkg/pb/chatim/v1/core_grpc.pb.go`, `grep -l "type Event_MessageEdited struct" pkg/pb/chatim/v1/events.pb.go`, `grep -l "EditKind_EDIT_KIND_ORIGINAL" pkg/pb/chatim/v1/core.pb.go` đều in tên file.

**Step 2: Biên dịch toàn repo, thấy fail ở route fake**

Run: `make vet`
Expected: FAIL biên dịch đúng một chỗ: `tools/internal/route/fakes_test.go`: `*fakeCore does not implement chatimv1.CoreServiceClient (missing method ClearHistory)`. Lỗi khác (đặc biệt ở `apps/core/internal/grpcsrv`) → dừng, báo cáo.

`tools/internal/route/fakes_test.go`, thay:

```go
type fakeCore struct {
	mu      sync.Mutex
```

bằng:

```go
type fakeCore struct {
	chatimv1.CoreServiceClient
	mu      sync.Mutex
```

Run: `make vet`
Expected: sạch.

**Step 3: Test pbconv + publish**

`apps/core/internal/pbconv/message_change_test.go` (`sample()`, `sentAt` có sẵn ở `pbconv_test.go`):

```go
package pbconv_test

import (
	"math"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var editedAt = sentAt.Add(time.Minute)

func TestMessageChangeEventIDAppendsTheVersion(t *testing.T) {
	cases := []struct {
		room, thread, seq uint64
		version           uint32
		want              string
	}{
		{42, 0, 7, 1, "42-0-7-v1"},
		{42, 3, 9, 12, "42-3-9-v12"},
		{1, 0, math.MaxUint64, math.MaxUint32, "1-0-18446744073709551615-v4294967295"},
	}
	for _, c := range cases {
		if got := pbconv.MessageChangeEventID(c.room, c.thread, c.seq, c.version); got != c.want {
			t.Errorf("MessageChangeEventID(%d, %d, %d, %d) = %q, want %q", c.room, c.thread, c.seq, c.version, got, c.want)
		}
	}
}

func TestMessageCarriesEditState(t *testing.T) {
	m := sample()
	m.Text, m.Version, m.Deleted, m.EditedAt, m.Hidden = "", 3, true, editedAt, true
	want := &chatimv1.Message{
		RoomId: "9007199254740993", Seq: 7, Sender: "alice", Kind: chatimv1.MessageKind_MESSAGE_KIND_TEXT,
		Cid: "c-1", CreatedAt: timestamppb.New(sentAt), Version: 3, Deleted: true, EditedAt: timestamppb.New(editedAt), Hidden: true,
	}
	if got := pbconv.Message(m); !proto.Equal(got, want) {
		t.Fatalf("Message = %v, want %v", got, want)
	}
	if got := pbconv.Message(sample()); got.GetEditedAt() != nil || got.GetVersion() != 0 {
		t.Fatalf("unedited message = %v, want no edited_at and version 0", got)
	}
}

func changeOf(kind domain.EditKind) (domain.Message, domain.Edit) {
	m := sample()
	m.Version, m.EditedAt = 2, editedAt
	e := domain.Edit{Room: m.Room, Seq: m.Seq, Version: 2, Kind: kind, Tenant: "acme", By: "bob", At: editedAt}
	if kind == domain.EditDelete {
		m.Text, m.Deleted = "", true
		return m, e
	}
	m.Text, e.Text = "đã sửa", "đã sửa"
	return m, e
}

func changeEnvelope() *chatimv1.Event {
	return &chatimv1.Event{
		Id: "9007199254740993-0-7-v2", Tenant: "acme", RoomId: "9007199254740993", RoomType: chatimv1.RoomType_ROOM_TYPE_GROUP,
		Seq: 7, Actor: "bob", Ts: timestamppb.New(editedAt),
	}
}

func TestMessageEditedEnvelope(t *testing.T) {
	m, e := changeOf(domain.EditText)
	want := changeEnvelope()
	want.Payload = &chatimv1.Event_MessageEdited{MessageEdited: &chatimv1.MessageEdited{Message: pbconv.Message(m), Version: 2}}
	if got := pbconv.MessageEdited(domain.RoomGroup, m, e); !proto.Equal(got, want) {
		t.Fatalf("MessageEdited = %v, want %v", got, want)
	}
}

func TestMessageDeletedEnvelope(t *testing.T) {
	m, e := changeOf(domain.EditDelete)
	want := changeEnvelope()
	want.Payload = &chatimv1.Event_MessageDeleted{MessageDeleted: &chatimv1.MessageDeleted{Message: pbconv.Message(m), Version: 2}}
	got := pbconv.MessageDeleted(domain.RoomGroup, m, e)
	if !proto.Equal(got, want) {
		t.Fatalf("MessageDeleted = %v, want %v", got, want)
	}
	if text := got.GetMessageDeleted().GetMessage().GetText(); text != "" {
		t.Fatalf("deleted snapshot carries text %q", text)
	}
}

func TestMessageVersionsStartWithTheOriginalOnlyOnTheFirstPage(t *testing.T) {
	m := sample()
	v1 := domain.Edit{Room: m.Room, Seq: m.Seq, Version: 1, Kind: domain.EditText, By: "alice", Text: "v1", Prev: "xin chào", At: editedAt}
	v2 := domain.Edit{Room: m.Room, Seq: m.Seq, Version: 2, Kind: domain.EditDelete, By: "bob", At: editedAt.Add(time.Minute)}
	original := &chatimv1.MessageVersion{Kind: chatimv1.EditKind_EDIT_KIND_ORIGINAL, Text: "xin chào", By: "alice", At: timestamppb.New(sentAt)}
	first := &chatimv1.MessageVersion{Version: 1, Kind: chatimv1.EditKind_EDIT_KIND_TEXT, Text: "v1", By: "alice", At: timestamppb.New(editedAt)}
	second := &chatimv1.MessageVersion{Version: 2, Kind: chatimv1.EditKind_EDIT_KIND_DELETE, By: "bob", At: timestamppb.New(editedAt.Add(time.Minute))}
	cases := []struct {
		name  string
		edits []domain.Edit
		after uint32
		want  []*chatimv1.MessageVersion
	}{
		{"first page", []domain.Edit{v1, v2}, 0, []*chatimv1.MessageVersion{original, first, second}},
		{"next page", []domain.Edit{v2}, 1, []*chatimv1.MessageVersion{second}},
		{"never edited", nil, 0, nil},
		{"first page without version 1", []domain.Edit{v2}, 0, []*chatimv1.MessageVersion{second}},
	}
	for _, c := range cases {
		got := pbconv.MessageVersions(m, c.edits, c.after)
		if !slices.EqualFunc(got, c.want, func(a, b *chatimv1.MessageVersion) bool { return proto.Equal(a, b) }) {
			t.Errorf("%s: MessageVersions = %v, want %v", c.name, got, c.want)
		}
	}
}
```

`apps/core/internal/publish/message_change_event_test.go` (`tenant`, `roomA = 101`, `sentAt` có sẵn ở `harness_test.go`):

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

func TestMessageChangesGoToTheirOwnSubjectsWithoutAMark(t *testing.T) {
	m := domain.Message{Room: roomA, Seq: 7, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "new", CID: "c-7", CreatedAt: sentAt, Version: 2, EditedAt: sentAt}
	e := domain.Edit{Room: roomA, Seq: 7, Version: 2, Kind: domain.EditText, Tenant: tenant, By: "alice", Text: "new", At: sentAt}
	cases := map[string]struct {
		ev      *chatimv1.Event
		subject string
	}{
		"edited":  {pbconv.MessageEdited(domain.RoomGroup, m, e), "evt.acme.room.101.msg_edited"},
		"deleted": {pbconv.MessageDeleted(domain.RoomGroup, m, e), "evt.acme.room.101.msg_deleted"},
	}
	for name, c := range cases {
		msg, err := publish.Message("evt", roomA, c.ev)
		if err != nil {
			t.Fatalf("%s: Message: %v", name, err)
		}
		if msg.Subject != c.subject || publishtest.MsgID(msg) != "101-0-7-v2" {
			t.Fatalf("%s: subject %q msg id %q, want %s and 101-0-7-v2", name, msg.Subject, publishtest.MsgID(msg), c.subject)
		}
		if key, ok := publish.MarkKey(roomA, c.ev); ok {
			t.Fatalf("%s: MarkKey = %v, true; want no mark", name, key)
		}
	}
}
```

**Step 4: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/pbconv/... ./apps/core/internal/publish/..."`
Expected: FAIL biên dịch ở pbconv: `undefined: pbconv.MessageChangeEventID`, `undefined: pbconv.MessageEdited`, `undefined: pbconv.MessageDeleted`, `undefined: pbconv.MessageVersions` (publish cũng fail biên dịch vì cùng symbol).

**Step 5: Code pbconv**

`apps/core/internal/pbconv/pbconv.go`:
- trong import, thêm `"time"` vào nhóm stdlib (sau `"strconv"`).
- thay:

```go
		Cid:        m.CID,
		CreatedAt:  timestamppb.New(m.CreatedAt),
	}
}
```

bằng:

```go
		Cid:        m.CID,
		CreatedAt:  timestamppb.New(m.CreatedAt),
		Version:    m.Version,
		Deleted:    m.Deleted,
		EditedAt:   optionalTime(m.EditedAt),
		Hidden:     m.Hidden,
	}
}

func optionalTime(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}
```

`apps/core/internal/pbconv/message_change.go`:

```go
package pbconv

import (
	"strconv"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func MessageChangeEventID(room, thread, seq uint64, version uint32) string {
	return MessageEventID(room, thread, seq) + "-v" + strconv.FormatUint(uint64(version), 10)
}

func MessageEdited(roomType domain.RoomType, m domain.Message, e domain.Edit) *chatimv1.Event {
	ev := messageChange(roomType, m, e)
	ev.Payload = &chatimv1.Event_MessageEdited{MessageEdited: &chatimv1.MessageEdited{Message: Message(m), Version: e.Version}}
	return ev
}

func MessageDeleted(roomType domain.RoomType, m domain.Message, e domain.Edit) *chatimv1.Event {
	ev := messageChange(roomType, m, e)
	ev.Payload = &chatimv1.Event_MessageDeleted{MessageDeleted: &chatimv1.MessageDeleted{Message: Message(m), Version: e.Version}}
	return ev
}

func messageChange(roomType domain.RoomType, m domain.Message, e domain.Edit) *chatimv1.Event {
	return &chatimv1.Event{
		Id:         MessageChangeEventID(e.Room, e.Thread, e.Seq, e.Version),
		Tenant:     m.Tenant,
		RoomId:     RoomID(m.Room),
		RoomType:   RoomType(roomType),
		ThreadRoot: m.Thread,
		Seq:        m.Seq,
		Actor:      e.By,
		Ts:         timestamppb.New(e.At),
	}
}

func MessageVersions(m domain.Message, edits []domain.Edit, after uint32) []*chatimv1.MessageVersion {
	out := make([]*chatimv1.MessageVersion, 0, len(edits)+1)
	if after == 0 && len(edits) > 0 && edits[0].Version == 1 {
		out = append(out, &chatimv1.MessageVersion{
			Kind: chatimv1.EditKind_EDIT_KIND_ORIGINAL, Text: edits[0].Prev, By: m.From, At: timestamppb.New(m.CreatedAt),
		})
	}
	for _, e := range edits {
		out = append(out, &chatimv1.MessageVersion{Version: e.Version, Kind: editKind(e.Kind), Text: e.Text, By: e.By, At: timestamppb.New(e.At)})
	}
	return out
}

func editKind(k domain.EditKind) chatimv1.EditKind {
	switch k {
	case domain.EditText:
		return chatimv1.EditKind_EDIT_KIND_TEXT
	case domain.EditDelete:
		return chatimv1.EditKind_EDIT_KIND_DELETE
	default:
		return chatimv1.EditKind_EDIT_KIND_UNSPECIFIED
	}
}
```

**Step 6: Code publish**

`apps/core/internal/publish/stream.go`, thay:

```go
	msgCreated  = "msg_created"
	roomCreated = "room_created"
)
```

bằng:

```go
	msgCreated  = "msg_created"
	roomCreated = "room_created"
	msgEdited   = "msg_edited"
	msgDeleted  = "msg_deleted"
)
```

`apps/core/internal/publish/message.go`, trong `eventKind` thay:

```go
	case *chatimv1.Event_RoomCreated:
		return roomCreated, true
```

bằng:

```go
	case *chatimv1.Event_RoomCreated:
		return roomCreated, true
	case *chatimv1.Event_MessageEdited:
		return msgEdited, true
	case *chatimv1.Event_MessageDeleted:
		return msgDeleted, true
```

`ack_mark_policy.go` giữ nguyên: `markKey` chỉ có case `Event_MessageCreated`, mọi payload khác rơi vào `default` (không mark). Test ở Step 3 chốt điều đó.

**Step 7: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/pbconv/... ./apps/core/internal/publish/... ./apps/core/internal/grpcsrv/... ./tools/internal/route/... ./tools/corecli/..."`
Expected: PASS. Đặc biệt `grpcsrv` `TestHistoryPagesThroughWhatWasSent` (so message proto) vẫn xanh: tin chưa sửa có `EditedAt` zero nên `edited_at` là nil, `version`/`deleted`/`hidden` là giá trị zero; đó là lý do `optionalTime` tồn tại, không được đổi thành `timestamppb.New` vô điều kiện. `wc -l apps/core/internal/pbconv/*.go` mỗi file < 200.

**Step 8: Commit**

INDEXES.csv:
- dòng `apps/core/internal/pbconv`: thay `RoomCreatedEventID {room}-created,` bằng `RoomCreatedEventID {room}-created; MessageChangeEventID {room}-{thread}-{seq}-v{version}; Message carries version/deleted/edited_at (nil when zero)/hidden; MessageEdited/MessageDeleted envelopes (current snapshot + fact version; actor and ts from the fact); MessageVersions (version 0 = original from Prev on the first page),`; trong key symbols thay `RoomCreated;Room;` bằng `RoomCreated;MessageChangeEventID;MessageEdited;MessageDeleted;MessageVersions;Room;`; cột decisions thay `D48` bằng `D48;D83`.
- dòng `apps/core/internal/publish`: thay `publishes msg_created and room_created; marks only events whose type has an ack mark policy (msg_created; room_created has none)` bằng `publishes msg_created/room_created/msg_edited/msg_deleted; marks only events whose type has an ack mark policy (msg_created; the others have none)`; cột decisions thay `D65;D76` bằng `D65;D76;D83`.
- dòng `pkg/pb/chatim/v1`: thay `Event;MessageCreated;RoomCreated;Message;Room` bằng `Event;MessageCreated;RoomCreated;MessageEdited;MessageDeleted;Message;MessageVersion;EditKind;Room`.

```bash
make fmt-check && make vet && make lint && make buf-lint
git add apps/core/internal/pbconv/message_change.go apps/core/internal/pbconv/message_change_test.go apps/core/internal/publish/message_change_event_test.go
git commit -m "feat(proto): add edit, delete, hide, clear history and edit history RPCs and change events" -- proto/chatim/v1/ pkg/pb/chatim/v1/ tools/internal/route/fakes_test.go apps/core/internal/pbconv/ apps/core/internal/publish/ INDEXES.csv
```

---

### Task 3: Store ports + memstore + contract `RunEdits`

Thêm port `Edits`, `Hidden` theo hợp đồng. **Tinh chỉnh hợp đồng (ghi ở cuối part A):** `ApplyEdit` và `ClearHistory` không nằm trong `Messages`/`Rooms` mà ở hai interface nhỏ `store.MessageEditor` và `store.HistoryClearer`. Nhờ vậy `mongostore.Store` và mọi fake đang cài `store.Messages`/`store.Rooms` (actor, grpcsrv, effects, resync) không vỡ ở task này; Mongo cài cả bốn port mới ở Task 4. `*memstore.Messages` cài `ApplyEdit`, `*memstore.Rooms` cài `ClearHistory`; `Member` của memstore trả `ClearedBeforeSeq` ngay, của Mongo trả 0 tới Task 4.

Thêm helper dùng chung (adapter, `mutate`, `effects` đều cần): `store.EditKeyOf(e)`, `store.ValidateEdit(e)` (khoá hợp lệ, `1 ≤ Version ≤ MaxInt32` vì field `v` của `messages` là int32, kind là `EditText`/`EditDelete`, `Prev` chỉ được có khi `Kind == EditText && Version == 1`), `store.ValidateLimit(limit, max)`, `store.MaxEditScan = 1000` (trần `limit` của `Between`).

**Room activity cho sửa (controller chốt):** Task 11 đăng ký `room_activity` cho `EditInserted` với `store.Activity{Room, Thread, Seq: 0, At}`. `TouchActivity` chỉ nâng `ls`/`lm` (`LastSeq`/`LastMsgAt`) khi `Thread == 0 && Seq > 0`; `lc`/`ab` (`LastChangeAt`, bucket) luôn nâng. Task này sửa memstore và thêm case contract `an activity with seq 0 bumps only the last change`; Mongo sửa ở Task 4 (case này chạy trên Mongo chỉ trong `make itest` cuối Task 4, nên giữa Task 3 và 4 không chạy itest).

Contract mới là một entry riêng `storetest.RunEdits`, nên `storetest.Run`/`RunFeed` và mọi caller giữ nguyên. `sameMessage` so `EditedAt` bằng `Equal` (Mongo trả time UTC, memstore giữ nguyên value).

**Files:**
- Create: `apps/core/internal/store/edit.go`
- Create: `apps/core/internal/store/edit_test.go`
- Modify: `apps/core/internal/store/ports.go`
- Modify: `apps/core/internal/store/write_contract_test.go`
- Create: `apps/core/internal/store/memstore/edits.go`, `apps/core/internal/store/memstore/hidden.go`, `apps/core/internal/store/memstore/apply_edit.go`
- Modify: `apps/core/internal/store/memstore/memstore.go` (`insertLocked`), `apps/core/internal/store/memstore/rooms.go` (`ClearHistory`), `apps/core/internal/store/memstore/room_activity.go` (`withActivity`)
- Modify: `apps/core/internal/store/memstore/memstore_test.go`
- Create: `apps/core/internal/store/storetest/edit_cases.go`, `fact_cases.go`, `apply_cases.go`, `viewer_cases.go`
- Modify: `apps/core/internal/store/storetest/fixtures.go` (`sameMessage`), `apps/core/internal/store/storetest/activity_cases.go` (case seq 0)
- Modify: `INDEXES.csv` (dòng `store`, `store/memstore`, `store/storetest`)

**Step 1: Test store**

`apps/core/internal/store/edit_test.go`:

```go
package store_test

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestEditErrorsWrapAppKinds(t *testing.T) {
	if !errors.Is(store.ErrEditExists, apperr.ErrAlreadyExists) || !errors.Is(store.ErrEditNotFound, apperr.ErrNotFound) {
		t.Fatalf("ErrEditExists = %v, ErrEditNotFound = %v; want already exists and not found", store.ErrEditExists, store.ErrEditNotFound)
	}
}

func TestEditKeyOf(t *testing.T) {
	e := domain.Edit{Room: 42, Thread: 7, Seq: 3, Version: 2, Text: "hi"}
	if got, want := store.EditKeyOf(e), (store.MsgKey{Room: 42, Thread: 7, Seq: 3}); got != want {
		t.Fatalf("EditKeyOf = %+v, want %+v", got, want)
	}
}

func TestValidateEdit(t *testing.T) {
	good := domain.Edit{Room: 1, Seq: 1, Version: 1, Kind: domain.EditText}
	tests := []struct {
		name   string
		mutate func(*domain.Edit)
		field  string
	}{
		{"text", func(*domain.Edit) {}, ""},
		{"delete", func(e *domain.Edit) { e.Kind = domain.EditDelete }, ""},
		{"max int32 version", func(e *domain.Edit) { e.Version = math.MaxInt32 }, ""},
		{"zero room", func(e *domain.Edit) { e.Room = 0 }, "room"},
		{"zero seq", func(e *domain.Edit) { e.Seq = 0 }, "seq"},
		{"zero version", func(e *domain.Edit) { e.Version = 0 }, "version"},
		{"version above max int32", func(e *domain.Edit) { e.Version = math.MaxInt32 + 1 }, "version"},
		{"zero kind", func(e *domain.Edit) { e.Kind = 0 }, "edit kind"},
		{"kind past delete", func(e *domain.Edit) { e.Kind = domain.EditDelete + 1 }, "edit kind"},
		{"prev on the first edit", func(e *domain.Edit) { e.Prev = "original" }, ""},
		{"prev on a delete", func(e *domain.Edit) { e.Kind, e.Prev = domain.EditDelete, "original" }, "prev"},
		{"prev after version 1", func(e *domain.Edit) { e.Version, e.Prev = 2, "original" }, "prev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := good
			tt.mutate(&e)
			err := store.ValidateEdit(e)
			switch {
			case tt.field == "" && err != nil:
				t.Fatalf("ValidateEdit = %v, want nil", err)
			case tt.field != "" && (!errors.Is(err, apperr.ErrInvalidArgument) || !strings.HasSuffix(err.Error(), ": "+tt.field)):
				t.Fatalf("ValidateEdit = %v, want ErrInvalidArgument naming %q", err, tt.field)
			}
		})
	}
}

func TestValidateLimit(t *testing.T) {
	for limit, ok := range map[int]bool{-1: false, 0: false, 1: true, 100: true, 101: false} {
		err := store.ValidateLimit(limit, 100)
		if ok != (err == nil) || (!ok && !errors.Is(err, apperr.ErrInvalidArgument)) {
			t.Errorf("ValidateLimit(%d, 100) = %v, want ok=%v", limit, err, ok)
		}
	}
}
```

`apps/core/internal/store/write_contract_test.go`:
- thay:

```go
	"read": true, "lifecycle": true, "reset": true,
```

bằng:

```go
	"read": true, "lifecycle": true, "reset": true, "purge": true,
```

- trong `portMethods`, thay:

```go
	"Cursor.Close":        "lifecycle",
}
```

bằng:

```go
	"Cursor.Close":        "lifecycle",

	"MessageEditor.ApplyEdit":     "cas",
	"HistoryClearer.ClearHistory": "monotonic-max",
	"Edits.Append":                "insert-unique",
	"Edits.At":                    "read",
	"Edits.Latest":                "read",
	"Edits.History":               "read",
	"Edits.Between":               "read",
	"Edits.PurgeText":             "purge",
	"Hidden.Hide":                 "upsert",
	"Hidden.HiddenIn":             "read",
}
```

(dòng trống tách hai nhóm để gofmt không căn lại 14 dòng cũ.)

- thay:

```go
		reflect.TypeFor[store.Cursor](),
	}
```

bằng:

```go
		reflect.TypeFor[store.Cursor](),
		reflect.TypeFor[store.MessageEditor](),
		reflect.TypeFor[store.HistoryClearer](),
		reflect.TypeFor[store.Edits](),
		reflect.TypeFor[store.Hidden](),
	}
```

- thay `writes must be insert-unique, cas, monotonic-cas, monotonic-max, upsert or version-bump` bằng `writes must be insert-unique, cas, monotonic-cas, monotonic-max, upsert, version-bump or purge`.

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/"`
Expected: FAIL biên dịch: `undefined: store.ErrEditExists`, `undefined: store.EditKeyOf`, `undefined: store.ValidateEdit`, `undefined: store.ValidateLimit`, `undefined: store.MessageEditor`, `undefined: store.Edits`.

**Step 3: Code store**

`apps/core/internal/store/edit.go`:

```go
package store

import (
	"fmt"
	"math"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	MaxEditPage = 100
	MaxEditScan = 1000
)

var (
	ErrEditExists   = fmt.Errorf("edit version %w", apperr.ErrAlreadyExists)
	ErrEditNotFound = fmt.Errorf("edit %w", apperr.ErrNotFound)
)

func EditKeyOf(e domain.Edit) MsgKey {
	return MsgKey{Room: e.Room, Thread: e.Thread, Seq: e.Seq}
}

func ValidateEdit(e domain.Edit) error {
	if err := EditKeyOf(e).Validate(); err != nil {
		return err
	}
	switch {
	case e.Version == 0 || e.Version > math.MaxInt32:
		return invalid("version")
	case e.Kind != domain.EditText && e.Kind != domain.EditDelete:
		return invalid("edit kind")
	case e.Prev != "" && (e.Kind != domain.EditText || e.Version != 1):
		return invalid("prev")
	default:
		return nil
	}
}

func ValidateLimit(limit, maxLimit int) error {
	if limit < 1 || limit > maxLimit {
		return invalid("limit")
	}
	return nil
}
```

`apps/core/internal/store/ports.go`, thay toàn bộ:

```go
package store

import (
	"context"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

type Messages interface {
	Insert(ctx context.Context, msgs []domain.Message) []Result
	Last(ctx context.Context, room, thread uint64) (uint64, error)
	Page(ctx context.Context, q PageQuery) ([]domain.Message, error)
	Find(ctx context.Context, room uint64, keys []MsgKey) ([]domain.Message, error)
}

type Rooms interface {
	Create(ctx context.Context, r domain.Room, members []domain.Member) error
	Get(ctx context.Context, id uint64) (domain.Room, error)
	Member(ctx context.Context, room uint64, user string) (domain.Member, error)
	TouchActivity(ctx context.Context, acts []Activity) error
	ActiveRooms(ctx context.Context, q ActiveQuery) ([]domain.Room, error)
}

type MessageEditor interface {
	ApplyEdit(ctx context.Context, e domain.Edit) error
}

type HistoryClearer interface {
	ClearHistory(ctx context.Context, room uint64, user string, seq uint64) (uint64, error)
}

type Edits interface {
	Append(ctx context.Context, e domain.Edit) error
	At(ctx context.Context, key MsgKey, version uint32) (domain.Edit, error)
	Latest(ctx context.Context, key MsgKey) (domain.Edit, bool, error)
	History(ctx context.Context, key MsgKey, after uint32, limit int) ([]domain.Edit, error)
	Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Edit, error)
	PurgeText(ctx context.Context, key MsgKey, upTo uint32) error
}

type Hidden interface {
	Hide(ctx context.Context, user string, key MsgKey) error
	HiddenIn(ctx context.Context, user string, room, thread, from, to uint64) ([]uint64, error)
}
```

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/"`
Expected: PASS.

**Step 4: Test contract + memstore**

`apps/core/internal/store/storetest/fixtures.go`, thay:

```go
func sameMessage(a, b domain.Message) bool {
	at, bt := a.CreatedAt, b.CreatedAt
	a.CreatedAt, b.CreatedAt = time.Time{}, time.Time{}
	return a == b && at.Equal(bt)
}
```

bằng:

```go
func sameMessage(a, b domain.Message) bool {
	at, bt, ae, be := a.CreatedAt, b.CreatedAt, a.EditedAt, b.EditedAt
	a.CreatedAt, b.CreatedAt, a.EditedAt, b.EditedAt = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	return a == b && at.Equal(bt) && ae.Equal(be)
}
```

`apps/core/internal/store/storetest/edit_cases.go`:

```go
package storetest

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type EditableMessages interface {
	store.Messages
	store.MessageEditor
}

type ClearableRooms interface {
	store.Rooms
	store.HistoryClearer
}

type editStores struct {
	msgs   EditableMessages
	rooms  ClearableRooms
	edits  store.Edits
	hidden store.Hidden
}

type editCase struct {
	name string
	run  func(t *testing.T, s editStores)
}

func RunEdits(t *testing.T, open func(t *testing.T) (EditableMessages, ClearableRooms, store.Edits, store.Hidden)) {
	t.Helper()
	for _, c := range slices.Concat(factCases(), applyCases(), viewerCases()) {
		t.Run(c.name, func(t *testing.T) {
			msgs, rooms, edits, hidden := open(t)
			c.run(t, editStores{msgs: msgs, rooms: rooms, edits: edits, hidden: hidden})
		})
	}
}

func fact(room, thread, seq uint64, version uint32) domain.Edit {
	e := domain.Edit{
		Room: room, Thread: thread, Seq: seq, Version: version, Kind: domain.EditText, Tenant: tenant, By: "alice",
		Text: fmt.Sprintf("edit %d/%d/%d v%d", room, thread, seq, version),
		At:   baseTime.Add(time.Duration(version) * time.Second),
	}
	if version == 1 {
		e.Prev = msg(room, thread, seq).Text
	}
	return e
}

func deletion(room, thread, seq uint64, version uint32) domain.Edit {
	e := fact(room, thread, seq, version)
	e.Kind, e.Text, e.Prev = domain.EditDelete, "", ""
	return e
}

func msgKey(room, thread, seq uint64) store.MsgKey {
	return store.MsgKey{Room: room, Thread: thread, Seq: seq}
}

func mustAppend(t *testing.T, s store.Edits, facts ...domain.Edit) {
	t.Helper()
	for _, e := range facts {
		if err := s.Append(t.Context(), e); err != nil {
			t.Fatalf("Append(%d/%d/%d v%d): %v", e.Room, e.Thread, e.Seq, e.Version, err)
		}
	}
}

func sameEdit(a, b domain.Edit) bool {
	at, bt := a.At, b.At
	a.At, b.At = time.Time{}, time.Time{}
	return a == b && at.Equal(bt)
}

func assertEdits(t *testing.T, op string, got, want []domain.Edit) {
	t.Helper()
	if !slices.EqualFunc(got, want, sameEdit) {
		t.Fatalf("%s = %+v,\nwant %+v", op, got, want)
	}
}

func assertEditAt(t *testing.T, s store.Edits, want domain.Edit) {
	t.Helper()
	got, err := s.At(t.Context(), store.EditKeyOf(want), want.Version)
	if err != nil {
		t.Fatalf("At(%d/%d/%d v%d): %v", want.Room, want.Thread, want.Seq, want.Version, err)
	}
	assertEdits(t, "At", []domain.Edit{got}, []domain.Edit{want})
}
```

`apps/core/internal/store/storetest/fact_cases.go`:

```go
package storetest

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func factCases() []editCase {
	return []editCase{
		{"append then read each fact back by version", factsAppendAt},
		{"append of an existing version fails and keeps the first fact", factsAppendExisting},
		{"latest is the highest version of that message only", factsLatest},
		{"history ascends after a version up to the limit", factsHistory},
		{"between returns the room facts in a time range by time then key", factsBetween},
		{"purge text clears text and prev up to a version of that message only", factsPurge},
		{"invalid facts and limits are rejected", factsInvalid},
	}
}

func factsAppendAt(t *testing.T, s editStores) {
	first, second := fact(roomA, mainThread, 1, 1), deletion(roomA, mainThread, 1, 2)
	mustAppend(t, s.edits, first, second)
	assertEditAt(t, s.edits, first)
	assertEditAt(t, s.edits, second)
	_, err := s.edits.At(t.Context(), store.EditKeyOf(first), 3)
	assertErrorIs(t, "At(missing version)", err, store.ErrEditNotFound)
	_, err = s.edits.At(t.Context(), msgKey(roomA, mainThread, 2), 1)
	assertErrorIs(t, "At(other message)", err, apperr.ErrNotFound)
}

func factsAppendExisting(t *testing.T, s editStores) {
	first := fact(roomA, mainThread, 1, 1)
	mustAppend(t, s.edits, first)
	rival := first
	rival.By, rival.Text = "bob", "rival"
	err := s.edits.Append(t.Context(), rival)
	assertErrorIs(t, "Append(existing version)", err, store.ErrEditExists)
	assertErrorIs(t, "Append(existing version)", err, apperr.ErrAlreadyExists)
	assertEditAt(t, s.edits, first)
}

func factsLatest(t *testing.T, s editStores) {
	key := msgKey(roomA, mainThread, 2)
	if _, ok, err := s.edits.Latest(t.Context(), key); ok || err != nil {
		t.Fatalf("Latest(no facts) = %v, %v; want false, nil", ok, err)
	}
	mustAppend(t, s.edits,
		fact(roomA, mainThread, 2, 1), fact(roomA, mainThread, 2, 3), fact(roomA, mainThread, 2, 2),
		fact(roomA, mainThread, 1, 9), fact(roomA, mainThread, 3, 7), fact(roomA, sideThread, 2, 8), fact(roomB, mainThread, 2, 6),
	)
	got, ok, err := s.edits.Latest(t.Context(), key)
	if err != nil || !ok {
		t.Fatalf("Latest = %v, %v; want a fact", ok, err)
	}
	assertEdits(t, "Latest", []domain.Edit{got}, []domain.Edit{fact(roomA, mainThread, 2, 3)})
}

func factsHistory(t *testing.T, s editStores) {
	all := make([]domain.Edit, 0, 5)
	for v := uint32(1); v <= 5; v++ {
		all = append(all, fact(roomA, mainThread, 1, v))
	}
	mustAppend(t, s.edits, all...)
	mustAppend(t, s.edits, fact(roomA, mainThread, 2, 1))
	cases := []struct {
		after uint32
		limit int
		want  []domain.Edit
	}{
		{0, store.MaxEditPage, all},
		{2, 2, all[2:4]},
		{4, 10, all[4:]},
		{5, 10, nil},
		{math.MaxUint32, 1, nil},
	}
	for _, c := range cases {
		got, err := s.edits.History(t.Context(), msgKey(roomA, mainThread, 1), c.after, c.limit)
		if err != nil {
			t.Fatalf("History(after %d, limit %d): %v", c.after, c.limit, err)
		}
		assertEdits(t, fmt.Sprintf("History(after %d, limit %d)", c.after, c.limit), got, c.want)
	}
}

func factsBetween(t *testing.T, s editStores) {
	from, to := baseTime.Add(time.Second), baseTime.Add(3*time.Second)
	atFrom := fact(roomA, mainThread, 5, 1)
	before := fact(roomA, mainThread, 4, 1)
	before.At = baseTime
	lowKey, highKey := fact(roomA, mainThread, 2, 2), fact(roomA, mainThread, 3, 2)
	atTo, after, other := fact(roomA, sideThread, 1, 3), fact(roomA, mainThread, 1, 4), fact(roomB, mainThread, 1, 2)
	mustAppend(t, s.edits, after, highKey, other, atTo, before, lowKey, atFrom)
	want := []domain.Edit{atFrom, lowKey, highKey, atTo}
	cases := []struct {
		room     uint64
		from, to time.Time
		limit    int
		want     []domain.Edit
	}{
		{roomA, from, to, store.MaxEditScan, want},
		{roomA, from, to, 2, want[:2]},
		{roomB, from, to, 10, []domain.Edit{other}},
		{roomA, to.Add(time.Hour), to.Add(2 * time.Hour), 10, nil},
	}
	for _, c := range cases {
		got, err := s.edits.Between(t.Context(), c.room, c.from, c.to, c.limit)
		if err != nil {
			t.Fatalf("Between(%d, %v, %v, %d): %v", c.room, c.from, c.to, c.limit, err)
		}
		assertEdits(t, fmt.Sprintf("Between(%d, limit %d)", c.room, c.limit), got, c.want)
	}
}

func factsPurge(t *testing.T, s editStores) {
	v1, v2, v3 := fact(roomA, mainThread, 1, 1), fact(roomA, mainThread, 1, 2), deletion(roomA, mainThread, 1, 3)
	neighbour := fact(roomA, mainThread, 2, 1)
	mustAppend(t, s.edits, v1, v2, v3, neighbour)
	key := store.EditKeyOf(v3)
	if err := s.edits.PurgeText(t.Context(), key, 2); err != nil {
		t.Fatalf("PurgeText(up to 2): %v", err)
	}
	if err := s.edits.PurgeText(t.Context(), key, 0); err != nil {
		t.Fatalf("PurgeText(up to 0): %v", err)
	}
	v1.Text, v1.Prev, v2.Text = "", "", ""
	for _, want := range []domain.Edit{v1, v2, v3, neighbour} {
		assertEditAt(t, s.edits, want)
	}
}

func factsInvalid(t *testing.T, s editStores) {
	for name, mutate := range map[string]func(*domain.Edit){
		"zero room":               func(e *domain.Edit) { e.Room = 0 },
		"zero seq":                func(e *domain.Edit) { e.Seq = 0 },
		"zero version":            func(e *domain.Edit) { e.Version = 0 },
		"version above max int32": func(e *domain.Edit) { e.Version = math.MaxInt32 + 1 },
		"zero kind":               func(e *domain.Edit) { e.Kind = 0 },
		"prev on a delete fact":   func(e *domain.Edit) { e.Kind, e.Text = domain.EditDelete, "" },
		"prev after version 1":    func(e *domain.Edit) { e.Version = 2 },
	} {
		e := fact(roomA, mainThread, 1, 1)
		mutate(&e)
		assertErrorIs(t, "Append("+name+")", s.edits.Append(t.Context(), e), apperr.ErrInvalidArgument)
		assertErrorIs(t, "ApplyEdit("+name+")", s.msgs.ApplyEdit(t.Context(), e), apperr.ErrInvalidArgument)
	}
	key := msgKey(roomA, mainThread, 1)
	for _, limit := range []int{-1, 0, store.MaxEditPage + 1} {
		_, err := s.edits.History(t.Context(), key, 0, limit)
		assertErrorIs(t, fmt.Sprintf("History(limit %d)", limit), err, apperr.ErrInvalidArgument)
	}
	for _, limit := range []int{0, store.MaxEditScan + 1} {
		_, err := s.edits.Between(t.Context(), roomA, baseTime, baseTime.Add(time.Hour), limit)
		assertErrorIs(t, fmt.Sprintf("Between(limit %d)", limit), err, apperr.ErrInvalidArgument)
	}
	if _, ok, err := s.edits.Latest(t.Context(), key); ok || err != nil {
		t.Fatalf("Latest after invalid appends = %v, %v; want nothing stored", ok, err)
	}
}
```

`apps/core/internal/store/storetest/apply_cases.go`:

```go
package storetest

import (
	"context"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func applyCases() []editCase {
	return []editCase{
		{"apply edit sets text, version and edited time on that message only", applyEdit},
		{"apply delete clears the text and marks the message deleted", applyDelete},
		{"apply at or below the stored version changes nothing", applyStale},
		{"apply on a missing message creates nothing", applyMissing},
		{"the view-only hidden flag is never stored", applyHiddenNeverStored},
		{"cancelled context writes nothing", editsCancelled},
	}
}

func edited(m domain.Message, e domain.Edit) domain.Message {
	m.Version, m.EditedAt, m.Text, m.Deleted = e.Version, e.At, e.Text, e.Kind == domain.EditDelete
	return m
}

func mustApply(t *testing.T, s store.MessageEditor, facts ...domain.Edit) {
	t.Helper()
	for _, e := range facts {
		if err := s.ApplyEdit(t.Context(), e); err != nil {
			t.Fatalf("ApplyEdit(%d/%d/%d v%d): %v", e.Room, e.Thread, e.Seq, e.Version, err)
		}
	}
}

func assertStored(t *testing.T, s store.Messages, want ...domain.Message) {
	t.Helper()
	got, err := s.Find(t.Context(), want[0].Room, keysOf(want))
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	assertMessages(t, got, want)
}

func applyEdit(t *testing.T, s editStores) {
	m, other := msg(roomA, mainThread, 1), msg(roomA, mainThread, 2)
	mustInsert(t, s.msgs, []domain.Message{m, other})
	first, second := fact(roomA, mainThread, 1, 1), fact(roomA, mainThread, 1, 2)
	mustApply(t, s.msgs, first)
	assertStored(t, s.msgs, edited(m, first), other)
	mustApply(t, s.msgs, second)
	assertStored(t, s.msgs, edited(m, second), other)
	page, err := s.msgs.Page(t.Context(), store.PageQuery{Room: roomA, Thread: mainThread, Anchor: store.Latest, Limit: 10})
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	assertMessages(t, page, []domain.Message{edited(m, second), other})
	if last, err := s.msgs.Last(t.Context(), roomA, mainThread); err != nil || last != 2 {
		t.Fatalf("Last = %d, %v; want 2, edits never move the timeline", last, err)
	}
}

func applyDelete(t *testing.T, s editStores) {
	m := msg(roomA, mainThread, 1)
	mustInsert(t, s.msgs, []domain.Message{m})
	gone := deletion(roomA, mainThread, 1, 2)
	mustApply(t, s.msgs, fact(roomA, mainThread, 1, 1), gone)
	want := edited(m, gone)
	if want.Text != "" || !want.Deleted || want.Version != 2 {
		t.Fatalf("fixture %+v must be an empty deleted message at version 2", want)
	}
	assertStored(t, s.msgs, want)
}

func applyStale(t *testing.T, s editStores) {
	m := msg(roomA, mainThread, 1)
	mustInsert(t, s.msgs, []domain.Message{m})
	second := fact(roomA, mainThread, 1, 2)
	mustApply(t, s.msgs, second, fact(roomA, mainThread, 1, 1), deletion(roomA, mainThread, 1, 2))
	assertStored(t, s.msgs, edited(m, second))
}

func applyMissing(t *testing.T, s editStores) {
	e := fact(roomA, mainThread, 1, 1)
	if err := s.msgs.ApplyEdit(t.Context(), e); err != nil {
		t.Fatalf("ApplyEdit(missing message) = %v, want nil", err)
	}
	got, err := s.msgs.Find(t.Context(), roomA, []store.MsgKey{store.EditKeyOf(e)})
	if err != nil || len(got) != 0 {
		t.Fatalf("Find after ApplyEdit on a missing message = %+v, %v; want nothing", got, err)
	}
	m := msg(roomA, mainThread, 1)
	mustInsert(t, s.msgs, []domain.Message{m})
	assertStored(t, s.msgs, m)
}

func applyHiddenNeverStored(t *testing.T, s editStores) {
	shown := msg(roomA, mainThread, 1)
	hidden := shown
	hidden.Hidden = true
	mustInsert(t, s.msgs, []domain.Message{hidden})
	assertStored(t, s.msgs, shown)
	e := fact(roomA, mainThread, 1, 1)
	mustApply(t, s.msgs, e)
	assertStored(t, s.msgs, edited(shown, e))
}

func editsCancelled(t *testing.T, s editStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	m := msg(roomA, mainThread, 1)
	mustInsert(t, s.msgs, []domain.Message{m})
	ctx := cancelledContext(t)
	e := fact(roomA, mainThread, 1, 1)
	assertErrorIs(t, "Append", s.edits.Append(ctx, e), context.Canceled)
	_, _, err := s.edits.Latest(ctx, store.EditKeyOf(e))
	assertErrorIs(t, "Latest", err, context.Canceled)
	assertErrorIs(t, "ApplyEdit", s.msgs.ApplyEdit(ctx, e), context.Canceled)
	assertErrorIs(t, "Hide", s.hidden.Hide(ctx, "bob", store.KeyOf(m)), context.Canceled)
	_, err = s.rooms.ClearHistory(ctx, roomA, "alice", 1)
	assertErrorIs(t, "ClearHistory", err, context.Canceled)
	if _, ok, err := s.edits.Latest(t.Context(), store.EditKeyOf(e)); ok || err != nil {
		t.Fatalf("Latest after a cancelled Append = %v, %v; want nothing stored", ok, err)
	}
	assertStored(t, s.msgs, m)
	assertMember(t, s.rooms, members[0])
}
```

`apps/core/internal/store/storetest/viewer_cases.go`:

```go
package storetest

import (
	"math"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func viewerCases() []editCase {
	return []editCase{
		{"hide is idempotent and per user and timeline", hidePerViewer},
		{"clear history only moves forward and is per member", clearForward},
		{"clear history of a non member fails and adds nobody", clearNotMember},
	}
}

func hidePerViewer(t *testing.T, s editStores) {
	hide := func(user string, k store.MsgKey) {
		t.Helper()
		if err := s.hidden.Hide(t.Context(), user, k); err != nil {
			t.Fatalf("Hide(%q, %+v): %v", user, k, err)
		}
	}
	hide("bob", msgKey(roomA, mainThread, 7))
	hide("bob", msgKey(roomA, mainThread, 3))
	hide("bob", msgKey(roomA, mainThread, 3))
	hide("bob", msgKey(roomA, sideThread, 4))
	hide("bob", msgKey(roomB, mainThread, 5))
	hide("carol", msgKey(roomA, mainThread, 5))
	cases := []struct {
		user                   string
		room, thread, from, to uint64
		want                   []uint64
	}{
		{"bob", roomA, mainThread, 1, 10, []uint64{3, 7}},
		{"bob", roomA, mainThread, 4, 7, []uint64{7}},
		{"bob", roomA, mainThread, 8, 10, nil},
		{"bob", roomA, mainThread, 7, 3, nil},
		{"bob", roomA, sideThread, 1, 10, []uint64{4}},
		{"bob", roomB, mainThread, 1, math.MaxUint64, []uint64{5}},
		{"carol", roomA, mainThread, 1, 10, []uint64{5}},
		{"dave", roomA, mainThread, 1, 10, nil},
	}
	for _, c := range cases {
		got, err := s.hidden.HiddenIn(t.Context(), c.user, c.room, c.thread, c.from, c.to)
		if err != nil || !slices.Equal(got, c.want) {
			t.Fatalf("HiddenIn(%q, %d, %d, %d..%d) = %v, %v; want %v", c.user, c.room, c.thread, c.from, c.to, got, err, c.want)
		}
	}
	assertErrorIs(t, "Hide(seq 0)", s.hidden.Hide(t.Context(), "bob", msgKey(roomA, mainThread, 0)), apperr.ErrInvalidArgument)
}

func clearForward(t *testing.T, s editStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	for _, step := range []struct{ seq, want uint64 }{{5, 5}, {3, 5}, {9, 9}, {0, 9}} {
		got, err := s.rooms.ClearHistory(t.Context(), roomA, "alice", step.seq)
		if err != nil || got != step.want {
			t.Fatalf("ClearHistory(alice, %d) = %d, %v; want %d", step.seq, got, err, step.want)
		}
	}
	alice := members[0]
	alice.ClearedBeforeSeq = 9
	assertMember(t, s.rooms, alice)
	assertMember(t, s.rooms, members[1])
}

func clearNotMember(t *testing.T, s editStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	_, err := s.rooms.ClearHistory(t.Context(), roomA, "carol", 1)
	assertErrorIs(t, "ClearHistory(non member)", err, domain.ErrNotMember)
	_, err = s.rooms.ClearHistory(t.Context(), roomB, "alice", 1)
	assertErrorIs(t, "ClearHistory(missing room)", err, domain.ErrNotMember)
	assertNotMember(t, s.rooms, roomA, "carol")
	assertNotMember(t, s.rooms, roomB, "alice")
}
```

`apps/core/internal/store/memstore/memstore_test.go`, thêm vào cuối:

```go
func TestEditsContract(t *testing.T) {
	storetest.RunEdits(t, func(*testing.T) (storetest.EditableMessages, storetest.ClearableRooms, store.Edits, store.Hidden) {
		return memstore.NewMessages(), memstore.NewRooms(), memstore.NewEdits(), memstore.NewHidden()
	})
}
```

`apps/core/internal/store/storetest/activity_cases.go`:
- trong `activityCases`, sau dòng `{"thread activity only bumps the change time and bucket", activityThread},` thêm `{"an activity with seq 0 bumps only the last change", activityWithoutSeq},`.
- thêm vào cuối file:

```go
func activityWithoutSeq(t *testing.T, s store.Rooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	editAt := baseTime.Add(5 * time.Hour)
	mustTouch(t, s, store.Activity{Room: roomA, Seq: 2, At: baseTime})
	mustTouch(t, s, store.Activity{Room: roomA, At: editAt})
	assertActivity(t, s, roomA, 2, baseTime, editAt)
	if got := activeIDs(t, s, store.ActiveQuery{From: editAt, To: editAt, Limit: 10}); !slices.Equal(got, []uint64{roomA}) {
		t.Fatalf("active rooms in the edit's hour = %v, want [%d]", got, roomA)
	}
}
```

**Step 5: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/..."`
Expected: FAIL biên dịch ở `memstore_test.go`: `undefined: memstore.NewEdits`, `undefined: memstore.NewHidden`, `*memstore.Messages does not implement storetest.EditableMessages (missing method ApplyEdit)`, `*memstore.Rooms does not implement storetest.ClearableRooms (missing method ClearHistory)`. Package `storetest` tự nó biên dịch được. (Case `an activity with seq 0 ...` sẽ fail trên memstore với `want seq 2, msg <baseTime>` sau khi biên dịch được; Step 6 sửa.)

**Step 6: Code memstore**

`apps/core/internal/store/memstore/apply_edit.go`:

```go
package memstore

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.MessageEditor = (*Messages)(nil)

func (s *Messages) ApplyEdit(ctx context.Context, e domain.Edit) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ValidateEdit(e); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	line := s.lines[timeline{e.Room, e.Thread}]
	i, found := slices.BinarySearchFunc(line, e.Seq, bySeq)
	if !found || line[i].Version >= e.Version {
		return nil
	}
	line[i] = projected(line[i], e)
	return nil
}

func projected(m domain.Message, e domain.Edit) domain.Message {
	m.Version, m.EditedAt, m.Text, m.Deleted = e.Version, e.At, e.Text, false
	if e.Kind == domain.EditDelete {
		m.Text, m.Deleted = "", true
	}
	return m
}
```

`apps/core/internal/store/memstore/memstore.go`, trong `insertLocked` thay:

```go
	s.lines[tl] = slices.Insert(line, i, m)
```

bằng:

```go
	m.Hidden = false
	s.lines[tl] = slices.Insert(line, i, m)
```

`apps/core/internal/store/memstore/edits.go`:

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

var _ store.Edits = (*Edits)(nil)

type Edits struct {
	mu    sync.RWMutex
	facts map[store.MsgKey][]domain.Edit
}

func NewEdits() *Edits { return &Edits{facts: make(map[store.MsgKey][]domain.Edit)} }

func (s *Edits) Append(ctx context.Context, e domain.Edit) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ValidateEdit(e); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := store.EditKeyOf(e)
	line := s.facts[key]
	i, found := slices.BinarySearchFunc(line, e.Version, byVersion)
	if found {
		return fmt.Errorf("append edit v%d of %+v: %w", e.Version, key, store.ErrEditExists)
	}
	s.facts[key] = slices.Insert(line, i, e)
	return nil
}

func (s *Edits) At(ctx context.Context, key store.MsgKey, version uint32) (domain.Edit, error) {
	if err := ctx.Err(); err != nil {
		return domain.Edit{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	line := s.facts[key]
	if i, ok := slices.BinarySearchFunc(line, version, byVersion); ok {
		return line[i], nil
	}
	return domain.Edit{}, fmt.Errorf("edit v%d of %+v: %w", version, key, store.ErrEditNotFound)
}

func (s *Edits) Latest(ctx context.Context, key store.MsgKey) (domain.Edit, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.Edit{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	line := s.facts[key]
	if len(line) == 0 {
		return domain.Edit{}, false, nil
	}
	return line[len(line)-1], true, nil
}

func (s *Edits) History(ctx context.Context, key store.MsgKey, after uint32, limit int) ([]domain.Edit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateLimit(limit, store.MaxEditPage); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	line := s.facts[key]
	i, found := slices.BinarySearchFunc(line, after, byVersion)
	if found {
		i++
	}
	return slices.Clone(line[i:min(len(line), i+limit)]), nil
}

func (s *Edits) Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Edit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateLimit(limit, store.MaxEditScan); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.Edit{}
	for key, line := range s.facts {
		if key.Room != room {
			continue
		}
		for _, e := range line {
			if !e.At.Before(from) && !e.At.After(to) {
				out = append(out, e)
			}
		}
	}
	slices.SortFunc(out, byTimeThenKey)
	return out[:min(len(out), limit)], nil
}

func (s *Edits) PurgeText(ctx context.Context, key store.MsgKey, upTo uint32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	line := s.facts[key]
	for i := range line {
		if line[i].Version <= upTo {
			line[i].Text, line[i].Prev = "", ""
		}
	}
	return nil
}

func byVersion(e domain.Edit, v uint32) int { return cmp.Compare(e.Version, v) }

func byTimeThenKey(a, b domain.Edit) int {
	return cmp.Or(a.At.Compare(b.At), cmp.Compare(a.Thread, b.Thread), cmp.Compare(a.Seq, b.Seq), cmp.Compare(a.Version, b.Version))
}
```

`apps/core/internal/store/memstore/hidden.go`:

```go
package memstore

import (
	"context"
	"slices"
	"sync"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.Hidden = (*Hidden)(nil)

type hiddenKey struct {
	user string
	key  store.MsgKey
}

type Hidden struct {
	mu   sync.RWMutex
	seqs map[hiddenKey]struct{}
}

func NewHidden() *Hidden { return &Hidden{seqs: make(map[hiddenKey]struct{})} }

func (s *Hidden) Hide(ctx context.Context, user string, key store.MsgKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := key.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seqs[hiddenKey{user: user, key: key}] = struct{}{}
	return nil
}

func (s *Hidden) HiddenIn(ctx context.Context, user string, room, thread, from, to uint64) ([]uint64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []uint64
	for k := range s.seqs {
		if k.user == user && k.key.Room == room && k.key.Thread == thread && k.key.Seq >= from && k.key.Seq <= to {
			out = append(out, k.key.Seq)
		}
	}
	slices.Sort(out)
	return out, nil
}
```

`apps/core/internal/store/memstore/rooms.go`:
- thay `var _ store.Rooms = (*Rooms)(nil)` bằng:

```go
var (
	_ store.Rooms          = (*Rooms)(nil)
	_ store.HistoryClearer = (*Rooms)(nil)
)
```

- thêm vào cuối file:

```go
func (s *Rooms) ClearHistory(ctx context.Context, room uint64, user string, seq uint64) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := memberKey{room, user}
	m, ok := s.members[k]
	if !ok {
		return 0, domain.ErrNotMember
	}
	m.ClearedBeforeSeq = max(m.ClearedBeforeSeq, seq)
	s.members[k] = m
	return m.ClearedBeforeSeq, nil
}
```

`apps/core/internal/store/memstore/room_activity.go`, trong `withActivity` thay:

```go
	if a.Thread == 0 {
```

bằng:

```go
	if a.Thread == 0 && a.Seq > 0 {
```

**Step 7: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/store/..."` rồi `make vet`
Expected: PASS (mongostore integration skip); vet sạch: `mongostore.Store` và mọi fake của `store.Messages`/`store.Rooms` biên dịch không đổi. `-count=5` vì có lock mới. `wc -l apps/core/internal/store/memstore/*.go apps/core/internal/store/storetest/*.go apps/core/internal/store/*.go` mỗi file < 200.

**Step 8: Commit**

INDEXES.csv:
- dòng `apps/core/internal/store`: thay `"Storage ports Messages/Rooms and the database-neutral change feed port` bằng `"Storage ports Messages/Rooms; edit ports MessageEditor (ApplyEdit: CAS on the message version) and HistoryClearer (ClearHistory: $max cleared-before seq on the member), Edits (Append insert-unique by room|thread|seq|version -> ErrEditExists; At -> ErrEditNotFound; Latest; History after a version up to MaxEditPage; Between room + time range by ts then key up to MaxEditScan; PurgeText), Hidden (Hide upsert, HiddenIn ascending seqs); EditKeyOf, ValidateEdit (version 1..MaxInt32, text or delete kind), ValidateLimit; and the database-neutral change feed port`; key symbols thay `Messages;Rooms;ChangeFeed;` bằng `Messages;Rooms;MessageEditor;HistoryClearer;Edits;Hidden;ErrEditExists;ErrEditNotFound;MaxEditPage;MaxEditScan;EditKeyOf;ValidateEdit;ValidateLimit;ChangeFeed;`; cột decisions thay `D10;D30;D52;D69` bằng `D10;D30;D52;D62;D69;D72`. Cũng trong dòng này thay `room activity port TouchActivity ($max, never back)` bằng `room activity port TouchActivity ($max, never back; last seq and message time only for main-thread activity with seq > 0, so an edit activity with seq 0 bumps only the last change)`.
- dòng `apps/core/internal/store/memstore`: thay `"In-memory Messages/Rooms adapter used by unit tests;` bằng `"In-memory Messages/Rooms adapter used by unit tests (Messages.ApplyEdit projects a fact only above the stored version; Rooms.ClearHistory takes the max; the view-only Hidden flag is dropped on insert; TouchActivity bumps last seq/msg time only for main-thread activity with seq > 0), Edits (facts per message sorted by version) and Hidden (set per user);`; key symbols thay `NewMessages;NewRooms;` bằng `NewMessages;NewRooms;NewEdits;NewHidden;Edits;Hidden;`.
- dòng `apps/core/internal/store/storetest`: thay `"Contract suites every store adapter must pass: Messages/Rooms (Run)` bằng `"Contract suites every store adapter must pass: Messages/Rooms (Run); edits (RunEdits over EditableMessages + ClearableRooms + Edits + Hidden: append/at/latest/history/between/purge of facts, invalid facts and limits, ApplyEdit CAS by version and delete, hidden flag never stored, hide per user and timeline, clear history $max and non member, cancelled context); room activity case: an activity with seq 0 bumps only the last change`; key symbols thay `Run;RunFeed` bằng `Run;RunFeed;RunEdits;EditableMessages;ClearableRooms`.

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/store/edit.go apps/core/internal/store/edit_test.go apps/core/internal/store/memstore/edits.go apps/core/internal/store/memstore/hidden.go apps/core/internal/store/memstore/apply_edit.go apps/core/internal/store/storetest/edit_cases.go apps/core/internal/store/storetest/fact_cases.go apps/core/internal/store/storetest/apply_cases.go apps/core/internal/store/storetest/viewer_cases.go
git commit -m "feat(store): add edit, hidden and clear history ports with memstore and contract" -- apps/core/internal/store/ INDEXES.csv
```

---
### Task 4: Mongostore — `message_edits`, `hidden`, codec `v/d/ea/cb`, bốn port mới

Rủi ro cao: đụng `Bootstrap`, codec của `messages`/`members` và thêm write CAS lên `messages`. Một reviewer.

- **Bootstrap:** `message_edits` là collection clustered zstd giống `messages`. `ensureMessages` thành `ensureClustered(name)` dùng cho cả hai; `ErrNotClustered` đổi text thành chung, sentinel giữ nguyên. Index `{r:1, ts:1}` (D70). `hidden` là collection thường, index unique `{u:1, r:1, th:1, s:1}`. `messages` vẫn không có index phụ.
- **Codec:** `messageDoc` thêm `v` (int32), `d` (bool), `ea` (date), đều `omitempty`. Tin chưa sửa vẫn đúng 7 field, nên `TestMessageCodecRoundTrip` không đổi. `encodeMessage` ghi các field này từ `Message` (Insert luôn có giá trị zero); `Hidden` không có field nào. `memberDoc` thêm `cb` (int64, `omitempty`); `encodeMember` không ghi `cb`. `editDoc` là `{_id: 28B, r, t, k, by, x?, p?, ts}`.
- **Edits:** `Append` = `InsertOne`, trùng khoá → `ErrEditExists`. `At` = `FindOne` theo `_id`. `Latest`/`History` là range scan trên `_id` clustered trong `[keys.Edit(..., after) , keys.Edit(..., MaxUint32)]`. `Between` = `{r, ts ∈ [from, to]}` sort `{ts:1, _id:1}`. `PurgeText` = `UpdateMany` `$unset x, p` cho `_id ∈ [v1, upTo]`.
- **ApplyEdit:** `UpdateOne` trên `messages` với filter `{_id, $or: [{v: {$exists: false}}, {v: {$lt: ver}}]}`, `$set {v, ea, x, d}`, không upsert. Không khớp thì `nil`.
- **Hidden:** `InsertOne`, trùng khoá unique → `nil` (upsert kiểu insert-or-ignore, không cần `$setOnInsert`). `HiddenIn` là query covered trên index (projection `{s:1, _id:0}`). `to` được kẹp về `MaxInt64`.
- **ClearHistory:** `FindOneAndUpdate` `{r, u}` `$max {cb}` trả doc sau update; không có doc → `domain.ErrNotMember`.
- **Room activity** (theo Task 3): `activityFields` chỉ thêm `ls`/`lm` khi `Thread == 0 && Seq > 0`.
- Collection `edits`/`hidden` dùng option `primary` (read primary, read concern local, write concern của store, mặc định majority) giống `rooms`.

Tra API (đã kiểm bằng `go doc`): `func (coll *Collection) FindOneAndUpdate(ctx, filter any, update any, opts ...options.Lister[options.FindOneAndUpdateOptions]) *SingleResult`; `options.FindOneAndUpdate().SetReturnDocument(options.After)` (`options.ReturnDocument`, consts `Before`, `After`); `func (coll *Collection) UpdateMany(ctx, filter, update any, opts ...) (*UpdateResult, error)`; `mongo.IsDuplicateKeyError(err)` dùng được cho lỗi của `InsertOne`.

**Files:**
- Modify: `apps/core/internal/store/mongostore/mongostore.go` (const, struct `Store`, `New`, assertion)
- Modify: `apps/core/internal/store/mongostore/codec.go` (`messageDoc`, `memberDoc`, `encodeMessage`, `decodeMessage`, `decodeMember`)
- Create: `apps/core/internal/store/mongostore/edit_codec.go`
- Create: `apps/core/internal/store/mongostore/edit_codec_test.go`
- Modify: `apps/core/internal/store/mongostore/bootstrap.go` (`Bootstrap`, `ensureMessages` → `ensureClustered`, thêm `editIndexes`, `hiddenIndexes`)
- Modify: `apps/core/internal/store/mongostore/errors.go` (text `ErrNotClustered`)
- Create: `apps/core/internal/store/mongostore/edits.go`, `apps/core/internal/store/mongostore/apply_edit.go`, `apps/core/internal/store/mongostore/hidden.go`
- Modify: `apps/core/internal/store/mongostore/rooms.go` (`ClearHistory`)
- Modify: `apps/core/internal/store/mongostore/room_activity.go` (`activityFields`)
- Modify: `apps/core/internal/store/mongostore/bootstrap_integration_test.go` (`assertMessagesLayout` → `assertClusteredLayout`)
- Create: `apps/core/internal/store/mongostore/bootstrap_edits_integration_test.go`
- Create: `apps/core/internal/store/mongostore/edits_integration_test.go`
- Modify: `INDEXES.csv` (dòng `apps/core/internal/store/mongostore`)

**Step 1: Test codec (unit)**

`apps/core/internal/store/mongostore/edit_codec_test.go` (`codecTime`, `sampleMessage`, `roundTrip`, `fieldNames` có sẵn ở `codec_test.go`; không thêm vào `codec_test.go` vì file đó đã 191 dòng):

```go
package mongostore

import (
	"bytes"
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func sampleEdit() domain.Edit {
	return domain.Edit{
		Room: 7_340_000_001, Thread: 3, Seq: 42, Version: 1, Kind: domain.EditText, Tenant: "acme", By: "alice",
		Text: "đã sửa", Prev: "xin chào", At: codecTime.Add(time.Minute),
	}
}

func TestEditCodecRoundTrip(t *testing.T) {
	e := sampleEdit()
	doc, err := encodeEdit(e)
	if err != nil {
		t.Fatalf("encodeEdit: %v", err)
	}
	if !bytes.Equal(doc.ID, keys.Edit(e.Room, e.Thread, e.Seq, e.Version)) || doc.Room != 7_340_000_001 {
		t.Fatalf("_id = %x, r = %d; want keys.Edit and the room", doc.ID, doc.Room)
	}
	back, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "r", "t", "k", "by", "x", "p", "ts"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	got, err := decodeEdit(back)
	if err != nil || !got.At.Equal(e.At) {
		t.Fatalf("decodeEdit = %+v, %v; want %+v", got, err, e)
	}
	got.At = e.At
	if got != e {
		t.Fatalf("decoded %+v, want %+v", got, e)
	}
}

func TestDeleteFactStoresNoText(t *testing.T) {
	e := sampleEdit()
	e.Version, e.Kind, e.Text, e.Prev = 2, domain.EditDelete, "", ""
	doc, err := encodeEdit(e)
	if err != nil {
		t.Fatalf("encodeEdit: %v", err)
	}
	_, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "r", "t", "k", "by", "ts"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
}

func TestEncodeEditRejectsInvalid(t *testing.T) {
	for name, mutate := range map[string]func(*domain.Edit){
		"zero room":               func(e *domain.Edit) { e.Room = 0 },
		"zero seq":                func(e *domain.Edit) { e.Seq = 0 },
		"zero version":            func(e *domain.Edit) { e.Version = 0 },
		"version above max int32": func(e *domain.Edit) { e.Version = math.MaxInt32 + 1 },
		"zero kind":               func(e *domain.Edit) { e.Kind = 0 },
		"room above max int64":    func(e *domain.Edit) { e.Room = math.MaxInt64 + 1 },
		"seq above max int64":     func(e *domain.Edit) { e.Seq = math.MaxInt64 + 1 },
	} {
		e := sampleEdit()
		mutate(&e)
		if _, err := encodeEdit(e); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: encodeEdit = %v, want ErrInvalidArgument", name, err)
		}
	}
}

func TestDecodeEditRejectsACorruptID(t *testing.T) {
	doc, err := encodeEdit(sampleEdit())
	if err != nil {
		t.Fatalf("encodeEdit: %v", err)
	}
	doc.ID = doc.ID[:24]
	if _, err := decodeEdit(doc); !errors.Is(err, errCorrupt) {
		t.Fatalf("decodeEdit(24-byte id) = %v, want errCorrupt", err)
	}
}

func TestMessageCodecCarriesEditStateButNotTheHiddenFlag(t *testing.T) {
	m := sampleMessage()
	m.Text, m.Version, m.Deleted, m.EditedAt = "", 3, true, codecTime.Add(time.Hour)
	stored := m
	m.Hidden = true
	doc, err := encodeMessage(m)
	if err != nil {
		t.Fatalf("encodeMessage: %v", err)
	}
	back, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "t", "f", "k", "x", "c", "ts", "v", "d", "ea"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	got, err := decodeMessage(back)
	if err != nil || !got.CreatedAt.Equal(stored.CreatedAt) || !got.EditedAt.Equal(stored.EditedAt) {
		t.Fatalf("decodeMessage = %+v, %v; want %+v", got, err, stored)
	}
	got.CreatedAt, got.EditedAt = stored.CreatedAt, stored.EditedAt
	if got != stored {
		t.Fatalf("decoded %+v, want %+v", got, stored)
	}
}

func TestMessageCodecRejectsOutOfRangeVersions(t *testing.T) {
	m := sampleMessage()
	m.Version = math.MaxInt32 + 1
	if _, err := encodeMessage(m); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("encodeMessage(version above max int32) = %v, want ErrInvalidArgument", err)
	}
	doc, err := encodeMessage(sampleMessage())
	if err != nil {
		t.Fatalf("encodeMessage: %v", err)
	}
	doc.Version = -1
	if _, err := decodeMessage(doc); !errors.Is(err, errCorrupt) {
		t.Fatalf("decodeMessage(negative version) = %v, want errCorrupt", err)
	}
}

func TestMemberCodecReadsClearedBefore(t *testing.T) {
	doc := encodeMember(domain.Member{Room: 7_340_000_001, Tenant: "acme", User: "bob", Role: domain.RoleMember, JoinedAt: codecTime}, 7_340_000_001)
	doc.ClearedBefore = 42
	got, err := decodeMember(doc)
	if err != nil || got.ClearedBeforeSeq != 42 {
		t.Fatalf("decodeMember = %+v, %v; want cleared before 42", got, err)
	}
	doc.ClearedBefore = -1
	if _, err := decodeMember(doc); !errors.Is(err, errCorrupt) {
		t.Fatalf("decodeMember(negative cb) = %v, want errCorrupt", err)
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/mongostore/..."`
Expected: FAIL biên dịch: `undefined: encodeEdit`, `undefined: decodeEdit`, `doc.Version undefined (type messageDoc has no field or method Version)`, `doc.ClearedBefore undefined`.

**Step 3: Code codec**

`apps/core/internal/store/mongostore/edit_codec.go`:

```go
package mongostore

import (
	"fmt"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type editDoc struct {
	ID     []byte          `bson:"_id"`
	Room   int64           `bson:"r"`
	Tenant string          `bson:"t"`
	Kind   domain.EditKind `bson:"k"`
	By     string          `bson:"by"`
	Text   string          `bson:"x,omitempty"`
	Prev   string          `bson:"p,omitempty"`
	At     time.Time       `bson:"ts"`
}

func encodeEdit(e domain.Edit) (editDoc, error) {
	if err := store.ValidateEdit(e); err != nil {
		return editDoc{}, err
	}
	room, err := toInt64("room id", e.Room)
	if err != nil {
		return editDoc{}, err
	}
	if _, err := toInt64("seq", e.Seq); err != nil {
		return editDoc{}, err
	}
	return editDoc{
		ID:     keys.Edit(e.Room, e.Thread, e.Seq, e.Version),
		Room:   room,
		Tenant: e.Tenant,
		Kind:   e.Kind,
		By:     e.By,
		Text:   e.Text,
		Prev:   e.Prev,
		At:     e.At,
	}, nil
}

func decodeEdit(d editDoc) (domain.Edit, error) {
	room, thread, seq, version, err := keys.ParseEdit(d.ID)
	if err != nil {
		return domain.Edit{}, fmt.Errorf("%w: edit _id: %w", errCorrupt, err)
	}
	return domain.Edit{
		Room:    room,
		Thread:  thread,
		Seq:     seq,
		Version: version,
		Kind:    d.Kind,
		Tenant:  d.Tenant,
		By:      d.By,
		Text:    d.Text,
		Prev:    d.Prev,
		At:      d.At,
	}, nil
}

func decodeEdits(docs []editDoc) ([]domain.Edit, error) {
	out := make([]domain.Edit, len(docs))
	for i, d := range docs {
		e, err := decodeEdit(d)
		if err != nil {
			return nil, err
		}
		out[i] = e
	}
	return out, nil
}

func toInt32(field string, v uint32) (int32, error) {
	if v > math.MaxInt32 {
		return 0, fmt.Errorf("%w: %s above max int32", apperr.ErrInvalidArgument, field)
	}
	return int32(v), nil
}

func toUint32(field string, v int32) (uint32, error) {
	if v < 0 {
		return 0, fmt.Errorf("%w: negative %s", errCorrupt, field)
	}
	return uint32(v), nil
}
```

`apps/core/internal/store/mongostore/codec.go`:
- thay struct `messageDoc`:

```go
type messageDoc struct {
	ID        []byte      `bson:"_id"`
	Tenant    string      `bson:"t"`
	From      string      `bson:"f"`
	Kind      domain.Kind `bson:"k"`
	Text      string      `bson:"x"`
	CID       string      `bson:"c"`
	CreatedAt time.Time   `bson:"ts"`
}
```

bằng:

```go
type messageDoc struct {
	ID        []byte      `bson:"_id"`
	Tenant    string      `bson:"t"`
	From      string      `bson:"f"`
	Kind      domain.Kind `bson:"k"`
	Text      string      `bson:"x"`
	CID       string      `bson:"c"`
	CreatedAt time.Time   `bson:"ts"`
	Version   int32       `bson:"v,omitempty"`
	Deleted   bool        `bson:"d,omitempty"`
	EditedAt  time.Time   `bson:"ea,omitempty"`
}
```

- thay struct `memberDoc`:

```go
type memberDoc struct {
	Room     int64       `bson:"r"`
	User     string      `bson:"u"`
	Tenant   string      `bson:"t"`
	Role     domain.Role `bson:"ro"`
	JoinedAt time.Time   `bson:"ja"`
}
```

bằng:

```go
type memberDoc struct {
	Room          int64       `bson:"r"`
	User          string      `bson:"u"`
	Tenant        string      `bson:"t"`
	Role          domain.Role `bson:"ro"`
	JoinedAt      time.Time   `bson:"ja"`
	ClearedBefore int64       `bson:"cb,omitempty"`
}
```

- trong `encodeMessage` thay:

```go
	if _, err := toInt64("seq", m.Seq); err != nil {
		return messageDoc{}, err
	}
	return messageDoc{
```

bằng:

```go
	if _, err := toInt64("seq", m.Seq); err != nil {
		return messageDoc{}, err
	}
	version, err := toInt32("version", m.Version)
	if err != nil {
		return messageDoc{}, err
	}
	return messageDoc{
```

và thay (trong `encodeMessage`, chỗ đầu tiên):

```go
		CreatedAt: m.CreatedAt,
	}, nil
}
```

bằng:

```go
		CreatedAt: m.CreatedAt,
		Version:   version,
		Deleted:   m.Deleted,
		EditedAt:  m.EditedAt,
	}, nil
}
```

- trong `decodeMessage` thay:

```go
		return domain.Message{}, fmt.Errorf("%w: message _id: %w", errCorrupt, err)
	}
	return domain.Message{
```

bằng:

```go
		return domain.Message{}, fmt.Errorf("%w: message _id: %w", errCorrupt, err)
	}
	version, err := toUint32("message version", d.Version)
	if err != nil {
		return domain.Message{}, err
	}
	return domain.Message{
```

và thay:

```go
		CreatedAt: d.CreatedAt,
	}, nil
}
```

bằng:

```go
		CreatedAt: d.CreatedAt,
		Version:   version,
		Deleted:   d.Deleted,
		EditedAt:  d.EditedAt,
	}, nil
}
```

- thay toàn bộ `decodeMember`:

```go
func decodeMember(d memberDoc) (domain.Member, error) {
	room, err := toUint64("member room", d.Room)
	if err != nil {
		return domain.Member{}, err
	}
	cleared, err := toUint64("member cleared before seq", d.ClearedBefore)
	if err != nil {
		return domain.Member{}, err
	}
	return domain.Member{Room: room, Tenant: d.Tenant, User: d.User, Role: d.Role, JoinedAt: d.JoinedAt, ClearedBeforeSeq: cleared}, nil
}
```

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/mongostore/..."`
Expected: PASS (test cũ `TestMessageCodecRoundTrip`, `TestMemberCodecRoundTrip` vẫn đúng danh sách field nhờ `omitempty`; integration skip). `wc -l apps/core/internal/store/mongostore/codec.go` ≤ 185.

**Step 5: Test integration**

`apps/core/internal/store/mongostore/bootstrap_integration_test.go`, thay:

```go
func assertMessagesLayout(t *testing.T, db *mongo.Database) {
	t.Helper()
	opts := collectionOptions(t, db, messagesCollection)
	if v, ok := opts.Lookup("clusteredIndex", "key", "_id").AsInt64OK(); !ok || v != 1 {
		t.Fatalf("messages options %s: want clusteredIndex key {_id: 1}", opts)
	}
	if u, ok := opts.Lookup("clusteredIndex", "unique").BooleanOK(); !ok || !u {
		t.Fatalf("messages options %s: want a unique clustered index", opts)
	}
	if c, _ := opts.Lookup("storageEngine", "wiredTiger", "configString").StringValueOK(); c != "block_compressor=zstd" {
		t.Fatalf("messages configString = %q, want block_compressor=zstd", c)
	}
}
```

bằng:

```go
func assertClusteredLayout(t *testing.T, db *mongo.Database, name string) {
	t.Helper()
	opts := collectionOptions(t, db, name)
	if v, ok := opts.Lookup("clusteredIndex", "key", "_id").AsInt64OK(); !ok || v != 1 {
		t.Fatalf("%s options %s: want clusteredIndex key {_id: 1}", name, opts)
	}
	if u, ok := opts.Lookup("clusteredIndex", "unique").BooleanOK(); !ok || !u {
		t.Fatalf("%s options %s: want a unique clustered index", name, opts)
	}
	if c, _ := opts.Lookup("storageEngine", "wiredTiger", "configString").StringValueOK(); c != "block_compressor=zstd" {
		t.Fatalf("%s configString = %q, want block_compressor=zstd", name, c)
	}
}
```

và trong `TestBootstrapIsIdempotent` thay `assertMessagesLayout(t, db)` bằng `assertClusteredLayout(t, db, messagesCollection)`.

`apps/core/internal/store/mongostore/bootstrap_edits_integration_test.go`:

```go
package mongostore

import (
	"errors"
	"testing"
)

func TestBootstrapCreatesEditAndHiddenCollections(t *testing.T) {
	db := itDatabase(t, itClient(t))
	for i := range 2 {
		if err := Bootstrap(t.Context(), db); err != nil {
			t.Fatalf("Bootstrap #%d: %v", i+1, err)
		}
	}
	assertClusteredLayout(t, db, editsCollection)
	if got := indexKeys(t, db.Collection(editsCollection)); !hasIndex(got, "r:1,ts:1", false) {
		t.Fatalf("message_edits indexes = %v, want non-unique r:1,ts:1", got)
	}
	if got := indexKeys(t, db.Collection(hiddenCollection)); !hasIndex(got, "u:1,r:1,th:1,s:1", true) {
		t.Fatalf("hidden indexes = %v, want unique u:1,r:1,th:1,s:1", got)
	}
}

func TestBootstrapRejectsUnclusteredEdits(t *testing.T) {
	db := itDatabase(t, itClient(t))
	if err := db.CreateCollection(t.Context(), editsCollection); err != nil {
		t.Fatalf("create plain message_edits: %v", err)
	}
	if err := Bootstrap(t.Context(), db); !errors.Is(err, ErrNotClustered) {
		t.Fatalf("Bootstrap error = %v, want ErrNotClustered", err)
	}
}

func hasIndex(indexes map[string]bool, pattern string, unique bool) bool {
	u, ok := indexes[pattern]
	return ok && u == unique
}
```

`apps/core/internal/store/mongostore/edits_integration_test.go`:

```go
package mongostore

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/storetest"
)

func TestMongoEditsContract(t *testing.T) {
	client := itClient(t)
	storetest.RunEdits(t, func(t *testing.T) (storetest.EditableMessages, storetest.ClearableRooms, store.Edits, store.Hidden) {
		s, _ := itStore(t, client)
		return s, s, s, s
	})
}
```

**Step 6: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/mongostore/..."`
Expected: FAIL biên dịch: `undefined: editsCollection`, `undefined: hiddenCollection`, `*Store does not implement storetest.EditableMessages (missing method ApplyEdit)`.

**Step 7: Code adapter**

`apps/core/internal/store/mongostore/mongostore.go`:
- thay khối `const (...)` bằng:

```go
const (
	messagesCollection        = "messages"
	roomsCollection           = "rooms"
	membersCollection         = "members"
	reconcilerStateCollection = "reconciler_state"
	editsCollection           = "message_edits"
	hiddenCollection          = "hidden"
)
```

- thay khối assertion:

```go
var (
	_ store.Messages = (*Store)(nil)
	_ store.Rooms    = (*Store)(nil)
)
```

bằng:

```go
var (
	_ store.Messages       = (*Store)(nil)
	_ store.Rooms          = (*Store)(nil)
	_ store.MessageEditor  = (*Store)(nil)
	_ store.HistoryClearer = (*Store)(nil)
	_ store.Edits          = (*Store)(nil)
	_ store.Hidden         = (*Store)(nil)
)
```

- trong struct `Store`, sau `members   *mongo.Collection` thêm:

```go
	edits     *mongo.Collection
	hidden    *mongo.Collection
```

- trong `New`, sau `members:   db.Collection(membersCollection, primary),` thêm:

```go
		edits:     db.Collection(editsCollection, primary),
		hidden:    db.Collection(hiddenCollection, primary),
```

`apps/core/internal/store/mongostore/errors.go`, thay `errors.New("mongostore: messages collection is not clustered on _id")` bằng `errors.New("mongostore: collection is not clustered on _id")`.

`apps/core/internal/store/mongostore/bootstrap.go`:
- thay toàn bộ hàm `Bootstrap` bằng:

```go
func Bootstrap(ctx context.Context, db *mongo.Database) error {
	for _, name := range []string{messagesCollection, editsCollection} {
		if err := ensureClustered(ctx, db, name); err != nil {
			return err
		}
	}
	for _, name := range []string{roomsCollection, membersCollection, reconcilerStateCollection, hiddenCollection} {
		if err := createCollection(ctx, db, name); err != nil {
			return err
		}
	}
	indexes := []struct {
		coll   string
		models []mongo.IndexModel
	}{
		{membersCollection, memberIndexes()},
		{roomsCollection, roomIndexes()},
		{editsCollection, editIndexes()},
		{hiddenCollection, hiddenIndexes()},
	}
	for _, ix := range indexes {
		if err := ensureIndexes(ctx, db, ix.coll, ix.models); err != nil {
			return err
		}
	}
	return ensureFeedAnchor(ctx, db)
}
```

- thay toàn bộ hàm `ensureMessages` bằng:

```go
func ensureClustered(ctx context.Context, db *mongo.Database, name string) error {
	opts := options.CreateCollection().
		SetClusteredIndex(bson.D{{Key: "key", Value: bson.D{{Key: "_id", Value: 1}}}, {Key: "unique", Value: true}}).
		SetStorageEngine(bson.D{{Key: "wiredTiger", Value: bson.D{{Key: "configString", Value: "block_compressor=zstd"}}}})
	if err := createCollection(ctx, db, name, opts); err != nil {
		return err
	}
	specs, err := db.ListCollectionSpecifications(ctx, bson.D{{Key: "name", Value: name}})
	if err != nil {
		return fmt.Errorf("bootstrap %s: list collections: %w", name, err)
	}
	if len(specs) != 1 || !clusteredOnID(specs[0].Options) {
		return fmt.Errorf("bootstrap %s: %w", name, ErrNotClustered)
	}
	return nil
}
```

- sau hàm `roomIndexes` thêm:

```go
func editIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{{Keys: bson.D{{Key: "r", Value: 1}, {Key: "ts", Value: 1}}}}
}

func hiddenIndexes() []mongo.IndexModel {
	keys := bson.D{{Key: "u", Value: 1}, {Key: "r", Value: 1}, {Key: "th", Value: 1}, {Key: "s", Value: 1}}
	return []mongo.IndexModel{{Keys: keys, Options: options.Index().SetUnique(true)}}
}
```

`messages` vẫn được tạo trước mọi collection khác, nên `TestBootstrapRejectsUnclusteredMessages` (chỉ còn đúng `messages` sau lỗi) giữ nguyên.

`apps/core/internal/store/mongostore/edits.go`:

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

func (s *Store) Append(ctx context.Context, e domain.Edit) error {
	doc, err := encodeEdit(e)
	if err != nil {
		return err
	}
	if _, err := s.edits.InsertOne(ctx, doc); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return fmt.Errorf("append edit v%d of %d/%d/%d: %w", e.Version, e.Room, e.Thread, e.Seq, store.ErrEditExists)
		}
		return fmt.Errorf("append edit v%d of %d/%d/%d: %w", e.Version, e.Room, e.Thread, e.Seq, err)
	}
	return nil
}

func (s *Store) At(ctx context.Context, key store.MsgKey, version uint32) (domain.Edit, error) {
	var d editDoc
	filter := bson.D{{Key: "_id", Value: keys.Edit(key.Room, key.Thread, key.Seq, version)}}
	if err := findOne(ctx, s.edits, filter, &d, store.ErrEditNotFound); err != nil {
		return domain.Edit{}, fmt.Errorf("edit v%d of %d/%d/%d: %w", version, key.Room, key.Thread, key.Seq, err)
	}
	return decodeEdit(d)
}

func (s *Store) Latest(ctx context.Context, key store.MsgKey) (domain.Edit, bool, error) {
	got, err := s.findEdits(ctx, versionsAfter(key, 0), bson.D{{Key: "_id", Value: -1}}, 1)
	if err != nil || len(got) == 0 {
		return domain.Edit{}, false, err
	}
	return got[0], true, nil
}

func (s *Store) History(ctx context.Context, key store.MsgKey, after uint32, limit int) ([]domain.Edit, error) {
	if err := store.ValidateLimit(limit, store.MaxEditPage); err != nil {
		return nil, err
	}
	return s.findEdits(ctx, versionsAfter(key, after), bson.D{{Key: "_id", Value: 1}}, limit)
}

func (s *Store) Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Edit, error) {
	if err := store.ValidateLimit(limit, store.MaxEditScan); err != nil {
		return nil, err
	}
	r, err := toInt64("room id", room)
	if err != nil {
		return nil, err
	}
	filter := bson.D{{Key: "r", Value: r}, {Key: "ts", Value: bson.D{{Key: "$gte", Value: from}, {Key: "$lte", Value: to}}}}
	return s.findEdits(ctx, filter, bson.D{{Key: "ts", Value: 1}, {Key: "_id", Value: 1}}, limit)
}

func (s *Store) PurgeText(ctx context.Context, key store.MsgKey, upTo uint32) error {
	if upTo == 0 {
		return ctx.Err()
	}
	filter := bson.D{{Key: "_id", Value: bson.D{
		{Key: "$gte", Value: keys.Edit(key.Room, key.Thread, key.Seq, 1)},
		{Key: "$lte", Value: keys.Edit(key.Room, key.Thread, key.Seq, upTo)},
	}}}
	update := bson.D{{Key: "$unset", Value: bson.D{{Key: "x", Value: ""}, {Key: "p", Value: ""}}}}
	if _, err := s.edits.UpdateMany(ctx, filter, update); err != nil {
		return fmt.Errorf("purge text of %d/%d/%d up to v%d: %w", key.Room, key.Thread, key.Seq, upTo, err)
	}
	return nil
}

func versionsAfter(key store.MsgKey, after uint32) bson.D {
	return bson.D{{Key: "_id", Value: bson.D{
		{Key: "$gt", Value: keys.Edit(key.Room, key.Thread, key.Seq, after)},
		{Key: "$lte", Value: keys.Edit(key.Room, key.Thread, key.Seq, math.MaxUint32)},
	}}}
}

func (s *Store) findEdits(ctx context.Context, filter, sort bson.D, limit int) ([]domain.Edit, error) {
	cur, err := s.edits.Find(ctx, filter, options.Find().SetSort(sort).SetLimit(int64(limit)))
	if err != nil {
		return nil, fmt.Errorf("find edits: %w", err)
	}
	var docs []editDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("find edits: %w", err)
	}
	return decodeEdits(docs)
}
```

`apps/core/internal/store/mongostore/apply_edit.go`:

```go
package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func (s *Store) ApplyEdit(ctx context.Context, e domain.Edit) error {
	if err := store.ValidateEdit(e); err != nil {
		return err
	}
	version, err := toInt32("version", e.Version)
	if err != nil {
		return err
	}
	filter := bson.D{
		{Key: "_id", Value: keys.Msg(e.Room, e.Thread, e.Seq)},
		{Key: "$or", Value: bson.A{
			bson.D{{Key: "v", Value: bson.D{{Key: "$exists", Value: false}}}},
			bson.D{{Key: "v", Value: bson.D{{Key: "$lt", Value: version}}}},
		}},
	}
	text, deleted := e.Text, e.Kind == domain.EditDelete
	if deleted {
		text = ""
	}
	set := bson.D{{Key: "v", Value: version}, {Key: "ea", Value: e.At}, {Key: "x", Value: text}, {Key: "d", Value: deleted}}
	if _, err := s.messages.UpdateOne(ctx, filter, bson.D{{Key: "$set", Value: set}}); err != nil {
		return fmt.Errorf("apply edit v%d to %d/%d/%d: %w", e.Version, e.Room, e.Thread, e.Seq, err)
	}
	return nil
}
```

`apps/core/internal/store/mongostore/hidden.go`:

```go
package mongostore

import (
	"context"
	"fmt"
	"math"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type hiddenDoc struct {
	User   string `bson:"u"`
	Room   int64  `bson:"r"`
	Thread int64  `bson:"th"`
	Seq    int64  `bson:"s"`
}

func (s *Store) Hide(ctx context.Context, user string, key store.MsgKey) error {
	doc, err := encodeHidden(user, key)
	if err != nil {
		return err
	}
	if _, err := s.hidden.InsertOne(ctx, doc); err != nil && !mongo.IsDuplicateKeyError(err) {
		return fmt.Errorf("hide %d/%d/%d for %q: %w", key.Room, key.Thread, key.Seq, user, err)
	}
	return nil
}

func encodeHidden(user string, key store.MsgKey) (hiddenDoc, error) {
	if err := key.Validate(); err != nil {
		return hiddenDoc{}, err
	}
	room, err := toInt64("room id", key.Room)
	if err != nil {
		return hiddenDoc{}, err
	}
	thread, err := toInt64("thread", key.Thread)
	if err != nil {
		return hiddenDoc{}, err
	}
	seq, err := toInt64("seq", key.Seq)
	if err != nil {
		return hiddenDoc{}, err
	}
	return hiddenDoc{User: user, Room: room, Thread: thread, Seq: seq}, nil
}

func (s *Store) HiddenIn(ctx context.Context, user string, room, thread, from, to uint64) ([]uint64, error) {
	r, roomErr := toInt64("room id", room)
	th, threadErr := toInt64("thread", thread)
	lo, fromErr := toInt64("from", from)
	hi, _ := toInt64("to", min(to, math.MaxInt64))
	if roomErr != nil || threadErr != nil || fromErr != nil || from > to {
		return nil, ctx.Err()
	}
	filter := bson.D{
		{Key: "u", Value: user}, {Key: "r", Value: r}, {Key: "th", Value: th},
		{Key: "s", Value: bson.D{{Key: "$gte", Value: lo}, {Key: "$lte", Value: hi}}},
	}
	opts := options.Find().SetSort(bson.D{{Key: "s", Value: 1}}).SetProjection(bson.D{{Key: "s", Value: 1}, {Key: "_id", Value: 0}})
	cur, err := s.hidden.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("hidden of %q in %d/%d: %w", user, room, thread, err)
	}
	var docs []hiddenDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("hidden of %q in %d/%d: %w", user, room, thread, err)
	}
	var out []uint64
	for _, d := range docs {
		seq, err := toUint64("hidden seq", d.Seq)
		if err != nil {
			return nil, err
		}
		out = append(out, seq)
	}
	return out, nil
}
```

`apps/core/internal/store/mongostore/rooms.go`, thêm vào cuối file (import đã đủ: `errors`, `bson`, `mongo`, `options`, `domain`):

```go
func (s *Store) ClearHistory(ctx context.Context, room uint64, user string, seq uint64) (uint64, error) {
	key, err := toInt64("room id", room)
	if err != nil {
		return 0, fmt.Errorf("clear history of %q in room %d: %w", user, room, domain.ErrNotMember)
	}
	upTo, err := toInt64("seq", seq)
	if err != nil {
		return 0, err
	}
	filter := bson.D{{Key: "r", Value: key}, {Key: "u", Value: user}}
	update := bson.D{{Key: "$max", Value: bson.D{{Key: "cb", Value: upTo}}}}
	opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
	var d memberDoc
	err = s.members.FindOneAndUpdate(ctx, filter, update, opts).Decode(&d)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		return 0, fmt.Errorf("clear history of %q in room %d: %w", user, room, domain.ErrNotMember)
	case err != nil:
		return 0, fmt.Errorf("clear history of %q in room %d: %w", user, room, err)
	}
	m, err := decodeMember(d)
	if err != nil {
		return 0, err
	}
	return m.ClearedBeforeSeq, nil
}
```

`apps/core/internal/store/mongostore/room_activity.go`, trong `activityFields` thay:

```go
	if a.Thread == 0 {
```

bằng:

```go
	if a.Thread == 0 && a.Seq > 0 {
```

**Step 8: Chạy unit, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/..."` rồi `make vet`
Expected: PASS (integration skip); vet sạch. `wc -l apps/core/internal/store/mongostore/*.go` mỗi file < 200.

**Step 9: Integration**

Run: `make infra-up && make itest`
Expected: PASS, gồm `TestMongoEditsContract` (mọi case của `RunEdits`), `TestMongoStoreContract` (có case mới `an activity with seq 0 bumps only the last change`), `TestBootstrapCreatesEditAndHiddenCollections`, `TestBootstrapRejectsUnclusteredEdits`, `TestBootstrapIsIdempotent`, `TestBootstrapRejectsUnclusteredMessages`, và itest của `apps/core` (bootstrap trên DB thật tạo thêm hai collection). Case Mongo khác memstore → dừng, báo cáo (không sửa contract cho vừa adapter).

**Step 10: Commit**

INDEXES.csv, dòng `apps/core/internal/store/mongostore`:
- thay `"MongoDB adapter: Bootstrap creates clustered zstd messages + rooms/members indexes + reconciler_state` bằng `"MongoDB adapter: Bootstrap creates clustered zstd messages and message_edits (index {r:1, ts:1}), hidden (unique {u:1, r:1, th:1, s:1}) + rooms/members indexes + reconciler_state`.
- thay `Find majority read; room before members;` bằng `Find majority read; room before members; edits: messages v/d/ea projection via ApplyEdit (UpdateOne only while v is missing or lower, never upserts), message_edits facts (_id room|thread|seq|version; Append insert, duplicate -> ErrEditExists; Latest/History clustered _id range; Between {r, ts} sorted ts,_id; PurgeText $unset x/p up to a version), hidden insert-or-ignore + covered HiddenIn, members cb via FindOneAndUpdate $max;`.
- thay `rooms ls/lm/lc/ab via unordered bulk $max (no upsert)` bằng `rooms ls/lm (main thread, seq > 0 only)/lc/ab via unordered bulk $max (no upsert)`.
- key symbols giữ nguyên; cột decisions thay `D52;D76;D69` bằng `D52;D62;D69;D70;D72;D75;D76`.

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/store/mongostore/edit_codec.go apps/core/internal/store/mongostore/edit_codec_test.go apps/core/internal/store/mongostore/edits.go apps/core/internal/store/mongostore/apply_edit.go apps/core/internal/store/mongostore/hidden.go apps/core/internal/store/mongostore/bootstrap_edits_integration_test.go apps/core/internal/store/mongostore/edits_integration_test.go
git commit -m "feat(mongostore): store edit facts, hidden messages and cleared history" -- apps/core/internal/store/mongostore/ INDEXES.csv
```

---

### Task 5: Feed `EditInserted` (memstore + Mongo)

Nhật ký commit có thêm loại fact thứ ba: insert `message_edits`. `store.Change` mang `Edit`. memstore gắn `Edits` vào log chung qua `NewFeed(msgs, rooms, edits)` (`edits` nil = không log fact sửa). Thứ tự lock luôn là `Edits.mu` → `Messages.mu`, giống `Rooms.mu` → `Messages.mu`; `ApplyEdit` chỉ giữ `Messages.mu`. `appendRoom` gộp thành `appendFact(logged)` dùng chung cho room và edit. Mongo feed lọc thêm `ns.coll = message_edits` và giải mã thành `EditInserted`. Contract mới là entry riêng `storetest.RunEditFeed(t, open func(t) (store.Edits, store.ChangeFeed))`, `RunFeed` không đổi chữ ký.

Reader (`reconcile/term.go`) **chưa** nhận kind mới: `forward()` vẫn drop và đếm `Dropped` cho `EditInserted` tới Task 6 (khi `work.Decode` biết kind này). Thực tế chưa có gì ghi `message_edits` trước Task 9 nên không mất gì.

Lint `exhaustive` (`default-signifies-exhaustive`) sẽ báo `work.RecordOf` (switch trên `store.ChangeKind` không có `default`) thiếu case `EditInserted`. Task này thêm case chỉ lấy khoá room/thread/seq; Task 6 thêm `Version`.

Caller của `memstore.NewFeed` đã grep: `memstore/feed_test.go:15,29,42`, `memstore/memstore_test.go:20`, `reconcile/harness_test.go:71`. Dựng `store.Change{...}`: `memstore/feed.go:86`, `mongostore/feed_change.go` (thêm case). Switch trên `store.ChangeKind`: `work/record.go` `RecordOf` (không default) và `Record.ID` (có default).

**Files:**
- Modify: `apps/core/internal/store/feed.go`
- Modify: `apps/core/internal/store/memstore/change_log.go`, `feed.go`, `edits.go`, `rooms.go` (`Create`), `feed_test.go`, `memstore_test.go`
- Create: `apps/core/internal/store/storetest/feed_edit_cases.go`
- Modify: `apps/core/internal/store/mongostore/feed.go` (`feedPipeline`), `feed_change.go`, `feed_change_test.go`, `feed_integration_test.go`
- Modify: `apps/core/internal/work/record.go` (`RecordOf`), `apps/core/internal/work/record_test.go`
- Modify: `apps/core/internal/reconcile/harness_test.go:71`
- Modify: `INDEXES.csv` (dòng `store`, `store/memstore`, `store/storetest`, `store/mongostore`)

**Step 1: Test**

`apps/core/internal/store/storetest/feed_edit_cases.go`:

```go
package storetest

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func RunEditFeed(t *testing.T, open func(t *testing.T) (store.Edits, store.ChangeFeed)) {
	t.Helper()
	t.Run("edit inserts come out in commit order with their content", func(t *testing.T) {
		edits, feed := open(t)
		cur := openCursor(t, feed)
		want := []domain.Edit{fact(roomA, mainThread, 1, 1), fact(roomB, sideThread, 2, 1), deletion(roomA, mainThread, 1, 2)}
		mustAppend(t, edits, want...)
		got := nextChanges(t, cur, len(want))
		facts := make([]domain.Edit, len(got))
		for i, c := range got {
			if c.Kind != store.EditInserted || c.Msg != (domain.Message{}) || c.Room != (domain.Room{}) {
				t.Fatalf("change %d = %+v, want only an edit", i, c)
			}
			facts[i] = c.Edit
		}
		assertEdits(t, "edit changes", facts, want)
	})
}
```

`apps/core/internal/store/memstore/memstore_test.go`:
- trong `TestFeedContract` thay `return msgs, rooms, memstore.NewFeed(msgs, rooms)` bằng `return msgs, rooms, memstore.NewFeed(msgs, rooms, nil)`.
- thêm vào cuối:

```go
func TestEditFeedContract(t *testing.T) {
	storetest.RunEditFeed(t, func(*testing.T) (store.Edits, store.ChangeFeed) {
		msgs, edits := memstore.NewMessages(), memstore.NewEdits()
		return edits, memstore.NewFeed(msgs, nil, edits)
	})
}
```

`apps/core/internal/store/memstore/feed_test.go`: thay `memstore.NewFeed(memstore.NewMessages(), nil)` (hai chỗ) bằng `memstore.NewFeed(memstore.NewMessages(), nil, nil)` và `memstore.NewFeed(msgs, nil)` bằng `memstore.NewFeed(msgs, nil, nil)`.

`apps/core/internal/reconcile/harness_test.go:71`, thay `rg.feed = memstore.NewFeed(rg.msgs, rg.rooms)` bằng `rg.feed = memstore.NewFeed(rg.msgs, rg.rooms, nil)`.

`apps/core/internal/store/mongostore/feed_change_test.go`:
- trong map của `TestDecodeChangeRejectsOtherCollectionsAndBrokenDocuments`, sau dòng `"negative room id": ...` thêm `"bad edit id":      changeOn(t, editsCollection, bson.D{{Key: "_id", Value: []byte{1, 2}}}),` (gofmt căn lại cột).
- thêm vào cuối (`sampleEdit` có ở `edit_codec_test.go`):

```go
func TestDecodeChangeReadsEdits(t *testing.T) {
	e := sampleEdit()
	doc, err := encodeEdit(e)
	if err != nil {
		t.Fatalf("encodeEdit: %v", err)
	}
	got, err := decodeChange(changeOn(t, editsCollection, doc))
	if err != nil || got.Kind != store.EditInserted || store.EditKeyOf(got.Edit) != store.EditKeyOf(e) || got.Edit.Version != e.Version ||
		got.Edit.Text != e.Text || got.Edit.Prev != e.Prev || !got.CommittedAt.Equal(codecTime) || got.Msg.Room != 0 || got.Room.ID != 0 {
		t.Fatalf("edit change = %+v, %v", got, err)
	}
}
```

`apps/core/internal/store/mongostore/feed_integration_test.go`, thêm vào cuối:

```go
func TestMongoEditFeedContract(t *testing.T) {
	client := itClient(t)
	storetest.RunEditFeed(t, func(t *testing.T) (store.Edits, store.ChangeFeed) {
		s, db := itStore(t, client)
		return s, NewFeed(db)
	})
}
```

`apps/core/internal/work/record_test.go`, trong `TestRecordOfKeepsOnlyKeysAndCommitTime` thêm trước dấu `}` cuối hàm:

```go
	edit := store.Change{Kind: store.EditInserted, Edit: domain.Edit{Room: 42, Thread: 3, Seq: 9, Version: 2, Text: "x"}, CommittedAt: committed}
	if got, want := work.RecordOf(edit), (work.Record{Kind: store.EditInserted, Room: 42, Thread: 3, Seq: 9, CommittedAt: committed}); got != want {
		t.Fatalf("RecordOf(edit) = %+v, want %+v", got, want)
	}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/... ./apps/core/internal/work/... ./apps/core/internal/reconcile/..."`
Expected: FAIL biên dịch: `undefined: store.EditInserted`, `unknown field Edit in struct literal of type store.Change`, `too many arguments in call to memstore.NewFeed`.

**Step 3: Code**

`apps/core/internal/store/feed.go`, thay:

```go
const (
	MessageInserted ChangeKind = iota + 1
	RoomInserted
)

type Change struct {
	Kind        ChangeKind
	Msg         domain.Message
	Room        domain.Room
	CommittedAt time.Time
	Position    Position
}
```

bằng:

```go
const (
	MessageInserted ChangeKind = iota + 1
	RoomInserted
	EditInserted
)

type Change struct {
	Kind        ChangeKind
	Msg         domain.Message
	Room        domain.Room
	Edit        domain.Edit
	CommittedAt time.Time
	Position    Position
}
```

`apps/core/internal/store/memstore/change_log.go`:
- struct `logged` thêm field `edit domain.Edit` sau `room domain.Room`:

```go
type logged struct {
	kind store.ChangeKind
	msg  domain.Message
	room domain.Room
	edit domain.Edit
	at   time.Time
}
```

- thay:

```go
func (s *Messages) appendRoom(r domain.Room) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendLog(logged{kind: store.RoomInserted, room: r})
}
```

bằng:

```go
func (s *Messages) appendFact(l logged) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendLog(l)
}
```

- thêm vào cuối:

```go
func (s *Edits) attach(log *Messages) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = log
}
```

`apps/core/internal/store/memstore/rooms.go`, trong `Create` thay `s.log.appendRoom(r)` bằng `s.log.appendFact(logged{kind: store.RoomInserted, room: r})`.

`apps/core/internal/store/memstore/edits.go`:
- struct `Edits` thêm field `log   *Messages` sau `facts map[store.MsgKey][]domain.Edit`.
- trong `Append` thay:

```go
	s.facts[key] = slices.Insert(line, i, e)
	return nil
}
```

bằng:

```go
	s.facts[key] = slices.Insert(line, i, e)
	if s.log != nil {
		s.log.appendFact(logged{kind: store.EditInserted, edit: e})
	}
	return nil
}
```

`apps/core/internal/store/memstore/feed.go`:
- thay:

```go
func NewFeed(msgs *Messages, rooms *Rooms) *Feed {
	if rooms != nil {
		rooms.attach(msgs)
	}
	return &Feed{msgs: msgs, confirmed: msgs.logLen(), known: true}
}
```

bằng:

```go
func NewFeed(msgs *Messages, rooms *Rooms, edits *Edits) *Feed {
	if rooms != nil {
		rooms.attach(msgs)
	}
	if edits != nil {
		edits.attach(msgs)
	}
	return &Feed{msgs: msgs, confirmed: msgs.logLen(), known: true}
}
```

- trong `cursor.Next` thay `return store.Change{Kind: l.kind, Msg: l.msg, Room: l.room, CommittedAt: l.at, Position: store.Position(strconv.Itoa(c.next))}, nil` bằng `return store.Change{Kind: l.kind, Msg: l.msg, Room: l.room, Edit: l.edit, CommittedAt: l.at, Position: store.Position(strconv.Itoa(c.next))}, nil`.

`apps/core/internal/store/mongostore/feed.go`, trong `feedPipeline` thay `bson.A{messagesCollection, roomsCollection}` bằng `bson.A{messagesCollection, roomsCollection, editsCollection}`.

`apps/core/internal/store/mongostore/feed_change.go`, trong `decodeChange` thay:

```go
		return store.Change{Kind: store.RoomInserted, Room: r, CommittedAt: ev.WallTime}, nil
	default:
```

bằng:

```go
		return store.Change{Kind: store.RoomInserted, Room: r, CommittedAt: ev.WallTime}, nil
	case editsCollection:
		var d editDoc
		if err := bson.Unmarshal(ev.FullDocument, &d); err != nil {
			return store.Change{}, fmt.Errorf("%w: edit document: %w", errCorrupt, err)
		}
		e, err := decodeEdit(d)
		if err != nil {
			return store.Change{}, err
		}
		return store.Change{Kind: store.EditInserted, Edit: e, CommittedAt: ev.WallTime}, nil
	default:
```

`apps/core/internal/work/record.go`, trong `RecordOf` thay:

```go
	case store.RoomInserted:
		r.Room = c.Room.ID
	}
```

bằng:

```go
	case store.RoomInserted:
		r.Room = c.Room.ID
	case store.EditInserted:
		r.Room, r.Thread, r.Seq = c.Edit.Room, c.Edit.Thread, c.Edit.Seq
	}
```

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/store/..."` rồi `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/work/... ./apps/core/internal/reconcile/..."`
Expected: PASS (`-count=5` vì lock mới giữa `Edits` và `Messages`). `make lint` sạch (không còn cảnh báo `exhaustive`). `wc -l apps/core/internal/store/memstore/*.go apps/core/internal/store/mongostore/feed_change.go` mỗi file < 200.

**Step 5: Integration**

Run: `make itest`
Expected: PASS, gồm `TestMongoEditFeedContract` và `TestMongoFeedContract`.

**Step 6: Commit**

INDEXES.csv:
- dòng `apps/core/internal/store`: thay `Change carries Kind MessageInserted with Msg or RoomInserted with Room)` bằng `Change carries Kind MessageInserted with Msg, RoomInserted with Room or EditInserted with Edit)`; key symbols thay `RoomInserted;Position;` bằng `RoomInserted;EditInserted;Position;`.
- dòng `apps/core/internal/store/memstore`: thay `in-memory change feed over one insert log of messages and (when NewFeed is given them) rooms,` bằng `in-memory change feed over one insert log of messages and (when NewFeed is given them) rooms and edit facts,`.
- dòng `apps/core/internal/store/storetest`: thay `room inserts in commit order with their content;` bằng `room inserts in commit order with their content; edit feed (RunEditFeed: edit inserts in commit order with their content);`; key symbols thay `RunFeed;` bằng `RunFeed;RunEditFeed;`.
- dòng `apps/core/internal/store/mongostore`: thay `(inserts into messages and rooms, decoded by collection into MessageInserted/RoomInserted;` bằng `(inserts into messages, rooms and message_edits, decoded by collection into MessageInserted/RoomInserted/EditInserted;`.

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/store/storetest/feed_edit_cases.go
git commit -m "feat(store): feed edit inserts as EditInserted changes" -- apps/core/internal/store/ apps/core/internal/work/record.go apps/core/internal/work/record_test.go apps/core/internal/reconcile/harness_test.go INDEXES.csv
```

---

### Task 6: `work.Record` 37 byte + `Version` + id `e:` + reader chuyển `EditInserted`

Record work mang `Version` (D84): layout `kind(1) room(8) thread(8) seq(8) version(4) unixNano(8)`, big-endian, 37 byte. Id `e:{room}-{th}-{seq}-v{ver}` = `"e:" + pbconv.MessageChangeEventID(...)`. Danh sách kind work stream chở được gom về một chỗ, `work.KnownKind`, dùng cho cả `Decode` lẫn reader (`reconcile` `forward`), để reader không bao giờ đẩy một record mà `Decode` sẽ từ chối.

Hệ quả triển khai (dev): record 33 byte còn tồn trong work stream sau khi deploy sẽ bị `Queue` Term như bản ghi hỏng (`BadRecordsError`, đếm vào metric failures). Không có dữ liệu prod; ghi vào "Kết quả thực thi".

Từ task này tới Task 11, record `EditInserted` tới effect worker nhưng registry chưa có effect cho kind này: `runGroup` không chạy effect nào và ack (không lỗi, không retry).

**Files:**
- Modify: `apps/core/internal/work/record.go`
- Modify: `apps/core/internal/work/record_test.go`
- Modify: `apps/core/internal/reconcile/term.go` (`forward`)
- Modify: `apps/core/internal/reconcile/harness_test.go` (`rig`, `newRig`, thêm `edit`)
- Create: `apps/core/internal/reconcile/edit_forward_test.go`
- Modify: `INDEXES.csv` (dòng `apps/core/internal/work`, `apps/core/internal/reconcile`)

**Step 1: Test work**

`apps/core/internal/work/record_test.go`:
- trong `TestRecordOfKeepsOnlyKeysAndCommitTime`, thay `(work.Record{Kind: store.EditInserted, Room: 42, Thread: 3, Seq: 9, CommittedAt: committed})` bằng `(work.Record{Kind: store.EditInserted, Room: 42, Thread: 3, Seq: 9, Version: 2, CommittedAt: committed})`.
- thay toàn bộ `TestRecordRoundTripsThroughThirtyThreeBytes` bằng:

```go
func TestRecordRoundTripsThroughThirtySevenBytes(t *testing.T) {
	for _, r := range []work.Record{
		{Kind: store.MessageInserted, Room: 42, Thread: 3, Seq: 9, CommittedAt: committed},
		{Kind: store.RoomInserted, Room: math.MaxInt64, CommittedAt: committed},
		{Kind: store.MessageInserted, Room: 1, Seq: math.MaxUint64, CommittedAt: committed},
		{Kind: store.EditInserted, Room: 42, Thread: 3, Seq: 9, Version: math.MaxUint32, CommittedAt: committed},
	} {
		b := work.Encode(r)
		if len(b) != work.RecordSize || work.RecordSize != 37 {
			t.Fatalf("Encode(%+v) is %d bytes, RecordSize %d; want 37", r, len(b), work.RecordSize)
		}
		got, err := work.Decode(b)
		if err != nil || got.Kind != r.Kind || got.Room != r.Room || got.Thread != r.Thread || got.Seq != r.Seq || got.Version != r.Version || !got.CommittedAt.Equal(r.CommittedAt) {
			t.Fatalf("Decode(Encode(%+v)) = %+v, %v", r, got, err)
		}
	}
}
```

- trong `TestEncodeIsBigEndianAndClampsTimesBeforeTheEpoch` thay:

```go
	want[0], want[8], want[16], want[24], want[32] = 1, 1, 2, 3, 4
	if got := work.Encode(work.Record{Kind: store.MessageInserted, Room: 1, Thread: 2, Seq: 3, CommittedAt: time.Unix(0, 4)}); !bytes.Equal(got, want) {
```

bằng:

```go
	want[0], want[8], want[16], want[24], want[28], want[36] = 3, 1, 2, 3, 5, 4
	if got := work.Encode(work.Record{Kind: store.EditInserted, Room: 1, Thread: 2, Seq: 3, Version: 5, CommittedAt: time.Unix(0, 4)}); !bytes.Equal(got, want) {
```

- trong `TestDecodeRejectsBadRecords` thay `zeroKind[0], unknownKind[0], pastInt64[25] = 0, 9, 0x80` bằng `zeroKind[0], unknownKind[0], pastInt64[29] = 0, 9, 0x80`.
- trong `TestIDsAreNaturalKeys`, sau dòng `"room": ...` thêm:

```go
		"edit":           {work.Record{Kind: store.EditInserted, Room: 42, Seq: 7, Version: 1}, "e:42-0-7-v1"},
		"thread edit":    {work.Record{Kind: store.EditInserted, Room: 42, Thread: 3, Seq: 9, Version: 12}, "e:42-3-9-v12"},
```

- thêm vào cuối file:

```go
func TestKnownKindsAreTheThreeChangeKinds(t *testing.T) {
	for k := range store.ChangeKind(6) {
		want := k == store.MessageInserted || k == store.RoomInserted || k == store.EditInserted
		if got := work.KnownKind(k); got != want {
			t.Errorf("KnownKind(%d) = %v, want %v", k, got, want)
		}
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/work/..."`
Expected: FAIL biên dịch: `unknown field Version in struct literal of type work.Record`, `undefined: work.KnownKind`.

**Step 3: Code work**

`apps/core/internal/work/record.go`:
- thay `const RecordSize = 33` bằng `const RecordSize = 37`.
- thay struct `Record`:

```go
type Record struct {
	Kind        store.ChangeKind
	Room        uint64
	Thread      uint64
	Seq         uint64
	CommittedAt time.Time
}
```

bằng:

```go
type Record struct {
	Kind        store.ChangeKind
	Room        uint64
	Thread      uint64
	Seq         uint64
	Version     uint32
	CommittedAt time.Time
}

func KnownKind(k store.ChangeKind) bool {
	switch k {
	case store.MessageInserted, store.RoomInserted, store.EditInserted:
		return true
	default:
		return false
	}
}
```

- trong `RecordOf` thay `r.Room, r.Thread, r.Seq = c.Edit.Room, c.Edit.Thread, c.Edit.Seq` bằng `r.Room, r.Thread, r.Seq, r.Version = c.Edit.Room, c.Edit.Thread, c.Edit.Seq, c.Edit.Version`.
- trong `ID` thay:

```go
	case store.RoomInserted:
		return "r:" + pbconv.RoomID(r.Room)
```

bằng:

```go
	case store.RoomInserted:
		return "r:" + pbconv.RoomID(r.Room)
	case store.EditInserted:
		return "e:" + pbconv.MessageChangeEventID(r.Room, r.Thread, r.Seq, r.Version)
```

- trong `Encode` thay:

```go
	b = binary.BigEndian.AppendUint64(b, r.Seq)
	return binary.BigEndian.AppendUint64(b, unixNano(r.CommittedAt))
```

bằng:

```go
	b = binary.BigEndian.AppendUint64(b, r.Seq)
	b = binary.BigEndian.AppendUint32(b, r.Version)
	return binary.BigEndian.AppendUint64(b, unixNano(r.CommittedAt))
```

- trong `Decode` thay:

```go
	if kind != store.MessageInserted && kind != store.RoomInserted {
		return Record{}, fmt.Errorf("%w: kind %d", ErrBadRecord, kind)
	}
	ns := binary.BigEndian.Uint64(b[25:])
```

bằng:

```go
	if !KnownKind(kind) {
		return Record{}, fmt.Errorf("%w: kind %d", ErrBadRecord, kind)
	}
	ns := binary.BigEndian.Uint64(b[29:])
```

và thay:

```go
		Seq:         binary.BigEndian.Uint64(b[17:]),
		CommittedAt: time.Unix(0, int64(ns)).UTC(),
```

bằng:

```go
		Seq:         binary.BigEndian.Uint64(b[17:]),
		Version:     binary.BigEndian.Uint32(b[25:]),
		CommittedAt: time.Unix(0, int64(ns)).UTC(),
```

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/work/..."`
Expected: PASS (nats integration skip). `wc -l apps/core/internal/work/record.go` ≤ 125.

**Step 4: Test reader**

`apps/core/internal/reconcile/harness_test.go`:
- struct `rig`, sau `rooms *memstore.Rooms` thêm `edits *memstore.Edits`.
- trong `newRig` thay:

```go
	rg := &rig{msgs: memstore.NewMessages(), rooms: memstore.NewRooms(), owner: &owner{}, js: &publishtest.JetStream{}, sink: &testlog.Sink{}}
```

bằng:

```go
	rg := &rig{msgs: memstore.NewMessages(), rooms: memstore.NewRooms(), edits: memstore.NewEdits(), owner: &owner{}, js: &publishtest.JetStream{}, sink: &testlog.Sink{}}
```

và thay `rg.feed = memstore.NewFeed(rg.msgs, rg.rooms, nil)` bằng `rg.feed = memstore.NewFeed(rg.msgs, rg.rooms, rg.edits)`.
- sau hàm `insert` thêm:

```go
func (rg *rig) edit(t *testing.T, r, seq uint64, version uint32) {
	t.Helper()
	e := domain.Edit{Room: r, Seq: seq, Version: version, Kind: domain.EditText, Tenant: tenant, By: "alice", Text: "edited", At: time.Now().UTC()}
	if err := rg.edits.Append(t.Context(), e); err != nil {
		t.Fatalf("append edit %d/%d v%d: %v", r, seq, version, err)
	}
}
```

`apps/core/internal/reconcile/edit_forward_test.go`:

```go
package reconcile_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

func TestForwardsAnEditInsertAsAnEditRecord(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		committed := time.Now()
		rg.edit(t, room, 1, 2)
		synctest.Wait()
		stored := rg.js.Stored()
		if len(stored) != 1 {
			t.Fatalf("stored = %v, want one edit record", storedIDs(rg.js))
		}
		if want := work.Subject(setup.SubjectRoot, work.Partition(room, setup.Partitions)); stored[0].Subject != want {
			t.Fatalf("subject = %q, want %q", stored[0].Subject, want)
		}
		got, err := work.Decode(stored[0].Data)
		if err != nil || got.Kind != store.EditInserted || got.Room != room || got.Thread != 0 || got.Seq != 1 || got.Version != 2 || !got.CommittedAt.Equal(committed) {
			t.Fatalf("record = %+v, %v; want edit %d/0/1 v2 committed at %v", got, err, room, committed)
		}
		if id := publishtest.MsgID(stored[0]); id != "e:4242-0-1-v2" {
			t.Fatalf("msg id = %q, want e:4242-0-1-v2", id)
		}
		if got := rg.Stats().Dropped; got != 0 {
			t.Fatalf("dropped = %d, want 0", got)
		}
	})
}
```

**Step 5: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on -run EditInsert ./apps/core/internal/reconcile/..."`
Expected: FAIL: `stored = [], want one edit record` (reader còn drop `EditInserted` như kind lạ).

**Step 6: Code reader**

`apps/core/internal/reconcile/term.go`, trong `forward` thay:

```go
	if c.Kind != store.MessageInserted && c.Kind != store.RoomInserted {
```

bằng:

```go
	if !work.KnownKind(c.Kind) {
```

**Step 7: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/work/... ./apps/core/internal/reconcile/... ./apps/core/internal/effects/... ./apps/core/internal/resync/..."`
Expected: PASS (effects/resync dựng `work.Record` bằng literal có khoá, không đổi). `wc -l apps/core/internal/reconcile/harness_test.go` ≤ 175.

**Step 8: Commit**

INDEXES.csv:
- dòng `apps/core/internal/work`: thay `Record holds only kind + room/thread/seq + CommittedAt in 33 big-endian bytes` bằng `Record holds only kind + room/thread/seq + version + CommittedAt in 37 big-endian bytes; KnownKind lists the change kinds the stream carries (message, room, edit)`; thay `natural ids m:{room}-{thread}-{seq} and r:{room} become Nats-Msg-Id` bằng `natural ids m:{room}-{thread}-{seq}, r:{room} and e:{room}-{thread}-{seq}-v{version} become Nats-Msg-Id`; key symbols thay `Record;RecordSize;` bằng `Record;RecordSize;KnownKind;`; cột decisions thay `D66;D79;D80` bằng `D66;D79;D80;D84`.
- dòng `apps/core/internal/reconcile`: thay `turns every MessageInserted/RoomInserted change into a key-only work record` bằng `turns every MessageInserted/RoomInserted/EditInserted change (work.KnownKind) into a key-only work record`.

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/reconcile/edit_forward_test.go
git commit -m "feat(work): carry the edit version in work records and forward edit changes" -- apps/core/internal/work/ apps/core/internal/reconcile/ INDEXES.csv
```

---

#### Ghi chú cho controller (part A)

**Tinh chỉnh hợp đồng (Go chính xác):**

1. `ApplyEdit`/`ClearHistory` không thêm vào `store.Messages`/`store.Rooms`, mà nằm ở hai interface riêng trong `store/ports.go`:
   ```go
   type MessageEditor interface { ApplyEdit(ctx context.Context, e domain.Edit) error }
   type HistoryClearer interface { ClearHistory(ctx context.Context, room uint64, user string, seq uint64) (uint64, error) }
   ```
   `Edits`/`Hidden` đúng chữ ký hợp đồng. Bên cài: `(*memstore.Messages).ApplyEdit`, `(*memstore.Rooms).ClearHistory`, `memstore.NewEdits() *memstore.Edits`, `memstore.NewHidden() *memstore.Hidden`; `*mongostore.Store` cài cả `MessageEditor`, `HistoryClearer`, `Edits`, `Hidden` (assertion ở `mongostore.go`). Các tên part B dùng đều khớp. `mutate.Messages` (Find/Last/ApplyEdit) được `*memstore.Messages` và `*mongostore.Store` thoả. `mutate.HistoryClearer` giống hệt `store.HistoryClearer`: part B có thể dùng thẳng `store.HistoryClearer`.
2. Thêm ngoài hợp đồng: `store.MaxEditScan = 1000`, `store.EditKeyOf(e domain.Edit) MsgKey`, `store.ValidateEdit(e domain.Edit) error`, `store.ValidateLimit(limit, maxLimit int) error`, `work.KnownKind(k store.ChangeKind) bool`, `storetest.RunEdits`, `storetest.RunEditFeed`, `storetest.EditableMessages`, `storetest.ClearableRooms`.
3. `ValidateEdit` (mọi adapter gọi trong `Append`/`ApplyEdit`) từ chối bằng `ErrInvalidArgument`: khoá sai, `Version == 0` hoặc `> math.MaxInt32` (vì `messages.v` là int32 theo hợp đồng BSON), kind khác `EditText`/`EditDelete`, `Prev != ""` khi không phải (`EditText` và `Version == 1`). **Part B (mutate):** coi `BaseVersion >= math.MaxInt32` là `ErrVersionConflict` trước khi `Append` (chặn tràn int32, không chỉ uint32); chỉ đặt `Prev = msg.Text` khi `Kind == EditText && Version == 1`; fact xoá không bao giờ mang `Prev`.
4. Hợp đồng nói "upsert `hidden`": Mongo cài bằng `InsertOne` + index unique, bỏ qua duplicate key (insert-or-ignore). Ngữ nghĩa giữ nguyên.

**Áp các chỉnh sửa của controller:**
- (1) `Prev` chỉ trên fact sửa v1: Task 1 (văn xuôi), `store.ValidateEdit` + test (Task 3), fixture `deletion` xoá `Prev`, `factsInvalid` có hai case `prev` (Task 3).
- (2) `pbconv.Message` đặt `edited_at` nil khi `EditedAt` zero (`optionalTime`); Task 2 Step 7 nêu rõ `TestHistoryPagesThroughWhatWasSent`.
- (3) `TouchActivity` chỉ nâng `ls`/`lm` khi `Thread == 0 && Seq > 0`: memstore `withActivity` ở Task 3, mongostore `activityFields` ở Task 4, case contract `an activity with seq 0 bumps only the last change` ở `storetest/activity_cases.go` (Task 3). Giữa Task 3 và 4 case này sẽ fail trên Mongo, nhưng `make itest` chỉ chạy cuối Task 4. Part B Task 11: `store.Activity{Room, Thread, Seq: 0, At}` cho `EditInserted` là đúng hợp đồng.
- (4) Tên dùng đúng như yêu cầu, không lệch.

**Caller đã cập nhật:**
- `memstore.NewFeed` thành 3 tham số: `memstore/feed_test.go` (3 chỗ), `memstore/memstore_test.go`, `reconcile/harness_test.go`.
- `tools/internal/route/fakes_test.go`: `fakeCore` nhúng `chatimv1.CoreServiceClient` (Task 2). Task 13 (part C) phải cài trên `fakeCore` các RPC mới mà route client gọi, nếu không sẽ panic do nil interface.
- `memstore.appendRoom` đổi thành `appendFact(logged)` (Task 5).
- `mongostore.ensureMessages` đổi thành `ensureClustered(name)`. Text của `ErrNotClustered` đổi thành `mongostore: collection is not clustered on _id`.
- `assertMessagesLayout` đổi thành `assertClusteredLayout(t, db, name)`.
- `storetest.sameMessage` so `EditedAt` bằng `Equal`.

**Thứ tự / lint:**
- Lint `exhaustive` buộc `work.RecordOf` có case `EditInserted` ngay ở Task 5 (chỉ khoá). Task 6 thêm `Version` và `KnownKind`.
- Task 5: reader còn drop `EditInserted` (đếm `Dropped`). Task 6: reader chuyển tiếp record. Từ Task 6 tới Task 11, worker ack record edit mà không chạy effect nào.
- Deploy dev: record 33 byte còn trong `CHATIM_WORK` bị Term thành bản ghi hỏng.

**Sự thật part B/C cần biết:**
- `edits.At` không có → lỗi bọc `store.ErrEditNotFound` (dùng `errors.Is`). `Append` trùng → `store.ErrEditExists`.
- `Latest` trả `(Edit{}, false, nil)` khi chưa có fact. `History` trả nil/rỗng khi hết. `HiddenIn` trả nil khi không có gì (dùng `len`).
- Sau `PurgeText(key, v-1)` các fact ≤ v−1 có `Text == ""` và `Prev == ""`. Retry sửa (bước 6 của mutate) trên tin đã xoá phải dừng ở kiểm "đã xoá" (bước 2) trước khi so text.
- `ApplyEdit` Mongo ghi `d: false` rõ ràng khi sửa, `x: ""` khi xoá; tin không tồn tại → no-op (không upsert). `Find`/`Page`/`Last` đọc `Version`/`Deleted`/`EditedAt`. Projection đã để `Text == ""` cho tin xoá, nên `view.MaskDeleted` chỉ là lớp bảo hiểm.
- `Hidden` không bao giờ được lưu: memstore xoá cờ khi insert, Mongo không có field.
- `Between`: lọc `{r, ts ∈ [from, to]}` (hai đầu đều tính), sort `ts, _id`, `limit ≤ MaxEditScan`. Mongo dùng index `{r:1, ts:1}` rồi sort top-k trên các doc trong khoảng. Resync (Task 12) nên đi theo cửa sổ thời gian ngắn. Khi phân trang bằng `from = last.At`, phải bỏ các fact cùng `ts` có khoá ≤ fact cuối. Nếu hơn `limit` fact cùng một ms trong một room thì không phân trang được (chấp nhận, ghi nhận).
- `ClearHistory(seq = 0)` trả giá trị hiện tại (Mongo có thể ghi `cb: 0` lần đầu). `seq > MaxInt64` → `ErrInvalidArgument` trên Mongo. Room không tồn tại → `ErrNotMember`.
- Collection `message_edits`/`hidden` dùng read primary, read concern local. `Find` trên `messages` vẫn là majority.
- `pbconv.MessageVersions` chỉ thêm phần tử version 0 (`ORIGINAL`, text = `Prev`) khi `after == 0` và fact đầu là v1. Tin bị xoá ở v1 (không có `Prev`) sẽ cho version 0 với text rỗng: grpcsrv (Task 9/10) phải trả rỗng cho tin đã xoá như hợp đồng.
- `pbconv.MessageEdited/Deleted`: `Id` lấy khoá từ fact, envelope room/thread/seq lấy từ snapshot `m`.

**Rủi ro:**
- Task 4: `$or` + `$set` trên `messages` là write mới đầu tiên vào collection đã có (shard-ready: filter có `_id`). Nên reviewer kiểm không upsert và CAS đúng chiều.
- `INDEXES.csv` của owner đang có thay đổi chưa commit (các dòng `docs/research` → `docs/archive/research`). Task 0 dừng nếu còn, vì commit theo pathspec sẽ cuốn chúng vào.

**Câu hỏi chưa giải quyết:**
- Index `{r:1, ts:1}` có nên thành `{r:1, ts:1, _id:1}` để `Between` sort hoàn toàn bằng index không? Hiện giữ đúng D70/hợp đồng.

### Task 7: `access` (actions, `Admit`/`Allow`, `DefaultPolicy`) + package `mutate` — `Edit` và `Delete`

Lệnh đổi không đi qua actor (D82): `mutate.Mutator` vào room qua `access.Checker.Admit` (tenant + membership), đọc tin, hỏi policy qua `Checker.Allow` với `Author = msg.From` (D86: quyền chỉ do `access.Policy` quyết, core không có luật tác giả/owner), đọc **fact cuối** (`Edits.Latest`, một reverse scan), kiểm trạng thái, rồi insert fact `version = base + 1` (khoá `_id` là CAS, D62/D63). Duplicate key → đọc fact ở version đó: cùng `By` + `Kind` + `Text` là retry thành công, khác là `ErrVersionConflict`. Sau fact: projection `Messages.ApplyEdit` (CAS theo version), xoá thì `Edits.PurgeText(key, v−1)` (D75), đọc lại snapshot, enqueue `msg_edited`/`msg_deleted` (lỗi enqueue bỏ qua, worker `msg_changed` của Task 11 gửi bù) rồi mới trả (D64: ack sau projection).

`access` (D86): `Request` thêm `Author`; `Checker` tách `Admit` (room → `CheckTenant` → membership, không hỏi policy) và `Allow` (hỏi policy), `Authorize` = `Admit` rồi `Allow` (chữ ký giữ nguyên, `GetHistory` không đổi). `DefaultPolicy` thay `AllowMembers` làm mặc định của `NewChecker(rooms, nil)` và của actor (`router_new.go`): `EditMessage`/`DeleteMessage` với `Author != User` → `ErrDenied`, mọi action khác cho phép, nên `SendMessage`/`ReadHistory` giữ nguyên hành vi. Owner room cũng bị từ chối khi xoá tin người khác; muốn cho phép thì cắm policy riêng (module policy chat Phase 2).

Thứ tự kiểm trong `apply` + `commit` (bước 1–6 của hợp đồng, viết lại cho retry):
0. `Mutator.target`: `Admit` → `Find` tin (không có → `ErrMessageNotFound`) → `Allow(Author = msg.From)`; từ chối → `access.ErrDenied`. Policy được hỏi **trước** nhận diện retry: tác giả gửi lại vẫn được phép, còn người bị từ chối không dò được trạng thái tin. `Hide` (Task 8) dùng lại `target`.
1. `BaseVersion >= MaxInt32` → `ErrVersionConflict` (không cộng tràn; `messages.v` lưu int32 nên store từ chối version > `MaxInt32`, xem Task 3–4).
2. Fact cuối có `Version == BaseVersion + 1` và trùng `(By, Kind, Text)` của lệnh → **retry**: dùng chính fact đã lưu (giữ `At` cũ, nên event giống hệt lần đầu và JetStream bỏ trùng theo id), đi thẳng tới projection. Bước này đứng **trước** kiểm "đã xoá", để retry của một lệnh xoá đã thắng vẫn thành công.
3. `msg.Deleted` hoặc fact cuối là `EditDelete` → `ErrMessageDeleted`.
4. `BaseVersion != max(msg.Version, latest.Version)` → `ErrVersionConflict`.
5. `Append`; `ErrEditExists` (một lệnh khác thắng giữa `Latest` và `Append`) → `At(version)` → trùng là retry, khác là conflict.

`Prev` chỉ ghi cho fact **sửa** version 1 (tinh chỉnh hợp đồng): fact **xoá** v1 mà mang `Prev = text gốc` thì `PurgeText(key, 0)` không dọn được nó và text gốc sống mãi trong `message_edits`, trái D75.

Thêm `pbconv.MessageChanged` (file mới, không đụng file của Task 2) để `mutate` và effect `msg_changed` (Task 11) dựng event cùng một chỗ: fact `EditDelete` → `MessageDeleted`, còn lại `MessageEdited`.

Giả định từ part A (Task 1–3): `domain.Edit`, `domain.EditText/EditDelete`, các lỗi `domain.ErrMessage*`, `store.Edits`, `store.Hidden`, `store.ErrEditExists`, `store.MaxEditPage`; `pbconv.MessageEdited/MessageDeleted/MessageChangeEventID`; memstore có `memstore.NewEdits() *memstore.Edits` (cài `store.Edits`), `memstore.NewHidden() *memstore.Hidden` (cài `store.Hidden`), `(*memstore.Messages).ApplyEdit` và `(*memstore.Rooms).ClearHistory`. Nếu part A đặt tên constructor memstore khác, chỉ sửa các dòng tạo rig trong test (`fixtures_test.go` của `mutate`, `harness_test.go` của `grpcsrv`, `edit_fixtures_test.go` của `effects`).

**Files:**
- Modify: `apps/core/internal/access/policy.go`
- Modify: `apps/core/internal/access/checker.go`
- Modify: `apps/core/internal/access/checker_test.go`
- Create: `apps/core/internal/access/default_policy_test.go`
- Modify: `apps/core/internal/actor/router_new.go`
- Create: `apps/core/internal/pbconv/change_event.go`
- Create: `apps/core/internal/pbconv/change_event_test.go`
- Create: `apps/core/internal/mutate/mutator.go`
- Create: `apps/core/internal/mutate/change.go`
- Create: `apps/core/internal/mutate/fixtures_test.go`
- Create: `apps/core/internal/mutate/edit_test.go`
- Create: `apps/core/internal/mutate/delete_test.go`
- Create: `apps/core/internal/mutate/policy_test.go`
- Modify: `INDEXES.csv`

**Step 1: Test `access` — `Admit`, `Allow`, `DefaultPolicy`**

`apps/core/internal/access/checker_test.go`, thay nguyên hàm `TestNilPolicyAllowsMembers` bằng:

```go
func TestNilPolicyUsesTheDefaultPolicy(t *testing.T) {
	c, err := access.NewChecker(rooms(t), nil)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	req, err := c.Authorize(t.Context(), access.ReadHistory, "acme", "alice", room)
	if err != nil || req.Member.User != "alice" {
		t.Fatalf("Authorize = %+v, %v; want alice allowed", req, err)
	}
	del, err := c.Admit(t.Context(), access.DeleteMessage, "acme", "alice", room)
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	del.Author = "bob"
	if err := c.Allow(t.Context(), del); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("owner alice deletes bob's message = %v, want ErrDenied", err)
	}
	if _, err := access.NewChecker(nil, nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("NewChecker(nil) = %v, want ErrInvalidArgument", err)
	}
}

func TestAdmitChecksTheRoomWithoutThePolicy(t *testing.T) {
	asked := 0
	deny := access.PolicyFunc(func(context.Context, access.Request) error { asked++; return access.ErrDenied })
	c, err := access.NewChecker(rooms(t), deny)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	req, err := c.Admit(t.Context(), access.EditMessage, "acme", "alice", room)
	if err != nil || asked != 0 {
		t.Fatalf("Admit = %v after %d policy calls, want nil after 0", err, asked)
	}
	if req.Action != access.EditMessage || req.User != "alice" || req.Author != "" || req.Room.ID != room || req.Member.Role != domain.RoleOwner {
		t.Fatalf("Admit = %+v, want edit_message by owner alice without an author", req)
	}
	if _, err := c.Admit(t.Context(), access.EditMessage, "acme", "mallory", room); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("Admit stranger = %v, want ErrPermissionDenied", err)
	}
}

func TestAllowPassesTheRequestWithTheAuthor(t *testing.T) {
	var got access.Request
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error { got = r; return access.ErrDenied })
	c, err := access.NewChecker(rooms(t), deny)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	req, err := c.Admit(t.Context(), access.DeleteMessage, "acme", "alice", room)
	if err != nil {
		t.Fatalf("Admit: %v", err)
	}
	req.Author = "bob"
	if err := c.Allow(t.Context(), req); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("Allow = %v, want ErrPermissionDenied", err)
	}
	if got.Action != access.DeleteMessage || got.User != "alice" || got.Author != "bob" || got.Room.ID != room || got.Member.Role != domain.RoleOwner {
		t.Fatalf("policy saw %+v, want delete_message by owner alice on bob's message", got)
	}
}
```

`apps/core/internal/access/default_policy_test.go`:

```go
package access_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
)

func TestDefaultPolicyLetsOnlyTheAuthorEditOrDelete(t *testing.T) {
	cases := []struct {
		action       access.Action
		user, author string
		want         error
	}{
		{access.EditMessage, "alice", "alice", nil},
		{access.DeleteMessage, "alice", "alice", nil},
		{access.EditMessage, "alice", "bob", access.ErrDenied},
		{access.DeleteMessage, "alice", "bob", access.ErrDenied},
		{access.DeleteMessage, "alice", "", access.ErrDenied},
		{access.ReadHistory, "alice", "", nil},
		{access.SendMessage, "alice", "", nil},
		{access.ClearHistory, "alice", "", nil},
		{access.HideMessage, "alice", "bob", nil},
		{access.ReadEditHistory, "alice", "bob", nil},
	}
	for _, tc := range cases {
		err := access.DefaultPolicy{}.Check(t.Context(), access.Request{Action: tc.action, User: tc.user, Author: tc.author})
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s by %s on %q's message = %v, want %v", tc.action, tc.user, tc.author, err, tc.want)
		}
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/access/..."`
Expected: FAIL biên dịch: `undefined: access.DeleteMessage`, `undefined: access.DefaultPolicy`, `c.Admit undefined (type *access.Checker has no field or method Admit)`, `unknown field Author in struct literal of type access.Request`.

**Step 3: Code `access` + mặc định của actor**

`apps/core/internal/access/policy.go` (thay cả file):

```go
package access

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type Action string

const (
	ReadHistory     Action = "read_history"
	SendMessage     Action = "send_message"
	EditMessage     Action = "edit_message"
	DeleteMessage   Action = "delete_message"
	HideMessage     Action = "hide_message"
	ClearHistory    Action = "clear_history"
	ReadEditHistory Action = "read_edit_history"
)

var ErrDenied = fmt.Errorf("action denied: %w", apperr.ErrPermissionDenied)

type Request struct {
	Action Action
	User   string
	Author string
	Room   domain.Room
	Member domain.Member
}

type Policy interface {
	Check(ctx context.Context, req Request) error
}

type PolicyFunc func(ctx context.Context, req Request) error

func (f PolicyFunc) Check(ctx context.Context, req Request) error { return f(ctx, req) }

type AllowMembers struct{}

func (AllowMembers) Check(context.Context, Request) error { return nil }

type DefaultPolicy struct{}

func (DefaultPolicy) Check(_ context.Context, req Request) error {
	if (req.Action == EditMessage || req.Action == DeleteMessage) && req.Author != req.User {
		return ErrDenied
	}
	return nil
}
```

`apps/core/internal/access/checker.go`, trong `NewChecker` thay `policy = AllowMembers{}` bằng `policy = DefaultPolicy{}`, rồi thay nguyên hàm `Authorize` bằng:

```go
func (c *Checker) Admit(ctx context.Context, action Action, tenant, user string, room uint64) (Request, error) {
	r, err := c.rooms.Get(ctx, room)
	if err != nil {
		return Request{}, err
	}
	if err := domain.CheckTenant(r, tenant); err != nil {
		return Request{}, err
	}
	m, err := c.rooms.Member(ctx, room, user)
	if err != nil {
		return Request{}, err
	}
	return Request{Action: action, User: user, Room: r, Member: m}, nil
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
```

`apps/core/internal/actor/router_new.go`: thay `policy:  access.AllowMembers{},` bằng `policy:  access.DefaultPolicy{},`. Actor chỉ hỏi `SendMessage`, policy mặc định cho phép, nên test actor không đổi.

**Step 4: Chạy, thấy pass + commit**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/access/... ./apps/core/internal/actor/... ./apps/core/internal/grpcsrv/..."`
Expected: PASS (`grpcsrv` `GetHistory` vẫn gọi `Authorize`, policy mặc định cho `ReadHistory`). `wc -l apps/core/internal/access/*.go` mỗi file < 200.

`INDEXES.csv`, dòng `apps/core/internal/access`:
- purpose thay "AllowMembers is the default policy; used by SendMessage in the actor and GetHistory in grpcsrv" bằng "Checker.Admit checks tenant and membership without the policy; Checker.Allow asks the policy; Authorize does both; Request.Author is the author of the target message; DefaultPolicy (default of NewChecker and the actor) lets only the author edit or delete and allows every other action (D86); AllowMembers allows every member; used by SendMessage in the actor; GetHistory/GetEditHistory in grpcsrv and the change commands in mutate";
- key_symbols thay `Action;Request;Policy;PolicyFunc;AllowMembers;Rooms;Checker;NewChecker;Checker.Authorize` bằng `Action;EditMessage;DeleteMessage;HideMessage;ClearHistory;ReadEditHistory;Request;Policy;PolicyFunc;AllowMembers;DefaultPolicy;Rooms;Checker;NewChecker;Checker.Admit;Checker.Allow;Checker.Authorize`;
- used_by thêm `;apps/core/internal/mutate`; decisions `D65;D77` → `D65;D77;D86`.

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/access/default_policy_test.go
git commit -m "feat(access): let only the author edit or delete by default" -- apps/core/internal/access/ apps/core/internal/actor/router_new.go INDEXES.csv
```

**Step 5: Test `pbconv.MessageChanged`**

`apps/core/internal/pbconv/change_event_test.go`:

```go
package pbconv_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
)

func TestMessageChangedPicksTheEventByFactKind(t *testing.T) {
	m := sample()
	edit := domain.Edit{Room: m.Room, Seq: m.Seq, Version: 1, Kind: domain.EditText, Tenant: "acme", By: "alice", Text: "sửa", Prev: m.Text, At: sentAt}
	if got, want := pbconv.MessageChanged(domain.RoomGroup, m, edit), pbconv.MessageEdited(domain.RoomGroup, m, edit); !proto.Equal(got, want) {
		t.Fatalf("edit fact: got %v, want %v", got, want)
	}
	del := domain.Edit{Room: m.Room, Seq: m.Seq, Version: 2, Kind: domain.EditDelete, Tenant: "acme", By: "alice", At: sentAt}
	if got, want := pbconv.MessageChanged(domain.RoomDM, m, del), pbconv.MessageDeleted(domain.RoomDM, m, del); !proto.Equal(got, want) {
		t.Fatalf("delete fact: got %v, want %v", got, want)
	}
}
```

**Step 6: Test `mutate`**

`apps/core/internal/mutate/fixtures_test.go`:

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
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
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
	m      *mutate.Mutator
	msgs   *memstore.Messages
	rooms  *memstore.Rooms
	edits  *memstore.Edits
	hidden *memstore.Hidden
	events *recordingEvents
	now    time.Time
}

func newRig(t *testing.T, policy access.Policy) *rig {
	t.Helper()
	rg := &rig{
		msgs: memstore.NewMessages(), rooms: memstore.NewRooms(), edits: memstore.NewEdits(), hidden: memstore.NewHidden(),
		events: &recordingEvents{}, now: created.Add(time.Minute + 1500*time.Microsecond),
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
	rg.m = rg.mutator(t, policy, rg.edits)
	return rg
}

func (rg *rig) mutator(t *testing.T, policy access.Policy, edits store.Edits) *mutate.Mutator {
	t.Helper()
	checker, err := access.NewChecker(rg.rooms, policy)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	m, err := mutate.New(mutate.Deps{
		Access: checker, Messages: rg.msgs, Edits: edits, Hidden: rg.hidden, Rooms: rg.rooms, Events: rg.events,
		Now: func() time.Time { return rg.now },
	})
	if err != nil {
		t.Fatalf("mutate.New: %v", err)
	}
	return m
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

`apps/core/internal/mutate/edit_test.go`:

```go
package mutate_test

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type laggingEdits struct{ *memstore.Edits }

func (laggingEdits) Latest(context.Context, store.MsgKey) (domain.Edit, bool, error) {
	return domain.Edit{}, false, nil
}

func TestEditWritesVersionOneWithTheOriginalText(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hello")
	got, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "hello there"))
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if got.Version != 1 || got.Text != "hello there" || got.Deleted || !got.EditedAt.Equal(rg.at()) {
		t.Fatalf("snapshot = %+v, want version 1 with the new text edited at %v", got, rg.at())
	}
	if s := rg.stored(t, 1); s.Version != 1 || s.Text != "hello there" {
		t.Fatalf("stored = %+v, want the projection applied before the ack", s)
	}
	want := domain.Edit{Room: room, Seq: 1, Version: 1, Kind: domain.EditText, Tenant: tenant, By: "alice", Text: "hello there", Prev: "hello", At: rg.at()}
	if facts := rg.facts(t, 1); len(facts) != 1 || !sameEdit(facts[0], want) {
		t.Fatalf("facts = %+v, want %+v", facts, want)
	}
	rooms, events := rg.events.list()
	if !slices.Equal(rooms, []uint64{room}) || len(events) != 1 || !proto.Equal(events[0], pbconv.MessageEdited(domain.RoomGroup, got, want)) {
		t.Fatalf("enqueued %v %v, want one msg_edited for room %d", rooms, events, room)
	}
	if id := events[0].GetId(); id != pbconv.MessageChangeEventID(room, 0, 1, 1) {
		t.Fatalf("event id = %q", id)
	}
}

func TestEditsBuildOnTheVersionTheClientSaw(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "v0")
	if _, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "v1")); err != nil {
		t.Fatalf("first edit: %v", err)
	}
	got, err := rg.m.Edit(t.Context(), edit("alice", 1, 1, "v2"))
	if err != nil || got.Version != 2 || got.Text != "v2" {
		t.Fatalf("second edit = %+v, %v; want version 2", got, err)
	}
	facts := rg.facts(t, 1)
	if len(facts) != 2 || facts[0].Prev != "v0" || facts[1].Version != 2 || facts[1].Prev != "" {
		t.Fatalf("facts = %+v, want v1 with prev v0 and v2 without prev", facts)
	}
	if _, events := rg.events.list(); len(events) != 2 || events[1].GetId() != pbconv.MessageChangeEventID(room, 0, 1, 2) {
		t.Fatalf("events = %v, want a second event with the v2 id", events)
	}
}

func TestEditWithAStaleBaseConflicts(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "v0")
	if _, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "v1")); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	for _, base := range []uint32{0, 2, math.MaxUint32} {
		if _, err := rg.m.Edit(t.Context(), edit("alice", 1, base, "other")); !errors.Is(err, domain.ErrVersionConflict) {
			t.Fatalf("Edit base %d = %v, want ErrVersionConflict", base, err)
		}
	}
	if s := rg.stored(t, 1); s.Version != 1 || s.Text != "v1" {
		t.Fatalf("stored = %+v, want v1 untouched", s)
	}
	if facts := rg.facts(t, 1); len(facts) != 1 {
		t.Fatalf("facts = %+v, want only v1", facts)
	}
}

func TestRetriedEditSucceedsWithoutASecondFact(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "v0")
	first, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "v1"))
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	rg.now = rg.now.Add(time.Second)
	again, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "v1"))
	if err != nil || again.Version != 1 || !again.EditedAt.Equal(first.EditedAt) {
		t.Fatalf("retry = %+v, %v; want the first result", again, err)
	}
	if facts := rg.facts(t, 1); len(facts) != 1 {
		t.Fatalf("facts = %+v, want one", facts)
	}
	if _, events := rg.events.list(); len(events) != 2 || !proto.Equal(events[0], events[1]) {
		t.Fatalf("events = %v, want the retry to enqueue the same event", events)
	}
}

func TestRetryAfterACrashFinishesTheProjection(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "v0")
	fact := domain.Edit{Room: room, Seq: 1, Version: 1, Kind: domain.EditText, Tenant: tenant, By: "alice", Text: "v1", Prev: "v0", At: created.Add(time.Second)}
	if err := rg.edits.Append(t.Context(), fact); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "v1"))
	if err != nil || got.Version != 1 || got.Text != "v1" || !got.EditedAt.Equal(fact.At) {
		t.Fatalf("retry = %+v, %v; want the stored fact projected", got, err)
	}
}

func TestDuplicateVersionIsARetryOnlyForTheSameChange(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "v0")
	rg.m = rg.mutator(t, nil, laggingEdits{rg.edits})
	other := domain.Edit{Room: room, Seq: 1, Version: 1, Kind: domain.EditText, Tenant: tenant, By: "alice", Text: "from another device", Prev: "v0", At: created}
	if err := rg.edits.Append(t.Context(), other); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "mine")); !errors.Is(err, domain.ErrVersionConflict) {
		t.Fatalf("losing edit = %v, want ErrVersionConflict", err)
	}
	got, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "from another device"))
	if err != nil || got.Version != 1 || got.Text != "from another device" {
		t.Fatalf("same change = %+v, %v; want a retry success", got, err)
	}
}

func TestEditRejectsBadInput(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "a")
	thread := edit("alice", 1, 0, "x")
	thread.Thread = 1
	noRoom := edit("alice", 1, 0, "x")
	noRoom.Room = 0
	for name, c := range map[string]mutate.EditCmd{
		"empty text": edit("alice", 1, 0, ""), "blank text": edit("alice", 1, 0, " \n "),
		"seq zero": edit("alice", 0, 0, "x"), "thread": thread, "room zero": noRoom,
	} {
		if _, err := rg.m.Edit(t.Context(), c); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: Edit = %v, want ErrInvalidArgument", name, err)
		}
	}
}

```

`apps/core/internal/mutate/delete_test.go`:

```go
package mutate_test

import (
	"errors"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestDeletePurgesTheTextOfEarlierVersions(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "v0")
	for _, c := range []mutate.EditCmd{edit("alice", 1, 0, "v1"), edit("alice", 1, 1, "v2")} {
		if _, err := rg.m.Edit(t.Context(), c); err != nil {
			t.Fatalf("Edit base %d: %v", c.BaseVersion, err)
		}
	}
	got, err := rg.m.Delete(t.Context(), del("alice", 1, 2))
	if err != nil || !got.Deleted || got.Text != "" || got.Version != 3 {
		t.Fatalf("Delete = %+v, %v; want deleted at version 3", got, err)
	}
	facts := rg.facts(t, 1)
	if len(facts) != 3 || facts[2].Kind != domain.EditDelete {
		t.Fatalf("facts = %+v, want v1, v2 and a delete", facts)
	}
	for _, f := range facts {
		if f.Text != "" || f.Prev != "" {
			t.Fatalf("fact v%d keeps text %q prev %q after delete", f.Version, f.Text, f.Prev)
		}
	}
	if _, events := rg.events.list(); len(events) != 3 || !proto.Equal(events[2], pbconv.MessageDeleted(domain.RoomGroup, got, facts[2])) {
		t.Fatalf("last event = %v, want msg_deleted v3", events)
	}
}

func TestNothingChangesADeletedMessage(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "v0")
	if _, err := rg.m.Delete(t.Context(), del("alice", 1, 0)); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := rg.m.Edit(t.Context(), edit("alice", 1, 1, "back")); !errors.Is(err, domain.ErrMessageDeleted) {
		t.Fatalf("edit after delete = %v, want ErrMessageDeleted", err)
	}
	if _, err := rg.m.Delete(t.Context(), del("alice", 1, 1)); !errors.Is(err, domain.ErrMessageDeleted) {
		t.Fatalf("second delete = %v, want ErrMessageDeleted", err)
	}
	if got, err := rg.m.Delete(t.Context(), del("alice", 1, 0)); err != nil || !got.Deleted {
		t.Fatalf("retried delete = %+v, %v; want success", got, err)
	}
	if facts := rg.facts(t, 1); len(facts) != 1 || facts[0].Kind != domain.EditDelete || facts[0].Prev != "" {
		t.Fatalf("facts = %+v, want only the delete, without the original text", facts)
	}
}

func TestChangesOfAnUnknownMessage(t *testing.T) {
	rg := newRig(t, nil)
	if _, err := rg.m.Edit(t.Context(), edit("alice", 9, 0, "x")); !errors.Is(err, domain.ErrMessageNotFound) {
		t.Fatalf("Edit = %v, want ErrMessageNotFound", err)
	}
	if _, err := rg.m.Delete(t.Context(), del("alice", 9, 0)); !errors.Is(err, domain.ErrMessageNotFound) {
		t.Fatalf("Delete = %v, want ErrMessageNotFound", err)
	}
}

func TestARefusedEventDoesNotFailTheChange(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "v0")
	rg.events.err = errBoom
	if got, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "v1")); err != nil || got.Version != 1 {
		t.Fatalf("Edit with a refused event = %+v, %v; want success", got, err)
	}
}

func TestNewRequiresEveryDependency(t *testing.T) {
	rg := newRig(t, nil)
	checker, err := access.NewChecker(rg.rooms, nil)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	full := mutate.Deps{Access: checker, Messages: rg.msgs, Edits: rg.edits, Hidden: rg.hidden, Rooms: rg.rooms, Events: rg.events}
	for name, drop := range map[string]func(d *mutate.Deps){
		"no access":   func(d *mutate.Deps) { d.Access = nil },
		"no messages": func(d *mutate.Deps) { d.Messages = nil },
		"no edits":    func(d *mutate.Deps) { d.Edits = nil },
		"no hidden":   func(d *mutate.Deps) { d.Hidden = nil },
		"no rooms":    func(d *mutate.Deps) { d.Rooms = nil },
		"no events":   func(d *mutate.Deps) { d.Events = nil },
	} {
		d := full
		drop(&d)
		if _, err := mutate.New(d); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: New = %v, want ErrInvalidArgument", name, err)
		}
	}
	if _, err := mutate.New(full); err != nil {
		t.Fatalf("New with a default clock: %v", err)
	}
}
```

`apps/core/internal/mutate/policy_test.go`:

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

func TestTheDefaultPolicyLetsOnlyTheAuthorChangeAMessage(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "a")
	rg.send(t, 2, "bob", "b")
	for _, c := range []mutate.EditCmd{edit("bob", 1, 0, "x"), edit("alice", 2, 0, "x")} {
		if _, err := rg.m.Edit(t.Context(), c); !errors.Is(err, access.ErrDenied) || !errors.Is(err, apperr.ErrPermissionDenied) {
			t.Fatalf("%s edits seq %d = %v, want ErrDenied", c.User, c.Seq, err)
		}
	}
	for _, c := range []mutate.DeleteCmd{del("bob", 1, 0), del("carol", 2, 0), del("alice", 2, 0)} {
		if _, err := rg.m.Delete(t.Context(), c); !errors.Is(err, access.ErrDenied) {
			t.Fatalf("%s deletes seq %d = %v, want ErrDenied even for the room owner", c.User, c.Seq, err)
		}
	}
	if len(rg.facts(t, 1))+len(rg.facts(t, 2)) != 0 {
		t.Fatalf("a refused change wrote a fact")
	}
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("a refused change enqueued %v", events)
	}
	if got, err := rg.m.Delete(t.Context(), del("bob", 2, 0)); err != nil || !got.Deleted || got.Version != 1 {
		t.Fatalf("author delete = %+v, %v; want a deleted snapshot at version 1", got, err)
	}
}

func TestAPolicyCanLetTheRoomOwnerDeleteAnyMessage(t *testing.T) {
	ownerOrAuthor := access.PolicyFunc(func(_ context.Context, r access.Request) error {
		if r.Action != access.DeleteMessage || r.Member.Role == domain.RoleOwner || r.Author == r.User {
			return nil
		}
		return access.ErrDenied
	})
	rg := newRig(t, ownerOrAuthor)
	rg.send(t, 1, "bob", "b")
	rg.send(t, 2, "carol", "c")
	if _, err := rg.m.Delete(t.Context(), del("bob", 2, 0)); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("bob deletes carol's message = %v, want ErrDenied", err)
	}
	got, err := rg.m.Delete(t.Context(), del("alice", 1, 0))
	if err != nil || !got.Deleted || got.Text != "" || got.Version != 1 {
		t.Fatalf("owner delete = %+v, %v; want a deleted snapshot at version 1", got, err)
	}
	if again, err := rg.m.Delete(t.Context(), del("alice", 1, 0)); err != nil || !again.Deleted {
		t.Fatalf("owner retry = %+v, %v; want success", again, err)
	}
	_, events := rg.events.list()
	if len(events) != 2 || events[0].GetActor() != "alice" || events[0].GetMessageDeleted() == nil {
		t.Fatalf("events = %v, want msg_deleted by alice", events)
	}
}

func TestChangesAskThePolicyWithTheAuthor(t *testing.T) {
	var asked []access.Request
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error { asked = append(asked, r); return access.ErrDenied })
	rg := newRig(t, deny)
	rg.send(t, 1, "bob", "b")
	if _, err := rg.m.Edit(t.Context(), edit("alice", 9, 0, "x")); !errors.Is(err, domain.ErrMessageNotFound) {
		t.Fatalf("Edit of a missing message = %v, want ErrMessageNotFound", err)
	}
	if _, err := rg.m.Edit(t.Context(), edit("alice", 1, 0, "x")); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("Edit = %v, want PermissionDenied", err)
	}
	if _, err := rg.m.Delete(t.Context(), del("alice", 1, 0)); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("Delete = %v, want PermissionDenied", err)
	}
	if len(asked) != 2 || asked[0].Action != access.EditMessage || asked[1].Action != access.DeleteMessage {
		t.Fatalf("policy asked %+v, want edit_message then delete_message", asked)
	}
	for _, r := range asked {
		if r.User != "alice" || r.Author != "bob" || r.Member.Role != domain.RoleOwner {
			t.Fatalf("policy saw %+v, want owner alice on bob's message", r)
		}
	}
	open := newRig(t, nil)
	open.send(t, 1, "alice", "a")
	if _, err := open.m.Edit(t.Context(), edit("mallory", 1, 0, "x")); !errors.Is(err, domain.ErrNotMember) {
		t.Fatalf("stranger Edit = %v, want ErrNotMember", err)
	}
	other := edit("alice", 1, 0, "x")
	other.Room = 999
	if _, err := open.m.Edit(t.Context(), other); !errors.Is(err, domain.ErrRoomNotFound) {
		t.Fatalf("unknown room Edit = %v, want ErrRoomNotFound", err)
	}
}
```

**Step 7: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/mutate/... ./apps/core/internal/pbconv/..."`
Expected: FAIL biên dịch: `no required module provides package .../internal/mutate` (hoặc `undefined: mutate.New`), `undefined: pbconv.MessageChanged`.

**Step 8: Code**

`apps/core/internal/pbconv/change_event.go`:

```go
package pbconv

import (
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func MessageChanged(roomType domain.RoomType, m domain.Message, e domain.Edit) *chatimv1.Event {
	if e.Kind == domain.EditDelete {
		return MessageDeleted(roomType, m, e)
	}
	return MessageEdited(roomType, m, e)
}
```

`apps/core/internal/mutate/mutator.go`:

```go
package mutate

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var errMissingDeps = fmt.Errorf("%w: mutator needs access, messages, edits, hidden, rooms and events", apperr.ErrInvalidArgument)

type Messages interface {
	Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error)
	Last(ctx context.Context, room, thread uint64) (uint64, error)
	ApplyEdit(ctx context.Context, e domain.Edit) error
}

type HistoryClearer interface {
	ClearHistory(ctx context.Context, room uint64, user string, seq uint64) (uint64, error)
}

type EventPublisher interface {
	Enqueue(room uint64, events []*chatimv1.Event) error
}

type Deps struct {
	Access   *access.Checker
	Messages Messages
	Edits    store.Edits
	Hidden   store.Hidden
	Rooms    HistoryClearer
	Events   EventPublisher
	Now      func() time.Time
}

type EditCmd struct {
	Tenant, User      string
	Room, Thread, Seq uint64
	BaseVersion       uint32
	Text              string
}

type DeleteCmd struct {
	Tenant, User      string
	Room, Thread, Seq uint64
	BaseVersion       uint32
}

type Mutator struct {
	d Deps
}

func New(d Deps) (*Mutator, error) {
	if d.Access == nil || d.Messages == nil || d.Edits == nil || d.Hidden == nil || d.Rooms == nil || d.Events == nil {
		return nil, errMissingDeps
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Mutator{d: d}, nil
}

func (m *Mutator) now() time.Time { return m.d.Now().UTC().Truncate(time.Millisecond) }

func (m *Mutator) find(ctx context.Context, key store.MsgKey) (domain.Message, error) {
	found, err := m.d.Messages.Find(ctx, key.Room, []store.MsgKey{key})
	if err != nil {
		return domain.Message{}, err
	}
	if len(found) == 0 {
		return domain.Message{}, domain.ErrMessageNotFound
	}
	return found[0], nil
}

func (m *Mutator) target(ctx context.Context, action access.Action, tenant, user string, key store.MsgKey) (access.Request, domain.Message, error) {
	req, err := m.d.Access.Admit(ctx, action, tenant, user, key.Room)
	if err != nil {
		return access.Request{}, domain.Message{}, err
	}
	msg, err := m.find(ctx, key)
	if err != nil {
		return access.Request{}, domain.Message{}, err
	}
	req.Author = msg.From
	if err := m.d.Access.Allow(ctx, req); err != nil {
		return access.Request{}, domain.Message{}, err
	}
	return req, msg, nil
}

func validKey(key store.MsgKey) error {
	if err := key.Validate(); err != nil {
		return err
	}
	return domain.ValidateThread(key.Thread)
}
```

`apps/core/internal/mutate/change.go`:

```go
package mutate

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type change struct {
	action       access.Action
	tenant, user string
	key          store.MsgKey
	base         uint32
	kind         domain.EditKind
	text         string
}

func (m *Mutator) Edit(ctx context.Context, c EditCmd) (domain.Message, error) {
	if err := domain.ValidateText(c.Text); err != nil {
		return domain.Message{}, err
	}
	return m.apply(ctx, change{
		action: access.EditMessage, tenant: c.Tenant, user: c.User,
		key: store.MsgKey{Room: c.Room, Thread: c.Thread, Seq: c.Seq}, base: c.BaseVersion, kind: domain.EditText, text: c.Text,
	})
}

func (m *Mutator) Delete(ctx context.Context, c DeleteCmd) (domain.Message, error) {
	return m.apply(ctx, change{
		action: access.DeleteMessage, tenant: c.Tenant, user: c.User,
		key: store.MsgKey{Room: c.Room, Thread: c.Thread, Seq: c.Seq}, base: c.BaseVersion, kind: domain.EditDelete,
	})
}

func (m *Mutator) apply(ctx context.Context, c change) (domain.Message, error) {
	if err := validKey(c.key); err != nil {
		return domain.Message{}, err
	}
	grant, msg, err := m.target(ctx, c.action, c.tenant, c.user, c.key)
	if err != nil {
		return domain.Message{}, err
	}
	fact, err := m.commit(ctx, c, msg)
	if err != nil {
		return domain.Message{}, err
	}
	if err := m.project(ctx, fact); err != nil {
		return domain.Message{}, err
	}
	snap, err := m.find(ctx, c.key)
	if err != nil {
		return domain.Message{}, err
	}
	_ = m.d.Events.Enqueue(c.key.Room, []*chatimv1.Event{pbconv.MessageChanged(grant.Room.Type, snap, fact)})
	return snap, nil
}

func (m *Mutator) commit(ctx context.Context, c change, msg domain.Message) (domain.Edit, error) {
	if c.base >= math.MaxInt32 {
		return domain.Edit{}, domain.ErrVersionConflict
	}
	next := c.base + 1
	latest, found, err := m.d.Edits.Latest(ctx, c.key)
	if err != nil {
		return domain.Edit{}, err
	}
	switch {
	case found && latest.Version == next && c.matches(latest):
		return latest, nil
	case msg.Deleted || (found && latest.Kind == domain.EditDelete):
		return domain.Edit{}, domain.ErrMessageDeleted
	case c.base != max(msg.Version, latest.Version):
		return domain.Edit{}, domain.ErrVersionConflict
	}
	fact := c.fact(msg, next, m.now())
	switch err := m.d.Edits.Append(ctx, fact); {
	case errors.Is(err, store.ErrEditExists):
		return m.recognize(ctx, c, next)
	case err != nil:
		return domain.Edit{}, err
	}
	return fact, nil
}

func (m *Mutator) recognize(ctx context.Context, c change, version uint32) (domain.Edit, error) {
	got, err := m.d.Edits.At(ctx, c.key, version)
	if err != nil {
		return domain.Edit{}, err
	}
	if !c.matches(got) {
		return domain.Edit{}, domain.ErrVersionConflict
	}
	return got, nil
}

func (m *Mutator) project(ctx context.Context, f domain.Edit) error {
	if err := m.d.Messages.ApplyEdit(ctx, f); err != nil {
		return err
	}
	if f.Kind != domain.EditDelete {
		return nil
	}
	return m.d.Edits.PurgeText(ctx, store.MsgKey{Room: f.Room, Thread: f.Thread, Seq: f.Seq}, f.Version-1)
}

func (c change) matches(e domain.Edit) bool {
	return e.By == c.user && e.Kind == c.kind && e.Text == c.text
}

func (c change) fact(msg domain.Message, version uint32, at time.Time) domain.Edit {
	f := domain.Edit{
		Room: c.key.Room, Thread: c.key.Thread, Seq: c.key.Seq, Version: version, Kind: c.kind,
		Tenant: c.tenant, By: c.user, Text: c.text, At: at,
	}
	if version == 1 && c.kind == domain.EditText {
		f.Prev = msg.Text
	}
	return f
}
```

**Step 9: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/mutate/... ./apps/core/internal/pbconv/... ./apps/core/internal/access/..."`
Expected: PASS. `wc -l apps/core/internal/mutate/*.go` mỗi file < 200.

Không có goroutine mới (Mutator đồng bộ, an toàn khi gọi song song vì mọi trạng thái nằm ở store), nên không cần `-count=5` hay synctest.

**Step 10: INDEXES + commit**

`INDEXES.csv`:
- Dòng `apps/core/internal/pbconv`: key_symbols thêm `MessageChanged`; purpose nối "; MessageChanged picks msg_deleted for a delete fact (else msg_edited)".
- Thêm dòng mới ngay sau dòng `apps/core/internal/metrics`:

```csv
apps/core/internal/mutate,package,"Change commands outside the actor (D82): Edit/Delete admit through access.Checker (tenant and membership), read the message, ask access.Policy with the message author (default: author only; no author or owner rule in mutate, D86), read the last edit fact, recognize a retry (fact at base+1 with the same by/kind/text, also on a duplicate version from Append), refuse a deleted message and a stale base, insert the fact (version base+1, v1 carries prev), project onto messages (CAS by version), purge older fact text on delete, re-read the snapshot and enqueue msg_edited/msg_deleted best effort before answering",Mutator;New;Deps;Messages;HistoryClearer;EventPublisher;EditCmd;DeleteCmd;Mutator.Edit;Mutator.Delete,apps/core/internal/grpcsrv;apps/core,unit,D62;D63;D64;D75;D82;D86
```

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/pbconv/change_event.go apps/core/internal/pbconv/change_event_test.go apps/core/internal/mutate/
git commit -m "feat(mutate): edit and delete messages through immutable facts" -- apps/core/internal/pbconv/change_event.go apps/core/internal/pbconv/change_event_test.go apps/core/internal/mutate/ INDEXES.csv
```

Task rủi ro: một reviewer, soát đúng luồng `apply` + `commit` (thứ tự Admit → Find → policy với `Author` → retry → xoá → base; không còn luật tác giả/owner trong `mutate`, D86), `DefaultPolicy` chỉ chặn sửa/xoá khi `Author != User`, `PurgeText` chỉ chạy sau `ApplyEdit` của fact xoá, và lỗi enqueue không làm hỏng lệnh.

---

### Task 8: `mutate` — `Hide` và `ClearHistory`

Ẩn và clear là giá trị theo người đọc (§4, §6.4): không fact bất biến, không event (owner 2026-10-05), chỉ áp ở reader pipeline (Task 10). `Hide`: `Mutator.target` của Task 7 (`Admit(HideMessage)` → tin phải tồn tại, kể cả đã xoá → `Allow` với `Author` = tác giả tin; `DefaultPolicy` cho phép ẩn tin của bất kỳ ai, D86), rồi `Hidden.Hide` (upsert, idempotent). `ClearHistory` là action theo room: `Authorize(ClearHistory)` (không có `Author`), đọc `Messages.Last(room, 0)` và **kẹp** `UpToSeq` về `last` (0 hoặc lớn hơn `last` → `last`), rồi `Rooms.ClearHistory` (`$max`, trả giá trị sau cập nhật). Kẹp là tinh chỉnh so với hợp đồng: không kẹp thì một client gửi `up_to_seq` quá lớn sẽ ẩn luôn các tin **tương lai** của chính mình mà không có cách gỡ (mốc chỉ tăng).

**Files:**
- Create: `apps/core/internal/mutate/hide_clear.go`
- Create: `apps/core/internal/mutate/hide_clear_test.go`
- Modify: `INDEXES.csv`

**Step 1: Test**

`apps/core/internal/mutate/hide_clear_test.go`:

```go
package mutate_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestHideNeedsAnExistingMessage(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	hide := func(user string, seq uint64) error {
		return rg.m.Hide(t.Context(), mutate.HideCmd{Tenant: tenant, User: user, Room: room, Seq: seq})
	}
	for range 2 {
		if err := hide("bob", 1); err != nil {
			t.Fatalf("Hide: %v", err)
		}
	}
	if seqs, err := rg.hidden.HiddenIn(t.Context(), "bob", room, 0, 1, 1); err != nil || !slices.Equal(seqs, []uint64{1}) {
		t.Fatalf("bob hidden = %v, %v; want [1]", seqs, err)
	}
	if seqs, err := rg.hidden.HiddenIn(t.Context(), "alice", room, 0, 1, 1); err != nil || len(seqs) != 0 {
		t.Fatalf("alice hidden = %v, %v; want none", seqs, err)
	}
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"unknown message", hide("bob", 9), domain.ErrMessageNotFound},
		{"not a member", hide("mallory", 1), domain.ErrNotMember},
		{"seq zero", hide("bob", 0), apperr.ErrInvalidArgument},
	}
	for _, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Fatalf("%s: Hide = %v, want %v", c.name, c.err, c.want)
		}
	}
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("hide enqueued %v, want no event", events)
	}
}

func TestClearHistoryOnlyRaisesTheMark(t *testing.T) {
	rg := newRig(t, nil)
	for seq := range uint64(3) {
		rg.send(t, seq+1, "alice", "m")
	}
	clearTo := func(user string, upTo uint64) uint64 {
		t.Helper()
		n, err := rg.m.ClearHistory(t.Context(), mutate.ClearCmd{Tenant: tenant, User: user, Room: room, UpToSeq: upTo})
		if err != nil {
			t.Fatalf("ClearHistory(%s, %d): %v", user, upTo, err)
		}
		return n
	}
	cases := []struct {
		user       string
		upTo, want uint64
	}{
		{"bob", 0, 3},
		{"bob", 1, 3},
		{"alice", 2, 2},
		{"carol", 99, 3},
	}
	for _, c := range cases {
		if got := clearTo(c.user, c.upTo); got != c.want {
			t.Fatalf("ClearHistory(%s, %d) = %d, want %d", c.user, c.upTo, got, c.want)
		}
	}
	m, err := rg.rooms.Member(t.Context(), room, "bob")
	if err != nil || m.ClearedBeforeSeq != 3 {
		t.Fatalf("bob member = %+v, %v; want cleared before 3", m, err)
	}
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("clear enqueued %v, want no event", events)
	}
}

func TestClearHistoryOfAnEmptyRoomKeepsZero(t *testing.T) {
	rg := newRig(t, nil)
	n, err := rg.m.ClearHistory(t.Context(), mutate.ClearCmd{Tenant: tenant, User: "bob", Room: room})
	if err != nil || n != 0 {
		t.Fatalf("ClearHistory = %d, %v; want 0", n, err)
	}
}

func TestHideAndClearAskThePolicy(t *testing.T) {
	var asked []access.Request
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error { asked = append(asked, r); return access.ErrDenied })
	rg := newRig(t, deny)
	rg.send(t, 1, "alice", "hi")
	if err := rg.m.Hide(t.Context(), mutate.HideCmd{Tenant: tenant, User: "bob", Room: room, Seq: 1}); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("Hide = %v, want PermissionDenied", err)
	}
	if _, err := rg.m.ClearHistory(t.Context(), mutate.ClearCmd{Tenant: tenant, User: "bob", Room: room}); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("ClearHistory = %v, want PermissionDenied", err)
	}
	if len(asked) != 2 || asked[0].Action != access.HideMessage || asked[0].Author != "alice" || asked[1].Action != access.ClearHistory || asked[1].Author != "" {
		t.Fatalf("policy asked %+v, want hide_message on alice's message, then clear_history without an author", asked)
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/mutate/..."`
Expected: FAIL biên dịch: `undefined: mutate.HideCmd`, `undefined: mutate.ClearCmd`, `rg.m.Hide undefined`.

**Step 3: Code**

`apps/core/internal/mutate/hide_clear.go`:

```go
package mutate

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type HideCmd struct {
	Tenant, User      string
	Room, Thread, Seq uint64
}

type ClearCmd struct {
	Tenant, User string
	Room         uint64
	UpToSeq      uint64
}

func (m *Mutator) Hide(ctx context.Context, c HideCmd) error {
	key := store.MsgKey{Room: c.Room, Thread: c.Thread, Seq: c.Seq}
	if err := validKey(key); err != nil {
		return err
	}
	if _, _, err := m.target(ctx, access.HideMessage, c.Tenant, c.User, key); err != nil {
		return err
	}
	return m.d.Hidden.Hide(ctx, c.User, key)
}

func (m *Mutator) ClearHistory(ctx context.Context, c ClearCmd) (uint64, error) {
	if _, err := m.d.Access.Authorize(ctx, access.ClearHistory, c.Tenant, c.User, c.Room); err != nil {
		return 0, err
	}
	last, err := m.d.Messages.Last(ctx, c.Room, 0)
	if err != nil {
		return 0, err
	}
	seq := c.UpToSeq
	if seq == 0 || seq > last {
		seq = last
	}
	return m.d.Rooms.ClearHistory(ctx, c.Room, c.User, seq)
}
```

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/mutate/..."`
Expected: PASS.

**Step 5: INDEXES + commit**

`INDEXES.csv`, dòng `apps/core/internal/mutate`: purpose nối "; Hide (admit, message must exist, ask the policy with the author; upsert into hidden) and ClearHistory (UpToSeq 0 or past the last seq clamps to Messages.Last; $max on the member; returns the new mark) are per-reader values with no fact and no event"; key_symbols thêm `HideCmd;ClearCmd;Mutator.Hide;Mutator.ClearHistory`.

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/mutate/hide_clear.go apps/core/internal/mutate/hide_clear_test.go
git commit -m "feat(mutate): hide a message and clear history per reader" -- apps/core/internal/mutate/hide_clear.go apps/core/internal/mutate/hide_clear_test.go INDEXES.csv
```

---

### Task 9: `grpcsrv` — 5 RPC mới + wiring `apps/core`

Năm handler mỏng: `callerOf` → `parseRoomID` (gộp trong `callerAndRoom`) → `Mutator`. `GetEditHistory` là đường đọc: kiểm khoá + `limit` (`domain.PageLimit`: 0 → 50, tối đa 100 = `store.MaxEditPage`) → `Admit(ReadEditHistory)` (A7) → `Find` tin (không có → `NotFound`) → `Allow` với `Author` = tác giả tin (D86; `DefaultPolicy` cho phép member) → tin đã xoá trả danh sách rỗng → `Edits.History(key, after, limit)` → `pbconv.MessageVersions`. Mã lỗi đi qua `pkg/grpcserver` như cũ: `ErrVersionConflict`/`ErrMessageDeleted` → `FailedPrecondition`, `access.ErrDenied`/`domain.ErrNotMember` → `PermissionDenied`, `ErrMessageNotFound` → `NotFound`.

`grpcsrv.Deps` thêm `Mutator *mutate.Mutator`, `Edits store.Edits`, `Hidden store.Hidden` (bắt buộc; thiếu → `errMissingDeps`). `Hidden` chỉ được kiểm ở `New` trong task này và được lưu vào `Service` ở Task 10 (tránh field không đọc bị `unused` báo). `PageReader` thêm `Find` (memstore và mongostore đều có sẵn).

Wiring: `access.Checker` của `mutate` dựng riêng trong `apps/core/service_wiring.go` bằng `access.NewChecker(st, nil)`, tức `access.DefaultPolicy` (Task 7), cùng mặc định với checker trong `grpcsrv` và actor. `DefaultPolicy` cho phép `ReadHistory`/`SendMessage`, nên `GetHistory` và gửi tin không đổi hành vi; chỉ sửa/xoá tin người khác bị từ chối. Tách file để `wiring.go` không phình; `wiring.go` bỏ import `grpcsrv`.

**Files:**
- Modify: `apps/core/internal/grpcsrv/core_service.go`
- Create: `apps/core/internal/grpcsrv/change_message.go`
- Create: `apps/core/internal/grpcsrv/edit_history.go`
- Modify: `apps/core/internal/grpcsrv/harness_test.go`
- Modify: `apps/core/internal/grpcsrv/fake_dependencies_test.go`
- Modify: `apps/core/internal/grpcsrv/caller_identity_test.go`
- Create: `apps/core/internal/grpcsrv/change_message_test.go`
- Create: `apps/core/internal/grpcsrv/edit_history_test.go`
- Create: `apps/core/service_wiring.go`
- Modify: `apps/core/wiring.go`
- Modify: `INDEXES.csv`

**Step 1: Harness**

`apps/core/internal/grpcsrv/harness_test.go`:
- Trong `sentinels`, sau dòng `codes.InvalidArgument:   apperr.ErrInvalidArgument.Error(),` thêm `codes.FailedPrecondition: apperr.ErrFailedPrecondition.Error(),` (gofmt căn lại cột).
- Thay

```go
type rig struct {
	client chatimv1.CoreServiceClient
	rooms  *memstore.Rooms
	msgs   *memstore.Messages
}
```

bằng

```go
type rig struct {
	client chatimv1.CoreServiceClient
	rooms  *memstore.Rooms
	msgs   *memstore.Messages
	edits  *memstore.Edits
	hidden *memstore.Hidden
}
```

- Trong `newRig`, thay

```go
	rg := &rig{rooms: memstore.NewRooms(), msgs: memstore.NewMessages()}
	if o.sender == nil {
		o.sender = startRouter(t, rg)
	}
	svc, err := grpcsrv.New(grpcsrv.Deps{Sender: o.sender, Rooms: rg.rooms, Pages: rg.msgs, NewID: o.newID, Now: o.now, Policy: o.policy, Events: o.events}, quiet)
```

bằng

```go
	rg := &rig{rooms: memstore.NewRooms(), msgs: memstore.NewMessages(), edits: memstore.NewEdits(), hidden: memstore.NewHidden()}
	if o.sender == nil {
		o.sender = startRouter(t, rg)
	}
	svc, err := grpcsrv.New(grpcsrv.Deps{
		Sender: o.sender, Rooms: rg.rooms, Pages: rg.msgs, NewID: o.newID, Now: o.now, Policy: o.policy, Events: o.events,
		Mutator: newMutator(t, rg, o), Edits: rg.edits, Hidden: rg.hidden,
	}, quiet)
```

`apps/core/internal/grpcsrv/fake_dependencies_test.go`: import thêm `"testing"`, `".../internal/access"`, `".../internal/mutate"`; cuối file thêm:

```go
func newMutator(t *testing.T, rg *rig, o options) *mutate.Mutator {
	t.Helper()
	checker, err := access.NewChecker(rg.rooms, o.policy)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	var events mutate.EventPublisher = nopPublisher{}
	if o.events != nil {
		events = o.events
	}
	m, err := mutate.New(mutate.Deps{Access: checker, Messages: rg.msgs, Edits: rg.edits, Hidden: rg.hidden, Rooms: rg.rooms, Events: events, Now: o.now})
	if err != nil {
		t.Fatalf("mutate.New: %v", err)
	}
	return m
}
```

**Step 2: Test**

`apps/core/internal/grpcsrv/caller_identity_test.go`, thay toàn bộ `TestNewRequiresEveryDependency` bằng:

```go
func TestNewRequiresEveryDependency(t *testing.T) {
	rg := &rig{rooms: memstore.NewRooms(), msgs: memstore.NewMessages(), edits: memstore.NewEdits(), hidden: memstore.NewHidden()}
	full := grpcsrv.Deps{Sender: &fakeSender{}, Rooms: rg.rooms, Pages: rg.msgs, Mutator: newMutator(t, rg, options{}), Edits: rg.edits, Hidden: rg.hidden}
	for name, drop := range map[string]func(d *grpcsrv.Deps){
		"no sender":  func(d *grpcsrv.Deps) { d.Sender = nil },
		"no rooms":   func(d *grpcsrv.Deps) { d.Rooms = nil },
		"no pages":   func(d *grpcsrv.Deps) { d.Pages = nil },
		"no mutator": func(d *grpcsrv.Deps) { d.Mutator = nil },
		"no edits":   func(d *grpcsrv.Deps) { d.Edits = nil },
		"no hidden":  func(d *grpcsrv.Deps) { d.Hidden = nil },
	} {
		deps := full
		drop(&deps)
		if _, err := grpcsrv.New(deps, quiet); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: New = %v, want ErrInvalidArgument", name, err)
		}
	}
	if _, err := grpcsrv.New(full, nil); err != nil {
		t.Errorf("New with defaults = %v, want nil", err)
	}
}
```

Trong `TestEveryRPCChecksCallerIdentityFirst`, map `rpcs` thêm sau `"GetHistory"`:

```go
		"EditMessage": func(ctx context.Context) error {
			_, err := rg.client.EditMessage(ctx, &chatimv1.EditMessageRequest{RoomId: "42", Seq: 1, Text: "hi"})
			return err
		},
		"DeleteMessage": func(ctx context.Context) error {
			_, err := rg.client.DeleteMessage(ctx, &chatimv1.DeleteMessageRequest{RoomId: "42", Seq: 1})
			return err
		},
		"HideMessage": func(ctx context.Context) error {
			_, err := rg.client.HideMessage(ctx, &chatimv1.HideMessageRequest{RoomId: "42", Seq: 1})
			return err
		},
		"ClearHistory": func(ctx context.Context) error {
			_, err := rg.client.ClearHistory(ctx, &chatimv1.ClearHistoryRequest{RoomId: "42"})
			return err
		},
		"GetEditHistory": func(ctx context.Context) error {
			_, err := rg.client.GetEditHistory(ctx, &chatimv1.GetEditHistoryRequest{RoomId: "42", Seq: 1})
			return err
		},
```

`apps/core/internal/grpcsrv/change_message_test.go`:

```go
package grpcsrv_test

import (
	"context"
	"slices"
	"strconv"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func roomNumber(t *testing.T, room string) uint64 {
	t.Helper()
	id, err := strconv.ParseUint(room, 10, 64)
	if err != nil {
		t.Fatalf("room id %q: %v", room, err)
	}
	return id
}

func TestEditAndDeleteThroughTheService(t *testing.T) {
	events := &recordingEvents{}
	rg := newRig(t, options{events: events})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice := as(t, "acme", "alice")
	rg.send(t, alice, room, "c-1", "hi")
	edited, err := rg.client.EditMessage(alice, &chatimv1.EditMessageRequest{RoomId: room, Seq: 1, Text: "hello"})
	if err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	if m := edited.GetMessage(); m.GetVersion() != 1 || m.GetText() != "hello" || m.GetEditedAt() == nil || m.GetDeleted() {
		t.Fatalf("edited = %v, want version 1 with the new text", m)
	}
	deleted, err := rg.client.DeleteMessage(alice, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 1, BaseVersion: 1})
	if err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if m := deleted.GetMessage(); !m.GetDeleted() || m.GetText() != "" || m.GetVersion() != 2 {
		t.Fatalf("deleted = %v, want a deleted message at version 2", m)
	}
	id := roomNumber(t, room)
	_, got := events.enqueued()
	var ids []string
	for _, ev := range got {
		ids = append(ids, ev.GetId())
	}
	want := []string{pbconv.RoomCreatedEventID(id), pbconv.MessageChangeEventID(id, 0, 1, 1), pbconv.MessageChangeEventID(id, 0, 1, 2)}
	if !slices.Equal(ids, want) {
		t.Fatalf("enqueued %v, want %v", ids, want)
	}
}

func TestChangeErrorsKeepTheirCodes(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	rg.send(t, alice, room, "c-1", "hi")
	if _, err := rg.client.EditMessage(alice, &chatimv1.EditMessageRequest{RoomId: room, Seq: 1, Text: "v1"}); err != nil {
		t.Fatalf("EditMessage: %v", err)
	}
	edit := func(ctx context.Context, req *chatimv1.EditMessageRequest) func() error {
		return func() error { _, err := rg.client.EditMessage(ctx, req); return err }
	}
	cases := []struct {
		name string
		call func() error
		code codes.Code
	}{
		{"stale base", edit(alice, &chatimv1.EditMessageRequest{RoomId: room, Seq: 1, Text: "v2"}), codes.FailedPrecondition},
		{"not the author", edit(bob, &chatimv1.EditMessageRequest{RoomId: room, Seq: 1, BaseVersion: 1, Text: "v2"}), codes.PermissionDenied},
		{"unknown message", edit(alice, &chatimv1.EditMessageRequest{RoomId: room, Seq: 9, Text: "v2"}), codes.NotFound},
		{"other tenant", edit(as(t, "other", "alice"), &chatimv1.EditMessageRequest{RoomId: room, Seq: 1, BaseVersion: 1, Text: "v2"}), codes.NotFound},
		{"bad room id", edit(alice, &chatimv1.EditMessageRequest{RoomId: "x", Seq: 1, Text: "v2"}), codes.InvalidArgument},
		{"empty text", edit(alice, &chatimv1.EditMessageRequest{RoomId: room, Seq: 1, BaseVersion: 1}), codes.InvalidArgument},
		{"thread", edit(alice, &chatimv1.EditMessageRequest{RoomId: room, ThreadRoot: 1, Seq: 1, BaseVersion: 1, Text: "v2"}), codes.InvalidArgument},
		{"member deletes another's message", func() error {
			_, err := rg.client.DeleteMessage(bob, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 1, BaseVersion: 1})
			return err
		}, codes.PermissionDenied},
		{"hide an unknown message", func() error {
			_, err := rg.client.HideMessage(bob, &chatimv1.HideMessageRequest{RoomId: room, Seq: 9})
			return err
		}, codes.NotFound},
		{"clear by a stranger", func() error {
			_, err := rg.client.ClearHistory(as(t, "acme", "mallory"), &chatimv1.ClearHistoryRequest{RoomId: room})
			return err
		}, codes.PermissionDenied},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { expectCode(t, c.call(), c.code) })
	}
	if _, err := rg.client.DeleteMessage(alice, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 1, BaseVersion: 1}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	expectCode(t, edit(alice, &chatimv1.EditMessageRequest{RoomId: room, Seq: 1, BaseVersion: 2, Text: "back"})(), codes.FailedPrecondition)
}

func TestHideAndClearHistoryThroughTheService(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	rg.send(t, alice, room, "c-1", "a")
	rg.send(t, alice, room, "c-2", "b")
	if _, err := rg.client.HideMessage(bob, &chatimv1.HideMessageRequest{RoomId: room, Seq: 2}); err != nil {
		t.Fatalf("HideMessage: %v", err)
	}
	if seqs, err := rg.hidden.HiddenIn(t.Context(), "bob", roomNumber(t, room), 0, 1, 2); err != nil || !slices.Equal(seqs, []uint64{2}) {
		t.Fatalf("bob hidden = %v, %v; want [2]", seqs, err)
	}
	cleared, err := rg.client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: room})
	if err != nil || cleared.GetClearedBeforeSeq() != 2 {
		t.Fatalf("ClearHistory = %v, %v; want cleared before 2", cleared, err)
	}
	again, err := rg.client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: room, UpToSeq: 1})
	if err != nil || again.GetClearedBeforeSeq() != 2 {
		t.Fatalf("lower ClearHistory = %v, %v; want the mark kept at 2", again, err)
	}
}
```

`apps/core/internal/grpcsrv/edit_history_test.go`:

```go
package grpcsrv_test

import (
	"context"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type versionRow struct {
	version  uint32
	kind     chatimv1.EditKind
	text, by string
}

func versionRows(vs []*chatimv1.MessageVersion) []versionRow {
	out := make([]versionRow, len(vs))
	for i, v := range vs {
		out[i] = versionRow{v.GetVersion(), v.GetKind(), v.GetText(), v.GetBy()}
	}
	return out
}

func (rg *rig) editTwice(t *testing.T, ctx context.Context, room string) {
	t.Helper()
	for _, e := range []struct {
		base uint32
		text string
	}{{0, "v1"}, {1, "v2"}} {
		if _, err := rg.client.EditMessage(ctx, &chatimv1.EditMessageRequest{RoomId: room, Seq: 1, BaseVersion: e.base, Text: e.text}); err != nil {
			t.Fatalf("EditMessage(%s): %v", e.text, err)
		}
	}
}

func TestEditHistoryListsEveryVersion(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	sent := rg.send(t, alice, room, "c-1", "v0")
	rg.editTwice(t, alice, room)
	resp, err := rg.client.GetEditHistory(bob, &chatimv1.GetEditHistoryRequest{RoomId: room, Seq: 1})
	if err != nil {
		t.Fatalf("GetEditHistory: %v", err)
	}
	want := []versionRow{
		{0, chatimv1.EditKind_EDIT_KIND_ORIGINAL, "v0", "alice"},
		{1, chatimv1.EditKind_EDIT_KIND_TEXT, "v1", "alice"},
		{2, chatimv1.EditKind_EDIT_KIND_TEXT, "v2", "alice"},
	}
	if got := versionRows(resp.GetVersions()); !slices.Equal(got, want) {
		t.Fatalf("versions = %+v, want %+v", got, want)
	}
	if at := resp.GetVersions()[0].GetAt(); !proto.Equal(at, sent.GetCreatedAt()) {
		t.Fatalf("original at = %v, want the send time %v", at, sent.GetCreatedAt())
	}
	after, err := rg.client.GetEditHistory(bob, &chatimv1.GetEditHistoryRequest{RoomId: room, Seq: 1, AfterVersion: 1})
	if err != nil || !slices.Equal(versionRows(after.GetVersions()), want[2:]) {
		t.Fatalf("after v1 = %v, %v; want only v2", after, err)
	}
}

func TestEditHistoryOfADeletedMessageIsEmpty(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice")
	alice := as(t, "acme", "alice")
	rg.send(t, alice, room, "c-1", "v0")
	rg.editTwice(t, alice, room)
	if _, err := rg.client.DeleteMessage(alice, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 1, BaseVersion: 2}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	resp, err := rg.client.GetEditHistory(alice, &chatimv1.GetEditHistoryRequest{RoomId: room, Seq: 1})
	if err != nil || len(resp.GetVersions()) != 0 {
		t.Fatalf("GetEditHistory = %v, %v; want no versions", resp, err)
	}
}

func TestEditHistoryChecksInputAndAccess(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice")
	rg.send(t, as(t, "acme", "alice"), room, "c-1", "v0")
	cases := []struct {
		name   string
		tenant string
		user   string
		req    *chatimv1.GetEditHistoryRequest
		code   codes.Code
	}{
		{"unknown message", "acme", "alice", &chatimv1.GetEditHistoryRequest{RoomId: room, Seq: 9}, codes.NotFound},
		{"other tenant", "other", "alice", &chatimv1.GetEditHistoryRequest{RoomId: room, Seq: 1}, codes.NotFound},
		{"not a member", "acme", "mallory", &chatimv1.GetEditHistoryRequest{RoomId: room, Seq: 1}, codes.PermissionDenied},
		{"seq zero", "acme", "alice", &chatimv1.GetEditHistoryRequest{RoomId: room}, codes.InvalidArgument},
		{"thread", "acme", "alice", &chatimv1.GetEditHistoryRequest{RoomId: room, ThreadRoot: 1, Seq: 1}, codes.InvalidArgument},
		{"limit over 100", "acme", "alice", &chatimv1.GetEditHistoryRequest{RoomId: room, Seq: 1, Limit: 101}, codes.InvalidArgument},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := rg.client.GetEditHistory(as(t, c.tenant, c.user), c.req)
			expectCode(t, err, c.code)
		})
	}
}

func TestEditHistoryAsksThePolicyWithTheAuthor(t *testing.T) {
	var got access.Request
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error { got = r; return access.ErrDenied })
	rg := newRig(t, options{policy: deny})
	room := rg.createGroup(t, "acme", "alice", "bob")
	rg.send(t, as(t, "acme", "bob"), room, "c-1", "v0")
	_, err := rg.client.GetEditHistory(as(t, "acme", "alice"), &chatimv1.GetEditHistoryRequest{RoomId: room, Seq: 1})
	expectCode(t, err, codes.PermissionDenied)
	if got.Action != access.ReadEditHistory || got.User != "alice" || got.Author != "bob" {
		t.Fatalf("policy saw %+v, want read_edit_history by alice on bob's message", got)
	}
}
```

**Step 3: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/grpcsrv/..."`
Expected: FAIL biên dịch: `unknown field Mutator in struct literal of type grpcsrv.Deps` (và `Edits`, `Hidden`).

**Step 4: Code**

`apps/core/internal/grpcsrv/core_service.go` (thay toàn bộ file):

```go
package grpcsrv

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/view"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	errMissingDeps = fmt.Errorf("%w: core service needs a sender, a room store, a page reader, a mutator, an edit store and a hidden store", apperr.ErrInvalidArgument)
	errBadRoomID   = fmt.Errorf("%w: room id", apperr.ErrInvalidArgument)
)

type Sender interface {
	Send(ctx context.Context, c actor.SendCmd) (actor.Ack, error)
}

type PageReader interface {
	Page(ctx context.Context, q store.PageQuery) ([]domain.Message, error)
	Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error)
}

type EventPublisher interface {
	Enqueue(room uint64, events []*chatimv1.Event) error
}

type noEvents struct{}

func (noEvents) Enqueue(uint64, []*chatimv1.Event) error { return nil }

type Deps struct {
	Sender  Sender
	Rooms   store.Rooms
	Pages   PageReader
	Policy  access.Policy
	Events  EventPublisher
	Mutator *mutate.Mutator
	Edits   store.Edits
	Hidden  store.Hidden
	NewID   func() uint64
	Now     func() time.Time
}

type Service struct {
	chatimv1.UnimplementedCoreServiceServer
	sender  Sender
	rooms   store.Rooms
	pages   PageReader
	events  EventPublisher
	mutator *mutate.Mutator
	edits   store.Edits
	access  *access.Checker
	view    view.Pipeline
	newID   func() uint64
	now     func() time.Time
	log     *slog.Logger
}

var _ chatimv1.CoreServiceServer = (*Service)(nil)

func New(d Deps, log *slog.Logger) (*Service, error) {
	if d.Sender == nil || d.Rooms == nil || d.Pages == nil || d.Mutator == nil || d.Edits == nil || d.Hidden == nil {
		return nil, errMissingDeps
	}
	if d.NewID == nil {
		d.NewID = ids.NewRoomID
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Events == nil {
		d.Events = noEvents{}
	}
	if log == nil {
		log = slog.Default()
	}
	checker, err := access.NewChecker(d.Rooms, d.Policy)
	if err != nil {
		return nil, err
	}
	return &Service{
		sender: d.Sender, rooms: d.Rooms, pages: d.Pages, events: d.Events, mutator: d.Mutator, edits: d.Edits,
		access: checker, view: view.Default(), newID: d.NewID, now: d.Now, log: log,
	}, nil
}

func (s *Service) SendMessage(ctx context.Context, req *chatimv1.SendMessageRequest) (*chatimv1.SendMessageResponse, error) {
	who, err := callerOf(ctx)
	if err != nil {
		return nil, err
	}
	room, err := parseRoomID(req.GetRoomId())
	if err != nil {
		return nil, err
	}
	ack, err := s.sender.Send(ctx, actor.SendCmd{
		Tenant: who.tenant, User: who.user, Room: room, Thread: req.GetThreadRoot(), CID: req.GetCid(), Text: req.GetText(),
	})
	if err != nil {
		return nil, err
	}
	return &chatimv1.SendMessageResponse{Seq: ack.Seq, CreatedAt: timestamppb.New(ack.CreatedAt)}, nil
}

func parseRoomID(s string) (uint64, error) {
	id, err := ids.ParseRoomID(s)
	if err != nil {
		return 0, errBadRoomID
	}
	return id, nil
}
```

`apps/core/internal/grpcsrv/change_message.go`:

```go
package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) EditMessage(ctx context.Context, req *chatimv1.EditMessageRequest) (*chatimv1.EditMessageResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	m, err := s.mutator.Edit(ctx, mutate.EditCmd{
		Tenant: who.tenant, User: who.user, Room: room, Thread: req.GetThreadRoot(), Seq: req.GetSeq(),
		BaseVersion: req.GetBaseVersion(), Text: req.GetText(),
	})
	if err != nil {
		return nil, err
	}
	return &chatimv1.EditMessageResponse{Message: pbconv.Message(m)}, nil
}

func (s *Service) DeleteMessage(ctx context.Context, req *chatimv1.DeleteMessageRequest) (*chatimv1.DeleteMessageResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	m, err := s.mutator.Delete(ctx, mutate.DeleteCmd{
		Tenant: who.tenant, User: who.user, Room: room, Thread: req.GetThreadRoot(), Seq: req.GetSeq(), BaseVersion: req.GetBaseVersion(),
	})
	if err != nil {
		return nil, err
	}
	return &chatimv1.DeleteMessageResponse{Message: pbconv.Message(m)}, nil
}

func (s *Service) HideMessage(ctx context.Context, req *chatimv1.HideMessageRequest) (*chatimv1.HideMessageResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	err = s.mutator.Hide(ctx, mutate.HideCmd{Tenant: who.tenant, User: who.user, Room: room, Thread: req.GetThreadRoot(), Seq: req.GetSeq()})
	if err != nil {
		return nil, err
	}
	return &chatimv1.HideMessageResponse{}, nil
}

func (s *Service) ClearHistory(ctx context.Context, req *chatimv1.ClearHistoryRequest) (*chatimv1.ClearHistoryResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	n, err := s.mutator.ClearHistory(ctx, mutate.ClearCmd{Tenant: who.tenant, User: who.user, Room: room, UpToSeq: req.GetUpToSeq()})
	if err != nil {
		return nil, err
	}
	return &chatimv1.ClearHistoryResponse{ClearedBeforeSeq: n}, nil
}

func callerAndRoom(ctx context.Context, roomID string) (caller, uint64, error) {
	who, err := callerOf(ctx)
	if err != nil {
		return caller{}, 0, err
	}
	room, err := parseRoomID(roomID)
	if err != nil {
		return caller{}, 0, err
	}
	return who, room, nil
}
```

`apps/core/internal/grpcsrv/edit_history.go`:

```go
package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) GetEditHistory(ctx context.Context, req *chatimv1.GetEditHistoryRequest) (*chatimv1.GetEditHistoryResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	key := store.MsgKey{Room: room, Thread: req.GetThreadRoot(), Seq: req.GetSeq()}
	if err := key.Validate(); err != nil {
		return nil, err
	}
	if err := domain.ValidateThread(key.Thread); err != nil {
		return nil, err
	}
	limit, err := domain.PageLimit(int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	grant, err := s.access.Admit(ctx, access.ReadEditHistory, who.tenant, who.user, room)
	if err != nil {
		return nil, err
	}
	found, err := s.pages.Find(ctx, room, []store.MsgKey{key})
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, domain.ErrMessageNotFound
	}
	grant.Author = found[0].From
	if err := s.access.Allow(ctx, grant); err != nil {
		return nil, err
	}
	if found[0].Deleted {
		return &chatimv1.GetEditHistoryResponse{}, nil
	}
	edits, err := s.edits.History(ctx, key, req.GetAfterVersion(), limit)
	if err != nil {
		return nil, err
	}
	return &chatimv1.GetEditHistoryResponse{Versions: pbconv.MessageVersions(found[0], edits, req.GetAfterVersion())}, nil
}
```

`apps/core/service_wiring.go`:

```go
package main

import (
	"fmt"
	"log/slog"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/grpcsrv"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

func wireService(st *mongostore.Store, router *actor.Router, pub *publish.Publisher, log *slog.Logger) (*grpcsrv.Service, error) {
	checker, err := access.NewChecker(st, nil)
	if err != nil {
		return nil, fmt.Errorf("wire access checker: %w", err)
	}
	mut, err := mutate.New(mutate.Deps{Access: checker, Messages: st, Edits: st, Hidden: st, Rooms: st, Events: pub})
	if err != nil {
		return nil, fmt.Errorf("wire mutator: %w", err)
	}
	svc, err := grpcsrv.New(grpcsrv.Deps{Sender: router, Rooms: st, Pages: st, Events: pub, Mutator: mut, Edits: st, Hidden: st}, log)
	if err != nil {
		return nil, fmt.Errorf("wire core service: %w", err)
	}
	return svc, nil
}
```

`apps/core/wiring.go`:
- Xoá dòng import `"github.com/ivannguyendev/chatim/apps/core/internal/grpcsrv"`.
- Thay

```go
	svc, err := grpcsrv.New(grpcsrv.Deps{Sender: router, Rooms: st, Pages: st, Events: pub}, log)
	if err != nil {
		return nil, fmt.Errorf("wire core service: %w", err)
	}
```

bằng

```go
	svc, err := wireService(st, router, pub, log)
	if err != nil {
		return nil, err
	}
```

Nếu `publish.New`/`actor.NewRouter` trả kiểu khác `*publish.Publisher`/`*actor.Router` (kiểm `make -s go ARGS="doc ./apps/core/internal/publish New"`), đổi đúng kiểu tham số của `wireService`; không đổi gì khác.

**Step 5: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/grpcsrv/... ./apps/core/"`
Expected: PASS (itest `apps/core` skip). `wc -l apps/core/internal/grpcsrv/*.go apps/core/wiring.go apps/core/service_wiring.go` mỗi file < 200 (`harness_test.go` ~181, `wiring.go` ~149).

Chạy thêm `make -s go ARGS="vet ./..."` để chắc `*mongostore.Store` cài đủ `mutate.Messages`, `store.Edits`, `store.Hidden`, `mutate.HistoryClearer`, `access.Rooms` (lỗi biên dịch ở `service_wiring.go` nghĩa là part A đặt method khác hợp đồng: dừng và báo).

**Step 6: Itest**

```bash
make infra-up && make itest
```

Expected: mọi package `ok` (wiring `apps/core` đổi; bootstrap của Task 4 đã tạo `message_edits` và `hidden`).

**Step 7: INDEXES + commit**

`INDEXES.csv`:
- Dòng `apps/core/internal/grpcsrv`: purpose thay "CoreService handlers CreateRoom/SendMessage/GetHistory" bằng "CoreService handlers CreateRoom/SendMessage/GetHistory/EditMessage/DeleteMessage/HideMessage/ClearHistory/GetEditHistory"; nối "; change RPCs are thin calls into mutate.Mutator (callerAndRoom parses caller and room id); GetEditHistory checks the key and limit (PageLimit); admits (access ReadEditHistory), finds the message (missing = NotFound), asks the policy with the message author, deleted = no versions and maps Edits.History with pbconv.MessageVersions; Deps requires Mutator/Edits/Hidden; PageReader has Page and Find"; key_symbols thêm `Service.EditMessage;Service.DeleteMessage;Service.HideMessage;Service.ClearHistory;Service.GetEditHistory`; decisions thêm `;D82;D86`.
- Dòng `apps/core`: purpose nối "; service_wiring.go builds the mutate.Mutator (own access checker with access.DefaultPolicy) and the CoreService"; key_symbols thêm `wireService`.

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/grpcsrv/ apps/core/service_wiring.go apps/core/wiring.go
git commit -m "feat(grpcsrv): serve edit, delete, hide, clear history and edit history" -- apps/core/internal/grpcsrv/ apps/core/service_wiring.go apps/core/wiring.go INDEXES.csv
```

Task rủi ro: một reviewer (mã lỗi ra ngoài chỉ là sentinel, `GetEditHistory` `Admit` trước khi đọc tin và hỏi policy với `Author` sau `Find`, wiring dùng cùng store cho mọi port).

---

### Task 10: `view` — `MaskDeleted` + `HideForViewer`, `GetHistory` dựng `Viewer`

Reader pipeline đủ bốn bước của §9.2 (D85): gộp retry → mặt nạ xoá → ẩn theo người đọc. `MaskDeleted` xoá `Text` của tin `Deleted` (projection đã xoá text, bước này là lưới an toàn cho dữ liệu cũ/adapter khác). `HideForViewer` đặt `Hidden = true`, `Text = ""` cho tin có `Seq ≤ ClearedBeforeSeq` hoặc nằm trong `HiddenSeqs`. Seq, version, cờ giữ nguyên, nên phân trang theo seq không lệch. Không bước nào sửa slice đầu vào.

`GetHistory` dựng `Viewer` từ grant (`Member.ClearedBeforeSeq`) và một lần `Hidden.HiddenIn(user, room, thread, lo, hi)` trên khoảng seq của trang; bỏ qua truy vấn khi trang rỗng hoặc cả trang đã nằm dưới mốc clear.

**Files:**
- Modify: `apps/core/internal/view/pipeline.go`
- Create: `apps/core/internal/view/masks.go`
- Create: `apps/core/internal/view/masks_test.go`
- Modify: `apps/core/internal/grpcsrv/core_service.go`
- Modify: `apps/core/internal/grpcsrv/get_history.go`
- Create: `apps/core/internal/grpcsrv/history_masks_test.go`
- Modify: `INDEXES.csv`

**Step 1: Test**

`apps/core/internal/view/masks_test.go`:

```go
package view_test

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/view"
)

var editedAt = time.Unix(1_700_000_000, 0).UTC()

func textMsg(seq uint64, text string) domain.Message {
	return domain.Message{Room: 7, Seq: seq, From: "alice", Text: text, CID: "c-" + text}
}

func TestMaskDeletedDropsOnlyTheTextOfDeletedMessages(t *testing.T) {
	page := []domain.Message{
		{Room: 7, Seq: 1, From: "alice", Text: "kept", Version: 1, EditedAt: editedAt},
		{Room: 7, Seq: 2, From: "bob", Text: "leftover", Version: 3, Deleted: true, EditedAt: editedAt},
	}
	before := slices.Clone(page)
	want := slices.Clone(page)
	want[1].Text = ""
	if got := view.MaskDeleted(view.Viewer{User: "bob"}, page); !slices.Equal(got, want) {
		t.Fatalf("masked = %+v, want %+v", got, want)
	}
	if !slices.Equal(page, before) {
		t.Fatalf("input page was modified")
	}
}

func TestHideForViewerHidesClearedAndHiddenSeqs(t *testing.T) {
	page := []domain.Message{textMsg(1, "a"), textMsg(2, "b"), textMsg(3, "c"), textMsg(4, "d")}
	before := slices.Clone(page)
	v := view.Viewer{User: "bob", ClearedBeforeSeq: 1, HiddenSeqs: map[uint64]bool{3: true}}
	got := view.HideForViewer(v, page)
	for i, m := range got {
		hidden := m.Seq == 1 || m.Seq == 3
		if m.Hidden != hidden || (m.Text == "") != hidden || m.Seq != page[i].Seq || m.CID != page[i].CID {
			t.Fatalf("message %d = %+v, want hidden=%v with seq and cid kept", i, m, hidden)
		}
	}
	if !slices.Equal(page, before) {
		t.Fatalf("input page was modified")
	}
	if got := view.HideForViewer(view.Viewer{}, page); !slices.Equal(got, page) {
		t.Fatalf("zero viewer hid %+v", got)
	}
}

func TestDefaultPipelineMasksAfterCollapsing(t *testing.T) {
	dup := textMsg(2, "a")
	gone := textMsg(3, "x")
	gone.Deleted = true
	page := []domain.Message{textMsg(1, "a"), dup, gone, textMsg(4, "d")}
	got := view.Default().Apply(view.Viewer{HiddenSeqs: map[uint64]bool{4: true}}, page)
	if want := []uint64{1, 3, 4}; !slices.Equal(seqs(got), want) {
		t.Fatalf("seqs = %v, want %v", seqs(got), want)
	}
	if got[1].Text != "" || !got[1].Deleted || got[2].Text != "" || !got[2].Hidden || got[0].Text != "a" {
		t.Fatalf("page = %+v, want seq 3 masked and seq 4 hidden", got)
	}
}
```

`apps/core/internal/grpcsrv/history_masks_test.go`:

```go
package grpcsrv_test

import (
	"context"
	"strconv"
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type shown struct {
	seq             uint64
	text            string
	deleted, hidden bool
	version         uint32
}

func TestHistoryShowsPlaceholdersPerViewer(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	for i, text := range []string{"one", "two", "three", "four"} {
		rg.send(t, alice, room, "c-"+strconv.Itoa(i), text)
	}
	steps := []func() error{
		func() error {
			_, err := rg.client.DeleteMessage(alice, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 2})
			return err
		},
		func() error {
			_, err := rg.client.EditMessage(alice, &chatimv1.EditMessageRequest{RoomId: room, Seq: 4, Text: "four!"})
			return err
		},
		func() error {
			_, err := rg.client.HideMessage(bob, &chatimv1.HideMessageRequest{RoomId: room, Seq: 3})
			return err
		},
		func() error {
			_, err := rg.client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: room, UpToSeq: 1})
			return err
		},
	}
	for i, step := range steps {
		if err := step(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
	cases := []struct {
		name string
		ctx  context.Context
		want []shown
	}{
		{"bob", bob, []shown{{1, "", false, true, 0}, {2, "", true, false, 1}, {3, "", false, true, 0}, {4, "four!", false, false, 1}}},
		{"alice", alice, []shown{{1, "one", false, false, 0}, {2, "", true, false, 1}, {3, "three", false, false, 0}, {4, "four!", false, false, 1}}},
	}
	for _, c := range cases {
		resp, err := rg.client.GetHistory(c.ctx, &chatimv1.GetHistoryRequest{RoomId: room, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_OLDEST})
		if err != nil {
			t.Fatalf("%s GetHistory: %v", c.name, err)
		}
		msgs := resp.GetMessages()
		if len(msgs) != len(c.want) {
			t.Fatalf("%s got %d messages, want %d", c.name, len(msgs), len(c.want))
		}
		for i, m := range msgs {
			if got := (shown{m.GetSeq(), m.GetText(), m.GetDeleted(), m.GetHidden(), m.GetVersion()}); got != c.want[i] {
				t.Fatalf("%s message %d = %+v, want %+v", c.name, i, got, c.want[i])
			}
		}
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/view/... ./apps/core/internal/grpcsrv/..."`
Expected: FAIL biên dịch: `undefined: view.MaskDeleted`, `undefined: view.HideForViewer`, `unknown field ClearedBeforeSeq in struct literal of type view.Viewer`.

**Step 3: Code**

`apps/core/internal/view/pipeline.go`, thay

```go
type Viewer struct {
	User string
	Room domain.Room
}
```

bằng

```go
type Viewer struct {
	User             string
	Room             domain.Room
	ClearedBeforeSeq uint64
	HiddenSeqs       map[uint64]bool
}
```

và thay `func Default() Pipeline { return New(CollapseRetried) }` bằng `func Default() Pipeline { return New(CollapseRetried, MaskDeleted, HideForViewer) }`.

`apps/core/internal/view/masks.go`:

```go
package view

import (
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func MaskDeleted(_ Viewer, msgs []domain.Message) []domain.Message {
	return eachCopy(msgs, func(m *domain.Message) {
		if m.Deleted {
			m.Text = ""
		}
	})
}

func HideForViewer(v Viewer, msgs []domain.Message) []domain.Message {
	return eachCopy(msgs, func(m *domain.Message) {
		if m.Seq <= v.ClearedBeforeSeq || v.HiddenSeqs[m.Seq] {
			m.Hidden = true
			m.Text = ""
		}
	})
}

func eachCopy(msgs []domain.Message, f func(m *domain.Message)) []domain.Message {
	out := slices.Clone(msgs)
	for i := range out {
		f(&out[i])
	}
	return out
}
```

`apps/core/internal/grpcsrv/core_service.go`:
- Trong `Service`, sau dòng `edits   store.Edits` thêm `hidden  store.Hidden`.
- Trong `New`, thay `mutator: d.Mutator, edits: d.Edits,` bằng `mutator: d.Mutator, edits: d.Edits, hidden: d.Hidden,`.

`apps/core/internal/grpcsrv/get_history.go`, thay

```go
	page = s.view.Apply(view.Viewer{User: who.user, Room: grant.Room}, page)
```

bằng

```go
	viewer, err := s.viewerOf(ctx, who.user, grant, q, page)
	if err != nil {
		return nil, err
	}
	page = s.view.Apply(viewer, page)
```

và cuối file thêm:

```go
func (s *Service) viewerOf(ctx context.Context, user string, grant access.Request, q store.PageQuery, page []domain.Message) (view.Viewer, error) {
	v := view.Viewer{User: user, Room: grant.Room, ClearedBeforeSeq: grant.Member.ClearedBeforeSeq}
	if len(page) == 0 {
		return v, nil
	}
	lo, hi := page[0].Seq, page[0].Seq
	for _, m := range page[1:] {
		lo, hi = min(lo, m.Seq), max(hi, m.Seq)
	}
	if hi <= v.ClearedBeforeSeq {
		return v, nil
	}
	seqs, err := s.hidden.HiddenIn(ctx, user, q.Room, q.Thread, lo, hi)
	if err != nil {
		return view.Viewer{}, err
	}
	v.HiddenSeqs = make(map[uint64]bool, len(seqs))
	for _, seq := range seqs {
		v.HiddenSeqs[seq] = true
	}
	return v, nil
}
```

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/view/... ./apps/core/internal/grpcsrv/..."`
Expected: PASS, gồm các test cũ `TestHistoryPagesThroughWhatWasSent` (tin chưa sửa: `version 0`, không `deleted`/`hidden`, `edited_at` rỗng — nếu test này fail vì `edited_at` khác nil thì `pbconv.Message` của Task 2 đang map thời điểm zero: dừng và báo, không sửa test) và `TestHistoryHidesASendStoredTwice`.

**Step 5: INDEXES + commit**

`INDEXES.csv`:
- Dòng `apps/core/internal/view`: purpose thay "; Default pipeline used by GetHistory" bằng "; MaskDeleted drops the text of deleted messages; HideForViewer turns seq <= ClearedBeforeSeq or seqs in HiddenSeqs into hidden placeholders without text (seq, version and flags kept); steps never modify their input; Default = CollapseRetried, MaskDeleted, HideForViewer used by GetHistory"; key_symbols thêm `MaskDeleted;HideForViewer`; decisions thêm `;D85`.
- Dòng `apps/core/internal/grpcsrv`: purpose nối "; GetHistory builds the view.Viewer from the member's cleared mark and one Hidden.HiddenIn over the page's seq range (skipped for an empty or fully cleared page)".

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/view/ apps/core/internal/grpcsrv/core_service.go apps/core/internal/grpcsrv/get_history.go apps/core/internal/grpcsrv/history_masks_test.go
git commit -m "feat(view): mask deleted messages and hide per reader in history" -- apps/core/internal/view/ apps/core/internal/grpcsrv/core_service.go apps/core/internal/grpcsrv/get_history.go apps/core/internal/grpcsrv/history_masks_test.go INDEXES.csv
```

---

### Task 11: Effect `edit_projection` + `msg_changed`, registry và metrics

Lưới an toàn cho fast path của Task 7 (D83), chạy trên record `EditInserted` (Task 5/6) theo thứ tự registry:
0. `room_activity` (delay 0, có sẵn từ M2b.1): record `EditInserted` thành `store.Activity{Room, Thread, Seq: 0, At: CommittedAt}`; part A đã đổi `TouchActivity` để `Seq == 0` chỉ nâng `lc`/`ab`, không đụng `ls`/`lm`. Nhờ vậy resync (tìm room theo `act_bucket`) thấy được room chỉ có sửa/xoá. Trong `latestActivity`, khoá gộp thêm cờ `edit` nên record sửa và record tin mới của cùng timeline (nếu có trong một lần gọi) ra hai `Activity` riêng: thời điểm sửa không bao giờ thành `LastMsgAt`. Worker vốn nhóm lô theo kind nên thực tế mỗi lần gọi chỉ có một kind; cờ chỉ để hàm đúng với mọi đầu vào.
1. `edit_projection` (delay 0): `Edits.At(key, version)` → `Messages.ApplyEdit` (CAS theo version, chạy lại vô hại) → fact xoá thì `PurgeText(key, version − 1)`. Fact không có (`ErrEditNotFound`) hoặc không giao được → bỏ, đếm `Dropped`, trả nil.
2. `msg_changed` (delay `RECONCILE_DELAY`, không ack mark): `At` → loại room (cache) → `Find` tin → `pbconv.MessageChanged` (cùng hàm fast path dùng, parity RC2) → publish, chờ PubAck. Tin, fact hoặc room không còn → bỏ. Nếu `msg.Version < fact.Version` (projection chưa tới, ví dụ `edit_projection` vừa lỗi trong cùng lô) → lỗi retry được, record `Nak` và cả hai effect chạy lại; không bao giờ phát snapshot cũ hơn fact.

**Đếm `Republished` của `msg_changed`:** effect này không tra ack mark nên gửi lại **mọi** fact; nếu đếm mọi PubAck thì `reconcile_republished_total{effect="msg_changed"}` = tốc độ sửa/xoá (100–300/s đỉnh theo §6.3) và luật `ChatimRepublishSurge` (`sum > 100/s`) kêu sai. Nên `msg_changed` chỉ đếm PubAck có `Duplicate == false` (stream chưa có id đó, tức fast path đã mất event) — đúng nghĩa detector RC1 của bảng tính năng. `awaitAcks` nhận hàm đếm: `countAll` (giữ nguyên nghĩa của `msg_created`, `room_created`) và `countStored` (`msg_changed`). Không đổi luật alert: `ChatimRepublishSurge`, `ChatimEffectDropping` (`sum by (effect)`), `ChatimWorkFailing` đã phủ hai effect mới.

**Metrics:** `edit_projection` không publish nên không có `reconcile_republished_total`; `effectCounters.republished` được phép nil và `workerSources` bỏ qua nguồn đó. Xuất `effect_dropped_total{effect="edit_projection"}`, `reconcile_republished_total{effect="msg_changed"}`, `effect_dropped_total{effect="msg_changed"}`.

**Files:**
- Modify: `apps/core/internal/effects/ports.go`
- Modify: `apps/core/internal/effects/pub_acks.go`
- Modify: `apps/core/internal/effects/message_created.go`, `room_created.go` (một dòng mỗi file)
- Modify: `apps/core/internal/effects/room_activity.go`, `room_activity_test.go`
- Create: `apps/core/internal/effects/edit_projection.go`
- Create: `apps/core/internal/effects/message_changed.go`
- Create: `apps/core/internal/effects/edit_fixtures_test.go`
- Create: `apps/core/internal/effects/edit_projection_test.go`
- Create: `apps/core/internal/effects/message_changed_test.go`
- Modify: `apps/core/effects_wiring.go`, `apps/core/metrics_wiring.go`, `apps/core/metrics_wiring_test.go`
- Modify: `INDEXES.csv`

**Step 1: Test**

`apps/core/internal/effects/edit_fixtures_test.go`:

```go
package effects_test

import (
	"context"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

type editRig struct {
	msgs    *memstore.Messages
	edits   *memstore.Edits
	rooms   *memstore.Rooms
	js      *publishtest.JetStream
	proj    *effects.EditProjection
	changed *effects.MessageChanged
}

func newEditRig(t *testing.T) *editRig {
	t.Helper()
	rg := &editRig{msgs: memstore.NewMessages(), edits: memstore.NewEdits(), rooms: memstore.NewRooms(), js: &publishtest.JetStream{}}
	createRoom(t, rg.rooms, room)
	proj, err := effects.NewEditProjection(effects.EditProjectionDeps{Edits: rg.edits, Messages: rg.msgs, Purger: rg.edits})
	if err != nil {
		t.Fatalf("NewEditProjection: %v", err)
	}
	changed, err := effects.NewMessageChanged(
		effects.MessageChangedDeps{Edits: rg.edits, Messages: rg.msgs, Rooms: rg.rooms, JS: rg.js},
		effects.MessageChangedConfig{SubjectRoot: "evt", Delay: delay, RoomCache: 16},
	)
	if err != nil {
		t.Fatalf("NewMessageChanged: %v", err)
	}
	rg.proj, rg.changed = proj, changed
	return rg
}

func (rg *editRig) original(t *testing.T, r, seq uint64) {
	t.Helper()
	m := domain.Message{Room: r, Seq: seq, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "v0", CID: "c", CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert %d/%d: %+v", r, seq, res)
	}
}

func (rg *editRig) appendFact(t *testing.T, r, seq uint64, v uint32, kind domain.EditKind, text string) domain.Edit {
	t.Helper()
	e := domain.Edit{Room: r, Seq: seq, Version: v, Kind: kind, Tenant: tenant, By: "alice", Text: text, At: time.Now().UTC().Truncate(time.Millisecond)}
	if v == 1 && kind == domain.EditText {
		e.Prev = "v0"
	}
	if err := rg.edits.Append(t.Context(), e); err != nil {
		t.Fatalf("append v%d: %v", v, err)
	}
	return e
}

func (rg *editRig) project(t *testing.T, e domain.Edit) {
	t.Helper()
	if err := rg.msgs.ApplyEdit(t.Context(), e); err != nil {
		t.Fatalf("ApplyEdit v%d: %v", e.Version, err)
	}
}

func (rg *editRig) stored(t *testing.T, seq uint64) domain.Message {
	t.Helper()
	found, err := rg.msgs.Find(t.Context(), room, []store.MsgKey{{Room: room, Seq: seq}})
	if err != nil || len(found) != 1 {
		t.Fatalf("find seq %d = %v, %v", seq, found, err)
	}
	return found[0]
}

func editRecs(r, seq uint64, versions ...uint32) []work.Record {
	out := make([]work.Record, len(versions))
	for i, v := range versions {
		out[i] = work.Record{Kind: store.EditInserted, Room: r, Seq: seq, Version: v, CommittedAt: time.Now()}
	}
	return out
}

func (brokenStore) At(context.Context, store.MsgKey, uint32) (domain.Edit, error) {
	return domain.Edit{}, errBoom
}

func (brokenStore) ApplyEdit(context.Context, domain.Edit) error { return errBoom }

func (brokenStore) PurgeText(context.Context, store.MsgKey, uint32) error { return errBoom }
```

`apps/core/internal/effects/edit_projection_test.go`:

```go
package effects_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestEditProjectionDeclaresItsPolicy(t *testing.T) {
	e := newEditRig(t).proj.Effect()
	if e.Name != effects.EditProjectionName || e.Delay != 0 || e.Run == nil {
		t.Fatalf("effect = %q delay %v, want %q delay 0", e.Name, e.Delay, effects.EditProjectionName)
	}
}

func TestEditProjectionAppliesAFactTheFastPathMissed(t *testing.T) {
	rg := newEditRig(t)
	rg.original(t, room, 1)
	f := rg.appendFact(t, room, 1, 1, domain.EditText, "v1")
	for range 2 {
		if errs := rg.proj.Effect().Run(t.Context(), editRecs(room, 1, 1)); !allNil(errs, 1) {
			t.Fatalf("errs = %v", errs)
		}
	}
	got := rg.stored(t, 1)
	if got.Version != 1 || got.Text != "v1" || got.Deleted || !got.EditedAt.Equal(f.At) {
		t.Fatalf("stored = %+v, want v1 projected", got)
	}
	if rg.proj.Dropped() != 0 {
		t.Fatalf("dropped = %d", rg.proj.Dropped())
	}
}

func TestEditProjectionPurgesOlderTextOnDelete(t *testing.T) {
	rg := newEditRig(t)
	rg.original(t, room, 1)
	rg.appendFact(t, room, 1, 1, domain.EditText, "v1")
	rg.appendFact(t, room, 1, 2, domain.EditDelete, "")
	if errs := rg.proj.Effect().Run(t.Context(), editRecs(room, 1, 2, 1)); !allNil(errs, 2) {
		t.Fatalf("errs = %v", errs)
	}
	if got := rg.stored(t, 1); got.Version != 2 || !got.Deleted || got.Text != "" {
		t.Fatalf("stored = %+v, want deleted at v2 and the late v1 ignored", got)
	}
	v1, err := rg.edits.At(t.Context(), store.MsgKey{Room: room, Seq: 1}, 1)
	if err != nil || v1.Text != "" || v1.Prev != "" {
		t.Fatalf("v1 = %+v, %v; want its text and prev purged", v1, err)
	}
}

func TestEditProjectionDropsAMissingFact(t *testing.T) {
	rg := newEditRig(t)
	rg.original(t, room, 1)
	if errs := rg.proj.Effect().Run(t.Context(), editRecs(room, 1, 7)); !allNil(errs, 1) {
		t.Fatalf("errs = %v, want nil so the record is not retried", errs)
	}
	if rg.proj.Dropped() != 1 || rg.stored(t, 1).Version != 0 {
		t.Fatalf("dropped %d, stored %+v; want 1 and the message untouched", rg.proj.Dropped(), rg.stored(t, 1))
	}
}

func TestEditProjectionRetriesWhenTheStoreFails(t *testing.T) {
	eff, err := effects.NewEditProjection(effects.EditProjectionDeps{Edits: brokenStore{}, Messages: brokenStore{}, Purger: brokenStore{}})
	if err != nil {
		t.Fatalf("NewEditProjection: %v", err)
	}
	if errs := eff.Effect().Run(t.Context(), editRecs(room, 1, 1)); len(errs) != 1 || !errors.Is(errs[0], errBoom) || eff.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want a retryable failure", errs, eff.Dropped())
	}
}

func TestNewEditProjectionRejectsMissingDeps(t *testing.T) {
	edits, msgs := memstore.NewEdits(), memstore.NewMessages()
	for name, deps := range map[string]effects.EditProjectionDeps{
		"no edits":    {Messages: msgs, Purger: edits},
		"no messages": {Edits: edits, Purger: edits},
		"no purger":   {Edits: edits, Messages: msgs},
	} {
		if _, err := effects.NewEditProjection(deps); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: NewEditProjection = %v, want ErrInvalidArgument", name, err)
		}
	}
}
```

`apps/core/internal/effects/message_changed_test.go`:

```go
package effects_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestMsgChangedDeclaresItsPolicy(t *testing.T) {
	e := newEditRig(t).changed.Effect()
	if e.Name != effects.MessageChangedName || e.Delay != delay || e.Run == nil {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.MessageChangedName, delay)
	}
}

func TestMsgChangedPublishesTheCurrentSnapshot(t *testing.T) {
	cases := []struct {
		name    string
		kind    domain.EditKind
		text    string
		subject string
	}{
		{"edit", domain.EditText, "v1", "evt.acme.room.4242.msg_edited"},
		{"delete", domain.EditDelete, "", "evt.acme.room.4242.msg_deleted"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rg := newEditRig(t)
			rg.original(t, room, 1)
			f := rg.appendFact(t, room, 1, 1, c.kind, c.text)
			rg.project(t, f)
			if errs := rg.changed.Effect().Run(t.Context(), editRecs(room, 1, 1)); !allNil(errs, 1) {
				t.Fatalf("errs = %v", errs)
			}
			if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.MessageChangeEventID(room, 0, 1, 1)}) {
				t.Fatalf("stored = %v", got)
			}
			if subj := rg.js.Stored()[0].Subject; subj != c.subject {
				t.Fatalf("subject = %q, want %q", subj, c.subject)
			}
			events, err := rg.js.Events()
			if want := pbconv.MessageChanged(domain.RoomGroup, rg.stored(t, 1), f); err != nil || !proto.Equal(events[0], want) {
				t.Fatalf("event = %v, %v; want the fast path event %v", events, err, want)
			}
			if rg.changed.Republished() != 1 || rg.changed.Dropped() != 0 {
				t.Fatalf("republished %d, dropped %d; want 1 and 0", rg.changed.Republished(), rg.changed.Dropped())
			}
		})
	}
}

func TestMsgChangedCountsOnlyEventsTheStreamLacked(t *testing.T) {
	rg := newEditRig(t)
	rg.original(t, room, 1)
	f := rg.appendFact(t, room, 1, 1, domain.EditText, "v1")
	rg.project(t, f)
	fast, err := publish.Message("evt", room, pbconv.MessageEdited(domain.RoomGroup, rg.stored(t, 1), f))
	if err != nil {
		t.Fatalf("publish.Message: %v", err)
	}
	if _, err := rg.js.PublishMsgAsync(fast); err != nil {
		t.Fatalf("fast path publish: %v", err)
	}
	if errs := rg.changed.Effect().Run(t.Context(), editRecs(room, 1, 1)); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if len(rg.js.Stored()) != 1 || len(rg.js.Attempts()) != 2 || rg.changed.Republished() != 0 {
		t.Fatalf("stored %d, attempts %d, republished %d; want 1, 2 and 0", len(rg.js.Stored()), len(rg.js.Attempts()), rg.changed.Republished())
	}
}

func TestMsgChangedWaitsForTheProjection(t *testing.T) {
	rg := newEditRig(t)
	rg.original(t, room, 1)
	rg.appendFact(t, room, 1, 1, domain.EditText, "v1")
	errs := rg.changed.Effect().Run(t.Context(), editRecs(room, 1, 1))
	if len(errs) != 1 || errs[0] == nil || len(rg.js.Attempts()) != 0 || rg.changed.Dropped() != 0 {
		t.Fatalf("errs = %v, attempts %d, dropped %d; want a retry and nothing sent", errs, len(rg.js.Attempts()), rg.changed.Dropped())
	}
}

func TestMsgChangedDropsWhatIsGone(t *testing.T) {
	rg := newEditRig(t)
	rg.original(t, room, 1)
	rg.appendFact(t, room, 8, 1, domain.EditText, "no message")
	rg.original(t, 999, 1)
	rg.project(t, rg.appendFact(t, 999, 1, 1, domain.EditText, "no room"))
	recs := append(editRecs(room, 1, 5), editRecs(room, 8, 1)...)
	recs = append(recs, editRecs(999, 1, 1)...)
	if errs := rg.changed.Effect().Run(t.Context(), recs); !allNil(errs, 3) {
		t.Fatalf("errs = %v, want nil so dropped records are not retried", errs)
	}
	if len(rg.js.Attempts()) != 0 || rg.changed.Dropped() != 3 {
		t.Fatalf("attempts %d, dropped %d; want 0 and 3 (missing fact, message, room)", len(rg.js.Attempts()), rg.changed.Dropped())
	}
}

func TestMsgChangedRetriesWhenTheStoreFails(t *testing.T) {
	eff, err := effects.NewMessageChanged(
		effects.MessageChangedDeps{Edits: brokenStore{}, Messages: brokenStore{}, Rooms: brokenStore{}, JS: &publishtest.JetStream{}},
		effects.MessageChangedConfig{SubjectRoot: "evt"},
	)
	if err != nil {
		t.Fatalf("NewMessageChanged: %v", err)
	}
	if errs := eff.Effect().Run(t.Context(), editRecs(room, 1, 1)); len(errs) != 1 || !errors.Is(errs[0], errBoom) || eff.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want a retryable failure", errs, eff.Dropped())
	}
}

func TestMsgChangedWaitsForThePubAck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newEditRig(t)
		rg.original(t, room, 1)
		rg.project(t, rg.appendFact(t, room, 1, 1, domain.EditText, "v1"))
		rg.js.Hold()
		done := make(chan []error, 1)
		go func() { done <- rg.changed.Effect().Run(context.Background(), editRecs(room, 1, 1)) }()
		synctest.Wait()
		select {
		case errs := <-done:
			t.Fatalf("Run returned %v before the PubAck", errs)
		default:
		}
		rg.js.Release()
		if errs := <-done; !allNil(errs, 1) || rg.changed.Republished() != 1 {
			t.Fatalf("errs = %v, republished %d; want success after the PubAck", errs, rg.changed.Republished())
		}
	})
}

func TestNewMessageChangedRejectsBadInput(t *testing.T) {
	js, edits, msgs, mem := &publishtest.JetStream{}, memstore.NewEdits(), memstore.NewMessages(), memstore.NewRooms()
	cfg := effects.MessageChangedConfig{SubjectRoot: "evt"}
	for name, deps := range map[string]effects.MessageChangedDeps{
		"no edits":    {Messages: msgs, Rooms: mem, JS: js},
		"no messages": {Edits: edits, Rooms: mem, JS: js},
		"no rooms":    {Edits: edits, Messages: msgs, JS: js},
		"no js":       {Edits: edits, Messages: msgs, Rooms: mem},
	} {
		if _, err := effects.NewMessageChanged(deps, cfg); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: NewMessageChanged = %v, want ErrInvalidArgument", name, err)
		}
	}
	full := effects.MessageChangedDeps{Edits: edits, Messages: msgs, Rooms: mem, JS: js}
	if _, err := effects.NewMessageChanged(full, effects.MessageChangedConfig{}); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("NewMessageChanged without a subject root = %v, want ErrInvalidArgument", err)
	}
	eff, err := effects.NewMessageChanged(full, cfg)
	if err != nil {
		t.Fatalf("NewMessageChanged with defaults: %v", err)
	}
	if got := eff.Effect().Delay; got != effects.DefaultDelay {
		t.Fatalf("default delay = %v, want %v", got, effects.DefaultDelay)
	}
}
```

`apps/core/internal/effects/room_activity_test.go`, cuối file thêm:

```go
func TestRoomActivityTouchesOnlyTheChangeTimeForEdits(t *testing.T) {
	spy := &touchSpy{}
	t1, t2 := activityAt, activityAt.Add(time.Second)
	recs := []work.Record{
		{Kind: store.EditInserted, Room: 101, Seq: 5, Version: 2, CommittedAt: t1},
		{Kind: store.EditInserted, Room: 101, Seq: 3, Version: 1, CommittedAt: t2},
		activityRecord(101, 0, 4, t1),
	}
	errs := effects.NewRoomActivity(spy).Effect().Run(t.Context(), recs)
	if len(errs) != len(recs) || slices.ContainsFunc(errs, func(err error) bool { return err != nil }) {
		t.Fatalf("Run errors = %v, want %d nils", errs, len(recs))
	}
	want := []store.Activity{{Room: 101, At: t2}, {Room: 101, Seq: 4, At: t1}}
	if len(spy.calls) != 1 || !slices.Equal(spy.calls[0], want) {
		t.Fatalf("TouchActivity calls = %+v, want one call with %+v (edits as seq 0, kept apart from the new message)", spy.calls, want)
	}
}
```

`apps/core/metrics_wiring_test.go`:
- Trong `fakeProbes`, map `effectCounts` thêm dòng `"edit_projection": {dropped: func() uint64 { return 4 }},`.
- Trong `TestEffectMetricsReadTheirEffectByLabel`, map `want` thêm `"effect_dropped_total{edit_projection}": 4,`; ngay sau vòng so `want` thêm:

```go
	if _, ok := got["reconcile_republished_total{edit_projection}"]; ok {
		t.Errorf("edit_projection never publishes, but reconcile_republished_total is exported for it")
	}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/effects/... ./apps/core/"`
Expected: FAIL biên dịch: `undefined: effects.EditProjection`, `effects.NewEditProjection`, `effects.MessageChanged`, `effects.MessageChangedName` (khi tạm bỏ hai file test mới thì `TestRoomActivityTouchesOnlyTheChangeTimeForEdits` fail vì nhận `{Room: 101, Seq: 5, At: t2}` gộp chung với tin seq 4); `apps/core` panic/fail vì `c.republished` nil được gọi (`nil pointer dereference` trong `TestEffectMetricsReadTheirEffectByLabel`) hoặc thiếu key `effect_dropped_total{edit_projection}` tuỳ thứ tự.

**Step 3: Code**

`apps/core/internal/effects/ports.go`, cuối file thêm:

```go
type EditReader interface {
	At(ctx context.Context, key store.MsgKey, version uint32) (domain.Edit, error)
}

type EditApplier interface {
	ApplyEdit(ctx context.Context, e domain.Edit) error
}

type TextPurger interface {
	PurgeText(ctx context.Context, key store.MsgKey, upTo uint32) error
}
```

`apps/core/internal/effects/pub_acks.go`, thay toàn bộ hàm `awaitAcks` bằng:

```go
func awaitAcks(ctx context.Context, pending []pendingAck, errs []error, count func(*jetstream.PubAck)) {
	for _, p := range pending {
		select {
		case ack := <-p.future.Ok():
			count(ack)
		case err := <-p.future.Err():
			errs[p.index] = err
		case <-ctx.Done():
			errs[p.index] = ctx.Err()
		}
	}
}

func countAll(n *atomic.Uint64) func(*jetstream.PubAck) {
	return func(*jetstream.PubAck) { n.Add(1) }
}

func countStored(n *atomic.Uint64) func(*jetstream.PubAck) {
	return func(ack *jetstream.PubAck) {
		if ack != nil && !ack.Duplicate {
			n.Add(1)
		}
	}
}
```

`apps/core/internal/effects/message_created.go` và `room_created.go`: mỗi file thay `awaitAcks(ctx, pending, errs, &e.republished)` bằng `awaitAcks(ctx, pending, errs, countAll(&e.republished))`.

`apps/core/internal/effects/room_activity.go`:
- Thay `type activityKey struct{ room, thread uint64 }` bằng

```go
type activityKey struct {
	room, thread uint64
	edit         bool
}
```

- Thay toàn bộ `latestActivity` bằng:

```go
func latestActivity(recs []work.Record) []store.Activity {
	at := make(map[activityKey]int, len(recs))
	out := make([]store.Activity, 0, len(recs))
	for _, r := range recs {
		edit := r.Kind == store.EditInserted
		seq := r.Seq
		if edit {
			seq = 0
		}
		k := activityKey{r.Room, r.Thread, edit}
		i, ok := at[k]
		if !ok {
			at[k] = len(out)
			out = append(out, store.Activity{Room: r.Room, Thread: r.Thread, Seq: seq, At: r.CommittedAt})
			continue
		}
		out[i].Seq = max(out[i].Seq, seq)
		if r.CommittedAt.After(out[i].At) {
			out[i].At = r.CommittedAt
		}
	}
	return out
}
```

Test cũ `TestRoomActivityTouchesTheHighestSeqPerTimelineInOneWrite` giữ nguyên kết quả (chỉ có `MessageInserted`, thứ tự xuất hiện không đổi).

`apps/core/internal/effects/edit_projection.go`:

```go
package effects

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const EditProjectionName = "edit_projection"

type EditProjectionDeps struct {
	Edits    EditReader
	Messages EditApplier
	Purger   TextPurger
}

type EditProjection struct {
	deps    EditProjectionDeps
	dropped atomic.Uint64
}

func NewEditProjection(deps EditProjectionDeps) (*EditProjection, error) {
	if deps.Edits == nil || deps.Messages == nil || deps.Purger == nil {
		return nil, fmt.Errorf("%w: edit_projection needs edits, messages and a text purger", apperr.ErrInvalidArgument)
	}
	return &EditProjection{deps: deps}, nil
}

func (e *EditProjection) Effect() Effect {
	return Effect{Name: EditProjectionName, Run: e.run}
}

func (e *EditProjection) Dropped() uint64 { return e.dropped.Load() }

func (e *EditProjection) run(ctx context.Context, recs []work.Record) []error {
	errs := make([]error, len(recs))
	for i, r := range recs {
		err := e.apply(ctx, r)
		switch {
		case gone(err):
			e.dropped.Add(1)
		case err != nil:
			errs[i] = err
		}
	}
	return errs
}

func (e *EditProjection) apply(ctx context.Context, r work.Record) error {
	key := recordKey(r)
	fact, err := e.deps.Edits.At(ctx, key, r.Version)
	if err != nil {
		return err
	}
	if err := e.deps.Messages.ApplyEdit(ctx, fact); err != nil {
		return err
	}
	if fact.Kind != domain.EditDelete {
		return nil
	}
	return e.deps.Purger.PurgeText(ctx, key, fact.Version-1)
}

func recordKey(r work.Record) store.MsgKey {
	return store.MsgKey{Room: r.Room, Thread: r.Thread, Seq: r.Seq}
}

func gone(err error) bool {
	return undeliverable(err) || errors.Is(err, store.ErrEditNotFound) || errors.Is(err, domain.ErrMessageNotFound)
}
```

(`fact.Version − 1` không tràn: `At` chỉ trả fact đã lưu, version ≥ 1.)

`apps/core/internal/effects/message_changed.go`:

```go
package effects

import (
	"cmp"
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const MessageChangedName = "msg_changed"

var errProjectionBehind = fmt.Errorf("message projection behind its edit fact: %w", apperr.ErrUnavailable)

type MessageChangedDeps struct {
	Edits    EditReader
	Messages MessageFinder
	Rooms    RoomReader
	JS       publish.JetStream
}

type MessageChangedConfig struct {
	SubjectRoot string
	Delay       time.Duration
	RoomCache   int
}

type MessageChanged struct {
	deps        MessageChangedDeps
	cfg         MessageChangedConfig
	types       *roomTypes
	republished atomic.Uint64
	dropped     atomic.Uint64
}

func NewMessageChanged(deps MessageChangedDeps, cfg MessageChangedConfig) (*MessageChanged, error) {
	if deps.Edits == nil || deps.Messages == nil || deps.Rooms == nil || deps.JS == nil {
		return nil, fmt.Errorf("%w: msg_changed needs edits, messages, rooms and a jetstream client", apperr.ErrInvalidArgument)
	}
	cfg.Delay = cmp.Or(cfg.Delay, DefaultDelay)
	cfg.RoomCache = cmp.Or(cfg.RoomCache, DefaultRoomCache)
	if cfg.SubjectRoot == "" || cfg.Delay < 0 || cfg.RoomCache < 0 {
		return nil, fmt.Errorf("%w: msg_changed config %+v needs a subject root, a delay and a room cache", apperr.ErrInvalidArgument, cfg)
	}
	return &MessageChanged{deps: deps, cfg: cfg, types: newRoomTypes(deps.Rooms, cfg.RoomCache)}, nil
}

func (e *MessageChanged) Effect() Effect {
	return Effect{Name: MessageChangedName, Delay: e.cfg.Delay, Run: e.run}
}

func (e *MessageChanged) Republished() uint64 { return e.republished.Load() }

func (e *MessageChanged) Dropped() uint64 { return e.dropped.Load() }

func (e *MessageChanged) run(ctx context.Context, recs []work.Record) []error {
	errs := make([]error, len(recs))
	var pending []pendingAck
	for i, r := range recs {
		ev, err := e.event(ctx, r)
		switch {
		case gone(err):
			e.dropped.Add(1)
			continue
		case err != nil:
			errs[i] = err
			continue
		}
		msg, err := publish.Message(e.cfg.SubjectRoot, r.Room, ev)
		if err != nil {
			e.dropped.Add(1)
			continue
		}
		pending = send(e.deps.JS, msg, i, errs, pending)
	}
	awaitAcks(ctx, pending, errs, countStored(&e.republished))
	return errs
}

func (e *MessageChanged) event(ctx context.Context, r work.Record) (*chatimv1.Event, error) {
	key := recordKey(r)
	fact, err := e.deps.Edits.At(ctx, key, r.Version)
	if err != nil {
		return nil, err
	}
	typ, err := e.types.get(ctx, r.Room)
	if err != nil {
		return nil, err
	}
	found, err := e.deps.Messages.Find(ctx, r.Room, []store.MsgKey{key})
	switch {
	case err != nil:
		return nil, err
	case len(found) == 0:
		return nil, domain.ErrMessageNotFound
	case found[0].Version < fact.Version:
		return nil, errProjectionBehind
	}
	return pbconv.MessageChanged(typ, found[0], fact), nil
}
```

`apps/core/effects_wiring.go` (thay toàn bộ file):

```go
package main

import (
	"fmt"
	"log/slog"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/eventmark"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

type effectSet struct {
	workers        *effects.Workers
	msgCreated     *effects.MessageCreated
	roomCreated    *effects.RoomCreated
	editProjection *effects.EditProjection
	msgChanged     *effects.MessageChanged
}

func wireEffects(cfg config.Config, cl *clients, st *mongostore.Store, marks *eventmark.Store, owner effects.Owner, log *slog.Logger) (effectSet, error) {
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
	activity := effects.NewRoomActivity(st)
	registry := effects.Registry{
		store.MessageInserted: {activity.Effect(), fx.msgCreated.Effect()},
		store.RoomInserted:    {fx.roomCreated.Effect()},
		store.EditInserted:    {activity.Effect(), fx.editProjection.Effect(), fx.msgChanged.Effect()},
	}
	fx.workers, err = effects.New(effects.Deps{
		Queue:    func(p int) work.Queue { return work.NewQueue(cl.effectsJS, cfg.Work.Name, p) },
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
		fx.msgCreated.Effect().Name:     {republished: fx.msgCreated.Republished, dropped: fx.msgCreated.Dropped},
		fx.roomCreated.Effect().Name:    {republished: fx.roomCreated.Republished, dropped: fx.roomCreated.Dropped},
		fx.msgChanged.Effect().Name:     {republished: fx.msgChanged.Republished, dropped: fx.msgChanged.Dropped},
		fx.editProjection.Effect().Name: {dropped: fx.editProjection.Dropped},
	}
}
```

Thứ tự registry `EditInserted`: `room_activity` và `edit_projection` (delay 0) **trước** `msg_changed` (delay `RECONCILE_DELAY`), cùng lý do `room_activity` trước `msg_created` (M2b.1 Task 12): worker chạy effect của một kind tuần tự và chờ `max(CommittedAt) + Delay` riêng từng effect.

`apps/core/metrics_wiring.go`, trong `workerSources`:
- Thay

```go
	const republishHelp = "Events effect workers sent because no ack mark showed the fast path delivered them."
	const dropHelp = "Work records an effect gave up on (missing room or corrupt document)."
```

bằng

```go
	const republishHelp = "Events effect workers sent and JetStream acked; msg_created sends only unmarked events, msg_changed counts only ids the stream had not stored."
	const dropHelp = "Work records an effect gave up on (missing room, message or edit fact, or a corrupt document)."
```

- Thay

```go
		out = append(out,
			metrics.Source{Name: "reconcile_republished_total", Help: republishHelp, Labels: labels, Read: func() float64 { return float64(c.republished()) }},
			metrics.Source{Name: "effect_dropped_total", Help: dropHelp, Labels: labels, Read: func() float64 { return float64(c.dropped()) }},
		)
```

bằng

```go
		if c.republished != nil {
			out = append(out, metrics.Source{Name: "reconcile_republished_total", Help: republishHelp, Labels: labels, Read: func() float64 { return float64(c.republished()) }})
		}
		out = append(out, metrics.Source{Name: "effect_dropped_total", Help: dropHelp, Labels: labels, Read: func() float64 { return float64(c.dropped()) }})
```

**Step 4: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/internal/effects/... ./apps/core/"
make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/effects/..."
```

Expected: PASS cả hai (test cũ `msg_created`/`room_created` giữ nguyên số `Republished` vì dùng `countAll`). `wc -l apps/core/internal/effects/*.go apps/core/effects_wiring.go apps/core/metrics_wiring.go` mỗi file < 200 (`message_changed_test.go` ~185).

**Step 5: Itest**

```bash
make infra-up && make itest
```

Expected: mọi package `ok` (wiring đổi; registry mới chưa có record `EditInserted` trong itest cũ, Task 14 thêm itest riêng).

**Step 6: INDEXES + commit**

`INDEXES.csv`:
- Dòng `apps/core/internal/effects`: purpose nối "; room_activity also runs on EditInserted as seq 0 activity (bumps only the change time and bucket, kept apart from new messages of the same timeline); edit_projection effect (delay 0) for EditInserted: Edits.At, Messages.ApplyEdit (CAS by version), PurgeText below a delete fact; missing fact dropped and counted; msg_changed effect (delay RECONCILE_DELAY, no ack mark): Edits.At, room type cache, Find, refuses (retry) while the projection is behind the fact, event rebuilt with pbconv.MessageChanged (fast path parity), publish and wait for the PubAck; counts as republished only PubAcks the stream had not deduplicated; missing fact, message or room dropped"; key_symbols thêm `EditProjection;NewEditProjection;EditProjection.Effect;EditProjection.Dropped;EditProjectionDeps;EditProjectionName;MessageChanged;NewMessageChanged;MessageChanged.Effect;MessageChanged.Republished;MessageChanged.Dropped;MessageChangedDeps;MessageChangedConfig;MessageChangedName;EditReader;EditApplier;TextPurger`; decisions thêm `;D83`.
- Dòng `apps/core`: purpose, đoạn registry của `effects_wiring.go` đổi thành "registry MessageInserted -> room_activity, msg_created; RoomInserted -> room_created; EditInserted -> room_activity, edit_projection, msg_changed"; nối "; effect metrics skip reconcile_republished_total for effects that never publish (edit_projection)".

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/effects/ apps/core/effects_wiring.go apps/core/metrics_wiring.go apps/core/metrics_wiring_test.go
git commit -m "feat(effects): project edit facts and republish message changes" -- apps/core/internal/effects/ apps/core/effects_wiring.go apps/core/metrics_wiring.go apps/core/metrics_wiring_test.go INDEXES.csv
```

Task rủi ro: một reviewer (thứ tự registry, record sửa chỉ ra `Activity` seq 0, `msg_changed` không phát snapshot cũ hơn fact, nghĩa mới của `Republished`, `-count=5` đã xanh thì reviewer chỉ chạy tối đa `-count=3`).

---

#### Ghi chú cho controller (part B)

**Tinh chỉnh/thêm vào hợp đồng (Go chính xác):**
- `pbconv` (Task 7, file mới `change_event.go`, không đụng file của Task 2):
  ```go
  func MessageChanged(roomType domain.RoomType, m domain.Message, e domain.Edit) *chatimv1.Event
  ```
  `EditDelete` → `MessageDeleted`, còn lại → `MessageEdited`. `mutate` và `effects.MessageChanged` cùng dùng (parity RC2).
- `mutate`: `HideCmd`, `ClearCmd` khai báo ở Task 8 (`hide_clear.go`); `EditCmd`, `DeleteCmd`, `Messages`, `HistoryClearer`, `EventPublisher`, `Deps`, `New` ở Task 7. `New` bắt buộc mọi dep trừ `Now` (mặc định `time.Now`); thiếu → `apperr.ErrInvalidArgument`.
- `Edit.Prev` chỉ có ở fact `EditText` version 1 (fact `EditDelete` v1 không mang `Prev`, nếu không text gốc thoát khỏi `PurgeText`, trái D75). Adapter/contract của part A nên coi `Prev` của fact xoá là rỗng.
- `mutate.ClearHistory`: luôn đọc `Messages.Last(room, 0)` và kẹp `UpToSeq` về `last` khi `UpToSeq == 0 || UpToSeq > last` (hợp đồng chỉ nói `0`). Lý do ở Task 8.
- Retry của lệnh đổi nhận ra từ **fact cuối** trước mọi kiểm khác (kể cả "đã xoá"), dùng fact đã lưu (giữ `At`), và vẫn chạy projection + **enqueue lại event cùng id** (JetStream bỏ trùng). Thiết kế §6.3 bước 4 ghi "không phát event"; thực tế phát lại cùng id là vô hại và cần cho trường hợp crash giữa fact và projection. Docs (D83) nên ghi đúng như vậy.
- `grpcsrv.PageReader` thêm `Find(ctx, room, keys) ([]domain.Message, error)`. `grpcsrv.Deps` thêm `Mutator *mutate.Mutator`, `Edits store.Edits`, `Hidden store.Hidden` (bắt buộc). `Service.hidden` chỉ có từ Task 10.
- `GetEditHistory` dùng `domain.PageLimit` (0 → 50, tối đa 100 = `store.MaxEditPage`); tin đã xoá (`msg.Deleted`) → `versions` rỗng; không xét ẩn/clear của người đọc.
- `effects`: `ports.go` thêm `EditReader`, `EditApplier`, `TextPurger` đúng hợp đồng. `awaitAcks(ctx, pending, errs, count func(*jetstream.PubAck))` + `countAll(*atomic.Uint64)` / `countStored(*atomic.Uint64)`; `MessageChanged.Republished()` chỉ đếm PubAck `Duplicate == false`. Lỗi nội bộ `errProjectionBehind` (wrap `apperr.ErrUnavailable`) khi `msg.Version < fact.Version` → record retry. Helper nội bộ `recordKey`, `gone` (drop khi `undeliverable` hoặc `store.ErrEditNotFound` hoặc `domain.ErrMessageNotFound`).
- `effects.room_activity` nhận cả `EditInserted` (quyết định controller): `latestActivity` map record sửa thành `store.Activity{Room, Thread, Seq: 0, At: CommittedAt}`, khoá gộp `activityKey{room, thread, edit bool}`. Phụ thuộc part A: `TouchActivity` với `Seq == 0` chỉ nâng `lc`/`ab`, không đụng `ls`/`lm` (memstore và Mongo, có case trong contract `storetest`). Registry: `store.EditInserted: {activity.Effect(), editProjection.Effect(), msgChanged.Effect()}`.
- `apps/core`: `effectCounters.republished` được phép nil (edit_projection); `workerSources` bỏ `reconcile_republished_total` cho effect đó. Help của hai metric được viết lại. Không thêm luật alert (các luật `sum`/`sum by (effect)` đã phủ).
- Wiring: `apps/core/service_wiring.go` `wireService(st *mongostore.Store, router *actor.Router, pub *publish.Publisher, log *slog.Logger) (*grpcsrv.Service, error)` dựng `access.NewChecker(st, nil)` (`DefaultPolicy`) riêng cho `mutate`.
- `access` (Task 7, D86): `Request.Author`; `Checker.Admit` + `Checker.Allow` (`Authorize` = hai bước); `DefaultPolicy` là mặc định của `NewChecker` và actor. `mutate.target` = `Admit` → `Find` → `Allow(Author)`, dùng cho Edit/Delete/Hide; `GetEditHistory` cùng thứ tự trong `grpcsrv`.

**Giả định về part A (kiểm khi ráp, sai thì chỉ sửa dòng tạo rig/wiring):**
- memstore: `memstore.NewEdits() *memstore.Edits` (cài `store.Edits`, có `Append/At/Latest/History/Between/PurgeText`), `memstore.NewHidden() *memstore.Hidden` (cài `store.Hidden`), `(*memstore.Messages).ApplyEdit`, `(*memstore.Rooms).ClearHistory`, `Member` trả `ClearedBeforeSeq`, `Find/Page` trả `Version/Deleted/EditedAt`.
- `*mongostore.Store` cài đủ `mutate.Messages` (`Find/Last/ApplyEdit`), `store.Edits`, `store.Hidden`, `mutate.HistoryClearer`, `access.Rooms`, `effects.EditReader/EditApplier/TextPurger`. Dù part A tách `store.MessageEditor`/`store.HistoryClearer` hay gộp vào `store.Messages`/`store.Rooms`, part B chỉ dùng interface nhỏ cục bộ nên không phụ thuộc.
- `work.Record.Version uint32` và `store.EditInserted` (Task 5/6); `publish.Message` biết payload `msg_edited`/`msg_deleted` với subject `{root}.{t}.room.{rid}.msg_edited|msg_deleted` (Task 2).
- `pbconv.Message` **không** đặt `edited_at` khi `EditedAt.IsZero()` (nếu đặt, `TestHistoryPagesThroughWhatWasSent` cũ fail ở Task 9/10 — dừng và báo, sửa ở Task 2).

**Caller đã sửa trong part B:** `grpcsrv/harness_test.go`, `grpcsrv/fake_dependencies_test.go`, `grpcsrv/caller_identity_test.go` (deps + 5 RPC vào bảng caller), `apps/core/wiring.go` (bỏ import `grpcsrv`, gọi `wireService`), `effects/message_created.go` + `room_created.go` (`countAll`), `apps/core/metrics_wiring{,_test}.go`. Không caller nào khác dùng `grpcsrv.New`/`PageReader`/`awaitAcks` (đã grep).

**Rủi ro:**
- Chi phí fast path một lệnh sửa: `Admit` (2 đọc: room + member) + `Find` + `Allow` (policy, không đọc store) + `Latest` + `Append` (majority) + `ApplyEdit` (majority) + `Find` lại (+ `PurgeText` khi xoá) ≈ 4 đọc + 2–3 ghi, nhiều hơn ngân sách §6.3 (1 reverse scan + 2 ghi) ở phần đọc. Có thể bỏ `Find` lần hai bằng cách dựng snapshot từ `msg` + fact nếu đo thấy nặng; giữ theo hợp đồng cho đơn giản và đúng (snapshot luôn là doc thật).
- D86: `mutate` không có luật tác giả/owner; `access.DefaultPolicy` chỉ cho tác giả sửa/xoá, nên owner room và người tạo DM (`RoleOwner`) cũng không xoá được tin của người khác. Owner/moderator cần policy Phase 2.
- Policy chat (Phase 2) phải truyền vào **ba** chỗ (cả ba hiện mặc định `access.DefaultPolicy`): actor (`actor.WithPolicy`), checker của `grpcsrv` (`Deps.Policy`), checker của `mutate` (`service_wiring.go`). Nên gom về một biến trong wiring khi có policy thật.
- Retry cũ sau một lần sửa mới hơn (`BaseVersion + 1 < version hiện tại`) trả `ErrVersionConflict` dù lần đầu đã thành công; client phải đọc lại (D63 chấp nhận, chống ABA).
- `msg_changed` gửi lại mọi fact sau `RECONCILE_DELAY` (không ack mark): một publish trùng mỗi lệnh đổi, JetStream bỏ trong `EVT_STREAM_DUPLICATES` 5m. Tải NATS +1 msg/lệnh đổi.
- Hai cache loại room riêng (`msg_created`, `msg_changed`), mỗi cái tối đa `EFFECT_ROOM_CACHE` mục.
- Từ Task 5 (reader đẩy `EditInserted`) tới Task 11, worker gặp kind không có effect trong registry thì `runGroup` không chạy gì và **ack** record (`batch.go`: vòng effect rỗng, `failed` toàn false). Nghĩa là trong khoảng đó event sửa/xoá mất ở fast path không ai bù và projection không được chạy lại. Chỉ ảnh hưởng dev giữa chừng; không deploy `feat/m2b` giữa chừng.

**Part C cần biết (Task 12–16):**
- Task 12 resync: record `EditInserted` cần `Version`; quét `Edits.Between(room, from, to, limit)` (sắp `ts` rồi `_id`), id work `e:{room}-{th}-{seq}-v{ver}`; effect nhận record đó là `edit_projection` + `msg_changed` (đã đăng ký ở Task 11).
- Task 13 route client + corecli + e2e: 4 RPC đổi (`EditMessage`, `DeleteMessage`, `HideMessage`, `ClearHistory`) định tuyến theo slot của room như `SendMessage` (D82), retry an toàn vì lệnh mang `base_version` (retry cùng nội dung = thành công). `GetEditHistory` đọc như `GetHistory` (core nào cũng được). Lỗi cần phân biệt ở CLI: `FailedPrecondition` (conflict/đã xoá), `PermissionDenied` (policy từ chối, mặc định: không phải tác giả), `NotFound`.
- Task 14 itest gợi ý: (a) sửa/xoá qua gRPC trên Mongo thật → `GetHistory` thấy version/`deleted`, `GetEditHistory` đủ bản; (b) fact chèn thẳng vào `message_edits` (bỏ qua core) → worker `edit_projection` chiếu lên `messages` và `msg_changed` phát `msg_edited` id `{room}-0-{seq}-v1` lên `live.*`; (c) fast path enqueue bị từ chối → event vẫn tới sau `RECONCILE_DELAY` và `reconcile_republished_total{effect="msg_changed"}` tăng 1; khi fast path thành công thì không tăng. (d) sửa một tin cũ → `rooms.lc`/`ab` của room được nâng, `ls`/`lm` giữ nguyên (effect `room_activity` trên `EditInserted`).
- Task 15 docs: D82 (mutate, không actor, vẫn theo slot; checker riêng), D86 (quyền chỉ do `access.Policy`; `DefaultPolicy` chỉ tác giả sửa/xoá), D83 (event = snapshot hiện tại + id theo version fact; retry enqueue lại cùng id; projection worker delay 0; `msg_changed` không publish snapshot cũ hơn fact; `Republished` của `msg_changed` chỉ đếm id stream chưa có), D84 (record 37 byte), D85 (view placeholder; clear kẹp về `Last`). CLAUDE.md: thêm đoạn "Change path" (`mutate`), cập nhật "Permission hook and reader pipeline" (5 action mới, `MaskDeleted`, `HideForViewer`, `Viewer` mới, `GetHistory` một lần `HiddenIn`), "Detectors" (effect mới, `effectCounters.republished` có thể nil). Design §9.2 đổi "Chưa xây" của bước 2–3 thành đã xây.
- Không có biến môi trường mới trong part B; `msg_changed` dùng `RECONCILE_DELAY` (`cfg.EffectDelay`) và `EFFECT_ROOM_CACHE` sẵn có, nên luật boot về delay (D65) áp luôn cho nó.

**Đã chốt với owner (2026-10-05):**
- D86: mặc định không ai sửa/xoá tin của người khác, kể cả owner room hay người tạo DM; owner/moderator cần policy Phase 2.
- `GetEditHistory` với tin người đọc đã ẩn/clear: mặc định vẫn trả các bản (không xét ẩn/clear); policy có thể đổi (`ReadEditHistory` mang `Author`).

**Đã chốt (controller):** `Prev` chỉ ở fact sửa; `msg_changed` chỉ đếm PubAck không trùng; `room_activity` nhận `EditInserted` (seq 0). Task 12 resync nên ghi rõ: room chỉ có sửa/xoá vẫn có `act_bucket` mới nhờ effect này.

### Task 12: `/app resync` quét `message_edits`

Resync (D81) hiện chỉ dựng record cho room tạo trong khoảng và tin timeline chính. Sửa/xoá tin cũ không đổi `last_seq` (D70), nên fact trong `message_edits` phải được quét riêng theo `{r, ts}`: với mỗi room đã chọn, `Edits.Between(room, from, to, 1000)` lấy fact theo `ts` rồi `_id`, phân trang bằng cách dời `from` tới `ts` cuối trang (bao gồm). Mép trang có thể lặp lại các fact cùng `ts`, nên chỉ giữ tập id record của đúng `ts` cuối trang để bỏ trùng (bộ nhớ chặn bởi một trang). Một thời điểm có ≥ 1000 fact của một room thì không phân trang được: trả `ErrEditPageFull` thay vì lặp vô hạn. Mỗi fact → record `EditInserted{Room, Thread, Seq, Version, CommittedAt: fact.At}` (id `e:{room}-{th}-{seq}-v{ver}`, D84); worker chạy `edit_projection` + `msg_changed` như đường thường.

Thứ tự trong một room: room record → tin (ngược từ mới nhất) → fact sửa (tăng theo `ts`). Worker không dựa vào thứ tự này (projection CAS `v < ver`, event mang snapshot).

Room chỉ có **sửa/xoá** trong khoảng mất vẫn được chọn: registry của Task 11 chạy `room_activity` cho `EditInserted` (`Activity{Seq: 0}`, `TouchActivity` chỉ nâng `lc/ab`), nên `ActiveRooms` (`ab ≥ giờ(from)`) thấy room đó như room có tin mới. Giới hạn còn lại giống M2b.1: nếu chính write activity của fact sửa cũng mất trong khoảng đó và room không có thay đổi nào sau đó thì phải chạy `-room`.

Trước khi bắt đầu, kiểm tên adapter của part A (chỉ đọc):

```bash
grep -n "func NewEdits\|func (.*Edits) Between\|func (.*Edits) Append" apps/core/internal/store/memstore/*.go
grep -n "func (s \*Store) Between\|func (s \*Store) Append\|func (s \*Store) History" apps/core/internal/store/mongostore/*.go
grep -n "EditInserted" apps/core/internal/store/feed.go apps/core/internal/work/record.go
```

Expected: `memstore.NewEdits() *Edits` có `Append`/`Between`; `*mongostore.Store` có `Append`/`Between`/`History`; `store.EditInserted` và `work.Record.Version` tồn tại. Tên khác thì chỉ đổi tên trong snippet dưới đây và ghi vào báo cáo; thiếu hẳn port thì dừng và báo cáo.

`INDEXES.csv` có thay đổi chưa commit của owner (đổi đường dẫn docs/research) thì commit có pathspec `INDEXES.csv` sẽ kéo theo cả thay đổi đó: chạy `git diff --quiet -- INDEXES.csv` trước khi sửa; exit khác 0 → dừng, hỏi controller.

**Files:**
- Create: `apps/core/internal/resync/edits.go`
- Create: `apps/core/internal/resync/edits_test.go`
- Modify: `apps/core/internal/resync/scan.go` (`Deps`, `Report`, `room`)
- Modify: `apps/core/internal/resync/scan_test.go` (`world` có `edits`)
- Modify: `apps/core/resync_command.go` (`Edits: st`)
- Modify: `apps/core/resync_integration_test.go` (diễn tập có thêm một fact sửa)
- Modify: `INDEXES.csv`

**Step 1: Test**

`apps/core/internal/resync/scan_test.go`, ba chỗ:

```go
type world struct {
	rooms *memstore.Rooms
	msgs  *memstore.Messages
}
```

thành

```go
type world struct {
	rooms *memstore.Rooms
	msgs  *memstore.Messages
	edits *memstore.Edits
}
```

`	w := world{rooms: memstore.NewRooms(), msgs: memstore.NewMessages()}` thành `	w := world{rooms: memstore.NewRooms(), msgs: memstore.NewMessages(), edits: memstore.NewEdits()}`

`	return resync.Deps{Rooms: w.rooms, Pages: w.msgs, Pub: pub}` thành `	return resync.Deps{Rooms: w.rooms, Pages: w.msgs, Edits: w.edits, Pub: pub}`

Các test cũ giữ nguyên: không có fact sửa nên `EditRecords` = 0 và các `Report{...}` so sánh vẫn đúng.

`apps/core/internal/resync/edits_test.go`:

```go
package resync_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

func (w world) edit(t *testing.T, room, seq uint64, version uint32, at time.Time) string {
	t.Helper()
	e := domain.Edit{Room: room, Seq: seq, Version: version, Kind: domain.EditText, Tenant: "acme", By: "alice", Text: "edited", At: at}
	if version == 1 {
		e.Prev = "hi"
	}
	if err := w.edits.Append(t.Context(), e); err != nil {
		t.Fatalf("Append(%d/%d v%d): %v", room, seq, version, err)
	}
	return work.Record{Kind: store.EditInserted, Room: room, Seq: seq, Version: version}.ID()
}

func TestResyncPublishesEditsOfTheLostRangeAfterTheTimeline(t *testing.T) {
	w := newWorld(t)
	inRange := w.edit(t, busyRoom, 40, 1, lostFrom.Add(5*time.Minute))
	w.edit(t, busyRoom, 40, 2, lostTo.Add(time.Minute))
	w.edit(t, busyRoom, 3, 1, lostFrom.Add(-time.Minute))
	pub := &publishSpy{}
	rep, err := resync.Run(t.Context(), w.deps(pub), target, resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Rate: resync.MaxRate})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := (resync.Report{Rooms: 2, RoomRecords: 1, MessageRecords: 61, EditRecords: 1}); rep != want {
		t.Fatalf("report = %+v, want %+v", rep, want)
	}
	got := pub.published()
	roomRecord := work.Record{Kind: store.RoomInserted, Room: newRoom}.ID()
	if len(got) != 63 || got[61] != inRange || got[62] != roomRecord {
		t.Fatalf("published %d ids ending %v, want 61 messages, then %s, then %s", len(got), got[max(0, len(got)-3):], inRange, roomRecord)
	}
	if rep.String() != "resync rooms=2 room_records=1 message_records=61 edit_records=1 dry_run=false" {
		t.Fatalf("report line = %q", rep.String())
	}
}

func TestResyncPagesEditsByTimeWithoutRepeatingThePageEdge(t *testing.T) {
	w := newWorld(t)
	for v := range uint32(1200) {
		w.edit(t, staleRoom, 1, v+1, lostFrom.Add(time.Duration((v+1)/3)*time.Millisecond))
	}
	opts := resync.Options{From: lostFrom, To: lostTo, Room: staleRoom, Rate: 1, DryRun: true}
	rep, err := resync.Run(t.Context(), w.deps(&publishSpy{}), target, opts)
	if err != nil || rep != (resync.Report{Rooms: 1, EditRecords: 1200, DryRun: true}) {
		t.Fatalf("Run = %+v, %v; want 1200 edit records, each once", rep, err)
	}
}

func TestResyncStopsWhenOneInstantHoldsMoreEditsThanAPage(t *testing.T) {
	w := newWorld(t)
	at := lostFrom.Add(time.Minute)
	for v := range uint32(1001) {
		w.edit(t, staleRoom, 1, v+1, at)
	}
	opts := resync.Options{From: lostFrom, To: lostTo, Room: staleRoom, Rate: 1, DryRun: true}
	rep, err := resync.Run(t.Context(), w.deps(&publishSpy{}), target, opts)
	if !errors.Is(err, resync.ErrEditPageFull) || rep.EditRecords != 1000 {
		t.Fatalf("Run = %+v, %v; want ErrEditPageFull after one full page", rep, err)
	}
}
```

Dữ liệu: fact v của `staleRoom` seq 1 có `ts = lostFrom + floor(v/3) ms`, nên mỗi ms có 3 fact; trang 1 (1000 fact) kết thúc ở v1000 (`ts` 333ms, cùng ms với v999 và v1001), trang 2 bắt đầu lại từ 333ms và phải bỏ v999, v1000. Không bỏ trùng thì đếm 1202. Tin của `staleRoom` cũ hơn `lostFrom` nên `MessageRecords` = 0; dry run nên không chờ ticker.

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/resync/..."`
Expected: FAIL biên dịch: `unknown field Edits in struct literal of type resync.Deps`, `unknown field EditRecords in struct literal of type resync.Report`, `undefined: resync.ErrEditPageFull`.

**Step 3: Code**

`apps/core/internal/resync/scan.go`:

```go
type Deps struct {
	Rooms Rooms
	Pages Pages
	Pub   Publisher
}
```

thành

```go
type Deps struct {
	Rooms Rooms
	Pages Pages
	Edits Edits
	Pub   Publisher
}
```

```go
type Report struct {
	Rooms, RoomRecords, MessageRecords int
	DryRun                             bool
}

func (r Report) String() string {
	return fmt.Sprintf("resync rooms=%d room_records=%d message_records=%d dry_run=%t", r.Rooms, r.RoomRecords, r.MessageRecords, r.DryRun)
}
```

thành

```go
type Report struct {
	Rooms, RoomRecords, MessageRecords, EditRecords int
	DryRun                                          bool
}

func (r Report) String() string {
	return fmt.Sprintf("resync rooms=%d room_records=%d message_records=%d edit_records=%d dry_run=%t", r.Rooms, r.RoomRecords, r.MessageRecords, r.EditRecords, r.DryRun)
}
```

Trong `func (s *scanner) room`, dòng cuối `	return s.timeline(ctx, r.ID)` thành:

```go
	if err := s.timeline(ctx, r.ID); err != nil {
		return err
	}
	return s.edits(ctx, r.ID)
```

`apps/core/internal/resync/edits.go`:

```go
package resync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

const editPage = 1000

var ErrEditPageFull = errors.New("resync: one instant holds more edits than an edit page")

type Edits interface {
	Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Edit, error)
}

func (s *scanner) edits(ctx context.Context, room uint64) error {
	from, edge := s.opts.From, map[string]bool{}
	for {
		page, err := s.deps.Edits.Between(ctx, room, from, s.opts.To, editPage)
		if err != nil {
			return fmt.Errorf("edits of room %d from %s: %w", room, from.Format(time.RFC3339Nano), err)
		}
		next, err := s.emitEdits(ctx, page, edge)
		if err != nil || len(page) < editPage {
			return err
		}
		last := page[len(page)-1].At
		if !last.After(from) {
			return fmt.Errorf("%w: room %d at %s", ErrEditPageFull, room, last.Format(time.RFC3339Nano))
		}
		from, edge = last, next
	}
}

func (s *scanner) emitEdits(ctx context.Context, page []domain.Edit, edge map[string]bool) (map[string]bool, error) {
	next := map[string]bool{}
	for _, e := range page {
		rec := work.Record{Kind: store.EditInserted, Room: e.Room, Thread: e.Thread, Seq: e.Seq, Version: e.Version, CommittedAt: e.At}
		id := rec.ID()
		if e.At.Equal(page[len(page)-1].At) {
			next[id] = true
		}
		if edge[id] {
			continue
		}
		if err := s.emit(ctx, rec); err != nil {
			return nil, err
		}
		s.rep.EditRecords++
	}
	return next, nil
}
```

`editPage` = 1000 = trần `limit` của `Between` theo hợp đồng (1..1000). Nếu Task 3 đã đặt hằng cho trần này (vd. `store.MaxEditScan`) thì dùng hằng đó thay số 1000.

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/resync/..."`
Expected: PASS. `wc -l apps/core/internal/resync/*.go` → mỗi file < 200 (scan.go ~158).

**Step 5: Wiring + diễn tập**

`apps/core/resync_command.go`: `	rep, err := resync.Run(ctx, resync.Deps{Rooms: st, Pages: st, Pub: c.js}, target, opts)` thành `	rep, err := resync.Run(ctx, resync.Deps{Rooms: st, Pages: st, Edits: st, Pub: c.js}, target, opts)`.

`apps/core/resync_integration_test.go`:
- Ngay sau vòng `for i, res := range st.Insert(t.Context(), missed) { ... }` và trước `assertNoLiveIDs(...)`, chèn:

```go
	edit := domain.Edit{
		Room: room, Seq: 1, Version: 1, Kind: domain.EditText, Tenant: itTenant, By: "migrator",
		Text: "edited while the reader was down", Prev: "missed by the reader", At: time.Now().UTC().Truncate(time.Millisecond),
	}
	if err := st.Append(t.Context(), edit); err != nil {
		t.Fatalf("append an edit the reader missed: %v", err)
	}
	want = append(want, pbconv.MessageChangeEventID(room, 0, 1, 1))
```

- Thay

```go
	if got := strings.TrimSpace(out.String()); got != "resync rooms=1 room_records=1 message_records=3 dry_run=false" {
		t.Fatalf("resync output = %q, want one room, its room record and three message records", got)
	}
	awaitLiveIDs(t, live, ran, want...)
```

bằng

```go
	if got := strings.TrimSpace(out.String()); got != "resync rooms=1 room_records=1 message_records=3 edit_records=1 dry_run=false" {
		t.Fatalf("resync output = %q, want one room, its room record, three message records and one edit record", got)
	}
	awaitLiveIDs(t, live, ran, want...)
	got, err := st.Find(t.Context(), room, []store.MsgKey{{Room: room, Seq: 1}})
	if err != nil || len(got) != 1 || got[0].Version != 1 || got[0].Text != edit.Text {
		t.Fatalf("seq 1 after resync = %+v, %v; want the missed edit projected at version 1", got, err)
	}
```

Reader tắt nên fact sửa ghi thẳng vào Mongo không được chiếu (projection) cũng không có event; resync đẩy record `e:`; worker chạy `room_activity` → `edit_projection` (delay 0) → `msg_changed` (delay 2s) theo thứ tự registry, nên khi live event `{room}-0-1-v1` tới thì projection đã xong.

**Step 6: Chạy**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/ ./apps/core/internal/resync/..."`
Expected: PASS (itest skip).

Run: `make infra-up && make itest`
Expected: mọi package `ok`, gồm `TestRealInfraResyncDrillRepublishesWritesTheReaderMissed` (log `live events [... <room>-0-1-v1] arrived …`).

**Step 7: INDEXES.csv + commit**

- Thay cả dòng bắt đầu bằng `apps/core/internal/resync,` bằng:

```csv
apps/core/internal/resync,package,"Manual resync of a lost change feed range (/app resync): flags -from/-to (RFC3339), -tenant, -room, -rate (default 500/s, max 10000), -dry-run; walks rooms by store.ActiveRooms (activity bucket since -from or created in range, 500 per page) or one room; per room emits RoomInserted if created in range, MessageInserted for main-timeline messages created in range (backward Page scan, stops before -from), then EditInserted for its message_edits facts with ts in range (Edits.Between, 1000 per page, paged by ts, the page edge deduped by record id; ErrEditPageFull when one instant fills a page); publishes work records synchronously at -rate; prints counts",ParseArgs;Options;Options.Validate;Run;Deps;Target;Report;Rooms;Pages;Edits;Publisher;ErrUsage;ErrEditPageFull;DefaultRate;MaxRate,apps/core,unit (memstore + fake publisher; synctest pacing);itest drill (apps/core),D69;D70;D81;D84
```

- Dòng `apps/core`: thay chuỗi `itest (resync drill republishes writes the reader missed)` bằng `itest (resync drill republishes messages and an edit the reader missed)`.

Kiểm: `python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"` → Expected: `{7}`.

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/resync/edits.go apps/core/internal/resync/edits_test.go
git commit -m "feat(core): resync replays message_edits facts of the lost range" -- apps/core/internal/resync/ apps/core/resync_command.go apps/core/resync_integration_test.go INDEXES.csv
```

---

### Task 13: Route client + corecli + e2e sửa/xoá

Tool cần gọi 5 RPC mới theo slot của room, và e2e cần chứng minh sửa/xoá trên cụm hai core: lịch sử thấy bản mới, lịch sử sửa đúng, live có `msg_edited`/`msg_deleted` với id theo version.

Chính sách retry: cả 5 lệnh đều theo room (`roomRoute`) và dùng `retryIdempotent` như `SendMessage`:
- `EditMessage`/`DeleteMessage`: idempotent nhờ `base_version` (D63): gửi lại cùng base + cùng nội dung sau khi lần đầu đã ghi thì core nhận ra retry và trả thành công; conflict thật là `FAILED_PRECONDITION`, không retry.
- `HideMessage`: upsert; `ClearHistory`: `$max`; `GetEditHistory`: đọc.

Không có test "thấy fail" cho `tools/corecli` (binary, không unit test); `tools/internal/route` và `tools/corecli/internal/e2e` theo TDD.

`fakeCore` trong `tools/internal/route/fakes_test.go` là `chatimv1.CoreServiceClient`; từ Task 2 interface có thêm 5 method. Kiểm trước: `grep -n "EditMessage\|CoreServiceClient" tools/internal/route/fakes_test.go`. Nếu Task 2 đã thêm stub 5 method này vào `fakes_test.go` thì xoá các stub đó (file mới dưới đây định nghĩa đủ); nếu Task 2 nhúng `chatimv1.CoreServiceClient` vào `fakeCore` thì giữ nguyên (method tường minh thắng method nhúng).

Kiểm `git diff --quiet -- INDEXES.csv` trước khi sửa (như Task 12).

**Files:**
- Create: `tools/internal/route/changes.go`
- Create: `tools/internal/route/changes_test.go`
- Create: `tools/internal/route/fakes_change_test.go`
- Modify: `tools/internal/route/retry.go` (helper `inRoom`), `tools/internal/route/client.go` (`SendMessage`, `GetHistory` dùng `inRoom`)
- Create: `tools/corecli/internal/e2e/changes.go`, `tools/corecli/internal/e2e/changes_test.go`
- Modify: `tools/corecli/internal/e2e/events.go` (viết lại), `events_test.go` (viết lại), `check.go`, `check_test.go`, `state.go`, `state_test.go`
- Create: `tools/corecli/cmd_change.go`, `tools/corecli/e2e_change.go`
- Modify: `tools/corecli/main.go`, `tools/corecli/cmd_e2e.go`, `tools/corecli/e2e_check.go`
- Modify: `scripts/e2e.sh`
- Modify: `INDEXES.csv`

**Step 1: Test route**

`tools/internal/route/fakes_change_test.go`:

```go
package route_test

import (
	"context"

	"google.golang.org/grpc"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (f *fakeCore) EditMessage(ctx context.Context, in *chatimv1.EditMessageRequest, _ ...grpc.CallOption) (*chatimv1.EditMessageResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	m := &chatimv1.Message{RoomId: in.GetRoomId(), Seq: in.GetSeq(), Version: in.GetBaseVersion() + 1, Text: in.GetText()}
	return &chatimv1.EditMessageResponse{Message: m}, nil
}

func (f *fakeCore) DeleteMessage(ctx context.Context, in *chatimv1.DeleteMessageRequest, _ ...grpc.CallOption) (*chatimv1.DeleteMessageResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	m := &chatimv1.Message{RoomId: in.GetRoomId(), Seq: in.GetSeq(), Version: in.GetBaseVersion() + 1, Deleted: true}
	return &chatimv1.DeleteMessageResponse{Message: m}, nil
}

func (f *fakeCore) HideMessage(ctx context.Context, _ *chatimv1.HideMessageRequest, _ ...grpc.CallOption) (*chatimv1.HideMessageResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.HideMessageResponse{}, nil
}

func (f *fakeCore) ClearHistory(ctx context.Context, in *chatimv1.ClearHistoryRequest, _ ...grpc.CallOption) (*chatimv1.ClearHistoryResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.ClearHistoryResponse{ClearedBeforeSeq: in.GetUpToSeq()}, nil
}

func (f *fakeCore) GetEditHistory(ctx context.Context, in *chatimv1.GetEditHistoryRequest, _ ...grpc.CallOption) (*chatimv1.GetEditHistoryResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.GetEditHistoryResponse{Versions: []*chatimv1.MessageVersion{{Version: in.GetAfterVersion() + 1}}}, nil
}
```

`tools/internal/route/changes_test.go`:

```go
package route_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

type roomLocator struct {
	mu    sync.Mutex
	rooms []uint64
}

func (l *roomLocator) Addr(room uint64) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rooms = append(l.rooms, room)
	return "core-1:9000", true
}

func (l *roomLocator) AnyAddr() (string, bool) { return "", false }

func (l *roomLocator) Refresh(context.Context) error { return nil }

func (l *roomLocator) asked() []uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.rooms)
}

type changeCall func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error)

func reply[T proto.Message](resp T, st route.Stats, err error) (proto.Message, route.Stats, error) {
	return resp, st, err
}

var changeCalls = map[string]changeCall{
	"edit": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.EditMessage(ctx, &chatimv1.EditMessageRequest{RoomId: room, Seq: 3, BaseVersion: 1, Text: "new"}))
	},
	"delete": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.DeleteMessage(ctx, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 3, BaseVersion: 2}))
	},
	"hide": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.HideMessage(ctx, &chatimv1.HideMessageRequest{RoomId: room, Seq: 3}))
	},
	"clear": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.ClearHistory(ctx, &chatimv1.ClearHistoryRequest{RoomId: room, UpToSeq: 9}))
	},
	"edits": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.GetEditHistory(ctx, &chatimv1.GetEditHistoryRequest{RoomId: room, Seq: 3, AfterVersion: 4, Limit: 10}))
	},
}

var changeReplies = map[string]proto.Message{
	"edit":   &chatimv1.EditMessageResponse{Message: &chatimv1.Message{RoomId: "42", Seq: 3, Version: 2, Text: "new"}},
	"delete": &chatimv1.DeleteMessageResponse{Message: &chatimv1.Message{RoomId: "42", Seq: 3, Version: 3, Deleted: true}},
	"hide":   &chatimv1.HideMessageResponse{},
	"clear":  &chatimv1.ClearHistoryResponse{ClearedBeforeSeq: 9},
	"edits":  &chatimv1.GetEditHistoryResponse{Versions: []*chatimv1.MessageVersion{{Version: 5}}},
}

func TestChangeCallsRouteByRoomAndRetryAttemptTimeouts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		core := &fakeCore{}
		loc := &roomLocator{}
		c := newClient(t, loc, newFakeNet(map[string]*fakeCore{"core-1:9000": core}), route.Policy{Attempt: time.Second})
		for name, call := range changeCalls {
			core.block = 1
			got, st, err := call(t.Context(), c, "42")
			if err != nil || st.Attempts != 2 || !proto.Equal(got, changeReplies[name]) {
				t.Fatalf("%s = %v, %v after %d attempts; want %v on the second attempt", name, got, err, st.Attempts, changeReplies[name])
			}
		}
		rooms := loc.asked()
		if len(rooms) != 2*len(changeCalls) || slices.ContainsFunc(rooms, func(r uint64) bool { return r != 42 }) {
			t.Fatalf("locator asked for rooms %v, want room 42 on every attempt", rooms)
		}
	})
}

func TestChangeCallsKeepConflictsAndBadRoomIDsLocal(t *testing.T) {
	core := &fakeCore{fail: []error{status.Error(codes.FailedPrecondition, "message version conflict")}}
	c := newClient(t, &roomLocator{}, newFakeNet(map[string]*fakeCore{"core-1:9000": core}), route.Policy{})
	if _, st, err := changeCalls["edit"](t.Context(), c, "42"); status.Code(err) != codes.FailedPrecondition || st.Attempts != 1 {
		t.Fatalf("edit on a stale base = %v after %d attempts, want FailedPrecondition at once", err, st.Attempts)
	}
	for name, call := range changeCalls {
		if _, st, err := call(t.Context(), c, "not-a-room"); status.Code(err) != codes.InvalidArgument || st.Attempts != 0 {
			t.Fatalf("%s with a bad room id = %v with %+v, want a local InvalidArgument", name, err, st)
		}
	}
}
```

`roomLocator.AnyAddr` trả `false`: nếu một lệnh đi đường `AnyAddr` thay vì theo room thì không có route và test fail.

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./tools/internal/route/..."`
Expected: FAIL biên dịch: `c.EditMessage undefined (type *route.Client has no field or method EditMessage)` (và 4 method còn lại).

**Step 3: Code route**

`tools/internal/route/retry.go`, thêm sau hàm `attempt`:

```go
func inRoom[T any](ctx context.Context, c *Client, roomID string, do rpc[T]) (T, Stats, error) {
	pick, err := c.roomRoute(roomID)
	if err != nil {
		var zero T
		return zero, Stats{}, err
	}
	return call(ctx, c, pick, retryIdempotent, do)
}
```

`tools/internal/route/client.go`, thay hai hàm:

```go
func (c *Client) SendMessage(ctx context.Context, req *chatimv1.SendMessageRequest) (*chatimv1.SendMessageResponse, Stats, error) {
	pick, err := c.roomRoute(req.GetRoomId())
	if err != nil {
		return nil, Stats{}, err
	}
	return call(ctx, c, pick, retryIdempotent, func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.SendMessageResponse, error) {
		return api.SendMessage(ctx, req)
	})
}

func (c *Client) GetHistory(ctx context.Context, req *chatimv1.GetHistoryRequest) (*chatimv1.GetHistoryResponse, Stats, error) {
	pick, err := c.roomRoute(req.GetRoomId())
	if err != nil {
		return nil, Stats{}, err
	}
	return call(ctx, c, pick, retryIdempotent, func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.GetHistoryResponse, error) {
		return api.GetHistory(ctx, req)
	})
}
```

bằng

```go
func (c *Client) SendMessage(ctx context.Context, req *chatimv1.SendMessageRequest) (*chatimv1.SendMessageResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.SendMessageResponse, error) {
		return api.SendMessage(ctx, req)
	})
}

func (c *Client) GetHistory(ctx context.Context, req *chatimv1.GetHistoryRequest) (*chatimv1.GetHistoryResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.GetHistoryResponse, error) {
		return api.GetHistory(ctx, req)
	})
}
```

`tools/internal/route/changes.go`:

```go
package route

import (
	"context"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (c *Client) EditMessage(ctx context.Context, req *chatimv1.EditMessageRequest) (*chatimv1.EditMessageResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.EditMessageResponse, error) {
		return api.EditMessage(ctx, req)
	})
}

func (c *Client) DeleteMessage(ctx context.Context, req *chatimv1.DeleteMessageRequest) (*chatimv1.DeleteMessageResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.DeleteMessageResponse, error) {
		return api.DeleteMessage(ctx, req)
	})
}

func (c *Client) HideMessage(ctx context.Context, req *chatimv1.HideMessageRequest) (*chatimv1.HideMessageResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.HideMessageResponse, error) {
		return api.HideMessage(ctx, req)
	})
}

func (c *Client) ClearHistory(ctx context.Context, req *chatimv1.ClearHistoryRequest) (*chatimv1.ClearHistoryResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.ClearHistoryResponse, error) {
		return api.ClearHistory(ctx, req)
	})
}

func (c *Client) GetEditHistory(ctx context.Context, req *chatimv1.GetEditHistoryRequest) (*chatimv1.GetEditHistoryResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.GetEditHistoryResponse, error) {
		return api.GetEditHistory(ctx, req)
	})
}
```

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./tools/internal/route/..."`
Expected: PASS (gồm các test cũ `TestSendRetries…`, `TestInvalidRoomIDsAndSettingsAreRejectedLocally` qua `inRoom`). `wc -l tools/internal/route/*.go` → client.go ~132, retry.go ~72.

**Step 5: Commit route**

INDEXES.csv: thay cả dòng bắt đầu bằng `tools/internal/route,` bằng:

```csv
tools/internal/route,package,"Slot-routed gRPC client for tools: Resolver + one conn per address; retries Unavailable with jitter and keeps the same cid; room-routed calls (SendMessage, GetHistory, EditMessage, DeleteMessage, HideMessage, ClearHistory, GetEditHistory) also retry attempt timeouts, ResourceExhausted and Aborted because they are idempotent (cid, base_version, upsert, $max or a read); Open/Session bundles state Redis (with RedisPassword) + resolver + client",Open;Session;SessionConfig;Client;New;Policy;DialInsecure;WithCaller;Locator;Dialer;Client.EditMessage;Client.DeleteMessage;Client.HideMessage;Client.ClearHistory;Client.GetEditHistory,tools/corecli;tools/poc/corebench,unit;synctest;goleak,D43;D63
```

```bash
make fmt-check && make vet && make lint
git add tools/internal/route/changes.go tools/internal/route/changes_test.go tools/internal/route/fakes_change_test.go
git commit -m "feat(route): route edit, delete, hide, clear and edit history by room" -- tools/internal/route/ INDEXES.csv
```

(Nếu đã xoá stub của Task 2 trong `fakes_test.go` thì file đó nằm trong pathspec `tools/internal/route/`.)

**Step 6: Test package e2e**

`tools/corecli/internal/e2e/events_test.go` (viết lại cả file):

```go
package e2e_test

import (
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

func TestEventOfKeepsMessageEventsOnly(t *testing.T) {
	const subject = "live.e2e.room.42.evt.msg_created"
	created := &chatimv1.Event{
		Id: "42-0-3", RoomId: "42", Seq: 3,
		Payload: &chatimv1.Event_MessageCreated{MessageCreated: &chatimv1.MessageCreated{Message: &chatimv1.Message{Cid: "a-c"}}},
	}
	want := e2e.Event{Kind: e2e.KindCreated, Room: "42", ID: "42-0-3", Seq: 3, CID: "a-c", Subject: subject}
	if got, ok := e2e.EventOf(subject, created); !ok || got != want {
		t.Fatalf("EventOf(msg_created) = %+v, %v; want %+v, true", got, ok, want)
	}
	edited := &chatimv1.Event{Id: "42-0-3-v1", RoomId: "42", Payload: &chatimv1.Event_MessageEdited{MessageEdited: &chatimv1.MessageEdited{
		Version: 1, Message: &chatimv1.Message{RoomId: "42", Seq: 3, Cid: "a-c", Text: "new", Version: 1},
	}}}
	wantEdited := e2e.Event{Kind: e2e.KindEdited, Room: "42", ID: "42-0-3-v1", Seq: 3, CID: "a-c", Version: 1, Text: "new", Subject: "s"}
	if got, ok := e2e.EventOf("s", edited); !ok || got != wantEdited || !got.IsChange() {
		t.Fatalf("EventOf(msg_edited) = %+v, %v; want %+v, true", got, ok, wantEdited)
	}
	deleted := &chatimv1.Event{Id: "42-0-3-v2", RoomId: "42", Payload: &chatimv1.Event_MessageDeleted{MessageDeleted: &chatimv1.MessageDeleted{
		Version: 2, Message: &chatimv1.Message{RoomId: "42", Seq: 3, Cid: "a-c", Deleted: true, Version: 2},
	}}}
	wantDeleted := e2e.Event{Kind: e2e.KindDeleted, Room: "42", ID: "42-0-3-v2", Seq: 3, CID: "a-c", Version: 2, Subject: "s"}
	if got, ok := e2e.EventOf("s", deleted); !ok || got != wantDeleted || !got.IsChange() {
		t.Fatalf("EventOf(msg_deleted) = %+v, %v; want %+v, true", got, ok, wantDeleted)
	}
	room := &chatimv1.Event{
		Id: "42-created", RoomId: "42",
		Payload: &chatimv1.Event_RoomCreated{RoomCreated: &chatimv1.RoomCreated{Room: &chatimv1.Room{Id: "42"}}},
	}
	if got, ok := e2e.EventOf("live.e2e.room.42.evt.room_created", room); ok {
		t.Fatalf("EventOf(room_created) = %+v, true; want it skipped", got)
	}
	if got, ok := e2e.EventOf(subject, &chatimv1.Event{Id: "42-0-4", RoomId: "42", Seq: 4}); ok {
		t.Fatalf("EventOf(no payload) = %+v, true; want it skipped", got)
	}
}
```

`tools/corecli/internal/e2e/state_test.go`, hai chỗ:
- `	want := e2e.State{Tenant: "e2e", User: "alice", Room: room, Owner: "core-1", Acks: acks(3)}` thành `	want := e2e.State{Tenant: "e2e", User: "alice", Room: room, Owner: "core-1", Acks: acks(3), Changes: []e2e.Change{e2e.EditOf(1), e2e.DeleteOf(2)}}`
- `	want := e2e.Event{Room: room, ID: "42-0-4", Seq: 4, CID: "a-4", Subject: "live.e2e.room.42.evt.msg_created"}` thành `	want := e2e.Event{Kind: e2e.KindCreated, Room: room, ID: "42-0-4", Seq: 4, CID: "a-4", Subject: "live.e2e.room.42.evt.msg_created"}`

`tools/corecli/internal/e2e/check_test.go`:
- Thay mọi `e2e.CheckPage(want, ` bằng `e2e.CheckPage(want, nil, ` (4 chỗ, trong `TestCheckPageWantsExactlyTheAckedMessagesInSeqOrder`).
- Trong map `cases` của test đó, thêm sau dòng `"thread": ...`:

```go
		"version": func(m *chatimv1.Message) { m.Version = 1 },
		"hidden":  func(m *chatimv1.Message) { m.Hidden = true },
```

(chạy gofmt nếu căn cột lệch: `make -s go ARGS="fmt ./tools/corecli/..."`).

`tools/corecli/internal/e2e/changes_test.go`:

```go
package e2e_test

import (
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

func changedPage(want []e2e.Ack) []*chatimv1.Message {
	got := messagesOf(want)
	got[0].Text, got[0].Version = e2e.EditTextFor(1), 1
	got[1].Text, got[1].Version, got[1].Deleted = "", 1, true
	return got
}

func TestCheckPageExpectsTheEditedAndDeletedContent(t *testing.T) {
	want := acks(3)
	changes := []e2e.Change{e2e.EditOf(1), e2e.DeleteOf(2)}
	if err := e2e.CheckPage(want, changes, changedPage(want), room, sender); err != nil {
		t.Fatalf("CheckPage(changed) = %v", err)
	}
	expectErr(t, e2e.CheckPage(want, nil, changedPage(want), room, sender), "text")
	expectErr(t, e2e.CheckPage(want, changes, messagesOf(want), room, sender), "text")
	undeleted := changedPage(want)
	undeleted[1].Deleted = false
	expectErr(t, e2e.CheckPage(want, changes, undeleted, room, sender), "deleted")
	hidden := changedPage(want)
	hidden[2].Hidden = true
	expectErr(t, e2e.CheckPage(want, changes, hidden, room, sender), "hidden")
}

func TestCheckChangeEventsFindsEachChangeByID(t *testing.T) {
	changes := []e2e.Change{e2e.EditOf(1), e2e.DeleteOf(2)}
	edited := e2e.Event{Kind: e2e.KindEdited, Room: room, ID: e2e.ChangeEventID(room, 1, 1), Seq: 1, Version: 1, Text: e2e.EditTextFor(1)}
	deleted := e2e.Event{Kind: e2e.KindDeleted, Room: room, ID: e2e.ChangeEventID(room, 2, 1), Seq: 2, Version: 1}
	created := e2e.Event{Kind: e2e.KindCreated, Room: room, ID: e2e.MessageEventID(room, 1), Seq: 1, CID: "x"}
	if got := e2e.ChangeEventID("42", 1, 1); got != "42-0-1-v1" {
		t.Fatalf("ChangeEventID = %q, want 42-0-1-v1", got)
	}
	missing, err := e2e.CheckChangeEvents(room, changes, []e2e.Event{created, edited})
	if err != nil || len(missing) != 1 || missing[0] != deleted.ID {
		t.Fatalf("CheckChangeEvents = %v, %v; want only %s missing", missing, err, deleted.ID)
	}
	if missing, err := e2e.CheckChangeEvents(room, changes, []e2e.Event{edited, deleted, edited}); err != nil || len(missing) != 0 {
		t.Fatalf("CheckChangeEvents(all, one duplicate) = %v, %v; want none missing", missing, err)
	}
	bad := map[string]e2e.Event{
		"unexpected":    {Kind: e2e.KindEdited, Room: room, ID: e2e.ChangeEventID(room, 3, 1), Seq: 3, Version: 1},
		"is msg_edited": {Kind: e2e.KindEdited, Room: room, ID: deleted.ID, Seq: 2, Version: 1},
		"carries":       {Kind: e2e.KindEdited, Room: room, ID: edited.ID, Seq: 9, Version: 1, Text: edited.Text},
		"text":          {Kind: e2e.KindEdited, Room: room, ID: edited.ID, Seq: 1, Version: 1, Text: "other"},
	}
	for part, ev := range bad {
		_, err := e2e.CheckChangeEvents(room, changes, []e2e.Event{ev})
		expectErr(t, err, part)
	}
}

func TestCheckVersionsWantsTheOriginalThenTheEdit(t *testing.T) {
	as := acks(2)
	at := timestamppb.Now()
	good := []*chatimv1.MessageVersion{
		{Version: 0, Kind: chatimv1.EditKind_EDIT_KIND_ORIGINAL, Text: e2e.TextFor(as[0].CID), By: sender, At: at},
		{Version: 1, Kind: chatimv1.EditKind_EDIT_KIND_TEXT, Text: e2e.EditTextFor(1), By: sender, At: at},
	}
	if err := e2e.CheckVersions(as, e2e.EditOf(1), good, sender); err != nil {
		t.Fatalf("CheckVersions(edit) = %v", err)
	}
	expectErr(t, e2e.CheckVersions(as, e2e.EditOf(1), good[:1], sender), "1 versions")
	expectErr(t, e2e.CheckVersions(as, e2e.EditOf(1), good, "bob"), "by")
	expectErr(t, e2e.CheckVersions(as, e2e.EditOf(5), good, sender), "never acked")
	if err := e2e.CheckVersions(as, e2e.DeleteOf(2), nil, sender); err != nil {
		t.Fatalf("CheckVersions(delete, none) = %v", err)
	}
	expectErr(t, e2e.CheckVersions(as, e2e.DeleteOf(2), good, sender), "want none")
}
```

**Step 7: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./tools/corecli/internal/e2e/..."`
Expected: FAIL biên dịch: `undefined: e2e.KindCreated`, `undefined: e2e.Change`, `too many arguments in call to e2e.CheckPage`.

**Step 8: Code package e2e**

`tools/corecli/internal/e2e/state.go`: trong `type State struct`, thêm sau dòng `	Acks   []Ack  `json:"acks"``:

```go
	Changes []Change `json:"changes,omitempty"`
```

(gofmt căn lại cột của cả struct.)

`tools/corecli/internal/e2e/events.go` (viết lại cả file):

```go
package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type Event struct {
	Kind    string `json:"kind,omitempty"`
	Room    string `json:"room"`
	ID      string `json:"id"`
	Seq     uint64 `json:"seq"`
	CID     string `json:"cid"`
	Version uint32 `json:"version,omitempty"`
	Text    string `json:"text,omitempty"`
	Subject string `json:"subject,omitempty"`
}

func (e Event) IsChange() bool { return e.Kind == KindEdited || e.Kind == KindDeleted }

func EventOf(subject string, ev *chatimv1.Event) (Event, bool) {
	if created := ev.GetMessageCreated(); created != nil {
		return Event{Kind: KindCreated, Room: ev.GetRoomId(), ID: ev.GetId(), Seq: ev.GetSeq(), CID: created.GetMessage().GetCid(), Subject: subject}, true
	}
	if edited := ev.GetMessageEdited(); edited != nil {
		return changeOf(subject, ev, KindEdited, edited.GetMessage(), edited.GetVersion()), true
	}
	if deleted := ev.GetMessageDeleted(); deleted != nil {
		return changeOf(subject, ev, KindDeleted, deleted.GetMessage(), deleted.GetVersion()), true
	}
	return Event{}, false
}

func changeOf(subject string, ev *chatimv1.Event, kind string, m *chatimv1.Message, version uint32) Event {
	return Event{Kind: kind, Room: m.GetRoomId(), ID: ev.GetId(), Seq: m.GetSeq(), CID: m.GetCid(), Version: version, Text: m.GetText(), Subject: subject}
}

func ReadEvents(path string) ([]Event, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read events: %w", err)
	}
	if end := bytes.LastIndexByte(data, '\n'); end >= 0 {
		data = data[:end]
	} else {
		data = nil
	}
	var out []Event
	for i, line := range bytes.Split(data, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, fmt.Errorf("events line %d: %w", i+1, err)
		}
		out = append(out, ev)
	}
	return out, nil
}
```

Event thay đổi lấy room/seq từ snapshot trong payload (không phụ thuộc envelope có điền `room_id`/`seq` hay không). Dòng JSON cũ không có `kind` đọc ra `Kind == ""`, được coi là `msg_created` (`IsChange` false), nên `CheckEvents` không đổi hành vi.

`tools/corecli/internal/e2e/check.go`:
- Thay

```go
func CheckPage(want []Ack, got []*chatimv1.Message, room, sender string) error {
	if len(got) != len(want) {
		return fmt.Errorf("history returned %d messages, want %d", len(got), len(want))
	}
	for i, m := range got {
		a := want[i]
		switch {
```

bằng

```go
func CheckPage(want []Ack, changes []Change, got []*chatimv1.Message, room, sender string) error {
	if len(got) != len(want) {
		return fmt.Errorf("history returned %d messages, want %d", len(got), len(want))
	}
	bySeq := make(map[uint64]Change, len(changes))
	for _, c := range changes {
		bySeq[c.Seq] = c
	}
	for i, m := range got {
		a := want[i]
		text, version, deleted := expected(a, bySeq)
		switch {
```

- Thay

```go
		case m.GetText() != TextFor(a.CID):
			return fmt.Errorf("seq %d has text %q, want %q", a.Seq, m.GetText(), TextFor(a.CID))
		}
```

bằng

```go
		case m.GetText() != text:
			return fmt.Errorf("seq %d has text %q, want %q", a.Seq, m.GetText(), text)
		case m.GetVersion() != version || m.GetDeleted() != deleted || m.GetHidden():
			return fmt.Errorf("seq %d has version %d deleted %v hidden %v, want version %d deleted %v hidden false",
				a.Seq, m.GetVersion(), m.GetDeleted(), m.GetHidden(), version, deleted)
		}
```

- Trong `CheckEvents`, ngay đầu thân vòng `for _, ev := range events {` thêm:

```go
		if ev.IsChange() {
			continue
		}
```

`tools/corecli/internal/e2e/changes.go`:

```go
package e2e

import (
	"fmt"
	"maps"
	"slices"
	"strconv"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	KindCreated = "msg_created"
	KindEdited  = "msg_edited"
	KindDeleted = "msg_deleted"
)

type Change struct {
	Seq     uint64 `json:"seq"`
	Version uint32 `json:"version"`
	Deleted bool   `json:"deleted,omitempty"`
	Text    string `json:"text,omitempty"`
}

func EditOf(seq uint64) Change { return Change{Seq: seq, Version: 1, Text: EditTextFor(seq)} }

func DeleteOf(seq uint64) Change { return Change{Seq: seq, Version: 1, Deleted: true} }

func EditTextFor(seq uint64) string { return "e2e edited seq " + strconv.FormatUint(seq, 10) }

func (c Change) Kind() string {
	if c.Deleted {
		return KindDeleted
	}
	return KindEdited
}

func ChangeEventID(room string, seq uint64, version uint32) string {
	return MessageEventID(room, seq) + "-v" + strconv.FormatUint(uint64(version), 10)
}

func expected(a Ack, bySeq map[uint64]Change) (text string, version uint32, deleted bool) {
	c, ok := bySeq[a.Seq]
	if !ok {
		return TextFor(a.CID), 0, false
	}
	return c.Text, c.Version, c.Deleted
}

func CheckChangeEvents(room string, changes []Change, events []Event) ([]string, error) {
	want := make(map[string]Change, len(changes))
	for _, c := range changes {
		want[ChangeEventID(room, c.Seq, c.Version)] = c
	}
	seen := make(map[string]bool, len(want))
	for _, ev := range events {
		if !ev.IsChange() {
			continue
		}
		c, ok := want[ev.ID]
		switch {
		case !ok:
			return nil, fmt.Errorf("unexpected change event %s (%s)", ev.ID, ev.Kind)
		case ev.Kind != c.Kind():
			return nil, fmt.Errorf("change event %s is %s, want %s", ev.ID, ev.Kind, c.Kind())
		case ev.Room != room || ev.Seq != c.Seq || ev.Version != c.Version:
			return nil, fmt.Errorf("change event %s carries room %s seq %d version %d", ev.ID, ev.Room, ev.Seq, ev.Version)
		case ev.Text != c.Text:
			return nil, fmt.Errorf("change event %s has text %q, want %q", ev.ID, ev.Text, c.Text)
		}
		seen[ev.ID] = true
	}
	var missing []string
	for _, id := range slices.Sorted(maps.Keys(want)) {
		if !seen[id] {
			missing = append(missing, id)
		}
	}
	return missing, nil
}

func CheckVersions(acks []Ack, c Change, got []*chatimv1.MessageVersion, author string) error {
	if c.Deleted {
		if len(got) != 0 {
			return fmt.Errorf("deleted seq %d has %d versions, want none", c.Seq, len(got))
		}
		return nil
	}
	i := slices.IndexFunc(acks, func(a Ack) bool { return a.Seq == c.Seq })
	if i < 0 {
		return fmt.Errorf("seq %d was never acked", c.Seq)
	}
	want := []*chatimv1.MessageVersion{
		{Version: 0, Kind: chatimv1.EditKind_EDIT_KIND_ORIGINAL, Text: TextFor(acks[i].CID), By: author},
		{Version: c.Version, Kind: chatimv1.EditKind_EDIT_KIND_TEXT, Text: c.Text, By: author},
	}
	if len(got) != len(want) {
		return fmt.Errorf("seq %d has %d versions, want %d", c.Seq, len(got), len(want))
	}
	for j, w := range want {
		g := got[j]
		if g.GetVersion() != w.GetVersion() || g.GetKind() != w.GetKind() || g.GetText() != w.GetText() || g.GetBy() != w.GetBy() || g.GetAt() == nil {
			return fmt.Errorf("seq %d entry %d = (v%d %s %q by %q), want (v%d %s %q by %q)",
				c.Seq, j, g.GetVersion(), g.GetKind(), g.GetText(), g.GetBy(), w.GetVersion(), w.GetKind(), w.GetText(), w.GetBy())
		}
	}
	return nil
}
```

Kỳ vọng `GetEditHistory` theo hợp đồng `pbconv.MessageVersions`: `after == 0` và fact đầu là v1 → phần tử v0 `ORIGINAL` (text = `prev`, by = tác giả), rồi mỗi fact một phần tử; tin đã xoá trả danh sách rỗng (bảng tính năng, cột View).

**Step 9: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./tools/corecli/..."`
Expected: FAIL biên dịch ở `tools/corecli` (main): `not enough arguments in call to e2e.CheckPage` trong `e2e_check.go`; package `tools/corecli/internal/e2e` PASS. (Main sửa ở Step 10.)

**Step 10: Code corecli**

`tools/corecli/e2e_check.go`:
- `		err = e2e.CheckPage(st.Acks[max(0, n-size):], latest, st.Room, st.User)` thành `		err = e2e.CheckPage(st.Acks[max(0, n-size):], st.Changes, latest, st.Room, st.User)`
- `		err = e2e.CheckPage(st.Acks[:min(n, size)], oldest, st.Room, st.User)` thành `		err = e2e.CheckPage(st.Acks[:min(n, size)], st.Changes, oldest, st.Room, st.User)`
- `			err = e2e.CheckPage(st.Acks, all, st.Room, st.User)` thành `			err = e2e.CheckPage(st.Acks, st.Changes, all, st.Room, st.User)`
- `	fmt.Fprintf(os.Stderr, "live ok: an event for each of %d seq, %d duplicate(s) dropped\n", cov.Distinct, cov.Duplicates)` thành `	fmt.Fprintf(os.Stderr, "live ok: an event for each of %d seq, %d duplicate(s) dropped, %d change event(s)\n", cov.Distinct, cov.Duplicates, len(st.Changes))`
- Thay cả hàm `awaitEvents` bằng:

```go
func awaitEvents(ctx context.Context, st e2e.State, path string, wait time.Duration) (e2e.Coverage, error) {
	deadline := time.Now().Add(wait)
	for {
		evs, err := e2e.ReadEvents(path)
		if err != nil {
			return e2e.Coverage{}, err
		}
		cov, err := e2e.CheckEvents(st.Acks, st.Room, evs)
		var changes []string
		if err == nil {
			changes, err = e2e.CheckChangeEvents(st.Room, st.Changes, evs)
		}
		switch {
		case err != nil:
			return cov, fmt.Errorf("live events: %w", err)
		case cov.MissingCount == 0 && len(changes) == 0:
			return cov, nil
		case time.Now().After(deadline):
			return cov, fmt.Errorf("after %v: %d of %d seq have no live event (first missing %v); change events missing %v",
				wait, cov.MissingCount, len(st.Acks), cov.Missing, changes)
		case !backoff.Pause(ctx, eventPoll):
			return cov, ctx.Err()
		}
	}
}
```

`tools/corecli/cmd_e2e.go`:
- `const e2eUsage = "usage: corecli e2e setup|send|check [flags]"` thành `const e2eUsage = "usage: corecli e2e setup|send|change|check [flags]"`
- `	steps := map[string]command{"setup": e2eSetup, "send": e2eSend, "check": e2eCheck}` thành `	steps := map[string]command{"setup": e2eSetup, "send": e2eSend, "change": e2eChange, "check": e2eCheck}`

`tools/corecli/e2e_change.go`:

```go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strconv"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

var errChangedTwice = errors.New("this scenario already holds its edit and delete")

func e2eChange(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("e2e change", flag.ContinueOnError)
	o := addOptions(fs)
	dir := fs.String("state", "/state", "directory holding the scenario state")
	edit := fs.Uint64("edit", 1, "acked seq to edit from version 0")
	del := fs.Uint64("delete", 2, "acked seq to delete from version 0")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := e2e.Load(statePath(*dir))
	if err != nil {
		return err
	}
	acked := func(seq uint64) bool { return slices.ContainsFunc(st.Acks, func(a e2e.Ack) bool { return a.Seq == seq }) }
	switch {
	case len(st.Changes) > 0:
		return errChangedTwice
	case *edit == *del || !acked(*edit) || !acked(*del):
		return fmt.Errorf("-edit %d and -delete %d must be two distinct acked seq", *edit, *del)
	}
	o.tenant, o.user = st.Tenant, st.User
	changes := []e2e.Change{e2e.EditOf(*edit), e2e.DeleteOf(*del)}
	err = withSession(ctx, o, func(ctx context.Context, s *session) error {
		for _, c := range changes {
			if err := applyChange(ctx, s.client, st.Room, c); err != nil {
				return err
			}
			if err := checkVersions(ctx, s.client, st, c); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	st.Changes = changes
	fmt.Fprintf(os.Stderr, "edited seq %d and deleted seq %d at version 1; edit history ok\n", *edit, *del)
	return e2e.Save(statePath(*dir), st)
}

func applyChange(ctx context.Context, cl *route.Client, room string, c e2e.Change) error {
	var m *chatimv1.Message
	var stats route.Stats
	var err error
	if c.Deleted {
		var resp *chatimv1.DeleteMessageResponse
		resp, stats, err = cl.DeleteMessage(ctx, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: c.Seq})
		m = resp.GetMessage()
	} else {
		var resp *chatimv1.EditMessageResponse
		resp, stats, err = cl.EditMessage(ctx, &chatimv1.EditMessageRequest{RoomId: room, Seq: c.Seq, Text: c.Text})
		m = resp.GetMessage()
	}
	if err != nil {
		return fmt.Errorf("%s seq %d: %w", c.Kind(), c.Seq, err)
	}
	if m.GetVersion() != c.Version || m.GetText() != c.Text || m.GetDeleted() != c.Deleted || m.GetEditedAt() == nil {
		return fmt.Errorf("%s seq %d returned version %d text %q deleted %v, want version %d text %q deleted %v",
			c.Kind(), c.Seq, m.GetVersion(), m.GetText(), m.GetDeleted(), c.Version, c.Text, c.Deleted)
	}
	report(c.Kind()+" seq "+strconv.FormatUint(c.Seq, 10), stats)
	return nil
}

func checkVersions(ctx context.Context, cl *route.Client, st e2e.State, c e2e.Change) error {
	resp, _, err := cl.GetEditHistory(ctx, &chatimv1.GetEditHistoryRequest{RoomId: st.Room, Seq: c.Seq, Limit: maxEditPage})
	if err != nil {
		return fmt.Errorf("edit history of seq %d: %w", c.Seq, err)
	}
	return e2e.CheckVersions(st.Acks, c, resp.GetVersions(), st.User)
}
```

Base luôn 0 (tin e2e chưa sửa). Chạy lại sau khi chỉ lệnh sửa thành công: gửi lại cùng base + cùng text là retry hợp lệ (D63), nên lệnh này chạy lại được.

`tools/corecli/cmd_change.go`:

```go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const maxEditPage = 100

var errSeqRequired = errors.New("-seq is required")

type messageFlags struct {
	room   *string
	thread *uint64
	seq    *uint64
}

func addMessageFlags(fs *flag.FlagSet) messageFlags {
	return messageFlags{
		room:   fs.String("room", "", "room id"),
		thread: fs.Uint64("thread", 0, "thread root, 0 for the main timeline"),
		seq:    fs.Uint64("seq", 0, "message seq"),
	}
}

func parseMessage(fs *flag.FlagSet, args []string, msg messageFlags) error {
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case *msg.room == "":
		return errRoomRequired
	case *msg.seq == 0:
		return errSeqRequired
	default:
		return nil
	}
}

func toUint32(name string, v uint64) (uint32, error) {
	if v > math.MaxUint32 {
		return 0, fmt.Errorf("-%s %d: want at most %d", name, v, uint64(math.MaxUint32))
	}
	return uint32(v), nil
}

func editCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("edit", flag.ContinueOnError)
	o := addOptions(fs)
	msg := addMessageFlags(fs)
	base := fs.Uint64("base", 0, "version the caller saw, 0 for the original message")
	text := fs.String("text", "", "new text")
	if err := parseMessage(fs, args, msg); err != nil {
		return err
	}
	baseVersion, err := toUint32("base", *base)
	if err != nil {
		return err
	}
	req := &chatimv1.EditMessageRequest{RoomId: *msg.room, ThreadRoot: *msg.thread, Seq: *msg.seq, BaseVersion: baseVersion, Text: *text}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
		resp, st, err := s.client.EditMessage(ctx, req)
		if err != nil {
			return err
		}
		report("edit", st)
		return printJSON(resp.GetMessage())
	})
}

func deleteCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("delete", flag.ContinueOnError)
	o := addOptions(fs)
	msg := addMessageFlags(fs)
	base := fs.Uint64("base", 0, "version the caller saw, 0 for the original message")
	if err := parseMessage(fs, args, msg); err != nil {
		return err
	}
	baseVersion, err := toUint32("base", *base)
	if err != nil {
		return err
	}
	req := &chatimv1.DeleteMessageRequest{RoomId: *msg.room, ThreadRoot: *msg.thread, Seq: *msg.seq, BaseVersion: baseVersion}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
		resp, st, err := s.client.DeleteMessage(ctx, req)
		if err != nil {
			return err
		}
		report("delete", st)
		return printJSON(resp.GetMessage())
	})
}

func hideCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("hide", flag.ContinueOnError)
	o := addOptions(fs)
	msg := addMessageFlags(fs)
	if err := parseMessage(fs, args, msg); err != nil {
		return err
	}
	req := &chatimv1.HideMessageRequest{RoomId: *msg.room, ThreadRoot: *msg.thread, Seq: *msg.seq}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
		resp, st, err := s.client.HideMessage(ctx, req)
		if err != nil {
			return err
		}
		report("hide", st)
		return printJSON(resp)
	})
}

func clearCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("clear", flag.ContinueOnError)
	o := addOptions(fs)
	room := fs.String("room", "", "room id")
	upTo := fs.Uint64("up-to", 0, "hide main-timeline messages up to this seq for the caller, 0 for the latest")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *room == "" {
		return errRoomRequired
	}
	req := &chatimv1.ClearHistoryRequest{RoomId: *room, UpToSeq: *upTo}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
		resp, st, err := s.client.ClearHistory(ctx, req)
		if err != nil {
			return err
		}
		report("clear", st)
		return printJSON(resp)
	})
}

func editsCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("edits", flag.ContinueOnError)
	o := addOptions(fs)
	msg := addMessageFlags(fs)
	after := fs.Uint64("after", 0, "list versions after this one")
	limit := fs.Uint64("limit", maxEditPage, "versions per call, at most 100")
	if err := parseMessage(fs, args, msg); err != nil {
		return err
	}
	if *limit < 1 || *limit > maxEditPage {
		return fmt.Errorf("-limit %d: want 1..%d", *limit, maxEditPage)
	}
	afterVersion, err := toUint32("after", *after)
	if err != nil {
		return err
	}
	size, err := toUint32("limit", *limit)
	if err != nil {
		return err
	}
	req := &chatimv1.GetEditHistoryRequest{RoomId: *msg.room, ThreadRoot: *msg.thread, Seq: *msg.seq, AfterVersion: afterVersion, Limit: size}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
		resp, st, err := s.client.GetEditHistory(ctx, req)
		if err != nil {
			return err
		}
		report("edits", st)
		for _, v := range resp.GetVersions() {
			if err := printJSON(v); err != nil {
				return err
			}
		}
		return nil
	})
}
```

`toUint32` chặn biên trước khi đổi kiểu (G115). Nếu `make lint` vẫn báo G115 ở `uint32(v)` thì dừng và báo cáo (không thêm `//nolint`).

`tools/corecli/main.go`:
- Trong `const usage`, thay dòng `  e2e           end-to-end scenario steps: setup, send, check` bằng `  e2e           end-to-end scenario steps: setup, send, change, check`.
- Trong `const usage`, thay dòng `  history       read one history page of a room` bằng:

```
  history       read one history page of a room
  edit          edit a message (-base: the version you saw)
  delete        delete a message for everyone (-base: the version you saw)
  hide          hide a message for the caller only
  clear         hide the history up to a seq for the caller only
  edits         list the edit history of a message
```

- Trong map `commands`, thêm sau `"history":     historyCmd,`:

```go
		"edit":        editCmd,
		"delete":      deleteCmd,
		"hide":        hideCmd,
		"clear":       clearCmd,
		"edits":       editsCmd,
```

`scripts/e2e.sh`:
- Thay

```bash
    echo "e2e PASS: $first messages before and $after after killing $victim, no loss, no duplicate, every acked seq live"
```

bằng

```bash
    echo "e2e PASS: $first messages before and $after after killing $victim, no loss, no duplicate, every acked seq live; seq 1 edited and seq 2 deleted on history, edit history and live"
```

- Thay

```bash
step="both cores share the slots again"
cli slots -cores 2 -wait 60s
result=PASS
```

bằng

```bash
step="both cores share the slots again"
cli slots -cores 2 -wait 60s

step="phase 3 edit seq 1 and delete seq 2"
echo "phase 3: edit seq 1, delete seq 2 (base version 0)"
cli e2e change -state /state -edit 1 -delete 2
step="phase 3 check"
cli e2e check -state /state
result=PASS
```

Watcher vẫn chạy từ phase 1 nên `events.jsonl` nhận cả `msg_edited`/`msg_deleted`. Phase 3 cần `E2E_FIRST ≥ 2` (mặc định 40).

**Step 11: Chạy**

Run: `make -s go ARGS="test -race -shuffle=on ./tools/..."` rồi `make -s go ARGS="vet ./tools/..."`
Expected: PASS; `wc -l tools/corecli/*.go tools/corecli/internal/e2e/*.go` → mỗi file < 200 (cmd_change.go ~185; nếu ≥ 200 thì tách `editsCmd` sang `cmd_edits.go`).

Run (cần Task 9 đã wiring, image core mới):

```bash
make infra-up && make image TARGET=apps/core && make core-up && make e2e
```

Expected: các dòng `phase 1`/`phase 2` như trước; `phase 3: edit seq 1, delete seq 2 (base version 0)`; `edited seq 1 and deleted seq 2 at version 1; edit history ok`; `live ok: an event for each of 80 seq, … duplicate(s) dropped, 2 change event(s)`; dòng cuối `e2e PASS: 40 messages before and 40 after killing core-1, no loss, no duplicate, every acked seq live; seq 1 edited and seq 2 deleted on history, edit history and live`.

Thử nhanh lệnh tay (room lấy từ log e2e hoặc `create-room`; `REDIS_PASSWORD` export từ `.env`, không in ra):

```bash
docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev edits -room <ROOM> -seq 1
docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev edit -room <ROOM> -seq 1 -base 0 -text x
```

Expected: lệnh đầu in hai dòng JSON (`EDIT_KIND_ORIGINAL`, `EDIT_KIND_TEXT`) nếu `<ROOM>` là room e2e vừa chạy (user mặc định `e2e-user`, tenant `e2e`); lệnh hai trả lỗi `FailedPrecondition` (base cũ), exit 1.

**Step 12: INDEXES.csv + commit**

Thay cả các dòng sau (theo đầu dòng):

```csv
tools/corecli,tool,"CLI routed by slot: create-room/send/history/edit/delete/hide/clear/edits/watch/slots/e2e (steps setup, send, change, check); -redis-password or env REDIS_PASSWORD; image chatim/corecli:dev used by make e2e",main,scripts/e2e.sh,e2e,D82
tools/corecli/internal/e2e,package,"E2E state file and checks: acks contiguous with distinct cids; history pages exact, including the scenario's edit (new text, version 1) and delete (no text, deleted, version 1); live msg_created events checked by natural id and deduped by seq; msg_edited/msg_deleted events checked by {room}-0-{seq}-v{ver} with kind and snapshot text (EventOf skips other event types); edit history = original then the edit, none after delete; slot share balance",State;Load;Save;Event;Event.IsChange;EventOf;CheckAcks;CheckPage;CheckEvents;MessageEventID;Change;EditOf;DeleteOf;EditTextFor;ChangeEventID;CheckChangeEvents;CheckVersions;KindCreated;KindEdited;KindDeleted;ShareOf,tools/corecli,unit,D83
scripts/e2e.sh,script,Kill-a-core end-to-end scenario: create/send/history/live; docker kill core-1; send more; verify no loss/dup; restore; phase 3 edits seq 1 and deletes seq 2 from version 0 then checks history/edit history/live change events; corecli containers get -e REDIS_PASSWORD,,make e2e,e2e,
```

Kiểm 7 cột như Task 12.

```bash
make fmt-check && make vet && make lint
git add tools/corecli/cmd_change.go tools/corecli/e2e_change.go tools/corecli/internal/e2e/changes.go tools/corecli/internal/e2e/changes_test.go
git commit -m "feat(corecli): add edit, delete, hide, clear and edits commands and an e2e edit/delete phase" -- tools/corecli/ scripts/e2e.sh INDEXES.csv
```

---

### Task 14: Itest end-to-end sửa/xoá/ẩn/clear

Khẳng định trên hạ tầng thật (skip khi thiếu `CHATIM_IT_*`), dùng helper sẵn có (`realInfra`, `startCore`, `dialCore`, `createRoom` — room có `alice` (owner) và `bob`, `caller`, `parseRoom`, `subscribeLive`, `awaitLiveIDs`, `itStore`, `itFastEffects`):
- (a) sửa qua RPC → `GetHistory` thấy text/version mới, `GetEditHistory` có bản gốc + bản sửa, live `msg_edited`; gửi lại đúng lệnh là retry thành công (D63).
- (b) fact sửa ghi thẳng vào Mongo (bỏ qua core) → reader → work stream → `edit_projection` chiếu lên `messages` → `msg_changed` publish: chứng minh đường bù.
- (c) xoá → text các bản trước bị dọn (gồm `prev` = bản gốc của fact v1, D75), `GetEditHistory` rỗng, history `deleted` không text, live `msg_deleted`; gửi lại lệnh xoá là retry thành công; sửa sau khi xoá → `FAILED_PRECONDITION`.
- (d) hai lệnh sửa đồng thời cùng base: đúng một thắng, lệnh kia `FAILED_PRECONDITION`; bob (không phải tác giả) sửa/xoá → `PERMISSION_DENIED`; owner room sửa hay xoá tin của member cũng → `PERMISSION_DENIED` với policy mặc định (D86); bob xoá tin của chính mình được.
- (e) ẩn + clear chỉ áp cho bob (placeholder `hidden`, không text, seq giữ), alice thấy đủ; `$max` không lùi; không có event thay đổi.

Không có bước "thấy fail": các test xác nhận hành vi của Task 7–11. Test fail ở lần chạy đầu là lỗi của task trước: dừng và báo (không sửa test cho qua). Đặc biệt (c) gửi lại `DeleteMessage` cùng base sau khi đã xoá: nếu nhận `FAILED_PRECONDITION` (`ErrMessageDeleted`) thì `mutate` đang kiểm "đã xoá" trước khi nhận ra retry — báo controller (vi phạm D63, route client retry lệnh xoá).

Kiểm `git diff --quiet -- INDEXES.csv` trước khi sửa (như Task 12).

**Files:**
- Create: `apps/core/edit_integration_test.go`
- Create: `apps/core/edit_access_integration_test.go`
- Modify: `INDEXES.csv`

**Step 1: Test**

`apps/core/edit_integration_test.go`:

```go
package main

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/grpcsrv"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func callerAs(ctx context.Context, user string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, grpcsrv.TenantHeader, itTenant, grpcsrv.UserHeader, user)
}

func sendAs(t *testing.T, client chatimv1.CoreServiceClient, user, roomID, cid, text string) uint64 {
	t.Helper()
	resp, err := client.SendMessage(callerAs(t.Context(), user), &chatimv1.SendMessageRequest{RoomId: roomID, Cid: cid, Text: text})
	if err != nil {
		t.Fatalf("SendMessage(%s as %s): %v", cid, user, err)
	}
	return resp.GetSeq()
}

func historyAs(t *testing.T, client chatimv1.CoreServiceClient, user, roomID string) map[uint64]*chatimv1.Message {
	t.Helper()
	req := &chatimv1.GetHistoryRequest{RoomId: roomID, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_LATEST, Limit: 50}
	resp, err := client.GetHistory(callerAs(t.Context(), user), req)
	if err != nil {
		t.Fatalf("GetHistory as %s: %v", user, err)
	}
	out := make(map[uint64]*chatimv1.Message, len(resp.GetMessages()))
	for _, m := range resp.GetMessages() {
		out[m.GetSeq()] = m
	}
	return out
}

func editHistory(t *testing.T, client chatimv1.CoreServiceClient, roomID string, seq uint64) []*chatimv1.MessageVersion {
	t.Helper()
	resp, err := client.GetEditHistory(caller(t.Context()), &chatimv1.GetEditHistoryRequest{RoomId: roomID, Seq: seq, Limit: 100})
	if err != nil {
		t.Fatalf("GetEditHistory(%d): %v", seq, err)
	}
	return resp.GetVersions()
}

func awaitLiveEvent(t *testing.T, live <-chan *nats.Msg, id string) *chatimv1.Event {
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
			return ev
		case <-deadline:
			t.Fatalf("live event %s did not arrive within %v", id, itLiveLimit)
		}
	}
}

func TestRealInfraEditShowsInHistoryEditHistoryAndLive(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)
	seq := sendAs(t, client, itUser, roomID, "edit-a", "before")

	req := &chatimv1.EditMessageRequest{RoomId: roomID, Seq: seq, Text: "after"}
	for attempt := range 2 {
		resp, err := client.EditMessage(caller(t.Context()), req)
		if m := resp.GetMessage(); err != nil || m.GetVersion() != 1 || m.GetText() != "after" || m.GetDeleted() || m.GetEditedAt() == nil {
			t.Fatalf("EditMessage attempt %d = %v, %v; want version 1 with the new text", attempt+1, m, err)
		}
	}
	ev := awaitLiveEvent(t, live, pbconv.MessageChangeEventID(room, 0, seq, 1))
	if e := ev.GetMessageEdited(); e.GetVersion() != 1 || e.GetMessage().GetText() != "after" || ev.GetActor() != itUser {
		t.Fatalf("msg_edited event = %v, want version 1 with the new text by %s", ev, itUser)
	}
	if m := historyAs(t, client, itUser, roomID)[seq]; m.GetText() != "after" || m.GetVersion() != 1 || m.GetDeleted() || m.GetHidden() {
		t.Fatalf("history shows %v, want the edited text at version 1", m)
	}
	v := editHistory(t, client, roomID, seq)
	if len(v) != 2 || v[0].GetKind() != chatimv1.EditKind_EDIT_KIND_ORIGINAL || v[0].GetText() != "before" ||
		v[1].GetKind() != chatimv1.EditKind_EDIT_KIND_TEXT || v[1].GetText() != "after" || v[1].GetVersion() != 1 || v[1].GetBy() != itUser {
		t.Fatalf("edit history = %v, want the original then version 1", v)
	}
	missing := &chatimv1.GetEditHistoryRequest{RoomId: roomID, Seq: seq + 1000, Limit: 100}
	if _, err := client.GetEditHistory(caller(t.Context()), missing); status.Code(err) != codes.NotFound {
		t.Fatalf("GetEditHistory of a missing message = %v, want NotFound", err)
	}
}

func TestRealInfraWorkersProjectAnEditWrittenOutsideTheCore(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)
	seq := sendAs(t, client, itUser, roomID, "edit-b", "original")
	core.awaitTerm(t)

	st := itStore(it, core)
	fact := domain.Edit{
		Room: room, Seq: seq, Version: 1, Kind: domain.EditText, Tenant: itTenant, By: itUser,
		Text: "edited outside the core", Prev: "original", At: time.Now().UTC().Truncate(time.Millisecond),
	}
	if err := st.Append(t.Context(), fact); err != nil {
		t.Fatalf("append an edit outside the core: %v", err)
	}
	ev := awaitLiveEvent(t, live, pbconv.MessageChangeEventID(room, 0, seq, 1))
	if m := ev.GetMessageEdited().GetMessage(); m.GetText() != fact.Text || m.GetVersion() != 1 {
		t.Fatalf("msg_edited from the workers = %v, want the projected snapshot at version 1", ev)
	}
	got, err := st.Find(t.Context(), room, []store.MsgKey{{Room: room, Seq: seq}})
	if err != nil || len(got) != 1 || got[0].Text != fact.Text || got[0].Version != 1 || !got[0].EditedAt.Equal(fact.At) {
		t.Fatalf("projection = %+v, %v; want the outside edit at version 1", got, err)
	}
}

func TestRealInfraDeletePurgesOlderTextsAndBlocksLaterEdits(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)
	seq := sendAs(t, client, itUser, roomID, "edit-c", "secret v0")
	if _, err := client.EditMessage(caller(t.Context()), &chatimv1.EditMessageRequest{RoomId: roomID, Seq: seq, Text: "secret v1"}); err != nil {
		t.Fatalf("EditMessage: %v", err)
	}

	del := &chatimv1.DeleteMessageRequest{RoomId: roomID, Seq: seq, BaseVersion: 1}
	for attempt := range 2 {
		resp, err := client.DeleteMessage(caller(t.Context()), del)
		if m := resp.GetMessage(); err != nil || !m.GetDeleted() || m.GetText() != "" || m.GetVersion() != 2 {
			t.Fatalf("DeleteMessage attempt %d = %v, %v; want deleted at version 2 without text", attempt+1, m, err)
		}
	}
	ev := awaitLiveEvent(t, live, pbconv.MessageChangeEventID(room, 0, seq, 2))
	if d := ev.GetMessageDeleted(); d.GetVersion() != 2 || !d.GetMessage().GetDeleted() || d.GetMessage().GetText() != "" {
		t.Fatalf("msg_deleted event = %v, want version 2 without text", ev)
	}
	facts, err := itStore(it, core).History(t.Context(), store.MsgKey{Room: room, Seq: seq}, 0, store.MaxEditPage)
	if err != nil || len(facts) != 2 || facts[0].Text != "" || facts[0].Prev != "" || facts[1].Kind != domain.EditDelete {
		t.Fatalf("facts after delete = %+v, %v; want v1 without text or prev, then the delete", facts, err)
	}
	if v := editHistory(t, client, roomID, seq); len(v) != 0 {
		t.Fatalf("edit history of a deleted message = %v, want none", v)
	}
	if m := historyAs(t, client, itUser, roomID)[seq]; !m.GetDeleted() || m.GetText() != "" || m.GetVersion() != 2 {
		t.Fatalf("history shows %v, want a deleted placeholder at version 2", m)
	}
	late := &chatimv1.EditMessageRequest{RoomId: roomID, Seq: seq, BaseVersion: 2, Text: "too late"}
	if _, err := client.EditMessage(caller(t.Context()), late); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("EditMessage after delete = %v, want FailedPrecondition", err)
	}
}
```

`apps/core/edit_access_integration_test.go`:

```go
package main

import (
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func assertNoChangeEvents(t *testing.T, live <-chan *nats.Msg, wait time.Duration) {
	t.Helper()
	timeout := time.After(wait)
	for {
		select {
		case m := <-live:
			if id := m.Header.Get(jetstream.MsgIDHeader); strings.Contains(id, "-v") {
				t.Fatalf("live change event %s arrived, want none for hide and clear", id)
			}
		case <-timeout:
			return
		}
	}
}

func hiddenOnly(m *chatimv1.Message) bool { return m.GetHidden() && m.GetText() == "" && m.GetSeq() != 0 }

func TestRealInfraConcurrentEditsOnOneBaseHaveOneWinner(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	seq := sendAs(t, client, itUser, roomID, "edit-d", "base")

	texts := []string{"left", "right"}
	errs := make([]error, len(texts))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, text := range texts {
		wg.Go(func() {
			<-start
			_, errs[i] = client.EditMessage(caller(t.Context()), &chatimv1.EditMessageRequest{RoomId: roomID, Seq: seq, Text: text})
		})
	}
	close(start)
	wg.Wait()
	winner := slices.IndexFunc(errs, func(err error) bool { return err == nil })
	if winner < 0 || status.Code(errs[1-winner]) != codes.FailedPrecondition {
		t.Fatalf("concurrent edits returned %v, want one success and one FailedPrecondition", errs)
	}
	if m := historyAs(t, client, itUser, roomID)[seq]; m.GetText() != texts[winner] || m.GetVersion() != 1 {
		t.Fatalf("history shows %v, want the winner %q at version 1", m, texts[winner])
	}

	bobSeq := sendAs(t, client, "bob", roomID, "edit-d-bob", "from bob")
	if _, err := client.DeleteMessage(callerAs(t.Context(), "bob"), &chatimv1.DeleteMessageRequest{RoomId: roomID, Seq: seq, BaseVersion: 1}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("bob deletes alice's message = %v, want PermissionDenied", err)
	}
	if _, err := client.EditMessage(caller(t.Context()), &chatimv1.EditMessageRequest{RoomId: roomID, Seq: bobSeq, Text: "not mine"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("the owner edits bob's message = %v, want PermissionDenied", err)
	}
	if _, err := client.DeleteMessage(caller(t.Context()), &chatimv1.DeleteMessageRequest{RoomId: roomID, Seq: bobSeq}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("the owner deletes bob's message = %v, want PermissionDenied with the default policy", err)
	}
	if _, err := client.DeleteMessage(callerAs(t.Context(), "bob"), &chatimv1.DeleteMessageRequest{RoomId: roomID, Seq: bobSeq}); err != nil {
		t.Fatalf("bob deletes his own message: %v", err)
	}
}

func TestRealInfraHideAndClearApplyOnlyToTheReader(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	live := subscribeLive(t, it, core.cfg, roomID)
	for i := range 3 {
		sendAs(t, client, itUser, roomID, "hide-"+strconv.Itoa(i+1), "visible "+strconv.Itoa(i+1))
	}

	bob := callerAs(t.Context(), "bob")
	if _, err := client.HideMessage(bob, &chatimv1.HideMessageRequest{RoomId: roomID, Seq: 3}); err != nil {
		t.Fatalf("HideMessage: %v", err)
	}
	if resp, err := client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: roomID, UpToSeq: 1}); err != nil || resp.GetClearedBeforeSeq() != 1 {
		t.Fatalf("ClearHistory(1) = %v, %v; want cleared before seq 1", resp, err)
	}
	got := historyAs(t, client, "bob", roomID)
	if len(got) != 3 || !hiddenOnly(got[1]) || got[2].GetHidden() || got[2].GetText() != "visible 2" || !hiddenOnly(got[3]) {
		t.Fatalf("bob's history = %v, want seq 1 cleared, seq 2 visible, seq 3 hidden, every seq kept", got)
	}
	for seq, m := range historyAs(t, client, itUser, roomID) {
		if m.GetHidden() || m.GetText() != "visible "+strconv.FormatUint(seq, 10) {
			t.Fatalf("alice sees seq %d as %v, want it visible", seq, m)
		}
	}
	if resp, err := client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: roomID}); err != nil || resp.GetClearedBeforeSeq() != 3 {
		t.Fatalf("ClearHistory(latest) = %v, %v; want cleared before seq 3", resp, err)
	}
	if resp, err := client.ClearHistory(bob, &chatimv1.ClearHistoryRequest{RoomId: roomID, UpToSeq: 1}); err != nil || resp.GetClearedBeforeSeq() != 3 {
		t.Fatalf("ClearHistory(1) after 3 = %v, %v; want it to stay at 3", resp, err)
	}
	for seq, m := range historyAs(t, client, "bob", roomID) {
		if !hiddenOnly(m) {
			t.Fatalf("bob sees seq %d as %v after clearing everything, want a hidden placeholder", seq, m)
		}
	}
	assertNoChangeEvents(t, live, 3*time.Second)
}
```

`assertNoChangeEvents` chờ 3s > `RECONCILE_DELAY` 2s của `itFastEffects`: id event thay đổi luôn có hậu tố `-v{ver}`, id `msg_created` (`{room}-0-{seq}`) và room id thì không.

**Step 2: Biên dịch**

Run: `make -s go ARGS="vet ./apps/core/"`
Expected: sạch. Lỗi tên (`st.Append`, `st.History`, field proto) nghĩa là part A đặt tên khác hợp đồng: chỉ sửa tên trong test, ghi vào báo cáo.

**Step 3: Chạy trên hạ tầng thật**

Run: `make infra-up && make itest`
Expected: mọi package `ok`, gồm `TestRealInfraEditShowsInHistoryEditHistoryAndLive`, `TestRealInfraWorkersProjectAnEditWrittenOutsideTheCore` (live event sau khoảng delay 2s + một nhịp fetch), `TestRealInfraDeletePurgesOlderTextsAndBlocksLaterEdits`, `TestRealInfraConcurrentEditsOnOneBaseHaveOneWinner`, `TestRealInfraHideAndClearApplyOnlyToTheReader`, và các itest cũ (`TestRealInfraResyncDrillRepublishesWritesTheReaderMissed`, `TestRealInfraStopUnderLoadKeepsEveryAckedMessage`, …).

**Step 4: INDEXES.csv + commit**

Dòng `apps/core`, cột tests: ngay sau mục `itest (resync drill republishes messages and an edit the reader missed)` thêm `;itest (edit shows in history/edit history/live; a resent edit succeeds);itest (workers project and publish an edit written straight to Mongo);itest (delete purges older texts, a resent delete succeeds, later edits fail);itest (concurrent edits on one base have one winner; default policy: only the author edits or deletes; even the owner is denied);itest (hide and clear apply only to the reader and publish nothing)`. Kiểm 7 cột.

```bash
make fmt-check && make vet && make lint
git add apps/core/edit_integration_test.go apps/core/edit_access_integration_test.go
git commit -m "test(core): cover edit, delete, hide and clear end to end on real infra" -- apps/core/edit_integration_test.go apps/core/edit_access_integration_test.go INDEXES.csv
```

---

### Task 15: Docs

Task docs, rủi ro thấp: controller kiểm nhanh, không reviewer. Không đổi code Go hay luật alert.

**Step 0: Dừng nếu cây làm việc còn thay đổi chưa commit của owner**

```bash
git status --porcelain -- CLAUDE.md README.md INDEXES.csv docs/designs/261005-chatim-architecture.md docs/roadmap.md docs/research docs/archive
```

Expected: không in gì (hoặc chỉ dòng của chính task này nếu đang làm dở). Lúc viết plan (2026-10-05), owner có thay đổi chưa commit ở `CLAUDE.md`, `README.md`, `INDEXES.csv`, thiết kế, roadmap (đổi link sang `docs/archive/research/` và `docs/research/261005-system-mechanisms-synthesis-report.md`) và đã chuyển `docs/research/261004-*` vào `docs/archive/research/`. Còn thấy các file đó trong output → **dừng, hỏi controller**; không commit chung thay đổi của owner, không `git stash`/`checkout` chúng. Các bước dưới viết theo bản đã commit; thay đổi của owner chỉ chạm dòng header/link nên không chồng lên chỗ sửa dưới đây.

**Files:**
- Modify: `docs/designs/261005-chatim-architecture.md`
- Modify: `docs/roadmap.md`
- Modify: `README.md`
- Modify: `INDEXES.csv`
- Modify: `CLAUDE.md`

**Step 1: Kiểm luật alert (không sửa)**

`msg_changed` không có ack mark nhưng (Task 11) chỉ đếm `reconcile_republished_total{effect="msg_changed"}` cho PubAck mà JetStream **không** đánh dấu trùng, tức chỉ khi fast path đã mất event. Vì vậy luật `ChatimRepublishSurge` (`sum(rate(chatim_core_reconcile_republished_total[5m])) > 100`) giữ nguyên ý nghĩa "đường bù gửi nhiều", không báo động giả ở 100–300 lệnh đổi/s. Kiểm:

```bash
grep -n "reconcile_republished_total\|effect_dropped_total" deploy/prometheus/alerts.yml
make alerts-check
```

Expected: hai biểu thức như M2b.1; `SUCCESS: 15 rules found`. Không sửa `alerts.yml`.

**Step 2: Thiết kế**

`docs/designs/261005-chatim-architecture.md`:

- **§5**, bốn dòng bảng:
  - `| \`messages\` (clustered) | … | **không có index phụ** | Đã xây (tạo) |`: cột Trường chính thay `cid, ts, v, d, meta` bằng `cid, ts, v, d, ea (lần sửa cuối), meta`; cột Trạng thái thành `Đã xây (tạo; sửa/xoá \`v/d/ea\`, M2b.2)`.
  - Dòng `message_edits` thay cả dòng bằng:

```markdown
| `message_edits` (clustered) | 28B `room│thread│seq│ver` | Fact | `r`, `t`, `k` (`edit`/`delete`), `x` (text), `by`, `ts`; v1 thêm `p` (`prev`, bản gốc); xoá dọn `x`/`p` của bản ≤ v−1 | `{r, ts}` (D70) | Đã xây (M2b.2) |
```

  - Dòng `members`: cột Trường chính thay `cleared_before_seq` bằng `cleared_before_seq (\`cb\`)`; cột Trạng thái thành `Đã xây (tạo; \`cb\` M2b.2)`.
  - Dòng `hidden` thay cả dòng bằng:

```markdown
| `hidden` | ObjectId | Fact thưa theo người đọc | `u`, `r`, `th`, `s` | `{u, r, th, s}` unique | Đã xây (M2b.2) |
```

- **§6.3**: tiêu đề `### 6.3 Lệnh đổi: fact + projection [Chưa xây]` → `### 6.3 Lệnh đổi: fact + projection [Đã xây sửa/xoá, M2b.2; ghim ở M2b.3]`. Câu đầu `Lệnh đổi không đi qua actor; vẫn được định tuyến tới core chủ slot.` → `Lệnh đổi không đi qua actor (package \`mutate\`, D82); vẫn được định tuyến tới core chủ slot.` Bước 2: `để kiểm quyền (đúng tác giả; xoá thì tác giả hoặc owner room) và trạng thái (chưa bị xoá)` → `để kiểm trạng thái (chưa bị xoá); quyền đã hỏi \`access.Policy\` trước đó với tác giả của tin (D86: mặc định chỉ tác giả sửa/xoá; owner/moderator do policy Phase 2)`. Bước 4: `→ retry, trả thành công, không phát event;` → `→ retry, trả thành công (chạy lại projection; event cùng id bị stream bỏ trùng);`. Bước 6: câu `Reconciler chạy lại projection + event từ feed insert của \`message_edits\`; delay riêng: xoá 2–3s.` → `Không ack mark. Worker chạy lại projection (\`edit_projection\`, delay 0) và event (\`msg_changed\`, delay \`RECONCILE_DELAY\`) từ feed insert của \`message_edits\` (D83).` Thêm đoạn ngay trước dòng `Ngân sách: 1–3% tin …`:

```markdown
**Đã xây (M2b.2, D82–D84, D86):** `grpcsrv` gọi `mutate.Mutator`. `Edit`/`Delete`: `access.Checker.Admit` (`EditMessage`/`DeleteMessage`: tenant + membership) → `Find` tin (không có → `NOT_FOUND`) → `Checker.Allow` với `Author` = tác giả tin (chỉ policy quyết; mặc định `access.DefaultPolicy` cho sửa/xoá chỉ tác giả, kể cả owner của room cũng bị từ chối; từ chối → `PERMISSION_DENIED`, D86) → `Edits.Latest` (reverse scan một doc); version hiện tại = `max(v của tin, version fact cuối)`; tin đã xoá hoặc `base_version` lệch → `FAILED_PRECONDITION`, trừ khi là retry (fact `base+1` đã có, cùng tác giả/loại/text) → `Edits.Append` fact `base+1` (v1 sửa có `prev`; `base_version ≥ MaxInt32` = conflict) → `Messages.ApplyEdit` (`$set v, ea, x, d` khi `v < ver`) → xoá thì `Edits.PurgeText` các bản ≤ v−1 → `Find` lại → enqueue `msg_edited`/`msg_deleted` (snapshot sau projection; lỗi enqueue bỏ qua, worker bù) → trả snapshot. `GetEditHistory` (`Admit` → `Find` → `Allow` với `ReadEditHistory`, A7) trả bản gốc (v0 từ `prev`) rồi từng fact theo version, tối đa 100 mỗi lần; tin đã xoá trả rỗng, tin không có → `NOT_FOUND`. `prev` chỉ nằm trên fact sửa v1 (không bao giờ trên fact xoá), nên `PurgeText` khi xoá dọn luôn bản gốc (D75). Wiring: `apps/core/service_wiring.go` dựng `access.Checker` (`DefaultPolicy`) + `mutate.Mutator` cho `grpcsrv`. Core chưa giới hạn thời gian sửa/xoá và không có luật owner/moderator (module policy chat cắm qua `access.Policy` ở Phase 2).
```

- **§6.4**: tiêu đề `### 6.4 Tập và vị trí đọc [Chưa xây]` → `### 6.4 Tập và vị trí đọc [Đã xây ẩn + clear, M2b.2; còn lại chưa]`. Thay `- Ẩn phía tôi: insert \`hidden\`; clear history: nâng \`cleared_before_seq\` trên member doc.` bằng:

```markdown
- Ẩn phía tôi [Đã xây, M2b.2]: upsert `hidden {u, r, th, s}` (`mutate.Hide`: `Admit` → tin phải tồn tại → policy `HideMessage` với `Author`; mặc định cho phép). Clear history [Đã xây, M2b.2]: `$max members.cb` (`mutate.ClearHistory`, quyền `ClearHistory`; `up_to_seq = 0` hoặc lớn hơn seq cuối → kẹp về `Last(room, 0)`), trả giá trị sau cập nhật nên không bao giờ lùi. Cả hai không phát event (owner 2026-10-05; đồng bộ đa thiết bị ở M3/M4) và chỉ áp lúc đọc qua `view.HideForViewer` (D85).
```

- **§8.2**: thay `lọc insert của \`messages\` và \`rooms\`` bằng `lọc insert của \`messages\`, \`rooms\` và \`message_edits\``; `(\`work.Record\`, chỉ khoá + \`CommittedAt\`, 33 byte, D80)` bằng `(\`work.Record\`, chỉ khoá + version + \`CommittedAt\`, 37 byte, D80, D84)`; `(\`m:{room}-{thread}-{seq}\`, \`r:{room}\`)` bằng `(\`m:{room}-{thread}-{seq}\`, \`r:{room}\`, \`e:{room}-{thread}-{seq}-v{ver}\`)`.
- **§8.3**: tiêu đề `### 8.3 Effect engine [Đã xây phần M2b.1]` → `### 8.3 Effect engine [Đã xây phần M2b.1, M2b.2]`. Dòng chính sách `delay`: `\`msg_created\` ~5s (> \`PUB_ACK_TIMEOUT\` + cửa sổ mark); xoá 2–3s; counter vài giây` → `\`msg_created\`, \`msg_changed\` = \`RECONCILE_DELAY\` (~5s, > \`PUB_ACK_TIMEOUT\` + cửa sổ mark); projection sửa/xoá 0 (D83); counter vài giây`. Bảng registry: thêm ba dòng ngay sau dòng `   | \`RoomInserted\` | \`room_created\` | …`:

```markdown
   | `EditInserted` | `room_activity` | 0 | — | `Activity{Seq: 0}`: chỉ nâng `lc`/`ab` (`$max`), không đổi `ls`/`lm`; nhờ vậy resync tìm được room chỉ có sửa/xoá |
   | `EditInserted` | `edit_projection` | 0 | — | `At` fact → `ApplyEdit` (`v < ver`); xoá thì `PurgeText` bản ≤ v−1; fact không còn → drop có đếm |
   | `EditInserted` | `msg_changed` | `RECONCILE_DELAY` | không | `At` fact + `Find` tin + loại room → `msg_edited`/`msg_deleted` (snapshot hiện tại, id theo version fact); publish + chờ PubAck; stream bỏ trùng với fast path; chỉ đếm republish khi PubAck không phải bản trùng |
```

  Đoạn **Resync (D69)**: thay câu `Chưa có thread và \`message_edits\`/\`pin_actions\` nên chỉ quét timeline chính; M2b.2/M2b.3 thêm quét \`{room, ts}\`.` bằng `Chưa có thread nên timeline chính là timeline duy nhất. M2b.2: sau timeline, mỗi room quét \`message_edits\` theo \`{r, ts}\` trong \`[from, to]\` (trang 1000, dời \`from\` tới \`ts\` cuối trang, bỏ trùng mép trang theo id record; một thời điểm đầy cả trang → \`ErrEditPageFull\`) → record \`EditInserted\`; \`pin_actions\` ở M2b.3. Fact sửa cũng chạy \`room_activity\` (chỉ \`lc/ab\`) nên room chỉ có sửa/xoá trong khoảng mất vẫn nằm trong chỉ mục activity.`
- **§9.2**: tiêu đề `### 9.2 Reader pipeline + permission hook [Đã xây một phần]` → `### 9.2 Reader pipeline + permission hook [Đã xây cho \`GetHistory\`, \`GetEditHistory\` và lệnh đổi]`. Bước 1: `(tenant, member, role, tác giả/owner, quyền đọc lịch sử sửa A7). Policy cắm sau (Phase 2).` → `(tenant, member, role, tác giả của tin đích, quyền đọc lịch sử sửa A7). Mặc định \`access.DefaultPolicy\` (sửa/xoá chỉ tác giả, D86); module policy chat (user → role → quyền) cắm sau (Phase 2).` Trong đoạn `**Đã xây (M2b.0):**` thay `\`access.AllowMembers\` là policy mặc định` bằng `\`access.AllowMembers\` là policy mặc định tới M2b.2, nay là \`access.DefaultPolicy\` (D86)`. Thay `**Chưa xây:** ẩn và mặt nạ xoá (bước 2, 3) thuộc M2b.2.` bằng:

```markdown
**Đã xây (M2b.2, D85):** bước 2 là `view.HideForViewer` (seq ≤ `Member.ClearedBeforeSeq` hoặc trong `Hidden.HiddenIn(user, room, thread, seq đầu trang, seq cuối trang)` → `hidden = true`, không text), bước 3 là `view.MaskDeleted` (`deleted = true`, không text); seq, version, `edited_at` giữ nguyên nên phân trang theo seq vẫn đúng. `view.Default()` = `CollapseRetried → MaskDeleted → HideForViewer`. Lệnh đổi hỏi `access` với action `edit_message`, `delete_message`, `hide_message`, `clear_history`; `GetEditHistory` với `read_edit_history`.

**Đã xây (M2b.2, D86):** `access.Checker` tách `Admit` (room → tenant → membership, không hỏi policy) và `Allow` (hỏi policy); `Authorize` = hai bước. `Request.Author` là tác giả tin đích (rỗng với action theo room). Action trên một tin (sửa, xoá, ẩn, `GetEditHistory`) chạy `Admit` → `Find` tin → `Allow`; `clear_history`, `read_history`, `send_message` dùng `Authorize`/actor như cũ. `access.DefaultPolicy` là mặc định của `NewChecker` và actor: `edit_message`/`delete_message` khi `Author ≠ User` → `PERMISSION_DENIED`, mọi action khác cho member. Core không có luật riêng cho tác giả hay owner.
```

- **§12**: dòng RC4, cột Metric: `\`reconcile_republished_total{effect}\` (worker gửi vì không có mark)` → `\`reconcile_republished_total{effect}\` (worker gửi vì không có mark; \`msg_changed\` không có mark, chỉ đếm khi PubAck không phải bản trùng, tức fast path đã mất event)`. Thay cả dòng PJ1 bằng:

```markdown
| PJ1 | Projection cuối cùng khớp fact cuối | Worker chạy lại projection từ mọi fact (`edit_projection`, CAS `v < ver`, M2b.2); lỗi → retry qua work stream; fact không đọc được → drop có đếm; counter (M2b.3) chưa có | `work_failures_total`, `effect_dropped_total{effect="edit_projection"}`, `effect_dropped_total{effect="msg_changed"}`; alert `ChatimWorkFailing`, `ChatimEffectDropping` |
```

- **§14**: dòng `| Core chết giữa fact và projection | … không retry thì reconciler sửa sau delay |` → `… không retry thì worker \`edit_projection\` sửa ngay khi record tới (delay 0) |`.
- **§17.2**: thêm năm dòng sau dòng `| D81 | …`:

```markdown
| D82 | Lệnh đổi (sửa, xoá, ẩn, clear) chạy trong package `mutate`, gọi thẳng từ `grpcsrv`, không qua actor; client vẫn định tuyến theo slot của room | Đưa lệnh đổi vào mailbox actor của room; một actor riêng cho lệnh đổi | Lệnh đổi không cấp seq nên không cần thứ tự của actor; CAS trên khoá fact `room│thread│seq│ver` và `v < ver` của projection giữ đúng khi hai core cùng nhận lệnh (P1); không chen vào hàng gửi tin; định tuyến theo slot vẫn giữ cache ấm |
| D83 | Event `msg_edited`/`msg_deleted` mang snapshot hiện tại của tin + `version` của fact, id `{room}-{th}-{seq}-v{ver}`, không ack mark; fast path enqueue sau projection, retry của lệnh đổi enqueue lại đúng id đó; worker chạy `room_activity` (chỉ `lc/ab`), `edit_projection` (delay 0) rồi `msg_changed` (delay `RECONCILE_DELAY`, chỉ đếm republish khi PubAck không phải bản trùng) từ insert của `message_edits` (thay "xoá delay 2–3s") | Event mang diff/text của fact; ack mark cho event đổi; delay xoá riêng 2–3s; projection chỉ ở fast path | Snapshot đúng cả khi event tới trễ hoặc lệch thứ tự (consumer giữ bản version lớn nhất); lệnh đổi hiếm (1–3%) nên publish lại sau delay rẻ và stream bỏ trùng theo id trong 5m, không cần không gian mark riêng (D65); projection delay 0 sửa ngay khi core chết giữa fact và projection; một delay chung đủ vì fast path đã đẩy event ngay |
| D84 | `work.Record` thêm `Version` (`uint32`): 37 byte, id `e:{room}-{th}-{seq}-v{ver}` cho `EditInserted` | Kiểu record riêng cho fact sửa; id theo `_id` nhị phân | Một định dạng cho mọi kind; id đọc được và trùng phần đuôi với id event để truy vết; thêm 4 byte không đáng kể |
| D85 | View: tin có `seq ≤ cleared_before_seq` hoặc trong `hidden` của người đọc → `hidden = true`, không nội dung; tin xoá → `deleted = true`, không nội dung; seq, version giữ nguyên; ẩn/clear không phát event | Bỏ hẳn tin khỏi trang; lọc trong truy vấn store; event `msg_hidden`/`history_cleared` | Phân trang theo seq vẫn đúng và client không tưởng là lỗ seq (R10); `messages` không có index phụ nên lọc ở view rẻ hơn; owner chốt 2026-10-05 không phát event cho ẩn/clear (đồng bộ đa thiết bị ở M3/M4) |
| D86 | Quyền trên một tin (sửa, xoá, ẩn, đọc lịch sử sửa) chỉ do `access.Policy` quyết; core chỉ biết user có quyền hay không, không có luật tác giả/owner. Mặc định `access.DefaultPolicy`: không ai sửa/xoá tin của người khác (chỉ tác giả), action khác cho member. `access.Checker` tách `Admit` (tenant + membership) và `Allow` (policy, `Request.Author` = tác giả tin); action trên một tin chạy `Admit` → `Find` → `Allow`. User → role → quyền thuộc module policy chat (Phase 2) | Luật cứng trong core "sửa: tác giả; xoá: tác giả hoặc owner room" (`domain.ErrNotAuthor`) | Owner chốt 2026-10-05. Mỗi sản phẩm muốn luật khác (owner/moderator xoá, giới hạn thời gian theo tenant); luật cứng trong core không linh hoạt và phải sửa core mỗi lần đổi. Mặc định chặt (chỉ tác giả) an toàn khi chưa có policy; hỏi policy sau `Find` để policy thấy tác giả, trước nhận diện retry nên tác giả gửi lại vẫn được phép |
```

**Step 3: Roadmap**

`docs/roadmap.md`:
- Dòng M2b.2: thay đuôi `(D70) | ⏭ Tiếp theo — [plan](plans/2026-10-05-m2b2-edit-delete.md) sẵn sàng |` bằng `(D70) | ✅ \`dev-done\` (trên \`feat/m2b\`, chưa merge \`main\`) — [plan](plans/2026-10-05-m2b2-edit-delete.md); \`message_edits\` + projection, 5 RPC, ẩn/clear ở reader pipeline (không event), effect \`edit_projection\` + \`msg_changed\` (thay delay xoá 2–3s), quyền sửa/xoá chỉ qua \`access.Policy\`, mặc định chỉ tác giả, resync quét \`message_edits\` (D82–D86) |`.
- Dòng Phase 2 `| 2 | Policy quyền | Chính sách quyền cắm vào permission hook (M2b.0) | Sau Phase 1 |` → `| 2 | Policy quyền | Module policy chat: ánh xạ user → role → quyền (owner/moderator xoá tin người khác, giới hạn thời gian sửa/xoá theo tenant…), cắm vào \`access.Policy\` (M2b.0) thay \`access.DefaultPolicy\` (sửa/xoá chỉ tác giả, D86) | Sau Phase 1 |`.
- Dòng M2b.3: thay đuôi `event cho SysMsg (cid \`sys:{event_id}\`) | Chưa |` bằng `event cho SysMsg (cid \`sys:{event_id}\`) | ⏭ Tiếp theo — cần plan |`.

(Mục "Mục mang sang M5" cập nhật ở Task 16, khi đã có danh sách Minor.)

**Step 4: README**

`README.md`:
- Dòng "Trạng thái": thay `M2b.0 và M2b.1 (effect engine: reader → work stream → worker mọi core, \`room_created\`, room activity, \`/app resync\`) xong trên nhánh \`feat/m2b\`; tiếp theo là M2b.2 theo [roadmap](docs/roadmap.md)` bằng `M2b.0, M2b.1 (effect engine: reader → work stream → worker mọi core, \`room_created\`, room activity, \`/app resync\`) và M2b.2 (sửa/xoá theo fact \`message_edits\` + projection, ẩn/clear phía người đọc, lịch sử sửa) xong trên nhánh \`feat/m2b\`; tiếp theo là M2b.3 theo [roadmap](docs/roadmap.md)`.
- Bảng "Kiến trúc", dòng `core`: `reader đọc change stream (\`messages\`, \`rooms\`) đẩy record vào work stream, worker ở mọi core chạy effect (event bù, room activity)` → `sửa/xoá tin là fact \`message_edits\` + projection \`messages\`, ẩn/clear theo người đọc; reader đọc change stream (\`messages\`, \`rooms\`, \`message_edits\`) đẩy record vào work stream, worker ở mọi core chạy effect (event bù, room activity, projection sửa/xoá)`.
- "Cấu trúc": `internal/{actor,flush,` → `internal/{actor,mutate,view,access,flush,`.
- "Lệnh hay dùng": thêm sau dòng `make e2e …`:

```
    docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev edit -room ID -seq N -base V -text "..."   # sửa tin; cùng cờ: delete -room ID -seq N -base V, hide -room ID -seq N, clear -room ID [-up-to N], edits -room ID -seq N [-after V]; -tenant/-user chọn người gọi; image do make e2e build
```

  và dòng `make e2e` đổi chú thích thành `# build tools/corecli, chạy scripts/e2e.sh (route theo slot, kill core-1, kiểm tra; phase 3 sửa seq 1, xoá seq 2)`.
- Đoạn `/app resync`: `Giới hạn: chỉ timeline chính;` → `Quét timeline chính rồi \`message_edits\` của từng room (theo \`{r, ts}\`). Giới hạn: chưa có thread;`.

**Step 5: INDEXES.csv**

Mỗi task trước sửa dòng của mình trong commit của nó; bước này chỉ kiểm và vá chỗ thiếu.

```bash
for p in apps/core/internal/mutate apps/core/internal/domain apps/core/internal/store apps/core/internal/store/memstore apps/core/internal/store/mongostore apps/core/internal/work apps/core/internal/pbconv apps/core/internal/access apps/core/internal/grpcsrv apps/core/internal/view apps/core/internal/effects apps/core/internal/publish apps/core/internal/resync tools/internal/route tools/corecli/internal/e2e; do printf '%s ' "$p"; grep -c "^$p," INDEXES.csv; done
grep -n "^apps/core/internal/mutate,\|^docs/plans/2026-10-05-m2b2" INDEXES.csv | cut -c1-120
grep -n "EditInserted\|MessageChangeEventID\|MaskDeleted\|edit_projection\|ClearHistory\|DefaultPolicy" INDEXES.csv | cut -c1-80
```

Expected: mỗi package đúng `1`; có dòng `mutate` và dòng plan M2b.2; grep cuối có kết quả ở các dòng `store`, `pbconv`, `view`, `effects`, `grpcsrv`, `work`, `access`. Thiếu dòng `mutate` thì thêm ngay sau dòng `apps/core/internal/metrics,...`:

```csv
apps/core/internal/mutate,package,"Change commands outside the actor (D82): Edit/Delete admit through access (edit_message/delete_message), read the message, ask access.Policy with the message author (default: author only; no author or owner rule in mutate, D86), read the last fact (Edits.Latest), check deleted state and base_version, insert the fact base+1 (v1 keeps prev), treat a duplicate with the same author/kind/text as a retry, project with Messages.ApplyEdit (v < ver), purge older texts on delete, enqueue msg_edited/msg_deleted and return the projected snapshot; Hide admits, finds the message, asks the policy with the author and upserts hidden {u, r, th, s}; ClearHistory raises members.cb with $max (0 = last seq of the main timeline)",Mutator;New;Deps;Messages;HistoryClearer;EventPublisher;EditCmd;DeleteCmd;HideCmd;ClearCmd;Mutator.Edit;Mutator.Delete;Mutator.Hide;Mutator.ClearHistory,apps/core/internal/grpcsrv;apps/core,unit (memstore),D62;D63;D64;D75;D82;D86
```

Thiếu dòng plan thì thêm ngay sau dòng `docs/plans/2026-10-05-m2b1-effect-engine.md,...`:

```csv
docs/plans/2026-10-05-m2b2-edit-delete.md,doc,"M2b.2 implementation plan: edit/delete as message_edits facts + messages projection (base_version CAS, ack after projection, purge on delete); hide (hidden) and clear history (members.cb) in the reader pipeline without events; GetEditHistory; package mutate; effects edit_projection + msg_changed; work record 37 bytes with Version; resync scans message_edits; corecli + e2e edit/delete; edit and delete rights decided by access.Policy (default: author only)",,everyone,,D62 D63 D64 D70 D75 D82 D83 D84 D85 D86
```

Dòng nào khác còn mô tả cũ (vd. `view` còn "Default pipeline used by GetHistory" mà không nhắc `MaskDeleted`/`HideForViewer`, `work` còn "33 big-endian bytes") thì sửa theo hợp đồng chung và ghi vào báo cáo task nào đã quên.

Kiểm: `python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"` → Expected: `{7}`.

**Step 6: CLAUDE.md**

- Mục Project, sau gạch đầu dòng `- M2b.1 (plan …)` thêm:

```markdown
- M2b.2 (plan `docs/plans/2026-10-05-m2b2-edit-delete.md`): edit and delete as immutable facts in `message_edits` plus the `messages` projection (`v/d/ea`), `base_version` CAS, ack after the projection (D62–D64, D82); hide (`hidden`) and clear history (`members.cb`) applied per reader in `view`, no events (D85); `GetEditHistory`; worker effects `edit_projection` and `msg_changed` on `EditInserted` (D83, D84); resync scans `message_edits`; corecli/e2e edit and delete; edit/delete rights only through `access.Policy`, default `access.DefaultPolicy` (author only, D86).
```

  Câu `built on a data-class framework (§4) with decisions D61–D81` → `… D61–D86`; `Next is M2b.2 (edit + delete); its plan is not written yet.` → `Next is M2b.3 (reactions + pins); its plan is not written yet.`
- Mục Commands: dòng `make e2e …` đổi chú thích thành `# tools/corecli: create, send, history, kill core-1, verify no loss/dup, live events, then edit seq 1 and delete seq 2`.
- **Storage ports**: `The ports are \`store.Messages\`, \`store.Rooms\` and \`store.ChangeFeed\`.` → `The ports are \`store.Messages\` , \`store.Rooms\`, \`store.MessageEditor\` (\`ApplyEdit\`), \`store.HistoryClearer\` (\`ClearHistory\`), \`store.Edits\`, \`store.Hidden\` and \`store.ChangeFeed\`.`
- **Effect engine**: `(\`store.Change.Kind\`: \`MessageInserted\`, \`RoomInserted\`)` → `(\`store.Change.Kind\`: \`MessageInserted\`, \`RoomInserted\`, \`EditInserted\`)`; `watches the database for inserts into \`messages\` and \`rooms\`` → `watches the database for inserts into \`messages\`, \`rooms\` and \`message_edits\``; `(keys + \`CommittedAt\`, 33 bytes; id \`m:{room}-{thread}-{seq}\` or \`r:{room}\`)` → `(keys + version + \`CommittedAt\`, 37 bytes; id \`m:{room}-{thread}-{seq}\`, \`r:{room}\` or \`e:{room}-{thread}-{seq}-v{ver}\`)`; trong gạch Workers, `and \`room_created\` (delay \`RECONCILE_DELAY\`, no mark) for \`RoomInserted\`.` → `, \`room_created\` (delay \`RECONCILE_DELAY\`, no mark) for \`RoomInserted\`, and \`room_activity\` (\`Seq: 0\`, only \`lc/ab\`), \`edit_projection\` (delay 0) then \`msg_changed\` (delay \`RECONCILE_DELAY\`, no mark; counts a republish only when the PubAck is not a duplicate) for \`EditInserted\`.`; `replays the main timeline backwards into the work stream at \`-rate\`` → `replays the main timeline backwards, then the room's \`message_edits\` by \`{r, ts}\`, into the work stream at \`-rate\``.
- Thêm khối mới ngay trước `**Permission hook and reader pipeline (\`access\`, \`view\`).**`:

```markdown
**Edits (`apps/core/internal/mutate`; D62–D64, D82–D86).**
- `EditMessage`, `DeleteMessage`, `HideMessage` and `ClearHistory` run in `mutate`, called by `grpcsrv`, not through actors; tools still route them by the room's slot.
- Edit and delete insert an immutable fact into `message_edits` (`_id` = `room│thread│seq│ver`, 28B, the CAS; version = `base_version + 1`; v1 keeps `prev`), project it onto `messages` with `ApplyEdit` (only where `v < ver`), purge older fact texts on delete, then ack. A duplicate fact with the same author, kind and text is a retry and succeeds; a stale base or a deleted message is `FAILED_PRECONDITION`. Rights come only from `access.Policy`, asked with the message author after `Find` and before retry recognition; `mutate` has no author or owner rule (D86).
- The fast path enqueues `msg_edited`/`msg_deleted` (id `{room}-{thread}-{seq}-v{ver}`, current snapshot, no ack mark); a retry re-enqueues the same id. Workers rerun `edit_projection` and `msg_changed` from the `EditInserted` record, which heals a core dying between fact and projection; the stream drops the duplicate event by id.
- Hide upserts `hidden {u, r, th, s}`; clear raises `members.cb` with `$max` (`up_to_seq` 0 or past the end is clamped to the last seq). Neither publishes an event. `GetEditHistory` (action `read_edit_history`) returns the original then each fact, nothing for a deleted message, `NOT_FOUND` for a missing one.
- Wiring: `apps/core/service_wiring.go` (`wireService`) builds the `access.Checker` (default policy) and the `mutate.Mutator` for `grpcsrv` (`Deps.Mutator`, `Edits`, `Hidden` are required).
```

- **Permission hook and reader pipeline**: `then asks the policy (default \`AllowMembers\`).` → `then asks the policy. \`Checker.Admit\` checks tenant and membership only, \`Checker.Allow\` asks the policy with \`Request.Author\` (the target message's author, empty for room actions), \`Authorize\` does both; actions on one message (edit, delete, hide, edit history) run \`Admit\` → \`Find\` → \`Allow\`. The default policy of \`NewChecker\` and the actor is \`access.DefaultPolicy\`: edit/delete only by the author, every other action for any member (D86); the chat policy module (user → role → permission) plugs in at Phase 2.`; `\`SendMessage\` asks it in the actor (\`actor.WithPolicy\`), \`GetHistory\` in \`grpcsrv\`.` → `\`SendMessage\` asks it in the actor (\`actor.WithPolicy\`), edit/delete/hide/clear in \`mutate\`, \`GetHistory\` and \`GetEditHistory\` in \`grpcsrv\`.`; `Hidden and deleted masks are M2b.2.` → `\`view.MaskDeleted\` (deleted → no text) and \`view.HideForViewer\` (seq ≤ \`ClearedBeforeSeq\` or hidden by the reader → \`hidden\`, no text) follow it in \`view.Default()\`; seq and version are kept.`
- **Docs**: trong gạch `docs/plans/`, sau `M2b.1: \`docs/plans/2026-10-05-m2b1-effect-engine.md\` (executed; results and known issues at its end).` thêm ` M2b.2: \`docs/plans/2026-10-05-m2b2-edit-delete.md\` (executed; results and known issues at its end).`

- Mục **Detectors**: thêm câu "Edit effects add the labels `edit_projection` (only `effect_dropped_total`; its `effectCounters.republished` is nil) and `msg_changed` (both `effect_dropped_total` and `reconcile_republished_total`, which counts only PubAcks the stream did not flag as duplicates)."

**Step 7: Kiểm**

```bash
grep -n "Chưa xây\]" docs/designs/261005-chatim-architecture.md | grep -n "6.3\|6.4\|9.2"
grep -n "xoá 2–3s\|Hidden and deleted masks are M2b.2\|33 bytes\|Next is M2b.2" CLAUDE.md docs/designs/261005-chatim-architecture.md README.md
grep -n "owner room\|default \`AllowMembers\`\|tác giả/owner" CLAUDE.md docs/designs/261005-chatim-architecture.md
```

Expected: lệnh đầu không in gì; lệnh hai chỉ còn dòng lịch sử có ghi rõ "thay" (vd. D83 "thay \"xoá delay 2–3s\"", roadmap cột Phạm vi M2b.2); lệnh ba chỉ còn dòng D86 (phương án bỏ).

**Step 8: Commit**

```bash
git commit -m "docs: record the M2b.2 edit and delete design and decisions D82-D86" -- docs/designs/261005-chatim-architecture.md docs/roadmap.md README.md INDEXES.csv CLAUDE.md
```

---

### Task 16: Kiểm chứng cuối milestone

Chạy một lần ở cuối, theo bảng "Verification and review budget" (mốc cuối milestone). M2b.2 không phải milestone perf: không sweep corebench trước/sau; chỉ một lần chạy ngắn để chắc đường gửi (codec `messages` có thêm `v/d/ea`) không lỗi.

**Step 1:** `make fmt-check && make vet && make lint && make vuln`
Expected: sạch; vuln: `Your code is affected by 0 vulnerabilities` (có thể kèm cảnh báo cấp module GO-2026-5932, có từ trước).

**Step 2:** `make test`
Expected: mọi package `ok`, gồm `apps/core/internal/{mutate,view,effects,work,resync,store/...}`, `tools/internal/route`, `tools/corecli/internal/e2e`.

**Step 3:** `make infra-up && make itest`
Expected: mọi package `ok`, gồm contract `storetest` cho Mongo (Edits/Hidden/ApplyEdit/ClearHistory, Task 4), feed `EditInserted` (Task 5), `TestBootstrapIsIdempotent` (collection `message_edits`, `hidden` + index), 5 itest của Task 14, `TestRealInfraResyncDrillRepublishesWritesTheReaderMissed` (có fact sửa), `TestRealInfraWorkersPublishWritesThatSkippedTheCore`, `TestRealInfraStopUnderLoadKeepsEveryAckedMessage`. Không cần chạy lại nếu sau lần xanh cuối (Task 14) chỉ đổi docs.

**Step 4:** `make image TARGET=apps/core && make core-up && make e2e`
Expected: `e2e PASS: 40 messages before and 40 after killing core-1, no loss, no duplicate, every acked seq live; seq 1 edited and seq 2 deleted on history, edit history and live`.

**Step 5:** `/metrics` trên cả hai core:

```bash
for c in chatim-core-1 chatim-core-2; do echo "== $c"; docker run --rm --network chatim_default curlimages/curl:8.11.1 -s http://$c:9090/metrics | grep -E '^chatim_core_(reconcile_running|reconcile_republished_total|effect_dropped_total|work_processed_total|work_failures_total)[ {]'; done
```

Expected:
- đúng một core có `chatim_core_reconcile_running 1`;
- cả hai core có `chatim_core_reconcile_republished_total{effect="msg_changed"}`, `chatim_core_effect_dropped_total{effect="msg_changed"} 0`, `chatim_core_effect_dropped_total{effect="edit_projection"} 0`, `chatim_core_work_failures_total 0`;
- tổng `reconcile_republished_total{effect="msg_changed"}` hai core = 0 sau e2e (fast path đã publish cả hai thay đổi của phase 3; bản gửi lại của worker là bản trùng nên không đếm). Khác 0 thì ghi lại (có event fast path bị mất), không phải lỗi.

**Step 6:** `make alerts-check`
Expected: `SUCCESS: 15 rules found` (không đổi).

**Step 7:** Resync dry-run trên compose (phạm vi 15 phút, có room e2e vừa sửa/xoá):

```bash
FROM=$(date -u -v-15M +%Y-%m-%dT%H:%M:%SZ); TO=$(date -u -v+1M +%Y-%m-%dT%H:%M:%SZ)
docker exec chatim-core-1 /app resync -from "$FROM" -to "$TO" -dry-run
```

Expected: một dòng `resync rooms=… room_records=… message_records=… edit_records=N dry_run=true` với `N ≥ 2` (fact sửa + xoá của phase 3), exit 0. (`date -v` là cú pháp macOS; Linux: `date -u -d '-15 min' +%FT%TZ`.)

**Step 8:** corebench ngắn (chỉ kiểm không lỗi):

```bash
pmset -g therm | grep CPU_Speed_Limit
make poc TOOL=corebench ARGS="-rate 1000 -duration 30s -watch 20"
```

Expected: dòng `sends due=… sent=… acked=… failed=0 …` và `live events on 20 watched rooms: … missing=0 duplicates=0 …`. Ghi p99 ack để tham khảo, không so sánh (không phải milestone perf); `CPU_Speed_Limit` < 100 thì ghi kèm.

**Step 9: Kết quả thực thi + roadmap**

Thêm cuối `docs/plans/2026-10-05-m2b2-edit-delete.md`:

```markdown
## Kết quả thực thi

Commit từng task (nhánh `feat/m2b`):

- T0 …; T1 `<sha>` domain; T2 `<sha>` proto + pbconv + publish kinds; …; T12 `<sha>` resync quét `message_edits`; T13 `<sha>`, `<sha>` route + corecli/e2e; T14 `<sha>` itest; T15 `<sha>` docs.

Lệch so với plan:

- (idiom gofmt/vet/lint đã áp; tên adapter khác hợp đồng; …)

Lỗi Minor còn mở:

1. (theo task, do controller/reviewer ghi)

Kiểm chứng cuối (Task 16, <YYYY-MM-DD>):
- `make fmt-check`, `make vet`, `make lint` sạch; `make vuln`: <kết quả>.
- `make test`: mọi package `ok`.
- `make itest`: <xanh sau Task 14 / chạy lại>.
- `make e2e`: `e2e PASS: … seq 1 edited and seq 2 deleted on history, edit history and live`.
- `/metrics` hai core: <reconcile_running, msg_changed republished/dropped, work_failures_total>.
- `make alerts-check`: `SUCCESS: 15 rules found`.
- resync dry-run: `<dòng in ra>`.
- corebench 1000/s 30s: failed=<n>, missing=<n>, duplicates=<n>, p99 ack <ms>, CPU_Speed_Limit <giá trị>.
- Mức sẵn sàng: `dev-done` trên `feat/m2b` (chưa merge `main`; merge một lần sau M2b.4).
```

`docs/roadmap.md`:
- Dòng M5: `mục mang sang từ M2b.0 và M2b.1 (xem dưới)` → `mục mang sang từ M2b.0, M2b.1 và M2b.2 (xem dưới)`.
- Mục "Mục mang sang M5 (hardening)", sau khối "Từ M2b.1 …" thêm:

```markdown
Từ M2b.2, chi tiết ở [plan, mục Kết quả thực thi](plans/2026-10-05-m2b2-edit-delete.md#kết-quả-thực-thi):

- (các Minor controller ghi ở mục Kết quả thực thi; nếu chưa có gì: "Resync: \`ErrEditPageFull\` khi một thời điểm có ≥ 1000 fact sửa của một room (chỉ báo lỗi, chưa phân trang theo \`_id\`).")
```

```bash
git add docs/plans/2026-10-05-m2b2-edit-delete.md
git commit -m "docs: record M2b.2 execution results" -- docs/plans/2026-10-05-m2b2-edit-delete.md docs/roadmap.md
```

(Nếu file plan chưa được commit ở Task 0, commit này đưa nó vào lần đầu; dòng `INDEXES.csv` của plan đã thêm ở Task 15.)

---

#### Ghi chú cho controller (part C)

**Chữ ký mới/chốt thêm (không đổi hợp đồng chung):**

```go
package resync

const editPage = 1000

var ErrEditPageFull = errors.New("resync: one instant holds more edits than an edit page")

type Edits interface {
	Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Edit, error)
}

type Deps struct {
	Rooms Rooms
	Pages Pages
	Edits Edits
	Pub   Publisher
}

type Report struct {
	Rooms, RoomRecords, MessageRecords, EditRecords int
	DryRun                                          bool
}
```

- `Report.String()` = `resync rooms=%d room_records=%d message_records=%d edit_records=%d dry_run=%t` (đổi chuỗi in ra của `/app resync`; itest drill cập nhật theo).
- `route`: `func inRoom[T any](ctx context.Context, c *Client, roomID string, do rpc[T]) (T, Stats, error)`; `Client.EditMessage/DeleteMessage/HideMessage/ClearHistory/GetEditHistory(ctx, *Req) (*Resp, Stats, error)`, cả 5 đều `retryIdempotent`.
- `e2e`: `CheckPage(want []Ack, changes []Change, got []*chatimv1.Message, room, sender string) error` (thêm tham số `changes`); `Event` thêm `Kind`, `Version`, `Text`; `State.Changes []Change`; mới `Change`, `EditOf`, `DeleteOf`, `EditTextFor`, `ChangeEventID`, `CheckChangeEvents`, `CheckVersions`, `KindCreated/KindEdited/KindDeleted`, `Event.IsChange`.
- corecli: lệnh `edit`, `delete`, `hide`, `clear`, `edits`; bước `e2e change`; `scripts/e2e.sh` phase 3 (dòng PASS dài thêm).

**Caller đã cập nhật:** `resync.Deps{...}` ở `apps/core/resync_command.go` và `scan_test.go` (`world.deps`); `e2e.CheckPage` ở `tools/corecli/e2e_check.go` (3 chỗ) và `check_test.go` (4 chỗ); `e2e.EventOf` ở `tools/corecli/cmd_watch.go` (không đổi chữ ký); `SendMessage`/`GetHistory` của route chuyển sang `inRoom` (hành vi y hệt).

**Giả định về part A/B (cần khớp, sai thì chỉ đổi tên):**
- `memstore.NewEdits() *memstore.Edits` có `Append`/`Between`; `*mongostore.Store` cài `store.Edits` (Task 12, 14 gọi `st.Append`, `st.History`, `st.Find`) — cùng mẫu một struct `Store` như hiện tại.
- `Between` sắp theo `ts` rồi `_id`, `[from, to]` bao gồm hai đầu, `limit` tối đa 1000.
- Proto Go: `EditMessageRequest{RoomId, ThreadRoot, Seq, BaseVersion, Text}`, `ClearHistoryRequest{RoomId, UpToSeq}`, `GetEditHistoryRequest{…, AfterVersion, Limit uint32}`, `Message.{Version, Deleted, EditedAt, Hidden}`, `Event_MessageEdited`/`Event_MessageDeleted` với `{Message, Version}`, `EditKind_EDIT_KIND_ORIGINAL/TEXT/DELETE`.
- `ClearHistory` trả giá trị `cb` sau `$max`; `HideMessage` từ chối seq không tồn tại.
- Registry Task 11: `EditInserted: [room_activity, edit_projection, msg_changed]` theo đúng thứ tự (itest (b) và drill dựa vào việc projection xong trước khi event được publish); `edit_projection` chỉ xuất `effect_dropped_total`.
- `GetEditHistory`: tin không có → `NOT_FOUND` (itest (a) khẳng định), đã xoá → rỗng. `ClearHistory` kẹp `up_to_seq` 0 hoặc quá seq cuối về `Last(room, 0)`. Mã lỗi: `ErrVersionConflict`/`ErrMessageDeleted` → `FailedPrecondition`, `access.ErrDenied` (policy từ chối; mặc định: không phải tác giả, kể cả owner room, D86) → `PermissionDenied`, `ErrMessageNotFound` → `NotFound`.
- Task 2 phải làm `tools/internal/route` biên dịch được sau khi `CoreServiceClient` có thêm 5 method (`make vet` biên dịch cả test): Task 13 xử lý cả hai cách (stub hoặc nhúng interface).

**Rủi ro / việc controller cần quyết:**
1. **Retry lệnh xoá (D63) phụ thuộc thứ tự kiểm trong `mutate` (Task 7).** Luồng hợp đồng bước 2 trả `ErrMessageDeleted` khi tin đã xoá *trước* bước nhận diện retry; vậy gửi lại `DeleteMessage` cùng base sau khi lần đầu đã thành công (ack mất) sẽ nhận `FAILED_PRECONDITION` thay vì thành công. Route client retry `DeleteMessage` khi timeout nên lỗi này lộ ra thật. Task 14 (c) khẳng định retry thành công; nên kiểm Task 7: nhận diện retry (`BaseVersion+1 == version hiện tại` và fact ở đó trùng tác giả/loại/text) phải chạy trước kiểm "đã xoá". (Đã áp ở Task 7; policy hỏi trước retry, D86.)
2. `ChatimRepublishSurge` không cần sửa: theo controller, `msg_changed` chỉ đếm republish khi PubAck không phải bản trùng (Task 15 Step 1 chỉ kiểm). Nếu Task 11 thực tế đếm mọi lần chạy thì luật này báo động giả ở 100–300 lệnh đổi/s: khi đó lọc `{effect="msg_created"}` (15 luật không đổi).
3. Resync tìm room chỉ có sửa/xoá nhờ registry `EditInserted → [room_activity (Seq 0, chỉ lc/ab), edit_projection, msg_changed]` (controller xác nhận); Task 12/15 viết theo đó. Nếu Task 11 bỏ `room_activity` cho edit thì phải khôi phục câu "dùng `-room`" trong docs.
4. `INDEXES.csv`, `CLAUDE.md`, `README.md`, thiết kế, roadmap đang có thay đổi chưa commit của owner (đổi link `docs/research` → `docs/archive/research`, report tổng hợp mới). `git commit -- INDEXES.csv` sẽ kéo cả thay đổi đó vào commit của task. Task 12–14 kiểm `git diff --quiet -- INDEXES.csv`, Task 15 Step 0 kiểm tất cả; controller nên để owner commit (hoặc xác nhận gộp) trước Task 12. Part A/B cũng sửa `INDEXES.csv` nên cùng rủi ro.
5. File plan `docs/plans/2026-10-05-m2b2-edit-delete.md` đang untracked; Task 16 commit nó nếu Task 0 chưa commit.
6. `make e2e` ở Task 13 cần image core có Task 9–11; e2e phase 3 chỉ dựa vào fast path (event từ `mutate`), không cần worker, nên chạy được ngay sau Task 9.
7. itest (d) dựa vào hai RPC tới gần đồng thời; mọi cách xen kẽ đều cho một thắng + một `FAILED_PRECONDITION` (base lệch hoặc trùng khoá khác text), nên không flaky theo thời điểm.

