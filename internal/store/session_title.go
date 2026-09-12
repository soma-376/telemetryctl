package store

import (
	"context"
	"fmt"
)

// 세션 제목은 **벤더가 만든 것만** 저장한다 (ADR 0018).
//
// 조립기는 제목을 만들지 않으므로 스냅샷 UPSERT 는 title 컬럼을 건드리지 않는다. 이 파일의
// 두 함수가 sessions.title 을 쓰는 유일한 경로다.
//
// 둘 다 조건을 걸지 않는다. 출처가 하나뿐이라 비교할 것이 없고, 벤더가 제목을 바꾸면
// 그것이 곧 최신 제목이다. 제목이 없으면 호출하지 않는다 — sessions.title 은 NULL 로 남고
// 무엇을 대신 그릴지는 표시 계층이 정한다.

// SetCodexTitle 은 App Server 스레드 이름을 세션 제목으로 저장한다 (ADR 0017).
func (d *DB) SetCodexTitle(ctx context.Context, sessionKey, title string) error {
	return setVendorTitle(ctx, d, "Codex", `UPDATE sessions
SET title = ?
WHERE vendor_id = 'codex' AND session_key = ?`, sessionKey, title)
}

// SetClaudeTitle 은 Claude Code 트랜스크립트의 세션 제목을 저장한다.
//
// 벤더를 접두사로 거르는 이유는 claude_code 와 claude_code_desktop 이 같은 트랜스크립트
// 저장소를 쓰기 때문이다 (internal/vendor).
func (d *DB) SetClaudeTitle(ctx context.Context, sessionKey, title string) error {
	return setVendorTitle(ctx, d, "Claude", `UPDATE sessions
SET title = ?
WHERE vendor_id LIKE 'claude%' AND session_key = ?`, sessionKey, title)
}

func setVendorTitle(ctx context.Context, d *DB, vendor, query, sessionKey, title string) error {
	if sessionKey == "" || title == "" {
		return nil
	}
	if _, err := d.db.ExecContext(ctx, query, title, sessionKey); err != nil {
		return fmt.Errorf("store: %s 세션 제목 저장 (%s): %w", vendor, sessionKey, err)
	}
	return nil
}
