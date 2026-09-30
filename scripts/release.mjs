import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { chmodSync, copyFileSync, existsSync, mkdirSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { targetPaths, versionParts } from './package-gui.mjs';

const run = (command, args, options = {}) => execFileSync(command, args, { encoding: 'utf8', ...options });
const hash = (filename) => createHash('sha256').update(readFileSync(filename)).digest('hex');
const nonempty = (filename) => {
  if (!statSync(filename).isFile() || statSync(filename).size === 0) throw new Error(`파일이 비어 있거나 일반 파일이 아닙니다: ${filename}`);
};

export function validateTag(tag, { cwd = process.cwd(), execute = run } = {}) {
  if (!tag?.startsWith('v')) throw new Error('릴리스 태그는 v로 시작해야 합니다.');
  const version = versionParts(tag.slice(1)).full;
  execute('git', ['merge-base', '--is-ancestor', 'HEAD', 'origin/main'], { cwd });
  return version;
}

export function assetNames(os, arch) {
  targetPaths('.', os, arch);
  return {
    cli: `pulsemetry_cli_${os}_${arch}${os === 'windows' ? '.exe' : ''}`,
    gui: `pulsemetry_gui_${os}_${arch}${{ windows: '.exe', darwin: '.dmg', linux: '.AppImage' }[os]}`,
  };
}

export function expectedAssets() {
  return ['windows', 'darwin', 'linux'].flatMap((os) => ['amd64', 'arm64'].flatMap((arch) => Object.values(assetNames(os, arch)))).sort();
}

export function checkBinary(filename, os, arch, version, cli, execute = run) {
  nonempty(filename);
  const metadata = execute('go', ['version', '-m', filename]);
  for (const setting of [`GOOS=${os}`, `GOARCH=${arch}`, ...(cli ? ['CGO_ENABLED=0'] : [])]) {
    if (!metadata.split(/\r?\n/).some((line) => line.trim() === `build\t${setting}`)) {
      throw new Error(`바이너리 빌드 정보가 일치하지 않습니다: ${filename}: ${setting}`);
    }
  }
  // -trimpath는 buildinfo에서 ldflags를 제외한다. 실행 결과로 주입된 버전을 확인한다.
  const output = execute(filename, cli ? ['version'] : ['--version']).trim();
  if (output !== `${cli ? 'pulsemetry' : 'pulsemetry-gui'} ${version}`) {
    throw new Error(`바이너리 버전 출력이 일치하지 않습니다: ${filename}`);
  }
}

function filesUnder(dir) {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const name = path.join(dir, entry.name);
    return entry.isDirectory() ? filesUnder(name) : entry.isFile() ? [name] : [];
  });
}

// 패키지를 읽기 전용으로 추출해 내부 실행 파일도 검사한다.
export function checkPackage(filename, os, arch, version, dirs, execute = run) {
  const work = path.join(dirs.packageDir, 'verify');
  rmSync(work, { recursive: true, force: true });
  mkdirSync(work, { recursive: true });
  let root;
  let mounted = false;
  try {
    if (os === 'darwin') {
      root = path.join(work, 'mount');
      mkdirSync(root);
      execute('hdiutil', ['attach', '-readonly', '-nobrowse', '-mountpoint', root, filename]);
      mounted = true;
      const plist = path.join(root, 'Pulsemetry.app', 'Contents', 'Info.plist');
      const executable = execute('/usr/libexec/PlistBuddy', ['-c', 'Print :CFBundleExecutable', plist]).trim();
      if (executable !== 'Pulsemetry') throw new Error('DMG 실행 파일명이 일치하지 않습니다.');
      const numeric = versionParts(version).numeric;
      for (const key of ['CFBundleVersion', 'CFBundleShortVersionString']) {
        if (execute('/usr/libexec/PlistBuddy', ['-c', `Print :${key}`, plist]).trim() !== numeric) {
          throw new Error(`DMG 버전이 일치하지 않습니다: ${key}`);
        }
      }
    } else if (os === 'linux') {
      chmodSync(filename, 0o755);
      execute(filename, ['--appimage-extract'], { cwd: work, stdio: ['ignore', 'ignore', 'pipe'] });
      root = path.join(work, 'squashfs-root');
    } else {
      root = path.join(work, 'extract');
      const info = JSON.parse(execute('powershell', ['-NoProfile', '-Command',
        '(Get-Item -LiteralPath $env:PULSEMETRY_GUI_INSTALLER).VersionInfo | Select-Object ProductName,ProductVersion | ConvertTo-Json -Compress'],
      { env: { ...process.env, PULSEMETRY_GUI_INSTALLER: filename } }).trim());
      if (info.ProductName !== 'Pulsemetry' || info.ProductVersion !== version) {
        throw new Error('NSIS 제품명 또는 표시 버전이 일치하지 않습니다.');
      }
      const fallback = path.join(process.env.ProgramFiles || 'C:\\Program Files', '7-Zip', '7z.exe');
      execute(existsSync(fallback) ? fallback : '7z', ['x', '-y', `-o${root}`, filename]);
    }
    const files = filesUnder(root);
    // CLI의 소문자 파일명과 GUI의 대문자 파일명을 구분해 검사한다.
    if (files.some((name) => /^pulsemetry(?:\.exe)?$/.test(path.basename(name)))) {
      throw new Error('GUI 패키지에 CLI가 포함되어 있습니다.');
    }
    const guiName = os === 'windows' ? 'Pulsemetry.exe' : 'Pulsemetry';
    const guis = files.filter((name) => path.basename(name) === guiName);
    if (guis.length !== 1) throw new Error(`GUI 패키지의 실행 파일 개수가 일치하지 않습니다: ${guis.length}`);
    checkBinary(guis[0], os, arch, version, false, execute);
    const original = path.join(dirs.build, guiName);
    // buildid 실행 파일은 Go 배포물에 없다. 함께 제공되는 표준 도구 소스를 실행한다.
    const buildID = execute('go', ['run', 'cmd/buildid', original]).trim();
    if (!buildID || execute('go', ['run', 'cmd/buildid', guis[0]]).trim() !== buildID) {
      throw new Error('패키지의 GUI가 이번 빌드 산출물과 일치하지 않습니다.');
    }
  } finally {
    if (mounted) execute('hdiutil', ['detach', root]);
    rmSync(work, { recursive: true, force: true });
  }
}

