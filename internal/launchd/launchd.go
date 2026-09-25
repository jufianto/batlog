// Package launchd writes the recorder's LaunchAgent plist and drives
// `launchctl` in the user's GUI domain (ADR-0004).
package launchd

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"howett.net/plist"
)

type agent struct {
	Label                string            `plist:"Label"`
	ProgramArguments     []string          `plist:"ProgramArguments"`
	RunAtLoad            bool              `plist:"RunAtLoad"`
	KeepAlive            bool              `plist:"KeepAlive"`
	ProcessType          string            `plist:"ProcessType"`
	StandardOutPath      string            `plist:"StandardOutPath"`
	StandardErrorPath    string            `plist:"StandardErrorPath"`
	EnvironmentVariables map[string]string `plist:"EnvironmentVariables,omitempty"`
}

// Plist renders the agent definition: run `<bin> daemon run` at login, keep
// it alive, low priority, stdout and stderr to the daemon log.
func Plist(label, bin, logPath string, env map[string]string) ([]byte, error) {
	if len(env) == 0 {
		env = nil
	}
	return plist.MarshalIndent(agent{
		Label:                label,
		ProgramArguments:     []string{bin, "daemon", "run"},
		RunAtLoad:            true,
		KeepAlive:            true,
		ProcessType:          "Background",
		StandardOutPath:      logPath,
		StandardErrorPath:    logPath,
		EnvironmentVariables: env,
	}, plist.XMLFormat, "\t")
}

// Program returns the binary an installed plist runs, so `status` can tell
// when a Homebrew upgrade has moved it.
func Program(data []byte) (string, error) {
	var a agent
	if _, err := plist.Unmarshal(data, &a); err != nil {
		return "", fmt.Errorf("read plist: %w", err)
	}
	if len(a.ProgramArguments) == 0 {
		return "", fmt.Errorf("read plist: no ProgramArguments")
	}
	return a.ProgramArguments[0], nil
}

// Env returns the environment an installed plist gives the recorder, so
// status and uninstall look where the daemon writes.
func Env(data []byte) (map[string]string, error) {
	var a agent
	if _, err := plist.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("read plist: %w", err)
	}
	return a.EnvironmentVariables, nil
}

// bootstrapAttempts covers launchd's habit of refusing a bootstrap with
// "5: Input/output error" while the previous instance is still going away.
const bootstrapAttempts = 3

// Client runs launchctl for one agent in the user's GUI domain. Every call
// takes a context so Ctrl-C stops an install between retries.
type Client struct {
	UID   int
	Label string
	Run   func(ctx context.Context, args ...string) ([]byte, error)
	Sleep func(ctx context.Context, d time.Duration) error
}

// New returns a client that runs the real launchctl.
func New(uid int, label string) Client {
	return Client{UID: uid, Label: label, Run: runLaunchctl, Sleep: sleep}
}

func runLaunchctl(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "launchctl", args...).CombinedOutput()
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func (c Client) domain() string  { return "gui/" + strconv.Itoa(c.UID) }
func (c Client) service() string { return c.domain() + "/" + c.Label }

// Loaded reports whether launchd knows the agent.
func (c Client) Loaded(ctx context.Context) bool {
	_, err := c.Run(ctx, "print", c.service())
	return err == nil
}

// Bootstrap loads the plist, retrying briefly. The error carries
// launchctl's own message, or the context's error if interrupted.
func (c Client) Bootstrap(ctx context.Context, plistPath string) error {
	var out []byte
	var err error
	for i := 0; i < bootstrapAttempts; i++ {
		if i > 0 {
			if err := c.Sleep(ctx, time.Second); err != nil {
				return err
			}
		}
		if out, err = c.Run(ctx, "bootstrap", c.domain(), plistPath); err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return fmt.Errorf("launchctl bootstrap: %s (%v)", strings.TrimSpace(string(out)), err)
}

// Bootout stops and unloads the agent.
func (c Client) Bootout(ctx context.Context) error {
	if out, err := c.Run(ctx, "bootout", c.service()); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("launchctl bootout: %s (%v)", strings.TrimSpace(string(out)), err)
	}
	return nil
}
