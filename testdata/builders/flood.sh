#!/usr/bin/env bash
# Writes one MiB to stdout. A capped writer must let it finish.
set -u
head -c 1048576 /dev/zero | tr '\0' x
