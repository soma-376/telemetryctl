import { execFileSync, spawnSync } from 'node:child_process';
import { cpSync, mkdirSync, readFileSync, rmSync, writeFileSync, copyFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { stripVTControlCharacters } from 'node:util';
import { prepareAppImagePatchelf } from './appimage-patchelf.mjs';

export function pinnedWailsVersion(output) {
  return /(?:^|\s)v3\.0\.0-beta\.11(?:\s|$)/.test(stripVTControlCharacters(output));
}

export function resolveWailsCommand(command, cwd = process.cwd()) {
  // 작업 디렉터리가 바뀌어도 사용자가 선택한 실행 파일을 유지한다.
  return path.isAbsolute(command) || /[\\/]/.test(command) ? path.resolve(cwd, command) : command;
}

export function appImageName(arch) {
  if (!['amd64', 'arm64'].includes(arch)) throw new Error('지원하지 않는 AppImage 아키텍처입니다.');
  // Wails beta.11은 패키지 이름을 소문자로 정규화한다.
  return `pulsemetry-${arch === 'amd64' ? 'x86_64' : 'aarch64'}.AppImage`;
}

export function versionParts(version) {
  const match = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$/.exec(version);
  if (!match || match.slice(1, 4).some((part) => Number(part) > 65535)) {
    throw new Error('버전은 0.1.0 또는 0.1.0-rc.1 형태이고 숫자 항목은 65535 이하여야 합니다.');
  }
  if (match[4]?.split('.').some((part) => /^0\d+$/.test(part))) {
    throw new Error('prerelease의 숫자 식별자에는 앞자리 0을 쓰지 않습니다.');
  }
  return { full: version, numeric: match.slice(1, 4).join('.') };
}

export function targetPaths(root, os, arch) {
  if (!['windows', 'darwin', 'linux'].includes(os) || !['amd64', 'arm64'].includes(arch)) {
    throw new Error('대상은 windows/darwin/linux 및 amd64/arm64여야 합니다.');
  }
  const build = path.join(root, 'artifacts', 'build', `${os}-${arch}`);
  const packageDir = path.join(root, 'artifacts', 'package', `${os}-${arch}`);
  return { build, packageDir, assets: path.join(packageDir, 'build') };
}

// 릴리스 메타데이터는 복사한 자산에만 적용한다. 원본 config와 생성 파일은 변경하지 않는다.
export function prepareAssets(root, os, arch, version, runWails) {
  const dirs = targetPaths(root, os, arch);
  mkdirSync(dirs.build, { recursive: true });
  rmSync(dirs.assets, { recursive: true, force: true });
  cpSync(path.join(root, 'cmd', 'pulsemetry-gui', 'build'), dirs.assets, { recursive: true });
  const { numeric, full } = versionParts(version);
  runWails(['update', 'build-assets', '-name', 'Pulsemetry', '-binaryname', 'Pulsemetry',
    '-config', path.join(root, 'cmd', 'pulsemetry-gui', 'build', 'config.yml'),
    '-productversion', numeric, '-dir', dirs.assets]);
  const infoPath = path.join(dirs.assets, 'windows', 'info.json');
  const info = JSON.parse(readFileSync(infoPath, 'utf8'));
  info.info['0000'].ProductVersion = full;
  writeFileSync(infoPath, `${JSON.stringify(info, null, 2)}\n`);
  // 복사본을 매번 다시 만들므로 이전 Task 캐시와 관계없이 아이콘도 새로 생성한다.
  runWails(['task', '-f', 'common:generate:icons', `ASSETS_DIR=${dirs.assets}`]);
  return dirs;
}

export function packageGUI(mode, { root, os, arch, version, wails = 'wails3' }) {
  if (!['build', 'package'].includes(mode)) throw new Error('build 또는 package 명령이 필요합니다.');
  const wailsCommand = resolveWailsCommand(wails);
  const host = execFileSync('go', ['env', 'GOHOSTOS', 'GOHOSTARCH'], { encoding: 'utf8' }).trim().split(/\r?\n/);
  if (host[0] !== os || host[1] !== arch) {
    throw new Error(`GUI는 대상과 같은 OS·CPU에서 빌드합니다. 호스트: ${host.join('/')}, 대상: ${os}/${arch}`);
  }
  const wailsVersion = spawnSync(wailsCommand, ['version'], { encoding: 'utf8' });
  if (wailsVersion.status !== 0 || !pinnedWailsVersion(`${wailsVersion.stdout || ''}\n${wailsVersion.stderr || ''}`)) {
    throw new Error('Wails CLI v3.0.0-beta.11이 필요합니다. 고정 버전을 PATH에 넣거나 WAILS3로 지정하세요.');
  }
  const cwd = path.join(root, 'cmd', 'pulsemetry-gui');
  // 내부 Task의 wails3 명령도 선택한 도구를 사용한다. Windows의 Path 키도 보존한다.
  const env = { ...process.env };
  if (path.isAbsolute(wailsCommand)) {
    const pathKey = process.platform === 'win32' ? Object.keys(env).find((key) => key.toLowerCase() === 'path') || 'PATH' : 'PATH';
    env[pathKey] = `${path.dirname(wailsCommand)}${path.delimiter}${env[pathKey] || ''}`;
  }
  const runWails = (args) => execFileSync(wailsCommand, args, { cwd, env, stdio: 'inherit' });
  // 패키징 도구는 자산 생성이나 이전 패키지 삭제 전에 검사한다.
  let packageEnv = env;
  if (mode === 'package' && os === 'linux') {
    const { packageDir } = targetPaths(root, os, arch);
    const appDir = appImageName(arch).replace(/\.AppImage$/, '.AppDir');
    const gui = path.join(packageDir, 'appimage', 'build', appDir, 'usr', 'bin', 'Pulsemetry');
    packageEnv = { ...env, PATCHELF: prepareAppImagePatchelf(packageDir, gui, { env }) };
  }
  const dirs = prepareAssets(root, os, arch, version, runWails);
  if (mode === 'package') {
    const extension = { windows: '.exe', darwin: '.dmg', linux: '.AppImage' }[os];
    rmSync(path.join(dirs.packageDir, `Pulsemetry${extension}`), { force: true });
    if (os === 'darwin') {
      rmSync(path.join(dirs.build, 'Pulsemetry.app'), { recursive: true, force: true });
      rmSync(path.join(dirs.build, 'Pulsemetry.dmg'), { force: true });
    }
    if (os === 'linux') {
      mkdirSync(path.join(dirs.packageDir, 'appimage'), { recursive: true });
      rmSync(path.join(dirs.packageDir, appImageName(arch)), { force: true });
    }
  }
  const task = mode === 'build' ? 'build' : {
    windows: 'windows:package', darwin: 'darwin:package:dmg', linux: 'linux:create:appimage',
  }[os];
  execFileSync(wailsCommand, ['task', task, `GOOS=${os}`, `ARCH=${arch}`, `GUI_VERSION=${version}`,
    `BIN_DIR=${dirs.build}`, `ASSETS_DIR=${dirs.assets}`, `PACKAGE_DIR=${dirs.packageDir}`,
    ...(os === 'windows' ? ['FORMAT=nsis'] : [])], { cwd, env: packageEnv, stdio: 'inherit' });
  if (mode === 'package' && os === 'darwin') {
    copyFileSync(path.join(dirs.build, 'Pulsemetry.dmg'), path.join(dirs.packageDir, 'Pulsemetry.dmg'));
  }
  if (mode === 'package' && os === 'linux') {
    copyFileSync(path.join(dirs.packageDir, appImageName(arch)), path.join(dirs.packageDir, 'Pulsemetry.AppImage'));
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
    const config = readFileSync(path.join(root, 'cmd', 'pulsemetry-gui', 'build', 'config.yml'), 'utf8');
    const defaultVersion = /^  version:\s*["']?([^"'\s]+)["']?\s*$/m.exec(config)?.[1];
    const version = process.env.PULSEMETRY_GUI_VERSION || defaultVersion;
    versionParts(version);
    packageGUI(process.argv[2], {
      root, os: process.env.PULSEMETRY_TARGET_OS, arch: process.env.PULSEMETRY_TARGET_ARCH,
      version, wails: process.env.WAILS3 || 'wails3',
    });
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
