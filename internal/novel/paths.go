package novel

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// unsafeNameChars mirrors the reference sanitisation of an uploaded file name:
// runs of control characters, slashes and colons collapse to a single dash.
var unsafeNameChars = regexp.MustCompile(`[\x00-\x1f/\\:]+`)

// resolveAbsolute mirrors Path(value).resolve(): absolute and symlink-free when
// the path exists.
func resolveAbsolute(value string) (string, error) {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return resolved, nil
	}
	return filepath.Clean(absolute), nil
}

// posixNormpathAbs resolves base/name the way (base / name).resolve() does.
func posixNormpathAbs(base, name string) string {
	joined := filepath.Clean(filepath.Join(base, name))
	if resolved, err := filepath.EvalSymlinks(joined); err == nil {
		return resolved
	}
	return joined
}

// pathWithin reports whether root is a strict ancestor of target.
func pathWithin(root, target string) bool {
	return strings.HasPrefix(target, root+string(filepath.Separator))
}

func isRegularFile(value string) bool {
	info, err := os.Stat(value)
	return err == nil && info.Mode().IsRegular()
}

// cleanEPUBDisplayName mirrors services._clean_epub_display_name.
func cleanEPUBDisplayName(filename string) (string, error) {
	cleanName := filepath.Base(strings.TrimSpace(filename))
	cleanName = unsafeNameChars.ReplaceAllString(cleanName, "-")
	cleanName = strings.Trim(cleanName, " .")
	if cleanName == "" {
		cleanName = "novel.epub"
	}
	if !strings.HasSuffix(strings.ToLower(cleanName), ".epub") {
		return "", &ValueError{Message: "请上传 EPUB 小说文件"}
	}
	runes := []rune(cleanName)
	if len(runes) > 255 {
		cleanName = string(runes[:255])
	}
	return cleanName, nil
}

// uploadedTargetPath mirrors services._uploaded_epub_target_path.
func (s *Service) uploadedTargetPath(displayName string, userID int64) (string, string, error) {
	relativeDir := filepath.Join(s.settings.uploadDir, "users", fmt.Sprintf("%d", userID))
	random, err := randomHex(16)
	if err != nil {
		return "", "", err
	}
	relativePath := filepath.Join(relativeDir, random+"-"+displayName)
	mediaRoot, err := resolveAbsolute(s.cfg.MediaRoot)
	if err != nil {
		return "", "", err
	}
	absolute := posixNormpathAbs(mediaRoot, relativePath)
	if !pathWithin(mediaRoot, absolute) {
		return "", "", &ValueError{Message: "上传路径不合法"}
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		return "", "", err
	}
	return filepath.ToSlash(relativePath), absolute, nil
}

func randomHex(bytesCount int) (string, error) {
	buffer := make([]byte, bytesCount)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

// maxEPUBBytesError mirrors the reference size message.
func maxEPUBBytesError(maxBytes int64) error {
	return &ValueError{Message: fmt.Sprintf("EPUB 文件不能超过 %d MB", maxBytes/1024/1024)}
}
