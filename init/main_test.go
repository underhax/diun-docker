package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

func mockFailingReadDir(_ string) ([]fs.DirEntry, error) {
	return nil, errMockReadDir
}

func mockFailingReadFile(_ string) ([]byte, error) {
	return nil, errMockReadFile
}

func mockSuccessfulExec(_ context.Context, _, _ []string) error {
	return nil
}

func mockFailingExec(_ context.Context, _, _ []string) error {
	return errMockExec
}

func mockSuccessfulCommand(ctx context.Context, _ []string) *exec.Cmd {
	return exec.CommandContext(ctx, "true")
}

func mockTerminatedExec(ctx context.Context, _, _ []string) error {
	return exec.CommandContext(ctx, "sh", "-c", "kill -TERM $$").Run()
}

var recordedExitCode = -1

func mockExitRecorder(code int) {
	recordedExitCode = code
}

func TestDefaultReadFile(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "sample.txt")
	expectedContent := []byte("secret_value")

	if err := os.WriteFile(filePath, expectedContent, 0o600); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	data, err := defaultReadFile(filePath)
	if err != nil {
		t.Fatalf("defaultReadFile() unexpected error: %v", err)
	}

	if !bytes.Equal(data, expectedContent) {
		t.Errorf("defaultReadFile() = %q, want %q", data, expectedContent)
	}

	if _, err := defaultReadFile(filepath.Join(tempDir, "nonexistent.txt")); err == nil {
		t.Error("defaultReadFile() expected error on nonexistent file, got nil")
	}
}

func TestDefaultReadDir(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "entry.txt")
	if err := os.WriteFile(filePath, []byte("ok"), 0o600); err != nil {
		t.Fatalf("writing entry file: %v", err)
	}

	entries, err := defaultReadDir(tempDir)
	if err != nil {
		t.Fatalf("defaultReadDir() unexpected error: %v", err)
	}

	if len(entries) != 1 || entries[0].Name() != "entry.txt" {
		t.Errorf("defaultReadDir() unexpected entries: %v", entries)
	}

	if _, err := defaultReadDir(filepath.Join(tempDir, "missing_subdir")); err == nil {
		t.Error("defaultReadDir() expected error on nonexistent directory, got nil")
	}
}

func TestDefaultCreateCommand(t *testing.T) {
	ctx := context.Background()

	t.Run("default serve invocation", func(t *testing.T) {
		cmd := defaultCreateCommand(ctx, nil)
		if cmd.Path != defaultBinaryPath && !slices.Contains(cmd.Args, defaultCommand) {
			t.Errorf("unexpected command: %v", cmd.Args)
		}
	})

	t.Run("healthcheck invocation", func(t *testing.T) {
		cmd := defaultCreateCommand(ctx, []string{healthcheckCmd})
		if !slices.Contains(cmd.Args, healthcheckCmd) {
			t.Errorf("unexpected command: %v", cmd.Args)
		}
	})

	t.Run("healthcheck raw invocation", func(t *testing.T) {
		cmd := defaultCreateCommand(ctx, []string{healthcheckCmd, rawFlag})
		if !slices.Contains(cmd.Args, rawFlag) {
			t.Errorf("unexpected command: %v", cmd.Args)
		}
	})

	t.Run("cancel command returns nil when process not started", func(t *testing.T) {
		cmd := defaultCreateCommand(ctx, nil)
		if cmd.Cancel == nil {
			t.Fatal("expected Cancel function to be configured")
		}
		if err := cmd.Cancel(); err != nil {
			t.Errorf("cmd.Cancel() unexpected error: %v", err)
		}
	})

	t.Run("cancel command signals running process", func(t *testing.T) {
		cmd := exec.CommandContext(ctx, "sleep", "10")
		cancelFn := cancelCommand(cmd)
		if err := cmd.Start(); err != nil {
			t.Fatalf("cmd.Start() unexpected error: %v", err)
		}
		defer func() {
			if waitErr := cmd.Wait(); waitErr != nil && !IsTermination(ctx, waitErr) {
				t.Errorf("cmd.Wait() unexpected error: %v", waitErr)
			}
		}()
		if err := cancelFn(); err != nil {
			t.Errorf("cancelFn() unexpected error: %v", err)
		}
	})
}

func TestDefaultExec(t *testing.T) {
	originalCreateCommand := createCommand
	defer func() {
		createCommand = originalCreateCommand
	}()

	t.Run("successful execution", func(t *testing.T) {
		createCommand = mockSuccessfulCommand
		if err := defaultExec(context.Background(), nil, nil); err != nil {
			t.Fatalf("defaultExec() unexpected error: %v", err)
		}
	})

	t.Run("missing binary execution failure", func(t *testing.T) {
		createCommand = defaultCreateCommand
		if err := defaultExec(context.Background(), []string{"serve"}, nil); err == nil {
			t.Error("defaultExec() expected error for missing default binary, got nil")
		}
	})
}

