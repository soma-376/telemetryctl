package rollup

import (
	"math"

	"github.com/your-org/pulsemetry/internal/event"
)

// Dim 은 rollup_hourly.dim 값이다. 계획서 스키마가 여섯 가지로 고정했다.
type Dim string

const (
	DimTotal   Dim = "total"
	DimVendor  Dim = "vendor"
	DimModel   Dim = "model"
	DimTool    Dim = "tool"
	DimProject Dim = "project"
	DimType    Dim = "type"
)

// dimOrder 는 팬아웃 순서이자 Rows 의 정렬 순서다. 스키마 주석의 나열 순서를 그대로 쓴다.
var dimOrder = []Dim{DimTotal, DimVendor, DimModel, DimTool, DimProject, DimType}

func dimRank(d Dim) int {
	for i, x := range dimOrder {
		if x == d {
			return i
		}
	}
	return len(dimOrder)
}

// rowKey 는 rollup_hourly 의 기본키 (hour, dim, key) 다.
type rowKey struct {
	hour event.Hour
	dim  Dim
	key  string
}

// dimKey 는 이벤트가 해당 dim 에서 어느 key 행에 기여하는지 돌려준다.
// ok=false 면 그 축에 대한 정보가 없는 이벤트라 행을 만들지 않는다 —
// 빈 key 로 행을 만들면 dim=model 의 빈 key 행이 "모델을 모르는 것들" 로 쌓여
// dim=total 의 빈 key 행과 구분이 안 되고 화면의 비율 계산이 틀어진다.
//
// dim='project' 의 key 는 project_hash 다. basename 은 서로 다른 프로젝트끼리 충돌해
// (~/a/api 와 ~/b/api) 두 프로젝트가 한 행으로 합쳐진다. 화면에 이름이 필요하면
// sessions.project_hash → project_name 으로 조인한다.
func dimKey(dim Dim, e event.Event) (string, bool) {
	switch dim {
	case DimTotal:
		return "", true // dim='total' 이면 key='' (스키마 규정)
	case DimVendor:
		return nonEmpty(e.Vendor)
	case DimModel:
		return nonEmpty(e.Attr.Model)
	case DimTool:
		return nonEmpty(e.Attr.ToolName)
	case DimProject:
		return nonEmpty(e.Attr.ProjectHash)
	case DimType:
		return nonEmpty(e.Attr.Type)
	default:
		return "", false
	}
}

func nonEmpty(s string) (string, bool) { return s, s != "" }

// Bucket 은 rollup_hourly 한 행의 수치 컬럼이다. 계획서 스키마의 컬럼 목록과 1:1 로 대응하고
// 순서도 같게 둔다 — store 가 INSERT 컬럼 목록을 이 순서로 쓴다.
type Bucket struct {
	CostUSD             float64
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64

	APIRequests  int64
	APIErrors    int64
	Retries      int64
	LinesAdded   int64
	LinesRemoved int64
	Commits      int64
	PullRequests int64
	Prompts      int64
	ToolCalls    int64
	ToolAccepts  int64
	ToolRejects  int64

	ActiveSeconds   float64
	SessionsStarted int64
}

// add 는 기여분을 누적한다. 필드를 리플렉션 없이 나열해 컬럼 추가 시 컴파일이 아니라
// 눈으로 걸리게 둔다 — 빠뜨린 컬럼은 조용히 0 으로 남는다.
func (b *Bucket) add(o Bucket) {
	b.CostUSD += o.CostUSD
	b.InputTokens += o.InputTokens
	b.OutputTokens += o.OutputTokens
	b.CacheReadTokens += o.CacheReadTokens
	b.CacheCreationTokens += o.CacheCreationTokens

	b.APIRequests += o.APIRequests
	b.APIErrors += o.APIErrors
	b.Retries += o.Retries
	b.LinesAdded += o.LinesAdded
	b.LinesRemoved += o.LinesRemoved
	b.Commits += o.Commits
	b.PullRequests += o.PullRequests
	b.Prompts += o.Prompts
	b.ToolCalls += o.ToolCalls
	b.ToolAccepts += o.ToolAccepts
	b.ToolRejects += o.ToolRejects

	b.ActiveSeconds += o.ActiveSeconds
	b.SessionsStarted += o.SessionsStarted
}

// IsZero 는 모든 수치가 0 인지 본다. 기여분이 0 인 행을 store 가 건너뛸 때 쓴다.
func (b Bucket) IsZero() bool { return b == Bucket{} }

// Row 는 rollup_hourly 한 행이다.
type Row struct {
	Hour event.Hour
	Dim  Dim
	Key  string
	Bucket
}

// roundInt 는 정수 컬럼에 넣을 값을 만든다. 토큰·라인 수는 실수로 올 이유가 없지만
// OTLP 의 Sum 은 double 이라 0.9999999 같은 값이 들어올 수 있다. 버림은 그런 값을 0 으로
// 만들어 조용히 누락시키므로 반올림한다.
func roundInt(v float64) int64 { return int64(math.Round(v)) }
