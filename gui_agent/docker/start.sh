#!/usr/bin/env bash
# Boot the virtual desktop + VNC stack, then the GUI agent. Playwright launches
# headful Chromium on :99, x11vnc exports it, noVNC serves a web viewer on 6080.
set -e

Xvfb :99 -screen 0 1280x800x24 -ac +extension RANDR +extension GLX >/tmp/xvfb.log 2>&1 &
sleep 1
fluxbox >/tmp/fluxbox.log 2>&1 &
x11vnc -display :99 -nopw -forever -shared -rfbport 5900 -quiet >/tmp/x11vnc.log 2>&1 &
websockify --web=/usr/share/novnc 6080 localhost:5900 >/tmp/novnc.log 2>&1 &

exec python main.py
