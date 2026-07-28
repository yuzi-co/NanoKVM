package tailscale

import (
	"NanoKVM-Server/utils"
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const (
	ScriptPath       = "/etc/init.d/S98tailscaled"
	ScriptBackupPath = "/kvmapp/system/init.d/S98tailscaled"

	// loginURLTimeout bounds how long the handler waits for the login URL.
	// The command itself runs for ten minutes waiting on the browser.
	loginURLTimeout = 60 * time.Second

	// waitDelay bounds how long Wait tolerates a still-open pipe after the
	// process itself has gone.
	waitDelay = 5 * time.Second
)

type Cli struct{}

type TsStatus struct {
	BackendState string `json:"BackendState"`

	Self struct {
		HostName     string   `json:"HostName"`
		TailscaleIPs []string `json:"TailscaleIPs"`
	} `json:"Self"`

	CurrentTailnet struct {
		Name string `json:"Name"`
	} `json:"CurrentTailnet"`
}

func NewCli() *Cli {
	return &Cli{}
}

func (c *Cli) Start() error {
	for _, filePath := range []string{TailscalePath, TailscaledPath} {
		if err := utils.EnsurePermission(filePath, 0o100); err != nil {
			return err
		}
	}

	commands := []string{
		fmt.Sprintf("cp -f %s %s", ScriptBackupPath, ScriptPath),
		fmt.Sprintf("%s start", ScriptPath),
	}

	command := strings.Join(commands, " && ")
	return exec.Command("sh", "-c", command).Run()
}

func (c *Cli) Restart() error {
	commands := []string{
		fmt.Sprintf("cp -f %s %s", ScriptBackupPath, ScriptPath),
		fmt.Sprintf("%s restart", ScriptPath),
	}

	command := strings.Join(commands, " && ")
	return exec.Command("sh", "-c", command).Run()
}

func (c *Cli) Stop() error {
	command := fmt.Sprintf("%s stop", ScriptPath)
	err := exec.Command("sh", "-c", command).Run()
	if err != nil {
		return err
	}

	return os.Remove(ScriptPath)
}

func (c *Cli) Up() error {
	command := "tailscale up --accept-dns=false"
	return exec.Command("sh", "-c", command).Run()
}

func (c *Cli) Down() error {
	command := "tailscale down"
	return exec.Command("sh", "-c", command).Run()
}

func (c *Cli) Status() (*TsStatus, error) {
	command := "tailscale status --json"
	cmd := exec.Command("sh", "-c", command)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, err
	}

	// output is not in standard json format
	if outputStr := string(output); !strings.HasPrefix(outputStr, "{") {
		index := strings.Index(outputStr, "{")
		if index == -1 {
			return nil, errors.New("unknown output")
		}

		output = []byte(outputStr[index:])
	}

	var status TsStatus
	err = json.Unmarshal(output, &status)
	if err != nil {
		return nil, err
	}

	return &status, nil
}

func (c *Cli) Login() (string, error) {
	// No shell: killing "sh -c tailscale ..." leaves tailscale holding the
	// stderr pipe, so the timeout below could never take effect.
	cmd := exec.Command("tailscale", "login", "--accept-dns=false", "--timeout=10m")

	return loginURL(cmd, loginURLTimeout)
}

var whitespace = regexp.MustCompile(`\s+`)

type loginResult struct {
	url string
	err error
}

// loginURL starts the login and returns the URL the user has to visit.
//
// The command keeps running afterwards, until the login is completed in the
// browser, so its output has to keep draining: closing the pipe early hands it
// a SIGPIPE on its next line and kills the login. It also has to be reaped
// rather than left as an orphan, once, on every path out of here.
func loginURL(cmd *exec.Cmd, timeout time.Duration) (string, error) {
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}

	// Safety net if the command ever leaves a child holding the pipe open.
	cmd.WaitDelay = waitDelay

	if err := cmd.Start(); err != nil {
		return "", err
	}

	// Buffered, so this goroutine still finishes if nobody is listening.
	results := make(chan loginResult, 1)

	go func() {
		url, err := readLoginURL(stderr)
		results <- loginResult{url: url, err: err}

		// Wait closes the pipe itself, so it must not be closed here.
		_, _ = io.Copy(io.Discard, stderr)
		_ = cmd.Wait()
	}()

	select {
	case result := <-results:
		return result.url, result.err

	case <-time.After(timeout):
		// Otherwise the handler blocks for the command's whole ten minutes.
		_ = cmd.Process.Kill()
		return "", fmt.Errorf("timed out waiting for the tailscale login url")
	}
}

func readLoginURL(reader io.Reader) (string, error) {
	buffered := bufio.NewReader(reader)

	for {
		line, err := buffered.ReadString('\n')
		if err != nil {
			return "", err
		}

		if strings.Contains(line, "https") {
			return whitespace.ReplaceAllString(line, ""), nil
		}
	}
}

func (c *Cli) Logout() error {
	command := "tailscale logout"
	return exec.Command("sh", "-c", command).Run()
}
