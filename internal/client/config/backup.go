// Package configmerge 는 Codex(TOML)·Claude Code(JSON) 설정 파일에 OTel 키만 안전하게 병합한다.
//
// 핵심 원칙 (§4.1, §4.2, §5.2):
//   - 파일 전체를 덮어쓰지 않는다. 파서로 읽어 필요한 키만 수정한다.
//   - 수정 전 원본을 백업한다.
//   - installer 가 추가/수정한 키 목록을 반환해 uninstall 이 그 키만 제거할 수 있게 한다.
//   - 다른 endpoint 가 이미 있으면 자동 덮어쓰지 않고 충돌로 보고한다.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Result 는 병합 한 건의 결과다. 호출자는 ManagedKeys 를 state 에 기록해 두었다가
// uninstall 시 그 키만 제거한다.
type Result struct {
	Path        string   // 대상 설정 파일
	BackupPath  string   // 생성된 백업 경로 (기존 파일이 없었으면 "")
	ManagedKeys []string // installer 가 추가/수정한 키 (예: "env.OTEL_EXPORTER_OTLP_ENDPOINT")
	Created     bool     // 설정 파일을 새로 만들었는가
}

// ErrEndpointConflict 는 사용자가 이미 다른 Collector 로 telemetry 를 보내고 있을 때 반환된다.
// 이 경우 자동 교체하지 않고 사용자/관리자 판단을 요구한다 (§4.2).
var ErrEndpointConflict = errors.New("기존 OTel endpoint 충돌: 자동 교체 금지")

// readFileIfExists 는 파일이 없으면 (nil, false, nil) 을 반환한다.
func readFileIfExists(path string) (data []byte, existed bool, err error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return b, true, nil
}

// backupPath 는 원본 경로의 확장자 앞에 pulsemetry-backup 마커를 넣은 백업 경로를 만든다.
// 예: ~/.claude/settings.json -> ~/.claude/settings.pulsemetry-backup.json
// 확장자를 유지해 에디터가 파일 형식(json/toml)을 그대로 인식하게 한다.
func backupPath(path string) string {
	ext := filepath.Ext(path)
	return strings.TrimSuffix(path, ext) + ".pulsemetry-backup" + ext
}

// backupOnce 는 원본을 backupPath 위치로 복사한다. 백업이 이미 있으면 건드리지 않는다
// (첫 설치 시점의 원본을 보존하기 위해).
func backupOnce(path string, content []byte) (string, error) {
	bak := backupPath(path)
	if _, err := os.Stat(bak); err == nil {
		return bak, nil // 이미 백업됨
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if err := os.WriteFile(bak, content, 0o600); err != nil {
		return "", fmt.Errorf("백업 생성 실패 %s: %w", bak, err)
	}
	return bak, nil
}

// ensureDir 는 대상 파일의 상위 디렉터리를 보장한다.
func ensureDir(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0o700)
}
