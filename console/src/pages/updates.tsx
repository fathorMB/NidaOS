// The updates page (task T-06): the system versions of bootc.

import { useState } from 'react';
import { post, type Deployment } from '../api';
import { useData } from '../data';
import { ago, dateTime } from '../format';
import { useNow } from '../live';
import { Btn, Confirm, ErrorNote, Head, Led, Note, Table, useAction, type Column, type LedKind } from '../ui';

interface VersionRow {
  key: string;
  dep: Deployment;
  state: string;
  led: LedKind;
  when: string;
}

const busyText = { checking: 'Asking for a new version…', downloading: 'Downloading the new version…', rolling_back: 'Preparing the rollback…', restarting: 'The computer restarts…' };

function versionName(dep?: Deployment) {
  if (!dep) return '–';
  return dep.version ? `NidaOS ${dep.version}` : dep.image;
}

export function UpdatesPage() {
  const d = useData();
  const now = useNow(10_000);
  const { busy, run } = useAction();
  const [ask, setAsk] = useState<'restart' | 'rollback' | null>(null);
  const u = d.updates;
  if (d.updatesOff) {
    return (
      <>
        <Head title="Updates" />
        <Note>This computer has no bootc: NidaOS cannot show the system versions.</Note>
      </>
    );
  }
  const rows: VersionRow[] = [];
  if (u?.available) rows.push({ key: 'available', dep: u.available, state: 'Available on the registry', led: 'warn', when: u.last_check ? `found ${ago(u.last_check, now)}` : '' });
  if (u?.staged) rows.push({ key: 'staged', dep: u.staged, state: 'Ready: it starts at the next restart', led: 'warn', when: u.staged.timestamp ? `built ${dateTime(u.staged.timestamp)}` : '' });
  if (u?.booted) rows.push({ key: 'booted', dep: u.booted, state: 'In operation', led: '', when: u.booted.timestamp ? `built ${dateTime(u.booted.timestamp)}` : '' });
  if (u?.rollback)
    rows.push({
      key: 'rollback',
      dep: u.rollback,
      state: u.rollback_queued ? 'It starts at the next restart (rollback)' : 'Kept for a rollback',
      led: u.rollback_queued ? 'warn' : 'off',
      when: u.rollback.timestamp ? `built ${dateTime(u.rollback.timestamp)}` : '',
    });
  const cols: Column<VersionRow>[] = [
    { title: 'Version', cell: (r) => <b>{versionName(r.dep)}</b> },
    { title: 'State', cell: (r) => <Led kind={r.led}>{r.state}</Led> },
    { title: 'Image', cell: (r) => <span className="mono">{r.dep.image}</span>, className: 'wrap' },
    { title: 'Date', cell: (r) => r.when },
    {
      title: '',
      cell: (r) =>
        d.admin && r.key === 'rollback' && !u?.rollback_queued ? (
          <Btn small disabled={busy || !!u?.busy} onClick={() => setAsk('rollback')}>
            Go back to this version
          </Btn>
        ) : null,
    },
  ];
  const waiting = !!u?.staged || !!u?.rollback_queued;
  return (
    <>
      <Head title="Updates" lead="An update replaces the full system in one step. The version before stays on the disk, thus you can go back.">
        {d.admin && (
          <>
            <Btn disabled={busy || !!u?.busy} onClick={() => run(() => post('/api/v1/updates/check'), 'NidaOS asks the registry for a new version.').then(d.reload)}>
              Look for an update
            </Btn>
            {u?.available && !u.staged && (
              <Btn disabled={busy || !!u?.busy} onClick={() => run(() => post('/api/v1/updates/download'), 'The download starts.').then(d.reload)}>
                Download {versionName(u.available)}
              </Btn>
            )}
            {waiting && (
              <Btn kind="primary" disabled={busy || !!u?.busy} onClick={() => setAsk('restart')}>
                {u?.staged ? `Install ${versionName(u.staged)} and restart` : 'Restart into the version before'}
              </Btn>
            )}
          </>
        )}
      </Head>
      {u?.busy && (
        <Note>
          <Led kind="busy">{busyText[u.busy]}</Led>
        </Note>
      )}
      {u?.error && <Note kind="bad">The last operation failed: {u.error}</Note>}
      <ErrorNote error={d.updatesError} />
      <Table label="System versions" cols={cols} rows={u ? rows : undefined} rowKey={(r) => r.key} />
      <Note kind="ok">
        {u?.last_check ? `The last look for an update was ${ago(u.last_check, now)}.` : 'NidaOS did not look for an update since its start.'} NidaOS does
        not install an update without your approval.
      </Note>
      <Confirm
        open={ask === 'restart'}
        title="Restart the computer"
        action="Restart"
        danger
        busy={busy}
        onCancel={() => setAsk(null)}
        onConfirm={() => run(() => post('/api/v1/updates/restart'), 'The computer restarts in some seconds.').then(() => (setAsk(null), d.reload()))}
      >
        <p>
          The computer restarts with {u?.staged ? versionName(u.staged) : 'the version before'}. The apps and the databases stop for some minutes. The
          console connects again when the computer is ready.
        </p>
      </Confirm>
      <Confirm
        open={ask === 'rollback'}
        title={`Go back to ${versionName(u?.rollback)}`}
        action="Go back and restart"
        danger
        busy={busy}
        onCancel={() => setAsk(null)}
        onConfirm={() => run(() => post('/api/v1/updates/rollback', { restart: true }), 'The computer restarts with the version before.').then(() => (setAsk(null), d.reload()))}
      >
        <p>The computer restarts with the version before. The data of the apps and the databases stay.</p>
      </Confirm>
    </>
  );
}
