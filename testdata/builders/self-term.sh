#!/usr/bin/env bash
# Ends by its own signal, so a test sees a signaled status.
set -u
kill -TERM $$
sleep 5
exit 0
