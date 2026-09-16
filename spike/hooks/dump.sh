#!/bin/sh
t=$(date +%s%N)
{ echo "--- $1 $t"; echo "argv: $*"; env | grep -iE '^(CLAUDE|CODEX|KANBAN)' ; echo "stdin:"; cat; echo; } >> "$(dirname "$0")/out/$1.log"
