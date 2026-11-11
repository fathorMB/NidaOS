// The live data (decision D8). One stream of metrics and one stream of log
// lines for all the pages. Each page reads the same store, thus a change
// of page does not open a new connection.

import { createContext, useContext, useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from 'react';
import type { LogLine, Snapshot } from './api';

export type StreamState = 'connecting' | 'live' | 'stopped';

export interface StreamStatus {
  state: StreamState;
  // lastData is the time of the last data from the stream.
  lastData: number | null;
}

// HISTORY is the number of snapshots for the small graphs (2 minutes).
export const HISTORY = 60;
// LOG_LINES is the number of lines that the console keeps in memory.
export const LOG_LINES = 2000;

// The interface of EventSource that the store uses. The tests give a fake.
export interface EventSourceLike {
  readyState: number;
  onopen: ((ev: Event) => void) | null;
  onerror: ((ev: Event) => void) | null;
  addEventListener(type: string, fn: (ev: MessageEvent) => void): void;
  close(): void;
}
export type SourceFactory = (url: string) => EventSourceLike;

type Listener = () => void;

export class LiveStore {
  history: Snapshot[] = [];
  lines: LogLine[] = [];
  dropped = 0;
  metrics: StreamStatus = { state: 'connecting', lastData: null };
  logs: StreamStatus = { state: 'connecting', lastData: null };

  private listeners = new Set<Listener>();
  private sources: EventSourceLike[] = [];
  private timer: ReturnType<typeof setInterval> | null = null;
  private pending = false;
  private startedAt = 0;
  // version changes at each change, for useSyncExternalStore.
  version = 0;

  constructor(
    private factory: SourceFactory,
    private now: () => number = Date.now,
  ) {}

  start() {
    this.startedAt = this.now();
    const m = this.factory('/api/v1/metrics/stream');
    m.onopen = () => this.setState('metrics', this.metrics.lastData ? 'live' : 'connecting');
    m.onerror = () => this.setState('metrics', 'stopped');
    m.addEventListener('metrics', (ev) => {
      const s = JSON.parse(ev.data) as Snapshot;
      const last = this.history[this.history.length - 1];
      // After a reconnection, the stream sends the last snapshot again.
      if (last && s.seq === last.seq && s.time === last.time) return;
      this.history = [...this.history.slice(-(HISTORY - 1)), s];
      this.metrics = { state: 'live', lastData: this.now() };
      this.changed();
    });

    const l = this.factory('/api/v1/logs/stream?tail=200');
    l.onopen = () => this.setState('logs', 'live');
    l.onerror = () => this.setState('logs', 'stopped');
    l.addEventListener('log', (ev) => {
      const line = JSON.parse(ev.data) as LogLine;
      this.lines.push(line);
      if (this.lines.length > LOG_LINES) this.lines = this.lines.slice(-LOG_LINES);
      this.logs = { state: 'live', lastData: this.now() };
      this.changed();
    });
    l.addEventListener('dropped', (ev) => {
      this.dropped += (JSON.parse(ev.data) as { count: number }).count;
      this.changed();
    });
    this.sources = [m, l];
    // A snapshot comes each 2 seconds. Without a snapshot for 3 intervals
    // (minimum 10 seconds), the stream of metrics is stopped.
    this.timer = setInterval(() => this.checkStale(), 1000);
  }

  stop() {
    this.sources.forEach((s) => s.close());
    this.sources = [];
    if (this.timer) clearInterval(this.timer);
    this.timer = null;
  }

  checkStale() {
    // The metrics come each 2 seconds: no snapshot in 15 seconds after the
    // start is a stopped stream. A log line can come later, thus the logs
    // have no such limit.
    if (this.metrics.state === 'connecting' && !this.metrics.lastData && this.now() - this.startedAt > 15_000) {
      this.setState('metrics', 'stopped');
    }
    const last = this.history[this.history.length - 1];
    if (this.metrics.state !== 'live' || !this.metrics.lastData || !last) return;
    const limit = Math.max(10_000, (last.interval_seconds || 2) * 3000);
    if (this.now() - this.metrics.lastData > limit) this.setState('metrics', 'stopped');
  }

  private setState(which: 'metrics' | 'logs', state: StreamState) {
    if (this[which].state === state) return;
    this[which] = { ...this[which], state };
    this.changed(true);
  }

  // changed tells the pages. Many log lines can come at the same time,
  // thus the pages get one notice each 250 ms at most.
  private changed(now = false) {
    this.version++;
    if (now) {
      this.listeners.forEach((fn) => fn());
      return;
    }
    if (this.pending) return;
    this.pending = true;
    setTimeout(() => {
      this.pending = false;
      this.listeners.forEach((fn) => fn());
    }, 250);
  }

  subscribe = (fn: Listener) => {
    this.listeners.add(fn);
    return () => {
      this.listeners.delete(fn);
    };
  };
}

const LiveContext = createContext<LiveStore | null>(null);

export const browserSource: SourceFactory = (url) => new EventSource(url, { withCredentials: true });

export function LiveProvider({ children, factory = browserSource }: { children: ReactNode; factory?: SourceFactory }) {
  const [store] = useState(() => new LiveStore(factory));
  useEffect(() => {
    store.start();
    return () => store.stop();
  }, [store]);
  return <LiveContext.Provider value={store}>{children}</LiveContext.Provider>;
}

export function useLiveStore(): LiveStore {
  const s = useContext(LiveContext);
  if (!s) throw new Error('useLive needs a LiveProvider');
  return s;
}

// useLive gives the store and draws the page again at each change.
export function useLive(): LiveStore {
  const store = useLiveStore();
  useSyncExternalStore(store.subscribe, () => store.version);
  return store;
}

// useNow gives the time, again each second. It is for "5 s ago".
export function useNow(every = 1000): number {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), every);
    return () => clearInterval(t);
  }, [every]);
  return now;
}

// usePoll reads a value from the API at once, then again each interval.
// The value stays when a read fails; error tells the cause.
export function usePoll<T>(load: () => Promise<T>, every: number, deps: unknown[] = []) {
  const [data, setData] = useState<T | undefined>(undefined);
  const [error, setError] = useState<Error | null>(null);
  const [tick, setTick] = useState(0);
  const loadRef = useRef(load);
  loadRef.current = load;
  useEffect(() => {
    let stop = false;
    const run = () =>
      loadRef.current().then(
        (v) => {
          if (!stop) {
            setData(v);
            setError(null);
          }
        },
        (e: Error) => !stop && setError(e),
      );
    run();
    const t = every > 0 ? setInterval(run, every) : null;
    return () => {
      stop = true;
      if (t) clearInterval(t);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [every, tick, ...deps]);
  return { data, error, reload: () => setTick((n) => n + 1) };
}
