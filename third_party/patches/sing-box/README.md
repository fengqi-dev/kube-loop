# KubeLoop sing-box patch set

KubeLoop does not maintain a sing-box fork. Release builds export the exact
upstream source, apply the patch files in this directory, and compile the
result in an isolated temporary directory.

- Upstream: `https://github.com/SagerNet/sing-box`
- Tag: `v1.14.0`
- Revision: `0b8995879f29a9b98ee027bc17b75e101445b238`
- License: GPL-3.0
- Retained build tags: `with_gvisor,with_clash_api,kubeloop_minimal`

`with_gvisor` is required by the configured TUN `mixed` stack. `with_clash_api`
is required for local readiness, connection inspection, and shutdown. KubeLoop
does not generate configurations for the removed QUIC, DHCP, WireGuard, uTLS,
ACME, Tailscale, CCM, OCM, Cloudflared, Naive, USB/IP, OpenVPN, or OpenConnect
features.

The `kubeloop_minimal` tag narrows sing-box's runtime registries to the types
emitted by KubeLoop: TUN, direct, SOCKS and Trojan inbounds; direct, SOCKS and
Trojan outbounds; and UDP, hosts and local DNS transports. Trojan is retained
for the v3 WebSocket data plane. Optional protocol stubs, endpoints and
services are excluded so their implementation packages remain unreachable to
the Go linker.

The same tag limits the command surface to `run`, `check`, and `version` (plus
Cobra's built-in `help`). Configuration formatting, generation, GeoIP/GeoSite,
rule-set, merge, completion, slew of experimental subcommands, and tools are
excluded from the runtime binary.

On macOS arm64 with Go 1.27.0, building v1.14.0 with only the retained tags
produces a 35 MB binary with the full command surface; adding
`kubeloop_minimal` yields an 18.4 MB runtime-only binary. Two additional
dead-code cuts drop it to 18.0 MB: the certificate provider registry is emptied
(no `origin_ca` provider, so the ACME stack - certmagic/acmez/zerossl/libdns -
stays out of the link) and the router no longer imports `sing-mux`/`sing-vmess`
only to reject their deprecated global-multiplex magic destinations, removing
those protocol packages (plus yamux/smux) from the linker graph. Exact sizes
vary by toolchain and platform.

The `release/DEFAULT_BUILD_TAGS*` patch replaces the default feature matrix with
the retained tags. Linker flags are untouched: v1.14.0's
`-X runtime.godebugDefault=multipathtcp=0,tlssha1=1 -checklinkname=0` links
cleanly with this configuration, and its bundled `golang.org/x/net v0.57.0`
already covers GO-2026-4559 (fixed in v0.51.0), so no security bump patch is
needed.

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
