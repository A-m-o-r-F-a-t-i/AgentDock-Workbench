#!/usr/bin/env python3
"""Exercise the release gate with isolated synthetic payloads and no network writes."""
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import subprocess
import textwrap
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location('workbench_release',ROOT/'scripts/release/publish-workbench.py')
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)

class ReleaseGate(unittest.TestCase):
    def test_repository_license_is_preserved(self):
        license_text=(ROOT/'LICENSE').read_text()
        self.assertTrue(license_text.startswith('Apache License'))
        builder=(ROOT/'packaging/build-unix-release.py').read_text()
        self.assertIn('LICENSE_ID = "Apache-2.0"',builder)
        self.assertIn('License: {LICENSE_ID}',builder)
        self.assertNotIn('License: MIT',builder)
        self.assertIn("%{ARCH} %{LICENSE}",builder)
        self.assertIn("stage/'share/agentdock/LICENSE'",builder)
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root=Path(self.temp.name); self.inputs=self.root/'input'; self.inputs.mkdir()
        self.dist=self.root/'dist';self.version=getattr(self,'target_version','1.1.6');self.commit='a'*40
        (self.inputs/'windows').mkdir()
        self.payloads=release.expected_payloads(self.version)
        for name in self.payloads:
            if name=='install.sh':continue
            path=self.inputs/name;path.write_bytes(('fixture:'+name).encode())
            path.with_name(name+'.sha256').write_text(f'{release.digest(path)}  {name}\n')
        base={'version':self.version,'commit':self.commit,'product_name':release.PRODUCT}
        self.reports={}
        for system in ['linux','darwin']:
            for arch in ['amd64','arm64']:
                self.reports[f'verification-{system}-{arch}.json']=dict(base,platform=f'{system}/{arch}',native_execution='passed',assets={})
        self.reports['verification-macos-app.json']=dict(base,bundle_verification='passed',embedded_core='passed',architectures=['amd64','arm64'],codesign='ad-hoc',notarization='not_run',assets={})
        self.reports['windows/build-report.json']=dict(base,channel='release',source_dirty=False,platforms=['windows/amd64','windows/arm64'],agentdock_authenticode='unsigned')
        self.reports['windows/verification.json']=[dict(base,channel='release',platform=f'windows/{arch}',native_execution=arch=='amd64',assets={}) for arch in ['amd64','arm64']]
        self.reports['windows/verification-scope.json']=dict(base,resolved_commit=self.commit,linux_tested_commit=self.commit)
        self.flush()
        self.mock=patch.object(release,'run',side_effect=lambda *args:self.commit if args[:2]==('git','rev-parse') else self.version if args==('go','run','./tools/release','version') else '')
        self.mock.start();self.addCleanup(self.mock.stop)
    def flush(self):
        for name,value in self.reports.items():(self.inputs/name).write_text(json.dumps(value))
    def assemble(self):return release.assemble(self.inputs,self.dist,self.version,self.commit)
    def test_complete_and_repeat(self):
        result=self.assemble();self.assertEqual(len(result['assets']),len(self.payloads))
        self.assertEqual(len(list(self.dist.iterdir())),2*len(self.payloads)+2)
        self.assertEqual(self.assemble()['commit'],self.commit)
    def test_missing_target_asset(self):
        (self.inputs/'AgentDockSetup-arm64.exe').unlink()
        with self.assertRaisesRegex(RuntimeError,'Missing verified build input'):self.assemble()
    def test_modified_payload(self):
        (self.inputs/'agentdock_linux_arm64.tar.gz').write_text('changed')
        with self.assertRaisesRegex(RuntimeError,'checksum'):self.assemble()
    def test_mixed_source(self):
        self.reports['verification-darwin-amd64.json']['commit']='b'*40;self.flush()
        with self.assertRaisesRegex(RuntimeError,'different source'):self.assemble()
    def test_conflicting_copies(self):
        other=self.inputs/'other';other.mkdir();(other/'AgentDockSetup-amd64.exe').write_text('different')
        with self.assertRaisesRegex(RuntimeError,'Conflicting artifact'):self.assemble()
    def test_report_hash_is_authoritative(self):
        self.reports['verification-linux-amd64.json']['assets']={'agentdock_linux_amd64.tar.gz':'f'*64};self.flush()
        with self.assertRaisesRegex(RuntimeError,'differs from target'):self.assemble()
    def test_missing_windows_architecture(self):
        self.reports['windows/verification.json'].pop();self.flush()
        with self.assertRaisesRegex(RuntimeError,'per-architecture'):self.assemble()
    def test_unexpected_staged_file(self):
        self.dist.mkdir();(self.dist/'foreign.exe').write_text('not in release')
        with self.assertRaisesRegex(RuntimeError,'unexpected artifacts'):self.assemble()

