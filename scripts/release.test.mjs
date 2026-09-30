import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { after, test } from 'node:test';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync, existsSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { appImageName, pinnedWailsVersion, prepareAssets, resolveWailsCommand, targetPaths, versionParts } from './package-gui.mjs';
import { assetNames, checkBinary, checkPackage, checksums, expectedAssets, publish, stageTarget, validateTag } from './release.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const testParent = path.join(root, 'artifacts', 'release-tests');
mkdirSync(testParent, { recursive: true });
const temp = mkdtempSync(path.join(testParent, 'run-'));
after(() => rmSync(temp, { recursive: true, force: true }));
const fixture = (name) => {
  const dir = path.join(temp, name);
  mkdirSync(dir, { recursive: true });
  return dir;
};
const execute = (command, args, options = {}) => execFileSync(command, args, { encoding: 'utf8', ...options });

test('정상 버전과 prerelease를 표시 버전·숫자 버전으로 나눈다', () => {
  assert.deepEqual(versionParts('1.2.3'), { full: '1.2.3', numeric: '1.2.3' });
  assert.deepEqual(versionParts('1.2.3-rc.1'), { full: '1.2.3-rc.1', numeric: '1.2.3' });
  for (const value of ['v1.2.3', '1.2', '01.2.3', '1.2.3-rc..1', '1.2.3-01', '65536.0.0', '1.2.3;echo bad']) {
    assert.throws(() => versionParts(value));
  }
});

test('Wails 버전의 stderr·색상 출력을 처리하고 다른 버전은 거절한다', () => {
  assert.equal(pinnedWailsVersion('\n\u001b[32mv3.0.0-beta.11\u001b[0m\n'), true);
  assert.equal(pinnedWailsVersion('Wails CLI v3.0.0-beta.12\n'), false);
  assert.equal(pinnedWailsVersion('v3.0.0-beta.111\n'), false);
  assert.equal(pinnedWailsVersion(''), false);
});

test('Wails 경로는 호출 위치에 고정하고 PATH 명령은 유지한다', () => {
  const cwd = fixture('command root with spaces');
  const absolute = path.join(cwd, 'tools with spaces', 'wails3');
  assert.equal(resolveWailsCommand('wails3', cwd), 'wails3');
  assert.equal(resolveWailsCommand('wails3'), 'wails3');
  assert.equal(resolveWailsCommand('./tools with spaces/wails3', cwd), absolute);
  assert.equal(resolveWailsCommand(absolute, fixture('different cwd')), absolute);
  assert.equal(resolveWailsCommand('tools\\wails3', cwd), path.resolve(cwd, 'tools\\wails3'));
});

