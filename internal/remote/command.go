// Package remote builds remote command lines and probes SSH hosts.
package remote

import (
	"sort"
	"strings"
)

// Wrapper is the remote helper that scopes software rendering to forwarded
// applications. It is deployed separately on the host.
const Wrapper = "/usr/local/libexec/vm200-waypipe-app"

// Spec is the part of a profile needed to build the remote command.
type Spec struct {
	Command []string
	Cwd     string
	Env     map[string]string
}

// Argv returns the remote argument vector:
//
//	<wrapper> env [-C <cwd>] [KEY=VAL ...] -- <command...>
//
// The "env ... --" form is always emitted, even with no cwd and no env, so
// the shape is uniform and a command that starts with "-" or contains "="
// can never be mistaken for an env option or assignment. Env keys are sorted
// for deterministic output.
func Argv(s Spec) []string {
	argv := []string{Wrapper, "env"}
	if s.Cwd != "" {
		argv = append(argv, "-C", s.Cwd)
	}
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		argv = append(argv, k+"="+s.Env[k])
	}
	argv = append(argv, "--")
	return append(argv, s.Command...)
}

// Command joins Argv into the single string passed to ssh, which hands it to
// the remote login shell. Nothing is evaluated locally.
func Command(s Spec) string { return Join(Argv(s)) }

// Join quotes each element for a POSIX shell and joins with spaces.
func Join(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = Quote(a)
	}
	return strings.Join(parts, " ")
}

// Quote returns s as a single POSIX shell word. Words made only of
// conservative characters are left bare; everything else is wrapped in single
// quotes with embedded quotes written as '\”.
func Quote(s string) string {
	if s == "" {
		return "''"
	}
	bare := true
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			strings.ContainsRune("_@%+=:,./-", r)) {
			bare = false
			break
		}
	}
	if bare {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
