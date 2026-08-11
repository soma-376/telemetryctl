package receiver

import (
	"fmt"
	"net"
	"strconv"
)

// 두 리스너를 쓰는 이유는 계획서 「반드시 알아야 할 제약」 §1 이 지목한 조용한 절반 실패다.
//
// 벤더 설정에는 반드시 http://localhost:<port> 로 써야 한다 — contract.validOTLPEndpoint 가
// http:// 를 리터럴 호스트 localhost 에만 허용하기 때문이다 (ADR 0001).
// 그런데 localhost 는 시스템에 따라 127.0.0.1 로도 ::1 로도 풀린다. 우리가
// net.Listen("tcp", "localhost:4318") 로 한쪽만 잡으면, 반대쪽으로 푸는 사용자에게는
// 연결이 거부된다. 우리 개발기에서는 멀쩡히 동작하므로 발견도 늦다.
//
// 그래서 127.0.0.1 과 [::1] 을 각각 잡아 **하나의 *http.Server** 에 물린다.

const (
	// ephemeralAttempts 는 임의 포트 폴백 재시도 횟수다. IPv4 를 0 번 포트로 잡아 번호를
	// 알아낸 뒤 같은 번호를 IPv6 로 잡는 구조라, 그 사이에 남이 그 번호를 채 가는 경합이
	// 이론상 가능하다. 몇 번 다시 던지면 사실상 사라진다.
	ephemeralAttempts = 8

	loopbackV4 = "127.0.0.1"
	loopbackV6 = "::1"
)

// listenerSet 은 바인딩 결과다.
type listenerSet struct {
	listeners []net.Listener
	port      int
	// ipv6 는 [::1] 리스너가 있는지다. false 면 이 머신에 IPv6 loopback 이 없다는 뜻이다.
	ipv6 bool
}

func (s listenerSet) addrs() []string {
	out := make([]string, 0, len(s.listeners))
	for _, l := range s.listeners {
		out = append(out, l.Addr().String())
	}
	return out
}

func (s listenerSet) close() {
	for _, l := range s.listeners {
		_ = l.Close()
	}
}

// bindLoopback 은 loopback 리스너들을 연다.
//
// # 한쪽만 성공한 경우를 어떻게 다루는가
//
// "IPv6 를 못 잡았다" 에는 성격이 완전히 다른 두 원인이 섞여 있어서, 뭉뚱그리면 둘 중
// 하나는 반드시 틀리게 처리된다.
//
//	(a) 이 머신에 IPv6 loopback 이 없다 (커널에서 IPv6 비활성, 일부 컨테이너·WSL 구성).
//	    이때 localhost 는 ::1 로 풀리지 않으므로 IPv4 만으로 충분하다.
//	    기동을 거부하면 멀쩡히 동작할 환경에서 기능을 통째로 못 쓴다.
//	(b) 그 포트의 [::1] 을 **다른 프로세스가 이미 쓰고 있다.**
//	    이때 IPv4 만 잡고 진행하면 localhost 가 ::1 로 풀리는 클라이언트의 텔레메트리가
//	    통째로 남의 프로세스로 간다. 조용한 절반 실패의 최악 형태다.
//
// 그래서 원인을 errno 문자열로 추측하지 않고 **[::1]:0 을 한 번 바인딩해 보고 닫는 것**으로
// 가른다. 포트 0 은 절대 충돌하지 않으므로, 여기서 실패하면 (a) 이고 성공하면 IPv6 는
// 멀쩡한데 그 포트가 막힌 (b) 다. errno 상수는 Windows 에서 값이 달라 이식성이 없는데
// 이 방법은 표준 라이브러리 동작만으로 판정된다.
//
//   - (a) → IPv4 만으로 진행하고 경고를 남긴다.
//   - (b) → 포트 충돌로 취급한다. 폴백 모드면 다른 포트를 잡고, --listen 명시면 하드 실패다.
//   - IPv4 실패 → 항상 포트 충돌로 취급한다. 127.0.0.1 이 없는 환경은 다루지 않는다.
//   - 둘 다 실패 → 오류.
//
// 폴백 시 **두 리스너는 반드시 같은 포트**를 잡는다. 한쪽만 임의 포트를 잡으면 벤더 설정에
// 적을 포트가 하나로 정해지지 않는다.
func bindLoopback(port int, fixed bool, logf func(string, ...any)) (listenerSet, error) {
	useV6 := ipv6LoopbackAvailable()
	if !useV6 {
		logf("경고: IPv6 loopback([::1])을 쓸 수 없어 127.0.0.1 만 바인딩한다. " +
			"localhost 가 ::1 로 풀리는 클라이언트는 연결하지 못한다")
	}
	if port <= 0 {
		port = DefaultPort
	}

	set, err := bindPairAt(port, useV6)
	if err == nil {
		return verifyLoopback(set)
	}
	if fixed {
		return listenerSet{}, fmt.Errorf(
			"지정한 포트 %d 를 loopback 에 바인딩하지 못함(--listen 을 명시하면 폴백하지 않는다): %w", port, err)
	}

	firstErr := err
	for range ephemeralAttempts {
		set, err := bindPairEphemeral(useV6)
		if err != nil {
			continue
		}
		out, verr := verifyLoopback(set)
		if verr != nil {
			return listenerSet{}, verr
		}
		logf("포트 %d 가 사용 중이라 임의 포트 %d 로 폴백했다. 벤더 설정 재병합이 필요하다",
			port, out.port)
		return out, nil
	}
	return listenerSet{}, fmt.Errorf(
		"loopback 리스너 바인딩 실패 (기본 포트 %d, 임의 포트 %d회 시도): %w", port, ephemeralAttempts, firstErr)
}

