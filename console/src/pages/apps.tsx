// The apps (task T-04): the list, the app page with its six tabs and the
// form to add an app.

import { useMemo, useState, type FormEvent } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import {
  ApiError,
  del,
  enc,
  get,
  patch,
  post,
  put,
  type App,
  type AppConfig,
  type Database,
  type Folder,
  type Job,
  type RepoConnection,
  type TierName,
  type Version,
} from '../api';
import { useData } from '../data';
import { ago, between, bytes, dateTime, errorText, fixed, GiB, MiB, shortSha, tierLabel } from '../format';
import { useLive, useNow, usePoll } from '../live';
import { appNamespace, nsCpu, nsMemMB, series } from '../metrics';
import {
  Btn,
  Confirm,
  ErrorNote,
  Head,
  KV,
  Led,
  LinkBtn,
  LivePill,
  LogTail,
  Meter,
  Mono,
  Note,
  Radio,
  Row,
  Stats,
  Table,
  Tabs,
  Tier,
  Todo,
  useAction,
  useTab,
  useToast,
  type Column,
  type LedKind,
} from '../ui';

// ---- Shared ----

export const NAME_RE = /^[a-z][a-z0-9-]{0,31}$/;

export function appLed(a: App): LedKind {
  switch (a.state) {
    case 'running':
      return a.versions?.some((v) => v.state === 'failed' && v.number > a.active) ? 'warn' : '';
    case 'failed':
      return 'bad';
    case 'stopped':
    case 'new':
      return 'off';
    default:
      return 'busy';
  }
}

export const appStateText: Record<App['state'], string> = {
  new: 'No version yet',
  deploying: 'Deploy in progress',
  moving: 'Folder move in progress',
  running: 'In operation',
  starting: 'Starting',
  stopped: 'Stopped',
  failed: 'Not in operation',
};

const versionText: Record<Version['state'], [string, LedKind]> = {
  deploying: ['In progress', 'busy'],
  serving: ['In operation', ''],
  failed: ['Failed', 'bad'],
  replaced: ['Replaced', 'off'],
};

function activeVersion(a: App): Version | undefined {
  return a.versions?.find((v) => v.number === a.active);
}

function source(a: App, c?: RepoConnection | null): string {
  if (c) return `${c.owner}/${c.repo}`;
  return a.repository ?? '';
}

function linkedDb(dbs: Database[] | undefined, app: string): Database | undefined {
  return dbs?.find((d) => d.links?.some((l) => l.app === app));
}

// ---- The list ----

export function AppsPage() {
  const d = useData();
  const nav = useNavigate();
  const now = useNow(10_000);
  const conns = new Map((d.runners?.connections ?? []).map((c) => [c.app, c]));
  const cols: Column<App>[] = [
    {
      title: 'Name',
      cell: (a) => (
        <Led kind={appLed(a)}>
          <b>{a.name}</b>
        </Led>
      ),
    },
    { title: 'Source', cell: (a) => <Mono>{source(a, conns.get(a.name)) || <span className="muted">image only</span>}</Mono> },
    {
      title: 'Storage',
      cell: (a) => (a.folders?.length ? <Tier name={a.folders[0].tier} /> : <span className="muted">no folder</span>),
    },
    { title: 'Address', cell: (a) => (a.address ? <Mono>{a.address.replace('https://', '')}</Mono> : <span className="muted">no port</span>) },
    { title: 'State', cell: (a) => appStateText[a.state] },
    {
      title: 'Version',
      cell: (a) => {
        const v = activeVersion(a);
        if (!v) return <span className="muted">–</span>;
        return (
          <>
            <Mono>{v.commit ? shortSha(v.commit) : `v${v.number}`}</Mono> <span className="muted">· {ago(v.finished ?? v.created, now)}</span>
          </>
        );
      },
    },
  ];
  return (
    <>
      <Head title="Apps" lead="An app comes from a GitHub repository. A runner on this computer builds it and puts it in operation.">
        {d.admin && (
          <LinkBtn to="/apps/new" kind="primary">
            Add an app
          </LinkBtn>
        )}
      </Head>
      <Table label="Apps" cols={cols} rows={d.apps} rowKey={(a) => a.name} onOpen={(a) => nav(`/apps/${a.name}`)} empty="No apps yet. Add an app to start." />
    </>
  );
}

// ---- The app page ----

function useApp(name: string) {
  const app = usePoll(() => get<App>(`/api/v1/apps/${enc(name)}`), 2000, [name]);
  const repo = usePoll(
    () =>
      get<RepoConnection>(`/api/v1/apps/${enc(name)}/repository`).catch((e) => {
        if (e instanceof ApiError && e.status === 404) return null;
        throw e;
      }),
    10_000,
    [name],
  );
  const jobs = usePoll(
    () =>
      get<{ jobs: Job[] }>(`/api/v1/apps/${enc(name)}/jobs`).then(
        (r) => r.jobs,
        (e) => {
          if (e instanceof ApiError && e.status === 404) return [] as Job[];
          throw e;
        },
      ),
    5000,
    [name],
  );
  return { app, repo, jobs };
}

