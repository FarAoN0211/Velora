# Instalar y mantener Velora

## Configuración inicial

1. Descarga el código y ejecuta `docker compose up -d --build` desde la raíz. Requiere Compose2.20 o superior. No exige Python, `.env` ni claves compartidas: el servicio init crea una contraseña aleatoria y persistente en el volumen credentials; PostgreSQL y Velora la leen como archivo.
2. Abre `http://IP_DE_TU_SERVIDOR:8640` y consulta `docker compose logs server` para el código inicial. Crea el administrador y añade tus bibliotecas.
3. En Linux, el explorador muestra las carpetas del servidor mediante `/host`: por ejemplo, `/home/tu-usuario/Vídeos` aparece como `/host/home/tu-usuario/Vídeos`, y un disco en `/mnt/Disco` aparece como `/host/mnt/Disco`. No son las carpetas del teléfono o del navegador. El acceso es de solo lectura y conserva los permisos del sistema. El montaje anterior `/media` también se conserva para las bibliotecas existentes.
   Para limitar el acceso a una carpeta concreta, copia `deploy/.env.example` a `deploy/.env`, con permisos0600, y define `VELORA_HOST_PATH=/ruta/a/tus/medios`. Aparecerá como `/host`; solo se mostrarán sus subcarpetas. `VELORA_MEDIA_PATH` controla el montaje adicional `/media`. Repite `docker compose up -d` después de cambiar montajes. No hace falta recompilar. Si el propietario de los archivos no es UID1000/GID100, configura `VELORA_UID` y `VELORA_GID` o concede lectura y acceso a directorios al usuario de Velora, sin hacer públicos los archivos. Los enlaces que apuntan fuera del montaje deben sustituirse por un montaje de la carpeta de destino. En Docker Desktop, comparte primero las unidades o carpetas desde sus ajustes.
4. Configura tus propias API desde Metadatos. Una instalación nueva no importa las claves de ninguna otra persona. Las fuentes son opcionales y no cambian los estilos de la interfaz.

`database` conserva PostgreSQL; `config` contiene imágenes, caché y secretos de la aplicación; `backups` las copias; `credentials` el secreto de conexión a PostgreSQL. No publiques ninguno. Los nombres mantienen el prefijo del proyecto Compose (`velora` por defecto). No uses `down -v` salvo que quieras borrar los datos.

`python3 deploy/configure.py` sigue disponible como ayuda opcional para crear la plantilla de configuración y carpetas. No cambia ni genera otra contraseña si Velora ya se inició automáticamente. Si vienes de una instalación anterior, conserva tu `deploy/.env` original: el include de Compose lo carga, y init conserva ese mismo valor en credentials para seguir accediendo a la base existente. No borres ni sustituyas la contraseña de una base ya inicializada.

## Parámetros

Consulta `deploy/.env.example`. `VELORA_PORT` cambia el puerto externo, `VELORA_BIND` la interfaz y `VELORA_TZ` la zona horaria. `VELORA_MIN_FREE_GB` reserva espacio para evitar agotar el disco. No cambies `VELORA_DB_PASSWORD` después de inicializar PostgreSQL sin realizar también la rotación en la base de datos.

El Compose predeterminado usa HTTP: sirve para una red privada de confianza. No abras su puerto directamente a Internet. Para acceso público configura un proxy HTTPS y `VELORA_SECURE_COOKIES=true`, `VELORA_PUBLIC_URLS=https://tu-dominio.example`. Si necesitas conservar la IP del cliente, declara en `VELORA_TRUSTED_PROXIES` exclusivamente el CIDR del proxy de confianza. HTTPS no cambia los permisos de biblioteca ni elimina la necesidad de cuentas.

## Hardware y consumo

El Compose básico funciona sin GPU y limita RAM del servidor y worker. En Linux con Intel VAAPI:

```sh
stat -c '%g' /dev/dri/renderD128
# Añade ese número como VELORA_RENDER_GID a deploy/.env.
docker compose --env-file deploy/.env -f deploy/compose.yaml -f deploy/compose.intel.yaml up -d --build
```

La detección del servidor comprueba la disponibilidad del codificador. Una GPU presente no garantiza que admita cada códec, profundidad o HDR. Ajusta resolución/bitrate y concurrencia en Reproducción. La reproducción directa conserva el archivo original. En equipos ARM la imagen se compila en la arquitectura anfitriona, pero no se ha validado aquí todo el flujo ni hay aceleración ARM configurada.

## APK público

Descarga el APK firmado desde [GitHub Releases](https://github.com/FarAoN0211/Velora/releases/latest). Sirve para móvil, tablet y Android TV; pide tu servidor al abrirlo y no incluye cuentas ni claves de proveedores. La firma privada nunca se distribuye. Consulta [Android y OTA](../clientes/android/README.md) para ofrecer las actualizaciones desde tu propio servidor.

## Bibliotecas remotas

Velora no administra tus cuentas Telegram ni monta Teldrive. Si ya tienes un montaje remoto, úsalo como fuente del bind de medios. Su rendimiento, permisos y conectividad deben resolverse en el host. No pegues tokens en el Compose ni en incidencias públicas.

## Actualizar

Conserva el código y el tag anteriores. Realiza una copia desde el panel y una copia protegida de `deploy/.env` y del volumen `config`, incluida su carpeta `secrets`, y del volumen `credentials`. Obtén la nueva versión y ejecuta el mismo comando `up -d --build`. Las migraciones se aplican automáticamente; una versión anterior podría no entender el esquema nuevo. Para volver atrás tras una migración incompatible restaura la copia en una instalación aislada y usa la versión correspondiente. No restaures encima de una reproducción activa.

Las copias ordinarias de la aplicación no incluyen todos los secretos de instalación. Una copia de PostgreSQL sola no basta para reconstruir la configuración. Guarda las claves en un destino privado cifrado y prueba periódicamente una restauración.

## Diagnóstico

```sh
docker compose ps
docker compose logs --tail=100 server worker
curl -f http://IP_DE_TU_SERVIDOR:8640/healthz
```

Antes de publicar logs elimina rutas personales, direcciones, usuarios, códigos iniciales/QR, cabeceras, cookies y tokens. Los datos de una incidencia nunca deben incluir el volumen config ni una copia de la BD.
