// The overview page (task T-03): the live metrics, the items that need
// attention, the recent changes and the live log.

import type { Snapshot } from '../api';
import { attention, useData } from '../data';
import { ago, bytes, fixed, GiB, plural, shortSha } from '../format';
import { useLive, useNow } from '../live';
import { diskMBs, hostCpu, hostMemGB, netInMbit, series } from '../metrics';
import { Head, LinkBtn, LivePill, LogTail, Meter, Todo } from '../ui';

// HostMeters are the four meters of the computer.
export function HostMeters() {
  const live = useLive();
  const d = useData();
  const h = live.history;
  const last: Snapshot | undefined = h[h.length - 1];
  const stale = live.metrics.state === 'stopped';
  const sys = d.state?.system;
  const v = (f: (s: Snapshot) => number, digits = 0) => (last ? fixed(f(last), digits) : '–');
  return (
    <div className="meters">
      <Meter
        label="Processor"
        value={v(hostCpu)}
        unit="%"
        values={series(h, hostCpu)}
        max={100}
        stale={stale}
        sub={[last?.host.cpu.temperature_c !== undefined ? `${fixed(last.host.cpu.temperature_c)} °C` : '', sys ? `${sys.cpu.threads} threads` : ''].filter(Boolean).join(' · ')}
      />
      <Meter
        label="Memory"
        value={v(hostMemGB, 1)}
        unit="GB"
        values={series(h, hostMemGB)}
        max={last ? last.host.memory.total_bytes / GiB : undefined}
        stale={stale}
        sub={last ? `of ${bytes(last.host.memory.total_bytes)}` : ''}
      />
      <Meter label="Network in" value={v(netInMbit, 1)} unit="Mbit/s" values={series(h, netInMbit)} stale={stale} sub={last ? `${last.host.network.length} interface${last.host.network.length === 1 ? '' : 's'}` : ''} />
      <Meter
        label="Disk activity"
        value={v(diskMBs, 1)}
        unit="MB/s"
        values={series(h, diskMBs)}
        stale={stale}
        sub={`${plural(d.apps?.length ?? 0, 'app')} · ${plural(d.databases?.length ?? 0, 'database')}`}
      />
    </div>
  );
}

interface Change {
  key: string;
  when: string;
  title: string;
  text: string;
  tag: string;
}

function recentChanges(d: ReturnType<typeof useData>): Change[] {
  const out: Change[] = [];
  for (const a of d.apps ?? []) {
    for (const v of a.versions ?? []) {
      const what = v.rollback_of ? `Rollback of ${a.name} to version ${v.rollback_of}` : `Deploy of ${a.name}, version ${v.number}`;
      const result = { serving: 'in operation', deploying: 'in progress', failed: 'failed', replaced: 'replaced later' }[v.state];
      out.push({
        key: `${a.name}-${v.number}`,
        when: v.created,
        title: what,
        text: [v.commit ? shortSha(v.commit) : '', v.summary ?? '', result].filter(Boolean).join(' · '),
        tag: v.commit ? 'runner' : 'deploy',
      });
    }
  }
  for (const db of d.databases ?? []) {
    out.push({ key: `db-${db.name}`, when: db.created, title: `Database ${db.name} added`, text: `PostgreSQL ${db.version}`, tag: 'database' });
  }
  return out.sort((x, y) => Date.parse(y.when) - Date.parse(x.when)).slice(0, 5);
}

export function Overview() {
  const d = useData();
  const live = useLive();
  const now = useNow(10_000);
  const items = attention(d);
  const changes = recentChanges(d);
  const lead =
    d.apps === undefined
      ? 'Reading the state of the computer…'
      : items.length === 0
        ? 'All parts are in operation. Nothing needs your attention.'
        : `${items.length} item${items.length > 1 ? 's need' : ' needs'} your attention.`;
  return (
    <>
      <Head title="Overview" lead={lead} />
      <h3 style={{ marginTop: 0 }}>
        Now <LivePill status={live.metrics} />
      </h3>
      <HostMeters />
      {items.length > 0 && (
        <>
          <h3>Needs your attention</h3>
          <Todo
            items={items.map((it) => ({
              key: it.key,
              led: it.level,
              title: it.title,
              text: it.text,
              action: (
                <LinkBtn to={it.to} small>
                  {it.action}
                </LinkBtn>
              ),
            }))}
          />
        </>
      )}
      <h3>Recent changes</h3>
      {changes.length === 0 ? (
        <div className="card">
          <ul className="todo">
            <li className="muted">No changes yet. Add an app or a database.</li>
          </ul>
        </div>
      ) : (
        <Todo
          items={changes.map((c) => ({
            key: c.key,
            title: c.title,
            text: `${c.text ? c.text + ' · ' : ''}${ago(c.when, now)}`,
            action: <span className="tag">{c.tag}</span>,
          }))}
        />
      )}
      <LogTail n={8} />
    </>
  );
}
