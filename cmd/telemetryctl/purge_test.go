package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/store"
)

// runPurgeCmd 는 stdin 과 대화 가능 여부까지 주입해 purge 를 돌린다.
func runPurgeCmd(t *testing.T, stdin string, canPrompt bool, args ...string) runResult {
	t.Helper()
	var out, errBuf bytes.Buffer
	code := runPurge(&out, &errBuf, strings.NewReader(stdin), canPrompt, args)
	return runResult{code: code, stdout: out.String(), stderr: errBuf.String()}
}

// contentRows 는 DB 에 남은 원문 행 수다. "정말 지웠나/안 지웠나" 를 출력 문구가 아니라
// DB 로 확인해야 안전장치 테스트에 의미가 있다.
//
// 세는 자리는 purge 가 지우는 자리와 같아야 한다 (store.ContentCounts). 조회 계층을 거치면
// 이 테스트가 CLI 가 아니라 조회 계층의 상태를 확인하게 된다.
func contentRows(t *testing.T, dataDir string) int64 {
	t.Helper()
	reader, err := store.OpenReadOnly(store.PathIn(dataDir))
	if err != nil {
		t.Fatalf("store.OpenReadOnly: %v", err)
	}
	defer reader.Close() //nolint:errcheck // 테스트 조회 핸들
	counts, err := reader.ContentCounts(context.Background(), time.Time{})
	if err != nil {
		t.Fatalf("ContentCounts: %v", err)
	}
	return counts.Total()
}

func TestPurgeRequiresContentFlag(t *testing.T) {
	_, args := tempTarget(t)
	res := runPurgeCmd(t, "", true, args...)
	if res.code != 2 {
		t.Fatalf("code = %d, want 2", res.code)
	}
	if !strings.Contains(res.stderr, "--content") {
		t.Errorf("stderr 가 무엇을 지정해야 하는지 말하지 않는다: %q", res.stderr)
	}
}

// DB 가 없는데 store.Open 을 부르면 빈 DB 파일이 생긴다. 지울 것이 없다고 말하고 끝내야 한다.
func TestPurgeWithoutDatabase(t *testing.T) {
	dir, args := tempTarget(t)
	res := runPurgeCmd(t, "", true, append(args, "--content")...)
	if res.code != 0 {
		t.Fatalf("code = %d, want 0 (stderr: %s)", res.code, res.stderr)
	}
	res.mustContain(t, "로컬 DB 가 없습니다")
	// store.Open 은 파일을 만든다. 지울 것이 없다고 말하면서 빈 DB 를 남기면,
	// 다음 status 가 "설정 안 됨" 대신 "데이터 0건" 을 보고하게 된다.
	if _, err := os.Stat(store.PathIn(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("DB 파일이 생겼다: %v", err)
	}
}

// 전체 삭제는 확인 없이 일어나면 안 된다.
func TestPurgeFullRequiresConfirmation(t *testing.T) {
	tests := []struct {
		name      string
		stdin     string
		canPrompt bool
		extra     []string
		wantCode  int
		wantRows  int64
	}{
		{name: "확인 프롬프트에 yes 아닌 답", stdin: "no\n", canPrompt: true, wantCode: 1, wantRows: 1},
		{name: "빈 입력", stdin: "\n", canPrompt: true, wantCode: 1, wantRows: 1},
		{name: "y 만으로는 안 된다", stdin: "y\n", canPrompt: true, wantCode: 1, wantRows: 1},
		{name: "비대화 실행에서 --yes 없음", stdin: "", canPrompt: false, wantCode: 2, wantRows: 1},
		{name: "yes 입력", stdin: "yes\n", canPrompt: true, wantCode: 0, wantRows: 0},
		{name: "--yes 로 비대화 진행", stdin: "", canPrompt: false, extra: []string{"--yes"}, wantCode: 0, wantRows: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, args := tempTarget(t)
			seed(t, dir, time.Now().Add(-30*time.Minute))
			if got := contentRows(t, dir); got != 1 {
				t.Fatalf("준비 상태의 원문 = %d행, want 1", got)
			}

			res := runPurgeCmd(t, tt.stdin, tt.canPrompt, append(append(args, "--content"), tt.extra...)...)
			if res.code != tt.wantCode {
				t.Fatalf("code = %d, want %d (stdout: %s / stderr: %s)", res.code, tt.wantCode, res.stdout, res.stderr)
			}
			if got := contentRows(t, dir); got != tt.wantRows {
				t.Errorf("남은 원문 = %d행, want %d", got, tt.wantRows)
			}
			// 무엇이 사라지는지 먼저 말해야 한다.
			res.mustContain(t, "되돌릴 수 없습니다")
		})
	}
}

