#!/usr/bin/env bash
# Ignores SIGTERM and keeps running, so only SIGKILL ends it. The sleep
# children inherit the ignored signal and also die only from the group
# SIGKILL.
set -u
trap '' TERM
while :; do
  sleep 1
done
