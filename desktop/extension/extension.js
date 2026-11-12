// The NidaOS desktop (milestone M-08, decision D11). In the session of the
// owner, the extension hides the top panel of GNOME and shows the monitor
// of the mockup design/desktop: the top bar, the live metrics, the storage
// tiers, the apps and the databases, the connections, the events and the
// dock with the terminal, the files, the logs and the power dialog. The
// data come from nida-core through the local socket (decision D8).

import Clutter from 'gi://Clutter';
import Gio from 'gi://Gio';
import GLib from 'gi://GLib';
import GObject from 'gi://GObject';
import St from 'gi://St';

import {Extension} from 'resource:///org/gnome/shell/extensions/extension.js';
import * as Main from 'resource:///org/gnome/shell/ui/main.js';
import * as ModalDialog from 'resource:///org/gnome/shell/ui/modalDialog.js';

import {NidaAPI, bytes, clock} from './api.js';

const HISTORY = 40;
const TIER_COLOR = {performance: '#f08a3e', standard: '#7fb0d6', system: '#b9aea3'};

// spawn starts a program of the desktop.
function spawn(argv) {
    try {
        Gio.Subprocess.new(argv, Gio.SubprocessFlags.NONE);
    } catch (e) {
        Main.notify('NidaOS', `The program ${argv[0]} did not start: ${e.message}`);
    }
}

const Spark = GObject.registerClass(
class NidaSpark extends St.DrawingArea {
    _init(max) {
        super._init({style_class: 'nida-spark', x_expand: true});
        this._values = [];
        this._max = max;
    }

    setValues(values) {
        this._values = values;
        this.queue_repaint();
    }

    vfunc_repaint() {
        const cr = this.get_context();
        const [w, h] = this.get_surface_size();
        const v = this._values;
        if (v.length > 1) {
            const hi = Math.max(this._max ?? 0, ...v, 1e-9);
            const step = w / (v.length - 1);
            cr.setSourceRGBA(0.94, 0.91, 0.87, 0.9);
            cr.setLineWidth(1.5);
            v.forEach((x, i) => {
                const y = h - 2 - (x / hi) * (h - 6);
                if (i === 0)
                    cr.moveTo(0, y);
                else
                    cr.lineTo(i * step, y);
            });
            cr.strokePreserve();
            cr.lineTo(w, h);
            cr.lineTo(0, h);
            cr.closePath();
            cr.setSourceRGBA(0.94, 0.91, 0.87, 0.08);
            cr.fill();
        }
        cr.$dispose();
    }
});

function label(text, style = '') {
    return new St.Label({text, style_class: style, y_align: Clutter.ActorAlign.CENTER});
}

function led(kind = '') {
    return new St.Widget({style_class: `nida-led ${kind}`, y_align: Clutter.ActorAlign.CENTER});
}

function card(title, note = '') {
    const box = new St.BoxLayout({orientation: Clutter.Orientation.VERTICAL, style_class: 'nida-card', x_expand: true});
    const head = new St.BoxLayout();
    head.add_child(label(title.toUpperCase(), 'nida-card-title'));
    const n = label(note, 'nida-card-note');
    n.x_expand = true;
    n.x_align = Clutter.ActorAlign.END;
    head.add_child(n);
    box.add_child(head);
    box._note = n;
    return box;
}

// A meter is a card with a big value, a sub line and a small graph.
function meter(title, unit, max) {
    const c = card(title);
    const row = new St.BoxLayout();
    const value = label('–', 'nida-big');
    row.add_child(value);
    row.add_child(label(` ${unit}`, 'nida-big-unit'));
    c.add_child(row);
    const sub = label('', 'nida-sub');
    c.add_child(sub);
    const spark = new Spark(max);
    c.add_child(spark);
    return {card: c, value, sub, spark};
}

