# Velora

Servidor multimedia para instalar en tu propio equipo y reproducir tus películas y series desde la web o Android (móvil, tablet y TV). Tus bibliotecas, cuentas, progreso y configuración viven en tu servidor. No necesita una cuenta central de Velora.

**Versión 0.22.3 · GPL-3.0-or-later.** Proyecto en desarrollo activo: conserva copias de seguridad antes de actualizar.

## Instalación con Docker Compose

Necesitas Docker Engine con Compose v2.20 o superior y espacio para PostgreSQL, imágenes, caché y conversiones. La compilación descarga dependencias; el servidor necesita acceso a los proveedores que habilites. Los archivos pueden estar en discos locales o montajes que gestiones tú.

```sh
# Desde la carpeta del código descargado de este repositorio:
docker compose up -d --build
```

Abre `http://IP_DE_TU_SERVIDOR:8640`. Consulta el código de instalación inicial en tu servidor:

```sh
docker compose logs server
```

Completa el asistente y crea tu administrador. El código inicial no es el código de seis dígitos para vincular dispositivos. Después añade tus carpetas desde **Perfil → Panel de control → Bibliotecas**; las carpetas del servidor aparecen bajo `/host` (por ejemplo `/host/home` y `/host/mnt`), en solo lectura. El montaje `/media` se conserva. Consulta cómo restringir las carpetas y ajustar permisos en la [guía de instalación](docs/installation.md).

No necesitas crear un `.env` ni ejecutar Python para instalar. La contraseña de PostgreSQL se genera automáticamente y se conserva en un volumen privado `credentials`; los siguientes arranques reutilizan la misma. Para restringir el explorador o añadir un montaje de medios concreto, copia opcionalmente `deploy/.env.example` a `deploy/.env`, ajusta `VELORA_HOST_PATH` o `VELORA_MEDIA_PATH` y repite el comando. Si ya tenías `deploy/.env`, se sigue leyendo y se conserva su contraseña de base de datos.

La contraseña de PostgreSQL es propia de cada instalación. La clave maestra y el código inicial se generan en el primer arranque. **Este repositorio no contiene cuentas, claves API, tokens, bibliotecas ni credenciales del autor.**

## Funciones

- Bibliotecas de películas y series, fichas, reparto, imágenes y colecciones TMDB con al menos dos películas disponibles; versiones por resolución y progreso compartido entre calidades de una película.
- Catálogo actualizado durante el escaneo, continuar viendo y siguiente episodio.
- Reproducción directa cuando el dispositivo admite las pistas; remultiplexado o conversión solo cuando hace falta, con límites configurables y aceleración Intel VAAPI opcional.
- Detección de introducciones/créditos, controles para saltarlos, subtítulos locales y selección de audio/subtítulos. Volumen automático opcional en el reproductor.
- Cuentas sin contraseña mediante QR/código y hogares: con permiso del administrador, cada cuenta puede crear su hogar y añadir miembros (10 perfiles por defecto) con historial y progreso independientes, papelera de 30 días, vinculación y retirada de dispositivos.
- PIN opcional de 4 dígitos por perfil con código de recuperación, bloqueo por inactividad aplicado por el servidor (nunca durante la reproducción) y privacidad del historial frente al propietario y al administrador.
- Límites de reproducción por usuario/hogar y por IP, actividad de reproducción y copias de seguridad.

## APIs de metadatos e imágenes: aporta tus propias claves

**Cada instalación debe obtener sus propias credenciales** para los proveedores que desee activar. Velora no proporciona una clave compartida. TMDB es la fuente principal de identificación y colecciones; las otras fuentes amplían las imágenes disponibles. Sin configurarlas puedes añadir tus archivos, pero no tendrás las mismas fichas e imágenes automáticas.

Configúralas en **Panel de control → Metadatos → Fichas y carátulas**. La disponibilidad, condiciones, límites y posibles cuotas son de cada proveedor, no de Velora.

