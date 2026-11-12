#!/usr/bin/env -S gjs -m
// The file program of the NidaOS desktop (milestone M-08, task T-05,
// decision D14: the files are only on the desktop, not in a browser). It
// shows the storage tiers, the home folder and the backups, with the app
// or the database that uses each folder, as the mockup design/files.
//
//   gjs -m nida-files.js            the program
//   gjs -m nida-files.js --check    print the places and the users of the folders (the VM test)

import Adw from 'gi://Adw?version=1';
import Gio from 'gi://Gio';
import GLib from 'gi://GLib';
import Gtk from 'gi://Gtk?version=4.0';
import system from 'system';

import {NidaAPI, bytes} from './api.js';

const ATTRS = 'standard::name,standard::type,standard::size,time::modified,access::can-read';

// usedBy gives the app or the database of a folder of a storage tier. The
// local-path provisioner of k3s names a volume folder
// "<volume>_<namespace>_<claim>" (image/rootfs/usr/share/nidaos/k3s).
export function usedBy(name) {
    const parts = name.split('_');
    if (parts.length < 3 || !parts[0].startsWith('pvc-'))
        return '';
    const ns = parts[1];
    if (ns.startsWith('nida-app-'))
        return `app ${ns.slice(9)}`;
    if (ns.startsWith('nida-db-'))
        return `database ${ns.slice(8)}`;
    if (ns === 'nida-system' || ns === 'kube-system')
        return `system (${parts.slice(2).join('_')})`;
    return `namespace ${ns}`;
}

function places() {
    const home = GLib.get_home_dir();
    return [
        {head: 'Storage tiers'},
        {id: 'performance', label: 'Performance', path: '/var/nida/performance', tier: 'performance'},
        {id: 'standard', label: 'Standard', path: '/var/nida/standard', tier: 'standard'},
        {head: 'Shortcuts'},
        {id: 'home', label: `Home of ${GLib.get_user_name()}`, path: home},
        {id: 'backups', label: 'Backups', path: '/var/nida/standard/backups'},
    ];
}

function list(path) {
    const dir = Gio.File.new_for_path(path);
    const out = [];
    const en = dir.enumerate_children(ATTRS, Gio.FileQueryInfoFlags.NONE, null);
    let info;
    while ((info = en.next_file(null))) {
        out.push({
            name: info.get_name(),
            dir: info.get_file_type() === Gio.FileType.DIRECTORY,
            size: info.get_size(),
            changed: info.get_modification_date_time()?.to_local(),
        });
    }
    en.close(null);
    out.sort((a, b) => (b.dir - a.dir) || a.name.localeCompare(b.name));
    return out;
}

async function check() {
    const api = new NidaAPI();
    const tiers = await api.get('/api/v1/tiers');
    for (const t of tiers.tiers)
        print(`nida-files: tier ${t.name} ${t.state} ${t.usage ? bytes(t.usage.free_bytes) + ' free' : ''}`);
    let users = 0;
    for (const p of places().filter(x => x.tier)) {
        try {
            const vols = list(`${p.path}/volumes`);
            for (const v of vols) {
                const u = usedBy(v.name);
                if (u) {
                    users++;
                    print(`nida-files: ${p.tier}/volumes/${v.name} is used by ${u}`);
                }
            }
        } catch (e) {
            print(`nida-files: ${p.path}: ${e.message}`);
        }
    }
    return tiers.tiers.length > 0 && users > 0 ? 0 : 1;
}

class FilesWindow {
    constructor(app) {
        this.api = new NidaAPI();
        this.place = places().find(p => p.id === 'standard');
        this.path = this.place.path;
        this.win = new Adw.ApplicationWindow({application: app, title: 'NidaOS files', default_width: 1200, default_height: 760});
        const view = new Adw.ToolbarView();
        const header = new Adw.HeaderBar();
        this.title = new Adw.WindowTitle({title: 'Files'});
        header.set_title_widget(this.title);
        const up = new Gtk.Button({icon_name: 'go-up-symbolic', tooltip_text: 'Go to the parent folder'});
        up.connect('clicked', () => this.open(GLib.path_get_dirname(this.path)));
        header.pack_start(up);
        this.search = new Gtk.SearchEntry({placeholder_text: 'Find in this folder'});
        this.search.connect('search-changed', () => this.draw());
        header.pack_start(this.search);
        const upload = new Gtk.Button({label: 'Upload', css_classes: ['suggested-action']});
        upload.connect('clicked', () => this.upload());
        header.pack_end(upload);
        const mkdir = new Gtk.Button({label: 'New folder'});
        mkdir.connect('clicked', () => this.newFolder());
        header.pack_end(mkdir);
        view.add_top_bar(header);

        const paned = new Gtk.Paned({position: 240});
        const side = new Gtk.ListBox({css_classes: ['navigation-sidebar']});
        for (const p of places()) {
            if (p.head) {
                side.append(new Gtk.ListBoxRow({selectable: false, activatable: false,
                    child: new Gtk.Label({label: p.head.toUpperCase(), xalign: 0, margin_start: 8, margin_top: 10, css_classes: ['caption-heading', 'dim-label']})}));
                continue;
            }
            const row = new Gtk.ListBoxRow({child: new Gtk.Label({label: p.label, xalign: 0, margin_start: 8, margin_top: 4, margin_bottom: 4})});
            row._place = p;
            side.append(row);
        }
        side.connect('row-activated', (_l, row) => {
            if (row._place) {
                this.place = row._place;
                this.open(row._place.path);
            }
        });
        paned.set_start_child(new Gtk.ScrolledWindow({child: side}));
        const center = new Gtk.Box({orientation: Gtk.Orientation.VERTICAL});
        this.list = new Gtk.ListBox({css_classes: ['boxed-list'], margin_start: 12, margin_end: 12, margin_top: 12, margin_bottom: 12});
        this.list.connect('row-activated', (_l, row) => {
            if (row._item?.dir)
                this.open(GLib.build_filenamev([this.path, row._item.name]));
        });
        center.append(new Gtk.ScrolledWindow({child: this.list, vexpand: true}));
        this.status = new Gtk.Label({xalign: 0, margin_start: 12, margin_bottom: 6, css_classes: ['dim-label']});
        center.append(this.status);
        paned.set_end_child(center);
        view.set_content(paned);
        this.win.set_content(view);
        this.open(this.path);
    }

