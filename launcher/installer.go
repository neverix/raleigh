package main

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jdx/go-netrc"
	"golang.org/x/mod/sumdb/dirhash"
	"golang.org/x/net/context"
)

type TpuConfig struct {
	repoPath         string
	remoteRepoPath   string
	zone             string
	project          string
	instanceType     string
	numTpus          int
	username         string
	installCommand   string
	tpuPrefix        string
	installerVersion string
	runCommand       string
}

type TpuInstaller struct {
	cfg              TpuConfig
	tpuController    *TpuController
	installerVersion string
	basicsInstalled  bool
	repoClonedHash   string
	repoCloned       bool
	runningPid       int
}

func NewTpuInstaller(cfg TpuConfig, id string) (*TpuInstaller, error) {
	installer := TpuInstaller{
		tpuController: &TpuController{
			project:      cfg.project,
			zone:         cfg.zone,
			instanceType: cfg.instanceType,
			id:           id,
		},
		cfg:              cfg,
		installerVersion: cfg.installerVersion,
	}
	_, status := installer.tpuController.checkStatus()
	if status == tpuStatusError {
		return nil, fmt.Errorf("error checking tpu status")
	}
	if status == tpuStatusRunning {
		basicsInstalled, err := installer.CheckBasicsInstalled()
		installer.basicsInstalled = basicsInstalled
		if err != nil {
			return nil, fmt.Errorf("error checking basics installed: %w", err)
		}

		installer.repoClonedHash, installer.repoCloned, err = installer.CheckRepoCloned()
		if err != nil {
			return nil, fmt.Errorf("error checking repo cloned: %w", err)
		}

		installer.runningPid, err = installer.CheckProcessRunning()
		if err != nil {
			return nil, fmt.Errorf("error checking process running: %w", err)
		}
	}
	return &installer, nil
}

type catError struct {
	code    int
	message string
}

func (e *catError) Error() string {
	return fmt.Sprintf("cat error: %d %s", e.code, e.message)
}

func (e *catError) IsNoFile() bool {
	return strings.Contains(e.message, "No such file or directory")
}

func (t *TpuController) ReadFile(user string, path string) (string, *catError) {
	cmd := t.ssh(user, "cat "+path)
	stderr := bytes.Buffer{}
	cmd.Stderr = &stderr
	stdout := bytes.Buffer{}
	cmd.Stdout = &stdout
	cmd.Run()
	if cmd.ProcessState.ExitCode() != 0 {
		return "", &catError{
			code:    cmd.ProcessState.ExitCode(),
			message: stderr.String(),
		}
	}
	text := stdout.String()
	text, _ = strings.CutSuffix(text, "\n")
	return text, nil
}

func (t *TpuInstaller) CheckBasicsInstalled() (bool, error) {
	version, err := t.tpuController.ReadFile(t.cfg.username, "~/.raleigh/install-version")
	if err != nil {
		if err.IsNoFile() {
			return false, nil
		}
		return false, fmt.Errorf("error checking basics installed: %w", err)
	}
	if version != t.installerVersion {
		return false, nil
	}
	return true, nil
}

func runCommand(t *TpuInstaller, command string) error {
	cmd := t.tpuController.ssh(t.cfg.username, command)
	stderr := bytes.Buffer{}
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("error running command: %w: %s", err, stderr.String())
	}
	return nil
}

func (t *TpuInstaller) InstallBasics() error {
	err := runCommand(t, "curl -LsSf https://astral.sh/uv/install.sh | sh")
	if err != nil {
		return err
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("error getting home directory: %w", err)
	}
	netrcPath := homeDir + "/.netrc"
	n, err := netrc.Parse(netrcPath)
	if err == nil {
		wandb := n.Machine("api.wandb.ai")
		key := wandb.Get("password")
		tmpNetrcPath, err := os.CreateTemp("", "netrc")
		if err != nil {
			return fmt.Errorf("error creating temp netrc: %w", err)
		}
		defer tmpNetrcPath.Close()
		_, err = tmpNetrcPath.WriteString(fmt.Sprintf("machine api.wandb.ai\n  login user\n  password %s", key))
		if err != nil {
			return fmt.Errorf("error writing netrc: %w", err)
		}
		err = t.tpuController.scp(tmpNetrcPath.Name(), "~/.netrc", t.cfg.username)
		if err != nil {
			return fmt.Errorf("error scping netrc: %w", err)
		}
		err = os.Remove(tmpNetrcPath.Name())
		if err != nil {
			return fmt.Errorf("error removing temp netrc: %w", err)
		}
	}

	err = runCommand(t, "mkdir -p ~/.raleigh && echo '"+t.installerVersion+"' > ~/.raleigh/install-version")
	if err != nil {
		return fmt.Errorf("error writing install version: %w", err)
	}

	return nil
}

