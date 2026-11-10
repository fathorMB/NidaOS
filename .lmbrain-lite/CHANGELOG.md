# Kit changelog

The kit version in `VERSION` always matches the LMBrain Lite app that bundles it.

## 1.0.0 — 2026-11-09

- `AGENT.md`: the **Sub-agents** section is replaced by a short **Delegation** section. The lead gives the simpler tasks of an approved milestone to a less powerful model of its own host, with the sub-agent mechanism of the host: Sonnet when the lead uses Opus on Claude Code, Terra when the lead uses Sol on Codex. The lead examines the result before it sets the task. The lead does not request sub-agents through the operator (#29).
- `AGENT.md`: the **Worktrees** section describes only the worktrees that the operator makes on the Repository page (#29, #30).
- `OPERATOR.md`: the app does not start agents. The operator starts the lead in a terminal with `claude`. The dispatch section, the Sessions page, the Browser page, Ollama and Diorama are removed. The operator keeps one control: the approval of milestones (#29, #30).
- `mcp/README.md`: `lite_subagent_request` and `lite_subagent_list` are removed, with the `LMBRAIN_SUBAGENT` paragraph. Diorama is removed (#29, #30).
- `templates/subagent-brief.md` and `templates/carve-out-prompt.md` are deleted (#29).
- `UPGRADING.md`: a new **Upgrade to 1.0.0** section. The migration deletes `subagents/` and the two templates, and it logs the deletion. It stops and names a request that is still `dispatched`. A `HARNESSES.json` that declares `diorama` or the `app-hosted` browser mode gets a named error (#29, #30).
- `WRITING.md`: the list of brain text has no sub-agent requests and briefs.
- `OPERATOR.md` and `mcp/README.md`: the supported hosts are Claude Code and Codex. The app writes `.codex/config.toml` for Codex and adds the trust entry for the workspace. Antigravity is removed. A worktree gets the two registration files (#31).
- `UPGRADING.md`: `codex` is valid again in `HARNESSES.json`. The Antigravity entry of an earlier version stays until the operator removes it (#31).
- `.gitignore`: the new `runtime/` folder is ignored. The sidecar keeps one presence file there for each connection (#32).
- `mcp/README.md`: a new **The presence file** section. `OPERATOR.md`: the **MCP** chip of the top bar (#32).
- `codebase/areas.json`: a new file that the project owns, with an empty area map. The migration adds it when it is missing and never replaces it (#35).
- `AGENT.md`: a new **Code analysis** section. The lead keeps the area map, writes the notes at each milestone close, and records the custom metrics (#35).
- `mcp/README.md`: the tools `lite_code_snapshot`, `lite_code_metric_set` and `lite_code_note_set`, and a new **The code analysis** section. `OPERATOR.md` and `README.md`: the Code analysis page and the `codebase/` directory (#35).
- `UPGRADING.md`: how a project with its own statistics script moves to the code analysis of the kit (#35).
- `AGENT.md`: a new rule 7 in **Always**, and a longer **Feedback about LMBrain Lite** section. The section tells when to record a note, how to write it, and to check before the report that each problem of LMBrain Lite has a note (#45).
- `templates/resume-milestone-prompt.md`, `dreaming-session-prompt.md`, `lead-bootstrap-prompt.md` and `takeover-prompt.md`: the agent records each problem of LMBrain Lite at once (#45).
- `mcp/README.md`: the server instructions, the `kit_feedback_rule` of the digest, and the reminder at the end of a tool error (#45).
- `design/README.md`: the operator or the lead adds the mockups. The manifest gets two optional keys: `group` for the variants of one decision, and `themes`. New sections tell how the Design page forces a light or dark theme, and give the rules for a mockup. `AGENT.md` and `README.md`: the `design/` line of the folder map (#46).
- `OPERATOR.md`: a paragraph about the live map of the MCP page. `mcp/README.md`: the presence files feed the live map (#47).
- `OPERATOR.md`: the Environment page shows a tile for each tool of the toolchain. The Code Index is turned off on the **tokensave** tile (#49).
- `skills/README.md`: write the id of a skill in the log line when you use it. The Skills page shows the log lines that name each skill (#50).
- `OPERATOR.md`: **Settings → About** shows the kit history and marks the releases that the project kit does not have (#51).
- `UPGRADING.md`: the preview knows the files of each released kit, and the line endings do not count. A manual or agent upgrade that leaves an old `.kit-baseline.json` no longer shows false local changes.

## 0.2.3 — 2026-11-09

- `WRITING.md`: new kit-owned file with the writing rules. The agents talk to the operator in Italian or English. All other text is in ASD-STE100 Simplified Technical English.
- `AGENT.md`: rule 0 makes the writing rules mandatory. A new **Code navigation** section tells the lead to use the `tokensave` tools, and not to change the index.
- `OPERATOR.md`: new **Code Index** and **Writing rules** sections.
- `mcp/README.md`: the `tokensave` server entry, its ownership rule, and `writing_warnings`.
- `templates/subagent-brief.md` and `templates/lead-bootstrap-prompt.md`: the writing rules, and the worktree note for the code index.
- All kit-owned documents are rewritten in STE. The content of the rules does not change.

## 0.2.2 — 2026-09-18

- `memory/README.md`: the new Project Memory area. Its README is kit-owned; all memory records are project-owned and survive migration.
- `AGENT.md`, `OPERATOR.md`, and `mcp/README.md`: evidence-backed project memory, lead-only mutation, sub-agent reporting, and the read-only Project Memory page.

## 0.2.1 — 2026-09-06

- `OPERATOR.md`: an **Agent hosts** section. The app launches Claude Code and Diorama, installs neither, and updates only the one that owns a self-updater; Diorama's path is a machine setting because it usually lives in a virtual environment, and `diorama doctor` refuses a start before a tab opens (#20).
- `OPERATOR.md`: the dispatch card names the host and the model a dispatch will actually use before it is confirmed, and a Diorama sub-agent always runs in a dedicated worktree, because the rule that stops it asking for its own sub-agents lives in that worktree's `.mcp.json` (#20, #17).
- `AGENT.md`: the tier may be informational — the operator can send a request to a host whose model is its own configuration — and `lite_subagent_list` reports the `host` beside the `model`, where `engine` means a local engine the app does not name. A worktree is not optional for a sub-agent on such a host, and the lead still never names a model or a host (#20).
- `mcp/README.md`: both hosts read the one workspace `.mcp.json`, so it is written once; a worktree gets its own with `--root` still on the main tree, and for a sub-agent that entry declares `LMBRAIN_SUBAGENT` (#20).

## 0.2.0 — 2026-09-05

- `UPGRADING.md`: describes the assisted Pulse migration, confirmation and backup, and the agent/manual fallback for locally edited or unverified files.
- `AGENT.md`: the sub-agent section is rewritten around the request flow. The lead asks with `lite_subagent_request`, the operator dispatches or declines in Sessions, the lead reads the outcome back with `lite_subagent_list`. Sub-agents still own no project state and cannot request sub-agents (#17).
- `templates/subagent-brief.md`: what a request must contain; `templates/carve-out-prompt.md` now routes the carved-out slice through a request instead of a chat prompt (#17).
- `AGENT.md`: a project taken over from LMBrain standard keeps `.lmbrain/` as legacy, read-only material; project state lives in `.lmbrain-lite/` and changes only through the `lite_*` tools. `templates/takeover-prompt.md` drives the conversion of the standard roadmap, specs and status through the tools, without inventing work (#23).
- `AGENT.md`: a Worktrees section. Sessions may run in a linked git worktree; project state stays in the main tree through the `lite_*` tools, and the `.lmbrain-lite/` copy inside a worktree is a stale copy never to be read or edited directly. `templates/subagent-brief.md` states the same rule for the sub-agent (#18).
- `AGENT.md`: rework goes back to the sub-agent that did the work, with `rework_of` and only the feedback in the brief; the three routes and what `context_reused: false` means for the lead. A sub-agent that logged its `report` stays open, awaiting review (#22).
- `mcp/README.md`: the two sub-agent tools (#17).
- `OPERATOR.md`, `mcp/README.md`: Claude Code is the only host the app launches. The Codex and Pi hosts and their registration files are gone (#19).

## 0.1.2 — 2026-09-05

- `templates/dreaming-session-prompt.md`: operator invitation to a bounded dreaming session, with the limits `dream_capture` assumes.
- `templates/carve-out-prompt.md`: ask the lead to carve an isolated feature out of the work in progress for a second agent, and to own the review of what comes back.
- `templates/resume-milestone-prompt.md`: the Sessions starter text now lives in the kit instead of the app.
- `AGENT.md`: the agent note supports Markdown; Pulse renders it.
- The app can now migrate a project's kit for you: Pulse offers a migration prompt and, when nothing would be lost, a one-click upgrade. `UPGRADING.md` remains the authority on which files the kit owns.

## 0.1.1 — 2026-09-04

First published kit.

- `AGENT.md`: operating contract for the lead agent (plan, work, log, block, sub-agents).
- `OPERATOR.md`: human guide. One control: approve milestones.
- `milestones/M-NN.md`: source of truth for the roadmap; tasks as a checklist (`[ ]`, `[x]`, `[!]`).
- `ROADMAP.md`: generated index, never edited by hand.
- `LOG.md`, `NOTE.md`: append-only action log and the one-line Pulse note.
- `templates/`: milestone, skill, and lead bootstrap prompt.
- `mcp/README.md`: registration and the `lite_*` tool list.
