package dashboard

import "context"

// ReadSnapshot은 첫 SELECT 시점의 데이터를 콜백이 끝날 때까지 유지한다.
// 콜백 밖으로 조회 핸들을 보관하지 않는다. DB가 없으면 기존 빈 결과 계약을 따른다.
func (s *Service) ReadSnapshot(ctx context.Context, read func(SQLQuerier) error) error {
	s.reconnect()
	db, ok := s.reader.db()
	if !ok {
		return read(nil)
	}
	// 연결 자체가 mode=ro다. 지연 트랜잭션으로 WAL 쓰기를 막지 않는다.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := read(tx); err != nil {
		return err
	}
	return tx.Commit()
}
