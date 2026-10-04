# M2a.2 — Reconcile event — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: dùng `subagent-driven-development` (cùng phiên) hoặc `separate-driven-development` (phiên riêng) để thực thi plan này theo từng task.

**Goal:** Mọi tin đã commit cuối cùng có event trong `CHATIM_EVT`. Reconciler trong core đọc nhật ký commit của DB, chỉ publish lại event mà đường thường chưa được NATS xác nhận.

**Architecture:**
- Port `store.ChangeFeed` / `store.Cursor` (không phụ thuộc DB). Adapter Mongo đọc change stream của `messages`, lưu vị trí trong `reconciler_state`. memstore có feed trong RAM cho unit test. Contract chung trong `storetest`; adapter Postgres sau này phải qua cùng contract.
- Publisher, sau `PubAck`, đánh dấu tin đã ack lên Redis dedupe bằng bitmap (`chatim:evtack:{room}:{thread}:{seq>>13}`, 1 bit mỗi tin, TTL 1h, package `eventmark`). Ngoài đường ack, gom theo batch.
- Package `reconcile`: chỉ chạy trên core giữ slot 0. Đọc thay đổi, chờ tới `CommittedAt + D` (D = 30s), tra mark theo batch, publish phần chưa có mark qua JetStream client riêng, cửa sổ K publish đang bay theo thứ tự, gửi lại vô hạn. Chỉ `Confirm` vị trí khi mọi thay đổi trước đó đã ack hoặc đã có mark.
- `EVT_STREAM_DUPLICATES` mặc định 5m. Boot kiểm `RECONCILE_DELAY < EVT_STREAM_DUPLICATES`.

**Tech Stack:** Go 1.26 trong Docker qua `make`; mongo-driver v2 (change stream); nats.go v1.54 jetstream; go-redis v9 + miniredis; `testing/synctest`; goleak.

**Nguồn quyết định:** `.claude/plans/m2a2-event-reconcile_design.md` (R1–R10, RC1–RC5), D47–D52 trong `docs/designs/260930-chat-core-gateway-design.md`.

---

## Quy tắc chung (đọc trước khi làm task nào)

- **Không chạy `go` trên host.** Test một package: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/<pkg>/..."`.
- **Không viết comment** trong Go, test, YAML, shell, Makefile.
- File code dưới 200 dòng. Giữ `make fmt-check` sạch.
- `gosec` bật cho code không phải test: không chuyển kiểu số nguyên khi chưa chặn biên (G115). Plan đã viết sẵn các chỗ chặn.
- Trước mỗi commit: `make fmt-check`, `make vet`, `make lint` + test các package đã đụng. Goroutine/channel mới: thêm `-count=5` cho package đó.
- Commit theo Conventional Commits, không nhắc AI, không có dòng `Co-Authored-By` (skill `commit` của repo).
- Branch: `feat/m2a2-event-reconcile` (đã tạo từ `main` sau khi merge M2a.1).
- Khi một bước cho kết quả khác "Expected": **dừng lại và báo cáo**, không vá cho tới khi qua.
- Task 3, 4, 5 rủi ro (publish, reconcile, lifecycle): mỗi task 1 reviewer kiểm spec + chất lượng trong một lượt. Task 1, 2, 6, 7, 8 do controller kiểm nhanh. Task 2 và 3 đụng file khác nhau: review task 2 trong lúc làm task 3.

## Thứ tự task

| # | Task | Vì sao ở vị trí này |
|---|---|---|
| 1 | Port `ChangeFeed` + contract + memstore feed | Nền cho task 2 và 4 |
| 2 | Adapter Mongo + `reconciler_state` + itest | Xác nhận change stream trên clustered collection sớm |
| 3 | `eventmark` + publisher đánh dấu tin đã ack | Độc lập với 1–2 |
| 4 | Package `reconcile` | Cần 1 và 3 |
| 5 | Config, wiring, lifecycle, `EVT_STREAM_DUPLICATES=5m` | Cần 2, 3, 4 |
| 6 | Itest end-to-end trong `apps/core` | Cần 5 |
| 7 | Docs | Mô tả trạng thái mới |
| 8 | Kiểm chứng cuối phase (DoD) + corebench | |

**Khác với khung task lúc brainstorm:** kịch bản e2e `docker pause chatim-nats` bị bỏ. nats.go giữ các publish trong bộ đệm reconnect (8MB) và gửi lại khi kết nối về, nên pause NATS không chắc làm mất event và phép kiểm không tất định. Thay bằng itest trong `apps/core` (task 6): ghi tin thẳng vào Mongo, không qua core (đúng trường hợp migrator Phase 2), và chờ event trên `live.*`. `make e2e` hiện có vẫn chạy với reconciler bật ở task 8.

---

### Task 1: Port `ChangeFeed`, contract chung, feed của memstore

**Files:**
- Create: `apps/core/internal/store/feed.go`
- Create: `apps/core/internal/store/storetest/feed_cases.go`
- Create: `apps/core/internal/store/memstore/feed.go`
- Create: `apps/core/internal/store/memstore/feed_test.go`
- Modify: `apps/core/internal/store/memstore/memstore.go` (struct `Messages`, `NewMessages`, `insertLocked`)
- Modify: `apps/core/internal/store/memstore/memstore_test.go`

**Step 1: Port**

`apps/core/internal/store/feed.go`:

```go
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var (
	ErrFeedHistoryLost = fmt.Errorf("change feed history lost: %w", apperr.ErrUnavailable)
	ErrFeedBusy        = fmt.Errorf("change feed held by another consumer: %w", apperr.ErrUnavailable)
	ErrCorruptChange   = fmt.Errorf("change feed returned a corrupt change: %w", apperr.ErrFailedPrecondition)
)

type Position []byte

type Change struct {
	Msg         domain.Message
	CommittedAt time.Time
	Position    Position
}

type ChangeFeed interface {
	Open(ctx context.Context) (Cursor, error)
	Forget(ctx context.Context) error
}

type Cursor interface {
	Next(ctx context.Context) (Change, error)
	Confirm(ctx context.Context, pos Position) error
	Close(ctx context.Context) error
}
```

Ý nghĩa (để reviewer đối chiếu, không ghi vào code):
- `Open` tự nạp vị trí đã xác nhận; chưa có thì đọc từ lúc mở. Lỗi `ErrFeedHistoryLost` hoặc `ErrFeedBusy` có thể đến từ `Open` hoặc `Next`.
- `Next` trả thay đổi theo thứ tự commit. `ErrCorruptChange` nghĩa là cursor đã đi qua thay đổi đó; gọi `Next` tiếp được.
- `Confirm` lưu bền, không bao giờ lùi.
- `Forget` xoá vị trí đã lưu (lần `Open` sau đọc từ lúc mở).

**Step 2: Viết contract (test hỏng vì chưa có adapter)**

`apps/core/internal/store/storetest/feed_cases.go`:

```go
package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const feedWait = 10 * time.Second

type feedCase struct {
	name string
	run  func(t *testing.T, msgs store.Messages, feed store.ChangeFeed)
}

func RunFeed(t *testing.T, open func(t *testing.T) (store.Messages, store.ChangeFeed)) {
	t.Helper()
	for _, c := range feedCases() {
		t.Run(c.name, func(t *testing.T) {
			msgs, feed := open(t)
			c.run(t, msgs, feed)
		})
	}
}

func feedCases() []feedCase {
	return []feedCase{
		{"new inserts come out in commit order with their content", feedOrder},
		{"inserts before the first open are not replayed", feedStartsNow},
		{"reopen resumes after the confirmed position", feedResume},
		{"an older confirm does not move the position back", feedNoRewind},
		{"forget restarts from now", feedForget},
	}
}

func openCursor(t *testing.T, feed store.ChangeFeed) store.Cursor {
	t.Helper()
	cur, err := feed.Open(t.Context())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = cur.Close(context.Background()) })
	return cur
}

func closeCursor(t *testing.T, cur store.Cursor) {
	t.Helper()
	if err := cur.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func confirm(t *testing.T, cur store.Cursor, c store.Change) {
	t.Helper()
	if err := cur.Confirm(t.Context(), c.Position); err != nil {
		t.Fatalf("Confirm(seq %d): %v", c.Msg.Seq, err)
	}
}

func nextChanges(t *testing.T, cur store.Cursor, n int) []store.Change {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), feedWait)
	defer cancel()
	out := make([]store.Change, 0, n)
	for range n {
		c, err := cur.Next(ctx)
		if err != nil {
			t.Fatalf("Next after %d changes: %v", len(out), err)
		}
		if c.CommittedAt.IsZero() || len(c.Position) == 0 {
			t.Fatalf("change for seq %d has no commit time or position: %+v", c.Msg.Seq, c)
		}
		out = append(out, c)
	}
	return out
}

func messagesOf(cs []store.Change) []domain.Message {
	out := make([]domain.Message, len(cs))
	for i, c := range cs {
		out[i] = c.Msg
	}
	return out
}

func insertEach(t *testing.T, s store.Messages, msgs ...domain.Message) {
	t.Helper()
	for _, m := range msgs {
		mustInsert(t, s, []domain.Message{m})
	}
}

func feedOrder(t *testing.T, msgs store.Messages, feed store.ChangeFeed) {
	cur := openCursor(t, feed)
	want := []domain.Message{msg(roomA, mainThread, 1), msg(roomB, sideThread, 1), msg(roomA, mainThread, 2)}
	insertEach(t, msgs, want...)
	assertMessages(t, messagesOf(nextChanges(t, cur, len(want))), want)
}

func feedStartsNow(t *testing.T, msgs store.Messages, feed store.ChangeFeed) {
	insertEach(t, msgs, msg(roomA, mainThread, 1))
	cur := openCursor(t, feed)
	later := msg(roomA, mainThread, 2)
	insertEach(t, msgs, later)
	assertMessages(t, messagesOf(nextChanges(t, cur, 1)), []domain.Message{later})
}

func feedResume(t *testing.T, msgs store.Messages, feed store.ChangeFeed) {
	cur := openCursor(t, feed)
	all := span(roomA, mainThread, 1, 3)
	insertEach(t, msgs, all...)
	got := nextChanges(t, cur, 3)
	confirm(t, cur, got[1])
	closeCursor(t, cur)
	again := openCursor(t, feed)
	assertMessages(t, messagesOf(nextChanges(t, again, 1)), all[2:])
}

func feedNoRewind(t *testing.T, msgs store.Messages, feed store.ChangeFeed) {
	cur := openCursor(t, feed)
	all := span(roomA, mainThread, 1, 3)
	insertEach(t, msgs, all...)
	got := nextChanges(t, cur, 3)
	confirm(t, cur, got[1])
	confirm(t, cur, got[0])
	closeCursor(t, cur)
	again := openCursor(t, feed)
	assertMessages(t, messagesOf(nextChanges(t, again, 1)), all[2:])
}

func feedForget(t *testing.T, msgs store.Messages, feed store.ChangeFeed) {
	cur := openCursor(t, feed)
	insertEach(t, msgs, span(roomA, mainThread, 1, 2)...)
	got := nextChanges(t, cur, 2)
	confirm(t, cur, got[0])
	closeCursor(t, cur)
	if err := feed.Forget(t.Context()); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	again := openCursor(t, feed)
	fresh := msg(roomA, mainThread, 3)
	insertEach(t, msgs, fresh)
	assertMessages(t, messagesOf(nextChanges(t, again, 1)), []domain.Message{fresh})
}
```

