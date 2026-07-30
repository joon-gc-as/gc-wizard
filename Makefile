-include .env

.ONESHELL:
SHELL := /bin/sh

.PHONY: all setup run build test clean

all: build test

# Run Dev
run:
	@go run main.go

# Make build
build:
	@go build -o wizard

# Clean the binary
clean:
	@echo "Cleaning..."
	@rm -f main