class EnhancedReleaseGate(ReleaseGate):
    target_version='1.1.7'
    def test_117_and_newer_keep_enhanced_acceptance(self):
        self.assertGreaterEqual(tuple(map(int,self.version.split('.'))),(1,1,7))
        manifest=self.assemble()
        native=[item for item in manifest['validation'] if item.get('native_privilege')=='passed']
        self.assertEqual({item['platform'] for item in native},{'windows/amd64','windows/arm64'})
        self.assertTrue(any(item.get('native_installation')=='passed' for item in manifest['validation']))
        workflow=(ROOT/'.github/workflows/workbench-release.yml').read_text()
        self.assertIn("github.event.forced != true",workflow)
        self.assertIn("github.event.deleted != true",workflow)
        self.assertIn('needs: [resolve-source, windows, windows-arm-package, unix, macos-app, android]',workflow)
        recovery=(ROOT/'.github/workflows/workbench-publish-existing.yml').read_text()
        self.assertIn(">= (1,1,7)",recovery)
        self.assertIn('Native Windows ARM64 install and uninstall',recovery)
    def setUp(self):
        super().setUp()
        for arch in ['amd64','arm64']:
            self.reports[f'verification-linux-{arch}.json']['package_installation']={'deb':'native_installed_verified_removed','rpm':'isolated_root_installed_verified_removed'}
            self.reports[f'acceptance-source-{arch}.json']={'version':self.version,'commit':self.commit,'platform':f'windows/{arch}','native_privilege':'passed','native_backup':'passed','keyboard':'passed' if arch=='amd64' else 'tested_on_x64'}
        self.reports['verification-native-windows-arm64.json']={'version':self.version,'commit':self.commit,'platform':'windows/arm64','native_execution':'passed','native_installation':'passed'}
        self.reports['windows/verification-scope.json']['installation_tests']='passed'
        self.flush()
    def test_missing_linux_installation_cannot_publish(self):
        self.reports['verification-linux-arm64.json']['package_installation']='not_run';self.flush()
        with self.assertRaisesRegex(RuntimeError,'Linux package installation'):self.assemble()
    def test_missing_native_recovery_cannot_publish(self):
        self.reports['acceptance-source-amd64.json']['native_privilege']='not_run';self.flush()
        with self.assertRaisesRegex(RuntimeError,'recovery/metadata/keyboard'):self.assemble()
    def test_failed_arm_installation_cannot_publish(self):
        self.reports['verification-native-windows-arm64.json']['native_installation']='failed';self.flush()
        with self.assertRaisesRegex(RuntimeError,'ARM64 installation'):self.assemble()
    def test_missing_x64_installation_cannot_publish(self):
        self.reports['windows/verification-scope.json']['installation_tests']='not_run';self.flush()
        with self.assertRaisesRegex(RuntimeError,'x64 isolated installation'):self.assemble()
    def test_native_evidence_cannot_mix_source_generations(self):
        self.reports['acceptance-source-arm64.json']['commit']='b'*40;self.flush()
        with self.assertRaisesRegex(RuntimeError,'different source'):self.assemble()

