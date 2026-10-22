#!/usr/bin/env python3
"""External Termux deployment manager, not a Core business-state implementation.

Mutations are serialized. Journals, consistent data snapshots and recovery limits
survive a missing Android callback. The public entry never bypasses verification.
"""
from __future__ import annotations
import contextlib
import base64
import fcntl
import hashlib
import json
import os
import platform
import re
import secrets
import shutil
import signal
import socket
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.request
from pathlib import Path, PurePosixPath
from typing import Any, Callable

MAX_JSON = 65536
MAX_ARCHIVE = 256 * 1024 * 1024
MAX_EXPANDED = 512 * 1024 * 1024
MAX_BACKUP = 1024 * 1024 * 1024
MAX_ENTRIES = 25000
MAX_JOURNALS = 128
BUNDLED_CORE_NAME = "agentdock-core-arm64.tar.gz"
MAX_TERMINAL_JOURNALS = 32
TERMINAL_JOURNAL_RETENTION_SECONDS = 7 * 24 * 60 * 60
ABANDONED_TRANSACTION_RETENTION_SECONDS = 7 * 24 * 60 * 60
DIAGNOSTIC_RETENTION_SECONDS = 7 * 24 * 60 * 60
MAX_DIAGNOSTICS = 8
TEMP_RETENTION_SECONDS = 24 * 60 * 60
WORKSPACE_RUNTIME_RETENTION_SECONDS = 7 * 24 * 60 * 60
CORE_LOG_LIMIT = 8 * 1024 * 1024
PUBLIC_WORKSPACE_PARTS = ("storage", "shared", "Documents", "AgentDock", "Workspaces")
PUBLIC_ARTIFACT_PARTS = ("storage", "shared", "Documents", "AgentDock", "Artifacts")
SAFE_ABANDONED_PHASES = {"created", "manifest", "download", "verify", "prepared"}
VERSION = re.compile(r"[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?\Z")
IDENTIFIER = re.compile(r"[A-Za-z0-9_-]{1,96}\Z")
NONCE = re.compile(r"[A-Za-z0-9_-]{20,128}\Z")
READ_ONLY = {"probe", "status", "operation_query", "logs", "diagnostic_preview", "cleanup_preview", "workspace_migration_preview", "uninstall_preview"}
OPERATIONS = READ_ONLY | {"bootstrap", "pair_local_core", "install", "update", "resume", "cancel_operation", "adopt", "configure", "start", "stop", "restart", "repair", "guardian_check", "rollback", "export_diagnostics", "path_probe", "project_create", "cleanup", "workspace_migrate", "uninstall"}
TERMINAL = {"succeeded", "failed", "rolled_back", "cancelled"}
SECRET_KEYS = {"token", "bearer", "password", "secret", "credential", "credentials", "authorization", "cookie"}
DEFAULT_CONFIG = {"schema_version": 1, "port": 8765, "distro": "debian", "node_name": "AgentDock Workbench", "workspace_root": "", "recovery_base_seconds": 30, "recovery_max_seconds": 900, "recovery_max_failures": 3}

class BridgeError(Exception):
    def __init__(self, code: str, message: str, retryable: bool = False):
        super().__init__(message)
        self.code, self.retryable = code, retryable

class NoRedirect(urllib.request.HTTPRedirectHandler):
    """Reject redirects so the bearer never leaves the configured loopback URL."""
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None

def check(condition: bool, code: str, message: str) -> None:
    if not condition:
        raise BridgeError(code, message)

def redact(value: str, maximum: int = 2048) -> str:
    value = value[:max(maximum * 2, 8192)]
    value = re.sub(r"(?i)bearer\s+[^\s,;\"]+", "Bearer <redacted>", value)
    value = re.sub(r"(?i)((?:[\w-]*(?:token|secret|password)|authorization|cookie)[\"'\s:=]+)(?:\"[^\"]*\"|'[^']*'|[^\s,;]+)", r"\1<redacted>", value)
    return re.sub(r"(?<![A-Za-z0-9])[a-fA-F0-9]{64}(?![A-Za-z0-9])", "<redacted>", value)[:maximum]

def no_secrets(value: Any, depth: int = 0) -> None:
    check(depth < 20, "invalid_request", "请求嵌套过深")
    if isinstance(value, dict):
        for key, child in value.items():
            normalized = str(key).lower()
            check(not any(normalized == name or normalized.endswith("_" + name) for name in SECRET_KEYS), "invalid_request", "凭据不能经部署请求或回执传送")
            no_secrets(child, depth + 1)
    elif isinstance(value, list):
        check(len(value) <= 1000, "invalid_request", "请求数组过长")
        for child in value:
            no_secrets(child, depth + 1)

def json_bytes(value: Any) -> bytes:
    data = (json.dumps(value, ensure_ascii=False, separators=(",", ":"), allow_nan=False) + "\n").encode()
    check(len(data) <= MAX_JSON, "size_limit", "结构化记录超过 64 KiB")
    return data

def fsync_directory(path: Path) -> None:
    descriptor = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)

def atomic_bytes(path: Path, data: bytes) -> None:
    check(not path.is_symlink(), "unsafe_path", "私有状态文件不能是符号链接")
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(prefix=".wb-write-", dir=path.parent)
    try:
        with os.fdopen(descriptor, "wb") as output:
            output.write(data)
            output.flush()
            os.fsync(output.fileno())
        os.replace(temporary, path)
        fsync_directory(path.parent)
    finally:
        with contextlib.suppress(FileNotFoundError):
            os.unlink(temporary)

def atomic_json(path: Path, value: Any) -> None:
    atomic_bytes(path, json_bytes(value))

def read_json(path: Path, default: Any = None) -> Any:
    if not path.exists() and not path.is_symlink():
        return default
    check(not path.is_symlink() and path.is_file() and path.stat().st_size <= MAX_JSON, "unsafe_state", "状态文件类型或大小无效；保留原文件")
    try:
        return json.loads(path.read_bytes())
    except (ValueError, UnicodeError) as error:
        raise BridgeError("unsafe_state", "状态文件损坏；保留原文件") from error

def read_text(path: Path, default: str = "", maximum: int = 4096) -> str:
    if not path.exists() and not path.is_symlink():
        return default
    check(not path.is_symlink() and path.is_file() and path.stat().st_size <= maximum, "unsafe_state", "私有记录类型或大小无效")
    return path.read_text().strip()

def inside(path: Path, root: Path, *, exists: bool = False) -> Path:
    try:
        result = path.expanduser().resolve(strict=exists)
    except (OSError, RuntimeError) as error:
        raise BridgeError("unsafe_path", "路径无法安全解析") from error
    check(result != root and root in result.parents, "unsafe_path", "路径超出已授权根目录")
    return result

def shared_storage_root(home: Path) -> Path:
    shared_link = home.joinpath(*PUBLIC_WORKSPACE_PARTS[:2])
    check(shared_link.exists() and shared_link.is_dir(), "workspace_unavailable",
          "Termux 共享存储尚未授权；请先运行 termux-setup-storage")
    try:
        shared = shared_link.resolve(strict=True)
    except (OSError, RuntimeError) as error:
        raise BridgeError("workspace_unavailable", "Termux 共享存储无法安全解析；请重新授权共享存储") from error
    return shared

def public_managed_directory(home: Path, candidate: Path, label: str, *, create: bool = False) -> Path:
    shared = shared_storage_root(home)
    check(candidate.is_absolute() and len(str(candidate)) <= 2048, "invalid_request",
          f"{label}必须是共享存储中的绝对路径")
    try:
        resolved = candidate.resolve(strict=False)
    except (OSError, RuntimeError) as error:
        raise BridgeError("unsafe_path", f"{label}路径无法安全解析") from error
    check(resolved != shared and shared in resolved.parents, "unsafe_path", f"{label}必须位于用户可访问的 Termux 共享存储内")
    if create:
        candidate.mkdir(parents=True, exist_ok=True)
        resolved = candidate.resolve(strict=True)
    if resolved.exists():
        check(resolved.is_dir(), "unsafe_path", f"{label}路径必须是普通目录")
    return resolved

def public_workspace_root(home: Path, config: dict, *, create: bool = False) -> Path:
    """Resolve a user-visible workspace below Termux shared storage."""
    raw = str(config.get("workspace_root", "")).strip()
    candidate = Path(raw) if raw else home.joinpath(*PUBLIC_WORKSPACE_PARTS)
    return public_managed_directory(home, candidate, "工作区", create=create)

def public_artifact_root(home: Path, *, create: bool = False) -> Path:
    return public_managed_directory(home, home.joinpath(*PUBLIC_ARTIFACT_PARTS), "制品目录", create=create)

def public_roots(home: Path, config: dict, *, create: bool = False) -> tuple[Path, Path]:
    workspace = public_workspace_root(home, config, create=False)
    artifacts = public_artifact_root(home, create=False)
    check(workspace != artifacts and workspace not in artifacts.parents and artifacts not in workspace.parents,
          "workspace_conflict", "工作区与制品目录不能相同或相互嵌套")
    if create:
        workspace = public_workspace_root(home, config, create=True)
        artifacts = public_artifact_root(home, create=True)
        check(workspace != artifacts and workspace not in artifacts.parents and artifacts not in workspace.parents,
              "workspace_conflict", "工作区与制品目录不能相同或相互嵌套")
    return workspace, artifacts

def deployment_home() -> Path:
    if os.environ.get("AGENTDOCK_WORKBENCH_TEST_MODE") == "1":
        raw = os.environ.get("AGENTDOCK_WORKBENCH_TEST_HOME", "")
        check(raw and Path(raw).is_absolute(), "invalid_request", "测试部署目录必须是绝对路径")
        return Path(raw).resolve(strict=True)
    return Path.home().resolve(strict=True)

def managed_latest_mtime(path: Path, maximum: int = 4096) -> float | None:
    """Return the newest managed-tree mtime; preserve links or oversized trees."""
    newest, seen, pending = path.lstat().st_mtime, 0, [path]
    while pending:
        current = pending.pop()
        if current.is_symlink():
            return None
        newest = max(newest, current.lstat().st_mtime)
        if not current.is_dir():
            continue
        for child in current.iterdir():
            seen += 1
            if seen > maximum:
                return None
            pending.append(child)
    return newest

def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(65536), b""):
            digest.update(chunk)
    return digest.hexdigest()

def safe_extract(archive: Path, destination: Path) -> None:
    """Validate all members before writing. Links and special files are refused."""
    check(not destination.is_symlink() and destination.is_dir() and not any(destination.iterdir()), "unsafe_archive", "解包目录必须为空的私有目录")
    check(archive.stat().st_size <= MAX_ARCHIVE, "size_limit", "压缩包超过大小上限")
    with tarfile.open(archive, "r:gz") as source:
        members, seen, total = [], set(), 0
        for member in source:
            path = PurePosixPath(member.name)
            if member.name in (".", "./") and member.isdir():
                continue
            valid = (len(members) < 10000 and bool(member.name) and not path.is_absolute()
                     and ".." not in path.parts and "\\" not in member.name and ":" not in member.name
                     and all(ord(char) >= 32 for char in member.name) and bool(path.parts)
                     and len(member.name) <= 1024 and not path.parts[0].startswith(".workbench-")
                     and (member.isfile() or member.isdir()) and 0 <= member.size <= 128 * 1024 * 1024
                     and not member.issparse() and path.as_posix() not in seen)
            check(valid, "unsafe_archive", "归档包含越界、链接、重复或不支持的成员")
            total += member.size
            check(total <= MAX_EXPANDED, "size_limit", "解包总量超过上限")
            seen.add(path.as_posix())
            members.append((member, path))
        files = {path.as_posix() for member, path in members if member.isfile()}
        check(all(not any(parent.as_posix() in files for parent in path.parents) for _, path in members), "unsafe_archive", "归档文件和目录冲突")
        check(shutil.disk_usage(destination).free >= total + 64 * 1024 * 1024, "disk_full", "暂存空间不足")
        for member, path in members:
            target = destination.joinpath(*path.parts)
            if member.isdir():
                target.mkdir(mode=0o700, parents=True, exist_ok=True)
            else:
                target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
                with source.extractfile(member) as stream:
                    descriptor = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
                    with os.fdopen(descriptor, "wb") as output:
                        shutil.copyfileobj(stream, output, 65536)
                        output.flush()
                        os.fsync(output.fileno())
                target.chmod(0o755 if member.mode & 0o111 else 0o644)
        fsync_directory(destination)

