package receiver

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"sync"
)

// Server 는 Receiver 를 loopback 리스너들에 물린 것이다.
//
// 리스너는 둘이지만 *http.Server 는 하나다 — 타임아웃·핸들러·종료 처리를 두 벌 관리하면
// 한쪽만 설정이 어긋나는 회귀가 난다 (계획서 제약 §1).
type Server struct {
	recv *Receiver
	http *http.Server
	set  listenerSet

	wg   sync.WaitGroup
	once sync.Once
}

// Start 는 리스너를 열고 워커와 HTTP 서비스를 띄운다. 반환 직후부터 요청을 받는다.
//
// 9단계 배선은 이렇게 쓴다.
//
//	token, err := receiver.EnsureToken()
//	srv, err := receiver.Start(receiver.Options{
//	    Port: state.Local.ListenPort, FixedPort: listenFlagGiven,
//	    Token: token, Sink: pipelineSink, Logger: logger,
//	    Decode: otlpdecode.Options{InstallationID: state.InstallationID},
//	})
//	defer srv.Shutdown(ctx)
//	runtimeinfo.Write(runtimeinfo.PathIn(dataDir), runtimeinfo.Info{
//	    PID: os.Getpid(), Endpoint: srv.Endpoint(),
//	    ListenPort: srv.Port(), ListenAddrs: srv.Addrs(), ...,
//	})
//
// 포트가 폴백됐는지는 srv.Port() != 요청 포트 로 알 수 있고, 그때 벤더 설정을 재병합한다.
func Start(opt Options) (*Server, error) {
	recv, err := New(opt)
	if err != nil {
		return nil, err
	}

	logf := func(format string, args ...any) { recv.logf(format, args...) }
	set, err := bindLoopback(opt.Port, opt.FixedPort, logf)
	if err != nil {
		_ = recv.Close()
		return nil, err
	}

	s := &Server{
		recv: recv,
		set:  set,
		http: &http.Server{
			Handler:           recv,
			ReadHeaderTimeout: defaultReadHeaderTimeout,
			ErrorLog:          opt.Logger,
		},
	}
	for _, l := range set.listeners {
		s.wg.Add(1)
		go func(l net.Listener) {
			defer s.wg.Done()
			if err := s.http.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
				recv.logf("수신기 리스너 종료: addr=%s: %v", l.Addr(), err)
			}
		}(l)
	}
	recv.logf("로컬 OTLP 수신기 기동: endpoint=%s listeners=%v", s.Endpoint(), s.Addrs())
	return s, nil
}

// Port 는 실제로 바인딩된 포트다. 폴백했으면 요청한 값과 다르다.
func (s *Server) Port() int { return s.set.port }

// Addrs 는 바인딩된 리스너 주소 전부다 (예: 127.0.0.1:4318, [::1]:4318).
func (s *Server) Addrs() []string { return s.set.addrs() }

// HasIPv6 는 [::1] 리스너가 있는지다. false 면 IPv6 loopback 이 없는 환경이다.
func (s *Server) HasIPv6() bool { return s.set.ipv6 }

// Endpoint 는 벤더 설정에 적을 주소다.
//
// **반드시 localhost 표기다.** 127.0.0.1 은 contract.validOTLPEndpoint 와
// enrollment-manifest.schema.json 양쪽에서 거부된다 (계획서 제약 §1, ADR 0001).
// 실제로 듣는 주소는 127.0.0.1 과 [::1] 이지만, 설정에 적히는 이름은 localhost 다.
func (s *Server) Endpoint() string {
	return "http://localhost:" + strconv.Itoa(s.set.port)
}

// HealthURL 은 /healthz 전체 주소다. status 명령이 runtime.json 의 pid 판정을
// 확정하는 데 쓴다.
func (s *Server) HealthURL() string { return s.Endpoint() + HealthPath }

// Stats 는 수신기 카운터 스냅샷이다.
func (s *Server) Stats() Stats { return s.recv.Stats() }

// Handler 는 내부 Receiver 다. 테스트와 상태 조회용이다.
func (s *Server) Handler() *Receiver { return s.recv }

// Shutdown 은 리스너를 닫고 진행 중인 요청을 끝낸 뒤 워커가 큐를 비울 때까지 기다린다.
//
// 순서가 중요하다. 먼저 HTTP 를 닫아 새 배치가 큐에 들어오지 않게 하고, 그 다음에
// Receiver 를 닫아 이미 들어온 배치를 마저 처리한다. 반대로 하면 종료 직전에 도착한
// 텔레메트리가 큐에 남은 채 버려진다.
//
// ctx 가 먼저 만료되면 http.Server.Shutdown 이 오류를 주지만, 워커 정리는 그대로 진행한다 —
// 데몬 종료를 워커 하나 때문에 멈추게 두지 않는다.
func (s *Server) Shutdown(ctx context.Context) error {
	var err error
	s.once.Do(func() {
		err = s.http.Shutdown(ctx)
		s.wg.Wait()
		if cerr := s.recv.Close(); cerr != nil && err == nil {
			err = cerr
		}
	})
	return err
}
