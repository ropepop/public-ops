.PHONY: test build tidy spacetime-install spacetime-build spacetime-sidecar-build spacetime-sidecar-test web-client-install web-client-build docker-image-build clean-local

SPACETIME ?= $(HOME)/.local/bin/spacetime

test:
	go test ./...
	cd ./spacetime-sidecar && cargo test

build:
	go build -o ./bin/ticket-remote ./cmd/ticket-remote

spacetime-sidecar-build:
	cd ./spacetime-sidecar && cargo build

spacetime-sidecar-test:
	cd ./spacetime-sidecar && cargo test

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
	rm -rf ./bin ./spacetimedb/node_modules ./spacetimedb/dist ./web-client/node_modules ./web-client/src/generated ./spacetime-sidecar/target
