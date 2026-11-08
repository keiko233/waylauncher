package instance

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLogCreatePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	l := &LogStore{Dir: dir}
	f, err := l.Create("konsole", 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", st.Mode())
	}
	if st, _ := os.Stat(f.Name()); st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v", st.Mode())
	}
}

func TestLogCreateTightensExistingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	os.Mkdir(dir, 0o755)
	f, err := (&LogStore{Dir: dir}).Create("a", 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", st.Mode())
	}
}

func TestLogRotation(t *testing.T) {
	dir := t.TempDir()
	clock := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	l := &LogStore{Dir: dir, Now: func() time.Time { clock = clock.Add(time.Second); return clock }}

	// Another app whose ID shares a prefix must not be touched.
	other := filepath.Join(dir, "foo-bar_20200101T000000.000_aaaa.log")
	os.WriteFile(other, nil, 0o600)

	var names []string
	for i := 0; i < 5; i++ {
		f, err := l.Create("foo", 3, nil)
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
		names = append(names, f.Name())
	}
	for i, n := range names {
		_, err := os.Stat(n)
		if i < 2 && err == nil {
			t.Errorf("%s should have been rotated away", n)
		}
		if i >= 2 && err != nil {
			t.Errorf("%s should be kept", n)
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("log of a different app was removed")
	}
}

func TestLogRotationProtectsLive(t *testing.T) {
	dir := t.TempDir()
	clock := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	l := &LogStore{Dir: dir, Now: func() time.Time { clock = clock.Add(time.Second); return clock }}
	first, _ := l.Create("foo", 1, nil)
	first.Close()
	second, err := l.Create("foo", 1, map[string]bool{first.Name(): true})
	if err != nil {
		t.Fatal(err)
	}
	second.Close()
	if _, err := os.Stat(first.Name()); err != nil {
		t.Fatal("protected (live) log was deleted")
	}
}
