#!/usr/bin/env python3
"""Create the public Android installer from one verified test-signed candidate."""
from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]
MAX_JSON_BYTES = 2 * 1024 * 1024
SHA256_PATTERN = re.compile(r"^[0-9a-f]{64}$")


def digest(path: Path) -> str:
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def read_json(path: Path) -> dict:
    if path.is_symlink() or not path.is_file() or path.stat().st_size > MAX_JSON_BYTES:
        raise RuntimeError(f"Invalid JSON input: {path.name}")
    value = json.loads(path.read_text(encoding="utf-8-sig"))
    if not isinstance(value, dict):
        raise RuntimeError(f"JSON input must be an object: {path.name}")
    return value


def locate_unique(directory: Path, name: str) -> Path:
    matches = sorted(path for path in directory.rglob(name) if path.is_file() and not path.is_symlink())
    if len(matches) != 1:
        raise RuntimeError(f"Expected exactly one {name}, found {len(matches)}")
    return matches[0]


def decode_keystore(encoded: str) -> bytes:
    if not encoded:
        raise RuntimeError("Android release keystore is unavailable")
    try:
        payload = base64.b64decode(encoded, validate=True)
    except ValueError as error:
        raise RuntimeError("Android release keystore encoding is invalid") from error
    if not 0 < len(payload) <= 16 * 1024 * 1024:
        raise RuntimeError("Android release keystore size is invalid")
    return payload


def release_identity(config_path: Path) -> tuple[str, str]:
    config = read_json(config_path)
    alias = config.get("key_alias")
    certificate = str(config.get("certificate_sha256", "")).lower()
    if config.get("schema_version") != 1 or not isinstance(alias, str) or not alias or not SHA256_PATTERN.fullmatch(certificate):
        raise RuntimeError("Invalid Android release signing identity")
    return alias, certificate


def locate_tool(name: str) -> Path:
    suffixes = [".bat", ".exe", ""] if os.name == "nt" else [""]
    roots = [value for key in ("ANDROID_HOME", "ANDROID_SDK_ROOT") if (value := os.environ.get(key))]
    candidates: list[Path] = []
    for value in roots:
        build_tools = Path(value) / "build-tools"
        if not build_tools.is_dir():
            continue
        for version in sorted(build_tools.iterdir(), key=lambda path: path.name, reverse=True):
            if not version.is_dir():
                continue
            for suffix in suffixes:
                candidate = version / f"{name}{suffix}"
                if candidate.is_file():
                    candidates.append(candidate)
    if candidates:
        return candidates[0]
    resolved = shutil.which(name)
    if resolved:
        return Path(resolved)
    raise RuntimeError(f"Android build tool is unavailable: {name}")


def run_tool(tool: Path, *arguments: str) -> subprocess.CompletedProcess[str]:
    command = [str(tool), *arguments]
    if os.name == "nt" and tool.suffix.lower() in {".bat", ".cmd"}:
        command = ["cmd.exe", "/d", "/c", str(tool), *arguments]
    return subprocess.run(
        command,
        check=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=180,
    )


def verified_certificate_digest(output: str) -> str:
    """Return the single signer certificate from supported apksigner formats."""
    signer_counts = re.findall(r"(?im)^Number of signers:\s*(\d+)\s*$", output)
    if signer_counts and (len(signer_counts) != 1 or signer_counts[0] != "1"):
        raise RuntimeError("Android release APK must have exactly one signer")
    certificates = {
        value.lower()
        for value in re.findall(
            r"(?im)^(?:Signer #\d+|V\d+(?:\.\d+)? Signer):?\s+certificate SHA-256 digest:\s*([0-9a-f]{64})\s*$",
            output,
        )
    }
    numbered_signers = set(re.findall(r"(?im)^Signer #(\d+)\b", output))
    if len(numbered_signers) > 1 or len(certificates) != 1:
        raise RuntimeError("Android release APK must have exactly one signer")
    return next(iter(certificates))


