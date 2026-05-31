package web

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/command"
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
	for _, want := range []string{"demo", "gain", "pm25", "Config", "Telemetry", "Recent", "Containers", "Actions"} {
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

func TestInstallUploadRegistersPayloadAndQueuesRun(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("name", "demo")
	mw.WriteField("interval", "30s")
	mw.WriteField("lifecycle", "run-once")
	fw, _ := mw.CreateFormFile("image", "demo.bin")
	fw.Write([]byte("IMAGE-BYTES"))
	mw.Close()

	resp, err := http.Post(srv.URL+"/n/aabbccddeeff/containers/install", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, readBody(t, resp))
	}
	cmds, _ := st.CommandLog("aabbccddeeff")
	if len(cmds) != 1 || cmds[0].Verb != "run" || cmds[0].IssuedBy != "web" {
		t.Fatalf("want web run command, got %+v", cmds)
	}
	ok, err := st.PayloadExists(int64(command.CRC32([]byte("IMAGE-BYTES"))))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("payload not registered")
	}
}

func TestUninstallFormQueuesStop(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)

	resp, err := http.PostForm(srv.URL+"/n/aabbccddeeff/containers/uninstall", url.Values{"name": {"demo"}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status %d: %s", resp.StatusCode, readBody(t, resp))
	}
	cmds, _ := st.CommandLog("aabbccddeeff")
	if len(cmds) != 1 || cmds[0].Verb != "stop" || cmds[0].IssuedBy != "web" {
		t.Fatalf("want web stop command, got %+v", cmds)
	}
}

func TestInstallEmptyNameRejected(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)

	// No "name" field and a filename that reduces to "" after stripping .bin.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("image", ".bin")
	fw.Write([]byte("IMAGE-BYTES"))
	mw.Close()

	resp, err := http.Post(srv.URL+"/n/aabbccddeeff/containers/install", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty name got %d, want 400", resp.StatusCode)
	}
	if cmds, _ := st.CommandLog("aabbccddeeff"); len(cmds) != 0 {
		t.Fatalf("empty-name install should enqueue nothing, got %+v", cmds)
	}
}

func TestInstallOversizedUploadRejected(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("name", "demo")
	fw, _ := mw.CreateFormFile("image", "demo.bin")
	fw.Write(bytes.Repeat([]byte("A"), maxUpload+1)) // one byte past the hard cap
	mw.Close()

	resp, err := http.Post(srv.URL+"/n/aabbccddeeff/containers/install", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("oversized upload got %d, want 400", resp.StatusCode)
	}
	if cmds, _ := st.CommandLog("aabbccddeeff"); len(cmds) != 0 {
		t.Fatalf("oversized install should enqueue nothing, got %+v", cmds)
	}
}

func TestGetToInstallIsRejected(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)

	for _, sub := range []string{"install", "uninstall"} {
		resp, err := http.Get(srv.URL + "/n/aabbccddeeff/containers/" + sub)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("GET %s got %d, want 405", sub, resp.StatusCode)
		}
		resp.Body.Close()
	}
	cmds, _ := st.CommandLog("aabbccddeeff")
	if len(cmds) != 0 {
		t.Fatalf("GET should enqueue nothing, got %+v", cmds)
	}
}

func TestLogPageRendersCommands(t *testing.T) {
	st := testStore(t)
	st.EnqueueCommand("aabbccddeeff", "set", `{"app":"demo","key":"gain","value":3}`, "web", 1000)
	srv := serve(t, st)

	body := readBody(t, mustGet(t, srv.URL+"/log"))
	for _, want := range []string{"aabbccddeeff", "set", "web", "Command Log"} {
		if !strings.Contains(body, want) {
			t.Errorf("/log missing %q", want)
		}
	}

	p := readBody(t, mustGet(t, srv.URL+"/partials/log"))
	if !strings.Contains(p, "<tbody") || !strings.Contains(p, "aabbccddeeff") {
		t.Errorf("partial missing tbody/node: %s", p)
	}
}

func TestLogPageEscapesArgs(t *testing.T) {
	st := testStore(t)
	// Args carries an operator-supplied string value with HTML metacharacters.
	st.EnqueueCommand("aabbccddeeff", "set", `{"app":"demo","key":"n","value":"<script>x</script>"}`, "web", 1000)
	srv := serve(t, st)

	body := readBody(t, mustGet(t, srv.URL+"/log"))
	if strings.Contains(body, "<script>x</script>") {
		t.Errorf("args rendered unescaped (XSS): %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;x&lt;/script&gt;") {
		t.Errorf("args not html-escaped: %s", body)
	}
}

func TestNodeRecentCommandsBadges(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	// A delivered set whose observed config matches → converged.
	id, err := control.Set(st, "aabbccddeeff", "demo", "gain", int64(2), "cli", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkDelivered(id, 1001); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertReport("aabbccddeeff",
		`{"config":{"demo":{"gain":2}},"apps":{"demo":{"crc":99,"runlevel":3}}}`, "", 1002); err != nil {
		t.Fatal(err)
	}
	srv := serve(t, st)

	body := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff"))
	for _, want := range []string{"Recent commands", "badge-converged", `id="recent"`} {
		if !strings.Contains(body, want) {
			t.Errorf("recent section missing %q: %s", want, body)
		}
	}
	// The polled partial endpoint serves the same section.
	p := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff/recent"))
	if !strings.Contains(p, `id="recent"`) || !strings.Contains(p, "badge-") {
		t.Errorf("recent partial missing wrapper/badge: %s", p)
	}
}
