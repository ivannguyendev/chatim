# M2a.3 — Perf đường ghi — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: dùng `subagent-driven-development` (cùng phiên) hoặc `separate-driven-development` (phiên riêng) để thực thi plan này theo từng task.

**Goal:** Bớt lượt Redis trên đường ack và bớt CPU của core. Cụ thể:
- gom lệnh chống trùng cid giữa các room (P2);
- trả ack trước khi Commit (P3);
- chặn vòng mở lại kết nối go-redis (C1–C3);
- thử `GOGC` (P4).

Mọi thay đổi đều có số đo trước và sau.

**Architecture:**
- `dedupe.Batcher` bọc `dedupe.Store` và có cùng interface `actor.CIDRegistry`, nên actor gần như không đổi.
  - **Reserve:** mỗi shard (theo slot của room) có một goroutine. Goroutine gửi ngay khi không có lệnh đang bay; trong lúc chờ Redis trả lời, các yêu cầu mới xếp hàng và đi chung lượt sau, tối đa `MaxKeys` key mỗi lượt (gom động).
  - **Commit và Abort:** chỉ đưa vào hàng đợi rồi trả về ngay. Một goroutine riêng mỗi shard gom và gửi đi.
- Actor đổi thứ tự trong `conclude`: trả ack (`cidCache.commit`), enqueue event, rồi mới đưa Commit vào batcher.
- Client Redis dedupe: pool nhỏ cỡ số goroutine của batcher, có `MinIdleConns`, `MaxActiveConns`, `DisableIdentity`.
- Lifecycle: batcher chạy trước router, dừng ngay sau router (gửi hết Commit đang chờ) và trước flusher.

**Tech Stack:** Go 1.26 qua `make`; go-redis v9.22; miniredis; `testing/synctest`; goleak.

**Nguồn quyết định:** `.claude/plans/m2a3-write-path-perf_design.md` (P1–P5, C1–C4, kết quả điều tra, quy trình đo).

**Nhánh:** `feat/m2a3-write-path-perf`, tách từ `feat/m2a2-event-reconcile`. Owner sẽ merge M2a.2 và M2a.3 cùng lúc khi M2a xong.

---

## Quy tắc chung

- Không chạy `go` trên host; dùng `make -s go ARGS="test -race -shuffle=on ./<pkg>/..."`.
- Không comment trong code/test. File < 200 dòng. `make fmt-check` sạch.
- Trước mỗi commit: `make fmt-check`, `make vet`, `make lint` + test các package đã đụng; goroutine mới thì thêm `-count=5` cho package đó.
- Commit Conventional Commits, không nhắc AI, không có dòng `Co-Authored-By`. Chỉ stage đúng các file của task.
- Kết quả khác "Expected" → dừng, báo cáo kèm output.
- Task 2, 3 (write path, dedupe, lifecycle): mỗi task 1 reviewer, một lượt. Task 1, 4, 5, 6 do controller kiểm.
- **Quy trình đo** (từ điều tra 2026-10-04): máy dev bị giảm xung vì nhiệt ở 5K tin/s. Quét tải 1K/3K/5K để tìm ngưỡng; chỉ so trước/sau ở các mức máy không bị giảm xung.
  - Mỗi ô: nghỉ 5 phút, `infra-reset`, `core-up`, corebench 60s.
  - Chạy kèm bộ đo theo giây: `scripts/bench-sample.sh`, thêm ở Task 1.
  - Ghi `CPU_Speed_Limit` cho mỗi ô; ô nào xuống dưới 100 thì ghi chú, không dùng để so.
  - Ở 5K tin/s chỉ kiểm không bỏ lượt và không lỗi.

## Thứ tự task

| # | Task | Ghi chú |
|---|---|---|
| 1 | Bộ đo vào repo + số đo nền (trước khi đổi code) | controller chạy |
| 2 | `dedupe.Batcher` (P2) | reviewer |
| 3 | Actor ack trước Commit (P3) + wiring batcher + client Redis (C1–C3) | reviewer |
| 4 | Đo sau + thử `GOGC=200` (P4) | controller chạy |
| 5 | Docs | |
| 6 | DoD toàn M2a (M2a.2 + M2a.3), mô tả hai PR | |

