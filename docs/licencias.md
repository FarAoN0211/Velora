# Matriz de licencias · E0 (versiones reales)

Fecha: 2026-10-25. Inventario de dependencias y sus condiciones de distribución. Licencia del código de VELORA: **GPL-3.0-or-later**, texto completo en LICENSE.

## Servidor (Go 1.24, `server/go.mod`)

| módulo | versión | licencia | uso | compatible con GPL-3.0+ |
| --- | --- | --- | --- | --- |
| Go (biblioteca estándar) | 1.24.13 | BSD-3-Clause | compilado | sí |
| github.com/go-chi/chi/v5 | v5.2.2 | MIT | router HTTP | sí |
| github.com/google/uuid | v1.6.0 | BSD-3-Clause | identificadores | sí |
| github.com/jackc/pgx/v5 (+ pgpassfile, pgservicefile, puddle) | v5.7.5 | MIT | PostgreSQL | sí |
| github.com/pressly/goose/v3 | v3.24.3 | MIT | migraciones | sí |
| github.com/robfig/cron/v3 | v3.0.1 | MIT | tareas programadas | sí |
| golang.org/x/crypto | v0.41.0 | BSD-3-Clause | argon2id | sí |
| golang.org/x/image | v0.25.0 | BSD-3-Clause | decodificar WebP y reescalar imágenes subidas | sí |
| gopkg.in/yaml.v3 | v3.0.1 | MIT + Apache-2.0 | solo en pruebas (lectura de openapi.yaml) | sí |
| github.com/sethvargo/go-retry | v0.3.0 | Apache-2.0 | indirecta (goose) | sí (Apache-2.0 es compatible con GPL-3.0) |
| go.uber.org/multierr, mfridman/interpolate | v1.11.0 / v0.0.2 | MIT | indirectas (goose) | sí |
| golang.org/x/sync, golang.org/x/sys | v0.16.0 / v0.35.0 | BSD-3-Clause | indirectas | sí |

Las versiones están fijadas para Go 1.24: las últimas de `x/crypto` y `goose` ya exigen Go 1.26.

## Web (`web/package-lock.json`, 190 paquetes)

| paquete | versión | licencia | ¿se distribuye en la web? |
| --- | --- | --- | --- |
| react, react-dom | 18.3.1 | MIT | sí |
| react-router-dom | 6.28.0 | MIT | sí |
| lucide-react (iconos) | 0.460.0 | ISC | sí (solo los iconos usados) |
| @fontsource-variable/inter (tipografía) | 5.1.0 | SIL OFL 1.1 | sí (ficheros woff2) |
| vite, @vitejs/plugin-react, tailwindcss, postcss, autoprefixer | 6.0.3 / 4.3.4 / 3.4.15 / 8.4.49 / 10.4.20 | MIT | no (compilación) |
| typescript, didyoumean, ts-interface-checker, baseline-browser-mapping | — | Apache-2.0 | no (compilación) |
| source-map-js | 1.2.2 | BSD-3-Clause | no (compilación) |
| caniuse-lite | 1.0.30001819 | CC-BY-4.0 (datos) | no (compilación; datos de compatibilidad de navegadores) |

Árbol completo: 171 MIT, 12 ISC, 4 Apache-2.0, 1 OFL-1.1, 1 CC-BY-4.0 y 1 BSD-3-Clause.

## Imágenes de contenedor

| imagen | uso | licencias relevantes |
| --- | --- | --- |
| postgres:17-alpine | base de datos | PostgreSQL License |
| golang:1.24-alpine (+ postgresql17-client) | compilar y probar (solo desarrollo) | BSD-3-Clause (Go), PostgreSQL License |
| node:22-alpine | compilar la web (solo desarrollo) | MIT (Node.js) |
| alpine:3.20 (+ postgresql17-client, tzdata) | base de la imagen de producción (no construida aún) | varias permisivas; Alpine/BusyBox GPL-2.0 en el sistema base |

## Conclusión

**Ninguna dependencia de E0 impide la GPL-3.0-or-later.** Lo que se distribuye en la web (React, iconos e Inter) exige conservar
sus avisos de licencia: se mostrarán en "Acerca de" cuando exista (E1). La OFL de Inter permite empaquetar la fuente con la
aplicación. FFmpeg, Chromaprint, Tesseract y whisper.cpp entrarán en etapas posteriores y se añadirán aquí con sus versiones exactas.

## Fuentes de imágenes adicionales

Velora consulta las API oficiales con las credenciales propias configuradas por el administrador. Las claves permanecen en `secrets/image-providers.json` con modo 0600 y nunca se devuelven a los clientes. La procedencia se muestra en la galería. Los derechos de las imágenes pertenecen a sus titulares.

- Fanart.tv: clave de proyecto y, opcionalmente, clave personal. API v3.2 para carteles, logos, arte transparente, fondos, pancartas, discos y miniaturas: https://api.fanart.tv/ y https://fanart.tv/get-an-api-key/ .
- TheTVDB: API v4, clave y PIN si la modalidad de acceso lo exige; tipos de arte obtenidos de `/artwork/types`, no de un catálogo numérico fijo. Documentación: https://thetvdb.github.io/v4-api/ .
- OMDb: API de metadatos para obtener el cartel por IMDb. No se utiliza su Poster API de pago, ni se anuncia soporte de logos/fondos/discos de OMDb. Documentación: https://www.omdbapi.com/ .
- TMDB mantiene su configuración y atribución anteriores. Las imágenes están etiquetadas por idioma; una etiqueta española no siempre identifica la variante de España. Se prioriza es-ES si se distingue y después es.

### Acceso rápido por QR

- `qrcode` (web): MIT, https://github.com/soldair/node-qrcode.
- ZXing Android Embedded 4.3.0 y ZXing Core 3.3.0 (lector local en Android): Apache-2.0, https://github.com/journeyapps/zxing-android-embedded y https://github.com/zxing/zxing.
- AndroidX AppCompat 1.7.0: Apache-2.0, https://developer.android.com/jetpack/androidx/releases/appcompat.
