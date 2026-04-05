BINARY      := glitch-mattermost
INSTALL_BIN := $(or $(shell test -w /usr/local/bin && echo /usr/local/bin),$(HOME)/.local/bin)

.PHONY: build install test clean

build:
	go build -o $(BINARY) .

install: build
	install -m 0755 $(BINARY) $(INSTALL_BIN)/$(BINARY)
	printf '#!/bin/sh\nexec glitch-mattermost chat "$$@"\n' > $(INSTALL_BIN)/glitch-mattermost-chat
	chmod 0755 $(INSTALL_BIN)/glitch-mattermost-chat

test:
	go test ./...

clean:
	rm -f $(BINARY)
