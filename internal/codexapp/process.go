package codexapp

import (
	"bufio"
	"errors"
	"io"
	"os/exec"
	"time"
)

type process struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines chan []byte
	done  chan error
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
	p := &process{cmd: cmd, stdin: stdin, lines: make(chan []byte, 16), done: make(chan error, 1)}
	go p.read(stdout)
	go func() { p.done <- cmd.Wait(); close(p.done) }()
	return p, nil
}

func (p *process) read(r io.Reader) {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), 1<<20)
	for s.Scan() {
		line := append([]byte(nil), s.Bytes()...)
		p.lines <- line
	}
	close(p.lines)
}

func (p *process) close() error {
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
	_ = p.stdin.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	<-p.done
}
