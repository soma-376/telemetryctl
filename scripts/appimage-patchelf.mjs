import { spawnSync } from 'node:child_process';
import { accessSync, chmodSync, constants, mkdirSync, realpathSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const script = fileURLToPath(import.meta.url);
const shellQuote = (value) => `'${value.replaceAll("'", "'\\''")}'`;

// linuxdeploy는 번들 도구를 PATH보다 먼저 찾으므로 PATCHELF로 명시해야 한다.
export function prepareAppImagePatchelf(packageDir, gui, {
  command = process.env.PATCHELF || 'patchelf', cwd = process.cwd(), env = process.env,
} = {}) {
  const candidates = path.isAbsolute(command) || command.includes('/')
    ? [path.resolve(cwd, command)]
    : (env.PATH || '').split(path.delimiter).map((dir) => path.resolve(cwd, dir, command));
  let tool;
  for (const candidate of candidates) {
    try {
      accessSync(candidate, constants.X_OK);
      tool = realpathSync(candidate);
      break;
    } catch {
      // 실행 가능한 다음 PATH 항목을 찾는다.
    }
  }
  if (!tool) throw new Error('AppImage 패키징에는 patchelf가 필요합니다. PATH에 설치하거나 PATCHELF로 실행 파일을 지정하세요.');
  const launcher = path.resolve(packageDir, 'tools', 'patchelf');
  let previousLauncher;
  try {
    previousLauncher = realpathSync(launcher);
  } catch {
    // 첫 패키징에는 래퍼가 없다.
  }
  if (tool === previousLauncher) throw new Error('PATCHELF에는 생성된 AppImage 래퍼 대신 실제 patchelf 실행 파일을 지정하세요.');
  const help = spawnSync(tool, ['--help'], { cwd, env, encoding: 'utf8' });
  if (help.error || help.status !== 0 || !/(?:^|\s)\[?--no-sort(?:\]|\s|$)/.test(`${help.stdout || ''}\n${help.stderr || ''}`)) {
    throw new Error('AppImage 패키징에는 --no-sort를 지원하는 patchelf(0.15.0 이상)가 필요합니다.');
  }
  const tools = path.resolve(packageDir, 'tools');
  mkdirSync(tools, { recursive: true });
  // /bin/sh 런처를 써서 Node·레포·도구 경로에 공백이나 작은따옴표가 있어도 보존한다.
  writeFileSync(launcher, `#!/bin/sh\nexec ${[process.execPath, script, tool, path.resolve(gui)].map(shellQuote).join(' ')} "$@"\n`, { mode: 0o755 });
  chmodSync(launcher, 0o755);
  return launcher;
}

export function runAppImagePatchelf(tool, gui, args) {
  let preserveOrder = false;
  if (args.includes('--set-rpath') && args.length > 0) {
    try {
      // 빈 GNU_STACK이 LOAD 앞에 오면 Go 판독기의 범위 계산이 underflow한다.
      preserveOrder = realpathSync(args.at(-1)) === realpathSync(gui);
    } catch {
      // 잘못된 입력의 진단과 종료 코드는 실제 patchelf에 맡긴다.
    }
  }
  const result = spawnSync(tool, preserveOrder && !args.includes('--no-sort') ? ['--no-sort', ...args] : args, {
    stdio: 'inherit',
  });
  if (result.error) throw new Error(`patchelf 실행에 실패했습니다: ${result.error.message}`);
  if (result.signal) process.kill(process.pid, result.signal);
  else process.exitCode = result.status;
}

if (process.argv[1] && path.resolve(process.argv[1]) === script) {
  try {
    const [tool, gui, ...args] = process.argv.slice(2);
    if (!tool || !gui) throw new Error('실제 patchelf와 AppImage GUI 경로가 필요합니다.');
    runAppImagePatchelf(tool, gui, args);
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
