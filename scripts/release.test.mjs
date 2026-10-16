import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { after, test } from 'node:test';
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync, existsSync, realpathSync, statSync, symlinkSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { appImageName, pinnedWailsVersion, prepareAssets, resolveWailsCommand, targetPaths, versionParts } from './package-gui.mjs';
import { assetNames, checkBinary, checkPackage, checksums, expectedAssets, publish, stageTarget, validateTag } from './release.mjs';
import { prepareAppImagePatchelf } from './appimage-patchelf.mjs';

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
const posixOnly = process.platform === 'win32' ? '실행 파일 fixture는 POSIX shebang을 사용한다.' : false;
const writeNodeTool = (filename, source) => writeFileSync(filename, `#!${process.execPath}\n${source}`, { mode: 0o755 });
const loggedCalls = (filename) => existsSync(filename) ? readFileSync(filename, 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse) : [];
const fakePatchelf = (filename) => writeNodeTool(filename, `
import { appendFileSync } from 'node:fs';
const args = process.argv.slice(2);
if (args[0] === '--help') {
  const output = process.env.FAKE_PATCHELF_UNSUPPORTED === '1' ? 'patchelf usage\\n' : 'patchelf usage --no-sort\\n';
  (process.env.FAKE_PATCHELF_HELP_STDERR === '1' ? process.stderr : process.stdout).write(output);
} else {
  appendFileSync(process.env.FAKE_PATCHELF_LOG, JSON.stringify({ args, cwd: process.cwd(), tool: process.argv[1] }) + '\\n');
  process.stdout.write('patchelf stdout\\n');
  process.stderr.write('patchelf stderr\\n');
  if (process.env.FAKE_PATCHELF_SIGNAL) process.kill(process.pid, process.env.FAKE_PATCHELF_SIGNAL);
  else process.exit(Number(process.env.FAKE_PATCHELF_STATUS || 0));
}
`);

