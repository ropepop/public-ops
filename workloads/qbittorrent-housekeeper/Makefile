.PHONY: test vet build check

test:
	go test ./...

vet:
	go vet ./...

build:
	go build ./cmd/qbittorrent-housekeeper

check: test vet build
