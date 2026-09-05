class KubeloopTui < Formula
  desc "K9s-style terminal client for KubeLoop"
  homepage "https://fengqi-dev.github.io/kube-loop/"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/fqix/kube-loop/releases/download/v2.6.0/kubeloop-tui-2.6.0-darwin-arm64.tar.gz"
      sha256 "70ca2375c3eaab65af98e6cc42098e3df31e27bc006b391388f17d30b2fd8f52"
    else
      url "https://github.com/fqix/kube-loop/releases/download/v2.6.0/kubeloop-tui-2.6.0-darwin-amd64.tar.gz"
      sha256 "62ad8692f35ffa0001401cf565922da155d733435429f83731a9cdc4c864ab28"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/fqix/kube-loop/releases/download/v2.6.0/kubeloop-tui-2.6.0-linux-arm64.tar.gz"
      sha256 "b3f041c8eab679fe56def66faa6e30cb0f20e7a7097114f04f0feceafd827842"
    else
      url "https://github.com/fqix/kube-loop/releases/download/v2.6.0/kubeloop-tui-2.6.0-linux-amd64.tar.gz"
      sha256 "fd75852ba4877a4bcb7463d73b6179f212c5a0ed2217e886570b76a3b170933f"
    end
  end

  def install
    bin.install "kubeloop"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/kubeloop --version")
  end
end
