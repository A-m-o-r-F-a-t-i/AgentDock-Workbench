#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
bridge="$root/mobile/android/termux/agentdock-workbench"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
export AGENTDOCK_WORKBENCH_TEST_MODE=1
export AGENTDOCK_WORKBENCH_TEST_HOME="$tmp/home"
mkdir -p "$AGENTDOCK_WORKBENCH_TEST_HOME/storage/shared"

python3 - "$root/mobile/android/termux/agentdock_workbench.py" "$tmp" <<'PYHTTP'
import http.server
import importlib.util
from pathlib import Path
import sys
import threading
import urllib.error

module_path, root = Path(sys.argv[1]), Path(sys.argv[2])
spec = importlib.util.spec_from_file_location('termux_bridge_http', module_path)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
requests = []

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        requests.append((self.path, self.headers.get('Authorization')))
        if self.path == '/redirect':
            self.send_response(302)
            self.send_header('Location', '/status')
            self.end_headers()
            return
        body = b'{"ok":true}'
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, _format, *_args):
        pass

node = root / 'http-node'
node.mkdir()
(node / 'auth-token').write_text('1' * 64)
server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
thread = threading.Thread(target=server.serve_forever, daemon=True)
thread.start()
try:
    backend = module.NodeBackend(node, dict(module.DEFAULT_CONFIG, port=server.server_port))
    assert backend.request('/status') == {'ok': True}
    assert requests[-1] == ('/status', 'Bearer ' + '1' * 64)
    try:
        backend.request('/redirect')
    except urllib.error.HTTPError as error:
        assert error.code == 302
    else:
        raise AssertionError('redirect was followed')
    assert [path for path, _ in requests] == ['/status', '/redirect']
finally:
    server.shutdown()
    server.server_close()
    thread.join(timeout=2)
print('Termux loopback HTTP client and redirect rejection passed')
PYHTTP

request() {
  local op="$1" oid="$2" rid="$3" nonce="$4" extra="${5-}"
  [[ -n "$extra" ]] || extra='{}'
  jq -cn --arg op "$op" --arg oid "$oid" --arg rid "$rid" --arg nonce "$nonce" --argjson extra "$extra" \
    '$extra + {schema_version:1,operation:$op,operation_id:$oid,request_id:$rid,nonce:$nonce}' |
    sh "$bridge" "$op" "$oid" "$rid" "$nonce" -
}

nonce=abcdefghijklmnopqrstuvwxyzABCDE123456
out="$(request probe op_probe req_probe "$nonce")"
jq -e '.operation=="probe" and .operation_id=="op_probe" and .request_id=="req_probe" and (.status|type=="string") and (has("credential")|not)' <<<"$out" >/dev/null
[ ! -e "$AGENTDOCK_WORKBENCH_TEST_HOME/.agentdock-workbench" ]
request status op_status req_status "$nonce" >/dev/null
[ ! -e "$AGENTDOCK_WORKBENCH_TEST_HOME/.agentdock-workbench" ]

out="$(request bootstrap op_boot req_boot "$nonce")"
jq -e '.status=="pending_manifest" or .status=="requires_user_action" or .status=="ok"' <<<"$out" >/dev/null

out="$(request install op_install req_install "$nonce" '{"apk_version":"1.1.9","start_after_install":false}')"
jq -e '.status=="requires_user_action" and .data.failure_code=="bundled_core_missing"' <<<"$out" >/dev/null
[ ! -e "$AGENTDOCK_WORKBENCH_TEST_HOME/.agentdock-workbench/node/current" ]
request cancel_operation op_cancel req_cancel "$nonce" '{"target_operation_id":"op_install","confirm_cancel":true}' >/dev/null

set +e
bad="$(printf '{}' | sh "$bridge" start 'bad/id' req_bad "$nonce" - 2>/dev/null)"
rc=$?
set -e
[ "$rc" -ne 0 ]
jq -e '.status=="invalid_request"' <<<"$bad" >/dev/null

printf '%70000s' x >"$tmp/oversize"
set +e
oversize="$(sh "$bridge" status op_large req_large "$nonce" - <"$tmp/oversize" 2>/dev/null)"
rc=$?
set -e
[ "$rc" -ne 0 ]
jq -e '.status=="invalid_request"' <<<"$oversize" >/dev/null

