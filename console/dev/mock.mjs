// A fake NidaOS API with sample data, for the development of the console
// and for the browser tests without a NidaOS computer.
//
//   node dev/mock.mjs [port]      then open http://127.0.0.1:<port>/
//
// It serves the built console (core/internal/console/dist) and the paths
// of docs/api.md that the console uses. The password is "mock password".

import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import { extname, join, normalize } from 'node:path';
import { fileURLToPath } from 'node:url';

const port = Number(process.argv[2] ?? process.env.PORT ?? 8480);
// MOCK_TTL: the fake API stops after this number of seconds (for the scripts).
if (process.env.MOCK_TTL) setTimeout(() => process.exit(0), Number(process.env.MOCK_TTL) * 1000).unref();
const dist = fileURLToPath(new URL('../../core/internal/console/dist/', import.meta.url));
const PASSWORD = 'mock password';
const GiB = 1024 ** 3;
const MiB = 1024 ** 2;
const now = () => new Date().toISOString();
const before = (ms) => new Date(Date.now() - ms).toISOString();

let session = null;
let seq = 0;
let logSeq = 0;

const state = {
  time: now(),
  system: {
    time: now(), hostname: 'nida', os: { name: 'Fedora Linux', version: '45', pretty_name: 'NidaOS 45', image_version: '45.20261111.0' },
    kernel: '6.17.4-300.fc45.x86_64', uptime_seconds: 900000, cpu: { model: 'Intel(R) Core(TM) i5-8500T', threads: 6 },
    memory: { total_bytes: 32 * GiB, available_bytes: 20 * GiB }, load: { one: 0.4, five: 0.3, fifteen: 0.3 },
  },
  disks: [
    { name: 'sda', model: 'Kingston SA400', size_bytes: 120 * GiB, kind: 'ssd', transport: 'sata', removable: false, read_only: false, role: 'os', partitions: [{ name: 'sda3', size_bytes: 118 * GiB, mount_point: '/sysroot', fs_type: 'xfs' }] },
    { name: 'nvme0n1', model: 'Samsung SSD 980 1TB', size_bytes: 1000 * GiB, kind: 'nvme', transport: 'nvme', removable: false, read_only: false, role: 'performance', partitions: [{ name: 'nvme0n1p1', size_bytes: 999 * GiB, mount_point: '/var/nida/performance', fs_type: 'xfs' }] },
    { name: 'sdb', model: 'Seagate ST1000DM', size_bytes: 931 * GiB, kind: 'hdd', transport: 'sata', removable: false, read_only: false, role: 'standard', partitions: [{ name: 'sdb1', size_bytes: 931 * GiB, mount_point: '/var/nida/standard', fs_type: 'xfs' }] },
  ],
  tiers: [
    { name: 'performance', storage_class: 'nida-performance', path: '/var/nida/performance', disk: 'nvme0n1', device: 'nvme0n1p1', fs_type: 'xfs', state: 'ready', usage: { total_bytes: 999 * GiB, used_bytes: 64 * GiB, free_bytes: 935 * GiB } },
    { name: 'standard', storage_class: 'nida-standard', path: '/var/nida/standard', disk: 'sdb', device: 'sdb1', fs_type: 'xfs', state: 'ready', usage: { total_bytes: 931 * GiB, used_bytes: 212 * GiB, free_bytes: 719 * GiB } },
  ],
};

const version = (n, st, commit, summary, ago, reason) => ({
  number: n, image: `localhost:5000/app:${commit}`, commit, summary, config: {}, state: st, reason,
  created: before(ago), finished: st === 'deploying' ? undefined : before(ago - 70_000),
});

