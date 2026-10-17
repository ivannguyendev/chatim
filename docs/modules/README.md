# chatim — Tài liệu Kỹ thuật Module & Tính năng (How It Works)

> **Mục tiêu:** Bản đặc tả chi tiết về cách thức vận hành (How It Works) của toàn bộ các tính năng nghiệp vụ trong nền tảng chat `chatim`. Tài liệu được phân rã theo **Module / Tính năng nghiệp vụ**, trả lời trực diện:
> - Tính năng / Use Case này vận hành thế nào?
> - Được bảo đảm nhờ **Cơ chế (Mechanisms)** gì?
> - Triển khai bằng **Kỹ thuật (Technologies)** gì?
> - Có **Hạn chế kỹ thuật (Limitations)** và **Phụ thuộc kỹ thuật (Dependencies)** gì khi thiết kế?

---

## 1. Bản đồ Tính năng Hệ thống (Feature Map)

```mermaid
flowchart TD
    subgraph Client["Tầng Khách (Client SDK)"]
        Web["Web SDK"]
        Mobile["Mobile SDK (iOS / Android)"]
    end

    subgraph Entry["Tầng Cổng & Thời gian thực"]
        M09["Module 09: Realtime Delivery & Ephemeral<br/>(App websocket M4 chưa xây, Typing, Presence)"]
    end

    subgraph CoreFeatures["Tầng Nghiệp vụ Hội thoại (Core Features)"]
        M01["Module 01: Rooms, DMs & Channels<br/>(Phòng chat 1-1, Nhóm ≤5K, Kênh ~200K chưa xây)"]
        M02["Module 02: Message Sending & Ordering<br/>(Gửi tin, Timeline sequence, Chống trùng CID)"]
        M03["Module 03: Message Edit, Delete & Privacy<br/>(Sửa tin + Audit log, Thu hồi, Ẩn tin, Xoá lịch sử)"]
        M04["Module 04: Reactions, Pins & Bookmarks<br/>(Thả cảm xúc, Ghim tin top room, Bookmark)"]
        M05["Module 05: Membership & Roles<br/>(Thêm/Xoá thành viên, Kế nhiệm Owner, Phân quyền)"]
        M06["Module 06: Threads, Replies & Mentions<br/>(Trả lời trích dẫn, Theo dõi thread, Tag @mention)"]
        M07["Module 07: Read Receipts & Unread Badge<br/>(Đã đọc, Đánh dấu chưa đọc, Badge đếm 99+)"]
        M08["Module 08: History Retrieval & Reconnect Sync<br/>(Phân trang lịch sử, Đồng bộ sau rớt mạng)"]
    end

    subgraph Resilience["Tầng Tự phục hồi & Dữ liệu"]
        M10["Module 10: Data Reconciliation & Self-Healing<br/>(Cờ _state, Sweeper slot 0, Projection, Survivor Timers)"]
    end

    Client <-->|WebSocket| M09
    M09 -->|gRPC Unary theo Slot| M01
    M09 -->|gRPC Unary theo Slot| M02
    M09 -->|gRPC Unary theo Slot| M03
    M09 -->|gRPC Unary theo Slot| M04
    M09 -->|gRPC Unary theo Slot| M05
    M09 -->|gRPC Unary theo Slot| M06
    M09 -->|gRPC Unary theo Slot| M07
    M09 -->|gRPC Unary theo Slot| M08

    M02 -.->|Fast-path Events| M09
    M03 -.->|Fast-path Events| M09
    M04 -.->|Fast-path Events| M09
    M05 -.->|Fast-path Events| M09
    M07 -.->|Fast-path Events| M09

    M02 -->|Cờ _state chờ phát| M10
    M03 -->|Cờ _state chờ phát| M10
    M04 -->|Cờ _state chờ phát| M10
    M05 -->|Cờ _state chờ phát| M10
    M10 -.->|Sweeper phát bù| M09
```

---

## 2. Sơ đồ Luồng Sự kiện Toàn cảnh Hệ thống (Global Event & Data Topology)

Sơ đồ khối dưới đây thể hiện luồng luân chuyển dữ liệu và liên kết phối hợp giữa toàn bộ 10 module nghiệp vụ từ lúc Client phát lệnh đến khi lưu trữ, phân phối realtime và tự phục hồi:

