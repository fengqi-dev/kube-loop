class KubeloopTui < Formula
  desc "K9s-style terminal client for KubeLoop"
  homepage "https://fengqi-dev.github.io/kube-loop/"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.1.2/kubeloop-tui-2.1.2-darwin-arm64.tar.gz"
      sha256 "d99359c5d4c98f70760f6c256119152d78dd23ba323dacf66262679d2f474cd8"
    else
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.1.2/kubeloop-tui-2.1.2-darwin-amd64.tar.gz"
      sha256 "f8e0f59049261ad0b805db2ad0cc5b493e0269e33e74a0ba089f959db1875031"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.1.2/kubeloop-tui-2.1.2-linux-arm64.tar.gz"
      sha256 "2583469fa81cfa07f042d64d74a8417c88e105e6e216e893838a08902a145b11"
    else
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.1.2/kubeloop-tui-2.1.2-linux-amd64.tar.gz"
      sha256 "0c9e3c1815ceec58bf30823ab7ab105f77feecf349645f1fa4834d773011910c"
    end
  end

  def install
    bin.install "kubeloop"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/kubeloop --version")
  end
end
