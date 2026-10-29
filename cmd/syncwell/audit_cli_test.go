package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const auditCLIArgsEnv = "SYNCWELL_TEST_AUDIT_CLI_ARGS"

func TestAuditCLISubprocessHelper(t *testing.T) {
	argText := os.Getenv(auditCLIArgsEnv)
	if argText == "" {
		return
	}
	flag.CommandLine = flag.NewFlagSet("syncwell", flag.ContinueOnError)
	os.Args = append([]string{"syncwell"}, strings.Fields(argText)...)
	main()
}

func TestOfflineBackupCommandsEmitAuditEvents(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	backupDir := filepath.Join(root, "backup")
	restoredDir := filepath.Join(root, "restored")
	auditPath := filepath.Join(root, "audit.jsonl")

	runCLI := func(args ...string) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestAuditCLISubprocessHelper$")
		cmd.Env = append(os.Environ(), auditCLIArgsEnv+"="+strings.Join(args, " "))
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("syncwell %s failed: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
	runCLI("-data", dataDir, "-backup", backupDir, "-backup-plaintext", "-audit-log", auditPath)
	runCLI("-verify-backup", backupDir, "-audit-log", auditPath)
	runCLI("-data", restoredDir, "-restore", backupDir, "-audit-log", auditPath)

	f, err := os.Open(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	wantActions := []string{"backup.create", "backup.verify", "backup.restore"}
	scanner := bufio.NewScanner(f)
	for i, wantAction := range wantActions {
		if !scanner.Scan() {
			t.Fatalf("missing CLI audit event %d (%s): %v", i, wantAction, scanner.Err())
		}
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatalf("invalid CLI audit event %q: %v", scanner.Text(), err)
		}
		if event["event"] != "audit" || event["action"] != wantAction || event["actor"] != "local-process" || event["result"] != "ok" {
			t.Fatalf("unexpected CLI audit event: %+v", event)
		}
	}
	if scanner.Scan() {
		t.Fatalf("unexpected extra CLI audit event: %s", scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}
