#!/usr/bin/env -S gjs -m
// The log program of the NidaOS desktop (milestone M-08, task T-06). It
// shows the live stream of nida-core with the sources, the levels, the
// search and the follow switch, as the mockup design/logs. Older lines
// come from the log store (GET /api/v1/logs/search).
//
//   gjs -m nida-logs.js            the program
//   gjs -m nida-logs.js --check    read the API and print a summary (the VM test)

import Adw from 'gi://Adw?version=1';
import Gio from 'gi://Gio';
import GLib from 'gi://GLib';
import Gtk from 'gi://Gtk?version=4.0';
import system from 'system';

import {NidaAPI, clockSeconds, sourceName} from './api.js';

const MAX_LINES = 2000;
const SHOW = 500;
const LEVELS = ['debug', 'info', 'warning', 'error'];

async function check() {
    const api = new NidaAPI();
    const page = await api.get('/api/v1/logs?tail=20');
    const sources = new Set(page.lines.map(l => l.source.id.split('/').slice(0, 2).join('/')));
    print(`nida-logs: ${page.lines.length} lines from ${sources.size} sources`);
    let events = 0;
    await new Promise(resolve => {
        const stop = api.stream('/api/v1/logs/stream?tail=5', ev => {
            if (ev === 'log' && ++events >= 1) {
                stop();
                resolve();
            }
        });
        GLib.timeout_add_seconds(GLib.PRIORITY_DEFAULT, 20, () => {
            stop();
            resolve();
            return GLib.SOURCE_REMOVE;
        });
    });
    print(`nida-logs: the live stream gave ${events} lines`);
    return page.lines.length > 0 && events > 0 ? 0 : 1;
}

class LogsWindow {
    constructor(app) {
        this.api = new NidaAPI();
        this.lines = [];
        this.source = '';
        this.levels = new Set(['info', 'warning', 'error']);
        this.query = '';
        this.follow = true;
        this.sources = new Map();

        this.win = new Adw.ApplicationWindow({application: app, title: 'NidaOS logs', default_width: 1200, default_height: 760});
        const view = new Adw.ToolbarView();
        const header = new Adw.HeaderBar({title_widget: new Adw.WindowTitle({title: 'Logs', subtitle: GLib.get_host_name()})});
        this.search = new Gtk.SearchEntry({placeholder_text: 'Find text in the messages', width_chars: 30});
        this.search.connect('search-changed', () => {
            this.query = this.search.text.trim().toLowerCase();
            this.draw();
        });
        header.pack_start(this.search);
        for (const [lvl, text] of [['error', 'Errors'], ['warning', 'Warnings'], ['info', 'Information']]) {
            const t = new Gtk.ToggleButton({label: text, active: true});
            t.connect('toggled', () => {
                if (t.active)
                    this.levels.add(lvl);
                else
                    this.levels.delete(lvl);
                this.draw();
            });
            header.pack_start(t);
        }
        const followBtn = new Gtk.ToggleButton({label: 'Follow', active: true});
        followBtn.connect('toggled', () => {
            this.follow = followBtn.active;
            if (this.follow)
                this.draw();
        });
        header.pack_end(followBtn);
        const save = new Gtk.Button({label: 'Save to a file'});
        save.connect('clicked', () => this.save());
        header.pack_end(save);
        this.range = Gtk.DropDown.new_from_strings(['Live', 'Last hour', 'Today', 'Last 7 days']);
        this.range.connect('notify::selected', () => this.loadRange());
        header.pack_end(this.range);
        view.add_top_bar(header);

        const paned = new Gtk.Paned({orientation: Gtk.Orientation.HORIZONTAL, position: 240});
        this.side = new Gtk.ListBox({css_classes: ['navigation-sidebar']});
        this.side.connect('row-selected', (_l, row) => {
            this.source = row?._source ?? '';
            this.draw();
        });
        paned.set_start_child(new Gtk.ScrolledWindow({child: this.side}));
        this.list = new Gtk.ListBox({selection_mode: Gtk.SelectionMode.NONE});
        this.scroll = new Gtk.ScrolledWindow({child: this.list, vexpand: true});
        const right = new Gtk.Box({orientation: Gtk.Orientation.VERTICAL});
        right.append(this.scroll);
        this.status = new Gtk.Label({xalign: 0, margin_start: 12, margin_end: 12, margin_top: 6, margin_bottom: 6, css_classes: ['dim-label']});
        right.append(this.status);
        paned.set_end_child(right);
        view.set_content(paned);
        this.win.set_content(view);
        this.addSource('', 'All sources');
        this.state = 'connecting';
        this.stop = this.api.stream('/api/v1/logs/stream?tail=300', (ev, data) => {
            if (ev !== 'log')
                return;
            this.lines.push(data);
            if (this.lines.length > MAX_LINES)
                this.lines = this.lines.slice(-MAX_LINES);
            this.noteSource(data);
            this.lastData = Date.now();
            this.dirty = true;
        }, s => {
            this.state = s;
            this.drawStatus();
        });
        this.timer = GLib.timeout_add(GLib.PRIORITY_DEFAULT, 500, () => {
            if (this.dirty && this.follow && this.range.selected === 0) {
                this.dirty = false;
                this.draw();
            }
            this.drawStatus();
            return GLib.SOURCE_CONTINUE;
        });
        this.win.connect('close-request', () => {
            this.stop();
            GLib.source_remove(this.timer);
            this.api.destroy();
            return false;
        });
    }

