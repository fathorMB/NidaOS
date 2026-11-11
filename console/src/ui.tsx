// The shared parts of the pages (task T-02). They follow the mockup
// design/console.

import { createContext, Fragment, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import type { Level, LogLine } from './api';
import { clock, errorText, tierLabel } from './format';
import { useLive, useNow, type StreamStatus } from './live';

// ---- Small parts ----

export type LedKind = '' | 'warn' | 'off' | 'bad' | 'busy';

export function Led({ kind = '', children }: { kind?: LedKind; children?: ReactNode }) {
  return (
    <span className="inline" style={{ gap: 0, display: 'inline-flex' }}>
      <i className={`led ${kind}`} aria-hidden="true" />
      {children}
    </span>
  );
}

export function Tier({ name }: { name?: string }) {
  const c = name === 'performance' ? 'var(--perf)' : name === 'standard' ? 'var(--std)' : 'var(--sys)';
  return (
    <span>
      <i className="dot" style={{ ['--c' as string]: c }} aria-hidden="true" />
      {tierLabel(name)}
    </span>
  );
}

export function Mono({ children }: { children: ReactNode }) {
  return <span className="mono">{children}</span>;
}

export function Note({ kind = '', children }: { kind?: '' | 'ok' | 'bad'; children: ReactNode }) {
  return <div className={`note ${kind}`}>{children}</div>;
}

export function ErrorNote({ error }: { error: unknown }) {
  if (!error) return null;
  return (
    <div className="note bad" role="alert">
      {errorText(error)}
    </div>
  );
}

export function Btn(props: {
  children: ReactNode;
  onClick?: () => void;
  kind?: '' | 'primary' | 'danger' | 'primary danger';
  small?: boolean;
  disabled?: boolean;
  type?: 'button' | 'submit';
  title?: string;
}) {
  const cls = ['btn', props.kind ?? '', props.small ? 'small' : ''].filter(Boolean).join(' ');
  return (
    <button type={props.type ?? 'button'} className={cls} onClick={props.onClick} disabled={props.disabled} title={props.title}>
      {props.children}
    </button>
  );
}

export function LinkBtn({ to, children, kind = '', small }: { to: string; children: ReactNode; kind?: string; small?: boolean }) {
  return (
    <Link to={to} className={['btn', kind, small ? 'small' : ''].filter(Boolean).join(' ')}>
      {children}
    </Link>
  );
}

export function Head({ title, lead, children }: { title: ReactNode; lead?: ReactNode; children?: ReactNode }) {
  return (
    <div className="head">
      <div>
        <h1>{title}</h1>
        {lead && <p className="lead">{lead}</p>}
      </div>
      {children}
    </div>
  );
}

export function Section({ title, children, action }: { title: ReactNode; children?: ReactNode; action?: ReactNode }) {
  return (
    <>
      <h3>
        {title}
        {action}
      </h3>
      {children}
    </>
  );
}

// ---- Tables and lists ----

export interface Column<T> {
  title: string;
  cell: (row: T) => ReactNode;
  className?: string;
}

export function Table<T>(props: {
  cols: Column<T>[];
  rows: T[] | undefined;
  rowKey: (row: T) => string | number;
  onOpen?: (row: T) => void;
  empty?: ReactNode;
  label?: string;
}) {
  const { cols, rows, onOpen } = props;
  return (
    <div className="card scroll">
      <table aria-label={props.label}>
        <thead>
          <tr>
            {cols.map((c) => (
              <th key={c.title} className={c.className}>
                {c.title}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows === undefined && (
            <tr>
              <td className="empty" colSpan={cols.length}>
                Loading…
              </td>
            </tr>
          )}
          {rows !== undefined && rows.length === 0 && (
            <tr>
              <td className="empty" colSpan={cols.length}>
                {props.empty ?? 'Nothing to show.'}
              </td>
            </tr>
          )}
          {rows?.map((r) => (
            <tr
              key={props.rowKey(r)}
              className={onOpen ? 'open' : undefined}
              tabIndex={onOpen ? 0 : undefined}
              onClick={
                onOpen
                  ? (e) => {
                      if ((e.target as HTMLElement).closest('button, a, input, select')) return;
                      onOpen(r);
                    }
                  : undefined
              }
              onKeyDown={onOpen ? (e) => e.key === 'Enter' && onOpen(r) : undefined}
            >
              {cols.map((c) => (
                <td key={c.title} className={c.className}>
                  {c.cell(r)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export type KVRow = [ReactNode, ReactNode, ReactNode?];

export function KV({ rows }: { rows: KVRow[] }) {
  return (
    <dl className="card kv">
      {rows.map(([k, v, a], i) => (
        <Fragment key={i}>
          <dt>{k}</dt>
          <dd>{v}</dd>
          <div className="a">{a}</div>
        </Fragment>
      ))}
    </dl>
  );
}

export function Stats({ items }: { items: [ReactNode, ReactNode, ReactNode?][] }) {
  return (
    <dl className="card stats">
      {items.map(([k, v, s], i) => (
        <div key={i}>
          <dt>{k}</dt>
          <dd>
            {v}
            {s !== undefined && <small>{s}</small>}
          </dd>
        </div>
      ))}
    </dl>
  );
}

export function Todo({ items }: { items: { key: string; led?: LedKind; title: ReactNode; text?: ReactNode; action?: ReactNode }[] }) {
  return (
    <div className="card">
      <ul className="todo">
        {items.map((it) => (
          <li key={it.key}>
            {it.led !== undefined && <i className={`led ${it.led}`} aria-hidden="true" />}
            <div className="grow">
              <b>{it.title}</b>
              {it.text && <small>{it.text}</small>}
            </div>
            {it.action}
          </li>
        ))}
      </ul>
    </div>
  );
}

// useTab keeps the tab in the address (?tab=...), thus a reload and the
// back button keep the tab.
export function useTab(first: string): [string, (t: string) => string] {
  const [params] = useSearchParams();
  const tab = params.get('tab') ?? first;
  const href = (t: string) => (t === first ? '?' : `?tab=${t}`);
  return [tab, href];
}

export function Tabs({ list, current, href }: { list: [string, string][]; current: string; href: (t: string) => string }) {
  return (
    <div className="tabs" role="tablist">
      {list.map(([k, t]) => (
        <Link key={k} role="tab" to={href(k)} aria-selected={k === current} replace>
          {t}
        </Link>
      ))}
    </div>
  );
}

// ---- Form parts ----

export function Row({ label, children, help, error, htmlFor }: { label: ReactNode; children: ReactNode; help?: ReactNode; error?: ReactNode; htmlFor?: string }) {
  return (
    <div className="row">
      {htmlFor ? <label htmlFor={htmlFor}>{label}</label> : <div className="lbl">{label}</div>}
      <div>
        {children}
        {help && <div className="help">{help}</div>}
        {error && <div className="field-error">{error}</div>}
      </div>
    </div>
  );
}

export function Radio(props: { name: string; checked: boolean; onChange: () => void; title: ReactNode; text?: ReactNode; disabled?: boolean }) {
  return (
    <label className="opt">
      <input type="radio" name={props.name} checked={props.checked} onChange={props.onChange} disabled={props.disabled} /> {props.title}
      {props.text && <small>{props.text}</small>}
    </label>
  );
}

export function Switch({ on, onChange, label, disabled }: { on: boolean; onChange: (v: boolean) => void; label: ReactNode; disabled?: boolean }) {
  return (
    <label className="switch">
      <input type="checkbox" role="switch" checked={on} onChange={(e) => onChange(e.target.checked)} disabled={disabled} /> {label}
    </label>
  );
}

export function Bar({ pct }: { pct: number }) {
  const p = Math.max(0, Math.min(100, pct));
  const cls = p >= 90 ? 'bad' : p >= 80 ? 'warn' : '';
  return (
    <div className="bar" role="meter" aria-valuenow={Math.round(p)} aria-valuemin={0} aria-valuemax={100}>
      <i className={cls} style={{ width: `${p}%` }} />
    </div>
  );
}

// ---- The dialog of a confirmation ----

export function Confirm(props: {
  open: boolean;
  title: string;
  children: ReactNode;
  action: string;
  danger?: boolean;
  busy?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
  // confirmText: the person must write this text first.
  confirmText?: string;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const [typed, setTyped] = useState('');
  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (props.open && !d.open) {
      setTyped('');
      if (typeof d.showModal === 'function') d.showModal();
      else d.setAttribute('open', '');
    }
    if (!props.open && d.open) {
      if (typeof d.close === 'function') d.close();
      else d.removeAttribute('open');
    }
  }, [props.open]);
  const ok = !props.confirmText || typed === props.confirmText;
  return (
    <dialog ref={ref} className="dlg" onCancel={(e) => (e.preventDefault(), props.onCancel())} aria-label={props.title}>
      {props.open && (
        <form
          method="dialog"
          onSubmit={(e) => {
            e.preventDefault();
            if (ok && !props.busy) props.onConfirm();
          }}
        >
          <div className="body">
            <h2>{props.title}</h2>
            {props.children}
            {props.confirmText && (
              <p>
                <label>
                  Write <b className="mono">{props.confirmText}</b> to confirm:
                  <br />
                  <input type="text" value={typed} onChange={(e) => setTyped(e.target.value)} autoFocus aria-label="Confirmation text" />
                </label>
              </p>
            )}
          </div>
          <div className="acts">
            <Btn onClick={props.onCancel}>Cancel</Btn>
            <Btn type="submit" kind={props.danger ? 'primary danger' : 'primary'} disabled={!ok || props.busy}>
              {props.busy ? 'Wait…' : props.action}
            </Btn>
          </div>
        </form>
      )}
    </dialog>
  );
}

// ---- Messages ----

interface Toast {
  id: number;
  text: string;
  bad: boolean;
}

const ToastContext = createContext<(text: string, bad?: boolean) => void>(() => {});

export function Toasts({ children }: { children: ReactNode }) {
  const [list, setList] = useState<Toast[]>([]);
  const add = useCallback((text: string, bad = false) => {
    const id = Math.random();
    setList((l) => [...l.slice(-3), { id, text, bad }]);
    setTimeout(() => setList((l) => l.filter((t) => t.id !== id)), bad ? 9000 : 4500);
  }, []);
  return (
    <ToastContext.Provider value={add}>
      {children}
      <div className="toasts" role="status" aria-live="polite">
        {list.map((t) => (
          <div key={t.id} className={`toast ${t.bad ? 'bad' : ''}`}>
            {t.text}
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export const useToast = () => useContext(ToastContext);

// useAction runs one operation of the API. busy is true during the
// operation. An error goes to a message.
export function useAction() {
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const run = useCallback(
    async (op: () => Promise<unknown>, done?: string): Promise<boolean> => {
      setBusy(true);
      try {
        await op();
        if (done) toast(done);
        return true;
      } catch (e) {
        toast(errorText(e), true);
        return false;
      } finally {
        setBusy(false);
      }
    },
    [toast],
  );
  return { busy, run };
}

export function CopyBtn({ text, label = 'Copy' }: { text: string; label?: string }) {
  const toast = useToast();
  return (
    <Btn
      small
      onClick={() => {
        if (navigator.clipboard) {
          navigator.clipboard.writeText(text).then(
            () => toast('Copied.'),
            () => toast('The browser did not permit the copy.', true),
          );
        } else {
          toast('The browser did not permit the copy.', true);
        }
      }}
    >
      {label}
    </Btn>
  );
}

// ---- Live parts (decision D8) ----

// LivePill shows the state of a stream. When the stream stops, it shows
// the time of the last data (task T-09).
export function LivePill({ status, paused }: { status: StreamStatus; paused?: boolean }) {
  useNow();
  if (paused) {
    return (
      <span className="live paused">
        <i />
        Paused
      </span>
    );
  }
  if (status.state === 'live') {
    return (
      <span className="live" title={`Last data ${clock(status.lastData)}`}>
        <i />
        Live
      </span>
    );
  }
  if (status.state === 'connecting') {
    return (
      <span className="live connecting">
        <i />
        Connecting
      </span>
    );
  }
  return (
    <span className="live stopped" role="status">
      <i />
      Stopped · last data {status.lastData ? clock(status.lastData) : 'none'}
    </span>
  );
}

// StreamBanner is at the top of each page when a stream stops.
export function StreamBanner() {
  const live = useLive();
  const stopped = [live.metrics, live.logs].filter((s) => s.state === 'stopped');
  if (stopped.length === 0) return null;
  const last = Math.max(...stopped.map((s) => s.lastData ?? 0));
  return (
    <div className="banner" role="alert">
      <i className="led bad" aria-hidden="true" />
      <div className="grow">
        <b>The live data stopped.</b> The values on this page are from {last ? clock(last) : 'no time'}. The console connects again
        automatically.
      </div>
    </div>
  );
}

// Spark draws the last values as a small line.
export function Spark({ values, max }: { values: number[]; max?: number }) {
  if (values.length < 2) return <svg className="spark" viewBox="0 0 100 40" preserveAspectRatio="none" aria-hidden="true" />;
  const hi = Math.max(max ?? 0, ...values, 1e-9);
  const lo = Math.min(0, ...values);
  const n = values.length - 1;
  const line = values
    .map((v, i) => `${i ? 'L' : 'M'}${((i / n) * 100).toFixed(1)} ${(38 - ((v - lo) / (hi - lo)) * 34).toFixed(1)}`)
    .join(' ');
  return (
    <svg className="spark" viewBox="0 0 100 40" preserveAspectRatio="none" aria-hidden="true">
      <path className="a" d={`${line} L100 40 L0 40 Z`} />
      <path className="l" d={line} />
    </svg>
  );
}

export function Meter(props: { label: string; value: string; unit: string; values: number[]; sub?: ReactNode; max?: number; stale?: boolean }) {
  return (
    <div className={`card m ${props.stale ? 'stale' : ''}`} aria-label={props.label}>
      <div className="mt">{props.label}</div>
      <div className="mv">
        <span data-testid={`meter-${props.label}`}>{props.value}</span>
        <small>{props.unit}</small>
      </div>
      <Spark values={props.values} max={props.max} />
      <div className="muted" style={{ fontSize: 12 }}>
        {props.sub}
      </div>
    </div>
  );
}

// LogRows shows log lines, the newest first.
export function LogRows({ lines, wrap, big, empty = 'No lines in the last minutes.' }: { lines: LogLine[]; wrap?: boolean; big?: boolean; empty?: string }) {
  return (
    <div className={`card tail ${wrap ? 'wrap' : ''} ${big ? 'big' : ''}`} data-testid="log-rows">
      {lines.length === 0 && (
        <div>
          <time />
          <span className="s" />
          <span className="t muted">{empty}</span>
        </div>
      )}
      {lines.map((l) => (
        <div key={`${l.seq}-${l.time}`} className={l.level}>
          <time dateTime={l.time}>{clock(l.time)}</time>
          <span className="s" title={l.source.id}>
            {sourceName(l)}
          </span>
          <span className="t" title={l.message}>
            {l.message}
          </span>
        </div>
      ))}
    </div>
  );
}

// sourceName gives the short name of a source: the app or the database
// for their pods, else the name of the unit or the pod.
export function sourceName(l: LogLine): string {
  const ns = l.source.namespace ?? '';
  if (ns.startsWith('nida-app-')) return ns.slice(9);
  if (ns.startsWith('nida-db-')) return ns.slice(8);
  if (ns === 'nida-runners') return 'runner';
  return l.source.name;
}

// match tells if a line is in a filter. source is a prefix of the source
// id, as in the API (section 4.4).
export function matchLine(l: LogLine, f: { source?: string; level?: Level | ''; q?: string }): boolean {
  if (f.source && !l.source.id.startsWith(f.source)) return false;
  if (f.level && LEVELS.indexOf(l.level) < LEVELS.indexOf(f.level)) return false;
  if (f.q && !l.message.toLowerCase().includes(f.q.toLowerCase())) return false;
  return true;
}

export const LEVELS: Level[] = ['debug', 'info', 'warning', 'error'];

// LogTail shows the last lines of a source in a section, as in the mockup.
export function LogTail({ source, n = 6, title = 'Log' }: { source?: string; n?: number; title?: string }) {
  const live = useLive();
  const lines = [];
  for (let i = live.lines.length - 1; i >= 0 && lines.length < n; i--) {
    const l = live.lines[i];
    if (matchLine(l, { source })) lines.push(l);
  }
  const to = source ? `/logs?source=${encodeURIComponent(source)}` : '/logs';
  return (
    <>
      <h3>
        {title} <LivePill status={live.logs} />
        <LinkBtn to={to} small>
          Open all logs
        </LinkBtn>
      </h3>
      <LogRows lines={lines} />
    </>
  );
}
