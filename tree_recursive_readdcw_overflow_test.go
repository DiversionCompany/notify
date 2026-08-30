//go:build windows
// +build windows

package notify

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestRecursiveTreeOverflowReachesNestedLogicalWatches(t *testing.T) {
	for _, parentFirst := range []bool{true, false} {
		name := "child first"
		if parentFirst {
			name = "parent first"
		}
		t.Run(name, func(t *testing.T) {
			backend := make(chan EventInfo, 1)
			var watcher Spy
			tree := newRecursiveTree(&watcher, backend)
			defer tree.Close()

			parent := filepath.Clean(t.TempDir())
			child := filepath.Join(parent, "submodule")
			if err := os.Mkdir(child, 0755); err != nil {
				t.Fatal(err)
			}
			parentEvents := make(chan EventInfo, 2)
			childEvents := make(chan EventInfo, 2)
			if parentFirst {
				watchOverflowPath(t, tree, parent, parentEvents, FileNotifyOverflow)
				watchOverflowPath(t, tree, child, childEvents, FileNotifyOverflow)
			} else {
				watchOverflowPath(t, tree, child, childEvents, FileNotifyOverflow)
				watchOverflowPath(t, tree, parent, parentEvents, FileNotifyOverflow)
			}

			overflow := dispatchOverflow(t, tree, parent)
			assertSingleOverflowAtPath(t, parentEvents, parent)
			assertSingleOverflowAtPath(t, childEvents, child)
			if overflow.Path() != parent {
				t.Fatalf("source event path=%q; want %q", overflow.Path(), parent)
			}
		})
	}
}

func TestRecursiveTreeOverflowHonorsLogicalWatchFilters(t *testing.T) {
	backend := make(chan EventInfo, 1)
	var watcher Spy
	tree := newRecursiveTree(&watcher, backend)
	defer tree.Close()

	parent := filepath.Clean(t.TempDir())
	writeOnly := filepath.Join(parent, "write-only")
	allOnly := filepath.Join(parent, "all-only")
	combined := filepath.Join(parent, "combined")
	for _, path := range []string{writeOnly, allOnly, combined} {
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Clean(t.TempDir())

	parentEvents := make(chan EventInfo, 2)
	writeEvents := make(chan EventInfo, 2)
	allEvents := make(chan EventInfo, 2)
	combinedEvents := make(chan EventInfo, 2)
	outsideEvents := make(chan EventInfo, 2)
	watchOverflowPath(t, tree, parent, parentEvents, Write)
	watchOverflowPath(t, tree, writeOnly, writeEvents, Write)
	watchOverflowPath(t, tree, allOnly, allEvents, All)
	watchOverflowPath(t, tree, combined, combinedEvents, Write|FileNotifyOverflow)
	watchOverflowPath(t, tree, outside, outsideEvents, FileNotifyOverflow)

	dispatchOverflow(t, tree, parent)

	assertSingleOverflowAtPath(t, combinedEvents, combined)
	assertNoEvent(t, parentEvents)
	assertNoEvent(t, writeEvents)
	assertNoEvent(t, allEvents)
	assertNoEvent(t, outsideEvents)

	dispatchOverflow(t, tree, outside)

	assertSingleOverflowAtPath(t, outsideEvents, outside)
	assertNoEvent(t, parentEvents)
	assertNoEvent(t, writeEvents)
	assertNoEvent(t, allEvents)
	assertNoEvent(t, combinedEvents)
}

func TestRecursiveTreeOverflowSendsOncePerLogicalRoot(t *testing.T) {
	backend := make(chan EventInfo, 1)
	var watcher Spy
	tree := newRecursiveTree(&watcher, backend)
	defer tree.Close()

	parent := filepath.Clean(t.TempDir())
	child := filepath.Join(parent, "submodule")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	events := make(chan EventInfo, 4)
	watchOverflowPath(t, tree, parent, events, FileNotifyOverflow)
	watchOverflowPath(t, tree, parent, events, FileNotifyOverflow)
	if err := tree.Watch(child, events, nil, FileNotifyOverflow); err != nil {
		t.Fatalf("Watch(%q): %v", child, err)
	}

	dispatchOverflow(t, tree, parent)

	got := make(map[string]int)
	for {
		select {
		case event := <-events:
			got[event.Path()]++
		default:
			if got[parent] != 1 || got[child] != 1 || len(got) != 2 {
				t.Fatalf("overflow paths=%v; want one event for %q and %q", got, parent, child)
			}
			return
		}
	}
}

func watchOverflowPath(
	t *testing.T,
	tree *recursiveTree,
	path string,
	events chan EventInfo,
	filter Event,
) {
	t.Helper()
	if err := tree.Watch(filepath.Join(path, "..."), events, nil, filter); err != nil {
		t.Fatalf("Watch(%q): %v", path, err)
	}
}

func dispatchOverflow(t *testing.T, tree *recursiveTree, path string) *event {
	t.Helper()
	pathw, err := syscall.UTF16FromString(path)
	if err != nil {
		t.Fatal(err)
	}
	overflow := &event{
		pathw: pathw,
		ftype: fTypeDirectory,
		e:     FileNotifyOverflow,
	}
	tree.dispatchEvent(overflow)
	return overflow
}

func assertSingleOverflowAtPath(t *testing.T, events chan EventInfo, wantPath string) {
	t.Helper()
	select {
	case event := <-events:
		if event.Event() != FileNotifyOverflow {
			t.Fatalf("event=%v; want %v", event.Event(), FileNotifyOverflow)
		}
		if event.Path() != wantPath {
			t.Fatalf("event path=%q; want %q", event.Path(), wantPath)
		}
		if got, want := fmt.Sprint(event), FileNotifyOverflow.String()+`: "`+wantPath+`"`; got != want {
			t.Fatalf("event string=%q; want %q", got, want)
		}
		isDir, ok := event.(isDirer)
		if !ok {
			t.Fatal("overflow event does not preserve isDirer")
		}
		if dir, err := isDir.isDir(); err != nil || !dir {
			t.Fatalf("overflow event isDir()=(%v, %v); want (true, nil)", dir, err)
		}
	default:
		t.Fatalf("no overflow event for %q", wantPath)
	}
	assertNoEvent(t, events)
}

func assertNoEvent(t *testing.T, events chan EventInfo) {
	t.Helper()
	select {
	case event := <-events:
		t.Fatalf("unexpected event: %v", event)
	default:
	}
}