func (t *TpuInstaller) LocalRepoHash() (string, error) {
	dirHash, err := dirhash.HashDir(t.cfg.repoPath, "", dirhash.DefaultHash)
	if err != nil {
		return "", fmt.Errorf("error hashing repo: %w", err)
	}
	return dirHash, nil
}

func (t *TpuInstaller) CheckRepoCloned() (string, bool, error) {
	dirHash, err := t.LocalRepoHash()
	if err != nil {
		return "", false, fmt.Errorf("error hashing repo: %w", err)
	}

	readHash, catErr := t.tpuController.ReadFile(t.cfg.username, "~/.raleigh/repo-version")
	if catErr != nil {
		if catErr.IsNoFile() {
			return "", false, nil
		}
		return "", false, fmt.Errorf("error checking repo cloned: %w", catErr)
	}
	return readHash, dirHash == readHash, nil
}

func (t *TpuInstaller) CloneRepo() error {
	err := t.tpuController.rsync(t.cfg.repoPath, t.cfg.remoteRepoPath, t.cfg.username)
	if err != nil {
		return fmt.Errorf("error cloning repo: %w", err)
	}

	err = runCommand(t, fmt.Sprintf("cd %s && %s", t.cfg.remoteRepoPath, t.cfg.installCommand))
	if err != nil {
		return fmt.Errorf("error syncing repo: %w", err)
	}

	dirHash, err := t.LocalRepoHash()
	if err != nil {
		return fmt.Errorf("error hashing repo: %w", err)
	}

	err = runCommand(t, "echo '"+dirHash+"' > ~/.raleigh/repo-version")
	if err != nil {
		return fmt.Errorf("error writing repo version: %w", err)
	}

	t.repoClonedHash = dirHash
	t.repoCloned = true

	return nil
}

func (t *TpuInstaller) CheckProcessRunning() (int, error) {
	pidFile := "~/.raleigh/running.pid"
	pid, catErr := t.tpuController.ReadFile(t.cfg.username, pidFile)
	if catErr != nil {
		if catErr.IsNoFile() {
			return -1, nil
		}
		return -1, fmt.Errorf("error reading pid file: %w", catErr)
	}
	pidInt, err := strconv.Atoi(pid)
	if err != nil {
		return -1, fmt.Errorf("error parsing pid: %w", err)
	}
	return pidInt, nil
}

func (t *TpuInstaller) KillRunningProcess() error {
	err := t.tpuController.killProcess(t.runningPid, 1*time.Second, context.Background())
	if err != nil {
		return fmt.Errorf("error killing process: %w", err)
	}
	err = runCommand(t, "rm -f ~/.raleigh/running.pid")
	if err != nil {
		return fmt.Errorf("error removing pid file: %s", err)
	}
	t.runningPid = -1
	return nil
}

func (t *TpuInstaller) StartProcess() error {
	// assumes that the process is not running
	// even if it is, tpu lockfile will be removed

	cmd := t.tpuController.ssh(t.cfg.username, fmt.Sprintf("cd %s && nohup %s > ~/.raleigh/nohup.log 2>&1 & echo $! > ~/.raleigh/running.pid", t.cfg.remoteRepoPath, t.cfg.runCommand))
	stderr := bytes.Buffer{}
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("error starting process: %s", stderr.String())
	}
	pid, err := t.CheckProcessRunning()
	if err != nil {
		return fmt.Errorf("error checking process running: %s", stderr.String())
	}
	if pid == -1 {
		return fmt.Errorf("process not running")
	}
	return nil
}
