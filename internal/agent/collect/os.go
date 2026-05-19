package collect

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func collectArch(ctx context.Context) (string, error) {
	out, err := runCommand(ctx, "uname", "-m")
	if err != nil {
		return "", err
	}
	switch strings.TrimSpace(out) {
	case "x86_64", "amd64":
		return "x86_64", nil
	case "aarch64", "arm64":
		return "arm64", nil
	default:
		return strings.TrimSpace(out), nil
	}
}

func collectGlibcVersion(ctx context.Context) (string, error) {
	out, err := runCommand(ctx, "ldd", "--version")
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) == 0 {
		return "", fmt.Errorf("empty ldd output")
	}
	line := lines[0]
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", fmt.Errorf("invalid ldd output")
	}
	return fields[len(fields)-1], nil
}

func collectOS(ctx context.Context) (string, error) {
	file, err := os.Open("/etc/os-release")
	if err != nil {
		return "", err
	}
	defer file.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[key] = strings.Trim(value, `"`)
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	name := values["NAME"]
	version := values["VERSION_ID"]
	if version == "" {
		if v, ok := values["VERSION"]; ok {
			version = v
		}
	}
	if name == "" {
		return "", fmt.Errorf("failed to parse /etc/os-release")
	}
	if version == "" {
		return name, nil
	}
	if _, err := strconv.Atoi(version); err == nil {
		return name + " " + version, nil
	}
	return name + " " + version, nil
}

func collectSELinux(ctx context.Context) (string, error) {
	out, err := runCommand(ctx, "getenforce")
	if err != nil {
		return "disabled", nil
	}
	switch strings.ToLower(strings.TrimSpace(out)) {
	case "enforcing", "permissive":
		return "enabled", nil
	default:
		return "disabled", nil
	}
}

func collectFirewall(ctx context.Context) (string, error) {
	out, err := runCommand(ctx, "systemctl", "is-active", "firewalld")
	if err != nil {
		return "inactive", nil
	}
	if strings.TrimSpace(out) == "active" {
		return "active", nil
	}
	return "inactive", nil
}