def tree_size(root: Path) -> int:
    """Bounded non-following inventory; backup links must remain inside data."""
    total = count = 0
    if not root.exists():
        return 0
    for directory, directories, files in os.walk(root, followlinks=False):
        for name in directories + files:
            path = Path(directory) / name
            count += 1
            check(count <= MAX_ENTRIES, "size_limit", "备份文件数量超过上限")
            if path.is_symlink():
                check(not os.path.isabs(os.readlink(path)), "unsafe_path", "备份拒绝绝对符号链接")
                inside(path, root)
            elif path.is_file():
                total += path.stat().st_size
                check(total <= MAX_BACKUP, "size_limit", "数据备份超过 1 GiB；需要先导出或清理")
            else:
                check(path.is_dir(), "unsafe_path", "备份包含特殊文件")
    return total

def tree_digest(root: Path) -> str:
    tree_size(root)
    digest = hashlib.sha256()
    for directory, directories, files in os.walk(root, followlinks=False):
        directories.sort()
        for name in sorted(directories + files):
            path = Path(directory) / name
            relative = path.relative_to(root).as_posix()
            if path.is_symlink():
                item = [relative, "link", os.readlink(path)]
            elif path.is_file():
                item = [relative, "file", path.stat().st_mode & 0o777, path.stat().st_size, sha256(path)]
            else:
                item = [relative, "directory"]
            digest.update(json.dumps(item, ensure_ascii=False, separators=(",", ":")).encode())
            digest.update(b"\n")
    return digest.hexdigest()

def tree_size_ignoring(root: Path, ignored_relative_paths: set[str]) -> int:
    total = count = 0
    if not root.exists():
        return 0
    for directory, directories, files in os.walk(root, followlinks=False):
        current = Path(directory)
        directories[:] = [name for name in directories
                           if (current / name).relative_to(root).as_posix() not in ignored_relative_paths]
        files = [name for name in files
                 if (current / name).relative_to(root).as_posix() not in ignored_relative_paths]
        for name in directories + files:
            path = current / name
            count += 1
            check(count <= MAX_ENTRIES, "size_limit", "工作区迁移文件数量超过上限")
            if path.is_symlink():
                check(not os.path.isabs(os.readlink(path)), "unsafe_path", "工作区迁移拒绝绝对符号链接")
                inside(path, root)
            elif path.is_file():
                total += path.stat().st_size
                check(total <= MAX_BACKUP, "size_limit", "工作区迁移数据超过 1 GiB")
            else:
                check(path.is_dir(), "unsafe_path", "工作区迁移包含特殊文件")
    return total

def tree_digest_ignoring(root: Path, ignored_relative_paths: set[str]) -> str:
    tree_size_ignoring(root, ignored_relative_paths)
    digest = hashlib.sha256()
    for directory, directories, files in os.walk(root, followlinks=False):
        current = Path(directory)
        directories[:] = [name for name in directories
                           if (current / name).relative_to(root).as_posix() not in ignored_relative_paths]
        files = [name for name in files
                 if (current / name).relative_to(root).as_posix() not in ignored_relative_paths]
        directories.sort()
        for name in sorted(directories + files):
            path = current / name
            relative = path.relative_to(root).as_posix()
            if path.is_symlink():
                item = [relative, "link", os.readlink(path)]
            elif path.is_file():
                item = [relative, "file", path.stat().st_mode & 0o777, path.stat().st_size, sha256(path)]
            else:
                item = [relative, "directory"]
            digest.update(json.dumps(item, ensure_ascii=False, separators=(",", ":")).encode())
            digest.update(b"\n")
    return digest.hexdigest()

def verify_snapshot(path: Path) -> None:
    record = read_json(path / ".snapshot-complete.json", {})
    check(record.get("schema_version") == 1 and record.get("digest") == tree_digest(path / "data"),
          "backup_corrupt", "一致性备份校验失败；未回写或自动降级")

def snapshot_tree(source: Path, target: Path) -> None:
    marker = target / ".snapshot-complete.json"
    if marker.is_file():
        verify_snapshot(target)
        return
    check(not target.is_symlink(), "unsafe_path", "备份目录无效")
    size = tree_size(source)
    check(shutil.disk_usage(source.parent).free >= size + 64 * 1024 * 1024, "disk_full", "一致性备份空间不足")
    temporary = target.with_name(target.name + ".partial")
    if temporary.exists():
        check(not temporary.is_symlink(), "unsafe_path", "备份暂存目录无效")
        shutil.rmtree(temporary)
    temporary.mkdir(mode=0o700, parents=True)
    if source.exists():
        shutil.copytree(source, temporary / "data", symlinks=True, copy_function=shutil.copy2)
    else:
        (temporary / "data").mkdir(mode=0o700)
    for directory, _, files in os.walk(temporary / "data", followlinks=False):
        for name in files:
            path = Path(directory) / name
            if not path.is_symlink():
                with path.open("rb") as stream:
                    os.fsync(stream.fileno())
        fsync_directory(Path(directory))
    atomic_json(temporary / ".snapshot-complete.json", {"schema_version": 1, "bytes": size, "digest": tree_digest(temporary / "data")})
    check(not target.exists(), "unsafe_state", "未完成备份目标已存在，需要人工核对")
    os.replace(temporary, target)
    fsync_directory(target.parent)

