package bloom

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	TaskOrder     []string
	Tasks         map[string]TaskConfig
	ProgressWidth int
	Color         bool
}

type TaskConfig struct {
	Enabled     bool
	Include     []string
	Exclude     []string
	InstallHint string
}

func DefaultConfig() Config {
	return Config{
		TaskOrder:     []string{"brew", "cask", "amp", "yazi", "nvim", "mason", "npm"},
		ProgressWidth: 24,
		Color:         true,
		Tasks: map[string]TaskConfig{
			"brew":    {Enabled: true},
			"cask":    {Enabled: true},
			"amp":     {Enabled: true},
			"yazi":    {Enabled: true},
			"nvim":    {Enabled: true},
			"mason":   {Enabled: true},
			"npm":     {Enabled: true},
			"cleanup": {Enabled: true},
		},
	}
}

func DefaultConfigPath() string {
	return configHome() + "/bloom/config.toml"
}

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		return cfg, nil
	}

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	defer file.Close()

	section := ""
	scanner := bufio.NewScanner(file)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := stripComment(scanner.Text())
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return cfg, fmt.Errorf("%s:%d: expected key = value", path, lineNo)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		switch {
		case section == "settings":
			if err := parseSetting(&cfg, key, value); err != nil {
				return cfg, fmt.Errorf("%s:%d: %w", path, lineNo, err)
			}
		case section == "tasks":
			if key != "order" {
				return cfg, fmt.Errorf("%s:%d: unsupported tasks key %q", path, lineNo, key)
			}
			order, err := parseStringArray(value)
			if err != nil {
				return cfg, fmt.Errorf("%s:%d: %w", path, lineNo, err)
			}
			cfg.TaskOrder = order
		case strings.HasPrefix(section, "tasks."):
			name := strings.TrimPrefix(section, "tasks.")
			task := cfg.Tasks[name]
			if err := parseTaskSetting(&task, key, value); err != nil {
				return cfg, fmt.Errorf("%s:%d: %w", path, lineNo, err)
			}
			cfg.Tasks[name] = task
		default:
			return cfg, fmt.Errorf("%s:%d: unsupported section [%s]", path, lineNo, section)
		}
	}
	if err := scanner.Err(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func WriteDefaultConfig(path string, force bool) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(DefaultConfigText()), 0o644)
}

func DefaultConfigText() string {
	return `# bm config

[settings]
progress_width = 24
color = true

[tasks]
order = ["brew", "cask", "amp", "yazi", "nvim", "mason", "npm"]

[tasks.brew]
enabled = true
include = []
exclude = []

[tasks.cask]
enabled = true
include = []
exclude = []

[tasks.amp]
enabled = true

[tasks.yazi]
enabled = true
include = []
exclude = []

[tasks.nvim]
enabled = true
include = []
exclude = []

[tasks.mason]
enabled = true
include = []
exclude = []

[tasks.npm]
enabled = true
include = []
exclude = []
`
}

func parseSetting(cfg *Config, key, value string) error {
	switch key {
	case "progress_width":
		width, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid progress_width")
		}
		if width < 8 || width > 80 {
			return fmt.Errorf("progress_width must be between 8 and 80")
		}
		cfg.ProgressWidth = width
	case "color":
		b, err := parseBool(value)
		if err != nil {
			return err
		}
		cfg.Color = b
	default:
		return fmt.Errorf("unsupported settings key %q", key)
	}
	return nil
}

func parseTaskSetting(task *TaskConfig, key, value string) error {
	switch key {
	case "enabled":
		b, err := parseBool(value)
		if err != nil {
			return err
		}
		task.Enabled = b
	case "include":
		items, err := parseStringArray(value)
		if err != nil {
			return err
		}
		task.Include = items
	case "exclude":
		items, err := parseStringArray(value)
		if err != nil {
			return err
		}
		task.Exclude = items
	case "install_hint":
		s, err := parseString(value)
		if err != nil {
			return err
		}
		task.InstallHint = s
	default:
		return fmt.Errorf("unsupported task key %q", key)
	}
	return nil
}

func stripComment(line string) string {
	inString := false
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inString {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if r == '#' && !inString {
			return line[:i]
		}
	}
	return line
}

func parseBool(value string) (bool, error) {
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("expected true or false")
	}
}

func parseString(value string) (string, error) {
	if len(value) < 2 || value[0] != '"' || value[len(value)-1] != '"' {
		return "", fmt.Errorf("expected quoted string")
	}
	unquoted, err := strconv.Unquote(value)
	if err != nil {
		return "", err
	}
	return unquoted, nil
}

func parseStringArray(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if len(value) < 2 || value[0] != '[' || value[len(value)-1] != ']' {
		return nil, fmt.Errorf("expected string array")
	}
	body := strings.TrimSpace(value[1 : len(value)-1])
	if body == "" {
		return nil, nil
	}

	var out []string
	for _, part := range splitArray(body) {
		item, err := parseString(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func splitArray(body string) []string {
	var parts []string
	start := 0
	inString := false
	escaped := false
	for i, r := range body {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inString {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if r == ',' && !inString {
			parts = append(parts, body[start:i])
			start = i + 1
		}
	}
	parts = append(parts, body[start:])
	return parts
}
