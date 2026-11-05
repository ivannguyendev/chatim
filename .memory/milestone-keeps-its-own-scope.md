---
name: milestone-keeps-its-own-scope
description: A milestone finishes its own system first; a design that would change another module stops for a scope check, and the integration waits as a separate item (D234)
metadata:
  type: feedback
---

A milestone finishes its own system first. Integrating with another module (e.g. syncing identity groups into Mention) pulls that module's own problems into the milestone and grows it without bound (D234).

**How to apply:** before detailing a design, list the modules it changes. If one is outside the milestone's own system, stop and show the owner the scope first. The integration becomes a separate item after the providing system is done, and the analysis already made goes into the local decision log for that later item. Related: [[owner-communication]].
