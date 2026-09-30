# chatim Phase 1 — Nền tảng & PoC (M0–M1) Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:separate-driven-development to implement this plan task-by-task.

**Goal:** Dựng nền monorepo Go + hạ tầng dev, các package lõi dùng chung, cơ chế sở hữu slot trên Redis, 3 công cụ PoC và image runtime, để kiểm chứng rủi ro R1–R5 trước khi viết `core`/`gateway`.

**Architecture:** Một Go module `github.com/ivannguyendev/chatim`. Package dùng chung ở `pkg/` (mã hoá `_id` nhị phân, room id, slot map). Cơ chế sở hữu slot là code production thật trong `apps/core/internal/slot` (R5). Công cụ đo ở `tools/poc/`, chỉ dùng cho PoC. Mọi lệnh Go chạy trong container `golang:1.26` qua `make`. Công cụ PoC chạy như container gắn vào mạng compose `chatim_default`, gọi dịch vụ bằng tên (`chatim-mongodb`, `chatim-redis`, `chatim-nats`). Hạ tầng dev: MongoDB replica set 1 node `rs0` (theo quy ước team), Redis, NATS JetStream.

**Tech Stack:** Go 1.26 (image `golang:1.26`) · Docker + Compose · MongoDB 8.2 · Redis 7.4.2 · NATS 2.15 (JetStream) · mongo-go-driver v2.9.1 · go-redis v9.22.0 · nats.go v1.54.0 · gws v1.10.2 · miniredis v2.39.0 (test) · distroless `static-debian12` (runtime)

**Tài liệu gốc:** [thiết kế Phase 1](../designs/260930-chat-core-gateway-design.md) · [nghiên cứu](../research/260930-opensource-chat-architecture-research.md)

> Toàn bộ code và lệnh `make` trong plan này đã được chạy thật trong Docker (Go 1.26.8 linux/amd64, 2026-09-30): fmt-check, vet, `test -race`, chạy thử 3 công cụ trong mạng compose, build image runtime. Nếu một bước cho ra kết quả khác "Expected", dừng lại và báo, đừng sửa cho qua.
> Code trong plan là **phiên bản cuối sau review** khi thực thi plan (2026-09-30): một số chi tiết đã được sửa qua các vòng review — lý do nằm trong phần mô tả của từng task và trong `git log` của branch `feat/phase1-foundation-poc`.

---

## Phạm vi

