# 6. Android y Android TV paso a paso

La app de Velora es un único APK para móvil, tablet y TV con Android 6 o superior. Muestra la web de tu servidor y, en
la TV, usa además un reproductor propio para el mando y para reproducir más formatos sin convertirlos.

## 1. Conseguir el APK

Descarga el APK oficial firmado desde [GitHub Releases](https://github.com/FarAoN0211/Velora/releases/latest). Es el mismo archivo para móvil, tablet y TV Box y no trae ningún servidor personal preconfigurado. También puedes compilar tu propia distribución ([instrucciones](../../clientes/android/README.md)). Para ofrecer OTA desde tu servidor, coloca el APK y su anuncio `velora.json` en `dist/ota`.

- **Móvil o tablet**: abre la página de Releases en el navegador, descarga el APK y ábrelo. Android pedirá permiso para instalar
  aplicaciones de ese origen.
- **TV Box / Android TV**: copia el APK con un pendrive o descárgalo con un gestor de archivos o un navegador para TV, y
  ábrelo. Android pedirá el mismo permiso.

## 2. Primera apertura: la dirección de tu servidor

La app pregunta la dirección de **tu** servidor. Sirve:

- una dirección de tu red local por HTTP, por ejemplo `192.168.1.10:8640`;
- o una dirección HTTPS con certificado válido, por ejemplo `https://velora.tu-dominio.example`.

Si la escribes sin `http://` ni `https://`, se toma como HTTP si es local y como HTTPS si no lo es. Fuera de tu red,
usa una VPN o HTTPS (nunca el puerto abierto a Internet). Si algún día la conexión falla, la app ofrece cambiar de
servidor.

## 3. Entrar

La pantalla de acceso muestra un **QR** y un **código de seis dígitos** (caduca en cinco minutos; si caduca, se genera
otro).

- **Si es tu primer dispositivo y no tienes cuenta**: comunica el código al administrador (por un canal de confianza).
  Él te da de alta sin contraseña y el dispositivo entra solo en cuanto confirma.
- **Si ya tienes cuenta o un hogar**: el propietario (o el administrador) autoriza este dispositivo desde otro en el que
  ya esté dentro: **Perfil → Vincular dispositivo**, escribe el código o pulsa **Escanear QR con Velora** en el móvil,
  comprueba el nombre del aparato, elige el perfil y, si tiene PIN, escríbelo.
- **Si tu cuenta tiene contraseña**: pulsa **Entrar con usuario y contraseña**.

## 4. Elegir perfil y usar el PIN con el mando

En un dispositivo del hogar, **Perfil → Cambiar usuario** muestra «¿Quién va a ver Velora?». Elige tu perfil con las
flechas y OK. Si tiene PIN:

- escríbelo con las **teclas numéricas del mando**, o
- muévete por el teclado de la pantalla con las flechas y pulsa OK en cada número.

Al cuarto dígito se abre solo. **He olvidado mi PIN** está en la misma pantalla
([cómo funciona](04-pin-bloqueo-privacidad.md#he-olvidado-mi-pin)).

## 5. Navegar con el mando

- **Flechas y OK** para moverte y elegir.
- **Atrás** cierra paneles y menús o vuelve a la pantalla anterior. En el inicio, pulsa Atrás dos veces para salir.
- **Menú** abre el menú del título seleccionado (reproducir desde el principio, marcar como visto…).
- El **Panel de control** del administrador no aparece en la TV: se usa desde el navegador o el móvil.

## 6. El reproductor de la TV

- OK muestra los controles; las flechas izquierda y derecha avanzan o retroceden.
- **Audio y subtítulos**, **Volumen automático** (Original, Equilibrado, Nocturno) y la información de reproducción
  están en los controles.
- **Saltar intro**, **Saltar créditos** y **Siguiente episodio** aparecen cuando corresponde.
- Mientras el reproductor está abierto, aunque esté en pausa, **tu perfil no se bloquea**. Si sales de la app (botón de
  inicio) o la TV la suspende, el tiempo de bloqueo empieza a contar; al volver, si ha pasado, se pide el PIN.
- Si el aparato no puede abrir un archivo, Velora lo intenta adaptado y, como último recurso, con el reproductor web.

En el móvil se usa el reproductor web, que pasa a pantalla completa cuando el sistema lo permite.

## 7. Actualizaciones

La app comprueba si hay una versión nueva al abrirse y de vez en cuando. Si la hay, la descarga y Android pide permiso
para instalarla. Solo acepta un APK con la **misma firma** que la instalada y cuya huella coincide con la publicada:
una app firmada por otra persona no puede sustituir a la tuya.

Las mejoras de la web llegan sin actualizar la app; los cambios del reproductor de la TV necesitan una versión nueva
del APK.

## 8. Cerrar sesión o retirar un dispositivo

**Perfil → Cerrar sesión** cierra la sesión y quita a ese aparato la autorización del hogar. El propietario también puede
**Retirar** el dispositivo desde **Mi hogar → Dispositivos del hogar**, por ejemplo si se pierde o se regala.
