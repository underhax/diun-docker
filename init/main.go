// Package main implements a minimal static initialization binary that discovers
// Docker secret files with the "diun_" prefix and maps them into environment
// variables before executing the Diun binary.
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	defaultBinaryPath = "/usr/local/bin/diun"
	defaultCommand    = "serve"
	defaultSecretsDir = "/run/secrets"
	healthcheckCmd    = "healthcheck"
	rawFlag           = "--raw"
	secretPrefix      = "diun_"
)

var (
	errInvalidSecretKey = errors.New("invalid characters in secret environment key")
	errMockExec         = errors.New("simulated execution failure")
	errMockReadDir      = errors.New("simulated readdir failure")
	errMockReadFile     = errors.New("simulated readfile failure")
)

func defaultReadFile(path string) ([]byte, error) {
	cleanPath := filepath.Clean(path)
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("reading secret file %q: %w", cleanPath, err)
	}
	return data, nil
}

func defaultReadDir(dirname string) ([]fs.DirEntry, error) {
	cleanDir := filepath.Clean(dirname)
	entries, err := os.ReadDir(cleanDir)
	if err != nil {
		return nil, fmt.Errorf("reading secrets directory %q: %w", cleanDir, err)
	}
	return entries, nil
}

func defaultCreateCommand(ctx context.Context, args []string) *exec.Cmd {
	if len(args) > 0 && args[0] == healthcheckCmd {
		if len(args) > 1 && args[1] == rawFlag {
			return exec.CommandContext(ctx, defaultBinaryPath, healthcheckCmd, rawFlag)
		}
		return exec.CommandContext(ctx, defaultBinaryPath, healthcheckCmd)
	}
	return exec.CommandContext(ctx, defaultBinaryPath, defaultCommand)
}

func defaultExec(ctx context.Context, args, env []string) error {
	cmd := createCommand(ctx, args)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("running target binary: %w", err)
	}
	return nil
}

var (
	createCommand = defaultCreateCommand
	execProcess   = defaultExec
	exitFunc      = os.Exit
	readDir       = defaultReadDir
	readFile      = defaultReadFile
)

// PrepareEnvironment scans the specified secrets directory for files matching
// the application prefix and merges their contents into the base environment
// so container management tools do not expose sensitive credentials in container inspection.
func PrepareEnvironment(baseEnv []string, secretsDir string) ([]string, error) {
	entries, err := readDir(secretsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return baseEnv, nil
		}
		return nil, fmt.Errorf("scanning secrets directory: %w", err)
	}

	discovered := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		lower := strings.ToLower(name)
		if !strings.HasPrefix(lower, secretPrefix) || len(name) <= len(secretPrefix) {
			continue
		}

		key := strings.ToUpper(name)
		if strings.ContainsAny(key, "= \t\r\n") {
			return nil, fmt.Errorf("key %q: %w", key, errInvalidSecretKey)
		}

		secretPath := filepath.Join(secretsDir, name)
		data, readErr := readFile(secretPath)
		if readErr != nil {
			return nil, fmt.Errorf("loading secret %q: %w", name, readErr)
		}

		val := strings.TrimSpace(string(data))
		if val == "" {
			continue
		}
		discovered = append(discovered, fmt.Sprintf("%s=%s", key, val))
	}

	env := make([]string, 0, len(baseEnv)+len(discovered))
	env = append(env, baseEnv...)
	env = append(env, discovered...)

	return env, nil
}

// BuildCommandArguments constructs the execution argument vector, defaulting
// to the service daemon command when no explicit subcommands are provided.
func BuildCommandArguments(defaultCmd string, args []string) []string {
	if len(args) == 0 {
		return []string{defaultCmd}
	}

	if args[0] == defaultBinaryPath {
		if len(args) == 1 {
			return []string{defaultCmd}
		}
		res := make([]string, len(args)-1)
		copy(res, args[1:])
		return res
	}

	res := make([]string, len(args))
	copy(res, args)
	return res
}

// ExitCode extracts the process exit code from an execution error to ensure
// container health checks and parent process status monitors receive accurate termination signals.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode()
	}
	return 1
}

// Run executes the initialization workflow by discovering secrets, preparing
// the runtime environment, and launching the application binary.
func Run(ctx context.Context, args []string, secretsDir string) error {
	env, err := PrepareEnvironment(os.Environ(), secretsDir)
	if err != nil {
		return fmt.Errorf("preparing runtime environment: %w", err)
	}

	cmdArgs := BuildCommandArguments(defaultCommand, args)
	if execErr := execProcess(ctx, cmdArgs, env); execErr != nil {
		return fmt.Errorf("transferring execution to binary: %w", execErr)
	}

	return nil
}

func main() {
	var args []string
	if len(os.Args) > 1 {
		args = os.Args[1:]
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := Run(ctx, args, defaultSecretsDir); err != nil {
		code := ExitCode(err)
		if code != 0 {
			fmt.Fprintf(os.Stderr, "init error: %v\n", err)
		}
		exitFunc(code)
	}
}
