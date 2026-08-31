// dev 서버용 빈 포트 탐색 — 시작 포트(기본 9245)부터 IPv4·IPv6 루프백이
// 모두 비어 있는 첫 포트를 stdout 으로 출력한다. 고아 vite/앱 프로세스가
// 포트를 물고 있어도 dev 가 다음 포트로 비켜가게 해 준다 (Taskfile dev 태스크).
import net from "node:net";

function free(port, host) {
  return new Promise((resolve) => {
    const srv = net.createServer();
    // EADDRINUSE/EACCES 만 "사용 중"으로 본다. IPv6 미지원(EAFNOSUPPORT 등)은
    // 그 스택에서 충돌할 수 없다는 뜻이므로 비어 있는 것으로 취급한다.
    srv.once("error", (e) =>
      resolve(e.code !== "EADDRINUSE" && e.code !== "EACCES"),
    );
    srv.listen(port, host, () => srv.close(() => resolve(true)));
  });
}

const start = Number(process.argv[2] ?? 9245);
for (let p = start; p < start + 100; p++) {
  if ((await free(p, "127.0.0.1")) && (await free(p, "::1"))) {
    console.log(p);
    process.exit(0);
  }
}
console.error(`no free port in ${start}..${start + 99}`);
process.exit(1);
