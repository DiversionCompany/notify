//go:build windows
// +build windows

package notify

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestReadDirectoryChangesBufferSize(t *testing.T) {
	const want = 64 * 1024
	if readBufferSize != want {
		t.Fatalf("readBufferSize=%d; want %d", readBufferSize, want)
	}
	if got := len((grip{}).buffer); got != want {
		t.Fatalf("len(grip.buffer)=%d; want %d", got, want)
	}
}

func TestFileNotifyOverflowContract(t *testing.T) {
	if FileNotifyOverflow != Event(1<<27) {
		t.Fatalf("FileNotifyOverflow=%#x; want %#x", FileNotifyOverflow, Event(1<<27))
	}
	if All&FileNotifyOverflow != 0 {
		t.Fatalf("All=%#x unexpectedly contains FileNotifyOverflow", All)
	}
	if got, want := FileNotifyOverflow.String(), "notify.FileNotifyOverflow"; got != want {
		t.Fatalf("FileNotifyOverflow.String()=%q; want %q", got, want)
	}
}

func TestReadDirectoryChangesFiltersStripOverflow(t *testing.T) {
	tests := []struct {
		name   string
		filter Event
		want   uint32
	}{
		{
			name:   "overflow only",
			filter: FileNotifyOverflow,
			want:   fileNotifyChangeAll,
		},
		{
			name:   "portable event",
			filter: Write | FileNotifyOverflow,
			want:   encode(uint32(Write)),
		},
		{
			name:   "native event",
			filter: FileNotifyChangeSize | FileNotifyOverflow,
			want:   uint32(FileNotifyChangeSize),
		},
		{
			name:   "all",
			filter: All | FileNotifyOverflow,
			want:   encode(uint32(All)),
		},
		{
			name:   "directory marker",
			filter: dirmarker | FileNotifyOverflow,
			want:   uint32(FileNotifyChangeDirName),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := encode(uint32(tt.filter)); got&uint32(FileNotifyOverflow) != 0 {
				t.Fatalf("encode(%#x) leaked overflow bit: %#x", tt.filter, got)
			}
			if got := readDirectoryChangesFilter(uint32(tt.filter)); got != tt.want {
				t.Fatalf("readDirectoryChangesFilter(%#x)=%#x; want %#x",
					tt.filter, got, tt.want)
			}
		})
	}
	if got := encode(uint32(FileNotifyOverflow)); got != 0 {
		t.Fatalf("encode(FileNotifyOverflow)=%#x; want 0", got)
	}
}

func TestSplitReadDirectoryChangesFilters(t *testing.T) {
	tests := []struct {
		name     string
		filter   uint32
		wantFile uint32
		wantDir  uint32
	}{
		{
			name:     "overflow only",
			filter:   uint32(FileNotifyOverflow),
			wantFile: uint32(FileNotifyOverflow),
		},
		{
			name:    "directory event and overflow",
			filter:  uint32(FileNotifyChangeDirName | FileNotifyOverflow),
			wantDir: uint32(FileNotifyChangeDirName | dirmarker),
		},
		{
			name:     "file event and overflow",
			filter:   uint32(Write | FileNotifyOverflow),
			wantFile: uint32(Write),
		},
		{
			name:     "all and overflow during rewatch",
			filter:   stateRewatch | uint32(All|FileNotifyOverflow),
			wantFile: uint32(All),
			wantDir:  uint32(All | dirmarker),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFile, gotDir := splitReadDirectoryChangesFilters(tt.filter)
			if gotFile != tt.wantFile || gotDir != tt.wantDir {
				t.Fatalf("splitReadDirectoryChangesFilters(%#x)=(%#x, %#x); want (%#x, %#x)",
					tt.filter, gotFile, gotDir, tt.wantFile, tt.wantDir)
			}
		})
	}
}

