#!/usr/bin/env bash

set -e

VERSION=$(git rev-parse --short HEAD)
docker push "planetlabs/hal5d:latest"
docker push "planetlabs/hal5d:${VERSION}"
