#!/bin/sh
# Prueba publicar.sh sin SDK ni clave de firma: copia el script a una carpeta temporal y simula la compilación y la verificación.
set -eu
aqui=$(cd "$(dirname "$0")/.." && pwd)
t=$(mktemp -d)
trap 'rm -rf "$t"' EXIT
mkdir -p "$t/app" "$t/ota" "$t/home/.config/velora-android"
cp "$aqui/publicar.sh" "$t/app/publicar.sh"
touch "$t/home/.config/velora-android/firma.properties"
cat > "$t/gradlew" <<'E'
#!/bin/sh
[ -z "${FALLA_COMPILACION:-}" ] || exit 1
mkdir -p app/build/outputs/apk/release
printf 'apk %s' "$(cat version.properties)" > app/build/outputs/apk/release/app-release.apk
E

fallos=0
publicar() {
  HOME="$t/home" VELORA_OTA="$t/ota" VELORA_GRADLEW="sh $t/gradlew" VELORA_APKSIGNER=true sh "$t/app/publicar.sh" "$@" >/dev/null 2>&1
}
props() { printf 'versionCode=%s\nversionName=%s\n' "$1" "$2" > "$t/app/version.properties"; }
bien() { printf 'ok    %s\n' "$1"; }
mal() { printf 'FALLO %s\n' "$1"; fallos=$((fallos + 1)); }
campo() { python3 -c "import json,sys;print(json.load(open(sys.argv[1]))[sys.argv[2]])" "$t/ota/velora.json" "$1"; }

props 2 0.1.0
if publicar 0.2.0 && [ "$(campo versionCode)" = 2 ] && [ "$(campo versionName)" = 0.2.0 ] && [ -f "$t/ota/$(campo archivo)" ]; then bien 'primera publicación: JSON válido y archivo presente'; else mal 'primera publicación'; fi
[ "$(sha256sum "$t/ota/velora-2.apk" | cut -d' ' -f1)" = "$(campo sha256)" ] && [ -n "$(campo sha256)" ] && bien 'la huella es la del archivo' || mal 'huella'
[ ! -e "$t/ota/velora.json.tmp" ] && bien 'sin temporales' || mal 'temporal'

if publicar && [ "$(campo versionCode)" = 3 ]; then bien 'la versión sube sobre lo publicado'; else mal 'versión creciente'; fi
grep -q '^versionCode=3$' "$t/app/version.properties" && bien 'el contador queda al día' || mal 'contador'

antes=$(cat "$t/ota/velora.json")
salto='1.0
basura'
largo=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
for raro in '0.3"x' '0 3' 'a$b' '-rara' "$salto" "$largo"; do
  if publicar "$raro"; then mal "acepta el nombre [$raro]"; else bien "rechaza el nombre [$raro]"; fi
done
[ "$antes" = "$(cat "$t/ota/velora.json")" ] && bien 'un nombre rechazado no toca el anuncio' || mal 'anuncio tocado'

props abc 0.3.0
if publicar; then mal 'acepta versionCode no numérico'; else bien 'rechaza versionCode no numérico'; fi
props 0 0.3.0
if publicar; then mal 'acepta versionCode 0'; else bien 'rechaza versionCode 0'; fi

props 3 0.3.0
if FALLA_COMPILACION=1 publicar 0.4.0; then mal 'ignora una compilación fallida'; else bien 'una compilación fallida aborta'; fi
grep -q '^versionCode=3$' "$t/app/version.properties" && grep -q '^versionName=0.3.0$' "$t/app/version.properties" && bien 'y el contador vuelve a como estaba' || mal 'contador tras el fallo'
[ "$antes" = "$(cat "$t/ota/velora.json")" ] && bien 'y el anuncio no cambia' || mal 'anuncio tras el fallo'

printf 'basura' > "$t/ota/velora.json"
if publicar; then mal 'pisa un anuncio ilegible'; else bien 'no pisa un anuncio ilegible'; fi
[ "$(cat "$t/ota/velora.json")" = basura ] && bien 'y lo deja intacto' || mal 'anuncio ilegible tocado'

for malo in 'basura "versionCode": 2.5' '{"versionCode": 2.5}' '{"versionCode": "9"}' '{"versionCode": true}' '{"versionCode": 0}' '{"versionCode": -4}' '[]' '{}'; do
  printf '%s' "$malo" > "$t/ota/velora.json"
  if publicar; then mal "acepta el anuncio [$malo]"; else bien "rechaza el anuncio [$malo]"; fi
  [ "$(cat "$t/ota/velora.json")" = "$malo" ] || mal "toca el anuncio [$malo]"
done

rm "$t/ota/velora.json"
for i in 4 5 6 7; do props "$i" 0.5.0; publicar || mal "publicar $i"; done
[ "$(ls "$t"/ota/velora-*.apk | wc -l)" = 3 ] && [ -f "$t/ota/velora-7.apk" ] && bien 'conserva las tres últimas' || mal 'rotación'

mv "$t/home/.config/velora-android/firma.properties" "$t/firma"
if publicar; then mal 'publica sin clave'; else bien 'no publica sin clave'; fi

[ "$fallos" = 0 ] && echo 'publicar-test: todo correcto' || { echo "publicar-test: $fallos fallos"; exit 1; }
