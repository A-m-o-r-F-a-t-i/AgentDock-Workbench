#!/usr/bin/env python3
"""Exercise Android Core release signing with synthetic ARM64 payloads."""
from __future__ import annotations

import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location(
    "android_core_release", ROOT / "scripts/release/sign-android-core-release.py"
)
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)


def openssl() -> str:
    configured = os.environ.get("OPENSSL_BIN", "")
    if configured and Path(configured).is_file():
        return configured
    resolved = shutil.which("openssl")
    if resolved:
        return resolved
    if os.name == "nt":
        candidate = Path(r"C:\Program Files\Git\mingw64\bin\openssl.exe")
        if candidate.is_file():
            return str(candidate)
    raise unittest.SkipTest("OpenSSL is unavailable")


class AndroidCoreReleaseSigning(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.inputs = self.root / "input"
        self.output = self.root / "output"
        self.inputs.mkdir()
        self.version = "1.1.9"
        self.commit = "a" * 40
        self.private_key = self.root / "private.pem"
        self.public_key = self.root / "public.pem"
        self.config = self.root / "identity.json"
        subprocess.run([openssl(), "genpkey", "-algorithm", "Ed25519", "-out", str(self.private_key)], check=True)
        subprocess.run([openssl(), "pkey", "-in", str(self.private_key), "-pubout", "-out", str(self.public_key)], check=True)
        public_der = subprocess.check_output(
            [openssl(), "pkey", "-pubin", "-in", str(self.public_key), "-outform", "DER"]
        )
        import hashlib
        self.config.write_text(json.dumps({
            "schema_version": 1,
            "algorithm": "Ed25519",
            "public_key": str(self.public_key),
            "public_key_sha256": hashlib.sha256(public_der).hexdigest(),
        }), encoding="utf-8")
        self.archive = self.inputs / "agentdock_linux_arm64.tar.gz"
        with tarfile.open(self.archive, "w:gz") as bundle:
            directory = tarfile.TarInfo("bin")
            directory.type = tarfile.DIRTYPE
            directory.mode = 0o755
            bundle.addfile(directory)
            binary = tarfile.TarInfo("bin/agentdock")
            payload = b"fixture-arm64-core"
            binary.size = len(payload)
            binary.mode = 0o755
            bundle.addfile(binary, io.BytesIO(payload))
        checksum = release.digest(self.archive)
        (self.inputs / (self.archive.name + ".sha256")).write_text(
            f"{checksum}  {self.archive.name}\n", encoding="utf-8"
        )
        (self.inputs / "verification-linux-arm64.json").write_text(json.dumps({
            "product_name": release.PRODUCT,
            "version": self.version,
            "commit": self.commit,
            "platform": "linux/arm64",
            "native_execution": "passed",
            "assets": {self.archive.name: checksum},
        }), encoding="utf-8")
        self.openssl_patch = patch.dict(os.environ, {"OPENSSL_BIN": openssl()})
        self.openssl_patch.start()
        self.addCleanup(self.openssl_patch.stop)

    def create(self) -> dict:
        return release.create_release(
            self.inputs, self.output, self.version, self.commit,
            self.private_key, self.config,
        )

    def test_signed_manifest_binds_verified_archive(self) -> None:
        report = self.create()
        self.assertEqual(report["release_signing_validation"], "passed")
        self.assertEqual(report["platform"], "android-core/arm64")
        self.assertEqual(len(report["assets"]), 3)
        manifest_path = self.output / report["manifest"]["name"]
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        archive_path = self.output / report["manifest"]["asset"]
        self.assertEqual(manifest["sha256"], release.digest(archive_path))
        self.assertEqual(manifest["platform"], "linux")
        self.assertEqual(manifest["arch"], "arm64")
        self.assertEqual(manifest["version"], self.version)
        self.assertEqual(manifest["commit"], self.commit)
        self.assertTrue(manifest["asset_url"].endswith("/" + archive_path.name))
        signature = self.output / report["manifest"]["signature"]
        self.assertEqual(signature.stat().st_size, 64)
        for name, checksum in report["assets"].items():
            self.assertEqual(release.digest(self.output / name), checksum)
            self.assertEqual(
                (self.output / (name + ".sha256")).read_text().strip(),
                f"{checksum}  {name}",
            )

    def test_modified_source_archive_is_rejected(self) -> None:
        self.archive.write_bytes(self.archive.read_bytes() + b"changed")
        with self.assertRaisesRegex(RuntimeError, "verification report"):
            self.create()

    def test_wrong_private_key_is_rejected(self) -> None:
        other = self.root / "other.pem"
        subprocess.run([openssl(), "genpkey", "-algorithm", "Ed25519", "-out", str(other)], check=True)
        with self.assertRaisesRegex(RuntimeError, "does not match"):
            release.create_release(
                self.inputs, self.output, self.version, self.commit,
                other, self.config,
            )

    def test_unsafe_archive_is_rejected(self) -> None:
        with tarfile.open(self.archive, "w:gz") as bundle:
            entry = tarfile.TarInfo("../agentdock")
            payload = b"unsafe"
            entry.size = len(payload)
            entry.mode = 0o755
            bundle.addfile(entry, io.BytesIO(payload))
        checksum = release.digest(self.archive)
        (self.inputs / (self.archive.name + ".sha256")).write_text(
            f"{checksum}  {self.archive.name}\n", encoding="utf-8"
        )
        report = json.loads((self.inputs / "verification-linux-arm64.json").read_text())
        report["assets"][self.archive.name] = checksum
        (self.inputs / "verification-linux-arm64.json").write_text(json.dumps(report))
        with self.assertRaisesRegex(RuntimeError, "unsafe path"):
            self.create()


if __name__ == "__main__":
    unittest.main()
