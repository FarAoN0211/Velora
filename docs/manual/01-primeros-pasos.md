# 1. Primeros pasos

## Instalar el servidor

Velora se instala con Docker Compose en un equipo de tu red (un miniPC, un NAS con Docker o un servidor Linux). Los
pasos detallados, los parámetros y cómo actualizar están en [Instalar y mantener Velora](../installation.md). En
resumen:

```sh
docker compose up -d --build
```

No exige un archivo `.env` previo: la contraseña de PostgreSQL se genera en un volumen privado y se conserva. Para personalizar las carpetas, copia `deploy/.env.example` a `deploy/.env`, ajusta `VELORA_MEDIA_PATH` y repite el comando. Conserva tu `.env` si actualizas desde una versión anterior.

Velora monta tu carpeta de medios **solo para lectura**: nunca modifica ni borra tus archivos.

## Asistente inicial

1. Abre `http://IP_DE_TU_SERVIDOR:8640` en un navegador.
2. Consulta el **código de instalación** en el registro del servidor
   (`docker compose logs server`). No es el código de seis dígitos de los
   dispositivos.
3. Elige el nombre del servidor y el idioma, y crea la cuenta de administrador (el usuario no puede llamarse `admin`
   ni `root`; la contraseña necesita al menos 10 caracteres).

El asistente también puede completarse desde el móvil escaneando el QR que aparece en la pantalla del servidor.

## Añadir bibliotecas

En **Perfil → Panel de control → Bibliotecas → Añadir biblioteca** elige el tipo (películas o series) y la carpeta. Las
rutas del servidor aparecen bajo `/host`: por ejemplo `/host/home/tu-usuario/Vídeos` o `/host/mnt/Disco/Peliculas`. El montaje `/media` también sigue disponible. Crea una biblioteca por tipo. El escaneo empieza solo y el catálogo se va llenando mientras dura.

- **Películas**: una película por carpeta o archivo. Si hay varias versiones (por ejemplo 1080p y 4K), Velora las agrupa
  en una sola ficha y comparte el progreso entre ellas.
- **Series**: una carpeta por serie, con temporadas y episodios reconocidos por su nombre (`S01E02`, `1x02`…).
- Una biblioteca **quitada** deja de verse, pero conserva fichas y progreso por si vuelves a añadirla.

Una cuenta nueva **no ve ninguna biblioteca** hasta que el administrador se la asigna (capítulo 5).

## Fichas, imágenes y subtítulos externos

Velora no trae claves de proveedores: cada instalación usa las suyas. Configúralas en **Panel de control → Metadatos**:

- **TMDB** (necesaria para identificar títulos, fichas, reparto, temporadas y colecciones).
- Opcionales para más imágenes: **Fanart.tv**, **TheTVDB** y **OMDb**.
- **OpenSubtitles.com** para buscar subtítulos cuando no hay ninguno junto al archivo.

Cómo obtener cada clave: [Fichas y carátulas: credenciales de cada instalación](../providers.md). Las claves se guardan
solo en tu servidor y nunca se devuelven completas a la web.

## Acceso desde fuera de casa

El Compose predeterminado usa HTTP y está pensado para tu red local. Para usar Velora fuera de casa hay dos opciones
seguras: entrar a tu red con una VPN (por ejemplo WireGuard) o poner Velora detrás de un proxy HTTPS. **No abras el
puerto 8640 directamente a Internet.** Detalles en [Instalar y mantener Velora](../installation.md) y en
[Seguridad](../../SECURITY.md).
