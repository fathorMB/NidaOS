// The client of the NidaOS API (docs/api.md). The console and the API have
// the same origin (decision D29), thus the session cookie goes with each
// request.

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message);
  }
}

// signedOut is called when the API answers 401. The frame then shows the
// sign-in page.
let signedOut: () => void = () => {};
export function onSignedOut(fn: () => void) {
  signedOut = fn;
}

export async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, credentials: 'same-origin', headers: {} };
  if (body !== undefined) {
    init.body = JSON.stringify(body);
    (init.headers as Record<string, string>)['Content-Type'] = 'application/json';
  }
  const res = await fetch(path, init);
  if (res.status === 204) return undefined as T;
  const type = res.headers.get('Content-Type') ?? '';
  if (!res.ok) {
    let code = 'http_' + res.status;
    let message = res.statusText || 'The request failed.';
    if (type.includes('application/json')) {
      const e = (await res.json().catch(() => null)) as { code?: string; message?: string } | null;
      if (e?.code) code = e.code;
      if (e?.message) message = e.message;
    }
    if (res.status === 401 && path !== '/api/v1/auth/login') signedOut();
    throw new ApiError(res.status, code, message);
  }
  if (type.includes('application/json')) return (await res.json()) as T;
  return (await res.text()) as T;
}

export const get = <T>(path: string) => request<T>('GET', path);
export const post = <T>(path: string, body?: unknown) => request<T>('POST', path, body ?? {});
export const put = <T>(path: string, body?: unknown) => request<T>('PUT', path, body ?? {});
export const patch = <T>(path: string, body: unknown) => request<T>('PATCH', path, body);
export const del = <T = void>(path: string) => request<T>('DELETE', path);

export const enc = encodeURIComponent;

// ---- Sign-in (section 4.6) ----

export interface Me {
  kind: 'owner' | 'token' | 'local';
  name?: string;
  level: 'read' | 'admin';
  token_id?: string;
}

// ---- The computer (sections 4.1 and 4.2) ----

export interface Health {
  status: string;
  version: string;
  uptime: string;
}

export interface SystemInfo {
  hostname: string;
  os: { name: string; version: string; pretty_name: string; image_version?: string };
  kernel: string;
  uptime_seconds: number;
  cpu: { model: string; threads: number };
  memory: { total_bytes: number; available_bytes: number };
}

export interface Partition {
  name: string;
  size_bytes: number;
  mount_point?: string;
  fs_type?: string;
}

export interface Disk {
  name: string;
  model: string;
  size_bytes: number;
  kind: 'nvme' | 'ssd' | 'hdd';
  transport: string;
  removable: boolean;
  role: 'os' | 'performance' | 'standard' | 'unused' | '';
  partitions: Partition[];
}

export type TierName = 'performance' | 'standard';

export interface Tier {
  name: TierName;
  storage_class: string;
  path: string;
  disk: string;
  device?: string;
  state: 'ready' | 'not_mounted' | 'no_disk';
  usage?: { total_bytes: number; used_bytes: number; free_bytes: number };
}

export interface State {
  time: string;
  system: SystemInfo;
  disks: Disk[];
  tiers: Tier[];
}

// ---- Metrics (sections 4.3 and 5.2) ----

export interface Workload {
  kind: 'pod' | 'service';
  namespace?: string;
  name: string;
  id: string;
  cpu_cores: number;
  memory_bytes: number;
}

export interface DatabaseLive {
  name: string;
  state: string;
  size_bytes: number;
  connections: number;
  transactions_per_second: number;
  observed: string;
}

export interface Snapshot {
  seq: number;
  time: string;
  interval_seconds: number;
  host: {
    cpu: { usage_percent: number; threads: number[]; temperature_c?: number };
    memory: { total_bytes: number; used_bytes: number; available_bytes: number; used_percent: number };
    load: number[];
    disks: { name: string; read_bytes_per_second: number; write_bytes_per_second: number; busy_percent: number }[];
    network: { name: string; receive_bytes_per_second: number; transmit_bytes_per_second: number }[];
  };
  workloads: Workload[];
  databases?: DatabaseLive[];
}

// ---- Logs (sections 4.4, 4.5 and 5.3) ----

export type Level = 'debug' | 'info' | 'warning' | 'error';

