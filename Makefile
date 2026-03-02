VERSION_PKG := h2/internal/version
GIT_REF ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
RELEASE ?= false
LDFLAGS := -X '$(VERSION_PKG).GitRef=$(GIT_REF)' -X '$(VERSION_PKG).ReleaseBuild=$(RELEASE)'

build:
	go build -ldflags "$(LDFLAGS)" -o h2 ./cmd/h2

build-release:
	$(MAKE) build RELEASE=true

test:
	go test $$(go list ./... | grep -v '^h2/e2etests$$')

test-e2e:
	go test ./e2etests

test-coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	go tool cover -func=coverage.out

deps:
	go install honnef.co/go/tools/cmd/staticcheck@latest

loc:
	scc --no-gen --exclude-dir .git,.beads,.claude,.github,docs,qa .

check: fmt
	@echo "==> go vet"
	go vet ./...
	@echo "==> staticcheck"
	go run honnef.co/go/tools/cmd/staticcheck@latest ./...

check-nofix: fmt-nofix
	@echo "==> go vet"
	go vet ./...
	@echo "==> staticcheck"
	go run honnef.co/go/tools/cmd/staticcheck@latest ./...

fmt:
	@echo "==> gofmt"
	gofmt -w .

fmt-nofix:
	@echo "==> gofmt (nofix)"
	@test -z "$$(gofmt -l .)" || (gofmt -l . && echo "above files are not formatted" && exit 1)

fmt-check: fmt-nofix
