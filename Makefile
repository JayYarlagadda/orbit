.PHONY: generate fmt check-fmt vet test test-race build-go configure-cpp build-cpp test-cpp verify

generate:
	powershell.exe -NoProfile -ExecutionPolicy Bypass -File scripts/generate.ps1

fmt:
	gofmt -w .

check-fmt:
	@test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)

vet:
	go vet ./...

test:
	go test -count=1 ./...

test-race:
	go test -race -count=1 ./...

build-go:
	mkdir -p build/go
	go build -o build/go/ ./cmd/...

configure-cpp:
	cmake --preset default

build-cpp: configure-cpp
	cmake --build --preset default

test-cpp: build-cpp
	ctest --preset default

verify: check-fmt vet test-race build-go test-cpp