- **Plan này:** M0 (nền tảng) + M1 (PoC R1–R5).
- **Không nằm trong plan này:** M2–M5 (core, gateway, hardening). Kết quả PoC có thể đổi thiết kế, nên mỗi milestone sẽ có plan chi tiết riêng *sau khi* có kết quả M1 — xem [Roadmap](#roadmap-sau-m1).

## Quy ước (bắt buộc)

- **Build/test/chạy Go bằng Docker.** Máy chỉ cần Docker; không dùng `go` trên host. Lệnh chung: `make go ARGS="…"`; các lệnh tắt: `make test`, `make vet`, `make fmt-check`, `make tidy`, `make poc TOOL=… ARGS="…"`. Thêm `-s` (`make -s …`) để ẩn dòng `docker run` được in ra.
- **Không viết comment trong code**: Go, test, YAML, shell, Makefile, `.env.example`. Tên hàm/kiểu/test phải tự nói lên ý nghĩa. Ràng buộc cần giải thích được ghi trong plan, tài liệu thiết kế hoặc commit message.
- **TDD** cho mọi package có logic (`pkg/*`, `apps/*`, `tools/poc/internal/*`): viết test → chạy thấy FAIL → code → chạy thấy PASS → commit. Công cụ đo `tools/poc/<tool>` không có unit test; chúng được kiểm bằng chạy thử có kết quả mong đợi.
- **Mỗi file code < 200 dòng**; `gofmt` sạch (`make fmt-check`).
- **Commit** theo Conventional Commits, không nhắc tới AI, không commit `.env`, dữ liệu thật hay file sinh ra (`rooms.txt`, `real-texts.txt`, `bin/`).
- **Quy tắc sẵn sàng sharding** (thiết kế mục 4.1): `_id` các collection lớn luôn có prefix `room_id`; mọi truy vấn có prefix room; không transaction nhiều document; `MONGO_URI` lấy từ cấu hình.
- Chạy mọi lệnh từ thư mục gốc repo. `MONGO_ROOT_PASSWORD` chỉ dùng chữ, số, `-` hoặc `_`, vì Makefile ghép nó thẳng vào `MONGO_URI` mà không mã hoá URL.

---

### Task 0: Tạo branch và commit tài liệu thiết kế

**Files:** không tạo mới; commit các file đang có (`.gitignore`, `README.md`, `docs/`).

**Step 1: Tạo branch**

Run: `git switch -c feat/phase1-foundation-poc`
Expected: `Switched to a new branch 'feat/phase1-foundation-poc'`

**Step 2: Commit tài liệu**

```bash
git add .gitignore README.md docs/
git commit -m "docs: add architecture research and phase 1 design"
```

Expected: commit chứa `.gitignore`, `README.md`, `docs/research/…`, `docs/designs/…`, `docs/plans/…`.

---

### Task 1: Makefile (Go qua Docker) và Go module

`GO_RUN` mount repo vào `/src` và giữ cache module/build trong 2 named volume (`chatim-gomod`, `chatim-gocache`), nên từ lần chạy thứ hai sẽ nhanh. `POC_RUN` gắn thêm mạng compose và ghép `MONGO_URI` từ `.env` (dùng `-include .env`). `poc` build binary vào `bin/` rồi chạy chính binary đó trong container, để server khởi động tức thì. `image` build image runtime (Task 11). `check-env` dừng sớm với thông báo rõ khi thiếu `.env` (nếu không, `poc` sẽ ghép `MONGO_URI` rỗng và lỗi xác thực ở tận trong chương trình).

**Files:**
- Create: `Makefile`, `.env.example`, `go.mod`
- Modify: `.gitignore` (thêm cuối file)

**Step 1: Tạo `Makefile`** (thụt lề bằng TAB)

`Makefile`:

```make
-include .env

GO_IMAGE ?= golang:1.26
NETWORK  ?= chatim_default
COMPOSE  := docker compose -f deploy/compose/docker-compose.yml --env-file .env
GO_RUN   := docker run --rm -v "$(CURDIR)":/src -w /src -v chatim-gomod:/go/pkg/mod -v chatim-gocache:/root/.cache/go-build -e GOFLAGS=-buildvcs=false
POC_RUN  := $(GO_RUN) --network $(NETWORK) -e "MONGO_URI=mongodb://$(MONGO_ROOT_USER):$(MONGO_ROOT_PASSWORD)@chatim-mongodb:27017/?replicaSet=rs0&authSource=admin"

.PHONY: go check-env test vet fmt-check tidy poc image infra-up infra-down infra-reset

go:
	$(GO_RUN) $(GO_IMAGE) go $(ARGS)

check-env:
	@test -f .env || (echo "missing .env: run cp .env.example .env" && exit 1)

test:
	$(GO_RUN) $(GO_IMAGE) go test -race ./...

vet:
	$(GO_RUN) $(GO_IMAGE) go vet ./...

fmt-check:
	$(GO_RUN) $(GO_IMAGE) sh -c 'test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)'

tidy:
	$(GO_RUN) $(GO_IMAGE) go mod tidy

poc: check-env
	$(GO_RUN) $(GO_IMAGE) go build -o bin/$(TOOL) ./tools/poc/$(TOOL)
	$(POC_RUN) $(POC_FLAGS) $(GO_IMAGE) ./bin/$(TOOL) $(ARGS)

image:
	docker build -f deploy/docker/Dockerfile --build-arg TARGET=$(TARGET) -t chatim/$(notdir $(TARGET)):dev .

infra-up: check-env
	$(COMPOSE) up -d
	./scripts/wait-mongo-primary.sh

infra-down: check-env
	$(COMPOSE) down

infra-reset: check-env
	$(COMPOSE) down -v
```

**Step 2: Tạo `.env.example`**

`.env.example`:

```text
MONGO_ROOT_USER=chatim
MONGO_ROOT_PASSWORD=change-me
MONGO_PORT=27117
MONGO_CACHE_GB=3
REDIS_PORT=6380
NATS_PORT=4223
NATS_MON_PORT=8223
```

**Step 3: Thêm vào cuối `.gitignore`**

```gitignore

/bin/
rooms*.txt
real-texts*.txt

.env.*
!.env.example
```

`.env.*` chặn mọi biến thể như `.env.local`; riêng `.env.example` vẫn được commit.

**Step 4: Khởi tạo module (trong container)**

```bash
make -s go ARGS="mod init github.com/ivannguyendev/chatim"
make -s go ARGS="mod edit -go=1.26.0"
cat go.mod
```

Expected: `go: creating new go.mod: module github.com/ivannguyendev/chatim`; `go.mod` gồm `module github.com/ivannguyendev/chatim` và `go 1.26.0`. Lần đầu Docker sẽ pull `golang:1.26`.

**Step 5: Kiểm tra**

Run: `make -s fmt-check && echo ok`
Expected: `ok`

**Step 6: Commit**

```bash
git add Makefile .env.example go.mod .gitignore
git commit -m "chore: init go module with docker-based go tooling"
```

---

### Task 2: Hạ tầng dev (MongoDB rs0, Redis, NATS)

Mongo theo đúng quy ước team: replica set 1 node `rs0`, keyfile tự sinh trong volume ở lần khởi động đầu, container `mongodb-init` chạy `rs.initiate` một lần. Credential lấy từ `.env`. Port host mặc định lệch chuẩn (27117/6380/4223/8223) để không đụng các stack khác; công cụ PoC không dùng port host mà gọi thẳng tên dịch vụ trong mạng `chatim_default`. Tên mạng được ghim cứng trong compose, để không phụ thuộc tên thư mục chứa repo hay `COMPOSE_PROJECT_NAME`.

**Files:**
- Create: `deploy/compose/docker-compose.yml`, `scripts/wait-mongo-primary.sh`

**Step 1: Tạo `deploy/compose/docker-compose.yml`**

`deploy/compose/docker-compose.yml`:

```yaml
name: chatim

services:
  mongodb:
    image: mongo:8.2
    container_name: chatim-mongodb
    restart: unless-stopped
    environment:
      MONGO_INITDB_ROOT_USERNAME: ${MONGO_ROOT_USER:?set MONGO_ROOT_USER in .env}
      MONGO_INITDB_ROOT_PASSWORD: ${MONGO_ROOT_PASSWORD:?set MONGO_ROOT_PASSWORD in .env}
    entrypoint:
      - bash
      - -c
      - >-
        if [ ! -f /data/db/mongo-keyfile ]; then openssl rand -base64 756 > /data/db/mongo-keyfile;
        chmod 400 /data/db/mongo-keyfile; chown 999:999 /data/db/mongo-keyfile; fi;
        exec docker-entrypoint.sh mongod --replSet rs0 --bind_ip_all --keyFile /data/db/mongo-keyfile
        --wiredTigerCacheSizeGB ${MONGO_CACHE_GB:-3} --oplogSize 4096
    ports:
      - "${MONGO_PORT:-27117}:27017"
    volumes:
      - mongodb_data:/data/db
    healthcheck:
      test: ["CMD-SHELL", "mongosh --quiet -u \"$$MONGO_INITDB_ROOT_USERNAME\" -p \"$$MONGO_INITDB_ROOT_PASSWORD\" --authenticationDatabase admin --eval \"db.runCommand('ping').ok\""]
      interval: 10s
      timeout: 5s
      retries: 10
      start_period: 20s

  mongodb-init:
    image: mongo:8.2
    container_name: chatim-mongodb-init
    depends_on:
      mongodb:
        condition: service_healthy
    environment:
      MONGO_USER: ${MONGO_ROOT_USER}
      MONGO_PASSWORD: ${MONGO_ROOT_PASSWORD}
    entrypoint: ["bash", "-c"]
    command:
      - >-
        mongosh --host chatim-mongodb -u "$$MONGO_USER" -p "$$MONGO_PASSWORD" --authenticationDatabase admin --quiet
        --eval 'try { rs.status(); print("replica set already initialized") }
        catch (e) { rs.initiate({ _id: "rs0", members: [{ _id: 0, host: "chatim-mongodb:27017" }] }); print("replica set initialized") }'
    restart: "no"

  redis:
    image: redis:7.4.2-alpine
    container_name: chatim-redis
    restart: unless-stopped
    command: ["redis-server", "--appendonly", "yes"]
    ports:
      - "${REDIS_PORT:-6380}:6379"
    volumes:
      - redis_data:/data
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 3s
      retries: 10

  nats:
    image: nats:2.15-alpine
    container_name: chatim-nats
    restart: unless-stopped
    command: ["-js", "-sd", "/data", "-m", "8222"]
    ports:
      - "${NATS_PORT:-4223}:4222"
      - "${NATS_MON_PORT:-8223}:8222"
    volumes:
      - nats_data:/data
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:8222/healthz"]
      interval: 5s
      timeout: 3s
      retries: 10

networks:
  default:
    name: chatim_default

volumes:
  mongodb_data:
  redis_data:
  nats_data:
```

**Step 2: Tạo `scripts/wait-mongo-primary.sh`**

`scripts/wait-mongo-primary.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "$0")/../.env"
for _ in $(seq 1 90); do
  if docker exec chatim-mongodb mongosh --quiet -u "$MONGO_ROOT_USER" -p "$MONGO_ROOT_PASSWORD" \
    --authenticationDatabase admin --eval 'db.hello().isWritablePrimary' 2>/dev/null | grep -q true; then
    echo "mongodb primary ready"
    exit 0
  fi
  sleep 1
done
echo "mongodb primary not ready after 90s" >&2
exit 1
```

Run: `chmod +x scripts/wait-mongo-primary.sh`

**Step 3: Chạy hạ tầng**

Tạo `.env` từ mẫu rồi sửa `MONGO_ROOT_PASSWORD` (chữ, số, `-` hoặc `_`) trước khi chạy tiếp:

```bash
cp .env.example .env
docker compose -f deploy/compose/docker-compose.yml --env-file .env config -q && echo valid
make infra-up
```

Expected: `valid`, rồi các container `chatim-mongodb`, `chatim-redis`, `chatim-nats` Started, `chatim-mongodb Healthy`, cuối cùng `mongodb primary ready`. Lần đầu sẽ pull `nats:2.15-alpine`.

**Step 4: Kiểm tra từng dịch vụ**

```bash
docker exec chatim-redis redis-cli ping
curl -s http://localhost:8223/healthz
docker logs chatim-mongodb-init
```

Expected: `PONG` · `{"status":"ok"}` · `replica set initialized` (hoặc `already initialized` ở lần chạy sau).

**Step 5: Commit**

```bash
git add deploy/compose/docker-compose.yml scripts/wait-mongo-primary.sh
git commit -m "chore: add dev infrastructure (mongo rs0, redis, nats jetstream)"
```

---

### Task 3: `pkg/keys` — mã hoá `_id` nhị phân

Khoá `_id` của các collection clustered là các số big-endian ghép lại, nên thứ tự byte trùng thứ tự số và tin của một room nằm liền nhau. MongoDB so sánh BinData theo **độ dài trước**, rồi subtype, rồi byte, nên mọi khoá trong một collection phải cùng độ dài: `Msg` 24B (`room│thread_root│seq`, `thread_root = 0` là timeline chính), `Event` 16B (`room│pts`), `Edit` 28B (`room│thread│seq│version`). `MsgRange(room, thread, from, to)` trả khoảng nửa mở `[from, to)`; `MsgRange(room, thread, 0, math.MaxUint64)` là cả timeline.

**Files:**
- Create: `pkg/keys/keys.go`
- Test: `pkg/keys/keys_test.go`

**Step 1: Viết test**

`pkg/keys/keys_test.go`:

```go
package keys

import (
	"bytes"
	"cmp"
	"errors"
	"math"
	"math/rand/v2"
	"testing"
)

func TestMsgRoundTrip(t *testing.T) {
	cases := []struct{ room, thread, seq uint64 }{
		{1, 0, 1},
		{0x5f00aa11bb22cc33, 0, 1000},
		{math.MaxInt64, 42, math.MaxUint64},
	}
	for _, c := range cases {
		b := Msg(c.room, c.thread, c.seq)
		if len(b) != MsgLen {
			t.Fatalf("Msg%+v length = %d, want %d", c, len(b), MsgLen)
		}
		room, thread, seq, err := ParseMsg(b)
		if err != nil || room != c.room || thread != c.thread || seq != c.seq {
			t.Fatalf("ParseMsg(Msg%+v) = %d %d %d %v", c, room, thread, seq, err)
		}
	}
}

func TestMsgByteOrderMatchesNumericOrder(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 10_000; i++ {
		a := []uint64{rng.Uint64() % 4, rng.Uint64() % 4, rng.Uint64()}
		b := []uint64{rng.Uint64() % 4, rng.Uint64() % 4, rng.Uint64()}
		got := bytes.Compare(Msg(a[0], a[1], a[2]), Msg(b[0], b[1], b[2]))
		if want := compareTuples(a, b); got != want {
			t.Fatalf("compare %v vs %v = %d, want %d", a, b, got, want)
		}
	}
}

func TestMsgRangeSelectsOneTimelineWindow(t *testing.T) {
	lo, hi := MsgRange(7, 0, 10, 20)
	inRange := func(k []byte) bool { return bytes.Compare(k, lo) >= 0 && bytes.Compare(k, hi) < 0 }
	for seq := uint64(0); seq < 30; seq++ {
		if got, want := inRange(Msg(7, 0, seq)), seq >= 10 && seq < 20; got != want {
			t.Errorf("seq %d in range = %v, want %v", seq, got, want)
		}
	}
	if inRange(Msg(7, 1, 15)) {
		t.Error("thread timeline leaked into main timeline range")
	}
	if inRange(Msg(6, 0, 15)) || inRange(Msg(8, 0, 15)) {
		t.Error("another room leaked into range")
	}
}

func TestEventAndEditRoundTrip(t *testing.T) {
	room, pts, err := ParseEvent(Event(9, 77))
	if err != nil || room != 9 || pts != 77 {
		t.Fatalf("ParseEvent = %d %d %v", room, pts, err)
	}
	r, th, s, v, err := ParseEdit(Edit(9, 3, 5, 2))
	if err != nil || r != 9 || th != 3 || s != 5 || v != 2 {
		t.Fatalf("ParseEdit = %d %d %d %d %v", r, th, s, v, err)
	}
	if bytes.Compare(Edit(9, 3, 5, 1), Edit(9, 3, 5, 2)) >= 0 {
		t.Fatal("edit versions must sort ascending")
	}
}

func TestParseRejectsWrongLength(t *testing.T) {
	if _, _, _, err := ParseMsg(make([]byte, MsgLen-1)); !errors.Is(err, ErrLength) {
		t.Errorf("ParseMsg short key err = %v", err)
	}
	if _, _, err := ParseEvent(make([]byte, MsgLen)); !errors.Is(err, ErrLength) {
		t.Errorf("ParseEvent wrong key err = %v", err)
	}
	if _, _, _, _, err := ParseEdit(make([]byte, MsgLen)); !errors.Is(err, ErrLength) {
		t.Errorf("ParseEdit wrong key err = %v", err)
	}
}

func compareTuples(a, b []uint64) int {
	for i := range a {
		if c := cmp.Compare(a[i], b[i]); c != 0 {
			return c
		}
	}
	return 0
}
```

**Step 2: Chạy test, phải FAIL**

Run: `make -s go ARGS="test ./pkg/keys/"`
Expected: FAIL, build failed với `undefined: Msg` (và các hàm khác).

**Step 3: Viết code**

`pkg/keys/keys.go`:

```go
package keys

import (
	"encoding/binary"
	"errors"
)

const (
	MsgLen   = 24
	EventLen = 16
	EditLen  = 28
)

var ErrLength = errors.New("keys: invalid key length")

var be = binary.BigEndian

func Msg(room, threadRoot, seq uint64) []byte {
	b := make([]byte, MsgLen)
	putMsg(b, room, threadRoot, seq)
	return b
}

func ParseMsg(b []byte) (room, threadRoot, seq uint64, err error) {
	if len(b) != MsgLen {
		return 0, 0, 0, ErrLength
	}
	return be.Uint64(b[0:8]), be.Uint64(b[8:16]), be.Uint64(b[16:24]), nil
}

func MsgRange(room, threadRoot, fromSeq, toSeq uint64) (lo, hi []byte) {
	return Msg(room, threadRoot, fromSeq), Msg(room, threadRoot, toSeq)
}

func Event(room, pts uint64) []byte {
	b := make([]byte, EventLen)
	be.PutUint64(b[0:8], room)
	be.PutUint64(b[8:16], pts)
	return b
}

func ParseEvent(b []byte) (room, pts uint64, err error) {
	if len(b) != EventLen {
		return 0, 0, ErrLength
	}
	return be.Uint64(b[0:8]), be.Uint64(b[8:16]), nil
}

func Edit(room, threadRoot, seq uint64, version uint32) []byte {
	b := make([]byte, EditLen)
	putMsg(b, room, threadRoot, seq)
	be.PutUint32(b[24:28], version)
	return b
}

func ParseEdit(b []byte) (room, threadRoot, seq uint64, version uint32, err error) {
	if len(b) != EditLen {
		return 0, 0, 0, 0, ErrLength
	}
	return be.Uint64(b[0:8]), be.Uint64(b[8:16]), be.Uint64(b[16:24]), be.Uint32(b[24:28]), nil
}

func putMsg(b []byte, room, threadRoot, seq uint64) {
	be.PutUint64(b[0:8], room)
	be.PutUint64(b[8:16], threadRoot)
	be.PutUint64(b[16:24], seq)
}
```

**Step 4: Chạy test, phải PASS**

Run: `make -s go ARGS="test -race ./pkg/keys/"`
Expected: `ok  	github.com/ivannguyendev/chatim/pkg/keys`

**Step 5: Commit**

```bash
git add pkg/keys
git commit -m "feat(keys): binary _id encoding for clustered collections"
```

---

### Task 4: `pkg/ids` — room id ngẫu nhiên 63-bit

Room id ngẫu nhiên thì không đoán được, không lộ số lượng room, và ghi phân tán đều giữa các shard nếu sau này shard. Bit cao nhất luôn là 0 để vừa `int64`. `crypto/rand.Read` không bao giờ trả lỗi từ Go 1.24, nên giá trị lỗi được bỏ qua. Id trả cho client dạng chuỗi (`FormatRoomID`) vì số JSON mất chính xác khi vượt 2^53.

**Files:**
- Create: `pkg/ids/ids.go`
- Test: `pkg/ids/ids_test.go`

**Step 1: Viết test**

`pkg/ids/ids_test.go`:

```go
package ids

import (
	"errors"
	"testing"
)

func TestNewRoomIDIsPositiveInt64AndUnique(t *testing.T) {
	seen := make(map[uint64]bool, 10_000)
	for i := 0; i < 10_000; i++ {
		id := NewRoomID()
		if id == 0 || id>>63 != 0 {
			t.Fatalf("NewRoomID() = %d, want non-zero with top bit clear", id)
		}
		if seen[id] {
			t.Fatalf("NewRoomID() repeated %d", id)
		}
		seen[id] = true
	}
}

func TestRoomIDStringRoundTrip(t *testing.T) {
	id := NewRoomID()
	got, err := ParseRoomID(FormatRoomID(id))
	if err != nil || got != id {
		t.Fatalf("ParseRoomID(FormatRoomID(%d)) = %d, %v", id, got, err)
	}
}

func TestParseRoomIDRejectsInvalid(t *testing.T) {
	for _, s := range []string{"", "0", "-1", "abc", "9223372036854775808"} {
		if _, err := ParseRoomID(s); !errors.Is(err, ErrInvalidRoomID) {
			t.Errorf("ParseRoomID(%q) err = %v, want ErrInvalidRoomID", s, err)
		}
	}
}
```

**Step 2: Chạy test, phải FAIL**

Run: `make -s go ARGS="test ./pkg/ids/"`
Expected: FAIL, `undefined: NewRoomID`

**Step 3: Viết code**

`pkg/ids/ids.go`:

```go
package ids

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
)

var ErrInvalidRoomID = errors.New("ids: invalid room id")

func NewRoomID() uint64 {
	var b [8]byte
	for {
		_, _ = rand.Read(b[:])
		if id := binary.BigEndian.Uint64(b[:]) &^ (1 << 63); id != 0 {
			return id
		}
	}
}

func FormatRoomID(id uint64) string { return strconv.FormatUint(id, 10) }

func ParseRoomID(s string) (uint64, error) {
	id, err := strconv.ParseUint(s, 10, 63)
	if err != nil || id == 0 {
		return 0, fmt.Errorf("%w: %q", ErrInvalidRoomID, s)
	}
	return id, nil
}
```

**Step 4: Chạy test, phải PASS**

Run: `make -s go ARGS="test -race ./pkg/ids/"`
Expected: `ok  	github.com/ivannguyendev/chatim/pkg/ids`

**Step 5: Commit**

```bash
git add pkg/ids
git commit -m "feat(ids): random 63-bit room ids"
```

---

### Task 5: `pkg/slotmap` — room → slot, rendezvous hashing, key Redis

Dùng chung cho `core` (nhận slot) và `gateway` (định tuyến, M4). `Count = 1024`; đổi số này là đổi slot của mọi room. `Of` dùng bộ trộn MurmurHash3 (`mix64`) để room id liền nhau vẫn rải đều. `Score`/`Preferred` là rendezvous hashing: thêm một core thì chỉ những slot chuyển sang core mới bị đổi chủ. Key Redis: `chatim:core:<id>` (heartbeat, giá trị là địa chỉ gRPC), `chatim:slot:<n>` (lease, giá trị là core id), kênh `chatim:slots:changed`.

**Files:**
- Create: `pkg/slotmap/slotmap.go`, `pkg/slotmap/redis_keys.go`
- Test: `pkg/slotmap/slotmap_test.go`

**Step 1: Viết test**

`pkg/slotmap/slotmap_test.go`:

```go
package slotmap

import (
	"math/rand/v2"
	"slices"
	"testing"
)

func TestOfSpreadsRoomsEvenly(t *testing.T) {
	const n = 1_000_000
	rng := rand.New(rand.NewPCG(3, 4))
	sources := map[string]func(i int) uint64{
		"random":     func(int) uint64 { return rng.Uint64() >> 1 },
		"sequential": func(i int) uint64 { return uint64(i + 1) },
	}
	for name, next := range sources {
		counts := make([]int, Count)
		for i := 0; i < n; i++ {
			counts[Of(next(i))]++
		}
		mean := n / Count
		for s, c := range counts {
			if c < mean*8/10 || c > mean*12/10 {
				t.Fatalf("%s rooms: slot %d has %d rooms, mean %d", name, s, c, mean)
			}
		}
	}
}

func TestOfIsDeterministic(t *testing.T) {
	if Of(0x5f00aa11bb22cc33) != Of(0x5f00aa11bb22cc33) {
		t.Fatal("Of must return the same slot for the same room")
	}
}

func TestPreferredMovesOnlyTheNewCoreShare(t *testing.T) {
	before := []string{"core-a", "core-b", "core-c"}
	after := append(slices.Clone(before), "core-d")
	moved := 0
	for s := uint16(0); s < Count; s++ {
		was, now := Preferred(s, before), Preferred(s, after)
		if was == now {
			continue
		}
		moved++
		if now != "core-d" {
			t.Fatalf("slot %d moved %s -> %s, want moves only to core-d", s, was, now)
		}
	}
	if moved < Count*15/100 || moved > Count*35/100 {
		t.Fatalf("moved %d of %d slots, want about a quarter", moved, Count)
	}
}

func TestPreferredBalancesCores(t *testing.T) {
	cores := []string{"core-a", "core-b", "core-c"}
	share := map[string]int{}
	for s := uint16(0); s < Count; s++ {
		share[Preferred(s, cores)]++
	}
	for _, c := range cores {
		if share[c] < Count*25/100 || share[c] > Count*42/100 {
			t.Fatalf("core %s prefers %d of %d slots, want about a third", c, share[c], Count)
		}
	}
	if Preferred(0, nil) != "" {
		t.Fatal("Preferred with no cores must return empty string")
	}
}

func TestRedisKeys(t *testing.T) {
	if got := SlotKey(17); got != "chatim:slot:17" {
		t.Fatalf("SlotKey(17) = %q", got)
	}
	id, ok := CoreIDFromKey(CoreKey("core-a"))
	if !ok || id != "core-a" {
		t.Fatalf("CoreIDFromKey(CoreKey) = %q, %v", id, ok)
	}
	if _, ok := CoreIDFromKey("chatim:slot:1"); ok {
		t.Fatal("slot key must not parse as core key")
	}
}
```

**Step 2: Chạy test, phải FAIL**

Run: `make -s go ARGS="test ./pkg/slotmap/"`
Expected: FAIL, `undefined: Count` / `undefined: Of`

**Step 3: Viết `pkg/slotmap/slotmap.go`**

`pkg/slotmap/slotmap.go`:

```go
package slotmap

import (
	"encoding/binary"
	"hash/fnv"
)

const Count = 1024

func Of(room uint64) uint16 { return uint16(mix64(room) % Count) }

func Score(slot uint16, core string) uint64 {
	h := fnv.New64a()
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], slot)
	_, _ = h.Write(b[:])
	_, _ = h.Write([]byte(core))
	return mix64(h.Sum64())
}

func Preferred(slot uint16, cores []string) string {
	best, bestScore := "", uint64(0)
	for _, c := range cores {
		s := Score(slot, c)
		if best == "" || s > bestScore || (s == bestScore && c < best) {
			best, bestScore = c, s
		}
	}
	return best
}

func mix64(x uint64) uint64 {
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	x ^= x >> 33
	return x
}
```

**Step 4: Viết `pkg/slotmap/redis_keys.go`**

`pkg/slotmap/redis_keys.go`:

```go
package slotmap

import (
	"strconv"
	"strings"
)

const (
	CorePattern    = corePrefix + "*"
	ChangedChannel = "chatim:slots:changed"

	corePrefix = "chatim:core:"
	slotPrefix = "chatim:slot:"
)

func CoreKey(id string) string { return corePrefix + id }

func CoreIDFromKey(key string) (string, bool) { return strings.CutPrefix(key, corePrefix) }

func SlotKey(slot uint16) string { return slotPrefix + strconv.Itoa(int(slot)) }
```

**Step 5: Chạy test, phải PASS**

Run: `make -s go ARGS="test -race ./pkg/slotmap/"`
Expected: `ok  	github.com/ivannguyendev/chatim/pkg/slotmap`

**Step 6: Commit**

```bash
git add pkg/slotmap
git commit -m "feat(slotmap): room-to-slot mapping with rendezvous hashing"
```

---

### Task 6: `tools/poc/internal/latency` — đo percentile

`Recorder` dùng được ngay ở giá trị zero và an toàn khi nhiều goroutine cùng ghi. Percentile tính theo nearest-rank: phần tử thứ `ceil(p·n/100)` sau khi sắp xếp. `SummaryAndReset` lấy mẫu và xoá trong **một** lần khoá, để mẫu đến giữa hai bước không bị mất (dùng cho báo cáo định kỳ của `wsbench`).

**Files:**
- Create: `tools/poc/internal/latency/latency.go`
- Test: `tools/poc/internal/latency/latency_test.go`

**Step 1: Viết test**

`tools/poc/internal/latency/latency_test.go`:

```go
package latency

import (
	"math/rand/v2"
	"sync"
	"testing"
	"time"
)

func TestSummaryNearestRank(t *testing.T) {
	var r Recorder
	for _, i := range rand.Perm(100) {
		r.Add(time.Duration(i+1) * time.Millisecond)
	}
	s := r.Summary()
	want := Summary{Count: 100, P50: 50 * time.Millisecond, P95: 95 * time.Millisecond, P99: 99 * time.Millisecond, Max: 100 * time.Millisecond}
	if s != want {
		t.Fatalf("Summary() = %+v, want %+v", s, want)
	}
}

func TestSummaryEdgeCases(t *testing.T) {
	var r Recorder
	if s := r.Summary(); s != (Summary{}) {
		t.Fatalf("empty Summary() = %+v", s)
	}
	r.Add(7 * time.Millisecond)
	if s := r.Summary(); s.P50 != 7*time.Millisecond || s.P99 != 7*time.Millisecond || s.Count != 1 {
		t.Fatalf("single-sample Summary() = %+v", s)
	}
	r.Add(3 * time.Millisecond)
	r.Add(9 * time.Millisecond)
	if s := r.SummaryAndReset(); s.Count != 3 {
		t.Fatalf("SummaryAndReset() = %+v, want Count 3", s)
	}
	if r.Summary().Count != 0 {
		t.Fatal("SummaryAndReset must drop samples")
	}
}

func TestRecorderIsSafeForConcurrentUse(t *testing.T) {
	var r Recorder
	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Go(func() {
			for i := 0; i < 100; i++ {
				r.Add(time.Millisecond)
			}
		})
	}
	wg.Wait()
	if n := r.Summary().Count; n != 1000 {
		t.Fatalf("Count = %d, want 1000", n)
	}
}
```

**Step 2: Chạy test, phải FAIL**

Run: `make -s go ARGS="test ./tools/poc/internal/latency/"`
Expected: FAIL, `undefined: Recorder`

**Step 3: Viết code**

`tools/poc/internal/latency/latency.go`:

```go
package latency

import (
	"fmt"
	"slices"
	"sync"
	"time"
)

type Recorder struct {
	mu      sync.Mutex
	samples []time.Duration
}

type Summary struct {
	Count              int
	P50, P95, P99, Max time.Duration
}

func (r *Recorder) Add(d time.Duration) {
	r.mu.Lock()
	r.samples = append(r.samples, d)
	r.mu.Unlock()
}

func (r *Recorder) Summary() Summary {
	r.mu.Lock()
	s := slices.Clone(r.samples)
	r.mu.Unlock()
	return summarize(s)
}

func (r *Recorder) SummaryAndReset() Summary {
	r.mu.Lock()
	s := slices.Clone(r.samples)
	r.samples = r.samples[:0]
	r.mu.Unlock()
	return summarize(s)
}

func summarize(s []time.Duration) Summary {
	if len(s) == 0 {
		return Summary{}
	}
	slices.Sort(s)
	return Summary{Count: len(s), P50: rank(s, 50), P95: rank(s, 95), P99: rank(s, 99), Max: s[len(s)-1]}
}

func (s Summary) String() string {
	return fmt.Sprintf("n=%d p50=%v p95=%v p99=%v max=%v", s.Count, s.P50, s.P95, s.P99, s.Max)
}

func rank(sorted []time.Duration, p int) time.Duration {
	i := (p*len(sorted)+99)/100 - 1
	return sorted[max(i, 0)]
}
```

**Step 4: Chạy test, phải PASS**

Run: `make -s go ARGS="test -race ./tools/poc/internal/latency/"`
Expected: `ok  	github.com/ivannguyendev/chatim/tools/poc/internal/latency`

**Step 5: Commit**

```bash
git add tools/poc/internal/latency
git commit -m "test(poc): latency recorder with nearest-rank percentiles"
```

---

### Task 7: `apps/core/internal/slot` — sở hữu slot mềm trên Redis (R5)

Đây là code production (thiết kế mục 5.1), không phải code PoC. Quyền sở hữu chỉ để định tuyến (gom batch, giữ thứ tự, cache); dữ liệu đúng nhờ `_id` unique của MongoDB, nên kể cả khi hai core cùng tưởng giữ một slot trong chốc lát thì cũng không mất hay trùng tin.

Hành vi cần đạt:
- Mỗi `Step`: heartbeat (`chatim:core:<id>`, TTL 5s) → quét core còn sống → gia hạn lease (`chatim:slot:<n>`, TTL 10s) → nhận hoặc nhả slot để tiến về `ceil(1024 / số core sống)`. Nhận slot theo thứ tự `Score` giảm dần để các core ít tranh nhau; nhả những slot có `Score` thấp nhất.
- Core chết (hết heartbeat) → các core còn lại nhận slot của nó bằng CAS (`claimScript` chỉ ghi khi chủ hiện tại vẫn là chủ đã thấy; chuỗi rỗng = slot trống).
- Redis mất hết dữ liệu → hội tụ lại; hai core chỉ có thể cùng tưởng giữ một slot cho tới khi mọi core chạy thêm một tick.
- `Owns` chỉ trả `true` khi lease còn mới hơn `min(LeaseTTL, HeartbeatTTL) − Tick`, tính từ **đầu** `Step` (thời điểm lấy trước lệnh ghi heartbeat). Lý do: core khác được phép nhận slot ngay khi heartbeat (5s) hết hạn, không phải đợi lease (10s); nếu mốc cắt dựa trên lease hoặc lấy sau round trip Redis, một core mất kết nối Redis sẽ tưởng mình còn giữ slot thêm khoảng 4s sau khi slot đã bị nhận.
- `New` từ chối cấu hình có `LeaseTTL` hoặc `HeartbeatTTL` không lớn hơn `2×Tick`.
- Khi claim/release thành công một phần rồi lỗi (pipeline đứt giữa chừng), các slot đã nhận vẫn được ghi nhận và `Step` vẫn báo `chatim:slots:changed`.
- `ReleaseAll` (tắt máy): gỡ slot khỏi định tuyến trước, gọi `BeforeRelease` cho từng slot để drain, nhả lease, xoá heartbeat, báo `chatim:slots:changed`.
- Các script Lua chạy trên **một Redis primary (Sentinel), không chạy trên Redis Cluster**, vì `renewScript` chạm nhiều key trong một lần gọi.

Test dùng miniredis để điều khiển thời gian (`FastForward`) và giả lập Redis mất dữ liệu (`FlushAll`) — chính là tình huống R5. `TestReleaseAllHandsSlotsBack` đặt lại bộ đếm drain ngay trước `ReleaseAll`, vì các slot core-a nhả lúc core-b tham gia cũng gọi hook, và kiểm tra lease đã bị xoá thật trong Redis. `ownership_rules_test.go` kiểm tra mốc cắt của `Owns` bằng đồng hồ inject (`m.now`), dùng một `redis.Hook` làm đồng hồ nhảy 2s mỗi lệnh để giả lập Redis chậm, và kiểm tra slot bị nhả là những slot có `Score` thấp nhất.

**Files:**
- Create: `apps/core/internal/slot/manager.go`, `apps/core/internal/slot/leases.go`
- Test: `apps/core/internal/slot/manager_test.go`, `apps/core/internal/slot/ownership_rules_test.go`

**Step 1: Thêm dependency**

Run: `make -s go ARGS="get github.com/redis/go-redis/v9@v9.22.0 github.com/alicebob/miniredis/v2@v2.39.0"`
Expected: `go: added github.com/redis/go-redis/v9 v9.22.0` và `go: added github.com/alicebob/miniredis/v2 v2.39.0`

**Step 2: Viết test**

`apps/core/internal/slot/manager_test.go`:

```go
package slot

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

func TestSingleCoreClaimsAllSlots(t *testing.T) {
	mr, rdb := newRedis(t)
	a := newManager(t, rdb, "core-a", nil)
	stepAll(t, a)
	if n := len(a.Owned()); n != slotmap.Count {
		t.Fatalf("core-a owns %d slots, want %d", n, slotmap.Count)
	}
	assertPartition(t, mr, a)
	if !a.Owns(0) {
		t.Fatal("Owns(0) = false for a freshly claimed slot")
	}
}

func TestCoresConvergeToFairShares(t *testing.T) {
	mr, rdb := newRedis(t)
	ms := []*Manager{newManager(t, rdb, "core-a", nil), newManager(t, rdb, "core-b", nil), newManager(t, rdb, "core-c", nil)}
	for i := 0; i < 4; i++ {
		stepAll(t, ms...)
	}
	assertPartition(t, mr, ms...)
	assertShares(t, 340, 342, ms...)
}

func TestDeadCoreSlotsAreTakenOver(t *testing.T) {
	mr, rdb := newRedis(t)
	a, b, c := newManager(t, rdb, "core-a", nil), newManager(t, rdb, "core-b", nil), newManager(t, rdb, "core-c", nil)
	for i := 0; i < 4; i++ {
		stepAll(t, a, b, c)
	}

	for i := 0; i < 10; i++ {
		mr.FastForward(time.Second)
		stepAll(t, a, b)
	}
	if mr.Exists(slotmap.CoreKey("core-c")) {
		t.Fatal("core-c heartbeat should have expired")
	}
	assertPartition(t, mr, a, b)
	assertShares(t, 512, 512, a, b)
}

func TestRedisDataLossReconverges(t *testing.T) {
	mr, rdb := newRedis(t)
	ms := []*Manager{newManager(t, rdb, "core-a", nil), newManager(t, rdb, "core-b", nil), newManager(t, rdb, "core-c", nil)}
	for i := 0; i < 4; i++ {
		stepAll(t, ms...)
	}
	mr.FlushAll()
	for round := 0; round < 5; round++ {
		stepAll(t, ms...)
		assertDisjoint(t, ms...)
	}
	assertPartition(t, mr, ms...)
	assertShares(t, 340, 342, ms...)
}

func TestReleaseAllHandsSlotsBack(t *testing.T) {
	mr, rdb := newRedis(t)
	drained := 0
	a := newManager(t, rdb, "core-a", func(context.Context, uint16) { drained++ })
	b := newManager(t, rdb, "core-b", nil)
	for i := 0; i < 3; i++ {
		stepAll(t, a, b)
	}
	held := a.Owned()
	drained = 0
	if err := a.ReleaseAll(context.Background()); err != nil {
		t.Fatalf("ReleaseAll: %v", err)
	}
	if drained != len(held) || len(a.Owned()) != 0 {
		t.Fatalf("drained %d of %d slots, still owns %d", drained, len(held), len(a.Owned()))
	}
	for _, s := range held {
		if mr.Exists(slotmap.SlotKey(s)) {
			t.Fatalf("slot %d lease still in redis after ReleaseAll", s)
		}
	}
	if mr.Exists(slotmap.CoreKey("core-a")) {
		t.Fatal("ReleaseAll must remove the heartbeat")
	}
	stepAll(t, b)
	assertPartition(t, mr, b)
}

func TestLostLeaseStopsOwnership(t *testing.T) {
	mr, rdb := newRedis(t)
	a := newManager(t, rdb, "core-a", nil)
	stepAll(t, a)
	mr.FlushAll()
	b := newManager(t, rdb, "core-b", nil)
	stepAll(t, b)
	stepAll(t, a)
	if a.Owns(0) || len(a.Owned()) != 0 {
		t.Fatalf("core-a still owns %d slots after losing its leases", len(a.Owned()))
	}
}

func TestNewValidatesConfig(t *testing.T) {
	_, rdb := newRedis(t)
	bad := []Config{
		{CoreID: ""},
		{CoreID: "core*"},
		{CoreID: "core-a", Tick: time.Second, LeaseTTL: 2 * time.Second},
		{CoreID: "core-a", Tick: time.Second, HeartbeatTTL: 2 * time.Second, LeaseTTL: 10 * time.Second},
	}
	for _, cfg := range bad {
		if _, err := New(rdb, cfg, nil); err == nil {
			t.Errorf("New(%+v) succeeded, want error", cfg)
		}
	}
}

func newRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

func newManager(t *testing.T, rdb *redis.Client, id string, hook func(context.Context, uint16)) *Manager {
	t.Helper()
	m, err := New(rdb, Config{CoreID: id, Addr: id + ":9000", BeforeRelease: hook}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New(%s): %v", id, err)
	}
	return m
}

func stepAll(t *testing.T, ms ...*Manager) {
	t.Helper()
	for _, m := range ms {
		if err := m.Step(context.Background()); err != nil {
			t.Fatalf("%s Step: %v", m.cfg.CoreID, err)
		}
	}
}

func assertPartition(t *testing.T, mr *miniredis.Miniredis, ms ...*Manager) {
	t.Helper()
	owner := assertDisjoint(t, ms...)
	for s := uint16(0); s < slotmap.Count; s++ {
		inRedis, _ := mr.Get(slotmap.SlotKey(s))
		if owner[s] == "" || inRedis != owner[s] {
			t.Fatalf("slot %d: local owner %q, redis owner %q", s, owner[s], inRedis)
		}
	}
}

func assertDisjoint(t *testing.T, ms ...*Manager) map[uint16]string {
	t.Helper()
	owner := map[uint16]string{}
	for _, m := range ms {
		for _, s := range m.Owned() {
			if prev, dup := owner[s]; dup {
				t.Fatalf("slot %d owned by both %s and %s", s, prev, m.cfg.CoreID)
			}
			owner[s] = m.cfg.CoreID
		}
	}
	return owner
}

func assertShares(t *testing.T, lo, hi int, ms ...*Manager) {
	t.Helper()
	for _, m := range ms {
		if n := len(m.Owned()); n < lo || n > hi {
			t.Fatalf("%s owns %d slots, want %d..%d", m.cfg.CoreID, n, lo, hi)
		}
	}
}
```

`apps/core/internal/slot/ownership_rules_test.go`:

```go
package slot

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

func TestOwnsExpiresBeforeOtherCoresMayTakeOver(t *testing.T) {
	_, rdb := newRedis(t)
	a := newManager(t, rdb, "core-a", nil)
	t0 := time.Unix(1_700_000_000, 0)
	a.now = func() time.Time { return t0 }
	stepAll(t, a)
	cutoff := t0.Add(a.cfg.HeartbeatTTL - a.cfg.Tick)
	a.now = func() time.Time { return cutoff.Add(-100 * time.Millisecond) }
	if !a.Owns(0) {
		t.Fatal("Owns(0) = false while no other core may take the slot over yet")
	}
	a.now = func() time.Time { return cutoff.Add(100 * time.Millisecond) }
	if a.Owns(0) {
		t.Fatal("Owns(0) = true after other cores may already have taken the slot over")
	}
}

func TestOwnershipStampedAtStepStart(t *testing.T) {
	_, rdb := newRedis(t)
	a := newManager(t, rdb, "core-a", nil)
	t0 := time.Unix(1_700_000_000, 0)
	clock := t0
	a.now = func() time.Time { return clock }
	rdb.AddHook(slowRoundTrips{clock: &clock, delay: 2 * time.Second})
	stepAll(t, a)
	clock = t0.Add(a.cfg.HeartbeatTTL - a.cfg.Tick + 100*time.Millisecond)
	if a.Owns(0) {
		t.Fatal("Owns(0) = true past the heartbeat window measured from step start")
	}
}

type slowRoundTrips struct {
	clock *time.Time
	delay time.Duration
}

func (h slowRoundTrips) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h slowRoundTrips) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		*h.clock = h.clock.Add(h.delay)
		return next(ctx, cmd)
	}
}

func (h slowRoundTrips) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		*h.clock = h.clock.Add(h.delay)
		return next(ctx, cmds)
	}
}

func TestReleaseDropsLowestScoreSlots(t *testing.T) {
	_, rdb := newRedis(t)
	a, b := newManager(t, rdb, "core-a", nil), newManager(t, rdb, "core-b", nil)
	stepAll(t, a)
	stepAll(t, b)
	if n := len(b.Owned()); n != 0 {
		t.Fatalf("core-b claimed %d slots held by a live core", n)
	}
	stepAll(t, a)
	kept := a.Owned()
	if len(kept) != slotmap.Count/2 {
		t.Fatalf("core-a owns %d slots, want %d", len(kept), slotmap.Count/2)
	}
	var keptScores, releasedScores []uint64
	for s := uint16(0); s < slotmap.Count; s++ {
		score := slotmap.Score(s, "core-a")
		if slices.Contains(kept, s) {
			keptScores = append(keptScores, score)
		} else {
			releasedScores = append(releasedScores, score)
		}
	}
	if lo, hi := slices.Min(keptScores), slices.Max(releasedScores); lo <= hi {
		t.Fatalf("lowest kept score %d <= highest released score %d", lo, hi)
	}
}
```

**Step 3: Chạy test, phải FAIL**

Run: `make -s go ARGS="test ./apps/core/internal/slot/"`
Expected: FAIL, `undefined: Manager` / `undefined: New`

**Step 4: Viết `apps/core/internal/slot/manager.go`**

`apps/core/internal/slot/manager.go`:

```go
package slot

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

type Config struct {
	CoreID       string
	Addr         string
	Tick         time.Duration
	HeartbeatTTL time.Duration
	LeaseTTL     time.Duration

	BeforeRelease func(ctx context.Context, slot uint16)
}

type Manager struct {
	cfg   Config
	rdb   redis.UniversalClient
	log   *slog.Logger
	mu    sync.RWMutex
	owned map[uint16]time.Time
	now   func() time.Time
}

func New(rdb redis.UniversalClient, cfg Config, log *slog.Logger) (*Manager, error) {
	if cfg.CoreID == "" || strings.ContainsAny(cfg.CoreID, "*?[]\\ ") {
		return nil, errors.New("slot: CoreID must be non-empty and free of glob characters and spaces")
	}
	cfg.Tick = cmp.Or(cfg.Tick, time.Second)
	cfg.HeartbeatTTL = cmp.Or(cfg.HeartbeatTTL, 5*time.Second)
	cfg.LeaseTTL = cmp.Or(cfg.LeaseTTL, 10*time.Second)
	if cfg.LeaseTTL <= 2*cfg.Tick || cfg.HeartbeatTTL <= 2*cfg.Tick {
		return nil, errors.New("slot: LeaseTTL and HeartbeatTTL must each exceed 2×Tick")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Manager{cfg: cfg, rdb: rdb, log: log.With("core", cfg.CoreID), owned: map[uint16]time.Time{}, now: time.Now}, nil
}

func (m *Manager) Owns(slot uint16) bool {
	m.mu.RLock()
	at, ok := m.owned[slot]
	m.mu.RUnlock()
	return ok && m.now().Sub(at) < min(m.cfg.LeaseTTL, m.cfg.HeartbeatTTL)-m.cfg.Tick
}

func (m *Manager) Owned() []uint16 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]uint16, 0, len(m.owned))
	for s := range m.owned {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

func (m *Manager) Run(ctx context.Context) error {
	t := time.NewTicker(m.cfg.Tick)
	defer t.Stop()
	for {
		if err := m.Step(ctx); err != nil && ctx.Err() == nil {
			m.log.Warn("slot reconcile failed", "err", err)
		}
		select {
		case <-ctx.Done():
			rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return m.ReleaseAll(rctx)
		case <-t.C:
		}
	}
}

func (m *Manager) Step(ctx context.Context) error {
	stamp := m.now()
	if err := m.rdb.Set(ctx, slotmap.CoreKey(m.cfg.CoreID), m.cfg.Addr, m.cfg.HeartbeatTTL).Err(); err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	alive, err := m.aliveCores(ctx)
	if err != nil {
		return err
	}
	lost, err := m.renew(ctx, stamp)
	if err != nil {
		return err
	}
	target := (slotmap.Count + len(alive) - 1) / len(alive)
	moved := false
	switch n := len(m.Owned()); {
	case n > target:
		moved, err = true, m.release(ctx, n-target)
	case n < target:
		moved, err = m.claim(ctx, alive, target-n, stamp)
	}
	if lost || moved {
		err = errors.Join(err, m.rdb.Publish(ctx, slotmap.ChangedChannel, m.cfg.CoreID).Err())
	}
	return err
}
```

**Step 5: Viết `apps/core/internal/slot/leases.go`**

`apps/core/internal/slot/leases.go`:

```go
package slot

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ivannguyendev/chatim/pkg/slotmap"
)

const renewScript = `
local res = {}
for i, k in ipairs(KEYS) do
  if redis.call('GET', k) == ARGV[1] then
    redis.call('PEXPIRE', k, ARGV[2])
    res[i] = 1
  else
    res[i] = 0
  end
end
return res`

const claimScript = `
local cur = redis.call('GET', KEYS[1])
if (cur == false and ARGV[1] == '') or cur == ARGV[1] then
  redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
  return 1
end
return 0`

const releaseScript = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0`

func (m *Manager) aliveCores(ctx context.Context) (map[string]bool, error) {
	alive := map[string]bool{m.cfg.CoreID: true}
	iter := m.rdb.Scan(ctx, 0, slotmap.CorePattern, 256).Iterator()
	for iter.Next(ctx) {
		if id, ok := slotmap.CoreIDFromKey(iter.Val()); ok {
			alive[id] = true
		}
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("scan cores: %w", err)
	}
	return alive, nil
}

func (m *Manager) renew(ctx context.Context, stamp time.Time) (lost bool, err error) {
	slots := m.Owned()
	if len(slots) == 0 {
		return false, nil
	}
	keys := make([]string, len(slots))
	for i, s := range slots {
		keys[i] = slotmap.SlotKey(s)
	}
	kept, err := m.rdb.Eval(ctx, renewScript, keys, m.cfg.CoreID, m.cfg.LeaseTTL.Milliseconds()).Int64Slice()
	if err != nil {
		return false, fmt.Errorf("renew: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range slots {
		if kept[i] == 1 {
			m.owned[s] = stamp
			continue
		}
		delete(m.owned, s)
		lost = true
	}
	return lost, nil
}

func (m *Manager) claim(ctx context.Context, alive map[string]bool, need int, stamp time.Time) (bool, error) {
	keys := make([]string, slotmap.Count)
	for s := range keys {
		keys[s] = slotmap.SlotKey(uint16(s))
	}
	owners, err := m.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return false, fmt.Errorf("read owners: %w", err)
	}
	type candidate struct {
		slot   uint16
		expect string
		score  uint64
	}
	var cands []candidate
	for s, v := range owners {
		owner, _ := v.(string)
		if m.Owns(uint16(s)) || (owner != "" && owner != m.cfg.CoreID && alive[owner]) {
			continue
		}
		cands = append(cands, candidate{uint16(s), owner, slotmap.Score(uint16(s), m.cfg.CoreID)})
	}
	slices.SortFunc(cands, func(a, b candidate) int { return cmp.Compare(b.score, a.score) })
	cands = cands[:min(need, len(cands))]
	if len(cands) == 0 {
		return false, nil
	}
	pipe := m.rdb.Pipeline()
	cmds := make([]*redis.Cmd, len(cands))
	for i, c := range cands {
		cmds[i] = pipe.Eval(ctx, claimScript, []string{keys[c.slot]}, c.expect, m.cfg.CoreID, m.cfg.LeaseTTL.Milliseconds())
	}
	_, execErr := pipe.Exec(ctx)
	claimed := false
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, c := range cands {
		if n, err := cmds[i].Int64(); err == nil && n == 1 {
			m.owned[c.slot] = stamp
			claimed = true
		}
	}
	if execErr != nil {
		return claimed, fmt.Errorf("claim: %w", execErr)
	}
	return claimed, nil
}

func (m *Manager) release(ctx context.Context, count int) error {
	slots := m.Owned()
	slices.SortFunc(slots, func(a, b uint16) int {
		return cmp.Compare(slotmap.Score(a, m.cfg.CoreID), slotmap.Score(b, m.cfg.CoreID))
	})
	return m.releaseSlots(ctx, slots[:count])
}

func (m *Manager) ReleaseAll(ctx context.Context) error {
	if slots := m.Owned(); len(slots) > 0 {
		if err := m.releaseSlots(ctx, slots); err != nil {
			return err
		}
	}
	if err := m.rdb.Del(ctx, slotmap.CoreKey(m.cfg.CoreID)).Err(); err != nil {
		return fmt.Errorf("remove heartbeat: %w", err)
	}
	return m.rdb.Publish(ctx, slotmap.ChangedChannel, m.cfg.CoreID).Err()
}

func (m *Manager) releaseSlots(ctx context.Context, slots []uint16) error {
	m.mu.Lock()
	for _, s := range slots {
		delete(m.owned, s)
	}
	m.mu.Unlock()
	if m.cfg.BeforeRelease != nil {
		for _, s := range slots {
			m.cfg.BeforeRelease(ctx, s)
		}
	}
	pipe := m.rdb.Pipeline()
	for _, s := range slots {
		pipe.Eval(ctx, releaseScript, []string{slotmap.SlotKey(s)}, m.cfg.CoreID)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("release: %w", err)
	}
	return nil
}
```

**Step 6: Chạy test, phải PASS (nhiều lần, có `-race`)**

Run: `make -s tidy && make -s go ARGS="test -race -count=5 ./apps/core/internal/slot/"`
Expected: `ok  	github.com/ivannguyendev/chatim/apps/core/internal/slot` (khoảng 10s mỗi lượt với `-race` trên máy dev Intel; mỗi lần claim gửi tới 1024 lệnh Lua qua miniredis)

**Step 7: Commit**

```bash
git add go.mod go.sum apps/core/internal/slot
git commit -m "feat(core): soft slot ownership on redis with heartbeat and leases"
```

---

### Task 8: `tools/poc/mongobench` — R1 (dung lượng, đọc) và R2 (ghi)

Ba lệnh con:
- `seed`: tạo collection clustered trên `_id` + nén `zstd`, nạp tin (ghi `w:1` cho nhanh), ghi danh sách room vào `rooms.txt`, in dung lượng và dự phóng cho 5 và 20 tỷ tin. `-text-file` lấy nội dung tin thật (mỗi dòng một tin); không có thì dùng văn bản giả, nén tốt bất thường nên chỉ để kiểm tra công cụ.
- `read`: đo trang `oldest` / `random` / `latest` của timeline chính, in plan để chắc chắn là `CLUSTERED_IXSCAN`. Trang `random` kết thúc ngay trước một seq ngẫu nhiên trong `2..per-room+1` nên không bao giờ rỗng. Lỗi truy vấn được đếm vào `errors=` (trừ lỗi do hết thời gian chạy), không bị bỏ qua trong im lặng.
- `write`: mô phỏng flusher của core (cửa sổ 2ms hoặc 256 doc, `insertMany(ordered:false)` với `w:majority`, nhiều room trong một batch). Bộ sinh giữ seq liên tục theo từng room như actor. Dùng room mới sinh nên không đụng dữ liệu đã seed. `arrival->commit` là thời gian người gửi chờ ack. Batch `insertMany` lỗi **không** được tính vào throughput hay độ trễ; `errors=` đếm số document ghi lỗi.

`MONGO_URI` do `make poc` truyền vào: `mongodb://<user>:<pass>@chatim-mongodb:27017/?replicaSet=rs0&authSource=admin`.

**Files:**
- Create: `tools/poc/mongobench/main.go`, `message.go`, `seed.go`, `stats.go`, `read.go`, `write.go`

**Step 1: Thêm dependency**

Run: `make -s go ARGS="get go.mongodb.org/mongo-driver/v2@v2.9.1"`
Expected: `go: added go.mongodb.org/mongo-driver/v2 v2.9.1`

**Step 2: Tạo `tools/poc/mongobench/main.go`**

`tools/poc/mongobench/main.go`:

```go
package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	run := map[string]func(context.Context, []string) error{"seed": runSeed, "read": runRead, "write": runWrite}[os.Args[1]]
	if run == nil {
		usage()
	}
	if err := run(ctx, os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "mongobench:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: mongobench seed|read|write [flags]  (use -h after a subcommand)")
	os.Exit(2)
}

type target struct{ uri, db, coll string }

func (t *target) register(fs *flag.FlagSet) {
	fs.StringVar(&t.uri, "uri", cmp.Or(os.Getenv("MONGO_URI"), "mongodb://chatim-mongodb:27017/?replicaSet=rs0&authSource=admin"), "MongoDB URI (env MONGO_URI)")
	fs.StringVar(&t.db, "db", "chatim_poc", "database")
	fs.StringVar(&t.coll, "coll", "messages", "collection")
}

func (t *target) connect(ctx context.Context, wc *writeconcern.WriteConcern) (*mongo.Client, *mongo.Collection, error) {
	client, err := mongo.Connect(options.Client().ApplyURI(t.uri))
	if err != nil {
		return nil, nil, fmt.Errorf("connect: %w", err)
	}
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		_ = client.Disconnect(ctx)
		return nil, nil, fmt.Errorf("ping: %w", err)
	}
	coll := client.Database(t.db).Collection(t.coll, options.Collection().SetWriteConcern(wc))
	return client, coll, nil
}
```

**Step 3: Tạo `tools/poc/mongobench/message.go`**

`tools/poc/mongobench/message.go`:

```go
package main

import (
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"time"

	"github.com/ivannguyendev/chatim/pkg/keys"
)

type message struct {
	ID   []byte    `bson:"_id"`
	From string    `bson:"f"`
	Pts  int64     `bson:"p"`
	Kind int32     `bson:"kind"`
	Text string    `bson:"text"`
	TS   time.Time `bson:"ts"`
}

var words = strings.Fields(`xin chào shop ạ mình muốn hỏi đơn hàng này giao khi nào vậy
cảm ơn bạn nhiều nhé sản phẩm kem chống nắng son dưỡng môi sữa rửa mặt còn hàng không
giá bao nhiêu có khuyến mãi freeship đổi trả được không địa chỉ số điện thoại mã giảm giá
hôm nay ngày mai buổi sáng chiều tối mình đã chuyển khoản rồi kiểm tra giúp mình với
okay dạ vâng được ạ chị em anh bên mình sẽ liên hệ lại sớm nhất có thể`)

func newMessage(room, seq uint64, rng *rand.Rand, texts []string) message {
	text := ""
	if len(texts) > 0 {
		text = texts[rng.IntN(len(texts))]
	} else {
		parts := make([]string, 20+rng.IntN(40))
		for i := range parts {
			parts[i] = words[rng.IntN(len(words))]
		}
		text = strings.Join(parts, " ")
	}
	return message{
		ID:   keys.Msg(room, 0, seq),
		From: "user-" + string(rune('a'+rng.IntN(26))),
		Pts:  int64(seq),
		Kind: 1,
		Text: text,
		TS:   time.Now(),
	}
}

func loadTexts(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read text file: %w", err)
	}
	var texts []string
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			texts = append(texts, line)
		}
	}
	return texts, nil
}
```

**Step 4: Tạo `tools/poc/mongobench/seed.go`**

`tools/poc/mongobench/seed.go`:

```go
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"github.com/ivannguyendev/chatim/pkg/ids"
)

func runSeed(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("seed", flag.ExitOnError)
	var t target
	t.register(fs)
	rooms := fs.Int("rooms", 10_000, "number of rooms")
	perRoom := fs.Int("per-room", 1000, "messages per room")
	workers := fs.Int("workers", 8, "parallel insert workers")
	batch := fs.Int("batch", 1000, "documents per insertMany")
	reset := fs.Bool("reset", false, "drop the collection first")
	roomsFile := fs.String("rooms-file", "rooms.txt", "file that receives the seeded room ids")
	textFile := fs.String("text-file", "", "optional file of real message texts, one per line (needed for credible storage numbers)")
	_ = fs.Parse(args)
	texts, err := loadTexts(*textFile)
	if err != nil {
		return err
	}

	client, coll, err := t.connect(ctx, writeconcern.W1())
	if err != nil {
		return err
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	if *reset {
		if err := coll.Drop(ctx); err != nil {
			return fmt.Errorf("drop: %w", err)
		}
	}
	if err := createClustered(ctx, coll.Database(), t.coll); err != nil {
		return err
	}
	roomIDs := make([]uint64, *rooms)
	for i := range roomIDs {
		roomIDs[i] = ids.NewRoomID()
	}
	if err := writeRooms(*roomsFile, roomIDs, *perRoom); err != nil {
		return err
	}

	start := time.Now()
	var inserted atomic.Int64
	stopProgress := progress(&inserted, int64(*rooms)*int64(*perRoom))
	jobs := make(chan uint64)
	errs := make(chan error, *workers)
	var wg sync.WaitGroup
	for w := 0; w < *workers; w++ {
		wg.Go(func() {
			rng := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
			for room := range jobs {
				for from := 1; from <= *perRoom; from += *batch {
					docs := make([]any, 0, *batch)
					for seq := from; seq < from+*batch && seq <= *perRoom; seq++ {
						docs = append(docs, newMessage(room, uint64(seq), rng, texts))
					}
					if _, err := coll.InsertMany(ctx, docs, options.InsertMany().SetOrdered(false)); err != nil {
						errs <- fmt.Errorf("insert room %d: %w", room, err)
						return
					}
					inserted.Add(int64(len(docs)))
				}
			}
		})
	}
feed:
	for _, room := range roomIDs {
		select {
		case jobs <- room:
		case err := <-errs:
			close(jobs)
			wg.Wait()
			return err
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	stopProgress()
	select {
	case err := <-errs:
		return err
	default:
	}
	fmt.Printf("seeded %d messages in %v\n", inserted.Load(), time.Since(start).Round(time.Second))
	return printStorage(ctx, coll)
}

func createClustered(ctx context.Context, db *mongo.Database, name string) error {
	names, err := db.ListCollectionNames(ctx, bson.D{{Key: "name", Value: name}})
	if err != nil || len(names) > 0 {
		return err
	}
	opts := options.CreateCollection().
		SetClusteredIndex(bson.D{{Key: "key", Value: bson.D{{Key: "_id", Value: 1}}}, {Key: "unique", Value: true}}).
		SetStorageEngine(bson.D{{Key: "wiredTiger", Value: bson.D{{Key: "configString", Value: "block_compressor=zstd"}}}})
	return db.CreateCollection(ctx, name, opts)
}

func writeRooms(path string, roomIDs []uint64, perRoom int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, id := range roomIDs {
		fmt.Fprintf(w, "%d %d\n", id, perRoom)
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func progress(done *atomic.Int64, total int64) (stop func()) {
	t := time.NewTicker(5 * time.Second)
	quit := make(chan struct{})
	go func() {
		for {
			select {
			case <-t.C:
				fmt.Printf("  %d / %d messages\n", done.Load(), total)
			case <-quit:
				return
			}
		}
	}()
	return func() { t.Stop(); close(quit) }
}
```

**Step 5: Tạo `tools/poc/mongobench/stats.go`**

`tools/poc/mongobench/stats.go`:

```go
package main

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func printStorage(ctx context.Context, coll *mongo.Collection) error {
	cur, err := coll.Aggregate(ctx, mongo.Pipeline{{{Key: "$collStats", Value: bson.D{{Key: "storageStats", Value: bson.D{}}}}}})
	if err != nil {
		return fmt.Errorf("collStats: %w", err)
	}
	var out []struct {
		Storage struct {
			Count       int64 `bson:"count"`
			Size        int64 `bson:"size"`
			StorageSize int64 `bson:"storageSize"`
		} `bson:"storageStats"`
	}
	if err := cur.All(ctx, &out); err != nil {
		return fmt.Errorf("decode collStats: %w", err)
	}
	if len(out) == 0 {
		return fmt.Errorf("collStats returned no rows")
	}
	s := out[0].Storage
	if s.Count == 0 || s.StorageSize == 0 {
		fmt.Println("collection is empty")
		return nil
	}
	perDoc := float64(s.StorageSize) / float64(s.Count)
	fmt.Printf("docs=%d logical=%.1fMB on-disk=%.1fMB ratio=%.2fx logical/doc=%.0fB disk/doc=%.0fB\n",
		s.Count, mb(s.Size), mb(s.StorageSize), float64(s.Size)/float64(s.StorageSize), float64(s.Size)/float64(s.Count), perDoc)
	for _, n := range []float64{5e9, 20e9} {
		fmt.Printf("projected on-disk for %.0f billion messages: %.2f TB (before oplog, indexes, replicas)\n", n/1e9, n*perDoc/1e12)
	}
	return nil
}

func mb(b int64) float64 { return float64(b) / (1 << 20) }

type planNode struct {
	Stage      string    `bson:"stage"`
	InputStage *planNode `bson:"inputStage"`
	QueryPlan  *planNode `bson:"queryPlan"`
}

func (n *planNode) stages() []string {
	if n == nil {
		return nil
	}
	var out []string
	if n.Stage != "" {
		out = append(out, n.Stage)
	}
	out = append(out, n.QueryPlan.stages()...)
	return append(out, n.InputStage.stages()...)
}

func explain(ctx context.Context, coll *mongo.Collection, filter, sort bson.D, limit int64) ([]string, error) {
	var res struct {
		QueryPlanner struct {
			WinningPlan planNode `bson:"winningPlan"`
		} `bson:"queryPlanner"`
	}
	cmd := bson.D{
		{Key: "explain", Value: bson.D{{Key: "find", Value: coll.Name()}, {Key: "filter", Value: filter}, {Key: "sort", Value: sort}, {Key: "limit", Value: limit}}},
		{Key: "verbosity", Value: "queryPlanner"},
	}
	if err := coll.Database().RunCommand(ctx, cmd).Decode(&res); err != nil {
		return nil, fmt.Errorf("explain: %w", err)
	}
	return res.QueryPlanner.WinningPlan.stages(), nil
}
```

**Step 6: Tạo `tools/poc/mongobench/read.go`**

`tools/poc/mongobench/read.go`:

```go
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"github.com/ivannguyendev/chatim/pkg/keys"
	"github.com/ivannguyendev/chatim/tools/poc/internal/latency"
)

type seededRoom struct {
	id      uint64
	perRoom uint64
}

func runRead(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("read", flag.ExitOnError)
	var t target
	t.register(fs)
	mode := fs.String("mode", "oldest", "page to read: oldest | random | latest")
	roomsFile := fs.String("rooms-file", "rooms.txt", "room ids written by seed")
	concurrency := fs.Int("concurrency", 16, "parallel readers")
	duration := fs.Duration("duration", 30*time.Second, "how long to read")
	limit := fs.Int64("limit", 50, "messages per page")
	_ = fs.Parse(args)
	switch *mode {
	case "oldest", "random", "latest":
	default:
		return fmt.Errorf("unknown -mode %q", *mode)
	}

	rooms, err := readRooms(*roomsFile)
	if err != nil {
		return err
	}
	client, coll, err := t.connect(ctx, writeconcern.W1())
	if err != nil {
		return err
	}
	defer func() { _ = client.Disconnect(context.Background()) }()

	filter, sort := pageQuery(*mode, rooms[0], rand.New(rand.NewPCG(1, 1)))
	stages, err := explain(ctx, coll, filter, sort, *limit)
	if err != nil {
		return err
	}
	fmt.Printf("plan (%s page): %s\n", *mode, strings.Join(stages, " > "))

	var rec latency.Recorder
	var pages, empty atomic.Int64
	var failed atomic.Int64
	runCtx, cancel := context.WithTimeout(ctx, *duration)
	defer cancel()
	var wg sync.WaitGroup
	for w := 0; w < *concurrency; w++ {
		wg.Go(func() {
			rng := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
			for runCtx.Err() == nil {
				filter, sort := pageQuery(*mode, rooms[rng.IntN(len(rooms))], rng)
				start := time.Now()
				n, err := readPage(runCtx, coll, filter, sort, *limit)
				if err != nil {
					if runCtx.Err() == nil {
						failed.Add(1)
					}
					continue
				}
				rec.Add(time.Since(start))
				pages.Add(1)
				if n == 0 {
					empty.Add(1)
				}
			}
		})
	}
	wg.Wait()
	fmt.Printf("mode=%s pages=%d (%.0f/s) empty=%d errors=%d latency: %v\n",
		*mode, pages.Load(), float64(pages.Load())/duration.Seconds(), empty.Load(), failed.Load(), rec.Summary())
	return nil
}

func pageQuery(mode string, r seededRoom, rng *rand.Rand) (filter, sort bson.D) {
	upper, dir := uint64(math.MaxUint64), -1
	switch mode {
	case "oldest":
		dir = 1
	case "random":
		upper = 2 + rng.Uint64N(r.perRoom)
	}
	lo, hi := keys.MsgRange(r.id, 0, 0, upper)
	filter = bson.D{{Key: "_id", Value: bson.D{{Key: "$gte", Value: lo}, {Key: "$lt", Value: hi}}}}
	return filter, bson.D{{Key: "_id", Value: dir}}
}

func readPage(ctx context.Context, coll *mongo.Collection, filter, sort bson.D, limit int64) (int, error) {
	cur, err := coll.Find(ctx, filter, options.Find().SetSort(sort).SetLimit(limit))
	if err != nil {
		return 0, err
	}
	var docs []bson.Raw
	if err := cur.All(ctx, &docs); err != nil {
		return 0, err
	}
	return len(docs), nil
}

func readRooms(path string) ([]seededRoom, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open rooms file (run seed first): %w", err)
	}
	defer f.Close()
	var rooms []seededRoom
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r seededRoom
		if _, err := fmt.Sscan(sc.Text(), &r.id, &r.perRoom); err == nil && r.perRoom > 0 {
			rooms = append(rooms, r)
		}
	}
	if len(rooms) == 0 {
		return nil, errors.New("rooms file has no rooms")
	}
	return rooms, sc.Err()
}
```

**Step 7: Tạo `tools/poc/mongobench/write.go`**

`tools/poc/mongobench/write.go`:

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"

	"github.com/ivannguyendev/chatim/pkg/ids"
	"github.com/ivannguyendev/chatim/tools/poc/internal/latency"
)