export function AppPage() {
  const { name = '' } = useParams();
  const { app, repo, jobs } = useApp(name);
  const [tab, href] = useTab('overview');
  const d = useData();
  const { busy, run } = useAction();
  const a = app.data;
  if (app.error && !a) {
    return (
      <>
        <Link className="back" to="/apps">
          ← Apps
        </Link>
        <Head title={name} />
        <ErrorNote error={app.error} />
      </>
    );
  }
  if (!a) return <p className="muted">Loading…</p>;
  const reload = () => {
    app.reload();
    d.reload();
  };
  const v = activeVersion(a);
  const tabs: [string, string][] = [
    ['overview', 'Overview'],
    ['deploys', 'Deploys'],
    ['variables', 'Variables and secrets'],
    ['resources', 'Resources and storage'],
    ['access', 'Access'],
    ['remove', 'Stop or remove'],
  ];
  const props = { a, reload, repo: repo.data ?? null, reloadRepo: repo.reload, jobs: jobs.data };
  return (
    <>
      <Link className="back" to="/apps">
        ← Apps
      </Link>
      <Head
        title={a.name}
        lead={
          <>
            <Led kind={appLed(a)}>{appStateText[a.state]}</Led>
            {a.address && (
              <>
                {' '}
                · <Mono>{a.address.replace('https://', '')}</Mono>
              </>
            )}
          </>
        }
      >
        <LinkBtn to={`/logs?source=${enc('pod/' + appNamespace(a.name))}`}>Open the logs</LinkBtn>
        {d.admin && (
          <>
            <Btn disabled={busy || a.active === 0} onClick={() => run(() => post(`/api/v1/apps/${enc(a.name)}/restart`), `${a.name} restarts.`).then(reload)}>
              Restart
            </Btn>
            <Btn
              kind="primary"
              disabled={busy || !v || a.state === 'deploying'}
              title="Deploy the image of the version in operation again"
              onClick={() =>
                v &&
                run(() => post(`/api/v1/apps/${enc(a.name)}/deploys`, { image: v.image, commit: v.commit, summary: 'Deploy again' }), 'The deploy starts.').then(reload)
              }
            >
              Deploy again
            </Btn>
          </>
        )}
      </Head>
      <Tabs list={tabs} current={tab} href={href} />
      {tab === 'overview' && <AppOverview {...props} />}
      {tab === 'deploys' && <AppDeploys {...props} />}
      {tab === 'variables' && <AppVariables {...props} />}
      {tab === 'resources' && <AppResources {...props} />}
      {tab === 'access' && <AppAccess {...props} />}
      {tab === 'remove' && <AppRemove {...props} />}
    </>
  );
}

interface TabProps {
  a: App;
  reload: () => void;
  repo: RepoConnection | null;
  reloadRepo: () => void;
  jobs?: Job[];
}

