# M2b.0 — Nền cơ chế — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: dùng `subagent-driven-development` (cùng phiên) hoặc `separate-driven-development` (phiên riêng) để thực thi plan này theo từng task.

**Goal:** Dựng các cơ chế dùng chung mà mọi milestone sau cần: mark ack đúng loại event, delay reconcile có ràng buộc, actor nhường room khi tranh seq, một điểm cắm quyền, reader pipeline cho API đọc, contract cho mọi write method của store, và detector + alert cho mọi guarantee đang có.

**Architecture:**
- `publish`: chỉ event có chính sách ack mark (`msg_created`) mới được mark; boot kiểm `RECONCILE_DELAY > PUB_ACK_TIMEOUT + cửa sổ mark + timeout mark` (D65), mặc định delay 30s → 5s.
- `actor`: tranh seq với doc của writer khác thì nghỉ có backoff + jitter giữa các vòng gán lại; hết lượt gán lại thì actor tự nghỉ (dùng lại cơ chế retire của slot hook), để lệnh sau đi về chủ thật (D77).
- Package mới `access` (permission hook: `Policy` + `Checker`) và `view` (reader pipeline: `Pipeline`, bước đầu `CollapseRetried` che trùng CD2/CD3). `GetHistory` đi qua cả hai; `SendMessage` hỏi cùng `Policy` trong actor.
- Detector: các component giữ bộ đếm atomic và accessor; package mới `metrics` đăng ký chúng vào một Prometheus registry theo kiểu pull (`CounterFunc`/`GaugeFunc`), phục vụ ở `GET /metrics` của cổng admin. Luật alert ở `deploy/prometheus/alerts.yml`, kiểm bằng `promtool` trong Docker (D76, D78).

**Tech Stack:** Go 1.26 trong Docker qua `make`; `github.com/prometheus/client_golang` (mới); mongo-driver v2; nats.go v1.54; go-redis v9 + miniredis; `testing/synctest`; goleak.

**Nguồn quyết định:** [thiết kế](../designs/261005-chatim-architecture.md) §4, §6.1, §8.1, §8.3, §9.2, §12, D65, D76, D77; [roadmap](../roadmap.md) dòng M2b.0; owner chốt Prometheus client cho detector (2026-10-05, ghi D78 ở Task 14).

---

## Quy tắc chung (đọc trước khi làm task nào)

- **Không chạy `go` trên host.** Test một package: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/<pkg>/..."`.
- **Không viết comment** trong Go, test, YAML, shell, Makefile.
- File code dưới 200 dòng; các file đang sát giới hạn: `reconcile/reconciler.go` (198), `actor/router.go` (189), `publish/publisher.go` (181). Plan đã tách phần thêm ra file mới.
- `gosec` bật cho code không phải test: không chuyển `int` → `uint64` (G115). Bộ đếm theo số phần tử dùng `atomic.Int64`.
- Trước mỗi commit: `make fmt-check`, `make vet`, `make lint` + test các package đã đụng. Goroutine/channel/lock mới: thêm `-count=5` cho package đó.
- Commit theo Conventional Commits, không nhắc AI, không có dòng `Co-Authored-By` (skill `commit` của repo).
- Khi một bước cho kết quả khác "Expected": **dừng lại và báo cáo**, không vá cho tới khi qua.
- Review: Task 3 (actor, write path), Task 9 (reconcile) và Task 12 (wiring, lifecycle) rủi ro → mỗi task 1 reviewer kiểm spec + chất lượng một lượt. Các task khác controller kiểm nhanh. Task đụng file khác nhau thì review task N trong lúc làm task N+1.

## Bảng tính năng → lớp dữ liệu (quy tắc roadmap)

M2b.0 không thêm fact hay collection mới; mọi hạng mục là cơ chế dùng chung.

| Hạng mục | Lớp / cơ chế (thiết kế) | Fact | Idempotent | Effect + chính sách | Quyền | View | Event | Khuếch đại | Guarantee + detector |
|---|---|---|---|---|---|---|---|---|---|
| Mark theo loại event | Effect engine §8.1, D65 | — | — | ack mark chỉ cho `msg_created` | — | — | không đổi | 0 | RC1, RC4: `publish_dropped_total`, `ack_marks_dropped_total` |
| Delay reconcile | §8.3 chính sách delay | — | — | `msg_created` 5s | — | — | không đổi | 0 | RC1: `reconcile_lag_seconds` |
| Actor nhường room | §6.1, D77 | Insert tin (đã có) | cid (đã có) | — | — | — | không đổi | ≤ 1 pause/vòng | `room_yields_total` |
| Permission hook | §9.2 | — | — | — | `access.Policy` | — | — | 0 (policy thuần) | — |
| Reader pipeline | §9.2 | — | — | — | — | `view.Pipeline` | — | O(trang) RAM | CD2/CD3: che trùng phía đọc |
| Contract write method | §15 | — | — | — | — | — | — | — | test |
| Detector | §12, D76 | — | — | — | — | — | — | 2 lệnh đọc oplog / scrape | RC1–RC5, CD1–CD3 |

## Thứ tự task

| # | Task | Rủi ro | Phụ thuộc |
|---|---|---|---|
| 0 | Branch | — | — |
| 1 | `publish`: mark chỉ cho event có chính sách, `MarkDeadline` | thấp | — |
| 2 | Config: `RECONCILE_DELAY` 5s + ràng buộc | thấp | 1 |
| 3 | Actor: backoff giữa vòng tranh seq, nhường room, `Stats` | **cao** | — |
| 4 | Package `access` | thấp | — |
| 5 | Actor hỏi `Policy` khi nhận lệnh gửi | trung bình | 3, 4 |
| 6 | Package `view` | thấp | — |
| 7 | `GetHistory` qua `access.Checker` + `view.Pipeline` | trung bình | 4, 6 |
| 8 | Contract: mọi method của port được phân loại | thấp | — |
| 9 | Bộ đếm của `publish` và `reconcile` | **cao** | 1, 2 |
| 10 | `Degraded()` của redisguard; `OplogWindow` của mongostore | thấp | — |
| 11 | Package `metrics` + `GET /metrics` của admin | thấp | — |
| 12 | Wiring trong `apps/core` | **cao** | 3, 5, 7, 9, 10, 11 |
| 13 | Luật alert + `make alerts-check` | thấp | 11 |
| 14 | Docs | thấp | tất cả |
| 15 | Kiểm chứng cuối milestone | — | tất cả |

---

### Task 0: Branch

**Step 1:** Nếu PR `docs/system-mechanisms` đã merge: `git switch main && git pull && git switch -c feat/m2b0-mechanism-foundations`. Nếu chưa merge: tạo branch từ `docs/system-mechanisms` và ghi chú trong PR rằng branch xếp chồng.

**Step 2:** `git branch --show-current` → Expected: `feat/m2b0-mechanism-foundations`.

---

### Task 1: `publish` — mark chỉ cho event có chính sách ack mark

Lỗi đang có: `publisher.go:130` mark mọi event theo (room, thread, seq). Khi có event thay đổi (M2b.2), ack của `msg_edited` sẽ bật bit của tin và reconciler bỏ qua `msg_created` bị rớt.

**Files:**
- Create: `apps/core/internal/publish/ack_mark_policy.go`
- Create: `apps/core/internal/publish/export_test.go`
- Create: `apps/core/internal/publish/ack_mark_policy_test.go`
- Modify: `apps/core/internal/publish/publisher.go:130`

**Step 1: Test**

`apps/core/internal/publish/export_test.go`:

```go
package publish

var MarkKey = markKey
```

`apps/core/internal/publish/ack_mark_policy_test.go`:

```go
package publish_test

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestOnlyMessageCreatedEventsGetAnAckMark(t *testing.T) {
	created := events(roomA, 7)[0]
	if key, ok := publish.MarkKey(roomA, created); !ok || key != (store.MsgKey{Room: roomA, Seq: 7}) {
		t.Fatalf("MarkKey(msg_created) = %v, %v; want {%d 0 7}, true", key, ok, roomA)
	}
	other := &chatimv1.Event{Id: "101-0-7-v2", Seq: 7}
	if key, ok := publish.MarkKey(roomA, other); ok {
		t.Fatalf("MarkKey(event without a msg_created payload) = %v, true; want no mark", key)
	}
}

func TestMarkDeadlineCoversAckTimeoutMarkWindowAndMarkTimeout(t *testing.T) {
	want := 2*time.Second + 10*time.Millisecond + time.Second
	if got := publish.MarkDeadline(2 * time.Second); got != want {
		t.Fatalf("MarkDeadline(2s) = %v, want %v", got, want)
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/publish/..."`
Expected: FAIL biên dịch: `undefined: markKey` và `undefined: publish.MarkDeadline`.

**Step 3: Code**

`apps/core/internal/publish/ack_mark_policy.go`:

```go
package publish

import (
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func MarkDeadline(ackTimeout time.Duration) time.Duration {
	return ackTimeout + markWindow + markTimeout
}

func markKey(room uint64, ev *chatimv1.Event) (store.MsgKey, bool) {
	switch ev.GetPayload().(type) {
	case *chatimv1.Event_MessageCreated:
		return store.MsgKey{Room: room, Thread: ev.GetThreadRoot(), Seq: ev.GetSeq()}, true
	default:
		return store.MsgKey{}, false
	}
}
```

Trong `publisher.go`, thay dòng 130:

```go
		p.marks.track(store.MsgKey{Room: it.room, Thread: ev.GetThreadRoot(), Seq: ev.GetSeq()}, f)
```

bằng:

```go
		if key, ok := markKey(it.room, ev); ok {
			p.marks.track(key, f)
		}
```

Nếu `store` không còn được dùng trong `publisher.go`, xoá import đó.

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/publish/..."`
Expected: PASS, gồm cả 7 test ack mark cũ (mọi event trong test đều là `msg_created`).

