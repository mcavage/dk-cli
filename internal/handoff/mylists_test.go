package handoff

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// resistorLine is the real fixture from /tmp/dk-contract.md's verified MyLists
// example, minus the array wrapper.
func resistorLine() Line {
	return Line{
		PartNumber:   "311-10.0KCRCT-ND",
		Manufacturer: "Yageo",
		Qty:          10,
		RefDes:       []string{"R1", "R2"},
		CustomerRef:  "pedal-v2",
	}
}

// TestMyLists_BareStringResponse covers the shape DigiKey actually sends,
// verified live and undocumented: a bare JSON string, not an object.
func TestMyLists_BareStringResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`"https://www.digikey.com/short/b3hdmm74"`))
	}))
	defer srv.Close()

	c := New(Options{BaseURL: srv.URL})
	res, err := c.MyLists(context.Background(), "dk-cli-test", "cli", []Line{resistorLine()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.URL != "https://www.digikey.com/short/b3hdmm74" {
		t.Fatalf("got URL %q", res.URL)
	}
	if res.Warning != ExpiryWarning {
		t.Fatalf("got warning %q, want ExpiryWarning", res.Warning)
	}
}

// TestMyLists_ObjectResponse covers the shape DigiKey's own documentation
// describes. DigiKey may fix their docs bug, so both shapes must keep working.
func TestMyLists_ObjectResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"singleUseUrl":"https://www.digikey.com/short/b3hdmm74"}`))
	}))
	defer srv.Close()

	c := New(Options{BaseURL: srv.URL})
	res, err := c.MyLists(context.Background(), "dk-cli-test", "cli", []Line{resistorLine()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.URL != "https://www.digikey.com/short/b3hdmm74" {
		t.Fatalf("got URL %q", res.URL)
	}
}

// TestMyLists_MalformedResponse covers a body that is neither documented
// shape: it must be a clear error, not a nil URL a caller might open.
func TestMyLists_MalformedResponse(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"not json at all", "not json at all"},
		{"truncated json", `{"singleUseUrl":`},
		{"wrong field name", `{"url":"https://www.digikey.com/short/b3hdmm74"}`},
		{"empty bare string", `""`},
		{"json null", `null`},
		{"unrelated array", `[1,2,3]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c := New(Options{BaseURL: srv.URL})
			_, err := c.MyLists(context.Background(), "dk-cli-test", "cli", []Line{resistorLine()})
			if !errors.Is(err, ErrBadResponse) {
				t.Fatalf("got err %v, want ErrBadResponse", err)
			}
		})
	}
}

// TestMyLists_HTTPErrorStatus covers DigiKey rejecting the request outright.
func TestMyLists_HTTPErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	}))
	defer srv.Close()

	c := New(Options{BaseURL: srv.URL})
	_, err := c.MyLists(context.Background(), "dk-cli-test", "cli", []Line{resistorLine()})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("error %v does not mention the status code", err)
	}
}

// TestMyLists_NetworkFailure covers the server being unreachable, which is a
// different failure mode than a non-2xx response and must not panic or hang.
func TestMyLists_NetworkFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // now nothing is listening

	c := New(Options{BaseURL: url})
	_, err := c.MyLists(context.Background(), "dk-cli-test", "cli", []Line{resistorLine()})
	if err == nil {
		t.Fatal("expected an error")
	}
}

// TestMyLists_RequestShape asserts the query params and JSON body match the
// verified contract example, since this endpoint has no spec to generate
// against.
func TestMyLists_RequestShape(t *testing.T) {
	var gotQuery, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mylists/api/thirdparty" {
			t.Errorf("got path %q", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("got method %q", r.Method)
		}
		gotQuery = r.URL.RawQuery
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Write([]byte(`"https://www.digikey.com/short/b3hdmm74"`))
	}))
	defer srv.Close()

	c := New(Options{BaseURL: srv.URL})
	_, err := c.MyLists(context.Background(), "pedal-v2", "cli,agent", []Line{resistorLine()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotQuery, "listName=pedal-v2") {
		t.Fatalf("query %q missing listName", gotQuery)
	}
	if !strings.Contains(gotQuery, "tags=cli%2Cagent") {
		t.Fatalf("query %q missing tags", gotQuery)
	}
	for _, want := range []string{
		`"requestedPartNumber":"311-10.0KCRCT-ND"`,
		`"manufacturerName":"Yageo"`,
		`"referenceDesignator":"R1,R2"`,
		`"customerReference":"pedal-v2"`,
		`"quantities":[{"quantity":10}]`,
	} {
		if !strings.Contains(gotBody, want) {
			t.Fatalf("body %s missing %s", gotBody, want)
		}
	}
}

