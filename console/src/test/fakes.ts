import type { EventSourceLike, SourceFactory } from '../live';

// FakeSource is an EventSource that the test controls.
export class FakeSource implements EventSourceLike {
  readyState = 0;
  onopen: ((ev: Event) => void) | null = null;
  onerror: ((ev: Event) => void) | null = null;
  closed = false;
  private handlers = new Map<string, ((ev: MessageEvent) => void)[]>();
  constructor(readonly url: string) {}
  addEventListener(type: string, fn: (ev: MessageEvent) => void) {
    this.handlers.set(type, [...(this.handlers.get(type) ?? []), fn]);
  }
  close() {
    this.closed = true;
  }
  open() {
    this.readyState = 1;
    this.onopen?.(new Event('open'));
  }
  fail() {
    this.readyState = 0;
    this.onerror?.(new Event('error'));
  }
  emit(type: string, data: unknown) {
    const ev = new MessageEvent(type, { data: JSON.stringify(data) });
    this.handlers.get(type)?.forEach((fn) => fn(ev));
  }
}

export function fakeFactory() {
  const sources: FakeSource[] = [];
  const factory: SourceFactory = (url) => {
    const s = new FakeSource(url);
    sources.push(s);
    return s;
  };
  const find = (part: string) => sources.find((s) => s.url.includes(part))!;
  return { factory, sources, find };
}

export function snapshot(seq: number, cpu = 10) {
  return {
    seq,
    time: new Date(Date.UTC(2026, 10, 11, 10, 0, seq)).toISOString(),
    interval_seconds: 2,
    host: {
      cpu: { usage_percent: cpu, threads: [cpu] },
      memory: { total_bytes: 8 * 1024 ** 3, used_bytes: 2 * 1024 ** 3, available_bytes: 6 * 1024 ** 3, used_percent: 25 },
      load: [0, 0, 0],
      disks: [],
      network: [{ name: 'eth0', receive_bytes_per_second: 125000, transmit_bytes_per_second: 0 }],
    },
    workloads: [{ kind: 'pod', namespace: 'nida-app-blog', name: 'web', id: 'u', cpu_cores: 0.25, memory_bytes: 100 * 1024 ** 2 }],
  };
}

export function logLine(seq: number, message: string, level = 'info', ns = 'nida-app-blog') {
  return {
    seq,
    time: new Date(Date.UTC(2026, 10, 11, 10, 0, seq)).toISOString(),
    source: { kind: 'pod', id: `pod/${ns}/web-1/web`, name: 'web-1', namespace: ns, container: 'web' },
    level,
    message,
  };
}
