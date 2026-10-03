# M2a.1 — Sửa hướng đánh số & publish — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: dùng `subagent-driven-development` (cùng phiên) hoặc `separate-driven-development` (phiên riêng) để thực thi plan này theo từng task.

**Goal:** Đưa phần M2a đã triển khai về đúng yêu cầu đã chốt ngày 2026-10-03: bỏ `pts` toàn room, id event là khoá tự nhiên, gỡ watermark/active marks/sweeper/recovery walk, publisher giữ thứ tự theo từng room, event best-effort.

**Architecture:**
- seq giữ nguyên như M2a: RAM của actor + CAS bằng va chạm `_id`.
- Event `msg_created` có id `{room}-{thread}-{seq}`, dùng cho cả `Event.id` lẫn `Nats-Msg-Id`. Không còn bộ đếm nào khác.
- Publisher không đụng Redis nữa. Mỗi lần `Enqueue` là một batch. Batch sau của cùng room chỉ được gửi khi batch trước xong (mọi event có ack, hoặc hết hạn mức retry). Các room khác không phải chờ.
- Mất event (core chết giữa commit và publish) là chấp nhận được (D47). Module đối soát làm ở Phase 2.

**Tech Stack:** Go 1.26 chạy trong Docker qua `make`; nats.go jetstream; mongo-driver v2; go-redis v9 (chỉ còn cho slot và dedupe); Buf v2; `testing/synctest`; goleak.

**Nguồn quyết định:** `.claude/plans/m2b-core-mutations_design.md` (D47–D51); session plan `~/.claude/plans/b-t-u-l-p-plan-memoized-treehouse.md` (mục "Phase M2a.1").

---

## Quy tắc chung (đọc trước khi làm task nào)

- **Không chạy `go` trên host.** Mọi lệnh Go chạy qua `make`. Test một package: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/<pkg>/..."`.
- **Không viết comment** trong Go, test, YAML, shell, Makefile. Tên phải tự nói lên ý nghĩa.
- File code dưới 200 dòng. Giữ `make fmt-check` sạch.
- Trước mỗi commit: `make fmt-check`, `make vet`, `make lint` + test của các package đã đụng.
- Commit theo Conventional Commits (`refactor`, `fix`, `feat`, `test`, `docs`), không nhắc tới AI, kết thúc bằng dòng `Co-Authored-By` theo quy định repo.
- Branch: `fix/m2a1-event-identity` (đã tạo từ `origin/main`).
- Khi một bước cho kết quả khác "Expected": **dừng lại và báo cáo**, không vá cho tới khi qua.
- Task 2, 3, 4 rủi ro cao (publish, lifecycle, write path): mỗi task có 1 reviewer kiểm spec + chất lượng trong một lượt. Task 1, 5, 6 do controller kiểm nhanh.

## Thứ tự task và lý do

| # | Task | Vì sao ở vị trí này |
|---|---|---|
| 1 | Id event tự nhiên | Nhỏ, độc lập; publisher dùng `Event.id` làm Msg-Id trước khi bỏ pts |
| 2 | Gỡ sweeper, recovery walk, ActivityMarks | Bỏ mọi chỗ cần pts liền nhau phía actor/app |
| 3 | Viết lại publisher theo thứ tự từng room, bỏ Redis | Sau task 2 không còn ai gọi `Skip` |
| 4 | Bỏ pts khỏi domain, store, dedupe, proto, tools | Sau task 3 publisher không đọc pts nữa |
| 5 | Docs, roadmap, CLAUDE.md, README, INDEXES | Mô tả trạng thái mới |
| 6 | Kiểm chứng cuối phase | DoD |

---

### Task 1: Id event là khoá tự nhiên `{room}-{thread}-{seq}`

**Files:**
- Modify: `apps/core/internal/pbconv/pbconv.go:19` (thay `EventID`), `:91` (Id của event)
- Modify: `apps/core/internal/publish/attempts.go:19`, `:34-46` (Msg-Id lấy từ `ev.GetId()`)
- Test: `apps/core/internal/pbconv/pbconv_test.go:29-30`, `:80`
- Test: `apps/core/internal/publish/stream_test.go:82`, `:93-102`
- Test: `apps/core/internal/publish/nats_integration_test.go:125`
- Test: `apps/core/shutdown_integration_test.go:149-150`

**Step 1: Viết test hỏng (pbconv)**

Trong `pbconv_test.go`, thay hai dòng case `"event"` và `"event max pts"` của `TestIdentifiersAreDecimal` bằng:

```go
		{"message event", pbconv.MessageEventID(42, 0, 7), "42-0-7"},
		{"thread message event", pbconv.MessageEventID(42, 3, 9), "42-3-9"},
		{"message event max seq", pbconv.MessageEventID(1, 0, math.MaxUint64), "1-0-18446744073709551615"},
```

Trong `TestMessageCreatedEnvelope`, đổi `Id: "9007199254740993-7"` thành `Id: "9007199254740993-0-7"`.

**Step 2: Chạy test, xác nhận hỏng**

Run: `make -s go ARGS="test -race ./apps/core/internal/pbconv/..."`
Expected: FAIL, lỗi biên dịch `undefined: pbconv.MessageEventID`.

**Step 3: Cài đặt tối thiểu**

`pbconv.go`: xoá `EventID`, thêm:

```go
func MessageEventID(room, thread, seq uint64) string {
	return RoomID(room) + "-" + strconv.FormatUint(thread, 10) + "-" + strconv.FormatUint(seq, 10)
}
```

Trong `MessageCreated` đổi `Id: EventID(m.Room, m.Pts),` thành `Id: MessageEventID(m.Room, m.Thread, m.Seq),`.

**Step 4: Publisher dùng `Event.id` làm Msg-Id**

`publish/attempts.go`:
- `errMalformed` thành `errors.New("event needs an id, a pts, a subject-safe tenant and a known payload")`. pts tạm còn vì tracker watermark vẫn dùng tới task 3.
- Trong `message`, điều kiện thành `if !ok || ev.GetId() == "" || ev.GetPts() == 0 || !validToken(ev.GetTenant()) {`.
- Đổi `m.Header.Set(jetstream.MsgIDHeader, pbconv.EventID(room, ev.GetPts()))` thành `m.Header.Set(jetstream.MsgIDHeader, ev.GetId())`, rồi bỏ import `pbconv`.

**Step 5: Sửa các test tham chiếu id cũ**

- `stream_test.go:82`: `"101-7"` → `"101-0-7"`.
- `stream_test.go:93-102`: thêm một event thiếu id. Thay khối khai báo bằng:

```go
	noPts, dotted, empty, noID := events(roomA, 1)[0], events(roomA, 2)[0], events(roomA, 3)[0], events(roomA, 5)[0]
	noPts.Pts, dotted.Tenant, empty.Payload, noID.Id = 0, "acme.x", nil, ""
	if err := rg.Enqueue(roomA, []*chatimv1.Event{noPts, dotted, empty, noID, events(roomA, 4)[0]}); err != nil {
```

  Đổi số malformed mong đợi `3` → `4`, và `"101-4"` → `"101-0-4"` (cả câu thông báo lỗi).
- `nats_integration_test.go:125`: `pbconv.EventID(roomA, pts)` → `pbconv.MessageEventID(roomA, 0, pts)`.
- `apps/core/shutdown_integration_test.go:149-150`: đổi key tra cứu thành `pbconv.MessageEventID(room, 0, a.seq)`, và câu báo lỗi thành `"acked %s in room %d has no event for seq %d", a.cid, room, a.seq`. Nếu struct ack trong file chưa có `seq`, lấy từ `resp.GetSeq()` tại chỗ ghi ack.

**Step 6: Chạy test, xác nhận qua**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/pbconv/... ./apps/core/internal/publish/... ./apps/core/internal/actor/..."`
Expected: PASS.

Run: `make -s go ARGS="vet ./..."`
Expected: không lỗi (bắt mọi chỗ còn gọi `EventID`).

