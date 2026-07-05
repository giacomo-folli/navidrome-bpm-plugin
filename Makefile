.PHONY: build

build:
	GOOS=wasip1 GOARCH=wasm go build -o plugin.wasm -buildmode=c-shared .
	zip -j navidrome-bpm-plugin.ndp manifest.json plugin.wasm
