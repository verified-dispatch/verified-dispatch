#!/usr/bin/env bash
# Exits at once and leaves a background process behind with no pipe to
# the tool. This is what the contract forbids; teardown must find it.
set -u
sleep 300 >/dev/null 2>&1 </dev/null &
echo "$!"
exit 0
