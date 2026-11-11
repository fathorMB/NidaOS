// The formats of the numbers and the times in the console.

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB'];

// bytes gives "412 MB" or "3.8 GB". The console uses units of 1024.
export function bytes(n: number | undefined | null, digits = 1): string {
  if (n === undefined || n === null || !Number.isFinite(n)) return '–';
  let v = Math.abs(n);
  let i = 0;
  while (v >= 1024 && i < UNITS.length - 1) {
    v /= 1024;
    i++;
  }
  const d = i === 0 || v >= 100 ? 0 : digits;
  return `${n < 0 ? '-' : ''}${v.toFixed(d)} ${UNITS[i]}`;
}

export const GiB = 1024 ** 3;
export const MiB = 1024 ** 2;

// rate gives a network rate in Mbit/s.
export function mbits(bytesPerSecond: number): number {
  return (bytesPerSecond * 8) / 1_000_000;
}

export function fixed(n: number | undefined | null, digits = 0): string {
  if (n === undefined || n === null || !Number.isFinite(n)) return '–';
  return n.toFixed(digits);
}

// ago gives "6 min ago" for a time before now.
export function ago(when: string | number | undefined | null, now = Date.now()): string {
  if (when === undefined || when === null || when === '') return '–';
  const t = typeof when === 'number' ? when : Date.parse(when);
  if (!Number.isFinite(t)) return '–';
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 5) return 'now';
  if (s < 60) return `${s} s ago`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m} min ago`;
  const h = Math.round(m / 60);
  if (h < 48) return `${h} h ago`;
  const d = Math.round(h / 24);
  if (d < 21) return `${d} days ago`;
  return dateShort(t);
}

// duration gives "1 min 12 s" for a time in milliseconds.
export function duration(ms: number | undefined | null): string {
  if (ms === undefined || ms === null || !Number.isFinite(ms) || ms < 0) return '–';
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s} s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} min ${String(s % 60).padStart(2, '0')} s`;
  const h = Math.floor(m / 60);
  if (h < 48) return `${h} h ${m % 60} min`;
  return `${Math.floor(h / 24)} days`;
}

export function between(start?: string, end?: string): string {
  if (!start || !end) return '–';
  return duration(Date.parse(end) - Date.parse(start));
}

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

export function dateShort(t: string | number): string {
  const d = new Date(t);
  return `${d.getDate()} ${MONTHS[d.getMonth()]}`;
}

// clock gives "21:03:15".
export function clock(t: string | number | null | undefined): string {
  if (t === undefined || t === null) return '–';
  const d = new Date(t);
  if (Number.isNaN(d.getTime())) return '–';
  return d.toTimeString().slice(0, 8);
}

// dateTime gives "11 Nov 21:03".
export function dateTime(t: string | number | undefined): string {
  if (t === undefined || t === '') return '–';
  const d = new Date(t);
  if (Number.isNaN(d.getTime())) return '–';
  return `${dateShort(d.getTime())} ${d.toTimeString().slice(0, 5)}`;
}

export function shortSha(sha?: string): string {
  return sha ? sha.slice(0, 7) : '–';
}

export function tierLabel(t?: string): string {
  if (t === 'performance') return 'Performance';
  if (t === 'standard') return 'Standard';
  return t ?? '–';
}

// errorText gives the message of an error for a note.
export function errorText(e: unknown): string {
  if (e instanceof Error) return e.message;
  return String(e);
}

// plural gives "1 app" or "3 apps".
export function plural(n: number, word: string): string {
  return `${n} ${word}${n === 1 ? '' : 's'}`;
}
