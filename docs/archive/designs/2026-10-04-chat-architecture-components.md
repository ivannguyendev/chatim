# Kiến trúc Components Hệ thống Chatim

> Ngày: 2026-10-04

Tài liệu này mô tả sơ đồ components ở tầng kiến trúc vĩ mô (Macro-Architecture) và cấu trúc module bên trong của hệ thống `chatim`, bổ sung cho tài liệu thiết kế gốc [260930-chat-core-gateway-design.md](./260930-chat-core-gateway-design.md).

## 1. Sơ đồ Concept Tổng quát (Macro-Architecture)

Sơ đồ này thể hiện `chatim` là một hạ tầng nền tảng (CPaaS). `core` chịu trách nhiệm ghi và xử lý logic nhất quán, kết hợp với `NATS JetStream` làm Event Bus để mở rộng khả năng phối hợp với các app khác.

```mermaid
flowchart TD
    %% Clients
    Client["Client SDK (Web/Mobile)"]

    %% External & Future Apps
    AppAuth["Auth App (Tenant/App)"]
    AppAPI["API App (Admin/REST)"]
    AppEvents["Events App (Bot/CRM/Webhook)"]
    AppPush["Push App (APNs/FCM)"]

    %% Core Ecosystem
    subgraph Chatim_Platform["Chatim Platform Ecosystem"]
        AppGateway["Gateway App (WebSocket)"]
        AppCore["Core App (Logic & Persistence)"]
        NATS[("NATS JetStream (Event Bus)")]
        Redis[("Redis (State & Dedupe)")]
        MongoDB[("MongoDB (Clustered Storage)")]
    end

    %% Interactions
    Client -. "1. Lấy JWT" .-> AppAuth
    Client -- "2. WebSocket (Live Event)" --> AppGateway
    AppGateway -- "3. gRPC (Gửi lệnh ghi)" --> AppCore
    
    AppCore -- "4. Commit tin & state" --> MongoDB
    AppCore -- "5. Mượn Slot / Lưu cờ Ack" --> Redis
    AppCore -- "6. Publish Events (Bất đồng bộ)" --> NATS
    
    %% NATS Fanout
    NATS -- "7. Subscribe (Live events)" --> AppGateway
    NATS -. "Subscribe (Push notification)" .-> AppPush
    NATS -. "Subscribe (Bot/Webhook)" .-> AppEvents
    
    %% API Interaction
    Client -. "REST (Query/Upload)" .-> AppAPI
    AppAPI -. "Read Data" .-> MongoDB
    
    classDef core fill:#1e40af,stroke:#fff,color:#fff,stroke-width:2px;
    classDef infra fill:#374151,stroke:#fff,color:#fff,stroke-width:2px;
    classDef future fill:#047857,stroke:#fff,color:#fff,stroke-width:2px,stroke-dasharray: 5 5;
    
    class AppCore core;
    class AppGateway core;
    class NATS,MongoDB,Redis infra;
    class AppAuth,AppAPI,AppEvents,AppPush future;
```

## 2. Sơ đồ Tương tác Module Nội bộ (App-level Components)

Sơ đồ thể hiện luồng giao tiếp giữa các package nội bộ (internal modules) của 2 app chính là `core` và `gateway`.

