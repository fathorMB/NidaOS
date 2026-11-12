#!/usr/bin/env bash
# Load the NidaOS extension in a headless GNOME Shell of the owner, and take
# a screenshot (milestone M-08). The VM test copies this script into the
# virtual machine and runs it as the owner, without sudo.
#
# The extension writes lines in the log of the shell when it is on and when
# the first data from nida-core is on the screen. The script waits for these
# lines: gnome-extensions asks a service that the shell does not give.
#
# Output: the lines of the extension, the names on the bus, the errors of
# the shell, and the screenshot /tmp/nida-desktop.png (when the shell
# answers).

set -u
# Fedora has dbus-broker and no dbus-run-session: use the session bus of
# the owner.
export DBUS_SESSION_BUS_ADDRESS="unix:path=/run/user/$(id -u)/bus"
export XDG_RUNTIME_DIR="/run/user/$(id -u)"
LOG=/tmp/nida-shell.log

# The shell runs as a service of the user manager, out of the SSH session:
# in the SSH session, mutter monitors a session that is not active, and the
# shell stops after the start.
: > "$LOG"
systemd-run --user --quiet --collect --unit nida-shell-check \
    -p StandardOutput="append:$LOG" -p StandardError="append:$LOG" \
    gnome-shell --headless --virtual-monitor 1280x800 --unsafe-mode
trap 'systemctl --user stop nida-shell-check.service 2> /dev/null' EXIT
running() { systemctl --user is-active --quiet nida-shell-check.service; }

# Wait for the first data on the screen (at most 3 minutes).
for _ in $(seq 1 60); do
    sleep 3
    grep -q 'NidaOS desktop: the first data' "$LOG" && break
    running || break
done
sleep 5
echo "## the lines of the extension"
grep 'NidaOS desktop' "$LOG" || echo "no line"
if running; then
    echo "shell: running"
else
    echo "shell: stopped"
fi
echo "## the GNOME names on the bus"
timeout 10 busctl --user list --no-pager 2> /dev/null | grep -i -e gnome.Shell -e Mutter | cut -c1-120
timeout 30 gdbus call --session --dest org.gnome.Shell.Screenshot --object-path /org/gnome/Shell/Screenshot \
    --method org.gnome.Shell.Screenshot.Screenshot false false /tmp/nida-desktop.png || true
echo "## the errors of GNOME Shell"
grep -i -B2 -A8 -e nidaos -e 'JS ERROR' -e 'JS WARNING' "$LOG" | grep -v 'NidaOS desktop:' | head -n 80
