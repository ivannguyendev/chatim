-include .env

GO_IMAGE ?= golang:1.26
LINT_IMAGE ?= golangci/golangci-lint:v2.14.0
NETWORK  ?= chatim_default
COMPOSE  := docker compose -f deploy/compose/docker-compose.yml --env-file .env
COMPOSE_ALL := $(COMPOSE) --profile postgres
GO_RUN   := docker run --rm -v "$(CURDIR)":/src -w /src -v chatim-gomod:/go/pkg/mod -v chatim-gocache:/root/.cache/go-build -e GOFLAGS=-buildvcs=false
POC_RUN  := $(GO_RUN) --network $(NETWORK) -e "MONGO_URI=mongodb://$(MONGO_ROOT_USER):$(MONGO_ROOT_PASSWORD)@chatim-mongodb:27017/?replicaSet=rs0&authSource=admin" -e "PG_URI=postgres://$(PG_USER):$(PG_PASSWORD)@chatim-postgres:5432/chatim_poc"

.PHONY: go check-env test vet fmt-check lint vuln tidy poc image infra-up infra-down infra-reset pg-up pg-down

go:
	$(GO_RUN) $(GO_IMAGE) go $(ARGS)

check-env:
	@test -f .env || (echo "missing .env: run cp .env.example .env" && exit 1)

test:
	$(GO_RUN) $(GO_IMAGE) go test -race -shuffle=on ./...

vet:
	$(GO_RUN) $(GO_IMAGE) go vet ./...

fmt-check:
	$(GO_RUN) $(GO_IMAGE) sh -c 'test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)'

lint:
	$(GO_RUN) -v chatim-lintcache:/root/.cache/golangci-lint $(LINT_IMAGE) golangci-lint run ./...

vuln:
	$(GO_RUN) $(GO_IMAGE) go tool govulncheck ./...

tidy:
	$(GO_RUN) $(GO_IMAGE) go mod tidy

poc: check-env
	$(GO_RUN) $(GO_IMAGE) go build -o bin/$(TOOL) ./tools/poc/$(TOOL)
	$(POC_RUN) $(POC_FLAGS) $(GO_IMAGE) ./bin/$(TOOL) $(ARGS)

image:
	docker build -f deploy/docker/Dockerfile --build-arg TARGET=$(TARGET) -t chatim/$(notdir $(TARGET)):dev .

infra-up: check-env
	$(COMPOSE) up -d
	./scripts/wait-mongo-primary.sh

infra-down: check-env
	$(COMPOSE_ALL) down

infra-reset: check-env
	$(COMPOSE_ALL) down -v

pg-up: check-env
	$(COMPOSE_ALL) up -d postgres
	./scripts/wait-postgres.sh

pg-down: check-env
	$(COMPOSE_ALL) stop postgres
