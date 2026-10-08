# Kiến trúc components hệ thống chatim

> Ngày: 2026-10-04 · Cập nhật: 2026-10-08 (bố cục package core trước M2c)

Tài liệu này vẽ components ở tầng vĩ mô và bố cục package bên trong `apps/core`. Nguồn sự thật vẫn là [261005-chatim-architecture.md](./261005-chatim-architecture.md); khi lệch, theo design.

## 1. Tổng quát (macro)

`core` giữ mọi lệnh ghi và tính nhất quán; NATS JetStream là event bus cho các app khác. Gateway và các app xanh lá chưa xây.

```mermaid
flowchart TD
    Client["Client SDK (Web/Mobile)"]

    AppAuth["Auth App (tenant/JWT)"]
    AppEvents["Distribution / Bot / Webhook"]
    AppPush["Push App (APNs/FCM)"]

    subgraph Platform["chatim"]
        AppGateway["Gateway (WebSocket)"]
        AppCore["Core (logic + lưu trữ)"]
        NATS[("NATS JetStream<br/>CHATIM_EVT, CHATIM_WORK")]
        RedisState[("Redis state<br/>slot lease")]
        RedisDedupe[("Redis dedupe<br/>cid, request_id, ack mark")]
        MongoDB[("MongoDB replica set<br/>clustered collections")]
    end

    Client -. "1. lấy JWT" .-> AppAuth
    Client -- "2. WebSocket" --> AppGateway
    AppGateway -- "3. gRPC unary" --> AppCore
    AppCore -- "4. ghi fact/state" --> MongoDB
    AppCore -- "5. lease slot" --> RedisState
    AppCore -- "6. chống trùng, ack mark" --> RedisDedupe
    AppCore -- "7. publish event" --> NATS
    MongoDB -. "change feed" .-> AppCore
    NATS -- "live.{t}.*.{rid}.>" --> AppGateway
    NATS -. "subscribe" .-> AppPush
    NATS -. "subscribe" .-> AppEvents

    classDef core fill:#1e40af,stroke:#fff,color:#fff,stroke-width:2px;
    classDef infra fill:#374151,stroke:#fff,color:#fff,stroke-width:2px;
    classDef future fill:#047857,stroke:#fff,color:#fff,stroke-width:2px,stroke-dasharray: 5 5;
    class AppCore core;
    class NATS,MongoDB,RedisState,RedisDedupe infra;
    class AppGateway,AppAuth,AppEvents,AppPush future;
```

## 2. Bố cục package `apps/core`

Package giữ tên ngắn, gom thư mục theo luồng. Tên nhóm lấy từ design: `send` = §6.2 gửi tin, `change` = §6.3 lệnh đổi.

```
apps/core/
  main.go          os.Exit(app.Main(os.Args[1:]))
  itest/           test tích hợp cả core (app.Run, infra thật)
  internal/
    app/           wiring, vòng đời, lệnh serve/probe/resync/recount
    config/        đọc + kiểm config lúc boot
    api/           grpcsrv, view
    model/         domain, pbconv, access
    send/          actor, dedupe, flush, memberwatch
    change/        mutate, ownership, counter, pinproj
    event/         publish, eventmark, work, effects, reconcile, resync
    store/         store (port), memstore, mongostore, storetest
    platform/      slot, metrics, redisguard, testlog
```

Phụ thuộc đi xuống; ngoại lệ duy nhất là `change/mutate` → `event/work` (phiếu hẹn sinh tồn):

```mermaid
flowchart TB
    app["app (+ config)"]
    api["api<br/>grpcsrv · view"]
    send["send<br/>actor · dedupe · flush · memberwatch"]
    change["change<br/>mutate · ownership · counter · pinproj"]
    event["event<br/>publish · eventmark · work · effects · reconcile · resync"]
    model["model<br/>domain · pbconv · access"]
    store["store<br/>store · memstore · mongostore · storetest"]
    platform["platform<br/>slot · metrics · redisguard · testlog"]

    app --> api
    app --> event
    app --> platform
    api --> send
    api --> change
    send --> model
    send --> store
    change --> model
    change --> store
    change -->|work.Timers| event
    event --> model
    event --> store
    send -->|redisguard| platform
    event -->|redisguard| platform
    store --> model
```

| Nhóm | Package | Vai trò | Design |
|---|---|---|---|
| `api` | `grpcsrv` | đọc metadata tenant/user, gọi actor/mutate/store, trả lỗi domain | §9 |
| | `view` | pipeline cho người đọc: gộp retry, che tin xoá/ẩn | §9.2 |
| `model` | `domain` | kiểu và lỗi nghiệp vụ, validate | §4–§5 |
| | `pbconv` | domain → proto, event id tự nhiên | §11 |
| | `access` | permission hook: tenant + membership rồi `Policy` | §9.2 |
| `send` | `actor` | một goroutine mỗi room, seq, write group | §6.2 |
| | `dedupe` | cid/`request_id` qua LRU + Redis, batcher | §6.2, D58 |
| | `flush` | gom `insertMany` theo shard | §6.2 |
| | `memberwatch` | nghe `member_removed`/`member_role_changed`, xoá cache member của actor | D106 |
| `change` | `mutate` | sửa/xoá/ẩn/clear/react/pin/member/read | §6.3–§6.5 |
| | `ownership` | kế nhiệm owner trong transaction | D100 |
| | `counter` | recount reaction có witness + CAS | §7, D90 |
| | `pinproj` | fold pin fact + CAS | D92 |
| `event` | `publish` | queue theo shard, `PublishMsgAsync`, ack mark | §8.1 |
| | `eventmark` | bitmap ack mark trên Redis dedupe | D52, D60 |
| | `work` | record, `CHATIM_WORK`, phiếu hẹn sinh tồn | §8.3, D111 |
| | `effects` | worker mỗi partition, registry effect theo kind | §8.3 |
| | `reconcile` | reader change feed trên chủ slot 0 | §8.2 |
| | `resync` | phát lại một khoảng feed bị mất | §14 |
| `store` | `store` | port + kiểu lưu trữ | §5 |
| | `memstore`, `mongostore` | adapter; cả hai qua `storetest` | §5.1 |
| `platform` | `slot` | lease slot qua Redis, hook evict | §6.1 |
| | `metrics` | registry Prometheus `/metrics` | §12 |
| | `redisguard` | cooldown khi Redis lỗi | D44 |
| | `testlog` | tắt log Redis trong test | — |

