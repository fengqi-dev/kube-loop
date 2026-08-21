class KubeloopTui < Formula
  desc "K9s-style terminal client for KubeLoop"
  homepage "https://fengqi-dev.github.io/kube-loop/"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.1.1/kubeloop-tui-2.1.1-darwin-arm64.tar.gz"
      sha256 "05f83af720d830e8f617eb40e61aa0425e90b77e4929422a7ddb148fa751e356"
    else
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.1.1/kubeloop-tui-2.1.1-darwin-amd64.tar.gz"
      sha256 "ad91c6e83f7f2bb41b9b2ca8e9c736aa955493cad5906915d297501720feee04"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.1.1/kubeloop-tui-2.1.1-linux-arm64.tar.gz"
      sha256 "49aaff1e9d3cbc1c9e88a920784415908d230e20f9eb221d82a6d7a26396286b"
    else
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.1.1/kubeloop-tui-2.1.1-linux-amd64.tar.gz"
      sha256 "2a44cb3c77d4ad67fe95425ae43bfc0331314214fd025679cb1fad3a55298438"
    end
  end

  def install
    bin.install "kubeloop"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/kubeloop --version")
  end
end
