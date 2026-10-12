.PHONY: build test vet

build:
	go build ./...

test:
	# -p 1: DB-backed tests across packages share one TEST_DATABASE_URL instance;
	# running package test binaries in parallel races their truncate-on-cleanup
	# against each other's inserts.
	go test -p 1 ./...

vet:
	go vet ./...
