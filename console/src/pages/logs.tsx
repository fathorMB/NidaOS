// The logs page (task T-07): one live stream for the system, the apps and
// the databases, with the filters, the search, the pause and the save.

import { useMemo, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { get, type Level, type LogLine, type LogPage } from '../api';
import { useData } from '../data';
import { errorText } from '../format';
import { useLive } from '../live';
import { appNamespace, dbNamespace } from '../metrics';
import { Btn, Head, LEVELS, LivePill, LogRows, matchLine, Note } from '../ui';

const SHOW = 300;

function saveFile(lines: LogLine[]) {
  const text = lines.map((l) => `${l.time}\t${l.level}\t${l.source.id}\t${l.message}`).join('\n') + '\n';
  const url = URL.createObjectURL(new Blob([text], { type: 'text/plain' }));
  const a = document.createElement('a');
  a.href = url;
  a.download = `nidaos-logs-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-')}.log`;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

export function LogsPage() {
  const live = useLive();
  const d = useData();
  const [params, setParams] = useSearchParams();
  const source = params.get('source') ?? '';
  const level = (params.get('level') ?? '') as Level | '';
  const q = params.get('q') ?? '';
  const [paused, setPaused] = useState(false);
  const frozen = useRef<LogLine[] | null>(null);
  const [old, setOld] = useState<LogPage | null>(null);
  const [oldBusy, setOldBusy] = useState(false);
  const [oldError, setOldError] = useState<string | null>(null);
  const [since, setSince] = useState('24h');

  const set = (k: string, v: string) => {
    const p = new URLSearchParams(params);
    if (v) p.set(k, v);
    else p.delete(k);
    setParams(p, { replace: true });
    setOld(null);
  };

  // The sources: the system services and the namespaces of the apps and
  // the databases. Other sources come from the lines.
  const sources = useMemo(() => {
    const m = new Map<string, string>();
    for (const a of d.apps ?? []) m.set(`pod/${appNamespace(a.name)}`, `app ${a.name}`);
    for (const x of d.databases ?? []) m.set(`pod/${dbNamespace(x.name)}`, `database ${x.name}`);
    m.set('service/', 'all system services');
    m.set('pod/nida-runners', 'GitHub runners');
    for (const l of live.lines.slice(-500)) {
      if (l.source.kind === 'service' && !m.has(l.source.id)) m.set(l.source.id, `service ${l.source.name}`);
      if (l.source.kind === 'pod' && l.source.namespace && !m.has(`pod/${l.source.namespace}`)) m.set(`pod/${l.source.namespace}`, `namespace ${l.source.namespace}`);
    }
    if (source && !m.has(source)) m.set(source, source);
    return [...m.entries()].sort((x, y) => x[1].localeCompare(y[1]));
  }, [d.apps, d.databases, live.lines, source]);

  const all = paused && frozen.current ? frozen.current : live.lines;
  const shown: LogLine[] = [];
  for (let i = all.length - 1; i >= 0 && shown.length < SHOW; i--) {
    if (matchLine(all[i], { source, level, q })) shown.push(all[i]);
  }

  const searchOld = async (cursor?: string) => {
    setOldBusy(true);
    setOldError(null);
    try {
      const p = new URLSearchParams({ limit: '200', since });
      if (source) p.set('source', source);
      if (level) p.set('level', level);
      if (q) p.set('q', q);
      if (cursor) p.set('cursor', cursor);
      const page = await get<LogPage>(`/api/v1/logs/search?${p}`);
      setOld((prev) => (cursor && prev ? { ...page, lines: [...prev.lines, ...page.lines] } : page));
    } catch (e) {
      setOldError(errorText(e));
    } finally {
      setOldBusy(false);
    }
  };

  return (
    <>
      <Head title="Logs" lead="One stream for the system, the apps and the databases. New lines come in at the top.">
        <LivePill status={live.logs} paused={paused} />
        <Btn
          onClick={() => {
            frozen.current = paused ? null : [...live.lines];
            setPaused(!paused);
          }}
        >
          {paused ? 'Continue' : 'Pause'}
        </Btn>
        <Btn onClick={() => saveFile([...shown].reverse())} disabled={shown.length === 0}>
          Save to a file
        </Btn>
      </Head>
      <div className="card filters" role="search">
        <label>
          Source
          <select value={source} onChange={(e) => set('source', e.target.value)} aria-label="Source">
            <option value="">All sources</option>
            {sources.map(([id, label]) => (
              <option key={id} value={id}>
                {label}
              </option>
            ))}
          </select>
        </label>
        <label>
          Minimum level
          <select value={level} onChange={(e) => set('level', e.target.value)} aria-label="Minimum level">
            <option value="">All levels</option>
            {LEVELS.map((l) => (
              <option key={l} value={l}>
                {l}
              </option>
            ))}
          </select>
        </label>
        <label>
          Text
          <input type="search" value={q} onChange={(e) => set('q', e.target.value)} placeholder="Search in the messages" aria-label="Text" />
        </label>
        {(source || level || q) && (
          <Btn small onClick={() => setParams(new URLSearchParams(), { replace: true })}>
            Clear the filters
          </Btn>
        )}
      </div>
      {live.dropped > 0 && <Note>The stream did not send {live.dropped} lines because the browser was too slow. Search the older lines to see them.</Note>}
      {paused && <Note>The view is paused. New lines come in when you continue.</Note>}
      <LogRows lines={shown} wrap big empty={live.lines.length ? 'No line in the memory of the console agrees with the filters.' : 'No lines yet.'} />
      <p className="muted" style={{ fontSize: 12 }}>
        The view shows the last {SHOW} lines that agree with the filters, from the {live.lines.length} lines that the console got since it opened.
      </p>
      <h3>Older lines</h3>
      <div className="card filters">
        <label>
          From
          <select value={since} onChange={(e) => setSince(e.target.value)} aria-label="From">
            <option value="1h">the last hour</option>
            <option value="24h">the last 24 hours</option>
            <option value="7d">the last 7 days</option>
            <option value="30d">the last 30 days</option>
          </select>
        </label>
        <Btn onClick={() => searchOld()} disabled={oldBusy}>
          {oldBusy ? 'Searching…' : 'Search the log store'}
        </Btn>
        <span className="muted" style={{ fontSize: 13 }}>
          The search uses the same filters. The newest line is first.
        </span>
      </div>
      {oldError && <Note kind="bad">{oldError}</Note>}
      {old && (
        <>
          <LogRows lines={old.lines} wrap big empty="The log store has no line that agrees with the filters." />
          <div className="inline" style={{ marginTop: 10 }}>
            {old.next_cursor && (
              <Btn onClick={() => searchOld(old.next_cursor)} disabled={oldBusy}>
                Get older lines
              </Btn>
            )}
            {old.partial && <span className="muted">The search stopped after 10 seconds. Get older lines to continue.</span>}
            <Btn onClick={() => saveFile([...old.lines].reverse())} disabled={old.lines.length === 0}>
              Save these lines
            </Btn>
          </div>
        </>
      )}
    </>
  );
}