```mermaid
flowchart TB
    %% Gateway Modules
    subgraph App_Gateway ["Gateway App (internal/*)"]
        WS["ws (WebSocket Server)"]
        Authn["authn (JWT Validator)"]
        Session["session (Client State)"]
        Interest["interest (NATS Routing)"]
        Fanout["fanout (Broadcaster)"]
    end

    %% Core Modules
    subgraph App_Core ["Core App (internal/*)"]
        GRPCSrv["grpcsrv (gRPC Server)"]
        Router["actor (Router)"]
        Actor["actor (Room Actor)"]
        Batcher["dedupe (Batcher)"]
        Flush["flush (Flusher)"]
        Publish["publish (Publisher)"]
        Slot["slot (Slot Manager)"]
        Reconcile["reconcile (Reconciler)"]
    end

    %% Infrastructure Data
    Infra_DB[("MongoDB (WiredTiger)")]
    Infra_Redis[("Redis")]
    Infra_NATS[("NATS JetStream")]

    %% --- Gateway flow ---
    WS --> Authn
    Authn --> Session
    Session --> Interest
    Interest -->|Sub/Unsub| Infra_NATS
    Infra_NATS -->|Live Event| Fanout
    Fanout -->|Encode 1 lần, Fan-out nhiều| Session
    Session -->|Send Command| GRPCSrv

    %% --- Core Flow ---
    Slot <-->|Heartbeat & Lease| Infra_Redis
    Slot -.->|Evict slots| Router
    
    GRPCSrv -->|Command| Router
    Router -->|1 Goroutine / Room| Actor
    
    Actor <-->|Reserve/Commit CID| Batcher
    Batcher <-->|EVALSHA| Infra_Redis
    
    Actor -->|Append Doc| Flush
    Flush -->|insertMany| Infra_DB
    
    Actor -->|1. Ack Client| GRPCSrv
    Actor -->|2. Enqueue Event| Publish
    
    Publish -->|PublishAsync| Infra_NATS
    Publish -->|Lưu cờ đã Ack| Infra_Redis
    
    %% --- Reconciler Flow ---
    Infra_DB -.->|Change Stream| Reconcile
    Infra_Redis -.->|Check cờ Ack| Reconcile
    Reconcile -.->|Publish bù (nếu rớt)| Infra_NATS

    %% Styling
    classDef module fill:#0f172a,stroke:#3b82f6,color:#e2e8f0,stroke-width:1px;
    classDef infra fill:#374151,stroke:#fff,color:#fff,stroke-width:2px;
    classDef highimpact fill:#7f1d1d,stroke:#f87171,color:#fff,stroke-width:2px;
    
    class WS,Authn,Session,Interest,Fanout,GRPCSrv,Router,Batcher,Publish,Slot module;
    class Infra_DB,Infra_Redis,Infra_NATS infra;
    class Actor,Flush,Reconcile highimpact;
```

## 3. Các Flow Kỹ Thuật Đặc Biệt Chú Ý

Từ sơ đồ module ở trên, có 3 flow chính mang tính quyết định đến kiến trúc và hiệu năng của hệ thống:

1. **Luồng Ghi & Chống Trùng (Write Path & Batcher)**
   - **Đường đi:** Client → Gateway (`session`) → Core (`grpcsrv`) → `actor` → `flush` → MongoDB.
   - **Đặc điểm:** Tối ưu hoá thông qua `dedupe.Batcher` (gom lệnh check Redis) và `flush` (gom lệnh ghi Mongo thành `insertMany`).

2. **Luồng Phát Sự Kiện & Đối Soát (Event Publisher & Reconciler)**
   - **Đường đi:** MongoDB Change Stream → `reconcile` (đối chiếu Redis mark) → NATS JetStream.
   - **Đặc điểm:** Tách biệt đường ghi và đường publish để giảm độ trễ (Ack client trước khi Publish). Reconciler đóng vai trò sửa sai, đảm bảo Eventual Consistency.

3. **Luồng Quản Lý Phân Mảnh (Slot Ownership & HA)**
   - **Đường đi:** Redis Lease ↔ `slot` → `actor` (Router).
   - **Đặc điểm:** Áp dụng Rendezvous Hash để chia tải (Slot). Đảm bảo mỗi room chỉ có đúng 1 Goroutine xử lý tại 1 thời điểm trên toàn Cluster để tránh Race Condition.

---

### Sơ đồ chi tiết: 1. Luồng Ghi & Chống Trùng (Write Path & Batcher)

Luồng này mô tả đường đi của một tin nhắn từ Client xuống tới DB, nhấn mạnh cách `core` ưu tiên chốt ghi thành công xuống Database nhanh nhất có thể bằng Batching (cả trên Redis dedupe lẫn MongoDB insert).

