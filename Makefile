.PHONY: test build tidy demo docker-demo deploy deploy-check

GO ?= docker run --rm -v "$(CURDIR)":/src:z -w /src golang:1.23-alpine

tidy:
	$(GO) go mod tidy

test:
	$(GO) go test ./...

build:
	$(GO) go build -o /src/bin/waygate ./cmd/waygate

demo:
	docker compose -f docker-compose.demo.yml up --build

docker-demo: demo

deploy-check:
	python3 scripts/deploy.py --check

deploy:
	python3 scripts/deploy.py
