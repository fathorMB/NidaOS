// The frame of the console (task T-01): the sign-in, the menu and the pages.

import { useCallback, useEffect, useState, type FormEvent, type ReactNode } from 'react';
import { BrowserRouter, NavLink, Navigate, Route, Routes } from 'react-router-dom';
import { ApiError, get, onSignedOut, post, type Me } from './api';
import { DataProvider, useData } from './data';
import { LiveProvider, type SourceFactory } from './live';
import { AppNew, AppPage, AppsPage } from './pages/apps';
import { DatabaseNew, DatabasePage, DatabasesPage } from './pages/databases';
import { LogsPage } from './pages/logs';
import { Overview } from './pages/overview';
import { RunnersPage } from './pages/runners';
import { StoragePage } from './pages/storage';
import { UpdatesPage } from './pages/updates';
import { Logo } from './logo';
import { Btn, ErrorNote, StreamBanner, Toasts } from './ui';

export { Logo };

// ---- Theme ----

type Theme = 'system' | 'light' | 'dark';

function readTheme(): Theme {
  try {
    const t = localStorage.getItem('nida-theme');
    if (t === 'light' || t === 'dark') return t;
  } catch {
    // The browser can refuse the storage.
  }
  return 'system';
}

function applyTheme(t: Theme) {
  const root = document.documentElement;
  if (t === 'system') root.removeAttribute('data-theme');
  else root.setAttribute('data-theme', t);
  try {
    if (t === 'system') localStorage.removeItem('nida-theme');
    else localStorage.setItem('nida-theme', t);
  } catch {
    // The theme stays only for this page.
  }
}

// ---- Sign-in ----

export function SignIn({ onDone }: { onDone: () => void }) {
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [ownerSet, setOwnerSet] = useState<boolean | null>(null);
  useEffect(() => {
    get<{ owner_set: boolean }>('/api/v1/auth/status').then(
      (s) => setOwnerSet(s.owner_set),
      () => setOwnerSet(true),
    );
  }, []);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await post('/api/v1/auth/login', { password });
      setPassword('');
      onDone();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };
  return (
    <main className="signin">
      <form className="card" onSubmit={submit} aria-label="Sign in">
        <div className="brand">
          <Logo />
          NidaOS
        </div>
        <div className="where">{location.host}</div>
        {ownerSet === false ? (
          <div className="note">
            The owner password is not set. Set it on the computer with <span className="mono">sudo nida-core set-password</span>, then
            reload this page.
          </div>
        ) : (
          <>
            <label htmlFor="password">Owner password</label>
            <input id="password" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} autoFocus required />
            {error && (
              <div className="note bad" role="alert">
                {error}
              </div>
            )}
            <Btn type="submit" kind="primary" disabled={busy || password === ''}>
              {busy ? 'Wait…' : 'Sign in'}
            </Btn>
          </>
        )}
      </form>
    </main>
  );
}

// ---- Menu ----

