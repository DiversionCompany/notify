// Copyright (c) 2014-2015 The Notify Authors. All rights reserved.
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

//go:build !windows
// +build !windows

package notify

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"testing"
)

func TestNodeAddDir(t *testing.T) {
	root := t.TempDir()
	join := func(elem ...string) string {
		return filepath.Join(append([]string{root}, elem...)...)
	}
	err := nonil(
		os.MkdirAll(join("a", "b"), 0755),
		os.MkdirAll(join("ignored", "c"), 0755),
		os.WriteFile(join("file"), nil, 0644),
		os.WriteFile(join("a", "file"), nil, 0644),
		os.Symlink(join("a"), join("link")),
	)
	if err != nil {
		t.Fatal(err)
	}
	var added, checked []string
	addFn := func(nd node) error {
		added = append(added, nd.Name)
		return nil
	}
	doNotWatch := func(name string) bool {
		checked = append(checked, name)
		return name == join("ignored")
	}
	if err := newnode(root).AddDir(addFn, doNotWatch); err != nil {
		t.Fatalf("AddDir()=%v", err)
	}
	sort.Strings(added)
	sort.Strings(checked)
	if want := []string{root, join("a"), join("a", "b")}; !reflect.DeepEqual(added, want) {
		t.Errorf("want added=%v; got %v", want, added)
	}
	if want := []string{join("a"), join("a", "b"), join("ignored")}; !reflect.DeepEqual(checked, want) {
		t.Errorf("want doNotWatch calls=%v; got %v", want, checked)
	}
}

func BenchmarkNodeAddDir(b *testing.B) {
	root := b.TempDir()
	for i := 0; i < 2000; i++ {
		dir := filepath.Join(root, strconv.Itoa(i/50), strconv.Itoa(i))
		if err := os.MkdirAll(dir, 0755); err != nil {
			b.Fatal(err)
		}
		for j := 0; j < 20; j++ {
			if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(j)), nil, 0644); err != nil {
				b.Fatal(err)
			}
		}
	}
	calls := 0
	doNotWatch := func(string) bool {
		calls++
		return false
	}
	addFn := func(node) error { return nil }
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := newnode(root).AddDir(addFn, doNotWatch); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(calls)/float64(b.N), "doNotWatch/op")
}
