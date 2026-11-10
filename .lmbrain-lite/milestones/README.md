# Milestones

Each milestone has one file. The ID of the milestone is the name of the file: `M-01.md`, `M-02.md`, and so on. These files are the source of truth for the roadmap. The app makes `../ROADMAP.md` from them.

Make milestones with the `lite_milestone_create` tool, or copy `../templates/milestone.md`.

## Frontmatter

```yaml
id: M-01
title: Short milestone title
status: proposed        # proposed | approved | active | done | dropped
priority: 1             # 1 = first
created: YYYY-MM-DD
updated: YYYY-MM-DD
```

Only the operator changes `proposed -> approved`, in the app. The agent changes `approved -> active -> done`.

## Tasks

Below the `## Tasks` heading, write one line for each task:

```
- [ ] T-01 Open task
- [x] T-02 Done task
- [!] T-03 Blocked task — the reason
```

The app shows the progress as `done / total`. A blocked task counts as open. The Pulse page shows it with a highlight.