---

### Task 1: Bộ đo vào repo và số đo nền

**Files:**
- Create: `scripts/bench-sample.sh` (bộ đo theo giây; dựa trên `sample.sh`/`stop.sh` đã dùng khi điều tra)
- Create: `scripts/bench-cell.sh` (một ô đo: reset tuỳ chọn, latency monitor Redis, profiler Mongo, corebench, thu log)
- Phân tích số liệu (ghép theo giây) làm ngoài repo; repo chỉ giữ hai script shell để người khác đo lại được.

`scripts/bench-sample.sh DIR start|stop`:
- `start`, chạy nền và ghi PID vào `DIR/pids`:
  - `/proc/stat` của VM qua container `alpine`, mỗi giây;
  - `docker stats` mỗi giây;
  - `pmset -g therm` mỗi 2 giây, chỉ trên macOS;
  - Redis dedupe `INFO stats` mỗi giây, qua `docker exec` với `REDIS_SECRET_FILE` như target `redis-cli` trong Makefile;
  - Mongo `serverStatus().wiredTiger.checkpoint` và `cache` mỗi giây, qua `mongosh`, xác thực bằng `/run/secrets/mongo_password` trong `--eval`.
- `stop`: dừng mọi vòng lặp; trong container dùng `pkill -f "[w]hile :"` và `pkill -f "[s]erverStatus"` để tránh tự giết chính lệnh `pkill`. Lưu log của container VM.
- Không in password ra argv hay log.

`scripts/bench-cell.sh NAME RATE [reset]`:
- Có `reset` thì nghỉ 300s, rồi `make infra-reset infra-up core-up`.
- Redis dedupe: `latency-monitor-threshold 5`, `slowlog-log-slower-than 5000`, reset cả hai.
- Mongo: profiler `slowms 20` trên DB `chatim`.
- Chạy `bench-sample.sh start`, `make poc TOOL=corebench ARGS="-rate RATE -duration 60s -watch 20"`, rồi `stop`.
- Thu thập: `LATENCY HISTORY command`, `SLOWLOG GET 256`, `system.profile`, log core (degraded/rpc finished/reconcile).
- Ghi tất cả vào `bin/bench/NAME/` (bin/ đã gitignored).

`bench-cell.sh` còn chụp `admin.runCommand({top: 1})` và `serverStatus().opcounters` trước và sau mỗi ô, để biết mongod tốn thời gian ở namespace và loại lệnh nào. Compose cho phép đặt `RECONCILE_ENABLED` và `CORE_GOGC` khi `core-up` (`x-core-env`).

**Mục tiêu đo: tìm ngưỡng cần can thiệp**, tức mức tải đầu tiên xuất hiện một trong các dấu hiệu sau:
- `shed_by_client > 0` hoặc `failed > 0`;
- có đợt Redis dedupe suy giảm;
- ack p99 > 100ms (A1 30ms chỉ xét trên prod-like);
- VM bận trung bình > 70% (`/proc/stat`), hoặc một thành phần dùng gần trọn phần CPU của nó;
- `CPU_Speed_Limit` < 100 → ghi là giới hạn máy dev, không phải của hệ thống.

Quét tải trên HEAD hiện tại (code M2a.2), mỗi ô có `reset`:
- `base-1k`: 1000 tin/s;
- `base-3k`: 3000 tin/s, kèm `pprof`;
- `base-5k`: 5000 tin/s;
- `base-3k-norec`: 3000 tin/s, `RECONCILE_ENABLED=false`, để đo chi phí change stream trên mongod và core-1.
- Ô 2.5K của lần điều tra 2026-10-04 dùng làm điểm thêm.

Mỗi ô ghi lại:
- ack p50/p95/p99/p99.9, live lag p99, pacer lag p99;
- tỉ trọng CPU từng thành phần (`docker stats` của OrbStack đếm dư, nên tổng VM lấy từ `/proc/stat`), CPU core trên mỗi 1K tin;
- lệnh Redis dedupe trên mỗi tin, tổng kết nối mới;
- `top` của mongod theo namespace/lệnh;
- số đợt suy giảm, `CPU_Speed_Limit` thấp nhất.

