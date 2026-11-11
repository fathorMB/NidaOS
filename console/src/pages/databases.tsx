// The databases (task T-05): the list, the database page with its five
// tabs and the form to add a database.

import { useMemo, useState, type FormEvent } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { del, enc, get, post, put, type Backup, type Connection, type Database, type TierName } from '../api';
import { useData } from '../data';
import { ago, bytes, dateTime, errorText, fixed, GiB, MiB, tierLabel } from '../format';
import { useLive, useNow, usePoll } from '../live';
import { dbLive, dbNamespace, nsCpu, nsMemMB, series } from '../metrics';
import { NAME_RE, TierPick } from './apps';
import {
  Bar,
  Btn,
  Confirm,
  CopyBtn,
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
  Row,
  Stats,
  Switch,
  Table,
  Tabs,
  Tier,
  Todo,
  useAction,
  useTab,
  type Column,
  type LedKind,
} from '../ui';

export function dbLed(db: Database): LedKind {
  if (db.state === 'ready') return db.reason ? 'warn' : '';
  if (db.state === 'failed') return 'bad';
  return 'busy';
}

const dbStateText: Record<Database['state'], string> = {
  creating: 'Starting',
  restoring: 'Restoring a backup',
  updating: 'Update in progress',
  ready: 'In operation',
  failed: 'Failed',
};

const imageVersion = (image?: string) => image?.split(':').pop()?.replace('-alpine', '') ?? '';

function lastBackupText(backups: Backup[] | undefined, now: number) {
  const b = backups?.[0];
  return b ? `${ago(b.created, now)} · ${b.kind}` : 'none yet';
}

// ---- The list ----

export function DatabasesPage() {
  const d = useData();
  const nav = useNavigate();
  const cols: Column<Database>[] = [
    {
      title: 'Name',
      cell: (db) => (
        <Led kind={dbLed(db)}>
          <b>{db.name}</b>
        </Led>
      ),
    },
    { title: 'Engine', cell: (db) => <span className="tag">PostgreSQL {imageVersion(db.image) || db.version}</span> },
    { title: 'Storage', cell: (db) => <Tier name={db.tier} /> },
    { title: 'Size', cell: (db) => <span className="num">{db.live ? `${bytes(db.live.size_bytes)} of ${bytes(db.size_bytes)}` : bytes(db.size_bytes)}</span> },
    { title: 'Used by', cell: (db) => (db.links?.length ? db.links.map((l) => l.app).join(', ') : <span className="muted">no app</span>) },
    { title: 'State', cell: (db) => dbStateText[db.state] },
  ];
  return (
    <>
      <Head title="Databases" lead="NidaOS operates each database: it starts it, makes the backups and keeps it on the tier that you select.">
        {d.admin && (
          <LinkBtn to="/databases/new" kind="primary">
            Add a database
          </LinkBtn>
        )}
      </Head>
      <Table label="Databases" cols={cols} rows={d.databases} rowKey={(db) => db.name} onOpen={(db) => nav(`/databases/${db.name}`)} empty="No databases yet." />
    </>
  );
}

// ---- The database page ----

interface TabProps {
  db: Database;
  backups?: Backup[];
  reload: () => void;
}

export function DatabasePage() {
  const { name = '' } = useParams();
  const db = usePoll(() => get<Database>(`/api/v1/databases/${enc(name)}`), 3000, [name]);
  const backups = usePoll(() => get<{ backups: Backup[] }>(`/api/v1/databases/${enc(name)}/backups`).then((r) => r.backups), 10_000, [name]);
  const [tab, href] = useTab('overview');
  const d = useData();
  const { busy, run } = useAction();
  const x = db.data;
  if (db.error && !x) {
    return (
      <>
        <Link className="back" to="/databases">
          ← Databases
        </Link>
        <Head title={name} />
        <ErrorNote error={db.error} />
      </>
    );
  }
  if (!x) return <p className="muted">Loading…</p>;
  const reload = () => {
    db.reload();
    backups.reload();
    d.reload();
  };
  const props = { db: x, backups: backups.data, reload };
  return (
    <>
      <Link className="back" to="/databases">
        ← Databases
      </Link>
      <Head
        title={x.name}
        lead={
          <>
            <Led kind={dbLed(x)}>{dbStateText[x.state]}</Led> · PostgreSQL {imageVersion(x.image) || x.version} · <Tier name={x.tier} />
          </>
        }
      >
        <LinkBtn to={`/logs?source=${enc('pod/' + dbNamespace(x.name))}`}>Open the logs</LinkBtn>
        {d.admin && (
          <Btn kind="primary" disabled={busy || x.state !== 'ready'} onClick={() => run(() => post(`/api/v1/databases/${enc(x.name)}/backups`), 'The backup starts.').then(reload)}>
            Back up now
          </Btn>
        )}
      </Head>
      <Tabs
        list={[
          ['overview', 'Overview'],
          ['backups', 'Backups'],
          ['access', 'Access'],
          ['settings', 'Settings'],
          ['remove', 'Remove'],
        ]}
        current={tab}
        href={href}
      />
      {x.reason && <Note kind="bad">{x.reason}</Note>}
      {tab === 'overview' && <DbOverview {...props} />}
      {tab === 'backups' && <DbBackups {...props} />}
      {tab === 'access' && <DbAccess {...props} />}
      {tab === 'settings' && <DbSettings {...props} />}
      {tab === 'remove' && <DbRemove {...props} />}
    </>
  );
}

