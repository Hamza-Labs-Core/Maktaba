# Homebrew formula for the Maktaba home server (binary install).
#
# This file is a TEMPLATE. The release workflow fills the __VERSION__ / __SHA256_*__
# placeholders with the tag and the SHA-256 of each macOS release archive
# (tools/release/homebrew-tap.sh formula), attaches the result to the release (listed in its
# signed checksums.txt), and packages.yml commits that file to the tap once the release is
# published as the newest stable one. Do NOT hand-edit the placeholders.
#
# Users then:  brew install hamza-labs-core/tap/maktaba
#
# Like the .deb/.rpm, it updates itself (Epic 35): brew owns the launcher, the server's
# updater owns the releases. The service stages the release this formula ships in the
# self-managed layout under var/maktaba/server (only when it is newer than what the updater
# installed there) and runs the launcher, which runs the current release; the updater puts
# each newer release beside it in the quiet window, verified like every install. A
# `brew upgrade` brings a new launcher (and its release, when newer); nothing else needs it.
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

  def install
    # The release this formula ships, beside brew's files; the service copies it into the
    # layout (`setup --self-managed --identity package`, as the packages' postinstall does).
    (libexec/"release").install "maktaba-server", "maktaba-api", "maktaba-streaming", "maktaba-launcher"

    # What brew's service runs: stage the release, then hand over to the launcher.
    (libexec/"maktaba-service").write <<~SH
      #!/bin/sh
      ROOT="#{var}/maktaba/server"
      "#{opt_libexec}/release/maktaba-server" setup --self-managed --no-service \\
        --root "$ROOT" --from "#{opt_libexec}/release" --identity package ||
        echo "maktaba: could not stage the release under $ROOT" >&2
      exec "#{opt_libexec}/release/maktaba-launcher" --root "$ROOT" serve
    SH
    chmod 0755, libexec/"maktaba-service"

    # The CLI on PATH runs the release the service runs (it follows the updates); before
    # the service's first start, the one this formula shipped.
    (bin/"maktaba-server").write <<~SH
      #!/bin/sh
      ROOT="#{var}/maktaba/server"
      cur=""
      [ -r "$ROOT/current" ] && cur=$(tr -d '[:space:]' <"$ROOT/current" 2>/dev/null)
      case "$cur" in ""|*[!0-9A-Za-z.+_-]*) cur="" ;; esac
      if [ -n "$cur" ] && [ -x "$ROOT/bin/releases/$cur/maktaba-server" ]; then
        exec "$ROOT/bin/releases/$cur/maktaba-server" "$@"
      fi
      exec "#{opt_libexec}/release/maktaba-server" "$@"
    SH
    chmod 0755, bin/"maktaba-server"
    bin.install_symlink libexec/"release/maktaba-launcher"

    # Seed a default config the first time only; never clobber an edited one.
    (etc/"maktaba").mkpath
    (etc/"maktaba/server.toml").write(File.read("server.toml.example")) unless (etc/"maktaba/server.toml").exist?
  end

  def post_install
    (var/"maktaba/server").mkpath
    (var/"log/maktaba").mkpath
  end

  service do
    run [opt_libexec/"maktaba-service"]
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

      Maktaba updates itself: the service runs maktaba-launcher, and the server installs
      each new release under #{var}/maktaba/server in its quiet window (Settings >
      Updates). `brew upgrade maktaba` brings a newer launcher.
      After upgrading from an older formula (whose service ran maktaba-server itself),
      restart the service once so it runs the launcher: brew services restart maktaba
    EOS
  end

  test do
    assert_match "maktaba-server", shell_output("#{bin}/maktaba-server --version")
  end
end
