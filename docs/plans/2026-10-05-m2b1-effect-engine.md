# M2b.1 — Effect engine — Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: dùng `subagent-driven-development` (cùng phiên) hoặc `separate-driven-development` (phiên riêng) để thực thi plan này theo từng task. Implementer dùng skill `go-lang`.

**Goal:** Thay reconciler một-instance bằng topo (a) của D66: một **reader** trên chủ slot 0 đọc nhật ký commit (`messages` + `rooms` insert) và đẩy **record** nhỏ vào **JetStream work stream** 32 partition; **worker** trên mọi core tiêu thụ partition của slot mình và chạy **effect** theo **registry + chính sách** (D65): `msg_created`, `room_created` (mới), `room_activity` (mới, D69). Thêm fast path `room_created`, công cụ `/app resync` thủ công và itest diễn tập.

**Architecture:**
- `store.Change` mang `Kind` (`MessageInserted`, `RoomInserted`); feed Mongo watch cấp database lọc `messages` + `rooms`, vị trí ở `reconciler_state._id="changes"`.
- Package mới `work`: `Record` (chỉ khoá + `CommittedAt`, 33 byte), id tự nhiên, partition `slot % P`, đảm bảo stream `CHATIM_WORK` (WorkQueue) + P durable consumer, adapter `Queue` bọc `jetstream.Consumer`, fake `worktest`.
- `reconcile` đổi vai thành **reader**: giữ term/window/checkpoint/confirm/leader slot 0/history lost; bỏ delay, mark, dựng event. Vị trí xác nhận = prefix record đã được work stream ack.
- Package mới `effects`: `Workers` (một goroutine mỗi partition, chỉ fetch khi `Owns(p)`), `Registry` (`Kind → []Effect`), effect `msg_created` (delay `RECONCILE_DELAY`, tra ack mark, `Find` tin chưa mark, publish + chờ PubAck), `room_created` (delay `RECONCILE_DELAY`, không mark), `room_activity` (delay 0, bulk `$max` lên `rooms`).
- Fast path `room_created` từ `CreateRoom` qua publisher.
- `/app resync`: quét `rooms` theo `act_bucket`/`created_at` trong khoảng thời gian, scan ngược timeline chính, đẩy record vào work stream có rate limit.

**Tech Stack:** Go 1.26 trong Docker qua `make`; nats.go v1.54 jetstream (pull consumer: `CreateOrUpdateConsumer`, `Fetch`, `Ack`, `NakWithDelay`); mongo-driver v2 (change stream cấp database); buf; `testing/synctest`; goleak; Prometheus client v1.24.1.

**Nguồn quyết định:** [thiết kế](../designs/261005-chatim-architecture.md) §4, §5, §8.3, §12, §13, D51, D52, D65, D66, D69, D76; [roadmap](../roadmap.md) dòng M2b.1; owner chốt 2026-10-05: 32 partition theo chủ slot, room activity chỉ ở worker, ngân sách dừng 28s / grace 33s, resync là subcommand `/app resync`. Quyết định mới ghi ở Task 16: **D79** work stream WorkQueue 32 partition do chủ slot p tiêu thụ; **D80** record chỉ mang khoá, worker đọc doc khi cần; **D81** resync là subcommand đẩy record vào work stream.

---

## Quy tắc chung (đọc trước khi làm task nào)

- **Không chạy `go` trên host.** Test một package: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/<pkg>/..."`. Tra API thư viện: `make -s go ARGS="doc github.com/nats-io/nats.go/jetstream Consumer"`.
- **Không viết comment** trong Go, test, YAML, shell, Makefile, proto mới (proto chỉ có comment nếu buf lint đòi; hiện không đòi).
- File code dưới 200 dòng; `wc -l` sau mỗi lần sửa file lớn. File sát giới hạn: `reconcile/reconciler.go` (176), `apps/core/wiring.go` (138), `publish/publisher.go` (189).
- `gosec`: không chuyển `int` → `uint64`/`uint16` khi chưa chặn biên (G115); bộ đếm theo số phần tử dùng `atomic.Int64`.
- Trước mỗi commit: `make fmt-check`, `make vet`, `make lint` + test các package đã đụng. Goroutine/channel/lock mới: thêm `-count=5` cho package đó.
- Đổi adapter Mongo/NATS hoặc wiring `apps/core`: `make itest` một lần ở cuối task (cần `make infra-up`).
- Commit theo Conventional Commits, không nhắc AI, không có dòng `Co-Authored-By`.
- Nhánh: `feat/m2b` (chung cho cả M2b, merge `main` một lần sau M2b.4). Không push từng task; controller push theo mốc.
- Khi một bước cho kết quả khác "Expected": **dừng lại và báo cáo**, không vá cho tới khi qua. Không sửa test cũ ngoài những chỗ plan ghi rõ.
- Review: Task 4, 7, 8, 9, 12, 14 rủi ro → mỗi task 1 reviewer kiểm spec + chất lượng một lượt. Task khác controller kiểm nhanh. Lỗi Minor ghi vào mục "Kết quả thực thi" cuối plan.

## Bảng tính năng → lớp dữ liệu (quy tắc roadmap)

| Tính năng | Lớp (thiết kế §4) | Fact | Idempotent | Effect + chính sách | Quyền | View | Event (id) | Khuếch đại | Guarantee + detector |
|---|---|---|---|---|---|---|---|---|---|
| `msg_created` qua engine | Fact bất biến (tin) | insert `messages` (đã có) | cid (đã có) | delay `RECONCILE_DELAY`, ack mark có, publish + chờ PubAck | — | — | `{room}-{th}-{seq}` | 1 record + 1 lookup mark mỗi tin; `Find` chỉ cho tin chưa mark | RC1–RC4: `reconcile_lag_seconds`, `reconcile_republished_total{effect}`, `work_failures_total` |
| `room_created` | Fact bất biến (room) | insert `rooms` (đã có) | id room ngẫu nhiên (CreateRoom thử lại 3 lần) | fast path từ CreateRoom; worker delay `RECONCILE_DELAY`, không ack mark | tenant (CreateRoom) | — | `{room}-created` | 1 event + 1 record mỗi room | RC1: như trên |
| Room activity | State có version (projection, D69) | — (suy ra từ insert `messages`) | `$max` không lùi | delay 0, gom theo lô fetch, bulk 1 write/room/lô | — | — | không có event | ≤ 1 write mỗi room mỗi lô (≤ 256 record) | RC5 (chỉ mục resync): `work_failures_total` |
| Reader → work stream | Effect engine (D66, D79) | — | `Nats-Msg-Id` = id record | — | — | — | — | 1 publish mỗi change | RC3, RC5: `reconcile_running`, `reconcile_forwarded_total`, `reconcile_history_lost_total` |
| Resync thủ công | Effect engine (D69, D81) | — | id record | đẩy record, worker chạy effect | vận hành | — | id như trên | rate limit `-rate` record/s | RC5: phục hồi sau `ChatimFeedHistoryLost` |

## Hợp đồng chung (mọi task phải khớp đúng chữ ký này)

### `apps/core/internal/store` (Task 3, 11)

```go
type ChangeKind uint8

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

type Activity struct {
	Room   uint64
	Thread uint64
	Seq    uint64
	At     time.Time
}

type ActiveQuery struct {
	From, To time.Time
	Tenant   string
	After    uint64
	Limit    int
}

type Rooms interface {
	Create(ctx context.Context, r domain.Room, members []domain.Member) error
	Get(ctx context.Context, id uint64) (domain.Room, error)
	Member(ctx context.Context, room uint64, user string) (domain.Member, error)
	TouchActivity(ctx context.Context, acts []Activity) error
	ActiveRooms(ctx context.Context, q ActiveQuery) ([]domain.Room, error)
}
```

- `domain.Room` thêm `LastSeq uint64`, `LastMsgAt time.Time`, `LastChangeAt time.Time` (zero khi chưa có tin).
- `TouchActivity`: mỗi `Activity` → `$max lc = At`, `$max ab = giờ(At)`; nếu `Thread == 0` thêm `$max ls = Seq`, `$max lm = At`. Không bao giờ lùi. Room không tồn tại: bỏ qua (không lỗi).
- `ActiveRooms`: room có `ab >= giờ(From)` (không có cận trên: `ab` là `$max`, chỉ giữ giờ hoạt động cuối, nên room còn bận sau `To` vẫn phải được quét) **hoặc** `ca` trong `[From, To]`; lọc `Tenant` nếu khác rỗng; `_id > After`; sắp theo `_id` tăng; tối đa `Limit` (1..`store.MaxActiveLimit` = 1000, kiểm bằng `ActiveQuery.Validate`). Bootstrap tạo index `{ab: 1}` và `{ca: 1}` trên `rooms`. Thêm `store.HourBucket(t time.Time) int64`.
- Giờ(t) = `t.UTC().Unix() / 3600` (int64). BSON field trên `rooms`: `ls`, `lm`, `lc`, `ab`.

### `apps/core/internal/work` (Task 5, 6)

```go
type Record struct {
	Kind        store.ChangeKind
	Room        uint64
	Thread      uint64
	Seq         uint64
	CommittedAt time.Time
}

const RecordSize = 33

var ErrBadRecord = fmt.Errorf("%w: work record", apperr.ErrInvalidArgument)

func RecordOf(c store.Change) Record
func (r Record) ID() string
func Encode(r Record) []byte
func Decode(b []byte) (Record, error)
func Partition(room uint64, partitions int) int
func Subject(root string, partition int) string
func Message(root string, partitions int, r Record) *nats.Msg

type StreamConfig struct {
	Name        string
	SubjectRoot string
	Partitions  int
	Replicas    int
	MaxAge      time.Duration
	Duplicates  time.Duration
	AckWait     time.Duration
}

func (c StreamConfig) Validate() error
func ConsumerName(partition int) string

type Admin interface {
	CreateOrUpdateStream(ctx context.Context, cfg jetstream.StreamConfig) (jetstream.Stream, error)
	CreateOrUpdateConsumer(ctx context.Context, stream string, cfg jetstream.ConsumerConfig) (jetstream.Consumer, error)
}

func EnsureStream(ctx context.Context, js Admin, c StreamConfig) error

type Delivery interface {
	Record() Record
	Ack() error
	Nak(delay time.Duration) error
}

type Queue interface {
	Fetch(ctx context.Context, max int, wait time.Duration) ([]Delivery, error)
}

func NewQueue(js jetstream.JetStream, stream string, partition int) Queue
```

- `ID()`: `"m:" + pbconv.MessageEventID(room, thread, seq)` cho `MessageInserted`; `"r:" + pbconv.RoomID(room)` cho `RoomInserted`.
- `Encode`: byte 0 = kind; byte 1–8 room, 9–16 thread, 17–24 seq (big-endian); 25–32 `CommittedAt.UnixNano()` (int64 big-endian). `Decode` lỗi `ErrBadRecord` khi sai độ dài hoặc kind lạ.
- `Partition` = `int(slotmap.Of(room)) % partitions`. `Subject(root, p)` = `root + ".p" + strconv.Itoa(p)`. `Message` đặt header `jetstream.MsgIDHeader = r.ID()`.
- Stream: subjects `root + ".>"`, `jetstream.WorkQueuePolicy`, `FileStorage`, `MaxAge`, `Duplicates`, `Replicas`. Consumer `ConsumerName(p)` = `"work-p" + itoa(p)`: `Durable`, `FilterSubject = Subject(root, p)`, `AckPolicy: AckExplicitPolicy`, `AckWait`, `MaxAckPending = 1024`, `DeliverPolicy: DeliverAllPolicy`.
- Package `worktest`: fake trong RAM `worktest.Broker` với `Publish(partition int, r work.Record)`, `Queue(partition int) work.Queue`, `Acked() []work.Record`, `Naked() []work.Record`, `Pending(partition int) int`; Nak trả record về hàng sau `delay` (theo đồng hồ, dùng được trong `synctest`).

### `apps/core/internal/reconcile` — reader (Task 7)

```go
type Deps struct {
	Feed  store.ChangeFeed
	Owner Owner
	JS    publish.JetStream
}

type Config struct {
	SubjectRoot  string
	Partitions   int
	Window       int
	Batch        int
	ConfirmEvery time.Duration
	Drain        time.Duration
	Poll         time.Duration
}

type Stats struct {
	Running     bool
	Terms       uint64
	Forwarded   uint64
	Dropped     uint64
	HistoryLost uint64
}
```

`SubjectRoot` ở đây là gốc subject của work stream (`WORK_SUBJECT_ROOT`, mặc định `work`). Bỏ `Delay`, `DuplicateWindow`, `RoomCache`, `Marks`, `Rooms`, `Lag`.

### `apps/core/internal/effects` (Task 8–11)

```go
type Effect struct {
	Name  string
	Delay time.Duration
	Run   func(ctx context.Context, recs []work.Record) []error
}

type Registry map[store.ChangeKind][]Effect

type Owner interface {
	Owns(slot uint16) bool
}

type Deps struct {
	Queue    func(partition int) work.Queue
	Owner    Owner
	Registry Registry
}

type Config struct {
	Partitions int
	FetchBatch int
	FetchWait  time.Duration
	RetryDelay time.Duration
	Drain      time.Duration
	Poll       time.Duration
}

type Stats struct {
	Processed uint64
	Failed    uint64
	Lag       time.Duration
}

func New(deps Deps, cfg Config, log *slog.Logger) (*Workers, error)
func (w *Workers) Run(ctx context.Context) error
func (w *Workers) Close(ctx context.Context) error
func (w *Workers) Stats() Stats
```

- `Run` của effect nhận các record cùng `Kind` của một lô, trả `[]error` cùng độ dài (nil = xong). Một record chỉ được `Ack` khi mọi effect của nó trả nil; có lỗi thì `Nak(RetryDelay)` và đếm `Failed`.
- Worker chạy effect theo thứ tự trong registry; trước mỗi effect chờ tới `max(CommittedAt) + Delay` của các record trong lô. `Lag` = max theo partition của `now − (CommittedAt cũ nhất + Delay)` lúc chạy, không âm; 0 khi không giữ partition nào.
- Partition p chỉ được fetch khi `Owner.Owns(uint16(p))`; kiểm trước mỗi `Fetch`.
- Effect cụ thể (Task 9–11): `NewMessageCreated(deps, cfg) (*MessageCreated, error)` và `NewRoomCreated(deps, cfg) (*RoomCreated, error)` có `Effect()`, `Republished()`, `Dropped()`; `NewRoomActivity(rooms ActivityWriter) *RoomActivity` chỉ có `Effect()`.

### `apps/core/internal/config` (Task 6, 7, 12)

```go
type Config struct {
	Work            work.StreamConfig
	EffectDelay     time.Duration
	EffectRoomCache int
	Effects         effects.Config
}
```

- Task 6 thêm `Work` (env `WORK_STREAM` = `CHATIM_WORK`, `WORK_SUBJECT_ROOT` = `work`, `WORK_PARTITIONS` = 32, `WORK_MAX_AGE` = 2h, `WORK_DUPLICATES` = 2m; `Replicas` = `EVT_STREAM_REPLICAS`; `AckWait` = `EffectDelay` + 30s) và gọi `work.EnsureStream` trong `prepare()` ngay sau `publish.EnsureStream`.
- Task 7 chuyển `RECONCILE_DELAY` (mặc định 5s) sang `EffectDelay` và `RECONCILE_ROOM_CACHE` (65536) sang `EffectRoomCache`; các luật boot về delay đổi sang `EffectDelay` và áp dụng luôn, không phụ thuộc `RECONCILE_ENABLED` (worker luôn chạy). Thêm luật `WORK_DUPLICATES > RECONCILE_CONFIRM_EVERY + RECONCILE_DRAIN`.
- Task 12 thêm `Effects` (env `WORK_FETCH_BATCH` = 256, `WORK_FETCH_WAIT` = 1s, `WORK_RETRY_DELAY` = 5s, `WORK_DRAIN` = 1s; `Partitions` = `Work.Partitions`; `Poll` = `SLOT_TICK`), bước dừng `workers` và nâng `CORE_SHUTDOWN_BUDGET` mặc định 25s → 28s.
- Mỗi env mới phải có trong `env_test.go` (`envKeys`, `overrides`), `load_test.go` (defaults, overrides) và README.

## Thứ tự task

| # | Task | Rủi ro | Phụ thuộc |
|---|---|---|---|
| 0 | Branch | — | — |
| 1 | Proto + pbconv `room_created` | thấp | — |
| 2 | Publish kind `room_created` + fast path CreateRoom | thấp | 1 |
| 3 | `store.Change.Kind` + memstore log chung + contract feed | trung bình | — |
| 4 | Feed Mongo cấp database + anchor `changes` | **cao** | 3 |
| 5 | Package `work`: record, id, partition, subject | thấp | 3 |
| 6 | Work stream: ensure + consumer adapter + `worktest` + config `Work` | trung bình | 5 |
| 7 | Reader: `reconcile` đẩy record vào work stream (+ sửa tối thiểu config/wiring/metrics để xanh) | **cao** | 3, 5, 6 |
| 8 | Package `effects`: registry + worker loop | **cao** | 7 |
| 9 | Effect `msg_created` | **cao** | 8 |
| 10 | Effect `room_created` | thấp | 1, 8 |
| 11 | Room activity: store + index + effect | trung bình | 3, 8 |
| 12 | Config, lifecycle, ngân sách dừng, compose, wiring | **cao** | 4, 6–11 |
| 13 | Metrics + alert | thấp | 12 |
| 14 | `/app resync` + itest diễn tập | **cao** | 11, 12 |
| 15 | Itest end-to-end | trung bình | 12 |
| 16 | Docs | thấp | tất cả |
| 17 | Kiểm chứng cuối + đo xả backlog dev | — | tất cả |

---

## Ghi chú tích hợp (controller, khi ghép 3 phần)

- Task 3 thêm guard tạm trong `reconcile/term.go` (`acked[i] || c.Kind == store.RoomInserted`) + test `reconcile/room_change_test.go`; Task 7 viết lại reader và **xoá** cả hai.
- Từ Task 7 tới Task 12 không có gì gửi bù event bị rớt ở fast path (reader chỉ đẩy record, worker chưa chạy); alert `ChatimReconcilerLagging`/`ChatimRepublishSurge` không có dữ liệu tới Task 13. Chấp nhận vì nhánh chưa merge và e2e chỉ chạy ở Task 12, 17.
- `TestRealInfraReaderForwardsWritesThatSkippedTheCore` (Task 7): chờ `termStarted` **trước** `createRoom` (xem Task 7), để cả hai record được đẩy ngay và sống ≥ `RECONCILE_DELAY` trước khi worker (từ Task 12) ack; `awaitRecords` gom id đã thấy qua các lần poll. Task 15 thay test này bằng khẳng định live event.
- Effects JetStream client: `WithPublishAsyncTimeout(PUB_ACK_TIMEOUT)` và `WithPublishAsyncMaxPending(WORK_PARTITIONS × WORK_FETCH_BATCH)`; effect publish lên gốc subject event (`EVT_SUBJECT_ROOT`), không phải gốc work.
- Registry: `MessageInserted → [room_activity, msg_created]`, `RoomInserted → [room_created]`.
- Ký hiệu thêm ngoài hợp đồng (cộng thêm, không đổi chữ ký): `work.BadRecordsError`, `work.Default*`, `work.MaxAckPending`, `worktest.ErrSettled`, `effects.Default*`, tên effect, `effects/ports.go`, `store.HourBucket`, `store.MaxActiveLimit`, `ActiveQuery.Validate`, `effects.RoomActivityName`, `effects.ActivityWriter`; `reconcile.Reconciler.Dropped()` bị bỏ (dùng `Stats().Dropped`).
- Write contract thêm kind `monotonic-max` (`Rooms.TouchActivity`); `Rooms.ActiveRooms` là `read`.
- `make alerts-check` sau Task 13: 15 luật.

---

### Task 0: Branch

Toàn bộ M2b dùng chung nhánh `feat/m2b` (đã có trên remote). Plan này đã được commit cùng dòng roadmap/INDEXES trỏ tới nó. Task này chỉ đồng bộ nhánh và chụp một baseline xanh, để lỗi ở các task sau không bị lẫn với lỗi có sẵn.

**Step 1: Đồng bộ nhánh**

```bash
git switch feat/m2b
git pull --ff-only
git branch --show-current
git status --short
```

Expected: `feat/m2b`; `git status --short` rỗng.

**Step 2: Baseline**

Run: `make fmt-check && make vet && make lint && make test`
Expected: tất cả xanh. Có gì đỏ thì dừng và báo cáo, không bắt đầu Task 1.

---

### Task 1: Proto + pbconv `room_created`

`room_created` là event đầu tiên không phải tin nhắn. Thêm payload vào `Event` (field 21, kế `message_created = 20`), và trong `pbconv` thêm id tự nhiên `{room}-created` cùng hàm dựng envelope. Event không có `thread_root`/`seq`; `actor` là người tạo, `ts` là `CreatedAt` của room.

**Files:**
- Modify: `proto/chatim/v1/events.proto`
- Regenerate: `pkg/pb/chatim/v1/events.pb.go` (`make proto`)
- Modify: `apps/core/internal/pbconv/pbconv.go`
- Modify (thêm test): `apps/core/internal/pbconv/room_conversion_test.go`
- Modify: `INDEXES.csv` (dòng `apps/core/internal/pbconv`, `pkg/pb/chatim/v1`)

**Step 1: Proto**

Trong `proto/chatim/v1/events.proto`, thay:

```proto
  oneof payload {
    MessageCreated message_created = 20;
  }
}

message MessageCreated {
  Message message = 1;
}
```

bằng:

```proto
  oneof payload {
    MessageCreated message_created = 20;
    RoomCreated room_created = 21;
  }
}

message MessageCreated {
  Message message = 1;
}

message RoomCreated {
  Room room = 1;
}
```

`Room` đã có trong `core.proto` (đã import).

Run: `make proto && make buf-lint`
Expected: không lỗi; `git status --short` có `M pkg/pb/chatim/v1/events.pb.go` và trong đó có `type Event_RoomCreated struct` và `func (x *Event) GetRoomCreated() *RoomCreated`.

**Step 2: Test pbconv**

Thêm vào cuối `apps/core/internal/pbconv/room_conversion_test.go` (import đã đủ: `math`, `proto`, `timestamppb`, `domain`, `pbconv`, `chatimv1`; `sentAt` có sẵn trong `pbconv_test.go`):

```go
func TestRoomCreatedEventIDIsTheDecimalRoomWithASuffix(t *testing.T) {
	cases := map[uint64]string{
		1:                     "1-created",
		9_007_199_254_740_993: "9007199254740993-created",
		math.MaxInt64:         "9223372036854775807-created",
	}
	for room, want := range cases {
		if got := pbconv.RoomCreatedEventID(room); got != want {
			t.Errorf("RoomCreatedEventID(%d) = %q, want %q", room, got, want)
		}
	}
}

func TestRoomCreatedEnvelope(t *testing.T) {
	room := domain.Room{ID: 9_007_199_254_740_993, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: sentAt, MemberCount: 3}
	want := &chatimv1.Event{
		Id: "9007199254740993-created", Tenant: "acme", RoomId: "9007199254740993", RoomType: chatimv1.RoomType_ROOM_TYPE_GROUP,
		Actor: "alice", Ts: timestamppb.New(sentAt),
		Payload: &chatimv1.Event_RoomCreated{RoomCreated: &chatimv1.RoomCreated{Room: pbconv.Room(room)}},
	}
	got := pbconv.RoomCreated(room)
	if !proto.Equal(got, want) {
		t.Fatalf("RoomCreated = %v, want %v", got, want)
	}
	if got.GetThreadRoot() != 0 || got.GetSeq() != 0 || !got.GetTs().AsTime().Equal(sentAt) {
		t.Fatalf("thread %d seq %d ts %v, want 0, 0 and %v", got.GetThreadRoot(), got.GetSeq(), got.GetTs().AsTime(), sentAt)
	}
}
```

**Step 3: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/pbconv/..."`
Expected: FAIL biên dịch: `undefined: pbconv.RoomCreatedEventID` và `undefined: pbconv.RoomCreated`.

**Step 4: Code**

Trong `apps/core/internal/pbconv/pbconv.go`, ngay sau hàm `MessageEventID` thêm:

```go
func RoomCreatedEventID(room uint64) string { return RoomID(room) + "-created" }
```

và thêm vào cuối file:

```go
func RoomCreated(r domain.Room) *chatimv1.Event {
	return &chatimv1.Event{
		Id:       RoomCreatedEventID(r.ID),
		Tenant:   r.Tenant,
		RoomId:   RoomID(r.ID),
		RoomType: RoomType(r.Type),
		Actor:    r.CreatedBy,
		Ts:       timestamppb.New(r.CreatedAt),
		Payload:  &chatimv1.Event_RoomCreated{RoomCreated: &chatimv1.RoomCreated{Room: Room(r)}},
	}
}
```

`wc -l apps/core/internal/pbconv/pbconv.go` phải ≤ 120.

**Step 5: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/pbconv/..."`
Expected: PASS.

**Step 6: INDEXES.csv**

- Thay cả dòng `apps/core/internal/pbconv,...` bằng:

```
apps/core/internal/pbconv,package,Domain <-> protobuf mapping; RoomID as decimal string; MessageEventID {room}-{thread}-{seq}; RoomCreatedEventID {room}-created,RoomID;MessageEventID;RoomCreatedEventID;Message;MessageCreated;RoomCreated;Room;RoomType;DomainRoomType;MessageKind,apps/core/internal/actor;apps/core/internal/grpcsrv;apps/core/internal/publish,unit,D48
```

- Dòng `pkg/pb/chatim/v1`: thay `CoreServiceServer;CoreServiceClient;Event;MessageCreated;Message;Room` bằng `CoreServiceServer;CoreServiceClient;Event;MessageCreated;RoomCreated;Message;Room`.

**Step 7: Commit**

```bash
make fmt-check && make vet && make lint
git add proto/chatim/v1/events.proto pkg/pb/chatim/v1/events.pb.go apps/core/internal/pbconv/ INDEXES.csv
git commit -m "feat(proto): add the room_created event"
```

---

### Task 2: Publish kind `room_created` + fast path CreateRoom

Publisher phải nhận payload mới (subject `evt.{t}.room.{rid}.room_created`, `Nats-Msg-Id = {room}-created`) và **không** đặt ack mark cho nó (D65: chỉ `msg_created` có chính sách mark). `CreateRoom` đẩy `room_created` vào publisher ngay sau khi `Create` thành công; lỗi `Enqueue` bị bỏ qua vì publisher đã tự log (giới hạn) và đếm `queue_full`, còn worker `room_created` (Task 10) phát lại sau `RECONCILE_DELAY`. `Deps.Events` được phép nil (test cũ không đổi).

`make e2e` đang coi mọi live event là `msg_created` (`EventOf` lấy cid rỗng, `CheckEvents` báo "unexpected seq 0"). Watcher trong `scripts/e2e.sh` bắt đầu sau `create-room` nên thường không thấy fast path, nhưng worker (Task 10) có thể phát lại `room_created` muộn → watcher chỉ ghi event `msg_created`.

**Files:**
- Modify: `apps/core/internal/publish/stream.go` (khối `const`)
- Modify: `apps/core/internal/publish/message.go` (`eventKind`)
- Create: `apps/core/internal/publish/room_created_event_test.go`
- Modify: `tools/corecli/internal/e2e/events.go` (`EventOf`)
- Create: `tools/corecli/internal/e2e/events_test.go`
- Modify: `tools/corecli/internal/e2e/state_test.go` (`TestEventOfReadsTheCreatedMessage`)
- Modify: `tools/corecli/cmd_watch.go` (vòng lặp trong `watch`)
- Modify: `apps/core/internal/grpcsrv/core_service.go` (`Deps`, `Service`, `New`, thêm `EventPublisher`, `noEvents`)
- Modify: `apps/core/internal/grpcsrv/create_room.go` (nhánh `err == nil`)
- Modify: `apps/core/internal/grpcsrv/harness_test.go` (`options`, `newRig`)
- Create: `apps/core/internal/grpcsrv/create_room_event_test.go`
- Modify: `apps/core/wiring.go:107`
- Modify: `INDEXES.csv` (dòng `apps/core/internal/publish`, `apps/core/internal/grpcsrv`, `tools/corecli/internal/e2e`)

Caller đã kiểm bằng grep: `publish.Message` chỉ được gọi ở `reconcile/event.go:26` (không đổi); `grpcsrv.Deps` ở `apps/core/wiring.go:107`, `grpcsrv/harness_test.go:68`, `grpcsrv/caller_identity_test.go:19,29` (hai chỗ sau không cần sửa vì `Events` nil được phép); `e2e.EventOf` ở `tools/corecli/cmd_watch.go:77` và `tools/corecli/internal/e2e/state_test.go:57`.

**Step 1: Test publish**

`apps/core/internal/publish/room_created_event_test.go`:

```go
package publish_test

import (
	"slices"
	"testing"
	"testing/synctest"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

func roomCreated(room uint64) *chatimv1.Event {
	return pbconv.RoomCreated(domain.Room{ID: room, Tenant: tenant, Type: domain.RoomGroup, Name: "team", CreatedBy: "alice", CreatedAt: sentAt, MemberCount: 1})
}

func TestRoomCreatedGoesToItsOwnSubject(t *testing.T) {
	m, err := publish.Message("evt", roomA, roomCreated(roomA))
	if err != nil {
		t.Fatalf("Message(room_created): %v", err)
	}
	if m.Subject != "evt.acme.room.101.room_created" || publishtest.MsgID(m) != "101-created" {
		t.Fatalf("subject %q msg id %q, want evt.acme.room.101.room_created and 101-created", m.Subject, publishtest.MsgID(m))
	}
}

func TestRoomCreatedIsPublishedButNeverMarked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := &recordingMarker{}
		rg := newRig(t, fastSetup, publish.WithAckMarks(m)).start(t)
		if err := rg.Enqueue(roomA, []*chatimv1.Event{roomCreated(roomA)}); err != nil {
			t.Fatalf("Enqueue(room_created): %v", err)
		}
		rg.enqueue(t, roomA, 1)
		closeRig(t, rg)
		if got := storedIDs(rg.js); !slices.Equal(got, []string{"101-created", "101-0-1"}) {
			t.Fatalf("stored %v, want 101-created then 101-0-1", got)
		}
		if got := m.marked(); !slices.Equal(got, []store.MsgKey{{Room: roomA, Seq: 1}}) {
			t.Fatalf("marked %v, want only the message", got)
		}
		if key, ok := publish.MarkKey(roomA, roomCreated(roomA)); ok {
			t.Fatalf("MarkKey(room_created) = %v, true; want no mark", key)
		}
	})
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on -run RoomCreated ./apps/core/internal/publish/..."`
Expected: FAIL: `Message(room_created): event needs an id, a subject-safe tenant and a known payload` và `stored [101-0-1], want 101-created then 101-0-1`.

**Step 3: Code publish**

`stream.go`, thay:

```go
	msgCreated = "msg_created"
)
```

bằng:

```go
	msgCreated  = "msg_created"
	roomCreated = "room_created"
)
```

`message.go`, trong `eventKind` thay:

```go
	case *chatimv1.Event_MessageCreated:
		return msgCreated, true
	default:
```

bằng:

```go
	case *chatimv1.Event_MessageCreated:
		return msgCreated, true
	case *chatimv1.Event_RoomCreated:
		return roomCreated, true
	default:
```

`markKey` (`ack_mark_policy.go`) giữ nguyên: nhánh `default` đã trả `false` cho `room_created`.

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/publish/..."`
Expected: PASS (cả test cũ).

**Step 5: Commit publish**

INDEXES.csv, dòng `apps/core/internal/publish`: thay chuỗi `marks only events whose type has an ack mark policy (msg_created)` bằng `publishes msg_created and room_created; marks only events whose type has an ack mark policy (msg_created; room_created has none)`.

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/publish/ INDEXES.csv
git commit -m "feat(publish): publish room_created events without an ack mark"
```

**Step 6: Test e2e bỏ qua event không phải tin**

`tools/corecli/internal/e2e/events_test.go`:

```go
package e2e_test

import (
	"testing"

	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
	"github.com/ivannguyendev/chatim/tools/corecli/internal/e2e"
)

func TestEventOfKeepsOnlyMessageCreatedEvents(t *testing.T) {
	const subject = "live.e2e.room.42.evt.msg_created"
	created := &chatimv1.Event{
		Id: "42-0-3", RoomId: "42", Seq: 3,
		Payload: &chatimv1.Event_MessageCreated{MessageCreated: &chatimv1.MessageCreated{Message: &chatimv1.Message{Cid: "a-c"}}},
	}
	want := e2e.Event{Room: "42", ID: "42-0-3", Seq: 3, CID: "a-c", Subject: subject}
	if got, ok := e2e.EventOf(subject, created); !ok || got != want {
		t.Fatalf("EventOf(msg_created) = %+v, %v; want %+v, true", got, ok, want)
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

Run: `make -s go ARGS="test -race -shuffle=on ./tools/corecli/..."`
Expected: FAIL biên dịch: `assignment mismatch: 2 variables but e2e.EventOf returns 1 value`.

**Step 7: Code e2e**

`tools/corecli/internal/e2e/events.go`, thay cả hàm `EventOf`:

```go
func EventOf(subject string, ev *chatimv1.Event) Event {
	return Event{
		Room:    ev.GetRoomId(),
		ID:      ev.GetId(),
		Seq:     ev.GetSeq(),
		CID:     ev.GetMessageCreated().GetMessage().GetCid(),
		Subject: subject,
	}
}
```

bằng:

```go
func EventOf(subject string, ev *chatimv1.Event) (Event, bool) {
	created := ev.GetMessageCreated()
	if created == nil {
		return Event{}, false
	}
	return Event{
		Room:    ev.GetRoomId(),
		ID:      ev.GetId(),
		Seq:     ev.GetSeq(),
		CID:     created.GetMessage().GetCid(),
		Subject: subject,
	}, true
}
```

`tools/corecli/cmd_watch.go`, trong `watch` thay:

```go
		if err := enc.Encode(e2e.EventOf(msg.Subject, &ev)); err != nil {
			return fmt.Errorf("write event: %w", err)
		}
```

bằng:

```go
		got, ok := e2e.EventOf(msg.Subject, &ev)
		if !ok {
			continue
		}
		if err := enc.Encode(got); err != nil {
			return fmt.Errorf("write event: %w", err)
		}
```

`tools/corecli/internal/e2e/state_test.go`, trong `TestEventOfReadsTheCreatedMessage` thay:

```go
	got := e2e.EventOf("live.e2e.room.42.evt.msg_created", ev)
```

bằng:

```go
	got, ok := e2e.EventOf("live.e2e.room.42.evt.msg_created", ev)
	if !ok {
		t.Fatal("EventOf skipped a message_created event")
	}
```

**Step 8: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./tools/corecli/..."` và `make -s go ARGS="vet ./tools/corecli/..."`
Expected: PASS; vet sạch.

**Step 9: Commit e2e**

INDEXES.csv, dòng `tools/corecli/internal/e2e`: thay `live events checked by natural id and deduped by seq` bằng `live msg_created events checked by natural id and deduped by seq (EventOf skips other event types)`, và thay key symbols `State;Load;Save;CheckAcks;CheckPage;CheckEvents;MessageEventID;ShareOf` bằng `State;Load;Save;EventOf;CheckAcks;CheckPage;CheckEvents;MessageEventID;ShareOf`.

```bash
make fmt-check && make vet && make lint
git add tools/corecli/ INDEXES.csv
git commit -m "fix(corecli): skip non-message live events in e2e"
```

**Step 10: Test CreateRoom enqueue**

`apps/core/internal/grpcsrv/harness_test.go`:
- struct `options`: thêm field `events  grpcsrv.EventPublisher` (sau `policy  access.Policy`).
- trong `newRig`, thay:

```go
	svc, err := grpcsrv.New(grpcsrv.Deps{Sender: o.sender, Rooms: rg.rooms, Pages: rg.msgs, NewID: o.newID, Now: o.now, Policy: o.policy}, quiet)
```

bằng:

```go
	svc, err := grpcsrv.New(grpcsrv.Deps{Sender: o.sender, Rooms: rg.rooms, Pages: rg.msgs, NewID: o.newID, Now: o.now, Policy: o.policy, Events: o.events}, quiet)
```

`apps/core/internal/grpcsrv/create_room_event_test.go`:

```go
package grpcsrv_test

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
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

func (r *recordingEvents) enqueued() ([]uint64, []*chatimv1.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.rooms), slices.Clone(r.events)
}

func TestCreateRoomEnqueuesRoomCreated(t *testing.T) {
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
	want := pbconv.RoomCreated(stored)
	rooms, got := events.enqueued()
	if !slices.Equal(rooms, []uint64{42}) || len(got) != 1 || !proto.Equal(got[0], want) {
		t.Fatalf("enqueued %v %v, want one %v for room 42", rooms, got, want)
	}
}

func TestCreateRoomSucceedsWhenTheEventIsRefused(t *testing.T) {
	newID, _ := idSequence(7)
	events := &recordingEvents{err: errors.New("publish queue full")}
	rg := newRig(t, options{sender: &fakeSender{}, newID: newID, events: events})
	resp, err := rg.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_DM, Members: []string{"alice", "bob"},
	})
	if err != nil || resp.GetRoom().GetId() != "7" {
		t.Fatalf("CreateRoom with a refused event = %v, %v; want room 7", resp, err)
	}
	if _, err := rg.rooms.Get(t.Context(), 7); err != nil {
		t.Fatalf("Get(7): %v", err)
	}
}

func TestCreateRoomEnqueuesOnlyForTheStoredRoom(t *testing.T) {
	newID, _ := idSequence(1, 2, 3)
	events := &recordingEvents{}
	rg := newRig(t, options{sender: &fakeSender{}, newID: newID, events: events})
	occupy(t, rg.rooms, 1, 2)
	if _, err := rg.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_DM, Members: []string{"alice", "bob"},
	}); err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	_, err := rg.client.CreateRoom(as(t, "acme", "alice"), &chatimv1.CreateRoomRequest{
		Type: chatimv1.RoomType_ROOM_TYPE_GROUP, Members: []string{"alice"},
	})
	expectCode(t, err, codes.InvalidArgument)
	if rooms, _ := events.enqueued(); !slices.Equal(rooms, []uint64{3}) {
		t.Fatalf("enqueued for rooms %v, want only 3", rooms)
	}
}
```

**Step 11: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/grpcsrv/..."`
Expected: FAIL biên dịch: `undefined: grpcsrv.EventPublisher` và `unknown field Events in struct literal of type grpcsrv.Deps`.

**Step 12: Code grpcsrv**

`core_service.go`:
- Ngay sau interface `PageReader` thêm:

```go
type EventPublisher interface {
	Enqueue(room uint64, events []*chatimv1.Event) error
}

type noEvents struct{}

func (noEvents) Enqueue(uint64, []*chatimv1.Event) error { return nil }
```

- struct `Deps`: thêm `Events EventPublisher` sau `Policy access.Policy`.
- struct `Service`: thêm `events EventPublisher` sau `pages  PageReader`.
- trong `New`, sau khối `if d.Now == nil { ... }` thêm:

```go
	if d.Events == nil {
		d.Events = noEvents{}
	}
```

- thay:

```go
	return &Service{sender: d.Sender, rooms: d.Rooms, pages: d.Pages, access: checker, view: view.Default(), newID: d.NewID, now: d.Now, log: log}, nil
```

bằng:

```go
	return &Service{sender: d.Sender, rooms: d.Rooms, pages: d.Pages, events: d.Events, access: checker, view: view.Default(), newID: d.NewID, now: d.Now, log: log}, nil
```

`create_room.go`, thay:

```go
		case err == nil:
			return &chatimv1.CreateRoomResponse{Room: pbconv.Room(room)}, nil
```

bằng:

```go
		case err == nil:
			_ = s.events.Enqueue(room.ID, []*chatimv1.Event{pbconv.RoomCreated(room)})
			return &chatimv1.CreateRoomResponse{Room: pbconv.Room(room)}, nil
```

(cùng mẫu `_ = a.r.events.Enqueue(...)` ở `actor/room_events.go:20`; publisher đã log `publish queue full` một lần mỗi đợt đầy và đếm `queue_full`.)

`apps/core/wiring.go`, thay dòng 107:

```go
	svc, err := grpcsrv.New(grpcsrv.Deps{Sender: router, Rooms: st, Pages: st}, log)
```

bằng:

```go
	svc, err := grpcsrv.New(grpcsrv.Deps{Sender: router, Rooms: st, Pages: st, Events: pub}, log)
```

**Step 13: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/grpcsrv/... ./apps/core/"`
Expected: PASS (cả test CreateRoom cũ, vốn chạy với `Events` nil). `wc -l apps/core/internal/grpcsrv/core_service.go apps/core/internal/grpcsrv/harness_test.go` ≤ 115 và ≤ 176.

**Step 14: Itest (đổi wiring `apps/core`)**

Run: `make infra-up && make itest`
Expected: PASS hết (`TestRealInfraReconcilerPublishesWritesThatSkippedTheCore` lọc theo Msg-Id nên không bị `room_created` làm nhiễu).

**Step 15: Commit**

INDEXES.csv, dòng `apps/core/internal/grpcsrv`: thay `CreateRoom retries id collisions;` bằng `CreateRoom retries id collisions and enqueues room_created on the publisher (fast path; a refused enqueue is not retried here);`, và thay key symbols `Service;New;Deps;Sender;PageReader;TenantHeader;UserHeader` bằng `Service;New;Deps;Sender;PageReader;EventPublisher;TenantHeader;UserHeader`.

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/grpcsrv/ apps/core/wiring.go INDEXES.csv
git commit -m "feat(core): enqueue room_created after CreateRoom"
```

---

### Task 3: `store.Change.Kind` + memstore log chung + contract feed

Nhật ký commit bây giờ chứa hai loại fact: insert `messages` và insert `rooms`. `store.Change` mang `Kind` và `Room` theo hợp đồng. memstore giữ **một** log chung trong `Messages`; `NewFeed(msgs, rooms)` gắn `rooms` vào log đó (lock luôn theo thứ tự `Rooms.mu` → `Messages.mu`, không có chiều ngược lại). `rooms` nil = chỉ tin nhắn (reconcile harness giữ nguyên hành vi tới Task 7). Contract `RunFeed` nhận thêm `store.Rooms` và có case trộn room/tin.

Reconciler hiện tại (trước Task 7) sẽ thấy room change ngay khi feed Mongo watch cấp database (Task 4). Không chặn thì mỗi `CreateRoom` thành một lần "dropping change that cannot become an event" (`r.types.get(0)` → room not found) và `Dropped` tăng. Task này thêm một điều kiện nhỏ: room change được xác nhận như change đã mark, không publish.

**Files:**
- Modify: `apps/core/internal/store/feed.go` (`ChangeKind`, `Change`)
- Create: `apps/core/internal/store/memstore/change_log.go` (dời `logged`, `appendLog`, `logAt`, `logLen` ra khỏi `feed.go`; thêm `appendRoom`, `Rooms.attach`)
- Modify: `apps/core/internal/store/memstore/feed.go` (`NewFeed`, `cursor.Next`, imports)
- Modify: `apps/core/internal/store/memstore/memstore.go` (`insertLocked`)
- Modify: `apps/core/internal/store/memstore/rooms.go` (struct `Rooms`, `Create`)
- Modify: `apps/core/internal/store/memstore/memstore_test.go` (`TestFeedContract`)
- Modify: `apps/core/internal/store/memstore/feed_test.go` (hai lời gọi `NewFeed`, thêm một test)
- Modify: `apps/core/internal/store/storetest/feed_cases.go` (`feedCase`, `RunFeed`, `feedCases`, chữ ký 5 case)
- Create: `apps/core/internal/store/storetest/feed_room_cases.go`
- Modify: `apps/core/internal/store/mongostore/feed.go:103` (gắn `Kind: store.MessageInserted`)
- Modify: `apps/core/internal/store/mongostore/feed_integration_test.go`
- Modify: `apps/core/internal/reconcile/harness_test.go:104`
- Modify: `apps/core/internal/reconcile/term.go` (vòng lặp trong `handle`)
- Create: `apps/core/internal/reconcile/room_change_test.go`
- Modify: `INDEXES.csv` (dòng `store`, `memstore`, `storetest`, `reconcile`)

Caller đã kiểm bằng grep: `memstore.NewFeed` ở `memstore/feed_test.go:13,27`, `memstore/memstore_test.go:20`, `reconcile/harness_test.go:104`; `storetest.RunFeed` ở `memstore/memstore_test.go:18`, `mongostore/feed_integration_test.go:12`; dựng `store.Change{...}` ở `memstore/feed.go:109`, `mongostore/feed.go:103` (và `reconcile/checkpoint_test.go:35` trả `store.Change{}` kèm lỗi, không đổi).

**Step 1: Test contract + memstore**

`storetest/feed_cases.go`:
- thay:

```go
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
```

bằng:

```go
type feedCase struct {
	name string
	run  func(t *testing.T, msgs store.Messages, rooms store.Rooms, feed store.ChangeFeed)
}

func RunFeed(t *testing.T, open func(t *testing.T) (store.Messages, store.Rooms, store.ChangeFeed)) {
	t.Helper()
	for _, c := range feedCases() {
		t.Run(c.name, func(t *testing.T) {
			msgs, rooms, feed := open(t)
			c.run(t, msgs, rooms, feed)
		})
	}
}
```

- trong `feedCases`, sau dòng `{"new inserts come out in commit order with their content", feedOrder},` thêm `{"room inserts come out in commit order with their content", feedRooms},`.
- năm hàm `feedOrder`, `feedStartsAtBootstrap`, `feedResume`, `feedNoRewind`, `feedForget`: chữ ký `(t *testing.T, msgs store.Messages, feed store.ChangeFeed)` đổi thành `(t *testing.T, msgs store.Messages, _ store.Rooms, feed store.ChangeFeed)`; thân giữ nguyên.

`storetest/feed_room_cases.go`:

```go
package storetest

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func feedRooms(t *testing.T, msgs store.Messages, rooms store.Rooms, feed store.ChangeFeed) {
	cur := openCursor(t, feed)
	first, firstMembers := teamOf(roomA)
	second, secondMembers := teamOf(roomB)
	mustCreate(t, rooms, first, firstMembers)
	insertEach(t, msgs, msg(roomA, mainThread, 1))
	mustCreate(t, rooms, second, secondMembers)
	insertEach(t, msgs, msg(roomB, mainThread, 1))
	got := nextChanges(t, cur, 4)
	kinds := []store.ChangeKind{store.RoomInserted, store.MessageInserted, store.RoomInserted, store.MessageInserted}
	for i, c := range got {
		if c.Kind != kinds[i] {
			t.Fatalf("change %d kind = %d, want %d", i, c.Kind, kinds[i])
		}
	}
	assertChangedRoom(t, got[0], first)
	assertChangedRoom(t, got[2], second)
	msgChanges := []store.Change{got[1], got[3]}
	assertMessages(t, messagesOf(msgChanges), []domain.Message{msg(roomA, mainThread, 1), msg(roomB, mainThread, 1)})
	for _, c := range msgChanges {
		if c.Room != (domain.Room{}) {
			t.Fatalf("message change carries room %+v, want none", c.Room)
		}
	}
}

func assertChangedRoom(t *testing.T, c store.Change, want domain.Room) {
	t.Helper()
	got := c.Room
	gotAt, wantAt := got.CreatedAt, want.CreatedAt
	got.CreatedAt, want.CreatedAt = time.Time{}, time.Time{}
	if got != want || !gotAt.Equal(wantAt) || c.Msg != (domain.Message{}) {
		t.Fatalf("room change = %+v at %v with message %+v, want %+v at %v and no message", got, gotAt, c.Msg, want, wantAt)
	}
}
```

`memstore/memstore_test.go`, thay `TestFeedContract`:

```go
func TestFeedContract(t *testing.T) {
	storetest.RunFeed(t, func(*testing.T) (store.Messages, store.Rooms, store.ChangeFeed) {
		msgs, rooms := memstore.NewMessages(), memstore.NewRooms()
		return msgs, rooms, memstore.NewFeed(msgs, rooms)
	})
}
```

`memstore/feed_test.go`: thay `memstore.NewFeed(memstore.NewMessages())` (hai chỗ, dòng 13 và 27) bằng `memstore.NewFeed(memstore.NewMessages(), nil)`; thêm import `"time"` và `"github.com/ivannguyendev/chatim/apps/core/internal/domain"`; thêm test:

```go
func TestFeedWithoutRoomsSeesOnlyMessages(t *testing.T) {
	msgs, rooms := memstore.NewMessages(), memstore.NewRooms()
	cur, err := memstore.NewFeed(msgs, nil).Open(t.Context())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	now := time.Now()
	r := domain.Room{ID: 9, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: now, MemberCount: 1}
	if err := rooms.Create(t.Context(), r, []domain.Member{{Room: 9, Tenant: "acme", User: "alice", Role: domain.RoleOwner, JoinedAt: now}}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	m := domain.Message{Room: 9, Seq: 1, Tenant: "acme", From: "alice", Kind: domain.KindText, Text: "hi", CID: "c-1", CreatedAt: now}
	if res := msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("Insert: %+v", res)
	}
	c, err := cur.Next(t.Context())
	if err != nil || c.Kind != store.MessageInserted || c.Msg.Seq != 1 {
		t.Fatalf("first change = %+v, %v; want the message, the room is not logged", c, err)
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/..."`
Expected: FAIL biên dịch: `undefined: store.ChangeKind`, `undefined: store.RoomInserted`, `too many arguments in call to memstore.NewFeed`.

**Step 3: Code store + memstore**

`store/feed.go`, thay:

```go
type Change struct {
	Msg         domain.Message
	CommittedAt time.Time
	Position    Position
}
```

bằng:

```go
type ChangeKind uint8

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

`memstore/change_log.go`:

```go
package memstore

import (
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type logged struct {
	kind store.ChangeKind
	msg  domain.Message
	room domain.Room
	at   time.Time
}

func (s *Messages) appendLog(l logged) {
	l.at = time.Now()
	s.log = append(s.log, l)
	close(s.grew)
	s.grew = make(chan struct{})
}

func (s *Messages) appendRoom(r domain.Room) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendLog(logged{kind: store.RoomInserted, room: r})
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
```

`memstore/feed.go`:
- xoá toàn bộ khối từ `type logged struct {` tới hết hàm `logLen` (dòng 17–41; đã dời sang `change_log.go`).
- xoá import `"time"` và `"github.com/ivannguyendev/chatim/apps/core/internal/domain"` (không còn dùng).
- thay:

```go
func NewFeed(msgs *Messages) *Feed { return &Feed{msgs: msgs, confirmed: msgs.logLen(), known: true} }
```

bằng:

```go
func NewFeed(msgs *Messages, rooms *Rooms) *Feed {
	if rooms != nil {
		rooms.attach(msgs)
	}
	return &Feed{msgs: msgs, confirmed: msgs.logLen(), known: true}
}
```

- trong `cursor.Next` thay:

```go
			return store.Change{Msg: l.msg, CommittedAt: l.at, Position: store.Position(strconv.Itoa(c.next))}, nil
```

bằng:

```go
			return store.Change{Kind: l.kind, Msg: l.msg, Room: l.room, CommittedAt: l.at, Position: store.Position(strconv.Itoa(c.next))}, nil
```

`memstore/memstore.go`, trong `insertLocked` thay `s.appendLog(m)` bằng `s.appendLog(logged{kind: store.MessageInserted, msg: m})`.

`memstore/rooms.go`:
- struct `Rooms`: thêm field `log     *Messages` sau `members map[memberKey]domain.Member`.
- trong `Create`, ngay trước `return nil` cuối hàm (sau vòng `for _, m := range members { ... }`) thêm:

```go
	if s.log != nil {
		s.log.appendRoom(r)
	}
```

`mongostore/feed.go`, thay dòng 103:

```go
	return store.Change{Msg: m, CommittedAt: ev.WallTime, Position: pos}, nil
```

bằng:

```go
	return store.Change{Kind: store.MessageInserted, Msg: m, CommittedAt: ev.WallTime, Position: pos}, nil
```

`mongostore/feed_integration_test.go`, thay:

```go
	storetest.RunFeed(t, func(t *testing.T) (store.Messages, store.ChangeFeed) {
		s, db := itStore(t, client)
		return s, NewFeed(db)
	})
```

bằng:

```go
	storetest.RunFeed(t, func(t *testing.T) (store.Messages, store.Rooms, store.ChangeFeed) {
		s, db := itStore(t, client)
		return s, s, NewFeed(db)
	})
```

(Case "room inserts ..." của Mongo chỉ qua sau Task 4; không có infra thì test này skip.)

`reconcile/harness_test.go:104`, thay `rg.feed = memstore.NewFeed(rg.msgs)` bằng `rg.feed = memstore.NewFeed(rg.msgs, nil)`.

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/store/..."` rồi `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/reconcile/..."`
Expected: PASS (mongostore integration skip). `-count=5` vì log chung có lock mới giữa `Rooms` và `Messages`. `wc -l apps/core/internal/store/memstore/*.go apps/core/internal/store/storetest/feed_cases.go` mỗi file ≤ 160.

**Step 5: Commit store**

INDEXES.csv:
- dòng `apps/core/internal/store`: thay `ErrFeedHistoryLost/ErrFeedBusy/ErrCorruptChange)` bằng `ErrFeedHistoryLost/ErrFeedBusy/ErrCorruptChange; Change carries Kind MessageInserted with Msg or RoomInserted with Room)`, và trong key symbols thay `Change;Position;` bằng `Change;ChangeKind;MessageInserted;RoomInserted;Position;`.
- dòng `apps/core/internal/store/memstore`: thay `in-memory change feed over the insert log, anchored` bằng `in-memory change feed over one insert log of messages and (when NewFeed is given them) rooms, anchored`.
- dòng `apps/core/internal/store/storetest`: thay `change feed (RunFeed: commit order;` bằng `change feed (RunFeed over messages + rooms + feed: commit order; room inserts in commit order with their content;`.

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/store/ apps/core/internal/reconcile/harness_test.go INDEXES.csv
git commit -m "feat(store): tag feed changes with a kind and log room inserts in memstore"
```

**Step 6: Test reconciler bỏ qua room change**

`apps/core/internal/reconcile/room_change_test.go` (dùng tham số `wrap` của `newRig` để thay feed bằng một feed có gắn rooms; tin vẫn thuộc `room` 4242 có trong rooms của harness):

```go
package reconcile_test

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
)

func TestRoomChangesAreConfirmedWithoutAnEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		msgs, rooms := memstore.NewMessages(), memstore.NewRooms()
		feed := memstore.NewFeed(msgs, rooms)
		rg := newRig(t, func(store.ChangeFeed) store.ChangeFeed { return feed }).start(t)
		synctest.Wait()
		now := time.Now()
		other := domain.Room{ID: 777, Tenant: tenant, Type: domain.RoomGroup, Name: "other", CreatedBy: "alice", CreatedAt: now, MemberCount: 1}
		if err := rooms.Create(t.Context(), other, []domain.Member{{Room: 777, Tenant: tenant, User: "alice", Role: domain.RoleOwner, JoinedAt: now}}); err != nil {
			t.Fatalf("create room: %v", err)
		}
		m := domain.Message{Room: room, Seq: 1, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "hi", CID: "c", CreatedAt: now.UTC()}
		if res := msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
			t.Fatalf("insert: %+v", res)
		}
		time.Sleep(delay + tick)
		synctest.Wait()
		if got := attemptIDs(rg.js); !slices.Equal(got, []string{eventID(1)}) {
			t.Fatalf("attempts = %v, want only %s", got, eventID(1))
		}
		if n, _ := feed.Confirmed(); n != 2 {
			t.Fatalf("confirmed = %d, want 2 (the room change and the message)", n)
		}
		if got := rg.Stats().Dropped; got != 0 {
			t.Fatalf("dropped = %d, want 0 for a room change", got)
		}
	})
}
```

Run: `make -s go ARGS="test -race -shuffle=on -run RoomChanges ./apps/core/internal/reconcile/..."`
Expected: FAIL: `dropped = 1, want 0 for a room change` (room change hiện đi vào nhánh `errUndeliverable`).

**Step 7: Code reconcile**

`reconcile/term.go`, trong `handle` thay:

```go
	for i, c := range batch {
		if acked[i] {
```

bằng:

```go
	for i, c := range batch {
		if acked[i] || c.Kind == store.RoomInserted {
```

(`acked` vẫn tra bit cho khoá rỗng của room change; `GETBIT` trên key room 0 trả 0, vô hại. Task 7 bỏ cả đoạn này khi `reconcile` thành reader.)

**Step 8: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/reconcile/..."`
Expected: PASS (cả test cũ). `wc -l apps/core/internal/reconcile/term.go` vẫn 161.

**Step 9: Commit reconcile**

INDEXES.csv, dòng `apps/core/internal/reconcile`: thay `republishes unmarked messages rebuilt from the doc with the natural id on its own JetStream client;` bằng `republishes unmarked messages rebuilt from the doc with the natural id on its own JetStream client; room inserts from the feed are confirmed without an event;`.

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/reconcile/ INDEXES.csv
git commit -m "fix(reconcile): confirm room changes without republishing them"
```

---

### Task 4: Feed Mongo cấp database + anchor `changes`

Rủi ro cao: đổi nguồn của mọi đảm bảo RC1–RC4. Feed chuyển từ change stream của collection `messages` sang change stream của **database**, lọc `operationType: insert` và `ns.coll ∈ {messages, rooms}`, giải mã theo `ns.coll`. Vị trí lưu ở `reconciler_state._id = "changes"`.

Chuyển đổi từ DB đang chạy (có `_id: "messages"`): Bootstrap chỉ đặt `at` khi `changes` chưa có `at`; giá trị lấy từ `at` của doc `messages` nếu có (vị trí đã xác nhận cuối của reconciler cũ, đọc lại từ đó là an toàn vì id tự nhiên), không thì `$$CLUSTER_TIME`. **Không** chép `token` cũ: resume token của stream cấp collection không dùng được cho stream cấp database; `StartAtOperationTime(at)` đọc lại cả change ở đúng `at` (trùng, vô hại). Một update pipeline không đọc được doc khác, nên làm hai bước (đọc doc cũ → upsert `$ifNull`); cả hai idempotent và không bao giờ đè `at`/`token` đã có. `Forget` xoá cả `changes` lẫn `messages`, để lần bootstrap sau một lần mất lịch sử không kéo vị trí về doc cũ.

Phát hiện mất lịch sử (mã 286 → `ErrFeedHistoryLost`) giữ nguyên.

**Files:**
- Modify: `apps/core/internal/store/mongostore/feed.go` (thay toàn bộ)
- Create: `apps/core/internal/store/mongostore/feed_change.go`
- Create: `apps/core/internal/store/mongostore/feed_change_test.go`
- Modify: `apps/core/internal/store/mongostore/bootstrap.go` (`ensureFeedAnchor`, thêm `feedStart`, imports)
- Modify: `apps/core/internal/store/mongostore/bootstrap_integration_test.go:130` (`feedState`)
- Create: `apps/core/internal/store/mongostore/feed_anchor_integration_test.go`
- Modify: `INDEXES.csv` (dòng `apps/core/internal/store/mongostore`)

Tra API (đã kiểm): `func (db *Database) Watch(ctx, pipeline any, opts ...options.Lister[options.ChangeStreamOptions]) (*ChangeStream, error)` đòi database có read concern majority hoặc không có; `func (db *Database) Client() *Client`; `options.Database().SetReadPreference/SetReadConcern/SetWriteConcern`.

**Step 1: Test**

`apps/core/internal/store/mongostore/feed_change_test.go` (unit, chạy trong `make test`; `sampleMessage`, `codecTime` có sẵn trong `codec_test.go`):

```go
package mongostore

import (
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func changeOn(t *testing.T, coll string, doc any) changeDoc {
	t.Helper()
	raw, err := bson.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return changeDoc{WallTime: codecTime, NS: changeNS{Coll: coll}, FullDocument: raw}
}

func TestDecodeChangeReadsMessagesAndRooms(t *testing.T) {
	m := sampleMessage()
	mdoc, err := encodeMessage(m)
	if err != nil {
		t.Fatalf("encodeMessage: %v", err)
	}
	got, err := decodeChange(changeOn(t, messagesCollection, mdoc))
	if err != nil || got.Kind != store.MessageInserted || store.KeyOf(got.Msg) != store.KeyOf(m) || got.Msg.CID != m.CID ||
		!got.CommittedAt.Equal(codecTime) || got.Room.ID != 0 {
		t.Fatalf("message change = %+v, %v", got, err)
	}
	r := domain.Room{ID: 7_340_000_009, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: codecTime, MemberCount: 2}
	rdoc, err := encodeRoom(r)
	if err != nil {
		t.Fatalf("encodeRoom: %v", err)
	}
	got, err = decodeChange(changeOn(t, roomsCollection, rdoc))
	if err != nil || got.Kind != store.RoomInserted || got.Room.ID != r.ID || got.Room.Name != "Team" || got.Room.MemberCount != 2 ||
		!got.Room.CreatedAt.Equal(codecTime) || got.Msg.Room != 0 {
		t.Fatalf("room change = %+v, %v", got, err)
	}
}

func TestDecodeChangeRejectsOtherCollectionsAndBrokenDocuments(t *testing.T) {
	cases := map[string]changeDoc{
		"members insert":   changeOn(t, membersCollection, bson.D{{Key: "u", Value: "bob"}}),
		"bad message id":   changeOn(t, messagesCollection, bson.D{{Key: "_id", Value: []byte{1, 2}}}),
		"negative room id": changeOn(t, roomsCollection, bson.D{{Key: "_id", Value: int64(-1)}}),
		"no document":      {WallTime: codecTime, NS: changeNS{Coll: messagesCollection}},
	}
	for name, ev := range cases {
		if _, err := decodeChange(ev); !errors.Is(err, errCorrupt) {
			t.Errorf("%s: decodeChange = %v, want errCorrupt", name, err)
		}
	}
}
```

`bootstrap_integration_test.go`, trong `feedState` thay `bson.D{{Key: "_id", Value: messagesFeedID}}` bằng `bson.D{{Key: "_id", Value: changesFeedID}}`.

`apps/core/internal/store/mongostore/feed_anchor_integration_test.go`:

```go
package mongostore

import (
	"context"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func setMessagesPosition(t *testing.T, db *mongo.Database, at bson.Timestamp) {
	t.Helper()
	filter := bson.D{{Key: "_id", Value: legacyMessagesFeedID}}
	update := bson.D{{Key: "$set", Value: bson.D{{Key: "at", Value: at}}}}
	if _, err := db.Collection(reconcilerStateCollection).UpdateOne(t.Context(), filter, update, options.UpdateOne().SetUpsert(true)); err != nil {
		t.Fatalf("set the messages position: %v", err)
	}
}

func TestBootstrapCarriesTheMessagesPositionOverWithoutLosingWrites(t *testing.T) {
	s, db := itStore(t, itClient(t))
	old := feedState(t, db).At
	if _, err := db.Collection(reconcilerStateCollection).DeleteOne(t.Context(), bson.D{{Key: "_id", Value: changesFeedID}}); err != nil {
		t.Fatalf("drop the changes position: %v", err)
	}
	setMessagesPosition(t, db, old)
	m := sampleMessage()
	if res := s.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
		t.Fatalf("Insert = %+v, want inserted", res)
	}
	bootstrapTwice(t, db, feedPosition{At: old})
	setMessagesPosition(t, db, bson.Timestamp{T: old.T + 60, I: 1})
	bootstrapTwice(t, db, feedPosition{At: old})
	cur, err := NewFeed(db).Open(t.Context())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = cur.Close(context.Background()) })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	c, err := cur.Next(ctx)
	if err != nil || c.Kind != store.MessageInserted || c.Msg.CID != m.CID {
		t.Fatalf("first change after the carry-over = %+v, %v; want the message written after the old position", c, err)
	}
}

func TestForgetClearsTheOldAndTheNewPosition(t *testing.T) {
	_, db := itStore(t, itClient(t))
	setMessagesPosition(t, db, bson.Timestamp{T: 1, I: 1})
	if err := NewFeed(db).Forget(t.Context()); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	n, err := db.Collection(reconcilerStateCollection).CountDocuments(t.Context(), bson.D{})
	if err != nil || n != 0 {
		t.Fatalf("reconciler_state holds %d documents after Forget (%v), want none", n, err)
	}
	if err := Bootstrap(t.Context(), db); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if at := feedState(t, db).At; at.T <= 1 {
		t.Fatalf("anchor after Forget = %+v, want a fresh cluster time", at)
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/mongostore/..."`
Expected: FAIL biên dịch: `undefined: changeNS`, `undefined: decodeChange`, `undefined: changesFeedID`, `undefined: legacyMessagesFeedID`, `unknown field NS in struct literal of type changeDoc`.

**Step 3: Code**

`apps/core/internal/store/mongostore/feed_change.go`:

```go
package mongostore

import (
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type changeDoc struct {
	Token        bson.Raw       `bson:"_id"`
	ClusterTime  bson.Timestamp `bson:"clusterTime"`
	WallTime     time.Time      `bson:"wallTime"`
	NS           changeNS       `bson:"ns"`
	FullDocument bson.Raw       `bson:"fullDocument"`
}

type changeNS struct {
	Coll string `bson:"coll"`
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
	default:
		return store.Change{}, fmt.Errorf("%w: change on collection %q", errCorrupt, ev.NS.Coll)
	}
}
```

`apps/core/internal/store/mongostore/feed.go` (thay toàn bộ):

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
	changesFeedID           = "changes"
	legacyMessagesFeedID    = "messages"
	changeStreamHistoryLost = 286
	feedMaxAwait            = time.Second
)

var _ store.ChangeFeed = (*Feed)(nil)

type Feed struct {
	db    *mongo.Database
	state *mongo.Collection
}

type feedPosition struct {
	Token bson.Raw       `bson:"token"`
	At    bson.Timestamp `bson:"at"`
}

func NewFeed(db *mongo.Database) *Feed {
	majority := options.Database().
		SetReadPreference(readpref.Primary()).
		SetReadConcern(readconcern.Majority()).
		SetWriteConcern(writeconcern.Majority())
	watched := db.Client().Database(db.Name(), majority)
	return &Feed{db: watched, state: watched.Collection(reconcilerStateCollection)}
}

func (f *Feed) Open(ctx context.Context) (store.Cursor, error) {
	opts := options.ChangeStream().SetMaxAwaitTime(feedMaxAwait)
	var saved feedPosition
	switch err := f.state.FindOne(ctx, bson.D{{Key: "_id", Value: changesFeedID}}).Decode(&saved); {
	case err == nil && len(saved.Token) > 0:
		opts.SetStartAfter(saved.Token)
	case err == nil && !saved.At.IsZero():
		opts.SetStartAtOperationTime(&saved.At)
	case err != nil && !errors.Is(err, mongo.ErrNoDocuments):
		return nil, fmt.Errorf("load change feed position: %w", err)
	}
	cs, err := f.db.Watch(ctx, feedPipeline(), opts)
	if err != nil {
		return nil, feedError("open change stream", err)
	}
	return &feedCursor{cs: cs, state: f.state}, nil
}

func feedPipeline() mongo.Pipeline {
	match := bson.D{
		{Key: "operationType", Value: "insert"},
		{Key: "ns.coll", Value: bson.D{{Key: "$in", Value: bson.A{messagesCollection, roomsCollection}}}},
	}
	return mongo.Pipeline{{{Key: "$match", Value: match}}}
}

func (f *Feed) Forget(ctx context.Context) error {
	filter := bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: bson.A{changesFeedID, legacyMessagesFeedID}}}}}
	if _, err := f.state.DeleteMany(ctx, filter); err != nil {
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
	change, err := decodeChange(ev)
	if err != nil {
		return store.Change{}, fmt.Errorf("%w: %w", store.ErrCorruptChange, err)
	}
	pos, err := bson.Marshal(feedPosition{Token: ev.Token, At: ev.ClusterTime})
	if err != nil {
		return store.Change{}, fmt.Errorf("%w: encode position: %w", store.ErrCorruptChange, err)
	}
	change.Position = pos
	return change, nil
}

func (c *feedCursor) Confirm(ctx context.Context, pos store.Position) error {
	var p feedPosition
	if err := bson.Unmarshal(pos, &p); err != nil || len(p.Token) == 0 {
		return fmt.Errorf("%w: change feed position", apperr.ErrInvalidArgument)
	}
	filter := bson.D{{Key: "_id", Value: changesFeedID}, {Key: "at", Value: bson.D{{Key: "$lt", Value: p.At}}}}
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

`bootstrap.go`: thêm import `"errors"`, `"go.mongodb.org/mongo-driver/v2/mongo/readconcern"`, `"go.mongodb.org/mongo-driver/v2/mongo/readpref"`; thay cả hàm `ensureFeedAnchor`:

```go
func ensureFeedAnchor(ctx context.Context, db *mongo.Database) error {
	state := db.Collection(reconcilerStateCollection, options.Collection().SetWriteConcern(writeconcern.Majority()))
	keepOrNow := bson.D{{Key: "$ifNull", Value: bson.A{"$at", "$$CLUSTER_TIME"}}}
	update := mongo.Pipeline{{{Key: "$set", Value: bson.D{{Key: "at", Value: keepOrNow}}}}}
	filter := bson.D{{Key: "_id", Value: messagesFeedID}}
	if _, err := state.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true)); err != nil && !mongo.IsDuplicateKeyError(err) {
		return fmt.Errorf("bootstrap %s: anchor change feed: %w", reconcilerStateCollection, err)
	}
	return nil
}
```

bằng:

```go
func ensureFeedAnchor(ctx context.Context, db *mongo.Database) error {
	majority := options.Collection().
		SetReadPreference(readpref.Primary()).
		SetReadConcern(readconcern.Majority()).
		SetWriteConcern(writeconcern.Majority())
	state := db.Collection(reconcilerStateCollection, majority)
	start, err := feedStart(ctx, state)
	if err != nil {
		return err
	}
	keepOrStart := bson.D{{Key: "$ifNull", Value: bson.A{"$at", start}}}
	update := mongo.Pipeline{{{Key: "$set", Value: bson.D{{Key: "at", Value: keepOrStart}}}}}
	filter := bson.D{{Key: "_id", Value: changesFeedID}}
	if _, err := state.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true)); err != nil && !mongo.IsDuplicateKeyError(err) {
		return fmt.Errorf("bootstrap %s: anchor change feed: %w", reconcilerStateCollection, err)
	}
	return nil
}

func feedStart(ctx context.Context, state *mongo.Collection) (any, error) {
	var old feedPosition
	err := state.FindOne(ctx, bson.D{{Key: "_id", Value: legacyMessagesFeedID}}).Decode(&old)
	switch {
	case errors.Is(err, mongo.ErrNoDocuments) || (err == nil && old.At.IsZero()):
		return "$$CLUSTER_TIME", nil
	case err != nil:
		return nil, fmt.Errorf("bootstrap %s: read the messages feed position: %w", reconcilerStateCollection, err)
	default:
		return old.At, nil
	}
}
```

`wc -l apps/core/internal/store/mongostore/feed.go apps/core/internal/store/mongostore/bootstrap.go` phải ≤ 135 và ≤ 100.

**Step 4: Chạy unit, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/... ./apps/core/internal/reconcile/..."`
Expected: PASS (integration skip).

**Step 5: Itest**

Run: `make infra-up && make itest`
Expected: PASS hết, trong đó:
- `TestMongoFeedContract` 6/6 case, gồm "room inserts come out in commit order with their content".
- `TestBootstrapAnchorsTheFeedOnce`, `TestBootstrapCarriesTheMessagesPositionOverWithoutLosingWrites`, `TestForgetClearsTheOldAndTheNewPosition`.
- `apps/core` `TestRealInfraReconcilerPublishesWritesThatSkippedTheCore` (reconciler giờ thấy cả room insert của `createRoom`, bỏ qua nhờ Task 3).

Nếu `Watch` cấp database báo lỗi quyền (user Mongo không phải root) thì dừng và báo cáo: user của core cần `changeStream` + `find` trên cả database.

**Step 6: Commit**

INDEXES.csv, dòng `apps/core/internal/store/mongostore`:
- thay `writes the change feed anchor (cluster time) once if missing` bằng `writes the change feed anchor (reconciler_state _id changes) once if missing, carried over from the old _id messages position when present, else cluster time`.
- thay `Feed tails the messages change stream (insert only; after the saved token, else from the anchor time, else from now) and stores the confirmed resume token in reconciler_state with a conditional majority update that never moves back` bằng `Feed tails a database-level change stream (inserts into messages and rooms, decoded by collection into MessageInserted/RoomInserted; after the saved token, else from the anchor time, else from now) and stores the confirmed resume token in reconciler_state _id changes with a conditional majority update that never moves back; Forget clears the changes and the old messages position`.

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/store/mongostore/ INDEXES.csv
git commit -m "feat(mongostore): watch messages and rooms on one database change stream"
```

Reviewer (task rủi ro): kiểm `ensureFeedAnchor` không bao giờ đè `at`/`token` đã có, `Forget` xoá cả hai id, `$match` chỉ nhận insert vào `messages`/`rooms`, `Confirm` chỉ ghi `_id: changes`.

---

### Task 5: Package `work`: record, id, partition, subject

Record là đơn vị của work stream (D80): chỉ khoá + `CommittedAt`, 33 byte, worker tự đọc doc khi cần. Id tự nhiên (`m:{room}-{thread}-{seq}`, `r:{room}`) làm `Nats-Msg-Id` để work stream gộp trùng khi reader gửi lại. Partition = `slot % P`, nên mọi record của một room luôn vào cùng partition và giữ thứ tự.

`gosec` G115: `CommittedAt.UnixNano()` (int64) ghi thành uint64 qua helper có chặn biên (`< 0` → 0, cùng mẫu `toUint64` trong mongostore); `Decode` từ chối giá trị > `math.MaxInt64` (cùng mẫu `toInt64`). Thời điểm trước 1970 (gồm `time.Time{}`) mã hoá thành 0.

**Files:**
- Create: `apps/core/internal/work/record.go`
- Create: `apps/core/internal/work/record_test.go`
- Modify: `INDEXES.csv` (thêm dòng `apps/core/internal/work` ngay sau dòng `apps/core/internal/view`)

**Step 1: Test**

`apps/core/internal/work/record_test.go`:

```go
package work_test

import (
	"bytes"
	"errors"
	"math"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

var committed = time.Unix(1_700_000_000, 123_456_789).UTC()

func TestRecordOfKeepsOnlyKeysAndCommitTime(t *testing.T) {
	msg := store.Change{Kind: store.MessageInserted, Msg: domain.Message{Room: 42, Thread: 3, Seq: 9, Text: "hi"}, CommittedAt: committed, Position: store.Position("p")}
	if got, want := work.RecordOf(msg), (work.Record{Kind: store.MessageInserted, Room: 42, Thread: 3, Seq: 9, CommittedAt: committed}); got != want {
		t.Fatalf("RecordOf(message) = %+v, want %+v", got, want)
	}
	room := store.Change{Kind: store.RoomInserted, Room: domain.Room{ID: 77, Tenant: "acme"}, CommittedAt: committed}
	if got, want := work.RecordOf(room), (work.Record{Kind: store.RoomInserted, Room: 77, CommittedAt: committed}); got != want {
		t.Fatalf("RecordOf(room) = %+v, want %+v", got, want)
	}
}

func TestRecordRoundTripsThroughThirtyThreeBytes(t *testing.T) {
	for _, r := range []work.Record{
		{Kind: store.MessageInserted, Room: 42, Thread: 3, Seq: 9, CommittedAt: committed},
		{Kind: store.RoomInserted, Room: math.MaxInt64, CommittedAt: committed},
		{Kind: store.MessageInserted, Room: 1, Seq: math.MaxUint64, CommittedAt: committed},
	} {
		b := work.Encode(r)
		if len(b) != work.RecordSize {
			t.Fatalf("Encode(%+v) is %d bytes, want %d", r, len(b), work.RecordSize)
		}
		got, err := work.Decode(b)
		if err != nil || got.Kind != r.Kind || got.Room != r.Room || got.Thread != r.Thread || got.Seq != r.Seq || !got.CommittedAt.Equal(r.CommittedAt) {
			t.Fatalf("Decode(Encode(%+v)) = %+v, %v", r, got, err)
		}
	}
}

func TestEncodeIsBigEndianAndClampsTimesBeforeTheEpoch(t *testing.T) {
	want := make([]byte, work.RecordSize)
	want[0], want[8], want[16], want[24], want[32] = 1, 1, 2, 3, 4
	if got := work.Encode(work.Record{Kind: store.MessageInserted, Room: 1, Thread: 2, Seq: 3, CommittedAt: time.Unix(0, 4)}); !bytes.Equal(got, want) {
		t.Fatalf("Encode = %x, want %x", got, want)
	}
	got, err := work.Decode(work.Encode(work.Record{Kind: store.RoomInserted, Room: 5}))
	if err != nil || !got.CommittedAt.Equal(time.Unix(0, 0)) {
		t.Fatalf("zero commit time decodes to %v, %v; want the epoch", got.CommittedAt, err)
	}
}

func TestDecodeRejectsBadRecords(t *testing.T) {
	good := work.Encode(work.Record{Kind: store.RoomInserted, Room: 7, CommittedAt: committed})
	zeroKind, unknownKind, pastInt64 := slices.Clone(good), slices.Clone(good), slices.Clone(good)
	zeroKind[0], unknownKind[0], pastInt64[25] = 0, 9, 0x80
	for name, b := range map[string][]byte{
		"empty":           nil,
		"short":           good[:work.RecordSize-1],
		"long":            append(slices.Clone(good), 0),
		"zero kind":       zeroKind,
		"unknown kind":    unknownKind,
		"time past int64": pastInt64,
	} {
		if _, err := work.Decode(b); !errors.Is(err, work.ErrBadRecord) || !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: Decode = %v, want ErrBadRecord", name, err)
		}
	}
}

func TestIDsAreNaturalKeys(t *testing.T) {
	cases := map[string]struct {
		r    work.Record
		want string
	}{
		"message":        {work.Record{Kind: store.MessageInserted, Room: 42, Seq: 7, CommittedAt: committed}, "m:42-0-7"},
		"thread message": {work.Record{Kind: store.MessageInserted, Room: 42, Thread: 3, Seq: 9}, "m:42-3-9"},
		"room":           {work.Record{Kind: store.RoomInserted, Room: 42, CommittedAt: committed}, "r:42"},
		"unknown kind":   {work.Record{Room: 42}, ""},
	}
	for name, c := range cases {
		if got := c.r.ID(); got != c.want {
			t.Errorf("%s: ID = %q, want %q", name, got, c.want)
		}
	}
	later := work.Record{Kind: store.MessageInserted, Room: 42, Seq: 7, CommittedAt: committed.Add(time.Hour)}
	if later.ID() != "m:42-0-7" {
		t.Errorf("ID depends on the commit time: %q", later.ID())
	}
}

func TestPartitionFollowsTheRoomSlot(t *testing.T) {
	seen := map[int]bool{}
	for i := range uint64(4096) {
		room := i + 1
		p := work.Partition(room, 32)
		if p != int(slotmap.Of(room))%32 || p != work.Partition(room, 32) {
			t.Fatalf("Partition(%d, 32) = %d, want slot %d %% 32 every time", room, p, slotmap.Of(room))
		}
		seen[p] = true
	}
	if len(seen) != 32 {
		t.Fatalf("4096 rooms fell into %d partitions, want all 32", len(seen))
	}
	if p := work.Partition(42, 1); p != 0 {
		t.Fatalf("Partition(42, 1) = %d, want 0", p)
	}
}

func TestSubjectNamesThePartition(t *testing.T) {
	for p, want := range map[int]string{0: "work.p0", 7: "work.p7", 31: "work.p31"} {
		if got := work.Subject("work", p); got != want {
			t.Errorf("Subject(work, %d) = %q, want %q", p, got, want)
		}
	}
}

func TestMessageCarriesSubjectRecordAndID(t *testing.T) {
	r := work.Record{Kind: store.MessageInserted, Room: 42, Seq: 7, CommittedAt: committed}
	m := work.Message("work", 32, r)
	if want := "work.p" + strconv.Itoa(int(slotmap.Of(42))%32); m.Subject != want {
		t.Fatalf("subject %q, want %q", m.Subject, want)
	}
	if got := m.Header.Get(jetstream.MsgIDHeader); got != "m:42-0-7" {
		t.Fatalf("Nats-Msg-Id %q, want m:42-0-7", got)
	}
	got, err := work.Decode(m.Data)
	if err != nil || got.Room != 42 || got.Seq != 7 || !got.CommittedAt.Equal(committed) {
		t.Fatalf("payload decodes to %+v, %v", got, err)
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/work/..."`
Expected: FAIL biên dịch: package `work` chưa có file non-test (`no non-test Go files` hoặc `undefined: work.Record`).

**Step 3: Code**

`apps/core/internal/work/record.go`:

```go
package work

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const RecordSize = 33

var (
	ErrBadRecord = fmt.Errorf("%w: work record", apperr.ErrInvalidArgument)

	epoch = time.Unix(0, 0)
)

type Record struct {
	Kind        store.ChangeKind
	Room        uint64
	Thread      uint64
	Seq         uint64
	CommittedAt time.Time
}

func RecordOf(c store.Change) Record {
	r := Record{Kind: c.Kind, CommittedAt: c.CommittedAt}
	switch c.Kind {
	case store.MessageInserted:
		r.Room, r.Thread, r.Seq = c.Msg.Room, c.Msg.Thread, c.Msg.Seq
	case store.RoomInserted:
		r.Room = c.Room.ID
	}
	return r
}

func (r Record) ID() string {
	switch r.Kind {
	case store.MessageInserted:
		return "m:" + pbconv.MessageEventID(r.Room, r.Thread, r.Seq)
	case store.RoomInserted:
		return "r:" + pbconv.RoomID(r.Room)
	default:
		return ""
	}
}

func Encode(r Record) []byte {
	b := make([]byte, 1, RecordSize)
	b[0] = byte(r.Kind)
	b = binary.BigEndian.AppendUint64(b, r.Room)
	b = binary.BigEndian.AppendUint64(b, r.Thread)
	b = binary.BigEndian.AppendUint64(b, r.Seq)
	return binary.BigEndian.AppendUint64(b, unixNano(r.CommittedAt))
}

func Decode(b []byte) (Record, error) {
	if len(b) != RecordSize {
		return Record{}, fmt.Errorf("%w: %d bytes, want %d", ErrBadRecord, len(b), RecordSize)
	}
	kind := store.ChangeKind(b[0])
	if kind != store.MessageInserted && kind != store.RoomInserted {
		return Record{}, fmt.Errorf("%w: kind %d", ErrBadRecord, kind)
	}
	ns := binary.BigEndian.Uint64(b[25:])
	if ns > math.MaxInt64 {
		return Record{}, fmt.Errorf("%w: commit time out of range", ErrBadRecord)
	}
	return Record{
		Kind:        kind,
		Room:        binary.BigEndian.Uint64(b[1:]),
		Thread:      binary.BigEndian.Uint64(b[9:]),
		Seq:         binary.BigEndian.Uint64(b[17:]),
		CommittedAt: time.Unix(0, int64(ns)).UTC(),
	}, nil
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

`switch c.Kind` trong `RecordOf` phủ đủ hai hằng `ChangeKind` nên `exhaustive` không đòi `default`. `Partition` không tự chặn `partitions <= 0`: `StreamConfig.Validate` (Task 6) chặn ở boot.

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/work/..."`
Expected: PASS. `make lint` không báo G115 (nếu báo, dừng và báo cáo, không thêm `//nolint`).

**Step 5: Commit**

INDEXES.csv, thêm ngay sau dòng `apps/core/internal/view,...`:

```
apps/core/internal/work,package,"Work stream records for the effect engine: Record holds only kind + room/thread/seq + CommittedAt in 33 big-endian bytes (times before 1970 encode as 0); natural ids m:{room}-{thread}-{seq} and r:{room} become Nats-Msg-Id; Partition = slotmap.Of(room) % P; Subject {root}.p{n}",Record;RecordSize;ErrBadRecord;RecordOf;Record.ID;Encode;Decode;Partition;Subject;Message,apps/core/internal/reconcile;apps/core/internal/effects,unit,D66;D79;D80
```

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/work/ INDEXES.csv
git commit -m "feat(work): add work records with natural ids and slot partitions"
```

---

#### Ghi chú cho controller (part A)

**Hợp đồng: không đổi chữ ký nào.** Có ba chỗ part A phải chốt mà hợp đồng chưa nói, các task sau cần biết:
1. `work.Encode`: `CommittedAt` trước 1970 (gồm `time.Time{}`) mã hoá thành 0. `work.Decode` còn từ chối giá trị thời gian > `math.MaxInt64`, ngoài hai lỗi độ dài và kind lạ. Lý do là G115 (không có comment/nolint thì cần chặn biên). Hợp đồng chỉ ghi "int64 big-endian"; với mọi thời điểm từ 1970 đến 2262 thì byte ra vẫn y như vậy.
2. `Record.ID()` của kind lạ trả `""`. `Message` khi đó đặt `Nats-Msg-Id` rỗng, tức không gộp trùng. Reader chỉ tạo record từ change hợp lệ.
3. `memstore.Rooms` chỉ ghi vào log chung sau khi được gắn bằng `NewFeed(msgs, rooms)`. Room tạo trước lúc gắn không có trong log. Harness của Task 7/14 muốn thấy room change thì phải gọi `NewFeed` trước `Create`.

**Ngoài phạm vi được giao, cần cho đúng:**
- Task 3 thêm điều kiện bỏ qua room change vào `reconcile/term.go` (`acked[i] || c.Kind == store.RoomInserted`) cùng test `reconcile/room_change_test.go`. Không có nó thì từ Task 4 tới Task 7 mỗi `CreateRoom` trên môi trường thật thành một lần drop (warn log, `Dropped++`, có thể kích alert). Harness reconcile vẫn là `memstore.NewFeed(rg.msgs, nil)` như yêu cầu. **Task 7 phải xoá điều kiện này và `room_change_test.go`** (hoặc viết lại thành test của reader).
- Task 3 gắn `Kind: store.MessageInserted` vào change của feed Mongo cũ (một dòng), để case contract mới không fail vì sai kind. Case room của Mongo chỉ qua sau Task 4.
- Task 2 tách thành 3 commit (publish / corecli / core), Task 3 thành 2 commit (store / reconcile).
- Mỗi task sửa `INDEXES.csv` ngay trong commit của nó (luật CLAUDE.md), bằng thay chuỗi chính xác. Task 16 không cần sửa lại các dòng này.

**Caller đã cập nhật:**
- `grpcsrv.Deps`: `apps/core/wiring.go:107` (`Events: pub`), `grpcsrv/harness_test.go:68`. `caller_identity_test.go` không cần sửa vì `Events` nil được phép.
- `e2e.EventOf`: `tools/corecli/cmd_watch.go:77`.
- `memstore.NewFeed`: `memstore/feed_test.go:13,27`, `memstore/memstore_test.go:20`, `reconcile/harness_test.go:104`.
- `storetest.RunFeed`: `memstore/memstore_test.go`, `mongostore/feed_integration_test.go`.
- `store.Change{...}`: `memstore/feed.go` (Next), `mongostore/feed.go` (Task 3 sửa một dòng, Task 4 viết lại).
- `messagesFeedID` bị bỏ: `bootstrap.go`, `bootstrap_integration_test.go:130`, `feed.go`.

**Rủi ro:**
- Change stream cấp database cần quyền `changeStream` + `find` trên cả database. Dev chạy root nên không gặp; prod phải kiểm trước khi deploy. Đưa vào docs ở Task 16.
- Chuyển từ `_id: messages` sang `changes` dùng `at` cũ (`StartAtOperationTime`), không dùng token cũ. Nếu `at` cũ đã ra khỏi oplog thì `Open` báo `ErrFeedHistoryLost`, rồi đi đường Forget hiện có (mất khoảng đó, có log + metric). Chấp nhận được, nhưng runbook nên ghi.
- Rolling deploy có cả core cũ và mới: core cũ còn upsert `_id: messages` khi Confirm. Doc đó chỉ được đọc khi `changes` thiếu (DB mới, hoặc ngay sau Forget và trước confirm đầu tiên). Rủi ro thấp; sau khi hết core cũ thì hết.
- `memstore`: `Rooms.Create` giữ `Rooms.mu` rồi lấy `Messages.mu`. Không có đường nào lấy theo chiều ngược lại; giữ nguyên quy tắc này khi sửa memstore ở Task 11 (`TouchActivity`).
- `apps/core` itest `TestRealInfraReconcilerPublishesWritesThatSkippedTheCore` từ Task 4 có thêm room change trong feed. Test lọc theo Msg-Id nên không ảnh hưởng.
- README và thiết kế còn nhắc `reconciler_state` `{_id: "messages"}` (CLAUDE.md, mục Bootstrap anchor). Task 16 phải sửa thành `changes` và mô tả cách chuyển.

### Task 6: Work stream — ensure stream + consumer, adapter `Queue`, fake `worktest`, config `Work`

Reader (Task 7) cần chỗ để đẩy record, worker (Task 8) cần chỗ để kéo record. Task này dựng stream `CHATIM_WORK` (WorkQueue, D79) với một durable pull consumer cho mỗi partition, adapter `Queue` bọc `jetstream.Consumer` và fake `worktest.Broker` cho unit test có `synctest`. Core đảm bảo stream ngay sau `publish.EnsureStream` lúc khởi động.

Quyết định cho record không giải mã được: adapter gọi `Term()` (JetStream không giao lại, stream WorkQueue xoá nó), bỏ khỏi kết quả, và trả kèm lỗi `work.BadRecordsError{Terminated: n}` (unwrap ra `work.ErrBadRecord`) cùng các delivery hợp lệ của lô đó. Worker (Task 8) cộng `n` vào `Failed` và log có giới hạn. Không `Ack` vì `Term` để lại advisory trên server, dễ truy vết hơn.

Hành vi `Fetch` của adapter: kéo một lô bằng `Consumer.Fetch(max, jetstream.FetchMaxWait(wait))`, đọc cho tới khi lô đóng (tối đa `wait`) hoặc `ctx` hết; khi `ctx` hết, trả các delivery đã nhận cùng `ctx.Err()` (worker trả chúng bằng `Nak(0)`). Tin server đã đẩy vào lô nhưng chưa đọc lúc `ctx` hết sẽ quay lại sau `AckWait` (chỉ xảy ra lúc dừng).

**Files:**
- Create: `apps/core/internal/work/stream.go`
- Create: `apps/core/internal/work/queue.go`
- Create: `apps/core/internal/work/stream_test.go`
- Create: `apps/core/internal/work/nats_integration_test.go`
- Create: `apps/core/internal/work/worktest/broker.go`
- Create: `apps/core/internal/work/worktest/broker_test.go`
- Modify: `apps/core/internal/config/config.go` (struct `Config`)
- Modify: `apps/core/internal/config/components.go`
- Modify: `apps/core/internal/config/validate.go`
- Modify: `apps/core/internal/config/env_test.go`, `load_test.go`, `validate_test.go`
- Modify: `apps/core/wiring.go` (`prepare`)
- Modify: `apps/core/it_infra_test.go` (`coreConfig`, `forget`)
- Modify: `README.md` (bảng env), `INDEXES.csv`

**Step 1: Test stream + broker**

`apps/core/internal/work/stream_test.go`:

```go
package work_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var errAdmin = errors.New("jetstream api down")

type spyAdmin struct {
	streams     []jetstream.StreamConfig
	owners      []string
	consumers   []jetstream.ConsumerConfig
	streamErr   error
	consumerErr error
	failOn      int
}

func (s *spyAdmin) CreateOrUpdateStream(_ context.Context, cfg jetstream.StreamConfig) (jetstream.Stream, error) {
	s.streams = append(s.streams, cfg)
	return nil, s.streamErr
}

func (s *spyAdmin) CreateOrUpdateConsumer(_ context.Context, stream string, cfg jetstream.ConsumerConfig) (jetstream.Consumer, error) {
	s.owners = append(s.owners, stream)
	s.consumers = append(s.consumers, cfg)
	if s.consumerErr != nil && len(s.consumers) == s.failOn {
		return nil, s.consumerErr
	}
	return nil, nil
}

var valid = work.StreamConfig{
	Name: "CHATIM_WORK", SubjectRoot: "work", Partitions: 3, Replicas: 1,
	MaxAge: 2 * time.Hour, Duplicates: 2 * time.Minute, AckWait: 35 * time.Second,
}

func TestEnsureStreamCreatesAWorkQueueAndAConsumerPerPartition(t *testing.T) {
	spy := &spyAdmin{}
	if err := work.EnsureStream(t.Context(), spy, valid); err != nil {
		t.Fatalf("EnsureStream: %v", err)
	}
	wantStream := jetstream.StreamConfig{
		Name: "CHATIM_WORK", Subjects: []string{"work.>"}, Retention: jetstream.WorkQueuePolicy,
		Storage: jetstream.FileStorage, Replicas: 1, MaxAge: 2 * time.Hour, Duplicates: 2 * time.Minute,
	}
	if !reflect.DeepEqual(spy.streams, []jetstream.StreamConfig{wantStream}) {
		t.Fatalf("streams = %+v, want %+v", spy.streams, wantStream)
	}
	if len(spy.consumers) != 3 {
		t.Fatalf("consumers = %d, want one per partition", len(spy.consumers))
	}
	for p, got := range spy.consumers {
		want := jetstream.ConsumerConfig{
			Durable: work.ConsumerName(p), FilterSubject: work.Subject("work", p),
			AckPolicy: jetstream.AckExplicitPolicy, AckWait: 35 * time.Second,
			MaxAckPending: work.MaxAckPending, DeliverPolicy: jetstream.DeliverAllPolicy,
		}
		if !reflect.DeepEqual(got, want) || spy.owners[p] != "CHATIM_WORK" {
			t.Fatalf("consumer %d on %s = %+v, want %+v on CHATIM_WORK", p, spy.owners[p], got, want)
		}
	}
}

func TestEnsureStreamSendsTheSameConfigEveryTime(t *testing.T) {
	spy := &spyAdmin{}
	for range 2 {
		if err := work.EnsureStream(t.Context(), spy, valid); err != nil {
			t.Fatalf("EnsureStream: %v", err)
		}
	}
	if len(spy.streams) != 2 || !reflect.DeepEqual(spy.streams[0], spy.streams[1]) || !reflect.DeepEqual(spy.consumers[:3], spy.consumers[3:]) {
		t.Fatalf("second ensure differs: streams %+v consumers %+v", spy.streams, spy.consumers)
	}
}

func TestEnsureStreamNamesTheStepThatFailed(t *testing.T) {
	spy := &spyAdmin{streamErr: errAdmin}
	err := work.EnsureStream(t.Context(), spy, valid)
	if !errors.Is(err, errAdmin) || !strings.Contains(err.Error(), "CHATIM_WORK") || len(spy.consumers) != 0 {
		t.Fatalf("EnsureStream with a failing stream call = %v (consumers %d), want the stream error and no consumer", err, len(spy.consumers))
	}
	spy = &spyAdmin{consumerErr: errAdmin, failOn: 2}
	err = work.EnsureStream(t.Context(), spy, valid)
	if !errors.Is(err, errAdmin) || !strings.Contains(err.Error(), work.ConsumerName(1)) || len(spy.consumers) != 2 {
		t.Fatalf("EnsureStream with a failing consumer = %v (consumers %d), want it to stop at %s", err, len(spy.consumers), work.ConsumerName(1))
	}
}

func TestEnsureStreamRejectsBadConfig(t *testing.T) {
	if err := work.EnsureStream(t.Context(), nil, valid); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("EnsureStream(nil admin) = %v, want ErrInvalidArgument", err)
	}
	tests := []struct {
		name   string
		mutate func(*work.StreamConfig)
	}{
		{"dotted name", func(c *work.StreamConfig) { c.Name = "CHATIM.WORK" }},
		{"wildcard root", func(c *work.StreamConfig) { c.SubjectRoot = "work>" }},
		{"partitions above slot count", func(c *work.StreamConfig) { c.Partitions = 1025 }},
		{"negative partitions", func(c *work.StreamConfig) { c.Partitions = -1 }},
		{"zero replicas", func(c *work.StreamConfig) { c.Replicas = 0 }},
		{"duplicate window above max age", func(c *work.StreamConfig) { c.Duplicates = 3 * time.Hour }},
		{"negative ack wait", func(c *work.StreamConfig) { c.AckWait = -time.Second }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := valid
			tt.mutate(&cfg)
			spy := &spyAdmin{}
			if err := work.EnsureStream(t.Context(), spy, cfg); !errors.Is(err, apperr.ErrInvalidArgument) || len(spy.streams) != 0 {
				t.Fatalf("EnsureStream(%+v) = %v with %d stream calls, want ErrInvalidArgument and none", cfg, err, len(spy.streams))
			}
		})
	}
}

func TestStreamConfigFillsDefaults(t *testing.T) {
	if err := (work.StreamConfig{Name: "W", SubjectRoot: "w", Replicas: 1}).Validate(); err != nil {
		t.Fatalf("Validate with defaults = %v, want nil", err)
	}
	if got := work.ConsumerName(7); got != "work-p7" {
		t.Fatalf("ConsumerName(7) = %q, want work-p7", got)
	}
}
```

`apps/core/internal/work/worktest/broker_test.go`:

```go
package worktest_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/work/worktest"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func rec(seq uint64) work.Record {
	return work.Record{Kind: store.MessageInserted, Room: 7, Seq: seq, CommittedAt: time.Unix(1700000000, 0)}
}

func seqs(ds []work.Delivery) []uint64 {
	var out []uint64
	for _, d := range ds {
		out = append(out, d.Record().Seq)
	}
	return out
}

func TestFetchReturnsUpToMaxInPublishOrder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := &worktest.Broker{}
		for s := range uint64(3) {
			b.Publish(1, rec(s+1))
		}
		q := b.Queue(1)
		first, err := q.Fetch(t.Context(), 2, time.Second)
		if err != nil || !slices.Equal(seqs(first), []uint64{1, 2}) {
			t.Fatalf("first Fetch = %v, %v; want [1 2]", seqs(first), err)
		}
		second, err := q.Fetch(t.Context(), 2, time.Second)
		if err != nil || !slices.Equal(seqs(second), []uint64{3}) {
			t.Fatalf("second Fetch = %v, %v; want [3]", seqs(second), err)
		}
		if got := b.Pending(1); got != 3 {
			t.Fatalf("Pending = %d, want 3 delivered but unacked", got)
		}
		if got := b.Pending(0); got != 0 {
			t.Fatalf("Pending(0) = %d, want 0", got)
		}
	})
}

func TestFetchWaitsForAPublishOrTheWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := &worktest.Broker{}
		q := b.Queue(0)
		begin := time.Now()
		go func() {
			time.Sleep(300 * time.Millisecond)
			b.Publish(0, rec(1))
		}()
		got, err := q.Fetch(t.Context(), 1, time.Second)
		if err != nil || !slices.Equal(seqs(got), []uint64{1}) || time.Since(begin) != 300*time.Millisecond {
			t.Fatalf("Fetch = %v, %v after %v; want [1] after 300ms", seqs(got), err, time.Since(begin))
		}
		begin = time.Now()
		got, err = q.Fetch(t.Context(), 1, time.Second)
		if err != nil || len(got) != 0 || time.Since(begin) != time.Second {
			t.Fatalf("idle Fetch = %v, %v after %v; want nothing after 1s", seqs(got), err, time.Since(begin))
		}
	})
}

func TestAckRemovesAndNakRedeliversAfterTheDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := &worktest.Broker{}
		b.Publish(0, rec(1))
		b.Publish(0, rec(2))
		q := b.Queue(0)
		ds, err := q.Fetch(t.Context(), 10, time.Second)
		if err != nil || len(ds) != 2 {
			t.Fatalf("Fetch = %v, %v", seqs(ds), err)
		}
		if err := ds[0].Ack(); err != nil {
			t.Fatalf("Ack: %v", err)
		}
		nakAt := time.Now()
		if err := ds[1].Nak(5 * time.Second); err != nil {
			t.Fatalf("Nak: %v", err)
		}
		if err := ds[0].Ack(); !errors.Is(err, worktest.ErrSettled) {
			t.Fatalf("second Ack = %v, want ErrSettled", err)
		}
		if a, n := b.Acked(), b.Naked(); len(a) != 1 || a[0].Seq != 1 || len(n) != 1 || n[0].Seq != 2 || b.Pending(0) != 1 {
			t.Fatalf("acked %v, naked %v, pending %d; want [1], [2], 1", a, n, b.Pending(0))
		}
		if early, err := q.Fetch(t.Context(), 10, time.Second); err != nil || len(early) != 0 {
			t.Fatalf("Fetch before the nak delay = %v, %v; want nothing", seqs(early), err)
		}
		again, err := q.Fetch(t.Context(), 10, time.Minute)
		if err != nil || !slices.Equal(seqs(again), []uint64{2}) || time.Since(nakAt) != 5*time.Second {
			t.Fatalf("redelivery = %v, %v after %v; want [2] exactly 5s after the nak", seqs(again), err, time.Since(nakAt))
		}
	})
}

func TestFetchStopsWhenTheContextEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		b := &worktest.Broker{}
		ctx, cancel := context.WithCancel(t.Context())
		go func() {
			time.Sleep(100 * time.Millisecond)
			cancel()
		}()
		if _, err := b.Queue(0).Fetch(ctx, 1, time.Minute); !errors.Is(err, context.Canceled) {
			t.Fatalf("Fetch after cancel = %v, want context.Canceled", err)
		}
	})
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/work/..."`
Expected: FAIL biên dịch: `undefined: work.StreamConfig`, `work.EnsureStream`, `work.ConsumerName`, `work.MaxAckPending` và package `worktest` không tồn tại.

**Step 3: Code**

`apps/core/internal/work/stream.go`:

```go
package work

import (
	"cmp"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const (
	DefaultPartitions = 32
	DefaultMaxAge     = 2 * time.Hour
	DefaultDuplicates = 2 * time.Minute
	DefaultAckWait    = 35 * time.Second
	MaxAckPending     = 1024
)

type StreamConfig struct {
	Name        string
	SubjectRoot string
	Partitions  int
	Replicas    int
	MaxAge      time.Duration
	Duplicates  time.Duration
	AckWait     time.Duration
}

type Admin interface {
	CreateOrUpdateStream(ctx context.Context, cfg jetstream.StreamConfig) (jetstream.Stream, error)
	CreateOrUpdateConsumer(ctx context.Context, stream string, cfg jetstream.ConsumerConfig) (jetstream.Consumer, error)
}

func (c StreamConfig) Validate() error { return c.withDefaults().validate() }

func (c StreamConfig) withDefaults() StreamConfig {
	c.Partitions = cmp.Or(c.Partitions, DefaultPartitions)
	c.MaxAge = cmp.Or(c.MaxAge, DefaultMaxAge)
	c.Duplicates = cmp.Or(c.Duplicates, DefaultDuplicates)
	c.AckWait = cmp.Or(c.AckWait, DefaultAckWait)
	return c
}

func (c StreamConfig) validate() error {
	switch {
	case !subjectToken(c.Name) || !subjectToken(c.SubjectRoot):
		return fmt.Errorf("%w: work stream name %q and subject root %q must be single subject tokens", apperr.ErrInvalidArgument, c.Name, c.SubjectRoot)
	case c.Partitions <= 0 || c.Partitions > slotmap.Count:
		return fmt.Errorf("%w: work partitions %d must be between 1 and the slot count %d", apperr.ErrInvalidArgument, c.Partitions, slotmap.Count)
	case c.Replicas <= 0 || c.MaxAge <= 0 || c.Duplicates <= 0 || c.AckWait <= 0:
		return fmt.Errorf("%w: work stream replicas %d, max age %v, duplicate window %v and ack wait %v must be positive", apperr.ErrInvalidArgument, c.Replicas, c.MaxAge, c.Duplicates, c.AckWait)
	case c.Duplicates > c.MaxAge:
		return fmt.Errorf("%w: work stream duplicate window %v exceeds max age %v", apperr.ErrInvalidArgument, c.Duplicates, c.MaxAge)
	default:
		return nil
	}
}

func ConsumerName(partition int) string { return "work-p" + strconv.Itoa(partition) }

func EnsureStream(ctx context.Context, js Admin, c StreamConfig) error {
	if js == nil {
		return fmt.Errorf("%w: ensure work stream needs a jetstream client", apperr.ErrInvalidArgument)
	}
	c = c.withDefaults()
	if err := c.validate(); err != nil {
		return err
	}
	if _, err := js.CreateOrUpdateStream(ctx, c.stream()); err != nil {
		return fmt.Errorf("ensure work stream %s: %w", c.Name, err)
	}
	for p := range c.Partitions {
		if _, err := js.CreateOrUpdateConsumer(ctx, c.Name, c.consumer(p)); err != nil {
			return fmt.Errorf("ensure work consumer %s on %s: %w", ConsumerName(p), c.Name, err)
		}
	}
	return nil
}

func (c StreamConfig) stream() jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:       c.Name,
		Subjects:   []string{c.SubjectRoot + ".>"},
		Retention:  jetstream.WorkQueuePolicy,
		Storage:    jetstream.FileStorage,
		Replicas:   c.Replicas,
		MaxAge:     c.MaxAge,
		Duplicates: c.Duplicates,
	}
}

func (c StreamConfig) consumer(p int) jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{
		Durable:       ConsumerName(p),
		FilterSubject: Subject(c.SubjectRoot, p),
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       c.AckWait,
		MaxAckPending: MaxAckPending,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	}
}

func subjectToken(s string) bool { return s != "" && !strings.ContainsAny(s, ".*> \t\r\n") }
```

`apps/core/internal/work/queue.go`:

```go
package work

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

type Delivery interface {
	Record() Record
	Ack() error
	Nak(delay time.Duration) error
}

type Queue interface {
	Fetch(ctx context.Context, max int, wait time.Duration) ([]Delivery, error)
}

type BadRecordsError struct {
	Terminated uint64
}

func (e BadRecordsError) Error() string {
	return strconv.FormatUint(e.Terminated, 10) + " undecodable work records terminated"
}

func (BadRecordsError) Unwrap() error { return ErrBadRecord }

type jetStreamQueue struct {
	js        jetstream.JetStream
	stream    string
	partition int
	consumer  jetstream.Consumer
}

func NewQueue(js jetstream.JetStream, stream string, partition int) Queue {
	return &jetStreamQueue{js: js, stream: stream, partition: partition}
}

func (q *jetStreamQueue) Fetch(ctx context.Context, limit int, wait time.Duration) ([]Delivery, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if q.consumer == nil {
		c, err := q.js.Consumer(ctx, q.stream, ConsumerName(q.partition))
		if err != nil {
			return nil, fmt.Errorf("work consumer %s on %s: %w", ConsumerName(q.partition), q.stream, err)
		}
		q.consumer = c
	}
	batch, err := q.consumer.Fetch(limit, jetstream.FetchMaxWait(wait))
	if err != nil {
		return nil, fmt.Errorf("fetch work partition %d: %w", q.partition, err)
	}
	return collect(ctx, batch)
}

func collect(ctx context.Context, batch jetstream.MessageBatch) ([]Delivery, error) {
	var out []Delivery
	var bad uint64
	msgs := batch.Messages()
	for {
		select {
		case m, open := <-msgs:
			if !open {
				return out, errors.Join(batch.Error(), badRecords(bad))
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

func badRecords(n uint64) error {
	if n == 0 {
		return nil
	}
	return BadRecordsError{Terminated: n}
}

type jetStreamDelivery struct {
	msg jetstream.Msg
	rec Record
}

func (d jetStreamDelivery) Record() Record { return d.rec }

func (d jetStreamDelivery) Ack() error { return d.msg.Ack() }

func (d jetStreamDelivery) Nak(delay time.Duration) error { return d.msg.NakWithDelay(delay) }
```

`Queue` của adapter chỉ dành cho một goroutine gọi (mỗi partition một worker); consumer được tra lười ở lần `Fetch` đầu vì `NewQueue` không có `ctx` theo hợp đồng.

`apps/core/internal/work/worktest/broker.go`:

```go
package worktest

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

var ErrSettled = errors.New("work delivery already settled")

type entry struct {
	rec   work.Record
	ready time.Time
}

type Broker struct {
	mu       sync.Mutex
	queued   map[int][]entry
	inflight map[int]int
	acked    []work.Record
	naked    []work.Record
	grew     chan struct{}
}

func (b *Broker) Publish(partition int, r work.Record) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pushLocked(partition, entry{rec: r, ready: time.Now()})
}

func (b *Broker) Queue(partition int) work.Queue { return &queue{b: b, partition: partition} }

func (b *Broker) Acked() []work.Record {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.acked)
}

func (b *Broker) Naked() []work.Record {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.naked)
}

func (b *Broker) Pending(partition int) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.queued[partition]) + b.inflight[partition]
}

func (b *Broker) initLocked() {
	if b.queued == nil {
		b.queued, b.inflight, b.grew = map[int][]entry{}, map[int]int{}, make(chan struct{})
	}
}

func (b *Broker) pushLocked(partition int, e entry) {
	b.initLocked()
	b.queued[partition] = append(b.queued[partition], e)
	close(b.grew)
	b.grew = make(chan struct{})
}

func (b *Broker) take(partition, limit int) ([]work.Delivery, time.Time, <-chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.initLocked()
	now := time.Now()
	var out []work.Delivery
	var keep []entry
	var next time.Time
	for _, e := range b.queued[partition] {
		switch {
		case len(out) < limit && !e.ready.After(now):
			out = append(out, &delivery{b: b, partition: partition, rec: e.rec})
		case e.ready.After(now) && (next.IsZero() || e.ready.Before(next)):
			next = e.ready
			keep = append(keep, e)
		default:
			keep = append(keep, e)
		}
	}
	b.queued[partition] = keep
	b.inflight[partition] += len(out)
	return out, next, b.grew
}

func (b *Broker) settle(d *delivery, nak bool, delay time.Duration) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if d.settled {
		return ErrSettled
	}
	d.settled = true
	b.inflight[d.partition]--
	if !nak {
		b.acked = append(b.acked, d.rec)
		return nil
	}
	b.naked = append(b.naked, d.rec)
	b.pushLocked(d.partition, entry{rec: d.rec, ready: time.Now().Add(delay)})
	return nil
}

type queue struct {
	b         *Broker
	partition int
}

func (q *queue) Fetch(ctx context.Context, limit int, wait time.Duration) ([]work.Delivery, error) {
	deadline := time.Now().Add(wait)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		out, next, grew := q.b.take(q.partition, limit)
		left := time.Until(deadline)
		if len(out) > 0 || left <= 0 {
			return out, nil
		}
		if !next.IsZero() {
			left = min(left, time.Until(next))
		}
		if err := await(ctx, grew, left); err != nil {
			return nil, err
		}
	}
}

func await(ctx context.Context, grew <-chan struct{}, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-grew:
		return nil
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type delivery struct {
	b         *Broker
	partition int
	rec       work.Record
	settled   bool
}

func (d *delivery) Record() work.Record { return d.rec }

func (d *delivery) Ack() error { return d.b.settle(d, false, 0) }

func (d *delivery) Nak(delay time.Duration) error { return d.b.settle(d, true, delay) }
```

`worktest.Broker` dùng được ở giá trị zero; `Pending` = record chưa ack (đang xếp hàng, đang chờ hết delay nak, hoặc đã giao mà chưa settle). Record bị nak xếp vào cuối hàng với mốc `ready = now + delay`.

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/work/..."`
Expected: PASS (test `record` của Task 5, test stream, test broker). `wc -l apps/core/internal/work/*.go apps/core/internal/work/worktest/*.go` đều < 200.

**Step 5: Itest trên NATS thật**

`apps/core/internal/work/nats_integration_test.go`:

```go
package work_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

const itNATSURLEnv = "CHATIM_IT_NATS_URL"

func realWork(t *testing.T) (jetstream.JetStream, work.StreamConfig) {
	t.Helper()
	url := os.Getenv(itNATSURLEnv)
	if url == "" {
		t.Skip("set CHATIM_IT_NATS_URL to run")
	}
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect %s: %v", url, err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatalf("jetstream.New: %v", err)
	}
	var b [6]byte
	_, _ = rand.Read(b[:])
	suffix := hex.EncodeToString(b[:])
	cfg := work.StreamConfig{
		Name: "IT_WORK_" + strings.ToUpper(suffix), SubjectRoot: "itwork" + suffix, Partitions: 4, Replicas: 1,
		MaxAge: time.Hour, Duplicates: time.Minute, AckWait: 30 * time.Second,
	}
	for range 2 {
		if err := work.EnsureStream(t.Context(), js, cfg); err != nil {
			t.Fatalf("EnsureStream: %v", err)
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := js.DeleteStream(ctx, cfg.Name); err != nil {
			t.Errorf("delete stream %s: %v", cfg.Name, err)
		}
	})
	return js, cfg
}

func roomsOnPartition(p, partitions, n int) []uint64 {
	var out []uint64
	for r := uint64(1); len(out) < n; r++ {
		if work.Partition(r, partitions) == p {
			out = append(out, r)
		}
	}
	return out
}

func TestRealWorkStreamIsAWorkQueueWithAConsumerPerPartition(t *testing.T) {
	js, cfg := realWork(t)
	s, err := js.Stream(t.Context(), cfg.Name)
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	info, err := s.Info(t.Context())
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}
	if info.Config.Retention != jetstream.WorkQueuePolicy || info.Config.Duplicates != time.Minute {
		t.Fatalf("stream config = %+v, want a work queue with a 1m duplicate window", info.Config)
	}
	for p := range cfg.Partitions {
		c, err := js.Consumer(t.Context(), cfg.Name, work.ConsumerName(p))
		if err != nil {
			t.Fatalf("consumer %d: %v", p, err)
		}
		ci := c.CachedInfo()
		if ci.Config.FilterSubject != work.Subject(cfg.SubjectRoot, p) || ci.Config.AckPolicy != jetstream.AckExplicitPolicy || ci.Config.MaxAckPending != work.MaxAckPending {
			t.Fatalf("consumer %d config = %+v", p, ci.Config)
		}
	}
}

func TestRealWorkQueueFetchesAcksAndRedeliversNaks(t *testing.T) {
	js, cfg := realWork(t)
	committed := time.Now().UTC()
	var want []work.Record
	for _, room := range roomsOnPartition(1, cfg.Partitions, 3) {
		r := work.Record{Kind: store.MessageInserted, Room: room, Seq: 1, CommittedAt: committed}
		if _, err := js.PublishMsg(t.Context(), work.Message(cfg.SubjectRoot, cfg.Partitions, r)); err != nil {
			t.Fatalf("publish %s: %v", r.ID(), err)
		}
		want = append(want, r)
	}
	q := work.NewQueue(js, cfg.Name, 1)
	ds, err := q.Fetch(t.Context(), 10, 2*time.Second)
	if err != nil || len(ds) != 3 {
		t.Fatalf("Fetch = %d deliveries, %v; want 3", len(ds), err)
	}
	for i, d := range ds {
		if got := d.Record(); got.ID() != want[i].ID() || !got.CommittedAt.Equal(committed) {
			t.Fatalf("delivery %d = %+v, want %+v", i, got, want[i])
		}
	}
	for _, d := range ds[:2] {
		if err := d.Ack(); err != nil {
			t.Fatalf("Ack: %v", err)
		}
	}
	nakAt := time.Now()
	if err := ds[2].Nak(500 * time.Millisecond); err != nil {
		t.Fatalf("Nak: %v", err)
	}
	again, err := q.Fetch(t.Context(), 10, 3*time.Second)
	if err != nil || len(again) != 1 || again[0].Record().ID() != want[2].ID() || time.Since(nakAt) < 300*time.Millisecond {
		t.Fatalf("redelivery = %d deliveries, %v after %v; want %s after the nak delay", len(again), err, time.Since(nakAt), want[2].ID())
	}
	if err := again[0].Ack(); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	awaitEmpty(t, js, cfg.Name)
}

func TestRealWorkQueueTerminatesUndecodableRecords(t *testing.T) {
	js, cfg := realWork(t)
	junk := &nats.Msg{Subject: work.Subject(cfg.SubjectRoot, 2), Data: []byte("junk"), Header: nats.Header{}}
	junk.Header.Set(jetstream.MsgIDHeader, "junk-1")
	if _, err := js.PublishMsg(t.Context(), junk); err != nil {
		t.Fatalf("publish junk: %v", err)
	}
	ds, err := work.NewQueue(js, cfg.Name, 2).Fetch(t.Context(), 10, 2*time.Second)
	var bad work.BadRecordsError
	if len(ds) != 0 || !errors.As(err, &bad) || bad.Terminated != 1 || !errors.Is(err, work.ErrBadRecord) {
		t.Fatalf("Fetch of junk = %d deliveries, %v; want none and one terminated bad record", len(ds), err)
	}
	awaitEmpty(t, js, cfg.Name)
}

func awaitEmpty(t *testing.T, js jetstream.JetStream, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s, err := js.Stream(t.Context(), name)
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		info, err := s.Info(t.Context())
		if err != nil {
			t.Fatalf("stream info: %v", err)
		}
		if info.State.Msgs == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("work stream still holds %d messages 5s after every ack", info.State.Msgs)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
```

`Ack` của nats.go không chờ xác nhận, nên `awaitEmpty` hỏi lại trong 5s; đây là itest trên hạ tầng thật, không phải unit test nhạy thời gian.

**Step 6: Config `Work` — test**

`apps/core/internal/config/env_test.go`:
- `envKeys`: thêm dòng sau dòng `"EVT_STREAM", "EVT_SUBJECT_ROOT", ...`:

```go
	"WORK_STREAM", "WORK_SUBJECT_ROOT", "WORK_PARTITIONS", "WORK_MAX_AGE", "WORK_DUPLICATES",
```

- `overrides`: thêm dòng sau dòng `"EVT_STREAM_REPLICAS": "3", ...`:

```go
	"WORK_STREAM": "CHATIM_WORK_IT", "WORK_SUBJECT_ROOT": "work_it", "WORK_PARTITIONS": "16", "WORK_MAX_AGE": "1h", "WORK_DUPLICATES": "3m",
```

`apps/core/internal/config/load_test.go`: thêm import `"github.com/ivannguyendev/chatim/apps/core/internal/work"`.
- `TestLoadDefaults`, ngay sau dòng `Stream: publish.StreamConfig{Name: "CHATIM_EVT", ...},`:

```go
		Work: work.StreamConfig{Name: "CHATIM_WORK", SubjectRoot: "work", Partitions: 32, Replicas: 1, MaxAge: 2 * time.Hour, Duplicates: 2 * time.Minute, AckWait: 35 * time.Second},
```

- `TestLoadOverrides`, ngay sau dòng `Stream: publish.StreamConfig{Name: "CHATIM_EVT_IT", ...},`:

```go
		Work: work.StreamConfig{Name: "CHATIM_WORK_IT", SubjectRoot: "work_it", Partitions: 16, Replicas: 3, MaxAge: time.Hour, Duplicates: 3 * time.Minute, AckWait: 50 * time.Second},
```

(`AckWait` = `RECONCILE_DELAY` (5s mặc định, 20s ở overrides) + 30s; `Replicas` theo `EVT_STREAM_REPLICAS`.)

`apps/core/internal/config/validate_test.go`, trong bảng `TestLoadValidation`, thêm sau case `"duplicate window above max age"`:

```go
		{"lowercase work stream", map[string]string{"WORK_STREAM": "chatim_work"}, "WORK_STREAM must match"},
		{"work stream named like the event stream", map[string]string{"WORK_STREAM": "CHATIM_EVT"}, "WORK_STREAM must differ from EVT_STREAM"},
		{"dotted work root", map[string]string{"WORK_SUBJECT_ROOT": "work.x"}, "WORK_SUBJECT_ROOT must match"},
		{"work root equals the event root", map[string]string{"WORK_SUBJECT_ROOT": "evt"}, "WORK_SUBJECT_ROOT must differ"},
		{"work root equals the live root", map[string]string{"WORK_SUBJECT_ROOT": "live"}, "WORK_SUBJECT_ROOT must differ"},
		{"work partitions above slot count", map[string]string{"WORK_PARTITIONS": "1025"}, "WORK_*"},
		{"work partitions at slot count", map[string]string{"WORK_PARTITIONS": "1024"}, ""},
		{"work duplicate window above max age", map[string]string{"WORK_DUPLICATES": "3h"}, "WORK_*"},
```

**Step 7: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/config/..."`
Expected: FAIL biên dịch: `unknown field Work in struct literal of type config.Config`.

**Step 8: Config `Work` — code**

`apps/core/internal/config/config.go`: thêm import `"github.com/ivannguyendev/chatim/apps/core/internal/work"`; trong struct `Config`, ngay sau dòng `Stream              publish.StreamConfig` thêm:

```go
	Work                work.StreamConfig
```

`apps/core/internal/config/components.go`:
- thêm import `"github.com/ivannguyendev/chatim/apps/core/internal/work"` và hằng số trước `func (p *parser) components`:

```go
const workAckSlack = 30 * time.Second
```

- sau dòng `tick := p.span("SLOT_TICK", time.Second)` thêm:

```go
	delay := p.span("RECONCILE_DELAY", reconcile.DefaultDelay)
```

- ngay sau khối `c.Stream = publish.StreamConfig{...}` thêm:

```go
	c.Work = work.StreamConfig{
		Name:        envconfig.String("WORK_STREAM", "CHATIM_WORK"),
		SubjectRoot: envconfig.String("WORK_SUBJECT_ROOT", "work"),
		Partitions:  p.count("WORK_PARTITIONS", work.DefaultPartitions),
		Replicas:    c.Stream.Replicas,
		MaxAge:      p.span("WORK_MAX_AGE", work.DefaultMaxAge),
		Duplicates:  p.span("WORK_DUPLICATES", work.DefaultDuplicates),
		AckWait:     delay + workAckSlack,
	}
```

- trong khối `c.Reconcile = reconcile.Config{...}` thay dòng

```go
		Delay:           p.span("RECONCILE_DELAY", reconcile.DefaultDelay),
```

bằng:

```go
		Delay:           delay,
```

`apps/core/internal/config/validate.go`:
- trong `rules`, ngay sau dòng `{subjectRootPattern.MatchString(c.Stream.LiveRoot), ...},` thêm:

```go
		{streamNamePattern.MatchString(c.Work.Name), "WORK_STREAM must match " + streamNamePattern.String()},
		{subjectRootPattern.MatchString(c.Work.SubjectRoot), "WORK_SUBJECT_ROOT must match " + subjectRootPattern.String()},
		{c.Work.Name != c.Stream.Name, "WORK_STREAM must differ from EVT_STREAM"},
		{c.Work.SubjectRoot != c.Stream.SubjectRoot && c.Work.SubjectRoot != c.Stream.LiveRoot, "WORK_SUBJECT_ROOT must differ from EVT_SUBJECT_ROOT and EVT_LIVE_ROOT"},
```

- trong `componentErrors`, ngay sau dòng `{"EVT_*", c.Stream.Validate()},` thêm:

```go
		{"WORK_*, EVT_STREAM_REPLICAS, RECONCILE_DELAY", c.Work.Validate()},
```

(Chuỗi khoá chứa `WORK_*` nên các case `"WORK_*"` ở Step 6 khớp.)

**Step 9: Wiring + itest infra**

`apps/core/wiring.go`, trong `prepare`, thay dòng

```go
	return config.RedactError(publish.EnsureStream(ctx, cl.js, cfg.Stream), cfg.NATSURL)
```

bằng:

```go
	if err := publish.EnsureStream(ctx, cl.js, cfg.Stream); err != nil {
		return config.RedactError(err, cfg.NATSURL)
	}
	return config.RedactError(work.EnsureStream(ctx, cl.js, cfg.Work), cfg.NATSURL)
```

và thêm import `"github.com/ivannguyendev/chatim/apps/core/internal/work"`.

`apps/core/it_infra_test.go`:
- trong `coreConfig`, map `base`, sau dòng `"EVT_LIVE_ROOT": "itclive" + it.suffix,` thêm:

```go
		"WORK_STREAM":           "IT_WORK_" + strings.ToUpper(it.suffix),
		"WORK_SUBJECT_ROOT":     "itcwork" + it.suffix,
```

- trong `forget`, thay

```go
	if err := it.js.DeleteStream(ctx, cfg.Stream.Name); err != nil && !errors.Is(err, jetstream.ErrStreamNotFound) {
		t.Errorf("delete stream %s: %v", cfg.Stream.Name, err)
	}
```

bằng:

```go
	for _, name := range []string{cfg.Stream.Name, cfg.Work.Name} {
		if err := it.js.DeleteStream(ctx, name); err != nil && !errors.Is(err, jetstream.ErrStreamNotFound) {
			t.Errorf("delete stream %s: %v", name, err)
		}
	}
```

Lý do: nếu itest dùng tên mặc định `CHATIM_WORK`/`work.>`, nó đụng stream của `make core-up` (hai stream không được trùng subject) và để lại consumer rác.

`README.md`, bảng env, thêm dòng ngay sau dòng `| \`EVT_STREAM\`, ...`:

```markdown
| `WORK_STREAM`, `WORK_SUBJECT_ROOT`, `WORK_PARTITIONS`, `WORK_MAX_AGE`, `WORK_DUPLICATES` | `CHATIM_WORK` / `work` / `32` / `2h` / `2m` | Work stream WorkQueue (D79): subject `{root}.p{n}`, mỗi partition một durable pull consumer `work-p{n}` (`AckWait` = `RECONCILE_DELAY` + 30s, `MaxAckPending` 1024, replicas = `EVT_STREAM_REPLICAS`). Partition = `slot % WORK_PARTITIONS` (1–1024), do core giữ slot p tiêu thụ. Tên và gốc subject phải khác stream event; đổi số partition khi đang chạy phải xả hàng trước |
```

`INDEXES.csv`:
- dòng `apps/core/internal/work` (Task 5 tạo): nối vào purpose `; EnsureStream makes the WorkQueue stream (subjects {root}.>, file storage, MaxAge, Duplicates) and one durable pull consumer work-p{n} per partition (filter {root}.p{n}, explicit ack, AckWait, MaxAckPending 1024); Queue adapter over jetstream.Consumer.Fetch with FetchMaxWait: undecodable records are terminated and reported as BadRecordsError`; key_symbols thêm `StreamConfig;StreamConfig.Validate;ConsumerName;Admin;EnsureStream;Delivery;Queue;NewQueue;BadRecordsError;MaxAckPending;DefaultPartitions`; used_by thêm `apps/core;apps/core/internal/config`; tests thêm `itest (work queue on real NATS)`; decisions thêm `D79`.
- thêm dòng mới:

```csv
apps/core/internal/work/worktest,package,"In-memory work broker for tests: Publish(partition, record), Queue(partition) with Fetch up to max within wait (synctest clock), Ack/Nak (nak returns the record after its delay), Acked/Naked/Pending",Broker;Broker.Publish;Broker.Queue;Broker.Acked;Broker.Naked;Broker.Pending;ErrSettled,tests of effects,unit;synctest;goleak,D79
```

- dòng `apps/core/internal/config`: nối vào purpose `; WORK_* work stream (replicas from EVT_STREAM_REPLICAS, AckWait = RECONCILE_DELAY + 30s; names must differ from the event stream)`.
- dòng `apps/core`: nối vào purpose `; prepare ensures the work stream after the event stream`.

**Step 10: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/config/... ./apps/core/internal/work/... ./apps/core/"`
Expected: PASS.

Run: `make itest` (cần `make infra-up`)
Expected: PASS, gồm 3 itest `TestRealWork*` và các itest `apps/core` (core mới tạo thêm stream `IT_WORK_*`, `forget` xoá nó).

**Step 11: Commit**

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/work/ apps/core/internal/config/ apps/core/wiring.go apps/core/it_infra_test.go README.md INDEXES.csv
git commit -m "feat(work): ensure the work queue stream with a pull consumer per partition"
```

---
### Task 7: Reader — `reconcile` đẩy record vào work stream

`reconcile` đổi vai thành **reader** (D66, D79): mỗi change → một record nhỏ (`work.RecordOf`) publish lên subject partition của nó, qua đúng window có thứ tự + retry vô hạn đang có. Bỏ chờ delay, tra mark, dựng event, cache room type, cảnh báo lag; giữ term, checkpoint, confirm = prefix đã settle, leader slot 0, history lost, bỏ change hỏng. Vị trí xác nhận giờ là prefix record đã được work stream ack. Change có `Kind` lạ cũng bị bỏ (đếm `Dropped`) và vị trí vẫn đi qua nó.

Task này cũng sửa tối thiểu để mọi thứ còn xanh: config (`RECONCILE_DELAY` → `EffectDelay`, `RECONCILE_ROOM_CACHE` → `EffectRoomCache`; luật delay áp dụng luôn; luật mới `WORK_DUPLICATES`), wiring, metrics, itest. Từ commit này tới Task 12, **không còn ai bù event** cho tin rớt ở fast path (record nằm chờ trong work stream, chưa có worker); nhánh `feat/m2b` không deploy nên chấp nhận, Task 12 nối worker.

Guard tạm của Task 3 (`acked[i] || c.Kind == store.RoomInserted` trong `term.go`) biến mất vì `term.go` được viết lại toàn bộ; test tạm `room_change_test.go` của Task 3 bị xoá và thay bằng `TestForwardsARoomInsertAsARoomRecord`.

Itest `reconcile_integration_test.go` trước đây chờ event live do reconciler phát. Chọn: đổi sang khẳng định **record** `r:{room}` và `m:{room}-0-1` nằm trong work stream (đúng phần việc của reader). Khẳng định event live end-to-end quay lại ở Task 15 khi worker đã được nối (Task 12).

Metrics: `reconcile_lag_seconds` và `reconcile_republished_total` biến mất khỏi reader (Task 13 đưa lại từ worker); hai luật `ChatimReconcilerLagging`, `ChatimRepublishSurge` tạm không có dữ liệu tới Task 13 (`make alerts-check` vẫn xanh vì chỉ kiểm cú pháp).

**Files:**
- Modify (viết lại toàn bộ): `apps/core/internal/reconcile/config.go`, `reconciler.go`, `term.go`, `stats.go`
- Modify: `apps/core/internal/reconcile/term_checkpoint.go` (xoá `pause`)
- Delete: `apps/core/internal/reconcile/event.go`
- Delete: `apps/core/internal/reconcile/republish_test.go`, `apps/core/internal/reconcile/room_change_test.go` (test tạm của Task 3)
- Create: `apps/core/internal/reconcile/forward_test.go`
- Modify (viết lại toàn bộ): `apps/core/internal/reconcile/harness_test.go`, `checkpoint_test.go`, `term_test.go`, `stats_test.go`
- Modify: `apps/core/internal/config/config.go`, `components.go`, `validate.go`, `load_test.go`, `validate_test.go`
- Modify: `apps/core/wiring.go` (`reconcile.Deps`)
- Modify: `apps/core/metrics_wiring.go` (`reconcileSources`), `apps/core/metrics_wiring_test.go`
- Modify: `apps/core/reconcile_integration_test.go`
- Modify: `README.md`, `INDEXES.csv`

Caller của các chữ ký đổi (đã grep): `reconcile.Deps{... Rooms, Marks ...}` chỉ ở `apps/core/wiring.go` và `reconcile/harness_test.go`; `reconcile.Config.Delay/DuplicateWindow/RoomCache` và `reconcile.DefaultDelay/DefaultRoomCache` chỉ ở `config/components.go`, `config/load_test.go`; `reconcile.Stats.Lag/Republished` chỉ ở `apps/core/metrics_wiring.go` và `reconcile/stats_test.go`; `Reconciler.Dropped()` chỉ ở test reconcile. Kiểm lại: `grep -rn "reconcile\.\(Default\|Deps\|Config\|Stats\)\|\.Dropped()" apps tools --include='*.go'`.

**Step 1: Test reader**

Xoá test cũ:

```bash
git rm apps/core/internal/reconcile/republish_test.go apps/core/internal/reconcile/room_change_test.go
```

`apps/core/internal/reconcile/harness_test.go` (thay toàn bộ):

```go
package reconcile_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/reconcile"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/apps/core/internal/testlog"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

const (
	tenant           = "acme"
	room      uint64 = 4242
	otherRoom uint64 = 777
	tick             = time.Second

	historyLostMsg = "change feed history lost; restarting from now, changes in the gap need a resync"
	dropMsg        = "dropping change that cannot become a work record"
	failedMsg      = "work record publish failed; retrying"
)

var setup = reconcile.Config{
	SubjectRoot: "work", Partitions: 4, Window: 4, Batch: 8,
	ConfirmEvery: tick, Drain: tick, Poll: tick,
}

type owner struct{ leading atomic.Bool }

func (o *owner) Owns(slot uint16) bool { return slot == reconcile.LeaderSlot && o.leading.Load() }

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
	rooms *memstore.Rooms
	feed  *memstore.Feed
	base  int
	owner *owner
	js    *publishtest.JetStream
	sink  *testlog.Sink
}

func newRig(t *testing.T, wrap func(store.ChangeFeed) store.ChangeFeed) *rig {
	t.Helper()
	rg := &rig{msgs: memstore.NewMessages(), rooms: memstore.NewRooms(), owner: &owner{}, js: &publishtest.JetStream{}, sink: &testlog.Sink{}}
	rg.createRoom(t, room)
	rg.feed = memstore.NewFeed(rg.msgs, rg.rooms)
	rg.base, _ = rg.feed.Confirmed()
	rg.owner.leading.Store(true)
	var feed store.ChangeFeed = rg.feed
	if wrap != nil {
		feed = wrap(feed)
	}
	rec, err := reconcile.New(reconcile.Deps{Feed: feed, Owner: rg.owner, JS: rg.js}, setup, rg.sink.Logger())
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

func (rg *rig) createRoom(t *testing.T, id uint64) {
	t.Helper()
	created := time.Now()
	r := domain.Room{ID: id, Tenant: tenant, Type: domain.RoomGroup, Name: "team", CreatedBy: "alice", CreatedAt: created, MemberCount: 1}
	if err := rg.rooms.Create(t.Context(), r, []domain.Member{{Room: id, Tenant: tenant, User: "alice", Role: domain.RoleOwner, JoinedAt: created}}); err != nil {
		t.Fatalf("create room %d: %v", id, err)
	}
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

func (rg *rig) confirmed(t *testing.T) int {
	t.Helper()
	n, _ := rg.feed.Confirmed()
	return n - rg.base
}

func recordID(seq uint64) string {
	return work.Record{Kind: store.MessageInserted, Room: room, Seq: seq}.ID()
}

func roomRecordID(id uint64) string {
	return work.Record{Kind: store.RoomInserted, Room: id}.ID()
}

func recordIDs(seqs ...uint64) []string {
	out := make([]string, len(seqs))
	for i, s := range seqs {
		out[i] = recordID(s)
	}
	return out
}

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
```

(`rg.base` bù cho log chung của memstore Task 3: room tạo trước `NewFeed` có thể chiếm một vị trí trong log; `confirmed` chỉ đếm change sau mốc.)

`apps/core/internal/reconcile/forward_test.go`:

```go
package reconcile_test

import (
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

func TestForwardsAMessageInsertAtOnceAsARecordOnItsPartition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		committed := time.Now()
		rg.insert(t, room, 1)
		synctest.Wait()
		stored := rg.js.Stored()
		if len(stored) != 1 {
			t.Fatalf("stored = %v, want one record without any delay", storedIDs(rg.js))
		}
		if want := work.Subject(setup.SubjectRoot, work.Partition(room, setup.Partitions)); stored[0].Subject != want {
			t.Fatalf("subject = %q, want %q", stored[0].Subject, want)
		}
		got, err := work.Decode(stored[0].Data)
		if err != nil || got.Kind != store.MessageInserted || got.Room != room || got.Thread != 0 || got.Seq != 1 || !got.CommittedAt.Equal(committed) {
			t.Fatalf("record = %+v, %v; want message %d/0/1 committed at %v", got, err, room, committed)
		}
		if id := publishtest.MsgID(stored[0]); id != got.ID() || id != recordID(1) {
			t.Fatalf("msg id = %q, want %q", id, recordID(1))
		}
	})
}

func TestForwardsARoomInsertAsARoomRecord(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.createRoom(t, otherRoom)
		synctest.Wait()
		stored := rg.js.Stored()
		if got := storedIDs(rg.js); !slices.Equal(got, []string{roomRecordID(otherRoom)}) {
			t.Fatalf("stored = %v, want only %s", got, roomRecordID(otherRoom))
		}
		if want := work.Subject(setup.SubjectRoot, work.Partition(otherRoom, setup.Partitions)); stored[0].Subject != want {
			t.Fatalf("subject = %q, want %q", stored[0].Subject, want)
		}
		if got, err := work.Decode(stored[0].Data); err != nil || got.Kind != store.RoomInserted || got.Room != otherRoom {
			t.Fatalf("record = %+v, %v; want a room record for %d", got, err, otherRoom)
		}
	})
}

func TestConfirmWaitsForTheAck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.js.Hold()
		rg.insert(t, room, 1)
		time.Sleep(2 * tick)
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
		time.Sleep(2 * tick)
		synctest.Wait()
		if got := attemptIDs(rg.js); len(got) < 2 {
			t.Fatalf("attempts = %v, want the refused publish resent", got)
		}
		if got := storedIDs(rg.js); !slices.Equal(got, recordIDs(1)) {
			t.Fatalf("stored = %v, want %s once", got, recordID(1))
		}
		if got := rg.confirmed(t); got != 1 {
			t.Fatalf("confirmed = %d, want 1", got)
		}
	})
}

func TestFullWindowWaitsForTheHead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.js.Hold()
		rg.insert(t, room, 1, 2, 3, 4, 5, 6)
		time.Sleep(tick)
		synctest.Wait()
		if got := len(rg.js.Attempts()); got != setup.Window {
			t.Fatalf("attempts with a full window = %d, want %d", got, setup.Window)
		}
		rg.js.Release()
		time.Sleep(tick)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, recordIDs(1, 2, 3, 4, 5, 6)) {
			t.Fatalf("stored after release = %v, want seq 1..6 in order", got)
		}
	})
}

func TestPersistentRefusalIsLoggedAndRetried(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.js.RefuseWhen(func(*nats.Msg) error { return errors.New("no responders") })
		rg.insert(t, room, 1)
		time.Sleep(4 * tick)
		synctest.Wait()
		attempts := attemptIDs(rg.js)
		if len(attempts) < 3 {
			t.Fatalf("attempts = %v, want the refused publish retried", attempts)
		}
		if got := rg.sink.Count(failedMsg); got < 1 || got > len(attempts) {
			t.Fatalf("%q logged %d times for %d attempts, want at least once and at most once per attempt", failedMsg, got, len(attempts))
		}
		if got := rg.confirmed(t); got != 0 || len(rg.js.Stored()) != 0 {
			t.Fatalf("confirmed = %d, stored = %v; want nothing past a refused publish", got, storedIDs(rg.js))
		}
	})
}
```

`apps/core/internal/reconcile/checkpoint_test.go` (thay toàn bộ):

```go
package reconcile_test

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type corruptFeed struct {
	store.ChangeFeed
	corrupt atomic.Int32
}

func (f *corruptFeed) Open(ctx context.Context) (store.Cursor, error) {
	cur, err := f.ChangeFeed.Open(ctx)
	if err != nil {
		return nil, err
	}
	return &corruptCursor{Cursor: cur, feed: f}, nil
}

type corruptCursor struct {
	store.Cursor
	feed *corruptFeed
}

func (c *corruptCursor) Next(ctx context.Context) (store.Change, error) {
	ch, err := c.Cursor.Next(ctx)
	if err == nil && c.feed.corrupt.Add(-1) >= 0 {
		return store.Change{}, store.ErrCorruptChange
	}
	return ch, err
}

type kindlessFeed struct {
	store.ChangeFeed
	strip atomic.Int32
}

func (f *kindlessFeed) Open(ctx context.Context) (store.Cursor, error) {
	cur, err := f.ChangeFeed.Open(ctx)
	if err != nil {
		return nil, err
	}
	return &kindlessCursor{Cursor: cur, feed: f}, nil
}

type kindlessCursor struct {
	store.Cursor
	feed *kindlessFeed
}

func (c *kindlessCursor) Next(ctx context.Context) (store.Change, error) {
	ch, err := c.Cursor.Next(ctx)
	if err == nil && c.feed.strip.Add(-1) >= 0 {
		ch.Kind = 0
	}
	return ch, err
}

func TestLostLeadershipWhileTheWindowIsFullEndsTheTerm(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.insert(t, room, 1)
		time.Sleep(2 * tick)
		synctest.Wait()
		if got := rg.confirmed(t); got != 1 {
			t.Fatalf("confirmed before the window fills = %d, want 1", got)
		}
		rg.js.Hold()
		rg.insert(t, room, 2, 3, 4, 5, 6, 7)
		time.Sleep(tick)
		synctest.Wait()
		inFlight := 1 + setup.Window
		if got := len(rg.js.Attempts()); got != inFlight {
			t.Fatalf("attempts with a full window = %d, want %d", got, inFlight)
		}
		rg.owner.leading.Store(false)
		time.Sleep(3 * tick)
		synctest.Wait()
		rg.js.Release()
		time.Sleep(2 * tick)
		synctest.Wait()
		if got := len(rg.js.Attempts()); got != inFlight {
			t.Fatalf("attempts after losing the lead = %d, want %d", got, inFlight)
		}
		rg.owner.leading.Store(true)
		time.Sleep(3 * tick)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, recordIDs(1, 2, 3, 4, 5, 6, 7)) {
			t.Fatalf("stored after regaining the lead = %v, want seq 1..7", got)
		}
		if got := rg.confirmed(t); got != 7 {
			t.Fatalf("confirmed after regaining the lead = %d, want 7", got)
		}
	})
}

func TestCorruptChangeIsDroppedAndTheNextForwarded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		corrupt := &corruptFeed{}
		corrupt.corrupt.Store(1)
		rg := newRig(t, func(f store.ChangeFeed) store.ChangeFeed {
			corrupt.ChangeFeed = f
			return corrupt
		}).start(t)
		synctest.Wait()
		rg.insert(t, room, 1, 2)
		time.Sleep(2 * tick)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, recordIDs(2)) {
			t.Fatalf("stored = %v, want only %s", got, recordID(2))
		}
		if d := rg.Stats().Dropped; d != 1 || rg.sink.Count(dropMsg) != 1 {
			t.Fatalf("dropped = %d, logged %d; want 1 and 1", d, rg.sink.Count(dropMsg))
		}
		if got := rg.confirmed(t); got != 2 {
			t.Fatalf("confirmed = %d, want 2", got)
		}
	})
}

func TestChangeOfUnknownKindIsDroppedAndConfirmedPast(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		kindless := &kindlessFeed{}
		kindless.strip.Store(1)
		rg := newRig(t, func(f store.ChangeFeed) store.ChangeFeed {
			kindless.ChangeFeed = f
			return kindless
		}).start(t)
		synctest.Wait()
		rg.insert(t, room, 1, 2)
		time.Sleep(2 * tick)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, recordIDs(2)) {
			t.Fatalf("stored = %v, want only %s", got, recordID(2))
		}
		if d := rg.Stats().Dropped; d != 1 {
			t.Fatalf("dropped = %d, want 1", d)
		}
		if got := rg.confirmed(t); got != 2 {
			t.Fatalf("confirmed = %d, want 2", got)
		}
	})
}
```

`apps/core/internal/reconcile/term_test.go` (thay toàn bộ):

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
		time.Sleep(3 * tick)
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
		time.Sleep(2 * tick)
		synctest.Wait()
		rg.owner.leading.Store(false)
		time.Sleep(2 * tick)
		synctest.Wait()
		rg.insert(t, room, 2)
		rg.owner.leading.Store(true)
		time.Sleep(3 * tick)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, recordIDs(1, 2)) {
			t.Fatalf("stored = %v, want %s then %s", got, recordID(1), recordID(2))
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
		time.Sleep(2 * tick)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, recordIDs(1)) {
			t.Fatalf("stored = %v, want %s", got, recordID(1))
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
		time.Sleep(2 * tick)
		synctest.Wait()
		if got := storedIDs(rg.js); !slices.Equal(got, recordIDs(1)) {
			t.Fatalf("stored = %v, want %s after the feed frees up", got, recordID(1))
		}
	})
}

func TestCloseSettlesInFlightPublishes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.js.Hold()
		rg.insert(t, room, 1)
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
```

`apps/core/internal/reconcile/stats_test.go` (thay toàn bộ):

```go
package reconcile_test

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestStatsTrackTermsAndForwards(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		rg.insert(t, room, 1, 2)
		rg.createRoom(t, otherRoom)
		time.Sleep(tick)
		synctest.Wait()
		if s := rg.Stats(); !s.Running || s.Terms != 1 || s.Forwarded != 3 || s.Dropped != 0 {
			t.Fatalf("stats = %+v, want running, 1 term, 3 forwarded, 0 dropped", s)
		}
		rg.owner.leading.Store(false)
		time.Sleep(2 * tick)
		synctest.Wait()
		if s := rg.Stats(); s.Running {
			t.Fatalf("stats after losing the lead = %+v, want not running", s)
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

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/reconcile/..."`
Expected: FAIL biên dịch: `unknown field Partitions in struct literal of type reconcile.Config`, `s.Forwarded undefined`.

**Step 3: Code reader**

```bash
git rm apps/core/internal/reconcile/event.go
```

`apps/core/internal/reconcile/config.go` (thay toàn bộ):

```go
package reconcile

import (
	"cmp"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const (
	LeaderSlot uint16 = 0

	DefaultWindow       = 1024
	DefaultBatch        = 256
	DefaultConfirmEvery = time.Second
	DefaultDrain        = time.Second
	DefaultPoll         = time.Second
)

type Config struct {
	SubjectRoot  string
	Partitions   int
	Window       int
	Batch        int
	ConfirmEvery time.Duration
	Drain        time.Duration
	Poll         time.Duration
}

func (c Config) Validate() error { return c.withDefaults().validate() }

func (c Config) withDefaults() Config {
	c.Partitions = cmp.Or(c.Partitions, work.DefaultPartitions)
	c.Window = cmp.Or(c.Window, DefaultWindow)
	c.Batch = cmp.Or(c.Batch, DefaultBatch)
	c.ConfirmEvery = cmp.Or(c.ConfirmEvery, DefaultConfirmEvery)
	c.Drain = cmp.Or(c.Drain, DefaultDrain)
	c.Poll = cmp.Or(c.Poll, DefaultPoll)
	return c
}

func (c Config) validate() error {
	switch {
	case c.SubjectRoot == "":
		return fmt.Errorf("%w: reconcile needs the work subject root", apperr.ErrInvalidArgument)
	case c.Partitions <= 0 || c.Partitions > slotmap.Count:
		return fmt.Errorf("%w: reconcile partitions %d must be between 1 and the slot count %d", apperr.ErrInvalidArgument, c.Partitions, slotmap.Count)
	case c.ConfirmEvery <= 0 || c.Drain <= 0 || c.Poll <= 0 || c.Window <= 0 || c.Batch <= 0:
		return fmt.Errorf("%w: reconcile config %+v must be positive", apperr.ErrInvalidArgument, c)
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

`apps/core/internal/reconcile/reconciler.go` (thay toàn bộ):

```go
package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const (
	historyLostMsg = "change feed history lost; restarting from now, changes in the gap need a resync"
	dropMsg        = "dropping change that cannot become a work record"
	failedMsg      = "work record publish failed; retrying"
)

var (
	errStarted  = errors.New("reconciler already started")
	errStopped  = errors.New("reconciler stopping")
	errLostLead = errors.New("reconciler no longer owns the leader slot")
)

type Owner interface {
	Owns(slot uint16) bool
}

type Deps struct {
	Feed  store.ChangeFeed
	Owner Owner
	JS    publish.JetStream
}

type Reconciler struct {
	deps    Deps
	cfg     Config
	log     *slog.Logger
	drops   limitedLog
	fails   limitedLog
	stats   counters
	started atomic.Bool
	stop    chan struct{}
	halt    sync.Once
	done    chan struct{}
}

func New(deps Deps, cfg Config, log *slog.Logger) (*Reconciler, error) {
	if deps.Feed == nil || deps.Owner == nil || deps.JS == nil {
		return nil, fmt.Errorf("%w: reconciler needs a feed, an owner and a jetstream client", apperr.ErrInvalidArgument)
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
		drops: limitedLog{log: log}, fails: limitedLog{log: log},
		stop: make(chan struct{}), done: make(chan struct{}),
	}, nil
}

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
	r.log.InfoContext(ctx, "reconcile term started")
	r.termStarted()
	defer r.termEnded()
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
		r.stats.historyLost.Add(1)
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

func (r *Reconciler) drop(ctx context.Context, err error) {
	r.stats.dropped.Add(1)
	r.drops.warn(ctx, dropMsg, "err", err)
}

func (r *Reconciler) publishFailed(err error) {
	r.fails.warn(context.Background(), failedMsg, "err", err)
}
```

Log `reconcile term started` giữ nguyên chữ: itest dùng nó làm tín hiệu.

`apps/core/internal/reconcile/term.go` (thay toàn bộ; guard `c.Kind == store.RoomInserted` của Task 3 không còn):

```go
package reconcile

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

var errReaderStopped = errors.New("change feed reader stopped")

type term struct {
	r         *Reconciler
	cur       store.Cursor
	win       *window
	changes   chan store.Change
	failed    chan error
	confirmed store.Position
	checked   time.Time
}

func newTerm(r *Reconciler, cur store.Cursor) *term {
	return &term{
		r: r, cur: cur,
		win:     newWindow(r.deps.JS, r.cfg.Window, r.cfg.Poll, r.publishFailed),
		changes: make(chan store.Change, r.cfg.Batch),
		failed:  make(chan error, 1),
		checked: time.Now(),
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
		select {
		case c, open := <-t.changes:
			if !open {
				return t.readFailure()
			}
			if err := t.forward(ctx, c); err != nil {
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
			t.r.drop(ctx, err)
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

func (t *term) forward(ctx context.Context, c store.Change) error {
	if c.Kind != store.MessageInserted && c.Kind != store.RoomInserted {
		t.r.drop(ctx, fmt.Errorf("%w: unknown change kind %d", store.ErrCorruptChange, c.Kind))
		t.win.done(c.Position)
		return nil
	}
	if err := t.makeRoom(ctx); err != nil {
		return err
	}
	t.win.send(c.Position, work.Message(t.r.cfg.SubjectRoot, t.r.cfg.Partitions, work.RecordOf(c)))
	t.r.stats.forwarded.Add(1)
	t.win.collect()
	return nil
}
```

`apps/core/internal/reconcile/term_checkpoint.go`: xoá nguyên hàm `pause` (không còn ai gọi); `checkpoint`, `checkpointIfDue`, `makeRoom`, `settle`, `confirm` giữ nguyên.

`apps/core/internal/reconcile/stats.go` (thay toàn bộ):

```go
package reconcile

import "sync/atomic"

type Stats struct {
	Running     bool
	Terms       uint64
	Forwarded   uint64
	Dropped     uint64
	HistoryLost uint64
}

type counters struct {
	running     atomic.Bool
	terms       atomic.Uint64
	forwarded   atomic.Uint64
	dropped     atomic.Uint64
	historyLost atomic.Uint64
}

func (r *Reconciler) Stats() Stats {
	return Stats{
		Running:     r.stats.running.Load(),
		Terms:       r.stats.terms.Load(),
		Forwarded:   r.stats.forwarded.Load(),
		Dropped:     r.stats.dropped.Load(),
		HistoryLost: r.stats.historyLost.Load(),
	}
}

func (r *Reconciler) termStarted() {
	r.stats.terms.Add(1)
	r.stats.running.Store(true)
}

func (r *Reconciler) termEnded() { r.stats.running.Store(false) }
```

`window.go`, `limited_log.go` không đổi.

**Step 4: Chạy reader, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/reconcile/..."`
Expected: PASS 5 lần. `wc -l apps/core/internal/reconcile/*.go` đều < 200. Package `apps/core/internal/config` và `apps/core` lúc này chưa biên dịch (bước sau sửa).

**Step 5: Config — test**

`apps/core/internal/config/load_test.go`:
- `TestLoadDefaults`: thay khối

```go
		Reconcile: reconcile.Config{
			SubjectRoot: "evt", Delay: 5 * time.Second, DuplicateWindow: 5 * time.Minute, Window: 1024, Batch: 256,
			ConfirmEvery: time.Second, Drain: time.Second, Poll: time.Second, RoomCache: 65536,
		},
```

bằng:

```go
		Reconcile: reconcile.Config{
			SubjectRoot: "work", Partitions: 32, Window: 1024, Batch: 256,
			ConfirmEvery: time.Second, Drain: time.Second, Poll: time.Second,
		},
		EffectDelay:     5 * time.Second,
		EffectRoomCache: 65536,
```

- `TestLoadOverrides`: thay khối

```go
		Reconcile: reconcile.Config{
			SubjectRoot: "evt_it", Delay: 20 * time.Second, DuplicateWindow: 5 * time.Minute, Window: 64, Batch: 32,
			ConfirmEvery: 2 * time.Second, Drain: 500 * time.Millisecond, Poll: 500 * time.Millisecond, RoomCache: 128,
		},
```

bằng:

```go
		Reconcile: reconcile.Config{
			SubjectRoot: "work_it", Partitions: 16, Window: 64, Batch: 32,
			ConfirmEvery: 2 * time.Second, Drain: 500 * time.Millisecond, Poll: 500 * time.Millisecond,
		},
		EffectDelay:     20 * time.Second,
		EffectRoomCache: 128,
```

`apps/core/internal/config/validate_test.go`, trong bảng `TestLoadValidation`:
- thay case

```go
		{"reconcile disabled ignores its delay", map[string]string{"RECONCILE_ENABLED": "false", "RECONCILE_DELAY": "5m"}, ""},
```

bằng:

```go
		{"effect delay rule holds with the reader off", map[string]string{"RECONCILE_ENABLED": "false", "RECONCILE_DELAY": "5m"}, "RECONCILE_DELAY must be shorter than EVT_STREAM_DUPLICATES"},
```

- thay case

```go
		{"reconcile disabled ignores the ack mark deadline", map[string]string{"RECONCILE_ENABLED": "false", "RECONCILE_DELAY": "1s"}, ""},
```

bằng:

```go
		{"ack mark deadline holds with the reader off", map[string]string{"RECONCILE_ENABLED": "false", "RECONCILE_DELAY": "1s"}, "RECONCILE_DELAY must be longer than PUB_ACK_TIMEOUT plus the ack mark window and timeout"},
		{"work duplicates within confirm plus drain", map[string]string{"WORK_DUPLICATES": "2s"}, "WORK_DUPLICATES must be longer than RECONCILE_CONFIRM_EVERY + RECONCILE_DRAIN"},
		{"work duplicates just past confirm plus drain", map[string]string{"WORK_DUPLICATES": "2001ms"}, ""},
```

`env_test.go` không đổi: `RECONCILE_DELAY` và `RECONCILE_ROOM_CACHE` vẫn là env (giờ đổ vào `EffectDelay`, `EffectRoomCache`).

**Step 6: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/config/..."`
Expected: FAIL biên dịch: `unknown field Delay in struct literal of type reconcile.Config` (ở `components.go`) và `unknown field EffectDelay`.

**Step 7: Config — code**

`apps/core/internal/config/config.go`, struct `Config`, ngay sau dòng `Reconcile           reconcile.Config` thêm:

```go
	EffectDelay         time.Duration
	EffectRoomCache     int
```

`apps/core/internal/config/components.go`:
- thay `const workAckSlack = 30 * time.Second` (Task 6) bằng:

```go
const (
	defaultEffectDelay     = 5 * time.Second
	defaultEffectRoomCache = 65536
	workAckSlack           = 30 * time.Second
)
```

- thay dòng `delay := p.span("RECONCILE_DELAY", reconcile.DefaultDelay)` bằng:

```go
	delay := p.span("RECONCILE_DELAY", defaultEffectDelay)
```

- thay toàn bộ khối `c.Reconcile = reconcile.Config{...}` (gồm dòng `Delay: delay,` của Task 6, `DuplicateWindow`, `RoomCache`) bằng:

```go
	c.EffectDelay = delay
	c.EffectRoomCache = p.count("RECONCILE_ROOM_CACHE", defaultEffectRoomCache)
	c.Reconcile = reconcile.Config{
		SubjectRoot:  c.Work.SubjectRoot,
		Partitions:   c.Work.Partitions,
		Window:       p.count("RECONCILE_WINDOW", reconcile.DefaultWindow),
		Batch:        p.count("RECONCILE_BATCH", reconcile.DefaultBatch),
		ConfirmEvery: p.span("RECONCILE_CONFIRM_EVERY", reconcile.DefaultConfirmEvery),
		Drain:        p.span("RECONCILE_DRAIN", reconcile.DefaultDrain),
		Poll:         tick,
	}
```

(`subjectRoot` vẫn dùng cho `Publish` và `Stream`.)

`apps/core/internal/config/validate.go`, trong `rules`, thay hai dòng

```go
		{!c.ReconcileEnabled || c.Reconcile.Delay < c.Stream.Duplicates, "RECONCILE_DELAY must be shorter than EVT_STREAM_DUPLICATES"},
		{!c.ReconcileEnabled || c.Reconcile.Delay > publish.MarkDeadline(c.Publish.AckTimeout), "RECONCILE_DELAY must be longer than PUB_ACK_TIMEOUT plus the ack mark window and timeout"},
```

bằng:

```go
		{c.EffectDelay < c.Stream.Duplicates, "RECONCILE_DELAY must be shorter than EVT_STREAM_DUPLICATES"},
		{c.EffectDelay > publish.MarkDeadline(c.Publish.AckTimeout), "RECONCILE_DELAY must be longer than PUB_ACK_TIMEOUT plus the ack mark window and timeout"},
		{c.Work.Duplicates > c.Reconcile.ConfirmEvery+c.Reconcile.Drain, "WORK_DUPLICATES must be longer than RECONCILE_CONFIRM_EVERY + RECONCILE_DRAIN"},
```

Lý do luật mới: reader mới lên term đọc lại từ vị trí đã confirm, nên gửi lại tối đa phần chưa confirm (≤ một nhịp confirm + drain của term cũ); cửa sổ chống trùng của work stream phải phủ được khoảng đó để record gửi lại bị bỏ theo id.

**Step 8: Wiring, metrics, itest**

`apps/core/wiring.go`, trong `wire`, thay

```go
			Feed: mongostore.NewFeed(cl.mongo.Database(cfg.MongoDB)), Rooms: st, Marks: marks, Owner: slots, JS: cl.reconcileJS,
```

bằng (giữ nguyên lời gọi `NewFeed` như Task 4 để lại, chỉ bỏ `Rooms`, `Marks`):

```go
			Feed: mongostore.NewFeed(cl.mongo.Database(cfg.MongoDB)), Owner: slots, JS: cl.reconcileJS,
```

(`marks` vẫn dùng cho publisher; `st` vẫn dùng cho router/grpcsrv.)

`apps/core/metrics_wiring.go`, thay toàn bộ hàm `reconcileSources` bằng:

```go
func reconcileSources(stats func() reconcile.Stats) []metrics.Source {
	return []metrics.Source{
		{Name: "reconcile_running", Help: "1 while this core runs a reader term.", Gauge: true, Read: func() float64 { return flag(stats().Running) }},
		{Name: "reconcile_terms_total", Help: "Reader terms started on this core.", Read: func() float64 { return float64(stats().Terms) }},
		{Name: "reconcile_forwarded_total", Help: "Changes the reader sent to the work stream.", Read: func() float64 { return float64(stats().Forwarded) }},
		{Name: "reconcile_dropped_total", Help: "Changes that could not become work records.", Read: func() float64 { return float64(stats().Dropped) }},
		{Name: "reconcile_history_lost_total", Help: "Times the change feed position fell out of the oplog.", Read: func() float64 { return float64(stats().HistoryLost) }},
	}
}
```

`apps/core/metrics_wiring_test.go`, trong `want` của `TestCoreMetricSourcesCoverEveryGuarantee`, thay

```go
		"reconcile_running", "reconcile_lag_seconds", "reconcile_terms_total", "reconcile_republished_total",
```

bằng:

```go
		"reconcile_running", "reconcile_terms_total", "reconcile_forwarded_total",
```

`apps/core/reconcile_integration_test.go`:
- đổi tên test `TestRealInfraReconcilerPublishesWritesThatSkippedTheCore` → `TestRealInfraReaderForwardsWritesThatSkippedTheCore`.
- xoá đoạn subscribe live (từ `live := make(chan *nats.Msg, 16)` tới `t.Cleanup(func() { _ = sub.Unsubscribe() })`).
- dời khối `select { case <-termStarted: ... case <-time.After(30 * time.Second): ... }` lên **ngay sau** `awaitReady(t, cfg, core)`, trước `roomID := createRoom(...)`, và đổi thông điệp thành `"reader did not start a term within 30s"`. Nhờ vậy record `r:{room}` được đẩy ngay khi room được tạo, không bị đẩy trễ gần `RECONCILE_DELAY` (từ Task 12 worker ack và WorkQueue xoá record sau delay).
- thay đoạn cuối (từ `want := pbconv.MessageEventID(room, 0, 1)` tới hết vòng `for`) bằng:

```go
	want := []string{
		work.Record{Kind: store.RoomInserted, Room: room}.ID(),
		work.Record{Kind: store.MessageInserted, Room: room, Seq: 1}.ID(),
	}
	awaitRecords(t, it, cfg.Work.Name, want)
	t.Logf("records %v reached the work stream %v after the insert", want, time.Since(inserted))
}

func awaitRecords(t *testing.T, it *itInfra, stream string, want []string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	poll := time.NewTicker(250 * time.Millisecond)
	defer poll.Stop()
	seen := map[string]bool{}
	for {
		got := it.streamIDs(t, stream)
		for id, n := range got {
			if n > 0 {
				seen[id] = true
			}
		}
		missing := slices.DeleteFunc(slices.Clone(want), func(id string) bool { return seen[id] })
		if len(missing) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("work stream %s lacks %v 30s after the insert; has %v", stream, missing, got)
		}
		<-poll.C
	}
}
```

- import: bỏ `github.com/nats-io/nats.go`, `github.com/nats-io/nats.go/jetstream`, `.../pbconv`; thêm `"slices"` và `"github.com/ivannguyendev/chatim/apps/core/internal/work"`.

Record `r:{room}` vào được vì `CreateRoom` ghi `rooms` sau mốc bootstrap của feed cấp database (Task 4). Chưa có worker nên record nằm yên trong work stream; `forget` xoá stream ở cleanup.

`README.md`, bảng env, thay 5 dòng `RECONCILE_*` như sau:
- `| \`RECONCILE_ENABLED\` | ...` → `| \`RECONCILE_ENABLED\` | \`true\` | Bật reader trên core giữ slot 0: đọc nhật ký commit, đẩy record vào work stream (D66, D79). Tắt thì publisher vẫn ghi mark (D52) |`
- `| \`RECONCILE_DELAY\` | ...` → `| \`RECONCILE_DELAY\` | \`5s\` | D của effect \`msg_created\`/\`room_created\`: worker chạy effect sau \`CommittedAt + D\`; phải dài hơn \`PUB_ACK_TIMEOUT\` + 10ms + 1s (cửa sổ và timeout của mark) và ngắn hơn \`EVT_STREAM_DUPLICATES\`, kiểm cả khi \`RECONCILE_ENABLED=false\`; với mặc định 5s, \`PUB_ACK_TIMEOUT\` phải dưới khoảng 3,99s |`
- `| \`RECONCILE_WINDOW\`, \`RECONCILE_BATCH\` | ...` → `| \`RECONCILE_WINDOW\`, \`RECONCILE_BATCH\` | \`1024\` / \`256\` | Số record đang bay tối đa từ reader tới work stream (JetStream client riêng); bộ đệm change giữa cursor và reader |`
- `| \`RECONCILE_CONFIRM_EVERY\` | ...` → `| \`RECONCILE_CONFIRM_EVERY\` | \`1s\` | Nhịp lưu vị trí đã xác nhận (prefix record work stream đã ack) và kiểm lại slot 0; \`WORK_DUPLICATES\` phải dài hơn \`RECONCILE_CONFIRM_EVERY + RECONCILE_DRAIN\` |`
- `| \`RECONCILE_ROOM_CACHE\` | ...` → `| \`RECONCILE_ROOM_CACHE\` | \`65536\` | Số room giữ \`room_type\` trong cache của effect \`msg_created\` |`

`INDEXES.csv`, thay dòng `apps/core/internal/reconcile` bằng:

```csv
apps/core/internal/reconcile,package,"Reader of the commit log, runs only on the slot 0 owner: one term per lead opens store.ChangeFeed (logs reconcile term started); turns every MessageInserted/RoomInserted change into a key-only work record (work.RecordOf) published at once on its partition subject with Nats-Msg-Id = record id, on its own JetStream client; in-order window of RECONCILE_WINDOW with unbounded retry; confirms the position only past changes the work stream acked, every RECONCILE_CONFIRM_EVERY, never blocking longer without a checkpoint; corrupt or unknown-kind changes are dropped and confirmed past; history lost -> error log + Forget; exposes Stats (running, terms, forwarded, dropped, history lost)",Reconciler;New;Reconciler.Run;Reconciler.Close;Deps;Config;Config.JetStreamOptions;Owner;LeaderSlot;Stats;Reconciler.Stats,apps/core;apps/core/internal/config,unit;synctest;goleak;itest (apps/core),D51;D52;D66;D76;D79;D80
```

Và: dòng `apps/core/internal/config` thay `RECONCILE_DELAY < EVT_STREAM_DUPLICATES` bằng `RECONCILE_DELAY (EffectDelay, always checked) < EVT_STREAM_DUPLICATES; RECONCILE_ROOM_CACHE -> EffectRoomCache; WORK_DUPLICATES > RECONCILE_CONFIRM_EVERY + RECONCILE_DRAIN`; dòng `apps/core` đổi tests `itest (reconciler publishes a message written straight to Mongo)` → `itest (reader forwards a room and a message written straight to Mongo to the work stream)`; dòng `apps/core/internal/eventmark` đổi used_by `apps/core/internal/reconcile (Marks)` → `apps/core/internal/effects (Marks, Task 9)`.

**Step 9: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/reconcile/... ./apps/core/internal/config/... ./apps/core/"`
Expected: PASS. `wc -l apps/core/internal/config/*.go apps/core/*.go` đều < 200.

Run: `make itest`
Expected: PASS, gồm `TestRealInfraReaderForwardsWritesThatSkippedTheCore` (log `records [...] reached the work stream ...`), `TestRealMongoFeed*` và itest dừng dưới tải.

**Step 10: Commit**

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/reconcile/ apps/core/internal/config/ apps/core/wiring.go apps/core/metrics_wiring.go apps/core/metrics_wiring_test.go apps/core/reconcile_integration_test.go README.md INDEXES.csv
git commit -m "refactor(reconcile): forward every committed change as a work record"
```

Thân commit ghi: event bị rớt ở fast path chưa được bù tới khi worker được nối (Task 12); `reconcile_lag_seconds`/`reconcile_republished_total` quay lại từ worker ở Task 13.

---
### Task 8: Package `effects` — registry + worker loop

Worker trên mọi core (D66 bước 3, D79): mỗi partition một goroutine, chỉ fetch khi core giữ slot cùng số (`Owns(uint16(p))`, kiểm trước **mỗi** `Fetch`). Một lô fetch về được nhóm theo `Kind` (thứ tự `ChangeKind` tăng dần); mỗi nhóm chạy lần lượt các effect của kind đó theo thứ tự trong registry, trước mỗi effect chờ tới `max(CommittedAt) + Delay` của nhóm. Record được `Ack` khi mọi effect của nó trả nil; có lỗi thì `Nak(RetryDelay)` và đếm `Failed`. Nhóm nào xong thì settle ngay, không chờ cả lô.

Quyết định vòng đời:
- `Close` dừng fetch ngay (ctx của `Fetch` bị huỷ khi `Close`), để effect **đang chạy** chạy xong trong ctx của `Run`, rồi trả về. Record còn đang **chờ delay** lúc `Close` được trả bằng `Nak(0)` để chủ slot kế tiếp nhận ngay, không phải đợi `AckWait`. Lô fetch về sau khi đã `Close` cũng `Nak(0)`.
- Effect trả lỗi khi ctx của `Run` đã bị huỷ: record lỗi `Nak(0)`, không đếm `Failed` (đó là dừng, không phải lỗi effect).
- `work.BadRecordsError` từ `Fetch` cộng `Terminated` vào `Failed`; mọi lỗi fetch log tối đa 1 lần/giây; lỗi không kèm delivery thì nghỉ `Poll` rồi thử lại.
- `Lag` của partition = max qua các effect của lô vừa chạy của `now − (CommittedAt cũ nhất + Delay)`; về 0 khi fetch rỗng hoặc partition không thuộc core này. `Stats().Lag` = max theo partition.
- `Config.Drain` không dùng bên trong worker: nó là giới hạn bước dừng `workers` trong `StopPlan` (Task 12).

**Files:**
- Create: `apps/core/internal/effects/effect.go`
- Create: `apps/core/internal/effects/workers.go`
- Create: `apps/core/internal/effects/partition.go`
- Create: `apps/core/internal/effects/batch.go`
- Create: `apps/core/internal/effects/limited_log.go`
- Create: `apps/core/internal/effects/harness_test.go`
- Create: `apps/core/internal/effects/workers_test.go`
- Create: `apps/core/internal/effects/failures_test.go`
- Create: `apps/core/internal/effects/lifecycle_test.go`
- Modify: `INDEXES.csv`

**Step 1: Test**

`apps/core/internal/effects/harness_test.go`:

```go
package effects_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/testlog"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/apps/core/internal/work/worktest"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

const (
	delay = 5 * time.Second
	retry = 3 * time.Second
	wait  = time.Second
	tick  = time.Second

	fetchFailedMsg = "work fetch failed; retrying"
)

var (
	errBoom = errors.New("boom")
	setup   = effects.Config{Partitions: 4, FetchBatch: 8, FetchWait: wait, RetryDelay: retry, Drain: tick, Poll: tick}
)

type owner struct {
	mu    sync.Mutex
	slots map[uint16]bool
}

func (o *owner) Owns(slot uint16) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.slots[slot]
}

func (o *owner) set(slot uint16, owned bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.slots[slot] = owned
}

type journal struct {
	mu      sync.Mutex
	entries []string
}

func (j *journal) add(s string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.entries = append(j.entries, s)
}

func (j *journal) list() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.entries)
}

type recorder struct {
	name    string
	journal *journal
	block   chan struct{}
	mu      sync.Mutex
	calls   [][]work.Record
	at      []time.Time
	fail    map[uint64]bool
}

func (r *recorder) effect(d time.Duration) effects.Effect {
	return effects.Effect{Name: r.name, Delay: d, Run: r.run}
}

func (r *recorder) run(ctx context.Context, recs []work.Record) []error {
	if r.block != nil {
		select {
		case <-r.block:
		case <-ctx.Done():
		}
	}
	if r.journal != nil {
		r.journal.add(r.name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, slices.Clone(recs))
	r.at = append(r.at, time.Now())
	errs := make([]error, len(recs))
	for i, rec := range recs {
		if r.fail[rec.Seq] {
			errs[i] = errBoom
		}
	}
	return errs
}

func (r *recorder) failSeqs(seqs ...uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fail = map[uint64]bool{}
	for _, s := range seqs {
		r.fail[s] = true
	}
}

func (r *recorder) record() ([][]work.Record, []time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls), slices.Clone(r.at)
}

type rig struct {
	*effects.Workers
	broker *worktest.Broker
	owner  *owner
	msgs   *recorder
	rooms  *recorder
	sink   *testlog.Sink
}

func newRig(t *testing.T, tune func(rg *rig, d *effects.Deps)) *rig {
	t.Helper()
	rg := &rig{
		broker: &worktest.Broker{}, owner: &owner{slots: map[uint16]bool{0: true}},
		msgs: &recorder{name: "msg"}, rooms: &recorder{name: "room"}, sink: &testlog.Sink{},
	}
	deps := effects.Deps{
		Queue: rg.broker.Queue,
		Owner: rg.owner,
		Registry: effects.Registry{
			store.MessageInserted: {rg.msgs.effect(delay)},
			store.RoomInserted:    {rg.rooms.effect(0)},
		},
	}
	if tune != nil {
		tune(rg, &deps)
	}
	w, err := effects.New(deps, setup, rg.sink.Logger())
	if err != nil {
		t.Fatalf("effects.New: %v", err)
	}
	rg.Workers = w
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

func msg(room, seq uint64, at time.Time) work.Record {
	return work.Record{Kind: store.MessageInserted, Room: room, Seq: seq, CommittedAt: at}
}

func roomRec(room uint64, at time.Time) work.Record {
	return work.Record{Kind: store.RoomInserted, Room: room, CommittedAt: at}
}

func seqs(recs []work.Record) []uint64 {
	out := make([]uint64, len(recs))
	for i, r := range recs {
		out[i] = r.Seq
	}
	return out
}

func rooms(recs []work.Record) []uint64 {
	out := make([]uint64, len(recs))
	for i, r := range recs {
		out[i] = r.Room
	}
	return out
}
```

`apps/core/internal/effects/workers_test.go`:

```go
package effects_test

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestFetchesOnlyPartitionsItOwns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		due := time.Now().Add(-delay)
		rg.broker.Publish(0, msg(1, 1, due))
		rg.broker.Publish(1, msg(2, 1, due))
		time.Sleep(tick)
		synctest.Wait()
		if got := rooms(rg.broker.Acked()); !slices.Equal(got, []uint64{1}) || rg.broker.Pending(1) != 1 {
			t.Fatalf("acked rooms %v, pending on partition 1 = %d; want only room 1 and partition 1 untouched", got, rg.broker.Pending(1))
		}
		rg.owner.set(1, true)
		time.Sleep(setup.Poll + tick)
		synctest.Wait()
		if got := rooms(rg.broker.Acked()); !slices.Equal(got, []uint64{1, 2}) || rg.broker.Pending(1) != 0 {
			t.Fatalf("acked rooms %v after taking slot 1, want [1 2]", got)
		}
	})
}

func TestRunsAnEffectOnlyAfterItsDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		committed := time.Now()
		rg.broker.Publish(0, msg(1, 1, committed))
		time.Sleep(delay - time.Millisecond)
		synctest.Wait()
		if calls, _ := rg.msgs.record(); len(calls) != 0 {
			t.Fatalf("calls before the delay = %v, want none", calls)
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		calls, at := rg.msgs.record()
		if len(calls) != 1 || !at[0].Equal(committed.Add(delay)) {
			t.Fatalf("calls %v at %v, want one exactly at commit + %v", calls, at, delay)
		}
		if got := rg.broker.Acked(); len(got) != 1 {
			t.Fatalf("acked = %v, want the record", got)
		}
	})
}

func TestGroupsABatchByKind(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil)
		due := time.Now().Add(-delay)
		rg.broker.Publish(0, msg(1, 1, due))
		rg.broker.Publish(0, roomRec(5, time.Now()))
		rg.broker.Publish(0, msg(1, 2, due))
		rg.start(t)
		synctest.Wait()
		msgCalls, _ := rg.msgs.record()
		roomCalls, _ := rg.rooms.record()
		if len(msgCalls) != 1 || !slices.Equal(seqs(msgCalls[0]), []uint64{1, 2}) {
			t.Fatalf("msg effect calls = %v, want one call with seq 1 and 2", msgCalls)
		}
		if len(roomCalls) != 1 || !slices.Equal(rooms(roomCalls[0]), []uint64{5}) {
			t.Fatalf("room effect calls = %v, want one call with room 5", roomCalls)
		}
		if got := len(rg.broker.Acked()); got != 3 {
			t.Fatalf("acked %d records, want 3", got)
		}
	})
}

func TestKeepsPublishOrderWithinAPartition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil)
		due := time.Now().Add(-delay)
		for s := uint64(1); s <= 10; s++ {
			rg.broker.Publish(0, msg(1, s, due))
		}
		rg.start(t)
		time.Sleep(tick)
		synctest.Wait()
		calls, _ := rg.msgs.record()
		var got []uint64
		for _, c := range calls {
			if len(c) > setup.FetchBatch {
				t.Fatalf("a call carried %d records, want at most %d", len(c), setup.FetchBatch)
			}
			got = append(got, seqs(c)...)
		}
		if !slices.Equal(got, []uint64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}) || len(calls) != 2 {
			t.Fatalf("effect saw %v in %d calls, want 1..10 in order in 2 batches", got, len(calls))
		}
	})
}

func TestAcksSucceededRecordsAndRetriesFailedOnes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil)
		rg.msgs.failSeqs(2)
		due := time.Now().Add(-delay)
		for s := uint64(1); s <= 3; s++ {
			rg.broker.Publish(0, msg(1, s, due))
		}
		rg.start(t)
		synctest.Wait()
		if a, n := seqs(rg.broker.Acked()), seqs(rg.broker.Naked()); !slices.Equal(a, []uint64{1, 3}) || !slices.Equal(n, []uint64{2}) {
			t.Fatalf("acked %v, naked %v; want [1 3] and [2]", a, n)
		}
		if s := rg.Stats(); s.Processed != 2 || s.Failed != 1 {
			t.Fatalf("stats = %+v, want 2 processed, 1 failed", s)
		}
		rg.msgs.failSeqs()
		time.Sleep(retry)
		synctest.Wait()
		calls, _ := rg.msgs.record()
		if a := seqs(rg.broker.Acked()); !slices.Equal(a, []uint64{1, 3, 2}) || len(calls) != 2 || !slices.Equal(seqs(calls[1]), []uint64{2}) {
			t.Fatalf("acked %v, calls %v; want seq 2 retried alone after %v", a, calls, retry)
		}
		if s := rg.Stats(); s.Processed != 3 || s.Failed != 1 {
			t.Fatalf("stats = %+v, want 3 processed, 1 failed", s)
		}
	})
}

func TestAcksOnlyWhenEveryEffectOfTheKindSucceeded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		order := &journal{}
		rg := newRig(t, func(_ *rig, d *effects.Deps) {
			first := &recorder{name: "first", journal: order}
			second := &recorder{name: "second", journal: order, fail: map[uint64]bool{1: true}}
			d.Registry = effects.Registry{store.MessageInserted: {first.effect(0), second.effect(0)}}
		}).start(t)
		rg.broker.Publish(0, msg(1, 1, time.Now()))
		synctest.Wait()
		if got := order.list(); !slices.Equal(got, []string{"first", "second"}) {
			t.Fatalf("effects ran as %v, want registry order", got)
		}
		if a, n := rg.broker.Acked(), rg.broker.Naked(); len(a) != 0 || len(n) != 1 {
			t.Fatalf("acked %v, naked %v; want the record naked because the second effect failed", a, n)
		}
	})
}

func TestLagShowsHowLateAnEffectRan(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		rg.broker.Publish(0, msg(1, 1, time.Now().Add(-delay-3*time.Second)))
		synctest.Wait()
		if got := rg.Stats().Lag; got != 3*time.Second {
			t.Fatalf("lag = %v, want 3s past commit + delay", got)
		}
		time.Sleep(wait + tick)
		synctest.Wait()
		if got := rg.Stats().Lag; got != 0 {
			t.Fatalf("lag of an idle partition = %v, want 0", got)
		}
	})
}
```

`apps/core/internal/effects/failures_test.go`:

```go
package effects_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type badOnce struct {
	work.Queue
	served bool
}

func (b *badOnce) Fetch(ctx context.Context, limit int, wait time.Duration) ([]work.Delivery, error) {
	if !b.served {
		b.served = true
		return nil, work.BadRecordsError{Terminated: 2}
	}
	return b.Queue.Fetch(ctx, limit, wait)
}

func TestUndecodableRecordsCountAsFailures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, func(rg *rig, d *effects.Deps) {
			d.Queue = func(p int) work.Queue { return &badOnce{Queue: rg.broker.Queue(p)} }
		}).start(t)
		time.Sleep(2 * tick)
		synctest.Wait()
		if got := rg.Stats().Failed; got != 2 {
			t.Fatalf("failed = %d, want the 2 terminated records", got)
		}
		if got := rg.sink.Count(fetchFailedMsg); got != 1 {
			t.Fatalf("%q logged %d times, want 1", fetchFailedMsg, got)
		}
	})
}

func TestNewRejectsBadConfig(t *testing.T) {
	ok := effects.Registry{store.MessageInserted: {{Name: "x", Run: func(context.Context, []work.Record) []error { return nil }}}}
	queue := func(int) work.Queue { return nil }
	tests := []struct {
		name string
		deps effects.Deps
		cfg  effects.Config
	}{
		{"partitions above slot count", effects.Deps{Queue: queue, Owner: &owner{}, Registry: ok}, effects.Config{Partitions: 1025}},
		{"fetch batch above max ack pending", effects.Deps{Queue: queue, Owner: &owner{}, Registry: ok}, effects.Config{FetchBatch: work.MaxAckPending + 1}},
		{"no queue", effects.Deps{Owner: &owner{}, Registry: ok}, setup},
		{"no owner", effects.Deps{Queue: queue, Registry: ok}, setup},
		{"effect without run", effects.Deps{Queue: queue, Owner: &owner{}, Registry: effects.Registry{store.RoomInserted: {{Name: "x"}}}}, setup},
		{"negative delay", effects.Deps{Queue: queue, Owner: &owner{}, Registry: effects.Registry{store.RoomInserted: {{Name: "x", Delay: -time.Second, Run: ok[store.MessageInserted][0].Run}}}}, setup},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := effects.New(tt.deps, tt.cfg, nil); !errors.Is(err, apperr.ErrInvalidArgument) {
				t.Fatalf("New = %v, want ErrInvalidArgument", err)
			}
		})
	}
}
```

`apps/core/internal/effects/lifecycle_test.go`:

```go
package effects_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func TestCloseReturnsRecordsStillWaitingForTheirDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		rg.broker.Publish(0, msg(1, 1, time.Now()))
		synctest.Wait()
		begin := time.Now()
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := rg.Close(stop); err != nil {
			t.Fatalf("Close = %v", err)
		}
		if took := time.Since(begin); took != 0 {
			t.Fatalf("Close took %v, want it not to wait for the delay", took)
		}
		if calls, _ := rg.msgs.record(); len(calls) != 0 {
			t.Fatalf("effect ran %v after Close, want never", calls)
		}
		if n := rg.broker.Naked(); len(n) != 1 || rg.broker.Pending(0) != 1 {
			t.Fatalf("naked %v, pending %d; want the record handed back", n, rg.broker.Pending(0))
		}
		if s := rg.Stats(); s.Failed != 0 {
			t.Fatalf("stats = %+v, want no failure for a record handed back on Close", s)
		}
	})
}

func TestCloseLetsARunningEffectFinish(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		block := make(chan struct{})
		rg := newRig(t, func(rg *rig, d *effects.Deps) {
			rg.rooms.block = block
			d.Registry = effects.Registry{store.RoomInserted: {rg.rooms.effect(0)}}
		}).start(t)
		rg.broker.Publish(0, roomRec(5, time.Now()))
		synctest.Wait()
		closed := make(chan error, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			closed <- rg.Close(ctx)
		}()
		synctest.Wait()
		select {
		case err := <-closed:
			t.Fatalf("Close returned %v while an effect was running", err)
		default:
		}
		close(block)
		if err := <-closed; err != nil {
			t.Fatalf("Close = %v", err)
		}
		if a := rg.broker.Acked(); len(a) != 1 || a[0].Room != 5 {
			t.Fatalf("acked = %v, want room 5 acked after the effect finished", a)
		}
	})
}

func TestCloseStopsAnIdleFetchAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newRig(t, nil).start(t)
		synctest.Wait()
		begin := time.Now()
		stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := rg.Close(stop); err != nil || time.Since(begin) != 0 {
			t.Fatalf("Close = %v after %v, want nil without waiting for the fetch wait", err, time.Since(begin))
		}
	})
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/effects/..."`
Expected: FAIL biên dịch: package `effects` không có file Go (`no non-test Go files`).

**Step 3: Code**

`apps/core/internal/effects/effect.go`:

```go
package effects

import (
	"cmp"
	"context"
	"fmt"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const (
	DefaultFetchBatch = 256
	DefaultFetchWait  = time.Second
	DefaultRetryDelay = 5 * time.Second
	DefaultDrain      = time.Second
	DefaultPoll       = time.Second
)

type Effect struct {
	Name  string
	Delay time.Duration
	Run   func(ctx context.Context, recs []work.Record) []error
}

type Registry map[store.ChangeKind][]Effect

type Owner interface {
	Owns(slot uint16) bool
}

type Deps struct {
	Queue    func(partition int) work.Queue
	Owner    Owner
	Registry Registry
}

type Config struct {
	Partitions int
	FetchBatch int
	FetchWait  time.Duration
	RetryDelay time.Duration
	Drain      time.Duration
	Poll       time.Duration
}

type Stats struct {
	Processed uint64
	Failed    uint64
	Lag       time.Duration
}

func (c Config) Validate() error { return c.withDefaults().validate() }

func (c Config) withDefaults() Config {
	c.Partitions = cmp.Or(c.Partitions, work.DefaultPartitions)
	c.FetchBatch = cmp.Or(c.FetchBatch, DefaultFetchBatch)
	c.FetchWait = cmp.Or(c.FetchWait, DefaultFetchWait)
	c.RetryDelay = cmp.Or(c.RetryDelay, DefaultRetryDelay)
	c.Drain = cmp.Or(c.Drain, DefaultDrain)
	c.Poll = cmp.Or(c.Poll, DefaultPoll)
	return c
}

func (c Config) validate() error {
	switch {
	case c.Partitions <= 0 || c.Partitions > slotmap.Count:
		return fmt.Errorf("%w: effect workers need 1 to %d partitions, got %d", apperr.ErrInvalidArgument, slotmap.Count, c.Partitions)
	case c.FetchBatch <= 0 || c.FetchBatch > work.MaxAckPending:
		return fmt.Errorf("%w: fetch batch %d must be between 1 and the consumer max ack pending %d", apperr.ErrInvalidArgument, c.FetchBatch, work.MaxAckPending)
	case c.FetchWait <= 0 || c.RetryDelay <= 0 || c.Drain <= 0 || c.Poll <= 0:
		return fmt.Errorf("%w: effect worker config %+v must be positive", apperr.ErrInvalidArgument, c)
	default:
		return nil
	}
}

func (r Registry) validate() error {
	for kind, effs := range r {
		for _, e := range effs {
			if e.Name == "" || e.Run == nil || e.Delay < 0 {
				return fmt.Errorf("%w: effect %q for change kind %d needs a name, a run function and a delay of at least 0", apperr.ErrInvalidArgument, e.Name, kind)
			}
		}
	}
	return nil
}
```

`apps/core/internal/effects/workers.go`:

```go
package effects

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var errStarted = errors.New("effect workers already started")

type Workers struct {
	deps      Deps
	cfg       Config
	log       *slog.Logger
	fails     limitedLog
	processed atomic.Uint64
	failed    atomic.Uint64
	lags      []atomic.Int64
	started   atomic.Bool
	stop      chan struct{}
	halt      sync.Once
	done      chan struct{}
}

func New(deps Deps, cfg Config, log *slog.Logger) (*Workers, error) {
	if deps.Queue == nil || deps.Owner == nil || deps.Registry == nil {
		return nil, fmt.Errorf("%w: effect workers need a queue, an owner and a registry", apperr.ErrInvalidArgument)
	}
	if err := deps.Registry.validate(); err != nil {
		return nil, err
	}
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	return &Workers{
		deps: deps, cfg: cfg, log: log, fails: limitedLog{log: log},
		lags: make([]atomic.Int64, cfg.Partitions),
		stop: make(chan struct{}), done: make(chan struct{}),
	}, nil
}

func (w *Workers) Run(ctx context.Context) error {
	if !w.started.CompareAndSwap(false, true) {
		return errStarted
	}
	defer close(w.done)
	fetchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Go(func() {
		select {
		case <-w.stop:
			cancel()
		case <-fetchCtx.Done():
		}
	})
	for p := range w.cfg.Partitions {
		wg.Go(func() { w.partition(ctx, fetchCtx, p) })
	}
	wg.Wait()
	if w.stopping() {
		return nil
	}
	return ctx.Err()
}

func (w *Workers) Close(ctx context.Context) error {
	w.halt.Do(func() { close(w.stop) })
	if !w.started.Load() {
		return nil
	}
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Workers) Stats() Stats {
	var lag int64
	for i := range w.lags {
		lag = max(lag, w.lags[i].Load())
	}
	return Stats{Processed: w.processed.Load(), Failed: w.failed.Load(), Lag: time.Duration(lag)}
}

func (w *Workers) stopping() bool {
	select {
	case <-w.stop:
		return true
	default:
		return false
	}
}
```

Khi mọi partition goroutine kết thúc (do `stop` hoặc `ctx`), goroutine canh `stop` cũng kết thúc: `stop` đóng → nó huỷ; `ctx` hết → `fetchCtx` hết.

`apps/core/internal/effects/partition.go`:

```go
package effects

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

const (
	fetchFailedMsg  = "work fetch failed; retrying"
	settleFailedMsg = "work ack or nak failed; the record comes back after the ack wait"
)

func (w *Workers) partition(ctx, fetchCtx context.Context, p int) {
	q := w.deps.Queue(p)
	slot := slotOf(p)
	for !w.stopping() && ctx.Err() == nil {
		if !w.deps.Owner.Owns(slot) {
			w.lags[p].Store(0)
			w.pause(ctx)
			continue
		}
		ds, err := q.Fetch(fetchCtx, w.cfg.FetchBatch, w.cfg.FetchWait)
		if w.stopping() || ctx.Err() != nil {
			w.release(ctx, ds)
			return
		}
		w.fetchFailed(ctx, p, err)
		if len(ds) == 0 {
			w.lags[p].Store(0)
			if err != nil {
				w.pause(ctx)
			}
			continue
		}
		w.process(ctx, p, ds)
	}
}

func (w *Workers) fetchFailed(ctx context.Context, p int, err error) {
	if err == nil {
		return
	}
	var bad work.BadRecordsError
	if errors.As(err, &bad) {
		w.failed.Add(bad.Terminated)
	}
	w.fails.warn(ctx, fetchFailedMsg, "partition", p, "err", err)
}

func (w *Workers) pause(ctx context.Context) {
	t := time.NewTimer(w.cfg.Poll)
	defer t.Stop()
	select {
	case <-t.C:
	case <-w.stop:
	case <-ctx.Done():
	}
}

func (w *Workers) release(ctx context.Context, ds []work.Delivery) {
	for _, d := range ds {
		if err := d.Nak(0); err != nil {
			w.fails.warn(ctx, settleFailedMsg, "err", err)
		}
	}
}

func slotOf(p int) uint16 {
	if p < 0 || p > math.MaxUint16 {
		return 0
	}
	return uint16(p)
}
```

(`slotOf` có chặn biên để `gosec` G115 không báo; `Config.validate` đã giới hạn `Partitions ≤ 1024`.)

`apps/core/internal/effects/batch.go`:

```go
package effects

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

var errShortResult = errors.New("effect returned fewer results than records")

type group struct {
	kind store.ChangeKind
	ds   []work.Delivery
	recs []work.Record
}

func (w *Workers) process(ctx context.Context, p int, ds []work.Delivery) {
	groups := groupByKind(ds)
	var lag time.Duration
	for i, g := range groups {
		done, late := w.runGroup(ctx, g)
		lag = max(lag, late)
		if !done {
			for _, rest := range groups[i:] {
				w.release(ctx, rest.ds)
			}
			break
		}
	}
	w.lags[p].Store(int64(lag))
}

func groupByKind(ds []work.Delivery) []group {
	var out []group
	at := map[store.ChangeKind]int{}
	for _, d := range ds {
		r := d.Record()
		i, ok := at[r.Kind]
		if !ok {
			i = len(out)
			at[r.Kind] = i
			out = append(out, group{kind: r.Kind})
		}
		out[i].ds = append(out[i].ds, d)
		out[i].recs = append(out[i].recs, r)
	}
	slices.SortFunc(out, func(a, b group) int { return cmp.Compare(a.kind, b.kind) })
	return out
}

func (w *Workers) runGroup(ctx context.Context, g group) (bool, time.Duration) {
	failed := make([]bool, len(g.recs))
	newest, oldest := committedRange(g.recs)
	var lag time.Duration
	for _, e := range w.deps.Registry[g.kind] {
		if !w.waitUntil(ctx, newest.Add(e.Delay)) {
			return false, lag
		}
		lag = max(lag, time.Since(oldest.Add(e.Delay)))
		errs := e.Run(ctx, g.recs)
		for i := range failed {
			if errAt(errs, i) != nil {
				failed[i] = true
			}
		}
	}
	w.settle(ctx, g.ds, failed)
	return true, lag
}

func (w *Workers) waitUntil(ctx context.Context, due time.Time) bool {
	left := time.Until(due)
	if left <= 0 {
		return true
	}
	t := time.NewTimer(left)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-w.stop:
		return false
	case <-ctx.Done():
		return false
	}
}

func (w *Workers) settle(ctx context.Context, ds []work.Delivery, failed []bool) {
	stopped := ctx.Err() != nil
	for i, d := range ds {
		var err error
		switch {
		case !failed[i]:
			if err = d.Ack(); err == nil {
				w.processed.Add(1)
			}
		case stopped:
			err = d.Nak(0)
		default:
			w.failed.Add(1)
			err = d.Nak(w.cfg.RetryDelay)
		}
		if err != nil {
			w.fails.warn(ctx, settleFailedMsg, "err", err)
		}
	}
}

func committedRange(recs []work.Record) (time.Time, time.Time) {
	newest, oldest := recs[0].CommittedAt, recs[0].CommittedAt
	for _, r := range recs[1:] {
		if r.CommittedAt.After(newest) {
			newest = r.CommittedAt
		}
		if r.CommittedAt.Before(oldest) {
			oldest = r.CommittedAt
		}
	}
	return newest, oldest
}

func errAt(errs []error, i int) error {
	if i < len(errs) {
		return errs[i]
	}
	return errShortResult
}
```

Record của kind không có effect nào trong registry: vòng effect rỗng, nên được `Ack` ngay (không có việc gì để làm).

`apps/core/internal/effects/limited_log.go`:

```go
package effects

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

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

(Bản sao nhỏ của `reconcile.limitedLog`, giống tiền lệ `publish.failureLog`; ghi Minor ở "Kết quả thực thi" nếu reviewer muốn gom.)

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/effects/..."`
Expected: PASS 5 lần, goleak sạch. `wc -l apps/core/internal/effects/*.go` đều < 200.

**Step 5: INDEXES + commit**

`INDEXES.csv`, thêm dòng:

```csv
apps/core/internal/effects,package,"Effect engine workers: one goroutine per work partition that fetches only while this core owns the slot with the same number (checked before every fetch); groups a batch by change kind and runs the kind's effects in registry order, each after max(CommittedAt) + its delay; acks a record when every effect returned nil, else naks it after WORK_RETRY_DELAY; Close stops fetching, lets a running effect finish and hands records still waiting their delay back with Nak(0); undecodable records count as failures; Stats (processed, failed, lag behind commit + delay)",Effect;Registry;Owner;Deps;Config;Config.Validate;Stats;Workers;New;Workers.Run;Workers.Close;Workers.Stats,apps/core (Task 12),unit;synctest;goleak,D65;D66;D79
```

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/effects/ INDEXES.csv
git commit -m "feat(effects): run registered effects per owned work partition"
```

---
### Task 9: Effect `msg_created`

Effect thay phần "bù event" của reconciler cũ, giờ chạy ở worker (D65, D80): record chỉ mang khoá, nên effect tra ack mark theo lô, chỉ `Find` (majority) những tin **chưa** có mark, dựng event từ doc bằng đúng `pbconv.MessageCreated` của fast path (parity RC2), publish và **chờ PubAck** của từng tin trong ctx. Record chỉ lỗi khi publish bị từ chối, PubAck báo lỗi, hoặc store/room lỗi tạm; doc thiếu, room không tồn tại hay key không hợp lệ thì **bỏ** (đếm `Dropped`, trả nil) vì thử lại không giúp được.

Mark lỗi (Redis dedupe chết, cooldown) → coi như mọi tin chưa mark và publish hết; JetStream bỏ trùng theo `Nats-Msg-Id` trong `EVT_STREAM_DUPLICATES`.

Cache room type dùng chung giữa các partition goroutine nên có khoá (khác `roomTypes` cũ của reconciler vốn chỉ một goroutine). Đầy thì xoá sạch như bản cũ.

`NewMessageCreated` không nhận logger (hợp đồng); bỏ tin chỉ đếm `Dropped`, metric/alert ở Task 13.

**Files:**
- Create: `apps/core/internal/effects/ports.go`
- Create: `apps/core/internal/effects/pub_acks.go`
- Create: `apps/core/internal/effects/room_types.go`
- Create: `apps/core/internal/effects/message_created.go`
- Create: `apps/core/internal/effects/fixtures_test.go`
- Create: `apps/core/internal/effects/message_created_test.go`
- Modify: `INDEXES.csv`

**Step 1: Test**

`apps/core/internal/effects/fixtures_test.go`:

```go
package effects_test

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

const (
	tenant        = "acme"
	room   uint64 = 4242
)

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

func (m *marks) mark(keys ...store.MsgKey) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		m.acked[k] = true
	}
}

func (m *marks) fail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

type countingRooms struct {
	effects.RoomReader
	gets atomic.Int32
}

func (c *countingRooms) Get(ctx context.Context, id uint64) (domain.Room, error) {
	c.gets.Add(1)
	return c.RoomReader.Get(ctx, id)
}

type brokenStore struct{}

func (brokenStore) Find(context.Context, uint64, []store.MsgKey) ([]domain.Message, error) {
	return nil, errBoom
}

func (brokenStore) Get(context.Context, uint64) (domain.Room, error) {
	return domain.Room{}, errBoom
}

func createRoom(t *testing.T, rooms *memstore.Rooms, id uint64) domain.Room {
	t.Helper()
	created := time.Now().UTC().Truncate(time.Millisecond)
	r := domain.Room{ID: id, Tenant: tenant, Type: domain.RoomGroup, Name: "team", CreatedBy: "alice", CreatedAt: created, MemberCount: 1}
	if err := rooms.Create(t.Context(), r, []domain.Member{{Room: id, Tenant: tenant, User: "alice", Role: domain.RoleOwner, JoinedAt: created}}); err != nil {
		t.Fatalf("create room %d: %v", id, err)
	}
	got, err := rooms.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("get room %d: %v", id, err)
	}
	return got
}

func storedEventIDs(js *publishtest.JetStream) []string {
	var out []string
	for _, m := range js.Stored() {
		out = append(out, publishtest.MsgID(m))
	}
	return out
}

type msgRig struct {
	eff   *effects.MessageCreated
	msgs  *memstore.Messages
	rooms *countingRooms
	marks *marks
	js    *publishtest.JetStream
}

func newMsgRig(t *testing.T, finder effects.MessageFinder) *msgRig {
	t.Helper()
	mem := memstore.NewRooms()
	createRoom(t, mem, room)
	rg := &msgRig{msgs: memstore.NewMessages(), rooms: &countingRooms{RoomReader: mem}, marks: &marks{acked: map[store.MsgKey]bool{}}, js: &publishtest.JetStream{}}
	if finder == nil {
		finder = rg.msgs
	}
	eff, err := effects.NewMessageCreated(
		effects.MessageCreatedDeps{Marks: rg.marks, Messages: finder, Rooms: rg.rooms, JS: rg.js},
		effects.MessageCreatedConfig{SubjectRoot: "evt", Delay: delay, RoomCache: 16},
	)
	if err != nil {
		t.Fatalf("NewMessageCreated: %v", err)
	}
	rg.eff = eff
	return rg
}

func (rg *msgRig) insert(t *testing.T, seqs ...uint64) []domain.Message {
	t.Helper()
	var out []domain.Message
	for _, s := range seqs {
		m := domain.Message{Room: room, Seq: s, Tenant: tenant, From: "alice", Kind: domain.KindText, Text: "hi", CID: "c", CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
		if res := rg.msgs.Insert(t.Context(), []domain.Message{m}); res[0].Outcome != store.Inserted {
			t.Fatalf("insert %d: %+v", s, res)
		}
		out = append(out, m)
	}
	return out
}

func (rg *msgRig) run(ctx context.Context, recs []work.Record) []error {
	return rg.eff.Effect().Run(ctx, recs)
}

func msgRecs(r uint64, seqs ...uint64) []work.Record {
	out := make([]work.Record, len(seqs))
	for i, s := range seqs {
		out[i] = work.Record{Kind: store.MessageInserted, Room: r, Seq: s, CommittedAt: time.Now()}
	}
	return out
}

func allNil(errs []error, n int) bool {
	return len(errs) == n && !slices.ContainsFunc(errs, func(err error) bool { return err != nil })
}
```

`apps/core/internal/effects/message_created_test.go`:

```go
package effects_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestMsgCreatedDeclaresItsPolicy(t *testing.T) {
	e := newMsgRig(t, nil).eff.Effect()
	if e.Name != effects.MessageCreatedName || e.Delay != delay || e.Run == nil {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.MessageCreatedName, delay)
	}
}

func TestMsgCreatedRepublishesOnlyUnmarkedMessagesLikeTheFastPath(t *testing.T) {
	rg := newMsgRig(t, nil)
	inserted := rg.insert(t, 1, 2, 3)
	rg.marks.mark(store.MsgKey{Room: room, Seq: 2})
	if errs := rg.run(t.Context(), msgRecs(room, 1, 2, 3)); !allNil(errs, 3) {
		t.Fatalf("errs = %v, want 3 nils", errs)
	}
	want := []string{pbconv.MessageEventID(room, 0, 1), pbconv.MessageEventID(room, 0, 3)}
	if got := storedEventIDs(rg.js); !slices.Equal(got, want) {
		t.Fatalf("stored = %v, want %v", got, want)
	}
	if subj := rg.js.Stored()[0].Subject; subj != "evt.acme.room.4242.msg_created" {
		t.Fatalf("subject = %q", subj)
	}
	events, err := rg.js.Events()
	if err != nil {
		t.Fatalf("decode events: %v", err)
	}
	if !proto.Equal(events[0], pbconv.MessageCreated(domain.RoomGroup, inserted[0])) || !proto.Equal(events[1], pbconv.MessageCreated(domain.RoomGroup, inserted[2])) {
		t.Fatalf("events = %v, want the fast path events for seq 1 and 3", events)
	}
	if rg.eff.Republished() != 2 || rg.eff.Dropped() != 0 {
		t.Fatalf("republished %d, dropped %d; want 2 and 0", rg.eff.Republished(), rg.eff.Dropped())
	}
}

func TestMsgCreatedPublishesEverythingWhenMarksFail(t *testing.T) {
	rg := newMsgRig(t, nil)
	rg.insert(t, 1)
	rg.marks.mark(store.MsgKey{Room: room, Seq: 1})
	rg.marks.fail(errBoom)
	if errs := rg.run(t.Context(), msgRecs(room, 1)); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.MessageEventID(room, 0, 1)}) {
		t.Fatalf("stored = %v, want seq 1 despite its mark", got)
	}
}

func TestMsgCreatedDropsMissingMessagesAndUnknownRooms(t *testing.T) {
	rg := newMsgRig(t, nil)
	rg.insert(t, 1)
	recs := append(msgRecs(room, 1, 9), msgRecs(999, 1, 2)...)
	if errs := rg.run(t.Context(), recs); !allNil(errs, 4) {
		t.Fatalf("errs = %v, want nil for dropped records so they are not retried", errs)
	}
	if got := storedEventIDs(rg.js); !slices.Equal(got, []string{pbconv.MessageEventID(room, 0, 1)}) {
		t.Fatalf("stored = %v, want only seq 1", got)
	}
	if rg.eff.Dropped() != 3 {
		t.Fatalf("dropped = %d, want 1 missing doc + 2 of an unknown room", rg.eff.Dropped())
	}
}

func TestMsgCreatedFailsOnlyTheRefusedRecord(t *testing.T) {
	rg := newMsgRig(t, nil)
	rg.insert(t, 1, 2, 3)
	refused := pbconv.MessageEventID(room, 0, 2)
	rg.js.RefuseWhen(func(m *nats.Msg) error {
		if publishtest.MsgID(m) == refused {
			return errBoom
		}
		return nil
	})
	errs := rg.run(t.Context(), msgRecs(room, 1, 2, 3))
	if len(errs) != 3 || errs[0] != nil || !errors.Is(errs[1], errBoom) || errs[2] != nil {
		t.Fatalf("errs = %v, want only the second record failed", errs)
	}
	if rg.eff.Republished() != 2 {
		t.Fatalf("republished = %d, want 2", rg.eff.Republished())
	}
}

func TestMsgCreatedRetriesWhenTheStoreFails(t *testing.T) {
	rg := newMsgRig(t, brokenStore{})
	errs := rg.run(t.Context(), msgRecs(room, 1, 2))
	if len(errs) != 2 || !errors.Is(errs[0], errBoom) || !errors.Is(errs[1], errBoom) || rg.eff.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want both failed for a retry and none dropped", errs, rg.eff.Dropped())
	}
}

func TestMsgCreatedWaitsForThePubAck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newMsgRig(t, nil)
		rg.insert(t, 1)
		rg.js.Hold()
		done := make(chan []error, 1)
		go func() { done <- rg.run(context.Background(), msgRecs(room, 1)) }()
		synctest.Wait()
		select {
		case errs := <-done:
			t.Fatalf("Run returned %v before the PubAck", errs)
		default:
		}
		rg.js.Release()
		if errs := <-done; !allNil(errs, 1) || rg.eff.Republished() != 1 {
			t.Fatalf("errs = %v, republished %d; want success after the PubAck", errs, rg.eff.Republished())
		}
	})
}

func TestMsgCreatedFailsUnackedRecordsWhenCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rg := newMsgRig(t, nil)
		rg.insert(t, 1)
		rg.js.Hold()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan []error, 1)
		go func() { done <- rg.run(ctx, msgRecs(room, 1)) }()
		synctest.Wait()
		cancel()
		if errs := <-done; len(errs) != 1 || !errors.Is(errs[0], context.Canceled) || rg.eff.Republished() != 0 {
			t.Fatalf("errs = %v, republished %d; want context.Canceled and nothing counted", errs, rg.eff.Republished())
		}
	})
}

func TestMsgCreatedCachesTheRoomType(t *testing.T) {
	rg := newMsgRig(t, nil)
	rg.insert(t, 1, 2)
	for _, s := range []uint64{1, 2} {
		if errs := rg.run(t.Context(), msgRecs(room, s)); !allNil(errs, 1) {
			t.Fatalf("errs = %v", errs)
		}
	}
	if got := rg.rooms.gets.Load(); got != 1 {
		t.Fatalf("room reads = %d, want 1", got)
	}
}

func TestNewMessageCreatedRejectsMissingDeps(t *testing.T) {
	js := &publishtest.JetStream{}
	full := effects.MessageCreatedDeps{Marks: &marks{}, Messages: memstore.NewMessages(), Rooms: memstore.NewRooms(), JS: js}
	cfg := effects.MessageCreatedConfig{SubjectRoot: "evt"}
	for name, deps := range map[string]effects.MessageCreatedDeps{
		"no marks":    {Messages: full.Messages, Rooms: full.Rooms, JS: js},
		"no messages": {Marks: full.Marks, Rooms: full.Rooms, JS: js},
		"no rooms":    {Marks: full.Marks, Messages: full.Messages, JS: js},
		"no js":       {Marks: full.Marks, Messages: full.Messages, Rooms: full.Rooms},
	} {
		if _, err := effects.NewMessageCreated(deps, cfg); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: NewMessageCreated = %v, want ErrInvalidArgument", name, err)
		}
	}
	if _, err := effects.NewMessageCreated(full, effects.MessageCreatedConfig{}); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("NewMessageCreated without a subject root = %v, want ErrInvalidArgument", err)
	}
	eff, err := effects.NewMessageCreated(full, cfg)
	if err != nil {
		t.Fatalf("NewMessageCreated with defaults: %v", err)
	}
	if got := eff.Effect().Delay; got != effects.DefaultDelay {
		t.Fatalf("default delay = %v, want %v", got, effects.DefaultDelay)
	}
}
```

Ghi chú test: `TestMsgCreatedWaitsForThePubAck` và `...WhenCancelled` chạy trong `synctest` để `synctest.Wait()` chứng minh `Run` đang chặn chờ future (không có timer); các test khác không có goroutine nên chạy thường. `createRoom` trong `newMsgRig` gọi `memstore.Rooms.Create` thường, không có feed nào gắn vào nên log chung của Task 3 không ảnh hưởng.

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/effects/..."`
Expected: FAIL biên dịch: `undefined: effects.MessageCreated`, `effects.NewMessageCreated`, `effects.RoomReader`, `effects.MessageFinder`.

**Step 3: Code**

`apps/core/internal/effects/ports.go`:

```go
package effects

import (
	"context"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

type Marks interface {
	Acked(ctx context.Context, keys []store.MsgKey) ([]bool, error)
}

type MessageFinder interface {
	Find(ctx context.Context, room uint64, keys []store.MsgKey) ([]domain.Message, error)
}

type RoomReader interface {
	Get(ctx context.Context, id uint64) (domain.Room, error)
}
```

`apps/core/internal/effects/pub_acks.go`:

```go
package effects

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

type pendingAck struct {
	index  int
	future jetstream.PubAckFuture
}

func send(js publish.JetStream, msg *nats.Msg, i int, errs []error, pending []pendingAck) []pendingAck {
	f, err := js.PublishMsgAsync(msg)
	if err != nil {
		errs[i] = err
		return pending
	}
	return append(pending, pendingAck{index: i, future: f})
}

func awaitAcks(ctx context.Context, pending []pendingAck, errs []error, acked *atomic.Uint64) {
	for _, p := range pending {
		select {
		case <-p.future.Ok():
			acked.Add(1)
		case err := <-p.future.Err():
			errs[p.index] = err
		case <-ctx.Done():
			errs[p.index] = ctx.Err()
		}
	}
}

func undeliverable(err error) bool {
	return errors.Is(err, domain.ErrRoomNotFound) || errors.Is(err, apperr.ErrInvalidArgument)
}
```

`apps/core/internal/effects/room_types.go`:

```go
package effects

import (
	"context"
	"sync"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
)

type roomTypes struct {
	rooms RoomReader
	limit int
	mu    sync.Mutex
	cache map[uint64]domain.RoomType
}

func newRoomTypes(rooms RoomReader, limit int) *roomTypes {
	return &roomTypes{rooms: rooms, limit: limit, cache: make(map[uint64]domain.RoomType)}
}

func (c *roomTypes) get(ctx context.Context, id uint64) (domain.RoomType, error) {
	c.mu.Lock()
	t, ok := c.cache[id]
	c.mu.Unlock()
	if ok {
		return t, nil
	}
	room, err := c.rooms.Get(ctx, id)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.cache) >= c.limit {
		clear(c.cache)
	}
	c.cache[id] = room.Type
	return room.Type, nil
}
```

`apps/core/internal/effects/message_created.go`:

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
)

const (
	MessageCreatedName = "msg_created"
	DefaultDelay       = 5 * time.Second
	DefaultRoomCache   = 65536
)

type MessageCreatedDeps struct {
	Marks    Marks
	Messages MessageFinder
	Rooms    RoomReader
	JS       publish.JetStream
}

type MessageCreatedConfig struct {
	SubjectRoot string
	Delay       time.Duration
	RoomCache   int
}

type MessageCreated struct {
	deps        MessageCreatedDeps
	cfg         MessageCreatedConfig
	types       *roomTypes
	republished atomic.Uint64
	dropped     atomic.Uint64
}

func NewMessageCreated(deps MessageCreatedDeps, cfg MessageCreatedConfig) (*MessageCreated, error) {
	if deps.Marks == nil || deps.Messages == nil || deps.Rooms == nil || deps.JS == nil {
		return nil, fmt.Errorf("%w: msg_created needs marks, messages, rooms and a jetstream client", apperr.ErrInvalidArgument)
	}
	cfg.Delay = cmp.Or(cfg.Delay, DefaultDelay)
	cfg.RoomCache = cmp.Or(cfg.RoomCache, DefaultRoomCache)
	if cfg.SubjectRoot == "" || cfg.Delay < 0 || cfg.RoomCache < 0 {
		return nil, fmt.Errorf("%w: msg_created config %+v needs a subject root, a delay and a room cache", apperr.ErrInvalidArgument, cfg)
	}
	return &MessageCreated{deps: deps, cfg: cfg, types: newRoomTypes(deps.Rooms, cfg.RoomCache)}, nil
}

func (e *MessageCreated) Effect() Effect {
	return Effect{Name: MessageCreatedName, Delay: e.cfg.Delay, Run: e.run}
}

func (e *MessageCreated) Republished() uint64 { return e.republished.Load() }

func (e *MessageCreated) Dropped() uint64 { return e.dropped.Load() }

func (e *MessageCreated) run(ctx context.Context, recs []work.Record) []error {
	errs := make([]error, len(recs))
	keys := make([]store.MsgKey, len(recs))
	for i, r := range recs {
		keys[i] = store.MsgKey{Room: r.Room, Thread: r.Thread, Seq: r.Seq}
	}
	var pending []pendingAck
	for _, idx := range e.unmarkedByRoom(ctx, keys) {
		pending = e.publishRoom(ctx, keys, idx, errs, pending)
	}
	awaitAcks(ctx, pending, errs, &e.republished)
	return errs
}

func (e *MessageCreated) unmarkedByRoom(ctx context.Context, keys []store.MsgKey) [][]int {
	acked, err := e.deps.Marks.Acked(ctx, keys)
	if err != nil || len(acked) != len(keys) {
		acked = make([]bool, len(keys))
	}
	at := map[uint64]int{}
	var out [][]int
	for i, k := range keys {
		if acked[i] {
			continue
		}
		g, ok := at[k.Room]
		if !ok {
			g = len(out)
			at[k.Room] = g
			out = append(out, nil)
		}
		out[g] = append(out[g], i)
	}
	return out
}

func (e *MessageCreated) publishRoom(ctx context.Context, keys []store.MsgKey, idx []int, errs []error, pending []pendingAck) []pendingAck {
	room := keys[idx[0]].Room
	typ, err := e.types.get(ctx, room)
	var found map[store.MsgKey]domain.Message
	if err == nil {
		found, err = e.find(ctx, room, keys, idx)
	}
	switch {
	case undeliverable(err):
		for range idx {
			e.dropped.Add(1)
		}
		return pending
	case err != nil:
		for _, i := range idx {
			errs[i] = err
		}
		return pending
	}
	for _, i := range idx {
		m, ok := found[keys[i]]
		if !ok {
			e.dropped.Add(1)
			continue
		}
		msg, err := publish.Message(e.cfg.SubjectRoot, room, pbconv.MessageCreated(typ, m))
		if err != nil {
			e.dropped.Add(1)
			continue
		}
		pending = send(e.deps.JS, msg, i, errs, pending)
	}
	return pending
}

func (e *MessageCreated) find(ctx context.Context, room uint64, keys []store.MsgKey, idx []int) (map[store.MsgKey]domain.Message, error) {
	want := make([]store.MsgKey, len(idx))
	for j, i := range idx {
		want[j] = keys[i]
	}
	msgs, err := e.deps.Messages.Find(ctx, room, want)
	if err != nil {
		return nil, err
	}
	found := make(map[store.MsgKey]domain.Message, len(msgs))
	for _, m := range msgs {
		found[store.KeyOf(m)] = m
	}
	return found, nil
}
```

Ghi chú: `mongostore.Find` đọc majority, đúng yêu cầu D52; doc thiếu sau khi change stream (majority-committed) đã báo insert nghĩa là doc thật sự không còn, nên bỏ. Record trùng khoá trong cùng lô (resync đẩy lại ngoài cửa sổ chống trùng của work stream) publish hai lần cùng `Nats-Msg-Id`; stream event bỏ bản sau.

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/effects/..."`
Expected: PASS 5 lần (gồm test worker của Task 8). `wc -l apps/core/internal/effects/*.go` đều < 200.

**Step 5: INDEXES + commit**

`INDEXES.csv`, dòng `apps/core/internal/effects`: nối vào purpose `; msg_created effect (delay RECONCILE_DELAY): batch ack mark lookup (failure = all unmarked), majority Find of unmarked messages per room, event rebuilt with pbconv.MessageCreated (fast path parity), publish and wait for every PubAck; missing doc, unknown room or invalid key is dropped and counted; room type cache shared across partitions`; key_symbols thêm `MessageCreated;NewMessageCreated;MessageCreated.Effect;MessageCreated.Republished;MessageCreated.Dropped;MessageCreatedDeps;MessageCreatedConfig;MessageCreatedName;Marks;MessageFinder;RoomReader;DefaultDelay;DefaultRoomCache`; decisions thêm `D52;D80`. Dòng `apps/core/internal/eventmark`: trong used_by thay `apps/core/internal/effects (Marks, Task 9)` bằng `apps/core/internal/effects (Marks)`.

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/effects/ INDEXES.csv
git commit -m "feat(effects): republish unmarked msg_created events from the worker"
```

---
### Task 10: Effect `room_created`

Lưới an toàn cho fast path `room_created` của Task 2 (D65): worker đọc room bằng `Rooms.Get`, dựng event bằng `pbconv.RoomCreated` (cùng hàm fast path dùng, parity RC2), publish và chờ PubAck. Không tra ack mark (effect hiếm, JetStream bỏ trùng theo id `{room}-created` trong `EVT_STREAM_DUPLICATES`), nên với delay `RECONCILE_DELAY` mặc định mỗi room sinh đúng một publish trùng bị stream bỏ. Room không tồn tại hoặc event không hợp lệ (tenant không an toàn cho subject) → bỏ, đếm `Dropped`, trả nil.

Giả định từ Task 1–2: `pbconv.RoomCreated(r domain.Room) *chatimv1.Event` với id `pbconv.RoomCreatedEventID(r.ID)`, và `publish.Message` đã biết payload `room_created` (subject `evt.{t}.room.{rid}.room_created`). Nếu Task 1 đặt chữ ký khác, sửa đúng một dòng gọi trong `room_created.go` và hai dòng trong test.

**Files:**
- Create: `apps/core/internal/effects/room_created.go`
- Create: `apps/core/internal/effects/room_created_test.go`
- Modify: `INDEXES.csv`

**Step 1: Test**

`apps/core/internal/effects/room_created_test.go`:

```go
package effects_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go"
	"google.golang.org/protobuf/proto"

	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish/publishtest"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func newRoomCreated(t *testing.T, rooms effects.RoomReader, js *publishtest.JetStream) *effects.RoomCreated {
	t.Helper()
	eff, err := effects.NewRoomCreated(effects.RoomCreatedDeps{Rooms: rooms, JS: js}, effects.RoomCreatedConfig{SubjectRoot: "evt", Delay: delay})
	if err != nil {
		t.Fatalf("NewRoomCreated: %v", err)
	}
	return eff
}

func roomRecs(ids ...uint64) []work.Record {
	out := make([]work.Record, len(ids))
	for i, id := range ids {
		out[i] = work.Record{Kind: store.RoomInserted, Room: id, CommittedAt: time.Now()}
	}
	return out
}

func TestRoomCreatedDeclaresItsPolicy(t *testing.T) {
	e := newRoomCreated(t, memstore.NewRooms(), &publishtest.JetStream{}).Effect()
	if e.Name != effects.RoomCreatedName || e.Delay != delay || e.Run == nil {
		t.Fatalf("effect = %q delay %v, want %q delay %v", e.Name, e.Delay, effects.RoomCreatedName, delay)
	}
}

func TestRoomCreatedPublishesTheFastPathEvent(t *testing.T) {
	mem := memstore.NewRooms()
	r := createRoom(t, mem, room)
	js := &publishtest.JetStream{}
	eff := newRoomCreated(t, mem, js)
	if errs := eff.Effect().Run(t.Context(), roomRecs(room)); !allNil(errs, 1) {
		t.Fatalf("errs = %v", errs)
	}
	if got := storedEventIDs(js); !slices.Equal(got, []string{pbconv.RoomCreatedEventID(room)}) {
		t.Fatalf("stored = %v, want %s", got, pbconv.RoomCreatedEventID(room))
	}
	if subj := js.Stored()[0].Subject; subj != "evt.acme.room.4242.room_created" {
		t.Fatalf("subject = %q", subj)
	}
	events, err := js.Events()
	if err != nil || !proto.Equal(events[0], pbconv.RoomCreated(r)) {
		t.Fatalf("event = %v, %v; want the fast path event %v", events, err, pbconv.RoomCreated(r))
	}
	if eff.Republished() != 1 || eff.Dropped() != 0 {
		t.Fatalf("republished %d, dropped %d; want 1 and 0", eff.Republished(), eff.Dropped())
	}
}

func TestRoomCreatedDropsAnUnknownRoom(t *testing.T) {
	js := &publishtest.JetStream{}
	eff := newRoomCreated(t, memstore.NewRooms(), js)
	if errs := eff.Effect().Run(t.Context(), roomRecs(999)); !allNil(errs, 1) {
		t.Fatalf("errs = %v, want nil so the record is not retried", errs)
	}
	if len(js.Attempts()) != 0 || eff.Dropped() != 1 {
		t.Fatalf("attempts %d, dropped %d; want 0 and 1", len(js.Attempts()), eff.Dropped())
	}
}

func TestRoomCreatedFailsOnlyTheRefusedRecord(t *testing.T) {
	mem := memstore.NewRooms()
	createRoom(t, mem, room)
	createRoom(t, mem, 777)
	js := &publishtest.JetStream{}
	js.RefuseWhen(func(m *nats.Msg) error {
		if publishtest.MsgID(m) == pbconv.RoomCreatedEventID(777) {
			return errBoom
		}
		return nil
	})
	eff := newRoomCreated(t, mem, js)
	errs := eff.Effect().Run(t.Context(), roomRecs(room, 777))
	if len(errs) != 2 || errs[0] != nil || !errors.Is(errs[1], errBoom) {
		t.Fatalf("errs = %v, want only room 777 failed", errs)
	}
}

func TestRoomCreatedRetriesWhenTheStoreFails(t *testing.T) {
	eff := newRoomCreated(t, brokenStore{}, &publishtest.JetStream{})
	if errs := eff.Effect().Run(t.Context(), roomRecs(room)); len(errs) != 1 || !errors.Is(errs[0], errBoom) || eff.Dropped() != 0 {
		t.Fatalf("errs = %v, dropped %d; want a retryable failure", errs, eff.Dropped())
	}
}

func TestRoomCreatedWaitsForThePubAck(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mem := memstore.NewRooms()
		createRoom(t, mem, room)
		js := &publishtest.JetStream{}
		js.Hold()
		eff := newRoomCreated(t, mem, js)
		done := make(chan []error, 1)
		go func() { done <- eff.Effect().Run(context.Background(), roomRecs(room)) }()
		synctest.Wait()
		select {
		case errs := <-done:
			t.Fatalf("Run returned %v before the PubAck", errs)
		default:
		}
		js.Release()
		if errs := <-done; !allNil(errs, 1) || eff.Republished() != 1 {
			t.Fatalf("errs = %v, republished %d; want success after the PubAck", errs, eff.Republished())
		}
	})
}

func TestNewRoomCreatedRejectsBadInput(t *testing.T) {
	js := &publishtest.JetStream{}
	cfg := effects.RoomCreatedConfig{SubjectRoot: "evt"}
	for name, deps := range map[string]effects.RoomCreatedDeps{
		"no rooms": {JS: js},
		"no js":    {Rooms: memstore.NewRooms()},
	} {
		if _, err := effects.NewRoomCreated(deps, cfg); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Fatalf("%s: NewRoomCreated = %v, want ErrInvalidArgument", name, err)
		}
	}
	full := effects.RoomCreatedDeps{Rooms: memstore.NewRooms(), JS: js}
	if _, err := effects.NewRoomCreated(full, effects.RoomCreatedConfig{}); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("NewRoomCreated without a subject root = %v, want ErrInvalidArgument", err)
	}
	eff, err := effects.NewRoomCreated(full, cfg)
	if err != nil {
		t.Fatalf("NewRoomCreated with defaults: %v", err)
	}
	if got := eff.Effect().Delay; got != effects.DefaultDelay {
		t.Fatalf("default delay = %v, want %v", got, effects.DefaultDelay)
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/effects/..."`
Expected: FAIL biên dịch: `undefined: effects.RoomCreated`, `effects.NewRoomCreated`, `effects.RoomCreatedName`.

**Step 3: Code**

`apps/core/internal/effects/room_created.go`:

```go
package effects

import (
	"cmp"
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

const RoomCreatedName = "room_created"

type RoomCreatedDeps struct {
	Rooms RoomReader
	JS    publish.JetStream
}

type RoomCreatedConfig struct {
	SubjectRoot string
	Delay       time.Duration
}

type RoomCreated struct {
	deps        RoomCreatedDeps
	cfg         RoomCreatedConfig
	republished atomic.Uint64
	dropped     atomic.Uint64
}

func NewRoomCreated(deps RoomCreatedDeps, cfg RoomCreatedConfig) (*RoomCreated, error) {
	if deps.Rooms == nil || deps.JS == nil {
		return nil, fmt.Errorf("%w: room_created needs rooms and a jetstream client", apperr.ErrInvalidArgument)
	}
	cfg.Delay = cmp.Or(cfg.Delay, DefaultDelay)
	if cfg.SubjectRoot == "" || cfg.Delay < 0 {
		return nil, fmt.Errorf("%w: room_created config %+v needs a subject root and a delay", apperr.ErrInvalidArgument, cfg)
	}
	return &RoomCreated{deps: deps, cfg: cfg}, nil
}

func (e *RoomCreated) Effect() Effect {
	return Effect{Name: RoomCreatedName, Delay: e.cfg.Delay, Run: e.run}
}

func (e *RoomCreated) Republished() uint64 { return e.republished.Load() }

func (e *RoomCreated) Dropped() uint64 { return e.dropped.Load() }

func (e *RoomCreated) run(ctx context.Context, recs []work.Record) []error {
	errs := make([]error, len(recs))
	var pending []pendingAck
	for i, r := range recs {
		room, err := e.deps.Rooms.Get(ctx, r.Room)
		switch {
		case undeliverable(err):
			e.dropped.Add(1)
			continue
		case err != nil:
			errs[i] = err
			continue
		}
		msg, err := publish.Message(e.cfg.SubjectRoot, room.ID, pbconv.RoomCreated(room))
		if err != nil {
			e.dropped.Add(1)
			continue
		}
		pending = send(e.deps.JS, msg, i, errs, pending)
	}
	awaitAcks(ctx, pending, errs, &e.republished)
	return errs
}
```

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/effects/..."`
Expected: PASS. `wc -l apps/core/internal/effects/*.go` đều < 200.

**Step 5: INDEXES + commit**

`INDEXES.csv`, dòng `apps/core/internal/effects`: nối vào purpose `; room_created effect (delay RECONCILE_DELAY, no ack mark): Rooms.Get, event rebuilt with pbconv.RoomCreated (fast path parity), publish and wait for the PubAck; unknown room dropped and counted`; key_symbols thêm `RoomCreated;NewRoomCreated;RoomCreated.Effect;RoomCreated.Republished;RoomCreated.Dropped;RoomCreatedDeps;RoomCreatedConfig;RoomCreatedName`.

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/effects/ INDEXES.csv
git commit -m "feat(effects): republish room_created events from the worker"
```

---
#### Ghi chú cho controller (part B)

**Thêm vào hợp đồng (không đổi chữ ký có sẵn, chỉ thêm ký hiệu mới):**
- `work`: `DefaultPartitions = 32`, `DefaultMaxAge = 2h`, `DefaultDuplicates = 2m`, `DefaultAckWait = 35s`, `MaxAckPending = 1024` (consumer dùng, `effects.Config` chặn `FetchBatch ≤ MaxAckPending`), `type BadRecordsError struct{ Terminated uint64 }` (unwrap ra `ErrBadRecord`). Implementation của `Queue.Fetch` đặt tên tham số `limit` (interface vẫn `max` như hợp đồng).
- `worktest`: `ErrSettled` (ack/nak lần hai); `Broker` dùng được ở giá trị zero (không có constructor).
- `effects`: `DefaultFetchBatch/FetchWait/RetryDelay/Drain/Poll`, `DefaultDelay = 5s`, `DefaultRoomCache = 65536`, `MessageCreatedName`, `RoomCreatedName`, interface `Marks`, `MessageFinder`, `RoomReader` (file `ports.go`), kiểu deps/config `MessageCreatedDeps{Marks, Messages, Rooms, JS}`, `MessageCreatedConfig{SubjectRoot, Delay, RoomCache}`, `RoomCreatedDeps{Rooms, JS}`, `RoomCreatedConfig{SubjectRoot, Delay}`. Hai constructor trả `(*T, error)` có method `Effect() Effect`, `Republished()`, `Dropped()`; (đã ghi vào hợp đồng). `NewRoomActivity` (Task 11) trả `*RoomActivity` chỉ có `Effect()`, như hợp đồng.
- `reconcile`: bỏ `Reconciler.Dropped()` (chỉ test dùng; giờ đọc `Stats().Dropped`) và `Marks`, `RoomReader`, `DefaultDelay`, `DefaultRoomCache`.
- `config`: các hằng `defaultEffectDelay`, `defaultEffectRoomCache`, `workAckSlack` (không export). Task 12 có thể đổi sang `effects.DefaultDelay`/`effects.DefaultRoomCache`.

**Quyết định cần controller biết:**
- Record không giải mã được: adapter `Term()` + trả `BadRecordsError`; worker cộng vào `Failed` (alert `work_failures_total` sẽ kêu, đúng ý: record hỏng là bất thường).
- `Close` của worker: dừng fetch ngay (ctx fetch bị huỷ), effect đang chạy chạy xong, record đang chờ delay `Nak(0)`. Nghĩa là `StopPlan.Workers` chỉ cần phủ một lô effect đang chạy (find + publish + chờ PubAck của tối đa `WORK_FETCH_BATCH` tin), không phủ delay 5s. Đề xuất Task 12: bước `workers` = `WORK_DRAIN + CloseTimeout` = 2s, nên tổng mặc định 24.2s + 2s = 26.2s < 28s.
- Worker dùng ctx của `Run` cho effect; nếu `Close` hết hạn và supervisor huỷ ctx, record lỗi do huỷ được `Nak(0)`, không đếm `Failed`.
- Itest `apps/core` dùng `WORK_STREAM`/`WORK_SUBJECT_ROOT` riêng mỗi lần chạy và `forget` xoá stream đó (Task 6), tránh đụng stream `CHATIM_WORK` của `make core-up`.

**Caller đã sửa trong part B:** `apps/core/wiring.go` (`prepare` Task 6, `reconcile.Deps` Task 7), `apps/core/metrics_wiring.go` + test (Task 7), `apps/core/it_infra_test.go` (Task 6), `apps/core/reconcile_integration_test.go` (Task 7), `config/{config,components,validate}.go` + `env_test/load_test/validate_test` (Task 6, 7), `README.md`, `INDEXES.csv`. Task 7 xoá guard `c.Kind == store.RoomInserted` của Task 3 (viết lại `term.go`) và xoá `reconcile/room_change_test.go`; harness reconcile gọi `memstore.NewFeed(rg.msgs, rg.rooms)` và đo vị trí confirm tương đối với mốc lúc tạo feed (`rg.base`), vì log chung có thể đã chứa room của rig.

**Rủi ro:**
- Từ Task 7 tới Task 12 không ai bù event bị rớt ở fast path (record nằm chờ trong `CHATIM_WORK`). Không deploy `feat/m2b` giữa chừng. Record tồn tối đa `WORK_MAX_AGE` 2h; dev chạy `make core-up` lâu hơn thì stream tự dọn.
- Từ Task 7 tới Task 13, `reconcile_lag_seconds` và `reconcile_republished_total` không còn; luật `ChatimReconcilerLagging`, `ChatimRepublishSurge` không có dữ liệu (`make alerts-check` vẫn xanh).
- `AckWait = RECONCILE_DELAY + 30s`: một lô `msg_created` 256 tin phải find + publish + chờ PubAck trong ~30s, dư nhiều; quá thì record được giao lại khi lô cũ còn chạy (effect idempotent nên chỉ tốn công).
- `jetstream` `Fetch` có thể đẩy sẵn tin vào bộ đệm của lô; khi ctx bị huỷ lúc dừng, tin chưa đọc quay lại sau `AckWait` (35s) thay vì ngay. Chỉ xảy ra lúc dừng.
- Đổi `WORK_PARTITIONS` khi đang chạy: consumer cũ `p ≥ P mới` còn record không ai kéo tới khi `MaxAge`. Ghi ở README (Task 6); docs Task 16 nên nhắc xả hàng trước.
- `gosec` G115: `effects.slotOf` có chặn biên; các bộ đếm tăng từng record (`Add(1)`) hoặc dùng `uint64` (`BadRecordsError.Terminated`) để tránh chuyển `int → uint64`.
- `Partition` và `Subject` của Task 5 được dùng trong test (`work.Subject("work", p)`), không viết cứng chuỗi `work.p0`.

**Part C cần biết:**
- Task 11 `room_activity`: đăng ký trước `msg_created` trong `Registry[store.MessageInserted]` (delay 0 chạy ngay, `msg_created` chờ 5s sau), vì worker chạy effect của một kind tuần tự theo thứ tự registry và mỗi effect chờ `max(CommittedAt)+Delay` riêng. `RoomInserted` cũng có thể cần `room_activity` (`lc`) tuỳ hợp đồng Task 11.
- Task 12 wiring:
  - `effects.New(effects.Deps{Queue: func(p int) work.Queue { return work.NewQueue(js, cfg.Work.Name, p) }, Owner: slots, Registry: reg}, cfg.Effects, log)`.
  - JetStream client cho effect publish phải đặt `jetstream.WithPublishAsyncTimeout(PUB_ACK_TIMEOUT)` (không có timeout thì future có thể không bao giờ xong và worker chỉ thoát khi ctx bị huỷ) và `WithPublishAsyncMaxPending ≥ WORK_PARTITIONS × WORK_FETCH_BATCH` (8192 mặc định; mặc định nats.go 4000 sẽ làm `PublishMsgAsync` trả "too many stalled" → nak + retry). Có thể dùng chung client với `Queue` vì `Fetch` không đi qua async publish.
  - `NewMessageCreated` cần `Marks: marks` (eventmark), `Messages: st`, `Rooms: st`, `JS: <client effect>`, `SubjectRoot: cfg.Stream.SubjectRoot` (gốc **event**, không phải work), `Delay: cfg.EffectDelay`, `RoomCache: cfg.EffectRoomCache`. `NewRoomCreated` tương tự (`Delay: cfg.EffectDelay`).
  - Thứ tự dừng gợi ý: grpc → reader → **workers** → router … (worker ngừng trước khi publisher và slot release; worker cần slot manager vẫn chạy để `Owns` đúng tới lúc dừng).
- Task 13 metrics: `work_failures_total` = `effects.Stats().Failed`; `reconcile_lag_seconds` = `Workers.Stats().Lag`; `reconcile_republished_total{effect}` = `MessageCreated.Republished()`, `RoomCreated.Republished()`; dropped của effect = `Dropped()`; cập nhật `metrics_wiring_test` (Task 7 đang liệt kê `reconcile_running`, `reconcile_terms_total`, `reconcile_forwarded_total`, `reconcile_dropped_total`, `reconcile_history_lost_total`).
- Task 15: khôi phục khẳng định event live end-to-end (Task 7 đổi `reconcile_integration_test.go` sang khẳng định record `r:{room}`, `m:{room}-0-1` trong work stream qua `awaitRecords`, có thể giữ cả hai).
- Task 16 docs: luật boot mới `WORK_DUPLICATES > RECONCILE_CONFIRM_EVERY + RECONCILE_DRAIN`; luật delay áp dụng cả khi `RECONCILE_ENABLED=false`; README đã có dòng `WORK_*` và đã sửa các dòng `RECONCILE_*` (Task 6, 7); `CLAUDE.md` mục "Event reconciliation" cần viết lại theo reader + worker.

### Task 11: Room activity — store, index, effect `room_activity`

Room activity là projection hội tụ (D69): `rooms.ls/lm/lc/ab` chỉ đi lên (`$max`), worker ghi theo lô fetch (≤ 1 write mỗi room mỗi lô). `ActiveRooms` là chỉ mục cho `/app resync` (Task 14).

**Lệch hợp đồng có chủ đích (ghi ở "Ghi chú cho controller"):** nhánh `ab` của `ActiveRooms` dùng `ab >= giờ(From)` (không chặn trên bằng `giờ(To)`). `ab` là `$max` nên chỉ giữ giờ hoạt động **cuối**; room hoạt động trong khoảng mất và còn hoạt động sau `To` có `ab > giờ(To)`, chặn trên sẽ bỏ sót đúng những room bận nhất. Resync scan ngược nên tin sau `To` chỉ bị bỏ qua, không phát record. Nhánh `ca` giữ `[From, To]`. Thêm index `{ca: 1}` cạnh `{ab: 1}` để mỗi nhánh `$or` có index (thiếu một nhánh thì Mongo quét cả collection); `ca` không đổi sau khi tạo nên không gây churn (lý do D69 loại `{last_msg_at}` không áp dụng).

Hàm phụ thêm vào hợp đồng (cộng thêm, không đổi chữ ký đã chốt): `store.HourBucket(t) int64`, `store.MaxActiveLimit = 1000`, `(store.ActiveQuery).Validate() error` — để hai adapter và test dùng chung một định nghĩa giờ và một luật kiểm.

Caller của `store.Rooms` (đã grep `store\.Rooms\b` và `Member(` trong `apps tools pkg`): adapter `memstore.Rooms`, `mongostore.Store`; `actor/spy_stores_test.go` nhúng `*memstore.Rooms` nên tự có method mới; còn lại chỉ dùng interface (`grpcsrv`, `actor`, `access` test). Không caller nào khác phải sửa.

**Files:**
- Create: `apps/core/internal/store/activity.go`
- Create: `apps/core/internal/store/activity_test.go`
- Modify: `apps/core/internal/store/ports.go`
- Modify: `apps/core/internal/store/write_contract_test.go`
- Modify: `apps/core/internal/domain/room.go`
- Create: `apps/core/internal/store/storetest/activity_cases.go`
- Modify: `apps/core/internal/store/storetest/storetest.go`
- Create: `apps/core/internal/store/memstore/room_activity.go`
- Create: `apps/core/internal/store/mongostore/room_activity.go`
- Modify: `apps/core/internal/store/mongostore/codec.go`, `codec_test.go`
- Modify: `apps/core/internal/store/mongostore/bootstrap.go`, `bootstrap_integration_test.go`
- Create: `apps/core/internal/effects/room_activity.go`
- Create: `apps/core/internal/effects/room_activity_test.go`
- Modify: `INDEXES.csv`

**Step 1: Test**

`apps/core/internal/store/activity_test.go`:

```go
package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func TestHourBucketCountsWholeUTCHours(t *testing.T) {
	at := time.Date(2026, 10, 5, 9, 59, 59, 0, time.FixedZone("ICT", 7*3600))
	if got, want := store.HourBucket(at), at.UTC().Unix()/3600; got != want {
		t.Fatalf("HourBucket(%v) = %d, want %d", at, got, want)
	}
	if store.HourBucket(at.Add(-59*time.Minute)) != store.HourBucket(at) {
		t.Fatalf("HourBucket splits one UTC hour")
	}
	if store.HourBucket(at.Add(time.Second)) != store.HourBucket(at)+1 {
		t.Fatalf("HourBucket does not move at the top of the hour")
	}
}

func TestActiveQueryValidate(t *testing.T) {
	from := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		q    store.ActiveQuery
		ok   bool
	}{
		{"one hour", store.ActiveQuery{From: from, To: from.Add(time.Hour), Limit: 1}, true},
		{"single instant at the max limit", store.ActiveQuery{From: from, To: from, Limit: store.MaxActiveLimit}, true},
		{"zero limit", store.ActiveQuery{From: from, To: from, Limit: 0}, false},
		{"limit above max", store.ActiveQuery{From: from, To: from, Limit: store.MaxActiveLimit + 1}, false},
		{"no start", store.ActiveQuery{To: from, Limit: 1}, false},
		{"end before start", store.ActiveQuery{From: from, To: from.Add(-time.Second), Limit: 1}, false},
	}
	for _, tt := range tests {
		err := tt.q.Validate()
		if tt.ok != (err == nil) || (err != nil && !errors.Is(err, apperr.ErrInvalidArgument)) {
			t.Errorf("%s: Validate() = %v, want ok=%v", tt.name, err, tt.ok)
		}
	}
}
```

`apps/core/internal/store/write_contract_test.go`: thêm loại ghi `monotonic-max` (bulk `$max`, không bao giờ lùi, hội tụ khi chạy lại):

```go
var allowedKinds = map[string]bool{
	"read": true, "lifecycle": true, "reset": true,
	"insert-unique": true, "cas": true, "monotonic-cas": true, "monotonic-max": true, "upsert": true, "version-bump": true,
}
```

Trong `portMethods`, sau dòng `"Rooms.Member":      "read",` thêm:

```go
	"Rooms.TouchActivity": "monotonic-max",
	"Rooms.ActiveRooms":   "read",
```

(gofmt căn lại cột của cả map). Đổi thông báo lỗi `"%s has kind %q; writes must be insert-unique, cas, monotonic-cas, upsert or version-bump"` thành `"%s has kind %q; writes must be insert-unique, cas, monotonic-cas, monotonic-max, upsert or version-bump"`.

`apps/core/internal/store/storetest/activity_cases.go`:

```go
package storetest

import (
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

func activityCases() []roomsCase {
	return []roomsCase{
		{"touch activity keeps the highest seq and time and never moves back", activityMonotonic},
		{"thread activity only bumps the change time and bucket", activityThread},
		{"touch of a missing room is ignored", activityMissingRoom},
		{"active rooms: touched since from or created in range, by tenant, paged by id", activeRoomsRange},
		{"active rooms rejects a bad limit or range", activeRoomsInvalid},
	}
}

func roomAt(id uint64, tenantID string, created time.Time) (domain.Room, []domain.Member) {
	r := domain.Room{ID: id, Tenant: tenantID, Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: created, MemberCount: 1}
	return r, []domain.Member{{Room: id, Tenant: tenantID, User: "alice", Role: domain.RoleOwner, JoinedAt: created}}
}

func mustTouch(t *testing.T, s store.Rooms, acts ...store.Activity) {
	t.Helper()
	if err := s.TouchActivity(t.Context(), acts); err != nil {
		t.Fatalf("TouchActivity(%+v): %v", acts, err)
	}
}

func assertActivity(t *testing.T, s store.Rooms, id, seq uint64, msgAt, changeAt time.Time) {
	t.Helper()
	got, err := s.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("Get(%d): %v", id, err)
	}
	if got.LastSeq != seq || !got.LastMsgAt.Equal(msgAt) || !got.LastChangeAt.Equal(changeAt) {
		t.Fatalf("room %d activity = seq %d, msg %v, change %v; want seq %d, msg %v, change %v",
			id, got.LastSeq, got.LastMsgAt, got.LastChangeAt, seq, msgAt, changeAt)
	}
}

func activeIDs(t *testing.T, s store.Rooms, q store.ActiveQuery) []uint64 {
	t.Helper()
	rooms, err := s.ActiveRooms(t.Context(), q)
	if err != nil {
		t.Fatalf("ActiveRooms(%+v): %v", q, err)
	}
	out := make([]uint64, len(rooms))
	for i, r := range rooms {
		out[i] = r.ID
	}
	return out
}

func activityMonotonic(t *testing.T, s store.Rooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	early, late := baseTime.Add(time.Hour), baseTime.Add(2*time.Hour)
	mustTouch(t, s, store.Activity{Room: roomA, Seq: 5, At: late}, store.Activity{Room: roomA, Seq: 3, At: early})
	assertActivity(t, s, roomA, 5, late, late)
	mustTouch(t, s, store.Activity{Room: roomA, Seq: 4, At: early})
	assertActivity(t, s, roomA, 5, late, late)
	mustTouch(t, s)
	assertActivity(t, s, roomA, 5, late, late)
}

func activityThread(t *testing.T, s store.Rooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	threadAt := baseTime.Add(3 * time.Hour)
	mustTouch(t, s, store.Activity{Room: roomA, Seq: 2, At: baseTime})
	mustTouch(t, s, store.Activity{Room: roomA, Thread: sideThread, Seq: 50, At: threadAt})
	assertActivity(t, s, roomA, 2, baseTime, threadAt)
	if got := activeIDs(t, s, store.ActiveQuery{From: threadAt, To: threadAt, Limit: 10}); !slices.Equal(got, []uint64{roomA}) {
		t.Fatalf("active rooms in the thread's hour = %v, want [%d]", got, roomA)
	}
}

func activityMissingRoom(t *testing.T, s store.Rooms) {
	room, members := teamOf(roomA)
	mustCreate(t, s, room, members)
	mustTouch(t, s, store.Activity{Room: roomB, Seq: 3, At: baseTime}, store.Activity{Room: roomA, Seq: 1, At: baseTime})
	assertNoRoom(t, s, roomB)
	assertActivity(t, s, roomA, 1, baseTime, baseTime)
}

func activeRoomsRange(t *testing.T, s store.Rooms) {
	old := baseTime.Add(-48 * time.Hour)
	for _, r := range []struct {
		id       uint64
		tenantID string
		created  time.Time
	}{
		{roomA, tenant, baseTime.Add(10 * time.Minute)},
		{roomB, tenant, old},
		{roomB + 1, tenant, old},
		{roomB + 2, tenant, old},
		{roomB + 3, "other", baseTime},
	} {
		room, members := roomAt(r.id, r.tenantID, r.created)
		mustCreate(t, s, room, members)
	}
	mustTouch(t, s,
		store.Activity{Room: roomB, Seq: 9, At: baseTime.Add(5 * time.Hour)},
		store.Activity{Room: roomB + 2, Seq: 4, At: baseTime.Add(-3 * time.Hour)},
	)
	q := store.ActiveQuery{From: baseTime, To: baseTime.Add(time.Hour), Limit: 10}
	steps := []struct {
		name string
		edit func(*store.ActiveQuery)
		want []uint64
	}{
		{"every tenant", func(*store.ActiveQuery) {}, []uint64{roomA, roomB, roomB + 3}},
		{"one tenant", func(q *store.ActiveQuery) { q.Tenant = tenant }, []uint64{roomA, roomB}},
		{"first page", func(q *store.ActiveQuery) { q.Limit = 1 }, []uint64{roomA}},
		{"second page", func(q *store.ActiveQuery) { q.After = roomA }, []uint64{roomB}},
		{"past the last page", func(q *store.ActiveQuery) { q.After = roomB }, []uint64{}},
	}
	for _, step := range steps {
		step.edit(&q)
		if got := activeIDs(t, s, q); !slices.Equal(got, step.want) {
			t.Fatalf("%s: ActiveRooms(%+v) = %v, want %v", step.name, q, got, step.want)
		}
	}
}

func activeRoomsInvalid(t *testing.T, s store.Rooms) {
	for _, q := range []store.ActiveQuery{
		{From: baseTime, To: baseTime, Limit: 0},
		{From: baseTime, To: baseTime, Limit: store.MaxActiveLimit + 1},
		{From: baseTime, To: baseTime.Add(-time.Second), Limit: 1},
	} {
		_, err := s.ActiveRooms(t.Context(), q)
		assertErrorIs(t, "ActiveRooms", err, apperr.ErrInvalidArgument)
	}
}
```

Ý nghĩa `activeRoomsRange`: `roomA` tạo trong khoảng (nhánh `ca`); `roomB` hoạt động sau `To` vẫn được tìm (nhánh `ab >= giờ(From)`); `roomB+1` cũ, không hoạt động → loại; `roomB+2` chỉ hoạt động trước `From` → loại; `roomB+3` khác tenant → chỉ có khi không lọc tenant.

`apps/core/internal/store/storetest/storetest.go`, trong `t.Run("Rooms", ...)` thay:

```go
		for _, c := range roomsCases() {
```

bằng:

```go
		for _, c := range append(roomsCases(), activityCases()...) {
```

`apps/core/internal/store/mongostore/codec_test.go`, thêm sau `TestRoomCodecRoundTrip`:

```go
func TestRoomCodecDecodesActivity(t *testing.T) {
	d := roomDoc{
		ID: 7_340_000_001, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: codecTime, MemberCount: 2,
		LastSeq: 42, LastMsgAt: codecTime.Add(time.Minute), LastChangeAt: codecTime.Add(2 * time.Minute),
	}
	got, err := decodeRoom(d)
	if err != nil || got.LastSeq != 42 || !got.LastMsgAt.Equal(d.LastMsgAt) || !got.LastChangeAt.Equal(d.LastChangeAt) {
		t.Fatalf("decodeRoom = %+v, %v; want seq 42 at %v / %v", got, err, d.LastMsgAt, d.LastChangeAt)
	}
	d.LastSeq = -1
	if _, err := decodeRoom(d); !errors.Is(err, errCorrupt) {
		t.Fatalf("decodeRoom(negative last seq) = %v, want errCorrupt", err)
	}
}
```

`TestRoomCodecRoundTrip` giữ nguyên: room mới không có activity nên field vẫn đúng `_id, t, ty, n, cb, ca, mc` (field activity có `omitempty`).

`apps/core/internal/store/mongostore/bootstrap_integration_test.go`: thêm sau `assertMemberIndexes`:

```go
func assertRoomIndexes(t *testing.T, db *mongo.Database) {
	t.Helper()
	got := indexKeys(t, db.Collection(roomsCollection))
	for _, k := range []string{"ab:1", "ca:1"} {
		if unique, ok := got[k]; !ok || unique {
			t.Fatalf("rooms indexes = %v, want non-unique %s", got, k)
		}
	}
}
```

và trong `TestBootstrapIsIdempotent`, sau dòng `assertMemberIndexes(t, db)` thêm `assertRoomIndexes(t, db)`.

`apps/core/internal/effects/room_activity_test.go` (tên helper có tiền tố `activity`/`touch` để không đụng helper của Task 8–10 trong cùng package test):

```go
package effects_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

var (
	activityAt     = time.UnixMilli(1_759_600_000_000).UTC()
	errTouchFailed = errors.New("touch failed")
)

type touchSpy struct {
	calls [][]store.Activity
	err   error
}

func (s *touchSpy) TouchActivity(_ context.Context, acts []store.Activity) error {
	s.calls = append(s.calls, slices.Clone(acts))
	return s.err
}

func activityRecord(room, thread, seq uint64, at time.Time) work.Record {
	return work.Record{Kind: store.MessageInserted, Room: room, Thread: thread, Seq: seq, CommittedAt: at}
}

func TestRoomActivityTouchesTheHighestSeqPerTimelineInOneWrite(t *testing.T) {
	spy := &touchSpy{}
	fx := effects.NewRoomActivity(spy).Effect()
	if fx.Name != effects.RoomActivityName || fx.Name != "room_activity" || fx.Delay != 0 {
		t.Fatalf("effect = %q with delay %v, want room_activity with delay 0", fx.Name, fx.Delay)
	}
	t1, t2, t3 := activityAt, activityAt.Add(time.Second), activityAt.Add(2*time.Second)
	recs := []work.Record{
		activityRecord(101, 0, 3, t1), activityRecord(101, 0, 5, t2), activityRecord(101, 7, 2, t1),
		activityRecord(202, 0, 1, t3), activityRecord(101, 0, 4, t3),
	}
	errs := fx.Run(t.Context(), recs)
	if len(errs) != len(recs) || slices.ContainsFunc(errs, func(err error) bool { return err != nil }) {
		t.Fatalf("Run errors = %v, want %d nils", errs, len(recs))
	}
	want := []store.Activity{{Room: 101, Seq: 5, At: t3}, {Room: 101, Thread: 7, Seq: 2, At: t1}, {Room: 202, Seq: 1, At: t3}}
	if len(spy.calls) != 1 || !slices.Equal(spy.calls[0], want) {
		t.Fatalf("TouchActivity calls = %+v, want one call with %+v", spy.calls, want)
	}
}

func TestRoomActivityFailsEveryRecordWhenTheWriteFails(t *testing.T) {
	spy := &touchSpy{err: errTouchFailed}
	recs := []work.Record{activityRecord(101, 0, 1, activityAt), activityRecord(202, 0, 1, activityAt)}
	errs := effects.NewRoomActivity(spy).Effect().Run(t.Context(), recs)
	if len(errs) != len(recs) {
		t.Fatalf("Run returned %d errors for %d records", len(errs), len(recs))
	}
	for i, err := range errs {
		if !errors.Is(err, errTouchFailed) {
			t.Errorf("errs[%d] = %v, want %v", i, err, errTouchFailed)
		}
	}
}

func TestRoomActivityNeverMovesAStoredRoomBack(t *testing.T) {
	rooms := memstore.NewRooms()
	r := domain.Room{ID: 101, Tenant: "acme", Type: domain.RoomGroup, Name: "Team", CreatedBy: "alice", CreatedAt: activityAt, MemberCount: 1}
	owner := domain.Member{Room: 101, Tenant: "acme", User: "alice", Role: domain.RoleOwner, JoinedAt: activityAt}
	if err := rooms.Create(t.Context(), r, []domain.Member{owner}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	fx := effects.NewRoomActivity(rooms).Effect()
	for _, rec := range []work.Record{activityRecord(101, 0, 9, activityAt.Add(time.Minute)), activityRecord(101, 0, 4, activityAt.Add(2*time.Minute))} {
		if errs := fx.Run(t.Context(), []work.Record{rec}); errs[0] != nil {
			t.Fatalf("Run(%+v) = %v", rec, errs[0])
		}
	}
	got, err := rooms.Get(t.Context(), 101)
	if err != nil || got.LastSeq != 9 || !got.LastMsgAt.Equal(activityAt.Add(2*time.Minute)) {
		t.Fatalf("room = %+v, %v; want last seq 9 at %v", got, err, activityAt.Add(2*time.Minute))
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/... ./apps/core/internal/effects/..."`
Expected: FAIL biên dịch: `undefined: store.HourBucket`, `undefined: store.ActiveQuery`, `s.TouchActivity undefined (type store.Rooms has no field or method TouchActivity)`, `unknown field LastSeq in struct literal of type roomDoc`, `undefined: effects.NewRoomActivity`.

**Step 3: Code — port và domain**

`apps/core/internal/store/activity.go`:

```go
package store

import "time"

const MaxActiveLimit = 1000

type Activity struct {
	Room   uint64
	Thread uint64
	Seq    uint64
	At     time.Time
}

type ActiveQuery struct {
	From, To time.Time
	Tenant   string
	After    uint64
	Limit    int
}

func HourBucket(t time.Time) int64 { return t.UTC().Unix() / 3600 }

func (q ActiveQuery) Validate() error {
	switch {
	case q.Limit < 1 || q.Limit > MaxActiveLimit:
		return invalid("active rooms limit")
	case q.From.IsZero() || q.To.Before(q.From):
		return invalid("active rooms range")
	default:
		return nil
	}
}
```

`apps/core/internal/store/ports.go`: `Rooms` thành (không cần import mới; `Activity`/`ActiveQuery` cùng package):

```go
type Rooms interface {
	Create(ctx context.Context, r domain.Room, members []domain.Member) error
	Get(ctx context.Context, id uint64) (domain.Room, error)
	Member(ctx context.Context, room uint64, user string) (domain.Member, error)
	TouchActivity(ctx context.Context, acts []Activity) error
	ActiveRooms(ctx context.Context, q ActiveQuery) ([]domain.Room, error)
}
```

`apps/core/internal/domain/room.go`: `Room` thành

```go
type Room struct {
	ID           uint64
	Tenant       string
	Type         RoomType
	Name         string
	CreatedBy    string
	CreatedAt    time.Time
	MemberCount  int
	LastSeq      uint64
	LastMsgAt    time.Time
	LastChangeAt time.Time
}
```

**Step 4: Code — memstore**

`apps/core/internal/store/memstore/room_activity.go` (dùng `s.mu`, `s.rooms` có sẵn của `Rooms`; `ab` không lưu riêng vì `ab = giờ(lc)` khi cả hai cùng là `$max`):

```go
package memstore

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
)

func (s *Rooms) TouchActivity(ctx context.Context, acts []store.Activity) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range acts {
		if r, ok := s.rooms[a.Room]; ok {
			s.rooms[a.Room] = withActivity(r, a)
		}
	}
	return nil
}

func (s *Rooms) ActiveRooms(ctx context.Context, q store.ActiveQuery) ([]domain.Room, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := q.Validate(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []domain.Room{}
	for _, r := range s.rooms {
		if r.ID > q.After && (q.Tenant == "" || r.Tenant == q.Tenant) && activeFor(r, q) {
			out = append(out, r)
		}
	}
	slices.SortFunc(out, func(a, b domain.Room) int { return cmp.Compare(a.ID, b.ID) })
	return out[:min(len(out), q.Limit)], nil
}

func withActivity(r domain.Room, a store.Activity) domain.Room {
	at := a.At.UTC()
	r.LastChangeAt = laterOf(r.LastChangeAt, at)
	if a.Thread == 0 {
		r.LastSeq = max(r.LastSeq, a.Seq)
		r.LastMsgAt = laterOf(r.LastMsgAt, at)
	}
	return r
}

func activeFor(r domain.Room, q store.ActiveQuery) bool {
	created := !r.CreatedAt.Before(q.From) && !r.CreatedAt.After(q.To)
	touched := !r.LastChangeAt.IsZero() && store.HourBucket(r.LastChangeAt) >= store.HourBucket(q.From)
	return created || touched
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
```

Nếu Task 3 đổi tên trường `mu`/`rooms` của `memstore.Rooms`, dùng tên mới (không đổi hành vi).

**Step 5: Code — mongostore**

`apps/core/internal/store/mongostore/codec.go`, `roomDoc` thêm ba trường (không có `ab` trong struct: chỉ ghi qua `$max`, đọc không cần, và `unused` sẽ báo trường không dùng):

```go
type roomDoc struct {
	ID           int64           `bson:"_id"`
	Tenant       string          `bson:"t"`
	Type         domain.RoomType `bson:"ty"`
	Name         string          `bson:"n"`
	CreatedBy    string          `bson:"cb"`
	CreatedAt    time.Time       `bson:"ca"`
	MemberCount  int             `bson:"mc"`
	LastSeq      int64           `bson:"ls,omitempty"`
	LastMsgAt    time.Time       `bson:"lm,omitempty"`
	LastChangeAt time.Time       `bson:"lc,omitempty"`
}
```

`encodeRoom` giữ nguyên (room mới chưa có activity; activity chỉ đổi qua `TouchActivity`). `decodeRoom` thành:

```go
func decodeRoom(d roomDoc) (domain.Room, error) {
	id, err := toUint64("room id", d.ID)
	if err != nil {
		return domain.Room{}, err
	}
	lastSeq, err := toUint64("room last seq", d.LastSeq)
	if err != nil {
		return domain.Room{}, err
	}
	return domain.Room{
		ID:           id,
		Tenant:       d.Tenant,
		Type:         d.Type,
		Name:         d.Name,
		CreatedBy:    d.CreatedBy,
		CreatedAt:    d.CreatedAt,
		MemberCount:  d.MemberCount,
		LastSeq:      lastSeq,
		LastMsgAt:    d.LastMsgAt,
		LastChangeAt: d.LastChangeAt,
	}, nil
}
```

`time.Time` có `IsZero()` nên `omitempty` của bson v2 bỏ field khi zero (đã kiểm `bson/struct_codec.go:isEmpty` với `Zeroer`).

`apps/core/internal/store/mongostore/room_activity.go`:

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

func (s *Store) TouchActivity(ctx context.Context, acts []store.Activity) error {
	models := make([]mongo.WriteModel, 0, len(acts))
	for _, a := range acts {
		room, roomErr := toInt64("room id", a.Room)
		seq, seqErr := toInt64("seq", a.Seq)
		if roomErr != nil || seqErr != nil || a.Room == 0 {
			continue
		}
		models = append(models, mongo.NewUpdateOneModel().
			SetFilter(bson.D{{Key: "_id", Value: room}}).
			SetUpdate(bson.D{{Key: "$max", Value: activityFields(a, seq)}}))
	}
	if len(models) == 0 {
		return ctx.Err()
	}
	if _, err := s.rooms.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false)); err != nil {
		return fmt.Errorf("touch activity of %d rooms: %w", len(models), err)
	}
	return nil
}

func activityFields(a store.Activity, seq int64) bson.D {
	at := a.At.UTC()
	fields := bson.D{{Key: "lc", Value: at}, {Key: "ab", Value: store.HourBucket(at)}}
	if a.Thread == 0 {
		fields = append(fields, bson.E{Key: "ls", Value: seq}, bson.E{Key: "lm", Value: at})
	}
	return fields
}

func (s *Store) ActiveRooms(ctx context.Context, q store.ActiveQuery) ([]domain.Room, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	after, err := toInt64("after", q.After)
	if err != nil {
		return nil, err
	}
	opts := options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(int64(q.Limit))
	cur, err := s.rooms.Find(ctx, activeFilter(q, after), opts)
	if err != nil {
		return nil, fmt.Errorf("active rooms after %d: %w", q.After, err)
	}
	var docs []roomDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("active rooms after %d: %w", q.After, err)
	}
	out := make([]domain.Room, 0, len(docs))
	for _, d := range docs {
		r, err := decodeRoom(d)
		if err != nil {
			return nil, fmt.Errorf("active rooms after %d: %w", q.After, err)
		}
		out = append(out, r)
	}
	return out, nil
}

func activeFilter(q store.ActiveQuery, after int64) bson.D {
	filter := bson.D{
		{Key: "_id", Value: bson.D{{Key: "$gt", Value: after}}},
		{Key: "$or", Value: bson.A{
			bson.D{{Key: "ab", Value: bson.D{{Key: "$gte", Value: store.HourBucket(q.From)}}}},
			bson.D{{Key: "ca", Value: bson.D{{Key: "$gte", Value: q.From}, {Key: "$lte", Value: q.To}}}},
		}},
	}
	if q.Tenant != "" {
		filter = append(filter, bson.E{Key: "t", Value: q.Tenant})
	}
	return filter
}
```

Không upsert: room không tồn tại thì `UpdateOne` khớp 0 doc, không lỗi. `$max` trên field chưa có thì đặt giá trị; lần chạy lại cùng lô hội tụ (P5 của §4).

`apps/core/internal/store/mongostore/bootstrap.go`: trong `Bootstrap` thay

```go
	if err := ensureMemberIndexes(ctx, db); err != nil {
		return err
	}
```

bằng

```go
	if err := ensureIndexes(ctx, db, membersCollection, memberIndexes()); err != nil {
		return err
	}
	if err := ensureIndexes(ctx, db, roomsCollection, roomIndexes()); err != nil {
		return err
	}
```

và thay cả hàm `ensureMemberIndexes` bằng:

```go
func memberIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{
		{Keys: bson.D{{Key: "r", Value: 1}, {Key: "u", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "t", Value: 1}, {Key: "u", Value: 1}, {Key: "r", Value: 1}}},
	}
}

func roomIndexes() []mongo.IndexModel {
	return []mongo.IndexModel{{Keys: bson.D{{Key: "ab", Value: 1}}}, {Keys: bson.D{{Key: "ca", Value: 1}}}}
}

func ensureIndexes(ctx context.Context, db *mongo.Database, coll string, models []mongo.IndexModel) error {
	if _, err := db.Collection(coll).Indexes().CreateMany(ctx, models); err != nil {
		return fmt.Errorf("bootstrap %s: create indexes: %w", coll, err)
	}
	return nil
}
```

(`grep -rn ensureMemberIndexes apps/` chỉ có hai chỗ trong `bootstrap.go`.)

**Step 6: Code — effect `room_activity`**

`apps/core/internal/effects/room_activity.go`:

```go
package effects

import (
	"context"
	"fmt"

	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

type ActivityWriter interface {
	TouchActivity(ctx context.Context, acts []store.Activity) error
}

type RoomActivity struct {
	rooms ActivityWriter
}

type activityKey struct{ room, thread uint64 }

const RoomActivityName = "room_activity"

func NewRoomActivity(rooms ActivityWriter) *RoomActivity { return &RoomActivity{rooms: rooms} }

func (a *RoomActivity) Effect() Effect {
	return Effect{Name: RoomActivityName, Run: a.run}
}

func (a *RoomActivity) run(ctx context.Context, recs []work.Record) []error {
	errs := make([]error, len(recs))
	if len(recs) == 0 {
		return errs
	}
	if err := a.rooms.TouchActivity(ctx, latestActivity(recs)); err != nil {
		err = fmt.Errorf("room activity: %w", err)
		for i := range errs {
			errs[i] = err
		}
	}
	return errs
}

func latestActivity(recs []work.Record) []store.Activity {
	at := make(map[activityKey]int, len(recs))
	out := make([]store.Activity, 0, len(recs))
	for _, r := range recs {
		k := activityKey{r.Room, r.Thread}
		i, ok := at[k]
		if !ok {
			at[k] = len(out)
			out = append(out, store.Activity{Room: r.Room, Thread: r.Thread, Seq: r.Seq, At: r.CommittedAt})
			continue
		}
		out[i].Seq = max(out[i].Seq, r.Seq)
		if r.CommittedAt.After(out[i].At) {
			out[i].At = r.CommittedAt
		}
	}
	return out
}
```

`At` lấy `CommittedAt` (cluster time của commit, đúng hợp đồng), không lấy `CreatedAt` của tin. Effect không cần bộ đếm riêng: lỗi đã vào `work_failures_total` của worker. Hằng `RoomActivityName` theo cùng kiểu `MessageCreatedName`/`RoomCreatedName` của Task 9/10. `RoomInserted` không chạm activity: room mới được resync tìm qua nhánh `ca`.

**Step 7: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/store/... ./apps/core/internal/effects/... ./apps/core/internal/actor/... ./apps/core/internal/grpcsrv/... ./apps/core/internal/access/..."`
Expected: PASS (memstore chạy 5 case activity mới trong `Run/Rooms`; mongostore itest skip; actor/grpcsrv/access biên dịch lại với `store.Rooms` mới). `wc -l apps/core/internal/store/mongostore/codec.go apps/core/internal/store/storetest/activity_cases.go apps/core/internal/store/mongostore/bootstrap_integration_test.go` → mỗi file < 200.

**Step 8: Itest adapter Mongo**

Run: `make infra-up && make itest`
Expected: mọi package `ok`, gồm các case activity trong `TestMongoStoreContract/Rooms/...` và `TestBootstrapIsIdempotent` (index `ab:1`, `ca:1`).

**Step 9: INDEXES.csv**

- `apps/core/internal/store`: purpose thêm "; room activity port TouchActivity ($max, never back) and ActiveRooms (activity bucket since From or created in range, tenant, paged by id) with HourBucket"; key_symbols thêm `Activity;ActiveQuery;HourBucket;MaxActiveLimit`; decisions thêm `D69`.
- `apps/core/internal/store/memstore`: purpose thêm "; room activity in RAM (act bucket derived from last change)".
- `apps/core/internal/store/mongostore`: purpose thêm "; rooms ls/lm/lc/ab via unordered bulk $max (no upsert) and ActiveRooms query ($or ab >= hour(From) | ca in range, indexes {ab:1} {ca:1} from Bootstrap)"; decisions thêm `D69`.
- `apps/core/internal/store/storetest`: purpose thêm "; room activity cases (monotonic, thread, missing room, active range/tenant/paging)".
- `apps/core/internal/effects` (dòng do Task 8 thêm): key_symbols thêm `NewRoomActivity;RoomActivity;RoomActivity.Effect;ActivityWriter;RoomActivityName`; decisions thêm `D69`.

Kiểm: `python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"` → Expected: `{7}`.

**Step 10: Commit**

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/store/ apps/core/internal/domain/room.go apps/core/internal/effects/room_activity.go apps/core/internal/effects/room_activity_test.go INDEXES.csv
git commit -m "feat(core): add room activity projection and the room_activity effect"
```

---

### Task 12: Config `Effects`, vòng đời worker, ngân sách dừng 28s, compose, wiring

Worker chạy trên **mọi** core (không phụ thuộc `RECONCILE_ENABLED`, cờ này chỉ tắt reader). Thứ tự khởi động: … → router → slot manager → **workers** → reconciler → gRPC (worker cần `Owns(p)` nên đi sau slot manager; worker trước reader để record đầu tiên đã có người nhận). Thứ tự dừng: gRPC → reconciler → **workers** → router → … (reader ngừng đẩy record trước; worker xả lô đang chạy, record chưa ack được giao lại sau `AckWait`). `Close` của worker (Task 8): dừng fetch ngay, effect đang chạy chạy xong, record còn chờ delay được `Nak(0)`; nên mốc dừng chỉ cần phủ một lô effect đang chạy, không phủ delay 5s. Mốc dừng worker = `WORK_DRAIN + 1s` → kế hoạch mặc định 24.2s + 2s = **26.2s**, `CORE_SHUTDOWN_BUDGET` 25s → **28s**, compose `stop_grace_period` 30s → **33s** (owner chốt 2026-10-05).

Worker có JetStream client thứ ba `effectsJS` (`PublishMsgAsync` + chờ `PubAck` của `msg_created`/`room_created`; `Fetch` của `work.Queue` dùng chung client vì không đi qua async publish): tách khỏi publisher fast path (hàng đợi + err handler riêng) và reader (cửa sổ `RECONCILE_WINDOW`). Giới hạn publish đang bay = `WORK_PARTITIONS × WORK_FETCH_BATCH` (8192 mặc định): một core có thể giữ cả 32 partition, mỗi partition chạy một lô 256 tin cùng lúc; giới hạn nhỏ hơn (ví dụ `2 × WORK_FETCH_BATCH` hay mặc định 4000 của nats.go) làm `PublishMsgAsync` trả "too many stalled" → `Nak` + retry vô ích (phân tích của part B). Timeout = `PUB_ACK_TIMEOUT` để future luôn kết thúc.

**Files:**
- Create: `apps/core/internal/config/effects_components.go`
- Modify: `apps/core/internal/config/config.go`, `components.go`, `validate.go`
- Modify: `apps/core/internal/config/env_test.go`, `load_test.go`, `validate_test.go`
- Create: `apps/core/effects_wiring.go`
- Create: `apps/core/stop_order_test.go`
- Modify: `apps/core/wiring.go`, `lifecycle.go`, `shutdown.go`, `clients.go`, `startup_gate_test.go`
- Modify: `deploy/compose/docker-compose.yml` (`x-core.stop_grace_period`)
- Modify: `README.md` (bảng env), `INDEXES.csv`

**Step 1: Test config**

`apps/core/internal/config/env_test.go`: cuối `envKeys` (sau các key Task 6/7 thêm) thêm dòng

```go
	"WORK_FETCH_BATCH", "WORK_FETCH_WAIT", "WORK_RETRY_DELAY", "WORK_DRAIN",
```

và trong `overrides` thêm

```go
	"WORK_FETCH_BATCH": "64", "WORK_FETCH_WAIT": "500ms", "WORK_RETRY_DELAY": "2s", "WORK_DRAIN": "500ms",
```

`apps/core/internal/config/load_test.go`:
- import `"github.com/ivannguyendev/chatim/apps/core/internal/effects"`.
- `TestLoadDefaults`, `want`: đổi `ShutdownBudget: 25 * time.Second` thành `ShutdownBudget: 28 * time.Second`; thêm ngay sau trường `EffectRoomCache` (Task 7 thêm):

```go
		Effects: effects.Config{Partitions: 32, FetchBatch: 256, FetchWait: time.Second, RetryDelay: 5 * time.Second, Drain: time.Second, Poll: time.Second},
```

- `TestLoadOverrides`, `want`: thêm sau `EffectRoomCache`:

```go
		Effects: effects.Config{Partitions: 16, FetchBatch: 64, FetchWait: 500 * time.Millisecond, RetryDelay: 2 * time.Second, Drain: 500 * time.Millisecond, Poll: 500 * time.Millisecond},
```

  (`Partitions: 16` = `WORK_PARTITIONS` của `overrides` do Task 6 đặt.) `ShutdownBudget` giữ `20 * time.Second`: kế hoạch override = 1 + 4 + (0.5+1) + 2 + 0.1 + 0.5 + 3 + 5 + 1 + **(0.5+1)** = 19.6s < 20s.

`apps/core/internal/config/validate_test.go`, thay ba case ngân sách:

```go
		{"stop phases fill the budget", map[string]string{"CORE_SHUTDOWN_BUDGET": "24200ms"}, "CORE_SHUTDOWN_BUDGET"},
		{"stop phases just fit the budget", map[string]string{"CORE_SHUTDOWN_BUDGET": "24201ms"}, ""},
		{"cid batch drain follows the redis op timeout", map[string]string{"REDIS_OP_TIMEOUT": "300ms", "CORE_SHUTDOWN_BUDGET": "24600ms"}, "2 x REDIS_OP_TIMEOUT (cid batcher drain) + FLUSH_INSERT_TIMEOUT"},
```

bằng

```go
		{"stop phases fill the budget", map[string]string{"CORE_SHUTDOWN_BUDGET": "26200ms"}, "CORE_SHUTDOWN_BUDGET"},
		{"stop phases just fit the budget", map[string]string{"CORE_SHUTDOWN_BUDGET": "26201ms"}, ""},
		{"cid batch drain follows the redis op timeout", map[string]string{"REDIS_OP_TIMEOUT": "300ms", "CORE_SHUTDOWN_BUDGET": "26600ms"}, "2 x REDIS_OP_TIMEOUT (cid batcher drain) + FLUSH_INSERT_TIMEOUT"},
		{"worker drain counts in the stop plan", map[string]string{"WORK_DRAIN": "1001ms", "CORE_SHUTDOWN_BUDGET": "26201ms"}, "WORK_DRAIN + 1s"},
		{"zero fetch batch", map[string]string{"WORK_FETCH_BATCH": "0"}, "WORK_FETCH_BATCH"},
		{"fetch batch above the consumer max ack pending", map[string]string{"WORK_FETCH_BATCH": "1025"}, "WORK_*"},
		{"fetch batch at the consumer max ack pending", map[string]string{"WORK_FETCH_BATCH": "1024"}, ""},
```

Hai case `CORE_SHUTDOWN_BUDGET: 30s` sẵn có (`request deadline just under grpc shutdown`, `insert timeout just under request deadline`) vẫn hợp lệ: 26.2 + 1.999 = 28.199s < 30s.

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/config/..."`
Expected: FAIL biên dịch: `unknown field Effects in struct literal of type config.Config`.

**Step 3: Code config**

`apps/core/internal/config/effects_components.go`:

```go
package config

import "github.com/ivannguyendev/chatim/apps/core/internal/effects"

func (p *parser) workerConfig(c *Config) {
	c.Effects = effects.Config{
		Partitions: c.Work.Partitions,
		FetchBatch: p.count("WORK_FETCH_BATCH", effects.DefaultFetchBatch),
		FetchWait:  p.span("WORK_FETCH_WAIT", effects.DefaultFetchWait),
		RetryDelay: p.span("WORK_RETRY_DELAY", effects.DefaultRetryDelay),
		Drain:      p.span("WORK_DRAIN", effects.DefaultDrain),
		Poll:       c.Slot.Tick,
	}
}
```

`components.go`: thêm `p.workerConfig(c)` làm **dòng cuối** của `func (p *parser) components(c *Config)` (sau khi `c.Slot` và `c.Work` đã gán).

`config.go`:
- import `"github.com/ivannguyendev/chatim/apps/core/internal/effects"`.
- `Config`: thêm `Effects effects.Config` ngay sau `EffectRoomCache`.
- `StopPlan`: thêm `Workers time.Duration` ngay sau `Reconciler time.Duration`.
- `Load()`: `ShutdownBudget: p.span("CORE_SHUTDOWN_BUDGET", 25*time.Second),` → `ShutdownBudget: p.span("CORE_SHUTDOWN_BUDGET", 28*time.Second),`.
- `StopPlan()`: sau `Reconciler: c.Reconcile.Drain + CloseTimeout,` thêm `Workers:    c.Effects.Drain + CloseTimeout,`.
- `total()`: danh sách thành `[]time.Duration{s.DrainDelay, s.GRPC, s.Reconciler, s.Workers, s.Router, s.CIDBatch, s.Flusher, s.Publisher, s.Slots, s.Close}`.

`validate.go`:
- `stopPhases` thành:

```go
const stopPhases = "CORE_DRAIN_DELAY + CORE_GRPC_SHUTDOWN + RECONCILE_DRAIN + 1s + WORK_DRAIN + 1s + CORE_REQUEST_DEADLINE (router drain) + " +
	"2 x REDIS_OP_TIMEOUT (cid batcher drain) + FLUSH_INSERT_TIMEOUT (flusher drain) + CORE_PUBLISHER_DRAIN + slot release + client close"
```

- `componentErrors`, trong `parts` sau dòng `{"EVT_ACK_MARK_TTL, REDIS_OP_TIMEOUT, REDIS_COOLDOWN", c.AckMarks.Validate()},` thêm `{"WORK_*, SLOT_TICK", c.Effects.Validate()},` (worker luôn chạy nên không đặt trong nhánh `ReconcileEnabled`).

`effects.Config.Validate()` có sẵn từ Task 8 (partition 1..1024, `FetchBatch ≤ work.MaxAckPending` = 1024, các khoảng thời gian dương).

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/config/... ./apps/core/internal/effects/..."`
Expected: PASS.

**Step 5: Test thứ tự dừng**

`apps/core/stop_order_test.go`:

```go
package main

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/flush"
	"github.com/ivannguyendev/chatim/pkg/admin"
	"github.com/ivannguyendev/chatim/pkg/grpcserver"
)

type stopOrder struct {
	mu  sync.Mutex
	got []string
}

func (o *stopOrder) drainer(name string) recordedDrainer { return recordedDrainer{name: name, order: o} }

func (o *stopOrder) names() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.got)
}

type recordedDrainer struct {
	idle
	name  string
	order *stopOrder
}

func (d recordedDrainer) Close(context.Context) error {
	d.order.mu.Lock()
	defer d.order.mu.Unlock()
	d.order.got = append(d.order.got, d.name)
	return nil
}

type recordedRouter struct {
	recordedDrainer
	running chan struct{}
}

func (r *recordedRouter) Running() <-chan struct{} { return r.running }

func TestShutdownDrainsWorkersAfterTheReconcilerAndBeforeTheRouter(t *testing.T) {
	order := &stopOrder{}
	running := make(chan struct{})
	close(running)
	cfg := config.Config{
		DrainDelay: 10 * time.Millisecond, GRPCShutdown: time.Second, RequestDeadline: 500 * time.Millisecond,
		PublisherDrain: time.Second, ShutdownBudget: gateLimit, Flush: flush.Config{InsertTimeout: 100 * time.Millisecond},
	}
	a := &app{
		cfg: cfg, log: quiet,
		admin:     admin.New(admin.Config{ShutdownTimeout: config.CloseTimeout}, quiet),
		grpc:      grpcserver.New(grpcserver.Config{ShutdownTimeout: time.Second}, quiet),
		publisher: order.drainer("publisher"), flusher: order.drainer("flusher"), cidBatch: order.drainer("cid batcher"),
		router: &recordedRouter{recordedDrainer: order.drainer("router"), running: running},
		slots:  idle{}, workers: order.drainer("workers"), reconciler: order.drainer("reconciler"),
	}
	lis, err := listenAll(t.Context(), "127.0.0.1:0", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- a.serve(ctx, lis[0], lis[1]) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve = %v, want a clean stop", err)
		}
	case <-time.After(gateLimit):
		t.Fatalf("serve did not return within %v", gateLimit)
	}
	want := []string{"reconciler", "workers", "router", "cid batcher", "flusher", "publisher"}
	if got := order.names(); !slices.Equal(got, want) {
		t.Fatalf("drain order = %v, want %v", got, want)
	}
}
```

Huỷ ngay sau khi gọi `serve` là đủ: dù `awaitRouter` chọn nhánh `Running` hay `ctx.Done`, `serve` đều đi qua `shutdown` với đủ các bước.

`apps/core/startup_gate_test.go`, trong `startGate` thay dòng

```go
		publisher: idle{}, flusher: idle{}, cidBatch: idle{}, router: router, slots: slots,
```

bằng

```go
		publisher: idle{}, flusher: idle{}, cidBatch: idle{}, router: router, slots: slots, workers: idle{},
```

**Step 6: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/"`
Expected: FAIL biên dịch: `unknown field workers in struct literal of type app`.

**Step 7: Code vòng đời và wiring**

`wiring.go`, struct `app`: thêm `workers    drainer` ngay sau `slots      runner`.

`lifecycle.go`:
- `tasks` thành `admin, publisher, flusher, cidBatch, router, slots, workers, reconciler, grpc *task`.
- `sup := newSupervisor(ctx, 8)` → `sup := newSupervisor(ctx, 9)`.
- trong literal `t := tasks{...}`, sau `slots:     sup.start("slot manager", a.slots.Run),` thêm `workers:   sup.start("workers", a.workers.Run),` (phần tử của composite literal được đánh giá theo thứ tự viết, nên worker khởi động sau slot manager, trước reconciler).

`shutdown.go`: sau dòng `s.step("reconciler", plan.Reconciler, closeReconciler, t.reconciler)` thêm

```go
	s.step("workers", plan.Workers, a.workers.Close, t.workers)
```

`clients.go`:
- struct `clients`: thêm `effectsJS   jetstream.JetStream` ngay sau `reconcileJS`.
- `connectNATS`: ngay trước `return nil` cuối hàm thêm

```go
	ejs, err := jetstream.New(nc,
		jetstream.WithPublishAsyncMaxPending(cfg.Effects.Partitions*cfg.Effects.FetchBatch),
		jetstream.WithPublishAsyncTimeout(cfg.Publish.AckTimeout),
	)
	if err != nil {
		return fmt.Errorf("effects jetstream: %w", config.RedactError(err, cfg.NATSURL))
	}
	c.effectsJS = ejs
```

`apps/core/effects_wiring.go` (giữ `wiring.go` < 200 dòng; constructor theo Task 9/10: `NewMessageCreated(MessageCreatedDeps, MessageCreatedConfig)`, `NewRoomCreated(RoomCreatedDeps, RoomCreatedConfig)`; `SubjectRoot` là gốc subject **event** `EVT_SUBJECT_ROOT`, không phải gốc work):

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
	workers     *effects.Workers
	msgCreated  *effects.MessageCreated
	roomCreated *effects.RoomCreated
}

func wireEffects(cfg config.Config, cl *clients, st *mongostore.Store, marks *eventmark.Store, owner effects.Owner, log *slog.Logger) (effectSet, error) {
	msgCreated, err := effects.NewMessageCreated(
		effects.MessageCreatedDeps{Marks: marks, Messages: st, Rooms: st, JS: cl.effectsJS},
		effects.MessageCreatedConfig{SubjectRoot: cfg.Stream.SubjectRoot, Delay: cfg.EffectDelay, RoomCache: cfg.EffectRoomCache},
	)
	if err != nil {
		return effectSet{}, fmt.Errorf("wire msg_created effect: %w", err)
	}
	roomCreated, err := effects.NewRoomCreated(
		effects.RoomCreatedDeps{Rooms: st, JS: cl.effectsJS},
		effects.RoomCreatedConfig{SubjectRoot: cfg.Stream.SubjectRoot, Delay: cfg.EffectDelay},
	)
	if err != nil {
		return effectSet{}, fmt.Errorf("wire room_created effect: %w", err)
	}
	activity := effects.NewRoomActivity(st)
	registry := effects.Registry{
		store.MessageInserted: {activity.Effect(), msgCreated.Effect()},
		store.RoomInserted:    {roomCreated.Effect()},
	}
	workers, err := effects.New(effects.Deps{
		Queue:    func(p int) work.Queue { return work.NewQueue(cl.effectsJS, cfg.Work.Name, p) },
		Owner:    owner,
		Registry: registry,
	}, cfg.Effects, log)
	if err != nil {
		return effectSet{}, fmt.Errorf("wire effect workers: %w", err)
	}
	return effectSet{workers: workers, msgCreated: msgCreated, roomCreated: roomCreated}, nil
}
```

Thứ tự registry: `room_activity` (delay 0) **trước** `msg_created` (delay `RECONCILE_DELAY`). Worker chờ `max(CommittedAt) + Delay` trước từng effect theo thứ tự, nên đặt `msg_created` trước sẽ giữ room activity lại thêm 5s vô ích. Cả hai hội tụ khi chạy lại (`$max`; id tự nhiên), nên record bị `Nak` vì một effect lỗi thì chạy lại effect kia cũng an toàn.

`wiring.go`, trong `wire`, ngay sau khối `slots, err := slot.New(cl.slots, slotCfg, log)` … `}` thêm:

```go
	fx, err := wireEffects(cfg, cl, st, marks, slots, log)
	if err != nil {
		return nil, err
	}
	a.workers = fx.workers
```

`it_infra_test.go` không đổi: Task 6 đã cho mỗi itest `WORK_STREAM`/`WORK_SUBJECT_ROOT` riêng và `forget` xoá stream đó (kiểm: `grep -n WORK_STREAM apps/core/it_infra_test.go` có một dòng). Thiếu thì dừng và báo: consumer durable `work-p{n}` cùng tên sẽ để các test và cụm compose ăn record của nhau.

`deploy/compose/docker-compose.yml`, trong `x-core`: `stop_grace_period: 30s` → `stop_grace_period: 33s`.

**Step 8: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/ ./apps/core/internal/config/..."`
Expected: PASS (itest skip). `wc -l apps/core/wiring.go apps/core/clients.go apps/core/effects_wiring.go apps/core/stop_order_test.go` → mỗi file < 200.

**Step 9: README**

Bảng "Biến môi trường `apps/core`":
- Dòng `CORE_SHUTDOWN_BUDGET` thay bằng:

```markdown
| `CORE_SHUTDOWN_BUDGET` | `28s` | Tổng ngân sách toàn bộ chuỗi dừng (D39); `Load()` từ chối nếu các mốc dừng cộng lại không nhỏ hơn. Mặc định các mốc là 26.2s (gồm `RECONCILE_DRAIN + 1s` của reader, `WORK_DRAIN + 1s` của worker và `2 × REDIS_OP_TIMEOUT` để cid batcher gửi hết Commit), nên tăng mốc nào cũng phải tăng ngân sách này và `stop_grace_period` của compose (33s) |
```

- Sau các dòng `WORK_*` của Task 6 (hoặc sau dòng `RECONCILE_ROOM_CACHE` nếu Task 6 chưa thêm) thêm:

```markdown
| `WORK_FETCH_BATCH`, `WORK_FETCH_WAIT` | `256` / `1s` | Số record tối đa một lần fetch của mỗi partition (≤ 1024, `MaxAckPending` của consumer) và thời gian chờ fetch; JetStream client của worker cho tối đa `WORK_PARTITIONS × WORK_FETCH_BATCH` publish đang bay, timeout `PUB_ACK_TIMEOUT` |
| `WORK_RETRY_DELAY` | `5s` | Record có effect lỗi được `Nak` với delay này rồi giao lại (đếm vào `work_failures_total`) |
| `WORK_DRAIN` | `1s` | Lúc dừng: chờ lô đang chạy xong; bước dừng worker chiếm `WORK_DRAIN + 1s` |
```

**Step 10: Itest và cụm compose**

```bash
make infra-up && make itest
make image TARGET=apps/core && make core-up && make e2e
docker logs chatim-core-1 2>&1 | grep -c '"msg":"reconcile term started"'; docker logs chatim-core-2 2>&1 | grep -c '"msg":"reconcile term started"'
```

Expected: itest mọi package `ok` (`TestRealInfraReaderForwardsWritesThatSkippedTheCore` của Task 7 vẫn qua: record nằm trong work stream ít nhất `RECONCILE_DELAY` = 2s trước khi worker ack và WorkQueue xoá, poll 250ms bắt được; Task 15 thay test này bằng khẳng định live event); `e2e PASS: 40 messages before and 40 after killing core-1, no loss, no duplicate, every acked seq live` (event tới từ fast path, tin fast path bỏ thì worker gửi bù); dòng `grep -c` cho ≥ 1 trên ít nhất một core (reader vẫn chạy trên chủ slot 0).

**Step 11: INDEXES.csv**

- `apps/core`: purpose thêm "; effect workers on every core (effects_wiring.go: registry MessageInserted -> room_activity, msg_created; RoomInserted -> room_created) on a third JetStream client; start after the slot manager, before the reader; stop after the reader, before the router (WORK_DRAIN + 1s)"; key_symbols thêm `wireEffects;effectSet`; decisions thêm `D65;D66;D69;D79`.
- `apps/core/internal/config`: purpose thêm "; Effects (WORK_FETCH_BATCH/WORK_FETCH_WAIT/WORK_RETRY_DELAY/WORK_DRAIN, partitions from WORK_PARTITIONS, poll = SLOT_TICK); StopPlan.Workers; default budget 28s"; key_symbols thêm `StopPlan.Workers`.
- `deploy/compose/docker-compose.yml` (nếu có dòng): ghi `stop_grace_period 33s`.
- `apps/core/internal/effects`: used_by `apps/core (Task 12)` → `apps/core`.

Kiểm 7 cột như Task 11.

**Step 12: Commit**

```bash
make fmt-check && make vet && make lint
git add apps/core/ deploy/compose/docker-compose.yml README.md INDEXES.csv
git commit -m "feat(core): run effect workers on every core within a 28s stop budget"
```

---

### Task 13: Metrics của worker/effect và luật alert

Sau Task 7, `reconcileSources` chỉ còn phần reader (running, terms, forwarded, dropped, history lost) và `reconcile_lag_seconds`, `reconcile_republished_total` không còn ai xuất (luật `ChatimReconcilerLagging`, `ChatimRepublishSurge` treo). Task này đưa hai metric đó về, lấy từ worker/effect, xuất trên **mọi** core (worker chạy mọi nơi; `max`/`sum` trong luật gộp đúng), và thêm metric riêng của worker.

Quyết định đặt tên (giữ tên cũ để luật và dashboard không đổi ngữ nghĩa):

| Metric | Nguồn | Xuất khi |
|---|---|---|
| `reconcile_lag_seconds` (gauge) | `Workers.Stats().Lag`: worker chạy trễ bao lâu so với delay của effect | mọi core |
| `reconcile_republished_total{effect="msg_created"\|"room_created"}` | `Republished()` của effect: event worker gửi vì không có mark (fast path có thể đã gửi) | mọi core |
| `effect_dropped_total{effect=...}` | `Dropped()` của effect: record effect bỏ (room mất, doc hỏng) | mọi core |
| `work_processed_total`, `work_failures_total` | `Workers.Stats().Processed/Failed` | mọi core |
| `reconcile_running`, `reconcile_terms_total`, `reconcile_forwarded_total`, `reconcile_dropped_total`, `reconcile_history_lost_total` | `reconcile.Stats` của reader; `reconcile_dropped_total` = change hỏng reader không dựng được record | chỉ khi `RECONCILE_ENABLED` |

`effect_dropped_total` tách khỏi `reconcile_dropped_total` vì nguồn và cách xử lý khác nhau: reader drop là change hỏng trong nhật ký (không có record), effect drop là record hợp lệ mà doc không đọc được lúc chạy.

**Files:**
- Modify (viết lại cả file): `apps/core/metrics_wiring.go`, `apps/core/metrics_wiring_test.go`
- Modify: `apps/core/wiring.go` (khối `probes`), `apps/core/effects_wiring.go` (thêm `counters`)
- Modify: `deploy/prometheus/alerts.yml`
- Modify: `INDEXES.csv`

**Step 1: Test**

`apps/core/metrics_wiring_test.go` (viết lại cả file):

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
			"msg_created":  {republished: func() uint64 { return 5 }, dropped: zero},
			"room_created": {republished: zero, dropped: func() uint64 { return 1 }},
		},
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
		if effect := s.Labels["effect"]; effect != "" {
			key += "{" + effect + "}"
		}
		got[key] = s.Read()
	}
	want := map[string]float64{
		"reconcile_lag_seconds": 3, "work_processed_total": 7, "work_failures_total": 2,
		"reconcile_republished_total{msg_created}": 5, "reconcile_republished_total{room_created}": 0,
		"effect_dropped_total{msg_created}": 0, "effect_dropped_total{room_created}": 1,
	}
	for key, v := range want {
		if g, ok := got[key]; !ok || g != v {
			t.Errorf("%s = %v (present %v), want %v", key, g, ok, v)
		}
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on -run 'TestCoreMetricSources|TestEffectMetrics' ./apps/core/"`
Expected: FAIL biên dịch: `unknown field workers in struct literal of type probes`, `undefined: effectCounters`.

**Step 3: Code**

`apps/core/metrics_wiring.go` (viết lại cả file; 13 nguồn đầu giữ nguyên, chỉ `dropHelp`/`markHelp` đổi "the reconciler" thành "the effect workers"):

```go
package main

import (
	"context"
	"maps"
	"math"
	"slices"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ivannguyendev/chatim/apps/core/internal/actor"
	"github.com/ivannguyendev/chatim/apps/core/internal/effects"
	"github.com/ivannguyendev/chatim/apps/core/internal/metrics"
	"github.com/ivannguyendev/chatim/apps/core/internal/publish"
	"github.com/ivannguyendev/chatim/apps/core/internal/reconcile"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

const oplogProbeTimeout = 2 * time.Second

type effectCounters struct {
	republished func() uint64
	dropped     func() uint64
}

type probes struct {
	drops        func() publish.Drops
	router       func() actor.Stats
	cidDegraded  func() bool
	markDegraded func() bool
	cidDropped   func() uint64
	loadShed     func() int64
	oplogWindow  func() float64
	workers      func() effects.Stats
	effectCounts map[string]effectCounters
	reconcile    func() reconcile.Stats
}

func metricSources(p probes) []metrics.Source {
	const dropHelp = "Events dropped before JetStream acked them; the effect workers send them again."
	const markHelp = "Ack marks not written; the effect workers send those events again."
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
	out = append(out, workerSources(p.workers, p.effectCounts)...)
	if p.reconcile != nil {
		out = append(out, readerSources(p.reconcile)...)
	}
	return out
}

func workerSources(stats func() effects.Stats, counts map[string]effectCounters) []metrics.Source {
	const republishHelp = "Events effect workers sent because no ack mark showed the fast path delivered them."
	const dropHelp = "Work records an effect gave up on (missing room or corrupt document)."
	out := []metrics.Source{
		{Name: "reconcile_lag_seconds", Help: "How far the effect workers run behind each effect's delay.", Gauge: true, Read: func() float64 { return stats().Lag.Seconds() }},
		{Name: "work_processed_total", Help: "Work records acked after every effect ran.", Read: func() float64 { return float64(stats().Processed) }},
		{Name: "work_failures_total", Help: "Work records sent back for retry after an effect failed.", Read: func() float64 { return float64(stats().Failed) }},
	}
	for _, name := range slices.Sorted(maps.Keys(counts)) {
		c, labels := counts[name], map[string]string{"effect": name}
		out = append(out,
			metrics.Source{Name: "reconcile_republished_total", Help: republishHelp, Labels: labels, Read: func() float64 { return float64(c.republished()) }},
			metrics.Source{Name: "effect_dropped_total", Help: dropHelp, Labels: labels, Read: func() float64 { return float64(c.dropped()) }},
		)
	}
	return out
}

func readerSources(stats func() reconcile.Stats) []metrics.Source {
	return []metrics.Source{
		{Name: "reconcile_running", Help: "1 while this core runs the change reader.", Gauge: true, Read: func() float64 { return flag(stats().Running) }},
		{Name: "reconcile_terms_total", Help: "Reader terms started on this core.", Read: func() float64 { return float64(stats().Terms) }},
		{Name: "reconcile_forwarded_total", Help: "Committed changes the reader sent to the work stream.", Read: func() float64 { return float64(stats().Forwarded) }},
		{Name: "reconcile_dropped_total", Help: "Corrupt changes the reader could not turn into work records.", Read: func() float64 { return float64(stats().Dropped) }},
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

Nếu Task 9/10 trả bộ đếm kiểu khác `uint64` (ví dụ `int64`), đổi kiểu hàm trong `effectCounters` cho khớp, không đổi effect.

`apps/core/effects_wiring.go`, thêm cuối file (tên lấy từ `Effect().Name` để không lặp chuỗi):

```go
func (fx effectSet) counters() map[string]effectCounters {
	return map[string]effectCounters{
		fx.msgCreated.Effect().Name:  {republished: fx.msgCreated.Republished, dropped: fx.msgCreated.Dropped},
		fx.roomCreated.Effect().Name: {republished: fx.roomCreated.Republished, dropped: fx.roomCreated.Dropped},
	}
}
```

`apps/core/wiring.go`, trong literal `p := probes{...}` thêm sau `oplogWindow:  oplogWindowSeconds(cl.mongo),`:

```go
		workers:      fx.workers.Stats,
		effectCounts: fx.counters(),
```

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/"`
Expected: PASS. `wc -l apps/core/metrics_wiring.go apps/core/wiring.go` → < 200.

**Step 5: Luật alert**

`deploy/prometheus/alerts.yml` — biểu thức giữ nguyên, chỉ sửa `summary` cho đúng nguồn mới:
- `ChatimReconcilerAbsent`: `summary: No core runs the change reader; committed changes do not reach the work stream.`
- `ChatimReconcilerLagging`: `summary: Effect workers run more than two minutes behind their effect delay.`
- `ChatimReconcilerDropping`: `summary: The change reader found corrupt changes it could not turn into work records.`
- `ChatimRepublishSurge`: `summary: Effect workers send more than 100 events per second that had no ack mark.`
- `ChatimEventsDropped`: `summary: The fast path keeps dropping events; delivery waits for the effect workers.`

Thêm cuối file (sau `ChatimSeqContention`, cùng thụt lề):

```yaml
      - alert: ChatimWorkFailing
        expr: sum(rate(chatim_core_work_failures_total[5m])) > 0
        for: 10m
        labels:
          severity: warning
          guarantee: RC1
        annotations:
          summary: Effect workers keep failing work records; events and room activity wait for retries.
      - alert: ChatimEffectDropping
        expr: sum by (effect) (increase(chatim_core_effect_dropped_total[15m])) > 0
        labels:
          severity: warning
          guarantee: RC1
        annotations:
          summary: An effect gave up on work records whose document could not be read.
```

Run: `make alerts-check`
Expected: `Checking /rules/alerts.yml` và `SUCCESS: 15 rules found` (13 cũ + 2).

**Step 6: Kiểm trên compose**

```bash
make image TARGET=apps/core && make core-up
for c in chatim-core-1 chatim-core-2; do echo "== $c"; docker run --rm --network chatim_default curlimages/curl:8.11.1 -s http://$c:9090/metrics | grep -E '^chatim_core_(reconcile_running|reconcile_lag_seconds|reconcile_republished_total|effect_dropped_total|work_processed_total|work_failures_total|reconcile_forwarded_total)'; done
```

Expected: cả hai core có `chatim_core_reconcile_lag_seconds`, `chatim_core_work_processed_total`, `chatim_core_work_failures_total 0`, `chatim_core_reconcile_republished_total{effect="msg_created"}`, `{effect="room_created"}`, `chatim_core_effect_dropped_total{effect="..."} 0`; đúng một core có `chatim_core_reconcile_running 1` (và có `chatim_core_reconcile_forwarded_total`).

**Step 7: INDEXES.csv**

- `apps/core`: purpose sửa đoạn metrics thành "metrics_wiring.go registers detectors on the admin /metrics (worker lag/processed/failures and per-effect republished/dropped on every core; reader metrics only with RECONCILE_ENABLED)"; key_symbols thêm `effectCounters;workerSources;readerSources`.
- `deploy/prometheus/alerts.yml`: purpose "15 alert rules … (reader absent/dropping, worker lagging, …, work failing, effect dropping)".

Kiểm 7 cột như Task 11.

**Step 8: Commit**

```bash
make fmt-check && make vet && make lint
git add apps/core/metrics_wiring.go apps/core/metrics_wiring_test.go apps/core/wiring.go apps/core/effects_wiring.go deploy/prometheus/alerts.yml INDEXES.csv
git commit -m "feat(core): export effect worker detectors and alert on failing work"
```

---

### Task 14: `/app resync` và itest diễn tập

Công cụ phục hồi sau `ChatimFeedHistoryLost` (RC5, D69, D81): subcommand của chính binary core (cùng image, cùng secret, cùng config), quét `rooms` qua `store.ActiveRooms` (hoặc một room), dựng **record** và đẩy vào work stream có rate limit; worker chạy effect như đường bình thường (delay, ack mark, id tự nhiên). Tool không publish event trực tiếp, không đụng Redis.

Thuật toán:
1. Room: `-room` → `Rooms.Get` (kiểm tenant nếu có `-tenant`); không thì trang `ActiveRooms{From, To, Tenant, After, Limit: 500}` tới khi trang ngắn hơn 500.
2. Room có `CreatedAt ∈ [from, to]` → record `RoomInserted{CommittedAt: CreatedAt}`.
3. Timeline chính: `Page(Latest, 100)` rồi `Page(Before seq đầu trang, 100)`, duyệt từ tin mới nhất về cũ; tin `CreatedAt > to` bỏ qua, gặp tin `CreatedAt < from` thì dừng room; còn lại → record `MessageInserted{Thread: 0, CommittedAt: CreatedAt}`.
4. Mỗi record: chờ một nhịp ticker `1s / -rate`, `js.PublishMsg(work.Message(root, partitions, rec))` đồng bộ (chờ PubAck). Lỗi publish → dừng, in số đã đẩy, exit 1; chạy lại an toàn (id record; worker bỏ trùng theo mark/id).
5. In một dòng `resync rooms=N room_records=N message_records=N dry_run=B`.

Giới hạn (ghi vào README và thiết kế ở Task 16): chỉ timeline chính tới khi có thread; thứ tự event trong một room theo resync là ngược (consumer không dựa thứ tự ở đường sửa lỗi); event cũ hơn cửa sổ chống trùng `EVT_STREAM_DUPLICATES` (5m) và mark đã hết TTL (1h) thành bản trùng thật trên stream — consumer bỏ trùng theo id; room scan ngược từ tin mới nhất nên room rất bận sau `to` tốn đọc tương ứng; lệch đồng hồ giữa core làm `CreatedAt` không đơn điệu theo seq có thể bỏ sót tin sát mép `from` — chọn `-from` rộng hơn khoảng mất vài phút.

**Files:**
- Create: `apps/core/internal/resync/options.go`, `options_test.go`
- Create: `apps/core/internal/resync/scan.go`, `scan_test.go`
- Create: `apps/core/resync_command.go`
- Create: `apps/core/it_core_test.go` (helper itest dùng chung; chuyển `termStartSignal` từ `reconcile_integration_test.go` sang)
- Create: `apps/core/resync_integration_test.go`
- Modify: `apps/core/main.go`, `apps/core/main_test.go`, `apps/core/reconcile_integration_test.go` (chỉ cắt `termStartSignal`)
- Modify: `INDEXES.csv`

**Step 1: Test package `resync`**

`apps/core/internal/resync/scan_test.go`:

```go
package resync_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/memstore"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
	"github.com/ivannguyendev/chatim/pkg/apperr"
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
	rooms *memstore.Rooms
	msgs  *memstore.Messages
}

func newWorld(t *testing.T) world {
	t.Helper()
	w := world{rooms: memstore.NewRooms(), msgs: memstore.NewMessages()}
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
	return resync.Deps{Rooms: w.rooms, Pages: w.msgs, Pub: pub}
}

func TestResyncPublishesRecordsOfTheLostRangeOnly(t *testing.T) {
	w := newWorld(t)
	pub := &publishSpy{}
	rep, err := resync.Run(t.Context(), w.deps(pub), target, resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Rate: resync.MaxRate})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var want []string
	for seq := uint64(91); seq >= 31; seq-- {
		want = append(want, work.Record{Kind: store.MessageInserted, Room: busyRoom, Seq: seq}.ID())
	}
	want = append(want, work.Record{Kind: store.RoomInserted, Room: newRoom}.ID())
	if got := pub.published(); !slices.Equal(got, want) {
		t.Fatalf("published %d ids %v,\nwant %d ids %v", len(got), got, len(want), want)
	}
	if rep != (resync.Report{Rooms: 2, RoomRecords: 1, MessageRecords: 61}) {
		t.Fatalf("report = %+v, want 2 rooms, 1 room record, 61 message records", rep)
	}
}

func TestResyncOfOneRoomSkipsTheActivityIndex(t *testing.T) {
	w := newWorld(t)
	pub := &publishSpy{}
	opts := resync.Options{From: lostFrom.Add(-4 * time.Hour), To: lostFrom.Add(-2 * time.Hour), Room: staleRoom, Rate: resync.MaxRate}
	rep, err := resync.Run(t.Context(), w.deps(pub), target, opts)
	if err != nil || rep != (resync.Report{Rooms: 1, MessageRecords: 5}) || len(pub.published()) != 5 {
		t.Fatalf("Run = %+v, %v with %d published; want 1 room and 5 message records", rep, err, len(pub.published()))
	}
	opts.Tenant = "other"
	if _, err := resync.Run(t.Context(), w.deps(pub), target, opts); !errors.Is(err, apperr.ErrInvalidArgument) {
		t.Fatalf("Run(room of another tenant) = %v, want ErrInvalidArgument", err)
	}
}

func TestResyncDryRunCountsWithoutPublishingOrPacing(t *testing.T) {
	w := newWorld(t)
	pub := &publishSpy{}
	rep, err := resync.Run(t.Context(), w.deps(pub), target, resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Rate: 1, DryRun: true})
	if err != nil || rep != (resync.Report{Rooms: 2, RoomRecords: 1, MessageRecords: 61, DryRun: true}) || len(pub.published()) != 0 {
		t.Fatalf("dry run = %+v, %v with %d published; want counts only", rep, err, len(pub.published()))
	}
}

func TestResyncIsPacedByRate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t)
		pub := &publishSpy{}
		start := time.Now()
		opts := resync.Options{From: lostFrom.Add(-4 * time.Hour), To: lostFrom.Add(-2 * time.Hour), Room: staleRoom, Rate: 10}
		rep, err := resync.Run(t.Context(), w.deps(pub), target, opts)
		if err != nil || rep.MessageRecords != 5 {
			t.Fatalf("Run = %+v, %v; want 5 message records", rep, err)
		}
		if took := time.Since(start); took != 500*time.Millisecond {
			t.Fatalf("5 records at 10/s took %v, want 500ms", took)
		}
	})
}

func TestResyncStopsAtTheFirstPublishError(t *testing.T) {
	w := newWorld(t)
	pub := &publishSpy{err: errPublish}
	rep, err := resync.Run(t.Context(), w.deps(pub), target, resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Rate: resync.MaxRate})
	if !errors.Is(err, errPublish) || rep.MessageRecords != 0 || rep.RoomRecords != 0 {
		t.Fatalf("Run = %+v, %v; want errPublish and nothing counted", rep, err)
	}
}
```

Dữ liệu: `busyRoom` cũ, tin seq s tạo lúc `lostFrom + (s−31) phút` (seq 31..91 trong khoảng, 150 tin vượt một trang 100), hoạt động lần cuối sau `lostTo` → vẫn được quét; `newRoom` tạo trong khoảng, không có tin; `staleRoom` chỉ hoạt động trước khoảng → không được quét theo chỉ mục; `foreignRoom` khác tenant.

`apps/core/internal/resync/options_test.go`:

```go
package resync_test

import (
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/ivannguyendev/chatim/apps/core/internal/resync"
	"github.com/ivannguyendev/chatim/pkg/apperr"
)

var rangeArgs = []string{"-from", "2026-10-01T10:00:00Z", "-to", "2026-10-01T18:00:00+07:00"}

func TestParseArgsReadsEveryFlag(t *testing.T) {
	got, err := resync.ParseArgs(slices.Concat(rangeArgs, []string{"-tenant", "acme", "-room", "7340000001", "-rate", "50", "-dry-run"}), io.Discard)
	want := resync.Options{From: lostFrom, To: lostTo, Tenant: "acme", Room: 7_340_000_001, Rate: 50, DryRun: true}
	if err != nil || !got.From.Equal(want.From) || !got.To.Equal(want.To) {
		t.Fatalf("ParseArgs = %+v, %v; want %+v", got, err, want)
	}
	got.From, got.To = want.From, want.To
	if got != want {
		t.Fatalf("ParseArgs = %+v, want %+v", got, want)
	}
	if def, err := resync.ParseArgs(rangeArgs, io.Discard); err != nil || def.Rate != resync.DefaultRate || def.Room != 0 || def.DryRun {
		t.Fatalf("ParseArgs(range only) = %+v, %v; want rate %d, every room, publishing", def, err, resync.DefaultRate)
	}
}

func TestParseArgsRejectsBadInput(t *testing.T) {
	tests := map[string][]string{
		"no flags":         nil,
		"no end":           {"-from", "2026-10-01T10:00:00Z"},
		"end before start": {"-from", "2026-10-01T11:00:00Z", "-to", "2026-10-01T10:00:00Z"},
		"zero rate":        slices.Concat(rangeArgs, []string{"-rate", "0"}),
		"rate above max":   slices.Concat(rangeArgs, []string{"-rate", "10001"}),
		"bad room":         slices.Concat(rangeArgs, []string{"-room", "abc"}),
		"extra argument":   slices.Concat(rangeArgs, []string{"extra"}),
		"unknown flag":     slices.Concat(rangeArgs, []string{"-bogus"}),
	}
	for name, args := range tests {
		if _, err := resync.ParseArgs(args, io.Discard); !errors.Is(err, apperr.ErrInvalidArgument) {
			t.Errorf("%s: ParseArgs(%v) = %v, want ErrInvalidArgument", name, args, err)
		}
	}
}
```

**Step 2: Chạy, thấy fail**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/internal/resync/..."`
Expected: FAIL biên dịch: `no non-test Go files in .../apps/core/internal/resync` (hoặc `undefined: resync.Run`).

**Step 3: Code package `resync`**

`apps/core/internal/resync/options.go`:

```go
package resync

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/ivannguyendev/chatim/pkg/apperr"
	"github.com/ivannguyendev/chatim/pkg/ids"
)

const (
	DefaultRate = 500
	MaxRate     = 10000
)

var ErrUsage = fmt.Errorf("%w: resync usage", apperr.ErrInvalidArgument)

type Options struct {
	From, To time.Time
	Tenant   string
	Room     uint64
	Rate     int
	DryRun   bool
}

func ParseArgs(args []string, stderr io.Writer) (Options, error) {
	fs := flag.NewFlagSet("resync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o Options
	var from, to, room string
	fs.StringVar(&from, "from", "", "start of the lost range, RFC3339")
	fs.StringVar(&to, "to", "", "end of the lost range, RFC3339")
	fs.StringVar(&o.Tenant, "tenant", "", "only rooms of this tenant")
	fs.StringVar(&room, "room", "", "only this room id")
	fs.IntVar(&o.Rate, "rate", DefaultRate, "work records published per second")
	fs.BoolVar(&o.DryRun, "dry-run", false, "count the records without publishing them")
	if err := fs.Parse(args); err != nil {
		return Options{}, fmt.Errorf("%w: %w", ErrUsage, err)
	}
	if fs.NArg() > 0 {
		return Options{}, fmt.Errorf("%w: unexpected arguments %v", ErrUsage, fs.Args())
	}
	var err error
	if o.From, err = time.Parse(time.RFC3339, from); err != nil {
		return Options{}, fmt.Errorf("%w: -from: %w", ErrUsage, err)
	}
	if o.To, err = time.Parse(time.RFC3339, to); err != nil {
		return Options{}, fmt.Errorf("%w: -to: %w", ErrUsage, err)
	}
	if room != "" {
		if o.Room, err = ids.ParseRoomID(room); err != nil {
			return Options{}, fmt.Errorf("%w: -room: %w", ErrUsage, err)
		}
	}
	return o, o.Validate()
}

func (o Options) Validate() error {
	switch {
	case !o.From.Before(o.To):
		return fmt.Errorf("%w: -from must be before -to", ErrUsage)
	case o.Rate < 1 || o.Rate > MaxRate:
		return fmt.Errorf("%w: -rate must be in 1..%d", ErrUsage, MaxRate)
	default:
		return nil
	}
}
```

`apps/core/internal/resync/scan.go`:

```go
package resync

import (
	"context"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/work"
)

const (
	roomPage    = 500
	messagePage = store.MaxPageLimit
)

type Rooms interface {
	Get(ctx context.Context, id uint64) (domain.Room, error)
	ActiveRooms(ctx context.Context, q store.ActiveQuery) ([]domain.Room, error)
}

type Pages interface {
	Page(ctx context.Context, q store.PageQuery) ([]domain.Message, error)
}

type Publisher interface {
	PublishMsg(ctx context.Context, m *nats.Msg, opts ...jetstream.PublishOpt) (*jetstream.PubAck, error)
}

type Deps struct {
	Rooms Rooms
	Pages Pages
	Pub   Publisher
}

type Target struct {
	SubjectRoot string
	Partitions  int
}

type Report struct {
	Rooms, RoomRecords, MessageRecords int
	DryRun                             bool
}

func (r Report) String() string {
	return fmt.Sprintf("resync rooms=%d room_records=%d message_records=%d dry_run=%t", r.Rooms, r.RoomRecords, r.MessageRecords, r.DryRun)
}

type scanner struct {
	deps   Deps
	target Target
	opts   Options
	tick   *time.Ticker
	rep    Report
}

func Run(ctx context.Context, deps Deps, target Target, opts Options) (Report, error) {
	if err := opts.Validate(); err != nil {
		return Report{DryRun: opts.DryRun}, err
	}
	s := &scanner{deps: deps, target: target, opts: opts, tick: time.NewTicker(time.Second / time.Duration(opts.Rate)), rep: Report{DryRun: opts.DryRun}}
	defer s.tick.Stop()
	err := s.rooms(ctx)
	return s.rep, err
}

func (s *scanner) rooms(ctx context.Context) error {
	if s.opts.Room != 0 {
		r, err := s.deps.Rooms.Get(ctx, s.opts.Room)
		if err != nil {
			return fmt.Errorf("room %d: %w", s.opts.Room, err)
		}
		if s.opts.Tenant != "" && r.Tenant != s.opts.Tenant {
			return fmt.Errorf("%w: room %d belongs to another tenant", ErrUsage, r.ID)
		}
		return s.room(ctx, r)
	}
	q := store.ActiveQuery{From: s.opts.From, To: s.opts.To, Tenant: s.opts.Tenant, Limit: roomPage}
	for {
		page, err := s.deps.Rooms.ActiveRooms(ctx, q)
		if err != nil {
			return fmt.Errorf("active rooms after %d: %w", q.After, err)
		}
		for _, r := range page {
			if err := s.room(ctx, r); err != nil {
				return err
			}
		}
		if len(page) < roomPage {
			return nil
		}
		q.After = page[len(page)-1].ID
	}
}

func (s *scanner) room(ctx context.Context, r domain.Room) error {
	s.rep.Rooms++
	if s.inRange(r.CreatedAt) {
		if err := s.emit(ctx, work.Record{Kind: store.RoomInserted, Room: r.ID, CommittedAt: r.CreatedAt}); err != nil {
			return err
		}
		s.rep.RoomRecords++
	}
	return s.timeline(ctx, r.ID)
}

func (s *scanner) timeline(ctx context.Context, room uint64) error {
	q := store.PageQuery{Room: room, Anchor: store.Latest, Limit: messagePage}
	for {
		page, err := s.deps.Pages.Page(ctx, q)
		if err != nil {
			return fmt.Errorf("page room %d before seq %d: %w", room, q.Seq, err)
		}
		for i := len(page) - 1; i >= 0; i-- {
			m := page[i]
			if m.CreatedAt.Before(s.opts.From) {
				return nil
			}
			if m.CreatedAt.After(s.opts.To) {
				continue
			}
			if err := s.emit(ctx, work.Record{Kind: store.MessageInserted, Room: m.Room, Thread: m.Thread, Seq: m.Seq, CommittedAt: m.CreatedAt}); err != nil {
				return err
			}
			s.rep.MessageRecords++
		}
		if len(page) < messagePage {
			return nil
		}
		q.Anchor, q.Seq = store.Before, page[0].Seq
	}
}

func (s *scanner) inRange(t time.Time) bool { return !t.Before(s.opts.From) && !t.After(s.opts.To) }

func (s *scanner) emit(ctx context.Context, rec work.Record) error {
	if s.opts.DryRun {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.tick.C:
	}
	if _, err := s.deps.Pub.PublishMsg(ctx, work.Message(s.target.SubjectRoot, s.target.Partitions, rec)); err != nil {
		return fmt.Errorf("publish work record %s: %w", rec.ID(), err)
	}
	return nil
}
```

`CommittedAt` của record resync = `CreatedAt` của doc (thời điểm core nhận tin), không phải cluster time của commit: lệch vài ms, chỉ ảnh hưởng mốc chờ delay của worker và `LastMsgAt` (`$max` nên không lùi giá trị thật mới hơn).

**Step 4: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on -count=5 ./apps/core/internal/resync/..."`
Expected: PASS (`-count=5` vì có ticker). `wc -l apps/core/internal/resync/*.go` → mỗi file < 200.

**Step 5: Test subcommand**

`apps/core/main_test.go`, trong `TestRealMainRejectsUnknownCommands` đổi danh sách thành:

```go
	for _, args := range [][]string{{"bogus"}, {"serve", "extra"}, {"probe", "extra"}, {"resync"}, {"resync", "-from", "2026-10-01T10:00:00Z"}} {
```

(`resync` thiếu cờ trả 2 trước khi đọc config hay kết nối gì.)

`apps/core/it_core_test.go` — helper itest dùng chung cho Task 14 và 15. **Cắt** nguyên khối `type termStartSignal struct` và bốn method của nó (`Enabled`, `Handle`, `WithAttrs`, `WithGroup`) khỏi `reconcile_integration_test.go`, dán không đổi vào cuối file này; bỏ các import không còn dùng trong `reconcile_integration_test.go` (`log/slog`, `os`, `sync` nếu không còn chỗ dùng; test trong file đó giữ nguyên tới Task 15):

```go
package main

import (
	"context"
	"log/slog"
	"maps"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/pkg/ids"
)

const itLiveLimit = 30 * time.Second

var itFastEffects = map[string]string{"CORE_DRAIN_DELAY": "200ms", "RECONCILE_DELAY": "2s", "PUB_ACK_TIMEOUT": "500ms"}

type itCore struct {
	cfg     config.Config
	started <-chan struct{}
}

func startCore(t *testing.T, it *itInfra, env map[string]string) itCore {
	t.Helper()
	cfg := it.coreConfig(t, env)
	started := make(chan struct{})
	logger := slog.New(&termStartSignal{
		Handler: slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}),
		once:    &sync.Once{},
		started: started,
	})
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
	return itCore{cfg: cfg, started: started}
}

func (c itCore) awaitTerm(t *testing.T) {
	t.Helper()
	select {
	case <-c.started:
	case <-time.After(itLiveLimit):
		t.Fatalf("no reconcile term started within %v", itLiveLimit)
	}
}

func parseRoom(t *testing.T, roomID string) uint64 {
	t.Helper()
	room, err := ids.ParseRoomID(roomID)
	if err != nil {
		t.Fatalf("ParseRoomID(%s): %v", roomID, err)
	}
	return room
}

func subscribeLive(t *testing.T, it *itInfra, cfg config.Config, roomID string) <-chan *nats.Msg {
	t.Helper()
	live := make(chan *nats.Msg, 256)
	sub, err := it.nc.ChanSubscribe(cfg.Stream.LiveRoot+"."+itTenant+".room."+roomID+".>", live)
	if err != nil {
		t.Fatalf("subscribe live: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	return live
}

func awaitLiveIDs(t *testing.T, live <-chan *nats.Msg, since time.Time, want ...string) {
	t.Helper()
	missing := make(map[string]bool, len(want))
	for _, id := range want {
		missing[id] = true
	}
	deadline := time.After(itLiveLimit)
	for len(missing) > 0 {
		select {
		case m := <-live:
			delete(missing, m.Header.Get(jetstream.MsgIDHeader))
		case <-deadline:
			t.Fatalf("live events %v did not arrive within %v", slices.Sorted(maps.Keys(missing)), itLiveLimit)
		}
	}
	t.Logf("live events %v arrived %v after the write", want, time.Since(since))
}

func assertNoLiveIDs(t *testing.T, live <-chan *nats.Msg, wait time.Duration, unwantedIDs ...string) {
	t.Helper()
	unwanted := make(map[string]bool, len(unwantedIDs))
	for _, id := range unwantedIDs {
		unwanted[id] = true
	}
	timeout := time.After(wait)
	for {
		select {
		case m := <-live:
			if id := m.Header.Get(jetstream.MsgIDHeader); unwanted[id] {
				t.Fatalf("live event %s arrived, want none within %v", id, wait)
			}
		case <-timeout:
			return
		}
	}
}
```

(`roomID` = `"*"` để nghe mọi room của tenant.) Sau khi dán `termStartSignal`, file khoảng 165 dòng.

`apps/core/resync_integration_test.go`:

```go
package main

import (
	"bytes"
	"maps"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

func TestRealInfraResyncDrillRepublishesWritesTheReaderMissed(t *testing.T) {
	it := realInfra(t)
	env := maps.Clone(itFastEffects)
	env["RECONCILE_ENABLED"] = "false"
	core := startCore(t, it, env)
	began := time.Now().UTC()
	roomID := createRoom(t, dialCore(t, core.cfg))
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)

	st := mongostore.New(it.mongo.Database(core.cfg.MongoDB), mongostore.Options{})
	missed := make([]domain.Message, 3)
	want := make([]string, len(missed))
	for i := range missed {
		seq := uint64(i + 1)
		missed[i] = domain.Message{
			Room: room, Seq: seq, Tenant: itTenant, From: "migrator", Kind: domain.KindText, Text: "missed by the reader",
			CID: "missed-" + strconv.FormatUint(seq, 10), CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
		}
		want[i] = pbconv.MessageEventID(room, 0, seq)
	}
	for i, res := range st.Insert(t.Context(), missed) {
		if res.Outcome != store.Inserted {
			t.Fatalf("insert missed[%d]: %+v", i, res)
		}
	}
	assertNoLiveIDs(t, live, 3*time.Second, want...)

	opts := resync.Options{From: began.Add(-time.Minute), To: time.Now().UTC().Add(time.Minute), Tenant: itTenant, Rate: 100}
	var out bytes.Buffer
	ran := time.Now()
	if err := runResync(t.Context(), core.cfg, opts, quiet, &out); err != nil {
		t.Fatalf("runResync: %v (output %q)", err, out.String())
	}
	if got := strings.TrimSpace(out.String()); got != "resync rooms=1 room_records=1 message_records=3 dry_run=false" {
		t.Fatalf("resync output = %q, want one room, its room record and three message records", got)
	}
	awaitLiveIDs(t, live, ran, want...)
}
```

Diễn tập đúng kịch bản RC5: reader tắt (`RECONCILE_ENABLED=false`) nên tin ghi thẳng vào Mongo không có event (kiểm 3s > delay 2s); resync quét theo `ActiveRooms` (nhánh `ca`, vì reader tắt nên không ai ghi activity) trên Mongo thật, đẩy record, worker publish event với id tự nhiên. Record `room_created` của resync trùng event fast path trong cửa sổ 5m nên stream bỏ, không có live event thừa.

**Step 6: Chạy, thấy fail**

Run: `make -s go ARGS="vet ./apps/core/"`
Expected: FAIL: `undefined: runResync`.

**Step 7: Code subcommand**

`apps/core/main.go`:
- `const usage = "usage: core [serve|probe]"` → `const usage = "usage: core [serve|probe|resync -from RFC3339 -to RFC3339 [-tenant T] [-room ID] [-rate N] [-dry-run]]"`.
- trong `realMain`, `switch {` thêm case đầu tiên (trước `case len(args) > 1:`):

```go
	case cmd == "resync":
		return resyncMain(args[1:])
```

`apps/core/resync_command.go`:

```go
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/ivannguyendev/chatim/apps/core/internal/config"
	"github.com/ivannguyendev/chatim/apps/core/internal/resync"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
)

func resyncMain(args []string) int {
	opts, err := resync.ParseArgs(args, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.ErrorContext(ctx, "invalid core config", "err", err)
		return 1
	}
	log := redactedLogger(logger, cfg)
	if err := runResync(ctx, cfg, opts, log, os.Stdout); err != nil {
		log.ErrorContext(ctx, "resync failed", "err", err)
		return 1
	}
	return 0
}

func runResync(ctx context.Context, cfg config.Config, opts resync.Options, log *slog.Logger, out io.Writer) error {
	c := &clients{}
	defer c.close(ctx, log)
	connectCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	err := c.connectMongo(connectCtx, cfg)
	cancel()
	if err == nil {
		err = c.connectNATS(cfg, log)
	}
	if err != nil {
		return err
	}
	st := mongostore.New(c.mongo.Database(cfg.MongoDB), mongostore.Options{})
	target := resync.Target{SubjectRoot: cfg.Work.SubjectRoot, Partitions: cfg.Work.Partitions}
	rep, err := resync.Run(ctx, resync.Deps{Rooms: st, Pages: st, Pub: c.js}, target, opts)
	fmt.Fprintln(out, rep)
	return err
}
```

Chỉ kết nối Mongo + NATS (không Redis, không bootstrap, không ensure stream: stream do core tạo; thiếu stream thì publish lỗi và tool exit 1). Dùng lại `connectMongo`/`connectNATS`/`close` nên credential được che như core.

**Step 8: Chạy, thấy pass**

Run: `make -s go ARGS="test -race -shuffle=on ./apps/core/ ./apps/core/internal/resync/..."`
Expected: PASS (itest skip).

Run: `make infra-up && make itest`
Expected: mọi package `ok`, gồm `TestRealInfraResyncDrillRepublishesWritesTheReaderMissed` (log `live events [...] arrived …`).

**Step 9: Chạy thật trên compose**

```bash
make image TARGET=apps/core && make core-up
FROM=$(date -u -v-10M +%Y-%m-%dT%H:%M:%SZ); TO=$(date -u -v+1M +%Y-%m-%dT%H:%M:%SZ)
docker exec chatim-core-1 /app resync -from "$FROM" -to "$TO" -dry-run
docker exec chatim-core-1 /app resync -from "$FROM" -to "$TO" -rate 200
docker exec chatim-core-1 /app resync
```

Expected: hai lệnh đầu in `resync rooms=… room_records=… message_records=… dry_run=true|false`, exit 0 (số có thể là 0 nếu 10 phút qua không có tin); lệnh thứ ba in usage + lỗi `-from`, exit 2. (`date -v` là cú pháp macOS; Linux dùng `date -u -d '-10 min' +%FT%TZ`.)

**Step 10: INDEXES.csv**

Thêm dòng:

```csv
apps/core/internal/resync,package,"Manual resync of a lost change feed range (/app resync): flags -from/-to (RFC3339), -tenant, -room, -rate (default 500/s, max 10000), -dry-run; walks rooms by store.ActiveRooms (activity bucket since -from or created in range, 500 per page) or one room; emits RoomInserted for rooms created in range and MessageInserted for main-timeline messages created in range (backward Page scan, stops before -from); publishes work records synchronously at -rate; prints counts",ParseArgs;Options;Options.Validate;Run;Deps;Target;Report;Rooms;Pages;Publisher;ErrUsage;DefaultRate;MaxRate,apps/core,unit (memstore + fake publisher; synctest pacing);itest drill (apps/core),D69;D81
```

`apps/core`: purpose thêm "; resync subcommand (resync_command.go: Mongo + NATS only, reuses client helpers and redaction)"; key_symbols thêm `resyncMain;runResync`; tests thêm `itest (resync drill republishes writes the reader missed)`; decisions thêm `D81`.

Kiểm 7 cột như Task 11.

**Step 11: Commit**

```bash
make fmt-check && make vet && make lint
git add apps/core/internal/resync/ apps/core/main.go apps/core/main_test.go apps/core/resync_command.go apps/core/it_core_test.go apps/core/resync_integration_test.go apps/core/reconcile_integration_test.go INDEXES.csv
git commit -m "feat(core): add the resync subcommand and a recovery drill"
```

---

### Task 15: Itest end-to-end của engine

Đưa lại khẳng định gốc của M2a.2 cho topo mới và phủ hai effect mới trên hạ tầng thật:
1. Tin ghi thẳng vào Mongo (bỏ qua core) → reader → work stream → worker `msg_created` → live event id tự nhiên; sau đó room activity của room đó có `LastSeq = 1`.
2. `room_created` tới từ **cả hai** đường: fast path (`CreateRoom`) và worker (room ghi thẳng vào Mongo).
3. `SendMessage` qua gRPC → worker `room_activity` ghi `LastSeq`/`LastMsgAt`/`LastChangeAt`.

Task 7 đã đổi file này sang khẳng định record `r:{room}`/`m:{room}-0-1` nằm trong work stream (`TestRealInfraReaderForwardsWritesThatSkippedTheCore` + `awaitRecords`). Từ Task 12 worker ack và WorkQueue xoá record sau `RECONCILE_DELAY`, nên khẳng định "record còn trong stream" chỉ còn đúng nhờ thời gian; Task này thay nó bằng khẳng định live event end-to-end (mạnh hơn, không phụ thuộc thời điểm), bỏ `awaitRecords`.

Không có bước "thấy fail": các test xác nhận hành vi đã xây ở Task 9–12 trên hạ tầng thật. Nếu một test fail ở lần chạy đầu, đó là lỗi của task trước: dừng và báo.

**Files:**
- Modify (viết lại cả file): `apps/core/reconcile_integration_test.go`

**Step 1: Test**

`apps/core/reconcile_integration_test.go` (viết lại cả file; `termStartSignal` đã chuyển sang `it_core_test.go` ở Task 14):

```go
package main

import (
	"testing"
	"time"

	"github.com/ivannguyendev/chatim/apps/core/internal/domain"
	"github.com/ivannguyendev/chatim/apps/core/internal/pbconv"
	"github.com/ivannguyendev/chatim/apps/core/internal/store"
	"github.com/ivannguyendev/chatim/apps/core/internal/store/mongostore"
	"github.com/ivannguyendev/chatim/pkg/ids"
	chatimv1 "github.com/ivannguyendev/chatim/pkg/pb/chatim/v1"
)

const itActivityPoll = 100 * time.Millisecond

func itStore(it *itInfra, c itCore) *mongostore.Store {
	return mongostore.New(it.mongo.Database(c.cfg.MongoDB), mongostore.Options{})
}

func awaitActivity(t *testing.T, st *mongostore.Store, room, seq uint64) domain.Room {
	t.Helper()
	deadline := time.Now().Add(itLiveLimit)
	for {
		r, err := st.Get(t.Context(), room)
		if err != nil {
			t.Fatalf("Get(%d): %v", room, err)
		}
		if r.LastSeq >= seq {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("room %d last seq = %d after %v, want %d", room, r.LastSeq, itLiveLimit, seq)
		}
		time.Sleep(itActivityPoll)
	}
}

func TestRealInfraWorkersPublishWritesThatSkippedTheCore(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	roomID := createRoom(t, dialCore(t, core.cfg))
	room := parseRoom(t, roomID)
	live := subscribeLive(t, it, core.cfg, roomID)
	core.awaitTerm(t)

	inserted := time.Now()
	outside := domain.Message{
		Room: room, Seq: 1, Tenant: itTenant, From: "migrator", Kind: domain.KindText, Text: "written outside the core",
		CID: "outside-1", CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	st := itStore(it, core)
	if res := st.Insert(t.Context(), []domain.Message{outside}); res[0].Outcome != store.Inserted {
		t.Fatalf("insert outside the core: %+v", res)
	}
	awaitLiveIDs(t, live, inserted, pbconv.MessageEventID(room, 0, 1))
	if got := awaitActivity(t, st, room, 1); got.LastMsgAt.IsZero() || got.LastChangeAt.Before(got.LastMsgAt) {
		t.Fatalf("room activity = %+v, want last message and change times", got)
	}
}

func TestRealInfraRoomCreatedComesFromTheFastPathAndTheWorkers(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	live := subscribeLive(t, it, core.cfg, "*")

	began := time.Now()
	fast := createRoom(t, dialCore(t, core.cfg))
	awaitLiveIDs(t, live, began, fast+"-created")

	core.awaitTerm(t)
	created := time.Now().UTC().Truncate(time.Millisecond)
	outside := domain.Room{ID: ids.NewRoomID(), Tenant: itTenant, Type: domain.RoomGroup, Name: "outside", CreatedBy: "migrator", CreatedAt: created, MemberCount: 1}
	owner := domain.Member{Room: outside.ID, Tenant: itTenant, User: "migrator", Role: domain.RoleOwner, JoinedAt: created}
	if err := itStore(it, core).Create(t.Context(), outside, []domain.Member{owner}); err != nil {
		t.Fatalf("create a room outside the core: %v", err)
	}
	awaitLiveIDs(t, live, time.Now(), pbconv.RoomID(outside.ID)+"-created")
}

func TestRealInfraSendMessageMovesRoomActivity(t *testing.T) {
	it := realInfra(t)
	core := startCore(t, it, itFastEffects)
	client := dialCore(t, core.cfg)
	roomID := createRoom(t, client)
	room := parseRoom(t, roomID)
	resp, err := client.SendMessage(caller(t.Context()), &chatimv1.SendMessageRequest{RoomId: roomID, Cid: "activity-1", Text: "moves the room"})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	got := awaitActivity(t, itStore(it, core), room, resp.GetSeq())
	if got.LastSeq != resp.GetSeq() || got.LastMsgAt.IsZero() || got.LastChangeAt.IsZero() {
		t.Fatalf("room activity = %+v, want last seq %d with message and change times", got, resp.GetSeq())
	}
}
```

Id event `room_created` là `{room}-created` (bảng tính năng); nếu Task 1 đặt hàm `pbconv` cho id này thì dùng hàm đó thay cho phép nối chuỗi ở hai chỗ trên. Đăng ký `live.{tenant}.room.*.>` nên test không phụ thuộc token loại của subject `room_created`.

**Step 2: Biên dịch**

Run: `make -s go ARGS="vet ./apps/core/"`
Expected: sạch (không còn `termStartSignal` trùng định nghĩa; import không dư).

**Step 3: Chạy trên hạ tầng thật**

Run: `make infra-up && make itest`
Expected: mọi package `ok`, gồm `TestRealInfraWorkersPublishWritesThatSkippedTheCore` (log `live events [<room>-0-1] arrived …` — khoảng delay 2s + một nhịp fetch), `TestRealInfraRoomCreatedComesFromTheFastPathAndTheWorkers`, `TestRealInfraSendMessageMovesRoomActivity`, `TestRealInfraResyncDrillRepublishesWritesTheReaderMissed`, `TestRealInfraStopUnderLoadKeepsEveryAckedMessage`.

**Step 4: Commit**

```bash
make fmt-check && make vet && make lint
git add apps/core/reconcile_integration_test.go
git commit -m "test(core): cover the effect engine end to end on real infra"
```

---

### Task 16: Docs

Task docs, rủi ro thấp: controller kiểm nhanh, không reviewer. Không đổi code.

**Files:**
- Modify: `docs/designs/261005-chatim-architecture.md`
- Modify: `docs/roadmap.md`
- Modify: `README.md`
- Modify: `INDEXES.csv`
- Modify: `CLAUDE.md`
- Modify: `docs/plans/2026-10-05-m2b1-effect-engine.md` (thêm mục kết quả)

**Step 1: Thiết kế**

- **§5**, dòng `rooms`: cột Index thành `{t, dm_key}` unique partial; `{act_bucket}` (D69); `{ca}` (nhánh "tạo trong khoảng" của truy vấn resync, M2b.1); cột Trạng thái thành `Đã xây (tạo; activity ls/lm/lc/ab, M2b.1)`. Thêm câu dưới bảng: "`rooms.ls/lm/lc/ab` chỉ đổi qua `$max` của effect `room_activity` (không bao giờ lùi); `ab = floor(lc/1h)` là giờ hoạt động **cuối**, nên truy vấn resync lấy `ab ≥ giờ(from)` hoặc `ca ∈ [from, to]`."
- **§8.2** đổi tiêu đề thành `### 8.2 Reader [Đã xây, M2b.1]`, thay đoạn bằng: "Package `reconcile` là reader, chạy trên core giữ slot 0, mỗi lần nhận slot 0 là một term (log `reconcile term started`). Đọc nhật ký commit qua `store.ChangeFeed`/`store.Cursor` (Mongo: change stream cấp database lọc insert của `messages` và `rooms`, vị trí ở `reconciler_state._id = "changes"`), dựng **record** (`work.Record`, chỉ khoá + `CommittedAt`, 33 byte, D80) và publish vào work stream với `Nats-Msg-Id` = id record (`m:{room}-{thread}-{seq}`, `r:{room}`), cửa sổ `RECONCILE_WINDOW`, retry vô hạn. Vị trí xác nhận = prefix record đã được work stream ack, lưu mỗi `RECONCILE_CONFIRM_EVERY`. Reader không chờ delay, không tra mark, không dựng event. Mất lịch sử (`ErrFeedHistoryLost`) → log Error, `Forget`, bắt đầu từ bây giờ, phục hồi bằng `/app resync` (D52, D81)."
- **§8.3** đổi tiêu đề thành `### 8.3 Effect engine [Đã xây phần M2b.1]`. Trong "Topo (a)": mục 1 thêm "Work stream `CHATIM_WORK`: WorkQueue, file, replica `EVT_STREAM_REPLICAS`, `MaxAge` 2h, chống trùng 2m, 32 partition `work.p{n}`, partition = `slot % 32` (D79)"; mục 3 thành "**Worker** (`effects.Workers`) ở mọi core: partition n do core giữ slot n tiêu thụ (consumer durable `work-p{n}`, kiểm `Owns(n)` trước mỗi fetch, lô ≤ `WORK_FETCH_BATCH`); chạy effect theo registry, trước mỗi effect chờ `max(CommittedAt) + delay`; record chỉ ack khi mọi effect xong, lỗi → `Nak(WORK_RETRY_DELAY)`." Thêm bảng registry hiện có:

  | Change | Effect (thứ tự) | Delay | Ack mark | Ghi |
  |---|---|---|---|---|
  | `MessageInserted` | `room_activity` | 0 | — | bulk `$max` lên `rooms`, 1 write/room/lô |
  | `MessageInserted` | `msg_created` | `RECONCILE_DELAY` | có | tra mark theo lô; `Find` tin chưa mark; publish + chờ PubAck |
  | `RoomInserted` | `room_created` | `RECONCILE_DELAY` | không | đọc room, publish (stream bỏ trùng với fast path `CreateRoom`) |

  Đoạn "Room activity (D69)": thay "ghi theo bulk 256 hoặc W_r = 1–5s; flush khi actor retire/shutdown" bằng "chỉ worker ghi, gom theo lô fetch (≤ `WORK_FETCH_BATCH` record → ≤ 1 write mỗi room mỗi lô); actor không ghi (owner chốt 2026-10-05)". Đoạn "Resync (D69)": thêm đầu đoạn "`/app resync -from -to [-tenant] [-room] [-rate] [-dry-run]` (D81, M2b.1): quét room `ab ≥ giờ(from)` hoặc `ca ∈ [from, to]`, record `RoomInserted` cho room tạo trong khoảng, scan ngược timeline chính tới khi `ts < from`, đẩy record vào work stream theo `-rate` (mặc định 500/s); diễn tập bằng itest `TestRealInfraResyncDrillRepublishesWritesTheReaderMissed`. Chưa có thread và `message_edits`/`pin_actions` nên chỉ quét timeline chính; M2b.2/M2b.3 thêm quét `{room, ts}`."
- **§11**: dòng `CHATIM_EVT` giữ; thêm dòng "`CHATIM_WORK`: work stream nội bộ của effect engine (§8.3), không phải event cho consumer."
- **§12**: câu mở đầu `(13 luật)` → `(15 luật)`. Bảng:
  - RC1, cột Metric: "`reconcile_lag_seconds` (worker chạy trễ so với delay của effect, xuất ở mọi core), `work_failures_total`, `effect_dropped_total{effect}`, `reconcile_running` (reader), `reconcile_dropped_total` (change hỏng reader bỏ); alert `ChatimReconcilerAbsent`, `ChatimReconcilerLagging`, `ChatimReconcilerDropping`, `ChatimWorkFailing`, `ChatimEffectDropping`".
  - RC3, cột Metric: "`reconcile_terms_total`, `reconcile_forwarded_total`".
  - RC4, cột Metric: "`reconcile_republished_total{effect}` (worker gửi vì không có mark), `publish_dropped_total{reason}`, `ack_marks_dropped_total{reason}`; alert `ChatimRepublishSurge`, `ChatimEventsDropped`".
  - RC5, cột Detector: thêm "phục hồi bằng `/app resync` (D81)".
  - Câu "Vận hành": thêm "tốc độ xả backlog đo trên dev ở `docs/poc/README.md` (W1), chỉ kiểm công cụ".
- **§13**, gạch đầu dòng "Vòng đời core" thay bằng: "**Vòng đời core:** config kiểm ở boot (`apps/core/internal/config`, mỗi component có `Validate()`). Khởi động: publisher → flusher → cid batcher → router → slot manager → workers → reader → gRPC (chỉ mở sau khi khởi động sạch, D40). Dừng: `/readyz` false → drain → gRPC → reader (`RECONCILE_DRAIN + 1s`) → workers (`WORK_DRAIN + 1s`) → router → cid batcher → flusher → publisher → nhả slot → đóng client, trong `CORE_SHUTDOWN_BUDGET` (26.2s/28s; tăng mốc nào phải tăng budget và `stop_grace_period` 33s). Mỗi RPC có `CORE_REQUEST_DEADLINE` (D42); client gRPC tắt service config từ DNS (D43)." Thêm câu: "Core có ba JetStream client: publisher fast path, reader, worker (`WORK_PARTITIONS × WORK_FETCH_BATCH` publish đang bay)."
- **§14**: dòng "NATS chết" → "…; reader/worker retry tới khi NATS về; …"; dòng "Reader chậm/ngừng quá cửa sổ oplog" → "Alert RC5; `/app resync` (§8.3, D81)".
- **§17.2**: thêm ba dòng cuối bảng:

```markdown
| D79 | Work stream `CHATIM_WORK` (WorkQueue, file, `MaxAge` 2h, chống trùng 2m) chia 32 partition `work.p{n}` theo `slot % 32`; consumer durable `work-p{n}` do core giữ slot n tiêu thụ, kiểm `Owns(n)` trước mỗi fetch; ack khi mọi effect của record xong, lỗi → `Nak(WORK_RETRY_DELAY)` | Một consumer theo mỗi slot (1024); một consumer chung, worker tự lọc; partition theo core id; hàng đợi trên Redis | 32 đủ chia tải cho ≤ 32 core với ít consumer; gắn với slot nên worker thường chạy trên chủ room (cache ấm); WorkQueue tự xoá record đã ack nên retention theo backlog chứ không theo lưu lượng; Redis dedupe không bền (D44). Owner chốt 2026-10-05 |
| D80 | Record work stream chỉ mang khoá (kind, room, thread, seq, `CommittedAt`; 33 byte), id `m:{room}-{thread}-{seq}` / `r:{room}`; worker đọc doc khi effect cần (`msg_created` chỉ `Find` tin chưa mark) | Đẩy doc đầy đủ; đẩy event dựng sẵn từ reader | Record nhỏ giữ work stream ≈ 33B/tin thay vì 300–500B (§8.3); phần lớn tin đã có mark nên không cần đọc lại; event chỉ dựng ở effect, dùng chung cho fast path và đường bù (RC2) |
| D81 | Resync là subcommand `/app resync` của binary core: cùng image, secret, config; quét `rooms` theo `ab ≥ giờ(from)` hoặc `ca ∈ [from, to]`, scan ngược timeline chính, đẩy record vào work stream có `-rate`; worker chạy effect như bình thường | Binary riêng trong `tools/`; resync tự động khi history lost; tool publish event trực tiếp | Không thêm thứ phải deploy và cấp secret; đi qua work stream nên dùng chung registry + chính sách (delay, mark) và bỏ trùng theo id; tự động dễ sinh hàng triệu bản trùng (D69). Owner chốt 2026-10-05 |
```

**Step 2: Roadmap**

- Dòng M2b.1, cột Trạng thái thay bằng:

```markdown
✅ `dev-done` (trên `feat/m2b`, chưa merge `main`) — [plan](plans/2026-10-05-m2b1-effect-engine.md); work stream 32 partition, worker mọi core, `room_created`, room activity, `/app resync`, 15 luật alert (D79–D81)
```

- Dòng M2b.2, cột Trạng thái `Chưa` → `⏭ Tiếp theo — cần plan`.
- Mục "Mục mang sang M5": thêm dòng mở đầu "Từ M2b.1, chi tiết ở [plan, mục Kết quả thực thi](plans/2026-10-05-m2b1-effect-engine.md#kết-quả-thực-thi):" và các lỗi Minor controller ghi ở mục đó (nếu chưa có gì, để một gạch đầu dòng "Resync: thứ tự event trong room là ngược; scan room bận từ tin mới nhất; lệch đồng hồ sát mép `-from`" — ba giới hạn ghi ở Task 14).

**Step 3: README**

- Dòng "Trạng thái" đầu file: thay "tiếp theo là M2b.0 theo [roadmap]" bằng "M2b.0 và M2b.1 (effect engine: reader → work stream → worker mọi core, `room_created`, room activity, `/app resync`) xong trên nhánh `feat/m2b`; tiếp theo là M2b.2 theo [roadmap]".
- Bảng "Kiến trúc", dòng `core`: "reconciler bù event bị bỏ từ change stream" → "reader đọc change stream (`messages`, `rooms`) đẩy record vào work stream, worker ở mọi core chạy effect (event bù, room activity)".
- Bảng env: các dòng sau phải đúng nội dung này (thay nếu Task 6/7 viết khác, thêm nếu thiếu; `WORK_FETCH_*`, `WORK_RETRY_DELAY`, `WORK_DRAIN`, `CORE_SHUTDOWN_BUDGET` đã sửa ở Task 12):

```markdown
| `RECONCILE_ENABLED` | `true` | Bật reader (chỉ core giữ slot 0 chạy). Tắt thì không có record mới vào work stream; worker vẫn chạy trên mọi core, publisher vẫn ghi mark |
| `RECONCILE_DELAY` | `5s` | Delay của effect `msg_created` và `room_created`: worker chỉ chạy khi `CommittedAt + D` đã qua. Luôn kiểm ở boot: dài hơn `PUB_ACK_TIMEOUT` + 10ms + 1s và ngắn hơn `EVT_STREAM_DUPLICATES`; với mặc định 5s, `PUB_ACK_TIMEOUT` phải dưới khoảng 3,99s. `AckWait` của work stream = D + 30s |
| `RECONCILE_WINDOW`, `RECONCILE_BATCH` | `1024` / `256` | Số record reader publish đang bay tối đa vào work stream (JetStream client riêng); số change xử lý một lần |
| `RECONCILE_CONFIRM_EVERY` | `1s` | Nhịp lưu vị trí đã xác nhận (prefix record đã được work stream ack) và kiểm lại slot 0 |
| `RECONCILE_DRAIN` | `1s` | Lúc dừng reader: chờ publish đang bay rồi lưu vị trí lần cuối; bước dừng chiếm `RECONCILE_DRAIN + 1s` |
| `RECONCILE_ROOM_CACHE` | `65536` | Số room giữ `room_type` trong cache của effect `msg_created` |
| `WORK_STREAM`, `WORK_SUBJECT_ROOT`, `WORK_PARTITIONS`, `WORK_MAX_AGE`, `WORK_DUPLICATES` | `CHATIM_WORK` / `work` / `32` / `2h` / `2m` | Work stream (WorkQueue, file, replica = `EVT_STREAM_REPLICAS`), subject `work.p{n}`, consumer `work-p{n}` do core giữ slot n tiêu thụ; `WORK_DUPLICATES` phải dài hơn `RECONCILE_CONFIRM_EVERY + RECONCILE_DRAIN` |
```

- "Lệnh hay dùng": thêm sau dòng `make e2e`:

```
    docker exec chatim-core-1 /app resync -from 2026-10-05T08:00:00Z -to 2026-10-05T09:00:00Z [-tenant T] [-room ID] [-rate 500] [-dry-run]   # phục hồi khoảng mất của change feed (RC5)
```

  và một đoạn ngắn ngay dưới khối lệnh: "`/app resync` dùng config và secret của core, chỉ nối Mongo + NATS, đẩy record vào work stream; worker chạy effect như bình thường. Giới hạn: chỉ timeline chính; thứ tự event trong một room là ngược; event cũ hơn 5m (cửa sổ chống trùng) và mark đã hết hạn (1h) thành bản trùng thật, consumer bỏ trùng theo id; chọn `-from` rộng hơn khoảng mất vài phút. Chạy `-dry-run` trước."
- "Cấu trúc": dòng `apps/core/` thêm `effects,work,resync` vào danh sách `internal/{…}`.

**Step 4: INDEXES.csv**

- Kiểm có đủ dòng cho `apps/core/internal/work`, `apps/core/internal/work/worktest`, `apps/core/internal/effects`, `apps/core/internal/resync` (Task 5/6/8/14 thêm); thiếu dòng nào thì thêm theo key symbols của hợp đồng.
- `apps/core/internal/reconcile`: purpose viết lại "Change reader on the slot 0 owner: one term per lead opens store.ChangeFeed (logs reconcile term started), turns MessageInserted/RoomInserted changes into work records and publishes them to the work stream (Nats-Msg-Id = record id) on its own JetStream client within RECONCILE_WINDOW, unbounded retry; confirms the position past the prefix the work stream acked every RECONCILE_CONFIRM_EVERY; history lost -> error log + Forget; Stats (running, terms, forwarded, dropped, history lost)"; decisions thêm `D66;D79;D80`.
- `docs/plans/2026-10-05-m2b1-effect-engine.md`: dòng này đã có (commit cùng plan); chỉ kiểm tồn tại và đủ 7 cột, không thêm dòng mới.
- `deploy/compose/docker-compose.yml` (nếu có dòng): `stop_grace_period 33s`.

Kiểm: `python3 -c "import csv;print({len(r) for r in csv.reader(open('INDEXES.csv')) if r})"` → Expected: `{7}`.

**Step 5: CLAUDE.md**

- Mục Project: thêm vào danh sách "Done on `feat/m2b`": "- M2b.1 (plan `docs/plans/2026-10-05-m2b1-effect-engine.md`): change reader on slot 0 → `CHATIM_WORK` work stream (32 partitions by slot) → effect workers on every core (D79–D81); `room_created` (fast path + worker); room activity `ls/lm/lc/ab` with `$max`; `/app resync` with a drill itest; stop budget 28s." Câu "Next is M2b.1 (effect engine); its plan is not written yet." → "Next is M2b.2 (edit + delete); its plan is not written yet."
- Mục Commands: thêm dòng `docker exec chatim-core-1 /app resync -from RFC3339 -to RFC3339 [-tenant T] [-room ID] [-rate 500] [-dry-run]   # replay a lost change feed range into the work stream`.
- Thay cả khối **Event reconciliation (`apps/core/internal/reconcile`, D52).** bằng:

```markdown
**Effect engine (`reconcile` reader, `work`, `effects`; D52, D65, D66, D79–D81).**
- The reader (`apps/core/internal/reconcile`) runs only on the slot 0 owner (`Owns(0)`, no extra lease); each lead is a term that opens the feed and logs `reconcile term started`. `RECONCILE_ENABLED=false` turns only the reader off; workers always run and marks are still written.
- Source is the database commit log through `store.ChangeFeed`/`store.Cursor` (`store.Change.Kind`: `MessageInserted`, `RoomInserted`); the reader imports no driver. `mongostore.Feed` watches the database for inserts into `messages` and `rooms` and stores the position in `reconciler_state` `_id: "changes"` (conditional, `w:majority`, never moves back). Every feed adapter must pass `storetest.RunFeed`.
- The reader turns each change into a `work.Record` (keys + `CommittedAt`, 33 bytes; id `m:{room}-{thread}-{seq}` or `r:{room}`) and publishes it to `CHATIM_WORK` (WorkQueue, subject `work.p{slot % 32}`, `Nats-Msg-Id` = record id) within `RECONCILE_WINDOW`, unbounded retry. It confirms only past the prefix the work stream acked, every `RECONCILE_CONFIRM_EVERY`. It never waits, reads marks or builds events.
- Workers (`effects.Workers`) run on every core: partition n is fetched only while `Owns(n)` (durable consumer `work-p{n}`, batches of `WORK_FETCH_BATCH`). The registry runs, in order, `room_activity` (delay 0, one bulk `$max` per batch on `rooms`) and `msg_created` (delay `RECONCILE_DELAY`, checks ack marks, `Find`s unmarked messages, publishes and waits for the PubAck on the third JetStream client) for `MessageInserted`, and `room_created` (delay `RECONCILE_DELAY`, no mark) for `RoomInserted`. A record is acked only when every effect returned nil; otherwise `Nak(WORK_RETRY_DELAY)`. A missing room or corrupt doc is dropped, counted in `effect_dropped_total`.
- `CreateRoom` publishes `room_created` on the fast path too; the stream drops the worker's copy by id within `EVT_STREAM_DUPLICATES`.
- Boot rules: `RECONCILE_DELAY < EVT_STREAM_DUPLICATES` and `RECONCILE_DELAY > PUB_ACK_TIMEOUT + 10ms + 1s` always apply (D65); `WORK_DUPLICATES > RECONCILE_CONFIRM_EVERY + RECONCILE_DRAIN`. The publisher marks only `msg_created`. Lost history (`ErrFeedHistoryLost`) logs an error, `Forget`s and restarts from now; recover the gap with `/app resync` (scans rooms with `ab >= hour(from)` or `ca` in range, replays the main timeline backwards into the work stream at `-rate`).
- Bootstrap anchor: `mongostore.Bootstrap` writes `reconciler_state` `{_id: "changes", at: $$CLUSTER_TIME}` only when no `at` exists, so a fresh database also forwards writes made before the first term; re-running it never moves the anchor or a confirmed token.
- Room activity (`rooms.ls/lm/lc/ab`, D69) is written only by the `room_activity` worker effect; `ab` is the hour of the last change and is indexed (`{ab: 1}`, plus `{ca: 1}` for the resync query).
```

- Mục **Process lifecycle**: dòng Start order → "publisher → flusher → cid batcher → router → slot manager → workers → reader → gRPC."; dòng Shutdown order → "… → gRPC → reader (`RECONCILE_DRAIN + 1s`) → workers (`WORK_DRAIN + 1s`) → router → … All of it fits within `CORE_SHUTDOWN_BUDGET`: the default plan is 26.2s of 28s, so raising any stop phase needs a higher budget and compose `stop_grace_period` (33s)."; thêm dòng "- `/app resync` is a one-shot subcommand: Mongo + NATS only, same config and redaction as serve."
- Mục **Detectors**: "(13 rules)" → "(15 rules)"; thêm "`reconcile_lag_seconds`, `reconcile_republished_total{effect}`, `effect_dropped_total{effect}`, `work_processed_total` and `work_failures_total` come from the workers on every core; reader metrics only on cores with `RECONCILE_ENABLED`." Câu "`reconcile_lag_seconds` is how far the reconciler runs behind `RECONCILE_DELAY`, not raw age." → "`reconcile_lag_seconds` is how far the workers run behind each effect's delay, not raw age."
- Mục **Docs**: thêm "M2b.1: `docs/plans/2026-10-05-m2b1-effect-engine.md` (executed; results and known issues at its end)."

**Step 6: Plan — chỗ cho kết quả**

Thêm cuối `docs/plans/2026-10-05-m2b1-effect-engine.md`:

```markdown
## Kết quả thực thi

(controller điền: commit của từng task, lệnh kiểm và kết quả, lỗi Minor còn mở, số đo xả backlog dev của Task 17)
```

**Step 7: Kiểm**

```bash
grep -n "24\.2s\|stop_grace_period (30s)\|SHUTDOWN_BUDGET.*25s\|13 rules\|13 luật" README.md CLAUDE.md docs/designs/261005-chatim-architecture.md docs/roadmap.md
```

Expected: không còn dòng nào nói về ngân sách hiện hành 24.2s/25s, grace 30s hay 13 luật (dòng lịch sử ghi rõ "M2b.0" được giữ).

**Step 8: Commit**

```bash
git add docs/ README.md INDEXES.csv CLAUDE.md
git commit -m "docs: record the M2b.1 effect engine and decisions D79-D81"
```

---

### Task 17: Kiểm chứng cuối milestone và đo xả backlog dev

Chạy một lần ở cuối, theo bảng "Verification and review budget" của CLAUDE.md (mốc cuối milestone). M2b.1 không phải milestone perf: không sweep corebench trước/sau; chỉ đo xả backlog trên dev để kiểm công cụ (số prod là P3 ở `docs/poc/README.md`).

**Step 1:** `make fmt-check && make vet && make lint && make vuln`
Expected: sạch; vuln: `Your code is affected by 0 vulnerabilities` (có thể kèm cảnh báo cấp module GO-2026-5932 ở `golang.org/x/crypto/openpgp`, có từ trước, code không gọi tới).

**Step 2:** `make test`
Expected: mọi package `ok` (gồm `apps/core/internal/{work,work/worktest,effects,resync}`).

**Step 3:** `make infra-up && make itest`
Expected: mọi package `ok`, gồm feed cấp database (Task 4), `TestBootstrapIsIdempotent` (index `ab:1`, `ca:1`), `TestRealInfraWorkersPublishWritesThatSkippedTheCore`, `TestRealInfraRoomCreatedComesFromTheFastPathAndTheWorkers`, `TestRealInfraSendMessageMovesRoomActivity`, `TestRealInfraResyncDrillRepublishesWritesTheReaderMissed`, `TestRealInfraStopUnderLoadKeepsEveryAckedMessage`.

**Step 4:** `make image TARGET=apps/core && make core-up && make e2e`
Expected: `e2e PASS: 40 messages before and 40 after killing core-1, no loss, no duplicate, every acked seq live`.

**Step 5:** `/metrics` trên cả hai core:

```bash
for c in chatim-core-1 chatim-core-2; do echo "== $c"; docker run --rm --network chatim_default curlimages/curl:8.11.1 -s http://$c:9090/metrics | grep -E '^chatim_core_(reconcile_running|reconcile_forwarded_total|reconcile_lag_seconds|reconcile_republished_total|effect_dropped_total|work_processed_total|work_failures_total|mongo_oplog_window_seconds)[ {]'; done
```

Expected:
- đúng một core có `chatim_core_reconcile_running 1` và `chatim_core_reconcile_forwarded_total` > 0 (sau e2e); core kia có `chatim_core_reconcile_running 0`;
- cả hai core có `chatim_core_work_processed_total` (tổng hai core > 0), `chatim_core_work_failures_total 0`, `chatim_core_reconcile_lag_seconds` (≥ 0), `chatim_core_reconcile_republished_total{effect="msg_created"}`, `chatim_core_reconcile_republished_total{effect="room_created"}`, `chatim_core_effect_dropped_total{effect="msg_created"} 0`, `chatim_core_effect_dropped_total{effect="room_created"} 0`, `chatim_core_mongo_oplog_window_seconds <số dương>`.

**Step 6:** `make alerts-check`
Expected: `SUCCESS: 15 rules found`.

**Step 7: Đo xả backlog trên dev (W1)**

Tạo backlog bằng cách tắt reader trong lúc ghi, rồi bật lại: reader đọc lại từ mốc bootstrap toàn bộ change đã ghi, đẩy vào work stream, worker hai core xử lý. Đo từ lúc `reconcile_forwarded_total` bắt đầu tăng tới lúc tổng `work_processed_total` hai core bằng `reconcile_forwarded_total` và đứng yên.

```bash
make core-down
make infra-reset && make infra-up
RECONCILE_ENABLED=false make core-up
make poc TOOL=corebench ARGS="-rate 1000 -duration 30s"
pmset -g therm | grep CPU_Speed_Limit
make core-up
mkdir -p bin/bench/m2b1-drain
for i in $(seq 1 180); do
  printf '%s ' "$(date +%s)"
  docker run --rm --network chatim_default curlimages/curl:8.11.1 -s http://chatim-core-1:9090/metrics http://chatim-core-2:9090/metrics \
    | awk '/^chatim_core_reconcile_forwarded_total/ {f+=$2} /^chatim_core_work_processed_total/ {p+=$2} /^chatim_core_reconcile_lag_seconds/ {if ($2>l) l=$2} END {printf "%d %d %.1f\n", f, p, l}'
  sleep 1
done | tee bin/bench/m2b1-drain/samples.txt
pmset -g therm | grep CPU_Speed_Limit
```

Mỗi dòng mẫu: `epoch forwarded processed max_lag_s`. Dừng vòng lặp (Ctrl-C) khi `processed` = `forwarded` ba mẫu liền mà `forwarded` không đổi.

Tính:
- `records` = `forwarded` cuối (≈ 1000 room `RoomInserted` + ~35K `MessageInserted`: 5s warmup + 30s đo ở 1000/s);
- `t_start` = epoch mẫu đầu tiên có `forwarded > 0`; `t_end` = epoch mẫu đầu tiên có `processed ≥ forwarded` cuối;
- tốc độ xả = `records / (t_end − t_start)` record/s; so với ingest 1000 tin/s (mục tiêu thiết kế ≥ 3× ingest đỉnh, D66 — trên dev chỉ ghi lại, không là tiêu chí đạt/không đạt);
- `reconcile_republished_total{effect="room_created"}` tổng hai core (≈ 1000: `room_created` không có mark nên worker gửi lại, stream bỏ trùng theo id) và `{effect="msg_created"}` (≈ 0: fast path đã mark).

Expected: vòng lặp kết thúc với `processed = forwarded`, `chatim_core_work_failures_total 0` trên cả hai core; có một con số record/s. Nếu `CPU_Speed_Limit` < 100 ở bất kỳ lần đọc nào, ghi kèm và coi số chỉ để tham khảo (CLAUDE.md, mục perf). Nếu `processed` không đuổi kịp `forwarded` trong 180 mẫu: dừng và báo.

Sau khi đo: `make core-up` đã chạy với reader bật; giữ cụm như vậy.

Ghi vào `docs/poc/README.md`, bảng "Kết quả dev", thêm dòng sau dòng `C1-M2a.3` (thay `<…>` bằng số đo):

```markdown
| W1 | Xả backlog work stream (reader slot 0 + worker 2 core), M2b.1 | ≥ 3× ingest đỉnh (D66; đo thật ở P3) | <records> record trong <t_end − t_start>s = <rate> record/s sau 30s ghi 1000 tin/s với reader tắt; lag worker tối đa <max_lag_s>s; CPU_Speed_Limit <giá trị> | Chỉ kiểm công cụ |
```

và đổi tiêu đề mục `## Kết quả dev (2026-09-30 → 2026-10-04)` thành `## Kết quả dev (2026-09-30 → <ngày đo, YYYY-MM-DD>)`.

```bash
git add docs/poc/README.md
git commit -m "docs(poc): record the dev work stream backlog drain"
```

**Step 8:** Ghi kết quả Step 1–7 (lệnh, Expected/thực tế, số đo W1) vào mục "Kết quả thực thi" cuối plan; mức sẵn sàng `dev-done` trên `feat/m2b` (chưa merge `main`: merge một lần sau M2b.4).

```bash
git add docs/plans/2026-10-05-m2b1-effect-engine.md
git commit -m "docs: record M2b.1 execution results"
```

---

#### Ghi chú cho controller (part C)

**Thay đổi hợp đồng cần thiết (đã viết vào Task 11; controller chấp nhận hoặc sửa hợp đồng chung):**
1. `ActiveRooms`: nhánh activity là `ab >= giờ(From)`, **không** chặn trên bằng `giờ(To)`. `ab` là `$max` nên chỉ giữ giờ hoạt động cuối; chặn trên làm resync bỏ sót đúng những room hoạt động trong khoảng mất và còn hoạt động sau `To` (room bận nhất). Nhánh `ca ∈ [From, To]` giữ nguyên, chữ ký không đổi. Câu trong "Hợp đồng chung" nên sửa thành "room có `ab ≥ giờ(From)` **hoặc** `ca` trong `[From, To]`".
2. Thêm index `{ca: 1}` trên `rooms` cạnh `{ab: 1}`: `$or` có một nhánh không index thì Mongo quét cả collection; `ca` bất biến nên không churn. Ghi ở thiết kế §5 (Task 16).
3. Cộng thêm (không đổi chữ ký đã chốt): `store.HourBucket(t) int64`, `store.MaxActiveLimit = 1000`, `(store.ActiveQuery).Validate() error`; `effects.RoomActivityName`, `effects.ActivityWriter`.
4. Write contract: loại mới `monotonic-max` cho `Rooms.TouchActivity` (bulk `$max`, không upsert, hội tụ); `Rooms.ActiveRooms` là `read`.

**Khớp với part B (đã đọc `parts/t6–t10.md`, `notes.md` và tin nhắn coordinator):**
- `effectsJS`: `WithPublishAsyncMaxPending(WORK_PARTITIONS × WORK_FETCH_BATCH)` (8192 mặc định) + `WithPublishAsyncTimeout(PUB_ACK_TIMEOUT)` — **khác** đề bài part C (`2 × FetchBatch`), theo phân tích part B: một core giữ cả 32 partition sẽ có tới 8192 publish đang bay; giới hạn nhỏ hơn gây "too many stalled" → `Nak` vô ích. Dùng chung client cho `work.NewQueue`.
- Constructor: `NewMessageCreated(MessageCreatedDeps{Marks, Messages, Rooms, JS}, MessageCreatedConfig{SubjectRoot: cfg.Stream.SubjectRoot, Delay: cfg.EffectDelay, RoomCache: cfg.EffectRoomCache})`, `NewRoomCreated(RoomCreatedDeps{Rooms, JS}, RoomCreatedConfig{SubjectRoot, Delay})`; `Republished()/Dropped()` trả `uint64` → kiểu `effectCounters`.
- Registry `MessageInserted: [room_activity, msg_created]`, `RoomInserted: [room_created]`.
- `effects.Config.Validate()` và `effects.Default*` có sẵn (Task 8) → `config/effects_components.go` dùng `effects.DefaultFetchBatch/FetchWait/RetryDelay/Drain`; thêm case `WORK_FETCH_BATCH=1025` (vượt `work.MaxAckPending`).
- `StopPlan.Workers = WORK_DRAIN + CloseTimeout` → 26.2s/28s, grace 33s (khớp ngữ nghĩa `Close` của Task 8).
- `TestLoadOverrides`: `Effects.Partitions = 16` (`WORK_PARTITIONS` override của Task 6).
- `it_infra_test.go` đã có stream riêng mỗi itest (Task 6) → Task 12 chỉ kiểm, không sửa.
- `reconcile.Stats{Running, Terms, Forwarded, Dropped, HistoryLost}`; Task 13 viết lại toàn bộ `metrics_wiring.go` + test sau khi Task 7 đã sửa chúng.
- Task 7 đổi `reconcile_integration_test.go` sang `TestRealInfraReaderForwardsWritesThatSkippedTheCore` + `awaitRecords`; Task 14 chỉ cắt `termStartSignal` sang `it_core_test.go`, Task 15 viết lại cả file (live event thay cho record, vì từ Task 12 WorkQueue xoá record đã ack).

**Lệch nhỏ khác, có lý do:**
- `RoomActivity` không có bộ đếm (không publish, không bỏ record); lỗi vào `work_failures_total`. `RoomInserted` không chạm activity (room mới được tìm qua nhánh `ca`).
- Metric: `effect_dropped_total{effect}` tách khỏi `reconcile_dropped_total` (reader: change hỏng; effect: doc không đọc được). `reconcile_lag_seconds`, `reconcile_republished_total{effect}`, `work_*` xuất trên **mọi** core. Luật cũ giữ biểu thức, chỉ sửa `summary`; thêm `ChatimWorkFailing`, `ChatimEffectDropping` → 15 luật.

**Giả định còn lại về part A (Task 1–5, chưa thấy file):**
- Task 3: `memstore.Rooms` còn trường `mu sync.RWMutex`, `rooms map[uint64]domain.Room`; `storetest.Run(t, open func(t) (store.Messages, store.Rooms))` giữ chữ ký.
- Task 4: `Bootstrap` vẫn gọi `ensureMemberIndexes(ctx, db)` (Task 11 thay dòng đó).
- Task 1/2: id event `room_created` = `{room}-created`; itest nghe `live.{t}.room.*.>` và lọc theo `Nats-Msg-Id` nên không phụ thuộc token loại; nếu Task 1 có hàm `pbconv` cho id này thì Task 15 dùng hàm đó.
- Task 5: `work.Record.ID()`, `work.Message(root, partitions, rec)` đúng hợp đồng (resync dùng).

**Caller đã rà:**
- `store.Rooms` thêm 2 method: adapter `memstore.Rooms`, `mongostore.Store`; `actor/spy_stores_test.go` nhúng `*memstore.Rooms`; còn lại chỉ dùng interface (`grpcsrv`, `actor`, `access`).
- `domain.Room` thêm 3 trường: vẫn comparable; `storetest.assertRoom`, `TestRoomCodecRoundTrip` (field list không đổi nhờ `omitempty`) vẫn đúng.
- `app` thêm `workers`: `startup_gate_test.go` (`workers: idle{}`), `stop_order_test.go` mới; `wire` gán qua `wireEffects`.
- `StopPlan` thêm `Workers`: `config.go` (field, `StopPlan()`, `total()`), `validate.go` (`stopPhases`), `shutdown.go`; test ngân sách 24200/24201/24600 → 26200/26201/26600 + case `WORK_DRAIN`.
- `realMain`: case `resync` đặt trước kiểm `len(args) > 1`; `main_test.go` thêm 2 case trả 2.

**Rủi ro:**
- Task 12 (cao): thứ tự start/stop và ngân sách; `TestShutdownDrainsWorkersAfterTheReconcilerAndBeforeTheRouter` chốt thứ tự dừng; compose grace 33s đi cùng budget 28s.
- `TestRealInfraReaderForwardsWritesThatSkippedTheCore` (Task 7) giữa Task 12 và 15 chỉ qua nhờ record sống ≥ `RECONCILE_DELAY` 2s trước khi bị xoá; nếu flaky ở Task 12–14, làm Task 15 sớm.
- `ActiveRooms` với `$or` + sort `_id` + `_id > After`: mỗi trang có thể đọc lại phần đầu khoảng (~N²/500 khoá index). Chấp nhận cho công cụ thủ công; xem lại khi có số thật (§2.3) trước M3.
- Resync dừng ở tin đầu tiên `CreatedAt < from`; lệch đồng hồ giữa core có thể bỏ sót tin sát mép → README/thiết kế dặn chọn `-from` rộng hơn.
- Task 17 Step 7 dùng `infra-reset` (xoá dữ liệu dev) và `pmset` (macOS).

