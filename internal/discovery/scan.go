package discovery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/keiko233/waylauncher/internal/execx"
	"github.com/keiko233/waylauncher/internal/remote"
)

// Options configure Scan and LoadIcons.
type Options struct {
	SSH     string        // ssh binary; default /usr/bin/ssh
	Target  string        // ssh target (host alias or user@host), as in a profile
	Exec    execx.Exec    // injectable; default execx.Run
	Timeout time.Duration // bound per ssh call; default 25s

	// Locales is the preferred-locale list for Name and Comment, such as
	// ["zh_CN", "zh"]; see LocalesFor. Empty means the plain Name.
	Locales []string
	// Desktops names the session for OnlyShowIn/NotShowIn. Empty means the
	// remote XDG_CURRENT_DESKTOP if the ssh environment has it, else a
	// generic session, which hides entries restricted by OnlyShowIn.
	Desktops []string
}

const (
	maxFileSize = 64 << 10

	markDesktop = "\x01WLD "
	markEnv     = "\x01WLENV "
	markEnd     = "\x01WLEND "
)

// scanScript is the read-only remote helper (POSIX sh). It writes nothing:
// no temp files, no redirections except to /dev/null. Limits: files over
// 64KB are skipped, at most 2000 files and 16MB in total. Output frames are
// length-prefixed so file content can never be confused with a delimiter:
//
//	\x01WLENV <XDG_CURRENT_DESKTOP>\n
//	\x01WLD <ok 0|1> <bytes> <desktop-id>\n<bytes of content>\n   (repeated)
//	\x01WLEND <count>\n
//
// ok is 0 for entries that shadow later ones but must not be listed
// (TryExec not executable, file too large); their content is empty. The
// first directory that has an ID wins: $XDG_DATA_HOME, then $XDG_DATA_DIRS,
// then flatpak and snap export directories.
const scanScript = `LC_ALL=C; export LC_ALL
set -f
home=${HOME:-}
xh=${XDG_DATA_HOME:-$home/.local/share}
xd=${XDG_DATA_DIRS:-/usr/local/share:/usr/share}
dirs=$xh:$xd:$home/.local/share/flatpak/exports/share:/var/lib/flatpak/exports/share:/var/lib/snapd/desktop
printf '\001WLENV %s\n' "$(printf %s "${XDG_CURRENT_DESKTOP:-}" | tr -d '\n')"
n=0; tot=0
seen='
'
oldifs=$IFS; IFS=:; set -- $dirs; IFS=$oldifs
for b in "$@"; do
  [ -n "$b" ] || continue
  d=$b/applications
  [ -d "$d" ] || continue
  list=$(find -L "$d" -name '*.desktop' -type f 2>/dev/null | sort)
  [ -n "$list" ] || continue
  while IFS= read -r f; do
    [ "$n" -lt 2000 ] || break 2
    rel=${f#"$d"/}
    case $rel in */*) id=$(printf %s "$rel" | tr / -);; *) id=$rel;; esac
    id=${id%.desktop}
    case $id in ''|*[!A-Za-z0-9._+@-]*) continue;; esac
    case $seen in *"
$id
"*) continue;; esac
    seen=$seen$id'
'
    c=$(head -c 65537 "$f" 2>/dev/null; echo x) || continue
    c=${c%x}
    size=${#c}
    flag=1
    if [ "$size" -gt 65536 ]; then flag=0; fi
    if [ "$flag" = 1 ]; then
      case $c in *TryExec=*)
        te=$(printf '%s\n' "$c" | sed -n 's/^TryExec=//p' | head -n 1)
        if [ -n "$te" ]; then
          case $te in
            -*) flag=0;;
            /*) [ -x "$te" ] || flag=0;;
            *) command -v "$te" >/dev/null 2>&1 || flag=0;;
          esac
        fi;;
      esac
    fi
    if [ "$flag" = 0 ]; then c=; size=0; fi
    tot=$((tot+size))
    [ "$tot" -le 16777216 ] || break 2
    printf '\001WLD %s %s %s\n' "$flag" "$size" "$id"
    printf '%s\n' "$c"
    n=$((n+1))
  done <<WLEOF
$list
WLEOF
done
printf '\001WLEND %s\n' "$n"
`