// bindPairAt 은 지정 포트에 두 리스너를 잡는다. 한쪽이라도 실패하면 전부 닫고 오류다.
func bindPairAt(port int, useV6 bool) (listenerSet, error) {
	v4, err := net.Listen("tcp", net.JoinHostPort(loopbackV4, strconv.Itoa(port)))
	if err != nil {
		return listenerSet{}, err
	}
	if !useV6 {
		return listenerSet{listeners: []net.Listener{v4}, port: port}, nil
	}
	v6, err := net.Listen("tcp", net.JoinHostPort(loopbackV6, strconv.Itoa(port)))
	if err != nil {
		_ = v4.Close()
		return listenerSet{}, err
	}
	return listenerSet{listeners: []net.Listener{v4, v6}, port: port, ipv6: true}, nil
}

// bindPairEphemeral 은 커널이 고른 포트에 두 리스너를 잡는다.
// IPv4 를 먼저 잡아 번호를 확정하고 같은 번호를 IPv6 로 잡는다.
func bindPairEphemeral(useV6 bool) (listenerSet, error) {
	v4, err := net.Listen("tcp", net.JoinHostPort(loopbackV4, "0"))
	if err != nil {
		return listenerSet{}, err
	}
	port, err := listenerPort(v4)
	if err != nil {
		_ = v4.Close()
		return listenerSet{}, err
	}
	if !useV6 {
		return listenerSet{listeners: []net.Listener{v4}, port: port}, nil
	}
	v6, err := net.Listen("tcp", net.JoinHostPort(loopbackV6, strconv.Itoa(port)))
	if err != nil {
		_ = v4.Close()
		return listenerSet{}, err
	}
	return listenerSet{listeners: []net.Listener{v4, v6}, port: port, ipv6: true}, nil
}

// ipv6LoopbackAvailable 은 [::1]:0 에 한 번 바인딩해 보고 즉시 닫는다.
// 포트 0 은 충돌하지 않으므로 여기서의 실패는 "이 머신에 IPv6 loopback 이 없다" 로만 읽힌다.
func ipv6LoopbackAvailable() bool {
	l, err := net.Listen("tcp", net.JoinHostPort(loopbackV6, "0"))
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

func listenerPort(l net.Listener) (int, error) {
	tcp, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("TCP 리스너가 아님: %s", l.Addr())
	}
	return tcp.Port, nil
}

// verifyLoopback 은 **기동 전에** 모든 리스너가 loopback 인지 단언한다 (ADR 0001).
//
// 위의 바인딩 코드만 보면 불필요해 보인다 — 주소를 상수로 넘기니까. 그래서 더 필요하다.
// 이 수신기는 인증이 붙어 있어도 로컬 전용 신뢰 모델로 설계됐고, 만약 누군가 주소를
// 설정 가능하게 바꾸는 순간 0.0.0.0 바인딩이 조용히 가능해진다. 그 변경이 리뷰를
// 통과하더라도 여기서 기동이 거부된다.
func verifyLoopback(set listenerSet) (listenerSet, error) {
	for _, l := range set.listeners {
		tcp, ok := l.Addr().(*net.TCPAddr)
		if !ok {
			set.close()
			return listenerSet{}, fmt.Errorf("리스너가 TCP 가 아님: %s", l.Addr())
		}
		if !tcp.IP.IsLoopback() {
			set.close()
			return listenerSet{}, fmt.Errorf("loopback 이 아닌 주소에 바인딩됨: %s — 기동을 거부한다", l.Addr())
		}
		if tcp.Port != set.port {
			set.close()
			return listenerSet{}, fmt.Errorf("리스너 포트가 어긋남: %s (기대 %d)", l.Addr(), set.port)
		}
	}
	if len(set.listeners) == 0 {
		return listenerSet{}, fmt.Errorf("loopback 리스너가 하나도 없음")
	}
	return set, nil
}