function DbOverview({ db, backups }: TabProps) {
  const live = useLive();
  const now = useNow(10_000);
  const last = live.history[live.history.length - 1];
  const ns = dbNamespace(db.name);
  const stale = live.metrics.state === 'stopped';
  const conn = (s: (typeof live.history)[number]) => dbLive(s, db.name)?.connections;
  const tps = (s: (typeof live.history)[number]) => dbLive(s, db.name)?.transactions_per_second;
  const lv = last ? dbLive(last, db.name) : undefined;
  const size = lv?.size_bytes ?? db.live?.size_bytes;
  return (
    <>
      <h3 style={{ marginTop: 16 }}>
        Now <LivePill status={live.metrics} />
      </h3>
      <div className="meters">
        <Meter label="Connections" value={lv ? fixed(lv.connections) : '–'} unit="open" values={series(live.history, conn)} stale={stale} />
        <Meter label="Transactions" value={lv ? fixed(lv.transactions_per_second, 1) : '–'} unit="each second" values={series(live.history, tps)} stale={stale} />
        <Meter label="Processor" value={last ? fixed(nsCpu(ns)(last)) : '–'} unit="%" values={series(live.history, nsCpu(ns))} stale={stale} sub={`limit ${fixed(db.cpu_millis / 1000, 2)} cores`} />
        <Meter
          label="Memory"
          value={last ? fixed(nsMemMB(ns)(last)) : '–'}
          unit="MB"
          values={series(live.history, nsMemMB(ns))}
          max={db.memory_bytes / MiB}
          stale={stale}
          sub={`limit ${bytes(db.memory_bytes)}`}
        />
      </div>
      <LogTail source={`pod/${ns}`} n={5} />
      <h3>State</h3>
      <Stats
        items={[
          ['State', dbStateText[db.state], `added ${ago(db.created, now)}`],
          ['Size', size !== undefined ? bytes(size) : '–', `limit ${bytes(db.size_bytes)}`],
          ['Last backup', backups?.[0] ? ago(backups[0].created, now) : 'none', backups?.[0]?.kind ?? 'a daily backup at 03:00'],
        ]}
      />
      <h3>How an app connects</h3>
      <KV
        rows={[
          ['Connected apps', db.links?.length ? db.links.map((l) => l.app).join(', ') : <span className="muted">none</span>, <LinkBtn small to="?tab=access">Connect an app</LinkBtn>],
          ['Variable', <Mono>DATABASE_URL</Mono>],
          ['From the home network', db.lan && db.address ? <Mono>{db.address}</Mono> : <span className="muted">closed</span>, <LinkBtn small to="?tab=access">Change</LinkBtn>],
        ]}
      />
      <Note kind="ok">
        A connected app gets the connection text in the variable <Mono>DATABASE_URL</Mono>. You do not copy the password by hand.
      </Note>
      <h3>Storage</h3>
      <KV
        rows={[
          ['Tier', <Tier name={db.tier} />],
          [
            'Used',
            <div className="inline">
              <div style={{ width: 160 }}>
                <Bar pct={size !== undefined ? (size / db.size_bytes) * 100 : 0} />
              </div>
              <span className="num">
                {size !== undefined ? bytes(size) : '–'} of {bytes(db.size_bytes)}
              </span>
            </div>,
          ],
          ['Last backup', lastBackupText(backups, now), <LinkBtn small to="?tab=backups">Open</LinkBtn>],
        ]}
      />
    </>
  );
}

