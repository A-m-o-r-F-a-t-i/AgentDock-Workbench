#!/usr/bin/env bash
set -euo pipefail
export SOURCE_SHA="${SOURCE_SHA:-${GITHUB_SHA:?GITHUB_SHA is required}}"

version_code=$((101080000 + GITHUB_RUN_NUMBER))
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
fi
exit "$gradle_status"
