.PHONY: build test clean release

BINARY_NAME=fused-cli
VERSION?=v0.1.0

build:
	go build -o $(BINARY_NAME) main.go

test:
	go test ./...

clean:
	go clean
	rm -f $(BINARY_NAME)

# Creates a git tag and pushes it to trigger GoReleaser via GitHub Actions
release:
	@echo "Creating and pushing tag $(VERSION)"
	git tag $(VERSION)
	git push origin $(VERSION)
