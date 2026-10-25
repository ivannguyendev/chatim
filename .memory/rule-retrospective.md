---
name: rule-retrospective
description: "Owner rule (2026-10-25) - at the end of each phase and milestone, and whenever the owner corrects me, check for new rules; store owner-confirmed rules in CLAUDE.md and their reasons in .memory; fix or delete stale ones"
metadata:
  type: feedback
---

**When:** at the close of every phase and milestone, and right after the owner corrects me.

**How:**
- List the owner's corrections and my repeated mistakes in that stretch of work.
- For each one, check whether a rule already covers it (`CLAUDE.md`, `docs/roadmap.md` §"Quy tắc viết plan", `.memory/`).
- Only a rule the owner stated or confirmed is saved, never my own assumption. The rule text goes into `CLAUDE.md` (Hard rules, Design checklist or Workflows); the reason, the owner's words and the date go into one `.memory/` file, indexed in `.memory/README.md`.
- A new rule that contradicts an old one updates the old one; a rule the code or a decision has superseded is fixed or deleted. No duplicates across files.
- The closing report always says either the rules added or changed, or "không có rule mới".

**Why:** owner 2026-10-25: "sau mỗi task lớn kiểm tra có rule gì mới thì sẽ cập nhật rule nếu cần thiết". Earlier memories drifted (e.g. the M4 notes still said an expired token closes 4001 after D202 changed it; owner decisions now live in the Decision Log and module docs, not in memory), and the owner had to remind me of rules already agreed (M2c, 2026-10-10).

Related: [[workflow-pipeline]].
