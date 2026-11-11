// The shared data of the pages: the lists that the menu and the overview
// use. The console reads them again each 4 seconds. The metrics and the
// logs come from the live streams (live.tsx).

import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from 'react';
import { ApiError, get, type App, type Database, type Health, type Me, type Runners, type State, type Updates } from './api';

export interface Data {
  me: Me;
  admin: boolean;
  health?: Health;
  state?: State;
  apps?: App[];
  databases?: Database[];
  versions?: number[];
  runners?: Runners;
  // runnersOff: nida-core has no runner part.
  runnersOff?: boolean;
  updates?: Updates;
  updatesOff?: boolean;
  updatesError?: string;
  // loadedAt is the time of the last complete read.
  loadedAt?: number;
  reload: () => void;
}

const DataContext = createContext<Data | null>(null);

export const POLL_MS = 4000;

async function optional<T>(path: string): Promise<{ v?: T; off?: boolean; err?: string }> {
  try {
    return { v: await get<T>(path) };
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) return { off: true };
    return { err: e instanceof Error ? e.message : String(e) };
  }
}

export function DataProvider({ me, children }: { me: Me; children: ReactNode }) {
  const [d, setD] = useState<Omit<Data, 'reload' | 'me' | 'admin'>>({});
  const [tick, setTick] = useState(0);
  const reload = useCallback(() => setTick((n) => n + 1), []);
  const running = useRef(false);

  useEffect(() => {
    let stop = false;
    const load = async () => {
      if (running.current) return;
      running.current = true;
      try {
        const [health, state, apps, dbs, runners, updates] = await Promise.all([
          optional<Health>('/api/v1/health'),
          optional<State>('/api/v1/state'),
          optional<{ apps: App[] }>('/api/v1/apps'),
          optional<{ databases: Database[]; versions: number[] }>('/api/v1/databases'),
          optional<Runners>('/api/v1/runners'),
          optional<Updates>('/api/v1/updates'),
        ]);
        if (stop) return;
        setD((old) => ({
          health: health.v ?? old.health,
          state: state.v ?? old.state,
          apps: apps.v?.apps ?? (apps.off ? [] : old.apps),
          databases: dbs.v?.databases ?? (dbs.off ? [] : old.databases),
          versions: dbs.v?.versions ?? old.versions,
          runners: runners.v ?? old.runners,
          runnersOff: runners.off,
          updates: updates.v ?? old.updates,
          updatesOff: updates.off,
          updatesError: updates.err,
          loadedAt: Date.now(),
        }));
      } finally {
        running.current = false;
      }
    };
    load();
    const t = setInterval(load, POLL_MS);
    return () => {
      stop = true;
      clearInterval(t);
    };
  }, [tick]);

  return <DataContext.Provider value={{ ...d, me, admin: me.level === 'admin', reload }}>{children}</DataContext.Provider>;
}

export function useData(): Data {
  const d = useContext(DataContext);
  if (!d) throw new Error('useData needs a DataProvider');
  return d;
}

// Attention is one item of "Needs your attention" on the overview.
export interface Attention {
  key: string;
  level: 'warn' | 'bad';
  title: string;
  text: string;
  to: string;
  action: string;
}

// attention finds the items that need the owner.
export function attention(d: Pick<Data, 'apps' | 'databases' | 'runners' | 'updates' | 'state'>): Attention[] {
  const out: Attention[] = [];
  for (const t of d.state?.tiers ?? []) {
    if (t.state === 'not_mounted') {
      out.push({ key: `tier-${t.name}`, level: 'bad', title: `The ${t.name} tier is not mounted`, text: `The disk of the tier is not on ${t.path}.`, to: '/storage', action: 'Open Storage' });
    } else if (t.usage && t.usage.total_bytes > 0 && t.usage.used_bytes / t.usage.total_bytes >= 0.85) {
      const pct = Math.round((t.usage.used_bytes / t.usage.total_bytes) * 100);
      out.push({ key: `tier-${t.name}`, level: 'warn', title: `The ${t.name} tier is ${pct}% full`, text: 'Move a folder or remove old data.', to: '/storage', action: 'Open Storage' });
    }
  }
  if (d.updates?.staged) {
    out.push({ key: 'update-staged', level: 'warn', title: `Update ${d.updates.staged.version ?? ''} is ready`.replace('  ', ' '), text: 'The installation restarts the computer. The apps stop for some minutes.', to: '/updates', action: 'Open Updates' });
  } else if (d.updates?.available) {
    out.push({ key: 'update-available', level: 'warn', title: `Update ${d.updates.available.version ?? ''} is available`.replace('  ', ' '), text: 'Download it, then restart the computer.', to: '/updates', action: 'Open Updates' });
  }
  for (const a of d.apps ?? []) {
    const last = a.versions?.[a.versions.length - 1];
    if (a.state === 'failed') {
      out.push({ key: `app-${a.name}`, level: 'bad', title: `${a.name} is not in operation`, text: last?.reason ?? 'No version passed the health check.', to: `/apps/${a.name}`, action: `Open ${a.name}` });
    } else if (last?.state === 'failed' && last.number > a.active) {
      out.push({ key: `app-${a.name}`, level: 'warn', title: `The last deploy of ${a.name} failed`, text: `${last.reason ?? 'No answer on the health check.'} Version ${a.active} stays in operation.`, to: `/apps/${a.name}?tab=deploys`, action: `Open ${a.name}` });
    }
    for (const f of a.folders ?? []) {
      if (f.move_error) out.push({ key: `move-${a.name}-${f.name}`, level: 'warn', title: `The move of the folder ${f.name} of ${a.name} failed`, text: f.move_error, to: `/apps/${a.name}?tab=resources`, action: `Open ${a.name}` });
    }
  }
  for (const db of d.databases ?? []) {
    if (db.state === 'failed') {
      out.push({ key: `db-${db.name}`, level: 'bad', title: `${db.name} failed`, text: db.reason ?? 'The database does not start.', to: `/databases/${db.name}`, action: `Open ${db.name}` });
    } else if (db.reason) {
      out.push({ key: `db-${db.name}`, level: 'warn', title: `The last operation on ${db.name} failed`, text: db.reason, to: `/databases/${db.name}`, action: `Open ${db.name}` });
    } else if (db.update_available) {
      out.push({ key: `dbup-${db.name}`, level: 'warn', title: `An update is available for ${db.name}`, text: `PostgreSQL ${db.image.split(':').pop()?.replace('-alpine', '')} → ${db.update_available.split(':').pop()?.replace('-alpine', '')}. NidaOS makes a backup first.`, to: `/databases/${db.name}?tab=settings`, action: `Open ${db.name}` });
    }
  }
  for (const c of d.runners?.connections ?? []) {
    if (c.problem) out.push({ key: `repo-${c.app}`, level: 'warn', title: `The repository of ${c.app} has a problem`, text: c.problem, to: '/runners', action: 'Open Runners' });
  }
  return out;
}
