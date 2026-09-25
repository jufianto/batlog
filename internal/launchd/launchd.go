// Package launchd writes the recorder's LaunchAgent plist and drives
// `launchctl` in the user's GUI domain (ADR-0004).
package launchd

import (
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

// bootstrapAttempts covers launchd's habit of refusing a bootstrap with
// "5: Input/output error" while the previous instance is still going away.
const bootstrapAttempts = 3

// Client runs launchctl for one agent in the user's GUI domain.
type Client struct {
	UID   int
	Label string
	Run   func(args ...string) ([]byte, error) // defaults to launchctl
	Sleep func(time.Duration)                  // defaults to time.Sleep
}

// New returns a client that runs the real launchctl.
func New(uid int, label string) Client {
	return Client{UID: uid, Label: label, Run: runLaunchctl, Sleep: time.Sleep}
}

func runLaunchctl(args ...string) ([]byte, error) {
	return exec.Command("launchctl", args...).CombinedOutput()
}

func (c Client) domain() string  { return "gui/" + strconv.Itoa(c.UID) }
func (c Client) service() string { return c.domain() + "/" + c.Label }

// Loaded reports whether launchd knows the agent.
func (c Client) Loaded() bool {
	_, err := c.Run("print", c.service())
	return err == nil
}

// Bootstrap loads the plist, retrying briefly. The error carries
// launchctl's own message.
func (c Client) Bootstrap(plistPath string) error {
	var out []byte
	var err error
	for i := 0; i < bootstrapAttempts; i++ {
		if i > 0 {
			c.Sleep(time.Second)
		}
		if out, err = c.Run("bootstrap", c.domain(), plistPath); err == nil {
			return nil
		}
	}
	return fmt.Errorf("launchctl bootstrap: %s (%v)", strings.TrimSpace(string(out)), err)
}

// Bootout stops and unloads the agent.
func (c Client) Bootout() error {
	if out, err := c.Run("bootout", c.service()); err != nil {
		return fmt.Errorf("launchctl bootout: %s (%v)", strings.TrimSpace(string(out)), err)
	}
	return nil
}