**Step 5: Commit**

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/publish/
git commit -m "fix(publish): mark only events whose type has an ack mark policy"
```

---

### Task 2: Config — `RECONCILE_DELAY` 5s và ràng buộc với deadline của mark

**Files:**
- Modify: `apps/core/internal/reconcile/config.go` (`DefaultDelay`)
- Modify: `apps/core/internal/config/validate.go` (rule mới sau dòng 39, import `publish`)
- Modify: `apps/core/internal/config/validate_test.go` (sau dòng 52)
- Modify: `apps/core/internal/config/load_test.go:55`
- Modify: `apps/core/reconcile_integration_test.go:23`
- Modify: `README.md` (dòng env `RECONCILE_DELAY`)

**Step 1: Test**

Thêm vào bảng của `validate_test.go`, ngay sau case `"reconcile disabled ignores its delay"`:

```go
		{"reconcile delay within the ack mark deadline", map[string]string{"RECONCILE_DELAY": "3s"}, "RECONCILE_DELAY must be longer than PUB_ACK_TIMEOUT plus the ack mark window and timeout"},
		{"reconcile delay just past the ack mark deadline", map[string]string{"RECONCILE_DELAY": "3011ms"}, ""},
		{"reconcile disabled ignores the ack mark deadline", map[string]string{"RECONCILE_ENABLED": "false", "RECONCILE_DELAY": "1s"}, ""},
```

Trong `load_test.go:55` đổi `Delay: 30 * time.Second` thành `Delay: 5 * time.Second`.

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/config/..."`
Expected: FAIL ở case `"reconcile delay within the ack mark deadline"` (không có lỗi) và ở test defaults của `load_test.go` (Delay 30s ≠ 5s).

**Step 3: Code**

`reconcile/config.go`: `DefaultDelay = 5 * time.Second`.

`config/validate.go`, thêm import `"github.com/ivannguyendev/chatim/apps/core/internal/publish"` và rule ngay sau rule `RECONCILE_DELAY must be shorter than EVT_STREAM_DUPLICATES`:

```go
		{!c.ReconcileEnabled || c.Reconcile.Delay > publish.MarkDeadline(c.Publish.AckTimeout), "RECONCILE_DELAY must be longer than PUB_ACK_TIMEOUT plus the ack mark window and timeout"},
```

`apps/core/reconcile_integration_test.go:23`: đổi map thành `map[string]string{"CORE_DRAIN_DELAY": "200ms", "RECONCILE_DELAY": "2s", "PUB_ACK_TIMEOUT": "500ms"}` (500ms + 10ms + 1s < 2s).

`README.md`, dòng env `RECONCILE_DELAY`: mặc định `5s`; mô tả thêm "phải dài hơn `PUB_ACK_TIMEOUT` + 10ms + 1s (cửa sổ và timeout của mark) và ngắn hơn `EVT_STREAM_DUPLICATES`".

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/config/... ./apps/core/internal/reconcile/..."`
Expected: PASS.

**Step 5: Commit**

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/reconcile/config.go apps/core/internal/config/ apps/core/reconcile_integration_test.go README.md
git commit -m "feat(config): default reconcile delay to 5s and require it to outlast ack marks"
```

---

### Task 3: Actor — backoff giữa các vòng tranh seq, nhường room, `Stats`

Hiện tại `resolve` gặp doc của writer khác ở cùng seq thì gán lại ngay, không nghỉ, tối đa 3 lần, rồi trả `errSeqContention`; actor ở lại và lệnh kế tiếp lại tranh tiếp. Sau task này: giữa hai vòng gán lại có pause `Jitter(5ms → 10ms → 20ms …, tối đa 200ms)`; hết lượt gán lại thì actor tự nghỉ qua đúng đường retire của slot hook (`a.retire`), trả `errRetired` (ErrRetryLater) cho lệnh đang chờ, nên client retry và đi theo bảng định tuyến mới.

**Files:**
- Create: `apps/core/internal/actor/seq_contention.go`
- Create: `apps/core/internal/actor/router_stats.go`
- Create: `apps/core/internal/actor/seq_contention_test.go`
- Modify: `apps/core/internal/actor/room_actor.go` (struct `actor`, vòng `run`)
- Modify: `apps/core/internal/actor/settle_results.go:80-82`
- Modify: `apps/core/internal/actor/slot_eviction.go:39-41`
- Modify: `apps/core/internal/actor/router.go` (struct `Router`)
- Modify: `apps/core/internal/actor/cid_reservations.go` (nhánh `PendingElsewhere`)
- Modify: `apps/core/internal/actor/config.go` (hằng số)
- Modify: `apps/core/internal/actor/reconcile_test.go` (`TestReassignLimitFailsWithRetryLater`)

**Step 1: Test**

`apps/core/internal/actor/seq_contention_test.go`:

```go
package actor_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/dedupe"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

func TestSeqContentionBacksOffBetweenReassignCycles(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.alwaysDo(rg.sub.foreignFirst)
		rg.start(t)
		begin := time.Now()
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		if waited := time.Since(begin); waited < 17500*time.Microsecond {
			t.Fatalf("three reassign cycles took %v, want at least 17.5ms of jittered backoff", waited)
		}
	})
}

func TestExhaustedContentionYieldsTheRoom(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.sub.alwaysDo(rg.sub.foreignFirst)
		rg.start(t)
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		synctest.Wait()
		if n := rg.ActorCount(); n != 0 {
			t.Fatalf("actors after exhausted contention = %d, want 0", n)
		}
		if got := rg.Stats().Yields; got != 1 {
			t.Fatalf("yields = %d, want 1", got)
		}
	})
}

func TestCIDPendingOnAnotherCoreIsCounted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.cids.force(dedupe.Key{Room: roomA, User: "alice", CID: "c1"}, dedupe.PendingElsewhere, dedupe.Record{})
		rg.start(t)
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, domain.ErrRetryLater)
		if got := rg.Stats().CIDElsewhere; got != 1 {
			t.Fatalf("cid pending elsewhere = %d, want 1", got)
		}
	})
}
```

Trong `reconcile_test.go`, `TestReassignLimitFailsWithRetryLater`: thêm `synctest.Wait()` ngay sau dòng `expectErr(t, err, domain.ErrRetryLater)`. Lý do: actor nhường room sau lỗi; lệnh `mustSend` kế tiếp phải tới sau khi actor cũ đã thoát, để router dựng actor mới (nạp lại `Last` = 4, nên ack seq 5 như cũ).

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on -run 'TestSeqContention|TestExhaustedContention|TestCIDPending|TestReassignLimit' ./apps/core/internal/actor/"`
Expected: FAIL biên dịch: `rg.Stats undefined`. (Sau khi có `Stats` mà chưa có logic: `waited = 0s`, `actors … = 1`.)

**Step 3: Code**

`config.go`, thêm vào khối `const`:

```go
	contentionBackoff    = 5 * time.Millisecond
	maxContentionBackoff = 200 * time.Millisecond
```

`router.go`, thêm import `"sync/atomic"` và hai field cuối struct `Router`:

```go
	yields       atomic.Uint64
	cidElsewhere atomic.Uint64
```

`apps/core/internal/actor/router_stats.go`:

```go
package actor

type Stats struct {
	Yields       uint64
	CIDElsewhere uint64
}

func (r *Router) Stats() Stats {
	return Stats{Yields: r.yields.Load(), CIDElsewhere: r.cidElsewhere.Load()}
}
```

`room_actor.go`: thêm hai field vào struct `actor` (sau `failed []failure`):

```go
	contended      bool
	contentionWait time.Duration
```

và trong `run`, đổi nhánh nhận kết quả:

```go
		case res := <-a.results:
			a.settle(res)
			a.afterContention(ctx)
```

`apps/core/internal/actor/seq_contention.go`:

```go
package actor

import (
	"context"

	"github.com/ivannguyendev/chatim/pkg/backoff"
)

func (a *actor) contend(e *entry) {
	a.stale = true
	a.contended = true
	if e.reassigns >= maxRequeues {
		a.r.yield(a)
	}
	a.requeue(e, false, errSeqContention)
}

func (a *actor) afterContention(ctx context.Context) {
	if !a.contended {
		a.contentionWait = 0
		return
	}
	a.contended = false
	if len(a.retries) == 0 || a.retireRequested() {
		return
	}
	a.contentionWait = min(max(2*a.contentionWait, contentionBackoff), maxContentionBackoff)
	backoff.Pause(ctx, backoff.Jitter(a.contentionWait))
}

func (r *Router) yield(a *actor) {
	r.yields.Add(1)
	r.mu.Lock()
	defer r.mu.Unlock()
	a.requestRetire()
}
```

`settle_results.go`, trong `resolve`, thay nhánh:

```go
		case ok:
			a.stale = true
			a.requeue(e, false, errSeqContention)
```

bằng:

```go
		case ok:
			a.contend(e)
```

`slot_eviction.go`: thêm method (dưới `retireRequested`):

```go
func (a *actor) requestRetire() {
	if !a.retireRequested() {
		close(a.retire)
	}
}
```

và trong `retireSlots` thay

```go
		if !a.retireRequested() {
			close(a.retire)
		}
```

bằng `a.requestRetire()`. Mọi lần đóng `a.retire` đều giữ `r.mu`, nên không đóng hai lần.

`cid_reservations.go`, nhánh `case dedupe.PendingElsewhere:` thêm `a.r.cidElsewhere.Add(1)` trước `a.cache.fail(...)`.

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/actor/"`
Expected: PASS, gồm `TestReassignLimitFailsWithRetryLater`, `TestCompetingCoresKeepEveryTimelineGapless` và các test retire. Test cũ nào fail vì actor nhường room → dừng và báo (không sửa test cho qua).

**Step 5: Commit**

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/actor/
git commit -m "feat(actor): back off between seq contention cycles and yield the room when exhausted"
```

---

### Task 4: Package `access` — một điểm cắm quyền

**Files:**
- Create: `apps/core/internal/access/policy.go`
- Create: `apps/core/internal/access/checker.go`
- Create: `apps/core/internal/access/checker_test.go`

**Step 1: Test**

`apps/core/internal/access/checker_test.go`:

```go
package access_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const room = 4242

