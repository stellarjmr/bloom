package bloom

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func hasRawLogControl(value string) bool {
	if !utf8.ValidString(value) {
		return true
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return true
		}
	}
	return false
}

func TestEscapeLogFieldSpellsEveryControlByte(t *testing.T) {
	for b := 1; b <= 255; b++ {
		for _, value := range []string{string([]byte{byte(b)}), "a" + string([]byte{byte(b)}) + "z"} {
			escaped := escapeLogField(value)
			if hasRawLogControl(escaped) {
				t.Fatalf("escapeLogField(%q) = %q still holds a raw control byte", value, escaped)
			}
			if escapeLogField(escaped) != escaped {
				t.Fatalf("escapeLogField is not idempotent for %q: %q", value, escaped)
			}
			if b >= 0x20 && b < 0x7f && escaped != value {
				t.Fatalf("printable ASCII %q changed to %q", value, escaped)
			}
		}
	}

	cases := map[string]string{
		"line\nbreak":      `line\nbreak`,
		"carriage\rreturn": `carriage\rreturn`,
		"tab\tseparated":   `tab\tseparated`,
		"\x1b[2Jclear":     `\x1b[2Jclear`,
		"bell\a":           `bell\x07`,
		"del\x7f":          `del\x7f`,
		"c1\u009b31m":      `c1\xc2\x9b31m`,
		"invalid\xff":      `invalid\xff`,
		`back\slash`:       `back\slash`,
		`literal\n`:        `literal\n`,
		"微信 Café � ok":     "微信 Café � ok",
	}
	for value, want := range cases {
		if got := escapeLogField(value); got != want {
			t.Fatalf("escapeLogField(%q) = %q, want %q", value, got, want)
		}
	}
}

func TestLogCleanOperationCannotForgeHistoryRecords(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	forged := filepath.Join(home, "Library", "Caches", "evil") +
		"\n2026-01-01T00:00:00+0000\ttrash\t99999999\tok\t" + filepath.Join(home, "Documents")
	logCleanOperation("trash", "0", "rejected", forged)

	data, err := os.ReadFile(bloomLogFile(home, "clean.log"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("one clean log call wrote %d records: %q", len(lines), data)
	}
	if fields := strings.Split(lines[0], "\t"); len(fields) != 5 || fields[3] != "rejected" {
		t.Fatalf("clean log record fields = %#v, want 5 fields with a rejected status", fields)
	}
	entries := readHistoryEntries(10)
	if len(entries) != 1 || !strings.Contains(entries[0].line, "rejected") || strings.Contains(entries[0].line, "moved") {
		t.Fatalf("history entries = %#v, want one rejected row", entries)
	}
}

func TestRunHistoryEscapesLegacyControlBytes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	logDir := bloomLogDir(home)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyClean := "2026-05-25T10:00:00+0100\ttrash\t12\tok\t/Users/me/Library/Caches/\x1b]0;owned\x07\x1b[2J\n"
	legacyUninstall := "2026-05-25T10:05:00+0100\tuninstall\tFoo\x1b[31m\tmoved\tok\t2048\t/Users/me/Library/Caches/\x1b[2Jcom.example\rfoo\n"
	if err := os.WriteFile(filepath.Join(logDir, "clean.log"), []byte(legacyClean), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "uninstall.log"), []byte(legacyUninstall), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	app := &App{Out: &out, Err: &errOut, Runner: pathRunner{}}
	if code := app.Run([]string{"history"}); code != 0 {
		t.Fatalf("history exit = %d, stderr=%q", code, errOut.String())
	}
	text := out.String()
	if hasRawLogControl(strings.ReplaceAll(text, "\n", "")) {
		t.Fatalf("history printed raw control bytes: %q", text)
	}
	for _, want := range []string{`\x1b]0;owned\x07\x1b[2J`, `Foo\x1b[31m`, `\x1b[2Jcom.example\rfoo`} {
		if !strings.Contains(text, want) {
			t.Fatalf("history output missing escaped %q: %q", want, text)
		}
	}
}

func TestUninstallHistoryKeepsBackslashesAndEscapesControls(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	value := filepath.Join(home, "Library", "Caches") + `/back\slash` + "\tname"
	logUninstallEvent("Foo\nBar", "moved", "ok", "1", value)

	data, err := os.ReadFile(bloomLogFile(home, "uninstall.log"))
	if err != nil {
		t.Fatal(err)
	}
	record := strings.TrimSuffix(string(data), "\n")
	if strings.Count(record, "\n") != 0 || len(strings.Split(record, "\t")) != 7 {
		t.Fatalf("uninstall log record = %q, want one 7-field row", record)
	}
	if !strings.Contains(record, `Foo\nBar`) || !strings.Contains(record, `/back\slash\tname`) {
		t.Fatalf("uninstall log record = %q, want escaped controls and a literal backslash", record)
	}
}
