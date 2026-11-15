# Imagen de desarrollo de VELORA (solo para compilar y probar; no es la imagen de producción).
# golang:1.24-alpine + cliente de PostgreSQL 17 (pg_dump/pg_restore para las pruebas de copias de seguridad).
FROM golang:1.24-alpine
# ffmpeg/ffprobe (E1): análisis de ficheros y Direct Stream con el audio convertido. Solo dentro del contenedor.
RUN apk add --no-cache postgresql17-client git ffmpeg intel-media-driver
# Las cachés de Go viven en .dev/ dentro de la carpeta del proyecto; el contenedor corre como 1000:1000.
ENV GOMODCACHE=/src/.dev/gomod \
    GOCACHE=/src/.dev/gocache \
    GOFLAGS=-buildvcs=false \
    CGO_ENABLED=0
WORKDIR /src/server