Commit:

```bash
git add scripts/bench-sample.sh scripts/bench-cell.sh
git commit -m "chore(scripts): add per-second bench sampling for corebench runs"
```

(Kết quả số nền ghi vào docs ở Task 5, cùng số sau.)

---

### Task 2: `dedupe.Batcher` (P2)

**Files:**
- Create: `apps/core/internal/dedupe/batch_config.go`
- Create: `apps/core/internal/dedupe/batcher.go`
- Create: `apps/core/internal/dedupe/batcher_test.go`

**Hành vi (để reviewer đối chiếu):**
1. `Reserve(ctx, keys)` chặn người gọi tới khi có kết quả, `ctx` hết hạn, hoặc batcher đóng (`ErrBatcherClosed`). Kết quả giống hệt `Store.Reserve` cho đúng các key của người gọi, theo thứ tự.
2. Mỗi shard (theo `slotmap.Of(room) % Shards`) có một goroutine Reserve. Lượt sau gom mọi yêu cầu đang xếp hàng, tối đa `MaxKeys` key, thành **một** `Store.Reserve`. Một yêu cầu đơn lẻ có nhiều hơn `MaxKeys` key vẫn đi nguyên, không bị cắt.
3. Lỗi của một lượt gộp (gồm `ErrDegraded`) trả cho mọi người gọi trong lượt đó.
4. `Commit(ctx, entries)` và `Abort(ctx, keys)` không chặn: đưa vào hàng đợi rồi trả `nil`. Hàng đợi đầy thì bỏ, đếm vào `Dropped()`, và log tối đa 1 lần cho mỗi đợt đầy (giống publisher). Mỗi shard có một goroutine settle gom Commit và Abort đang chờ, tối đa `MaxKeys`, thành một `Store.Commit` và một `Store.Abort`.
5. `Close(ctx)`:
   - không nhận yêu cầu mới;
   - gửi hết Commit/Abort đang chờ;
   - trả `ErrBatcherClosed` cho các Reserve còn trong hàng;
   - chờ goroutine kết thúc. Hết `ctx` thì bỏ phần còn lại và trả `ctx.Err()`.
   `Run` trả về sau khi đóng xong.
6. Lời gọi Store dùng `context.Background()`; timeout do `redisguard` của Store áp, mỗi lượt một lần.

**Step 1: Test hỏng** — `batcher_test.go` (package `dedupe`, cùng harness/goleak hiện có), dùng một `registry` giả trong test, có hook chặn/thả từng lời gọi, cùng miniredis cho một test end-to-end:
- `TestReserveCoalescesWaitingCallsIntoOneRound`: chặn lời gọi Reserve đầu tiên tới store. Gửi thêm 10 Reserve từ 10 room cùng shard. Thả ra. Store phải nhận đúng 2 lời gọi; lời gọi thứ hai chứa 10 key. Mỗi người gọi nhận đúng verdict của mình.
- `TestReserveRoundsRespectMaxKeys`: `MaxKeys=4`, 10 yêu cầu 1 key đang chờ → các lượt có ≤4 key.
- `TestReserveErrorReachesEveryCallerInTheRound`.
- `TestReserveReturnsWhenTheCallerGivesUp`: store bị chặn, `ctx` của người gọi hết hạn → nhận `ctx.Err()`, batcher vẫn chạy tiếp.
- `TestCommitAndAbortDoNotWaitForRedis`: store bị chặn; `Commit` và `Abort` trả về ngay. Sau khi thả, store nhận đủ entries và keys.
- `TestCloseFlushesQueuedCommits`: đưa Commit vào hàng khi store bị chặn, gọi `Close`, rồi thả → store nhận đủ; `Close` trả `nil`.
- `TestCloseAnswersQueuedReservesAndRejectsNewCalls`: Reserve sau `Close` → `ErrBatcherClosed`.
- `TestFullSettleQueueDropsAndLogsOnce`.
- `TestBatchedReserveMatchesTheStoreOnRedis`: miniredis + `Store` thật. Reserve, Commit, rồi Reserve lại cùng key, qua batcher → `Reserved`, rồi `Committed` với đúng record. Một core khác giữ pending → `PendingElsewhere`.
- Tất cả chạy trong `synctest.Test` nếu không đụng miniredis; test miniredis dùng thời gian thật nhưng chờ bằng tín hiệu, không poll.

