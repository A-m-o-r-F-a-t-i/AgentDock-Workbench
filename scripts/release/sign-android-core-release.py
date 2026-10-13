#!/usr/bin/env python3
"""Bind the verified Linux ARM64 runtime to the Android deployment manifest."""
from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[2]
PRODUCT = "AgentDock Workbench"
REPOSITORY = "A-m-o-r-F-a-t-i/AgentDock-Workbench"
CONFIG = Path("packaging/android/core-release-signing.json")
MAX_ARCHIVE_BYTES = 256 * 1024 * 1024
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


def openssl_binary() -> str:
    configured = os.environ.get("OPENSSL_BIN", "")
    if configured:
        path = Path(configured)
        if path.is_file():
            return str(path)
        raise RuntimeError("Configured OpenSSL binary does not exist")
    resolved = shutil.which("openssl")
    if resolved:
        return resolved
    raise RuntimeError("OpenSSL is unavailable")


def run_openssl(*arguments: str, binary: bool = False) -> bytes | str:
    result = subprocess.run(
        [openssl_binary(), *arguments],
        check=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        timeout=120,
    )
    if binary:
        return result.stdout
    return result.stdout.decode("utf-8", errors="replace")


def release_identity(config_path: Path) -> tuple[Path, str]:
    config = read_json(config_path)
    public_value = config.get("public_key")
    fingerprint = str(config.get("public_key_sha256", "")).lower()
    if (
        config.get("schema_version") != 1
        or config.get("algorithm") != "Ed25519"
        or not isinstance(public_value, str)
        or not public_value
        or not SHA256_PATTERN.fullmatch(fingerprint)
    ):
        raise RuntimeError("Invalid Android Core release signing identity")
    public_key = Path(public_value)
    if not public_key.is_absolute():
        public_key = ROOT / public_key
    public_key = public_key.resolve()
    if public_key.is_symlink() or not public_key.is_file() or not 0 < public_key.stat().st_size <= 16 * 1024:
        raise RuntimeError("Android Core release public key is unavailable")
    actual = hashlib.sha256(
        run_openssl("pkey", "-pubin", "-in", str(public_key), "-outform", "DER", binary=True)
    ).hexdigest()
    if actual != fingerprint:
        raise RuntimeError("Android Core release public key fingerprint mismatch")
    return public_key, fingerprint


def decode_private_key(encoded: str) -> bytes:
    if not encoded:
        raise RuntimeError("Android Core release private key is unavailable")
    try:
        payload = base64.b64decode(encoded, validate=True)
    except ValueError as error:
        raise RuntimeError("Android Core release private key encoding is invalid") from error
    if not 0 < len(payload) <= 64 * 1024:
        raise RuntimeError("Android Core release private key size is invalid")
    return payload


def inspect_archive(path: Path) -> None:
    if path.is_symlink() or not path.is_file() or not 0 < path.stat().st_size <= MAX_ARCHIVE_BYTES:
        raise RuntimeError("Invalid Linux ARM64 source archive")
    binary = None
    total_members = 0
    with tarfile.open(path, "r:gz") as archive:
        for member in archive.getmembers():
            total_members += 1
            if total_members > 100_000:
                raise RuntimeError("Linux ARM64 source archive contains too many entries")
            name = PurePosixPath(member.name)
            if name.is_absolute() or ".." in name.parts or not name.parts:
                raise RuntimeError("Linux ARM64 source archive contains an unsafe path")
            if member.issym() or member.islnk() or member.isdev() or member.isfifo():
                raise RuntimeError("Linux ARM64 source archive contains an unsupported entry")
            if name.as_posix() == "bin/agentdock":
                if not member.isfile() or member.size <= 0 or member.mode & 0o111 == 0:
                    raise RuntimeError("Linux ARM64 source archive has an invalid Core binary")
                binary = member
    if binary is None:
        raise RuntimeError("Linux ARM64 source archive is missing bin/agentdock")