Thêm vào `memstore_test.go`:

```go
func TestFeedContract(t *testing.T) {
	storetest.RunFeed(t, func(*testing.T) (store.Messages, store.ChangeFeed) {
		msgs := memstore.NewMessages()
		return msgs, memstore.NewFeed(msgs)
	})
}
```

**Step 3: Chạy cho thấy hỏng**

Run: `make -s go ARGS="test -race ./apps/core/internal/store/..."`
Expected: FAIL biên dịch, `undefined: memstore.NewFeed`.

**Step 4: Ghi log insert trong memstore**

Trong `memstore.go`:
- Struct `Messages` thêm hai field: `log []logged` và `grew chan struct{}`.
- `NewMessages` trả `&Messages{lines: make(map[timeline][]domain.Message), grew: make(chan struct{})}`.
- Trong `insertLocked`, ngay sau `s.lines[tl] = slices.Insert(line, i, m)`, thêm `s.appendLog(m)`.

`apps/core/internal/store/memstore/feed.go`:

```go
package memstore

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var _ store.ChangeFeed = (*Feed)(nil)

type logged struct {
	msg domain.Message
	at  time.Time
}

func (s *Messages) appendLog(m domain.Message) {
	s.log = append(s.log, logged{msg: m, at: time.Now()})
	close(s.grew)
	s.grew = make(chan struct{})
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

type Feed struct {
	msgs      *Messages
	mu        sync.Mutex
	confirmed int
	known     bool
	lost      bool
}

func NewFeed(msgs *Messages) *Feed { return &Feed{msgs: msgs} }

func (f *Feed) LoseHistory() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lost = true
}

func (f *Feed) Confirmed() (int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.confirmed, f.known
}

func (f *Feed) Open(ctx context.Context) (store.Cursor, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lost {
		return nil, store.ErrFeedHistoryLost
	}
	next := f.confirmed
	if !f.known {
		next = f.msgs.logLen()
	}
	return &cursor{feed: f, next: next}, nil
}

func (f *Feed) Forget(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confirmed, f.known, f.lost = 0, false, false
	return nil
}

func (f *Feed) confirm(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.known || n > f.confirmed {
		f.confirmed, f.known = n, true
	}
}

type cursor struct {
	feed *Feed
	next int
}

func (c *cursor) Next(ctx context.Context) (store.Change, error) {
	for {
		l, grew, ok := c.feed.msgs.logAt(c.next)
		if ok {
			c.next++
			return store.Change{Msg: l.msg, CommittedAt: l.at, Position: store.Position(strconv.Itoa(c.next))}, nil
		}
		select {
		case <-grew:
		case <-ctx.Done():
			return store.Change{}, ctx.Err()
		}
	}
}

func (c *cursor) Confirm(ctx context.Context, pos store.Position) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n, err := strconv.Atoi(string(pos))
	if err != nil || n < 0 {
		return fmt.Errorf("%w: memstore feed position %q", apperr.ErrInvalidArgument, pos)
	}
	c.feed.confirm(n)
	return nil
}

func (c *cursor) Close(context.Context) error { return nil }
```

Vị trí là chuỗi số thập phân để không phải chuyển kiểu số nguyên (G115).

**Step 5: Test riêng của memstore cho mất lịch sử**

`apps/core/internal/store/memstore/feed_test.go`:

```go
package memstore_test

import (
	"errors"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestFeedLosesHistoryUntilForgotten(t *testing.T) {
	feed := memstore.NewFeed(memstore.NewMessages())
	feed.LoseHistory()
	if _, err := feed.Open(t.Context()); !errors.Is(err, store.ErrFeedHistoryLost) {
		t.Fatalf("Open after lost history = %v, want ErrFeedHistoryLost", err)
	}
	if err := feed.Forget(t.Context()); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if _, err := feed.Open(t.Context()); err != nil {
		t.Fatalf("Open after Forget: %v", err)
	}
}

func TestFeedRejectsForeignPositions(t *testing.T) {
	cur, err := memstore.NewFeed(memstore.NewMessages()).Open(t.Context())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, pos := range []store.Position{nil, store.Position("x"), store.Position("-1")} {
		if err := cur.Confirm(t.Context(), pos); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("Confirm(%q) = %v, want ErrInvalidArgument", pos, err)
		}
	}
}
```

**Step 6: Chạy test**

Run: `make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/store/..."`
Expected: PASS (`TestContract`, `TestFeedContract`, hai test feed mới). Các test actor dùng memstore không đổi: `make -s go ARGS="test -race ./apps/core/internal/actor/..."` PASS.

**Step 7: Commit**

```bash
git add apps/core/internal/store
git commit -m "feat(store): add a database-neutral change feed port with a shared contract"
```

---

### Task 2: Adapter Mongo cho change feed

**Files:**
- Create: `apps/core/internal/store/mongostore/feed.go`
- Create: `apps/core/internal/store/mongostore/feed_integration_test.go`
- Modify: `apps/core/internal/store/mongostore/mongostore.go` (hằng `reconcilerStateCollection`)
- Modify: `apps/core/internal/store/mongostore/bootstrap.go:18` (tạo thêm `reconciler_state`)
- Modify: `apps/core/internal/store/mongostore/bootstrap_integration_test.go:98` (kiểm collection mới)

**Step 1: Viết itest hỏng**

`feed_integration_test.go`:

```go
package mongostore

import (
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/storetest"
)

func TestMongoFeedContract(t *testing.T) {
	client := itClient(t)
	storetest.RunFeed(t, func(t *testing.T) (store.Messages, store.ChangeFeed) {
		s, db := itStore(t, client)
		return s, NewFeed(db)
	})
}
```

Trong `bootstrap_integration_test.go`, ngay sau dòng `collectionOptions(t, db, roomsCollection)` ở test re-bootstrap, thêm `collectionOptions(t, db, reconcilerStateCollection)`.

**Step 2: Chạy cho thấy hỏng**

Run: `make -s go ARGS="vet ./apps/core/internal/store/mongostore/"`
Expected: FAIL `undefined: NewFeed`, `undefined: reconcilerStateCollection`.

**Step 3: Bootstrap**

Trong `mongostore.go`, khối `const` thêm `reconcilerStateCollection = "reconciler_state"`.
Trong `bootstrap.go`, vòng tạo collection đổi thành `for _, name := range []string{roomsCollection, membersCollection, reconcilerStateCollection} {`.

**Step 4: Adapter**

`apps/core/internal/store/mongostore/feed.go`:

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
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	messagesFeedID          = "messages"
	changeStreamHistoryLost = 286
	feedMaxAwait            = time.Second
)

var _ store.ChangeFeed = (*Feed)(nil)

type Feed struct {
	messages *mongo.Collection
	state    *mongo.Collection
}

type feedPosition struct {
	Token bson.Raw       `bson:"token"`
	At    bson.Timestamp `bson:"at"`
}

type changeDoc struct {
	Token        bson.Raw       `bson:"_id"`
	ClusterTime  bson.Timestamp `bson:"clusterTime"`
	WallTime     time.Time      `bson:"wallTime"`
	FullDocument messageDoc     `bson:"fullDocument"`
}

func NewFeed(db *mongo.Database) *Feed {
	majority := options.Collection().
		SetReadPreference(readpref.Primary()).
		SetReadConcern(readconcern.Majority()).
		SetWriteConcern(writeconcern.Majority())
	return &Feed{messages: db.Collection(messagesCollection, majority), state: db.Collection(reconcilerStateCollection, majority)}
}

func (f *Feed) Open(ctx context.Context) (store.Cursor, error) {
	opts := options.ChangeStream().SetMaxAwaitTime(feedMaxAwait)
	var saved feedPosition
	switch err := f.state.FindOne(ctx, bson.D{{Key: "_id", Value: messagesFeedID}}).Decode(&saved); {
	case err == nil:
		opts.SetStartAfter(saved.Token)
	case !errors.Is(err, mongo.ErrNoDocuments):
		return nil, fmt.Errorf("load change feed position: %w", err)
	}
	pipeline := mongo.Pipeline{{{Key: "$match", Value: bson.D{{Key: "operationType", Value: "insert"}}}}}
	cs, err := f.messages.Watch(ctx, pipeline, opts)
	if err != nil {
		return nil, feedError("open change stream", err)
	}
	return &feedCursor{cs: cs, state: f.state}, nil
}

func (f *Feed) Forget(ctx context.Context) error {
	if _, err := f.state.DeleteOne(ctx, bson.D{{Key: "_id", Value: messagesFeedID}}); err != nil {
		return fmt.Errorf("forget change feed position: %w", err)
	}
	return nil
}

type feedCursor struct {
	cs    *mongo.ChangeStream
	state *mongo.Collection
}

func (c *feedCursor) Next(ctx context.Context) (store.Change, error) {
	if !c.cs.Next(ctx) {
		if err := c.cs.Err(); err != nil {
			return store.Change{}, feedError("read change stream", err)
		}
		return store.Change{}, fmt.Errorf("read change stream: %w", apperr.ErrUnavailable)
	}
	var ev changeDoc
	if err := c.cs.Decode(&ev); err != nil {
		return store.Change{}, fmt.Errorf("%w: decode change event: %w", store.ErrCorruptChange, err)
	}
	m, err := decodeMessage(ev.FullDocument)
	if err != nil {
		return store.Change{}, fmt.Errorf("%w: %w", store.ErrCorruptChange, err)
	}
	pos, err := bson.Marshal(feedPosition{Token: ev.Token, At: ev.ClusterTime})
	if err != nil {
		return store.Change{}, fmt.Errorf("%w: encode position: %w", store.ErrCorruptChange, err)
	}
	return store.Change{Msg: m, CommittedAt: ev.WallTime, Position: pos}, nil
}

func (c *feedCursor) Confirm(ctx context.Context, pos store.Position) error {
	var p feedPosition
	if err := bson.Unmarshal(pos, &p); err != nil || len(p.Token) == 0 {
		return fmt.Errorf("%w: change feed position", apperr.ErrInvalidArgument)
	}
	filter := bson.D{{Key: "_id", Value: messagesFeedID}, {Key: "at", Value: bson.D{{Key: "$lt", Value: p.At}}}}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "token", Value: p.Token}, {Key: "at", Value: p.At}}}}
	_, err := c.state.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
	if err != nil && !mongo.IsDuplicateKeyError(err) {
		return fmt.Errorf("confirm change feed position: %w", err)
	}
	return nil
}

func (c *feedCursor) Close(ctx context.Context) error { return c.cs.Close(ctx) }

