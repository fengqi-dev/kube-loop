cask "kubeloop-desktop" do
  arch arm: "arm64", intel: "amd64"

  version "2.3.0"
  sha256 arm:   "58d525eb6a2b77f03ea11779b2073f5484544f48e0d45d0b7cd59eb92dae16cc",
         intel: "bea6eecaa4dab5555855bb4b268d23ed3d70093319339425b18014c30d1a569c"

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