type pending struct {
	doc message
	at  time.Time
}

type writeStats struct {
	msgLat, insLat         latency.Recorder
	batches, docs, errored atomic.Int64
}

func runWrite(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("write", flag.ExitOnError)
	var t target
	t.register(fs)
	rate := fs.Int("rate", 10_000, "messages per second")
	duration := fs.Duration("duration", 60*time.Second, "test length")
	rooms := fs.Int("rooms", 5000, "active rooms receiving messages")
	window := fs.Duration("window", 2*time.Millisecond, "flush window")
	maxBatch := fs.Int("max-batch", 256, "flush when a batch reaches this size")
	flushers := fs.Int("flushers", 6, "parallel flushers (≈ cores × flush workers)")
	_ = fs.Parse(args)

	client, coll, err := t.connect(ctx, writeconcern.Majority())
	if err != nil {
		return err
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	if err := createClustered(ctx, coll.Database(), t.coll); err != nil {
		return err
	}
	roomIDs := make([]uint64, *rooms)
	for i := range roomIDs {
		roomIDs[i] = ids.NewRoomID()
	}

	in := make(chan pending, 100_000)
	var st writeStats
	start := time.Now()
	go generate(ctx, in, *rate, *duration, roomIDs)
	var wg sync.WaitGroup
	for i := 0; i < *flushers; i++ {
		wg.Go(func() { flushLoop(ctx, coll, in, *window, *maxBatch, &st) })
	}
	wg.Wait()
	elapsed := time.Since(start)
	fmt.Printf("target=%d/s achieved=%.0f/s docs=%d batches=%d avg-batch=%.1f errors=%d\n",
		*rate, float64(st.docs.Load())/elapsed.Seconds(), st.docs.Load(), st.batches.Load(),
		float64(st.docs.Load())/float64(max(st.batches.Load(), 1)), st.errored.Load())
	fmt.Printf("arrival->commit (what a sender waits for ack): %v\n", st.msgLat.Summary())
	fmt.Printf("insertMany w:majority round trip:              %v\n", st.insLat.Summary())
	return nil
}

func generate(ctx context.Context, out chan<- pending, rate int, d time.Duration, rooms []uint64) {
	defer close(out)
	rng := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	seqs := make(map[uint64]uint64, len(rooms))
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	start, emitted := time.Now(), 0
	for range tick.C {
		elapsed := time.Since(start)
		if elapsed >= d || ctx.Err() != nil {
			return
		}
		for due := int(float64(rate) * elapsed.Seconds()); emitted < due; emitted++ {
			room := rooms[rng.IntN(len(rooms))]
			seqs[room]++
			select {
			case out <- pending{doc: newMessage(room, seqs[room], rng, nil), at: time.Now()}:
			case <-ctx.Done():
				return
			}
		}
	}
}

func flushLoop(ctx context.Context, coll *mongo.Collection, in <-chan pending, window time.Duration, maxBatch int, st *writeStats) {
	batch := make([]pending, 0, maxBatch)
	timer := time.NewTimer(window)
	timer.Stop()
	flush := func() {
		timer.Stop()
		if len(batch) == 0 {
			return
		}
		docs := make([]any, len(batch))
		for i, p := range batch {
			docs[i] = p.doc
		}
		begin := time.Now()
		_, err := coll.InsertMany(ctx, docs, options.InsertMany().SetOrdered(false))
		done := time.Now()
		if err != nil {
			st.errored.Add(int64(len(batch)))
			batch = batch[:0]
			return
		}
		st.insLat.Add(done.Sub(begin))
		for _, p := range batch {
			st.msgLat.Add(done.Sub(p.at))
		}
		st.batches.Add(1)
		st.docs.Add(int64(len(batch)))
		batch = batch[:0]
	}
	for {
		select {
		case p, ok := <-in:
			if !ok {
				flush()
				return
			}
			if len(batch) == 0 {
				timer.Reset(window)
			}
			if batch = append(batch, p); len(batch) >= maxBatch {
				flush()
			}
		case <-timer.C:
			flush()
		case <-ctx.Done():
			return
		}
	}
}
```

**Step 8: Tidy và vet**

Run: `make -s tidy && make -s go ARGS="vet ./tools/poc/mongobench"`
Expected: không lỗi.

**Step 9: Chạy thử (hạ tầng từ Task 2 phải đang chạy)**

```bash
make -s poc TOOL=mongobench ARGS="seed -rooms 200 -per-room 500 -reset"
make -s poc TOOL=mongobench ARGS="read -mode oldest -duration 3s"
make -s poc TOOL=mongobench ARGS="read -mode random -duration 3s"
make -s poc TOOL=mongobench ARGS="write -rate 2000 -duration 5s -rooms 500"
```

Expected (số đo trên máy dev chỉ để tham khảo; lần chạy mẫu trên Docker Desktop cho p99 đọc khoảng 30ms):
- `seed`: `seeded 100000 messages …`, dòng `docs=100000 … ratio=…x`, và 2 dòng `projected on-disk …`.
- `read`: `plan (oldest page): LIMIT > CLUSTERED_IXSCAN` (tương tự cho `random`), `empty=0 errors=0`.
- `write`: `achieved=≈2000/s`, `errors=0`.

Nếu plan **không** phải `CLUSTERED_IXSCAN` thì collection không được tạo dạng clustered: chạy lại `seed` với `-reset`.

**Step 10: Commit**

```bash
git add go.mod go.sum tools/poc/mongobench
git commit -m "test(poc): mongobench for clustered message storage, reads and batched writes"
```

---

### Task 9: `tools/poc/natsbench` — R3 (RePublish và interest subscription)

Kiểm tra 2 điều:
- (a) Stream RePublish `evt.{t}.room.{rid}.{type}` sang `live.{t}.room.{rid}.evt.{type}` bằng `{{wildcard(n)}}`.
- (b) Mỗi gateway giữ một subscription cho mỗi room. Dùng `ChanSubscribe` vì nó không tạo goroutine cho từng subscription — điều bắt buộc khi một gateway giữ hàng trăm nghìn room.

Deadline chỉ dừng vòng lặp publish; lệnh publish đang chạy dùng context cha, nên cuối lượt không có lỗi giả. `NATS_URL` và `NATS_MONITOR_URL` mặc định là `nats://chatim-nats:4222` và `http://chatim-nats:8222`.

Để số đo đáng tin:
- `ChanSubscribe` bỏ tin khi channel đầy (slow consumer) mà không báo lỗi, nên công cụ giữ lại mọi `*nats.Subscription` và in tổng `Dropped()` thành `dropped=`.
- Cờ không hợp lệ (`-subs`, `-conns`, `-rate`, `-publishers`, `-duration` ≤ 0, hoặc rate quá cao để chia nhịp) bị từ chối **trước** khi kết nối, không để panic sau khi đã tốn công tạo 1M subscription.
- Sau khi dừng publish, công cụ chờ theo bước 50ms cho tới khi `received ≥ published` (tối đa 5s) thay vì chờ cố định.
- Dòng tổng kết in `target=` cạnh tốc độ đạt được, để thấy ngay khi publish không theo kịp.

**Files:**
- Create: `tools/poc/natsbench/main.go`, `tools/poc/natsbench/load.go`

**Step 1: Thêm dependency**

Run: `make -s go ARGS="get github.com/nats-io/nats.go@v1.54.0"`
Expected: `go: added github.com/nats-io/nats.go v1.54.0` (thư viện này yêu cầu `go 1.26.0`, khớp với `go.mod`)

**Step 2: Tạo `tools/poc/natsbench/main.go`**

`tools/poc/natsbench/main.go`:

```go
package main

import (
	"cmp"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const stream = "POC_EVT"

type config struct {
	url, monitor     string
	subs, conns      int
	rate, publishers int
	size             int
	duration         time.Duration
}

func main() {
	var c config
	flag.StringVar(&c.url, "url", cmp.Or(os.Getenv("NATS_URL"), "nats://chatim-nats:4222"), "NATS URL (env NATS_URL)")
	flag.StringVar(&c.monitor, "monitor", cmp.Or(os.Getenv("NATS_MONITOR_URL"), "http://chatim-nats:8222"), "NATS monitoring URL (env NATS_MONITOR_URL)")
	flag.IntVar(&c.subs, "subs", 100_000, "room subscriptions across all gateway connections")
	flag.IntVar(&c.conns, "conns", 4, "gateway connections")
	flag.IntVar(&c.rate, "rate", 5000, "events per second")
	flag.IntVar(&c.publishers, "publishers", 8, "parallel publishers (≈ core flushers)")
	flag.IntVar(&c.size, "size", 256, "event payload bytes")
	flag.DurationVar(&c.duration, "duration", 30*time.Second, "publish duration")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, c); err != nil {
		fmt.Fprintln(os.Stderr, "natsbench:", err)
		os.Exit(1)
	}
}

func (c config) interval() time.Duration {
	return time.Duration(float64(time.Second) * float64(c.publishers) / float64(c.rate))
}

func (c config) validate() error {
	if c.subs <= 0 || c.conns <= 0 || c.rate <= 0 || c.publishers <= 0 || c.duration <= 0 {
		return errors.New("invalid flags: -subs, -conns, -rate, -publishers and -duration must be positive")
	}
	if c.interval() <= 0 {
		return errors.New("rate too high for publisher count")
	}
	return nil
}

func run(ctx context.Context, c config) error {
	if err := c.validate(); err != nil {
		return err
	}
	nc, err := nats.Connect(c.url)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer nc.Close()
	js, err := jetstream.New(nc)
	if err != nil {
		return err
	}
	if err := js.DeleteStream(ctx, stream); err != nil && !errors.Is(err, jetstream.ErrStreamNotFound) {
		return fmt.Errorf("delete old stream: %w", err)
	}
	_, err = js.CreateStream(ctx, jetstream.StreamConfig{
		Name:       stream,
		Subjects:   []string{"evt.>"},
		Storage:    jetstream.FileStorage,
		MaxAge:     time.Hour,
		Duplicates: 2 * time.Minute,
		RePublish: &jetstream.RePublish{
			Source:      "evt.*.room.*.*",
			Destination: "live.{{wildcard(1)}}.room.{{wildcard(2)}}.evt.{{wildcard(3)}}",
		},
	})
	if err != nil {
		return fmt.Errorf("create stream: %w", err)
	}
	if err := checkRepublish(ctx, nc, js); err != nil {
		return err
	}
	fmt.Println("republish transform: PASS")
	return load(ctx, c, js)
}

func checkRepublish(ctx context.Context, nc *nats.Conn, js jetstream.JetStream) error {
	sub, err := nc.SubscribeSync("live.t1.room.42.evt.msg_created")
	if err != nil {
		return err
	}
	defer func() { _ = sub.Unsubscribe() }()
	if err := nc.Flush(); err != nil {
		return err
	}
	if _, err := js.Publish(ctx, "evt.t1.room.42.msg_created", stamp(16)); err != nil {
		return fmt.Errorf("publish probe: %w", err)
	}
	if _, err := sub.NextMsg(2 * time.Second); err != nil {
		return fmt.Errorf("republish transform: FAIL (%w)", err)
	}
	return nil
}

func stamp(size int) []byte {
	b := make([]byte, max(size, 8))
	binary.BigEndian.PutUint64(b, uint64(time.Now().UnixNano()))
	return b
}

func serverMem(monitor string) string {
	resp, err := http.Get(monitor + "/varz")
	if err != nil {
		return "unknown (" + err.Error() + ")"
	}
	defer resp.Body.Close()
	var v struct {
		Mem  int64  `json:"mem"`
		Subs uint32 `json:"subscriptions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return "unknown (" + err.Error() + ")"
	}
	return fmt.Sprintf("server mem=%.0fMB subscriptions=%d", float64(v.Mem)/(1<<20), v.Subs)
}
```

**Step 3: Tạo `tools/poc/natsbench/load.go`**

`tools/poc/natsbench/load.go`:

```go
package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/ivannguyendev/chatim/tools/poc/internal/latency"
)

