package imagestore

import "testing"

func TestLockReleaseNilSafe(t *testing.T) {
	var lock *Lock
	if err := lock.Release(); err != nil {
		t.Fatalf("nil receiver Release: %v, want nil", err)
	}
	lock2 := &Lock{}
	if err := lock2.Release(); err != nil {
		t.Fatalf("zero-value Release: %v, want nil", err)
	}
	if err := lock2.Release(); err != nil {
		t.Fatalf("second Release on zero-value: %v, want nil (idempotent)", err)
	}
}

func TestImageLockBasic(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if _, err := TryAcquireLock(dir); err == nil {
		t.Fatalf("second acquire should fail")
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	lock2, err := TryAcquireLock(dir)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	lock2.Release()
}
