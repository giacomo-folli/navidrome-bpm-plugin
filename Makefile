.PHONY: test build run

GO_ENV = CGO_ENABLED=1 GOCACHE=/tmp/navidrome-bpm-gocache GOMODCACHE=/tmp/navidrome-bpm-gomodcache

test:
	$(GO_ENV) go test ./...

build:
	$(GO_ENV) go build -buildvcs=false ./cmd/navidrome-bpm-plugin

run:
	NBDPM_CACHE_PATH=./config/cache.sqlite $(GO_ENV) go run ./cmd/navidrome-bpm-plugin
