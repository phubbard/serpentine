# serpentine-api: build on the dev Mac, deploy to axiom. No cloud CI.
#   make test     vet + unit tests (fixtures, no network)
#   make run      local server against axiom's GraphHopper, on :18990
#   make deploy   test, cross-build, copy to axiom, (re)load the launchd job, health-check

AXIOM   ?= axiom
LABEL   := net.phfactor.serpentine-api
VERSION := $(shell git rev-list --count HEAD)-$(shell git rev-parse --short HEAD)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: test build run deploy logs

test:
	go vet ./...
	go test ./...

build:
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/serpentine-api .

run:
	go run . -addr 127.0.0.1:18990 -gh http://$(AXIOM):8989

deploy: test build
	ssh $(AXIOM) 'mkdir -p ~/serpentine-api/logs'
	scp -q bin/serpentine-api $(AXIOM):serpentine-api/serpentine-api.new
	scp -q launchd/$(LABEL).plist $(AXIOM):Library/LaunchAgents/$(LABEL).plist
	ssh $(AXIOM) 'mv ~/serpentine-api/serpentine-api.new ~/serpentine-api/serpentine-api && \
		launchctl bootout gui/$$(id -u)/$(LABEL) 2>/dev/null; \
		for i in 1 2 3 4 5; do sleep 1; \
			launchctl bootstrap gui/$$(id -u) ~/Library/LaunchAgents/$(LABEL).plist 2>/dev/null && break; \
			echo "launchd still tearing the old job down, retrying ($$i)"; \
		done; \
		pgrep -x serpentine-api >/dev/null || { echo "error: service did not come back up"; exit 1; }'
	@for i in 1 2 3 4 5 6 7 8 9 10; do \
		curl -sf -m 2 http://$(AXIOM):8990/v1/health && echo && exit 0; sleep 1; \
	done; echo "health check failed; make logs"; exit 1

logs:
	ssh $(AXIOM) 'tail -50 ~/serpentine-api/logs/serpentine-api.log'
