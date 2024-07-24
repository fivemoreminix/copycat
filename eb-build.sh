#!/usr/bin/env bash
set -xe

# Build the current Go module
GOARCH=amd64 GOOS=linux go build -o bin/application .

# Zip the runtime dependencies for Elastic Beanstalk
zip -r uploadThis.zip bin templates assets .ebextensions .env
