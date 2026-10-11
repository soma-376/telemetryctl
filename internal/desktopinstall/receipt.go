package desktopinstall

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/your-org/pulsemetry/internal/instancelock"
)

const ReceiptVersion = 1

type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type Receipt struct {
	Version int `json:"version"`
	// 각 구성요소는 등록된 일반 파일만 소유한다. 디렉터리 재귀 삭제는 하지 않는다.
	Components map[string][]File `json:"components"`
}

func ReceiptPath(home string) string {
	return filepath.Join(home, ".pulsemetry", "product-installation.json")
}

// regularPath는 부모 링크까지 확인한다. 사용자 홈 밖 파일의 소유권을 추측하지 않는다.
func regularPath(name string) error {
	if !filepath.IsAbs(name) || filepath.Clean(name) != name || filepath.Dir(name) == name {
		return errors.New("절대 파일 경로가 필요합니다")
	}
	for p := name; ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("링크 경로는 관리하지 않습니다: %s", p)
		}
		if p == name && err == nil && !info.Mode().IsRegular() {
			return fmt.Errorf("일반 파일이 아닙니다: %s", p)
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}

func fingerprint(name string) (string, error) {
	if err := regularPath(name); err != nil {
		return "", err
	}
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func LoadReceipt(name string) (Receipt, error) {
	r := Receipt{Version: ReceiptVersion, Components: map[string][]File{}}
	if err := regularPath(name); err != nil {
		return r, err
	}
	b, err := os.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(b, &r); err != nil {
		return r, err
	}
	if r.Version != ReceiptVersion || r.Components == nil {
		return r, errors.New("지원하지 않는 설치 기록입니다")
	}
	seen := map[string]bool{}
	for component, files := range r.Components {
		if !slices.Contains([]string{"gui", "cli", "uninstaller", "launcher"}, component) {
			return r, errors.New("알 수 없는 설치 구성요소입니다")
		}
		for _, f := range files {
			if err := regularPath(f.Path); err != nil {
				return r, err
			}
			digest, err := hex.DecodeString(f.SHA256)
			if err != nil || len(digest) != sha256.Size || seen[f.Path] {
				return r, errors.New("잘못된 프로그램 파일 기록입니다")
			}
			seen[f.Path] = true
		}
	}
	return r, nil
}

func saveReceipt(name string, r Receipt) error {
	if err := regularPath(name); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(name), ".installation-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(b, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), name)
}

// RegisterFiles는 패키저가 확인한 고정 파일 목록만 받는다. 임의 디렉터리를 스캔하지 않는다.
func RegisterFiles(receiptPath, component string, names []string) error {
	if err := regularPath(filepath.Join(filepath.Dir(receiptPath), "product-lock", ".pulsemetry.lock")); err != nil {
		return err
	}
	lease, err := instancelock.Acquire(filepath.Join(filepath.Dir(receiptPath), "product-lock"))
	if err != nil {
		return err
	}
	defer lease.Close()
	r, err := LoadReceipt(receiptPath)
	if err != nil {
		return err
	}
	if !slices.Contains([]string{"gui", "cli", "uninstaller", "launcher"}, component) {
		return errors.New("알 수 없는 설치 구성요소입니다")
	}
	files := make([]File, 0, len(names))
	seen := map[string]bool{}
	for key, registered := range r.Components {
		if key != component {
			for _, f := range registered {
				seen[strings.ToLower(f.Path)] = true
			}
		}
	}
	for _, name := range names {
		if seen[strings.ToLower(name)] {
			return fmt.Errorf("중복 설치 파일입니다: %s", name)
		}
		seen[strings.ToLower(name)] = true
		hash, err := fingerprint(name)
		if err != nil {
			return err
		}
		files = append(files, File{Path: name, SHA256: hash})
	}
	r.Components[component] = files
	return saveReceipt(receiptPath, r)
}

// RemoveFiles는 다른 파일이나 바뀐 파일을 지우지 않는다. 실패한 항목은 기록에 남긴다.
// 제거 도구와 launcher는 최종 단계까지 남겨 재시도가 가능하게 한다.
func RemoveFiles(receiptPath string, components ...string) ([]string, error) {
	if err := regularPath(filepath.Join(filepath.Dir(receiptPath), "product-lock", ".pulsemetry.lock")); err != nil {
		return nil, err
	}
	lease, err := instancelock.Acquire(filepath.Join(filepath.Dir(receiptPath), "product-lock"))
	if err != nil {
		return nil, err
	}
	defer lease.Close()
	r, err := LoadReceipt(receiptPath)
	if err != nil {
		return nil, err
	}
	var preserved []string
	var deleted []string
	for _, component := range components {
		var remaining []File
		for _, f := range r.Components[component] {
			hash, err := fingerprint(f.Path)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return preserved, err
			}
			if hash != f.SHA256 {
				preserved = append(preserved, f.Path)
				remaining = append(remaining, f)
				continue
			}
			if err = os.Remove(f.Path); err != nil {
				return preserved, fmt.Errorf("파일 제거 실패 (%s): %w", f.Path, err)
			}
			deleted = append(deleted, f.Path)
		}
		if len(remaining) == 0 {
			delete(r.Components, component)
		} else {
			r.Components[component] = remaining
		}
	}
	if err = saveReceipt(receiptPath, r); err != nil {
		return preserved, err
	}
	pruneBundleDirs(deleted)
	return preserved, nil
}

func pruneBundleDirs(files []string) {
	for _, name := range files {
		parent := filepath.Dir(name)
		for strings.Contains(filepath.ToSlash(parent), ".app/") || strings.HasSuffix(parent, ".app") {
			if err := os.Remove(parent); err != nil {
				break
			}
			parent = filepath.Dir(parent)
		}
	}
}