func feedError(op string, err error) error {
	if se, ok := errors.AsType[mongo.ServerError](err); ok && se.HasErrorCode(changeStreamHistoryLost) {
		return fmt.Errorf("%s: %w: %w", op, store.ErrFeedHistoryLost, err)
	}
	return fmt.Errorf("%s: %w", op, err)
}
```

Vì sao đúng (cho reviewer):
- Upsert có điều kiện `at < mới`. Nếu doc đã có `at ≥ mới`, filter không khớp, upsert thử chèn `_id` trùng, ra lỗi trùng khoá: coi là "không lùi", không lỗi.
- `wallTime` có từ MongoDB 6.0 (dev chạy 8.2) và có độ chính xác ms.
- Token là `_id` của chính change event, nên `StartAfter` trả đúng thay đổi kế tiếp.

**Step 5: Chạy itest**

Run: `make infra-up` (nếu chưa chạy) rồi `make -s itest 2>&1 | grep -E "mongostore|FAIL"`
Expected: `ok .../store/mongostore`. Nếu lỗi kiểu "change streams are not supported" trên clustered collection: **dừng và báo cáo** (đây là rủi ro đã ghi ở decision log).

Unit (không infra): `make -s go ARGS="test -race ./apps/core/internal/store/..."` PASS.

**Step 6: Commit**

```bash
git add apps/core/internal/store/mongostore
git commit -m "feat(mongostore): read the messages change stream as a change feed"
```

---

### Task 3: Đánh dấu tin đã ack lên Redis dedupe

**Files:**
- Create: `apps/core/internal/eventmark/store.go`
- Create: `apps/core/internal/eventmark/store_test.go`
- Create: `apps/core/internal/publish/ack_marks.go`
- Create: `apps/core/internal/publish/ack_marks_test.go`
- Modify: `apps/core/internal/publish/message.go:16` (export `Message`)
- Modify: `apps/core/internal/publish/publisher.go` (`New` nhận option, `Run`, `publish`)
- Modify: `apps/core/internal/publish/harness_test.go:52-61` (`newRig` nhận option)

**Vì sao bitmap, không phải key string mỗi tin:** decision log ghi "TTL 1h". Key string mỗi tin ở 10K tin/s × 1h = 36M key (~2.5GB), sẽ đẩy key cid committed ra khỏi Redis dedupe (`allkeys-lru`) và làm yếu CD1. seq của một timeline liền nhau, nên 1 bit mỗi tin trong chunk 8192 seq (`chatim:evtack:{room}:{thread}:{seq>>13}`) chỉ tốn vài chục MB. Hướng R2 giữ nguyên.

**Step 1: Test `eventmark` hỏng**

`apps/core/internal/eventmark/store_test.go`:

```go
package eventmark_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/eventmark"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/testlog"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestMain(m *testing.M) {
	testlog.SilenceRedis()
	goleak.VerifyTestMain(m)
}

const room uint64 = 4242

func key(thread, seq uint64) store.MsgKey { return store.MsgKey{Room: room, Thread: thread, Seq: seq} }

func newStore(t *testing.T) (*miniredis.Miniredis, *eventmark.Store) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), ContextTimeoutEnabled: true, MaxRetries: -1, DialerRetries: 1})
	t.Cleanup(func() { _ = rdb.Close() })
	s, err := eventmark.New(rdb, eventmark.Config{TTL: time.Hour, Timeout: time.Second, Cooldown: time.Second}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return mr, s
}

func TestMarkedKeysReadBackAsAcked(t *testing.T) {
	_, s := newStore(t)
	marked := []store.MsgKey{key(0, 1), key(0, 8192), key(3, 5)}
	if err := s.Mark(t.Context(), marked); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	got, err := s.Acked(t.Context(), append(slices.Clone(marked), key(0, 2), key(3, 1)))
	if err != nil {
		t.Fatalf("Acked: %v", err)
	}
	if want := []bool{true, true, true, false, false}; !slices.Equal(got, want) {
		t.Fatalf("Acked = %v, want %v", got, want)
	}
}

func TestOneKeyPerChunkWithTTL(t *testing.T) {
	mr, s := newStore(t)
	var keys []store.MsgKey
	for seq := uint64(1); seq <= 100; seq++ {
		keys = append(keys, key(0, seq))
	}
	keys = append(keys, key(0, 8192))
	if err := s.Mark(t.Context(), keys); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	want := []string{"chatim:evtack:4242:0:0", "chatim:evtack:4242:0:1"}
	if got := mr.Keys(); !slices.Equal(got, want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	for _, k := range want {
		if ttl := mr.TTL(k); ttl <= 0 || ttl > time.Hour {
			t.Fatalf("TTL(%s) = %v, want within (0, 1h]", k, ttl)
		}
	}
}

func TestMarksExpire(t *testing.T) {
	mr, s := newStore(t)
	if err := s.Mark(t.Context(), []store.MsgKey{key(0, 1)}); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	mr.FastForward(time.Hour + time.Second)
	got, err := s.Acked(t.Context(), []store.MsgKey{key(0, 1)})
	if err != nil || got[0] {
		t.Fatalf("Acked after TTL = %v, %v; want [false], nil", got, err)
	}
}

func TestRedisDownIsAnError(t *testing.T) {
	mr, s := newStore(t)
	mr.Close()
	if _, err := s.Acked(t.Context(), []store.MsgKey{key(0, 1)}); err == nil {
		t.Fatal("Acked with redis down = nil error")
	}
	if err := s.Mark(t.Context(), []store.MsgKey{key(0, 1)}); err == nil {
		t.Fatal("Mark with redis down = nil error")
	}
}

func TestEmptyInputsSkipRedis(t *testing.T) {
	mr, s := newStore(t)
	mr.Close()
	if err := s.Mark(t.Context(), nil); err != nil {
		t.Fatalf("Mark(nil) = %v", err)
	}
	if got, err := s.Acked(t.Context(), nil); err != nil || got != nil {
		t.Fatalf("Acked(nil) = %v, %v", got, err)
	}
}

func TestConfigValidate(t *testing.T) {
	for _, c := range []eventmark.Config{{TTL: time.Millisecond}, {Timeout: -time.Second}, {Cooldown: -time.Second}} {
		if err := c.Validate(); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("Validate(%+v) = %v, want ErrInvalidArgument", c, err)
		}
	}
	if err := (eventmark.Config{}).Validate(); err != nil {
		t.Fatalf("defaults: %v", err)
	}
}
```

Run: `make -s go ARGS="test ./apps/core/internal/eventmark/"`
Expected: FAIL `no non-test Go files` / `undefined: eventmark.New`.

**Step 2: `eventmark`**

`apps/core/internal/eventmark/store.go`:

```go
package eventmark

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/apps/core/internal/redisguard"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	DefaultTTL      = time.Hour
	DefaultTimeout  = 100 * time.Millisecond
	DefaultCooldown = time.Second

	keyPrefix = "chatim:evtack:"
	chunkBits = 13
	chunkMask = 1<<chunkBits - 1
)

var ErrDegraded = fmt.Errorf("event ack marks cooling down after a redis failure: %w", apperr.ErrUnavailable)

type Config struct {
	TTL      time.Duration
	Timeout  time.Duration
	Cooldown time.Duration
}

func (c Config) Validate() error { return c.withDefaults().validate() }

func (c Config) withDefaults() Config {
	c.TTL = cmp.Or(c.TTL, DefaultTTL)
	c.Timeout = cmp.Or(c.Timeout, DefaultTimeout)
	c.Cooldown = cmp.Or(c.Cooldown, DefaultCooldown)
	return c
}

func (c Config) validate() error {
	if c.TTL < time.Second || c.Timeout <= 0 || c.Cooldown <= 0 {
		return fmt.Errorf("%w: event ack marks ttl %v must be at least 1s, timeout %v and cooldown %v positive", apperr.ErrInvalidArgument, c.TTL, c.Timeout, c.Cooldown)
	}
	return nil
}

type Store struct {
	rdb   *redis.Client
	cfg   Config
	guard *redisguard.Guard
}

func New(rdb *redis.Client, cfg Config, log *slog.Logger) (*Store, error) {
	if err := redisguard.CheckClient(rdb, "event ack marks"); err != nil {
		return nil, err
	}
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	guard, err := redisguard.New(redisguard.Config{
		Name:      "event ack marks",
		Timeout:   cfg.Timeout,
		Cooldown:  cfg.Cooldown,
		Skipped:   ErrDegraded,
		Degraded:  "event ack marks degraded; reconciliation republishes unmarked events",
		Recovered: "event ack marks recovered",
		Now:       time.Now,
	}, log)
	if err != nil {
		return nil, err
	}
	return &Store{rdb: rdb, cfg: cfg, guard: guard}, nil
}

func (s *Store) Mark(ctx context.Context, keys []store.MsgKey) error {
	if len(keys) == 0 {
		return nil
	}
	return s.guard.Do(ctx, "mark", func(cctx context.Context) error {
		_, err := s.rdb.Pipelined(cctx, func(p redis.Pipeliner) error {
			touched := make(map[string]bool, len(keys))
			for _, k := range keys {
				chunk := chunkKey(k)
				p.SetBit(cctx, chunk, offset(k), 1)
				if !touched[chunk] {
					touched[chunk] = true
					p.PExpire(cctx, chunk, s.cfg.TTL)
				}
			}
			return nil
		})
		return err
	})
}

