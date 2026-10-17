# Channel Tongues build. `make build` produces bin/tongues and extension/dist.

GO ?= go

.PHONY: build helper ext test serve mock clean

build: helper ext

helper:
	$(GO) build -o bin/tongues ./cmd/tongues

ext: extension/node_modules
	cd extension && npm run build

extension/node_modules: extension/package.json extension/package-lock.json
	cd extension && npm ci --no-audit --no-fund
	@touch extension/node_modules

test: extension/node_modules
	$(GO) test ./...
	cd extension && npm test

serve: helper
	bin/tongues serve

mock: helper
	bin/tongues serve --mock

clean:
	rm -r -f bin extension/dist
