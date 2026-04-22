.PHONY: run test build fmt env docker-build docker-run

GOCACHE ?= $(CURDIR)/.cache/go-build
DOCKER_IMAGE ?= tg-pint:local
DOCKER_CONTAINER ?= tg-pint

run:
	go run .

test:
	GOCACHE=$(GOCACHE) go test ./...

build:
	GOCACHE=$(GOCACHE) go build -o bin/tg-pint .

fmt:
	gofmt -w main.go markdown.go main_test.go

env:
	cp .env.example .env

docker-build:
	docker build -t $(DOCKER_IMAGE) .

docker-run: docker-build
	-docker rm -f $(DOCKER_CONTAINER)
	docker run --rm --name $(DOCKER_CONTAINER) --env-file .env $(DOCKER_IMAGE)
