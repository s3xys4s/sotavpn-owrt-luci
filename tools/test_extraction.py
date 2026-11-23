"""Regression checks using the released archive, without changing its contents."""

import json
from pathlib import Path
import sys
import tempfile
import unittest

from extract import BASELINE, extract

PACKAGE = None
SOURCE = "https://github.com/sotavpn/sotavpn-app/releases/download/v1.8.11/sotaconnect_arm64.deb"
DIGEST = "44ec209c690ed718879486341529dd89e01b3783790b306c2b5cfe7681ecedbd"


class Extraction(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)

    def candidate(self, name="out", digest=DIGEST, arch="arm64", baseline=BASELINE):
        extract(PACKAGE, self.root / name, SOURCE, digest, "1.8.11+1892", arch, baseline)

    def test_repeatable(self):
        self.candidate("first")
        self.candidate("second")
        for file in ("manifest.json", "inventory.json", "kittend"):
            self.assertEqual((self.root / "first" / file).read_bytes(),
                             (self.root / "second" / file).read_bytes())
        with self.assertRaisesRegex(ValueError, "exists"):
            self.candidate("first")

    def test_digest_and_arch(self):
        for args, msg in (({"digest": "0" * 64}, "SHA-256"), ({"arch": "amd64"}, "identity")):
            with self.assertRaisesRegex(ValueError, msg):
                self.candidate(**args)
            self.assertFalse((self.root / "out").exists())

    def test_contract_drift(self):
        for field in ("wire_types", "layout_sha256"):
            contract = json.loads(BASELINE.read_text())
            contract[field] = "different reviewed contract"
            alternate = self.root / "other.json"
            alternate.write_text(json.dumps(contract))
            with self.assertRaisesRegex(ValueError, field):
                self.candidate(baseline=alternate)
            self.assertFalse((self.root / "out").exists())


if __name__ == "__main__":
    PACKAGE = Path(sys.argv.pop(1))
    unittest.main()
