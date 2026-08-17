# Third-party notices

## sing-box

KubeLoop distributes and runs sing-box as a separate managed process.

- Project: https://github.com/SagerNet/sing-box
- Pinned version: v1.13.18
- License: GNU General Public License v3.0
- Source: https://github.com/SagerNet/sing-box/tree/v1.13.18
- Full license text: https://www.gnu.org/licenses/gpl-3.0.txt

Platform packages include a binary built from the pinned source revision and
the repository-owned patch set, together with `LICENSE.sing-box.txt`. Explicit
upstream comparison builds use the matching official release archive and any
Windows sidecar DLLs it contains. The privileged helper runs the packaged
binary in place and does not copy or download sing-box at runtime.

For local development and e2e, `KUBELOOP_SINGBOX_PATH` or the optional
installer download path may still materialize a core under a cache directory.
