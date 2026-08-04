#!/bin/bash
# Stands in for the jira CLI so tests never touch the network.
# Echoes the args it was given to stderr so a test can assert on them.
echo "args: $*" >&2
case "$1" in
    search)  cat "$(dirname "$0")/search.json" ;;
    get)     printf '{"key":"%s","description":"# %s\\n\\n本文です"}\n' "$2" "$2" ;;
    comment) printf '{"issue_key":"%s","total":1,"comments":[{"id":"1","author":"甲","body":"ひとこと","created":"2026-06-18T16:10:10.119+0900"}]}\n' "$3" ;;
    create)  printf '{"key":"NEW-1","id":"1","self":"x","url":"https://example.atlassian.net/browse/NEW-1"}\n' ;;
    *)       echo "unexpected command: $1" >&2; exit 64 ;;
esac