func TestPrepareEnvironment_Errors(t *testing.T) {
	baseEnv := []string{"SYSTEM_ENV=test"}

	t.Run("readDir error propagates", func(t *testing.T) {
		originalReadDir := readDir
		defer func() {
			readDir = originalReadDir
		}()
		readDir = mockFailingReadDir

		if _, err := PrepareEnvironment(baseEnv, "/any/dir"); err == nil {
			t.Error("PrepareEnvironment() expected error when readDir fails, got nil")
		}
	})

	t.Run("missing directory returns base environment", func(t *testing.T) {
		tempDir := t.TempDir()
		missingDir := filepath.Join(tempDir, "not_exist")

		gotEnv, err := PrepareEnvironment(baseEnv, missingDir)
		if err != nil {
			t.Fatalf("PrepareEnvironment() unexpected error for missing dir: %v", err)
		}

		if !reflect.DeepEqual(gotEnv, baseEnv) {
			t.Errorf("PrepareEnvironment() = %v, want %v", gotEnv, baseEnv)
		}
	})

	t.Run("readFile error propagates", func(t *testing.T) {
		tempDir := t.TempDir()
		secretFile := filepath.Join(tempDir, "diun_failing_key")
		if err := os.WriteFile(secretFile, []byte("val"), 0o600); err != nil {
			t.Fatalf("writing secret file: %v", err)
		}

		originalReadFile := readFile
		defer func() {
			readFile = originalReadFile
		}()
		readFile = mockFailingReadFile

		if _, err := PrepareEnvironment(baseEnv, tempDir); err == nil {
			t.Error("PrepareEnvironment() expected error when readFile fails, got nil")
		}
	})

	t.Run("invalid secret key returns error", func(t *testing.T) {
		tempDir := t.TempDir()
		invalidFile := filepath.Join(tempDir, "diun_invalid=key")
		if err := os.WriteFile(invalidFile, []byte("val"), 0o600); err != nil {
			t.Fatalf("writing invalid key file: %v", err)
		}

		if _, err := PrepareEnvironment(baseEnv, tempDir); err == nil {
			t.Error("PrepareEnvironment() expected error for invalid secret key with equals sign, got nil")
		}
	})
}

func TestPrepareEnvironment_Scanning(t *testing.T) {
	baseEnv := []string{"PREFIX=/usr"}
	tempDir := t.TempDir()

	setupFiles := []struct {
		name    string
		content string
		isDir   bool
	}{
		{name: "ignored_subfolder", isDir: true},
		{name: "not_a_secret.txt", content: "ignore_me"},
		{name: "diun_", content: "too_short_prefix"},
		{name: "diun_notif_ntfy_token", content: "   \n\t  "},
		{name: "diun_notif_ntfy_topic", content: "  my_secret_topic \n"},
		{name: "diun_notif_ntfy_endpoint", content: "https://ntfy.example.com\n"},
	}

	for _, item := range setupFiles {
		targetPath := filepath.Join(tempDir, item.name)
		if item.isDir {
			if err := os.Mkdir(targetPath, 0o700); err != nil {
				t.Fatalf("mkdir failed: %v", err)
			}
			continue
		}
		if err := os.WriteFile(targetPath, []byte(item.content), 0o600); err != nil {
			t.Fatalf("write file failed: %v", err)
		}
	}

	gotEnv, err := PrepareEnvironment(baseEnv, tempDir)
	if err != nil {
		t.Fatalf("PrepareEnvironment() unexpected error: %v", err)
	}

	expectedVars := []string{
		"PREFIX=/usr",
		"DIUN_NOTIF_NTFY_TOPIC=my_secret_topic",
		"DIUN_NOTIF_NTFY_ENDPOINT=https://ntfy.example.com",
	}

	if len(gotEnv) != len(expectedVars) {
		t.Errorf("PrepareEnvironment() returned %d variables, want %d: %v", len(gotEnv), len(expectedVars), gotEnv)
	}

	for _, expected := range expectedVars {
		if !slices.Contains(gotEnv, expected) {
			t.Errorf("PrepareEnvironment() missing expected variable %q in %v", expected, gotEnv)
		}
	}
}