func newSuccessfulCompletionTest(t *testing.T, filter uint32) (
	*readdcw, *watched, *overlappedEx, chan EventInfo,
) {
	t.Helper()
	path := filepath.Clean(t.TempDir())
	pathw, err := syscall.UTF16FromString(path)
	if err != nil {
		t.Fatal(err)
	}
	wd := &watched{
		filter: filter,
		count:  2,
		pathw:  pathw,
	}
	g := &grip{
		handle: syscall.InvalidHandle,
		filter: filter,
		pathw:  pathw,
		parent: wd,
	}
	overEx := &overlappedEx{parent: g}
	g.ovlapped = overEx
	wd.digrip[0] = g
	events := make(chan EventInfo, 2)
	r := &readdcw{
		m:   map[string]*watched{path: wd},
		cph: syscall.InvalidHandle,
		c:   events,
	}
	return r, wd, overEx, events
}

func TestSuccessfulZeroCompletionReportsOverflowAndRearms(t *testing.T) {
	r, _, overEx, events := newSuccessfulCompletionTest(
		t, uint32(Write|FileNotifyOverflow),
	)
	rearms := 0
	err := r.handleSuccessfulCompletion(0, overEx, func(g *grip) error {
		if g != overEx.parent {
			t.Fatalf("rearmed grip %p; want %p", g, overEx.parent)
		}
		rearms++
		return nil
	})
	if err != nil {
		t.Fatalf("handleSuccessfulCompletion: %v", err)
	}
	if rearms != 1 {
		t.Fatalf("rearm count=%d; want 1", rearms)
	}

	select {
	case event := <-events:
		if event.Event() != FileNotifyOverflow {
			t.Fatalf("event=%v; want %v", event.Event(), FileNotifyOverflow)
		}
		wantPath := filepath.Clean(syscall.UTF16ToString(overEx.parent.pathw))
		if event.Path() != wantPath {
			t.Fatalf("event path=%q; want watched root %q", event.Path(), wantPath)
		}
	default:
		t.Fatal("successful zero-byte completion did not report overflow")
	}
	select {
	case event := <-events:
		t.Fatalf("successful zero-byte completion reported an extra event: %v", event)
	default:
	}
}

func TestSuccessfulZeroCompletionRearmsBeforeBlockedOverflowSend(t *testing.T) {
	r, wd, overEx, _ := newSuccessfulCompletionTest(
		t, uint32(Write|FileNotifyOverflow),
	)
	events := make(chan EventInfo, 1)
	events <- &event{e: Write}
	r.c = events

	rearmed := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- r.handleSuccessfulCompletion(0, overEx, func(*grip) error {
			close(rearmed)
			return nil
		})
	}()

	select {
	case <-rearmed:
	case <-time.After(5 * time.Second):
		<-events // Let the blocked overflow send and handler finish.
		if err := <-done; err != nil {
			t.Fatalf("handleSuccessfulCompletion: %v", err)
		}
		t.Fatal("overflow delivery blocked the watch before it was rearmed")
	}

	rewatched := make(chan error, 1)
	go func() {
		rewatched <- r.Rewatch(
			syscall.UTF16ToString(wd.pathw),
			Write|FileNotifyOverflow,
			Rename|FileNotifyOverflow,
		)
	}()
	select {
	case err := <-rewatched:
		if err != nil {
			t.Fatalf("Rewatch: %v", err)
		}
	case <-time.After(5 * time.Second):
		<-events // Let the blocked overflow send release the watcher lock.
		if err := <-done; err != nil {
			t.Fatalf("handleSuccessfulCompletion: %v", err)
		}
		if err := <-rewatched; err != nil {
			t.Fatalf("Rewatch: %v", err)
		}
		t.Fatal("overflow delivery blocked rewatch")
	}

	<-events // Remove the event that filled the channel.
	select {
	case event := <-events:
		if event.Event() != FileNotifyOverflow {
			t.Fatalf("event=%v; want %v", event.Event(), FileNotifyOverflow)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("overflow event was not delivered after rearm")
	}
	if err := <-done; err != nil {
		t.Fatalf("handleSuccessfulCompletion: %v", err)
	}
	if wd.count != 2 {
		t.Fatalf("teardown count=%d; old completion consumed new rewatch state", wd.count)
	}
}

