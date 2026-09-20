package otlpdecode

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	logscolpb "go.opentelemetry.io/proto/otlp/collector/logs/v1"

	"github.com/your-org/pulsemetry/internal/contract"
	"github.com/your-org/pulsemetry/internal/event"
)

const patchFixture = "*** Begin Patch\n*** Add File: src/new.go\n+hello\n*** Update File: src/existing.go\n@@\n-old\n+new\n*** Delete File: src/old.go\n*** Update File: src/before.go\n*** Move to: src/after.go\n@@\n-old\n+new\n*** End Patch"

func TestPatchFileOperations(t *testing.T) {
	files, err := parsePatch(patchFixture)
	if err != nil {
		t.Fatal(err)
	}
	want := []Target{
		{RawPath: "src/new.go", Operation: "create", Additions: event.Some[int64](1), Deletions: event.Some[int64](0)},
		{RawPath: "src/existing.go", Operation: "modify", Additions: event.Some[int64](1), Deletions: event.Some[int64](1)},
		{RawPath: "src/old.go", Operation: "delete", Additions: event.Some[int64](0)},
		{RawPath: "src/after.go", Operation: "rename", RenamedFrom: "src/before.go", Additions: event.Some[int64](1), Deletions: event.Some[int64](1)},
	}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("files=%+v", files)
	}
	quoted, _ := json.Marshal(patchFixture)
	wrapped, _ := json.Marshal(map[string]string{"input": patchFixture})
	for _, input := range []string{string(quoted), string(wrapped), strings.ReplaceAll(patchFixture, "\n", "\r\n")} {
		got, err := parsePatch(input)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("wrapped patch: files=%+v err=%v", got, err)
		}
	}
}

func TestPatchLineCounts(t *testing.T) {
	for _, tc := range []struct {
		name, body     string
		added, removed int64
		unknownRemoved bool
	}{
		{name: "create_blank_and_header_text", body: "*** Add File: a\n+hello\n+\n+*** Delete File: b", added: 3},
		{name: "multiple_blocks", body: "*** Update File: a\n@@ function\n context\n-old\n-\n+new\n+\n@@ next\n +context\n -context\n\n+tail\n*** End of File", added: 3, removed: 2},
		{name: "insert_only", body: "*** Update File: a\n context\n+new", added: 1},
		{name: "remove_only", body: "*** Update File: a\n-old\n context", removed: 1},
		{name: "pure_rename", body: "*** Update File: a\n*** Move to: b"},
		{name: "rename_with_context", body: "*** Update File: a\n*** Move to: b\n@@\n context"},
		{name: "rename_with_edit", body: "*** Update File: a\n*** Move to: b\n@@\n-old\n+new\n+extra", added: 2, removed: 1},
		{name: "whole_file_delete", body: "*** Delete File: a", unknownRemoved: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := "*** Begin Patch\n" + tc.body + "\n*** End Patch"
			for _, newline := range []string{"\n", "\r\n"} {
				files, err := parsePatch(strings.ReplaceAll(input, "\n", newline))
				if err != nil || len(files) != 1 {
					t.Fatalf("files=%+v err=%v", files, err)
				}
				added, addedOK := files[0].Additions.Get()
				removed, removedOK := files[0].Deletions.Get()
				if !addedOK || added != tc.added || removedOK != !tc.unknownRemoved || removed != tc.removed {
					t.Fatalf("added=%d (%v) removed=%d (%v)", added, addedOK, removed, removedOK)
				}
			}
		})
	}
}

func TestPatchRejectsIncompleteOrInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        error
	}{
		{"truncated", strings.TrimSuffix(patchFixture, "*** End Patch"), errPatchTruncated},
		{"invalid_json", `{"input":`, errPatchInvalid},
		{"missing_path", "*** Begin Patch\n*** Delete File: \n*** End Patch", errPatchInvalid},
		{"invalid_body", "*** Begin Patch\n*** Add File: a\nnot a patch line\n*** End Patch", errPatchInvalid},
		{"empty_hunk", "*** Begin Patch\n*** Update File: a\n@@\n*** End Patch", errPatchInvalid},
		{"empty_add", "*** Begin Patch\n*** Add File: a\n*** End Patch", errPatchInvalid},
		{"duplicate_file", "*** Begin Patch\n*** Delete File: a\n*** Delete File: a\n*** End Patch", errPatchInvalid},
		{"trailing_data", patchFixture + "\nnot part of patch", errPatchTruncated},
		{"invalid_later_file", strings.Replace(patchFixture, "*** Delete File:", "*** Unknown File:", 1), errPatchInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files, err := parsePatch(tc.input)
			if !errors.Is(err, tc.want) || len(files) != 0 {
				t.Fatalf("files=%+v err=%v", files, err)
			}
		})
	}
	// 추가하는 코드에 패치 헤더 문자열이 있어도 새 파일로 취급하지 않는다.
	files, err := parsePatch("*** Begin Patch\n*** Add File: a\n+*** Delete File: b\n*** End Patch")
	if err != nil || len(files) != 1 || files[0].RawPath != "a" {
		t.Fatalf("files=%+v err=%v", files, err)
	}
}

func TestPatchPathUsesOnlyKnownWorkspace(t *testing.T) {
	for _, tc := range []struct{ raw, cwd, want string }{
		{"src/a.go", `C:\work\project`, "C:/work/project/src/a.go"},
		{`src\a.go`, "/work/project", "/work/project/src/a.go"},
		{`C:\other\a.go`, "/work/project", `C:\other\a.go`},
		{"a.go", `\\server\share`, "//server/share/a.go"},
		{"src/a.go", "", "src/a.go"},
		{"src/a.go", "relative", "src/a.go"},
		{"/other/a.go", `C:\work`, "/other/a.go"},
	} {
		if got := patchPath(tc.raw, tc.cwd); got != tc.want {
			t.Errorf("path(%q,%q)=%q want %q", tc.raw, tc.cwd, got, tc.want)
		}
	}
}

func TestPatchMappingBeforeContentCap(t *testing.T) {
	for _, tc := range []struct {
		name, vendor, eventName, input, decision string
		success                                  event.Opt[bool]
		wantFiles                                int
		diag                                     PatchDiagnostics
	}{
		{name: "large", vendor: "codex", eventName: "codex.tool_result", input: strings.Replace(patchFixture, "+hello", "+"+strings.Repeat("x", 20000), 1), success: event.Some(true), wantFiles: 4},
		{name: "failed", vendor: "codex", eventName: "codex.tool_result", input: patchFixture, success: event.Some(false), diag: PatchDiagnostics{Unconfirmed: 1}},
		{name: "unknown_success", vendor: "codex", eventName: "codex.tool_result", input: patchFixture, diag: PatchDiagnostics{Unconfirmed: 1}},
		{name: "rejected", vendor: "codex", eventName: "codex.tool_result", input: patchFixture, success: event.Some(true), decision: "reject", diag: PatchDiagnostics{Unconfirmed: 1}},
		{name: "missing", vendor: "codex", eventName: "codex.tool_result", success: event.Some(true), diag: PatchDiagnostics{MissingInput: 1}},
		{name: "truncated", vendor: "codex", eventName: "codex.tool_result", input: "*** Begin Patch\n*** Add File: a\n+x", success: event.Some(true), diag: PatchDiagnostics{Truncated: 1}},
		{name: "invalid", vendor: "codex", eventName: "codex.tool_result", input: "not a patch", success: event.Some(true), diag: PatchDiagnostics{Invalid: 1}},
		{name: "decision_only", vendor: "codex", eventName: "codex.tool_decision", input: patchFixture, success: event.Some(true)},
		{name: "other_vendor", vendor: "claude_code", eventName: "claude_code.tool_result", input: patchFixture, success: event.Some(true)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newDecoder(Options{Vendor: tc.vendor, InstallationID: "test"})
			c := carrier{codexArguments: rawContent{body: tc.input, set: tc.input != ""}, codexOutput: rawContent{body: "Success", set: true}}
			ev := event.Event{Signal: event.SignalLog, Name: tc.eventName, TS: 1}
			ev.Attr.ToolName, ev.Attr.Decision = "apply_patch", tc.decision
			ev.Measure.Success = tc.success
			if !d.emit(ev, &c) {
				t.Fatal("event rejected")
			}
			res := d.result()
			if len(res.Targets) != tc.wantFiles || res.Patches != tc.diag {
				t.Fatalf("targets=%+v diagnostics=%+v", res.Targets, res.Patches)
			}
			if tc.name == "large" {
				if !res.Contents[0].Content.Truncated {
					t.Fatal("content cap not exercised")
				}
				for _, target := range res.Targets {
					if target.EventIndex != 0 || target.DedupKey != res.Events[0].DedupKey() {
						t.Fatal("event association lost")
					}
					if !target.Additions.Valid() || (target.Operation != "delete" && !target.Deletions.Valid()) {
						t.Fatal("line counts lost after content cap")
					}
				}
			}
			if tc.name == "other_vendor" || tc.name == "decision_only" {
				if len(res.Contents) != 0 {
					t.Fatal("generic arguments/output mapped outside Codex result")
				}
			}
		})
	}
}

