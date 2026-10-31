---
name: milestone-keeps-its-own-scope
description: "Owner 2026-10-31 (M6 3b) - a milestone builds its own system; integrating it into another module (e.g. iam groups into Mention) waits until the system is done and never reopens that module's problems inside the milestone"
metadata:
  type: feedback
---

In the M6 part 3b brainstorm (sync iam groups into Mention, D232) the design moved group-tree handling into Mention. That pulled Mention's own problems into the iam milestone: group expansion, the `@` picker, `ListMentions`, cache staleness (60s/10 min) and the e2e phases 6 and 8. It became three sub-phases. The owner stopped it: "tôi thấy scope này sai sai ý, giờ đụng tới các bài toán của mention, lại phải sửa mấy bài toán mention nữa, scope quá to", then "chuyển tích hợp mention để sau, mention vẫn là mention. sau khi IAM xong rồi tích hợp tiếp" (D234).

**Why:** a milestone that reaches into another module's problems grows without bound and delays the system it was meant to finish; the owner wants iam complete first.

**How to apply:**
- Before detailing a design, list which modules it changes. If it changes a module outside the milestone's own system, stop and show the owner the scope first.
- Integration work goes as a separate item after the providing system is done.
- The analysis already done goes into the Decision Log for later (D234 keeps the 3b options).
- Ask questions in plain words with data examples. The owner could not answer my A/B questions about the 3b design ("chưa hiểu các đề xuất giải pháp", "không hiểu ý gì? tại sao bỏ? tại sao giữ? mục đích là gì?").

Related: [[owner-communication]], [[workflow-pipeline]], [[mention-app-role]].
