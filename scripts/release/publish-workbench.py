#!/usr/bin/env python3
"""Assemble one verified source generation, then optionally publish its complete release."""
from __future__ import annotations
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import zipfile
from urllib.parse import quote

ROOT = Path(__file__).resolve().parents[2]
REPOSITORY = 'A-m-o-r-F-a-t-i/AgentDock-Workbench'
PRODUCT = 'ACTOVO'
ANDROID_RELEASE_CONFIG = Path('packaging/android/release-signing.json')


def android_release_identity() -> tuple[str,str]:
    path=ROOT/ANDROID_RELEASE_CONFIG
    value=json.loads(path.read_text(encoding='utf-8-sig'))
    alias=value.get('key_alias'); certificate=str(value.get('certificate_sha256','')).lower()
    if value.get('schema_version')!=1 or not isinstance(alias,str) or not alias or not re.fullmatch(r'[a-f0-9]{64}',certificate):
        raise RuntimeError('Invalid committed Android release signing identity')
    return alias,certificate


def actovo_brand_required(version: str) -> bool:
    return tuple(map(int,version.split('.'))) >= (1,1,9)


def stable_android_release_required(version: str) -> bool:
    return tuple(map(int,version.split('.'))) >= (1,1,9)


def product_asset_prefix(version: str) -> str:
    return 'ACTOVO' if actovo_brand_required(version) else 'AgentDock-Workbench'


def android_candidate_asset_name(version: str) -> str:
    return f'{product_asset_prefix(version)}-{version}-Android-test-signed.apk'


def android_public_asset_name(version: str) -> str:
    suffix='Android.apk' if stable_android_release_required(version) else 'Android-test-signed.apk'
    return f'{product_asset_prefix(version)}-{version}-{suffix}'


def macos_asset_names(version: str) -> tuple[str,str]:
    prefix='ACTOVO' if actovo_brand_required(version) else 'AgentDock'
    return f'{prefix}-macos-universal.dmg',f'{prefix}-macos-universal.zip'


def run(*args: str) -> str:
    return subprocess.check_output(args,cwd=ROOT,text=True,encoding='utf-8',timeout=600).strip()


def digest(path: Path) -> str:
    with path.open('rb') as stream:
        return hashlib.file_digest(stream,'sha256').hexdigest()


def zip_member_digest(path: Path, member: str, maximum: int=64*1024*1024) -> str:
    if path.is_symlink() or not path.is_file():
        raise RuntimeError(f'Invalid ZIP input: {path.name}')
    try:
        with zipfile.ZipFile(path) as archive:
            matches=[value for value in archive.infolist() if value.filename==member]
            if len(matches)!=1 or matches[0].is_dir() or not 0<matches[0].file_size<=maximum:
                raise RuntimeError(f'APK must contain exactly one bounded {member}')
            with archive.open(matches[0]) as stream:
                return hashlib.file_digest(stream,'sha256').hexdigest()
    except zipfile.BadZipFile as error:
        raise RuntimeError(f'Invalid APK ZIP: {path.name}') from error


def locate(directory: Path, name: str) -> Path:
    paths = sorted(directory.rglob(name))
    paths = [path for path in paths if path.is_file() and not path.is_symlink()]
    if not paths:
        raise RuntimeError(f'Missing verified build input: {name}')
    if len({digest(path) for path in paths}) != 1:
        raise RuntimeError(f'Conflicting artifact copies: {name}')
    return paths[0]


def read_report(directory: Path,name: str) -> dict | list:
    return json.loads(locate(directory,name).read_text(encoding='utf-8-sig'))


def identity(report: dict,version: str,commit: str) -> None:
    if report.get('version')!=version or report.get('commit')!=commit:
        raise RuntimeError('Validation/build reports refer to different source generations')


def windows_setup_names(version: str) -> list[str]:
    """Preserve historical Release names while using ACTOVO names from 1.1.9."""
    prefix='ACTOVO-Setup' if actovo_brand_required(version) else 'AgentDockSetup'
    return [f'{prefix}-{arch}.exe' for arch in ['amd64','arm64']]


