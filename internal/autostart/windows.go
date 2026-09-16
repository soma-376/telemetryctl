package autostart

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/user"
	"strings"
)

const windowsRestartInterval = "PT30S"

type windowsBackend struct {
	opts Options
}

func (b *windowsBackend) kind() Kind       { return KindTaskScheduler }
func (b *windowsBackend) unitPath() string { return TaskPath() }
func (b *windowsBackend) logPath() string  { return "" }

func (b *windowsBackend) enable(ctx context.Context, execPath string, args []string) (Result, error) {
	res := Result{Kind: KindTaskScheduler, UnitPath: b.unitPath(), ExecPath: execPath}

	userID, err := b.userID()
	if err != nil {
		return res, err
	}
	content, err := renderWindowsTask(windowsTaskParams{
		UserID:     userID,
		ExecPath:   execPath,
		Args:       args,
		WorkingDir: windowsDir(execPath),
	})
	if err != nil {
		return res, fmt.Errorf("작업 스케줄러 XML 생성 실패: %w", err)
	}

	f, err := os.CreateTemp("", "pulsemetry-autostart-*.xml")
	if err != nil {
		return res, fmt.Errorf("작업 스케줄러 임시 파일 생성 실패: %w", err)
	}
	xmlPath := f.Name()
	defer os.Remove(xmlPath)

	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return res, fmt.Errorf("작업 스케줄러 임시 파일 기록 실패: %w", err)
	}
	if err := f.Close(); err != nil {
		return res, fmt.Errorf("작업 스케줄러 임시 파일 닫기 실패: %w", err)
	}

	if err := b.runRequired(ctx, "/Create", "/TN", TaskName, "/XML", xmlPath, "/F"); err != nil {
		return res, err
	}
	if err := b.runRequired(ctx, "/Run", "/TN", TaskName); err != nil {
		return res, err
	}

	res.Notes = append(res.Notes,
		"Windows 로그인 시 사용자 권한으로 시작합니다.",
		"비정상 종료 시 30초 간격으로 최대 5회 다시 시작합니다.")
	return res, nil
}

func (b *windowsBackend) disable(ctx context.Context) (Result, error) {
	res := Result{Kind: KindTaskScheduler, UnitPath: b.unitPath()}

	if _, _, code, err := b.opts.Runner.Run(ctx, "schtasks.exe", "/Query", "/TN", TaskName); err != nil {
		return res, fmt.Errorf("schtasks.exe 실행 실패: %w", err)
	} else if code != 0 {
		res.AlreadyInState = true
		return res, nil
	}

	// 실행 중이 아니면 /End 가 실패할 수 있다. 삭제는 계속 진행한다.
	_, _, _, _ = b.opts.Runner.Run(ctx, "schtasks.exe", "/End", "/TN", TaskName)
	if err := b.runRequired(ctx, "/Delete", "/TN", TaskName, "/F"); err != nil {
		return res, err
	}
	return res, nil
}

func (b *windowsBackend) status(ctx context.Context) (Status, error) {
	st := Status{
		Kind:      KindTaskScheduler,
		UnitPath:  b.unitPath(),
		Supported: true,
	}

	stdout, stderr, code, err := b.opts.Runner.Run(ctx, "schtasks.exe", "/Query", "/TN", TaskName, "/XML")
	if err != nil {
		st.Detail = appendDetail(st.Detail, fmt.Sprintf("schtasks.exe를 실행하지 못했습니다: %v", err))
		return st, nil
	}
	if code != 0 {
		// 미등록은 상태이지 오류가 아니다. schtasks 오류 문구는 Windows 표시 언어마다
		// 달라서 문자열로 분기하지 않는다.
		if strings.TrimSpace(stderr) != "" {
			st.Detail = appendDetail(st.Detail, "작업 스케줄러에 등록되지 않았습니다")
		}
		return st, nil
	}

	var task windowsTaskRead
	if err := xml.Unmarshal([]byte(stdout), &task); err != nil {
		st.Registered = true
		st.Detail = appendDetail(st.Detail, fmt.Sprintf("작업 XML을 해석하지 못했습니다: %v", err))
		return st, nil
	}
	st.Registered = true
	st.Loaded = task.Settings.Enabled
	st.RegisteredExecPath = strings.TrimSpace(task.Actions.Exec.Command)
	if !task.Settings.Enabled {
		st.Detail = appendDetail(st.Detail, "작업이 비활성화되어 있습니다")
	}
	return st, nil
}

