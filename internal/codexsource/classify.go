package codexsource

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
)

// Prompt는 이미 존재하는 OTel 턴이다. JSONL은 Prompt를 만들지 않는다.
type Prompt struct {
	ID                                                                         int64
	Owner, MessageID, TurnID, Body, Completeness, CompletenessEvidence, Source string
	StartedAt, EndedAt, NextStartedAt                                          int64 // SQLite 초이며 0이면 해당 경계가 없다.
	Conflict, Purged, SameSecond                                               bool
}

type Decision struct {
	Label, ProcessingState, LinkState, LinkMethod, Reason, StructureEvidence string
	LinkedRecordID                                                           int64
}

func decodeStructure(raw string) map[string]any {
	var m map[string]any
	_ = json.Unmarshal([]byte(raw), &m)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func subagentSignal(v any, objectAllowed bool) (active, valid bool) {
	switch x := v.(type) {
	case nil:
		return false, true
	case bool:
		return false, !x
	case string:
		return x != "", true
	case map[string]any:
		if !objectAllowed {
			return false, false
		}
		known := false
		for _, k := range []string{"other", "parent_thread_id", "parent_session_id"} {
			if v, ok := x[k]; ok {
				s, valid := v.(string)
				if !valid || s == "" {
					return false, false
				}
				known = true
			}
		}
		if v, ok := x["thread_spawn"]; ok {
			spawn, valid := v.(map[string]any)
			if !valid {
				return false, false
			}
			parent, valid := spawn["parent_thread_id"].(string)
			if !valid || parent == "" {
				return false, false
			}
			for _, k := range []string{"agent_nickname", "agent_path", "agent_role"} {
				if value, ok := spawn[k]; ok && value != nil {
					if _, valid := value.(string); !valid {
						return false, false
					}
				}
			}
			if depth, ok := spawn["depth"]; ok {
				f, valid := depth.(float64)
				if !valid || f < 0 || math.Trunc(f) != f {
					return false, false
				}
			}
			known = true
		}
		return known, known
	default:
		return false, false
	}
}

// signals는 구조화 출처만 인정한다. 역할·본문·키워드만으로 사용자 제출을 증명하지 않는다.
func signals(m map[string]any) (internal, normal, form, conflict bool) {
	for _, k := range []string{"internal", "internal_task", "task.internal"} {
		if value, ok := m[k]; ok {
			b, valid := value.(bool)
			conflict = conflict || !valid
			internal = internal || (valid && b)
		}
	}
	for _, k := range []string{"subagent", "subagent_id", "subagent.id"} {
		if value, ok := m[k]; ok {
			active, valid := subagentSignal(value, k == "subagent")
			internal = internal || active
			conflict = conflict || !valid
		}
	}
	for _, k := range []string{"source", "owner_source", "session_source", "session.source"} {
		v, ok := m[k]
		if !ok || v == nil {
			continue
		}
		switch x := v.(type) {
		case string:
			switch x {
			case "", "unknown":
			case "cli", "exec", "vscode", "app", "client":
				normal = true
			case "subagent", "internal", "internal_task":
				internal = true
			default:
				if strings.HasPrefix(x, "{") {
					a, b, c, d := signals(decodeStructure(x))
					internal = internal || a
					normal = normal || b
					form = form || c
					conflict = conflict || d
				} else {
					conflict = true
				}
			}
		case map[string]any:
			a, b, c, d := signals(x)
			internal = internal || a
			normal = normal || b
			form = form || c
			conflict = conflict || d
		default:
			conflict = true
		}
	}
	for _, k := range []string{"form", "kind", "message_kind", "message.kind", "event.kind"} {
		if value, ok := m[k]; ok {
			s, valid := value.(string)
			if !valid {
				conflict = true
				continue
			}
			switch s {
			case "":
			case "UserMessage", "user_message", "user.text":
				form = true
			case "internal_task", "subagent":
				internal = true
			default:
				conflict = true
			}
		}
	}
	if m["unsupported_content"] == true || m["conflict"] == true {
		conflict = true
	}
	if m["type"] == "subagent" || m["type"] == "internal_task" {
		internal = true
	}
	return
}

func sourceLabel(raw string) (string, bool) {
	i, n, f, c := signals(decodeStructure(raw))
	if c {
		return "unknown", true
	}
	if i {
		return "internal_task", false
	}
	if n && f {
		return "client_submitted", false
	}
	return "unknown", false
}

type candidate struct {
	records []Record
	aliases bool
}

func logicalCandidates(records []Record) []candidate {
	out := make([]candidate, 0, len(records))
	for _, r := range records {
		if decodeStructure(r.Structure)["form"] != nil {
			out = append(out, candidate{records: []Record{r}})
		}
	}
	merge := func(indices []int, aliases bool) {
		if len(indices) != 2 {
			return
		}
		i, j := indices[0], indices[1]
		a, b := out[i].records[0], out[j].records[0]
		if (a.Type == "response_item") == (b.Type == "response_item") {
			return
		}
		out[i].records = append(out[i].records, b)
		out[i].aliases = aliases
		out[j].records = nil
	}
	pairs := map[string][]int{}
	for i, c := range out {
		r := c.records[0]
		if r.MessageID != "" && r.EventTime != 0 {
			key := encode([]any{r.Owner, r.MessageID, r.EventTime})
			pairs[key] = append(pairs[key], i)
		}
	}
	for _, indices := range pairs {
		merge(indices, false)
	}
	pairs = map[string][]int{}
	for i, c := range out {
		if len(c.records) != 1 {
			continue
		}
		r := c.records[0]
		if r.TurnID == "" || r.EventTime == 0 || r.Completeness != "complete" || r.CompletenessEvidence == "" {
			continue
		}
		key := encode([]any{r.Owner, r.TurnID, r.Body, r.EventTime})
		pairs[key] = append(pairs[key], i)
	}
	for _, indices := range pairs {
		merge(indices, true)
	}
	filtered := out[:0]
	for _, c := range out {
		if len(c.records) > 0 {
			sort.Slice(c.records, func(i, j int) bool {
				if c.records[i].Type != c.records[j].Type {
					return c.records[i].Type == "event_msg"
				}
				return c.records[i].ID < c.records[j].ID
			})
			filtered = append(filtered, c)
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].records[0].ID < filtered[j].records[0].ID })
	return filtered
}

