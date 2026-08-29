class KubeloopTui < Formula
  desc "K9s-style terminal client for KubeLoop"
  homepage "https://fengqi-dev.github.io/kube-loop/"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.3.0/kubeloop-tui-2.3.0-darwin-arm64.tar.gz"
      sha256 "ec3733583f685fed31ac28f9e8dfb593f0471b829f6dd846fd320f0ccbeda802"
    else
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.3.0/kubeloop-tui-2.3.0-darwin-amd64.tar.gz"
      sha256 "550d5284af063e9647756e7965429e7cd955757bf58020eb366ecbc1b3d8cf97"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.3.0/kubeloop-tui-2.3.0-linux-arm64.tar.gz"
      sha256 "30c59afc786dd3b81232f556d3aae0141517e7edb284bffc02037a0e4bc8a3aa"
    else
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.3.0/kubeloop-tui-2.3.0-linux-amd64.tar.gz"
      sha256 "52ce975b2b0e902ba13fb0c4b64b01d53f0d2a77489cc9c3108f6be6bb719176"
    end
  end

  def install
    bin.install "kubeloop"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/kubeloop --version")
  end
end
