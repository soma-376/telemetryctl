package otlpdecode

import (
	"slices"
	"strings"
	"testing"

	"github.com/your-org/pulsemetry/internal/event"
)

// 이 파일이 이 패키지의 존재 이유다 (계획서 테스트 전략 §3, ADR 0003).
//
// 픽스처에 심어 둔 값들. 디코드 결과의 어느 문자열에도 이것들이 부분 문자열로 등장하면 안 된다.
const (
	fixtureProjectPath = "/Users/jy/dev/projects/soma-376/telemetryctl"
	fixtureFilePath    = "/Users/jy/dev/projects/soma-376/telemetryctl/internal/otlpdecode/decode.go"
	fixtureUserEmail   = "kjy02927@gmail.com"
	fixtureUserID      = "u_88213f0c9d"
	fixtureAccountUUID = "9f2c1d55-3a7e-4c18-9b02-6d41ee0a7788"
	fixtureOrgID       = "org_01HXYZQ7K3M9V2"
)

// TestNoFullPathsInDecodedEvents 는 계획서가 "가장 중요한 테스트" 로 지목한 것이다.
// project_hash·project_name 은 채워지되 전체 경로 문자열은 events 로 가는 구조체 어디에도
// 없어야 한다. 리플렉션으로 비공개 필드까지 훑으므로 "어디에도" 를 문자 그대로 검사한다.
func TestNoFullPathsInDecodedEvents(t *testing.T) {
	fixtures := []struct {
		name string
		kind PayloadKind
	}{
		{"logs_session_walkthrough.json", PayloadLogs},
		{"metrics_lines_of_code.json", PayloadMetrics},
		{"metrics_token_usage.json", PayloadMetrics},
	}

	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			res := decodeBoth(t, f.kind, f.name, testOptions())
			assertNoForbiddenStrings(t, "Events", res.Events)

			// Targets 는 파일 경로가 세션으로 흐르는 유일한 통로다. events 와 같은 규칙을
			// 지켜야 한다 — 여기가 뚫리면 session_files·tool_events 에 전체 경로가 실린다.
			assertNoForbiddenStrings(t, "Targets", res.Targets)

			// dedup_key 도 결과의 일부다. 해시라 되돌릴 수 없지만 눈으로 확인한다.
			for _, e := range res.Events {
				assertNoForbiddenStrings(t, "DedupKey", e.DedupKey())
			}
		})
	}
}

// TestPrivacyScannerActuallyScans 는 위 단언들의 전제를 지킨다.
// allStrings 가 조용히 아무것도 못 보게 되면 프라이버시 테스트 전부가 무의미하게 통과한다.
func TestPrivacyScannerActuallyScans(t *testing.T) {
	res := decodeBoth(t, PayloadLogs, "logs_session_walkthrough.json", testOptions())
	found := map[string]bool{}
	for _, s := range allStrings(res.Events) {
		found[s] = true
	}
	// 각각 Attributes(비공개 아님)·Measures.ErrorType·최상위 필드에서 와야 한다.
	for _, want := range []string{"telemetryctl", "rate_limit_error", "claude_code", "iTerm.app"} {
		if !found[want] {
			t.Fatalf("스캐너가 %q 를 못 봤다 — collectStrings 가 구조체를 못 훑고 있다", want)
		}
	}
	if len(allStrings(res.Contents)) == 0 {
		t.Fatal("스캐너가 Contents 를 못 훑는다")
	}
	// Targets 단언도 같은 전제 위에 있다. 스캐너가 여기를 못 보면 경로 유출 검사가 무의미하다.
	targetStrings := allStrings(res.Targets)
	if len(targetStrings) == 0 {
		t.Fatal("스캐너가 Targets 를 못 훑는다 — 대상 파일이 있는 픽스처인데 문자열이 하나도 없다")
	}
	if !slices.Contains(targetStrings, "decode.go") {
		t.Fatalf("스캐너가 Target 의 basename 을 못 봤다: %q", targetStrings)
	}
}

