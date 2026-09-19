#!/bin/bash

go build -ldflags "-s -w" -tags libvirt_dlopen ./cmd/libvirt-rest-api
