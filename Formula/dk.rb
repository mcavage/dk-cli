# THIS FILE IS THE SOURCE OF TRUTH for mcavage/homebrew-tap's Formula/dk.rb.
#
# The `update homebrew tap` step in .github/workflows/release.yml copies this
# file over the tap's copy on every release and then rewrites exactly three
# things: `version`, all four `url`s, and all four `sha256`s. Everything else,
# notably `def install`, ships to users verbatim from here. Editing the tap
# directly will be silently overwritten by the next release; edit this file.
#
# WHY IT LIVES HERE. The tarball's contents and the formula's `install` block
# have to agree, and if they live in two repos nothing compares them. That is
# not hypothetical: the sibling pix formula shipped a broken release exactly
# this way, by installing a man page the tarball had stopped carrying. A test
# in this repo asserts this formula only installs paths `make dist` actually
# stages into the tarball, so that drift fails at review time rather than at
# someone's terminal.
#
# The version/url/sha256 values below are LAST RELEASE'S. They are placeholders
# the release job rewrites; do not hand-maintain them.
class Dk < Formula
  desc "Agent-first CLI for DigiKey: price a BOM, hand off a cart"
  homepage "https://github.com/mcavage/dk-cli"
  # Required: Homebrew otherwise parses the archive suffix "arm64" as version
  # "64" and installs into Cellar/dk/64.
  version "0.0.0"
  license "MIT"

  livecheck do
    url :stable
    strategy :github_latest
  end

  on_macos do
    on_arm do
      url "https://github.com/mcavage/dk-cli/releases/download/v0.0.0/dk_0.0.0_darwin_arm64.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
    on_intel do
      url "https://github.com/mcavage/dk-cli/releases/download/v0.0.0/dk_0.0.0_darwin_amd64.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/mcavage/dk-cli/releases/download/v0.0.0/dk_0.0.0_linux_arm64.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
    on_intel do
      url "https://github.com/mcavage/dk-cli/releases/download/v0.0.0/dk_0.0.0_linux_amd64.tar.gz"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
  end

  def install
    bin.install "dk"
    # AGENTS.md is not documentation trivia here: it is the file an agent reads
    # to use this tool correctly, and `dk agents-md` embeds the same content.
    doc.install "LICENSE", "README.md", "AGENTS.md"
  end

  def caveats
    <<~EOS
      Run `dk doctor` to check credentials and connectivity, and `dk help`
      to get started.

      dk cannot place orders. It prices a BOM and hands off a cart URL;
      buying stays a human action in a browser.
    EOS
  end

  test do
    # `dk version` needs no credentials and no network, so this is a real test
    # rather than a smoke test that would pass on a broken binary.
    assert_match version.to_s, shell_output("#{bin}/dk version")
    # Help must work too: it is the first thing a new user runs.
    assert_match "price a DigiKey BOM", shell_output("#{bin}/dk help")
  end
end
