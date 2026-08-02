package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mcavage/dk-cli/internal/handoff"
	"github.com/mcavage/dk-cli/internal/output"
)

func TestSchema_CoversEveryRegisteredCommand(t *testing.T) {
	r := runCapture(t, "schema")
	env := r.envelope(t)
	if ok, _ := env["ok"].(bool); !ok {
		t.Fatalf("schema should always succeed: %v", env)
	}
	data := env["data"].(map[string]any)
	cmds := data["commands"].([]any)
	if len(cmds) != len(registry()) {
		t.Fatalf("schema lists %d commands, registry has %d", len(cmds), len(registry()))
	}

	names := map[string]bool{}
	for _, c := range cmds {
		m := c.(map[string]any)
		names[m["name"].(string)] = true
		if m["usage"].(string) == "" {
			t.Fatalf("command %v missing usage", m["name"])
		}
	}
	for _, want := range []string{"part.search", "part.get", "part.price",
		"bom.price", "bom.resolve", "bom.push", "auth.status", "doctor", "schema", "agents-md", "version"} {
		if !names[want] {
			t.Fatalf("schema missing command %q", want)
		}
	}

	exitCodes := data["exit_codes"].([]any)
	if len(exitCodes) == 0 {
		t.Fatal("schema missing exit code table")
	}
	envShape := data["envelope"].(map[string]any)
	if envShape["example_ok"] == nil || envShape["example_failed"] == nil {
		t.Fatal("schema envelope section missing worked examples")
	}
}

func TestSchema_NoCredentialsNeeded(t *testing.T) {
	t.Setenv("DK_CLIENT_ID", "")
	t.Setenv("DK_CLIENT_SECRET", "")
	r := runCapture(t, "schema")
	if r.Exit != output.ExitOK {
		t.Fatalf("schema should not need credentials, exit = %d", r.Exit)
	}
}

func TestAgentsMD_NoCredentialsNeeded(t *testing.T) {
	t.Setenv("DK_CLIENT_ID", "")
	t.Setenv("DK_CLIENT_SECRET", "")
	r := runCapture(t, "agents-md")
	env := r.envelope(t)
	data := env["data"].(map[string]any)
	content, _ := data["content"].(string)
	if !strings.Contains(content, "dk") {
		t.Fatalf("agents-md content looks empty or wrong: %q", content)
	}
}

func TestVersion_NoCredentialsNeeded(t *testing.T) {
	t.Setenv("DK_CLIENT_ID", "")
	t.Setenv("DK_CLIENT_SECRET", "")
	r := runCapture(t, "version")
	env := r.envelope(t)
	data := env["data"].(map[string]any)
	if _, ok := data["disclaimer"].(string); !ok {
		t.Fatalf("version missing non-affiliation disclaimer: %v", data)
	}
}

func TestAuthStatus_DegradesWithNoCredentials(t *testing.T) {
	t.Setenv("DK_CLIENT_ID", "")
	t.Setenv("DK_CLIENT_SECRET", "")
	r := runCapture(t, "auth", "status")
	if r.Exit != output.ExitOK {
		t.Fatalf("auth status must not fail hard on missing credentials, exit = %d", r.Exit)
	}
	env := r.envelope(t)
	data := env["data"].(map[string]any)
	if present, _ := data["client_id_present"].(bool); present {
		t.Fatalf("client_id_present should be false with no env set")
	}
}

func TestDoctor_DegradesWithNoCredentials(t *testing.T) {
	t.Setenv("DK_CLIENT_ID", "")
	t.Setenv("DK_CLIENT_SECRET", "")

	// Point the handoff check at a local stub so this test needs no network
	// access; only the no-credentials degradation is under test here.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`"https://www.digikey.com/short/doctortest"`))
	}))
	defer srv.Close()

	cmds := registry()
	doctorEntry := mustFind(t, cmds, "doctor")
	fs, fv := buildFlagSet(doctorEntry)
	fs.Parse(nil)

	rc := testRC()
	rc.newHandoff = func() *handoff.Client {
		return handoff.New(handoff.Options{BaseURL: srv.URL})
	}
	env, _ := doctorEntry.Run(rc, nil, fv)
	if !env.OK {
		t.Fatalf("doctor itself should always report ok:true (it's a report), got %+v", env.Error)
	}
	data := env.Data.(map[string]any)
	checks := data["checks"].([]doctorCheck)
	found := map[string]doctorCheck{}
	for _, c := range checks {
		found[c.Name] = c
	}
	if found["credentials"].OK {
		t.Fatalf("credentials check should fail with no env set")
	}
	if found["token"].OK || found["product_details"].OK {
		t.Fatalf("token/product_details must degrade gracefully (not attempt a call) with no credentials")
	}
	if !found["handoff"].OK {
		t.Fatalf("handoff check should succeed against the stub: %+v", found["handoff"])
	}
}

func mustFind(t *testing.T, cmds []command, name string) command {
	t.Helper()
	for _, c := range cmds {
		if c.name() == name {
			return c
		}
	}
	t.Fatalf("command %q not found", name)
	return command{}
}
