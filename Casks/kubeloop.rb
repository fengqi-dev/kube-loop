cask "kubeloop" do
  arch arm: "arm64", intel: "amd64"

  version "1.9.0"
  sha256 arm:   "c8d59c028ba65a8ef74be0c39fcafbe2b3d209153e231336448b9fb61831cf4c",
         intel: "e7083cf7d593288d15c9d1561ecae305cfb84ba98e0c0e698b0789851b9f9559"

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
