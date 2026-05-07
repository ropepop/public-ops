.PHONY: run test build tidy spacetime-install spacetime-build web-client-install web-client-build docker-image-build clean-local

SPACETIME ?= $(HOME)/.local/bin/spacetime

run:
	TICKET_REMOTE_AUTH_MODE=dev go run ./cmd/ticket-remote

test:
	go test ./...

build:
	go build -o ./bin/ticket-remote ./cmd/ticket-remote

tidy:
	go mod tidy

spacetime-install:
	cd ./spacetimedb && npm install

spacetime-build:
	cd ./spacetimedb && HOME=$(HOME) npm run build

web-client-install:
	cd ./web-client && npm install

web-client-build:
	cd ./web-client && PATH="$(HOME)/.local/bin:$(PATH)" npm run build

docker-image-build:
	docker build -f ../../infra/arbuzas/docker/images/ticket-remote.Dockerfile -t arbuzas/ticket-remote:dev ../../

clean-local:
	rm -rf ./bin ./spacetimedb/node_modules ./spacetimedb/dist ./web-client/node_modules ./web-client/src/generated
