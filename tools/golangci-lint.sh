#!/usr/bin/env bash

set -euo pipefail

version="v2.11.4"
pkg="github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${version}"
exec go run "${pkg}" "$@"
