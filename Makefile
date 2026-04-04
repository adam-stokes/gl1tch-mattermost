BINARY      := glitch-mattermost
INSTALL_BIN := $(or $(shell test -w /usr/local/bin && echo /usr/local/bin),$(HOME)/.local/bin)

.PHONY: build install test clean

build:
	go build -o $(BINARY) .

install: build
	install -m 0755 $(BINARY) $(INSTALL_BIN)/$(BINARY)

test:
	go test ./...

clean:
	rm -f $(BINARY)
