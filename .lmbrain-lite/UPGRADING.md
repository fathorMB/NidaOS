# Upgrading the kit

The file `.lmbrain-lite/VERSION` of your project gives the version of its kit. The About page of the app shows that version and the version of the kit in the app.

When the kit of your project is older than the kit in the app, Pulse offers an assisted upgrade. An update of the app does not migrate your projects automatically.

## Upgrade from Pulse

1. Read the `CHANGELOG.md` of the kit in the app. Read the versions between your version and the version of the app.
2. Open the project in the app and examine the **Project kit** card on Pulse. The card shows:
   - the versions;
   - the number of files to replace;
   - the files with local changes;
   - the files that the app cannot compare with a recorded baseline or with a released kit.

   The app compares each file with the baseline and with the copies of the released kits. It ignores the line endings.

3. If the plan has no changed files and no files without a baseline, select **Migrate now**, then **Yes, migrate the kit**.
   - The app prepares the new kit before it replaces the live directory.
   - It keeps the old kit in `.lmbrain-lite-backup-<timestamp>` in the same directory.
   - After the upgrade, it updates `VERSION` and the baseline.
4. In these conditions, select **Copy migration prompt** and paste it into the agent in your terminal:
   - files have local changes;
   - files have no baseline;
   - the app cannot make the preview.

   The agent compares the files and keeps your changes. Do not think that a file without a baseline is safe to replace.

The app migration keeps the state that the project owns. If the kit of the project is newer than the kit in the app, update the app. Do not downgrade the kit. If the app cannot read a version, use the migration prompt to examine the problem before you change it.

## Upgrade to 1.0.0

Version 1.0.0 removes the sub-agent dispatch of the app. The migration deletes these paths from the kit of the project:

- the directory `subagents/`, with the request files;
- `templates/subagent-brief.md` and `templates/carve-out-prompt.md`.

The migration adds a line to the log. Git and the backup keep the files.

If a request in `subagents/` has the state `dispatched`, the migration stops and gives the ID of the request. Complete the review of that work. Then set `state: closed` in the file of the request, or close the request in the app version that dispatched it. Then migrate again.

Version 1.0.0 also removes the Sessions page, the Browser page and the `diorama` host. If `HARNESSES.json` declares `diorama`, or `browser_mcp` with `mode: app-hosted`, the Environment page shows an error that names the section to delete. The app does not change `HARNESSES.json`. Edit the file.

Version 1.0.0 supports two hosts: Claude Code and Codex.

- Codex is a host again. The app writes `.codex/config.toml` at the root of the workspace. In `HARNESSES.json`, the host `codex` is valid again.
- Antigravity is not a host. The app does not write the `lmbrain-lite` entry in the Antigravity configuration. The app does not delete an entry that an earlier version wrote. You can remove the entry manually: open `~/.gemini/antigravity/mcp_config.json` and `~/.gemini/config/mcp_config.json`, and delete `lmbrain-lite` from `mcpServers`.

## Manual procedure

Use these steps to upgrade the kit manually or with an agent. First, make a backup of the kit of the project.

1. Read `CHANGELOG.md` in the kit of the app. Read the versions between your version and the version of the app.
2. Copy the kit files that you did not change over the copies in your project:
   `AGENT.md`, `WRITING.md`, `OPERATOR.md`, `README.md`, `UPGRADING.md`, `CHANGELOG.md`, `templates/*`, `mcp/README.md`, `milestones/README.md`, `knowledge/README.md`, `skills/README.md`, `design/README.md`, `reports/README.md`, `memory/README.md`.

   The kit owns `memory/README.md`. The project owns each memory record in `memory/projects/`. Keep these records without changes.

3. Do not replace these files: `PROJECT.md`, `milestones/M-*.md`, `LOG.md`, `NOTE.md`, the pages in `knowledge/`, the procedures in `skills/`, `reports/lmbrain-kit-feedback.md`, `HARNESSES.json`.
4. For 1.0.0 or later, delete `subagents/`, `templates/subagent-brief.md` and `templates/carve-out-prompt.md`. First read *Upgrade to 1.0.0*.
5. Set `VERSION` to the version of the kit in the app.
6. Open the workspace in the app. If a file is missing, Pulse shows a diagnostic.

A manual upgrade does not write `.kit-baseline.json`, and you do not have to write it. The next preview compares each file with the copies of the released kits. Thus a file that you copied from a released kit does not show as a local change.

The kit of the app is in the installation directory of the app, in `kit/.lmbrain-lite/`. On Windows, for the installer for one user: `%LOCALAPPDATA%\Programs\LMBrain Lite\kit\.lmbrain-lite\`.

### The code analysis

Version 1.0.0 adds the directory `codebase/`. The migration makes `codebase/areas.json` with an empty area map. It does not change an area map that is there.

The first snapshot is made when the lead calls `lite_code_snapshot`, or when a milestone goes to `done`. No more steps are necessary.

If the project has its own script for code statistics, do these steps to replace it:

1. Move the area list of the script into the `areas` key of `codebase/areas.json`. Each area keeps its `id`, `name`, `description` and `patterns`.
2. Put the folders of experiments in `categories.spike`. Put the build and support files that are not in `scripts/`, `tools/` or `.github/` in `categories.tooling`.
3. Record the figures that only the project can compute with `lite_code_metric_set`. An example is the number of chapters of a manual.
4. Call `lite_code_snapshot` with `rebuild_history: true`. The tool makes one snapshot for each milestone that has a `commit` line in the log. A milestone without a `commit` line is listed in the result as skipped.
5. Compare the totals with the totals of the script. Then delete the script and its data files.

Small differences are usual, because the line counters are different:

- The kit counts more languages than a list of file extensions.
- The kit counts the Markdown text as comment lines, not as code lines.
- The kit also counts the new files that git does not track.
- The kit reads the manifests in all the directories. Thus the dependency totals include the experiments.