```mermaid
flowchart TD
    %% Tầng Client & Gateway
    subgraph Clients["Tầng Thiết bị Khách (Clients)"]
        UserA["Client A (Người gửi)"]
        UserB["Client B (Đang mở phòng)"]
        UserC["Client C (Thiết bị khác của A)"]
    end

    subgraph GW["Module 09: App websocket (WebSocket gws)"]
        ConnHub["Connection Hub (Zero-Alloc Fanout)"]
        EphemRouter["Ephemeral Bypass (Typing / Presence)"]
    end

    %% Tầng Core
    subgraph CoreLayer["Cụm Dịch vụ Lõi (Core Service Modules)"]
        M01["M01: Rooms & DMs<br/>(Sổ nhận chỗ tất định)"]
        M02["M02: Send Actor & Flusher<br/>(Single-Writer seq)"]
        M03["M03: Edit & Delete<br/>(Fact & CAS Projection)"]
        M04["M04: Reactions & Pins<br/>(Survivor Timers)"]
        M05["M05: Roles & Succession<br/>(Single Transaction)"]
        M06["M06: Threads & Mentions<br/>(Flat rp {th, s})"]
        M07["M07: Read Receipts<br/>(Unread đếm tới 100)"]
        M08["M08: Reader View Pipeline<br/>(Lọc & che dấu riêng tư)"]
    end

    %% Tầng Lưu trữ & Cache
    subgraph Storage["Tầng Lưu trữ Bền vững & Bộ nhớ đệm"]
        RedisStorage[("Redis<br/>- Dedupe EVALSHA (M02)<br/>- request_id (M01, M05)")]
        MongoStorage[("MongoDB Replica Set<br/>- Clustered messages (M02, M08)<br/>- room_dms (M01)<br/>- message_edits Fact (M03)<br/>- members & interactions (M04, M05, M07)")]
    end

    %% Tầng Message Bus
    subgraph EventBus["Hạ tầng NATS JetStream"]
        NATSEph["NATS Ephemeral (Typing)"]
        NATSEvt["NATS Live Events (CHATIM_EVT)"]
        NATSWork["NATS Timer Stream (CHATIM_TIMER)"]
    end

    %% Tầng Projection & Tự phục hồi
    subgraph AsyncWorkers["Tầng Xử lý Ngầm & Tự phục hồi (M3.1)"]
        WorkerIndex["Projection room_activity · reply_index (M06, M07, M10)"]
        MentionApp["Mention App: consumer mention (mọi core) → mentions, CHATIM_NOTIFY (M06)"]
        WorkerTimer["Consumer timer-due: count_repair · member_count_repair (M04, M05, M10)"]
        M10Reconcile["M10: Sweeper slot 0<br/>(quét cờ _state trên partial index)"]
    end

    %% Liên kết luồng
    UserA -->|1. Frame gửi tin / lệnh| ConnHub
    UserA -.->|Typing frame| EphemRouter
    EphemRouter -.-> NATSEph -.-> ConnHub -.-> UserB

    ConnHub -->|2. gRPC Unary| M02
    ConnHub -->|gRPC lệnh khác| M01 & M03 & M04 & M05 & M07
    ConnHub -->|Đọc lịch sử| M08
    M08 -->|Clustered read| MongoStorage

    %% Core ghi Storage
    M02 <-->|Reserve CID| RedisStorage
    M02 -->|Group Commit| MongoStorage
    M03 & M04 & M05 & M07 --> MongoStorage

    %% Core phát event
    M02 -.->|Fast-path Ack| ConnHub
    M02 -.->|Fast-path Event| NATSEvt
    M03 & M04 & M05 & M07 -.-> NATSEvt
    NATSEvt -->|Interest Routing| ConnHub
    ConnHub -->|Đẩy tin tức thời| UserB
    NATSEvt -.->|Đồng bộ vị trí đọc| UserC

    %% Luồng projection & phiếu hẹn
    NATSEvt --> WorkerIndex
    NATSEvt --> MentionApp
    MentionApp -->|Chỉ mục mention| MongoStorage
    M04 & M05 -.->|Arm survivor timer| NATSWork
    NATSWork --> WorkerTimer
    WorkerIndex -->|Danh sách hội thoại, rc, reply_threads| MongoStorage
    WorkerTimer -->|Đối soát sửa rx/rc/member_count| MongoStorage

    %% Luồng sweeper
    M10Reconcile -.->|Doc còn cờ quá 15s| MongoStorage
    M10Reconcile -.->|Phát bù từ doc hiện tại| NATSEvt
```

---

## 3. Mục lục 10 Module Độc lập

Mỗi module dưới đây là một tập tài liệu đặc tả hoàn chỉnh, đi sâu vào từng trường hợp sử dụng cụ thể:

| # | Tài liệu | Tính năng nghiệp vụ trọng tâm | Điểm cốt lõi về Cơ chế & Kỹ thuật |
|---|---|---|---|
| **01** | [01. Rooms, DMs & Channels](./01-feature-rooms-dms-and-channels.md) | Mở chat 1-1, tạo nhóm chat, kênh phát sóng ~200K | Sổ nhận chỗ `room_dms` `$setOnInsert` triệt tiêu race condition tạo trùng phòng DM; băm 1024 soft slots; bỏ retry nội bộ. |
| **02** | [02. Message Sending & Ordering](./02-feature-message-sending-and-ordering.md) | Gửi tin nhắn văn bản, tệp, metadata; thứ tự timeline | Goroutine per-room actor độc quyền cấp monotonic seq; dedupe 2 tầng (LRU -> Redis), bản trùng lọt được `view.CollapseRetried` ẩn khi đọc; Fast-ack sau `w:majority`. |
| **03** | [03. Message Edit, Delete & Privacy](./03-feature-message-edit-delete-and-privacy.md) | Sửa tin, xem lịch sử sửa, thu hồi, ẩn tin, xoá lịch sử | Fact/Projection pattern (`message_edits` -> `messages`); Optimistic CAS `base_ver`; Sparse fact filtering phía người đọc. |
| **04** | [04. Reactions, Pins & Bookmarks](./04-feature-reactions-pins-and-bookmarks.md) | Thả reaction, ghim tin nhắn, lưu trữ bookmark | Tập `(target, user)` với Tombstone; Survivor timers (Phiếu hẹn sinh tồn D111) tự sửa sai biến đếm; Pin folding CAS hạn mức 50 pins. |
| **05** | [05. Membership & Roles](./05-feature-membership-roles-and-succession.md) | Thêm/xoá thành viên, rời phòng, chuyển giao owner | BulkWrite upsert kèm `request_id`; Transaction Mongo duy nhất cho kế nhiệm Owner; NATS `memberwatch` xoá cache quyền Core. |
| **06** | [06. Threads, Replies & Mentions](./06-feature-threads-replies-and-mentions.md) | Trả lời trích dẫn, đếm reply, tag @mention, inbox nhắc tên | Trả lời trên timeline chính qua `messages.rp {th, s}`, doc trả lời trong `message_interactions`, `reply_preview` tính lúc đọc; Biến đếm `rc` `$inc` + survivor timer (projection `reply_index`); Mention App (nhúng trong core, D162–D176) lập chỉ mục mention từ event tự đủ, `ListMentions`, nhóm, gợi ý `@nhóm`, thông báo `CHATIM_NOTIFY`. |
| **07** | [07. Read Receipts & Unread Badge](./07-feature-read-receipts-and-unread-badge.md) | Đánh dấu đã đọc/chưa đọc, badge số tin chưa đọc | Cập nhật vị trí đọc trên doc Member (`read_seq`, `read_ver`); Unread tính lúc đọc trong `ListMyRooms` từ đuôi phòng ≤ 300 tin, dừng ở 100, `approx` khi hết cửa sổ. |
| **08** | [08. History Retrieval & Reconnect Sync](./08-feature-history-retrieval-and-sync.md) | Cuộn xem lịch sử tin nhắn, khôi phục sau mất mạng | Phân trang theo `seq` (`LATEST`/`OLDEST`/`BEFORE`/`AFTER`/`AROUND`); Dynamic View pipeline che giấu tin nhạy cảm; reconnect kéo snapshot (`ListMyRooms` + `GetHistory AFTER`), sync token thuộc Mention App (chưa có). |
| **09** | [09. Realtime Delivery & Ephemeral](./09-feature-realtime-delivery-and-ephemeral.md) | Đẩy tin realtime, báo đang gõ, trạng thái online | (M4, chưa xây; core đã RePublish event sang `live.*`) Dynamic interest routing trên NATS PubSub; Bỏ qua DB cho tín hiệu tạm thời (typing, presence); Zero-alloc fanout WebSocket `gws`. |
| **10** | [10. Data Reconciliation & Self-Healing](./10-feature-data-reconciliation-and-repair.md) | Bù đắp event rớt mạng, tự sửa sai bộ đếm, resync | Cờ `_state` ghi cùng lệnh, xoá sau PubAck; sweeper slot 0 phát bù cờ quá 15s; một consumer `projections` (slot 0, 3 handler) không delay; phiếu hẹn `CHATIM_TIMER`; CLI `/app resync` bật lại cờ. |