func compatibleIDs(p Prompt, c candidate) bool {
	seen, known := false, false
	for _, r := range c.records {
		if p.TurnID != "" && r.TurnID != "" && p.TurnID != r.TurnID {
			return false
		}
		if !c.aliases && p.MessageID != "" && r.MessageID != "" && p.MessageID != r.MessageID {
			return false
		}
		known = known || r.MessageID != ""
		seen = seen || r.MessageID == p.MessageID
	}
	return !c.aliases || p.MessageID == "" || !known || seen
}

func candidateLinkConflict(p Prompt, c candidate) bool {
	if !compatibleIDs(p, c) {
		return true
	}
	var body, turn string
	hasBody := false
	for _, r := range c.records {
		if r.TurnID != "" {
			if turn != "" && turn != r.TurnID {
				return true
			}
			turn = r.TurnID
		}
		if r.Completeness == "complete" {
			if hasBody && body != r.Body {
				return true
			}
			body = r.Body
			hasBody = true
			if p.Completeness == "complete" && p.Body != "" && p.Body != r.Body {
				return true
			}
			if p.Completeness == "truncated" && p.Body != "" && !provenStrictPrefix(p, r) {
				return true
			}
		}
	}
	return false
}

func candidateLabel(p Prompt, c candidate) (string, bool, bool) {
	if candidateLinkConflict(p, c) {
		return "unknown", false, true
	}
	label := ""
	for _, r := range c.records {
		l, bad := sourceLabel(r.Structure)
		if bad {
			return "unknown", false, true
		}
		if label != "" && label != l {
			return "unknown", false, true
		}
		label = l
	}
	return label, label != "unknown" && label != "", false
}