Run: `make -s go ARGS="test ./apps/core/internal/dedupe/"` → FAIL (`undefined: NewBatcher`).

**Step 2: `batch_config.go`**

```go
package dedupe

import (
	"cmp"
	"fmt"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const (
	DefaultBatchShards  = 4
	DefaultBatchMaxKeys = 256
	DefaultBatchQueue   = 4096
)

type BatchConfig struct {
	Shards  int
	MaxKeys int
	Queue   int
}

func (c BatchConfig) Validate() error { return c.withDefaults().validate() }

func (c BatchConfig) withDefaults() BatchConfig {
	c.Shards = cmp.Or(c.Shards, DefaultBatchShards)
	c.MaxKeys = cmp.Or(c.MaxKeys, DefaultBatchMaxKeys)
	c.Queue = cmp.Or(c.Queue, DefaultBatchQueue)
	return c
}

func (c BatchConfig) validate() error {
	switch {
	case c.Shards <= 0 || c.MaxKeys <= 0 || c.Queue <= 0:
		return fmt.Errorf("%w: cid batch config %+v must be positive", apperr.ErrInvalidArgument, c)
	case c.Shards > slotmap.Count:
		return fmt.Errorf("%w: cid batch shards %d exceed %d slots", apperr.ErrInvalidArgument, c.Shards, slotmap.Count)
	default:
		return nil
	}
}

func (c BatchConfig) Connections() int { return 2 * c.withDefaults().Shards }
```

**Step 3: `batcher.go`** — gợi ý cấu trúc (tách file nếu vượt 200 dòng, ví dụ `batcher_rounds.go` cho hai vòng lặp):

```go
package dedupe

type registry interface {
	Reserve(ctx context.Context, keys []Key) ([]Verdict, error)
	Commit(ctx context.Context, entries []Entry) error
	Abort(ctx context.Context, keys []Key) error
}

var (
	ErrBatcherClosed  = fmt.Errorf("cid batcher closed: %w", apperr.ErrUnavailable)
	errBatcherStarted = errors.New("cid batcher already started")
)

type reserveCall struct {
	keys []Key
	out  chan reserveResult
}

type reserveResult struct {
	verdicts []Verdict
	err      error
}

type settleCall struct {
	commits []Entry
	aborts  []Key
}

type batchShard struct {
	reserves chan reserveCall
	settles  chan settleCall
	full     atomic.Bool
}

type Batcher struct {
	store   registry
	cfg     BatchConfig
	log     *slog.Logger
	shards  []*batchShard
	mu      sync.RWMutex
	closed  bool
	started atomic.Bool
	dropped atomic.Uint64
	abort   chan struct{}
	halt    sync.Once
	done    chan struct{}
}
```

- `NewBatcher(store registry, cfg BatchConfig, log *slog.Logger) (*Batcher, error)` — store nil → `ErrInvalidArgument`.
- `Reserve`: chọn shard theo `keys[0].Room` (mọi key của một lời gọi cùng room). Giữ `mu.RLock` trong lúc gửi vào `reserves`, `select` thêm `ctx.Done()`, rồi chờ `out` hoặc `ctx.Done()`. `out` có buffer 1 để shard không bị chặn khi người gọi đã bỏ đi.
- `Commit`/`Abort`: `RLock`; nếu đã đóng thì trả `nil`. Gửi không chặn vào `settles`; đầy thì `dropped.Add(n)`, và nếu `full.CompareAndSwap(false, true)` thì log Warn `"cid settle queue full; dropping commits"` (gửi thành công thì `full.Store(false)`).
- `Run(ctx)`: mỗi shard chạy `reserveLoop` và `settleLoop` (`wg.Go`), rồi `wg.Wait()`, `close(done)`.
- `reserveLoop`: nhận lời gọi đầu tiên (hoặc channel đóng hoặc `abort` thì thoát; khi thoát vì đóng, trả `ErrBatcherClosed` cho phần còn lại trong channel). Gom thêm không chặn tới `MaxKeys`. Gọi `store.Reserve(context.Background(), allKeys)`, rồi chia kết quả theo độ dài `keys` của từng lời gọi. Lỗi hoặc độ dài không khớp → mọi lời gọi nhận lỗi.
- `settleLoop`: tương tự, gom `commits` và `aborts`, gọi `store.Commit` nếu có commits và `store.Abort` nếu có aborts; lỗi bỏ qua (Store đã log qua redisguard). Thoát khi channel đóng **và đã gửi hết**, hoặc khi `abort`.
- `Close(ctx)`: `Lock`, `closed=true`, đóng mọi channel, `Unlock`. Chưa chạy `Run` thì trả `nil`. Chờ `done` hoặc `ctx` hết → `close(abort)` (qua `halt`), chờ `done`, trả `ctx.Err()`.
- `Dropped() uint64`.

