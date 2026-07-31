.PHONY: build test clean release skills-version bundle-skills cut-release

BINARY_NAME=fused-cli
VERSION?=v0.1.0

build:
	go build -o $(BINARY_NAME) main.go

test:
	go test ./...

clean:
	go clean
	rm -f $(BINARY_NAME)

# Snapshots skills/dev/ into skills/<VERSION without the leading v>/ so the
# release binary embeds the same skill content it fetches at runtime.
skills-version:
	@ver=$$(echo $(VERSION) | sed 's/^v//'); \
	if [ -d "skills/$$ver" ]; then \
		rm -rf "skills/$$ver"; \
	fi; \
	cp -r skills/dev "skills/$$ver"; \
	echo "Created/updated skills/$$ver from skills/dev."; \
	echo "Next: git add skills/$$ver && git commit && git push origin main, then make cut-release VERSION=$(VERSION)"

bundle-skills: skills-version

# Creates a git tag and pushes it to trigger GoReleaser via GitHub Actions
release:
	@echo "Creating and pushing tag $(VERSION)"
	git tag $(VERSION)
	git push origin $(VERSION)

# One-shot release tag push. Copies skills/dev into skills/<version>, then
# creates and pushes the release tag.
cut-release:
	$(MAKE) skills-version VERSION=$(VERSION)
	$(MAKE) release VERSION=$(VERSION)
