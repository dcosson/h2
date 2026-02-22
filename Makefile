build:
	go build -o h2 ./cmd/h2

test:
	go test ./...

test-coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	go tool cover -func=coverage.out

deps:
	go install honnef.co/go/tools/cmd/staticcheck@latest

check: fmt vet staticcheck

check-ci: fmt-check vet staticcheck

fmt:
	@echo "==> gofmt"
	gofmt -w .

fmt-check:
	@echo "==> gofmt (check)"
	@test -z "$$(gofmt -l .)" || (gofmt -l . && echo "above files are not formatted" && exit 1)

vet:
	@echo "==> go vet"
	go vet ./...

staticcheck:
	@echo "==> staticcheck"
	go run honnef.co/go/tools/cmd/staticcheck@latest ./...
