---
name: http-between-services-plain-keepalive
description: "Owner 2026-11-02 - HTTP calls between our services are plain request/response that answer at once, reusing one kept-alive connection for call 1..N; never hold a request open waiting for changes (no long polling, no SSE)"
metadata:
  type: feedback
---

On 2026-10-31 (D227) the owner described the directory feed as "khi request cần sẽ gọi như path + query như https, nhưng ko cần tạo connection mới" and "không phải 2 chiều"; part 3a still built a long poll that held each request until a change or `wait`. On 2026-11-02, after the feed was dropped from M6 PM, the owner clarified: "không dùng SSE cho Feed HTTP, theo mô tả rất giống SSE. ý tôi là như http, gọi là trả kết quả, không chờ đợi, tuy nhiên lần 1 và lần 2 và lần 3 và N lần đều dùng chung 1 connection để request được không?"

**Why:** a held request looks like SSE to the owner; they want the simplest HTTP shape, with the connection reuse giving the efficiency.

**How to apply:** any service-to-service HTTP (a future directory feed for Mention 3b, M8, M9, revocation sync) answers immediately with the data and a `next` position; the caller polls at its own interval over one kept-alive connection (Go default transport, body fully read and closed). Do not propose long polling or SSE; state the trade-off as "change arrives within one poll interval". `pkg/identitysync` already works this way (2s poll, 304 when unchanged). Related: [[identity-store-on-mongodb]], [[cache-follows-db]].
