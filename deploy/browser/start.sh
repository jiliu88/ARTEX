#!/bin/sh
set -eu

# 浏览器、MCP 和网页查看器共用一个显示会话。
Xvfb :99 -screen 0 1360x900x24 -nolisten tcp &
display_pid=$!
cleanup() {
    kill "$display_pid" ${window_pid:-} ${vnc_pid:-} ${web_pid:-} ${demo_pid:-} ${browser_pid:-} ${mcp_pid:-} 2>/dev/null || true
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
# 浏览器独立常驻，MCP 客户端断开后仍保留窗口和当前页面。
browser_bin=$(node -p 'require("/app/node_modules/playwright").chromium.executablePath()')
"$browser_bin" --no-sandbox --disable-dev-shm-usage --disable-gpu \
    --remote-debugging-address=127.0.0.1 --remote-debugging-port=9222 \
    --user-data-dir=/data/profile --no-first-run --no-default-browser-check \
    --disable-background-networking --disable-component-update --disable-sync \
    --proxy-server=http://egress:3128 --proxy-bypass-list=127.0.0.1 \
    --window-size=1280,800 http://127.0.0.1:8000/ >/dev/null 2>&1 &
browser_pid=$!
attempt=0
until node -e "fetch('http://127.0.0.1:9222/json/version').then(r=>process.exit(r.ok?0:1)).catch(()=>process.exit(1))"; do
    attempt=$((attempt + 1))
    [ "$attempt" -lt 50 ] || exit 1
    sleep 0.2
done
node /app/cli.js --cdp-endpoint http://127.0.0.1:9222 \
    --host 0.0.0.0 --port 8931 \
    --allowed-hosts browser:8931,localhost:8931,127.0.0.1:8931 \
    --shared-browser-context --no-webmcp \
    --output-dir /data/artifacts &
mcp_pid=$!
wait "$mcp_pid"