test('상대·절대·bare Wails 경로가 GUI cwd와 내부 PATH 실행에서도 같은 도구를 유지한다', {
  skip: process.platform === 'win32' ? '실행 파일 fixture는 POSIX shebang을 사용한다.' : false,
}, () => {
  const cwd = fixture('selected Wails with spaces');
  const gui = path.join(cwd, 'cmd', 'pulsemetry-gui');
  const source = path.join(gui, 'build');
  const selectedDir = path.join(cwd, 'tools with spaces');
  const competingDir = path.join(cwd, 'competing tools');
  mkdirSync(path.join(source, 'windows'), { recursive: true });
  mkdirSync(selectedDir, { recursive: true });
  mkdirSync(competingDir, { recursive: true });
  const original = '{"fixed":{"file_version":"0.1.0"},"info":{"0000":{"ProductVersion":"0.1.0"}}}\n';
  writeFileSync(path.join(source, 'windows', 'info.json'), original);
  writeFileSync(path.join(source, 'config.yml'), 'info:\n  version: "0.1.0"\n');
  writeFileSync(path.join(source, 'appicon.png'), 'source icon');
  const log = path.join(cwd, 'wails.log');
  const selected = path.join(selectedDir, 'wails3');
  writeFileSync(selected, `#!${process.execPath}
import { appendFileSync, readFileSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import path from 'node:path';
const args = process.argv.slice(2);
appendFileSync(process.env.FAKE_WAILS_LOG, JSON.stringify({ args, cwd: process.cwd(), tool: process.argv[1] }) + '\\n');
if (args[0] === 'version') {
  console.error('v3.0.0-beta.11');
} else if (args[0] === 'update') {
  const assets = args[args.indexOf('-dir') + 1];
  const numeric = args[args.indexOf('-productversion') + 1];
  writeFileSync(path.join(assets, 'windows', 'info.json'), JSON.stringify({ fixed: { file_version: numeric }, info: { '0000': { ProductVersion: numeric } } }));
} else if (args[0] === 'task' && args.includes('common:generate:icons')) {
  const assets = args.find((arg) => arg.startsWith('ASSETS_DIR=')).slice('ASSETS_DIR='.length);
  writeFileSync(path.join(assets, 'windows', 'icon.ico'), readFileSync(path.join(assets, 'appicon.png')));
} else if (args[0] === 'task') {
  const result = spawnSync('wails3', ['probe'], { env: process.env, stdio: 'inherit' });
  if (result.error) throw result.error;
  process.exit(result.status);
} else if (args[0] !== 'probe') {
  throw new Error('unexpected Wails command');
}
`, { mode: 0o755 });
  writeFileSync(path.join(competingDir, 'wails3'), `#!${process.execPath}
import { appendFileSync } from 'node:fs';
appendFileSync(process.env.FAKE_WAILS_LOG, JSON.stringify({ tool: 'wrong Wails command' }) + '\\n');
process.exit(99);
`, { mode: 0o755 });
  const [os, arch] = execute('go', ['env', 'GOHOSTOS', 'GOHOSTARCH']).trim().split(/\r?\n/);
  const driver = path.join(cwd, 'driver.mjs');
  const moduleURL = pathToFileURL(path.join(root, 'scripts', 'package-gui.mjs')).href;
  writeFileSync(driver, `import { packageGUI } from ${JSON.stringify(moduleURL)};
packageGUI('build', { root: process.cwd(), os: ${JSON.stringify(os)}, arch: ${JSON.stringify(arch)}, version: '1.2.3-rc.1', wails: process.argv[2] });
`);
  for (const command of ['./tools with spaces/wails3', selected, 'wails3']) {
    const firstPath = command === 'wails3' ? selectedDir : competingDir;
    const result = spawnSync(process.execPath, [driver, command], {
      cwd, encoding: 'utf8', env: { ...process.env, FAKE_WAILS_LOG: log, PATH: `${firstPath}${path.delimiter}${process.env.PATH}` },
    });
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.stdout, '');
    assert.equal(result.stderr, '');
  }
  const calls = readFileSync(log, 'utf8').trim().split('\n').map(JSON.parse);
  assert.equal(calls.every((call) => call.tool === selected), true);
  const versionCalls = calls.filter((call) => call.args[0] === 'version');
  assert.equal(versionCalls.length, 3);
  assert.equal(versionCalls.every((call) => call.cwd === cwd), true);
  assert.equal(calls.filter((call) => call.args[0] === 'probe').length, 3);
  assert.equal(calls.filter((call) => call.args[0] !== 'version').every((call) => call.cwd === gui), true);
  assert.equal(readFileSync(path.join(source, 'windows', 'info.json'), 'utf8'), original);
});

test('고정 Wails 버전의 AppImage 출력명은 Linux에서 소문자 경로로 찾는다', () => {
  assert.equal(appImageName('amd64'), 'pulsemetry-x86_64.AppImage');
  assert.equal(appImageName('arm64'), 'pulsemetry-aarch64.AppImage');
});