function DbBackups({ db, backups, reload }: TabProps) {
  const d = useData();
  const nav = useNavigate();
  const { busy, run } = useAction();
  const [restore, setRestore] = useState<Backup | null>(null);
  const [remove, setRemove] = useState<Backup | null>(null);
  const [target, setTarget] = useState('');
  const kind = { daily: 'automatic', manual: 'on demand', update: 'before an update' };
  return (
    <>
      <h3>Schedule</h3>
      <KV
        rows={[
          ['Automatic backup', 'each day after 03:00'],
          ['Keep', 'the last 7 automatic backups, and the last 5 other backups'],
          ['Place', <span>The Standard tier, in a file that only root can read</span>],
        ]}
      />
      <h3>
        Backups{' '}
        {d.admin && (
          <Btn disabled={busy || db.state !== 'ready'} onClick={() => run(() => post(`/api/v1/databases/${enc(db.name)}/backups`), 'The backup starts.').then(reload)}>
            Back up now
          </Btn>
        )}
      </h3>
      <Table
        label="Backups"
        cols={[
          { title: 'Date', cell: (b: Backup) => dateTime(b.created) },
          { title: 'Type', cell: (b) => kind[b.kind] },
          { title: 'Version', cell: (b) => `PostgreSQL ${b.version}` },
          { title: 'Size', cell: (b) => <span className="num">{bytes(b.size_bytes)}</span> },
          {
            title: '',
            cell: (b) =>
              d.admin ? (
                <span className="inline">
                  <Btn
                    small
                    onClick={() => {
                      setRestore(b);
                      setTarget(`${db.name}-restore`.slice(0, 32));
                    }}
                  >
                    Restore…
                  </Btn>
                  <Btn small onClick={() => setRemove(b)}>
                    Delete
                  </Btn>
                </span>
              ) : null,
          },
        ]}
        rows={backups}
        rowKey={(b) => b.name}
        empty="No backups yet."
      />
      <Note>
        <b>A restore makes a new database.</b> It does not replace {db.name}. You examine the new database, then you connect the apps to it.
      </Note>
      <Confirm
        open={restore !== null}
        title="Restore into a new database"
        action="Restore"
        busy={busy}
        onCancel={() => setRestore(null)}
        onConfirm={() =>
          restore &&
          run(
            () => post('/api/v1/databases', { name: target, version: Math.max(restore.version, db.version), tier: db.tier, size_bytes: db.size_bytes, memory_bytes: db.memory_bytes, cpu_millis: db.cpu_millis, restore_from: { database: db.name, backup: restore.name } }),
            `The restore into ${target} starts.`,
          ).then((ok) => {
            setRestore(null);
            if (ok) {
              d.reload();
              nav(`/databases/${target}`);
            }
          })
        }
      >
        <p>
          NidaOS makes the database <b>{target || '…'}</b> from the backup of {restore && dateTime(restore.created)}.
        </p>
        <label>
          Name of the new database
          <br />
          <input type="text" value={target} onChange={(e) => setTarget(e.target.value.toLowerCase())} aria-label="Name of the new database" />
        </label>
        {target && !NAME_RE.test(target) && <div className="field-error">Lowercase letters, digits and hyphens. Start with a letter.</div>}
      </Confirm>
      <Confirm
        open={remove !== null}
        title="Delete the backup"
        action="Delete"
        danger
        busy={busy}
        onCancel={() => setRemove(null)}
        onConfirm={() =>
          remove &&
          run(() => del(`/api/v1/databases/${enc(db.name)}/backups/${enc(remove.name)}`), 'The backup is deleted.').then(() => {
            setRemove(null);
            reload();
          })
        }
      >
        <p>NidaOS deletes the backup of {remove && dateTime(remove.created)}. You cannot go back.</p>
      </Confirm>
    </>
  );
}

