# Velora para Android

Un APK para móvil, tablet y TV Box (Android6 o superior). La navegación carga la web de tu servidor; Media3 proporciona el mismo reproductor nativo en TV, móvil y tablet, con detección de códecs del aparato y pantalla completa horizontal. Tocar el vídeo muestra los controles sin pausarlo. Requiere la web actualizada; el navegador y los APK antiguos conservan el reproductor web como respaldo.

## Descargar sin compilar

El [APK oficial firmado](https://github.com/FarAoN0211/Velora/releases/latest) sirve para móvil, tablet y Android TV. No necesitas compilar ni obtener una clave de firma para instalarlo. La clave privada del autor no se publica; el APK conserva la misma firma entre versiones.

Para ofrecer OTA en tu servidor, descarga de esa misma release el APK y `velora.json` y cópialos juntos en `dist/ota` (o en tu `VELORA_OTA_PATH`). Conserva sus nombres y no cambies el SHA256 del anuncio. El contenedor ya monta esa carpeta; no hace falta recompilarlo. Las actualizaciones se sirven desde tu servidor, sin acceso al servidor del autor. Las nuevas versiones se importan de igual forma.

## Servidor propio

Al instalar por primera vez se pide la dirección de **tu servidor**. No existe una dirección personal preconfigurada. Admite direcciones de la red local por HTTP (por ejemplo `192.168.1.10:8640`) y cualquier dirección HTTPS con certificado válido (por ejemplo `https://velora.tu-dominio.example`, con Velora en la raíz del dominio). Sin cifrar solo se acepta la red local; una dirección escrita sin `http://` ni `https://` se toma como HTTP si es local y como HTTPS si no lo es. Conserva la dirección guardada al actualizar. Si vienes de una versión antigua que utilizaba la dirección predeterminada sin guardarla, esta actualización te pedirá introducirla una vez; las cookies de sesión del mismo servidor se conservan. Cambiar de servidor se ofrece al fallar la conexión; las sesiones y QR no se transfieren entre servidores.

La pantalla de acceso muestra QR y código de seis dígitos. El administrador puede darte de alta sin contraseña y, si te da permiso, crear tu hogar y sus miembros. Consulta el [manual paso a paso](../../docs/manual/06-android-tv.md).

## Compilar

Instala JDK17, Android SDK35 y build-tools. Configura `ANDROID_HOME` o tu propio `local.properties` (excluido de Git).

```sh
cd clientes/android
./gradlew :app:testDebugUnitTest :app:assembleDebug
```

La compilación debug usa una firma de desarrollo propia del equipo. Para distribuir e instalar actualizaciones necesitas tu propia firma release; no se incluye la del autor.

Crea un almacén privado con `keytool` (solicitará las contraseñas sin ponerlas en el comando):

```sh
mkdir -p ~/.config/velora-android
chmod 700 ~/.config/velora-android
keytool -genkeypair -keystore ~/.config/velora-android/velora.jks -alias velora -keyalg RSA -keysize 3072 -validity 10000
```

Crea `~/.config/velora-android/firma.properties` con permisos0600:

```properties
archivo=velora.jks
alias=velora
clave=TU_CONTRASENA_PRIVADA
```

El script de compilación actual usa la misma contraseña para el almacén y la clave. No publiques ese archivo ni el almacén. Conserva una copia privada: perder la firma obliga a desinstalar y reinstalar para cambiarla. Un fork público debe usar su propia firma y puede cambiar applicationId para coexistir con otras distribuciones.

## OTA desde tu servidor

Desde la raíz del proyecto, deja el destino predeterminado `dist/ota` (coincide con el Compose). Si cambias `VELORA_OTA`, usa una ruta absoluta porque el script cambia su directorio de trabajo:

```sh
clientes/android/publicar.sh 0.7.6
```

Compila release, verifica la firma, genera el APK y publica el anuncio `velora.json` al final. `versionCode` debe superar el instalado; modifica `version.properties` antes de tu siguiente versión si fuera necesario. El servidor sirve ese directorio en `/ota/`; la app lo comprueba al abrirse y periódicamente. Android pide permiso para instalar.

El cliente valida SHA256 del archivo, nombre, tamaño, versión y **misma firma de la APK instalada**. Una APK firmada por ti no actualiza una aplicación firmada por otra persona. No se instala por ADB automáticamente.

## Comportamiento

En TV se navega con flechas/OK; Atrás cierra paneles o vuelve. El menú del administrador no aparece en TV. El móvil usa menú hamburguesa desplazable y el administrador accede a su panel desde el perfil. Las mejoras web llegan al recargar la app; cambios nativos requieren OTA. El PIN se escribe con las teclas numéricas del mando o con el teclado en pantalla. Mientras el reproductor nativo está abierto (también en pausa) sus latidos mantienen el perfil desbloqueado; al salir de la app dejan de enviarse y, si el servidor bloquea el perfil, el reproductor vuelve a la web para pedir el PIN.

La compatibilidad de códecs, HDR y sonido depende del hardware. Volumen automático se ajusta dentro de Audio y subtítulos; Original conserva el tratamiento original, mientras Equilibrado/Nocturno pueden requerir PCM en vez de passthrough. Las sesiones y cachés son privadas del dispositivo.
