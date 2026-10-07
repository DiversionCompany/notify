//go:build windows
// +build windows

package notify

import (
	"syscall"
	"testing"
)

func TestArmFirstReadRetriesWithNetworkBufferSize(t *testing.T) {
	tests := []struct {
		name      string
		readErr   error // returned while the buffer is over 64 KB
		wantErr   error
		wantSize  int
		wantCalls int
	}{
		{"network share", errorInvalidParameter, nil, networkReadBufferSize, 2},
		{"other error", syscall.ERROR_ACCESS_DENIED, syscall.ERROR_ACCESS_DENIED, readBufferSize, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := &grip{buffer: make([]byte, readBufferSize)}
			calls := 0
			err := g.armFirstRead(func(g *grip) error {
				calls++
				if len(g.buffer) > networkReadBufferSize {
					return tt.readErr
				}
				return nil
			})
			if err != tt.wantErr || len(g.buffer) != tt.wantSize || calls != tt.wantCalls {
				t.Fatalf("err=%v size=%d calls=%d; want err=%v size=%d calls=%d",
					err, len(g.buffer), calls, tt.wantErr, tt.wantSize, tt.wantCalls)
			}
		})
	}
}