function DbAccess({ db, reload }: TabProps) {
  const d = useData();
  const { busy, run } = useAction();
  const [app, setApp] = useState('');
  const [conn, setConn] = useState<Connection | null>(null);
  const [connError, setConnError] = useState<unknown>(null);
  const linked = new Set((db.links ?? []).map((l) => l.app));
  const free = (d.apps ?? []).filter((a) => !linked.has(a.name) && !(d.databases ?? []).some((x) => x.links?.some((l) => l.app === a.name)));
  const show = () =>
    get<Connection>(`/api/v1/databases/${enc(db.name)}/connection`).then(
      (c) => {
        setConn(c);
        setConnError(null);
      },
      setConnError,
    );
  return (
    <>
      <h3>Connected apps</h3>
      <Table
        label="Connected apps"
        cols={[
          { title: 'App', cell: (l: NonNullable<Database['links']>[number]) => <Link to={`/apps/${l.app}`}><b>{l.app}</b></Link> },
          { title: 'Mode', cell: (l) => (l.mode === 'ro' ? 'read only' : 'read and write') },
          { title: 'Role', cell: (l) => <Mono>{l.role}</Mono> },
          { title: 'Since', cell: (l) => dateTime(l.created) },
          {
            title: '',
            cell: (l) =>
              d.admin ? (
                <Btn small disabled={busy} onClick={() => run(() => del(`/api/v1/databases/${enc(db.name)}/links/${enc(l.app)}`), `${l.app} is disconnected.`).then(reload)}>
                  Disconnect
                </Btn>
              ) : null,
          },
        ]}
        rows={db.links ?? []}
        rowKey={(l) => l.app}
        empty="No app is connected."
      />
      {d.admin && (
        <div className="card form" style={{ marginTop: 10 }}>
          <Row label="Connect an app" htmlFor="link-app" help="The app gets DATABASE_URL and the PG variables, then it restarts. An app can have one database.">
            <div className="inline">
              <select id="link-app" value={app} onChange={(e) => setApp(e.target.value)} style={{ maxWidth: 260 }}>
                <option value="">Select…</option>
                {free.map((a) => (
                  <option key={a.name}>{a.name}</option>
                ))}
              </select>
              <Btn
                disabled={busy || !app || db.state !== 'ready'}
                onClick={() =>
                  run(() => post(`/api/v1/databases/${enc(db.name)}/links`, { app, mode: 'rw' }), `${app} is connected.`).then(() => {
                    setApp('');
                    reload();
                  })
                }
              >
                Connect
              </Btn>
            </div>
          </Row>
        </div>
      )}
      <Note kind="ok">Only the connected apps can get to this database. Other apps cannot see it.</Note>
      <h3>From the home network</h3>
      <div className="card form">
        <Row label="Open to the home network" help="For a database program on your PC. The connection uses TLS and the password.">
          <Switch
            on={db.lan}
            disabled={!d.admin || busy || db.state !== 'ready'}
            label={db.lan ? 'On' : 'Off'}
            onChange={(on) => run(() => put(`/api/v1/databases/${enc(db.name)}/lan`, { on }), on ? 'The port is open.' : 'The port is closed.').then(reload)}
          />
        </Row>
        <Row label="Address">{db.lan && db.address ? <Mono>{db.address}</Mono> : <span className="muted">not available while the switch is off</span>}</Row>
      </div>
      <Note>NidaOS does not open a database to the internet.</Note>
      {d.admin && (
        <>
          <h3>The owner connection</h3>
          {conn ? (
            <KV
              rows={[
                ['Host and port', <Mono>{conn.host}:{conn.port}</Mono>],
                ['Database and user', <><Mono>{conn.database}</Mono> · <Mono>{conn.user}</Mono></>],
                ['Password', <span className="secret">{conn.password}</span>, <CopyBtn text={conn.password} />],
                ['Connection text', <Mono>{conn.url}</Mono>, <CopyBtn text={conn.url} />],
              ]}
            />
          ) : (
            <Todo
              items={[
                {
                  key: 'conn',
                  title: 'Show the connection of the owner role',
                  text: 'It has the password. Use it with a database program and the certificate of the NidaOS authority.',
                  action: <Btn onClick={show}>Show</Btn>,
                },
              ]}
            />
          )}
          <ErrorNote error={connError} />
          {conn?.note && <Note>{conn.note}</Note>}
        </>
      )}
    </>
  );
}

