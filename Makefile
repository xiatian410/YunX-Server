.PHONY: frontend provider build test check
frontend:
	npm --prefix web ci
	npm --prefix web run build
provider:
	npm --prefix provider ci --omit=dev
	./provider/gradlew -p provider --no-daemon test fatJar
build: frontend provider
	mkdir -p release/provider
	CGO_ENABLED=0 go build -trimpath -o release/yunx-server ./cmd/yunx-server
	cp provider/build/libs/yunx-provider.jar release/provider/provider.jar
	cp provider/browser.mjs provider/package*.json release/provider/
	npm --prefix release/provider ci --omit=dev
	cp LICENSE NOTICE.md release/
test: frontend provider
	CGO_ENABLED=0 go test ./... -count=1
	CGO_ENABLED=0 go vet ./...
	npm --prefix web test
check:
	python3 scripts/check-freeze.py
	python3 scripts/check-provider.py
	git diff --check