export interface LogLine {
  seq: number;
  time: string;
  source: { kind: 'service' | 'pod'; id: string; name: string; namespace?: string; container?: string };
  level: Level;
  message: string;
}

export interface LogPage {
  lines: LogLine[];
  next_cursor?: string;
  partial?: boolean;
}

// ---- Apps (section 4.8) ----

export interface HealthCheck {
  path: string;
  interval_seconds?: number;
  timeout_seconds?: number;
}

export interface AppConfig {
  port?: number;
  health_check?: HealthCheck;
  variables?: Record<string, string>;
  limits?: { cpu_millis?: number; memory_bytes?: number };
}

export interface Folder {
  name: string;
  path: string;
  tier: TierName;
  size_bytes: number;
  moving_to?: TierName;
  move_error?: string;
}

export interface Version {
  number: number;
  image: string;
  commit?: string;
  summary?: string;
  rollback_of?: number;
  config: AppConfig;
  state: 'deploying' | 'serving' | 'failed' | 'replaced';
  reason?: string;
  created: string;
  finished?: string;
}

export type AppState = 'new' | 'deploying' | 'moving' | 'running' | 'starting' | 'stopped' | 'failed';

export interface App {
  name: string;
  repository?: string;
  branch?: string;
  config: AppConfig;
  copies: number;
  folders?: Folder[];
  active: number;
  versions?: Version[];
  created: string;
  updated: string;
  state: AppState;
  address?: string;
  secrets?: string[];
  status?: { copies: number; ready: number; version?: number; observed: string };
}

export interface NewApp {
  name: string;
  config: AppConfig;
  copies?: number;
  folders?: Folder[];
}

// ---- Databases (section 4.10) ----

export interface Link {
  app: string;
  mode: 'rw' | 'ro';
  role: string;
  created: string;
}

export interface Database {
  name: string;
  version: number;
  image: string;
  tier: TierName;
  size_bytes: number;
  cpu_millis: number;
  memory_bytes: number;
  state: 'creating' | 'restoring' | 'updating' | 'ready' | 'failed';
  reason?: string;
  lan: boolean;
  lan_port?: number;
  links?: Link[];
  created: string;
  updated: string;
  update_available?: string;
  update_to?: string;
  restore?: { database: string; backup: string };
  address?: string;
  live?: { size_bytes: number; connections: number; transactions_per_second: number; observed: string };
}

export interface NewDatabase {
  name: string;
  version?: number;
  tier?: TierName;
  size_bytes?: number;
  memory_bytes?: number;
  cpu_millis?: number;
  restore_from?: { database: string; backup: string };
}

export interface Backup {
  database: string;
  name: string;
  kind: 'daily' | 'manual' | 'update';
  created: string;
  version: number;
  size_bytes: number;
}

export interface Connection {
  host: string;
  port: number;
  database: string;
  user: string;
  password: string;
  sslmode: string;
  url: string;
  lan: boolean;
  note?: string;
}

// ---- GitHub runners (section 4.11) ----

export interface GitHubApp {
  configured: boolean;
  id?: number;
  slug?: string;
  name?: string;
  owner?: string;
  html_url?: string;
  install_url?: string;
}

export interface RepoConnection {
  app: string;
  owner: string;
  repo: string;
  branch: string;
  created: string;
  pull_request?: string;
  problem?: string;
}

export type JobState = 'queued' | 'running' | 'succeeded' | 'failed' | 'cancelled';

export interface Job {
  id: number;
  app: string;
  run_id: number;
  name: string;
  sha: string;
  url: string;
  state: JobState;
  pod?: string;
  runner?: string;
  created: string;
  started?: string;
  finished?: string;
  message?: string;
  has_log?: boolean;
}

export interface Runners {
  github: GitHubApp;
  connections: RepoConnection[];
  active: Job[];
}

// ---- Updates (section 4.12) ----

export interface Deployment {
  image: string;
  version?: string;
  timestamp?: string;
  digest?: string;
  pinned?: boolean;
}

export interface Updates {
  booted?: Deployment;
  staged?: Deployment;
  rollback?: Deployment;
  available?: Deployment;
  rollback_queued?: boolean;
  busy?: 'checking' | 'downloading' | 'rolling_back' | 'restarting';
  last_check?: string;
  error?: string;
}
