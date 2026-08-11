package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/your-org/pulsemetry/internal/installer"
	"github.com/your-org/pulsemetry/internal/store"
)

// --since 는 time.ParseDuration 이 모르는 문법(7d)을 포함한다. 파싱이 관대하면 오타가
// 조용히 다른 구간을 조회하고, 사용자는 자기가 본 숫자가 무엇의 합인지 모르게 된다.
func TestParseSince(t *testing.T) {
	day := 24 * time.Hour
	tests := []struct {
		name    string
		in      string
		want    time.Duration
		wantErr bool
	}{
		{name: "빈 값은 기본 7일", in: "", want: 7 * day},
		{name: "일", in: "7d", want: 7 * day},
		{name: "하루", in: "1d", want: day},
		{name: "주", in: "2w", want: 14 * day},
		{name: "시간", in: "24h", want: 24 * time.Hour},
		{name: "분", in: "30m", want: 30 * time.Minute},
		{name: "초", in: "90s", want: 90 * time.Second},
		{name: "복합 표준 문법", in: "1h30m", want: 90 * time.Minute},
		{name: "공백은 다듬는다", in: "  7d ", want: 7 * day},
		{name: "상한 경계는 통과", in: "400d", want: 400 * day},

		{name: "단위 없는 숫자는 거부", in: "7", wantErr: true},
		{name: "한글 단위는 거부", in: "7일", wantErr: true},
		{name: "알 수 없는 단위는 거부", in: "7y", wantErr: true},
		{name: "숫자 없는 단위는 거부", in: "d", wantErr: true},
		{name: "소수 일은 거부", in: "1.5d", wantErr: true},
		{name: "0 은 거부", in: "0d", wantErr: true},
		{name: "음수는 거부", in: "-1d", wantErr: true},
		{name: "상한 초과는 거부", in: "401d", wantErr: true},
		{name: "주 단위 상한 초과도 거부", in: "60w", wantErr: true},
		{name: "곱셈 오버플로도 거부", in: "999999999d", wantErr: true},
		{name: "쓰레기 값은 거부", in: "abc", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSince(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseSince(%q) = %v, want 오류", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSince(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("parseSince(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// --group 오타를 기본값으로 떨어뜨리면 사용자는 요청하지 않은 축의 표를 본다.
func TestParseGroup(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "", want: "vendor"},
		{in: "vendor", want: "vendor"},
		{in: "model", want: "model"},
		{in: "tool", want: "tool"},
		{in: "project", want: "project"},
		{in: "day", want: "day"},

		{in: "vendors", wantErr: true},
		{in: "VENDOR", wantErr: true},
		{in: "total", wantErr: true}, // dashboard 에는 있지만 CLI 어휘에는 없다
		{in: "hour", wantErr: true},
	}
	for _, tt := range tests {
		t.Run("group="+tt.in, func(t *testing.T) {
			g, err := parseGroup(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseGroup(%q) = %q, want 오류", tt.in, g.Name)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseGroup(%q): %v", tt.in, err)
			}
			if g.Name != tt.want {
				t.Errorf("parseGroup(%q) = %q, want %q", tt.in, g.Name, tt.want)
			}
		})
	}
}

func TestParseStatuses(t *testing.T) {
	tests := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{in: "", want: nil},
		{in: "running", want: []string{"running"}},
		{in: "running,completed", want: []string{"running", "completed"}},
		{in: " running , completed ", want: []string{"running", "completed"}},
		{in: "running,running", want: []string{"running"}},
		{in: "abandoned", want: []string{"abandoned"}},
		{in: "handoff", want: []string{"handoff"}},

		{in: "done", wantErr: true},
		{in: "running,done", wantErr: true},
		{in: "Running", wantErr: true},
	}
	for _, tt := range tests {
		t.Run("status="+tt.in, func(t *testing.T) {
			got, err := parseStatuses(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseStatuses(%q) = %v, want 오류", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseStatuses(%q): %v", tt.in, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("parseStatuses(%q) = %v, want %v", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("parseStatuses(%q) = %v, want %v", tt.in, got, tt.want)
				}
			}
		})
	}
}

// 데이터 디렉터리 우선순위가 데몬(resolveDataDir)과 어긋나면 CLI 가 데몬이 쓰는 것과
// 다른 파일을 읽고도 "데이터 없음" 이라고만 말한다.
func TestResolveLocalTargetPrecedence(t *testing.T) {
	stateDir := t.TempDir()
	statePath := filepath.Join(stateDir, "state.json")
	stateDataDir := filepath.Join(stateDir, "from-state")
	if err := installer.SaveState(statePath, &installer.State{
		StateSchemaVersion: installer.StateSchemaVersion,
		InstallationID:     "inst-1",
		Local:              installer.Local{DataDir: stateDataDir, RetentionDays: 30, StoreContent: true},
	}); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	t.Run("--data-dir 가 상태 파일을 이긴다", func(t *testing.T) {
		flagDir := t.TempDir()
		got, err := resolveLocalTarget(flagDir, statePath)
		if err != nil {
			t.Fatalf("resolveLocalTarget: %v", err)
		}
		if got.DataDir != flagDir {
			t.Errorf("DataDir = %q, want %q", got.DataDir, flagDir)
		}
		if got.DBPath != store.PathIn(flagDir) {
			t.Errorf("DBPath = %q, want %q", got.DBPath, store.PathIn(flagDir))
		}
	})

	t.Run("--data-dir 가 없으면 상태 파일", func(t *testing.T) {
		got, err := resolveLocalTarget("", statePath)
		if err != nil {
			t.Fatalf("resolveLocalTarget: %v", err)
		}
		if got.DataDir != stateDataDir {
			t.Errorf("DataDir = %q, want %q", got.DataDir, stateDataDir)
		}
		if got.State == nil {
			t.Fatal("State = nil, want 로드됨")
		}
	})

	t.Run("상태 파일이 없어도 에러가 아니다", func(t *testing.T) {
		dir := t.TempDir()
		got, err := resolveLocalTarget(dir, filepath.Join(dir, "없는-state.json"))
		if err != nil {
			t.Fatalf("resolveLocalTarget: %v", err)
		}
		if got.State != nil || got.StateErr != nil {
			t.Errorf("State=%v StateErr=%v, want 둘 다 nil (미설치)", got.State, got.StateErr)
		}
	})
}
