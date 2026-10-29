#!/usr/bin/env bash
set -euo pipefail
postgres_container=$1

# DB 用例不启动引擎，保留整套执行并逐项核对已存在的失败。
if go test ./db -count=1 -v > backend-tests.log 2>&1; then
  echo 'database suite passed'
else
  git fetch --depth=1 origin main
  git worktree add --detach "$RUNNER_TEMP/artex-baseline" FETCH_HEAD
  docker exec "$postgres_container" createdb -U artex_test artex_baseline_test
  (cd "$RUNNER_TEMP/artex-baseline" && ARTEX_PG_DSN='postgres://artex_test:synthetic_ci_only@127.0.0.1:5432/artex_baseline_test?sslmode=disable' go test ./db -count=1 -v -run '^(TestInterceptApprovalFilters|TestSideTaskArchiveVersions)$') > baseline-tests.log 2>&1 || true
  python3 - <<'PY'
import pathlib,re
failures=lambda name:set(re.findall(r'^--- FAIL: (\S+)',pathlib.Path(name).read_text(),re.M))
current,baseline=failures('backend-tests.log'),failures('baseline-tests.log')
print('database failures:',sorted(current),'baseline failures:',sorted(baseline))
assert current and current<=baseline, 'new database regression'
PY
fi

# Server.New 会自动恢复库内所有任务。每个顶层用例使用独立进程和空库，
# 避免其它用例残留的引擎继续写入已清理的临时目录；不跳过任何 Server 用例。
binary="$RUNNER_TEMP/artex-server.test"
go test -c -o "$binary" ./server
"$binary" -test.list '^Test' | awk '/^Test/ {print}' > "$RUNNER_TEMP/artex-server-tests"
index=0
failed=0
while IFS= read -r name; do
  index=$((index + 1))
  database="artex_server_${index}"
  docker exec "$postgres_container" createdb -U artex_test "$database"
  echo "[$index] $name"
  if ! (cd server && ARTEX_PG_DSN="postgres://artex_test:synthetic_ci_only@127.0.0.1:5432/${database}?sslmode=disable" "$binary" -test.run "^${name}$" -test.v -test.timeout=90s) >> backend-tests.log 2>&1; then
    echo "FAILED: $name"
    failed=$((failed + 1))
  fi
  docker exec "$postgres_container" dropdb -U artex_test --force "$database"
done < "$RUNNER_TEMP/artex-server-tests"
grep -E 'TestLoading|old_ms=|TestRequestTiming' backend-tests.log
if [ "$failed" -ne 0 ]; then
  tail -80 backend-tests.log
  exit 1
fi
echo "server_tests=$index failures=$failed"
