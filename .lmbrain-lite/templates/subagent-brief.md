# Sub-agent brief

This is the content of a `lite_subagent_request`. The sub-agent sees this brief and nothing more. It does not see your chat, your context, or the intent of the operator. Write it for a capable person who does not know the project.

Write the brief in ASD-STE100 Simplified Technical English (see `../WRITING.md`).

## Fields

- **task**: the `M-NN/T-NN` (or `M-NN`) that the job is for. It must exist on the roadmap.
- **title**: one line. The app makes the session label `sub:<name>` from it.
- **tier**: the type of work, not a model.
  - `implementation`: a part of a feature with a small surface. It must not depend much on work that is not complete.
  - `mechanical`: a rename, a migration, a format change, a port, or a known pattern in many files.
  - `research`: find information and report it. No code changes.
- **files**: the paths, relative to the workspace, that the sub-agent can change. Leave the list empty only when the brief gives the limits of the scope.
- **done_when**: the exit criterion that the sub-agent can check itself. Examples: a command that must pass, a new test that must pass, or a result that it can see.

## Rework

For a rework, give `rework_of: SA-NN`. In `brief`, write only the review feedback: what is wrong, what must change, and what stays. The request gets all other fields from the first request. After the request, examine `route` and `context_reused` in `lite_subagent_list`. A new session (`context_reused: false`) got only the first brief and the log.

## The brief

Write these items in this sequence:

1. **What to make or find.** Give the result in one paragraph.
2. **What is already decided.** Give the contracts, the names, the formats and the reasons. Then the sub-agent does not decide them again.
3. **What not to change.** Give the files, the behaviors and the public interfaces.
4. **How to work.**
   - Read and change the project state only with the `lite_*` tools. Do not edit files in `.lmbrain-lite/`. In a git worktree, that directory is an old copy, and the tools write to the main tree.
   - Log with `lite_log` and `by: sub:<name>` at the end, and for each decision that the lead must know.
   - Do not set tasks or the status of milestones. Do not request sub-agents.
   - Obey `WRITING.md`: write code, comments and log lines in ASD-STE100.
   - In a worktree, the `tokensave` tools answer from the index of the main tree. Read the files that you change directly.
5. **How to report.** Send one message: what you changed, what you verified, and what is not complete.

## Example

```
task: M-02/T-03
title: Refresh-token rotation
tier: implementation
files: [src/auth/refresh.ts, src/auth/refresh.test.ts]
done_when: `pnpm test` passes. refresh.test.ts has a test that refuses a token that was used before.
brief: |
  Replace the refresh token each time the client uses it. The session cookie
  keeps its current format (see knowledge/auth.md). Change only the token
  store. Decided: a token is an opaque value of 32 bytes. The store keeps
  its hash. Each session has one active token.
  Do not change the login handler or the cookie helpers.
  ...
```
