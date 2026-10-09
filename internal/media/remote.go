package media

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// FetchRemoteImage downloads a provider result with the reference's SSRF guard:
// an exact-hostname allow list, an address check on every hop, manual redirects
// and a hard byte cap. It returns the bytes and the detected mime type.
func (s *Service) FetchRemoteImage(ctx context.Context, rawURL string) ([]byte, string, error) {
	current, err := s.validateRemoteImageURL(ctx, rawURL)
	if err != nil {
		return nil, "", err
	}
	client := &http.Client{
		Timeout:       remoteImageTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	parsed, err := url.Parse(current)
	if err != nil {
		return nil, "", errors.New("远程图片地址不合法")
	}
	for hop := 0; hop <= remoteRedirectLimit; hop++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			return nil, "", err
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, "", err
		}
		location := strings.TrimSpace(response.Header.Get("Location"))
		if isRedirectStatus(response.StatusCode) && location != "" {
			response.Body.Close()
			next, err := s.resolveRedirect(ctx, parsed, location)
			if err != nil {
				return nil, "", err
			}
			parsed = next
			continue
		}
		return readRemoteImageBody(response, s.cfg.MaxRemoteImageBytes)
	}
	return nil, "", errors.New("远程图片重定向次数过多")
}

// resolveRedirect validates a redirect target before it is followed.
func (s *Service) resolveRedirect(ctx context.Context, base *url.URL, location string) (*url.URL, error) {
	target, err := base.Parse(location)
	if err != nil {
		return nil, errors.New("远程图片重定向地址无效")
	}
	validated, err := s.validateRemoteImageURL(ctx, target.String())
	if err != nil {
		return nil, err
	}
	return url.Parse(validated)
}

func isRedirectStatus(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

// validateRemoteImageURL checks the scheme, the allow list and every resolved
// address, then returns the URL to request.
func (s *Service) validateRemoteImageURL(ctx context.Context, rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", errors.New("远程图片地址不合法")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", errors.New("远程图片地址不合法")
	}
	if parsed.Host == "" {
		return "", errors.New("远程图片地址不合法")
	}
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" {
		return "", errors.New("远程图片地址不合法")
	}
	if s.remoteAllowedHosts()[hostname] {
		return parsed.String(), nil
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, hostname)
	if err != nil || len(addresses) == 0 {
		return "", errors.New("远程图片地址解析失败")
	}
	for _, address := range addresses {
		if !isGlobalIP(address.IP) {
			return "", errors.New("远程图片地址禁止访问内网主机")
		}
	}
	return parsed.String(), nil
}

func (s *Service) remoteAllowedHosts() map[string]bool {
	allowed := map[string]bool{}
	for _, host := range s.cfg.RemoteImageAllowedHosts {
		trimmed := strings.ToLower(strings.TrimSpace(host))
		if trimmed != "" {
			allowed[trimmed] = true
		}
	}
	return allowed
}

// isGlobalIP reproduces the reference's ipaddress.is_global check for the
// ranges that can be reached from an upstream response.
func isGlobalIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ipv4 := ip.To4(); ipv4 != nil {
		switch {
		case ipv4[0] == 0:
			return false
		case ipv4[0] == 10:
			return false
		case ipv4[0] == 127:
			return false
		case ipv4[0] == 169 && ipv4[1] == 254:
			return false
		case ipv4[0] == 172 && ipv4[1] >= 16 && ipv4[1] <= 31:
			return false
		case ipv4[0] == 192 && ipv4[1] == 168:
			return false
		case ipv4[0] == 100 && ipv4[1] >= 64 && ipv4[1] <= 127:
			return false
		case ipv4[0] == 192 && ipv4[1] == 0 && (ipv4[2] == 0 || ipv4[2] == 2):
			return false
		case ipv4[0] == 198 && (ipv4[1] == 18 || ipv4[1] == 19):
			return false
		case ipv4[0] == 198 && ipv4[1] == 51 && ipv4[2] == 100:
			return false
		case ipv4[0] == 203 && ipv4[1] == 0 && ipv4[2] == 113:
			return false
		case ipv4[0] >= 224:
			return false
		}
		return true
	}
	if len(ip) != net.IPv6len {
		return false
	}
	switch {
	case ip.Equal(net.IPv6unspecified), ip.Equal(net.IPv6loopback):
		return false
	case ip[0]&0xfe == 0xfc:
		return false
	case ip[0] == 0xfe && ip[1]&0xc0 == 0x80:
		return false
	case ip[0] == 0xff:
		return false
	case ip[0] == 0x20 && ip[1] == 0x01 && ip[2] == 0x0d && ip[3] == 0xb8:
		return false
	// 2002::/16 (6to4): the last four bytes encode an arbitrary IPv4
	// destination, so an attacker can reach any v4 host through it.
	case ip[0] == 0x20 && ip[1] == 0x02:
		return false
	// 2001:0000::/24 through 2001:01ff::/24 (the first two octets of the /23
	// IETF protocol assignments Python's is_global rejects as non-global).
	// 2001:db8::/32 documentation is rejected above; Teredo 2001::/32 also
	// lands here.
	case ip[0] == 0x20 && ip[1] == 0x01 && ip[2] <= 0x01:
		return false
	// 100::/64: discard-only range.
	case ip[0] == 0x01 && ip[1] == 0x00 && ip[2] == 0x00 && ip[3] == 0x00:
		return false
	// 64:ff9b:1::/48: local-use NAT64.
	case ip[0] == 0x00 && ip[1] == 0x64 && ip[2] == 0xff && ip[3] == 0x9b && ip[4] == 0x00 && ip[5] == 0x01:
		return false
	// 3fff::/20: documentation range (RFC 9637).
	case ip[0] == 0x3f && ip[1] == 0xff:
		return false
	}
	return true
}

// readRemoteImageBody enforces the size cap and the image-content rule.
func readRemoteImageBody(response *http.Response, maxBytes int64) ([]byte, string, error) {
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return nil, "", errors.New("远程图片请求失败")
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.SplitN(response.Header.Get("Content-Type"), ";", 2)[0]))
	reader := io.Reader(response.Body)
	if maxBytes > 0 {
		reader = io.LimitReader(response.Body, maxBytes+1)
	}
	content, err := io.ReadAll(reader)
	if err != nil {
		return nil, "", err
	}
	if maxBytes > 0 && int64(len(content)) > maxBytes {
		return nil, "", errors.New("远程图片超过大小限制")
	}
	if sniffed := sniffImageMIME(content); sniffed != "" {
		contentType = sniffed
	}
	if !strings.HasPrefix(contentType, "image/") {
		return nil, "", errors.New("远程地址未返回图片内容")
	}
	if contentType == "" {
		contentType = "image/png"
	}
	return content, contentType, nil
}
