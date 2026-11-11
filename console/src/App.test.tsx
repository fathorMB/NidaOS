import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { Root } from './App';
import { fakeFactory, logLine, snapshot } from './test/fakes';

type Handler = (method: string, body: unknown) => [number, unknown];

// mockApi replaces fetch with the answers of a fake NidaOS API.
function mockApi(routes: Record<string, Handler | unknown>) {
  const calls: string[] = [];
  vi.stubGlobal('fetch', async (path: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET';
    calls.push(`${method} ${path}`);
    const r = routes[path.split('?')[0]];
    let status = 404;
    let body: unknown = { code: 'not_found', message: 'The path does not exist.' };
    if (typeof r === 'function') [status, body] = (r as Handler)(method, init?.body ? JSON.parse(init.body as string) : undefined);
    else if (r !== undefined) [status, body] = [200, r];
    return new Response(status === 204 ? null : JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
  });
  return calls;
}

const base = {
  '/api/v1/health': { status: 'ok', version: '45.1', uptime: '1h' },
  '/api/v1/auth/status': { owner_set: true },
  '/api/v1/state': {
    time: '',
    system: { hostname: 'nida', os: {}, cpu: { threads: 6 }, memory: { total_bytes: 1, available_bytes: 1 } },
    disks: [],
    tiers: [],
  },
  '/api/v1/apps': {
    apps: [
      { name: 'blog', config: { port: 8080 }, copies: 1, active: 1, created: '2026-11-01T00:00:00Z', updated: '2026-11-01T00:00:00Z', state: 'running', address: 'https://blog.nidaos.local', versions: [{ number: 1, image: 'x', config: {}, state: 'serving', created: '2026-11-01T00:00:00Z', commit: 'abcdef1234' }] },
    ],
  },
  '/api/v1/databases': { databases: [], versions: [17, 18] },
  '/api/v1/runners': { github: { configured: false }, connections: [], active: [] },
  '/api/v1/updates': { booted: { image: 'ghcr.io/x/nidaos:proof', version: '45.1' } },
};

afterEach(() => vi.unstubAllGlobals());

test('the sign-in opens the console, and the live data come in', async () => {
  let signedIn = false;
  const calls = mockApi({
    ...base,
    '/api/v1/auth/me': () => (signedIn ? [200, { kind: 'owner', level: 'admin' }] : [401, { code: 'sign_in_required', message: 'Sign in.' }]),
    '/api/v1/auth/login': (_m: string, b: { password: string }) => {
      if (b.password !== 'a long password') return [401, { code: 'wrong_password', message: 'The password is not correct.' }];
      signedIn = true;
      return [200, { expires: '' }];
    },
  });
  const f = fakeFactory();
  render(
    <MemoryRouter initialEntries={['/']}>
      <Root factory={f.factory} />
    </MemoryRouter>,
  );
  const user = userEvent.setup();
  const pw = await screen.findByLabelText('Owner password');
  await user.type(pw, 'wrong');
  await user.click(screen.getByRole('button', { name: 'Sign in' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('The password is not correct.');

  await user.clear(pw);
  await user.type(pw, 'a long password');
  await user.click(screen.getByRole('button', { name: 'Sign in' }));
  expect(await screen.findByRole('heading', { name: 'Overview' })).toBeInTheDocument();
  expect(calls).toContain('POST /api/v1/auth/login');

  // The menu has the sections and the count of the apps.
  const nav = screen.getByRole('navigation', { name: 'Sections' });
  expect(within(nav).getByRole('link', { name: /Apps/ })).toHaveTextContent('1');

  // The live metrics and the log come from the streams.
  await waitFor(() => expect(f.sources.length).toBe(2));
  act(() => {
    f.find('metrics').open();
    f.find('metrics').emit('metrics', snapshot(1, 42));
    f.find('logs').open();
    f.find('logs').emit('log', logLine(1, 'GET / 200'));
  });
  expect(await screen.findByTestId('meter-Processor')).toHaveTextContent('42');
  expect(await screen.findByText('GET / 200')).toBeInTheDocument();
  // The source of a pod line is the name of the app.
  expect(screen.getAllByText('blog').length).toBeGreaterThan(0);

  // When the stream stops, the page tells it.
  act(() => f.find('metrics').fail());
  expect(await screen.findByText(/The live data stopped/)).toBeInTheDocument();
  expect(screen.getAllByText(/Stopped · last data/).length).toBeGreaterThan(0);
});

test('a page without a password tells how to set it', async () => {
  mockApi({ ...base, '/api/v1/auth/status': { owner_set: false }, '/api/v1/auth/me': () => [401, { code: 'sign_in_required', message: '' }] });
  render(
    <MemoryRouter>
      <Root factory={fakeFactory().factory} />
    </MemoryRouter>,
  );
  expect(await screen.findByText(/nida-core set-password/)).toBeInTheDocument();
});

test('the app page shows the app and its tabs', async () => {
  mockApi({
    ...base,
    '/api/v1/auth/me': { kind: 'owner', level: 'admin' },
    '/api/v1/apps/blog': (base['/api/v1/apps'] as { apps: unknown[] }).apps[0],
    '/api/v1/apps/blog/repository': () => [404, { code: 'repository_not_connected', message: '' }],
    '/api/v1/apps/blog/jobs': { jobs: [] },
  });
  render(
    <MemoryRouter initialEntries={['/apps/blog?tab=deploys']}>
      <Root factory={fakeFactory().factory} />
    </MemoryRouter>,
  );
  expect(await screen.findByRole('heading', { name: 'blog' })).toBeInTheDocument();
  expect(screen.getByRole('tab', { name: 'Deploys' })).toHaveAttribute('aria-selected', 'true');
  expect(await screen.findByText('abcdef1 · v1')).toBeInTheDocument();
  expect(screen.getByText(/NidaOS has no GitHub App yet/)).toBeInTheDocument();
});
