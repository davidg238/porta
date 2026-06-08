// Copyright (c) 2026 Ekorau LLC

package web

import (
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/davidg238/porta/internal/control"
	"github.com/davidg238/porta/internal/store"
	"github.com/davidg238/porta/internal/telemetry"
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
	srv := serve(t, st)

	body := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff"))
	// Read-only dashboard: identity/config/recent/containers + the gateway-settings
	// edit block survive; node-command write surfaces do not.
	for _, want := range []string{"demo", "gain", "Config", "Telemetry", "Recent", "Containers"} {
		if !strings.Contains(body, want) {
			t.Errorf("detail body missing %q: %s", want, body)
		}
	}
	for _, gone := range []string{"Actions", "upload &amp; queue run", "queue stop", `hx-post="/n/aabbccddeeff/set"`} {
		if strings.Contains(body, gone) {
			t.Errorf("detail body should not contain %q (read-only dashboard): %s", gone, body)
		}
	}

	nf := mustGet(t, srv.URL+"/n/deadbeef0000")
	if nf.StatusCode != 404 {
		t.Errorf("unknown node got %d, want 404", nf.StatusCode)
	}
}

// The node page is a fully read-only dashboard: every write route — node
// commands AND the former gateway-side rename/max-offline — was removed. Config
// originates only in nodus-cli now; rename moved to `nodus rename`. They must
// 404 and enqueue nothing, whatever the method.
func TestRemovedWriteRoutesAre404(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)

	for _, sub := range []string{"set", "console", "poll-interval", "rename", "max-offline", "containers/install", "containers/uninstall"} {
		resp, err := http.PostForm(srv.URL+"/n/aabbccddeeff/"+sub, url.Values{"app": {"demo"}, "key": {"gain"}, "value": {"3"}, "state": {"on"}, "dur": {"60s"}, "name": {"demo"}})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("POST /%s got %d, want 404", sub, resp.StatusCode)
		}
	}
	if cmds, _ := st.CommandLog("aabbccddeeff"); len(cmds) != 0 {
		t.Fatalf("removed routes must not enqueue, got %+v", cmds)
	}
}

