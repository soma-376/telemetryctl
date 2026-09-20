package otlpdecode

import (
	"encoding/json"
	"errors"
	"path"
	"strings"

	"github.com/your-org/pulsemetry/internal/event"
)

// PatchDiagnostics는 원문이나 경로를 포함하지 않는 배치별 진단 건수다.
type PatchDiagnostics struct {
	MissingInput int
	Truncated    int
	Invalid      int
	Unconfirmed  int
}

var (
	errPatchInvalid   = errors.New("패치 형식 오류")
	errPatchTruncated = errors.New("불완전한 패치")
)

func (d *decoder) appendPatchTargets(index int, key string, ev event.Event, c *carrier) {
	if success, known := ev.Measure.Success.Get(); !known || !success || strings.EqualFold(ev.Attr.Decision, "reject") {
		d.patches.Unconfirmed++
		return
	}
	input := c.content[contentOrdinal(event.ContentToolInput)]
	if !input.set || strings.TrimSpace(input.body) == "" {
		d.patches.MissingInput++
		return
	}
	files, err := parsePatch(input.body)
	if err != nil {
		if errors.Is(err, errPatchTruncated) {
			d.patches.Truncated++
		} else {
			d.patches.Invalid++
		}
		return
	}
	for _, file := range files {
		file.EventIndex, file.DedupKey = index, key
		file.RawPath = patchPath(file.RawPath, ev.Attr.WorkspacePath)
		if file.RenamedFrom != "" {
			file.RenamedFrom = patchPath(file.RenamedFrom, ev.Attr.WorkspacePath)
		}
		file.Path = event.NormalizePath(file.RawPath)
		d.targets = append(d.targets, file)
	}
}

// parsePatch는 적용을 실행하지 않고 파일별 작업과 명시된 추가·삭제 줄 수를 읽는다. 하나라도 잘못되면 전체를
// 버린다. JSON 입력 래퍼와 자유 형식 패치를 모두 받되, 임의의 중첩 문자열은 탐색하지 않는다.
// 헤더만 검색하지 않고 본문까지 검사해 코드 안의 패치 예시를 파일 변경으로 오인하지 않는다.
func parsePatch(input string) ([]Target, error) {
	input = strings.TrimSpace(input)
	if strings.HasPrefix(input, `"`) {
		if json.Unmarshal([]byte(input), &input) != nil {
			return nil, errPatchInvalid
		}
	} else if strings.HasPrefix(input, "{") {
		var wrapper struct {
			Input string `json:"input"`
			Patch string `json:"patch"`
		}
		if json.Unmarshal([]byte(input), &wrapper) != nil || (wrapper.Input != "" && wrapper.Patch != "") {
			return nil, errPatchInvalid
		}
		input = wrapper.Input
		if input == "" {
			input = wrapper.Patch
		}
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(input, "\r\n", "\n")), "\n")
	if len(lines) < 2 || lines[0] != "*** Begin Patch" {
		return nil, errPatchInvalid
	}
	if lines[len(lines)-1] != "*** End Patch" {
		return nil, errPatchTruncated
	}
	end := len(lines) - 1
	i := 1
	if i < end && strings.HasPrefix(lines[i], "*** Environment ID: ") {
		if !validPatchPath(strings.TrimPrefix(lines[i], "*** Environment ID: ")) {
			return nil, errPatchInvalid
		}
		i++
	}
	var files []Target
	seen := map[string]bool{}
	for i < end {
		var file Target
		header := lines[i]
		switch {
		case strings.HasPrefix(header, "*** Add File: "):
			file.Operation, file.RawPath = "create", strings.TrimPrefix(header, "*** Add File: ")
		case strings.HasPrefix(header, "*** Update File: "):
			file.Operation, file.RawPath = "modify", strings.TrimPrefix(header, "*** Update File: ")
		case strings.HasPrefix(header, "*** Delete File: "):
			file.Operation, file.RawPath = "delete", strings.TrimPrefix(header, "*** Delete File: ")
		default:
			return nil, errPatchInvalid
		}
		if !validPatchPath(file.RawPath) || seen[file.RawPath] {
			return nil, errPatchInvalid
		}
		seen[file.RawPath] = true
		i++
		if file.Operation == "modify" && i < end && strings.HasPrefix(lines[i], "*** Move to: ") {
			file.Operation, file.RenamedFrom = "rename", file.RawPath
			file.RawPath = strings.TrimPrefix(lines[i], "*** Move to: ")
			if !validPatchPath(file.RawPath) || seen[file.RawPath] {
				return nil, errPatchInvalid
			}
			seen[file.RawPath] = true
			i++
		}
		var additions, deletions int64
		bodyStart := i
		if file.Operation == "create" {
			for i < end && strings.HasPrefix(lines[i], "+") {
				additions++
				i++
			}
			if i == bodyStart {
				return nil, errPatchInvalid
			}
		} else if file.Operation == "modify" || file.Operation == "rename" {
			// 첫 블록은 @@ 없이 올 수 있다. 이후 @@에는 본문이 반드시 뒤따라야 한다.
			blockLines, pendingHeader, sawEOF := 0, false, false
			for i < end && !patchFileHeader(lines[i]) {
				line := lines[i]
				if sawEOF {
					return nil, errPatchInvalid
				}
				switch {
				case line == "@@" || strings.HasPrefix(line, "@@ "):
					if pendingHeader || (i != bodyStart && blockLines == 0) {
						return nil, errPatchInvalid
					}
					pendingHeader, blockLines = true, 0
				case line == "*** End of File":
					if blockLines == 0 || pendingHeader {
						return nil, errPatchInvalid
					}
					sawEOF = true
				case line == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-"):
					if strings.HasPrefix(line, "+") {
						additions++
					} else if strings.HasPrefix(line, "-") {
						deletions++
					}
					blockLines++
					pendingHeader = false
				default:
					return nil, errPatchInvalid
				}
				i++
			}
			if pendingHeader || (i == bodyStart && file.Operation != "rename") {
				return nil, errPatchInvalid
			}
		}
		file.Additions = event.Some(additions)
		// 전체 삭제 패치에는 이전 파일 내용이 없어 삭제량을 관측할 수 없다.
		if file.Operation != "delete" {
			file.Deletions = event.Some(deletions)
		}
		files = append(files, file)
	}
	return files, nil
}

func patchFileHeader(line string) bool {
	return strings.HasPrefix(line, "*** Add File: ") || strings.HasPrefix(line, "*** Update File: ") || strings.HasPrefix(line, "*** Delete File: ")
}

func validPatchPath(value string) bool {
	return strings.TrimSpace(value) != "" && !strings.ContainsAny(value, "\x00\r\n")
}

// Windows 경로도 호스트 OS에 관계없이 해석한다. 작업 디렉터리가 없으면 원문을 보존한다.
func patchPath(raw, cwd string) string {
	if absolutePatchPath(raw) || !absolutePatchPath(cwd) {
		return raw
	}
	base := strings.ReplaceAll(cwd, "\\", "/")
	joined := path.Join(base, strings.ReplaceAll(raw, "\\", "/"))
	if strings.HasPrefix(base, "//") {
		joined = "/" + joined
	}
	return joined
}

func absolutePatchPath(value string) bool {
	return strings.HasPrefix(value, "/") || strings.HasPrefix(value, "\\") ||
		(len(value) >= 3 && value[1] == ':' && (value[2] == '/' || value[2] == '\\'))
}
