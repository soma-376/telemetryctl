// 앱 아이콘을 SVG 원본에서 한 번에 만든다.
//
//   원본: cmd/pulsemetry-gui/build/icons/*.svg  (디자인 핸드오프의 svg/ 를 메타데이터만 걷어 옮긴 것)
//   산출: build/appicon.png · build/darwin/icons.icns · build/windows/icon.ico
//         build/linux/icons/hicolor/**
//
// 트레이 아이콘은 여기서 만들지 않는다. 사용률·테마마다 달라서 GUI 가 런타임에 그린다
// (internal/trayicon).
//
// 인자로 출력 디렉터리(build 와 같은 구조)를 받는다. 없으면 cmd/pulsemetry-gui/build 다.
// 릴리스는 build 를 복사한 디렉터리를 넘겨 그 안에 만든다 (scripts/package-gui.mjs).
//
// 산출물은 커밋한다. 빌드 머신마다 래스터라이저를 설치하지 않아도 되게 하려는 것이고,
// 그래서 빌드의 generate:icons 는 산출물이 없을 때만 이 스크립트를 부른다. SVG 를 고쳤다면
// 루트에서 `task gen:icons` 로 다시 만들고 결과를 함께 커밋한다.

import { Resvg } from "@resvg/resvg-js";
import { copyFileSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const out = resolve(process.argv[2] ?? join(here, "../../cmd/pulsemetry-gui/build"));
const src = join(out, "icons");
const svg = (name) => readFileSync(join(src, `${name}.svg`), "utf8");

function render(markup, px) {
  return new Resvg(markup, { fitTo: { mode: "width", value: px } }).render().asPng();
}

function write(path, data) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, data);
}

// ── 앱 아이콘 ────────────────────────────────────────────────────────────
// 16·24px 는 링을 뺀 전용 그림을 쓴다. 그 크기에서 링과 파형을 함께 그리면 뭉개진다.
const appIcon = (px, master) => render(px < 32 ? svg("app-icon-small") : master, px);

const macMaster = svg("app-icon-macos");
const plainMaster = svg("app-icon");

write(join(out, "appicon.png"), appIcon(1024, plainMaster));

// .icns 는 "타입 4바이트 + 길이 + PNG" 덩어리의 나열이다. iconutil 은 macOS 에만 있어서
// 직접 묶는다. 16·32 는 PNG 를 담는 icp4·icp5 를 쓴다 (ic04·ic05 는 ARGB 가 원형이다).
const icnsEntries = [
  ["icp4", 16],
  ["ic11", 32], // 16@2x
  ["icp5", 32],
  ["ic12", 64], // 32@2x
  ["ic07", 128],
  ["ic13", 256], // 128@2x
  ["ic08", 256],
  ["ic14", 512], // 256@2x
  ["ic09", 512],
  ["ic10", 1024], // 512@2x
];
{
  const chunks = icnsEntries.map(([type, px]) => {
    const png = appIcon(px, macMaster);
    const head = Buffer.alloc(8);
    head.write(type, 0, "ascii");
    head.writeUInt32BE(png.length + 8, 4);
    return Buffer.concat([head, png]);
  });
  const body = Buffer.concat(chunks);
  const head = Buffer.alloc(8);
  head.write("icns", 0, "ascii");
  head.writeUInt32BE(body.length + 8, 4);
  write(join(out, "darwin/icons.icns"), Buffer.concat([head, body]));
}

// .ico 는 PNG 를 그대로 담는다 (Vista 이후 지원).
{
  const sizes = [16, 24, 32, 48, 256];
  const pngs = sizes.map((px) => appIcon(px, plainMaster));
  const header = Buffer.alloc(6);
  header.writeUInt16LE(0, 0);
  header.writeUInt16LE(1, 2); // 1 = 아이콘
  header.writeUInt16LE(sizes.length, 4);
  let offset = 6 + 16 * sizes.length;
  const dir = sizes.map((px, i) => {
    const e = Buffer.alloc(16);
    e.writeUInt8(px >= 256 ? 0 : px, 0); // 0 은 256 을 뜻한다
    e.writeUInt8(px >= 256 ? 0 : px, 1);
    e.writeUInt16LE(1, 4); // planes
    e.writeUInt16LE(32, 6); // bpp
    e.writeUInt32LE(pngs[i].length, 8);
    e.writeUInt32LE(offset, 12);
    offset += pngs[i].length;
    return e;
  });
  write(join(out, "windows/icon.ico"), Buffer.concat([header, ...dir, ...pngs]));
}

// 리눅스는 hicolor 테마 규칙의 경로로 둔다. .desktop 의 Icon=pulsemetry 가 이 이름을 찾는다.
{
  const root = join(out, "linux/icons/hicolor");
  rmSync(root, { recursive: true, force: true });
  for (const px of [16, 24, 32, 48, 64, 128, 256, 512]) {
    write(join(root, `${px}x${px}/apps/pulsemetry.png`), appIcon(px, plainMaster));
  }
  mkdirSync(join(root, "scalable/apps"), { recursive: true });
  copyFileSync(join(src, "app-icon.svg"), join(root, "scalable/apps/pulsemetry.svg"));
}

console.log("아이콘 생성 완료");