**Step 4: Chạy**

Run: `make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/dedupe/"`
Expected: PASS, goleak sạch.

**Step 5: Commit**

```bash
git add apps/core/internal/dedupe/batch_config.go apps/core/internal/dedupe/batcher*.go
git commit -m "feat(dedupe): batch cid reserve and settle calls across rooms"
```

---

### Task 3: Ack trước Commit (P3), wiring batcher, client Redis (C1–C3)

**Files:**
- Modify: `apps/core/internal/actor/cid_reservations.go:58-89` (`conclude`)
- Test: `apps/core/internal/actor/` (test mới `ack_before_commit_test.go`; `fake_registry_test.go` thêm khả năng chặn Commit)
- Modify: `apps/core/internal/config/{config.go,components.go,validate.go}` + test
- Modify: `apps/core/clients.go` (`redisOptions`), `apps/core/redis_clients_test.go`
- Modify: `apps/core/wiring.go`, `apps/core/lifecycle.go`, `apps/core/shutdown.go`

**Step 1: Test actor hỏng**
- `fakeRegistry` thêm `commitHold chan struct{}`: nếu khác nil, `Commit` chờ channel này (hoặc `ctx`) trước khi ghi nhận.
- `TestAckDoesNotWaitForTheCidCommit`: đặt `commitHold`, gửi một tin. `Send` phải trả ack trước khi thả `commitHold`; sau khi thả, registry ghi nhận đúng entry. Chạy trong `synctest`.
- `TestEventsAreEnqueuedBeforeTheCidCommit`: thứ tự quan sát được là ack, event, rồi Commit.

Run → FAIL (`Send` treo tới khi thả Commit).

**Step 2: Đổi `conclude`**

Thứ tự mới:
1. `for _, l := range a.landed { a.cache.commit(l.e.key, l.ack) }` (trả ack).
2. `a.publishLanded()`.
3. Commit các entry đã landed qua `a.r.cids.Commit(detached, entries)`.
4. Abort các failure cần release qua `a.r.cids.Abort(detached, released)`.
5. `for _, f := range a.failed { a.cache.fail(f.e.key, f.err) }`.
6. Dọn `landed`/`failed` như cũ.

Lưu ý cho test chống trùng giữa các core (`cross_core_dedupe_test.go`, `cid_registry_test.go`): sau P3, Commit có thể tới Redis **sau** ack. Test nào kiểm trạng thái Redis ngay sau `Send` phải chờ bằng tín hiệu (`synctest.Wait()` hoặc chờ fake registry), không được sleep. Nếu một test cũ khẳng định "Commit xong trước ack" thì đó là hành vi cũ đã bỏ (P3): sửa test theo hành vi mới và ghi trong báo cáo.

Run: `make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/actor/"` → PASS.

**Step 3: Config**
- `config.Config` thêm `CIDBatch dedupe.BatchConfig`. Trong `components.go`:
  `CIDBatch = dedupe.BatchConfig{Shards: p.count("CID_BATCH_SHARDS", dedupe.DefaultBatchShards), MaxKeys: p.count("CID_BATCH_MAX_KEYS", dedupe.DefaultBatchMaxKeys), Queue: p.count("CID_BATCH_QUEUE", dedupe.DefaultBatchQueue)}`.
