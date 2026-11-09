# Makefile for framer

.DEFAULT_GOAL := test

BENCH ?= .
BENCHTIME ?= 100ms
BENCHCOUNT ?= 10

.PHONY: test
test:
	go test -timeout=120s -race -covermode=atomic -coverprofile=coverage.out ./...

.PHONY: bench
bench:
	go test -run='^$$' -bench='$(BENCH)' -benchmem -benchtime=$(BENCHTIME) -count=$(BENCHCOUNT) -cpu=1 -timeout=120s ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: clean
clean:
	rm -f coverage.out cpu.out mem.out
