package novel

import (
	"archive/zip"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"golang.org/x/net/html"
)

// The reference implementation reads EPUBs with ebooklib, whose reader wraps the
// zip/OPF failures in errors with these exact texts; the task pipeline surfaces
// str(exc)[:255] to the user, so the strings are part of the contract.
const (
	badZipMessage     = "'Bad Zip file'"
	noContainerName   = "META-INF/container.xml"
	noContainerErrFmt = "There is no item named '%s' in the archive"
	containerFailMsg  = "Can not find container file"
	noBodyTextMessage = "EPUB 未提取到正文内容"
)

// extractEPUBText mirrors novelviz.services.extract_epub_text: it reads every
// manifest item of type application/xhtml+xml in OPF order and joins the text
// of the body of each document.
func extractEPUBText(filePath string) (string, error) {
	archive, err := zip.OpenReader(filePath)
	if err != nil {
		return "", errors.New(badZipMessage)
	}
	defer archive.Close()

	containerName := posixNormpath(noContainerName)
	containerData, ok := readZipEntry(archive, containerName)
	if !ok {
		return "", fmt.Errorf(noContainerErrFmt, containerName)
	}
	opfPath := findPackagePath(containerData)
	if opfPath == "" {
		return "", errors.New(containerFailMsg)
	}
	opfData, ok := readZipEntry(archive, posixNormpath(opfPath))
	if !ok {
		return "", errors.New(containerFailMsg)
	}
	items := manifestDocuments(opfData)
	opfDir := posixDirname(posixNormpath(opfPath))

	chunks := []string{}
	for _, item := range items {
		name := posixJoin(opfDir, posixNormpath(urlUnquoteLenient(item)))
		content, ok := readZipEntry(archive, name)
		if !ok {
			return "", fmt.Errorf(noContainerErrFmt, name)
		}
		text := normalizeDocumentText(content)
		if text != "" {
			chunks = append(chunks, text)
		}
	}
	fullText := strings.TrimSpace(strings.Join(chunks, "\n\n"))
	if fullText == "" {
		return "", errors.New(noBodyTextMessage)
	}
	return fullText, nil
}

func readZipEntry(archive *zip.ReadCloser, name string) ([]byte, bool) {
	for _, entry := range archive.File {
		if entry.Name == name {
			reader, err := entry.Open()
			if err != nil {
				return nil, false
			}
			defer reader.Close()
			data, err := io.ReadAll(reader)
			if err != nil {
				return nil, false
			}
			return data, true
		}
	}
	return nil, false
}

// findPackagePath returns the full-path of the first rootfile that declares the
// OPF media type, the same rootfile selection ebooklib performs.
func findPackagePath(containerXML []byte) string {
	decoder := xml.NewDecoder(strings.NewReader(string(containerXML)))
	for {
		token, err := decoder.Token()
		if err != nil {
			return ""
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "rootfile" {
			continue
		}
		fullPath := ""
		mediaType := ""
		for _, attr := range start.Attr {
			switch attr.Name.Local {
			case "full-path":
				fullPath = attr.Value
			case "media-type":
				mediaType = attr.Value
			}
		}
		if mediaType == "application/oebps-package+xml" {
			return fullPath
		}
	}
}

// manifestDocuments lists the hrefs of the manifest entries the reference treats
// as documents: exactly the application/xhtml+xml items, in manifest order.
func manifestDocuments(opfXML []byte) []string {
	decoder := xml.NewDecoder(strings.NewReader(string(opfXML)))
	inManifest := false
	hrefs := []string{}
	for {
		token, err := decoder.Token()
		if err != nil {
			return hrefs
		}
		switch typed := token.(type) {
		case xml.StartElement:
			if typed.Name.Local == "manifest" {
				inManifest = true
				continue
			}
			if !inManifest || typed.Name.Local != "item" {
				continue
			}
			href := ""
			mediaType := ""
			for _, attr := range typed.Attr {
				switch attr.Name.Local {
				case "href":
					href = attr.Value
				case "media-type":
					mediaType = attr.Value
				}
			}
			if mediaType == "application/xhtml+xml" && href != "" {
				hrefs = append(hrefs, href)
			}
		case xml.EndElement:
			if typed.Name.Local == "manifest" {
				inManifest = false
			}
		}
	}
}

// normalizeDocumentText reproduces the reference pipeline: the body children of
// the document (ebooklib's EpubHtml.get_content rebuilds a template and copies
// only the body), script/style/nav removed, text nodes joined with "\n" after
// per-node stripping, then runs of three or more newlines collapsed.
func normalizeDocumentText(content []byte) string {
	document, err := html.Parse(strings.NewReader(string(content)))
	if err != nil {
		return ""
	}
	body := findElement(document, "body")
	if body == nil {
		return ""
	}
	pieces := []string{}
	collectText(body, &pieces)
	text := strings.Join(pieces, "\n")
	return collapseNewlines(text)
}

func findElement(node *html.Node, name string) *html.Node {
	if node.Type == html.ElementNode && strings.EqualFold(node.Data, name) {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findElement(child, name); found != nil {
			return found
		}
	}
	return nil
}

func collectText(node *html.Node, pieces *[]string) {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		switch child.Type {
		case html.TextNode:
			if text := strings.TrimSpace(child.Data); text != "" {
				*pieces = append(*pieces, text)
			}
		case html.ElementNode:
			switch strings.ToLower(child.Data) {
			case "script", "style", "nav":
				continue
			}
			collectText(child, pieces)
		}
	}
}