---

## 4. Ma trận Đối chiếu: Feature ↔ Khung Lớp Dữ liệu ↔ Hạ tầng

Để đảm bảo tính nhất quán trên toàn bộ hệ thống, mọi tính năng đều phải tuân thủ nghiêm ngặt **Khung lớp dữ liệu (Data Class Framework - §4)**:

| Tính năng | Lớp dữ liệu quy định | Cách ghi dữ liệu | Cách phát Event | Cơ chế sửa sai khi crash |
|---|---|---|---|---|
| **Tạo tin nhắn** | Fact bất biến | Insert unique theo `_id` room│thread│seq (CAS); cid chống trùng ở LRU + Redis | Tức thời `msg_created` (idempotent id) | Cờ `_state` bit 1 + sweeper phát bù |
| **Sửa / Thu hồi tin** | Fact bất biến + Projection | Insert `message_edits`, update CAS `messages` | `msg_edited`/`msg_deleted` mang snapshot | Sweeper áp lại Projection rồi phát |
| **Thả Reaction / Bookmark** | Tập `(target, user)` | Update `ver` trên `_id: {target, user}` | `reaction_changed`, `bookmark_changed` | Sweeper phát lại doc hiện tại |
| **Ghim tin nhắn** | Fact bất biến + Projection | Insert `pin_actions`, fold CAS `rooms.pins` | `msg_pinned`, `msg_unpinned` | Sweeper fold + CAS lại rồi phát |
| **Bộ đếm tin nhắn (rx, rc)** | Aggregate theo target | `$inc` tức thời + Phiếu hẹn sinh tồn | `counts_changed` mang version | Phiếu hẹn `CHATIM_TIMER` → `count_repair` đếm lại; event qua cờ bit 2/4 |
| **Thành viên nhóm** | Tập `(room, user)` | BulkWrite upsert `$cond` kèm `request_id` | `member_added`, `member_removed` | Sweeper phát lại doc member |
| **Số lượng thành viên** | Aggregate theo target | `$inc` theo doc đổi + Phiếu hẹn | `member_count_changed` | Phiếu hẹn → `member_count_repair`; event qua cờ bit 2 |
| **Vị trí đã đọc** | Vị trí đọc | Update `read_seq`, tăng `read_ver` | `read_updated` không coalesce | Sweeper phát lại vị trí mới nhất |
| **Ẩn tin / Xoá lịch sử** | Giá trị theo người đọc | Ghi fact thưa `hidden`, update `cleared_at` | `message_hidden`, `history_cleared` | Sweeper phát lại trạng thái hiện tại |
| **Sổ nhận chỗ DM** | Sổ nhận chỗ | Upsert `$setOnInsert` theo cặp User | Không event riêng (thuộc room/member) | Lần mở phòng sau hoàn tất bước dở |
| **Typing & Presence** | Ephemeral (Tạm thời) | **Không ghi cơ sở dữ liệu** | Đi thẳng qua NATS WebSocket | Mất khi rớt mạng, không cần phục hồi |

---

## 5. Hướng dẫn Đọc tài liệu theo Vai trò

- **Dành cho Product Owner / Business Analyst:**
  Đọc phần **1. Các Use Case nghiệp vụ** và **4. Hạn chế kỹ thuật & Đánh đổi** của từng module để hiểu rõ hệ thống hỗ trợ được những gì, giới hạn dung lượng ra sao và tại sao các tính năng như "Unread đếm 99+" hay "Kênh 200K chỉ admin post" lại được thiết kế như vậy.

- **Dành cho Frontend / Client SDK Developers:**
  Đọc kỹ **Module 02 (Gửi tin & Chống trùng CID)**, **Module 07 (Vị trí đọc & Unread)**, **Module 08 (Đọc lịch sử & Reconnect Token)** và **Module 09 (WebSocket Realtime)** để nắm bắt chính xác hợp đồng giao tiếp, mã lỗi gRPC, cơ chế client retry và cách hiển thị snapshot.

- **Dành cho Backend / Core Developers:**
  Đọc toàn bộ 10 module, đặc biệt chú ý phần **3. Cơ chế bảo đảm tính đúng đắn** và **4. Kỹ thuật triển khai**. Mọi thay đổi logic hay thêm tính năng mới bắt buộc phải đối chiếu với nguyên tắc: *Một ghi đơn doc atomic, không retry nội bộ, dùng phiếu hẹn sinh tồn cho dữ liệu phái sinh*.

