#!/usr/bin/env bash

set -e

VERSION=$(git rev-parse --short HEAD)
docker push "negz/hal5d:latest"
docker push "negz/hal5d:${VERSION}"
