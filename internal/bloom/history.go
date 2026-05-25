package bloom

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const historyTimestampLayout = "2006-01-02T15:04:05-0700"

type historyEntry struct {
	when time.Time
	seq  int
	line string
}

func (a *App) runHistory(args []string) int {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	limit := fs.Int("limit", 50, "maximum number of history entries to print")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(a.Err, "unexpected history argument: %s\n", fs.Arg(0))
		return 2
	}
	if *limit <= 0 {
		fmt.Fprintln(a.Err, "history limit must be greater than zero")
		return 2
	}

	entries := readHistoryEntries(*limit)
	if len(entries) == 0 {
		fmt.Fprintln(a.Out, "No Bloom history found yet. Run bm clean or bm uninstall first.")
		return 0
	}
	for _, entry := range entries {
		fmt.Fprintln(a.Out, entry.line)
	}
	return 0
}

func readHistoryEntries(limit int) []historyEntry {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	seq := 0
	entries := []historyEntry{}
	entries = append(entries, readHistoryLogFile(bloomLogFile(home, "clean.log"), parseCleanHistoryLine, &seq)...)
	entries = append(entries, readHistoryLogFile(bloomLogFile(home, "uninstall.log"), parseUninstallHistoryLine, &seq)...)
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].when.Equal(entries[j].when) {
			return entries[i].seq > entries[j].seq
		}
		return entries[i].when.After(entries[j].when)
	})
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries
}

func readHistoryLogFile(path string, parse func(string, int) (historyEntry, bool), seq *int) []historyEntry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	entries := []historyEntry{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		*seq = *seq + 1
		if entry, ok := parse(scanner.Text(), *seq); ok {
			entries = append(entries, entry)
		}
	}
	return entries
}

func parseCleanHistoryLine(line string, seq int) (historyEntry, bool) {
	parts := strings.SplitN(line, "\t", 5)
	if len(parts) != 5 {
		return historyEntry{}, false
	}
	when, ok := parseHistoryTimestamp(parts[0])
	if !ok {
		return historyEntry{}, false
	}
	action := cleanHistoryAction(parts[3])
	size := historySize(parts[2])
	fields := []string{formatHistoryTime(when), "clean", action}
	if size != "" {
		fields = append(fields, size)
	}
	fields = append(fields, parts[4])
	return historyEntry{when: when, seq: seq, line: strings.Join(fields, "  ")}, true
}

func parseUninstallHistoryLine(line string, seq int) (historyEntry, bool) {
	parts := strings.SplitN(line, "\t", 7)
	if len(parts) != 7 || parts[1] != "uninstall" {
		return historyEntry{}, false
	}
	when, ok := parseHistoryTimestamp(parts[0])
	if !ok {
		return historyEntry{}, false
	}
	appName := parts[2]
	if strings.TrimSpace(appName) == "" {
		appName = "unknown app"
	}
	event := parts[3]
	status := parts[4]
	size := historySize(parts[5])
	value := parts[6]

	fields := []string{formatHistoryTime(when), "uninstall", appName}
	switch event {
	case "command":
		if status == "ok" {
			fields = append(fields, "ran: "+value)
		} else {
			fields = append(fields, "failed command: "+value)
		}
	case "moved":
		fields = append(fields, "moved")
		if size != "" {
			fields = append(fields, size)
		}
		fields = append(fields, value)
	case "failed":
		fields = append(fields, "failed: "+value)
	default:
		fields = append(fields, event, status, value)
	}
	return historyEntry{when: when, seq: seq, line: strings.Join(fields, "  ")}, true
}

func parseHistoryTimestamp(value string) (time.Time, bool) {
	when, err := time.Parse(historyTimestampLayout, value)
	if err != nil {
		return time.Time{}, false
	}
	return when, true
}

func formatHistoryTime(when time.Time) string {
	return when.Format("2006-01-02 15:04")
}

func cleanHistoryAction(status string) string {
	switch status {
	case "ok":
		return "moved"
	case "dry-run":
		return "would move"
	case "error":
		return "failed"
	case "rejected":
		return "rejected"
	default:
		if status == "" {
			return "unknown"
		}
		return status
	}
}

func historySize(sizeKB string) string {
	sizeKB = strings.TrimSpace(sizeKB)
	if sizeKB == "" || sizeKB == "unknown" {
		return ""
	}
	n, err := strconv.ParseInt(sizeKB, 10, 64)
	if err != nil || n < 0 {
		return ""
	}
	return FormatBytes(n)
}

func logUninstallResult(res UninstallResult) {
	appName := res.App.Name
	if strings.TrimSpace(appName) == "" {
		appName = strings.TrimSuffix(filepath.Base(res.App.Path), ".app")
	}
	if res.BrewCask != "" {
		status := "error"
		if res.BrewRemoved {
			status = "ok"
		}
		logUninstallEvent(appName, "command", status, "0", brewCaskZapCommand(res.BrewCask))
	}
	for _, moved := range res.Moved {
		logUninstallEvent(appName, "moved", "ok", cleanLogSize(moved.SizeKB), moved.Path)
	}
	for _, failed := range res.Failed {
		logUninstallEvent(appName, "failed", "error", "unknown", failed)
	}
}

func logUninstallEvent(appName, event, status, sizeKB, value string) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return
	}
	logFile := bloomLogFile(home, "uninstall.log")
	if err := os.MkdirAll(filepath.Dir(logFile), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s\tuninstall\t%s\t%s\t%s\t%s\t%s\n",
		time.Now().Format(historyTimestampLayout),
		historyLogField(appName),
		historyLogField(event),
		historyLogField(status),
		historyLogField(sizeKB),
		historyLogField(value),
	)
}

func historyLogField(value string) string {
	replacer := strings.NewReplacer("\t", " ", "\n", " ", "\r", " ")
	return replacer.Replace(value)
}

func bloomLogFile(home, name string) string {
	return filepath.Join(bloomLogDir(home), name)
}

func bloomLogDir(home string) string {
	return filepath.Join(home, "Library", "Logs", "bloom")
}