const fakePackaging = (name) => {
  const cwd = fixture(name);
  const gui = path.join(cwd, 'cmd', 'pulsemetry-gui');
  const source = path.join(gui, 'build');
  const tools = path.join(cwd, "tools with 'quotes' and spaces");
  mkdirSync(path.join(source, 'windows'), { recursive: true });
  mkdirSync(tools, { recursive: true });
  const originalInfo = '{"fixed":{"file_version":"0.1.0"},"info":{"0000":{"ProductVersion":"0.1.0"}}}\n';
  writeFileSync(path.join(source, 'windows', 'info.json'), originalInfo);
  writeFileSync(path.join(source, 'config.yml'), 'info:\n  version: "0.1.0"\n');
  writeFileSync(path.join(source, 'appicon.png'), 'source icon');
  const patchelf = path.join(tools, 'selected-patchelf.mjs');
  fakePatchelf(patchelf);
  writeNodeTool(path.join(tools, 'go'), `
if (JSON.stringify(process.argv.slice(2)) !== JSON.stringify(['env', 'GOHOSTOS', 'GOHOSTARCH'])) throw new Error('unexpected Go command');
process.stdout.write(process.env.FAKE_HOST_OS + '\\n' + process.env.FAKE_HOST_ARCH + '\\n');
`);
  const wails = path.join(tools, 'wails3');
  writeNodeTool(wails, `
import { appendFileSync, mkdirSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import path from 'node:path';
const args = process.argv.slice(2);
appendFileSync(process.env.FAKE_WAILS_LOG, JSON.stringify({ args, cwd: process.cwd(), patchelf: process.env.PATCHELF || null }) + '\\n');
if (args[0] === 'version') {
  console.error('v3.0.0-beta.11');
} else if (args[0] === 'update') {
  const assets = args[args.indexOf('-dir') + 1];
  const numeric = args[args.indexOf('-productversion') + 1];
  writeFileSync(path.join(assets, 'windows', 'info.json'), JSON.stringify({ fixed: { file_version: numeric }, info: { '0000': { ProductVersion: numeric } } }));
} else if (args[0] === 'task' && args.includes('common:generate:icons')) {
  const assets = args.find((arg) => arg.startsWith('ASSETS_DIR=')).slice('ASSETS_DIR='.length);
  writeFileSync(path.join(assets, 'windows', 'icon.ico'), 'generated icon');
} else if (args[0] === 'task') {
  const value = (key) => args.find((arg) => arg.startsWith(key + '=')).slice(key.length + 1);
  const packageDir = value('PACKAGE_DIR');
  const build = value('BIN_DIR');
  if (args[1] === 'linux:create:appimage') {
    const arch = value('ARCH') === 'amd64' ? 'x86_64' : 'aarch64';
    const gui = path.join(packageDir, 'appimage', 'build', 'pulsemetry-' + arch + '.AppDir', 'usr', 'bin', 'Pulsemetry');
    mkdirSync(path.dirname(gui), { recursive: true });
    writeFileSync(gui, 'packaged GUI');
    const result = spawnSync(process.env.PATCHELF, ['--set-rpath', '$ORIGIN/../lib', gui], { env: process.env, encoding: 'utf8' });
    if (result.error) throw result.error;
    if (result.status !== 0) process.exit(result.status || 1);
    writeFileSync(path.join(packageDir, 'pulsemetry-' + arch + '.AppImage'), 'new AppImage');
  } else if (args[1] === 'darwin:package:dmg') {
    writeFileSync(path.join(build, 'Pulsemetry.dmg'), 'new DMG');
  } else if (args[1] === 'windows:package') {
    writeFileSync(path.join(packageDir, 'Pulsemetry.exe'), 'new installer');
  } else if (args[1] !== 'build') throw new Error('unexpected Wails task');
} else throw new Error('unexpected Wails command');
`);
  const driver = path.join(cwd, 'driver.mjs');
  const moduleURL = pathToFileURL(path.join(root, 'scripts', 'package-gui.mjs')).href;
  writeFileSync(driver, `import { packageGUI } from ${JSON.stringify(moduleURL)};
const before = process.env.PATCHELF;
try {
  packageGUI(process.argv[2], { root: process.cwd(), os: process.env.FAKE_HOST_OS, arch: process.env.FAKE_HOST_ARCH, version: '1.2.3-rc.1', wails: process.argv[3] });
  if (process.env.PATCHELF !== before) throw new Error('PATCHELF environment was mutated');
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
}
`);
  const wailsLog = path.join(cwd, 'wails.log');
  const patchelfLog = path.join(cwd, 'patchelf.log');
  const run = (os, arch, mode, extraEnv = {}) => spawnSync(process.execPath, [driver, mode, wails], {
    cwd, encoding: 'utf8', env: {
      ...process.env, FAKE_HOST_OS: os, FAKE_HOST_ARCH: arch, FAKE_WAILS_LOG: wailsLog,
      FAKE_PATCHELF_LOG: patchelfLog, PATCHELF: path.relative(cwd, patchelf),
      PATH: `${tools}${path.delimiter}${process.env.PATH}`, ...extraEnv,
    },
  });
  return { cwd, source, originalInfo, patchelf, wailsLog, patchelfLog, run };
};

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

