.PHONY: build run test clean

build:
	go build -o build/leben cmd/leben/main.go

run:
	go run cmd/leben/main.go

test:
	go test -v ./...

clean:
	rm -rf build/