func TestSuccessfulZeroCompletionSuppressedDuringTeardown(t *testing.T) {
	tests := []struct {
		name   string
		filter uint32
		state  bool
	}{
		{name: "not subscribed", filter: uint32(Write)},
		{
			name:   "rewatch",
			filter: uint32(FileNotifyOverflow) | stateRewatch,
			state:  true,
		},
		{
			name:   "unwatch",
			filter: uint32(FileNotifyOverflow) | stateUnwatch,
			state:  true,
		},
		{
			name:   "close",
			filter: uint32(FileNotifyOverflow) | stateCPClose,
			state:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, wd, overEx, events := newSuccessfulCompletionTest(t, tt.filter)
			rearms := 0
			err := r.handleSuccessfulCompletion(0, overEx, func(*grip) error {
				rearms++
				return nil
			})
			if err != nil {
				t.Fatalf("handleSuccessfulCompletion: %v", err)
			}
			if rearms != 1 {
				t.Fatalf("rearm count=%d; want 1", rearms)
			}
			if tt.state && wd.count != 1 {
				t.Fatalf("teardown count=%d; want 1 after one completion", wd.count)
			}
			select {
			case event := <-events:
				t.Fatalf("unexpected overflow event during %s: %v", tt.name, event)
			default:
			}
		})
	}
}

func TestWatchAndRewatchAcceptFileNotifyOverflow(t *testing.T) {
	events := make(chan EventInfo, 1)
	r := newWatcher(events).(*readdcw)
	defer r.Close()

	path := t.TempDir()
	if err := r.Watch(path, FileNotifyOverflow); err != nil {
		t.Fatalf("Watch(FileNotifyOverflow): %v", err)
	}
	if err := r.Rewatch(
		path,
		FileNotifyOverflow,
		Write|FileNotifyOverflow,
	); err != nil {
		t.Fatalf("Rewatch(FileNotifyOverflow): %v", err)
	}
}

func TestRewatchOverflowSubscriptionKeepsNativeWatch(t *testing.T) {
	events := make(chan EventInfo, 1)
	r := newWatcher(events).(*readdcw)
	defer r.Close()

	path := t.TempDir()
	if err := r.Watch(path, Write); err != nil {
		t.Fatalf("Watch(Write): %v", err)
	}

	r.Lock()
	wd := r.m[path]
	grip := wd.digrip[0]
	handle := grip.handle
	count := wd.count
	r.Unlock()

	for _, filters := range []struct {
		old Event
		new Event
	}{
		{old: Write, new: Write | FileNotifyOverflow},
		{old: Write | FileNotifyOverflow, new: Write},
	} {
		if err := r.Rewatch(path, filters.old, filters.new); err != nil {
			t.Fatalf("Rewatch(%v, %v): %v", filters.old, filters.new, err)
		}
		r.Lock()
		if wd.filter != uint32(filters.new) {
			r.Unlock()
			t.Fatalf("filter=%#x; want %#x", wd.filter, filters.new)
		}
		if wd.digrip[0] != grip || grip.handle != handle || wd.count != count {
			r.Unlock()
			t.Fatal("overflow subscription change restarted the native watch")
		}
		r.Unlock()
	}
}

func TestFileNotifyOverflowDispatchesAtRecursiveWatchRoot(t *testing.T) {
	backend := make(chan EventInfo, buffer)
	r := newWatcher(backend).(*readdcw)
	tree := newRecursiveTree(r, backend)
	defer tree.Close()

	events := make(chan EventInfo, 1)
	path := filepath.Clean(t.TempDir())
	if err := tree.Watch(
		filepath.Join(path, "..."),
		events,
		nil,
		FileNotifyOverflow,
	); err != nil {
		t.Fatalf("Watch(FileNotifyOverflow): %v", err)
	}

	r.Lock()
	wd, ok := r.m[path]
	if !ok || wd.digrip[0] == nil {
		r.Unlock()
		t.Fatalf("no overflow grip found for %q", path)
	}
	overEx := wd.digrip[0].ovlapped
	r.Unlock()

	if err := r.handleSuccessfulCompletion(
		0,
		overEx,
		func(*grip) error { return nil },
	); err != nil {
		t.Fatalf("handleSuccessfulCompletion: %v", err)
	}

	select {
	case event := <-events:
		if event.Event() != FileNotifyOverflow {
			t.Fatalf("event=%v; want %v", event.Event(), FileNotifyOverflow)
		}
		if event.Path() != path {
			t.Fatalf("event path=%q; want watched root %q", event.Path(), path)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("overflow event was not dispatched to the recursive watchpoint")
	}
}
