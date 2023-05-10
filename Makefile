default: vet lint test

test:
	go test -race ./...

vet:
	go vet ./...

lint:
	golangci-lint run
