# Third-party notices

## sing-box

KubeLoop distributes and runs sing-box as a separate managed process.

- Project: https://github.com/SagerNet/sing-box
- Pinned version: v1.14.0
- License: GNU General Public License v3.0
- Source: https://github.com/SagerNet/sing-box/tree/v1.14.0
- Full license text: https://www.gnu.org/licenses/gpl-3.0.txt

Platform packages include a binary built from the pinned source revision and
the repository-owned patch set, together with `LICENSE.sing-box.txt`. Release
and end-to-end builds compile that source locally; they do not download an
official prebuilt sing-box archive. The privileged helper runs the packaged
binary in place and does not copy or download sing-box at runtime.

Local development and end-to-end tests may select an existing locally built
core with `KUBELOOP_SINGBOX_PATH`.