def expected_payloads(version: str) -> list[str]:
    macos_dmg,macos_zip=macos_asset_names(version)
    payloads = [f'agentdock_{platform}_{arch}.tar.gz' for platform in ['linux','darwin'] for arch in ['amd64','arm64']] + [
        f'agentdock_windows_{arch}.zip' for arch in ['amd64','arm64']] + windows_setup_names(version) + [
        macos_dmg,macos_zip,
        f'agentdock-workbench_{version}_amd64.deb',f'agentdock-workbench_{version}_arm64.deb',
        f'agentdock-workbench-{version}-1.x86_64.rpm',f'agentdock-workbench-{version}-1.aarch64.rpm',
        'install.sh','install.ps1']
    if tuple(map(int,version.split('.'))) >= (1,1,8):
        payloads.append(android_public_asset_name(version))
    return payloads


def public_installers(version: str) -> list[str]:
    """Return user-installable release assets only."""
    macos_dmg,_=macos_asset_names(version)
    installers = windows_setup_names(version) + [
        macos_dmg,
        f'agentdock-workbench_{version}_amd64.deb',
        f'agentdock-workbench_{version}_arm64.deb',
        f'agentdock-workbench-{version}-1.x86_64.rpm',
        f'agentdock-workbench-{version}-1.aarch64.rpm',
    ]
    if tuple(map(int,version.split('.'))) >= (1,1,8):
        installers.append(android_public_asset_name(version))
    return installers


def obsolete_public_installers(version: str) -> set[str]:
    """Return historical public names that an explicit replacement may remove."""
    obsolete: set[str] = set()
    if stable_android_release_required(version):
        obsolete.update({
            android_candidate_asset_name(version),
            f'AgentDock-Workbench-{version}-Android.apk',
            f'AgentDock-Workbench-{version}-Android-test-signed.apk',
        })
    if actovo_brand_required(version):
        obsolete.update({
            'AgentDockSetup-amd64.exe','AgentDockSetup-arm64.exe',
            'AgentDock-Workbench-Setup-amd64.exe','AgentDock-Workbench-Setup-arm64.exe',
            'AgentDock-macos-universal.dmg',
        })
        for prefix in ('ACTOVO','AgentDock-Workbench'):
            stem=f'{prefix}-{version}-Android-Core-arm64'
            obsolete.update({stem+'.tar.gz',stem+'.manifest.json',stem+'.manifest.sig'})
    return obsolete