// --before 로 구간을 자르면 그 경계만 지운다. 구간 삭제는 확인을 요구하지 않는다
// (스크립트에서 정기적으로 도는 용법이 정상이다).
func TestPurgeBefore(t *testing.T) {
	at := time.Now().Add(-30 * time.Minute)

	t.Run("경계 이전이 없으면 아무것도 안 지운다", func(t *testing.T) {
		dir, args := tempTarget(t)
		seed(t, dir, at)
		before := at.Add(-24 * time.Hour).Format("2006-01-02")
		res := runPurgeCmd(t, "", false, append(args, "--content", "--before", before)...)
		if res.code != 0 {
			t.Fatalf("code = %d (stderr: %s)", res.code, res.stderr)
		}
		if got := contentRows(t, dir); got != 1 {
			t.Errorf("남은 원문 = %d행, want 1", got)
		}
	})

	t.Run("경계 이후를 주면 지운다", func(t *testing.T) {
		dir, args := tempTarget(t)
		seed(t, dir, at)
		before := at.Add(24 * time.Hour).Format("2006-01-02")
		res := runPurgeCmd(t, "", false, append(args, "--content", "--before", before)...)
		if res.code != 0 {
			t.Fatalf("code = %d (stderr: %s)", res.code, res.stderr)
		}
		if got := contentRows(t, dir); got != 0 {
			t.Errorf("남은 원문 = %d행, want 0", got)
		}
		res.mustContain(t, "원문 1행을 지웠습니다")
	})

	t.Run("해석할 수 없는 --before 는 거부", func(t *testing.T) {
		_, args := tempTarget(t)
		res := runPurgeCmd(t, "", false, append(args, "--content", "--before", "2026년 7월")...)
		if res.code != 2 {
			t.Fatalf("code = %d, want 2", res.code)
		}
	})
}

func TestParseBefore(t *testing.T) {
	tests := []struct {
		in      string
		wantErr bool
		zero    bool
	}{
		{in: "", zero: true},
		{in: "2026-07-01"},
		{in: "2026-07-01 09:30"},
		{in: "2026-07-01T09:30:00"},
		{in: "2026-07-01T09:30:00Z"},
		{in: "07/01/2026", wantErr: true},
		{in: "어제", wantErr: true},
	}
	for _, tt := range tests {
		t.Run("before="+tt.in, func(t *testing.T) {
			got, err := parseBefore(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseBefore(%q) = %v, want 오류", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseBefore(%q): %v", tt.in, err)
			}
			if got.IsZero() != tt.zero {
				t.Errorf("parseBefore(%q).IsZero() = %t, want %t", tt.in, got.IsZero(), tt.zero)
			}
		})
	}
}

// 날짜만 준 --before 는 로컬 자정이어야 한다. UTC 로 읽으면 사용자가 말한 것보다
// 몇 시간 더(또는 덜) 지워진다.
func TestParseBeforeUsesLocalMidnight(t *testing.T) {
	got, err := parseBefore("2026-07-01")
	if err != nil {
		t.Fatalf("parseBefore: %v", err)
	}
	want := time.Date(2026, 7, 1, 0, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("parseBefore = %s, want %s", got, want)
	}
}

func TestConfirmRefusesWhenNotInteractive(t *testing.T) {
	ok, err := confirm(io.Discard, strings.NewReader("yes\n"), false)
	if err == nil {
		t.Fatal("err = nil, want 비대화 거부 오류")
	}
	if ok {
		t.Error("ok = true, want false")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("오류가 대안을 알려 주지 않는다: %v", err)
	}
}