func load(ctx context.Context, c config, js jetstream.JetStream) error {
	conns := make([]*nats.Conn, c.conns)
	inboxes := make([]chan *nats.Msg, c.conns)
	for i := range conns {
		nc, err := nats.Connect(c.url, nats.Name(fmt.Sprintf("gateway-%d", i)))
		if err != nil {
			return fmt.Errorf("gateway connect: %w", err)
		}
		defer nc.Close()
		conns[i], inboxes[i] = nc, make(chan *nats.Msg, 65_536)
	}

	start := time.Now()
	subs := make([]*nats.Subscription, 0, c.subs)
	for room := 0; room < c.subs; room++ {
		i := room % c.conns
		sub, err := conns[i].ChanSubscribe(fmt.Sprintf("live.t1.room.%d.>", room), inboxes[i])
		if err != nil {
			return fmt.Errorf("subscribe room %d: %w", room, err)
		}
		subs = append(subs, sub)
	}
	for _, nc := range conns {
		if err := nc.Flush(); err != nil {
			return err
		}
	}
	fmt.Printf("subscribed %d rooms on %d connections in %v; %s\n", c.subs, c.conns, time.Since(start).Round(time.Millisecond), serverMem(c.monitor))

	var e2e, ack latency.Recorder
	var received, published, failed atomic.Int64
	recvCtx, stopRecv := context.WithCancel(ctx)
	var recvWG sync.WaitGroup
	for _, inbox := range inboxes {
		recvWG.Go(func() {
			for {
				select {
				case m := <-inbox:
					sent := time.Unix(0, int64(binary.BigEndian.Uint64(m.Data)))
					e2e.Add(time.Since(sent))
					received.Add(1)
				case <-recvCtx.Done():
					return
				}
			}
		})
	}

	pubCtx, cancel := context.WithTimeout(ctx, c.duration)
	defer cancel()
	var pubWG sync.WaitGroup
	interval := c.interval()
	for p := 0; p < c.publishers; p++ {
		pubWG.Go(func() {
			rng := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
			tick := time.NewTicker(interval)
			defer tick.Stop()
			for {
				select {
				case <-pubCtx.Done():
					return
				case <-tick.C:
				}
				subj := fmt.Sprintf("evt.t1.room.%d.msg_created", rng.IntN(c.subs))
				begin := time.Now()
				if _, err := js.Publish(ctx, subj, stamp(c.size)); err != nil {
					failed.Add(1)
					continue
				}
				ack.Add(time.Since(begin))
				published.Add(1)
			}
		})
	}
	pubWG.Wait()
	awaitDelivered(ctx, &received, &published)
	stopRecv()
	recvWG.Wait()

	dropped, err := totalDropped(subs)
	if err != nil {
		return err
	}
	fmt.Printf("target=%d/s published=%d (%.0f/s) failed=%d received=%d dropped=%d; %s\n",
		c.rate, published.Load(), float64(published.Load())/c.duration.Seconds(), failed.Load(), received.Load(), dropped, serverMem(c.monitor))
	fmt.Printf("jetstream publish ack: %v\n", ack.Summary())
	fmt.Printf("publish -> gateway:    %v\n", e2e.Summary())
	return nil
}

