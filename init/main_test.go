package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

var (
	errMockClose   = errors.New("simulated close error")
	errMockDial    = errors.New("simulated dial error")
	errMockExec    = errors.New("simulated exec error")
	errMockOpen    = errors.New("simulated open error")
	errMockRead    = errors.New("simulated read error")
	errMockReadDir = errors.New("simulated readdir error")
	errMockScrub   = errors.New("simulated scrub error")
	errMockWrite   = errors.New("simulated write error")
)

var (
	recordedExitCode     = -1
	recordedExecArgs     []string
	recordedExecEnv      []string
	mockProcDirectory    string
	mockDialAttemptCount int
)

func mockExit(code int) {
	recordedExitCode = code
}

func mockFailingReadDir(_ string) ([]fs.DirEntry, error) {
	return nil, errMockReadDir
}

func mockFailingReadFile(_ string) ([]byte, error) {
	return nil, errMockRead
}

func mockPermissionReadFile(_ string) ([]byte, error) {
	return nil, fs.ErrPermission
}

func mockSuccessfulExec(_ context.Context, args, env []string) error {
	recordedExecArgs = args
	recordedExecEnv = env
	return nil
}

func mockFailingExec(_ context.Context, _, _ []string) error {
	return errMockExec
}

func mockNonExistentCommand(ctx context.Context, _ []string) *exec.Cmd {
	return exec.CommandContext(ctx, "/dev/null/non_existent_binary")
}

func mockTrueCommand(ctx context.Context, _ []string) *exec.Cmd {
	return exec.CommandContext(ctx, "true")
}

func mockFalseCommand(ctx context.Context, _ []string) *exec.Cmd {
	return exec.CommandContext(ctx, "false")
}

func mockFailingScrub(_ context.Context, _ int) error {
	return errMockScrub
}

type mockSocketDialer struct {
	err  error
	conn net.Conn
}

func (m *mockSocketDialer) DialContext(_ context.Context, _, _ string) (net.Conn, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.conn, nil
}

var activeMockDialer = &mockSocketDialer{}

func mockCreateDialer() socketDialer {
	return activeMockDialer
}

type mockSuccessConn struct {
	net.Conn
}

func (m *mockSuccessConn) Close() error {
	return nil
}

func mockSuccessDialTCP(_ context.Context, _, _ string) (net.Conn, error) {
	return &mockSuccessConn{}, nil
}

func mockFailingDialTCP(_ context.Context, _, _ string) (net.Conn, error) {
	return nil, errMockDial
}

type mockErrorCloserConn struct {
	net.Conn
}

func (m *mockErrorCloserConn) Close() error {
	return errMockClose
}

func mockErrorCloserDialTCP(_ context.Context, _, _ string) (net.Conn, error) {
	return &mockErrorCloserConn{}, nil
}

func mockRetrySuccessDialTCP(_ context.Context, _, _ string) (net.Conn, error) {
	mockDialAttemptCount++
	if mockDialAttemptCount == 1 {
		return nil, errMockDial
	}
	return &mockSuccessConn{}, nil
}

func mockProcPath(pid int, entry string) string {
	if mockProcDirectory != "" {
		return filepath.Join(mockProcDirectory, strconv.Itoa(pid), entry)
	}
	return defaultProcPath(pid, entry)
}

func mockFailingOpenFile(_ string, _ int, _ os.FileMode) (*os.File, error) {
	return nil, errMockOpen
}

func mockPermissionOpenFile(_ string, _ int, _ os.FileMode) (*os.File, error) {
	return nil, fs.ErrPermission
}

func mockFailingWriteMemory(_ *os.File, _ []byte, _ int64) (int, error) {
	return 0, errMockWrite
}

func mockPermissionWriteMemory(_ *os.File, _ []byte, _ int64) (int, error) {
	return 0, fs.ErrPermission
}

func mockFailingCloseFile(_ *os.File) error {
	return errMockClose
}

func mockPermissionCloseFile(_ *os.File) error {
	return fs.ErrPermission
}

func mockSuccessfulScrub(_ context.Context, _ int) error {
	return nil
}

func mockTerminatedExec(ctx context.Context, _, _ []string) error {
	return exec.CommandContext(ctx, "sh", "-c", "kill -TERM $$").Run()
}

