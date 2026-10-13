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
Xvfb :99 -screen 0 1280x900x24 &
XVFB_PID=$!
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