const apps = [
  {
    name: 'shop-web', repository: 'example/shop-web', branch: 'main', copies: 1, active: 4, created: before(40 * 864e5), updated: before(360_000),
    config: { port: 8080, health_check: { path: '/healthz' }, variables: { LOG_LEVEL: 'info' }, limits: { cpu_millis: 1000, memory_bytes: 1024 * MiB } },
    folders: [], secrets: ['PAYMENT_API_KEY'], state: 'running', address: 'https://shop-web.nidaos.local',
    versions: [version(2, 'replaced', 'e41f0aa', 'Correct the price format', 9 * 864e5), version(3, 'failed', 'a93d210', 'Try the new cache', 8 * 864e5, 'the health check did not pass in 120 seconds'), version(4, 'serving', '7c1d9a2', 'Show the stock quantity on the product page', 360_000)],
    status: { copies: 1, ready: 1, version: 4, observed: now() },
  },
  {
    name: 'photos', repository: 'example/photos', branch: 'main', copies: 1, active: 2, created: before(20 * 864e5), updated: before(5 * 864e5),
    config: { port: 3000, health_check: { path: '/' }, limits: { memory_bytes: 2048 * MiB } },
    folders: [{ name: 'data', path: '/data', tier: 'standard', size_bytes: 10 * GiB }], secrets: [], state: 'running', address: 'https://photos.nidaos.local',
    versions: [version(1, 'replaced', '0d3b5c8', 'First version', 20 * 864e5), version(2, 'serving', '19ac77d', 'Add the albums', 5 * 864e5)],
    status: { copies: 1, ready: 1, version: 2, observed: now() },
  },
];

const dbs = [
  {
    name: 'orders-db', version: 18, image: 'docker.io/library/postgres:18.1-alpine', tier: 'performance', size_bytes: 40 * GiB, cpu_millis: 500, memory_bytes: 3 * GiB,
    state: 'ready', lan: false, links: [{ app: 'shop-web', mode: 'rw', role: 'l_shop_web', created: before(30 * 864e5) }], created: before(30 * 864e5), updated: before(864e5),
    update_available: 'docker.io/library/postgres:18.2-alpine', live: { size_bytes: 22 * GiB, connections: 18, transactions_per_second: 40, observed: now() },
  },
];

const backups = {
  'orders-db': [
    { database: 'orders-db', name: '20261111T030000Z-daily-pg18.dump', kind: 'daily', created: before(18 * 3600e3), version: 18, size_bytes: 3.1 * GiB },
    { database: 'orders-db', name: '20261110T030000Z-daily-pg18.dump', kind: 'daily', created: before(42 * 3600e3), version: 18, size_bytes: 3.1 * GiB },
  ],
};

const runners = {
  github: { configured: true, id: 1234567, slug: 'nidaos-nida-1a2b3c', name: 'NidaOS nida 1a2b3c', owner: 'example', html_url: 'https://github.com/apps/nidaos-nida-1a2b3c', install_url: 'https://github.com/apps/nidaos-nida-1a2b3c/installations/new' },
  connections: [{ app: 'shop-web', owner: 'example', repo: 'shop-web', branch: 'main', created: before(30 * 864e5), pull_request: 'https://github.com/example/shop-web/pull/1', problem: '' }],
  active: [],
};

const jobs = {
  'shop-web': [
    { id: 55500011122, app: 'shop-web', run_id: 1900000001, name: 'deploy', sha: '7c1d9a2a0000000000000000000000000000000', url: 'https://github.com/example/shop-web/actions/runs/1/job/2', state: 'succeeded', pod: 'run-shop-web-1a', runner: 'run-shop-web-1a', created: before(430_000), started: before(420_000), finished: before(348_000), has_log: true },
  ],
};

const updates = {
  booted: { image: 'ghcr.io/fathormb/nidaos:proof', version: '45.20261029.0', timestamp: before(13 * 864e5), digest: 'sha256:aa' },
  staged: { image: 'ghcr.io/fathormb/nidaos:proof', version: '45.20261111.0', timestamp: before(864e5), digest: 'sha256:bb' },
  rollback: { image: 'ghcr.io/fathormb/nidaos:proof', version: '45.20261014.0', timestamp: before(28 * 864e5), digest: 'sha256:99' },
  last_check: before(3600e3),
};

const tokens = [
  { id: 'tok1', name: 'laptop', level: 'agent', hint: 'nida_ab12', created: before(10 * 864e5), last_used: before(480_000), agent: 'Claude Code',
    rules: { read: 'allow', apps: 'allow', databases: 'allow', delete: 'ask', updates: 'ask', secrets: 'ask' } },
  { id: 'tok2', name: 'home-assistant', level: 'read', hint: 'nida_cd34', created: before(30 * 864e5) },
];
const approvals = [
  { id: 'ap1', token_id: 'tok1', token: 'laptop', agent: 'Claude Code', class: 'delete', method: 'DELETE', path: '/api/v1/apps/photos',
    summary: 'Remove the app photos with its folders', state: 'pending', created: before(120_000) },
];
const actions = [
  { time: before(120_000), token_id: 'tok1', token: 'laptop', agent: 'Claude Code', class: 'delete', method: 'DELETE', path: '/api/v1/apps/photos', status: 202, approval: 'ap1', outcome: 'asked' },
  { time: before(2_640_000), token_id: 'tok1', token: 'laptop', agent: 'Claude Code', class: 'apps', method: 'PATCH', path: '/api/v1/apps/shop-web', status: 200, outcome: 'done' },
];

