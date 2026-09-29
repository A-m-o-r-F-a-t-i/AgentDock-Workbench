#!/usr/bin/env python3
"""Isolated Android publication-gate regressions; no emulator or network required."""
import importlib.util
import json
from pathlib import Path
import shutil
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('android_evidence', Path(__file__).with_name('verify-evidence.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

class EvidenceGate(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.source = self.root / 'inputs'
        self.source.mkdir()
        self.version, self.sha = '1.1.8', 'a' * 40
        self.base = {'source_sha': self.sha, 'run_id': '123', 'run_attempt': '1'}
        for api in module.APIS:
            path = self.source / str(api)
            (path / 'screenshots').mkdir(parents=True)
            value = dict(self.base, api_level=api, actual_api_level=str(api), tests=13, passed=13,
                         evidence_gate='passed', connected_test_exit_code=0, attempt_exit_codes=[0],
                         missing_screenshots=[], invalid_images=[], report_errors=[], screenshots=sorted(module.REQUIRED_SHOTS))
            (path / 'result.json').write_text(json.dumps(value))
            header = b'\x89PNG\r\n\x1a\n' + (13).to_bytes(4, 'big') + b'IHDR' + (100).to_bytes(4, 'big') * 2
            for name in module.REQUIRED_SHOTS:
                (path / 'screenshots' / name).write_bytes(header)
        candidate = self.source / 'candidate'
        (candidate / 'apk').mkdir(parents=True)
        self.apk = candidate / 'apk' / f'AgentDock-Workbench-{self.version}-Android-release-shaped-{self.sha[:12]}-test-signed.apk'
        self.apk.write_bytes(b'fixture-apk')
        checksum = module.hashlib.sha256(self.apk.read_bytes()).hexdigest()
        (candidate / 'apk' / 'SHA256SUMS').write_text(f'{checksum}  {self.apk.name}\n')
        (candidate / 'candidate.json').write_text(json.dumps(dict(self.base, product='AgentDock Workbench',
            product_version=self.version, signing='Android debug/test key only', apks=[self.apk.name])))
    def verify(self):
        return module.verify(self.source, self.root / 'output', self.version, self.sha, '123', '1')
    def mutate(self, **fields):
        path = self.source / '26' / 'result.json'
        value = json.loads(path.read_text())
        value.update(fields)
        path.write_text(json.dumps(value))
    def test_complete(self):
        report = self.verify()
        self.assertEqual(report['api_levels'], [26, 33, 34, 35, 37])
        self.assertEqual(report['physical_arm64_termux'], 'not_run')
        self.assertTrue((self.root / 'output' / 'AgentDock-Workbench-1.1.8-Android-test-signed.apk').is_file())
    def test_missing_api_cannot_pass(self):
        shutil.rmtree(self.source / '26')
        with self.assertRaisesRegex(RuntimeError, 'Missing Android API'): self.verify()
    def test_cancelled_or_failed_cannot_pass(self):
        for code in (1, 124, 137):
            with self.subTest(code=code):
                self.mutate(connected_test_exit_code=code)
                with self.assertRaises(RuntimeError): self.verify()
    def test_retried_assertions_cannot_pass(self):
        self.mutate(attempt_exit_codes=[1, 0])
        with self.assertRaises(RuntimeError): self.verify()
    def test_incomplete_tests_cannot_pass(self):
        self.mutate(passed=12)
        with self.assertRaises(RuntimeError): self.verify()
    def test_mixed_source_cannot_pass(self):
        self.mutate(source_sha='b' * 40)
        with self.assertRaisesRegex(RuntimeError, 'source/run mismatch'): self.verify()
    def test_missing_png_cannot_pass(self):
        (self.source / '26' / 'screenshots' / 'home.png').unlink()
        with self.assertRaisesRegex(RuntimeError, 'screenshot file is missing'): self.verify()
    def test_corrupt_png_cannot_pass(self):
        (self.source / '26' / 'screenshots' / 'home.png').write_text('invalid')
        with self.assertRaisesRegex(RuntimeError, 'screenshot is invalid'): self.verify()
    def test_changed_apk_cannot_pass(self):
        self.apk.write_bytes(b'changed')
        with self.assertRaisesRegex(RuntimeError, 'checksum mismatch'): self.verify()
    def test_duplicate_api_cannot_pass(self):
        shutil.copytree(self.source / '26', self.source / 'duplicate')
        with self.assertRaisesRegex(RuntimeError, 'Duplicate'): self.verify()

if __name__ == '__main__':
    unittest.main()
