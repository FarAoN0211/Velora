#!/bin/sh
# Prueba de humo de E0 y E1 contra el binario real, desde la red interna de velora-dev (sin puertos publicados).
# Uso: docker compose -p velora-dev -f deploy/compose.dev.yaml run --rm go sh /src/deploy/dev-go/smoke.sh
set -eu
B=${VELORA_SMOKE_URL:-http://server:8640}
fail() { echo "FALLO: $*"; exit 1; }

for i in $(seq 1 30); do wget -qO- "$B/healthz" >/dev/null 2>&1 && break; sleep 1; done
wget -qO- "$B/healthz" | grep -q '"ok"' || fail "healthz"
echo "ok   healthz"

wget -qO- "$B/api/v1/system/info" | grep -q '"setup_required":true' || fail "system/info debería pedir el asistente"
echo "ok   system/info pide el asistente inicial"

code=$(cat "${VELORA_SMOKE_CODE_FILE:-/src/.dev/config/secrets/setup-code}" 2>/dev/null) || fail "no se encuentra el código de instalación"
nocode='{"server_name":"Pruebas","username":"marta","display_name":"Marta","password":"contraseña-de-prueba"}'
if wget -qO- --header="Content-Type: application/json" --post-data="$nocode" "$B/api/v1/setup" >/dev/null 2>&1; then
  fail "el asistente no debería aceptar peticiones sin código de instalación"
fi
echo "ok   sin código de instalación no se crea el administrador"
body="{\"setup_code\":\"$code\",\"server_name\":\"Pruebas\",\"username\":\"marta\",\"display_name\":\"Marta\",\"password\":\"contraseña-de-prueba\"}"
resp=$(wget -qO- --header="X-Velora-Client: android" --header="Content-Type: application/json" --post-data="$body" "$B/api/v1/setup")
token=$(echo "$resp" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')
[ ${#token} -ge 43 ] || fail "setup no devolvió token"
echo "ok   setup crea el administrador y abre sesión"

if wget -qO- --header="Content-Type: application/json" --post-data="$body" "$B/api/v1/setup" >/dev/null 2>&1; then
  fail "un segundo setup no debería aceptarse"
fi
echo "ok   un segundo setup se rechaza"

auth="Authorization: Bearer $token"
wget -qO- --header="$auth" "$B/api/v1/me" | grep -q '"username":"marta"' || fail "me"
echo "ok   me devuelve el usuario"

wget -qO- --header="$auth" --post-data="" "$B/api/v1/admin/backup" | grep -q job_id || fail "admin/backup"
echo "ok   copia encolada"
verified=""
for i in $(seq 1 60); do
  s=$(wget -qO- --header="$auth" "$B/api/v1/admin/backups")
  if echo "$s" | grep -q '"verified":true'; then verified=1; break; fi
  sleep 1
done
[ -n "$verified" ] || fail "el worker no completó una copia verificada en 60 s: $s"
echo "ok   el worker hizo la copia y la verificó restaurándola"
# El worker guarda el informe de almacenamiento justo después de verificar la copia.
reported=""
for i in $(seq 1 15); do
  if wget -qO- --header="$auth" "$B/api/v1/admin/backups" | grep -q '"warn_margin_invasion"'; then reported=1; break; fi
  sleep 1
done
[ -n "$reported" ] || fail "no se registró el informe de almacenamiento"
echo "ok   informe de almacenamiento registrado"

# --- E1: biblioteca de películas, decisión de reproducción y streaming ---
m=/src/.dev/media/Peliculas/"Prueba sintética (2020)"
if [ ! -f "$m/Prueba sintética (2020).mkv" ]; then
  mkdir -p "$m"
  ffmpeg -hide_banner -loglevel error -f lavfi -i testsrc=size=320x240:rate=25:duration=20 -f lavfi -i sine=frequency=440:duration=20 \
    -c:v libx264 -preset ultrafast -g 25 -c:a ac3 -metadata:s:a:0 language=spa "$m/Prueba sintética (2020).mkv"
fi
wget -qO- --header="$auth" "$B/api/v1/admin/media-folders" | grep -q '"/media/Peliculas"' || fail "media-folders no lista /media/Peliculas"
echo "ok   media-folders lista las carpetas montadas"
if wget -qO- --header="$auth" --header="Content-Type: application/json" --post-data='{"name":"Fuera","path":"/etc"}' "$B/api/v1/libraries" >/dev/null 2>&1; then
  fail "una biblioteca fuera de las raíces de medios debería rechazarse"
fi
echo "ok   se rechaza una carpeta fuera de las raíces de medios"
wget -qO- --header="$auth" --header="Content-Type: application/json" --post-data='{"name":"Películas","path":"/media/Peliculas"}' "$B/api/v1/libraries" | grep -q '"library"' || fail "crear biblioteca"
item=""
for i in $(seq 1 60); do
  item=$(wget -qO- --header="$auth" "$B/api/v1/items" | sed -n 's/.*"items":\[{[^{}]*"id":"\([^"]*\)".*/\1/p')
  [ -n "$item" ] && break
  sleep 1
done
[ -n "$item" ] || fail "el worker no escaneó la biblioteca en 60 s"
echo "ok   el worker escaneó la biblioteca y la película aparece"
# La película aparece al registrarse su archivo; sus pistas llegan al terminar el análisis (ffprobe), un momento después.
for i in $(seq 1 60); do
  detail=$(wget -qO- --header="$auth" "$B/api/v1/items/$item")
  echo "$detail" | grep -q '"streams":\[{' && break
  sleep 1
done
echo "$detail" | grep -q '"streams":\[{' || fail "el archivo no terminó de analizarse en 60 s: $detail"
echo "$detail" | grep -q '"title":"Prueba sintética"' || fail "título deducido de la carpeta: $detail"
file=$(echo "$detail" | sed -n 's/.*"files":\[{"id":"\([^"]*\)".*/\1/p')
[ -n "$file" ] || fail "la ficha no tiene archivo"
echo "ok   ficha con título y año deducidos del nombre"

web='{"file_id":"'$file'","caps":{"containers":["mp4","webm"],"video":["h264"],"audio":["aac","mp3","opus"]}}'
plan=$(wget -qO- --header="$auth" --header="Content-Type: application/json" --post-data="$web" "$B/api/v1/playback/decide")
echo "$plan" | grep -q '"method":"direct_stream"' || fail "un navegador sin AC3 ni MKV debería ir por direct_stream: $plan"
echo "$plan" | grep -q '"convert_audio":true' || fail "el AC3 debería convertirse a AAC: $plan"
url=$(echo "$plan" | sed -n 's/.*"url":"\([^"]*\)".*/\1/p' | sed 's/\\u0026/\&/g')
echo "ok   decide: MKV+AC3 en navegador → Direct Stream con audio a AAC"
wget -qO- --header="$auth" "$B$url&t=5" 2>/dev/null | head -c 64 | grep -q ftyp || fail "el remux no devuelve MP4 fragmentado"
echo "ok   remux en vivo desde el segundo 5 devuelve MP4 fragmentado"

tv='{"file_id":"'$file'","caps":{"containers":["mkv","mp4"],"video":["h264","hevc"],"audio":["aac","ac3","eac3"]}}'
plan=$(wget -qO- --header="$auth" --header="Content-Type: application/json" --post-data="$tv" "$B/api/v1/playback/decide")
echo "$plan" | grep -q '"method":"direct_play"' || fail "un cliente con MKV y AC3 debería ir por direct_play: $plan"
size=$(wget -qO- --header="$auth" "$B/api/v1/stream/files/$file" | wc -c)
[ "$size" -gt 10000 ] || fail "direct play devolvió $size bytes"
echo "ok   decide: cliente compatible → Direct Play del archivo original ($size bytes)"

if wget -qO- "$B/api/v1/me" >/dev/null 2>&1; then fail "me sin sesión debería dar 401"; fi
echo "ok   sin sesión no hay acceso"

wget -qO- --header="$auth" --post-data="" "$B/api/v1/auth/logout" >/dev/null 2>&1 || true
if wget -qO- --header="$auth" "$B/api/v1/me" >/dev/null 2>&1; then fail "la sesión cerrada sigue valiendo"; fi
echo "ok   logout invalida el token"
echo "PRUEBA DE HUMO SUPERADA"