test('patchelf 경로를 고정하고 정확한 GUI RPATH 변경에만 --no-sort를 전달한다', { skip: posixOnly }, () => {
  const cwd = fixture("patchelf 'quoted' $root with spaces");
  const tools = path.join(cwd, "tools with 'quotes'");
  mkdirSync(tools);
  const tool = path.join(tools, 'real-patchelf.mjs');
  const alias = path.join(tools, 'patchelf');
  fakePatchelf(tool);
  symlinkSync(tool, alias);
  const gui = path.join(cwd, 'AppDir', 'usr', 'bin', 'Pulsemetry');
  mkdirSync(path.dirname(gui), { recursive: true });
  writeFileSync(gui, 'GUI');
  const guiAlias = path.join(cwd, 'GUI symlink');
  symlinkSync(gui, guiAlias);
  const otherGUI = path.join(fixture('other AppDir'), 'Pulsemetry');
  writeFileSync(otherGUI, 'different GUI');
  const library = path.join(cwd, 'AppDir', 'libexample.so');
  writeFileSync(library, 'library');
  const log = path.join(cwd, 'patchelf.log');
  const env = { ...process.env, FAKE_PATCHELF_LOG: log, PATH: `${tools}${path.delimiter}${process.env.PATH}` };
  const otherCwd = fixture('different patchelf cwd');
  const rpath = "$ORIGIN/../lib with 'quotes' and $(literal)";
  const cases = [
    { args: ['--set-rpath', rpath, gui], protected: true },
    { args: ['--set-rpath', rpath, guiAlias], protected: true },
    { args: ['--set-rpath', rpath, path.relative(otherCwd, gui)], protected: true },
    { args: ['--no-sort', '--set-rpath', rpath, gui], protected: true },
    { args: ['--print-rpath', gui], protected: false },
    { args: ['--set-rpath', rpath, otherGUI], protected: false },
    { args: ['--set-rpath', rpath, library], protected: false },
    { args: ['--version'], protected: false },
  ];
  for (const [index, command] of [tool, path.relative(cwd, alias), 'patchelf'].entries()) {
    const packageDir = path.join(cwd, `package ${index}`);
    const launcher = prepareAppImagePatchelf(packageDir, gui, { command, cwd, env: { ...env, FAKE_PATCHELF_HELP_STDERR: index === 1 ? '1' : '0' } });
    assert.equal(path.isAbsolute(launcher), true);
    for (const scenario of cases) {
      const result = spawnSync(launcher, scenario.args, { cwd: otherCwd, env, encoding: 'utf8' });
      assert.equal(result.status, 0, result.stderr);
      assert.equal(result.stdout, 'patchelf stdout\n');
      assert.equal(result.stderr, 'patchelf stderr\n');
      const call = loggedCalls(log).at(-1);
      assert.equal(call.tool, realpathSync(tool));
      assert.equal(call.cwd, otherCwd);
      assert.deepEqual(call.args.filter((arg) => arg !== '--no-sort'), scenario.args.filter((arg) => arg !== '--no-sort'));
      assert.equal(call.args.filter((arg) => arg === '--no-sort').length, scenario.protected ? 1 : 0);
    }
  }
  assert.equal(readFileSync(gui, 'utf8'), 'GUI');
});

test('patchelf wrapper가 출력·실패 종료코드·종료 시그널을 보존한다', { skip: posixOnly }, () => {
  const cwd = fixture('patchelf failure forwarding');
  const tool = path.join(cwd, 'patchelf.mjs');
  fakePatchelf(tool);
  const gui = path.join(cwd, 'Pulsemetry');
  writeFileSync(gui, 'GUI');
  const env = { ...process.env, FAKE_PATCHELF_LOG: path.join(cwd, 'patchelf.log') };
  const launcher = prepareAppImagePatchelf(path.join(cwd, 'package'), gui, { command: tool, cwd, env });
  const failed = spawnSync(launcher, ['--set-rpath', '$ORIGIN', gui], { env: { ...env, FAKE_PATCHELF_STATUS: '23' }, encoding: 'utf8' });
  assert.equal(failed.status, 23, failed.stderr);
  assert.equal(failed.stdout, 'patchelf stdout\n');
  assert.equal(failed.stderr, 'patchelf stderr\n');
  const signaled = spawnSync(launcher, ['--set-rpath', '$ORIGIN', gui], { env: { ...env, FAKE_PATCHELF_SIGNAL: 'SIGTERM' }, encoding: 'utf8' });
  assert.equal(signaled.status, null);
  assert.equal(signaled.signal, 'SIGTERM', signaled.stderr);
});

