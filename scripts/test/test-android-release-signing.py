#!/usr/bin/env python3
"""Isolated stable Android release-signing regressions."""
import base64
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
import zipfile
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location(
    "android_release_signing",
    ROOT / "scripts/release/sign-android-release.py",
)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

CERTIFICATE = "a" * 64


class AndroidReleaseSigningTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        self.inputs = self.root / "inputs"
        self.output = self.root / "output"
        self.inputs.mkdir()
        self.version = "1.1.9"
        self.commit = "b" * 40
        self.core_name = "agentdock_linux_arm64.tar.gz"
        self.core = self.inputs / self.core_name
        self.core.write_bytes(b"verified-linux-arm64-core")
        self.core_checksum = module.digest(self.core)
        (self.inputs / (self.core_name + ".sha256")).write_text(
            f"{self.core_checksum}  {self.core_name}\n"
        )
        (self.inputs / "verification-linux-arm64.json").write_text(json.dumps({
            "product_name": "AgentDock Workbench",
            "version": self.version,
            "commit": self.commit,
            "platform": "linux/arm64",
            "native_execution": "passed",
            "assets": {self.core_name: self.core_checksum},
        }))
        self.candidate_name = f"AgentDock-Workbench-{self.version}-Android-test-signed.apk"
        self.candidate = self.inputs / self.candidate_name
        with zipfile.ZipFile(self.candidate, "w", compression=zipfile.ZIP_STORED) as archive:
            archive.writestr("assets/agentdock-core-arm64.tgz", self.core.read_bytes())
        checksum = module.digest(self.candidate)
        (self.inputs / (self.candidate_name + ".sha256")).write_text(
            f"{checksum}  {self.candidate_name}\n"
        )
        (self.inputs / "verification-android.json").write_text(json.dumps({
            "schema_version": 1,
            "product_name": "AgentDock Workbench",
            "version": self.version,
            "commit": self.commit,
            "platform": "android/arm64",
            "run_id": "123",
            "run_attempt": "1",
            "signing": "test-signed Android debug key",
            "apk_validation": "passed",
            "emulator_validation": "passed",
            "assets": {self.candidate_name: checksum},
        }))
        self.config = self.root / "release-signing.json"
        self.config.write_text(json.dumps({
            "schema_version": 1,
            "key_alias": "agentdock-android-release",
            "certificate_sha256": CERTIFICATE,
        }))
        self.keystore = self.root / "release.p12"
        self.keystore.write_bytes(b"fixture-keystore")
        self.environment = patch.dict(os.environ, {
            "ANDROID_RELEASE_KEYSTORE_PASSWORD": "store-password",
            "ANDROID_RELEASE_KEY_PASSWORD": "key-password",
        })
        self.environment.start()
        self.addCleanup(self.environment.stop)
        self.tool = patch.object(module, "locate_tool", side_effect=lambda name: Path(name))
        self.tool.start()
        self.addCleanup(self.tool.stop)
        self.runner_patch = patch.object(module, "run_tool", side_effect=self.fake_tool)
        self.runner = self.runner_patch.start()
        self.addCleanup(self.runner_patch.stop)
        self.report_certificate = CERTIFICATE
        self.verification_output = None

    def fake_tool(self, tool: Path, *arguments: str):
        name = tool.name
        if name == "zipalign":
            shutil.copy2(arguments[-2], arguments[-1])
            output = "aligned"
        elif name == "apksigner" and arguments[0] == "sign":
            destination = Path(arguments[arguments.index("--out") + 1])
            shutil.copy2(arguments[-1], destination)
            with destination.open("ab") as stream:
                stream.write(b"-stable-signed")
            output = "signed"
        elif name == "apksigner" and arguments[0] == "verify":
            output = self.verification_output or f"Signer #1 certificate SHA-256 digest: {self.report_certificate}\n"
        else:
            raise AssertionError((tool, arguments))
        return subprocess.CompletedProcess([str(tool), *arguments], 0, output, "")

    def sign(self):
        return module.sign_candidate(
            self.inputs,
            self.output,
            self.version,
            self.commit,
            self.keystore,
            self.config,
        )

    def test_verified_candidate_becomes_stably_signed_public_apk(self):
        report = self.sign()
        public = self.output / f"AgentDock-Workbench-{self.version}-Android.apk"
        self.assertTrue(public.is_file())
        self.assertEqual(report["certificate_sha256"], CERTIFICATE)
        self.assertEqual(report["source_apk"]["sha256"], module.digest(self.candidate))
        self.assertEqual(report["bundled_core"], {
            "apk_asset": "assets/agentdock-core-arm64.tgz",
            "source_archive": {"name": self.core_name, "sha256": self.core_checksum},
            "validation": "passed",
        })
        self.assertEqual(report["assets"][public.name], module.digest(public))
        self.assertEqual(
            (self.output / (public.name + ".sha256")).read_text().strip(),
            f"{module.digest(public)}  {public.name}",
        )

    def test_current_apksigner_scheme_output_is_accepted(self):
        self.verification_output = (
            "Verified using v2 scheme (APK Signature Scheme v2): true\n"
            "Number of signers: 1\n"
            f"V2 Signer: certificate SHA-256 digest: {CERTIFICATE}\n"
        )
        self.assertEqual(self.sign()["certificate_sha256"], CERTIFICATE)

    def test_multiple_apksigner_signers_are_rejected(self):
        self.verification_output = (
            "Number of signers: 2\n"
            f"V2 Signer: certificate SHA-256 digest: {CERTIFICATE}\n"
            f"V3 Signer: certificate SHA-256 digest: {'c' * 64}\n"
        )
        with self.assertRaisesRegex(RuntimeError, "exactly one signer"):
            self.sign()
        self.assertFalse((self.output / f"AgentDock-Workbench-{self.version}-Android.apk").exists())

    def test_candidate_byte_change_is_rejected_before_signing(self):
        self.candidate.write_bytes(b"changed")
        with self.assertRaisesRegex(RuntimeError, "differ from verified evidence"):
            self.sign()
        self.runner.assert_not_called()

    def test_candidate_bundled_core_must_match_verified_linux_archive(self):
        with zipfile.ZipFile(self.candidate, "w") as archive:
            archive.writestr("assets/agentdock-core-arm64.tgz", b"different")
        checksum = module.digest(self.candidate)
        (self.inputs / (self.candidate_name + ".sha256")).write_text(
            f"{checksum}  {self.candidate_name}\n"
        )
        report = json.loads((self.inputs / "verification-android.json").read_text())
        report["assets"][self.candidate_name] = checksum
        (self.inputs / "verification-android.json").write_text(json.dumps(report))
        with self.assertRaisesRegex(RuntimeError, "verified Linux ARM64 Core"):
            self.sign()
        self.runner.assert_not_called()

    def test_unexpected_release_certificate_is_rejected(self):
        self.report_certificate = "c" * 64
        with self.assertRaisesRegex(RuntimeError, "fingerprint mismatch"):
            self.sign()
        self.assertFalse((self.output / f"AgentDock-Workbench-{self.version}-Android.apk").exists())

    def test_missing_passwords_are_rejected(self):
        with patch.dict(os.environ, {}, clear=True):
            with self.assertRaisesRegex(RuntimeError, "passwords are unavailable"):
                self.sign()
        self.runner.assert_not_called()

    def test_base64_keystore_payload_is_strictly_decoded(self):
        payload = b"fixture-pkcs12"
        encoded = base64.b64encode(payload).decode("ascii")
        self.assertEqual(module.decode_keystore(encoded), payload)
        with self.assertRaisesRegex(RuntimeError, "encoding is invalid"):
            module.decode_keystore("not-base64!")
        with self.assertRaisesRegex(RuntimeError, "is unavailable"):
            module.decode_keystore("")

    def test_invalid_committed_identity_is_rejected(self):
        self.config.write_text(json.dumps({
            "schema_version": 1,
            "key_alias": "agentdock-android-release",
            "certificate_sha256": "invalid",
        }))
        with self.assertRaisesRegex(RuntimeError, "Invalid Android release signing identity"):
            self.sign()
        self.runner.assert_not_called()


if __name__ == "__main__":
    unittest.main()