// rawFile is one framed desktop file from the scan stream.
type rawFile struct {
	ID      string
	OK      bool
	Content []byte
}

// parseScanStream decodes the scanScript output. Text outside frames (a login
// banner, say) is ignored. A truncated stream is an error: the result would
// silently miss applications.
func parseScanStream(out []byte) (files []rawFile, desktop string, err error) {
	complete := false
	for len(out) > 0 {
		i := bytes.IndexByte(out, 0x01)
		if i < 0 {
			break
		}
		out = out[i:]
		line, rest, _ := bytes.Cut(out, []byte("\n"))
		s := string(line)
		switch {
		case strings.HasPrefix(s, markEnv):
			desktop = strings.TrimSpace(s[len(markEnv):])
			out = rest
		case strings.HasPrefix(s, markEnd):
			complete = true
			out = rest
		case strings.HasPrefix(s, markDesktop):
			f := strings.SplitN(s[len(markDesktop):], " ", 3)
			if len(f) != 3 {
				return nil, "", errors.New("discovery: malformed frame header")
			}
			n, perr := strconv.Atoi(f[1])
			if perr != nil || n < 0 || n > maxFileSize {
				return nil, "", errors.New("discovery: bad frame length")
			}
			if len(rest) < n+1 || rest[n] != '\n' {
				return nil, "", errors.New("discovery: truncated scan output")
			}
			files = append(files, rawFile{ID: f[2], OK: f[0] == "1", Content: rest[:n]})
			out = rest[n+1:]
		default:
			out = out[1:]
		}
	}
	if !complete {
		return nil, "", errors.New("discovery: truncated scan output (no end marker)")
	}
	return files, desktop, nil
}

// buildEntries parses framed files into sorted entries. The remote side
// already sends only the first (highest precedence) file per ID; duplicates
// in the stream are dropped defensively, first wins.
func buildEntries(files []rawFile, opt Options, remoteDesktop string) []Entry {
	if len(opt.Desktops) == 0 && remoteDesktop != "" {
		opt.Desktops = strings.Split(remoteDesktop, ":")
	}
	seen := map[string]bool{}
	var entries []Entry
	for _, f := range files {
		if seen[f.ID] {
			continue
		}
		seen[f.ID] = true
		if !f.OK {
			continue
		}
		if e, ok := entryFrom(f.ID, parseDesktopFile(f.Content), opt); ok {
			entries = append(entries, e)
		}
	}
	assignProfileIDs(entries)
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := strings.ToLower(entries[i].Name), strings.ToLower(entries[j].Name)
		if a != b {
			return a < b
		}
		return entries[i].ID < entries[j].ID
	})
	return entries
}

// Scan lists the installable applications of the remote host with one
// read-only ssh call. Nothing is written remotely.
func Scan(ctx context.Context, opt Options) ([]Entry, error) {
	res, err := opt.ssh(ctx, "sh -c "+remote.Quote(scanScript))
	if err != nil {
		return nil, err
	}
	files, desktop, err := parseScanStream(res.Stdout)
	if err != nil {
		return nil, err
	}
	return buildEntries(files, opt, desktop), nil
}

// ssh runs one remote command with the launcher's standard non-interactive
// options. Host-key checking stays at the user's ssh configuration.
func (o Options) ssh(ctx context.Context, remoteCmd string) (execx.Result, error) {
	t := o.Target
	if t == "" || strings.HasPrefix(t, "-") {
		return execx.Result{}, fmt.Errorf("invalid ssh target %q", t)
	}
	sshBin, run, timeout := o.SSH, o.Exec, o.Timeout
	if sshBin == "" {
		sshBin = "/usr/bin/ssh"
	}
	if run == nil {
		run = execx.Run
	}
	if timeout == 0 {
		timeout = 25 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := append(append([]string{}, remote.SSHOptions...), t, remoteCmd)
	res, err := run(ctx, sshBin, args...)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return res, &remote.Error{Kind: remote.KindTimeout, Target: t}
		}
		return res, err
	}
	if res.ExitCode != 0 {
		detail := strings.TrimSpace(string(res.Stderr))
		return res, &remote.Error{Kind: remote.Classify(detail), Target: t, Detail: detail}
	}
	return res, nil
}