    open(path) {
        this.path = path;
        this.title.subtitle = path;
        try {
            this.items = list(path);
            this.error = null;
        } catch (e) {
            this.items = [];
            this.error = e.matches?.(Gio.IOErrorEnum, Gio.IOErrorEnum.PERMISSION_DENIED)
                ? 'Only root and the app can read this folder. Use the terminal with sudo.'
                : e.message;
        }
        this.draw();
    }

    draw() {
        let child;
        while ((child = this.list.get_first_child()))
            this.list.remove(child);
        const q = this.search.text.trim().toLowerCase();
        const items = this.items.filter(x => x.name.toLowerCase().includes(q));
        for (const it of items) {
            const row = new Adw.ActionRow({title: it.name, activatable: it.dir,
                subtitle: [usedBy(it.name) && `used by ${usedBy(it.name)}`, it.changed?.format('%e %b %H:%M')].filter(Boolean).join(' · ')});
            row.add_prefix(new Gtk.Image({icon_name: it.dir ? 'folder-symbolic' : 'text-x-generic-symbolic'}));
            row.add_suffix(new Gtk.Label({label: it.dir ? '' : bytes(it.size), css_classes: ['monospace', 'dim-label']}));
            row._item = it;
            this.list.append(row);
        }
        if (this.error)
            this.list.append(new Adw.ActionRow({title: this.error}));
        else if (items.length === 0)
            this.list.append(new Adw.ActionRow({title: q ? 'No item agrees with the search.' : 'The folder is empty.'}));
        this.status.label = `${items.length} items · ${this.place.tier ? `${this.place.label} tier` : this.place.label}`;
        if (this.place.tier) {
            this.api.get('/api/v1/tiers').then(t => {
                const x = t.tiers.find(y => y.name === this.place.tier);
                if (x?.usage)
                    this.status.label = `${items.length} items · ${this.place.label}: ${bytes(x.usage.free_bytes)} free of ${bytes(x.usage.total_bytes)}`;
            }).catch(() => {});
        }
    }

    newFolder() {
        const entry = new Gtk.Entry({placeholder_text: 'Name of the folder', activates_default: true});
        const dialog = new Adw.AlertDialog({heading: 'New folder', extra_child: entry});
        dialog.add_response('cancel', 'Cancel');
        dialog.add_response('make', 'Make');
        dialog.set_default_response('make');
        dialog.connect('response', (_d, r) => {
            const name = entry.text.trim();
            if (r !== 'make' || !name || name.includes('/'))
                return;
            try {
                Gio.File.new_for_path(GLib.build_filenamev([this.path, name])).make_directory(null);
            } catch (e) {
                this.status.label = `The folder was not made: ${e.message}`;
            }
            this.open(this.path);
        });
        dialog.present(this.win);
    }

    upload() {
        const dialog = new Gtk.FileDialog({title: 'Upload into this folder'});
        dialog.open(this.win, null, (d, res) => {
            try {
                const src = d.open_finish(res);
                const dst = Gio.File.new_for_path(GLib.build_filenamev([this.path, src.get_basename()]));
                src.copy(dst, Gio.FileCopyFlags.NONE, null, null);
                this.open(this.path);
            } catch (e) {
                if (!e.matches?.(Gtk.DialogError, Gtk.DialogError.DISMISSED))
                    this.status.label = `The upload failed: ${e.message}`;
            }
        });
    }
}

if (ARGV.includes('--check')) {
    const loop = new GLib.MainLoop(null, false);
    let code = 1;
    check().then(c => {
        code = c;
    }, e => printerr(`nida-files: ${e.message}`)).finally(() => loop.quit());
    loop.run();
    system.exit(code);
} else {
    const app = new Adw.Application({application_id: 'local.nidaos.Files', flags: Gio.ApplicationFlags.DEFAULT_FLAGS});
    app.connect('activate', () => new FilesWindow(app).win.present());
    app.run([system.programInvocationName, ...ARGV]);
}
