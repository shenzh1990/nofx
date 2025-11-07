# Go parameters
GOCMD=go
GORUN=$(GOCMD) run
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
BIN_ROOT = bin
BIN_NAME = nofx
RELEASE_ROOT = release
RELEASE_NAME = nofx
RELEASE_LATEST = $(RELEASE_ROOT)/$(RELEASE_NAME)
NOW = $(shell date -u '+%Y%m%d%I%M%S')
REMOTE_URL=qinali
REMOTE_DIR=/root/quantifytrade

run:
	$(GORUN) main.go

build-linux64: clean
	@GOOS=linux GOARCH=amd64 go build -ldflags "-w -s" -o $(BIN_ROOT)/linux64/$(BIN_NAME) main.go


build-darwin64: clean
	@GOOS=darwin GOARCH=amd64 go build -ldflags "-w -s" -o $(BIN_ROOT)/darwin64/$(BIN_NAME) main.go



pack-linux64: clean-bin build-linux64
	@echo "cp $(BIN_NAME) To $(REMOTE_URL):$(REMOTE_DIR)/$(BIN_NAME)"
	@scp ./$(BIN_ROOT)/linux64/$(BIN_NAME)  $(REMOTE_URL):$(REMOTE_DIR)/$(BIN_NAME)
	@echo "success"

clean-bin:
	rm -rf $(BIN_ROOT)

clean-release:
	rm -rf $(RELEASE_ROOT)

clean: clean-bin clean-release

.PHONY: dev build-linux64 build-darwin64  pack-linux64 clean