func (b *windowsBackend) runRequired(ctx context.Context, args ...string) error {
	_, stderr, code, err := b.opts.Runner.Run(ctx, "schtasks.exe", args...)
	if err != nil {
		return fmt.Errorf("schtasks.exe 실행 실패: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("schtasks.exe %s 실패 (exit %d): %s", args[0], code, oneLine(stderr))
	}
	return nil
}

func (b *windowsBackend) userID() (string, error) {
	if id := strings.TrimSpace(b.opts.UserID); id != "" {
		return id, nil
	}
	current, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("현재 Windows 사용자 조회 실패: %w", err)
	}
	if id := strings.TrimSpace(current.Username); id != "" {
		return id, nil
	}
	return "", errors.New("현재 Windows 사용자 이름이 비어 있습니다")
}

type windowsTaskParams struct {
	UserID     string
	ExecPath   string
	Args       []string
	WorkingDir string
}

type windowsTaskRead struct {
	Settings struct {
		Enabled bool `xml:"Enabled"`
	} `xml:"Settings"`
	Actions struct {
		Exec struct {
			Command string `xml:"Command"`
		} `xml:"Exec"`
	} `xml:"Actions"`
}

func renderWindowsTask(p windowsTaskParams) ([]byte, error) {
	var out bytes.Buffer
	out.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	out.WriteString(`<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">` + "\n")
	out.WriteString("  <RegistrationInfo><Description>Pulsemetry 로컬 텔레메트리 데몬</Description></RegistrationInfo>\n")
	out.WriteString("  <Triggers><LogonTrigger><Enabled>true</Enabled><UserId>")
	if err := xml.EscapeText(&out, []byte(p.UserID)); err != nil {
		return nil, err
	}
	out.WriteString("</UserId></LogonTrigger></Triggers>\n")
	out.WriteString(`  <Principals><Principal id="Author"><UserId>`)
	if err := xml.EscapeText(&out, []byte(p.UserID)); err != nil {
		return nil, err
	}
	out.WriteString("</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>\n")
	out.WriteString("  <Settings>\n")
	out.WriteString("    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>\n")
	out.WriteString("    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>\n")
	out.WriteString("    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>\n")
	out.WriteString("    <StartWhenAvailable>true</StartWhenAvailable>\n")
	out.WriteString("    <AllowStartOnDemand>true</AllowStartOnDemand>\n")
	out.WriteString("    <Enabled>true</Enabled><ExecutionTimeLimit>PT0S</ExecutionTimeLimit>\n")
	out.WriteString("    <RestartOnFailure><Interval>" + windowsRestartInterval + "</Interval><Count>5</Count></RestartOnFailure>\n")
	out.WriteString("  </Settings>\n")
	out.WriteString(`  <Actions Context="Author"><Exec><Command>`)
	if err := xml.EscapeText(&out, []byte(p.ExecPath)); err != nil {
		return nil, err
	}
	out.WriteString("</Command><Arguments>")
	if err := xml.EscapeText(&out, []byte(joinWindowsArgs(p.Args))); err != nil {
		return nil, err
	}
	out.WriteString("</Arguments><WorkingDirectory>")
	if err := xml.EscapeText(&out, []byte(p.WorkingDir)); err != nil {
		return nil, err
	}
	out.WriteString("</WorkingDirectory></Exec></Actions>\n</Task>\n")
	return out.Bytes(), nil
}

func joinWindowsArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = quoteWindowsArg(arg)
	}
	return strings.Join(quoted, " ")
}

// quoteWindowsArg는 CommandLineToArgvW 규칙에 맞게 인자 하나를 이스케이프한다.
func quoteWindowsArg(arg string) string {
	if arg != "" && !strings.ContainsAny(arg, " \t\n\v\"") {
		return arg
	}

	var out strings.Builder
	out.WriteByte('"')
	backslashes := 0
	for _, r := range arg {
		switch r {
		case '\\':
			backslashes++
		case '"':
			out.WriteString(strings.Repeat("\\", backslashes*2+1))
			out.WriteByte('"')
			backslashes = 0
		default:
			out.WriteString(strings.Repeat("\\", backslashes))
			backslashes = 0
			out.WriteRune(r)
		}
	}
	out.WriteString(strings.Repeat("\\", backslashes*2))
	out.WriteByte('"')
	return out.String()
}

func windowsDir(path string) string {
	if i := strings.LastIndexAny(path, `\/`); i >= 0 {
		return path[:i]
	}
	return "."
}