function AppOverview({ a, repo }: TabProps) {
  const live = useLive();
  const d = useData();
  const now = useNow(10_000);
  const ns = appNamespace(a.name);
  const last = live.history[live.history.length - 1];
  const cpu = nsCpu(ns);
  const mem = nsMemMB(ns);
  const stale = live.metrics.state === 'stopped';
  const limits = a.config.limits ?? {};
  const v = activeVersion(a);
  const db = linkedDb(d.databases, a.name);
  const failed = a.versions?.filter((x) => x.state === 'failed' && x.number > a.active).pop();
  const versions = [...(a.versions ?? [])].reverse().slice(0, 3);
  return (
    <>
      <h3 style={{ marginTop: 16 }}>
        Now <LivePill status={live.metrics} />
      </h3>
      <div className="meters">
        <Meter
          label="Processor"
          value={last ? fixed(cpu(last)) : '–'}
          unit="%"
          values={series(live.history, cpu)}
          stale={stale}
          sub={limits.cpu_millis ? `limit ${fixed(limits.cpu_millis / 1000, 2)} cores` : 'no limit'}
        />
        <Meter
          label="Memory"
          value={last ? fixed(mem(last)) : '–'}
          unit="MB"
          values={series(live.history, mem)}
          max={limits.memory_bytes ? limits.memory_bytes / MiB : undefined}
          stale={stale}
          sub={limits.memory_bytes ? `limit ${bytes(limits.memory_bytes)}` : 'no limit'}
        />
        <Meter
          label="Copies ready"
          value={a.status ? `${a.status.ready}` : '–'}
          unit={`of ${a.copies}`}
          values={[]}
          sub={a.status ? `read ${ago(a.status.observed, now)}` : 'k3s did not answer yet'}
        />
      </div>
      <LogTail source={`pod/${ns}`} n={5} />
      <h3>State</h3>
      <Stats
        items={[
          ['State', appStateText[a.state], failed ? `the last deploy failed: ${failed.reason ?? ''}` : a.config.port ? 'answers the health check' : 'no port'],
          ['Version', v ? <Mono>{v.commit ? shortSha(v.commit) : `v${v.number}`}</Mono> : '–', v ? ago(v.finished ?? v.created, now) : 'no version in operation'],
          ['Added', dateTime(a.created), `changed ${ago(a.updated, now)}`],
        ]}
      />
      {failed && (
        <Note>
          <b>Version {failed.number} failed.</b> {failed.reason} Version {a.active || 'none'} stays in operation.{' '}
          <LinkBtn small to="?tab=deploys">
            See the deploys
          </LinkBtn>
        </Note>
      )}
      <h3>Setup</h3>
      <KV
        rows={[
          [
            'Address',
            a.address ? <Mono>{a.address}</Mono> : <span className="muted">no port: the app has no address</span>,
            a.address ? (
              <a className="btn small" href={a.address} target="_blank" rel="noreferrer">
                Open
              </a>
            ) : null,
          ],
          [
            'Source',
            repo ? (
              <>
                <Mono>github.com/{repo.owner}/{repo.repo}</Mono> · branch {repo.branch}
              </>
            ) : (
              <span className="muted">not connected to GitHub</span>
            ),
            <LinkBtn small to="?tab=deploys">
              {repo ? 'Deploys' : 'Connect'}
            </LinkBtn>,
          ],
          [
            'Storage',
            a.folders?.length ? (
              a.folders.map((f) => (
                <div key={f.name}>
                  <Mono>{f.path}</Mono> · <Tier name={f.tier} /> · {bytes(f.size_bytes)}
                </div>
              ))
            ) : (
              <span className="muted">no folder that stays</span>
            ),
            <LinkBtn small to="?tab=resources">
              Change
            </LinkBtn>,
          ],
          [
            'Database',
            db ? <b>{db.name}</b> : <span className="muted">none</span>,
            db ? (
              <LinkBtn small to={`/databases/${db.name}`}>
                Open
              </LinkBtn>
            ) : (
              <LinkBtn small to="?tab=variables">
                Connect
              </LinkBtn>
            ),
          ],
          ['Name for other apps', a.config.port ? <Mono>http://{a.name}</Mono> : <span className="muted">no port</span>],
        ]}
      />
      <h3>
        Last deploys{' '}
        <LinkBtn small to="?tab=deploys">
          See all
        </LinkBtn>
      </h3>
      <VersionTable a={a} versions={versions} />
    </>
  );
}

function VersionTable({ a, versions, rollback }: { a: App; versions: Version[]; rollback?: (v: Version) => void }) {
  const now = useNow(10_000);
  const cols: Column<Version>[] = [
    { title: 'Version', cell: (v) => <Mono>{v.commit ? `${shortSha(v.commit)} · v${v.number}` : `v${v.number}`}</Mono> },
    { title: 'Change', cell: (v) => v.summary ?? (v.rollback_of ? `Rollback to version ${v.rollback_of}` : <span className="muted">–</span>), className: 'wrap' },
    { title: 'When', cell: (v) => ago(v.created, now) },
    { title: 'Time', cell: (v) => between(v.created, v.finished) },
    {
      title: 'Result',
      cell: (v) => {
        const [t, k] = versionText[v.state];
        return (
          <span title={v.reason}>
            <Led kind={k}>{v.state === 'failed' && v.reason ? `Failed: ${v.reason}` : t}</Led>
          </span>
        );
      },
      className: 'wrap',
    },
  ];
  if (rollback) {
    cols.push({
      title: '',
      cell: (v) =>
        v.state === 'replaced' ? (
          <Btn small onClick={() => rollback(v)}>
            Go back
          </Btn>
        ) : null,
    });
  }
  return <Table label={`Versions of ${a.name}`} cols={cols} rows={versions} rowKey={(v) => v.number} empty="No deploys yet." />;
}

const jobText: Record<Job['state'], [string, LedKind]> = {
  queued: ['Waiting for a runner', 'busy'],
  running: ['Running', 'busy'],
  succeeded: ['Passed', ''],
  failed: ['Failed', 'bad'],
  cancelled: ['Cancelled', 'off'],
};

export function JobTable({ jobs, showApp }: { jobs?: Job[]; showApp?: boolean }) {
  const now = useNow(5000);
  const [log, setLog] = useState<{ job: Job; text?: string; error?: unknown } | null>(null);
  const open = (job: Job) => {
    setLog({ job });
    get<string>(`/api/v1/jobs/${job.id}/log`).then(
      (text) => setLog({ job, text }),
      (error) => setLog({ job, error }),
    );
  };
  const cols: Column<Job>[] = [];
  if (showApp) cols.push({ title: 'App', cell: (j) => <Link to={`/apps/${j.app}`}>{j.app}</Link> });
  cols.push(
    { title: 'Commit', cell: (j) => <Mono>{shortSha(j.sha)}</Mono> },
    {
      title: 'State',
      cell: (j) => {
        const [t, k] = jobText[j.state];
        return (
          <span title={j.message}>
            <Led kind={k}>{j.message && j.state === 'failed' ? `${t}: ${j.message}` : t}</Led>
          </span>
        );
      },
      className: 'wrap',
    },
    { title: 'Started', cell: (j) => ago(j.started ?? j.created, now) },
    { title: 'Time', cell: (j) => (j.started ? between(j.started, j.finished ?? new Date(now).toISOString()) : '–') },
    {
      title: '',
      cell: (j) => (
        <span className="inline">
          {j.has_log && (
            <Btn small onClick={() => open(j)}>
              Open the log
            </Btn>
          )}
          <a className="btn small" href={j.url} target="_blank" rel="noreferrer">
            GitHub
          </a>
        </span>
      ),
    },
  );
  return (
    <>
      <Table label="Jobs" cols={cols} rows={jobs} rowKey={(j) => j.id} empty="No jobs yet. A push to the branch starts a job." />
      {log && (
        <>
          <h3>
            Log of the job {log.job.id} ({shortSha(log.job.sha)}){' '}
            <Btn small onClick={() => setLog(null)}>
              Close
            </Btn>
          </h3>
          <div className="card">
            {log.error ? <ErrorNote error={log.error} /> : <pre className="log">{log.text ?? 'Loading…'}</pre>}
          </div>
        </>
      )}
    </>
  );
}

function AppDeploys({ a, reload, repo, reloadRepo, jobs }: TabProps) {
  const d = useData();
  const { busy, run } = useAction();
  const [target, setTarget] = useState<Version | null>(null);
  const [image, setImage] = useState('');
  const [repoName, setRepoName] = useState('');
  const [branch, setBranch] = useState('');
  const [disconnect, setDisconnect] = useState(false);
  const versions = [...(a.versions ?? [])].reverse();
  const github = d.runners?.github;
  const connect = (e: FormEvent) => {
    e.preventDefault();
    run(
      () => put<{ connection: RepoConnection; warning?: string }>(`/api/v1/apps/${enc(a.name)}/repository`, { repository: repoName.trim(), branch: branch.trim() || undefined }),
      'The repository is connected. NidaOS opened a pull request with the workflow.',
    ).then((ok) => {
      if (ok) {
        reloadRepo();
        d.reload();
      }
    });
  };
  return (
    <>
      <p className="lead" style={{ margin: '16px 0' }}>
        {repo ? (
          <>
            A push to the branch <Mono>{repo.branch}</Mono> of <Mono>{repo.owner}/{repo.repo}</Mono> starts a deploy.{' '}
          </>
        ) : null}
        NidaOS keeps the last 5 versions, thus you can go back.
      </p>
      <VersionTable a={a} versions={versions} rollback={d.admin ? setTarget : undefined} />
      <Note kind="ok">If a new version does not answer the health check, NidaOS keeps the version before in operation.</Note>
      <Confirm
        open={target !== null}
        title={`Go back to version ${target?.number}`}
        action="Go back"
        busy={busy}
        onCancel={() => setTarget(null)}
        onConfirm={() =>
          target &&
          run(() => post(`/api/v1/apps/${enc(a.name)}/rollback`, { version: target.number }), 'The rollback starts.').then(() => {
            setTarget(null);
            reload();
          })
        }
      >
        <p>NidaOS makes a new version with the image and the settings of version {target?.number}. It also has the health check.</p>
      </Confirm>

      <h3>The GitHub repository</h3>
      {repo ? (
        <>
          <KV
            rows={[
              ['Repository', <Mono>github.com/{repo.owner}/{repo.repo}</Mono>, <a className="btn small" href={`https://github.com/${repo.owner}/${repo.repo}`} target="_blank" rel="noreferrer">Open</a>],
              ['Branch', <Mono>{repo.branch}</Mono>],
              [
                'Workflow',
                <Mono>.github/workflows/nidaos-deploy-{a.name}.yml</Mono>,
                repo.pull_request ? (
                  <a className="btn small" href={repo.pull_request} target="_blank" rel="noreferrer">
                    Pull request
                  </a>
                ) : null,
              ],
              ['Connected', dateTime(repo.created)],
            ]}
          />
          {repo.problem && <Note kind="bad">{repo.problem}</Note>}
          {d.admin && (
            <div className="inline" style={{ marginTop: 12 }}>
              <Btn
                disabled={busy}
                onClick={() => run(() => post(`/api/v1/apps/${enc(a.name)}/repository/pull-request`), 'The pull request with the workflow is open.').then(reloadRepo)}
              >
                Open the pull request again
              </Btn>
              <Btn kind="danger" disabled={busy} onClick={() => setDisconnect(true)}>
                Disconnect
              </Btn>
            </div>
          )}
          <Confirm
            open={disconnect}
            title="Disconnect the repository"
            action="Disconnect"
            danger
            busy={busy}
            onCancel={() => setDisconnect(false)}
            onConfirm={() =>
              run(() => del(`/api/v1/apps/${enc(a.name)}/repository`), 'The repository is disconnected.').then(() => {
                setDisconnect(false);
                reloadRepo();
                d.reload();
              })
            }
          >
            <p>NidaOS stops the jobs of {a.name}. The workflow file stays in the repository, and the app stays in operation.</p>
          </Confirm>
          <h3>Jobs</h3>
          <JobTable jobs={jobs} />
        </>
      ) : d.runnersOff ? (
        <Note>This NidaOS has no GitHub runners.</Note>
      ) : !github?.configured ? (
        <Note>
          NidaOS has no GitHub App yet. <LinkBtn small to="/runners">Open Runners</LinkBtn> to make it, then connect the repository here.
        </Note>
      ) : d.admin ? (
        <form className="card form" onSubmit={connect}>
          <Row label="Repository" htmlFor="repo" help="A private repository where the GitHub App is installed. Example: me/shop.">
            <input id="repo" type="text" value={repoName} onChange={(e) => setRepoName(e.target.value)} placeholder="owner/name" required />
          </Row>
          <Row label="Branch" htmlFor="branch" help="Empty: the default branch of the repository.">
            <input id="branch" type="text" className="mid" value={branch} onChange={(e) => setBranch(e.target.value)} placeholder="main" />
          </Row>
          <Row label="">
            <Btn type="submit" kind="primary" disabled={busy || !repoName.includes('/')}>
              Connect and open the pull request
            </Btn>
          </Row>
        </form>
      ) : (
        <Note>The app is not connected to a repository.</Note>
      )}

      {d.admin && (
        <>
          <h3>Deploy an image</h3>
          <form
            className="card form"
            onSubmit={(e) => {
              e.preventDefault();
              run(() => post(`/api/v1/apps/${enc(a.name)}/deploys`, { image: image.trim() }), 'The deploy starts.').then((ok) => {
                if (ok) {
                  setImage('');
                  reload();
                }
              });
            }}
          >
            <Row label="Image" htmlFor="image" help="An image in the local registry, for example localhost:5000/name:tag.">
              <input id="image" type="text" value={image} onChange={(e) => setImage(e.target.value)} placeholder={`localhost:5000/${a.name}:1.0`} required />
            </Row>
            <Row label="">
              <Btn type="submit" disabled={busy || image.trim() === '' || a.state === 'deploying'}>
                Deploy
              </Btn>
            </Row>
          </form>
        </>
      )}
    </>
  );
}

const RESERVED = ['PORT', 'DATABASE_URL', 'PGHOST', 'PGPORT', 'PGDATABASE', 'PGUSER', 'PGPASSWORD', 'PGSSLMODE'];

function AppVariables({ a, reload }: TabProps) {
  const d = useData();
  const { busy, run } = useAction();
  const [key, setKey] = useState('');
  const [value, setValue] = useState('');
  const [secret, setSecret] = useState(false);
  const [dbName, setDbName] = useState('');
  const vars = Object.entries(a.config.variables ?? {}).sort(([x], [y]) => x.localeCompare(y));
  const db = linkedDb(d.databases, a.name);
  const saveConfig = (variables: Record<string, string>) =>
    run(() => patch(`/api/v1/apps/${enc(a.name)}`, { config: { ...a.config, variables } }), 'Saved. The app restarts with the new values.').then(reload);
  const keyError = key && !/^[A-Za-z_][A-Za-z0-9_]*$/.test(key) ? 'Use letters, digits and _. Start with a letter.' : key.startsWith('NIDA_') || RESERVED.includes(key) ? 'This name is for NidaOS.' : '';
  type VarRow = { name: string; value: string; from: string; kind: 'var' | 'secret' | 'nida' | 'db' };
  const rows: VarRow[] = [
    ...(a.config.port ? [{ name: 'PORT', value: String(a.config.port), from: 'NidaOS', kind: 'nida' as const }] : []),
    ...(db ? [{ name: 'DATABASE_URL', value: '••••••••••••', from: `link to ${db.name}`, kind: 'db' as const }] : []),
    ...vars.map(([name, value]) => ({ name, value, from: 'you', kind: 'var' as const })),
    ...(a.secrets ?? []).map((name) => ({ name, value: '••••••••••••', from: 'you', kind: 'secret' as const })),
  ];
  const cols: Column<VarRow>[] = [
    {
      title: 'Name',
      cell: (r) => (
        <>
          <Mono>{r.name}</Mono> {r.kind === 'secret' && <span className="tag">secret</span>}
        </>
      ),
    },
    { title: 'Value', cell: (r) => <Mono>{r.value}</Mono>, className: 'wrap' },
    { title: 'Comes from', cell: (r) => r.from },
    {
      title: '',
      cell: (r) =>
        d.admin && (r.kind === 'var' || r.kind === 'secret') ? (
          <span className="inline">
            <Btn
              small
              onClick={() => {
                setKey(r.name);
                setSecret(r.kind === 'secret');
                setValue(r.kind === 'var' ? r.value : '');
              }}
            >
              {r.kind === 'secret' ? 'Replace' : 'Change'}
            </Btn>
            <Btn
              small
              disabled={busy}
              onClick={() => {
                if (r.kind === 'secret') run(() => del(`/api/v1/apps/${enc(a.name)}/secrets/${enc(r.name)}`), 'The secret is removed.').then(reload);
                else {
                  const next = { ...(a.config.variables ?? {}) };
                  delete next[r.name];
                  saveConfig(next);
                }
              }}
            >
              Remove
            </Btn>
          </span>
        ) : null,
    },
  ];
  const dbs = (d.databases ?? []).filter((x) => x.state === 'ready');
  return (
    <>
      <p className="lead" style={{ margin: '16px 0' }}>
        The app reads these values when it starts. A change restarts the app.
      </p>
      <Table label="Variables" cols={cols} rows={rows} rowKey={(r) => r.kind + r.name} empty="No variables." />
      <Note>NidaOS does not show a secret again after you save it. You can only replace it.</Note>
      {d.admin && (
        <>
          <h3>Add or change a value</h3>
          <form
            className="card form"
            onSubmit={(e) => {
              e.preventDefault();
              const k = key.trim();
              const ok = secret
                ? run(() => put(`/api/v1/apps/${enc(a.name)}/secrets/${enc(k)}`, { value }), 'The secret is saved.').then(reload)
                : saveConfig({ ...(a.config.variables ?? {}), [k]: value });
              ok.then(() => {
                setKey('');
                setValue('');
              });
            }}
          >
            <Row label="Name" htmlFor="var-name" error={keyError}>
              <input id="var-name" type="text" className="mid" value={key} onChange={(e) => setKey(e.target.value)} placeholder="LOG_LEVEL" required />
            </Row>
            <Row label="Value" htmlFor="var-value">
              <input id="var-value" type={secret ? 'password' : 'text'} value={value} onChange={(e) => setValue(e.target.value)} autoComplete="off" />
            </Row>
            <Row label="Kind">
              <div className="opts2">
                <Radio name="kind" checked={!secret} onChange={() => setSecret(false)} title="Variable" text="The console shows the value." />
                <Radio name="kind" checked={secret} onChange={() => setSecret(true)} title="Secret" text="NidaOS never shows the value again." />
              </div>
            </Row>
            <Row label="">
              <Btn type="submit" kind="primary" disabled={busy || !key.trim() || !!keyError}>
                Save
              </Btn>
            </Row>
          </form>
          <h3>Database</h3>
          {db ? (
            <Todo
              items={[
                {
                  key: db.name,
                  led: '',
                  title: `Connected to ${db.name}`,
                  text: 'The app gets DATABASE_URL and the PG variables.',
                  action: (
                    <LinkBtn small to={`/databases/${db.name}?tab=access`}>
                      Open
                    </LinkBtn>
                  ),
                },
              ]}
            />
          ) : dbs.length === 0 ? (
            <Note>
              There is no database that is ready. <LinkBtn small to="/databases/new">Add a database</LinkBtn>
            </Note>
          ) : (
            <div className="card form">
              <Row label="Connect a database" htmlFor="db" help="The app gets the variable DATABASE_URL and restarts.">
                <div className="inline">
                  <select id="db" value={dbName} onChange={(e) => setDbName(e.target.value)} style={{ maxWidth: 260 }}>
                    <option value="">Select…</option>
                    {dbs.map((x) => (
                      <option key={x.name} value={x.name}>
                        {x.name}
                      </option>
                    ))}
                  </select>
                  <Btn
                    disabled={busy || !dbName}
                    onClick={() => run(() => post(`/api/v1/databases/${enc(dbName)}/links`, { app: a.name, mode: 'rw' }), 'The database is connected.').then(() => d.reload())}
                  >
                    Connect
                  </Btn>
                </div>
              </Row>
            </div>
          )}
        </>
      )}
    </>
  );
}

function AppResources({ a, reload }: TabProps) {
  const d = useData();
  const { busy, run } = useAction();
  const limits = a.config.limits ?? {};
  const [mem, setMem] = useState(limits.memory_bytes ? String(Math.round(limits.memory_bytes / MiB)) : '');
  const [cpu, setCpu] = useState(limits.cpu_millis ? String(limits.cpu_millis / 1000) : '');
  const [copies, setCopies] = useState(String(a.copies));
  const [port, setPort] = useState(a.config.port ? String(a.config.port) : '');
  const [health, setHealth] = useState(a.config.health_check?.path ?? '');
  const [move, setMove] = useState<Folder | null>(null);
  const [fPath, setFPath] = useState('');
  const [fSize, setFSize] = useState('10');
  const [fTier, setFTier] = useState<TierName>('standard');
  const hasFolders = (a.folders?.length ?? 0) > 0;
  const save = (e: FormEvent) => {
    e.preventDefault();
    const config: AppConfig = { ...a.config, limits: { cpu_millis: cpu ? Math.round(Number(cpu) * 1000) : 0, memory_bytes: mem ? Math.round(Number(mem) * MiB) : 0 } };
    if (port) config.port = Number(port);
    else delete config.port;
    if (health && port) config.health_check = { ...(a.config.health_check ?? {}), path: health };
    else delete config.health_check;
    run(() => patch(`/api/v1/apps/${enc(a.name)}`, { config, copies: Number(copies) }), 'Saved. A change of the settings makes a new version.').then(reload);
  };
  const tiers = new Map((d.state?.tiers ?? []).map((t) => [t.name, t]));
  const free = (t: TierName) => tiers.get(t)?.usage?.free_bytes;
  return (
    <>
      <h3>Limits</h3>
      <form className="card form" onSubmit={save}>
        <Row label="Memory" htmlFor="mem" help="Empty: no limit. If the app uses more, k3s stops it and starts it again. Minimum 16 MB.">
          <div className="inline">
            <input id="mem" className="short" type="number" min={16} value={mem} onChange={(e) => setMem(e.target.value)} disabled={!d.admin} /> MB
          </div>
        </Row>
        <Row label="Processor" htmlFor="cpu" help="Empty: no limit.">
          <div className="inline">
            <input id="cpu" className="short" type="number" min={0.05} step={0.05} value={cpu} onChange={(e) => setCpu(e.target.value)} disabled={!d.admin} /> cores
          </div>
        </Row>
        <Row label="Copies" htmlFor="copies" help={hasFolders ? 'An app with a folder has 1 copy at most.' : 'More copies share the requests. 0 stops the app.'}>
          <input id="copies" className="short" type="number" min={0} max={hasFolders ? 1 : 10} value={copies} onChange={(e) => setCopies(e.target.value)} disabled={!d.admin} />
        </Row>
        <Row label="Port" htmlFor="port" help="The port where the app listens for HTTP. Empty: the app has no address (for example a worker).">
          <input id="port" className="short" type="number" min={1} max={65535} value={port} onChange={(e) => setPort(e.target.value)} disabled={!d.admin} />
        </Row>
        <Row label="Health check" htmlFor="health" help="NidaOS puts a new version in operation only if this path answers. Empty: NidaOS only opens the port.">
          <input id="health" type="text" className="mid" value={health} onChange={(e) => setHealth(e.target.value)} placeholder="/healthz" disabled={!d.admin || !port} />
        </Row>
        {d.admin && (
          <Row label="">
            <Btn type="submit" kind="primary" disabled={busy}>
              Save
            </Btn>
          </Row>
        )}
      </form>
      <h3>Folders that stay</h3>
      <Table
        label="Folders"
        cols={[
          { title: 'Folder in the app', cell: (f: Folder) => <Mono>{f.path}</Mono> },
          { title: 'Tier', cell: (f) => (f.moving_to ? <Led kind="busy">moving to {tierLabel(f.moving_to)}</Led> : <Tier name={f.tier} />) },
          { title: 'Limit', cell: (f) => <span className="num">{bytes(f.size_bytes)}</span> },
          { title: 'Problem', cell: (f) => (f.move_error ? <span className="bad-text">{f.move_error}</span> : ''), className: 'wrap' },
          {
            title: '',
            cell: (f) =>
              d.admin && !f.moving_to ? (
                <Btn small onClick={() => setMove(f)}>
                  Move to {f.tier === 'standard' ? 'Performance' : 'Standard'}…
                </Btn>
              ) : null,
          },
        ]}
        rows={a.folders ?? []}
        rowKey={(f) => f.name}
        empty="No folders. All the files of the app go away at each deploy."
      />
      <Note>All other files of the app go away at each deploy. A move to a different tier stops the app during the copy.</Note>
      <Confirm
        open={move !== null}
        title={`Move ${move?.path}`}
        action="Move"
        busy={busy}
        onCancel={() => setMove(null)}
        onConfirm={() =>
          move &&
          run(
            () => post(`/api/v1/apps/${enc(a.name)}/folders/${enc(move.name)}/move`, { tier: move.tier === 'standard' ? 'performance' : 'standard' }),
            'The move starts. The app stops during the copy.',
          ).then(() => {
            setMove(null);
            reload();
          })
        }
      >
        <p>
          The app stops during the copy of the files to the {move?.tier === 'standard' ? 'Performance' : 'Standard'} tier. If the copy fails, the app
          starts again on the old tier with the old files.
        </p>
      </Confirm>
      {d.admin && (a.copies <= 1 || hasFolders) && (
        <>
          <h3>Add a folder</h3>
          <form
            className="card form"
            onSubmit={(e) => {
              e.preventDefault();
              const name = fPath.replace(/^\/+/, '').replace(/[^a-z0-9-]+/gi, '-').toLowerCase().slice(0, 32) || 'data';
              run(
                () => patch(`/api/v1/apps/${enc(a.name)}`, { add_folders: [{ name, path: fPath, tier: fTier, size_bytes: Math.round(Number(fSize) * GiB) }] }),
                'The folder is added.',
              ).then((ok) => {
                if (ok) {
                  setFPath('');
                  reload();
                }
              });
            }}
          >
            <Row label="Folder in the app" htmlFor="fpath" help="An absolute path in the container, for example /data.">
              <input id="fpath" type="text" className="mid" value={fPath} onChange={(e) => setFPath(e.target.value)} placeholder="/data" required />
            </Row>
            <Row label="Size" htmlFor="fsize">
              <div className="inline">
                <input id="fsize" className="short" type="number" min={1} value={fSize} onChange={(e) => setFSize(e.target.value)} /> GB
              </div>
            </Row>
            <Row label="Tier">
              <TierPick value={fTier} onChange={setFTier} free={free} name="ftier" />
            </Row>
            <Row label="">
              <Btn type="submit" disabled={busy || !fPath.startsWith('/')}>
                Add the folder
              </Btn>
            </Row>
          </form>
        </>
      )}
    </>
  );
}

export function TierPick({ value, onChange, free, name }: { value: TierName; onChange: (t: TierName) => void; free: (t: TierName) => number | undefined; name: string }) {
  const f = (t: TierName) => (free(t) !== undefined ? `${bytes(free(t))} free` : 'not available');
  return (
    <div className="opts2">
      <Radio name={name} checked={value === 'performance'} onChange={() => onChange('performance')} title={<Tier name="performance" />} text={`Solid-state disk · ${f('performance')}`} />
      <Radio name={name} checked={value === 'standard'} onChange={() => onChange('standard')} title={<Tier name="standard" />} text={`Hard disk · ${f('standard')}`} />
    </div>
  );
}

function AppAccess({ a }: TabProps) {
  return (
    <>
      <h3>Who can open it</h3>
      <Radio name="reach" checked onChange={() => {}} title="Home network only" text="Only the devices in your home network." />
      <Radio name="reach" checked={false} disabled onChange={() => {}} title="Public, with sign-in" text="Through the internet gateway. It comes with the gateway (milestone M-10)." />
      <Radio name="reach" checked={false} disabled onChange={() => {}} title="Public, no sign-in" text="Through the internet gateway. It comes with the gateway (milestone M-10)." />
      <h3>Addresses</h3>
      <KV
        rows={[
          ['In the home network', a.address ? <Mono>{a.address}</Mono> : <span className="muted">no port</span>],
          ['For other apps', a.config.port ? <Mono>http://{a.name}</Mono> : <span className="muted">no port</span>],
        ]}
      />
      <Note>
        A device must trust the NidaOS authority one time for the https addresses. Get the certificate at{' '}
        <a href="/api/v1/ca.crt" download="nidaos-ca.crt">
          /api/v1/ca.crt
        </a>
        .
      </Note>
    </>
  );
}

function AppRemove({ a, reload }: TabProps) {
  const d = useData();
  const nav = useNavigate();
  const { busy, run } = useAction();
  const [remove, setRemove] = useState(false);
  const db = linkedDb(d.databases, a.name);
  const stopped = a.copies === 0;
  const size = (a.folders ?? []).reduce((n, f) => n + f.size_bytes, 0);
  if (!d.admin) return <Note>You need the admin level to stop or remove an app.</Note>;
  return (
    <>
      <h3>{stopped ? 'Start' : 'Stop'}</h3>
      <Todo
        items={[
          {
            key: 'stop',
            title: stopped ? `Start ${a.name}` : `Stop ${a.name}`,
            text: stopped ? 'NidaOS starts one copy of the version in operation.' : 'The address does not answer. The data and the settings stay.',
            action: (
              <Btn disabled={busy} onClick={() => run(() => patch(`/api/v1/apps/${enc(a.name)}`, { copies: stopped ? 1 : 0 }), stopped ? 'The app starts.' : 'The app stops.').then(reload)}>
                {stopped ? 'Start' : 'Stop'}
              </Btn>
            ),
          },
        ]}
      />
      <h3>Remove</h3>
      <Todo
        items={[
          {
            key: 'remove',
            title: `Remove ${a.name}`,
            text: `NidaOS removes the app, its versions, its secrets and its folders${size ? ` (${bytes(size)})` : ''}. ${db ? `The database ${db.name} stays. ` : ''}You cannot go back.`,
            action: (
              <Btn kind="danger" onClick={() => setRemove(true)}>
                Remove…
              </Btn>
            ),
          },
        ]}
      />
      <Confirm
        open={remove}
        title={`Remove ${a.name}`}
        action="Remove"
        danger
        busy={busy}
        confirmText={a.name}
        onCancel={() => setRemove(false)}
        onConfirm={() =>
          run(() => del(`/api/v1/apps/${enc(a.name)}`), `${a.name} is removed.`).then((ok) => {
            setRemove(false);
            if (ok) {
              d.reload();
              nav('/apps');
            }
          })
        }
      >
        <p>This deletes the files of the folders of {a.name}. You cannot go back.</p>
      </Confirm>
    </>
  );
}

// ---- Add an app ----

type DbChoice = 'none' | 'new' | 'existing';

export function AppNew() {
  const d = useData();
  const toast = useToast();
  const nav = useNavigate();
  const [name, setName] = useState('');
  const [repoName, setRepoName] = useState('');
  const [branch, setBranch] = useState('');
  const [port, setPort] = useState('8080');
  const [health, setHealth] = useState('/healthz');
  const [folder, setFolder] = useState(false);
  const [fPath, setFPath] = useState('/data');
  const [fSize, setFSize] = useState('10');
  const [tier, setTier] = useState<TierName>('standard');
  const [dbChoice, setDbChoice] = useState<DbChoice>('none');
  const [dbExisting, setDbExisting] = useState('');
  const [step, setStep] = useState<string | null>(null);
  const [error, setError] = useState<unknown>(null);
  const github = d.runners?.github?.configured;
  const tiers = useMemo(() => new Map((d.state?.tiers ?? []).map((t) => [t.name, t])), [d.state]);
  const nameError = name && !NAME_RE.test(name) ? 'Lowercase letters, digits and hyphens. Start with a letter. 32 characters at most.' : d.apps?.some((x) => x.name === name) ? 'An app with this name exists.' : '';
  const dbName = `${name || 'app'}-db`;
  const readyDbs = (d.databases ?? []).filter((x) => x.state === 'ready' && !x.links?.length);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    let added = false;
    try {
      setStep('Adding the app…');
      const config: AppConfig = {};
      if (port) {
        config.port = Number(port);
        if (health) config.health_check = { path: health };
      }
      const folders = folder ? [{ name: 'data', path: fPath, tier, size_bytes: Math.round(Number(fSize) * GiB) }] : undefined;
      await post<App>('/api/v1/apps', { name, config, folders });
      added = true;
      if (dbChoice === 'new') {
        setStep(`Adding the database ${dbName}…`);
        await post('/api/v1/databases', { name: dbName, tier: 'performance' });
        setStep(`Waiting for ${dbName}. This takes approximately one minute…`);
        const until = Date.now() + 180_000;
        for (;;) {
          const db = await get<Database>(`/api/v1/databases/${enc(dbName)}`);
          if (db.state === 'ready') break;
          if (db.state === 'failed') throw new Error(`The database ${dbName} failed: ${db.reason ?? ''}`);
          if (Date.now() > until) throw new Error(`The database ${dbName} is not ready after 3 minutes. Connect it later on the app page.`);
          await new Promise((r) => setTimeout(r, 3000));
        }
      }
      const link = dbChoice === 'new' ? dbName : dbChoice === 'existing' ? dbExisting : '';
      if (link) {
        setStep(`Connecting the database ${link}…`);
        await post(`/api/v1/databases/${enc(link)}/links`, { app: name, mode: 'rw' });
      }
      if (repoName.trim()) {
        setStep('Connecting the repository and opening the pull request…');
        await put(`/api/v1/apps/${enc(name)}/repository`, { repository: repoName.trim(), branch: branch.trim() || undefined });
      }
      d.reload();
      nav(`/apps/${name}${repoName.trim() ? '?tab=deploys' : ''}`);
    } catch (err) {
      setStep(null);
      if (added) {
        // The app exists: the owner continues on its page.
        toast(`${name} is added, but a step failed: ${errorText(err)}`, true);
        d.reload();
        nav(`/apps/${name}`);
        return;
      }
      setError(err);
    }
  };

  if (!d.admin) return <Note>You need the admin level to add an app.</Note>;
  const h = (n: number, t: string) => (
    <h3>
      <span className="step">{n}</span>
      {t}
    </h3>
  );
  return (
    <>
      <Link className="back" to="/apps">
        ← Apps
      </Link>
      <Head title="Add an app" lead="NidaOS prepares the place for the app. Then a push to the repository builds it and puts it in operation." />
      <form className="cols" onSubmit={submit}>
        <div>
          {h(1, 'Source')}
          <div className="card form">
            {github ? (
              <>
                <Row label="Repository" htmlFor="new-repo" help="A private repository where the GitHub App of NidaOS is installed. Empty: connect it later.">
                  <input id="new-repo" type="text" value={repoName} onChange={(e) => setRepoName(e.target.value)} placeholder="owner/name" />
                </Row>
                <Row label="Branch" htmlFor="new-branch" help="A push to this branch starts a deploy. Empty: the default branch.">
                  <input id="new-branch" type="text" className="mid" value={branch} onChange={(e) => setBranch(e.target.value)} placeholder="main" />
                </Row>
              </>
            ) : (
              <Row label="Repository">
                <span className="muted">
                  NidaOS has no GitHub App yet. Add the app now, then connect the repository after you make the GitHub App on the{' '}
                  <Link to="/runners">Runners</Link> page.
                </span>
              </Row>
            )}
          </div>
          {h(2, 'The app')}
          <div className="card form">
            <Row label="Name" htmlFor="new-name" help="Lowercase letters, digits and hyphens. The name does not change." error={nameError}>
              <input id="new-name" type="text" value={name} onChange={(e) => setName(e.target.value.toLowerCase())} placeholder="blog" required />
            </Row>
            <Row label="Port" htmlFor="new-port" help="The port on which the app listens. Empty: the app has no address.">
              <input id="new-port" className="short" type="number" min={1} max={65535} value={port} onChange={(e) => setPort(e.target.value)} />
            </Row>
            <Row label="Health check" htmlFor="new-health" help="NidaOS puts a new version in operation only if this path answers.">
              <input id="new-health" type="text" className="mid" value={health} onChange={(e) => setHealth(e.target.value)} disabled={!port} />
            </Row>
          </div>
          {h(3, 'Storage')}
          <div className="card form">
            <Row label="Folders that stay">
              <Radio name="vol" checked={!folder} onChange={() => setFolder(false)} title="None" text="The app keeps no files. This is the usual choice for an app with a database." />
              <Radio name="vol" checked={folder} onChange={() => setFolder(true)} title="One folder" text="The files in this folder stay at each deploy." />
            </Row>
            {folder && (
              <>
                <Row label="Folder in the app" htmlFor="new-fpath">
                  <div className="inline">
                    <input id="new-fpath" type="text" className="mid" value={fPath} onChange={(e) => setFPath(e.target.value)} /> limit{' '}
                    <input className="short" type="number" min={1} value={fSize} onChange={(e) => setFSize(e.target.value)} aria-label="Size in GB" /> GB
                  </div>
                </Row>
                <Row label="Tier">
                  <TierPick value={tier} onChange={setTier} free={(t) => tiers.get(t)?.usage?.free_bytes} name="apptier" />
                </Row>
              </>
            )}
          </div>
          {h(4, 'Database')}
          <div className="card form">
            <Row label="Connect" help={<>The app gets the connection data in the variable <Mono>DATABASE_URL</Mono>.</>}>
              <Radio name="db" checked={dbChoice === 'none'} onChange={() => setDbChoice('none')} title="No database" />
              <Radio name="db" checked={dbChoice === 'new'} onChange={() => setDbChoice('new')} title="A new database" text={`PostgreSQL 18 · name ${dbName} · Performance tier`} />
              <Radio
                name="db"
                checked={dbChoice === 'existing'}
                onChange={() => setDbChoice('existing')}
                disabled={readyDbs.length === 0}
                title="A database that you have"
                text={readyDbs.length ? readyDbs.map((x) => x.name).join(', ') : 'No free database'}
              />
              {dbChoice === 'existing' && (
                <select value={dbExisting} onChange={(e) => setDbExisting(e.target.value)} aria-label="Database" style={{ marginTop: 6 }}>
                  <option value="">Select…</option>
                  {readyDbs.map((x) => (
                    <option key={x.name}>{x.name}</option>
                  ))}
                </select>
              )}
            </Row>
          </div>
          {h(5, 'Address and access')}
          <div className="card form">
            <Row label="Who can open it" help="Public access comes with the internet gateway (milestone M-10).">
              <Radio name="nreach" checked onChange={() => {}} title="Home network only" text="Only the devices in your home network." />
            </Row>
            <Row label="Address">
              <Mono>{port ? `https://${name || 'name'}.nidaos.local` : 'no address (no port)'}</Mono>
            </Row>
          </div>
        </div>
        <aside className="card sum">
          <h4>{name || 'New app'}</h4>
          <dl>
            <dt>Source</dt>
            <dd className="mono">{repoName ? `${repoName}${branch ? ' · ' + branch : ''}` : 'later'}</dd>
            <dt>Port</dt>
            <dd>{port || 'none'}</dd>
            <dt>Storage</dt>
            <dd>{folder ? `${fPath} · ${fSize} GB · ${tierLabel(tier)}` : 'no folder'}</dd>
            <dt>Database</dt>
            <dd>{dbChoice === 'new' ? `new · ${dbName}` : dbChoice === 'existing' ? dbExisting || '–' : 'none'}</dd>
            <dt>Access</dt>
            <dd>Home network</dd>
          </dl>
          <Btn type="submit" kind="primary" disabled={!!step || !name || !!nameError || (dbChoice === 'existing' && !dbExisting)}>
            {repoName ? 'Add the app and connect' : 'Add the app'}
          </Btn>
          {step && (
            <div className="note" role="status" style={{ fontSize: 13 }}>
              {step}
            </div>
          )}
          {error ? (
            <div className="note bad" role="alert" style={{ fontSize: 13 }}>
              {errorText(error)}
            </div>
          ) : null}
          {repoName && (
            <div className="note" style={{ fontSize: 13 }}>
              NidaOS adds the workflow file to the repository in a pull request. You merge it on GitHub, then the first deploy starts.
            </div>
          )}
        </aside>
      </form>
    </>
  );
}