- **Dành cho SRE / DevOps / Infrastructure Engineers:**
  Tập trung vào phần **5. Phụ thuộc kỹ thuật & Bán kính sự cố** của các module và đọc toàn bộ **Module 10 (Data Reconciliation & Self-Healing)** để hiểu rõ cách thức hệ thống tự chữa lành, cách phân bổ 1024 slot, ngưỡng cảnh báo Redis/Mongo/NATS và quy trình vận hành công cụ `/app resync` khi có sự cố hạ tầng.

---

## 6. Ma trận Mã lỗi Hệ thống chung (System-Wide Error Codes Matrix)

Để đồng bộ hành vi xử lý ngoại lệ trên toàn bộ các nền tảng Client (iOS, Android, Web SDK), mọi RPC gọi đến Core đều tuân thủ nguyên tắc phân loại lỗi chuẩn:

| Mã lỗi gRPC | Ý nghĩa trong chatim | Hành vi Client SDK bắt buộc | Ví dụ tình huống |
|---|---|---|---|
| `codes.Unavailable` | Lỗi tạm thời do contention/failover | **BẮT BUỘC RETRY** kèm Exponential Backoff & Jitter (50ms–500ms). Không báo lỗi ngay cho người dùng. | Trùng `pin_ver` khi ghim, trượt CAS `info_ver` của `UpdateRoom`, xung đột transaction đổi owner, cid đang pending ở core khác, Mongo đang failover bầu Primary. |
| `codes.FailedPrecondition` | Lệch trạng thái / Phiên bản cũ | **KHÔNG RETRY MÙ**. Phải tải lại Snapshot mới nhất từ server rồi cập nhật giao diện. | Sửa tin khi `base_ver` bị trôi do người khác đã sửa trước; ghim tin thứ 51 (`PIN_LIMIT = 50`); xoá tin còn trả lời. |
| `codes.InvalidArgument` | Dữ liệu đầu vào sai quy cách | **KHÔNG RETRY**. Kiểm tra lại payload đầu vào. | Text > 16KB, CID chứa ký tự cấm, Emoji không thuộc whitelist. |
| `codes.PermissionDenied` | Vi phạm quyền hạn nghiệp vụ | **KHÔNG RETRY**. Hiển thị thông báo cấm thao tác. | Người bị kick cố gửi tin, thành viên thường cố sửa tin người khác. |
| `codes.ResourceExhausted` | Phòng đang quá tải tạm thời (`domain.ErrBusy`) | **RETRY** kèm backoff jitter, giữ nguyên `cid`. | Mailbox actor của phòng hoặc hàng flusher đầy. |
| `codes.NotFound` | Tài nguyên không tồn tại | **KHÔNG RETRY**. Đồng bộ lại giao diện. | Thả reaction vào tin nhắn không tồn tại hoặc đã bị xoá. |

---

## 7. Tiêu chuẩn Vận hành SLA / SLO & Ngưỡng Cảnh báo Nền tảng

| Mục tiêu vận hành | Chỉ số cam kết (SLO) | Metric Prometheus tương ứng | Ngưỡng Cảnh báo (Alert Rule) |
|---|---|---|---|
| **Độ trễ Ack gửi tin** | p99 ≤ 30ms | Chưa có metric trong core; đo bằng `corebench` (bench 2K: p99 ~121–131ms trên máy dev) | Chưa có alert |
| **Độ trễ phân phối Realtime** | p99 ≤ 100ms | Dự kiến `chatim_realtime_delivery_latency_seconds` (app websocket M4, chưa xây) | Dự kiến `ChatimDeliveryLatencyHigh` (chưa có) |
| **Độ trễ đọc 50 tin lịch sử** | p99 ≤ 20ms | Chưa có metric | Chưa có alert |
| **Bảo đảm không mất tin** | 100% (At-least-once) | `chatim_core_event_pending_oldest_seconds`, `chatim_core_sweep_passes_total` | `ChatimEventPendingStuck`: cờ cũ nhất > 60s kéo dài 5 phút; `ChatimSweeperIdle`: không lượt quét nào trong 10 phút |
| **Projection theo kịp** | Danh sách hội thoại ổn định ≤ ~3s (cửa sổ gom 3s; bench 2K trung vị 1,6s) | `chatim_core_projection_lag_seconds` | `ChatimProjectionLagging`: > 10s kéo dài 5 phút |
| **Cân bằng bộ đếm tự động** | Tự sửa sai trong ≤ 10s | `chatim_core_counter_repaired_total` | `ChatimCounterRepairSurge`: > 1 lần/giây kéo dài 10 phút |

