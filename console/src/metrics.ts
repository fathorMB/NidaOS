// The values of the live metrics for the meters.

import type { Snapshot, Workload } from './api';
import { GiB, MiB, mbits } from './format';

export const hostCpu = (s: Snapshot) => s.host.cpu.usage_percent;
export const hostMemGB = (s: Snapshot) => s.host.memory.used_bytes / GiB;
export const netInMbit = (s: Snapshot) => mbits(s.host.network.reduce((n, i) => n + i.receive_bytes_per_second, 0));
export const netOutMbit = (s: Snapshot) => mbits(s.host.network.reduce((n, i) => n + i.transmit_bytes_per_second, 0));
export const diskMBs = (s: Snapshot) => s.host.disks.reduce((n, d) => n + d.read_bytes_per_second + d.write_bytes_per_second, 0) / MiB;

// The pods of an app are in the namespace nida-app-<name>, the pods of a
// database in nida-db-<name>.
export const appNamespace = (app: string) => `nida-app-${app}`;
export const dbNamespace = (db: string) => `nida-db-${db}`;

export function inNamespace(s: Snapshot, ns: string): Workload[] {
  return s.workloads.filter((w) => w.kind === 'pod' && w.namespace === ns);
}

// nsCpu gives the processor use of a namespace in percent of one thread.
export const nsCpu = (ns: string) => (s: Snapshot) => inNamespace(s, ns).reduce((n, w) => n + w.cpu_cores, 0) * 100;
export const nsMemMB = (ns: string) => (s: Snapshot) => inNamespace(s, ns).reduce((n, w) => n + w.memory_bytes, 0) / MiB;

export function dbLive(s: Snapshot, name: string) {
  return s.databases?.find((d) => d.name === name);
}

// series gives the values of the history for one metric.
export function series(history: Snapshot[], f: (s: Snapshot) => number | undefined): number[] {
  const out: number[] = [];
  for (const s of history) {
    const v = f(s);
    if (v !== undefined && Number.isFinite(v)) out.push(v);
  }
  return out;
}