test('실제 Git 저장소에서 main 포함 커밋만 릴리스하고 잘못된 태그는 출력 없이 실패한다', () => {
  const cwd = fixture('git repo with spaces');
  const git = (...args) => execute('git', args, { cwd });
  git('init', '--initial-branch=main');
  git('config', 'user.name', 'Release Test');
  git('config', 'user.email', 'release-test@example.invalid');
  writeFileSync(path.join(cwd, 'fixture.txt'), 'main\n');
  git('add', 'fixture.txt');
  git('commit', '-m', 'fixture');
  git('update-ref', 'refs/remotes/origin/main', 'HEAD');
  assert.equal(validateTag('v1.2.3', { cwd }), '1.2.3');
  const script = path.join(root, 'scripts', 'release.mjs');
  const valid = spawnSync(process.execPath, [script, 'validate-tag', 'v1.2.3-rc.1'], { cwd, encoding: 'utf8' });
  assert.equal(valid.status, 0, valid.stderr);
  assert.equal(valid.stdout, '1.2.3-rc.1\n');
  assert.equal(valid.stderr, '');
  for (const tag of ['release/v1.2.3', 'v1.2', 'v1.2.3-rc..1']) {
    const invalid = spawnSync(process.execPath, [script, 'validate-tag', tag], { cwd, encoding: 'utf8' });
    assert.equal(invalid.status, 1);
    assert.equal(invalid.stdout, '');
    assert.notEqual(invalid.stderr, '');
  }
  writeFileSync(path.join(cwd, 'fixture.txt'), 'feature\n');
  git('add', 'fixture.txt');
  git('commit', '-m', 'feature fixture');
  const unmerged = spawnSync(process.execPath, [script, 'validate-tag', 'v1.2.3'], { cwd, encoding: 'utf8' });
  assert.equal(unmerged.status, 1);
  assert.equal(unmerged.stdout, '');
});

test('제품 메타데이터를 복사본에만 생성하고 prerelease 표시 버전을 보존한다', () => {
  const dir = fixture('metadata');
  const source = path.join(dir, 'cmd', 'pulsemetry-gui', 'build');
  mkdirSync(path.join(source, 'windows'), { recursive: true });
  const original = '{"fixed":{"file_version":"0.1.0"},"info":{"0000":{"ProductVersion":"0.1.0"}}}\n';
  writeFileSync(path.join(source, 'windows', 'info.json'), original);
  writeFileSync(path.join(source, 'config.yml'), 'info:\n  version: "0.1.0"\n');
  const calls = [];
  const dirs = prepareAssets(dir, 'windows', 'arm64', '2.3.4-rc.2', (args) => {
    calls.push(args);
    if (args[0] === 'update') {
      assert.equal(args[args.indexOf('-productversion') + 1], '2.3.4');
      const assets = args[args.indexOf('-dir') + 1];
      writeFileSync(path.join(assets, 'windows', 'info.json'), '{"fixed":{"file_version":"2.3.4"},"info":{"0000":{"ProductVersion":"2.3.4"}}}');
    } else {
      assert.deepEqual(args.slice(0, 3), ['task', '-f', 'common:generate:icons']);
      const assets = args[3].slice('ASSETS_DIR='.length);
      const info = JSON.parse(readFileSync(path.join(assets, 'windows', 'info.json')));
      assert.equal(info.info['0000'].ProductVersion, '2.3.4-rc.2');
    }
  });
  assert.equal(calls.length, 2);
  assert.equal(readFileSync(path.join(source, 'windows', 'info.json'), 'utf8'), original);
  const generated = JSON.parse(readFileSync(path.join(dirs.assets, 'windows', 'info.json')));
  assert.equal(generated.fixed.file_version, '2.3.4');
  assert.equal(generated.info['0000'].ProductVersion, '2.3.4-rc.2');
});

