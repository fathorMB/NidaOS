#!/usr/bin/env bash
# Load the NidaOS extension in a headless GNOME Shell of the owner, and take
# a screenshot (milestone M-08). The VM test copies this script into the
# virtual machine and runs it as the owner, without sudo.
#
# The script starts the shell with all the extensions off, and makes sure
# that the shell answers. Then it sets the extensions on, and examines the
# NidaOS extension. The times of the queries show if the extension blocks
# the shell.
#
# Output: the state of the extension, the times of the queries, the load of
# the processor, the errors of the shell, and the screenshot
# /tmp/nida-desktop.png.

set -u
# Fedora has dbus-broker and no dbus-run-session: use the session bus of
# the owner.
export DBUS_SESSION_BUS_ADDRESS="unix:path=/run/user/$(id -u)/bus"
export XDG_RUNTIME_DIR="/run/user/$(id -u)"
ID=nidaos@nidaos.local

# info prints the state of the extension and the time of the query.
info() {
    local start end
    start=$(date +%s%N)
    timeout 20 gnome-extensions info "$ID" 2>&1 | grep -E 'State|Error' || echo "no answer"
    end=$(date +%s%N)
    echo "query time: $(((end - start) / 1000000)) ms"
}

report() {
    echo "## the load of the processor"
    top -b -n 1 | head -n 15
    echo "## the errors of GNOME Shell"
    grep -i -B2 -A8 -e nidaos -e 'JS ERROR' -e 'error' /tmp/nida-shell.log | head -n 80
    echo "## the end of the log of GNOME Shell"
    tail -n 30 /tmp/nida-shell.log
}

gsettings set org.gnome.shell disable-user-extensions true
gnome-shell --headless --virtual-monitor 1280x800 --unsafe-mode > /tmp/nida-shell.log 2>&1 &
shell=$!
trap 'kill "$shell" 2> /dev/null; gsettings reset org.gnome.shell disable-user-extensions' EXIT

# Wait until the shell owns its name on the bus (at most 2 minutes).
for _ in $(seq 1 40); do
    sleep 3
    timeout 5 gdbus call --session --dest org.freedesktop.DBus --object-path /org/freedesktop/DBus \
        --method org.freedesktop.DBus.NameHasOwner org.gnome.Shell 2> /dev/null | grep -q true && break
done
echo "## the shell without extensions"
info

gsettings set org.gnome.shell disable-user-extensions false
echo "## the shell with the extensions"
for _ in $(seq 1 8); do
    sleep 5
    out=$(info)
    echo "$out"
    grep -Eq 'State: (ACTIVE|ENABLED)' <<< "$out" && break
done
# The extension reads nida-core: wait for the first data, then take the
# screenshot.
sleep 10
info
timeout 30 gdbus call --session --dest org.gnome.Shell.Screenshot --object-path /org/gnome/Shell/Screenshot \
    --method org.gnome.Shell.Screenshot.Screenshot false false /tmp/nida-desktop.png || true
report
