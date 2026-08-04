#!/bin/bash
# Stands in for the jira CLI so tests never touch the network.
# Echoes the args it was given to stderr so a test can assert on them.
echo "args: $*" >&2
case "$1" in
    search) cat "$(dirname "$0")/search.json" ;;
    get)    printf '# %s\n\n本文です\n' "$2" ;;
    *)      echo "unexpected command: $1" >&2; exit 64 ;;
esac
