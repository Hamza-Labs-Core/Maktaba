# Homebrew formula for the Maktaba home server (binary install).
#
# This file is a TEMPLATE. The release workflow copies it, substitutes
# the __VERSION__ / __SHA256_*__ placeholders with the real tag and the
# SHA-256 of each macOS release archive, and opens a PR against the
# homebrew-tap repo. Do NOT hand-edit the placeholders.
#
# Users then:  brew install hamza-labs-core/tap/maktaba
class Maktaba < Formula
  desc "Self-hosted Islamic / Arabic media library with transcription + search"
  homepage "https://maktaba.dev"
  version "__VERSION__"
  license "MIT"

  on_macos do
    on_arm do
      url "https://github.com/Hamza-Labs-Core/Maktaba/releases/download/v#{version}/maktaba-server-#{version}-darwin-arm64.tar.gz"
      sha256 "__SHA256_DARWIN_ARM64__"
    end
    on_intel do
      url "https://github.com/Hamza-Labs-Core/Maktaba/releases/download/v#{version}/maktaba-server-#{version}-darwin-amd64.tar.gz"
      sha256 "__SHA256_DARWIN_AMD64__"
    end
  end

  # ffmpeg is needed at runtime for media probing/transcoding. Whisper
  # transcription additionally needs a Python toolchain the user sets up
  # via `maktaba-server setup`.
  depends_on "ffmpeg"
  depends_on :macos

  # The unified binary supervises (forks) these siblings, so ship all
  # three onto PATH.
  def install
    bin.install "maktaba-server"
    bin.install "maktaba-api"
    bin.install "maktaba-streaming"

    # Seed a default config the first time only; never clobber an edited one.
    (etc/"maktaba").mkpath
    (etc/"maktaba/server.toml").write(File.read("server.toml.example")) unless (etc/"maktaba/server.toml").exist?

    (var/"maktaba").mkpath
    (var/"log/maktaba").mkpath
  end

  service do
    run [opt_bin/"maktaba-server", "serve"]
    keep_alive true
    log_path var/"log/maktaba/server.log"
    error_log_path var/"log/maktaba/server.err.log"
    working_dir var/"maktaba"
    environment_variables MAKTABA_CONFIG: etc/"maktaba/server.toml"
  end

  def caveats
    <<~EOS
      Edit your config, then start the service:
        $EDITOR #{etc}/maktaba/server.toml
        brew services start maktaba

      Web UI: http://localhost:8088
    EOS
  end

  test do
    assert_match "maktaba-server", shell_output("#{bin}/maktaba-server --version")
  end
end
