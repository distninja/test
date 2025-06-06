#!/bin/bash

ldflags="-s -w"
target="test"

go env -w GOPROXY=https://goproxy.cn,direct

CGO_ENABLED=0 GOARCH=$(go env GOARCH) GOOS=$(go env GOOS) go build -ldflags "$ldflags" -o bin/$target .
