// The agents page (milestone M-09, task T-07): the agent tokens with their
// rules, the requests that wait for the owner, and the record of the
// actions. Mockup: design/console (Agents).

import { useState, type FormEvent } from 'react';
import { del, get, patch, post, type AgentAction, type Approval, type Rule, type Token } from '../api';
import { useData } from '../data';
import { ago, dateTime } from '../format';
import { useNow, usePoll } from '../live';
import { Btn, Confirm, CopyBtn, Head, Led, Mono, Note, Row, Table, Todo, useAction, type Column, type LedKind } from '../ui';

const CLASSES: [string, string, string][] = [
  ['read', 'Read', 'The state, the metrics, the logs and the lists.'],
  ['apps', 'Change the apps', 'Add, deploy, restart, settings, variables and secrets.'],
  ['databases', 'Change the databases', 'Add, connect, backups, home network.'],
  ['delete', 'Delete', 'Remove an app, a database, a backup or a repository.'],
  ['updates', 'System updates', 'Look for, download and install an update, restart the computer.'],
  ['secrets', 'Read a database password', 'The connection of the owner role of a database.'],
];
const RULES: [Rule, string][] = [
  ['allow', 'Can do'],
  ['ask', 'Asks you first'],
  ['deny', 'Cannot do'],
];
const DEFAULTS: Record<string, Rule> = { read: 'allow', apps: 'allow', databases: 'allow', delete: 'ask', updates: 'ask', secrets: 'ask' };

function rulesText(r: Record<string, Rule> | undefined, which: Rule): string {
  const names = CLASSES.filter(([k]) => (r?.[k] ?? DEFAULTS[k]) === which).map(([, t]) => t.toLowerCase());
  return names.length ? names.join(', ') : 'nothing';
}

