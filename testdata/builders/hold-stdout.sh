#!/usr/bin/env bash
# Exits at once but leaves a child holding stdout open, the shape that
# makes a naive wait hang until the child closes the pipe.
set -u
sleep 300 &
echo "$!"
exit 0