def create_release(
    inputs: Path,
    output: Path,
    version: str,
    commit: str,
    private_key: Path,
    config_path: Path,
) -> dict:
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", version) or not re.fullmatch(r"[a-f0-9]{40}", commit):
        raise RuntimeError("Invalid Android Core release identity")
    if private_key.is_symlink() or not private_key.is_file() or not 0 < private_key.stat().st_size <= 64 * 1024:
        raise RuntimeError("Invalid Android Core release private key")

    public_key, public_fingerprint = release_identity(config_path)
    derived_public = run_openssl("pkey", "-in", str(private_key), "-pubout", "-outform", "DER", binary=True)
    if hashlib.sha256(derived_public).hexdigest() != public_fingerprint:
        raise RuntimeError("Android Core release private key does not match the committed public key")

    source_name = "agentdock_linux_arm64.tar.gz"
    source = locate_unique(inputs, source_name)
    source_checksum = digest(source)
    report = read_json(locate_unique(inputs, "verification-linux-arm64.json"))
    assets = report.get("assets")
    if (
        report.get("product_name") != PRODUCT
        or report.get("version") != version
        or report.get("commit") != commit
        or report.get("platform") != "linux/arm64"
        or report.get("native_execution") != "passed"
        or not isinstance(assets, dict)
        or assets.get(source_name) != source_checksum
    ):
        raise RuntimeError("Linux ARM64 verification report does not bind the source archive")
    sidecar = locate_unique(inputs, source_name + ".sha256").read_text(encoding="utf-8-sig").strip()
    if sidecar != f"{source_checksum}  {source_name}":
        raise RuntimeError("Linux ARM64 source checksum sidecar is invalid")
    inspect_archive(source)

    output.mkdir(parents=True, exist_ok=True)
    stem = f"AgentDock-Workbench-{version}-Android-Core-arm64"
    archive_name = stem + ".tar.gz"
    manifest_name = stem + ".manifest.json"
    signature_name = stem + ".manifest.sig"
    archive = output / archive_name
    manifest_path = output / manifest_name
    signature = output / signature_name
    for path in (archive, manifest_path, signature):
        if path.is_symlink():
            raise RuntimeError("Android Core release output must not be a symbolic link")
    shutil.copyfile(source, archive)
    if digest(archive) != source_checksum:
        raise RuntimeError("Android Core release archive copy changed the verified bytes")

    manifest = {
        "schema_version": 1,
        "product_name": PRODUCT,
        "platform": "linux",
        "arch": "arm64",
        "version": version,
        "commit": commit,
        "sha256": source_checksum,
        "asset_url": f"https://github.com/{REPOSITORY}/releases/download/v{version}/{archive_name}",
        "data_schema": 1,
    }
    manifest_path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    run_openssl(
        "pkeyutl", "-sign", "-inkey", str(private_key), "-rawin",
        "-in", str(manifest_path), "-out", str(signature),
    )
    if signature.stat().st_size != 64:
        raise RuntimeError("Android Core release signature size is invalid")
    run_openssl(
        "pkeyutl", "-verify", "-pubin", "-inkey", str(public_key), "-rawin",
        "-in", str(manifest_path), "-sigfile", str(signature),
    )

    output_assets = {path.name: digest(path) for path in (archive, manifest_path, signature)}
    for name, checksum in output_assets.items():
        (output / (name + ".sha256")).write_text(f"{checksum}  {name}\n", encoding="utf-8")
    release_report = {
        "schema_version": 1,
        "product_name": PRODUCT,
        "version": version,
        "commit": commit,
        "platform": "android-core/arm64",
        "signing": "stable Android Core Ed25519 key",
        "public_key_sha256": public_fingerprint,
        "source_archive": {"name": source_name, "sha256": source_checksum},
        "manifest": {
            "name": manifest_name,
            "signature": signature_name,
            "asset": archive_name,
            "asset_url": manifest["asset_url"],
        },
        "release_signing_validation": "passed",
        "assets": output_assets,
    }
    (output / "verification-android-core-release.json").write_text(
        json.dumps(release_report, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )
    return release_report


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--private-key", type=Path)
    parser.add_argument("--config", type=Path, default=ROOT / CONFIG)
    args = parser.parse_args()
    if args.private_key is not None:
        report = create_release(
            args.input.resolve(), args.output.resolve(), args.version, args.commit,
            args.private_key.resolve(), args.config.resolve(),
        )
    else:
        payload = decode_private_key(os.environ.get("ANDROID_CORE_RELEASE_PRIVATE_KEY_B64", ""))
        with tempfile.TemporaryDirectory(prefix="agentdock-android-core-key-") as temporary:
            private_key = Path(temporary) / "release-ed25519-private.pem"
            private_key.write_bytes(payload)
            private_key.chmod(0o600)
            report = create_release(
                args.input.resolve(), args.output.resolve(), args.version, args.commit,
                private_key, args.config.resolve(),
            )
    print(json.dumps(report, ensure_ascii=False))


if __name__ == "__main__":
    main()
