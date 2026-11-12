// The data of the installer page (core/internal/installer, API /api/state).

export type Role = 'system' | 'performance' | 'standard' | 'none';

export interface Disk {
  name: string;
  path: string;
  model: string;
  kind: 'nvme' | 'ssd' | 'hdd';
  transport: string;
  size_bytes: number;
  now: string;
  usable: boolean;
  reason?: string;
  speed_mbs: number;
}

export interface NIC {
  name: string;
  driver?: string;
  speed_mbit?: number;
  carrier: boolean;
  addresses?: string[];
  wireless?: boolean;
}

export interface Facts {
  computer: string;
  cpu: string;
  threads: number;
  memory_bytes: number;
  firmware: string;
  network: NIC[] | null;
  version: string;
}

export interface Network {
  mode: 'dhcp' | 'static';
  interface?: string;
  address?: string;
  gateway?: string;
  dns?: string[];
}

export interface Answers {
  roles: Record<string, Role>;
  hostname: string;
  user: string;
  ssh_key: string;
  password: string;
  keyboard: string;
  timezone: string;
  network: Network;
  confirm: string;
}

export interface StepState {
  name: string;
  state: '' | 'run' | 'done' | 'failed';
  seconds: number;
}

export interface InstallerState {
  phase: 'starting' | 'measuring' | 'ready' | 'installing' | 'done' | 'failed';
  control: boolean;
  facts: Facts;
  disks: Disk[] | null;
  plan: { roles: Record<string, Role> | null; reasons: Record<string, string> | null; notes?: string[] };
  seed: { found: boolean; has_key: boolean; key_comment?: string };
  measuring?: string;
  steps: StepState[];
  error?: string;
  answers?: Answers;
  auto: boolean;
  plan_only?: boolean;
  log: string[] | null;
}

export const ROLES: Record<Role, string> = {
  system: 'System',
  performance: 'Performance tier',
  standard: 'Standard tier',
  none: 'Do not use',
};

export const ROLE_COLOR: Record<string, string> = { system: 'var(--sys)', performance: 'var(--perf)', standard: 'var(--std)' };

export const KIND: Record<string, string> = { nvme: 'NVMe', ssd: 'SSD', hdd: 'HDD' };

export const KEYBOARDS: [string, string][] = [
  ['us', 'English (US)'],
  ['gb', 'English (UK)'],
  ['it', 'Italian'],
  ['de', 'German'],
  ['fr', 'French'],
  ['es', 'Spanish'],
  ['ch', 'Swiss'],
  ['pt', 'Portuguese'],
];

export const TIMEZONES = [
  'Europe/Rome', 'Europe/London', 'Europe/Berlin', 'Europe/Paris', 'Europe/Madrid', 'Europe/Zurich', 'Europe/Lisbon',
  'America/New_York', 'America/Chicago', 'America/Los_Angeles', 'Asia/Tokyo', 'Australia/Sydney', 'UTC',
];

export const MIN_PASSWORD = 12;

// gb gives "931.5 GB" in units of 10^9, as the disk makers.
export function gb(bytes: number): string {
  const v = bytes / 1e9;
  return `${v >= 100 ? v.toFixed(0) : v.toFixed(1)} GB`;
}

// planProblem gives the problem of a plan, or "".
export function planProblem(roles: Record<string, Role>): string {
  const used = Object.values(roles).filter((r) => r !== 'none');
  if (!used.includes('system')) return 'One disk must have the System role.';
  if (new Set(used).size !== used.length) return 'Two disks have the same role. Give each role to one disk only.';
  return '';
}

export function samePlan(a: Record<string, Role>, b: Record<string, Role>): boolean {
  const keys = new Set([...Object.keys(a), ...Object.keys(b)]);
  for (const k of keys) if ((a[k] ?? 'none') !== (b[k] ?? 'none')) return false;
  return true;
}

export const HOST_RE = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;
export const USER_RE = /^[a-z_][a-z0-9_-]{0,31}$/;
export const KEY_RE = /^(ssh-(ed25519|rsa|dss)|ecdsa-sha2-nistp(256|384|521)|sk-(ssh-ed25519|ecdsa-sha2-nistp256)@openssh\.com) [A-Za-z0-9+/=]+( .*)?$/;
const IPV4 = /^(25[0-5]|2[0-4]\d|1?\d?\d)(\.(25[0-5]|2[0-4]\d|1?\d?\d)){3}$/;

export function ipv4(s: string): boolean {
  return IPV4.test(s.trim());
}

// accessProblems gives the problems of the access step, by field.
export function accessProblems(a: { hostname: string; user: string; keyMode: string; key: string; seedKey: boolean; password: string; repeat: string }) {
  const p: Record<string, string> = {};
  if (!HOST_RE.test(a.hostname)) p.hostname = 'Use 1 to 63 lowercase letters, digits and hyphens.';
  if (!USER_RE.test(a.user) || ['root', 'bin', 'daemon', 'nobody', 'kiosk', 'sshd'].includes(a.user)) p.user = 'Use lowercase letters, digits, - and _. Start with a letter.';
  if (a.keyMode === 'seed' && !a.seedKey) p.key = 'The seed volume NIDASEED has no key.';
  if (a.keyMode === 'paste' && !KEY_RE.test(a.key.trim())) p.key = 'Paste an OpenSSH public key: it starts with ssh-ed25519, ssh-rsa or ecdsa-.';
  if (a.password.length < MIN_PASSWORD) p.password = `Use ${MIN_PASSWORD} characters or more.`;
  else if (a.password !== a.repeat) p.repeat = 'The two passwords are not the same.';
  return p;
}

export function networkProblems(n: { mode: string; address: string; prefix: string; gateway: string; dns: string }) {
  const p: Record<string, string> = {};
  if (n.mode !== 'static') return p;
  const bits = Number(n.prefix);
  if (!ipv4(n.address)) p.address = 'Write an IPv4 address, for example 192.168.1.50.';
  if (!(bits >= 8 && bits <= 30)) p.prefix = 'Use a prefix from 8 to 30, usually 24.';
  if (!ipv4(n.gateway)) p.gateway = 'Write the address of the router, for example 192.168.1.1.';
  else if (!p.address && !p.prefix && !sameNet(n.address, n.gateway, bits)) p.gateway = 'The router must be in the network of the address.';
  for (const d of n.dns.split(/[\s,]+/).filter(Boolean)) if (!ipv4(d)) p.dns = 'Write IPv4 addresses, separated by spaces.';
  return p;
}

function toNum(ip: string): number {
  return ip.split('.').reduce((n, x) => n * 256 + Number(x), 0);
}

export function sameNet(a: string, b: string, bits: number): boolean {
  const mask = bits === 0 ? 0 : (0xffffffff << (32 - bits)) >>> 0;
  return ((toNum(a) & mask) >>> 0) === ((toNum(b) & mask) >>> 0);
}

export function clock(seconds: number): string {
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;
}
