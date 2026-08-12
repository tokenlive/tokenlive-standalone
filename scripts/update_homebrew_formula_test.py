import pathlib
import subprocess
import sys
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
UPDATER = ROOT / "scripts" / "update_homebrew_formula.py"
CANONICAL = ROOT / "packaging" / "homebrew" / "tokenlive.rb"


class UpdateHomebrewFormulaTest(unittest.TestCase):
    def test_updates_release_metadata_and_preserves_install_logic(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            formula = pathlib.Path(temp_dir) / "tokenlive.rb"
            formula.write_text(CANONICAL.read_text())

            subprocess.run(
                [
                    sys.executable,
                    str(UPDATER),
                    "--formula",
                    str(formula),
                    "--version",
                    "9.8.7",
                    "--arm64-url",
                    "https://example.test/tokenlive-arm64.tar.gz",
                    "--arm64-sha256",
                    "a" * 64,
                    "--amd64-url",
                    "https://example.test/tokenlive-amd64.tar.gz",
                    "--amd64-sha256",
                    "b" * 64,
                ],
                check=True,
            )

            text = formula.read_text()
            self.assertIn('version "9.8.7"', text)
            self.assertIn(
                'url "https://example.test/tokenlive-arm64.tar.gz"',
                text,
            )
            self.assertIn('sha256 "' + ("a" * 64) + '"', text)
            self.assertIn(
                'url "https://example.test/tokenlive-amd64.tar.gz"',
                text,
            )
            self.assertIn('sha256 "' + ("b" * 64) + '"', text)
            self.assertIn('libexec/"install-brew-config.sh"', text)
            self.assertIn("config.yml.default", text)


if __name__ == "__main__":
    unittest.main()
