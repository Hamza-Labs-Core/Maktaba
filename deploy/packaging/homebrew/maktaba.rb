class Maktaba < Formula
  desc "Self-hosted Islamic / Arabic media library with transcription + search"
  homepage "https://maktaba.dev"
  url "https://github.com/Hamza-Labs-Core/Maktaba/archive/refs/tags/v0.1.0.tar.gz"
  sha256 "0000000000000000000000000000000000000000000000000000000000000000"
  license "MIT"

  depends_on "go" => :build
  depends_on "node" => :build
  depends_on "pnpm" => :build
  depends_on "uv" => :build
  depends_on "ffmpeg"
  depends_on "postgresql@16"

  def install
    system "make", "build"
    bin.install "build/api/maktaba-api"
    bin.install "build/streaming/maktaba-streaming"
    libexec.install "pipeline"
    (etc/"maktaba").install Dir["config/*"]
    (var/"maktaba").mkpath

    plist_path = etc/"maktaba/launchd"
    plist_path.mkpath
    plist_path.install "deploy/packaging/launchd/com.maktaba.api.plist"
    plist_path.install "deploy/packaging/launchd/com.maktaba.streaming.plist"
    plist_path.install "deploy/packaging/launchd/com.maktaba.pipeline.plist"
  end

  service do
    run [opt_bin/"maktaba-api"]
    keep_alive true
    log_path var/"log/maktaba/api.log"
    error_log_path var/"log/maktaba/api.err.log"
    environment_variables MAKTABA_CONFIG: etc/"maktaba/api.yml"
  end

  test do
    assert_match "maktaba", shell_output("#{bin}/maktaba-api --version")
  end
end
