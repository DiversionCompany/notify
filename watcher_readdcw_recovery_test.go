//go:build windows
// +build windows

package notify

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

var procCancelIoExRecovery = modkernel32.NewProc("CancelIoEx")

type logCapture struct {
	mu   sync.Mutex
	msgs []string
}

func (lc *logCapture) log(_ Level, format string, v ...interface{}) {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	lc.msgs = append(lc.msgs, fmt.Sprintf(format, v...))
}

func (lc *logCapture) contains(substr string) bool {
	lc.mu.Lock()
	defer lc.mu.Unlock()
	for _, m := range lc.msgs {
		if strings.Contains(m, substr) {
			return true
		}
	}
	return false
}

// TestRecreateHandleAfterFailedCompletion covers handleFailedCompletion: a
// pending ReadDirectoryChangesW that fails outside our own unwatch/rewatch
// must be answered by recreating the handle, and the watch must stay alive.
//
// CancelIoEx aborts the pending I/O the way a filter driver or a volume reset
// does: the completion port receives a packet with ERROR_OPERATION_ABORTED and
// zero bytes. On a healthy volume even the aborted handle happens to accept a
// re-arm, so "events resume" alone cannot tell recovery from the old blind
// re-arm -- the log assertion pins the recovery path. After a real volume
// reset only a recreated handle delivers events again (ONC-5571).
func TestRecreateHandleAfterFailedCompletion(t *testing.T) {
	lc := &logCapture{}
	SetLogger(lc.log)
	defer SetLogger(nil)

	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("0"), 0644); err != nil {
		t.Fatal(err)
	}

	c := make(chan EventInfo, 256)
	r := newWatcher(c).(*readdcw)
	defer r.Close()

	// The Write filter creates one grip. The synthetic overflow subscription
	// does not add another, so there is one pending I/O to abort.
	if err := r.Watch(dir, Write|FileNotifyOverflow); err != nil {
		t.Fatalf("Watch: %v", err)
	}

	awaitEvent := func(stage string) {
		t.Helper()
		deadline := time.After(10 * time.Second)
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		payload := []byte(stage)
		for {
			select {
			case event := <-c:
				if event.Event() == FileNotifyOverflow {
					t.Fatalf("%s: failed completion was reported as overflow", stage)
				}
				return
			case <-tick.C:
				// Keep rewriting: the watcher may still be (re)arming.
				payload = append(payload, 'x')
				if err := os.WriteFile(target, payload, 0644); err != nil {
					t.Fatalf("%s: write: %v", stage, err)
				}
			case <-deadline:
				t.Fatalf("%s: no event within 10s -- watcher is dead", stage)
			}
		}
	}

	awaitEvent("before-failure")

	r.Lock()
	wd, ok := r.m[dir]
	if !ok || wd.digrip[0] == nil {
		r.Unlock()
		t.Fatalf("no grip found for %q", dir)
	}
	handle := syscall.Handle(wd.digrip[0].handle)
	r.Unlock()

	if ret, _, err := procCancelIoExRecovery.Call(uintptr(handle), 0); ret == 0 {
		t.Fatalf("CancelIoEx: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for !lc.contains("recreating the watch handle") {
		if time.Now().After(deadline) {
			t.Fatal("failed completion was not detected within 5s (error discarded?)")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// drain events emitted before the failure so the next await is genuine
	for {
		select {
		case event := <-c:
			if event.Event() == FileNotifyOverflow {
				t.Fatal("failed completion was reported as overflow")
			}
			continue
		default:
		}
		break
	}

	awaitEvent("after-recovery")
}

// TestRegisterFailureClosesHandle covers register failing after CreateFile
// succeeded. Arming a plain file forces that: CreateFile and the completion
// port association accept a file handle, but ReadDirectoryChangesW needs a
// directory and fails. The grip must not keep the open, unarmed handle -- no
// completion will ever arrive for it, so it must read as dead (InvalidHandle).
func TestRegisterFailureClosesHandle(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "plain.txt")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	pathw, err := syscall.UTF16FromString(file)
	if err != nil {
		t.Fatal(err)
	}

	cph, err := syscall.CreateIoCompletionPort(syscall.InvalidHandle, 0, 0, 0)
	if err != nil {
		t.Fatalf("CreateIoCompletionPort: %v", err)
	}
	defer syscall.CloseHandle(cph)

	g := &grip{
		handle:   syscall.InvalidHandle,
		filter:   uint32(Write),
		pathw:    pathw,
		ovlapped: &overlappedEx{},
	}
	g.ovlapped.parent = g

	if err := g.register(cph); err == nil {
		t.Fatal("register on a plain file should fail at readDirChanges")
	}
	if g.handle != syscall.InvalidHandle {
		t.Fatalf("failed register left an open handle: %v", g.handle)
	}
}
