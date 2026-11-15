# VELORA · imagen multietapa de producción.
# Multietapa: web (node) → binario (go) → ejecución (alpine + cliente de PostgreSQL 17 para las copias + FFmpeg).
# E1 usa FFmpeg solo para analizar (ffprobe) y remultiplexar con el audio a AAC; en E2 se añadirá intel-media-driver (iHD).

FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
COPY replica/design/tokens.css replica/design/tailwind.preset.cjs /src/replica/design/
RUN npm run tokens:check && npm run typecheck && npm run build

FROM golang:1.24-alpine AS server
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
ARG VERSION=0.22.2
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X velora/internal/api.Version=${VERSION}" -o /out/velora ./cmd/velora

# Misma versión de Alpine que la imagen de Desarrollo en la que se ejecutan todas las pruebas (FFmpeg 8, cliente de
# PostgreSQL 17). Alpine 3.20 no tiene postgresql17-client. El grupo 100 (users) ya existe en Alpine.
FROM alpine:3.23
RUN apk add --no-cache postgresql17-client ffmpeg intel-media-driver tzdata ca-certificates \
 && mkdir -p /config /backups /ota && chown 1000:100 /config /backups /ota \
 && ffmpeg -version >/dev/null && ffprobe -version >/dev/null && pg_dump --version >/dev/null
COPY --from=server /out/velora /usr/local/lib/velora/velora
COPY deploy/start-velora.sh /usr/local/bin/velora
RUN chmod 0755 /usr/local/bin/velora
COPY --from=web /src/web/dist /usr/share/velora/web
ENV VELORA_LISTEN=:8640 \
    VELORA_CONFIG_DIR=/config \
    VELORA_BACKUP_DIR=/backups \
    VELORA_WEB_DIR=/usr/share/velora/web \
    VELORA_TZ=Europe/Madrid
# UID 1000 (usuario del servidor) y GID 100 (users, grupo de los medios): architecture.md §10.
USER 1000:100
EXPOSE 8640
ENTRYPOINT ["/usr/local/bin/velora"]
CMD ["serve"]