const PowerDialog = GObject.registerClass(
class NidaPowerDialog extends ModalDialog.ModalDialog {
    _init(desktop) {
        super._init({styleClass: 'nida-power'});
        const box = this.contentLayout;
        box.add_child(label('Power', 'nida-big'));
        const running = desktop.workloadCount();
        box.add_child(label(`${running} apps and databases are in operation. A restart stops them for some minutes.`, 'nida-sub'));
        const choice = (title, text, action, danger = false) => {
            const b = new St.Button({style_class: 'nida-power-choice', x_expand: true, can_focus: true});
            const v = new St.BoxLayout({orientation: Clutter.Orientation.VERTICAL});
            v.add_child(label(title, danger ? 'nida-power-danger' : 'nida-tier-name'));
            v.add_child(label(text, 'nida-sub'));
            b.set_child(v);
            b.connect('clicked', () => {
                this.close();
                action();
            });
            box.add_child(b);
        };
        choice('Disconnect', 'Close this remote session. The computer continues.',
            () => spawn(['gnome-session-quit', '--logout', '--no-prompt']));
        choice('Restart', 'Stop the apps, restart, then start the apps again.',
            () => spawn(['systemctl', 'reboot']));
        if (desktop.stagedUpdate()) {
            choice(`Restart and install ${desktop.stagedUpdate()}`, 'The same as a restart, with the update.',
                () => desktop.api.post('/api/v1/updates/restart').catch(e => Main.notify('NidaOS', e.message)));
        }
        choice('Shut down', 'The apps stay off until a person pushes the power button.',
            () => spawn(['systemctl', 'poweroff']), true);
        this.addButton({label: 'Cancel', action: () => this.close(), key: Clutter.KEY_Escape});
    }
});

class Desktop {
    constructor(extension) {
        this._ext = extension;
        this.api = new NidaAPI();
        this._history = [];
        this._data = {};
        this._timers = [];
        this._stops = [];
    }

    workloadCount() {
        return (this._data.apps?.length ?? 0) + (this._data.databases?.length ?? 0);
    }

    stagedUpdate() {
        return this._data.updates?.staged?.version ?? null;
    }

