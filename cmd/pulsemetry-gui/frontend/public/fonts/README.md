# GUI 폰트

공식 저장소의 버전 태그에서 받은 원본 WOFF2 파일이다. CDN 없이 앱에 포함해 사용한다.

| 폰트 | 버전 | 파일 | 용도 |
|---|---|---|---|
| Pretendard Variable | v1.3.9 | pretendard/PretendardVariable.woff2 | 본문, 가변 굵기 |
| JetBrains Mono | v2.304 | jetbrains-mono/JetBrainsMono-{Regular,Medium,SemiBold}.woff2 | 경로·시각, 400·500·600 |

- Pretendard 원본: https://github.com/orioncactus/pretendard/tree/v1.3.9/packages/pretendard/dist/web/variable/woff2
- JetBrains Mono 원본: https://github.com/JetBrains/JetBrainsMono/tree/v2.304/fonts/webfonts
- 라이선스: 각 폴더의 LICENSE.txt·OFL.txt를 배포물에 함께 포함한다.

`src/app.css`의 `@font-face`로 로컬 파일을 로드한다. 본문은 `--font-sans`의 Pretendard
Variable을, 경로·시각 등 고정폭 텍스트는 `--font-mono`의 JetBrains Mono를 사용한다.
JetBrains Mono에 없는 한글은 Pretendard Variable로 표시한다.
