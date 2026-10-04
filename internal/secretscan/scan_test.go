package secretscan_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/federbenjamin/desk/internal/secretscan"
)

func TestBuiltinFindsEveryPublishedPatternWithoutReturningSecret(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		pattern string
	}{
		{"private key", "-----BEGIN EC PRIVATE KEY-----", "private-key"},
		{"AWS AKIA key", "AKIA1234567890ABCDEF", "aws-access-key"},
		{"AWS ASIA key", "ASIA1234567890ABCDEF", "aws-access-key"},
		{"GitHub classic token", "ghp_abcdefghijklmnopqrstuvwxyz0123456789", "github-token"},
		{"GitHub fine-grained token", "github_pat_abcdefghijklmnopqrstuv", "github-token"},
		{"Anthropic key", "sk-ant-abcdefghijklmnopqrst", "anthropic-key"},
		{"Slack key", "xoxb-1234567890", "slack-token"},
	}

	scan := secretscan.Builtin()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := scan(context.Background(), tt.text)
			if err != nil {
				t.Fatalf("Builtin()(%q) error = %v", tt.text, err)
			}
			if got != tt.pattern {
				t.Fatalf("Builtin()(%q) = %q; want pattern %q", tt.text, got, tt.pattern)
			}
			if got == tt.text || strings.Contains(got, tt.text) {
				t.Fatalf("Builtin() exposed matching text %q", got)
			}
		})
	}
}

func TestBuiltinReportsCleanTextAsEmptyPattern(t *testing.T) {
	t.Parallel()

	got, err := secretscan.Builtin()(context.Background(), "ordinary task notes only")
	if err != nil {
		t.Fatalf("Builtin()(clean text) error = %v", err)
	}
	if got != "" {
		t.Errorf("Builtin()(clean text) = %q; want empty", got)
	}
}

func TestCommandMapsExitCodesAndSendsTextOnStandardInput(t *testing.T) {
	for _, tt := range []struct {
		name      string
		exit      string
		text      string
		want      string
		wantErr   bool
		wantStdin string
	}{
		{"clean exit", "0", "plain text", "", false, "plain text"},
		{"hit exit", "1", "secret candidate", "command", false, "secret candidate"},
		{"scanner failure", "8", "text", "", true, "text"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DESK_SCAN_HELPER", "1")
			t.Setenv("DESK_SCAN_EXIT", tt.exit)
			t.Setenv("DESK_SCAN_STDIN", tt.wantStdin)

			scan := secretscan.Command([]string{os.Args[0], "-test.run=TestCommandHelperProcess", "--"})
			got, err := scan(context.Background(), tt.text)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Command exit %s error = %v; want error %t", tt.exit, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Command exit %s pattern = %q; want %q", tt.exit, got, tt.want)
			}
		})
	}
}

func TestCommandReportsAProgramThatCannotStart(t *testing.T) {
	t.Parallel()

	pattern, err := secretscan.Command(nil)(context.Background(), "text")
	if err == nil {
		t.Fatal("Command(empty argv) error = nil; want error")
	}
	if pattern != "" {
		t.Errorf("Command(empty argv) pattern = %q; want empty", pattern)
	}

	pattern, err = secretscan.Command([]string{"/definitely/not/a/desk-scanner"})(context.Background(), "text")
	if err == nil {
		t.Fatal("Command(nonexistent program) error = nil; want error")
	}
	if pattern != "" {
		t.Errorf("Command(nonexistent program) pattern = %q; want empty", pattern)
	}
}

func TestFromConfigUsesBuiltinOnlyForAnEmptyCommand(t *testing.T) {
	pattern, err := secretscan.FromConfig(nil)(context.Background(), "xoxp-1234567890")
	if err != nil || pattern != "slack-token" {
		t.Fatalf("FromConfig(nil) = %q, %v; want slack-token, nil", pattern, err)
	}

	t.Setenv("DESK_SCAN_HELPER", "1")
	t.Setenv("DESK_SCAN_EXIT", "0")
	t.Setenv("DESK_SCAN_STDIN", "xoxp-1234567890")
	pattern, err = secretscan.FromConfig([]string{os.Args[0], "-test.run=TestCommandHelperProcess", "--"})(context.Background(), "xoxp-1234567890")
	if err != nil || pattern != "" {
		t.Fatalf("FromConfig(command) = %q, %v; want empty, nil", pattern, err)
	}
}

func TestCommandHelperProcess(t *testing.T) {
	if os.Getenv("DESK_SCAN_HELPER") != "1" {
		return
	}
	text, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(9)
	}
	if string(text) != os.Getenv("DESK_SCAN_STDIN") {
		fmt.Fprintln(os.Stderr, "stdin did not contain the text to scan")
		os.Exit(9)
	}
	switch os.Getenv("DESK_SCAN_EXIT") {
	case "0":
		os.Exit(0)
	case "1":
		os.Exit(1)
	default:
		os.Exit(8)
	}
}
