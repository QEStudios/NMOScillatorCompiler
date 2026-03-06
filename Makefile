exe != go env GOEXE

build:
	go build -ldflags="-X main.version=$(shell git describe --always --dirty) -s -w" -trimpath -o bin/NMOScillatorCompiler$(exe) cmd/compiler/main.go

buildall:
	GOOS=windows GOARCH=amd64 go build -ldflags="-X main.version=$(shell git describe --always --dirty) -s -w" -trimpath -o bin/NMOScillatorCompiler-windows-amd64.exe cmd/compiler/main.go
	GOOS=linux GOARCH=amd64 go build -ldflags="-X main.version=$(shell git describe --always --dirty) -s -w" -trimpath -o bin/NMOScillatorCompiler-linux-amd64 cmd/compiler/main.go

run:
	go run cmd/compiler/main.go