- `componentErrors` thêm `{"CID_BATCH_*", c.CIDBatch.Validate()}`.
- `StopPlan` thêm field `CIDBatch time.Duration = 2 * c.Dedupe.Timeout` và cộng vào `total()`. Sửa `stopPhases` cho khớp thứ tự dừng mới. Mặc định 24.2s < 25s.
- Test: `env_test.go` thêm 3 key; `load_test.go` thêm `CIDBatch` vào hai struct mong đợi (mặc định `{4, 256, 4096}`; override ví dụ `"CID_BATCH_SHARDS": "2", "CID_BATCH_MAX_KEYS": "64", "CID_BATCH_QUEUE": "512"`); `validate_test.go` chỉnh các case ngân sách theo tổng mới, và thêm case `CID_BATCH_SHARDS=2000` báo lỗi.

**Step 4: Client Redis (C1–C3)**

Client hiện để gần hết mặc định của go-redis v9.22: `PoolSize` = 10 × GOMAXPROCS (100 mỗi core), `MaxRetries` 3, `DialerRetries` 5 cách nhau 100ms, `CLIENT SETINFO` mỗi lần mở kết nối.
- **Client dedupe:**
  - `PoolSize = MaxActiveConns = cfg.CIDBatch.Connections() + dedupeSideConns`, với `const dedupeSideConns = 4` (mark của publisher, reconciler, probe, dự phòng);
  - `MinIdleConns = cfg.CIDBatch.Connections()`;
  - `MaxRetries: -1` (C5): thử lại tốn hạn 100ms, và Reserve không idempotent; lần thử lại sau EOF sẽ thấy pending của chính core mình và trả `errCIDUnsettled` giả;
  - `DialerRetries: 1` (C6);
  - `DisableIdentity: true`.
- **Client state (slot manager):** chỉ thêm `DisableIdentity: true`; không đổi pool hay retry, vì đụng lease phải chạy R5.
- **Cách làm:** đổi `redisOptions` thành nhận một struct nhỏ (ví dụ `redisPool{size, minIdle int; noRetry bool}`) để hai lời gọi rõ nghĩa.
- **Test** trong `redis_clients_test.go`: `TestRedisClientsUseSmallWarmPools` kiểm `c.dedupe.Options()` (`PoolSize`, `MaxActiveConns`, `MinIdleConns`, `MaxRetries`, `DialerRetries`, `DisableIdentity`) và `c.slots.Options()` (`PoolSize` 4, `DisableIdentity`, `MaxRetries` giữ mặc định).

**Step 5: Wiring và lifecycle**
- `wire`: sau `dedupe.New`, tạo `batch, err := dedupe.NewBatcher(cids, cfg.CIDBatch, log)`, rồi truyền `batch` cho `actor.NewRouter` thay cho `cids`. `app` thêm field `cidBatch drainer`.
- `lifecycle.go`: `tasks` thêm `cidBatch *task`; khởi động `cidBatch` **trước** `router`; `newSupervisor(ctx, 8)`.
- `shutdown.go`: ngay sau bước `router`, thêm `s.step("cid batcher", plan.CIDBatch, a.cidBatch.Close, t.cidBatch)`; trước `flusher`.
- File nào vượt 200 dòng thì tách theo mẫu `wiring_reconcile.go` nếu đã có.

**Step 6: Chạy**
- `make -s go ARGS="test -race -shuffle=on ./apps/core/..."` → PASS.
- `make fmt-check && make vet && make lint`.
- `make -s itest` (`TestRealInfraStopUnderLoadKeepsEveryAckedMessage` phải qua: sau dừng êm, mọi tin đã ack đều đúng 1 bản và có event).
- `make core-up && make e2e` → PASS (bước gửi lại cid sau khi kill core vẫn nhận "same ack seq").

**Step 7: Commit**

```bash
git add apps/core
git commit -m "perf(core): answer the ack before the cid commit and batch dedupe calls"
```

(Stage chỉ các file của task; không có agent khác đụng `apps/core` lúc này.)

---

### Task 4: Đo sau và thử `GOGC=200` (P4)