func rooms(t *testing.T) *memstore.Rooms {
	t.Helper()
	rs := memstore.NewRooms()
	at := time.Now().UTC()
	r := domain.Room{ID: room, Tenant: "acme", Type: domain.RoomGroup, Name: "team", CreatedBy: "alice", CreatedAt: at, MemberCount: 1}
	m := domain.Member{Room: room, Tenant: "acme", User: "alice", Role: domain.RoleOwner, JoinedAt: at}
	if err := rs.Create(t.Context(), r, []domain.Member{m}); err != nil {
		t.Fatalf("create room: %v", err)
	}
	return rs
}

func TestCheckerEnforcesTenantAndMembershipBeforeThePolicy(t *testing.T) {
	asked := 0
	policy := access.PolicyFunc(func(context.Context, access.Request) error { asked++; return nil })
	c, err := access.NewChecker(rooms(t), policy)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	cases := []struct {
		name         string
		tenant, user string
		room         uint64
		want         error
	}{
		{"unknown room", "acme", "alice", 1, apperr.ErrNotFound},
		{"other tenant", "other", "alice", room, apperr.ErrNotFound},
		{"not a member", "acme", "mallory", room, apperr.ErrPermissionDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := c.Authorize(t.Context(), access.ReadHistory, tc.tenant, tc.user, tc.room); !errors.Is(err, tc.want) {
				t.Fatalf("Authorize = %v, want %v", err, tc.want)
			}
		})
	}
	if asked != 0 {
		t.Fatalf("policy asked %d times before the invariants passed, want 0", asked)
	}
}

func TestCheckerPassesTheFullRequestToThePolicy(t *testing.T) {
	var got access.Request
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error { got = r; return access.ErrDenied })
	c, err := access.NewChecker(rooms(t), deny)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	if _, err := c.Authorize(t.Context(), access.ReadHistory, "acme", "alice", room); !errors.Is(err, apperr.ErrPermissionDenied) {
		t.Fatalf("Authorize = %v, want ErrPermissionDenied", err)
	}
	if got.Action != access.ReadHistory || got.User != "alice" || got.Room.ID != room || got.Member.Role != domain.RoleOwner {
		t.Fatalf("policy saw %+v, want read_history by owner alice in room %d", got, room)
	}
}

func TestNilPolicyAllowsMembers(t *testing.T) {
	c, err := access.NewChecker(rooms(t), nil)
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}
	req, err := c.Authorize(t.Context(), access.ReadHistory, "acme", "alice", room)
	if err != nil || req.Member.User != "alice" {
		t.Fatalf("Authorize = %+v, %v; want alice allowed", req, err)
	}
	if _, err := access.NewChecker(nil, nil); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("NewChecker(nil) = %v, want ErrInvalidArgument", err)
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/access/..."`
Expected: FAIL biên dịch: package `access` chưa có file không phải test.

**Step 3: Code**

`apps/core/internal/access/policy.go`:

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
	ReadHistory Action = "read_history"
	SendMessage Action = "send_message"
)

var ErrDenied = fmt.Errorf("action denied: %w", apperr.ErrPermissionDenied)

type Request struct {
	Action Action
	User   string
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
```

`apps/core/internal/access/checker.go`:

```go
package access

import (
	"context"
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
		policy = AllowMembers{}
	}
	return &Checker{rooms: rooms, policy: policy}, nil
}

func (c *Checker) Authorize(ctx context.Context, action Action, tenant, user string, room uint64) (Request, error) {
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
	req := Request{Action: action, User: user, Room: r, Member: m}
	return req, c.policy.Check(ctx, req)
}
```

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/access/..."`
Expected: PASS.

**Step 5: Commit**

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/access/
git commit -m "feat(access): add one permission hook with tenant and membership invariants"
```

---

### Task 5: Actor hỏi `Policy` khi nhận lệnh gửi

Cache member của actor đổi từ `struct{}` sang `domain.Member`, để policy theo role (Phase 2) có dữ liệu mà không thêm lượt đọc DB.

**Files:**
- Create: `apps/core/internal/actor/router_options.go`
- Create: `apps/core/internal/actor/access_policy_test.go`
- Modify: `apps/core/internal/actor/router.go` (`NewRouter`, struct `Router`)
- Modify: `apps/core/internal/actor/room_actor.go` (field `members`, `newActor`)
- Modify: `apps/core/internal/actor/room_state.go` (`admit`, `member`)
- Modify: `apps/core/internal/actor/harness_test.go` (`newRig`)

**Step 1: Test**

`harness_test.go`: đổi chữ ký thành `func newRig(t *testing.T, cfg actor.Config, opts ...actor.Option) *rig` và dòng 63 thành `r, err := actor.NewRouter(rg.msgs, rg.rooms, rg.sub, rg.cids, rg.events, cfg, quiet, opts...)`.

`apps/core/internal/actor/access_policy_test.go`:

```go
package actor_test

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestSendAsksThePolicyBeforeWriting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var got []access.Request
		deny := access.PolicyFunc(func(_ context.Context, r access.Request) error {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, r)
			return access.ErrDenied
		})
		rg := newRig(t, baseConfig, actor.WithPolicy(deny))
		rg.start(t)
		_, err := rg.Send(t.Context(), cmd(roomA, "alice", "c1"))
		expectErr(t, err, apperr.ErrPermissionDenied)
		if n := len(rg.sub.sent()); n != 0 {
			t.Fatalf("denied send reached the flusher %d times", n)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(got) != 1 || got[0].Action != access.SendMessage || got[0].User != "alice" || got[0].Room.ID != roomA || got[0].Member.User != "alice" {
			t.Fatalf("policy saw %+v, want one send_message by member alice in room %d", got, roomA)
		}
	})
}

func TestDefaultPolicyAllowsMembers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, baseConfig)
		rg.start(t)
		if ack := mustSend(t, rg.Router, cmd(roomA, "alice", "c1")); ack.Seq != 1 {
			t.Fatalf("seq = %d, want 1", ack.Seq)
		}
	})
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on -run 'TestSendAsksThePolicy|TestDefaultPolicy' ./apps/core/internal/actor/"`
Expected: FAIL biên dịch: `undefined: actor.Option`, `undefined: actor.WithPolicy`.

**Step 3: Code**

`apps/core/internal/actor/router_options.go`:

```go
package actor

import "github.com/ivannguyendev/chatim/apps/core/internal/access"

type Option func(*Router)

func WithPolicy(p access.Policy) Option {
	return func(r *Router) {
		if p != nil {
			r.policy = p
		}
	}
}
```

`router.go`: thêm field `policy access.Policy` (sau `events EventPublisher`) và import `access`; đổi `NewRouter` thành nhận `opts ...Option`:

```go
func NewRouter(msgs store.Messages, rooms store.Rooms, sub Submitter, cids CIDRegistry, events EventPublisher, cfg Config, log *slog.Logger, opts ...Option) (*Router, error) {
```

và thay `return &Router{...}, nil` bằng:

```go
	r := &Router{
		msgs:    msgs,
		rooms:   rooms,
		sub:     sub,
		cids:    cids,
		events:  events,
		policy:  access.AllowMembers{},
		cfg:     cfg,
		log:     log,
		actors:  make(map[uint64]*actor),
		running: make(chan struct{}),
		closing: make(chan struct{}),
		done:    make(chan struct{}),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r, nil
```

`room_actor.go`: field `members *lru[string, domain.Member]`; trong `newActor`: `members: newLRU[string, domain.Member](memberCacheSize),`.

`room_state.go`, thay `admit` (phần kiểm member) và `member`:

```go
	m, err := a.member(ctx, c.User)
	if err != nil {
		q.answer(Ack{}, err)
		return nil
	}
	if err := a.r.policy.Check(ctx, access.Request{Action: access.SendMessage, User: c.User, Room: a.room, Member: m}); err != nil {
		q.answer(Ack{}, err)
		return nil
	}
```

```go
func (a *actor) member(ctx context.Context, user string) (domain.Member, error) {
	if m, ok := a.members.get(user); ok {
		return m, nil
	}
	m, err := a.r.rooms.Member(ctx, a.id, user)
	switch {
	case err == nil:
		a.members.put(user, m)
		return m, nil
	case errors.Is(err, domain.ErrNotMember):
		return domain.Member{}, domain.ErrNotMember
	default:
		a.r.log.WarnContext(ctx, "membership check failed", "room", a.id, "err", err)
		return domain.Member{}, errUnavailable
	}
}
```

Thêm import `access` vào `room_state.go`.

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/actor/... ./apps/core/internal/grpcsrv/..."`
Expected: PASS (grpcsrv dựng router với chữ ký cũ, vẫn biên dịch vì `opts` là variadic).

**Step 5: Commit**

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/actor/
git commit -m "feat(actor): ask the permission policy before admitting a send"
```

---

### Task 6: Package `view` — reader pipeline

Bước đầu tiên: che bản trùng của một lần gửi được lưu hai lần (CD2/CD3): cùng `(From, CID)` khác seq thì giữ seq nhỏ nhất trong trang. Lỗ seq của bản bị che là chấp nhận được (D71).

**Files:**
- Create: `apps/core/internal/view/pipeline.go`
- Create: `apps/core/internal/view/collapse_retried.go`
- Create: `apps/core/internal/view/pipeline_test.go`

**Step 1: Test**

`apps/core/internal/view/pipeline_test.go`:

```go
package view_test

import (
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/view"
)

func msg(seq uint64, from, cid string) domain.Message {
	return domain.Message{Room: 7, Seq: seq, From: from, CID: cid}
}

func seqs(msgs []domain.Message) []uint64 {
	out := make([]uint64, len(msgs))
	for i, m := range msgs {
		out[i] = m.Seq
	}
	return out
}

func TestCollapseRetriedKeepsTheLowestSeqOfEachSend(t *testing.T) {
	page := []domain.Message{msg(9, "alice", "c-1"), msg(4, "alice", "c-1"), msg(5, "bob", "c-1"), msg(6, "alice", "c-2"), msg(7, "", ""), msg(8, "", "")}
	before := slices.Clone(page)
	got := view.CollapseRetried(view.Viewer{User: "bob"}, page)
	if want := []uint64{4, 5, 6, 7, 8}; !slices.Equal(seqs(got), want) {
		t.Fatalf("seqs = %v, want %v", seqs(got), want)
	}
	if !slices.Equal(page, before) {
		t.Fatalf("input page was modified")
	}
}

