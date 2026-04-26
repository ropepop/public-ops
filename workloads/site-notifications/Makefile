.PHONY: test docker-image-build

test:
	PYTHONPATH=. pytest -q

docker-image-build:
	docker build -f ../../infra/arbuzas/docker/images/site-notifications.Dockerfile -t arbuzas/site-notifications:dev ../../