| Servicio | Uso en Velora | Credenciales propias |
| --- | --- | --- |
| [TMDB](https://developer.themoviedb.org/docs/getting-started) | Identificar películas/series, fichas, reparto, colecciones e imágenes | Clave API v3 obtenida en tu cuenta TMDB |
| [TheTVDB](https://thetvdb.com/api-information) | Fuente adicional de imágenes | Clave API v4 y PIN de suscriptor si tu acceso lo exige |
| [Fanart.tv](https://fanart.tv/get-an-api-key/) | Logos, fondos, carteles y otros tipos de arte | Clave de proyecto; clave personal opcional |
| [OMDb](https://www.omdbapi.com/apikey.aspx) | Carteles de títulos identificados por IMDb | Clave API propia; no sustituye a TMDB ni ofrece todos los tipos de imagen |
| [OpenSubtitles.com](https://opensubtitles.stoplight.io/docs/opensubtitles-api) | Buscar subtítulos externos cuando faltan locales | Clave API propia y cuenta cuando corresponda |

No es necesario habilitarlos todos. No prometemos acceso gratuito ilimitado ni compatibilidad con cualquier plan. Velora no incluye un acceso de usuario/contraseña al servicio antiguo de OpenSubtitles.org. Respeta las condiciones y atribución de cada fuente; su contenido no está cubierto por la licencia del código de Velora.

## Documentación

- **[Manual de Velora](docs/manual/README.md)**: todas las funciones, para usuarios y administradores
- [Instalación, almacenamiento, actualizaciones y copias](docs/installation.md)
- [Configuración de proveedores](docs/providers.md)
- [Cuentas y hogares](docs/manual/03-cuentas-y-hogares.md) y [PIN, bloqueo y privacidad](docs/manual/04-pin-bloqueo-privacidad.md)
- [Descargar APK firmado para móvil y TV](https://github.com/FarAoN0211/Velora/releases/latest)
- [Android, firma propia y OTA](clientes/android/README.md)
- [Seguridad y despliegue con HTTPS](SECURITY.md)
- [Desarrollo y pruebas](CONTRIBUTING.md)
- [Licencias de dependencias](docs/licencias.md)

Velora no instala ni necesita Radarr, Sonarr, Jackett, Teldrive o Telegram. Si usas almacenamiento remoto, móntalo y administra sus credenciales fuera de Velora. Una biblioteca local funciona sin esos servicios.

## Limitaciones

La compatibilidad de vídeo, HDR, sonido multicanal, pantalla completa y subtítulos depende del dispositivo, navegador y servidor. Una transcodificación 4K necesita recursos suficientes y hardware compatible; no se garantiza en cualquier equipo. Las marcas automáticas pueden requerir revisión. El cliente Android admite servidores de la red local por HTTP y cualquier servidor por HTTPS (ver [la guía de instalación](docs/installation.md) para ponerlo detrás de un proxy). No se incluyen películas, series, bases de datos personales ni una clave privada de firma Android.

## Licencia y atribución

Código bajo [GPL-3.0-or-later](LICENSE). Al distribuir versiones modificadas debes cumplir la licencia y ofrecer el código correspondiente. Las dependencias, marcas e imágenes de terceros conservan sus propias condiciones. Velora no está afiliado a TMDB, TheTVDB, Fanart.tv, OMDb u OpenSubtitles. Consulta [las condiciones de TMDB](https://developer.themoviedb.org/docs/faq) y las de las otras fuentes antes de distribuir o explotar servicios basados en sus datos.

## Actualizar una instalación existente

```sh
git pull --ff-only
docker compose up -d --build
```

Conserva `deploy/.env` si lo tenías, tus volúmenes y las rutas de los medios. No uses `down -v` para actualizar. El archivo Compose de la raíz reutiliza los servicios, nombre del proyecto y volúmenes anteriores; también sigue funcionando el comando antiguo con `--env-file deploy/.env -f deploy/compose.yaml`.

La versión0.22.1 corrige una omisión en el paquete inicial: faltaba la configuración PostCSS y Tailwind no generaba sus utilidades. Se incluye la configuración completa y la compilación comprueba los estilos de web y TV. No se necesita borrar la base de datos, volver a crear usuarios ni reescanear bibliotecas para aplicar esta corrección.

La versión 0.22.3 amplía el margen para preparar vídeo convertido en conexiones variables: adelanto inicial de 20 segundos y ritmo normal de preparación de 3× y recuperación de hasta 6×, sujeto a la capacidad real del servidor y al consumo del cliente. No cambia la velocidad de reproducción, la resolución ni los códecs elegidos; mantiene los límites de hilos y concurrencia. No requiere otro APK. Las desconexiones o un ancho de banda inferior al del vídeo siguen necesitando revisar la conexión o ajustar la calidad.
