# 0025. 이벤트별 수신 payload를 로컬에 보관한다

## Status
Accepted — ADR 0002·0003의 속성 allowlist 및 토큰 저장 금지 결정을 로컬 `events.payload`에 한해 부분 대체한다. 정규화 컬럼과 상위 전송 정책은 유지한다.

## Context
`events.payload` 컬럼은 있지만 INSERT가 NULL로 고정돼 있어 정규화 과정에서 선택하지 않은 수신 내용을 확인할 수 없다. 사용자는 민감 필드를 제거하지 않은 이벤트별 수신 내용의 로컬 보관을 요청했다.

## Decision
- 정규화에 성공한 로그 레코드 또는 Sum 데이터 포인트 한 건을 resource·scope·메트릭 문맥과 함께 OTLP JSON으로 저장한다. 한 요청의 다른 레코드는 제외한다.
- JSON 입력은 알려지지 않은 필드도 유지한다. Protobuf 입력은 지원하는 OTLP 스키마로 JSON 직렬화한다. 원래 Protobuf 바이트·unknown wire fields·HTTP 헤더·압축 형식을 재현하는 기능은 아니다.
- payload의 속성·본문에서 민감 정보를 제거하지 않는다. 이 예외는 로컬 payload에만 적용하며, 상위 전송 스크럽과 정규화 allowlist를 변경하지 않는다.
- `StoreContent=false` 또는 `--no-store-content`이면 payload를 생성하지 않고 저장 계층에서도 NULL을 강제한다.
- 이벤트당 JSON 64 KiB, 수신 배치당 payload 합계 4 MiB를 상한으로 둔다. 초과·직렬화 실패는 payload만 생략하고 정규화 이벤트는 저장한다. JSON 일부를 잘라 저장하지 않는다.
- `jsonb(?)`로 기존 컬럼에 저장한다. payload가 없으면 NULL이다. 중복 판정 키에 payload를 추가하지 않는다.
- 기존 원문 purge와 이벤트 보존 삭제에 포함한다. 기존 NULL 행 복원, traces 로컬 저장, GUI payload 뷰어는 이번 범위에서 제외한다.

## Alternatives Considered
- 정규화 이벤트만 JSON으로 저장: 정규화에서 제외된 수신 필드를 확인할 수 없다.
- 요청 전체를 행마다 복사: 같은 배치의 원문이 반복돼 저장량이 커진다.
- 민감 필드 제거: 이번 사용자의 로컬 원본 보관 요구와 다르다.

## Consequences
### Positive
이벤트별 수신 내용을 로컬에서 확인하면서 기존 조회용 모델을 유지한다.
### Negative
로컬 DB와 그 백업에 수신 본문의 자격증명이나 민감 내용이 포함될 수 있다. 직렬화 비용과 저장량이 증가한다. 상한을 초과한 payload는 남지 않으며 완전한 네트워크 패킷 보관소가 아니다.

## Acceptance Criteria
JSON·Protobuf 이벤트 연결, 미지원 JSON 필드와 민감 값 보존, 저장 OFF, 크기 상한, JSONB 조회, 중복 처리, purge 및 상위 스크럽 불변을 테스트한다.
