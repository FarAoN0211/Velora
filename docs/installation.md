# Instalar y mantener Velora

## Configuración inicial

1. Descarga el código y ejecuta `python3 deploy/configure.py`. Este comando crea `deploy/.env` con permisos 0600 y una contraseña PostgreSQL aleatoria; no pisa una configuración existente.
2. Edita `VELORA_MEDIA_PATH` con la ruta absoluta de tu carpeta de películas/series. El contenedor la monta en `/media` **solo para lectura**. La carpeta y sus archivos deben ser legibles por UID1000/GID100; puedes usar ACL o permisos de grupo, sin dar escritura a Velora.
3. Inicia `docker compose --env-file deploy/.env -f deploy/compose.yaml up -d --build`.
4. Abre tu dirección y consulta `docker compose --env-file deploy/.env -f deploy/compose.yaml logs server` para el código inicial. Crea el administrador y añade bibliotecas por separado, por ejemplo `/media/Peliculas` y `/media/Series`.
5. Configura tus propias API desde Metadatos. Una instalación nueva no importa las de ninguna otra persona.

`database` conserva PostgreSQL; `config` contiene imágenes, caché y secretos de ESTA instalación; `backups` contiene las copias. No publiques ninguno. Los volúmenes llevan el prefijo del proyecto Compose. No uses `down -v` salvo que quieras borrar los datos.

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

## Bibliotecas remotas

Velora no administra tus cuentas Telegram ni monta Teldrive. Si ya tienes un montaje remoto, úsalo como fuente del bind de medios. Su rendimiento, permisos y conectividad deben resolverse en el host. No pegues tokens en el Compose ni en incidencias públicas.

## Actualizar

Conserva el código y el tag anteriores. Realiza una copia desde el panel y una copia protegida de `deploy/.env` y del volumen `config`, incluida su carpeta `secrets`. Obtén la nueva versión y ejecuta el mismo comando `up -d --build`. Las migraciones se aplican automáticamente; una versión anterior podría no entender el esquema nuevo. Para volver atrás tras una migración incompatible restaura la copia en una instalación aislada y usa la versión correspondiente. No restaures encima de una reproducción activa.

Las copias ordinarias de la aplicación no incluyen todos los secretos de instalación. Una copia de PostgreSQL sola no basta para reconstruir la configuración. Guarda las claves en un destino privado cifrado y prueba periódicamente una restauración.

## Diagnóstico

```sh
docker compose --env-file deploy/.env -f deploy/compose.yaml ps
docker compose --env-file deploy/.env -f deploy/compose.yaml logs --tail=100 server worker
curl -f http://IP_DE_TU_SERVIDOR:8640/healthz
```

Antes de publicar logs elimina rutas personales, direcciones, usuarios, códigos iniciales/QR, cabeceras, cookies y tokens. Los datos de una incidencia nunca deben incluir el volumen config ni una copia de la BD.
