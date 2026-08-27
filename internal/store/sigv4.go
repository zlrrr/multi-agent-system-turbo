// AWS Signature Version 4, implemented from the specification.
//
// There is no library here because every feature in this project has held
// go.mod unchanged, and an S3 SDK is the largest dependency tree it could take
// — for a signature that is HMAC-SHA256, SHA-256 and careful canonicalisation.
//
// What makes hand-rolling this responsible rather than reckless is the
// direction of the failure. We are the *client*: a signature computed wrongly
// is one the server rejects, so the failure mode is 403 SignatureDoesNotMatch
// rather than an unauthorised request that succeeds. And the specification
// ships test vectors, so "we implemented it" and "we implemented it correctly"
// are different claims and the second one is the one asserted
// (specs/010-object-run-store/design-hld.md §3).
package store

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	sigV4Algorithm = "AWS4-HMAC-SHA256"
	amzDateFormat  = "20060102T150405Z"
	shortDateFmt   = "20060102"
)

// Sign adds the Authorization and x-amz-* headers a request needs.
//
// payloadSHA256 is the hex SHA-256 of the body, which the caller already has
// because it also has to send it as x-amz-content-sha256.
func Sign(req *http.Request, payloadSHA256, accessKeyID, secret, region, service string, now time.Time) {
	now = now.UTC()
	amzDate := now.Format(amzDateFormat)
	shortDate := now.Format(shortDateFmt)

	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadSHA256)
	if req.Host != "" {
		req.Header.Set("Host", req.Host)
	} else if req.URL != nil {
		req.Header.Set("Host", req.URL.Host)
	}

	signed, canonicalHeaders := canonicalHeaders(req)
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL),
		canonicalQuery(req.URL),
		canonicalHeaders,
		signed,
		payloadSHA256,
	}, "\n")

	scope := strings.Join([]string{shortDate, region, service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		sigV4Algorithm,
		amzDate,
		scope,
		hexSHA256([]byte(canonicalRequest)),
	}, "\n")

	key := signingKey(secret, shortDate, region, service)
	signature := hex.EncodeToString(hmacSHA256(key, []byte(stringToSign)))

	req.Header.Set("Authorization", sigV4Algorithm+
		" Credential="+accessKeyID+"/"+scope+
		", SignedHeaders="+signed+
		", Signature="+signature)
}

// canonicalURI escapes the path per segment, leaving `/` alone. Escaping the
// whole path at once turns the separators into %2F and is one of the two
// mistakes the specification's test suite exists to catch.
func canonicalURI(u *url.URL) string {
	path := u.EscapedPath()
	if path == "" {
		return "/"
	}
	segments := strings.Split(path, "/")
	for i, s := range segments {
		unescaped, err := url.PathUnescape(s)
		if err != nil {
			unescaped = s
		}
		segments[i] = rfc3986Escape(unescaped)
	}
	return strings.Join(segments, "/")
}

// canonicalQuery sorts by name, then by value within a name, escaping both with
// RFC 3986 rules — `+` is %2B and a space is %20, which is where a naive
// url.Values.Encode() diverges.
func canonicalQuery(u *url.URL) string {
	if u.RawQuery == "" {
		return ""
	}
	values := u.Query()
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)

	var parts []string
	for _, name := range names {
		vs := append([]string(nil), values[name]...)
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, rfc3986Escape(name)+"="+rfc3986Escape(v))
		}
	}
	return strings.Join(parts, "&")
}

// canonicalHeaders lower-cases names, trims and collapses values, and returns
// the sorted signed-header list alongside the canonical block.
func canonicalHeaders(req *http.Request) (signed, canonical string) {
	names := make([]string, 0, len(req.Header)+1)
	values := map[string]string{}
	for name, vs := range req.Header {
		lower := strings.ToLower(name)
		joined := make([]string, 0, len(vs))
		for _, v := range vs {
			joined = append(joined, collapseSpaces(v))
		}
		names = append(names, lower)
		values[lower] = strings.Join(joined, ",")
	}
	if _, ok := values["host"]; !ok {
		host := req.Host
		if host == "" && req.URL != nil {
			host = req.URL.Host
		}
		names = append(names, "host")
		values["host"] = host
	}
	sort.Strings(names)

	var b strings.Builder
	for _, name := range names {
		b.WriteString(name)
		b.WriteByte(':')
		b.WriteString(values[name])
		b.WriteByte('\n')
	}
	return strings.Join(names, ";"), b.String()
}

// collapseSpaces trims a header value and reduces internal runs of spaces to
// one, which the specification requires for values outside quotes.
func collapseSpaces(v string) string {
	return strings.Join(strings.Fields(v), " ")
}

// rfc3986Escape percent-encodes everything except the unreserved set. Go's
// url.QueryEscape turns a space into `+`, which SigV4 rejects.
func rfc3986Escape(s string) string {
	const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.~"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if strings.IndexByte(unreserved, c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteString(strings.ToUpper(hex.EncodeToString([]byte{c})))
	}
	return b.String()
}

func signingKey(secret, shortDate, region, service string) []byte {
	k := hmacSHA256([]byte("AWS4"+secret), []byte(shortDate))
	k = hmacSHA256(k, []byte(region))
	k = hmacSHA256(k, []byte(service))
	return hmacSHA256(k, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}

func hexSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
