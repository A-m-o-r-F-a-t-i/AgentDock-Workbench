"""Synthetic deployment fault injection. No production node/Termux installation."""
import contextlib
import base64
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import unittest
from unittest.mock import patch

MODULE = Path(__file__).resolve().parents[3] / 'mobile/android/termux/agentdock_workbench.py'
spec = importlib.util.spec_from_file_location('wb_deployment', MODULE)
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)

class SimulatedCrash(BaseException):
    pass

class FakeBackend(m.NodeBackend):
    """Models process I/O only; filesystem, journals and extraction remain real."""
    def __init__(self, root, config, state):
        super().__init__(root, config)
        self.state = state
    def environment(self):
        return {'missing': [], 'architecture': 'synthetic-arm64', 'arm64': True}
    def validate_environment(self):
        pass
    def owned_pid(self):
        if self.state.get('unknown_pid'):
            raise m.BridgeError('unknown_process', 'fixture unknown process')
        return 424242 if self.state.get('running') else None
    def health(self, expected_version=''):
        return self.state.get('running', False) and self.state.get('version') not in self.state.get('fail_versions', set()) and (not expected_version or self.state.get('version') == expected_version)
    def binary_version(self, directory):
        return (directory / 'bin/agentdock').read_text().strip()
    def stop(self):
        self.owned_pid()
        self.state['stops'] = self.state.get('stops', 0) + 1
        self.state['running'] = False
    def start(self, version):
        self.bearer()
        m.check(m.read_text(self.root / 'desired-state') == 'running', 'user_stopped', 'stop respected')
        self.state.setdefault('starts', []).append(version)
        self.state.update(running=True, version=version)
        (self.root / 'data/schema.txt').write_text(version)
        if version in self.state.get('fail_versions', set()):
            raise m.BridgeError('health_failed', 'synthetic startup failure')
class DeploymentTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='wb07-deployment-')
        self.home = Path(self.tmp.name)
        self.previous_test_mode = os.environ.get('AGENTDOCK_WORKBENCH_TEST_MODE')
        os.environ['AGENTDOCK_WORKBENCH_TEST_MODE'] = '1'
        (self.home / 'storage/shared').mkdir(parents=True)
        self.state = {'running': False, 'starts': [], 'fail_versions': set()}
        self.now = [1000.0]
        self.manager = self.make()
        self.manager.initialize()
        self.workspace = self.manager.workspace_root()
        m.atomic_bytes(self.manager.root / 'auth-token', b'1' * 64)
        (self.workspace / 'project.txt').write_text('user project')
        (self.manager.root / 'data/config.txt').write_text('preserve settings')
    def tearDown(self):
        if self.previous_test_mode is None:
            os.environ.pop('AGENTDOCK_WORKBENCH_TEST_MODE', None)
        else:
            os.environ['AGENTDOCK_WORKBENCH_TEST_MODE'] = self.previous_test_mode
        self.tmp.cleanup()
    def make(self, fault=lambda _: None):
        return m.Deployment(self.home, lambda root, config: FakeBackend(root, config, self.state), lambda: self.now[0], fault)
    def release(self, version='1.1.8', build_marker=''):
        content = io.BytesIO()
        with tarfile.open(fileobj=content, mode='w:gz') as archive:
            data = version.encode()
            entry = tarfile.TarInfo('bin/agentdock'); entry.size = len(data); entry.mode = 0o755
            archive.addfile(entry, io.BytesIO(data))
            if build_marker:
                marker = build_marker.encode()
                entry = tarfile.TarInfo('build.txt'); entry.size = len(marker)
                archive.addfile(entry, io.BytesIO(marker))
        bundle = self.manager.state / 'bundle' / m.BUNDLED_CORE_NAME
        bundle.parent.mkdir(parents=True, exist_ok=True)
        bundle.write_bytes(content.getvalue())
        return {'apk_version': version, 'start_after_install': True}
    def seed(self, version='1.1.7', name='old'):
        path = self.manager.root / 'versions' / name
        (path / 'bin').mkdir(parents=True)
        (path / 'bin/agentdock').write_text(version); (path / 'bin/agentdock').chmod(0o755)
        metadata = {'version': version, 'platform': 'linux', 'arch': 'arm64', 'sha256': '2' * 64,
                    'binary_sha256': m.sha256(path / 'bin/agentdock'), 'data_schema': 'fixture-' + version}
        m.atomic_json(path / '.workbench-release.json', metadata)
        self.manager.set_pointer('current', name)
        self.manager.set_desired('running')
        self.state.update(running=True, version=version)
        (self.manager.root / 'data/schema.txt').write_text(version)
        return name
    def test_read_only_discovery_creates_nothing(self):
        with tempfile.TemporaryDirectory() as empty:
            manager = m.Deployment(Path(empty))
            manager.dispatch('probe', 'probe', {})
            manager.dispatch('status', 'status', {})
            self.assertEqual([], list(Path(empty).iterdir()))
    def test_stop_remains_available_when_shared_storage_is_unavailable(self):
        isolated = self.home / 'stop-without-shared'; isolated.mkdir()
        manager = m.Deployment(isolated, lambda root, config: FakeBackend(root, config, self.state), lambda: self.now[0])
        result = manager.dispatch('stop', 'stop_without_shared', {})
        self.assertEqual('stopped', result['status'])
        self.assertEqual('stopped', (manager.root / 'desired-state').read_text())
    def test_missing_bundled_core_changes_nothing(self):
        payload = self.release()
        (self.manager.state / 'bundle' / m.BUNDLED_CORE_NAME).unlink()
        result = self.manager.dispatch('install', 'install_missing', payload)
        self.assertEqual('requires_user_action', result['status'])
        self.assertEqual('bundled_core_missing', result['data']['failure_code'])
        self.assertEqual('', self.manager.pointer('current'))
    def test_install_idempotency_and_query(self):
        payload = self.release()
        first = self.manager.dispatch('install', 'install_once', payload)
        self.assertEqual('installed', first['status'])
        starts = len(self.state['starts'])
        repeat = self.manager.dispatch('install', 'install_once', payload)
        self.assertEqual('ok', repeat['status']); self.assertEqual(starts, len(self.state['starts']))
        query = self.manager.dispatch('operation_query', 'read_once', {'target_operation_id': 'install_once'})
        self.assertEqual('succeeded', query['data']['status'])
        self.assertEqual('user project', (self.workspace / 'project.txt').read_text())
        self.assertEqual('1' * 64, (self.manager.root / 'auth-token').read_text())
    def test_same_operation_different_payload_rejected(self):
        payload = self.release()
        self.manager.dispatch('install', 'bound_op', payload)
        with self.assertRaises(m.BridgeError):
            self.manager.dispatch('install', 'bound_op', dict(payload, apk_version='2.0.0'))
    def test_missing_binary_never_stops_existing_core(self):
        self.seed(); payload = self.release()
        bundle = self.manager.state / 'bundle' / m.BUNDLED_CORE_NAME
        with tarfile.open(bundle, 'w:gz') as archive:
            marker = b'no executable'
            entry = tarfile.TarInfo('build.txt'); entry.size = len(marker)
            archive.addfile(entry, io.BytesIO(marker))
        result = self.manager.dispatch('update', 'missing_binary', payload)
        self.assertEqual('requires_user_action', result['status'])
        self.assertEqual('verification_failed', result['data']['failure_code'])
        self.assertTrue(self.state['running']); self.assertEqual('old', self.manager.pointer('current'))

    def test_symlinked_bundled_core_never_switches_or_stops(self):
        self.seed(); payload = self.release()
        bundle = self.manager.state / 'bundle' / m.BUNDLED_CORE_NAME
        external = self.home / 'external-core.tar.gz'; external.write_bytes(bundle.read_bytes())
        bundle.unlink(); bundle.symlink_to(external)
        result = self.manager.dispatch('update', 'linked_bundle', payload)
        self.assertEqual('bundled_core_missing', result['data']['failure_code'])
        self.assertEqual(0, self.state.get('stops', 0)); self.assertEqual('old', self.manager.pointer('current'))
    def test_apk_core_version_mismatch_blocks_before_asset(self):
        self.seed(); payload = self.release(); payload['apk_version'] = '9.9.9'
        result = self.manager.dispatch('update', 'bad_version', payload)
        self.assertEqual('version_mismatch', result['data']['failure_code'])
        self.assertEqual(0, self.state.get('stops', 0))

    def test_changed_bundle_requires_a_new_confirmation(self):
        self.seed(); payload = self.release(); fired = [False]
        def fault(phase):
            if phase == 'verify' and not fired[0]:
                fired[0] = True
                raise SimulatedCrash()
        with self.assertRaises(SimulatedCrash):
            self.make(fault).dispatch('update', 'changing', payload)
        self.release('1.1.9')
        result = self.make().dispatch('resume', 'resume_changing', {'target_operation_id': 'changing'})
        self.assertEqual('bundle_changed', result['data']['failure_code'])
        self.assertEqual('old', self.manager.pointer('current'))
    def test_health_failure_restores_schema_and_keeps_new_data(self):
        self.seed(); self.state['fail_versions'].add('1.1.8')
        result = self.manager.dispatch('update', 'bad_health', self.release())
        self.assertEqual('failed', result['status']); self.assertEqual('old', self.manager.pointer('current'))
        self.assertEqual('1.1.7', (self.manager.root / 'data/schema.txt').read_text())
        self.assertEqual('1.1.8', (self.manager.root / 'quarantine/bad_health-failed-new-data/schema.txt').read_text())
        self.assertTrue(self.state['running'])
    def test_stop_intent_prevents_resume_start(self):
        self.seed()
        def fault(phase):
            if phase == 'start':
                self.manager.set_desired('stopped')
        result = self.make(fault).dispatch('update', 'stopped', self.release())
        self.assertEqual('requires_user_action', result['status'])
        self.assertFalse(self.state['running']); self.assertEqual('stopped', self.manager.desired())
        again = self.make().dispatch('resume', 'resume_stopped', {'target_operation_id': 'stopped'})
        self.assertEqual('requires_user_action', again['status']); self.assertFalse(self.state['running'])
    def test_cleanup_retains_only_current_and_verified_fallback(self):
        self.seed('1.1.6', 'obsolete')
        self.seed()
        result = self.manager.dispatch('update', 'retention', self.release())
        self.assertEqual('updated', result['status'])
        names = {p.name for p in (self.manager.root / 'versions').iterdir() if not p.name.startswith('.')}
        self.assertEqual({'old', self.manager.pointer('current')}, names)
        self.assertEqual('old', self.manager.pointer('previous'))
        self.assertEqual('preserve settings', (self.manager.root / 'backups/retention/data/config.txt').read_text())
    def test_explicit_rollback_restores_consistent_old_data(self):
        self.seed()
        self.manager.dispatch('update', 'upgrade', self.release())
        (self.manager.root / 'data/config.txt').write_text('post-upgrade configuration')
        result = self.manager.dispatch('rollback', 'rollback_confirmed', {'confirm_data_restore': True, 'start_after_install': True})
        self.assertEqual('rolled_back', result['status']); self.assertEqual('old', self.manager.pointer('current'))
        self.assertEqual('preserve settings', (self.manager.root / 'data/config.txt').read_text())
        self.assertEqual('post-upgrade configuration', (self.manager.root / 'quarantine/rollback_confirmed-pre-rollback-data/config.txt').read_text())
    def test_tampered_backup_never_restores(self):
        self.seed(); self.manager.dispatch('update', 'upgrade', self.release())
        (self.manager.root / 'backups/upgrade/data/config.txt').write_text('corrupted snapshot')
        result = self.manager.dispatch('rollback', 'rollback_corrupt', {'confirm_data_restore': True, 'start_after_install': True})
        self.assertIn(result['status'], {'failed', 'requires_user_action'})
        self.assertNotEqual('corrupted snapshot', (self.manager.root / 'data/config.txt').read_text())
    def test_unfinished_transaction_protects_refs_and_blocks_another(self):
        self.seed(); payload = self.release()
        def fault(phase):
            if phase == 'verify':
                raise SimulatedCrash()
        with self.assertRaises(SimulatedCrash):
            self.make(fault).dispatch('update', 'pending', payload)
        with self.assertRaises(m.BridgeError):
            self.manager.dispatch('update', 'different', payload)
        self.manager.cleanup(False)
        self.assertTrue((self.manager.root / 'versions/old').is_dir())
    def test_cancel_before_switch_does_not_touch_running_source(self):
        self.seed(); payload = self.release()
        def fault(phase):
            if phase == 'verify':
                raise SimulatedCrash()
        with self.assertRaises(SimulatedCrash):
            self.make(fault).dispatch('update', 'cancel_me', payload)
        result = self.manager.dispatch('cancel_operation', 'cancel', {'target_operation_id': 'cancel_me', 'confirm_cancel': True})
        self.assertEqual('ok', result['status']); self.assertEqual('cancelled', result['data']['status'])
        self.assertTrue(self.state['running']); self.assertEqual('old', self.manager.pointer('current'))
    def test_runtime_restart_receipt_prevents_replay(self):
        self.seed()
        self.manager.dispatch('restart', 'restart_once', {})
        count = len(self.state['starts'])
        self.manager.dispatch('restart', 'restart_once', {})
        self.assertEqual(count, len(self.state['starts']))
        self.assertEqual('succeeded', self.manager.query('restart_once')['status'])
    def test_local_pairing_encrypts_bearer_and_does_not_persist_ciphertext(self):
        private_key = self.home / 'pairing-private.pem'
        public_key = self.home / 'pairing-public.der'
        subprocess.run(['openssl', 'genpkey', '-algorithm', 'RSA', '-pkeyopt', 'rsa_keygen_bits:2048', '-out', str(private_key)], check=True, capture_output=True)
        subprocess.run(['openssl', 'pkey', '-in', str(private_key), '-pubout', '-outform', 'DER', '-out', str(public_key)], check=True, capture_output=True)
        identity = 'pair_once'
        result = self.manager.dispatch('pair_local_core', identity, {
            'key_id': identity,
            'algorithm': 'RSA-OAEP-SHA256-MGF1-SHA1',
            'public_key_der': base64.b64encode(public_key.read_bytes()).decode('ascii'),
        })
        self.assertEqual('ok', result['status'])
        sealed = base64.b64decode(result['data']['sealed_value'])
        decrypted = subprocess.run([
            'openssl', 'pkeyutl', '-decrypt', '-inkey', str(private_key),
            '-pkeyopt', 'rsa_padding_mode:oaep', '-pkeyopt', 'rsa_oaep_md:sha256',
            '-pkeyopt', 'rsa_mgf1_md:sha1',
        ], input=sealed, check=True, capture_output=True).stdout
        self.assertEqual(b'1' * 64, decrypted)
        stored = m.read_json(self.manager.journal_path(identity))['result']
        self.assertNotIn('sealed_value', json.dumps(stored))
        self.assertTrue(stored['data']['ciphertext_delivered'])
    def test_unknown_runtime_completion_is_not_replayed(self):
        self.seed()
        def fault(phase):
            if phase == 'running': raise SimulatedCrash()
        with self.assertRaises(SimulatedCrash): self.make(fault).dispatch('restart', 'unknown', {})
        result = self.make().dispatch('restart', 'unknown', {})
        self.assertEqual('requires_user_action', result['status']); self.assertEqual([], self.state['starts'])
    def test_recovery_backoff_and_circuit_persist(self):
        self.seed(); self.state.update(running=False, fail_versions={'1.1.7'})
        for index in range(3):
            result = self.make().dispatch('guardian_check', 'repair_' + str(index), {})
            self.assertEqual('failed', result['status'])
            stored = m.read_json(self.manager.root / 'recovery.json')
            self.assertEqual(index + 1, stored['failures'])
            self.now[0] = stored['next_attempt_at']
        calls = len(self.state['starts'])
        result = self.make().dispatch('guardian_check', 'after_circuit', {})
        self.assertEqual('requires_user_action', result['status']); self.assertEqual(calls, len(self.state['starts']))
        self.state['fail_versions'] = set()
        reset = self.make().dispatch('repair', 'manual_reset', {'confirm_reset': True})
        self.assertEqual('healthy', reset['status'])
        self.assertEqual(0, m.read_json(self.manager.root / 'recovery.json')['failures'])
    def test_guardian_does_not_restart_explicitly_stopped_node(self):
        self.seed(); self.manager.set_desired('stopped'); self.state['running'] = False
        result = self.manager.dispatch('guardian_check', 'intent_stop', {})
        self.assertEqual('stopped', result['status']); self.assertEqual([], self.state['starts'])
    def test_unknown_pid_is_never_stopped(self):
        self.seed(); self.state['unknown_pid'] = True
        result = self.manager.dispatch('stop', 'unknown_pid', {})
        self.assertEqual('requires_user_action', result['status']); self.assertEqual('stopped', self.manager.desired())
        self.assertEqual(0, self.state.get('stops', 0))
    def test_configuration_is_validated_and_requires_stopped_node(self):
        result = self.manager.dispatch('configure', 'cfg', {'node': {'port': 9876, 'distro': 'debian', 'node_name': 'phone'}})
        self.assertEqual('ok', result['status']); self.assertEqual(9876, self.make().config['port'])
        bad = self.manager.dispatch('configure', 'bad_cfg', {'node': {'port': 22}})
        self.assertEqual('failed', bad['status']); self.assertEqual(9876, self.make().config['port'])
    def test_secret_fields_and_unknown_arguments_rejected(self):
        for payload in ({'credentials': {'value': 'bad'}}, {'extra': True}):
            with self.assertRaises(m.BridgeError): self.manager.dispatch('start', 'bad_field', payload)
    def test_no_credentials_or_private_database_in_diagnostic(self):
        (self.manager.root / 'logs/core.log').write_text('Bearer secret-value\npassword=private-password\n' + 'a' * 64 + '\npublic information\n')
        result = self.manager.dispatch('export_diagnostics', 'diagnostic', {})
        value = (self.manager.root / result['data']['relative_path']).read_text()
        for private in ('secret-value', 'private-password', 'a' * 64, 'preserve settings', '1' * 64):
            self.assertNotIn(private, value)
    def test_log_cursor_does_not_expose_partial_secret(self):
        line = 'prefix Bearer ' + 's' * 12000 + '\nnext line\n'
        (self.manager.root / 'logs/core.log').write_text(line)
        result = self.manager.dispatch('logs', 'logs', {'offset': 100})
        self.assertNotIn('sss', result['data']['text']); self.assertIn('next line', result['data']['text'])
    def test_proot_hardlink_snapshot_and_rollback_preserve_secret(self):
        self.seed()
        prefix = self.home / 'termux-prefix'
        with patch.dict(os.environ, {'PREFIX': str(prefix)}):
            store = self.manager.proot_link_store(); store.mkdir(parents=True)
            backing = store / '.l2s..public-url-secret-0001'
            backing.write_bytes(b'fixture identity'); backing.chmod(0o600)
            source = self.manager.root / 'data/secrets/public-url-secret'
            source.parent.mkdir(); source.symlink_to(backing)
            result = self.manager.dispatch('update', 'proot_upgrade', self.release())
            self.assertEqual('updated', result['status'])
            copied = self.manager.root / 'backups/proot_upgrade/data/secrets/public-url-secret'
            self.assertFalse(copied.is_symlink())
            self.assertEqual(b'fixture identity', copied.read_bytes())
            self.assertEqual(0o600, copied.stat().st_mode & 0o777)
            self.assertTrue(source.is_symlink())
            backing.write_bytes(b'changed after snapshot')
            m.verify_snapshot(self.manager.root / 'backups/proot_upgrade')
            result = self.manager.dispatch('rollback', 'proot_rollback', {
                'confirm_data_restore': True, 'start_after_install': True,
            })
            self.assertEqual('rolled_back', result['status'])
            self.assertFalse(source.is_symlink())
            self.assertEqual(b'fixture identity', source.read_bytes())

    def test_unsupported_backup_link_is_rejected_before_core_stop(self):
        self.seed()
        outside = self.home / 'outside'; outside.write_bytes(b'unrelated')
        (self.manager.root / 'data/escape').symlink_to(outside)
        result = self.manager.dispatch('update', 'unsafe_backup', self.release())
        self.assertEqual('unsafe_path', result['data']['failure_code'])
        self.assertEqual(0, self.state.get('stops', 0))
        self.assertTrue(self.state['running'])
        self.assertEqual('old', self.manager.pointer('current'))

    def test_proot_backing_symlink_and_other_container_are_rejected(self):
        store = self.home / '.l2s'; store.mkdir()
        outside = self.home / '.l2s.outside'; outside.write_bytes(b'unrelated')
        backing = store / '.l2s.link'; backing.symlink_to(outside)
        source = self.manager.root / 'data/escape'
        for target in (backing, outside):
            source.symlink_to(target)
            with self.assertRaises(m.BridgeError):
                m.snapshot_tree(self.manager.root / 'data', self.manager.root / 'backups/rejected', proot_l2s=store)
            source.unlink()
        self.assertFalse((self.manager.root / 'backups/rejected').exists())

    def test_proot_backing_bytes_count_toward_backup_limit(self):
        store = self.home / '.l2s'; store.mkdir()
        backing = store / '.l2s.large'; backing.write_bytes(b'12345')
        data = self.home / 'bounded-data'; data.mkdir(); (data / 'alias').symlink_to(backing)
        with patch.object(m, 'MAX_BACKUP', 4), self.assertRaises(m.BridgeError):
            m.snapshot_tree(data, self.home / 'bounded-backup', proot_l2s=store)

    def test_backup_link_escape_rejected(self):
        (self.manager.root / 'data/escape').symlink_to('../../outside')
        with self.assertRaises(m.BridgeError): m.snapshot_tree(self.manager.root / 'data', self.manager.root / 'backups/bad')
    def test_cleanup_requires_exact_preview(self):
        result = self.manager.dispatch('cleanup', 'clean_bad', {'confirm_cleanup': True, 'preview_digest': 'wrong'})
        self.assertEqual('failed', result['status'])
        preview = self.manager.dispatch('cleanup_preview', 'preview', {})
        result = self.manager.dispatch('cleanup', 'clean_good', {'confirm_cleanup': True, 'preview_digest': preview['data']['preview_digest']})
        self.assertEqual('ok', result['status'])

    def test_default_workspace_is_public_and_private_state_has_no_workspace(self):
        self.assertEqual(self.home / 'storage/shared/Documents/AgentDock/Workspaces', self.workspace)
        self.assertTrue(self.workspace.is_dir())
        self.assertFalse((self.manager.root / 'workspace').exists())
        command = m.core_launch_command(self.manager.config)
        self.assertIn('AGENTDOCK_DEFAULT_DIR=/workspace;', command)
        self.assertIn('AGENTDOCK_LEGACY_DEFAULT_DIR=/opt/agentdock-workbench/workspace;', command)
        self.assertIn('AGENTDOCK_ARTIFACT_ROOT=/artifacts;', command)
        self.assertIn('AGENTDOCK_SCRATCH_ROOT=/opt/agentdock-workbench/workspace-runtime/scratch;', command)
        self.assertIn('AGENTDOCK_CACHE_ROOT=/opt/agentdock-workbench/workspace-runtime/cache;', command)
        self.assertNotIn('AGENTDOCK_DEFAULT_DIR=/opt/agentdock-workbench/workspace;', command)

    def test_config_rejects_private_workspace_and_accepts_shared_workspace(self):
        rejected = self.manager.dispatch('configure', 'private_workspace', {
            'node': {'workspace_root': str(self.home / 'private-workspace')},
        })
        self.assertEqual('failed', rejected['status'])
        artifacts = self.home / 'storage/shared/Documents/AgentDock/Artifacts'
        overlapping = self.manager.dispatch('configure', 'overlapping_workspace', {
            'node': {'workspace_root': str(artifacts)},
        })
        self.assertEqual('failed', overlapping['status'])
        custom = self.home / 'storage/shared/Documents/AgentDock/Custom'
        accepted = self.manager.dispatch('configure', 'public_workspace', {
            'node': {'workspace_root': str(custom)},
        })
        self.assertEqual('ok', accepted['status'])
        self.assertEqual(custom.resolve(), self.make().workspace_root())
        self.assertTrue(custom.is_dir())

    def test_configure_can_repair_an_inaccessible_current_workspace(self):
        self.manager.config['workspace_root'] = str(self.home / 'private-invalid')
        replacement = self.home / 'storage/shared/Documents/AgentDock/Repaired'
        result = self.manager.dispatch('configure', 'repair_workspace_config', {
            'node': {'workspace_root': str(replacement)},
        })
        self.assertEqual('ok', result['status'])
        self.assertEqual(replacement.resolve(), self.manager.workspace_root())

    def test_legacy_private_workspace_requires_preview_and_verified_migration(self):
        (self.workspace / 'project.txt').unlink()
        legacy = self.manager.legacy_workspace(); legacy.mkdir()
        (legacy / 'source').mkdir(); (legacy / 'source/main.txt').write_text('preserved project')
        preview = self.manager.dispatch('workspace_migration_preview', 'migration_preview', {})
        self.assertTrue(preview['data']['legacy_present'])
        rejected = self.manager.dispatch('workspace_migrate', 'migration_wrong', {
            'confirm_migration': True, 'preview_digest': 'wrong',
        })
        self.assertEqual('failed', rejected['status'])
        result = self.manager.dispatch('workspace_migrate', 'migration_confirmed', {
            'confirm_migration': True, 'preview_digest': preview['data']['preview_digest'],
        })
        self.assertEqual('ok', result['status'])
        self.assertEqual('preserved project', (self.workspace / 'source/main.txt').read_text())
        self.assertFalse(legacy.exists())

    def test_managed_legacy_workspace_separates_artifacts_and_discards_transient_data(self):
        (self.workspace / 'project.txt').unlink()
        identity = 'wsp_' + 'a' * 16
        legacy = self.manager.legacy_workspace(); legacy.mkdir()
        (legacy / 'source').mkdir(); (legacy / 'source/main.txt').write_text('project source')
        (legacy / 'artifacts' / identity).mkdir(parents=True)
        (legacy / 'artifacts' / identity / 'firmware.bin').write_bytes(b'firmware')
        (legacy / 'scratch' / identity).mkdir(parents=True)
        (legacy / 'scratch' / identity / 'temporary.log').write_text('temporary')
        (legacy / 'cache').mkdir(); (legacy / 'cache/index').write_text('cache')
        m.atomic_json(self.manager.root / 'data/workspaces.json', {
            'schema_version': 1,
            'default_workspace_id': identity,
            'workspaces': [{
                'workspace_id': identity, 'runtime': 'unix', 'root': '/opt/agentdock-workbench/workspace',
                'artifact_root': f'/opt/agentdock-workbench/workspace/artifacts/{identity}',
                'scratch_root': f'/opt/agentdock-workbench/workspace/scratch/{identity}',
                'cache_root': '/opt/agentdock-workbench/workspace/cache',
            }],
        })
        preview = self.manager.dispatch('workspace_migration_preview', 'managed_migration_preview', {})
        self.assertTrue(preview['data']['managed_layout'])
        self.assertTrue(preview['data']['artifact_present'])
        self.assertGreater(preview['data']['discarded_transient_bytes'], 0)
        result = self.manager.dispatch('workspace_migrate', 'managed_migration', {
            'confirm_migration': True, 'preview_digest': preview['data']['preview_digest'],
        })
        self.assertEqual('ok', result['status'])
        self.assertEqual('project source', (self.workspace / 'source/main.txt').read_text())
        self.assertFalse((self.workspace / 'scratch').exists())
        self.assertFalse((self.workspace / 'cache').exists())
        self.assertFalse((self.workspace / 'artifacts').exists())
        artifact_target = self.home / 'storage/shared/Documents/AgentDock/Artifacts' / identity
        self.assertEqual(b'firmware', (artifact_target / 'firmware.bin').read_bytes())
        self.assertFalse(legacy.exists())

    def test_automatic_cleanup_removes_old_private_files_and_rotates_log(self):
        self.now[0] = m.TERMINAL_JOURNAL_RETENTION_SECONDS + 1000
        transaction = self.manager.root / 'transactions/terminal-old'; transaction.mkdir()
        m.atomic_json(transaction / 'journal.json', {'status': 'succeeded', 'updated_at': 0})
        diagnostic = self.manager.root / 'logs/diagnostics-old.json'; diagnostic.write_text('{}')
        temporary = self.manager.root / 'tmp/stale'; temporary.mkdir(); (temporary / 'part').write_text('x')
        scratch = self.manager.root / 'workspace-runtime/scratch/old-task'; scratch.mkdir(); (scratch / 'probe.log').write_text('x')
        cache = self.manager.root / 'workspace-runtime/cache/old-entry'; cache.write_text('x')
        old = time.time() - max(m.DIAGNOSTIC_RETENTION_SECONDS, m.TEMP_RETENTION_SECONDS) - 10
        for path in (diagnostic, temporary / 'part', temporary, scratch / 'probe.log', scratch, cache):
            os.utime(path, (old, old))
        log = self.manager.root / 'logs/core.log'; log.write_bytes(b'a' * 128)
        with patch.object(m, 'CORE_LOG_LIMIT', 64):
            result = self.manager.automatic_cleanup(False)
        self.assertFalse(transaction.exists()); self.assertFalse(diagnostic.exists()); self.assertFalse(temporary.exists())
        self.assertFalse(scratch.exists()); self.assertFalse(cache.exists())
        self.assertEqual(b'', log.read_bytes())
        self.assertEqual(b'a' * 64, (self.manager.root / 'logs/core.previous.log').read_bytes())
        self.assertTrue(result['log_rotation']['rotated'])

    def test_automatic_cleanup_batches_excess_runtime_entries_without_blocking_startup(self):
        parent = self.manager.root / 'workspace-runtime/scratch'
        for index in range(257):
            (parent / f'task-{index:03d}').mkdir()
        result = self.manager.automatic_cleanup(True)
        self.assertEqual(1, result['workspace_runtime_skipped'])
        self.assertEqual([], result['workspace_runtime'])

    def test_cleanup_reclaims_only_old_pre_switch_failed_install(self):
        self.now[0] = m.ABANDONED_TRANSACTION_RETENTION_SECONDS + 1000
        abandoned = self.manager.root / 'transactions/abandoned'; abandoned.mkdir()
        m.atomic_json(abandoned / 'journal.json', {'kind': 'deployment', 'operation_id': 'abandoned',
            'operation': 'update', 'phase': 'download', 'status': 'requires_user_action', 'updated_at': 0})
        (abandoned / 'core.tar.gz.partial').write_bytes(b'partial')
        preview = self.manager.dispatch('cleanup_preview', 'abandoned_preview', {})
        self.assertIn('abandoned', preview['data']['abandoned_transactions'])
        result = self.manager.dispatch('cleanup', 'abandoned_cleanup', {
            'confirm_cleanup': True, 'preview_digest': preview['data']['preview_digest'],
        })
        self.assertEqual('ok', result['status'])
        self.assertFalse(abandoned.exists())

    def test_full_uninstall_removes_private_runtime_and_preserves_public_workspace(self):
        tasker = self.home / '.termux/tasker'; tasker.mkdir(parents=True)
        for name in ('agentdock-workbench', 'agentdock_workbench.py', 'agentdock_host_executor.py'):
            (tasker / name).write_text('managed')
        (self.manager.state / 'managed-distro-debian').write_text('')
        preview = self.manager.dispatch('uninstall_preview', 'uninstall_preview', {})
        self.assertTrue(preview['data']['workspace_preserved'])
        self.assertTrue(preview['data']['artifacts_preserved'])
        result = self.manager.dispatch('uninstall', 'uninstall_confirmed', {
            'confirm_uninstall': True, 'preview_digest': preview['data']['preview_digest'],
        })
        self.assertEqual('ok', result['status'])
        self.assertTrue(result['data']['distro_removed'])
        self.assertFalse(self.manager.state.exists())
        self.assertTrue((self.workspace / 'project.txt').is_file())
        self.assertEqual(self.home / 'storage/shared/Documents/AgentDock/Artifacts', Path(result['data']['artifact_path']))
        self.assertEqual([], [path.name for path in tasker.iterdir()])

    def test_identical_artifact_with_new_id_preserves_distinct_fallback(self):
        self.seed(); payload = self.release()
        self.manager.dispatch('update', 'upgrade', payload)
        fallback = m.read_json(self.manager.root / 'fallback.json')
        starts = len(self.state['starts']); stops = self.state['stops']
        result = self.make().dispatch('update', 'same_artifact', payload)
        self.assertEqual('updated', result['status'])
        self.assertEqual(fallback, m.read_json(self.manager.root / 'fallback.json'))
        self.assertNotEqual(self.manager.pointer('current'), self.manager.pointer('previous'))
        self.assertTrue((self.manager.root / 'versions/old').is_dir())
        self.assertEqual(starts, len(self.state['starts'])); self.assertEqual(stops, self.state['stops'])
        self.assertFalse((self.manager.root / 'backups/same_artifact').exists())
        self.make().dispatch('resume', 'repeat_same', {'target_operation_id': 'same_artifact'})
        self.assertEqual(fallback, m.read_json(self.manager.root / 'fallback.json'))

    def test_same_version_different_artifact_is_a_real_transition(self):
        self.seed(); self.manager.dispatch('update', 'build_one', self.release())
        first = self.manager.pointer('current')
        self.manager.dispatch('update', 'build_two', self.release(build_marker='different build'))
        self.assertNotEqual(first, self.manager.pointer('current'))
        self.assertEqual(first, self.manager.pointer('previous'))

    def test_identical_artifact_respects_stop_intent(self):
        self.seed(); payload = self.release()
        self.manager.dispatch('update', 'upgrade', payload)
        self.manager.dispatch('stop', 'stop', {})
        starts = len(self.state['starts'])
        self.manager.dispatch('install', 'same_stopped', dict(payload, start_after_install=False))
        self.assertEqual('stopped', self.manager.desired())
        self.assertEqual(starts, len(self.state['starts']))
        self.assertEqual('old', self.manager.pointer('previous'))

    def test_single_port_authority_ignores_legacy_conflicting_file(self):
        (self.manager.root / 'port').write_text('8765')
        result = self.manager.dispatch('configure', 'new_port', {'node': {'port': 9876}})
        self.assertEqual('ok', result['status'])
        manager = self.make(); manager.initialize()
        command = m.core_launch_command(manager.config)
        self.assertEqual(9876, manager.backend.config['port'])
        self.assertIn('AGENTDOCK_PORT=9876;', command)
        self.assertNotIn('cat /opt/agentdock-workbench/port', command)
        self.assertEqual('8765', (manager.root / 'port').read_text())

    def test_rejected_config_write_keeps_persisted_port_authority(self):
        write = m.atomic_json
        def reject(path, value):
            if path == self.manager.root / 'node.json':
                raise OSError('injected config write error')
            return write(path, value)
        with patch.object(m, 'atomic_json', reject):
            result = self.manager.dispatch('configure', 'bad_write', {'node': {'port': 9876}})
        self.assertEqual('failed', result['status'])
        manager = self.make()
        self.assertEqual(8765, manager.config['port'])
        self.assertIn('AGENTDOCK_PORT=8765;', m.core_launch_command(manager.config))

    def test_restore_does_not_replace_unknown_destination(self):
        self.seed(); self.state['fail_versions'].add('1.1.8')
        def fault(phase):
            if phase == 'failed-new-data-quarantined': raise SimulatedCrash()
        with self.assertRaises(SimulatedCrash):
            self.make(fault).dispatch('update', 'interrupted', self.release())
        destination = self.manager.root / 'data'; destination.mkdir()
        (destination / 'unknown.txt').write_text('do not overwrite')
        result = self.make().dispatch('resume', 'resume_unknown', {'target_operation_id': 'interrupted'})
        self.assertEqual('requires_user_action', result['status'])
        self.assertEqual('do not overwrite', (destination / 'unknown.txt').read_text())
        self.assertTrue((self.manager.root / 'backups/interrupted/.snapshot-complete.json').is_file())

    def _bootstrap(self, names, fail_list=False, repeat=False):
        mock = self.home / 'mock-bin'; mock.mkdir()
        installed = self.home / 'containers'; installed.write_text(names)
        calls = self.home / 'calls'
        for name in ('pkg', 'dpkg', 'apt-get', 'termux-reload-settings'):
            path = mock / name; path.write_text('#!/bin/sh\nexit 0\n'); path.chmod(0o700)
        path = mock / 'dpkg-query'
        path.write_text("#!/bin/sh\nprintf 'install ok installed\\n'\n"); path.chmod(0o700)
        path = mock / 'proot-distro'
        path.write_text('''#!/bin/sh
printf '%s\\n' "$*" >> "$AUDIT_CALLS"
case "$1" in
  list) [ "$2" = --quiet ] || exit 8
        [ "$AUDIT_FAIL_LIST" = 0 ] || exit 9
        cat "$AUDIT_INSTALLED" ;;
  install) grep -Fxq "$2" "$AUDIT_INSTALLED" && exit 1
           printf '%s\\n' "$2" >> "$AUDIT_INSTALLED" ;;
  login) exit 0 ;;
  *) exit 7 ;;
esac
'''); path.chmod(0o700)
        env = dict(os.environ, HOME=str(self.home), PATH=str(mock)+os.pathsep+os.environ['PATH'],
                   AUDIT_CALLS=str(calls), AUDIT_INSTALLED=str(installed), AUDIT_FAIL_LIST='1' if fail_list else '0')
        bundle = self.home / 'bootstrap-bundle'; bundle.mkdir()
        for name in ('agentdock-workbench-bootstrap.sh', 'agentdock-workbench', 'agentdock_workbench.py', 'agentdock_host_executor.py'):
            shutil.copy2(MODULE.with_name(name), bundle / name)
        (bundle / m.BUNDLED_CORE_NAME).write_bytes(b'fixture bundled core')
        script = bundle / 'agentdock-workbench-bootstrap.sh'
        result = subprocess.run(['/bin/sh', str(script)], env=env, capture_output=True, text=True, timeout=15)
        if repeat:
            self.assertEqual(0, result.returncode, result.stderr)
            result = subprocess.run(['/bin/sh', str(script)], env=env, capture_output=True, text=True, timeout=15)
        return result, calls.read_text()

    def test_bootstrap_installed_debian_is_not_reinstalled(self):
        result, calls = self._bootstrap('debian\n')
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertNotIn('install debian', calls); self.assertIn('login debian', calls)

    def test_bootstrap_installs_once_and_can_repeat(self):
        result, calls = self._bootstrap('', repeat=True)
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual(1, calls.count('install debian'))

    def test_bootstrap_other_container_is_not_debian(self):
        result, calls = self._bootstrap('ubuntu\ndebian-test\n')
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertIn('install debian\n', calls)

    def test_bootstrap_failed_list_does_not_install(self):
        result, calls = self._bootstrap('debian\n', fail_list=True)
        self.assertNotEqual(0, result.returncode)
        self.assertNotIn('install debian', calls); self.assertNotIn('login debian', calls)


