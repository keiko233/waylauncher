// Package execx is a tiny injectable wrapper around running a short-lived
// command, so other packages can be tested without real ssh or open.
package execx

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"
)

type Result struct {
	Stdout, Stderr []byte
	ExitCode       int
}

// Exec runs name with args to completion. A non-zero exit code is reported in
// Result, not as an error; the error is for start failures and context expiry.
type Exec func(ctx context.Context, name string, args ...string) (Result, error)

func Run(ctx context.Context, name string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.WaitDelay = time.Second // do not hang on inherited pipes after kill
	err := cmd.Run()
	res := Result{Stdout: out.Bytes(), Stderr: errb.Bytes()}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ctx.Err() == nil {
		res.ExitCode = ee.ExitCode()
		return res, nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		return res, err
	}
	return res, nil
}
