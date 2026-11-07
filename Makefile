.PHONY: test lint cover mockfeed sample

test:
	cd backend && go test -race -count=1 ./...
lint:
	cd backend && test -z "$$(gofmt -l .)" && go vet ./... && go build ./...
cover:
	cd backend && go test -race -coverprofile=coverage.out ./...
mockfeed:
	cd backend && go run ./cmd/mockfeed -addr :8089 -token dev-token -drones 5 -imagery
sample:
	cd backend && go run ./cmd/gensample -out testdata/dji_sample.csv