def sign_candidate(
    inputs: Path,
    output: Path,
    version: str,
    commit: str,
    keystore: Path,
    config_path: Path,
) -> dict:
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", version) or not re.fullmatch(r"[a-f0-9]{40}", commit):
        raise RuntimeError("Invalid Android release identity")
    if keystore.is_symlink() or not keystore.is_file() or not 0 < keystore.stat().st_size <= 16 * 1024 * 1024:
        raise RuntimeError("Invalid Android release keystore")
    if not os.environ.get("ANDROID_RELEASE_KEYSTORE_PASSWORD") or not os.environ.get("ANDROID_RELEASE_KEY_PASSWORD"):
        raise RuntimeError("Android release signing passwords are unavailable")

    key_alias, expected_certificate = release_identity(config_path)
    candidate_report = read_json(locate_unique(inputs, "verification-android.json"))
    if (
        candidate_report.get("version") != version
        or candidate_report.get("commit") != commit
        or candidate_report.get("product_name") != "AgentDock Workbench"
        or candidate_report.get("platform") != "android/arm64"
        or candidate_report.get("signing") != "test-signed Android debug key"
        or candidate_report.get("apk_validation") != "passed"
        or candidate_report.get("emulator_validation") != "passed"
    ):
        raise RuntimeError("Android candidate report is not publishable")

    candidate_name = f"AgentDock-Workbench-{version}-Android-test-signed.apk"
    assets = candidate_report.get("assets")
    if not isinstance(assets, dict) or set(assets) != {candidate_name}:
        raise RuntimeError("Android candidate report has an unexpected asset set")
    candidate_checksum = str(assets[candidate_name]).lower()
    if not SHA256_PATTERN.fullmatch(candidate_checksum):
        raise RuntimeError("Android candidate checksum is invalid")
    candidate = locate_unique(inputs, candidate_name)
    if digest(candidate) != candidate_checksum:
        raise RuntimeError("Android candidate bytes differ from verified evidence")
    sidecar = locate_unique(inputs, candidate_name + ".sha256").read_text(encoding="utf-8-sig").strip()
    if sidecar != f"{candidate_checksum}  {candidate_name}":
        raise RuntimeError("Android candidate checksum sidecar is invalid")

    output.mkdir(parents=True, exist_ok=True)
    public_name = f"AgentDock-Workbench-{version}-Android.apk"
    public_path = output / public_name
    if public_path.is_symlink():
        raise RuntimeError("Android public output must not be a symbolic link")
    zipalign = locate_tool("zipalign")
    apksigner = locate_tool("apksigner")
    with tempfile.TemporaryDirectory(prefix="agentdock-android-sign-", dir=output) as temporary:
        temporary_root = Path(temporary)
        aligned = temporary_root / "aligned.apk"
        signed = temporary_root / "signed.apk"
        run_tool(zipalign, "-f", "4", str(candidate), str(aligned))
        run_tool(
            apksigner,
            "sign",
            "--ks",
            str(keystore),
            "--ks-key-alias",
            key_alias,
            "--ks-pass",
            "env:ANDROID_RELEASE_KEYSTORE_PASSWORD",
            "--key-pass",
            "env:ANDROID_RELEASE_KEY_PASSWORD",
            "--out",
            str(signed),
            str(aligned),
        )
        verification = run_tool(apksigner, "verify", "--verbose", "--print-certs", str(signed)).stdout
        actual_certificate = verified_certificate_digest(verification)
        if actual_certificate != expected_certificate:
            raise RuntimeError("Android release certificate fingerprint mismatch")
        os.replace(signed, public_path)

    checksum = digest(public_path)
    (output / (public_name + ".sha256")).write_text(f"{checksum}  {public_name}\n", encoding="utf-8")
    report = {
        "schema_version": 1,
        "product_name": "AgentDock Workbench",
        "version": version,
        "commit": commit,
        "platform": "android/arm64",
        "signing": "stable Android release key",
        "key_alias": key_alias,
        "certificate_sha256": actual_certificate,
        "source_apk": {"name": candidate_name, "sha256": candidate_checksum},
        "source_run_id": str(candidate_report.get("run_id", "")),
        "source_run_attempt": str(candidate_report.get("run_attempt", "")),
        "release_signing_validation": "passed",
        "assets": {public_name: checksum},
    }
    (output / "verification-android-release.json").write_text(
        json.dumps(report, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )
    return report


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--keystore", type=Path)
    parser.add_argument("--config", type=Path, default=ROOT / "packaging/android/release-signing.json")
    args = parser.parse_args()
    config_path = args.config.resolve()
    if args.keystore is not None:
        report = sign_candidate(
            args.input.resolve(), args.output.resolve(), args.version, args.commit,
            args.keystore.resolve(), config_path,
        )
    else:
        payload = decode_keystore(os.environ.get("ANDROID_RELEASE_KEYSTORE_B64", ""))
        with tempfile.TemporaryDirectory(prefix="agentdock-android-keystore-") as temporary:
            keystore = Path(temporary) / "release.p12"
            keystore.write_bytes(payload)
            keystore.chmod(0o600)
            report = sign_candidate(
                args.input.resolve(), args.output.resolve(), args.version, args.commit,
                keystore, config_path,
            )
    print(json.dumps(report, ensure_ascii=False))


if __name__ == "__main__":
    main()