func awaitDelivered(ctx context.Context, received, published *atomic.Int64) {
	deadline := time.Now().Add(5 * time.Second)
	for received.Load() < published.Load() && time.Now().Before(deadline) && ctx.Err() == nil {
		time.Sleep(50 * time.Millisecond)
	}
}

func totalDropped(subs []*nats.Subscription) (int, error) {
	total := 0
	for _, sub := range subs {
		n, err := sub.Dropped()
		if err != nil {
			return 0, fmt.Errorf("dropped count: %w", err)
		}
		total += n
	}
	return total, nil
}
```

**Step 4: Tidy, vet và chạy thử**

```bash
make -s tidy && make -s go ARGS="vet ./tools/poc/natsbench"
make -s poc TOOL=natsbench ARGS="-subs 20000 -conns 4 -rate 1000 -duration 5s"
```

Expected:
```
republish transform: PASS
subscribed 20000 rooms on 4 connections in …; server mem=…MB subscriptions=20066
target=1000/s published=≈5000 (≈1000/s) failed=0 received=≈5000 dropped=0; …
jetstream publish ack: n=… p50=… p99=…
publish -> gateway:    n=… p50=… p99=…
```
Nếu ra `republish transform: FAIL` thì dừng lại: thiết kế mục 7 phải đổi (core tự publish thêm subject live).

**Step 5: Commit**

```bash
git add go.mod go.sum tools/poc/natsbench
git commit -m "test(poc): natsbench for stream republish and per-room interest subscriptions"
```

---

### Task 10: `tools/poc/wsbench` — R4 (gws: RAM mỗi connection, broadcast)

Server đo RAM mỗi connection (heap + stack chia số connection) **ngay trước** mỗi broadcast — tức trạng thái rảnh, vì trong lúc broadcast gws sinh thêm một goroutine ghi cho mỗi connection — và thời điểm **lần ghi cuối** của một broadcast xong, qua callback của gws nên không phụ thuộc đồng hồ client. Nếu broadcast trước chưa ghi xong khi tới tick sau, server in `overlap: previous broadcast still has N writes pending` (số đo lúc đó bị lẫn backlog). Cờ `-ports` (1–65535), `-every`, `-size`, `-conns`, `-dial-rate`, `-addrs` được kiểm tra trước khi chạy. Client báo độ trễ mỗi 5s bằng `SummaryAndReset`. `ParallelEnabled=false` để giữ thứ tự tin trên mỗi connection. Client mở nhiều connection và đo độ trễ nhận. Một máy client chỉ mở được khoảng 16K connection cho mỗi port server (giới hạn ephemeral port), nên server nghe trên nhiều port. Trong mạng compose, server chạy với tên container `chatim-wsbench-server` (địa chỉ mặc định của client).

Lưu ý gws v1.10.2: `Broadcaster.Broadcast(conn, callback func(error))`; callback có thể là `nil`.

**Files:**
- Create: `tools/poc/wsbench/main.go`, `tools/poc/wsbench/server.go`, `tools/poc/wsbench/client.go`

**Step 1: Thêm dependency**

Run: `make -s go ARGS="get github.com/lxzan/gws@v1.10.2"`
Expected: `go: added github.com/lxzan/gws v1.10.2`

**Step 2: Tạo `tools/poc/wsbench/main.go`**

`tools/poc/wsbench/main.go`:

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	run := map[string]func(context.Context, []string) error{"server": runServer, "client": runClient}[os.Args[1]]
	if run == nil {
		usage()
	}
	if err := run(ctx, os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "wsbench:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: wsbench server|client [flags]  (use -h after a subcommand)")
	os.Exit(2)
}

func splitList(s string) []string {
	parts := strings.Split(s, ",")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

func requirePositive[T int | time.Duration](name string, v T) error {
	if v <= 0 {
		return fmt.Errorf("invalid -%s: %v (must be > 0)", name, v)
	}
	return nil
}
```

