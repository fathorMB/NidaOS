---
id: LMBRAIN-KIT-FEEDBACK
schema_version: 1
updated: "2026-11-12"
lmbrain_version: 0.1.1
notes:
  - schema_version: "1"
    id: KIT-NOTE-001
    timestamp: "2026-11-12T04:34:43.546714100+01:00"
    lmbrain_version: "1.0.0"
    category: workflow
    severity: medium
    summary: "lite_milestone_status sets a milestone to done when tasks are still open."
    observed_behavior: "On 2026-11-12, lite_milestone_status(M-07, done) changed the status to done and made the code snapshot. The response showed 9 of 10 tasks open (\"done\":1, \"progress\":0.1)."
    expected_behavior: "AGENT.md says that a milestone is done when each task is done or dropped. The tool must refuse \"done\" when a task is open or blocked, and name these tasks."
    impact: "A milestone can show \"done\" on the Pulse page with open tasks, and its code snapshot is of the wrong moment. The lead found the problem only in the response."
    evidence: "The response of lite_milestone_status for M-07 on 2026-11-12T04:33 has status \"done\" with tasks T-02 to T-10 \"open\". The cause on the project side: the lead changed the git branch, and the task states stayed in an other branch."
    workaround: "Set the open tasks to done with lite_task_set, then make the snapshot again with lite_code_snapshot."
    suggested_improvement: "Refuse active->done when a task is open or blocked, or ask for a \"force\" argument with a reason."
    related_note: null
    actor: lead
---
# LMBrain Lite kit feedback

This append-only report records evidence-backed observations about LMBrain Lite itself. It is not project backlog or task state.

The lead agent maintains it through `lmbrain_feedback_record`. Share this file with the LMBrain team when requesting kit, app, or MCP improvements.