class AndroidReleaseGate(EnhancedReleaseGate):
    target_version='1.1.8'
    def setUp(self):
        super().setUp()
        apk=f'AgentDock-Workbench-{self.version}-Android-test-signed.apk'
        self.reports['verification-android.json']={
            'version':self.version,'commit':self.commit,'product_name':release.PRODUCT,
            'platform':'android/arm64','apk_validation':'passed','emulator_validation':'passed',
            'api_levels':[26,33,34,35,37],'signing':'test-signed Android debug key',
            'physical_arm64_termux':'not_run',
            'emulators':[{'api':api,'tests':13,'passed':13,'screenshots':23,'attempt_exit_codes':[0]} for api in [26,33,34,35,37]],
            'assets':{apk:release.digest(self.inputs/apk)}}
        self.flush()
    def test_android_payload_and_prerelease_manifest(self):
        manifest=release.assemble(self.inputs,self.dist,self.version,self.commit,True)
        self.assertTrue(manifest['prerelease'])
        self.assertEqual(len(manifest['assets']),17)
        self.assertIn('android/arm64',manifest['platforms'])
        self.assertEqual(manifest['signing']['android'],'test-signed Android debug key')
    def test_missing_android_report_blocks_publication(self):
        (self.inputs/'verification-android.json').unlink()
        with self.assertRaisesRegex(RuntimeError,'Missing verified build input'):self.assemble()
    def test_cancelled_android_matrix_blocks_publication(self):
        self.reports['verification-android.json']['emulator_validation']='cancelled';self.flush()
        with self.assertRaisesRegex(RuntimeError,'five-API validation incomplete'):self.assemble()
    def test_missing_android_api_blocks_publication(self):
        self.reports['verification-android.json']['emulators'].pop();self.flush()
        with self.assertRaisesRegex(RuntimeError,'five-API validation incomplete'):self.assemble()
    def test_mixed_android_source_blocks_publication(self):
        self.reports['verification-android.json']['commit']='b'*40;self.flush()
        with self.assertRaisesRegex(RuntimeError,'different source'):self.assemble()
    def test_incomplete_android_tests_block_publication(self):
        self.reports['verification-android.json']['emulators'][0]['passed']=12;self.flush()
        with self.assertRaisesRegex(RuntimeError,'navigation/screenshot evidence'):self.assemble()