def assemble(inputs: Path,dist: Path,version: str,commit: str,prerelease: bool=False) -> dict:
    if not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+',version) or not re.fullmatch(r'[a-f0-9]{40}',commit):
        raise RuntimeError('Invalid release identity')
    if run('git','rev-parse','HEAD')!=commit or run('go','run','./tools/release','version')!=version:
        raise RuntimeError('Publisher checkout does not match build evidence')
    if run('git','status','--porcelain','--untracked-files=no'):
        raise RuntimeError('Verified build tracked sources were modified')
    reports=[]
    enhanced_acceptance=tuple(map(int,version.split('.'))) >= (1,1,7)
    for platform in ['linux','darwin']:
        for arch in ['amd64','arm64']:
            report=read_report(inputs,f'verification-{platform}-{arch}.json')
            identity(report,version,commit)
            if report.get('platform')!=f'{platform}/{arch}' or report.get('native_execution')!='passed' or report.get('product_name')!=PRODUCT:
                raise RuntimeError('Missing native Unix execution/branding evidence')
            if enhanced_acceptance and platform=='linux':
                installed=report.get('package_installation',{})
                if not isinstance(installed,dict) or installed.get('deb')!='native_installed_verified_removed' or installed.get('rpm')!='isolated_root_installed_verified_removed':
                    raise RuntimeError('Missing native Linux package installation/removal evidence')
            reports.append(report)
    mac=read_report(inputs,'verification-macos-app.json');identity(mac,version,commit)
    if mac.get('bundle_verification')!='passed' or mac.get('embedded_core')!='passed' or set(mac.get('architectures',[]))!={'amd64','arm64'}:
        raise RuntimeError('macOS universal app verification incomplete')
    reports.append(mac)
    windows=read_report(inputs/'windows','build-report.json');identity(windows,version,commit)
    if windows.get('source_dirty') or windows.get('channel')!='release' or set(windows.get('platforms',[]))!={'windows/amd64','windows/arm64'}:
        raise RuntimeError('Windows build was not a clean dual-architecture release build')
    verified=read_report(inputs/'windows','verification.json')
    if not isinstance(verified,list) or len(verified)!=2:
        raise RuntimeError('Missing per-architecture Windows verification')
    if {item.get('platform') for item in verified}!={'windows/amd64','windows/arm64'}:
        raise RuntimeError('Windows verification architecture mismatch')
    for item in verified:
        identity(item,version,commit)
        if item.get('product_name')!=PRODUCT or item.get('channel')!='release':
            raise RuntimeError('Windows verified product/channel mismatch')
        if item['platform']=='windows/amd64' and item.get('native_execution') is not True:
            raise RuntimeError('Windows x64 packaged execution was not verified')
    reports.extend(verified)
    if enhanced_acceptance:
        for arch in ['amd64','arm64']:
            native=read_report(inputs,f'acceptance-source-{arch}.json');identity(native,version,commit)
            if native.get('platform')!=f'windows/{arch}' or native.get('native_privilege')!='passed' or native.get('native_backup')!='passed' or arch=='amd64' and native.get('keyboard')!='passed':
                raise RuntimeError('Native Windows recovery/metadata/keyboard acceptance incomplete')
            reports.append(dict(native,assets={}))
        arm_native=read_report(inputs,'verification-native-windows-arm64.json');identity(arm_native,version,commit)
        if arm_native.get('platform')!='windows/arm64' or arm_native.get('native_installation')!='passed' or arm_native.get('native_execution')!='passed':
            raise RuntimeError('Native Windows ARM64 installation evidence incomplete')
        reports.append(dict(arm_native,assets={}))
    android = None
    android_release = None
    stable_android = stable_android_release_required(version)
    if tuple(map(int,version.split('.'))) >= (1,1,8):
        android=read_report(inputs,'verification-android.json');identity(android,version,commit)
        expected_apis={26,33,34,35,37}
        candidate_name=android_candidate_asset_name(version)
        candidate_assets=android.get('assets')
        if (android.get('product_name')!=PRODUCT or android.get('platform')!='android/arm64' or
                android.get('signing')!='test-signed Android debug key' or
                android.get('apk_validation')!='passed' or android.get('emulator_validation')!='passed' or
                set(android.get('api_levels',[]))!=expected_apis or len(android.get('emulators',[]))!=5 or
                not isinstance(candidate_assets,dict) or set(candidate_assets)!={candidate_name} or
                not re.fullmatch(r'[a-f0-9]{64}',str(candidate_assets.get(candidate_name,'')))):
            raise RuntimeError('Android APK or five-API validation incomplete')
        if {row.get('api') for row in android['emulators']}!=expected_apis:
            raise RuntimeError('Android API evidence mismatch')
        for row in android['emulators']:
            if (type(row.get('tests')) is not int or row['tests']<13 or row.get('passed')!=row['tests'] or
                    row.get('screenshots',0)<23 or row.get('attempt_exit_codes')!=[0]):
                raise RuntimeError('Android navigation/screenshot evidence incomplete')
        candidate_checksum=str(candidate_assets[candidate_name])
        candidate=locate(inputs,candidate_name)
        if digest(candidate)!=candidate_checksum or locate(inputs,candidate_name+'.sha256').read_text(encoding='utf-8-sig').strip()!=f'{candidate_checksum}  {candidate_name}':
            raise RuntimeError('Android candidate bytes differ from verified evidence')
        if stable_android:
            raw_core_name='agentdock_linux_arm64.tar.gz'
            raw_core_checksum=digest(locate(inputs,raw_core_name))
            bundled_core_asset='assets/agentdock-core-arm64.tgz'
            if zip_member_digest(candidate,bundled_core_asset)!=raw_core_checksum:
                raise RuntimeError('Android candidate does not contain the verified Linux ARM64 Core')
            public_name=android_public_asset_name(version)
            alias,certificate=android_release_identity()
            android_release=read_report(inputs,'verification-android-release.json');identity(android_release,version,commit)
            release_assets=android_release.get('assets')
            bundled_core=android_release.get('bundled_core')
            if (android_release.get('product_name')!=PRODUCT or android_release.get('platform')!='android/arm64' or
                    android_release.get('signing')!='stable Android release key' or
                    android_release.get('release_signing_validation')!='passed' or
                    android_release.get('key_alias')!=alias or android_release.get('certificate_sha256')!=certificate or
                    android_release.get('source_apk')!={'name':candidate_name,'sha256':candidate_checksum} or
                    bundled_core!={'apk_asset':bundled_core_asset,
                        'source_archive':{'name':raw_core_name,'sha256':raw_core_checksum},'validation':'passed'} or
                    not isinstance(release_assets,dict) or set(release_assets)!={public_name} or
                    not re.fullmatch(r'[a-f0-9]{64}',str(release_assets.get(public_name,'')))):
                raise RuntimeError('Stable Android release signing evidence is incomplete')
            public_apk=locate(inputs,public_name)
            if digest(public_apk)!=release_assets[public_name] or zip_member_digest(public_apk,bundled_core_asset)!=raw_core_checksum:
                raise RuntimeError('Published Android APK lost the verified bundled Core')
            reports.append(dict(android,assets={}))
            reports.append(android_release)
        else:
            reports.append(android)
    scope=read_report(inputs/'windows','verification-scope.json');identity(scope,version,commit)
    if scope.get('resolved_commit')!=commit or scope.get('linux_tested_commit')!=commit:
        raise RuntimeError('Windows workflow lost its immutable validation source')
    if enhanced_acceptance and scope.get('installation_tests')!='passed':
        raise RuntimeError('Missing Windows x64 isolated installation/uninstall evidence')
    dist.mkdir(parents=True,exist_ok=True)
    payloads=expected_payloads(version)
    for name in payloads:
        if name=='install.sh':
            source=ROOT/'scripts/install/install.sh'
            shutil.copy2(source,dist/name)
            (dist/(name+'.sha256')).write_text(f'{digest(source)}  {name}\n',encoding='utf-8')
        else:
            source=locate(inputs,name)
            expected=f'{digest(source)}  {name}'
            if locate(inputs,name+'.sha256').read_text(encoding='utf-8-sig').strip()!=expected:
                raise RuntimeError(f'Invalid published checksum: {name}')
            shutil.copy2(source,dist/name)
            shutil.copy2(locate(inputs,name+'.sha256'),dist/(name+'.sha256'))
    actual={name:digest(dist/name) for name in payloads}
    for report in reports:
        for name,checksum in report['assets'].items():
            if name not in actual or actual[name]!=checksum:
                raise RuntimeError(f'Artifact differs from target-platform verification: {name}')
    run('go','run','./tools/release','verify-dist',str(dist))
    signing={'windows':windows['agentdock_authenticode'],'macos':mac['codesign'],'macos_notarization':mac['notarization'],
             'android':android_release['signing'] if android_release else android['signing'] if android else 'not_included'}
    if android and not stable_android:
        signing['android_core']='not_included'
    manifest={'schema_version':1,'product_name':PRODUCT,'version':version,'commit':commit,'prerelease':prerelease,
              'platforms':[f'{platform}/{arch}' for platform in ['windows','linux','darwin'] for arch in ['amd64','arm64']]+(['android/arm64'] if android else []),
              'signing':signing,
              'validation':reports,'windows_verification_scope':scope,
              'assets':[{'name':name,'bytes':(dist/name).stat().st_size,'sha256':actual[name]} for name in sorted(payloads)]}
    (dist/'release-manifest.json').write_text(json.dumps(manifest,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
    (dist/'SHA256SUMS').write_text(''.join(f'{digest(dist/name)}  {name}\n' for name in sorted(payloads+['release-manifest.json'])),encoding='utf-8')
    allowed=set(payloads+[name+'.sha256' for name in payloads]+['release-manifest.json','SHA256SUMS'])
    if {path.name for path in dist.iterdir()}!=allowed:
        raise RuntimeError('Staging directory contains unexpected artifacts')
    return manifest


def find_release(tag: str) -> dict | None:
    # /releases/tags/{tag} only returns published releases. The authenticated
    # list includes drafts; retain their numeric ID throughout publication.
    matches=[]
    for page in range(1,21):
        records=json.loads(run('gh','api',f'repos/{REPOSITORY}/releases?per_page=100&page={page}'))
        if not isinstance(records,list):raise RuntimeError('Invalid release listing')
        matches.extend(record for record in records if record.get('tag_name')==tag)
        if len(matches)>1:raise RuntimeError('Multiple releases reference the same tag; resolve before publishing')
        if len(records)<100:return matches[0] if matches else None
    raise RuntimeError('Release listing exceeded its bounded search; no release was created')


def release_assets(record: dict,expected: dict[str,dict],allowed_extra: set[str] | None=None) -> dict[str,dict]:
    rows=record.get('assets',[])
    if not isinstance(rows,list):
        raise RuntimeError('Remote release assets are invalid')
    actual={}
    for asset in rows:
        if not isinstance(asset,dict) or not isinstance(asset.get('name'),str) or not asset['name'] or asset['name'] in actual:
            raise RuntimeError('Remote release contains duplicate or unexpected assets')
        actual[asset['name']]=asset
    allowed=set(expected)
    if allowed_extra:
        allowed.update(allowed_extra)
    if set(actual)-allowed:
        raise RuntimeError('Remote release contains duplicate or unexpected assets')
    return actual


def asset_matches(asset: dict,expected: dict) -> bool:
    return asset.get('state')=='uploaded' and asset.get('size')==expected['size'] and asset.get('digest')==expected['digest']


def missing_release_assets(record: dict,expected: dict[str,dict]) -> list[str]:
    actual=release_assets(record,expected)
    for name,asset in actual.items():
        if not asset_matches(asset,expected[name]):
            raise RuntimeError(f'Remote asset integrity mismatch: {name}; existing bytes were not overwritten')
    return sorted(set(expected)-set(actual))


def remote_tag_identity(tag: str) -> tuple[str | None,str | None]:
    remote=run('git','ls-remote','--tags','origin',f'refs/tags/{tag}',f'refs/tags/{tag}^{{}}')
    if not remote:
        return None,None
    refs={line.split()[1]:line.split()[0] for line in remote.splitlines() if len(line.split())==2}
    direct=refs.get(f'refs/tags/{tag}')
    resolved=refs.get(f'refs/tags/{tag}^{{}}',direct)
    if not direct or not resolved or not re.fullmatch(r'[a-f0-9]{40}',direct) or not re.fullmatch(r'[a-f0-9]{40}',resolved):
        raise RuntimeError('Remote release tag identity is incomplete')
    return direct,resolved


def write_release_tag(tag: str,commit: str,title: str,expected_ref: str | None=None) -> None:
    # A release tag is an immutable source pointer here, not a signed identity.
    # Use a lightweight local ref so recovery works in clean CI checkouts that
    # deliberately have no git author configuration.
    run('git','tag','-f',tag,commit)
    arguments=['git','push']
    if expected_ref is not None:
        arguments.append(f'--force-with-lease=refs/tags/{tag}:{expected_ref}')
    arguments.extend(['origin',f'refs/tags/{tag}:refs/tags/{tag}'])
    run(*arguments)
    _,resolved=remote_tag_identity(tag)
    if resolved!=commit:
        raise RuntimeError('Remote release tag does not match the verified source after update')


def publish(dist: Path,version: str,commit: str,prerelease: bool=False,
            replace_published: bool=False,replace_stable: bool=False) -> None:
    if os.environ.get('GITHUB_REPOSITORY')!=REPOSITORY:
        raise RuntimeError('Publication is restricted to the user fork')
    if replace_published and replace_stable:
        raise RuntimeError('Published prerelease and stable replacement modes are mutually exclusive')
    if replace_stable and prerelease:
        raise RuntimeError('Stable replacement must remain on the stable release channel')
    replacement_requested=replace_published or replace_stable
    tag='v'+version
    title=f'{PRODUCT} {version}' + (' Pre-release' if prerelease else '')
    legacy_title=f'AgentDock Workbench {version}' + (' Pre-release' if prerelease else '')
    accepted_replacement_titles={title,legacy_title}
    prerelease_field='prerelease='+str(prerelease).lower()
    latest_field='make_latest='+('false' if prerelease else 'true')
    latest_before=json.loads(run('gh','api',f'repos/{REPOSITORY}/releases/latest')) if prerelease or replace_stable else None
    previous_latest=latest_before.get('id') if prerelease else None
    notes=ROOT/f'docs/releases/{tag}.md'
    if not notes.is_file():raise RuntimeError('Final release notes are missing')
    public_names=public_installers(version)
    files=[]
    for name in public_names:
        path=dist/name
        if not path.is_file() or path.is_symlink():
            raise RuntimeError(f'Missing public installer: {name}')
        files.append(path)
    expected={path.name:{'size':path.stat().st_size,'digest':'sha256:'+digest(path)} for path in files}
    record=find_release(tag)
    if record is None:
        if replacement_requested:
            raise RuntimeError('Published replacement requires an existing release')
    elif not isinstance(record,dict) or type(record.get('id')) is not int or record['id']<=0 or record.get('tag_name')!=tag:
        raise RuntimeError('Created release could not be located; preserve draft and inspect before retrying')
    obsolete=obsolete_public_installers(version) if replacement_requested else set()
    if record is not None:
        release_assets(record,expected,obsolete)
        if replace_published and record.get('prerelease') is not True:
            raise RuntimeError('Published replacement requires an existing prerelease')
        if replace_stable:
            if record.get('draft'):
                if record.get('prerelease') is not True or record.get('name') not in accepted_replacement_titles:
                    raise RuntimeError('Stable replacement recovery requires the original stable release draft')
            elif record.get('prerelease') is not False or record.get('name') not in accepted_replacement_titles:
                raise RuntimeError('Stable replacement requires an existing stable release')
            elif not isinstance(latest_before,dict) or latest_before.get('id')!=record['id']:
                raise RuntimeError('Stable replacement requires the current Latest release')
        if replacement_requested and record.get('immutable') is True:
            raise RuntimeError('Published release is immutable and cannot be replaced')
    release_target=str(record.get('target_commitish','')).lower() if record is not None else ''
    release_was_draft=bool(record.get('draft')) if record is not None else False
    tag_ref,tag_commit=remote_tag_identity(tag)
    if replacement_requested and tag_ref is not None and tag_commit!=commit:
        allowed_targets={tag_commit}
        if release_was_draft:
            # A prior replacement may have updated the draft target before its
            # compare-and-swap tag push failed. Resume that exact partial state.
            allowed_targets.add(commit)
        if release_target not in allowed_targets:
            raise RuntimeError('Existing release target does not match the remote tag or verified source; preserve it for inspection')
    if tag_ref is None:
        if record is not None:
            raise RuntimeError('Existing release has no remote tag; preserve it for inspection')
        write_release_tag(tag,commit,title)
        tag_ref,tag_commit=remote_tag_identity(tag)
    elif tag_commit!=commit and not replacement_requested:
        raise RuntimeError('Remote release tag does not match the verified source')
    if record is None:
        # Retain the identity returned by the successful POST. A subsequent
        # list can temporarily omit the freshly created draft. Never repeat
        # creation merely because a collection read did not show it yet.
        record=json.loads(run('gh','api','--method','POST',f'repos/{REPOSITORY}/releases',
            '-f',f'tag_name={tag}','-f',f'target_commitish={commit}','-F','draft=true',
            '-F',prerelease_field,'-f',latest_field,'-f',f'name={title}','-F',f'body=@{notes}'))
    if not isinstance(record,dict) or type(record.get('id')) is not int or record['id']<=0 or record.get('tag_name')!=tag:
        raise RuntimeError('Created release could not be located; preserve draft and inspect before retrying')
    release_id=record['id']
    endpoint=f'repos/{REPOSITORY}/releases/{release_id}'
    # Reject unknown names before a published release is hidden. This keeps the
    # explicit replacement path scoped to the installer set assembled above.
    release_assets(record,expected,obsolete)
    if not record.get('draft'):
        if not replacement_requested:
            raise RuntimeError('Refusing to replace an already published release')
        record=json.loads(run('gh','api','--method','PATCH',endpoint,
            '-F','draft=true','-F','prerelease=true','-f','make_latest=false','-f',f'target_commitish={commit}'))
    elif replace_published and record.get('prerelease') is not True:
        raise RuntimeError('Published replacement requires an existing prerelease')
    elif replace_stable and (record.get('prerelease') is not True or record.get('name') not in accepted_replacement_titles):
        raise RuntimeError('Stable replacement recovery requires the original stable release draft')
    if replacement_requested and tag_commit!=commit:
        write_release_tag(tag,commit,title,tag_ref)
        tag_ref,tag_commit=remote_tag_identity(tag)
    record=json.loads(run('gh','api',endpoint))
    if record.get('id')!=release_id or record.get('tag_name')!=tag or not record.get('draft'):
        raise RuntimeError('Release identity or draft state changed before upload')
    if replacement_requested:
        actual=release_assets(record,expected,obsolete)
        stale=[]
        for name,asset in actual.items():
            if name not in expected or not asset_matches(asset,expected[name]):
                asset_id=asset.get('id')
                if type(asset_id) is not int or asset_id<=0:
                    raise RuntimeError(f'Remote stale asset has no stable numeric ID: {name}')
                stale.append((name,asset_id))
        for _,asset_id in stale:
            run('gh','api','--method','DELETE',f'repos/{REPOSITORY}/releases/assets/{asset_id}')
        record=json.loads(run('gh','api',endpoint))
        actual=release_assets(record,expected)
        if any(not asset_matches(asset,expected[name]) for name,asset in actual.items()):
            raise RuntimeError('Remote stale assets remained after explicit deletion')
        missing=sorted(set(expected)-set(actual))
    else:
        missing=missing_release_assets(record,expected)
    for name in missing:
        # Upload by the retained numeric ID as well. No tag-to-draft lookup is
        # delegated to a second client, and existing bytes are never clobbered.
        upload=f'https://uploads.github.com/repos/{REPOSITORY}/releases/{release_id}/assets?name={quote(name,safe="")}'
        asset=json.loads(run('gh','api','--method','POST',upload,
            '-H','Content-Type: application/octet-stream','--input',str(dist/name)))
        if missing_release_assets({'assets':[asset]},{name:expected[name]}):
            raise RuntimeError('Asset upload response did not confirm the requested file')
    record=json.loads(run('gh','api',endpoint))
    if record.get('id')!=release_id or record.get('tag_name')!=tag or not record.get('draft'):
        raise RuntimeError('Release identity or draft state changed before publication')
    if missing_release_assets(record,expected):raise RuntimeError('Remote draft asset set is incomplete')
    run('gh','api','--method','PATCH',endpoint,'-F','draft=false','-F',prerelease_field,
        '-f',latest_field,'-f',f'target_commitish={commit}','-f',f'name={title}','-F',f'body=@{notes}')
    published=json.loads(run('gh','api',endpoint if prerelease else f'repos/{REPOSITORY}/releases/latest'))
    if (published.get('id')!=release_id or published['tag_name']!=tag or published['draft'] or
            published['prerelease']!=prerelease or published['name']!=title):
        raise RuntimeError('Published release identity/channel mismatch')
    if missing_release_assets(published,expected):raise RuntimeError('Published release asset set is incomplete')
    _,published_tag_commit=remote_tag_identity(tag)
    if published_tag_commit!=commit:
        raise RuntimeError('Published release tag does not match the verified source')
    if prerelease and json.loads(run('gh','api',f'repos/{REPOSITORY}/releases/latest')).get('id')!=previous_latest:
        raise RuntimeError('Prerelease unexpectedly changed the stable Latest release')
    print(published['html_url'])
    if summary:=os.environ.get('GITHUB_STEP_SUMMARY'):
        with open(summary,'a',encoding='utf-8') as stream:stream.write(f'## {PRODUCT} {version}\n\n{published["html_url"]}\n\nVerified source: `{commit}`\n')


def main() -> None:
    global ROOT
    parser=argparse.ArgumentParser()
    parser.add_argument('--input',type=Path,required=True)
    parser.add_argument('--dist',type=Path,required=True)
    parser.add_argument('--version',required=True)
    parser.add_argument('--commit',required=True)
    parser.add_argument('--publish',action='store_true')
    parser.add_argument('--prerelease',action='store_true',help='Publish as a prerelease without replacing stable Latest')
    parser.add_argument('--replace-published',action='store_true',help='Explicitly replace or promote an existing published prerelease after full artifact verification')
    parser.add_argument('--replace-stable',action='store_true',help='Explicitly replace the current stable Latest release after full artifact verification')
    parser.add_argument('--source-root',type=Path,default=ROOT,help='Verified build source checkout; does not change the artifact SHA')
    args=parser.parse_args()
    if args.replace_published and args.replace_stable:parser.error('replacement modes are mutually exclusive')
    if args.replace_stable and args.prerelease:parser.error('--replace-stable cannot be combined with --prerelease')
    if (args.replace_published or args.replace_stable) and not args.publish:parser.error('replacement requires --publish')
    ROOT=args.source_root.resolve()
    manifest=assemble(args.input.resolve(),args.dist.resolve(),args.version,args.commit,args.prerelease)
    print(f'Verified {len(manifest["assets"])} payloads across the verified target platforms')
    if args.publish:publish(args.dist.resolve(),args.version,args.commit,args.prerelease,args.replace_published,args.replace_stable)

if __name__=='__main__':main()
