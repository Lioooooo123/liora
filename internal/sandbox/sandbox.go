package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Lioooooo123/liora/internal/tools"
)

const defaultTimeout = 30 * time.Second
const maxShellOutputBytes = 512 * 1024

type Executor interface {
	Run(ctx context.Context, workspace string, command string) (tools.ShellResult, error)
}

type LocalExecutor struct{}

func (LocalExecutor) Run(ctx context.Context, workspace string, command string) (tools.ShellResult, error) {
	return runCommand(ctx, workspace, "sh", []string{"-c", command})
}

type DockerExecutor struct {
	Image         string
	Network       string
	Memory        string
	CPUs          string
	WorkspacePath string
	dockerPath    string
}

func FromEnv() Executor {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("LIORA_SANDBOX")), "docker") {
		return DockerExecutor{
			Image:         envDefault("LIORA_DOCKER_IMAGE", "golang:1.24-alpine"),
			Network:       envDefault("LIORA_DOCKER_NETWORK", "none"),
			Memory:        envDefault("LIORA_DOCKER_MEMORY", "1g"),
			CPUs:          envDefault("LIORA_DOCKER_CPUS", "2"),
			WorkspacePath: envDefault("LIORA_DOCKER_WORKSPACE", "/workspace"),
		}
	}
	return LocalExecutor{}
}

func Label(executor Executor) string {
	switch executor.(type) {
	case DockerExecutor:
		return "docker"
	case LocalExecutor:
		return "local"
	default:
		return "custom"
	}
}

func (e DockerExecutor) Run(ctx context.Context, workspace string, command string) (tools.ShellResult, error) {
	dockerPath := e.dockerPath
	if dockerPath == "" {
		var err error
		dockerPath, err = exec.LookPath("docker")
		if err != nil {
			return tools.ShellResult{ExitCode: -1}, errors.New("docker executable not found")
		}
	} else if _, err := os.Stat(dockerPath); err != nil {
		return tools.ShellResult{ExitCode: -1}, errors.New("docker executable not found")
	}
	return runCommand(ctx, "", dockerPath, e.runArgs(workspace, command))
}

func (e DockerExecutor) runArgs(workspace string, command string) []string {
	image := e.Image
	if image == "" {
		image = "golang:1.24-alpine"
	}
	network := e.Network
	if network == "" {
		network = "none"
	}
	memory := e.Memory
	if memory == "" {
		memory = "1g"
	}
	cpus := e.CPUs
	if cpus == "" {
		cpus = "2"
	}
	workspacePath := e.WorkspacePath
	if workspacePath == "" {
		workspacePath = "/workspace"
	}
	volume := filepath.Clean(workspace) + ":" + workspacePath
	return []string{
		"run", "--rm",
		"--network", network,
		"--memory", memory,
		"--cpus", cpus,
		"-v", volume,
		"-w", workspacePath,
		image,
		"sh", "-lc", command,
	}
}

func runCommand(parent context.Context, dir string, name string, args []string) (tools.ShellResult, error) {
	if strings.TrimSpace(name) == "" {
		return tools.ShellResult{}, errors.New("command is required")
	}
	ctx, cancel := context.WithTimeout(parent, defaultTimeout)
	defer cancel()
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	configureCommandProcessGroup(cmd)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return tools.ShellResult{ExitCode: -1}, err
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	var err error
	cancelled := false
	select {
	case err = <-done:
	case <-ctx.Done():
		cancelled = true
		killCommandProcessGroup(cmd)
		err = <-done
	}
	if cancelled {
		killCommandProcessGroup(cmd)
	}
	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	result := tools.ShellResult{
		Stdout:   truncateString(stdout.String(), maxShellOutputBytes),
		Stderr:   truncateString(stderr.String(), maxShellOutputBytes),
		ExitCode: exitCode,
	}
	if ctx.Err() == context.DeadlineExceeded {
		result.ExitCode = -1
		return result, fmt.Errorf("shell command timed out after %s", defaultTimeout)
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

func truncateString(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	if maxBytes < 20 {
		return value[:maxBytes]
	}
	return value[:maxBytes] + "\n[...truncated]\n"
}

func envDefault(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