func (s *Store) Acked(ctx context.Context, keys []store.MsgKey) ([]bool, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	out := make([]bool, len(keys))
	err := s.guard.Do(ctx, "acked", func(cctx context.Context) error {
		bits := make([]*redis.IntCmd, len(keys))
		_, err := s.rdb.Pipelined(cctx, func(p redis.Pipeliner) error {
			for i, k := range keys {
				bits[i] = p.GetBit(cctx, chunkKey(k), offset(k))
			}
			return nil
		})
		if err != nil {
			return err
		}
		for i, b := range bits {
			out[i] = b.Val() == 1
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func chunkKey(k store.MsgKey) string {
	return keyPrefix + strconv.FormatUint(k.Room, 10) + ":" + strconv.FormatUint(k.Thread, 10) + ":" + strconv.FormatUint(k.Seq>>chunkBits, 10)
}

func offset(k store.MsgKey) int64 {
	bit := k.Seq & chunkMask
	if bit > math.MaxInt64 {
		return 0
	}
	return int64(bit)
}
```

Nếu `redisguard.Config` không có field `Now` thì bỏ dòng `Now` (dedupe truyền nó; kiểm `redisguard/guard.go:15-23`).

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/eventmark/"`
Expected: PASS.

**Step 3: Test publisher đánh dấu hỏng**

Trong `harness_test.go`, đổi chữ ký `newRig(t *testing.T, cfg publish.Config)` thành `newRig(t *testing.T, cfg publish.Config, opts ...publish.Option)` và gọi `publish.New(rg.js, cfg, rg.sink.Logger(), opts...)`.

`apps/core/internal/publish/ack_marks_test.go`:

```go
package publish_test

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const markFailedMsg = "marking acked events failed; reconciliation republishes them"

type recordingMarker struct {
	mu   sync.Mutex
	keys []store.MsgKey
	err  error
}

func (m *recordingMarker) Mark(_ context.Context, keys []store.MsgKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keys = append(m.keys, keys...)
	return m.err
}

func (m *recordingMarker) marked() []store.MsgKey {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := slices.Clone(m.keys)
	slices.SortFunc(out, func(a, b store.MsgKey) int {
		return cmp.Or(cmp.Compare(a.Room, b.Room), cmp.Compare(a.Seq, b.Seq))
	})
	return out
}

func closeRig(t *testing.T, rg *rig) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rg.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestAckedEventsAreMarked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := &recordingMarker{}
		rg := newRig(t, fastSetup, publish.WithAckMarks(m)).start(t)
		rg.enqueue(t, roomA, 1, 2, 3)
		rg.enqueue(t, roomB, 1)
		closeRig(t, rg)
		want := []store.MsgKey{{Room: roomA, Seq: 1}, {Room: roomA, Seq: 2}, {Room: roomA, Seq: 3}, {Room: roomB, Seq: 1}}
		if got := m.marked(); !slices.Equal(got, want) {
			t.Fatalf("marked = %v, want %v", got, want)
		}
	})
}

func TestRefusedEventsAreNotMarked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := &recordingMarker{}
		rg := newRig(t, fastSetup, publish.WithAckMarks(m)).start(t)
		rg.js.RefuseWhen(func(*nats.Msg) error { return errRefused })
		rg.enqueue(t, roomA, 1)
		closeRig(t, rg)
		if got := m.marked(); len(got) != 0 {
			t.Fatalf("marked = %v, want none", got)
		}
	})
}

func TestHeldEventsAreMarkedOnlyAfterTheirAck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := &recordingMarker{}
		rg := newRig(t, fastSetup, publish.WithAckMarks(m)).start(t)
		rg.js.Hold()
		rg.enqueue(t, roomA, 1)
		synctest.Wait()
		if got := m.marked(); len(got) != 0 {
			t.Fatalf("marked before ack = %v, want none", got)
		}
		rg.js.Release()
		closeRig(t, rg)
		if got := m.marked(); !slices.Equal(got, []store.MsgKey{{Room: roomA, Seq: 1}}) {
			t.Fatalf("marked after ack = %v", got)
		}
	})
}

func TestMarkFailureIsOnlyLogged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := &recordingMarker{err: errors.New("redis down")}
		rg := newRig(t, fastSetup, publish.WithAckMarks(m)).start(t)
		rg.enqueue(t, roomA, 1)
		closeRig(t, rg)
		if got := rg.sink.Count(markFailedMsg); got != 1 {
			t.Fatalf("%q logged %d times, want 1", markFailedMsg, got)
		}
		if ids := storedIDs(rg.js); len(ids) != 1 {
			t.Fatalf("stored = %v, want the event despite the mark failure", ids)
		}
	})
}
```

Run: `make -s go ARGS="test ./apps/core/internal/publish/"`
Expected: FAIL `undefined: publish.Option`, `undefined: publish.WithAckMarks`.

**Step 4: Export `Message`**

Trong `message.go` đổi `func message(` thành `func Message(`. Trong `publisher.go` đổi lời gọi `message(p.cfg.SubjectRoot, ...)` thành `Message(p.cfg.SubjectRoot, ...)`.

**Step 5: `ack_marks.go`**

```go
package publish

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

const (
	markQueue   = 4096
	markBatch   = 256
	markTimeout = time.Second
)

type Marker interface {
	Mark(ctx context.Context, keys []store.MsgKey) error
}

type Option func(*Publisher)

func WithAckMarks(m Marker) Option {
	return func(p *Publisher) {
		if m != nil {
			p.marks = &ackMarks{marker: m, queue: make(chan tracked, markQueue), fails: failureLog{log: p.log}, log: p.log}
		}
	}
}

type tracked struct {
	key    store.MsgKey
	future jetstream.PubAckFuture
}

type ackMarks struct {
	marker Marker
	queue  chan tracked
	full   atomic.Bool
	fails  failureLog
	log    *slog.Logger
}

func (a *ackMarks) track(key store.MsgKey, f jetstream.PubAckFuture) {
	if a == nil {
		return
	}
	select {
	case a.queue <- tracked{key: key, future: f}:
		a.full.Store(false)
	default:
		if a.full.CompareAndSwap(false, true) {
			a.log.Warn("ack mark queue full; reconciliation republishes unmarked events")
		}
	}
}

func (a *ackMarks) run(abort <-chan struct{}) {
	var batch []store.MsgKey
	for {
		var t tracked
		var open bool
		if len(batch) == 0 {
			select {
			case t, open = <-a.queue:
			case <-abort:
				return
			}
		} else {
			select {
			case t, open = <-a.queue:
			default:
				a.flush(batch)
				batch = batch[:0]
				continue
			}
		}
		if !open {
			a.flush(batch)
			return
		}
		if acked(t.future, abort) {
			batch = append(batch, t.key)
		}
		if len(batch) >= markBatch {
			a.flush(batch)
			batch = batch[:0]
		}
	}
}

func acked(f jetstream.PubAckFuture, abort <-chan struct{}) bool {
	select {
	case <-f.Ok():
		return true
	case <-f.Err():
		return false
	case <-abort:
		return false
	}
}

func (a *ackMarks) flush(keys []store.MsgKey) {
	if len(keys) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), markTimeout)
	defer cancel()
	if err := a.marker.Mark(ctx, keys); err != nil {
		a.fails.record("marking acked events failed; reconciliation republishes them", "", err)
	}
}
```

**Step 6: Nối vào publisher**

Trong `publisher.go`:
- Struct `Publisher` thêm field `marks *ackMarks`.
- `New(js JetStream, cfg Config, log *slog.Logger, opts ...Option)`: sau khi tạo `p` và trước vòng tạo shard, thêm:
  ```go
  	for _, opt := range opts {
  		opt(p)
  	}
  ```
- `Run`: thay thân hàm sau `defer close(p.done)` bằng:
  ```go
  	marking := make(chan struct{})
  	go func() {
  		defer close(marking)
  		if p.marks != nil {
  			p.marks.run(p.abort)
  		}
  	}()
  	var wg sync.WaitGroup
  	for _, s := range p.shards {
  		wg.Go(func() { p.drain(ctx, s.queue) })
  	}
  	wg.Wait()
  	if p.marks != nil {
  		close(p.marks.queue)
  	}
  	<-marking
  	return ctx.Err()
  ```
- `publish(it item)`: đổi `if _, err := p.js.PublishMsgAsync(...); err != nil {` thành:
  ```go
  		f, err := p.js.PublishMsgAsync(msg, jetstream.WithRetryAttempts(p.cfg.Attempts), jetstream.WithRetryWait(p.cfg.RetryBackoff))
  		if err != nil {
  			p.fails.record("event publish refused; reconciliation must republish it", ev.GetId(), err)
  			continue
  		}
  		p.marks.track(store.MsgKey{Room: it.room, Thread: ev.GetThreadRoot(), Seq: ev.GetSeq()}, f)
  ```
  và thêm import `github.com/ivannguyendev/chatim/apps/core/internal/store`.

Vì sao `close(p.marks.queue)` an toàn: chỉ goroutine shard gọi `track`, và mọi shard đã trả về sau `wg.Wait()`. Khi bị abort, `run` thoát qua `abort` và hàng đợi còn lại bị bỏ (mark chỉ là tối ưu).

**Step 7: Chạy test**

Run: `make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/publish/... ./apps/core/internal/eventmark/..."`
Expected: PASS, gồm mọi test publisher cũ (gọi `newRig` không option).

Run: `make -s go ARGS="test -race ./apps/core/internal/actor/..."`
Expected: PASS (`publish_pipeline_test.go` gọi `publish.New` 3 tham số, vẫn hợp lệ).

**Step 8: Commit**

```bash
git add apps/core/internal/eventmark apps/core/internal/publish
git commit -m "feat(publish): mark acked message events on the dedupe redis"
```

---

### Task 4: Package `reconcile`

**Files:**
- Create: `apps/core/internal/reconcile/config.go`
- Create: `apps/core/internal/reconcile/reconciler.go`
- Create: `apps/core/internal/reconcile/term.go`
- Create: `apps/core/internal/reconcile/window.go`
- Create: `apps/core/internal/reconcile/event.go`
- Create: `apps/core/internal/reconcile/harness_test.go`
- Create: `apps/core/internal/reconcile/republish_test.go`
- Create: `apps/core/internal/reconcile/term_test.go`

Hành vi cần có (để reviewer đối chiếu):
1. Chỉ chạy khi `Owner.Owns(0)`. Kiểm mỗi `ConfirmEvery` trong một nhiệm kỳ, và mỗi `Poll` khi đang không giữ slot 0.
2. Một nhiệm kỳ: `Open` → goroutine đọc `Next` vào channel → vòng chính chờ tới `CommittedAt + Delay`, gom thêm các thay đổi đã đến hạn (tối đa `Batch`), tra mark một lần cho cả batch, publish phần chưa có mark.
3. Cửa sổ theo thứ tự: mục đã có mark tính là xong ngay; publish bị từ chối hoặc ack lỗi thì gửi lại sau `Poll`, không giới hạn số lần; cửa sổ đầy (`Window`) thì chờ đầu hàng. Chỉ `Confirm` vị trí của mục cuối trong đoạn đầu hàng đã xong.
4. Thay đổi hỏng hoặc room không còn: bỏ, đếm, log tối đa 1 lần/giây; vị trí vẫn đi qua.
5. `ErrFeedHistoryLost` → log lỗi, `Forget`, mở lại từ bây giờ. `ErrFeedBusy` → chờ `Poll` rồi thử lại.
6. Tra mark lỗi → coi như chưa có mark (publish tất cả).
7. Lag (`now − CommittedAt`) vượt `DuplicateWindow` → cảnh báo, tối đa 1 lần/giây.
8. `Close`: dừng đọc, chờ các publish đang bay tối đa `Drain`, `Confirm` lần cuối, đóng cursor.

**Step 1: Viết harness và test hỏng**

`harness_test.go`:

```go
package reconcile_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/reconcile"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/apps/core/internal/testlog"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

const (
	tenant        = "acme"
	room   uint64 = 4242
	delay         = 30 * time.Second
	tick          = time.Second

	historyLostMsg = "change feed history lost; restarting from now, events in the gap are lost for good"
	dropMsg        = "dropping change that cannot become an event"
	lagMsg         = "reconciler lags behind the stream duplicate window; republished events may duplicate"
)

var setup = reconcile.Config{
	SubjectRoot: "evt", Delay: delay, DuplicateWindow: 5 * time.Minute, Window: 4, Batch: 8,
	ConfirmEvery: tick, Drain: tick, Poll: tick, RoomCache: 16,
}

type owner struct{ leading atomic.Bool }

func (o *owner) Owns(slot uint16) bool { return slot == reconcile.LeaderSlot && o.leading.Load() }

type marks struct {
	mu    sync.Mutex
	acked map[store.MsgKey]bool
	err   error
}

func (m *marks) Acked(_ context.Context, keys []store.MsgKey) ([]bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	out := make([]bool, len(keys))
	for i, k := range keys {
		out[i] = m.acked[k]
	}
	return out, nil
}

func (m *marks) fail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

func (m *marks) mark(keys ...store.MsgKey) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		m.acked[k] = true
	}
}

type busyFeed struct {
	store.ChangeFeed
	refusals atomic.Int32
}

func (b *busyFeed) Open(ctx context.Context) (store.Cursor, error) {
	if b.refusals.Add(-1) >= 0 {
		return nil, store.ErrFeedBusy
	}
	return b.ChangeFeed.Open(ctx)
}

type rig struct {
	*reconcile.Reconciler
	msgs  *memstore.Messages
	feed  *memstore.Feed
	owner *owner
	marks *marks
	js    *publishtest.JetStream
	sink  *testlog.Sink
}

func newRig(t *testing.T, wrap func(store.ChangeFeed) store.ChangeFeed) *rig {
	t.Helper()
	rg := &rig{msgs: memstore.NewMessages(), owner: &owner{}, marks: &marks{acked: map[store.MsgKey]bool{}}, js: &publishtest.JetStream{}, sink: &testlog.Sink{}}
	rg.feed = memstore.NewFeed(rg.msgs)
	rg.owner.leading.Store(true)
	rooms := memstore.NewRooms()
	created := time.Now()
	r := domain.Room{ID: room, Tenant: tenant, Type: domain.RoomGroup, Name: "team", CreatedBy: "alice", CreatedAt: created, MemberCount: 1}
	if err := rooms.Create(t.Context(), r, []domain.Member{{Room: room, Tenant: tenant, User: "alice", Role: domain.RoleOwner, JoinedAt: created}}); err != nil {
		t.Fatalf("create room: %v", err)
	}
	var feed store.ChangeFeed = rg.feed
	if wrap != nil {
		feed = wrap(feed)
	}
	rec, err := reconcile.New(reconcile.Deps{Feed: feed, Rooms: rooms, Marks: rg.marks, Owner: rg.owner, JS: rg.js}, setup, rg.sink.Logger())
	if err != nil {
		t.Fatalf("reconcile.New: %v", err)
	}
	rg.Reconciler = rec
	return rg
}

func (rg *rig) start(t *testing.T) *rig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- rg.Run(ctx) }()
	t.Cleanup(func() {
		stop, stopped := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopped()
		if err := rg.Close(stop); err != nil {
			t.Errorf("Close: %v", err)
		}
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run = %v, want nil after Close", err)
		}
	})
	return rg
}

func (rg *rig) insert(t *testing.T, r uint64, seqs ...uint64) {
	t.Helper()
	for _, s := range seqs {
		m := domain.Message{Room: r, Seq: s, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "hi", CID: "c", CreatedAt: time.Now().UTC()}
		if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
			t.Fatalf("insert %d/%d: %+v", r, s, res)
		}
	}
}

func eventID(seq uint64) string { return pbconv.MessageEventID(room, 0, seq) }

func attemptIDs(js *publishtest.JetStream) []string {
	var out []string
	for _, m := range js.Attempts() {
		out = append(out, publishtest.MsgID(m))
	}
	return out
}

func storedIDs(js *publishtest.JetStream) []string {
	var out []string
	for _, m := range js.Stored() {
		out = append(out, publishtest.MsgID(m))
	}
	return out
}

func (rg *rig) confirmed(t *testing.T) int {
	t.Helper()
	n, _ := rg.feed.Confirmed()
	return n
}
```

`republish_test.go`:

```go
package reconcile_test

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestRepublishesAnUnmarkedChangeOnlyAfterTheDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.insert(t, room, 1)
		time.Sleep(delay - time.Millisecond)
		synctest.Wait()
		if got := attemptIDs(rg.js); len(got) != 0 {
			t.Fatalf("attempts before the delay = %v, want none", got)
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		stored := rg.js.Stored()
		if len(stored) != 1 || stored[0].Subject != "evt.acme.room.4242.msg_created" || storedIDs(rg.js)[0] != eventID(1) {
			t.Fatalf("stored = %v, want one %s on evt.acme.room.4242.msg_created", storedIDs(rg.js), eventID(1))
		}
	})
}

func TestSkipsMarkedChangesAndConfirmsPastThem(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.marks.mark(store.MsgKey{Room: room, Seq: 1}, store.MsgKey{Room: room, Seq: 3})
		rg.insert(t, room, 1, 2, 3)
		time.Sleep(delay + tick)
		synctest.Wait()
		if got := attemptIDs(rg.js); !slices.Equal(got, []string{eventID(2)}) {
			t.Fatalf("attempts = %v, want only %s", got, eventID(2))
		}
		if got := rg.confirmed(t); got != 3 {
			t.Fatalf("confirmed = %d, want 3", got)
		}
	})
}

func TestConfirmWaitsForTheAck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.js.Hold()
		rg.insert(t, room, 1)
		time.Sleep(delay + 2*tick)
		synctest.Wait()
		if got := rg.confirmed(t); got != 0 {
			t.Fatalf("confirmed before the ack = %d, want 0", got)
		}
		rg.js.Release()
		time.Sleep(tick)
		synctest.Wait()
		if got := rg.confirmed(t); got != 1 {
			t.Fatalf("confirmed after the ack = %d, want 1", got)
		}
	})
}

func TestRefusedPublishIsResent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		refused := 0
		rg.js.RefuseWhen(func(*nats.Msg) error {
			if refused == 0 {
				refused++
				return errors.New("too many stalled")
			}
			return nil
		})
		rg.insert(t, room, 1)
		time.Sleep(delay + 2*tick)
		synctest.Wait()
		if got := attemptIDs(rg.js); len(got) < 2 {
			t.Fatalf("attempts = %v, want the refused publish resent", got)
		}
		if got := storedIDs(rg.js); !slices.Equal(got, []string{eventID(1)}) {
			t.Fatalf("stored = %v, want %s once", got, eventID(1))
		}
		if got := rg.confirmed(t); got != 1 {
			t.Fatalf("confirmed = %d, want 1", got)
		}
	})
}

func TestMarkLookupFailurePublishesEverything(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.marks.mark(store.MsgKey{Room: room, Seq: 1})
		rg.marks.fail(errors.New("redis down"))
		rg.insert(t, room, 1)
		time.Sleep(delay + tick)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, []string{eventID(1)}) {
			t.Fatalf("stored = %v, want %s", got, eventID(1))
		}
	})
}

func TestUnknownRoomIsDroppedAndPassed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.insert(t, 999, 1)
		rg.insert(t, room, 1)
		time.Sleep(delay + tick)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, []string{eventID(1)}) {
			t.Fatalf("stored = %v, want only %s", got, eventID(1))
		}
		if rg.Dropped() != 1 || rg.sink.Count(dropMsg) != 1 {
			t.Fatalf("dropped = %d, logged %d; want 1 and 1", rg.Dropped(), rg.sink.Count(dropMsg))
		}
		if got := rg.confirmed(t); got != 2 {
			t.Fatalf("confirmed = %d, want 2", got)
		}
	})
}

func TestFullWindowWaitsForTheHead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.js.Hold()
		rg.insert(t, room, 1, 2, 3, 4, 5, 6)
		time.Sleep(delay + tick)
		synctest.Wait()
		if got := len(rg.js.Attempts()); got != setup.Window {
			t.Fatalf("attempts with a full window = %d, want %d", got, setup.Window)
		}
		rg.js.Release()
		time.Sleep(tick)
		synctest.Wait()
		if got := len(rg.js.Stored()); got != 6 {
			t.Fatalf("stored after release = %d, want 6", got)
		}
	})
}
```

`term_test.go`:

```go
package reconcile_test

import (
	"context"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestIdleUntilItOwnsTheLeaderSlot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil)
		rg.owner.leading.Store(false)
		rg.start(t)
		synctest.Wait()
		rg.insert(t, room, 1)
		time.Sleep(delay + 2*tick)
		synctest.Wait()
		if got := attemptIDs(rg.js); len(got) != 0 {
			t.Fatalf("attempts while not leading = %v, want none", got)
		}
	})
}

func TestLeadershipHandoverResumesFromTheConfirmedPosition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.insert(t, room, 1)
		time.Sleep(delay + tick)
		synctest.Wait()
		rg.owner.leading.Store(false)
		time.Sleep(2 * tick)
		synctest.Wait()
		rg.insert(t, room, 2)
		rg.owner.leading.Store(true)
		time.Sleep(delay + 2*tick)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, []string{eventID(1), eventID(2)}) {
			t.Fatalf("stored = %v, want %s then %s", got, eventID(1), eventID(2))
		}
	})
}

func TestLostHistoryRestartsFromNow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil)
		rg.feed.LoseHistory()
		rg.start(t)
		time.Sleep(2 * tick)
		synctest.Wait()
		if got := rg.sink.Count(historyLostMsg); got != 1 {
			t.Fatalf("%q logged %d times, want 1", historyLostMsg, got)
		}
		rg.insert(t, room, 1)
		time.Sleep(delay + tick)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, []string{eventID(1)}) {
			t.Fatalf("stored = %v, want %s", got, eventID(1))
		}
	})
}

func TestBusyFeedIsRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		busy := &busyFeed{}
		busy.refusals.Store(2)
		rg := newRig(t, func(f store.ChangeFeed) store.ChangeFeed {
			busy.ChangeFeed = f
			return busy
		}).start(t)
		time.Sleep(3 * tick)
		synctest.Wait()
		rg.insert(t, room, 1)
		time.Sleep(delay + tick)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, []string{eventID(1)}) {
			t.Fatalf("stored = %v, want %s after the feed frees up", got, eventID(1))
		}
	})
}

func TestCloseSettlesInFlightPublishes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.js.Hold()
		rg.insert(t, room, 1)
		time.Sleep(delay)
		synctest.Wait()
		closed := make(chan error, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			closed <- rg.Close(ctx)
		}()
		time.Sleep(tick / 2)
		rg.js.Release()
		if err := <-closed; err != nil {
			t.Fatalf("Close = %v", err)
		}
		if got := rg.confirmed(t); got != 1 {
			t.Fatalf("confirmed after Close = %d, want 1", got)
		}
	})
}

func TestLagBeyondTheDuplicateWindowWarns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.insert(t, room, 1)
		time.Sleep(delay + tick)
		synctest.Wait()
		rg.owner.leading.Store(false)
		time.Sleep(2 * tick)
		synctest.Wait()
		rg.insert(t, room, 2)
		time.Sleep(setup.DuplicateWindow + tick)
		rg.owner.leading.Store(true)
		time.Sleep(2 * tick)
		synctest.Wait()
		if got := rg.sink.Count(lagMsg); got != 1 {
			t.Fatalf("%q logged %d times, want 1", lagMsg, got)
		}
		if got := storedIDs(rg.js); !slices.Equal(got, []string{eventID(1), eventID(2)}) {
			t.Fatalf("stored = %v, want %s then the late %s", got, eventID(1), eventID(2))
		}
	})
}
```

Run: `make -s go ARGS="test ./apps/core/internal/reconcile/"`
Expected: FAIL `no non-test Go files`.

**Step 2: `config.go`**

```go
package reconcile

import (
	"cmp"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	LeaderSlot uint16 = 0

	DefaultDelay        = 30 * time.Second
	DefaultWindow       = 1024
	DefaultBatch        = 256
	DefaultConfirmEvery = time.Second
	DefaultDrain        = time.Second
	DefaultPoll         = time.Second
	DefaultRoomCache    = 65536
)

type Config struct {
	SubjectRoot     string
	Delay           time.Duration
	DuplicateWindow time.Duration
	Window          int
	Batch           int
	ConfirmEvery    time.Duration
	Drain           time.Duration
	Poll            time.Duration
	RoomCache       int
}

func (c Config) Validate() error { return c.withDefaults().validate() }

func (c Config) withDefaults() Config {
	c.Delay = cmp.Or(c.Delay, DefaultDelay)
	c.Window = cmp.Or(c.Window, DefaultWindow)
	c.Batch = cmp.Or(c.Batch, DefaultBatch)
	c.ConfirmEvery = cmp.Or(c.ConfirmEvery, DefaultConfirmEvery)
	c.Drain = cmp.Or(c.Drain, DefaultDrain)
	c.Poll = cmp.Or(c.Poll, DefaultPoll)
	c.RoomCache = cmp.Or(c.RoomCache, DefaultRoomCache)
	return c
}

func (c Config) validate() error {
	switch {
	case c.SubjectRoot == "":
		return fmt.Errorf("%w: reconcile needs the event subject root", apperr.ErrInvalidArgument)
	case c.Delay <= 0 || c.ConfirmEvery <= 0 || c.Drain <= 0 || c.Poll <= 0 || c.Window <= 0 || c.Batch <= 0 || c.RoomCache <= 0:
		return fmt.Errorf("%w: reconcile config %+v must be positive", apperr.ErrInvalidArgument, c)
	case c.DuplicateWindow <= c.Delay:
		return fmt.Errorf("%w: reconcile delay %v must be shorter than the stream duplicate window %v", apperr.ErrInvalidArgument, c.Delay, c.DuplicateWindow)
	default:
		return nil
	}
}

func (c Config) JetStreamOptions(ackTimeout time.Duration) []jetstream.JetStreamOpt {
	c = c.withDefaults()
	return []jetstream.JetStreamOpt{
		jetstream.WithPublishAsyncMaxPending(c.Window),
		jetstream.WithPublishAsyncTimeout(ackTimeout),
	}
}
```

**Step 3: `reconciler.go`**

```go
package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	historyLostMsg = "change feed history lost; restarting from now, events in the gap are lost for good"
	lagMsg         = "reconciler lags behind the stream duplicate window; republished events may duplicate"
)

var (
	errStarted  = errors.New("reconciler already started")
	errStopped  = errors.New("reconciler stopping")
	errLostLead = errors.New("reconciler no longer owns the leader slot")
)

type Owner interface {
	Owns(slot uint16) bool
}

type Marks interface {
	Acked(ctx context.Context, keys []store.MsgKey) ([]bool, error)
}

type RoomReader interface {
	Get(ctx context.Context, id uint64) (domain.Room, error)
}

type Deps struct {
	Feed  store.ChangeFeed
	Rooms RoomReader
	Marks Marks
	Owner Owner
	JS    publish.JetStream
}

type Reconciler struct {
	deps    Deps
	cfg     Config
	log     *slog.Logger
	types   *roomTypes
	drops   limitedLog
	lags    limitedLog
	dropped atomic.Uint64
	started atomic.Bool
	stop    chan struct{}
	halt    sync.Once
	done    chan struct{}
}

func New(deps Deps, cfg Config, log *slog.Logger) (*Reconciler, error) {
	if deps.Feed == nil || deps.Rooms == nil || deps.Marks == nil || deps.Owner == nil || deps.JS == nil {
		return nil, fmt.Errorf("%w: reconciler needs a feed, rooms, marks, an owner and a jetstream client", apperr.ErrInvalidArgument)
	}
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	return &Reconciler{
		deps: deps, cfg: cfg, log: log,
		types: newRoomTypes(deps.Rooms, cfg.RoomCache),
		drops: limitedLog{log: log}, lags: limitedLog{log: log},
		stop: make(chan struct{}), done: make(chan struct{}),
	}, nil
}

func (r *Reconciler) Dropped() uint64 { return r.dropped.Load() }

func (r *Reconciler) Run(ctx context.Context) error {
	if !r.started.CompareAndSwap(false, true) {
		return errStarted
	}
	defer close(r.done)
	for {
		select {
		case <-r.stop:
			return nil
		default:
		}
		if r.leading() {
			r.term(ctx)
		}
		switch err := sleep(ctx, r.stop, r.cfg.Poll); {
		case errors.Is(err, errStopped):
			return nil
		case err != nil:
			return err
		}
	}
}

func (r *Reconciler) Close(ctx context.Context) error {
	r.halt.Do(func() { close(r.stop) })
	if !r.started.Load() {
		return nil
	}
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Reconciler) leading() bool { return r.deps.Owner.Owns(LeaderSlot) }

func (r *Reconciler) term(ctx context.Context) {
	cur, err := r.deps.Feed.Open(ctx)
	if err != nil {
		r.ended(ctx, err)
		return
	}
	t := newTerm(r, cur)
	err = t.run(ctx)
	settle, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.cfg.Drain)
	defer cancel()
	t.settle(settle)
	if cerr := cur.Close(settle); cerr != nil {
		r.log.WarnContext(ctx, "close change feed", "err", cerr)
	}
	r.ended(ctx, err)
}

func (r *Reconciler) ended(ctx context.Context, err error) {
	switch {
	case err == nil || ctx.Err() != nil || errors.Is(err, errStopped) || errors.Is(err, errLostLead):
	case errors.Is(err, store.ErrFeedHistoryLost):
		r.log.ErrorContext(ctx, historyLostMsg, "err", err)
		if ferr := r.deps.Feed.Forget(ctx); ferr != nil {
			r.log.WarnContext(ctx, "forget change feed position", "err", ferr)
		}
	case errors.Is(err, store.ErrFeedBusy):
		r.log.InfoContext(ctx, "change feed held by another consumer; retrying")
	default:
		r.log.WarnContext(ctx, "reconcile term ended", "err", err)
	}
}

func (r *Reconciler) drop(ctx context.Context, msg string, err error) {
	r.dropped.Add(1)
	r.drops.warn(ctx, msg, "err", err)
}

func (r *Reconciler) watchLag(ctx context.Context, committed time.Time) {
	if lag := time.Since(committed); lag > r.cfg.DuplicateWindow {
		r.lags.warn(ctx, lagMsg, "lag", lag, "window", r.cfg.DuplicateWindow)
	}
}

func sleep(ctx context.Context, stop <-chan struct{}, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-stop:
		return errStopped
	case <-ctx.Done():
		return ctx.Err()
	}
}

type limitedLog struct {
	log  *slog.Logger
	mu   sync.Mutex
	last time.Time
}

func (l *limitedLog) warn(ctx context.Context, msg string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now := time.Now(); l.last.IsZero() || now.Sub(l.last) >= time.Second {
		l.last = now
		l.log.WarnContext(ctx, msg, args...)
	}
}
```

Nếu file vượt 200 dòng thì tách `limitedLog` và `sleep` ra `limited_log.go`.

**Step 4: `term.go`**

```go
package reconcile

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var errReaderStopped = errors.New("change feed reader stopped")

type term struct {
	r         *Reconciler
	cur       store.Cursor
	win       *window
	changes   chan store.Change
	failed    chan error
	next      *store.Change
	confirmed store.Position
}

func newTerm(r *Reconciler, cur store.Cursor) *term {
	return &term{
		r: r, cur: cur,
		win:     newWindow(r.deps.JS, r.cfg.Window, r.cfg.Poll),
		changes: make(chan store.Change, r.cfg.Batch),
		failed:  make(chan error, 1),
	}
}

func (t *term) run(ctx context.Context) error {
	readCtx, stopReading := context.WithCancel(ctx)
	reading := make(chan struct{})
	go func() {
		defer close(reading)
		t.read(readCtx)
	}()
	defer func() {
		stopReading()
		<-reading
	}()
	tick := time.NewTicker(t.r.cfg.ConfirmEvery)
	defer tick.Stop()
	for {
		if c := t.next; c != nil {
			t.next = nil
			if err := t.handle(ctx, *c); err != nil {
				return err
			}
			continue
		}
		select {
		case c, open := <-t.changes:
			if !open {
				return t.readFailure()
			}
			if err := t.handle(ctx, c); err != nil {
				return err
			}
		case <-tick.C:
			if err := t.checkpoint(ctx); err != nil {
				return err
			}
		case <-t.r.stop:
			return errStopped
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (t *term) read(ctx context.Context) {
	defer close(t.changes)
	for {
		c, err := t.cur.Next(ctx)
		switch {
		case errors.Is(err, store.ErrCorruptChange):
			t.r.drop(ctx, "dropping change that cannot become an event", err)
			continue
		case err != nil:
			t.failed <- err
			return
		}
		select {
		case t.changes <- c:
		case <-ctx.Done():
			return
		}
	}
}

func (t *term) readFailure() error {
	select {
	case err := <-t.failed:
		return err
	default:
		return errReaderStopped
	}
}

func (t *term) handle(ctx context.Context, first store.Change) error {
	if err := sleep(ctx, t.r.stop, time.Until(first.CommittedAt.Add(t.r.cfg.Delay))); err != nil {
		return err
	}
	t.r.watchLag(ctx, first.CommittedAt)
	batch := t.gather(first)
	acked := t.r.acked(ctx, batch)
	for i, c := range batch {
		if acked[i] {
			t.win.done(c.Position)
			continue
		}
		if err := t.publish(ctx, c); err != nil {
			return err
		}
	}
	t.win.collect()
	return nil
}

func (t *term) gather(first store.Change) []store.Change {
	batch := []store.Change{first}
	due := time.Now().Add(-t.r.cfg.Delay)
	for len(batch) < t.r.cfg.Batch {
		select {
		case c, open := <-t.changes:
			if !open {
				return batch
			}
			if c.CommittedAt.After(due) {
				t.next = &c
				return batch
			}
			batch = append(batch, c)
		default:
			return batch
		}
	}
	return batch
}

func (t *term) publish(ctx context.Context, c store.Change) error {
	msg, err := t.r.message(ctx, c)
	switch {
	case errors.Is(err, errUndeliverable):
		t.r.drop(ctx, "dropping change that cannot become an event", err)
		t.win.done(c.Position)
		return nil
	case err != nil:
		return err
	}
	if err := t.win.wait(ctx, t.r.stop); err != nil {
		return err
	}
	t.win.send(c.Position, msg)
	return nil
}

func (t *term) checkpoint(ctx context.Context) error {
	t.win.collect()
	t.confirm(ctx)
	if !t.r.leading() {
		return errLostLead
	}
	return nil
}

func (t *term) settle(ctx context.Context) {
	t.win.drain(ctx)
	t.confirm(ctx)
}

func (t *term) confirm(ctx context.Context) {
	pos := t.win.settled
	if pos == nil || bytes.Equal(pos, t.confirmed) {
		return
	}
	if err := t.cur.Confirm(ctx, pos); err != nil {
		t.r.log.WarnContext(ctx, "confirm change feed position", "err", err)
		return
	}
	t.confirmed = pos
}
```

**Step 5: `window.go`**

```go
package reconcile

import (
	"context"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type entry struct {
	pos     store.Position
	msg     *nats.Msg
	future  jetstream.PubAckFuture
	acked   bool
	retryAt time.Time
}

type window struct {
	js      publish.JetStream
	limit   int
	backoff time.Duration
	entries []*entry
	settled store.Position
}

func newWindow(js publish.JetStream, limit int, backoff time.Duration) *window {
	return &window{js: js, limit: limit, backoff: backoff}
}

func (w *window) done(pos store.Position) {
	w.entries = append(w.entries, &entry{pos: pos, acked: true})
}

func (w *window) send(pos store.Position, msg *nats.Msg) {
	e := &entry{pos: pos, msg: msg}
	w.publish(e)
	w.entries = append(w.entries, e)
}

func (w *window) publish(e *entry) {
	f, err := w.js.PublishMsgAsync(e.msg)
	if err != nil {
		e.retryAt = time.Now().Add(w.backoff)
		return
	}
	e.future = f
}

func (w *window) poll(e *entry) bool {
	switch {
	case e.acked:
		return true
	case e.future == nil:
		if !time.Now().Before(e.retryAt) {
			w.publish(e)
		}
		return false
	}
	select {
	case <-e.future.Ok():
		e.acked = true
	case <-e.future.Err():
		e.future, e.retryAt = nil, time.Now().Add(w.backoff)
	default:
	}
	return e.acked
}

func (w *window) collect() {
	for len(w.entries) > 0 && w.poll(w.entries[0]) {
		w.settled = w.entries[0].pos
		w.entries[0] = nil
		w.entries = w.entries[1:]
	}
}

func (w *window) wait(ctx context.Context, stop <-chan struct{}) error {
	for {
		w.collect()
		if len(w.entries) < w.limit {
			return nil
		}
		if err := w.awaitHead(ctx, stop); err != nil {
			return err
		}
	}
}

func (w *window) drain(ctx context.Context) {
	for {
		w.collect()
		if len(w.entries) == 0 || w.awaitHead(ctx, nil) != nil {
			return
		}
	}
}

func (w *window) awaitHead(ctx context.Context, stop <-chan struct{}) error {
	head := w.entries[0]
	if head.future == nil {
		return sleep(ctx, stop, time.Until(head.retryAt))
	}
	select {
	case <-head.future.Ok():
		head.acked = true
	case <-head.future.Err():
		head.future, head.retryAt = nil, time.Now().Add(w.backoff)
	case <-stop:
		return errStopped
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}
```

**Step 6: `event.go`**

```go
package reconcile

import (
	"context"
	"errors"
	"fmt"

	"github.com/nats-io/nats.go"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var errUndeliverable = errors.New("change cannot become an event")

func (r *Reconciler) message(ctx context.Context, c store.Change) (*nats.Msg, error) {
	typ, err := r.types.get(ctx, c.Msg.Room)
	switch {
	case errors.Is(err, domain.ErrRoomNotFound):
		return nil, fmt.Errorf("%w: room %d: %w", errUndeliverable, c.Msg.Room, err)
	case err != nil:
		return nil, err
	}
	msg, err := publish.Message(r.cfg.SubjectRoot, c.Msg.Room, pbconv.MessageCreated(typ, c.Msg))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errUndeliverable, err)
	}
	return msg, nil
}

func (r *Reconciler) acked(ctx context.Context, batch []store.Change) []bool {
	keys := make([]store.MsgKey, len(batch))
	for i, c := range batch {
		keys[i] = store.KeyOf(c.Msg)
	}
	got, err := r.deps.Marks.Acked(ctx, keys)
	if err != nil || len(got) != len(batch) {
		return make([]bool, len(batch))
	}
	return got
}

type roomTypes struct {
	rooms RoomReader
	limit int
	cache map[uint64]domain.RoomType
}

func newRoomTypes(rooms RoomReader, limit int) *roomTypes {
	return &roomTypes{rooms: rooms, limit: limit, cache: make(map[uint64]domain.RoomType)}
}

func (c *roomTypes) get(ctx context.Context, id uint64) (domain.RoomType, error) {
	if t, ok := c.cache[id]; ok {
		return t, nil
	}
	room, err := c.rooms.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if len(c.cache) >= c.limit {
		clear(c.cache)
	}
	c.cache[id] = room.Type
	return room.Type, nil
}
```

`roomTypes` chỉ được dùng trong goroutine vòng chính, nên không cần khoá. Loại room không đổi sau khi tạo, nên xoá cả map khi đầy là đủ.

**Step 7: Chạy test**

Run: `make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/reconcile/"`
Expected: PASS mọi test ở Step 1, goleak sạch. Test nào hỏng vì thời điểm (synctest) thì **dừng và báo cáo** kèm output, không nới assert.

**Step 8: Commit**

```bash
git add apps/core/internal/reconcile
git commit -m "feat(reconcile): republish unacked message events from the change feed"
```

---

### Task 5: Config, wiring, lifecycle, `EVT_STREAM_DUPLICATES=5m`

**Files:**
- Modify: `apps/core/internal/publish/stream.go:16` (`DefaultStreamDuplicates = 5 * time.Minute`)
- Modify: `apps/core/internal/config/config.go` (field, `StopPlan`)
- Modify: `apps/core/internal/config/components.go`
- Modify: `apps/core/internal/config/parse.go` (thêm `flag`)
- Modify: `apps/core/internal/config/validate.go`
- Modify: `apps/core/internal/config/env_test.go`, `load_test.go`, `validate_test.go`
- Modify: `apps/core/clients.go` (JetStream riêng cho reconciler)
- Modify: `apps/core/wiring.go`, `apps/core/lifecycle.go`, `apps/core/shutdown.go`

**Step 1: Test config hỏng**

- `env_test.go`: thêm vào `envKeys`: `"RECONCILE_ENABLED", "RECONCILE_DELAY", "RECONCILE_WINDOW", "RECONCILE_BATCH", "RECONCILE_CONFIRM_EVERY", "RECONCILE_DRAIN", "RECONCILE_ROOM_CACHE", "EVT_ACK_MARK_TTL"`. Thêm vào `overrides`: `"RECONCILE_ENABLED": "false", "RECONCILE_DELAY": "20s", "RECONCILE_WINDOW": "64", "RECONCILE_BATCH": "32", "RECONCILE_CONFIRM_EVERY": "2s", "RECONCILE_DRAIN": "500ms", "RECONCILE_ROOM_CACHE": "128", "EVT_ACK_MARK_TTL": "30m"`.
- `load_test.go`:
  - Struct mong đợi của bộ mặc định: `Duplicates: 5 * time.Minute`; thêm `ReconcileEnabled: true`, `Reconcile: reconcile.Config{SubjectRoot: "evt", Delay: 30 * time.Second, DuplicateWindow: 5 * time.Minute, Window: 1024, Batch: 256, ConfirmEvery: time.Second, Drain: time.Second, Poll: time.Second, RoomCache: 65536}`, `AckMarks: eventmark.Config{TTL: time.Hour, Timeout: 100 * time.Millisecond, Cooldown: time.Second}`.
  - Struct mong đợi của bộ override: `ReconcileEnabled: false`, `Reconcile: reconcile.Config{SubjectRoot: "evt_it", Delay: 20 * time.Second, DuplicateWindow: 5 * time.Minute, Window: 64, Batch: 32, ConfirmEvery: 2 * time.Second, Drain: 500 * time.Millisecond, Poll: 500 * time.Millisecond, RoomCache: 128}`, `AckMarks: eventmark.Config{TTL: 30 * time.Minute, Timeout: 50 * time.Millisecond, Cooldown: 2 * time.Second}`.
- `validate_test.go`, thêm vào bảng case lỗi:
  `{"reconcile delay not under the duplicate window", map[string]string{"RECONCILE_DELAY": "5m"}, "RECONCILE_DELAY must be shorter than EVT_STREAM_DUPLICATES"},`
  và case hợp lệ: `{"reconcile disabled ignores its delay", map[string]string{"RECONCILE_ENABLED": "false", "RECONCILE_DELAY": "5m"}, ""},`.

Run: `make -s go ARGS="test ./apps/core/internal/config/"`
Expected: FAIL (field chưa có).

**Step 2: Parse và config**

`parse.go`, thêm:

```go
func (p *parser) flag(key string, def bool) bool {
	v, err := envconfig.Bool(key, def)
	if err != nil {
		p.fail(err)
	}
	return v
}
```

`config.go`:
- `Config` thêm `ReconcileEnabled bool`, `Reconcile reconcile.Config`, `AckMarks eventmark.Config` (import hai package).
- `StopPlan` thêm field `Reconciler time.Duration`. `StopPlan()` gán `Reconciler: c.Reconcile.Drain + CloseTimeout`. `total()` thêm `s.Reconciler` vào danh sách cộng.

`components.go`, cuối `components`:

```go
	c.ReconcileEnabled = p.flag("RECONCILE_ENABLED", true)
	c.Reconcile = reconcile.Config{
		SubjectRoot:     subjectRoot,
		Delay:           p.span("RECONCILE_DELAY", reconcile.DefaultDelay),
		DuplicateWindow: c.Stream.Duplicates,
		Window:          p.count("RECONCILE_WINDOW", reconcile.DefaultWindow),
		Batch:           p.count("RECONCILE_BATCH", reconcile.DefaultBatch),
		ConfirmEvery:    p.span("RECONCILE_CONFIRM_EVERY", reconcile.DefaultConfirmEvery),
		Drain:           p.span("RECONCILE_DRAIN", reconcile.DefaultDrain),
		Poll:            tick,
		RoomCache:       p.count("RECONCILE_ROOM_CACHE", reconcile.DefaultRoomCache),
	}
	c.AckMarks = eventmark.Config{
		TTL:      p.span("EVT_ACK_MARK_TTL", eventmark.DefaultTTL),
		Timeout:  redisTimeout,
		Cooldown: redisCooldown,
	}
```

`validate.go`:
- `stopPhases` thêm `" + RECONCILE_DRAIN + 1s"` trước `" + CORE_PUBLISHER_DRAIN"`, viết lại cho đúng thứ tự dừng mới: `"CORE_DRAIN_DELAY + CORE_GRPC_SHUTDOWN + RECONCILE_DRAIN + 1s + CORE_REQUEST_DEADLINE (router drain) + FLUSH_INSERT_TIMEOUT (flusher drain) + CORE_PUBLISHER_DRAIN + slot release + client close"`.
- `rules` thêm `{!c.ReconcileEnabled || c.Reconcile.Delay < c.Stream.Duplicates, "RECONCILE_DELAY must be shorter than EVT_STREAM_DUPLICATES"},`.
- `componentErrors` thêm `{"EVT_ACK_MARK_TTL, REDIS_OP_TIMEOUT, REDIS_COOLDOWN", c.AckMarks.Validate()}` và, chỉ khi bật, reconcile:

```go
	if c.ReconcileEnabled {
		parts = append(parts, struct {
			keys string
			err  error
		}{"RECONCILE_*", c.Reconcile.Validate()})
	}
```

`publish/stream.go`: `DefaultStreamDuplicates = 5 * time.Minute`.

Ngân sách dừng mặc định: 2 + 5 + 2 (reconciler) + 3 + 1 + 5 + 5 + 1 = 24s < 25s.

Run: `make -s go ARGS="test -race ./apps/core/internal/config/ ./apps/core/internal/publish/"`
Expected: PASS.

**Step 3: Clients, wiring, lifecycle**

`clients.go`:
- Struct `clients` thêm `reconcileJS jetstream.JetStream`.
- Trong `connectNATS`, sau khi gán `c.js`:

```go
	rjs, err := jetstream.New(nc, cfg.Reconcile.JetStreamOptions(cfg.Publish.AckTimeout)...)
	if err != nil {
		return fmt.Errorf("reconcile jetstream: %w", config.RedactError(err, cfg.NATSURL))
	}
	c.reconcileJS = rjs
```

`wiring.go`:
- Struct `app` thêm `reconciler drainer`.
- Trong `wire`, thay khối tạo publisher bằng:

```go
	marks, err := eventmark.New(cl.dedupe, cfg.AckMarks, log)
	if err != nil {
		return nil, fmt.Errorf("wire event ack marks: %w", err)
	}
	pub, err := publish.New(cl.js, cfg.Publish, log, publish.WithAckMarks(marks))
	if err != nil {
		return nil, fmt.Errorf("wire publisher: %w", err)
	}
```

- Sau khi tạo `slots`, thêm:

```go
	if cfg.ReconcileEnabled {
		rec, err := reconcile.New(reconcile.Deps{
			Feed: mongostore.NewFeed(cl.mongo.Database(cfg.MongoDB)), Rooms: st, Marks: marks, Owner: slots, JS: cl.reconcileJS,
		}, cfg.Reconcile, log)
		if err != nil {
			return nil, fmt.Errorf("wire reconciler: %w", err)
		}
		a.reconciler = rec
	}
```

Nếu `wire` vượt 60 dòng hoặc file vượt 200 dòng, tách phần reconciler ra hàm `wireReconciler(cfg, cl, st, marks, slots, log) (drainer, error)` trong `wiring_reconcile.go`.

`lifecycle.go`:
- `tasks` thêm `reconciler *task`.
- `newSupervisor(ctx, 6)` → `newSupervisor(ctx, 7)`.
- Sau dòng `slots: sup.start(...)` (sau khi tạo `t`), thêm:

```go
	if a.reconciler != nil {
		t.reconciler = sup.start("reconciler", a.reconciler.Run)
	}
```

`shutdown.go`, ngay sau `s.step("grpc", 0, nil, t.grpc)`:

```go
	var closeReconciler func(context.Context) error
	if a.reconciler != nil {
		closeReconciler = a.reconciler.Close
	}
	s.step("reconciler", plan.Reconciler, closeReconciler, t.reconciler)
```

**Step 4: Chạy test**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/..."`
Expected: PASS.

Run (cần `make infra-up`): `make -s itest 2>&1 | grep -E "FAIL|ok .*apps/core"`
Expected: không có FAIL. `TestRealInfraStopUnderLoadKeepsEveryAckedMessage` vẫn qua với reconciler bật.

**Step 5: Commit**

```bash
git add apps/core
git commit -m "feat(core): run the event reconciler on the slot 0 owner"
```

---

### Task 6: Itest — ghi thẳng Mongo, event vẫn tới `live.*`

**Files:**
- Create: `apps/core/reconcile_integration_test.go`
- Modify: `apps/core/it_infra_test.go` (giữ `*nats.Conn` trong `itInfra`)

**Step 1: Giữ kết nối NATS trong harness**

Trong `it_infra_test.go`: struct `itInfra` thêm field `nc *nats.Conn`; trong `realInfra`, sau `t.Cleanup(nc.Close)` thêm `it.nc = nc`.

**Step 2: Viết test**

`apps/core/reconcile_integration_test.go`:

```go
package main

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/pkg/ids"
)

func TestRealInfraReconcilerPublishesWritesThatSkippedTheCore(t *testing.T) {
	it := realInfra(t)
	cfg := it.coreConfig(t, map[string]string{"CORE_DRAIN_DELAY": "200ms", "RECONCILE_DELAY": "1s"})
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	ctx, cancel := context.WithCancel(context.Background())
	core := &running{done: make(chan struct{})}
	go func() {
		defer close(core.done)
		core.err = run(ctx, cfg, logger)
	}()
	t.Cleanup(func() {
		cancel()
		<-core.done
	})
	awaitReady(t, cfg, core)

	roomID := createRoom(t, dialCore(t, cfg))
	room, err := ids.ParseRoomID(roomID)
	if err != nil {
		t.Fatalf("ParseRoomID(%s): %v", roomID, err)
	}
	live := make(chan *nats.Msg, 16)
	sub, err := it.nc.ChanSubscribe(cfg.Stream.LiveRoot+"."+itTenant+".room."+roomID+".>", live)
	if err != nil {
		t.Fatalf("subscribe live: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })

	outside := domain.Message{Room: room, Seq: 1, Tenant: itTenant, From: "migrator", Kind: domain.KindText, Text: "written outside the core", CID: "outside-1", CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	st := mongostore.New(it.mongo.Database(cfg.MongoDB), mongostore.Options{})
	if res := st.Insert(t.Context(), []domain.Message{outside}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert outside the core: %+v", res)
	}

	want := pbconv.MessageEventID(room, 0, 1)
	deadline := time.After(30 * time.Second)
	for {
		select {
		case m := <-live:
			if m.Header.Get(jetstream.MsgIDHeader) == want {
				return
			}
		case <-deadline:
			t.Fatalf("no live event %s within 30s of a write that skipped the core", want)
		}
	}
}
```

Kiểm trước khi chạy:
- RePublish có giữ header `Nats-Msg-Id` sang `live.*` không. Nếu không giữ, so `pbconv.MessageEventID` với `Event.Id` sau khi `proto.Unmarshal(m.Data, &ev)`.
- Core itest chiếm đủ 1024 slot sau vài tick. Nếu `Owns(0)` đến muộn hơn 30s thì **dừng và báo cáo**.

**Step 3: Chạy**

Run: `make -s itest 2>&1 | grep -E "Reconciler|FAIL|ok .*apps/core\b"`
Expected: `ok github.com/ivannguyendev/chatim/apps/core`.

**Step 4: Commit**

```bash
git add apps/core/reconcile_integration_test.go apps/core/it_infra_test.go
git commit -m "test(core): reconcile a message written outside the core"
```

---

### Task 7: Docs

**Files:**
- Modify: `docs/designs/260930-chat-core-gateway-design.md` (header cập nhật, §5.4 mục 3, bảng sự cố dòng "Core chết" và "Core bị kill giữa chừng", Decision Log D52)
- Modify: `docs/roadmap.md` (M2a.2 → ✅ Xong trên dev; mức sẵn sàng)
- Modify: `README.md` (bảng env: `RECONCILE_*`, `EVT_ACK_MARK_TTL`; `EVT_STREAM_DUPLICATES` mặc định `5m`; trạng thái)
- Modify: `INDEXES.csv` (thêm `apps/core/internal/reconcile`, `apps/core/internal/eventmark`; sửa `store`, `memstore`, `mongostore`, `storetest`, `publish`, `apps/core`; plan M2a.2)
- Modify: `CLAUDE.md` (Done/Next; "Send path" bước 6 thêm mark; mục Redis dedupe thêm `chatim:evtack:*`; thêm đoạn "Event reconciliation"; Docs thêm plan M2a.2)

Nội dung D52 viết lại (một dòng bảng, cùng phong cách D50), gồm:
- R1–R10 tóm tắt;
- bitmap mark và lý do bộ nhớ (36M key string so với vài chục MB bitmap);
- `EVT_STREAM_DUPLICATES = 5m`;
- các phương án bị bác bỏ (mark Redis làm nguồn, republish mọi tin, CDC relay là đường publish duy nhất, `docker pause` trong e2e).

Ghi chú vận hành (§ bảng sự cố hoặc §5.4):
- prod cần oplog `minRetentionHours` ≥ 24h;
- cần alert theo log `change feed history lost` và log lag;
- bộ nhớ NATS cho map dedupe 5 phút.

Run: `make fmt-check`
Expected: sạch.

```bash
git add docs README.md INDEXES.csv CLAUDE.md
git commit -m "docs: describe event reconciliation from the change feed"
```

---

### Task 8: Kiểm chứng cuối phase (DoD) + corebench

**Step 1: Unit + lint**

Run: `make fmt-check && make vet && make lint && make test`
Expected: tất cả PASS, lint `0 issues`.

**Step 2: Integration**

```bash
make core-down
make infra-reset
make infra-up
make itest
```

Expected: PASS, gồm `TestMongoFeedContract` và `TestRealInfraReconcilerPublishesWritesThatSkippedTheCore`.

**Step 3: E2E với reconciler bật**

Run: `make core-up && make e2e`
Expected: `e2e PASS: ... every acked seq live`. Lần kill core-1 có thể làm slot 0 đổi chủ; phép kiểm không đổi. Fail thì **dừng và báo cáo** kèm `docker logs chatim-core-1 chatim-core-2`.

**Step 4: Reconciler chạy đúng một chỗ**

Run: `docker logs chatim-core-1 2>&1 | grep -c "reconcile term ended"; docker logs chatim-core-2 2>&1 | grep -c "reconcile term ended"`
Expected: số nhỏ (0–2), không tăng liên tục. Ghi số vào báo cáo.

**Step 5: Corebench**

Cùng quy trình lần 13–20 (`docs/poc/README.md`): `make infra-reset && make infra-up && make core-up`, rồi:

```bash
make poc TOOL=corebench ARGS="-rate 5000 -duration 60s -watch 20"
```

Trong lúc chạy, lấy thêm:
- `docker stats --no-stream chatim-core-1 chatim-core-2` vài lần (CPU của core giữ slot 0);
- `curl -s http://127.0.0.1:8223/varz | grep -E '"mem"'` trước và sau (bộ nhớ NATS với cửa sổ dedupe 5m);
- `make redis-cli INSTANCE=dedupe ARGS="info commandstats"` (số `setbit`, `getbit`, `pexpire`).

Expected: `failed=0`, `shed_by_client=0`, live `missing=0 duplicates=0`. Ghi một dòng "M2a.2" vào bảng C1 trong `docs/poc/README.md`, kèm CPU, mem NATS và số lệnh mark.

```bash
git add docs/poc/README.md
git commit -m "docs(poc): record corebench with the event reconciler on"
```

**Step 6: PR**

- Push branch, mở PR `feat/m2a2-event-reconcile` → `main`.
- Mô tả PR: mục tiêu, R1–R10, kết quả `test`/`itest`/`e2e`/corebench, readiness `dev-done`, và những gì còn thiếu cho go-live (oplog retention, alert, mem NATS prod-like).

---

## Ghi chú cho người thực thi

- Không đụng `apps/core/internal/slot` hay `pkg/slotmap`, nên không cần R5.
- Nếu `redisguard.CheckClient` từ chối client dedupe vì tên/cấu hình: đọc `redisguard/guard.go:43`, không đổi client dedupe chỉ để qua kiểm.
- Bitmap mark chỉ cho `msg_created`. Ở M2b, id event theo version (D53) cần namespace riêng; ghi vào plan M2b, không làm ở đây.
- Reconciler publish qua JetStream client riêng, nên `PublishAsyncComplete` của publisher không chờ nó, và ngược lại.
- Finding Minor từ review (chưa sửa, cân nhắc ở M2a.3 hoặc M5):
  - Task 3: `eventmark.offset` còn chặn `> math.MaxInt64` không bao giờ đúng, chỉ để qua gosec. Pipeline `SETBIT` + `PEXPIRE` không phải transaction, nên một chunk có thể mất TTL; `allkeys-lru` vẫn đuổi nó, và cái giá chỉ là publish thừa. `ackMarks.flush` dùng context `Background`, không theo abort. Marker chờ ack theo thứ tự, nên một ack chậm có thể làm đầy hàng đợi 4096 (đúng thiết kế, R2).
  - Task 4: `Marks.Acked` và `Rooms.Get` chạy theo context của `Run`, không theo `stop`, nên một lệnh Redis/Mongo treo có thể làm `Close` chậm tới hết ngân sách của lệnh đó.
  - Task 5: `StopPlan.Reconciler` vẫn được tính khi `RECONCILE_ENABLED=false`. `RECONCILE_DELAY >= EVT_STREAM_DUPLICATES` sinh hai lỗi. Chuỗi `stopPhases` ghi cứng "+ 1s". `marks` và JetStream client của reconcile vẫn được tạo khi tắt reconciler.
  - Task 6: test trong plan gốc đua với term đầu tiên (tin ghi trước khi feed mở thì không được bù); đã sửa bằng cách chờ log `reconcile term started` rồi mới ghi.
  - Task 7: contract `storetest.RunFeed` không có case mất lịch sử. Chỉ memstore có test `LoseHistory`; việc map `ChangeStreamHistoryLost` → `ErrFeedHistoryLost` của mongostore chưa có test (đưa vào chaos test M5).
  - Task 8: cả hai lần corebench đều có một đợt Redis dedupe nghẽn khoảng 1s, hai core cùng suy giảm một lúc. Điều tra ở M2a.3.
- R7 đã sửa sau khi làm xong (bootstrap anchor): `Bootstrap` ghi mốc cluster time vào `reconciler_state`, feed của DB mới đọc từ mốc đó; case contract "inserts before the first open are not replayed" đổi thành "inserts after bootstrap but before the first open are read".
