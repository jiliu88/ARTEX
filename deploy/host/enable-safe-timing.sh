#!/usr/bin/env bash
set -euo pipefail
umask 077
config=${1:-/etc/nginx/conf.d/artex-https.conf}
backup="${config}.before-loading-$(date -u +%Y-%m-%dT%H%M%SZ)"
cp -p "$config" "$backup"
/usr/bin/python3 - "$config" <<'PY'
from pathlib import Path
import re,sys
p=Path(sys.argv[1]);s=p.read_text()
line="log_format artex_timed '$time_iso8601 $request_id $request_method $uri $status $body_bytes_sent rt=$request_time uct=$upstream_connect_time uht=$upstream_header_time urt=$upstream_response_time us=$upstream_status';"
if 'log_format artex_timed ' not in s:s=line+'\n'+s
s=re.sub(r'(access_log\s+/var/log/nginx/artex-access\.log\s+)\w+;',r'\1artex_timed;',s)
if 'proxy_set_header X-Request-ID ' not in s:
 s=s.replace('    proxy_set_header Host ', '    proxy_set_header X-Request-ID $request_id;\n    proxy_set_header Host ',1)
if '# loading-performance compression' not in s:
 s=s.replace('    listen 8443 ssl http2;', '''    listen 8443 ssl http2;
    # loading-performance compression：SSE 的 text/event-stream 不在压缩类型内。
    gzip on;
    gzip_types application/json application/javascript text/javascript text/css image/svg+xml;
    gzip_min_length 1024;
    gzip_comp_level 4;
    gzip_vary on;''',1)
# 兼容已运行过旧脚本的配置，补齐 Go 实际返回的 JavaScript MIME。
s=s.replace('gzip_types application/json application/javascript text/css image/svg+xml;',
 'gzip_types application/json application/javascript text/javascript text/css image/svg+xml;')
# 原始 Nginx 错误包含完整 URL（SSE 查询中有令牌），不启用该输出。
p.write_text(s)
PY
if nginx -t; then
  systemctl reload nginx
else
  cp -p "$backup" "$config"
  exit 1
fi