func makeStatContent(comm, envStart, envEnd string, fieldCount int) string {
	var sb strings.Builder
	sb.WriteString("1234 (")
	sb.WriteString(comm)
	sb.WriteString(")")
	for i := range fieldCount {
		sb.WriteString(" ")
		switch i {
		case statEnvStartIndex:
			sb.WriteString(envStart)
		case statEnvEndIndex:
			sb.WriteString(envEnd)
		default:
			sb.WriteString("0")
		}
	}
	return sb.String()
}

func setupScrubCleanup(t *testing.T) {
	t.Helper()
	origProcPath := procPath
	origReadFile := readFile
	origOpenFile := openFile
	origWriteMem := writeMemory
	origCloseFile := closeFile
	origDialTCP := dialTCP

	dialTCP = mockSuccessDialTCP

	t.Cleanup(func() {
		procPath = origProcPath
		readFile = origReadFile
		openFile = origOpenFile
		writeMemory = origWriteMem
		closeFile = origCloseFile
		dialTCP = origDialTCP
		mockProcDirectory = ""
	})
}

func setupScrubChildFixture(t *testing.T) (pid int, statPath, memPath string) {
	t.Helper()
	tempDir := t.TempDir()
	pid = 4242
	pidDir := filepath.Join(tempDir, strconv.Itoa(pid))
	if err := os.Mkdir(pidDir, 0o750); err != nil {
		t.Fatalf("creating pid dir: %v", err)
	}

	statFilePath := filepath.Join(pidDir, "stat")
	memFilePath := filepath.Join(pidDir, "mem")

	validStatData := makeStatContent("diun", "10", "20", 49)
	if err := os.WriteFile(filepath.Clean(statFilePath), []byte(validStatData), 0o600); err != nil {
		t.Fatalf("writing stat file: %v", err)
	}

	initialMem := make([]byte, 30)
	for i := range initialMem {
		initialMem[i] = byte(i + 1)
	}
	if err := os.WriteFile(filepath.Clean(memFilePath), initialMem, 0o600); err != nil {
		t.Fatalf("writing mem file: %v", err)
	}

	mockProcDirectory = tempDir
	procPath = mockProcPath
	return pid, statFilePath, memFilePath
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

func TestDefaultOpenFile(t *testing.T) {
	tempDir := t.TempDir()
	testPath := filepath.Join(tempDir, "open_test.txt")

	f, err := defaultOpenFile(testPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("defaultOpenFile() failed to create: %v", err)
	}
	if closeErr := f.Close(); closeErr != nil {
		t.Fatalf("closing test file: %v", closeErr)
	}

	f, err = defaultOpenFile(testPath, os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("defaultOpenFile() failed to open existing: %v", err)
	}
	if closeErr := f.Close(); closeErr != nil {
		t.Fatalf("closing opened test file: %v", closeErr)
	}

	if _, err := defaultOpenFile(filepath.Join(tempDir, "not_found", "file"), os.O_RDONLY, 0); err == nil {
		t.Error("defaultOpenFile() expected error for nonexistent path")
	}
}

func TestDefaultCloseFile(t *testing.T) {
	tempDir := t.TempDir()
	testPath := filepath.Join(tempDir, "close_test.txt")

	f, err := os.Create(filepath.Clean(testPath))
	if err != nil {
		t.Fatalf("creating test file: %v", err)
	}

	if err := defaultCloseFile(f); err != nil {
		t.Fatalf("defaultCloseFile() unexpected error: %v", err)
	}

	if err := defaultCloseFile(f); err == nil {
		t.Error("defaultCloseFile() expected error on double close")
	}
}

func TestDefaultWriteMemory(t *testing.T) {
	tempDir := t.TempDir()
	testPath := filepath.Join(tempDir, "write_test.txt")

	f, err := os.OpenFile(filepath.Clean(testPath), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("creating test file: %v", err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			t.Errorf("closing test file: %v", closeErr)
		}
	}()

	payload := []byte("zeros")
	n, err := defaultWriteMemory(f, payload, 0)
	if err != nil {
		t.Fatalf("defaultWriteMemory() unexpected error: %v", err)
	}
	if n != len(payload) {
		t.Errorf("defaultWriteMemory() wrote %d bytes, want %d", n, len(payload))
	}

	roFile, err := os.OpenFile(filepath.Clean(testPath), os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("opening read-only file: %v", err)
	}
	defer func() {
		if closeErr := roFile.Close(); closeErr != nil {
			t.Errorf("closing read-only file: %v", closeErr)
		}
	}()

	if _, err := defaultWriteMemory(roFile, payload, 0); err == nil {
		t.Error("defaultWriteMemory() expected error on read-only file")
	}
}

func TestDefaultCreateDialer(t *testing.T) {
	dialer := defaultCreateDialer()
	if dialer == nil {
		t.Fatal("defaultCreateDialer() returned nil")
	}
}

func TestDefaultDialTCP(t *testing.T) {
	origDialer := createDialer
	createDialer = mockCreateDialer
	t.Cleanup(func() { createDialer = origDialer })

	activeMockDialer.err = errMockDial
	activeMockDialer.conn = nil
	if _, err := defaultDialTCP(context.Background(), "tcp", "127.0.0.1:42286"); err == nil {
		t.Fatal("expected dial error, got nil")
	}

	activeMockDialer.err = nil
	activeMockDialer.conn = &mockSuccessConn{}
	conn, err := defaultDialTCP(context.Background(), "tcp", "127.0.0.1:42286")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if conn == nil {
		t.Fatal("expected non-nil conn, got nil")
	}
}

func TestDefaultProcPath(t *testing.T) {
	got := defaultProcPath(42, "stat")
	want := filepath.Join(defaultProcDir, "42", "stat")
	if got != want {
		t.Errorf("defaultProcPath() = %q, want %q", got, want)
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

func TestParseProcStat(t *testing.T) {
	tests := []struct {
		statContent  string
		name         string
		wantEnvEnd   int64
		wantEnvStart int64
		expectErr    bool
	}{
		{
			statContent: "1234 diun 0 0 0",
			name:        "missing_closing_comm_paren",
			expectErr:   true,
		},
		{
			statContent: makeStatContent("diun", "100", "200", 20),
			name:        "insufficient_fields_after_comm",
			expectErr:   true,
		},
		{
			statContent: makeStatContent("diun", "not_a_number", "200", 49),
			name:        "invalid_env_start_non_number",
			expectErr:   true,
		},
		{
			statContent: makeStatContent("diun", "100", "not_a_number", 49),
			name:        "invalid_env_end_non_number",
			expectErr:   true,
		},
		{
			statContent: makeStatContent("diun", "0", "200", 49),
			name:        "negative_or_zero_env_start",
			expectErr:   true,
		},
		{
			statContent: makeStatContent("diun", "300", "200", 49),
			name:        "env_start_greater_than_env_end",
			expectErr:   true,
		},
		{
			statContent: makeStatContent("diun", "200", "200", 49),
			name:        "env_start_equal_to_env_end",
			expectErr:   true,
		},
		{
			statContent: makeStatContent("diun", "1000", strconv.FormatInt(1000+maxEnvRangeSize+1, 10), 49),
			name:        "env_range_exceeds_max_limit",
			expectErr:   true,
		},
		{
			statContent:  makeStatContent("diun (worker) proc", "1000", "2000", 49),
			name:         "valid_stat_with_nested_comm",
			wantEnvStart: 1000,
			wantEnvEnd:   2000,
			expectErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envStart, envEnd, err := parseProcStat(tt.statContent)
			if tt.expectErr {
				if err == nil {
					t.Fatalf("expected error for case %q, got nil", tt.name)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for case %q: %v", tt.name, err)
			}
			if envStart != tt.wantEnvStart || envEnd != tt.wantEnvEnd {
				t.Fatalf("expected (%d, %d), got (%d, %d)", tt.wantEnvStart, tt.wantEnvEnd, envStart, envEnd)
			}
		})
	}
}

func TestWaitForChildReadiness(t *testing.T) {
	origDial := dialTCP
	t.Cleanup(func() { dialTCP = origDial })

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForChildReadiness(canceledCtx, "127.0.0.1:42286", time.Second); err == nil {
		t.Fatal("expected error on canceled context, got nil")
	}

	dialTCP = mockSuccessDialTCP
	if err := waitForChildReadiness(context.Background(), "127.0.0.1:42286", time.Second); err != nil {
		t.Fatalf("unexpected error on ready socket: %v", err)
	}

	dialTCP = mockErrorCloserDialTCP
	if err := waitForChildReadiness(context.Background(), "127.0.0.1:42286", time.Second); err == nil {
		t.Fatal("expected error when socket close fails, got nil")
	}

	dialTCP = mockFailingDialTCP
	if err := waitForChildReadiness(context.Background(), "127.0.0.1:42286", 30*time.Millisecond); err != nil {
		t.Fatalf("expected nil on deadline fallback, got: %v", err)
	}

	ctxWait, cancelWait := context.WithCancel(context.Background())
	time.AfterFunc(2*time.Millisecond, cancelWait)
	if err := waitForChildReadiness(ctxWait, "127.0.0.1:42286", 2*time.Second); err == nil {
		t.Fatal("expected error when context canceled during wait, got nil")
	}

	mockDialAttemptCount = 0
	dialTCP = mockRetrySuccessDialTCP
	if err := waitForChildReadiness(context.Background(), "127.0.0.1:42286", 300*time.Millisecond); err != nil {
		t.Fatalf("unexpected error on retry: %v", err)
	}
}

func TestIsIgnorableMemoryError(t *testing.T) {
	tests := []struct {
		err  error
		name string
		want bool
	}{
		{
			err:  fs.ErrNotExist,
			name: "not exist is ignorable",
			want: true,
		},
		{
			err:  fs.ErrPermission,
			name: "permission is ignorable",
			want: true,
		},
		{
			err:  errors.New("other error"),
			name: "generic error is not ignorable",
			want: false,
		},
		{
			err:  nil,
			name: "nil error is not ignorable",
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isIgnorableMemoryError(tc.err); got != tc.want {
				t.Errorf("isIgnorableMemoryError() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDefaultScrubChildMemory_ReadinessErrors(t *testing.T) {
	setupScrubCleanup(t)
	pid, _, _ := setupScrubChildFixture(t)

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := defaultScrubChildMemory(canceledCtx, pid); err != nil {
		t.Fatalf("expected nil when readiness context canceled, got %v", err)
	}

	dialTCP = mockErrorCloserDialTCP
	if err := defaultScrubChildMemory(context.Background(), pid); err == nil {
		t.Fatal("expected error on readiness failure, got nil")
	}
}

func TestDefaultScrubChildMemory_ReadAndParseErrors(t *testing.T) {
	setupScrubCleanup(t)
	pid, statFilePath, _ := setupScrubChildFixture(t)

	if err := defaultScrubChildMemory(context.Background(), 9999); err != nil {
		t.Fatalf("expected nil when stat file missing, got %v", err)
	}

	readFile = mockPermissionReadFile
	if err := defaultScrubChildMemory(context.Background(), pid); err != nil {
		t.Fatalf("expected nil on stat read permission error, got %v", err)
	}

	readFile = mockFailingReadFile
	if err := defaultScrubChildMemory(context.Background(), pid); err == nil {
		t.Fatal("expected error on generic stat read error, got nil")
	}
	readFile = defaultReadFile

	if err := os.WriteFile(filepath.Clean(statFilePath), []byte("corrupt stat data"), 0o600); err != nil {
		t.Fatalf("overwriting stat: %v", err)
	}
	if err := defaultScrubChildMemory(context.Background(), pid); err == nil {
		t.Fatal("expected error on corrupt stat data, got nil")
	}
}

func TestDefaultScrubChildMemory_OpenErrors(t *testing.T) {
	setupScrubCleanup(t)
	pid, _, memFilePath := setupScrubChildFixture(t)

	if err := os.Remove(filepath.Clean(memFilePath)); err != nil {
		t.Fatalf("removing mem file: %v", err)
	}
	if err := defaultScrubChildMemory(context.Background(), pid); err != nil {
		t.Fatalf("expected nil when mem file missing, got %v", err)
	}

	openFile = mockPermissionOpenFile
	if err := defaultScrubChildMemory(context.Background(), pid); err != nil {
		t.Fatalf("expected nil on mem open permission error, got %v", err)
	}

	openFile = mockFailingOpenFile
	if err := defaultScrubChildMemory(context.Background(), pid); err == nil {
		t.Fatal("expected error on generic mem open error, got nil")
	}
}

func TestDefaultScrubChildMemory_WriteErrors(t *testing.T) {
	setupScrubCleanup(t)
	pid, _, _ := setupScrubChildFixture(t)

	writeMemory = mockPermissionWriteMemory
	if err := defaultScrubChildMemory(context.Background(), pid); err != nil {
		t.Fatalf("expected nil on mem write permission error, got %v", err)
	}

	writeMemory = mockFailingWriteMemory
	if err := defaultScrubChildMemory(context.Background(), pid); err == nil {
		t.Fatal("expected error on generic mem write error, got nil")
	}
}

func TestDefaultScrubChildMemory_CloseErrors(t *testing.T) {
	setupScrubCleanup(t)
	pid, _, _ := setupScrubChildFixture(t)

	closeFile = mockPermissionCloseFile
	if err := defaultScrubChildMemory(context.Background(), pid); err != nil {
		t.Fatalf("expected nil on mem close permission error, got %v", err)
	}

	closeFile = mockFailingCloseFile
	if err := defaultScrubChildMemory(context.Background(), pid); err == nil {
		t.Fatal("expected error on generic mem close error, got nil")
	}
}

func TestDefaultScrubChildMemory_Success(t *testing.T) {
	setupScrubCleanup(t)
	pid, _, memFilePath := setupScrubChildFixture(t)

	if err := defaultScrubChildMemory(context.Background(), pid); err != nil {
		t.Fatalf("unexpected error on successful scrub: %v", err)
	}

	scrubbedData, err := os.ReadFile(filepath.Clean(memFilePath))
	if err != nil {
		t.Fatalf("reading scrubbed mem file: %v", err)
	}
	for i := range 10 {
		if scrubbedData[i] != byte(i+1) {
			t.Fatalf("expected untouched byte at %d, got %d", i, scrubbedData[i])
		}
	}
	for i := 10; i < 20; i++ {
		if scrubbedData[i] != 0 {
			t.Fatalf("expected zeroed byte at %d, got %d", i, scrubbedData[i])
		}
	}
	for i := 20; i < 30; i++ {
		if scrubbedData[i] != byte(i+1) {
			t.Fatalf("expected untouched byte at %d, got %d", i, scrubbedData[i])
		}
	}
}

func TestDefaultExec(t *testing.T) {
	originalCreateCommand := createCommand
	originalScrub := scrubChildMemory
	defer func() {
		createCommand = originalCreateCommand
		scrubChildMemory = originalScrub
	}()

	t.Run("successful execution", func(t *testing.T) {
		createCommand = mockTrueCommand
		scrubChildMemory = mockSuccessfulScrub
		env := []string{"TEST_SECRET=value"}
		if err := defaultExec(context.Background(), nil, env); err != nil {
			t.Fatalf("defaultExec() unexpected error: %v", err)
		}
	})

	t.Run("scrub error logged but execution succeeds", func(t *testing.T) {
		createCommand = mockTrueCommand
		scrubChildMemory = mockFailingScrub
		env := []string{"TEST_SECRET=value"}
		if err := defaultExec(context.Background(), nil, env); err != nil {
			t.Fatalf("defaultExec() unexpected error: %v", err)
		}
	})

	t.Run("missing binary execution failure", func(t *testing.T) {
		createCommand = mockNonExistentCommand
		scrubChildMemory = mockSuccessfulScrub
		if err := defaultExec(context.Background(), nil, nil); err == nil {
			t.Error("defaultExec() expected error for non-existent binary, got nil")
		}
	})

	t.Run("subprocess non-zero failure", func(t *testing.T) {
		createCommand = mockFalseCommand
		scrubChildMemory = mockSuccessfulScrub
		if err := defaultExec(context.Background(), nil, nil); err == nil {
			t.Error("defaultExec() expected error for false command, got nil")
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
	exitFunc = mockExit

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
	exitFunc = mockExit

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
	exitFunc = mockExit

	main()

	if recordedExitCode != 0 {
		t.Errorf("main() on termination expected exit code 0, got %d", recordedExitCode)
	}
}