    build() {
        const mon = Main.layoutManager.primaryMonitor;
        // The top bar.
        this._top = new St.BoxLayout({style_class: 'nida-top', reactive: true, height: 44, width: mon.width, x: mon.x, y: mon.y});
        this._top.add_child(label('N', 'nida-brand-mark'));
        this._top.add_child(label('NidaOS', 'nida-brand'));
        this._host = label(GLib.get_host_name(), 'nida-host');
        this._top.add_child(this._host);
        this._attention = new St.BoxLayout({style_class: 'nida-pill'});
        this._attentionLed = led('');
        this._attention.add_child(this._attentionLed);
        this._attentionText = label('Reading the state…');
        this._attention.add_child(this._attentionText);
        this._top.add_child(this._attention);
        const spacer = new St.Widget({x_expand: true});
        this._top.add_child(spacer);
        const session = new St.BoxLayout({style_class: 'nida-pill'});
        session.add_child(led(''));
        session.add_child(label(`Session · ${GLib.get_user_name()}`));
        this._top.add_child(session);
        this._clock = label(clock(), 'nida-clock');
        this._top.add_child(this._clock);
        Main.layoutManager.addChrome(this._top, {affectsStruts: true, trackFullscreen: true});

        // The dock.
        this._dock = new St.BoxLayout({style_class: 'nida-dock', reactive: true, width: mon.width, x: mon.x});
        const dockButton = (title, icon, action) => {
            const b = new St.Button({style_class: 'nida-dock-button', can_focus: true, toggle_mode: false});
            const v = new St.BoxLayout({orientation: Clutter.Orientation.VERTICAL, x_align: Clutter.ActorAlign.CENTER});
            v.add_child(new St.Icon({icon_name: icon, style_class: 'nida-dock-icon', x_align: Clutter.ActorAlign.CENTER}));
            v.add_child(label(title));
            b.set_child(v);
            b.connect('clicked', action);
            this._dock.add_child(b);
            return b;
        };
        const left = new St.Widget({x_expand: true});
        this._dock.add_child(left);
        dockButton('Monitor', 'utilities-system-monitor-symbolic', () => Main.overview.hide());
        dockButton('Terminal', 'utilities-terminal-symbolic', () => spawn(['ptyxis', '--new-window']));
        dockButton('Files', 'folder-symbolic', () => spawn(['gjs', '-m', '/usr/share/nidaos/desktop/nida-files.js']));
        dockButton('Logs', 'text-x-generic-symbolic', () => spawn(['gjs', '-m', '/usr/share/nidaos/desktop/nida-logs.js']));
        this._dock.add_child(new St.Widget({style_class: 'nida-dock-sep'}));
        dockButton('Console', 'web-browser-symbolic', () =>
            Main.notify('NidaOS console', `Open https://${GLib.get_host_name()}.local in the browser of your computer.`));
        dockButton('Power', 'system-shutdown-symbolic', () => new PowerDialog(this).open());
        this._dock.add_child(new St.Widget({x_expand: true}));
        Main.layoutManager.addChrome(this._dock, {affectsStruts: true, trackFullscreen: true});
        this._dock.y = mon.y + mon.height - 64;
        this._dock.height = 64;

        // The monitor, below the windows.
        this._desk = new St.ScrollView({style_class: 'nida-desk', x: mon.x, y: mon.y + 44, width: mon.width, height: mon.height - 44 - 64});
        const grid = new St.BoxLayout({orientation: Clutter.Orientation.VERTICAL, style_class: 'nida-grid', x_expand: true});
        this._desk.set_child(grid);
        // The background group is in the window group: the monitor goes just
        // above it, below the windows.
        global.window_group.insert_child_above(this._desk, Main.layoutManager._backgroundGroup);

        // Row 1: the meters and the system.
        const row1 = new St.BoxLayout({style_class: 'nida-row', x_expand: true});
        this._cpu = meter('Processor', '%', 100);
        this._mem = meter('Memory', 'GB used');
        this._net = meter('Network', 'Mbit/s in');
        this._sys = card('System');
        this._sysVersion = label('–', 'nida-big');
        this._sys.add_child(this._sysVersion);
        this._sysSub = label('', 'nida-sub');
        this._sys.add_child(this._sysSub);
        this._sysUpdate = new St.BoxLayout({orientation: Clutter.Orientation.VERTICAL});
        this._sys.add_child(this._sysUpdate);
        for (const c of [this._cpu.card, this._mem.card, this._net.card, this._sys])
            row1.add_child(c);
        grid.add_child(row1);

        // Row 2: the storage tiers.
        this._storage = card('Storage');
        this._tiers = new St.BoxLayout({style_class: 'nida-row'});
        this._storage.add_child(this._tiers);
        grid.add_child(this._storage);

        // Row 3: the workloads, the connections and the events.
        const row3 = new St.BoxLayout({style_class: 'nida-row', x_expand: true});
        this._work = card('Apps and databases');
        this._workRows = new St.BoxLayout({orientation: Clutter.Orientation.VERTICAL});
        this._work.add_child(this._workRows);
        this._work.x_expand = true;
        const side = new St.BoxLayout({orientation: Clutter.Orientation.VERTICAL, style_class: 'nida-grid', width: 420});
        this._conn = card('Connections');
        this._connRows = new St.BoxLayout({orientation: Clutter.Orientation.VERTICAL});
        this._conn.add_child(this._connRows);
        this._events = card('Events');
        this._eventRows = new St.BoxLayout({orientation: Clutter.Orientation.VERTICAL});
        this._events.add_child(this._eventRows);
        side.add_child(this._conn);
        side.add_child(this._events);
        row3.add_child(this._work);
        row3.add_child(side);
        grid.add_child(row3);
        this._stale = label('', 'nida-stale');
        grid.add_child(this._stale);
    }

