# Third-party notices

## sing-box

KubeLoop distributes and runs sing-box as a separate managed process.

- Project: https://github.com/SagerNet/sing-box
- Pinned version: v1.13.16
- License: GNU General Public License v3.0
- Source: https://github.com/SagerNet/sing-box/tree/v1.13.16
- Full license text: https://www.gnu.org/licenses/gpl-3.0.txt

Platform packages include the pinned upstream binary (and on Windows any
sidecar DLLs from the same release archive, such as `libcronet.dll`) next to
the application, together with `LICENSE.sing-box.txt`. The privileged helper
runs that packaged binary in place and does not copy or download sing-box at
runtime.

For local development and e2e, `KUBELOOP_SINGBOX_PATH` or the optional
installer download path may still materialize a core under a cache directory.
