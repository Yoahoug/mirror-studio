package config

import (
	"os"
	"regexp"
	"strings"
)

var changelogSectionRE = regexp.MustCompile(`^##\s+\[?([0-9]+\.[0-9]+\.[0-9]+)\]?(?:\s*-\s*([0-9]{4}-[0-9]{2}-[0-9]{2}))?`)

// ParseChangelog reads the newest released section of a Keep-a-Changelog file.
// CHANGELOG.md is the single source of truth for the "更新说明" payload, so no
// release value is hardcoded anywhere else in the service.
func ParseChangelog(path, fallbackVersion string) ReleaseInfo {
	fallback := ReleaseInfo{
		Version:     "v" + strings.TrimPrefix(fallbackVersion, "v"),
		Title:       "绘镜 " + strings.TrimPrefix(fallbackVersion, "v"),
		Notes:       "",
		PublishedAt: "",
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return fallback
	}

	lines := strings.Split(string(content), "\n")
	start := -1
	info := ReleaseInfo{}
	for index, line := range lines {
		match := changelogSectionRE.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		start = index
		info.Version = "v" + match[1]
		info.Title = "绘镜 " + match[1]
		info.PublishedAt = match[2]
		break
	}
	if start < 0 {
		return fallback
	}

	notes := []string{}
	for _, line := range lines[start+1:] {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			break
		}
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			notes = append(notes, strings.TrimSpace(trimmed[2:]))
		}
	}
	info.Notes = strings.Join(notes, "\n")
	return info
}
