package session

// lineLedger 는 lines_of_code 메트릭을 세션 합계에 더하고 그 일부를 파일에 배분한다.
//
// # 왜 어려운가
//
// claude_code.lines_of_code.count 메트릭에는 **파일명이 없다** — type(added/removed)과
// model 뿐이다. 파일별 수치는 tool_result 의 tool_input(Edit/Write 의 file_path)에서 파일을
// 얻고 같은 구간의 메트릭 증분을 귀속시켜야 한다. 한 응답에서 여러 파일을 고치면 배분이
// 근사가 된다. 세션 합계는 메트릭에서 직접 받으므로 정확하다.
//
// # 불변식: Σ(파일별 배분) ≤ 세션 합계
//
// 계획서 「테스트 전략」이 요구한 불변식이다. 테스트로 확인하는 것에 그치지 않고 구조로 막는다.
//
//   - 라인 수를 담는 필드는 이 타입 안에만 있다. fileState 에는 라인 필드가 없어서
//     원장 밖에서 파일에 라인을 더하는 코드를 **쓸 수가 없다**.
//   - total 을 늘리는 경로는 add() 하나뿐이고, add() 는 같은 양만큼 unassigned 도 늘린다.
//   - 배분은 더하기가 아니라 **이동**이다. distribute() 는 unassigned 에서 뺀 만큼만
//     assigned 에 넣고 unassigned 를 음수로 만들지 않는다.
//
// 따라서 Σassigned = total - unassigned ≤ total 이 항상 성립한다. 어떤 도착 순서에서도,
// 메트릭만 오거나 편집만 와도 마찬가지다.
//
// # 도착 순서
//
// Claude Code 의 로그 내보내기 주기는 5초, 메트릭은 60초라 보통 편집(로그)이 먼저 오고
// 메트릭이 나중에 온다. 반대 순서도 처리한다 — 편집은 큐에 쌓이고 메트릭은 잔량으로 남아
// 어느 쪽이 먼저 와도 만나는 순간 배분된다.
type lineLedger struct {
	added    bucket
	removed  bucket
	queue    []string // 배분 대기 중인 편집 대상(file_path_hash), 도착 순서. 중복 허용
	spent    bool     // 현재 큐로 이미 한 번 배분했는지
	assigned map[string]lineCounts
}

type bucket struct {
	total      int64
	unassigned int64
}

type lineCounts struct {
	added   int64
	removed int64
}

// addAdded·addRemoved 는 lines_of_code 메트릭 증분을 세션 합계에 더한다.
// 음수와 0 은 무시한다 — 배분할 것이 없고, 음수를 받으면 unassigned 가 음수가 돼
// 불변식의 전제가 깨진다.
func (l *lineLedger) addAdded(v int64)   { l.add(&l.added, v) }
func (l *lineLedger) addRemoved(v int64) { l.add(&l.removed, v) }

func (l *lineLedger) add(b *bucket, v int64) {
	if v <= 0 {
		return
	}
	b.total += v
	b.unassigned += v
	l.flush()
}

// noteEdit 는 파일 편집이 일어났음을 알린다. 배분 단위는 편집 1회라 같은 파일이 여러 번
// 편집되면 큐에 여러 번 들어가고 그만큼 많이 배분받는다.
//
// 직전 큐로 이미 배분했다면 큐를 비우고 새로 시작한다. 메트릭 한 묶음(added·removed 두
// 데이터포인트)은 같은 큐를 보고, 다음 편집이 오는 순간 구간이 갈린다.
func (l *lineLedger) noteEdit(pathHash string) {
	if pathHash == "" {
		return
	}
	if l.spent {
		l.queue = l.queue[:0]
		l.spent = false
	}
	l.queue = append(l.queue, pathHash)
	l.flush()
}

func (l *lineLedger) flush() {
	if len(l.queue) == 0 {
		return
	}
	l.distribute(&l.added, func(c *lineCounts, n int64) { c.added += n })
	l.distribute(&l.removed, func(c *lineCounts, n int64) { c.removed += n })
}

// distribute 는 잔량을 큐에 균등 배분한다. 나머지는 앞쪽(먼저 편집된) 항목에 1씩 얹어
// 잔량을 정확히 0 으로 만든다. 배분량의 합이 잔량과 정확히 같으므로 총량이 늘지도 줄지도 않는다.
func (l *lineLedger) distribute(b *bucket, put func(*lineCounts, int64)) {
	if b.unassigned <= 0 {
		return
	}
	n := int64(len(l.queue))
	share, rem := b.unassigned/n, b.unassigned%n

	if l.assigned == nil {
		l.assigned = make(map[string]lineCounts, len(l.queue))
	}
	for i, h := range l.queue {
		amount := share
		if int64(i) < rem {
			amount++
		}
		if amount == 0 {
			continue
		}
		c := l.assigned[h]
		put(&c, amount)
		l.assigned[h] = c
		b.unassigned -= amount
	}
	l.spent = true
}

func (l *lineLedger) assignedTo(pathHash string) lineCounts { return l.assigned[pathHash] }
