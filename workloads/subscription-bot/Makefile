.PHONY: run test build tidy docker-image-build

LDFLAGS := $(shell bash ./scripts/ldflags.sh)

run:
	go run -ldflags "$(LDFLAGS)" ./cmd/bot

test:
	go test ./...

build:
	go build -ldflags "$(LDFLAGS)" -o ./bin/subscription-bot ./cmd/bot

tidy:
	go mod tidy

docker-image-build:
	docker build -f ../../infra/arbuzas/docker/images/subscription-bot.Dockerfile -t arbuzas/subscription-bot:dev ../../