func eventEligible(p Prompt, r Record) bool {
	if r.Owner != p.Owner || r.EventTime == 0 {
		return false
	}
	if p.StartedAt != 0 && r.EventTime < p.StartedAt*1_000_000_000 {
		return false
	}
	// 저장한 초 단위 종료 시각은 JSONL의 소수 초를 버린 값이다. 마지막 초는
	// 포함하되 그 다음 초의 레코드는 제외한다.
	if p.EndedAt != 0 && r.EventTime >= (p.EndedAt+1)*1_000_000_000 {
		return false
	}
	// 다음 턴의 시작 초는 배타적 상한이다. 종료 시각처럼 1초를 더하면
	// 다음 턴 첫 소수 초의 같은 본문을 현재 턴 후보로 잘못 받는다.
	if p.NextStartedAt > p.StartedAt && r.EventTime >= p.NextStartedAt*1_000_000_000 {
		return false
	}
	return true
}

func provenStrictPrefix(p Prompt, r Record) bool {
	if p.Completeness != "truncated" || r.Completeness != "complete" || r.CompletenessEvidence == "" ||
		p.Body == "" || len(p.Body) >= len(r.Body) || !strings.HasPrefix(r.Body, p.Body) {
		return false
	}
	var proof struct {
		Source         string `json:"source"`
		CapBytes       int    `json:"cap_bytes"`
		StoredBytes    int    `json:"stored_bytes"`
		OriginalBytes  int    `json:"original_bytes"`
		OriginalSHA256 string `json:"original_sha256"`
	}
	if json.Unmarshal([]byte(p.CompletenessEvidence), &proof) != nil {
		return false
	}
	return proof.Source == "decoder_utf8_cap" && proof.CapBytes > 0 && proof.StoredBytes == len(p.Body) &&
		proof.StoredBytes <= proof.CapBytes && proof.OriginalBytes == len(r.Body) && proof.OriginalSHA256 == hashBody(r.Body)
}

