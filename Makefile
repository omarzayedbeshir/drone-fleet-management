.PHONY: test lint cover fmt mockfeed sample

test:      ## run every test with the race detector
	go test -race -count=1 ./...

lint:      ## same checks as CI
	@test -z "$$(gofmt -l .)" || (gofmt -l .; echo "run: make fmt"; exit 1)
	go vet ./...

fmt:
	gofmt -w .

cover:
	go test -race -count=1 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

mockfeed:  ## start the mock vendor on :8089 (token: dev-token)
	go run ./cmd/mockfeed -addr :8089 -token dev-token -drones 5 -imagery

sample:    ## regenerate the simulated DJI-format sample
	go run ./cmd/gensample -out testdata/dji_sample.csv