- Quét tải trên HEAD mới, cùng các ô như Task 1, mỗi ô có `reset`:
  - `after-1k`, `after-3k` (kèm `pprof`), `after-5k`, `after-3k-norec`;
  - `after-3k-gogc200`: `CORE_GOGC=200 ./scripts/bench-cell.sh after-3k-gogc200 3000 reset`.
- So với nền (Task 1) ở từng mức tải. Chỉ so các ô có `CPU_Speed_Limit` = 100:
  - CPU core trên mỗi 1K tin/s;
  - lệnh Redis dedupe mỗi giây và lệnh trên mỗi tin;
  - kết nối mới trong mỗi run (kỳ vọng ≈ pool, không vọt);
  - ack p50/p95/p99;
  - `dedupe.(*Store).Reserve` + `Commit` cumulative trong pprof.
- `GOGC=200`:
  - Giữ (đặt mặc định `CORE_GOGC` 200 trong compose) nếu CPU core giảm ≥5% so với `after-3k` và RSS core dưới 80% `mem_limit`.
  - Ngược lại để 100 và ghi lý do.
- Ô 5K: `failed=0`, không bỏ lượt (`shed_by_client=0`) nếu `CPU_Speed_Limit` không xuống dưới 60; nếu máy bị giảm xung thì chỉ ghi lại.

Commit: `chore(compose): ...` (chỉ khi đổi mặc định `GOGC`) + kết quả vào docs ở Task 5.

---

### Task 5: Docs

- `docs/poc/README.md`: mục "M2a.3" có bảng nền và sau (các ô 2.5K), ô 5K, `GOGC`, số pprof. Ghi rõ quy trình đo 2.5K và lý do (giảm xung vì nhiệt).
- `docs/designs/260930-chat-core-gateway-design.md`:
  - §5 đường ghi: chống trùng cid gom theo shard; ack trước Commit.
  - Bảng sự cố: core chết giữa ack và Commit.
  - Decision Log (D53–D57 đã dành cho M2b):
    - **D58** gom Reserve/Commit/Abort giữa các room bằng batcher theo shard, gửi kiểu động;
    - **D59** ack trước Commit, kèm hệ quả với CD3;
    - **D60** client Redis pool nhỏ, ấm, `DisableIdentity`; giữ việc bỏ kết nối sau timeout;
    - **D61** `GOGC`, nếu giữ.
- `README.md`: env `CID_BATCH_SHARDS`, `CID_BATCH_MAX_KEYS`, `CID_BATCH_QUEUE`, `CORE_GOGC` (compose); ngân sách dừng mới.
- `docs/roadmap.md`: M2a.3 → ✅ Xong trên dev; mức sẵn sàng (M2a.2 + M2a.3 merge chung).
- `INDEXES.csv`: `dedupe` (Batcher), `actor`, `apps/core`, `config`, scripts mới, plan M2a.3.
- `CLAUDE.md`: Done/Next; Send path bước 3 và 6 (batcher, ack trước Commit); quy trình đo; thứ tự start/stop.
- Decision log `.claude/plans/m2a3-write-path-perf_design.md`: kết quả và quyết định `GOGC`.

Commit: `docs: describe cid dedupe batching and the ack-before-commit path`.

---

### Task 6: DoD toàn M2a và mô tả PR

1. `make fmt-check && make vet && make lint && make test`.
2. `make core-down; make infra-reset; make infra-up; make itest`.
3. `make core-up && make e2e`.
4. Push `feat/m2a3-write-path-perf`.
5. Soạn mô tả PR M2a.3 (base `feat/m2a2-event-reconcile`, hoặc `main` sau khi merge M2a.2). Owner merge M2a.2 rồi M2a.3, bằng merge commit, khi CI `checks` xanh.

---

## Ghi chú cho người thực thi

- Không đụng `apps/core/internal/slot` hay `pkg/slotmap` (chỉ dùng `slotmap.Of`), nên không cần R5.
- P5 (gộp Commit vào script Reserve) không làm ở đây; chỉ xét lại nếu pprof sau vẫn cho thấy Redis tốn nhiều.
- Mark của publisher và lệnh tra mark của reconciler vẫn đi thẳng `eventmark.Store`, không qua batcher (đã tự gom batch).