func TestBuildCommandArguments(t *testing.T) {
	tests := []struct {
		name       string
		defaultCmd string
		args       []string
		want       []string
	}{
		{
			name:       "empty input produces default daemon command invocation",
			defaultCmd: defaultCommand,
			args:       nil,
			want:       []string{defaultCommand},
		},
		{
			name:       "custom command arguments are returned as subcommands",
			defaultCmd: defaultCommand,
			args:       []string{healthcheckCmd},
			want:       []string{healthcheckCmd},
		},
		{
			name:       "explicit binary argument is stripped and returns subcommands",
			defaultCmd: defaultCommand,
			args:       []string{defaultBinaryPath, "version"},
			want:       []string{"version"},
		},
		{
			name:       "explicit binary argument alone falls back to default command",
			defaultCmd: defaultCommand,
			args:       []string{defaultBinaryPath},
			want:       []string{defaultCommand},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := BuildCommandArguments(tc.defaultCmd, tc.args)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("BuildCommandArguments() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExitCode(t *testing.T) {
	tests := []struct {
		err  error
		name string
		want int
	}{
		{
			err:  nil,
			name: "nil error returns zero exit code",
			want: 0,
		},
		{
			err:  errors.New("generic execution failure"),
			name: "generic error returns exit code one",
			want: 1,
		},
		{
			err:  exec.CommandContext(context.Background(), "false").Run(),
			name: "exit error extracts subprocess status code",
			want: 1,
		},
		{
			err:  exec.CommandContext(context.Background(), "sh", "-c", "kill -TERM $$").Run(),
			name: "termination signal returns zero exit code",
			want: 0,
		},
		{
			err:  exec.CommandContext(context.Background(), "sh", "-c", "kill -INT $$").Run(),
			name: "interrupt signal returns zero exit code",
			want: 0,
		},
		{
			err:  exec.CommandContext(context.Background(), "sh", "-c", "kill -ABRT $$").Run(),
			name: "other signal returns 128 plus signal code",
			want: 134,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExitCode(tc.err); got != tc.want {
				t.Errorf("ExitCode() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestIsTermination(t *testing.T) {
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		ctx  context.Context
		err  error
		name string
		want bool
	}{
		{
			ctx:  context.Background(),
			err:  nil,
			name: "nil error with active context",
			want: false,
		},
		{
			ctx:  context.Background(),
			err:  errors.New("standard error"),
			name: "standard error with active context",
			want: false,
		},
		{
			ctx:  canceledCtx,
			err:  context.Canceled,
			name: "canceled context error",
			want: true,
		},
		{
			ctx:  context.Background(),
			err:  exec.CommandContext(context.Background(), "sh", "-c", "kill -TERM $$").Run(),
			name: "subprocess killed by sigterm",
			want: true,
		},
		{
			ctx:  context.Background(),
			err:  exec.CommandContext(context.Background(), "sh", "-c", "kill -INT $$").Run(),
			name: "subprocess killed by sigint",
			want: true,
		},
		{
			ctx:  context.Background(),
			err:  exec.CommandContext(context.Background(), "sh", "-c", "kill -KILL $$").Run(),
			name: "subprocess killed by sigkill",
			want: true,
		},
		{
			ctx:  context.Background(),
			err:  exec.CommandContext(context.Background(), "false").Run(),
			name: "subprocess non-signal failure",
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsTermination(tc.ctx, tc.err); got != tc.want {
				t.Errorf("IsTermination() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRun(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		readDirFn  func(string) ([]fs.DirEntry, error)
		execFn     func(context.Context, []string, []string) error
		name       string
		secretsDir string
		wantErr    bool
	}{
		{
			readDirFn:  defaultReadDir,
			execFn:     mockSuccessfulExec,
			name:       "successful execution passes arguments and environment",
			secretsDir: tempDir,
			wantErr:    false,
		},
		{
			readDirFn:  mockFailingReadDir,
			execFn:     mockSuccessfulExec,
			name:       "environment preparation error propagates failure",
			secretsDir: tempDir,
			wantErr:    true,
		},
		{
			readDirFn:  defaultReadDir,
			execFn:     mockFailingExec,
			name:       "process execution failure propagates error",
			secretsDir: tempDir,
			wantErr:    true,
		},
	}

	originalReadDir := readDir
	originalExec := execProcess
	defer func() {
		readDir = originalReadDir
		execProcess = originalExec
	}()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			readDir = tc.readDirFn
			execProcess = tc.execFn

			err := Run(context.Background(), []string{"serve"}, tc.secretsDir)
			if (err != nil) != tc.wantErr {
				t.Errorf("Run() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestMain_Success(t *testing.T) {
	originalExec := execProcess
	originalExit := exitFunc
	defer func() {
		execProcess = originalExec
		exitFunc = originalExit
	}()

	execProcess = mockSuccessfulExec
	recordedExitCode = 0
	exitFunc = mockExitRecorder

	main()

	if recordedExitCode != 0 {
		t.Errorf("main() expected exit code 0, got %d", recordedExitCode)
	}
}

func TestMain_Failure(t *testing.T) {
	originalReadDir := readDir
	originalExit := exitFunc
	defer func() {
		readDir = originalReadDir
		exitFunc = originalExit
	}()

	readDir = mockFailingReadDir
	recordedExitCode = -1
	exitFunc = mockExitRecorder

	main()

	if recordedExitCode != 1 {
		t.Errorf("main() expected exit code 1, got %d", recordedExitCode)
	}
}

func TestMain_Termination(t *testing.T) {
	originalExec := execProcess
	originalExit := exitFunc
	defer func() {
		execProcess = originalExec
		exitFunc = originalExit
	}()

	execProcess = mockTerminatedExec
	recordedExitCode = -1
	exitFunc = mockExitRecorder

	main()

	if recordedExitCode != 0 {
		t.Errorf("main() on termination expected exit code 0, got %d", recordedExitCode)
	}
}
