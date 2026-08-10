package session

import (
	"math/rand"
	"testing"
)

// 계획서 「테스트 전략」이 지정한 불변식 — 파일별 배분 합계가 세션 합계를 초과하지 않는다.
// 세션 합계는 메트릭에서 직접 받아 정확하고 파일별 배분만 근사이므로, 근사가 정확한 값을
// 넘어서는 순간 화면의 두 숫자가 서로 모순된다.
func TestFileLinesNeverExceedSessionTotal(t *testing.T) {
	const start = 1_700_000_000

	tests := []struct {
		name       string
		events     []Input
		wantAdded  int64 // 세션 합계
		wantFiles  int
		wantUnattr int64
	}{
		{
			name: "편집 먼저, 메트릭 나중 (실제 도착 순서)",
			events: []Input{
				logEv("s1", "claude_code.tool_result", start, tool("Edit"), target("/repo/a.go"), success(true)),
				metricEv("s1", "claude_code.lines_of_code.count", start+60, 30, typ("added")),
			},
			wantAdded: 30, wantFiles: 1, wantUnattr: 0,
		},
		{
			name: "메트릭 먼저, 편집 나중",
			events: []Input{
				metricEv("s1", "claude_code.lines_of_code.count", start, 30, typ("added")),
				logEv("s1", "claude_code.tool_result", start+1, tool("Edit"), target("/repo/a.go"), success(true)),
			},
			wantAdded: 30, wantFiles: 1, wantUnattr: 0,
		},
		{
			name: "메트릭만 오면 전부 미배분",
			events: []Input{
				metricEv("s1", "claude_code.lines_of_code.count", start, 42, typ("added")),
			},
			wantAdded: 42, wantFiles: 0, wantUnattr: 42,
		},
		{
			name: "편집만 오면 파일은 있고 라인은 0",
			events: []Input{
				logEv("s1", "claude_code.tool_result", start, tool("Write"), target("/repo/a.go"), success(true)),
			},
			wantAdded: 0, wantFiles: 1, wantUnattr: 0,
		},
		{
			name: "한 구간에 여러 파일 — 나눠 귀속",
			events: []Input{
				logEv("s1", "claude_code.tool_result", start, tool("Edit"), target("/repo/a.go"), success(true)),
				logEv("s1", "claude_code.tool_result", start+1, tool("Edit"), target("/repo/b.go"), success(true)),
				logEv("s1", "claude_code.tool_result", start+2, tool("Edit"), target("/repo/c.go"), success(true)),
				metricEv("s1", "claude_code.lines_of_code.count", start+60, 10, typ("added")),
			},
			wantAdded: 10, wantFiles: 3, wantUnattr: 0,
		},
		{
			name: "added·removed 두 데이터포인트가 같은 구간을 본다",
			events: []Input{
				logEv("s1", "claude_code.tool_result", start, tool("Edit"), target("/repo/a.go"), success(true)),
				metricEv("s1", "claude_code.lines_of_code.count", start+60, 12, typ("added")),
				metricEv("s1", "claude_code.lines_of_code.count", start+60, 5, typ("removed")),
			},
			wantAdded: 12, wantFiles: 1, wantUnattr: 0,
		},
		{
			name: "실패한 편집은 파일을 만들지 않는다",
			events: []Input{
				logEv("s1", "claude_code.tool_result", start, tool("Edit"), target("/repo/a.go"), success(false)),
				metricEv("s1", "claude_code.lines_of_code.count", start+60, 20, typ("added")),
			},
			wantAdded: 20, wantFiles: 0, wantUnattr: 20,
		},
		{
			name: "읽기는 파일 변경이 아니다",
			events: []Input{
				logEv("s1", "claude_code.tool_result", start, tool("Read"), target("/repo/a.go"), success(true)),
				metricEv("s1", "claude_code.lines_of_code.count", start+60, 20, typ("added")),
			},
			wantAdded: 20, wantFiles: 0, wantUnattr: 20,
		},
		{
			name: "명령 실행은 파일 변경이 아니다",
			events: []Input{
				logEv("s1", "claude_code.tool_result", start, tool("Bash"), target("go"), success(true)),
				metricEv("s1", "claude_code.lines_of_code.count", start+60, 20, typ("added")),
			},
			wantAdded: 20, wantFiles: 0, wantUnattr: 20,
		},
		{
			name: "음수 증분은 무시한다",
			events: []Input{
				logEv("s1", "claude_code.tool_result", start, tool("Edit"), target("/repo/a.go"), success(true)),
				metricEv("s1", "claude_code.lines_of_code.count", start+60, -5, typ("added")),
			},
			wantAdded: 0, wantFiles: 1, wantUnattr: 0,
		},
		{
			name: "여러 구간이 이어져도 합계가 유지된다",
			events: []Input{
				logEv("s1", "claude_code.tool_result", start, tool("Edit"), target("/repo/a.go"), success(true)),
				metricEv("s1", "claude_code.lines_of_code.count", start+60, 10, typ("added")),
				logEv("s1", "claude_code.tool_result", start+70, tool("Edit"), target("/repo/b.go"), success(true)),
				metricEv("s1", "claude_code.lines_of_code.count", start+120, 7, typ("added")),
			},
			wantAdded: 17, wantFiles: 2, wantUnattr: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := New()
			for _, e := range tt.events {
				a.Add(e)
			}
			s, _ := a.Session("s1")

			assertLineInvariant(t, s)
			if s.LinesAdded != tt.wantAdded {
				t.Errorf("세션 lines_added = %d, want %d", s.LinesAdded, tt.wantAdded)
			}
			if len(s.Files) != tt.wantFiles {
				t.Errorf("파일 %d개, want %d개: %+v", len(s.Files), tt.wantFiles, s.Files)
			}
			if s.Diag.UnattributedLinesAdded != tt.wantUnattr {
				t.Errorf("미배분 = %d, want %d", s.Diag.UnattributedLinesAdded, tt.wantUnattr)
			}
		})
	}
}