function DbSettings({ db, reload }: TabProps) {
  const d = useData();
  const { busy, run } = useAction();
  const [ask, setAsk] = useState(false);
  return (
    <>
      <h3>Version</h3>
      <KV
        rows={[
          ['In operation', `PostgreSQL ${imageVersion(db.image)}`],
          [
            'Available',
            db.update_available ? `PostgreSQL ${imageVersion(db.update_available)} · small update` : db.update_to ? `update to ${imageVersion(db.update_to)} in progress` : 'no update',
            db.update_available && d.admin ? (
              <Btn small onClick={() => setAsk(true)} disabled={busy || db.state !== 'ready'}>
                Update…
              </Btn>
            ) : null,
          ],
        ]}
      />
      <Note>
        Before an update, NidaOS makes a backup. The database stops for some seconds. NidaOS does not update a database without your approval. For a
        new major version, restore a backup into a new database of that version.
      </Note>
      <Confirm
        open={ask}
        title={`Update ${db.name}`}
        action="Update"
        busy={busy}
        onCancel={() => setAsk(false)}
        onConfirm={() =>
          run(() => post(`/api/v1/databases/${enc(db.name)}/update`), 'The update starts with a backup.').then(() => {
            setAsk(false);
            reload();
          })
        }
      >
        <p>
          NidaOS makes a backup, then it starts PostgreSQL {imageVersion(db.update_available)}. If the server does not start, the old version comes
          back.
        </p>
      </Confirm>
      <h3>Limits</h3>
      <KV
        rows={[
          ['Memory', bytes(db.memory_bytes)],
          ['Processor', `${fixed(db.cpu_millis / 1000, 2)} cores`],
          ['Size', `${bytes(db.size_bytes)} (NidaOS warns you at 80%)`],
          ['Storage tier', <Tier name={db.tier} />],
        ]}
      />
      <Note>To change the limits or the tier, restore a backup into a new database with the new values.</Note>
    </>
  );
}

function DbRemove({ db }: TabProps) {
  const d = useData();
  const nav = useNavigate();
  const { busy, run } = useAction();
  const [ask, setAsk] = useState(false);
  if (!d.admin) return <Note>You need the admin level to remove a database.</Note>;
  const apps = (db.links ?? []).map((l) => l.app);
  return (
    <>
      <h3>Remove</h3>
      <Todo
        items={[
          {
            key: 'remove',
            title: `Remove ${db.name}`,
            text: `${apps.length ? `${apps.length} connected app${apps.length > 1 ? 's' : ''} (${apps.join(', ')}) will lose the connection. ` : ''}NidaOS deletes the data. The backups stay, and you can restore them.`,
            action: (
              <Btn kind="danger" onClick={() => setAsk(true)}>
                Remove…
              </Btn>
            ),
          },
        ]}
      />
      <Confirm
        open={ask}
        title={`Remove ${db.name}`}
        action="Remove"
        danger
        busy={busy}
        confirmText={db.name}
        onCancel={() => setAsk(false)}
        onConfirm={() =>
          run(() => del(`/api/v1/databases/${enc(db.name)}`), `${db.name} is removed.`).then((ok) => {
            setAsk(false);
            if (ok) {
              d.reload();
              nav('/databases');
            }
          })
        }
      >
        <p>This deletes the data of {db.name}. The backups stay.</p>
      </Confirm>
    </>
  );
}

// ---- Add a database ----