test('staging을 다시 복사해도 현재 입력으로 아이콘을 매번 생성하고 원본 자산은 보존한다', () => {
  const dir = fixture('regenerated icons');
  const source = path.join(dir, 'cmd', 'pulsemetry-gui', 'build');
  mkdirSync(path.join(source, 'windows'), { recursive: true });
  mkdirSync(path.join(source, 'darwin'), { recursive: true });
  const originalInfo = '{"fixed":{"file_version":"0.1.0"},"info":{"0000":{"ProductVersion":"0.1.0"}}}\n';
  const originalConfig = 'info:\n  version: "0.1.0"\n';
  writeFileSync(path.join(source, 'windows', 'info.json'), originalInfo);
  writeFileSync(path.join(source, 'config.yml'), originalConfig);
  writeFileSync(path.join(source, 'windows', 'icon.ico'), 'old source ico');
  writeFileSync(path.join(source, 'darwin', 'icons.icns'), 'old source icns');
  const calls = [];
  const runWails = (args) => {
    calls.push(args[0]);
    if (args[0] === 'update') return;
    assert.deepEqual(args.slice(0, 3), ['task', '-f', 'common:generate:icons']);
    assert.equal(args[3].startsWith('ASSETS_DIR='), true);
    const assets = args[3].slice('ASSETS_DIR='.length);
    assert.equal(readFileSync(path.join(assets, 'windows', 'icon.ico'), 'utf8'), 'old source ico');
    const currentIcon = readFileSync(path.join(assets, 'appicon.png'), 'utf8');
    writeFileSync(path.join(assets, 'windows', 'icon.ico'), `ico from ${currentIcon}`);
    writeFileSync(path.join(assets, 'darwin', 'icons.icns'), `icns from ${currentIcon}`);
  };
  for (const currentIcon of ['first input', 'second input']) {
    writeFileSync(path.join(source, 'appicon.png'), currentIcon);
    const dirs = prepareAssets(dir, 'darwin', 'arm64', '1.2.3', runWails);
    assert.equal(readFileSync(path.join(dirs.assets, 'windows', 'icon.ico'), 'utf8'), `ico from ${currentIcon}`);
    assert.equal(readFileSync(path.join(dirs.assets, 'darwin', 'icons.icns'), 'utf8'), `icns from ${currentIcon}`);
    assert.equal(readFileSync(path.join(source, 'appicon.png'), 'utf8'), currentIcon);
    assert.equal(readFileSync(path.join(source, 'windows', 'icon.ico'), 'utf8'), 'old source ico');
    assert.equal(readFileSync(path.join(source, 'darwin', 'icons.icns'), 'utf8'), 'old source icns');
    assert.equal(readFileSync(path.join(source, 'windows', 'info.json'), 'utf8'), originalInfo);
    assert.equal(readFileSync(path.join(source, 'config.yml'), 'utf8'), originalConfig);
  }
  assert.deepEqual(calls, ['update', 'task', 'update', 'task']);
});

test('12개 새 파일명만 허용하고 체크섬 변경·누락·빈 파일을 거절한다', () => {
  assert.equal(expectedAssets().length, 12);
  assert.equal(new Set(expectedAssets()).size, 12);
  assert.deepEqual(assetNames('linux', 'arm64'), { cli: 'pulsemetry_cli_linux_arm64', gui: 'pulsemetry_gui_linux_arm64.AppImage' });
  const dir = fixture('checksums');
  for (const name of expectedAssets()) writeFileSync(path.join(dir, name), `${name}\n`);
  checksums(dir);
  checksums(dir, true);
  assert.equal(readFileSync(path.join(dir, 'SHA256SUMS'), 'utf8').trim().split('\n').length, 12);
  const name = expectedAssets()[0];
  writeFileSync(path.join(dir, name), 'tampered');
  assert.throws(() => checksums(dir, true), /체크섬/);
  writeFileSync(path.join(dir, name), '');
  assert.throws(() => checksums(dir), /비어/);
  rmSync(path.join(dir, name));
  assert.throws(() => checksums(dir), /목록/);
  writeFileSync(path.join(dir, name), 'restored');
  writeFileSync(path.join(dir, 'pulsemetry_linux_amd64'), 'legacy');
  assert.throws(() => checksums(dir), /목록/);
});

