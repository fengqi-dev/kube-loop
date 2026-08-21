class KubeloopTui < Formula
  desc "K9s-style terminal client for KubeLoop"
  homepage "https://fengqi-dev.github.io/kube-loop/"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.1.3/kubeloop-tui-2.1.3-darwin-arm64.tar.gz"
      sha256 "be7d401f66f05a0ec42ce9d2fab4231032f509e6b030ee4ccfeec1ff29c5ba94"
    else
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.1.3/kubeloop-tui-2.1.3-darwin-amd64.tar.gz"
      sha256 "b6a7ff5d6cae16f8c1dc05e3ef552523b0ef4b2e007c0d1d7b7601482722d92d"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.1.3/kubeloop-tui-2.1.3-linux-arm64.tar.gz"
      sha256 "42eb2c0afca3120be0b66136dd72cb72fdd0d6689e8d33e388256ef741c400f7"
    else
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.1.3/kubeloop-tui-2.1.3-linux-amd64.tar.gz"
      sha256 "f7de804f4ab9137a0e9fe796c017057405636254c435956996bf371cc929bad9"
    end
  end

  def install
    bin.install "kubeloop"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/kubeloop --version")
  end
end
