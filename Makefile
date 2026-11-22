# Makefile for framer

.DEFAULT_GOAL := test

BENCH ?= .
BENCHTIME ?= 200ms
BENCHCOUNT ?= 10
BENCHCPU ?= 1

.PHONY: test
test:
	go test -timeout=120s -race -covermode=atomic -coverprofile=coverage.out ./...

.PHONY: bench
bench:
	go test -run='^$$' -bench='$(value BENCH)' -benchmem -benchtime=$(BENCHTIME) -count=$(BENCHCOUNT) -cpu=$(BENCHCPU) -timeout=120s ./...

.PHONY: vet
vet:
	go vet ./...

.PHONY: clean
clean:
	rm -f coverage.out cpu.out mem.out
