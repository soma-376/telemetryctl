package codexapp

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os/exec"
	"time"
)

type process struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	lines    chan []byte
	done     chan error
	ctx      context.Context
	cancel   context.CancelFunc
	readDone chan struct{}
}

func startProcess(command []string) (*process, error) {
	if len(command) == 0 {
		command = []string{"codex", "app-server", "--stdio"}
	}
	cmd := exec.Command(command[0], command[1:]...) //nolint:gosec // 실행 대상은 Options로 제한된 로컬 도구다.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := &process{cmd: cmd, stdin: stdin, lines: make(chan []byte, 16), done: make(chan error, 1),
		ctx: ctx, cancel: cancel, readDone: make(chan struct{})}
	go p.read(stdout)
	go func() { p.done <- cmd.Wait(); close(p.done) }()
	return p, nil
}

func (p *process) read(r io.Reader) {
	defer close(p.readDone)
	defer close(p.lines)
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), 1<<20)
	for s.Scan() {
		line := append([]byte(nil), s.Bytes()...)
		// 소비자가 요청을 취소해도 가득 찬 출력 채널에 고루틴이 남지 않는다.
		select {
		case p.lines <- line:
		case <-p.ctx.Done():
			return
		}
	}
}

func (p *process) close() error {
	p.cancel()
	defer func() { <-p.readDone }()
	_ = p.stdin.Close()
	select {
	case <-p.done:
		return nil
	case <-time.After(2 * time.Second):
		if p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
		<-p.done
		return nil
	}
}

func (p *process) kill() {
	p.cancel()
	_ = p.stdin.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	<-p.done
	<-p.readDone
}
