// Package instance supervises transport processes.
//
// States describe the transport process, not the remote application:
// "connected" means the transport process is alive. It does not mean a window
// has been shown (no compositor reports that yet). The supervisor never
// restarts a remote application; only pre-launch checks are retried.
package instance

import (
	"strconv"
	"strings"
	"time"
)

type State string

const (
	Connecting   State = "connecting"   // running pre-checks (may retry) or starting the transport
	Connected    State = "connected"    // transport process is alive
	Disconnected State = "disconnected" // transport exited abnormally; needs a manual start
	Exited       State = "exited"       // normal exit, or stopped by the user
	Failed       State = "failed"       // a pre-check failed; the application was never started
	Stopping     State = "stopping"     // user asked to stop; waiting for the process group to die
)

// Live reports whether a process may still be running or about to start.
func (s State) Live() bool { return s == Connecting || s == Connected || s == Stopping }

// Snapshot is an immutable copy of an instance's public state.
type Snapshot struct {
	ID        string     `json:"id"`
	AppID     string     `json:"appId"`
	AppName   string     `json:"appName"`
	HostID    string     `json:"hostId"`
	State     State      `json:"state"`
	ExitCode  *int       `json:"exitCode,omitempty"`
	Reason    string     `json:"reason,omitempty"` // classification or failure message
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
	LogPath   string     `json:"logPath,omitempty"`
}

type Event struct {
	Instance Snapshot
	Previous State // empty for the first event of an instance
}

type Action string

const (
	ActionStarted Action = "started" // a new instance was created
	ActionFocus   Action = "focus"   // an existing instance was returned; the caller should activate it
)

type LaunchResult struct {
	Instance Snapshot
	Action   Action
}

// Exit is the outcome of classifying a transport exit.
type Exit struct {
	Normal bool
	Reason string
}

// noChildMarker is printed by waypipe-darwin when a window is closed normally
// but its child cannot be waited for; the process then exits with code 1.
const noChildMarker = "No child processes"

// Classify is the only place that decides whether an exit is a disconnect.
// code is the process exit code, or -1 if it was killed by a signal.
func Classify(code int, logTail string, treatNoChildAsNormal bool) Exit {
	switch {
	case code == 0:
		return Exit{Normal: true, Reason: "exit code 0"}
	case code == 1 && treatNoChildAsNormal && strings.Contains(logTail, noChildMarker):
		return Exit{Normal: true, Reason: `exit code 1 with "No child processes" (known Waypipe-darwin behaviour when a window closes)`}
	case code < 0:
		return Exit{Reason: "transport was terminated by a signal"}
	default:
		return Exit{Reason: "transport exited with code " + strconv.Itoa(code)}
	}
}
