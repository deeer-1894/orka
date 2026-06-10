#!/usr/bin/env bash
# Boot the virtual desktop + VNC stack, then the GUI agent. Playwright launches
# headful Chromium on :99, x11vnc exports it, noVNC serves a web viewer on 6080.
set -e

# A container *restart* keeps /tmp, and Xvfb refuses to start over a stale lock
# from the previous run — clean it or the whole desktop stack dies.
rm -f /tmp/.X99-lock /tmp/.X11-unix/X99

Xvfb :99 -screen 0 1280x800x24 -ac +extension RANDR +extension GLX >/tmp/xvfb.log 2>&1 &

# Wait until the display socket exists and Xvfb is still alive (not a blind
# sleep; xdpyinfo isn't in the slim image).
XVFB_PID=$!
for i in $(seq 1 30); do
  if [ -S /tmp/.X11-unix/X99 ] && kill -0 "$XVFB_PID" 2>/dev/null; then break; fi
  sleep 0.5
done

fluxbox >/tmp/fluxbox.log 2>&1 &
x11vnc -display :99 -nopw -forever -shared -rfbport 5900 -quiet >/tmp/x11vnc.log 2>&1 &
websockify --web=/usr/share/novnc 6080 localhost:5900 >/tmp/novnc.log 2>&1 &

exec python main.py
