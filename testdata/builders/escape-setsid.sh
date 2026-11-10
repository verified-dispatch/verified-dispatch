#!/usr/bin/env bash
# Starts a process in a new session and exits. The process has no link to
# the child's session or group; only a subreaper sees it, as a direct child
# from a foreign session.
set -u
setsid sleep 300 >/dev/null 2>&1 </dev/null &
echo "$!"
exit 0