    start() {
        this._stops.push(this.api.stream('/api/v1/metrics/stream', (ev, s) => {
            if (ev !== 'metrics')
                return;
            this._history = [...this._history.slice(-(HISTORY - 1)), s];
            this._lastData = Date.now();
            this.drawMetrics();
        }, state => {
            this._streamState = state;
            this.drawStale();
        }));
        const load = () => {
            this.load().catch(e => logError(e, 'NidaOS desktop'));
            return GLib.SOURCE_CONTINUE;
        };
        load();
        this._timers.push(GLib.timeout_add_seconds(GLib.PRIORITY_DEFAULT, 4, load));
        this._timers.push(GLib.timeout_add_seconds(GLib.PRIORITY_DEFAULT, 1, () => {
            this._clock.text = clock();
            this.drawStale();
            return GLib.SOURCE_CONTINUE;
        }));
    }

    async load() {
        const opt = p => this.api.get(p).catch(() => null);
        const [state, apps, dbs, updates, runners, approvals] = await Promise.all([
            opt('/api/v1/state'), opt('/api/v1/apps'), opt('/api/v1/databases'), opt('/api/v1/updates'),
            opt('/api/v1/runners'), opt('/api/v1/approvals?state=pending'),
        ]);
        this._data = {state, apps: apps?.apps ?? [], databases: dbs?.databases ?? [], updates, runners, approvals: approvals?.approvals ?? []};
        this.drawState();
    }

    drawStale() {
        const stopped = this._streamState === 'stopped' || (this._lastData && Date.now() - this._lastData > 10000);
        this._stale.text = stopped
            ? `The live data stopped. The values are from ${this._lastData ? clock(this._lastData) : 'no time'}.`
            : '';
    }

    drawMetrics() {
        const h = this._history;
        const s = h[h.length - 1];
        if (!s)
            return;
        const cpu = s.host.cpu.usage_percent;
        this._cpu.value.text = cpu.toFixed(0);
        this._cpu.sub.text = `${s.host.cpu.temperature_c !== undefined ? `${s.host.cpu.temperature_c.toFixed(0)} °C · ` : ''}load ${s.host.load[0].toFixed(2)}`;
        this._cpu.spark.setValues(h.map(x => x.host.cpu.usage_percent));
        const mem = s.host.memory;
        this._mem.value.text = (mem.used_bytes / 1024 ** 3).toFixed(1);
        this._mem.sub.text = `${bytes(mem.available_bytes)} free for apps · ${bytes(mem.total_bytes)}`;
        this._mem.spark._max = mem.total_bytes / 1024 ** 3;
        this._mem.spark.setValues(h.map(x => x.host.memory.used_bytes / 1024 ** 3));
        const rx = x => x.host.network.reduce((n, i) => n + i.receive_bytes_per_second, 0) * 8 / 1e6;
        const tx = x => x.host.network.reduce((n, i) => n + i.transmit_bytes_per_second, 0) * 8 / 1e6;
        this._net.value.text = rx(s).toFixed(1);
        this._net.sub.text = `out ${tx(s).toFixed(1)} Mbit/s`;
        this._net.spark.setValues(h.map(rx));
        // The processor and the memory of each workload.
        this._usage = new Map();
        for (const w of s.workloads ?? []) {
            if (w.kind !== 'pod' || !w.namespace)
                continue;
            const u = this._usage.get(w.namespace) ?? {cpu: 0, mem: 0};
            u.cpu += w.cpu_cores * 100;
            u.mem += w.memory_bytes;
            this._usage.set(w.namespace, u);
        }
    }

