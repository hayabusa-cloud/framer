# Makefile for framer

.DEFAULT_GOAL := test

.PHONY: test
test:
	go test -race -covermode=atomic -coverprofile=coverage.out ./...

.PHONY: bench
bench:
	go test -bench=. -benchmem -run=^$$ ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: clean
clean:
	rm -f coverage.out cpu.out mem.out