test('없는·실행 불가능한·--no-sort 미지원 patchelf는 staging 생성 전에 거절한다', { skip: posixOnly }, () => {
  const cwd = fixture('patchelf preflight failures');
  const tool = path.join(cwd, 'patchelf.mjs');
  fakePatchelf(tool);
  const blocked = path.join(cwd, 'nonexecutable-patchelf');
  writeFileSync(blocked, 'not executable');
  chmodSync(blocked, 0o644);
  const scenarios = [
    { command: path.join(cwd, 'missing-patchelf') },
    { command: 'missing-patchelf', env: { PATH: cwd } },
    { command: blocked },
    { command: tool, env: { FAKE_PATCHELF_UNSUPPORTED: '1' } },
  ];
  for (const [index, scenario] of scenarios.entries()) {
    const packageDir = path.join(cwd, `package ${index}`);
    assert.throws(() => prepareAppImagePatchelf(packageDir, path.join(cwd, 'Pulsemetry'), {
      cwd, command: scenario.command, env: { ...process.env, ...scenario.env },
    }), (error) => /patchelf/i.test(error.message) && /[가-힣]/.test(error.message));
    assert.equal(existsSync(packageDir), false);
  }
});

test('생성한 launcher를 실제 patchelf로 재사용하지 않고 재생성 시 실행 권한을 복구한다', { skip: posixOnly }, () => {
  const cwd = fixture('patchelf launcher reuse');
  const tool = path.join(cwd, 'patchelf.mjs');
  fakePatchelf(tool);
  const packageDir = path.join(cwd, 'package');
  const gui = path.join(cwd, 'Pulsemetry');
  const launcher = prepareAppImagePatchelf(packageDir, gui, { command: tool, cwd });
  const original = readFileSync(launcher, 'utf8');
  const alias = path.join(cwd, 'launcher alias');
  symlinkSync(launcher, alias);
  for (const command of [launcher, alias]) {
    assert.throws(() => prepareAppImagePatchelf(packageDir, gui, { command, cwd }), /실제 patchelf/);
    assert.equal(readFileSync(launcher, 'utf8'), original);
  }
  chmodSync(launcher, 0o644);
  assert.equal(prepareAppImagePatchelf(packageDir, gui, { command: tool, cwd }), launcher);
  assert.equal(statSync(launcher).mode & 0o777, 0o755);
  assert.equal(readFileSync(launcher, 'utf8'), original);
});

test('Linux 두 아키텍처의 패키징 task에만 PATCHELF wrapper를 배선한다', { skip: posixOnly }, () => {
  for (const arch of ['amd64', 'arm64']) {
    const setup = fakePackaging(`Linux ${arch} packaging with spaces`);
    const originalPatchelf = path.relative(setup.cwd, setup.patchelf);
    const result = setup.run('linux', arch, 'package');
    assert.equal(result.status, 0, result.stderr);
    assert.equal(result.stdout, '');
    assert.equal(result.stderr, '');
    const calls = loggedCalls(setup.wailsLog);
    const packageCall = calls.find((call) => call.args[1] === 'linux:create:appimage');
    assert.equal(path.isAbsolute(packageCall.patchelf), true);
    assert.notEqual(packageCall.patchelf, originalPatchelf);
    assert.equal(calls.filter((call) => call !== packageCall).every((call) => call.patchelf === originalPatchelf), true);
    const dirs = targetPaths(setup.cwd, 'linux', arch);
    const packagedGUI = path.join(dirs.packageDir, 'appimage', 'build', appImageName(arch).replace(/\.AppImage$/, '.AppDir'), 'usr', 'bin', 'Pulsemetry');
    const patchCalls = loggedCalls(setup.patchelfLog);
    assert.equal(patchCalls.length, 1);
    assert.deepEqual(patchCalls[0].args.filter((arg) => arg !== '--no-sort'), ['--set-rpath', '$ORIGIN/../lib', packagedGUI]);
    assert.equal(patchCalls[0].args.filter((arg) => arg === '--no-sort').length, 1);
    assert.equal(patchCalls[0].tool, realpathSync(setup.patchelf));
    assert.equal(readFileSync(path.join(dirs.packageDir, 'Pulsemetry.AppImage'), 'utf8'), 'new AppImage');
    assert.equal(readFileSync(path.join(setup.source, 'windows', 'info.json'), 'utf8'), setup.originalInfo);
  }
});

