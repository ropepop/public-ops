.PHONY: run test build tidy scrape \
	spacetime-install spacetime-build spacetime-publish-local \
	web-client-install web-client-build web-static-test clean-local \
	test-login-link docker-image-build

LDFLAGS := $(shell bash ./scripts/ldflags.sh)
SPACETIME ?= $(HOME)/.local/bin/spacetime
SPACETIME_SERVER ?= http://127.0.0.1:3100
SPACETIME_ENV ?= local

run:
	go run -ldflags "$(LDFLAGS)" ./cmd/bot

test: web-static-test
	go test ./...

build:
	go build -ldflags "$(LDFLAGS)" -o ./bin/train-bot ./cmd/bot

tidy:
	go mod tidy

scrape:
	go run ./cmd/scrape

test-login-link:
	go run ./cmd/test-login-link

spacetime-install:
	cd ./spacetimedb && npm install

spacetime-build:
	$(SPACETIME) build

spacetime-publish-local:
	$(SPACETIME) publish --server $(SPACETIME_SERVER) --yes --env $(SPACETIME_ENV)

web-client-install:
	cd ./web-client && npm install

web-client-build:
	./scripts/build_web_client.sh

web-static-test:
	node --test ./internal/web/static/app.test.js ./internal/web/static/external-feed.test.js

clean-local:
	rm -rf ./.artifacts ./output ./bin ./tmp ./node_modules ./web-client/node_modules ./spacetimedb/node_modules ./spacetimedb/dist
	find ./data/schedules -maxdepth 1 -type f -name '*.json' -delete

docker-image-build:
	docker build -f ../../infra/arbuzas/docker/images/train-bot.Dockerfile -t arbuzas/train-bot:dev ../../
