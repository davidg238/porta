package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func serve(t *testing.T, st *store.Store) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	New(st).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestNodeTableRendersRowAndGauge(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)

	body := readBody(t, mustGet(t, srv.URL+"/"))
	if !strings.Contains(body, "192.168.1.9") || !strings.Contains(body, "gauge") {
		t.Errorf("row/gauge missing: %s", body)
	}
	p := readBody(t, mustGet(t, srv.URL+"/partials/nodes"))
	if !strings.Contains(p, "<tbody") || !strings.Contains(p, "aabbccddeeff") {
		t.Errorf("partial missing tbody/node: %s", p)
	}
}

func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestNodeDetailRendersSections(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	if _, err := control.Set(st, "aabbccddeeff", "demo", "gain", int64(2), "cli", 1000); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertReport("aabbccddeeff",
		`{"config":{"demo":{"gain":2}},"apps":{"demo":{"crc":99,"runlevel":3}}}`, "", 1001); err != nil {
		t.Fatal(err)
	}
	st.InsertData("aabbccddeeff", 1001, 0, "metric", "pm25", int64(7), "", "int")
	srv := serve(t, st)

	body := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff"))
	for _, want := range []string{"demo", "gain", "pm25", "Config", "Telemetry", "Pending", "Containers", "Actions"} {
		if !strings.Contains(body, want) {
			t.Errorf("detail body missing %q: %s", want, body)
		}
	}

	nf := mustGet(t, srv.URL+"/n/deadbeef0000")
	if nf.StatusCode != 404 {
		t.Errorf("unknown node got %d, want 404", nf.StatusCode)
	}
}

func TestSetFormEnqueuesWebCommand(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)

	form := url.Values{"app": {"demo"}, "key": {"gain"}, "value": {"3"}}
	resp, err := http.PostForm(srv.URL+"/n/aabbccddeeff/set", form)
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != 200 || !strings.Contains(body, "queued") {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	cmds, _ := st.CommandLog("aabbccddeeff")
	if len(cmds) != 1 || cmds[0].Verb != "set" || cmds[0].IssuedBy != "web" {
		t.Fatalf("want one web 'set' command, got %+v", cmds)
	}
}

func TestConsoleFormEnqueuesWebCommand(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)

	resp, err := http.PostForm(srv.URL+"/n/aabbccddeeff/console", url.Values{"state": {"on"}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	resp.Body.Close()
	cmds, _ := st.CommandLog("aabbccddeeff")
	if len(cmds) != 1 || cmds[0].Verb != "set-console" || cmds[0].IssuedBy != "web" {
		t.Fatalf("want one web 'set-console' command, got %+v", cmds)
	}
}

func TestPollIntervalFormEnqueuesAndPersists(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)

	resp, err := http.PostForm(srv.URL+"/n/aabbccddeeff/poll-interval", url.Values{"dur": {"60s"}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	resp.Body.Close()
	cmds, _ := st.CommandLog("aabbccddeeff")
	if len(cmds) != 1 || cmds[0].Verb != "set-poll-interval" || cmds[0].IssuedBy != "web" {
		t.Fatalf("want one web 'set-poll-interval' command, got %+v", cmds)
	}
	n, _ := st.GetNode("aabbccddeeff")
	if n == nil || n.PollIntervalS != 60 {
		t.Fatalf("poll interval not persisted, got %+v", n)
	}
}

func TestRenameFormRenamesNode(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)

	resp, err := http.PostForm(srv.URL+"/n/aabbccddeeff/rename", url.Values{"name": {"foo"}})
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != 200 || !strings.Contains(body, "foo") {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	n, _ := st.GetNode("aabbccddeeff")
	if n == nil || n.Name != "foo" {
		t.Fatalf("node not renamed, got %+v", n)
	}
}

func TestGetToWriteSubPathIsRejected(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)

	resp := mustGet(t, srv.URL+"/n/aabbccddeeff/set?app=demo&key=gain&value=3")
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET to /set got %d, want 405", resp.StatusCode)
	}
	cmds, _ := st.CommandLog("aabbccddeeff")
	if len(cmds) != 0 {
		t.Fatalf("GET must not enqueue, got %+v", cmds)
	}
}

func TestIndexRendersNavAndAssets(t *testing.T) {
	st := testStore(t)
	srv := serve(t, st)

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if !strings.Contains(body, "Nodes") || !strings.Contains(body, "Command Log") {
		t.Errorf("nav missing: %s", body)
	}
	js, _ := http.Get(srv.URL + "/assets/htmx.min.js")
	if js.StatusCode != 200 {
		t.Errorf("htmx asset status %d", js.StatusCode)
	}
	nf, _ := http.Get(srv.URL + "/nope")
	if nf.StatusCode != 404 {
		t.Errorf("unknown path got %d, want 404", nf.StatusCode)
	}
}