func collapseNewlines(text string) string {
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}
	return text
}

// posixNormpath mirrors python's posixpath.normpath, which ebooklib applies to
// every archive path it reads.
func posixNormpath(value string) string {
	if value == "" {
		return "."
	}
	initialSlashes := 0
	for initialSlashes < len(value) && value[initialSlashes] == '/' {
		initialSlashes++
	}
	prefix := ""
	if initialSlashes > 0 {
		prefix = "/"
		if initialSlashes >= 2 {
			prefix = "//"
		}
	}
	parts := []string{}
	for _, part := range strings.Split(value, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(parts) > 0 && parts[len(parts)-1] != ".." {
				parts = parts[:len(parts)-1]
				continue
			}
			if prefix == "" {
				parts = append(parts, "..")
			}
		default:
			parts = append(parts, part)
		}
	}
	joined := strings.Join(parts, "/")
	if prefix != "" {
		return prefix + joined
	}
	if joined == "" {
		return "."
	}
	return joined
}

func posixDirname(value string) string {
	trimmed := strings.TrimRight(value, "/")
	if index := strings.LastIndex(trimmed, "/"); index >= 0 {
		return trimmed[:index]
	}
	return ""
}

func posixJoin(dir, name string) string {
	if dir == "" {
		return posixNormpath(name)
	}
	return posixNormpath(dir + "/" + name)
}

// urlUnquoteLenient mirrors urllib.parse.unquote: invalid escape sequences are
// left untouched instead of failing the whole path.
func urlUnquoteLenient(value string) string {
	if !strings.ContainsRune(value, '%') {
		return value
	}
	var builder strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] == '%' && index+2 < len(value) {
			high, okHigh := hexValue(value[index+1])
			low, okLow := hexValue(value[index+2])
			if okHigh && okLow {
				builder.WriteByte(high<<4 | low)
				index += 2
				continue
			}
		}
		builder.WriteByte(value[index])
	}
	return builder.String()
}

func hexValue(char byte) (byte, bool) {
	switch {
	case char >= '0' && char <= '9':
		return char - '0', true
	case char >= 'a' && char <= 'f':
		return char - 'a' + 10, true
	case char >= 'A' && char <= 'F':
		return char - 'A' + 10, true
	}
	return 0, false
}

// resolveBookPath mirrors services.resolve_book_path; its failures are the
// reference's ValueErrors.
func resolveBookPath(bookDir, filename string) (string, error) {
	cleanName := path.Base(strings.TrimSpace(filename))
	if !strings.HasSuffix(strings.ToLower(cleanName), ".epub") {
		return "", &ValueError{Message: "请选择 EPUB 小说文件"}
	}
	root, err := resolveAbsolute(bookDir)
	if err != nil {
		return "", err
	}
	target := posixNormpathAbs(root, cleanName)
	if target != root && !pathWithin(root, target) {
		return "", &ValueError{Message: "EPUB 文件路径不合法"}
	}
	if !isRegularFile(target) {
		return "", &ValueError{Message: "EPUB 文件不存在"}
	}
	return target, nil
}
