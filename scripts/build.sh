#!/bin/sh
set -eu
npm ci
npm run build
mkdir -p dist/bin
go test -tags pam ./...
go build -trimpath -tags pam -o dist/bin/server ./cmd/server
python3 scripts/check.py
python3 scripts/package.py
