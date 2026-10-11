import { useEffect, useState } from "react";
import { App, Removal } from "$lib/bindings";

export default function Uninstall() {
  const [deleteData, setDeleteData] = useState(false);
  const [ready, setReady] = useState(false);
  const [busy, setBusy] = useState(false);
  const [complete, setComplete] = useState(false);
  const [error, setError] = useState("");
  const [warnings, setWarnings] = useState<string[]>([]);
  const [preserved, setPreserved] = useState<string[]>([]);

  useEffect(() => {
    let active = true;

    setReady(false);

    void Removal.Preview(deleteData).then(
      (preview) => {
        if (!active) return;
        setWarnings(preview.warnings ?? []);
        setError("");
        setReady(true);
      },
      (reason: unknown) => {
        if (active) setError(String(reason));
      },
    );

    return () => {
      active = false;
    };
  }, [deleteData]);

  async function remove() {
    setBusy(true);
    setError("");
    try {
      const result = await Removal.Execute(deleteData);

      setWarnings(result.warnings ?? []);
      setPreserved(result.preserved ?? []);
      if (result.success) setComplete(true);
      else
        setError(
          result.error || "제거를 완료하지 못했어요. 다시 시도해 주세요.",
        );
    } catch (reason) {
      setError(String(reason));
    } finally {
      setBusy(false);
    }
  }

  async function finish() {
    setBusy(true);
    try {
      await Removal.Finish();
    } catch (reason) {
      setError(String(reason));
      setBusy(false);
    }
  }

  return (
    <main className="bg-bg text-text min-h-screen p-8">
      <h1 className="text-2xl font-bold mb-4">Pulsemetry 제거</h1>
      <p className="text-text-secondary mb-5 leading-relaxed">
        {complete
          ? "자동 시작과 연결 설정 정리를 마쳤어요. 닫기를 누르면 제거 도구도 정리합니다."
          : "자동 시작과 사용량 수집을 중지하고, Pulsemetry가 추가한 연결 설정과 프로그램을 제거합니다."}
      </p>
      {!complete && (
        <label className="flex items-start gap-3 rounded-xl border border-border p-4 mb-4">
          <input
            type="checkbox"
            checked={deleteData}
            disabled={busy}
            onChange={(e) => setDeleteData(e.target.checked)}
            className="mt-1"
          />
          <span>
            이 기기의 사용 기록도 삭제하기
            <br />
            <span className="text-text-secondary text-sm">
              선택하면 로컬 사용 기록을 복구할 수 없어요. 선택하지 않으면
              보존합니다.
            </span>
          </span>
        </label>
      )}
      <p className="text-sm text-text-secondary mb-4">
        서버에 이미 전송한 기록은 삭제되지 않아요. 사용자가 직접 변경한
        Claude·Codex 설정은 보존합니다. 제거 전에 사용 중인 Claude·Codex를
        종료해 주세요.
      </p>
      {warnings.length > 0 && (
        <details className="text-sm mb-4">
          <summary className="cursor-pointer">보존 항목과 안내</summary>
          <ul className="list-disc pl-5 mt-2 space-y-2">
            {warnings.map((w) => (
              <li key={w} className="break-all">
                {w}
              </li>
            ))}
          </ul>
        </details>
      )}
      {preserved.length > 0 && (
        <div className="text-sm mb-4">
          <p>설치 후 변경되어 보존한 파일:</p>
          <ul className="list-disc pl-5">
            {preserved.map((p) => (
              <li key={p} className="break-all">
                {p}
              </li>
            ))}
          </ul>
        </div>
      )}
      {error && (
        <p
          role="alert"
          className="text-red-700 bg-red-50 rounded-lg p-3 mb-4 break-all"
        >
          {error}
        </p>
      )}
      {busy && (
        <p role="status" className="mb-4">
          제거 작업 중이에요. 잠시 기다려 주세요.
        </p>
      )}
      <div className="flex justify-end gap-3 mt-6">
        {!complete && (
          <button
            type="button"
            disabled={busy}
            onClick={() => void App.Quit()}
            className="border border-border rounded-lg px-4 py-2 disabled:opacity-50"
          >
            취소
          </button>
        )}
        <button
          type="button"
          disabled={busy || (!ready && !complete)}
          onClick={() => void (complete ? finish() : remove())}
          className="bg-text text-white rounded-lg px-4 py-2 disabled:opacity-50"
        >
          {complete ? "닫고 마무리" : "Pulsemetry 제거"}
        </button>
      </div>
    </main>
  );
}

export function UninstallSetting() {
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function open() {
    setBusy(true);
    try {
      await App.OpenUninstaller();
    } catch (reason) {
      setError(String(reason));
      setBusy(false);
    }
  }

  return (
    <section
      aria-label="Pulsemetry 제거"
      className="border-t border-track pt-4 mt-4"
    >
      <h2 className="text-text font-semibold text-sm">Pulsemetry 제거</h2>
      <p className="text-text-secondary text-xs my-2">
        앱을 닫고 제거 도구를 엽니다. 제거 전 확인 화면에서 취소할 수 있어요.
      </p>
      <button
        type="button"
        disabled={busy}
        onClick={() => void open()}
        className="border border-border rounded-lg px-3 py-2 text-sm disabled:opacity-50"
      >
        제거 도구 열기
      </button>
      {error && (
        <p role="alert" className="text-red-700 text-xs mt-2 break-all">
          {error}
        </p>
      )}
    </section>
  );
}
