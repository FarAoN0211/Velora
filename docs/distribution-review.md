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