# Unknown process ownership must never signal the current CI shell or a PID group.
node="$AGENTDOCK_WORKBENCH_TEST_HOME/.agentdock-workbench/node"
printf '%s\n' "$$" >"$node/core.pid"
set +e
out="$(request stop op_stop req_stop "$nonce")"
rc=$?
set -e
[ "$rc" -ne 0 ]
jq -e '.status=="requires_user_action"' <<<"$out" >/dev/null
kill -0 "$$"
[ "$(cat "$node/desired-state")" = stopped ]
[ "$(cat "$node/core.pid")" = "$$" ]
# The supervised child must not inherit the bridge's mutation lock descriptor.
rm -f "$node/core.pid" "$node/core.identity"
mkdir -p "$tmp/mock-bin" "$node/versions/fixture/bin"
printf '#!/bin/sh\nexit 1\n' >"$node/versions/fixture/bin/agentdock"
chmod 700 "$node/versions/fixture/bin/agentdock"
ln -s versions/fixture "$node/current"
printf '%064d\n' 0 >"$node/auth-token"
cat >"$tmp/mock-bin/proot-distro" <<'MOCK_PROOT'
#!/bin/sh
if [ -e "/proc/$$/fd/9" ]; then
  printf inherited >"$AGENTDOCK_WORKBENCH_TEST_HOME/child-lock"
else
  printf closed >"$AGENTDOCK_WORKBENCH_TEST_HOME/child-lock"
fi
exit 1
MOCK_PROOT
chmod 700 "$tmp/mock-bin/proot-distro"
# Non-hex identity material must be rejected before any process is spawned.
printf '%064d\n' 0 | tr '0' 'G' >"$node/auth-token"
set +e
out="$(PATH="$tmp/mock-bin:$PATH" request start op_invalid_token req_invalid_token "$nonce")"
rc=$?
set -e
[ "$rc" -ne 0 ]
[ ! -e "$AGENTDOCK_WORKBENCH_TEST_HOME/child-lock" ]
printf '%064d\n' 0 >"$node/auth-token"
set +e
out="$(PATH="$tmp/mock-bin:$PATH" request start op_lock req_lock "$nonce")"
rc=$?
set -e
[ "$rc" -ne 0 ]
[ "$(cat "$AGENTDOCK_WORKBENCH_TEST_HOME/child-lock")" = closed ]

# Exact code used by the bridge extractor is tested against the same bundled-archive path used in production.
python3 - "$bridge" "$tmp" <<'PYTEST'
import io, os, pathlib, subprocess, sys, tarfile
bridge, root = pathlib.Path(sys.argv[1]), pathlib.Path(sys.argv[2])
text = bridge.read_text()
module = bridge.with_name('agentdock_workbench.py')
code = "import importlib.util,sys; from pathlib import Path; spec=importlib.util.spec_from_file_location('bridge',sys.argv[1]); m=importlib.util.module_from_spec(spec); spec.loader.exec_module(m); m.safe_extract(Path(sys.argv[2]),Path(sys.argv[3]))"
cases = [
    ("good", [("bin/agentdock", tarfile.REGTYPE, b"fixture")], True),
    ("traversal", [("../escape", tarfile.REGTYPE, b"fixture")], False),
    ("absolute", [("/tmp/escape", tarfile.REGTYPE, b"fixture")], False),
    ("symlink", [("bin/link", tarfile.SYMTYPE, b"")], False),
    ("hardlink", [("bin/link", tarfile.LNKTYPE, b"")], False),
    ("duplicate", [("bin/app", tarfile.REGTYPE, b"a"), ("bin/app", tarfile.REGTYPE, b"b")], False),
    ("conflict", [("bin", tarfile.REGTYPE, b"a"), ("bin/app", tarfile.REGTYPE, b"b")], False),
    ("backslash", [("..\\escape", tarfile.REGTYPE, b"a")], False),
    ("reserved", [(".workbench-release.json", tarfile.REGTYPE, b"{}")], False),
]
for name, entries, success in cases:
    archive = root / (name + ".tar.gz"); stage = root / (name + "-stage"); stage.mkdir()
    with tarfile.open(archive, "w:gz") as output:
        for filename, kind, payload in entries:
            member = tarfile.TarInfo(filename); member.type = kind; member.mode = 0o7777
            member.size = len(payload) if kind == tarfile.REGTYPE else 0
            if kind in (tarfile.SYMTYPE, tarfile.LNKTYPE): member.linkname = "/tmp/escape"
            output.addfile(member, io.BytesIO(payload) if member.isfile() else None)
    result = subprocess.run([sys.executable, "-c", code, str(module), str(archive), str(stage)], capture_output=True, text=True)
    assert (result.returncode == 0) == success, (name, result.stderr)
    if success:
        assert (stage / "bin/agentdock").read_bytes() == b"fixture"
        assert (stage / "bin/agentdock").stat().st_mode & 0o7777 == 0o755
    else:
        assert not any(stage.iterdir()), (name, "partial extraction before validation")
print("9 archive cases passed")
PYTEST

bash "$root/mobile/android/termux/tests/bootstrap-test.sh"
python3 "$root/scripts/ci/parallel-android/test_deployment.py"
echo 'termux bridge contract and deployment tests passed'
