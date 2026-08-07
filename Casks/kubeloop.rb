cask "kubeloop" do
  arch arm: "arm64", intel: "amd64"

  version "1.10.0"
  sha256 arm:   "369ccd820a7ebc26270af7a295b95153524dc8c4b7a8fa5be2f1b278cf51b2f1",
         intel: "314e59a3c1069526cc2e934d90681eacd4fd6c49cd9b140b0c80510b86d2ffc1"

  url "https://github.com/fengqi-dev/kube-loop/releases/download/v#{version}/kubeloop-#{version}-darwin-#{arch}.dmg"
  name "KubeLoop"
  desc "Connect your laptop to Kubernetes like a VPN"
  homepage "https://fengqi-dev.github.io/kube-loop/"

  livecheck do
    url :url
    strategy :github_latest
  end

  depends_on macos: :big_sur

  app "KubeLoop.app"

  # Unsigned / unnotarized builds trip Gatekeeper via com.apple.quarantine.
  # Clear it after install so users do not need a manual xattr.
  postflight do
    system_command "/usr/bin/xattr",
                   args: ["-dr", "com.apple.quarantine", "#{appdir}/KubeLoop.app"],
                   sudo: false
  end

  zap trash: [
    "~/.kubeloop",
    "~/Library/Application Support/KubeLoop",
    "~/Library/Caches/KubeLoop",
    "~/Library/Preferences/com.wails.kube-loop.plist",
    "~/Library/Saved Application State/com.wails.kube-loop.savedState",
  ]

  caveats <<~EOS
    On first use, approve the virtual network helper when prompted.
    You can uninstall it later from Settings.
  EOS
end
