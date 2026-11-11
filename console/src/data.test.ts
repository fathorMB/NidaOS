import type { App, Database, Tier } from './api';
import { attention } from './data';

const app = (over: Partial<App>): App => ({
  name: 'blog', config: {}, copies: 1, active: 1, created: '', updated: '', state: 'running', versions: [], ...over,
});

test('the items that need attention', () => {
  const items = attention({
    apps: [
      app({ name: 'ok' }),
      app({ name: 'down', state: 'failed', active: 0, versions: [{ number: 1, image: 'x', config: {}, state: 'failed', reason: 'exit code 1', created: '' }] }),
      app({ name: 'lastfail', active: 1, versions: [{ number: 1, image: 'x', config: {}, state: 'serving', created: '' }, { number: 2, image: 'y', config: {}, state: 'failed', reason: 'no answer', created: '' }] }),
    ],
    databases: [{ name: 'db', image: 'docker.io/library/postgres:18.1-alpine', update_available: 'docker.io/library/postgres:18.2-alpine', state: 'ready' } as Database],
    state: { tiers: [{ name: 'standard', state: 'ready', path: '/s', usage: { total_bytes: 100, used_bytes: 90, free_bytes: 10 } } as Tier] } as never,
    updates: { staged: { image: 'i', version: '45.2' } },
  });
  const keys = items.map((i) => i.key);
  expect(keys).toEqual(['tier-standard', 'update-staged', 'app-down', 'app-lastfail', 'dbup-db']);
  expect(items.find((i) => i.key === 'app-down')!.level).toBe('bad');
  expect(items.find((i) => i.key === 'dbup-db')!.text).toContain('18.1 → 18.2');
});

test('nothing needs attention', () => {
  expect(attention({ apps: [app({})], databases: [], updates: { booted: { image: 'i' } } })).toEqual([]);
});
