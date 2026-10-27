# Build flags for a reproducible backend binary. Keep in sync with Dockerfile.
GOFLAGS_BUILD := -trimpath -buildvcs=false -ldflags="-s -w -buildid="
export CGO_ENABLED := 0

.PHONY: all build backend frontend test test-backend test-frontend vuln repro run dev clean

all: test build

build: backend frontend

backend:
	cd backend && go build $(GOFLAGS_BUILD) -o ../bin/filebunny ./cmd/filebunny

frontend: frontend/node_modules
	cd frontend && npm run build

frontend/node_modules: frontend/package-lock.json
	cd frontend && npm ci --ignore-scripts
	@touch $@

test: test-backend test-frontend

test-backend:
	cd backend && test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
	cd backend && go vet ./...
	cd backend && CGO_ENABLED=1 go test -race ./...

test-frontend: frontend/node_modules
	cd frontend && npm run check

# `go run pkg@version` ignores go.mod; force the module's toolchain.
vuln:
	cd backend && GOTOOLCHAIN=$$(go env GOVERSION) go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...

repro:
	./scripts/repro-check.sh

# Backend on :8080 with data in ./data; use `make dev` in another terminal.
run:
	cd backend && FILEBUNNY_DATA_DIR=../data go run ./cmd/filebunny

dev: frontend/node_modules
	cd frontend && npm run dev

clean:
	rm -rf bin frontend/dist
