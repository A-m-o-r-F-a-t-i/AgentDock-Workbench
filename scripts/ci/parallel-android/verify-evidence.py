#!/usr/bin/env python3
"""Fail closed on missing/cancelled Android evidence before staging the tested APK."""
from __future__ import annotations
import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil

APIS = {26, 33, 34, 35, 37}
REQUIRED_SHOTS = {name + '.png' for name in (
    'home workspaces conversations tasks activity calls insert approvals permissions skills plugins '
    'connections install projects diagnostics settings tasks-empty tasks-error '
    'home-dark tasks-large-text permissions-landscape projects-expanded settings-expanded-compact').split()}


def read_json(path: Path) -> dict:
    if path.is_symlink() or not path.is_file() or path.stat().st_size > 2 * 1024 * 1024:
        raise RuntimeError('Invalid Android evidence file: ' + path.name)
    value = json.loads(path.read_text(encoding='utf-8'))
    if not isinstance(value, dict):
        raise RuntimeError('Android evidence must be an object')
    return value


def verify(inputs: Path, output: Path, version: str, sha: str, run_id: str, attempt: str) -> dict:
    if not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+', version) or not re.fullmatch(r'[a-f0-9]{40}', sha):
        raise RuntimeError('Invalid Android source identity')
    def identity(value: dict) -> None:
        if (value.get('source_sha'), str(value.get('run_id')), str(value.get('run_attempt'))) != (sha, run_id, attempt):
            raise RuntimeError('Android evidence source/run mismatch')
    results = {}
    for path in inputs.rglob('result.json'):
        value = read_json(path)
        identity(value)
        api = value.get('api_level')
        if type(api) is not int or api not in APIS or api in results:
            raise RuntimeError('Duplicate or unexpected Android API evidence')
        tests, passed = value.get('tests'), value.get('passed')
        if (type(tests) is not int or tests < 13 or passed != tests or
                value.get('actual_api_level') != str(api) or value.get('evidence_gate') != 'passed' or
                value.get('connected_test_exit_code') != 0 or value.get('attempt_exit_codes') != [0] or
                value.get('missing_screenshots') != [] or value.get('invalid_images') != [] or value.get('report_errors') != []):
            raise RuntimeError(f'Android API {api} has failed or incomplete evidence')
        names = value.get('screenshots', [])
        if len(names) != len(set(names)) or not REQUIRED_SHOTS.issubset(names):
            raise RuntimeError(f'Android API {api} screenshot inventory is incomplete')
        for name in REQUIRED_SHOTS:
            shots = list((path.parent / 'screenshots').rglob(name))
            if len(shots) != 1 or shots[0].is_symlink():
                raise RuntimeError(f'Android API {api} screenshot file is missing: {name}')
            with shots[0].open('rb') as stream:
                header = stream.read(24)
            if (len(header) != 24 or header[:8] != b'\x89PNG\r\n\x1a\n' or header[12:16] != b'IHDR' or
                    int.from_bytes(header[16:20], 'big') == 0 or int.from_bytes(header[20:24], 'big') == 0):
                raise RuntimeError(f'Android API {api} screenshot is invalid: {name}')
        results[api] = {'api': api, 'tests': tests, 'passed': passed, 'screenshots': len(names), 'attempt_exit_codes': [0]}
    if set(results) != APIS:
        raise RuntimeError('Missing Android API evidence: ' + str(sorted(APIS - set(results))))
    candidates = list(inputs.rglob('candidate.json'))
    if len(candidates) != 1:
        raise RuntimeError('Expected exactly one Android APK candidate')
    candidate = read_json(candidates[0])
    identity(candidate)
    if (candidate.get('product_version') != version or candidate.get('product') != 'AgentDock Workbench' or
            candidate.get('signing') != 'Android debug/test key only'):
        raise RuntimeError('Android candidate version/product/signing mismatch')
    original_name = f'AgentDock-Workbench-{version}-Android-release-shaped-{sha[:12]}-test-signed.apk'
    if original_name not in candidate.get('apks', []):
        raise RuntimeError('Release-shaped Android APK is not declared')
    source = candidates[0].parent / 'apk' / original_name
    if not source.is_file() or source.is_symlink() or not 0 < source.stat().st_size <= 256 * 1024 * 1024:
        raise RuntimeError('Invalid Android APK input')
    with source.open('rb') as stream:
        checksum = hashlib.file_digest(stream, 'sha256').hexdigest()
    sums = (source.parent / 'SHA256SUMS').read_text().splitlines()
    if sums.count(f'{checksum}  {original_name}') != 1:
        raise RuntimeError('Android APK checksum mismatch')
    output.mkdir(parents=True, exist_ok=True)
    name = f'AgentDock-Workbench-{version}-Android-test-signed.apk'
    shutil.copy2(source, output / name)
    (output / (name + '.sha256')).write_text(f'{checksum}  {name}\n')
    report = {'schema_version': 1, 'product_name': 'AgentDock Workbench', 'version': version, 'commit': sha,
              'platform': 'android/arm64', 'run_id': run_id, 'run_attempt': attempt,
              'signing': 'test-signed Android debug key', 'apk_validation': 'passed',
              'emulator_validation': 'passed', 'api_levels': sorted(APIS),
              'emulators': [results[api] for api in sorted(APIS)],
              'physical_arm64_termux': 'not_run', 'assets': {name: checksum}}
    (output / 'verification-android.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    return report


def main() -> None:
    parser = argparse.ArgumentParser()
    for name in ('input', 'output', 'version', 'sha', 'run-id', 'attempt'):
        parser.add_argument('--' + name, required=True)
    args = parser.parse_args()
    result = verify(Path(args.input), Path(args.output), args.version, args.sha, args.run_id, args.attempt)
    print(json.dumps(result, ensure_ascii=False))

if __name__ == '__main__':
    main()
