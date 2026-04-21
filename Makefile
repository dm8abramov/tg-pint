.PHONY: run test build fmt env

GOCACHE ?= $(CURDIR)/.cache/go-build

run:
	go run .

test:
	GOCACHE=$(GOCACHE) go test ./...

build:
	GOCACHE=$(GOCACHE) go build -o bin/tg-pint .

fmt:
	gofmt -w main.go

env:
	cp .env.example .env
