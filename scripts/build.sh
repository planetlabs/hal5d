#!/usr/bin/env bash

VERSION=$(git rev-parse --short HEAD)
docker build --tag "planetlabs/hal5d:latest" .
docker build --tag "planetlabs/hal5d:${VERSION}" .
