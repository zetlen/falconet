# How falconet is built, in one place.
#
# `build` is the development binary, out of tree and unstamped. `assets` is
# a release's binaries: the release workflow runs it at a tag, and the
# composite action downloads what it writes. At any ref that is not a
# release, a job or a workstation runs
#
#   go install github.com/zetlen/falconet/cmd/falconet@<ref>
#
# and the go command compiles the module the proxy serves for that ref. The
# compiler is pinned by go.mod's `toolchain` line, which the go command
# honours inside this module on its own; ci.yml and release.yml export it as
# GOTOOLCHAIN as well, so a runner whose Go is NEWER cannot quietly
# substitute itself.

SHELL := /bin/bash

# Git hands a hook the repository it is running for in the environment:
# GIT_DIR always inside a worktree, GIT_INDEX_FILE in pre-commit,
# GIT_CONFIG_PARAMETERS after `git -c`. lefthook's pre-push runs `make test`,
# and every git command a test runs in its own temp directory obeys those
# over the directory it was pointed at: `git init` and `git config` land in
# THIS clone's shared .git/config, which a guard test fills with
# core.fsmonitor, core.hooksPath and filter commands. No recipe here works
# on any repository but the one found from the working directory, so none
# of them inherits the hook's.
unexport GIT_DIR GIT_WORK_TREE GIT_COMMON_DIR GIT_INDEX_FILE GIT_PREFIX \
  GIT_OBJECT_DIRECTORY GIT_ALTERNATE_OBJECT_DIRECTORIES GIT_NAMESPACE \
  GIT_QUARANTINE_PATH GIT_CONFIG GIT_CONFIG_PARAMETERS GIT_CONFIG_COUNT

GO   ?= go
DIST ?= dist
CMD  := ./cmd/falconet

# The GOOS/GOARCH pairs a release publishes. The composite action maps the
# runner to one of these, and contract.test.sh holds the two lists equal.
PLATFORMS := darwin/arm64 linux/arm64 linux/amd64

LEFTHOOK_VERSION := v2.1.14

.DEFAULT_GOAL := build

.PHONY: build check test assets hooks clean

# The development binary, out of tree, unstamped: the exact command AGENTS.md
# and ci.yml name, so the suite runs against what those two describe.
build:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 $(GO) build -trimpath -o $(DIST)/falconet $(CMD)

# The fail-closed discipline, at the versions ci.yml pins: `go run` fetches
# each tool at exactly that version, so a laptop and the runner disagree on
# nothing. govulncheck reaches the vulnerability database over the network.
check:
	$(GO) vet ./...
	$(GO) run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...
	$(GO) run github.com/kisielk/errcheck@v1.20.0 ./...
	$(GO) run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...

test: build
	$(GO) test ./...
	FALCONET="$(CURDIR)/$(DIST)/falconet" bash tests/run.sh

# A release's binaries, for `make assets VERSION=vX.Y.Z`: one
# falconet_X.Y.Z_<os>_<arch>.tar.gz per platform, holding `falconet` and
# LICENSE at its root, and checksums.txt beside them in sha256sum's format.
# The version is linked into main.version, because a checkout build records
# no module version of its own. mise's github backend, ubi and eget pick an
# asset by the os and arch in that name.
assets:
	@[[ "$(VERSION)" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$$ ]] || { echo "VERSION must be vX.Y.Z, got '$(VERSION)'" >&2; exit 2; }
	rm -rf $(DIST)/assets
	@set -euo pipefail; \
	for p in $(PLATFORMS); do \
	  os="$${p%/*}"; arch="$${p#*/}"; \
	  name="falconet_$(VERSION:v%=%)_$${os}_$${arch}"; \
	  mkdir -p "$(DIST)/assets/$$name"; \
	  echo "building $$name"; \
	  CGO_ENABLED=0 GOOS="$$os" GOARCH="$$arch" $(GO) build -trimpath \
	    -ldflags "-X main.version=$(VERSION)" \
	    -o "$(DIST)/assets/$$name/falconet" $(CMD); \
	  cp LICENSE "$(DIST)/assets/$$name/"; \
	  tar -czf "$(DIST)/assets/$$name.tar.gz" -C "$(DIST)/assets/$$name" falconet LICENSE; \
	  rm -rf "$(DIST)/assets/$$name"; \
	done
	cd $(DIST)/assets && shasum -a 256 *.tar.gz > checksums.txt
	cat $(DIST)/assets/checksums.txt

# The git hooks in lefthook.yml, installed into this clone's .git/hooks.
# lefthook is installed at the pinned version where `go install` puts
# binaries: GOBIN, else GOPATH/bin. --reset-hooks-path unsets a
# core.hooksPath that would send git to another directory.
hooks:
	$(GO) install github.com/evilmartians/lefthook/v2@$(LEFTHOOK_VERSION)
	@set -euo pipefail; \
	bin="$$($(GO) env GOBIN)"; [ -n "$$bin" ] || bin="$$($(GO) env GOPATH)/bin"; \
	"$$bin/lefthook" install --reset-hooks-path

clean:
	rm -rf $(DIST)