function RulesEditor({ rules, onChange, disabled }: { rules: Record<string, Rule>; onChange: (r: Record<string, Rule>) => void; disabled?: boolean }) {
  return (
    <div className="card scroll">
      <table aria-label="Rules">
        <thead>
          <tr>
            <th>Operation</th>
            {RULES.map(([, t]) => (
              <th key={t}>{t}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {CLASSES.map(([k, t, help]) => (
            <tr key={k}>
              <td className="wrap">
                <b>{t}</b>
                <div className="muted" style={{ fontSize: 12 }}>
                  {help}
                </div>
              </td>
              {RULES.map(([v, label]) => (
                <td key={v}>
                  <input type="radio" name={`rule-${k}`} aria-label={`${t}: ${label}`} checked={(rules[k] ?? DEFAULTS[k]) === v} disabled={disabled} onChange={() => onChange({ ...rules, [k]: v })} />
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

const stateLed: Record<Approval['state'], LedKind> = { pending: 'warn', approved: 'busy', denied: 'off', done: '', failed: 'bad', expired: 'off' };
const outcomeLed: Record<AgentAction['outcome'], LedKind> = { done: '', failed: 'bad', asked: 'warn', refused: 'bad', denied: 'off' };

export function AgentsPage() {
  const d = useData();
  const now = useNow(10_000);
  const { busy, run } = useAction();
  const tokens = usePoll(() => get<{ tokens: Token[] }>('/api/v1/tokens').then((r) => r.tokens), 10_000);
  const approvals = usePoll(() => get<{ approvals: Approval[] }>('/api/v1/approvals').then((r) => r.approvals), 3000);
  const actions = usePoll(() => get<{ actions: AgentAction[] }>('/api/v1/agents/actions?limit=100').then((r) => r.actions), 5000);
  const [name, setName] = useState('');
  const [agent, setAgent] = useState('Claude Code');
  const [rules, setRules] = useState<Record<string, Rule>>({ ...DEFAULTS });
  const [secret, setSecret] = useState<{ token: Token; secret: string } | null>(null);
  const [editing, setEditing] = useState<Token | null>(null);
  const [editRules, setEditRules] = useState<Record<string, Rule>>({});
  const [remove, setRemove] = useState<Token | null>(null);
  const reload = () => {
    tokens.reload();
    approvals.reload();
    actions.reload();
    d.reload();
  };
  const agentTokens = tokens.data?.filter((t) => t.level === 'agent');
  const pending = approvals.data?.filter((a) => a.state === 'pending') ?? [];
  const decided = approvals.data?.filter((a) => a.state !== 'pending').slice(0, 20);
  const origin = typeof location !== 'undefined' ? location.origin : 'https://nidaos.local';

  if (!d.admin) return <Note>You need the admin level to manage the agents.</Note>;

  const create = (e: FormEvent) => {
    e.preventDefault();
    run(async () => {
      const r = await post<Token & { secret: string }>('/api/v1/tokens', { name: name.trim(), level: 'agent', agent: agent.trim(), rules });
      setSecret({ token: r, secret: r.secret });
      setName('');
      reload();
    });
  };

  const cols: Column<Token>[] = [
    { title: 'Token', cell: (t) => <Led kind={t.last_used && Date.now() - Date.parse(t.last_used) < 86400e3 ? '' : 'off'}><b>{t.name}</b></Led> },
    { title: 'Agent', cell: (t) => t.agent ?? '–' },
    { title: 'Can do', cell: (t) => rulesText(t.rules, 'allow'), className: 'wrap' },
    { title: 'Asks you first', cell: (t) => rulesText(t.rules, 'ask'), className: 'wrap' },
    { title: 'Last use', cell: (t) => (t.last_used ? ago(t.last_used, now) : 'never') },
    {
      title: '',
      cell: (t) => (
        <span className="inline">
          <Btn
            small
            onClick={() => {
              setEditing(t);
              setEditRules({ ...DEFAULTS, ...(t.rules ?? {}) });
            }}
          >
            Rules
          </Btn>
          <Btn small onClick={() => setRemove(t)}>
            Remove
          </Btn>
        </span>
      ),
    },
  ];

  return (
    <>
      <Head title="Agents" lead="An agent uses the same commands as this console. Each agent has a token, and the rules tell what it can do without your approval." />

      <h3>Waiting for you {pending.length > 0 && <span className="tag">{pending.length}</span>}</h3>
      {pending.length === 0 ? (
        <div className="card">
          <ul className="todo">
            <li className="muted">No agent waits for an approval.</li>
          </ul>
        </div>
      ) : (
        <Todo
          items={pending.map((a) => ({
            key: a.id,
            led: 'warn' as LedKind,
            title: `${a.agent}: ${a.summary}`,
            text: (
              <>
                Token {a.token} · {ago(a.created, now)} · <Mono>{a.method} {a.path}</Mono>
              </>
            ),
            action: (
              <span className="inline">
                <Btn small disabled={busy} onClick={() => run(() => post(`/api/v1/approvals/${a.id}/deny`), 'The request is denied.').then(reload)}>
                  Deny
                </Btn>
                <Btn small kind="primary" disabled={busy} onClick={() => run(() => post(`/api/v1/approvals/${a.id}/approve`), 'Approved. NidaOS ran the request.').then(reload)}>
                  Approve
                </Btn>
              </span>
            ),
          }))}
        />
      )}

      <h3>Agent tokens</h3>
      <Table label="Agent tokens" cols={cols} rows={agentTokens} rowKey={(t) => t.id} empty="No agent token yet. Make one below." />

      {editing && (
        <>
          <h3>
            Rules of {editing.name}{' '}
            <Btn small onClick={() => setEditing(null)}>
              Close
            </Btn>
          </h3>
          <RulesEditor rules={editRules} onChange={setEditRules} />
          <div className="inline" style={{ marginTop: 10 }}>
            <Btn
              kind="primary"
              disabled={busy}
              onClick={() =>
                run(() => patch(`/api/v1/tokens/${editing.id}`, { rules: editRules }), 'The rules changed. They apply at once.').then(() => {
                  setEditing(null);
                  reload();
                })
              }
            >
              Save the rules
            </Btn>
          </div>
        </>
      )}

      <h3>Make an agent token</h3>
      {secret ? (
        <div className="card form">
          <Row label="Token" help="Copy it now: NidaOS does not show it again.">
            <div className="inline">
              <span className="secret" data-testid="agent-secret">{secret.secret}</span>
              <CopyBtn text={secret.secret} />
            </div>
          </Row>
          <Row label="Claude Code" help="The computer of the agent must trust the NidaOS authority (/api/v1/ca.crt). For Node programs, set NODE_EXTRA_CA_CERTS to the file.">
            <pre className="secret" style={{ whiteSpace: 'pre-wrap' }}>
              claude mcp add --transport http nidaos {origin}/api/v1/mcp --header "Authorization: Bearer {secret.secret}"
            </pre>
          </Row>
          <Row label="">
            <Btn onClick={() => setSecret(null)}>Done</Btn>
          </Row>
        </div>
      ) : (
        <form className="card form" onSubmit={create}>
          <Row label="Token name" htmlFor="token-name" help="For example the computer of the agent: laptop.">
            <input id="token-name" type="text" className="mid" value={name} onChange={(e) => setName(e.target.value)} required />
          </Row>
          <Row label="Agent" htmlFor="agent-name">
            <input id="agent-name" type="text" className="mid" value={agent} onChange={(e) => setAgent(e.target.value)} />
          </Row>
          <Row label="Rules">
            <RulesEditor rules={rules} onChange={setRules} />
            <div className="help">An agent never makes tokens, changes the rules or approves a request.</div>
          </Row>
          <Row label="">
            <Btn type="submit" kind="primary" disabled={busy || !name.trim()}>
              Make the token
            </Btn>
          </Row>
        </form>
      )}

      <h3>Record of the actions</h3>
      <Table
        label="Record of the actions"
        cols={[
          { title: 'When', cell: (a: AgentAction) => dateTime(a.time) },
          { title: 'Token', cell: (a) => `${a.token} · ${a.agent}` },
          { title: 'Operation', cell: (a) => <Mono>{a.method} {a.path}</Mono>, className: 'wrap' },
          { title: 'Result', cell: (a) => <Led kind={outcomeLed[a.outcome]}>{a.outcome}{a.status ? ` · ${a.status}` : ''}</Led> },
        ]}
        rows={actions.data}
        rowKey={(a) => `${a.time}-${a.path}-${a.outcome}`}
        empty="No agent changed something yet."
      />

      {decided && decided.length > 0 && (
        <>
          <h3>Decided requests</h3>
          <Table
            label="Decided requests"
            cols={[
              { title: 'Request', cell: (a: Approval) => `${a.agent}: ${a.summary}`, className: 'wrap' },
              { title: 'State', cell: (a) => <Led kind={stateLed[a.state]}>{a.state}{a.status ? ` · ${a.status}` : ''}</Led> },
              { title: 'Decided', cell: (a) => (a.decided ? ago(a.decided, now) : '–') },
            ]}
            rows={decided}
            rowKey={(a) => a.id}
          />
        </>
      )}

      <Confirm
        open={remove !== null}
        title={`Remove the token ${remove?.name}`}
        action="Remove"
        danger
        busy={busy}
        onCancel={() => setRemove(null)}
        onConfirm={() =>
          remove &&
          run(() => del(`/api/v1/tokens/${remove.id}`), 'The token is removed. It stops at once.').then(() => {
            setRemove(null);
            reload();
          })
        }
      >
        <p>The agent with this token cannot connect again. Its record stays.</p>
      </Confirm>
    </>
  );
}
