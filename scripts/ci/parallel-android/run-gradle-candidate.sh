#!/usr/bin/env bash
set -euo pipefail
export SOURCE_SHA="${SOURCE_SHA:-${GITHUB_SHA:?GITHUB_SHA is required}}"

version_code=$((101090000 + GITHUB_RUN_NUMBER))
mkdir -p evidence/build
set +e
gradle -p mobile/android --no-daemon --stacktrace --warning-mode all \
  lintDebug testDebugUnitTest assembleDebug assembleRelease assembleDebugAndroidTest \
  -PagentdockAndroidVersionCode="$version_code" \
  -PagentdockCandidateSha="$SOURCE_SHA" \
  -PagentdockCandidateRunId="$GITHUB_RUN_ID" \
  -PagentdockCandidateRunAttempt="$GITHUB_RUN_ATTEMPT" \
  2>&1 | tee evidence/build/gradle.log
gradle_status=${PIPESTATUS[0]}
set -e

if (( gradle_status != 0 )); then
  python3 - <<'PY'
from pathlib import Path
import os
import re

path = Path("evidence/build/gradle.log")
lines = path.read_text(errors="replace").splitlines()
pattern = re.compile(
    r"(^e: |^error: |\.kt:[0-9]+:[0-9]+|FAILURE:|What went wrong|Execution failed|"
    r"Could not (?:resolve|find|determine)|Unresolved reference|Compilation error|Caused by:)",
    re.IGNORECASE,
)
selected = [line for line in lines if pattern.search(line)]
if not selected:
    selected = lines[-120:]
message = "\n".join(selected[-160:])[-24000:]
escaped = message.replace("%", "%25").replace("\r", "%0D").replace("\n", "%0A")
print(f"::error title=Gradle candidate build failed::{escaped}")
summary = os.environ.get("GITHUB_STEP_SUMMARY")
if summary:
    with open(summary, "a", encoding="utf-8") as output:
        output.write("## Gradle failure summary\n\n```text\n" + message + "\n```\n")
PY
fi

if (( gradle_status == 0 )); then
  python3 - <<'PY'
from pathlib import Path
import json
import os
import xml.etree.ElementTree as ET

root = Path("mobile/android/app/build/test-results/testDebugUnitTest")
files = sorted(root.glob("TEST-*.xml"))
if not files:
    raise SystemExit("JVM test reports are missing")
cases = {}
for path in files:
    for case in ET.parse(path).iter("testcase"):
        key = (case.get("classname", ""), case.get("name", ""))
        if not all(key):
            raise SystemExit(f"invalid JVM testcase identity in {path}")
        state = "failed" if case.find("failure") is not None or case.find("error") is not None else (
            "skipped" if case.find("skipped") is not None else "passed"
        )
        cases[key] = state
passed = sum(value == "passed" for value in cases.values())
valid = len(cases) >= 60 and passed == len(cases)
summary = {
    "schema_version": 1,
    "source_sha": os.environ["SOURCE_SHA"],
    "tests": len(cases),
    "passed": passed,
    "failed": sum(value == "failed" for value in cases.values()),
    "skipped": sum(value == "skipped" for value in cases.values()),
    "gate": "passed" if valid else "failed",
}
Path("evidence/build/unit-test-summary.json").write_text(json.dumps(summary, indent=2) + "\n")
if not valid:
    raise SystemExit(f"JVM evidence gate failed: {summary}")
print(f"JVM evidence gate passed: {len(cases)} tests")
PY

  python3 - <<'PY'
from pathlib import Path
import hashlib
import json
import os
import zipfile

source = Path("mobile/android/termux/agentdock-core-arm64.tgz")
if source.is_symlink() or not source.is_file() or not 0 < source.stat().st_size <= 64 * 1024 * 1024:
    raise SystemExit("verified bundled Core source is missing or invalid")
with source.open("rb") as stream:
    source_digest = hashlib.file_digest(stream, "sha256").hexdigest()
expected_member = "assets/agentdock-core-arm64.tgz"
invalid_members = {
    "assets/agentdock-core-arm64.tar.gz",
    "assets/agentdock-core-arm64.tar",
}
results = []
for variant in ("debug", "release"):
    apk = Path(f"mobile/android/app/build/outputs/apk/{variant}/app-{variant}.apk")
    if apk.is_symlink() or not apk.is_file():
        raise SystemExit(f"Android {variant} APK is missing")
    try:
        with zipfile.ZipFile(apk) as archive:
            candidates = [
                entry for entry in archive.infolist()
                if entry.filename == expected_member or entry.filename in invalid_members
            ]
            if len(candidates) != 1 or candidates[0].filename != expected_member:
                names = [entry.filename for entry in candidates]
                raise SystemExit(f"{apk.name} does not preserve the bundled Core asset name: {names}")
            entry = candidates[0]
            if entry.is_dir() or not 0 < entry.file_size <= 64 * 1024 * 1024:
                raise SystemExit(f"{apk.name} contains an invalid bundled Core asset")
            with archive.open(entry) as stream:
                member_digest = hashlib.file_digest(stream, "sha256").hexdigest()
            if member_digest != source_digest:
                raise SystemExit(f"{apk.name} bundled Core bytes differ from the verified Linux archive")
            if entry.compress_type != zipfile.ZIP_STORED:
                raise SystemExit(f"{apk.name} bundled Core must remain uncompressed inside the APK")
            results.append({
                "variant": variant,
                "apk": str(apk),
                "apk_asset": expected_member,
                "sha256": member_digest,
                "size_bytes": entry.file_size,
                "zip_method": "stored",
            })
    except zipfile.BadZipFile as error:
        raise SystemExit(f"invalid Android APK ZIP: {apk}") from error

report = {
    "schema_version": 1,
    "source_sha": os.environ["SOURCE_SHA"],
    "source_archive": str(source),
    "source_sha256": source_digest,
    "apks": results,
    "gate": "passed",
}
Path("evidence/build/bundled-core-apk.json").write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
print(f"APK bundled Core gate passed: {len(results)} variants")
PY
fi
exit "$gradle_status"