**Step 7: Commit**

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/pbconv apps/core/internal/publish apps/core/shutdown_integration_test.go
git commit -m "refactor(core): identify message events by room, thread and seq"
```

---

### Task 2: Gỡ sweeper, recovery walk và ActivityMarks (D49, phía actor/app)

**Files:**
- Delete: `apps/core/internal/recovery/` (cả thư mục)
- Delete: `apps/core/internal/actor/room_recovery.go`
- Delete: `apps/core/internal/publish/activity_marks.go`, `apps/core/internal/publish/activity_marks_test.go`
- Delete tests: `apps/core/internal/actor/{recover_walk_test,recover_routing_test,mark_pin_test}.go`
- Modify: `apps/core/internal/actor/{config,router,room_actor,write_group,room_events}.go`
- Modify tests: `apps/core/internal/actor/{harness_test,fake_publisher_test,room_events_test,publish_pipeline_test,competing_writers_test,cross_core_harness_test,send_rules_test}.go`
- Modify tests: `apps/core/internal/grpcsrv/{fake_dependencies_test,harness_test}.go`
- Modify: `apps/core/{wiring,lifecycle,shutdown}.go`, `apps/core/startup_gate_test.go`
- Modify: `apps/core/internal/config/{config,components,validate}.go` + `{load_test,env_test,parse_test,validate_test}.go`
- Modify: `README.md:95`

**Step 1: Viết test hỏng (constructor không còn nhận marker)**

Trong `actor/send_rules_test.go:33-50`, xoá `nopMarker{},` ở mọi lời gọi `actor.NewRouter(...)` và xoá hẳn khối kiểm `nil activity marker` (dòng 49-51).

**Step 2: Chạy, xác nhận hỏng**

Run: `make -s go ARGS="test -race ./apps/core/internal/actor/..."`
Expected: FAIL, lỗi biên dịch `not enough arguments in call to actor.NewRouter`.

**Step 3: Sửa actor**

`config.go`:
- Xoá hằng `ActiveMarkEvery` và dòng trống phía trên nó.
- `EventPublisher` chỉ còn:

```go
type EventPublisher interface {
	Enqueue(room uint64, events []*chatimv1.Event) error
}
```

- Xoá interface `ActivityMarker`.
- `request` chỉ còn `cmd SendCmd` và `reply chan reply`. Xoá `newRecovery`.

`router.go`:
- Xoá field `marks`.
- Chữ ký và kiểm tra thành:

```go
func NewRouter(msgs store.Messages, rooms store.Rooms, sub Submitter, cids CIDRegistry, events EventPublisher, cfg Config, log *slog.Logger) (*Router, error) {
	if msgs == nil || rooms == nil || sub == nil || cids == nil || events == nil {
		return nil, fmt.Errorf("%w: router needs message and room stores, a submitter, a cid registry and an event publisher", apperr.ErrInvalidArgument)
	}
```

- Bỏ `marks: marks,` trong literal.

`room_actor.go`:
- Xoá field `markedAt` và `replays`.
- Trong `run`, xoá khối `if a.flight == nil && len(a.replays) > 0 && !a.retireRequested() { ... }` và khối `if first != nil && first.republish { ... }`.
- Trong `exit`, xoá vòng `for _, q := range a.replays` và `a.replays = nil`.

`write_group.go`:
- Xoá dòng `a.markActive(gctx, msgs)`.
- Trong `take`, xoá khối `if q.republish { ... }`.

`room_events.go`: xoá hàm `markActive` cùng các import chỉ nó dùng (`context`, `time`, `domain`). Giữ `publishLanded`.

Xoá file `room_recovery.go`. Lúc này `holeGrace`, `maxRecoverPages`, `holeClockSkew` biến mất theo.

**Step 4: Sửa test actor**

- Xoá `recover_walk_test.go`, `recover_routing_test.go`, `mark_pin_test.go`.
- `room_events_test.go`:
  - xoá hằng `markEvery` và ba test `TestActiveMarkIsWrittenBeforeTheInsert`, `TestActiveMarkIsThrottledPerRoom`, `TestSendSucceedsWhenTheMarkFailsAndTheNextGroupRetriesIt`;
  - đổi tên test còn lại thành `TestOnlyCommittedMessagesArePublishedInSeqOrder`;
  - bỏ import `errors`, `slices`, `time` nếu không còn dùng.
- `harness_test.go`:
  - bỏ field `marks`, `order` khỏi `rig` và khỏi `newRig`;
  - lời gọi thành `actor.NewRouter(rg.msgs, rg.rooms, rg.sub, rg.cids, rg.events, cfg, quiet)`.
- `fake_publisher_test.go`:
  - xoá `nopPublisher.Skip`, `nopMarker`, `journal`, `markSpy`, `journaledSubmitter`;
  - trong `publishSpy` chỉ giữ `mu`, `batches`, `Enqueue` (không còn `err`, `handed`, `note`, `calls`, `fail`, `Skip`) và `events`;
  - bỏ import `context`, `fmt`, `flush`, `actor` nếu không còn dùng.
- `competing_writers_test.go`:
  - `startCoreWith(t, msgs, rooms, cids, events actor.EventPublisher)` bỏ tham số `marks`;
  - `startCore` gọi `startCoreWith(t, msgs, rooms, cids, nopPublisher{})`;
  - `actor.NewRouter(msgs, rooms, fl, cids, events, clusterConfig, quiet)`.
- `cross_core_harness_test.go:59`: `actor.NewRouter(msgs, rooms, sub, cids, nopPublisher{}, cfg, quiet)`.
- `publish_pipeline_test.go`:
  - xoá `marksFor` và `TestSendSucceedsWhileRedisIsDownForActiveMarks`;
  - trong `TestSentMessagesArePublishedInPtsOrderUpToTheWatermark` đổi tên thành `TestSentMessagesArePublishedInSeqOrder`, gọi `startCoreWith(t, w.msgs, w.rooms, &fakeRegistry{}, pub)`, xoá khối kiểm `WatermarkKey` và khối kiểm `ActiveKey`;
  - bỏ import `strconv`, `slotmap` nếu không còn dùng.
- `grpcsrv/fake_dependencies_test.go`: xoá `nopPublisher.Skip` và `nopMarker`. `grpcsrv/harness_test.go:80`: `actor.NewRouter(rg.msgs, rg.rooms, fl, acceptAllCIDs{}, nopPublisher{}, actorConfig, quiet)`.

**Step 5: Chạy test actor + grpcsrv**

Run: `make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/actor/... ./apps/core/internal/grpcsrv/..."`
Expected: PASS.

**Step 6: Gỡ ActivityMarks khỏi publish**

Xoá `publish/activity_marks.go` và `publish/activity_marks_test.go`. Grep `MarkConfig|NewActivityMarks|ActiveKey` trong `apps/` phải chỉ còn ở `apps/core/wiring.go` và `apps/core/internal/config/*` (sửa ở step 7–8).

**Step 7: Sửa config**

`config/config.go`: xoá field `Marks` và `Recovery` cùng import `recovery`.

`config/components.go`:
- xoá dòng `c.Marks = ...` và cả khối `c.Recovery = recovery.Config{...}`;
- giữ `watermarkTTL` vì `c.Publish` còn dùng tới task 3;
- bỏ import `recovery`.

`config/validate.go`: trong `componentErrors` xoá dòng `{"RECOVERY_*, CORE_REQUEST_DEADLINE", c.Recovery.Validate()},`.

Test config:
- `load_test.go`: xoá các dòng `Marks: ...` và `Recovery: ...` ở cả hai `want`; bỏ import `recovery`.
- `env_test.go:18`: xoá dòng `"RECOVERY_INTERVAL", "RECOVERY_REMOVE_AFTER", "RECOVERY_STALE_AFTER",`. Ở `:34-35` xoá các cặp `"RECOVERY_*": ...`.
- `parse_test.go:61-63`: xoá ba case `RECOVERY_*`.
- `validate_test.go:39-40`: xoá hai case `remove-after`.

**Step 8: Sửa `apps/core`**

`wiring.go`:
- bỏ import `recovery`; bỏ field `sweeper` khỏi `app`;
- xoá khối `marks, err := publish.NewActivityMarks(...)`;
- `actor.NewRouter(st, st, fl, cids, pub, cfg.Actor, log)`;
- khối slot thành:

```go
	slotCfg := cfg.Slot
	slotCfg.BeforeRelease = router.EvictSlots
	slotCfg.AfterLose = router.EvictSlots
	slotCfg.AfterClaim = router.EvictSlots
	slots, err := slot.New(cl.slots, slotCfg, log)
	if err != nil {
		return nil, fmt.Errorf("wire slot manager: %w", err)
	}
	a.publisher, a.flusher, a.router, a.slots = pub, fl, router, slots
```

  rồi xoá các dòng `var sweeper ...`, `deps := recovery.Deps{...}` và `if sweeper, err = recovery.New(...)`.

`lifecycle.go`:
- `tasks` bỏ `sweeper`;
- `newSupervisor(ctx, 6)`;
- xoá dòng `sweeper: sup.start("sweeper", a.sweeper.Run),`.

`shutdown.go`: xoá `s.step("recovery sweeper", 0, nil, t.sweeper)`.

`startup_gate_test.go`:
- `startGate(t *testing.T, ctx context.Context, router *gatedRouter, slots runner) *gateRig`;
- literal `app` dùng `slots: slots` và bỏ `sweeper: sweeper`.

  Các lời gọi giữ nguyên đối số thứ tư (giờ là slot manager); test "sibling fails at startup" vẫn đúng ý.

`README.md:95`: xoá dòng `RECOVERY_*`.

**Step 9: Xoá package recovery**

```bash
git rm -r apps/core/internal/recovery apps/core/internal/actor/room_recovery.go apps/core/internal/actor/recover_walk_test.go apps/core/internal/actor/recover_routing_test.go apps/core/internal/actor/mark_pin_test.go apps/core/internal/publish/activity_marks.go apps/core/internal/publish/activity_marks_test.go
```

**Step 10: Chạy toàn bộ unit test**

Run: `make test`
Expected: PASS.

Run: `make vet && make lint`
Expected: sạch. Nếu `unused` báo helper test còn sót (ví dụ `journal`), xoá helper đó.

**Step 11: Commit**

```bash
make fmt-check
git add -A apps/core README.md
git commit -m "refactor(core): remove the recovery sweeper, recovery walk and activity marks"
```

Commit body ghi: "D47: events are best-effort; a periodic event reconciliation module makes up for losses (Phase 2). D49."

---

### Task 3: Publisher giữ thứ tự từng room, không còn Redis (D49, D50)

**Files:**
- Delete: `apps/core/internal/publish/{room_watermarks,watermark_store}.go`
- Delete tests: `apps/core/internal/publish/{base_test,skip_test,tracking_test,watermark_test}.go`
- Rewrite: `apps/core/internal/publish/{config,publisher,shard,attempts}.go`
- Create: `apps/core/internal/publish/room_queues.go`
- Create test: `apps/core/internal/publish/room_order_test.go`
- Modify tests: `apps/core/internal/publish/{harness_test,lifecycle_test,stream_test,nats_integration_test}.go`
- Modify: `apps/core/internal/actor/publish_pipeline_test.go` (`startPublisher`)
- Modify: `apps/core/internal/actor/room_events.go` (sắp theo seq)
- Modify: `apps/core/wiring.go`, `apps/core/clients.go`, `apps/core/redis_clients_test.go`
- Modify: `apps/core/internal/config/{components,validate}.go` + `{load_test,env_test,parse_test}.go`
- Modify: `README.md:93`

**Step 1: Harness test mới (không còn Redis)**

Viết lại `publish/harness_test.go`:

```go
package publish_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/testlog"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

const (
	tenant        = "acme"
	roomA  uint64 = 101
	roomB  uint64 = 202

	queueFullMsg = "publish queue full; dropping events"
	abandonedMsg = "event publish abandoned; reconciliation must republish it"
	malformedMsg = "dropping malformed event"
)

var (
	errNack   = errors.New("nack")
	sentAt    = time.UnixMilli(1_700_000_000_000).UTC()
	fastSetup = publish.Config{
		SubjectRoot: "evt", Shards: 2, QueueSize: 64, MaxPending: 16, MaxRetrying: 64, Attempts: 3,
		RetryBackoff: time.Millisecond, MaxBackoff: 4 * time.Millisecond, AckTimeout: 200 * time.Millisecond,
	}
)

type rig struct {
	*publish.Publisher
	js   *publishtest.JetStream
	sink *testlog.Sink
	done chan error
	once sync.Once
	err  error
}

func newRig(t *testing.T, cfg publish.Config) *rig {
	t.Helper()
	rg := &rig{js: &publishtest.JetStream{}, sink: &testlog.Sink{}}
	p, err := publish.New(rg.js, cfg, rg.sink.Logger())
	if err != nil {
		t.Fatalf("publish.New: %v", err)
	}
	rg.Publisher = p
	return rg
}

func (rg *rig) start(t *testing.T) *rig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	rg.done = make(chan error, 1)
	go func() { rg.done <- rg.Run(ctx) }()
	t.Cleanup(func() {
		stop, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = rg.Close(stop)
		cancel()
		_ = rg.wait()
	})
	return rg
}

func (rg *rig) wait() error {
	rg.once.Do(func() { rg.err = <-rg.done })
	return rg.err
}

func started(t *testing.T, cfg publish.Config) *rig {
	t.Helper()
	return newRig(t, cfg).start(t)
}

func events(room uint64, seqs ...uint64) []*chatimv1.Event {
	out := make([]*chatimv1.Event, len(seqs))
	for i, s := range seqs {
		m := domain.Message{Room: room, Seq: s, Pts: s, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "hi", CID: "c" + strconv.FormatUint(s, 10), CreatedAt: sentAt}
		out[i] = pbconv.MessageCreated(domain.RoomGroup, m)
	}
	return out
}

func (rg *rig) enqueue(t *testing.T, room uint64, seqs ...uint64) {
	t.Helper()
	if err := rg.Enqueue(room, events(room, seqs...)); err != nil {
		t.Fatalf("Enqueue(%d, %v): %v", room, seqs, err)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within 5s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func nackIDs(ids ...string) publishtest.Rule {
	return func(m *nats.Msg) error {
		if slices.Contains(ids, publishtest.MsgID(m)) {
			return errNack
		}
		return nil
	}
}

func storedIDs(js *publishtest.JetStream) []string {
	var out []string
	for _, m := range js.Stored() {
		out = append(out, publishtest.MsgID(m))
	}
	return out
}

func attemptsOf(js *publishtest.JetStream, id string) int {
	n := 0
	for _, m := range js.Attempts() {
		if publishtest.MsgID(m) == id {
			n++
		}
	}
	return n
}

func attemptIndexes(js *publishtest.JetStream, id string) []int {
	var out []int
	for i, m := range js.Attempts() {
		if publishtest.MsgID(m) == id {
			out = append(out, i)
		}
	}
	return out
}
```

`Pts: s` trong `events` chỉ giữ tới task 4 (trường `Pts` của domain bị bỏ ở đó).

**Step 2: Viết test thứ tự (hỏng với publisher cũ)**

Tạo `publish/room_order_test.go`:

```go
package publish_test

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
)

func TestARoomsNextBatchWaitsForItsEarlierBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, fastSetup)
		rg.js.Hold()
		rg.enqueue(t, roomA, 1, 2)
		rg.enqueue(t, roomA, 3)
		synctest.Wait()
		if n := rg.js.Held(); n != 2 {
			t.Fatalf("%d publishes in flight, want only the 2 of the first batch", n)
		}
		rg.js.Release()
		synctest.Wait()
		if got, want := storedIDs(rg.js), []string{"101-0-1", "101-0-2", "101-0-3"}; !slices.Equal(got, want) {
			t.Fatalf("stored %v, want %v", got, want)
		}
	})
}

func TestARetriedEventStillPublishesBeforeTheRoomsNextBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := started(t, fastSetup)
		nacked := false
		rg.js.NackWhen(func(m *nats.Msg) error {
			if publishtest.MsgID(m) == "101-0-1" && !nacked {
				nacked = true
				return errNack
			}
			return nil
		})
		rg.enqueue(t, roomA, 1)
		rg.enqueue(t, roomA, 2)
		synctest.Wait()
		time.Sleep(2 * fastSetup.MaxBackoff)
		synctest.Wait()
		if got, want := storedIDs(rg.js), []string{"101-0-1", "101-0-2"}; !slices.Equal(got, want) {
			t.Fatalf("stored %v, want the retried first batch before the second", got)
		}
		if n := attemptsOf(rg.js, "101-0-1"); n != 2 {
			t.Fatalf("first event attempted %d times, want 2", n)
		}
	})
}

func TestAnAbandonedEventReleasesItsRoomAndNeverHoldsOtherRooms(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := fastSetup
		cfg.Shards = 1
		rg := started(t, cfg)
		rg.js.NackWhen(nackIDs("101-0-1"))
		rg.enqueue(t, roomA, 1)
		rg.enqueue(t, roomA, 2)
		rg.enqueue(t, roomB, 1)
		synctest.Wait()
		time.Sleep(time.Second)
		synctest.Wait()
		if got, want := storedIDs(rg.js), []string{"202-0-1", "101-0-2"}; !slices.Equal(got, want) {
			t.Fatalf("stored %v, want room B at once and room A's second batch after the first gave up", got)
		}
		first, second := attemptIndexes(rg.js, "101-0-1"), attemptIndexes(rg.js, "101-0-2")
		if len(first) != cfg.Attempts || len(second) != 1 || second[0] < first[len(first)-1] {
			t.Fatalf("attempt order: first %v second %v, want %d tries of the first before the second", first, second, cfg.Attempts)
		}
		if n := rg.sink.Count(abandonedMsg); n != 1 {
			t.Fatalf("logged %d abandoned publishes, want 1", n)
		}
	})
}
```

**Step 3: Sửa các test publish còn lại theo API mới**

- Xoá `base_test.go`, `skip_test.go`, `tracking_test.go`, `watermark_test.go`.
- `lifecycle_test.go`:
  - `newRig(t, cfg, nil)` → `newRig(t, cfg)`.
  - `TestCloseDrainsQueuedEventsAndFlushesWatermarks` đổi tên thành `TestCloseDrainsQueuedEvents`; bỏ `cfg.FlushEvery = time.Hour`; khối kiểm cuối thành `if n := len(rg.js.Stored()); n != 3 { t.Fatalf("stored %d events after Close, want 3", n) }`.
  - `TestCloseStopsAtItsDeadlineAndKeepsAckedProgress`: bỏ `cfg.FlushEvery`; khối kiểm cuối thành `if got := storedIDs(rg.js); !slices.Equal(got, []string{"101-0-1"}) { t.Fatalf("stored %v after an aborted drain, want only 101-0-1", got) }`; thêm import `slices`.
  - `TestHardCancelStopsShardsWithoutLeaks`: `newRig(t, fastSetup)`. Vì room A giờ chỉ có 2 event đang bay trong batch đầu, đổi `rg.js.Held() == 3` giữ nguyên (2 của A + 1 của B).
  - `TestNewRejectsMissingDependenciesAndBadConfig`:
    - bỏ `newRedis`, `plain`, hai case redis và case `"negative ttl"`;
    - mọi lời gọi `publish.New(js, rdb, cfg, nil)` → `publish.New(js, cfg, nil)`;
    - bỏ import `redis`.
- `stream_test.go`:
  - `started(t, fastSetup)` giữ nguyên.
  - Trong test malformed, sau task này điều kiện không còn kiểm `pts`, nên event `noPts` hợp lệ trở lại. Đổi khối thành:

```go
	dotted, empty, noID := events(roomA, 2)[0], events(roomA, 3)[0], events(roomA, 5)[0]
	dotted.Tenant, empty.Payload, noID.Id = "acme.x", nil, ""
	if err := rg.Enqueue(roomA, []*chatimv1.Event{dotted, empty, noID, events(roomA, 4)[0]}); err != nil {
```

    và số malformed mong đợi `3`.
- `nats_integration_test.go`:
  - bỏ `mr, rdb := newRedis(t)`; `publish.New(it.js, cfg, sink.Logger())`;
  - thay `waitWatermark(t, mr, roomA, 2)` bằng `eventually(t, "both events stored", func() bool { return streamMsgs(t, it) == 2 })`. Nếu thấy gọn hơn, có thể bỏ dòng này và dựa vào `Close` drain, vì `Close` chờ mọi publish xong;
  - đổi chữ "pts" trong thông báo lỗi thành "seq". Thêm helper nhỏ trong file:

```go
func streamMsgs(t *testing.T, it *itStream) uint64 {
	t.Helper()
	s, err := it.js.Stream(t.Context(), it.cfg.Name)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	info, err := s.Info(t.Context())
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	return info.State.Msgs
}
```

**Step 4: Chạy, xác nhận hỏng**

Run: `make -s go ARGS="test -race ./apps/core/internal/publish/..."`
Expected: FAIL, lỗi biên dịch (`publish.New` sai số đối số).

**Step 5: Cài đặt — `config.go`**

Thay toàn bộ `publish/config.go` bằng:

```go
package publish

import (
	"cmp"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const (
	DefaultShards       = 4
	DefaultQueueSize    = 1024
	DefaultMaxPending   = 256
	DefaultMaxRetrying  = 1024
	DefaultAttempts     = 4
	DefaultRetryBackoff = 100 * time.Millisecond
	DefaultMaxBackoff   = 2 * time.Second
	DefaultAckTimeout   = 2 * time.Second
)

type Config struct {
	SubjectRoot  string
	Shards       int
	QueueSize    int
	MaxPending   int
	MaxRetrying  int
	Attempts     int
	RetryBackoff time.Duration
	MaxBackoff   time.Duration
	AckTimeout   time.Duration
}

func (c Config) Validate() error { return c.withDefaults().validate() }

func (c Config) withDefaults() Config {
	c.Shards = cmp.Or(c.Shards, DefaultShards)
	c.QueueSize = cmp.Or(c.QueueSize, DefaultQueueSize)
	c.MaxPending = cmp.Or(c.MaxPending, DefaultMaxPending)
	c.MaxRetrying = cmp.Or(c.MaxRetrying, DefaultMaxRetrying)
	c.Attempts = cmp.Or(c.Attempts, DefaultAttempts)
	c.RetryBackoff = cmp.Or(c.RetryBackoff, DefaultRetryBackoff)
	c.MaxBackoff = cmp.Or(c.MaxBackoff, DefaultMaxBackoff)
	c.AckTimeout = cmp.Or(c.AckTimeout, DefaultAckTimeout)
	return c
}

func (c Config) validate() error {
	counts := []int{c.Shards, c.QueueSize, c.MaxPending, c.MaxRetrying, c.Attempts}
	spans := []time.Duration{c.RetryBackoff, c.MaxBackoff, c.AckTimeout}
	switch {
	case !validToken(c.SubjectRoot):
		return fmt.Errorf("%w: publish subject root %q must be one subject token", apperr.ErrInvalidArgument, c.SubjectRoot)
	case !allPositive(counts) || !allPositive(spans):
		return fmt.Errorf("%w: publish config %+v must be positive", apperr.ErrInvalidArgument, c)
	case c.Shards > slotmap.Count:
		return fmt.Errorf("%w: publish shards %d exceed %d slots", apperr.ErrInvalidArgument, c.Shards, slotmap.Count)
	case c.RetryBackoff > c.MaxBackoff:
		return fmt.Errorf("%w: publish retry backoff %v exceeds its cap %v", apperr.ErrInvalidArgument, c.RetryBackoff, c.MaxBackoff)
	default:
		return nil
	}
}

func (c Config) JetStreamOptions() []jetstream.JetStreamOpt {
	c = c.withDefaults()
	return []jetstream.JetStreamOpt{
		jetstream.WithPublishAsyncMaxPending(2 * c.Shards * c.MaxPending),
		jetstream.WithPublishAsyncTimeout(c.AckTimeout),
	}
}

func allPositive[T int | time.Duration](vs []T) bool {
	for _, v := range vs {
		if v <= 0 {
			return false
		}
	}
	return true
}

func validToken(s string) bool {
	return s != "" && !strings.ContainsAny(s, ".*> \t\r\n")
}
```

**Step 6: Cài đặt — `publisher.go`**

Thay toàn bộ bằng:

```go
package publish

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

var (
	ErrQueueFull = fmt.Errorf("publish queue full: %w", domain.ErrBusy)
	ErrClosed    = fmt.Errorf("publisher closed: %w", domain.ErrRetryLater)
	errStarted   = errors.New("publisher already started")
)

type JetStream interface {
	PublishMsgAsync(m *nats.Msg, opts ...jetstream.PublishOpt) (jetstream.PubAckFuture, error)
}

type Publisher struct {
	shards  []*shard
	log     *slog.Logger
	mu      sync.RWMutex
	closed  bool
	started atomic.Bool
	abort   chan struct{}
	stop    sync.Once
	done    chan struct{}
}

func New(js JetStream, cfg Config, log *slog.Logger) (*Publisher, error) {
	if js == nil {
		return nil, fmt.Errorf("%w: publisher needs a jetstream client", apperr.ErrInvalidArgument)
	}
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	p := &Publisher{shards: make([]*shard, cfg.Shards), log: log, abort: make(chan struct{}), done: make(chan struct{})}
	for i := range p.shards {
		p.shards[i] = newShard(js, cfg, log, p.abort)
	}
	return p, nil
}

func (p *Publisher) Run(ctx context.Context) error {
	if !p.started.CompareAndSwap(false, true) {
		return errStarted
	}
	defer close(p.done)
	var wg sync.WaitGroup
	for _, s := range p.shards {
		wg.Go(func() { s.run(ctx) })
	}
	wg.Wait()
	return ctx.Err()
}

func (p *Publisher) Enqueue(room uint64, events []*chatimv1.Event) error {
	if len(events) == 0 {
		return nil
	}
	s := p.shards[int(slotmap.Of(room))%len(p.shards)]
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return ErrClosed
	}
	select {
	case s.queue <- item{room: room, events: events}:
		s.full.Store(false)
		return nil
	default:
		if s.full.CompareAndSwap(false, true) {
			p.log.Warn("publish queue full; dropping events", "room", room, "events", len(events))
		}
		return ErrQueueFull
	}
}

func (p *Publisher) Close(ctx context.Context) error {
	p.mu.Lock()
	if !p.closed {
		p.closed = true
		for _, s := range p.shards {
			close(s.queue)
		}
	}
	p.mu.Unlock()
	if !p.started.Load() {
		return nil
	}
	select {
	case <-p.done:
		return nil
	case <-ctx.Done():
		p.stop.Do(func() { close(p.abort) })
		<-p.done
		return ctx.Err()
	}
}
```

**Step 7: Cài đặt — `room_queues.go` (mới)**

```go
package publish

type batch struct {
	room uint64
	open int
}

type roomQueues struct {
	active  map[uint64]*batch
	waiting map[uint64][]item
	held    int
}

func newRoomQueues() roomQueues {
	return roomQueues{active: make(map[uint64]*batch), waiting: make(map[uint64][]item)}
}

func (q *roomQueues) empty() bool { return len(q.active) == 0 && q.held == 0 }

func (q *roomQueues) hold(it item) bool {
	if _, busy := q.active[it.room]; !busy {
		return false
	}
	q.waiting[it.room] = append(q.waiting[it.room], it)
	q.held += len(it.events)
	return true
}

func (q *roomQueues) start(b *batch) { q.active[b.room] = b }

func (q *roomQueues) finish(room uint64) (item, bool) {
	delete(q.active, room)
	items := q.waiting[room]
	if len(items) == 0 {
		return item{}, false
	}
	it := items[0]
	items[0] = item{}
	if len(items) == 1 {
		delete(q.waiting, room)
	} else {
		q.waiting[room] = items[1:]
	}
	q.held -= len(it.events)
	return it, true
}
```

**Step 8: Cài đặt — `shard.go`**

Thay toàn bộ bằng:

```go
package publish

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

type item struct {
	room   uint64
	events []*chatimv1.Event
}

type shard struct {
	js    JetStream
	cfg   Config
	log   *slog.Logger
	queue chan item
	abort <-chan struct{}
	full  atomic.Bool

	rooms     roomQueues
	ready     []*attempt
	pending   []*attempt
	retrying  []*attempt
	nextRetry time.Time
	wake      *time.Timer
	givingUp  bool
}

func newShard(js JetStream, cfg Config, log *slog.Logger, abort <-chan struct{}) *shard {
	return &shard{js: js, cfg: cfg, log: log, queue: make(chan item, cfg.QueueSize), abort: abort, rooms: newRoomQueues()}
}

func (s *shard) run(ctx context.Context) {
	s.wake = time.NewTimer(time.Hour)
	s.wake.Stop()
	defer s.wake.Stop()
	queue := s.queue
	for {
		s.feed()
		if queue == nil && s.idle() {
			return
		}
		in := queue
		if len(s.ready) > 0 || s.rooms.held >= s.cfg.QueueSize {
			in = nil
		}
		var okc <-chan *jetstream.PubAck
		var errc <-chan error
		if len(s.pending) > 0 {
			okc, errc = s.pending[0].okc, s.pending[0].errc
		}
		select {
		case it, open := <-in:
			if !open {
				queue = nil
				continue
			}
			if !s.rooms.hold(it) {
				s.begin(it)
			}
		case <-okc:
			s.settleHead(nil)
		case err := <-errc:
			s.settleHead(err)
		case <-s.armed():
			s.wakeUp(time.Now())
		case <-s.abort:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (s *shard) idle() bool {
	return s.rooms.empty() && len(s.ready) == 0 && len(s.pending) == 0 && len(s.retrying) == 0
}

func (s *shard) begin(it item) {
	for !s.queueBatch(it) {
		next, ok := s.rooms.finish(it.room)
		if !ok {
			return
		}
		it = next
	}
}

func (s *shard) queueBatch(it item) bool {
	b := &batch{room: it.room}
	for _, ev := range it.events {
		msg, err := message(s.cfg.SubjectRoot, it.room, ev)
		if err != nil {
			s.log.Error("dropping malformed event", "room", it.room, "err", err)
			continue
		}
		b.open++
		s.ready = append(s.ready, &attempt{batch: b, msg: msg})
	}
	if b.open == 0 {
		return false
	}
	s.rooms.start(b)
	return true
}

func (s *shard) feed() {
	for len(s.ready) > 0 && len(s.pending) < s.cfg.MaxPending {
		a := s.ready[0]
		s.ready[0] = nil
		s.ready = s.ready[1:]
		s.send(a)
	}
}

func (s *shard) settleHead(err error) {
	a := s.pending[0]
	s.pending[0] = nil
	s.pending = s.pending[1:]
	if err != nil {
		s.failed(a, err)
		return
	}
	s.givingUp = false
	s.done(a)
}

func (s *shard) done(a *attempt) {
	a.batch.open--
	if a.batch.open > 0 {
		return
	}
	if next, ok := s.rooms.finish(a.batch.room); ok {
		s.begin(next)
	}
}
```

**Step 9: Cài đặt — `attempts.go`**

Thay toàn bộ bằng:

```go
package publish

import (
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/pkg/backoff"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

var (
	errAckTimeout = errors.New("publish ack timed out")
	errMalformed  = errors.New("event needs an id, a subject-safe tenant and a known payload")
)

type attempt struct {
	batch    *batch
	msg      *nats.Msg
	tries    int
	okc      <-chan *jetstream.PubAck
	errc     <-chan error
	deadline time.Time
	due      time.Time
}

func message(root string, room uint64, ev *chatimv1.Event) (*nats.Msg, error) {
	kind, ok := eventKind(ev)
	if !ok || ev.GetId() == "" || !validToken(ev.GetTenant()) {
		return nil, errMalformed
	}
	data, err := proto.Marshal(ev)
	if err != nil {
		return nil, fmt.Errorf("marshal event: %w", err)
	}
	m := &nats.Msg{Subject: roomSubject(root, ev.GetTenant(), room, kind), Data: data, Header: nats.Header{}}
	m.Header.Set(jetstream.MsgIDHeader, ev.GetId())
	return m, nil
}

func eventKind(ev *chatimv1.Event) (string, bool) {
	switch ev.GetPayload().(type) {
	case *chatimv1.Event_MessageCreated:
		return msgCreated, true
	default:
		return "", false
	}
}

func (s *shard) send(a *attempt) {
	a.tries++
	f, err := s.js.PublishMsgAsync(a.msg)
	if err != nil {
		s.failed(a, err)
		return
	}
	a.okc, a.errc, a.deadline = f.Ok(), f.Err(), time.Now().Add(s.cfg.AckTimeout)
	s.pending = append(s.pending, a)
}

func (s *shard) failed(a *attempt, err error) {
	if a.tries < s.cfg.Attempts && len(s.retrying) < s.cfg.MaxRetrying {
		a.due = time.Now().Add(backoff.Jitter(s.retryDelay(a.tries)))
		s.retrying = append(s.retrying, a)
		if s.nextRetry.IsZero() || a.due.Before(s.nextRetry) {
			s.nextRetry = a.due
		}
		return
	}
	if !s.givingUp {
		s.givingUp = true
		s.log.Warn("event publish abandoned; reconciliation must republish it", "room", a.batch.room, "event", a.msg.Header.Get(jetstream.MsgIDHeader), "tries", a.tries, "err", err)
	}
	s.done(a)
}

func (s *shard) retryDelay(tries int) time.Duration {
	d := s.cfg.RetryBackoff
	for range tries - 1 {
		if d >= s.cfg.MaxBackoff/2 {
			return s.cfg.MaxBackoff
		}
		d *= 2
	}
	return d
}

func (s *shard) armed() <-chan time.Time {
	var at time.Time
	if len(s.pending) > 0 {
		at = s.pending[0].deadline
	}
	if len(s.retrying) > 0 && (at.IsZero() || s.nextRetry.Before(at)) {
		at = s.nextRetry
	}
	if at.IsZero() {
		return nil
	}
	s.wake.Reset(time.Until(at))
	return s.wake.C
}

func (s *shard) wakeUp(now time.Time) {
	for len(s.pending) > 0 && !now.Before(s.pending[0].deadline) {
		select {
		case <-s.pending[0].okc:
			s.settleHead(nil)
		case err := <-s.pending[0].errc:
			s.settleHead(err)
		default:
			s.settleHead(errAckTimeout)
		}
	}
	if len(s.retrying) == 0 || now.Before(s.nextRetry) {
		return
	}
	var due []*attempt
	rest := s.retrying[:0:0]
	s.nextRetry = time.Time{}
	for _, a := range s.retrying {
		if !now.Before(a.due) {
			due = append(due, a)
			continue
		}
		rest = append(rest, a)
		if s.nextRetry.IsZero() || a.due.Before(s.nextRetry) {
			s.nextRetry = a.due
		}
	}
	s.retrying = rest
	s.ready = append(due, s.ready...)
}
```

Xoá `room_watermarks.go` và `watermark_store.go`.

**Step 10: Chạy test publish**

Run: `make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/publish/..."`
Expected: PASS. Ba test thứ tự qua, không leak (goleak).

**Step 11: Nối lại actor và app**

- `actor/room_events.go`: trong `publishLanded`, sắp theo seq: `cmp.Compare(x.GetSeq(), y.GetSeq())`.
- `actor/publish_pipeline_test.go`:
  - `startPublisher(t *testing.T, js publish.JetStream) *publish.Publisher` gọi `publish.New(js, publish.Config{SubjectRoot: "evt"}, quiet)`;
  - lời gọi trong test thành `startPublisher(t, js)`;
  - nếu `newWorld` không còn cần cho test này thì dùng `memstore` trực tiếp như `newCluster`; nếu vẫn dùng thì giữ;
  - bỏ import `redis`.
- `apps/core/wiring.go`: `pub, err := publish.New(cl.js, cfg.Publish, log)`.
- `apps/core/clients.go`:
  - xoá field `redis` và dòng tạo `c.redis`;
  - danh sách ping thành `{{"state", c.slots}, {"dedupe", c.dedupe}}`;
  - trong `close`, vòng lặp thành `[]*redis.Client{c.slots, c.dedupe}`.
- `apps/core/redis_clients_test.go:47-49`: xoá khối kiểm `c.redis`.
- `config/components.go`:
  - xoá `watermarkTTL`;
  - `c.Publish` bỏ `FlushEvery`, `WatermarkTTL`, `RedisTimeout`, `RedisCooldown`;
  - `redisTimeout`/`redisCooldown` chỉ còn cho `c.Dedupe`.
- `config/validate.go`: nhãn `{"PUB_*, EVT_SUBJECT_ROOT, REDIS_OP_TIMEOUT, REDIS_COOLDOWN", ...}` → `{"PUB_*, EVT_SUBJECT_ROOT", ...}`.
- Test config:
  - `load_test.go`: hai `Publish: publish.Config{...}` bỏ `FlushEvery`, `WatermarkTTL`, `RedisTimeout`, `RedisCooldown`; nếu `day` không còn dùng ở chỗ khác thì giữ vì `Stream` còn dùng.
  - `env_test.go`: xoá `"PUB_FLUSH_EVERY"`, `"PUB_WATERMARK_TTL"` ở danh sách và hai cặp giá trị.
  - `parse_test.go:56-57`: xoá hai case.
- `README.md:93`: dòng `PUB_*` bỏ `PUB_FLUSH_EVERY`, `PUB_WATERMARK_TTL` và cột giá trị `50ms`, `168h`; mô tả thành "Publisher JetStream: sharding theo slot, hàng đợi, số ack đang chờ, timeout ack; phát theo thứ tự từng room (D50)".

**Step 12: Chạy toàn bộ**

Run: `make test`
Expected: PASS.

Run: `make vet && make lint`
Expected: sạch. Grep `WatermarkKey|pubwm|ActiveKey|chatim:active` trong `apps/` phải trống (trừ docs).

**Step 13: Commit**

```bash
make fmt-check
git add -A apps/core README.md
git commit -m "refactor(publish): keep per-room order without redis watermarks"
```

Body: "D50: a later batch of a room waits until its earlier batch is acked or gives up; other rooms flow. Events are best-effort (D47)."

---

### Task 4: Bỏ pts khỏi domain, store, dedupe, proto và tools (D48)

**Files:**
- Modify: `apps/core/internal/domain/message.go`
- Modify: `apps/core/internal/store/ports.go`, `store/memstore/memstore.go:56-68`, `store/mongostore/{codec,messages}.go`
- Modify tests: `store/validate_test.go:29`, `store/storetest/{fixtures,read_cases,insert_cases}.go`, `store/mongostore/{codec_test,query_integration_test}.go`, `flush/fake_store_test.go:109`
- Modify: `apps/core/internal/dedupe/records.go` + `{harness_test,commit_abort_test,value_codec_test}.go`
- Modify: `apps/core/internal/actor/{config,room_state}.go` + test `{harness_test,competing_writers_test,reconcile_test,send_rules_test,slot_eviction_test,cid_registry_test,spy_stores_test}.go`
- Modify: `apps/core/internal/grpcsrv/core_service.go:83` + `{send_message_test,get_history_test}.go`
- Modify: `apps/core/internal/pbconv/pbconv.go` + `pbconv_test.go`
- Modify: `proto/chatim/v1/{core,events}.proto`, rồi `make proto` (sinh lại `pkg/pb`)
- Modify: `apps/core/load_driver_test.go:29,81`
- Modify: `apps/core/internal/publish/harness_test.go` (bỏ `Pts: s`)
- Modify: `tools/corecli/internal/e2e/{check,events,state}.go` + `{check_test,state_test}.go`, `tools/corecli/{e2e_check,e2e_send}.go`, `scripts/e2e.sh:37`

**Step 1: Proto**

`core.proto`:
- `message Message`: xoá dòng `uint64 pts = 4;`, thêm `reserved 4; reserved "pts";` ở đầu message.
- `message SendMessageResponse`: xoá `uint64 pts = 2;`, thêm `reserved 2; reserved "pts";`.

`events.proto`: `message Event` xoá `uint64 pts = 7;`, thêm `reserved 7; reserved "pts";`.

Run: `make proto && make buf-lint`
Expected: sinh lại `pkg/pb/chatim/v1/*.pb.go`, lint sạch.

**Step 2: Biên dịch để thấy mọi chỗ cần sửa**

Run: `make -s go ARGS="vet ./..."`
Expected: FAIL, liệt kê các chỗ dùng `GetPts`, `Pts:`. Đây là danh sách việc của các step sau.

**Step 3: Domain + store**

- `domain/message.go`: xoá field `Pts`.
- `store/ports.go`: `Last(ctx context.Context, room, thread uint64) (uint64, error)`.
- `memstore.Last` trả `(uint64, error)` với `last.Seq`.
- `mongostore/messages.go`: `Last` trả `(uint64, error)`, return `msgs[0].Seq, nil`.
- `mongostore/codec.go`:
  - xoá field `Pts` khỏi `messageDoc`;
  - trong `encodeMessage` xoá khối `pts, err := toInt64("pts", m.Pts)` và `Pts: pts,`;
  - trong `decodeMessage` xoá khối `toUint64("message pts", ...)` và `Pts: pts,`.

  Doc cũ trong dev còn field `p` vẫn đọc được vì driver bỏ qua field lạ. Dev vẫn nên `make infra-reset`.

**Step 4: Test store**

- `storetest/fixtures.go:31`: xoá dòng `Pts:`.
- `storetest/read_cases.go`:
  - đổi tên case `"highest seq and its pts per timeline"` thành `"highest seq per timeline"`;
  - `assertLast(t, s, room, thread, wantSeq uint64)` gọi `seq, err := s.Last(...)` và báo `Last(%d, %d) = %d, %v; want %d, nil`;
  - mọi lời gọi `assertLast` bỏ đối số pts cuối;
  - trong `lastCancelled` đổi `_, _, err :=` thành `_, err :=`.
- `storetest/insert_cases.go:47,54`: bỏ `changed.Pts = ...` (giữ `Text` và `CID`); `assertLast(t, s, roomA, mainThread, orig.Seq)`.
- `store/validate_test.go:29`: bỏ `Pts: 99`.
- `mongostore/codec_test.go`:
  - bỏ `Pts: 99` ở fixture;
  - xoá case `"pts above max int64"`;
  - `TestEncodeMessageAcceptsMaxInt64` chỉ kiểm `Seq`;
  - test doc hỏng bỏ case `"negative pts"`.
- `mongostore/query_integration_test.go`:
  - dòng 24 bỏ `m.Pts`;
  - các dòng 127-131: đổi `hugePts` thành một message có seq vượt `MaxInt64`, gán `hugeSeq.Seq = math.MaxInt64 + 1`. Nếu `store.KeyOf(...).Validate()` đã chặn seq đó thì giữ kỳ vọng outcome `Rejected` như cũ; nếu khác, xoá phần tử đó khỏi batch và cập nhật danh sách outcome mong đợi.
- `flush/fake_store_test.go:109`: bỏ `Pts: seq`.

**Step 5: Dedupe record**

`dedupe/records.go`:
- `Record` thành `struct { Seq uint64; CreatedAt time.Time }` (viết trên nhiều dòng theo gofmt);
- `committedValue`: `committedPrefix + strconv.FormatUint(r.Seq, 10) + ":" + strconv.FormatInt(r.CreatedAt.UnixMilli(), 10)`;
- `parseCommitted`:

```go
func parseCommitted(v string) (Record, bool) {
	fields := strings.Split(v, ":")
	if len(fields) != 3 || fields[0]+":" != committedPrefix {
		return Record{}, false
	}
	seq, errSeq := strconv.ParseUint(fields[1], 10, 64)
	ms, errMs := strconv.ParseInt(fields[2], 10, 64)
	if errSeq != nil || errMs != nil || seq == 0 {
		return Record{}, false
	}
	r := Record{Seq: seq, CreatedAt: time.UnixMilli(ms).UTC()}
	if committedValue(r) != v {
		return Record{}, false
	}
	return r, true
}
```

Test dedupe:
- bỏ `Pts:` trong `harness_test.go:29`, `commit_abort_test.go:12`, `value_codec_test.go:18-19`;
- hàm so sánh `harness_test.go:83` bỏ `a.Pts == b.Pts`;
- nếu `value_codec_test` có case chuỗi `c:...:...:...` 4 trường thì đổi sang 3 trường, và thêm case `"c:7:7:1"` (định dạng cũ) vào danh sách giá trị **không** hợp lệ.

**Step 6: Actor**

- `actor/config.go`: `Ack` thành `struct { Seq uint64; CreatedAt time.Time }`; `ackOf` trả `Ack{Seq: m.Seq, CreatedAt: m.CreatedAt}`. Cast `Ack(v.Record)` và `dedupe.Record(l.ack)` vẫn hợp lệ vì hai struct cùng field.
- `actor/write_group.go:111`: `e.msg.Seq, e.msg.CreatedAt = next, now`.
- `actor/room_state.go:35`: `seq, err := a.r.msgs.Last(ctx, a.id, 0)`.
- Test actor:
  - `harness_test.go:176-182` bỏ phần so `Pts`;
  - `competing_writers_test.go:127-128` chỉ kiểm `doc.Seq != want` (`"room %d position %d: seq %d, want %d"`);
  - `reconcile_test.go:95` bỏ `Pts: seq`;
  - `send_rules_test.go:163-164` chỉ kiểm `ack.Seq != 1`;
  - `slot_eviction_test.go:72-73` đổi `evs[0].GetPts() != 1` thành `evs[0].GetSeq() != 1`, thông báo "want only seq 1 of the settled group";
  - `cid_registry_test.go:18` bỏ `Pts: 7`;
  - `spy_stores_test.go:38` chữ ký `Last` trả `(uint64, error)`, sửa thân hàm tương ứng.

**Step 7: grpcsrv + pbconv**

- `grpcsrv/core_service.go:83`: `&chatimv1.SendMessageResponse{Seq: ack.Seq, CreatedAt: timestamppb.New(ack.CreatedAt)}`.
- Test grpcsrv:
  - `send_message_test.go:23,26` bỏ `Pts: 9`;
  - `send_message_test.go:118-119` chỉ kiểm seq;
  - `get_history_test.go:19-23` bỏ `GetPts`/`Pts`.
- `pbconv.go`: xoá `Pts: m.Pts,` trong `Message` và `MessageCreated`. Test `pbconv_test.go`: bỏ `Pts: 7` ở fixture và hai `want`.

**Step 8: publish harness + app test**

- `publish/harness_test.go`: bỏ `Pts: s` trong `events`.
- `apps/core/load_driver_test.go`: bỏ field `pts` trong struct `sent` (dòng 29) và `pts: resp.GetPts()` (dòng 81). Mọi chỗ còn đọc `.pts` thì đổi sang `.seq`.

**Step 9: Tools e2e**

`tools/corecli/internal/e2e/state.go`: `Ack` bỏ `Pts`.

`tools/corecli/internal/e2e/events.go`:
- `Event` thay `Pts uint64 \`json:"pts"\`` bằng `ID string \`json:"id"\``;
- `EventOf` gán `ID: ev.GetId()`.

`tools/corecli/internal/e2e/check.go`: thay toàn bộ bằng:

```go
package e2e

import (
	"fmt"
	"strconv"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const missingListed = 10

type Coverage struct {
	Distinct     int
	Duplicates   int
	MissingCount int
	Missing      []uint64
}

func CheckAcks(acks []Ack) error {
	cids := make(map[string]uint64, len(acks))
	for i, a := range acks {
		want := uint64(i + 1)
		switch prev, dup := cids[a.CID]; {
		case a.Seq != want:
			return fmt.Errorf("ack %d (cid %s) has seq %d, want %d", i+1, a.CID, a.Seq, want)
		case dup:
			return fmt.Errorf("cid %s acked twice, at seq %d and %d", a.CID, prev, a.Seq)
		}
		cids[a.CID] = a.Seq
	}
	return nil
}

func CheckPage(want []Ack, got []*chatimv1.Message, room, sender string) error {
	if len(got) != len(want) {
		return fmt.Errorf("history returned %d messages, want %d", len(got), len(want))
	}
	for i, m := range got {
		a := want[i]
		switch {
		case m.GetSeq() != a.Seq:
			return fmt.Errorf("message %d has seq %d, want %d", i, m.GetSeq(), a.Seq)
		case m.GetCid() != a.CID:
			return fmt.Errorf("seq %d has cid %q, want %q", a.Seq, m.GetCid(), a.CID)
		case m.GetRoomId() != room:
			return fmt.Errorf("seq %d belongs to room %s, want %s", a.Seq, m.GetRoomId(), room)
		case m.GetThreadRoot() != 0:
			return fmt.Errorf("seq %d is in thread %d, want the main timeline", a.Seq, m.GetThreadRoot())
		case m.GetSender() != sender:
			return fmt.Errorf("seq %d has sender %q, want %q", a.Seq, m.GetSender(), sender)
		case m.GetText() != TextFor(a.CID):
			return fmt.Errorf("seq %d has text %q, want %q", a.Seq, m.GetText(), TextFor(a.CID))
		}
	}
	return nil
}

func MessageEventID(room string, seq uint64) string {
	return room + "-0-" + strconv.FormatUint(seq, 10)
}

func CheckEvents(acks []Ack, room string, events []Event) (Coverage, error) {
	bySeq := make(map[uint64]Ack, len(acks))
	for _, a := range acks {
		bySeq[a.Seq] = a
	}
	seen := make(map[uint64]bool, len(acks))
	var cov Coverage
	for _, ev := range events {
		a, known := bySeq[ev.Seq]
		switch {
		case ev.Room != room:
			return cov, fmt.Errorf("live event for other room %s (seq %d), want only room %s", ev.Room, ev.Seq, room)
		case !known:
			return cov, fmt.Errorf("live event with unexpected seq %d (cid %q)", ev.Seq, ev.CID)
		case ev.CID != a.CID:
			return cov, fmt.Errorf("live event seq %d has cid %q, want %q", ev.Seq, ev.CID, a.CID)
		case ev.ID != MessageEventID(room, ev.Seq):
			return cov, fmt.Errorf("live event seq %d has id %q, want %q", ev.Seq, ev.ID, MessageEventID(room, ev.Seq))
		case seen[ev.Seq]:
			cov.Duplicates++
			continue
		}
		seen[ev.Seq] = true
		cov.Distinct++
	}
	for _, a := range acks {
		if seen[a.Seq] {
			continue
		}
		cov.MissingCount++
		if len(cov.Missing) < missingListed {
			cov.Missing = append(cov.Missing, a.Seq)
		}
	}
	return cov, nil
}
```

Test e2e:
- `check_test.go`:
  - `acks` bỏ `Pts: seq`; `messagesOf` bỏ `Pts: a.Pts`;
  - `TestCheckAcksWantsContiguousSeqAndPtsWithDistinctCIDs` đổi tên thành `TestCheckAcksWantsContiguousSeqWithDistinctCIDs` và xoá khối `pts`;
  - `TestCheckPage...` xoá case `"pts"`;
  - `TestCheckEventsDedupesByPtsAndReportsWhatIsMissing` đổi tên thành `TestCheckEventsDedupesBySeqAndReportsWhatIsMissing`; mọi `e2e.Event{Room: room, Pts: x, Seq: y, CID: c}` thành `e2e.Event{Room: room, ID: e2e.MessageEventID(room, y), Seq: y, CID: c}`;
  - map `bad`:

```go
	bad := map[string]e2e.Event{
		"unexpected seq": {Room: room, ID: e2e.MessageEventID(room, 6), Seq: 6, CID: "x"},
		"other room":     {Room: "7", ID: e2e.MessageEventID("7", 1), Seq: 1, CID: want[0].CID},
		"cid":            {Room: room, ID: e2e.MessageEventID(room, 2), Seq: 2, CID: "x"},
		"id":             {Room: room, ID: room + "-3", Seq: 3, CID: want[2].CID},
	}
```

  - `TestMissingListIsBounded`: thông báo "from seq 1".
- `state_test.go:37,42,54,58`:
  - chuỗi JSON đổi `"pts":N` thành `"id":"42-0-N"`;
  - struct mong đợi dùng `ID: "42-0-2"`;
  - event proto bỏ `Pts: 4`, thêm `Id: "42-0-4"`; mong đợi `ID: "42-0-4"`.

`tools/corecli/e2e_send.go:91,104`: ack thành `e2e.Ack{CID: cid, Seq: resp.GetSeq()}`, thông báo `"resend of acked cid %s returned seq %d, want the original seq %d", last.CID, got.Seq, last.Seq`.

`tools/corecli/e2e_check.go:25,52,125`: chữ "pts" → "seq" trong mô tả flag và hai thông báo.

`scripts/e2e.sh:37`: `every pts live` → `every acked seq live`.

**Step 10: Chạy toàn bộ**

Run: `make test`
Expected: PASS.

Run: `make vet && make lint && make buf-lint`
Expected: sạch. Grep `Pts\b|GetPts|\.pts\b|"pts"` trong `apps/ tools/ pkg/keys` chỉ còn `reserved "pts"` trong proto và code sinh ra.

**Step 11: Commit**

```bash
make fmt-check
git add -A apps tools proto pkg/pb scripts
git commit -m "refactor(core): drop room-wide pts from messages, events and acks"
```

Body: "D48: event identity is the natural key {room}-{thread}-{seq}; pts fields are reserved in the protos."

---

### Task 5: Docs — design, roadmap, CLAUDE.md, README, INDEXES

**Files:**
- Modify: `docs/designs/260930-chat-core-gateway-design.md`
- Modify: `docs/roadmap.md`
- Modify: `CLAUDE.md`, `README.md`, `INDEXES.csv`

**Step 1: Design**

- §2 A2 (dòng 50): đổi thành "Đã ack = đã lưu `w:majority`; thứ tự tin trong timeline thống nhất cho mọi người (theo seq); event phát best-effort, mỗi event có id tự nhiên để người nhận bỏ trùng; mất event do module đối soát định kỳ bù (D47, D48)".
- §4 dòng 87-89 "Hai loại số thứ tự": chỉ giữ `seq`. Thêm đoạn: "Không có bộ đếm event toàn room. Id event là khoá tự nhiên suy ra từ doc: tạo tin `{room}-{thread}-{seq}`; các loại thay đổi (M2b) dùng version của doc đích (D48, D51)."
- §4 bảng collection:
  - `rooms` bỏ `last_pts`;
  - `messages` bỏ `p (pts)`;
  - `room_events` ghi chú "chưa dùng; xem lại ở M2b/M3".
- §5.2 dòng 179 `③ seq = last_seq+1, pts = last_pts+1` → `③ seq = last_seq+1`. Dòng 182 bỏ `pts`. Dòng 183 "nạp lại last_seq/pts" → "nạp lại last_seq".
- §5.4: thay toàn bộ nội dung bằng:

```markdown
### 5.4 Publish best-effort (D47–D51)

1. Ack client ngay khi DB commit. Sau đó actor đưa event vào publisher; mỗi event có `Nats-Msg-Id` = id tự nhiên (tạo tin: `{room}-{thread}-{seq}`), nên JetStream bỏ trùng trong cửa sổ 2 phút.
2. Publisher sharding theo slot. Mỗi lần actor `Enqueue` là một batch; batch sau của cùng room chỉ gửi khi batch trước đã có ack hoặc hết hạn mức retry; các room khác không phải chờ (D50). Thứ tự này đảm bảo thay đổi của một tin (M2b) phát sau chính tin đó.
3. Không có watermark, active mark hay sweeper. Event mất khi core chết giữa commit và publish, khi NATS gián đoạn lâu hơn hạn mức retry, hoặc khi hàng đợi đầy. Client không dò thiếu bằng số; khi connect/reconnect client lấy bản mới nhất (doc tin luôn giữ trạng thái hiện tại). Các app cần đủ event dựa vào **module đối soát event định kỳ** (Phase 2), module này dựng lại event từ doc với đúng id (D51).
4. Lúc dừng, publisher drain trong `CORE_PUBLISHER_DRAIN`, chỉ abort khi hết giờ.
```

- §6 bảng: dòng `Sync` thêm ghi chú "(thiết kế lại ở M3: không phát lại; reconnect lấy mới nhất)".
- §7 dòng 233 "client kết nối lại và `Sync`" → "client kết nối lại và lấy bản mới nhất". Dòng 250 → "Gateway không sắp lại thứ tự; client sắp tin theo seq và bỏ trùng event theo id".
- §8 dòng 255-256: envelope bỏ `pts`; consumer "bỏ trùng theo id event; thứ tự event của một room theo thứ tự publisher phát (D50)".
- §9:
  - dòng 265 (NATS chết): "DB vẫn ghi; event trong lúc gián đoạn có thể mất sau hạn mức retry; client reconnect lấy mới nhất; đối soát bù cho app";
  - dòng 266: bỏ "watermark/active mark tạm ngừng, sweeper publish bù khi Redis sống lại";
  - dòng 268–269: bỏ "publish bù theo watermark"/"sweeper publish bù", thay "event chưa publish có thể mất (D47)";
  - xoá dòng 271 (TTL watermark).
- §11 dòng 287: bỏ "kiểm tra publish bù". Dòng 291 "consumer nhận đủ mọi pts" → "mọi `cid` đã ack có đúng 1 bản trong DB".
- §12 dòng 295: bỏ "độ trễ publish (`pts` DB − watermark)", thay "số event publish bị bỏ (abandoned/queue full)".
- §14:
  - D19 thêm "— **thay bởi D47** (2026-10-03)";
  - D36, D37, D38 thêm "— **thay bởi D49**";
  - D39 thêm "(publisher: drain, không còn dựa vào sweeper — D49)";
  - thêm năm dòng D47–D51 theo bảng ở `.claude/plans/m2b-core-mutations_design.md`, cột "Phương án khác" ghi các phương án đã loại (bộ đếm +1 RTT, thuê khối, id kiểu Snowflake, tách hai dãy, log sự kiện).
- Header file: thêm dòng "Cập nhật 2026-10-03: M2a.1 đổi hướng đánh số và publish (D47–D51)".

**Step 2: Roadmap**

Sửa `docs/roadmap.md` đúng như mục "Thay đổi `docs/roadmap.md`" của session plan:
- thêm dòng M2a.1 vào bảng;
- sửa nội dung M2b, M3, M4;
- thêm Phase 2: "Module đối soát event" và "Policy quyền";
- thứ tự phụ thuộc `M2a → M2a.1 → M2b → …`;
- thêm mục ngắn "## M2a.1 — lý do" (3–5 dòng, trỏ D47–D51);
- dòng "Cập nhật" đầu file ghi 2026-10-03.

**Step 3: CLAUDE.md**

- "Done": thêm "M2a.1: bỏ pts toàn room, id event tự nhiên, publisher theo thứ tự từng room, event best-effort" (đánh dấu khi merge).
- "Next": M2b theo phạm vi mới.
- **Counters**: thay bằng "`seq` là vị trí trong timeline. Không có bộ đếm event toàn room; id event là khoá tự nhiên (`{room}-{thread}-{seq}` cho tin mới)."
- **Send path**:
  - bỏ bước 4 (`publish.ActivityMarks`);
  - bước 7 bỏ `pts`/`pubwm`: "publish.Publisher gửi tới `evt.{t}.room.{rid}.msg_created` với `Nats-Msg-Id` = id tự nhiên; batch sau của room chờ batch trước (D50); best-effort (D47)";
  - xoá bước 8 (sweeper).
- **Two Redis instances**: state bỏ `chatim:pubwm:*`, `chatim:active:*`.
- **Slots**: AfterClaim chỉ còn `EvictSlots`.
- **Process lifecycle**:
  - start order `publisher → flusher → router → slot manager → gRPC`;
  - shutdown bỏ `sweeper`.
- **Verification table**: bỏ mọi nhắc tới recovery.

**Step 4: README**

- Mục "Kiến trúc": bỏ câu về watermark/sweeper nếu có.
- Bảng env: đã sửa ở task 2–3, rà lại.
- Mục `make e2e`: mô tả "no loss/dup" vẫn đúng với kịch bản hiện tại.

**Step 5: INDEXES.csv**

- Xoá dòng `apps/core/internal/recovery`.
- `apps/core/internal/actor`: purpose bỏ "active mark before insert", "recovery replay"; key_symbols bỏ `Router.Recover`, `ActivityMarker`; decisions bỏ `D36`, thêm `D48`.
- `apps/core/internal/publish`:
  - purpose thành "JetStream publisher sharded by slot; per-room batch order (a room's next batch waits for its earlier batch); best-effort with bounded retries; Msg-Id = event id; EnsureStream with RePublish to live.*";
  - key_symbols bỏ `Skip`, `ActivityMarks`, `NewActivityMarks`, `ActivityMarks.Mark`, `WatermarkKey`, `ActiveKey`;
  - decisions `D47;D50`;
  - tests bỏ "Msg-Id dedupe and RePublish" chỉ khi đã đổi, nếu không giữ.
- `apps/core/internal/pbconv`: key_symbols `EventID` → `MessageEventID`; purpose "EventID {room}-{pts}" → "MessageEventID {room}-{thread}-{seq}".
- `apps/core/internal/dedupe`: purpose `c:{seq}:{pts}:{ms}` → `c:{seq}:{ms}`.
- `apps/core/internal/store`: không đổi tên symbol; nếu purpose nhắc pts thì bỏ.
- `apps/core`: purpose bỏ "state: marks/watermarks/sweeper"; thành "two Redis instances (state: slot pool; dedupe: cid)"; decisions bỏ D39 phần sweeper nếu ghi.
- `apps/core/internal/config`: bỏ nhắc `RECOVERY_*`, `PUB_WATERMARK_TTL`.
- `proto/chatim/v1/core.proto`, `events.proto`: purpose ghi "pts reserved (D48)".
- `docs/roadmap.md`: purpose "(M2a done; M2a.1 next, then M2b)".
- Thêm dòng `docs/plans/2026-10-03-m2a1-event-identity.md,doc,M2a.1 implementation plan,,everyone,,D47-D51`.

**Step 6: Kiểm nhanh và commit**

Run: `grep -rn "pubwm\|chatim:active\|Sweeper\|RECOVERY_" CLAUDE.md README.md INDEXES.csv docs/roadmap.md`
Expected: không còn (trừ các dòng lịch sử ghi rõ "thay bởi").

```bash
git add docs CLAUDE.md README.md INDEXES.csv
git commit -m "docs: describe best-effort events with natural ids and the M2a.1 milestone"
```

---

### Task 6: Kiểm chứng cuối phase (DoD)

**Step 1: Unit + lint**

Run: `make fmt-check && make vet && make lint && make test`
Expected: tất cả PASS.

**Step 2: Integration**

```bash
make infra-reset
make infra-up
make itest
```

Expected: PASS. Các itest của `recovery` đã bị xoá cùng package. `TestRealJetStreamDedupesByMsgIDAndRepublishesLive` và `shutdown_integration_test` vẫn chạy.

**Step 3: E2E**

```bash
make core-up && make e2e
```

Expected: `e2e PASS: ... every acked seq live`. Kịch bản chỉ kill core sau khi đã thấy event của nhóm tin đầu, nên D47 không làm hỏng phép kiểm. Nếu fail ở bước "live", **dừng và báo cáo** kèm log, không nới phép kiểm.

**Step 4: Corebench**

```bash
make poc TOOL=corebench ARGS="-rate 5000 -duration 60s -watch 20"
```

Expected: `failed=0`. Ghi số (acked/s, p50/p95/p99, live lag) thành một dòng mới trong `docs/poc/README.md#c1…` kèm nhãn "M2a.1", để so với số M2a. Commit riêng: `docs(poc): record corebench after M2a.1`.

**Step 5: Redis state không còn key cũ**

Run: `make redis-cli ARGS="--scan --pattern 'chatim:pubwm:*' --count 100"`. Lệnh này chỉ dùng một lần trên dev sau `infra-reset`; quy tắc "không SCAN" áp cho code, không áp cho kiểm tay.
Expected: rỗng.

**Step 6: PR**

- Push branch, mở PR `fix/m2a1-event-identity` → `main`.
- Mô tả PR: mục tiêu, D47–D51, những gì bị gỡ, readiness `dev-done`, kết quả `test`/`itest`/`e2e`/corebench.
- PR mang dòng kết thúc theo quy định repo.

---

## Ghi chú cho người thực thi

- Grep nhanh để không sót:
  - `rg -n "Pts|GetPts|EventID\(|Skip\(|ActivityMark|WatermarkKey|ActiveKey|recovery\." apps tools`
  - `rg -n "RECOVERY_|PUB_WATERMARK|PUB_FLUSH_EVERY" .`
- Nếu một test cũ kiểm hành vi đã bị D47/D49 bỏ (watermark, skip, sweeper, publish bù), **xoá test đó**, không chuyển sang giữ hành vi cũ.
- Không đụng `apps/core/internal/slot` hay `pkg/slotmap`, nên không cần chạy R5.
