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
API_BEARER_TOKEN='replace-with-a-long-random-token' ./virt-rest-api
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
| `API_BEARER_TOKEN` | unset | Bearer token required on API routes; startup fails when unset unless an insecure override applies |
| `API_READ_TOKEN` | unset | Optional token for read-only API access |
| `API_CONTROL_TOKEN` | unset | Optional token for read and ordinary VM lifecycle controls |
| `API_ADMIN_TOKEN` | unset | Optional token for all operations, including destructive administration |
| `CORS_ORIGINS` | unset | Comma-separated exact HTTP(S) origins; unset rejects browser cross-origin requests |
| `ALLOW_INSECURE_LOOPBACK_LISTEN` | `false` | Explicitly allow authentication-free access only when `LISTEN_ADDR` is loopback |
| `ALLOW_INSECURE_LISTEN` | `false` | Explicitly allow authentication-free access on either loopback or external listeners |

Authentication is required by default, including on loopback. For remote
access, set a strong `API_BEARER_TOKEN` and terminate TLS in front of this
service. `ALLOW_INSECURE_LOOPBACK_LISTEN=true` waives authentication only for a
loopback listener. `ALLOW_INSECURE_LISTEN=true` is the broader escape hatch and
waives authentication for any listen address.

`API_BEARER_TOKEN` remains a backwards-compatible administrator credential.
Scoped credentials are hierarchical: admin includes control and read, while
control includes read. Configuring the same value for multiple token variables
is rejected. The insecure-listen overrides grant unauthenticated administrator
access and should be used only in deliberately isolated environments.

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
| `GET` | `/readyz` | Libvirt connection readiness; does not require auth |
| `GET` | `/api/v1/host` | Hypervisor host information |
| `GET` | `/api/v1/host/stats` | Timestamped cumulative host CPU and memory counters |
| `GET` | `/api/v1/vms?state=all` | List domains; state may be `all`, `active`, or `inactive` |
| `GET` | `/api/v1/events` | Server-sent VM lifecycle events with `Last-Event-ID` replay |
| `GET` | `/api/v1/vms/{identifier}` | Domain state and resource information |
| `GET` | `/api/v1/vms/{identifier}/stats` | Timestamped cumulative CPU, memory, disk, and network counters |
| `GET` | `/api/v1/vms/{identifier}/interfaces?source=lease` | Interface addresses from `lease`, `agent`, or `arp` |
| `GET` | `/api/v1/vms/{identifier}/autostart` | Read host-boot autostart configuration |
| `PATCH` | `/api/v1/vms/{identifier}/autostart` | Set autostart with `{"enabled":true}` (admin) |
| `GET` | `/api/v1/vms/{identifier}/xml` | Raw libvirt domain XML as `application/xml` |
| `GET` | `/api/v1/vms/{identifier}/viewer` | First configured graphics listener |
| `GET` | `/api/v1/vms/{identifier}/screenshot` | Current display in libvirt's native image format |
| `POST` | `/api/v1/vms/{identifier}/actions/start` | Start an inactive domain |
| `POST` | `/api/v1/vms/{identifier}/actions/shutdown` | Request a graceful guest shutdown |
| `POST` | `/api/v1/vms/{identifier}/actions/reboot` | Request a graceful guest reboot |
| `POST` | `/api/v1/vms/{identifier}/actions/pause` | Pause a running domain |
| `POST` | `/api/v1/vms/{identifier}/actions/resume` | Resume a paused domain |
| `POST` | `/api/v1/vms/{identifier}/actions/reset` | Force-reset an active domain |
| `POST` | `/api/v1/vms/{identifier}/actions/stop` | Force an active domain off immediately |

VM list and detail objects include a stable libvirt `uuid`. Their numeric `id`
is a transient runtime identifier and is `-1` while the VM is inactive.
Every `{identifier}` path parameter accepts either the VM name or its UUID.

Graceful shutdown is asynchronous: a successfully accepted request returns
`202 Accepted` with status `shutdown-requested` and the currently observed VM
state, but guest cooperation determines when shutdown completes. Start and
force-stop return `200 OK`. Repeating an action that conflicts with the current
state returns `409 Conflict`; a guest without graceful-shutdown support returns
`422 Unprocessable Entity`. A missing domain returns `404 Not Found`;
unavailable libvirt operations return 503 Service Unavailable without exposing
internal connection details.

Shutdown and reboot accept an optional JSON body with `mode` set to `default`,
`acpi`, or `guest-agent`. An empty body uses libvirt's default mechanism.

## Extending the API

HTTP routing and transport behavior live in `internal/api`. Hypervisor details
live behind the `hypervisor.Service` interface in `internal/hypervisor`. To add
an endpoint, add the operation to that interface and its libvirt adapter, then
register a handler in `api.New`. Handler tests use an in-memory fake, so normal
API work does not require a running hypervisor.
