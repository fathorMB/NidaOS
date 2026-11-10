# MCP registration

When the LMBrain Lite app opens a workspace, it registers two MCP servers **automatically**.

## The `lmbrain-lite` server

The `lmbrain-lite-mcp` server operates on this repository only.

- **Claude Code**: the app writes a `lmbrain-lite` entry in the `.mcp.json` at the root of the workspace. The entry points to the binary with `--root <workspace>`. Claude Code reads this file when you start it in the workspace.
- **Codex**: the app writes a `mcp_servers.lmbrain-lite` table in the `.codex/config.toml` at the root of the workspace, with the same command and `--root <workspace>`. Codex reads this file only in a directory that it trusts. Thus the app adds a trust entry for the workspace to the user configuration of Codex, `~/.codex/config.toml`, when the entry is missing.

The app writes the two files each time the workspace opens. It changes only its own entry. If the entry is already correct, the app does not write the file.

A git worktree that the app makes gets its own `.mcp.json` and its own `.codex/config.toml`. The `--root` in the two files points to the main tree.

The app finds the command in this order: `LMBRAIN_MCP_BIN`, a binary in the directory of the app, then `PATH`.

### The presence file

Each host starts its own `lmbrain-lite-mcp` process. While a process runs, it keeps the file `.lmbrain-lite/runtime/sidecar-<pid>.json`. The file contains the process id, the start time, the name and the version of the client, and the last tool call. The process deletes the file when the host closes the connection.

The app reads these files to show the **MCP** chip in the top bar and the live map on the MCP page. The app deletes the file of a process that does not run. The `runtime/` folder is local: git ignores it. Do not edit these files.

## The `tokensave` server (Code Index)

When the Code Index is ready, the app adds a `tokensave` entry to `.mcp.json`:

```json
"tokensave": {
  "command": "<absolute path of tokensave>",
  "args": ["serve", "--path", "<absolute path of the main tree>"],
  "env": { "TOKENSAVE_UPDATE_CHECK": "off" }
}
```

- `--path` always points to the main tree, also in a worktree.
- The app writes and removes only an entry with this exact form. It does not change a `tokensave` entry that a different tool or the operator wrote.
- When the operator turns the Code Index off, the app removes its entry.

## Tools of the `lmbrain-lite` server

| Tool | Function |
| --- | --- |
| `lite_digest` | Read this first. The summary of the project: milestones, the active milestone, open and blocked tasks, the last log lines, the note, the diagnostics and the writing rules. |
| `lite_roadmap` | All milestones with all their tasks. |
| `lite_log_read` | The newest log lines. |
| `lite_memory_list` / `_get` | Read the summaries of Project Memory, or one full record. |
| `lite_memory_remember` | Make or update a record, with evidence, about an examined external Git repository. Only for the lead. |
| `lite_memory_archive` | Archive a record and keep its history. Only for the lead. |
| `lite_milestone_create` | Make a new `proposed` milestone with tasks. |
| `lite_milestone_status` | Change the status of a milestone. Only the operator can set `approved`. |
| `lite_milestones_reorder` | Set the priorities. |
| `lite_task_add` | Add a task to a milestone. |
| `lite_task_set` | Set a task to open, done, or blocked (with a reason). |
| `lite_log` | Add one log line. |
| `lite_note_set` | Replace the note on Pulse. |
| `lite_code_snapshot` | Compute the code analysis snapshot. With `milestone`, keep it as the record of that milestone. With `rebuild_history`, recreate the history from the log. |
| `lite_code_metric_set` | Set or remove a custom metric of the code analysis. |
| `lite_code_note_set` | Write the notes of the lead for a snapshot, as a list of steps. |
| `dream_capture` | Record a dream during a session that the operator started for dreams. |
| `lmbrain_feedback_record` / `_report` / `_resolve` | The kit feedback report: record a problem of LMBrain Lite, read the report, close or reconfirm a note. |
| `harness_*` | The harness manifest: intent, approval, plan, apply, drift. |

Each tool that writes accepts `by`: `lead` (the default), `sub:<name>`, or `operator`.

The server gives instructions to the host when it starts. Claude Code and Codex show them to the agent for the whole session. They tell the agent to start with `lite_digest`, to obey the writing rules, and to record each problem of LMBrain Lite with `lmbrain_feedback_record`. The digest has the same rule in `kit_feedback_rule`.

The error of a tool ends with a reminder. If the call was correct, the error is a problem of LMBrain Lite, and the agent records it. The feedback tools do not add the reminder.

Each tool that writes text also checks the writing rules of `WRITING.md`. If the text does not obey a rule, the tool does the write and returns `writing_warnings`. Correct the text with a second write.

### The code analysis

The tools write in `.lmbrain-lite/codebase/`:

| File | Content |
| --- | --- |
| `areas.json` | The area map. The lead edits it. The kit migration never replaces it. |
| `current.json` | The latest snapshot. |
| `snapshots/M-NN.json` | The snapshot of a completed milestone. |
| `metrics.json` | The custom metrics. |
| `notes/M-NN.md`, `notes/current.md` | The notes of the lead. The frontmatter holds the steps. |

`lite_milestone_status` makes the snapshot of a milestone when it sets the milestone to `done`. Its result then has `code_snapshot`. If the snapshot fails, the milestone stays `done` and the result has `code_snapshot_warning`.

A snapshot counts the files that git tracks and the new files that git does not ignore. It has these parts:

- **Lines.** The code, comment and blank lines for each language, from the `tokei` counter. Each file has one category: `prod`, `test`, `spike`, `tooling` or `docs`. The inline test modules of a Rust file count as test code.
- **Tests.** The number of test cases, from patterns for Rust, TypeScript and JavaScript, Python, C#, Go and Java.
- **Git.** The commits for each file, the first and the last commit, and the file pairs that change in the same commits.
- **Quality.** The complexity of the functions, the health, the dead code and the references between areas. The sidecar reads them from the tokensave index and does not change the index. Without an index, the snapshot has no quality section and `quality_missing` gives the reason.
- **Dependencies.** The direct, development and total dependencies for each ecosystem, from `Cargo.toml`, `package.json`, `pyproject.toml`, `requirements*.txt`, `*.csproj`, `Directory.Packages.props`, `go.mod` and their lock files.
- **Milestones** and **custom metrics**.

The keys of `areas.json`:

| Key | Function |
| --- | --- |
| `areas` | The product areas. Each area has `id`, `name`, `description` and `patterns`. The first area with a pattern that matches gets the file. |
| `categories` | The patterns for `spike`, `tooling` and `docs`. They are added to the general rules. |
| `test_patterns` | More patterns for test files. |
| `test_counters` | More test counters. Each counter has a `glob` and a regular expression `pattern` for one line. |
| `exclude` | The files that the snapshot does not count. |
| `coupling_max_commit_files` | A commit with more files than this number is not used for the file pairs. The default is 30. |

In a pattern, `**` matches any number of directories and `*` matches a part of one name.

`rebuild_history` reads the `commit` lines of the log. Each line gives a milestone and a commit hash. The tool counts the tree of each commit again: the lines, the tests and the areas. It does not read the subjects of the commits. A snapshot of the past has no quality section, because the index shows only the current tree.

The diagnostics `code-areas-invalid`, `code-areas-unmapped`, `code-quality-missing` and `code-history-stale` show the problems of this data.

An example of a `.mcp.json` entry:

```json
{
  "mcpServers": {
    "lmbrain-lite": {
      "command": "lmbrain-lite-mcp",
      "args": ["--root", "."]
    }
  }
}
```
