#!/bin/sh
# Compila la versión siguiente, la deja disponible para OTA y sube el contador. Se ejecuta en el portátil (ahí está el SDK de Android).
# Los aparatos la verán en cuanto abran Velora. Uso: clientes/android/publicar.sh [nombre-de-versión]
# Las variables VELORA_* solo las usa clientes/android/pruebas/publicar-test.sh, para probarlo sin SDK ni clave.
set -eu
cd "$(dirname "$0")"
OTA=${VELORA_OTA:-../../dist/ota}
ANDROID_HOME=${ANDROID_HOME:-$HOME/Android/Sdk}
export ANDROID_HOME
[ -f "$HOME/.config/velora-android/firma.properties" ] || { echo "Falta la clave de firma en ~/.config/velora-android."; exit 1; }
command -v python3 >/dev/null || { echo "Hace falta python3 para leer y escribir el anuncio."; exit 1; }

entero() { case "$1" in ''|*[!0-9]*) return 1;; *) [ "$1" -gt 0 ];; esac; }

actual=$(sed -n 's/^versionCode=//p' version.properties)
anterior=$(sed -n 's/^versionName=//p' version.properties)
entero "$actual" || { echo "versionCode no válido en version.properties: '$actual'."; exit 1; }
nombre=${1:-$anterior}
# Va dentro de un JSON y lo ve el usuario: nada de comillas ni caracteres raros.
# Con case y no con grep: grep mira línea a línea y daría por bueno un nombre de dos líneas si una de ellas lo es.
case "$nombre" in
  ''|*[!0-9A-Za-z._+-]*|[!0-9A-Za-z]*) echo "Nombre de versión no válido: '$nombre'."; exit 1;;
esac
[ "${#nombre}" -le 40 ] || { echo "Nombre de versión demasiado largo."; exit 1; }

# Un anuncio publicado que no se entiende no se pisa: podría retroceder el contador y los aparatos no verían la versión nueva.
codigo=$actual
if [ -f "$OTA/velora.json" ]; then
  publicado=$(python3 -c '
import json, sys
info = json.load(open(sys.argv[1], encoding="utf-8"))
codigo = info["versionCode"]
if type(codigo) is not int or codigo <= 0: sys.exit(1)
print(codigo)' "$OTA/velora.json" 2>/dev/null) || { echo "No se entiende el anuncio publicado en $OTA/velora.json."; exit 1; }
  if [ "$publicado" -ge "$actual" ]; then codigo=$((publicado + 1)); fi
fi

[ -d "$OTA" ] || mkdir -p "$OTA"
# Si la compilación falla, el contador vuelve a como estaba.
restaurar() { printf 'versionCode=%s\nversionName=%s\n' "$actual" "$anterior" > version.properties; }
trap restaurar EXIT
printf 'versionCode=%s\nversionName=%s\n' "$codigo" "$nombre" > version.properties
${VELORA_GRADLEW:-./gradlew} --quiet :app:assembleRelease
apk=app/build/outputs/apk/release/app-release.apk
herramientas=$(ls -d "$ANDROID_HOME"/build-tools/* 2>/dev/null | sort -V | tail -n 1)
"${VELORA_APKSIGNER:-$herramientas/apksigner}" verify "$apk"
huella=$(sha256sum "$apk" | cut -d' ' -f1)
archivo=velora-$codigo.apk
cp "$apk" "$OTA/$archivo.tmp" && mv "$OTA/$archivo.tmp" "$OTA/$archivo"
# El anuncio va lo último: nunca apunta a un archivo a medias.
python3 -c '
import json, sys
codigo, nombre, archivo, huella, destino = sys.argv[1:]
with open(destino, "w", encoding="utf-8") as f:
    json.dump({"versionCode": int(codigo), "versionName": nombre, "archivo": archivo, "sha256": huella}, f, indent=2)
    f.write("\n")' "$codigo" "$nombre" "$archivo" "$huella" "$OTA/velora.json.tmp"
mv "$OTA/velora.json.tmp" "$OTA/velora.json"
trap - EXIT
ls -1t "$OTA"/velora-*.apk | tail -n +4 | xargs -r rm -f
echo "Publicada la versión $nombre ($codigo): $archivo"