def restore_phase_case(explicit, phase):
    def test(self):
        self.seed()
        payload = self.release()
        identity = 'restore_window'
        if explicit:
            self.manager.dispatch('update', 'before_rollback', payload)
            operation = 'rollback'; payload = {'confirm_data_restore': True, 'start_after_install': True}
            label = 'pre-rollback-data'
        else:
            self.state['fail_versions'].add('1.1.8')
            operation = 'update'; label = 'failed-new-data'
        def fault(current):
            if current == label + '-' + phase: raise SimulatedCrash()
        with self.assertRaises(SimulatedCrash):
            self.make(fault).dispatch(operation, identity, payload)
        result = self.make().dispatch('resume', 'resume_restore', {'target_operation_id': identity})
        self.assertEqual('rolled_back', result['status'])
        self.assertEqual('1.1.7', self.manager.current_version())
        self.assertEqual('preserve settings', (self.manager.root / 'data/config.txt').read_text())
        self.assertEqual('user project', (self.workspace / 'project.txt').read_text())
        starts = len(self.state['starts'])
        self.make().dispatch('resume', 'repeat_restore', {'target_operation_id': identity})
        self.assertEqual(starts, len(self.state['starts']))
    return test

for explicit in (False, True):
    for phase in ('prepared', 'quarantined', 'published', 'marked'):
        setattr(DeploymentTest, 'test_restore_%s_%s' % ('explicit' if explicit else 'failed_upgrade', phase),
                restore_phase_case(explicit, phase))

