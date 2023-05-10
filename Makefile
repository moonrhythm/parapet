default: vet staticcheck test

test:
	go test -race ./...

vet:
	go vet ./...

staticcheck:
	staticcheck ./...
