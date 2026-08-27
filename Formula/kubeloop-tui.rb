class KubeloopTui < Formula
  desc "K9s-style terminal client for KubeLoop"
  homepage "https://fengqi-dev.github.io/kube-loop/"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.2.0/kubeloop-tui-2.2.0-darwin-arm64.tar.gz"
      sha256 "77878ed77c283bb9521c36017044a93692dcf546eac46223388c418c7c694657"
    else
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.2.0/kubeloop-tui-2.2.0-darwin-amd64.tar.gz"
      sha256 "4c172e87c1e76e9cb8107efaa085c04fb4076706d66c455349c1b56c04308980"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.2.0/kubeloop-tui-2.2.0-linux-arm64.tar.gz"
      sha256 "165113cbefd82b1e94171b4df2adcd0759e777eed9a57e471145c3b42b0fdd21"
    else
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.2.0/kubeloop-tui-2.2.0-linux-amd64.tar.gz"
      sha256 "795027c50898fbd2a6025839541932ac793a41b156131e24623e9f46850260bf"
    end
  end

  def install
    bin.install "kubeloop"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/kubeloop --version")
  end
end
