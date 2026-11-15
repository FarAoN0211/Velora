#!/bin/sh
set -eu
secret=/run/velora-secrets/db_password
if [ -s "$secret" ]; then
 if [ -n "${VELORA_DB_PASSWORD:-}" ] && [ "$(cat "$secret")" != "$VELORA_DB_PASSWORD" ]; then
  echo 'La contraseña indicada no coincide con la almacenada. No se modifica ningún secreto.' >&2
  exit 1
 fi
else
 umask 077
 if [ -n "${VELORA_DB_PASSWORD:-}" ]; then
  printf '%s' "$VELORA_DB_PASSWORD" > "$secret.tmp"
 else
  od -An -N32 -tx1 /dev/urandom | tr -d ' \n' > "$secret.tmp"
 fi
 test -s "$secret.tmp"
 # Solo los contenedores autorizados montan este volumen; PostgreSQL y Velora usan UID distintos.
 chmod 0444 "$secret.tmp"
 mv "$secret.tmp" "$secret"
fi
