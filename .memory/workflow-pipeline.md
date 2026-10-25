---
name: workflow-pipeline
description: "Owner rule - pipeline with gates (brainstorm, decision log, independent phase-split review, summary and detailed plan per phase, plan review, TDD, verify, close the phase with rule check and progress report) and the format of phase summaries and execution plans"
metadata:
  type: feedback
---

The rule lives in `CLAUDE.md` §Workflows and `docs/roadmap.md` §"Quy tắc viết plan"; this note keeps the reasons.

Pipeline: brainstorm → decision log → **phase-split review** → summary for one phase (owner approves) → detailed plan for that phase → **plan review** (owner go) → code with TDD → verify by the budget table → close the phase (summary as built, docs, [[rule-retrospective]], progress report).

**Splitting into phases** (owner 2026-10-25)
- Before any summary, an independent reviewer (an agent that never saw my reasoning) reads the decision log and proposes the phases: goal, a deliverable testable on its own, dependencies, which requirements each phase covers (the coverage table must have no empty cell). The owner approves the split; the phase list goes into the milestone row of `docs/roadmap.md`.
- A phase is a part on the shared branch `feat/<milestone>` (Git workflow); each finished phase is green and has its own e2e phase. Never send several big documents at once.
- Why: a long plan for a whole milestone must change during execution, and each change ripples into details that did not need to move; a phase isolates that. Earlier: "summary quá nhiều, chia ra đi, làm từng cái… dồn dập thế này quá nhiều" (2026-10-20); M4 ran as 5 parts + gateway.

**Phase summary** `docs/plans/<date>-<milestone>-<n>-<slug>-summary.md`
- Vietnamese, no Go code, one phase only; written before execution, approved by the owner, then rewritten to the as-built state as the phase record.
- The 12 items of the roadmap rules (glossary, diagram, field table, use-case flows with gRPC codes, correctness and races, events, libraries, decisions with the rejected alternative, cost, real risks, team choices, tests) are a checklist, not 12 long sections: about 130 lines in all ([[owner-communication]] #10), plus the coverage table.

**Detailed plan** `.claude/plans/<plan>.md` (local), one phase only
- No code blocks. Per task: goal, files, todos, technique (API, Mongo operator, index, ordering, error codes), contracts in prose, tests and what each proves, commands, done criteria, commit. Implementers write the code with TDD. Overrides the `writing-plans` "complete code" guidance and its save path.
- Why: the M2b.3 (13K lines) and M2b.4 (19K lines) plans carried full code, so every small design change rewrote dozens of code blocks; on 2026-10-09 the owner moved execution plans out of `docs/`.

**Plan review before code** (owner 2026-10-20): "khi viết plan chi tiết phần 1 xong, thì cần reviewer kiểm tra và đánh giá phù hợp enterprise thì mới code". The reviewer judges enterprise fitness and the coverage table; Critical/Important findings go into the plan, I report the verdict and code only after the owner's go.

**Test is not a step after code**: the repo uses TDD, so tests are written with the code; "verify" means the checks of CLAUDE.md "Verification and review budget". Code review follows that budget too (one reviewer for risky tasks), overriding the global "review after every implementation".

**Progress report** (owner 2026-10-25): update the milestone row in `docs/roadmap.md` first, then end the closing report with the phase just finished (with evidence), the next phase (goal, first step) and the remaining phases with their status. The chat and the roadmap say the same thing.

**Local plans and decision logs** (owner 2026-10-25): `.claude/plans/` is gitignored, so anything left only there is lost on a clean-up. When a phase closes, fold the decision log into `docs/decision-log.md` (decisions and rejected alternatives) and the plan's Minor findings into the summary or the roadmap's "Mục mang sang M5", then delete both local files; tracked docs never cite a `.claude/plans/` path. Rejected: committing the logs to `docs/decisions/` (a second record beside the Decision Log). Also rejected earlier (2026-10-25): a separate PRD document, since design §2 + Decision Log + summaries already serve as the reference.

Related: [[owner-communication]], [[rule-retrospective]].
