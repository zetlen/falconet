# How falconet is built, in one place.
#
# `build` is the development binary, out of tree and unstamped, and it is the
# only build here: there is no release build. A version of falconet is a git
# tag, and what a job or a workstation runs is
#
#   go install github.com/zetlen/falconet/cmd/falconet@<tag>
#
# — the go command compiles the module the proxy serves for that tag, and the
# checksum database vouches for the bytes. Nothing is committed ahead of a
# tag and nothing is uploaded after one. The compiler is pinned by go.mod's
# `toolchain` line, which the go command honours inside this module on its
# own; ci.yml exports it as GOTOOLCHAIN as well, so a runner whose Go is
# NEWER cannot quietly substitute itself.

SHELL := /bin/bash

GO   ?= go
DIST ?= dist
CMD  := ./cmd/falconet

.DEFAULT_GOAL := build

.PHONY: build check test clean

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

clean:
	rm -rf $(DIST)