// TestProjectIsHashPlusBasenameOnly 는 경로가 버려지는 게 아니라 정확히 해시+basename 으로
// 살아 있음을 확인한다. 위 테스트만 있으면 "경로 속성을 통째로 무시" 로도 통과해 버린다.
func TestProjectIsHashPlusBasenameOnly(t *testing.T) {
	res := decodeBoth(t, PayloadLogs, "logs_session_walkthrough.json", testOptions())
	want := event.NormalizePath(fixtureProjectPath)

	for _, e := range res.Events {
		if e.Attr.ProjectHash != want.Hash {
			t.Fatalf("%s: project_hash = %q, want %q", e.Name, e.Attr.ProjectHash, want.Hash)
		}
		if e.Attr.ProjectName != "telemetryctl" {
			t.Fatalf("%s: project_name = %q, want basename", e.Name, e.Attr.ProjectName)
		}
		if strings.ContainsAny(e.Attr.ProjectName, `/\`) {
			t.Fatalf("%s: project_name 에 경로 구분자가 있다: %q", e.Name, e.Attr.ProjectName)
		}
	}
	if len(want.Hash) != 64 {
		t.Fatalf("해시 길이 = %d, want 64 (sha256 hex)", len(want.Hash))
	}
}

// TestNoIdentityAttributesAnywhere 는 user.*·organization.id 가 Event 에도 Content 에도
// 흘러들지 않음을 단언한다. allowlist 에 자리가 없다는 사실을 테스트로 고정한다.
func TestNoIdentityAttributesAnywhere(t *testing.T) {
	identities := map[string]string{
		"user.email":        fixtureUserEmail,
		"user.id":           fixtureUserID,
		"user.account_uuid": fixtureAccountUUID,
		"organization.id":   fixtureOrgID,
	}

	fixtures := []struct {
		name string
		kind PayloadKind
	}{
		{"logs_session_walkthrough.json", PayloadLogs},
		{"metrics_lines_of_code.json", PayloadMetrics},
		{"metrics_token_usage.json", PayloadMetrics},
	}

	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			res := decodeBoth(t, f.kind, f.name, testOptions())
			// 원문(Contents)·대상 파일(Targets)까지 포함해 검사한다.
			// 신원 값은 로컬 저장에도 자리가 없다.
			haystack := append(allStrings(res.Events), allStrings(res.Contents)...)
			haystack = append(haystack, allStrings(res.Targets)...)
			for key, value := range identities {
				for _, s := range haystack {
					if strings.Contains(s, value) {
						t.Errorf("%s 의 값이 디코드 결과에 남았다: %q 안의 %q", key, s, value)
					}
				}
			}
		})
	}
}

// TestRawContentIsSeparatedFromEvents 는 두 규칙의 경계를 못 박는다.
//
// 원문은 event_content 로만 가고 events 로는 길이·바이트 수만 간다. tool_input 원문에는
// 전체 경로가 그대로 남는다 — 세션 조립기가 파일별 변경을 만들려면 그 경로가 필요하고,
// ADR 0003·0008이 그 원문을 16KB 캡·400일 보존·상위 미전달 조건으로 로컬에만 허용했기 때문이다.
// 이 단언이 깨진다면 events 와 event_content 의 역할이 섞였다는 뜻이다.
func TestRawContentIsSeparatedFromEvents(t *testing.T) {
	res := decodeBoth(t, PayloadLogs, "logs_session_walkthrough.json", testOptions())

	index := indexOfEventID(res, "evt_tool_0002")
	contents := contentsOf(res, index)
	input, ok := contents[event.ContentToolInput]
	if !ok {
		t.Fatal("tool_input 원문이 없다 — 세션 조립기가 파일 변경 목록을 만들 수 없다")
	}
	if !strings.Contains(input.Body, fixtureFilePath) {
		t.Fatalf("tool_input 원문이 원본 그대로가 아니다: %q", input.Body)
	}

	// 같은 이벤트의 Event 쪽에는 그 경로가 없다.
	assertNoForbiddenStrings(t, "Events[tool_result]", res.Events[index])

	// events 에는 길이만 남는다.
	if !res.Events[index].Measure.ToolInputBytes.Valid() {
		t.Error("tool_input_bytes 가 비었다")
	}

	// 같은 원문에서 파생된 Target 은 반대 규칙을 따른다 — 해시+basename 뿐이다.
	// 두 규칙이 같은 바이트를 서로 다르게 다루는 지점이라 여기가 흐려지면 ADR 0003 이 무너진다.
	var target Target
	for _, tg := range res.Targets {
		if tg.EventIndex == index {
			target = tg
		}
	}
	if target.Path.Hash == "" {
		t.Fatal("tool_input 에 file_path 가 있는데 Target 이 없다 — session_files 가 빈다")
	}
	assertNoForbiddenStrings(t, "Targets[tool_result]", target)
	if target.Path.Name != "decode.go" {
		t.Errorf("target basename = %q, want decode.go", target.Path.Name)
	}
}

// TestScrubbedPayloadCarriesNoContent 는 상위 전달 쪽 규칙이다.
// 회사 기본 Privacy(전부 false)면 원문·tool details 가 전달 바이트에서 사라져야 한다.
func TestScrubbedPayloadCarriesNoContent(t *testing.T) {
	for _, enc := range []Encoding{EncodingJSON, EncodingProtobuf} {
		t.Run(enc.String(), func(t *testing.T) {
			raw := payloadIn(t, PayloadLogs, "logs_session_walkthrough.json", enc)
			out, _, err := Scrub(PayloadLogs, raw, enc, PolicyFromPrivacy(defaultPrivacy()))
			if err != nil {
				t.Fatalf("Scrub: %v", err)
			}
			// 전달 바이트를 다시 디코드해 원문이 하나도 딸려오지 않는지 본다.
			res, err := DecodeLogs(out, enc, testOptions())
			if err != nil {
				t.Fatalf("전달 바이트 재디코드: %v", err)
			}
			if len(res.Contents) != 0 {
				t.Fatalf("전달 바이트에 원문이 %d건 남았다: %+v", len(res.Contents), res.Contents)
			}
			// fixtureProjectPath(cwd 리소스 속성)는 여기 없다. manifest Privacy 가 금지하지
			// 않은 속성이라 그대로 전달하는 것이 맞다 — 직결 시절에도 회사가 받던 값이다.
			// 로컬 저장 쪽에서만 해시+basename 으로 줄인다.
			for _, forbidden := range []string{fixtureFilePath, fixtureUserEmail} {
				if strings.Contains(string(out), forbidden) {
					t.Errorf("전달 바이트에 %q 가 남았다", forbidden)
				}
			}
		})
	}
}

var forbiddenInEvents = []string{
	fixtureProjectPath,
	fixtureFilePath,
	fixtureUserEmail,
	fixtureUserID,
	fixtureAccountUUID,
	fixtureOrgID,
	"/Users/",
}

func assertNoForbiddenStrings(t *testing.T, label string, v any) {
	t.Helper()
	for _, s := range allStrings(v) {
		for _, forbidden := range forbiddenInEvents {
			if strings.Contains(s, forbidden) {
				t.Errorf("%s: 금지 문자열 %q 가 %q 안에 남았다", label, forbidden, s)
			}
		}
	}
}
