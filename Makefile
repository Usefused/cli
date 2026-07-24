.PHONY: build test clean release skills-version cut-release

BINARY_NAME=fused-cli
VERSION?=v0.1.0

build:
	go build -o $(BINARY_NAME) main.go

test:
	go test ./...

clean:
	go clean
	rm -f $(BINARY_NAME)

# Snapshots skills/dev/ into skills/<VERSION without the leading v>/ so this
# release's binary embeds (go:embed skills in main.go) and fetches
# (cmd/skill.go's skillVersionFolder) the right skill content -- GoReleaser's
# {{.Version}} template (and so cmd.Version, set via ldflags) never has the
# leading v, so the folder must match that, not the raw tag.
#
# This only creates the folder -- it deliberately does NOT git add/commit/tag,
# so you can review the diff first. Run this, commit the result to main, THEN
# run `make release`, so the tagged commit (and main, since skill fetch always
# reads main) both already contain the new version folder.
skills-version:
	@ver=$$(echo $(VERSION) | sed 's/^v//'); \
	if [ -d "skills/$$ver" ]; then \
		echo "skills/$$ver already exists -- remove it first if you want to regenerate it."; \
		exit 1; \
	fi; \
	cp -r skills/dev "skills/$$ver"; \
	echo "Created skills/$$ver from skills/dev."; \
	echo "Next: git add skills/$$ver && git commit && git push origin main, THEN make release VERSION=$(VERSION)"

# Creates a git tag and pushes it to trigger GoReleaser via GitHub Actions
release:
	@echo "Creating and pushing tag $(VERSION)"
	git tag $(VERSION)
	git push origin $(VERSION)

# Does the whole release flow in one shot: skills-version, then commit+push
# JUST that new skills/<version> folder to main (a scoped `git add`, so any
# other unrelated uncommitted changes in your tree are left alone), then
# release. Stops immediately if any step fails -- e.g. skills-version refusing
# to overwrite an existing folder, or nothing configured to push to origin.
#
# Use the individual targets instead (skills-version, then your own
# add/commit/push, then release) if you want to review the skills diff
# before it's committed -- this collapses that review step away.
cut-release: skills-version
	@set -e; \
	ver=$$(echo $(VERSION) | sed 's/^v//'); \
	git add "skills/$$ver"; \
	git commit -m "skills: snapshot v$$ver"; \
	git push origin main
	$(MAKE) release VERSION=$(VERSION)