class PublicationGate(unittest.TestCase):
    def setUp(self):
        temporary=tempfile.TemporaryDirectory();self.addCleanup(temporary.cleanup)
        self.root=Path(temporary.name);self.dist=self.root/'dist';self.dist.mkdir()
        notes=self.root/'docs/releases';notes.mkdir(parents=True);(notes/'v1.1.7.md').write_text('Workbench release notes')
        self.commit='a'*40;self.tag='v1.1.7';self.endpoint=f'repos/{release.REPOSITORY}/releases/123'
        (self.dist/'package.zip').write_bytes(b'verified package')
        path=self.dist/'package.zip'
        self.asset={'name':path.name,'size':path.stat().st_size,'digest':'sha256:'+release.digest(path),'state':'uploaded'}
        self.record={'id':123,'tag_name':self.tag,'draft':True,'prerelease':False,'name':'AgentDock Workbench 1.1.7','assets':[self.asset],'html_url':'https://github.com/example/release'}
        self.commands=[];self.lookup=[]
        self.stable=dict(self.record,id=99,tag_name='v1.1.6',draft=False)
        self.latest=self.stable
        for mock in [patch.object(release,'ROOT',self.root),patch.dict(os.environ,{'GITHUB_REPOSITORY':release.REPOSITORY,'GITHUB_STEP_SUMMARY':''}),patch.object(release,'run',side_effect=self.fake_command)]:
            mock.start();self.addCleanup(mock.stop)
    def fake_command(self,*args):
        self.commands.append(args)
        if args[:2]==('git','ls-remote'):return self.commit+'\trefs/tags/'+self.tag
        if args[:4]==('gh','api','--method','POST'):
            if args[4]==f'repos/{release.REPOSITORY}/releases':
                self.record['assets']=[];return json.dumps(self.record)
            if args[4].startswith(f'https://uploads.github.com/repos/{release.REPOSITORY}/releases/123/assets?name='):
                self.record['assets']=[self.asset];return json.dumps(self.asset)
        if args[:4]==('gh','api','--method','PATCH'):
            self.assertEqual(args[4],self.endpoint)
            self.record['draft']=False
            self.record['prerelease']='prerelease=true' in args
            self.record['name']=next(value.split('=',1)[1] for value in args if value.startswith('name='))
            if 'make_latest=true' in args:self.latest=self.record
            return json.dumps(self.record)
        if args[:2]==('gh','api'):
            target=args[2]
            self.assertNotIn('/releases/tags/',target,'Draft publication must never query the published-only tag endpoint')
            if '/releases?' in target:return json.dumps(self.lookup.pop(0) if self.lookup else [self.record])
            if target==self.endpoint:return json.dumps(self.record)
            if target.endswith('/releases/latest'):return json.dumps(self.latest)
        raise AssertionError('Unexpected command '+repr(args))
    def publish(self):release.publish(self.dist,'1.1.7',self.commit)
    def mutations(self):return [args for args in self.commands if args[:4] in [('gh','api','--method','POST'),('gh','api','--method','PATCH')]]
    def test_existing_complete_draft_uses_id_without_reupload(self):
        self.publish()
        self.assertFalse(self.record['draft'])
        self.assertEqual(len(self.mutations()),1)
        self.assertEqual(self.mutations()[0][:5],('gh','api','--method','PATCH',self.endpoint))
    def test_partial_upload_only_supplies_missing_files(self):
        self.record['assets']=[];self.publish()
        uploads=[args for args in self.commands if args[:4]==('gh','api','--method','POST') and args[4].startswith('https://uploads.github.com/')]
        self.assertEqual(len(uploads),1);self.assertEqual(uploads[0][-2:],('--input',str(self.dist/'package.zip')));self.assertNotIn('--clobber',uploads[0])
    def test_missing_draft_is_created_then_loaded_by_id(self):
        self.lookup=[[],[]];self.publish()
        self.assertEqual(len([args for args in self.commands if args[:5]==('gh','api','--method','POST',f'repos/{release.REPOSITORY}/releases')]),1)
        self.assertEqual(self.lookup,[[]],'Creation must use its returned ID rather than requiring immediate list visibility')
    def test_binary_asset_name_is_url_encoded(self):
        path=self.dist/'package name.zip';(self.dist/'package.zip').rename(path)
        self.asset['name']=path.name;self.record['assets']=[];self.publish()
        urls=[args[4] for args in self.commands if args[:4]==('gh','api','--method','POST') and args[4].startswith('https://uploads.github.com/')]
        self.assertEqual(urls,[f'https://uploads.github.com/repos/{release.REPOSITORY}/releases/123/assets?name=package%20name.zip'])
    def test_wrong_digest_is_not_overwritten(self):
        self.record['assets']=[dict(self.asset,digest='sha256:'+'f'*64)]
        with self.assertRaisesRegex(RuntimeError,'integrity mismatch'):self.publish()
        self.assertEqual(self.mutations(),[])
    def test_unexpected_remote_asset_stops_publication(self):
        self.record['assets'].append(dict(self.asset,name='unrelated.zip'))
        with self.assertRaisesRegex(RuntimeError,'unexpected assets'):self.publish()
        self.assertEqual(self.mutations(),[])
    def test_published_release_is_not_mutated(self):
        self.record['draft']=False
        with self.assertRaisesRegex(RuntimeError,'already published'):self.publish()
        self.assertEqual(self.mutations(),[])
    def test_duplicate_drafts_are_not_guessed(self):
        self.lookup=[[self.record,dict(self.record,id=124)]]
        with self.assertRaisesRegex(RuntimeError,'Multiple releases'):self.publish()
        self.assertEqual(self.mutations(),[])
    def test_draft_lookup_paginates_without_tag_query(self):
        self.lookup=[[{'tag_name':'unrelated'}]*100,[self.record]]
        self.assertEqual(release.find_release(self.tag)['id'],123)
        self.assertTrue(any('page=2' in args[-1] for args in self.commands))
    def test_listing_is_bounded_and_never_creates_on_uncertainty(self):
        self.lookup=[[{'tag_name':'unrelated'}]*100 for _ in range(20)]
        with self.assertRaisesRegex(RuntimeError,'bounded search'):self.publish()
        self.assertEqual(self.mutations(),[])

    def test_prerelease_preserves_stable_latest(self):
        release.publish(self.dist,'1.1.7',self.commit,True)
        self.assertTrue(self.record['prerelease'])
        self.assertFalse(self.record['draft'])
        self.assertEqual(self.record['name'],'AgentDock Workbench 1.1.7 Pre-release')
        self.assertEqual(self.latest['id'],99)
        mutation=self.mutations()[-1]
        self.assertIn('prerelease=true',mutation)
        self.assertIn('make_latest=false',mutation)
        self.assertNotIn('make_latest=true',mutation)
    def test_stable_release_remains_latest_by_default(self):
        self.publish()
        self.assertEqual(self.latest['id'],123)
        self.assertIn('make_latest=true',self.mutations()[-1])

