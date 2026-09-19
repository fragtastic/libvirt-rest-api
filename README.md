# virt-rest-api

A small, versioned REST API for managing libvirt virtual machines. The service
is written in modern Go, uses the standard library for HTTP, configuration,
logging, and shutdown, and has one runtime dependency: libvirt's official Go
binding.

## Requirements

- Go 1.24 or newer
- libvirt on the machine running the API
- Access to the configured libvirt connection URI

The normal build uses libvirt development headers. The `libvirt_dlopen` build
tag avoids that build-time requirement and loads `libvirt.so` at runtime:

```sh
go build -tags libvirt_dlopen ./cmd/virt-rest-api
./virt-rest-api
```

Run the tests without a live hypervisor:

```sh
go test -tags libvirt_dlopen ./...
```

## Configuration

Configuration is read from environment variables.

| Variable | Default | Purpose |
| --- | --- | --- |
| `LIBVIRT_URI` | `qemu:///system` | libvirt connection URI |
| `QEMU_URI` | unset | Compatibility fallback when `LIBVIRT_URI` is unset |
| `LISTEN_ADDR` | `127.0.0.1:8080` | HTTP listen address |
| `API_BEARER_TOKEN` | unset | Bearer token required on API routes |
| `CORS_ORIGINS` | unset | Comma-separated exact HTTP(S) origins; unset rejects browser cross-origin requests |
| `ALLOW_INSECURE_LISTEN` | `false` | Explicitly allow a non-loopback listener without authentication |

The default is intentionally local-only. For remote access, set a strong
`API_BEARER_TOKEN` and terminate TLS in front of this service. A non-loopback
listener without a token is rejected unless `ALLOW_INSECURE_LISTEN=true` is
explicitly set.

Example:

```sh
LIBVIRT_URI=qemu:///system \
API_BEARER_TOKEN='replace-with-a-long-random-token' \
LISTEN_ADDR=127.0.0.1:8080 \
./virt-rest-api
```

Requests with authentication use:

```sh
curl -H 'Authorization: Bearer replace-with-a-long-random-token' \
  http://127.0.0.1:8080/api/v1/vms
```

## API

All responses are JSON except the XML and screenshot endpoints. Errors use a
stable envelope:

```json
{"error":{"code":"vm_not_found","message":"virtual machine not found"}}
```

| Method | Path | Description |
| --- | --- | --- |
| `GET` | `/healthz` | Process liveness; intentionally does not contact libvirt or require auth |
| `GET` | `/api/v1/host` | Hypervisor host information |
| `GET` | `/api/v1/vms?state=all` | List domains; state may be `all`, `active`, or `inactive` |
| `GET` | `/api/v1/vms/{identifier}` | Domain state and resource information |
| `GET` | `/api/v1/vms/{identifier}/xml` | Raw libvirt domain XML as `application/xml` |
| `GET` | `/api/v1/vms/{identifier}/viewer` | First configured graphics listener |
| `GET` | `/api/v1/vms/{identifier}/screenshot` | Current display in libvirt's native image format |
| `POST` | `/api/v1/vms/{identifier}/actions/start` | Start an inactive domain |
| `POST` | `/api/v1/vms/{identifier}/actions/stop` | Immediately stop an active domain |

VM list and detail objects include a stable libvirt `uuid`. Their numeric `id`
is a transient runtime identifier and is `-1` while the VM is inactive.
Every `{identifier}` path parameter accepts either the VM name or its UUID.

Successful start and stop operations return the resulting state. Repeating an
action that conflicts with the current state returns `409 Conflict`. A missing
domain returns `404 Not Found`; unavailable libvirt operations return 503
Service Unavailable without exposing internal connection details.

## Extending the API

HTTP routing and transport behavior live in `internal/api`. Hypervisor details
live behind the `hypervisor.Service` interface in `internal/hypervisor`. To add
an endpoint, add the operation to that interface and its libvirt adapter, then
register a handler in `api.New`. Handler tests use an in-memory fake, so normal
API work does not require a running hypervisor.