const pool = [
  ['info', 'pod', 'nida-app-shop-web', 'web', 'GET / 200 in 8 ms'],
  ['info', 'pod', 'nida-db-orders-db', 'postgres', 'checkpoint complete: wrote 96 buffers'],
  ['warning', 'pod', 'nida-app-shop-web', 'web', 'The connection pool to orders-db is 85% full (17 of 20)'],
  ['info', 'pod', 'nida-app-photos', 'web', 'Thumbnail made for IMG_4480.jpg'],
  ['info', 'service', '', 'k3s', 'Started the k3s agent'],
  ['error', 'pod', 'nida-app-shop-web', 'web', 'POST /checkout 500: timeout after 5000 ms waiting for orders-db'],
  ['info', 'service', '', 'nida-core', 'a request'],
];

function logLine() {
  const [level, kind, ns, name, message] = pool[logSeq % pool.length];
  logSeq++;
  const source = kind === 'pod'
    ? { kind, id: `pod/${ns}/${name}-7d9f/${name}`, name: `${name}-7d9f`, namespace: ns, container: name }
    : { kind, id: `service/${name}`, name };
  return { seq: logSeq, time: now(), source, level, message };
}
const logBuffer = Array.from({ length: 40 }, logLine);

function snapshot() {
  seq++;
  const w = (n) => n + (Math.random() - 0.5) * n * 0.4;
  return {
    seq, time: now(), interval_seconds: 2,
    host: {
      cpu: { usage_percent: w(14), threads: [10, 15, 8, 20, 9, 11], temperature_c: 47 },
      memory: { total_bytes: 32 * GiB, used_bytes: w(11.4) * GiB, available_bytes: 20 * GiB, used_percent: 35 },
      load: [0.4, 0.35, 0.3],
      disks: [{ name: 'sda', read_bytes_per_second: 0, write_bytes_per_second: w(40960), busy_percent: 1 }, { name: 'nvme0n1', read_bytes_per_second: w(2e6), write_bytes_per_second: w(1e6), busy_percent: 4 }, { name: 'sdb', read_bytes_per_second: 0, write_bytes_per_second: w(5e5), busy_percent: 3 }],
      network: [{ name: 'enp1s0', receive_bytes_per_second: w(400000), transmit_bytes_per_second: w(200000) }],
    },
    workloads: [
      { kind: 'pod', namespace: 'nida-app-shop-web', name: 'web-1', id: 'u1', cpu_cores: w(0.04), memory_bytes: w(412) * MiB },
      { kind: 'pod', namespace: 'nida-app-photos', name: 'web-1', id: 'u2', cpu_cores: w(0.01), memory_bytes: w(980) * MiB },
      { kind: 'pod', namespace: 'nida-db-orders-db', name: 'pg-0', id: 'u3', cpu_cores: w(0.08), memory_bytes: w(2900) * MiB },
    ],
    databases: [{ name: 'orders-db', state: 'ready', size_bytes: 22 * GiB, connections: Math.round(w(18)), transactions_per_second: w(40), observed: now() }],
  };
}

const json = (res, status, body) => {
  res.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' });
  res.end(body === undefined ? '' : JSON.stringify(body));
};
const err = (res, status, code, message) => json(res, status, { code, message });
const readBody = (req) => new Promise((ok) => {
  let b = '';
  req.on('data', (c) => (b += c));
  req.on('end', () => { try { ok(b ? JSON.parse(b) : {}); } catch { ok({}); } });
});
const cookie = (req) => /nida_session=([^;]+)/.exec(req.headers.cookie ?? '')?.[1];

