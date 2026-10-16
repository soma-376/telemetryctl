package vendorlimit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadClaudeCredential(t *testing.T) {
	t.Parallel()
	expiry := testNow.Add(2 * time.Hour)

	tests := []struct {
		name       string
		body       string
		write      bool
		wantReason Reason
		wantPlan   string
		wantExpiry time.Time
	}{
		{
			name:       "정상 파일에서 토큰·플랜·만료를 읽는다",
			body:       claudeCredentialJSON(claudeCanary, expiry, "max"),
			write:      true,
			wantPlan:   "max",
			wantExpiry: expiry.UTC().Truncate(time.Millisecond),
		},
		{
			name:  "만료 시각이 0 이면 모름으로 둔다",
			body:  claudeCredentialJSON(claudeCanary, time.Time{}, "pro"),
			write: true, wantPlan: "pro",
		},
		{
			name:  "모르는 필드가 늘어도 깨지지 않는다",
			body:  `{"claudeAiOauth":{"accessToken":"` + claudeCanary + `","newField":{"a":1}},"otherTop":true}`,
			write: true,
		},
		{
			name:       "파일이 없으면 credential_missing",
			write:      false,
			wantReason: ReasonCredentialMissing,
		},
		{
			name:       "JSON 이 아니면 credential_malformed",
			body:       "not json at all",
			write:      true,
			wantReason: ReasonCredentialMalformed,
		},
		{
			name:       "빈 파일은 credential_malformed",
			body:       "",
			write:      true,
			wantReason: ReasonCredentialMalformed,
		},
		{
			name:       "claudeAiOauth 가 없으면 credential_malformed",
			body:       `{"other":{"accessToken":"x"}}`,
			write:      true,
			wantReason: ReasonCredentialMalformed,
		},
		{
			name:       "accessToken 이 비면 credential_malformed",
			body:       `{"claudeAiOauth":{"accessToken":"   "}}`,
			write:      true,
			wantReason: ReasonCredentialMalformed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := newHome(t)
			if tc.write {
				writeClaudeCredential(t, home, tc.body)
			}

			cred, err := loadClaudeCredential(home)
			if tc.wantReason != "" {
				if err == nil {
					t.Fatalf("실패해야 하는데 성공했다: %+v", cred)
				}
				if got := reasonOf(err, ReasonInternal); got != tc.wantReason {
					t.Fatalf("reason = %q, want %q (%v)", got, tc.wantReason, err)
				}
				// 실패 경로의 오류 문자열에는 토큰도 홈 경로도 없어야 한다.
				assertNoSecret(t, "오류 문자열", []string{err.Error()}, claudeCanary, home)
				return
			}
			if err != nil {
				t.Fatalf("loadClaudeCredential: %v", err)
			}
			if cred.token.reveal() != claudeCanary {
				t.Errorf("토큰을 못 읽었다: %q", cred.token.reveal())
			}
			if cred.plan != tc.wantPlan {
				t.Errorf("plan = %q, want %q", cred.plan, tc.wantPlan)
			}
			if !cred.expiresAt.Equal(tc.wantExpiry) {
				t.Errorf("expiresAt = %v, want %v", cred.expiresAt, tc.wantExpiry)
			}
		})
	}
}

// 권한 오류는 파일 부재와 반드시 구분되어야 한다 — 사용자가 할 일이 다르다.
func TestReadCredentialFile은권한오류를따로구분한다(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	path := claudeCredentialPath(home)
	writeClaudeCredential(t, home, claudeCredentialJSON(claudeCanary, testNow, "max"))
	makeUnreadable(t, path)

	_, err := loadClaudeCredential(home)
	if err == nil {
		t.Fatal("권한 0000 파일이 읽혔다")
	}
	if got := reasonOf(err, ReasonInternal); got != ReasonCredentialUnreadable {
		t.Fatalf("reason = %q, want %q (%v)", got, ReasonCredentialUnreadable, err)
	}
	assertNoSecret(t, "오류 문자열", []string{err.Error()}, claudeCanary, home)
}

// 자격증명 로더는 파일을 절대 바꾸지 않는다 (읽기 전용 경계).
func TestLoad는자격증명파일을수정하지않는다(t *testing.T) {
	t.Parallel()
	home := newHome(t)
	body := claudeCredentialJSON(claudeCanary, testNow.Add(-time.Hour), "max")
	writeClaudeCredential(t, home, body)
	path := claudeCredentialPath(home)

	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := loadClaudeCredential(home); err != nil {
		t.Fatalf("loadClaudeCredential: %v", err)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("mtime 이 바뀌었다: %v → %v", before.ModTime(), after.ModTime())
	}
	if after.Mode() != before.Mode() {
		t.Errorf("권한이 바뀌었다: %v → %v", before.Mode(), after.Mode())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Error("내용이 바뀌었다 — 이 패키지는 자격증명 파일을 쓰지 않는다")
	}
	// 만료된 토큰이라도 갱신 파일(*.new 등)을 만들지 않는다.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("디렉터리에 파일이 늘었다: %v", entries)
	}
}

func TestDisplayPath는홈을접는다(t *testing.T) {
	t.Parallel()
	home := filepath.Join("/Users", "jy")
	if got := displayPath(home, filepath.Join(home, ".codex", "auth.json")); !strings.HasPrefix(got, "~/") {
		t.Errorf("displayPath = %q", got)
	}
	if strings.Contains(displayPath(home, filepath.Join(home, ".codex", "auth.json")), "jy") {
		t.Error("사용자 이름이 남았다")
	}
	// 홈 밖의 경로는 basename 만 남긴다.
	if got := displayPath(home, filepath.Join("/etc", "secret", "auth.json")); got != "auth.json" {
		t.Errorf("displayPath = %q, want auth.json", got)
	}
}

func TestReasonOf는우리오류가아니면기본값을준다(t *testing.T) {
	t.Parallel()
	if got := reasonOf(errors.New("남의 오류"), ReasonNetwork); got != ReasonNetwork {
		t.Errorf("reasonOf = %q", got)
	}
	wrapped := errors.Join(errors.New("바깥"), credErr(ReasonTokenExpired, "안쪽"))
	if got := reasonOf(wrapped, ReasonInternal); got != ReasonTokenExpired {
		t.Errorf("감싼 오류에서 reason 을 못 찾았다: %q", got)
	}
}
