.PHONY: frontend build test check
frontend:
	npm --prefix web ci
	npm --prefix web run build
build: frontend
	mkdir -p release
	CGO_ENABLED=0 go build -trimpath -o release/yunx-server ./cmd/yunx-server
test: frontend
	CGO_ENABLED=0 go test ./... -count=1
	CGO_ENABLED=0 go vet ./...
	npm --prefix web test
check:
	python3 scripts/check-freeze.py
	git diff --check
