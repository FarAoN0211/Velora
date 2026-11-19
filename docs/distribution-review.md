# Revisión de distribución · 2026-11-15

La publicación inicial se hizo en un único commit (`c4f6c36`, «Velora 0.22.0 Beta») con una exportación por lista explícita y un manifiesto SHA256. El repositorio incluye código de servidor, web y Android, diseño necesario para compilar, pruebas, CI y documentación; no incluye los datos, volúmenes, API, credenciales o historial de la instalación privada.

## Incidencia de estilos

La lista del exportador omitía `web/postcss.config.cjs`. Vite podía terminar correctamente conservando las directivas `@tailwind` sin generar las utilidades. La comprobación de tipos y los tokens no detectaban este fallo. Por eso el JavaScript servido por ambas instalaciones era idéntico pero su CSS difería:

| Archivo | Distribución inicial | Referencia y distribución corregida |
| --- | --- | --- |
| CSS | 11.811 bytes | 46.315 bytes |
| SHA256 CSS | `8834d3a46cfa7a8a3016b553f2e46c56046a7b67a0180c54353cf2bec3b946a1` | `fc79b938df82c26775836259e887f5e3f95ee85f32817a90a93fa4847b479986` |

Comparación del código: los349 archivos comunes de web, servidor, Android y despliegue coincidían byte a byte. Las demás omisiones de ese conjunto eran herramientas de desarrollo/diagnóstico Android, no código necesario para mostrar la interfaz. No se sustituyeron pantallas ni se simplificó el diseño para la distribución.

## Correcciones 0.22.1

- Configuración PostCSS incluida tanto en Git como en el exportador.
- La compilación web falla si encuentra Tailwind sin procesar o faltan utilidades, tokens o la variante TV. Se ha comprobado que rechaza el CSS defectuoso.
- CI exporta una copia nueva y compila esa copia, para detectar omisiones en el paquete distribuido.
- `compose.yaml` en la raíz permite `docker compose up -d --build` sin crear previamente un `.env` ni ejecutar Python.
- Un servicio de inicialización crea una contraseña PostgreSQL aleatoria y persistente en el volumen credentials, que solo se monta en los contenedores que la necesitan. Cada instalación conserva sus propias claves y datos.
- Se mantiene la lectura de un `deploy/.env` existente y los nombres de los volúmenes, para actualizar sin borrar cuentas, bibliotecas o progreso.

El aspecto de la interfaz se reproduce con los mismos assets; el contenido de las bibliotecas, metadatos descargados, preferencias, GPU y credenciales pertenecen a cada instalación. La verificación de referencia se realizó exclusivamente mediante lecturas HTTP y comparación de archivos; no se modificó ni reinició la instalación de referencia.

## Validación de cierre

Comprobaciones realizadas en un proyecto Docker temporal separado, sin reutilizar los volúmenes de ninguna instalación real:

- Arranque desde la raíz sin `.env`, usando `docker compose up -d --build`.
- CSS y JavaScript servidos idénticos byte a byte a los de referencia, con los SHA256 indicados.
- Asistente inicial, cuenta de prueba, biblioteca en una subcarpeta de medios y proveedores sin credenciales.
- Reinicio conservando credenciales y sesión al cargar un `.env` heredado.
- Rechazo de una contraseña distinta sin modificar el secreto almacenado.
- Comando administrativo `docker compose exec server velora migrate status` funcional con el secreto automático.
- Compilación desde una exportación nueva, verificación de tokens/tipos y rechazo explícito del CSS defectuoso.

No se han actualizado ni recreado las instalaciones reales. Para corregir una instalación existente basta descargar esta versión y reconstruirla conservando configuración y volúmenes.

## 0.22.2: carpetas del servidor y APK descargable

El Compose exportado solo montaba una carpeta `media` del proyecto, vacía en una instalación nueva. El explorador no podía mostrar los discos y directorios del anfitrión. La distribución añade `/host` en solo lectura, con la raíz del servidor Linux como origen predeterminado, y mantiene `/media` para no cambiar las rutas de bibliotecas existentes. Se puede limitar el origen con `VELORA_HOST_PATH`; UID y GID son configurables y se mantienen los permisos del sistema. El selector explica los montajes y muestra la ruta de cada carpeta. No se cambian permisos ni archivos del anfitrión.

Se ofrece el APK Android 0.8.1 (código 29), firmado con la identidad de publicación existente, junto con un anuncio OTA y SHA256. La clave de firma no forma parte del código, del APK ni de los archivos de la release. La aplicación sigue sin servidor personal predeterminado. Los administradores pueden descargar el APK sin compilarlo y alojar las actualizaciones en su propio servidor.

Validación de 0.22.2: instalación Docker aislada con volúmenes nuevos; alta inicial; exploración de `/host/home`, `/host/mnt` y subcarpetas; creación de una biblioteca en el montaje del anfitrión; intento de escritura rechazado por el sistema de archivos de solo lectura. La web supera comprobación de tipos, tokens y CSS compilado. El APK release supera compilación, lint y verificación de firma. Se corrige también la compilación duplicada y concurrente de una misma imagen: solo server la construye y worker la reutiliza. GitHub Actions instala `platform-tools` sin solicitar el antiguo paquete `tools`, que ya no está disponible.

## 0.22.3: margen de preparación para conexiones variables

