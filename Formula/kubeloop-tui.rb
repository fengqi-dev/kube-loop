class KubeloopTui < Formula
  desc "K9s-style terminal client for KubeLoop"
  homepage "https://fengqi-dev.github.io/kube-loop/"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/fqix/kube-loop/releases/download/v2.5.0/kubeloop-tui-2.5.0-darwin-arm64.tar.gz"
      sha256 "83a23c3a8963c8dc935f2199f7f41fff7f54aa70cda140ccf6194f35338926a2"
    else
      url "https://github.com/fqix/kube-loop/releases/download/v2.5.0/kubeloop-tui-2.5.0-darwin-amd64.tar.gz"
      sha256 "9aec18a9e7e65a6f4739e605f7816cac265ebbe77093d130b3f238d1528977dd"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/fqix/kube-loop/releases/download/v2.5.0/kubeloop-tui-2.5.0-linux-arm64.tar.gz"
      sha256 "d7a61fa957cc4eab60702e243a91808375d8529c59b40be23973b04e061441a9"
    else
      url "https://github.com/fqix/kube-loop/releases/download/v2.5.0/kubeloop-tui-2.5.0-linux-amd64.tar.gz"
      sha256 "6a170734b234605b69cf176c28aab7dd748b177ecd86eeb9ea8e93bd46a35a50"
    end
  end

  def install
    bin.install "kubeloop"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/kubeloop --version")
  end
end
