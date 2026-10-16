package receiver

import (
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"strings"
)

var (
	// errTooLarge 는 413 으로 나간다.
	errTooLarge = errors.New("receiver: 본문이 상한을 넘음")
	// errUnsupportedEncoding 은 415 로 나간다.
	errUnsupportedEncoding = errors.New("receiver: 지원하지 않는 Content-Encoding")
	// errBadBody 는 400 으로 나간다.
	errBadBody = errors.New("receiver: 본문을 읽을 수 없음")
)

// readBody 는 요청 본문을 상한 안에서 읽는다.
//
// # gzip 캡을 압축 해제 후 크기로 거는 이유
//
// 압축된 바이트에만 상한을 걸면 gzip 폭탄이 그대로 통과한다. 0 으로 채운 1 GiB 는
// 1 MiB 도 안 되게 압축되므로, 4 MiB 압축 본문 하나가 힙에 수 GiB 를 요구할 수 있다.
// 그래서 상한을 두 번 건다.
//
//  1. http.MaxBytesReader — **압축된** 스트림에 건다. 상한을 넘으면 소켓 읽기가 그 자리에서
//     끊긴다. 폭탄이든 아니든 4 MiB 넘게는 네트워크에서 읽지 않는다.
//  2. io.LimitReader(gz, max+1) — **압축 해제된** 스트림에 건다. +1 은 "상한을 넘었는지"
//     와 "정확히 상한인지" 를 구분하기 위해서다. 여기서 멈추므로 힙에 max+1 바이트보다
//     많이 올라올 수 없다 — 폭탄의 실제 크기와 무관하게 상수 메모리다.
//
// gzip.Reader 를 io.ReadAll 로 끝까지 읽지 않는 것도 의도적이다. 뒷부분을 마저 풀어 봐야
// 어차피 버릴 데이터이고, 푸는 비용은 폭탄이 노리는 바로 그 비용이다.
func (rc *Receiver) readBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	max := rc.opt.MaxBodyBytes
	body := http.MaxBytesReader(w, r.Body, max)

	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))) {
	case "", "identity":
		b, err := io.ReadAll(body)
		if err != nil {
			return nil, classifyReadError(err)
		}
		return b, nil

	case "gzip":
		gz, err := gzip.NewReader(body)
		if err != nil {
			// gzip 헤더가 아니거나, 압축 본문이 이미 상한을 넘어 끊긴 경우다.
			return nil, classifyReadError(err)
		}
		defer gz.Close()

		b, err := io.ReadAll(io.LimitReader(gz, max+1))
		if err != nil {
			return nil, classifyReadError(err)
		}
		if int64(len(b)) > max {
			return nil, errTooLarge
		}
		return b, nil

	default:
		return nil, errUnsupportedEncoding
	}
}

// classifyReadError 는 읽기 오류를 응답 코드로 옮긴다.
// MaxBytesReader 는 *http.MaxBytesError 를 준다 — 상한 초과는 클라이언트 잘못이므로 413 이다.
func classifyReadError(err error) error {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return errTooLarge
	}
	if errors.Is(err, errTooLarge) {
		return errTooLarge
	}
	return errBadBody
}
