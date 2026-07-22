.PHONY: build test reporter-test deploy-integration-test migration-test inventory-test

build:
	spacetime build

reporter-test:
	bash ./scripts/report-deployment_test.sh

deploy-integration-test:
	bash ./scripts/deploy_integration_test.sh

migration-test:
	bash ./scripts/migrate-retained-history_test.sh

inventory-test:
	bash ./scripts/inventory-private-log-sources_test.sh

test:
	cargo test --manifest-path ./spacetimedb/Cargo.toml
	$(MAKE) reporter-test
	$(MAKE) deploy-integration-test
	$(MAKE) migration-test
	$(MAKE) inventory-test
