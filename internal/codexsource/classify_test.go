package codexsource

import (
	"encoding/json"
	"testing"
)

func testRecord(id int64, body, message, turn, source string) Record {
	s := map[string]any{"form": "UserMessage", "owner_source": source}
	b, _ := json.Marshal(s)
	return Record{ID: id, Owner: "owner", Type: "event_msg", EventTime: 1_700_000_000_250_000_000,
		Body: body, BodyHash: hashBody(body), MessageID: message, TurnID: turn,
		Completeness: "complete", CompletenessEvidence: "native whole text field", Structure: string(b)}
}

func testPrompt() Prompt {
	return Prompt{ID: 1, Owner: "owner", Body: "hello", MessageID: "m1", TurnID: "t1", Completeness: "complete", StartedAt: 1_700_000_000, EndedAt: 1_700_000_000, Source: "{}"}
}

func TestClassifyPriorityAndConflicts(t *testing.T) {
	p := testPrompt()
	strong := testRecord(1, "hello", "m1", "t1", "cli")
	weak := testRecord(2, "hello", "m2", "t2", "subagent")
	if d := Classify(p, []Record{strong, weak}, false); d.Label != "client_submitted" || d.LinkMethod != "message_id" || d.LinkedRecordID != 1 {
		t.Fatalf("강한 ID 우선 = %+v", d)
	}
	conflicting := testRecord(3, "different", "m1", "t1", "cli")
	if d := Classify(p, []Record{conflicting}, false); d.Label != "unknown" || d.LinkState != "conflict" || d.LinkedRecordID != 0 {
		t.Fatalf("본문 충돌 = %+v", d)
	}
	structureConflict := testRecord(4, "hello", "m1", "t1", "unsupported")
	if d := Classify(p, []Record{structureConflict}, false); d.Label != "unknown" || d.LinkState != "unique" || d.LinkedRecordID != 4 {
		t.Fatalf("출처만 충돌한 연결 = %+v", d)
	}
}

func TestClassifyStrictPrefixNeedsOriginalProof(t *testing.T) {
	p := testPrompt()
	p.MessageID = ""
	p.TurnID = ""
	p.Body = "hel"
	p.Completeness = "truncated"
	r := testRecord(5, "hello", "", "", "cli")
	p.CompletenessEvidence = `{"source":"decoder_utf8_cap","cap_bytes":3,"stored_bytes":3,"original_bytes":5,"original_sha256":"wrong"}`
	if d := Classify(p, []Record{r}, false); d.Label != "unknown" || d.LinkMethod != "" {
		t.Fatalf("가짜 proof = %+v", d)
	}
	p.CompletenessEvidence = encode(map[string]any{"source": "decoder_utf8_cap", "cap_bytes": 3, "stored_bytes": 3, "original_bytes": 5, "original_sha256": hashBody("hello")})
	if d := Classify(p, []Record{r}, false); d.Label != "client_submitted" || d.LinkMethod != "stored_body_prefix" {
		t.Fatalf("입증된 prefix = %+v", d)
	}
	p.StartedAt = 0
	p.EndedAt = 0
	if d := Classify(p, []Record{r}, false); d.LinkMethod != "" {
		t.Fatalf("시간 경계 없는 body match = %+v", d)
	}
}

func TestClassifyAmbiguousConsensusAndTimeBoundary(t *testing.T) {
	p := testPrompt()
	p.MessageID = ""
	p.TurnID = ""
	a := testRecord(1, "hello", "", "", "cli")
	b := testRecord(2, "hello", "", "", "cli")
	if d := Classify(p, []Record{a, b}, false); d.Label != "client_submitted" || d.LinkState != "ambiguous" || d.LinkedRecordID != 0 {
		t.Fatalf("동일 출처 복수 후보 = %+v", d)
	}
	b.Structure = encode(map[string]any{"form": "UserMessage", "owner_source": "subagent"})
	if d := Classify(p, []Record{a, b}, false); d.Label != "unknown" || d.LinkState != "ambiguous" {
		t.Fatalf("출처 충돌 복수 후보 = %+v", d)
	}
	a.EventTime = 1_700_000_001_000_000_000
	if d := Classify(p, []Record{a}, false); d.LinkMethod != "" {
		t.Fatalf("종료 다음 초 복사 후보 = %+v", d)
	}
}

func TestClassifyActualCLIExecSourceWithDuplicateRepresentations(t *testing.T) {
	p := testPrompt()
	p.MessageID = ""
	p.TurnID = ""
	p.EndedAt = 0 // 실제 CLI 실행 중에는 종료 경계가 아직 없다.
	a := testRecord(1, "hello", "m1", "t1", "exec")
	b := testRecord(2, "hello", "m2", "", "exec")
	b.Type = "response_item"
	b.EventTime++ // 두 표현의 기록 시각이 조금 달라도 출처 합의는 가능하다.
	if d := Classify(p, []Record{a, b}, false); d.Label != "client_submitted" || d.LinkState != "ambiguous" || d.LinkedRecordID != 0 {
		t.Fatalf("CLI exec의 두 구조화 표현 = %+v", d)
	}
	b.Structure = encode(map[string]any{"form": "user.text", "owner_source": "exec", "internal_task": true})
	if d := Classify(p, []Record{a, b}, false); d.Label != "unknown" || d.LinkState != "ambiguous" {
		t.Fatalf("exec 내부 근거 충돌 = %+v", d)
	}
}
