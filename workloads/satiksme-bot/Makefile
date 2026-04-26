.PHONY: run test test-go test-js live-client-build build catalog-sync spacetime-sync spacetime-build spacetime-publish tidy docker-image-build

LDFLAGS := $(shell bash ./scripts/ldflags.sh)

run:
	go run -ldflags "$(LDFLAGS)" ./cmd/bot

test: test-go test-js

test-go:
	go test ./...

test-js:
	node --test ./internal/web/static/*.test.js

live-client-build:
	cd ./web-client && npm run build

build:
	go build -ldflags "$(LDFLAGS)" -o ./bin/satiksme-bot ./cmd/bot

catalog-sync:
	go run ./cmd/catalogsync

spacetime-sync:
	go run ./cmd/spacetime-sync

spacetime-build:
	cd ./spacetimedb && npm run build

spacetime-publish:
	cd ./spacetimedb && npm run publish

tidy:
	go mod tidy

docker-image-build:
	docker build -f ../../infra/arbuzas/docker/images/satiksme-bot.Dockerfile -t arbuzas/satiksme-bot:dev ../../
