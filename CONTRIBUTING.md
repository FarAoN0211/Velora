# Desarrollo

Requisitos: Docker Compose v2, Node22 para desarrollo web y JDK17/Android SDK35 para Android. Go1.24 o superior es necesario por el uso de os.Root. No hace falta ninguna API real para las pruebas: los proveedores se simulan.

```sh
export VELORA_DEV_UID=$(id -u) VELORA_DEV_GID=$(id -g)
make db
make vet
make test
make web-install
make web-check
```

El Compose de desarrollo no publica puertos y utiliza una base desechable separada. `make dev-down` conserva sus datos; `make dev-clean` los borra. No lo apuntes a una base de producción. También puedes ejecutar `cd server && go test ./...` con `VELORA_TEST_DATABASE_URL` apuntando a PostgreSQL17 de pruebas y FFmpeg/ffprobe y pg_dump/pg_restore17 disponibles. Sin esa variable algunas pruebas de integración se omiten; eso no equivale a haberlas pasado.

```sh
cd clientes/android
./gradlew :app:testDebugUnitTest :app:assembleDebug
```

Mantén las políticas de autorización y OpenAPI sincronizados. Los cambios en acceso, hogares o sesiones necesitan pruebas de aislamiento, revocación y concurrencia. No añadas claves reales a fixtures; usa respuestas simuladas y direcciones de ejemplo. No se aceptan datos de instalaciones personales, volcados de biblioteca, APK firmadas con claves privadas ni configuraciones de proveedores.

El código se distribuye bajo GPL-3.0-or-later. Conserva los avisos y licencias de dependencias y documenta las variables de entorno y migraciones nuevas.

La compilación web incluye una comprobación de CSS generado: no se aceptan directivas Tailwind sin procesar ni la ausencia de utilidades/variante TV. El flujo de distribución de CI exporta desde cero y vuelve a compilar esa copia; las comprobaciones de tipos por sí solas no detectan una configuración PostCSS omitida.