```mermaid
sequenceDiagram
    participant C as Client
    participant GW as Gateway
    participant Srv as Core: gRPC
    participant R as Core: Router
    participant A as Core: Actor(room)
    participant B as Core: dedupe.Batcher
    participant F as Core: Flusher
    participant Redis as Redis (Dedupe)
    participant DB as MongoDB

    C->>GW: Gửi tin nhắn (WebSocket)
    GW->>Srv: gRPC Send(room, thread, cid)
    Srv->>R: Route request
    R->>A: Phân bổ vào Goroutine của Room
    
    A->>B: Reserve(cid)
    B->>Redis: EVALSHA (Gom batch check cid)
    Redis-->>B: Trả kết quả Reserve
    B-->>A: OK (Chưa trùng)
    
    A->>F: Đẩy vào hàng đợi Flush
    F->>DB: insertMany(ordered:false) (Gom nhiều room)
    DB-->>F: Ghi thành công
    F-->>A: Kết quả Inserted
    
    A->>Srv: Trả Ack
    Srv-->>GW: Trả gRPC Response
    GW-->>C: WebSocket Ack
    
    %% Async actions after DB commit
    par Async Publish & Commit
        A->>Core: Publisher: Enqueue(Event)
        A->>B: Commit(cid)
        B->>Redis: Lưu cid đã commit (15 phút)
    end
```

### Sơ đồ chi tiết: 2. Luồng Phát Sự Kiện & Đối Soát (M2a.2)

Flow này giải quyết bài toán "Publish Best-effort" để tốc độ Write Path không bị nghẽn bởi hạ tầng queue (NATS), trong khi Reconciler đảm bảo tỷ lệ chuyển phát là 100% (không rơi rớt event).

```mermaid
sequenceDiagram
    participant A as Core: Actor
    participant Pub as Core: Publisher
    participant Rec as Core: Reconciler (Slot 0)
    participant DB as MongoDB
    participant Redis as Redis (Ack Marks)
    participant NATS as JetStream

    %% Normal Publish Path
    A->>Pub: Enqueue(Event)
    Pub->>NATS: PublishAsync (Best-effort)
    NATS-->>Pub: PubAck
    Pub->>Redis: SETBIT mark đã Ack (Gom batch)

    %% Reconciler Path
    loop Chạy ngầm liên tục
        DB-->>Rec: Change Stream (Insert vào messages)
        Rec->>Rec: Chờ delay (D = 30s)
        Rec->>Redis: Tra cứu GETBIT mark đã Ack
        alt Đã có Mark
            Redis-->>Rec: Bit = 1
            Rec->>Rec: Bỏ qua (Đã Publish thành công)
        else Không có Mark (Event bị rớt)
            Redis-->>Rec: Bit = 0
            Rec->>NATS: Publish lại Event bù
        end
        Rec->>DB: Ghi nhận vị trí Reconciler đã xử lý
    end
```

### Sơ đồ chi tiết: 3. Luồng Quản Lý Phân Mảnh (Slot Ownership & HA)

Cơ chế Lease (thuê) Slot qua Redis. Khi sự cố xảy ra, `Router` phải huỷ quyền quản lý cũ (Evict) để nhường đường cho Core mới tiếp quản, đảm bảo không có 2 Goroutine cùng xử lý 1 Room.

```mermaid
sequenceDiagram
    participant C1 as Core 1 (Slot Mgr)
    participant C2 as Core 2 (Slot Mgr)
    participant Redis as Redis (Slots)
    participant R1 as Core 1: Router
    participant A1 as Core 1: Actor(room)

    loop Mỗi 1 giây
        C1->>Redis: Heartbeat (SET chatim:core:{id})
        C2->>Redis: Heartbeat
    end

    %% C1 holds slot
    C1->>Redis: Gia hạn slot đang giữ (PEXPIRE)
    Redis-->>C1: OK
    
    %% Simulate C1 crashing or network partition
    Note over C1: Core 1 rớt mạng hoặc bị treo
    
    C2->>Redis: Lấy danh sách core & tính thiếu Slot
    C2->>Redis: SET NX (Chiếm Slot trống)
    Redis-->>C2: OK (C2 giờ là chủ)

    %% C1 wakes up
    Note over C1: Core 1 phục hồi / Hết treo
    C1->>Redis: Gia hạn slot
    Redis-->>C1: Fail (Đã bị mất Slot)
    
    C1->>R1: Gọi Hook AfterLose(slots)
    R1->>A1: EvictSlots (Ngừng Actor)
    Note over A1: Actor từ chối lệnh mới, hoàn tất lệnh <br/>đang xử lý (nếu có) rồi tự huỷ.
    
    %% Next request goes to C2
    Note over C2: Lệnh mới cho room sẽ được chuyển tới C2.<br/>C2 sẽ khởi tạo Actor mới và nạp lại trạng thái.
```
