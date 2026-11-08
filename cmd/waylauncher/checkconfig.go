package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/keiko233/waylauncher/internal/profile"
)

type migrationReport struct {
	Needed     bool   `json:"needed"`
	DryRun     bool   `json:"dryRun"`
	Applied    bool   `json:"applied"`
	Source     string `json:"source"`
	WriteTo    string `json:"writeTo"`
	BackupDir  string `json:"backupDir"`
	SourceKept bool   `json:"sourceKept"`
}

type checkReport struct {
	OK        bool              `json:"ok"`
	ConfigDir string            `json:"configDir"`
	Source    string            `json:"source,omitempty"`
	Migration *migrationReport  `json:"migration,omitempty"`
	Hosts     []profile.Host    `json:"hosts"`
	Apps      []appSummary      `json:"apps"`
	Settings  *profile.Settings `json:"settings,omitempty"`
	Warnings  []string          `json:"warnings"`
	Errors    []string          `json:"errors"`
}

type appSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Host      string `json:"host"`
	Instances string `json:"instances"`
	Transport string `json:"transport"`
}

func checkConfig(o options, stdout, stderr io.Writer) int {
	rep := checkReport{ConfigDir: o.paths.ConfigDir, Hosts: []profile.Host{}, Apps: []appSummary{}, Warnings: []string{}, Errors: []string{}}

	res, err := profile.Load(o.paths, o.dryRun)
	if res != nil {
		rep.Source = res.Source
		rep.Warnings = append(rep.Warnings, res.Warnings...)
		if res.File != nil {
			rep.Hosts = append(rep.Hosts, res.File.Hosts...)
			for _, a := range res.File.Apps {
				rep.Apps = append(rep.Apps, appSummary{ID: a.ID, Name: a.Name, Host: a.Host, Instances: a.Instances, Transport: a.Transport.MacOS})
			}
		}
		if m := res.Migration; m != nil {
			rep.Migration = &migrationReport{Needed: true, DryRun: o.dryRun, Applied: m.Applied, Source: m.SourcePath,
				WriteTo: m.TOMLPath, BackupDir: m.BackupPath, SourceKept: true}
		}
	}
	if err != nil {
		rep.Errors = append(rep.Errors, strings.Split(err.Error(), "\n")...)
	}
	if errors.Is(err, profile.ErrNoProfiles) {
		rep.Errors = []string{err.Error() + " in " + o.paths.ConfigDir}
	}
	settings, warnings, serr := profile.LoadSettings(o.paths)
	rep.Warnings = append(rep.Warnings, warnings...)
	if serr != nil {
		rep.Errors = append(rep.Errors, strings.Split(serr.Error(), "\n")...)
	} else {
		rep.Settings = &settings
	}
	rep.OK = len(rep.Errors) == 0

	if o.json {
		printJSON(stdout, rep)
	} else {
		printCheck(stdout, rep, res)
	}
	if !rep.OK {
		return 1
	}
	return 0
}

func printCheck(w io.Writer, r checkReport, res *profile.Result) {
	fmt.Fprintf(w, "config directory: %s\n", r.ConfigDir)
	switch {
	case r.Migration == nil && r.Source == "toml":
		fmt.Fprintln(w, "profiles: profiles.toml")
	case r.Migration != nil && r.Migration.DryRun:
		fmt.Fprintln(w, "profiles: legacy profiles.json (dry run: nothing written)")
		fmt.Fprintf(w, "migration would:\n  create    %s (mode 0600, never overwrites)\n  back up   %s -> %s/profiles.json\n  leave     %s untouched\n",
			r.Migration.WriteTo, r.Migration.Source, r.Migration.BackupDir, r.Migration.Source)
	case r.Migration != nil && r.Migration.Applied:
		fmt.Fprintf(w, "profiles: migrated profiles.json -> %s\n  backup: %s/profiles.json (original left in place)\n", r.Migration.WriteTo, r.Migration.BackupDir)
	}
	if res != nil && res.File != nil {
		fmt.Fprintf(w, "hosts: %d\n", len(r.Hosts))
		for _, h := range r.Hosts {
			fmt.Fprintf(w, "  %-24s %s\n", h.ID, h.SSH)
		}
		fmt.Fprintf(w, "apps: %d\n", len(r.Apps))
		for _, a := range r.Apps {
			fmt.Fprintf(w, "  %-12s %-20q host=%s instances=%s transport=%s\n", a.ID, a.Name, a.Host, a.Instances, a.Transport)
		}
	}
	if r.Settings != nil {
		fmt.Fprintln(w, "settings: ok")
	}
	for _, x := range r.Warnings {
		fmt.Fprintf(w, "warning: %s\n", x)
	}
	for _, x := range r.Errors {
		fmt.Fprintf(w, "error: %s\n", x)
	}
	if r.OK {
		fmt.Fprintln(w, "result: OK")
	} else {
		fmt.Fprintln(w, "result: INVALID")
	}
}
