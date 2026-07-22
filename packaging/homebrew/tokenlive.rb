# typed: false
# frozen_string_literal: true

# Homebrew Formula for TokenLive all-in-one.
#
# Local install (sibling gateway + admin checkouts):
#   ./scripts/brew-install-local.sh
#
# After install:
#   brew services start tokenlive   # when formula is registered
#   tokenlive-start                 # LaunchAgent helper
#   tokenlive                       # foreground (paths baked in)
#
class Tokenlive < Formula
  desc "TokenLive all-in-one LLM API gateway and admin console"
  homepage "https://github.com/tokenlive/tokenlive-standalone"
  version "0.2.0"
  license "Apache-2.0"

  head "https://github.com/tokenlive/tokenlive-standalone.git", branch: "master"

  depends_on "go" => :build
  depends_on "node" => :build
  depends_on "rsync" => :build

  def install
    gateway = ENV.fetch("TOKENLIVE_GATEWAY_SRC")
    admin = ENV.fetch("TOKENLIVE_ADMIN_SRC")
    ENV["TOKENLIVE_GATEWAY_SRC"] = File.expand_path(gateway)
    ENV["TOKENLIVE_ADMIN_SRC"] = File.expand_path(admin)
    ENV["VERSION"] = version.to_s
    ENV["OUT_DIR"] = (buildpath/"stage").to_s
    ENV["BREW_PREFIX"] = HOMEBREW_PREFIX.to_s

    system "bash", "scripts/package-release.sh"

    bin.install "stage/bin/tokenlive"
    (pkgshare/"admin").install Dir["stage/share/tokenlive/admin/*"]
    (pkgshare/"web").mkpath
    if (buildpath/"stage/share/tokenlive/web/index.html").exist?
      (pkgshare/"web").install Dir["stage/share/tokenlive/web/*"]
    end

    (etc/"tokenlive").mkpath
    (etc/"tokenlive").install "stage/etc/tokenlive/config.yml" unless (etc/"tokenlive/config.yml").exist?
    rm_f etc/"tokenlive/config.example.yml"
    (etc/"tokenlive").install "stage/etc/tokenlive/config.example.yml"
    (var/"tokenlive").mkpath
  end

  def caveats
    <<~EOS
      Start:
        brew services start tokenlive
        # or: tokenlive

      Open http://127.0.0.1:2525 — login admin / admin
      Config: #{etc}/tokenlive/config.yml
    EOS
  end

  # Binary-only service; conf/data/admin/web come from build-time defaults.
  service do
    run [opt_bin/"tokenlive"]
    keep_alive true
    working_dir var/"tokenlive"
    log_path var/"log/tokenlive.log"
    error_log_path var/"log/tokenlive.err.log"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/tokenlive -version")
  end
end
