#!/usr/bin/env python3
"""빌드한 macOS 앱의 제거 흐름을 임시 HOME에서 검증한다. 실제 서비스·키링 실행은 차단한다."""

import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import tempfile


def main():
    if platform.system() != "Darwin":
        raise SystemExit("macOS에서 실행하세요")
    root = Path(__file__).resolve().parents[1]
    arch = "arm64" if platform.machine() == "arm64" else "amd64"
    build = root / "artifacts" / "build" / f"darwin-{arch}"
    with tempfile.TemporaryDirectory(prefix="pulsemetry-removal-test-") as scratch:
        home = Path(scratch).resolve()
        app = home / "Applications" / "Pulsemetry.app"
        app.parent.mkdir()
        shutil.copytree(build / "Pulsemetry.app", app)
        cli = home / ".pulsemetry" / "bin" / "pulsemetry"
        cli.parent.mkdir(parents=True)
        shutil.copy2(build / "bin" / "pulsemetry", cli)
        stub = home / "stubs"
        stub.mkdir()
        launchctl = stub / "launchctl"
        launchctl.write_text("#!/bin/sh\nexit 3\n")
        launchctl.chmod(0o700)
        temp = home / "temporary"
        temp.mkdir()
        env = dict(os.environ, HOME=str(home), TMPDIR=str(temp), PATH=f"{stub}:{os.environ['PATH']}")
        policy = '(version 1)(allow default)(deny process-exec (literal "/bin/launchctl") (literal "/usr/bin/launchctl") (literal "/usr/bin/security"))'

        def run(*args):
            return subprocess.check_output(["/usr/bin/sandbox-exec", "-p", policy, *map(str, args)], env=env, text=True)

        run(cli, "register-product")
        run(app / "Contents" / "MacOS" / "Pulsemetry", "--register-product")
        helper_app = home / "Applications" / "Uninstall Pulsemetry.app"
        helper = helper_app / "Contents" / "MacOS" / "PulsemetryUninstall"
        subprocess.run(["codesign", "--verify", "--deep", "--strict", str(helper_app)], check=True)
        # 본 앱을 먼저 휴지통으로 옮긴 경우에도 독립 도구가 동작해야 한다.
        shutil.rmtree(app)
        user_file = home / ".pulsemetry" / "user-notes.txt"
        user_file.write_text("keep")
        result = json.loads(run(helper, "--uninstall-cleanup"))
        assert result["success"], result
        assert not cli.exists()
        assert helper.exists()
        assert user_file.read_text() == "keep"
        # 창 종료 뒤 실행할 복사본을 실제 최종 단계와 같은 위치에 둔다.
        finish_dir = Path(tempfile.mkdtemp(prefix="pulsemetry-uninstall-", dir=temp))
        finalizer = finish_dir / "finish"
        shutil.copy2(helper, finalizer)
        subprocess.run(["/usr/bin/sandbox-exec", "-p", policy, str(finalizer), "--uninstall-finalize"], env=env, stdin=subprocess.DEVNULL, check=True)
        assert not helper_app.exists()
        assert not (home / ".pulsemetry" / "product-installation.json").exists()
        assert not finish_dir.exists()
        assert user_file.read_text() == "keep"
    print("PASS: 등록 → 본 앱 선삭제 → 독립 제거 → 도구 자체 정리 / 사용자 파일 보존")


if __name__ == "__main__":
    main()
