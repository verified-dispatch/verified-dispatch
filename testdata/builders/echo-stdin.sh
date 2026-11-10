#!/usr/bin/env bash
# Copies stdin to stdout, so a test can see exactly what the child was fed.
set -u
exec cat
