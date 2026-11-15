#!/bin/sh
set -eu
if [ -z "${VELORA_DATABASE_URL:-}" ]; then
 secret=/run/velora-secrets/db_password
 if [ ! -s "$secret" ]; then
  echo 'Falta el secreto de PostgreSQL. Inicia Velora mediante Docker Compose.' >&2
  exit 1
 fi
 password=$(cat "$secret")
 # El generador y configure.py usan hexadecimal, seguro para una URL PostgreSQL.
 case "$password" in *[!0-9a-fA-F]*) echo 'Para contraseñas personalizadas no hexadecimales configura VELORA_DATABASE_URL con codificación URL.' >&2; exit 1;; esac
 export VELORA_DATABASE_URL="postgres://velora:${password}@db:5432/velora?sslmode=disable"
fi
exec /usr/local/lib/velora/velora "$@"
