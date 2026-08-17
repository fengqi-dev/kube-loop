# KubeLoop sing-box patch set

KubeLoop does not maintain a sing-box fork. Release builds export the exact
upstream source, apply the patch files in this directory, and compile the
result in an isolated temporary directory.

- Upstream: `https://github.com/SagerNet/sing-box`
- Tag: `v1.13.18`
- Revision: `45ca32dcb966f07f97fc888fe8586e359dbe8405`
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

On macOS arm64 with Go 1.26.5, the patched binary is 16,725,074 bytes versus
50,140,546 bytes for the upstream release binary (66.6% smaller). Exact sizes
vary by toolchain and platform.

## Verify and package

Initialize the submodule, then run:

```sh
git submodule update --init --recursive
make singbox-patch-check
make singbox-package
```

`make singbox-package` writes a binary archive, a reconstructable patch archive,
and SHA-256 checksums to `dist/`. Desktop release builds use this patched source
by default. Set `KUBELOOP_SINGBOX_SOURCE=upstream` only for comparison builds.

When updating sing-box, change the submodule, `Version` and `SourceRevision` in
`internal/singbox/distribution/version.go`, refresh the patch, and rerun the
verification and package targets on every supported platform.
