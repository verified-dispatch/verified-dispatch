#!/usr/bin/env bash
# Ignores SIGTERM and keeps running, so only SIGKILL ends it. The sleep
# children die from the group signal; the loop keeps going.
set -u
trap '' TERM
while :; do
  sleep 1
done
