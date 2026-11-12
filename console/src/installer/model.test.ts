import { accessProblems, gb, networkProblems, planProblem, sameNet } from './model';

test('the problems of a plan', () => {
  expect(planProblem({ sda: 'system', sdb: 'performance' })).toBe('');
  expect(planProblem({ sda: 'performance' })).toContain('System');
  expect(planProblem({ sda: 'system', sdb: 'system' })).toContain('same role');
});

test('the access step', () => {
  const ok = { hostname: 'nidaos', user: 'nida', keyMode: 'seed', key: '', seedKey: true, password: 'a long password', repeat: 'a long password' };
  expect(accessProblems(ok)).toEqual({});
  expect(accessProblems({ ...ok, repeat: 'other' }).repeat).toBeTruthy();
  expect(accessProblems({ ...ok, password: 'short', repeat: 'short' }).password).toBeTruthy();
  expect(accessProblems({ ...ok, keyMode: 'paste', key: 'not a key' }).key).toBeTruthy();
  expect(accessProblems({ ...ok, user: 'root' }).user).toBeTruthy();
});

test('the network step', () => {
  expect(networkProblems({ mode: 'dhcp', address: '', prefix: '', gateway: '', dns: '' })).toEqual({});
  expect(networkProblems({ mode: 'static', address: '192.168.1.50', prefix: '24', gateway: '192.168.1.1', dns: '1.1.1.1 8.8.8.8' })).toEqual({});
  expect(networkProblems({ mode: 'static', address: '192.168.1.50', prefix: '24', gateway: '10.0.0.1', dns: '' }).gateway).toContain('network');
  expect(sameNet('10.1.2.3', '10.1.200.1', 16)).toBe(true);
  expect(gb(120034123776)).toBe('120 GB');
});
