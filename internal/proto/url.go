package proto

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
)

// loopbackHostPort matches `<loopback>:<port>` in a string. Loopback host is
// `localhost`, `127.0.0.1`, or `[::1]`. Case-insensitive on `localhost`.
var loopbackHostPort = regexp.MustCompile(`(?i)(?:localhost|127\.0\.0\.1|\[::1\]):(\d{1,5})`)

// ExtractLoopbackPorts scans rawURL for any reference to a loopback host with
// an explicit port. It URL-decodes once before scanning, so percent-encoded
// `redirect_uri` parameters are matched. Returns deduplicated ports in
// ascending order. Out-of-range ports (>65535) are skipped.
func ExtractLoopbackPorts(rawURL string) []int {
	decoded, err := url.QueryUnescape(rawURL)
	if err != nil {
		decoded = rawURL
	}
	// Scan both decoded and raw — covers edge cases where decoding loses content.
	haystacks := []string{decoded}
	if decoded != rawURL {
		haystacks = append(haystacks, rawURL)
	}
	seen := map[int]struct{}{}
	for _, h := range haystacks {
		for _, m := range loopbackHostPort.FindAllStringSubmatch(h, -1) {
			p, err := strconv.Atoi(m[1])
			if err != nil || p < 1 || p > 65535 {
				continue
			}
			seen[p] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]int, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Ints(out)
	return out
}
