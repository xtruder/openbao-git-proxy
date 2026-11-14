GOLANGCI_LINT := golangci-lint

.PHONY: test fmt lint build bin/openbao-git-proxy clean

test:
	go test -race -count=1 ./...

fmt:
	$(GOLANGCI_LINT) run --fix -c .golangci.yml ./...

lint:
	$(GOLANGCI_LINT) run -c .golangci.yml ./...

build: bin/openbao-git-proxy

bin/openbao-git-proxy:
	mkdir -p $(@D)
	go build -o $@ ./cmd/openbao-git-proxy

clean:
	rm -rf bin
