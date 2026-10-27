#!/bin/sh
set -eu

# 浏览器、MCP 和网页查看器共用一个显示会话。
Xvfb :99 -screen 0 1360x900x24 -nolisten tcp &
display_pid=$!
cleanup() {
    kill "$display_pid" ${window_pid:-} ${vnc_pid:-} ${web_pid:-} ${demo_pid:-} ${mcp_pid:-} 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 0' TERM INT
attempt=0
until [ -S /tmp/.X11-unix/X99 ]; do
    attempt=$((attempt + 1))
    [ "$attempt" -lt 50 ] || exit 1
    sleep 0.1
done
fluxbox >/dev/null 2>&1 &
window_pid=$!
x11vnc -display :99 -localhost -nopw -forever -shared -rfbport 5900 -quiet >/dev/null 2>&1 &
vnc_pid=$!
websockify --web /usr/share/novnc 6080 127.0.0.1:5900 >/dev/null 2>&1 &
web_pid=$!
busybox httpd -f -p 127.0.0.1:8000 -h /opt/browser/demo &
demo_pid=$!
node /app/cli.js --browser chromium --no-sandbox \
    --host 0.0.0.0 --port 8931 \
    --allowed-hosts browser:8931,localhost:8931,127.0.0.1:8931 \
    --shared-browser-context --no-webmcp \
    --proxy-server http://egress:3128 --proxy-bypass 127.0.0.1 \
    --user-data-dir /data/profile --output-dir /data/artifacts &
mcp_pid=$!
wait "$mcp_pid"
