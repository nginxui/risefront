
test:
	go test ./... -race -cover

upgrade:
	go get -u ./...

lint:
	golangci-lint run ./...