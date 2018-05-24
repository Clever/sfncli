include golang.mk
.DEFAULT_GOAL := test # override default goal set in library makefile

SHELL := /bin/bash
PKGS := $(shell go list ./... | grep -v /vendor)
VERSION := $(shell head -n 1 VERSION)
EXECUTABLE := sfncli
EXECUTABLE_PKG := github.com/Clever/sfncli/cmd/sfncli

.PHONY: all test $(PKGS) build install_deps release clean mocks

$(eval $(call golang-version-check,1.10))

all: test build release

test: mocks $(PKGS)

$(PKGS): golang-test-all-deps
	$(call golang-test-all,$@)

build:
	mkdir -p build
	go build -ldflags="-X main.Version=$(VERSION)" -o bin/$(EXECUTABLE) $(EXECUTABLE_PKG)

run: build
	./bin/sfncli -activityname $$_DEPLOY_ENV--echo -region us-west-2 -workername `hostname` -cmd echo

release:
	mkdir -p release
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X main.Version=$(VERSION)" \
-o="$@/$(EXECUTABLE)-$(VERSION)-linux-amd64" $(EXECUTABLE_PKG)
	GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w -X main.Version=$(VERSION)" \
-o="$@/$(EXECUTABLE)-$(VERSION)-darwin-amd64" $(EXECUTABLE_PKG)

clean:
	rm -rf bin release

mocks:
	mkdir -p bin
	go build -o ./bin/mockgen ./vendor/github.com/golang/mock/mockgen
	rm -rf gen-go/mocksfn && mkdir -p gen-go/mocksfn
	./bin/mockgen -source vendor/github.com/aws/aws-sdk-go/service/sfn/sfniface/interface.go -destination gen-go/mocksfn/mocksfn.go -package mocksfn
	rm -rf gen-go/mockcloudwatch && mkdir -p gen-go/mockcloudwatch
	./bin/mockgen -source vendor/github.com/aws/aws-sdk-go/service/cloudwatch/cloudwatchiface/interface.go -destination gen-go/mockcloudwatch/mockcloudwatch.go -package mockcloudwatch

install_deps: golang-dep-vendor-deps
	$(call golang-dep-vendor)
