GO ?= go

.PHONY: test
test: 
	$(GO) test -cover -coverprofile=coverage.out -v ./...
	$(GO) tool cover -html=coverage.out -o cover.html
	rm coverage.out
