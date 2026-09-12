"""Discoverable packaging tests in disposable fixtures; never publish or install."""

import json
import os
import pathlib
import shutil
import subprocess
import sys
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]

# External build/network commands are the only fakes. Real shell packaging,
# file copying, archiving, checksums and Formula updates still execute.
FAKE_TOOL = r"""
import json, os, pathlib, shutil, sys
name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
with open(os.environ["COMMAND_LOG"], "a") as log:
    log.write(json.dumps({"tool": name, "args": args,
        "cwd": os.getcwd(), "version": os.environ.get("VITE_APP_VERSION"),
        "gowork": os.environ.get("GOWORK")}) + "\n")
if name == "go":
    if args[:2] == ["env", "GOARCH"]:
        print("arm64")
    elif args and args[0] == "build":
        target = pathlib.Path(args[args.index("-o") + 1])
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(json.dumps(args))
    elif args[:2] not in (["mod", "edit"], ["mod", "tidy"]):
        sys.exit("unexpected go operation")
elif name == "npm":
    if args == ["run", "build:prod"]:
        pathlib.Path("dist").mkdir(exist_ok=True)
        pathlib.Path("dist/index.html").write_text(os.environ["VITE_APP_VERSION"])
    elif args != ["ci"]:
        sys.exit("unexpected npm operation")
elif name == "rsync":
    excludes = []
    for i, arg in enumerate(args[:-2]):
        if arg == "--exclude":
            excludes.append(args[i + 1])
    src, dest = map(pathlib.Path, args[-2:])
    shutil.copytree(src, dest, dirs_exist_ok=True,
                    ignore=shutil.ignore_patterns(*excludes))
elif name == "gh":
    state_path = pathlib.Path(os.environ["RELEASE_STATE_PATH"])
    state = (state_path.read_text() if state_path.exists()
             else os.environ.get("INITIAL_RELEASE_STATE", "missing"))
    if args[:2] == ["release", "view"]:
        if state == "missing":
            sys.exit(1)
        if "--json" in args:
            fields = args[args.index("--json") + 1]
            if fields == "url" and args[-2:] == ["-q", ".url"]:
                print("https://example.test/releases/v9.8.7")
            elif fields == "isDraft" and args[-2:] == ["-q", ".isDraft"]:
                if os.environ.get("FAIL_RELEASE_VERIFY") == "1":
                    sys.exit(1)
                print("true" if state == "draft" else "false")
            else:
                sys.exit("unexpected release JSON query")
    elif args[:2] in (["release", "create"], ["release", "upload"]):
        if os.environ.get("FAIL_RELEASE_ASSETS") == "1":
            sys.exit(1)
        for asset in args[3:]:
            if asset.startswith("--"):
                break
            if not pathlib.Path(asset).is_file():
                sys.exit("release asset does not exist")
        if args[1] == "create":
            state = "draft" if "--draft" in args else "public"
            state_path.write_text(state)
    elif args[:2] == ["release", "edit"]:
        if "--draft=false" in args:
            if os.environ.get("FAIL_RELEASE_PUBLISH") == "1":
                sys.exit(1)
            if os.environ.get("KEEP_RELEASE_DRAFT") != "1":
                state = "public"
            state_path.write_text(state)
    else:
        sys.exit("unexpected gh operation")
elif name == "git":
    if args and args[0] == "clone":
        formula = pathlib.Path(args[-1]) / "Formula/tokenlive.rb"
        formula.parent.mkdir(parents=True)
        formula.write_text("old formula")
    elif "diff" in args:
        sys.exit(1)
    elif "push" in args:
        sys.exit(int(os.environ.get("FAIL_TAP_PUSH", "0")))
    elif not any(operation in args for operation in ("config", "add", "commit")):
        sys.exit("unexpected git operation")
else:
    sys.exit("unexpected fake tool")
"""


class PackageVersionTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="tokenlive-package-test-")
        self.addCleanup(self.temp.cleanup)
        self.base = pathlib.Path(self.temp.name)
        self.root = self.base / "standalone"
        self.root.mkdir()
        for name in ("scripts", "config", "configs", "packaging"):
            shutil.copytree(ROOT / name, self.root / name)
        (self.root / "go.mod").write_text("module fixture\n")
        (self.root / "go.sum").write_text("unchanged\n")
        self.admin = self.base / "admin"
        self.gateway = self.base / "gateway"
        self.frontend = self.admin / "frontend"
        for source in (self.admin, self.gateway):
            source.mkdir()
            (source / "go.mod").write_text("module fixture\n")
        (self.frontend / "dist").mkdir(parents=True)
        (self.frontend / "dist/index.html").write_text("stale-version")
        (self.frontend / "package.json").write_text("{}")
        self.tools = self.base / "tools"
        self.tools.mkdir()
        for tool in ("go", "npm", "rsync", "gh", "git"):
            path = self.tools / tool
            path.write_text(f"#!{sys.executable}\n" + FAKE_TOOL)
            path.chmod(0o755)
        self.log = self.base / "commands.jsonl"
        self.out = self.base / "package"
        self.env = {
            "PATH": f"{self.tools}:/usr/bin:/bin:/usr/sbin:/sbin",
            "HOME": str(self.base),
            "TMPDIR": str(self.base),
            "COMMAND_LOG": str(self.log),
            "RELEASE_STATE_PATH": str(self.base / "release-state"),
            "TOKENLIVE_GATEWAY_SRC": str(self.gateway),
            "TOKENLIVE_ADMIN_SRC": str(self.admin),
            "OUT_DIR": str(self.out),
            "GOWORK": str(self.base / "must-not-leak.work"),
        }

    def run_script(self, script="package-release.sh", **env):
        result = subprocess.run(
            ["bash", str(self.root / "scripts" / script)],
            cwd=self.root,
            env={**self.env, **env},
            capture_output=True,
            text=True,
        )
        return result

    def assert_ok(self, result):
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def commands(self, tool=None):
        if not self.log.exists():
            return []
        return [
            entry for line in self.log.read_text().splitlines()
            if (entry := json.loads(line))["tool"] == tool or tool is None
        ]

    def publication_events(self):
        events = []
        for entry in self.commands():
            args = entry["args"]
            if entry["tool"] == "gh":
                operation = args[1]
                if operation == "view" and "--json" in args:
                    operation += ":" + args[args.index("--json") + 1]
                elif operation == "edit":
                    operation = "publish" if "--draft=false" in args else "notes"
                events.append(operation)
            elif entry["tool"] == "git":
                if args[0] == "clone":
                    events.append("tap-clone")
                elif "push" in args:
                    events.append("tap-push")
        return events

    def run_brew_publish(self, state, **env):
        return self.run_script(
            "publish-brew-release.sh", VERSION="v9.8.7", GH_TOKEN="fixture",
            INITIAL_RELEASE_STATE=state, GITHUB_OUTPUT=str(self.base / "github-output"),
            **env,
        )

    def assert_brew_assets(self, operation):
        calls = [entry for entry in self.commands("gh") if entry["args"][1] == operation]
        self.assertEqual(len(calls), 1)
        self.assertEqual(
            [pathlib.Path(arg).name for arg in calls[0]["args"][3:7]],
            [
                "tokenlive-9.8.7-darwin-arm64.tar.gz",
                "tokenlive-9.8.7-darwin-arm64.tar.gz.sha256",
                "tokenlive-9.8.7-darwin-amd64.tar.gz",
                "tokenlive-9.8.7-darwin-amd64.tar.gz.sha256",
            ],
        )

    def assert_not_ready(self, result):
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.commands("git"), [], "failed public-release gate must not touch tap")
        self.assertNotIn("Homebrew ready", result.stdout)
        output = self.base / "github-output"
        self.assertFalse(output.exists() and "homebrew_ready=true" in output.read_text())

    def binary_flags(self, package=None):
        args = json.loads(((package or self.out) / "bin/tokenlive").read_text())
        return next(arg for arg in args if arg.startswith("-ldflags="))

    def test_release_rebuilds_stale_frontend_even_if_skip_requested(self):
        result = self.run_script(
            VERSION="v9.8.7", BUILD_KIND="release", SKIP_WEB="1",
            FORCE_WEB_BUILD="0",
        )
        self.assert_ok(result)
        self.assertEqual(
            (self.out / "share/tokenlive/web/index.html").read_text(), "v9.8.7"
        )
        self.assertIn("main.version=v9.8.7", self.binary_flags())
        self.assertIn("main.buildKind=release", self.binary_flags())
        self.assertTrue(self.commands("npm"))

    def test_local_package_defaults_to_dev_without_guessing_a_release(self):
        self.assert_ok(self.run_script())
        self.assertIn("main.version=dev", self.binary_flags())
        self.assertIn("main.buildKind=dev", self.binary_flags())

    def test_explicit_semver_does_not_promote_local_build_to_release(self):
        self.assert_ok(self.run_script(VERSION="v9.8.7"))
        self.assertIn("main.version=v9.8.7", self.binary_flags())
        self.assertIn("main.buildKind=dev", self.binary_flags())

    def test_release_rejects_image_alias_before_building(self):
        for kind in ("release", "dev"):
            with self.subTest(kind=kind):
                result = self.run_script(VERSION="latest", BUILD_KIND=kind)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse(self.commands("go"))

    def test_release_requires_frontend_source_instead_of_reusing_dist(self):
        (self.frontend / "package.json").unlink()
        result = self.run_script(VERSION="v9.8.7", BUILD_KIND="release")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.commands("go"))

    def test_package_owns_temporary_module_graph_and_preserves_source_modules(self):
        self.assert_ok(self.run_script(VERSION="v9.8.7", BUILD_KIND="release"))
        for entry in self.commands("go"):
            self.assertEqual(entry["gowork"], "off")
            self.assertNotEqual(entry["cwd"], str(self.root))
        self.assertEqual((self.root / "go.mod").read_text(), "module fixture\n")
        self.assertEqual((self.root / "go.sum").read_text(), "unchanged\n")

    def test_generic_package_never_contains_homebrew_install_marker(self):
        self.assert_ok(self.run_script(VERSION="v9.8.7", BUILD_KIND="release"))
        self.assertFalse(list(self.out.rglob("tokenlive-install-channel")))

    def test_brew_prefix_package_does_not_claim_it_was_installed_by_homebrew(self):
        self.assert_ok(self.run_script(
            VERSION="v9.8.7", BUILD_KIND="release", BREW_PREFIX="/opt/homebrew"
        ))
        self.assertFalse(list(self.out.rglob("tokenlive-install-channel")))

    def test_publishers_build_both_arches_with_release_metadata(self):
        for script, system, arches in (
            ("publish-brew-release.sh", "darwin", ("arm64", "amd64")),
            ("publish-linux-release.sh", "linux", ("amd64", "arm64")),
        ):
            with self.subTest(script=script):
                self.assert_ok(self.run_script(script, VERSION="v9.8.7", SKIP_RELEASE="1"))
                for arch in arches:
                    package = self.root / f"dist/tokenlive-9.8.7-{system}-{arch}"
                    self.assertIn("main.version=9.8.7", self.binary_flags(package))
                    self.assertIn("main.buildKind=release", self.binary_flags(package))
                    self.assertEqual(
                        (package / "share/tokenlive/web/index.html").read_text(), "9.8.7"
                    )
                    self.assertFalse(list(package.rglob("tokenlive-install-channel")))

    def test_skipping_tap_does_not_announce_homebrew_readiness(self):
        output = self.base / "github-output"
        result = self.run_script(
            "publish-brew-release.sh", VERSION="v9.8.7",
            SKIP_TAP="1", GH_TOKEN="fixture", GITHUB_OUTPUT=str(output),
        )
        self.assert_ok(result)
        self.assertIn("Homebrew is not ready", result.stdout)
        self.assertNotIn("Homebrew ready", result.stdout)
        self.assertFalse(any("push" in entry["args"] for entry in self.commands("git")))
        self.assertFalse(output.exists() and "homebrew_ready=true" in output.read_text())

    def test_homebrew_readiness_requires_successful_tap_push(self):
        output = self.base / "github-output"
        result = self.run_script(
            "publish-brew-release.sh", VERSION="v9.8.7",
            GH_TOKEN="fixture", FAIL_TAP_PUSH="1", GITHUB_OUTPUT=str(output),
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("Homebrew ready", result.stdout)
        self.assertFalse(output.exists() and "homebrew_ready=true" in output.read_text())
        result = self.run_script(
            "publish-brew-release.sh", VERSION="v9.8.7", GH_TOKEN="fixture",
            GITHUB_OUTPUT=str(output),
        )
        self.assert_ok(result)
        self.assertIn("Homebrew ready", result.stdout)
        self.assertTrue(output.exists(), "successful tap update must signal workflow readiness")
        self.assertIn("homebrew_ready=true", output.read_text())

    def test_new_release_is_publicly_verified_before_tap_update(self):
        result = self.run_brew_publish("missing")
        self.assert_ok(result)
        self.assert_brew_assets("create")
        self.assertEqual(
            self.publication_events(),
            ["view", "create", "view:isDraft", "view:url", "tap-clone", "tap-push"],
        )
        self.assertIn("homebrew_ready=true", (self.base / "github-output").read_text())

    def test_public_existing_release_uploads_both_arches_before_public_gate_and_tap(self):
        result = self.run_brew_publish("public")
        self.assert_ok(result)
        self.assert_brew_assets("upload")
        self.assertEqual(
            self.publication_events(),
            ["view", "upload", "publish", "view:isDraft", "view:url", "tap-clone", "tap-push"],
        )
        self.assertIn("homebrew_ready=true", (self.base / "github-output").read_text())

    def test_linux_draft_release_is_published_before_any_tap_update(self):
        result = self.run_brew_publish("draft")
        self.assert_ok(result)
        self.assert_brew_assets("upload")
        self.assertEqual(
            self.publication_events(),
            ["view", "upload", "publish", "view:isDraft", "view:url", "tap-clone", "tap-push"],
        )
        self.assertIn("homebrew_ready=true", (self.base / "github-output").read_text())

    def test_draft_publish_failure_never_updates_tap_or_signals_ready(self):
        result = self.run_brew_publish("draft", FAIL_RELEASE_PUBLISH="1")
        self.assert_not_ready(result)
        self.assertEqual(self.publication_events(), ["view", "upload", "publish"])

    def test_still_draft_after_publish_never_updates_tap_or_signals_ready(self):
        result = self.run_brew_publish("draft", KEEP_RELEASE_DRAFT="1")
        self.assert_not_ready(result)
        self.assertEqual(
            self.publication_events(), ["view", "upload", "publish", "view:isDraft"]
        )

    def test_failed_public_state_query_never_updates_tap_or_signals_ready(self):
        result = self.run_brew_publish("public", FAIL_RELEASE_VERIFY="1")
        self.assert_not_ready(result)
        self.assertEqual(
            self.publication_events(), ["view", "upload", "publish", "view:isDraft"]
        )

    def test_failed_new_release_assets_never_updates_tap_or_signals_ready(self):
        result = self.run_brew_publish("missing", FAIL_RELEASE_ASSETS="1")
        self.assert_not_ready(result)
        self.assertEqual(self.publication_events(), ["view", "create"])

    def test_failed_draft_asset_upload_never_publishes_or_updates_tap(self):
        result = self.run_brew_publish("draft", FAIL_RELEASE_ASSETS="1")
        self.assert_not_ready(result)
        self.assertEqual(self.publication_events(), ["view", "upload"])


if __name__ == "__main__":
    unittest.main()