test('patchelf 전처리 실패는 Wails 자산 갱신·이전 AppImage 삭제 전에 멈춘다', { skip: posixOnly }, () => {
  for (const scenario of ['missing', 'unsupported']) {
    const setup = fakePackaging(`Linux rejected ${scenario} patchelf`);
    const dirs = targetPaths(setup.cwd, 'linux', 'amd64');
    mkdirSync(dirs.packageDir, { recursive: true });
    writeFileSync(path.join(dirs.packageDir, 'Pulsemetry.AppImage'), 'previous AppImage');
    const input = scenario === 'missing' ? { PATCHELF: path.join(setup.cwd, 'missing-patchelf') } : { FAKE_PATCHELF_UNSUPPORTED: '1' };
    const result = setup.run('linux', 'amd64', 'package', input);
    assert.equal(result.status, 1);
    assert.match(result.stderr, /patchelf/i);
    assert.match(result.stderr, /[가-힣]/);
    assert.equal(loggedCalls(setup.wailsLog).every((call) => call.args[0] === 'version'), true);
    assert.equal(existsSync(dirs.assets), false);
    assert.equal(readFileSync(path.join(dirs.packageDir, 'Pulsemetry.AppImage'), 'utf8'), 'previous AppImage');
  }
});

test('build·macOS·Windows 작업은 사용자 PATCHELF를 그대로 전달한다', { skip: posixOnly }, () => {
  for (const [os, mode] of [['linux', 'build'], ['darwin', 'package'], ['windows', 'package']]) {
    const setup = fakePackaging(`${os} ${mode} patchelf preservation`);
    const originalPatchelf = path.join(setup.cwd, 'deliberately nonexistent patchelf');
    const result = setup.run(os, 'amd64', mode, { PATCHELF: originalPatchelf });
    assert.equal(result.status, 0, result.stderr);
    assert.equal(loggedCalls(setup.wailsLog).every((call) => call.patchelf === originalPatchelf), true);
    assert.equal(loggedCalls(setup.patchelfLog).length, 0);
  }
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
  mkdirSync(path.join(dirs.build, 'bin'), { recursive: true });
  mkdirSync(dirs.packageDir, { recursive: true });
  const cli = path.join(dirs.build, 'bin', 'pulsemetry');
  const original = path.join(dirs.build, 'Pulsemetry');
  const pkg = path.join(dirs.packageDir, 'Pulsemetry.AppImage');
  writeFileSync(cli, 'current CLI');
  writeFileSync(original, 'current build');
  writeFileSync(pkg, 'package');
  let includeCLI = false;
  let packageBuildID = 'current-build';
  let packageVersion = '1.2.3';
  let metadataError = false;
  let postMetadataCalls = 0;
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
      assert.deepEqual(args.slice(0, 2), ['version', '-m']);
      if (metadataError && args[2].includes(`${path.sep}verify${path.sep}`)) throw new Error('not a Go executable');
      return `\tbuild\tGOOS=linux\n\tbuild\tGOARCH=amd64\n\tbuild\tCGO_ENABLED=${args[2] === cli ? '0' : '1'}\n`;
    }
    postMetadataCalls++;
    if (command === 'go') {
      assert.deepEqual(args.slice(0, 2), ['run', 'cmd/buildid']);
      return args[2] === original ? 'current-build\n' : `${packageBuildID}\n`;
    }
    if (command === cli) {
      assert.deepEqual(args, ['version']);
      return 'pulsemetry 1.2.3\n';
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
  packageVersion = '1.2.3';
  metadataError = true;
  const before = postMetadataCalls;
  assert.throws(() => checkPackage(pkg, 'linux', 'amd64', '1.2.3', dirs, fake), /not a Go executable/);
  assert.equal(postMetadataCalls, before);
  assert.equal(existsSync(path.join(dirs.packageDir, 'verify')), false);
  assert.throws(() => stageTarget(dir, 'linux', 'amd64', '1.2.3', { execute: fake }), /not a Go executable/);
  assert.equal(existsSync(path.join(dir, 'artifacts', 'release', 'linux-amd64')), false);
  assert.equal(existsSync(path.join(dirs.packageDir, 'verify')), false);
});
