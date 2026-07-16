.PHONY: proto build test clean

proto:
	protoc \
		--proto_path=ore \
		--go_out=ore \
		--go_opt=paths=source_relative \
		ore/*.proto

build:
	go build -o bin/molten ./cmd/molten

test:
	go test ./...

clean:
	rm -rf bin/ ore/*.pb.go
