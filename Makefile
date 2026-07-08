BINARY := screener
PKG := ./cmd/screener

.PHONY: build test run release

build:
	go build -o $(BINARY) $(PKG)

test:
	go test ./...

run:
	go run $(PKG) -config config.example.yaml

release:
	cd web && npm ci && npm run build
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o $(BINARY) $(PKG)
