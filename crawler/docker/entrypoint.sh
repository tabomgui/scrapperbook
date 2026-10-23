#!/bin/sh
set -e

# Xvfb gives Chromium a virtual display; x11vnc exposes it over VNC;
# websockify bridges that to the browser-based noVNC client on :6080.
# Needed even in "worker" mode because Playwright still launches a real
# (non-headless-in-the-Chromium-sense, but display-less-without-Xvfb) browser.
#
# set -e doesn't catch any of these dying: a backgrounded command returns
# success the instant the fork succeeds, regardless of what the child does
# afterward. Without an explicit check, a dead Xvfb surfaces later as an
# opaque X11 connection error inside Playwright, misclassified by the
# worker loop as a generic NETWORK failure — check each process is still
# running after it's had a moment to fail, so a startup problem is loud
# and specific instead of silent.
# A stale lock from a prior unclean shutdown (container killed mid-run,
# e.g. by an OOM or a host reboot) makes Xvfb refuse to start with
# "Server is already active for display 99" even though nothing is
# actually using it — in-place restarts would then fail forever until
# someone recreated the container from scratch. Nothing else should hold
# this lock at entrypoint time, so it's always safe to clear here.
rm -f /tmp/.X99-lock

Xvfb :99 -screen 0 1280x900x24 &
XVFB_PID=$!

# x11vnc connects to :99 the instant it starts, racing Xvfb's own startup —
# on a loaded host Xvfb can take longer than one tick to create its socket,
# so x11vnc dies before Xvfb is actually ready. Poll for the socket instead
# of guessing a fixed delay (see superpowers:condition-based-waiting).
for _ in $(seq 1 50); do
    [ -S "/tmp/.X11-unix/X99" ] && break
    kill -0 "$XVFB_PID" 2>/dev/null || { echo "entrypoint: Xvfb (pid $XVFB_PID) died before it was ready" >&2; exit 1; }
    sleep 0.1
done
if [ ! -S "/tmp/.X11-unix/X99" ]; then
    echo "entrypoint: Xvfb socket never appeared after 5s" >&2
    exit 1
fi

x11vnc -display :99 -nopw -listen 0.0.0.0 -xkb -forever -quiet &
X11VNC_PID=$!
websockify --web /usr/share/novnc 6080 localhost:5900 &
WEBSOCKIFY_PID=$!
sleep 1

for name_pid in "Xvfb:$XVFB_PID" "x11vnc:$X11VNC_PID" "websockify:$WEBSOCKIFY_PID"; do
    name="${name_pid%%:*}"
    pid="${name_pid##*:}"
    if ! kill -0 "$pid" 2>/dev/null; then
        echo "entrypoint: $name (pid $pid) failed to start" >&2
        exit 1
    fi
done

if [ "$WORKER_MODE" = "login" ]; then
    exec python scripts/login.py
else
    exec python scripts/run_worker.py
fi
