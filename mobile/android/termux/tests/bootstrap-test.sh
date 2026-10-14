#!/usr/bin/env bash
set -euo pipefail
source_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp="$(mktemp -d)"
trap 'rm -rf -- "$tmp"' EXIT
mkdir -p "$tmp/bin" "$tmp/bundle with spaces"
cp "$source_root/agentdock-workbench-bootstrap.sh" "$tmp/bundle with spaces/bootstrap.sh"
for companion in agentdock-workbench agentdock_workbench.py agentdock_host_executor.py; do
  printf '#!/bin/sh\n# fixture companion\nexit 0\n' >"$tmp/bundle with spaces/$companion"
done
printf 'fixture bundled core\n' >"$tmp/bundle with spaces/agentdock-core-arm64.tar.gz"
cat >"$tmp/bin/dpkg" <<'MOCK'
#!/usr/bin/env bash
set -eu
scope="${BOOTSTRAP_SCOPE:-host}"
printf '%s dpkg %s\n' "$scope" "$*" >>"$BOOTSTRAP_LOG"
if [[ "$*" == '--audit' ]]; then
  [[ "$BOOTSTRAP_CASE" != audit-dirty ]] || printf 'package remains unpacked\n'
  exit 0
fi
[[ "$DEBIAN_FRONTEND" == noninteractive && "$*" == '--force-confold --configure -a' ]] || exit 90
[[ "$BOOTSTRAP_CASE" != config-failure ]] || exit 23
[[ "$BOOTSTRAP_CASE" != guest-failure || "$scope" != guest ]] || exit 24
touch "$CASE_DIR/$scope.configured"
MOCK
cat >"$tmp/bin/apt-get" <<'MOCK'
#!/usr/bin/env bash
set -eu
scope="${BOOTSTRAP_SCOPE:-host}"
printf '%s apt %s\n' "$scope" "$*" >>"$BOOTSTRAP_LOG"
[[ "$DEBIAN_FRONTEND" == noninteractive ]] || exit 91
if [[ " $* " == *' install '* ]]; then
  [[ " $* " == *' -y '* && " $* " == *' Dpkg::Options::=--force-confold '* ]] || exit 92
  [[ -f "$CASE_DIR/$scope.configured" ]] || exit 93
  [[ "$BOOTSTRAP_CASE" != install-failure ]] || exit 25
fi
MOCK
cat >"$tmp/bin/dpkg-query" <<'MOCK'
#!/usr/bin/env bash
set -eu
if [[ "$BOOTSTRAP_CASE" == package-unconfigured ]]; then printf 'install ok unpacked\n'; else printf 'install ok installed\n'; fi
MOCK
cat >"$tmp/bin/proot-distro" <<'MOCK'
#!/usr/bin/env bash
set -eu
printf 'proot %s\n' "$*" >>"$BOOTSTRAP_LOG"
case "$1" in
  list)
    [[ "$BOOTSTRAP_CASE" != list-failure ]] || exit 26
    [[ ! -f "$CASE_DIR/installed" ]] || printf 'debian\n'
    ;;
  install) touch "$CASE_DIR/installed" ;;
  login) BOOTSTRAP_SCOPE=guest bash -c "${!#}" ;;
  *) exit 94 ;;
esac
MOCK
printf '#!/usr/bin/env bash\nexit 0\n' >"$tmp/bin/termux-reload-settings"
printf '#!/usr/bin/env bash\nexit 0\n' >"$tmp/bin/termux-setup-storage"
printf '#!/usr/bin/env bash\nexit 0\n' >"$tmp/bin/openssl"
printf '#!/usr/bin/env bash\nexit 95\n' >"$tmp/bin/pkg"
chmod 700 "$tmp/bin/"*
export PATH="$tmp/bin:$PATH"
for scenario in fresh installed dirty-config config-failure install-failure list-failure guest-failure audit-dirty package-unconfigured storage-missing; do
  export BOOTSTRAP_CASE="$scenario" CASE_DIR="$tmp/$scenario" BOOTSTRAP_SCOPE=host
  export HOME="$CASE_DIR/home" BOOTSTRAP_LOG="$CASE_DIR/commands.log"
  mkdir -p "$HOME/.termux"
  [[ "$scenario" == storage-missing ]] || mkdir -p "$HOME/storage/shared"
  printf 'allow-external-apps=false\n# existing preference\n' >"$HOME/.termux/termux.properties"
  printf 'custom openssl configuration\n' >"$CASE_DIR/openssl.cnf"
  [[ "$scenario" == fresh ]] || touch "$CASE_DIR/installed"
  set +e
  timeout 15s bash "$tmp/bundle with spaces/bootstrap.sh" < /dev/null >"$CASE_DIR/output" 2>&1
  rc=$?
  set -e
  case "$scenario" in
    fresh|installed|dirty-config)
      [[ "$rc" == 0 ]] || { cat "$CASE_DIR/output"; exit 1; }
      [[ -f "$CASE_DIR/host.configured" && -f "$CASE_DIR/guest.configured" ]]
      grep -Fxq 'custom openssl configuration' "$CASE_DIR/openssl.cnf"
      grep -Fxq 'allow-external-apps=true' "$HOME/.termux/termux.properties"
      grep -Fxq '# existing preference' "$HOME/.termux/termux.properties"
      [[ -x "$HOME/.termux/tasker/agentdock-workbench" ]]
      [[ -d "$HOME/storage/shared/Documents/AgentDock/Workspaces" ]]
      [[ -d "$HOME/storage/shared/Documents/AgentDock/Artifacts" ]]
      cmp "$tmp/bundle with spaces/agentdock-core-arm64.tar.gz" "$HOME/.agentdock-workbench/bundle/agentdock-core-arm64.tar.gz"
      [[ "$(stat -c '%a' "$HOME/.agentdock-workbench/bundle/agentdock-core-arm64.tar.gz")" == 600 ]]
      grep -Fq 'Termux bridge, Debian PRoot and the APK-bundled Android Core are ready.' "$CASE_DIR/output"
      if [[ "$scenario" == fresh ]]; then
        [[ -f "$HOME/.agentdock-workbench/managed-distro-debian" ]]
        timeout 15s bash "$tmp/bundle with spaces/bootstrap.sh" < /dev/null >>"$CASE_DIR/output" 2>&1
        [[ "$(grep -c '^proot install debian$' "$BOOTSTRAP_LOG")" == 1 ]]
        [[ "$(grep -c '^allow-external-apps=' "$HOME/.termux/termux.properties")" == 1 ]]
      else
        [[ ! -e "$HOME/.agentdock-workbench/managed-distro-debian" ]]
        if grep -q '^proot install' "$BOOTSTRAP_LOG"; then exit 1; fi
      fi
      ;;
    *)
      [[ "$rc" != 0 && "$rc" != 124 ]] || { cat "$CASE_DIR/output"; exit 1; }
      if grep -Fq 'APK-bundled Android Core are ready.' "$CASE_DIR/output"; then exit 1; fi
      if [[ "$scenario" == config-failure ]]; then if grep -q ' apt ' "$BOOTSTRAP_LOG"; then exit 1; fi; fi
      if [[ "$scenario" == list-failure ]]; then if grep -q '^proot install' "$BOOTSTRAP_LOG"; then exit 1; fi; fi
      ;;
  esac
  printf 'bootstrap fixture passed: %s\n' "$scenario"
done
printf '10 bootstrap scenarios, public workspace setup and repeat-install preservation passed; no real package manager invoked.\n'
