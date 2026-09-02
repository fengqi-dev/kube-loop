class KubeloopTui < Formula
  desc "K9s-style terminal client for KubeLoop"
  homepage "https://fengqi-dev.github.io/kube-loop/"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.4.0/kubeloop-tui-2.4.0-darwin-arm64.tar.gz"
      sha256 "3aa5bdb1dc06f874677cc3e2dfda80c7f7985f4ada497aa95cf39f685eb72f48"
    else
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.4.0/kubeloop-tui-2.4.0-darwin-amd64.tar.gz"
      sha256 "65c87c1a3108163a8414a8a37f928e41db4d140d91c9c0d1f4fb5354d1089178"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.4.0/kubeloop-tui-2.4.0-linux-arm64.tar.gz"
      sha256 "d6fcc0eb96af8f88b5f5591b52d2849259ecb207025f875be2b4fd1d41f239c4"
    else
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.4.0/kubeloop-tui-2.4.0-linux-amd64.tar.gz"
      sha256 "fe9b0dca5450b73d2af755262bbd7593aa8dad6c7b20c11785c82f4e38319ed3"
    end
  end

  def install
    bin.install "kubeloop"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/kubeloop --version")
  end
end
