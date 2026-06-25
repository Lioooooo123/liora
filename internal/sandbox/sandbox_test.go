package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalExecutorRunsInsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	result, err := LocalExecutor{}.Run(t.Context(), workspace, "pwd")
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != workspace {
		t.Fatalf("unexpected local result %#v", result)
	}
}

func TestDockerExecutorBuildsRestrictedRunCommand(t *testing.T) {
	workspace := t.TempDir()
	executor := DockerExecutor{
		Image:         "alpine:3.20",
		Network:       "none",
		Memory:        "512m",
		CPUs:          "1.5",
		WorkspacePath: "/workspace",
		dockerPath:    "docker",
	}

	args := executor.runArgs(workspace, "echo hello")
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"run", "--rm", "--network", "none", "--memory", "512m", "--cpus", "1.5",
		"-v", workspace + ":/workspace", "-w", "/workspace", "alpine:3.20", "sh", "-lc", "echo hello",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected docker args to contain %q, got %#v", want, args)
		}
	}
}

func TestExecutorFromEnvSelectsDocker(t *testing.T) {
	t.Setenv("LIORA_SANDBOX", "docker")
	t.Setenv("LIORA_DOCKER_IMAGE", "golang:1.24-alpine")
	executor := FromEnv()
	docker, ok := executor.(DockerExecutor)
	if !ok {
		t.Fatalf("expected docker executor, got %T", executor)
	}
	if docker.Image != "golang:1.24-alpine" {
		t.Fatalf("unexpected image %q", docker.Image)
	}
}

func TestDockerExecutorReportsMissingDocker(t *testing.T) {
	executor := DockerExecutor{Image: "alpine:3.20", dockerPath: "/definitely/missing/docker"}
	_, err := executor.Run(context.Background(), t.TempDir(), "echo hello")
	if err == nil || !strings.Contains(err.Error(), "docker executable not found") {
		t.Fatalf("expected missing docker error, got %v", err)
	}
}

func TestLocalExecutorCancelStopsChildProcesses(t *testing.T) {
	workspace := t.TempDir()
	marker := filepath.Join(workspace, "child-survived")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		_, err := LocalExecutor{}.Run(ctx, workspace, "(sleep 1; touch child-survived) & wait")
		done <- err
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled shell command did not return")
	}

	time.Sleep(1500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("cancelled shell command left a child process running")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
