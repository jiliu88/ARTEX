#!/bin/sh
# 镜像构建时封住预装浏览器的直接启动入口，受管实例由服务端使用原始二进制启动。
set -eu
for root in /opt/google/chrome /root/.cache/ms-playwright /ms-playwright; do
    [ -d "$root" ] || continue
    find "$root" -type f \( -name chrome -o -name headless_shell -o -name chrome-headless-shell \) | while IFS= read -r browser; do
        [ -x "$browser" ] || continue
        if [ ! -f "$browser.artex-real" ]; then
            mv "$browser" "$browser.artex-real"
        fi
        cat > "$browser" <<'WRAPPER'
#!/bin/sh
echo 'ARTEX：禁止独立启动 Chrome。请调用 task_browser 获取当前主任务唯一实例的 CDP 地址，并使用 connect_over_cdp / connectOverCDP。' >&2
exit 126
WRAPPER
        chmod 755 "$browser"
    done
done