func TestMyLists_ValidationErrors(t *testing.T) {
	c := New(Options{BaseURL: "http://unused.invalid"})

	t.Run("empty part list", func(t *testing.T) {
		_, err := c.MyLists(context.Background(), "l", "t", nil)
		if !errors.Is(err, ErrNoLines) {
			t.Fatalf("got %v, want ErrNoLines", err)
		}
	})

	t.Run("zero quantity", func(t *testing.T) {
		l := resistorLine()
		l.Qty = 0
		_, err := c.MyLists(context.Background(), "l", "t", []Line{l})
		if !errors.Is(err, ErrBadQuantity) {
			t.Fatalf("got %v, want ErrBadQuantity", err)
		}
	})

	t.Run("negative quantity", func(t *testing.T) {
		l := resistorLine()
		l.Qty = -3
		_, err := c.MyLists(context.Background(), "l", "t", []Line{l})
		if !errors.Is(err, ErrBadQuantity) {
			t.Fatalf("got %v, want ErrBadQuantity", err)
		}
	})

	t.Run("missing part number", func(t *testing.T) {
		l := resistorLine()
		l.PartNumber = "  "
		_, err := c.MyLists(context.Background(), "l", "t", []Line{l})
		if !errors.Is(err, ErrMissingPart) {
			t.Fatalf("got %v, want ErrMissingPart", err)
		}
	})

	t.Run("absurdly large part count", func(t *testing.T) {
		lines := make([]Line, MyListsMaxLines+1)
		for i := range lines {
			lines[i] = resistorLine()
		}
		_, err := c.MyLists(context.Background(), "l", "t", lines)
		if !errors.Is(err, ErrTooManyLines) {
			t.Fatalf("got %v, want ErrTooManyLines", err)
		}
	})
}

// The returned URL goes to a browser, so a third party naming an arbitrary URL
// must not become "the CLI launches whatever it is told to". This endpoint is
// unversioned, so a redirect or injection bug there must not escalate locally.
func TestMyLists_RejectsNonDigiKeyOrNonHTTPSURLs(t *testing.T) {
	cases := map[string]string{
		"attacker host":  `"https://evil.example.com/short/abc"`,
		"plain http":     `"http://www.digikey.com/short/abc"`,
		"file scheme":    `"file:///etc/passwd"`,
		"javascript":     `"javascript:alert(1)"`,
		"host suffix":    `"https://www.digikey.com.evil.example/short/abc"`,
		"object variant": `{"singleUseUrl":"https://evil.example.com/x"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			_, err := New(Options{BaseURL: srv.URL}).MyLists(
				context.Background(), "test", "", []Line{dkLine("311-X-ND", 1)})
			if err == nil {
				t.Fatalf("must refuse %s", name)
			}
			if !errors.Is(err, ErrBadResponse) {
				t.Fatalf("want ErrBadResponse, got %v", err)
			}
		})
	}
}

func TestMyLists_AcceptsRealDigiKeyURL(t *testing.T) {
	for _, body := range []string{
		`"https://www.digikey.com/short/b3hdmm74"`,
		`"https://digikey.com/short/b3hdmm74"`,
		`{"singleUseUrl":"https://www.digikey.com/mylists/singleuse/12345abcde"}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		res, err := New(Options{BaseURL: srv.URL}).MyLists(
			context.Background(), "test", "", []Line{dkLine("311-X-ND", 1)})
		srv.Close()
		if err != nil {
			t.Fatalf("%s must be accepted, got %v", body, err)
		}
		if res.URL == "" {
			t.Fatalf("%s produced an empty URL", body)
		}
	}
}