# Each phase is a distinct unittest case so a skipped phase cannot disappear from CI evidence.
def phase_case(phase):
    def test(self):
        self.seed(); payload = self.release(); fired = [False]
        def fault(current):
            if current == phase and not fired[0]:
                fired[0] = True
                raise SimulatedCrash()
        with self.assertRaises(SimulatedCrash):
            self.make(fault).dispatch('update', 'crash_case', payload)
        self.assertTrue(fired[0])
        result = self.make().dispatch('resume', 'resume_case', {'target_operation_id': 'crash_case', 'confirm_start': True})
        self.assertIn(result['status'], {'updated', 'ok'})
        self.assertEqual('1.1.8', self.manager.current_version())
        self.assertEqual('preserve settings', (self.manager.root / 'data/config.txt').read_text())
        self.assertEqual('user project', (self.workspace / 'project.txt').read_text())
        self.assertEqual('succeeded', self.manager.query('crash_case')['status'])
    return test
for phase in ('created','verify','prepared','stop','backup','switch','pointer-published','start','health','commit','complete'):
    setattr(DeploymentTest, 'test_resume_after_' + phase.replace('-', '_'), phase_case(phase))

if __name__ == '__main__':
    suite = unittest.defaultTestLoader.loadTestsFromTestCase(DeploymentTest)
    expected_tests = suite.countTestCases()
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    output = Path(os.environ.get('WB07_DEPLOYMENT_RESULT', 'evidence/contract/deployment-result.json'))
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps({'scope':'synthetic filesystem and injected process backend; real bundled archive extraction',
        'tests': result.testsRun, 'failures':len(result.failures), 'errors':len(result.errors), 'skipped':len(result.skipped),
        'production_installed':False, 'source_sha':os.environ.get('GITHUB_SHA','unknown')}, indent=2) + '\n')
    raise SystemExit(0 if result.wasSuccessful() and result.testsRun == expected_tests and not result.skipped else 1)
