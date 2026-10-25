---
name: server-spec-client-owns-behavior
description: "Owner 2026-10-24: chatim repo is server-only; server specifies events and mechanisms fully and blocks misbehaving clients; client behaviour (reconnect waits, SDK logic) is the client repo's job, never ask the owner about it"
metadata:
  type: feedback
---

The chatim repo is server-only, and the client SDK (ReactJS) lives in another repo. The server publishes a full specification of its events, frames, close codes and mechanisms, and enforces them: rate limits, caps and per-message errors. A client that implements the spec wrongly gets blocked. How long a client waits before reconnecting, how it renews tokens, which commands it resends, and how it handles `resync` belong to the client repo. Write them in the spec as the expected client behaviour, and never bring them to the owner as questions.

The owner also prefers keeping connections open over disconnecting. A token that is invalid or expired puts the connection in a "waiting for login" state for 3 minutes (`WS_AUTH_GRACE`), with events stopped. A bad frame gets a per-message error. Rate limiting answers `retry_later` and keeps the socket open. Only a frame over the size limit, a server panic, a dead connection, node shutdown, or the per-user cap closes the socket. Alerts are Prometheus rules over `/metrics`, not NATS, and they stay.

**Why:** On 2026-10-24 the owner asked "ủa có viết sdk cho client à, sdk của client dùng reactjs mà?" and "dùng sai là chuyện client, server ko quản, tôi cũng ko muốn trả lời câu hỏi như thế này". They also wanted `pkg/wsclient` removed from the repo as an SDK, keeping only a minimal test client.

**How to apply:**
- Do not design or ask about client-side logic. The part 5 SDK `Session` was dropped.
- Keep only a test client in `tools/internal` for e2e and benches.
- Put the client contract in module 09.

Related: [[owner-communication]].
