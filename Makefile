include golang.mk
.DEFAULT_GOAL := test # override default goal set in library makefile

SHELL := /bin/bash
PKGS := $(shell go list ./... | grep -v /vendor | grep -v /tools)
VERSION := $(shell head -n 1 VERSION)
EXECUTABLE := sfncli
EXECUTABLE_PKG := github.com/Clever/sfncli/cmd/sfncli

.PHONY: all test $(PKGS) build install_deps release clean mocks

$(eval $(call golang-version-check,1.13))

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
	go build -o bin/mockgen -mod=vendor ./vendor/github.com/golang/mock/mockgen
	rm -rf mocks/mock_*.go
	./bin/mockgen -source ./vendor/github.com/aws/aws-sdk-go/service/sfn/sfniface/interface.go -destination mocks/mock_sfn.go -package mocks
	./bin/mockgen -source ./vendor/github.com/aws/aws-sdk-go/service/cloudwatch/cloudwatchiface/interface.go -destination mocks/mock_cloudwatch.go -package mocks

install_deps:
	go mod vendor
