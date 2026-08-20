class Kubeloop < Formula
  desc "K9s-style terminal client for KubeLoop"
  homepage "https://fengqi-dev.github.io/kube-loop/"
  version "2.1.0"
  license "MIT"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.1.0/kubeloop-tui-2.1.0-darwin-arm64.tar.gz"
      sha256 "ce0311ce75f54c43416378f53082f16981a9e3ae1b1bdb7cc2cab05f4a74bff6"
    else
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.1.0/kubeloop-tui-2.1.0-darwin-amd64.tar.gz"
      sha256 "fd42f406fb50be068c40559b4a7259e5c3bbbe4f847ec77bd2e35a75b3bd96d7"
    end
  end

  on_linux do
    if Hardware::CPU.arm?
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.1.0/kubeloop-tui-2.1.0-linux-arm64.tar.gz"
      sha256 "ba593d5bd24997c6855f80e99c0253fc26eedbaa17976294a27ec846b2811666"
    else
      url "https://github.com/fengqi-dev/kube-loop/releases/download/v2.1.0/kubeloop-tui-2.1.0-linux-amd64.tar.gz"
      sha256 "9afc5c2b35f7355d17cadc5f4ffb84f4db70c969b56489d7387cca9c382ff8d7"
    end
  end

  def install
    libexec.install "kubeloop"
    bin.write_exec_script libexec/"kubeloop"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/kubeloop --version")
  end
end
