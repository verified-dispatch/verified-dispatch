#!/usr/bin/env bash
# Runs a helper in its own group that ignores SIGTERM, then waits. A
# teardown of this running child kills both through the group; the helper
# is the child's own work, not a leftover.
set -u
bash -c 'trap "" TERM; while :; do sleep 1; done' &
wait
