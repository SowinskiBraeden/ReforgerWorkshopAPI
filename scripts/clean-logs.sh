#!/usr/bin/env sh
set -eu

LOG_DIR="${LOG_DIR:-logs}"
LOG_RETENTION_DAYS="${LOG_RETENTION_DAYS:-14}"

case "$LOG_RETENTION_DAYS" in
  ''|*[!0-9]*)
    echo "LOG_RETENTION_DAYS must be a positive integer" >&2
    exit 2
    ;;
esac

if [ "$LOG_RETENTION_DAYS" -eq 0 ]; then
  echo "LOG_RETENTION_DAYS must be greater than 0" >&2
  exit 2
fi

if [ ! -d "$LOG_DIR" ]; then
  exit 0
fi

find "$LOG_DIR" -type f -name '????-??-??.log' -mtime +"$LOG_RETENTION_DAYS" -delete
