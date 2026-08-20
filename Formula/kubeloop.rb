class Kubeloop < Formula
  desc "K9s-style terminal client for KubeLoop"
  homepage "https://fengqi-dev.github.io/kube-loop/"
  license "MIT"
  head "https://github.com/fengqi-dev/kube-loop.git", branch: "main"

  depends_on "go" => :build

	def install
	  system "make", "tui", "VERSION=HEAD"
	  libexec.install "build/bin/kubeloop"
	  bin.write_exec_script libexec/"kubeloop"
	end

  test do
    assert_match "HEAD", shell_output("#{bin}/kubeloop --version")
  end
end
