// The runners page (task T-08): the GitHub App, the connected repositories
// and the jobs. Each job gets a new runner pod (decision D26).

import { useState } from 'react';
import { Link } from 'react-router-dom';
import { del, post, type RepoConnection } from '../api';
import { useData } from '../data';
import { dateTime } from '../format';
import { JobTable } from './apps';
import { Btn, Confirm, Head, KV, Led, LivePill, LogTail, Mono, Note, Row, Table, Todo, useAction, type Column } from '../ui';
import { useLive } from '../live';

export function RunnersPage() {
  const d = useData();
  const live = useLive();
  const { busy, run } = useAction();
  const [org, setOrg] = useState('');
  const [setupUrl, setSetupUrl] = useState<string | null>(null);
  const [importOpen, setImportOpen] = useState(false);
  const [appId, setAppId] = useState('');
  const [key, setKey] = useState('');
  const [remove, setRemove] = useState(false);
  const gh = d.runners?.github;
  const conns = d.runners?.connections ?? [];
  if (d.runnersOff) {
    return (
      <>
        <Head title="GitHub runners" />
        <Note>This NidaOS has no GitHub runners.</Note>
      </>
    );
  }
  const cols: Column<RepoConnection>[] = [
    {
      title: 'App',
      cell: (c) => (
        <Led kind={c.problem ? 'warn' : ''}>
          <Link to={`/apps/${c.app}?tab=deploys`}>
            <b>{c.app}</b>
          </Link>
        </Led>
      ),
    },
    { title: 'Repository', cell: (c) => <Mono>{`${c.owner}/${c.repo}`}</Mono> },
    { title: 'Branch', cell: (c) => <Mono>{c.branch}</Mono> },
    { title: 'Connected', cell: (c) => dateTime(c.created) },
    { title: 'Problem', cell: (c) => c.problem || <span className="muted">none</span>, className: 'wrap' },
    {
      title: '',
      cell: (c) =>
        c.pull_request ? (
          <a className="btn small" href={c.pull_request} target="_blank" rel="noreferrer">
            Pull request
          </a>
        ) : null,
    },
  ];
  return (
    <>
      <Head title="GitHub runners" lead="A push to a connected repository starts a job. NidaOS starts a new runner pod for each job: the pod builds the app, deploys it and stops. The source stays on GitHub." />
      <h3>The GitHub App of NidaOS</h3>
      {gh === undefined ? (
        <p className="muted">Loading…</p>
      ) : gh.configured ? (
        <>
          <KV
            rows={[
              ['Name', <b>{gh.name}</b>, gh.html_url ? <a className="btn small" href={gh.html_url} target="_blank" rel="noreferrer">Open on GitHub</a> : null],
              ['Owner', <Mono>{gh.owner}</Mono>],
              ['App ID', <Mono>{gh.id}</Mono>],
              ['Repositories', 'Install the app on each private repository of your apps.', gh.install_url ? <a className="btn small" href={gh.install_url} target="_blank" rel="noreferrer">Select the repositories</a> : null],
            ]}
          />
          {d.admin && (
            <div className="inline" style={{ marginTop: 10 }}>
              <Btn kind="danger" small disabled={conns.length > 0} title={conns.length ? 'Disconnect all the apps first' : undefined} onClick={() => setRemove(true)}>
                Remove the GitHub App from NidaOS
              </Btn>
              {conns.length > 0 && <span className="muted" style={{ fontSize: 13 }}>Disconnect all the apps first.</span>}
            </div>
          )}
          <Confirm
            open={remove}
            title="Remove the GitHub App"
            action="Remove"
            danger
            busy={busy}
            onCancel={() => setRemove(false)}
            onConfirm={() => run(() => del('/api/v1/github'), 'NidaOS removed the GitHub App.').then(() => (setRemove(false), d.reload()))}
          >
            <p>NidaOS deletes its private key. The app stays on GitHub: delete it there too.</p>
          </Confirm>
        </>
      ) : d.admin ? (
        <>
          <Todo
            items={[
              {
                key: 'make',
                led: 'warn',
                title: 'Make the GitHub App of NidaOS',
                text: 'You click one time on GitHub. GitHub makes the app, then you select the private repositories of your apps. NidaOS keeps no personal token.',
                action: (
                  <Btn
                    kind="primary"
                    disabled={busy}
                    onClick={() =>
                      run(async () => {
                        const r = await post<{ url: string }>('/api/v1/github/setup', org.trim() ? { organization: org.trim() } : {});
                        setSetupUrl(r.url);
                        window.open(r.url, '_blank', 'noopener');
                      })
                    }
                  >
                    Make the app on GitHub
                  </Btn>
                ),
              },
            ]}
          />
          <div className="card form" style={{ marginTop: 10 }}>
            <Row label="Organization" htmlFor="org" help="Empty: the app is in your personal account.">
              <input id="org" type="text" className="mid" value={org} onChange={(e) => setOrg(e.target.value)} placeholder="optional" />
            </Row>
          </div>
          {setupUrl && (
            <Note>
              If the browser did not open the page, open{' '}
              <a href={setupUrl} target="_blank" rel="noreferrer">
                the setup page
              </a>
              . The address is valid for 30 minutes.
            </Note>
          )}
          <p>
            <Btn small onClick={() => setImportOpen(!importOpen)}>
              {importOpen ? 'Close' : 'Use an app that exists'}
            </Btn>
          </p>
          {importOpen && (
            <form
              className="card form"
              onSubmit={(e) => {
                e.preventDefault();
                run(() => post('/api/v1/github/import', { app_id: Number(appId), private_key: key }), 'NidaOS uses the GitHub App.').then((ok) => {
                  if (ok) {
                    setKey('');
                    d.reload();
                  }
                });
              }}
            >
              <Row label="App ID" htmlFor="app-id">
                <input id="app-id" className="mid" type="number" value={appId} onChange={(e) => setAppId(e.target.value)} required />
              </Row>
              <Row label="Private key" htmlFor="app-key" help="The PEM file of the app (GitHub, the settings of the app, Private keys). NidaOS keeps it with the mode 0600 and never shows it.">
                <textarea id="app-key" value={key} onChange={(e) => setKey(e.target.value)} placeholder="-----BEGIN RSA PRIVATE KEY-----" required autoComplete="off" spellCheck={false} />
              </Row>
              <Row label="">
                <Btn type="submit" kind="primary" disabled={busy || !appId || !key.includes('PRIVATE KEY')}>
                  Use this app
                </Btn>
              </Row>
            </form>
          )}
        </>
      ) : (
        <Note>NidaOS has no GitHub App.</Note>
      )}

      <h3>Connected repositories</h3>
      <Table label="Connected repositories" cols={cols} rows={d.runners ? conns : undefined} rowKey={(c) => c.app} empty="No repository is connected. Connect one on the page of an app, tab Deploys." />

      <h3>
        Jobs now <span className="tag">{d.runners?.active?.length ?? 0}</span>
      </h3>
      <JobTable jobs={d.runners?.active} showApp />
      <Note kind="ok">
        A runner pod can open only the internet and the job endpoint of NidaOS: not the apps, the databases and your home network. It stops after its
        job, or after 2 hours.
      </Note>
      <LogTail source="pod/nida-runners" n={8} title="Log of the runner pods" />
      <p className="muted" style={{ fontSize: 12 }}>
        <LivePill status={live.logs} /> The list of the jobs comes again each 4 seconds.
      </p>
    </>
  );
}
