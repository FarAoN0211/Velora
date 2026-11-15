# Fichas y carátulas: credenciales de cada instalación

Velora se distribuye sin API de metadatos configurada. El propietario de cada servidor es responsable de obtener sus claves, aceptar las condiciones de uso, mantener sus cuotas y revocarlas cuando sea necesario. No copies las claves de otra instalación.

## TMDB

Crea una cuenta y solicita una clave API en [los ajustes API de TMDB](https://www.themoviedb.org/settings/api). Consulta su [documentación oficial](https://developer.themoviedb.org/docs/getting-started). Velora utiliza la clave API v3; introdúcela en el apartado TMDB de Metadatos. Se usa para identificación, datos de ficha, reparto, temporadas, colecciones e imágenes. No basta con activar Fanart o TVDB para reemplazar este flujo de identificación.

## Fanart.tv

Obtén tu clave de proyecto en [Fanart.tv](https://fanart.tv/get-an-api-key/). Añádela a Fichas y carátulas → Fuentes de imágenes → Fanart.tv. La clave personal es opcional. Ofrece distintos tipos de imagen, pero no todos están disponibles para todos los títulos. Las series necesitan un identificador TVDB; Velora intenta resolver identificadores externos a partir de TMDB cuando dispone de ellos.

## TheTVDB

Revisa [sus modalidades de API](https://thetvdb.com/api-information) y [documentación v4](https://thetvdb.github.io/v4-api/). Introduce una clave v4 propia y el PIN de suscriptor si lo exige tu modalidad. El PIN de TheTVDB es una credencial del proveedor y **no es el PIN de los perfiles de Velora**. Se utiliza aquí como fuente de imágenes adicional; no se promete acceso incluido ni gratuito.

## OMDb

Solicita una clave en [OMDb](https://www.omdbapi.com/apikey.aspx) y actívala en Fuentes de imágenes. Usa identificadores IMDb y aporta carteles; no ofrece logos, fondos o discos en esta integración. Su servicio especial de carteles de alta resolución no está integrado. Consulta tu plan para conocer los límites vigentes.

## OpenSubtitles

La integración utiliza [OpenSubtitles.com REST API](https://opensubtitles.stoplight.io/docs/opensubtitles-api), con clave API y cuenta cuando corresponda. Configúrala globalmente desde Metadatos → Subtítulos. No incluye credenciales compartidas ni una garantía de descargas ilimitadas. No utiliza el inicio de sesión antiguo de OpenSubtitles.org. Los subtítulos contenidos en el archivo o junto a él se priorizan; el proveedor externo es un complemento.

## Almacenamiento y comprobación

Las claves se guardan exclusivamente en el volumen config del servidor, dentro de `secrets`, con permisos restringidos. No se entregan completas de nuevo al cliente. Un campo vacío al guardar conserva la clave anterior; usa Eliminar configuración para retirarla. Las fuentes adicionales se comprueban al activarlas. La ausencia de resultados puede significar que faltan imágenes o identificadores, una cuota agotada o credenciales sin acceso, no necesariamente un fallo de Velora.

Nunca incluyas `config`, `.env`, copias de seguridad o capturas con claves en GitHub. Los términos, licencias y atribución de los datos de estos servicios son independientes de GPL-3.0-or-later.