// The node page shows the node-owned mode + cadence read-only (from the
// node_config echo), and exposes no form that originates a config change.
func TestNodePageShowsModeCadenceReadOnly(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	st.UpdateNodeConfig("aabbccddeeff", `{"mode":"always-on","poll_interval_s":60,"name":"vin"}`, "vin")
	srv := serve(t, st)

	body := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff"))
	for _, want := range []string{"mode always-on", "cadence 1m"} {
		if !strings.Contains(body, want) {
			t.Errorf("node page missing read-only %q: %s", want, body)
		}
	}
	if strings.Contains(body, "gw-settings") || strings.Contains(body, "edit gateway settings") {
		t.Error("node page must expose no gateway-settings write form")
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

// Column order is #, command (verb), delivered badge, then the full JSON args
// running last (widest column).
func TestRecentCommandsColumnOrder(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	id, err := control.Set(st, "aabbccddeeff", "demo", "gain", int64(2), "cli", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkDelivered(id, 1001); err != nil {
		t.Fatal(err)
	}
	srv := serve(t, st)

	p := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff/recent"))
	verb := strings.Index(p, ">set<")
	badge := strings.Index(p, "badge-")
	args := strings.Index(p, "&#34;gain&#34;") // the JSON args, HTML-escaped
	if verb < 0 || badge < 0 || args < 0 {
		t.Fatalf("missing cells: verb=%d badge=%d args=%d\n%s", verb, badge, args, p)
	}
	if !(verb < badge && badge < args) {
		t.Errorf("want order verb < badge < args, got verb=%d badge=%d args=%d", verb, badge, args)
	}
}

func TestNodeHeaderShowsIdentity(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	if err := st.UpdateNodeIdentity("aabbccddeeff", "esp32", "v2.0.0-alpha.192"); err != nil {
		t.Fatal(err)
	}
	srv := serve(t, st)

	body := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff"))
	for _, want := range []string{"esp32", "v2.0.0-alpha.192"} {
		if !strings.Contains(body, want) {
			t.Errorf("header missing reported identity %q: %s", want, body)
		}
	}
}

func TestNodeHeaderIdentityFallback(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000) // no UpdateNodeIdentity → chip/sdk empty
	srv := serve(t, st)

	body := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff"))
	for _, want := range []string{"chip ?", "sdk —"} {
		if !strings.Contains(body, want) {
			t.Errorf("header missing identity fallback %q: %s", want, body)
		}
	}
}

func TestNodeConsolePanels(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	_ = st.InsertData("aabbccddeeff", 1001, 0, "print", "", nil, "hello world", "", "")
	_ = st.InsertData("aabbccddeeff", 1002, 0, "log", "", nil, "pump stalled", "", "warn")
	_ = st.InsertData("aabbccddeeff", 1003, 0, "panic", "", nil, "traceblob", "", "")
	srv := serve(t, st)

	prints := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff/prints"))
	if !strings.Contains(prints, `id="prints"`) || !strings.Contains(prints, "hello world") {
		t.Errorf("prints panel missing content: %s", prints)
	}
	if strings.Contains(prints, "pump stalled") {
		t.Errorf("prints panel must not contain log lines: %s", prints)
	}

	logs := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff/logs"))
	if !strings.Contains(logs, `id="logs"`) || !strings.Contains(logs, "[warn] pump stalled") {
		t.Errorf("logs panel missing leveled log: %s", logs)
	}
	if !strings.Contains(logs, "traceblob") {
		t.Errorf("logs panel should include panic rows: %s", logs)
	}
	if strings.Contains(logs, "hello world") {
		t.Errorf("logs panel must not contain prints: %s", logs)
	}
}

func TestNodeConsoleEmptyHint(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)
	prints := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff/prints"))
	if !strings.Contains(prints, "no prints") {
		t.Errorf("want empty hint, got: %s", prints)
	}
}

func TestNodeConsoleEscapesText(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	_ = st.InsertData("aabbccddeeff", 1001, 0, "print", "", nil, "<script>x</script>", "", "")
	srv := serve(t, st)
	prints := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff/prints"))
	if strings.Contains(prints, "<script>x</script>") {
		t.Errorf("console text must be HTML-escaped: %s", prints)
	}
}

func TestNodePageEmbedsConsolePlaceholders(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	srv := serve(t, st)
	body := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff"))
	if !strings.Contains(body, `hx-get="/n/aabbccddeeff/prints"`) ||
		!strings.Contains(body, `hx-get="/n/aabbccddeeff/logs"`) {
		t.Errorf("node page missing console placeholders: %s", body)
	}
}

func TestTelemetryPageMetricsOnly(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	st.InsertData("aabbccddeeff", 1001, 1, "metric", "pm25", int64(7), "", "int", "")
	st.InsertData("aabbccddeeff", 1001, 0, "log", "", nil, "vin: pm25=7 (olympic)", "", "")
	srv := serve(t, st)

	body := readBody(t, mustGet(t, srv.URL+"/telemetry"))
	if !strings.Contains(body, "pm25") {
		t.Errorf("telemetry page missing metric: %s", body)
	}
	if strings.Contains(body, "olympic") {
		t.Errorf("telemetry page leaked a log row: %s", body)
	}
	// Polled partial honors the node filter and re-emits its wrapper.
	p := readBody(t, mustGet(t, srv.URL+"/partials/telemetry?node=aabbccddeeff"))
	if !strings.Contains(p, `id="telem"`) || !strings.Contains(p, "pm25") {
		t.Errorf("telemetry partial missing wrapper/metric: %s", p)
	}
}

func TestTelemetryNodeFilterSelect(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	_ = st.SetNodeName("aabbccddeeff", "fwkb")
	st.TouchNode("ccddeeff0011", "192.168.1.10", 1000)
	_ = st.SetNodeName("ccddeeff0011", "vin")
	srv := serve(t, st)

	// unfiltered: All nodes selected, both options present
	all := readBody(t, mustGet(t, srv.URL+"/telemetry"))
	if !strings.Contains(all, `name="node"`) ||
		!strings.Contains(all, ">All nodes<") ||
		!strings.Contains(all, ">fwkb<") || !strings.Contains(all, ">vin<") {
		t.Errorf("telemetry select missing options: %s", all)
	}

	// filtered: the chosen node's option is marked selected
	one := readBody(t, mustGet(t, srv.URL+"/telemetry?node=aabbccddeeff"))
	if !strings.Contains(one, `value="aabbccddeeff" selected`) {
		t.Errorf("selected option not marked: %s", one)
	}
}

func TestNodeLogsPanicDecodeLink(t *testing.T) {
	st := testStore(t)
	st.TouchNode("aabbccddeeff", "192.168.1.9", 1000)
	// A plain log row (no link) and a panic row (gets a decode link). The blob
	// contains +, /, = on purpose so we exercise URL-encoding round-trip.
	_ = st.InsertData("aabbccddeeff", 1002, 0, "log", "", nil, "plain log", "", "")
	_ = st.InsertData("aabbccddeeff", 1003, 0, "panic", "", nil, "a+b/c=d", "", "")
	srv := serve(t, st)

	body := readBody(t, mustGet(t, srv.URL+"/n/aabbccddeeff/logs"))
	// html/template escapes text nodes (+ -> &#43;, = -> &#61;, & -> &amp;).
	// Unescape once so the visibility/order/href checks compare logical chars;
	// the href's blob param is *additionally* percent-encoded, which
	// url.ParseQuery undoes below.
	logs := html.UnescapeString(body)

	// Exactly one decode link: the panic row gets it, the log row does not.
	if n := strings.Count(logs, "[decode"); n != 1 {
		t.Fatalf("want exactly one decode link, got %d: %s", n, logs)
	}
	// html/template must NOT have neutralized the nodus:// scheme.
	if strings.Contains(logs, "ZgotmplZ") {
		t.Fatalf("nodus:// href was sanitized to ZgotmplZ: %s", logs)
	}
	// Link sits between the panic column and the raw blob, which stays visible.
	iPanic := strings.Index(logs, "panic")
	iLink := strings.Index(logs, "[decode")
	iBlob := strings.Index(logs, "a+b/c=d")
	if iBlob < 0 || !(iPanic < iLink && iLink < iBlob) {
		t.Fatalf("want order panic<[decode]<blob, got %d/%d/%d: %s", iPanic, iLink, iBlob, logs)
	}
	// The panic line's prefix must stay column-aligned with FormatLine (which
	// owns the "ts  col  text" layout). renderNodeLogs rebuilds that prefix
	// by hand, so guard against silent drift if FormatLine's spacing changes.
	prefix := strings.TrimSuffix(telemetry.FormatLine(store.DataRow{TS: 1003, Kind: "panic", Text: "a+b/c=d"}), "a+b/c=d")
	if !strings.Contains(logs, prefix+`<a href="nodus://`) {
		t.Errorf("panic prefix not aligned with FormatLine: want %q before the decode link, in: %s", prefix, logs)
	}
	// The href round-trips: node + blob parse back to the originals.
	m := regexp.MustCompile(`href="(nodus://decode\?[^"]*)"`).FindStringSubmatch(logs)
	if m == nil {
		t.Fatalf("no nodus decode href found: %s", logs)
	}
	q, err := url.ParseQuery(strings.TrimPrefix(m[1], "nodus://decode?"))
	if err != nil {
		t.Fatalf("href query parse: %v (%q)", err, m[1])
	}
	if got := q.Get("node"); got != "aabbccddeeff" {
		t.Errorf("node param = %q, want aabbccddeeff", got)
	}
	if got := q.Get("blob"); got != "a+b/c=d" {
		t.Errorf("blob param = %q, want a+b/c=d", got)
	}
}