func TestPipelineRunsStepsInOrder(t *testing.T) {
	var order []string
	step := func(name string) view.Step {
		return func(_ view.Viewer, msgs []domain.Message) []domain.Message {
			order = append(order, name)
			return msgs[1:]
		}
	}
	got := view.New(step("a"), step("b")).Apply(view.Viewer{}, []domain.Message{msg(1, "x", "1"), msg(2, "x", "2"), msg(3, "x", "3")})
	if !slices.Equal(order, []string{"a", "b"}) || !slices.Equal(seqs(got), []uint64{3}) {
		t.Fatalf("order = %v, seqs = %v; want [a b], [3]", order, seqs(got))
	}
	if got := view.Default().Apply(view.Viewer{}, nil); len(got) != 0 {
		t.Fatalf("default pipeline on nil = %v, want empty", got)
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/view/..."`
Expected: FAIL biên dịch: package `view` chưa có file không phải test.

**Step 3: Code**

`apps/core/internal/view/pipeline.go`:

```go
package view

import (
	"slices"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

type Viewer struct {
	User string
	Room domain.Room
}

type Step func(v Viewer, msgs []domain.Message) []domain.Message

type Pipeline struct {
	steps []Step
}

func New(steps ...Step) Pipeline { return Pipeline{steps: slices.Clone(steps)} }

func Default() Pipeline { return New(CollapseRetried) }

func (p Pipeline) Apply(v Viewer, msgs []domain.Message) []domain.Message {
	for _, step := range p.steps {
		msgs = step(v, msgs)
	}
	return msgs
}
```

`apps/core/internal/view/collapse_retried.go`:

```go
package view

import "github.com/ivannguyendev/chatim/apps/core/internal/domain"

type sendKey struct {
	from, cid string
}

func CollapseRetried(_ Viewer, msgs []domain.Message) []domain.Message {
	first := make(map[sendKey]uint64, len(msgs))
	for _, m := range msgs {
		if m.CID == "" {
			continue
		}
		k := sendKey{m.From, m.CID}
		if seq, ok := first[k]; !ok || m.Seq < seq {
			first[k] = m.Seq
		}
	}
	out := make([]domain.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.CID != "" && first[sendKey{m.From, m.CID}] != m.Seq {
			continue
		}
		out = append(out, m)
	}
	return out
}
```

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/view/..."`
Expected: PASS.

**Step 5: Commit**

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/view/
git commit -m "feat(view): add the reader pipeline with a step that hides retried duplicates"
```

---

### Task 7: `GetHistory` qua `access.Checker` và `view.Pipeline`

**Files:**
- Modify: `apps/core/internal/grpcsrv/core_service.go` (`Deps`, `Service`, `New`)
- Modify: `apps/core/internal/grpcsrv/get_history.go` (bỏ `authorizeRead`)
- Modify: `apps/core/internal/grpcsrv/harness_test.go` (`options`, `newRig`)
- Create: `apps/core/internal/grpcsrv/history_view_test.go`

**Step 1: Test**

`harness_test.go`: thêm field `policy access.Policy` vào `options` và `Policy: o.policy` vào `grpcsrv.Deps` ở `newRig` (dòng 66). Thêm import `access`.

`apps/core/internal/grpcsrv/history_view_test.go`:

```go
package grpcsrv_test

import (
	"context"
	"slices"
	"strconv"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/ivannguyendev/chatim/apps/core/internal/access"
	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func TestHistoryHidesASendStoredTwice(t *testing.T) {
	rg := newRig(t, options{})
	room := rg.createGroup(t, "acme", "alice")
	id, err := strconv.ParseUint(room, 10, 64)
	if err != nil {
		t.Fatalf("room id %q: %v", room, err)
	}
	at := time.Now().UTC().Truncate(time.Millisecond)
	stored := func(seq uint64, cid string) domain.Message {
		return domain.Message{Room: id, Seq: seq, Tenant: "acme", From: "alice", Kind: domain.KindText, Text: "hi", CID: cid, CreatedAt: at}
	}
	for _, r := range rg.msgs.Insert(t.Context(), []domain.Message{stored(1, "c-1"), stored(2, "c-1"), stored(3, "c-2")}) {
		if r.Outcome != store.Inserted {
			t.Fatalf("insert: %v %v", r.Outcome, r.Err)
		}
	}
	resp, err := rg.client.GetHistory(as(t, "acme", "alice"), &chatimv1.GetHistoryRequest{RoomId: room, Anchor: chatimv1.HistoryAnchor_HISTORY_ANCHOR_OLDEST})
	if err != nil {
		t.Fatalf("GetHistory: %v", err)
	}
	var got []uint64
	for _, m := range resp.GetMessages() {
		got = append(got, m.GetSeq())
	}
	if !slices.Equal(got, []uint64{1, 3}) {
		t.Fatalf("seqs = %v, want [1 3]", got)
	}
}

func TestHistoryAsksThePolicy(t *testing.T) {
	var got access.Request
	deny := access.PolicyFunc(func(_ context.Context, r access.Request) error { got = r; return access.ErrDenied })
	rg := newRig(t, options{policy: deny})
	room := rg.createGroup(t, "acme", "alice")
	_, err := rg.client.GetHistory(as(t, "acme", "alice"), &chatimv1.GetHistoryRequest{RoomId: room})
	expectCode(t, err, codes.PermissionDenied)
	if got.Action != access.ReadHistory || got.User != "alice" || got.Member.User != "alice" {
		t.Fatalf("policy saw %+v, want read_history by member alice", got)
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/grpcsrv/..."`
Expected: FAIL biên dịch: `unknown field Policy in struct literal of type grpcsrv.Deps`.

**Step 3: Code**

`core_service.go`: thêm `Policy access.Policy` vào `Deps`; thay field `rooms store.Rooms` của `Service` bằng hai field và giữ `rooms` (CreateRoom vẫn dùng):

```go
type Service struct {
	chatimv1.UnimplementedCoreServiceServer
	sender Sender
	rooms  store.Rooms
	pages  PageReader
	access *access.Checker
	view   view.Pipeline
	newID  func() uint64
	now    func() time.Time
	log    *slog.Logger
}
```

trong `New`, trước `return`:

```go
	checker, err := access.NewChecker(d.Rooms, d.Policy)
	if err != nil {
		return nil, err
	}
	return &Service{sender: d.Sender, rooms: d.Rooms, pages: d.Pages, access: checker, view: view.Default(), newID: d.NewID, now: d.Now, log: log}, nil
```

Thêm import `access`, `view`.

`get_history.go`: xoá `authorizeRead`; trong `GetHistory` thay khối `if err := s.authorizeRead(...)` … `return &chatimv1.GetHistoryResponse{...}` bằng:

```go
	req, err := s.access.Authorize(ctx, access.ReadHistory, who.tenant, who.user, q.Room)
	if err != nil {
		return nil, err
	}
	page, err := s.pages.Page(ctx, q)
	if err != nil {
		return nil, err
	}
	page = s.view.Apply(view.Viewer{User: who.user, Room: req.Room}, page)
	out := make([]*chatimv1.Message, len(page))
	for i, m := range page {
		out[i] = pbconv.Message(m)
	}
	return &chatimv1.GetHistoryResponse{Messages: out}, nil
```

Sửa import: thêm `access`, `view`; bỏ `domain` nếu không còn dùng.

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/grpcsrv/..."`
Expected: PASS, gồm `TestHistoryHidesRoomsOfOtherTenants` (NotFound) và `TestHistoryRequiresMembership` (PermissionDenied).

**Step 5: Commit**

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/grpcsrv/
git commit -m "feat(grpcsrv): route history reads through the permission hook and reader pipeline"
```

---

### Task 8: Contract — mọi method của port được phân loại

Test này chặn việc thêm method ghi vào port mà không chọn loại (insert unique, CAS, upsert, bump version) và không thêm case vào `storetest`. `ChangeFeed.Forget` là ngoại lệ vận hành (`reset`, chỉ sau mất lịch sử).

**Files:**
- Create: `apps/core/internal/store/write_contract_test.go`

**Step 1: Test**

```go
package store_test

import (
	"reflect"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

var allowedKinds = map[string]bool{
	"read": true, "lifecycle": true, "reset": true,
	"insert-unique": true, "cas": true, "monotonic-cas": true, "upsert": true, "version-bump": true,
}

var portMethods = map[string]string{
	"Messages.Insert":   "insert-unique",
	"Messages.Last":     "read",
	"Messages.Page":     "read",
	"Messages.Find":     "read",
	"Rooms.Create":      "insert-unique",
	"Rooms.Get":         "read",
	"Rooms.Member":      "read",
	"ChangeFeed.Open":   "read",
	"ChangeFeed.Forget": "reset",
	"Cursor.Next":       "read",
	"Cursor.Confirm":    "monotonic-cas",
	"Cursor.Close":      "lifecycle",
}

func TestEveryPortMethodHasAWriteContract(t *testing.T) {
	ports := []reflect.Type{
		reflect.TypeFor[store.Messages](),
		reflect.TypeFor[store.Rooms](),
		reflect.TypeFor[store.ChangeFeed](),
		reflect.TypeFor[store.Cursor](),
	}
	seen := map[string]bool{}
	for _, p := range ports {
		for i := range p.NumMethod() {
			name := p.Name() + "." + p.Method(i).Name
			seen[name] = true
			kind, ok := portMethods[name]
			switch {
			case !ok:
				t.Errorf("%s has no write contract: classify it here and add its storetest case", name)
			case !allowedKinds[kind]:
				t.Errorf("%s has kind %q; writes must be insert-unique, cas, monotonic-cas, upsert or version-bump", name, kind)
			}
		}
	}
	for name := range portMethods {
		if !seen[name] {
			t.Errorf("%s is classified but is not a port method", name)
		}
	}
}
```

**Step 2: Chạy**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/"`
Expected: PASS (test chốt hiện trạng).

**Step 3: Kiểm test bắt được vi phạm**

Tạm xoá dòng `"Cursor.Close": "lifecycle",`, chạy lại lệnh trên. Expected: FAIL `Cursor.Close has no write contract`. Khôi phục dòng đó, chạy lại → PASS.

**Step 4: Commit**

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/store/write_contract_test.go
git commit -m "test(store): require every port method to declare its write contract"
```

---

### Task 9: Bộ đếm của `publish` và `reconcile`

**Files:**
- Create: `apps/core/internal/publish/drop_counters.go`
- Create: `apps/core/internal/publish/drop_counters_test.go`
- Modify: `apps/core/internal/publish/publisher.go` (struct, `New`, `publish`, `Enqueue`)
- Modify: `apps/core/internal/publish/ack_marks.go` (struct `ackMarks`, `track`, `flush`)
- Modify: `apps/core/internal/publish/async_failures.go` (`AsyncFailureHandler`)
- Modify: `apps/core/internal/publish/config.go` (`JetStreamOptions`)
- Modify: `apps/core/internal/publish/order_test.go:65`, `lifecycle_test.go:136`, `nats_integration_test.go:43`
- Modify: `apps/core/clients.go` (struct `clients`, `connectNATS`)
- Create: `apps/core/internal/reconcile/stats.go`
- Create: `apps/core/internal/reconcile/limited_log.go` (dời `limitedLog` + `sleep` ra khỏi `reconciler.go`)
- Create: `apps/core/internal/reconcile/stats_test.go`
- Modify: `apps/core/internal/reconcile/reconciler.go`, `term.go`

**Step 1: Test publish**

`apps/core/internal/publish/drop_counters_test.go`:

```go
package publish_test

import (
	"errors"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/testlog"
)

func TestQueueFullDropsAreCounted(t *testing.T) {
	c := &publish.Counters{}
	cfg := fastSetup
	cfg.QueueSize = 1
	rg := newRig(t, cfg, publish.WithCounters(c))
	rg.enqueue(t, roomA, 1, 2)
	if err := rg.Enqueue(roomA, events(roomA, 3, 4, 5)); !errors.Is(err, publish.ErrQueueFull) {
		t.Fatalf("Enqueue on a full queue = %v, want ErrQueueFull", err)
	}
	if got := c.Drops().QueueFull; got != 3 {
		t.Fatalf("queue full drops = %d, want 3", got)
	}
}

func TestRefusedPublishesAreCounted(t *testing.T) {
	c := &publish.Counters{}
	rg := newRig(t, fastSetup, publish.WithCounters(c)).start(t)
	rg.js.RefuseWhen(func(*nats.Msg) error { return errRefused })
	rg.enqueue(t, roomA, 1, 2)
	eventually(t, "two refused publishes counted", func() bool { return c.Drops().Refused == 2 })
}

func TestAsyncFailuresAreCounted(t *testing.T) {
	c := &publish.Counters{}
	handle := publish.AsyncFailureHandler((&testlog.Sink{}).Logger(), c)
	msg := &nats.Msg{Subject: "evt.acme.room.101.msg_created", Header: nats.Header{}}
	msg.Header.Set(jetstream.MsgIDHeader, "101-0-1")
	for range 4 {
		handle(nil, msg, errRefused)
	}
	if got := c.Drops().AsyncFailed; got != 4 {
		t.Fatalf("async failures = %d, want 4", got)
	}
}

func TestFailedMarksAreCounted(t *testing.T) {
	c := &publish.Counters{}
	marker := &recordingMarker{err: errRefused}
	rg := newRig(t, fastSetup, publish.WithAckMarks(marker), publish.WithCounters(c)).start(t)
	rg.enqueue(t, roomA, 1, 2)
	eventually(t, "two failed marks counted", func() bool { return c.Drops().MarkFailed == 2 })
}
```

Sửa call site cũ: `order_test.go:65` → `publish.AsyncFailureHandler(sink.Logger(), nil)`; `lifecycle_test.go:136` → `publish.Config{}.JetStreamOptions(nil, nil)`; `nats_integration_test.go:43` → `pub.JetStreamOptions(failures.Logger(), nil)`.

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/publish/..."`
Expected: FAIL biên dịch: `undefined: publish.Counters`, `publish.WithCounters`.

**Step 3: Code publish**

`apps/core/internal/publish/drop_counters.go`:

```go
package publish

import "sync/atomic"

type Counters struct {
	queueFull     atomic.Int64
	malformed     atomic.Int64
	refused       atomic.Int64
	asyncFailed   atomic.Int64
	markQueueFull atomic.Int64
	markFailed    atomic.Int64
}

type Drops struct {
	QueueFull     int64
	Malformed     int64
	Refused       int64
	AsyncFailed   int64
	MarkQueueFull int64
	MarkFailed    int64
}

func (c *Counters) Drops() Drops {
	return Drops{
		QueueFull:     c.queueFull.Load(),
		Malformed:     c.malformed.Load(),
		Refused:       c.refused.Load(),
		AsyncFailed:   c.asyncFailed.Load(),
		MarkQueueFull: c.markQueueFull.Load(),
		MarkFailed:    c.markFailed.Load(),
	}
}

func WithCounters(c *Counters) Option {
	return func(p *Publisher) {
		if c != nil {
			p.counters = c
		}
	}
}
```

`publisher.go`:
- struct `Publisher`: thêm field `counters *Counters`.
- `New`: trong literal `&Publisher{...}` thêm `counters: &Counters{}`; sau vòng `for _, opt := range opts` thêm:

```go
	if p.marks != nil {
		p.marks.counters = p.counters
	}
```

- `publish`: sau `p.fails.record("dropping malformed event", ...)` thêm `p.counters.malformed.Add(1)`; sau `p.fails.record("event publish refused; ...", ...)` thêm `p.counters.refused.Add(1)`.
- `Enqueue`, nhánh `default:` (hàng đầy), ngay đầu nhánh: `p.counters.queueFull.Add(int64(len(events)))`.

`ack_marks.go`:
- struct `ackMarks`: thêm field `counters *Counters`.
- `track`, nhánh `default:` ngay đầu: `a.counters.markQueueFull.Add(1)`.
- `flush`, trong `if err := a.marker.Mark(ctx, keys); err != nil {`: thêm `a.counters.markFailed.Add(int64(len(keys)))`.
- `WithAckMarks`: thêm `counters: &Counters{}` vào literal `&ackMarks{...}` (New sẽ thay bằng bộ đếm của publisher).

`async_failures.go`:

```go
func AsyncFailureHandler(log *slog.Logger, counters *Counters) jetstream.MsgErrHandler {
	if log == nil {
		log = slog.Default()
	}
	if counters == nil {
		counters = &Counters{}
	}
	f := &failureLog{log: log}
	return func(_ jetstream.JetStream, m *nats.Msg, err error) {
		counters.asyncFailed.Add(1)
		f.record("event publish failed; reconciliation must republish it", m.Header.Get(jetstream.MsgIDHeader), err)
	}
}
```

`config.go`: `func (c Config) JetStreamOptions(log *slog.Logger, counters *Counters) []jetstream.JetStreamOpt` và `jetstream.WithPublishAsyncErrHandler(AsyncFailureHandler(log, counters))`.

`apps/core/clients.go`: thêm field `pubCounters *publish.Counters` vào `clients`; trong `connectNATS` trước `jetstream.New(...)`: `c.pubCounters = &publish.Counters{}` và gọi `cfg.Publish.JetStreamOptions(log, c.pubCounters)`. Thêm import `publish`.

**Step 4: Chạy publish, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/publish/..." && make -s go ARGS="vet ./apps/core/..."`
Expected: PASS; vet sạch.

**Step 5: Test reconcile**

`apps/core/internal/reconcile/stats_test.go`:

```go
package reconcile_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestStatsTrackTermsRepublishesAndLag(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.marks.mark(store.MsgKey{Room: room, Seq: 1})
		rg.insert(t, room, 1, 2)
		time.Sleep(delay + tick)
		synctest.Wait()
		s := rg.Stats()
		if !s.Running || s.Terms != 1 || s.Republished != 1 || s.Lag < delay {
			t.Fatalf("stats = %+v, want running, 1 term, 1 republished, lag >= %v", s, delay)
		}
		rg.owner.leading.Store(false)
		time.Sleep(2 * tick)
		synctest.Wait()
		if s := rg.Stats(); s.Running || s.Lag != 0 {
			t.Fatalf("stats after losing the lead = %+v, want not running and zero lag", s)
		}
	})
}

func TestStatsCountLostHistory(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil)
		rg.feed.LoseHistory()
		rg.start(t)
		time.Sleep(2 * tick)
		synctest.Wait()
		if got := rg.Stats().HistoryLost; got != 1 {
			t.Fatalf("history lost = %d, want 1", got)
		}
	})
}
```

**Step 6: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/reconcile/..."`
Expected: FAIL biên dịch: `rg.Stats undefined`.

**Step 7: Code reconcile**

Dời `type limitedLog`, method `warn` và hàm `sleep` (dòng 172–198 của `reconciler.go`) sang `apps/core/internal/reconcile/limited_log.go` không đổi nội dung (package `reconcile`, import `context`, `log/slog`, `sync`, `time`).

`apps/core/internal/reconcile/stats.go`:

```go
package reconcile

import (
	"sync/atomic"
	"time"
)

type Stats struct {
	Running     bool
	Terms       uint64
	Republished uint64
	Dropped     uint64
	HistoryLost uint64
	Lag         time.Duration
}

type counters struct {
	running     atomic.Bool
	terms       atomic.Uint64
	republished atomic.Uint64
	historyLost atomic.Uint64
	lag         atomic.Int64
}

func (r *Reconciler) Stats() Stats {
	return Stats{
		Running:     r.stats.running.Load(),
		Terms:       r.stats.terms.Load(),
		Republished: r.stats.republished.Load(),
		Dropped:     r.dropped.Load(),
		HistoryLost: r.stats.historyLost.Load(),
		Lag:         time.Duration(r.stats.lag.Load()),
	}
}

func (r *Reconciler) termStarted() {
	r.stats.terms.Add(1)
	r.stats.running.Store(true)
}

func (r *Reconciler) termEnded() {
	r.stats.running.Store(false)
	r.stats.lag.Store(0)
}
```

`reconciler.go`:
- struct `Reconciler`: thêm field `stats counters` (sau `dropped atomic.Uint64`).
- `term`: ngay sau `r.log.InfoContext(ctx, "reconcile term started")` thêm `r.termStarted()` và `defer r.termEnded()`.
- `ended`: trong nhánh `case errors.Is(err, store.ErrFeedHistoryLost):` thêm dòng đầu `r.stats.historyLost.Add(1)`.
- `watchLag`: đổi thành

```go
func (r *Reconciler) watchLag(ctx context.Context, committed time.Time) {
	lag := time.Since(committed)
	r.stats.lag.Store(int64(lag))
	if lag > r.cfg.DuplicateWindow {
		r.lags.warn(ctx, lagMsg, "lag", lag, "window", r.cfg.DuplicateWindow)
	}
}
```

`term.go`, trong `publish`, sau `t.win.send(c.Position, msg)`: thêm `t.r.stats.republished.Add(1)`.

**Step 8: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/reconcile/..."`
Expected: PASS. `wc -l apps/core/internal/reconcile/reconciler.go` < 200.

**Step 9: Commit**

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/publish/ apps/core/internal/reconcile/ apps/core/clients.go
git commit -m "feat(core): count dropped events, failed marks and reconciler terms, republishes and lag"
```

---

### Task 10: `Degraded()` của redisguard; `OplogWindow` của mongostore

**Files:**
- Create: `apps/core/internal/redisguard/guard_state.go`
- Create: `apps/core/internal/redisguard/guard_state_test.go`
- Modify: `apps/core/internal/dedupe/redis_store.go` (method `Degraded`)
- Modify: `apps/core/internal/eventmark/store.go` (method `Degraded`)
- Create: `apps/core/internal/store/mongostore/oplog_window.go`
- Create: `apps/core/internal/store/mongostore/oplog_window_integration_test.go`

**Step 1: Test redisguard**

`apps/core/internal/redisguard/guard_state_test.go` (package `redisguard`, dùng helper `newGuard` và `errBoom` của `guard_test.go`; đồng hồ giả, cooldown 1s):

```go
package redisguard

import (
	"context"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/testlog"
)

func TestDegradedFollowsFailuresAndRecovery(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	g := newGuard(t, &testlog.Sink{}, &now)
	if g.Degraded() {
		t.Fatal("fresh guard reports degraded")
	}
	_ = g.Do(t.Context(), "op", func(context.Context) error { return errBoom })
	if !g.Degraded() {
		t.Fatal("guard not degraded after a failed call")
	}
	now = now.Add(time.Second)
	if err := g.Do(t.Context(), "op", func(context.Context) error { return nil }); err != nil {
		t.Fatalf("probe after cooldown = %v", err)
	}
	if g.Degraded() {
		t.Fatal("guard still degraded after a successful probe")
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/redisguard/..."`
Expected: FAIL biên dịch: `g.Degraded undefined`.

**Step 3: Code**

`apps/core/internal/redisguard/guard_state.go`:

```go
package redisguard

func (g *Guard) Degraded() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.degraded
}
```

`dedupe/redis_store.go` và `eventmark/store.go`, cuối file:

```go
func (s *Store) Degraded() bool { return s.guard.Degraded() }
```

`apps/core/internal/store/mongostore/oplog_window.go`:

```go
package mongostore

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func OplogWindow(ctx context.Context, client *mongo.Client) (time.Duration, error) {
	oplog := client.Database("local").Collection("oplog.rs")
	first, err := oplogTime(ctx, oplog, 1)
	if err != nil {
		return 0, err
	}
	last, err := oplogTime(ctx, oplog, -1)
	if err != nil {
		return 0, err
	}
	return time.Duration(last.T-first.T) * time.Second, nil
}

func oplogTime(ctx context.Context, oplog *mongo.Collection, direction int) (bson.Timestamp, error) {
	var doc struct {
		TS bson.Timestamp `bson:"ts"`
	}
	opts := options.FindOne().SetSort(bson.D{{Key: "$natural", Value: direction}}).SetProjection(bson.D{{Key: "ts", Value: 1}})
	err := oplog.FindOne(ctx, bson.D{}, opts).Decode(&doc)
	return doc.TS, err
}
```

`apps/core/internal/store/mongostore/oplog_window_integration_test.go`:

```go
package mongostore

import "testing"

func TestOplogWindowReadsTheReplicaSetLog(t *testing.T) {
	client := itClient(t)
	window, err := OplogWindow(t.Context(), client)
	if err != nil {
		t.Fatalf("OplogWindow: %v", err)
	}
	if window < 0 {
		t.Fatalf("window = %v, want >= 0", window)
	}
}
```

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/redisguard/... ./apps/core/internal/dedupe/... ./apps/core/internal/eventmark/... ./apps/core/internal/store/mongostore/..."`
Expected: PASS (test oplog skip vì không có `CHATIM_IT_MONGO_URI`). Itest chạy ở Task 15.

**Step 5: Commit**

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/redisguard/ apps/core/internal/dedupe/redis_store.go apps/core/internal/eventmark/store.go apps/core/internal/store/mongostore/
git commit -m "feat(core): expose redis degraded state and the mongo oplog window"
```

---

### Task 11: Package `metrics` và `GET /metrics` của admin

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `apps/core/internal/metrics/registry.go`
- Create: `apps/core/internal/metrics/registry_test.go`
- Modify: `pkg/admin/admin_config.go` (field `Metrics`)
- Modify: `pkg/admin/admin_server.go` (`routes`)
- Create: `pkg/admin/metrics_route_test.go`

**Step 1: Dependency**

Run: `make go ARGS="get github.com/prometheus/client_golang@latest" && make tidy`
Expected: `go.mod` có `github.com/prometheus/client_golang vX.Y.Z` ở khối require trực tiếp. Ghi version vào commit body. Sau đó `make vuln` → Expected: `No vulnerabilities found.`

**Step 2: Test metrics**

`apps/core/internal/metrics/registry_test.go`:

```go
package metrics_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/metrics"
)

func TestHandlerExposesCountersAndGaugesFromSources(t *testing.T) {
	queueFull, refused := 3.0, 1.0
	h, err := metrics.Handler([]metrics.Source{
		{Name: "publish_dropped_total", Help: "Events dropped before JetStream acked them.", Labels: map[string]string{"reason": "queue_full"}, Read: func() float64 { return queueFull }},
		{Name: "publish_dropped_total", Help: "Events dropped before JetStream acked them.", Labels: map[string]string{"reason": "refused"}, Read: func() float64 { return refused }},
		{Name: "reconcile_running", Help: "1 while this core runs a reconcile term.", Gauge: true, Read: func() float64 { return 1 }},
	})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	for _, want := range []string{
		`chatim_core_publish_dropped_total{reason="queue_full"} 3`,
		`chatim_core_publish_dropped_total{reason="refused"} 1`,
		`# TYPE chatim_core_reconcile_running gauge`,
		`chatim_core_reconcile_running 1`,
		`go_goroutines`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("metrics output lacks %q", want)
		}
	}
}

func TestHandlerRejectsConflictingSources(t *testing.T) {
	_, err := metrics.Handler([]metrics.Source{
		{Name: "x_total", Help: "a", Read: func() float64 { return 0 }},
		{Name: "x_total", Help: "a", Read: func() float64 { return 0 }},
	})
	if err == nil {
		t.Fatal("Handler accepted two identical sources")
	}
}
```

**Step 3: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/metrics/..."`
Expected: FAIL biên dịch: package `metrics` chưa có file không phải test.

**Step 4: Code metrics**

`apps/core/internal/metrics/registry.go`:

```go
package metrics

import (
	"fmt"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	namespace = "chatim"
	subsystem = "core"
)

type Source struct {
	Name   string
	Help   string
	Labels map[string]string
	Gauge  bool
	Read   func() float64
}

func Handler(sources []Source) (http.Handler, error) {
	reg := prometheus.NewRegistry()
	if err := reg.Register(collectors.NewGoCollector()); err != nil {
		return nil, err
	}
	if err := reg.Register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})); err != nil {
		return nil, err
	}
	for _, s := range sources {
		if err := reg.Register(collector(s)); err != nil {
			return nil, fmt.Errorf("register metric %s: %w", s.Name, err)
		}
	}
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{}), nil
}

func collector(s Source) prometheus.Collector {
	if s.Gauge {
		return prometheus.NewGaugeFunc(prometheus.GaugeOpts{Namespace: namespace, Subsystem: subsystem, Name: s.Name, Help: s.Help, ConstLabels: s.Labels}, s.Read)
	}
	return prometheus.NewCounterFunc(prometheus.CounterOpts{Namespace: namespace, Subsystem: subsystem, Name: s.Name, Help: s.Help, ConstLabels: s.Labels}, s.Read)
}
```

**Step 5: Test admin**

`pkg/admin/metrics_route_test.go`:

```go
package admin_test

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"testing"

	"github.com/ivannguyendev/chatim/pkg/admin"
)

func TestMetricsRouteServesOnlyAConfiguredHandler(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("chatim_up 1\n")) })
	cases := map[string]struct {
		handler http.Handler
		code    int
	}{
		"configured": {h, http.StatusOK},
		"absent":     {nil, http.StatusNotFound},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			lis, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			srv := admin.New(admin.Config{Metrics: tc.handler}, slog.New(slog.DiscardHandler))
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- srv.ServeListener(ctx, lis) }()
			t.Cleanup(func() {
				cancel()
				<-done
			})
			client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
			resp, err := client.Get("http://" + lis.Addr().String() + "/metrics")
			if err != nil {
				t.Fatalf("GET /metrics: %v", err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != tc.code {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.code)
			}
		})
	}
}
```

**Step 6: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./pkg/admin/..."`
Expected: FAIL biên dịch: `unknown field Metrics in struct literal of type admin.Config`.

**Step 7: Code admin**

`admin_config.go`: thêm field `Metrics http.Handler` vào `Config` (import `net/http`).

`admin_server.go`, trong `routes` trước `return mux`:

```go
	if s.cfg.Metrics != nil {
		mux.Handle("GET /metrics", s.cfg.Metrics)
	}
```

**Step 8: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/metrics/... ./pkg/admin/..."`
Expected: PASS.

**Step 9: Commit**

```bash
make fmt-check && make vet && make lint
git add go.mod go.sum apps/core/internal/metrics/ pkg/admin/
git commit -m "feat(metrics): serve pull-based prometheus metrics on the admin port"
```

---

### Task 12: Wiring trong `apps/core`

**Files:**
- Create: `apps/core/metrics_wiring.go`
- Create: `apps/core/metrics_wiring_test.go`
- Modify: `apps/core/wiring.go` (`wire`)

**Step 1: Test**

`apps/core/metrics_wiring_test.go` (package `main`): kiểm danh sách nguồn có đủ tên và không trùng, với probes rỗng chỉ ở phần tuỳ chọn (reconciler tắt).

```go
package main

import (
	"strings"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/metrics"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/reconcile"
)

func fakeProbes(withReconciler bool) probes {
	p := probes{
		drops:        func() publish.Drops { return publish.Drops{} },
		router:       func() actor.Stats { return actor.Stats{} },
		cidDegraded:  func() bool { return false },
		markDegraded: func() bool { return false },
		cidDropped:   func() uint64 { return 0 },
		loadShed:     func() int64 { return 0 },
		oplogWindow:  func() float64 { return 0 },
	}
	if withReconciler {
		p.reconcile = func() reconcile.Stats { return reconcile.Stats{} }
	}
	return p
}

func TestCoreMetricSourcesCoverEveryGuarantee(t *testing.T) {
	want := []string{
		"publish_dropped_total", "ack_marks_dropped_total", "redis_degraded", "cid_settle_dropped_total",
		"cid_pending_elsewhere_total", "room_yields_total", "grpc_load_shed_total", "mongo_oplog_window_seconds",
		"reconcile_running", "reconcile_lag_seconds", "reconcile_terms_total", "reconcile_republished_total",
		"reconcile_dropped_total", "reconcile_history_lost_total",
	}
	got := map[string]bool{}
	for _, s := range metricSources(fakeProbes(true)) {
		got[s.Name] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("no metric source %s", name)
		}
	}
	if _, err := metrics.Handler(metricSources(fakeProbes(true))); err != nil {
		t.Fatalf("Handler: %v", err)
	}
	for _, s := range metricSources(fakeProbes(false)) {
		if strings.HasPrefix(s.Name, "reconcile_") {
			t.Errorf("reconciler disabled but %s is exported", s.Name)
		}
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on -run TestCoreMetricSources ./apps/core/"`
Expected: FAIL biên dịch: `undefined: metricSources`, `probes`.

**Step 3: Code**

`apps/core/metrics_wiring.go`: `probes` chỉ giữ hàm đọc (không giữ component), để test không cần dựng hạ tầng.

```go
package main

import (
	"context"
	"math"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/metrics"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/reconcile"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

const oplogProbeTimeout = 2 * time.Second

type probes struct {
	drops        func() publish.Drops
	router       func() actor.Stats
	cidDegraded  func() bool
	markDegraded func() bool
	cidDropped   func() uint64
	loadShed     func() int64
	oplogWindow  func() float64
	reconcile    func() reconcile.Stats
}

func metricSources(p probes) []metrics.Source {
	const dropHelp = "Events dropped before JetStream acked them; the reconciler republishes them."
	const markHelp = "Ack marks not written; the reconciler republishes those events."
	const degradedHelp = "1 while this redis client is degraded."
	out := []metrics.Source{
		{Name: "publish_dropped_total", Help: dropHelp, Labels: map[string]string{"reason": "queue_full"}, Read: func() float64 { return float64(p.drops().QueueFull) }},
		{Name: "publish_dropped_total", Help: dropHelp, Labels: map[string]string{"reason": "malformed"}, Read: func() float64 { return float64(p.drops().Malformed) }},
		{Name: "publish_dropped_total", Help: dropHelp, Labels: map[string]string{"reason": "refused"}, Read: func() float64 { return float64(p.drops().Refused) }},
		{Name: "publish_dropped_total", Help: dropHelp, Labels: map[string]string{"reason": "async_failed"}, Read: func() float64 { return float64(p.drops().AsyncFailed) }},
		{Name: "ack_marks_dropped_total", Help: markHelp, Labels: map[string]string{"reason": "queue_full"}, Read: func() float64 { return float64(p.drops().MarkQueueFull) }},
		{Name: "ack_marks_dropped_total", Help: markHelp, Labels: map[string]string{"reason": "marker_failed"}, Read: func() float64 { return float64(p.drops().MarkFailed) }},
		{Name: "redis_degraded", Help: degradedHelp, Gauge: true, Labels: map[string]string{"client": "cid_dedupe"}, Read: func() float64 { return flag(p.cidDegraded()) }},
		{Name: "redis_degraded", Help: degradedHelp, Gauge: true, Labels: map[string]string{"client": "ack_marks"}, Read: func() float64 { return flag(p.markDegraded()) }},
		{Name: "cid_settle_dropped_total", Help: "Cid commits or aborts dropped by the batcher.", Read: func() float64 { return float64(p.cidDropped()) }},
		{Name: "cid_pending_elsewhere_total", Help: "Sends refused because their cid is pending on another core (CD3 window).", Read: func() float64 { return float64(p.router().CIDElsewhere) }},
		{Name: "room_yields_total", Help: "Rooms an actor gave up after exhausting seq contention retries.", Read: func() float64 { return float64(p.router().Yields) }},
		{Name: "grpc_load_shed_total", Help: "gRPC calls rejected by the in-flight limiter.", Read: func() float64 { return float64(p.loadShed()) }},
		{Name: "mongo_oplog_window_seconds", Help: "Time span covered by the MongoDB oplog; NaN when unreadable.", Gauge: true, Read: p.oplogWindow},
	}
	if p.reconcile != nil {
		out = append(out, reconcileSources(p.reconcile)...)
	}
	return out
}

func reconcileSources(stats func() reconcile.Stats) []metrics.Source {
	return []metrics.Source{
		{Name: "reconcile_running", Help: "1 while this core runs a reconcile term.", Gauge: true, Read: func() float64 { return flag(stats().Running) }},
		{Name: "reconcile_lag_seconds", Help: "Age of the last change the reconciler handled.", Gauge: true, Read: func() float64 { return stats().Lag.Seconds() }},
		{Name: "reconcile_terms_total", Help: "Reconcile terms started on this core.", Read: func() float64 { return float64(stats().Terms) }},
		{Name: "reconcile_republished_total", Help: "Events the reconciler sent again.", Read: func() float64 { return float64(stats().Republished) }},
		{Name: "reconcile_dropped_total", Help: "Changes that could not become events.", Read: func() float64 { return float64(stats().Dropped) }},
		{Name: "reconcile_history_lost_total", Help: "Times the change feed position fell out of the oplog.", Read: func() float64 { return float64(stats().HistoryLost) }},
	}
}

func oplogWindowSeconds(client *mongo.Client) func() float64 {
	return func() float64 {
		ctx, cancel := context.WithTimeout(context.Background(), oplogProbeTimeout)
		defer cancel()
		window, err := mongostore.OplogWindow(ctx, client)
		if err != nil {
			return math.NaN()
		}
		return window.Seconds()
	}
}

func flag(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
```

`wiring.go`, trong `wire`:
- tách limiter ra biến: `limiter := resilience.NewLimiter(cfg.MaxInflight, cfg.QueueWait)` và dùng `Limiter: limiter` trong `grpcserver.Config`.
- `publish.New(cl.js, cfg.Publish, log, publish.WithAckMarks(marks), publish.WithCounters(cl.pubCounters))`.
- giữ `rec` ở phạm vi hàm (khai báo `var rec *reconcile.Reconciler` trước khối `if cfg.ReconcileEnabled`, gán `rec, err = reconcile.New(...)`).
- thay dòng tạo admin bằng:

```go
	p := probes{
		drops:        cl.pubCounters.Drops,
		router:       router.Stats,
		cidDegraded:  cids.Degraded,
		markDegraded: marks.Degraded,
		cidDropped:   batch.Dropped,
		loadShed:     limiter.Rejected,
		oplogWindow:  oplogWindowSeconds(cl.mongo),
	}
	if rec != nil {
		p.reconcile = rec.Stats
	}
	handler, err := metrics.Handler(metricSources(p))
	if err != nil {
		return nil, fmt.Errorf("wire metrics: %w", err)
	}
	a.admin = admin.New(admin.Config{Addr: cfg.AdminAddr, ShutdownTimeout: config.CloseTimeout, Metrics: handler}, log)
```

Kiểm chữ ký thật trước khi viết: `limiter.Rejected` trả `int64`, `batch.Dropped` trả `uint64` (theo `pkg/resilience/limiter.go:61`, `dedupe/batcher.go:187`). Khác thì sửa kiểu trong `probes` cho khớp, không đổi component.

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/"`
Expected: PASS (itest skip). `wc -l apps/core/wiring.go` < 200.

**Step 5: Kiểm trên compose**

```bash
make infra-up
make image TARGET=apps/core && make core-up
docker run --rm --network chatim_default curlimages/curl:8.11.1 -s http://chatim-core-1:9090/metrics | grep -E '^chatim_core_(reconcile_running|mongo_oplog_window_seconds|publish_dropped_total)'
```

Expected: có dòng `chatim_core_publish_dropped_total{reason="queue_full"} 0`, `chatim_core_mongo_oplog_window_seconds <số dương>`, và đúng một trong hai core có `chatim_core_reconcile_running 1` (kiểm cả `chatim-core-2`).

**Step 6: Commit**

```bash
make fmt-check && make vet && make lint
git add apps/core/
git commit -m "feat(core): wire guarantee detectors into the admin metrics endpoint"
```

---

### Task 13: Luật alert và `make alerts-check`

**Files:**
- Create: `deploy/prometheus/alerts.yml`
- Modify: `Makefile` (biến `PROM_IMAGE`, target `alerts-check`, `.PHONY`)

**Step 1: Luật**

`deploy/prometheus/alerts.yml`:

```yaml
groups:
  - name: chatim-core-guarantees
    rules:
      - alert: ChatimReconcilerAbsent
        expr: sum(chatim_core_reconcile_running) == 0
        for: 2m
        labels:
          severity: critical
          guarantee: RC1
        annotations:
          summary: No core runs the event reconciler; lost events are not republished.
      - alert: ChatimReconcilerLagging
        expr: max(chatim_core_reconcile_lag_seconds) > 120
        for: 5m
        labels:
          severity: warning
          guarantee: RC1
        annotations:
          summary: The reconciler is more than two minutes behind commits.
      - alert: ChatimReconcilerDropping
        expr: sum(increase(chatim_core_reconcile_dropped_total[15m])) > 0
        labels:
          severity: warning
          guarantee: RC1
        annotations:
          summary: Committed changes could not become events.
      - alert: ChatimFeedHistoryLost
        expr: sum(increase(chatim_core_reconcile_history_lost_total[15m])) > 0
        labels:
          severity: critical
          guarantee: RC5
        annotations:
          summary: The change feed position fell out of the oplog; events in the gap are lost until a manual resync.
      - alert: ChatimOplogWindowShort
        expr: min(chatim_core_mongo_oplog_window_seconds) < 172800
        for: 15m
        labels:
          severity: warning
          guarantee: RC5
        annotations:
          summary: The oplog covers less than 48h.
      - alert: ChatimOplogWindowCritical
        expr: min(chatim_core_mongo_oplog_window_seconds) < 86400
        for: 15m
        labels:
          severity: critical
          guarantee: RC5
        annotations:
          summary: The oplog covers less than 24h, below the minimum retention.
      - alert: ChatimRepublishSurge
        expr: sum(rate(chatim_core_reconcile_republished_total[5m])) > 100
        for: 10m
        labels:
          severity: warning
          guarantee: RC4
        annotations:
          summary: The reconciler republishes more than 100 events per second.
      - alert: ChatimEventsDropped
        expr: sum(rate(chatim_core_publish_dropped_total[5m])) > 0
        for: 10m
        labels:
          severity: warning
          guarantee: RC1
        annotations:
          summary: The fast path keeps dropping events; delivery waits for the reconciler.
      - alert: ChatimRedisDegraded
        expr: max by (client) (chatim_core_redis_degraded) == 1
        for: 1m
        labels:
          severity: warning
          guarantee: CD2
        annotations:
          summary: A dedupe redis client is degraded; cross-core duplicates are possible.
      - alert: ChatimCIDSettleDropped
        expr: sum(increase(chatim_core_cid_settle_dropped_total[5m])) > 0
        labels:
          severity: warning
          guarantee: CD1
        annotations:
          summary: Cid commits were dropped; retries may wait for the pending window.
      - alert: ChatimCIDPendingElsewhere
        expr: sum(increase(chatim_core_cid_pending_elsewhere_total[5m])) > 0
        labels:
          severity: info
          guarantee: CD3
        annotations:
          summary: Sends hit a cid pending on another core, the CD3 duplicate window.
      - alert: ChatimSeqContention
        expr: sum(increase(chatim_core_room_yields_total[5m])) > 10
        labels:
          severity: warning
          guarantee: P1
        annotations:
          summary: Actors keep yielding rooms after seq contention; check slot ownership.
```

**Step 2: Makefile**

Thêm dưới `BUF_IMAGE`: `PROM_IMAGE ?= prom/prometheus:v3.5.0`. Thêm `alerts-check` vào `.PHONY` và target:

```make
alerts-check:
	docker run --rm -v "$(CURDIR)/deploy/prometheus":/rules:ro --entrypoint promtool $(PROM_IMAGE) check rules /rules/alerts.yml
```

**Step 3: Chạy**

Run: `make alerts-check`
Expected: `Checking /rules/alerts.yml` và `SUCCESS: 12 rules found`. Nếu tag image không tồn tại: dừng và báo (chọn tag v3 có thật rồi ghi vào commit).

**Step 4: Commit**

```bash
git add deploy/prometheus/alerts.yml Makefile
git commit -m "feat(deploy): add alert rules for every guarantee and a promtool check"
```

---

### Task 14: Docs

**Files:**
- Modify: `docs/designs/261005-chatim-architecture.md`
- Modify: `docs/roadmap.md`
- Modify: `README.md`
- Modify: `INDEXES.csv`
- Modify: `CLAUDE.md`

**Step 1: Thiết kế**
- §8.1: bỏ đoạn "Lỗi cần sửa (M2b.0)"; ghi publisher chỉ mark event có chính sách ack mark (`markKey`), boot kiểm `RECONCILE_DELAY > PUB_ACK_TIMEOUT + 10ms + 1s`, mặc định 5s. Phần delay theo từng effect vẫn ở M2b.1.
- §6.1: đoạn "Khi va doc của core khác" đổi nhãn [Đã xây]; ghi backoff `Jitter(5ms → 200ms)` và nhường room qua `a.retire`.
- §9.2: đổi nhãn [Đã xây] cho permission hook (`access.Policy`, `access.Checker`, `SendMessage` và `GetHistory`) và reader pipeline (`view.Pipeline`, bước `CollapseRetried`); ẩn/mặt nạ xoá vẫn [Chưa xây] (M2b.2).
- §12: thêm cột tên metric cho từng dòng (theo Task 12) và ghi luật alert ở `deploy/prometheus/alerts.yml`; dòng CD1 đổi detector thành `cid_settle_dropped_total` + `redis_degraded`.
- §17.2: thêm D78: "Detector là bộ đếm atomic + accessor trong component; package `metrics` đăng ký theo kiểu pull vào Prometheus registry riêng, `GET /metrics` trên cổng admin; luật alert trong repo, kiểm bằng `promtool`. Phương án bị loại: chỉ log; expvar; OTel metrics ngay. Lý do: owner chọn 2026-10-05; Prometheus đã nằm trong kế hoạch M5, pull không đổi đường nóng."

**Step 2: Roadmap:** dòng M2b.0 → `✅ dev-done` kèm link plan; dòng tiếp theo M2b.1 → `⏭ Tiếp theo — cần plan`.

**Step 3: README:** thêm `/metrics` vào dòng cổng admin; dòng env `RECONCILE_DELAY` đã sửa ở Task 2; thêm `make alerts-check` vào danh sách lệnh.

**Step 4: INDEXES.csv:** thêm dòng cho `apps/core/internal/access`, `apps/core/internal/view`, `apps/core/internal/metrics`, `deploy/prometheus/alerts.yml`, `docs/plans/2026-10-05-m2b0-mechanism-foundations.md`; cập nhật key symbols của `actor` (`Stats`, `WithPolicy`), `publish` (`Counters`, `MarkDeadline`), `reconcile` (`Stats`), `redisguard` (`Degraded`), `mongostore` (`OplogWindow`), `pkg/admin` (`Config.Metrics`). Kiểm 7 cột: `python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"` → Expected: `{7}`.

**Step 5: CLAUDE.md:** mục Project: M2b.0 xong, tiếp theo M2b.1. Mục Architecture: thêm một đoạn ngắn "Permission hook và reader pipeline" (`access`, `view`) và "Detectors" (`/metrics` trên admin, `deploy/prometheus/alerts.yml`, `make alerts-check`); mục Commands thêm `make alerts-check`.

**Step 6: Commit**

```bash
git add docs/ README.md INDEXES.csv CLAUDE.md
git commit -m "docs: record M2b.0 mechanisms, detectors and decision D78"
```

---

### Task 15: Kiểm chứng cuối milestone

**Step 1:** `make fmt-check && make vet && make lint && make vuln` → Expected: sạch, `No vulnerabilities found.`

**Step 2:** `make test` → Expected: mọi package `ok`.

**Step 3:** `make infra-up && make itest` → Expected: mọi package `ok`, gồm `TestOplogWindowReadsTheReplicaSetLog` và `TestRealInfraReconcilerPublishesWritesThatSkippedTheCore` (delay 2s).

**Step 4:** `make image TARGET=apps/core && make core-up && make e2e` → Expected: e2e qua (không mất, không trùng sau khi kill core-1).

**Step 5:** Kiểm `/metrics` trên cả hai core như Task 12 Step 5. Expected: đúng một core có `chatim_core_reconcile_running 1`; `chatim_core_room_yields_total` và `chatim_core_cid_pending_elsewhere_total` có mặt.

**Step 6:** `make alerts-check` → Expected: `SUCCESS: 12 rules found`.

**Step 7:** M2b.0 không phải milestone perf: không chạy corebench sweep. Ghi kết quả các bước trên vào PR (mức sẵn sàng `dev-done`).

---

## Ghi chú cho người thực thi

- Task 3 là chỗ dễ sai nhất: mọi lần đóng `a.retire` phải giữ `r.mu` (dùng `requestRetire`). Actor nhường room bằng cách đóng `a.retire` **trước** khi lệnh hết lượt nhận lỗi, nên lệnh mới tới actor đang nghỉ nhận `errRetired`; test phải `synctest.Wait()` trước khi gửi tiếp.
- Pause trong `afterContention` dùng `ctx` của vòng `run`, nên dừng core không bị chặn bởi pause.
- Không đổi hành vi slot manager: Task 3 không chạm `apps/core/internal/slot`, nên không cần R5.
- `mongo_oplog_window_seconds` đọc `local.oplog.rs` mỗi lần scrape (2 lệnh `findOne`, timeout 2s). User Mongo của compose là root nên có quyền; prod cần quyền đọc `local`.
- Số đo dev không phải tiêu chí ở milestone này; tiêu chí là test và detector có mặt.
