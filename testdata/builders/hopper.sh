#!/usr/bin/env bash
# Leaves behind a process that forks a new copy of itself in a new session
# and exits, over and over. The test binary supplies it in a helper mode.
# No listing can catch it alive; teardown must report it, not miss it.
set -u
"$PROCS_TESTBIN" -test.run='^$' >/dev/null 2>&1 </dev/null &
exit 0