test('바이너리의 실제 대상·버전·CLI CGO 설정이 어긋나면 파일 배치를 중단한다', () => {
  const dir = fixture('stage');
  const dirs = targetPaths(dir, 'linux', 'arm64');
  mkdirSync(path.join(dirs.build, 'bin'), { recursive: true });
  mkdirSync(dirs.packageDir, { recursive: true });
  const cli = path.join(dirs.build, 'bin', 'pulsemetry');
  const gui = path.join(dirs.build, 'Pulsemetry');
  writeFileSync(cli, 'cli');
  writeFileSync(gui, 'gui');
  writeFileSync(path.join(dirs.packageDir, 'Pulsemetry.AppImage'), 'package');
  let goarch = 'amd64';
  let cgo = '0';
  const fake = (command, args) => {
    if (command === cli) return 'pulsemetry 1.2.3\n';
    if (command === gui) return 'pulsemetry-gui 1.2.3\n';
    assert.equal(command, 'go');
    return `\tbuild\tGOOS=linux\n\tbuild\tGOARCH=${goarch}\n\tbuild\tCGO_ENABLED=${cgo}\n`;
  };
  const stage = path.join(dir, 'artifacts', 'release', 'linux-arm64');
  assert.throws(() => stageTarget(dir, 'linux', 'arm64', '1.2.3', { execute: fake, inspectPackage() {} }), /GOARCH/);
  assert.equal(existsSync(stage), false);
  goarch = 'arm64';
  cgo = '1';
  assert.throws(() => checkBinary(cli, 'linux', 'arm64', '1.2.3', true, fake), /CGO_ENABLED/);
  cgo = '0';
  assert.throws(() => stageTarget(dir, 'linux', 'arm64', '1.2.3', { execute: fake, inspectPackage() { throw new Error('잘못된 패키지'); } }), /잘못된/);
  assert.equal(existsSync(stage), false);
  stageTarget(dir, 'linux', 'arm64', '1.2.3', { execute: fake, inspectPackage() {} });
  assert.equal(readFileSync(path.join(stage, 'pulsemetry_cli_linux_arm64'), 'utf8'), 'cli');
  assert.equal(readFileSync(path.join(stage, 'pulsemetry_gui_linux_arm64.AppImage'), 'utf8'), 'package');
});

test('가짜 gh 프로세스로 draft 업로드 성공 후 공개하고 실패·기존 버전에는 공개하지 않는다', () => {
  const dir = fixture('publish assets');
  for (const name of expectedAssets()) writeFileSync(path.join(dir, name), name);
  checksums(dir);
  const fake = path.join(temp, 'fake-gh.mjs');
  const log = path.join(temp, 'gh.log');
  writeFileSync(fake, `import { appendFileSync } from 'node:fs';
appendFileSync(process.env.FAKE_GH_LOG, JSON.stringify(process.argv.slice(2)) + '\\n');
if (process.argv[2] === 'api') {
  if (process.env.FAKE_GH_FAIL === 'lookup') process.exit(1);
  if (process.env.FAKE_GH_EXISTING) console.log(process.env.FAKE_GH_EXISTING);
}
if (process.argv[3] === 'create' && process.env.FAKE_GH_FAIL === 'upload') process.exit(1);
`);
  const gh = (command, args) => {
    assert.equal(command, 'gh');
    return execute(process.execPath, [fake, ...args], { env: { ...process.env, FAKE_GH_LOG: log } });
  };
  publish('v1.2.3-rc.1', dir, gh);
  const calls = readFileSync(log, 'utf8').trim().split('\n').map(JSON.parse);
  assert.equal(calls.length, 3);
  assert.deepEqual(calls[0], ['api', '--paginate', 'repos/{owner}/{repo}/releases?per_page=100', '--jq', '.[].tag_name']);
  assert.deepEqual(calls[1].slice(0, 3), ['release', 'create', 'v1.2.3-rc.1']);
  assert.equal(calls[1].filter((arg) => arg.startsWith(dir)).length, 13);
  assert.equal(calls[1].includes('--draft'), true);
  assert.equal(calls[1].includes('--verify-tag'), true);
  assert.equal(calls[1].includes('--prerelease'), true);
  assert.deepEqual(calls[2], ['release', 'edit', 'v1.2.3-rc.1', '--draft=false']);
  writeFileSync(log, '');
  assert.throws(() => publish('v1.2.3', dir, (command, args) => execute(process.execPath, [fake, ...args], {
    env: { ...process.env, FAKE_GH_LOG: log, FAKE_GH_FAIL: 'upload' },
  })));
  const failed = readFileSync(log, 'utf8').trim().split('\n').map(JSON.parse);
  assert.equal(failed.length, 2);
  assert.equal(failed[1].includes('--prerelease'), false);
  for (const scenario of [
    { FAKE_GH_EXISTING: 'v0.9.0\nv1.2.3' },
    { FAKE_GH_FAIL: 'lookup' },
  ]) {
    writeFileSync(log, '');
    assert.throws(() => publish('v1.2.3', dir, (command, args) => execute(process.execPath, [fake, ...args], {
      env: { ...process.env, FAKE_GH_LOG: log, ...scenario },
    })));
    const blocked = readFileSync(log, 'utf8').trim().split('\n').map(JSON.parse);
    assert.equal(blocked.length, 1);
    assert.equal(blocked[0][0], 'api');
  }
  writeFileSync(log, '');
  writeFileSync(path.join(dir, expectedAssets()[0]), 'tampered');
  assert.throws(() => publish('v1.2.3', dir, gh), /체크섬/);
  assert.equal(readFileSync(log, 'utf8'), '');
});