class CandidateScaleException(unittest.TestCase):
    def test_exception_is_opt_in_scoped_and_non_publishing(self):
        workflow=(ROOT/'.github/workflows/workbench-release.yml').read_text()
        option=workflow.split('      ignore_macos_intel_scale:',1)[1].split('\npermissions:',1)[0]
        self.assertIn('default: false',option)
        self.assertIn("inputs.ignore_macos_intel_scale && (inputs.publish || github.event_name == 'push')",workflow)
        self.assertIn("IGNORE_INTEL_SCALE: ${{ inputs.ignore_macos_intel_scale && matrix.platform == 'darwin' && matrix.arch == 'amd64' }}",workflow)
        self.assertIn('candidate-validation-exceptions.json',workflow)
        self.assertIn("'status': 'ignored_by_user_request'",workflow)
        self.assertIn('coldLimit := 2 * time.Second',(ROOT/'internal/activity/scale_test.go').read_text())

    @unittest.skipUnless(os.name=='posix','exercise the Bash native-Unix workflow step')
    def test_only_exact_scale_case_is_omitted_and_remaining_checks_run(self):
        workflow=(ROOT/'.github/workflows/workbench-release.yml').read_text()
        step=workflow.split('      - name: Native backend regression\n',1)[1].split('      - name:',1)[0]
        script=textwrap.dedent(step.split('        run: |\n',1)[1])
        for ignored in ['false','true']:
            with self.subTest(ignored=ignored), tempfile.TemporaryDirectory() as directory:
                root=Path(directory)
                fake_go=root/'go'
                fake_go.write_text('#!/bin/sh\nif [ "$1" = list ]; then\n printf "%s\\n" github.com/uvwt/agentdock/internal/activity github.com/uvwt/agentdock/cmd/agentdock\nelse\n printf "%s\\n" "$*" >> "$GO_INVOCATIONS"\nfi\n')
                fake_go.chmod(0o700)
                env=dict(os.environ,PATH=str(root)+os.pathsep+os.environ['PATH'],IGNORE_INTEL_SCALE=ignored,GO_INVOCATIONS=str(root/'calls'),GITHUB_STEP_SUMMARY=str(root/'summary'))
                subprocess.run(['bash','-c',script],env=env,check=True,capture_output=True,text=True)
                calls=(root/'calls').read_text().splitlines()
                self.assertIn('test -p 2 github.com/uvwt/agentdock/cmd/agentdock -count=1 -timeout=8m',calls)
                self.assertIn('vet ./...',calls)
                if ignored=='true':
                    self.assertEqual(len(calls),3)
                    self.assertIn('test -p 1 ./internal/activity -skip ^TestExecutionProjectionScale100k$ -count=1 -timeout=8m',calls)
                    self.assertIn('ignored by explicit candidate request',(root/'summary').read_text())
                else:
                    self.assertEqual(len(calls),4)
                    self.assertIn('test -p 1 ./internal/activity -run ^TestExecutionProjectionScale100k$ -count=3 -v -timeout=8m',calls)
                    self.assertFalse(any('-skip' in call for call in calls))

if __name__=='__main__':unittest.main()
