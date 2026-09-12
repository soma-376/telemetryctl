# 이벤트별 수신 payload

`events.payload`는 정규화된 이벤트에 대응하는 수신 OTLP JSON을 SQLite JSONB로 저장한다.
로그는 한 레코드, 메트릭은 한 Sum 데이터 포인트만 포함하고 resource·scope 문맥을 유지한다.
정규화에 성공한 이벤트가 대상이며 traces와 거부된 레코드는 여기에 저장하지 않는다.

민감 필드와 본문을 제거하지 않는다. JSON 입력의 미지원 필드도 보존한다.
Protobuf 입력은 현재 OTLP 스키마를 통해 JSON으로 변환하므로 unknown wire fields와 원래 바이트
형식은 복원하지 못한다. HTTP 헤더·gzip 바이트는 포함하지 않는다. 상위 전송 스크럽은 그대로다.

원문 저장 OFF일 때는 생성·저장을 건너뛴다. 이벤트당 64 KiB, 수신 배치당 합계 4 MiB를 넘으면
payload만 생략하고 정규화 이벤트는 저장한다. 디코더의 `PayloadsDropped`와 데몬의 생략 건수
로그로 확인한다. 기존 NULL 행을 복원하지 않으며, 적용한 데몬을 재시작한 뒤 새 수신분부터 채운다.

JSONB는 다음처럼 조회한다. payload가 크므로 실제 확인할 이벤트 ID로 좁힌다.

```sql
SELECT id, event_name, json(payload)
FROM events
WHERE id = ?;
```

기존 원문 purge가 payload를 NULL로 지우고, 보존 기간 만료 시 이벤트 행과 함께 삭제한다.
GUI 상세 뷰어와 수신 요청 전체의 재전송 기능은 포함하지 않는다.

결정: [ADR 0025](adr/0025-이벤트별-수신-payload를-로컬에-보관한다.md).