test('패키지에 CLI·다른 GUI 빌드·다른 버전이 있으면 거절하고 추출물을 정리한다', () => {
  const dir = fixture('package validation');
  const dirs = targetPaths(dir, 'linux', 'amd64');
  mkdirSync(dirs.build, { recursive: true });
  mkdirSync(dirs.packageDir, { recursive: true });
  const original = path.join(dirs.build, 'Pulsemetry');
  const pkg = path.join(dirs.packageDir, 'Pulsemetry.AppImage');
  writeFileSync(original, 'current build');
  writeFileSync(pkg, 'package');
  let includeCLI = false;
  let packageBuildID = 'current-build';
  let packageVersion = '1.2.3';
  const fake = (command, args, options) => {
    if (command === pkg) {
      assert.deepEqual(args, ['--appimage-extract']);
      const bin = path.join(options.cwd, 'squashfs-root', 'usr', 'bin');
      mkdirSync(bin, { recursive: true });
      writeFileSync(path.join(bin, 'Pulsemetry'), 'packaged build');
      if (includeCLI) {
        const daemon = path.join(bin, 'daemon');
        mkdirSync(daemon, { recursive: true });
        writeFileSync(path.join(daemon, 'pulsemetry'), 'CLI');
      }
      return '';
    }
    if (command === 'go' && args[0] === 'version') {
      return '\tbuild\tGOOS=linux\n\tbuild\tGOARCH=amd64\n\tbuild\tCGO_ENABLED=1\n';
    }
    if (command === 'go') {
      assert.deepEqual(args.slice(0, 2), ['run', 'cmd/buildid']);
      return args[2] === original ? 'current-build\n' : `${packageBuildID}\n`;
    }
    assert.equal(path.basename(command), 'Pulsemetry');
    assert.deepEqual(args, ['--version']);
    return `pulsemetry-gui ${packageVersion}\n`;
  };
  checkPackage(pkg, 'linux', 'amd64', '1.2.3', dirs, fake);
  includeCLI = true;
  assert.throws(() => checkPackage(pkg, 'linux', 'amd64', '1.2.3', dirs, fake), /CLI/);
  includeCLI = false;
  packageBuildID = 'old-build';
  assert.throws(() => checkPackage(pkg, 'linux', 'amd64', '1.2.3', dirs, fake), /이번 빌드/);
  packageBuildID = 'current-build';
  packageVersion = '1.2.2';
  assert.throws(() => checkPackage(pkg, 'linux', 'amd64', '1.2.3', dirs, fake), /버전/);
  assert.equal(existsSync(path.join(dirs.packageDir, 'verify')), false);
});
