# How falconet is built, in one place.
#
# A consumer pins a SHA, and that SHA has to vouch for a binary it will
# download later — so the bytes must be knowable BEFORE the tag exists. That
# only works if the same source produces the same bytes on the operator's
# laptop and on the release runner, which means every input to the compiler
# is nailed down here rather than left to whichever machine is building.
# release.yml does not repeat these flags; it calls this file. One definition,
# so the two sides cannot drift apart while still agreeing that they agree.
#
# Each pin below was measured, on 2026-08-22, by building
# cmd/falconet for linux/amd64 four ways and comparing SHA-256s. Every one of
# them changes the bytes:
#
#   -buildvcs=false   a clean clone stamped vcs.modified=false -> 87a702b0…,
#                     the same tree with one edited file -> 518f330b…. The
#                     operator prepares the digest on a DIRTY tree at a
#                     pre-tag commit; the runner builds a CLEAN tree at the
#                     tag. Without this flag those two can never match, by
#                     construction. Worse, a git WORKTREE stamps nothing at
#                     all — even -buildvcs=true is silently a no-op there —
#                     so preparing a digest from a worktree and building it
#                     from a clone disagree with no warning anywhere.
#   -trimpath         the same source built at two different absolute paths.
#                     With it, /Volumes/…/falconet and /private/tmp/… both
#                     produced 5bf1cd19….
#   CGO_ENABLED=0     cgo on -> 1a17f2c1…, cgo off -> a71cdf73… (darwin/arm64,
#                     identical size, different bytes). Go turns cgo ON by
#                     default whenever it is building for the HOST platform
#                     and a C compiler is present: true for linux/amd64 on the
#                     ubuntu runner, false for the same target cross-compiled
#                     from a Mac. The one target whose digest is compared is
#                     exactly the one where the two machines disagree.
#   -ldflags -buildid=  with it 5bf1cd19…, without it 22b75150….
#   GOAMD64=v1        v1 -> 5bf1cd19…, v3 -> 0d757d8f…. It is the default, but
#                     it is an environment variable, and an operator who has
#                     exported it for something else would prepare a digest
#                     no runner will ever reproduce.
#
# GOTOOLCHAIN is read from go.mod's `toolchain` line, the same single source
# ci.yml reads. It must be set EXPLICITLY: GOTOOLCHAIN=auto, the default, is a
# floor and not a pin — it downloads the pinned toolchain when the local Go is
# older and silently uses a newer local Go otherwise. The ubuntu runner image
# ships a Go newer than go.mod's, so under `auto` the runner would build with
# a different compiler than the laptop and say nothing.
#
# The third thing a release writes is the workflow's own refs. Every verb step
# in .github/workflows/falconet.yml is `uses: zetlen/falconet@vX.Y.Z` — the
# composite action at a tag, which installs the asset whose digest that tag's
# tree holds — and `uses:` cannot take an expression, so the tag is a literal
# in the file and nothing at run time can derive it. It lives there because
# that is the only place GitHub will read it from; it is rewritten HERE, by
# release-prep, so that release/VERSION, the digest and the refs are written
# by one command in one second and cannot be bumped separately. release-verify
# refuses a tree where they disagree, for the same reason it refuses a digest
# prepared for another version: a workflow that runs one falconet and vouches
# for another is the drift the whole release discipline exists to prevent.

SHELL := /bin/bash

# Prerequisite order is load-bearing: `release-prep` prints the toolchain it
# is about to use before it uses it, and `release-verify` compares bytes that
# were just built. Parallel make would interleave those.
.NOTPARALLEL:

GO   ?= go
DIST ?= dist
CMD  := ./cmd/falconet

# The four ADR-0006 D6 ships. Bare names, no version: the tag lives in the
# release URL's path, which is what lets action.yml build a download URL out
# of a version and a target the way it already does for gitleaks.
TARGETS      := linux_amd64 linux_arm64 darwin_arm64 darwin_amd64
RELEASE_BINS := $(addprefix $(DIST)/falconet_,$(TARGETS))

# The one target whose digest is committed, because it is the one CI downloads.
DIGEST_TARGET := linux_amd64
DIGEST_FILE   := release/falconet_$(DIGEST_TARGET).sha256
VERSION_FILE  := release/VERSION