Se encontró un límite explícito de entrada de FFmpeg: `-readrate 1.15 -readrate_initial_burst 5`. Limita la preparación a 1,15 veces el tiempo real después de un adelanto inicial de 5 segundos. Esto deja poco margen para recuperar la reserva del cliente y queda por debajo de la velocidad de reproducción 2× que permite la web. Se cambia a un ritmo normal de 3× con un adelanto inicial de 20 segundos y recuperación explícita de hasta 6×, sin cambiar resolución, bitrate, códecs, límites de hilos ni concurrencia. La velocidad efectiva sigue dependiendo del equipo, del origen y de la lectura del cliente. No se promete un búfer constante: el navegador decide cuánto lee.

Los flujos generados añaden `X-Accel-Buffering: no` para solicitar entrega inmediata a proxies compatibles. No modifica el funcionamiento de una VPN ni sustituye las pruebas de la conexión real. Referencia de parámetros: [documentación oficial de FFmpeg](https://ffmpeg.org/ffmpeg.html#Main-options).

Validado con FFmpeg real: planificación y recursos; conversión H.264; conservación exacta de paquetes de audio compatible; rechazo de tickets alterados y de otra sesión; salida autorizada y cabecera de streaming. Comparación sintética de ritmo: en FFmpeg 6.1, tras 8 segundos se preparan aproximadamente 14 segundos con los límites anteriores y 44 con los nuevos. En FFmpeg 8, después de su arranque efectivo, la prueba final mide aproximadamente 9,7 segundos con los parámetros anteriores y 44,1 con los nuevos en 8 segundos. La inspección del código oficial de FFmpeg 8 confirmó que el ritmo predeterminado de recuperación, próximo al de lectura, retrasa también el llenado del adelanto inicial; se configura `-readrate_catchup 6` explícitamente. Son pruebas de preparación del archivo, no mediciones de red o del dispositivo del usuario. La reproducción mediante VPN en el aparato real y la aceleración VAAPI quedan pendientes de validación.

No hay cambios nativos Android: sigue vigente el APK 0.8.1 (29). Esta corrección solo llega a los clientes cuando se actualiza su servidor Velora.

## 0.22.4: origen al identificar una ficha

El diálogo de identificación muestra los nombres originales del catálogo junto a los datos de búsqueda. Las películas muestran biblioteca, carpeta relativa y nombre de cada archivo principal; los extras no se confunden con versiones de la película. Las series muestran su propia carpeta, sin nombres de episodios. Se indican archivos no disponibles y las películas pendientes conservan su carpeta aunque no tengan archivo principal. El bloque permanece visible al consultar resultados y confirmar una identificación.

La respuesta administrativa de metadatos añade `sources` (biblioteca, carpeta relativa, archivo opcional y disponibilidad). Se consulta exclusivamente el catálogo: no se abre el contenido ni se inicia una lectura remota. El acceso sigue reservado a administradores; no se exponen rutas absolutas del anfitrión. No hay migraciones ni cambios de APK.

Validación de 0.22.4: pruebas API de película con identificación incorrecta, versiones múltiples y archivo ausente, restricción a administradores y carpeta de serie sin episodios. Regresión de edición/candados correcta. Web: tipos, tokens, compilación CSS y revisión visual real en Chrome de escritorio y móvil con datos ficticios.

## 0.22.5: identificación independiente mediante nombres de episodios

Cuando una serie sin TMDB asignado no obtiene resultados por el nombre de su carpeta, se consulta el catálogo de episodios. Se extrae solo el prefijo anterior a SxxExx/1xNN, nunca el título del episodio posterior al marcador. Deben coincidir al menos dos episodios distintos, sin prefijos contradictorios; las versiones de un episodio no multiplican la evidencia. Se acepta únicamente un resultado cuyo título original o localizado coincida exactamente tras normalizar puntuación/acentos y cuyo año, si se conoce, corresponda. Si no hay evidencia suficiente o hay ambigüedad, no se identifica automáticamente.

No consulta Sonarr ni ninguna aplicación externa: solo nombres ya catalogados y el proveedor TMDB configurado por el propietario. No abre vídeos, cambia carpetas ni añade servicios de traducción. Las identidades existentes se conservan y la aplicación de metadatos mantiene su protección frente a cambios concurrentes. El diálogo administrativo muestra los títulos deducidos y un botón para usarlos en una búsqueda manual.

Pruebas: extracción del prefijo, formato 1xNN/SxxExx y normalización; identificación correcta con alias de carpeta sin resultados; rechazo de resultados ambiguos, prefijos contradictorios y versiones de un único episodio; conservación del identificador existente; sugerencias por la API; regresión del origen de películas/series. Todas ejecutadas en entorno aislado con proveedor simulado y sin aplicaciones auxiliares. Web: tipos y compilación correctos. No hay migraciones ni APK nuevo.


## 0.22.6: reproductor nativo en todos los APK

APK 0.8.2 (30), Media3 compartido por TV, móvil y tablet. Se elimina la restricción de TV en el puente Android y en la web. Los APK antiguos que rechazan la apertura conservan el reproductor web; web=1 mantiene el respaldo tras errores. Tocar el vídeo muestra los botones sin pausar y respeta los avisos de intro/créditos. Sin cambios de política de códecs, rutas multimedia ni credenciales. La firma privada no se distribuye.

Validación: 117 pruebas Android correctas, incluidas apertura nativa en móvil y toque sin pausa/respeto de avisos. Web: typecheck, tokens, build y comprobación del CSS correctos. Pendiente la prueba física del TBox UHD, que no expone ADB actualmente.
