import { LiveStore, LOG_LINES } from './live';
import { fakeFactory, logLine, snapshot } from './test/fakes';

test('the store keeps the snapshots and the lines', () => {
  vi.useFakeTimers();
  const f = fakeFactory();
  const s = new LiveStore(f.factory);
  s.start();
  const m = f.find('metrics'), l = f.find('logs');
  expect(m.url).toBe('/api/v1/metrics/stream');
  m.open();
  m.emit('metrics', snapshot(1));
  m.emit('metrics', snapshot(1)); // the same snapshot after a reconnection
  m.emit('metrics', snapshot(2));
  expect(s.history.map((x) => x.seq)).toEqual([1, 2]);
  expect(s.metrics.state).toBe('live');
  for (let i = 1; i <= LOG_LINES + 5; i++) l.emit('log', logLine(i, `line ${i}`));
  expect(s.lines.length).toBe(LOG_LINES);
  expect(s.lines[s.lines.length - 1].message).toBe(`line ${LOG_LINES + 5}`);
  l.emit('dropped', { count: 3 });
  expect(s.dropped).toBe(3);
  s.stop();
  expect(m.closed && l.closed).toBe(true);
  vi.useRealTimers();
});

test('a stream without data is stopped, with the time of the last data', () => {
  let t = 1_000_000;
  const f = fakeFactory();
  const s = new LiveStore(f.factory, () => t);
  s.start();
  const m = f.find('metrics');
  m.open();
  m.emit('metrics', snapshot(1));
  expect(s.metrics).toEqual({ state: 'live', lastData: 1_000_000 });
  t += 9_000;
  s.checkStale();
  expect(s.metrics.state).toBe('live');
  t += 2_000; // 11 s: more than 3 intervals and more than 10 s
  s.checkStale();
  expect(s.metrics).toEqual({ state: 'stopped', lastData: 1_000_000 });
  m.emit('metrics', snapshot(2));
  expect(s.metrics.state).toBe('live');
  f.find('logs').fail();
  expect(s.logs.state).toBe('stopped');
  s.stop();
});

test('a stream that never gives data is stopped after 15 seconds', () => {
  let t = 0;
  const f = fakeFactory();
  const s = new LiveStore(f.factory, () => t);
  s.start();
  f.find('metrics').open();
  t = 14_000;
  s.checkStale();
  expect(s.metrics.state).toBe('connecting');
  t = 16_000;
  s.checkStale();
  expect(s.metrics).toEqual({ state: 'stopped', lastData: null });
  s.stop();
});