    addSource(id, text) {
        const row = new Gtk.ListBoxRow({child: new Gtk.Label({label: text, xalign: 0, margin_start: 8, margin_top: 4, margin_bottom: 4})});
        row._source = id;
        this.side.append(row);
        this.sources.set(id, row);
        if (id === '')
            this.side.select_row(row);
    }

    noteSource(l) {
        const ns = l.source.namespace;
        const id = l.source.kind === 'pod' && ns ? `pod/${ns}` : l.source.id;
        if (this.sources.has(id))
            return;
        const name = l.source.kind === 'pod' ? (ns.startsWith('nida-app-') ? `app ${sourceName(l)}`
            : ns.startsWith('nida-db-') ? `database ${sourceName(l)}` : `namespace ${ns}`) : `service ${l.source.name}`;
        this.addSource(id, name);
    }

    match(l) {
        if (this.source && !l.source.id.startsWith(this.source))
            return false;
        if (!this.levels.has(l.level) && !(l.level === 'debug' && this.levels.has('info')))
            return false;
        return !this.query || l.message.toLowerCase().includes(this.query);
    }

    visible() {
        const out = [];
        for (let i = this.shown.length - 1; i >= 0 && out.length < SHOW; i--) {
            if (this.match(this.shown[i]))
                out.push(this.shown[i]);
        }
        return out;
    }

    get shown() {
        return this.range?.selected > 0 ? (this.old ?? []) : this.lines;
    }

    draw() {
        let child;
        while ((child = this.list.get_first_child()))
            this.list.remove(child);
        for (const l of this.visible()) {
            const box = new Gtk.Box({spacing: 12, margin_start: 10, margin_end: 10, margin_top: 2, margin_bottom: 2});
            box.append(new Gtk.Label({label: clockSeconds(l.time), css_classes: ['monospace', 'dim-label']}));
            box.append(new Gtk.Label({label: sourceName(l), width_chars: 16, max_width_chars: 16, xalign: 0, ellipsize: 3, css_classes: ['monospace', 'dim-label']}));
            const msg = new Gtk.Label({label: l.message, xalign: 0, hexpand: true, wrap: true, selectable: true, css_classes: ['monospace']});
            if (l.level === 'error')
                msg.add_css_class('error');
            else if (l.level === 'warning')
                msg.add_css_class('warning');
            box.append(msg);
            this.list.append(box);
        }
        this.drawStatus();
    }

    drawStatus() {
        const live = this.range.selected === 0;
        const state = !live ? 'from the log store' : this.state === 'live' ? (this.follow ? 'live' : 'paused')
            : `the live stream stopped · last data ${this.lastData ? clockSeconds(this.lastData) : 'none'}`;
        this.status.label = `${this.visible().length} lines · ${state} · the system keeps the logs for 30 days`;
    }

    async loadRange() {
        const since = [null, '1h', '24h', '7d'][this.range.selected];
        if (!since) {
            this.draw();
            return;
        }
        const q = [`since=${since}`, 'limit=1000'];
        if (this.source)
            q.push(`source=${encodeURIComponent(this.source)}`);
        if (this.query)
            q.push(`q=${encodeURIComponent(this.query)}`);
        try {
            const page = await this.api.get(`/api/v1/logs/search?${q.join('&')}`);
            this.old = page.lines.reverse();
        } catch (e) {
            this.old = [];
            this.status.label = `The search failed: ${e.message}`;
        }
        this.draw();
    }

    save() {
        const dialog = new Gtk.FileDialog({initial_name: `nidaos-logs-${GLib.DateTime.new_now_local().format('%Y-%m-%d-%H%M')}.log`});
        dialog.save(this.win, null, (d, res) => {
            try {
                const file = d.save_finish(res);
                const text = this.visible().reverse().map(l => `${l.time}\t${l.level}\t${l.source.id}\t${l.message}`).join('\n') + '\n';
                file.replace_contents(new TextEncoder().encode(text), null, false, Gio.FileCreateFlags.REPLACE_DESTINATION, null);
            } catch (e) {
                if (!e.matches?.(Gtk.DialogError, Gtk.DialogError.DISMISSED))
                    logError(e, 'save the logs');
            }
        });
    }
}

if (ARGV.includes('--check')) {
    const loop = new GLib.MainLoop(null, false);
    let code = 1;
    check().then(c => {
        code = c;
    }, e => {
        printerr(`nida-logs: ${e.message}`);
    }).finally(() => loop.quit());
    loop.run();
    system.exit(code);
} else {
    const app = new Adw.Application({application_id: 'local.nidaos.Logs', flags: Gio.ApplicationFlags.DEFAULT_FLAGS});
    app.connect('activate', () => new LogsWindow(app).win.present());
    app.run([system.programInvocationName, ...ARGV]);
}
