# M2b.3 — Reaction + ghim — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: dùng `subagent-driven-development` (cùng phiên) hoặc `separate-driven-development` (phiên riêng) để thực thi plan này theo từng task. Implementer dùng skill `go-lang`.

**Goal:** Reaction một emoji cho mỗi (user, tin) theo lớp **tập** (§4): doc `reactions` `_id = k│u` mang change number `n`, event `reaction_changed`; số đếm theo emoji là **aggregate** `messages.rx` ghi bằng recount CAS (§7, không `$inc`), touch ở fast path và ở worker, event `counts_changed`. Ghim/bỏ ghim theo **fact + projection** (D62): fact `pin_actions` `room│pv` với pv dày, projection `rooms.pins` + `pv` bằng fold + CAS, event `msg_pinned`/`msg_unpinned`. Feed thêm insert/update/replace của `reactions` và insert của `pin_actions`; bốn effect mới trên work stream; resync quét thêm hai collection.

**Architecture:**
- `pkg/keys`: `Reaction` (24B khoá tin + byte user), `Pin` (16B `room│pv`).
- `domain`: `Reaction`, `ReactionCount`, `ReactionSummary` (gắn vào `Message.Reactions`), `ValidateEmoji`, `SortReactionCounts`; `PinAction`, `Pin`, `PinState`, `FoldPins` (thuần); lỗi `ErrTooManyEmojis`, `ErrTooManyPins`. `Room` **không** thêm field ghim (đọc qua `PinProjector`, không làm nặng `Rooms.Get` của `access.Admit`).
- `store`: port `Reactions`, `ReactionSummaries` (CAS `rx.v`), `Pins`, `PinProjector` (CAS `pv`); `ErrStaleRead`, `Witness`; `ChangeKind` `ReactionChanged`, `PinInserted`. Mongo: clustered `reactions` (index `{k, e}`, `{r, ts}`) và `pin_actions` (index `{r, ts}`); `messages.rx`, `rooms.pins/pv`.
- Feed: insert của `reactions`/`pin_actions` + update/replace của `reactions` (không `updateLookup`). `work.Record` thêm đuôi user cho `ReactionChanged`; kind lạ nhưng đúng dạng → `Nak`, không `Term`. `room_activity` để `Seq = 0` cho mọi kind trừ `MessageInserted`.
- Package mới `counter` (touch: witness → đếm → CAS) và `pinproj` (đọc state → quét fact sau pv → `FoldPins` → CAS); dùng chung cho `mutate` và `effects`, nên `effects` không import `mutate`.
- `access`: action `ReactMessage`, `PinMessage`, `UnpinMessage`; `DefaultPolicy` cho member (D94).
- `mutate`: `React`, `Pin`, `Unpin`; `Deps` thêm `Reactions`, `Counter`, `Pins`, `Projector`, `Limits`.
- `grpcsrv`: 3 RPC (`ReactMessage`, `PinMessage`, `UnpinMessage`); `GetHistory` trả `Message.reactions` (chỉ số đếm); `view` che `Reactions` của tin xoá/ẩn.
- `effects`: `reaction_counter`, `reaction_event`, `pin_projection`, `pin_event`; metric `counter_repaired_total{counter}`, luật `ChatimCounterRepairSurge` (15 → 16 luật).
- `/app resync` quét `reactions` và `pin_actions` theo `{r, ts}`; route + `corecli` + e2e có react/ghim.

**Tech Stack:** Go 1.26 trong Docker qua `make`; buf; mongo-driver v2 (clustered collection, `FindOneAndUpdate` pipeline có upsert, causal session, aggregate phủ index, change stream update/replace); nats.go jetstream; `testing/synctest`; goleak.

**Nguồn quyết định:** [thiết kế](../designs/261005-chatim-architecture.md) §4, §5, §5.1, §6.3, §6.4, §7, §8.3, §12, D62, D64, D65, D67, D69, D70, D79–D87; [roadmap](../roadmap.md) dòng M2b.3. Owner chốt 2026-10-06:
- Reaction **một emoji cho mỗi user trên một tin**; emoji khác thay emoji cũ; emoji rỗng là gỡ. "Nhiều trên một user" là reply/mention (M2c).
- Emoji bất kỳ, chỉ kiểm định dạng; tối đa `REACTION_MAX_EMOJIS` loại trên một tin (mặc định 20, giới hạn mềm).
- Ghim/bỏ ghim mặc định mọi member (`access.Policy`, như D86); tối đa `PIN_LIMIT` (mặc định 50, chính xác).
- Counter theo §7 (touch fast path + touch worker).
- **Bỏ `base_pv`**: server đọc pv hiện tại rồi ghi pv+1; ghim tin đã ghim / bỏ ghim tin chưa ghim = thành công, không fact.
- `GetHistory` chỉ trả số đếm theo emoji; emoji của chính người đọc để M3 (`GetReactions`).

Quyết định mới, Task 17 ghi vào Decision Log:
- **D88** Reaction một emoji/(user, tin): clustered `reactions` `_id = k│u`, shard `{_id: 1}`, index `{k, e}` + `{r, ts}`; thay D68.
- **D89** Đặt = upsert có điều kiện `e ≠ emoji` (pipeline `n+1`, `pe` = emoji cũ), trùng khoá → đọc lại, cùng emoji là no-op, khác thì thử lại ≤3; gỡ = update không upsert, để tombstone `e:""`; không cid (retry trễ có thể khôi phục trạng thái cũ, chấp nhận cho lớp tập); `ValidateEmoji` (UTF-8, 1–32 byte, không ký tự điều khiển); giới hạn emoji là mềm (đọc từ `rx`).
- **D90** Summary `messages.rx {c: [{e, n}], v}` (mảng, không map); touch = witness (doc của user có `n ≥ N`, majority, causal session) → aggregate phủ index → bằng thì bỏ, khác thì CAS `rx.v`; touch inline trước khi trả lời (≤3 lần CAS, lỗi bỏ qua) + touch worker một lần mỗi tin mỗi lô sau `REACTION_COUNT_DELAY`; cửa sổ gom W và bucket K để milestone Channel (tinh chỉnh D67).
- **D91** Feed thêm insert/update/replace của `reactions` (giải mã `documentKey._id` + `updatedFields.n`), insert của `pin_actions`; `work.Record` thêm đuôi user cho `ReactionChanged`; kind lạ đúng dạng → `Nak` có đếm; `room_activity` để `Seq = 0` cho mọi kind trừ `MessageInserted`; rolling deploy cần nâng mọi core trước khi có record mới (prod chưa live); Nak kind lạ không giới hạn, `ChatimWorkFailing` kêu khi còn core cũ; resync chọn room theo `ab`, room chỉ có reaction/ghim trong khoảng mất cần `-room`.
- **D92** Ghim: bỏ `base_pv`; pv dày (current+1, CAS bằng khoá); `PIN_LIMIT` chính xác; đúng trạng thái sẵn = thành công không fact; ghim tin đã xoá → `FAILED_PRECONDITION`, bỏ ghim vẫn được; xoá tin không đụng ghim; projection fold + CAS `pv == p` dùng chung fast path/worker (thay quy tắc `pv < v` ở §6.3); trùng khoá hết lượt → `ErrRetryLater`.
- **D93** Event: `reaction_changed` `{room}-{th}-{seq}-{u}-n{n}`, `counts_changed` `{room}-{th}-{seq}-reactions-v{v}`, `msg_pinned`/`msg_unpinned` `{room}-p{pv}` mang snapshot tin (tin xoá thì không text); không ack mark; cid SysMsg `sys-{event_id}` (sửa P7, vì `ValidCID` không nhận `:`).
- **D94** Action `react_message`, `pin_message`, `unpin_message`; `DefaultPolicy` cho member; `MESSAGE_LOCKED_KINDS` (D87) không áp cho chúng.

---

## Quy tắc chung (đọc trước khi làm task nào)

- **Không chạy `go` trên host.** Test một package: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/<pkg>/..."`. Tra API: `make -s go ARGS="doc <pkg> <Symbol>"`.
- **Không viết comment** trong Go, test, YAML, shell, Makefile, proto mới.
- File code dưới 200 dòng; `wc -l` sau mỗi lần sửa file lớn.
- `gosec`: không chuyển `int`/`int64` → `uint*` (và ngược lại) khi chưa chặn biên (G115). Mongo lưu `n`, `pv`, `rx.v`, `th`, `s`, số đếm dưới dạng int; đọc khoan dung `AsInt64OK` (int32/int64/double), âm hoặc vượt kiểu Go → `errCorrupt`. `pv`/`rx.v` > `MaxInt64` bị store từ chối.
- Trước mỗi commit: `make fmt-check`, `make vet`, `make lint` + test các package đã đụng. Goroutine/channel/lock mới: thêm `-count=5`.
- Sửa lint/vet thuần idiom/format có hành vi y hệt: được áp và ghi vào báo cáo. Lệch khác, kết quả khác "Expected", test cũ fail: **dừng và báo cáo**.
- Đổi adapter Mongo/NATS hoặc wiring `apps/core`: `make itest` một lần cuối task (cần `make infra-up`).
- Commit theo Conventional Commits, không nhắc AI, không có dòng `Co-Authored-By`. Nhánh `feat/m2b`. **Push sau Task 7, Task 13 và Task 18** (`git push origin feat/m2b`); không push ở task khác.
- Review: Task 5, 6, 7, 9, 10, 11, 13 rủi ro → mỗi task 1 reviewer (spec + chất lượng một lượt, tối đa `-count=3` trên package đụng). Task khác controller kiểm nhanh. Lỗi Minor ghi vào "Kết quả thực thi".

## Bảng tính năng → lớp dữ liệu (quy tắc roadmap)

| Tính năng | Lớp (§4) | Fact / ghi | Idempotent | Effect + chính sách | Quyền | View | Event (id) | Khuếch đại nhóm 5K | Khuếch đại channel 200K | Guarantee + detector |
|---|---|---|---|---|---|---|---|---|---|---|
| Đặt/gỡ reaction | Tập (target, user) | `FindOneAndUpdate` upsert `reactions {_id: k│u, e ≠ emoji}`: `n+1`, `pe`, `e`, `ts`; gỡ: update không upsert, `e ≠ ""` | Lệnh là trạng thái mong muốn; đúng trạng thái sẵn = no-op (không ghi, không event); trùng khoá → đọc lại, ≤3 lần | fast path enqueue; worker `room_activity` (0, chỉ `lc/ab`) + `reaction_event` (`RECONCILE_DELAY`, không mark; doc `n == rec.n` mới phát, `>` bỏ, `<` Nak) | `react_message` (member) | — (emoji của mình: M3) | `reaction_changed` `{room}-{th}-{seq}-{u}-n{n}` | fast: 3 read (room, member, tin) + 1 write majority + 1 event; worker: ≤1 write activity/room/lô + 1 read + ≤1 publish (bỏ trùng). Phát tới 5K người là việc gateway | như 5K mỗi thao tác; phát tới 200K người do gateway | RC1: `reconcile_republished_total{effect="reaction_event"}`, `effect_dropped_total{effect="reaction_event"}`, `work_failures_total`; alert `ChatimWorkFailing`, `ChatimEffectDropping` |
| Số reaction theo emoji | Aggregate theo target | `messages.rx {c: [{e, n}], v}`; CAS `rx.v == k` (`$exists: false` khi k = 0), không `$inc` | recount tuyệt đối; bằng nhau → không ghi, không bump `v` | fast path touch inline (≤3 CAS, lỗi bỏ qua); worker `reaction_counter` (`REACTION_COUNT_DELAY`, 1 touch/tin/lô, witness của mọi record, phát lại `counts_changed` của `v` hiện tại) | — | `Message.reactions` trong `GetHistory`; tin xoá/ẩn: rỗng | `counts_changed` `{room}-{th}-{seq}-reactions-v{v}` | mỗi touch: 1 read witness + 1 aggregate phủ index `{k, e}` O(số reaction của tin) ≤ 5K khoá + ≤1 CAS + ≤1 event; worker lặp 1 lần/tin/lô | **⚠ target nóng**: post nhận R react/s đếm lại ≤ 200K khoá mỗi lần (500/s × 100K ≈ 50M khoá index/s) và CAS tranh nhau. Cửa sổ gom W + bucket `hash(u) % K` (§7.6) **để milestone Channel**, bắt buộc trước khi channel go-live | CT1 (mới): summary hội tụ về số doc `e ≠ ""` → `counter_repaired_total{counter="reactions"}`, alert `ChatimCounterRepairSurge`; `work_failures_total` (witness cũ, CAS hết lượt) |
| Ghim / bỏ ghim | Fact + projection | insert `pin_actions {_id: room│pv}` với pv = current+1 (dày); projection `rooms.pins` + `pv` CAS `pv == p` | đúng trạng thái sẵn = thành công không fact; trùng khoá → đọc fact ở pv: cùng op/tin/người = kết quả, khác → thử lại ≤3 rồi `ErrRetryLater` | fast path fold + CAS + enqueue; worker `room_activity` (0) + `pin_projection` (0, 1 lần/room/lô) + `pin_event` (`RECONCILE_DELAY`, không mark) | `pin_message` / `unpin_message` (member) | response trả `pins` hiện tại | `msg_pinned`/`msg_unpinned` `{room}-p{pv}` + snapshot tin | fast: 3 read + 1 read `rooms.pins` + 1 range scan fact sau pv + 1 insert + 1 CAS majority + 1 event; worker: 1 read + ≤1 CAS; event: 1 read fact + 1 `Find` + publish | như 5K (ghim hiếm, `rooms` doc không nóng) | PJ1: `effect_dropped_total{effect="pin_projection"}`, `{effect="pin_event"}`, `reconcile_republished_total{effect="pin_event"}`, `work_failures_total` |

## Hợp đồng chung (mọi task phải khớp đúng chữ ký này)

Struct/interface viết gọn một dòng ở đây chỉ là ký hiệu; code thật để `gofmt` dàn dòng, giữ đúng tên, kiểu và thứ tự field.

### `pkg/keys` (Task 2)

```go
const (
	PinLen          = 16
	MaxReactionUser = 64
)

func Reaction(room, threadRoot, seq uint64, user string) []byte
func ParseReaction(b []byte) (room, threadRoot, seq uint64, user string, err error)
func Pin(room, pv uint64) []byte
func ParsePin(b []byte) (room, pv uint64, err error)
```

- `Reaction` = `Msg(room, threadRoot, seq)` nối byte của `user` (không tiền tố độ dài). `ParseReaction`: `MsgLen < len(b) ≤ MsgLen + MaxReactionUser`, không thì `ErrLength`. `Pin` = room(8) pv(8) big-endian; `ParsePin` cần đúng `PinLen`.

### `apps/core/internal/domain` (Task 2)

```go
type Reaction struct {
	Room, Thread, Seq          uint64
	Tenant, User, Emoji, Prev string
	N                          uint32
	At                         time.Time
}

type ReactionCount struct{ Emoji string; Count uint32 }
type ReactionSummary struct{ Counts []ReactionCount; Version uint64 }

type PinOp uint8

const (
	PinOpPin PinOp = iota + 1
	PinOpUnpin
)

type PinAction struct {
	Room, PV    uint64
	Tenant      string
	Op          PinOp
	Thread, Seq uint64
	By          string
	At          time.Time
}

type Pin struct{ Thread, Seq uint64; By string; At time.Time; PV uint64 }
type PinState struct{ Pins []Pin; Version uint64 }

var (
	ErrTooManyEmojis = fmt.Errorf("too many reaction emojis: %w", apperr.ErrFailedPrecondition)
	ErrTooManyPins   = fmt.Errorf("too many pinned messages: %w", apperr.ErrFailedPrecondition)
)

func ValidateEmoji(emoji string) error
func SortReactionCounts(counts []ReactionCount)
func FoldPins(s PinState, facts []PinAction) PinState
func (s PinState) Pinned(thread, seq uint64) bool
```

- `Message` thêm field cuối `Reactions ReactionSummary`. Từ đây `domain.Message` **không còn so sánh được bằng `==`**; Task 2 sửa mọi chỗ `make vet` báo: `storetest/fixtures.go` (`sameMessage`: so `Reactions` bằng `slices.Equal` + `Version`), `storetest/feed_room_cases.go:42` và `storetest/feed_edit_cases.go:20` (`c.Msg != (domain.Message{})` → `c.Msg.Seq != 0`).
- `Reaction.Emoji == ""` là tombstone (đã gỡ); `Prev` = emoji trước lần đổi này; `N` = change number (lần đổi đầu = 1).
- `ValidateEmoji`: rỗng, không phải UTF-8 hợp lệ, > 32 byte, hay có rune `unicode.IsControl` → lỗi `invalid("emoji")` (`ErrInvalidArgument`). Gọi chỉ khi emoji khác rỗng.
- `SortReactionCounts`: sắp tại chỗ theo `Count` giảm dần rồi `Emoji` tăng dần. Mọi adapter trả số đếm đã sắp.
- `FoldPins` thuần (không sửa slice đầu vào): áp các fact `PV > s.Version` theo thứ tự PV; pin tin chưa ghim → chèn `Pin{…, PV: a.PV}` lên **đầu** (mới nhất trước); unpin → gỡ; pin tin đã ghim / unpin tin chưa ghim → bỏ qua; `Version` = PV của fact cuối đã áp.
- `PinOp`, `PinOpPin = 1`, `PinOpUnpin = 2` là giá trị lưu trong `pin_actions.op`.

### `apps/core/internal/store` (Task 4; Mongo Task 5; feed Task 6)

```go
const MaxReactionScan, MaxPinScan = 1000, 1000

var (
	ErrStaleRead         = fmt.Errorf("read is behind a witnessed write: %w", apperr.ErrUnavailable)
	ErrReactionContended = fmt.Errorf("reaction write contended: %w", apperr.ErrUnavailable)
	ErrPinExists         = fmt.Errorf("pin version %w", apperr.ErrAlreadyExists)
	ErrPinNotFound       = fmt.Errorf("pin action %w", apperr.ErrNotFound)
)

type Witness struct{ User string; N uint32 }

type Reactions interface {
	Set(ctx context.Context, r domain.Reaction) (domain.Reaction, bool, error)
	Remove(ctx context.Context, key MsgKey, user string, at time.Time) (domain.Reaction, bool, error)
	Get(ctx context.Context, key MsgKey, user string) (domain.Reaction, bool, error)
	Count(ctx context.Context, key MsgKey, witnesses []Witness) ([]domain.ReactionCount, error)
	Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Reaction, error)
}

type ReactionSummaries interface {
	SetReactions(ctx context.Context, key MsgKey, base uint64, s domain.ReactionSummary) (bool, error)
}

type Pins interface {
	Append(ctx context.Context, a domain.PinAction) error
	At(ctx context.Context, room, pv uint64) (domain.PinAction, error)
	After(ctx context.Context, room, pv uint64, limit int) ([]domain.PinAction, error)
	Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.PinAction, error)
}

type PinProjector interface {
	PinState(ctx context.Context, room uint64) (domain.PinState, error)
	ApplyPins(ctx context.Context, room, base uint64, s domain.PinState) (bool, error)
}

func ReactionKeyOf(r domain.Reaction) MsgKey
func ValidateReaction(r domain.Reaction) error
func ValidatePinAction(a domain.PinAction) error
```

- File mới `store/reaction.go`, `store/pin.go`. Interface riêng (không thêm vào `Messages`/`Rooms`); memstore `Messages` cài `ReactionSummaries`, memstore `Rooms` cài `PinProjector`. `*mongostore.Store` cài `ReactionSummaries` và `PinProjector`; `store.Reactions` và `store.Pins` do kiểu con `mongostore.Reactions`/`mongostore.Pins` cài, lấy qua accessor `func (s *Store) Reactions() *Reactions` và `func (s *Store) Pins() *Pins` (`Store` đã có `Append/At/Between/Get` của `Edits`/`Rooms` khác chữ ký, Go không cho trùng tên).
- `Set`: `r.Emoji` khác rỗng, `ValidateReaction` (khoá hợp lệ, `domain.ValidUser(r.User)`, `r.Tenant` khác rỗng). Đúng trạng thái sẵn (`e == emoji`) → `(doc hiện tại, false, nil)`; đổi → `(doc sau ghi, true, nil)` với `N` tăng 1, `Prev` = emoji cũ (`""` khi doc mới). Mongo: trùng khoá → đọc lại, thử ≤3 lần, hết → `ErrReactionContended`.
- `Remove`: không có doc hoặc đã tombstone → `(doc hiện tại hoặc zero, false, nil)`, **không** chèn tombstone; có emoji → `e = ""`, `pe` = emoji cũ, `n+1`, `ts = at` → `(doc sau, true, nil)`.
- `Get`: đọc majority. `Count`: mọi witness phải có doc với `n ≥ N`, không thì `ErrStaleRead`; trả số doc `e ≠ ""` theo emoji, đã `SortReactionCounts`, slice rỗng khi không có.
- `Between`: doc (kể cả tombstone) của room có `ts ∈ [from, to]`, sắp theo `ts` rồi `_id`, `limit` 1..`MaxReactionScan` (`ValidateLimit`).
- `SetReactions`: `$set rx = {c, v}` chỉ khi `rx.v == base` (`base == 0` → `rx.v` không tồn tại); cần `s.Version > base`, không thì `ErrInvalidArgument`. Tin không có hoặc CAS trượt → `(false, nil)`.
- `Append`: `ValidatePinAction` (room, PV ≠ 0 và ≤ `MaxInt64`, op hợp lệ, seq ≠ 0, `ValidUser(By)`); trùng khoá → `ErrPinExists`. `At` không có → `ErrPinNotFound`. `After`: fact `PV > pv` tăng dần, tối đa `limit` (1..`MaxPinScan`). `Between`: như `Reactions.Between`.
- `PinState`: room không có → `domain.ErrRoomNotFound`; room chưa ghim → `PinState{}`. `ApplyPins`: `$set pins, pv = s.Version` chỉ khi `pv == base` (`base == 0` → `pv` không tồn tại); cần `s.Version > base`; trượt → `(false, nil)`.
- Feed (Task 4 khai báo, Task 6 phát): `ReactionChanged ChangeKind = 4`, `PinInserted ChangeKind = 5`; `Change` thêm `Reaction domain.Reaction`, `Pin domain.PinAction`. Với update của Mongo, `Change.Reaction` **chỉ chắc** có `Room, Thread, Seq, User, N`. Write no-op (đúng trạng thái sẵn, CAS trượt) **không** sinh change.
- Write contract (`write_contract_test.go`): `Reactions.Set` và `Reactions.Remove` = `version-bump`; `Get/Count/Between` = `read`; `ReactionSummaries.SetReactions` = `cas`; `Pins.Append` = `insert-unique`; `Pins.At/After/Between` = `read`; `PinProjector.PinState` = `read`; `PinProjector.ApplyPins` = `cas`. Thêm 4 port vào danh sách reflect.
- memstore: `NewReactions() *Reactions`, `NewPins() *Pins` (Task 4); `NewFeed(msgs *Messages, rooms *Rooms, edits *Edits, opts ...FeedOption) *Feed`, `WithReactions(*Reactions) FeedOption`, `WithPins(*Pins) FeedOption` (Task 6; caller cũ không đổi).
- storetest: `type ReactableMessages interface { store.Messages; store.ReactionSummaries }`, `type PinnableRooms interface { store.Rooms; store.PinProjector }`; `RunReactions(t, open func(*testing.T) (ReactableMessages, store.Reactions))`, `RunPins(t, open func(*testing.T) (PinnableRooms, store.Pins))` (Task 4); `RunReactionFeed(t, open func(*testing.T) (store.Reactions, store.ChangeFeed))`, `RunPinFeed(t, open func(*testing.T) (store.Pins, store.ChangeFeed))` (Task 6). Chạy trên memstore và mongostore (itest).

### Mongo (Task 5; feed Task 6)

- Hằng: `reactionsCollection = "reactions"`, `pinActionsCollection = "pin_actions"`. `Store` thêm `reactions` (read concern majority, write concern như `messages`) và `pins` (primary). Count chạy trong session `SetCausalConsistency(true)` lấy từ client của database.
- `reactions` (clustered): `{_id: keys.Reaction(...), k: binary 24B keys.Msg, r: int64, t, u, e, pe, n, ts}`. Index `{k: 1, e: 1}`, `{r: 1, ts: 1}`. `Set` = `FindOneAndUpdate({_id, e: {$ne: emoji}}, pipeline [{$set: {k, r, t, u, pe: {$ifNull: ["$e", ""]}, e: emoji, n: {$add: [{$ifNull: ["$n", 0]}, 1]}, ts}}], upsert, ReturnDocument After)`. `Remove` = cùng dạng, filter `{_id, e: {$ne: ""}}`, không upsert. `Count` = đọc witness `{_id: {$in}}` majority rồi aggregate `[{$match: {k, e: {$gt: ""}}}, {$group: {_id: "$e", n: {$sum: 1}}}]` cùng session.
- `pin_actions` (clustered): `{_id: keys.Pin(room, pv), r: int64, t, op: int32, th: int64, s: int64, by, ts}`. Index `{r: 1, ts: 1}`.
- `messages` thêm `rx: {c: [{e, n}], v: int64}` (con trỏ, bỏ khi rỗng; `Insert` không bao giờ ghi `rx`); `Page`/`Find` đọc `rx`.
- `rooms` thêm `pins: [{th, s, by, ts, pv}]` và `pv: int64` (omitempty). `decodeRoom`/`Rooms.Get` **không** đọc hai field này; `PinState` đọc bằng projection `{pins, pv}`.
- `Bootstrap`: `reactions`, `pin_actions` vào danh sách `ensureClustered`; thêm `reactionIndexes()`, `pinIndexes()`.
- Feed `$match` (Task 6): `{$or: [{operationType: "insert", "ns.coll": {$in: [messages, rooms, message_edits, reactions, pin_actions]}}, {operationType: {$in: ["update", "replace"]}, "ns.coll": "reactions"}]}`. `changeDoc` thêm `OperationType string bson:"operationType"`, `DocumentKey{ID bson.RawValue bson:"_id"}`, `UpdateDescription{UpdatedFields bson.Raw bson:"updatedFields"}`. Insert/replace → giải mã `fullDocument`; update → `keys.ParseReaction(documentKey._id)` + `updatedFields.n`; thiếu `n`, `n` ngoài 1..`MaxUint32` hoặc user sai `domain.ValidUser` → `ErrCorruptChange` (reader drop có đếm). Update của `messages` (`rx`, edit) và `rooms` (activity, pins) vẫn bị loại bởi `ns.coll`.

### `apps/core/internal/work` (Task 7)

```go
const RecordSize, MaxRecordSize = 37, 37 + 1 + 64

var ErrUnknownKind = fmt.Errorf("%w: unknown kind", ErrBadRecord)

type Record struct {
	Kind        store.ChangeKind
	Room        uint64
	Thread      uint64
	Seq         uint64
	Version     uint32
	User        string
	CommittedAt time.Time
}

type BadRecordsError struct{ Terminated, Deferred uint64 }

func NewQueue(js jetstream.JetStream, stream string, partition int, retryDelay time.Duration) Queue
```

- Layout: kind(1) room(8) thread(8) seq(8) version(4) unixNano(8) [userLen(1) user(userLen)], big-endian. `Encode` thêm đuôi khi `User != ""`.
- `Decode`: độ dài 37, hoặc `38 + L` với `L = b[37]` trong 1..64; khác → `ErrBadRecord`. Đúng dạng nhưng `!KnownKind` → `ErrUnknownKind`. `ReactionChanged` bắt buộc có đuôi với `domain.ValidUser`; kind khác có đuôi → `ErrBadRecord`. Record 37 byte cũ vẫn giải mã như trước.
- `KnownKind` nhận thêm `ReactionChanged`, `PinInserted`. `RecordOf`: `ReactionChanged` → room/thread/seq/user từ `c.Reaction`, `Version = c.Reaction.N`; `PinInserted` → `Room = c.Pin.Room`, `Seq = c.Pin.PV` (thread 0, version 0).
- `ID()`: `ReactionChanged` → `"x:" + pbconv.ReactionEventID(room, thread, seq, user, version)`; `PinInserted` → `"p:" + pbconv.PinEventID(room, seq)`.
- `collect`: `ErrUnknownKind` → `NakWithDelay(retryDelay)`, đếm `Deferred`; lỗi khác → `Term`, đếm `Terminated`. `effects` cộng `Terminated + Deferred` vào `work_failures_total`. `effects_wiring.go` truyền `cfg.Effects.RetryDelay`.
- `effects.latestActivity`: `Seq = 0` cho **mọi** kind trừ `MessageInserted` (khoá gộp `activityKey{room, thread, msg bool}`). Task 7 đăng ký tạm `store.ReactionChanged: {activity.Effect()}`, `store.PinInserted: {activity.Effect()}`; Task 13 thêm effect còn lại.

### `apps/core/internal/counter` (Task 8, package mới)

```go
type Messages interface {
	Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error)
	store.ReactionSummaries
}
type Reactions interface{ Count(ctx context.Context, key store.MsgKey, witnesses []store.Witness) ([]domain.ReactionCount, error) }

var ErrContended = fmt.Errorf("reaction summary contended: %w", apperr.ErrUnavailable)

func New(msgs Messages, reactions Reactions) (*Toucher, error)
func (t *Toucher) Touch(ctx context.Context, key store.MsgKey, cur domain.ReactionSummary, witnesses []store.Witness, tries int) (domain.ReactionSummary, bool, error)
```

- Mỗi lượt (≤ `tries`): `Count` → `slices.Equal(counts, cur.Counts)` thì trả `(cur, false, nil)` → `SetReactions(key, cur.Version, {counts, cur.Version+1})` → khớp thì `(mới, true, nil)` → trượt thì `Find` lại (không còn tin → `domain.ErrMessageNotFound`) lấy `cur` mới. Hết lượt → `ErrContended`. `ErrStaleRead` và lỗi store trả nguyên.

### `apps/core/internal/pinproj` (Task 8, package mới)

```go
type Facts interface{ After(ctx context.Context, room, pv uint64, limit int) ([]domain.PinAction, error) }

const MaxTries = 5

var ErrContended = fmt.Errorf("pin projection contended: %w", apperr.ErrUnavailable)

func New(facts Facts, rooms store.PinProjector) (*Projector, error)
func (p *Projector) Current(ctx context.Context, room uint64) (domain.PinState, error)
func (p *Projector) Project(ctx context.Context, room, target uint64) (domain.PinState, error)
```

- `Current`: `PinState` rồi lặp `After(room, s.Version, store.MaxPinScan)` + `domain.FoldPins` tới khi trang ngắn hơn `MaxPinScan`; không ghi. `Version` của kết quả = max(`rooms.pv`, pv fact cuối).
- `Project`: tối đa `MaxTries` lượt: đọc state `p`; `p.Version ≥ target` → trả `p`; fold như `Current`; kết quả `< target` → `store.ErrStaleRead`; `ApplyPins(room, p.Version, folded)` khớp → trả; trượt → lượt sau. Hết lượt → `ErrContended`.

### `apps/core/internal/access` (Task 9)

Thêm `ReactMessage Action = "react_message"`, `PinMessage Action = "pin_message"`, `UnpinMessage Action = "unpin_message"`. `DefaultPolicy.Check` không đổi logic (chỉ chặn edit/delete), nên ba action mới cho phép member; test chốt điều đó và chốt `LockedKinds` không áp cho chúng. Thứ tự ở `mutate`: `Admit` → `Find` tin → `Allow` (`Author = msg.From`, `Kind = msg.Kind`).

### `apps/core/internal/mutate` (Task 9: React; Task 10: Pin/Unpin)

```go
type CounterToucher interface {
	Touch(ctx context.Context, key store.MsgKey, cur domain.ReactionSummary, witnesses []store.Witness, tries int) (domain.ReactionSummary, bool, error)
}
type PinProjector interface {
	Current(ctx context.Context, room uint64) (domain.PinState, error)
	Project(ctx context.Context, room, target uint64) (domain.PinState, error)
}
type Limits struct{ MaxEmojis, PinLimit int }

const (
	DefaultMaxEmojis = 20
	MaxEmojisCap     = 100
	DefaultPinLimit  = 50
	MaxPinLimit      = 1000
	FastTouchTries   = 3
)

type ReactCmd struct{ Tenant, User string; Room, Thread, Seq uint64; Emoji string }
type ReactResult struct{ Change uint32; Reactions domain.ReactionSummary }
type PinCmd struct{ Tenant, User string; Room, Thread, Seq uint64 }

func (l Limits) Validate() error
func (m *Mutator) React(ctx context.Context, c ReactCmd) (ReactResult, error)
func (m *Mutator) Pin(ctx context.Context, c PinCmd) (domain.PinState, error)
func (m *Mutator) Unpin(ctx context.Context, c PinCmd) (domain.PinState, error)
```

- `Deps` thêm `Reactions store.Reactions`, `Counter CounterToucher`, `Limits Limits` (Task 9) và `Pins store.Pins`, `Projector PinProjector` (Task 10). `New` bắt buộc các dep mới (nil → `errMissingDeps`, cập nhật thông điệp); `Limits` zero từng trường → mặc định (`cmp.Or`), rồi `Validate` (`MaxEmojis` 1..`MaxEmojisCap`, `PinLimit` 1..`MaxPinLimit`, sai → `ErrInvalidArgument`). Task nào thêm dep thì sửa `mutate/fixtures_test.go` và `apps/core/service_wiring.go` trong cùng task.
- `React`: `validKey` → emoji khác rỗng thì `ValidateEmoji` → `target(ReactMessage)` → tin `Deleted` và emoji khác rỗng → `ErrMessageDeleted` (gỡ vẫn được) → emoji chưa có trong `msg.Reactions.Counts` và `len ≥ MaxEmojis` → `ErrTooManyEmojis` → `Set` (hoặc `Remove` khi rỗng) với `At = now()`. Không đổi → trả `{doc.N, msg.Reactions}`, không event, không touch. Đổi → enqueue `pbconv.ReactionChanged` → `Counter.Touch(key, msg.Reactions, []Witness{{User, doc.N}}, FastTouchTries)` (lỗi bỏ qua, giữ `msg.Reactions`) → nếu bump thì enqueue `pbconv.CountsChanged(room.Type, msg với summary mới, now())` → trả `{doc.N, summary}`. Lỗi enqueue luôn bỏ qua.
- `Pin`/`Unpin`: `validKey` → `target(PinMessage|UnpinMessage)` → `Pin` trên tin `Deleted` → `ErrMessageDeleted` → tối đa 3 lượt: `Projector.Current` → đúng trạng thái sẵn (`Pinned`) → trả state, không fact, không event → `Pin` và `len(Pins) ≥ PinLimit` → `ErrTooManyPins` → `Pins.Append(PinAction{PV: state.Version+1, Op, Thread, Seq, By: user, Tenant, At: now()})` → `ErrPinExists`: `At(room, pv)` cùng `Op/Thread/Seq/By` → coi là kết quả, khác → lượt sau. Hết lượt → `domain.ErrRetryLater`. Sau append: `Projector.Project(room, pv)` (lỗi → dùng `FoldPins(state, [fact])`) → enqueue `pbconv.PinChanged(room.Type, msg, fact)` → trả state.

### `apps/core/internal/pbconv` + proto + publish (Task 3)

```go
const ReactionsCounter = "reactions"

func ReactionEventID(room, thread, seq uint64, user string, change uint32) string
func ReactionCountsEventID(room, thread, seq, version uint64) string
func PinEventID(room, pv uint64) string
func ReactionSummary(s domain.ReactionSummary) *chatimv1.ReactionSummary
func ReactionChanged(roomType domain.RoomType, r domain.Reaction) *chatimv1.Event
func CountsChanged(roomType domain.RoomType, m domain.Message, at time.Time) *chatimv1.Event
func MessagePinned(roomType domain.RoomType, m domain.Message, a domain.PinAction) *chatimv1.Event
func MessageUnpinned(roomType domain.RoomType, m domain.Message, a domain.PinAction) *chatimv1.Event
func PinChanged(roomType domain.RoomType, m domain.Message, a domain.PinAction) *chatimv1.Event
func Pins(pins []domain.Pin) []*chatimv1.Pin
```

- Id: `ReactionEventID` = `MessageEventID(room, thread, seq) + "-" + user + "-n" + change`; `ReactionCountsEventID` = `MessageEventID(...) + "-reactions-v" + version`; `PinEventID` = `RoomID(room) + "-p" + pv`.
- `ReactionSummary`: `Version == 0` và không có count → `nil`. `Message(m)` đặt `Reactions: ReactionSummary(m.Reactions)`.
- `ReactionChanged`: id theo `r`, `Tenant r.Tenant`, `ThreadRoot/Seq` của tin, `Actor r.User`, `Ts r.At`, payload `{user, emoji, previous_emoji: r.Prev, change: r.N}`. `CountsChanged`: id theo `m.Reactions.Version`, `Tenant m.Tenant`, `Actor ""`, `Ts at`, payload `{counter: ReactionsCounter, reactions}`. Pin: id `PinEventID(a.Room, a.PV)`, `ThreadRoot a.Thread`, `Seq a.Seq`, `Actor a.By`, `Ts a.At`, payload `{message: Message(m), pin_version: a.PV}`; `PinChanged` chọn theo `a.Op` và xoá `Text` khi `m.Deleted`.
- `core.proto`: `Message` thêm `ReactionSummary reactions = 14;`. RPC và message mới:

```proto
rpc ReactMessage(ReactMessageRequest) returns (ReactMessageResponse);
rpc PinMessage(PinMessageRequest) returns (PinMessageResponse);
rpc UnpinMessage(UnpinMessageRequest) returns (UnpinMessageResponse);

message ReactionCount { string emoji = 1; uint32 count = 2; }
message ReactionSummary { repeated ReactionCount counts = 1; uint64 version = 2; }
message Pin { uint64 thread_root = 1; uint64 seq = 2; string by = 3; google.protobuf.Timestamp pinned_at = 4; uint64 pin_version = 5; }
message ReactMessageRequest { string room_id = 1; uint64 thread_root = 2; uint64 seq = 3; string emoji = 4; }
message ReactMessageResponse { uint32 change = 1; ReactionSummary reactions = 2; }
message PinMessageRequest { string room_id = 1; uint64 thread_root = 2; uint64 seq = 3; }
message PinMessageResponse { uint64 pin_version = 1; repeated Pin pins = 2; }
message UnpinMessageRequest { string room_id = 1; uint64 thread_root = 2; uint64 seq = 3; }
message UnpinMessageResponse { uint64 pin_version = 1; repeated Pin pins = 2; }
```

- `events.proto` oneof: `ReactionChanged reaction_changed = 24; CountsChanged counts_changed = 25; MessagePinned message_pinned = 26; MessageUnpinned message_unpinned = 27;` với `message ReactionChanged { string user = 1; string emoji = 2; string previous_emoji = 3; uint32 change = 4; }`, `message CountsChanged { string counter = 1; ReactionSummary reactions = 2; }`, `message MessagePinned { Message message = 1; uint64 pin_version = 2; }`, `message MessageUnpinned { Message message = 1; uint64 pin_version = 2; }`.
- `publish`: hằng `reactionChanged = "reaction_changed"`, `countsChanged = "counts_changed"`, `msgPinned = "msg_pinned"`, `msgUnpinned = "msg_unpinned"` (`stream.go`), thêm vào `eventKind`; `markKey` **không** mark bốn loại này. `fakeCore` của route đã nhúng `chatimv1.CoreServiceClient`, không cần sửa ở Task 3.

### `apps/core/internal/grpcsrv` + `view` (Task 11, 12)

- File mới `grpcsrv/react_pin.go`: `ReactMessage`, `PinMessage`, `UnpinMessage` dùng `callerAndRoom` rồi `s.mutator.React/Pin/Unpin`; response `{Change, Reactions: pbconv.ReactionSummary(...)}` và `{PinVersion: state.Version, Pins: pbconv.Pins(state.Pins)}`. **`grpcsrv.Deps` không thêm field.**
- `view.MaskDeleted` và `view.HideForViewer` đặt `Reactions = domain.ReactionSummary{}` cùng lúc xoá `Text` (D85: placeholder không nội dung). `GetHistory` không đổi luồng; `pbconv.Message` đã mang số đếm.
- `apps/core/service_wiring.go` (cuối Task 11): `wireService(st *mongostore.Store, router *actor.Router, pub *publish.Publisher, lockedKinds []domain.Kind, limits mutate.Limits, log *slog.Logger)`; dựng `counter.New(st, st.Reactions())`, `pinproj.New(st.Pins(), st)`, `mutate.Deps{Reactions: st.Reactions(), Pins: st.Pins(), …}`; `wiring.go` truyền `cfg.Limits`.

### `apps/core/internal/effects` (Task 13)

```go
const (
	ReactionEventName   = "reaction_event"
	ReactionCounterName = "reaction_counter"
	PinProjectionName   = "pin_projection"
	PinEventName        = "pin_event"
	DefaultCountDelay   = time.Second
	DefaultCounterTries = 3
)

type ReactionReader interface{ Get(ctx context.Context, key store.MsgKey, user string) (domain.Reaction, bool, error) }
type CounterToucher interface {
	Touch(ctx context.Context, key store.MsgKey, cur domain.ReactionSummary, witnesses []store.Witness, tries int) (domain.ReactionSummary, bool, error)
}
type PinFacts interface{ At(ctx context.Context, room, pv uint64) (domain.PinAction, error) }
type PinProjecter interface{ Project(ctx context.Context, room, target uint64) (domain.PinState, error) }

type ReactionEventDeps struct{ Reactions ReactionReader; Rooms RoomReader; JS publish.JetStream }
type ReactionCounterDeps struct{ Messages MessageFinder; Counter CounterToucher; Rooms RoomReader; JS publish.JetStream; Now func() time.Time }
type ReactionCounterConfig struct{ SubjectRoot string; Delay time.Duration; RoomCache, Tries int }
type PinEventDeps struct{ Pins PinFacts; Messages MessageFinder; Rooms RoomReader; JS publish.JetStream }

func NewReactionEvent(deps ReactionEventDeps, cfg MessageChangedConfig) (*ReactionEvent, error)
func NewReactionCounter(deps ReactionCounterDeps, cfg ReactionCounterConfig) (*ReactionCounter, error)
func NewPinProjection(p PinProjecter) (*PinProjection, error)
func NewPinEvent(deps PinEventDeps, cfg MessageChangedConfig) (*PinEvent, error)
```

- Mỗi kiểu có `Effect() Effect` và `Dropped() uint64`; `ReactionEvent`, `ReactionCounter`, `PinEvent` có `Republished() uint64` (chỉ PubAck không trùng, `countStored`); `ReactionCounter` có `Repaired() uint64`. `MessageChangedConfig` dùng lại cho hai effect event (SubjectRoot, Delay, RoomCache).
- `reaction_event`: mỗi record `Get(key, rec.User)`; không có → dropped; `doc.N > rec.Version` → nil (bỏ); `<` → `store.ErrStaleRead` (Nak); `==` → publish `ReactionChanged`.
- `reaction_counter`: gom record theo tin; witness = `{User, max N}` của mỗi user; `Find` theo room; `Touch(…, cfg.Tries)`; bump → `Repaired()++`; `Version > 0` → publish `CountsChanged(type, msg với summary, Now())`. Tin hoặc room không còn → dropped; lỗi khác → lỗi cho mọi record của tin.
- `pin_projection` (delay 0): mỗi room `Project(room, max PV của room trong lô)`; `domain.ErrRoomNotFound` → dropped; lỗi khác → lỗi cho mọi record của room.
- `pin_event`: `At(room, pv)` → `Find` tin → loại room → `PinChanged` → publish; fact/tin/room không còn → dropped.
- Registry cuối: `store.ReactionChanged: {activity, reactionCounter, reactionEvent}`, `store.PinInserted: {activity, pinProjection, pinEvent}` (delay tăng dần: 0 → `REACTION_COUNT_DELAY` → `RECONCILE_DELAY`; 0 → 0 → `RECONCILE_DELAY`).
- Metrics: `effectSet.counters()` thêm `reaction_event`, `reaction_counter`, `pin_event` (republished + dropped) và `pin_projection` (chỉ dropped). `probes` thêm `counterRepairs map[string]func() uint64` (`{pbconv.ReactionsCounter: fx.reactionCounter.Repaired}`) → `counter_repaired_total{counter}` (help: "Counter summaries the effect workers rewrote because the fast path left them stale."). Luật mới trong `deploy/prometheus/alerts.yml`: `alert: ChatimCounterRepairSurge`, `expr: sum by (counter) (rate(chatim_core_counter_repaired_total[5m])) > 1`, `for: 10m`, `severity: warning` → 16 luật.

### `apps/core/internal/config` (Task 11)

| Env | Field | Mặc định | Kiểm |
|---|---|---|---|
| `REACTION_MAX_EMOJIS` | `Config.Limits.MaxEmojis` | 20 | 1..100 (`mutate.Limits.Validate`) |
| `PIN_LIMIT` | `Config.Limits.PinLimit` | 50 | 1..1000 (`mutate.Limits.Validate`) |
| `REACTION_COUNT_DELAY` | `Config.ReactionCountDelay` | `effects.DefaultCountDelay` (1s) | `> 0` và `≤ RECONCILE_DELAY` (giữ delay tăng dần trong registry và vừa `AckWait` của work consumer = `RECONCILE_DELAY + 30s`) |

- `Config` thêm `Limits mutate.Limits`, `ReactionCountDelay time.Duration`; `componentErrors` thêm `{"REACTION_MAX_EMOJIS, PIN_LIMIT", c.Limits.Validate()}`; `validate` thêm rule `REACTION_COUNT_DELAY must be positive and at most RECONCILE_DELAY`. README (bảng env) cập nhật trong Task 11.

### `apps/core/internal/resync` (Task 14)

```go
type Reactions interface{ Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Reaction, error) }
type Pins interface{ Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.PinAction, error) }

var (
	ErrReactionPageFull = errors.New("resync: one instant holds more reactions than a reaction page")
	ErrPinPageFull      = errors.New("resync: one instant holds more pin actions than a pin page")
)
```

- `Deps` thêm `Reactions Reactions`, `Pins Pins`; `Report` thêm `ReactionRecords, PinRecords int`; `String()` = `resync rooms=%d room_records=%d message_records=%d edit_records=%d reaction_records=%d pin_records=%d dry_run=%t`. Mỗi room: timeline → edits → reactions → pins, phân trang như `emitEdits` (trang `store.MaxReactionScan`/`store.MaxPinScan`, bỏ trùng mép trang theo id record). Record: `{ReactionChanged, Room, Thread, Seq, Version: r.N, User: r.User, CommittedAt: r.At}` (kể cả tombstone, doc hiện tại thắng) và `{PinInserted, Room, Seq: a.PV, CommittedAt: a.At}`. `resync_command.go` truyền `Reactions: st.Reactions(), Pins: st.Pins()`.

### Route + corecli (Task 15)

- `tools/internal/route/reactions_pins.go`: `func (c *Client) ReactMessage(ctx, *chatimv1.ReactMessageRequest) (*chatimv1.ReactMessageResponse, Stats, error)`, `PinMessage`, `UnpinMessage` cùng dạng `EditMessage` (định tuyến theo room, retry giữ nguyên request). `fakeCore` cài ba method này.
- `corecli`: lệnh `react` (`-room -seq -emoji`, emoji rỗng = gỡ), `pin`, `unpin` (`-room -seq`); e2e thêm pha react + pin (Part C chốt chi tiết), `e2e.EventOf` nhận bốn payload mới.

## Thứ tự task

| # | Task | Rủi ro | Phụ thuộc | Phần |
|---|---|---|---|---|
| 1 | Baseline: đồng bộ `feat/m2b`, quy tắc commit, `fmt-check vet lint test` xanh | — | — | A |
| 2 | `domain` (Reaction/Summary/Pin/FoldPins/ValidateEmoji, lỗi, `Message.Reactions` + sửa `==`) + `pkg/keys` (`Reaction`, `Pin`) | thấp | 1 | A |
| 3 | Proto (field 14, 3 RPC, oneof 24–27) + `make proto` + pbconv (id, event, summary, pins) + 4 kind của publish | thấp | 2 | A |
| 4 | Store port + ChangeKind/Change + memstore (`Reactions`, `Pins`, `SetReactions`, `PinState/ApplyPins`) + storetest `RunReactions/RunPins` + write contract | trung bình | 2 | A |
| 5 | ★ mongostore: 2 collection clustered + index, codec `rx/pins/pv`, `Set/Remove` (pipeline, trùng khoá ≤3), `Count` (witness + causal session), pin ports | **cao** | 4 | A |
| 6 | ★ Feed: `$match` mới, giải mã update/replace, memstore `FeedOption`, `RunReactionFeed/RunPinFeed`; reader chưa forward kind mới (drop có đếm tới Task 7) | **cao** | 4, 5 | A |
| 7 | ★ `work.Record` (đuôi user, id `x:`/`p:`, `KnownKind`) + `Nak` kind lạ (`NewQueue` thêm retry delay) + sửa `room_activity` Seq + đăng ký tạm activity cho 2 kind; reader forward. **Push** | **cao** | 3, 6 | A |
| 8 | Package `counter` (touch) + `pinproj` (Current/Project) với test trên memstore | trung bình | 4 | A |
| 9 | ★ `access` (3 action) + `mutate.React` + Deps (`Reactions`, `Counter`, `Limits`) + wiring tạm | **cao** | 3, 4, 8 | B |
| 10 | ★ `mutate.Pin`/`Unpin` + Deps (`Pins`, `Projector`) | **cao** | 9 | B |
| 11 | ★ Config (3 env) + `grpcsrv/react_pin.go` + `wireService(…, limits, …)` + README env | **cao** | 5, 9, 10 | B |
| 12 | `view` che `Reactions` + test `GetHistory` trả số đếm | trung bình | 3, 11 | B |
| 13 | ★ 4 effect + registry + metrics + `counter_repaired_total` + luật 16. **Push** | **cao** | 5, 7, 8, 11 | B |
| 14 | Resync `reactions` + `pin_actions` | trung bình | 5, 7 | C |
| 15 | Route + corecli + e2e react/ghim | thấp | 11 | C |
| 16 | Itest: event update thật từ Mongo; kill core giữa fact và projection ghim; counter hội tụ khi react đồng thời; diễn tập resync có reaction/pin | trung bình | 11–14 | C |
| 17 | Docs: thiết kế (§4, §5, §5.1, §6.3, §6.4, §7, §8.3, §12, P7, D88–D94), roadmap, CLAUDE.md, INDEXES, "Kết quả thực thi" | thấp | tất cả | C |
| 18 | Kiểm chứng cuối (`fmt-check vet lint vuln test itest`, image + `core-up` + e2e, `/metrics`, `alerts-check` 16 luật, resync dry-run, corebench ngắn). **Push** | — | tất cả | C |

Ghi chú phụ thuộc:
- Task 7 sửa `room_activity` **trước** khi bất kỳ record reaction/ghim nào tới được worker; Task 6 không bật forward (reader còn `KnownKind` cũ, change mới bị drop có đếm `reconcile_dropped_total`, chỉ dev).
- Task 9 và 10 làm `mutate.New` chặt hơn, nên chính task đó sửa `service_wiring.go` (truyền `st`, `counter`, `pinproj`, `Limits{}`); Task 11 mới đổi chữ ký `wireService` để nhận `cfg.Limits`.
- Task 13 cần `Config.ReactionCountDelay` (Task 11) và `counter`/`pinproj` (Task 8).
- Task 12 và 13 khác file, có thể review Task 12 trong khi làm Task 13.

---

## Ghi chú tích hợp (controller, khi ghép 3 phần)

- **Commit có pathspec.** `git add` đúng file mới, `git commit -m "..." -- <paths>`; cấm `add -A`, `commit -a`, `stash`. `git show --stat HEAD` chỉ liệt kê file của task. File chung đang có thay đổi chưa commit của người khác (`INDEXES.csv`, `README.md`, `CLAUDE.md`, docs) → **dừng, báo controller**. Sau mỗi lần sửa CSV: `python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"` → `{7}`.
- **Tên phải giống hệt giữa các phần:** `store.ReactionChanged`/`PinInserted` (4/5), `store.Witness`, `store.ErrStaleRead`, `ReactionSummaries.SetReactions`, `PinProjector.PinState/ApplyPins`, `counter.Toucher.Touch`, `pinproj.Projector.Current/Project`, `mutate.ReactCmd/PinCmd/ReactResult/Limits`, `pbconv.ReactionsCounter`, tên effect và hằng ở trên. Part B/C không đổi chữ ký của Part A; cần đổi thì báo controller để sửa hợp đồng.
- **Id event và record (không trùng nhau trong cửa sổ bỏ trùng của stream):**

  | Loại | Event id | Record id |
  |---|---|---|
  | tin mới | `{room}-{th}-{seq}` | `m:` + event id |
  | sửa/xoá | `{room}-{th}-{seq}-v{ver}` | `e:` + event id |
  | reaction | `{room}-{th}-{seq}-{u}-n{n}` | `x:` + event id |
  | số reaction | `{room}-{th}-{seq}-reactions-v{v}` | — (không có record) |
  | ghim | `{room}-p{pv}` | `p:` + event id |
  | room | `{room}-created` | `r:{room}` |

  User `[A-Za-z0-9_-]{1,64}` chứa được `-`, nhưng ba số đầu cố định và đuôi `-n\d+` khác `-v\d+`; Task 3 có test bảng chốt các dạng không trùng nhau (kể cả user `reactions`, `v1`, `a-n1`). Id dài nhất ~140 ký tự (hợp lệ cho `Nats-Msg-Id`).
- **Thứ tự effect trong registry** (Task 13 viết, Task 7 viết dòng tạm): `ReactionChanged → [room_activity, reaction_counter, reaction_event]`, `PinInserted → [room_activity, pin_projection, pin_event]`. Worker chạy nhóm theo kind tăng dần, mỗi effect chờ `max(CommittedAt) + delay`, nên delay phải tăng dần trong danh sách; rule `REACTION_COUNT_DELAY ≤ RECONCILE_DELAY` (Task 11) giữ điều đó.
- **Fast path không bao giờ báo lỗi vì effect:** lỗi enqueue, touch (`ErrStaleRead`, `ErrContended`) và `Project` bị bỏ qua sau khi fact đã commit; worker hội tụ về **trạng thái cuối** (event trung gian của reaction, ví dụ `n1` khi đã có `n2`, có thể mất hẳn nếu fast path rớt; lớp tập chỉ hứa doc hiện tại). Lỗi của chính fact (validate, quyền, `ErrTooManyEmojis/Pins`, `ErrMessageDeleted`, `ErrReactionContended`, `ErrRetryLater`) trả về client qua `pkg/grpcserver`.
- **Không ack mark** cho bốn event mới (`markKey` giữ nguyên); worker gửi lại, JetStream bỏ trùng theo id; `republished` chỉ đếm PubAck không trùng.
- **Tương thích record:** core cũ `Term` record 38+ byte và record kind 5. Rolling deploy phải nâng mọi core trước khi có record mới (prod chưa live; ghi trong D91). Record work 37 byte cũ vẫn chạy.
- **`domain.Message` không còn `==`** từ Task 2; task sau so tin bằng helper của storetest hoặc so field.
- **Ai sửa file chung (tuần tự, không hai task cùng lúc):**

  | File | Task |
  |---|---|
  | `apps/core/service_wiring.go` | 9 → 10 → 11 |
  | `apps/core/effects_wiring.go` | 7 → 13 |
  | `apps/core/wiring.go` | 11 → 13 |
  | `apps/core/metrics_wiring.go`, `deploy/prometheus/alerts.yml` | 13 |
  | `apps/core/resync_command.go` | 14 |
  | `apps/core/internal/mutate/fixtures_test.go` | 9 → 10 |
  | `apps/core/internal/store/write_contract_test.go` | 4 |
  | `apps/core/internal/store/mongostore/codec.go`, `bootstrap.go`, `mongostore.go` | 5 |
  | `apps/core/internal/store/mongostore/feed.go`, `feed_change.go`, `memstore/feed.go`, `memstore/change_log.go` | 6 |
  | `apps/core/internal/effects/room_activity.go`, `partition.go` | 7 |
  | `proto/chatim/v1/*.proto`, `pkg/pb/chatim/v1/*` | 3 |
  | `README.md` | 7 (`WORK_RETRY_DELAY`) → 11 (env) → 17 |

- **Dòng `INDEXES.csv` mỗi task sửa:** 2: `domain`, `pkg/keys`; 3: `proto/chatim/v1/core.proto`, `proto/chatim/v1/events.proto`, `pkg/pb/chatim/v1`, `pbconv`, `publish`; 4: `store`, `store/memstore`, `store/storetest`; 5: `store/mongostore`; 6: `store/mongostore`, `store/memstore`, `store/storetest`; 7: `work`, `effects`, `reconcile`, `apps/core`; 8: dòng mới `apps/core/internal/counter`, `apps/core/internal/pinproj`; 9: `access`, `mutate`, `apps/core`; 10: `mutate`; 11: `config`, `grpcsrv`, `apps/core`; 12: `view`, `grpcsrv`; 13: `effects`, `apps/core`, `deploy/prometheus/alerts.yml` (15 → 16 luật); 14: `resync`, `apps/core`; 15: `tools/internal/route`, `tools/corecli`, `tools/corecli/internal/e2e`; 17: dòng docs thiết kế, roadmap, plan này. Cột decisions thêm `D88`–`D94` đúng chỗ.
- **Khoảng trống tạm (chỉ dev):** Task 6 → 7: reader drop change mới. Task 7 → 13: worker chỉ chạy `room_activity` cho record reaction/ghim rồi ack; event và counter chỉ từ fast path (Task 9/10 trở đi).
- **Detector:** sau Task 13, `/metrics` có `effect_dropped_total{effect}` cho `reaction_event`, `reaction_counter`, `pin_projection`, `pin_event`, `reconcile_republished_total{effect}` cho ba effect event, `counter_repaired_total{counter="reactions"}`; `make alerts-check` báo 16 luật.

### Ghi chú cho controller (part A)

**Tinh chỉnh hợp đồng (đã được controller chốt giữa chừng):**

1. `*mongostore.Store` không cài `store.Reactions`/`store.Pins` (trùng `Get` của `Rooms`, `Append/At/Between` của `Edits`). Có kiểu `mongostore.Reactions`, `mongostore.Pins` và accessor `(*Store).Reactions()`, `(*Store).Pins()`. `*Store` vẫn cài `store.ReactionSummaries` và `store.PinProjector`. Part B/C: `counter.New(st, st.Reactions())`, `pinproj.New(st.Pins(), st)`, `mutate.Deps{Reactions: st.Reactions(), Pins: st.Pins()}`, `effects.ReactionEventDeps{Reactions: st.Reactions()}`, `effects.PinEventDeps{Pins: st.Pins()}`, `resync.Deps{Reactions: st.Reactions(), Pins: st.Pins()}`; itest contract mở `(s, s.Reactions())`, `(s, s.Pins())`.

**Thêm ngoài hợp đồng (Part B/C được dùng):**
- `store.ValidateReactionTarget(key MsgKey, user string) error` (khoá + `ValidUser`), `store.ValidateVersionBump(base, next uint64) error` (`next > base`, `≤ MaxInt64`).
- `memstore.FeedOption` (kiểu của `WithReactions`/`WithPins`).
- `work.ErrUnknownKind` đúng hợp đồng; thêm quyết định: **kind 0 là `ErrBadRecord` (Term), không phải kind lạ (Nak)**.
- `mongostore`: `editIndexes()` đổi tên `roomTimeIndexes()` (dùng cho `message_edits`, `reactions`, `pin_actions`); `findOne` nhận thêm `opts ...options.Lister[options.FindOneOptions]`; `Rooms.Get` có projection loại `pins`, `pv`.
- `work/record_codec.go` tách `Encode`/`Decode`/`unixNano` khỏi `record.go`.

**Quyết định trong Part A:**
- `domain.FoldPins`: mọi fact `PV > s.Version` đều tiến `Version`, kể cả fact không đổi danh sách (ghim tin đã ghim, bỏ ghim tin chưa ghim, op lạ). Nếu không, `rooms.pv` kẹt và `Project` báo `ErrStaleRead` mãi.
- `Set` của mọi adapter kiểm `domain.ValidateEmoji` (bao "emoji khác rỗng"), nên store từ chối emoji > 32 byte hay có ký tự điều khiển dù `mutate` đã kiểm.
- Mongo pipeline bọc `t`, `u`, `e` bằng `$literal` (emoji `$e` không thành đường dẫn field). Unit test + itest layout chốt điều đó.
- `Reactions.Set` Mongo: lần đọc lại sau trùng khoá là majority; nếu doc của thiết bị kia chưa majority-commit thì lượt sau ghi lại, tối đa 3 lượt rồi `ErrReactionContended` (hiếm, chỉ khi cùng user đổi đồng thời).
- `memstore.Messages.Insert` bỏ `Reactions` của tin vào (như `Hidden`), cho khớp Mongo (`Insert` không ghi `rx`); có case contract.
- `pinproj.fold` dừng cả khi fold không tiến (adapter trả trang đầy nhưng toàn fact cũ), tránh lặp vô hạn.
- Task 2 sửa thêm `apps/core/internal/view/masks_test.go` và `pipeline_test.go` (`slices.Equal` trên `[]domain.Message`), ngoài danh sách của hợp đồng; Step 5 chạy `make vet` + `make test` toàn repo để bắt chỗ còn sót.

**Cam kết cho Part B/C (đã kiểm trong code/test của Part A):**
1. `Reactions.Set`/`Remove` (memstore, Mongo) trả doc đầy đủ (`Room, Thread, Seq, Tenant, User, Emoji, Prev, N, At`); `Remove` không có doc → `(Reaction{}, false, nil)`; tombstone → `(tombstone, false, nil)`. Case contract `reactRemove`, `reactSetOther` so toàn bộ doc.
2. `Count` trả `ErrStaleRead` (bọc `apperr.ErrUnavailable`) khi witness thiếu hoặc `N` thấp hơn; trả slice `len 0` khi không còn reaction (memstore non-nil, Mongo non-nil). Case `reactWitness`, `reactCount`.
3. `PinState` trả `domain.ErrRoomNotFound` (memstore, Mongo) cho room không có. Case `pinStateEmpty`.
4. `pinproj.Current/Project` bọc lỗi store bằng `%w`; test `TestMissingRoomAndMissingDeps` kiểm `errors.Is(err, domain.ErrRoomNotFound)`.
5. memstore `Reactions.Set` và `Pins.Append` không kiểm tin tồn tại hay pv dày; `mutate` lo phần đó. Mongo cũng vậy.
6. Envelope của bốn event mới mang `RoomId` và `Seq` (reaction/counts: seq của tin; ghim: seq tin đích; `ThreadRoot` tương ứng), `Tenant` từ reaction/fact/tin.

**Thứ tự / khoảng trống tạm (dev):**
- Task 4 thêm case `ReactionChanged`/`PinInserted` (chỉ khoá) vào `work.RecordOf` vì lint `exhaustive`; Task 7 thêm `User`. `KnownKind` chỉ nhận hai kind mới từ Task 7.
- Task 6 → 7: feed phát change mới, reader drop có đếm (`reconcile_dropped_total`).
- Task 7 → 13: worker chỉ chạy `room_activity` (seq 0) cho record reaction/ghim rồi ack.
- Deploy dev: core cũ Term record 38+ byte và kind 4/5; ghi vào D91 (Task 17).

**File gần 200 dòng (kiểm `wc -l` ở task tương ứng):**
- `apps/core/internal/store/mongostore/codec_test.go` (~192 sau Task 2), `codec.go` (~187 sau Task 5).
- `apps/core/internal/counter/counter_test.go` (~185).
- `apps/core/internal/reconcile/harness_test.go` (~172 sau Task 7).
- `apps/core/internal/store/mongostore/bootstrap_integration_test.go` (188, không đổi).
- `apps/core/internal/work/nats_integration_test.go` (169, test mới để file riêng).

**Câu hỏi chưa giải quyết:**
- Explain của aggregate đếm reaction (Task 5) có thể khác giữa engine classic và SBE; test chỉ đòi `k_1_e_1` và không `FETCH`. Nếu bản Mongo của dev đưa `GROUP` vào cây theo cách `walkWinning` không duyệt tới, implementer báo lại kèm explain thô.
- Đọc lại sau trùng khoá trong `Reactions.Set` dùng majority; có nên dùng primary-local cho riêng lần đọc này (giảm `ErrReactionContended` khi cùng user đổi đồng thời) không? Hiện giữ majority cho đơn giản.

### Ghi chú cho controller (part B)

**Khoảng trống hợp đồng:**
- **G1 (đã chỉnh trong hợp đồng):** `*mongostore.Store` không cài trực tiếp `store.Reactions`/`store.Pins` (trùng tên `Get`, `At`, `Append`, `Between`). Part A thêm `(*Store).Reactions() *mongostore.Reactions` và `(*Store).Pins() *mongostore.Pins`; part B dùng chúng ở `apps/core/service_wiring.go` (Task 9–11) và `apps/core/reaction_pin_effects_wiring.go` (Task 13). Part C (`resync_command.go`) cũng phải truyền `Reactions: st.Reactions(), Pins: st.Pins()` thay cho `st`.
- **G2 (thứ tự):** `effects.DefaultCountDelay` thuộc khối hằng Task 13 nhưng config (Task 11) cần trước; Task 11 tạo `effects/reaction_counter.go` chỉ chứa hằng này, Task 13 thay cả file (API không đổi).

**Quyết định của part B (trong hợp đồng chưa ghi):**
- `mutate.Limits.Validate()` = `withDefaults().validate()` (mẫu `Config.Validate` của component): `Limits{}` hợp lệ; `mutate.New` lưu bản đã điền mặc định. Âm hoặc vượt trần → `ErrInvalidArgument`.
- `mutate` nội bộ: `pinTries = 3`; `At` của fact ghim lấy một lần trước vòng lặp; khi `ErrPinExists` mà `At(room, pv)` lỗi (kể cả `ErrPinNotFound`) thì trả lỗi đó, không thử lượt sau.
- `React` kiểm giới hạn emoji và "tin đã xoá" chỉ khi emoji khác rỗng (gỡ luôn được). Lệnh no-op trả `msg.Reactions` đọc lúc `target`, không touch.
- Harness `grpcsrv` thêm `memStores()` (dùng chung cho `newRig` và `TestNewRequiresEveryDependency`) và field `reactions`, `pins`; Task 9/10 sửa `harness_test.go`, `fake_dependencies_test.go`, `caller_identity_test.go` (không có trong bảng file chung vì chỉ part B đụng).
- Rig `mutate`: `rg.deps(t, policy)` + `rg.build(t, d)`; `rg.mutator(t, policy, edits)` giữ chữ ký cũ cho `edit_test.go`.
- `effects`: `eventPublisher` (nhúng vào `ReactionEvent`, `ReactionCounter`, `PinEvent`, cấp `Republished`/`Dropped`) và `groupRecords` generic; `msg_changed`, `msg_created`, `room_created`, `edit_projection` không đổi. `reaction_counter` lấy loại room trước khi touch (room không còn → bỏ, không ghi `rx`). `Dropped` của effect gom nhóm cộng theo record. `pin_projection` bỏ khi `undeliverable` (room không có hoặc `ErrInvalidArgument`), hợp đồng chỉ nêu `ErrRoomNotFound`.
- `reaction_counter` không có env cho `Tries` (mặc định `DefaultCounterTries` 3, wiring không truyền).
- Wiring 4 effect mới tách `apps/core/reaction_pin_effects_wiring.go`; `counter.New`/`pinproj.New` dựng hai lần (service + effects), không giữ trạng thái.
- `apps/core/effects_wiring_test.go` mới chốt mọi effect có metric dropped và chỉ effect phát event có republished.

**Giả định về part A (kiểm khi ráp; sai thì chỉ sửa dòng dựng rig/wiring):**
- memstore: `NewReactions() *Reactions` cài `store.Reactions`, `NewPins() *Pins` cài `store.Pins`; `*memstore.Messages` có `SetReactions` và `Find` trả `Reactions`; `*memstore.Rooms` có `PinState`/`ApplyPins` (room chưa ghim → `PinState{}`, không có room → `ErrRoomNotFound`). `Reactions.Set/Remove` trả doc đầy đủ (`Tenant`, `At`, `Prev`, `N`); `Count` báo `ErrStaleRead` khi witness không có doc hoặc `n < N`, trả slice rỗng khi không còn emoji.
- `counter.Toucher.Touch` trả `(cur, false, nil)` khi số đếm bằng (kể cả `nil` với slice rỗng, vì `slices.Equal`), `(mới, true, nil)` khi CAS khớp. `pinproj.Projector.Project` trả lỗi của `PinState` (bọc `%w`) khi room không có.
- Task 2 đã sửa các test `view` so `[]domain.Message` bằng `slices.Equal` (Task 12 chỉ thêm file test mới).
- `work.Record.User`, `work.NewQueue(js, stream, partition, retryDelay)`; `store.ReactionChanged`/`PinInserted`; record `PinInserted` có `Seq = pv`.
- `publish.Message` biết bốn payload mới với subject `{root}.{t}.room.{rid}.reaction_changed|counts_changed|msg_pinned|msg_unpinned`.

**Part C cần biết:**
- Task 14 resync: record `ReactionChanged` sẽ chạy `room_activity` → `reaction_counter` (touch với witness `n` của doc hiện tại) → `reaction_event` (doc hiện tại thắng: record `n` cũ hơn doc bị bỏ qua, không lỗi); record `PinInserted` chạy `pin_projection` + `pin_event`. `resync_command.go` dùng `st.Reactions()`/`st.Pins()` (G1).
- Task 15 route/corecli: mã lỗi cần phân biệt: `FailedPrecondition` (tin xoá, quá giới hạn emoji/ghim), `Unavailable` (`ErrReactionContended`, ghim hết lượt → retry), `PermissionDenied`, `NotFound`. `ReactMessageResponse.reactions` là `nil` khi chưa từng có số đếm.
- Task 16 itest gợi ý: (a) xoá `rx` thẳng trên Mongo rồi chèn một reaction mới → worker `reaction_counter` sửa lại, `counter_repaired_total{counter="reactions"}` tăng; (b) chèn fact `pin_actions` thẳng (bỏ qua core) → `pin_projection` ghi `rooms.pins/pv`, `pin_event` phát `msg_pinned` id `{room}-p{pv}`; (c) fast path bị từ chối enqueue → `reaction_changed` vẫn tới sau `RECONCILE_DELAY`, `reconcile_republished_total{effect="reaction_event"}` tăng 1.
- Task 17 docs: D90 ghi touch worker một lần/tin/lô với witness n lớn nhất mỗi user; D92 ghi `pinTries = 3`, fallback `FoldPins` khi `Project` lỗi; D93 ghi `Republished` của ba effect event chỉ đếm id stream chưa có; detector CT1 = `counter_repaired_total` + `ChatimCounterRepairSurge` (16 luật).

### Ghi chú cho controller (part C)

**GAP-1 đã chốt (controller):** `*mongostore.Store` không cài thẳng `store.Reactions`/`store.Pins` (trùng tên `Append/At/Between/Get` của `Edits`/`Rooms`); Task 5 thêm kiểu con `mongostore.Reactions`/`mongostore.Pins` qua accessor `st.Reactions()`/`st.Pins()`. Part C dùng accessor ở `resync_command.go` (`Reactions: st.Reactions(), Pins: st.Pins()`) và helper itest `itReactions`, `itPins` trong `apps/core/it_reactions_pins_test.go`. `SetReactions`, `PinState`, `ApplyPins` vẫn gọi trên `st`.

**Chữ ký mới/chốt thêm (không đổi hợp đồng chung):**

```go
package resync

type timeScan[T any] struct {
	name    string
	limit   int
	full    error
	between func(ctx context.Context, room uint64, from, to time.Time, limit int) ([]T, error)
	record  func(T) work.Record
	counted *int
}

func scanByTime[T any](ctx context.Context, s *scanner, room uint64, ts timeScan[T]) error

type Deps struct {
	Rooms     Rooms
	Pages     Pages
	Edits     Edits
	Reactions Reactions
	Pins      Pins
	Pub       Publisher
}

type Report struct {
	Rooms, RoomRecords, MessageRecords, EditRecords, ReactionRecords, PinRecords int
	DryRun                                                                       bool
}
```

- `resync`: `editPage`, `emitEdits` bỏ (thay bằng `scanByTime`); `Report.String()` đổi chuỗi in ra của `/app resync` (thêm `reaction_records`, `pin_records`); `edits_test.go` và itest drill cập nhật theo.
- `route`: `Client.ReactMessage/PinMessage/UnpinMessage(ctx, *Req) (*Resp, Stats, error)`, cả ba `retryIdempotent` qua `inRoom`.
- `e2e`: `Event.User`, `Event.IsCreated`, `Event.IsMark`; `State.Reactions []Reaction`, `State.Pins []Pin`; mới `Reaction`, `Pin`, `ReactionEventID`, `CountsEventID`, `PinEventID`, `FormatCounts`, `CheckReactReply`, `CheckPinReply`, `CheckReactions`, `CheckMarkEvents`, `KindReaction/KindCounts/KindPinned/KindUnpinned`. `CheckEvents` giờ bỏ qua mọi event không phải `msg_created` (trước: chỉ bỏ `msg_edited/msg_deleted`), cùng hành vi với dữ liệu cũ.
- corecli: lệnh `react`, `pin`, `unpin`; bước `e2e react-pin`; `scripts/e2e.sh` phase 4 (dòng PASS dài thêm).
- itest: `retryingUnavailable[T]` (tổng quát `sendRetrying`, cùng hằng và hành vi), `createRoomWith`, `awaitLiveEvents`, `awaitStored[T]`, `awaitCounts`, `countsOf`, `sameCounts`, `pinnedSeqs`.

**Giả định về part A/B (cần khớp, sai thì chỉ đổi tên):**
- memstore: `NewReactions() *Reactions` (`Set`, `Remove`, `Between`; `Set` không kiểm tin tồn tại), `NewPins() *Pins` (`Append` không kiểm pv dày hay fold). `Between` của cả hai sắp theo `ts` rồi `_id`, `[from, to]` bao gồm hai đầu, `limit` 1..1000.
- `work.Record{Kind: store.ReactionChanged, …, Version: n, User: u}.ID()` = `"x:" + pbconv.ReactionEventID(...)`; `PinInserted` dùng `Seq = pv`; `ID()` không phụ thuộc `CommittedAt`.
- pbconv: envelope của bốn event mới có `RoomId` và `Seq` (= seq tin), `Actor` = người làm (`a.By` cho ghim); `ReactionSummary` trả `nil` khi `Version == 0` và không có số đếm.
- Registry Task 13: `ReactionChanged → [room_activity, reaction_counter, reaction_event]`, `PinInserted → [room_activity, pin_projection, pin_event]`; itest (a) và drill dựa vào việc `counts_changed` của record n là `v = n` khi các write cách nhau đủ lâu.
- `mutate`: react no-op trả `change` = `n` hiện tại, response `reactions` = summary sau touch inline; `Pin` trên tin xoá trả `ErrMessageDeleted` **trước** khi kiểm "đã ghim"; `Unpin` trên tin xoá được.
- Config `PIN_LIMIT` đọc từ env (itest (d) đặt `3`); `MESSAGE_LOCKED_KINDS=text` khoá sửa của tác giả (D87).

**Rủi ro / việc controller cần quyết:**
1. GAP-1 ở trên.
2. e2e chỉ đòi id event **cuối** (`n` cuối, `v` cuối, `p` của ghim): event trung gian `n1`/`v1` chỉ có từ fast path, worker không phát lại (doc hiện tại thắng). Nếu muốn e2e đòi cả `n1` thì phải chấp nhận e2e fail khi fast path lỡ một event — trái thiết kế lớp tập.
3. e2e kiểm response react **chính xác** (`change`, số đếm, `v`): nếu touch inline trượt (rất hiếm trên cụm dev, chỉ khi CAS tranh hoặc witness cũ) thì response mang summary cũ và e2e fail ở phase 4; chạy lại `make e2e`, ghi vào Minor nếu tái hiện.
4. Itest (c), (d) dùng goroutine và poll 100ms có hạn (như `awaitActivity`); `make itest` không nhận `ARGS` nên không chạy lặp riêng được (Task 16 ghi rõ).
5. `apps/core/send_retry_test.go` đổi trong Task 16 (thêm `retryingUnavailable`); không task nào khác của M2b.3 sửa file này.
6. File gần 200 dòng sau Part C: `resync/scan_test.go` ~187, `resync/scan.go` ~164; lần sửa sau phải tách (`scan_test.go` → tách `world` sang `world_test.go`).

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

Expected: `feat/m2b`; HEAD là commit plan M2b.3 (con của `b669138`). `git status --short` không có dòng nào thuộc `apps/`, `proto/`, `pkg/`, `tools/`, `deploy/` hoặc `INDEXES.csv`. Owner có thể đang có thay đổi doc riêng (`docs/...`, `CLAUDE.md`, `README.md`): không đụng, không stage, không stash, không revert.

Nếu `INDEXES.csv` có thay đổi chưa commit (` M INDEXES.csv` hoặc `M  INDEXES.csv`): **dừng và báo controller**. Mọi task dưới đây sửa `INDEXES.csv` và commit theo pathspec, nên sẽ cuốn luôn thay đổi của owner trong file đó.

**Quy tắc commit cho mọi task của M2b.3:**

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

### Task 2: `domain` (reaction, ghim, `FoldPins`, `ValidateEmoji`, lỗi) + `pkg/keys` (`Reaction`, `Pin`)

Đặt kiểu dữ liệu chung cho mọi task sau. `Reaction` là doc của lớp tập: một doc cho mỗi (tin, user), `Emoji == ""` là tombstone, `N` là change number, `Prev` là emoji trước lần đổi đó. `ReactionSummary` là aggregate gắn vào `Message.Reactions` (counts đã sắp + version). `PinAction` là fact bất biến của `pin_actions`; `FoldPins` là hàm thuần dựng projection từ state + fact, dùng chung cho fast path và worker (Task 8).

**`Version` của `FoldPins`:** mọi fact `PV > s.Version` đều "được áp", kể cả fact không đổi danh sách (ghim tin đã ghim, bỏ ghim tin chưa ghim): `Version` luôn tiến tới PV của fact cuối được xét. Nếu không, `rooms.pv` sẽ kẹt sau một fact no-op và `pinproj.Project` (Task 8) báo `ErrStaleRead` mãi.

**`domain.Message` không còn so sánh được bằng `==`** (`ReactionSummary.Counts` là slice). Đã grep toàn repo; các chỗ vỡ biên dịch và cách sửa (Step 5):

| File | Chỗ | Sửa |
|---|---|---|
| `apps/core/internal/store/storetest/fixtures.go` | `sameMessage`: `a == b` | so field bằng `reflect.DeepEqual` sau khi tách thời gian và `Reactions`; `Reactions` so bằng `Version` + `slices.Equal` |
| `apps/core/internal/store/storetest/feed_room_cases.go:42` | `c.Msg != (domain.Message{})` | `c.Msg.Seq != 0` |
| `apps/core/internal/store/storetest/feed_edit_cases.go:20` | `c.Msg != (domain.Message{})` | `c.Msg.Seq != 0` |
| `apps/core/internal/store/mongostore/codec_test.go:74` | `got != m` | `!reflect.DeepEqual(got, m)` |
| `apps/core/internal/store/mongostore/edit_codec_test.go:106` | `got != stored` | `!reflect.DeepEqual(got, stored)` |
| `apps/core/internal/view/masks_test.go:26,29,45,48` | `slices.Equal` trên `[]domain.Message` | `reflect.DeepEqual` |
| `apps/core/internal/view/pipeline_test.go:30` | `slices.Equal(page, before)` | `reflect.DeepEqual(page, before)` |

Hai file `view` nằm ngoài danh sách của hợp đồng nhưng cùng nguyên nhân ("sửa mọi chỗ `make vet` báo"). Nếu `make vet` báo thêm chỗ nào khác ngoài bảng này: sửa cùng cách (so field hoặc `reflect.DeepEqual`, không đổi ý nghĩa test) và ghi vào báo cáo.

**Files:**
- Modify: `pkg/keys/keys.go`
- Create: `pkg/keys/reaction_pin_test.go`
- Modify: `apps/core/internal/domain/message.go`, `apps/core/internal/domain/errors.go`, `apps/core/internal/domain/errors_test.go`
- Create: `apps/core/internal/domain/reaction.go`, `apps/core/internal/domain/reaction_test.go`
- Create: `apps/core/internal/domain/pin.go`, `apps/core/internal/domain/pin_test.go`
- Modify: `apps/core/internal/store/storetest/fixtures.go`, `apps/core/internal/store/storetest/feed_room_cases.go`, `apps/core/internal/store/storetest/feed_edit_cases.go`
- Modify: `apps/core/internal/store/mongostore/codec_test.go`, `apps/core/internal/store/mongostore/edit_codec_test.go`
- Modify: `apps/core/internal/view/masks_test.go`, `apps/core/internal/view/pipeline_test.go`
- Modify: `INDEXES.csv` (dòng `apps/core/internal/domain`, `pkg/keys`)

**Step 1: Test**

`pkg/keys/reaction_pin_test.go`:

```go
package keys

import (
	"bytes"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestReactionKeyIsTheMessageKeyPlusTheUser(t *testing.T) {
	b := Reaction(9, 3, 5, "alice")
	if len(b) != MsgLen+5 || !bytes.HasPrefix(b, Msg(9, 3, 5)) || string(b[MsgLen:]) != "alice" {
		t.Fatalf("Reaction = %x, want keys.Msg(9, 3, 5) followed by the user bytes", b)
	}
	for _, user := range []string{"a", "alice", strings.Repeat("Z", MaxReactionUser)} {
		room, thread, seq, got, err := ParseReaction(Reaction(9, 3, 5, user))
		if err != nil || room != 9 || thread != 3 || seq != 5 || got != user {
			t.Fatalf("ParseReaction(Reaction(9, 3, 5, %q)) = %d %d %d %q %v", user, room, thread, seq, got, err)
		}
	}
}

func TestReactionKeysOfAMessageStayInsideItsRange(t *testing.T) {
	lo, hi := Msg(9, 0, 5), Msg(9, 0, 6)
	for _, user := range []string{"-", "a", "alice", "zz", strings.Repeat("Z", MaxReactionUser)} {
		k := Reaction(9, 0, 5, user)
		if bytes.Compare(k, lo) <= 0 || bytes.Compare(k, hi) >= 0 {
			t.Fatalf("Reaction(9, 0, 5, %q) = %x is outside (%x, %x)", user, k, lo, hi)
		}
	}
}

func TestPinKeysRoundTripAndSortByVersion(t *testing.T) {
	b := Pin(9, 77)
	room, pv, err := ParsePin(b)
	if len(b) != PinLen || PinLen != 16 || err != nil || room != 9 || pv != 77 {
		t.Fatalf("ParsePin(Pin(9, 77)) = %d %d %v from %d bytes", room, pv, err, len(b))
	}
	if bytes.Compare(Pin(9, 1), Pin(9, 2)) >= 0 || bytes.Compare(Pin(9, math.MaxUint64), Pin(10, 0)) >= 0 {
		t.Fatal("pin keys must sort by room then version")
	}
}

func TestParseReactionAndPinRejectWrongLengths(t *testing.T) {
	for _, n := range []int{0, MsgLen, MsgLen + MaxReactionUser + 1} {
		if _, _, _, _, err := ParseReaction(make([]byte, n)); !errors.Is(err, ErrLength) {
			t.Errorf("ParseReaction(%d bytes) err = %v, want ErrLength", n, err)
		}
	}
	for _, n := range []int{0, PinLen - 1, PinLen + 1} {
		if _, _, err := ParsePin(make([]byte, n)); !errors.Is(err, ErrLength) {
			t.Errorf("ParsePin(%d bytes) err = %v, want ErrLength", n, err)
		}
	}
}
```

`apps/core/internal/domain/errors_test.go`, trong `TestDomainErrorsWrapAppKinds` thay:

```go
		{domain.ErrVersionConflict, apperr.ErrFailedPrecondition},
	}
```

bằng:

```go
		{domain.ErrVersionConflict, apperr.ErrFailedPrecondition},
		{domain.ErrTooManyEmojis, apperr.ErrFailedPrecondition},
		{domain.ErrTooManyPins, apperr.ErrFailedPrecondition},
	}
```

`apps/core/internal/domain/reaction_test.go` (`assertInvalid` có sẵn ở `validate_test.go`):

```go
package domain_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func TestValidateEmoji(t *testing.T) {
	tests := []struct {
		name  string
		emoji string
		ok    bool
	}{
		{"thumbs up", "👍", true},
		{"skin tone", "👍🏽", true},
		{"variation selector", "❤️", true},
		{"zero width joiner family", "👨‍👩‍👧", true},
		{"looks like a field path", "$e", true},
		{"has a dot", "a.b", true},
		{"32 bytes", strings.Repeat("a", 32), true},
		{"empty", "", false},
		{"33 bytes", strings.Repeat("a", 33), false},
		{"invalid utf-8", "\xff", false},
		{"newline", "a\nb", false},
		{"nul", "\x00", false},
		{"delete", "\x7f", false},
		{"c1 control", "\u0085", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domain.ValidateEmoji(tt.emoji)
			if tt.ok {
				if err != nil {
					t.Fatalf("ValidateEmoji(%q) = %v, want nil", tt.emoji, err)
				}
				return
			}
			assertInvalid(t, err, "emoji")
		})
	}
}

func TestSortReactionCountsByCountThenEmoji(t *testing.T) {
	counts := []domain.ReactionCount{{Emoji: "😂", Count: 1}, {Emoji: "👍", Count: 3}, {Emoji: "$e", Count: 1}, {Emoji: "❤️", Count: 3}}
	domain.SortReactionCounts(counts)
	want := []domain.ReactionCount{{Emoji: "❤️", Count: 3}, {Emoji: "👍", Count: 3}, {Emoji: "$e", Count: 1}, {Emoji: "😂", Count: 1}}
	if !slices.Equal(counts, want) {
		t.Fatalf("sorted = %v, want %v", counts, want)
	}
}

func TestNewMessagesCarryNoReactions(t *testing.T) {
	m := domain.Message{Room: 1, Seq: 1, Text: "hi", CreatedAt: time.UnixMilli(1)}
	if m.Reactions.Version != 0 || m.Reactions.Counts != nil {
		t.Fatalf("new message carries reactions %+v", m.Reactions)
	}
	r := domain.Reaction{Room: 1, Seq: 1, User: "alice"}
	if r.Emoji != "" || r.Prev != "" || r.N != 0 {
		t.Fatalf("zero reaction = %+v, want a tombstone-shaped zero", r)
	}
}
```

`apps/core/internal/domain/pin_test.go`:

```go
package domain_test

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

var pinAt = time.Unix(1_700_000_000, 0).UTC()

func pinAction(pv uint64, op domain.PinOp, thread, seq uint64) domain.PinAction {
	return domain.PinAction{Room: 7, PV: pv, Tenant: "acme", Op: op, Thread: thread, Seq: seq, By: "alice", At: pinAt.Add(time.Duration(pv) * time.Second)}
}

func pinned(pv, thread, seq uint64) domain.Pin {
	a := pinAction(pv, domain.PinOpPin, thread, seq)
	return domain.Pin{Thread: thread, Seq: seq, By: a.By, At: a.At, PV: pv}
}

func TestPinOpsAreTheStoredValues(t *testing.T) {
	if domain.PinOpPin != 1 || domain.PinOpUnpin != 2 {
		t.Fatalf("PinOpPin = %d, PinOpUnpin = %d; want 1 and 2, the values stored in pin_actions.op", domain.PinOpPin, domain.PinOpUnpin)
	}
}

func TestFoldPins(t *testing.T) {
	tests := []struct {
		name  string
		state domain.PinState
		facts []domain.PinAction
		want  domain.PinState
	}{
		{
			"pins in pv order with the newest first",
			domain.PinState{},
			[]domain.PinAction{pinAction(2, domain.PinOpPin, 0, 5), pinAction(1, domain.PinOpPin, 0, 3)},
			domain.PinState{Pins: []domain.Pin{pinned(2, 0, 5), pinned(1, 0, 3)}, Version: 2},
		},
		{
			"unpin removes only that message",
			domain.PinState{Pins: []domain.Pin{pinned(2, 0, 5), pinned(1, 0, 3)}, Version: 2},
			[]domain.PinAction{pinAction(3, domain.PinOpUnpin, 0, 3)},
			domain.PinState{Pins: []domain.Pin{pinned(2, 0, 5)}, Version: 3},
		},
		{
			"facts at or below the version are skipped",
			domain.PinState{Pins: []domain.Pin{pinned(1, 0, 3)}, Version: 2},
			[]domain.PinAction{pinAction(2, domain.PinOpUnpin, 0, 3), pinAction(1, domain.PinOpPin, 0, 9), pinAction(3, domain.PinOpPin, 4, 3)},
			domain.PinState{Pins: []domain.Pin{pinned(3, 4, 3), pinned(1, 0, 3)}, Version: 3},
		},
		{
			"repeated pins and stray unpins only move the version",
			domain.PinState{Pins: []domain.Pin{pinned(1, 0, 3)}, Version: 1},
			[]domain.PinAction{pinAction(2, domain.PinOpPin, 0, 3), pinAction(3, domain.PinOpUnpin, 0, 9)},
			domain.PinState{Pins: []domain.Pin{pinned(1, 0, 3)}, Version: 3},
		},
		{
			"no facts keep the state",
			domain.PinState{Pins: []domain.Pin{pinned(1, 0, 3)}, Version: 1},
			nil,
			domain.PinState{Pins: []domain.Pin{pinned(1, 0, 3)}, Version: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := domain.FoldPins(tt.state, tt.facts)
			if got.Version != tt.want.Version || !slices.Equal(got.Pins, tt.want.Pins) {
				t.Fatalf("FoldPins = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestFoldPinsLeavesItsInputsAlone(t *testing.T) {
	state := domain.PinState{Pins: []domain.Pin{pinned(2, 0, 4), pinned(1, 0, 3)}, Version: 2}
	facts := []domain.PinAction{pinAction(4, domain.PinOpUnpin, 0, 4), pinAction(3, domain.PinOpPin, 0, 8)}
	pins, order := slices.Clone(state.Pins), slices.Clone(facts)
	domain.FoldPins(state, facts)
	if !slices.Equal(state.Pins, pins) || !slices.Equal(facts, order) {
		t.Fatalf("FoldPins changed its inputs: pins %+v, facts %+v", state.Pins, facts)
	}
}

func TestPinStatePinned(t *testing.T) {
	s := domain.PinState{Pins: []domain.Pin{pinned(1, 0, 3)}, Version: 1}
	if !s.Pinned(0, 3) || s.Pinned(0, 4) || s.Pinned(1, 3) || (domain.PinState{}).Pinned(0, 3) {
		t.Fatalf("Pinned is wrong for %+v", s)
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./pkg/keys/... ./apps/core/internal/domain/..."`
Expected: FAIL biên dịch: `undefined: Reaction`, `undefined: ParseReaction`, `undefined: MaxReactionUser`, `undefined: Pin`, `undefined: PinLen` (keys); `undefined: domain.ErrTooManyEmojis`, `undefined: domain.ValidateEmoji`, `undefined: domain.ReactionCount`, `m.Reactions undefined`, `undefined: domain.PinAction`, `undefined: domain.FoldPins` (domain).

**Step 3: Code**

`pkg/keys/keys.go`:
- thay khối const:

```go
const (
	MsgLen   = 24
	EventLen = 16
	EditLen  = 28
)
```

bằng:

```go
const (
	MsgLen          = 24
	EventLen        = 16
	EditLen         = 28
	PinLen          = 16
	MaxReactionUser = 64
)
```

- thêm trước `func putMsg`:

```go
func Reaction(room, threadRoot, seq uint64, user string) []byte {
	b := make([]byte, MsgLen, MsgLen+len(user))
	putMsg(b, room, threadRoot, seq)
	return append(b, user...)
}

func ParseReaction(b []byte) (room, threadRoot, seq uint64, user string, err error) {
	if len(b) <= MsgLen || len(b) > MsgLen+MaxReactionUser {
		return 0, 0, 0, "", ErrLength
	}
	return be.Uint64(b[0:8]), be.Uint64(b[8:16]), be.Uint64(b[16:24]), string(b[MsgLen:]), nil
}

func Pin(room, pv uint64) []byte {
	b := make([]byte, PinLen)
	be.PutUint64(b[0:8], room)
	be.PutUint64(b[8:16], pv)
	return b
}

func ParsePin(b []byte) (room, pv uint64, err error) {
	if len(b) != PinLen {
		return 0, 0, ErrLength
	}
	return be.Uint64(b[0:8]), be.Uint64(b[8:16]), nil
}
```

`apps/core/internal/domain/message.go`, trong struct `Message` thay:

```go
	EditedAt  time.Time
	Hidden    bool
}
```

bằng:

```go
	EditedAt  time.Time
	Hidden    bool
	Reactions ReactionSummary
}
```

`apps/core/internal/domain/errors.go`, thay:

```go
	ErrVersionConflict = fmt.Errorf("message version conflict: %w", apperr.ErrFailedPrecondition)
)
```

bằng:

```go
	ErrVersionConflict = fmt.Errorf("message version conflict: %w", apperr.ErrFailedPrecondition)
	ErrTooManyEmojis   = fmt.Errorf("too many reaction emojis: %w", apperr.ErrFailedPrecondition)
	ErrTooManyPins     = fmt.Errorf("too many pinned messages: %w", apperr.ErrFailedPrecondition)
)
```

`apps/core/internal/domain/reaction.go`:

```go
package domain

import (
	"cmp"
	"slices"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxEmojiBytes = 32

type Reaction struct {
	Room   uint64
	Thread uint64
	Seq    uint64
	Tenant string
	User   string
	Emoji  string
	Prev   string
	N      uint32
	At     time.Time
}

type ReactionCount struct {
	Emoji string
	Count uint32
}

type ReactionSummary struct {
	Counts  []ReactionCount
	Version uint64
}

func ValidateEmoji(emoji string) error {
	if emoji == "" || len(emoji) > maxEmojiBytes || !utf8.ValidString(emoji) {
		return invalid("emoji")
	}
	for _, r := range emoji {
		if unicode.IsControl(r) {
			return invalid("emoji")
		}
	}
	return nil
}

func SortReactionCounts(counts []ReactionCount) {
	slices.SortFunc(counts, func(a, b ReactionCount) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Emoji, b.Emoji))
	})
}
```

`apps/core/internal/domain/pin.go`:

```go
package domain

import (
	"cmp"
	"slices"
	"time"
)

type PinOp uint8

const (
	PinOpPin PinOp = iota + 1
	PinOpUnpin
)

type PinAction struct {
	Room   uint64
	PV     uint64
	Tenant string
	Op     PinOp
	Thread uint64
	Seq    uint64
	By     string
	At     time.Time
}

type Pin struct {
	Thread uint64
	Seq    uint64
	By     string
	At     time.Time
	PV     uint64
}

type PinState struct {
	Pins    []Pin
	Version uint64
}

func (s PinState) Pinned(thread, seq uint64) bool {
	return slices.ContainsFunc(s.Pins, func(p Pin) bool { return p.Thread == thread && p.Seq == seq })
}

func FoldPins(s PinState, facts []PinAction) PinState {
	ordered := slices.SortedFunc(slices.Values(facts), func(a, b PinAction) int { return cmp.Compare(a.PV, b.PV) })
	out := PinState{Pins: slices.Clone(s.Pins), Version: s.Version}
	for _, a := range ordered {
		if a.PV <= out.Version {
			continue
		}
		out.Version = a.PV
		at := slices.IndexFunc(out.Pins, func(p Pin) bool { return p.Thread == a.Thread && p.Seq == a.Seq })
		switch {
		case a.Op == PinOpPin && at < 0:
			out.Pins = slices.Insert(out.Pins, 0, Pin{Thread: a.Thread, Seq: a.Seq, By: a.By, At: a.At, PV: a.PV})
		case a.Op == PinOpUnpin && at >= 0:
			out.Pins = slices.Delete(out.Pins, at, at+1)
		}
	}
	return out
}
```

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./pkg/keys/... ./apps/core/internal/domain/..."`
Expected: PASS. `wc -l pkg/keys/keys.go` ≤ 95.

**Step 5: Sửa các phép `==` trên `domain.Message`**

Run: `make vet`
Expected: FAIL biên dịch đúng các chỗ trong bảng ở đầu task, dạng `invalid operation: a == b (struct containing []domain.ReactionCount cannot be compared)` (storetest, mongostore) và `domain.Message does not satisfy comparable` (view, ở `slices.Equal`).

`apps/core/internal/store/storetest/fixtures.go`:
- import thêm `"reflect"` (nhóm stdlib, sau `"fmt"`).
- thay:

```go
func sameMessage(a, b domain.Message) bool {
	at, bt, ae, be := a.CreatedAt, b.CreatedAt, a.EditedAt, b.EditedAt
	a.CreatedAt, b.CreatedAt, a.EditedAt, b.EditedAt = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	return a == b && at.Equal(bt) && ae.Equal(be)
}
```

bằng:

```go
func sameMessage(a, b domain.Message) bool {
	at, bt, ae, be := a.CreatedAt, b.CreatedAt, a.EditedAt, b.EditedAt
	ar, br := a.Reactions, b.Reactions
	a.CreatedAt, b.CreatedAt, a.EditedAt, b.EditedAt = time.Time{}, time.Time{}, time.Time{}, time.Time{}
	a.Reactions, b.Reactions = domain.ReactionSummary{}, domain.ReactionSummary{}
	sameReactions := ar.Version == br.Version && slices.Equal(ar.Counts, br.Counts)
	return reflect.DeepEqual(a, b) && at.Equal(bt) && ae.Equal(be) && sameReactions
}
```

`apps/core/internal/store/storetest/feed_room_cases.go`, trong `assertChangedRoom` thay `if got != want || !gotAt.Equal(wantAt) || c.Msg != (domain.Message{}) {` bằng `if got != want || !gotAt.Equal(wantAt) || c.Msg.Seq != 0 {`.

`apps/core/internal/store/storetest/feed_edit_cases.go`, thay `if c.Kind != store.EditInserted || c.Msg != (domain.Message{}) || c.Room != (domain.Room{}) {` bằng `if c.Kind != store.EditInserted || c.Msg.Seq != 0 || c.Room != (domain.Room{}) {`.

`apps/core/internal/store/mongostore/codec_test.go`:
- import thêm `"reflect"` (sau `"math"`).
- trong `TestMessageCodecRoundTrip` thay `if got != m {` bằng `if !reflect.DeepEqual(got, m) {` (chỉ chỗ này; `got != m` trong `TestMemberCodecRoundTrip` là `domain.Member`, giữ nguyên).

`apps/core/internal/store/mongostore/edit_codec_test.go`:
- import thêm `"reflect"` (sau `"math"`).
- trong `TestMessageCodecCarriesEditStateButNotTheHiddenFlag` thay `if got != stored {` bằng `if !reflect.DeepEqual(got, stored) {` (chỉ chỗ này; `got != e` trong `TestEditCodecRoundTrip` là `domain.Edit`, giữ nguyên).

`apps/core/internal/view/masks_test.go`:
- import thêm `"reflect"` (trước `"slices"`).
- thay `if got := view.MaskDeleted(view.Viewer{User: "bob"}, page); !slices.Equal(got, want) {` bằng `if got := view.MaskDeleted(view.Viewer{User: "bob"}, page); !reflect.DeepEqual(got, want) {`.
- thay cả hai dòng `if !slices.Equal(page, before) {` bằng `if !reflect.DeepEqual(page, before) {`.
- thay `if got := view.HideForViewer(view.Viewer{}, page); !slices.Equal(got, page) {` bằng `if got := view.HideForViewer(view.Viewer{}, page); !reflect.DeepEqual(got, page) {`.
- `slices.Equal(seqs(got), want)` (so `[]uint64`) giữ nguyên.

`apps/core/internal/view/pipeline_test.go`:
- import thêm `"reflect"` (trước `"slices"`).
- trong `TestCollapseRetriedKeepsTheLowestSeqOfEachSend` thay `if !slices.Equal(page, before) {` bằng `if !reflect.DeepEqual(page, before) {`.

Run: `make vet`
Expected: sạch.

Run: `make test`
Expected: PASS toàn repo (mongostore integration skip). `make vet` chỉ bắt lỗi biên dịch; `make test` chạy mọi test để chắc không test nào đổi hành vi khi `Message` mang thêm `Reactions` (ví dụ so sánh bằng `reflect.DeepEqual` giữa nil và slice rỗng). Test đỏ ngoài bảng trên → sửa cùng cách nếu chỉ là phép so sánh, còn lại dừng và báo cáo. `wc -l apps/core/internal/store/storetest/fixtures.go apps/core/internal/store/mongostore/codec_test.go` lần lượt ≤ 140 và ≤ 195.

**Step 6: Commit**

INDEXES.csv:
- dòng `apps/core/internal/domain`: thay `Member.ClearedBeforeSeq;` bằng `Member.ClearedBeforeSeq; Message.Reactions summary (counts per emoji + version; Message is no longer comparable with ==); Reaction doc (one emoji per user and message, empty emoji = tombstone, Prev, change number N), ValidateEmoji (UTF-8, 1-32 bytes, no control runes), SortReactionCounts (count desc, emoji asc); PinAction fact (PinOpPin/PinOpUnpin stored as 1/2), Pin, PinState.Pinned, FoldPins (pure: facts above the version in pv order, newest pin first, no-op facts only move the version); ErrTooManyEmojis/ErrTooManyPins (failed precondition);`; trong key symbols thay `ErrVersionConflict` bằng `ErrVersionConflict;Reaction;ReactionCount;ReactionSummary;ValidateEmoji;SortReactionCounts;PinOp;PinOpPin;PinOpUnpin;PinAction;Pin;PinState;PinState.Pinned;FoldPins;ErrTooManyEmojis;ErrTooManyPins`; cột decisions thay `D7;D35;D62;D63;D87` bằng `D7;D35;D62;D63;D87;D88;D89;D92`.
- dòng `pkg/keys`: thay `Edit (+version);` bằng `Edit (+version); Reaction = Msg key + user bytes (no length prefix, user 1-64 bytes, so one message's reactions are one range); Pin = room|pv (16B);`; trong key symbols thay `Edit;ParseEdit;` bằng `Edit;ParseEdit;Reaction;ParseReaction;Pin;ParsePin;PinLen;MaxReactionUser;`; cột decisions thay `D10` bằng `D10;D88;D92`.

```bash
make fmt-check && make vet && make lint
git add pkg/keys/reaction_pin_test.go apps/core/internal/domain/reaction.go apps/core/internal/domain/reaction_test.go apps/core/internal/domain/pin.go apps/core/internal/domain/pin_test.go
git commit -m "feat(domain): add reactions, pin facts and their keys" -- pkg/keys/ apps/core/internal/domain/ apps/core/internal/store/storetest/fixtures.go apps/core/internal/store/storetest/feed_room_cases.go apps/core/internal/store/storetest/feed_edit_cases.go apps/core/internal/store/mongostore/codec_test.go apps/core/internal/store/mongostore/edit_codec_test.go apps/core/internal/view/masks_test.go apps/core/internal/view/pipeline_test.go INDEXES.csv
```

---

### Task 3: Proto + pbconv (id, event, summary, pins) + 4 kind của publish

Proto thêm `Message.reactions = 14`, 3 RPC (`ReactMessage`, `PinMessage`, `UnpinMessage`), `ReactionCount`, `ReactionSummary`, `Pin` và bốn payload event `reaction_changed = 24`, `counts_changed = 25`, `message_pinned = 26`, `message_unpinned = 27` (field mới là số tiếp theo còn trống: `Message` đang tới 13, oneof `Event.payload` tới 23). `pbconv` dựng id, envelope, summary (nil khi chưa có gì, để tin chưa ai react không đổi bytes), danh sách ghim. Publisher biết bốn kind mới và **không** đặt ack mark cho chúng (D93).

Không cần sửa caller: `grpcsrv.Service` nhúng `chatimv1.UnimplementedCoreServiceServer` (3 RPC trả `Unimplemented` tới Task 11), `tools/internal/route/fakes_test.go` `fakeCore` đã nhúng `chatimv1.CoreServiceClient` (từ M2b.2), `tools/corecli/internal/e2e.EventOf` trả `ok=false` cho payload lạ, corebench dùng getter nil-safe.

**Id event không bao giờ trùng nhau** (bảng ở "Ghi chú tích hợp"): ba số đầu cố định từ trái; reaction luôn kết thúc `-n\d+`, số đếm luôn `-reactions-v\d+`, sửa/xoá `-v\d+`; ghim `{room}-p{pv}` và room `{room}-created` chỉ hai đoạn. User `[A-Za-z0-9_-]{1,64}` có thể chứa `-`, `reactions`, `v1`, `a-n1` nhưng không làm hai loại trùng nhau: test bảng ở Step 3 chốt điều đó.

**Files:**
- Modify: `proto/chatim/v1/core.proto`, `proto/chatim/v1/events.proto`
- Regenerate: `pkg/pb/chatim/v1/core.pb.go`, `pkg/pb/chatim/v1/core_grpc.pb.go`, `pkg/pb/chatim/v1/events.pb.go` (`make proto`)
- Modify: `apps/core/internal/pbconv/pbconv.go` (`Message`)
- Create: `apps/core/internal/pbconv/reaction.go`, `apps/core/internal/pbconv/pin.go`
- Create: `apps/core/internal/pbconv/reaction_test.go`, `apps/core/internal/pbconv/pin_test.go`, `apps/core/internal/pbconv/event_id_test.go`
- Modify: `apps/core/internal/publish/stream.go` (const), `apps/core/internal/publish/message.go` (`eventKind`)
- Create: `apps/core/internal/publish/reaction_pin_event_test.go`
- Modify: `INDEXES.csv` (dòng `proto/chatim/v1/core.proto`, `proto/chatim/v1/events.proto`, `pkg/pb/chatim/v1`, `apps/core/internal/pbconv`, `apps/core/internal/publish`)

**Step 1: Proto**

`proto/chatim/v1/core.proto`:

- thay:

```proto
  rpc GetEditHistory(GetEditHistoryRequest) returns (GetEditHistoryResponse);
}
```

bằng:

```proto
  rpc GetEditHistory(GetEditHistoryRequest) returns (GetEditHistoryResponse);
  rpc ReactMessage(ReactMessageRequest) returns (ReactMessageResponse);
  rpc PinMessage(PinMessageRequest) returns (PinMessageResponse);
  rpc UnpinMessage(UnpinMessageRequest) returns (UnpinMessageResponse);
}
```

- trong `message Message`, thay:

```proto
  bool hidden = 13;
}
```

bằng:

```proto
  bool hidden = 13;
  ReactionSummary reactions = 14;
}

message ReactionCount {
  string emoji = 1;
  uint32 count = 2;
}

message ReactionSummary {
  repeated ReactionCount counts = 1;
  uint64 version = 2;
}

message Pin {
  uint64 thread_root = 1;
  uint64 seq = 2;
  string by = 3;
  google.protobuf.Timestamp pinned_at = 4;
  uint64 pin_version = 5;
}
```

(`bool hidden = 13;\n}` là duy nhất trong file.)

- thêm vào cuối file:

```proto

message ReactMessageRequest {
  string room_id = 1;
  uint64 thread_root = 2;
  uint64 seq = 3;
  string emoji = 4;
}

message ReactMessageResponse {
  uint32 change = 1;
  ReactionSummary reactions = 2;
}

message PinMessageRequest {
  string room_id = 1;
  uint64 thread_root = 2;
  uint64 seq = 3;
}

message PinMessageResponse {
  uint64 pin_version = 1;
  repeated Pin pins = 2;
}

message UnpinMessageRequest {
  string room_id = 1;
  uint64 thread_root = 2;
  uint64 seq = 3;
}

message UnpinMessageResponse {
  uint64 pin_version = 1;
  repeated Pin pins = 2;
}
```

`proto/chatim/v1/events.proto`:

- thay:

```proto
    MessageDeleted message_deleted = 23;
  }
}
```

bằng:

```proto
    MessageDeleted message_deleted = 23;
    ReactionChanged reaction_changed = 24;
    CountsChanged counts_changed = 25;
    MessagePinned message_pinned = 26;
    MessageUnpinned message_unpinned = 27;
  }
}
```

- thêm vào cuối file:

```proto

message ReactionChanged {
  string user = 1;
  string emoji = 2;
  string previous_emoji = 3;
  uint32 change = 4;
}

message CountsChanged {
  string counter = 1;
  ReactionSummary reactions = 2;
}

message MessagePinned {
  Message message = 1;
  uint64 pin_version = 2;
}

message MessageUnpinned {
  Message message = 1;
  uint64 pin_version = 2;
}
```

(Viết nhiều dòng như trên: `make buf-lint` chạy `buf format -d --exit-code`.)

Run: `make proto && make buf-lint`
Expected: không lỗi; `git status --short` có `M` ở `pkg/pb/chatim/v1/core.pb.go`, `core_grpc.pb.go`, `events.pb.go`. Kiểm: `grep -l "coreServiceClient) ReactMessage" pkg/pb/chatim/v1/core_grpc.pb.go`, `grep -l "type Event_CountsChanged struct" pkg/pb/chatim/v1/events.pb.go`, `grep -l "func (x \*Message) GetReactions" pkg/pb/chatim/v1/core.pb.go` đều in tên file.

Run: `make vet`
Expected: sạch (route `fakeCore` và `grpcsrv.Service` đều nhúng interface/Unimplemented). Lỗi biên dịch ở đâu đó → dừng, báo cáo.

**Step 2: Test pbconv + publish**

`apps/core/internal/pbconv/event_id_test.go`:

```go
package pbconv_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
)

func TestReactionAndPinEventIDs(t *testing.T) {
	cases := []struct{ name, got, want string }{
		{"reaction", pbconv.ReactionEventID(42, 0, 7, "bob", 1), "42-0-7-bob-n1"},
		{"thread reaction by a dashed user", pbconv.ReactionEventID(42, 3, 9, "a-n1", 12), "42-3-9-a-n1-n12"},
		{
			"widest reaction",
			pbconv.ReactionEventID(math.MaxInt64, math.MaxUint64, math.MaxUint64, "u", math.MaxUint32),
			"9223372036854775807-18446744073709551615-18446744073709551615-u-n4294967295",
		},
		{"counts", pbconv.ReactionCountsEventID(42, 0, 7, 3), "42-0-7-reactions-v3"},
		{"pin", pbconv.PinEventID(42, 5), "42-p5"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if pbconv.ReactionsCounter != "reactions" {
		t.Errorf("ReactionsCounter = %q, want reactions", pbconv.ReactionsCounter)
	}
}

func TestEventIDsNeverCollide(t *testing.T) {
	users := []string{"alice", "reactions", "v1", "a-n1", "n1", "1", "created", "p1", "a-v1", "reactions-v1"}
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
		for _, pv := range []uint64{1, 2} {
			add(pbconv.PinEventID(room, pv), fmt.Sprintf("pin %d/p%d", room, pv))
		}
		for _, thread := range []uint64{0, 1} {
			for _, seq := range []uint64{1, 2} {
				at := fmt.Sprintf("%d/%d/%d", room, thread, seq)
				add(pbconv.MessageEventID(room, thread, seq), "message "+at)
				for _, v := range []uint32{1, 2} {
					add(pbconv.MessageChangeEventID(room, thread, seq, v), fmt.Sprintf("change %s v%d", at, v))
					add(pbconv.ReactionCountsEventID(room, thread, seq, uint64(v)), fmt.Sprintf("counts %s v%d", at, v))
					for _, u := range users {
						add(pbconv.ReactionEventID(room, thread, seq, u, v), fmt.Sprintf("reaction %s %s n%d", at, u, v))
					}
				}
			}
		}
	}
}
```

`apps/core/internal/pbconv/reaction_test.go` (`sample()`, `sentAt` có sẵn ở `pbconv_test.go`):

```go
package pbconv_test

import (
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var reactedAt = sentAt.Add(2 * time.Minute)

func counts(c ...domain.ReactionCount) domain.ReactionSummary {
	return domain.ReactionSummary{Counts: c, Version: 3}
}

func TestReactionSummary(t *testing.T) {
	if got := pbconv.ReactionSummary(domain.ReactionSummary{}); got != nil {
		t.Fatalf("ReactionSummary(zero) = %v, want nil", got)
	}
	s := counts(domain.ReactionCount{Emoji: "👍", Count: 2}, domain.ReactionCount{Emoji: "$e", Count: 1})
	want := &chatimv1.ReactionSummary{Counts: []*chatimv1.ReactionCount{{Emoji: "👍", Count: 2}, {Emoji: "$e", Count: 1}}, Version: 3}
	if got := pbconv.ReactionSummary(s); !proto.Equal(got, want) {
		t.Fatalf("ReactionSummary = %v, want %v", got, want)
	}
	cleared := pbconv.ReactionSummary(domain.ReactionSummary{Version: 4})
	if cleared == nil || len(cleared.GetCounts()) != 0 || cleared.GetVersion() != 4 {
		t.Fatalf("ReactionSummary(all removed) = %v, want version 4 and no counts", cleared)
	}
}

func TestMessageCarriesReactionCounts(t *testing.T) {
	m := sample()
	m.Reactions = counts(domain.ReactionCount{Emoji: "👍", Count: 2})
	if got := pbconv.Message(m).GetReactions(); got == nil || !proto.Equal(got, pbconv.ReactionSummary(m.Reactions)) {
		t.Fatalf("Message reactions = %v, want %v", got, pbconv.ReactionSummary(m.Reactions))
	}
	if got := pbconv.Message(sample()).GetReactions(); got != nil {
		t.Fatalf("unreacted message carries reactions %v, want nil", got)
	}
}

func TestReactionChangedEnvelope(t *testing.T) {
	r := domain.Reaction{Room: 9_007_199_254_740_993, Seq: 7, Tenant: "acme", User: "bob", Emoji: "❤️", Prev: "👍", N: 2, At: reactedAt}
	want := &chatimv1.Event{
		Id: "9007199254740993-0-7-bob-n2", Tenant: "acme", RoomId: "9007199254740993", RoomType: chatimv1.RoomType_ROOM_TYPE_GROUP,
		Seq: 7, Actor: "bob", Ts: timestamppb.New(reactedAt),
		Payload: &chatimv1.Event_ReactionChanged{ReactionChanged: &chatimv1.ReactionChanged{User: "bob", Emoji: "❤️", PreviousEmoji: "👍", Change: 2}},
	}
	if got := pbconv.ReactionChanged(domain.RoomGroup, r); !proto.Equal(got, want) {
		t.Fatalf("ReactionChanged = %v, want %v", got, want)
	}
}

func TestCountsChangedEnvelope(t *testing.T) {
	m := sample()
	m.Reactions = counts(domain.ReactionCount{Emoji: "👍", Count: 2})
	want := &chatimv1.Event{
		Id: "9007199254740993-0-7-reactions-v3", Tenant: "acme", RoomId: "9007199254740993", RoomType: chatimv1.RoomType_ROOM_TYPE_DM,
		Seq: 7, Ts: timestamppb.New(reactedAt),
		Payload: &chatimv1.Event_CountsChanged{CountsChanged: &chatimv1.CountsChanged{Counter: pbconv.ReactionsCounter, Reactions: pbconv.ReactionSummary(m.Reactions)}},
	}
	if got := pbconv.CountsChanged(domain.RoomDM, m, reactedAt); !proto.Equal(got, want) {
		t.Fatalf("CountsChanged = %v, want %v", got, want)
	}
}
```

`apps/core/internal/pbconv/pin_test.go`:

```go
package pbconv_test

import (
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func pinFact(op domain.PinOp, pv uint64) domain.PinAction {
	return domain.PinAction{Room: 9_007_199_254_740_993, PV: pv, Tenant: "acme", Op: op, Seq: 7, By: "bob", At: reactedAt}
}

func pinEnvelope(id string) *chatimv1.Event {
	return &chatimv1.Event{
		Id: id, Tenant: "acme", RoomId: "9007199254740993", RoomType: chatimv1.RoomType_ROOM_TYPE_GROUP,
		Seq: 7, Actor: "bob", Ts: timestamppb.New(reactedAt),
	}
}

func TestPinChangedPicksTheEventByOp(t *testing.T) {
	m := sample()
	wantPin := pinEnvelope("9007199254740993-p3")
	wantPin.Payload = &chatimv1.Event_MessagePinned{MessagePinned: &chatimv1.MessagePinned{Message: pbconv.Message(m), PinVersion: 3}}
	wantUnpin := pinEnvelope("9007199254740993-p4")
	wantUnpin.Payload = &chatimv1.Event_MessageUnpinned{MessageUnpinned: &chatimv1.MessageUnpinned{Message: pbconv.Message(m), PinVersion: 4}}
	pin, unpin := pinFact(domain.PinOpPin, 3), pinFact(domain.PinOpUnpin, 4)
	cases := []struct {
		name      string
		got, want *chatimv1.Event
	}{
		{"pinned", pbconv.MessagePinned(domain.RoomGroup, m, pin), wantPin},
		{"unpinned", pbconv.MessageUnpinned(domain.RoomGroup, m, unpin), wantUnpin},
		{"changed by a pin fact", pbconv.PinChanged(domain.RoomGroup, m, pin), wantPin},
		{"changed by an unpin fact", pbconv.PinChanged(domain.RoomGroup, m, unpin), wantUnpin},
	}
	for _, c := range cases {
		if !proto.Equal(c.got, c.want) {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestPinChangedDropsTheTextOfADeletedMessage(t *testing.T) {
	m := sample()
	m.Deleted, m.Version = true, 2
	got := pbconv.PinChanged(domain.RoomGroup, m, pinFact(domain.PinOpUnpin, 5)).GetMessageUnpinned().GetMessage()
	if got.GetText() != "" || !got.GetDeleted() || got.GetSeq() != 7 {
		t.Fatalf("snapshot of a deleted message = %v, want seq 7, deleted and no text", got)
	}
}

func TestPinsListsEveryPin(t *testing.T) {
	pins := []domain.Pin{{Seq: 9, By: "bob", At: reactedAt, PV: 4}, {Thread: 3, Seq: 7, By: "alice", At: sentAt, PV: 1}}
	want := []*chatimv1.Pin{
		{Seq: 9, By: "bob", PinnedAt: timestamppb.New(reactedAt), PinVersion: 4},
		{ThreadRoot: 3, Seq: 7, By: "alice", PinnedAt: timestamppb.New(sentAt), PinVersion: 1},
	}
	if got := pbconv.Pins(pins); !slices.EqualFunc(got, want, func(a, b *chatimv1.Pin) bool { return proto.Equal(a, b) }) {
		t.Fatalf("Pins = %v, want %v", got, want)
	}
	if got := pbconv.Pins(nil); len(got) != 0 {
		t.Fatalf("Pins(nil) = %v, want empty", got)
	}
}
```

`apps/core/internal/publish/reaction_pin_event_test.go` (`tenant`, `roomA = 101`, `sentAt` có sẵn ở `harness_test.go`):

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

func TestReactionAndPinEventsGoToTheirOwnSubjectsWithoutAMark(t *testing.T) {
	m := domain.Message{
		Room: roomA, Seq: 7, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "hi", CID: "c-7", CreatedAt: sentAt,
		Reactions: domain.ReactionSummary{Counts: []domain.ReactionCount{{Emoji: "👍", Count: 1}}, Version: 2},
	}
	r := domain.Reaction{Room: roomA, Seq: 7, Tenant: tenant, User: "bob", Emoji: "👍", N: 1, At: sentAt}
	pin := domain.PinAction{Room: roomA, PV: 3, Tenant: tenant, Op: domain.PinOpPin, Seq: 7, By: "bob", At: sentAt}
	unpin := pin
	unpin.PV, unpin.Op = 4, domain.PinOpUnpin
	cases := map[string]struct {
		ev          *chatimv1.Event
		subject, id string
	}{
		"reaction": {pbconv.ReactionChanged(domain.RoomGroup, r), "evt.acme.room.101.reaction_changed", "101-0-7-bob-n1"},
		"counts":   {pbconv.CountsChanged(domain.RoomGroup, m, sentAt), "evt.acme.room.101.counts_changed", "101-0-7-reactions-v2"},
		"pinned":   {pbconv.PinChanged(domain.RoomGroup, m, pin), "evt.acme.room.101.msg_pinned", "101-p3"},
		"unpinned": {pbconv.PinChanged(domain.RoomGroup, m, unpin), "evt.acme.room.101.msg_unpinned", "101-p4"},
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
```

**Step 3: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/pbconv/... ./apps/core/internal/publish/..."`
Expected: FAIL biên dịch ở pbconv: `undefined: pbconv.ReactionEventID`, `undefined: pbconv.ReactionCountsEventID`, `undefined: pbconv.PinEventID`, `undefined: pbconv.ReactionsCounter`, `undefined: pbconv.ReactionSummary`, `undefined: pbconv.ReactionChanged`, `undefined: pbconv.CountsChanged`, `undefined: pbconv.MessagePinned`, `undefined: pbconv.PinChanged`, `undefined: pbconv.Pins` (publish fail biên dịch vì cùng symbol).

**Step 4: Code pbconv**

`apps/core/internal/pbconv/pbconv.go`, trong `Message` thay:

```go
		Hidden:     m.Hidden,
	}
}
```

bằng:

```go
		Hidden:     m.Hidden,
		Reactions:  ReactionSummary(m.Reactions),
	}
}
```

`apps/core/internal/pbconv/reaction.go`:

```go
package pbconv

import (
	"strconv"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const ReactionsCounter = "reactions"

func ReactionEventID(room, thread, seq uint64, user string, change uint32) string {
	return MessageEventID(room, thread, seq) + "-" + user + "-n" + strconv.FormatUint(uint64(change), 10)
}

func ReactionCountsEventID(room, thread, seq, version uint64) string {
	return MessageEventID(room, thread, seq) + "-" + ReactionsCounter + "-v" + strconv.FormatUint(version, 10)
}

func ReactionSummary(s domain.ReactionSummary) *chatimv1.ReactionSummary {
	if s.Version == 0 && len(s.Counts) == 0 {
		return nil
	}
	counts := make([]*chatimv1.ReactionCount, len(s.Counts))
	for i, c := range s.Counts {
		counts[i] = &chatimv1.ReactionCount{Emoji: c.Emoji, Count: c.Count}
	}
	return &chatimv1.ReactionSummary{Counts: counts, Version: s.Version}
}

func ReactionChanged(roomType domain.RoomType, r domain.Reaction) *chatimv1.Event {
	return &chatimv1.Event{
		Id:         ReactionEventID(r.Room, r.Thread, r.Seq, r.User, r.N),
		Tenant:     r.Tenant,
		RoomId:     RoomID(r.Room),
		RoomType:   RoomType(roomType),
		ThreadRoot: r.Thread,
		Seq:        r.Seq,
		Actor:      r.User,
		Ts:         timestamppb.New(r.At),
		Payload: &chatimv1.Event_ReactionChanged{ReactionChanged: &chatimv1.ReactionChanged{
			User: r.User, Emoji: r.Emoji, PreviousEmoji: r.Prev, Change: r.N,
		}},
	}
}

func CountsChanged(roomType domain.RoomType, m domain.Message, at time.Time) *chatimv1.Event {
	return &chatimv1.Event{
		Id:         ReactionCountsEventID(m.Room, m.Thread, m.Seq, m.Reactions.Version),
		Tenant:     m.Tenant,
		RoomId:     RoomID(m.Room),
		RoomType:   RoomType(roomType),
		ThreadRoot: m.Thread,
		Seq:        m.Seq,
		Ts:         timestamppb.New(at),
		Payload: &chatimv1.Event_CountsChanged{CountsChanged: &chatimv1.CountsChanged{
			Counter: ReactionsCounter, Reactions: ReactionSummary(m.Reactions),
		}},
	}
}
```

`apps/core/internal/pbconv/pin.go`:

```go
package pbconv

import (
	"strconv"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func PinEventID(room, pv uint64) string { return RoomID(room) + "-p" + strconv.FormatUint(pv, 10) }

func MessagePinned(roomType domain.RoomType, m domain.Message, a domain.PinAction) *chatimv1.Event {
	ev := pinEvent(roomType, a)
	ev.Payload = &chatimv1.Event_MessagePinned{MessagePinned: &chatimv1.MessagePinned{Message: Message(m), PinVersion: a.PV}}
	return ev
}

func MessageUnpinned(roomType domain.RoomType, m domain.Message, a domain.PinAction) *chatimv1.Event {
	ev := pinEvent(roomType, a)
	ev.Payload = &chatimv1.Event_MessageUnpinned{MessageUnpinned: &chatimv1.MessageUnpinned{Message: Message(m), PinVersion: a.PV}}
	return ev
}

func PinChanged(roomType domain.RoomType, m domain.Message, a domain.PinAction) *chatimv1.Event {
	if m.Deleted {
		m.Text = ""
	}
	if a.Op == domain.PinOpUnpin {
		return MessageUnpinned(roomType, m, a)
	}
	return MessagePinned(roomType, m, a)
}

func Pins(pins []domain.Pin) []*chatimv1.Pin {
	out := make([]*chatimv1.Pin, len(pins))
	for i, p := range pins {
		out[i] = &chatimv1.Pin{ThreadRoot: p.Thread, Seq: p.Seq, By: p.By, PinnedAt: timestamppb.New(p.At), PinVersion: p.PV}
	}
	return out
}

func pinEvent(roomType domain.RoomType, a domain.PinAction) *chatimv1.Event {
	return &chatimv1.Event{
		Id:         PinEventID(a.Room, a.PV),
		Tenant:     a.Tenant,
		RoomId:     RoomID(a.Room),
		RoomType:   RoomType(roomType),
		ThreadRoot: a.Thread,
		Seq:        a.Seq,
		Actor:      a.By,
		Ts:         timestamppb.New(a.At),
	}
}
```

**Step 5: Code publish**

`apps/core/internal/publish/stream.go`, thay:

```go
	msgEdited   = "msg_edited"
	msgDeleted  = "msg_deleted"
)
```

bằng:

```go
	msgEdited       = "msg_edited"
	msgDeleted      = "msg_deleted"
	reactionChanged = "reaction_changed"
	countsChanged   = "counts_changed"
	msgPinned       = "msg_pinned"
	msgUnpinned     = "msg_unpinned"
)
```

(gofmt căn lại các dòng const phía trên trong cùng khối.)

`apps/core/internal/publish/message.go`, trong `eventKind` thay:

```go
	case *chatimv1.Event_MessageDeleted:
		return msgDeleted, true
```

bằng:

```go
	case *chatimv1.Event_MessageDeleted:
		return msgDeleted, true
	case *chatimv1.Event_ReactionChanged:
		return reactionChanged, true
	case *chatimv1.Event_CountsChanged:
		return countsChanged, true
	case *chatimv1.Event_MessagePinned:
		return msgPinned, true
	case *chatimv1.Event_MessageUnpinned:
		return msgUnpinned, true
```

`ack_mark_policy.go` giữ nguyên: `markKey` chỉ có case `Event_MessageCreated`; bốn payload mới rơi vào `default` (không mark). Test Step 2 chốt điều đó.

**Step 6: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/pbconv/... ./apps/core/internal/publish/... ./apps/core/internal/grpcsrv/... ./apps/core/internal/mutate/... ./tools/internal/route/... ./tools/corecli/..."`
Expected: PASS. `grpcsrv` `TestHistoryPagesThroughWhatWasSent` (so proto `Message`) vẫn xanh vì tin chưa react có `Reactions` zero → `reactions` nil; đó là lý do `ReactionSummary` trả nil khi `Version == 0` và không có count. `wc -l apps/core/internal/pbconv/*.go` mỗi file < 200 (`pbconv.go` ≤ 130).

**Step 7: Commit**

INDEXES.csv:
- dòng `proto/chatim/v1/core.proto`: thay `CoreService CreateRoom/SendMessage/GetHistory;` bằng `CoreService CreateRoom/SendMessage/GetHistory/EditMessage/DeleteMessage/HideMessage/ClearHistory/GetEditHistory/ReactMessage/PinMessage/UnpinMessage; Message.reactions (ReactionSummary: counts per emoji + version, field 14); Pin (thread_root, seq, by, pinned_at, pin_version);`; key symbols thay `CoreService;Room;Message;HistoryAnchor` bằng `CoreService;Room;Message;HistoryAnchor;ReactionCount;ReactionSummary;Pin;ReactMessageRequest;PinMessageRequest;UnpinMessageRequest`; cột decisions của dòng này thay `D17;D48` bằng `D17;D48;D90;D92;D94`.
- dòng `proto/chatim/v1/events.proto`: thay `with oneof payload MessageCreated;` bằng `with oneof payload MessageCreated/RoomCreated/MessageEdited/MessageDeleted/ReactionChanged (24)/CountsChanged (25)/MessagePinned (26)/MessageUnpinned (27);`; key symbols thay `Event;MessageCreated` bằng `Event;MessageCreated;ReactionChanged;CountsChanged;MessagePinned;MessageUnpinned`; cột decisions của dòng này thay `D17;D48` bằng `D17;D48;D93`.
- dòng `pkg/pb/chatim/v1`: thay `MessageEdited;MessageDeleted;Message;MessageVersion;EditKind;Room` bằng `MessageEdited;MessageDeleted;ReactionChanged;CountsChanged;MessagePinned;MessageUnpinned;Message;MessageVersion;EditKind;ReactionSummary;ReactionCount;Pin;Room`.
- dòng `apps/core/internal/pbconv`: thay `MessageChanged picks msg_deleted for a delete fact (else msg_edited)` bằng `MessageChanged picks msg_deleted for a delete fact (else msg_edited); Message carries reactions (ReactionSummary: nil when version 0 and no counts); ReactionEventID {room}-{thread}-{seq}-{user}-n{change}, ReactionCountsEventID {room}-{thread}-{seq}-reactions-v{version}, PinEventID {room}-p{pv} (table test: no two kinds share an id); ReactionChanged (actor = user), CountsChanged (counter reactions, no actor), MessagePinned/MessageUnpinned (snapshot + pin version; actor and ts from the fact); PinChanged picks by op and drops the text of a deleted target; Pins`; trong key symbols thay `MessageChanged;` bằng `MessageChanged;ReactionsCounter;ReactionEventID;ReactionCountsEventID;PinEventID;ReactionSummary;ReactionChanged;CountsChanged;MessagePinned;MessageUnpinned;PinChanged;Pins;`; cột decisions thay `D48;D83` bằng `D48;D83;D90;D93`.
- dòng `apps/core/internal/publish`: thay `publishes msg_created/room_created/msg_edited/msg_deleted;` bằng `publishes msg_created/room_created/msg_edited/msg_deleted/reaction_changed/counts_changed/msg_pinned/msg_unpinned;`; cột decisions thay `D65;D76;D83` bằng `D65;D76;D83;D93`.

```bash
make fmt-check && make vet && make lint && make buf-lint
git add apps/core/internal/pbconv/reaction.go apps/core/internal/pbconv/pin.go apps/core/internal/pbconv/reaction_test.go apps/core/internal/pbconv/pin_test.go apps/core/internal/pbconv/event_id_test.go apps/core/internal/publish/reaction_pin_event_test.go
git commit -m "feat(proto): add reaction and pin RPCs, summaries and events" -- proto/chatim/v1/ pkg/pb/chatim/v1/ apps/core/internal/pbconv/ apps/core/internal/publish/ INDEXES.csv
```

---

### Task 4: Store port + `ChangeKind`/`Change` + memstore + storetest `RunReactions`/`RunPins` + write contract

Thêm bốn port theo hợp đồng, mỗi port là interface riêng (không thêm vào `Messages`/`Rooms`), nên `mongostore.Store` và mọi fake của `store.Messages`/`store.Rooms` (actor, grpcsrv, effects, resync, mutate) không vỡ ở task này. memstore: kiểu mới `Reactions`, `Pins`; `*Messages` cài `ReactionSummaries`, `*Rooms` cài `PinProjector`. Mongo cài ở Task 5.

Thêm ngoài hợp đồng (dùng chung cho hai adapter, để luật kiểm nằm một chỗ):
- `store.ValidateReactionTarget(key MsgKey, user string) error`: khoá hợp lệ + `domain.ValidUser`. `ValidateReaction` = target + tenant khác rỗng. `Set` của mọi adapter gọi `ValidateReaction` rồi `domain.ValidateEmoji` (gồm "emoji khác rỗng" của hợp đồng); `Remove` gọi `ValidateReactionTarget`.
- `store.ValidateVersionBump(base, next uint64) error`: `next > base` và `next ≤ MaxInt64` (vì Mongo lưu `rx.v`, `pv` là int64), không thì `invalid("version")`. Dùng ở `SetReactions` và `ApplyPins` của mọi adapter.

`ChangeKind` thêm `ReactionChanged = 4`, `PinInserted = 5` (giá trị đi vào byte đầu của work record, chỉ được thêm cuối). `Change` thêm `Reaction`, `Pin`. Lint `exhaustive` (`default-signifies-exhaustive`) sẽ báo `work.RecordOf` (switch không có `default`) thiếu hai case: task này thêm case chỉ lấy khoá (`ReactionChanged`: room/thread/seq/version = N; `PinInserted`: room, seq = pv); Task 7 thêm `User`. `KnownKind` chưa nhận hai kind mới, nên chưa record nào đi qua.

memstore `Insert` bỏ `Reactions` của tin vào (như `Hidden`), giống Mongo: `Insert` không bao giờ ghi `rx`. Contract có case chốt điều đó.

**Files:**
- Create: `apps/core/internal/store/reaction.go`, `apps/core/internal/store/pin.go`, `apps/core/internal/store/reaction_test.go`
- Modify: `apps/core/internal/store/feed.go` (`ChangeKind`, `Change`), `apps/core/internal/store/validate.go` (`ValidateVersionBump`), `apps/core/internal/store/write_contract_test.go`
- Modify: `apps/core/internal/work/record.go` (`RecordOf`)
- Create: `apps/core/internal/store/memstore/reactions.go`, `reaction_reads.go`, `reaction_summary.go`, `pins.go`, `pin_state.go`
- Modify: `apps/core/internal/store/memstore/memstore.go` (`insertLocked`), `apps/core/internal/store/memstore/rooms.go` (`Rooms`, `NewRooms`), `apps/core/internal/store/memstore/memstore_test.go`
- Create: `apps/core/internal/store/storetest/reaction_cases.go`, `reaction_set_cases.go`, `reaction_read_cases.go`, `summary_cases.go`, `pin_cases.go`, `pin_fact_cases.go`, `pin_state_cases.go`
- Modify: `INDEXES.csv` (dòng `apps/core/internal/store`, `apps/core/internal/store/memstore`, `apps/core/internal/store/storetest`)

**Step 1: Test store**

`apps/core/internal/store/reaction_test.go`:

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

func TestReactionAndPinErrorsWrapAppKinds(t *testing.T) {
	for _, c := range []struct{ err, kind error }{
		{store.ErrStaleRead, apperr.ErrUnavailable},
		{store.ErrReactionContended, apperr.ErrUnavailable},
		{store.ErrPinExists, apperr.ErrAlreadyExists},
		{store.ErrPinNotFound, apperr.ErrNotFound},
	} {
		if !errors.Is(c.err, c.kind) {
			t.Errorf("%v does not wrap %v", c.err, c.kind)
		}
	}
}

func TestChangeKindsOnlyGrowAtTheEnd(t *testing.T) {
	kinds := []store.ChangeKind{store.MessageInserted, store.RoomInserted, store.EditInserted, store.ReactionChanged, store.PinInserted}
	for i, k := range kinds {
		if int(k) != i+1 {
			t.Fatalf("change kind %d = %d, want %d: kinds are stored in work records", i, k, i+1)
		}
	}
}

func TestReactionKeyOf(t *testing.T) {
	r := domain.Reaction{Room: 42, Thread: 7, Seq: 3, User: "bob", Emoji: "👍"}
	if got, want := store.ReactionKeyOf(r), (store.MsgKey{Room: 42, Thread: 7, Seq: 3}); got != want {
		t.Fatalf("ReactionKeyOf = %+v, want %+v", got, want)
	}
}

func assertField(t *testing.T, op string, err error, field string) {
	t.Helper()
	switch {
	case field == "" && err != nil:
		t.Fatalf("%s = %v, want nil", op, err)
	case field != "" && (!errors.Is(err, apperr.ErrInvalidArgument) || !strings.HasSuffix(err.Error(), ": "+field)):
		t.Fatalf("%s = %v, want ErrInvalidArgument naming %q", op, err, field)
	}
}

func TestValidateReaction(t *testing.T) {
	good := domain.Reaction{Room: 1, Seq: 1, Tenant: "acme", User: "bob", Emoji: "👍"}
	for name, c := range map[string]struct {
		mutate func(*domain.Reaction)
		field  string
	}{
		"valid":                   {func(*domain.Reaction) {}, ""},
		"empty emoji is not its business": {func(r *domain.Reaction) { r.Emoji = "" }, ""},
		"zero room":               {func(r *domain.Reaction) { r.Room = 0 }, "room"},
		"zero seq":                {func(r *domain.Reaction) { r.Seq = 0 }, "seq"},
		"empty user":              {func(r *domain.Reaction) { r.User = "" }, "user"},
		"user with a dot":         {func(r *domain.Reaction) { r.User = "b.b" }, "user"},
		"empty tenant":            {func(r *domain.Reaction) { r.Tenant = "" }, "tenant"},
	} {
		r := good
		c.mutate(&r)
		assertField(t, "ValidateReaction("+name+")", store.ValidateReaction(r), c.field)
	}
	assertField(t, "ValidateReactionTarget(ok)", store.ValidateReactionTarget(store.MsgKey{Room: 1, Seq: 1}, "bob"), "")
	assertField(t, "ValidateReactionTarget(zero seq)", store.ValidateReactionTarget(store.MsgKey{Room: 1}, "bob"), "seq")
	assertField(t, "ValidateReactionTarget(bad user)", store.ValidateReactionTarget(store.MsgKey{Room: 1, Seq: 1}, "b b"), "user")
}

func TestValidatePinAction(t *testing.T) {
	good := domain.PinAction{Room: 1, PV: 1, Op: domain.PinOpPin, Seq: 1, By: "bob"}
	for name, c := range map[string]struct {
		mutate func(*domain.PinAction)
		field  string
	}{
		"pin":                     {func(*domain.PinAction) {}, ""},
		"unpin":                   {func(a *domain.PinAction) { a.Op = domain.PinOpUnpin }, ""},
		"max int64 version":       {func(a *domain.PinAction) { a.PV = math.MaxInt64 }, ""},
		"zero room":               {func(a *domain.PinAction) { a.Room = 0 }, "room"},
		"zero version":            {func(a *domain.PinAction) { a.PV = 0 }, "pin version"},
		"version above max int64": {func(a *domain.PinAction) { a.PV = math.MaxInt64 + 1 }, "pin version"},
		"zero op":                 {func(a *domain.PinAction) { a.Op = 0 }, "pin op"},
		"op past unpin":           {func(a *domain.PinAction) { a.Op = domain.PinOpUnpin + 1 }, "pin op"},
		"zero seq":                {func(a *domain.PinAction) { a.Seq = 0 }, "seq"},
		"bad user":                {func(a *domain.PinAction) { a.By = "b.b" }, "user"},
	} {
		a := good
		c.mutate(&a)
		assertField(t, "ValidatePinAction("+name+")", store.ValidatePinAction(a), c.field)
	}
}

func TestValidateVersionBump(t *testing.T) {
	for _, c := range []struct {
		base, next uint64
		ok         bool
	}{
		{0, 1, true}, {1, 2, true}, {5, math.MaxInt64, true},
		{0, 0, false}, {2, 2, false}, {3, 2, false}, {0, math.MaxInt64 + 1, false},
	} {
		field := "version"
		if c.ok {
			field = ""
		}
		assertField(t, "ValidateVersionBump", store.ValidateVersionBump(c.base, c.next), field)
	}
}
```

(gofmt căn lại cột của hai map literal.)

`apps/core/internal/store/write_contract_test.go`:
- trong `portMethods`, thay:

```go
	"Hidden.HiddenIn":             "read",
}
```

bằng:

```go
	"Hidden.HiddenIn":             "read",

	"Reactions.Set":                  "version-bump",
	"Reactions.Remove":               "version-bump",
	"Reactions.Get":                  "read",
	"Reactions.Count":                "read",
	"Reactions.Between":              "read",
	"ReactionSummaries.SetReactions": "cas",
	"Pins.Append":                    "insert-unique",
	"Pins.At":                        "read",
	"Pins.After":                     "read",
	"Pins.Between":                   "read",
	"PinProjector.PinState":          "read",
	"PinProjector.ApplyPins":         "cas",
}
```

(dòng trống tách nhóm để gofmt không căn lại nhóm cũ.)

- thay:

```go
		reflect.TypeFor[store.Hidden](),
	}
```

bằng:

```go
		reflect.TypeFor[store.Hidden](),
		reflect.TypeFor[store.Reactions](),
		reflect.TypeFor[store.ReactionSummaries](),
		reflect.TypeFor[store.Pins](),
		reflect.TypeFor[store.PinProjector](),
	}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/"`
Expected: FAIL biên dịch: `undefined: store.ErrStaleRead`, `undefined: store.ReactionChanged`, `undefined: store.PinInserted`, `undefined: store.ReactionKeyOf`, `undefined: store.ValidateReaction`, `undefined: store.ValidatePinAction`, `undefined: store.ValidateVersionBump`, `undefined: store.Reactions`, `undefined: store.PinProjector`.

**Step 3: Code store**

`apps/core/internal/store/reaction.go`:

```go
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const MaxReactionScan = 1000

var (
	ErrStaleRead         = fmt.Errorf("read is behind a witnessed write: %w", apperr.ErrUnavailable)
	ErrReactionContended = fmt.Errorf("reaction write contended: %w", apperr.ErrUnavailable)
)

type Witness struct {
	User string
	N    uint32
}

type Reactions interface {
	Set(ctx context.Context, r domain.Reaction) (domain.Reaction, bool, error)
	Remove(ctx context.Context, key MsgKey, user string, at time.Time) (domain.Reaction, bool, error)
	Get(ctx context.Context, key MsgKey, user string) (domain.Reaction, bool, error)
	Count(ctx context.Context, key MsgKey, witnesses []Witness) ([]domain.ReactionCount, error)
	Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Reaction, error)
}

type ReactionSummaries interface {
	SetReactions(ctx context.Context, key MsgKey, base uint64, s domain.ReactionSummary) (bool, error)
}

func ReactionKeyOf(r domain.Reaction) MsgKey {
	return MsgKey{Room: r.Room, Thread: r.Thread, Seq: r.Seq}
}

func ValidateReaction(r domain.Reaction) error {
	if err := ValidateReactionTarget(ReactionKeyOf(r), r.User); err != nil {
		return err
	}
	if r.Tenant == "" {
		return invalid("tenant")
	}
	return nil
}

func ValidateReactionTarget(key MsgKey, user string) error {
	if err := key.Validate(); err != nil {
		return err
	}
	return domain.ValidUser(user)
}
```

`apps/core/internal/store/pin.go`:

```go
package store

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const MaxPinScan = 1000

var (
	ErrPinExists   = fmt.Errorf("pin version %w", apperr.ErrAlreadyExists)
	ErrPinNotFound = fmt.Errorf("pin action %w", apperr.ErrNotFound)
)

type Pins interface {
	Append(ctx context.Context, a domain.PinAction) error
	At(ctx context.Context, room, pv uint64) (domain.PinAction, error)
	After(ctx context.Context, room, pv uint64, limit int) ([]domain.PinAction, error)
	Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.PinAction, error)
}

type PinProjector interface {
	PinState(ctx context.Context, room uint64) (domain.PinState, error)
	ApplyPins(ctx context.Context, room, base uint64, s domain.PinState) (bool, error)
}

func ValidatePinAction(a domain.PinAction) error {
	switch {
	case a.Room == 0:
		return invalid("room")
	case a.PV == 0 || a.PV > math.MaxInt64:
		return invalid("pin version")
	case a.Op != domain.PinOpPin && a.Op != domain.PinOpUnpin:
		return invalid("pin op")
	case a.Seq == 0:
		return invalid("seq")
	default:
		return domain.ValidUser(a.By)
	}
}
```

`apps/core/internal/store/validate.go`:
- import thêm `"math"` (sau `"fmt"`).
- thêm vào cuối file (trước `func invalid`):

```go
func ValidateVersionBump(base, next uint64) error {
	if next <= base || next > math.MaxInt64 {
		return invalid("version")
	}
	return nil
}
```

`apps/core/internal/store/feed.go`, thay:

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

bằng:

```go
const (
	MessageInserted ChangeKind = iota + 1
	RoomInserted
	EditInserted
	ReactionChanged
	PinInserted
)

type Change struct {
	Kind        ChangeKind
	Msg         domain.Message
	Room        domain.Room
	Edit        domain.Edit
	Reaction    domain.Reaction
	Pin         domain.PinAction
	CommittedAt time.Time
	Position    Position
}
```

`apps/core/internal/work/record.go`, trong `RecordOf` thay:

```go
	case store.EditInserted:
		r.Room, r.Thread, r.Seq, r.Version = c.Edit.Room, c.Edit.Thread, c.Edit.Seq, c.Edit.Version
	}
	return r
```

bằng:

```go
	case store.EditInserted:
		r.Room, r.Thread, r.Seq, r.Version = c.Edit.Room, c.Edit.Thread, c.Edit.Seq, c.Edit.Version
	case store.ReactionChanged:
		r.Room, r.Thread, r.Seq, r.Version = c.Reaction.Room, c.Reaction.Thread, c.Reaction.Seq, c.Reaction.N
	case store.PinInserted:
		r.Room, r.Seq = c.Pin.Room, c.Pin.PV
	}
	return r
```

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/ ./apps/core/internal/work/..."`
Expected: PASS (`TestKnownKindsAreTheThreeChangeKinds` vẫn đúng: `KnownKind(4)`, `KnownKind(5)` vẫn false tới Task 7).

**Step 4: Test contract + memstore**

`apps/core/internal/store/storetest/reaction_cases.go`:

```go
package storetest

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type ReactableMessages interface {
	store.Messages
	store.ReactionSummaries
}

type reactionStores struct {
	msgs      ReactableMessages
	reactions store.Reactions
}

type reactionCase struct {
	name string
	run  func(t *testing.T, s reactionStores)
}

func RunReactions(t *testing.T, open func(t *testing.T) (ReactableMessages, store.Reactions)) {
	t.Helper()
	for _, c := range slices.Concat(reactionSetCases(), reactionReadCases(), summaryCases()) {
		t.Run(c.name, func(t *testing.T) {
			msgs, reactions := open(t)
			c.run(t, reactionStores{msgs: msgs, reactions: reactions})
		})
	}
}

func reactionOf(room, thread, seq uint64, user, emoji string) domain.Reaction {
	return domain.Reaction{Room: room, Thread: thread, Seq: seq, Tenant: tenant, User: user, Emoji: emoji, At: baseTime}
}

func reactAt(room, thread, seq uint64, user, emoji string, after time.Duration) domain.Reaction {
	r := reactionOf(room, thread, seq, user, emoji)
	r.At = baseTime.Add(after)
	return r
}

func changed(r domain.Reaction, prev string, n uint32) domain.Reaction {
	r.Prev, r.N = prev, n
	return r
}

func mustSet(t *testing.T, s store.Reactions, r domain.Reaction, wantChanged bool) domain.Reaction {
	t.Helper()
	got, ok, err := s.Set(t.Context(), r)
	if err != nil || ok != wantChanged {
		t.Fatalf("Set(%q by %q on %d/%d/%d) = %+v, %v, %v; want changed %v", r.Emoji, r.User, r.Room, r.Thread, r.Seq, got, ok, err, wantChanged)
	}
	return got
}

func mustRemove(t *testing.T, s store.Reactions, key store.MsgKey, user string, at time.Time, wantChanged bool) domain.Reaction {
	t.Helper()
	got, ok, err := s.Remove(t.Context(), key, user, at)
	if err != nil || ok != wantChanged {
		t.Fatalf("Remove(%q on %+v) = %+v, %v, %v; want changed %v", user, key, got, ok, err, wantChanged)
	}
	return got
}

func sameReaction(a, b domain.Reaction) bool {
	at, bt := a.At, b.At
	a.At, b.At = time.Time{}, time.Time{}
	return a == b && at.Equal(bt)
}

func assertReactions(t *testing.T, op string, got, want []domain.Reaction) {
	t.Helper()
	if !slices.EqualFunc(got, want, sameReaction) {
		t.Fatalf("%s = %+v,\nwant %+v", op, got, want)
	}
}

func assertStoredReaction(t *testing.T, s store.Reactions, want domain.Reaction) {
	t.Helper()
	got, ok, err := s.Get(t.Context(), store.ReactionKeyOf(want), want.User)
	if err != nil || !ok {
		t.Fatalf("Get(%q on %+v) = %+v, %v, %v; want %+v", want.User, store.ReactionKeyOf(want), got, ok, err, want)
	}
	assertReactions(t, "Get", []domain.Reaction{got}, []domain.Reaction{want})
}

func assertNoReaction(t *testing.T, s store.Reactions, key store.MsgKey, user string) {
	t.Helper()
	if got, ok, err := s.Get(t.Context(), key, user); ok || err != nil {
		t.Fatalf("Get(%q on %+v) = %+v, %v, %v; want nothing", user, key, got, ok, err)
	}
}

func assertCounts(t *testing.T, s store.Reactions, key store.MsgKey, want []domain.ReactionCount) {
	t.Helper()
	got, err := s.Count(t.Context(), key, nil)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("Count(%+v) = %v, %v; want %v", key, got, err, want)
	}
}
```

`apps/core/internal/store/storetest/reaction_set_cases.go`:

```go
package storetest

import (
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func reactionSetCases() []reactionCase {
	return []reactionCase{
		{"set stores the emoji as change 1 with no previous emoji", reactSetFirst},
		{"set of the same emoji is a no-op that returns the stored reaction", reactSetSame},
		{"set of another emoji replaces it and keeps the previous one", reactSetOther},
		{"remove leaves a tombstone and a second remove is a no-op", reactRemove},
		{"remove without a reaction stores nothing", reactRemoveMissing},
		{"set after remove counts on from the tombstone", reactSetAfterRemove},
		{"invalid reactions and limits are rejected", reactInvalid},
	}
}

func reactSetFirst(t *testing.T, s reactionStores) {
	r := reactionOf(roomA, mainThread, 1, "alice", "👍")
	got := mustSet(t, s.reactions, r, true)
	want := changed(r, "", 1)
	assertReactions(t, "Set", []domain.Reaction{got}, []domain.Reaction{want})
	assertStoredReaction(t, s.reactions, want)
	assertNoReaction(t, s.reactions, msgKey(roomA, mainThread, 1), "bob")
	assertNoReaction(t, s.reactions, msgKey(roomA, mainThread, 2), "alice")
	assertNoReaction(t, s.reactions, msgKey(roomA, sideThread, 1), "alice")
}

func reactSetSame(t *testing.T, s reactionStores) {
	first := mustSet(t, s.reactions, reactionOf(roomA, mainThread, 1, "alice", "👍"), true)
	got := mustSet(t, s.reactions, reactAt(roomA, mainThread, 1, "alice", "👍", time.Minute), false)
	assertReactions(t, "Set(same emoji)", []domain.Reaction{got}, []domain.Reaction{first})
	assertStoredReaction(t, s.reactions, first)
}

func reactSetOther(t *testing.T, s reactionStores) {
	mustSet(t, s.reactions, reactionOf(roomA, mainThread, 1, "alice", "👍"), true)
	heart := reactAt(roomA, mainThread, 1, "alice", "❤️", time.Minute)
	got := mustSet(t, s.reactions, heart, true)
	want := changed(heart, "👍", 2)
	assertReactions(t, "Set(other emoji)", []domain.Reaction{got}, []domain.Reaction{want})
	assertStoredReaction(t, s.reactions, want)
}

func reactRemove(t *testing.T, s reactionStores) {
	set := mustSet(t, s.reactions, reactionOf(roomA, sideThread, 4, "alice", "👍"), true)
	key, at := store.ReactionKeyOf(set), baseTime.Add(time.Hour)
	got := mustRemove(t, s.reactions, key, "alice", at, true)
	want := set
	want.Emoji, want.Prev, want.N, want.At = "", "👍", 2, at
	assertReactions(t, "Remove", []domain.Reaction{got}, []domain.Reaction{want})
	again := mustRemove(t, s.reactions, key, "alice", at.Add(time.Hour), false)
	assertReactions(t, "Remove(again)", []domain.Reaction{again}, []domain.Reaction{want})
	assertStoredReaction(t, s.reactions, want)
}

func reactRemoveMissing(t *testing.T, s reactionStores) {
	key := msgKey(roomA, mainThread, 1)
	if got := mustRemove(t, s.reactions, key, "bob", baseTime, false); got != (domain.Reaction{}) {
		t.Fatalf("Remove(missing) = %+v, want the zero reaction", got)
	}
	assertNoReaction(t, s.reactions, key, "bob")
}

func reactSetAfterRemove(t *testing.T, s reactionStores) {
	key := msgKey(roomA, mainThread, 1)
	mustSet(t, s.reactions, reactionOf(roomA, mainThread, 1, "alice", "👍"), true)
	mustRemove(t, s.reactions, key, "alice", baseTime.Add(time.Second), true)
	r := reactAt(roomA, mainThread, 1, "alice", "👍", time.Minute)
	got := mustSet(t, s.reactions, r, true)
	assertReactions(t, "Set(after remove)", []domain.Reaction{got}, []domain.Reaction{changed(r, "", 3)})
}

func reactInvalid(t *testing.T, s reactionStores) {
	for name, mutate := range map[string]func(*domain.Reaction){
		"zero room":         func(r *domain.Reaction) { r.Room = 0 },
		"zero seq":          func(r *domain.Reaction) { r.Seq = 0 },
		"user with a dot":   func(r *domain.Reaction) { r.User = "a.b" },
		"empty user":        func(r *domain.Reaction) { r.User = "" },
		"empty tenant":      func(r *domain.Reaction) { r.Tenant = "" },
		"empty emoji":       func(r *domain.Reaction) { r.Emoji = "" },
		"emoji of 33 bytes": func(r *domain.Reaction) { r.Emoji = strings.Repeat("a", 33) },
		"control emoji":     func(r *domain.Reaction) { r.Emoji = "a\nb" },
	} {
		r := reactionOf(roomA, mainThread, 1, "alice", "👍")
		mutate(&r)
		_, _, err := s.reactions.Set(t.Context(), r)
		assertErrorIs(t, "Set("+name+")", err, apperr.ErrInvalidArgument)
	}
	_, _, err := s.reactions.Remove(t.Context(), msgKey(roomA, mainThread, 0), "alice", baseTime)
	assertErrorIs(t, "Remove(zero seq)", err, apperr.ErrInvalidArgument)
	_, _, err = s.reactions.Remove(t.Context(), msgKey(roomA, mainThread, 1), "a.b", baseTime)
	assertErrorIs(t, "Remove(bad user)", err, apperr.ErrInvalidArgument)
	_, err = s.reactions.Count(t.Context(), msgKey(roomA, mainThread, 0), nil)
	assertErrorIs(t, "Count(zero seq)", err, apperr.ErrInvalidArgument)
	for _, limit := range []int{0, store.MaxReactionScan + 1} {
		_, err := s.reactions.Between(t.Context(), roomA, baseTime, baseTime.Add(time.Hour), limit)
		assertErrorIs(t, "Between(bad limit)", err, apperr.ErrInvalidArgument)
	}
	assertNoReaction(t, s.reactions, msgKey(roomA, mainThread, 1), "alice")
}
```

`apps/core/internal/store/storetest/reaction_read_cases.go`:

```go
package storetest

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func reactionReadCases() []reactionCase {
	return []reactionCase{
		{"count returns live emojis of that message only, sorted by count then emoji", reactCount},
		{"count fails with a stale read while a witness is behind", reactWitness},
		{"between returns reactions and tombstones of a room by time then key", reactBetween},
		{"cancelled context writes nothing", reactCancelled},
	}
}

func setAll(t *testing.T, s store.Reactions, rs ...domain.Reaction) {
	t.Helper()
	for _, r := range rs {
		mustSet(t, s, r, true)
	}
}

func reactCount(t *testing.T, s reactionStores) {
	setAll(t, s.reactions,
		reactionOf(roomA, mainThread, 1, "alice", "👍"), reactionOf(roomA, mainThread, 1, "bob", "👍"),
		reactionOf(roomA, mainThread, 1, "carol", "❤️"), reactionOf(roomA, mainThread, 1, "dave", "😂"),
		reactionOf(roomA, mainThread, 1, "erin", "$e"), reactionOf(roomA, mainThread, 1, "frank", "a.b"),
		reactionOf(roomA, mainThread, 2, "alice", "👍"), reactionOf(roomA, sideThread, 1, "bob", "❤️"),
		reactionOf(roomB, mainThread, 1, "carol", "👍"),
	)
	key := msgKey(roomA, mainThread, 1)
	mustRemove(t, s.reactions, key, "dave", baseTime.Add(time.Second), true)
	assertCounts(t, s.reactions, key, []domain.ReactionCount{
		{Emoji: "👍", Count: 2}, {Emoji: "$e", Count: 1}, {Emoji: "a.b", Count: 1}, {Emoji: "❤️", Count: 1},
	})
	assertCounts(t, s.reactions, msgKey(roomA, sideThread, 1), []domain.ReactionCount{{Emoji: "❤️", Count: 1}})
	assertCounts(t, s.reactions, msgKey(roomA, mainThread, 9), nil)
	assertStoredReaction(t, s.reactions, changed(reactionOf(roomA, mainThread, 1, "erin", "$e"), "", 1))
}

func reactWitness(t *testing.T, s reactionStores) {
	key := msgKey(roomA, mainThread, 1)
	mustSet(t, s.reactions, reactionOf(roomA, mainThread, 1, "alice", "👍"), true)
	mustSet(t, s.reactions, reactionOf(roomA, mainThread, 1, "bob", "❤️"), true)
	mustRemove(t, s.reactions, key, "bob", baseTime.Add(time.Second), true)
	got, err := s.reactions.Count(t.Context(), key, []store.Witness{{User: "alice", N: 1}, {User: "bob", N: 2}, {User: "alice", N: 1}})
	if err != nil || !slices.Equal(got, []domain.ReactionCount{{Emoji: "👍", Count: 1}}) {
		t.Fatalf("Count(witnessed) = %v, %v; want [{👍 1}]", got, err)
	}
	for name, ws := range map[string][]store.Witness{
		"behind":            {{User: "alice", N: 2}},
		"missing":           {{User: "carol", N: 1}},
		"one of two behind": {{User: "alice", N: 1}, {User: "bob", N: 3}},
	} {
		_, err := s.reactions.Count(t.Context(), key, ws)
		assertErrorIs(t, "Count("+name+")", err, store.ErrStaleRead)
		assertErrorIs(t, "Count("+name+")", err, apperr.ErrUnavailable)
	}
}

func reactBetween(t *testing.T, s reactionStores) {
	from, to := baseTime.Add(time.Second), baseTime.Add(3*time.Second)
	mustSet(t, s.reactions, reactAt(roomA, mainThread, 4, "alice", "👍", 0), true)
	atFrom := mustSet(t, s.reactions, reactAt(roomA, mainThread, 5, "alice", "👍", time.Second), true)
	lowKey := mustSet(t, s.reactions, reactAt(roomA, mainThread, 2, "bob", "👍", 2*time.Second), true)
	highBob := mustSet(t, s.reactions, reactAt(roomA, mainThread, 3, "bob", "👍", 2*time.Second), true)
	highCarol := mustSet(t, s.reactions, reactAt(roomA, mainThread, 3, "carol", "❤️", 2*time.Second), true)
	mustSet(t, s.reactions, reactAt(roomA, sideThread, 1, "dave", "😂", 0), true)
	gone := mustRemove(t, s.reactions, msgKey(roomA, sideThread, 1), "dave", to, true)
	mustSet(t, s.reactions, reactAt(roomA, mainThread, 1, "erin", "👍", 4*time.Second), true)
	other := mustSet(t, s.reactions, reactAt(roomB, mainThread, 1, "alice", "👍", 2*time.Second), true)
	want := []domain.Reaction{atFrom, lowKey, highBob, highCarol, gone}
	cases := []struct {
		room     uint64
		from, to time.Time
		limit    int
		want     []domain.Reaction
	}{
		{roomA, from, to, store.MaxReactionScan, want},
		{roomA, from, to, 2, want[:2]},
		{roomB, from, to, 10, []domain.Reaction{other}},
		{roomA, to.Add(time.Hour), to.Add(2 * time.Hour), 10, nil},
	}
	for _, c := range cases {
		got, err := s.reactions.Between(t.Context(), c.room, c.from, c.to, c.limit)
		if err != nil {
			t.Fatalf("Between(%d, limit %d): %v", c.room, c.limit, err)
		}
		assertReactions(t, fmt.Sprintf("Between(%d, limit %d)", c.room, c.limit), got, c.want)
	}
}

func reactCancelled(t *testing.T, s reactionStores) {
	m := msg(roomA, mainThread, 1)
	mustInsert(t, s.msgs, []domain.Message{m})
	ctx, key := cancelledContext(t), store.KeyOf(m)
	_, _, err := s.reactions.Set(ctx, reactionOf(roomA, mainThread, 1, "alice", "👍"))
	assertErrorIs(t, "Set", err, context.Canceled)
	_, _, err = s.reactions.Remove(ctx, key, "alice", baseTime)
	assertErrorIs(t, "Remove", err, context.Canceled)
	_, _, err = s.reactions.Get(ctx, key, "alice")
	assertErrorIs(t, "Get", err, context.Canceled)
	_, err = s.reactions.Count(ctx, key, nil)
	assertErrorIs(t, "Count", err, context.Canceled)
	_, err = s.msgs.SetReactions(ctx, key, 0, domain.ReactionSummary{Version: 1})
	assertErrorIs(t, "SetReactions", err, context.Canceled)
	assertNoReaction(t, s.reactions, key, "alice")
	assertStored(t, s.msgs, m)
}
```

`apps/core/internal/store/storetest/summary_cases.go`:

```go
package storetest

import (
	"math"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func summaryCases() []reactionCase {
	return []reactionCase{
		{"set reactions writes only over the expected version", summaryCAS},
		{"set reactions on a missing message writes nothing", summaryMissing},
		{"insert never stores a reaction summary", summaryNotInserted},
		{"set reactions needs a version above the base", summaryInvalid},
	}
}

func summary(version uint64, counts ...domain.ReactionCount) domain.ReactionSummary {
	return domain.ReactionSummary{Counts: counts, Version: version}
}

func withReactions(m domain.Message, s domain.ReactionSummary) domain.Message {
	m.Reactions = s
	return m
}

func mustSetReactions(t *testing.T, s store.ReactionSummaries, key store.MsgKey, base uint64, sum domain.ReactionSummary, want bool) {
	t.Helper()
	ok, err := s.SetReactions(t.Context(), key, base, sum)
	if err != nil || ok != want {
		t.Fatalf("SetReactions(%+v, base %d, v%d) = %v, %v; want %v", key, base, sum.Version, ok, err, want)
	}
}

func summaryCAS(t *testing.T, s reactionStores) {
	m, other := msg(roomA, mainThread, 1), msg(roomA, mainThread, 2)
	mustInsert(t, s.msgs, []domain.Message{m, other})
	key := store.KeyOf(m)
	first := summary(1, domain.ReactionCount{Emoji: "👍", Count: 2}, domain.ReactionCount{Emoji: "$e", Count: 1})
	mustSetReactions(t, s.msgs, key, 0, first, true)
	assertStored(t, s.msgs, withReactions(m, first), other)
	mustSetReactions(t, s.msgs, key, 0, summary(1, domain.ReactionCount{Emoji: "❤️", Count: 9}), false)
	mustSetReactions(t, s.msgs, key, 2, summary(3), false)
	assertStored(t, s.msgs, withReactions(m, first), other)
	second := summary(2, domain.ReactionCount{Emoji: "👍", Count: 3})
	mustSetReactions(t, s.msgs, key, 1, second, true)
	cleared := summary(3)
	mustSetReactions(t, s.msgs, key, 2, cleared, true)
	page, err := s.msgs.Page(t.Context(), store.PageQuery{Room: roomA, Thread: mainThread, Anchor: store.Latest, Limit: 10})
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	assertMessages(t, page, []domain.Message{withReactions(m, cleared), other})
}

func summaryMissing(t *testing.T, s reactionStores) {
	key := msgKey(roomA, mainThread, 1)
	mustSetReactions(t, s.msgs, key, 0, summary(1, domain.ReactionCount{Emoji: "👍", Count: 1}), false)
	if got, err := s.msgs.Find(t.Context(), roomA, []store.MsgKey{key}); err != nil || len(got) != 0 {
		t.Fatalf("Find after SetReactions on a missing message = %+v, %v; want nothing", got, err)
	}
}

func summaryNotInserted(t *testing.T, s reactionStores) {
	m := msg(roomA, mainThread, 1)
	mustInsert(t, s.msgs, []domain.Message{withReactions(m, summary(4, domain.ReactionCount{Emoji: "👍", Count: 1}))})
	assertStored(t, s.msgs, m)
	mustSetReactions(t, s.msgs, store.KeyOf(m), 0, summary(1, domain.ReactionCount{Emoji: "👍", Count: 1}), true)
}

func summaryInvalid(t *testing.T, s reactionStores) {
	m := msg(roomA, mainThread, 1)
	mustInsert(t, s.msgs, []domain.Message{m})
	key := store.KeyOf(m)
	for name, c := range map[string]struct {
		key  store.MsgKey
		base uint64
		sum  domain.ReactionSummary
	}{
		"same version":            {key, 1, summary(1)},
		"lower version":           {key, 2, summary(1)},
		"zero version":            {key, 0, summary(0)},
		"version above max int64": {key, 0, summary(math.MaxInt64 + 1)},
		"zero seq":                {msgKey(roomA, mainThread, 0), 0, summary(1)},
	} {
		_, err := s.msgs.SetReactions(t.Context(), c.key, c.base, c.sum)
		assertErrorIs(t, "SetReactions("+name+")", err, apperr.ErrInvalidArgument)
	}
	assertStored(t, s.msgs, m)
}
```

`apps/core/internal/store/storetest/pin_cases.go`:

```go
package storetest

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type PinnableRooms interface {
	store.Rooms
	store.PinProjector
}

type pinStores struct {
	rooms PinnableRooms
	pins  store.Pins
}

type pinCase struct {
	name string
	run  func(t *testing.T, s pinStores)
}

func RunPins(t *testing.T, open func(t *testing.T) (PinnableRooms, store.Pins)) {
	t.Helper()
	for _, c := range slices.Concat(pinFactCases(), pinStateCases()) {
		t.Run(c.name, func(t *testing.T) {
			rooms, pins := open(t)
			c.run(t, pinStores{rooms: rooms, pins: pins})
		})
	}
}

func pinFact(room uint64, pv uint32, op domain.PinOp, seq uint64) domain.PinAction {
	return domain.PinAction{
		Room: room, PV: uint64(pv), Tenant: tenant, Op: op, Seq: seq, By: "alice",
		At: baseTime.Add(time.Duration(pv) * time.Second),
	}
}

func pinOf(seq uint64, pv uint32) domain.Pin {
	a := pinFact(roomA, pv, domain.PinOpPin, seq)
	return domain.Pin{Thread: a.Thread, Seq: a.Seq, By: a.By, At: a.At, PV: a.PV}
}

func mustAppendPins(t *testing.T, s store.Pins, facts ...domain.PinAction) {
	t.Helper()
	for _, a := range facts {
		if err := s.Append(t.Context(), a); err != nil {
			t.Fatalf("Append(room %d v%d): %v", a.Room, a.PV, err)
		}
	}
}

func samePinAction(a, b domain.PinAction) bool {
	at, bt := a.At, b.At
	a.At, b.At = time.Time{}, time.Time{}
	return a == b && at.Equal(bt)
}

func samePin(a, b domain.Pin) bool {
	at, bt := a.At, b.At
	a.At, b.At = time.Time{}, time.Time{}
	return a == b && at.Equal(bt)
}

func assertPinActions(t *testing.T, op string, got, want []domain.PinAction) {
	t.Helper()
	if !slices.EqualFunc(got, want, samePinAction) {
		t.Fatalf("%s = %+v,\nwant %+v", op, got, want)
	}
}

func assertPinAt(t *testing.T, s store.Pins, want domain.PinAction) {
	t.Helper()
	got, err := s.At(t.Context(), want.Room, want.PV)
	if err != nil {
		t.Fatalf("At(room %d v%d): %v", want.Room, want.PV, err)
	}
	assertPinActions(t, "At", []domain.PinAction{got}, []domain.PinAction{want})
}

func assertPinState(t *testing.T, s store.PinProjector, room uint64, want domain.PinState) {
	t.Helper()
	got, err := s.PinState(t.Context(), room)
	if err != nil || got.Version != want.Version || !slices.EqualFunc(got.Pins, want.Pins, samePin) {
		t.Fatalf("PinState(%d) = %+v, %v; want %+v", room, got, err, want)
	}
}

func mustApplyPins(t *testing.T, s store.PinProjector, room, base uint64, st domain.PinState, want bool) {
	t.Helper()
	ok, err := s.ApplyPins(t.Context(), room, base, st)
	if err != nil || ok != want {
		t.Fatalf("ApplyPins(%d, base %d, v%d) = %v, %v; want %v", room, base, st.Version, ok, err, want)
	}
}
```

`apps/core/internal/store/storetest/pin_fact_cases.go`:

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

func pinFactCases() []pinCase {
	return []pinCase{
		{"append then read each fact back by version", pinsAppendAt},
		{"append of an existing version fails and keeps the first fact", pinsAppendExisting},
		{"after ascends past a version up to the limit in that room only", pinsAfter},
		{"between returns the room facts in a time range by time then version", pinsBetween},
		{"invalid facts and limits are rejected", pinsInvalid},
	}
}

func pinsAppendAt(t *testing.T, s pinStores) {
	first, second := pinFact(roomA, 1, domain.PinOpPin, 3), pinFact(roomA, 2, domain.PinOpUnpin, 3)
	second.Thread = sideThread
	mustAppendPins(t, s.pins, first, second)
	assertPinAt(t, s.pins, first)
	assertPinAt(t, s.pins, second)
	_, err := s.pins.At(t.Context(), roomA, 3)
	assertErrorIs(t, "At(missing version)", err, store.ErrPinNotFound)
	_, err = s.pins.At(t.Context(), roomB, 1)
	assertErrorIs(t, "At(other room)", err, apperr.ErrNotFound)
}

func pinsAppendExisting(t *testing.T, s pinStores) {
	first := pinFact(roomA, 1, domain.PinOpPin, 3)
	mustAppendPins(t, s.pins, first)
	rival := first
	rival.By, rival.Seq = "bob", 9
	err := s.pins.Append(t.Context(), rival)
	assertErrorIs(t, "Append(existing version)", err, store.ErrPinExists)
	assertErrorIs(t, "Append(existing version)", err, apperr.ErrAlreadyExists)
	assertPinAt(t, s.pins, first)
}

func pinsAfter(t *testing.T, s pinStores) {
	all := make([]domain.PinAction, 0, 5)
	for pv := uint32(1); pv <= 5; pv++ {
		all = append(all, pinFact(roomA, pv, domain.PinOpPin, uint64(pv)))
	}
	other := pinFact(roomB, 1, domain.PinOpPin, 1)
	mustAppendPins(t, s.pins, all...)
	mustAppendPins(t, s.pins, other)
	cases := []struct {
		room, after uint64
		limit       int
		want        []domain.PinAction
	}{
		{roomA, 0, store.MaxPinScan, all},
		{roomA, 2, 2, all[2:4]},
		{roomA, 5, 10, nil},
		{roomA, math.MaxUint64, 1, nil},
		{roomB, 0, 10, []domain.PinAction{other}},
	}
	for _, c := range cases {
		got, err := s.pins.After(t.Context(), c.room, c.after, c.limit)
		if err != nil {
			t.Fatalf("After(%d, %d, %d): %v", c.room, c.after, c.limit, err)
		}
		assertPinActions(t, fmt.Sprintf("After(%d, %d, %d)", c.room, c.after, c.limit), got, c.want)
	}
}

func pinsBetween(t *testing.T, s pinStores) {
	from, to := baseTime.Add(time.Second), baseTime.Add(3*time.Second)
	atFrom, mid, atTo, after := pinFact(roomA, 1, domain.PinOpPin, 1), pinFact(roomA, 2, domain.PinOpPin, 2), pinFact(roomA, 3, domain.PinOpPin, 3), pinFact(roomA, 4, domain.PinOpPin, 4)
	sameTime, before, other := pinFact(roomA, 5, domain.PinOpUnpin, 2), pinFact(roomA, 6, domain.PinOpPin, 6), pinFact(roomB, 1, domain.PinOpPin, 1)
	sameTime.At, before.At, other.At = mid.At, baseTime, mid.At
	mustAppendPins(t, s.pins, after, other, atTo, before, sameTime, mid, atFrom)
	want := []domain.PinAction{atFrom, mid, sameTime, atTo}
	cases := []struct {
		room     uint64
		from, to time.Time
		limit    int
		want     []domain.PinAction
	}{
		{roomA, from, to, store.MaxPinScan, want},
		{roomA, from, to, 2, want[:2]},
		{roomB, from, to, 10, []domain.PinAction{other}},
		{roomA, to.Add(time.Hour), to.Add(2 * time.Hour), 10, nil},
	}
	for _, c := range cases {
		got, err := s.pins.Between(t.Context(), c.room, c.from, c.to, c.limit)
		if err != nil {
			t.Fatalf("Between(%d, limit %d): %v", c.room, c.limit, err)
		}
		assertPinActions(t, fmt.Sprintf("Between(%d, limit %d)", c.room, c.limit), got, c.want)
	}
}

func pinsInvalid(t *testing.T, s pinStores) {
	for name, mutate := range map[string]func(*domain.PinAction){
		"zero room":               func(a *domain.PinAction) { a.Room = 0 },
		"zero version":            func(a *domain.PinAction) { a.PV = 0 },
		"version above max int64": func(a *domain.PinAction) { a.PV = math.MaxInt64 + 1 },
		"zero op":                 func(a *domain.PinAction) { a.Op = 0 },
		"op past unpin":           func(a *domain.PinAction) { a.Op = domain.PinOpUnpin + 1 },
		"zero seq":                func(a *domain.PinAction) { a.Seq = 0 },
		"user with a dot":         func(a *domain.PinAction) { a.By = "a.b" },
	} {
		a := pinFact(roomA, 1, domain.PinOpPin, 3)
		mutate(&a)
		assertErrorIs(t, "Append("+name+")", s.pins.Append(t.Context(), a), apperr.ErrInvalidArgument)
	}
	for _, limit := range []int{0, store.MaxPinScan + 1} {
		_, err := s.pins.After(t.Context(), roomA, 0, limit)
		assertErrorIs(t, fmt.Sprintf("After(limit %d)", limit), err, apperr.ErrInvalidArgument)
		_, err = s.pins.Between(t.Context(), roomA, baseTime, baseTime.Add(time.Hour), limit)
		assertErrorIs(t, fmt.Sprintf("Between(limit %d)", limit), err, apperr.ErrInvalidArgument)
	}
	if got, err := s.pins.After(t.Context(), roomA, 0, store.MaxPinScan); err != nil || len(got) != 0 {
		t.Fatalf("After after invalid appends = %+v, %v; want nothing stored", got, err)
	}
}
```

`apps/core/internal/store/storetest/pin_state_cases.go`:

```go
package storetest

import (
	"context"
	"math"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func pinStateCases() []pinCase {
	return []pinCase{
		{"pin state of a new room is empty and of a missing room not found", pinStateEmpty},
		{"apply pins writes only over the expected version", pinStateCAS},
		{"apply pins needs a version above the base", pinStateInvalid},
		{"pins never change the room read", pinStateRoomUnchanged},
		{"cancelled context writes nothing", pinsCancelled},
	}
}

func pinStateEmpty(t *testing.T, s pinStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	assertPinState(t, s.rooms, roomA, domain.PinState{})
	_, err := s.rooms.PinState(t.Context(), roomB)
	assertErrorIs(t, "PinState(missing room)", err, domain.ErrRoomNotFound)
}

func pinStateCAS(t *testing.T, s pinStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	first := domain.PinState{Pins: []domain.Pin{pinOf(3, 1)}, Version: 1}
	mustApplyPins(t, s.rooms, roomA, 0, first, true)
	assertPinState(t, s.rooms, roomA, first)
	mustApplyPins(t, s.rooms, roomA, 0, domain.PinState{Pins: []domain.Pin{pinOf(9, 1)}, Version: 1}, false)
	mustApplyPins(t, s.rooms, roomA, 2, domain.PinState{Version: 3}, false)
	assertPinState(t, s.rooms, roomA, first)
	second := domain.PinState{Pins: []domain.Pin{pinOf(5, 2), pinOf(3, 1)}, Version: 2}
	mustApplyPins(t, s.rooms, roomA, 1, second, true)
	assertPinState(t, s.rooms, roomA, second)
	cleared := domain.PinState{Version: 3}
	mustApplyPins(t, s.rooms, roomA, 2, cleared, true)
	assertPinState(t, s.rooms, roomA, cleared)
	mustApplyPins(t, s.rooms, roomB, 0, first, false)
}

func pinStateInvalid(t *testing.T, s pinStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	for name, c := range map[string]struct {
		base uint64
		st   domain.PinState
	}{
		"same version":            {1, domain.PinState{Version: 1}},
		"lower version":           {2, domain.PinState{Version: 1}},
		"zero version":            {0, domain.PinState{}},
		"version above max int64": {0, domain.PinState{Version: math.MaxInt64 + 1}},
	} {
		_, err := s.rooms.ApplyPins(t.Context(), roomA, c.base, c.st)
		assertErrorIs(t, "ApplyPins("+name+")", err, apperr.ErrInvalidArgument)
	}
	assertPinState(t, s.rooms, roomA, domain.PinState{})
}

func pinStateRoomUnchanged(t *testing.T, s pinStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	mustApplyPins(t, s.rooms, roomA, 0, domain.PinState{Pins: []domain.Pin{pinOf(3, 1)}, Version: 1}, true)
	assertRoom(t, s.rooms, room)
}

func pinsCancelled(t *testing.T, s pinStores) {
	room, members := teamOf(roomA)
	mustCreate(t, s.rooms, room, members)
	ctx := cancelledContext(t)
	assertErrorIs(t, "Append", s.pins.Append(ctx, pinFact(roomA, 1, domain.PinOpPin, 3)), context.Canceled)
	_, err := s.pins.At(ctx, roomA, 1)
	assertErrorIs(t, "At", err, context.Canceled)
	_, err = s.pins.After(ctx, roomA, 0, 10)
	assertErrorIs(t, "After", err, context.Canceled)
	_, err = s.rooms.PinState(ctx, roomA)
	assertErrorIs(t, "PinState", err, context.Canceled)
	_, err = s.rooms.ApplyPins(ctx, roomA, 0, domain.PinState{Version: 1})
	assertErrorIs(t, "ApplyPins", err, context.Canceled)
	if got, err := s.pins.After(t.Context(), roomA, 0, 10); err != nil || len(got) != 0 {
		t.Fatalf("After after a cancelled Append = %+v, %v; want nothing stored", got, err)
	}
	assertPinState(t, s.rooms, roomA, domain.PinState{})
}
```

`apps/core/internal/store/memstore/memstore_test.go`, thêm vào cuối:

```go
func TestReactionsContract(t *testing.T) {
	storetest.RunReactions(t, func(*testing.T) (storetest.ReactableMessages, store.Reactions) {
		return memstore.NewMessages(), memstore.NewReactions()
	})
}

func TestPinsContract(t *testing.T) {
	storetest.RunPins(t, func(*testing.T) (storetest.PinnableRooms, store.Pins) {
		return memstore.NewRooms(), memstore.NewPins()
	})
}
```

**Step 5: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/..."`
Expected: FAIL biên dịch ở `memstore_test.go`: `undefined: memstore.NewReactions`, `undefined: memstore.NewPins`, `*memstore.Messages does not implement storetest.ReactableMessages (missing method SetReactions)`, `*memstore.Rooms does not implement storetest.PinnableRooms (missing method ApplyPins)`. Package `storetest` tự nó biên dịch được.

**Step 6: Code memstore**

`apps/core/internal/store/memstore/memstore.go`, trong `insertLocked` thay `m.Hidden = false` bằng:

```go
	m.Hidden, m.Reactions = false, domain.ReactionSummary{}
```

`apps/core/internal/store/memstore/rooms.go`:
- trong struct `Rooms`, sau `members map[memberKey]domain.Member` thêm `pins    map[uint64]domain.PinState` (gofmt căn cột).
- thay:

```go
	return &Rooms{rooms: make(map[uint64]domain.Room), members: make(map[memberKey]domain.Member)}
```

bằng:

```go
	return &Rooms{rooms: make(map[uint64]domain.Room), members: make(map[memberKey]domain.Member), pins: make(map[uint64]domain.PinState)}
```

`apps/core/internal/store/memstore/reactions.go`:

```go
package memstore

import (
	"context"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.Reactions = (*Reactions)(nil)

type reactionKey struct {
	key  store.MsgKey
	user string
}

type Reactions struct {
	mu   sync.RWMutex
	docs map[reactionKey]domain.Reaction
}

func NewReactions() *Reactions { return &Reactions{docs: make(map[reactionKey]domain.Reaction)} }

func (s *Reactions) Set(ctx context.Context, r domain.Reaction) (domain.Reaction, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.Reaction{}, false, err
	}
	if err := store.ValidateReaction(r); err != nil {
		return domain.Reaction{}, false, err
	}
	if err := domain.ValidateEmoji(r.Emoji); err != nil {
		return domain.Reaction{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := reactionKey{key: store.ReactionKeyOf(r), user: r.User}
	cur, ok := s.docs[k]
	if ok && cur.Emoji == r.Emoji {
		return cur, false, nil
	}
	next := r
	next.Prev, next.N = cur.Emoji, cur.N+1
	s.docs[k] = next
	return next, true, nil
}

func (s *Reactions) Remove(ctx context.Context, key store.MsgKey, user string, at time.Time) (domain.Reaction, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.Reaction{}, false, err
	}
	if err := store.ValidateReactionTarget(key, user); err != nil {
		return domain.Reaction{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := reactionKey{key: key, user: user}
	cur, ok := s.docs[k]
	if !ok || cur.Emoji == "" {
		return cur, false, nil
	}
	next := cur
	next.Prev, next.Emoji, next.N, next.At = cur.Emoji, "", cur.N+1, at
	s.docs[k] = next
	return next, true, nil
}

func (s *Reactions) Get(ctx context.Context, key store.MsgKey, user string) (domain.Reaction, bool, error) {
	if err := ctx.Err(); err != nil {
		return domain.Reaction{}, false, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	cur, ok := s.docs[reactionKey{key: key, user: user}]
	return cur, ok, nil
}
```

`apps/core/internal/store/memstore/reaction_reads.go`:

```go
package memstore

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (s *Reactions) Count(ctx context.Context, key store.MsgKey, witnesses []store.Witness) ([]domain.ReactionCount, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := key.Validate(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, w := range witnesses {
		if cur, ok := s.docs[reactionKey{key: key, user: w.User}]; !ok || cur.N < w.N {
			return nil, fmt.Errorf("witness %q at change %d on %+v: %w", w.User, w.N, key, store.ErrStaleRead)
		}
	}
	by := map[string]uint32{}
	for k, r := range s.docs {
		if k.key == key && r.Emoji != "" {
			by[r.Emoji]++
		}
	}
	out := make([]domain.ReactionCount, 0, len(by))
	for emoji, n := range by {
		out = append(out, domain.ReactionCount{Emoji: emoji, Count: n})
	}
	domain.SortReactionCounts(out)
	return out, nil
}

func (s *Reactions) Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Reaction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateLimit(limit, store.MaxReactionScan); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.Reaction{}
	for _, r := range s.docs {
		if r.Room == room && !r.At.Before(from) && !r.At.After(to) {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, reactionOrder)
	return out[:min(len(out), limit)], nil
}

func reactionOrder(a, b domain.Reaction) int {
	return cmp.Or(a.At.Compare(b.At), cmp.Compare(a.Thread, b.Thread), cmp.Compare(a.Seq, b.Seq), cmp.Compare(a.User, b.User))
}
```

`apps/core/internal/store/memstore/reaction_summary.go`:

```go
package memstore

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.ReactionSummaries = (*Messages)(nil)

func (s *Messages) SetReactions(ctx context.Context, key store.MsgKey, base uint64, sum domain.ReactionSummary) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := key.Validate(); err != nil {
		return false, err
	}
	if err := store.ValidateVersionBump(base, sum.Version); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	line := s.lines[timeline{key.Room, key.Thread}]
	i, found := slices.BinarySearchFunc(line, key.Seq, bySeq)
	if !found || line[i].Reactions.Version != base {
		return false, nil
	}
	line[i].Reactions = domain.ReactionSummary{Counts: slices.Clone(sum.Counts), Version: sum.Version}
	return true, nil
}
```

`apps/core/internal/store/memstore/pins.go`:

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

var _ store.Pins = (*Pins)(nil)

type Pins struct {
	mu    sync.RWMutex
	facts map[uint64][]domain.PinAction
}

func NewPins() *Pins { return &Pins{facts: make(map[uint64][]domain.PinAction)} }

func (s *Pins) Append(ctx context.Context, a domain.PinAction) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ValidatePinAction(a); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	line := s.facts[a.Room]
	i, found := slices.BinarySearchFunc(line, a.PV, byPV)
	if found {
		return fmt.Errorf("append pin v%d of room %d: %w", a.PV, a.Room, store.ErrPinExists)
	}
	s.facts[a.Room] = slices.Insert(line, i, a)
	return nil
}

func (s *Pins) At(ctx context.Context, room, pv uint64) (domain.PinAction, error) {
	if err := ctx.Err(); err != nil {
		return domain.PinAction{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	line := s.facts[room]
	if i, ok := slices.BinarySearchFunc(line, pv, byPV); ok {
		return line[i], nil
	}
	return domain.PinAction{}, fmt.Errorf("pin v%d of room %d: %w", pv, room, store.ErrPinNotFound)
}

func (s *Pins) After(ctx context.Context, room, pv uint64, limit int) ([]domain.PinAction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateLimit(limit, store.MaxPinScan); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	line := s.facts[room]
	i, found := slices.BinarySearchFunc(line, pv, byPV)
	if found {
		i++
	}
	return slices.Clone(line[i:min(len(line), i+limit)]), nil
}

func (s *Pins) Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.PinAction, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := store.ValidateLimit(limit, store.MaxPinScan); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.PinAction{}
	for _, a := range s.facts[room] {
		if !a.At.Before(from) && !a.At.After(to) {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(a, b domain.PinAction) int { return cmp.Or(a.At.Compare(b.At), cmp.Compare(a.PV, b.PV)) })
	return out[:min(len(out), limit)], nil
}

func byPV(a domain.PinAction, pv uint64) int { return cmp.Compare(a.PV, pv) }
```

`apps/core/internal/store/memstore/pin_state.go`:

```go
package memstore

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var _ store.PinProjector = (*Rooms)(nil)

func (s *Rooms) PinState(ctx context.Context, room uint64) (domain.PinState, error) {
	if err := ctx.Err(); err != nil {
		return domain.PinState{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.rooms[room]; !ok {
		return domain.PinState{}, domain.ErrRoomNotFound
	}
	st := s.pins[room]
	return domain.PinState{Pins: slices.Clone(st.Pins), Version: st.Version}, nil
}

func (s *Rooms) ApplyPins(ctx context.Context, room, base uint64, st domain.PinState) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := store.ValidateVersionBump(base, st.Version); err != nil {
		return false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rooms[room]; !ok || s.pins[room].Version != base {
		return false, nil
	}
	s.pins[room] = domain.PinState{Pins: slices.Clone(st.Pins), Version: st.Version}
	return true, nil
}
```

**Step 7: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/store/..."` rồi `make vet`
Expected: PASS (mongostore integration skip); vet sạch: `mongostore.Store` và mọi fake của `store.Messages`/`store.Rooms` biên dịch không đổi. `-count=5` vì có lock mới. `wc -l apps/core/internal/store/*.go apps/core/internal/store/memstore/*.go apps/core/internal/store/storetest/*.go` mỗi file < 200 (`pin_fact_cases.go` ~150, `reaction_read_cases.go` ~125).

**Step 8: Commit**

INDEXES.csv:
- dòng `apps/core/internal/store`: thay `Hidden (Hide upsert, HiddenIn ascending seqs);` bằng `Hidden (Hide upsert, HiddenIn ascending seqs); reaction ports Reactions (Set version-bump: write unless that emoji is already set, N + 1, Prev = old emoji; Remove leaves a tombstone and never inserts one; Get; Count of live docs per emoji, sorted, ErrStaleRead while a Witness doc is behind; Between room + time range by ts then key up to MaxReactionScan, tombstones included) and ReactionSummaries (SetReactions CAS on rx.v, base 0 = missing); pin ports Pins (Append insert-unique by room|pv -> ErrPinExists; At -> ErrPinNotFound; After ascending up to MaxPinScan; Between) and PinProjector (PinState: ErrRoomNotFound or empty; ApplyPins CAS on pv); ReactionKeyOf, ValidateReaction, ValidateReactionTarget, ValidatePinAction, ValidateVersionBump (next > base and <= MaxInt64); ErrReactionContended;`; thay `Change carries Kind MessageInserted with Msg, RoomInserted with Room or EditInserted with Edit)` bằng `Change carries Kind MessageInserted with Msg, RoomInserted with Room, EditInserted with Edit, ReactionChanged with Reaction or PinInserted with Pin; kinds only grow at the end)`; trong key symbols thay `Hidden;ErrEditExists;` bằng `Hidden;Reactions;ReactionSummaries;Pins;PinProjector;Witness;ErrStaleRead;ErrReactionContended;ErrPinExists;ErrPinNotFound;MaxReactionScan;MaxPinScan;ReactionKeyOf;ValidateReaction;ValidateReactionTarget;ValidatePinAction;ValidateVersionBump;ErrEditExists;` và `EditInserted;Position;` bằng `EditInserted;ReactionChanged;PinInserted;Position;`; cột decisions thay `D10;D30;D52;D62;D69;D72` bằng `D10;D30;D52;D62;D69;D72;D88;D89;D90;D92`.
- dòng `apps/core/internal/store/memstore`: thay `Edits (facts per message sorted by version) and Hidden (set per user);` bằng `Edits (facts per message sorted by version) and Hidden (set per user); Reactions (one doc per message and user, tombstones kept), Pins (facts per room sorted by pv), Messages.SetReactions (CAS on the summary version; Insert drops any summary), Rooms.PinState/ApplyPins (pin state beside the room, CAS on pv);`; trong key symbols thay `NewEdits;NewHidden;Edits;Hidden;` bằng `NewEdits;NewHidden;Edits;Hidden;NewReactions;NewPins;Reactions;Pins;`.
- dòng `apps/core/internal/store/storetest`: thay `cancelled context); room activity case` bằng `cancelled context); reactions (RunReactions over ReactableMessages + Reactions: set, same, other, remove and tombstone, per user and message, field-path emojis stored as given, Count sorted and witness ErrStaleRead, Between with tombstones, invalid input and limits, SetReactions CAS, Insert stores no summary, cancelled context); pins (RunPins over PinnableRooms + Pins: append/at/after/between, existing version, invalid facts and limits, PinState empty or missing room, ApplyPins CAS, room read unchanged, cancelled context); room activity case`; trong key symbols thay `RunEdits;EditableMessages;ClearableRooms` bằng `RunEdits;EditableMessages;ClearableRooms;RunReactions;RunPins;ReactableMessages;PinnableRooms`; cột decisions thay `D52` bằng `D52;D88;D89;D90;D92`.

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/store/reaction.go apps/core/internal/store/pin.go apps/core/internal/store/reaction_test.go apps/core/internal/store/memstore/reactions.go apps/core/internal/store/memstore/reaction_reads.go apps/core/internal/store/memstore/reaction_summary.go apps/core/internal/store/memstore/pins.go apps/core/internal/store/memstore/pin_state.go apps/core/internal/store/storetest/reaction_cases.go apps/core/internal/store/storetest/reaction_set_cases.go apps/core/internal/store/storetest/reaction_read_cases.go apps/core/internal/store/storetest/summary_cases.go apps/core/internal/store/storetest/pin_cases.go apps/core/internal/store/storetest/pin_fact_cases.go apps/core/internal/store/storetest/pin_state_cases.go
git commit -m "feat(store): add reaction, summary, pin and pin projection ports with memstore and contract" -- apps/core/internal/store/ apps/core/internal/work/record.go INDEXES.csv
```

---

### Task 5: ★ Mongostore — `reactions`, `pin_actions`, codec `rx`/`pins`/`pv`, bốn port

Rủi ro cao: write mới lên `messages` (`rx`) và `rooms` (`pins`, `pv`), upsert pipeline có điều kiện và đọc causal session. Một reviewer.

**Tinh chỉnh hợp đồng (controller đã chốt, áp cho cả Part A/B/C):** `*mongostore.Store` **không thể** cài `store.Reactions` và `store.Pins`, vì trùng tên method có sẵn với chữ ký khác: `Reactions.Get(ctx, MsgKey, string)` với `Rooms.Get(ctx, uint64)`, `Reactions.Between`/`Pins.Between` với `Edits.Between` (khác kiểu trả về), `Pins.Append`/`Pins.At` với `Edits.Append`/`Edits.At`. Cách làm: hai kiểu riêng `mongostore.Reactions` và `mongostore.Pins` dùng chung collection của store, lấy qua `(*Store).Reactions() *Reactions` và `(*Store).Pins() *Pins`. `*Store` vẫn cài `store.ReactionSummaries` (`SetReactions`) và `store.PinProjector` (`PinState`, `ApplyPins`) vì không trùng. Hệ quả cho Part B/C (controller sửa hợp đồng): `counter.New(st, st.Reactions())`, `pinproj.New(st.Pins(), st)`, `mutate.Deps{Reactions: st.Reactions(), Pins: st.Pins(), …}`, `effects.ReactionEventDeps{Reactions: st.Reactions(), …}`, `effects.PinEventDeps{Pins: st.Pins(), …}`, `resync.Deps{Reactions: st.Reactions(), Pins: st.Pins(), …}`.

- **Bootstrap:** `reactions`, `pin_actions` clustered zstd (cùng `ensureClustered`). Index `reactions`: `{k:1, e:1}` (đếm phủ index) và `{r:1, ts:1}` (resync); `pin_actions`: `{r:1, ts:1}`. `editIndexes()` đổi tên thành `roomTimeIndexes()` vì ba collection fact dùng chung.
- **Collection:** `reactions` đọc primary + read concern **majority**, write concern của store; `pin_actions` dùng `primary` như `message_edits`. `Reactions` giữ `*mongo.Client` để mở causal session.
- **`Set`:** `FindOneAndUpdate({_id: k│u, e: {$ne: emoji}}, [{$set: {k, r, t, u, pe: {$ifNull: ["$e", ""]}, e, n: {$ifNull: ["$n", 0]} + 1, ts}}], upsert, ReturnDocument After)`. Trong `$set` của pipeline, một chuỗi bắt đầu bằng `$` là đường dẫn field, nên `e`, `t`, `u` đi qua `{$literal: …}` (emoji `$e` phải lưu đúng `$e`). Mọi biểu thức trong cùng một stage đọc doc **trước** stage, nên `pe` nhận emoji cũ. Không có doc → Mongo dựng doc từ điều kiện bằng (`_id`) rồi chạy pipeline (`pe = ""`, `n = 1`). Trùng khoá (`e == emoji` sẵn có, hoặc upsert đua với thiết bị khác của cùng user; Mongo không tự retry upsert khi filter có `$ne`) → `Get` majority: cùng emoji → `(doc, false, nil)`, khác → ghi lại; hết 3 lượt → `ErrReactionContended`.
- **`Remove`:** cùng dạng, filter `{_id, e: {$ne: ""}}`, không upsert, `$set {pe: "$e", e: "", n + 1, ts}`; `ErrNoDocuments` → `Get` (doc hiện tại hoặc zero), `false`.
- **`Count`:** session `SetCausalConsistency(true)` từ `client.StartSession`; trong session: đọc witness `{_id: {$in}}` (projection `u`, `n`) rồi aggregate `[{$match: {k, e: {$gt: ""}}}, {$group: {_id: "$e", n: {$sum: 1}}}]`. Read thứ hai mang `afterClusterTime` của read thứ nhất (driver tự gắn), nên không đếm trên snapshot cũ hơn witness. Witness thiếu hoặc `n < N` → `ErrStaleRead`.
- **`messages.rx`:** `{c: [{e, n}], v}` (mảng, không map), con trỏ `omitempty`; `encodeMessage` không bao giờ ghi; `decodeMessage` đọc (`n`, `v` qua int64 khoan dung: driver nhận int32/int64/double nguyên; âm hoặc vượt `MaxUint32` → `errCorrupt`). `SetReactions` = `UpdateOne({_id, rx.v == base | $exists false}, {$set: {rx}})`, trả `MatchedCount == 1`.
- **`pin_actions`:** `{_id: keys.Pin(room, pv), r, t, op: int32, th, s, by, ts}`. `Append` = `InsertOne`, trùng → `ErrPinExists`. `After` = range clustered `_id ∈ (Pin(room, pv), Pin(room, MaxUint64)]`. `Between` = `{r, ts}` sort `{ts:1, _id:1}`.
- **`rooms.pins`/`pv`:** `PinState` = `FindOne` projection `{pins, pv}`; `ApplyPins` = `UpdateOne({_id, pv == base | $exists false}, {$set: {pins, pv}})`. `Rooms.Get` thêm projection loại `pins`, `pv` (mỗi lệnh đều qua `access.Admit` → `Rooms.Get`; không đọc danh sách ghim ở đó).

Tra API (driver v2.9.1, đã kiểm trong module cache): `func (c *Client) StartSession(opts ...options.Lister[options.SessionOptions]) (*Session, error)`; `options.Session().SetCausalConsistency(true)`; `func NewSessionContext(parent context.Context, sess *Session) context.Context`; `func (s *Session) EndSession(ctx context.Context)`; `options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)`; `func (coll *Collection) Aggregate(ctx, pipeline any, opts ...) (*Cursor, error)`; decode int64 field nhận int32/int64/double nguyên.

**Files:**
- Modify: `apps/core/internal/store/mongostore/mongostore.go` (thay toàn bộ)
- Modify: `apps/core/internal/store/mongostore/codec.go` (`messageDoc`, `decodeMessage`)
- Modify: `apps/core/internal/store/mongostore/bootstrap.go` (`Bootstrap`, `editIndexes` → `roomTimeIndexes`, thêm `reactionIndexes`)
- Modify: `apps/core/internal/store/mongostore/rooms.go` (`Get`, `findOne`)
- Create: `apps/core/internal/store/mongostore/reaction_codec.go`, `reaction_codec_test.go`
- Create: `apps/core/internal/store/mongostore/reactions.go`, `reaction_count.go`, `reaction_summary.go`
- Create: `apps/core/internal/store/mongostore/pin_codec.go`, `pin_codec_test.go`, `pins.go`, `pin_state.go`
- Create: `apps/core/internal/store/mongostore/reactions_integration_test.go`, `reaction_count_integration_test.go`, `bootstrap_reactions_integration_test.go`
- Modify: `INDEXES.csv` (dòng `apps/core/internal/store/mongostore`)

**Step 1: Test codec (unit)**

`apps/core/internal/store/mongostore/reaction_codec_test.go` (`codecTime`, `sampleMessage`, `roundTrip`, `fieldNames` có sẵn ở `codec_test.go`):

```go
package mongostore

import (
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func sampleReactionDoc() reactionDoc {
	return reactionDoc{
		ID: keys.Reaction(7_340_000_001, 3, 42, "bob"), Key: keys.Msg(7_340_000_001, 3, 42), Room: 7_340_000_001,
		Tenant: "acme", User: "bob", Emoji: "❤️", Prev: "👍", N: 2, At: codecTime,
	}
}

func TestDecodeReactionReadsTheKeyAndChangeNumber(t *testing.T) {
	got, err := decodeReaction(sampleReactionDoc())
	want := domain.Reaction{Room: 7_340_000_001, Thread: 3, Seq: 42, Tenant: "acme", User: "bob", Emoji: "❤️", Prev: "👍", N: 2, At: codecTime}
	if err != nil || !got.At.Equal(want.At) {
		t.Fatalf("decodeReaction = %+v, %v; want %+v", got, err, want)
	}
	got.At = want.At
	if got != want {
		t.Fatalf("decoded %+v, want %+v", got, want)
	}
}

func TestDecodeReactionRejectsCorruptDocs(t *testing.T) {
	for name, mutate := range map[string]func(*reactionDoc){
		"message-only id":   func(d *reactionDoc) { d.ID = keys.Msg(1, 0, 1) },
		"user with a dot":   func(d *reactionDoc) { d.ID = keys.Reaction(1, 0, 1, "a.b") },
		"negative n":        func(d *reactionDoc) { d.N = -1 },
		"n above uint32":    func(d *reactionDoc) { d.N = math.MaxUint32 + 1 },
	} {
		d := sampleReactionDoc()
		mutate(&d)
		if _, err := decodeReaction(d); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: decodeReaction = %v, want errCorrupt", name, err)
		}
	}
}

func TestSummaryCodecRoundTrip(t *testing.T) {
	s := domain.ReactionSummary{Counts: []domain.ReactionCount{{Emoji: "👍", Count: 2}, {Emoji: "$x", Count: 1}}, Version: 7}
	doc, err := encodeSummary(s)
	if err != nil {
		t.Fatalf("encodeSummary: %v", err)
	}
	back, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"c", "v"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	if e := raw.Lookup("c", "1", "e").StringValue(); e != "$x" {
		t.Fatalf("second count emoji = %q, want $x stored as a value", e)
	}
	got, err := decodeSummary(&back)
	if err != nil || got.Version != 7 || !slices.Equal(got.Counts, s.Counts) {
		t.Fatalf("decodeSummary = %+v, %v; want %+v", got, err, s)
	}
	if got, err := decodeSummary(nil); err != nil || got.Version != 0 || got.Counts != nil {
		t.Fatalf("decodeSummary(nil) = %+v, %v; want the zero summary", got, err)
	}
	if _, err := encodeSummary(domain.ReactionSummary{Version: math.MaxInt64 + 1}); err == nil {
		t.Fatal("encodeSummary accepted a version above max int64")
	}
	for name, d := range map[string]reactionsDoc{
		"negative version": {Version: -1},
		"negative count":   {Counts: []countDoc{{Emoji: "👍", N: -1}}, Version: 1},
	} {
		if _, err := decodeSummary(&d); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: decodeSummary = %v, want errCorrupt", name, err)
		}
	}
}

func TestMessageCodecReadsReactionsButInsertNeverWritesThem(t *testing.T) {
	m := sampleMessage()
	m.Reactions = domain.ReactionSummary{Counts: []domain.ReactionCount{{Emoji: "👍", Count: 1}}, Version: 1}
	doc, err := encodeMessage(m)
	if err != nil || doc.Reactions != nil {
		t.Fatalf("encodeMessage = %+v, %v; want no rx", doc.Reactions, err)
	}
	if _, raw := roundTrip(t, doc); !slices.Equal(fieldNames(t, raw), []string{"_id", "t", "f", "k", "x", "c", "ts"}) {
		t.Fatalf("fields = %v, want the 7 fields of a new message", fieldNames(t, raw))
	}
	rx, err := encodeSummary(m.Reactions)
	if err != nil {
		t.Fatalf("encodeSummary: %v", err)
	}
	doc.Reactions = &rx
	got, err := decodeMessage(doc)
	if err != nil || !reflect.DeepEqual(got.Reactions, m.Reactions) {
		t.Fatalf("decodeMessage reactions = %+v, %v; want %+v", got.Reactions, err, m.Reactions)
	}
}

func TestSetPipelineTakesStringsLiterally(t *testing.T) {
	r := domain.Reaction{Room: 7, Seq: 1, Tenant: "acme", User: "alice", Emoji: "$e", At: codecTime}
	raw, err := bson.Marshal(setReaction(r, 7)[0])
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if got, ok := raw.Lookup("$set", "e", "$literal").StringValueOK(); !ok || got != "$e" {
		t.Fatalf("$set.e = %s, want {$literal: $e}", raw.Lookup("$set", "e"))
	}
	if got, ok := raw.Lookup("$set", "u", "$literal").StringValueOK(); !ok || got != "alice" {
		t.Fatalf("$set.u = %s, want {$literal: alice}", raw.Lookup("$set", "u"))
	}
	if _, k, ok := raw.Lookup("$set", "k").BinaryOK(); !ok || len(k) != keys.MsgLen {
		t.Fatalf("$set.k = %s, want the 24-byte message key", raw.Lookup("$set", "k"))
	}
}
```

(gofmt căn lại cột map literal.)

`apps/core/internal/store/mongostore/pin_codec_test.go`:

```go
package mongostore

import (
	"bytes"
	"errors"
	"math"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func samplePinAction() domain.PinAction {
	return domain.PinAction{Room: 7_340_000_001, PV: 3, Tenant: "acme", Op: domain.PinOpUnpin, Thread: 2, Seq: 42, By: "bob", At: codecTime}
}

func TestPinActionCodecRoundTrip(t *testing.T) {
	a := samplePinAction()
	doc, err := encodePinAction(a)
	if err != nil {
		t.Fatalf("encodePinAction: %v", err)
	}
	if !bytes.Equal(doc.ID, keys.Pin(a.Room, a.PV)) || doc.Op != 2 {
		t.Fatalf("_id = %x, op = %d; want keys.Pin and 2", doc.ID, doc.Op)
	}
	back, raw := roundTrip(t, doc)
	if got, want := fieldNames(t, raw), []string{"_id", "r", "t", "op", "th", "s", "by", "ts"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	got, err := decodePinAction(back)
	if err != nil || !got.At.Equal(a.At) {
		t.Fatalf("decodePinAction = %+v, %v; want %+v", got, err, a)
	}
	got.At = a.At
	if got != a {
		t.Fatalf("decoded %+v, want %+v", got, a)
	}
}

func TestEncodePinActionRejectsInvalid(t *testing.T) {
	for name, mutate := range map[string]func(*domain.PinAction){
		"zero version":            func(a *domain.PinAction) { a.PV = 0 },
		"version above max int64": func(a *domain.PinAction) { a.PV = math.MaxInt64 + 1 },
		"room above max int64":    func(a *domain.PinAction) { a.Room = math.MaxInt64 + 1 },
		"thread above max int64":  func(a *domain.PinAction) { a.Thread = math.MaxInt64 + 1 },
		"seq above max int64":     func(a *domain.PinAction) { a.Seq = math.MaxInt64 + 1 },
		"unknown op":              func(a *domain.PinAction) { a.Op = 3 },
	} {
		a := samplePinAction()
		mutate(&a)
		if _, err := encodePinAction(a); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: encodePinAction = %v, want ErrInvalidArgument", name, err)
		}
	}
}

func TestDecodePinActionRejectsCorruptDocs(t *testing.T) {
	for name, mutate := range map[string]func(*pinActionDoc){
		"short id":        func(d *pinActionDoc) { d.ID = d.ID[:8] },
		"unknown op":      func(d *pinActionDoc) { d.Op = 7 },
		"negative thread": func(d *pinActionDoc) { d.Thread = -1 },
		"negative seq":    func(d *pinActionDoc) { d.Seq = -1 },
	} {
		d, err := encodePinAction(samplePinAction())
		if err != nil {
			t.Fatalf("encodePinAction: %v", err)
		}
		mutate(&d)
		if _, err := decodePinAction(d); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: decodePinAction = %v, want errCorrupt", name, err)
		}
	}
}

func TestPinStateCodecRoundTrip(t *testing.T) {
	s := domain.PinState{Pins: []domain.Pin{{Seq: 9, By: "bob", At: codecTime, PV: 4}, {Thread: 2, Seq: 7, By: "alice", At: codecTime, PV: 1}}, Version: 5}
	pins, pv, err := encodePinState(s)
	if err != nil || pv != 5 {
		t.Fatalf("encodePinState = %d, %v; want pv 5", pv, err)
	}
	back, raw := roundTrip(t, pinStateDoc{Pins: pins, PV: pv})
	if got, want := fieldNames(t, raw), []string{"pins", "pv"}; !slices.Equal(got, want) {
		t.Fatalf("fields = %v, want %v", got, want)
	}
	got, err := decodePinState(back)
	if err != nil || got.Version != 5 || len(got.Pins) != 2 || got.Pins[0].Seq != 9 || got.Pins[1].Thread != 2 || !got.Pins[1].At.Equal(codecTime) {
		t.Fatalf("decodePinState = %+v, %v; want %+v", got, err, s)
	}
	if got, err := decodePinState(pinStateDoc{}); err != nil || got.Version != 0 || got.Pins != nil {
		t.Fatalf("decodePinState(empty) = %+v, %v; want the zero state", got, err)
	}
	for name, d := range map[string]pinStateDoc{
		"negative version": {PV: -1},
		"negative seq":     {Pins: []pinDoc{{Seq: -1, PV: 1}}, PV: 1},
		"negative pv":      {Pins: []pinDoc{{Seq: 1, PV: -1}}, PV: 1},
	} {
		if _, err := decodePinState(d); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: decodePinState = %v, want errCorrupt", name, err)
		}
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/mongostore/..."`
Expected: FAIL biên dịch: `undefined: reactionDoc`, `undefined: decodeReaction`, `undefined: encodeSummary`, `undefined: decodeSummary`, `undefined: countDoc`, `doc.Reactions undefined (type messageDoc has no field or method Reactions)`, `undefined: setReaction`, `undefined: encodePinAction`, `undefined: pinActionDoc`, `undefined: encodePinState`, `undefined: pinStateDoc`.

**Step 3: Code codec**

`apps/core/internal/store/mongostore/reaction_codec.go`:

```go
package mongostore

import (
	"fmt"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type reactionDoc struct {
	ID     []byte    `bson:"_id"`
	Key    []byte    `bson:"k"`
	Room   int64     `bson:"r"`
	Tenant string    `bson:"t"`
	User   string    `bson:"u"`
	Prev   string    `bson:"pe"`
	Emoji  string    `bson:"e"`
	N      int64     `bson:"n"`
	At     time.Time `bson:"ts"`
}

type reactionsDoc struct {
	Counts  []countDoc `bson:"c"`
	Version int64      `bson:"v"`
}

type countDoc struct {
	Emoji string `bson:"e"`
	N     int64  `bson:"n"`
}

func decodeReaction(d reactionDoc) (domain.Reaction, error) {
	room, thread, seq, user, err := keys.ParseReaction(d.ID)
	if err != nil {
		return domain.Reaction{}, fmt.Errorf("%w: reaction _id: %w", errCorrupt, err)
	}
	if err := domain.ValidUser(user); err != nil {
		return domain.Reaction{}, fmt.Errorf("%w: reaction user: %w", errCorrupt, err)
	}
	n, err := narrowUint32("reaction change", d.N)
	if err != nil {
		return domain.Reaction{}, err
	}
	return domain.Reaction{
		Room: room, Thread: thread, Seq: seq, Tenant: d.Tenant, User: user, Emoji: d.Emoji, Prev: d.Prev, N: n, At: d.At,
	}, nil
}

func decodeReactions(docs []reactionDoc) ([]domain.Reaction, error) {
	out := make([]domain.Reaction, len(docs))
	for i, d := range docs {
		r, err := decodeReaction(d)
		if err != nil {
			return nil, err
		}
		out[i] = r
	}
	return out, nil
}

func encodeSummary(s domain.ReactionSummary) (reactionsDoc, error) {
	v, err := toInt64("reactions version", s.Version)
	if err != nil {
		return reactionsDoc{}, err
	}
	counts := make([]countDoc, len(s.Counts))
	for i, c := range s.Counts {
		counts[i] = countDoc{Emoji: c.Emoji, N: int64(c.Count)}
	}
	return reactionsDoc{Counts: counts, Version: v}, nil
}

func decodeSummary(d *reactionsDoc) (domain.ReactionSummary, error) {
	if d == nil {
		return domain.ReactionSummary{}, nil
	}
	v, err := toUint64("reactions version", d.Version)
	if err != nil {
		return domain.ReactionSummary{}, err
	}
	counts := make([]domain.ReactionCount, len(d.Counts))
	for i, c := range d.Counts {
		n, err := narrowUint32("reaction count", c.N)
		if err != nil {
			return domain.ReactionSummary{}, err
		}
		counts[i] = domain.ReactionCount{Emoji: c.Emoji, Count: n}
	}
	return domain.ReactionSummary{Counts: counts, Version: v}, nil
}

func narrowUint32(field string, v int64) (uint32, error) {
	if v < 0 || v > math.MaxUint32 {
		return 0, fmt.Errorf("%w: %s %d out of range", errCorrupt, field, v)
	}
	return uint32(v), nil
}
```

(`reactionDoc` khai báo `pe` trước `e` cho khớp thứ tự field mà pipeline `$set` ghi; decode theo tên nên thứ tự không ảnh hưởng.)

`apps/core/internal/store/mongostore/pin_codec.go`:

```go
package mongostore

import (
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type pinActionDoc struct {
	ID     []byte    `bson:"_id"`
	Room   int64     `bson:"r"`
	Tenant string    `bson:"t"`
	Op     int32     `bson:"op"`
	Thread int64     `bson:"th"`
	Seq    int64     `bson:"s"`
	By     string    `bson:"by"`
	At     time.Time `bson:"ts"`
}

type pinDoc struct {
	Thread int64     `bson:"th"`
	Seq    int64     `bson:"s"`
	By     string    `bson:"by"`
	At     time.Time `bson:"ts"`
	PV     int64     `bson:"pv"`
}

type pinStateDoc struct {
	Pins []pinDoc `bson:"pins"`
	PV   int64    `bson:"pv"`
}

func encodePinAction(a domain.PinAction) (pinActionDoc, error) {
	if err := store.ValidatePinAction(a); err != nil {
		return pinActionDoc{}, err
	}
	room, err := toInt64("room id", a.Room)
	if err != nil {
		return pinActionDoc{}, err
	}
	thread, err := toInt64("thread", a.Thread)
	if err != nil {
		return pinActionDoc{}, err
	}
	seq, err := toInt64("seq", a.Seq)
	if err != nil {
		return pinActionDoc{}, err
	}
	return pinActionDoc{
		ID: keys.Pin(a.Room, a.PV), Room: room, Tenant: a.Tenant, Op: int32(a.Op), Thread: thread, Seq: seq, By: a.By, At: a.At,
	}, nil
}

func decodePinAction(d pinActionDoc) (domain.PinAction, error) {
	room, pv, err := keys.ParsePin(d.ID)
	if err != nil {
		return domain.PinAction{}, fmt.Errorf("%w: pin _id: %w", errCorrupt, err)
	}
	op, err := decodePinOp(d.Op)
	if err != nil {
		return domain.PinAction{}, err
	}
	thread, err := toUint64("pin thread", d.Thread)
	if err != nil {
		return domain.PinAction{}, err
	}
	seq, err := toUint64("pin seq", d.Seq)
	if err != nil {
		return domain.PinAction{}, err
	}
	return domain.PinAction{Room: room, PV: pv, Tenant: d.Tenant, Op: op, Thread: thread, Seq: seq, By: d.By, At: d.At}, nil
}

func decodePinOp(op int32) (domain.PinOp, error) {
	switch op {
	case int32(domain.PinOpPin):
		return domain.PinOpPin, nil
	case int32(domain.PinOpUnpin):
		return domain.PinOpUnpin, nil
	default:
		return 0, fmt.Errorf("%w: pin op %d", errCorrupt, op)
	}
}

func decodePinActions(docs []pinActionDoc) ([]domain.PinAction, error) {
	out := make([]domain.PinAction, len(docs))
	for i, d := range docs {
		a, err := decodePinAction(d)
		if err != nil {
			return nil, err
		}
		out[i] = a
	}
	return out, nil
}

func encodePinState(s domain.PinState) ([]pinDoc, int64, error) {
	pv, err := toInt64("pin version", s.Version)
	if err != nil {
		return nil, 0, err
	}
	docs := make([]pinDoc, len(s.Pins))
	for i, p := range s.Pins {
		thread, threadErr := toInt64("pin thread", p.Thread)
		seq, seqErr := toInt64("pin seq", p.Seq)
		pinPV, pvErr := toInt64("pin version", p.PV)
		if threadErr != nil || seqErr != nil || pvErr != nil {
			return nil, 0, fmt.Errorf("encode pin %d of state v%d: %w", i, s.Version, firstErr(threadErr, seqErr, pvErr))
		}
		docs[i] = pinDoc{Thread: thread, Seq: seq, By: p.By, At: p.At, PV: pinPV}
	}
	return docs, pv, nil
}

func decodePinState(d pinStateDoc) (domain.PinState, error) {
	version, err := toUint64("pin version", d.PV)
	if err != nil {
		return domain.PinState{}, err
	}
	var pins []domain.Pin
	for _, p := range d.Pins {
		thread, threadErr := toUint64("pin thread", p.Thread)
		seq, seqErr := toUint64("pin seq", p.Seq)
		pv, pvErr := toUint64("pin version", p.PV)
		if err := firstErr(threadErr, seqErr, pvErr); err != nil {
			return domain.PinState{}, err
		}
		pins = append(pins, domain.Pin{Thread: thread, Seq: seq, By: p.By, At: p.At, PV: pv})
	}
	return domain.PinState{Pins: pins, Version: version}, nil
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
```

`apps/core/internal/store/mongostore/codec.go`:
- thay toàn bộ struct `messageDoc` bằng:

```go
type messageDoc struct {
	ID        []byte        `bson:"_id"`
	Tenant    string        `bson:"t"`
	From      string        `bson:"f"`
	Kind      domain.Kind   `bson:"k"`
	Text      string        `bson:"x"`
	CID       string        `bson:"c"`
	CreatedAt time.Time     `bson:"ts"`
	Version   int32         `bson:"v,omitempty"`
	Deleted   bool          `bson:"d,omitempty"`
	EditedAt  time.Time     `bson:"ea,omitempty"`
	Reactions *reactionsDoc `bson:"rx,omitempty"`
}
```

- trong `decodeMessage` thay:

```go
	version, err := toUint32("message version", d.Version)
	if err != nil {
		return domain.Message{}, err
	}
	return domain.Message{
```

bằng:

```go
	version, err := toUint32("message version", d.Version)
	if err != nil {
		return domain.Message{}, err
	}
	reactions, err := decodeSummary(d.Reactions)
	if err != nil {
		return domain.Message{}, err
	}
	return domain.Message{
```

và thay (trong `decodeMessage`):

```go
		EditedAt:  d.EditedAt,
	}, nil
}
```

bằng:

```go
		EditedAt:  d.EditedAt,
		Reactions: reactions,
	}, nil
}
```

`encodeMessage` giữ nguyên: không đặt `Reactions`, nên `rx` không bao giờ đi vào `InsertMany`.

**Step 4: Chạy, codec đã đủ**

Run: `make -s go ARGS="vet ./apps/core/internal/store/mongostore/"`
Expected: FAIL biên dịch chỉ còn `undefined: setReaction` (pipeline của adapter, Step 7 tạo); mọi symbol codec đã có. Lỗi khác → dừng, báo cáo. Test codec chạy ở Step 8.

**Step 5: Test integration**

`apps/core/internal/store/mongostore/bootstrap_reactions_integration_test.go`:

```go
package mongostore

import "testing"

func TestBootstrapCreatesReactionAndPinCollections(t *testing.T) {
	db := itDatabase(t, itClient(t))
	for i := range 2 {
		if err := Bootstrap(t.Context(), db); err != nil {
			t.Fatalf("Bootstrap #%d: %v", i+1, err)
		}
	}
	assertClusteredLayout(t, db, reactionsCollection)
	assertClusteredLayout(t, db, pinActionsCollection)
	if got := indexKeys(t, db.Collection(reactionsCollection)); !hasIndex(got, "k:1,e:1", false) || !hasIndex(got, "r:1,ts:1", false) {
		t.Fatalf("reactions indexes = %v, want non-unique k:1,e:1 and r:1,ts:1", got)
	}
	if got := indexKeys(t, db.Collection(pinActionsCollection)); !hasIndex(got, "r:1,ts:1", false) {
		t.Fatalf("pin_actions indexes = %v, want non-unique r:1,ts:1", got)
	}
	if got := indexKeys(t, db.Collection(editsCollection)); !hasIndex(got, "r:1,ts:1", false) {
		t.Fatalf("message_edits indexes = %v, want non-unique r:1,ts:1 after the rename", got)
	}
}
```

`apps/core/internal/store/mongostore/reactions_integration_test.go`:

```go
package mongostore

import (
	"bytes"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/storetest"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func TestMongoReactionsContract(t *testing.T) {
	client := itClient(t)
	storetest.RunReactions(t, func(t *testing.T) (storetest.ReactableMessages, store.Reactions) {
		s, _ := itStore(t, client)
		return s, s.Reactions()
	})
}

func TestMongoPinsContract(t *testing.T) {
	client := itClient(t)
	storetest.RunPins(t, func(t *testing.T) (storetest.PinnableRooms, store.Pins) {
		s, _ := itStore(t, client)
		return s, s.Pins()
	})
}

func TestReactionDocumentLayout(t *testing.T) {
	s, db := itStore(t, itClient(t))
	r := domain.Reaction{Room: itRoom, Seq: 1, Tenant: "acme", User: "alice", Emoji: "$e", At: codecTime}
	if _, _, err := s.Reactions().Set(t.Context(), r); err != nil {
		t.Fatalf("Set: %v", err)
	}
	raw, err := db.Collection(reactionsCollection).FindOne(t.Context(), bson.D{{Key: "_id", Value: keys.Reaction(itRoom, 0, 1, "alice")}}).Raw()
	if err != nil {
		t.Fatalf("FindOne raw: %v", err)
	}
	if got, want := fieldNames(t, raw), []string{"_id", "k", "r", "t", "u", "pe", "e", "n", "ts"}; !slices.Equal(got, want) {
		t.Fatalf("stored fields = %v, want %v", got, want)
	}
	if e := raw.Lookup("e").StringValue(); e != "$e" {
		t.Fatalf("stored emoji = %q, want $e", e)
	}
	if _, k, ok := raw.Lookup("k").BinaryOK(); !ok || !bytes.Equal(k, keys.Msg(itRoom, 0, 1)) {
		t.Fatalf("stored k = %s, want keys.Msg", raw.Lookup("k"))
	}
}

func TestReactionSetCountsEveryChangeOnceUnderRacingDevices(t *testing.T) {
	s, _ := itStore(t, itClient(t))
	reactions := s.Reactions()
	emojis := []string{"👍", "❤️", "😂", "🎉"}
	var changes atomic.Int64
	var wg sync.WaitGroup
	for _, e := range emojis {
		wg.Go(func() {
			for range 10 {
				r := domain.Reaction{Room: itRoom, Seq: 1, Tenant: "acme", User: "alice", Emoji: e, At: codecTime}
				_, changed, err := reactions.Set(t.Context(), r)
				if err != nil && !errors.Is(err, store.ErrReactionContended) {
					t.Errorf("Set(%s) = %v, want nil or ErrReactionContended", e, err)
				}
				if changed {
					changes.Add(1)
				}
			}
		})
	}
	wg.Wait()
	got, ok, err := reactions.Get(t.Context(), store.MsgKey{Room: itRoom, Seq: 1}, "alice")
	if err != nil || !ok || int64(got.N) != changes.Load() || !slices.Contains(emojis, got.Emoji) {
		t.Fatalf("final = %+v, %v, %v; want change number %d and one of %v", got, ok, err, changes.Load(), emojis)
	}
}
```

`apps/core/internal/store/mongostore/reaction_count_integration_test.go` (`walkStages` có sẵn ở `query_integration_test.go`):

```go
package mongostore

import (
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func walkWinning(doc bson.Raw, visit func(bson.Raw)) {
	elems, _ := doc.Elements()
	for _, e := range elems {
		if e.Key() == "winningPlan" {
			walkStages(e.Value(), visit)
			continue
		}
		if d, ok := e.Value().DocumentOK(); ok {
			walkWinning(d, visit)
		}
		if a, ok := e.Value().ArrayOK(); ok {
			vals, _ := a.Values()
			for _, v := range vals {
				if d, ok := v.DocumentOK(); ok {
					walkWinning(d, visit)
				}
			}
		}
	}
}

func TestReactionCountIsCoveredByTheEmojiIndex(t *testing.T) {
	s, db := itStore(t, itClient(t))
	for user, emoji := range map[string]string{"alice": "👍", "bob": "👍", "carol": "❤️"} {
		r := domain.Reaction{Room: itRoom, Seq: 1, Tenant: "acme", User: user, Emoji: emoji, At: codecTime}
		if _, _, err := s.Reactions().Set(t.Context(), r); err != nil {
			t.Fatalf("Set: %v", err)
		}
	}
	key := store.MsgKey{Room: itRoom, Seq: 1}
	aggregate := bson.D{{Key: "aggregate", Value: reactionsCollection}, {Key: "pipeline", Value: countPipeline(key)}, {Key: "cursor", Value: bson.D{}}}
	raw, err := db.RunCommand(t.Context(), bson.D{{Key: "explain", Value: aggregate}, {Key: "verbosity", Value: "queryPlanner"}}).Raw()
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	var stages, indexes []string
	walkWinning(raw, func(d bson.Raw) {
		stages = append(stages, d.Lookup("stage").StringValue())
		if name, ok := d.Lookup("indexName").StringValueOK(); ok {
			indexes = append(indexes, name)
		}
	})
	if !slices.Contains(indexes, "k_1_e_1") || slices.Contains(stages, "FETCH") || slices.Contains(stages, "COLLSCAN") {
		t.Fatalf("winning plan stages %v on indexes %v, want a covered scan of k_1_e_1 without FETCH", stages, indexes)
	}
	got, err := s.Reactions().Count(t.Context(), key, []store.Witness{{User: "alice", N: 1}})
	if err != nil || !slices.Equal(got, []domain.ReactionCount{{Emoji: "👍", Count: 2}, {Emoji: "❤️", Count: 1}}) {
		t.Fatalf("Count = %v, %v", got, err)
	}
}
```

**Step 6: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/mongostore/..."`
Expected: FAIL biên dịch: `undefined: reactionsCollection`, `undefined: pinActionsCollection`, `s.Reactions undefined (type *Store has no field or method Reactions)`, `s.Pins undefined`, `*Store does not implement storetest.ReactableMessages (missing method SetReactions)`, `*Store does not implement storetest.PinnableRooms (missing method ApplyPins)`, `undefined: countPipeline`, `undefined: setReaction`.

**Step 7: Code adapter**

`apps/core/internal/store/mongostore/mongostore.go`, thay toàn bộ:

```go
package mongostore

import (
	"cmp"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const (
	messagesCollection        = "messages"
	roomsCollection           = "rooms"
	membersCollection         = "members"
	reconcilerStateCollection = "reconciler_state"
	editsCollection           = "message_edits"
	hiddenCollection          = "hidden"
	reactionsCollection       = "reactions"
	pinActionsCollection      = "pin_actions"
)

var (
	_ store.Messages          = (*Store)(nil)
	_ store.Rooms             = (*Store)(nil)
	_ store.MessageEditor     = (*Store)(nil)
	_ store.HistoryClearer    = (*Store)(nil)
	_ store.Edits             = (*Store)(nil)
	_ store.Hidden            = (*Store)(nil)
	_ store.ReactionSummaries = (*Store)(nil)
	_ store.PinProjector      = (*Store)(nil)
	_ store.Reactions         = (*Reactions)(nil)
	_ store.Pins              = (*Pins)(nil)
)

type Options struct {
	WriteConcern *writeconcern.WriteConcern
}

type Store struct {
	messages  *mongo.Collection
	committed *mongo.Collection
	rooms     *mongo.Collection
	members   *mongo.Collection
	edits     *mongo.Collection
	hidden    *mongo.Collection
	reactions *Reactions
	pins      *Pins
}

func New(db *mongo.Database, opts Options) *Store {
	wc := cmp.Or(opts.WriteConcern, writeconcern.Majority())
	primary := options.Collection().SetWriteConcern(wc).SetReadPreference(readpref.Primary())
	local := options.Collection().SetWriteConcern(wc).SetReadPreference(readpref.Primary()).SetReadConcern(readconcern.Local())
	majority := options.Collection().SetReadPreference(readpref.Primary()).SetReadConcern(readconcern.Majority())
	reacted := options.Collection().SetWriteConcern(wc).SetReadPreference(readpref.Primary()).SetReadConcern(readconcern.Majority())
	return &Store{
		messages:  db.Collection(messagesCollection, local),
		committed: db.Collection(messagesCollection, majority),
		rooms:     db.Collection(roomsCollection, primary),
		members:   db.Collection(membersCollection, primary),
		edits:     db.Collection(editsCollection, primary),
		hidden:    db.Collection(hiddenCollection, primary),
		reactions: &Reactions{coll: db.Collection(reactionsCollection, reacted), client: db.Client()},
		pins:      &Pins{coll: db.Collection(pinActionsCollection, primary)},
	}
}

func (s *Store) Reactions() *Reactions { return s.reactions }

func (s *Store) Pins() *Pins { return s.pins }
```

`apps/core/internal/store/mongostore/bootstrap.go`:
- trong `Bootstrap` thay `for _, name := range []string{messagesCollection, editsCollection} {` bằng `for _, name := range []string{messagesCollection, editsCollection, reactionsCollection, pinActionsCollection} {`.
- trong danh sách `indexes` thay:

```go
		{editsCollection, editIndexes()},
		{hiddenCollection, hiddenIndexes()},
```

bằng:

```go
		{editsCollection, roomTimeIndexes()},
		{hiddenCollection, hiddenIndexes()},
		{reactionsCollection, reactionIndexes()},
		{pinActionsCollection, roomTimeIndexes()},
```

- thay:

```go
func editIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{{Keys: bson.D{{Key: "r", Value: 1}, {Key: "ts", Value: 1}}}}
}
```

bằng:

```go
func roomTimeIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{{Keys: bson.D{{Key: "r", Value: 1}, {Key: "ts", Value: 1}}}}
}

func reactionIndexes() []mongo.IndexModel {
	return append([]mongo.IndexModel{{Keys: bson.D{{Key: "k", Value: 1}, {Key: "e", Value: 1}}}}, roomTimeIndexes()...)
}
```

`apps/core/internal/store/mongostore/rooms.go`:
- thay:

```go
	var d roomDoc
	if err := findOne(ctx, s.rooms, bson.D{{Key: "_id", Value: key}}, &d, domain.ErrRoomNotFound); err != nil {
		return domain.Room{}, fmt.Errorf("get room %d: %w", id, err)
	}
```

bằng:

```go
	var d roomDoc
	withoutPins := options.FindOne().SetProjection(bson.D{{Key: "pins", Value: 0}, {Key: "pv", Value: 0}})
	if err := findOne(ctx, s.rooms, bson.D{{Key: "_id", Value: key}}, &d, domain.ErrRoomNotFound, withoutPins); err != nil {
		return domain.Room{}, fmt.Errorf("get room %d: %w", id, err)
	}
```

- thay:

```go
func findOne(ctx context.Context, coll *mongo.Collection, filter bson.D, out any, missing error) error {
	err := coll.FindOne(ctx, filter).Decode(out)
```

bằng:

```go
func findOne(ctx context.Context, coll *mongo.Collection, filter bson.D, out any, missing error, opts ...options.Lister[options.FindOneOptions]) error {
	err := coll.FindOne(ctx, filter, opts...).Decode(out)
```

`apps/core/internal/store/mongostore/reactions.go`:

```go
package mongostore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

const reactionTries = 3

type Reactions struct {
	coll   *mongo.Collection
	client *mongo.Client
}

func (r *Reactions) Set(ctx context.Context, x domain.Reaction) (domain.Reaction, bool, error) {
	if err := store.ValidateReaction(x); err != nil {
		return domain.Reaction{}, false, err
	}
	if err := domain.ValidateEmoji(x.Emoji); err != nil {
		return domain.Reaction{}, false, err
	}
	room, err := toInt64("room id", x.Room)
	if err != nil {
		return domain.Reaction{}, false, err
	}
	key := store.ReactionKeyOf(x)
	filter := bson.D{{Key: "_id", Value: reactionID(key, x.User)}, {Key: "e", Value: bson.D{{Key: "$ne", Value: x.Emoji}}}}
	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)
	for range reactionTries {
		var d reactionDoc
		err := r.coll.FindOneAndUpdate(ctx, filter, setReaction(x, room), opts).Decode(&d)
		if err == nil {
			got, err := decodeReaction(d)
			return got, err == nil, err
		}
		if !mongo.IsDuplicateKeyError(err) {
			return domain.Reaction{}, false, fmt.Errorf("set reaction of %q on %d/%d/%d: %w", x.User, x.Room, x.Thread, x.Seq, err)
		}
		cur, ok, err := r.Get(ctx, key, x.User)
		if err != nil {
			return domain.Reaction{}, false, err
		}
		if ok && cur.Emoji == x.Emoji {
			return cur, false, nil
		}
	}
	return domain.Reaction{}, false, fmt.Errorf("set reaction of %q on %d/%d/%d: %w", x.User, x.Room, x.Thread, x.Seq, store.ErrReactionContended)
}

func (r *Reactions) Remove(ctx context.Context, key store.MsgKey, user string, at time.Time) (domain.Reaction, bool, error) {
	if err := store.ValidateReactionTarget(key, user); err != nil {
		return domain.Reaction{}, false, err
	}
	filter := bson.D{{Key: "_id", Value: reactionID(key, user)}, {Key: "e", Value: bson.D{{Key: "$ne", Value: ""}}}}
	var d reactionDoc
	err := r.coll.FindOneAndUpdate(ctx, filter, removeReaction(at), options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&d)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		cur, _, err := r.Get(ctx, key, user)
		return cur, false, err
	case err != nil:
		return domain.Reaction{}, false, fmt.Errorf("remove reaction of %q on %d/%d/%d: %w", user, key.Room, key.Thread, key.Seq, err)
	}
	got, err := decodeReaction(d)
	return got, err == nil, err
}

func (r *Reactions) Get(ctx context.Context, key store.MsgKey, user string) (domain.Reaction, bool, error) {
	var d reactionDoc
	err := r.coll.FindOne(ctx, bson.D{{Key: "_id", Value: reactionID(key, user)}}).Decode(&d)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments):
		return domain.Reaction{}, false, nil
	case err != nil:
		return domain.Reaction{}, false, fmt.Errorf("get reaction of %q on %d/%d/%d: %w", user, key.Room, key.Thread, key.Seq, err)
	}
	got, err := decodeReaction(d)
	return got, err == nil, err
}

func reactionID(key store.MsgKey, user string) []byte {
	return keys.Reaction(key.Room, key.Thread, key.Seq, user)
}

func setReaction(x domain.Reaction, room int64) mongo.Pipeline {
	set := bson.D{
		{Key: "k", Value: keys.Msg(x.Room, x.Thread, x.Seq)},
		{Key: "r", Value: room},
		{Key: "t", Value: literal(x.Tenant)},
		{Key: "u", Value: literal(x.User)},
		{Key: "pe", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$e", ""}}}},
		{Key: "e", Value: literal(x.Emoji)},
		{Key: "n", Value: nextChange()},
		{Key: "ts", Value: x.At},
	}
	return mongo.Pipeline{{{Key: "$set", Value: set}}}
}

func removeReaction(at time.Time) mongo.Pipeline {
	set := bson.D{
		{Key: "pe", Value: "$e"},
		{Key: "e", Value: ""},
		{Key: "n", Value: nextChange()},
		{Key: "ts", Value: at},
	}
	return mongo.Pipeline{{{Key: "$set", Value: set}}}
}

func nextChange() bson.D {
	return bson.D{{Key: "$add", Value: bson.A{bson.D{{Key: "$ifNull", Value: bson.A{"$n", 0}}}, 1}}}
}

func literal(v string) bson.D { return bson.D{{Key: "$literal", Value: v}} }
```

`apps/core/internal/store/mongostore/reaction_count.go`:

```go
package mongostore

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

type witnessDoc struct {
	User string `bson:"u"`
	N    int64  `bson:"n"`
}

type countRow struct {
	Emoji string `bson:"_id"`
	N     int64  `bson:"n"`
}

func (r *Reactions) Count(ctx context.Context, key store.MsgKey, witnesses []store.Witness) ([]domain.ReactionCount, error) {
	if err := key.Validate(); err != nil {
		return nil, err
	}
	sess, err := r.client.StartSession(options.Session().SetCausalConsistency(true))
	if err != nil {
		return nil, fmt.Errorf("count reactions of %d/%d/%d: start session: %w", key.Room, key.Thread, key.Seq, err)
	}
	defer sess.EndSession(context.WithoutCancel(ctx))
	sctx := mongo.NewSessionContext(ctx, sess)
	if err := r.witnessed(sctx, key, witnesses); err != nil {
		return nil, err
	}
	return r.live(sctx, key)
}

func (r *Reactions) witnessed(ctx context.Context, key store.MsgKey, witnesses []store.Witness) error {
	if len(witnesses) == 0 {
		return nil
	}
	need := make(map[string]uint32, len(witnesses))
	ids := make(bson.A, 0, len(witnesses))
	for _, w := range witnesses {
		if _, seen := need[w.User]; !seen {
			ids = append(ids, reactionID(key, w.User))
		}
		need[w.User] = max(need[w.User], w.N)
	}
	opts := options.Find().SetProjection(bson.D{{Key: "u", Value: 1}, {Key: "n", Value: 1}})
	cur, err := r.coll.Find(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}}, opts)
	if err != nil {
		return fmt.Errorf("read reaction witnesses of %d/%d/%d: %w", key.Room, key.Thread, key.Seq, err)
	}
	var docs []witnessDoc
	if err := cur.All(ctx, &docs); err != nil {
		return fmt.Errorf("read reaction witnesses of %d/%d/%d: %w", key.Room, key.Thread, key.Seq, err)
	}
	seen := 0
	for _, d := range docs {
		if n, ok := need[d.User]; ok && d.N >= int64(n) {
			seen++
		}
	}
	if seen < len(need) {
		return fmt.Errorf("reactions of %d/%d/%d: %w", key.Room, key.Thread, key.Seq, store.ErrStaleRead)
	}
	return nil
}

func countPipeline(key store.MsgKey) mongo.Pipeline {
	match := bson.D{{Key: "k", Value: keys.Msg(key.Room, key.Thread, key.Seq)}, {Key: "e", Value: bson.D{{Key: "$gt", Value: ""}}}}
	group := bson.D{{Key: "_id", Value: "$e"}, {Key: "n", Value: bson.D{{Key: "$sum", Value: 1}}}}
	return mongo.Pipeline{{{Key: "$match", Value: match}}, {{Key: "$group", Value: group}}}
}

func (r *Reactions) live(ctx context.Context, key store.MsgKey) ([]domain.ReactionCount, error) {
	cur, err := r.coll.Aggregate(ctx, countPipeline(key))
	if err != nil {
		return nil, fmt.Errorf("count reactions of %d/%d/%d: %w", key.Room, key.Thread, key.Seq, err)
	}
	var rows []countRow
	if err := cur.All(ctx, &rows); err != nil {
		return nil, fmt.Errorf("count reactions of %d/%d/%d: %w", key.Room, key.Thread, key.Seq, err)
	}
	out := make([]domain.ReactionCount, 0, len(rows))
	for _, row := range rows {
		n, err := narrowUint32("reaction count", row.N)
		if err != nil {
			return nil, err
		}
		out = append(out, domain.ReactionCount{Emoji: row.Emoji, Count: n})
	}
	domain.SortReactionCounts(out)
	return out, nil
}

func (r *Reactions) Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Reaction, error) {
	if err := store.ValidateLimit(limit, store.MaxReactionScan); err != nil {
		return nil, err
	}
	rid, err := toInt64("room id", room)
	if err != nil {
		return nil, err
	}
	filter := bson.D{{Key: "r", Value: rid}, {Key: "ts", Value: bson.D{{Key: "$gte", Value: from}, {Key: "$lte", Value: to}}}}
	opts := options.Find().SetSort(bson.D{{Key: "ts", Value: 1}, {Key: "_id", Value: 1}}).SetLimit(int64(limit))
	cur, err := r.coll.Find(ctx, filter, opts)
	if err != nil {
		return nil, fmt.Errorf("reactions of room %d between %v and %v: %w", room, from, to, err)
	}
	var docs []reactionDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("reactions of room %d between %v and %v: %w", room, from, to, err)
	}
	return decodeReactions(docs)
}
```

`apps/core/internal/store/mongostore/reaction_summary.go`:

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

func (s *Store) SetReactions(ctx context.Context, key store.MsgKey, base uint64, sum domain.ReactionSummary) (bool, error) {
	if err := key.Validate(); err != nil {
		return false, err
	}
	if err := store.ValidateVersionBump(base, sum.Version); err != nil {
		return false, err
	}
	doc, err := encodeSummary(sum)
	if err != nil {
		return false, err
	}
	from, err := toInt64("reactions base version", base)
	if err != nil {
		return false, err
	}
	filter := bson.D{{Key: "_id", Value: keys.Msg(key.Room, key.Thread, key.Seq)}, versionIs("rx.v", from)}
	res, err := s.messages.UpdateOne(ctx, filter, bson.D{{Key: "$set", Value: bson.D{{Key: "rx", Value: doc}}}})
	if err != nil {
		return false, fmt.Errorf("set reactions v%d of %d/%d/%d: %w", sum.Version, key.Room, key.Thread, key.Seq, err)
	}
	return res.MatchedCount == 1, nil
}

func versionIs(field string, base int64) bson.E {
	if base == 0 {
		return bson.E{Key: field, Value: bson.D{{Key: "$exists", Value: false}}}
	}
	return bson.E{Key: field, Value: base}
}
```

`apps/core/internal/store/mongostore/pins.go`:

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

type Pins struct {
	coll *mongo.Collection
}

func (p *Pins) Append(ctx context.Context, a domain.PinAction) error {
	doc, err := encodePinAction(a)
	if err != nil {
		return err
	}
	if _, err := p.coll.InsertOne(ctx, doc); err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return fmt.Errorf("append pin v%d of room %d: %w", a.PV, a.Room, store.ErrPinExists)
		}
		return fmt.Errorf("append pin v%d of room %d: %w", a.PV, a.Room, err)
	}
	return nil
}

func (p *Pins) At(ctx context.Context, room, pv uint64) (domain.PinAction, error) {
	var d pinActionDoc
	if err := findOne(ctx, p.coll, bson.D{{Key: "_id", Value: keys.Pin(room, pv)}}, &d, store.ErrPinNotFound); err != nil {
		return domain.PinAction{}, fmt.Errorf("pin v%d of room %d: %w", pv, room, err)
	}
	return decodePinAction(d)
}

func (p *Pins) After(ctx context.Context, room, pv uint64, limit int) ([]domain.PinAction, error) {
	if err := store.ValidateLimit(limit, store.MaxPinScan); err != nil {
		return nil, err
	}
	filter := bson.D{{Key: "_id", Value: bson.D{
		{Key: "$gt", Value: keys.Pin(room, pv)},
		{Key: "$lte", Value: keys.Pin(room, math.MaxUint64)},
	}}}
	return p.find(ctx, filter, bson.D{{Key: "_id", Value: 1}}, limit)
}

func (p *Pins) Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.PinAction, error) {
	if err := store.ValidateLimit(limit, store.MaxPinScan); err != nil {
		return nil, err
	}
	rid, err := toInt64("room id", room)
	if err != nil {
		return nil, err
	}
	filter := bson.D{{Key: "r", Value: rid}, {Key: "ts", Value: bson.D{{Key: "$gte", Value: from}, {Key: "$lte", Value: to}}}}
	return p.find(ctx, filter, bson.D{{Key: "ts", Value: 1}, {Key: "_id", Value: 1}}, limit)
}

func (p *Pins) find(ctx context.Context, filter, sort bson.D, limit int) ([]domain.PinAction, error) {
	cur, err := p.coll.Find(ctx, filter, options.Find().SetSort(sort).SetLimit(int64(limit)))
	if err != nil {
		return nil, fmt.Errorf("find pin actions: %w", err)
	}
	var docs []pinActionDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("find pin actions: %w", err)
	}
	return decodePinActions(docs)
}
```

`apps/core/internal/store/mongostore/pin_state.go`:

```go
package mongostore

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (s *Store) PinState(ctx context.Context, room uint64) (domain.PinState, error) {
	key, err := toInt64("room id", room)
	if err != nil {
		return domain.PinState{}, fmt.Errorf("pins of room %d: %w", room, domain.ErrRoomNotFound)
	}
	var d pinStateDoc
	onlyPins := options.FindOne().SetProjection(bson.D{{Key: "pins", Value: 1}, {Key: "pv", Value: 1}})
	if err := findOne(ctx, s.rooms, bson.D{{Key: "_id", Value: key}}, &d, domain.ErrRoomNotFound, onlyPins); err != nil {
		return domain.PinState{}, fmt.Errorf("pins of room %d: %w", room, err)
	}
	return decodePinState(d)
}

func (s *Store) ApplyPins(ctx context.Context, room, base uint64, st domain.PinState) (bool, error) {
	if err := store.ValidateVersionBump(base, st.Version); err != nil {
		return false, err
	}
	key, err := toInt64("room id", room)
	if err != nil {
		return false, err
	}
	from, err := toInt64("pin base version", base)
	if err != nil {
		return false, err
	}
	pins, pv, err := encodePinState(st)
	if err != nil {
		return false, err
	}
	filter := bson.D{{Key: "_id", Value: key}, versionIs("pv", from)}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "pins", Value: pins}, {Key: "pv", Value: pv}}}}
	res, err := s.rooms.UpdateOne(ctx, filter, update)
	if err != nil {
		return false, fmt.Errorf("apply pins v%d to room %d: %w", st.Version, room, err)
	}
	return res.MatchedCount == 1, nil
}
```

**Step 8: Chạy unit, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/..."` rồi `make vet`
Expected: PASS (integration skip); vet sạch. `TestMessageCodecRoundTrip` vẫn đúng 7 field (rx `omitempty`, `encodeMessage` không đặt) và `reflect.DeepEqual` vẫn đúng vì `decodeSummary(nil)` trả summary zero (Counts nil). `wc -l apps/core/internal/store/mongostore/*.go` mỗi file < 200 (`codec.go` ≤ 190, `pin_codec.go` ~150, `reactions.go` ~140, `reaction_count.go` ~135).

**Step 9: Integration**

Run: `make infra-up && make itest`
Expected: PASS, gồm `TestMongoReactionsContract` và `TestMongoPinsContract` (mọi case của `RunReactions`/`RunPins`, kể cả `set of the same emoji is a no-op` đi qua nhánh trùng khoá → đọc lại), `TestReactionDocumentLayout` (field `_id k r t u pe e n ts`, emoji `$e` lưu nguyên), `TestReactionSetCountsEveryChangeOnceUnderRacingDevices`, `TestReactionCountIsCoveredByTheEmojiIndex` (winning plan có `k_1_e_1`, không `FETCH`), `TestBootstrapCreatesReactionAndPinCollections`, `TestBootstrapIsIdempotent`, `TestMongoStoreContract`, `TestMongoEditsContract` và itest của `apps/core`. Case Mongo khác memstore → dừng, báo cáo (không sửa contract cho vừa adapter). Explain trả dạng khác (ví dụ SBE gom `GROUP` nhưng vẫn có `k_1_e_1` và không `FETCH`) mà test fail vì cách duyệt cây: báo controller kèm `raw` explain, không nới điều kiện "không FETCH".

**Step 10: Commit**

INDEXES.csv, dòng `apps/core/internal/store/mongostore`:
- thay `Bootstrap creates clustered zstd messages and message_edits (index {r:1, ts:1}),` bằng `Bootstrap creates clustered zstd messages, message_edits (index {r:1, ts:1}), reactions (indexes {k:1, e:1} for a covered count and {r:1, ts:1}) and pin_actions (index {r:1, ts:1}),`.
- thay `members cb via FindOneAndUpdate $max;` bằng `members cb via FindOneAndUpdate $max; reactions via Store.Reactions() (own type: Get/Between/Append/At clash with Rooms.Get and Edits): Set = FindOneAndUpdate upsert on {_id: k|u, e != emoji} with a pipeline (t/u/e as $literal, pe = old e, n + 1), duplicate key -> majority reread (same emoji = no-op) up to 3 tries -> ErrReactionContended; Remove = same filter on e != '' without upsert; Count = witness read + covered aggregate in one causal session (majority); messages.rx {c: [{e, n}], v} written only by SetReactions (UpdateOne CAS on rx.v) and read by Page/Find; pin facts via Store.Pins() (Append insert -> ErrPinExists, At, After clustered _id range, Between {r, ts}); rooms.pins/pv read only by PinState (projection; Rooms.Get excludes them) and written by ApplyPins (CAS on pv);`.
- trong key symbols thay `New;Store;Options;` bằng `New;Store;Options;Store.Reactions;Store.Pins;Reactions;Pins;`; cột decisions thay `D52;D62;D69;D70;D72;D75;D76` bằng `D52;D62;D69;D70;D72;D75;D76;D88;D89;D90;D92`.

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/store/mongostore/reaction_codec.go apps/core/internal/store/mongostore/reaction_codec_test.go apps/core/internal/store/mongostore/reactions.go apps/core/internal/store/mongostore/reaction_count.go apps/core/internal/store/mongostore/reaction_summary.go apps/core/internal/store/mongostore/pin_codec.go apps/core/internal/store/mongostore/pin_codec_test.go apps/core/internal/store/mongostore/pins.go apps/core/internal/store/mongostore/pin_state.go apps/core/internal/store/mongostore/reactions_integration_test.go apps/core/internal/store/mongostore/reaction_count_integration_test.go apps/core/internal/store/mongostore/bootstrap_reactions_integration_test.go
git commit -m "feat(mongostore): store reactions, reaction summaries, pin facts and pin projections" -- apps/core/internal/store/mongostore/ INDEXES.csv
```

---

### Task 6: ★ Feed — `ReactionChanged` (insert/update/replace) + `PinInserted` (memstore + Mongo)

Nhật ký commit có thêm hai loại thay đổi: mọi lần đổi của doc `reactions` và insert `pin_actions`. Rủi ro cao vì feed Mongo lần đầu nhận `update`/`replace` (trước giờ chỉ `insert`), nên `$match` phải chặn chặt: update của `messages` (`rx`, `ApplyEdit`), `rooms` (activity, `pins`/`pv`), `members` (`cb`) và mọi collection khác vẫn bị loại bởi `ns.coll`. Không dùng `updateLookup` (đọc thêm doc mỗi event, và có thể trả bản mới hơn event).

- **Mongo update:** event không có `fullDocument`; khoá lấy từ `documentKey._id` (`keys.ParseReaction` → room/thread/seq/user, user phải qua `domain.ValidUser`), `n` từ `updateDescription.updatedFields.n` (đọc khoan dung `AsInt64OK`: int32/int64/double), phải trong `1..MaxUint32`. Thiếu hay sai → `errCorrupt` → `store.ErrCorruptChange`, reader drop có đếm. `Change.Reaction` của update **chỉ chắc** có `Room, Thread, Seq, User, N` (hợp đồng). Insert (upsert tạo doc) và replace (Mongo có thể ghi pipeline update của doc nhỏ thành replace) giải mã `fullDocument` đầy đủ.
- **memstore:** `NewFeed(msgs, rooms, edits, opts ...FeedOption)`; `WithReactions(*Reactions)`, `WithPins(*Pins)` gắn hai kho vào log chung. Thứ tự lock `Reactions.mu` → `Messages.mu` và `Pins.mu` → `Messages.mu`, như `Edits`. Write no-op (đúng trạng thái sẵn, append trùng) không log. Caller cũ của `NewFeed` không đổi (tham số variadic).
- Contract mới là hai entry riêng `storetest.RunReactionFeed`, `storetest.RunPinFeed`; `RunFeed`/`RunEditFeed` giữ nguyên.

Reader (`reconcile/term.go`) **chưa** nhận hai kind mới: `work.KnownKind` còn false, `forward()` drop và đếm `Dropped` tới Task 7. Chưa có gì ghi `reactions`/`pin_actions` ngoài test trước Task 9, nên không mất gì (dev).

**Files:**
- Modify: `apps/core/internal/store/memstore/change_log.go` (thay toàn bộ), `feed.go` (`NewFeed`, `cursor.Next`), `reactions.go` (`Reactions`, `Set`, `Remove`), `pins.go` (`Pins`, `Append`), `memstore_test.go`
- Create: `apps/core/internal/store/storetest/feed_reaction_cases.go`
- Modify: `apps/core/internal/store/mongostore/feed.go` (`feedPipeline`), `feed_change.go` (thay toàn bộ), `feed_integration_test.go`
- Create: `apps/core/internal/store/mongostore/feed_reaction_change.go`, `feed_reaction_change_test.go`, `feed_skip_integration_test.go`
- Modify: `INDEXES.csv` (dòng `apps/core/internal/store/memstore`, `apps/core/internal/store/storetest`, `apps/core/internal/store/mongostore`)

**Step 1: Test**

`apps/core/internal/store/storetest/feed_reaction_cases.go`:

```go
package storetest

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func RunReactionFeed(t *testing.T, open func(t *testing.T) (store.Reactions, store.ChangeFeed)) {
	t.Helper()
	t.Run("reaction changes come out in commit order and no-ops add none", func(t *testing.T) {
		reactions, feed := open(t)
		cur := openCursor(t, feed)
		key := msgKey(roomA, mainThread, 1)
		first := mustSet(t, reactions, reactionOf(roomA, mainThread, 1, "alice", "👍"), true)
		mustSet(t, reactions, reactionOf(roomA, mainThread, 1, "alice", "👍"), false)
		second := mustSet(t, reactions, reactAt(roomA, mainThread, 1, "alice", "❤️", time.Second), true)
		removed := mustRemove(t, reactions, key, "alice", baseTime.Add(2*time.Second), true)
		mustRemove(t, reactions, key, "alice", baseTime.Add(3*time.Second), false)
		mustRemove(t, reactions, key, "bob", baseTime, false)
		other := mustSet(t, reactions, reactionOf(roomB, sideThread, 2, "bob", "😂"), true)
		want := []domain.Reaction{first, second, removed, other}
		for i, c := range nextChanges(t, cur, len(want)) {
			got, w := c.Reaction, want[i]
			if c.Kind != store.ReactionChanged || c.Msg.Seq != 0 || c.Room.ID != 0 || c.Edit.Version != 0 || c.Pin.PV != 0 {
				t.Fatalf("change %d = %+v, want only a reaction change", i, c)
			}
			if got.Room != w.Room || got.Thread != w.Thread || got.Seq != w.Seq || got.User != w.User || got.N != w.N {
				t.Fatalf("change %d = %d/%d/%d %q n%d, want %d/%d/%d %q n%d", i, got.Room, got.Thread, got.Seq, got.User, got.N, w.Room, w.Thread, w.Seq, w.User, w.N)
			}
		}
	})
}

func RunPinFeed(t *testing.T, open func(t *testing.T) (store.Pins, store.ChangeFeed)) {
	t.Helper()
	t.Run("pin facts come out in commit order with their content and a refused append adds none", func(t *testing.T) {
		pins, feed := open(t)
		cur := openCursor(t, feed)
		first, second := pinFact(roomA, 1, domain.PinOpPin, 3), pinFact(roomA, 2, domain.PinOpUnpin, 3)
		other := pinFact(roomB, 1, domain.PinOpPin, 9)
		mustAppendPins(t, pins, first)
		assertErrorIs(t, "Append(existing version)", pins.Append(t.Context(), pinFact(roomA, 1, domain.PinOpPin, 8)), store.ErrPinExists)
		mustAppendPins(t, pins, second, other)
		got := nextChanges(t, cur, 3)
		facts := make([]domain.PinAction, len(got))
		for i, c := range got {
			if c.Kind != store.PinInserted || c.Msg.Seq != 0 || c.Room.ID != 0 || c.Reaction.N != 0 {
				t.Fatalf("change %d = %+v, want only a pin fact", i, c)
			}
			facts[i] = c.Pin
		}
		assertPinActions(t, "pin changes", facts, []domain.PinAction{first, second, other})
	})
}
```

`apps/core/internal/store/memstore/memstore_test.go`, thêm vào cuối:

```go
func TestReactionFeedContract(t *testing.T) {
	storetest.RunReactionFeed(t, func(*testing.T) (store.Reactions, store.ChangeFeed) {
		reactions := memstore.NewReactions()
		return reactions, memstore.NewFeed(memstore.NewMessages(), nil, nil, memstore.WithReactions(reactions))
	})
}

func TestPinFeedContract(t *testing.T) {
	storetest.RunPinFeed(t, func(*testing.T) (store.Pins, store.ChangeFeed) {
		pins := memstore.NewPins()
		return pins, memstore.NewFeed(memstore.NewMessages(), nil, nil, memstore.WithPins(pins))
	})
}
```

`apps/core/internal/store/mongostore/feed_reaction_change_test.go` (`codecTime`, `changeOn` có sẵn; `sampleReactionDoc`, `samplePinAction` từ Task 5):

```go
package mongostore

import (
	"errors"
	"math"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func reactionUpdate(t *testing.T, id any, fields bson.D) changeDoc {
	t.Helper()
	key, err := bson.Marshal(bson.D{{Key: "_id", Value: id}})
	if err != nil {
		t.Fatalf("Marshal key: %v", err)
	}
	updated, err := bson.Marshal(fields)
	if err != nil {
		t.Fatalf("Marshal fields: %v", err)
	}
	return changeDoc{
		OperationType: "update", WallTime: codecTime, NS: changeNS{Coll: reactionsCollection},
		DocumentKey: changeKey{ID: bson.Raw(key).Lookup("_id")}, UpdateDescription: changeUpdate{UpdatedFields: updated},
	}
}

func TestDecodeChangeReadsReactionUpdatesFromTheKey(t *testing.T) {
	id := keys.Reaction(7_340_000_001, 3, 42, "bob")
	want := domain.Reaction{Room: 7_340_000_001, Thread: 3, Seq: 42, User: "bob", N: 2}
	for name, n := range map[string]any{"int32": int32(2), "int64": int64(2), "double": float64(2)} {
		got, err := decodeChange(reactionUpdate(t, id, bson.D{{Key: "pe", Value: "👍"}, {Key: "e", Value: ""}, {Key: "n", Value: n}}))
		if err != nil || got.Kind != store.ReactionChanged || got.Reaction != want || !got.CommittedAt.Equal(codecTime) || got.Msg.Seq != 0 {
			t.Fatalf("%s: update change = %+v, %v; want %+v", name, got, err, want)
		}
	}
}

func TestDecodeChangeReadsReactionInsertsAndReplacesFromTheDocument(t *testing.T) {
	want, err := decodeReaction(sampleReactionDoc())
	if err != nil {
		t.Fatalf("decodeReaction: %v", err)
	}
	for _, op := range []string{"insert", "replace"} {
		ev := changeOn(t, reactionsCollection, sampleReactionDoc())
		ev.OperationType = op
		got, err := decodeChange(ev)
		if err != nil || got.Kind != store.ReactionChanged || !got.Reaction.At.Equal(want.At) {
			t.Fatalf("%s change = %+v, %v; want %+v", op, got, err, want)
		}
		got.Reaction.At = want.At
		if got.Reaction != want {
			t.Fatalf("%s change reaction = %+v, want %+v", op, got.Reaction, want)
		}
	}
}

func TestDecodeChangeReadsPinFacts(t *testing.T) {
	a := samplePinAction()
	doc, err := encodePinAction(a)
	if err != nil {
		t.Fatalf("encodePinAction: %v", err)
	}
	got, err := decodeChange(changeOn(t, pinActionsCollection, doc))
	if err != nil || got.Kind != store.PinInserted || !got.Pin.At.Equal(a.At) || got.Reaction.N != 0 {
		t.Fatalf("pin change = %+v, %v; want %+v", got, err, a)
	}
	got.Pin.At = a.At
	if got.Pin != a {
		t.Fatalf("pin change fact = %+v, want %+v", got.Pin, a)
	}
}

func TestDecodeChangeRejectsBrokenReactionAndPinChanges(t *testing.T) {
	id := keys.Reaction(7_340_000_001, 0, 42, "bob")
	cases := map[string]changeDoc{
		"update without n":       reactionUpdate(t, id, bson.D{{Key: "e", Value: "x"}}),
		"update with n 0":        reactionUpdate(t, id, bson.D{{Key: "n", Value: int32(0)}}),
		"update with n too big":  reactionUpdate(t, id, bson.D{{Key: "n", Value: int64(math.MaxUint32) + 1}}),
		"update with string n":   reactionUpdate(t, id, bson.D{{Key: "n", Value: "2"}}),
		"update of a short key":  reactionUpdate(t, keys.Msg(1, 0, 1), bson.D{{Key: "n", Value: int32(2)}}),
		"update of a bad user":   reactionUpdate(t, keys.Reaction(1, 0, 1, "a.b"), bson.D{{Key: "n", Value: int32(2)}}),
		"update of a string key": reactionUpdate(t, "x", bson.D{{Key: "n", Value: int32(2)}}),
		"insert of a bad key":    changeOn(t, reactionsCollection, bson.D{{Key: "_id", Value: []byte{1, 2}}}),
		"insert of a bad pin":    changeOn(t, pinActionsCollection, bson.D{{Key: "_id", Value: []byte{1}}}),
	}
	for name, ev := range cases {
		if _, err := decodeChange(ev); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: decodeChange = %v, want errCorrupt", name, err)
		}
	}
}

func TestFeedPipelineLetsOnlyReactionUpdatesThrough(t *testing.T) {
	raw, err := bson.Marshal(feedPipeline()[0])
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	inserts, _ := raw.Lookup("$match", "$or", "0", "ns.coll", "$in").Array().Values()
	if op := raw.Lookup("$match", "$or", "0", "operationType").StringValue(); op != "insert" || len(inserts) != 5 {
		t.Fatalf("insert branch = %s, want inserts of the 5 fact collections", raw.Lookup("$match", "$or", "0"))
	}
	if coll := raw.Lookup("$match", "$or", "1", "ns.coll").StringValue(); coll != reactionsCollection {
		t.Fatalf("change branch = %s, want updates and replaces of reactions only", raw.Lookup("$match", "$or", "1"))
	}
}
```

(gofmt căn lại cột map literal.)

`apps/core/internal/store/mongostore/feed_integration_test.go`, thêm vào cuối:

```go
func TestMongoReactionFeedContract(t *testing.T) {
	client := itClient(t)
	storetest.RunReactionFeed(t, func(t *testing.T) (store.Reactions, store.ChangeFeed) {
		s, db := itStore(t, client)
		return s.Reactions(), NewFeed(db)
	})
}

func TestMongoPinFeedContract(t *testing.T) {
	client := itClient(t)
	storetest.RunPinFeed(t, func(t *testing.T) (store.Pins, store.ChangeFeed) {
		s, db := itStore(t, client)
		return s.Pins(), NewFeed(db)
	})
}
```

`apps/core/internal/store/mongostore/feed_skip_integration_test.go` (`itRoom`, `msgAt`, `seedTimeline` có sẵn ở `query_integration_test.go`):

```go
package mongostore

import (
	"context"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestFeedSkipsSummaryPinActivityEditHideAndClearWrites(t *testing.T) {
	s, db := itStore(t, itClient(t))
	ctx := t.Context()
	room := domain.Room{ID: itRoom, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: codecTime, MemberCount: 1}
	owner := domain.Member{Room: itRoom, Tenant: "acme", User: "alice", Role: domain.RoleOwner, JoinedAt: codecTime}
	if err := s.Create(ctx, room, []domain.Member{owner}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	seedTimeline(t, s, itRoom, 0, 1)
	cur, err := NewFeed(db).Open(ctx)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = cur.Close(context.Background()) })
	wait, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, want := range []store.ChangeKind{store.RoomInserted, store.MessageInserted} {
		if c, err := cur.Next(wait); err != nil || c.Kind != want {
			t.Fatalf("Next = %+v, %v; want kind %d", c, err, want)
		}
	}
	m := msgAt(itRoom, 0, 1)
	key := store.KeyOf(m)
	sum := domain.ReactionSummary{Counts: []domain.ReactionCount{{Emoji: "👍", Count: 1}}, Version: 1}
	if ok, err := s.SetReactions(ctx, key, 0, sum); err != nil || !ok {
		t.Fatalf("SetReactions = %v, %v", ok, err)
	}
	pins := domain.PinState{Pins: []domain.Pin{{Seq: 1, By: "alice", At: codecTime, PV: 1}}, Version: 1}
	if ok, err := s.ApplyPins(ctx, itRoom, 0, pins); err != nil || !ok {
		t.Fatalf("ApplyPins = %v, %v", ok, err)
	}
	if err := s.TouchActivity(ctx, []store.Activity{{Room: itRoom, Seq: 1, At: codecTime}}); err != nil {
		t.Fatalf("TouchActivity: %v", err)
	}
	edit := domain.Edit{Room: itRoom, Seq: 1, Version: 1, Kind: domain.EditText, Tenant: "acme", By: "alice", Text: "sửa", Prev: m.Text, At: codecTime}
	if err := s.ApplyEdit(ctx, edit); err != nil {
		t.Fatalf("ApplyEdit: %v", err)
	}
	if err := s.Hide(ctx, "bob", key); err != nil {
		t.Fatalf("Hide: %v", err)
	}
	if _, err := s.ClearHistory(ctx, itRoom, "alice", 1); err != nil {
		t.Fatalf("ClearHistory: %v", err)
	}
	if _, _, err := s.Reactions().Set(ctx, domain.Reaction{Room: itRoom, Seq: 1, Tenant: "acme", User: "alice", Emoji: "👍", At: codecTime}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	c, err := cur.Next(wait)
	if err != nil || c.Kind != store.ReactionChanged || c.Reaction.User != "alice" || c.Reaction.N != 1 {
		t.Fatalf("next change = %+v, %v; want only the reaction after the summary, pin, activity, edit, hide and clear writes", c, err)
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/..."`
Expected: FAIL biên dịch: `undefined: memstore.WithReactions`, `undefined: memstore.WithPins`, `too many arguments in call to memstore.NewFeed` (memstore); `unknown field OperationType in struct literal of type changeDoc`, `undefined: changeKey`, `undefined: changeUpdate` (mongostore).

**Step 3: Code memstore**

`apps/core/internal/store/memstore/change_log.go`, thay toàn bộ:

```go
package memstore

import (
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type logged struct {
	kind     store.ChangeKind
	msg      domain.Message
	room     domain.Room
	edit     domain.Edit
	reaction domain.Reaction
	pin      domain.PinAction
	at       time.Time
}

type FeedOption func(log *Messages)

func WithReactions(r *Reactions) FeedOption { return func(log *Messages) { r.attach(log) } }

func WithPins(p *Pins) FeedOption { return func(log *Messages) { p.attach(log) } }

func (s *Messages) appendLog(l logged) {
	l.at = time.Now()
	s.log = append(s.log, l)
	close(s.grew)
	s.grew = make(chan struct{})
}

func (s *Messages) appendFact(l logged) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendLog(l)
}

func (s *Messages) logAt(i int) (logged, <-chan struct{}, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if i < len(s.log) {
		return s.log[i], nil, true
	}
	return logged{}, s.grew, false
}

func (s *Messages) logLen() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.log)
}

func (s *Rooms) attach(log *Messages) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = log
}

func (s *Edits) attach(log *Messages) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = log
}

func (s *Reactions) attach(log *Messages) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = log
}

func (s *Pins) attach(log *Messages) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = log
}
```

`apps/core/internal/store/memstore/feed.go`:
- thay:

```go
func NewFeed(msgs *Messages, rooms *Rooms, edits *Edits) *Feed {
	if rooms != nil {
		rooms.attach(msgs)
	}
	if edits != nil {
		edits.attach(msgs)
	}
```

bằng:

```go
func NewFeed(msgs *Messages, rooms *Rooms, edits *Edits, opts ...FeedOption) *Feed {
	if rooms != nil {
		rooms.attach(msgs)
	}
	if edits != nil {
		edits.attach(msgs)
	}
	for _, opt := range opts {
		opt(msgs)
	}
```

- trong `cursor.Next` thay:

```go
			return store.Change{Kind: l.kind, Msg: l.msg, Room: l.room, Edit: l.edit, CommittedAt: l.at, Position: store.Position(strconv.Itoa(c.next))}, nil
```

bằng:

```go
			return store.Change{
				Kind: l.kind, Msg: l.msg, Room: l.room, Edit: l.edit, Reaction: l.reaction, Pin: l.pin,
				CommittedAt: l.at, Position: store.Position(strconv.Itoa(c.next)),
			}, nil
```

`apps/core/internal/store/memstore/reactions.go`:
- thay struct:

```go
type Reactions struct {
	mu   sync.RWMutex
	docs map[reactionKey]domain.Reaction
}
```

bằng:

```go
type Reactions struct {
	mu   sync.RWMutex
	docs map[reactionKey]domain.Reaction
	log  *Messages
}
```

- trong `Set` thay:

```go
	next.Prev, next.N = cur.Emoji, cur.N+1
	s.docs[k] = next
	return next, true, nil
```

bằng:

```go
	next.Prev, next.N = cur.Emoji, cur.N+1
	s.docs[k] = next
	s.logChange(next)
	return next, true, nil
```

- trong `Remove` thay:

```go
	next.Prev, next.Emoji, next.N, next.At = cur.Emoji, "", cur.N+1, at
	s.docs[k] = next
	return next, true, nil
```

bằng:

```go
	next.Prev, next.Emoji, next.N, next.At = cur.Emoji, "", cur.N+1, at
	s.docs[k] = next
	s.logChange(next)
	return next, true, nil
```

- thêm vào cuối file:

```go
func (s *Reactions) logChange(r domain.Reaction) {
	if s.log != nil {
		s.log.appendFact(logged{kind: store.ReactionChanged, reaction: r})
	}
}
```

`apps/core/internal/store/memstore/pins.go`:
- thay struct:

```go
type Pins struct {
	mu    sync.RWMutex
	facts map[uint64][]domain.PinAction
}
```

bằng:

```go
type Pins struct {
	mu    sync.RWMutex
	facts map[uint64][]domain.PinAction
	log   *Messages
}
```

- trong `Append` thay:

```go
	s.facts[a.Room] = slices.Insert(line, i, a)
	return nil
```

bằng:

```go
	s.facts[a.Room] = slices.Insert(line, i, a)
	if s.log != nil {
		s.log.appendFact(logged{kind: store.PinInserted, pin: a})
	}
	return nil
```

**Step 4: Code Mongo**

`apps/core/internal/store/mongostore/feed.go`, thay toàn bộ `feedPipeline`:

```go
func feedPipeline() mongo.Pipeline {
	facts := bson.A{messagesCollection, roomsCollection, editsCollection, reactionsCollection, pinActionsCollection}
	inserts := bson.D{
		{Key: "operationType", Value: "insert"},
		{Key: "ns.coll", Value: bson.D{{Key: "$in", Value: facts}}},
	}
	reactionChanges := bson.D{
		{Key: "operationType", Value: bson.D{{Key: "$in", Value: bson.A{"update", "replace"}}}},
		{Key: "ns.coll", Value: reactionsCollection},
	}
	return mongo.Pipeline{{{Key: "$match", Value: bson.D{{Key: "$or", Value: bson.A{inserts, reactionChanges}}}}}}
}
```

`apps/core/internal/store/mongostore/feed_change.go`, thay toàn bộ:

```go
package mongostore

import (
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type changeDoc struct {
	Token             bson.Raw       `bson:"_id"`
	OperationType     string         `bson:"operationType"`
	ClusterTime       bson.Timestamp `bson:"clusterTime"`
	WallTime          time.Time      `bson:"wallTime"`
	NS                changeNS       `bson:"ns"`
	DocumentKey       changeKey      `bson:"documentKey"`
	FullDocument      bson.Raw       `bson:"fullDocument"`
	UpdateDescription changeUpdate   `bson:"updateDescription"`
}

type changeNS struct {
	Coll string `bson:"coll"`
}

type changeKey struct {
	ID bson.RawValue `bson:"_id"`
}

type changeUpdate struct {
	UpdatedFields bson.Raw `bson:"updatedFields"`
}

func decodeChange(ev changeDoc) (store.Change, error) {
	switch ev.NS.Coll {
	case messagesCollection:
		var d messageDoc
		if err := bson.Unmarshal(ev.FullDocument, &d); err != nil {
			return store.Change{}, fmt.Errorf("%w: message document: %w", errCorrupt, err)
		}
		m, err := decodeMessage(d)
		if err != nil {
			return store.Change{}, err
		}
		return store.Change{Kind: store.MessageInserted, Msg: m, CommittedAt: ev.WallTime}, nil
	case roomsCollection:
		var d roomDoc
		if err := bson.Unmarshal(ev.FullDocument, &d); err != nil {
			return store.Change{}, fmt.Errorf("%w: room document: %w", errCorrupt, err)
		}
		r, err := decodeRoom(d)
		if err != nil {
			return store.Change{}, err
		}
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
	case reactionsCollection:
		return decodeReactionChange(ev)
	case pinActionsCollection:
		return decodePinChange(ev)
	default:
		return store.Change{}, fmt.Errorf("%w: change on collection %q", errCorrupt, ev.NS.Coll)
	}
}
```

`apps/core/internal/store/mongostore/feed_reaction_change.go`:

```go
package mongostore

import (
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/keys"
)

func decodeReactionChange(ev changeDoc) (store.Change, error) {
	if ev.OperationType == "update" {
		r, err := reactionFromUpdate(ev)
		if err != nil {
			return store.Change{}, err
		}
		return store.Change{Kind: store.ReactionChanged, Reaction: r, CommittedAt: ev.WallTime}, nil
	}
	var d reactionDoc
	if err := bson.Unmarshal(ev.FullDocument, &d); err != nil {
		return store.Change{}, fmt.Errorf("%w: reaction document: %w", errCorrupt, err)
	}
	r, err := decodeReaction(d)
	if err != nil {
		return store.Change{}, err
	}
	return store.Change{Kind: store.ReactionChanged, Reaction: r, CommittedAt: ev.WallTime}, nil
}

func reactionFromUpdate(ev changeDoc) (domain.Reaction, error) {
	_, id, ok := ev.DocumentKey.ID.BinaryOK()
	if !ok {
		return domain.Reaction{}, fmt.Errorf("%w: reaction update without a binary _id", errCorrupt)
	}
	room, thread, seq, user, err := keys.ParseReaction(id)
	if err != nil {
		return domain.Reaction{}, fmt.Errorf("%w: reaction update _id: %w", errCorrupt, err)
	}
	if err := domain.ValidUser(user); err != nil {
		return domain.Reaction{}, fmt.Errorf("%w: reaction update user: %w", errCorrupt, err)
	}
	raw, ok := ev.UpdateDescription.UpdatedFields.Lookup("n").AsInt64OK()
	if !ok {
		return domain.Reaction{}, fmt.Errorf("%w: reaction update without a numeric n", errCorrupt)
	}
	n, err := narrowUint32("reaction change", raw)
	if err != nil {
		return domain.Reaction{}, err
	}
	if n == 0 {
		return domain.Reaction{}, fmt.Errorf("%w: reaction update with change 0", errCorrupt)
	}
	return domain.Reaction{Room: room, Thread: thread, Seq: seq, User: user, N: n}, nil
}

func decodePinChange(ev changeDoc) (store.Change, error) {
	var d pinActionDoc
	if err := bson.Unmarshal(ev.FullDocument, &d); err != nil {
		return store.Change{}, fmt.Errorf("%w: pin document: %w", errCorrupt, err)
	}
	a, err := decodePinAction(d)
	if err != nil {
		return store.Change{}, err
	}
	return store.Change{Kind: store.PinInserted, Pin: a, CommittedAt: ev.WallTime}, nil
}
```

**Step 5: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/store/..."` rồi `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/reconcile/... ./apps/core/internal/work/..."`
Expected: PASS (`-count=5` vì log chung có thêm hai kho ghi). Các test feed cũ (`TestDecodeChangeReadsMessagesAndRooms`, `TestDecodeChangeRejectsOtherCollectionsAndBrokenDocuments`, `TestFeedContract`, `TestEditFeedContract`) không đổi: `OperationType` rỗng ở `changeOn` đi nhánh tài liệu đầy đủ. `make lint` sạch. `wc -l apps/core/internal/store/memstore/*.go apps/core/internal/store/mongostore/feed*.go` mỗi file < 200 (`memstore/feed.go` ≤ 120, `change_log.go` ≤ 90).

**Step 6: Integration**

Run: `make itest`
Expected: PASS, gồm `TestMongoReactionFeedContract` (insert → update → update ra đúng thứ tự, no-op không có event; nếu Mongo ghi update thành `replace` thì nhánh `fullDocument` xử lý), `TestMongoPinFeedContract`, `TestFeedSkipsSummaryPinActivityEditHideAndClearWrites`, `TestMongoFeedContract`, `TestMongoEditFeedContract` và itest của `apps/core` (reader drop có đếm nếu có reaction; chưa có). Event update của reaction thiếu `n` trong `updatedFields` (decode báo corrupt) → dừng, báo cáo kèm event thô, không bật `updateLookup`.

**Step 7: Commit**

INDEXES.csv:
- dòng `apps/core/internal/store/memstore`: thay `in-memory change feed over one insert log of messages and (when NewFeed is given them) rooms and edit facts,` bằng `in-memory change feed over one insert log of messages and (when NewFeed is given them) rooms and edit facts, plus reaction changes and pin facts with the WithReactions/WithPins options (no-op writes are not logged),`; trong key symbols thay `NewFeed;Feed;` bằng `NewFeed;FeedOption;WithReactions;WithPins;Feed;`.
- dòng `apps/core/internal/store/storetest`: thay `edit feed (RunEditFeed: edit inserts in commit order with their content);` bằng `edit feed (RunEditFeed: edit inserts in commit order with their content); reaction feed (RunReactionFeed: every reaction change in commit order with key, user and change number, no-ops add none); pin feed (RunPinFeed: facts in commit order with their content, a refused append adds none);`; trong key symbols thay `RunEditFeed;` bằng `RunEditFeed;RunReactionFeed;RunPinFeed;`.
- dòng `apps/core/internal/store/mongostore`: thay `(inserts into messages, rooms and message_edits, decoded by collection into MessageInserted/RoomInserted/EditInserted;` bằng `(inserts into messages, rooms, message_edits, reactions and pin_actions plus updates and replaces of reactions only, never updateLookup; decoded by collection into MessageInserted/RoomInserted/EditInserted/ReactionChanged/PinInserted; a reaction update is read from documentKey._id and updatedFields.n (int32/int64/double), a bad key, user or n is a corrupt change;`; cột decisions thêm `;D91` vào cuối.

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/store/storetest/feed_reaction_cases.go apps/core/internal/store/mongostore/feed_reaction_change.go apps/core/internal/store/mongostore/feed_reaction_change_test.go apps/core/internal/store/mongostore/feed_skip_integration_test.go
git commit -m "feat(store): feed reaction changes and pin facts" -- apps/core/internal/store/ INDEXES.csv
```

---

### Task 7: ★ `work.Record` (đuôi user, id `x:`/`p:`, `KnownKind`) + `Nak` kind lạ + sửa `room_activity` + reader chuyển hai kind mới. **Push**

Rủi ro cao: đổi định dạng record trên `CHATIM_WORK` và cách worker xử lý record không đọc được. Một reviewer.

- **Layout** (D91): `kind(1) room(8) thread(8) seq(8) version(4) unixNano(8) [userLen(1) user(userLen)]`, big-endian. Đuôi chỉ cho `ReactionChanged` (`Version` = change number `n`); `PinInserted` dùng `Seq = pv`, thread 0, version 0, không đuôi. Record 37 byte cũ giải mã như trước. `Encode` chỉ ghi đuôi khi `1 ≤ len(User) ≤ 64` (chặn biên trước khi đổi sang byte, gosec G115).
- **`Decode`:** độ dài `37` hoặc `38 + L` với `L = b[37] ∈ 1..64`; khác → `ErrBadRecord`. Kind 0 → `ErrBadRecord` (không producer nào ghi kind 0; nó là rác, không phải kind của core mới hơn). Kind khác 0 không có trong `KnownKind` nhưng đúng dạng → `ErrUnknownKind` (bọc `ErrBadRecord`). `ReactionChanged` phải có đuôi qua `domain.ValidUser`; kind khác có đuôi → `ErrBadRecord`.
- **Queue:** `NewQueue(js, stream, partition, retryDelay)`; `collect`: `ErrUnknownKind` → `NakWithDelay(retryDelay)` và đếm `Deferred` (core mới hơn trong rolling deploy sẽ xử lý), lỗi khác → `Term`, đếm `Terminated`. `BadRecordsError{Terminated, Deferred}`; `effects` cộng cả hai vào `work_failures_total`. `effects_wiring.go` truyền `cfg.Effects.RetryDelay` (`WORK_RETRY_DELAY`, mặc định 5s từ config). Không giới hạn số lần `Nak` (consumer không có `MaxDeliver`): kind lạ chỉ xuất hiện khi rolling deploy còn core cũ, và `ChatimWorkFailing` kêu suốt lúc đó là tín hiệu đúng (còn core chưa nâng); ghi vào D91. Task 7 sửa thêm dòng `WORK_RETRY_DELAY` của README: Nak dùng cả cho record kind lạ (thêm `README.md` vào pathspec commit).
- **Sửa lỗi `room_activity` (bắt buộc trước khi record mới tới worker):** `latestActivity` để `Seq = 0` cho **mọi** kind trừ `MessageInserted`; khoá gộp `activityKey{room, thread, msg bool}`. Nếu không, record reaction (seq của tin) và record ghim (`Seq = pv`) sẽ đẩy `ls`/`lm` của room.
- **Registry tạm:** `store.ReactionChanged: {activity.Effect()}`, `store.PinInserted: {activity.Effect()}`. Task 13 thêm effect còn lại theo thứ tự trong "Ghi chú tích hợp".
- **Reader:** không sửa `term.go`: `forward()` đã lọc bằng `work.KnownKind`, nay nhận hai kind mới. Test mới chốt record id `x:`/`p:` và nội dung.

Tương thích (ghi D91, Task 17): core cũ `Term` record 38+ byte và kind 4/5. Rolling deploy phải nâng mọi core trước khi có record mới; prod chưa live.

`work/record.go` sẽ vượt 200 dòng nếu giữ codec trong đó: tách `Encode`/`Decode`/`unixNano` sang file mới `work/record_codec.go`.

**Files:**
- Modify: `apps/core/internal/work/record.go` (thay toàn bộ)
- Create: `apps/core/internal/work/record_codec.go`
- Modify: `apps/core/internal/work/queue.go`
- Modify: `apps/core/internal/work/record_test.go`, `apps/core/internal/work/nats_integration_test.go`
- Create: `apps/core/internal/work/record_tail_test.go`, `apps/core/internal/work/queue_test.go`, `apps/core/internal/work/nats_defer_integration_test.go`
- Modify: `apps/core/internal/effects/room_activity.go`, `apps/core/internal/effects/partition.go`, `apps/core/internal/effects/room_activity_test.go`, `apps/core/internal/effects/failures_test.go`
- Modify: `apps/core/effects_wiring.go`
- Modify: `apps/core/internal/reconcile/harness_test.go`
- Create: `apps/core/internal/reconcile/reaction_forward_test.go`
- Modify: `INDEXES.csv` (dòng `apps/core/internal/work`, `apps/core/internal/effects`, `apps/core/internal/reconcile`, `apps/core`)
- Modify: `README.md` (dòng env `WORK_RETRY_DELAY`)

**Step 1: Test work**

`apps/core/internal/work/record_test.go`:
- trong `TestRecordOfKeepsOnlyKeysAndCommitTime` thêm trước dấu `}` cuối hàm:

```go
	reaction := store.Change{Kind: store.ReactionChanged, Reaction: domain.Reaction{Room: 42, Thread: 3, Seq: 9, User: "bob", N: 4, Emoji: "👍"}, CommittedAt: committed}
	if got, want := work.RecordOf(reaction), (work.Record{Kind: store.ReactionChanged, Room: 42, Thread: 3, Seq: 9, Version: 4, User: "bob", CommittedAt: committed}); got != want {
		t.Fatalf("RecordOf(reaction) = %+v, want %+v", got, want)
	}
	pin := store.Change{Kind: store.PinInserted, Pin: domain.PinAction{Room: 42, PV: 6, Op: domain.PinOpPin, Thread: 3, Seq: 9, By: "bob"}, CommittedAt: committed}
	if got, want := work.RecordOf(pin), (work.Record{Kind: store.PinInserted, Room: 42, Seq: 6, CommittedAt: committed}); got != want {
		t.Fatalf("RecordOf(pin) = %+v, want %+v", got, want)
	}
```

- trong `TestIDsAreNaturalKeys`, sau dòng `"thread edit": ...` thêm:

```go
		"reaction":       {work.Record{Kind: store.ReactionChanged, Room: 42, Seq: 7, Version: 3, User: "bob"}, "x:42-0-7-bob-n3"},
		"thread reaction": {work.Record{Kind: store.ReactionChanged, Room: 42, Thread: 3, Seq: 9, Version: 1, User: "a-n1"}, "x:42-3-9-a-n1-n1"},
		"pin":            {work.Record{Kind: store.PinInserted, Room: 42, Seq: 5}, "p:42-p5"},
```

(gofmt căn lại cột của map literal.)

- thay toàn bộ `TestKnownKindsAreTheThreeChangeKinds` bằng:

```go
func TestKnownKindsAreTheFiveChangeKinds(t *testing.T) {
	for k := range store.ChangeKind(8) {
		want := k >= store.MessageInserted && k <= store.PinInserted
		if got := work.KnownKind(k); got != want {
			t.Errorf("KnownKind(%d) = %v, want %v", k, got, want)
		}
	}
}
```

`apps/core/internal/work/record_tail_test.go` (`committed` có sẵn ở `record_test.go`):

```go
package work_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestReactionRecordsCarryTheUserTail(t *testing.T) {
	if work.MaxRecordSize != 37+1+64 {
		t.Fatalf("MaxRecordSize = %d, want 102", work.MaxRecordSize)
	}
	for _, user := range []string{"a", "alice", strings.Repeat("Z", 64)} {
		r := work.Record{Kind: store.ReactionChanged, Room: 42, Thread: 3, Seq: 9, Version: 2, User: user, CommittedAt: committed}
		b := work.Encode(r)
		if len(b) != work.RecordSize+1+len(user) || int(b[work.RecordSize]) != len(user) || string(b[work.RecordSize+1:]) != user {
			t.Fatalf("Encode(user %q) = %x, want 37 bytes, the length byte and the user", user, b)
		}
		got, err := work.Decode(b)
		if err != nil || got.Kind != r.Kind || got.Room != 42 || got.Thread != 3 || got.Seq != 9 || got.Version != 2 || got.User != user || !got.CommittedAt.Equal(committed) {
			t.Fatalf("Decode(Encode(%+v)) = %+v, %v", r, got, err)
		}
	}
	pin := work.Encode(work.Record{Kind: store.PinInserted, Room: 42, Seq: 6, CommittedAt: committed})
	if got, err := work.Decode(pin); len(pin) != work.RecordSize || err != nil || got.Seq != 6 || got.User != "" {
		t.Fatalf("pin record = %d bytes, %+v, %v; want 37 bytes with seq 6", len(pin), got, err)
	}
}

func TestDecodeRejectsMalformedTails(t *testing.T) {
	reaction := work.Encode(work.Record{Kind: store.ReactionChanged, Room: 42, Seq: 9, Version: 1, User: "alice", CommittedAt: committed})
	room := work.Encode(work.Record{Kind: store.RoomInserted, Room: 42, CommittedAt: committed})
	badUser := slices.Clone(reaction)
	badUser[work.RecordSize+2] = '.'
	cases := map[string][]byte{
		"reaction without a user":      slices.Clone(reaction[:work.RecordSize]),
		"reaction with a bad user":     badUser,
		"tail of length 0":             append(slices.Clone(room), 0),
		"tail shorter than its length": append(slices.Clone(room), 3, 'a'),
		"tail over 64 bytes":           append(append(slices.Clone(room), 65), strings.Repeat("a", 65)...),
		"room record with a user":      append(slices.Clone(room), 1, 'a'),
	}
	for name, b := range cases {
		if _, err := work.Decode(b); !errors.Is(err, work.ErrBadRecord) || errors.Is(err, work.ErrUnknownKind) {
			t.Errorf("%s: Decode = %v, want ErrBadRecord and not ErrUnknownKind", name, err)
		}
	}
}

func TestDecodeDefersWellFormedUnknownKinds(t *testing.T) {
	future := work.Encode(work.Record{Kind: store.RoomInserted, Room: 42, CommittedAt: committed})
	future[0] = 9
	for name, b := range map[string][]byte{"bare": future, "with a tail": append(slices.Clone(future), 2, 'a', 'b')} {
		_, err := work.Decode(b)
		if !errors.Is(err, work.ErrUnknownKind) || !errors.Is(err, work.ErrBadRecord) || !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: Decode = %v, want ErrUnknownKind", name, err)
		}
	}
	zero := slices.Clone(future)
	zero[0] = 0
	if _, err := work.Decode(zero); !errors.Is(err, work.ErrBadRecord) || errors.Is(err, work.ErrUnknownKind) {
		t.Fatalf("Decode(kind 0) = %v, want a malformed record", err)
	}
}
```

`apps/core/internal/work/queue_test.go`:

```go
package work

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type fakeMsg struct {
	jetstream.Msg
	data    []byte
	settled []string
	delay   time.Duration
}

func (m *fakeMsg) Data() []byte { return m.data }

func (m *fakeMsg) Term() error {
	m.settled = append(m.settled, "term")
	return nil
}

func (m *fakeMsg) NakWithDelay(d time.Duration) error {
	m.settled, m.delay = append(m.settled, "nak"), d
	return nil
}

type fakeBatch struct{ msgs chan jetstream.Msg }

func (b fakeBatch) Messages() <-chan jetstream.Msg { return b.msgs }

func (fakeBatch) Error() error { return nil }

func batchOf(msgs ...*fakeMsg) fakeBatch {
	ch := make(chan jetstream.Msg, len(msgs))
	for _, m := range msgs {
		ch <- m
	}
	close(ch)
	return fakeBatch{msgs: ch}
}

func TestCollectTermsMalformedAndDefersUnknownKinds(t *testing.T) {
	good := &fakeMsg{data: Encode(Record{Kind: store.MessageInserted, Room: 1, Seq: 1})}
	future := Encode(Record{Kind: store.RoomInserted, Room: 2})
	future[0] = 9
	unknown, junk := &fakeMsg{data: future}, &fakeMsg{data: []byte("junk")}
	ds, err := collect(t.Context(), batchOf(good, unknown, junk), 7*time.Second)
	var bad BadRecordsError
	if len(ds) != 1 || ds[0].Record().Room != 1 || !errors.As(err, &bad) || bad != (BadRecordsError{Terminated: 1, Deferred: 1}) || !errors.Is(err, ErrBadRecord) {
		t.Fatalf("collect = %d deliveries, %v; want 1 delivery, 1 terminated and 1 deferred", len(ds), err)
	}
	if !slices.Equal(unknown.settled, []string{"nak"}) || unknown.delay != 7*time.Second {
		t.Fatalf("unknown kind settled %v after %v, want one nak after the retry delay", unknown.settled, unknown.delay)
	}
	if !slices.Equal(junk.settled, []string{"term"}) || len(good.settled) != 0 {
		t.Fatalf("junk settled %v, good settled %v; want term and nothing", junk.settled, good.settled)
	}
}

func TestCollectReportsNothingWhenEveryRecordDecodes(t *testing.T) {
	ds, err := collect(t.Context(), batchOf(&fakeMsg{data: Encode(Record{Kind: store.RoomInserted, Room: 3})}), time.Second)
	if len(ds) != 1 || err != nil {
		t.Fatalf("collect = %d deliveries, %v; want 1 and no error", len(ds), err)
	}
}
```

`apps/core/internal/work/nats_integration_test.go`: thay `q := work.NewQueue(js, cfg.Name, 1)` bằng `q := work.NewQueue(js, cfg.Name, 1, time.Second)` và `ds, err := work.NewQueue(js, cfg.Name, 2).Fetch(t.Context(), 10, 2*time.Second)` bằng `ds, err := work.NewQueue(js, cfg.Name, 2, time.Second).Fetch(t.Context(), 10, 2*time.Second)`.

`apps/core/internal/work/nats_defer_integration_test.go` (`realWork` có sẵn):

```go
package work_test

import (
	"errors"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

func TestRealWorkQueueDefersRecordsOfUnknownKinds(t *testing.T) {
	js, cfg := realWork(t)
	data := work.Encode(work.Record{Kind: store.RoomInserted, Room: 5, CommittedAt: time.Now()})
	data[0] = 9
	future := &nats.Msg{Subject: work.Subject(cfg.SubjectRoot, 3), Data: data, Header: nats.Header{}}
	future.Header.Set(jetstream.MsgIDHeader, "future-1")
	if _, err := js.PublishMsg(t.Context(), future); err != nil {
		t.Fatalf("publish: %v", err)
	}
	q := work.NewQueue(js, cfg.Name, 3, 200*time.Millisecond)
	for round := range 2 {
		ds, err := q.Fetch(t.Context(), 10, 2*time.Second)
		var bad work.BadRecordsError
		if len(ds) != 0 || !errors.As(err, &bad) || bad.Deferred != 1 || bad.Terminated != 0 {
			t.Fatalf("fetch %d = %d deliveries, %v; want one deferred record that comes back", round, len(ds), err)
		}
	}
}
```

**Step 2: Test effects + reader**

`apps/core/internal/effects/room_activity_test.go`, thêm vào cuối:

```go
func TestRoomActivityTouchesOnlyTheChangeTimeForReactionsAndPins(t *testing.T) {
	spy := &touchSpy{}
	t1, t2 := activityAt, activityAt.Add(time.Second)
	recs := []work.Record{
		{Kind: store.ReactionChanged, Room: 101, Seq: 5, Version: 2, User: "bob", CommittedAt: t1},
		{Kind: store.PinInserted, Room: 101, Seq: 9, CommittedAt: t2},
		activityRecord(101, 0, 4, t1),
		{Kind: store.PinInserted, Room: 202, Seq: 3, CommittedAt: t1},
	}
	errs := effects.NewRoomActivity(spy).Effect().Run(t.Context(), recs)
	if len(errs) != len(recs) || slices.ContainsFunc(errs, func(err error) bool { return err != nil }) {
		t.Fatalf("Run errors = %v, want %d nils", errs, len(recs))
	}
	want := []store.Activity{{Room: 101, At: t2}, {Room: 101, Seq: 4, At: t1}, {Room: 202, At: t1}}
	if len(spy.calls) != 1 || !slices.Equal(spy.calls[0], want) {
		t.Fatalf("TouchActivity calls = %+v, want one call with %+v (reactions and pins as seq 0, never the message seq or pv)", spy.calls, want)
	}
}
```

`apps/core/internal/effects/failures_test.go`:
- trong `badOnce.Fetch` thay `return nil, work.BadRecordsError{Terminated: 2}` bằng `return nil, work.BadRecordsError{Terminated: 2, Deferred: 3}`.
- trong `TestUndecodableRecordsCountAsFailures` thay:

```go
		if got := rg.Stats().Failed; got != 2 {
			t.Fatalf("failed = %d, want the 2 terminated records", got)
		}
```

bằng:

```go
		if got := rg.Stats().Failed; got != 5 {
			t.Fatalf("failed = %d, want the 2 terminated and 3 deferred records", got)
		}
```

`apps/core/internal/reconcile/harness_test.go`:
- struct `rig`, sau `edits *memstore.Edits` thêm hai field `reactions *memstore.Reactions` và `pins *memstore.Pins` (gofmt căn cột).
- trong `newRig` thay:

```go
	rg := &rig{msgs: memstore.NewMessages(), rooms: memstore.NewRooms(), edits: memstore.NewEdits(), owner: &owner{}, js: &publishtest.JetStream{}, sink: &testlog.Sink{}}
```

bằng:

```go
	rg := &rig{
		msgs: memstore.NewMessages(), rooms: memstore.NewRooms(), edits: memstore.NewEdits(), reactions: memstore.NewReactions(), pins: memstore.NewPins(),
		owner: &owner{}, js: &publishtest.JetStream{}, sink: &testlog.Sink{},
	}
```

và thay `rg.feed = memstore.NewFeed(rg.msgs, rg.rooms, rg.edits)` bằng `rg.feed = memstore.NewFeed(rg.msgs, rg.rooms, rg.edits, memstore.WithReactions(rg.reactions), memstore.WithPins(rg.pins))`.

`apps/core/internal/reconcile/reaction_forward_test.go`:

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

func (rg *rig) react(t *testing.T, r, seq uint64, user, emoji string) {
	t.Helper()
	x := domain.Reaction{Room: r, Seq: seq, Tenant: tenant, User: user, Emoji: emoji, At: time.Now().UTC()}
	if _, _, err := rg.reactions.Set(t.Context(), x); err != nil {
		t.Fatalf("react %d/%d by %s: %v", r, seq, user, err)
	}
}

func (rg *rig) pin(t *testing.T, r, pv, seq uint64) {
	t.Helper()
	a := domain.PinAction{Room: r, PV: pv, Tenant: tenant, Op: domain.PinOpPin, Seq: seq, By: "alice", At: time.Now().UTC()}
	if err := rg.pins.Append(t.Context(), a); err != nil {
		t.Fatalf("pin %d v%d: %v", r, pv, err)
	}
}

func TestForwardsReactionChangesAndPinFactsAsRecords(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		committed := time.Now()
		rg.react(t, room, 1, "alice", "👍")
		rg.pin(t, room, 1, 1)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, []string{"x:4242-0-1-alice-n1", "p:4242-p1"}) {
			t.Fatalf("stored = %v, want the reaction and the pin record", got)
		}
		stored := rg.js.Stored()
		reaction, err := work.Decode(stored[0].Data)
		if err != nil || reaction.Kind != store.ReactionChanged || reaction.Room != room || reaction.Seq != 1 || reaction.Version != 1 || reaction.User != "alice" || !reaction.CommittedAt.Equal(committed) {
			t.Fatalf("reaction record = %+v, %v; want %d/0/1 n1 by alice at %v", reaction, err, room, committed)
		}
		fact, err := work.Decode(stored[1].Data)
		if err != nil || fact.Kind != store.PinInserted || fact.Room != room || fact.Seq != 1 || fact.User != "" || !fact.CommittedAt.Equal(committed) {
			t.Fatalf("pin record = %+v, %v; want room %d pv 1 at %v", fact, err, room, committed)
		}
		if got := rg.Stats().Dropped; got != 0 {
			t.Fatalf("dropped = %d, want 0", got)
		}
	})
}
```

**Step 3: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/work/... ./apps/core/internal/effects/... ./apps/core/internal/reconcile/..."`
Expected: FAIL biên dịch: `unknown field User in struct literal of type work.Record`, `undefined: work.MaxRecordSize`, `undefined: work.ErrUnknownKind`, `too many arguments in call to collect`, `unknown field Deferred in struct literal of type work.BadRecordsError` (work, effects, reconcile cùng fail ở `work.Record.User`/`Deferred`).

**Step 4: Code work**

`apps/core/internal/work/record.go`, thay toàn bộ:

```go
package work

import (
	"fmt"
	"strconv"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const (
	RecordSize    = 37
	maxUserLen    = 64
	MaxRecordSize = RecordSize + 1 + maxUserLen
)

var (
	ErrBadRecord   = fmt.Errorf("%w: work record", apperr.ErrInvalidArgument)
	ErrUnknownKind = fmt.Errorf("%w: unknown kind", ErrBadRecord)

	epoch = time.Unix(0, 0)
)

type Record struct {
	Kind        store.ChangeKind
	Room        uint64
	Thread      uint64
	Seq         uint64
	Version     uint32
	User        string
	CommittedAt time.Time
}

func KnownKind(k store.ChangeKind) bool {
	switch k {
	case store.MessageInserted, store.RoomInserted, store.EditInserted, store.ReactionChanged, store.PinInserted:
		return true
	default:
		return false
	}
}

func RecordOf(c store.Change) Record {
	r := Record{Kind: c.Kind, CommittedAt: c.CommittedAt}
	switch c.Kind {
	case store.MessageInserted:
		r.Room, r.Thread, r.Seq = c.Msg.Room, c.Msg.Thread, c.Msg.Seq
	case store.RoomInserted:
		r.Room = c.Room.ID
	case store.EditInserted:
		r.Room, r.Thread, r.Seq, r.Version = c.Edit.Room, c.Edit.Thread, c.Edit.Seq, c.Edit.Version
	case store.ReactionChanged:
		x := c.Reaction
		r.Room, r.Thread, r.Seq, r.Version, r.User = x.Room, x.Thread, x.Seq, x.N, x.User
	case store.PinInserted:
		r.Room, r.Seq = c.Pin.Room, c.Pin.PV
	}
	return r
}

func (r Record) ID() string {
	switch r.Kind {
	case store.MessageInserted:
		return "m:" + pbconv.MessageEventID(r.Room, r.Thread, r.Seq)
	case store.RoomInserted:
		return "r:" + pbconv.RoomID(r.Room)
	case store.EditInserted:
		return "e:" + pbconv.MessageChangeEventID(r.Room, r.Thread, r.Seq, r.Version)
	case store.ReactionChanged:
		return "x:" + pbconv.ReactionEventID(r.Room, r.Thread, r.Seq, r.User, r.Version)
	case store.PinInserted:
		return "p:" + pbconv.PinEventID(r.Room, r.Seq)
	default:
		return ""
	}
}

func Partition(room uint64, partitions int) int {
	return int(slotmap.Of(room)) % partitions
}

func Subject(root string, partition int) string {
	return root + ".p" + strconv.Itoa(partition)
}

func Message(root string, partitions int, r Record) *nats.Msg {
	m := &nats.Msg{Subject: Subject(root, Partition(r.Room, partitions)), Data: Encode(r), Header: nats.Header{}}
	m.Header.Set(jetstream.MsgIDHeader, r.ID())
	return m
}
```

`apps/core/internal/work/record_codec.go`:

```go
package work

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func Encode(r Record) []byte {
	b := make([]byte, 1, RecordSize+1+len(r.User))
	b[0] = byte(r.Kind)
	b = binary.BigEndian.AppendUint64(b, r.Room)
	b = binary.BigEndian.AppendUint64(b, r.Thread)
	b = binary.BigEndian.AppendUint64(b, r.Seq)
	b = binary.BigEndian.AppendUint32(b, r.Version)
	b = binary.BigEndian.AppendUint64(b, unixNano(r.CommittedAt))
	return appendUser(b, r.User)
}

func appendUser(b []byte, user string) []byte {
	n := len(user)
	if n < 1 || n > maxUserLen {
		return b
	}
	return append(append(b, byte(n)), user...)
}

func Decode(b []byte) (Record, error) {
	if len(b) < RecordSize || len(b) > MaxRecordSize {
		return Record{}, fmt.Errorf("%w: %d bytes, want %d to %d", ErrBadRecord, len(b), RecordSize, MaxRecordSize)
	}
	user, err := userTail(b[RecordSize:])
	if err != nil {
		return Record{}, err
	}
	ns := binary.BigEndian.Uint64(b[29:])
	if ns > math.MaxInt64 {
		return Record{}, fmt.Errorf("%w: commit time out of range", ErrBadRecord)
	}
	r := Record{
		Kind:        store.ChangeKind(b[0]),
		Room:        binary.BigEndian.Uint64(b[1:]),
		Thread:      binary.BigEndian.Uint64(b[9:]),
		Seq:         binary.BigEndian.Uint64(b[17:]),
		Version:     binary.BigEndian.Uint32(b[25:]),
		User:        user,
		CommittedAt: time.Unix(0, int64(ns)).UTC(),
	}
	if err := checkKind(r); err != nil {
		return Record{}, err
	}
	return r, nil
}

func userTail(tail []byte) (string, error) {
	if len(tail) == 0 {
		return "", nil
	}
	if n := int(tail[0]); n < 1 || n > maxUserLen || len(tail) != 1+n {
		return "", fmt.Errorf("%w: user tail of %d bytes", ErrBadRecord, len(tail))
	}
	return string(tail[1:]), nil
}

func checkKind(r Record) error {
	switch {
	case r.Kind == 0:
		return fmt.Errorf("%w: kind 0", ErrBadRecord)
	case !KnownKind(r.Kind):
		return fmt.Errorf("%w %d", ErrUnknownKind, r.Kind)
	case r.Kind == store.ReactionChanged && domain.ValidUser(r.User) != nil:
		return fmt.Errorf("%w: reaction record without a valid user", ErrBadRecord)
	case r.Kind != store.ReactionChanged && r.User != "":
		return fmt.Errorf("%w: kind %d carries a user", ErrBadRecord, r.Kind)
	default:
		return nil
	}
}

func unixNano(t time.Time) uint64 {
	if t.Before(epoch) {
		return 0
	}
	ns := t.UnixNano()
	if ns < 0 {
		return 0
	}
	return uint64(ns)
}
```

`apps/core/internal/work/queue.go`:
- thay:

```go
type BadRecordsError struct {
	Terminated uint64
}

func (e BadRecordsError) Error() string {
	return strconv.FormatUint(e.Terminated, 10) + " undecodable work records terminated"
}
```

bằng:

```go
type BadRecordsError struct {
	Terminated uint64
	Deferred   uint64
}

func (e BadRecordsError) Error() string {
	return strconv.FormatUint(e.Terminated, 10) + " undecodable work records terminated, " +
		strconv.FormatUint(e.Deferred, 10) + " records of unknown kinds deferred"
}

func (e BadRecordsError) orNil() error {
	if e.Terminated == 0 && e.Deferred == 0 {
		return nil
	}
	return e
}
```

- thay:

```go
type jetStreamQueue struct {
	js        jetstream.JetStream
	stream    string
	partition int
	consumer  jetstream.Consumer
}

func NewQueue(js jetstream.JetStream, stream string, partition int) Queue {
	return &jetStreamQueue{js: js, stream: stream, partition: partition}
}
```

bằng:

```go
type jetStreamQueue struct {
	js         jetstream.JetStream
	stream     string
	partition  int
	retryDelay time.Duration
	consumer   jetstream.Consumer
}

func NewQueue(js jetstream.JetStream, stream string, partition int, retryDelay time.Duration) Queue {
	return &jetStreamQueue{js: js, stream: stream, partition: partition, retryDelay: retryDelay}
}
```

- trong `Fetch` thay `return collect(ctx, batch)` bằng `return collect(ctx, batch, q.retryDelay)`.
- thay toàn bộ `collect` và `badRecords`:

```go
func collect(ctx context.Context, batch jetstream.MessageBatch) ([]Delivery, error) {
	var out []Delivery
	var bad uint64
	msgs := batch.Messages()
	for {
		select {
		case m, open := <-msgs:
			if !open {
				return out, errors.Join(fetchError(ctx, batch.Error()), badRecords(bad))
			}
			r, err := Decode(m.Data())
			if err != nil {
				_ = m.Term()
				bad++
				continue
			}
			out = append(out, jetStreamDelivery{msg: m, rec: r})
		case <-ctx.Done():
			return out, ctx.Err()
		}
	}
}
```

và

```go
func badRecords(n uint64) error {
	if n == 0 {
		return nil
	}
	return BadRecordsError{Terminated: n}
}
```

bằng (một hàm, `badRecords` bỏ hẳn):

```go
func collect(ctx context.Context, batch jetstream.MessageBatch, retryDelay time.Duration) ([]Delivery, error) {
	var out []Delivery
	var bad BadRecordsError
	msgs := batch.Messages()
	for {
		select {
		case m, open := <-msgs:
			if !open {
				return out, errors.Join(fetchError(ctx, batch.Error()), bad.orNil())
			}
			r, err := Decode(m.Data())
			switch {
			case errors.Is(err, ErrUnknownKind):
				_ = m.NakWithDelay(retryDelay)
				bad.Deferred++
			case err != nil:
				_ = m.Term()
				bad.Terminated++
			default:
				out = append(out, jetStreamDelivery{msg: m, rec: r})
			}
		case <-ctx.Done():
			return out, ctx.Err()
		}
	}
}
```

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/work/..."`
Expected: PASS (nats integration skip). `wc -l apps/core/internal/work/*.go`: `record.go` ≤ 115, `record_codec.go` ≤ 100, `queue.go` ≤ 125.

**Step 5: Code effects + wiring**

`apps/core/internal/effects/partition.go`, trong `fetchFailed` thay `w.failed.Add(bad.Terminated)` bằng `w.failed.Add(bad.Terminated + bad.Deferred)`.

`apps/core/internal/effects/room_activity.go`:
- thay:

```go
type activityKey struct {
	room, thread uint64
	edit         bool
}
```

bằng:

```go
type activityKey struct {
	room, thread uint64
	msg          bool
}
```

- trong `latestActivity` thay:

```go
		edit := r.Kind == store.EditInserted
		seq := r.Seq
		if edit {
			seq = 0
		}
		k := activityKey{r.Room, r.Thread, edit}
```

bằng:

```go
		msg := r.Kind == store.MessageInserted
		seq := uint64(0)
		if msg {
			seq = r.Seq
		}
		k := activityKey{r.Room, r.Thread, msg}
```

`apps/core/effects_wiring.go`:
- thay:

```go
		store.EditInserted:    {activity.Effect(), fx.editProjection.Effect(), fx.msgChanged.Effect()},
	}
```

bằng:

```go
		store.EditInserted:    {activity.Effect(), fx.editProjection.Effect(), fx.msgChanged.Effect()},
		store.ReactionChanged: {activity.Effect()},
		store.PinInserted:     {activity.Effect()},
	}
```

- thay `Queue:    func(p int) work.Queue { return work.NewQueue(cl.effectsJS, cfg.Work.Name, p) },` bằng `Queue:    func(p int) work.Queue { return work.NewQueue(cl.effectsJS, cfg.Work.Name, p, cfg.Effects.RetryDelay) },`.

**Step 6: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/work/... ./apps/core/internal/effects/... ./apps/core/internal/reconcile/... ./apps/core/internal/resync/... ./apps/core/..."`
Expected: PASS. `TestRoomActivityTouchesOnlyTheChangeTimeForEdits` (cũ) vẫn xanh: edit giờ đi qua khoá `msg=false`, cùng thứ tự. `TestForwardsAnEditInsertAsAnEditRecord` vẫn xanh (record edit không đuôi, 37 byte). `wc -l apps/core/internal/reconcile/harness_test.go` ≤ 175, `apps/core/internal/effects/room_activity_test.go` ≤ 130.

Run: `make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/effects/ ./apps/core/internal/reconcile/"`
Expected: PASS (worker và reader có goroutine; chạy lặp theo quy tắc).

**Step 7: Integration**

Run: `make itest`
Expected: PASS, gồm `TestRealWorkQueueDefersRecordsOfUnknownKinds` (record kind 9 không bị Term, quay lại sau 200ms, vẫn được đếm `Deferred`), `TestRealWorkQueueFetchesAcksAndRedeliversNaks`, `TestRealWorkQueueTerminatesUndecodableRecords` và itest của `apps/core` (wiring đọc `cfg.Effects.RetryDelay`).

**Step 7b: README**

`README.md`, dòng `WORK_RETRY_DELAY`, cột mô tả: đổi "Record có effect lỗi được `Nak` với delay này rồi giao lại (đếm vào `work_failures_total`)" thành "Record có effect lỗi, hoặc record kind lạ do core mới hơn ghi trong lúc rolling deploy, được `Nak` với delay này rồi giao lại (đếm vào `work_failures_total`)".

**Step 8: Commit + push**

INDEXES.csv:
- dòng `apps/core/internal/work`: thay `Record holds only kind + room/thread/seq + version + CommittedAt in 37 big-endian bytes; KnownKind lists the change kinds the stream carries (message, room, edit)` bằng `Record holds only kind + room/thread/seq + version + CommittedAt in 37 big-endian bytes plus a user tail (length byte + 1-64 bytes, up to MaxRecordSize) only for ReactionChanged (version = change number); PinInserted carries pv as seq; KnownKind lists the change kinds the stream carries (message, room, edit, reaction, pin); Decode (record_codec.go): malformed or kind 0 -> ErrBadRecord, well-formed unknown kind -> ErrUnknownKind`; thay `natural ids m:{room}-{thread}-{seq}, r:{room} and e:{room}-{thread}-{seq}-v{version} become Nats-Msg-Id` bằng `natural ids m:{room}-{thread}-{seq}, r:{room}, e:{room}-{thread}-{seq}-v{version}, x:{room}-{thread}-{seq}-{user}-n{change} and p:{room}-p{pv} become Nats-Msg-Id`; thay `undecodable records are terminated and reported as BadRecordsError` bằng `undecodable records are terminated and records of unknown kinds are naked after the retry delay given to NewQueue, both reported as BadRecordsError (Terminated, Deferred)`; trong key symbols thay `Record;RecordSize;KnownKind;` bằng `Record;RecordSize;MaxRecordSize;ErrUnknownKind;KnownKind;`; cột decisions thay `D66;D79;D80;D84` bằng `D66;D79;D80;D84;D91`.
- dòng `apps/core/internal/effects`: thay `undecodable records count as failures;` bằng `undecodable and deferred unknown-kind records count as failures;`; thay `room_activity also runs on EditInserted as seq 0 activity (bumps only the change time and bucket, kept apart from new messages of the same timeline);` bằng `room_activity uses seq 0 for every kind except MessageInserted (edits, reactions and pins bump only the change time and bucket, kept apart from new messages of the same timeline);`; cột decisions thêm `;D91` vào cuối.
- dòng `apps/core/internal/reconcile`: thay `turns every MessageInserted/RoomInserted/EditInserted change (work.KnownKind)` bằng `turns every MessageInserted/RoomInserted/EditInserted/ReactionChanged/PinInserted change (work.KnownKind)`; cột decisions thêm `;D91` vào cuối.
- dòng `apps/core`: thay `EditInserted -> room_activity, edit_projection, msg_changed)` bằng `EditInserted -> room_activity, edit_projection, msg_changed; ReactionChanged and PinInserted -> room_activity until the M2b.3 effects land; work queues nak unknown kinds after WORK_RETRY_DELAY)`; cột decisions thêm `;D91` vào cuối.

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/work/record_codec.go apps/core/internal/work/record_tail_test.go apps/core/internal/work/queue_test.go apps/core/internal/work/nats_defer_integration_test.go apps/core/internal/reconcile/reaction_forward_test.go
git commit -m "feat(work): carry reaction and pin records and defer unknown kinds" -- apps/core/internal/work/ apps/core/internal/effects/room_activity.go apps/core/internal/effects/partition.go apps/core/internal/effects/room_activity_test.go apps/core/internal/effects/failures_test.go apps/core/effects_wiring.go apps/core/internal/reconcile/ INDEXES.csv README.md
git push origin feat/m2b
```

Expected: push thành công; `git status --short` không còn file nào của task.

---

### Task 8: Package `counter` (touch) + `pinproj` (Current/Project), test trên memstore

Hai helper dùng chung cho fast path (`mutate`, Task 9/10) và worker (`effects`, Task 13), để `effects` không import `mutate`. Cả hai chỉ phụ thuộc port nhỏ; lỗi store trả nguyên hoặc bọc bằng `%w` (Part B dùng `errors.Is` với `domain.ErrRoomNotFound`, `domain.ErrMessageNotFound`, `store.ErrStaleRead`).

- **`counter.Toucher.Touch(key, cur, witnesses, tries)`** (D90): mỗi lượt `Count(key, witnesses)` → bằng `cur.Counts` (`slices.Equal`, nil = rỗng) thì `(cur, false, nil)` (không ghi, không bump `v`) → `SetReactions(key, cur.Version, {counts, cur.Version+1})` → khớp thì `(mới, true, nil)` → trượt thì `Find` lại tin (không còn → `domain.ErrMessageNotFound`) lấy `cur` mới, lượt sau đếm lại. Hết `tries` → `ErrContended`. `ErrStaleRead` và lỗi store trả nguyên. `tries < 1` → `ErrInvalidArgument`.
- **`pinproj.Projector`** (D92): `Current(room)` = `PinState` + lặp `After(room, s.Version, MaxPinScan)` + `domain.FoldPins` tới khi trang ngắn hơn `MaxPinScan` (hoặc fold không tiến, chặn lặp vô hạn khi adapter lỗi); không ghi. `Project(room, target)`: tối đa `MaxTries` lượt: đọc state `p`; `p.Version ≥ target` → trả `p` (không ghi); fold; kết quả `< target` → `store.ErrStaleRead` (fact chưa thấy được); `ApplyPins(room, p.Version, folded)` khớp → trả; trượt → lượt sau. Hết lượt → `ErrContended`.
- Wiring (Part B, theo tinh chỉnh hợp đồng ở Task 5): `counter.New(st, st.Reactions())`, `pinproj.New(st.Pins(), st)`; test dùng `memstore.NewMessages()`/`NewReactions()`, `NewPins()`/`NewRooms()`.

**Files:**
- Create: `apps/core/internal/counter/counter.go`, `apps/core/internal/counter/counter_test.go`
- Create: `apps/core/internal/pinproj/pinproj.go`, `apps/core/internal/pinproj/pinproj_test.go`, `apps/core/internal/pinproj/fixtures_test.go`
- Modify: `INDEXES.csv` (hai dòng mới)

**Step 1: Test**

`apps/core/internal/counter/counter_test.go`:

```go
package counter_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/counter"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var (
	at        = time.UnixMilli(1_700_000_000_000).UTC()
	key       = store.MsgKey{Room: 42, Seq: 1}
	errBroken = errors.New("store broken")
)

type rig struct {
	msgs      *memstore.Messages
	reactions *memstore.Reactions
}

func newRig(t *testing.T) rig {
	t.Helper()
	rg := rig{msgs: memstore.NewMessages(), reactions: memstore.NewReactions()}
	m := domain.Message{Room: 42, Seq: 1, Tenant: "acme", From: "alice", Kind: domain.KindText, Text: "hi", CID: "c-1", CreatedAt: at}
	if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("Insert: %+v", res)
	}
	return rg
}

func (rg rig) react(t *testing.T, user, emoji string) store.Witness {
	t.Helper()
	r, _, err := rg.reactions.Set(t.Context(), domain.Reaction{Room: 42, Seq: 1, Tenant: "acme", User: user, Emoji: emoji, At: at})
	if err != nil {
		t.Fatalf("Set(%s by %s): %v", emoji, user, err)
	}
	return store.Witness{User: user, N: r.N}
}

func (rg rig) stored(t *testing.T) domain.ReactionSummary {
	t.Helper()
	got, err := rg.msgs.Find(t.Context(), 42, []store.MsgKey{key})
	if err != nil || len(got) != 1 {
		t.Fatalf("Find = %+v, %v", got, err)
	}
	return got[0].Reactions
}

func toucher(t *testing.T, msgs counter.Messages, reactions counter.Reactions) *counter.Toucher {
	t.Helper()
	tc, err := counter.New(msgs, reactions)
	if err != nil {
		t.Fatalf("counter.New: %v", err)
	}
	return tc
}

func same(a, b domain.ReactionSummary) bool {
	return a.Version == b.Version && slices.Equal(a.Counts, b.Counts)
}

type racer struct {
	*memstore.Messages
	raced bool
}

func (r *racer) SetReactions(ctx context.Context, k store.MsgKey, base uint64, s domain.ReactionSummary) (bool, error) {
	if !r.raced {
		r.raced = true
		if _, err := r.Messages.SetReactions(ctx, k, base, domain.ReactionSummary{Version: base + 1}); err != nil {
			return false, err
		}
	}
	return r.Messages.SetReactions(ctx, k, base, s)
}

type loser struct {
	*memstore.Messages
	tries int
	gone  bool
}

func (l *loser) SetReactions(context.Context, store.MsgKey, uint64, domain.ReactionSummary) (bool, error) {
	l.tries++
	return false, nil
}

func (l *loser) Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error) {
	if l.gone {
		return nil, nil
	}
	return l.Messages.Find(ctx, room, keys)
}

type brokenCount struct{}

func (brokenCount) Count(context.Context, store.MsgKey, []store.Witness) ([]domain.ReactionCount, error) {
	return nil, errBroken
}

func TestTouchWritesTheRecountAndBumpsTheVersion(t *testing.T) {
	rg := newRig(t)
	w := rg.react(t, "alice", "👍")
	rg.react(t, "bob", "👍")
	rg.react(t, "carol", "❤️")
	got, bumped, err := toucher(t, rg.msgs, rg.reactions).Touch(t.Context(), key, domain.ReactionSummary{}, []store.Witness{w}, 3)
	want := domain.ReactionSummary{Counts: []domain.ReactionCount{{Emoji: "👍", Count: 2}, {Emoji: "❤️", Count: 1}}, Version: 1}
	if err != nil || !bumped || !same(got, want) || !same(rg.stored(t), want) {
		t.Fatalf("Touch = %+v, %v, %v; stored %+v; want %+v bumped", got, bumped, err, rg.stored(t), want)
	}
}

func TestTouchWithAnEqualCountWritesNothing(t *testing.T) {
	rg := newRig(t)
	tc := toucher(t, rg.msgs, rg.reactions)
	if got, bumped, err := tc.Touch(t.Context(), key, domain.ReactionSummary{}, nil, 3); err != nil || bumped || !same(got, domain.ReactionSummary{}) {
		t.Fatalf("Touch(no reactions) = %+v, %v, %v; want the zero summary unchanged", got, bumped, err)
	}
	rg.react(t, "alice", "👍")
	first, _, err := tc.Touch(t.Context(), key, domain.ReactionSummary{}, nil, 3)
	if err != nil {
		t.Fatalf("Touch: %v", err)
	}
	got, bumped, err := tc.Touch(t.Context(), key, first, nil, 3)
	if err != nil || bumped || !same(got, first) || rg.stored(t).Version != 1 {
		t.Fatalf("Touch(equal) = %+v, %v, %v; stored v%d; want %+v and no write", got, bumped, err, rg.stored(t).Version, first)
	}
}

func TestTouchRereadsTheSummaryAfterALostCAS(t *testing.T) {
	rg := newRig(t)
	rg.react(t, "alice", "👍")
	got, bumped, err := toucher(t, &racer{Messages: rg.msgs}, rg.reactions).Touch(t.Context(), key, domain.ReactionSummary{}, nil, 3)
	want := domain.ReactionSummary{Counts: []domain.ReactionCount{{Emoji: "👍", Count: 1}}, Version: 2}
	if err != nil || !bumped || !same(got, want) || !same(rg.stored(t), want) {
		t.Fatalf("Touch after a rival write = %+v, %v, %v; want %+v", got, bumped, err, want)
	}
}

func TestTouchGivesUpAfterItsTries(t *testing.T) {
	rg := newRig(t)
	rg.react(t, "alice", "👍")
	l := &loser{Messages: rg.msgs}
	_, bumped, err := toucher(t, l, rg.reactions).Touch(t.Context(), key, domain.ReactionSummary{}, nil, 3)
	if !errors.Is(err, counter.ErrContended) || !errors.Is(err, apperr.ErrUnavailable) || bumped || l.tries != 3 {
		t.Fatalf("Touch = %v, bumped %v after %d CAS tries; want ErrContended after 3", err, bumped, l.tries)
	}
}

func TestTouchReportsAMessageThatIsGone(t *testing.T) {
	rg := newRig(t)
	rg.react(t, "alice", "👍")
	l := &loser{Messages: rg.msgs, gone: true}
	if _, _, err := toucher(t, l, rg.reactions).Touch(t.Context(), key, domain.ReactionSummary{}, nil, 3); !errors.Is(err, domain.ErrMessageNotFound) || l.tries != 1 {
		t.Fatalf("Touch = %v after %d tries, want ErrMessageNotFound after 1", err, l.tries)
	}
}

func TestTouchStopsOnAStaleWitnessAndPassesStoreErrors(t *testing.T) {
	rg := newRig(t)
	rg.react(t, "alice", "👍")
	_, bumped, err := toucher(t, rg.msgs, rg.reactions).Touch(t.Context(), key, domain.ReactionSummary{}, []store.Witness{{User: "alice", N: 2}}, 3)
	if !errors.Is(err, store.ErrStaleRead) || bumped || rg.stored(t).Version != 0 {
		t.Fatalf("Touch(stale witness) = %v, bumped %v, stored v%d; want ErrStaleRead and no write", err, bumped, rg.stored(t).Version)
	}
	if _, _, err := toucher(t, rg.msgs, brokenCount{}).Touch(t.Context(), key, domain.ReactionSummary{}, nil, 3); !errors.Is(err, errBroken) {
		t.Fatalf("Touch(broken count) = %v, want %v", err, errBroken)
	}
}

func TestNewAndTouchRejectBadInput(t *testing.T) {
	if _, err := counter.New(nil, memstore.NewReactions()); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("New(nil messages) = %v, want ErrInvalidArgument", err)
	}
	if _, err := counter.New(memstore.NewMessages(), nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("New(nil reactions) = %v, want ErrInvalidArgument", err)
	}
	rg := newRig(t)
	if _, _, err := toucher(t, rg.msgs, rg.reactions).Touch(t.Context(), key, domain.ReactionSummary{}, nil, 0); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("Touch(0 tries) = %v, want ErrInvalidArgument", err)
	}
}
```

`apps/core/internal/pinproj/fixtures_test.go`:

```go
package pinproj_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

const room uint64 = 42

var at = time.UnixMilli(1_700_000_000_000).UTC()

func newRooms(t *testing.T) *memstore.Rooms {
	t.Helper()
	rooms := memstore.NewRooms()
	r := domain.Room{ID: room, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: at, MemberCount: 1}
	owner := domain.Member{Room: room, Tenant: "acme", User: "alice", Role: domain.RoleOwner, JoinedAt: at}
	if err := rooms.Create(t.Context(), r, []domain.Member{owner}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return rooms
}

func fact(pv uint64, op domain.PinOp, seq uint64) domain.PinAction {
	return domain.PinAction{Room: room, PV: pv, Tenant: "acme", Op: op, Seq: seq, By: "alice", At: at}
}

func pinned(pv, seq uint64) domain.Pin { return domain.Pin{Seq: seq, By: "alice", At: at, PV: pv} }

func appendFacts(t *testing.T, pins *memstore.Pins, facts ...domain.PinAction) {
	t.Helper()
	for _, a := range facts {
		if err := pins.Append(t.Context(), a); err != nil {
			t.Fatalf("Append(v%d): %v", a.PV, err)
		}
	}
}

func same(a, b domain.PinState) bool { return a.Version == b.Version && slices.Equal(a.Pins, b.Pins) }

func projector(t *testing.T, facts pinproj.Facts, rooms store.PinProjector) *pinproj.Projector {
	t.Helper()
	p, err := pinproj.New(facts, rooms)
	if err != nil {
		t.Fatalf("pinproj.New: %v", err)
	}
	return p
}

func storedState(t *testing.T, rooms store.PinProjector) domain.PinState {
	t.Helper()
	s, err := rooms.PinState(t.Context(), room)
	if err != nil {
		t.Fatalf("PinState: %v", err)
	}
	return s
}

type countingRooms struct {
	*memstore.Rooms
	applies, lose int
}

func (c *countingRooms) ApplyPins(ctx context.Context, r, base uint64, s domain.PinState) (bool, error) {
	c.applies++
	if c.lose > 0 {
		c.lose--
		return false, nil
	}
	return c.Rooms.ApplyPins(ctx, r, base, s)
}

type rival struct {
	*memstore.Rooms
	raced bool
}

func (r *rival) ApplyPins(ctx context.Context, id, base uint64, s domain.PinState) (bool, error) {
	if !r.raced {
		r.raced = true
		if _, err := r.Rooms.ApplyPins(ctx, id, base, s); err != nil {
			return false, err
		}
		return false, nil
	}
	return r.Rooms.ApplyPins(ctx, id, base, s)
}
```

`apps/core/internal/pinproj/pinproj_test.go`:

```go
package pinproj_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestCurrentFoldsFactsAfterTheStoredVersionWithoutWriting(t *testing.T) {
	rooms, pins := newRooms(t), memstore.NewPins()
	p := projector(t, pins, rooms)
	if got, err := p.Current(t.Context(), room); err != nil || !same(got, domain.PinState{}) {
		t.Fatalf("Current(no facts) = %+v, %v; want the zero state", got, err)
	}
	appendFacts(t, pins, fact(1, domain.PinOpPin, 3), fact(2, domain.PinOpPin, 5), fact(3, domain.PinOpUnpin, 3))
	got, err := p.Current(t.Context(), room)
	if want := (domain.PinState{Pins: []domain.Pin{pinned(2, 5)}, Version: 3}); err != nil || !same(got, want) {
		t.Fatalf("Current = %+v, %v; want %+v", got, err, want)
	}
	if s := storedState(t, rooms); s.Version != 0 {
		t.Fatalf("stored state = %+v, Current must not write", s)
	}
}

func TestProjectWritesTheFoldUpToTheTarget(t *testing.T) {
	rooms, pins := newRooms(t), memstore.NewPins()
	p := projector(t, pins, rooms)
	appendFacts(t, pins, fact(1, domain.PinOpPin, 3), fact(2, domain.PinOpPin, 5))
	want := domain.PinState{Pins: []domain.Pin{pinned(2, 5), pinned(1, 3)}, Version: 2}
	if got, err := p.Project(t.Context(), room, 2); err != nil || !same(got, want) || !same(storedState(t, rooms), want) {
		t.Fatalf("Project(2) = %+v, %v; stored %+v; want %+v", got, err, storedState(t, rooms), want)
	}
	appendFacts(t, pins, fact(3, domain.PinOpUnpin, 5))
	want = domain.PinState{Pins: []domain.Pin{pinned(1, 3)}, Version: 3}
	if got, err := p.Project(t.Context(), room, 3); err != nil || !same(got, want) || !same(storedState(t, rooms), want) {
		t.Fatalf("Project(3) = %+v, %v; want %+v", got, err, want)
	}
}

func TestProjectAtOrPastTheTargetWritesNothing(t *testing.T) {
	rooms, pins := &countingRooms{Rooms: newRooms(t)}, memstore.NewPins()
	p := projector(t, pins, rooms)
	appendFacts(t, pins, fact(1, domain.PinOpPin, 3), fact(2, domain.PinOpPin, 5))
	for _, target := range []uint64{2, 1, 2} {
		if got, err := p.Project(t.Context(), room, target); err != nil || got.Version != 2 {
			t.Fatalf("Project(%d) = %+v, %v; want version 2", target, got, err)
		}
	}
	if rooms.applies != 1 {
		t.Fatalf("ApplyPins ran %d times, want once", rooms.applies)
	}
}

func TestProjectIsStaleBeforeTheTargetFactIsVisible(t *testing.T) {
	rooms, pins := newRooms(t), memstore.NewPins()
	appendFacts(t, pins, fact(1, domain.PinOpPin, 3))
	_, err := projector(t, pins, rooms).Project(t.Context(), room, 2)
	if !errors.Is(err, store.ErrStaleRead) || storedState(t, rooms).Version != 0 {
		t.Fatalf("Project(2) with one fact = %v, stored %+v; want ErrStaleRead and no write", err, storedState(t, rooms))
	}
}

func TestProjectRereadsAfterALostCAS(t *testing.T) {
	rooms, pins := newRooms(t), memstore.NewPins()
	appendFacts(t, pins, fact(1, domain.PinOpPin, 3))
	got, err := projector(t, pins, &rival{Rooms: rooms}).Project(t.Context(), room, 1)
	if want := (domain.PinState{Pins: []domain.Pin{pinned(1, 3)}, Version: 1}); err != nil || !same(got, want) {
		t.Fatalf("Project after a rival write = %+v, %v; want %+v", got, err, want)
	}
}

func TestProjectGivesUpAfterMaxTries(t *testing.T) {
	rooms, pins := &countingRooms{Rooms: newRooms(t), lose: pinproj.MaxTries}, memstore.NewPins()
	appendFacts(t, pins, fact(1, domain.PinOpPin, 3))
	_, err := projector(t, pins, rooms).Project(t.Context(), room, 1)
	if !errors.Is(err, pinproj.ErrContended) || !errors.Is(err, apperr.ErrUnavailable) || rooms.applies != pinproj.MaxTries {
		t.Fatalf("Project = %v after %d CAS tries, want ErrContended after %d", err, rooms.applies, pinproj.MaxTries)
	}
}

func TestCurrentPagesThroughMoreThanOneScan(t *testing.T) {
	rooms, pins := newRooms(t), memstore.NewPins()
	for pv := uint64(1); pv <= store.MaxPinScan+1; pv++ {
		op := domain.PinOpPin
		if pv%2 == 0 {
			op = domain.PinOpUnpin
		}
		appendFacts(t, pins, fact(pv, op, 3))
	}
	got, err := projector(t, pins, rooms).Current(t.Context(), room)
	if want := (domain.PinState{Pins: []domain.Pin{pinned(store.MaxPinScan+1, 3)}, Version: store.MaxPinScan + 1}); err != nil || !same(got, want) {
		t.Fatalf("Current over %d facts = %+v, %v; want %+v", store.MaxPinScan+1, got, err, want)
	}
}

func TestMissingRoomAndMissingDeps(t *testing.T) {
	p := projector(t, memstore.NewPins(), memstore.NewRooms())
	if _, err := p.Current(t.Context(), room); !errors.Is(err, domain.ErrRoomNotFound) {
		t.Fatalf("Current(missing room) = %v, want ErrRoomNotFound", err)
	}
	if _, err := p.Project(t.Context(), room, 1); !errors.Is(err, domain.ErrRoomNotFound) {
		t.Fatalf("Project(missing room) = %v, want ErrRoomNotFound", err)
	}
	if _, err := pinproj.New(nil, memstore.NewRooms()); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("New(nil facts) = %v, want ErrInvalidArgument", err)
	}
	if _, err := pinproj.New(memstore.NewPins(), nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("New(nil rooms) = %v, want ErrInvalidArgument", err)
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/counter/... ./apps/core/internal/pinproj/..."`
Expected: FAIL: `no non-test Go files in .../apps/core/internal/counter` và `.../pinproj` (hoặc `undefined: counter.New`, `undefined: pinproj.New`).

**Step 3: Code**

`apps/core/internal/counter/counter.go`:

```go
package counter

import (
	"context"
	"fmt"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type Messages interface {
	Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error)
	store.ReactionSummaries
}

type Reactions interface {
	Count(ctx context.Context, key store.MsgKey, witnesses []store.Witness) ([]domain.ReactionCount, error)
}

var (
	ErrContended = fmt.Errorf("reaction summary contended: %w", apperr.ErrUnavailable)

	errMissingDeps = fmt.Errorf("%w: counter needs messages and reactions", apperr.ErrInvalidArgument)
	errNoTries     = fmt.Errorf("%w: touch needs at least one try", apperr.ErrInvalidArgument)
)

type Toucher struct {
	msgs      Messages
	reactions Reactions
}

func New(msgs Messages, reactions Reactions) (*Toucher, error) {
	if msgs == nil || reactions == nil {
		return nil, errMissingDeps
	}
	return &Toucher{msgs: msgs, reactions: reactions}, nil
}

func (t *Toucher) Touch(ctx context.Context, key store.MsgKey, cur domain.ReactionSummary, witnesses []store.Witness, tries int) (domain.ReactionSummary, bool, error) {
	if tries < 1 {
		return cur, false, errNoTries
	}
	for range tries {
		counts, err := t.reactions.Count(ctx, key, witnesses)
		if err != nil {
			return cur, false, err
		}
		if slices.Equal(counts, cur.Counts) {
			return cur, false, nil
		}
		next := domain.ReactionSummary{Counts: counts, Version: cur.Version + 1}
		ok, err := t.msgs.SetReactions(ctx, key, cur.Version, next)
		if err != nil {
			return cur, false, err
		}
		if ok {
			return next, true, nil
		}
		if cur, err = t.reload(ctx, key); err != nil {
			return cur, false, err
		}
	}
	return cur, false, fmt.Errorf("reactions of %d/%d/%d after %d tries: %w", key.Room, key.Thread, key.Seq, tries, ErrContended)
}

func (t *Toucher) reload(ctx context.Context, key store.MsgKey) (domain.ReactionSummary, error) {
	msgs, err := t.msgs.Find(ctx, key.Room, []store.MsgKey{key})
	if err != nil {
		return domain.ReactionSummary{}, err
	}
	if len(msgs) == 0 {
		return domain.ReactionSummary{}, fmt.Errorf("reactions of %d/%d/%d: %w", key.Room, key.Thread, key.Seq, domain.ErrMessageNotFound)
	}
	return msgs[0].Reactions, nil
}
```

`apps/core/internal/pinproj/pinproj.go`:

```go
package pinproj

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const MaxTries = 5

type Facts interface {
	After(ctx context.Context, room, pv uint64, limit int) ([]domain.PinAction, error)
}

var (
	ErrContended = fmt.Errorf("pin projection contended: %w", apperr.ErrUnavailable)

	errMissingDeps = fmt.Errorf("%w: pin projector needs facts and rooms", apperr.ErrInvalidArgument)
)

type Projector struct {
	facts Facts
	rooms store.PinProjector
}

func New(facts Facts, rooms store.PinProjector) (*Projector, error) {
	if facts == nil || rooms == nil {
		return nil, errMissingDeps
	}
	return &Projector{facts: facts, rooms: rooms}, nil
}

func (p *Projector) Current(ctx context.Context, room uint64) (domain.PinState, error) {
	s, err := p.rooms.PinState(ctx, room)
	if err != nil {
		return domain.PinState{}, fmt.Errorf("current pins of room %d: %w", room, err)
	}
	return p.fold(ctx, room, s)
}

func (p *Projector) Project(ctx context.Context, room, target uint64) (domain.PinState, error) {
	for range MaxTries {
		base, err := p.rooms.PinState(ctx, room)
		if err != nil {
			return domain.PinState{}, fmt.Errorf("project pins of room %d: %w", room, err)
		}
		if base.Version >= target {
			return base, nil
		}
		folded, err := p.fold(ctx, room, base)
		if err != nil {
			return domain.PinState{}, err
		}
		if folded.Version < target {
			return domain.PinState{}, fmt.Errorf("pins of room %d reach v%d, want v%d: %w", room, folded.Version, target, store.ErrStaleRead)
		}
		ok, err := p.rooms.ApplyPins(ctx, room, base.Version, folded)
		if err != nil {
			return domain.PinState{}, fmt.Errorf("project pins of room %d: %w", room, err)
		}
		if ok {
			return folded, nil
		}
	}
	return domain.PinState{}, fmt.Errorf("pins of room %d to v%d: %w", room, target, ErrContended)
}

func (p *Projector) fold(ctx context.Context, room uint64, s domain.PinState) (domain.PinState, error) {
	for {
		facts, err := p.facts.After(ctx, room, s.Version, store.MaxPinScan)
		if err != nil {
			return domain.PinState{}, fmt.Errorf("pin facts of room %d after v%d: %w", room, s.Version, err)
		}
		next := domain.FoldPins(s, facts)
		if len(facts) < store.MaxPinScan || next.Version == s.Version {
			return next, nil
		}
		s = next
	}
}
```

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/counter/... ./apps/core/internal/pinproj/..."`
Expected: PASS. Không có goroutine mới, không cần `-count=5`. `wc -l apps/core/internal/counter/*.go apps/core/internal/pinproj/*.go` mỗi file < 200 (`counter_test.go` ~185, `pinproj_test.go` ~120).

**Step 5: INDEXES + commit**

INDEXES.csv, thêm hai dòng ngay sau dòng `apps/core/internal/mutate`:

```csv
apps/core/internal/counter,package,"Reaction summary touch shared by the mutate fast path and the effect worker (D90): Count with witnesses (store.ErrStaleRead while a witness doc is behind), equal to the given summary -> no write and no version bump, else SetReactions CAS on rx.v (base 0 = missing) with version + 1; a lost CAS rereads the message (gone -> domain.ErrMessageNotFound) and recounts, up to the given tries, then ErrContended; store errors pass through",Messages;Reactions;Toucher;New;Toucher.Touch;ErrContended,apps/core/internal/mutate;apps/core/internal/effects;apps/core,unit (memstore),D67;D90
apps/core/internal/pinproj,package,"Pin projection shared by the mutate fast path and the effect worker (D92): Current = PinState + pin facts after its version folded with domain.FoldPins page by page (MaxPinScan), no write; Project(room, target) = read state, return it when already at target, fold, store.ErrStaleRead when the fold stays below target, ApplyPins CAS on pv (base 0 = missing), retry a lost CAS up to MaxTries then ErrContended; store errors wrapped with %w",Facts;Projector;New;Projector.Current;Projector.Project;MaxTries;ErrContended,apps/core/internal/mutate;apps/core/internal/effects;apps/core,unit (memstore),D62;D92
```

Run: `python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"`
Expected: `{7}`.

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/counter/counter.go apps/core/internal/counter/counter_test.go apps/core/internal/pinproj/pinproj.go apps/core/internal/pinproj/pinproj_test.go apps/core/internal/pinproj/fixtures_test.go
git commit -m "feat(core): add the reaction counter touch and the pin projector" -- apps/core/internal/counter/ apps/core/internal/pinproj/ INDEXES.csv
```

---

### Task 9: ★ `access` (3 action) + `mutate.React` + Deps (`Reactions`, `Counter`, `Limits`)

Reaction đi đường lệnh đổi như sửa/xoá (D82): không qua actor, vào room bằng `access.Checker.Admit`, đọc tin, hỏi policy bằng `Checker.Allow` với `Author = msg.From`, `Kind = msg.Kind` (dùng lại `Mutator.target`). Ba action mới `react_message`, `pin_message`, `unpin_message` không cần sửa logic `DefaultPolicy` (nó chỉ chặn edit/delete), nên member nào cũng được, kể cả trên loại tin bị khoá bởi `MESSAGE_LOCKED_KINDS` (D94); test chốt cả hai điều đó.

`React` (D89, D90), đúng thứ tự hợp đồng:
1. `validKey` (seq ≠ 0, thread 0) → emoji khác rỗng thì `domain.ValidateEmoji`.
2. `target(ReactMessage)`: `Admit` → `Find` → `Allow`.
3. Emoji khác rỗng: tin `Deleted` → `ErrMessageDeleted`; emoji chưa có trong `msg.Reactions.Counts` và đã đủ `Limits.MaxEmojis` loại → `ErrTooManyEmojis` (giới hạn mềm: đọc từ `rx`, hai lệnh đồng thời có thể vượt một chút). Gỡ (`""`) bỏ qua cả hai kiểm, nên tin đã xoá vẫn gỡ được.
4. `Reactions.Set` (hoặc `Remove` khi emoji rỗng) với `At = now()`. Không đổi (`changed == false`) → trả `{doc.N, msg.Reactions}`, không event, không touch.
5. Đổi → enqueue `pbconv.ReactionChanged` → `Counter.Touch(key, msg.Reactions, [{User, doc.N}], FastTouchTries)`; lỗi (`ErrStaleRead`, `counter.ErrContended`, lỗi store) bị bỏ qua và giữ `msg.Reactions`, worker `reaction_counter` (Task 13) hội tụ. Touch có bump → enqueue `pbconv.CountsChanged(room.Type, msg với summary mới, now())`. Lỗi enqueue luôn bỏ qua.

`Limits` theo mẫu `Config.Validate` của các component: `Validate()` = `withDefaults().validate()`, nên `Limits{}` hợp lệ (20 emoji, 50 ghim); `mutate.New` lưu bản đã điền mặc định. `PinLimit` khai báo ở task này để `Limits` đủ hợp đồng; Task 10 mới dùng.

`mutate.New` bắt buộc `Reactions` và `Counter`, nên task này sửa luôn mọi chỗ dựng `mutate.Deps`: rig của `mutate`, `TestNewRequiresEveryDependency`, harness `grpcsrv` (`newMutator`, `memStores`, `caller_identity_test.go`) và `apps/core/service_wiring.go`.

**Hợp đồng đã chỉnh (controller, G1):** `*mongostore.Store` đã có `Get` (rooms), `At`/`Append`/`Between` (edits), nên không cài trực tiếp `store.Reactions`/`store.Pins`. Part A thêm `func (s *Store) Reactions() *mongostore.Reactions` và `func (s *Store) Pins() *mongostore.Pins` (cài hai port đó); `SetReactions`, `PinState`, `ApplyPins` vẫn trên `*Store`. Wiring dùng `st.Reactions()`/`st.Pins()` cho port `Reactions`/`Pins` và reader của effect (`counter.New(st, st.Reactions())`, `pinproj.New(st.Pins(), st)`). Test trên memstore không đổi.

**Files:**
- Modify: `apps/core/internal/access/policy.go`
- Modify: `apps/core/internal/access/default_policy_test.go`
- Create: `apps/core/internal/mutate/limits.go`
- Create: `apps/core/internal/mutate/react.go`
- Modify: `apps/core/internal/mutate/mutator.go`
- Modify: `apps/core/internal/mutate/fixtures_test.go`
- Modify: `apps/core/internal/mutate/delete_test.go`
- Create: `apps/core/internal/mutate/react_test.go`
- Create: `apps/core/internal/mutate/react_rules_test.go`
- Modify: `apps/core/internal/grpcsrv/harness_test.go`, `fake_dependencies_test.go`, `caller_identity_test.go`
- Modify: `apps/core/service_wiring.go`
- Modify: `INDEXES.csv`

**Step 1: Test `access`**

`apps/core/internal/access/default_policy_test.go`, thêm cuối file:

```go
func TestDefaultPolicyLetsMembersReactAndPinAnyMessage(t *testing.T) {
	names := map[access.Action]string{
		access.ReactMessage: "react_message",
		access.PinMessage:   "pin_message",
		access.UnpinMessage: "unpin_message",
	}
	policies := map[string]access.DefaultPolicy{
		"nothing locked": {},
		"text locked":    {LockedKinds: []domain.Kind{domain.KindText}},
	}
	for action, name := range names {
		if string(action) != name {
			t.Fatalf("action %q, want %q", action, name)
		}
		for label, p := range policies {
			req := access.Request{Action: action, User: "bob", Author: "alice", Kind: domain.KindText}
			if err := p.Check(t.Context(), req); err != nil {
				t.Fatalf("%s: %s by bob on alice's message = %v, want nil", label, action, err)
			}
		}
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/access/..."`
Expected: FAIL biên dịch: `undefined: access.ReactMessage`, `undefined: access.PinMessage`, `undefined: access.UnpinMessage`.

**Step 3: Code `access`**

`apps/core/internal/access/policy.go`, thay khối `const` của `Action` bằng:

```go
const (
	ReadHistory     Action = "read_history"
	SendMessage     Action = "send_message"
	EditMessage     Action = "edit_message"
	DeleteMessage   Action = "delete_message"
	HideMessage     Action = "hide_message"
	ClearHistory    Action = "clear_history"
	ReadEditHistory Action = "read_edit_history"
	ReactMessage    Action = "react_message"
	PinMessage      Action = "pin_message"
	UnpinMessage    Action = "unpin_message"
)
```

`DefaultPolicy.Check` giữ nguyên.

**Step 4: Chạy, thấy pass + commit**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/access/..."`
Expected: PASS.

`INDEXES.csv`, dòng `apps/core/internal/access`:
- purpose: sau "DefaultPolicy.LockedKinds denies edit/delete of those kinds even to the author (D87)" nối "; react_message, pin_message and unpin_message are allowed to every member by DefaultPolicy, also on locked kinds (D94)";
- key_symbols: sau `ReadEditHistory` thêm `;ReactMessage;PinMessage;UnpinMessage`;
- decisions: `D65;D77;D86;D87` → `D65;D77;D86;D87;D94`.

```bash
make fmt-check && make vet && make lint
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
git commit -m "feat(access): add react, pin and unpin actions for members" -- apps/core/internal/access/ INDEXES.csv
```

Expected: `{7}`; `git show --stat HEAD` chỉ có 3 file.

**Step 5: Test `mutate`**

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
	m         *mutate.Mutator
	msgs      *memstore.Messages
	rooms     *memstore.Rooms
	edits     *memstore.Edits
	hidden    *memstore.Hidden
	reactions *memstore.Reactions
	events    *recordingEvents
	now       time.Time
}

func newRig(t *testing.T, policy access.Policy) *rig {
	t.Helper()
	rg := &rig{
		msgs: memstore.NewMessages(), rooms: memstore.NewRooms(), edits: memstore.NewEdits(), hidden: memstore.NewHidden(),
		reactions: memstore.NewReactions(), events: &recordingEvents{}, now: created.Add(time.Minute + 1500*time.Microsecond),
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
	return mutate.Deps{
		Access: checker, Messages: rg.msgs, Edits: rg.edits, Hidden: rg.hidden, Rooms: rg.rooms, Events: rg.events,
		Reactions: rg.reactions, Counter: counts, Now: func() time.Time { return rg.now },
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

`rg.mutator(t, policy, edits)` giữ chữ ký cũ nên `edit_test.go` (`laggingEdits`) không đổi.

`apps/core/internal/mutate/delete_test.go`: bỏ import `".../internal/access"` (chỉ hàm dưới dùng), rồi thay nguyên `TestNewRequiresEveryDependency` bằng:

```go
func TestNewRequiresEveryDependency(t *testing.T) {
	rg := newRig(t, nil)
	full := rg.deps(t, nil)
	full.Now = nil
	for name, drop := range map[string]func(d *mutate.Deps){
		"no access":    func(d *mutate.Deps) { d.Access = nil },
		"no messages":  func(d *mutate.Deps) { d.Messages = nil },
		"no edits":     func(d *mutate.Deps) { d.Edits = nil },
		"no hidden":    func(d *mutate.Deps) { d.Hidden = nil },
		"no rooms":     func(d *mutate.Deps) { d.Rooms = nil },
		"no events":    func(d *mutate.Deps) { d.Events = nil },
		"no reactions": func(d *mutate.Deps) { d.Reactions = nil },
		"no counter":   func(d *mutate.Deps) { d.Counter = nil },
		"bad limits":   func(d *mutate.Deps) { d.Limits = mutate.Limits{MaxEmojis: mutate.MaxEmojisCap + 1} },
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

`apps/core/internal/mutate/react_test.go`:

```go
package mutate_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func react(user string, seq uint64, emoji string) mutate.ReactCmd {
	return mutate.ReactCmd{Tenant: tenant, User: user, Room: room, Seq: seq, Emoji: emoji}
}

func counts(v uint64, cs ...domain.ReactionCount) domain.ReactionSummary {
	return domain.ReactionSummary{Counts: cs, Version: v}
}

func sameSummary(a, b domain.ReactionSummary) bool {
	return a.Version == b.Version && slices.Equal(a.Counts, b.Counts)
}

func (rg *rig) reaction(t *testing.T, seq uint64, user string) (domain.Reaction, bool) {
	t.Helper()
	doc, found, err := rg.reactions.Get(t.Context(), key(seq), user)
	if err != nil {
		t.Fatalf("reaction of %s on seq %d: %v", user, seq, err)
	}
	return doc, found
}

func (rg *rig) mustReact(t *testing.T, c mutate.ReactCmd) mutate.ReactResult {
	t.Helper()
	got, err := rg.m.React(t.Context(), c)
	if err != nil {
		t.Fatalf("%s reacts %q on seq %d: %v", c.User, c.Emoji, c.Seq, err)
	}
	return got
}

func TestReactWritesTheEmojiAndCountsItBeforeTheAck(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	got := rg.mustReact(t, react("bob", 1, "👍"))
	want := counts(1, domain.ReactionCount{Emoji: "👍", Count: 1})
	if got.Change != 1 || !sameSummary(got.Reactions, want) {
		t.Fatalf("React = %+v, want change 1 with %+v", got, want)
	}
	stored := rg.stored(t, 1)
	if !sameSummary(stored.Reactions, want) {
		t.Fatalf("stored summary = %+v, want %+v written by the inline touch", stored.Reactions, want)
	}
	doc, _ := rg.reaction(t, 1, "bob")
	if doc.Emoji != "👍" || doc.Prev != "" || doc.N != 1 || doc.Tenant != tenant || !doc.At.Equal(rg.at()) {
		t.Fatalf("reaction = %+v, want 👍 as change 1 at %v", doc, rg.at())
	}
	rooms, events := rg.events.list()
	changed, counted := pbconv.ReactionChanged(domain.RoomGroup, doc), pbconv.CountsChanged(domain.RoomGroup, stored, rg.at())
	if !slices.Equal(rooms, []uint64{room, room}) || len(events) != 2 || !proto.Equal(events[0], changed) || !proto.Equal(events[1], counted) {
		t.Fatalf("enqueued %v %v, want reaction_changed then counts_changed", rooms, events)
	}
	if events[0].GetId() != pbconv.ReactionEventID(room, 0, 1, "bob", 1) || events[1].GetId() != pbconv.ReactionCountsEventID(room, 0, 1, 1) {
		t.Fatalf("event ids = %q, %q", events[0].GetId(), events[1].GetId())
	}
}

func TestReactWithTheSameEmojiChangesNothing(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	first := rg.mustReact(t, react("bob", 1, "👍"))
	at := rg.at()
	rg.now = rg.now.Add(time.Second)
	again := rg.mustReact(t, react("bob", 1, "👍"))
	if again.Change != 1 || !sameSummary(again.Reactions, first.Reactions) {
		t.Fatalf("again = %+v, want %+v", again, first)
	}
	if doc, _ := rg.reaction(t, 1, "bob"); doc.N != 1 || !doc.At.Equal(at) {
		t.Fatalf("reaction = %+v, want the first write kept", doc)
	}
	if _, events := rg.events.list(); len(events) != 2 {
		t.Fatalf("a no-op enqueued events: %d in total, want the first 2", len(events))
	}
}

func TestReactReplacesTheEmojiOfTheUser(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	rg.mustReact(t, react("bob", 1, "👍"))
	got := rg.mustReact(t, react("bob", 1, "❤️"))
	want := counts(2, domain.ReactionCount{Emoji: "❤️", Count: 1})
	if got.Change != 2 || !sameSummary(got.Reactions, want) {
		t.Fatalf("React = %+v, want change 2 with %+v", got, want)
	}
	doc, _ := rg.reaction(t, 1, "bob")
	if doc.Emoji != "❤️" || doc.Prev != "👍" || doc.N != 2 {
		t.Fatalf("reaction = %+v, want ❤️ replacing 👍 as change 2", doc)
	}
	_, events := rg.events.list()
	if len(events) != 4 || !proto.Equal(events[2], pbconv.ReactionChanged(domain.RoomGroup, doc)) || events[3].GetId() != pbconv.ReactionCountsEventID(room, 0, 1, 2) {
		t.Fatalf("events = %v, want reaction_changed n2 then counts_changed v2", events)
	}
}

func TestRemovingAReactionLeavesATombstone(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	rg.mustReact(t, react("bob", 1, "👍"))
	got := rg.mustReact(t, react("bob", 1, ""))
	if got.Change != 2 || got.Reactions.Version != 2 || len(got.Reactions.Counts) != 0 {
		t.Fatalf("remove = %+v, want change 2 with no counts at version 2", got)
	}
	if doc, found := rg.reaction(t, 1, "bob"); !found || doc.Emoji != "" || doc.Prev != "👍" || doc.N != 2 {
		t.Fatalf("reaction = %+v (found %v), want a tombstone after 👍 as change 2", doc, found)
	}
	_, events := rg.events.list()
	if len(events) != 4 || events[2].GetReactionChanged().GetEmoji() != "" || events[2].GetReactionChanged().GetPreviousEmoji() != "👍" {
		t.Fatalf("events = %v, want a removal event after 👍", events)
	}
	none := rg.mustReact(t, react("carol", 1, ""))
	if none.Change != 0 || none.Reactions.Version != 2 {
		t.Fatalf("remove without a reaction = %+v, want change 0 and the current summary", none)
	}
	if _, found := rg.reaction(t, 1, "carol"); found {
		t.Fatalf("removing nothing wrote a tombstone")
	}
	if _, events := rg.events.list(); len(events) != 4 {
		t.Fatalf("removing nothing enqueued events: %d in total, want 4", len(events))
	}
}

func TestADeletedMessageTakesOnlyRemovals(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	rg.mustReact(t, react("bob", 1, "👍"))
	if _, err := rg.m.Delete(t.Context(), del("alice", 1, 0)); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	for _, c := range []mutate.ReactCmd{react("bob", 1, "❤️"), react("carol", 1, "👍")} {
		if _, err := rg.m.React(t.Context(), c); !errors.Is(err, domain.ErrMessageDeleted) || !errors.Is(err, apperr.ErrFailedPrecondition) {
			t.Fatalf("%s reacts %q on a deleted message = %v, want ErrMessageDeleted", c.User, c.Emoji, err)
		}
	}
	if got := rg.mustReact(t, react("bob", 1, "")); got.Change != 2 {
		t.Fatalf("removal on a deleted message = %+v, want change 2", got)
	}
}
```

`apps/core/internal/mutate/react_rules_test.go`:

```go
package mutate_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/counter"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type touchCall struct {
	key       store.MsgKey
	cur       domain.ReactionSummary
	witnesses []store.Witness
	tries     int
}

type scriptedCounter struct {
	inner mutate.CounterToucher
	err   error
	calls []touchCall
}

func (c *scriptedCounter) Touch(ctx context.Context, k store.MsgKey, cur domain.ReactionSummary, ws []store.Witness, tries int) (domain.ReactionSummary, bool, error) {
	c.calls = append(c.calls, touchCall{key: k, cur: cur, witnesses: slices.Clone(ws), tries: tries})
	if c.err != nil {
		return domain.ReactionSummary{}, false, c.err
	}
	return c.inner.Touch(ctx, k, cur, ws, tries)
}

func TestTheEmojiLimitCountsDistinctEmojisOfAMessage(t *testing.T) {
	rg := newRig(t, nil)
	d := rg.deps(t, nil)
	d.Limits = mutate.Limits{MaxEmojis: 2}
	rg.m = rg.build(t, d)
	rg.send(t, 1, "alice", "hi")
	rg.send(t, 2, "alice", "ho")
	rg.mustReact(t, react("alice", 1, "👍"))
	rg.mustReact(t, react("bob", 1, "❤️"))
	if _, err := rg.m.React(t.Context(), react("carol", 1, "😂")); !errors.Is(err, domain.ErrTooManyEmojis) || !errors.Is(err, apperr.ErrFailedPrecondition) {
		t.Fatalf("third emoji = %v, want ErrTooManyEmojis", err)
	}
	if _, found := rg.reaction(t, 1, "carol"); found {
		t.Fatalf("a refused emoji was written")
	}
	got := rg.mustReact(t, react("carol", 1, "👍"))
	if got.Reactions.Version != 3 || !slices.Contains(got.Reactions.Counts, domain.ReactionCount{Emoji: "👍", Count: 2}) {
		t.Fatalf("known emoji at the limit = %+v, want 👍 counted twice at version 3", got)
	}
	rg.mustReact(t, react("carol", 2, "😂"))
}

func TestReactRejectsBadInputBeforeWriting(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	threaded := react("bob", 1, "👍")
	threaded.Thread = 1
	cases := []struct {
		name string
		cmd  mutate.ReactCmd
		want error
	}{
		{"control character", react("bob", 1, "\u0007"), apperr.ErrInvalidArgument},
		{"longer than 32 bytes", react("bob", 1, strings.Repeat("a", 33)), apperr.ErrInvalidArgument},
		{"not utf-8", react("bob", 1, "\xff"), apperr.ErrInvalidArgument},
		{"thread", threaded, apperr.ErrInvalidArgument},
		{"zero seq", react("bob", 0, "👍"), apperr.ErrInvalidArgument},
		{"unknown message", react("bob", 9, "👍"), domain.ErrMessageNotFound},
		{"stranger", react("mallory", 1, "👍"), domain.ErrNotMember},
	}
	for _, c := range cases {
		if _, err := rg.m.React(t.Context(), c.cmd); !errors.Is(err, c.want) {
			t.Fatalf("%s: React = %v, want %v", c.name, err, c.want)
		}
	}
	if _, found := rg.reaction(t, 1, "bob"); found {
		t.Fatalf("a refused reaction was written")
	}
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("refused reactions enqueued %v", events)
	}
}

func TestReactTouchesTheCounterWithItsOwnWrite(t *testing.T) {
	rg := newRig(t, nil)
	d := rg.deps(t, nil)
	spy := &scriptedCounter{inner: d.Counter}
	d.Counter = spy
	rg.m = rg.build(t, d)
	rg.send(t, 1, "alice", "hi")
	rg.mustReact(t, react("bob", 1, "👍"))
	rg.mustReact(t, react("bob", 1, "❤️"))
	rg.mustReact(t, react("bob", 1, "❤️"))
	if len(spy.calls) != 2 {
		t.Fatalf("touched %d times, want 2 (none for the no-op)", len(spy.calls))
	}
	last := spy.calls[1]
	if last.key != key(1) || last.tries != mutate.FastTouchTries || !slices.Equal(last.witnesses, []store.Witness{{User: "bob", N: 2}}) ||
		!sameSummary(last.cur, counts(1, domain.ReactionCount{Emoji: "👍", Count: 1})) {
		t.Fatalf("touch = %+v, want seq 1, bob's change 2 as the witness, %d tries and the summary read before the write", last, mutate.FastTouchTries)
	}
}

func TestATouchFailureDoesNotFailTheReaction(t *testing.T) {
	for _, cause := range []error{store.ErrStaleRead, counter.ErrContended, errBoom} {
		rg := newRig(t, nil)
		d := rg.deps(t, nil)
		d.Counter = &scriptedCounter{err: cause}
		rg.m = rg.build(t, d)
		rg.send(t, 1, "alice", "hi")
		got := rg.mustReact(t, react("bob", 1, "👍"))
		if got.Change != 1 || !sameSummary(got.Reactions, domain.ReactionSummary{}) {
			t.Fatalf("%v: React = %+v, want the write acked with the summary it read", cause, got)
		}
		if s := rg.stored(t, 1); s.Reactions.Version != 0 {
			t.Fatalf("%v: summary %+v written without a touch", cause, s.Reactions)
		}
		if _, events := rg.events.list(); len(events) != 1 || events[0].GetReactionChanged() == nil {
			t.Fatalf("%v: events = %v, want only reaction_changed", cause, events)
		}
	}
	refused := newRig(t, nil)
	refused.events.err = errBoom
	refused.send(t, 1, "alice", "hi")
	if got := refused.mustReact(t, react("bob", 1, "👍")); got.Change != 1 || got.Reactions.Version != 1 {
		t.Fatalf("React with refused events = %+v, want change 1 counted", got)
	}
}

func TestReactAsksThePolicyButIgnoresLockedKinds(t *testing.T) {
	var asked []access.Request
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error { asked = append(asked, r); return access.ErrDenied })
	rg := newRig(t, deny)
	rg.send(t, 1, "alice", "hi")
	if _, err := rg.m.React(t.Context(), react("bob", 1, "👍")); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("React = %v, want PermissionDenied", err)
	}
	if len(asked) != 1 || asked[0].Action != access.ReactMessage || asked[0].User != "bob" || asked[0].Author != "alice" || asked[0].Kind != domain.KindText {
		t.Fatalf("policy asked %+v, want react_message by bob on alice's text", asked)
	}
	locked := newRig(t, access.DefaultPolicy{LockedKinds: []domain.Kind{domain.KindText}})
	locked.send(t, 1, "alice", "hi")
	if got := locked.mustReact(t, react("bob", 1, "👍")); got.Change != 1 {
		t.Fatalf("React on a locked kind = %+v, want it allowed (D94)", got)
	}
}

func TestLimitsFillDefaultsAndCheckBounds(t *testing.T) {
	cases := []struct {
		limits mutate.Limits
		ok     bool
	}{
		{mutate.Limits{}, true},
		{mutate.Limits{MaxEmojis: mutate.MaxEmojisCap, PinLimit: mutate.MaxPinLimit}, true},
		{mutate.Limits{MaxEmojis: 1, PinLimit: 1}, true},
		{mutate.Limits{MaxEmojis: mutate.MaxEmojisCap + 1}, false},
		{mutate.Limits{PinLimit: mutate.MaxPinLimit + 1}, false},
		{mutate.Limits{MaxEmojis: -1}, false},
		{mutate.Limits{PinLimit: -1}, false},
	}
	for _, c := range cases {
		err := c.limits.Validate()
		if (err == nil) != c.ok || (err != nil && !errors.Is(err, apperr.ErrInvalidArgument)) {
			t.Fatalf("%+v.Validate() = %v, want ok=%v", c.limits, err, c.ok)
		}
	}
}
```

**Step 6: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/mutate/..."`
Expected: FAIL biên dịch: `unknown field Reactions in struct literal of type mutate.Deps`, `unknown field Counter in struct literal of type mutate.Deps`, `undefined: mutate.ReactCmd`, `undefined: mutate.ReactResult`, `undefined: mutate.Limits`, `undefined: mutate.CounterToucher`, `undefined: mutate.FastTouchTries`, `rg.m.React undefined (type *mutate.Mutator has no field or method React)`.

**Step 7: Code `mutate`**

`apps/core/internal/mutate/limits.go`:

```go
package mutate

import (
	"cmp"
	"fmt"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	DefaultMaxEmojis = 20
	MaxEmojisCap     = 100
	DefaultPinLimit  = 50
	MaxPinLimit      = 1000
	FastTouchTries   = 3
)

type Limits struct {
	MaxEmojis int
	PinLimit  int
}

func (l Limits) Validate() error { return l.withDefaults().validate() }

func (l Limits) withDefaults() Limits {
	l.MaxEmojis = cmp.Or(l.MaxEmojis, DefaultMaxEmojis)
	l.PinLimit = cmp.Or(l.PinLimit, DefaultPinLimit)
	return l
}

func (l Limits) validate() error {
	if l.MaxEmojis < 1 || l.MaxEmojis > MaxEmojisCap || l.PinLimit < 1 || l.PinLimit > MaxPinLimit {
		return fmt.Errorf("%w: limits %+v need 1 to %d emojis per message and 1 to %d pins per room", apperr.ErrInvalidArgument, l, MaxEmojisCap, MaxPinLimit)
	}
	return nil
}
```

`apps/core/internal/mutate/react.go`:

```go
package mutate

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type CounterToucher interface {
	Touch(ctx context.Context, key store.MsgKey, cur domain.ReactionSummary, witnesses []store.Witness, tries int) (domain.ReactionSummary, bool, error)
}

type ReactCmd struct {
	Tenant, User      string
	Room, Thread, Seq uint64
	Emoji             string
}

type ReactResult struct {
	Change    uint32
	Reactions domain.ReactionSummary
}

func (m *Mutator) React(ctx context.Context, c ReactCmd) (ReactResult, error) {
	key := store.MsgKey{Room: c.Room, Thread: c.Thread, Seq: c.Seq}
	if err := validKey(key); err != nil {
		return ReactResult{}, err
	}
	if c.Emoji != "" {
		if err := domain.ValidateEmoji(c.Emoji); err != nil {
			return ReactResult{}, err
		}
	}
	grant, msg, err := m.target(ctx, access.ReactMessage, c.Tenant, c.User, key)
	if err != nil {
		return ReactResult{}, err
	}
	if err := m.canReact(msg, c.Emoji); err != nil {
		return ReactResult{}, err
	}
	doc, changed, err := m.writeReaction(ctx, c, key)
	if err != nil {
		return ReactResult{}, err
	}
	if !changed {
		return ReactResult{Change: doc.N, Reactions: msg.Reactions}, nil
	}
	_ = m.d.Events.Enqueue(key.Room, []*chatimv1.Event{pbconv.ReactionChanged(grant.Room.Type, doc)})
	summary := m.touch(ctx, grant.Room.Type, msg, store.Witness{User: c.User, N: doc.N})
	return ReactResult{Change: doc.N, Reactions: summary}, nil
}

func (m *Mutator) canReact(msg domain.Message, emoji string) error {
	if emoji == "" {
		return nil
	}
	if msg.Deleted {
		return domain.ErrMessageDeleted
	}
	known := slices.ContainsFunc(msg.Reactions.Counts, func(rc domain.ReactionCount) bool { return rc.Emoji == emoji })
	if !known && len(msg.Reactions.Counts) >= m.d.Limits.MaxEmojis {
		return domain.ErrTooManyEmojis
	}
	return nil
}

func (m *Mutator) writeReaction(ctx context.Context, c ReactCmd, key store.MsgKey) (domain.Reaction, bool, error) {
	if c.Emoji == "" {
		return m.d.Reactions.Remove(ctx, key, c.User, m.now())
	}
	return m.d.Reactions.Set(ctx, domain.Reaction{
		Room: key.Room, Thread: key.Thread, Seq: key.Seq, Tenant: c.Tenant, User: c.User, Emoji: c.Emoji, At: m.now(),
	})
}

func (m *Mutator) touch(ctx context.Context, typ domain.RoomType, msg domain.Message, w store.Witness) domain.ReactionSummary {
	key := store.KeyOf(msg)
	summary, bumped, err := m.d.Counter.Touch(ctx, key, msg.Reactions, []store.Witness{w}, FastTouchTries)
	if err != nil {
		return msg.Reactions
	}
	if bumped {
		msg.Reactions = summary
		_ = m.d.Events.Enqueue(key.Room, []*chatimv1.Event{pbconv.CountsChanged(typ, msg, m.now())})
	}
	return summary
}
```

`apps/core/internal/mutate/mutator.go`:
- Thay dòng `errMissingDeps` bằng:

```go
var errMissingDeps = fmt.Errorf("%w: mutator needs access, messages, edits, hidden, rooms, events, reactions and a counter", apperr.ErrInvalidArgument)
```

- Thay nguyên `type Deps struct {...}` bằng:

```go
type Deps struct {
	Access    *access.Checker
	Messages  Messages
	Edits     store.Edits
	Hidden    store.Hidden
	Rooms     HistoryClearer
	Events    EventPublisher
	Reactions store.Reactions
	Counter   CounterToucher
	Limits    Limits
	Now       func() time.Time
}
```

- Thay nguyên `func New` bằng:

```go
func New(d Deps) (*Mutator, error) {
	if d.Access == nil || d.Messages == nil || d.Edits == nil || d.Hidden == nil || d.Rooms == nil || d.Events == nil ||
		d.Reactions == nil || d.Counter == nil {
		return nil, errMissingDeps
	}
	d.Limits = d.Limits.withDefaults()
	if err := d.Limits.validate(); err != nil {
		return nil, err
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Mutator{d: d}, nil
}
```

**Step 8: Caller của `mutate.New` ngoài package**

`apps/core/internal/grpcsrv/harness_test.go`:
- Trong `type rig struct`, sau `hidden *memstore.Hidden` thêm `reactions *memstore.Reactions` (gofmt căn cột).
- Trong `newRig`, thay `rg := &rig{rooms: memstore.NewRooms(), msgs: memstore.NewMessages(), edits: memstore.NewEdits(), hidden: memstore.NewHidden()}` bằng `rg := memStores()`.

`apps/core/internal/grpcsrv/caller_identity_test.go`: trong `TestNewRequiresEveryDependency` thay dòng `rg := &rig{...}` bằng `rg := memStores()`; bỏ import `".../internal/store/memstore"` (không còn dùng).

`apps/core/internal/grpcsrv/fake_dependencies_test.go`: import thêm `".../internal/counter"` và `".../internal/store/memstore"`; thay nguyên `newMutator` bằng:

```go
func memStores() *rig {
	return &rig{
		rooms: memstore.NewRooms(), msgs: memstore.NewMessages(), edits: memstore.NewEdits(), hidden: memstore.NewHidden(),
		reactions: memstore.NewReactions(),
	}
}

func newMutator(t *testing.T, rg *rig, o options) *mutate.Mutator {
	t.Helper()
	checker, err := access.NewChecker(rg.rooms, o.policy)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	counts, err := counter.New(rg.msgs, rg.reactions)
	if err != nil {
		t.Fatalf("counter.New: %v", err)
	}
	var events mutate.EventPublisher = nopPublisher{}
	if o.events != nil {
		events = o.events
	}
	m, err := mutate.New(mutate.Deps{
		Access: checker, Messages: rg.msgs, Edits: rg.edits, Hidden: rg.hidden, Rooms: rg.rooms, Events: events, Now: o.now,
		Reactions: rg.reactions, Counter: counts,
	})
	if err != nil {
		t.Fatalf("mutate.New: %v", err)
	}
	return m
}
```

`apps/core/service_wiring.go`: import thêm `".../internal/counter"`; thay dòng `mut, err := mutate.New(mutate.Deps{Access: checker, Messages: st, Edits: st, Hidden: st, Rooms: st, Events: pub})` bằng:

```go
	reactions := st.Reactions()
	counts, err := counter.New(st, reactions)
	if err != nil {
		return nil, fmt.Errorf("wire reaction counter: %w", err)
	}
	mut, err := mutate.New(mutate.Deps{
		Access: checker, Messages: st, Edits: st, Hidden: st, Rooms: st, Events: pub,
		Reactions: reactions, Counter: counts,
	})
```

(`Limits` để zero → mặc định 20/50; Task 11 truyền `cfg.Limits`.)

**Step 9: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/internal/mutate/... ./apps/core/internal/grpcsrv/... ./apps/core/internal/access/..."
make vet
```

Expected: PASS; `make vet` sạch (biên dịch cả `apps/core` với `st.Reactions()` của Task 5). `wc -l apps/core/internal/mutate/*.go apps/core/internal/grpcsrv/*_test.go`: mỗi file < 200 (`fixtures_test.go` ~160, `react_rules_test.go` ~185, `react_test.go` ~160, `mutator.go` ~112, `grpcsrv/harness_test.go` ~182).

**Step 10: INDEXES + commit**

`INDEXES.csv`:
- Dòng `apps/core/internal/mutate`: purpose nối "; React (D89, D90): validates the key and the emoji (ValidateEmoji, empty = remove), admits and asks the policy (react_message), refuses a new emoji on a deleted message and a new emoji kind past Limits.MaxEmojis (soft, read from rx), Reactions.Set/Remove (no write and no event when already in the wanted state), enqueues reaction_changed, touches the counts inline (counter, FastTouchTries CAS tries, the caller's write as witness; failures ignored, the worker converges) and enqueues counts_changed when the summary moved; Limits (REACTION_MAX_EMOJIS 1..100, PIN_LIMIT 1..1000, zero = default) checked by New"; key_symbols nối `;CounterToucher;Limits;Limits.Validate;DefaultMaxEmojis;MaxEmojisCap;DefaultPinLimit;MaxPinLimit;FastTouchTries;ReactCmd;ReactResult;Mutator.React`; decisions nối `;D89;D90;D94`.
- Dòng `apps/core`: trong purpose, sau "service_wiring.go builds the mutate.Mutator (own access checker with access.DefaultPolicy{LockedKinds: MESSAGE_LOCKED_KINDS}" chèn " plus the reaction store and a counter.Toucher over messages and reactions"; decisions nối `;D90`.
- Dòng `apps/core/internal/counter`: used_by phải có `apps/core/internal/mutate;apps/core`, thiếu thì thêm.

```bash
make fmt-check && make vet && make lint
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
git add apps/core/internal/mutate/limits.go apps/core/internal/mutate/react.go apps/core/internal/mutate/react_test.go apps/core/internal/mutate/react_rules_test.go
git commit -m "feat(mutate): react with one emoji per user and touch the counts inline" -- apps/core/internal/mutate/ apps/core/internal/grpcsrv/harness_test.go apps/core/internal/grpcsrv/fake_dependencies_test.go apps/core/internal/grpcsrv/caller_identity_test.go apps/core/service_wiring.go INDEXES.csv
```

Expected: `{7}`; `git show --stat HEAD` chỉ có các file trên.

Task rủi ro: một reviewer (thứ tự kiểm của `React`, gỡ vẫn được trên tin xoá, không event/touch khi no-op, touch lỗi không làm hỏng lệnh, `Limits` mặc định; tối đa `-count=3` trên `mutate`).

---

### Task 10: ★ `mutate.Pin`/`Unpin` + Deps (`Pins`, `Projector`)

Ghim là fact + projection (D62, D92). Không còn `base_pv`: server đọc trạng thái hiện tại qua `pinproj.Projector.Current` (`rooms.pins/pv` + fold các fact sau `pv`), rồi ghi fact `pv = Version + 1`; khoá `_id = room│pv` là CAS nên pv dày và `PIN_LIMIT` chính xác.

Thứ tự (`pin`, dùng chung cho `Pin`/`Unpin`):
1. `validKey` → `target(PinMessage | UnpinMessage)` (`Admit` → `Find` → `Allow` với tác giả và loại tin; D94 cho mọi member, kể cả loại bị khoá).
2. `Pin` trên tin `Deleted` → `ErrMessageDeleted`. `Unpin` tin đã xoá vẫn được; xoá tin không đụng ghim.
3. Tối đa `pinTries = 3` lượt (`commitPin`): `Projector.Current` → đã đúng trạng thái (`Pinned` khớp lệnh) → trả state, không fact, không event → `Pin` mà `len(Pins) ≥ Limits.PinLimit` → `ErrTooManyPins` → `Pins.Append(fact pv = Version + 1)` → `ErrPinExists` thì `Pins.At(room, pv)`: cùng `Op/Thread/Seq/By` là chính lệnh này (retry hoặc lệnh song song cùng ý) → coi là kết quả, dùng fact đã lưu (giữ `At` cũ để event trùng id và giống hệt); khác → lượt sau. Hết lượt → `domain.ErrRetryLater` (`Unavailable`).
4. Sau fact: `Projector.Project(room, pv)` (CAS `pv == p`); lỗi → trả `domain.FoldPins(state đọc ở lượt thắng, [fact])` và để worker `pin_projection` (Task 13) ghi projection. Enqueue `pbconv.PinChanged(room.Type, msg, fact)` (lỗi bỏ qua; tin xoá thì payload không có text) → trả state.

`At` của fact lấy `now()` một lần trước vòng lặp, nên mọi lượt của cùng một lệnh mang cùng thời điểm.

**Files:**
- Create: `apps/core/internal/mutate/pin.go`
- Modify: `apps/core/internal/mutate/mutator.go`
- Modify: `apps/core/internal/mutate/fixtures_test.go`
- Modify: `apps/core/internal/mutate/delete_test.go`
- Create: `apps/core/internal/mutate/pin_test.go`
- Create: `apps/core/internal/mutate/pin_race_test.go`
- Modify: `apps/core/internal/grpcsrv/harness_test.go`, `fake_dependencies_test.go`
- Modify: `apps/core/service_wiring.go`
- Modify: `INDEXES.csv`

**Step 1: Rig**

`apps/core/internal/mutate/fixtures_test.go`:
- Import thêm `"github.com/ivannguyendev/chatim/apps/core/internal/pinproj"`.
- Trong `type rig struct`, sau `reactions *memstore.Reactions` thêm `pins      *memstore.Pins`.
- Trong `newRig`, thay `reactions: memstore.NewReactions(), events: &recordingEvents{}, now: created.Add(time.Minute + 1500*time.Microsecond),` bằng:

```go
		reactions: memstore.NewReactions(), pins: memstore.NewPins(), events: &recordingEvents{},
		now: created.Add(time.Minute + 1500*time.Microsecond),
```

- Thay nguyên `func (rg *rig) deps` bằng:

```go
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
	return mutate.Deps{
		Access: checker, Messages: rg.msgs, Edits: rg.edits, Hidden: rg.hidden, Rooms: rg.rooms, Events: rg.events,
		Reactions: rg.reactions, Counter: counts, Pins: rg.pins, Projector: projector, Now: func() time.Time { return rg.now },
	}
}
```

`apps/core/internal/mutate/delete_test.go`, trong map của `TestNewRequiresEveryDependency` thêm sau `"no counter"`:

```go
		"no pins":       func(d *mutate.Deps) { d.Pins = nil },
		"no projector":  func(d *mutate.Deps) { d.Projector = nil },
		"bad pin limit": func(d *mutate.Deps) { d.Limits = mutate.Limits{PinLimit: mutate.MaxPinLimit + 1} },
```

(gofmt căn lại cột của cả map.)

**Step 2: Test**

`apps/core/internal/mutate/pin_test.go`:

```go
package mutate_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func pinCmd(user string, seq uint64) mutate.PinCmd {
	return mutate.PinCmd{Tenant: tenant, User: user, Room: room, Seq: seq}
}

func pinnedSeqs(s domain.PinState) []uint64 {
	out := make([]uint64, len(s.Pins))
	for i, p := range s.Pins {
		out[i] = p.Seq
	}
	return out
}

func samePin(a, b domain.PinAction) bool {
	at := a.At.Equal(b.At)
	a.At, b.At = time.Time{}, time.Time{}
	return at && a == b
}

func (rg *rig) pinFacts(t *testing.T) []domain.PinAction {
	t.Helper()
	got, err := rg.pins.After(t.Context(), room, 0, store.MaxPinScan)
	if err != nil {
		t.Fatalf("pin facts: %v", err)
	}
	return got
}

func (rg *rig) mustPin(t *testing.T, pin bool, c mutate.PinCmd) domain.PinState {
	t.Helper()
	call := rg.m.Pin
	if !pin {
		call = rg.m.Unpin
	}
	got, err := call(t.Context(), c)
	if err != nil {
		t.Fatalf("pin=%v by %s on seq %d: %v", pin, c.User, c.Seq, err)
	}
	return got
}

func TestPinAppendsAFactAndProjectsItBeforeTheAck(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	got := rg.mustPin(t, true, pinCmd("bob", 1))
	want := domain.PinAction{Room: room, PV: 1, Tenant: tenant, Op: domain.PinOpPin, Seq: 1, By: "bob", At: rg.at()}
	facts := rg.pinFacts(t)
	if len(facts) != 1 || !samePin(facts[0], want) {
		t.Fatalf("facts = %+v, want %+v", facts, want)
	}
	if got.Version != 1 || len(got.Pins) != 1 || got.Pins[0].Seq != 1 || got.Pins[0].By != "bob" || got.Pins[0].PV != 1 || !got.Pins[0].At.Equal(rg.at()) {
		t.Fatalf("state = %+v, want seq 1 pinned by bob at version 1", got)
	}
	if stored, err := rg.rooms.PinState(t.Context(), room); err != nil || stored.Version != 1 || !slices.Equal(pinnedSeqs(stored), []uint64{1}) {
		t.Fatalf("projection = %+v, %v; want version 1 written before the ack", stored, err)
	}
	rooms, events := rg.events.list()
	if !slices.Equal(rooms, []uint64{room}) || len(events) != 1 || !proto.Equal(events[0], pbconv.MessagePinned(domain.RoomGroup, rg.stored(t, 1), facts[0])) {
		t.Fatalf("enqueued %v %v, want one msg_pinned", rooms, events)
	}
	if id := events[0].GetId(); id != pbconv.PinEventID(room, 1) {
		t.Fatalf("event id = %q", id)
	}
}

func TestPinAndUnpinInTheWantedStateWriteNothing(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	rg.send(t, 2, "alice", "ho")
	rg.mustPin(t, true, pinCmd("bob", 1))
	rg.now = rg.now.Add(time.Second)
	noops := []struct {
		pin bool
		cmd mutate.PinCmd
	}{{true, pinCmd("carol", 1)}, {false, pinCmd("bob", 2)}}
	for _, n := range noops {
		if got := rg.mustPin(t, n.pin, n.cmd); got.Version != 1 || !slices.Equal(pinnedSeqs(got), []uint64{1}) {
			t.Fatalf("no-op pin=%v on seq %d = %+v, want version 1 with seq 1", n.pin, n.cmd.Seq, got)
		}
	}
	if facts := rg.pinFacts(t); len(facts) != 1 {
		t.Fatalf("no-ops wrote facts %+v", facts)
	}
	if got := rg.mustPin(t, false, pinCmd("carol", 1)); got.Version != 2 || len(got.Pins) != 0 {
		t.Fatalf("unpin = %+v, want version 2 without pins", got)
	}
	if again := rg.mustPin(t, false, pinCmd("carol", 1)); again.Version != 2 || len(again.Pins) != 0 {
		t.Fatalf("second unpin = %+v, want a no-op at version 2", again)
	}
	_, events := rg.events.list()
	if len(events) != 2 || events[1].GetMessageUnpinned() == nil || events[1].GetId() != pbconv.PinEventID(room, 2) {
		t.Fatalf("events = %v, want msg_pinned then msg_unpinned v2 only", events)
	}
	if facts := rg.pinFacts(t); len(facts) != 2 {
		t.Fatalf("facts = %+v, want the pin and one unpin", facts)
	}
}

func TestThePinLimitIsExact(t *testing.T) {
	rg := newRig(t, nil)
	d := rg.deps(t, nil)
	d.Limits = mutate.Limits{PinLimit: 2}
	rg.m = rg.build(t, d)
	for _, seq := range []uint64{1, 2, 3} {
		rg.send(t, seq, "alice", "m")
	}
	rg.mustPin(t, true, pinCmd("bob", 1))
	rg.mustPin(t, true, pinCmd("bob", 2))
	if _, err := rg.m.Pin(t.Context(), pinCmd("bob", 3)); !errors.Is(err, domain.ErrTooManyPins) || !errors.Is(err, apperr.ErrFailedPrecondition) {
		t.Fatalf("third pin = %v, want ErrTooManyPins", err)
	}
	if again := rg.mustPin(t, true, pinCmd("carol", 1)); again.Version != 2 {
		t.Fatalf("re-pin at the limit = %+v, want a no-op at version 2", again)
	}
	rg.mustPin(t, false, pinCmd("bob", 2))
	got := rg.mustPin(t, true, pinCmd("bob", 3))
	if got.Version != 4 || !slices.Equal(pinnedSeqs(got), []uint64{3, 1}) {
		t.Fatalf("state = %+v, want version 4 with seq 3 then seq 1", got)
	}
}

func TestPinningADeletedMessageFailsButUnpinningWorks(t *testing.T) {
	rg := newRig(t, nil)
	rg.send(t, 1, "alice", "hi")
	rg.send(t, 2, "alice", "ho")
	rg.mustPin(t, true, pinCmd("bob", 1))
	for _, seq := range []uint64{1, 2} {
		if _, err := rg.m.Delete(t.Context(), del("alice", seq, 0)); err != nil {
			t.Fatalf("Delete seq %d: %v", seq, err)
		}
	}
	if _, err := rg.m.Pin(t.Context(), pinCmd("bob", 2)); !errors.Is(err, domain.ErrMessageDeleted) {
		t.Fatalf("pin a deleted message = %v, want ErrMessageDeleted", err)
	}
	if stored, err := rg.rooms.PinState(t.Context(), room); err != nil || !slices.Equal(pinnedSeqs(stored), []uint64{1}) {
		t.Fatalf("pins after delete = %+v, %v; want seq 1 kept", stored, err)
	}
	if got := rg.mustPin(t, false, pinCmd("bob", 1)); got.Version != 2 || len(got.Pins) != 0 {
		t.Fatalf("unpin a deleted message = %+v, want version 2 without pins", got)
	}
	_, events := rg.events.list()
	last := events[len(events)-1].GetMessageUnpinned()
	if last == nil || !last.GetMessage().GetDeleted() || last.GetMessage().GetText() != "" {
		t.Fatalf("last event = %v, want msg_unpinned of the deleted message without text", events[len(events)-1])
	}
}
```

`apps/core/internal/mutate/pin_race_test.go`:

```go
package mutate_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type scriptedProjector struct {
	mutate.PinProjector
	stale   int
	current int
	project error
}

func (p *scriptedProjector) Current(ctx context.Context, r uint64) (domain.PinState, error) {
	p.current++
	if p.current <= p.stale {
		return domain.PinState{}, nil
	}
	return p.PinProjector.Current(ctx, r)
}

func (p *scriptedProjector) Project(ctx context.Context, r, target uint64) (domain.PinState, error) {
	if p.project != nil {
		return domain.PinState{}, p.project
	}
	return p.PinProjector.Project(ctx, r, target)
}

func (rg *rig) withProjector(t *testing.T, p *scriptedProjector) *scriptedProjector {
	t.Helper()
	d := rg.deps(t, nil)
	p.PinProjector = d.Projector
	d.Projector = p
	rg.m = rg.build(t, d)
	return p
}

func (rg *rig) appendPin(t *testing.T, a domain.PinAction) domain.PinAction {
	t.Helper()
	if err := rg.pins.Append(t.Context(), a); err != nil {
		t.Fatalf("append pin v%d: %v", a.PV, err)
	}
	return a
}

func TestADuplicatePinVersionFromTheSameCommandIsItsResult(t *testing.T) {
	rg := newRig(t, nil)
	p := rg.withProjector(t, &scriptedProjector{stale: 1})
	rg.send(t, 1, "alice", "hi")
	earlier := rg.appendPin(t, domain.PinAction{Room: room, PV: 1, Tenant: tenant, Op: domain.PinOpPin, Seq: 1, By: "bob", At: created})
	got, err := rg.m.Pin(t.Context(), pinCmd("bob", 1))
	if err != nil || got.Version != 1 || !slices.Equal(pinnedSeqs(got), []uint64{1}) {
		t.Fatalf("Pin = %+v, %v; want the stored fact as the result", got, err)
	}
	if facts := rg.pinFacts(t); len(facts) != 1 || p.current != 1 {
		t.Fatalf("facts %+v after %d reads, want the one stored fact after one read", facts, p.current)
	}
	_, events := rg.events.list()
	if len(events) != 1 || !proto.Equal(events[0], pbconv.MessagePinned(domain.RoomGroup, rg.stored(t, 1), earlier)) {
		t.Fatalf("events = %v, want msg_pinned built from the stored fact", events)
	}
}

func TestAnotherFactAtThePinVersionRetriesThenGivesUp(t *testing.T) {
	rg := newRig(t, nil)
	p := rg.withProjector(t, &scriptedProjector{stale: 3})
	rg.send(t, 1, "alice", "hi")
	rg.send(t, 2, "alice", "ho")
	rg.appendPin(t, domain.PinAction{Room: room, PV: 1, Tenant: tenant, Op: domain.PinOpPin, Seq: 2, By: "carol", At: created})
	if _, err := rg.m.Pin(t.Context(), pinCmd("bob", 1)); !errors.Is(err, domain.ErrRetryLater) || !errors.Is(err, apperr.ErrUnavailable) {
		t.Fatalf("Pin = %v, want ErrRetryLater after every try lost", err)
	}
	if facts := rg.pinFacts(t); len(facts) != 1 || p.current != 3 {
		t.Fatalf("facts %+v after %d reads, want only carol's fact after 3 reads", facts, p.current)
	}
	if _, events := rg.events.list(); len(events) != 0 {
		t.Fatalf("a lost pin enqueued %v", events)
	}
	p.stale = 4
	got, err := rg.m.Pin(t.Context(), pinCmd("bob", 1))
	if err != nil || got.Version != 2 || !slices.Equal(pinnedSeqs(got), []uint64{1, 2}) {
		t.Fatalf("Pin after one lost try = %+v, %v; want version 2 with seq 1 first", got, err)
	}
}

func TestAFailedProjectionFallsBackToTheFold(t *testing.T) {
	rg := newRig(t, nil)
	rg.withProjector(t, &scriptedProjector{project: errBoom})
	rg.events.err = errBoom
	rg.send(t, 1, "alice", "hi")
	got, err := rg.m.Pin(t.Context(), pinCmd("bob", 1))
	if err != nil || got.Version != 1 || !slices.Equal(pinnedSeqs(got), []uint64{1}) {
		t.Fatalf("Pin = %+v, %v; want the folded state", got, err)
	}
	if stored, err := rg.rooms.PinState(t.Context(), room); err != nil || stored.Version != 0 {
		t.Fatalf("projection = %+v, %v; want it left to the worker", stored, err)
	}
}

func TestPinAsksThePolicyAndChecksTheTarget(t *testing.T) {
	var asked []access.Request
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error { asked = append(asked, r); return access.ErrDenied })
	rg := newRig(t, deny)
	rg.send(t, 1, "alice", "hi")
	if _, err := rg.m.Pin(t.Context(), pinCmd("bob", 1)); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("Pin = %v, want PermissionDenied", err)
	}
	if _, err := rg.m.Unpin(t.Context(), pinCmd("bob", 1)); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("Unpin = %v, want PermissionDenied", err)
	}
	if len(asked) != 2 || asked[0].Action != access.PinMessage || asked[1].Action != access.UnpinMessage || asked[0].Author != "alice" || asked[0].Kind != domain.KindText {
		t.Fatalf("policy asked %+v, want pin_message then unpin_message on alice's text", asked)
	}
	open := newRig(t, access.DefaultPolicy{LockedKinds: []domain.Kind{domain.KindText}})
	open.send(t, 1, "alice", "hi")
	threaded, elsewhere := pinCmd("bob", 1), pinCmd("bob", 1)
	threaded.Thread, elsewhere.Room = 1, 999
	cases := []struct {
		name string
		cmd  mutate.PinCmd
		want error
	}{
		{"stranger", pinCmd("mallory", 1), domain.ErrNotMember},
		{"unknown message", pinCmd("bob", 9), domain.ErrMessageNotFound},
		{"thread", threaded, apperr.ErrInvalidArgument},
		{"unknown room", elsewhere, domain.ErrRoomNotFound},
	}
	for _, c := range cases {
		if _, err := open.m.Pin(t.Context(), c.cmd); !errors.Is(err, c.want) {
			t.Fatalf("%s: Pin = %v, want %v", c.name, err, c.want)
		}
	}
	if facts := open.pinFacts(t); len(facts) != 0 {
		t.Fatalf("refused pins wrote facts %+v", facts)
	}
	if got, err := open.m.Pin(t.Context(), pinCmd("bob", 1)); err != nil || got.Version != 1 {
		t.Fatalf("Pin on a locked kind = %+v, %v; want it allowed (D94)", got, err)
	}
}
```

**Step 3: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/mutate/..."`
Expected: FAIL biên dịch: `unknown field Pins in struct literal of type mutate.Deps`, `unknown field Projector in struct literal of type mutate.Deps`, `undefined: mutate.PinCmd`, `undefined: mutate.PinProjector`, `rg.m.Pin undefined (type *mutate.Mutator has no field or method Pin)`, `rg.m.Unpin undefined (type *mutate.Mutator has no field or method Unpin)`.

**Step 4: Code**

`apps/core/internal/mutate/pin.go`:

```go
package mutate

import (
	"context"
	"errors"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const pinTries = 3

type PinProjector interface {
	Current(ctx context.Context, room uint64) (domain.PinState, error)
	Project(ctx context.Context, room, target uint64) (domain.PinState, error)
}

type PinCmd struct {
	Tenant, User      string
	Room, Thread, Seq uint64
}

func (m *Mutator) Pin(ctx context.Context, c PinCmd) (domain.PinState, error) {
	return m.pin(ctx, c, domain.PinOpPin, access.PinMessage)
}

func (m *Mutator) Unpin(ctx context.Context, c PinCmd) (domain.PinState, error) {
	return m.pin(ctx, c, domain.PinOpUnpin, access.UnpinMessage)
}

func (m *Mutator) pin(ctx context.Context, c PinCmd, op domain.PinOp, action access.Action) (domain.PinState, error) {
	key := store.MsgKey{Room: c.Room, Thread: c.Thread, Seq: c.Seq}
	if err := validKey(key); err != nil {
		return domain.PinState{}, err
	}
	grant, msg, err := m.target(ctx, action, c.Tenant, c.User, key)
	if err != nil {
		return domain.PinState{}, err
	}
	if op == domain.PinOpPin && msg.Deleted {
		return domain.PinState{}, domain.ErrMessageDeleted
	}
	want := domain.PinAction{Room: key.Room, Tenant: c.Tenant, Op: op, Thread: key.Thread, Seq: key.Seq, By: c.User, At: m.now()}
	state, fact, wrote, err := m.commitPin(ctx, want)
	if err != nil || !wrote {
		return state, err
	}
	return m.finishPin(ctx, grant.Room.Type, msg, state, fact), nil
}

func (m *Mutator) commitPin(ctx context.Context, want domain.PinAction) (domain.PinState, domain.PinAction, bool, error) {
	for range pinTries {
		state, err := m.d.Projector.Current(ctx, want.Room)
		if err != nil {
			return domain.PinState{}, domain.PinAction{}, false, err
		}
		if state.Pinned(want.Thread, want.Seq) == (want.Op == domain.PinOpPin) {
			return state, domain.PinAction{}, false, nil
		}
		if want.Op == domain.PinOpPin && len(state.Pins) >= m.d.Limits.PinLimit {
			return domain.PinState{}, domain.PinAction{}, false, domain.ErrTooManyPins
		}
		fact := want
		fact.PV = state.Version + 1
		got, err := m.appendPin(ctx, fact)
		switch {
		case err != nil:
			return domain.PinState{}, domain.PinAction{}, false, err
		case got.PV != 0:
			return state, got, true, nil
		}
	}
	return domain.PinState{}, domain.PinAction{}, false, domain.ErrRetryLater
}

func (m *Mutator) appendPin(ctx context.Context, fact domain.PinAction) (domain.PinAction, error) {
	err := m.d.Pins.Append(ctx, fact)
	if !errors.Is(err, store.ErrPinExists) {
		return fact, err
	}
	got, err := m.d.Pins.At(ctx, fact.Room, fact.PV)
	if err != nil {
		return domain.PinAction{}, err
	}
	if got.Op != fact.Op || got.Thread != fact.Thread || got.Seq != fact.Seq || got.By != fact.By {
		return domain.PinAction{}, nil
	}
	return got, nil
}

func (m *Mutator) finishPin(ctx context.Context, typ domain.RoomType, msg domain.Message, before domain.PinState, fact domain.PinAction) domain.PinState {
	state, err := m.d.Projector.Project(ctx, fact.Room, fact.PV)
	if err != nil {
		state = domain.FoldPins(before, []domain.PinAction{fact})
	}
	_ = m.d.Events.Enqueue(fact.Room, []*chatimv1.Event{pbconv.PinChanged(typ, msg, fact)})
	return state
}
```

`appendPin` trả fact rỗng (`PV == 0`, `err == nil`) khi pv đã thuộc lệnh khác: lượt sau đọc lại state. Trả `fact, err` khi `Append` lỗi khác hoặc thành công.

`apps/core/internal/mutate/mutator.go`:
- `errMissingDeps` đổi thông điệp thành `"%w: mutator needs access, messages, edits, hidden, rooms, events, reactions, a counter, pins and a pin projector"`.
- Trong `type Deps struct`, sau `Counter   CounterToucher` thêm:

```go
	Pins      store.Pins
	Projector PinProjector
```

- Trong `New`, điều kiện thành:

```go
	if d.Access == nil || d.Messages == nil || d.Edits == nil || d.Hidden == nil || d.Rooms == nil || d.Events == nil ||
		d.Reactions == nil || d.Counter == nil || d.Pins == nil || d.Projector == nil {
```

**Step 5: Caller ngoài package**

`apps/core/internal/grpcsrv/harness_test.go`: trong `type rig struct`, sau `reactions *memstore.Reactions` thêm `pins      *memstore.Pins`.

`apps/core/internal/grpcsrv/fake_dependencies_test.go`: import thêm `".../internal/pinproj"`; trong `memStores` đổi dòng `reactions: memstore.NewReactions(),` thành `reactions: memstore.NewReactions(), pins: memstore.NewPins(),`; trong `newMutator`, sau khối `counter.New` thêm:

```go
	projector, err := pinproj.New(rg.pins, rg.rooms)
	if err != nil {
		t.Fatalf("pinproj.New: %v", err)
	}
```

và đổi dòng `Reactions: rg.reactions, Counter: counts,` thành `Reactions: rg.reactions, Counter: counts, Pins: rg.pins, Projector: projector,`.

`apps/core/service_wiring.go`: import thêm `".../internal/pinproj"`; thay đoạn từ `reactions := st.Reactions()` tới hết lời gọi `mutate.New(...)` bằng:

```go
	reactions, pins := st.Reactions(), st.Pins()
	counts, err := counter.New(st, reactions)
	if err != nil {
		return nil, fmt.Errorf("wire reaction counter: %w", err)
	}
	projector, err := pinproj.New(pins, st)
	if err != nil {
		return nil, fmt.Errorf("wire pin projector: %w", err)
	}
	mut, err := mutate.New(mutate.Deps{
		Access: checker, Messages: st, Edits: st, Hidden: st, Rooms: st, Events: pub,
		Reactions: reactions, Counter: counts, Pins: pins, Projector: projector,
	})
```

**Step 6: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/internal/mutate/... ./apps/core/internal/grpcsrv/..."
make vet
```

Expected: PASS; `make vet` sạch. `wc -l apps/core/internal/mutate/*.go apps/core/internal/grpcsrv/harness_test.go`: mỗi file < 200 (`pin.go` ~110, `pin_test.go` ~165, `pin_race_test.go` ~170, `fixtures_test.go` ~168, `mutator.go` ~115, `harness_test.go` ~183).

**Step 7: INDEXES + commit**

`INDEXES.csv`:
- Dòng `apps/core/internal/mutate`: purpose nối "; Pin/Unpin (D92): admit and ask the policy (pin_message/unpin_message), refuse to pin a deleted message (unpin still works, delete leaves pins alone), up to 3 tries of: pinproj Current, already in the wanted state = success without fact or event, PinLimit exact, append the fact at pv = current + 1 (key is the CAS); a duplicate pv with the same op/thread/seq/by is this command's result, another fact retries; out of tries = ErrRetryLater; then Project (CAS pv, falls back to FoldPins and leaves the projection to the worker) and enqueue msg_pinned/msg_unpinned best effort"; key_symbols nối `;PinProjector;PinCmd;Mutator.Pin;Mutator.Unpin`; decisions nối `;D92`.
- Dòng `apps/core`: chỗ đã chèn ở Task 9 đổi thành " plus the reaction store, a counter.Toucher over messages and reactions, the pin facts and a pinproj.Projector".
- Dòng `apps/core/internal/pinproj`: used_by phải có `apps/core/internal/mutate;apps/core`, thiếu thì thêm.

```bash
make fmt-check && make vet && make lint
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
git add apps/core/internal/mutate/pin.go apps/core/internal/mutate/pin_test.go apps/core/internal/mutate/pin_race_test.go
git commit -m "feat(mutate): pin and unpin messages with dense pin versions" -- apps/core/internal/mutate/ apps/core/internal/grpcsrv/harness_test.go apps/core/internal/grpcsrv/fake_dependencies_test.go apps/core/service_wiring.go INDEXES.csv
```

Expected: `{7}`.

Task rủi ro: một reviewer (pv dày + nhận ra trùng khoá, giới hạn chính xác, no-op không ghi, hết lượt → `Unavailable`, fallback khi `Project` lỗi, tin xoá; tối đa `-count=3` trên `mutate`).

---

### Task 11: ★ Config (3 env) + `grpcsrv/react_pin.go` + `wireService(…, limits, …)` + README env

Ba biến môi trường mới (hợp đồng, mục config):

| Env | Field | Mặc định | Kiểm |
|---|---|---|---|
| `REACTION_MAX_EMOJIS` | `Config.Limits.MaxEmojis` | 20 | `p.count` (> 0) + `mutate.Limits.Validate` (≤ 100) |
| `PIN_LIMIT` | `Config.Limits.PinLimit` | 50 | `p.count` (> 0) + `mutate.Limits.Validate` (≤ 1000) |
| `REACTION_COUNT_DELAY` | `Config.ReactionCountDelay` | `effects.DefaultCountDelay` (1s) | `p.span` (> 0) + luật `REACTION_COUNT_DELAY must be positive and at most RECONCILE_DELAY` |

Luật delay giữ delay tăng dần trong registry `ReactionChanged → [room_activity (0), reaction_counter (REACTION_COUNT_DELAY), reaction_event (RECONCILE_DELAY)]` (worker chờ `max(CommittedAt) + delay` của từng effect theo thứ tự) và giữ record trong `AckWait` của consumer (`RECONCILE_DELAY + 30s`).

`effects.DefaultCountDelay` thuộc khối hằng của Task 13, nhưng config cần nó ngay: task này tạo `effects/reaction_counter.go` chỉ với hằng đó; Task 13 thay cả file (cùng tên, cùng giá trị, API không đổi).

Ba RPC mỏng trong `grpcsrv/react_pin.go` (`grpcsrv.Deps` không thêm field): `callerAndRoom` → `Mutator.React/Pin/Unpin` → `pbconv.ReactionSummary` / `pbconv.Pins`. Mã lỗi đi qua `pkg/grpcserver` như cũ: `ErrTooManyEmojis`, `ErrTooManyPins`, `ErrMessageDeleted` → `FailedPrecondition`; `ErrReactionContended`, `ErrRetryLater` → `Unavailable`; `ErrNotMember`, `access.ErrDenied` → `PermissionDenied`.

Cuối task `wireService` nhận `limits mutate.Limits` (hợp đồng), `wiring.go` truyền `cfg.Limits`.

**Files:**
- Modify: `apps/core/internal/config/config.go`, `effects_components.go`, `validate.go`
- Modify: `apps/core/internal/config/env_test.go`, `load_test.go`, `validate_test.go`, `parse_test.go`
- Create: `apps/core/internal/effects/reaction_counter.go` (chỉ hằng `DefaultCountDelay`)
- Create: `apps/core/internal/grpcsrv/react_pin.go`
- Create: `apps/core/internal/grpcsrv/react_pin_test.go`
- Modify: `apps/core/internal/grpcsrv/caller_identity_test.go`
- Modify: `apps/core/service_wiring.go`, `apps/core/wiring.go`
- Modify: `README.md` (bảng env)
- Modify: `INDEXES.csv`

**Step 1: Test config**

`apps/core/internal/config/env_test.go`:
- `envKeys`: sau `"MESSAGE_LOCKED_KINDS",` thêm dòng `"REACTION_MAX_EMOJIS", "PIN_LIMIT", "REACTION_COUNT_DELAY",`.
- `overrides`: sau `"MESSAGE_LOCKED_KINDS": "text",` thêm dòng `"REACTION_MAX_EMOJIS": "30", "PIN_LIMIT": "10", "REACTION_COUNT_DELAY": "2s",`.

`apps/core/internal/config/load_test.go`: import thêm `".../internal/mutate"`;
- trong `TestLoadDefaults`, sau `AckMarks: ...` thêm:

```go
		Limits:             mutate.Limits{MaxEmojis: 20, PinLimit: 50},
		ReactionCountDelay: time.Second,
```

- trong `TestLoadOverrides`, sau `LockedMessageKinds: []domain.Kind{domain.KindText},` thêm:

```go
		Limits:             mutate.Limits{MaxEmojis: 30, PinLimit: 10},
		ReactionCountDelay: 2 * time.Second,
```

(gofmt căn lại cột của hai literal.)

`apps/core/internal/config/validate_test.go`, trong bảng `TestLoadValidation`, trước dòng `{"stop phases overflow", ...` thêm:

```go
		{"reaction count delay above the reconcile delay", map[string]string{"REACTION_COUNT_DELAY": "5001ms"}, "REACTION_COUNT_DELAY must be positive and at most RECONCILE_DELAY"},
		{"reaction count delay at the reconcile delay", map[string]string{"REACTION_COUNT_DELAY": "5s"}, ""},
		{"emoji limit above the cap", map[string]string{"REACTION_MAX_EMOJIS": "101"}, "REACTION_MAX_EMOJIS, PIN_LIMIT"},
		{"emoji limit at the cap", map[string]string{"REACTION_MAX_EMOJIS": "100"}, ""},
		{"pin limit above the cap", map[string]string{"PIN_LIMIT": "1001"}, "REACTION_MAX_EMOJIS, PIN_LIMIT"},
		{"pin limit at the cap", map[string]string{"PIN_LIMIT": "1000"}, ""},
```

`apps/core/internal/config/parse_test.go`, trong bảng `TestLoadRejectsNonPositiveValues`, sau `{"SLOT_HOOK_TIMEOUT", "0s"},` thêm:

```go
		{"REACTION_MAX_EMOJIS", "0"},
		{"PIN_LIMIT", "0"},
		{"REACTION_COUNT_DELAY", "0s"},
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/config/..."`
Expected: FAIL biên dịch: `unknown field Limits in struct literal of type config.Config`, `unknown field ReactionCountDelay in struct literal of type config.Config`.

**Step 3: Code config**

`apps/core/internal/effects/reaction_counter.go`:

```go
package effects

import "time"

const DefaultCountDelay = time.Second
```

`apps/core/internal/config/config.go`: import thêm `"github.com/ivannguyendev/chatim/apps/core/internal/mutate"`;
- trong `type Config struct`, sau `LockedMessageKinds  []domain.Kind` thêm:

```go
	Limits              mutate.Limits
	ReactionCountDelay  time.Duration
```

- trong literal của `Load`, sau `LockedMessageKinds:  p.kinds("MESSAGE_LOCKED_KINDS"),` thêm:

```go
		Limits: mutate.Limits{
			MaxEmojis: p.count("REACTION_MAX_EMOJIS", mutate.DefaultMaxEmojis),
			PinLimit:  p.count("PIN_LIMIT", mutate.DefaultPinLimit),
		},
```

`apps/core/internal/config/effects_components.go`, cuối `workerConfig` (sau literal `c.Effects`) thêm:

```go
	c.ReactionCountDelay = p.span("REACTION_COUNT_DELAY", effects.DefaultCountDelay)
```

`apps/core/internal/config/validate.go`:
- trong `rules`, sau dòng `WORK_DUPLICATES ...` thêm:

```go
		{c.ReactionCountDelay > 0 && c.ReactionCountDelay <= c.EffectDelay, "REACTION_COUNT_DELAY must be positive and at most RECONCILE_DELAY"},
```

- trong `parts` của `componentErrors`, sau `{"WORK_*, SLOT_TICK", c.Effects.Validate()},` thêm:

```go
		{"REACTION_MAX_EMOJIS, PIN_LIMIT", c.Limits.Validate()},
```

Chạy `make -s go ARGS="fmt ./apps/core/internal/config/"` để gofmt căn cột.

**Step 4: Chạy, thấy pass + commit**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/config/... ./apps/core/internal/effects/..."`
Expected: PASS (`TestOverridesCoverEveryKey` thấy 3 khoá mới ở cả hai bảng). `wc -l apps/core/internal/config/*.go`: mỗi file < 200 (`load_test.go` ~181, `config.go` ~158).

`INDEXES.csv`, dòng `apps/core/internal/config`: purpose nối "; REACTION_MAX_EMOJIS/PIN_LIMIT -> Limits (mutate.Limits, defaults 20/50, checked by Limits.Validate: 1..100 and 1..1000); REACTION_COUNT_DELAY -> ReactionCountDelay (default effects.DefaultCountDelay 1s, positive and at most RECONCILE_DELAY so effect delays grow along the registry)"; key_symbols nối `;Config.Limits;Config.ReactionCountDelay`; used_by giữ; decisions nối `;D89;D90;D92`. Dòng `apps/core/internal/effects`: key_symbols nối `;DefaultCountDelay`.

```bash
make fmt-check && make vet && make lint
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
git add apps/core/internal/effects/reaction_counter.go
git commit -m "feat(config): reaction emoji limit, pin limit and counter delay" -- apps/core/internal/config/ apps/core/internal/effects/reaction_counter.go INDEXES.csv
```

Expected: `{7}`.

**Step 5: Test `grpcsrv`**

`apps/core/internal/grpcsrv/caller_identity_test.go`, trong map `rpcs` của `TestEveryRPCChecksCallerIdentityFirst`, sau `"GetEditHistory"` thêm:

```go
		"ReactMessage": func(ctx context.Context) error {
			_, err := rg.client.ReactMessage(ctx, &chatimv1.ReactMessageRequest{RoomId: "42", Seq: 1, Emoji: "👍"})
			return err
		},
		"PinMessage": func(ctx context.Context) error {
			_, err := rg.client.PinMessage(ctx, &chatimv1.PinMessageRequest{RoomId: "42", Seq: 1})
			return err
		},
		"UnpinMessage": func(ctx context.Context) error {
			_, err := rg.client.UnpinMessage(ctx, &chatimv1.UnpinMessageRequest{RoomId: "42", Seq: 1})
			return err
		},
```

`apps/core/internal/grpcsrv/react_pin_test.go`:

```go
package grpcsrv_test

import (
	"context"
	"slices"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestReactAndPinThroughTheService(t *testing.T) {
	events := &recordingEvents{}
	rg := newRig(t, options{events: events})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob := as(t, "acme", "alice"), as(t, "acme", "bob")
	rg.send(t, alice, room, "c-1", "hi")
	first, err := rg.client.ReactMessage(bob, &chatimv1.ReactMessageRequest{RoomId: room, Seq: 1, Emoji: "👍"})
	if err != nil {
		t.Fatalf("bob ReactMessage: %v", err)
	}
	second, err := rg.client.ReactMessage(alice, &chatimv1.ReactMessageRequest{RoomId: room, Seq: 1, Emoji: "👍"})
	if err != nil {
		t.Fatalf("alice ReactMessage: %v", err)
	}
	want := pbconv.ReactionSummary(domain.ReactionSummary{Counts: []domain.ReactionCount{{Emoji: "👍", Count: 2}}, Version: 2})
	if first.GetChange() != 1 || second.GetChange() != 1 || !proto.Equal(second.GetReactions(), want) {
		t.Fatalf("reactions = %v then %v, want change 1 each and %v", first, second, want)
	}
	pinned, err := rg.client.PinMessage(bob, &chatimv1.PinMessageRequest{RoomId: room, Seq: 1})
	if err != nil || pinned.GetPinVersion() != 1 || len(pinned.GetPins()) != 1 {
		t.Fatalf("PinMessage = %v, %v; want one pin at version 1", pinned, err)
	}
	if p := pinned.GetPins()[0]; p.GetSeq() != 1 || p.GetBy() != "bob" || p.GetPinVersion() != 1 || p.GetPinnedAt() == nil {
		t.Fatalf("pin = %v, want seq 1 pinned by bob at version 1", p)
	}
	unpinned, err := rg.client.UnpinMessage(alice, &chatimv1.UnpinMessageRequest{RoomId: room, Seq: 1})
	if err != nil || unpinned.GetPinVersion() != 2 || len(unpinned.GetPins()) != 0 {
		t.Fatalf("UnpinMessage = %v, %v; want no pins at version 2", unpinned, err)
	}
	id := roomNumber(t, room)
	_, got := events.enqueued()
	var ids []string
	for _, ev := range got {
		ids = append(ids, ev.GetId())
	}
	wantIDs := []string{
		pbconv.RoomCreatedEventID(id),
		pbconv.ReactionEventID(id, 0, 1, "bob", 1), pbconv.ReactionCountsEventID(id, 0, 1, 1),
		pbconv.ReactionEventID(id, 0, 1, "alice", 1), pbconv.ReactionCountsEventID(id, 0, 1, 2),
		pbconv.PinEventID(id, 1), pbconv.PinEventID(id, 2),
	}
	if !slices.Equal(ids, wantIDs) {
		t.Fatalf("enqueued %v, want %v", ids, wantIDs)
	}
}

func TestReactAndPinErrorsKeepTheirCodes(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob")
	alice, bob, mallory := as(t, "acme", "alice"), as(t, "acme", "bob"), as(t, "acme", "mallory")
	rg.send(t, alice, room, "c-1", "hi")
	rg.send(t, alice, room, "c-2", "gone")
	if _, err := rg.client.DeleteMessage(alice, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 2}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	react := func(ctx context.Context, req *chatimv1.ReactMessageRequest) func() error {
		return func() error { _, err := rg.client.ReactMessage(ctx, req); return err }
	}
	pin := func(ctx context.Context, req *chatimv1.PinMessageRequest) func() error {
		return func() error { _, err := rg.client.PinMessage(ctx, req); return err }
	}
	unpin := func(ctx context.Context, req *chatimv1.UnpinMessageRequest) func() error {
		return func() error { _, err := rg.client.UnpinMessage(ctx, req); return err }
	}
	cases := []struct {
		name string
		call func() error
		code codes.Code
	}{
		{"control character emoji", react(bob, &chatimv1.ReactMessageRequest{RoomId: room, Seq: 1, Emoji: "\u0007"}), codes.InvalidArgument},
		{"unknown message", react(bob, &chatimv1.ReactMessageRequest{RoomId: room, Seq: 9, Emoji: "👍"}), codes.NotFound},
		{"stranger", react(mallory, &chatimv1.ReactMessageRequest{RoomId: room, Seq: 1, Emoji: "👍"}), codes.PermissionDenied},
		{"other tenant", react(as(t, "other", "bob"), &chatimv1.ReactMessageRequest{RoomId: room, Seq: 1, Emoji: "👍"}), codes.NotFound},
		{"bad room id", react(bob, &chatimv1.ReactMessageRequest{RoomId: "x", Seq: 1, Emoji: "👍"}), codes.InvalidArgument},
		{"thread", react(bob, &chatimv1.ReactMessageRequest{RoomId: room, ThreadRoot: 1, Seq: 1, Emoji: "👍"}), codes.InvalidArgument},
		{"react on a deleted message", react(bob, &chatimv1.ReactMessageRequest{RoomId: room, Seq: 2, Emoji: "👍"}), codes.FailedPrecondition},
		{"pin a deleted message", pin(bob, &chatimv1.PinMessageRequest{RoomId: room, Seq: 2}), codes.FailedPrecondition},
		{"pin an unknown message", pin(bob, &chatimv1.PinMessageRequest{RoomId: room, Seq: 9}), codes.NotFound},
		{"unpin by a stranger", unpin(mallory, &chatimv1.UnpinMessageRequest{RoomId: room, Seq: 1}), codes.PermissionDenied},
		{"unpin without a seq", unpin(bob, &chatimv1.UnpinMessageRequest{RoomId: room}), codes.InvalidArgument},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { expectCode(t, c.call(), c.code) })
	}
	if _, err := rg.client.ReactMessage(bob, &chatimv1.ReactMessageRequest{RoomId: room, Seq: 2}); err != nil {
		t.Fatalf("removing nothing from a deleted message = %v, want success", err)
	}
}
```

**Step 6: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/grpcsrv/..."`
Expected: FAIL: `TestReactAndPinThroughTheService`: `bob ReactMessage: rpc error: code = Internal desc = internal error` (handler `Unimplemented` của `UnimplementedCoreServiceServer`, `pkg/grpcserver` đổi thành `Internal`); các case của `TestEveryRPCChecksCallerIdentityFirst/ReactMessage|PinMessage|UnpinMessage/*` báo `status = (Internal, "internal error"), want (Unauthenticated, …)`.

**Step 7: Code `grpcsrv/react_pin.go`**

```go
package grpcsrv

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (s *Service) ReactMessage(ctx context.Context, req *chatimv1.ReactMessageRequest) (*chatimv1.ReactMessageResponse, error) {
	who, room, err := callerAndRoom(ctx, req.GetRoomId())
	if err != nil {
		return nil, err
	}
	res, err := s.mutator.React(ctx, mutate.ReactCmd{
		Tenant: who.tenant, User: who.user, Room: room, Thread: req.GetThreadRoot(), Seq: req.GetSeq(), Emoji: req.GetEmoji(),
	})
	if err != nil {
		return nil, err
	}
	return &chatimv1.ReactMessageResponse{Change: res.Change, Reactions: pbconv.ReactionSummary(res.Reactions)}, nil
}

func (s *Service) PinMessage(ctx context.Context, req *chatimv1.PinMessageRequest) (*chatimv1.PinMessageResponse, error) {
	cmd, err := pinCmdOf(ctx, req.GetRoomId(), req.GetThreadRoot(), req.GetSeq())
	if err != nil {
		return nil, err
	}
	state, err := s.mutator.Pin(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return &chatimv1.PinMessageResponse{PinVersion: state.Version, Pins: pbconv.Pins(state.Pins)}, nil
}

func (s *Service) UnpinMessage(ctx context.Context, req *chatimv1.UnpinMessageRequest) (*chatimv1.UnpinMessageResponse, error) {
	cmd, err := pinCmdOf(ctx, req.GetRoomId(), req.GetThreadRoot(), req.GetSeq())
	if err != nil {
		return nil, err
	}
	state, err := s.mutator.Unpin(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return &chatimv1.UnpinMessageResponse{PinVersion: state.Version, Pins: pbconv.Pins(state.Pins)}, nil
}

func pinCmdOf(ctx context.Context, roomID string, thread, seq uint64) (mutate.PinCmd, error) {
	who, room, err := callerAndRoom(ctx, roomID)
	if err != nil {
		return mutate.PinCmd{}, err
	}
	return mutate.PinCmd{Tenant: who.tenant, User: who.user, Room: room, Thread: thread, Seq: seq}, nil
}
```

**Step 8: Wiring + README**

`apps/core/service_wiring.go` (thay cả file):

```go
package main

import (
	"fmt"
	"log/slog"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/counter"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/grpcsrv"
	"github.com/ivannguyendev/chatim/apps/core/internal/mutate"
	"github.com/ivannguyendev/chatim/apps/core/internal/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

func wireService(st *mongostore.Store, router *actor.Router, pub *publish.Publisher, lockedKinds []domain.Kind, limits mutate.Limits, log *slog.Logger) (*grpcsrv.Service, error) {
	checker, err := access.NewChecker(st, access.DefaultPolicy{LockedKinds: lockedKinds})
	if err != nil {
		return nil, fmt.Errorf("wire access checker: %w", err)
	}
	reactions, pins := st.Reactions(), st.Pins()
	counts, err := counter.New(st, reactions)
	if err != nil {
		return nil, fmt.Errorf("wire reaction counter: %w", err)
	}
	projector, err := pinproj.New(pins, st)
	if err != nil {
		return nil, fmt.Errorf("wire pin projector: %w", err)
	}
	mut, err := mutate.New(mutate.Deps{
		Access: checker, Messages: st, Edits: st, Hidden: st, Rooms: st, Events: pub,
		Reactions: reactions, Counter: counts, Pins: pins, Projector: projector, Limits: limits,
	})
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

`apps/core/wiring.go`: thay `svc, err := wireService(st, router, pub, cfg.LockedMessageKinds, log)` bằng `svc, err := wireService(st, router, pub, cfg.LockedMessageKinds, cfg.Limits, log)`.

`README.md`, bảng env, ngay sau dòng `MESSAGE_LOCKED_KINDS` thêm:

```markdown
| `REACTION_MAX_EMOJIS` | `20` | Số loại emoji tối đa trên một tin (1–100). Giới hạn mềm: đọc từ số đếm `messages.rx` nên hai lệnh đồng thời có thể vượt một chút; gỡ reaction và emoji đã có trên tin không bị chặn (D89) |
| `PIN_LIMIT` | `50` | Số tin ghim tối đa mỗi room (1–1000), chính xác vì pv dày; ghim lại tin đã ghim vẫn thành công (D92) |
| `REACTION_COUNT_DELAY` | `1s` | Delay của effect `reaction_counter`: worker đếm lại `messages.rx` một lần mỗi tin mỗi lô sau `CommittedAt + D`; phải dương và không quá `RECONCILE_DELAY` (D90) |
```

**Step 9: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/internal/grpcsrv/... ./apps/core/"
make vet
```

Expected: PASS (test `apps/core` không cần hạ tầng; itest bị bỏ qua khi thiếu `CHATIM_IT_*`). `wc -l apps/core/internal/grpcsrv/*.go apps/core/service_wiring.go`: mỗi file < 200 (`react_pin_test.go` ~120, `caller_identity_test.go` ~112, `service_wiring.go` ~47).

**Step 10: Itest**

```bash
make infra-up && make itest
```

Expected: mọi package `ok` (wiring mới dựng `counter`/`pinproj` trên Mongo thật; itest cũ không đụng reaction/ghim, Task 16 thêm itest riêng).

**Step 11: INDEXES + commit**

`INDEXES.csv`:
- Dòng `apps/core/internal/grpcsrv`: purpose, sau "CoreService handlers CreateRoom/SendMessage/GetHistory/EditMessage/DeleteMessage/HideMessage/ClearHistory/GetEditHistory" thêm "/ReactMessage/PinMessage/UnpinMessage"; nối "; ReactMessage returns the change number and the reaction counts (pbconv.ReactionSummary), PinMessage/UnpinMessage return the pin version and the current pins (pbconv.Pins); all three are thin calls into mutate (react_pin.go)"; key_symbols nối `;Service.ReactMessage;Service.PinMessage;Service.UnpinMessage`; decisions nối `;D89;D92;D94`.
- Dòng `apps/core`: purpose nối "; wireService takes MESSAGE_LOCKED_KINDS and cfg.Limits (REACTION_MAX_EMOJIS, PIN_LIMIT) for the mutator"; decisions nối `;D92`.
- Dòng `README.md` (nếu có): nhắc bảng env có `REACTION_MAX_EMOJIS`, `PIN_LIMIT`, `REACTION_COUNT_DELAY`.

```bash
make fmt-check && make vet && make lint
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
git add apps/core/internal/grpcsrv/react_pin.go apps/core/internal/grpcsrv/react_pin_test.go
git commit -m "feat(grpcsrv): react, pin and unpin RPCs" -- apps/core/internal/grpcsrv/ apps/core/service_wiring.go apps/core/wiring.go README.md INDEXES.csv
```

Expected: `{7}`; `README.md` chỉ đổi 3 dòng env (nếu `README.md` có thay đổi chưa commit của người khác: dừng, báo controller).

Task rủi ro: một reviewer (luật delay, `Limits` qua `Validate` một chỗ, mã lỗi của 3 RPC, wiring dùng `st.Reactions()`/`st.Pins()` theo hợp đồng đã chỉnh).

---

### Task 12: `view` che `Reactions` + test `GetHistory` trả số đếm

Từ Task 3, `pbconv.Message` đặt `Message.reactions` từ `domain.Message.Reactions`, nên `GetHistory` đã trả số đếm mà không đổi luồng. Placeholder thì không được lộ nội dung (D85): `view.MaskDeleted` (tin đã xoá) và `view.HideForViewer` (tin người đọc đã ẩn hoặc nằm dưới mốc clear) đặt `Reactions = domain.ReactionSummary{}` cùng lúc xoá `Text`, nên `pbconv.ReactionSummary` trả `nil` và client không thấy `reactions`. Bước nào cũng làm trên bản sao (`eachCopy`), không sửa trang đầu vào.

Giả định: Task 2 đã sửa các test `view` so `[]domain.Message` bằng `slices.Equal` (từ Task 2 `domain.Message` không còn `==`); task này chỉ thêm file test mới, không đụng `masks_test.go`.

**Files:**
- Modify: `apps/core/internal/view/masks.go`
- Create: `apps/core/internal/view/reaction_masks_test.go`
- Create: `apps/core/internal/grpcsrv/history_reactions_test.go`
- Modify: `INDEXES.csv`

**Step 1: Test**

`apps/core/internal/view/reaction_masks_test.go`:

```go
package view_test

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/view"
)

var thumbs = []domain.ReactionCount{{Emoji: "👍", Count: 2}}

func reacted(seq uint64, deleted bool) domain.Message {
	return domain.Message{
		Room: 7, Seq: seq, From: "alice", Text: "t", Deleted: deleted,
		Reactions: domain.ReactionSummary{Counts: slices.Clone(thumbs), Version: 3},
	}
}

func counted(m domain.Message) bool {
	return m.Reactions.Version == 3 && slices.Equal(m.Reactions.Counts, thumbs)
}

func uncounted(m domain.Message) bool {
	return m.Reactions.Version == 0 && m.Reactions.Counts == nil
}

func TestPlaceholdersCarryNoReactionCounts(t *testing.T) {
	page := []domain.Message{reacted(1, false), reacted(2, true), reacted(3, false)}
	masked := view.MaskDeleted(view.Viewer{}, page)
	if !counted(masked[0]) || !uncounted(masked[1]) || !counted(masked[2]) {
		t.Fatalf("MaskDeleted reactions = %+v, want only the deleted seq 2 without counts", masked)
	}
	hidden := view.HideForViewer(view.Viewer{ClearedBeforeSeq: 1, HiddenSeqs: map[uint64]bool{3: true}}, page)
	if !uncounted(hidden[0]) || !counted(hidden[1]) || !uncounted(hidden[2]) {
		t.Fatalf("HideForViewer reactions = %+v, want seq 1 and 3 without counts", hidden)
	}
	for _, m := range page {
		if !counted(m) {
			t.Fatalf("input page was modified: %+v", m)
		}
	}
}
```

`apps/core/internal/grpcsrv/history_reactions_test.go`:

```go
package grpcsrv_test

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestHistoryReturnsReactionCountsButNotOnPlaceholders(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice", "bob", "carol")
	alice, bob, carol := as(t, "acme", "alice"), as(t, "acme", "bob"), as(t, "acme", "carol")
	for _, cid := range []string{"c-1", "c-2", "c-3"} {
		rg.send(t, alice, room, cid, "hi "+cid)
	}
	react := func(ctx context.Context, seq uint64, emoji string) {
		t.Helper()
		if _, err := rg.client.ReactMessage(ctx, &chatimv1.ReactMessageRequest{RoomId: room, Seq: seq, Emoji: emoji}); err != nil {
			t.Fatalf("ReactMessage seq %d %q: %v", seq, emoji, err)
		}
	}
	react(bob, 1, "👍")
	react(carol, 1, "👍")
	react(alice, 1, "❤️")
	react(bob, 2, "🎉")
	react(bob, 3, "👀")
	if _, err := rg.client.DeleteMessage(alice, &chatimv1.DeleteMessageRequest{RoomId: room, Seq: 2}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	if _, err := rg.client.HideMessage(bob, &chatimv1.HideMessageRequest{RoomId: room, Seq: 3}); err != nil {
		t.Fatalf("HideMessage: %v", err)
	}
	first := pbconv.ReactionSummary(domain.ReactionSummary{Counts: []domain.ReactionCount{{Emoji: "👍", Count: 2}, {Emoji: "❤️", Count: 1}}, Version: 3})
	third := pbconv.ReactionSummary(domain.ReactionSummary{Counts: []domain.ReactionCount{{Emoji: "👀", Count: 1}}, Version: 1})
	cases := []struct {
		name string
		ctx  context.Context
		want []*chatimv1.ReactionSummary
	}{
		{"bob", bob, []*chatimv1.ReactionSummary{first, nil, nil}},
		{"alice", alice, []*chatimv1.ReactionSummary{first, nil, third}},
	}
	for _, c := range cases {
		resp, err := rg.client.GetHistory(c.ctx, &chatimv1.GetHistoryRequest{RoomId: room, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_OLDEST})
		if err != nil || len(resp.GetMessages()) != 3 {
			t.Fatalf("%s GetHistory = %v, %v; want 3 messages", c.name, resp, err)
		}
		for i, m := range resp.GetMessages() {
			if got := m.GetReactions(); !proto.Equal(got, c.want[i]) {
				t.Fatalf("%s message %d reactions = %v, want %v", c.name, m.GetSeq(), got, c.want[i])
			}
		}
	}
}
```

Số đếm của seq 1 sắp theo `SortReactionCounts` (👍 2 trước ❤️ 1); version 3 vì ba lần touch inline đều bump.

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/view/... ./apps/core/internal/grpcsrv/..."`
Expected: FAIL: `MaskDeleted reactions = [...], want only the deleted seq 2 without counts`; `bob message 2 reactions = counts:{emoji:"🎉" count:1} version:1, want <nil>`.

**Step 3: Code**

`apps/core/internal/view/masks.go`, thay hai hàm bước bằng:

```go
func MaskDeleted(_ Viewer, msgs []domain.Message) []domain.Message {
	return eachCopy(msgs, func(m *domain.Message) {
		if m.Deleted {
			m.Text = ""
			m.Reactions = domain.ReactionSummary{}
		}
	})
}

func HideForViewer(v Viewer, msgs []domain.Message) []domain.Message {
	return eachCopy(msgs, func(m *domain.Message) {
		if m.Seq <= v.ClearedBeforeSeq || v.HiddenSeqs[m.Seq] {
			m.Hidden = true
			m.Text = ""
			m.Reactions = domain.ReactionSummary{}
		}
	})
}
```

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/view/... ./apps/core/internal/grpcsrv/..."`
Expected: PASS (test cũ `masks_test.go`, `history_masks_test.go` giữ nguyên). `wc -l apps/core/internal/view/*.go apps/core/internal/grpcsrv/history_reactions_test.go`: mỗi file < 200.

**Step 5: INDEXES + commit**

`INDEXES.csv`:
- Dòng `apps/core/internal/view`: purpose, "MaskDeleted drops the text of deleted messages" → "MaskDeleted drops the text and the reaction counts of deleted messages"; "into hidden placeholders without text" → "into hidden placeholders without text or reaction counts"; decisions nối `;D90`.
- Dòng `apps/core/internal/grpcsrv`: purpose nối "; GetHistory returns Message.reactions (counts per emoji only, own emoji is M3) except on deleted or hidden placeholders"; decisions nối `;D90` nếu chưa có.

```bash
make fmt-check && make vet && make lint
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
git add apps/core/internal/view/reaction_masks_test.go apps/core/internal/grpcsrv/history_reactions_test.go
git commit -m "feat(view): drop reaction counts from placeholders" -- apps/core/internal/view/ apps/core/internal/grpcsrv/history_reactions_test.go INDEXES.csv
```

Expected: `{7}`.

Task trung bình: controller kiểm nhanh (không reviewer). Task 12 và 13 khác file, có thể kiểm Task 12 trong lúc làm Task 13.

---

### Task 13: ★ 4 effect (`reaction_counter`, `reaction_event`, `pin_projection`, `pin_event`) + registry + metrics + luật 16

Lưới an toàn của fast path Task 9/10, chạy trên record `ReactionChanged` và `PinInserted` (Task 6/7), theo thứ tự registry với delay tăng dần:

```
ReactionChanged → room_activity (0) → reaction_counter (REACTION_COUNT_DELAY) → reaction_event (RECONCILE_DELAY)
PinInserted     → room_activity (0) → pin_projection (0)                     → pin_event (RECONCILE_DELAY)
```

1. `reaction_counter` (D90): gom record của lô theo tin (`recordKey`); witness = `{User, max Version}` của từng user (thứ tự gặp đầu tiên); mỗi tin một lần: `Find` tin → loại room (cache) → `Counter.Touch(key, msg.Reactions, witnesses, cfg.Tries)`. Bump → `Repaired()++` (fast path đã để summary cũ: detector CT1). `summary.Version > 0` → publish `pbconv.CountsChanged(type, msg với summary, Now())` id `{room}-{th}-{seq}-reactions-v{v}` (fast path đã gửi thì stream bỏ trùng; `Republished` chỉ đếm PubAck không trùng); `Version == 0` (tin chưa từng có số đếm, ví dụ đặt rồi gỡ trước khi touch) → không publish. Tin hoặc room không còn → bỏ, `Dropped` cộng theo số record; lỗi khác (`ErrStaleRead` do witness chưa thấy, `counter.ErrContended`, lỗi store) → lỗi cho **mọi** record của tin (Nak, lô sau làm lại).
2. `reaction_event` (D93): mỗi record `Reactions.Get(key, rec.User)`: không có doc → bỏ; `doc.N > rec.Version` → bỏ qua (record mới hơn sẽ phát); `doc.N < rec.Version` → `store.ErrStaleRead` (Nak); bằng → publish `pbconv.ReactionChanged(type, doc)`. Không ack mark.
3. `pin_projection` (D92, delay 0): gom theo room, mỗi room một `Projector.Project(room, max pv của room trong lô)` (`Seq` của record = pv). Room không còn (`errors.Is(err, domain.ErrRoomNotFound)`) → bỏ, `Dropped` theo số record; lỗi khác (kể cả `ErrInvalidArgument` từ `ValidateVersionBump`, để không nuốt lỗi dữ liệu) (kể cả `ErrStaleRead` khi fact chưa thấy) → lỗi cho mọi record của room.
4. `pin_event` (D93): `Pins.At(room, pv)` → `Find` tin → loại room → `pbconv.PinChanged` (tin đã xoá: payload không text) → publish. Fact, tin hoặc room không còn → bỏ.

Ba effect phát event dùng chung `eventPublisher` (file mới): vòng publish + chờ PubAck (`countStored`), cache loại room, bộ đếm `Republished`/`Dropped`, kiểm config (`eventConfig`, dùng lại `MessageChangedConfig`). `groupRecords` (generic) gom chỉ số record theo khoá cho `reaction_counter` và `pin_projection`. `msg_changed` và các effect cũ không đổi.

**Metrics:** `effectSet.counters()` thêm `reaction_counter`, `reaction_event`, `pin_event` (republished + dropped) và `pin_projection` (chỉ dropped). `probes.counterRepairs` (`{pbconv.ReactionsCounter: fx.reactionCounter.Repaired}`) → `chatim_core_counter_repaired_total{counter="reactions"}`. Luật mới `ChatimCounterRepairSurge` (CT1). Các luật cũ `ChatimWorkFailing`, `ChatimEffectDropping` (`sum by (effect)`), `ChatimRepublishSurge` đã phủ bốn effect.

Wiring tách file `apps/core/reaction_pin_effects_wiring.go` (`effectSet.wireReactionPinEffects`) để `effects_wiring.go` không phình; `counter.New`/`pinproj.New` dựng riêng ở đây (không giữ trạng thái, hai bản với `service_wiring.go` là vô hại). `st.Reactions()`/`st.Pins()` theo hợp đồng đã chỉnh (Task 9, G1).

**Files:**
- Modify: `apps/core/internal/effects/ports.go`
- Create: `apps/core/internal/effects/event_publisher.go`
- Create: `apps/core/internal/effects/record_groups.go`
- Modify (thay cả file): `apps/core/internal/effects/reaction_counter.go`
- Create: `apps/core/internal/effects/reaction_event.go`
- Create: `apps/core/internal/effects/pin_projection.go`
- Create: `apps/core/internal/effects/pin_event.go`
- Create: `apps/core/internal/effects/reaction_fixtures_test.go`
- Create: `apps/core/internal/effects/effect_spies_test.go`
- Create: `apps/core/internal/effects/reaction_counter_test.go`
- Create: `apps/core/internal/effects/reaction_event_test.go`
- Create: `apps/core/internal/effects/pin_projection_test.go`
- Create: `apps/core/internal/effects/pin_event_test.go`
- Create: `apps/core/internal/effects/effect_constructors_test.go`
- Modify: `apps/core/effects_wiring.go`
- Create: `apps/core/reaction_pin_effects_wiring.go`
- Create: `apps/core/effects_wiring_test.go`
- Modify: `apps/core/metrics_wiring.go`, `apps/core/metrics_wiring_test.go`, `apps/core/wiring.go`
- Modify: `deploy/prometheus/alerts.yml`
- Modify: `INDEXES.csv`

`harness_test.go` (199 dòng) không đổi; fixture mới nằm ở `reaction_fixtures_test.go` và `effect_spies_test.go`.

**Step 1: Fixture test**

`apps/core/internal/effects/effect_spies_test.go`:

```go
package effects_test

import (
	"context"
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type touchCall struct {
	key       store.MsgKey
	witnesses []store.Witness
	tries     int
}

func sameTouch(a, b touchCall) bool {
	return a.key == b.key && a.tries == b.tries && slices.Equal(a.witnesses, b.witnesses)
}

type spyCounter struct {
	inner effects.CounterToucher
	err   error
	calls []touchCall
}

func (s *spyCounter) Touch(ctx context.Context, k store.MsgKey, cur domain.ReactionSummary, ws []store.Witness, tries int) (domain.ReactionSummary, bool, error) {
	s.calls = append(s.calls, touchCall{key: k, witnesses: slices.Clone(ws), tries: tries})
	if s.err != nil {
		return domain.ReactionSummary{}, false, s.err
	}
	return s.inner.Touch(ctx, k, cur, ws, tries)
}

type projectCall struct{ room, target uint64 }

type spyProjector struct {
	inner effects.PinProjecter
	fail  map[uint64]error
	calls []projectCall
}

func (s *spyProjector) Project(ctx context.Context, r, target uint64) (domain.PinState, error) {
	s.calls = append(s.calls, projectCall{room: r, target: target})
	if err := s.fail[r]; err != nil {
		return domain.PinState{}, err
	}
	return s.inner.Project(ctx, r, target)
}

type brokenReactions struct{}

func (brokenReactions) Get(context.Context, store.MsgKey, string) (domain.Reaction, bool, error) {
	return domain.Reaction{}, false, errBoom
}

type brokenPins struct{}

func (brokenPins) At(context.Context, uint64, uint64) (domain.PinAction, error) {
	return domain.PinAction{}, errBoom
}
```

`brokenStore` đã có `Get` (room) và `At` (edit) nên không thể cài `ReactionReader`/`PinFacts`; hai kiểu hỏng riêng ở trên.

`apps/core/internal/effects/reaction_fixtures_test.go`:

```go
package effects_test

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/counter"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

const (
	countDelay        = time.Second
	otherRoom  uint64 = 4343
)

var countedAt = time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)

type reactRig struct {
	msgs      *memstore.Messages
	rooms     *memstore.Rooms
	reactions *memstore.Reactions
	pins      *memstore.Pins
	js        *publishtest.JetStream
	touches   *spyCounter
	projects  *spyProjector
	counter   *effects.ReactionCounter
	event     *effects.ReactionEvent
	pinProj   *effects.PinProjection
	pinEvent  *effects.PinEvent
}

func newReactRig(t *testing.T) *reactRig {
	t.Helper()
	rg := &reactRig{msgs: memstore.NewMessages(), rooms: memstore.NewRooms(), reactions: memstore.NewReactions(), pins: memstore.NewPins(), js: &publishtest.JetStream{}}
	createRoom(t, rg.rooms, room)
	counts, err := counter.New(rg.msgs, rg.reactions)
	if err != nil {
		t.Fatalf("counter.New: %v", err)
	}
	proj, err := pinproj.New(rg.pins, rg.rooms)
	if err != nil {
		t.Fatalf("pinproj.New: %v", err)
	}
	rg.touches, rg.projects = &spyCounter{inner: counts}, &spyProjector{inner: proj}
	events := effects.MessageChangedConfig{SubjectRoot: "evt", Delay: delay, RoomCache: 16}
	if rg.counter, err = effects.NewReactionCounter(
		effects.ReactionCounterDeps{Messages: rg.msgs, Counter: rg.touches, Rooms: rg.rooms, JS: rg.js, Now: func() time.Time { return countedAt }},
		effects.ReactionCounterConfig{SubjectRoot: "evt", Delay: countDelay, RoomCache: 16, Tries: 2},
	); err != nil {
		t.Fatalf("NewReactionCounter: %v", err)
	}
	if rg.event, err = effects.NewReactionEvent(effects.ReactionEventDeps{Reactions: rg.reactions, Rooms: rg.rooms, JS: rg.js}, events); err != nil {
		t.Fatalf("NewReactionEvent: %v", err)
	}
	if rg.pinProj, err = effects.NewPinProjection(rg.projects); err != nil {
		t.Fatalf("NewPinProjection: %v", err)
	}
	if rg.pinEvent, err = effects.NewPinEvent(effects.PinEventDeps{Pins: rg.pins, Messages: rg.msgs, Rooms: rg.rooms, JS: rg.js}, events); err != nil {
		t.Fatalf("NewPinEvent: %v", err)
	}
	return rg
}

func (rg *reactRig) message(t *testing.T, r, seq uint64) {
	t.Helper()
	m := domain.Message{Room: r, Seq: seq, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "hi", CID: "c", CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert %d/%d: %+v", r, seq, res)
	}
}

func (rg *reactRig) stored(t *testing.T, seq uint64) domain.Message {
	t.Helper()
	found, err := rg.msgs.Find(t.Context(), room, []store.MsgKey{{Room: room, Seq: seq}})
	if err != nil || len(found) != 1 {
		t.Fatalf("find seq %d = %v, %v", seq, found, err)
	}
	return found[0]
}

func (rg *reactRig) react(t *testing.T, seq uint64, user, emoji string) {
	t.Helper()
	at := time.Now().UTC().Truncate(time.Millisecond)
	var err error
	if emoji == "" {
		_, _, err = rg.reactions.Remove(t.Context(), store.MsgKey{Room: room, Seq: seq}, user, at)
	} else {
		_, _, err = rg.reactions.Set(t.Context(), domain.Reaction{Room: room, Seq: seq, Tenant: tenant, User: user, Emoji: emoji, At: at})
	}
	if err != nil {
		t.Fatalf("%s reacts %q on seq %d: %v", user, emoji, seq, err)
	}
}

func (rg *reactRig) current(t *testing.T, seq uint64, user string) domain.Reaction {
	t.Helper()
	doc, found, err := rg.reactions.Get(t.Context(), store.MsgKey{Room: room, Seq: seq}, user)
	if err != nil || !found {
		t.Fatalf("reaction of %s on seq %d = %+v, %v, %v", user, seq, doc, found, err)
	}
	return doc
}

func (rg *reactRig) pin(t *testing.T, r, pv uint64, op domain.PinOp, seq uint64) domain.PinAction {
	t.Helper()
	a := domain.PinAction{Room: r, PV: pv, Tenant: tenant, Op: op, Seq: seq, By: "bob", At: time.Now().UTC().Truncate(time.Millisecond)}
	if err := rg.pins.Append(t.Context(), a); err != nil {
		t.Fatalf("append pin %d/%d: %v", r, pv, err)
	}
	return a
}

func reactionRec(seq uint64, user string, n uint32) work.Record {
	return work.Record{Kind: store.ReactionChanged, Room: room, Seq: seq, Version: n, User: user, CommittedAt: time.Now()}
}

func pinRec(r, pv uint64) work.Record {
	return work.Record{Kind: store.PinInserted, Room: r, Seq: pv, CommittedAt: time.Now()}
}
```

**Step 2: Test effect**

`apps/core/internal/effects/reaction_counter_test.go`:

```go
package effects_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

func TestReactionCounterDeclaresItsPolicy(t *testing.T) {
	e := newReactRig(t).counter.Effect()
	if e.Name != effects.ReactionCounterName || e.Delay != countDelay || e.Run == nil {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.ReactionCounterName, countDelay)
	}
}

func TestReactionCounterTouchesEachMessageOnceWithEveryWitness(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	rg.message(t, room, 2)
	rg.react(t, 1, "bob", "👍")
	rg.react(t, 1, "carol", "👍")
	rg.react(t, 1, "bob", "❤️")
	rg.react(t, 2, "bob", "🎉")
	recs := []work.Record{reactionRec(1, "bob", 1), reactionRec(1, "carol", 1), reactionRec(2, "bob", 1), reactionRec(1, "bob", 2)}
	if errs := rg.counter.Effect().Run(t.Context(), recs); !allNil(errs, 4) {
		t.Fatalf("errs = %v", errs)
	}
	want := []touchCall{
		{key: store.MsgKey{Room: room, Seq: 1}, witnesses: []store.Witness{{User: "bob", N: 2}, {User: "carol", N: 1}}, tries: 2},
		{key: store.MsgKey{Room: room, Seq: 2}, witnesses: []store.Witness{{User: "bob", N: 1}}, tries: 2},
	}
	if !slices.EqualFunc(rg.touches.calls, want, sameTouch) {
		t.Fatalf("touches = %+v, want one per message with the newest change of each user", rg.touches.calls)
	}
	counts := []domain.ReactionCount{{Emoji: "❤️", Count: 1}, {Emoji: "👍", Count: 1}}
	domain.SortReactionCounts(counts)
	if got := rg.stored(t, 1).Reactions; got.Version != 1 || !slices.Equal(got.Counts, counts) {
		t.Fatalf("summary of seq 1 = %+v, want %v at version 1", got, counts)
	}
	ids := storedEventIDs(rg.js)
	if !slices.Equal(ids, []string{pbconv.ReactionCountsEventID(room, 0, 1, 1), pbconv.ReactionCountsEventID(room, 0, 2, 1)}) {
		t.Fatalf("stored = %v", ids)
	}
	if subj := rg.js.Stored()[0].Subject; subj != "evt.acme.room.4242.counts_changed" {
		t.Fatalf("subject = %q", subj)
	}
	events, err := rg.js.Events()
	if want := pbconv.CountsChanged(domain.RoomGroup, rg.stored(t, 1), countedAt); err != nil || !proto.Equal(events[0], want) {
		t.Fatalf("event = %v, %v; want %v", events, err, want)
	}
	if rg.counter.Repaired() != 2 || rg.counter.Republished() != 2 || rg.counter.Dropped() != 0 {
		t.Fatalf("repaired %d, republished %d, dropped %d; want 2, 2 and 0", rg.counter.Repaired(), rg.counter.Republished(), rg.counter.Dropped())
	}
}

func TestReactionCounterRepublishesAnUpToDateSummaryWithoutRepairing(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	rg.react(t, 1, "bob", "👍")
	recs := []work.Record{reactionRec(1, "bob", 1)}
	for range 2 {
		if errs := rg.counter.Effect().Run(t.Context(), recs); !allNil(errs, 1) {
			t.Fatalf("errs = %v", errs)
		}
	}
	if rg.counter.Repaired() != 1 || rg.counter.Republished() != 1 || len(rg.js.Attempts()) != 2 || len(rg.js.Stored()) != 1 {
		t.Fatalf("repaired %d, republished %d, attempts %d, stored %d; want 1, 1, 2 and 1",
			rg.counter.Repaired(), rg.counter.Republished(), len(rg.js.Attempts()), len(rg.js.Stored()))
	}
	if got := rg.stored(t, 1).Reactions; got.Version != 1 {
		t.Fatalf("summary = %+v, want version 1 kept by the equal recount", got)
	}
}

func TestReactionCounterSkipsAMessageThatNeverHadCounts(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	rg.react(t, 1, "bob", "👍")
	rg.react(t, 1, "bob", "")
	if errs := rg.counter.Effect().Run(t.Context(), []work.Record{reactionRec(1, "bob", 2)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if len(rg.touches.calls) != 1 || rg.counter.Repaired() != 0 || len(rg.js.Attempts()) != 0 {
		t.Fatalf("touches %d, repaired %d, attempts %d; want 1, 0 and 0", len(rg.touches.calls), rg.counter.Repaired(), len(rg.js.Attempts()))
	}
}

func TestReactionCounterRetriesEveryRecordOfAStaleMessage(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	rg.message(t, room, 2)
	rg.react(t, 1, "bob", "👍")
	rg.react(t, 2, "bob", "🎉")
	errs := rg.counter.Effect().Run(t.Context(), []work.Record{reactionRec(1, "bob", 1), reactionRec(1, "carol", 4), reactionRec(2, "bob", 1)})
	if len(errs) != 3 || !errors.Is(errs[0], store.ErrStaleRead) || !errors.Is(errs[1], store.ErrStaleRead) || errs[2] != nil {
		t.Fatalf("errs = %v, want a stale read for both records of seq 1 only", errs)
	}
	if ids := storedEventIDs(rg.js); !slices.Equal(ids, []string{pbconv.ReactionCountsEventID(room, 0, 2, 1)}) || rg.counter.Dropped() != 0 {
		t.Fatalf("stored %v, dropped %d; want only seq 2 counted", ids, rg.counter.Dropped())
	}
}

func TestReactionCounterDropsWhatIsGone(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, 999, 1)
	other := work.Record{Kind: store.ReactionChanged, Room: 999, Seq: 1, Version: 1, User: "bob", CommittedAt: time.Now()}
	recs := []work.Record{reactionRec(9, "bob", 1), reactionRec(9, "carol", 1), other}
	if errs := rg.counter.Effect().Run(t.Context(), recs); !allNil(errs, 3) {
		t.Fatalf("errs = %v, want nil so dropped records are not retried", errs)
	}
	if rg.counter.Dropped() != 3 || len(rg.touches.calls) != 0 || len(rg.js.Attempts()) != 0 {
		t.Fatalf("dropped %d, touches %d, attempts %d; want 3, 0 and 0", rg.counter.Dropped(), len(rg.touches.calls), len(rg.js.Attempts()))
	}
}

func TestReactionCounterRetriesWhenTheStoreFails(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	rg.touches.err = errBoom
	errs := rg.counter.Effect().Run(t.Context(), []work.Record{reactionRec(1, "bob", 1), reactionRec(1, "carol", 1)})
	if len(errs) != 2 || !errors.Is(errs[0], errBoom) || !errors.Is(errs[1], errBoom) || rg.counter.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want both records retried", errs, rg.counter.Dropped())
	}
	broken, err := effects.NewReactionCounter(
		effects.ReactionCounterDeps{Messages: brokenStore{}, Counter: &spyCounter{}, Rooms: brokenStore{}, JS: &publishtest.JetStream{}},
		effects.ReactionCounterConfig{SubjectRoot: "evt"},
	)
	if err != nil {
		t.Fatalf("NewReactionCounter: %v", err)
	}
	if errs := broken.Effect().Run(t.Context(), []work.Record{reactionRec(1, "bob", 1)}); len(errs) != 1 || !errors.Is(errs[0], errBoom) {
		t.Fatalf("errs = %v, want a retryable failure", errs)
	}
}
```

`apps/core/internal/effects/reaction_event_test.go`:

```go
package effects_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

func TestReactionEventDeclaresItsPolicy(t *testing.T) {
	e := newReactRig(t).event.Effect()
	if e.Name != effects.ReactionEventName || e.Delay != delay || e.Run == nil {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.ReactionEventName, delay)
	}
}

func TestReactionEventPublishesTheCurrentChange(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	rg.react(t, 1, "bob", "👍")
	if errs := rg.event.Effect().Run(t.Context(), []work.Record{reactionRec(1, "bob", 1)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.ReactionEventID(room, 0, 1, "bob", 1)}) {
		t.Fatalf("stored = %v", got)
	}
	if subj := rg.js.Stored()[0].Subject; subj != "evt.acme.room.4242.reaction_changed" {
		t.Fatalf("subject = %q", subj)
	}
	events, err := rg.js.Events()
	if want := pbconv.ReactionChanged(domain.RoomGroup, rg.current(t, 1, "bob")); err != nil || !proto.Equal(events[0], want) {
		t.Fatalf("event = %v, %v; want the fast path event %v", events, err, want)
	}
	if rg.event.Republished() != 1 || rg.event.Dropped() != 0 {
		t.Fatalf("republished %d, dropped %d; want 1 and 0", rg.event.Republished(), rg.event.Dropped())
	}
}

func TestReactionEventSkipsOlderChangesAndRetriesNewerOnes(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	rg.react(t, 1, "bob", "👍")
	rg.react(t, 1, "bob", "❤️")
	errs := rg.event.Effect().Run(t.Context(), []work.Record{reactionRec(1, "bob", 1), reactionRec(1, "bob", 3), reactionRec(1, "bob", 2)})
	if len(errs) != 3 || errs[0] != nil || !errors.Is(errs[1], store.ErrStaleRead) || errs[2] != nil {
		t.Fatalf("errs = %v, want only the change ahead of the read retried", errs)
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.ReactionEventID(room, 0, 1, "bob", 2)}) || rg.event.Dropped() != 0 {
		t.Fatalf("stored %v, dropped %d; want only change 2", got, rg.event.Dropped())
	}
}

func TestReactionEventCountsOnlyEventsTheStreamLacked(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	rg.react(t, 1, "bob", "👍")
	fast, err := publish.Message("evt", room, pbconv.ReactionChanged(domain.RoomGroup, rg.current(t, 1, "bob")))
	if err != nil {
		t.Fatalf("publish.Message: %v", err)
	}
	if _, err := rg.js.PublishMsgAsync(fast); err != nil {
		t.Fatalf("fast path publish: %v", err)
	}
	if errs := rg.event.Effect().Run(t.Context(), []work.Record{reactionRec(1, "bob", 1)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if len(rg.js.Stored()) != 1 || len(rg.js.Attempts()) != 2 || rg.event.Republished() != 0 {
		t.Fatalf("stored %d, attempts %d, republished %d; want 1, 2 and 0", len(rg.js.Stored()), len(rg.js.Attempts()), rg.event.Republished())
	}
}

func TestReactionEventDropsWhatIsGone(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	elsewhere := domain.Reaction{Room: 999, Seq: 1, Tenant: tenant, User: "bob", Emoji: "👍", At: time.Now().UTC().Truncate(time.Millisecond)}
	if _, _, err := rg.reactions.Set(t.Context(), elsewhere); err != nil {
		t.Fatalf("Set: %v", err)
	}
	other := work.Record{Kind: store.ReactionChanged, Room: 999, Seq: 1, Version: 1, User: "bob", CommittedAt: time.Now()}
	if errs := rg.event.Effect().Run(t.Context(), []work.Record{reactionRec(1, "carol", 1), other}); !allNil(errs, 2) {
		t.Fatalf("errs = %v, want nil so dropped records are not retried", errs)
	}
	if len(rg.js.Attempts()) != 0 || rg.event.Dropped() != 2 {
		t.Fatalf("attempts %d, dropped %d; want 0 and 2 (no reaction, no room)", len(rg.js.Attempts()), rg.event.Dropped())
	}
}

func TestReactionEventRetriesWhenTheStoreFails(t *testing.T) {
	eff, err := effects.NewReactionEvent(
		effects.ReactionEventDeps{Reactions: brokenReactions{}, Rooms: brokenStore{}, JS: &publishtest.JetStream{}},
		effects.MessageChangedConfig{SubjectRoot: "evt"},
	)
	if err != nil {
		t.Fatalf("NewReactionEvent: %v", err)
	}
	if errs := eff.Effect().Run(t.Context(), []work.Record{reactionRec(1, "bob", 1)}); len(errs) != 1 || !errors.Is(errs[0], errBoom) || eff.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want a retryable failure", errs, eff.Dropped())
	}
}
```

`apps/core/internal/effects/pin_projection_test.go`:

```go
package effects_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

func TestPinProjectionDeclaresItsPolicy(t *testing.T) {
	e := newReactRig(t).pinProj.Effect()
	if e.Name != effects.PinProjectionName || e.Delay != 0 || e.Run == nil {
		t.Fatalf("effect = %q delay %v, want %q delay 0", e.Name, e.Delay, effects.PinProjectionName)
	}
}

func TestPinProjectionFoldsEachRoomOnceUpToItsNewestFact(t *testing.T) {
	rg := newReactRig(t)
	createRoom(t, rg.rooms, otherRoom)
	rg.pin(t, room, 1, domain.PinOpPin, 1)
	rg.pin(t, room, 2, domain.PinOpPin, 2)
	rg.pin(t, otherRoom, 1, domain.PinOpPin, 5)
	recs := []work.Record{pinRec(room, 1), pinRec(otherRoom, 1), pinRec(room, 2), pinRec(999, 1)}
	if errs := rg.pinProj.Effect().Run(t.Context(), recs); !allNil(errs, 4) {
		t.Fatalf("errs = %v", errs)
	}
	want := []projectCall{{room: room, target: 2}, {room: otherRoom, target: 1}, {room: 999, target: 1}}
	if !slices.Equal(rg.projects.calls, want) {
		t.Fatalf("projects = %+v, want one per room up to its newest pin version", rg.projects.calls)
	}
	state, err := rg.rooms.PinState(t.Context(), room)
	if err != nil || state.Version != 2 || len(state.Pins) != 2 || state.Pins[0].Seq != 2 {
		t.Fatalf("projection = %+v, %v; want version 2 with seq 2 first", state, err)
	}
	if rg.pinProj.Dropped() != 1 {
		t.Fatalf("dropped %d, want 1 for the unknown room", rg.pinProj.Dropped())
	}
}

func TestPinProjectionRetriesOnlyTheRoomThatFailed(t *testing.T) {
	rg := newReactRig(t)
	createRoom(t, rg.rooms, otherRoom)
	rg.pin(t, room, 1, domain.PinOpPin, 1)
	rg.pin(t, otherRoom, 1, domain.PinOpPin, 5)
	rg.projects.fail = map[uint64]error{room: errBoom}
	errs := rg.pinProj.Effect().Run(t.Context(), []work.Record{pinRec(room, 1), pinRec(otherRoom, 1), pinRec(room, 1)})
	if len(errs) != 3 || !errors.Is(errs[0], errBoom) || errs[1] != nil || !errors.Is(errs[2], errBoom) || rg.pinProj.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want both records of the failed room retried", errs, rg.pinProj.Dropped())
	}
}
```

`apps/core/internal/effects/pin_event_test.go`:

```go
package effects_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

func TestPinEventDeclaresItsPolicy(t *testing.T) {
	e := newReactRig(t).pinEvent.Effect()
	if e.Name != effects.PinEventName || e.Delay != delay || e.Run == nil {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.PinEventName, delay)
	}
}

func TestPinEventPublishesPinsAndUnpinsWithTheMessage(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	pinned := rg.pin(t, room, 1, domain.PinOpPin, 1)
	unpinned := rg.pin(t, room, 2, domain.PinOpUnpin, 1)
	if errs := rg.pinEvent.Effect().Run(t.Context(), []work.Record{pinRec(room, 1), pinRec(room, 2)}); !allNil(errs, 2) {
		t.Fatalf("errs = %v", errs)
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.PinEventID(room, 1), pbconv.PinEventID(room, 2)}) {
		t.Fatalf("stored = %v", got)
	}
	stored := rg.js.Stored()
	if stored[0].Subject != "evt.acme.room.4242.msg_pinned" || stored[1].Subject != "evt.acme.room.4242.msg_unpinned" {
		t.Fatalf("subjects = %q, %q", stored[0].Subject, stored[1].Subject)
	}
	events, err := rg.js.Events()
	msg := rg.stored(t, 1)
	if err != nil || !proto.Equal(events[0], pbconv.PinChanged(domain.RoomGroup, msg, pinned)) || !proto.Equal(events[1], pbconv.PinChanged(domain.RoomGroup, msg, unpinned)) {
		t.Fatalf("events = %v, %v; want the fast path events", events, err)
	}
	if rg.pinEvent.Republished() != 2 || rg.pinEvent.Dropped() != 0 {
		t.Fatalf("republished %d, dropped %d; want 2 and 0", rg.pinEvent.Republished(), rg.pinEvent.Dropped())
	}
}

func TestPinEventHidesTheTextOfADeletedMessage(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	rg.pin(t, room, 1, domain.PinOpPin, 1)
	gone := domain.Edit{Room: room, Seq: 1, Version: 1, Kind: domain.EditDelete, Tenant: tenant, By: "alice", At: time.Now().UTC().Truncate(time.Millisecond)}
	if err := rg.msgs.ApplyEdit(t.Context(), gone); err != nil {
		t.Fatalf("ApplyEdit: %v", err)
	}
	if errs := rg.pinEvent.Effect().Run(t.Context(), []work.Record{pinRec(room, 1)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	events, err := rg.js.Events()
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %v, %v", events, err)
	}
	if m := events[0].GetMessagePinned().GetMessage(); !m.GetDeleted() || m.GetText() != "" {
		t.Fatalf("pinned message = %v, want deleted without text", m)
	}
}

func TestPinEventCountsOnlyEventsTheStreamLacked(t *testing.T) {
	rg := newReactRig(t)
	rg.message(t, room, 1)
	a := rg.pin(t, room, 1, domain.PinOpPin, 1)
	fast, err := publish.Message("evt", room, pbconv.PinChanged(domain.RoomGroup, rg.stored(t, 1), a))
	if err != nil {
		t.Fatalf("publish.Message: %v", err)
	}
	if _, err := rg.js.PublishMsgAsync(fast); err != nil {
		t.Fatalf("fast path publish: %v", err)
	}
	if errs := rg.pinEvent.Effect().Run(t.Context(), []work.Record{pinRec(room, 1)}); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if len(rg.js.Stored()) != 1 || len(rg.js.Attempts()) != 2 || rg.pinEvent.Republished() != 0 {
		t.Fatalf("stored %d, attempts %d, republished %d; want 1, 2 and 0", len(rg.js.Stored()), len(rg.js.Attempts()), rg.pinEvent.Republished())
	}
}

func TestPinEventDropsWhatIsGone(t *testing.T) {
	rg := newReactRig(t)
	rg.pin(t, room, 1, domain.PinOpPin, 7)
	rg.message(t, 999, 1)
	rg.pin(t, 999, 1, domain.PinOpPin, 1)
	if errs := rg.pinEvent.Effect().Run(t.Context(), []work.Record{pinRec(room, 9), pinRec(room, 1), pinRec(999, 1)}); !allNil(errs, 3) {
		t.Fatalf("errs = %v, want nil so dropped records are not retried", errs)
	}
	if len(rg.js.Attempts()) != 0 || rg.pinEvent.Dropped() != 3 {
		t.Fatalf("attempts %d, dropped %d; want 0 and 3 (no fact, no message, no room)", len(rg.js.Attempts()), rg.pinEvent.Dropped())
	}
}

func TestPinEventRetriesWhenTheStoreFails(t *testing.T) {
	eff, err := effects.NewPinEvent(
		effects.PinEventDeps{Pins: brokenPins{}, Messages: brokenStore{}, Rooms: brokenStore{}, JS: &publishtest.JetStream{}},
		effects.MessageChangedConfig{SubjectRoot: "evt"},
	)
	if err != nil {
		t.Fatalf("NewPinEvent: %v", err)
	}
	if errs := eff.Effect().Run(t.Context(), []work.Record{pinRec(room, 1)}); len(errs) != 1 || !errors.Is(errs[0], errBoom) || eff.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want a retryable failure", errs, eff.Dropped())
	}
}
```

`apps/core/internal/effects/effect_constructors_test.go`:

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

func TestNewReactionAndPinEffectsRejectBadInput(t *testing.T) {
	js, msgs, mem, reactions, pins := &publishtest.JetStream{}, memstore.NewMessages(), memstore.NewRooms(), memstore.NewReactions(), memstore.NewPins()
	counts, events, noRoot := &spyCounter{}, effects.MessageChangedConfig{SubjectRoot: "evt"}, effects.MessageChangedConfig{}
	count := effects.ReactionCounterConfig{SubjectRoot: "evt"}
	counterDeps := effects.ReactionCounterDeps{Messages: msgs, Counter: counts, Rooms: mem, JS: js}
	eventDeps := effects.ReactionEventDeps{Reactions: reactions, Rooms: mem, JS: js}
	pinDeps := effects.PinEventDeps{Pins: pins, Messages: msgs, Rooms: mem, JS: js}
	bad := map[string]func() error{
		"reaction_event without reactions": func() error { _, err := effects.NewReactionEvent(effects.ReactionEventDeps{Rooms: mem, JS: js}, events); return err },
		"reaction_event without rooms":     func() error { _, err := effects.NewReactionEvent(effects.ReactionEventDeps{Reactions: reactions, JS: js}, events); return err },
		"reaction_event without js":        func() error { _, err := effects.NewReactionEvent(effects.ReactionEventDeps{Reactions: reactions, Rooms: mem}, events); return err },
		"reaction_event without a root":    func() error { _, err := effects.NewReactionEvent(eventDeps, noRoot); return err },
		"reaction_counter without messages": func() error {
			_, err := effects.NewReactionCounter(effects.ReactionCounterDeps{Counter: counts, Rooms: mem, JS: js}, count)
			return err
		},
		"reaction_counter without a counter": func() error {
			_, err := effects.NewReactionCounter(effects.ReactionCounterDeps{Messages: msgs, Rooms: mem, JS: js}, count)
			return err
		},
		"reaction_counter without rooms": func() error {
			_, err := effects.NewReactionCounter(effects.ReactionCounterDeps{Messages: msgs, Counter: counts, JS: js}, count)
			return err
		},
		"reaction_counter without js": func() error {
			_, err := effects.NewReactionCounter(effects.ReactionCounterDeps{Messages: msgs, Counter: counts, Rooms: mem}, count)
			return err
		},
		"reaction_counter without a root": func() error {
			_, err := effects.NewReactionCounter(counterDeps, effects.ReactionCounterConfig{})
			return err
		},
		"reaction_counter with negative tries": func() error {
			_, err := effects.NewReactionCounter(counterDeps, effects.ReactionCounterConfig{SubjectRoot: "evt", Tries: -1})
			return err
		},
		"pin_projection without a projector": func() error { _, err := effects.NewPinProjection(nil); return err },
		"pin_event without pins":             func() error { _, err := effects.NewPinEvent(effects.PinEventDeps{Messages: msgs, Rooms: mem, JS: js}, events); return err },
		"pin_event without messages":         func() error { _, err := effects.NewPinEvent(effects.PinEventDeps{Pins: pins, Rooms: mem, JS: js}, events); return err },
		"pin_event without rooms":            func() error { _, err := effects.NewPinEvent(effects.PinEventDeps{Pins: pins, Messages: msgs, JS: js}, events); return err },
		"pin_event without js":               func() error { _, err := effects.NewPinEvent(effects.PinEventDeps{Pins: pins, Messages: msgs, Rooms: mem}, events); return err },
		"pin_event without a root":           func() error { _, err := effects.NewPinEvent(pinDeps, noRoot); return err },
	}
	for name, build := range bad {
		if err := build(); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s = %v, want ErrInvalidArgument", name, err)
		}
	}
	ctr, err := effects.NewReactionCounter(counterDeps, count)
	if err != nil || ctr.Effect().Delay != effects.DefaultCountDelay {
		t.Fatalf("reaction_counter with defaults = %v, %v; want delay %v", ctr, err, effects.DefaultCountDelay)
	}
	ev, err := effects.NewReactionEvent(eventDeps, events)
	if err != nil || ev.Effect().Delay != effects.DefaultDelay {
		t.Fatalf("reaction_event with defaults = %v, %v; want delay %v", ev, err, effects.DefaultDelay)
	}
	pe, err := effects.NewPinEvent(pinDeps, events)
	if err != nil || pe.Effect().Delay != effects.DefaultDelay {
		t.Fatalf("pin_event with defaults = %v, %v; want delay %v", pe, err, effects.DefaultDelay)
	}
}
```

**Step 3: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/effects/..."`
Expected: FAIL biên dịch: `undefined: effects.ReactionCounter`, `undefined: effects.NewReactionCounter`, `undefined: effects.ReactionEvent`, `undefined: effects.PinProjection`, `undefined: effects.PinEvent`, `undefined: effects.CounterToucher`, `undefined: effects.PinProjecter`, `undefined: effects.ReactionCounterName`.

**Step 4: Code effect**

`apps/core/internal/effects/ports.go`, thêm cuối file:

```go
type ReactionReader interface {
	Get(ctx context.Context, key store.MsgKey, user string) (domain.Reaction, bool, error)
}

type CounterToucher interface {
	Touch(ctx context.Context, key store.MsgKey, cur domain.ReactionSummary, witnesses []store.Witness, tries int) (domain.ReactionSummary, bool, error)
}

type PinFacts interface {
	At(ctx context.Context, room, pv uint64) (domain.PinAction, error)
}

type PinProjecter interface {
	Project(ctx context.Context, room, target uint64) (domain.PinState, error)
}
```

`apps/core/internal/effects/record_groups.go`:

```go
package effects

import (
	"sync/atomic"

	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

type recordGroup[K comparable] struct {
	key     K
	indexes []int
}

func groupRecords[K comparable](recs []work.Record, keyOf func(work.Record) K) []recordGroup[K] {
	at := make(map[K]int, len(recs))
	var out []recordGroup[K]
	for i, r := range recs {
		k := keyOf(r)
		j, ok := at[k]
		if !ok {
			j = len(out)
			at[k] = j
			out = append(out, recordGroup[K]{key: k})
		}
		out[j].indexes = append(out[j].indexes, i)
	}
	return out
}

func (g recordGroup[K]) fail(errs []error, err error) {
	for _, i := range g.indexes {
		errs[i] = err
	}
}

func (g recordGroup[K]) share(errs []error) {
	for _, i := range g.indexes[1:] {
		errs[i] = errs[g.indexes[0]]
	}
}

func (g recordGroup[K]) drop(n *atomic.Uint64) {
	for range g.indexes {
		n.Add(1)
	}
}

func recordRoom(r work.Record) uint64 { return r.Room }
```

`drop` cộng từng record (không `uint64(len(...))`, tránh G115).

`apps/core/internal/effects/event_publisher.go`:

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
	errs := make([]error, len(recs))
	var pending []pendingAck
	for i, r := range recs {
		ev, err := build(ctx, r)
		switch {
		case drop(err):
			p.dropped.Add(1)
		case err != nil:
			errs[i] = err
		case ev != nil:
			pending = p.queue(pending, errs, i, r.Room, ev)
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

`build` trả `(nil, nil)` khi record không cần event (bị record mới hơn thay thế, hoặc chưa từng có số đếm); `drop(nil)` luôn false.

`apps/core/internal/effects/reaction_event.go`:

```go
package effects

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const ReactionEventName = "reaction_event"

var errNoReaction = fmt.Errorf("reaction %w", apperr.ErrNotFound)

type ReactionEventDeps struct {
	Reactions ReactionReader
	Rooms     RoomReader
	JS        publish.JetStream
}

type ReactionEvent struct {
	eventPublisher
	reactions ReactionReader
	delay     time.Duration
}

func NewReactionEvent(deps ReactionEventDeps, cfg MessageChangedConfig) (*ReactionEvent, error) {
	if deps.Reactions == nil || deps.Rooms == nil || deps.JS == nil {
		return nil, fmt.Errorf("%w: %s needs reactions, rooms and a jetstream client", apperr.ErrInvalidArgument, ReactionEventName)
	}
	cfg, err := eventConfig(ReactionEventName, cfg)
	if err != nil {
		return nil, err
	}
	e := &ReactionEvent{reactions: deps.Reactions, delay: cfg.Delay}
	e.init(deps.JS, cfg.SubjectRoot, deps.Rooms, cfg.RoomCache)
	return e, nil
}

func (e *ReactionEvent) Effect() Effect {
	return Effect{Name: ReactionEventName, Delay: e.delay, Run: e.run}
}

func (e *ReactionEvent) run(ctx context.Context, recs []work.Record) []error {
	return e.each(ctx, recs, e.event, reactionGone)
}

func reactionGone(err error) bool { return gone(err) || errors.Is(err, errNoReaction) }

func (e *ReactionEvent) event(ctx context.Context, r work.Record) (*chatimv1.Event, error) {
	doc, found, err := e.reactions.Get(ctx, recordKey(r), r.User)
	switch {
	case err != nil:
		return nil, err
	case !found:
		return nil, errNoReaction
	case doc.N > r.Version:
		return nil, nil
	case doc.N < r.Version:
		return nil, store.ErrStaleRead
	}
	typ, err := e.types.get(ctx, r.Room)
	if err != nil {
		return nil, err
	}
	return pbconv.ReactionChanged(typ, doc), nil
}
```

`apps/core/internal/effects/reaction_counter.go` (thay cả file của Task 11):

```go
package effects

import (
	"cmp"
	"context"
	"fmt"
	"slices"
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

const (
	ReactionCounterName = "reaction_counter"
	DefaultCountDelay   = time.Second
	DefaultCounterTries = 3
)

type ReactionCounterDeps struct {
	Messages MessageFinder
	Counter  CounterToucher
	Rooms    RoomReader
	JS       publish.JetStream
	Now      func() time.Time
}

type ReactionCounterConfig struct {
	SubjectRoot string
	Delay       time.Duration
	RoomCache   int
	Tries       int
}

type ReactionCounter struct {
	eventPublisher
	deps     ReactionCounterDeps
	cfg      ReactionCounterConfig
	repaired atomic.Uint64
}

func NewReactionCounter(deps ReactionCounterDeps, cfg ReactionCounterConfig) (*ReactionCounter, error) {
	if deps.Messages == nil || deps.Counter == nil || deps.Rooms == nil || deps.JS == nil {
		return nil, fmt.Errorf("%w: %s needs messages, a counter, rooms and a jetstream client", apperr.ErrInvalidArgument, ReactionCounterName)
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	cfg.Delay = cmp.Or(cfg.Delay, DefaultCountDelay)
	cfg.RoomCache = cmp.Or(cfg.RoomCache, DefaultRoomCache)
	cfg.Tries = cmp.Or(cfg.Tries, DefaultCounterTries)
	if cfg.SubjectRoot == "" || cfg.Delay < 0 || cfg.RoomCache < 0 || cfg.Tries < 1 {
		return nil, fmt.Errorf("%w: %s config %+v needs a subject root, a delay, a room cache and a try", apperr.ErrInvalidArgument, ReactionCounterName, cfg)
	}
	c := &ReactionCounter{deps: deps, cfg: cfg}
	c.init(deps.JS, cfg.SubjectRoot, deps.Rooms, cfg.RoomCache)
	return c, nil
}

func (c *ReactionCounter) Effect() Effect {
	return Effect{Name: ReactionCounterName, Delay: c.cfg.Delay, Run: c.run}
}

func (c *ReactionCounter) Repaired() uint64 { return c.repaired.Load() }

func (c *ReactionCounter) run(ctx context.Context, recs []work.Record) []error {
	errs := make([]error, len(recs))
	groups := groupRecords(recs, recordKey)
	var pending []pendingAck
	for _, g := range groups {
		ev, err := c.touch(ctx, g.key, witnessesOf(recs, g.indexes))
		switch {
		case gone(err):
			g.drop(&c.dropped)
		case err != nil:
			g.fail(errs, err)
		case ev != nil:
			pending = c.queue(pending, errs, g.indexes[0], g.key.Room, ev)
		}
	}
	awaitAcks(ctx, pending, errs, countStored(&c.republished))
	for _, g := range groups {
		g.share(errs)
	}
	return errs
}

func (c *ReactionCounter) touch(ctx context.Context, key store.MsgKey, witnesses []store.Witness) (*chatimv1.Event, error) {
	found, err := c.deps.Messages.Find(ctx, key.Room, []store.MsgKey{key})
	switch {
	case err != nil:
		return nil, err
	case len(found) == 0:
		return nil, domain.ErrMessageNotFound
	}
	typ, err := c.types.get(ctx, key.Room)
	if err != nil {
		return nil, err
	}
	msg := found[0]
	summary, bumped, err := c.deps.Counter.Touch(ctx, key, msg.Reactions, witnesses, c.cfg.Tries)
	if err != nil {
		return nil, err
	}
	if bumped {
		c.repaired.Add(1)
	}
	if summary.Version == 0 {
		return nil, nil
	}
	msg.Reactions = summary
	return pbconv.CountsChanged(typ, msg, c.deps.Now().UTC().Truncate(time.Millisecond)), nil
}

func witnessesOf(recs []work.Record, indexes []int) []store.Witness {
	var out []store.Witness
	for _, i := range indexes {
		r := recs[i]
		j := slices.IndexFunc(out, func(w store.Witness) bool { return w.User == r.User })
		if j < 0 {
			out = append(out, store.Witness{User: r.User, N: r.Version})
			continue
		}
		out[j].N = max(out[j].N, r.Version)
	}
	return out
}
```

`share` chép lỗi của record đầu nhóm (lỗi publish/PubAck) sang các record còn lại của cùng tin; nhóm đã `fail` thì không đổi.

`apps/core/internal/effects/pin_projection.go`:

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

const PinProjectionName = "pin_projection"

type PinProjection struct {
	proj    PinProjecter
	dropped atomic.Uint64
}

func NewPinProjection(p PinProjecter) (*PinProjection, error) {
	if p == nil {
		return nil, fmt.Errorf("%w: %s needs a pin projector", apperr.ErrInvalidArgument, PinProjectionName)
	}
	return &PinProjection{proj: p}, nil
}

func (e *PinProjection) Effect() Effect {
	return Effect{Name: PinProjectionName, Run: e.run}
}

func (e *PinProjection) Dropped() uint64 { return e.dropped.Load() }

func (e *PinProjection) run(ctx context.Context, recs []work.Record) []error {
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
		}
	}
	return errs
}
```

`apps/core/internal/effects/pin_event.go`:

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

const PinEventName = "pin_event"

type PinEventDeps struct {
	Pins     PinFacts
	Messages MessageFinder
	Rooms    RoomReader
	JS       publish.JetStream
}

type PinEvent struct {
	eventPublisher
	deps  PinEventDeps
	delay time.Duration
}

func NewPinEvent(deps PinEventDeps, cfg MessageChangedConfig) (*PinEvent, error) {
	if deps.Pins == nil || deps.Messages == nil || deps.Rooms == nil || deps.JS == nil {
		return nil, fmt.Errorf("%w: %s needs pins, messages, rooms and a jetstream client", apperr.ErrInvalidArgument, PinEventName)
	}
	cfg, err := eventConfig(PinEventName, cfg)
	if err != nil {
		return nil, err
	}
	e := &PinEvent{deps: deps, delay: cfg.Delay}
	e.init(deps.JS, cfg.SubjectRoot, deps.Rooms, cfg.RoomCache)
	return e, nil
}

func (e *PinEvent) Effect() Effect {
	return Effect{Name: PinEventName, Delay: e.delay, Run: e.run}
}

func (e *PinEvent) run(ctx context.Context, recs []work.Record) []error {
	return e.each(ctx, recs, e.event, pinGone)
}

func pinGone(err error) bool { return gone(err) || errors.Is(err, store.ErrPinNotFound) }

func (e *PinEvent) event(ctx context.Context, r work.Record) (*chatimv1.Event, error) {
	fact, err := e.deps.Pins.At(ctx, r.Room, r.Seq)
	if err != nil {
		return nil, err
	}
	key := store.MsgKey{Room: fact.Room, Thread: fact.Thread, Seq: fact.Seq}
	found, err := e.deps.Messages.Find(ctx, fact.Room, []store.MsgKey{key})
	switch {
	case err != nil:
		return nil, err
	case len(found) == 0:
		return nil, domain.ErrMessageNotFound
	}
	typ, err := e.types.get(ctx, r.Room)
	if err != nil {
		return nil, err
	}
	return pbconv.PinChanged(typ, found[0], fact), nil
}
```

**Step 5: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/internal/effects/..."
```

Expected: PASS (test cũ của `msg_created`, `msg_changed`, `room_created`, `edit_projection`, workers không đổi). `wc -l apps/core/internal/effects/*.go`: mỗi file < 200 (`reaction_counter.go` ~135, `event_publisher.go` ~70, `reaction_counter_test.go` ~165, `pin_event_test.go` ~140, `effect_constructors_test.go` ~85, `reaction_fixtures_test.go` ~130, `harness_test.go` vẫn 199).

**Step 6: Wiring, metrics, luật alert**

`apps/core/effects_wiring.go` (thay cả file; dòng `Queue` giữ đúng chữ ký `NewQueue` của Task 7):

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
	workers         *effects.Workers
	msgCreated      *effects.MessageCreated
	roomCreated     *effects.RoomCreated
	editProjection  *effects.EditProjection
	msgChanged      *effects.MessageChanged
	reactionCounter *effects.ReactionCounter
	reactionEvent   *effects.ReactionEvent
	pinProjection   *effects.PinProjection
	pinEvent        *effects.PinEvent
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
	if err = fx.wireReactionPinEffects(cfg, cl, st); err != nil {
		return effectSet{}, err
	}
	activity := effects.NewRoomActivity(st)
	registry := effects.Registry{
		store.MessageInserted: {activity.Effect(), fx.msgCreated.Effect()},
		store.RoomInserted:    {fx.roomCreated.Effect()},
		store.EditInserted:    {activity.Effect(), fx.editProjection.Effect(), fx.msgChanged.Effect()},
		store.ReactionChanged: {activity.Effect(), fx.reactionCounter.Effect(), fx.reactionEvent.Effect()},
		store.PinInserted:     {activity.Effect(), fx.pinProjection.Effect(), fx.pinEvent.Effect()},
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
		fx.msgCreated.Effect().Name:      {republished: fx.msgCreated.Republished, dropped: fx.msgCreated.Dropped},
		fx.roomCreated.Effect().Name:     {republished: fx.roomCreated.Republished, dropped: fx.roomCreated.Dropped},
		fx.msgChanged.Effect().Name:      {republished: fx.msgChanged.Republished, dropped: fx.msgChanged.Dropped},
		fx.editProjection.Effect().Name:  {dropped: fx.editProjection.Dropped},
		fx.reactionCounter.Effect().Name: {republished: fx.reactionCounter.Republished, dropped: fx.reactionCounter.Dropped},
		fx.reactionEvent.Effect().Name:   {republished: fx.reactionEvent.Republished, dropped: fx.reactionEvent.Dropped},
		fx.pinEvent.Effect().Name:        {republished: fx.pinEvent.Republished, dropped: fx.pinEvent.Dropped},
		fx.pinProjection.Effect().Name:   {dropped: fx.pinProjection.Dropped},
	}
}

func (fx effectSet) counterRepairs() map[string]func() uint64 {
	return map[string]func() uint64{pbconv.ReactionsCounter: fx.reactionCounter.Repaired}
}
```

`apps/core/reaction_pin_effects_wiring.go`:

```go
package main

import (
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/counter"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/pinproj"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

func (fx *effectSet) wireReactionPinEffects(cfg config.Config, cl *clients, st *mongostore.Store) error {
	reactions, pins := st.Reactions(), st.Pins()
	counts, err := counter.New(st, reactions)
	if err != nil {
		return fmt.Errorf("wire reaction counter: %w", err)
	}
	projector, err := pinproj.New(pins, st)
	if err != nil {
		return fmt.Errorf("wire pin projector: %w", err)
	}
	events := effects.MessageChangedConfig{SubjectRoot: cfg.Stream.SubjectRoot, Delay: cfg.EffectDelay, RoomCache: cfg.EffectRoomCache}
	if fx.reactionCounter, err = effects.NewReactionCounter(
		effects.ReactionCounterDeps{Messages: st, Counter: counts, Rooms: st, JS: cl.effectsJS},
		effects.ReactionCounterConfig{SubjectRoot: cfg.Stream.SubjectRoot, Delay: cfg.ReactionCountDelay, RoomCache: cfg.EffectRoomCache},
	); err != nil {
		return fmt.Errorf("wire reaction_counter effect: %w", err)
	}
	if fx.reactionEvent, err = effects.NewReactionEvent(effects.ReactionEventDeps{Reactions: reactions, Rooms: st, JS: cl.effectsJS}, events); err != nil {
		return fmt.Errorf("wire reaction_event effect: %w", err)
	}
	if fx.pinProjection, err = effects.NewPinProjection(projector); err != nil {
		return fmt.Errorf("wire pin_projection effect: %w", err)
	}
	if fx.pinEvent, err = effects.NewPinEvent(effects.PinEventDeps{Pins: pins, Messages: st, Rooms: st, JS: cl.effectsJS}, events); err != nil {
		return fmt.Errorf("wire pin_event effect: %w", err)
	}
	return nil
}
```

`apps/core/metrics_wiring.go`:
- Trong `type probes struct`, sau `effectCounts map[string]effectCounters` thêm `counterRepairs map[string]func() uint64` (gofmt căn cột).
- Trong `metricSources`, thay `out = append(out, workerSources(p.workers, p.effectCounts)...)` bằng:

```go
	out = append(out, workerSources(p.workers, p.effectCounts)...)
	out = append(out, counterSources(p.counterRepairs)...)
```

- Trong `workerSources`, thay hai hằng help bằng:

```go
	const republishHelp = "Events effect workers sent and JetStream acked; msg_created sends only unmarked events, the other event effects count only ids the stream had not stored."
	const dropHelp = "Work records an effect gave up on (missing room, message, edit fact, reaction or pin fact, or a corrupt document)."
```

- Sau hàm `workerSources` thêm:

```go
func counterSources(repairs map[string]func() uint64) []metrics.Source {
	const help = "Counter summaries the effect workers rewrote because the fast path left them stale."
	out := make([]metrics.Source, 0, len(repairs))
	for _, name := range slices.Sorted(maps.Keys(repairs)) {
		read := repairs[name]
		out = append(out, metrics.Source{Name: "counter_repaired_total", Help: help, Labels: map[string]string{"counter": name}, Read: func() float64 { return float64(read()) }})
	}
	return out
}
```

`apps/core/wiring.go`, trong literal `p := probes{...}` thêm sau `effectCounts: fx.counters(),` dòng `counterRepairs: fx.counterRepairs(),` (gofmt căn lại cột).

`apps/core/metrics_wiring_test.go` (thay cả file):

```go
package main

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/metrics"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/reconcile"
)

var readerMetrics = []string{
	"reconcile_running", "reconcile_terms_total", "reconcile_forwarded_total", "reconcile_dropped_total", "reconcile_history_lost_total",
}

func fakeProbes(withReader bool) probes {
	zero := func() uint64 { return 0 }
	p := probes{
		drops:        func() publish.Drops { return publish.Drops{} },
		router:       func() actor.Stats { return actor.Stats{} },
		cidDegraded:  func() bool { return false },
		markDegraded: func() bool { return false },
		cidDropped:   zero,
		loadShed:     func() int64 { return 0 },
		oplogWindow:  func() float64 { return 0 },
		workers:      func() effects.Stats { return effects.Stats{Processed: 7, Failed: 2, Lag: 3 * time.Second} },
		effectCounts: map[string]effectCounters{
			"msg_created":      {republished: func() uint64 { return 5 }, dropped: zero},
			"room_created":     {republished: zero, dropped: func() uint64 { return 1 }},
			"edit_projection":  {dropped: func() uint64 { return 4 }},
			"reaction_counter": {republished: func() uint64 { return 8 }, dropped: func() uint64 { return 2 }},
			"pin_projection":   {dropped: func() uint64 { return 3 }},
		},
		counterRepairs: map[string]func() uint64{"reactions": func() uint64 { return 6 }},
	}
	if withReader {
		p.reconcile = func() reconcile.Stats { return reconcile.Stats{} }
	}
	return p
}

func sourceNames(sources []metrics.Source) map[string]bool {
	out := map[string]bool{}
	for _, s := range sources {
		out[s.Name] = true
	}
	return out
}

func TestCoreMetricSourcesCoverEveryGuarantee(t *testing.T) {
	everyCore := []string{
		"publish_dropped_total", "ack_marks_dropped_total", "redis_degraded", "cid_settle_dropped_total",
		"cid_pending_elsewhere_total", "room_yields_total", "grpc_load_shed_total", "mongo_oplog_window_seconds",
		"reconcile_lag_seconds", "reconcile_republished_total", "effect_dropped_total", "work_processed_total", "work_failures_total",
		"counter_repaired_total",
	}
	on := sourceNames(metricSources(fakeProbes(true)))
	for _, name := range append(everyCore, readerMetrics...) {
		if !on[name] {
			t.Errorf("no metric source %s", name)
		}
	}
	if _, err := metrics.Handler(metricSources(fakeProbes(true))); err != nil {
		t.Fatalf("Handler: %v", err)
	}
	off := sourceNames(metricSources(fakeProbes(false)))
	for _, name := range readerMetrics {
		if off[name] {
			t.Errorf("reader disabled but %s is exported", name)
		}
	}
	for _, name := range everyCore {
		if !off[name] {
			t.Errorf("%s must be exported on every core, also without the reader", name)
		}
	}
}

func TestEffectMetricsReadTheirEffectByLabel(t *testing.T) {
	got := map[string]float64{}
	for _, s := range metricSources(fakeProbes(false)) {
		key := s.Name
		for _, label := range []string{s.Labels["effect"], s.Labels["counter"]} {
			if label != "" {
				key += "{" + label + "}"
			}
		}
		got[key] = s.Read()
	}
	want := map[string]float64{
		"reconcile_lag_seconds": 3, "work_processed_total": 7, "work_failures_total": 2,
		"reconcile_republished_total{msg_created}": 5, "reconcile_republished_total{room_created}": 0,
		"effect_dropped_total{msg_created}": 0, "effect_dropped_total{room_created}": 1,
		"effect_dropped_total{edit_projection}": 4,
		"reconcile_republished_total{reaction_counter}": 8, "effect_dropped_total{reaction_counter}": 2,
		"effect_dropped_total{pin_projection}": 3,
		"counter_repaired_total{reactions}":    6,
	}
	for key, v := range want {
		if g, ok := got[key]; !ok || g != v {
			t.Errorf("%s = %v (present %v), want %v", key, g, ok, v)
		}
	}
	for _, silent := range []string{"edit_projection", "pin_projection"} {
		if _, ok := got["reconcile_republished_total{"+silent+"}"]; ok {
			t.Errorf("%s never publishes, but reconcile_republished_total is exported for it", silent)
		}
	}
}
```

`apps/core/effects_wiring_test.go`:

```go
package main

import (
	"context"
	"maps"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/counter"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
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

func built[T any](t *testing.T, v T, err error) T {
	t.Helper()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return v
}

func TestEffectSetExportsEveryEffect(t *testing.T) {
	msgs, rooms, edits := memstore.NewMessages(), memstore.NewRooms(), memstore.NewEdits()
	reactions, pins, js := memstore.NewReactions(), memstore.NewPins(), &publishtest.JetStream{}
	events := effects.MessageChangedConfig{SubjectRoot: "evt"}
	fx := effectSet{
		msgCreated: built(t, effects.NewMessageCreated(
			effects.MessageCreatedDeps{Marks: noMarks{}, Messages: msgs, Rooms: rooms, JS: js}, effects.MessageCreatedConfig{SubjectRoot: "evt"})),
		roomCreated:    built(t, effects.NewRoomCreated(effects.RoomCreatedDeps{Rooms: rooms, JS: js}, effects.RoomCreatedConfig{SubjectRoot: "evt"})),
		editProjection: built(t, effects.NewEditProjection(effects.EditProjectionDeps{Edits: edits, Messages: msgs, Purger: edits})),
		msgChanged:     built(t, effects.NewMessageChanged(effects.MessageChangedDeps{Edits: edits, Messages: msgs, Rooms: rooms, JS: js}, events)),
		reactionCounter: built(t, effects.NewReactionCounter(
			effects.ReactionCounterDeps{Messages: msgs, Counter: built(t, counter.New(msgs, reactions)), Rooms: rooms, JS: js},
			effects.ReactionCounterConfig{SubjectRoot: "evt"})),
		reactionEvent: built(t, effects.NewReactionEvent(effects.ReactionEventDeps{Reactions: reactions, Rooms: rooms, JS: js}, events)),
		pinProjection: built(t, effects.NewPinProjection(built(t, pinproj.New(pins, rooms)))),
		pinEvent:      built(t, effects.NewPinEvent(effects.PinEventDeps{Pins: pins, Messages: msgs, Rooms: rooms, JS: js}, events)),
	}
	counters := fx.counters()
	want := []string{
		effects.EditProjectionName, effects.MessageChangedName, effects.MessageCreatedName, effects.PinEventName,
		effects.PinProjectionName, effects.ReactionCounterName, effects.ReactionEventName, effects.RoomCreatedName,
	}
	if got := slices.Sorted(maps.Keys(counters)); !slices.Equal(got, want) {
		t.Fatalf("effects with metrics = %v, want %v", got, want)
	}
	for name, c := range counters {
		silent := name == effects.EditProjectionName || name == effects.PinProjectionName
		if c.dropped == nil || (c.republished == nil) != silent {
			t.Errorf("%s: dropped set %v, republished set %v; want dropped always and republished only when it publishes", name, c.dropped != nil, c.republished != nil)
		}
	}
	if repairs := fx.counterRepairs(); len(repairs) != 1 || repairs[pbconv.ReactionsCounter] == nil {
		t.Errorf("counter repairs = %v, want only %q", repairs, pbconv.ReactionsCounter)
	}
}
```

(`want` đã sắp theo chữ: `edit_projection` < `msg_changed` < `msg_created` < `pin_event` < `pin_projection` < `reaction_counter` < `reaction_event` < `room_created`.)

`deploy/prometheus/alerts.yml`, thêm cuối nhóm (sau `ChatimEffectDropping`):

```yaml
      - alert: ChatimCounterRepairSurge
        expr: sum by (counter) (rate(chatim_core_counter_repaired_total[5m])) > 1
        for: 10m
        labels:
          severity: warning
          guarantee: CT1
        annotations:
          summary: Effect workers keep rewriting counter summaries the fast path left stale; check fast path touch failures and hot targets.
```

**Step 7: Chạy, thấy pass**

```bash
make -s go ARGS="test -race -shuffle=on ./apps/core/internal/effects/... ./apps/core/"
make alerts-check
```

Expected: PASS; `make alerts-check` in `SUCCESS: 16 rules found`. `wc -l apps/core/*.go`: mỗi file < 200 (`effects_wiring.go` ~100, `reaction_pin_effects_wiring.go` ~45, `metrics_wiring.go` ~125, `metrics_wiring_test.go` ~115, `effects_wiring_test.go` ~70, `wiring.go` ~150).

**Step 8: Itest**

```bash
make infra-up && make itest
```

Expected: mọi package `ok` (registry mới, wiring dựng 4 effect trên Mongo/NATS thật; itest có reaction/ghim là Task 16).

**Step 9: INDEXES + commit + push**

`INDEXES.csv`:
- Dòng `apps/core/internal/effects`: purpose nối "; reaction_counter effect (delay REACTION_COUNT_DELAY) for ReactionChanged: groups the batch by message, witnesses = newest change number per user, Find + counter Touch (CAS on rx.v), counts a bump as Repaired (CT1), publishes counts_changed of the current version (nothing at version 0), counts only PubAcks the stream had not stored; a stale witness, contention or store error retries every record of the message; reaction_event effect (delay RECONCILE_DELAY, no ack mark): Reactions.Get, publishes reaction_changed only when the doc change number equals the record (newer doc skips, older doc = ErrStaleRead retry), missing doc or room dropped; pin_projection effect (delay 0) for PinInserted: one pinproj Project per room up to the newest pin version of the batch, missing room dropped, other errors retry every record of the room; pin_event effect (delay RECONCILE_DELAY, no ack mark): Pins.At(room, pv), Find, pbconv.PinChanged (deleted message without text); missing fact, message or room dropped; eventPublisher shares the publish loop, room type cache and counters of the new event effects; groupRecords groups record indexes by key"; key_symbols nối `;ReactionEvent;NewReactionEvent;ReactionEvent.Effect;ReactionEventDeps;ReactionEventName;ReactionCounter;NewReactionCounter;ReactionCounter.Effect;ReactionCounter.Repaired;ReactionCounterDeps;ReactionCounterConfig;ReactionCounterName;DefaultCounterTries;PinProjection;NewPinProjection;PinProjection.Effect;PinProjection.Dropped;PinProjectionName;PinEvent;NewPinEvent;PinEvent.Effect;PinEventDeps;PinEventName;ReactionReader;CounterToucher;PinFacts;PinProjecter`; decisions nối `;D90;D92;D93`.
- Dòng `apps/core`: đoạn registry của `effects_wiring.go` đổi thành "registry MessageInserted -> room_activity, msg_created; RoomInserted -> room_created; EditInserted -> room_activity, edit_projection, msg_changed; ReactionChanged -> room_activity, reaction_counter, reaction_event; PinInserted -> room_activity, pin_projection, pin_event (reaction_pin_effects_wiring.go builds the four new effects with their own counter and pin projector)"; "skip reconcile_republished_total for effects that never publish (edit_projection)" → "(edit_projection, pin_projection)"; nối "; counter_repaired_total{counter} from effectSet.counterRepairs (reactions)"; key_symbols nối `;counterSources;effectSet.counterRepairs;effectSet.wireReactionPinEffects`; tests nối `;unit (every effect exports its metrics)`; decisions nối `;D93`.
- Dòng `deploy/prometheus/alerts.yml`: "15 alert rules" → "16 alert rules"; trong ngoặc, sau "effect dropping" thêm ", counter repair surge"; decisions nối `;D90`.

```bash
make fmt-check && make vet && make lint
python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"
git add apps/core/internal/effects/ apps/core/reaction_pin_effects_wiring.go apps/core/effects_wiring_test.go
git commit -m "feat(effects): count reactions, project pins and republish their events" -- apps/core/internal/effects/ apps/core/effects_wiring.go apps/core/reaction_pin_effects_wiring.go apps/core/effects_wiring_test.go apps/core/metrics_wiring.go apps/core/metrics_wiring_test.go apps/core/wiring.go deploy/prometheus/alerts.yml INDEXES.csv
git push origin feat/m2b
```

Expected: `{7}`; `git show --stat HEAD` chỉ có các file trên; push thành công.

Task rủi ro: một reviewer (thứ tự và delay trong registry, gom theo tin/room, witness = n lớn nhất mỗi user, `Republished` chỉ đếm PubAck không trùng, `Repaired` chỉ khi bump, lỗi lan cho cả nhóm, `Dropped` theo record, metric và luật 16; tối đa `-count=3` trên `effects`).

---

### Task 14: `/app resync` quét `reactions` và `pin_actions`

Resync (D81) đã quét timeline chính và `message_edits` (M2b.2). Reaction và ghim không đổi `last_seq`, nên phải quét riêng theo `{r, ts}` như fact sửa: sau `message_edits`, mỗi room quét `Reactions.Between(room, from, to, store.MaxReactionScan)` rồi `Pins.Between(room, from, to, store.MaxPinScan)`. Phân trang y hệt `edits`: dời `from` tới `ts` cuối trang (bao gồm), bỏ trùng mép trang theo id record của đúng `ts` cuối trang; một thời điểm đầy cả trang → `ErrReactionPageFull` / `ErrPinPageFull`.

Ba lần phân trang giống nhau nên task này tách vòng phân trang của `edits.go` thành một helper generic `scanByTime` (file mới `time_scan.go`); `edits.go` chỉ còn khai báo nguồn và cách dựng record. Hành vi của `edits` giữ nguyên (thông điệp lỗi `edits of room %d from %s`, `ErrEditPageFull`), test cũ của `edits` là lưới an toàn.

Record:
- Reaction: `{ReactionChanged, Room, Thread, Seq, Version: r.N, User: r.User, CommittedAt: r.At}`. Chỉ có **doc hiện tại** (lớp tập: doc hiện tại thắng), kể cả tombstone `e: ""`; worker `reaction_event` phát đúng doc đó (`n == rec.n`) và `reaction_counter` đếm lại.
- Ghim: `{PinInserted, Room, Seq: a.PV, CommittedAt: a.At}`; worker `pin_projection` fold + CAS rồi `pin_event` phát.

Thứ tự trong một room: room record → tin (ngược) → fact sửa → reaction → ghim. Worker không dựa vào thứ tự này. Giới hạn (giống tin và fact sửa từ M2b.1/M2b.2): resync chọn room theo `ab`, mà `ab` chỉ do worker `room_activity` ghi; trong khoảng mất feed không record nào tới worker, nên room **chỉ** có reaction/ghim (không có tin hay fact sửa nào sau khoảng đó nâng `ab`) không được chọn: phải chạy `-room`. Task 17 ghi giới hạn này vào D91 và đoạn resync của thiết kế §8.3.

**GAP-1 đã chốt (controller):** `*mongostore.Store` không cài thẳng `store.Reactions`/`store.Pins` (trùng tên `Append/At/Between/Get` của `Edits`/`Rooms`); Task 5 thêm kiểu con `mongostore.Reactions`/`mongostore.Pins` qua accessor `st.Reactions()`/`st.Pins()`. Part C dùng accessor ở `resync_command.go` (`Reactions: st.Reactions(), Pins: st.Pins()`) và helper itest `itReactions`, `itPins` trong `apps/core/it_reactions_pins_test.go`. `SetReactions`, `PinState`, `ApplyPins` vẫn gọi trên `st`.

Trước khi bắt đầu, kiểm tên của part A (chỉ đọc):

```bash
grep -n "func NewReactions\|func NewPins\|func (r \*Reactions) Between\|func (p \*Pins) Between" apps/core/internal/store/memstore/*.go
grep -n "MaxReactionScan\|MaxPinScan\|ReactionChanged\|PinInserted" apps/core/internal/store/*.go
grep -n "User \+string\|func (r Record) ID" apps/core/internal/work/record.go
grep -n "func (s \*Store) Reactions()\|func (s \*Store) Pins()\|func (s \*Store) PinState" apps/core/internal/store/mongostore/*.go
```

Expected: `memstore.NewReactions() *Reactions`, `memstore.NewPins() *Pins` có `Set`/`Remove`/`Between` và `Append`/`Between`; hằng `store.MaxReactionScan`, `store.MaxPinScan`, kind `store.ReactionChanged`, `store.PinInserted`; `work.Record.User`; dòng cuối in hai accessor của GAP-1 và `PinState`. Tên khác thì chỉ đổi tên trong snippet dưới đây và ghi vào báo cáo; thiếu hẳn port thì dừng và báo cáo.

Chạy `git diff --quiet -- INDEXES.csv` trước khi sửa; exit khác 0 (có thay đổi chưa commit của người khác) → dừng, hỏi controller.

**Files:**
- Create: `apps/core/internal/resync/time_scan.go`
- Create: `apps/core/internal/resync/reactions.go`
- Create: `apps/core/internal/resync/pins.go`
- Create: `apps/core/internal/resync/reactions_pins_test.go`
- Modify: `apps/core/internal/resync/edits.go` (viết lại trên `scanByTime`)
- Modify: `apps/core/internal/resync/edits_test.go` (chuỗi `Report.String()`)
- Modify: `apps/core/internal/resync/scan.go` (`Deps`, `Report`, `room`)
- Modify: `apps/core/internal/resync/scan_test.go` (`world` có `reactions`, `pins`)
- Create: `apps/core/it_reactions_pins_test.go` (helper GAP-1)
- Modify: `apps/core/resync_command.go`
- Modify: `apps/core/resync_integration_test.go` (diễn tập có thêm một reaction và một ghim)
- Modify: `INDEXES.csv`

**Step 1: Test**

`apps/core/internal/resync/scan_test.go`, ba chỗ:

```go
type world struct {
	rooms *memstore.Rooms
	msgs  *memstore.Messages
	edits *memstore.Edits
}
```

thành

```go
type world struct {
	rooms     *memstore.Rooms
	msgs      *memstore.Messages
	edits     *memstore.Edits
	reactions *memstore.Reactions
	pins      *memstore.Pins
}
```

`	w := world{rooms: memstore.NewRooms(), msgs: memstore.NewMessages(), edits: memstore.NewEdits()}` thành:

```go
	w := world{
		rooms: memstore.NewRooms(), msgs: memstore.NewMessages(), edits: memstore.NewEdits(),
		reactions: memstore.NewReactions(), pins: memstore.NewPins(),
	}
```

`	return resync.Deps{Rooms: w.rooms, Pages: w.msgs, Edits: w.edits, Pub: pub}` thành `	return resync.Deps{Rooms: w.rooms, Pages: w.msgs, Edits: w.edits, Reactions: w.reactions, Pins: w.pins, Pub: pub}`.

Các test cũ so `Report{...}` giữ nguyên (không có reaction/ghim nên hai trường mới bằng 0). `wc -l scan_test.go` → ~187.

`apps/core/internal/resync/edits_test.go`, một chỗ (chuỗi in ra có thêm hai trường):

`	if rep.String() != "resync rooms=2 room_records=1 message_records=61 edit_records=1 dry_run=false" {` thành `	if rep.String() != "resync rooms=2 room_records=1 message_records=61 edit_records=1 reaction_records=0 pin_records=0 dry_run=false" {`

`apps/core/internal/resync/reactions_pins_test.go`:

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

func (w world) react(t *testing.T, room, seq uint64, user, emoji string, at time.Time) string {
	t.Helper()
	r, changed, err := w.reactions.Set(t.Context(), domain.Reaction{Room: room, Seq: seq, Tenant: "acme", User: user, Emoji: emoji, At: at})
	if err != nil || !changed {
		t.Fatalf("Set(%d/%d %s %q) = %+v, %v, %v; want a change", room, seq, user, emoji, r, changed, err)
	}
	return work.Record{Kind: store.ReactionChanged, Room: room, Seq: seq, Version: r.N, User: user}.ID()
}

func (w world) unreact(t *testing.T, room, seq uint64, user string, at time.Time) string {
	t.Helper()
	r, changed, err := w.reactions.Remove(t.Context(), store.MsgKey{Room: room, Seq: seq}, user, at)
	if err != nil || !changed {
		t.Fatalf("Remove(%d/%d %s) = %+v, %v, %v; want a change", room, seq, user, r, changed, err)
	}
	return work.Record{Kind: store.ReactionChanged, Room: room, Seq: seq, Version: r.N, User: user}.ID()
}

func (w world) pin(t *testing.T, room, pv, seq uint64, at time.Time) string {
	t.Helper()
	op := domain.PinOpPin
	if pv%2 == 0 {
		op = domain.PinOpUnpin
	}
	a := domain.PinAction{Room: room, PV: pv, Tenant: "acme", Op: op, Seq: seq, By: "alice", At: at}
	if err := w.pins.Append(t.Context(), a); err != nil {
		t.Fatalf("Append(%d p%d): %v", room, pv, err)
	}
	return work.Record{Kind: store.PinInserted, Room: room, Seq: pv}.ID()
}

func TestResyncPublishesReactionsAndPinsOfTheLostRangeAfterTheEdits(t *testing.T) {
	w := newWorld(t)
	edit := w.edit(t, busyRoom, 40, 1, lostFrom.Add(5*time.Minute))
	w.react(t, busyRoom, 40, "bob", "👍", lostFrom.Add(5*time.Minute))
	changed := w.react(t, busyRoom, 40, "bob", "🎉", lostFrom.Add(6*time.Minute))
	w.react(t, busyRoom, 41, "carol", "👍", lostFrom.Add(-time.Minute))
	w.react(t, busyRoom, 42, "dave", "👍", lostFrom.Add(7*time.Minute))
	removed := w.unreact(t, busyRoom, 42, "dave", lostFrom.Add(8*time.Minute))
	pinned := w.pin(t, busyRoom, 1, 40, lostFrom.Add(9*time.Minute))
	w.pin(t, busyRoom, 2, 40, lostTo.Add(time.Minute))
	pub := &publishSpy{}
	rep, err := resync.Run(t.Context(), w.deps(pub), target, resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Rate: resync.MaxRate})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := resync.Report{Rooms: 2, RoomRecords: 1, MessageRecords: 61, EditRecords: 1, ReactionRecords: 2, PinRecords: 1}
	if rep != want {
		t.Fatalf("report = %+v, want %+v", rep, want)
	}
	got := pub.published()
	tail := []string{edit, changed, removed, pinned, work.Record{Kind: store.RoomInserted, Room: newRoom}.ID()}
	if len(got) != 66 || !slices.Equal(got[61:], tail) {
		t.Fatalf("published %d ids ending %v, want 61 messages then %v", len(got), got[max(0, len(got)-5):], tail)
	}
	if rep.String() != "resync rooms=2 room_records=1 message_records=61 edit_records=1 reaction_records=2 pin_records=1 dry_run=false" {
		t.Fatalf("report line = %q", rep.String())
	}
}

func TestResyncPagesReactionsAndPinsByTimeWithoutRepeatingThePageEdge(t *testing.T) {
	w := newWorld(t)
	for i := range uint32(1200) {
		at := lostFrom.Add(time.Duration((i+1)/3) * time.Millisecond)
		w.react(t, staleRoom, 1, "u"+strconv.FormatUint(uint64(i+1), 10), "👍", at)
		w.pin(t, staleRoom, uint64(i+1), 1, at)
	}
	opts := resync.Options{From: lostFrom, To: lostTo, Room: staleRoom, Rate: 1, DryRun: true}
	rep, err := resync.Run(t.Context(), w.deps(&publishSpy{}), target, opts)
	if err != nil || rep != (resync.Report{Rooms: 1, ReactionRecords: 1200, PinRecords: 1200, DryRun: true}) {
		t.Fatalf("Run = %+v, %v; want 1200 reaction and 1200 pin records, each once", rep, err)
	}
}

func TestResyncStopsWhenOneInstantHoldsMoreReactionsOrPinsThanAPage(t *testing.T) {
	at := lostFrom.Add(time.Minute)
	opts := resync.Options{From: lostFrom, To: lostTo, Room: staleRoom, Rate: 1, DryRun: true}
	w := newWorld(t)
	for i := range 1001 {
		w.react(t, staleRoom, 1, "u"+strconv.Itoa(i+1), "👍", at)
	}
	rep, err := resync.Run(t.Context(), w.deps(&publishSpy{}), target, opts)
	if !errors.Is(err, resync.ErrReactionPageFull) || rep.ReactionRecords != 1000 {
		t.Fatalf("Run = %+v, %v; want ErrReactionPageFull after one full page", rep, err)
	}
	w = newWorld(t)
	for pv := range uint64(1001) {
		w.pin(t, staleRoom, pv+1, 1, at)
	}
	rep, err = resync.Run(t.Context(), w.deps(&publishSpy{}), target, opts)
	if !errors.Is(err, resync.ErrPinPageFull) || rep.PinRecords != 1000 {
		t.Fatalf("Run = %+v, %v; want ErrPinPageFull after one full page", rep, err)
	}
}
```

Vòng lặp dùng `range uint32(...)` (như test phân trang của `edits`) để không đổi `int` → `uint*` (gosec G115 chạy cả trên test).

Dữ liệu:
- Test đầu: bob đổi 👍 → 🎉 nên chỉ còn doc `n2` (ts +6m); carol ngoài khoảng; dave đặt rồi gỡ nên tombstone `n2` (ts +8m) vẫn được phát; ghim pv1 trong khoảng, pv2 (bỏ ghim) sau `lostTo`. Thứ tự: 61 tin → fact sửa → reaction theo `ts` (bob rồi dave) → ghim → room record của `newRoom` (room sau trong `ActiveRooms`).
- Test phân trang: 1200 doc reaction của 1200 user khác nhau và 1200 fact ghim, mỗi ms 3 cái, như test phân trang của `edits` (không bỏ trùng thì đếm 1202).
- `memstore.Pins.Append` không kiểm pv dày hay fold (đó là việc của `mutate`), nên pin/unpin xen kẽ trên cùng seq là dữ liệu hợp lệ cho resync.

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/resync/..."`
Expected: FAIL biên dịch: `unknown field Reactions in struct literal of type resync.Deps`, `unknown field ReactionRecords in struct literal of type resync.Report`, `undefined: resync.ErrReactionPageFull`, `undefined: resync.ErrPinPageFull`.

**Step 3: Code**

`apps/core/internal/resync/time_scan.go`:

```go
package resync

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

type timeScan[T any] struct {
	name    string
	limit   int
	full    error
	between func(ctx context.Context, room uint64, from, to time.Time, limit int) ([]T, error)
	record  func(T) work.Record
	counted *int
}

func scanByTime[T any](ctx context.Context, s *scanner, room uint64, ts timeScan[T]) error {
	from, edge := s.opts.From, map[string]bool{}
	for {
		page, err := ts.between(ctx, room, from, s.opts.To, ts.limit)
		if err != nil {
			return fmt.Errorf("%s of room %d from %s: %w", ts.name, room, from.Format(time.RFC3339Nano), err)
		}
		next, err := emitTimePage(ctx, s, ts, page, edge)
		if err != nil || len(page) < ts.limit {
			return err
		}
		last := ts.record(page[len(page)-1]).CommittedAt
		if !last.After(from) {
			return fmt.Errorf("%w: room %d at %s", ts.full, room, last.Format(time.RFC3339Nano))
		}
		from, edge = last, next
	}
}

func emitTimePage[T any](ctx context.Context, s *scanner, ts timeScan[T], page []T, edge map[string]bool) (map[string]bool, error) {
	next := map[string]bool{}
	if len(page) == 0 {
		return next, nil
	}
	end := ts.record(page[len(page)-1]).CommittedAt
	for _, item := range page {
		rec := ts.record(item)
		id := rec.ID()
		if rec.CommittedAt.Equal(end) {
			next[id] = true
		}
		if edge[id] {
			continue
		}
		if err := s.emit(ctx, rec); err != nil {
			return nil, err
		}
		*ts.counted++
	}
	return next, nil
}
```

`apps/core/internal/resync/edits.go` (viết lại cả file; `editPage`, `emitEdits` bỏ, không nơi nào khác dùng):

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

var ErrEditPageFull = errors.New("resync: one instant holds more edits than an edit page")

type Edits interface {
	Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Edit, error)
}

func (s *scanner) edits(ctx context.Context, room uint64) error {
	return scanByTime(ctx, s, room, timeScan[domain.Edit]{
		name:    "edits",
		limit:   store.MaxEditScan,
		full:    ErrEditPageFull,
		between: s.deps.Edits.Between,
		record:  editRecord,
		counted: &s.rep.EditRecords,
	})
}

func editRecord(e domain.Edit) work.Record {
	return work.Record{Kind: store.EditInserted, Room: e.Room, Thread: e.Thread, Seq: e.Seq, Version: e.Version, CommittedAt: e.At}
}
```

`apps/core/internal/resync/reactions.go`:

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

var ErrReactionPageFull = errors.New("resync: one instant holds more reactions than a reaction page")

type Reactions interface {
	Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.Reaction, error)
}

func (s *scanner) reactions(ctx context.Context, room uint64) error {
	return scanByTime(ctx, s, room, timeScan[domain.Reaction]{
		name:    "reactions",
		limit:   store.MaxReactionScan,
		full:    ErrReactionPageFull,
		between: s.deps.Reactions.Between,
		record:  reactionRecord,
		counted: &s.rep.ReactionRecords,
	})
}

func reactionRecord(r domain.Reaction) work.Record {
	return work.Record{Kind: store.ReactionChanged, Room: r.Room, Thread: r.Thread, Seq: r.Seq, Version: r.N, User: r.User, CommittedAt: r.At}
}
```

`apps/core/internal/resync/pins.go`:

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

var ErrPinPageFull = errors.New("resync: one instant holds more pin actions than a pin page")

type Pins interface {
	Between(ctx context.Context, room uint64, from, to time.Time, limit int) ([]domain.PinAction, error)
}

func (s *scanner) pins(ctx context.Context, room uint64) error {
	return scanByTime(ctx, s, room, timeScan[domain.PinAction]{
		name:    "pin actions",
		limit:   store.MaxPinScan,
		full:    ErrPinPageFull,
		between: s.deps.Pins.Between,
		record:  pinRecord,
		counted: &s.rep.PinRecords,
	})
}

func pinRecord(a domain.PinAction) work.Record {
	return work.Record{Kind: store.PinInserted, Room: a.Room, Seq: a.PV, CommittedAt: a.At}
}
```

`apps/core/internal/resync/scan.go`, ba chỗ:

```go
type Deps struct {
	Rooms Rooms
	Pages Pages
	Edits Edits
	Pub   Publisher
}
```

thành

```go
type Deps struct {
	Rooms     Rooms
	Pages     Pages
	Edits     Edits
	Reactions Reactions
	Pins      Pins
	Pub       Publisher
}
```

```go
type Report struct {
	Rooms, RoomRecords, MessageRecords, EditRecords int
	DryRun                                          bool
}

func (r Report) String() string {
	return fmt.Sprintf("resync rooms=%d room_records=%d message_records=%d edit_records=%d dry_run=%t", r.Rooms, r.RoomRecords, r.MessageRecords, r.EditRecords, r.DryRun)
}
```

thành

```go
type Report struct {
	Rooms, RoomRecords, MessageRecords, EditRecords, ReactionRecords, PinRecords int
	DryRun                                                                       bool
}

func (r Report) String() string {
	return fmt.Sprintf("resync rooms=%d room_records=%d message_records=%d edit_records=%d reaction_records=%d pin_records=%d dry_run=%t",
		r.Rooms, r.RoomRecords, r.MessageRecords, r.EditRecords, r.ReactionRecords, r.PinRecords, r.DryRun)
}
```

Trong `func (s *scanner) room`, bốn dòng cuối

```go
	if err := s.timeline(ctx, r.ID); err != nil {
		return err
	}
	return s.edits(ctx, r.ID)
```

thành

```go
	for _, scan := range []func(context.Context, uint64) error{s.timeline, s.edits, s.reactions, s.pins} {
		if err := scan(ctx, r.ID); err != nil {
			return err
		}
	}
	return nil
```

(gofmt căn cột `Report`; nếu `make fmt-check` báo thì chạy `make -s go ARGS="fmt ./apps/core/internal/resync/"`.)

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/resync/..."`
Expected: PASS, gồm ba test của `edits` (hành vi giữ nguyên sau khi chuyển sang `scanByTime`) và ba test mới. `wc -l apps/core/internal/resync/*.go` → mỗi file < 200 (scan.go ~164, scan_test.go ~187, time_scan.go ~56, edits.go ~33).

**Step 5: Wiring + diễn tập**

`apps/core/it_reactions_pins_test.go` (điểm GAP-1 cho itest; Task 16 thêm helper vào file này):

```go
package main

import (
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

func itReactions(st *mongostore.Store) store.Reactions { return st.Reactions() }

func itPins(st *mongostore.Store) store.Pins { return st.Pins() }
```

`apps/core/resync_command.go`: `	rep, err := resync.Run(ctx, resync.Deps{Rooms: st, Pages: st, Edits: st, Pub: c.js}, target, opts)` thành:

```go
	deps := resync.Deps{Rooms: st, Pages: st, Edits: st, Reactions: st.Reactions(), Pins: st.Pins(), Pub: c.js}
	rep, err := resync.Run(ctx, deps, target, opts)
```

`apps/core/resync_integration_test.go`:
- Import thêm `"slices"`.
- Ngay sau dòng `	want = append(want, pbconv.MessageChangeEventID(room, 0, 1, 1))` và trước `	assertNoLiveIDs(...)`, chèn:

```go
	marked := time.Now().UTC().Truncate(time.Millisecond)
	reaction := domain.Reaction{Room: room, Seq: 2, Tenant: itTenant, User: "migrator", Emoji: "👍", At: marked}
	if _, _, err := itReactions(st).Set(t.Context(), reaction); err != nil {
		t.Fatalf("set a reaction the reader missed: %v", err)
	}
	pin := domain.PinAction{Room: room, PV: 1, Tenant: itTenant, Op: domain.PinOpPin, Seq: 3, By: "migrator", At: marked}
	if err := itPins(st).Append(t.Context(), pin); err != nil {
		t.Fatalf("append a pin the reader missed: %v", err)
	}
	want = append(want, pbconv.ReactionEventID(room, 0, 2, "migrator", 1), pbconv.ReactionCountsEventID(room, 0, 2, 1), pbconv.PinEventID(room, 1))
```

- Thay

```go
	if got := strings.TrimSpace(out.String()); got != "resync rooms=1 room_records=1 message_records=3 edit_records=1 dry_run=false" {
		t.Fatalf("resync output = %q, want one room, its room record, three message records and one edit record", got)
	}
```

bằng

```go
	if got := strings.TrimSpace(out.String()); got != "resync rooms=1 room_records=1 message_records=3 edit_records=1 reaction_records=1 pin_records=1 dry_run=false" {
		t.Fatalf("resync output = %q, want one room, its room record, three message, one edit, one reaction and one pin record", got)
	}
```

- Cuối hàm (sau khối kiểm `seq 1 after resync`), thêm:

```go
	counted, err := st.Find(t.Context(), room, []store.MsgKey{{Room: room, Seq: 2}})
	if err != nil || len(counted) != 1 || counted[0].Reactions.Version != 1 ||
		!slices.Equal(counted[0].Reactions.Counts, []domain.ReactionCount{{Emoji: "👍", Count: 1}}) {
		t.Fatalf("seq 2 after resync = %+v, %v; want the missed reaction counted at version 1", counted, err)
	}
	pins, err := st.PinState(t.Context(), room)
	if err != nil || pins.Version != 1 || len(pins.Pins) != 1 || pins.Pins[0].Seq != 3 || pins.Pins[0].PV != 1 {
		t.Fatalf("pins after resync = %+v, %v; want seq 3 pinned at version 1", pins, err)
	}
```

Reader tắt (`RECONCILE_ENABLED=false`) nên reaction và fact ghim ghi thẳng vào Mongo không có event, không có `rx`, không có projection. Resync đẩy record `x:`/`p:`; worker chạy `room_activity` → `reaction_counter` (`REACTION_COUNT_DELAY` 1s: witness `migrator n1`, đếm `👍=1`, CAS `rx.v` 0 → 1, phát `counts_changed` v1) → `reaction_event` (2s, `n == 1`, phát `reaction_changed` n1); và `room_activity` → `pin_projection` (0: fold pv1 → `rooms.pins`) → `pin_event` (2s, `msg_pinned` p1). Khi cả sáu id đã tới live thì `rx` và `rooms.pins` đã ghi xong (effect trước của cùng record chạy trước). File ~93 dòng.

**Step 6: Chạy**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/ ./apps/core/internal/resync/..."`
Expected: PASS (itest skip).

Run: `make infra-up && make itest`
Expected: mọi package `ok`, gồm `TestRealInfraResyncDrillRepublishesWritesTheReaderMissed` (log `live events [... <room>-0-2-migrator-n1 <room>-0-2-reactions-v1 <room>-p1] arrived …`).

**Step 7: INDEXES.csv + commit**

- Thay cả dòng bắt đầu bằng `apps/core/internal/resync,` bằng:

```csv
apps/core/internal/resync,package,"Manual resync of a lost change feed range (/app resync): flags -from/-to (RFC3339), -tenant, -room, -rate (default 500/s, max 10000), -dry-run; walks rooms by store.ActiveRooms (activity bucket since -from or created in range, 500 per page) or one room; per room emits RoomInserted if created in range, MessageInserted for main-timeline messages created in range (backward Page scan, stops before -from), then by {r, ts} in range (one generic scanByTime: 1000 per page, paged by ts, the page edge deduped by record id; one instant filling a page is an error): EditInserted for message_edits facts (ErrEditPageFull), ReactionChanged for the current reactions docs including tombstones (Version = n, User; ErrReactionPageFull), PinInserted for pin_actions facts (Seq = pv; ErrPinPageFull); publishes work records synchronously at -rate; prints counts",ParseArgs;Options;Options.Validate;Run;Deps;Target;Report;Rooms;Pages;Edits;Reactions;Pins;Publisher;ErrUsage;ErrEditPageFull;ErrReactionPageFull;ErrPinPageFull;DefaultRate;MaxRate,apps/core,unit (memstore + fake publisher; synctest pacing);itest drill (apps/core),D69;D70;D81;D84;D91
```

- Dòng `apps/core`: thay chuỗi `itest (resync drill republishes messages and an edit the reader missed)` bằng `itest (resync drill republishes messages, an edit, a reaction and a pin the reader missed)`.

Kiểm: `python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"` → Expected: `{7}`.

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/resync/time_scan.go apps/core/internal/resync/reactions.go apps/core/internal/resync/pins.go apps/core/internal/resync/reactions_pins_test.go apps/core/it_reactions_pins_test.go
git commit -m "feat(core): resync replays reactions and pin_actions of the lost range" -- apps/core/internal/resync/ apps/core/it_reactions_pins_test.go apps/core/resync_command.go apps/core/resync_integration_test.go INDEXES.csv
git show --stat HEAD
```

Expected: `git show --stat HEAD` chỉ liệt kê 12 file của task.

---

### Task 15: Route client + corecli + e2e reaction và ghim

Tool cần gọi 3 RPC mới theo slot của room, và e2e cần chứng minh trên cụm hai core: react một tin rồi đổi emoji (response có `change` và số đếm đúng, lặp lại là no-op), ghim một tin (response có `pin_version` và danh sách ghim, lặp lại là no-op), tin đã xoá không nhận reaction/ghim mới, lịch sử trả `reactions` đúng, live có `reaction_changed`, `counts_changed`, `msg_pinned` đúng id.

Chính sách retry: cả 3 lệnh theo room (`inRoom`) với `retryIdempotent` như `EditMessage`. Lệnh là trạng thái mong muốn (D89, D92): gửi lại sau khi lần đầu đã ghi là no-op và trả trạng thái hiện tại. Retry trễ của `ReactMessage` có thể đưa reaction về trạng thái cũ (không cid); chấp nhận cho lớp tập (D89).

Kiểm tra live chỉ **bắt buộc** id cuối cùng: `reaction_changed` của `change` cuối, `counts_changed` của `version` cuối, `msg_pinned` của pv. Worker chỉ phát doc hiện tại (`reaction_event`: `doc.n > rec.n` thì bỏ) và `counts_changed` của `v` hiện tại, nên event trung gian (`n1`, `v1`) chỉ đến từ fast path, không có đường bù; checker nhận chúng nếu có nhưng không đòi. Mọi event reaction/ghim khác của room phải đúng room, đúng seq đã react/ghim và đúng user.

Không có test "thấy fail" cho `tools/corecli` (binary, không unit test); `tools/internal/route` và `tools/corecli/internal/e2e` theo TDD. `fakeCore` nhúng `chatimv1.CoreServiceClient` (Task 3 không sửa), method tường minh ở file fake mới thắng method nhúng.

Giả định proto/pbconv (Task 3): getter Go `ReactMessageResponse.GetChange/GetReactions`, `ReactionSummary.GetCounts/GetVersion`, `ReactionCount.GetEmoji/GetCount`, `PinMessageResponse`/`UnpinMessageResponse` `.GetPinVersion/GetPins`, `Pin.GetThreadRoot/GetSeq/GetBy/GetPinnedAt/GetPinVersion`, oneof `Event_ReactionChanged`, `Event_CountsChanged`, `Event_MessagePinned`, `Event_MessageUnpinned`; envelope của bốn event mới có `room_id` và `seq` (= seq của tin) như mọi event hiện có. Khác thì chỉ đổi tên, ghi vào báo cáo.

Kiểm `git diff --quiet -- INDEXES.csv` trước khi sửa (như Task 14).

**Files:**
- Create: `tools/internal/route/reactions_pins.go`
- Create: `tools/internal/route/fakes_reactions_pins_test.go`
- Modify: `tools/internal/route/changes_test.go` (thêm 3 lệnh vào `changeCalls`, `changeReplies`)
- Create: `tools/corecli/internal/e2e/marks.go`, `tools/corecli/internal/e2e/marks_check.go`, `tools/corecli/internal/e2e/marks_test.go`
- Modify: `tools/corecli/internal/e2e/events.go` (`Event.User`, `IsCreated`, `EventOf` gọi `markOf`), `check.go` (`CheckEvents` bỏ qua mọi event không phải tin mới), `state.go`, `state_test.go`
- Create: `tools/corecli/cmd_react_pin.go`, `tools/corecli/e2e_react_pin.go`
- Modify: `tools/corecli/main.go`, `tools/corecli/cmd_e2e.go`, `tools/corecli/e2e_check.go`
- Modify: `scripts/e2e.sh`
- Modify: `INDEXES.csv`

**Step 1: Test route**

`tools/internal/route/fakes_reactions_pins_test.go`:

```go
package route_test

import (
	"context"

	"google.golang.org/grpc"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (f *fakeCore) ReactMessage(ctx context.Context, in *chatimv1.ReactMessageRequest, _ ...grpc.CallOption) (*chatimv1.ReactMessageResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	counts := []*chatimv1.ReactionCount{{Emoji: in.GetEmoji(), Count: 1}}
	return &chatimv1.ReactMessageResponse{Change: 1, Reactions: &chatimv1.ReactionSummary{Counts: counts, Version: 1}}, nil
}

func (f *fakeCore) PinMessage(ctx context.Context, in *chatimv1.PinMessageRequest, _ ...grpc.CallOption) (*chatimv1.PinMessageResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.PinMessageResponse{PinVersion: 1, Pins: []*chatimv1.Pin{{Seq: in.GetSeq(), PinVersion: 1}}}, nil
}

func (f *fakeCore) UnpinMessage(ctx context.Context, _ *chatimv1.UnpinMessageRequest, _ ...grpc.CallOption) (*chatimv1.UnpinMessageResponse, error) {
	if err := f.next(ctx); err != nil {
		return nil, err
	}
	return &chatimv1.UnpinMessageResponse{PinVersion: 2}, nil
}
```

`tools/internal/route/changes_test.go`, hai chỗ (hai test hiện có lặp qua mọi phần tử của map, nên tự phủ ba lệnh mới: định tuyến theo room ở mọi lần thử, retry khi attempt timeout, room id sai bị từ chối tại chỗ):
- Trong `var changeCalls = map[string]changeCall{`, thêm sau phần tử `"edits": …,`:

```go
	"react": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.ReactMessage(ctx, &chatimv1.ReactMessageRequest{RoomId: room, Seq: 3, Emoji: "👍"}))
	},
	"pin": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.PinMessage(ctx, &chatimv1.PinMessageRequest{RoomId: room, Seq: 3}))
	},
	"unpin": func(ctx context.Context, c *route.Client, room string) (proto.Message, route.Stats, error) {
		return reply(c.UnpinMessage(ctx, &chatimv1.UnpinMessageRequest{RoomId: room, Seq: 3}))
	},
```

- Trong `var changeReplies = map[string]proto.Message{`, thêm sau phần tử `"edits": …,`:

```go
	"react": &chatimv1.ReactMessageResponse{Change: 1, Reactions: &chatimv1.ReactionSummary{Counts: []*chatimv1.ReactionCount{{Emoji: "👍", Count: 1}}, Version: 1}},
	"pin":   &chatimv1.PinMessageResponse{PinVersion: 1, Pins: []*chatimv1.Pin{{Seq: 3, PinVersion: 1}}},
	"unpin": &chatimv1.UnpinMessageResponse{PinVersion: 2},
```

(gofmt căn lại cột của map `changeReplies`.)

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./tools/internal/route/..."`
Expected: FAIL biên dịch: `c.ReactMessage undefined (type *route.Client has no field or method ReactMessage)` (và `PinMessage`, `UnpinMessage`).

**Step 3: Code route**

`tools/internal/route/reactions_pins.go`:

```go
package route

import (
	"context"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func (c *Client) ReactMessage(ctx context.Context, req *chatimv1.ReactMessageRequest) (*chatimv1.ReactMessageResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.ReactMessageResponse, error) {
		return api.ReactMessage(ctx, req)
	})
}

func (c *Client) PinMessage(ctx context.Context, req *chatimv1.PinMessageRequest) (*chatimv1.PinMessageResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.PinMessageResponse, error) {
		return api.PinMessage(ctx, req)
	})
}

func (c *Client) UnpinMessage(ctx context.Context, req *chatimv1.UnpinMessageRequest) (*chatimv1.UnpinMessageResponse, Stats, error) {
	return inRoom(ctx, c, req.GetRoomId(), func(ctx context.Context, api chatimv1.CoreServiceClient) (*chatimv1.UnpinMessageResponse, error) {
		return api.UnpinMessage(ctx, req)
	})
}
```

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./tools/internal/route/..."`
Expected: PASS (`TestChangeCallsRouteByRoomAndRetryAttemptTimeouts` với 8 lệnh, 16 lần hỏi locator; `TestChangeCallsKeepConflictsAndBadRoomIDsLocal`). `wc -l tools/internal/route/changes_test.go` → ~116.

**Step 5: Commit route**

INDEXES.csv: thay cả dòng bắt đầu bằng `tools/internal/route,` bằng:

```csv
tools/internal/route,package,"Slot-routed gRPC client for tools: Resolver + one conn per address; retries Unavailable with jitter and keeps the same cid; room-routed calls (SendMessage, GetHistory, EditMessage, DeleteMessage, HideMessage, ClearHistory, GetEditHistory, ReactMessage, PinMessage, UnpinMessage) also retry attempt timeouts, ResourceExhausted and Aborted because they are idempotent (cid, base_version, desired state, upsert, $max or a read); Open/Session bundles state Redis (with RedisPassword) + resolver + client",Open;Session;SessionConfig;Client;New;Policy;DialInsecure;WithCaller;Locator;Dialer;Client.EditMessage;Client.DeleteMessage;Client.HideMessage;Client.ClearHistory;Client.GetEditHistory;Client.ReactMessage;Client.PinMessage;Client.UnpinMessage,tools/corecli;tools/poc/corebench,unit;synctest;goleak,D43;D63;D89;D92
```

Kiểm 7 cột như Task 14.

```bash
make fmt-check && make vet && make lint
git add tools/internal/route/reactions_pins.go tools/internal/route/fakes_reactions_pins_test.go
git commit -m "feat(route): route react, pin and unpin by room" -- tools/internal/route/ INDEXES.csv
```

**Step 6: Test package e2e**

`tools/corecli/internal/e2e/state_test.go`, một chỗ: `	want := e2e.State{Tenant: "e2e", User: "alice", Room: room, Owner: "core-1", Acks: acks(3), Changes: []e2e.Change{e2e.EditOf(1), e2e.DeleteOf(2)}}` thành:

```go
	want := e2e.State{
		Tenant: "e2e", User: "alice", Room: room, Owner: "core-1", Acks: acks(3),
		Changes:   []e2e.Change{e2e.EditOf(1), e2e.DeleteOf(2)},
		Reactions: []e2e.Reaction{{Seq: 3, Emoji: "🎉", Change: 2, Version: 2}},
		Pins:      []e2e.Pin{{Seq: 4, Version: 1}},
	}
```

`tools/corecli/internal/e2e/marks_test.go`:

```go
package e2e_test

import (
	"slices"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

const reactor = "e2e-user"

func summary(version uint64, emoji string) *chatimv1.ReactionSummary {
	return &chatimv1.ReactionSummary{Counts: []*chatimv1.ReactionCount{{Emoji: emoji, Count: 1}}, Version: version}
}

func TestMarkEventIDsMatchTheCore(t *testing.T) {
	cases := map[string]string{
		e2e.ReactionEventID("42", 3, reactor, 2): "42-0-3-e2e-user-n2",
		e2e.CountsEventID("42", 3, 2):            "42-0-3-reactions-v2",
		e2e.PinEventID("42", 1):                  "42-p1",
	}
	for got, want := range cases {
		if got != want {
			t.Fatalf("event id = %q, want %q", got, want)
		}
	}
}

func TestEventOfReadsReactionCountAndPinEvents(t *testing.T) {
	msg := &chatimv1.Message{RoomId: "42", Seq: 4, Cid: "a-d"}
	cases := []struct {
		ev   *chatimv1.Event
		want e2e.Event
	}{
		{
			&chatimv1.Event{Id: "42-0-3-e2e-user-n2", RoomId: "42", Seq: 3, Payload: &chatimv1.Event_ReactionChanged{
				ReactionChanged: &chatimv1.ReactionChanged{User: reactor, Emoji: "🎉", PreviousEmoji: "👍", Change: 2},
			}},
			e2e.Event{Kind: e2e.KindReaction, Room: "42", ID: "42-0-3-e2e-user-n2", Seq: 3, User: reactor, Version: 2, Text: "🎉", Subject: "s"},
		},
		{
			&chatimv1.Event{Id: "42-0-3-reactions-v2", RoomId: "42", Seq: 3, Payload: &chatimv1.Event_CountsChanged{
				CountsChanged: &chatimv1.CountsChanged{Counter: "reactions", Reactions: summary(2, "🎉")},
			}},
			e2e.Event{Kind: e2e.KindCounts, Room: "42", ID: "42-0-3-reactions-v2", Seq: 3, Text: "🎉=1", Subject: "s"},
		},
		{
			&chatimv1.Event{Id: "42-p1", RoomId: "42", Seq: 4, Payload: &chatimv1.Event_MessagePinned{
				MessagePinned: &chatimv1.MessagePinned{Message: msg, PinVersion: 1},
			}},
			e2e.Event{Kind: e2e.KindPinned, Room: "42", ID: "42-p1", Seq: 4, CID: "a-d", Subject: "s"},
		},
		{
			&chatimv1.Event{Id: "42-p2", RoomId: "42", Seq: 4, Payload: &chatimv1.Event_MessageUnpinned{
				MessageUnpinned: &chatimv1.MessageUnpinned{Message: msg, PinVersion: 2},
			}},
			e2e.Event{Kind: e2e.KindUnpinned, Room: "42", ID: "42-p2", Seq: 4, CID: "a-d", Subject: "s"},
		},
	}
	for _, c := range cases {
		got, ok := e2e.EventOf("s", c.ev)
		if !ok || got != c.want || !got.IsMark() || got.IsChange() || got.IsCreated() {
			t.Fatalf("EventOf(%s) = %+v, %v; want %+v, true", c.ev.GetId(), got, ok, c.want)
		}
	}
}

func TestCheckEventsSkipsReactionAndPinEvents(t *testing.T) {
	as := acks(1)
	created := e2e.Event{Kind: e2e.KindCreated, Room: room, ID: e2e.MessageEventID(room, 1), Seq: 1, CID: as[0].CID}
	counts := e2e.Event{Kind: e2e.KindCounts, Room: room, ID: e2e.CountsEventID(room, 9, 1), Seq: 9}
	if cov, err := e2e.CheckEvents(as, room, []e2e.Event{created, counts}); err != nil || cov.Distinct != 1 || cov.MissingCount != 0 {
		t.Fatalf("CheckEvents = %+v, %v; want the counts event skipped", cov, err)
	}
}

func TestCheckReactAndPinReplies(t *testing.T) {
	want := e2e.Reaction{Seq: 3, Emoji: "🎉", Change: 2, Version: 2}
	if err := e2e.CheckReactReply(want, 2, summary(2, "🎉")); err != nil {
		t.Fatalf("CheckReactReply(good) = %v", err)
	}
	expectErr(t, e2e.CheckReactReply(want, 1, summary(2, "🎉")), "change 1")
	expectErr(t, e2e.CheckReactReply(want, 2, summary(1, "🎉")), "version 1")
	expectErr(t, e2e.CheckReactReply(want, 2, summary(2, "👍")), "👍=1")
	pin := e2e.Pin{Seq: 4, Version: 1}
	good := []*chatimv1.Pin{{Seq: 4, By: reactor, PinVersion: 1, PinnedAt: timestamppb.Now()}}
	if err := e2e.CheckPinReply(pin, reactor, 1, good); err != nil {
		t.Fatalf("CheckPinReply(good) = %v", err)
	}
	expectErr(t, e2e.CheckPinReply(pin, reactor, 2, good), "version 2")
	expectErr(t, e2e.CheckPinReply(pin, "bob", 1, good), "by")
	expectErr(t, e2e.CheckPinReply(pin, reactor, 1, nil), "0 pin")
}

func TestCheckReactionsWantsCountsOnTheReactedSeqOnly(t *testing.T) {
	as := acks(3)
	want := []e2e.Reaction{{Seq: 2, Emoji: "🎉", Change: 2, Version: 2}}
	page := messagesOf(as)
	page[1].Reactions = summary(2, "🎉")
	if err := e2e.CheckReactions(want, page); err != nil {
		t.Fatalf("CheckReactions(reacted) = %v", err)
	}
	if err := e2e.CheckReactions(nil, messagesOf(as)); err != nil {
		t.Fatalf("CheckReactions(none) = %v", err)
	}
	expectErr(t, e2e.CheckReactions(nil, page), "want none")
	expectErr(t, e2e.CheckReactions(want, messagesOf(as)), "seq 2")
	stale := messagesOf(as)
	stale[1].Reactions = summary(1, "🎉")
	expectErr(t, e2e.CheckReactions(want, stale), "version 1")
	expectErr(t, e2e.CheckReactions(want, page[:1]), "missing")
}

func TestCheckMarkEventsWantsTheFinalIDs(t *testing.T) {
	reactions := []e2e.Reaction{{Seq: 3, Emoji: "🎉", Change: 2, Version: 2}}
	pins := []e2e.Pin{{Seq: 4, Version: 1}}
	final := e2e.Event{Kind: e2e.KindReaction, Room: room, ID: e2e.ReactionEventID(room, 3, reactor, 2), Seq: 3, User: reactor, Version: 2, Text: "🎉"}
	earlier := e2e.Event{Kind: e2e.KindReaction, Room: room, ID: e2e.ReactionEventID(room, 3, reactor, 1), Seq: 3, User: reactor, Version: 1, Text: "👍"}
	counts := e2e.Event{Kind: e2e.KindCounts, Room: room, ID: e2e.CountsEventID(room, 3, 2), Seq: 3, Text: "🎉=1"}
	pinned := e2e.Event{Kind: e2e.KindPinned, Room: room, ID: e2e.PinEventID(room, 1), Seq: 4, CID: "a-d"}
	created := e2e.Event{Kind: e2e.KindCreated, Room: room, ID: e2e.MessageEventID(room, 1), Seq: 1}
	missing, err := e2e.CheckMarkEvents(room, reactor, reactions, pins, []e2e.Event{created, earlier, final})
	if err != nil || !slices.Equal(missing, []string{counts.ID, pinned.ID}) {
		t.Fatalf("CheckMarkEvents = %v, %v; want %s and %s missing", missing, err, counts.ID, pinned.ID)
	}
	if missing, err := e2e.CheckMarkEvents(room, reactor, reactions, pins, []e2e.Event{final, counts, pinned, pinned}); err != nil || len(missing) != 0 {
		t.Fatalf("CheckMarkEvents(all, one duplicate) = %v, %v; want none missing", missing, err)
	}
	wrongText := final
	wrongText.Text = "👍"
	bad := map[string]e2e.Event{
		"room 7":              {Kind: e2e.KindCounts, Room: "7", ID: counts.ID, Seq: 3, Text: "🎉=1"},
		"unexpected":          {Kind: e2e.KindReaction, Room: room, ID: e2e.ReactionEventID(room, 5, reactor, 1), Seq: 5, User: reactor, Version: 1},
		`by "bob"`:            {Kind: e2e.KindReaction, Room: room, ID: e2e.ReactionEventID(room, 3, "bob", 1), Seq: 3, User: "bob", Version: 1},
		"want counts_changed": {Kind: e2e.KindReaction, Room: room, ID: counts.ID, Seq: 3, User: reactor, Text: "🎉=1"},
		`"👍", want`:           wrongText,
	}
	for part, ev := range bad {
		_, err := e2e.CheckMarkEvents(room, reactor, reactions, pins, []e2e.Event{ev})
		expectErr(t, err, part)
	}
}
```

**Step 7: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./tools/corecli/internal/e2e/..."`
Expected: FAIL biên dịch: `undefined: e2e.Reaction`, `undefined: e2e.ReactionEventID`, `unknown field User in struct literal of type e2e.Event`, `got.IsMark undefined`.

**Step 8: Code package e2e**

`tools/corecli/internal/e2e/state.go`: trong `type State struct`, thêm sau dòng `	Changes []Change `json:"changes,omitempty"``:

```go
	Reactions []Reaction `json:"reactions,omitempty"`
	Pins      []Pin      `json:"pins,omitempty"`
```

(gofmt căn lại cột của cả struct.)

`tools/corecli/internal/e2e/events.go`, ba chỗ:
- Trong `type Event struct`, thêm sau dòng `	CID     string `json:"cid"``: `	User    string `json:"user,omitempty"`` (gofmt căn cột).
- Thay `func (e Event) IsChange() bool { return e.Kind == KindEdited || e.Kind == KindDeleted }` bằng:

```go
func (e Event) IsChange() bool { return e.Kind == KindEdited || e.Kind == KindDeleted }

func (e Event) IsCreated() bool { return e.Kind == "" || e.Kind == KindCreated }
```

- Trong `EventOf`, thay dòng `	return Event{}, false` cuối hàm bằng `	return markOf(subject, ev)`.

Dòng JSON cũ không có `kind` vẫn đọc ra `Kind == ""` và được coi là `msg_created` (`IsCreated`).

`tools/corecli/internal/e2e/check.go`: trong `CheckEvents`, thay

```go
		if ev.IsChange() {
			continue
		}
```

bằng

```go
		if !ev.IsCreated() {
			continue
		}
```

`tools/corecli/internal/e2e/marks.go`:

```go
package e2e

import (
	"strconv"
	"strings"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const (
	KindReaction = "reaction_changed"
	KindCounts   = "counts_changed"
	KindPinned   = "msg_pinned"
	KindUnpinned = "msg_unpinned"
)

type Reaction struct {
	Seq     uint64 `json:"seq"`
	Emoji   string `json:"emoji"`
	Change  uint32 `json:"change"`
	Version uint64 `json:"version"`
}

type Pin struct {
	Seq     uint64 `json:"seq"`
	Version uint64 `json:"version"`
}

func ReactionEventID(room string, seq uint64, user string, change uint32) string {
	return MessageEventID(room, seq) + "-" + user + "-n" + strconv.FormatUint(uint64(change), 10)
}

func CountsEventID(room string, seq, version uint64) string {
	return MessageEventID(room, seq) + "-reactions-v" + strconv.FormatUint(version, 10)
}

func PinEventID(room string, version uint64) string {
	return room + "-p" + strconv.FormatUint(version, 10)
}

func FormatCounts(counts []*chatimv1.ReactionCount) string {
	parts := make([]string, len(counts))
	for i, c := range counts {
		parts[i] = c.GetEmoji() + "=" + strconv.FormatUint(uint64(c.GetCount()), 10)
	}
	return strings.Join(parts, ",")
}

func (e Event) IsMark() bool {
	switch e.Kind {
	case KindReaction, KindCounts, KindPinned, KindUnpinned:
		return true
	default:
		return false
	}
}

func markOf(subject string, ev *chatimv1.Event) (Event, bool) {
	out := Event{Room: ev.GetRoomId(), ID: ev.GetId(), Seq: ev.GetSeq(), Subject: subject}
	switch {
	case ev.GetReactionChanged() != nil:
		r := ev.GetReactionChanged()
		out.Kind, out.User, out.Text, out.Version = KindReaction, r.GetUser(), r.GetEmoji(), r.GetChange()
	case ev.GetCountsChanged() != nil:
		out.Kind, out.Text = KindCounts, FormatCounts(ev.GetCountsChanged().GetReactions().GetCounts())
	case ev.GetMessagePinned() != nil:
		out.Kind, out.CID = KindPinned, ev.GetMessagePinned().GetMessage().GetCid()
	case ev.GetMessageUnpinned() != nil:
		out.Kind, out.CID = KindUnpinned, ev.GetMessageUnpinned().GetMessage().GetCid()
	default:
		return Event{}, false
	}
	return out, true
}
```

`tools/corecli/internal/e2e/marks_check.go`:

```go
package e2e

import (
	"fmt"
	"maps"
	"slices"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type markWant struct {
	kind string
	seq  uint64
	text string
}

func CheckReactReply(want Reaction, change uint32, got *chatimv1.ReactionSummary) error {
	counts, wantCounts := FormatCounts(got.GetCounts()), want.Emoji+"=1"
	if change != want.Change || got.GetVersion() != want.Version || counts != wantCounts {
		return fmt.Errorf("react on seq %d returned change %d counts %q version %d, want change %d counts %q version %d",
			want.Seq, change, counts, got.GetVersion(), want.Change, wantCounts, want.Version)
	}
	return nil
}

func CheckPinReply(want Pin, by string, version uint64, pins []*chatimv1.Pin) error {
	if version != want.Version || len(pins) != 1 {
		return fmt.Errorf("pin of seq %d returned version %d with %d pin(s), want version %d with one pin", want.Seq, version, len(pins), want.Version)
	}
	p := pins[0]
	if p.GetSeq() != want.Seq || p.GetThreadRoot() != 0 || p.GetBy() != by || p.GetPinVersion() != want.Version || p.GetPinnedAt() == nil {
		return fmt.Errorf("pin = (seq %d thread %d by %q version %d), want (seq %d thread 0 by %q version %d)",
			p.GetSeq(), p.GetThreadRoot(), p.GetBy(), p.GetPinVersion(), want.Seq, by, want.Version)
	}
	return nil
}

func CheckReactions(want []Reaction, got []*chatimv1.Message) error {
	bySeq := make(map[uint64]Reaction, len(want))
	for _, r := range want {
		bySeq[r.Seq] = r
	}
	seen := 0
	for _, m := range got {
		counts, version := FormatCounts(m.GetReactions().GetCounts()), m.GetReactions().GetVersion()
		r, ok := bySeq[m.GetSeq()]
		switch {
		case !ok && (counts != "" || version != 0):
			return fmt.Errorf("seq %d has reactions %q (version %d), want none", m.GetSeq(), counts, version)
		case ok && (counts != r.Emoji+"=1" || version < r.Version):
			return fmt.Errorf("seq %d has reactions %q (version %d), want %q at version %d or later", m.GetSeq(), counts, version, r.Emoji+"=1", r.Version)
		case ok:
			seen++
		}
	}
	if seen != len(bySeq) {
		return fmt.Errorf("%d of %d reacted seq missing from history", len(bySeq)-seen, len(bySeq))
	}
	return nil
}

func CheckMarkEvents(room, user string, reactions []Reaction, pins []Pin, events []Event) ([]string, error) {
	want := map[string]markWant{}
	reacted, pinned := map[uint64]bool{}, map[uint64]bool{}
	for _, r := range reactions {
		want[ReactionEventID(room, r.Seq, user, r.Change)] = markWant{KindReaction, r.Seq, r.Emoji}
		want[CountsEventID(room, r.Seq, r.Version)] = markWant{KindCounts, r.Seq, r.Emoji + "=1"}
		reacted[r.Seq] = true
	}
	for _, p := range pins {
		want[PinEventID(room, p.Version)] = markWant{KindPinned, p.Seq, ""}
		pinned[p.Seq] = true
	}
	seen := make(map[string]bool, len(want))
	for _, ev := range events {
		if !ev.IsMark() {
			continue
		}
		if err := checkMark(room, user, ev, reacted, pinned); err != nil {
			return nil, err
		}
		w, ok := want[ev.ID]
		if !ok {
			continue
		}
		if ev.Kind != w.kind || ev.Seq != w.seq || (w.kind != KindPinned && ev.Text != w.text) {
			return nil, fmt.Errorf("event %s is %s seq %d %q, want %s seq %d %q", ev.ID, ev.Kind, ev.Seq, ev.Text, w.kind, w.seq, w.text)
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

func checkMark(room, user string, ev Event, reacted, pinned map[uint64]bool) error {
	onReaction := ev.Kind == KindReaction || ev.Kind == KindCounts
	switch {
	case ev.Room != room:
		return fmt.Errorf("%s event %s is for room %s, want %s", ev.Kind, ev.ID, ev.Room, room)
	case onReaction && !reacted[ev.Seq], !onReaction && !pinned[ev.Seq]:
		return fmt.Errorf("unexpected %s event %s on seq %d", ev.Kind, ev.ID, ev.Seq)
	case ev.Kind == KindReaction && ev.User != user:
		return fmt.Errorf("reaction event %s by %q, want %q", ev.ID, ev.User, user)
	}
	return nil
}
```

Ghi chú hành vi:
- `CheckReactions` nhận `version ≥` bản response: worker chỉ bump khi số đếm khác, nên thường bằng; nếu touch inline của fast path đã trượt thì worker sửa và tăng `v`, số đếm vẫn phải đúng.
- `CheckMarkEvents` bắt buộc đúng các id cuối; event trung gian của cùng tin/user được nhận (fast path), event của seq khác, room khác hay user khác là lỗi.

**Step 9: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./tools/corecli/..."`
Expected: PASS cho `tools/corecli/internal/e2e` (gồm test cũ `TestEventOfKeepsMessageEventsOnly`, `TestCheckChangeEventsFindsEachChangeByID`); `tools/corecli` (main) vẫn biên dịch (chưa dùng hàm mới). `wc -l tools/corecli/internal/e2e/*.go` → mỗi file < 200 (marks_check.go ~111, marks_test.go ~145).

**Step 10: Code corecli**

`tools/corecli/cmd_react_pin.go`:

```go
package main

import (
	"context"
	"flag"

	"google.golang.org/protobuf/proto"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

func reactCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("react", flag.ContinueOnError)
	o := addOptions(fs)
	msg := addMessageFlags(fs)
	emoji := fs.String("emoji", "", "emoji to set; empty removes the caller's reaction")
	if err := parseMessage(fs, args, msg); err != nil {
		return err
	}
	req := &chatimv1.ReactMessageRequest{RoomId: *msg.room, ThreadRoot: *msg.thread, Seq: *msg.seq, Emoji: *emoji}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
		resp, st, err := s.client.ReactMessage(ctx, req)
		if err != nil {
			return err
		}
		report("react", st)
		return printJSON(resp)
	})
}

func pinCmd(ctx context.Context, args []string) error { return pinChangeCmd(ctx, "pin", args) }

func unpinCmd(ctx context.Context, args []string) error { return pinChangeCmd(ctx, "unpin", args) }

func pinChangeCmd(ctx context.Context, name string, args []string) error {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	o := addOptions(fs)
	msg := addMessageFlags(fs)
	if err := parseMessage(fs, args, msg); err != nil {
		return err
	}
	return withSession(ctx, o, func(ctx context.Context, s *session) error {
		resp, st, err := setPinned(ctx, s.client, name == "pin", *msg.room, *msg.thread, *msg.seq)
		if err != nil {
			return err
		}
		report(name, st)
		return printJSON(resp)
	})
}

func setPinned(ctx context.Context, cl *route.Client, pinned bool, room string, thread, seq uint64) (proto.Message, route.Stats, error) {
	if pinned {
		return cl.PinMessage(ctx, &chatimv1.PinMessageRequest{RoomId: room, ThreadRoot: thread, Seq: seq})
	}
	return cl.UnpinMessage(ctx, &chatimv1.UnpinMessageRequest{RoomId: room, ThreadRoot: thread, Seq: seq})
}
```

`tools/corecli/e2e_react_pin.go`:

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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
	"github.com/ivannguyendev/chatim/tools/internal/route"
)

const (
	firstEmoji  = "👍"
	secondEmoji = "🎉"
)

var errMarkedTwice = errors.New("this scenario already holds its reaction and pin")

func e2eReactPin(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("e2e react-pin", flag.ContinueOnError)
	o := addOptions(fs)
	dir := fs.String("state", "/state", "directory holding the scenario state")
	react := fs.Uint64("react", 3, "acked, not deleted seq to react to, then change the emoji")
	pin := fs.Uint64("pin", 4, "acked, not deleted seq to pin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := e2e.Load(statePath(*dir))
	if err != nil {
		return err
	}
	switch {
	case len(st.Reactions) > 0 || len(st.Pins) > 0:
		return errMarkedTwice
	case !markable(st, *react) || !markable(st, *pin):
		return fmt.Errorf("-react %d and -pin %d must be acked seq that are not deleted", *react, *pin)
	}
	o.tenant, o.user = st.Tenant, st.User
	var r e2e.Reaction
	var p e2e.Pin
	err = withSession(ctx, o, func(ctx context.Context, s *session) error {
		var err error
		if r, err = reactThenChange(ctx, s.client, st.Room, *react); err != nil {
			return err
		}
		if p, err = pinTwice(ctx, s.client, st.Room, st.User, *pin); err != nil {
			return err
		}
		return deletedTakesNoMark(ctx, s.client, st)
	})
	if err != nil {
		return err
	}
	st.Reactions, st.Pins = []e2e.Reaction{r}, []e2e.Pin{p}
	fmt.Fprintf(os.Stderr, "seq %d reacted %s then %s (change %d, counts version %d), seq %d pinned (pin version %d); repeats were no-ops\n",
		r.Seq, firstEmoji, r.Emoji, r.Change, r.Version, p.Seq, p.Version)
	return e2e.Save(statePath(*dir), st)
}

func markable(st e2e.State, seq uint64) bool {
	acked := slices.ContainsFunc(st.Acks, func(a e2e.Ack) bool { return a.Seq == seq })
	deleted := slices.ContainsFunc(st.Changes, func(c e2e.Change) bool { return c.Seq == seq && c.Deleted })
	return acked && !deleted
}

func reactThenChange(ctx context.Context, cl *route.Client, room string, seq uint64) (e2e.Reaction, error) {
	steps := []e2e.Reaction{
		{Seq: seq, Emoji: firstEmoji, Change: 1, Version: 1},
		{Seq: seq, Emoji: secondEmoji, Change: 2, Version: 2},
		{Seq: seq, Emoji: secondEmoji, Change: 2, Version: 2},
	}
	for i, want := range steps {
		resp, stats, err := cl.ReactMessage(ctx, &chatimv1.ReactMessageRequest{RoomId: room, Seq: seq, Emoji: want.Emoji})
		if err == nil {
			err = e2e.CheckReactReply(want, resp.GetChange(), resp.GetReactions())
		}
		if err != nil {
			return e2e.Reaction{}, fmt.Errorf("react %d on seq %d: %w", i+1, seq, err)
		}
		report("react "+strconv.Itoa(i+1)+" seq "+strconv.FormatUint(seq, 10), stats)
	}
	return steps[len(steps)-1], nil
}

func pinTwice(ctx context.Context, cl *route.Client, room, user string, seq uint64) (e2e.Pin, error) {
	want := e2e.Pin{Seq: seq, Version: 1}
	for i := range 2 {
		resp, stats, err := cl.PinMessage(ctx, &chatimv1.PinMessageRequest{RoomId: room, Seq: seq})
		if err == nil {
			err = e2e.CheckPinReply(want, user, resp.GetPinVersion(), resp.GetPins())
		}
		if err != nil {
			return e2e.Pin{}, fmt.Errorf("pin %d of seq %d: %w", i+1, seq, err)
		}
		report("pin "+strconv.Itoa(i+1)+" seq "+strconv.FormatUint(seq, 10), stats)
	}
	return want, nil
}

func deletedTakesNoMark(ctx context.Context, cl *route.Client, st e2e.State) error {
	for _, c := range st.Changes {
		if !c.Deleted {
			continue
		}
		_, _, err := cl.ReactMessage(ctx, &chatimv1.ReactMessageRequest{RoomId: st.Room, Seq: c.Seq, Emoji: firstEmoji})
		if status.Code(err) != codes.FailedPrecondition {
			return fmt.Errorf("react on deleted seq %d = %v, want FailedPrecondition", c.Seq, err)
		}
		_, _, err = cl.PinMessage(ctx, &chatimv1.PinMessageRequest{RoomId: st.Room, Seq: c.Seq})
		if status.Code(err) != codes.FailedPrecondition {
			return fmt.Errorf("pin of deleted seq %d = %v, want FailedPrecondition", c.Seq, err)
		}
	}
	return nil
}
```

Kịch bản: room e2e mới nên lần react đầu là `change 1`, `rx.v` 1; đổi emoji là `change 2`, `v` 2; gửi lại 🎉 là no-op (`change 2`, `v` 2, không event); ghim lần đầu `pin_version 1`, lần hai no-op. Lệnh lỗi giữa chừng thì chạy lại không được (`errMarkedTwice` chỉ chặn khi đã lưu state; react lần một đã ghi thì lần chạy lại nhận `change 2` ở bước một và báo lệch) — giống `e2e change`, chạy lại cả `make e2e`.

`tools/corecli/main.go`:
- Trong `const usage`, thay dòng `  edits         list the edit history of a message` bằng:

```
  edits         list the edit history of a message
  react         set or change the caller's reaction on a message (-emoji "" removes it)
  pin           pin a message in its room
  unpin         unpin a message
```

- Thay dòng `  e2e           end-to-end scenario steps: setup, send, change, check` bằng `  e2e           end-to-end scenario steps: setup, send, change, react-pin, check`.
- Trong map `commands`, thêm sau `"edits":       editsCmd,`:

```go
		"react":       reactCmd,
		"pin":         pinCmd,
		"unpin":       unpinCmd,
```

`tools/corecli/cmd_e2e.go`:
- `const e2eUsage = "usage: corecli e2e setup|send|change|check [flags]"` thành `const e2eUsage = "usage: corecli e2e setup|send|change|react-pin|check [flags]"`.
- `	steps := map[string]command{"setup": e2eSetup, "send": e2eSend, "change": e2eChange, "check": e2eCheck}` thành:

```go
	steps := map[string]command{"setup": e2eSetup, "send": e2eSend, "change": e2eChange, "react-pin": e2eReactPin, "check": e2eCheck}
```

`tools/corecli/e2e_check.go`, ba chỗ:
- Trong `checkHistory`, thay

```go
		if err == nil {
			err = e2e.CheckPage(st.Acks, st.Changes, all, st.Room, st.User)
		}
```

bằng

```go
		if err == nil {
			err = e2e.CheckPage(st.Acks, st.Changes, all, st.Room, st.User)
		}
		if err == nil {
			err = e2e.CheckReactions(st.Reactions, all)
		}
```

- `	fmt.Fprintf(os.Stderr, "live ok: an event for each of %d seq, %d duplicate(s) dropped, %d change event(s)\n", cov.Distinct, cov.Duplicates, len(st.Changes))` thành:

```go
	fmt.Fprintf(os.Stderr, "live ok: an event for each of %d seq, %d duplicate(s) dropped, %d change event(s), %d reaction(s), %d pin(s)\n",
		cov.Distinct, cov.Duplicates, len(st.Changes), len(st.Reactions), len(st.Pins))
```

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
		var changes, marks []string
		if err == nil {
			changes, err = e2e.CheckChangeEvents(st.Room, st.Changes, evs)
		}
		if err == nil {
			marks, err = e2e.CheckMarkEvents(st.Room, st.User, st.Reactions, st.Pins, evs)
		}
		switch {
		case err != nil:
			return cov, fmt.Errorf("live events: %w", err)
		case cov.MissingCount == 0 && len(changes) == 0 && len(marks) == 0:
			return cov, nil
		case time.Now().After(deadline):
			return cov, fmt.Errorf("after %v: %d of %d seq have no live event (first missing %v); change events missing %v; reaction and pin events missing %v",
				wait, cov.MissingCount, len(st.Acks), cov.Missing, changes, marks)
		case !backoff.Pause(ctx, eventPoll):
			return cov, ctx.Err()
		}
	}
}
```

`scripts/e2e.sh`:
- Thay

```bash
    echo "e2e PASS: $first messages before and $after after killing $victim, no loss, no duplicate, every acked seq live; seq 1 edited and seq 2 deleted on history, edit history and live"
```

bằng

```bash
    echo "e2e PASS: $first messages before and $after after killing $victim, no loss, no duplicate, every acked seq live; seq 1 edited and seq 2 deleted on history, edit history and live; seq 3 reacted then re-reacted and seq 4 pinned on replies, history and live"
```

- Thay

```bash
step="phase 3 check"
cli e2e check -state /state
result=PASS
```

bằng

```bash
step="phase 3 check"
cli e2e check -state /state

step="phase 4 react to seq 3 and pin seq 4"
echo "phase 4: react to seq 3, change the emoji, pin seq 4"
cli e2e react-pin -state /state -react 3 -pin 4
step="phase 4 check"
cli e2e check -state /state
result=PASS
```

Watcher chạy từ phase 1 nên `events.jsonl` nhận cả bốn loại event mới. Phase 4 cần `E2E_FIRST ≥ 4` (mặc định 40).

**Step 11: Chạy**

Run: `make -s go ARGS="test -race -shuffle=on ./tools/..."` rồi `make -s go ARGS="vet ./tools/..."`
Expected: PASS; `wc -l tools/corecli/*.go tools/corecli/internal/e2e/*.go` → mỗi file < 200 (e2e_react_pin.go ~123, e2e_check.go ~144, cmd_react_pin.go ~58).

Run (cần Task 11 đã wiring; Task 13 cho event bù, không bắt buộc cho e2e vì fast path đã phát các id cuối):

```bash
make infra-up && make image TARGET=apps/core && make core-up && make e2e
```

Expected: các dòng `phase 1`…`phase 3` như M2b.2; `phase 4: react to seq 3, change the emoji, pin seq 4`; `react 1 seq 3 via …`, `react 2 …`, `react 3 …`, `pin 1 seq 4 via …`, `pin 2 …`; `seq 3 reacted 👍 then 🎉 (change 2, counts version 2), seq 4 pinned (pin version 1); repeats were no-ops`; ở phase 4 check: `live ok: an event for each of 80 seq, … duplicate(s) dropped, 2 change event(s), 1 reaction(s), 1 pin(s)`; dòng cuối `e2e PASS: 40 messages before and 40 after killing core-1, no loss, no duplicate, every acked seq live; seq 1 edited and seq 2 deleted on history, edit history and live; seq 3 reacted then re-reacted and seq 4 pinned on replies, history and live`.

Thử nhanh lệnh tay (room lấy từ log e2e; `REDIS_PASSWORD` export từ `.env`, không in ra):

```bash
docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev react -room <ROOM> -seq 5 -emoji "❤️"
docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev unpin -room <ROOM> -seq 4
```

Expected: lệnh đầu in một dòng protojson (khoảng trắng do protojson chọn) có `"change":1`, `"counts":[{"emoji":"❤️","count":1}]` và `"version":"1"` (seq 5 chưa có reaction); lệnh hai in `"pinVersion":"2"` và không có `pins` (danh sách rỗng), exit 0. `react -room <ROOM> -seq 2 -emoji x` (seq 2 đã xoá ở phase 3) → `failed precondition`, exit 1.

**Step 12: INDEXES.csv + commit**

Thay cả các dòng sau (theo đầu dòng):

```csv
tools/corecli,tool,"CLI routed by slot: create-room/send/history/edit/delete/hide/clear/edits/react/pin/unpin/watch/slots/e2e (steps setup, send, change, react-pin, check); -redis-password or env REDIS_PASSWORD; image chatim/corecli:dev used by make e2e",main,scripts/e2e.sh,e2e,D82;D89;D92
tools/corecli/internal/e2e,package,"E2E state file and checks: acks contiguous with distinct cids; history pages exact, including the scenario's edit (new text, version 1) and delete (no text, deleted, version 1) and the reaction counts (only the reacted seq, version at least the reply's); live msg_created events checked by natural id and deduped by seq (CheckEvents skips every other kind); msg_edited/msg_deleted events checked by {room}-0-{seq}-v{ver} with kind and snapshot text; reaction_changed/counts_changed/msg_pinned checked by the final ids {room}-0-{seq}-{user}-n{n}, {room}-0-{seq}-reactions-v{v}, {room}-p{pv} (earlier ones of the same seq and user accepted); react and pin replies (change, counts, versions, pins); edit history = original then the edit, none after delete; slot share balance",State;Load;Save;Event;Event.IsChange;Event.IsCreated;Event.IsMark;EventOf;CheckAcks;CheckPage;CheckEvents;MessageEventID;Change;EditOf;DeleteOf;EditTextFor;ChangeEventID;CheckChangeEvents;CheckVersions;Reaction;Pin;ReactionEventID;CountsEventID;PinEventID;FormatCounts;CheckReactReply;CheckPinReply;CheckReactions;CheckMarkEvents;KindCreated;KindEdited;KindDeleted;KindReaction;KindCounts;KindPinned;KindUnpinned;ShareOf,tools/corecli,unit,D83;D93
scripts/e2e.sh,script,Kill-a-core end-to-end scenario: create/send/history/live; docker kill core-1; send more; verify no loss/dup; restore; phase 3 edits seq 1 and deletes seq 2 from version 0 then checks history/edit history/live change events; phase 4 reacts to seq 3 then changes the emoji and pins seq 4 (repeats are no-ops; the deleted seq takes no reaction or pin) then checks history counts and live reaction/counts/pin events; corecli containers get -e REDIS_PASSWORD,,make e2e,e2e,
```

Kiểm 7 cột như Task 14.

```bash
make fmt-check && make vet && make lint
git add tools/corecli/cmd_react_pin.go tools/corecli/e2e_react_pin.go tools/corecli/internal/e2e/marks.go tools/corecli/internal/e2e/marks_check.go tools/corecli/internal/e2e/marks_test.go
git commit -m "feat(corecli): add react, pin and unpin commands and an e2e reaction and pin phase" -- tools/corecli/ scripts/e2e.sh INDEXES.csv
```

---

### Task 16: Itest end-to-end reaction và ghim

Khẳng định trên hạ tầng thật (skip khi thiếu `CHATIM_IT_*`), dùng helper sẵn có (`realInfra`, `startCore`, `dialCore`, `createRoom` — room có `alice` (owner) và `bob`, `caller`, `callerAs`, `sendAs` (đã gửi qua `sendRetrying`), `historyAs`, `parseRoom`, `subscribeLive`, `itStore`, `itFastEffects`, `itActivityPoll`) và helper GAP-1 của Task 14 (`itReactions`, `itPins`):
- (a) reaction ghi thẳng vào Mongo (insert, rồi đổi emoji, rồi gỡ: insert → update/replace → update) → feed giải mã đúng → work stream → `reaction_counter` + `reaction_event` phát `counts_changed` v1..v3 và `reaction_changed` n1..n3 với payload của doc hiện tại; `rx` cuối rỗng ở v3.
- (b) fact ghim ghi thẳng vào Mongo (giả lập core chết giữa fact và projection) → `pin_projection` sửa `rooms.pins`/`pv` → `pin_event` phát `msg_pinned`; lệnh ghim/bỏ ghim sau đó đi tiếp từ projection đã sửa; bỏ ghim lặp lại là no-op; live `msg_unpinned`.
- (c) 24 user react đồng thời, rồi đổi/gỡ đồng thời → `rx` hội tụ đúng số đếm (bằng `Count` của fact), `rx.v` tăng; `GetHistory` trả đúng số đếm.
- (d) `PIN_LIMIT=3`, 6 lệnh ghim đồng thời (client gửi lại khi `UNAVAILABLE` như route): đúng 3 thành công, 3 `FAILED_PRECONDITION`, đúng 3 fact, projection 3 ghim ở pv 3.
- (e) `DefaultPolicy`: member không phải tác giả react/ghim/bỏ ghim được, kể cả khi `MESSAGE_LOCKED_KINDS=text` khoá sửa/xoá (D94); người ngoài room → `PERMISSION_DENIED`. Thêm: tin đã xoá không nhận reaction/ghim mới (`FAILED_PRECONDITION`, kể cả khi đang ghim), gỡ reaction và bỏ ghim vẫn được, lịch sử không trả `reactions` của tin xoá.

`sendRetrying` được tổng quát thành `retryingUnavailable[T]` (cùng hằng, cùng hành vi) để (d) dùng lại; `sendRetrying` giữ chữ ký.

Chờ trạng thái lưu (`rx`, `rooms.pins`) dùng poll `itActivityPoll` 100ms có hạn `itLiveLimit`, như `awaitActivity` đã có: hội tụ của worker không có tín hiệu nào khác để chờ. Live event thì chờ theo id (`awaitLiveEvents` gom nhiều id, không phụ thuộc thứ tự tới).

Không có bước "thấy fail": các test xác nhận hành vi của Task 5–13. Test fail ở lần chạy đầu là lỗi của task trước: dừng và báo (không sửa test cho qua). Đặc biệt:
- (a) không thấy `reaction_changed` n2/n3 → feed chưa giải mã update/replace (Task 6) hoặc reader chưa forward `ReactionChanged` (Task 7).
- (c) kẹt ở số đếm cũ → `reaction_counter` không chạy hoặc witness luôn `ErrStaleRead` (Task 5/13).
- (d) 4 thành công → kiểm giới hạn chạy trên projection thay vì fold tới pv hiện tại (Task 8/10).

Kiểm `git diff --quiet -- INDEXES.csv` trước khi sửa (như Task 14).

**Files:**
- Modify: `apps/core/send_retry_test.go` (`retryingUnavailable`)
- Modify: `apps/core/it_reactions_pins_test.go` (viết lại: thêm helper)
- Create: `apps/core/reaction_integration_test.go`
- Create: `apps/core/pin_integration_test.go`
- Create: `apps/core/reaction_pin_access_integration_test.go`
- Modify: `INDEXES.csv`

**Step 1: Helper**

`apps/core/send_retry_test.go`, thay hàm `sendRetrying` bằng:

```go
func sendRetrying(ctx context.Context, client chatimv1.CoreServiceClient, req *chatimv1.SendMessageRequest) (*chatimv1.SendMessageResponse, error) {
	return retryingUnavailable(ctx, func(ctx context.Context) (*chatimv1.SendMessageResponse, error) {
		return client.SendMessage(ctx, req)
	})
}

func retryingUnavailable[T any](ctx context.Context, do func(context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, itSendRetryLimit)
	defer cancel()
	gap := itSendRetryFirstGap
	for {
		out, err := do(ctx)
		if status.Code(err) != codes.Unavailable || !backoff.Pause(ctx, backoff.Jitter(gap)) {
			return out, err
		}
		gap = min(2*gap, itSendRetryMaxGap)
	}
}
```

`apps/core/it_reactions_pins_test.go` (viết lại cả file; hai helper GAP-1 giữ nguyên):

```go
package main

import (
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func itReactions(st *mongostore.Store) store.Reactions { return st.Reactions() }

func itPins(st *mongostore.Store) store.Pins { return st.Pins() }

func createRoomWith(t *testing.T, client chatimv1.CoreServiceClient, members []string) string {
	t.Helper()
	resp, err := client.CreateRoom(caller(t.Context()), &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Name: "reactions", Members: members,
	})
	if err != nil {
		t.Fatalf("CreateRoom(%d members): %v", len(members), err)
	}
	return resp.GetRoom().GetId()
}

func awaitLiveEvents(t *testing.T, live <-chan *nats.Msg, ids ...string) map[string]*chatimv1.Event {
	t.Helper()
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	got := make(map[string]*chatimv1.Event, len(ids))
	deadline := time.After(itLiveLimit)
	for len(got) < len(wanted) {
		select {
		case m := <-live:
			id := m.Header.Get(jetstream.MsgIDHeader)
			if !wanted[id] || got[id] != nil {
				continue
			}
			ev := &chatimv1.Event{}
			if err := proto.Unmarshal(m.Data, ev); err != nil {
				t.Fatalf("decode live event %s: %v", id, err)
			}
			got[id] = ev
		case <-deadline:
			t.Fatalf("live events %v did not all arrive within %v; got %v", ids, itLiveLimit, slices.Sorted(maps.Keys(got)))
		}
	}
	return got
}

func awaitStored[T any](t *testing.T, what string, read func() (T, error), done func(T) bool) T {
	t.Helper()
	deadline := time.Now().Add(itLiveLimit)
	for {
		got, err := read()
		if err != nil {
			t.Fatalf("read %s: %v", what, err)
		}
		if done(got) {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s = %+v after %v", what, got, itLiveLimit)
		}
		time.Sleep(itActivityPoll)
	}
}

func awaitCounts(t *testing.T, st *mongostore.Store, key store.MsgKey, want []domain.ReactionCount) domain.ReactionSummary {
	t.Helper()
	read := func() (domain.ReactionSummary, error) {
		got, err := st.Find(t.Context(), key.Room, []store.MsgKey{key})
		if err == nil && len(got) != 1 {
			err = fmt.Errorf("found %d messages for %+v", len(got), key)
		}
		if err != nil {
			return domain.ReactionSummary{}, err
		}
		return got[0].Reactions, nil
	}
	what := fmt.Sprintf("reactions of seq %d (want %+v)", key.Seq, want)
	return awaitStored(t, what, read, func(s domain.ReactionSummary) bool { return slices.Equal(s.Counts, want) })
}

func countsOf(emojis ...string) []domain.ReactionCount {
	by := map[string]uint32{}
	for _, e := range emojis {
		if e != "" {
			by[e]++
		}
	}
	out := make([]domain.ReactionCount, 0, len(by))
	for e, n := range by {
		out = append(out, domain.ReactionCount{Emoji: e, Count: n})
	}
	domain.SortReactionCounts(out)
	return out
}

func sameCounts(got *chatimv1.ReactionSummary, want []domain.ReactionCount) bool {
	return slices.EqualFunc(got.GetCounts(), want, func(g *chatimv1.ReactionCount, w domain.ReactionCount) bool {
		return g.GetEmoji() == w.Emoji && g.GetCount() == w.Count
	})
}

func pinnedSeqs(pins []*chatimv1.Pin) []uint64 {
	out := make([]uint64, len(pins))
	for i, p := range pins {
		out[i] = p.GetSeq()
	}
	return out
}
```

**Step 2: Test**

`apps/core/reaction_integration_test.go`:

```go
package main

import (
	"fmt"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const itReactors = 24

var itEmojis = []string{"👍", "🎉", "❤️"}

func itReactorNames() []string {
	users := []string{itUser}
	for i := 1; i < itReactors; i++ {
		users = append(users, "r"+strconv.Itoa(i))
	}
	return users
}

func reactAll(t *testing.T, client chatimv1.CoreServiceClient, roomID string, seq uint64, users []string, change uint32, emojiOf func(int) string) []domain.ReactionCount {
	t.Helper()
	emojis := make([]string, len(users))
	errs := make([]error, len(users))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, user := range users {
		emojis[i] = emojiOf(i)
		req := &chatimv1.ReactMessageRequest{RoomId: roomID, Seq: seq, Emoji: emojis[i]}
		wg.Go(func() {
			<-start
			resp, err := client.ReactMessage(callerAs(t.Context(), user), req)
			if err == nil && resp.GetChange() != change {
				err = fmt.Errorf("change %d, want %d", resp.GetChange(), change)
			}
			errs[i] = err
		})
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("%s reacts %q: %v", users[i], emojis[i], err)
		}
	}
	return countsOf(emojis...)
}

func TestRealInfraWorkersPublishReactionChangesWrittenOutsideTheCore(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)
	seq := sendAs(t, client, itUser, roomID, "react-a", "reacted outside the core")
	core.awaitTerm(t)

	st := itStore(it, core)
	key := store.MsgKey{Room: room, Seq: seq}
	steps := []struct {
		n           uint32
		emoji, prev string
	}{{1, "👍", ""}, {2, "🎉", "👍"}, {3, "", "🎉"}}
	for _, step := range steps {
		n := step.n
		at := time.Now().UTC().Truncate(time.Millisecond)
		var changed bool
		var err error
		if step.emoji == "" {
			_, changed, err = itReactions(st).Remove(t.Context(), key, "bob", at)
		} else {
			_, changed, err = itReactions(st).Set(t.Context(), domain.Reaction{Room: room, Seq: seq, Tenant: itTenant, User: "bob", Emoji: step.emoji, At: at})
		}
		if err != nil || !changed {
			t.Fatalf("change %d outside the core = %v, %v; want a change", n, changed, err)
		}
		reactionID, countsID := pbconv.ReactionEventID(room, 0, seq, "bob", n), pbconv.ReactionCountsEventID(room, 0, seq, uint64(n))
		evs := awaitLiveEvents(t, live, reactionID, countsID)
		if r := evs[reactionID].GetReactionChanged(); r.GetUser() != "bob" || r.GetEmoji() != step.emoji || r.GetPreviousEmoji() != step.prev || r.GetChange() != n {
			t.Fatalf("reaction_changed %d = %v, want %q after %q by bob", n, r, step.emoji, step.prev)
		}
		c := evs[countsID].GetCountsChanged()
		if c.GetCounter() != pbconv.ReactionsCounter || c.GetReactions().GetVersion() != uint64(n) || !sameCounts(c.GetReactions(), countsOf(step.emoji)) {
			t.Fatalf("counts_changed %d = %v, want the counts of %q at version %d", n, c, step.emoji, n)
		}
	}
	got, err := st.Find(t.Context(), room, []store.MsgKey{key})
	if err != nil || len(got) != 1 || len(got[0].Reactions.Counts) != 0 || got[0].Reactions.Version != 3 {
		t.Fatalf("summary after the removal = %+v, %v; want no counts at version 3", got, err)
	}
}

func TestRealInfraConcurrentReactionsConvergeToExactCounts(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	users := itReactorNames()
	roomID := createRoomWith(t, client, users)
	seq := sendAs(t, client, itUser, roomID, "react-c", "react to me")
	key := store.MsgKey{Room: parseRoom(t, roomID), Seq: seq}
	st := itStore(it, core)

	first := reactAll(t, client, roomID, seq, users, 1, func(i int) string { return itEmojis[i%len(itEmojis)] })
	before := awaitCounts(t, st, key, first)
	second := reactAll(t, client, roomID, seq, users, 2, func(i int) string {
		if i%4 == 0 {
			return ""
		}
		return itEmojis[(i+1)%len(itEmojis)]
	})
	after := awaitCounts(t, st, key, second)
	if before.Version == 0 || after.Version <= before.Version {
		t.Fatalf("rx.v went from %d to %d, want it above zero and rising", before.Version, after.Version)
	}
	facts, err := itReactions(st).Count(t.Context(), key, nil)
	if err != nil || !slices.Equal(facts, second) {
		t.Fatalf("Count = %+v, %v; want %+v", facts, err, second)
	}
	m := historyAs(t, client, "r1", roomID)[seq]
	if !sameCounts(m.GetReactions(), second) || m.GetReactions().GetVersion() < after.Version {
		t.Fatalf("history shows %v, want %+v at version %d or later", m.GetReactions(), second, after.Version)
	}
}
```

Dữ liệu (c): pha 1, 24 user chia đều 8 👍, 8 🎉, 8 ❤️ (mỗi user `change 1`); pha 2, user `i % 4 == 0` (6 người) gỡ, 18 người còn lại đổi sang emoji kế tiếp (luôn khác emoji cũ), mỗi user `change 2`. Mỗi user gửi tuần tự nên không có trùng khoá cùng user; tranh chấp chỉ ở CAS `rx.v` (touch inline ≤3 lần, lỗi bỏ qua) và worker `reaction_counter` sửa phần còn lại.

`apps/core/pin_integration_test.go`:

```go
package main

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestRealInfraWorkersProjectAPinFactWrittenOutsideTheCore(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)
	first := sendAs(t, client, itUser, roomID, "pin-b-1", "pinned outside the core")
	second := sendAs(t, client, itUser, roomID, "pin-b-2", "pinned through the core")
	core.awaitTerm(t)

	st := itStore(it, core)
	at := time.Now().UTC().Truncate(time.Millisecond)
	fact := domain.PinAction{Room: room, PV: 1, Tenant: itTenant, Op: domain.PinOpPin, Seq: first, By: itUser, At: at}
	if err := itPins(st).Append(t.Context(), fact); err != nil {
		t.Fatalf("append a pin outside the core: %v", err)
	}
	pinnedID := pbconv.PinEventID(room, 1)
	ev := awaitLiveEvents(t, live, pinnedID)[pinnedID]
	if p := ev.GetMessagePinned(); p.GetPinVersion() != 1 || p.GetMessage().GetSeq() != first || p.GetMessage().GetText() != "pinned outside the core" || ev.GetActor() != itUser {
		t.Fatalf("msg_pinned from the workers = %v, want seq %d at pin version 1 by %s", ev, first, itUser)
	}
	state, err := st.PinState(t.Context(), room)
	if err != nil || state.Version != 1 || len(state.Pins) != 1 || state.Pins[0].Seq != first || state.Pins[0].PV != 1 || !state.Pins[0].At.Equal(at) {
		t.Fatalf("projection = %+v, %v; want seq %d pinned at version 1", state, err, first)
	}

	resp, err := client.PinMessage(caller(t.Context()), &chatimv1.PinMessageRequest{RoomId: roomID, Seq: second})
	if err != nil || resp.GetPinVersion() != 2 || !slices.Equal(pinnedSeqs(resp.GetPins()), []uint64{second, first}) {
		t.Fatalf("PinMessage = %v, %v; want version 2 with the new pin first", resp, err)
	}
	unpin := &chatimv1.UnpinMessageRequest{RoomId: roomID, Seq: first}
	for attempt := range 2 {
		resp, err := client.UnpinMessage(caller(t.Context()), unpin)
		if err != nil || resp.GetPinVersion() != 3 || !slices.Equal(pinnedSeqs(resp.GetPins()), []uint64{second}) {
			t.Fatalf("UnpinMessage attempt %d = %v, %v; want version 3 with only seq %d", attempt+1, resp, err, second)
		}
	}
	unpinnedID := pbconv.PinEventID(room, 3)
	if u := awaitLiveEvents(t, live, unpinnedID)[unpinnedID].GetMessageUnpinned(); u.GetPinVersion() != 3 || u.GetMessage().GetSeq() != first {
		t.Fatalf("msg_unpinned = %v, want seq %d at pin version 3", u, first)
	}
}

func TestRealInfraPinLimitHoldsUnderConcurrentPins(t *testing.T) {
	it := realInfra(t)
	env := maps.Clone(itFastEffects)
	env["PIN_LIMIT"] = "3"
	core := startCore(t, it, env)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	seqs := make([]uint64, 6)
	for i := range seqs {
		seqs[i] = sendAs(t, client, itUser, roomID, "pin-d-"+strconv.Itoa(i+1), "pin candidate")
	}

	errs := make([]error, len(seqs))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, seq := range seqs {
		req := &chatimv1.PinMessageRequest{RoomId: roomID, Seq: seq}
		wg.Go(func() {
			<-start
			_, errs[i] = retryingUnavailable(caller(t.Context()), func(ctx context.Context) (*chatimv1.PinMessageResponse, error) {
				return client.PinMessage(ctx, req)
			})
		})
	}
	close(start)
	wg.Wait()
	pinned := 0
	for i, err := range errs {
		switch status.Code(err) {
		case codes.OK:
			pinned++
		case codes.FailedPrecondition:
		default:
			t.Fatalf("pin of seq %d = %v, want success or FailedPrecondition", seqs[i], err)
		}
	}
	if pinned != 3 {
		t.Fatalf("%d of %d concurrent pins succeeded, want exactly PIN_LIMIT 3", pinned, len(seqs))
	}
	st := itStore(it, core)
	facts, err := itPins(st).After(t.Context(), room, 0, store.MaxPinScan)
	if err != nil || len(facts) != 3 {
		t.Fatalf("pin facts = %+v, %v; want exactly 3", facts, err)
	}
	read := func() (domain.PinState, error) { return st.PinState(t.Context(), room) }
	state := awaitStored(t, "pins of the room", read, func(s domain.PinState) bool { return s.Version >= 3 })
	if state.Version != 3 || len(state.Pins) != 3 {
		t.Fatalf("projection = %+v, want 3 pins at version 3", state)
	}
}
```

(d) không phụ thuộc thời điểm: mỗi lệnh đọc trạng thái fold tới pv hiện tại rồi insert pv+1 (khoá là CAS), nên số fact không bao giờ vượt 3; lệnh thua trùng khoá 3 lần nhận `UNAVAILABLE` (`ErrRetryLater`) và client gửi lại (như route); mọi lệnh cuối cùng hoặc thành công hoặc gặp `ErrTooManyPins`. Projection có thể trễ khi `Project` của fast path trượt CAS (lỗi bỏ qua), nên chờ `pv ≥ 3` (worker `pin_projection` sửa với delay 0).

`apps/core/reaction_pin_access_integration_test.go`:

```go
package main

import (
	"maps"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestRealInfraMembersReactAndPinEvenOnLockedKinds(t *testing.T) {
	it := realInfra(t)
	env := maps.Clone(itFastEffects)
	env["MESSAGE_LOCKED_KINDS"] = "text"
	core := startCore(t, it, env)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	seq := sendAs(t, client, itUser, roomID, "access-e", "locked text")
	edit := &chatimv1.EditMessageRequest{RoomId: roomID, Seq: seq, Text: "not allowed"}
	if _, err := client.EditMessage(caller(t.Context()), edit); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("the author edits a locked kind = %v, want PermissionDenied (MESSAGE_LOCKED_KINDS active)", err)
	}

	bob := callerAs(t.Context(), "bob")
	if resp, err := client.ReactMessage(bob, &chatimv1.ReactMessageRequest{RoomId: roomID, Seq: seq, Emoji: "👍"}); err != nil || resp.GetChange() != 1 {
		t.Fatalf("bob reacts to alice's locked message = %v, %v; want change 1", resp, err)
	}
	if resp, err := client.PinMessage(bob, &chatimv1.PinMessageRequest{RoomId: roomID, Seq: seq}); err != nil || resp.GetPinVersion() != 1 {
		t.Fatalf("bob pins alice's locked message = %v, %v; want pin version 1", resp, err)
	}
	if resp, err := client.UnpinMessage(bob, &chatimv1.UnpinMessageRequest{RoomId: roomID, Seq: seq}); err != nil || resp.GetPinVersion() != 2 || len(resp.GetPins()) != 0 {
		t.Fatalf("bob unpins it = %v, %v; want pin version 2 and no pins", resp, err)
	}

	carol := callerAs(t.Context(), "carol")
	denied := map[string]func() error{
		"react": func() error {
			_, err := client.ReactMessage(carol, &chatimv1.ReactMessageRequest{RoomId: roomID, Seq: seq, Emoji: "👍"})
			return err
		},
		"remove": func() error {
			_, err := client.ReactMessage(carol, &chatimv1.ReactMessageRequest{RoomId: roomID, Seq: seq})
			return err
		},
		"pin": func() error {
			_, err := client.PinMessage(carol, &chatimv1.PinMessageRequest{RoomId: roomID, Seq: seq})
			return err
		},
		"unpin": func() error {
			_, err := client.UnpinMessage(carol, &chatimv1.UnpinMessageRequest{RoomId: roomID, Seq: seq})
			return err
		},
	}
	for name, call := range denied {
		if err := call(); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("carol (not a member) %s = %v, want PermissionDenied", name, err)
		}
	}
}

func TestRealInfraDeletedMessagesTakeNoNewReactionOrPin(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	seq := sendAs(t, client, itUser, roomID, "deleted-f", "soon deleted")
	bob := callerAs(t.Context(), "bob")
	if _, err := client.ReactMessage(bob, &chatimv1.ReactMessageRequest{RoomId: roomID, Seq: seq, Emoji: "👍"}); err != nil {
		t.Fatalf("ReactMessage: %v", err)
	}
	if _, err := client.PinMessage(caller(t.Context()), &chatimv1.PinMessageRequest{RoomId: roomID, Seq: seq}); err != nil {
		t.Fatalf("PinMessage: %v", err)
	}
	if _, err := client.DeleteMessage(caller(t.Context()), &chatimv1.DeleteMessageRequest{RoomId: roomID, Seq: seq}); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}

	if _, err := client.ReactMessage(bob, &chatimv1.ReactMessageRequest{RoomId: roomID, Seq: seq, Emoji: "🎉"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("react on a deleted message = %v, want FailedPrecondition", err)
	}
	if _, err := client.PinMessage(caller(t.Context()), &chatimv1.PinMessageRequest{RoomId: roomID, Seq: seq}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("pin of a deleted message = %v, want FailedPrecondition even while it is pinned", err)
	}
	removed, err := client.ReactMessage(bob, &chatimv1.ReactMessageRequest{RoomId: roomID, Seq: seq})
	if err != nil || removed.GetChange() != 2 || len(removed.GetReactions().GetCounts()) != 0 {
		t.Fatalf("remove on a deleted message = %v, %v; want change 2 and no counts", removed, err)
	}
	unpinned, err := client.UnpinMessage(caller(t.Context()), &chatimv1.UnpinMessageRequest{RoomId: roomID, Seq: seq})
	if err != nil || unpinned.GetPinVersion() != 2 || len(unpinned.GetPins()) != 0 {
		t.Fatalf("unpin of a deleted message = %v, %v; want pin version 2 and no pins", unpinned, err)
	}
	if m := historyAs(t, client, "bob", roomID)[seq]; !m.GetDeleted() || m.GetText() != "" || m.GetReactions() != nil {
		t.Fatalf("history shows %v, want a deleted placeholder without reactions", m)
	}
}
```

Ghi chú: `pbconv.ReactionSummary` trả `nil` khi `Version == 0` và không có số đếm, và `view.MaskDeleted` xoá `Reactions` (Task 12), nên tin xoá có `reactions` nil dù `rx` trong Mongo còn v2.

**Step 3: Biên dịch**

Run: `make -s go ARGS="vet ./apps/core/"` rồi `make -s go ARGS="test -race -shuffle=on ./apps/core/"`
Expected: vet sạch; test PASS (itest skip). Lỗi tên (`st.Reactions()`, `st.Pins()`, `st.PinState`, getter proto) nghĩa là part A/B đặt tên khác hợp đồng hoặc GAP-1 giải khác: chỉ sửa tên trong test (GAP-1: chỉ hai helper đầu file), ghi vào báo cáo. `wc -l apps/core/*reaction* apps/core/*pin*` → mỗi file < 200 (reaction_integration_test.go ~130, pin_integration_test.go ~120, reaction_pin_access_integration_test.go ~100, it_reactions_pins_test.go ~123).

**Step 4: Chạy trên hạ tầng thật**

Run: `make infra-up && make itest`
Expected: mọi package `ok`, gồm `TestRealInfraWorkersPublishReactionChangesWrittenOutsideTheCore`, `TestRealInfraConcurrentReactionsConvergeToExactCounts`, `TestRealInfraWorkersProjectAPinFactWrittenOutsideTheCore`, `TestRealInfraPinLimitHoldsUnderConcurrentPins`, `TestRealInfraMembersReactAndPinEvenOnLockedKinds`, `TestRealInfraDeletedMessagesTakeNoNewReactionOrPin`, drill `TestRealInfraResyncDrillRepublishesWritesTheReaderMissed` (Task 14), các itest M2b.2 (`sendRetrying` qua `retryingUnavailable`, hành vi y hệt), contract `storetest` Mongo của Task 4–6 (`RunReactions`, `RunPins`, `RunReactionFeed`, `RunPinFeed`).

Target `make itest` chạy cả repo một lần (`-count=1`, không nhận `ARGS`), nên không chạy lặp riêng (c) và (d); hai test này không phụ thuộc thời điểm (xem ghi chú ở dưới mỗi test). Nếu một trong hai fail ở lần chạy này hoặc ở Task 18 thì dừng và báo, ghi vào "Kết quả thực thi"; không sửa Makefile.

**Step 5: INDEXES.csv + commit**

Dòng `apps/core`, cột tests: ngay sau mục `itest (hide and clear apply only to the reader and publish nothing)` thêm `;itest (workers publish reaction_changed and counts_changed for reaction inserts, updates and removals written straight to Mongo);itest (concurrent reactions from 24 users converge to exact counts and rx.v rises);itest (workers project a pin fact written straight to Mongo and publish msg_pinned; later pins and a repeated unpin continue from it);itest (PIN_LIMIT holds exactly under concurrent pins);itest (members react and pin even on locked kinds, non-members are denied; a deleted message takes no new reaction or pin)`. Kiểm 7 cột.

```bash
make fmt-check && make vet && make lint
git add apps/core/reaction_integration_test.go apps/core/pin_integration_test.go apps/core/reaction_pin_access_integration_test.go
git commit -m "test(core): cover reactions and pins end to end on real infra" -- apps/core/send_retry_test.go apps/core/it_reactions_pins_test.go apps/core/reaction_integration_test.go apps/core/pin_integration_test.go apps/core/reaction_pin_access_integration_test.go INDEXES.csv
```

---

### Task 17: Docs

Task docs, rủi ro thấp: controller kiểm nhanh, không reviewer. Không đổi code Go hay luật alert (luật thứ 16 đã thêm ở Task 13).

**Step 0: Dừng nếu cây làm việc còn thay đổi chưa commit của người khác**

```bash
git status --porcelain -- CLAUDE.md README.md INDEXES.csv docs/designs/261005-chatim-architecture.md docs/roadmap.md docs/plans/2026-10-06-m2b3-reactions-pins.md
```

Expected: không in gì. Có dòng nào → **dừng, hỏi controller**; không commit chung thay đổi của người khác, không `git stash`/`checkout` chúng.

**Files:**
- Modify: `docs/designs/261005-chatim-architecture.md`
- Modify: `docs/roadmap.md`
- Modify: `README.md`
- Modify: `CLAUDE.md`
- Modify: `INDEXES.csv`
- Modify: `docs/plans/2026-10-06-m2b3-reactions-pins.md` (mục "Kết quả thực thi")

**Step 1: Kiểm luật alert (không sửa)**

```bash
grep -n "ChatimCounterRepairSurge\|counter_repaired_total" deploy/prometheus/alerts.yml
make alerts-check
```

Expected: luật `ChatimCounterRepairSurge` với `sum by (counter) (rate(chatim_core_counter_repaired_total[5m])) > 1`, `for: 10m`; `SUCCESS: 16 rules found`. Khác → dừng, báo (Task 13 chưa xong).

**Step 2: Thiết kế**

`docs/designs/261005-chatim-architecture.md`:

- **§3**, dòng P7: `` `sys:{event_id}`; core không tự sinh `` → `` `sys-{event_id}` (`ValidCID` không nhận `:`, D93); core không tự sinh ``.
- **§4**, bảng lớp:
  - Dòng `Fact bất biến`: `Lệnh tạo dùng cid; lệnh đổi mang \`base_ver\`` → `Lệnh tạo dùng cid; lệnh sửa/xoá mang \`base_ver\`; ghim không mang \`base_pv\`, server ghi pv hiện tại + 1 (D92)`.
  - Dòng `State có version (projection)`: `Effect \`set … where ver < v\` từ fact` → `Effect \`set … where ver < v\` từ fact (ghim: fold fact sau \`pv\` + CAS \`pv == p\`, D92)`.
  - Thay cả dòng `Tập (target, user)` bằng:

```markdown
| Tập (target, user) | reaction (một emoji mỗi (user, tin), D88), thread_subs, bookmark | Upsert có điều kiện theo khoá unique + change number `n`; gỡ để lại tombstone (D89) | Id `{target}-{user}-n{n}` (reaction: `{room}-{th}-{seq}-{user}-n{n}`, D93); feed insert/update/replace, doc hiện tại thắng | Worker phát lại doc hiện tại (`reaction_event`) |
```

  - Thay cả dòng `Aggregate theo target` bằng:

```markdown
| Aggregate theo target | số reaction theo emoji, `thread_count`, `member_count` | Recount CAS-ver với witness (§7, D90) | `counts_changed` id `{target}-{counter}-v{ver}` (reaction: `{room}-{th}-{seq}-reactions-v{v}`, D93) | Touch của worker (`reaction_counter`) |
```

- **§5**, bảng collection:
  - Dòng `messages`: cột Lớp `Fact (tạo) + projection (sửa/xoá)` → `Fact (tạo) + projection (sửa/xoá) + aggregate (\`rx\`)`; cột Trường chính `reaction summary \`{n, ver}\`` → `\`rx {c: [{e, n}], v}\` (số reaction theo emoji, mảng sắp theo n giảm rồi e, D90)`; cột Trạng thái → `Đã xây (tạo; sửa/xoá \`v/d/ea\`, M2b.2; \`rx\`, M2b.3)`.
  - Thay cả dòng `pin_actions` bằng:

```markdown
| `pin_actions` (clustered) | 16B `room│pv` | Fact | `r`, `t`, `op` (1 ghim, 2 bỏ ghim), `th`, `s`, `by`, `ts`; pv dày (D92) | `{r, ts}` (D70) | Đã xây (M2b.3) |
```

  - Dòng `rooms`: cột Trường chính `pins + pv` → `pins \`[{th, s, by, ts, pv}]\` + \`pv\` (fold + CAS, D92; \`Rooms.Get\` không đọc)`; cột Trạng thái → `Đã xây (tạo; activity ls/lm/lc/ab, M2b.1; pins/pv, M2b.3)`.
  - Thay cả dòng `reactions` bằng:

```markdown
| `reactions` (clustered) | `k│u` (24B khoá tin + byte user) | Tập | `k`, `r`, `t`, `u`, `e` (`""` = đã gỡ), `pe` (emoji trước), `n`, `ts` | `{k, e}` (đếm phủ index), `{r, ts}` (resync) (D88) | Đã xây (M2b.3) |
```

- **§5.1**:
  - Dòng đầu: `Collection lớn (\`messages\`, \`message_edits\`, \`pin_actions\`)` → `Collection lớn (\`messages\`, \`message_edits\`, \`pin_actions\`, \`reactions\`)`.
  - Thay cả dòng `| \`reactions\` shard key \`{k: 1}\`, unique \`{k, emoji, u}\` | …` bằng:

```markdown
| `reactions` clustered, `_id = k│u`, shard key `{_id: 1}` (D88, thay D68); index `{k, e}` và `{r, ts}` | `_id` bắt đầu bằng khoá tin nên reaction của một tin là một range trên một shard; `_id` unique toàn cluster nên không cần unique index phụ có shard key làm prefix |
```

- **§6.3**:
  - Tiêu đề `### 6.3 Lệnh đổi: fact + projection [Đã xây sửa/xoá, M2b.2; ghim ở M2b.3]` → `### 6.3 Lệnh đổi: fact + projection [Đã xây sửa/xoá M2b.2, ghim M2b.3]`.
  - Bước 1 `1. Lệnh mang \`base_ver\` (ghim: \`base_pv\`) là version client đang thấy.` → `1. Lệnh sửa/xoá mang \`base_ver\` là version client đang thấy. Ghim/bỏ ghim **không** mang \`base_pv\` (D92): server đọc trạng thái ghim hiện tại rồi ghi pv + 1.`
  - Bước 2: `Ràng buộc (≤50 pin)` → `Ràng buộc ghim (\`PIN_LIMIT\`, mặc định 50)`.
  - Thay bước 7 bằng: `7. Ghim: fact \`pin_actions {room│pv}\` với pv = pv hiện tại + 1 (dày) → projection \`rooms.pins\` + \`pv\` bằng fold fact sau \`pv\` + CAS \`pv == p\` (D92, thay \`where pv < v\`). "X đã ghim" sinh từ fact (A ghim rồi B bỏ ghim vẫn ra đủ hai thông báo).`
  - Thêm đoạn ngay sau đoạn `**Đã xây (M2b.2, D82–D84, D86, D87):** …`:

```markdown
**Đã xây (M2b.3, D92, D94):** `PinMessage`/`UnpinMessage` → `mutate.Pin`/`Unpin`: `Admit` (`pin_message`/`unpin_message`) → `Find` tin → `Allow` (mặc định mọi member, `MESSAGE_LOCKED_KINDS` không áp) → ghim tin đã xoá → `FAILED_PRECONDITION` (bỏ ghim vẫn được; xoá tin không đụng ghim) → tối đa 3 lượt: `pinproj.Projector.Current` (đọc `rooms.pins/pv`, quét `pin_actions` sau `pv` theo trang 1000, `domain.FoldPins`) → đã đúng trạng thái (ghim tin đã ghim, bỏ ghim tin chưa ghim) → trả state, không fact, không event → ghim khi đã có `PIN_LIMIT` ghim → `FAILED_PRECONDITION` (`ErrTooManyPins`; chính xác vì pv dày và kiểm trên state đúng pv−1) → `Pins.Append` fact pv+1 → trùng khoá: fact ở pv cùng op/tin/người thì là kết quả (retry), khác thì lượt sau; hết lượt → `UNAVAILABLE` (`ErrRetryLater`) → `Projector.Project(room, pv)` (fold + CAS `pv == p`, ≤5 lượt; lỗi → trả fold cục bộ, worker `pin_projection` sửa) → enqueue `msg_pinned`/`msg_unpinned` (id `{room}-p{pv}`, snapshot tin, tin xoá không text) → trả `pin_version` + danh sách ghim (mới nhất trước). `Rooms.Get` (chạy ở mọi `access.Admit`) không đọc `pins`; `PinState` đọc riêng bằng projection `{pins, pv}`. Ngân sách: fast path 3 read + 1 read `rooms.pins` + 1 range scan fact sau pv + 1 insert + 1 CAS majority + 1 event; worker 1 read + ≤1 CAS mỗi room mỗi lô.
```

- **§6.4**:
  - Tiêu đề `### 6.4 Tập và vị trí đọc [Đã xây ẩn + clear, M2b.2; còn lại chưa]` → `### 6.4 Tập và vị trí đọc [Đã xây ẩn + clear M2b.2, reaction M2b.3; còn lại chưa]`.
  - Thay gạch `- Reaction: upsert \`{k, emoji, u}\` với \`n\` tăng mỗi lần bật/tắt; event \`reaction_changed\` id \`{k}-{u}-{emoji}-n{n}\`; touch counter của target (§7).` bằng:

```markdown
- Reaction [Đã xây, M2b.3, D88, D89, D93]: một emoji cho mỗi (user, tin); emoji khác thay emoji cũ, emoji rỗng là gỡ ("nhiều trên một user" là reply/mention, M2c). Doc `reactions {_id: k│u, k, r, t, u, e, pe, n, ts}`. Đặt = `FindOneAndUpdate({_id, e: {$ne: emoji}}, pipeline {n: ifNull(n, 0) + 1, pe: e cũ, e: emoji, ts}, upsert, sau ghi)`; trùng khoá (Mongo không tự retry upsert có filter không thuần bằng) → đọc lại: cùng emoji là no-op, khác thì thử lại ≤3 rồi `UNAVAILABLE`. Gỡ = cùng dạng, filter `e ≠ ""`, không upsert, để tombstone `e: ""` giữ `n` (không bao giờ chèn tombstone khi chưa có doc). Đúng trạng thái sẵn → không ghi, không event, không touch. Không cid: lệnh là trạng thái mong muốn, retry trễ có thể đưa về trạng thái cũ (chấp nhận cho lớp tập). `ValidateEmoji`: UTF-8 hợp lệ, 1–32 byte, không ký tự điều khiển; tối đa `REACTION_MAX_EMOJIS` (20) loại emoji mỗi tin, mềm (đọc từ `rx`, hai lệnh đồng thời có thể vượt một chút). Tin đã xoá: đặt → `FAILED_PRECONDITION`, gỡ vẫn được. Quyền `react_message` (mặc định mọi member, D94). Sau khi ghi: enqueue `reaction_changed` (id `{room}-{th}-{seq}-{u}-n{n}`, payload `{user, emoji, previous_emoji, change}`) → touch counter inline (§7) → enqueue `counts_changed` nếu `v` tăng → trả `{change, reactions}`. Worker `reaction_event` (delay `RECONCILE_DELAY`, không ack mark): `Get` doc; `n == rec.n` → phát doc hiện tại, `n > rec.n` → bỏ (record mới hơn lo), `n < rec.n` → `ErrStaleRead`, Nak. `GetHistory` chỉ trả số đếm (`Message.reactions`); emoji của chính người đọc để M3 (`GetReactions`).
```

- **§7**:
  - Tiêu đề `## 7. Counter theo target [Chưa xây]` → `## 7. Counter theo target [Đã xây cho reaction, M2b.3]`.
  - Thêm đoạn ngay sau mục 7 (`7. Single-writer theo \`hash(target)\` …`):

```markdown
**Đã xây (M2b.3, D90, tinh chỉnh D67):** summary `messages.rx {c: [{e, n}], v}` là **mảng** (emoji như `$x`, `a.b` không được làm tên field), sắp theo `n` giảm rồi `e` (`domain.SortReactionCounts`); `rx` không có = `v` 0. Touch (`counter.Toucher.Touch`, dùng chung cho fast path và worker): (1) **witness** thay `afterClusterTime` — `Reactions.Count(key, witnesses)` đọc doc của từng witness (`{user, n}`) bằng majority trong một causal session, doc có `n < N` → `store.ErrStaleRead`; (2) aggregate `{$match: {k, e: {$gt: ""}}}, {$group: {_id: "$e", n: {$sum: 1}}}` phủ index `{k, e}` trong cùng session (tự mang `afterClusterTime` của lần đọc witness); (3) bằng summary hiện tại → không ghi, không bump `v`; (4) khác → `SetReactions` CAS `rx.v == k` (`$exists: false` khi k = 0), trượt → `Find` lại rồi lượt sau. Fast path touch inline **trước** khi trả lời (unary handler chỉ trả khi return) với `FastTouchTries` 3, lỗi bỏ qua (client nhận summary đang có, worker sửa). Worker `reaction_counter` (delay `REACTION_COUNT_DELAY` 1s, ≤ `RECONCILE_DELAY`): gom record theo tin trong lô fetch, witness = `max n` của mỗi user, một touch mỗi tin mỗi lô (`DefaultCounterTries` 3), bump → `counter_repaired_total{counter="reactions"}`, `v > 0` → phát lại `counts_changed` của `v` hiện tại (stream bỏ trùng với fast path). Witness không đi qua port driver nào nên đúng cả sau failover (thiếu thì Nak, không ghi số sai). Cửa sổ gom W và bucket `hash(u) % K` (mục 6) **để milestone Channel**: target nóng (post nhận R react/s) đếm lại O(số reaction của tin) mỗi touch, bắt buộc trước khi channel go-live.
```

- **§8.2**: `lọc insert của \`messages\`, \`rooms\` và \`message_edits\`` → `lọc insert của \`messages\`, \`rooms\`, \`message_edits\`, \`reactions\`, \`pin_actions\` và update/replace của \`reactions\` (không \`updateLookup\`; update giải mã \`documentKey._id\` + \`updatedFields.n\`, D91)`; `(\`work.Record\`, chỉ khoá + version + \`CommittedAt\`, 37 byte, D80, D84)` → `(\`work.Record\`, chỉ khoá + version + \`CommittedAt\`, 37 byte; \`ReactionChanged\` thêm đuôi \`len + user\`, 38–102 byte; D80, D84, D91)`; `(\`m:{room}-{thread}-{seq}\`, \`r:{room}\`, \`e:{room}-{thread}-{seq}-v{ver}\`)` → `(\`m:{room}-{thread}-{seq}\`, \`r:{room}\`, \`e:{room}-{thread}-{seq}-v{ver}\`, \`x:{room}-{thread}-{seq}-{user}-n{n}\`, \`p:{room}-p{pv}\`)`. Thêm câu cuối đoạn: `Record đúng dạng nhưng kind lạ (core cũ gặp record của core mới) bị \`Nak\` (\`WORK_RETRY_DELAY\`, đếm vào \`work_failures_total\`) thay vì \`Term\`; rolling deploy vẫn phải nâng mọi core trước khi có record mới (D91).`
- **§8.3**:
  - Tiêu đề `### 8.3 Effect engine [Đã xây phần M2b.1, M2b.2]` → `### 8.3 Effect engine [Đã xây phần M2b.1, M2b.2, M2b.3]`.
  - Bảng chính sách, dòng `delay`: `counter vài giây` → `\`reaction_counter\` = \`REACTION_COUNT_DELAY\` (1s, ≤ \`RECONCILE_DELAY\`); projection ghim 0`. Dòng `coalesce`: `counter W` → `counter: một touch mỗi tin mỗi lô fetch (W/bucket để milestone Channel)`.
  - Topo (a) mục 1: `\`Nats-Msg-Id\` = id tự nhiên của fact (\`{coll}:{_id}\`; tập dùng \`{coll}:{_id}:n{n}\`)` → `\`Nats-Msg-Id\` = id record (\`m:\`, \`r:\`, \`e:\`, \`x:\`, \`p:\`; §8.2)`.
  - Bảng registry: thêm sáu dòng ngay sau dòng `   | \`EditInserted\` | \`msg_changed\` | …`:

```markdown
   | `ReactionChanged` | `room_activity` | 0 | — | `Activity{Seq: 0}` (mọi kind trừ `MessageInserted`, D91): chỉ nâng `lc`/`ab` |
   | `ReactionChanged` | `reaction_counter` | `REACTION_COUNT_DELAY` | không | gom theo tin, witness = `max n` mỗi user; một touch (§7) mỗi tin mỗi lô; bump → `counter_repaired_total`; `v > 0` → phát lại `counts_changed` của `v` hiện tại; tin/room không còn → drop có đếm |
   | `ReactionChanged` | `reaction_event` | `RECONCILE_DELAY` | không | `Get` doc `(k, u)`: `n == rec.n` → `reaction_changed` của doc hiện tại; `>` bỏ; `<` → Nak; doc/room không còn → drop có đếm |
   | `PinInserted` | `room_activity` | 0 | — | như trên |
   | `PinInserted` | `pin_projection` | 0 | — | mỗi room một lần mỗi lô: `Project(room, max pv)` (fold + CAS `pv == p`); room không còn → drop có đếm |
   | `PinInserted` | `pin_event` | `RECONCILE_DELAY` | không | `At` fact + `Find` tin + loại room → `msg_pinned`/`msg_unpinned` (snapshot, tin xoá không text); fact/tin/room không còn → drop có đếm |
```

  - Đoạn **Resync (D69)**: thay `; \`pin_actions\` ở M2b.3.` bằng `. M2b.3: tiếp đó quét \`reactions\` (doc hiện tại, kể cả tombstone, record \`ReactionChanged\` với \`n\` của doc) rồi \`pin_actions\` (record \`PinInserted\`, \`Seq = pv\`) cùng cách phân trang (\`ErrReactionPageFull\`, \`ErrPinPageFull\`).`; `Fact sửa cũng chạy \`room_activity\`` → `Fact sửa, reaction và ghim cũng chạy \`room_activity\``.
- **§12**:
  - Câu đầu `(15 luật)` → `(16 luật)`.
  - Thay cả dòng PJ1 bằng:

```markdown
| PJ1 | Projection cuối cùng khớp fact cuối | Worker chạy lại projection từ mọi fact: `edit_projection` (CAS `v < ver`, M2b.2), `pin_projection` (fold + CAS `pv == p`, M2b.3); lỗi → retry qua work stream; fact/room không đọc được → drop có đếm | `work_failures_total`, `effect_dropped_total{effect}` với `edit_projection`, `msg_changed`, `pin_projection`, `pin_event`; `reconcile_republished_total{effect="pin_event"}`; alert `ChatimWorkFailing`, `ChatimEffectDropping` |
```

  - Thêm dòng mới ngay sau PJ1:

```markdown
| CT1 | Summary counter cuối cùng bằng số fact (reaction: số doc `e ≠ ""` theo emoji); `v` chỉ tăng khi số đổi | Worker `reaction_counter` recount mỗi tin mỗi lô sau mỗi fact (witness: lần đếm thấy mọi write của lô); fast path lỡ (CAS hết lượt, witness cũ, core chết giữa fact và touch) thì worker sửa và đếm; tăng vọt nghĩa là touch inline hỏng hệ thống | `counter_repaired_total{counter}`, `effect_dropped_total{effect="reaction_counter"}`, `reconcile_republished_total{effect="reaction_event"\|"reaction_counter"}`, `work_failures_total`; alert `ChatimCounterRepairSurge` (> 1/s trong 10m), `ChatimWorkFailing` |
```

- **§14**: dòng `| Core chết giữa fact và projection | … không retry thì worker \`edit_projection\` sửa ngay khi record tới (delay 0) |` → `… không retry thì worker \`edit_projection\`/\`pin_projection\` sửa ngay khi record tới (delay 0); reaction: worker \`reaction_counter\` đếm lại sau \`REACTION_COUNT_DELAY\` |`.
- **§16**: dòng `| Chi phí recount target nóng | Trung bình | W, bucket K, đo key/s |` → `| Chi phí recount target nóng | Trung bình | W, bucket K (để milestone Channel, bắt buộc trước khi channel go-live, D90), đo key/s |`.
- **§17.2**:
  - Dòng D63: cuối cột Quyết định thêm ` (phần \`base_pv\` thay bởi D92)`.
  - Dòng D64: `ràng buộc (≤50 pin) kiểm trước khi commit fact` → `ràng buộc (≤50 pin, nay \`PIN_LIMIT\`) kiểm trước khi commit fact`.
  - Dòng D67: cuối cột Quyết định thêm ` (tinh chỉnh bởi D90: witness thay \`afterClusterTime\`)`.
  - Dòng D68: cột Quyết định `Unique index reaction \`{k, emoji, u}\`` → `~~Unique index reaction \`{k, emoji, u}\`~~ **Thay bởi D88** (M2b.3: một emoji mỗi (user, tin), \`_id = k│u\`)`.
  - Thêm bảy dòng sau dòng `| D87 | …`:

```markdown
| D88 | Reaction một emoji cho mỗi (user, tin): collection clustered `reactions`, `_id = k│u` (khoá tin 24B + byte user), trường `k r t u e pe n ts`, shard key tương lai `{_id: 1}`, index `{k, e}` (đếm theo emoji phủ index) và `{r, ts}` (resync); thay D68 | `{k, emoji, u}` unique trên ObjectId (D68, nhiều emoji mỗi user); shard `{k: 1}` | Owner chốt 2026-10-06: một emoji mỗi (user, tin), "nhiều trên một user" là reply/mention (M2c). Khoá `k│u` làm chính `_id` nên là CAS, không cần unique index phụ; `_id` bắt đầu bằng khoá tin nên reaction của một tin là một range trên một shard và unique toàn cluster |
| D89 | Đặt reaction = upsert có điều kiện `e ≠ emoji` (pipeline `n+1`, `pe` = emoji cũ), trả doc sau ghi; trùng khoá → đọc lại: cùng emoji là no-op, khác thì thử lại ≤3 rồi `UNAVAILABLE`; gỡ = update không upsert, filter `e ≠ ""`, để tombstone `e: ""`; đúng trạng thái sẵn → không ghi, không event; không cid; `ValidateEmoji` (UTF-8, 1–32 byte, không ký tự điều khiển); `REACTION_MAX_EMOJIS` (20) là giới hạn mềm đọc từ `rx`; tin đã xoá chỉ cho gỡ | Toggle theo emoji; cid cho reaction; chèn tombstone khi chưa có doc; giới hạn emoji chính xác | Lệnh là trạng thái mong muốn nên retry an toàn; Mongo không tự retry upsert có filter không thuần bằng nên trùng khoá phải đọc lại; retry trễ có thể đưa về trạng thái cũ, chấp nhận cho lớp tập (người dùng thấy và bấm lại), cid tốn Redis mà không bỏ được ABA; tombstone giữ `n` để id event không lặp; giới hạn chính xác cần tuần tự hoá trên tin nóng |
| D90 | Summary `messages.rx {c: [{e, n}], v}` (mảng, sắp n giảm rồi e); touch = witness (doc của user có `n ≥ N`, majority, causal session) → aggregate phủ `{k, e}` cùng session → bằng thì bỏ (không bump `v`) → khác thì CAS `rx.v == k` (`$exists: false` khi 0); fast path touch inline trước khi trả lời (≤3 CAS, lỗi bỏ qua); worker `reaction_counter` một touch mỗi tin mỗi lô sau `REACTION_COUNT_DELAY` (1s), phát lại `counts_changed` của `v` hiện tại, đếm `counter_repaired_total`; cửa sổ gom W và bucket K để milestone Channel. Tinh chỉnh D67 | `afterClusterTime` truyền qua port (D67 gốc); map `emoji → n`; `$inc`; chỉ touch ở worker | Cluster time không đi qua port mà không lộ kiểu driver, record work chỉ có wall time; witness chứng minh lần đếm đã thấy write, đúng cả sau failover (thiếu thì `ErrStaleRead`, Nak); emoji có thể là `$x`, `a.b` nên không làm tên field; touch inline để người bấm thấy số mới trong reply; worker bảo đảm hội tụ (§7.5) |
| D91 | Feed thêm insert của `reactions`/`pin_actions` và update/replace của `reactions` (update: `documentKey._id` + `updatedFields.n`; thiếu/sai → change hỏng, drop có đếm); kind `ReactionChanged` (4), `PinInserted` (5); `work.Record` thêm đuôi `len(1) + user` chỉ cho `ReactionChanged` (`Version = n`), id `x:`/`p:`; record đúng dạng nhưng kind lạ → Nak có đếm (không Term); `room_activity` để `Seq = 0` cho mọi kind trừ `MessageInserted`; rolling deploy phải nâng mọi core trước khi có record mới (prod chưa live) | `updateLookup`; record riêng cho reaction; đẩy doc đầy đủ vào work stream | `updateLookup` thêm một read mỗi update và trả doc lúc đọc chứ không phải bản của change; worker đọc doc hiện tại nên chỉ cần khoá + `n`; đuôi user giữ record 37 byte cũ đọc được; Nak kind lạ để core cũ không xoá record của core mới; `Seq` của reaction/ghim mà lọt vào `room_activity` sẽ ghi đè `ls/lm` |
| D92 | Ghim: bỏ `base_pv`; server đọc state (fold `rooms.pins/pv` với fact sau pv) rồi insert fact pv+1 (pv dày, khoá `room│pv` là CAS); đúng trạng thái sẵn = thành công, không fact, không event; `PIN_LIMIT` (50, 1..1000) chính xác; trùng khoá → fact ở pv cùng op/tin/người là kết quả, khác thì thử lại ≤3 rồi `ErrRetryLater`; ghim tin đã xoá → `FAILED_PRECONDITION`, bỏ ghim vẫn được, xoá tin không đụng ghim; projection `rooms.pins` + `pv` = fold + CAS `pv == p` (package `pinproj`, dùng chung fast path và worker `pin_projection`), thay `pv < v`; `Rooms.Get` không đọc pins. Thay phần ghim của D63/D64 | `base_pv` từ client; `$push`/`$pull` + `pv < v`; kiểm giới hạn trên projection | Owner chốt 2026-10-06: client không cần biết pv; fold + CAS đúng mọi thứ tự fact và khi core chết giữa fact và projection; pv dày nên giới hạn chính xác không cần khoá; `Rooms.Get` chạy ở mọi `access.Admit` nên không mang danh sách ghim |
| D93 | Event: `reaction_changed` `{room}-{th}-{seq}-{u}-n{n}` `{user, emoji, previous_emoji, change}`; `counts_changed` `{room}-{th}-{seq}-reactions-v{v}` `{counter: "reactions", reactions}`; `msg_pinned`/`msg_unpinned` `{room}-p{pv}` `{message (tin xoá không text), pin_version}`; không ack mark (worker gửi lại, stream bỏ trùng, chỉ đếm republish khi PubAck không phải bản trùng); `Message.reactions` trong `GetHistory` chỉ là số đếm, `view` che ở tin xoá/ẩn; cid SysMsg `sys-{event_id}` (sửa P7) | `{k}-{u}-{emoji}-n{n}` (§6.4 cũ); ack mark cho event mới; emoji của người đọc trong `GetHistory`; cid `sys:{event_id}` | User `[A-Za-z0-9_-]{1,64}` nên id đọc một cách (ba số từ trái, `n\d+` từ phải) và không trùng `-v{ver}`, `-created`, `-p{pv}`, `-reactions-v{v}`; lưu lượng thấp nên không cần không gian mark; emoji của người đọc cần query theo user (M3 `GetReactions`); `ValidCID` không nhận `:` |
| D94 | Action `react_message`, `pin_message`, `unpin_message`; `DefaultPolicy` cho mọi member (không cần là tác giả); `MESSAGE_LOCKED_KINDS` (D87) không áp; thứ tự `Admit` → `Find` → `Allow` (`Author`, `Kind`) | Chỉ owner/tác giả được ghim; khoá loại tin áp cả reaction/ghim | Owner chốt 2026-10-06, như D86: luật owner/moderator thuộc module policy chat (Phase 2) |
```

**Step 3: Roadmap**

`docs/roadmap.md`:
- Dòng đầu: `Cập nhật: 2026-10-05 (viết lại sau 2 vòng phản biện cơ chế hệ thống)` → `Cập nhật: 2026-10-06 (M2b.3 xong; viết lại 2026-10-05 sau 2 vòng phản biện cơ chế hệ thống)`; phần link sau giữ nguyên.
- Thay cả dòng bắt đầu bằng `| 1 | M2b.3 — Reaction + ghim |` bằng:

```markdown
| 1 | M2b.3 — Reaction + ghim | Tập reaction một emoji mỗi (user, tin), `reactions {_id: k│u}` + `n` (D88, D89, thay D68); counter recount CAS-ver với witness (D90, tinh chỉnh D67); `pin_actions` pv dày, bỏ `base_pv`, `PIN_LIMIT` chính xác (D92); event `reaction_changed`/`counts_changed`/`msg_pinned`/`msg_unpinned`, cid SysMsg `sys-{event_id}` (D93); quyền member (D94) | ✅ `dev-done` (trên `feat/m2b`, chưa merge `main`) — [plan](plans/2026-10-06-m2b3-reactions-pins.md); 3 RPC, `Message.reactions` trong `GetHistory`, package `counter` + `pinproj`, feed update/replace của `reactions`, 4 effect mới, `counter_repaired_total` + 16 luật alert, resync quét `reactions` + `pin_actions` (D88–D94) |
```

- Dòng M2b.4: cột Trạng thái `Chưa` → `⏭ Tiếp theo — cần plan`.
- Dòng M5: `mục mang sang từ M2b.0, M2b.1 và M2b.2 (xem dưới)` → `mục mang sang từ M2b.0, M2b.1, M2b.2 và M2b.3 (xem dưới)`.
- Dòng Channel: `counter lớn (bucket)` → `counter lớn (cửa sổ gom W + bucket \`hash(u) % K\` cho reaction, D90; bắt buộc trước khi channel go-live)`.
- Mục "Mục mang sang M5 (hardening)", sau khối "Từ M2b.2 …" thêm:

```markdown
Từ M2b.3, chi tiết ở [plan, mục Kết quả thực thi](plans/2026-10-06-m2b3-reactions-pins.md#kết-quả-thực-thi):

- Target nóng: mỗi touch đếm lại O(số reaction của tin) và các CAS `rx.v` tranh nhau; cửa sổ gom W + bucket `hash(u) % K` để milestone Channel (D90).
- Rolling deploy: core cũ `Term` record 38+ byte và record kind 5 (`PinInserted`); nâng mọi core trước khi có record mới (D91).
- `ReactMessage` không có cid: retry trễ có thể đưa reaction về trạng thái cũ (D89).
- Giới hạn emoji `REACTION_MAX_EMOJIS` là mềm: hai lệnh đồng thời có thể vượt một chút (D89).
- Resync: `ErrReactionPageFull`/`ErrPinPageFull` khi một thời điểm đầy một trang 1000 (như `ErrEditPageFull`).
- (các Minor controller ghi ở mục Kết quả thực thi)
```

**Step 4: README**

`README.md`:
- Dòng "Trạng thái": thay `` `/app resync`) và M2b.2 (sửa/xoá theo fact `message_edits` + projection, ẩn/clear phía người đọc, lịch sử sửa) xong trên nhánh `feat/m2b`; tiếp theo là M2b.3 theo `` bằng `` `/app resync`), M2b.2 (sửa/xoá theo fact `message_edits` + projection, ẩn/clear phía người đọc, lịch sử sửa) và M2b.3 (reaction một emoji mỗi user + số đếm recount, ghim theo fact `pin_actions` + projection) xong trên nhánh `feat/m2b`; tiếp theo là M2b.4 theo ``.
- Bảng "Kiến trúc", dòng `core`: `sửa/xoá tin là fact \`message_edits\` + projection \`messages\`, ẩn/clear theo người đọc; reader đọc change stream (\`messages\`, \`rooms\`, \`message_edits\`) đẩy record vào work stream, worker ở mọi core chạy effect (event bù, room activity, projection sửa/xoá)` → `sửa/xoá tin là fact \`message_edits\` + projection \`messages\`, ẩn/clear theo người đọc; reaction (một emoji mỗi user, số đếm theo emoji recount CAS), ghim là fact \`pin_actions\` + projection \`rooms.pins\`; reader đọc change stream (\`messages\`, \`rooms\`, \`message_edits\`, \`reactions\`, \`pin_actions\`) đẩy record vào work stream, worker ở mọi core chạy effect (event bù, room activity, projection sửa/xoá/ghim, đếm lại reaction)`.
- "Cấu trúc": `internal/{actor,mutate,view,access,flush,` → `internal/{actor,mutate,counter,pinproj,view,access,flush,`.
- "Lệnh hay dùng": dòng `make e2e` đổi chú thích thành `# build tools/corecli, chạy scripts/e2e.sh (route theo slot, kill core-1, kiểm tra; phase 3 sửa seq 1, xoá seq 2; phase 4 react seq 3 rồi đổi emoji, ghim seq 4)`; thêm sau dòng `docker run … edit -room ID …`:

```
    docker run --rm --network chatim_default -e REDIS_PASSWORD chatim/corecli:dev react -room ID -seq N -emoji "👍"   # đặt/đổi reaction của người gọi (-emoji "" gỡ); pin -room ID -seq N, unpin -room ID -seq N; -tenant/-user chọn người gọi
```

- Đoạn `/app resync`: `Quét timeline chính rồi \`message_edits\` của từng room (theo \`{r, ts}\`).` → `Quét timeline chính rồi \`message_edits\`, \`reactions\` (doc hiện tại) và \`pin_actions\` của từng room (theo \`{r, ts}\`).`
- Bảng env: kiểm ba dòng Task 11 đã thêm (`grep -n "REACTION_MAX_EMOJIS\|PIN_LIMIT\|REACTION_COUNT_DELAY" README.md` → 3 dòng). Thiếu dòng nào → báo controller (Task 11 quên), thêm theo bảng config của hợp đồng chung.

**Step 5: CLAUDE.md**

- Mục Project, sau gạch `- M2b.2 (plan …)` thêm:

```markdown
- M2b.3 (plan `docs/plans/2026-10-06-m2b3-reactions-pins.md`): one reaction emoji per (user, message) in clustered `reactions` (`_id` = message key + user, conditional upsert, tombstone on remove, no cid; D88, D89); per-emoji counts in `messages.rx` by witness-checked recount + CAS on `rx.v`, touched inline and by the `reaction_counter` worker (D90); pins as dense `pin_actions` facts with a fold + CAS projection on `rooms.pins/pv`, no `base_pv`, exact `PIN_LIMIT` (D92); feed update/replace of `reactions`, work record user tail, unknown kinds Nak'd, `room_activity` keeps `Seq` 0 for non-message kinds (D91); events `reaction_changed`, `counts_changed`, `msg_pinned`, `msg_unpinned` (D93); member-level actions (D94); resync scans `reactions` and `pin_actions`; corecli/e2e react and pin.
```

- Câu `built on a data-class framework (§4) with decisions D61–D87` → `… D61–D94`. Thay câu `M2b.2 (edit + delete) is done on \`feat/m2b\`; next is M2b.3 (reactions + pins); its plan is not written yet.` (hoặc câu đang nói M2b.3 là việc tiếp theo, nếu commit plan đã đổi nó) bằng `M2b.3 (reactions + pins) is done on \`feat/m2b\`; next is M2b.4 (members + read position); its plan is not written yet.`
- Mục Commands: dòng `make e2e …` đổi chú thích thành `# tools/corecli: create, send, history, kill core-1, verify no loss/dup, live events, then edit seq 1 and delete seq 2, then react to seq 3 and pin seq 4`.
- **Key encoding**: thêm hai gạch sau gạch `- message_edits = …`:

```markdown
- reactions = the message key plus the user bytes (24B + 1..64B), so one message's reactions are one range
- pin_actions = `room│pv` (16B)
```

- **Counters**: thêm gạch cuối: `- \`pv\` is a room's dense pin version; \`rx.v\` versions a message's reaction summary and moves only when a count changes; a reaction's \`n\` counts the changes of one (user, message) doc.`
- **Storage ports**: `\`store.Edits\`, \`store.Hidden\` and \`store.ChangeFeed\`` → `\`store.Edits\`, \`store.Hidden\`, \`store.Reactions\`, \`store.ReactionSummaries\` (\`SetReactions\`), \`store.Pins\`, \`store.PinProjector\` (\`PinState\`, \`ApplyPins\`) and \`store.ChangeFeed\``.
- **Effect engine**:
  - `(\`store.Change.Kind\`: \`MessageInserted\`, \`RoomInserted\`, \`EditInserted\`)` → `(\`store.Change.Kind\`: \`MessageInserted\`, \`RoomInserted\`, \`EditInserted\`, \`ReactionChanged\`, \`PinInserted\`)`.
  - `watches the database for inserts into \`messages\`, \`rooms\` and \`message_edits\`` → `watches the database for inserts into \`messages\`, \`rooms\`, \`message_edits\`, \`reactions\` and \`pin_actions\`, and updates/replaces of \`reactions\` (an update decodes \`documentKey._id\` + \`updatedFields.n\`; no \`updateLookup\`),`.
  - `(keys + version + \`CommittedAt\`, 37 bytes; id \`m:{room}-{thread}-{seq}\`, \`r:{room}\` or \`e:{room}-{thread}-{seq}-v{ver}\`)` → `(keys + version + \`CommittedAt\`, 37 bytes, plus a \`len + user\` tail for \`ReactionChanged\`; id \`m:{room}-{thread}-{seq}\`, \`r:{room}\`, \`e:{room}-{thread}-{seq}-v{ver}\`, \`x:{room}-{thread}-{seq}-{user}-n{n}\` or \`p:{room}-p{pv}\`; a well-formed record of an unknown kind is Nak'd, not Term'd)`.
  - Trong gạch Workers, `for \`MessageInserted\`,,` → `for \`MessageInserted\`,`; sau `… then \`msg_changed\` (delay \`RECONCILE_DELAY\`, no mark; counts a republish only when the PubAck is not a duplicate) for \`EditInserted\`` thêm `; \`room_activity\`, \`reaction_counter\` (delay \`REACTION_COUNT_DELAY\`, one witness-checked touch per message per batch) then \`reaction_event\` (delay \`RECONCILE_DELAY\`, publishes the current doc when its \`n\` equals the record's) for \`ReactionChanged\`; \`room_activity\`, \`pin_projection\` (delay 0, one fold + CAS per room per batch) then \`pin_event\` (delay \`RECONCILE_DELAY\`) for \`PinInserted\`. \`room_activity\` keeps \`Seq\` 0 for every kind but \`MessageInserted\``.
  - `replays the main timeline backwards, then the room's \`message_edits\` by \`{r, ts}\`` → `replays the main timeline backwards, then the room's \`message_edits\`, \`reactions\` (current docs) and \`pin_actions\` by \`{r, ts}\``.
- Thêm khối mới ngay trước `**Permission hook and reader pipeline (\`access\`, \`view\`).**`:

```markdown
**Reactions and pins (`mutate`, `counter`, `pinproj`; D88–D94).**
- `ReactMessage`, `PinMessage` and `UnpinMessage` run in `mutate` (`Admit` → `Find` → `Allow` with actions `react_message`, `pin_message`, `unpin_message`; the default policy allows any member and `MESSAGE_LOCKED_KINDS` does not apply, D94).
- A reaction is one emoji per (user, message) in clustered `reactions` (`_id` = message key + user; fields `k r t u e pe n ts`; indexes `{k, e}`, `{r, ts}`; D88). Set is a conditional upsert on `e ≠ emoji` with a pipeline (`n+1`, `pe` = the old emoji); a duplicate key re-reads (same emoji = no-op, else retry ≤3, then `UNAVAILABLE`). Remove is an update without upsert that leaves the tombstone `e: ""`. No cid: the command is the desired state (D89). `REACTION_MAX_EMOJIS` (20) is soft; a deleted message only accepts a removal.
- Counts (design §7, D90): `messages.rx {c: [{e, n}], v}`, an array sorted by count then emoji. `counter.Toucher.Touch` checks witnesses (`Reactions.Count` reads each witness doc with majority in a causal session; `n < N` is `store.ErrStaleRead`), runs the covered aggregate on `{k, e}` in the same session, skips an equal summary and otherwise CASes `rx.v` (`$exists: false` at 0). The fast path touches inline before replying (`FastTouchTries` 3, errors ignored); the `reaction_counter` worker touches once per message per batch and counts repairs in `counter_repaired_total{counter="reactions"}`. Never `$inc`.
- Pins (D92): no `base_pv`. `pinproj.Projector.Current` folds `rooms.pins/pv` with the facts after pv; the command appends `pin_actions {_id: room│pv+1}` (dense pv, so `PIN_LIMIT` 50 is exact; already in the wanted state = success with no fact; a duplicate key with the same op, message and user is the result, else retry ≤3, then `ErrRetryLater`), then `Project`s (fold + CAS `pv == p`, shared with the `pin_projection` worker). Pinning a deleted message is `FAILED_PRECONDITION`; unpinning still works. `Rooms.Get` never reads pins.
- Events (D93, no ack marks): `reaction_changed` `{room}-{thread}-{seq}-{user}-n{n}`, `counts_changed` `{room}-{thread}-{seq}-reactions-v{v}`, `msg_pinned`/`msg_unpinned` `{room}-p{pv}` with the message snapshot (no text when deleted). `GetHistory` returns `Message.reactions` (counts only). SysMsg cids are `sys-{event_id}`.
- Wiring: `apps/core/service_wiring.go` builds `counter` and `pinproj` from the Mongo store and passes `mutate.Limits` from config.
```

- **Permission hook and reader pipeline**: `edit/delete/hide/clear in \`mutate\`` → `edit/delete/hide/clear/react/pin/unpin in \`mutate\``; `\`view.MaskDeleted\` (deleted → no text) and \`view.HideForViewer\` (seq ≤ \`ClearedBeforeSeq\` or hidden by the reader → \`hidden\`, no text)` → `\`view.MaskDeleted\` (deleted → no text, no reactions) and \`view.HideForViewer\` (seq ≤ \`ClearedBeforeSeq\` or hidden by the reader → \`hidden\`, no text, no reactions)`.
- **Detectors**: sau câu `Edit effects add the labels …` thêm `Reaction and pin effects add \`reaction_event\`, \`reaction_counter\` and \`pin_event\` (both counters) and \`pin_projection\` (only \`effect_dropped_total\`); \`counter_repaired_total{counter}\` counts summaries the workers rewrote.`; `(15 rules)` → `(16 rules)`.
- **Shard-readiness**: `Small fact collections (\`message_edits\`, \`pin_actions\`) may have \`{room, ts}\` (D70).` giữ; thay gạch `- The \`reactions\` unique index \`{k, emoji, u}\` starts with \`k\` (D68).` bằng `- \`reactions\` is clustered with \`_id\` = message key + user, so one message's reactions are one range under the future shard key \`{_id: 1}\`; it has \`{k, e}\` and \`{r, ts}\` (D88, replaces D68).`
- **Docs**: `new D61–D87` → `new D61–D94`; trong gạch `docs/plans/`, cuối câu thêm ` M2b.3: \`docs/plans/2026-10-06-m2b3-reactions-pins.md\` (executed; results and known issues at its end).`

**Step 6: INDEXES.csv**

Mỗi task trước sửa dòng của mình trong commit của nó; bước này chỉ kiểm và vá chỗ thiếu.

```bash
for p in apps/core/internal/counter apps/core/internal/pinproj apps/core/internal/domain apps/core/internal/store apps/core/internal/store/memstore apps/core/internal/store/mongostore apps/core/internal/store/storetest apps/core/internal/work apps/core/internal/pbconv apps/core/internal/access apps/core/internal/mutate apps/core/internal/grpcsrv apps/core/internal/view apps/core/internal/effects apps/core/internal/publish apps/core/internal/resync apps/core/internal/config tools/internal/route tools/corecli/internal/e2e; do printf '%s ' "$p"; grep -c "^$p," INDEXES.csv; done
grep -n "^docs/plans/2026-10-06-m2b3" INDEXES.csv | cut -c1-120
grep -n "ReactionChanged\|PinInserted\|ReactionEventID\|counter_repaired_total\|FoldPins\|SetReactions\|ReactMessage" INDEXES.csv | cut -c1-80
grep -n "16 alert rules\|15 alert rules" INDEXES.csv | cut -c1-80
```

Expected: mỗi package đúng `1`; có dòng plan M2b.3; grep thứ ba có kết quả ở các dòng `store`, `work`, `pbconv`, `effects`, `domain`, `grpcsrv`, `tools/internal/route`; grep cuối chỉ thấy `16 alert rules` (dòng `deploy/prometheus/alerts.yml`, Task 13). Thiếu dòng plan thì thêm ngay sau dòng `docs/plans/2026-10-05-m2b2-edit-delete.md,...`:

```csv
docs/plans/2026-10-06-m2b3-reactions-pins.md,doc,"M2b.3 implementation plan: one reaction emoji per (user, message) in clustered reactions (conditional upsert, tombstone, no cid); per-emoji counts in messages.rx by witness-checked recount + CAS (packages counter, pinproj); dense pin_actions facts with a fold + CAS projection on rooms.pins/pv, no base_pv, exact PIN_LIMIT; feed update/replace of reactions; work record user tail, unknown kinds Nak'd; effects reaction_counter, reaction_event, pin_projection, pin_event; counter_repaired_total and a 16th alert rule; resync scans reactions and pin_actions; corecli + e2e react and pin; execution results and known issues at its end",,everyone,,D67 D68 D88 D89 D90 D91 D92 D93 D94
```

Thay hai dòng docs:

```csv
docs/designs/261005-chatim-architecture.md,doc,"Single source of truth: requirements (R17 revised) and assumptions needing real numbers; principles P1-P8; data-class framework for every plan; data model and shard rules; built write path; fact + projection mutations (edit/delete built in M2b.2, pins in M2b.3); reaction set + counter recount CAS-ver with witness (M2b.3); effect engine (reader -> work stream -> workers); read path, room list, unread, sync token; gateway; guarantees with detectors; Decision Log D1-D60 kept, D61-D94 new",,everyone,,D1-D94
docs/roadmap.md,doc,"Plan-writing rules; milestones (M0-M2a.3 merged; M2b.0 mechanism foundations, M2b.1 effect engine, M2b.2 edit/delete, M2b.3 reactions/pins dev-done on feat/m2b; next members/read position, threads, read path, gateway, hardening); dependencies; readiness; M5 carry-over list",,everyone,,
```

Dòng khác còn mô tả cũ (vd. `work` còn "37 big-endian bytes" mà không nhắc đuôi user, `view` không nhắc `Reactions`) thì sửa theo hợp đồng chung và ghi vào báo cáo task nào đã quên.

Kiểm: `python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"` → Expected: `{7}`.

**Step 7: Kết quả thực thi**

Thêm cuối `docs/plans/2026-10-06-m2b3-reactions-pins.md`, điền từ nhật ký thực thi của controller (sha, lệch, Minor theo task); phần "Kiểm chứng cuối" để trống, Task 18 điền:

```markdown
## Kết quả thực thi

Commit từng task (nhánh `feat/m2b`):

- Chuẩn bị: `<sha>` plan. T1: baseline `fmt-check`/`vet`/`lint`/`test` xanh.
- T2 `<sha>` domain + keys; T3 `<sha>` proto + pbconv + kind publish; T4 `<sha>` port store + memstore + `storetest`; T5 `<sha>` mongostore; T6 `<sha>` feed; T7 `<sha>` `work.Record` + Nak kind lạ + `room_activity` (push); T8 `<sha>` `counter` + `pinproj`.
- T9 `<sha>` access + `mutate.React`; T10 `<sha>` `mutate.Pin`/`Unpin`; T11 `<sha>` config + grpcsrv + wiring; T12 `<sha>` view + `GetHistory`; T13 `<sha>` effect + metrics + luật 16 (push).
- T14 `<sha>` resync; T15 `<sha>`, `<sha>` route + corecli/e2e; T16 `<sha>` itest; T17 commit docs này.

Quyết định của owner trong lúc làm:

- (ghi theo nhật ký; không có thì "Không có ngoài các quyết định 2026-10-06 ở đầu plan.")

Lệch so với plan:

- (idiom gofmt/vet/lint đã áp; cách giải GAP-1 (accessor `Reactions()`/`Pins()` của `mongostore.Store` hay cách khác) và các chỗ đã đổi theo; tên adapter khác hợp đồng; …)

Lỗi đã biết, đã sửa:

- (theo nhật ký)

Lỗi Minor còn mở:

1. (theo task, do controller/reviewer ghi)
2. File gần 200 dòng (lần sửa sau phải tách): (liệt kê `wc -l` ≥ 180 của các file đã đụng)

Kiểm chứng trong lúc làm: (itest xanh ở task nào; `make e2e` PASS ở T15; `make alerts-check` 16 luật ở T13/T17)

Kiểm chứng cuối (Task 18, <YYYY-MM-DD>, trên `<sha>`):
```

Cập nhật luôn khối "Từ M2b.3" của roadmap (Step 3) bằng các Minor đáng mang sang M5, thay dòng `(các Minor controller ghi …)`.

**Step 8: Kiểm**

```bash
grep -n "Chưa xây\]" docs/designs/261005-chatim-architecture.md | grep "## 7\|6.3\|6.4"
grep -n "sys:{event_id}\|{k}-{u}-{emoji}-n{n}\|(15 luật)\|15 rules\|Next is M2b.3\|next is M2b.3" CLAUDE.md docs/designs/261005-chatim-architecture.md README.md docs/roadmap.md
grep -n "base_pv" docs/designs/261005-chatim-architecture.md CLAUDE.md docs/roadmap.md
```

Expected: lệnh đầu không in gì; lệnh hai chỉ còn dòng D93 (phương án bị loại `sys:{event_id}`, `{k}-{u}-{emoji}-n{n}`); lệnh ba chỉ còn các dòng nói `base_pv` bị bỏ/thay (§4, §6.3, D63, D92, roadmap M2b.3).

**Step 9: Commit**

```bash
git commit -m "docs: record the M2b.3 reactions and pins design and decisions D88-D94" -- docs/designs/261005-chatim-architecture.md docs/roadmap.md README.md CLAUDE.md INDEXES.csv docs/plans/2026-10-06-m2b3-reactions-pins.md
git show --stat HEAD
```

Expected: đúng 6 file.

---

### Task 18: Kiểm chứng cuối milestone

Chạy một lần ở cuối, theo bảng "Verification and review budget" (mốc cuối milestone). M2b.3 không phải milestone perf: không sweep corebench trước/sau; chỉ một lần chạy ngắn để chắc đường gửi (codec `messages` đọc thêm `rx`) không lỗi.

**Step 1:** `make fmt-check && make vet && make lint && make vuln`
Expected: sạch; vuln: `Your code is affected by 0 vulnerabilities` (có thể kèm cảnh báo cấp module GO-2026-5932, có từ trước).

**Step 2:** `make test`
Expected: mọi package `ok`, gồm `apps/core/internal/{counter,pinproj,mutate,view,effects,work,resync,access,pbconv,store/...}`, `pkg/keys`, `tools/internal/route`, `tools/corecli/internal/e2e`.

**Step 3:** `make infra-up && make itest`
Expected: mọi package `ok`, gồm contract `storetest` cho Mongo (`RunReactions`, `RunPins`, `RunReactionFeed`, `RunPinFeed`), `TestBootstrapIsIdempotent` (collection `reactions`, `pin_actions` + index), 6 itest của Task 16, drill resync (Task 14), các itest M2b.2. Không cần chạy lại nếu sau lần xanh cuối (Task 16) chỉ đổi docs.

**Step 4:** `make image TARGET=apps/core && make core-up && make e2e`
Expected: `e2e PASS: 40 messages before and 40 after killing core-1, no loss, no duplicate, every acked seq live; seq 1 edited and seq 2 deleted on history, edit history and live; seq 3 reacted then re-reacted and seq 4 pinned on replies, history and live`.

**Step 5:** `/metrics` trên cả hai core:

```bash
for c in chatim-core-1 chatim-core-2; do echo "== $c"; docker run --rm --network chatim_default curlimages/curl:8.11.1 -s http://$c:9090/metrics | grep -E '^chatim_core_(reconcile_running|reconcile_republished_total|effect_dropped_total|counter_repaired_total|work_processed_total|work_failures_total)[ {]'; done
```

Expected:
- đúng một core có `chatim_core_reconcile_running 1`;
- cả hai core có `effect_dropped_total{effect=…}` cho `reaction_event`, `reaction_counter`, `pin_projection`, `pin_event` (cùng các nhãn cũ), tất cả `0`; `reconcile_republished_total{effect=…}` cho `reaction_event`, `reaction_counter`, `pin_event` (không có nhãn `pin_projection`); `chatim_core_counter_repaired_total{counter="reactions"}`; `chatim_core_work_failures_total 0`;
- tổng hai core của `reconcile_republished_total` cho ba effect mới và của `counter_repaired_total{counter="reactions"}` = 0 sau e2e (fast path đã phát mọi event cuối và touch inline đã ghi `rx`; bản worker gửi lại là bản trùng nên không đếm). Khác 0 thì ghi lại (fast path đã lỡ một event hay một touch), không phải lỗi.

**Step 6:** `make alerts-check`
Expected: `SUCCESS: 16 rules found`.

**Step 7:** Resync dry-run trên compose (phạm vi 15 phút, có room e2e vừa react/ghim):

```bash
FROM=$(date -u -v-15M +%Y-%m-%dT%H:%M:%SZ); TO=$(date -u -v+1M +%Y-%m-%dT%H:%M:%SZ)
docker exec chatim-core-1 /app resync -from "$FROM" -to "$TO" -dry-run
```

Expected: một dòng `resync rooms=… room_records=… message_records=… edit_records=E reaction_records=R pin_records=P dry_run=true` với `E ≥ 2`, `R ≥ 1` (doc hiện tại của seq 3; lần react và đổi emoji của e2e là cùng một doc, cộng doc của lệnh tay ở Task 15 nếu có), `P ≥ 1` (fact pv1 của seq 4, cộng fact bỏ ghim của lệnh tay nếu có), exit 0. (`date -v` là cú pháp macOS; Linux: `date -u -d '-15 min' +%FT%TZ`.)

**Step 8:** corebench ngắn (chỉ kiểm không lỗi):

```bash
pmset -g therm | grep CPU_Speed_Limit
make poc TOOL=corebench ARGS="-rate 1000 -duration 30s -watch 20"
```

Expected: dòng `sends due=… sent=… acked=… failed=0 …` và `live events on 20 watched rooms: … missing=0 duplicates=0 …`. Ghi p99 ack để tham khảo, không so sánh (không phải milestone perf); `CPU_Speed_Limit` < 100 thì ghi kèm.

**Step 9: Kết quả thực thi + push**

Cuối mục "Kết quả thực thi" của `docs/plans/2026-10-06-m2b3-reactions-pins.md` (Task 17 đã tạo), dưới dòng `Kiểm chứng cuối (Task 18, …)`, điền:

```markdown
- `make fmt-check`, `make vet`, `make lint` sạch; `make vuln`: <kết quả>.
- `make test`: mọi package `ok` (<n> package).
- `make itest`: <xanh sau Task 16 / chạy lại>.
- `make e2e`: `e2e PASS: … seq 3 reacted then re-reacted and seq 4 pinned on replies, history and live` (phase 4: <số lần thử mỗi lệnh, core xử lý>).
- `/metrics` hai core: <reconcile_running; effect_dropped_total và reconcile_republished_total của reaction_event, reaction_counter, pin_projection, pin_event; counter_repaired_total{counter="reactions"}; work_failures_total; work_processed_total>.
- `make alerts-check`: `SUCCESS: 16 rules found`.
- resync dry-run: `<dòng in ra>`.
- corebench 1000/s 30s: failed=<n>, missing=<n>, duplicates=<n>, p99 ack <ms>, CPU_Speed_Limit <giá trị>.
- Mức sẵn sàng: `dev-done` trên `feat/m2b` (chưa merge `main`; merge một lần sau M2b.4).
```

Nếu Step 5 thấy `counter_repaired_total` hay republished khác 0, hoặc Step 1–8 có lệch, thêm vào "Lỗi Minor còn mở" và (nếu đáng mang sang) khối "Từ M2b.3" của roadmap.

```bash
git commit -m "docs: record M2b.3 execution results" -- docs/plans/2026-10-06-m2b3-reactions-pins.md docs/roadmap.md
git push origin feat/m2b
```

Expected: push thành công; `git status` sạch; `git log origin/feat/m2b -1` là commit này.

---

## Kết quả thực thi

Commit từng task (nhánh `feat/m2b`):

- Chuẩn bị: `a33611d` plan (đã sửa theo review trước khi commit: `pin_projection` chỉ drop `ErrRoomNotFound`; resync cần `-room` cho room chỉ có reaction/ghim trong khoảng mất; Nak kind lạ không giới hạn; event reaction trung gian có thể mất). T1: baseline `fmt-check`/`vet`/`lint`/`test` xanh.
- T2 `a68cfe2` domain + keys; T3 `d1af561` proto + pbconv + kind publish; T4 `8e8fccf` port store + memstore + `storetest` (25 subtest contract); T5 `c30531d` mongostore; T6 `df567ec` feed; T7 `3abaa6c` `work.Record` + Nak kind lạ + `room_activity` + README (push `a33611d..3abaa6c`); T8 `171855f` `counter` + `pinproj`.
- T9 `d5bd38c` access, `0e73c87` `mutate.React`; T10 `56f385d` `mutate.Pin`/`Unpin`; T11 `6e56626` config, `cb88a4c` grpcsrv + wiring + README; T12 `bf9f530` view + `GetHistory`; T13 `5ceb97f` effect + metrics + luật 16 (push `3abaa6c..5ceb97f`).
- T14 `82c4792` resync (một `scanByTime` dùng chung); T15 `31a5fc2`, `2a89f7e` route + corecli/e2e; T16 `c24d866` itest; T17 commit docs này.

Quyết định của owner trong lúc làm:

- Không có ngoài các quyết định 2026-10-06 ở đầu plan.

Lệch so với plan:

- GAP-1: `mongostore.Store` có accessor `Reactions()`/`Pins()` trả sub-store cho port `store.Reactions`/`store.Pins`; `SetReactions`, `PinState`, `ApplyPins` vẫn ở `*Store`. Các task sau dùng đúng cách này.
- T2: `pkg/keys/keys.go` 96 dòng, plan ước ≤ 95 (plan đếm sai).
- T2, T3, T11: cột purpose của các dòng `domain`, `pkg/keys`, `core.proto`, `pbconv`, `README.md` trong `INDEXES.csv` giờ có ngoặc kép vì chứa dấu phẩy (text giữ nguyên).
- T5, T6: sửa biên dịch trong test (`bson.Raw(data)` ở `reaction_codec_test`, test pipeline feed); code không đổi.
- T7: test `TestRealWorkQueueDefersRecordsOfUnknownKinds` của plan assert `Deferred == 1` nhưng `Fetch(10, 2s)` với Nak 200ms giao lại record ~9 lần trong một lần pull; controller duyệt sửa chỉ ở test (fetch lô 1). Hệ quả ghi vào D91 (xem Minor 3).
- T11: bước red thấy `Unimplemented` chứ không `Internal` (`pkg/grpcserver` truyền nguyên `Unimplemented`; plan đoán sai); lý do fail vẫn đúng.
- T13: sửa cơ học để qua lint/biên dịch: helper `built[T]` (gọi hàm nhiều giá trị), `strings.Builder` theo perfsprint, gofmt. Lỗi quy trình: không có bước red riêng (code không lệch).
- T15: errorlint `%v` → `%w` trong `deletedTakesNoMark`.
- T17: dòng plan M2b.3 trong `INDEXES.csv` (có từ commit plan) thay bằng bản đầy đủ của Step 6; README thêm giới hạn `-room` vào đoạn `/app resync`; ghi thêm vào thiết kế các điểm đã biết của D88, D89, D91, D93 (từ review).

Lỗi đã biết, đã sửa:

- Không có lỗi runtime. Itest flaky của M2b.2 đã sửa từ trước (`sendRetrying`); mọi `make itest` của M2b.3 xanh (T7 xanh ở lần chạy thứ hai, sau khi sửa test ở trên).

Lỗi Minor còn mở:

1. T5: `Reactions.Set` đọc lại sau trùng khoá bằng majority, nên write chưa majority có thể làm hết 3 lượt (`ErrReactionContended` → `UNAVAILABLE` giả) hoặc trả `n` cũ ở nhánh no-op; đọc local trên primary sẽ sửa (câu hỏi còn mở). `Pins` đọc local trên primary còn `Reactions` đọc majority (chưa ghi lý do). `Count` mở causal session cả khi không có witness. `PinState` với id > `MaxInt64` → `ErrRoomNotFound`. Decoder không đối chiếu field của doc với `_id`. `{r, ts}` của `reactions` không có prefix `_id` nên `Between` của resync sẽ scatter-gather khi đã shard (công cụ thủ công, chấp nhận, D88). Tombstone không bao giờ dọn (cố ý, D88).
2. T6: `AsInt64OK` cắt `n` kiểu double không nguyên; `n == 0` bị từ chối ở nhánh update nhưng `decodeReaction` (insert/replace) nhận (không nhất quán, vô hại); test pipeline không assert nội dung `$in` của update/replace (itest phủ); chưa kiểm Mongo phát update hay replace cho `FindOneAndUpdate` pipeline (cả hai đều xử lý); cột decisions dòng `mongostore` ghi `D92;D91` (thứ tự).
3. T7: `Encode` bỏ im lặng user không hợp lệ → record `ReactionChanged` 37 byte bị Term không đếm (không tới được: user đã qua `ValidUser`); lỗi của `Nak`/`Term` bị bỏ qua (giao lại sau `AckWait`); `Decode` chặn độ dài ở `MaxRecordSize` nên đuôi dài hơn sau này bị Term (D91 phủ); `queue_test` không assert record đã Nak không bao giờ được ack (đúng theo cấu trúc). D91: Nak kind lạ không giới hạn nên `ChatimWorkFailing` kêu trong lúc còn core cũ; một fetch có thể đếm cùng record kind lạ nhiều lần khi `WORK_FETCH_WAIT` > `WORK_RETRY_DELAY` (hiếm với mặc định 1s so với 5s).
4. T9: ở giới hạn `REACTION_MAX_EMOJIS`, user là người duy nhất react emoji của mình cũng không đổi sang emoji mới được dù số loại emoji giữ nguyên (luật "emoji mới khi ≥ Max", giới hạn mềm D89; owner có thể xem lại). Thứ tự kiểm (policy trước tin xoá, `validKey` trước emoji) chưa ghim bằng test; nhánh no-op trả summary đọc trước khi ghi (mềm); interface `CounterToucher` ở `react.go` còn `Deps` ở `mutator.go` (thẩm mỹ).
5. T10: chưa có unit test đồng thời thật (itest T16 (d) phủ `PIN_LIMIT` khi đồng thời); nhận diện retry không so tenant (theo room, vô hại); core chết sau `Append` trước enqueue → retry là no-op đúng trạng thái, không event (worker `pin_event` phát lại từ fact); không kiểm ctx giữa các lượt (store trả `ctx.Err`).
6. T11: test mức RPC không phủ `TooManyEmojis`/`TooManyPins`/`Unavailable` (mutate phủ); parse `REACTION_COUNT_DELAY` dựa vào thứ tự gọi sau khi `EffectDelay` đã đặt; thread bị từ chối (chỉ timeline chính, `ValidateThread`).
7. T13: lỗi `publish.Message` đếm drop một lần mỗi nhóm chứ không mỗi record; ba cache loại room mới theo effect (bộ nhớ); `cmp.Or` biến `Delay` 0 thành 1s (config cấm 0); `reaction_counter` drop có đếm cả lỗi vĩnh viễn `ErrInvalidArgument`. D93: bốn event mới không có ack mark; event reaction trung gian có thể mất, chỉ trạng thái cuối được bảo đảm (lớp tập).
8. T14: resync `ErrReactionPageFull`/`ErrPinPageFull` khi một thời điểm đầy một trang 1000 (như `ErrEditPageFull`); room chỉ có reaction/ghim trong khoảng mất không được chọn theo `ab`, phải chạy `-room` (D91).
9. File gần 200 dòng (lần sửa sau phải tách): `proto/chatim/v1/core.proto` 221 (từ 165, vượt 200); `mongostore/codec_test.go` 192; `counter/counter_test.go` 189; `mongostore/codec.go` 187; `resync/scan_test.go` 187 (chuyển `world` sang `world_test.go`); `grpcsrv/harness_test.go` 183; `config/load_test.go` 181.

Kiểm chứng trong lúc làm: `itest` xanh ở T5 (index phủ `k_1_e_1`, không FETCH), T6, T7, T9, T10, T11, T13, T14, T16 (6 itest mới PASS lần đầu, không skip; cả `make itest` xanh); `make e2e` PASS lần đầu ở T15 (phase 4: react 👍 rồi 🎉 trên seq 3, `change` 2, số đếm `v2`; ghim seq 4 `pv1`; lặp lệnh là no-op); `make alerts-check` `SUCCESS: 16 rules found` ở T13 và T17.

Kiểm chứng cuối (Task 18, <YYYY-MM-DD>, trên `<sha>`):
