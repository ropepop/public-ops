.PHONY: build test reporter-test deploy-integration-test

build:
	spacetime build

reporter-test:
	bash ./scripts/report_test.sh

deploy-integration-test:
	bash ./scripts/deploy_integration_test.sh

test:
	cargo test --manifest-path ./spacetimedb/Cargo.toml
	$(MAKE) reporter-test
	$(MAKE) deploy-integration-test
