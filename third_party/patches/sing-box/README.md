# KubeLoop sing-box patch set

KubeLoop does not maintain a sing-box fork. Release builds export the exact
upstream source, apply the patch files in this directory, and compile the
result in an isolated temporary directory.

- Upstream: `https://github.com/SagerNet/sing-box`
- Tag: `v1.13.21`
- Revision: `628cb31ffa79cffffd34c2f9cde6cae044e4fc12`
- License: GPL-3.0
- Retained build tags: `with_gvisor,with_clash_api,kubeloop_minimal`

`with_gvisor` is required by the configured TUN `mixed` stack. `with_clash_api`
is required for local readiness, connection inspection, and shutdown. KubeLoop
does not generate configurations for the removed QUIC, DHCP, WireGuard, uTLS,
ACME, Tailscale, CCM, OCM, or Naive features.

The `kubeloop_minimal` tag narrows sing-box's runtime registries to the types
emitted by KubeLoop: TUN, direct and SOCKS inbounds; direct and SOCKS
outbounds; and UDP, hosts and local DNS transports. Optional protocol stubs,
endpoints and services are excluded so their implementation packages remain
unreachable to the Go linker.

The same tag limits the command surface to `run`, `check`, and `version` (plus
Cobra's built-in `help`). Configuration formatting, generation, GeoIP/GeoSite,
rule-set, merge, completion, and experimental tools are excluded from the
runtime binary.

The security dependency patch updates `golang.org/x/net` to `v0.51.0` and the
patched source's Go requirement to 1.25, fixing GO-2026-4559 in the HTTP/2 code
reachable from the minimal command binary.

The Go fix patch makes import aliases explicit when a dependency's declared
package name differs from the final element of its import path. It records the
Go 1.26 `go fix` result without changing runtime behavior.

On macOS arm64 with Go 1.26.6, the runtime-only command patch reduces the
already-minimal binary from 16,760,338 to 16,236,642 bytes (3.1%). Exact sizes
vary by toolchain and platform.

## Verify and package

Initialize the submodule, then run:

```sh
git submodule update --init --recursive
make singbox-patch-check
make singbox-package
```

`make singbox-package` writes a binary archive, a reconstructable patch archive,
and SHA-256 checksums to `dist/`. Desktop release builds and end-to-end tests use
this patched source.

When updating sing-box, change the submodule, `Version` and `SourceRevision` in
`internal/singbox/distribution/version.go`, refresh the patch, and rerun the
verification and package targets on every supported platform.