export function DatabaseNew() {
  const d = useData();
  const nav = useNavigate();
  const versions = d.versions?.length ? d.versions : [18, 17];
  const newest = Math.max(...versions);
  const [name, setName] = useState('');
  const [version, setVersion] = useState(newest);
  const [mem, setMem] = useState('256');
  const [cpu, setCpu] = useState('0.1');
  const [size, setSize] = useState('1');
  const [tier, setTier] = useState<TierName>('performance');
  const [lan, setLan] = useState(false);
  const [app, setApp] = useState('');
  const [step, setStep] = useState<string | null>(null);
  const [error, setError] = useState<unknown>(null);
  const tiers = useMemo(() => new Map((d.state?.tiers ?? []).map((t) => [t.name, t])), [d.state]);
  const nameError = name && !NAME_RE.test(name) ? 'Lowercase letters, digits and hyphens. Start with a letter. 32 characters at most.' : d.databases?.some((x) => x.name === name) ? 'A database with this name exists.' : '';
  const freeApps = (d.apps ?? []).filter((a) => !(d.databases ?? []).some((x) => x.links?.some((l) => l.app === a.name)));
  const memFree = d.state ? bytes(d.state.system.memory.available_bytes) : '';

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setError(null);
    try {
      setStep('Adding the database…');
      await post('/api/v1/databases', {
        name,
        version,
        tier,
        size_bytes: Math.round(Number(size) * GiB),
        memory_bytes: Math.round(Number(mem) * MiB),
        cpu_millis: Math.round(Number(cpu) * 1000),
      });
      if (app || lan) {
        setStep('Waiting for the database. This takes approximately one minute…');
        const until = Date.now() + 180_000;
        for (;;) {
          const db = await get<Database>(`/api/v1/databases/${enc(name)}`);
          if (db.state === 'ready') break;
          if (db.state === 'failed') throw new Error(`The database failed: ${db.reason ?? ''}`);
          if (Date.now() > until) throw new Error('The database is not ready after 3 minutes. Connect the app later.');
          await new Promise((r) => setTimeout(r, 3000));
        }
        if (app) {
          setStep(`Connecting ${app}…`);
          await post(`/api/v1/databases/${enc(name)}/links`, { app, mode: 'rw' });
        }
        if (lan) {
          setStep('Opening the port for the home network…');
          await put(`/api/v1/databases/${enc(name)}/lan`, { on: true });
        }
      }
      d.reload();
      nav(`/databases/${name}`);
    } catch (err) {
      setError(err);
      setStep(null);
    }
  };
  if (!d.admin) return <Note>You need the admin level to add a database.</Note>;
  return (
    <>
      <Link className="back" to="/databases">
        ← Databases
      </Link>
      <Head title="Add a database" lead="NidaOS starts a PostgreSQL database, makes the password and prepares the backups. The database is ready in approximately one minute." />
      <form className="cols" onSubmit={submit}>
        <div>
          <h3>The database</h3>
          <div className="card form">
            <Row label="Name" htmlFor="db-name" help="The name does not change." error={nameError}>
              <input id="db-name" type="text" value={name} onChange={(e) => setName(e.target.value.toLowerCase())} placeholder="shop-db" required />
            </Row>
            <Row label="Version" htmlFor="db-version">
              <select id="db-version" value={version} onChange={(e) => setVersion(Number(e.target.value))} style={{ maxWidth: 200 }}>
                {[...versions].sort((x, y) => y - x).map((v) => (
                  <option key={v} value={v}>
                    {v}
                    {v === newest ? ' (recommended)' : ''}
                  </option>
                ))}
              </select>
            </Row>
            <Row label="Memory" htmlFor="db-mem" help={memFree ? `${memFree} are free on this computer now.` : undefined}>
              <div className="inline">
                <input id="db-mem" className="short" type="number" min={64} value={mem} onChange={(e) => setMem(e.target.value)} /> MB
              </div>
            </Row>
            <Row label="Processor" htmlFor="db-cpu">
              <div className="inline">
                <input id="db-cpu" className="short" type="number" min={0.05} step={0.05} value={cpu} onChange={(e) => setCpu(e.target.value)} /> cores
              </div>
            </Row>
            <Row label="Size" htmlFor="db-size" help="NidaOS warns you at 80%.">
              <div className="inline">
                <input id="db-size" className="short" type="number" min={1} value={size} onChange={(e) => setSize(e.target.value)} /> GB
              </div>
            </Row>
          </div>
          <h3>Storage tier</h3>
          <TierPick value={tier} onChange={setTier} free={(t) => tiers.get(t)?.usage?.free_bytes} name="dbtier" />
          <h3>Access</h3>
          <div className="card form">
            <Row label="Connect to an app" htmlFor="db-app" help="The app gets the variable DATABASE_URL and restarts.">
              <select id="db-app" value={app} onChange={(e) => setApp(e.target.value)} style={{ maxWidth: 260 }}>
                <option value="">Later</option>
                {freeApps.map((a) => (
                  <option key={a.name}>{a.name}</option>
                ))}
              </select>
            </Row>
            <Row label="Open to the home network" help="For a database program on your PC.">
              <Switch on={lan} onChange={setLan} label={lan ? 'On' : 'Off'} />
            </Row>
          </div>
        </div>
        <aside className="card sum">
          <h4>{name || 'New database'}</h4>
          <dl>
            <dt>Type</dt>
            <dd>PostgreSQL {version}</dd>
            <dt>Tier</dt>
            <dd>{tierLabel(tier)}</dd>
            <dt>Memory</dt>
            <dd>{mem} MB</dd>
            <dt>Size</dt>
            <dd>{size} GB</dd>
            <dt>Backups</dt>
            <dd>daily, after 03:00</dd>
            <dt>Access</dt>
            <dd>{app ? app : 'connected apps only'}</dd>
          </dl>
          <Btn type="submit" kind="primary" disabled={!!step || !name || !!nameError}>
            Add the database
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
        </aside>
      </form>
    </>
  );
}
