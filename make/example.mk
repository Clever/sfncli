include sfncli.mk
.DEFAULT_GOAL := build

SFNCLI_VERSION := latest

build: bin/sfncli

run: build
	bin/sfncli --help

clean:
	rm -rf bin