class NodeBackend:
    """Actual Termux/PRoot I/O. CI uses an injected, temporary-filesystem backend."""
    def __init__(self, root: Path, config: dict[str, Any]):
        self.root, self.config = root, config

    def environment(self) -> dict[str, Any]:
        if os.environ.get("AGENTDOCK_WORKBENCH_TEST_MODE") == "1":
            return {"architecture": "synthetic-arm64", "arm64": True, "missing": [],
                    "distro": self.config["distro"], "port": self.config["port"]}
        missing = [name for name in ("proot-distro", "curl", "openssl", "python3") if not shutil.which(name)]
        architecture = platform.machine().lower()
        return {"architecture": architecture, "arm64": architecture in ("aarch64", "arm64"), "missing": missing,
                "distro": self.config["distro"], "port": self.config["port"]}

    def validate_environment(self) -> None:
        if os.environ.get("AGENTDOCK_WORKBENCH_TEST_MODE") == "1":
            return
        value = self.environment()
        check(not value["missing"], "requires_user_action", "缺少 Termux 依赖，请执行导出的 bootstrap")
        check(value["arm64"], "unsupported_architecture", "Core 部署要求 ARM64 Termux；界面模拟器不代表该运行架构")
        result = subprocess.run(["proot-distro", "login", self.config["distro"], "--", "/bin/sh", "-c", "command -v curl >/dev/null && command -v tar >/dev/null && command -v jq >/dev/null"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=20, close_fds=True)
        check(result.returncode == 0, "requires_user_action", "Linux 环境未就绪，请继续 bootstrap")

    @staticmethod
    def process_stamp(pid: int) -> str:
        check(1 < pid <= 2147483647, "unknown_process", "进程标识无效")
        fields = Path(f"/proc/{pid}/stat").read_text().rsplit(") ", 1)[1].split()
        boot = Path("/proc/sys/kernel/random/boot_id").read_text().strip()
        return f"{pid}:{fields[2]}:{fields[3]}:{fields[19]}:{boot}"

    def owned_pid(self) -> int | None:
        pid_text = read_text(self.root / "core.pid")
        identity = read_text(self.root / "core.identity")
        if not pid_text:
            return None
        check(pid_text.isdecimal() and 1 < int(pid_text) <= 2147483647, "unknown_process", "已有 PID 无效；未发送进程信号")
        pid = int(pid_text)
        if not Path(f"/proc/{pid}").exists():
            return None
        try:
            actual = self.process_stamp(pid)
        except (OSError, IndexError) as error:
            raise BridgeError("unknown_process", "无法读取已有进程身份；未发送信号") from error
        check(actual == identity and actual.startswith(f"{pid}:{pid}:{pid}:"), "unknown_process", "已有进程身份与受管记录不同；未接管或发送信号")
        return pid

    def bearer(self) -> str:
        value = read_text(self.root / "auth-token", maximum=128)
        check(re.fullmatch(r"[a-fA-F0-9]{64}", value) is not None, "requires_user_action", "节点身份材料无效；未生成替代凭据")
        return value

    def request(self, path: str) -> dict[str, Any]:
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
        request = urllib.request.Request(f"http://127.0.0.1:{self.config['port']}{path}", headers={"Authorization": "Bearer " + self.bearer(), "Accept": "application/json"})
        with opener.open(request, timeout=3) as response:
            data = response.read(MAX_JSON + 1)
            check(response.status == 200 and len(data) <= MAX_JSON, "health_failed", "节点管理响应无效")
            value = json.loads(data)
            check(isinstance(value, dict), "health_failed", "节点未返回结构化响应")
            return value

    def health(self, expected_version: str = "") -> bool:
        try:
            if self.owned_pid() is None:
                return False
            result = self.request("/internal/runtime/status")
            permission = self.request("/internal/runtime/permissions/effective")
            effective = permission.get("effective")
            revision = permission.get("revision") if not isinstance(effective, dict) else effective.get("revision", permission.get("revision"))
            return (result.get("ok") is True and result.get("source") == "agentdock-api"
                    and result.get("agentdock_home") == "/opt/agentdock-workbench/data"
                    and (not expected_version or result.get("version") == expected_version)
                    and type(revision) is int and revision > 0)
        except (BridgeError, OSError, ValueError, TypeError):
            return False

    def binary_version(self, directory: Path) -> str:
        relative = directory.relative_to(self.root).as_posix()
        result = subprocess.run(["proot-distro", "login", self.config["distro"], "--bind", f"{self.root}:/opt/agentdock-workbench", "--", f"/opt/agentdock-workbench/{relative}/bin/agentdock", "version", "--json"], capture_output=True, timeout=20, close_fds=True)
        check(result.returncode == 0 and len(result.stdout) <= 65536, "verification_failed", "Core 版本探针失败")
        try:
            return json.loads(result.stdout)["version"]
        except (KeyError, ValueError, TypeError) as error:
            raise BridgeError("verification_failed", "Core 版本探针格式无效") from error

    def stop(self) -> None:
        pid = self.owned_pid()
        if pid is None:
            for name in ("core.pid", "core.identity"):
                (self.root / name).unlink(missing_ok=True)
            return
        os.killpg(pid, signal.SIGTERM)
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            try:
                os.killpg(pid, 0)
            except ProcessLookupError:
                break
            time.sleep(0.1)
        else:
            check(self.owned_pid() == pid, "unknown_process", "停止期间进程身份改变；未升级信号")
            os.killpg(pid, signal.SIGKILL)
            time.sleep(0.1)
            try:
                os.killpg(pid, 0)
            except ProcessLookupError:
                pass
            else:
                raise BridgeError("stop_incomplete", "进程组仍存在，停止结果待核对")
        for name in ("core.pid", "core.identity"):
            (self.root / name).unlink(missing_ok=True)

    def start(self, expected_version: str) -> None:
        self.bearer()
        if self.health(expected_version):
            return
        self.stop()
        check(read_text(self.root / "desired-state", "stopped") == "running", "user_stopped", "停止意图已生效，未启动 Core")
        if os.environ.get("AGENTDOCK_WORKBENCH_TEST_MODE") != "1":
            with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as probe:
                try:
                    probe.bind(("127.0.0.1", self.config["port"]))
                except OSError as error:
                    raise BridgeError("port_in_use", "端口已被占用；未终止其他服务") from error
        workspace, _ = public_roots(deployment_home(), self.config, create=True)
        log = self.root / "logs/core.log"
        check(not log.is_symlink(), "unsafe_path", "受管日志链接无效")
        log.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        if log.exists() and log.stat().st_size > 8 * 1024 * 1024:
            os.replace(log, log.with_suffix(".previous.log"))
        with log.open("ab") as output:
            # The child records its PID/session before exec. Killing this caller
            # cannot turn the successful launch into an unrecorded managed process.
            child = subprocess.Popen([sys.executable, str(Path(__file__).resolve()), "--launch", str(self.root)], stdin=subprocess.DEVNULL, stdout=output, stderr=subprocess.STDOUT, start_new_session=True, close_fds=True)
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline and child.poll() is None:
            if self.health(expected_version):
                marker = workspace / (".wb-health-" + secrets.token_hex(12))
                descriptor = os.open(marker, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
                try:
                    with os.fdopen(descriptor, "wb") as output:
                        output.write(b"workspace-probe")
                    check(marker.read_bytes() == b"workspace-probe", "health_failed", "工作目录读写探针失败")
                finally:
                    marker.unlink(missing_ok=True)
                return
            if read_text(self.root / "desired-state", "stopped") != "running":
                break
            time.sleep(0.25)
        child.poll()
        self.stop()
        raise BridgeError("health_failed", "Core 身份、版本、管理鉴权或健康检查失败")

def core_launch_command(config: dict) -> str:
    # The validated node.json port is authoritative; ignore legacy port files.
    port = Deployment.validate_config(config)["port"]
    return ('export AGENTDOCK_HOME=/opt/agentdock-workbench/data; '
            'export AGENTDOCK_DEFAULT_DIR=/workspace; '
            'export AGENTDOCK_LEGACY_DEFAULT_DIR=/opt/agentdock-workbench/workspace; '
            'export AGENTDOCK_ARTIFACT_ROOT=/artifacts; '
            'export AGENTDOCK_SCRATCH_ROOT=/opt/agentdock-workbench/workspace-runtime/scratch; '
            'export AGENTDOCK_CACHE_ROOT=/opt/agentdock-workbench/workspace-runtime/cache; '
            f'export AGENTDOCK_HOST=127.0.0.1; export AGENTDOCK_PORT={port}; '
            'export AGENTDOCK_AUTH_TOKEN="$(cat /opt/agentdock-workbench/auth-token)"; '
            'export AGENTDOCK_MCP_APPS_ENABLED=false; '
            'exec /opt/agentdock-workbench/current/bin/agentdock')


def launch_owned(root: Path) -> None:
    root = root.resolve(strict=True)
    config = Deployment.validate_config(read_json(root / "node.json", DEFAULT_CONFIG))
    workspace, artifacts = public_roots(deployment_home(), config, create=True)
    check(read_text(root / "desired-state", "stopped") == "running", "user_stopped", "停止意图已生效")
    backend = NodeBackend(root, config)
    backend.bearer()
    pid = os.getpid()
    stamp = backend.process_stamp(pid)
    check(stamp.startswith(f"{pid}:{pid}:{pid}:"), "unknown_process", "无法建立独立受管进程组")
    atomic_bytes(root / "core.pid", str(pid).encode())
    atomic_bytes(root / "core.identity", stamp.encode())
    command = core_launch_command(config)
    os.execvp("proot-distro", ["proot-distro", "login", config["distro"], "--bind", f"{root}:/opt/agentdock-workbench", "--bind", f"{workspace}:/workspace", "--bind", f"{artifacts}:/artifacts", "--", "/bin/sh", "-lc", command])

class Deployment:
    def __init__(self, home: Path, backend_factory: Callable = NodeBackend, clock: Callable[[], float] = time.time, fault: Callable[[str], None] = lambda _: None):
        self.home = home.resolve(strict=True)
        self.state = self.home / ".agentdock-workbench"
        check(not self.state.is_symlink(), "unsafe_path", "产品私有目录不能是符号链接")
        adopted = read_text(self.state / "adopted-root")
        self.root = inside(Path(adopted) if adopted else self.state / "node", self.home)
        self.config = self.validate_config(read_json(self.root / "node.json", DEFAULT_CONFIG))
        self.backend_factory, self.clock, self.fault = backend_factory, clock, fault
        self.backend = backend_factory(self.root, self.config)
        for name in ("versions", "data", "logs", "transactions", "backups", "quarantine", "tmp", "workspace-runtime"):
            check(not (self.root / name).is_symlink(), "unsafe_path", "受管目录链接需要先明确迁移")

    @staticmethod
    def validate_config(value: dict) -> dict:
        check(isinstance(value, dict) and not set(value) - set(DEFAULT_CONFIG), "invalid_request", "节点配置字段无效")
        result = dict(DEFAULT_CONFIG, **value)
        check(result["schema_version"] == 1, "invalid_request", "节点配置版本无效")
        check(type(result["port"]) is int and 1024 <= result["port"] <= 65535, "invalid_request", "端口范围为 1024–65535")
        check(isinstance(result["distro"], str) and re.fullmatch(r"[a-z][a-z0-9_-]{0,31}", result["distro"]) is not None, "invalid_request", "PRoot 发行版标识无效")
        check(isinstance(result["node_name"], str) and 0 < len(result["node_name"]) <= 80, "invalid_request", "节点名称无效")
        check(isinstance(result["workspace_root"], str) and len(result["workspace_root"]) <= 2048
              and (not result["workspace_root"] or Path(result["workspace_root"]).is_absolute()),
              "invalid_request", "工作区须为空或共享存储中的绝对路径")
        check(type(result["recovery_base_seconds"]) is int and 15 <= result["recovery_base_seconds"] <= 300, "invalid_request", "恢复初始退避须为 15–300 秒")
        check(type(result["recovery_max_seconds"]) is int and result["recovery_base_seconds"] <= result["recovery_max_seconds"] <= 3600, "invalid_request", "恢复退避上限无效")
        check(type(result["recovery_max_failures"]) is int and 1 <= result["recovery_max_failures"] <= 10, "invalid_request", "连续失败上限须为 1–10")
        return result

    def workspace_root(self, create: bool = False) -> Path:
        workspace, _ = public_roots(self.home, self.config, create=create)
        return workspace

    def legacy_workspace(self) -> Path:
        return self.root / "workspace"

    def initialize(self, automatic_cleanup: bool = True, ensure_workspace: bool = True) -> None:
        for name in ("versions", "logs", "transactions", "backups", "quarantine", "tmp",
                     "workspace-runtime/scratch", "workspace-runtime/cache"):
            (self.root / name).mkdir(mode=0o700, parents=True, exist_ok=True)
        if ensure_workspace:
            self.workspace_root(create=True)
        # Missing data can be an intentional restore publication window.
        # Do not alter that evidence before resume/cancel examines the journal.
        pending = any(record.get("kind") == "deployment" and record.get("status") not in TERMINAL
                      for _, record in self.journal_records())
        if not pending:
            (self.root / "data").mkdir(mode=0o700, parents=True, exist_ok=True)
        if not (self.root / "node.json").exists():
            atomic_json(self.root / "node.json", self.config)
        if automatic_cleanup:
            self.automatic_cleanup(False)

    @contextlib.contextmanager
    def lock(self):
        directory = self.state / "locks"
        check(not directory.is_symlink(), "unsafe_path", "管理锁目录无效")
        directory.mkdir(mode=0o700, parents=True, exist_ok=True)
        descriptor = os.open(directory / "bridge.lock", os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW | os.O_CLOEXEC, 0o600)
        try:
            deadline = time.monotonic() + 10
            while True:
                try:
                    fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    break
                except BlockingIOError:
                    check(time.monotonic() < deadline, "busy", "另一个节点操作仍在执行；未重复提交")
                    time.sleep(0.05)
            yield
        finally:
            os.close(descriptor)

    def pointer(self, name: str) -> str:
        path = self.root / name
        if not path.exists() and not path.is_symlink():
            return ""
        check(path.is_symlink(), "unsafe_state", "版本指针不是受管符号链接")
        target = inside(path, self.root / "versions", exists=True)
        check(target.parent == self.root / "versions", "unsafe_path", "版本指针不在版本根目录")
        return target.name

    def set_pointer(self, name: str, version: str) -> None:
        current = self.root / name
        check(not current.exists() or current.is_symlink(), "unsafe_path", "拒绝覆盖非版本指针")
        if not version:
            current.unlink(missing_ok=True)
            fsync_directory(self.root)
            return
        check(Path(version).name == version and not version.startswith("."), "unsafe_path", "版本目录名无效")
        target = self.root / "versions" / version
        check(not target.is_symlink() and target.is_dir(), "unsafe_path", "受管版本目录无效")
        inside(target, self.root / "versions", exists=True)
        temporary = self.root / (".pointer-" + secrets.token_hex(12))
        temporary.symlink_to("versions/" + version)
        try:
            os.replace(temporary, current)
            fsync_directory(self.root)
        finally:
            temporary.unlink(missing_ok=True)

    def metadata(self, name: str) -> dict:
        if not name:
            return {}
        check(Path(name).name == name and not name.startswith("."), "unsafe_path", "版本目录标识无效")
        path = inside(self.root / "versions" / name, self.root / "versions", exists=True)
        value = read_json(path / ".workbench-release.json", {})
        check(isinstance(value, dict), "unsafe_state", "版本描述格式无效")
        return value

    def desired(self) -> str:
        state = read_text(self.root / "desired-state", "stopped")
        check(state in ("running", "stopped"), "unsafe_state", "节点运行意图无效")
        return state

    def set_desired(self, value: str) -> None:
        atomic_bytes(self.root / "desired-state", value.encode())

    def workspace_status(self) -> dict:
        legacy = self.legacy_workspace()
        legacy_pending = legacy.is_dir() and not legacy.is_symlink() and any(legacy.iterdir())
        try:
            workspace = self.workspace_root(create=False)
            return {"path": str(workspace), "available": workspace.is_dir(), "user_accessible": True,
                    "legacy_private_pending": legacy_pending}
        except BridgeError as error:
            return {"path": str(self.home.joinpath(*PUBLIC_WORKSPACE_PARTS)), "available": False,
                    "user_accessible": True, "legacy_private_pending": legacy_pending, "error_code": error.code,
                    "message": redact(str(error))}

    def rotate_logs(self) -> dict:
        logs = self.root / "logs"
        logs.mkdir(mode=0o700, parents=True, exist_ok=True)
        current, previous = logs / "core.log", logs / "core.previous.log"
        if not current.exists():
            return {"rotated": False, "current_bytes": 0}
        check(not current.is_symlink() and current.is_file() and not previous.is_symlink(),
              "unsafe_path", "受管日志文件类型无效")
        size = current.stat().st_size
        if size <= CORE_LOG_LIMIT:
            return {"rotated": False, "current_bytes": size}
        with current.open("rb") as source:
            source.seek(max(0, size - CORE_LOG_LIMIT))
            retained = source.read(CORE_LOG_LIMIT)
        atomic_bytes(previous, retained)
        with current.open("r+b") as output:
            output.truncate(0)
            output.flush()
            os.fsync(output.fileno())
        return {"rotated": True, "current_bytes": 0, "previous_bytes": len(retained)}

    @staticmethod
    def _remove_managed_entry(path: Path, parent: Path) -> None:
        check(path.parent == parent and path.name not in ("", ".", ".."), "unsafe_path", "清理目标超出受管目录")
        if path.is_symlink() or path.is_file():
            path.unlink(missing_ok=True)
        elif path.is_dir():
            shutil.rmtree(path)

    def automatic_cleanup(self, preview: bool = True) -> dict:
        now = self.clock()
        active = read_text(self.root / "active-operation")
        records = sorted(self.journal_records(), key=lambda pair: pair[1].get("updated_at", 0), reverse=True)
        terminal = [(path, value) for path, value in records if value.get("status") in TERMINAL]
        terminal_paths = [path.parent for index, (path, value) in enumerate(terminal)
                          if path.parent.name != active and (index >= MAX_TERMINAL_JOURNALS
                          or now - float(value.get("updated_at", 0)) >= TERMINAL_JOURNAL_RETENTION_SECONDS)]
        logs = self.root / "logs"
        diagnostics = sorted([path for path in (list(logs.glob("diagnostics-*.json")) if logs.is_dir() else [])
                              if path.is_file() and not path.is_symlink()], key=lambda path: path.stat().st_mtime, reverse=True)
        wall_now = time.time()
        diagnostic_paths = [path for index, path in enumerate(diagnostics)
                            if index >= MAX_DIAGNOSTICS or wall_now - path.stat().st_mtime >= DIAGNOSTIC_RETENTION_SECONDS]
        temporary = self.root / "tmp"
        temp_paths = [path for path in (list(temporary.iterdir()) if temporary.is_dir() else [])
                      if wall_now - path.lstat().st_mtime >= TEMP_RETENTION_SECONDS]
        runtime_paths = []
        runtime_skipped = 0
        runtime_root = self.root / "workspace-runtime"
        for category in ("scratch", "cache"):
            parent = runtime_root / category
            candidates = sorted(list(parent.iterdir()) if parent.is_dir() else [], key=lambda path: path.lstat().st_mtime)
            runtime_skipped += max(0, len(candidates) - 256)
            for path in candidates[:256]:
                latest = managed_latest_mtime(path)
                if latest is not None and wall_now - latest >= WORKSPACE_RUNTIME_RETENTION_SECONDS:
                    runtime_paths.append((category, path))
        result = {"terminal_transactions": sorted(path.name for path in terminal_paths),
                  "diagnostics": sorted(path.name for path in diagnostic_paths),
                  "temporary": sorted(path.name for path in temp_paths),
                  "workspace_runtime": sorted(category + "/" + path.name for category, path in runtime_paths),
                  "workspace_runtime_skipped": runtime_skipped,
                  "preview": preview}
        if not preview:
            for path in terminal_paths:
                self._remove_managed_entry(path, self.root / "transactions")
            for path in diagnostic_paths:
                self._remove_managed_entry(path, logs)
            for path in temp_paths:
                self._remove_managed_entry(path, temporary)
            for category, path in runtime_paths:
                self._remove_managed_entry(path, runtime_root / category)
            result["log_rotation"] = self.rotate_logs()
        return result

    def current_version(self) -> str:
        return self.metadata(self.pointer("current")).get("version", "")

    def status(self) -> dict:
        version = self.current_version()
        owned, error = False, ""
        try:
            owned = self.backend.owned_pid() is not None
        except BridgeError as failure:
            error = failure.code
        healthy = owned and self.backend.health(version)
        active = read_text(self.root / "active-operation")
        return {"status": "running" if healthy else ("requires_user_action" if error else "degraded" if owned else "stopped"),
                "message": "节点身份和管理鉴权已验证" if healthy else "节点状态待核对" if error else "没有健康的受管 Core",
                "data": {"version": version, "desired_state": self.desired(), "healthy": healthy, "process_identity": "verified" if owned else "unknown" if error else "absent", "active_operation_id": active, "node": self.config,
                         "environment": self.backend.environment(), "recovery": read_json(self.root / "recovery.json", {}),
                         "workspace": self.workspace_status(),
                         "bundled_core_ready": self.bundled_core_ready(), "observed_at": self.clock()}}

    def journal_path(self, identity: str) -> Path:
        check(isinstance(identity, str) and IDENTIFIER.fullmatch(identity) is not None, "invalid_request", "操作标识无效")
        parent = self.root / "transactions" / identity
        check(not parent.is_symlink(), "unsafe_path", "操作目录不能是符号链接")
        return parent / "journal.json"

    def journal_records(self) -> list[tuple[Path, dict]]:
        paths = list((self.root / "transactions").glob("*/journal.json"))
        check(len(paths) <= 256, "capacity", "操作记录数量异常；停止自动清理")
        records = []
        for path in paths:
            check(not path.parent.is_symlink(), "unsafe_path", "操作记录目录无效")
            record = read_json(path)
            check(isinstance(record, dict), "unsafe_state", "操作记录格式无效")
            records.append((path, record))
        return records

    def reserve_journal(self) -> None:
        records = self.journal_records()
        if len(records) < MAX_JOURNALS:
            return
        self.automatic_cleanup(False)
        records = self.journal_records()
        if len(records) < MAX_JOURNALS:
            return
        terminal = [(path, value) for path, value in records if value.get("status") in TERMINAL]
        for path, _ in sorted(terminal, key=lambda pair: pair[1].get("updated_at", 0))[:len(records) - MAX_JOURNALS + 1]:
            self._remove_managed_entry(path.parent, self.root / "transactions")
        check(len(self.journal_records()) < MAX_JOURNALS, "capacity", "未决操作达到上限；请先查询和核对原操作")

    def save(self, journal: dict, phase: str | None = None, **changes) -> None:
        if phase is not None:
            journal["phase"] = phase
        journal.update(changes, updated_at=self.clock())
        atomic_json(self.journal_path(journal["operation_id"]), journal)
        self.fault(journal["phase"])

    def query(self, identity: str) -> dict:
        record = read_json(self.journal_path(identity))
        check(record is not None, "not_found", "操作记录不存在，未自动重放请求")
        result = {key: record.get(key) for key in ("operation_id", "operation", "phase", "status", "source_version", "target_version", "created_at", "updated_at", "retryable", "message", "backup", "failure_code")}
        if isinstance(record.get("result"), dict):
            result["result"] = record["result"]
        return result

    def ensure_no_active(self, identity: str = "") -> None:
        # The scan also covers a crash between journaling and publishing the active ID.
        for _, record in self.journal_records():
            if record.get("kind") == "deployment" and record.get("operation_id") != identity:
                check(record.get("status") in TERMINAL, "transaction_pending", "存在未完成部署事务；请按原 operation ID 继续或回退")

    def finish(self, journal: dict, status: str, message: str) -> dict:
        changes = {"status": status, "retryable": False, "message": message}
        if status == "succeeded":
            changes["failure_code"] = ""
        self.save(journal, "complete", **changes)
        if read_text(self.root / "active-operation") == journal["operation_id"]:
            (self.root / "active-operation").unlink(missing_ok=True)
            fsync_directory(self.root)
        directory = self.journal_path(journal["operation_id"]).parent
        for name in ("core.tar.gz.partial", "manifest.sig"):
            (directory / name).unlink(missing_ok=True)
        outward = "ok" if status == "cancelled" else status
        if status == "succeeded":
            outward = {"install": "installed", "update": "updated", "rollback": "rolled_back"}.get(journal["operation"], "ok")
        data = self.query(journal["operation_id"])
        data["desired_state"] = self.desired()
        return {"status": outward, "message": message, "data": data}

    def make_journal(self, operation: str, identity: str, payload: dict) -> dict:
        fingerprint = hashlib.sha256(json_bytes(payload)).hexdigest()
        existing = read_json(self.journal_path(identity))
        if existing is not None:
            check(existing.get("operation") == operation and existing.get("fingerprint") == fingerprint, "operation_conflict", "同一 operation ID 的请求内容不同；未重放")
            return existing
        self.ensure_no_active(identity)
        self.reserve_journal()
        source = self.pointer("current")
        journal = {"schema_version": 1, "kind": "deployment", "operation_id": identity, "operation": operation, "fingerprint": fingerprint,
                   "payload": payload, "phase": "created", "status": "running", "created_at": self.clock(), "updated_at": self.clock(),
                   "source": source, "source_version": self.metadata(source).get("version", ""), "target": "", "target_version": "", "backup": identity,
                   "retryable": True, "message": "部署事务已建立", "desired_before": self.desired()}
        self.save(journal)
        atomic_bytes(self.root / "active-operation", identity.encode())
        return journal

    def bundled_core(self) -> Path:
        archive = self.state / "bundle" / BUNDLED_CORE_NAME
        check(not archive.is_symlink() and archive.is_file() and 0 < archive.stat().st_size <= MAX_ARCHIVE,
              "bundled_core_missing", "APK 内置 Core 尚未导入 Termux；请重新导出并运行完整桥包")
        return archive

    def bundled_core_ready(self) -> bool:
        archive = self.state / "bundle" / BUNDLED_CORE_NAME
        return not archive.is_symlink() and archive.is_file() and 0 < archive.stat().st_size <= MAX_ARCHIVE

    def prepare_install(self, journal: dict) -> None:
        payload = journal["payload"]
        self.backend.validate_environment()
        version = payload.get("apk_version", "")
        check(isinstance(version, str) and len(version) <= 80 and VERSION.fullmatch(version) is not None,
              "version_mismatch", "必须提供当前 APK 的准确产品版本")
        archive = self.bundled_core()
        archive_digest = sha256(archive)
        bound_digest = str(journal.get("bundle_digest", ""))
        check(not bound_digest or bound_digest == archive_digest, "bundle_changed",
              "原操作绑定的 APK 内置 Core 已变化，请取消后重新确认")
        if not bound_digest:
            self.save(journal, bundle_digest=archive_digest)
        target_name = version + "-" + archive_digest[:12]
        target = self.root / "versions" / target_name
        stage = self.root / "versions" / (".stage-" + journal["operation_id"])
        self.save(journal, "verify", target=target_name)
        if target.exists():
            metadata = self.metadata(target_name)
            check(metadata.get("sha256") == archive_digest and metadata.get("binary_sha256") == sha256(target / "bin/agentdock"),
                  "version_conflict", "已有版本目录内容不同；未覆盖")
        else:
            if stage.exists():
                check(not stage.is_symlink(), "unsafe_path", "版本暂存目录无效")
                shutil.rmtree(stage)
            stage.mkdir(mode=0o700)
            safe_extract(archive, stage)
            check((stage / "bin/agentdock").is_file() and os.access(stage / "bin/agentdock", os.X_OK), "verification_failed", "缺少可执行 Core")
            check(self.backend.binary_version(stage) == version, "version_mismatch", "APK 内置 Core 自报版本与 APK 不一致")
            metadata = {"version": version, "platform": "linux", "arch": "arm64", "sha256": archive_digest}
            metadata["binary_sha256"] = sha256(stage / "bin/agentdock")
            metadata["data_schema"] = "apk-bundled"
            atomic_json(stage / ".workbench-release.json", metadata)
            os.replace(stage, target)
            fsync_directory(target.parent)
        self.save(journal, "prepared", target=target_name, target_version=version)

    def source_healthy(self, journal: dict) -> bool:
        source = journal["source"]
        if not source:
            return False
        if self.backend.health(journal["source_version"]):
            return True
        proof = read_json(self.root / "health-proof.json", {})
        binary = self.root / "versions" / source / "bin/agentdock"
        return proof.get("version_directory") == source and binary.is_file() and proof.get("binary_sha256") == sha256(binary)

    def backup_path(self, identity: str) -> Path:
        check(isinstance(identity, str) and IDENTIFIER.fullmatch(identity) is not None, "unsafe_state", "备份标识无效")
        path = self.root / "backups" / identity
        check(not path.is_symlink(), "unsafe_path", "备份目录不能是符号链接")
        return path

    def restore_data(self, journal: dict, backup: str, label: str) -> None:
        source = self.backup_path(backup)
        check(read_json(source / ".snapshot-complete.json", {}).get("schema_version") == 1, "backup_missing", "缺少完整一致性备份；未自动降级")
        verify_snapshot(source)
        destination = self.root / "data"
        quarantine = self.root / "quarantine" / (journal["operation_id"] + "-" + label)
        restored = self.root / (".restore-" + journal["operation_id"] + "-" + label)
        marker = self.journal_path(journal["operation_id"]).parent / (label + "-restored.json")
        if marker.exists():
            (destination / ".restore-ready").unlink(missing_ok=True)
            return
        if not (restored / ".restore-ready").exists():
            if restored.exists():
                check(not restored.is_symlink(), "unsafe_path", "恢复暂存目录无效")
                shutil.rmtree(restored)
            size = tree_size(source / "data")
            check(shutil.disk_usage(self.root).free > size + 64 * 1024 * 1024, "disk_full", "恢复空间不足；备份保持完整")
            shutil.copytree(source / "data", restored, symlinks=True)
            atomic_bytes(restored / ".restore-ready", b"1")
        self.fault(label + "-prepared")
        if not quarantine.exists() and destination.exists():
            os.replace(destination, quarantine)
            fsync_directory(self.root)
            self.fault(label + "-quarantined")
        if not destination.exists():
            os.replace(restored, destination)
            fsync_directory(self.root)
            self.fault(label + "-published")
        check((destination / ".restore-ready").exists(), "restore_conflict", "恢复目标已有未知数据；未覆盖")
        atomic_json(marker, {"schema_version": 1, "backup": backup})
        self.fault(label + "-marked")
        (destination / ".restore-ready").unlink(missing_ok=True)
        if restored.exists():
            shutil.rmtree(restored)

    def recover_previous(self, journal: dict) -> dict:
        self.save(journal, "rollback", status="recovering")
        self.backend.stop()
        if (self.backup_path(journal["backup"]) / ".snapshot-complete.json").exists():
            self.restore_data(journal, journal["backup"], "failed-new-data")
        self.set_pointer("current", journal["source"])
        if journal["source"] and self.desired() == "running":
            self.backend.start(journal["source_version"])
        return self.finish(journal, "rolled_back", "部署未提交，已恢复旧版本与一致性数据；失败数据仍在私有隔离目录")

    def execute_transaction(self, operation: str, identity: str, payload: dict) -> dict:
        new = read_json(self.journal_path(identity)) is None
        journal = self.make_journal(operation, identity, payload)
        if journal["status"] in TERMINAL:
            if read_text(self.root / "active-operation") == identity:
                (self.root / "active-operation").unlink(missing_ok=True)
                fsync_directory(self.root)
            success = journal["status"] == "succeeded"
            if success:
                try:
                    self.cleanup(False)
                except (BridgeError, OSError):
                    pass
            return {"status": "ok" if success else "failed", "message": "返回原操作终态，未重复执行", "data": self.query(identity)}
        if new and payload.get("start_after_install") is True:
            self.set_desired("running")
        try:
            if journal["phase"] == "rollback":
                return self.recover_previous(journal)
            if journal["phase"] in {"created", "manifest", "download", "verify"}:
                if operation == "rollback":
                    fallback = read_json(self.root / "fallback.json", {})
                    target = fallback.get("version_directory", "")
                    check(target and fallback.get("backup"), "backup_missing", "没有可验证的完整回退点")
                    metadata = self.metadata(target)
                    check(metadata.get("binary_sha256") == sha256(self.root / "versions" / target / "bin/agentdock"), "verification_failed", "回退二进制改变；未自动降级")
                    self.backup_path(fallback["backup"])
                    self.save(journal, "prepared", target=target, target_version=metadata["version"], restore_backup=fallback["backup"])
                else:
                    self.prepare_install(journal)
            if journal["phase"] == "prepared":
                check(self.metadata(journal["target"]).get("binary_sha256") == sha256(self.root / "versions" / journal["target"] / "bin/agentdock"), "verification_failed", "暂存二进制已改变，未切换")
                if operation in {"install", "update"} and journal["source"] == journal["target"]:
                    # A new request ID does not make an identical signed artifact
                    # a version transition. Preserve the distinct fallback.
                    if (payload.get("start_after_install") is True and self.desired() == "running"
                            and not self.backend.health(journal["target_version"])):
                        self.backend.start(journal["target_version"])
                        check(self.backend.health(journal["target_version"]), "health_failed", "当前制品启动验证失败")
                    return self.finish(journal, "succeeded", "已安装相同制品，保留现有回退版本与数据备份")
                self.save(journal, "stop", source_was_healthy=self.source_healthy(journal))
            if journal["phase"] == "stop":
                self.backend.stop()
                self.save(journal, "backup")
            if journal["phase"] == "backup":
                snapshot_tree(self.root / "data", self.backup_path(journal["backup"]))
                self.save(journal, "switch")
            if journal["phase"] == "switch":
                if operation == "rollback":
                    self.restore_data(journal, journal["restore_backup"], "pre-rollback-data")
                self.set_pointer("current", journal["target"])
                self.fault("pointer-published")
                self.save(journal, "start")
            if journal["phase"] == "start":
                check(self.desired() == "running", "user_stopped", "节点停止意图已保留；请明确恢复后继续验证")
                self.backend.start(journal["target_version"])
                self.save(journal, "health")
            if journal["phase"] == "health":
                check(self.backend.health(journal["target_version"]), "health_failed", "新版本未通过真实节点身份与管理鉴权检查")
                self.save(journal, "commit")
            if journal["phase"] == "commit":
                atomic_json(self.root / "health-proof.json", {"version_directory": journal["target"], "binary_sha256": self.metadata(journal["target"])["binary_sha256"], "verified_at": self.clock()})
                if journal["source"] and journal["source"] != journal["target"] and journal.get("source_was_healthy"):
                    atomic_json(self.root / "fallback.json", {"version_directory": journal["source"], "backup": journal["backup"], "product_version": journal["source_version"]})
                    self.set_pointer("previous", journal["source"])
                result = self.finish(journal, "succeeded", "版本、管理鉴权和健康验证通过，部署事务已提交")
                try:
                    result["data"]["retention"] = self.cleanup(False)
                except (BridgeError, OSError):
                    result["data"]["retention"] = {"warning": "安装已提交；旧文件清理需要另行核对"}
                return result
            raise BridgeError("unsafe_state", "未知部署阶段；保留原事务")
        except BridgeError as error:
            safe = redact(str(error))
            self.save(journal, failure_code=error.code, message=safe, retryable=True, status="requires_user_action")
            if journal["phase"] in {"switch", "start", "health", "commit", "rollback"} and error.code != "user_stopped":
                try:
                    result = self.recover_previous(journal)
                    result["status"] = "failed"
                    result["message"] = safe + "；旧版本及数据已回退"
                    return result
                except (BridgeError, OSError, subprocess.SubprocessError) as rollback_error:
                    self.save(journal, "rollback", status="requires_user_action", message="回退待继续核对，所有备份与版本引用已保留：" + redact(str(rollback_error)), retryable=True)
            return {"status": "pending_manifest" if error.code == "pending_manifest" else "requires_user_action", "message": safe, "data": self.query(identity)}
        except (OSError, ValueError, subprocess.SubprocessError, tarfile.TarError) as error:
            self.save(journal, status="requires_user_action", failure_code="io_failed", message="部署 I/O 中断，可查询原 operation ID 后继续", retryable=True)
            raise BridgeError("io_failed", "部署 I/O 中断；未重复执行，原事务与备份保留", True) from error

    def recovery(self, manual_reset: bool = False) -> dict:
        if self.desired() != "running":
            return {"status": "stopped", "message": "用户已停止节点，未自动恢复"}
        self.ensure_no_active()
        version = self.current_version()
        record = read_json(self.root / "recovery.json", {"failures": 0, "next_attempt_at": 0, "circuit_open": False})
        check(isinstance(record, dict) and type(record.get("failures", 0)) is int and 0 <= record.get("failures", 0) <= 10, "unsafe_state", "恢复状态损坏，未自动重置")
        if self.backend.health(version):
            atomic_json(self.root / "recovery.json", {"failures": 0, "next_attempt_at": 0, "circuit_open": False, "last_success": self.clock()})
            return {"status": "healthy", "message": "节点健康，无需恢复"}
        if manual_reset:
            record = {"failures": 0, "next_attempt_at": 0, "circuit_open": False}
        check(not record.get("circuit_open"), "recovery_circuit_open", "自动恢复已熔断，需要用户核对并明确恢复")
        check(self.clock() >= record.get("next_attempt_at", 0), "recovery_backoff", "恢复退避尚未结束，未重复启动")
        record.update(failures=record.get("failures", 0) + 1, last_attempt_at=self.clock())
        record["next_attempt_at"] = self.clock() + min(self.config["recovery_max_seconds"], self.config["recovery_base_seconds"] * (2 ** (record["failures"] - 1)))
        record["circuit_open"] = record["failures"] >= self.config["recovery_max_failures"]
        atomic_json(self.root / "recovery.json", record)
        try:
            self.backend.start(version)
            check(self.backend.health(version), "health_failed", "恢复后的管理鉴权未通过")
        except (BridgeError, OSError, subprocess.SubprocessError) as error:
            record["last_error"] = redact(str(error))
            atomic_json(self.root / "recovery.json", record)
            raise BridgeError("health_failed", "恢复未通过；失败次数、退避时间和熔断状态已持久化") from error
        atomic_json(self.root / "recovery.json", {"failures": 0, "next_attempt_at": 0, "circuit_open": False, "last_success": self.clock()})
        current = self.pointer("current")
        if current and self.metadata(current).get("binary_sha256"):
            atomic_json(self.root / "health-proof.json", {"version_directory": current, "binary_sha256": self.metadata(current)["binary_sha256"], "verified_at": self.clock()})
        return {"status": "healthy", "message": "当前版本已恢复，未安装或更新版本"}

    def legacy_workspace_layout(self) -> dict:
        registry = read_json(self.root / "data/workspaces.json", {})
        if not isinstance(registry, dict) or registry.get("schema_version") != 1:
            return {"managed": False, "workspace_id": "", "ignored_paths": []}
        records = registry.get("workspaces")
        if not isinstance(records, list) or len(records) > 128:
            return {"managed": False, "workspace_id": "", "ignored_paths": []}
        legacy_root, current_root = "/opt/agentdock-workbench/workspace", "/workspace"
        for record in records:
            if not isinstance(record, dict) or record.get("runtime") != "unix" or record.get("root") not in (legacy_root, current_root):
                continue
            identity = str(record.get("workspace_id", ""))
            if re.fullmatch(r"wsp_[a-f0-9]{16}", identity) is None:
                continue
            old_layout = (record.get("root") == legacy_root and
                          record.get("artifact_root") == f"{legacy_root}/artifacts/{identity}" and
                          record.get("scratch_root") == f"{legacy_root}/scratch/{identity}" and
                          record.get("cache_root") == f"{legacy_root}/cache")
            new_layout = (record.get("root") == current_root and
                          record.get("artifact_root") == f"/artifacts/{identity}" and
                          record.get("scratch_root") == f"/opt/agentdock-workbench/workspace-runtime/scratch/{identity}" and
                          record.get("cache_root") == "/opt/agentdock-workbench/workspace-runtime/cache")
            if old_layout or new_layout:
                return {"managed": True, "workspace_id": identity,
                        "ignored_paths": ["cache", "scratch", f"artifacts/{identity}"]}
        return {"managed": False, "workspace_id": "", "ignored_paths": []}

    def workspace_migration_preview(self) -> dict:
        target = self.workspace_root(create=False)
        legacy = self.legacy_workspace()
        check(not legacy.is_symlink(), "unsafe_path", "旧私有工作区不能是符号链接")
        legacy_present = legacy.is_dir() and any(legacy.iterdir())
        target_empty = not target.exists() or target.is_dir() and not any(target.iterdir())
        layout = self.legacy_workspace_layout() if legacy_present else {"managed": False, "workspace_id": "", "ignored_paths": []}
        ignored = set(layout["ignored_paths"])
        artifact_source = legacy / "artifacts" / layout["workspace_id"] if layout["managed"] else None
        if artifact_source is not None and (artifact_source.exists() or artifact_source.is_symlink()):
            check(artifact_source.is_dir() and not artifact_source.is_symlink(), "unsafe_path", "旧制品目录类型无效")
        artifact_present = artifact_source is not None and artifact_source.is_dir()
        artifact_container = legacy / "artifacts"
        if artifact_present and artifact_container.is_dir() and not artifact_container.is_symlink():
            other_artifacts = [path for path in artifact_container.iterdir() if path.name != layout["workspace_id"]]
            if not other_artifacts:
                ignored.discard(f"artifacts/{layout['workspace_id']}")
                ignored.add("artifacts")
        artifact_target = public_artifact_root(self.home, create=False) / layout["workspace_id"] if layout["managed"] else None
        if artifact_target is not None and (artifact_target.exists() or artifact_target.is_symlink()):
            check(artifact_target.is_dir() and not artifact_target.is_symlink(), "unsafe_path", "公共制品目标类型无效")
        artifact_target_empty = artifact_target is None or not artifact_target.exists() or artifact_target.is_dir() and not any(artifact_target.iterdir())
        transient_bytes = 0
        if layout["managed"]:
            transient_bytes = tree_size(legacy / "scratch") + tree_size(legacy / "cache")
        return {"legacy_path": str(legacy), "target_path": str(target), "legacy_present": legacy_present,
                "legacy_bytes": tree_size(legacy) if legacy_present else 0,
                "legacy_digest": tree_digest(legacy) if legacy_present else "",
                "workspace_bytes": tree_size_ignoring(legacy, ignored) if legacy_present else 0,
                "workspace_digest": tree_digest_ignoring(legacy, ignored) if legacy_present else "",
                "managed_layout": layout["managed"], "workspace_id": layout["workspace_id"],
                "ignored_paths": sorted(ignored), "discarded_transient_bytes": transient_bytes,
                "artifact_present": artifact_present,
                "artifact_bytes": tree_size(artifact_source) if artifact_present else 0,
                "artifact_digest": tree_digest(artifact_source) if artifact_present else "",
                "artifact_target_path": str(artifact_target) if artifact_target is not None else "",
                "artifact_target_empty": artifact_target_empty,
                "target_empty": target_empty, "workspace_preserved": True, "artifacts_preserved": True}

    def migrate_workspace(self, identity: str, payload: dict) -> dict:
        preview = self.workspace_migration_preview()
        expected = hashlib.sha256(json_bytes(preview)).hexdigest()
        check(payload.get("confirm_migration") is True and secrets.compare_digest(expected, str(payload.get("preview_digest", ""))),
              "preview_changed", "工作区迁移预览已变化，请重新确认")
        check(self.backend.owned_pid() is None, "node_running", "迁移工作区前须停止 Core")
        legacy = self.legacy_workspace()
        target = self.workspace_root(create=True)
        if not preview["legacy_present"]:
            if legacy.is_dir() and not any(legacy.iterdir()):
                legacy.rmdir()
            return {"status": "ok", "message": "没有需要迁移的旧私有工作区", "data": preview}
        check(preview["target_empty"], "workspace_conflict", "公共工作区已有文件，未合并或覆盖")
        check(preview["artifact_target_empty"], "workspace_conflict", "公共制品目标已有文件，未合并或覆盖")
        stage = target.parent / (".agentdock-workspace-migration-" + identity)
        artifact_source = legacy / "artifacts" / preview["workspace_id"] if preview["artifact_present"] else None
        artifact_target = Path(preview["artifact_target_path"]) if preview["artifact_present"] else None
        artifact_stage = artifact_target.parent / (".agentdock-artifact-migration-" + identity) if artifact_target is not None else None
        check(not stage.exists() and not stage.is_symlink(), "workspace_conflict", "公共工作区迁移暂存目录已存在")
        if artifact_stage is not None:
            public_artifact_root(self.home, create=True)
            check(not artifact_stage.exists() and not artifact_stage.is_symlink(), "workspace_conflict", "公共制品迁移暂存目录已存在")
        ignored = set(preview["ignored_paths"])
        published: list[tuple[Path, str]] = []
        committed = False
        try:
            def ignore_managed(directory: str, names: list[str]) -> set[str]:
                current = Path(directory)
                return {name for name in names if (current / name).relative_to(legacy).as_posix() in ignored}
            shutil.copytree(legacy, stage, symlinks=True, ignore=ignore_managed if ignored else None)
            check(tree_digest(stage) == preview["workspace_digest"], "verification_failed", "公共工作区复制校验失败；旧工作区保持不变")
            if artifact_stage is not None and artifact_source is not None:
                shutil.copytree(artifact_source, artifact_stage, symlinks=True)
                check(tree_digest(artifact_stage) == preview["artifact_digest"], "verification_failed", "公共制品复制校验失败；旧工作区保持不变")
            if target.exists():
                check(target.is_dir() and not any(target.iterdir()), "workspace_conflict", "公共工作区在迁移期间发生变化")
                target.rmdir()
            os.replace(stage, target)
            published.append((target, preview["workspace_digest"]))
            check(tree_digest(target) == preview["workspace_digest"], "verification_failed", "公共工作区发布后校验失败；旧工作区保持不变")
            if artifact_stage is not None and artifact_target is not None:
                if artifact_target.exists():
                    check(artifact_target.is_dir() and not any(artifact_target.iterdir()), "workspace_conflict", "公共制品目录在迁移期间发生变化")
                    artifact_target.rmdir()
                os.replace(artifact_stage, artifact_target)
                published.append((artifact_target, preview["artifact_digest"]))
                check(tree_digest(artifact_target) == preview["artifact_digest"], "verification_failed", "公共制品发布后校验失败；旧工作区保持不变")
            check(tree_digest(legacy) == preview["legacy_digest"], "preview_changed", "旧私有工作区在迁移期间发生变化；已撤销公共副本")
            atomic_json(self.root / "workspace-migration.json", {"schema_version": 1, "target": str(target),
                        "digest": preview["workspace_digest"], "artifact_target": preview["artifact_target_path"],
                        "artifact_digest": preview["artifact_digest"], "discarded_transient_bytes": preview["discarded_transient_bytes"],
                        "migrated_at": self.clock()})
            committed = True
            legacy_retained = False
            try:
                shutil.rmtree(legacy)
            except OSError:
                legacy_retained = True
        except Exception:
            if not committed:
                for published_path, digest in reversed(published):
                    if published_path.is_dir() and not published_path.is_symlink() and tree_digest(published_path) == digest:
                        shutil.rmtree(published_path)
            raise
        finally:
            if stage.exists() and not stage.is_symlink():
                shutil.rmtree(stage)
            if artifact_stage is not None and artifact_stage.exists() and not artifact_stage.is_symlink():
                shutil.rmtree(artifact_stage)
        return {"status": "ok", "message": "旧私有工作区已校验迁移到用户可访问目录",
                "data": {"target_path": str(target), "bytes": preview["workspace_bytes"],
                         "artifact_target_path": preview["artifact_target_path"], "artifact_bytes": preview["artifact_bytes"],
                         "discarded_transient_bytes": preview["discarded_transient_bytes"], "legacy_retained": legacy_retained,
                         "workspace_preserved": True, "artifacts_preserved": True}}

    def uninstall_preview(self) -> dict:
        workspace = self.workspace_status()
        try:
            artifacts = str(public_artifact_root(self.home, create=False))
        except BridgeError:
            artifacts = str(self.home.joinpath(*PUBLIC_ARTIFACT_PARTS))
        tasker = self.home / ".termux" / "tasker"
        bridge_names = ("agentdock-workbench", "agentdock_workbench.py", "agentdock_host_executor.py")
        marker = self.state / ("managed-distro-" + self.config["distro"])
        default_root = self.state / "node"
        return {"private_runtime_exists": self.state.exists() or self.root.exists(),
                "private_root": str(self.root), "adopted_private_root": self.root != default_root,
                "bridge_files": sorted(name for name in bridge_names if (tasker / name).exists() or (tasker / name).is_symlink()),
                "managed_distro": self.config["distro"] if marker.is_file() and not marker.is_symlink() else "",
                "workspace_path": workspace.get("path", ""), "workspace_preserved": True,
                "artifact_path": artifacts, "artifacts_preserved": True,
                "termux_packages_preserved": True, "shared_storage_preserved": True}

    def uninstall(self, payload: dict) -> dict:
        preview = self.uninstall_preview()
        expected = hashlib.sha256(json_bytes(preview)).hexdigest()
        check(payload.get("confirm_uninstall") is True and secrets.compare_digest(expected, str(payload.get("preview_digest", ""))),
              "preview_changed", "完整卸载预览已变化，请重新确认")
        self.backend.stop()
        marker = self.state / ("managed-distro-" + self.config["distro"])
        distro_removed = False
        if marker.is_file() and not marker.is_symlink():
            if os.environ.get("AGENTDOCK_WORKBENCH_TEST_MODE") == "1":
                distro_removed = True
            else:
                listed = subprocess.run(["proot-distro", "list", "--quiet"], capture_output=True, text=True, timeout=20, close_fds=True)
                check(listed.returncode == 0, "uninstall_failed", "无法确认受管 PRoot 状态；私有文件尚未删除")
                if self.config["distro"] in listed.stdout.splitlines():
                    removed = subprocess.run(["proot-distro", "remove", self.config["distro"]], capture_output=True, timeout=180, close_fds=True)
                    check(removed.returncode == 0, "uninstall_failed", "受管 PRoot 删除失败；私有文件尚未删除")
                    distro_removed = True
        default_root = self.state / "node"
        if self.root != default_root and self.root.exists():
            check(not self.root.is_symlink() and self.home in self.root.parents, "unsafe_path", "接管节点不在 Termux 私有目录内")
            shutil.rmtree(self.root)
        if self.state.exists():
            check(not self.state.is_symlink(), "unsafe_path", "产品私有目录类型无效")
            shutil.rmtree(self.state)
        tasker = self.home / ".termux" / "tasker"
        for name in ("agentdock-workbench", "agentdock_workbench.py", "agentdock_host_executor.py"):
            path = tasker / name
            if path.exists() or path.is_symlink():
                path.unlink()
        return {"status": "ok", "message": "AgentDock 私有运行环境已卸载，公共工作区、制品和共享 Termux 依赖已保留",
                "data": {"distro_removed": distro_removed, "workspace_path": preview["workspace_path"],
                         "artifact_path": preview["artifact_path"], "workspace_preserved": True,
                         "artifacts_preserved": True, "termux_packages_preserved": True}}

    def cleanup(self, preview: bool = True) -> dict:
        records = self.journal_records()
        now = self.clock()
        abandoned = [(path, record) for path, record in records
                     if record.get("kind") == "deployment" and record.get("status") == "requires_user_action"
                     and record.get("phase") in SAFE_ABANDONED_PHASES
                     and now - float(record.get("updated_at", 0)) >= ABANDONED_TRANSACTION_RETENTION_SECONDS]
        abandoned_ids = {path.parent.name for path, _ in abandoned}
        protected = {self.pointer("current"), self.pointer("previous")}
        fallback = read_json(self.root / "fallback.json", {})
        protected.add(fallback.get("version_directory", ""))
        backups = {fallback.get("backup", "")}
        for path, record in records:
            if record.get("status") not in TERMINAL and path.parent.name not in abandoned_ids:
                protected.update((record.get("source", ""), record.get("target", "")))
                backups.update((record.get("backup", ""), record.get("restore_backup", "")))
        versions_dir, backups_dir = self.root / "versions", self.root / "backups"
        eligible = []
        for path in list(versions_dir.iterdir()) if versions_dir.is_dir() else []:
            if path.is_dir() and not path.is_symlink() and not path.name.startswith(".") and path.name not in protected and (path / ".workbench-release.json").is_file():
                eligible.append(path)
        old_backups = [path for path in (list(backups_dir.iterdir()) if backups_dir.is_dir() else []) if path.is_dir() and not path.is_symlink() and path.name not in backups and (path / ".snapshot-complete.json").is_file()]
        check(len(eligible) + len(old_backups) + len(abandoned) <= 256, "capacity", "清理对象超过上限；未执行")
        result = {"versions": sorted(path.name for path in eligible), "backups": sorted(path.name for path in old_backups),
                  "abandoned_transactions": sorted(abandoned_ids), "lifecycle": self.automatic_cleanup(True), "preview": preview,
                  "preserved": ["current", "verified fallback", "unfinished maintenance", "quarantined data", "public workspace", "identity"]}
        if not preview:
            result["lifecycle"] = self.automatic_cleanup(False)
            for path in eligible:
                self._remove_managed_entry(path, versions_dir)
            for path in old_backups:
                self._remove_managed_entry(path, backups_dir)
            active = read_text(self.root / "active-operation")
            for path, _ in abandoned:
                stage = versions_dir / (".stage-" + path.parent.name)
                if stage.exists() or stage.is_symlink():
                    self._remove_managed_entry(stage, versions_dir)
                self._remove_managed_entry(path.parent, self.root / "transactions")
                if active == path.parent.name:
                    (self.root / "active-operation").unlink(missing_ok=True)
        return result

    def logs(self, payload: dict) -> dict:
        name = payload.get("log", "core")
        check(name in ("core", "previous"), "invalid_request", "日志类型无效")
        path = self.root / "logs" / ("core.log" if name == "core" else "core.previous.log")
        offset = payload.get("offset", 0)
        check(type(offset) is int and 0 <= offset <= 2**53, "invalid_request", "日志游标无效")
        query = str(payload.get("search", ""))[:256]
        if not path.exists():
            return {"status": "ok", "message": "尚无该类日志", "data": {"text": "", "offset": 0, "next_offset": 0, "has_more": False}}
        check(not path.is_symlink() and path.is_file(), "unsafe_path", "日志文件类型无效")
        size, output, consumed = path.stat().st_size, [], 0
        with path.open("rb") as stream:
            start = min(offset, size)
            if start:
                stream.seek(start - 1)
                if stream.read(1) != b"\n":
                    while stream.tell() < size and consumed < 65536:
                        chunk = stream.readline(8193)
                        consumed += len(chunk)
                        if chunk.endswith(b"\n"):
                            break
            else:
                stream.seek(0)
            while stream.tell() < size and consumed < 65536 and len(output) < 200:
                line = stream.readline(8193)
                consumed += len(line)
                if len(line) > 8192:
                    while line and not line.endswith(b"\n") and consumed < 65536:
                        line = stream.readline(8193)
                        consumed += len(line)
                    output.append("[超长日志行已省略]")
                    continue
                safe = redact(line.decode("utf-8", "replace"), 8192)
                if not query or query.casefold() in safe.casefold():
                    output.append(safe.rstrip("\r\n"))
                if sum(len(item.encode()) for item in output) > 14000:
                    break
            next_offset = stream.tell()
        text = "\n".join(output)
        return {"status": "ok", "message": "仅返回完整行的有界脱敏日志", "data": {"text": text, "offset": start, "next_offset": next_offset, "has_more": next_offset < size, "original_bytes": size}}

    def validate_payload(self, operation: str, payload: dict) -> None:
        allowed = {
            "probe": set(), "status": set(), "bootstrap": set(), "start": set(), "stop": set(), "restart": set(),
            "pair_local_core": {"key_id", "algorithm", "public_key_der"},
            "guardian_check": set(), "diagnostic_preview": set(), "cleanup_preview": set(), "export_diagnostics": set(),
            "workspace_migration_preview": set(), "uninstall_preview": set(),
            "logs": {"log", "offset", "search"}, "operation_query": {"target_operation_id"},
            "configure": {"node"}, "adopt": {"existing_root", "confirm_adopt"},
            "install": {"apk_version", "start_after_install"},
            "update": {"apk_version", "start_after_install"},
            "resume": {"target_operation_id", "confirm_start"},
            "cancel_operation": {"target_operation_id", "confirm_cancel"},
            "repair": {"confirm_reset"}, "rollback": {"confirm_data_restore", "start_after_install"},
            "cleanup": {"confirm_cleanup", "preview_digest"},
            "workspace_migrate": {"confirm_migration", "preview_digest"},
            "uninstall": {"confirm_uninstall", "preview_digest"},
            "path_probe": {"termux_path", "confirm_path"},
            "project_create": {"termux_path", "confirm_path", "name"},
        }
        check(operation in allowed and isinstance(payload, dict), "invalid_request", "不支持该部署操作")
        check(not set(payload) - (allowed[operation] | {"source"}), "invalid_request", "请求包含未声明字段")
        no_secrets(payload)

    def dispatch(self, operation: str, identity: str, payload: dict) -> dict:
        self.validate_payload(operation, payload)
        if operation in READ_ONLY:
            if operation == "operation_query":
                data = self.query(payload.get("target_operation_id", ""))
                data["desired_state"] = self.desired()
                return {"status": "ok", "message": "只读取原操作记录，未重放", "data": data}
            if operation == "logs":
                return self.logs(payload)
            if operation == "cleanup_preview":
                preview = self.cleanup(True)
                preview["preview_digest"] = hashlib.sha256(json_bytes(preview)).hexdigest()
                return {"status": "ok", "message": "仅预览可安全清理的受管对象", "data": preview}
            if operation == "workspace_migration_preview":
                preview = self.workspace_migration_preview()
                preview["preview_digest"] = hashlib.sha256(json_bytes(preview)).hexdigest()
                return {"status": "ok", "message": "仅预览旧私有工作区到公共目录的迁移", "data": preview}
            if operation == "uninstall_preview":
                preview = self.uninstall_preview()
                preview["preview_digest"] = hashlib.sha256(json_bytes(preview)).hexdigest()
                return {"status": "ok", "message": "仅预览私有运行环境卸载；公共工作区始终保留", "data": preview}
            if operation == "diagnostic_preview":
                return {"status": "ok", "message": "诊断只包含节点状态和有界脱敏日志，不含身份或工程", "data": {"categories": ["version", "desired state", "environment", "redacted Core log"], "excludes": ["credentials", "OAuth state", "project files"]}}
            return self.status()
        if operation == "uninstall":
            with self.lock():
                return self.uninstall(payload)
        if operation == "stop":
            self.root.mkdir(mode=0o700, parents=True, exist_ok=True)
            self.set_desired("stopped")
        with self.lock():
            self.initialize(automatic_cleanup=operation not in {"cleanup", "workspace_migrate"},
                            ensure_workspace=operation not in {"stop", "cleanup", "configure"})
            if operation in ("install", "update", "rollback"):
                if operation == "rollback":
                    check(payload.get("confirm_data_restore") is True, "confirmation_required", "回退会恢复一致性数据快照，必须明确确认")
                return self.execute_transaction(operation, identity, payload)
            if operation == "resume":
                target = payload.get("target_operation_id", "")
                original = read_json(self.journal_path(target))
                check(original is not None and original.get("kind") == "deployment", "not_found", "原部署操作不存在；未重放运行命令")
                if payload.get("confirm_start") is True:
                    self.set_desired("running")
                return self.execute_transaction(original["operation"], target, original["payload"])
            if operation == "cancel_operation":
                check(payload.get("confirm_cancel") is True, "confirmation_required", "取消事务需要明确确认")
                original = read_json(self.journal_path(payload.get("target_operation_id", "")))
                check(original is not None and original.get("kind") == "deployment", "not_found", "没有对应部署事务")
                if original["status"] in TERMINAL:
                    return {"status": "ok", "message": "原操作已有终态，未撤销已提交安装", "data": self.query(original["operation_id"])}
                if original["phase"] in {"created", "manifest", "download", "verify", "prepared"}:
                    return self.finish(original, "cancelled", "部署已取消；未切换版本或回写数据")
                return self.recover_previous(original)
            fingerprint = hashlib.sha256(json_bytes(payload)).hexdigest()
            existing = read_json(self.journal_path(identity))
            if existing:
                check(existing.get("operation") == operation and existing.get("fingerprint") == fingerprint, "operation_conflict", "原操作标识已被不同请求使用")
                if existing.get("status") in TERMINAL and isinstance(existing.get("result"), dict):
                    return existing["result"]
                return {"status": "requires_user_action", "message": "原运行操作没有终态；请读取节点状态，不自动重放", "data": self.query(identity)}
            self.reserve_journal()
            record = {"schema_version": 1, "kind": "runtime", "operation_id": identity, "operation": operation,
                      "fingerprint": fingerprint, "phase": "running", "status": "running", "created_at": self.clock(), "retryable": False}
            self.save(record)
            try:
                result = self.runtime_operation(operation, identity, payload)
                if operation != "pair_local_core":
                    result.setdefault("data", {}).setdefault("desired_state", self.desired())
                terminal_status = "succeeded" if result["status"] in {"ok", "healthy", "running", "stopped", "adopted"} else "failed"
                stored_result = result
                if operation == "pair_local_core" and terminal_status == "succeeded":
                    stored_result = {"status": "ok", "message": result.get("message", ""),
                                     "data": {"key_id": identity, "ciphertext_delivered": True}}
                self.save(record, "complete", status=terminal_status, message=result.get("message", ""), result=stored_result)
                return result
            except (BridgeError, OSError, subprocess.SubprocessError) as error:
                code = error.code if isinstance(error, BridgeError) else "io_failed"
                result = {"status": "requires_user_action" if code in {"unknown_process", "recovery_circuit_open", "recovery_backoff", "transaction_pending"} else "failed", "error_code": code, "message": redact(str(error)) if isinstance(error, BridgeError) else "运行 I/O 失败，未重放请求"}
                self.save(record, "complete", status="failed", message=result["message"], result=result)
                return result

    def runtime_operation(self, operation: str, identity: str, payload: dict) -> dict:
        if operation == "stop":
            self.backend.stop()
            return {"status": "stopped", "message": "停止意图已落盘，受管进程已停止；未修改守护开关"}
        if operation == "cleanup":
            check(payload.get("confirm_cleanup") is True, "confirmation_required", "清理前必须预览并确认")
            preview = self.cleanup(True)
            expected = hashlib.sha256(json_bytes(preview)).hexdigest()
            check(secrets.compare_digest(expected, str(payload.get("preview_digest", ""))), "preview_changed", "清理预览已变化，请重新确认；未执行删除")
            return {"status": "ok", "message": "已清理无恢复引用的旧版本、备份、废弃事务、日志和临时文件", "data": self.cleanup(False)}
        self.ensure_no_active()
        if operation == "bootstrap":
            value = self.backend.environment()
            if value["missing"]:
                return {"status": "requires_user_action", "message": "请在 Termux 执行导出的 bootstrap", "data": value}
            self.backend.validate_environment()
            token = self.root / "auth-token"
            if token.exists() or token.is_symlink():
                self.backend.bearer()
            else:
                atomic_bytes(token, secrets.token_hex(32).encode())
            return {"status": "ok", "message": "私有节点目录、环境与 APK 内置 Core 已准备"}
        if operation == "pair_local_core":
            key_id = payload.get("key_id", "")
            algorithm = payload.get("algorithm", "")
            encoded = payload.get("public_key_der", "")
            check(key_id == identity and algorithm == "RSA-OAEP-SHA256-MGF1-SHA1", "invalid_request", "配对公钥与操作标识不一致")
            check(isinstance(encoded, str) and 256 <= len(encoded) <= 8192, "invalid_request", "配对公钥长度无效")
            try:
                public_key = base64.b64decode(encoded, validate=True)
            except (ValueError, TypeError) as error:
                raise BridgeError("invalid_request", "配对公钥编码无效") from error
            check(256 <= len(public_key) <= 4096, "invalid_request", "配对公钥大小无效")
            directory = self.journal_path(identity).parent
            public_path = directory / "pairing-public.der"
            atomic_bytes(public_path, public_key)
            try:
                result = subprocess.run([
                    "openssl", "pkeyutl", "-encrypt", "-pubin", "-keyform", "DER", "-inkey", str(public_path),
                    "-pkeyopt", "rsa_padding_mode:oaep", "-pkeyopt", "rsa_oaep_md:sha256",
                    "-pkeyopt", "rsa_mgf1_md:sha1",
                ], input=self.backend.bearer().encode(), capture_output=True, timeout=10, close_fds=True)
            finally:
                public_path.unlink(missing_ok=True)
            check(result.returncode == 0 and 128 <= len(result.stdout) <= 512, "pairing_failed", "一次性公钥加密失败；未返回本机凭据")
            return {"status": "ok", "message": "本机凭据已按一次性公钥加密；明文未进入回执、参数或日志",
                    "data": {"key_id": identity, "algorithm": algorithm,
                             "sealed_value": base64.b64encode(result.stdout).decode("ascii")}}
        if operation == "configure":
            check(self.backend.owned_pid() is None, "node_running", "节点运行时不修改运行配置，请先停止")
            candidate = self.validate_config(payload.get("node", {}))
            public_roots(self.home, candidate, create=True)
            self.config = candidate
            atomic_json(self.root / "node.json", candidate)
            self.backend = self.backend_factory(self.root, candidate)
            return {"status": "ok", "message": "节点配置已保存；未启动 Core", "data": self.config}
        if operation in ("start", "restart"):
            self.set_desired("running")
            if operation == "restart":
                self.backend.stop()
            return self.recovery(manual_reset=True)
        if operation in ("repair", "guardian_check"):
            return self.recovery(manual_reset=operation == "repair" and payload.get("confirm_reset") is True)
        if operation == "adopt":
            check(payload.get("confirm_adopt") is True, "confirmation_required", "已有节点接管须明确确认")
            raw = payload.get("existing_root", "")
            check(isinstance(raw, str) and raw.startswith("/"), "invalid_request", "已有节点目录无效")
            candidate = inside(Path(raw), self.home, exists=True)
            check((candidate / "node.json").is_file(), "incompatible_layout", "该节点没有受管部署描述；仅提供只读诊断，不直接迁移")
            config = self.validate_config(read_json(candidate / "node.json"))
            backend = self.backend_factory(candidate, config)
            check(backend.owned_pid() is None or backend.health(), "unknown_process", "已有节点身份未验证；未接管")
            atomic_bytes(self.state / "adopted-root", str(candidate).encode())
            return {"status": "adopted", "message": "只建立受管目录引用，未替换程序、配置或身份",
                    "data": {"desired_state": read_text(candidate / "desired-state", "stopped")}}
        if operation == "workspace_migrate":
            return self.migrate_workspace(identity, payload)
        if operation == "export_diagnostics":
            out = self.root / "logs" / ("diagnostics-" + identity + ".json")
            value = {"schema_version": 1, "status": self.status(), "logs": self.logs({})["data"], "excludes": ["credentials", "OAuth", "projects"]}
            atomic_json(out, value)
            return {"status": "ok", "message": "诊断已保留在 Termux 私有目录", "data": {"relative_path": "logs/" + out.name, "bytes": out.stat().st_size}}
        if operation in ("path_probe", "project_create"):
            check(payload.get("confirm_path") is True, "confirmation_required", "Termux 路径须由用户明确授权")
            raw = payload.get("termux_path", "")
            check(isinstance(raw, str) and raw.startswith("/") and len(raw) <= 2048, "invalid_request", "路径无效")
            root = Path(raw).resolve(strict=True)
            allowed = root == self.home or self.home in root.parents or str(root).startswith("/storage/emulated/0/")
            check(allowed and root.is_dir(), "unsafe_path", "只允许明确授权的 Termux home 或共享存储目录")
            if operation == "project_create":
                name = payload.get("name", "")
                check(isinstance(name, str) and 0 < len(name) <= 128 and name not in (".", "..") and not any(c in name for c in '/\\\x00\n\r'), "invalid_request", "工程名称无效")
                root = inside(root / name, root)
                root.mkdir(mode=0o700, exist_ok=True)
                rules = root / "AGENTS.md"
                if not rules.exists():
                    descriptor = os.open(rules, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
                    with os.fdopen(descriptor, "w") as output:
                        output.write("# Project rules\n\nPreserve user files and verify changes before delivery.\n")
            marker = root / (".wb-path-" + identity)
            descriptor = os.open(marker, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
            try:
                with os.fdopen(descriptor, "wb") as output:
                    output.write(b"path-probe")
                check(marker.read_bytes() == b"path-probe", "path_unavailable", "路径回读失败")
            finally:
                marker.unlink(missing_ok=True)
            return {"status": "ok", "message": "Termux 路径读写已验证；Android SAF 权限仍独立验证", "data": {"termux_path": str(root), "readable": True, "writable": True, "android_uri_mapped": False}}
        raise BridgeError("invalid_request", "不支持该部署操作")


def main(argv: list[str]) -> int:
    if len(argv) == 2 and argv[0] == "--launch":
        launch_owned(Path(argv[1]))
        return 1
    envelope = {"schema_version": 1}
    try:
        check(len(argv) == 5, "invalid_request", "部署入口需要五个固定参数")
        operation, operation_id, request_id, nonce, payload_path = argv
        check(operation in OPERATIONS and IDENTIFIER.fullmatch(operation_id) is not None and IDENTIFIER.fullmatch(request_id) is not None and NONCE.fullmatch(nonce) is not None and payload_path == "-", "invalid_request", "部署请求标识或操作无效")
        envelope.update(operation=operation, operation_id=operation_id, request_id=request_id, nonce=nonce)
        def expired(_signal, _frame):
            raise BridgeError("invalid_request", "部署请求输入超时")
        signal.signal(signal.SIGALRM, expired)
        signal.alarm(5)
        try:
            data = sys.stdin.buffer.read(MAX_JSON + 1)
        finally:
            signal.alarm(0)
        check(len(data) <= MAX_JSON, "invalid_request", "部署请求超过 64 KiB")
        payload = json.loads(data, parse_constant=lambda _value: (_ for _ in ()).throw(ValueError("non-finite JSON")))
        check(isinstance(payload, dict) and type(payload.get("schema_version")) is int and all(payload.get(key) == value for key, value in envelope.items()), "invalid_request", "请求正文与部署标识不一致")
        for key in envelope:
            payload.pop(key)
        no_secrets(payload)
        home = Path(os.environ.get("AGENTDOCK_WORKBENCH_TEST_HOME", "")) if os.environ.get("AGENTDOCK_WORKBENCH_TEST_MODE") == "1" else Path.home()
        manager = Deployment(home)
        result = manager.dispatch(operation, operation_id, payload)
        success = result["status"] in {"ok", "healthy", "running", "stopped", "installed", "updated", "rolled_back", "adopted"}
        blocked = result["status"] in {"pending_manifest", "requires_user_action"}
        envelope.update(result, phase="complete" if success else "blocked" if blocked else "failed")
        sys.stdout.buffer.write(json_bytes(envelope))
        return 0 if (success or blocked) and not result.get("error_code") else 1
    except BridgeError as error:
        code = "requires_user_action" if error.code in {"unknown_process", "recovery_backoff", "recovery_circuit_open", "transaction_pending", "user_stopped"} else error.code
        envelope.update(status=code, phase="failed", message=redact(str(error)), error_code=error.code, retryable=error.retryable)
    except (ValueError, TypeError, UnicodeError, RecursionError):
        envelope.update(status="invalid_request", phase="failed", message="部署请求格式无效")
    except (OSError, subprocess.SubprocessError, tarfile.TarError):
        envelope.update(status="failed", phase="failed", message="部署 I/O 失败；请按原 operation ID 查询，未自动重放")
    sys.stdout.buffer.write(json_bytes(envelope))
    return 1

if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
