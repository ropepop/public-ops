.PHONY: test build tidy spacetime-install spacetime-build spacetime-test activation-report spacetime-sidecar-bindings spacetime-sidecar-bindings-check spacetime-sidecar-build spacetime-sidecar-test web-client-install web-client-build client-hdr-real-gpu-serve docker-image-build clean-local

SPACETIME ?= $(HOME)/.local/bin/spacetime

test: spacetime-sidecar-bindings-check spacetime-test
	cd ./web-client && npm run test:experimental-hdr-preference
	cd ./web-client && npm run test:client-hdr-boost-preference
	cd ./web-client && npm run test:client-hdr
	cd ./web-client && npm run test:ticket-actions-v3
	cd ./web-client && npm run test:relay-current-report
	cd ./web-client && npm run test:admin-schedule
	cd ./web-client && npm run test:admin-statistics
	cd ./web-client && npm run test:admin-vivi-auth
	go test ./...
	cd ./spacetime-sidecar && cargo test

build:
	go build -o ./bin/ticket-remote ./cmd/ticket-remote

spacetime-sidecar-build:
	cd ./spacetime-sidecar && cargo build

spacetime-sidecar-test: spacetime-sidecar-bindings-check
	cd ./spacetime-sidecar && cargo test

tidy:
	go mod tidy

spacetime-install:
	cd ./spacetimedb && npm install

spacetime-build:
	cd ./spacetimedb && HOME=$(HOME) npm run build

spacetime-test:
	cd ./spacetimedb && cargo test

activation-report:
	./scripts/report-activation-ledger.sh

spacetime-sidecar-bindings:
	$(SPACETIME) generate --lang rust --out-dir ./spacetime-sidecar/src/module_bindings --module-path ./spacetimedb --yes

spacetime-sidecar-bindings-check:
	@bindings_tmp="$$(mktemp -d)"; \
	trap 'rm -rf "$$bindings_tmp"' EXIT; \
	$(SPACETIME) generate --lang rust --out-dir "$$bindings_tmp" --module-path ./spacetimedb --yes; \
	diff -ru ./spacetime-sidecar/src/module_bindings "$$bindings_tmp"

web-client-install:
	cd ./web-client && npm install

web-client-build:
	cd ./web-client && PATH="$(HOME)/.local/bin:$(PATH)" npm run build

client-hdr-real-gpu-serve:
	cd ./web-client && npm run test:client-hdr-real-gpu:serve

docker-image-build:
	docker build -f ../../infra/arbuzas/docker/images/ticket-remote.Dockerfile -t arbuzas/ticket-remote:dev ../../

clean-local:
	rm -rf ./bin ./spacetimedb/node_modules ./spacetimedb/dist ./web-client/node_modules ./web-client/src/generated ./spacetime-sidecar/target
