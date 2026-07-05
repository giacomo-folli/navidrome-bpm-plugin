BINARY = bpmd

build:
	go build -o $(BINARY) .

test:
	go test ./...

install: build
	install -Dm755 $(BINARY) /usr/local/bin/$(BINARY)

clean:
	rm -f $(BINARY)

.PHONY: build test install clean
