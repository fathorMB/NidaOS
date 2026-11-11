import { ago, bytes, duration, GiB } from './format';

test('bytes', () => {
  expect(bytes(512)).toBe('512 B');
  expect(bytes(412 * 1024 * 1024)).toBe('412 MB');
  expect(bytes(3.8 * GiB)).toBe('3.8 GB');
  expect(bytes(undefined)).toBe('–');
});

test('ago and duration', () => {
  const now = Date.parse('2026-11-11T10:00:00Z');
  expect(ago('2026-11-11T09:54:00Z', now)).toBe('6 min ago');
  expect(ago('2026-11-11T09:59:58Z', now)).toBe('now');
  expect(ago('2026-11-09T10:00:00Z', now)).toBe('2 days ago');
  expect(duration(72_000)).toBe('1 min 12 s');
  expect(duration(9_000)).toBe('9 s');
});
