# Operator Guide

This guide is the start point for the human operator of LMBrain Lite in a project repository.

LMBrain Lite is for projects with **one lead agent**. The lead can give the simpler tasks to sub-agents of its own host. You talk to the lead in your terminal. The app shows the status of the project. You keep one control: **you approve milestones**.

## First use in a new repository

1. Open the repository in LMBrain Lite and select **Initialize kit**. The app copies `.lmbrain-lite/` into the repository. It registers the `lmbrain-lite-mcp` tools for the two supported hosts: in `.mcp.json` for Claude Code, and in `.codex/config.toml` for Codex.
2. Start the lead agent: open a terminal at the root of the repository and run `claude` or `codex`. The app does not start agents.
3. Give it `templates/lead-bootstrap-prompt.md`.
4. Read its report. The lead writes `PROJECT.md` for this project and proposes the first milestone.
5. Open **Roadmap** and approve the milestone. Now the agent can start the work.

## You need a feature, a fix, or a change

1. Tell the agent in simple words.
2. The agent makes a milestone with tasks (status `proposed`) and tells you the ID.
3. Approve it on the **Roadmap** page. You can also change the priorities there.
4. The agent does the work. Monitor **Pulse**: the progress bar, the blocked tasks, the last log lines, and the agent note.

To change a task, a priority, or the scope, tell the agent. The app does not edit tasks. When only one actor changes the files, there are no conflicts.

## The lead delegates tasks

The lead can give the simpler tasks of an approved milestone to a sub-agent of its host. The sub-agent uses a less powerful model: Sonnet when the lead uses Opus on Claude Code, Terra when the lead uses Sol on Codex. The app does not start or show these sub-agents.

The lead examines the result before it sets the task. In the log, the lines of a sub-agent have `by: sub:<name>`. Sub-agents never change milestones, tasks, or the note.

## How to read the status

- **Pulse**: the active milestone, the progress, the blocked tasks, the last log lines, the agent note, and the diagnostics.
- **Roadmap**: all milestones with their tasks. Approve and change the order here.
- **Wiki**: `knowledge/`, the milestone files, the skills, and the root documents.
- **Project Memory**: read-only records about other repositories that the lead examined.
  - Say "remember this repository" to record one.
  - During approved work, the lead can record an important fact with evidence. It never changes that external repository.
  - On a different machine, the local paths can be unavailable.
- **Code analysis**: the size of the code, its areas, its growth through the milestones, and its risk. The lead keeps the data in `codebase/`, and each completed milestone adds one snapshot.
- **Environment**: the toolchain, the Code Index and the harness manifest. Each tool has a tile: Claude Code, Codex, tokensave, git and the manifest. A tile that is not ready tells what makes it ready.

All the files are Markdown in `.lmbrain-lite/`. Commit them with the repository. Then the status moves with the code.

## Code Index

The app indexes each workspace with **tokensave**. The agents then find code and brain documents through the `tokensave` MCP server, and they read fewer files.

- The Code Index is on by default. To turn it off for one workspace, open the **tokensave** tile on **Environment**. To turn it off for all workspaces, use **Settings → General**.
- The app uses the tokensave that is installed on this machine. It looks for it in this order: the path in **Settings → General**, `PATH`, then the usual install locations.
- If the app cannot find tokensave, the card shows the install commands for your platform. Install it, then select **Check again**. The app does not run installers.
- The app builds the index in the background when you open the workspace. It never runs `tokensave install`, and it does not change your global tokensave configuration.
- The index includes `knowledge/`, `milestones/`, `memory/`, `skills/`, `design/` and `PROJECT.md`. It does not include the log, the roadmap or the templates.
- When you turn the Code Index off, the app removes only the `.mcp.json` entry that it wrote. The `.tokensave/` directory stays.
- If you wrote your own `tokensave` entry in `.mcp.json`, the app does not change it.

## Writing rules

The agents talk to you in Italian or English. All the text that they write in the brain, the code and its comments, the documentation, and the commits is in ASD-STE100 Simplified Technical English. `WRITING.md` gives the rules. When an agent writes text that does not obey the rules, the `lite_*` tools warn the agent. The agent then corrects the text.

## Agent hosts

The app supports two hosts: Claude Code and Codex. The app does not start agents. You start the lead in your own terminal: run `claude` or `codex` at the root of the repository.

The app registers the `lite_*` tools for the two hosts each time you open the workspace. Claude Code reads `.mcp.json`. Codex reads `.codex/config.toml`. Codex reads that file only in a directory that it trusts. Thus the app adds a trust entry for the workspace to `~/.codex/config.toml` when the entry is missing. The app does not change other content of these files.

The **MCP** chip in the top bar shows the state of this registration and the hosts that are connected now. `MCP · 2 connected` means that two hosts use the tools. `MCP idle` means that no host is connected. `MCP degraded` or `MCP missing` means that the registration has a problem. Put the pointer on the chip to read the reason. Click the chip to open the MCP page.

The MCP page shows a live map: one node for each host, linked to the `lmbrain-lite` sidecar. A link pulses while a sidecar of that host runs. A recent tool call moves along the link. An amber host has a problem, and the node gives the reason. Below the map, the page shows the tools that the agent sees, with their parameters. It also shows the other MCP servers of the workspace and the approval state of `HARNESSES.json`.

**Settings → Harnesses** examines the Claude Code and the Codex that are installed on this machine. When you confirm, the app can run the updater of a host: `claude update` or `codex update`. The app does not install a host.

**Settings → About** shows the versions of the app, of the project kit and of the bundled kit. It also shows the kit history from `CHANGELOG.md`. The releases that the project kit does not have are open and marked.

## Safety model

- The app does not start agents. You start the lead in your own terminal.
- The app edits milestone files only for the approve action and the order action.
- The app makes `ROADMAP.md`. Do not edit it: the next tool call writes it again.
- Agent hosts manage their own authentication and payments. LMBrain Lite keeps no credentials in the kit.