    drawState() {
        const d = this._data;
        // The VM test looks for this line in the log of the shell.
        if (!this._shown && d.state) {
            this._shown = true;
            console.log('NidaOS desktop: the first data from nida-core is on the screen');
        }
        // The top bar.
        const items = [];
        if (d.updates?.staged)
            items.push(`Update ${d.updates.staged.version ?? ''} is ready`);
        for (const a of d.apps)
            if (a.state === 'failed') items.push(`${a.name} is not in operation`);
        for (const db of d.databases)
            if (db.state === 'failed') items.push(`${db.name} failed`);
        for (const ap of d.approvals)
            items.push(`${ap.agent} asks: ${ap.summary}`);
        this._attentionLed.style_class = `nida-led ${items.length ? 'warn' : ''}`;
        this._attentionText.text = items.length === 0 ? 'All parts are in operation'
            : items.length === 1 ? items[0] : `${items.length} items need attention`;
        const sys = d.state?.system;
        if (sys)
            this._host.text = `${sys.hostname}.local`;

        // The system card.
        this._sysVersion.text = d.updates?.booted?.version ?? sys?.os?.image_version ?? '–';
        this._sysSub.text = sys ? `On for ${Math.floor(sys.uptime_seconds / 86400)} days · ${sys.cpu.model}` : '';
        this._sysUpdate.destroy_all_children();
        if (d.updates?.staged) {
            const b = new St.Button({label: `Install ${d.updates.staged.version ?? 'the update'} and restart`, style_class: 'nida-link', x_align: Clutter.ActorAlign.START});
            b.connect('clicked', () => new PowerDialog(this).open());
            this._sysUpdate.add_child(b);
        } else if (d.updates?.available) {
            this._sysUpdate.add_child(label(`Update ${d.updates.available.version ?? ''} is available`, 'nida-sub'));
        }
        if (d.updates?.rollback)
            this._sysUpdate.add_child(label(`Version ${d.updates.rollback.version ?? ''} is kept for a rollback`, 'nida-sub'));

        // The storage tiers.
        this._tiers.destroy_all_children();
        for (const t of d.state?.tiers ?? []) {
            const box = new St.BoxLayout({orientation: Clutter.Orientation.VERTICAL, style_class: 'nida-tier', x_expand: true});
            const head = new St.BoxLayout();
            head.add_child(label(t.name === 'performance' ? 'Performance' : 'Standard', 'nida-tier-name'));
            const used = t.usage ? `${bytes(t.usage.used_bytes, 0)} / ${bytes(t.usage.total_bytes, 0)}` : t.state.replace('_', ' ');
            const u = label(used, 'nida-cell-mono');
            u.x_expand = true;
            u.x_align = Clutter.ActorAlign.END;
            head.add_child(u);
            box.add_child(head);
            // A bar of a fixed width: a size that follows the width of the
            // card can make an endless layout loop.
            const BAR = 360;
            const bar = new St.BoxLayout({style_class: 'nida-bar', width: BAR});
            const pct = t.usage && t.usage.total_bytes ? t.usage.used_bytes / t.usage.total_bytes : 0;
            const fill = new St.Widget({style_class: 'nida-bar-fill', style: `background-color: ${TIER_COLOR[t.name]};`, width: Math.max(2, Math.round(BAR * pct))});
            bar.add_child(fill);
            box.add_child(bar);
            const disk = d.state.disks?.find(x => x.name === t.disk);
            box.add_child(label(disk ? `${disk.model || disk.name} · ${disk.kind.toUpperCase()}` : 'no disk', 'nida-sub'));
            const dbs = d.databases.filter(x => x.tier === t.name).length;
            const folders = d.apps.flatMap(a => a.folders ?? []).filter(f => f.tier === t.name).length;
            box.add_child(label(`Contains ${dbs} databases, ${folders} app folders${t.name === 'standard' ? ', backups' : ''}`, 'nida-sub'));
            this._tiers.add_child(box);
        }

        // The workloads.
        this._workRows.destroy_all_children();
        this._work._note.text = `${this.workloadCount()} in operation`;
        const rows = [
            ...d.apps.map(a => ({name: a.name, kind: 'app', ns: `nida-app-${a.name}`, tier: a.folders?.[0]?.tier ?? '–',
                address: a.address?.replace('https://', '') ?? 'no port', led: a.state === 'running' ? '' : a.state === 'failed' ? 'bad' : 'warn'})),
            ...d.databases.map(x => ({name: x.name, kind: `PostgreSQL ${x.version}`, ns: `nida-db-${x.name}`, tier: x.tier,
                address: 'internal', led: x.state === 'ready' ? '' : x.state === 'failed' ? 'bad' : 'warn'})),
        ];
        if (rows.length === 0)
            this._workRows.add_child(label('No apps and no databases yet. Add them in the web console.', 'nida-sub'));
        for (const r of rows) {
            const row = new St.BoxLayout({style_class: 'nida-table-row'});
            row.add_child(led(r.led));
            const name = label(r.name, 'nida-tier-name');
            name.width = 180;
            row.add_child(name);
            const kind = label(r.kind, 'nida-kind');
            row.add_child(kind);
            const tier = label(r.tier, 'nida-sub');
            tier.width = 110;
            row.add_child(tier);
            const addr = label(r.address, 'nida-cell-mono');
            addr.x_expand = true;
            row.add_child(addr);
            const u = this._usage?.get(r.ns);
            row.add_child(label(u ? `${u.cpu.toFixed(0)}% · ${bytes(u.mem)}` : '–', 'nida-cell-mono'));
            this._workRows.add_child(row);
        }

        // The connections and the events.
        this._connRows.destroy_all_children();
        const conn = (kind, title, value) => {
            const row = new St.BoxLayout({style_class: 'nida-table-row'});
            row.add_child(led(kind));
            const t = label(title);
            t.x_expand = true;
            row.add_child(t);
            row.add_child(label(value, 'nida-cell-mono'));
            this._connRows.add_child(row);
        };
        const gh = d.runners?.github;
        conn(gh?.configured ? '' : 'off', 'GitHub runners', gh?.configured ? `${d.runners.connections.length} repositories · ${d.runners.active.length} jobs` : 'not set');
        conn(d.approvals.length ? 'warn' : '', 'Agent access', d.approvals.length ? `${d.approvals.length} requests wait` : 'no request');
        conn('off', 'Internet gateway', 'comes with M-10');

        this._eventRows.destroy_all_children();
        const events = [];
        for (const a of d.apps) {
            for (const v of a.versions ?? [])
                events.push({t: v.created, text: `Deploy of ${a.name}, version ${v.number}: ${v.state}`});
        }
        events.sort((x, y) => Date.parse(y.t) - Date.parse(x.t));
        for (const e of events.slice(0, 5)) {
            const row = new St.BoxLayout({style_class: 'nida-table-row'});
            row.add_child(label(clock(e.t), 'nida-cell-mono'));
            row.add_child(label(e.text));
            this._eventRows.add_child(row);
        }
        if (events.length === 0)
            this._eventRows.add_child(label('No events yet.', 'nida-sub'));
    }

    destroy() {
        for (const id of this._timers)
            GLib.source_remove(id);
        for (const stop of this._stops)
            stop();
        this.api.destroy();
        for (const a of [this._top, this._dock]) {
            if (a) {
                Main.layoutManager.removeChrome(a);
                a.destroy();
            }
        }
        this._desk?.destroy();
    }
}

export default class NidaDesktopExtension extends Extension {
    enable() {
        // The standard parts of GNOME go away (task T-02): the dock of
        // NidaOS replaces the top panel and the activities.
        Main.panel.hide();
        this._desktop = new Desktop(this);
        this._desktop.build();
        this._desktop.start();
        console.log('NidaOS desktop: enabled');
    }

    disable() {
        this._desktop?.destroy();
        this._desktop = null;
        Main.panel.show();
    }
}
