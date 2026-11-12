// The client of the NidaOS API for the desktop (milestone M-08). The
// extension and the programs use the local socket of nida-core: the owner
// is in the group nida, thus the requests need no password (decision D18).
// The live streams are Server-Sent Events, as in the web console (D8).

import Gio from 'gi://Gio';
import GLib from 'gi://GLib';
import Soup from 'gi://Soup?version=3.0';

Gio._promisify(Soup.Session.prototype, 'send_and_read_async', 'send_and_read_finish');
Gio._promisify(Soup.Session.prototype, 'send_async', 'send_finish');
Gio._promisify(Gio.DataInputStream.prototype, 'read_line_async', 'read_line_finish_utf8');

export const SOCKET = '/run/nidaos/core.sock';

export class NidaAPI {
    constructor(socket = SOCKET) {
        this._session = new Soup.Session({
            remote_connectable: Gio.UnixSocketAddress.new(socket),
            timeout: 15,
        });
        this._streams = new Set();
    }

    _message(method, path, body) {
        const msg = Soup.Message.new(method, `http://nida${path}`);
        if (body !== undefined) {
            const bytes = new TextEncoder().encode(JSON.stringify(body));
            msg.set_request_body_from_bytes('application/json', new GLib.Bytes(bytes));
        }
        return msg;
    }

    // request sends a request and gives the JSON of the response. An error
    // of the API gives an Error with the code and the message.
    async request(method, path, body) {
        const msg = this._message(method, path, body);
        const bytes = await this._session.send_and_read_async(msg, GLib.PRIORITY_DEFAULT, null);
        const status = msg.get_status();
        const text = new TextDecoder().decode(bytes.get_data() ?? new Uint8Array());
        let data = null;
        if (text.trim() !== '') {
            try {
                data = JSON.parse(text);
            } catch {
                data = text;
            }
        }
        if (status >= 300) {
            const err = new Error(data?.message ?? `NidaOS answered ${status}`);
            err.code = data?.code ?? `http_${status}`;
            err.status = status;
            throw err;
        }
        return data;
    }

    get(path) {
        return this.request('GET', path);
    }

    post(path, body) {
        return this.request('POST', path, body ?? {});
    }

    // stream reads a live stream. onEvent(name, data) gets each event,
    // onState(state) gets "live" or "stopped". The stream connects again
    // after an error. It gives a function that stops the stream.
    stream(path, onEvent, onState) {
        const cancel = new Gio.Cancellable();
        this._streams.add(cancel);
        let stopped = false;
        const run = async () => {
            while (!stopped) {
                try {
                    const msg = this._message('GET', path);
                    const input = await this._session.send_async(msg, GLib.PRIORITY_DEFAULT, cancel);
                    if (msg.get_status() !== 200)
                        throw new Error(`the stream answered ${msg.get_status()}`);
                    onState?.('live');
                    const lines = new Gio.DataInputStream({base_stream: input, close_base_stream: true});
                    const decoder = new TextDecoder();
                    let event = 'message';
                    let data = '';
                    for (;;) {
                        // GNOME Shell can make read_line_async give bytes,
                        // not text: decode the bytes.
                        const [raw] = await lines.read_line_async(GLib.PRIORITY_DEFAULT, cancel);
                        if (raw === null)
                            break;
                        const line = typeof raw === 'string' ? raw : decoder.decode(raw);
                        if (line === '') {
                            if (data !== '') {
                                try {
                                    onEvent(event, JSON.parse(data));
                                } catch (e) {
                                    logError(e, 'NidaOS stream event');
                                }
                            }
                            event = 'message';
                            data = '';
                        } else if (line.startsWith('event:')) {
                            event = line.slice(6).trim();
                        } else if (line.startsWith('data:')) {
                            data += line.slice(5).trim();
                        }
                    }
                } catch (e) {
                    if (cancel.is_cancelled())
                        return;
                    console.warn(`NidaOS stream ${path}: ${e.message}`);
                }
                onState?.('stopped');
                await new Promise(resolve => GLib.timeout_add_seconds(GLib.PRIORITY_DEFAULT, 3, () => {
                    resolve();
                    return GLib.SOURCE_REMOVE;
                }));
            }
        };
        run();
        return () => {
            stopped = true;
            cancel.cancel();
            this._streams.delete(cancel);
        };
    }

    destroy() {
        for (const c of this._streams)
            c.cancel();
        this._streams.clear();
        this._session.abort();
    }
}

// The formats of the numbers, as in the web console.
export function bytes(n, digits = 1) {
    if (n === undefined || n === null || !Number.isFinite(n))
        return '–';
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let v = Math.abs(n);
    let i = 0;
    while (v >= 1024 && i < units.length - 1) {
        v /= 1024;
        i++;
    }
    return `${v.toFixed(i === 0 || v >= 100 ? 0 : digits)} ${units[i]}`;
}

export function clock(t) {
    const d = t ? new Date(t) : new Date();
    return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
}

export function clockSeconds(t) {
    const d = new Date(t);
    return `${clock(t)}:${String(d.getSeconds()).padStart(2, '0')}`;
}

// sourceName gives the short name of a log source, as in the console.
export function sourceName(line) {
    const ns = line.source?.namespace ?? '';
    if (ns.startsWith('nida-app-'))
        return ns.slice(9);
    if (ns.startsWith('nida-db-'))
        return ns.slice(8);
    if (ns === 'nida-runners')
        return 'runner';
    return line.source?.name ?? '';
}
