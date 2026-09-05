cask "kubeloop-desktop" do
  arch arm: "arm64", intel: "amd64"

  version "2.5.0"
  sha256 arm:   "12017d593e4fe05b5a8b6bb5d265bedd8701bf559896d5228e071d55cd4959e4",
         intel: "847f227a7622a114f3a2eeb87150a7ccd4b1f4db8872c86596188ba384a231e2"

  url "https://github.com/fengqi-dev/kube-loop/releases/download/v#{version}/kubeloop-desktop-#{version}-darwin-#{arch}.dmg"
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
