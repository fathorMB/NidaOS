#!/usr/bin/env bash
# Load the NidaOS extension in a headless GNOME Shell of the owner, and take
# a screenshot (milestone M-08). The VM test copies this script into the
# virtual machine and runs it as the owner, without sudo.
#
# Output: the state of the extension, the errors of the shell, and the
# screenshot /tmp/nida-desktop.png.

set -u
# Fedora has dbus-broker and no dbus-run-session: use the session bus of
# the owner.
export DBUS_SESSION_BUS_ADDRESS="unix:path=/run/user/$(id -u)/bus"
export XDG_RUNTIME_DIR="/run/user/$(id -u)"

gnome-shell --headless --virtual-monitor 1280x800 --unsafe-mode > /tmp/nida-shell.log 2>&1 &
shell=$!
for _ in $(seq 1 40); do
    sleep 3
    timeout 10 gnome-extensions info nidaos@nidaos.local 2> /dev/null | grep -Eq 'State: (ACTIVE|ENABLED)' && break
done
timeout 20 gnome-extensions info nidaos@nidaos.local
# The extension reads nida-core: wait for the first data, then take the
# screenshot.
sleep 10
timeout 20 gdbus call --session --dest org.gnome.Shell.Screenshot --object-path /org/gnome/Shell/Screenshot \
    --method org.gnome.Shell.Screenshot.Screenshot false false /tmp/nida-desktop.png || true
kill "$shell" 2> /dev/null
wait "$shell" 2> /dev/null
echo "## the errors of GNOME Shell"
grep -i -B2 -A8 -e nidaos -e 'JS ERROR' /tmp/nida-shell.log | head -n 80
