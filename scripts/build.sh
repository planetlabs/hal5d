#!/usr/bin/env bash

VERSION=$(git rev-parse --short HEAD)
docker build --tag "negz/hal5d:latest" .
docker build --tag "negz/hal5d:${VERSION}" .
