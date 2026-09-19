#!/bin/bash

go build -ldflags "-s -w" -tags libvirt_dlopen ./cmd/virt-rest-api
