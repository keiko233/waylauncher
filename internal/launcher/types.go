// Package launcher is the application core behind the UI: it owns the loaded
// profiles and settings, the instance supervisor and the diagnostics, and
// exposes plain JSON-friendly values. It has no dependency on the desktop
// framework, so it is testable without a window.
package launcher

import (
	"time"

	"github.com/keiko233/waylauncher/internal/profile"
)

// Kind is a status colour class used by the UI: ok, warn, err, idle.
type Kind string

const (
	KindOK   Kind = "ok"
	KindWarn Kind = "warn"
	KindErr  Kind = "err"
	KindIdle Kind = "idle"
)

type State struct {
	Version   string           `json:"version"`
	ConfigDir string           `json:"configDir"`
	LogDir    string           `json:"logDir"`
	Profiles  ProfilesInfo     `json:"profiles"`
	Hosts     []HostInfo       `json:"hosts"`
	Apps      []AppInfo        `json:"apps"`
	Instances []InstanceInfo   `json:"instances"`
	Settings  profile.Settings `json:"settings"`
	Cocoa     CocoaInfo        `json:"cocoa"`
	Live      int              `json:"live"`
	Ended     int              `json:"ended"`
}

type ProfilesInfo struct {
	Source   string   `json:"source"` // toml | json-legacy | none
	Error    string   `json:"error,omitempty"`
	Warnings []string `json:"warnings"`
	Notice   string   `json:"notice,omitempty"` // for example a migration summary
}

type HostInfo struct {
	ID        string    `json:"id"`
	SSH       string    `json:"ssh"`
	Apps      int       `json:"apps"`
	Checked   bool      `json:"checked"`
	Reachable bool      `json:"reachable"`
	CheckedAt time.Time `json:"checkedAt"`
}

type AppInfo struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Host        string            `json:"host"`
	HostSSH     string            `json:"hostSsh"`
	Command     []string          `json:"command"`
	CommandText string            `json:"commandText"`
	Cwd         string            `json:"cwd,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Instances   string            `json:"instances"`
	Transport   string            `json:"transport"`
	Pinned      bool              `json:"pinned"`
	Icon        string            `json:"icon,omitempty"` // profile icon: a local PNG path
	Glyph       string            `json:"glyph"`
	Tile        string            `json:"tile"`
	StatusKind  Kind              `json:"statusKind"`
	StatusLabel string            `json:"statusLabel"`
	Live        int               `json:"live"`
	// Action is what the card's main button does: open, new-window or start-again.
	Action string `json:"action"`
}

type InstanceInfo struct {
	ID        string     `json:"id"`
	AppID     string     `json:"appId"`
	AppName   string     `json:"appName"`
	Glyph     string     `json:"glyph"`
	Tile      string     `json:"tile"`
	Icon      string     `json:"icon,omitempty"` // the application's profile icon, as in AppInfo
	HostID    string     `json:"hostId"`
	HostSSH   string     `json:"hostSsh"`
	Transport string     `json:"transport"`
	Command   string     `json:"command"`
	State     string     `json:"state"`
	Label     string     `json:"label"`
	Kind      Kind       `json:"kind"`
	Detail    string     `json:"detail"`
	ExitCode  *int       `json:"exitCode,omitempty"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
	Live      bool       `json:"live"`
	LogPath   string     `json:"logPath,omitempty"`
}

type CocoaInfo struct {
	Reachable bool      `json:"reachable"`
	Version   string    `json:"version,omitempty"`
	PID       int       `json:"pid,omitempty"`
	Socket    string    `json:"socket"`
	Protocol  int       `json:"protocol"`
	Display   string    `json:"display,omitempty"` // Wayland display from Runtime, when checked
	Error     string    `json:"error,omitempty"`
	CheckedAt time.Time `json:"checkedAt"`
}

// OpenResult tells the UI what an Open or Focus did.
type OpenResult struct {
	Action     string `json:"action"` // started | focused
	InstanceID string `json:"instanceId"`
	Note       string `json:"note,omitempty"`
}

// Problem is one validation message, attached to an editor field.
type Problem struct {
	Field   string `json:"field"` // id name host command cwd env instances general; env rows are "env.<n>"
	Message string `json:"message"`
}

type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// AppEdit is the editor's model of one application.
type AppEdit struct {
	IsNew     bool     `json:"isNew"`
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Host      string   `json:"host"`
	Command   []string `json:"command"`
	Cwd       string   `json:"cwd"`
	Env       []EnvVar `json:"env"`
	Instances string   `json:"instances"`
	Pinned    bool     `json:"pinned"`
	// Icon is the profile's icon path. The editor shows it and keeps it
	// unchanged on save (there is no field to change it yet).
	Icon string `json:"icon,omitempty"`
}

type Preview struct {
	Local  string `json:"local"`  // the process the launcher starts
	Remote string `json:"remote"` // what the remote login shell receives
}

type Check struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Kind   Kind   `json:"kind"`
	Result string `json:"result"`
}

// Failure is a connection problem with next steps, such as an untrusted host key.
type Failure struct {
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	Message    string `json:"message"`
	SSHCommand string `json:"sshCommand,omitempty"`
}

type HostReport struct {
	HostID  string    `json:"hostId"`
	SSH     string    `json:"ssh"`
	At      time.Time `json:"at"`
	Checks  []Check   `json:"checks"`
	Failure *Failure  `json:"failure,omitempty"`
	Cocoa   CocoaInfo `json:"cocoa"`
}
