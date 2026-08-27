package store

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// The credentials and instant from AWS's published SigV4 examples for S3. They
// are not secrets: they are the fixed inputs the specification's worked
// examples use, which is what makes the expected signatures checkable.
const (
	vectorKeyID  = "AKIAIOSFODNN7EXAMPLE"
	vectorSecret = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	vectorRegion = "us-east-1"
)

var vectorTime = time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)

// TestSigV4MatchesPublishedVectors is FR-002 and CON-005.
//
// Four independent signatures over four different shapes of request: a plain
// GET with an extra signed header, a PUT with a body and a vendor header, a
// query string with no value, and a query string with two parameters that have
// to be sorted. Those are exactly the cases where implementations diverge, and
// four hex digests cannot agree by accident.
func TestSigV4MatchesPublishedVectors(t *testing.T) {
	for _, c := range []struct {
		name      string
		method    string
		url       string
		headers   map[string]string
		payload   string
		wantSig   string
		wantSHead string
	}{
		{
			name:   "GET Object with a Range header",
			method: http.MethodGet,
			url:    "https://examplebucket.s3.amazonaws.com/test.txt",
			headers: map[string]string{
				"Range": "bytes=0-9",
			},
			payload:   "",
			wantSig:   "f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41",
			wantSHead: "host;range;x-amz-content-sha256;x-amz-date",
		},
		{
			name:   "PUT Object with a body and a storage class",
			method: http.MethodPut,
			url:    "https://examplebucket.s3.amazonaws.com/test%24file.text",
			headers: map[string]string{
				"Date":                "Fri, 24 May 2013 00:00:00 GMT",
				"X-Amz-Storage-Class": "REDUCED_REDUNDANCY",
			},
			payload:   "Welcome to Amazon S3.",
			wantSig:   "98ad721746da40c64f1a55b78f14c238d841ea1380cd77a1b5971af0ece108bd",
			wantSHead: "date;host;x-amz-content-sha256;x-amz-date;x-amz-storage-class",
		},
		{
			name:      "GET Bucket Lifecycle: a query parameter with no value",
			method:    http.MethodGet,
			url:       "https://examplebucket.s3.amazonaws.com/?lifecycle",
			payload:   "",
			wantSig:   "fea454ca298b7da1c68078a5d1bdbfbbe0d65c699e0f91ac7a200a0136783543",
			wantSHead: "host;x-amz-content-sha256;x-amz-date",
		},
		{
			name:      "List Objects: two query parameters, which must sort",
			method:    http.MethodGet,
			url:       "https://examplebucket.s3.amazonaws.com/?max-keys=2&prefix=J",
			payload:   "",
			wantSig:   "34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7",
			wantSHead: "host;x-amz-content-sha256;x-amz-date",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			req, err := http.NewRequest(c.method, c.url, strings.NewReader(c.payload))
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range c.headers {
				req.Header.Set(k, v)
			}

			Sign(req, hexSHA256([]byte(c.payload)), vectorKeyID, vectorSecret, vectorRegion, "s3", vectorTime)

			auth := req.Header.Get("Authorization")
			if !strings.Contains(auth, "Signature="+c.wantSig) {
				t.Errorf("signature mismatch\n got: %s\nwant Signature=%s", auth, c.wantSig)
			}
			if !strings.Contains(auth, "SignedHeaders="+c.wantSHead+",") {
				t.Errorf("signed headers mismatch\n got: %s\nwant SignedHeaders=%s", auth, c.wantSHead)
			}
			if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 Credential="+vectorKeyID+"/20130524/us-east-1/s3/aws4_request") {
				t.Errorf("credential scope mismatch: %s", auth)
			}
		})
	}
}

// TestRFC3986EscapingDiffersFromQueryEscape pins the difference that a naive
// url.QueryEscape hides: SigV4 wants %20 for a space and %2B for a plus, and
// Go's query escaping produces `+` for the first.
func TestRFC3986EscapingDiffersFromQueryEscape(t *testing.T) {
	for in, want := range map[string]string{
		"a b":     "a%20b",
		"a+b":     "a%2Bb",
		"a/b":     "a%2Fb",
		"a~b":     "a~b",
		"a_b.c-d": "a_b.c-d",
		"ü":       "%C3%BC",
	} {
		if got := rfc3986Escape(in); got != want {
			t.Errorf("rfc3986Escape(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestHeaderValuesAreCollapsed pins the trimming rule: leading and trailing
// space goes, and internal runs become one space.
func TestHeaderValuesAreCollapsed(t *testing.T) {
	for in, want := range map[string]string{
		"  a  b  ": "a b",
		"a\tb":     "a b",
		"plain":    "plain",
	} {
		if got := collapseSpaces(in); got != want {
			t.Errorf("collapseSpaces(%q) = %q, want %q", in, got, want)
		}
	}
}