**Step 3: Tạo `tools/poc/wsbench/server.go`**

`tools/poc/wsbench/server.go`:

```go
package main

import (
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lxzan/gws"
)

type hub struct {
	gws.BuiltinEventHandler
	mu    sync.RWMutex
	conns map[*gws.Conn]struct{}
}

func (h *hub) OnOpen(c *gws.Conn) {
	h.mu.Lock()
	h.conns[c] = struct{}{}
	h.mu.Unlock()
}

func (h *hub) OnClose(c *gws.Conn, _ error) {
	h.mu.Lock()
	delete(h.conns, c)
	h.mu.Unlock()
}

func (h *hub) OnMessage(_ *gws.Conn, m *gws.Message) { _ = m.Close() }

func (h *hub) snapshot() []*gws.Conn {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*gws.Conn, 0, len(h.conns))
	for c := range h.conns {
		out = append(out, c)
	}
	return out
}

func validateServerFlags(ports []string, every time.Duration, size int) error {
	for _, p := range ports {
		if n, err := strconv.ParseUint(p, 10, 16); err != nil || n == 0 {
			return fmt.Errorf("invalid -ports: %q", p)
		}
	}
	return errors.Join(requirePositive("every", every), requirePositive("size", size))
}

func runServer(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	portList := fs.String("ports", "9001,9002,9003,9004", "comma-separated listen ports")
	every := fs.Duration("every", 5*time.Second, "broadcast interval")
	size := fs.Int("size", 256, "broadcast payload bytes")
	duration := fs.Duration("duration", 0, "exit after this long (0 = until interrupted)")
	_ = fs.Parse(args)
	ports := splitList(*portList)
	if err := validateServerFlags(ports, *every, *size); err != nil {
		return err
	}
	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}

	h := &hub{conns: map[*gws.Conn]struct{}{}}
	up := gws.NewUpgrader(h, &gws.ServerOption{
		ParallelEnabled:   false,
		PermessageDeflate: gws.PermessageDeflate{Enabled: false},
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r)
		if err != nil {
			return
		}
		go c.ReadLoop()
	})
	servers := make([]*http.Server, 0, len(ports))
	for _, p := range ports {
		srv := &http.Server{Addr: ":" + p, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		servers = append(servers, srv)
		go func() {
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				fmt.Println("listen:", err)
			}
		}()
	}
	fmt.Printf("listening on %s, broadcasting every %v\n", strings.Join(ports, ","), *every)

	tick := time.NewTicker(*every)
	defer tick.Stop()
	pending := new(atomic.Int64)
	for {
		select {
		case <-ctx.Done():
			for _, srv := range servers {
				_ = srv.Close()
			}
			return nil
		case <-tick.C:
			if n := pending.Load(); n > 0 {
				fmt.Printf("overlap: previous broadcast still has %d writes pending\n", n)
			}
			pending = broadcast(h.snapshot(), *size)
		}
	}
}

func broadcast(conns []*gws.Conn, size int) *atomic.Int64 {
	printIdleMemory(len(conns))
	payload := make([]byte, max(size, 8))
	start := time.Now()
	binary.BigEndian.PutUint64(payload, uint64(start.UnixNano()))
	pending := new(atomic.Int64)
	pending.Store(int64(len(conns)))
	var failed atomic.Int64
	written := func(err error) {
		if err != nil {
			failed.Add(1)
		}
		if pending.Add(-1) == 0 {
			fmt.Printf("  last write done %v after broadcast start (write errors=%d)\n", time.Since(start), failed.Load())
		}
	}
	b := gws.NewBroadcaster(gws.OpcodeBinary, payload)
	enqueued := 0
	for _, c := range conns {
		if err := b.Broadcast(c, written); err != nil {
			written(err)
			continue
		}
		enqueued++
	}
	_ = b.Close()
	fmt.Printf("  enqueued %d frames in %v\n", enqueued, time.Since(start))
	return pending
}

func printIdleMemory(conns int) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	perConn := 0.0
	if conns > 0 {
		perConn = float64(ms.HeapInuse+ms.StackInuse) / float64(conns) / 1024
	}
	fmt.Printf("conns=%d goroutines=%d heap=%.0fMB stack=%.0fMB sys=%.0fMB per-conn=%.1fKB\n",
		conns, runtime.NumGoroutine(), mbytes(ms.HeapInuse), mbytes(ms.StackInuse), mbytes(ms.Sys), perConn)
}

func mbytes(b uint64) float64 { return float64(b) / (1 << 20) }
```