func TestCodexAliasesFollowPrivacyPolicy(t *testing.T) {
	for _, tc := range []struct{ details, content bool }{{}, {true, false}, {false, true}, {true, true}} {
		policy := PolicyFromPrivacy(contract.Privacy{CollectToolDetails: tc.details, CollectToolContent: tc.content})
		if policy.DropAttributes["arguments"] == tc.details || policy.DropAttributes["output"] == tc.content {
			t.Fatalf("aliases do not follow independent flags: %+v", tc)
		}
	}
}

func TestCodexFileChangeWireAndScrub(t *testing.T) {
	res := decodeBoth(t, PayloadLogs, "logs_codex_file_changes.json", testOptions())
	if len(res.Targets) != 4 || res.Patches != (PatchDiagnostics{MissingInput: 1, Invalid: 1, Unconfirmed: 1}) {
		t.Fatalf("targets=%+v diagnostics=%+v", res.Targets, res.Patches)
	}
	for _, target := range res.Targets {
		if target.EventIndex != 1 || target.DedupKey != res.Events[1].DedupKey() {
			t.Fatal("target belongs to wrong result")
		}
		if !strings.HasPrefix(target.RawPath, "C:/work/project/") {
			t.Fatal("workspace path not applied")
		}
		for _, safe := range []string{target.Path.Hash, target.Path.Name, target.Path.Ext, target.DedupKey} {
			if strings.Contains(safe, "C:/work") {
				t.Fatal("raw path leaked into normalized target")
			}
		}
	}
	for _, enc := range []Encoding{EncodingJSON, EncodingProtobuf} {
		raw := payloadIn(t, PayloadLogs, "logs_codex_file_changes.json", enc)
		for _, allowed := range []bool{false, true} {
			policy := PolicyFromPrivacy(contract.Privacy{CollectToolDetails: allowed, CollectToolContent: allowed})
			out, _, err := Scrub(PayloadLogs, raw, enc, policy)
			if err != nil {
				t.Fatal(err)
			}
			var msg logscolpb.ExportLogsServiceRequest
			if err := unmarshalPayload(out, enc, &msg); err != nil {
				t.Fatal(err)
			}
			foundArguments, foundOutput := false, false
			for _, record := range msg.ResourceLogs[0].ScopeLogs[0].LogRecords {
				for _, attr := range record.Attributes {
					foundArguments = foundArguments || attr.Key == "arguments"
					foundOutput = foundOutput || attr.Key == "output"
				}
			}
			if foundArguments != allowed || foundOutput != allowed {
				t.Fatalf("scrub: allowed=%v arguments=%v output=%v", allowed, foundArguments, foundOutput)
			}
		}
	}
}
