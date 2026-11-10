# LMBrain Lite Project Brain

This directory keeps the state of a project with one lead coding agent. The state moves with the repository, and Git keeps its versions. The Markdown files are the source of truth. The LMBrain Lite app shows them. The operator can do two actions in the app: approve a milestone and change the priorities.

**Kit version:** see `VERSION`.

## Layout

```
.lmbrain-lite/
  AGENT.md            operating contract for the lead agent
  WRITING.md          writing rules for all agents
  OPERATOR.md         guide for the human operator
  PROJECT.md          what the project is
  ROADMAP.md          milestone index that the app makes (do not edit)
  milestones/M-NN.md  one file for each milestone; the tasks are a checklist
  LOG.md              append-only log of actions (the first write makes it)
  NOTE.md             one short agent note for the Pulse page (optional)
  knowledge/          project knowledge, with wikilinks
  memory/             facts with evidence about external repositories
  skills/             Markdown procedures (the app shows them, it does not run them)
  design/             HTML mockups of the operator or the lead
  reports/            kit feedback report
  codebase/           code analysis: the area map, the snapshots and the notes of the lead
  templates/          templates for milestones, skills and prompts
  mcp/                how the app registers the MCP servers
  HARNESSES.json      optional harness manifest
```

## Quick start

1. Open the repository in LMBrain Lite and initialize the kit.
2. Start the lead agent. Give it `templates/lead-bootstrap-prompt.md`.
3. Approve its first milestone on the Roadmap page.

Read `OPERATOR.md` for the work of the operator. Read `AGENT.md` and `WRITING.md` for the rules of the agents.
