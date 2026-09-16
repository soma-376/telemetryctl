package autostart

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
)

func windowsOptions(t *testing.T, fr *fakeRunner) Options {
	t.Helper()
	return Options{
		Env:      testEnv(t, "windows"),
		GOOS:     "windows",
		UserID:   `ACME\junil`,
		Runner:   fr,
		ExecPath: `C:\Program Files\Pulsemetry\telemetryctl.exe`,
		Args:     []string{"daemon", "--data-dir", `C:\Users\junil\Pulsemetry Data`},
	}
}

func TestWindowsEnable은XML등록후즉시실행한다(t *testing.T) {
	fr := newFakeRunner(nil)
	opts := windowsOptions(t, fr)
	m := newTestManager(t, opts)

	var taskXML string
	fr.onCall = func(c call) {
		if len(c.Args) >= 5 && c.Args[0] == "/Create" {
			content, err := os.ReadFile(c.Args[4])
			if err != nil {
				t.Fatalf("ReadFile(%q): %v", c.Args[4], err)
			}
			taskXML = string(content)
		}
	}

	res, err := m.Enable(context.Background())
	if err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if res.Kind != KindTaskScheduler || res.UnitPath != TaskPath() {
		t.Fatalf("result = %+v", res)
	}
	keys := fr.keys()
	if len(keys) != 2 {
		t.Fatalf("calls = %v", keys)
	}
	if !strings.HasPrefix(keys[0], "schtasks.exe /Create /TN "+TaskName+" /XML ") ||
		!strings.HasSuffix(keys[0], " /F") {
		t.Fatalf("create call = %q", keys[0])
	}
	if keys[1] != "schtasks.exe /Run /TN "+TaskName {
		t.Fatalf("run call = %q", keys[1])
	}
	for _, want := range []string{
		`<UserId>ACME\junil</UserId>`,
		`<Command>C:\Program Files\Pulsemetry\telemetryctl.exe</Command>`,
		`<Arguments>daemon --data-dir &#34;C:\Users\junil\Pulsemetry Data&#34;</Arguments>`,
		"<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>",
		"<RestartOnFailure><Interval>PT30S</Interval><Count>5</Count></RestartOnFailure>",
	} {
		if !strings.Contains(taskXML, want) {
			t.Errorf("task XML에 %q가 없음:\n%s", want, taskXML)
		}
	}
}

func TestWindowsStatus는XML에서등록경로를읽는다(t *testing.T) {
	xml := `<?xml version="1.0"?><Task xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task"><Settings><Enabled>true</Enabled></Settings><Actions><Exec><Command>C:\bin\telemetryctl.exe</Command></Exec></Actions></Task>`
	fr := newFakeRunner(map[string]scriptedReply{
		"schtasks.exe /Query /TN " + TaskName + " /XML": {Stdout: xml},
	})
	m := newTestManager(t, windowsOptions(t, fr))

	st, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !st.Supported || !st.Registered || !st.Loaded {
		t.Fatalf("status = %+v", st)
	}
	if st.RegisteredExecPath != `C:\bin\telemetryctl.exe` {
		t.Fatalf("RegisteredExecPath = %q", st.RegisteredExecPath)
	}
}

func TestWindowsDisable은실행종료후작업을삭제한다(t *testing.T) {
	fr := newFakeRunner(nil)
	m := newTestManager(t, windowsOptions(t, fr))

	res, err := m.Disable(context.Background())
	if err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if res.AlreadyInState {
		t.Fatal("등록된 작업을 미등록으로 판정했다")
	}
	want := []string{
		"schtasks.exe /Query /TN " + TaskName,
		"schtasks.exe /End /TN " + TaskName,
		"schtasks.exe /Delete /TN " + TaskName + " /F",
	}
	if got := fr.keys(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %#v, want %#v", got, want)
	}
}

func TestQuoteWindowsArg(t *testing.T) {
	tests := map[string]string{
		"":                    `""`,
		"daemon":              "daemon",
		`C:\Pulsemetry Data`:  `"C:\Pulsemetry Data"`,
		`value"quoted`:        `"value\"quoted"`,
		`C:\ends with slash\`: `"C:\ends with slash\\"`,
	}
	for input, want := range tests {
		if got := quoteWindowsArg(input); got != want {
			t.Errorf("quoteWindowsArg(%q) = %q, want %q", input, got, want)
		}
	}
}
