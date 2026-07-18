# typed: false
# frozen_string_literal: true

# Homebrew Formula for TokenLive all-in-one (Gateway + Admin).
#
# Local install (recommended while modules use path replace):
#
#   cd tokenlive-standalone
#   ./scripts/brew-install-local.sh
#
# Manual:
#
#   export TOKENLIVE_STANDALONE_SRC=$PWD
#   export TOKENLIVE_GATEWAY_SRC=$PWD/../tokenlive-gateway
#   export TOKENLIVE_ADMIN_SRC=$PWD/../tokenlive-admin
#   brew install --build-from-source ./packaging/homebrew/tokenlive.rb
#
# After install:
#
#   brew services start tokenlive
#   open http://127.0.0.1:2525   # admin / admin
#
class Tokenlive < Formula
  desc "TokenLive all-in-one LLM API gateway and admin console"
  homepage "https://github.com/tokenlive/tokenlive-standalone"
  version "0.1.0"
  license "Apache-2.0"

  # Local tree (set by scripts/brew-install-local.sh). Falls back to GitHub HEAD.
  if (src = ENV["TOKENLIVE_STANDALONE_SRC"]) && File.directory?(src)
    url "file://#{File.expand_path(src)}", using: :git, tag: nil, revision: nil, branch: "HEAD"
  else
    head "https://github.com/tokenlive/tokenlive-standalone.git", branch: "master"
  end

  depends_on "go" => :build
  depends_on "node" => :build
  depends_on "rsync" => :build

  def install
    gateway = ENV.fetch("TOKENLIVE_GATEWAY_SRC") do
      candidates = [
        Pathname.new(__dir__)/"../../../tokenlive-gateway",
        buildpath/"../tokenlive-gateway",
        HOMEBREW_PREFIX/"../tokenlive-gateway",
      ]
      found = candidates.map { |p| Pathname.new(p).expand_path }.find { |p| (p/"go.mod").exist? }
      odie <<~EOS if found.nil?
        tokenlive-gateway source not found.
        Set TOKENLIVE_GATEWAY_SRC or run ./scripts/brew-install-local.sh from a sibling checkout layout.
      EOS
      found
    end
    admin = ENV.fetch("TOKENLIVE_ADMIN_SRC") do
      candidates = [
        Pathname.new(__dir__)/"../../../tokenlive-admin",
        buildpath/"../tokenlive-admin",
        HOMEBREW_PREFIX/"../tokenlive-admin",
      ]
      found = candidates.map { |p| Pathname.new(p).expand_path }.find { |p| (p/"go.mod").exist? }
      odie <<~EOS if found.nil?
        tokenlive-admin source not found.
        Set TOKENLIVE_ADMIN_SRC or run ./scripts/brew-install-local.sh
      EOS
      found
    end

    gateway = Pathname.new(gateway).expand_path
    admin = Pathname.new(admin).expand_path
    odie "invalid gateway: #{gateway}" unless (gateway/"go.mod").exist?
    odie "invalid admin: #{admin}" unless (admin/"go.mod").exist?

    ENV["TOKENLIVE_GATEWAY_SRC"] = gateway.to_s
    ENV["TOKENLIVE_ADMIN_SRC"] = admin.to_s
    ENV["VERSION"] = version.to_s
    ENV["OUT_DIR"] = (buildpath/"stage").to_s

    system "bash", "scripts/package-release.sh"

    bin.install "stage/bin/tokenlive"
    pkgshare.mkpath
    (pkgshare/"admin").install Dir["stage/share/tokenlive/admin/*"]
    (pkgshare/"web").mkpath
    web_index = buildpath/"stage/share/tokenlive/web/index.html"
    (pkgshare/"web").install Dir["stage/share/tokenlive/web/*"] if web_index.exist?

    (etc/"tokenlive").mkpath
    config_dst = etc/"tokenlive/config.yml"
    unless config_dst.exist?
      (etc/"tokenlive").install "stage/etc/tokenlive/config.yml"
    end
    # Always refresh example
    rm_f etc/"tokenlive/config.example.yml"
    (etc/"tokenlive").install "stage/etc/tokenlive/config.example.yml"

    (var/"tokenlive").mkpath
    (var/"log").mkpath
  end

  def caveats
    <<~EOS
      TokenLive all-in-one installed.

        Config:  #{etc}/tokenlive/config.yml
        Data:    #{var}/tokenlive
        Admin:   #{opt_pkgshare}/admin
        Web UI:  #{opt_pkgshare}/web

      Start:
        brew services start tokenlive

      Open:  http://127.0.0.1:2525
      Login: admin / admin

      Dual-process production still uses tokenlive-gateway + tokenlive-admin separately.
    EOS
  end

  service do
    run [
      opt_bin/"tokenlive",
      "-conf", etc/"tokenlive/config.yml",
      "-data-dir", var/"tokenlive",
      "-admin-workdir", opt_pkgshare/"admin",
      "-admin-static", opt_pkgshare/"web",
    ]
    keep_alive true
    working_dir var/"tokenlive"
    log_path var/"log/tokenlive.log"
    error_log_path var/"log/tokenlive.err.log"
    environment_variables PATH: std_service_path_env
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/tokenlive -version")
  end
end