# The reusable workflow, whose `uses: zetlen/falconet@<tag>` refs are the
# third thing release-prep writes and release-verify checks (see the header).
# Only lines that ARE a uses: key count — anchored on `^ *` below — so the
# prose above a step can name the shape without being rewritten or refused.
WORKFLOW      := .github/workflows/falconet.yml
USES_REF      := uses: zetlen/falconet@

# sha256sum is GNU coreutils and is on the runner; macOS ships shasum. Both
# print "<hex>  <name>", which is the format `sha256sum -c` reads back.
SHA256 := $(shell command -v sha256sum >/dev/null 2>&1 && echo 'sha256sum' || echo 'shasum -a 256')

GOTOOLCHAIN := $(shell sed -n 's/^toolchain //p' go.mod)
ifeq ($(strip $(GOTOOLCHAIN)),)
$(error go.mod has no toolchain line, so there is nothing to pin the compiler to)
endif
export GOTOOLCHAIN

export GOAMD64 := v1

BUILD_FLAGS      := -trimpath -buildvcs=false
RELEASE_LDFLAGS   = -buildid= -X main.version=$(VERSION)

.DEFAULT_GOAL := build

.PHONY: build test clean release-prep release-build release-verify \
        require-version go-toolchain FORCE

# The development binary, out of tree, unstamped: the exact command AGENTS.md
# and ci.yml name, so the suite runs against what those two describe.
build:
	@mkdir -p $(DIST)
	CGO_ENABLED=0 $(GO) build -trimpath -o $(DIST)/falconet $(CMD)

test: build
	$(GO) test ./...
	FALCONET="$(CURDIR)/$(DIST)/falconet" bash tests/run.sh

clean:
	rm -rf $(DIST)

# --- the release build ------------------------------------------------------

go-toolchain:
	@$(GO) version
	@echo "GOTOOLCHAIN=$(GOTOOLCHAIN)  GOAMD64=$(GOAMD64)  $(SHA256)"

require-version:
	@set -euo pipefail; \
	if [[ ! "$(VERSION)" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$$ ]]; then \
	  echo "make: VERSION must be a release tag, like VERSION=v0.1.0 (got: '$(VERSION)')" >&2; \
	  exit 1; \
	fi

# FORCE, not a dependency on the sources: the version is compiled IN, so a
# dist/ left over from `make release-prep VERSION=v0.1.0` is not the binary
# `make release-build VERSION=v0.1.1` was asked for, and make cannot see the
# difference by timestamp.
FORCE:

$(RELEASE_BINS): require-version

$(DIST)/falconet_%: FORCE
	@mkdir -p $(DIST)
	CGO_ENABLED=0 GOOS=$(firstword $(subst _, ,$*)) GOARCH=$(lastword $(subst _, ,$*)) \
	  $(GO) build $(BUILD_FLAGS) -ldflags='$(RELEASE_LDFLAGS)' -o $@ $(CMD)

# All four assets and the checksums.txt that ships beside them. Run by
# release.yml; runnable by hand, which is how a mismatch gets diagnosed.
release-build: require-version go-toolchain $(RELEASE_BINS)
	@set -euo pipefail; \
	( cd $(DIST) && $(SHA256) $(notdir $(RELEASE_BINS)) > checksums.txt ); \
	cat $(DIST)/checksums.txt

