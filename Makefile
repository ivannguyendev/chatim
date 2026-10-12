-include .env
export REDIS_PASSWORD REDIS_DEDUPE_PASSWORD
export CHATIM_IT_REDIS_PASSWORD := $(REDIS_PASSWORD)
export CHATIM_IT_REDIS_DEDUPE_PASSWORD := $(REDIS_DEDUPE_PASSWORD)
export MONGO_URI := mongodb://$(MONGO_ROOT_USER):$(MONGO_ROOT_PASSWORD)@chatim-mongodb:27017/?replicaSet=rs0&authSource=admin
export CHATIM_IT_MONGO_URI := $(MONGO_URI)
export PG_URI := postgres://$(PG_USER):$(PG_PASSWORD)@chatim-postgres:5432/chatim_poc

GO_IMAGE ?= golang:1.26.9
LINT_IMAGE ?= golangci/golangci-lint:v2.14.0
BUF_IMAGE ?= bufbuild/buf:1.73.0
PROM_IMAGE ?= prom/prometheus:v3.5.0
NETWORK  ?= chatim_default
COMPOSE  := docker compose -f deploy/compose/docker-compose.yml --env-file .env
COMPOSE_ALL := $(COMPOSE) --profile postgres --profile app
CORE_COMPOSE := $(COMPOSE) --profile app
CORES    := core-1 core-2
INSTANCE ?= state
REDIS_CONTAINER_state  := chatim-redis
REDIS_CONTAINER_dedupe := chatim-redis-dedupe
GO_RUN   := docker run --rm -v "$(CURDIR)":/src -w /src -v chatim-gomod:/go/pkg/mod -v chatim-gocache:/root/.cache/go-build -e GOFLAGS=-buildvcs=false
BUF_RUN  := docker run --rm -v "$(CURDIR)":/src -w /src $(BUF_IMAGE)
POC_RUN  := $(GO_RUN) --network $(NETWORK) -e MONGO_URI -e PG_URI -e REDIS_PASSWORD

.PHONY: go check-env test itest vet fmt-check lint vuln tidy proto buf-lint alerts-check poc image infra-up infra-down infra-reset pg-up pg-down core-up core-down e2e redis-cli

go:
	$(GO_RUN) $(GO_IMAGE) go $(ARGS)

check-env:
	@test -f .env || (echo "missing .env: run cp .env.example .env" && exit 1)

test:
	$(GO_RUN) $(GO_IMAGE) go test -race -shuffle=on ./...

itest: check-env
	$(GO_RUN) --network $(NETWORK) -e CHATIM_IT_MONGO_URI -e CHATIM_IT_REDIS_ADDR=chatim-redis:6379 -e CHATIM_IT_REDIS_PASSWORD -e CHATIM_IT_REDIS_DEDUPE_ADDR=chatim-redis-dedupe:6379 -e CHATIM_IT_REDIS_DEDUPE_PASSWORD -e CHATIM_IT_NATS_URL=nats://chatim-nats:4222 $(GO_IMAGE) go test -race -shuffle=on -count=1 ./...

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

proto:
	$(BUF_RUN) generate

buf-lint:
	$(BUF_RUN) lint
	$(BUF_RUN) format -d --exit-code

alerts-check:
	docker run --rm -v "$(CURDIR)/deploy/prometheus":/rules:ro --entrypoint promtool $(PROM_IMAGE) check rules /rules/alerts.yml

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

redis-cli:
	@test -n "$(REDIS_CONTAINER_$(INSTANCE))" || (echo "INSTANCE must be state or dedupe" && exit 1)
	docker exec $(REDIS_CONTAINER_$(INSTANCE)) sh -c 'read -r REDISCLI_AUTH < "$$REDIS_SECRET_FILE"; export REDISCLI_AUTH; exec redis-cli "$$@"' redis-cli $(ARGS)

pg-up: check-env
	$(COMPOSE_ALL) up -d postgres
	./scripts/wait-postgres.sh

pg-down: check-env
	$(COMPOSE_ALL) stop postgres

core-up: check-env
	$(MAKE) -s image TARGET=apps/core
	$(CORE_COMPOSE) up -d $(CORES)
	./scripts/wait-core-healthy.sh $(addprefix chatim-,$(CORES))

core-down: check-env
	$(CORE_COMPOSE) rm -s -f $(CORES)

e2e: check-env
	$(MAKE) -s image TARGET=tools/corecli
	NETWORK=$(NETWORK) ./scripts/e2e.sh
