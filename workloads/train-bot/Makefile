.PHONY: run test build tidy scrape \
	pixel-native-test pixel-native-build pixel-native-deploy pixel-native-validate pixel-native-all \
	pixel-bootstrap pixel-sync pixel-test pixel-build pixel-deploy pixel-autostart \
	pixel-refresh-runtime pixel-restart-train pixel-isolation-check \
	pixel-release-check pixel-public-smoke pixel-miniapp-smoke pixel-bot-smoke pixel-e2e \
	pixel-rollback-last pixel-rollback pixel-validate pixel-all

LDFLAGS := $(shell bash ./scripts/ldflags.sh)

run:
	go run -ldflags "$(LDFLAGS)" ./cmd/bot

test:
	go test ./...

build:
	go build -ldflags "$(LDFLAGS)" -o ./bin/train-bot ./cmd/bot

tidy:
	go mod tidy

scrape:
	go run ./cmd/scrape

pixel-native-test:
	./scripts/pixel/run_native_tests.sh

pixel-native-build:
	./scripts/pixel/prepare_native_release.sh

pixel-native-deploy:
	../../tools/pixel/redeploy.sh --scope train_bot

pixel-native-validate:
	./scripts/pixel/validate_prod_readiness.sh

pixel-native-all:
	./scripts/pixel/pixel_native_all.sh

pixel-bootstrap:
	@echo "pixel-bootstrap retired: use ../../tools/pixel/redeploy.sh --scope train_bot." >&2
	@exit 1

pixel-sync:
	@echo "pixel-sync retired: train-bot no longer syncs source to the device. Use make pixel-native-build." >&2
	@exit 1

pixel-test:
	@echo "pixel-test retired: use make pixel-native-test." >&2
	@exit 1

pixel-build:
	@echo "pixel-build retired: use make pixel-native-build." >&2
	@exit 1

pixel-deploy:
	@echo "pixel-deploy retired: use ../../tools/pixel/redeploy.sh --scope train_bot." >&2
	@exit 1

pixel-refresh-runtime:
	./scripts/pixel/refresh_runtime_assets.sh

pixel-restart-train:
	./scripts/pixel/restart_train_bot_runtime.sh

pixel-isolation-check:
	./scripts/pixel/restart_isolation_acceptance.sh

pixel-autostart:
	@echo "Autostart is orchestrator-owned. Run ../../tools/pixel/redeploy.sh --scope train_bot instead."

pixel-release-check:
	./scripts/pixel/release_check.sh

pixel-public-smoke:
	./scripts/pixel/public_smoke.sh

pixel-miniapp-smoke:
	./scripts/pixel/miniapp_smoke.sh

pixel-bot-smoke:
	./scripts/pixel/e2e_agent_browser.sh

pixel-e2e:
	./scripts/pixel/e2e_all.sh

pixel-rollback-last:
	./scripts/pixel/rollback_release.sh --last

pixel-rollback:
	./scripts/pixel/rollback_release.sh --sha "$$SHA"

pixel-validate:
	@echo "pixel-validate retired: use make pixel-native-validate." >&2
	@exit 1

pixel-all:
	@echo "pixel-all retired: use make pixel-native-all." >&2
	@exit 1
