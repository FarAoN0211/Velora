# VELORA · tareas de desarrollo. Se ejecutan EN EL SERVIDOR, dentro de la carpeta del proyecto.
# Todo corre en contenedores efímeros del proyecto Compose "velora-dev", sin puertos publicados.

DC := docker compose -p velora-dev -f deploy/compose.dev.yaml
GO := $(DC) run --rm go
NODE := $(DC) run --rm web

.PHONY: db tidy vet test build smoke web-install web-check dev-down dev-clean

db:            ## arranca solo la base de datos de pruebas
	$(DC) up -d --wait db

tidy: db       ## resuelve dependencias de Go
	$(GO) go mod tidy

vet: db
	$(GO) go vet ./...

test: db       ## pruebas unitarias y de integración (con PostgreSQL 17)
	$(GO) go test -count=1 ./...

build: db      ## compila el binario en server/bin/velora
	$(GO) go build -o bin/velora ./cmd/velora

smoke: build   ## base limpia + server y worker en la red interna + deploy/dev-go/smoke.sh
	mkdir -p .dev/media .dev/config .dev/backups
	$(DC) --profile app stop server worker
	$(DC) exec -T db psql -U velora -d postgres -qc "drop database if exists velora with (force)" -c "create database velora"
	$(DC) --profile app up -d server worker
	$(GO) sh /src/deploy/dev-go/smoke.sh; status=$$?; $(DC) --profile app stop server worker; exit $$status

web-install:
	$(NODE) npm ci

web-check:     ## tipos y build de la web
	$(NODE) sh -c 'npm run typecheck && npm run build'

dev-down:      ## para todo (conserva la base de pruebas)
	$(DC) --profile app --profile tools down

dev-clean:     ## para todo y borra los volúmenes de velora-dev
	$(DC) --profile app --profile tools down -v
