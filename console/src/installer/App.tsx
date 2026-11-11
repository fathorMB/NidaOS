// The installer page (milestone M-07): the six steps of the mockup
// design/installer. The page reads /api/state each second. Only the screen
// of the computer can start the installation; a browser of an other
// computer follows it (decision D32).

import { useEffect, useMemo, useRef, useState } from 'react';
import { Logo } from '../logo';
import { bytes } from '../format';
import {
  accessProblems,
  clock,
  gb,
  KEYBOARDS,
  KIND,
  networkProblems,
  planProblem,
  ROLE_COLOR,
  ROLES,
  samePlan,
  TIMEZONES,
  type Answers,
  type Disk,
  type InstallerState,
  type Role,
} from './model';

const STEPS = ['Welcome', 'Disks', 'Access', 'Network', 'Review and erase', 'Install'];

async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, { method, headers: body ? { 'Content-Type': 'application/json' } : {}, body: body ? JSON.stringify(body) : undefined });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    const e = new Error(data.message ?? res.statusText) as Error & { field?: string };
    e.field = data.field;
    throw e;
  }
  return data as T;
}

export function usePolledState(every = 1000) {
  const [st, setSt] = useState<InstallerState | null>(null);
  const [lost, setLost] = useState(false);
  useEffect(() => {
    let stop = false;
    const load = () =>
      api<InstallerState>('GET', '/api/state').then(
        (s) => {
          if (!stop) {
            setSt(s);
            setLost(false);
          }
        },
        () => !stop && setLost(true),
      );
    load();
    const t = setInterval(load, every);
    return () => {
      stop = true;
      clearInterval(t);
    };
  }, [every]);
  return { st, lost, setSt };
}

function Row({ label, htmlFor, children, help, err }: { label: string; htmlFor?: string; children: React.ReactNode; help?: React.ReactNode; err?: string }) {
  return (
    <div className="irow">
      {htmlFor ? <label htmlFor={htmlFor}>{label}</label> : <div className="lbl">{label}</div>}
      <div>
        {children}
        {help && <div className="help">{help}</div>}
        {err && (
          <div className="err" role="alert">
            {err}
          </div>
        )}
      </div>
    </div>
  );
}

