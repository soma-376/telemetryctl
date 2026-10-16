// Package codexsource는 Codex JSONL을 기존 OTel 턴의 출처 근거로만 읽는다.
package codexsource

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"time"
)

const ClassifierVersion = "v2-session-body"

const (
	maxJSONLTokens = 32 << 10
	maxJSONLDepth  = 64
)

var ErrJSONLStructureLimit = errors.New("JSONL 구조 토큰·깊이 한도 초과")

type Record struct {
	ID, FileID, StartOffset, EndOffset                               int64
	Owner, Parent, Type, MessageID, TurnID, Body, BodyHash           string
	Completeness, CompletenessEvidence, Structure, RepresentationKey string
	EventTime                                                        int64 // Unix 나노초이며 0이면 시각을 알 수 없다.
	SessionMetaID                                                    string
}

func object(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func stringField(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if s, ok := m[key].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func selected(m map[string]any, keys []string) map[string]any {
	out := map[string]any{}
	for _, key := range keys {
		if v, ok := m[key]; ok {
			out[key] = v
		}
	}
	return out
}

var provenanceKeys = []string{"source", "internal", "internal_task", "subagent", "subagent_id", "kind", "message_kind"}
var completenessKeys = []string{"truncated", "prompt.truncated", "content.truncated", "completeness", "prompt.completeness", "content.completeness", "original_bytes", "prompt.original_bytes", "content.original_bytes"}

func encode(v any) string      { b, _ := json.Marshal(v); return string(b) }
func hashBody(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// validateJSONLStructure는 map 디코딩 전에 중첩·요소 수를 유계화한다.
// 작은 객체를 4 MiB 행에 빽빽하게 넣어 파서 메모리를 크게 늘리는 입력을 거부한다.
func validateJSONLStructure(line []byte) error {
	if len(line) > maxJSONLLine {
		return ErrJSONLStructureLimit
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	depth := 0
	for count := 0; ; count++ {
		if count >= maxJSONLTokens {
			return ErrJSONLStructureLimit
		}
		token, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{', '[':
				depth++
				if depth > maxJSONLDepth {
					return ErrJSONLStructureLimit
				}
			case '}', ']':
				depth--
			}
		}
	}
}

// FirstOwner는 비어 있지 않은 id가 있는 완전한 session_meta만 소유자로 인정한다.
func FirstOwner(line []byte) (owner, parent, source string, found bool, err error) {
	if err = validateJSONLStructure(line); err != nil {
		return "", "", "", false, err
	}
	var m map[string]any
	if err = json.Unmarshal(line, &m); err != nil {
		return "", "", "", false, err
	}
	if m == nil {
		return "", "", "", false, errors.New("top-level JSON object required")
	}
	if stringField(m, "type") != "session_meta" {
		return "", "", "", false, nil
	}
	p := object(m["payload"])
	owner = stringField(p, "id")
	if owner == "" {
		return "", "", "", true, errors.New("session_meta.payload.id missing")
	}
	parent = stringField(p, "parent_session_id", "parent_thread_id", "parent_id")
	if parent == "" {
		parent = stringField(object(object(p["source"])["subagent"]), "parent_thread_id", "parent_session_id")
	}
	source = encode(p["source"])
	return owner, parent, source, true, nil
}

// ParseLine은 구조 필드만 고른다. 임의 JSONL 본문은 출처 라벨이나 OTel 턴이 되지 않는다.
func ParseLine(line []byte, owner, parent, ownerSource string) (Record, error) {
	r := Record{Owner: owner, Parent: parent, Completeness: "unknown", Structure: "{}"}
	if err := validateJSONLStructure(line); err != nil {
		return r, err
	}
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		return r, err
	}
	if m == nil {
		return r, errors.New("top-level JSON object required")
	}
	r.Type = stringField(m, "type")
	if raw := stringField(m, "timestamp", "event_time"); raw != "" {
		stamp, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return r, fmt.Errorf("invalid timestamp: %w", err)
		}
		r.EventTime = stamp.UnixNano()
	}
	p := object(m["payload"])
	r.MessageID = stringField(p, "message_id", "message.id")
	r.TurnID = stringField(p, "turn_id", "turn.id")
	if r.MessageID == "" {
		r.MessageID = stringField(m, "message_id")
	}
	if r.TurnID == "" {
		r.TurnID = stringField(m, "turn_id")
	}
	var ownerSourceValue any
	_ = json.Unmarshal([]byte(ownerSource), &ownerSourceValue)
	s := map[string]any{"owner_source": ownerSourceValue}
	if v, ok := p["source"]; ok {
		s["source"] = v
	}
	for _, key := range provenanceKeys {
		if key == "source" {
			continue
		}
		if v, ok := p[key]; ok {
			s[key] = v
		}
	}
	fields := []map[string]any{p}
	whole, mixed, unsupported := false, false, false
	form := ""
	switch r.Type {
	case "session_meta":
		r.SessionMetaID = stringField(p, "id")
		r.Parent = stringField(p, "parent_session_id", "parent_thread_id", "parent_id")
		if r.Parent == "" {
			r.Parent = stringField(object(object(p["source"])["subagent"]), "parent_thread_id", "parent_session_id")
		}
		s["session_meta_id"] = r.SessionMetaID
		s["session_meta_provenance"] = selected(p, provenanceKeys)
		r.MessageID, r.TurnID = "", ""
	case "event_msg":
		switch stringField(p, "type") {
		case "user_message":
			form = "UserMessage"
			r.Body, whole = wholeText(p, "message", "text")
		case "item_completed":
			item := object(p["item"])
			if stringField(item, "type") == "UserMessage" {
				form = "UserMessage"
				r.Body, whole = wholeText(item, "text", "message")
				r.MessageID = stringField(item, "id", "message_id")
				if r.TurnID == "" {
					r.TurnID = stringField(item, "turn_id")
				}
				if v, ok := item["content"]; ok {
					switch c := v.(type) {
					case string:
						r.Body, whole = c, true
					case []any:
						whole = len(c) > 0
						var b strings.Builder
						for _, v := range c {
							part := object(v)
							if typ := stringField(part, "type"); typ != "input_text" && typ != "text" {
								unsupported = true
								continue
							}
							b.WriteString(stringField(part, "text"))
						}
						r.Body = b.String()
					default:
						unsupported = true
					}
				}
				fields = append(fields, item)
				s["payload_provenance"] = selected(p, provenanceKeys)
				s["item_provenance"] = selected(item, provenanceKeys)
				s["payload_completeness"] = selected(p, completenessKeys)
				s["item_completeness"] = selected(item, completenessKeys)
				for _, key := range provenanceKeys {
					value, present := item[key]
					if !present {
						continue
					}
					if previous, present := p[key]; present {
						if !reflect.DeepEqual(previous, value) {
							s["conflict"] = true
						}
					} else {
						s[key] = value
					}
				}
			}
		}
	case "response_item":
		if stringField(p, "type") == "message" && stringField(p, "role") == "user" {
			r.MessageID = stringField(p, "id", "message_id", "message.id")
			contents, ok := p["content"].([]any)
			kinds, kok := object(p["internal_chat_message_metadata_passthrough"])["content_item_kinds"].([]any)
			valid := ok && kok && len(contents) > 0 && len(contents) == len(kinds)
			var b strings.Builder
			selectedCount := 0
			if valid {
				for i, value := range contents {
					kind, ok := kinds[i].(string)
					if !ok {
						valid = false
						break
					}
					if kind != "user.text" {
						mixed = true
						continue
					}
					part := object(value)
					if typ := stringField(part, "type"); typ != "input_text" && typ != "text" {
						valid = false
						break
					}
					selectedCount++
					b.WriteString(stringField(part, "text"))
				}
			}
			if valid && selectedCount > 0 {
				form, r.Body, whole = "user.text", b.String(), true
				s["content_item_kinds"] = kinds
			} else {
				unsupported = true
			}
		}
	case "UserMessage", "user_message":
		form = "UserMessage"
		r.Body, whole = wholeText(p, "message", "text")
	case "user.text":
		form = "user.text"
		r.Body, whole = wholeText(p, "text", "message")
	}
	if form != "" {
		s["form"] = form
		r.Completeness, r.CompletenessEvidence = completeness(r.Body, fields)
		if whole && r.Completeness == "unknown" && r.CompletenessEvidence == "no explicit whole-body evidence" {
			r.Completeness, r.CompletenessEvidence = "complete", "native whole text field"
		}
		if !whole {
			r.Completeness, r.CompletenessEvidence = "unknown", "whole text field missing"
		}
		if mixed {
			s["mixed_content"] = true
			r.Completeness, r.CompletenessEvidence = "unknown", "mixed content"
		}
		if unsupported {
			s["unsupported_content"] = true
			r.Completeness, r.CompletenessEvidence = "unknown", "unsupported content component"
		}
	}
	if unsupported {
		s["unsupported_content"] = true
	}
	r.Structure = encode(s)
	if r.Completeness == "complete" {
		r.BodyHash = hashBody(r.Body)
	}
	if r.MessageID != "" {
		r.RepresentationKey = "message:" + r.MessageID
	} else if r.TurnID != "" {
		r.RepresentationKey = "turn:" + r.TurnID
	}
	return r, nil
}

func wholeText(m map[string]any, keys ...string) (string, bool) {
	for _, key := range keys {
		if s, ok := m[key].(string); ok {
			return s, true
		}
	}
	return "", false
}

func completeness(body string, fields []map[string]any) (string, string) {
	state := ""
	evidence := []string{}
	conflict := false
	add := func(next, why string) {
		if state != "" && state != next {
			conflict = true
		}
		state = next
		evidence = append(evidence, why)
	}
	for index, m := range fields {
		prefix := "payload"
		if index > 0 {
			prefix = "payload.item"
		}
		for _, key := range []string{"truncated", "prompt.truncated", "content.truncated"} {
			v, ok := m[key]
			if !ok {
				continue
			}
			flag, valid := v.(bool)
			if !valid {
				add("unknown", prefix+"."+key+" unsupported")
				continue
			}
			if flag {
				add("truncated", prefix+"."+key+"=true")
			} else {
				add("complete", prefix+"."+key+"=false")
			}
		}
		for _, key := range []string{"completeness", "prompt.completeness", "content.completeness"} {
			v, ok := m[key]
			if !ok {
				continue
			}
			text, valid := v.(string)
			if !valid || (text != "complete" && text != "truncated" && text != "unknown") {
				add("unknown", prefix+"."+key+" unsupported")
				continue
			}
			add(text, prefix+"."+key+"="+text)
		}
		for _, key := range []string{"original_bytes", "prompt.original_bytes", "content.original_bytes"} {
			v, ok := m[key]
			if !ok {
				continue
			}
			n, valid := v.(float64)
			if !valid || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || math.Trunc(n) != n {
				add("unknown", prefix+"."+key+" unsupported")
				continue
			}
			switch {
			case n == float64(len(body)):
				add("complete", prefix+"."+key+" matches bytes")
			case n > float64(len(body)):
				add("truncated", prefix+"."+key+" exceeds bytes")
			default:
				add("unknown", prefix+"."+key+" below stored bytes")
			}
		}
	}
	if conflict {
		return "unknown", "conflicting completeness evidence: " + strings.Join(evidence, "; ")
	}
	if state == "" {
		return "unknown", "no explicit whole-body evidence"
	}
	return state, strings.Join(evidence, "; ")
}