export function stageTarget(root, os, arch, version, { execute = run, inspectPackage = checkPackage } = {}) {
  versionParts(version);
  const dirs = targetPaths(root, os, arch);
  const cli = path.join(dirs.build, 'bin', `pulsemetry${os === 'windows' ? '.exe' : ''}`);
  const gui = path.join(dirs.build, `Pulsemetry${os === 'windows' ? '.exe' : ''}`);
  const pkg = path.join(dirs.packageDir, `Pulsemetry${{ windows: '.exe', darwin: '.dmg', linux: '.AppImage' }[os]}`);
  checkBinary(cli, os, arch, version, true, execute);
  checkBinary(gui, os, arch, version, false, execute);
  nonempty(pkg);
  inspectPackage(pkg, os, arch, version, dirs, execute);
  const dest = path.join(root, 'artifacts', 'release', `${os}-${arch}`);
  // 검사가 끝난 뒤에만 파일을 배치한다. 이전 빌드의 파일은 재사용하지 않는다.
  rmSync(dest, { recursive: true, force: true });
  mkdirSync(dest, { recursive: true });
  const names = assetNames(os, arch);
  copyFileSync(cli, path.join(dest, names.cli));
  copyFileSync(pkg, path.join(dest, names.gui));
  return dest;
}

export function checksums(dir, verify = false) {
  const expected = expectedAssets();
  const actual = readdirSync(dir).filter((name) => name !== 'SHA256SUMS').sort();
  if (JSON.stringify(actual) !== JSON.stringify(expected)) throw new Error('릴리스 파일 목록은 CLI 6개와 GUI 6개여야 합니다.');
  const content = expected.map((name) => {
    nonempty(path.join(dir, name));
    return `${hash(path.join(dir, name))}  ${name}\n`;
  }).join('');
  if (verify) {
    if (readFileSync(path.join(dir, 'SHA256SUMS'), 'utf8') !== content) throw new Error('릴리스 체크섬이 일치하지 않습니다.');
  } else writeFileSync(path.join(dir, 'SHA256SUMS'), content);
}

export function publish(tag, dir, execute = run) {
  if (!tag?.startsWith('v')) throw new Error('릴리스 태그는 v로 시작해야 합니다.');
  versionParts(tag.slice(1));
  checksums(dir, true);
  // draft도 포함해 모든 페이지를 조회한다. 조회 실패를 신규 버전으로 처리하지 않는다.
  const releases = execute('gh', ['api', '--paginate', 'repos/{owner}/{repo}/releases?per_page=100', '--jq', '.[].tag_name']);
  if (releases.split(/\r?\n/).includes(tag)) throw new Error(`이미 존재하는 Release입니다: ${tag}`);
  // 모든 파일의 업로드가 성공한 새 draft만 공개한다.
  execute('gh', ['release', 'create', tag, ...expectedAssets().map((name) => path.join(dir, name)),
    path.join(dir, 'SHA256SUMS'), '--verify-tag', '--draft', '--title', tag, '--generate-notes',
    ...(tag.includes('-') ? ['--prerelease'] : [])]);
  execute('gh', ['release', 'edit', tag, '--draft=false']);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const [command, ...args] = process.argv.slice(2);
    const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
    if (command === 'validate-tag') console.log(validateTag(args[0]));
    else if (command === 'stage') stageTarget(root, ...args);
    else if (command === 'checksums') checksums(path.resolve(args[0]));
    else if (command === 'publish') publish(args[0], path.resolve(args[1]));
    else throw new Error('validate-tag, stage, checksums, publish 명령 중 하나가 필요합니다.');
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
