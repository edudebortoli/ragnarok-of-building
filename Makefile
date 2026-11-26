.PHONY: run test vet build web web-serve

run:
	go run ./cmd/app

test:
	go test ./...

vet:
	go vet ./...
	GOOS=js GOARCH=wasm go vet ./internal/... ./cmd/web

# The Windows program as released (icon, version, zip in dist/).
build:
	go run ./tools/buildexe

# The browser version: a static site in dist/web.
web:
	mkdir -p dist/web
	GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o dist/web/app.wasm ./cmd/web
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" dist/web/
	cp web/index.html dist/web/
	cp cmd/app/icon.png dist/web/icon.png
	cp data/gen/catalog.json.gz dist/web/

# Serve dist/web on http://localhost:8080.
web-serve: web
	go run ./tools/webserve
