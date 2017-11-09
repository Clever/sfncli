include golang.mk
.DEFAULT_GOAL := test # override default goal set in library makefile

SHELL := /bin/bash
PKGS := $(shell go list ./... | grep -v /vendor)
VERSION := $(shell head -n 1 VERSION)
EXECUTABLE := sfncli
EXECUTABLE_PKG := github.com/Clever/sfncli/cmd/sfncli

.PHONY: all test $(PKGS) build install_deps release clean

$(eval $(call golang-version-check,1.9))

GLIDE_VERSION := v0.12.3
$(GOPATH)/src/github.com/Masterminds/glide:
	git clone -b $(GLIDE_VERSION) https://github.com/Masterminds/glide.git $(GOPATH)/src/github.com/Masterminds/glide

$(GOPATH)/bin/glide: $(GOPATH)/src/github.com/Masterminds/glide
	go build -o $(GOPATH)/bin/glide github.com/Masterminds/glide

all: test build release

test: $(PKGS)

$(PKGS): golang-test-all-deps
	$(call golang-test-all,$@)

build:
	mkdir -p build
	go build -ldflags="-X main.Version=$(VERSION)" -o build/$(EXECUTABLE) $(EXECUTABLE_PKG)

	go build -o ./mockgen ./vendor/github.com/golang/mock/mockgen
	rm -rf gen-go/mocksfn && mkdir -p gen-go/mocksfn
	./mockgen -source vendor/github.com/aws/aws-sdk-go/service/sfn/sfniface/interface.go -destination gen-go/mocksfn/mocksfn.go -package mocksfn

run: build
	./build/sfncli -activityname $$_DEPLOY_ENV--echo -region us-west-2 -workername `hostname` -cmd echo

release:
	mkdir -p release
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X main.Version=$(VERSION)" \
-o="$@/$(EXECUTABLE)-$(VERSION)-linux-amd64" $(EXECUTABLE_PKG)
	GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w -X main.Version=$(VERSION)" \
-o="$@/$(EXECUTABLE)-$(VERSION)-darwin-amd64" $(EXECUTABLE_PKG)

clean:
	rm -rf build release


install_deps: golang-dep-vendor-deps
	$(call golang-dep-vendor)