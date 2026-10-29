import type { PolicySnapshot } from "$lib/bindings";

const STATUS: Record<string, string> = {
  checking: "서버 정책 확인 중",
  disabled: "등록 서버가 없어 최신 정책을 확인할 수 없어요",
  error: "서버 정책 확인 실패",
  unknown: "서버 최신 정책 버전을 확인할 수 없어요",
  stale: "정책 확인 결과가 오래되었어요",
  ahead: "서버와 적용 정책 버전이 달라요",
  pending: "정책 업데이트 대기",
  current: "최신 정책 적용 완료",
};

export default function CollectionPolicy({
  snapshot,
  unavailable,
}: {
  snapshot?: PolicySnapshot;
  unavailable: boolean;
}) {
  if (unavailable || !snapshot) {
    return (
      <div className="text-text-secondary text-[12px] leading-[1.6]">
        {unavailable
          ? "데몬에 연결할 수 없어 적용 정책을 확인할 수 없어요."
          : "적용 정책을 불러오는 중이에요."}
      </div>
    );
  }

  const rows: [string, boolean][] = [
    ["로그", snapshot.signals.logs],
    ["메트릭", snapshot.signals.metrics],
    ["트레이스", snapshot.signals.traces],
    ["사용자 프롬프트", snapshot.privacy.collect_user_prompts],
    ["AI 응답", snapshot.privacy.collect_assistant_responses],
    ["도구 호출 정보", snapshot.privacy.collect_tool_details],
    ["도구 입출력 내용", snapshot.privacy.collect_tool_content],
    ["사용자 이메일", snapshot.privacy.collect_user_email],
    ["API 원문", snapshot.privacy.collect_raw_api_bodies],
  ];

  return (
    <section aria-label="수집 정책" className="text-[12px] leading-[1.6]">
      <div className="bg-surface-hover rounded-[11px] p-[12px_14px]">
        <p className="text-text font-semibold">
          적용 정책 v{snapshot.applied_revision}
        </p>
        <p className="text-text-secondary">
          {STATUS[snapshot.status] ?? STATUS.unknown}
        </p>
        {snapshot.expected_revision !== null && (
          <p className="text-text-secondary">
            마지막 확인한 서버 정책 v{snapshot.expected_revision}
          </p>
        )}
        {snapshot.last_checked_at && (
          <p className="text-text-muted">
            마지막 확인 {new Date(snapshot.last_checked_at).toLocaleString()}
          </p>
        )}
        {snapshot.expected_revision !== null &&
          snapshot.expected_revision > snapshot.applied_revision && (
            <p className="text-text-secondary">
              새 정책은 아직 적용되지 않았어요. 현재는 재설치가 필요하므로 조직
              관리자에게 문의하세요.
            </p>
          )}
      </div>
      <p className="text-text-muted my-[12px]">
        현재 데몬에 적용된 조직 정책입니다. 허용된 항목도 사용 중인 도구와
        시그널에 따라 실제 전송 여부가 달라질 수 있어요.
      </p>
      <dl>
        {rows.map(([label, allowed]) => (
          <div
            key={label}
            className="border-track flex justify-between border-b py-[8px]"
          >
            <dt className="text-text">{label}</dt>
            <dd className={allowed ? "text-accent" : "text-text-muted"}>
              {allowed ? "허용" : "제외"}
            </dd>
          </div>
        ))}
      </dl>
    </section>
  );
}
