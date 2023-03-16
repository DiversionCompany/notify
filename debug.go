// Copyright (c) 2014-2015 The Notify Authors. All rights reserved.
// Use of this source code is governed by the MIT license that can be
// found in the LICENSE file.

package notify

import (
	"log"
	"os"
)

var dbgprint func(...interface{})

var dbgprintf func(string, ...interface{})

func SetLogger(print func(...interface{}), printf func(string, ...interface{})) {
	dbgprint = print
	dbgprintf = printf
}

func init() {
	if _, ok := os.LookupEnv("NOTIFY_DEBUG"); ok || debugTag {
		log.SetOutput(os.Stdout)
		log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
		dbgprint = func(v ...interface{}) {
			v = append([]interface{}{"[D] "}, v...)
			log.Println(v...)
		}
		dbgprintf = func(format string, v ...interface{}) {
			format = "[D] " + format
			log.Printf(format, v...)
		}
		return
	}
	dbgprint = func(v ...interface{}) {}
	dbgprintf = func(format string, v ...interface{}) {}
}
