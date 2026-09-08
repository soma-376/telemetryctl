package instancelock

import "testing"

func TestExclusiveAndRelease(t *testing.T) {
	dir := t.TempDir()
	a, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := Acquire(dir); err == nil {
		_ = b.Close()
		t.Fatal("중복 잠금 허용")
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = b.Close()
}