# What a person runs before tagging. It writes three files and stops: it does
# not commit, it does not tag, and it does not push. The last step of a
# release stays in a person's hands, as every other last step here does.
#
# The workflow's refs are rewritten through a temporary file and a mv, not
# `sed -i`: GNU and BSD sed disagree about -i's argument, and macOS ships
# the BSD one.
release-prep: require-version go-toolchain $(DIST)/falconet_$(DIGEST_TARGET)
	@set -euo pipefail; \
	mkdir -p $(dir $(DIGEST_FILE)); \
	( cd $(DIST) && $(SHA256) falconet_$(DIGEST_TARGET) ) > $(DIGEST_FILE); \
	printf '%s\n' '$(VERSION)' > $(VERSION_FILE); \
	grep -q '^ *$(USES_REF)' $(WORKFLOW) || { echo "release-prep: $(WORKFLOW) has no '$(USES_REF)' line to rewrite" >&2; exit 1; }; \
	sed -e 's#^\( *\)$(USES_REF)[^ ]*$$#\1$(USES_REF)$(VERSION)#' $(WORKFLOW) > $(WORKFLOW).release-prep.tmp; \
	mv $(WORKFLOW).release-prep.tmp $(WORKFLOW); \
	echo; \
	echo "wrote $(VERSION_FILE):     $$(cat $(VERSION_FILE))"; \
	echo "wrote $(DIGEST_FILE): $$(cat $(DIGEST_FILE))"; \
	echo "wrote $(WORKFLOW): $$(grep -c '^ *$(USES_REF)$(VERSION)$$' $(WORKFLOW)) lines now read '$(USES_REF)$(VERSION)'"; \
	echo; \
	echo "Next, by hand:"; \
	echo "  git add $(VERSION_FILE) $(DIGEST_FILE) $(WORKFLOW)"; \
	echo "  git commit -m 'Release $(VERSION)'"; \
	echo "  git tag $(VERSION)"; \
	echo "  git push origin main && git push origin $(VERSION)"; \
	echo; \
	echo "The tag push runs release.yml, which rebuilds these bytes and"; \
	echo "refuses to publish anything if they differ. So run this LAST: a"; \
	echo "digest describes one build of one tree, and any later commit that"; \
	echo "touches cmd/, internal/ or go.mod makes it stale. The workflow's"; \
	echo "refs now name a tag that does not exist until you push it."

# The guarantee, and the reason the two files above are worth committing.
# release.yml runs this BEFORE it creates a release or uploads an asset, so a
# build that does not reproduce publishes nothing at all.
release-verify: require-version $(DIST)/falconet_$(DIGEST_TARGET)
	@set -euo pipefail; \
	test -f $(VERSION_FILE) || { echo "release-verify: no $(VERSION_FILE); run 'make release-prep VERSION=$(VERSION)' and commit it" >&2; exit 1; }; \
	test -f $(DIGEST_FILE)  || { echo "release-verify: no $(DIGEST_FILE); run 'make release-prep VERSION=$(VERSION)' and commit it" >&2; exit 1; }; \
	recorded_version="$$(cat $(VERSION_FILE))"; \
	if [[ "$$recorded_version" != "$(VERSION)" ]]; then \
	  echo "release-verify: the digest in the tree was prepared for $$recorded_version, and this is $(VERSION)." >&2; \
	  echo "  A digest is only a claim about one build of one commit. Re-run release-prep." >&2; \
	  exit 1; \
	fi; \
	grep -q '^ *$(USES_REF)' $(WORKFLOW) || { echo "release-verify: $(WORKFLOW) pins no falconet at all: no '$(USES_REF)' line" >&2; exit 1; }; \
	stray="$$(grep -n '^ *$(USES_REF)' $(WORKFLOW) | grep -v '$(USES_REF)$(VERSION)$$' || true)"; \
	if [[ -n "$$stray" ]]; then \
	  echo "release-verify: $(WORKFLOW) pins falconet at a ref that is not $(VERSION):" >&2; \
	  printf '  %s\n' "$$stray" >&2; \
	  echo "  The workflow at this tag would install one falconet and vouch for another. Re-run release-prep." >&2; \
	  exit 1; \
	fi; \
	recorded="$$(awk '{ print $$1 }' $(DIGEST_FILE))"; \
	built="$$( cd $(DIST) && $(SHA256) falconet_$(DIGEST_TARGET) | awk '{ print $$1 }' )"; \
	if [[ "$$recorded" != "$$built" ]]; then \
	  echo "release-verify: falconet_$(DIGEST_TARGET) did not reproduce." >&2; \
	  echo "  in the tree: $$recorded" >&2; \
	  echo "  built here:  $$built" >&2; \
	  echo "  Nothing is published. The build is not reproducible, and the digest a" >&2; \
	  echo "  consumer's pinned SHA vouches for would be a digest of something else." >&2; \
	  exit 1; \
	fi; \
	echo "release-verify: $(VERSION) falconet_$(DIGEST_TARGET) $$built reproduced"