// Classify는 입증되지 않은 후보를 선택하지 않는다. overflow는 유계 DB 조회로
// 모든 후보의 합의를 확인하지 못했다는 뜻이다.
func Classify(p Prompt, records []Record, overflow bool) Decision {
	d := Decision{Label: "unknown", ProcessingState: "pending", LinkState: "not_attempted", Reason: "no structural provenance", StructureEvidence: "{}"}
	evidence := map[string]any{"owner_session_id": p.Owner, "stored_source": decodeStructure(p.Source), "completeness_evidence": p.CompletenessEvidence, "candidate_count": 0, "candidate_ids": []int64{}, "classification_basis": "none"}
	finish := func() Decision {
		evidence["link_state"] = d.LinkState
		evidence["link_method"] = d.LinkMethod
		d.StructureEvidence = encode(evidence)
		return d
	}
	if p.Owner == "" {
		d.LinkState = "unavailable"
		d.Reason = "owner missing"
		return finish()
	}
	ownLabel, ownConflict := sourceLabel(p.Source)
	metaLabel, metaConflict := "unknown", false
	for _, r := range records {
		if r.Owner != p.Owner || r.Type != "session_meta" || r.SessionMetaID != p.Owner {
			continue
		}
		s := decodeStructure(r.Structure)
		meta := object(s["session_meta_provenance"])
		if ownerSource, ok := s["owner_source"]; ok {
			meta["owner_source"] = ownerSource
		}
		i, n, _, bad := signals(meta)
		l := "unknown"
		if i {
			l = "internal_task"
		} else if n {
			l = "client_submitted"
		}
		if i && n {
			bad = true
		}
		if metaLabel != "unknown" && l != "unknown" && metaLabel != l {
			metaConflict = true
		}
		if l != "unknown" {
			metaLabel = l
		}
		metaConflict = metaConflict || bad
	}
	if overflow {
		d.LinkState = "unavailable"
		d.Reason = "candidate memory bound exceeded"
		return finish()
	}
	eligible := make([]Record, 0, len(records))
	for _, r := range records {
		if eventEligible(p, r) {
			eligible = append(eligible, r)
		}
	}
	all := logicalCandidates(eligible)
	var matches []candidate
	for _, c := range all {
		for _, r := range c.records {
			if p.MessageID != "" && r.MessageID == p.MessageID {
				matches = append(matches, c)
				break
			}
		}
	}
	if len(matches) > 0 {
		d.LinkMethod = "message_id"
	}
	if d.LinkMethod == "" {
		for _, c := range all {
			for _, r := range c.records {
				if p.TurnID != "" && r.TurnID == p.TurnID {
					matches = append(matches, c)
					break
				}
			}
		}
		if len(matches) > 0 {
			d.LinkMethod = "turn_id"
		}
	}
	bodyWindowSafe := !p.SameSecond && (p.NextStartedAt == 0 || p.NextStartedAt > p.StartedAt)
	if d.LinkMethod == "" && bodyWindowSafe && p.StartedAt != 0 && p.Body != "" && p.Completeness != "truncated" {
		h := hashBody(p.Body)
		for _, c := range all {
			if !compatibleIDs(p, c) {
				continue
			}
			for _, r := range c.records {
				if r.Completeness == "complete" && r.CompletenessEvidence != "" && r.BodyHash == h && r.Body == p.Body {
					matches = append(matches, c)
					break
				}
			}
		}
		if len(matches) > 0 {
			d.LinkMethod = "stored_body_exact"
		}
	}
	if d.LinkMethod == "" && bodyWindowSafe && p.StartedAt != 0 && p.Body != "" && p.Completeness == "truncated" && p.CompletenessEvidence != "" {
		for _, c := range all {
			if !compatibleIDs(p, c) {
				continue
			}
			for _, r := range c.records {
				if provenStrictPrefix(p, r) {
					matches = append(matches, c)
					break
				}
			}
		}
		if len(matches) > 0 {
			d.LinkMethod = "stored_body_prefix"
		}
	}
	evidence["candidate_count"] = len(matches)
	ids := make([]int64, 0, len(matches))
	consensus := ""
	supported := len(matches) > 0
	conflict := ownConflict || metaConflict || p.Conflict
	for _, c := range matches {
		ids = append(ids, c.records[0].ID)
		label, valid, bad := candidateLabel(p, c)
		conflict = conflict || bad
		supported = supported && valid
		if consensus != "" && consensus != label {
			supported = false
		}
		consensus = label
	}
	evidence["candidate_ids"] = ids
	if ownLabel != "unknown" && metaLabel != "unknown" && ownLabel != metaLabel {
		conflict = true
	}
	if supported && ((ownLabel != "unknown" && ownLabel != consensus) || (metaLabel != "unknown" && metaLabel != consensus)) {
		conflict = true
	}
	switch len(matches) {
	case 0:
		d.LinkState = "unmatched"
		d.Reason = "no eligible JSONL candidate"
		if !conflict && (metaLabel == "internal_task" || ownLabel == "internal_task") {
			d.Label = "internal_task"
			d.ProcessingState = "finalized"
			evidence["classification_basis"] = "owner_or_stored_internal"
		}
	case 1:
		d.ProcessingState = "finalized"
		d.LinkState = "unique"
		d.Reason = "linked but missing supported structured provenance"
		if candidateLinkConflict(p, matches[0]) {
			d.LinkState = "conflict"
		} else {
			d.LinkedRecordID = matches[0].records[0].ID
		}
		if supported && !conflict {
			d.Label = consensus
			d.Reason = "linked structured provenance"
			evidence["classification_basis"] = "candidate"
		}
		if d.Label == "unknown" && !conflict && (metaLabel == "internal_task" || ownLabel == "internal_task") {
			d.Label = "internal_task"
			evidence["classification_basis"] = "owner_or_stored_internal"
		}
	default:
		d.LinkState = "ambiguous"
		d.Reason = "ambiguous logical candidates"
		if supported && !conflict {
			d.Label = consensus
			d.ProcessingState = "finalized"
			evidence["classification_basis"] = "candidate_consensus"
		}
	}
	if conflict {
		d.Label = "unknown"
		if len(matches) == 1 && candidateLinkConflict(p, matches[0]) {
			d.LinkState = "conflict"
			d.LinkedRecordID = 0
		}
		d.Reason = "conflicting IDs, kinds, bodies or provenance"
		evidence["classification_basis"] = "none"
	}
	return finish()
}
