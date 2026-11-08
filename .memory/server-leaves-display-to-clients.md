---
name: server-leaves-display-to-clients
description: The server serves data and facts; it does not decide how an app shows them (open vs download, inline vs attachment) unless web/app/clients ask for it
metadata:
  type: feedback
---
The server serves data and facts (types, sizes, flags such as `expired`) and does not force client behaviour such as showing versus downloading a file; apps and web decide. Client docs may still describe how an app shows the data. Exception: a behaviour that web, app or other clients ask the server to provide.
**Why:** owner 2026-11-08 removed forced `response-content-disposition` on download URLs as the server deciding for apps; file safety comes from serving files on a separate domain.
**How to apply:** when a design adds a header, flag or rule that changes what a client does with data, ask whether it is a fact or a decision; keep facts, drop decisions unless a client asked for them, or ask the owner.
