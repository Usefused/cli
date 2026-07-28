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
# release binary embeds the same skill content it fetches at runtime. This is
# an explicit prep step, not part of cut-release, so releases don't create a
# hidden "skills:" commit while your working tree has unrelated changes.
skills-version:
	@ver=$$(echo $(VERSION) | sed 's/^v//'); \
	if [ -d "skills/$$ver" ]; then \
		echo "skills/$$ver already exists -- remove it first if you want to regenerate it."; \
		exit 1; \
	fi; \
	cp -r skills/dev "skills/$$ver"; \
	echo "Created skills/$$ver from skills/dev."; \
	echo "Next: git add skills/$$ver && git commit && git push origin main, then make cut-release VERSION=$(VERSION)"

bundle-skills: skills-version

# Creates a git tag and pushes it to trigger GoReleaser via GitHub Actions
release:
	@echo "Creating and pushing tag $(VERSION)"
	git tag $(VERSION)
	git push origin $(VERSION)

# One-shot release tag push. Run `make bundle-skills VERSION=$(VERSION)`,
# review and commit the generated skills/<version> folder first when skill
# content needs to ship with this CLI version.
cut-release:
	$(MAKE) release VERSION=$(VERSION)