**Step 4: Tạo `tools/poc/wsbench/client.go`**

`tools/poc/wsbench/client.go`:

```go
package main

import (
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lxzan/gws"

	"github.com/ivannguyendev/chatim/tools/poc/internal/latency"
)

type receiver struct {
	gws.BuiltinEventHandler
	lat *latency.Recorder
}

func (r *receiver) OnMessage(_ *gws.Conn, m *gws.Message) {
	defer func() { _ = m.Close() }()
	if b := m.Bytes(); len(b) >= 8 {
		r.lat.Add(time.Since(time.Unix(0, int64(binary.BigEndian.Uint64(b)))))
	}
}

func validateClientFlags(addrs string, urls []string, conns, dialRate int) error {
	if slices.Contains(urls, "") {
		return fmt.Errorf("invalid -addrs: %q", addrs)
	}
	return errors.Join(requirePositive("conns", conns), requirePositive("dial-rate", dialRate))
}

func runClient(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("client", flag.ExitOnError)
	addrs := fs.String("addrs", "ws://chatim-wsbench-server:9001/ws", "comma-separated server URLs")
	total := fs.Int("conns", 10_000, "connections to open")
	dialRate := fs.Int("dial-rate", 2000, "new connections per second")
	duration := fs.Duration("duration", 0, "exit after this long (0 = until interrupted)")
	_ = fs.Parse(args)
	urls := splitList(*addrs)
	if err := validateClientFlags(*addrs, urls, *total, *dialRate); err != nil {
		return err
	}
	if *duration > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}

	rec := &receiver{lat: &latency.Recorder{}}
	var mu sync.Mutex
	var open []*gws.Conn
	var failed atomic.Int64
	go func() {
		pace := time.NewTicker(time.Second / time.Duration(*dialRate))
		defer pace.Stop()
		for i := 0; i < *total && ctx.Err() == nil; i++ {
			<-pace.C
			go func(url string) {
				c, _, err := gws.NewClient(rec, &gws.ClientOption{Addr: url})
				if err != nil {
					failed.Add(1)
					return
				}
				mu.Lock()
				open = append(open, c)
				mu.Unlock()
				c.ReadLoop()
			}(urls[i%len(urls)])
		}
	}()

	report := time.NewTicker(5 * time.Second)
	defer report.Stop()
	for {
		select {
		case <-ctx.Done():
			mu.Lock()
			for _, c := range open {
				_ = c.WriteClose(1000, nil)
			}
			mu.Unlock()
			return nil
		case <-report.C:
			mu.Lock()
			n := len(open)
			mu.Unlock()
			fmt.Printf("connected=%d failed=%d broadcast latency (last 5s): %v\n", n, failed.Load(), rec.lat.SummaryAndReset())
		}
	}
}
```