// 도착 순서를 뒤섞어도 불변식이 깨지지 않는지. 배분은 근사라 순서마다 결과가 달라지지만
// "합계를 넘지 않는다"는 어떤 순서에서도 성립해야 한다.
func TestLineInvariantHoldsUnderShuffledArrival(t *testing.T) {
	const start = 1_700_000_000

	base := []Input{
		logEv("s1", "claude_code.tool_result", start, tool("Edit"), target("/repo/a.go"), success(true)),
		logEv("s1", "claude_code.tool_result", start+1, tool("Write"), target("/repo/b.go"), success(true)),
		logEv("s1", "claude_code.tool_result", start+2, tool("Edit"), target("/repo/a.go"), success(true)),
		logEv("s1", "claude_code.tool_result", start+3, tool("Read"), target("/repo/c.go"), success(true)),
		metricEv("s1", "claude_code.lines_of_code.count", start+60, 37, typ("added")),
		metricEv("s1", "claude_code.lines_of_code.count", start+60, 11, typ("removed")),
		metricEv("s1", "claude_code.lines_of_code.count", start+120, 5, typ("added")),
		logEv("s1", "claude_code.tool_result", start+100, tool("Edit"), target("/repo/b.go"), success(true)),
	}

	rng := rand.New(rand.NewSource(20260810))
	for i := range 200 {
		shuffled := append([]Input(nil), base...)
		rng.Shuffle(len(shuffled), func(x, y int) { shuffled[x], shuffled[y] = shuffled[y], shuffled[x] })

		a := New()
		for _, e := range shuffled {
			a.Add(e)
		}
		s, _ := a.Session("s1")

		assertLineInvariant(t, s)
		// 세션 합계는 배분과 무관하게 항상 메트릭 그대로다.
		if s.LinesAdded != 42 || s.LinesRemoved != 11 {
			t.Fatalf("순열 %d: 세션 합계가 흔들림 added=%d removed=%d", i, s.LinesAdded, s.LinesRemoved)
		}
	}
}

// 파일 목록은 화면 쿼리와 같은 순서여야 한다 — ORDER BY lines_added+lines_removed DESC.
func TestFileRowsOrderedByChangeVolume(t *testing.T) {
	const start = 1_700_000_000
	a := New()
	// small.go 한 번, big.go 세 번 편집 → 같은 구간의 라인이 편집 횟수 비율로 나뉜다.
	a.Add(logEv("s1", "claude_code.tool_result", start, tool("Edit"), target("/repo/small.go"), success(true)))
	for i := range 3 {
		a.Add(logEv("s1", "claude_code.tool_result", start+int64(i)+1, tool("Edit"), target("/repo/big.go"), success(true)))
	}
	a.Add(metricEv("s1", "claude_code.lines_of_code.count", start+60, 40, typ("added")))

	s, _ := a.Session("s1")
	assertLineInvariant(t, s)
	if len(s.Files) != 2 {
		t.Fatalf("파일 %d개, want 2", len(s.Files))
	}
	if s.Files[0].Name != "big.go" {
		t.Fatalf("첫 행 = %q, want big.go (변경량 내림차순)", s.Files[0].Name)
	}
	if s.Files[0].Edits != 3 || s.Files[1].Edits != 1 {
		t.Fatalf("edits 집계가 틀림: %+v", s.Files)
	}
	if s.Files[0].Ext != "go" {
		t.Fatalf("ext = %q", s.Files[0].Ext)
	}
	// 전체 경로는 어디에도 남지 않는다 (ADR 0003).
	for _, f := range s.Files {
		if f.Name != "big.go" && f.Name != "small.go" {
			t.Fatalf("basename 이 아닌 값: %q", f.Name)
		}
		if f.PathHash == "" || len(f.PathHash) != 64 {
			t.Fatalf("file_path_hash 가 sha256 hex 가 아님: %q", f.PathHash)
		}
	}
}

// 원장 단독 검사 — 배분은 더하기가 아니라 이동이라는 성질을 직접 본다.
func TestLedgerDistributionIsATransfer(t *testing.T) {
	var l lineLedger
	l.noteEdit("a")
	l.noteEdit("b")
	l.noteEdit("c")
	l.addAdded(10) // 3으로 안 나눠떨어진다

	sum := l.assignedTo("a").added + l.assignedTo("b").added + l.assignedTo("c").added
	if sum+l.added.unassigned != l.added.total {
		t.Fatalf("배분 %d + 미배분 %d != 합계 %d", sum, l.added.unassigned, l.added.total)
	}
	if l.added.unassigned != 0 {
		t.Fatalf("나머지가 남음: %d", l.added.unassigned)
	}
	// 나머지는 앞쪽에 1씩 얹는다 — 4/3/3
	if got := l.assignedTo("a").added; got != 4 {
		t.Fatalf("a = %d, want 4", got)
	}
}

func TestLedgerIgnoresEmptyPathHash(t *testing.T) {
	var l lineLedger
	l.noteEdit("") // 툴 상세가 꺼져 경로가 없는 경우
	l.addAdded(10)

	if l.added.unassigned != 10 {
		t.Fatalf("미배분 = %d, want 10 (귀속 대상이 없다)", l.added.unassigned)
	}
	if len(l.assigned) != 0 {
		t.Fatalf("빈 해시에 배분됨: %+v", l.assigned)
	}
}
