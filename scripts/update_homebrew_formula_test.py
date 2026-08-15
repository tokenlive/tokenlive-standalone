import pathlib
import subprocess
import sys
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
UPDATER = ROOT / "scripts" / "update_homebrew_formula.py"
CANONICAL = ROOT / "packaging" / "homebrew" / "tokenlive.rb"
VERSION = "9.8.7"
ARM64_URL = "https://example.test/tokenlive-arm64.tar.gz"
ARM64_SHA256 = "a" * 64
AMD64_URL = "https://example.test/tokenlive-amd64.tar.gz"
AMD64_SHA256 = "b" * 64


class UpdateHomebrewFormulaTest(unittest.TestCase):
    def run_updater(self, formula, check=False):
        return subprocess.run(
            [
                sys.executable,
                str(UPDATER),
                "--formula",
                str(formula),
                "--version",
                VERSION,
                "--arm64-url",
                ARM64_URL,
                "--arm64-sha256",
                ARM64_SHA256,
                "--amd64-url",
                AMD64_URL,
                "--amd64-sha256",
                AMD64_SHA256,
            ],
            check=check,
            capture_output=True,
            text=True,
        )

    def test_updates_release_metadata_and_preserves_install_logic(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            formula = pathlib.Path(temp_dir) / "tokenlive.rb"
            formula.write_text(CANONICAL.read_text())

            self.run_updater(formula, check=True)

            text = formula.read_text()
            self.assertIn(f'version "{VERSION}"', text)
            amd64_block = (
                "  if Hardware::CPU.intel?\n"
                f'    url "{AMD64_URL}"\n'
                f'    sha256 "{AMD64_SHA256}"\n'
            )
            arm64_block = (
                "  else\n"
                f'    url "{ARM64_URL}"\n'
                f'    sha256 "{ARM64_SHA256}"\n'
            )
            self.assertEqual(text.count(amd64_block), 1)
            self.assertEqual(text.count(arm64_block), 1)
            self.assertIn('libexec/"install-brew-config.sh"', text)
            self.assertIn("config.yml.default", text)

    def test_rejects_duplicate_version_without_changing_formula(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            formula = pathlib.Path(temp_dir) / "tokenlive.rb"
            original = CANONICAL.read_text().replace(
                '  version "0.6.0"\n',
                '  version "0.6.0"\n  version "0.5.0"\n',
                1,
            )
            formula.write_text(original)

            result = self.run_updater(formula)

            self.assertNotEqual(result.returncode, 0)
            self.assertIn(
                "failed to update version: expected 1 match, got 2",
                result.stderr,
            )
            self.assertEqual(formula.read_text(), original)

    def test_rejects_duplicate_architecture_block_without_changing_formula(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            formula = pathlib.Path(temp_dir) / "tokenlive.rb"
            original = CANONICAL.read_text()
            architecture_block = (
                "  if Hardware::CPU.intel?\n"
                '    url "https://github.com/tokenlive/tokenlive-standalone/'
                'releases/download/v0.6.0/tokenlive-0.6.0-darwin-amd64.tar.gz"\n'
                f'    sha256 "{"0" * 64}"\n'
                "  else\n"
                '    url "https://github.com/tokenlive/tokenlive-standalone/'
                'releases/download/v0.6.0/tokenlive-0.6.0-darwin-arm64.tar.gz"\n'
                f'    sha256 "{"0" * 64}"\n'
                "  end\n"
            )
            self.assertIn(architecture_block, original)
            original = original.replace(
                architecture_block,
                architecture_block + "\n" + architecture_block,
                1,
            )
            formula.write_text(original)

            result = self.run_updater(formula)

            self.assertNotEqual(result.returncode, 0)
            self.assertIn(
                "failed to update amd64 release: expected 1 match, got 2",
                result.stderr,
            )
            self.assertEqual(formula.read_text(), original)


if __name__ == "__main__":
    unittest.main()