function sse(req, res, kind) {
  res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-store', Connection: 'keep-alive' });
  const send = (event, id, data) => res.write(`id: ${id}\nevent: ${event}\ndata: ${JSON.stringify(data)}\n\n`);
  let t;
  if (kind === 'metrics') {
    send('metrics', seq, snapshot());
    t = setInterval(() => { const s = snapshot(); send('metrics', s.seq, s); }, 2000);
  } else {
    for (const l of logBuffer.slice(-200)) send('log', l.seq, l);
    t = setInterval(() => { const l = logLine(); logBuffer.push(l); send('log', l.seq, l); }, 1500);
  }
  req.on('close', () => clearInterval(t));
}

async function api(req, res, url) {
  const p = url.pathname;
  const m = req.method;
  if (p === '/api/v1/health') return json(res, 200, { status: 'ok', version: '45.20261111.0', uptime: '250h' });
  if (p === '/api/v1/auth/status') return json(res, 200, { owner_set: true });
  if (p === '/api/v1/auth/login' && m === 'POST') {
    const b = await readBody(req);
    if (b.password !== PASSWORD) return err(res, 401, 'wrong_password', 'The password is not correct.');
    session = Math.random().toString(36).slice(2);
    res.setHeader('Set-Cookie', `nida_session=${session}; Path=/; HttpOnly; SameSite=Strict`);
    return json(res, 200, { expires: before(-30 * 864e5) });
  }
  if (!session || cookie(req) !== session) return err(res, 401, 'sign_in_required', 'Sign in.');
  if (p === '/api/v1/auth/logout') { session = null; res.setHeader('Set-Cookie', 'nida_session=; Path=/; Max-Age=0'); return json(res, 204); }
  if (p === '/api/v1/auth/me') return json(res, 200, { kind: 'owner', level: 'admin' });
  if (p === '/api/v1/state') return json(res, 200, { ...state, time: now() });
  if (p === '/api/v1/metrics/stream') return sse(req, res, 'metrics');
  if (p === '/api/v1/logs/stream') return sse(req, res, 'logs');
  if (p === '/api/v1/logs/search') return json(res, 200, { lines: logBuffer.slice(0, 20).reverse(), next_cursor: undefined });
  if (p === '/api/v1/apps' && m === 'GET') return json(res, 200, { apps });
  if (p === '/api/v1/apps' && m === 'POST') {
    const b = await readBody(req);
    if (apps.some((a) => a.name === b.name)) return err(res, 409, 'app_exists', 'An app with this name exists.');
    const a = { name: b.name, config: b.config ?? {}, copies: 1, folders: b.folders ?? [], active: 0, versions: [], secrets: [], created: now(), updated: now(), state: 'new', address: b.config?.port ? `https://${b.name}.nidaos.local` : undefined };
    apps.push(a);
    return json(res, 201, a);
  }
  let r = /^\/api\/v1\/apps\/([^/]+)(\/.*)?$/.exec(p);
  if (r) {
    const a = apps.find((x) => x.name === decodeURIComponent(r[1]));
    if (!a) return err(res, 404, 'app_not_found', 'The app does not exist.');
    const rest = r[2] ?? '';
    if (rest === '' && m === 'GET') return json(res, 200, a);
    if (rest === '' && m === 'PATCH') { const b = await readBody(req); Object.assign(a, b.config ? { config: b.config } : {}, b.copies !== undefined ? { copies: b.copies, state: b.copies === 0 ? 'stopped' : 'running' } : {}); return json(res, 200, a); }
    if (rest === '' && m === 'DELETE') { apps.splice(apps.indexOf(a), 1); return json(res, 204); }
    if (rest === '/repository') {
      const c = runners.connections.find((x) => x.app === a.name);
      if (m === 'GET') return c ? json(res, 200, c) : err(res, 404, 'repository_not_connected', 'The app has no repository.');
    }
    if (rest === '/jobs') return json(res, 200, { jobs: jobs[a.name] ?? [] });
    if (m === 'POST' || m === 'PUT' || m === 'DELETE') return json(res, m === 'POST' ? 202 : 204, m === 'POST' ? a : undefined);
  }
  if (p === '/api/v1/databases' && m === 'GET') return json(res, 200, { databases: dbs, versions: [17, 18] });
  r = /^\/api\/v1\/databases\/([^/]+)(\/.*)?$/.exec(p);
  if (r) {
    const d = dbs.find((x) => x.name === decodeURIComponent(r[1]));
    if (!d) return err(res, 404, 'database_not_found', 'The database does not exist.');
    const rest = r[2] ?? '';
    if (rest === '' && m === 'GET') return json(res, 200, d);
    if (rest === '/backups' && m === 'GET') return json(res, 200, { backups: backups[d.name] ?? [] });
    if (rest === '/connection') return json(res, 200, { host: 'nidaos.local', port: 15432, database: 'orders_db', user: 'orders_db_owner', password: 'mock-not-a-secret', sslmode: 'verify-full', url: 'postgresql://orders_db_owner:mock-not-a-secret@nidaos.local:15432/orders_db?sslmode=verify-full', lan: d.lan });
    return json(res, 202, d);
  }
  if (p === '/api/v1/runners') return json(res, 200, runners);
  if (p === '/api/v1/tokens' && m === 'GET') return json(res, 200, { tokens });
  if (p === '/api/v1/tokens' && m === 'POST') {
    const b = await readBody(req);
    const t = { id: 'tok' + (tokens.length + 1), name: b.name, level: b.level, hint: 'nida_new1', created: now(), agent: b.agent, rules: b.rules };
    tokens.push(t);
    return json(res, 201, { ...t, secret: 'nida_mock_secret_for_the_test' });
  }
  let t = /^\/api\/v1\/tokens\/([^/]+)$/.exec(p);
  if (t) {
    const tk = tokens.find((x) => x.id === t[1]);
    if (!tk) return err(res, 404, 'token_not_found', 'The token does not exist.');
    if (m === 'PATCH') { tk.rules = { ...tk.rules, ...(await readBody(req)).rules }; return json(res, 200, tk); }
    if (m === 'DELETE') { tokens.splice(tokens.indexOf(tk), 1); return json(res, 204); }
  }
  if (p === '/api/v1/approvals') {
    const st = url.searchParams.get('state');
    return json(res, 200, { approvals: approvals.filter((a) => !st || a.state === st) });
  }
  t = /^\/api\/v1\/approvals\/([^/]+)\/(approve|deny)$/.exec(p);
  if (t && m === 'POST') {
    const a = approvals.find((x) => x.id === t[1]);
    if (!a) return err(res, 404, 'approval_not_found', 'The approval request does not exist.');
    if (a.state !== 'pending') return err(res, 409, 'approval_not_pending', 'The approval request is not pending.');
    Object.assign(a, t[2] === 'approve' ? { state: 'done', status: 204 } : { state: 'denied' }, { decided: now(), by: 'owner:owner' });
    actions.unshift({ time: now(), token_id: a.token_id, token: a.token, agent: a.agent, class: a.class, method: a.method, path: a.path, status: a.status ?? 403, approval: a.id, outcome: t[2] === 'approve' ? 'done' : 'denied' });
    return json(res, 200, a);
  }
  if (p === '/api/v1/agents/actions') return json(res, 200, { actions });
  if (p === '/api/v1/updates') return json(res, 200, updates);
  if (p.startsWith('/api/v1/updates/')) return json(res, 202, updates);
  if (/^\/api\/v1\/jobs\/\d+\/log$/.test(p)) { res.writeHead(200, { 'Content-Type': 'text/plain' }); return res.end('2026-11-11T20:00:00Z Build and deploy on NidaOS\n#1 [internal] load build definition\n#9 DONE 12.1s\nThe deploy passed: version 4 is in operation.\n'); }
  return err(res, 404, 'not_found', 'The path does not exist.');
}

const types = { '.js': 'text/javascript', '.css': 'text/css', '.html': 'text/html', '.svg': 'image/svg+xml' };

createServer(async (req, res) => {
  const url = new URL(req.url, 'http://x');
  if (url.pathname.startsWith('/api/')) return api(req, res, url);
  const file = normalize(join(dist, url.pathname)).startsWith(dist) && extname(url.pathname) ? join(dist, url.pathname) : join(dist, 'index.html');
  try {
    const b = await readFile(file);
    res.writeHead(200, { 'Content-Type': types[extname(file)] ?? 'application/octet-stream' });
    res.end(b);
  } catch {
    res.writeHead(404);
    res.end();
  }
}).listen(port, '127.0.0.1', () => console.log(`mock NidaOS API on http://127.0.0.1:${port}/`));
