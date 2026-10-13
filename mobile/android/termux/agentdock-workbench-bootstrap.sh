#!/data/data/com.termux/files/usr/bin/sh
set -eu
umask 077

# Run manually inside the official external Termux app after exporting the complete
# bridge bundle from AgentDock Workbench. It never installs a Core release by itself.
SELF_DIR="$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)"
BRIDGE="$SELF_DIR/agentdock-workbench"
MODULE="$SELF_DIR/agentdock_workbench.py"
HOST_EXECUTOR="$SELF_DIR/agentdock_host_executor.py"
PUBLISHER_KEY="$SELF_DIR/release-ed25519-public.pem"
[ -f "$HOST_EXECUTOR" ] || { printf 'Missing host executor module\n' >&2; exit 1; }
[ -f "$MODULE" ] || { printf 'Missing companion module\n' >&2; exit 1; }
[ -f "$BRIDGE" ] || { printf 'Missing companion file: %s\n' "$BRIDGE" >&2; exit 1; }
[ -f "$PUBLISHER_KEY" ] || { printf 'Missing Android Core release public key\n' >&2; exit 1; }

# -y alone does not answer dpkg conffile prompts. Keep the user's existing
# configuration and finish interrupted package configuration before installing.
export DEBIAN_FRONTEND=noninteractive
timeout --signal=TERM --kill-after=15s 180s dpkg --force-confold --configure -a
timeout --signal=TERM --kill-after=15s 900s apt-get update
timeout --signal=TERM --kill-after=15s 900s apt-get -y -o Dpkg::Options::=--force-confold install proot-distro curl jq coreutils util-linux procps openssl-tool tar python
audit="$(dpkg --audit)"
[ -z "$audit" ] || { printf 'Package configuration remains incomplete:\n%s\n' "$audit" >&2; exit 1; }
for package in proot-distro curl jq coreutils util-linux procps openssl-tool tar python; do
  [ "$(dpkg-query -W -f='${Status}' "$package")" = 'install ok installed' ] || { printf 'Package is not configured: %s\n' "$package" >&2; exit 1; }
done
mkdir -p "$HOME/.termux/tasker" "$HOME/.agentdock-workbench/trust"
install -m 0600 "$MODULE" "$HOME/.termux/tasker/agentdock_workbench.py"
install -m 0600 "$HOST_EXECUTOR" "$HOME/.termux/tasker/agentdock_host_executor.py"
install -m 0700 "$BRIDGE" "$HOME/.termux/tasker/agentdock-workbench"
openssl pkey -pubin -in "$PUBLISHER_KEY" -noout >/dev/null
install -m 0600 "$PUBLISHER_KEY" "$HOME/.agentdock-workbench/trust/release-ed25519-public.pem"
properties="$HOME/.termux/termux.properties"
touch "$properties"
if grep -q '^[[:space:]]*allow-external-apps[[:space:]]*=' "$properties"; then
  sed -i 's/^[[:space:]]*allow-external-apps[[:space:]]*=.*/allow-external-apps=true/' "$properties"
else
  printf '\nallow-external-apps=true\n' >>"$properties"
fi
termux-reload-settings 2>/dev/null || true
# proot-distro 5.x quiet output is one installed container name per line.
# A failed/unsupported query is not evidence that Debian is absent.
if ! installed_containers="$(proot-distro list --quiet)"; then
  printf '%s\n' 'Unable to list containers; proot-distro 5.x with list --quiet is required. No container was reinstalled.' >&2
  exit 1
fi
if ! printf '%s\n' "$installed_containers" | grep -Fxq 'debian'; then
  proot-distro install debian
fi
# Variables below must expand inside Debian, not in the Termux host shell.
# shellcheck disable=SC2016
proot-distro login debian -- /bin/sh -lc '
  set -eu
  export DEBIAN_FRONTEND=noninteractive
  timeout --signal=TERM --kill-after=15s 180s dpkg --force-confold --configure -a
  timeout --signal=TERM --kill-after=15s 900s apt-get update
  timeout --signal=TERM --kill-after=15s 900s apt-get -y -o Dpkg::Options::=--force-confold install ca-certificates curl tar jq procps openssl
  audit="$(dpkg --audit)"
  [ -z "$audit" ] || { printf "Debian package configuration remains incomplete:\n%s\n" "$audit" >&2; exit 1; }
  for package in ca-certificates curl tar jq procps openssl; do
    [ "$(dpkg-query -W -f="\${Status}" "$package")" = "install ok installed" ] || { printf "Debian package is not configured: %s\n" "$package" >&2; exit 1; }
  done
  apt-get clean
'
printf '%s\n' 'Termux bridge, Debian PRoot and the Android Core release trust key are ready.'
printf '%s\n' 'Return to AgentDock Workbench, grant RUN_COMMAND, then run Probe/Bootstrap.'
printf '%s\n' 'Use Install & Update to install the signed ARM64 Core release.'
