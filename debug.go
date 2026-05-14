// Copyright (c) 2014-2015 The Notify Authors. All rights reserved.
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package notify

import (
	"fmt"
	"log"
	"os"
	"runtime"
	"strings"
)

// Level classifies log messages emitted by the notify package.
type Level int

const (
	LevelDebug Level = iota
	LevelWarn
	LevelError
)

var logger func(level Level, format string, v ...interface{})

// SetLogger installs a leveled callback that receives all log messages emitted
// by the notify package. Passing nil disables logging.
func SetLogger(fn func(Level, string, ...interface{})) {
	logger = fn
}

func debugf(format string, v ...interface{}) {
	if logger != nil {
		logger(LevelDebug, format, v...)
	}
}

func warnf(format string, v ...interface{}) {
	if logger != nil {
		logger(LevelWarn, format, v...)
	}
}

func errorf(format string, v ...interface{}) {
	if logger != nil {
		logger(LevelError, format, v...)
	}
}

// dbgprintf and dbgprint preserve the old call shape used throughout the
// package; they route to debugf so existing call sites keep working without
// modification.
var dbgprintf = debugf

var dbgprint = func(v ...interface{}) {
	debugf("%s", fmt.Sprint(v...))
}

func dbgcallstack(max int) []string {
	pc, stack := make([]uintptr, max), make([]string, 0, max)
	runtime.Callers(2, pc)
	for _, pc := range pc {
		if f := runtime.FuncForPC(pc); f != nil {
			fname := f.Name()
			idx := strings.LastIndex(fname, string(os.PathSeparator))
			if idx != -1 {
				stack = append(stack, fname[idx+1:])
			} else {
				stack = append(stack, fname)
			}
		}
	}
	return stack
}

func init() {
	if _, ok := os.LookupEnv("NOTIFY_DEBUG"); ok || debugTag {
		log.SetOutput(os.Stdout)
		log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
		logger = func(level Level, format string, v ...interface{}) {
			prefix := "[D] "
			switch level {
			case LevelWarn:
				prefix = "[W] "
			case LevelError:
				prefix = "[E] "
			}
			log.Printf(prefix+format, v...)
		}
	}
}