function Nav({ onSignOut }: { onSignOut: () => void }) {
  const d = useData();
  const [theme, setTheme] = useState<Theme>(readTheme);
  const active = d.runners?.active?.length ?? 0;
  const failedApps = d.apps?.filter((a) => a.state === 'failed').length ?? 0;
  const update = d.updates?.staged || d.updates?.available ? 1 : 0;
  const item = (to: string, label: string, count?: ReactNode, warn = false, end = false) => (
    <NavLink to={to} end={end}>
      {label}
      {count !== undefined && count !== 0 && <span className={warn ? 'warn' : ''}>{count}</span>}
    </NavLink>
  );
  const who = d.me.kind === 'owner' ? 'the owner' : d.me.kind === 'token' ? `token ${d.me.name}` : 'the local socket';
  return (
    <nav className="nav" aria-label="Sections">
      <div className="brand">
        <Logo />
        NidaOS
      </div>
      <div className="where">{d.state?.system.hostname ? `${d.state.system.hostname} · ${location.hostname}` : location.hostname}</div>
      {item('/', 'Overview', undefined, false, true)}
      <h2>Hosting</h2>
      {item('/apps', 'Apps', failedApps ? `${failedApps}!` : d.apps?.length, failedApps > 0)}
      {item('/databases', 'Databases', d.databases?.length)}
      <h2>Computer</h2>
      {item('/storage', 'Storage')}
      {item('/logs', 'Logs')}
      {!d.updatesOff && item('/updates', 'Updates', update, true)}
      {!d.runnersOff && (
        <>
          <h2>Access</h2>
          {item('/runners', 'Runners', active)}
        </>
      )}
      <div className="foot">
        <div>
          Signed in as {who}
          {d.me.level !== 'admin' && ' (read only)'}
        </div>
        <div>NidaOS {d.health?.version ?? ''}</div>
        <div className="row">
          <label>
            <span className="sr">Theme</span>
            <select
              value={theme}
              aria-label="Theme"
              onChange={(e) => {
                const t = e.target.value as Theme;
                setTheme(t);
                applyTheme(t);
              }}
            >
              <option value="system">System theme</option>
              <option value="light">Light</option>
              <option value="dark">Dark</option>
            </select>
          </label>
          {d.me.kind === 'owner' && (
            <Btn small onClick={onSignOut}>
              Sign out
            </Btn>
          )}
        </div>
      </div>
    </nav>
  );
}

function NotFound() {
  return (
    <div>
      <h1>Not found</h1>
      <p className="lead">This page does not exist.</p>
    </div>
  );
}

export function Shell({ onSignOut }: { onSignOut: () => void }) {
  return (
    <div className="app">
      <Nav onSignOut={onSignOut} />
      <main className="page">
        <StreamBanner />
        <Routes>
          <Route path="/" element={<Overview />} />
          <Route path="/apps" element={<AppsPage />} />
          <Route path="/apps/new" element={<AppNew />} />
          <Route path="/apps/:name" element={<AppPage />} />
          <Route path="/databases" element={<DatabasesPage />} />
          <Route path="/databases/new" element={<DatabaseNew />} />
          <Route path="/databases/:name" element={<DatabasePage />} />
          <Route path="/storage" element={<StoragePage />} />
          <Route path="/logs" element={<LogsPage />} />
          <Route path="/updates" element={<UpdatesPage />} />
          <Route path="/runners" element={<RunnersPage />} />
          <Route path="/signin" element={<Navigate to="/" replace />} />
          <Route path="*" element={<NotFound />} />
        </Routes>
      </main>
    </div>
  );
}

// ---- The root ----

export function Root({ factory }: { factory?: SourceFactory }) {
  const [me, setMe] = useState<Me | null | undefined>(undefined);
  const [error, setError] = useState<unknown>(null);
  const check = useCallback(() => {
    get<Me>('/api/v1/auth/me').then(
      (m) => {
        setMe(m);
        setError(null);
      },
      (e) => {
        if (e instanceof ApiError && e.status === 401) setMe(null);
        else setError(e);
      },
    );
  }, []);
  useEffect(() => {
    applyTheme(readTheme());
    onSignedOut(() => setMe(null));
    check();
  }, [check]);
  const signOut = async () => {
    try {
      await post('/api/v1/auth/logout');
    } finally {
      setMe(null);
    }
  };
  if (error && me === undefined) {
    return (
      <main className="signin">
        <div className="card" style={{ padding: 24, maxWidth: 460 }}>
          <h1>NidaOS</h1>
          <ErrorNote error={error} />
          <p>
            <Btn onClick={check}>Try again</Btn>
          </p>
        </div>
      </main>
    );
  }
  if (me === undefined) return null;
  if (me === null) return <SignIn onDone={check} />;
  return (
    <Toasts>
      <DataProvider me={me}>
        <LiveProvider factory={factory}>
          <Shell onSignOut={signOut} />
        </LiveProvider>
      </DataProvider>
    </Toasts>
  );
}

export default function App() {
  return (
    <BrowserRouter>
      <Root />
    </BrowserRouter>
  );
}