**Step 5: Tidy, vet và chạy thử (server và client đều là container)**

```bash
make -s tidy && make -s go ARGS="vet ./tools/poc/wsbench"
make -s poc TOOL=wsbench POC_FLAGS="-d --name chatim-wsbench-server" ARGS="server -ports 9001,9002 -every 3s"
make -s poc TOOL=wsbench POC_FLAGS="--ulimit nofile=65536:65536" ARGS="client -addrs ws://chatim-wsbench-server:9001/ws,ws://chatim-wsbench-server:9002/ws -conns 2000 -dial-rate 1000 -duration 15s"
docker logs chatim-wsbench-server
docker stop chatim-wsbench-server
```

Expected:
- Client: `connected=2000 failed=0 broadcast latency (last 5s): n=… p99=…`
- Server log: mỗi lần broadcast có dòng `conns=2000 goroutines=… per-conn=…KB` (trạng thái rảnh), rồi `enqueued 2000 frames in …`, rồi `last write done …`. Lần chạy mẫu: 11–14KB/conn, lần ghi cuối xong sau 6–9ms, `write errors=0`, không có dòng `overlap`.

**Step 6: Commit**

```bash
git add go.mod go.sum tools/poc/wsbench
git commit -m "test(poc): wsbench for gws memory per connection and broadcast fanout"
```

---

### Task 11: Image runtime (`deploy/docker/Dockerfile`)

Một Dockerfile multi-stage cho mọi chương trình Go trong repo, chọn bằng `TARGET` (đường dẫn package main): build `CGO_ENABLED=0` trong `golang:1.26`, chạy trên `gcr.io/distroless/static-debian12:nonroot` (đã có sẵn CA certificates và tzdata). `.dockerignore` loại `.env*` để mọi biến thể file env không lọt vào build context. Dùng ngay cho PoC trên máy prod-like (không cần cài Go); M2+ dùng lại cho `apps/core`, `apps/gateway`. Task này nằm sau Task 7 vì Dockerfile cần `go.sum`.

**Files:**
- Create: `deploy/docker/Dockerfile`, `.dockerignore`

**Step 1: Tạo `deploy/docker/Dockerfile`**

`deploy/docker/Dockerfile`:

```dockerfile
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG TARGET
RUN test -n "$TARGET"
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ./${TARGET}

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
ENTRYPOINT ["/app"]
```

**Step 2: Tạo `.dockerignore`**

`.dockerignore`:

```text
.git
.env*
.claude
.agent
bin
docs
rooms*.txt
real-texts*.txt
```

**Step 3: Build và chạy thử một image**

```bash
make -s image TARGET=tools/poc/natsbench
docker images chatim/natsbench:dev --format '{{.Repository}}:{{.Tag}} {{.Size}}'
docker run --rm --network chatim_default chatim/natsbench:dev -subs 1000 -rate 200 -duration 2s
```

Expected: image khoảng 9–10MB; lệnh chạy in `republish transform: PASS` và `failed=0`.

**Step 4: Commit**

```bash
git add deploy/docker/Dockerfile .dockerignore
git commit -m "build: add multi-stage distroless runtime image for go programs"
```

---

### Task 12: Chạy PoC R1–R5 và ghi kết quả

**Files:**
- Create: `docs/poc/README.md`

**Step 1: Tạo mẫu kết quả `docs/poc/README.md`**

`docs/poc/README.md`:

````markdown
# Kết quả PoC — chatim Phase 1

> Thiết kế: [../designs/260930-chat-core-gateway-design.md](../designs/260930-chat-core-gateway-design.md) (mục 13 — Rủi ro)
> Mỗi lần chạy ghi 1 dòng. Chạy trên máy dev chỉ để kiểm tra công cụ; kết luận R1–R4 phải dựa trên máy giống production.

## Môi trường

| Lần chạy | Ngày | Máy (CPU / RAM / disk) | MongoDB | NATS | Ghi chú |
|---|---|---|---|---|---|
| dev | | | 8.2, 1 node rs0 | 2.15, 1 node | Docker Desktop, chỉ kiểm tra công cụ |
| prod-like | | | 8.2, rs 3 member | 2.15, 3 node | |

## Tiêu chí và kết quả

| # | Câu hỏi | Tiêu chí đạt | Kết quả dev | Kết quả prod-like | Kết luận |
|---|---|---|---|---|---|
| R1a | Dung lượng/tin trên đĩa (dữ liệu thật) | Dự phóng 20 tỷ tin vừa 1 replica set (ghi con số TB) | | | |
| R1b | Trang cũ nhất / ngẫu nhiên / mới nhất khi dữ liệu > WiredTiger cache | p99 ≤ 20ms, plan `CLUSTERED_IXSCAN` | | | |
| R2 | `insertMany` w:majority ở 10K tin/s | Thời gian chờ ack p99 ≤ 30ms, errors = 0 | | | |
| R3a | RePublish đổi subject | `republish transform: PASS` | | | |
| R3b | 1M interest sub + 5K event/s | publish→gateway p99 ≤ 10ms; RAM server (ghi MB) | | | |
| R4 | gws 50K connection | ≤ 40KB/conn; lần ghi cuối của 1 broadcast ≤ 100ms | | | |
| R5 | Soft-ownership khi Redis mất dữ liệu | `go test -race -count=20 ./apps/core/internal/slot/` pass | | | |

## Quyết định sau PoC

- (ghi quyết định giữ/đổi so với thiết kế, kèm lý do)
````

**Step 2: Kiểm tra toàn bộ (R5)**

Run: `make -s fmt-check && make -s vet && make -s go ARGS="test -race -count=20 ./apps/core/internal/slot/" && make -s test`
Expected: tất cả `ok`. Ghi dòng R5.

**Step 3: R1 — xuất dữ liệu thật**

Văn bản giả có từ vựng nhỏ nên tỉ lệ nén không phản ánh thực tế (lần chạy thử: 17x với 100K tin, 3.37x với 10M tin), nên dung lượng **chỉ tính theo dữ liệu thật**. Xuất ít nhất 1 triệu nội dung tin từ MongoDB của hệ thống cũ, mỗi dòng một tin, bằng `mongoexport` có sẵn trong image `mongo:8.2`. Tên collection và field phải chỉnh theo hệ thống cũ:

```bash
docker run --rm -v "$PWD":/work -w /work mongo:8.2 mongoexport --uri "$LEGACY_MONGO_URI" \
  -c <collection_tin_nhan> --fields <field_noi_dung> --type=csv --noHeaderLine --limit 1000000 --out real-texts.txt
```

File này chứa dữ liệu khách hàng: chỉ để ở máy chạy PoC, không commit (đã có trong `.gitignore`), xoá sau khi đo xong.

**Step 4: R1a — dung lượng**

Run: `make -s poc TOOL=mongobench ARGS="seed -rooms 10000 -per-room 1000 -reset -text-file real-texts.txt"`
Expected: dòng `docs=10000000 … disk/doc=…B` và 2 dòng `projected on-disk …`. Ghi R1a. Trên máy prod-like dùng `-rooms 100000` (100M tin).

**Step 5: R1b — đọc khi dữ liệu lớn hơn cache**

Đặt `MONGO_CACHE_GB=1` trong `.env` để dữ liệu vượt WiredTiger cache, tạo lại container, rồi restart để xoá cache:

```bash
make infra-down && make infra-up
docker restart chatim-mongodb && ./scripts/wait-mongo-primary.sh
make -s poc TOOL=mongobench ARGS="read -mode oldest -duration 60s"
make -s poc TOOL=mongobench ARGS="read -mode random -duration 60s"
make -s poc TOOL=mongobench ARGS="read -mode latest -duration 60s"
```

Trên Linux host, chạy thêm `sync; echo 3 | sudo tee /proc/sys/vm/drop_caches` trước khi đọc để xoá cả page cache của OS.

Expected: mỗi lệnh in `plan … CLUSTERED_IXSCAN` và `empty=0`. Tiêu chí: p99 ≤ 20ms. Ghi R1b.

**Step 6: R2 — ghi 10K tin/s**

Run: `make -s poc TOOL=mongobench ARGS="write -rate 10000 -duration 60s -rooms 5000 -flushers 6"`
Expected: `achieved=≈10000/s errors=0`. Tiêu chí: `arrival->commit` p99 ≤ 30ms. Chỉ replica set 3 member mới phản ánh đúng chi phí `w:majority`. Ghi R2.

**Step 7: R3 — 1M interest subscription**

Run: `make -s poc TOOL=natsbench ARGS="-subs 1000000 -conns 4 -rate 5000 -duration 60s"`
Expected: `republish transform: PASS`; `failed=0`. Tiêu chí: `publish -> gateway` p99 ≤ 10ms. Ghi thêm thời gian subscribe và `server mem`. Ghi R3a, R3b.

**Step 8: R4 — 50K connection trên Linux (2 máy nếu có)**

Build image rồi đưa sang máy đo (registry hoặc `docker save … | ssh <host> docker load`):

```bash
make -s image TARGET=tools/poc/wsbench
docker save chatim/wsbench:dev | ssh <server-host> docker load
docker save chatim/wsbench:dev | ssh <client-host> docker load
```

Trên máy client: `sudo sysctl -w net.ipv4.ip_local_port_range="1024 65535"`.

```bash
# máy server
docker run --rm --network host --ulimit nofile=200000:200000 chatim/wsbench:dev server -ports 9001,9002,9003,9004 -every 5s
# máy client
docker run --rm --network host --ulimit nofile=200000:200000 chatim/wsbench:dev client \
  -addrs ws://<server-host>:9001/ws,ws://<server-host>:9002/ws,ws://<server-host>:9003/ws,ws://<server-host>:9004/ws \
  -conns 50000 -dial-rate 2000
```

Expected: client `connected=50000 failed=0`. Tiêu chí: `per-conn` ≤ 40KB, `last write done` ≤ 100ms. Ghi R4. Độ trễ đo phía client chỉ đáng tin khi 2 máy đồng bộ NTP. R1–R3 trên máy prod-like chạy theo cách tương tự với `chatim/mongobench:dev` / `chatim/natsbench:dev`; `mongobench` cần thư mục ghi được: thêm `--user "$(id -u):$(id -g)" -v "$PWD":/work -w /work`.

**Step 9: Ghi quyết định sau PoC**

Điền mục "Quyết định sau PoC" trong `docs/poc/README.md`. Nếu có tiêu chí không đạt, cập nhật mục 13 và Decision Log của [thiết kế](../designs/260930-chat-core-gateway-design.md) theo cột "Nếu không đạt".

**Step 10: Commit**

```bash
git add docs/poc/README.md docs/designs/260930-chat-core-gateway-design.md
git commit -m "docs(poc): record phase 1 poc results and decisions"
```

---

### Task 13: Cập nhật README cách chạy hạ tầng dev

**Files:**
- Modify: `README.md` (thêm mục mới trước mục "Tài liệu")

**Step 1: Thêm mục**

```markdown
## Chạy hạ tầng dev

Chỉ cần Docker; Go chạy trong container `golang:1.26` qua `make`.

    cp .env.example .env        # đổi MONGO_ROOT_PASSWORD (chữ, số, - hoặc _)
    make infra-up               # mongo rs0 :27117, redis :6380, nats :4223 (monitor :8223)
    make test                   # go test -race ./... trong container
    make go ARGS="vet ./..."    # lệnh go bất kỳ
    make infra-down             # dừng; make infra-reset để xoá cả dữ liệu

Công cụ PoC nằm ở `tools/poc/`, chạy bằng `make poc TOOL=<tên> ARGS="…"` — cách chạy và kết quả: [docs/poc/README.md](docs/poc/README.md).
```

Và thêm vào danh sách ở mục "Tài liệu": `- [Kết quả PoC](docs/poc/README.md)` · `- [Plan M0–M1](docs/plans/2026-09-30-phase1-foundation-and-poc.md)`.

**Step 2: Kiểm tra link**

Run: `for l in docs/poc/README.md docs/plans/2026-09-30-phase1-foundation-and-poc.md; do test -f "$l" && echo "ok $l"; done`
Expected: 2 dòng `ok`.

**Step 3: Commit**

```bash
git add README.md
git commit -m "docs: add dev infrastructure and poc instructions to readme"
```

---

## Roadmap sau M1

Mỗi milestone có plan chi tiết riêng, viết sau khi có kết quả M1.

| Milestone | Nội dung | Phụ thuộc |
|---|---|---|
| **M2 — core: đường ghi** | ghim digest cho `golang:1.26` và distroless trước khi build image app; `proto/chatim/v1` + buf (chạy trong container); `pkg/config`, `logx`, `telemetry`; bootstrap collection/index theo quy tắc sẵn sàng sharding; actor theo room + flusher; chống trùng `cid`; sửa/xoá/reaction/pin/read + `message_edits`; publish JetStream + watermark publish bù; gRPC Send/Edit/Delete/React/Pin/Read; integration test bằng testcontainers | R1, R2, R5 |
| **M3 — core: đường đọc** | GetHistory, GetMessages, ListMyRoomIDs, ListMyRooms, Sync, GetEditHistory, GetReactions, ListPins, ListBookmarks; bộ test sẵn sàng sharding trên cluster 2 shard (`SINGLE_SHARD`) | M2, R1 |
| **M4 — gateway** | gws server, JWT/JWKS, frame protobuf, interest subscription, hàng đợi gửi có giới hạn + 4008, typing/presence, đọc bảng slot từ Redis | M2, R3, R4 |
| **M5 — hardening** | service `core`/`gateway` trong compose (dùng Dockerfile ở Task 11), load test 100K, chaos test, OTel/Prometheus/Grafana, CI (fmt-check, vet, test -race trong container); chạy container Go bằng uid của user trên Linux (hiện `make` chạy bằng root nên file sinh ra trong repo thuộc root trên Linux; macOS không bị) | M3, M4 |

## Tóm tắt kiểm chứng

- Unit test: `make test` (keys, ids, slotmap, latency, slot manager — tất cả có `-race`, chạy trong container).
- R5: `make go ARGS="test -race -count=20 ./apps/core/internal/slot/"`.
- R1–R4: Task 12, ghi vào `docs/poc/README.md` theo tiêu chí có sẵn trong bảng.
