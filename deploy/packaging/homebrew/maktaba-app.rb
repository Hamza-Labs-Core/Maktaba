# Homebrew cask for the Maktaba Mac app (Apple silicon: the app that runs the server).
#
# This file is a TEMPLATE. packages.yml fills the __VERSION__ / __SHA256_*__ placeholders
# (tools/release/homebrew-tap.sh cask) with the tag and the SHA-256 of the release's app
# bundle, once its updater signature checks against the apps' key, and commits the result
# to the tap with the formula when the release is published as the newest stable one. Do
# NOT hand-edit the placeholders.
#
# Users then:  brew install --cask hamza-labs-core/tap/maktaba-app
#
# The app updates itself (its updater, and the home's orders: auto_updates), so brew does
# not need to upgrade it.
cask "maktaba-app" do
  version "__VERSION__"
  sha256 "__SHA256_APP_AARCH64__"

  url "https://github.com/Hamza-Labs-Core/Maktaba/releases/download/v#{version}/Maktaba_#{version}_aarch64.app.tar.gz"
  name "Maktaba"
  desc "Self-hosted Islamic / Arabic media library with transcription + search"
  homepage "https://maktaba.tv"

  livecheck do
    url :url
    strategy :github_latest
  end

  auto_updates true
  depends_on arch: :arm64
  depends_on macos: ">= :sequoia"

  app "Maktaba.app"

  # The library's database and the server live in ~/Library/Application Support/Maktaba:
  # never removed here.
  zap trash: [
    "~/Library/Caches/com.hamzalabs.maktaba",
    "~/Library/Logs/Maktaba",
    "~/Library/Preferences/com.hamzalabs.maktaba.plist",
    "~/Library/Saved Application State/com.hamzalabs.maktaba.savedState",
    "~/Library/WebKit/com.hamzalabs.maktaba",
  ]
end
