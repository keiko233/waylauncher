package instance

import (
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestClassify(t *testing.T) {
	windowClose := fixture(t, "waypipe-window-close.log")
	lost := fixture(t, "waypipe-connection-lost.log")
	tests := []struct {
		name     string
		code     int
		log      string
		treat    bool
		normal   bool
		contains string
	}{
		{"clean exit", 0, "", true, true, "code 0"},
		{"clean exit ignores log", 0, lost, false, true, ""},
		{"window close, setting on", 1, windowClose, true, true, "No child processes"},
		{"window close, setting off", 1, windowClose, false, false, "code 1"},
		{"exit 1 without marker", 1, lost, true, false, "code 1"},
		{"marker but other code", 2, windowClose, true, false, "code 2"},
		{"ssh failure 255", 255, "", true, false, "255"},
		{"killed by signal", -1, windowClose, true, false, "signal"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.code, tc.log, tc.treat)
			if got.Normal != tc.normal || !strings.Contains(got.Reason, tc.contains) {
				t.Fatalf("got %+v", got)
			}
		})
	}
}
