#!/usr/bin/env bash
# Prints two variables so a test can see that the environment is exactly
# what was passed, with nothing inherited from the tool.
set -u
printf '%s\n' "${PROBE-unset}" "${ABSENT-unset}"
