.PHONY: build

build:
	tinygo build -o plugin.wasm -target wasip1 -buildmode=c-shared .
	zip -j navidrome-bpm-plugin.ndp manifest.json plugin.wasm
