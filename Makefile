-include .env

GO_IMAGE ?= golang:1.26
NETWORK  ?= chatim_default
COMPOSE  := docker compose -f deploy/compose/docker-compose.yml --env-file .env
GO_RUN   := docker run --rm -v "$(CURDIR)":/src -w /src -v chatim-gomod:/go/pkg/mod -v chatim-gocache:/root/.cache/go-build -e GOFLAGS=-buildvcs=false
POC_RUN  := $(GO_RUN) --network $(NETWORK) -e "MONGO_URI=mongodb://$(MONGO_ROOT_USER):$(MONGO_ROOT_PASSWORD)@chatim-mongodb:27017/?replicaSet=rs0&authSource=admin"

.PHONY: go test vet fmt-check tidy poc image infra-up infra-down infra-reset

go:
	$(GO_RUN) $(GO_IMAGE) go $(ARGS)

test:
	$(GO_RUN) $(GO_IMAGE) go test -race ./...

vet:
	$(GO_RUN) $(GO_IMAGE) go vet ./...

fmt-check:
	$(GO_RUN) $(GO_IMAGE) sh -c 'test -z "$$(gofmt -l .)" || (gofmt -l . && exit 1)'

tidy:
	$(GO_RUN) $(GO_IMAGE) go mod tidy

poc:
	$(GO_RUN) $(GO_IMAGE) go build -o bin/$(TOOL) ./tools/poc/$(TOOL)
	$(POC_RUN) $(POC_FLAGS) $(GO_IMAGE) ./bin/$(TOOL) $(ARGS)

image:
	docker build -f deploy/docker/Dockerfile --build-arg TARGET=$(TARGET) -t chatim/$(notdir $(TARGET)):dev .

infra-up:
	$(COMPOSE) up -d
	./scripts/wait-mongo-primary.sh

infra-down:
	$(COMPOSE) down

infra-reset:
	$(COMPOSE) down -v
