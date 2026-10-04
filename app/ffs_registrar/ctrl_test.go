package main

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
)

func TestReadySetup(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typeID  byte
		request byte
		length  byte
		ready   uint32
		want    []byte
	}{
		{"vendor ready", 0xC1, 0x5A, 8, 1, []byte{'T', 'Z', 'S', 'G', 1, 0, 1, 0}},
		{"legacy ready", 0x81, 0x5A, 8, 1, []byte{'T', 'Z', 'S', 'G', 1, 0, 1, 0}},
		{"not ready", 0xC1, 0x5A, 8, 0, []byte{'T', 'Z', 'S', 'G', 1, 0, 0, 0}},
		{"short reply", 0xC1, 0x5A, 4, 1, []byte("TZSG")},
		{"wrong recipient", 0xC0, 0x5A, 8, 1, nil},
		{"wrong direction", 0x41, 0x5A, 8, 1, nil},
		{"wrong request", 0xC1, 0x5B, 8, 1, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A regular file supplies one SETUP event, records the response after
			// it, then returns EOF to stop the real EP0 event handler.
			ep0, err := os.CreateTemp(t.TempDir(), "ep0")
			if err != nil {
				t.Fatal(err)
			}
			defer ep0.Close()
			event := []byte{tc.typeID, tc.request, 0, 0, 1, 0, tc.length, 0, evTypeSetup, 0, 0, 0}
			if _, err := ep0.Write(event); err != nil {
				t.Fatal(err)
			}
			if _, err := ep0.Seek(0, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			var ready atomic.Uint32
			ready.Store(tc.ready)
			drainEP0Events(ep0, make(chan bool, 1), &ready, slog.New(slog.NewTextHandler(io.Discard, nil)))
			data, err := os.ReadFile(ep0.Name())
			if err != nil {
				t.Fatal(err)
			}
			if got := data[len(event):]; !bytes.Equal(got, tc.want) {
				t.Fatalf("reply = %x, want %x", got, tc.want)
			}
		})
	}
}