export default function InstallerApp() {
  const { st, lost, setSt } = usePolledState();
  const [step, setStep] = useState(0);
  // The choices of the operator.
  const [keyboard, setKeyboard] = useState('it');
  const [timezone, setTimezone] = useState('Europe/Rome');
  const [roles, setRoles] = useState<Record<string, Role> | null>(null);
  const [hostname, setHostname] = useState('nidaos');
  const [user, setUser] = useState('nida');
  const [keyMode, setKeyMode] = useState<'seed' | 'paste' | 'none'>('seed');
  const [key, setKey] = useState('');
  const [password, setPassword] = useState('');
  const [repeat, setRepeat] = useState('');
  const [netMode, setNetMode] = useState<'dhcp' | 'static'>('dhcp');
  const [address, setAddress] = useState('');
  const [prefix, setPrefix] = useState('24');
  const [gateway, setGateway] = useState('');
  const [dns, setDns] = useState('');
  const [confirm, setConfirm] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [touched, setTouched] = useState(false);
  const [showLog, setShowLog] = useState(false);
  const proposedRef = useRef<Record<string, Role> | null>(null);

  const disks = useMemo(() => st?.disks ?? [], [st?.disks]);
  const proposed = st?.plan.roles ?? null;
  // The proposed plan becomes the plan of the operator, until the operator
  // changes it.
  useEffect(() => {
    if (!proposed) return;
    if (!roles || (proposedRef.current && samePlan(roles, proposedRef.current))) setRoles({ ...proposed });
    proposedRef.current = proposed;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [proposed]);
  useEffect(() => {
    if (st && !st.seed.has_key && keyMode === 'seed') setKeyMode('paste');
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [st?.seed.has_key]);
  const nic = st?.facts.network?.[0];

  const installing = st && ['installing', 'done'].includes(st.phase) && st.answers;
  const failedRun = st?.phase === 'failed' && st.answers;
  // The progress replaces the steps when an installation runs.
  const view = installing || (failedRun && step !== 4) || (st?.auto && !st.plan_only) ? 5 : step;
  const control = st?.control ?? false;

  if (!st) {
    return (
      <div className="signin">
        <div className="card" style={{ padding: 24 }}>
          {lost ? 'The installer does not answer. It starts again automatically.' : 'The installer examines the computer…'}
        </div>
      </div>
    );
  }

  const access = accessProblems({ hostname, user, keyMode, key, seedKey: st.seed.has_key, password, repeat });
  const net = networkProblems({ mode: netMode, address, prefix, gateway, dns });
  const plan = roles ?? {};
  const problem = planProblem(plan);
  const answers = (): Answers => ({
    roles: plan,
    hostname,
    user,
    ssh_key: keyMode === 'seed' ? 'seed' : keyMode === 'paste' ? key.trim() : '',
    password,
    keyboard,
    timezone,
    network:
      netMode === 'static'
        ? { mode: 'static', interface: nic?.name, address: `${address}/${prefix}`, gateway, dns: dns.split(/[\s,]+/).filter(Boolean) }
        : { mode: 'dhcp' },
    confirm: confirm.trim(),
  });

  const canNext = [
    st.phase !== 'starting',
    st.phase === 'ready' && !problem,
    Object.keys(access).length === 0,
    Object.keys(net).length === 0,
    confirm.trim() === hostname && control,
  ][view];

  const next = async () => {
    setTouched(true);
    if (!canNext) return;
    if (view < 4) {
      setStep(view + 1);
      setTouched(false);
      setError(null);
      return;
    }
    try {
      setError(null);
      const s = await api<InstallerState>('POST', '/api/install', answers());
      setSt(s);
      setPassword('');
      setRepeat('');
    } catch (e) {
      const err = e as Error & { field?: string };
      setError(err.message);
      if (err.field === 'password' || err.field === 'user' || err.field === 'hostname' || err.field === 'ssh_key') setStep(2);
    }
  };

  const used = disks.filter((d) => (plan[d.name] ?? 'none') !== 'none');
  const order = ['system', 'performance', 'standard'];
  used.sort((a, b) => order.indexOf(plan[a.name]) - order.indexOf(plan[b.name]));
  const maxSpeed = Math.max(1, ...disks.map((d) => d.speed_mbs));
  const ofRole = (r: Role) => {
    const d = disks.find((x) => plan[x.name] === r);
    return d ? `${d.model} · ${gb(d.size_bytes)}` : 'none';
  };
  const done = st.phase === 'done';
  const usableCount = disks.filter((d) => d.usable);
  const kinds = ['nvme', 'ssd', 'hdd']
    .map((k) => [k, usableCount.filter((d) => d.kind === k).length] as const)
    .filter(([, n]) => n > 0)
    .map(([k, n]) => `${n} ${KIND[k]}`)
    .join(' · ');
  const doneSteps = st.steps.filter((s) => s.state === 'done').length;

  return (
    <div className="shell">
      <aside className="rail">
        <div className="brand">
          <Logo />
          <div>
            NidaOS<small>Installer · build {st.facts.version}</small>
          </div>
        </div>
        <ol className="steps" aria-label="Steps">
          {STEPS.map((s, i) => (
            <li key={s} className={i < view || done ? 'done' : i === view ? 'now' : ''} aria-current={i === view ? 'step' : undefined}>
              <span>{s}</span>
            </li>
          ))}
        </ol>
        <div className="machine">
          <b>{st.facts.computer}</b>
          <br />
          {st.facts.cpu} · {bytes(st.facts.memory_bytes, 0)}
          <br />
          {st.facts.firmware} · {nic ? (nic.carrier ? `${nic.wireless ? 'Wireless' : 'Ethernet'} connected` : 'no network cable') : 'no network'}
        </div>
      </aside>

      <main className="main">
        <section className="ipage" aria-labelledby="step-title">
          {!control && (
            <div className="follow" role="note">
              You follow the installer from an other computer. Only the screen of the computer can change the choices and start the installation.
            </div>
          )}
          {st.auto && (
            <div className="follow" role="note">
              {st.plan_only ? 'The seed volume NIDASEED asks only for the plan of the disks.' : 'The seed volume NIDASEED starts an automatic installation.'}
            </div>
          )}
          {lost && <div className="note bad">The installer does not answer. The page tries again.</div>}

          {view === 0 && (
            <>
              <h1 id="step-title">Install NidaOS on this computer</h1>
              <p className="lead">
                The installer examined the computer. It makes a plan for the disks, then you approve the plan. The installation erases all the disks that
                the plan uses.
              </p>
              <dl className="card facts">
                <div>
                  <dt>Computer</dt>
                  <dd>{st.facts.computer}</dd>
                </div>
                <div>
                  <dt>Processor</dt>
                  <dd>
                    {st.facts.cpu} · {st.facts.threads} threads
                  </dd>
                </div>
                <div>
                  <dt>Memory</dt>
                  <dd>{bytes(st.facts.memory_bytes, 0)}</dd>
                </div>
                <div>
                  <dt>Disks</dt>
                  <dd>{kinds || 'none'}</dd>
                </div>
              </dl>
              <h2>Language of the installer</h2>
              <div className="card">
                <Row label="Language" help="The installer and NidaOS use English.">
                  <span className="addr" style={{ fontSize: 14 }}>
                    English
                  </span>
                </Row>
                <Row label="Keyboard" htmlFor="kbd">
                  <select id="kbd" value={keyboard} onChange={(e) => setKeyboard(e.target.value)} disabled={!control}>
                    {KEYBOARDS.map(([k, v]) => (
                      <option key={k} value={k}>
                        {v}
                      </option>
                    ))}
                  </select>
                </Row>
                <Row label="Time zone" htmlFor="tz">
                  <select id="tz" value={timezone} onChange={(e) => setTimezone(e.target.value)} disabled={!control}>
                    {TIMEZONES.map((z) => (
                      <option key={z}>{z}</option>
                    ))}
                  </select>
                </Row>
              </div>
            </>
          )}

          {view === 1 && (
            <>
              <h1 id="step-title">The plan for the disks</h1>
              <p className="lead">
                The installer measured the speed of each disk. The fastest disk gets the system. The other disks become storage tiers for your apps and
                databases.
              </p>
              {st.phase === 'measuring' && (
                <div className="note" role="status">
                  Measuring the speed of {st.measuring || 'the disks'}… The test only reads the disks.
                </div>
              )}
              <div className="card" aria-label="Disks">
                {disks.map((d: Disk) => {
                  const r = plan[d.name] ?? 'none';
                  return (
                    <div key={d.name} className={`disk ${d.usable ? '' : 'unusable'}`} data-role={d.usable ? r : 'none'} data-testid={`disk-${d.name}`}>
                      <div className="stripe" />
                      <div>
                        <div className="name">
                          {d.model}
                          <span className="tag" style={{ marginLeft: 6 }}>
                            {KIND[d.kind]}
                          </span>
                        </div>
                        <div className="meta">
                          {d.name} · {d.transport || 'disk'} · {gb(d.size_bytes)} · now: {d.now}
                        </div>
                      </div>
                      <div className="speed">
                        {d.usable ? (d.speed_mbs ? `${d.speed_mbs} MB/s` : st.measuring === d.name ? 'measuring…' : '–') : ''}
                        {d.usable && (
                          <i>
                            <b style={{ width: `${Math.round((d.speed_mbs / maxSpeed) * 100)}%` }} />
                          </i>
                        )}
                      </div>
                      <div className="role">
                        {d.usable ? (
                          <>
                            <select
                              aria-label={`Role of ${d.model}`}
                              value={r}
                              disabled={!control || st.phase !== 'ready'}
                              onChange={(e) => setRoles({ ...plan, [d.name]: e.target.value as Role })}
                            >
                              {(Object.keys(ROLES) as Role[]).map((k) => (
                                <option key={k} value={k}>
                                  {ROLES[k]}
                                </option>
                              ))}
                            </select>
                            <div className="why">{proposed && r === (proposed[d.name] ?? 'none') ? st.plan.reasons?.[d.name] : 'Changed by you.'}</div>
                          </>
                        ) : (
                          <div className="why">Not available: {d.reason}.</div>
                        )}
                      </div>
                    </div>
                  );
                })}
                {disks.length === 0 && <div className="irow">The installer found no disk.</div>}
              </div>
              {problem && st.phase === 'ready' ? (
                <div className="note bad">
                  <span role="alert">{problem}</span>{' '}
                  {proposed && (
                    <button className="btn small" onClick={() => setRoles({ ...proposed })}>
                      Use the proposed plan
                    </button>
                  )}
                </div>
              ) : proposed && !samePlan(plan, proposed) ? (
                <div className="note">
                  This is not the proposed plan.{' '}
                  <button className="btn small" onClick={() => setRoles({ ...proposed })}>
                    Use the proposed plan
                  </button>
                </div>
              ) : null}
              {st.plan.notes?.map((n) => (
                <div key={n} className="note">
                  {n}
                </div>
              ))}
              <h2>Result</h2>
              <div className="layout" aria-label="Result">
                {used.map((d) => (
                  <span key={d.name} style={{ background: ROLE_COLOR[plan[d.name]], flex: Math.max(d.size_bytes / 1e9, 180) }}>
                    {ROLES[plan[d.name]].replace(' tier', '')} · {gb(d.size_bytes)}
                  </span>
                ))}
              </div>
              <div className="legend">
                <span>
                  <i className="dot" style={{ background: 'var(--sys)' }} />
                  <b>System</b> NidaOS and its updates
                </span>
                <span>
                  <i className="dot" style={{ background: 'var(--perf)' }} />
                  <b>Performance</b> databases and fast apps
                </span>
                <span>
                  <i className="dot" style={{ background: 'var(--std)' }} />
                  <b>Standard</b> large data, backups, default
                </span>
              </div>
              {control && st.phase === 'ready' && (
                <p>
                  <button className="btn small" onClick={() => api('POST', '/api/measure').catch(() => {})}>
                    Measure the disks again
                  </button>
                </p>
              )}
            </>
          )}

          {view === 2 && (
            <>
              <h1 id="step-title">Who can come in</h1>
              <p className="lead">
                NidaOS has no default password. You give one owner password for the web console and Remote Desktop. The terminal uses an SSH key.
              </p>
              <div className="card">
                <Row label="Computer name" htmlFor="host" err={touched ? access.hostname : undefined} help={<>Other computers find it as <span className="addr" style={{ fontSize: 13 }}>{hostname || 'nidaos'}.local</span></>}>
                  <input id="host" type="text" value={hostname} onChange={(e) => setHostname(e.target.value.toLowerCase())} />
                </Row>
                <Row label="Owner" htmlFor="user" err={touched ? access.user : undefined} help="The administrator of the computer. NidaOS adds it to the groups wheel and nida.">
                  <input id="user" type="text" value={user} onChange={(e) => setUser(e.target.value.toLowerCase())} />
                </Row>
                <Row label="SSH key" err={touched ? access.key : undefined}>
                  <label className={`radio ${st.seed.has_key ? '' : 'off'}`}>
                    <input type="radio" name="key" checked={keyMode === 'seed'} disabled={!st.seed.has_key} onChange={() => setKeyMode('seed')} /> Use the key on the USB volume{' '}
                    <span className="tag">NIDASEED</span>
                    {st.seed.key_comment && <span className="muted"> · {st.seed.key_comment}</span>}
                  </label>
                  <label className="radio">
                    <input type="radio" name="key" checked={keyMode === 'paste'} onChange={() => setKeyMode('paste')} /> Paste a public key
                  </label>
                  {keyMode === 'paste' && <textarea aria-label="Public key" value={key} onChange={(e) => setKey(e.target.value)} placeholder="ssh-ed25519 AAAA… operator@pc" spellCheck={false} />}
                  <label className="radio">
                    <input type="radio" name="key" checked={keyMode === 'none'} onChange={() => setKeyMode('none')} /> No key: use the terminal only on the screen
                  </label>
                </Row>
                <Row label="Owner password" htmlFor="pw" err={touched ? access.password : undefined} help="For the web console and for Remote Desktop. 12 characters or more.">
                  <input id="pw" type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} />
                </Row>
                <Row label="Repeat the password" htmlFor="pw2" err={touched ? access.repeat : undefined}>
                  <input id="pw2" type="password" autoComplete="new-password" value={repeat} onChange={(e) => setRepeat(e.target.value)} />
                </Row>
              </div>
            </>
          )}

          {view === 3 && (
            <>
              <h1 id="step-title">Network</h1>
              <p className="lead">Keep the automatic address, or set a fixed address for a server.</p>
              <div className="card">
                <Row label={nic?.wireless ? 'Wireless' : 'Ethernet'}>
                  <div className="addr">
                    {nic ? `${nic.name}${nic.driver ? ' · ' + nic.driver : ''}${nic.speed_mbit ? ` · ${nic.speed_mbit >= 1000 ? nic.speed_mbit / 1000 + ' Gbit/s' : nic.speed_mbit + ' Mbit/s'}` : ''} · ${nic.carrier ? 'connected' : 'no cable'}` : 'No network interface'}
                  </div>
                </Row>
                <Row label="Address">
                  <label className="radio">
                    <input type="radio" name="ip" checked={netMode === 'dhcp'} onChange={() => setNetMode('dhcp')} /> Automatic (DHCP)
                    {nic?.addresses?.[0] && (
                      <>
                        {' '}
                        — now <span className="addr" style={{ fontSize: 13 }}>{nic.addresses[0].split('/')[0]}</span>
                      </>
                    )}
                  </label>
                  <label className="radio">
                    <input type="radio" name="ip" checked={netMode === 'static'} onChange={() => setNetMode('static')} /> Fixed address
                  </label>
                </Row>
                {netMode === 'static' && (
                  <>
                    <Row label="Fixed address" htmlFor="addr" err={touched ? net.address || net.prefix : undefined}>
                      <div className="inline">
                        <input id="addr" type="text" value={address} onChange={(e) => setAddress(e.target.value)} placeholder={nic?.addresses?.[0]?.split('/')[0] ?? '192.168.1.50'} style={{ maxWidth: 220 }} /> /
                        <input type="text" aria-label="Prefix" value={prefix} onChange={(e) => setPrefix(e.target.value)} style={{ maxWidth: 70 }} />
                      </div>
                    </Row>
                    <Row label="Router" htmlFor="gw" err={touched ? net.gateway : undefined}>
                      <input id="gw" type="text" value={gateway} onChange={(e) => setGateway(e.target.value)} placeholder="192.168.1.1" style={{ maxWidth: 220 }} />
                    </Row>
                    <Row label="DNS servers" htmlFor="dns" err={touched ? net.dns : undefined} help="Empty: the router.">
                      <input id="dns" type="text" value={dns} onChange={(e) => setDns(e.target.value)} placeholder="1.1.1.1 8.8.8.8" style={{ maxWidth: 320 }} />
                    </Row>
                  </>
                )}
                <Row label="Internet gateway" help="The gateway is the small device that gives your apps a public address with sign-in.">
                  <label className="radio">
                    <input type="radio" name="gw" checked readOnly /> Connect a NidaOS gateway later, from the console
                  </label>
                  <label className="radio off">
                    <input type="radio" name="gw" disabled /> Connect now with a pairing code (it comes with the gateway, milestone M-10)
                  </label>
                </Row>
              </div>
            </>
          )}

          {view === 4 && (
            <>
              <h1 id="step-title">Review, then erase</h1>
              <p className="lead">This is the last step that you can cancel. After it, the installer erases the disks below.</p>
              <div className="card">
                <table className="sum" aria-label="Summary">
                  <tbody>
                    <tr>
                      <th>Computer name</th>
                      <td>{hostname}</td>
                    </tr>
                    <tr>
                      <th>Owner</th>
                      <td>
                        {user} · {keyMode === 'seed' ? 'SSH key from NIDASEED' : keyMode === 'paste' ? 'pasted SSH key' : 'no SSH key'} · owner password set
                      </td>
                    </tr>
                    <tr>
                      <th>System</th>
                      <td>{ofRole('system')}</td>
                    </tr>
                    <tr>
                      <th>Performance tier</th>
                      <td>{ofRole('performance')}</td>
                    </tr>
                    <tr>
                      <th>Standard tier</th>
                      <td>{ofRole('standard')}</td>
                    </tr>
                    <tr>
                      <th>Network</th>
                      <td>{netMode === 'static' ? `fixed ${address}/${prefix} · router ${gateway}` : `${nic?.wireless ? 'wireless' : 'Ethernet'} · automatic address`}</td>
                    </tr>
                    <tr>
                      <th>Keyboard and time</th>
                      <td>
                        {KEYBOARDS.find(([k]) => k === keyboard)?.[1]} · {timezone}
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
              <div className="note bad">
                <b>These disks lose all their data.</b> {used.map((d) => `${d.model} (${d.name}, now: ${d.now})`).join(', ')}.
              </div>
              <h2>Confirm</h2>
              <div className="card">
                <Row label="Type the computer name" htmlFor="confirm" help="The button becomes available when the name is correct.">
                  <input id="confirm" type="text" autoComplete="off" placeholder={hostname} value={confirm} onChange={(e) => setConfirm(e.target.value)} disabled={!control} />
                </Row>
              </div>
              {error && (
                <div className="note bad" role="alert">
                  {error}
                </div>
              )}
            </>
          )}

          {view === 5 && (
            <>
              <h1 id="step-title">{done ? 'NidaOS is ready' : st.phase === 'failed' ? 'The installation stopped' : 'Installing NidaOS'}</h1>
              <p className="lead">
                {done
                  ? 'The installation is complete. The plan for the disks is saved on the computer.'
                  : st.phase === 'failed'
                    ? 'The installer stopped at the step below. The disks of the plan can be partly erased.'
                    : st.auto && !st.answers
                      ? 'The installer measures the disks and makes the plan.'
                      : 'You can leave the computer. The installation needs some minutes. The computer restarts at the end.'}
              </p>
              <div className="meter" role="progressbar" aria-valuenow={doneSteps} aria-valuemin={0} aria-valuemax={st.steps.length}>
                <b style={{ width: `${Math.round((doneSteps / st.steps.length) * 100)}%` }} />
              </div>
              <div className="card">
                <ul className="tasks" aria-label="Installation steps">
                  {st.steps.map((s) => (
                    <li key={s.name} className={s.state}>
                      {s.name}
                      <time>{s.state ? clock(s.seconds) : ''}</time>
                    </li>
                  ))}
                </ul>
              </div>
              {st.phase === 'failed' && st.error && (
                <div className="note bad" role="alert">
                  {st.error}
                </div>
              )}
              {done && (
                <>
                  <div className="note ok">
                    <b>NidaOS is installed.</b> Remove the installation media, then restart.
                  </div>
                  <h2>How to connect after the restart</h2>
                  <div className="card">
                    <table className="sum" aria-label="Addresses">
                      <tbody>
                        <tr>
                          <th>Console</th>
                          <td>https://{st.answers?.hostname}.local</td>
                        </tr>
                        <tr>
                          <th>Remote Desktop</th>
                          <td>{st.answers?.hostname}.local · port 3389</td>
                        </tr>
                        <tr>
                          <th>Terminal</th>
                          <td>
                            ssh {st.answers?.user}@{st.answers?.hostname}.local
                          </td>
                        </tr>
                      </tbody>
                    </table>
                  </div>
                </>
              )}
              <p>
                <button className="btn small" onClick={() => setShowLog(!showLog)}>
                  {showLog ? 'Hide the details' : 'Show the details'}
                </button>
              </p>
              {showLog && (
                <pre className="card ilog" aria-label="Log">
                  {(st.log ?? []).join('\n')}
                </pre>
              )}
            </>
          )}
        </section>

        <div className="bar">
          <span className="hint">{view === 4 ? 'You cannot cancel after this step.' : view === 1 && st.phase === 'measuring' ? 'Wait for the end of the measurement.' : ''}</span>
          {view > 0 && view < 5 && (
            <button className="btn" onClick={() => setStep(view - 1)}>
              Back
            </button>
          )}
          {view < 4 && (
            <button className="btn primary" onClick={next} disabled={view > 0 && !canNext && !(view >= 2 && !touched)}>
              Continue
            </button>
          )}
          {view === 4 && (
            <button className="btn erase" onClick={next} disabled={!canNext}>
              Erase the disks and install
            </button>
          )}
          {view === 5 && st.phase === 'failed' && control && (
            <button className="btn" onClick={() => setStep(4)}>
              Back to the review
            </button>
          )}
          {view === 5 && done && control && (
            <button className="btn primary" onClick={() => api('POST', '/api/restart').catch(() => {})}>
              Restart
            </button>
          )}
        </div>
      </main>
    </div>
  );
}