M2c: thread, reply/forward, mention đi qua `send`; bookmark (lớp tập) đi qua `change`; effect mới vào `event/effects`.

## 3. Luồng gửi tin (`send`)

```mermaid
sequenceDiagram
    participant GW as Gateway / corecli
    participant Srv as api/grpcsrv
    participant A as send/actor (room)
    participant B as send/dedupe.Batcher
    participant F as send/flush
    participant P as event/publish
    participant R as Redis dedupe
    participant DB as MongoDB
    participant N as JetStream

    GW->>Srv: SendMessage(room, cid)
    Srv->>A: Router → actor của room
    A->>B: Reserve(cid)
    B->>R: EVALSHA (gom theo shard)
    R-->>B: mới / đã commit / pending
    A->>F: doc seq kế tiếp
    F->>DB: insertMany(ordered:false, w:majority)
    DB-->>F: Inserted / Duplicate / Unknown
    F-->>A: kết quả (Duplicate, Unknown → Find theo cid)
    A-->>Srv: ack
    Srv-->>GW: response
    A->>P: Enqueue msg_created
    A->>B: Commit(cid) (settle loop)
    P->>N: PublishMsgAsync (best-effort)
    N-->>P: PubAck
    P->>R: ack mark (gom 256 key / 10ms)
```

## 4. Luồng lệnh đổi (`change`)

```mermaid
sequenceDiagram
    participant Srv as api/grpcsrv
    participant M as change/mutate
    participant AC as model/access
    participant DB as MongoDB
    participant T as event/work.Timers
    participant P as event/publish

    Srv->>M: Edit / React / Pin / AddMembers …
    M->>AC: Admit (tenant + member)
    M->>DB: Find target
    M->>AC: Allow (policy với tác giả, kind)
    opt giá trị dẫn xuất ghi ngoài transaction (member_count)
        M->>T: Arm phiếu hẹn
    end
    M->>DB: fact / CAS ver / transaction owner
    M->>DB: projection hoặc counter (pinproj, counter)
    opt
        M->>T: Disarm
    end
    M->>P: Enqueue event (lỗi bỏ qua, worker phát bù)
    M-->>Srv: kết quả
```

Không retry nội bộ: CAS hoặc transaction xung đột trả `UNAVAILABLE` ngay, client retry.

## 5. Effect engine (`event`)

```mermaid
sequenceDiagram
    participant DB as MongoDB change feed
    participant Rd as event/reconcile (chủ slot 0)
    participant W as CHATIM_WORK (32 partition)
    participant E as event/effects (mọi core, partition mình sở hữu)
    participant R as Redis dedupe
    participant N as CHATIM_EVT

    DB-->>Rd: insert/update/replace (kind 1–9)
    Rd->>W: work.Record, Nats-Msg-Id = record id
    Rd->>DB: lưu resume token sau prefix đã ack
    W-->>E: fetch batch
    E->>DB: room_activity, edit_projection, pin_projection, reaction_counter …
    E->>E: chờ RECONCILE_DELAY (5s)
    E->>R: msg_created: đọc ack mark
    alt chưa có mark / effect không dùng mark
        E->>DB: đọc doc hiện tại
        E->>N: publish lại (stream bỏ bản trùng theo id)
    end
    E-->>W: Ack (mọi effect nil) hoặc Nak(WORK_RETRY_DELAY)
```

Mất lịch sử feed: reader log lỗi và chạy từ hiện tại; vá khoảng hở bằng `/app resync`.

## 6. Slot ownership (`platform/slot`)

```mermaid
sequenceDiagram
    participant C1 as Core 1 slot.Manager
    participant C2 as Core 2 slot.Manager
    participant Redis as Redis state
    participant R1 as Core 1 send/actor.Router

    loop mỗi tick
        C1->>Redis: heartbeat + gia hạn lease (Lua)
        C2->>Redis: heartbeat + gia hạn lease
    end
    Note over C1: Core 1 treo / mất mạng
    C2->>Redis: lease hết hạn → claim slot
    Note over C1: Core 1 tỉnh lại
    C1->>Redis: gia hạn thất bại
    C1->>R1: AfterLose(slots) → EvictSlots
    Note over R1: actor từ chối lệnh mới, xong lệnh đang chạy rồi tự huỷ
    Note over C2: lệnh mới của room tới Core 2 (slotmap.Resolver)
```

Ownership chỉ mua batching, thứ tự và cache. Đúng đắn đến từ `_id` unique làm CAS, nên hai chủ cùng lúc không làm mất hay nhân đôi tin. Effect worker chỉ fetch partition `n` khi `Owns(n)`; reader chạy khi `Owns(0)`.

## 7. Gateway (chưa xây)

Module dự kiến: `ws` (gws), `authn` (JWT theo JWKS tenant), `session`, `interest` (subscribe `live.{t}.*.{rid}.>`), `fanout` (encode một lần, gửi nhiều). Gateway gọi core qua gRPC và nhận event từ NATS; không đọc Mongo.
