// The storage page (task T-06): the tiers, the disks and their live
// activity.

import type { Disk, Snapshot, Tier as TierT } from '../api';
import { useData } from '../data';
import { bytes, fixed, MiB, tierLabel } from '../format';
import { useLive } from '../live';
import { series } from '../metrics';
import { Bar, Head, Led, LivePill, Meter, Note, Table, Tier, type Column } from '../ui';

const roleText: Record<string, string> = { os: 'System', performance: 'Performance', standard: 'Standard', unused: 'Not used', '': 'Not in the plan' };
const kindText: Record<string, string> = { nvme: 'NVMe SSD', ssd: 'SSD', hdd: 'HDD' };

function diskText(disks: Disk[], name?: string) {
  const d = disks.find((x) => x.name === name);
  if (!d) return name ?? '–';
  return `${d.model || d.name} · ${kindText[d.kind] ?? d.kind}`;
}

export function StoragePage() {
  const d = useData();
  const live = useLive();
  const disks = d.state?.disks ?? [];
  const tiers = d.state?.tiers;
  const contains = (t: TierT['name']) => {
    const folders = (d.apps ?? []).flatMap((a) => a.folders ?? []).filter((f) => f.tier === t).length;
    const dbs = (d.databases ?? []).filter((x) => x.tier === t).length;
    const parts = [];
    if (dbs) parts.push(`${dbs} database${dbs > 1 ? 's' : ''}`);
    if (folders) parts.push(`${folders} app folder${folders > 1 ? 's' : ''}`);
    if (t === 'standard') parts.push('backups');
    return parts.join(', ') || 'nothing yet';
  };
  const cols: Column<TierT>[] = [
    {
      title: 'Tier',
      cell: (t) => (
        <b>
          <Tier name={t.name} />
        </b>
      ),
    },
    { title: 'Disk', cell: (t) => diskText(disks, t.disk) },
    {
      title: 'Used',
      cell: (t) =>
        t.usage ? (
          <div className="inline">
            <div style={{ width: 120 }}>
              <Bar pct={(t.usage.used_bytes / t.usage.total_bytes) * 100} />
            </div>
            <span className="num">
              {bytes(t.usage.used_bytes)} / {bytes(t.usage.total_bytes)}
            </span>
          </div>
        ) : (
          <span className="muted">–</span>
        ),
    },
    {
      title: 'State',
      cell: (t) =>
        t.state === 'ready' ? <Led>Mounted</Led> : t.state === 'not_mounted' ? <Led kind="bad">Not mounted</Led> : <Led kind="off">No disk in the plan</Led>,
    },
    { title: 'Contains', cell: (t) => contains(t.name), className: 'wrap' },
  ];
  const osDisk = disks.find((x) => x.role === 'os');
  const busy = (name: string) => (s: Snapshot) => s.host.disks.find((x) => x.name === name)?.busy_percent;
  const io = (name: string) => (s: Snapshot) => {
    const x = s.host.disks.find((y) => y.name === name);
    return x ? (x.read_bytes_per_second + x.write_bytes_per_second) / MiB : undefined;
  };
  const last = live.history[live.history.length - 1];
  const liveDisks = disks.filter((x) => !x.removable);
  return (
    <>
      <Head title="Storage" lead="The installer gave a role to each disk. Apps and databases select a tier, not a disk." />
      <Table label="Tiers" cols={cols} rows={tiers} rowKey={(t) => t.name} empty="The disk plan has no tiers." />
      {osDisk && (
        <Note kind="ok">
          The system is on {diskText(disks, osDisk.name)} ({bytes(osDisk.size_bytes)}). It keeps two versions of NidaOS for a rollback.
        </Note>
      )}
      <h3>
        Disk activity <LivePill status={live.metrics} />
      </h3>
      <div className="meters">
        {liveDisks.map((x) => (
          <Meter
            key={x.name}
            label={x.name}
            value={last ? fixed(io(x.name)(last), 1) : '–'}
            unit="MB/s"
            values={series(live.history, io(x.name))}
            stale={live.metrics.state === 'stopped'}
            sub={`${roleText[x.role] ?? x.role} · busy ${last ? fixed(busy(x.name)(last)) : '–'}%`}
          />
        ))}
      </div>
      <h3>Disks</h3>
      <Table
        label="Disks"
        cols={[
          { title: 'Disk', cell: (x: Disk) => <b className="mono">{x.name}</b> },
          { title: 'Model', cell: (x) => x.model || <span className="muted">–</span> },
          { title: 'Type', cell: (x) => `${kindText[x.kind] ?? x.kind} · ${x.transport}` },
          { title: 'Size', cell: (x) => <span className="num">{bytes(x.size_bytes)}</span> },
          { title: 'Role', cell: (x) => (x.role === 'performance' || x.role === 'standard' ? <Tier name={x.role} /> : roleText[x.role] ?? x.role) },
          { title: 'Mounted on', cell: (x) => <span className="mono">{x.partitions.map((p) => p.mount_point).filter(Boolean).join(', ') || '–'}</span>, className: 'wrap' },
        ]}
        rows={d.state ? disks : undefined}
        rowKey={(x) => x.name}
      />
      <Note>
        A move of an app folder to an other tier is on the app page, tab Resources and storage. The {tierLabel('standard')} tier also keeps the backups
        of the databases.
      </Note>
    </>
  );
}
