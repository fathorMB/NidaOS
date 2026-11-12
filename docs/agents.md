# Agents on NidaOS

This document is for the owner and for the persons that connect an agent, for example Claude Code or Hermes, to NidaOS (milestone M-09). The agent itself reads the same rules from the MCP server when it connects.

## 1. How an agent connects

1. In the console, open **Agents** and make an agent token. Select the rules (section 2). Copy the token: NidaOS shows it only one time.
2. Connect the agent to the MCP server of NidaOS:
   - **Over HTTP** (recommended): the MCP endpoint is `https://nidaos.local/api/v1/mcp`. For Claude Code:

     ```sh
     claude mcp add --transport http nidaos https://nidaos.local/api/v1/mcp --header "Authorization: Bearer nida_..."
     ```

     The computer of the agent must trust the NidaOS authority (`https://nidaos.local/api/v1/ca.crt`). Node programs such as Claude Code read the variable `NODE_EXTRA_CA_CERTS`: set it to the path of the file.
   - **Over stdio**: on a computer with the `nida` command, `NIDA_SERVER=https://nidaos.local NIDA_TOKEN=nida_... nida mcp`. Do not use `nida mcp` through the local socket for an agent: the socket has the `local` identity, without rules.
3. The agent calls `nidaos_status` first. The tools are the commands of the console: apps, databases, logs, metrics, updates and the approval requests.

## 2. The rules

Each agent token has one rule for each class of operation:

| Class | Operations | Default |
| --- | --- | --- |
| `read` | The state, the metrics, the logs, the lists | Can do (`allow`) |
| `apps` | Add an app, deploy, rollback, restart, settings, variables, secrets | Can do |
| `databases` | Add a database, connect an app, backups, the home network, the minor update | Can do |
| `delete` | Remove an app, a database, a backup, a repository | Asks you first (`ask`) |
| `updates` | Look for, download and install a system update, restart the computer | Asks you first |
| `secrets` | Read the password of a database (`/connection`) | Asks you first |

The values are `allow` (the agent can do it), `ask` (NidaOS keeps the request and waits for you) and `deny` (NidaOS refuses it). An agent never makes or removes tokens, changes a password or the rules, connects the GitHub App, or approves a request: these operations are always refused for an agent token.

## 3. The approval requests

When the rule is `ask`:

1. NidaOS keeps the request and answers `202` with the code `approval_required` and the approval request. The MCP tool tells the agent the id. Nothing changes yet.
2. The console shows the request on the overview ("Needs your attention") and on the Agents page. The menu shows the number of the requests.
3. When you approve, NidaOS runs the request as the agent sent it, with the identity of the agent, and keeps the response. When you deny, NidaOS runs nothing.
4. The agent reads the result with the tool `approval_get` (or `GET /api/v1/approvals/{id}`).

A request that waits more than 24 hours expires. The body of a request (for example the value of a secret) stays only in `/var/lib/nidaos/agents.json` (mode 0600) until the decision. The API never gives it.

## 4. The record

NidaOS records each change of an agent: the operations that ran, the requests that wait, the approved and the denied requests, and the refused operations. The console shows the record on the Agents page. The file is `/var/lib/nidaos/agents.json`. NidaOS keeps the last 2000 actions.
