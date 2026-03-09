package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// testEnv holds a shared browser and test HTTP server for all tests.
type testEnv struct {
	browser  *rod.Browser
	server   *httptest.Server
	debugURL string // WebSocket debug URL for setting up state in cmdXxx tests
}

var env *testEnv

func TestMain(m *testing.M) {
	// Launch headless Chrome once for all tests
	l := launcher.New().
		Set("no-sandbox").
		Set("disable-gpu").
		Set("single-process").
		Headless(true).
		Leakless(false)

	if bin := os.Getenv("ROD_CHROME_BIN"); bin != "" {
		l = l.Bin(bin)
	}

	u := l.MustLaunch()
	browser := rod.New().ControlURL(u).MustConnect()

	// Start test HTTP server with known HTML fixtures
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/form", handleForm)
	mux.HandleFunc("/upload", handleUpload)
	mux.HandleFunc("/download", handleDownload)
	mux.HandleFunc("/testfile.txt", handleTestFile)
	mux.HandleFunc("/empty", handleEmpty)
	mux.HandleFunc("/logs", handleLogs)
	mux.HandleFunc("/discover", handleDiscover)
	server := httptest.NewServer(mux)

	env = &testEnv{browser: browser, server: server, debugURL: u}

	code := m.Run()

	server.Close()
	browser.MustClose()
	os.Exit(code)
}

// --- HTML fixtures ---

func handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head><title>Test Page</title></head>
<body>
  <nav aria-label="Main">
    <a href="/about">About</a>
    <a href="/contact">Contact</a>
  </nav>
  <main>
    <h1>Welcome</h1>
    <p>Hello world</p>
    <button id="submit-btn">Submit</button>
    <button id="cancel-btn" disabled>Cancel</button>
  </main>
</body>
</html>`))
}

func handleForm(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head><title>Form Page</title></head>
<body>
  <h1>Contact Us</h1>
  <form>
    <label for="name-input">Name</label>
    <input id="name-input" type="text" aria-required="true">
    <label for="email-input">Email</label>
    <input id="email-input" type="email">
    <select id="topic" aria-label="Topic">
      <option value="general">General</option>
      <option value="support">Support</option>
    </select>
    <button type="submit">Send</button>
  </form>
</body>
</html>`))
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head><title>Upload Page</title></head>
<body>
  <input id="file-input" type="file" accept="image/*">
  <span id="file-name"></span>
  <script>
    document.getElementById('file-input').addEventListener('change', function(e) {
      document.getElementById('file-name').textContent = e.target.files[0] ? e.target.files[0].name : '';
    });
  </script>
</body>
</html>`))
}

func handleDownload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head><title>Download Page</title></head>
<body>
  <a id="file-link" href="/testfile.txt">Download file</a>
  <a id="data-link" href="data:text/plain;base64,SGVsbG8gV29ybGQ=">Download data</a>
  <img id="test-img" src="/testfile.txt">
</body>
</html>`))
}

func handleTestFile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("Hello World"))
}

func handleEmpty(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head><title>Empty Page</title></head>
<body></body>
</html>`))
}

func handleLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head><title>Logs Test Page</title></head>
<body>
<script>
  console.log("info message from logs test");
  console.warn("warning message from logs test");
  console.error("error message from logs test");
</script>
</body>
</html>`))
}

// --- Helper: navigate to a fixture and return the page ---

func navigateTo(t *testing.T, path string) *rod.Page {
	t.Helper()
	page := env.browser.MustPage(env.server.URL + path)
	page.MustWaitLoad()
	t.Cleanup(func() { page.MustClose() })
	return page
}

// =====================
// ax-tree tests (RED)
// =====================

func TestAXTree_ReturnsNodes(t *testing.T) {
	page := navigateTo(t, "/")
	result, err := proto.AccessibilityGetFullAXTree{}.Call(page)
	if err != nil {
		t.Fatalf("CDP call failed: %v", err)
	}
	// Sanity: we should get nodes back
	if len(result.Nodes) == 0 {
		t.Fatal("expected nodes in accessibility tree, got 0")
	}

	// Now test our formatting function
	out := formatAXTree(result.Nodes)
	if out == "" {
		t.Fatal("formatAXTree returned empty string")
	}
	if !strings.Contains(out, "Welcome") {
		t.Errorf("tree should contain heading text 'Welcome', got:\n%s", out)
	}
	if !strings.Contains(out, "button") {
		t.Errorf("tree should contain 'button' role, got:\n%s", out)
	}
	if !strings.Contains(out, "Submit") {
		t.Errorf("tree should contain button name 'Submit', got:\n%s", out)
	}
}

func TestAXTree_Indentation(t *testing.T) {
	page := navigateTo(t, "/")
	result, err := proto.AccessibilityGetFullAXTree{}.Call(page)
	if err != nil {
		t.Fatalf("CDP call failed: %v", err)
	}
	out := formatAXTree(result.Nodes)
	lines := strings.Split(out, "\n")

	// Root node should have no indentation
	if len(lines) == 0 {
		t.Fatal("no lines in output")
	}
	if strings.HasPrefix(lines[0], " ") {
		t.Errorf("root node should not be indented, got: %q", lines[0])
	}

	// Some lines should be indented (children)
	hasIndented := false
	for _, line := range lines {
		if strings.HasPrefix(line, "  ") {
			hasIndented = true
			break
		}
	}
	if !hasIndented {
		t.Errorf("expected some indented lines for child nodes, got:\n%s", out)
	}
}

func TestAXTree_SkipsIgnoredNodes(t *testing.T) {
	page := navigateTo(t, "/")
	result, err := proto.AccessibilityGetFullAXTree{}.Call(page)
	if err != nil {
		t.Fatalf("CDP call failed: %v", err)
	}
	out := formatAXTree(result.Nodes)

	// Count ignored vs total
	ignoredCount := 0
	for _, node := range result.Nodes {
		if node.Ignored {
			ignoredCount++
		}
	}

	// If there are ignored nodes, they shouldn't appear in text output
	if ignoredCount > 0 {
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) >= len(result.Nodes) {
			t.Errorf("text output should skip ignored nodes: %d lines for %d nodes (%d ignored)",
				len(lines), len(result.Nodes), ignoredCount)
		}
	}
}

func TestAXTree_DepthLimit(t *testing.T) {
	page := navigateTo(t, "/")
	full, err := proto.AccessibilityGetFullAXTree{}.Call(page)
	if err != nil {
		t.Fatalf("CDP call failed: %v", err)
	}

	depth := 2
	limited, err := proto.AccessibilityGetFullAXTree{Depth: &depth}.Call(page)
	if err != nil {
		t.Fatalf("CDP call with depth failed: %v", err)
	}

	if len(limited.Nodes) >= len(full.Nodes) {
		t.Errorf("depth-limited tree (%d nodes) should have fewer nodes than full tree (%d nodes)",
			len(limited.Nodes), len(full.Nodes))
	}
}

func TestAXTree_JSONOutput(t *testing.T) {
	page := navigateTo(t, "/")
	result, err := proto.AccessibilityGetFullAXTree{}.Call(page)
	if err != nil {
		t.Fatalf("CDP call failed: %v", err)
	}
	out := formatAXTreeJSON(result.Nodes)
	// Must be valid JSON
	var parsed []interface{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("JSON output is not valid JSON: %v\nOutput:\n%s", err, out[:min(len(out), 500)])
	}
	if len(parsed) == 0 {
		t.Error("JSON output should contain nodes")
	}
}

// =====================
// ax-find tests (RED)
// =====================

func TestAXFind_ByRole(t *testing.T) {
	page := navigateTo(t, "/")
	nodes, err := queryAXNodes(page, "", "button")
	if err != nil {
		t.Fatalf("queryAXNodes failed: %v", err)
	}
	if len(nodes) < 2 {
		t.Fatalf("expected at least 2 buttons, got %d", len(nodes))
	}

	out := formatAXNodeList(nodes)
	if !strings.Contains(out, "Submit") {
		t.Errorf("output should contain 'Submit' button, got:\n%s", out)
	}
	if !strings.Contains(out, "Cancel") {
		t.Errorf("output should contain 'Cancel' button, got:\n%s", out)
	}
}

func TestAXFind_ByName(t *testing.T) {
	page := navigateTo(t, "/")
	nodes, err := queryAXNodes(page, "Submit", "")
	if err != nil {
		t.Fatalf("queryAXNodes failed: %v", err)
	}
	if len(nodes) == 0 {
		t.Fatal("expected at least 1 node named 'Submit', got 0")
	}
	out := formatAXNodeList(nodes)
	if !strings.Contains(out, "Submit") {
		t.Errorf("output should contain 'Submit', got:\n%s", out)
	}
}

func TestAXFind_ByNameAndRoleExact(t *testing.T) {
	page := navigateTo(t, "/")
	// Combining name + role should give exactly one result
	nodes, err := queryAXNodes(page, "Submit", "button")
	if err != nil {
		t.Fatalf("queryAXNodes failed: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected exactly 1 button named 'Submit', got %d", len(nodes))
	}
}

func TestAXFind_ByNameAndRole(t *testing.T) {
	page := navigateTo(t, "/")
	nodes, err := queryAXNodes(page, "About", "link")
	if err != nil {
		t.Fatalf("queryAXNodes failed: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected 1 link named 'About', got %d", len(nodes))
	}
}

func TestAXFind_NoResults(t *testing.T) {
	page := navigateTo(t, "/")
	nodes, err := queryAXNodes(page, "NonexistentThing", "")
	if err != nil {
		t.Fatalf("queryAXNodes failed: %v", err)
	}
	if len(nodes) != 0 {
		t.Errorf("expected 0 results for nonexistent name, got %d", len(nodes))
	}
}

func TestAXFind_FormPage(t *testing.T) {
	page := navigateTo(t, "/form")
	nodes, err := queryAXNodes(page, "", "textbox")
	if err != nil {
		t.Fatalf("queryAXNodes failed: %v", err)
	}
	if len(nodes) < 2 {
		t.Fatalf("expected at least 2 textboxes on form page, got %d", len(nodes))
	}
}

// =====================
// ax-node tests (RED)
// =====================

func TestAXNode_ButtonBySelector(t *testing.T) {
	page := navigateTo(t, "/")
	node, err := getAXNode(page, "#submit-btn")
	if err != nil {
		t.Fatalf("getAXNode failed: %v", err)
	}
	out := formatAXNodeDetail(node)
	if !strings.Contains(out, "button") {
		t.Errorf("should show role 'button', got:\n%s", out)
	}
	if !strings.Contains(out, "Submit") {
		t.Errorf("should show name 'Submit', got:\n%s", out)
	}
}

func TestAXNode_DisabledButton(t *testing.T) {
	page := navigateTo(t, "/")
	node, err := getAXNode(page, "#cancel-btn")
	if err != nil {
		t.Fatalf("getAXNode failed: %v", err)
	}
	out := formatAXNodeDetail(node)
	if !strings.Contains(out, "button") {
		t.Errorf("should show role 'button', got:\n%s", out)
	}
	if !strings.Contains(out, "disabled") {
		t.Errorf("should show disabled property, got:\n%s", out)
	}
}

func TestAXNode_InputWithLabel(t *testing.T) {
	page := navigateTo(t, "/form")
	node, err := getAXNode(page, "#name-input")
	if err != nil {
		t.Fatalf("getAXNode failed: %v", err)
	}
	out := formatAXNodeDetail(node)
	if !strings.Contains(out, "textbox") {
		t.Errorf("should show role 'textbox', got:\n%s", out)
	}
	if !strings.Contains(out, "Name") {
		t.Errorf("should show accessible name 'Name' from label, got:\n%s", out)
	}
}

func TestAXNode_HeadingLevel(t *testing.T) {
	page := navigateTo(t, "/")
	node, err := getAXNode(page, "h1")
	if err != nil {
		t.Fatalf("getAXNode failed: %v", err)
	}
	out := formatAXNodeDetail(node)
	if !strings.Contains(out, "heading") {
		t.Errorf("should show role 'heading', got:\n%s", out)
	}
	if !strings.Contains(out, "level") {
		t.Errorf("should show level property for heading, got:\n%s", out)
	}
}

func TestAXNode_JSONOutput(t *testing.T) {
	page := navigateTo(t, "/")
	node, err := getAXNode(page, "#submit-btn")
	if err != nil {
		t.Fatalf("getAXNode failed: %v", err)
	}
	out := formatAXNodeDetailJSON(node)
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("JSON output is not valid JSON: %v\nOutput:\n%s", err, out)
	}
	if _, ok := parsed["nodeId"]; !ok {
		t.Error("JSON should contain nodeId field")
	}
}

func TestAXNode_SelectorNotFound(t *testing.T) {
	page := navigateTo(t, "/")
	// Use a short timeout so we don't block for 30s waiting for a nonexistent element
	shortPage := page.Timeout(2 * time.Second)
	_, err := getAXNode(shortPage, "#does-not-exist")
	if err == nil {
		t.Error("expected error for nonexistent selector, got nil")
	}
}

// =====================
// file command tests
// =====================

func TestFile_SetFileOnInput(t *testing.T) {
	page := navigateTo(t, "/upload")

	// Create a temp file to upload
	tmp, err := os.CreateTemp("", "rodney-test-*.txt")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmp.Name())
	tmp.Write([]byte("test content"))
	tmp.Close()

	el, err := page.Element("#file-input")
	if err != nil {
		t.Fatalf("element not found: %v", err)
	}
	if err := el.SetFiles([]string{tmp.Name()}); err != nil {
		t.Fatalf("SetFiles failed: %v", err)
	}

	// Wait for the change event to fire and check the file name
	page.MustWaitStable()
	nameEl, err := page.Element("#file-name")
	if err != nil {
		t.Fatalf("file-name element not found: %v", err)
	}
	text, _ := nameEl.Text()
	if text == "" {
		t.Error("expected file name to be set after SetFiles, got empty string")
	}
}

func TestFile_MultipleFiles(t *testing.T) {
	page := navigateTo(t, "/upload")

	tmp1, _ := os.CreateTemp("", "rodney-test1-*.txt")
	defer os.Remove(tmp1.Name())
	tmp1.Write([]byte("file 1"))
	tmp1.Close()

	tmp2, _ := os.CreateTemp("", "rodney-test2-*.txt")
	defer os.Remove(tmp2.Name())
	tmp2.Write([]byte("file 2"))
	tmp2.Close()

	el, err := page.Element("#file-input")
	if err != nil {
		t.Fatalf("element not found: %v", err)
	}

	// Setting files should not error even with multiple files
	if err := el.SetFiles([]string{tmp1.Name(), tmp2.Name()}); err != nil {
		t.Fatalf("SetFiles with multiple files failed: %v", err)
	}
}

// =====================
// download command tests
// =====================

func TestDownload_DataURL(t *testing.T) {
	// Test decoding a data: URL directly
	data, err := decodeDataURL("data:text/plain;base64,SGVsbG8gV29ybGQ=")
	if err != nil {
		t.Fatalf("decodeDataURL failed: %v", err)
	}
	if string(data) != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", string(data))
	}
}

func TestDownload_DataURL_URLEncoded(t *testing.T) {
	data, err := decodeDataURL("data:text/plain,Hello%20World")
	if err != nil {
		t.Fatalf("decodeDataURL failed: %v", err)
	}
	if string(data) != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", string(data))
	}
}

func TestDownload_InferFilename_URL(t *testing.T) {
	name := inferDownloadFilename("https://example.com/images/photo.png")
	if name != "photo.png" {
		t.Errorf("expected 'photo.png', got %q", name)
	}
}

func TestDownload_InferFilename_DataURL(t *testing.T) {
	name := inferDownloadFilename("data:image/png;base64,abc")
	if !strings.HasPrefix(name, "download") || !strings.Contains(name, ".png") {
		t.Errorf("expected 'download*.png', got %q", name)
	}
}

func TestDownload_FetchLink(t *testing.T) {
	page := navigateTo(t, "/download")

	el, err := page.Element("#file-link")
	if err != nil {
		t.Fatalf("element not found: %v", err)
	}
	href := el.MustAttribute("href")
	if href == nil {
		t.Fatal("expected href attribute")
	}

	// Fetch using JS in the page context, same as cmdDownload does
	js := fmt.Sprintf(`async () => {
		const resp = await fetch(%q);
		if (!resp.ok) throw new Error('HTTP ' + resp.status);
		const buf = await resp.arrayBuffer();
		const bytes = new Uint8Array(buf);
		let binary = '';
		for (let i = 0; i < bytes.length; i++) {
			binary += String.fromCharCode(bytes[i]);
		}
		return btoa(binary);
	}`, *href)
	result, err := page.Eval(js)
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}

	data, err := base64.StdEncoding.DecodeString(result.Value.Str())
	if err != nil {
		t.Fatalf("base64 decode failed: %v", err)
	}
	if string(data) != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", string(data))
	}
}

func TestDownload_DataLinkElement(t *testing.T) {
	page := navigateTo(t, "/download")

	el, err := page.Element("#data-link")
	if err != nil {
		t.Fatalf("element not found: %v", err)
	}
	href := el.MustAttribute("href")
	if href == nil {
		t.Fatal("expected href attribute")
	}

	data, err := decodeDataURL(*href)
	if err != nil {
		t.Fatalf("decodeDataURL failed: %v", err)
	}
	if string(data) != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", string(data))
	}
}

func TestDownload_ImgSrc(t *testing.T) {
	page := navigateTo(t, "/download")

	el, err := page.Element("#test-img")
	if err != nil {
		t.Fatalf("element not found: %v", err)
	}
	src := el.MustAttribute("src")
	if src == nil {
		t.Fatal("expected src attribute")
	}
	if *src != "/testfile.txt" {
		t.Errorf("expected '/testfile.txt', got %q", *src)
	}
}

// =====================
// Directory-scoped sessions tests
// =====================

func TestExtractScopeArgs_NoFlags(t *testing.T) {
	mode, remaining := extractScopeArgs([]string{"open", "https://example.com"})
	if mode != scopeAuto {
		t.Errorf("expected scopeAuto, got %v", mode)
	}
	if len(remaining) != 2 || remaining[0] != "open" || remaining[1] != "https://example.com" {
		t.Errorf("expected [open https://example.com], got %v", remaining)
	}
}

func TestExtractScopeArgs_LocalFlag(t *testing.T) {
	mode, remaining := extractScopeArgs([]string{"--local", "start"})
	if mode != scopeLocal {
		t.Errorf("expected scopeLocal, got %v", mode)
	}
	if len(remaining) != 1 || remaining[0] != "start" {
		t.Errorf("expected [start], got %v", remaining)
	}
}

func TestExtractScopeArgs_GlobalFlag(t *testing.T) {
	mode, remaining := extractScopeArgs([]string{"--global", "open", "https://example.com"})
	if mode != scopeGlobal {
		t.Errorf("expected scopeGlobal, got %v", mode)
	}
	if len(remaining) != 2 || remaining[0] != "open" || remaining[1] != "https://example.com" {
		t.Errorf("expected [open https://example.com], got %v", remaining)
	}
}

func TestExtractScopeArgs_LocalFlagAfterCommand(t *testing.T) {
	mode, remaining := extractScopeArgs([]string{"open", "--local", "https://example.com"})
	if mode != scopeLocal {
		t.Errorf("expected scopeLocal, got %v", mode)
	}
	if len(remaining) != 2 || remaining[0] != "open" || remaining[1] != "https://example.com" {
		t.Errorf("expected [open https://example.com], got %v", remaining)
	}
}

func TestExtractScopeArgs_LastFlagWins(t *testing.T) {
	mode, _ := extractScopeArgs([]string{"--local", "--global", "start"})
	if mode != scopeGlobal {
		t.Errorf("expected last flag (scopeGlobal) to win, got %v", mode)
	}
}

func TestResolveStateDir_Global(t *testing.T) {
	dir := resolveStateDir(scopeGlobal, "/some/working/dir")
	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".rodney")
	if dir != expected {
		t.Errorf("expected %q, got %q", expected, dir)
	}
}

func TestResolveStateDir_Local(t *testing.T) {
	dir := resolveStateDir(scopeLocal, "/some/working/dir")
	expected := filepath.Join("/some/working/dir", ".rodney")
	if dir != expected {
		t.Errorf("expected %q, got %q", expected, dir)
	}
}

func TestResolveStateDir_AutoPrefersLocal(t *testing.T) {
	// Create a temp directory with a .rodney/state.json to simulate local session
	tmpDir := t.TempDir()
	localRodney := filepath.Join(tmpDir, ".rodney")
	os.MkdirAll(localRodney, 0755)
	os.WriteFile(filepath.Join(localRodney, "state.json"), []byte(`{}`), 0644)

	dir := resolveStateDir(scopeAuto, tmpDir)
	if dir != localRodney {
		t.Errorf("auto mode should prefer local when .rodney/state.json exists: expected %q, got %q", localRodney, dir)
	}
}

func TestResolveStateDir_AutoFallsBackToGlobal(t *testing.T) {
	// Use a temp directory with NO .rodney/ — should fall back to global
	tmpDir := t.TempDir()
	dir := resolveStateDir(scopeAuto, tmpDir)
	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".rodney")
	if dir != expected {
		t.Errorf("auto mode should fall back to global: expected %q, got %q", expected, dir)
	}
}

func TestResolveStateDir_LocalUsesWorkingDir(t *testing.T) {
	tmpDir := t.TempDir()
	dir := resolveStateDir(scopeLocal, tmpDir)
	expected := filepath.Join(tmpDir, ".rodney")
	if dir != expected {
		t.Errorf("local mode should use working dir: expected %q, got %q", expected, dir)
	}
}

// =====================
// RODNEY_HOME env var tests
// =====================

func TestStateDir_Default(t *testing.T) {
	t.Setenv("RODNEY_HOME", "")
	home, _ := os.UserHomeDir()
	want := home + "/.rodney"
	got := stateDir()
	if got != want {
		t.Errorf("stateDir() = %q, want %q", got, want)
	}
}

func TestStateDir_EnvVar(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RODNEY_HOME", dir)
	got := stateDir()
	if got != dir {
		t.Errorf("stateDir() = %q, want %q", got, dir)
	}
}

func TestMimeToExt(t *testing.T) {
	tests := []struct {
		mime string
		ext  string
	}{
		{"image/png", ".png"},
		{"image/jpeg", ".jpg"},
		{"application/pdf", ".pdf"},
		{"text/plain", ".txt"},
		{"unknown/type", ""},
	}
	for _, tt := range tests {
		got := mimeToExt(tt.mime)
		if got != tt.ext {
			t.Errorf("mimeToExt(%q) = %q, want %q", tt.mime, got, tt.ext)
		}
	}
}

// =====================
// assert command tests
// =====================

func TestAssert_TruthyPass_String(t *testing.T) {
	page := navigateTo(t, "/")
	// document.title is "Test Page" which is truthy
	result, err := page.Eval(`() => { return (document.title); }`)
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	raw := result.Value.JSON("", "")
	// Should not be falsy
	switch raw {
	case "false", "0", "null", "undefined", `""`:
		t.Errorf("document.title should be truthy, got raw=%q", raw)
	}
	if result.Value.Str() != "Test Page" {
		t.Errorf("expected 'Test Page', got %q", result.Value.Str())
	}
}

func TestAssert_TruthyPass_True(t *testing.T) {
	page := navigateTo(t, "/")
	result, err := page.Eval(`() => { return (1 === 1); }`)
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	raw := result.Value.JSON("", "")
	if raw != "true" {
		t.Errorf("1 === 1 should be true, got %q", raw)
	}
}

func TestAssert_TruthyPass_Number(t *testing.T) {
	page := navigateTo(t, "/")
	result, err := page.Eval(`() => { return (42); }`)
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	raw := result.Value.JSON("", "")
	if raw == "0" || raw == "false" || raw == "null" || raw == "undefined" || raw == `""` {
		t.Errorf("42 should be truthy, got raw=%q", raw)
	}
}

func TestAssert_TruthyFail_Null(t *testing.T) {
	page := navigateTo(t, "/")
	result, err := page.Eval(`() => { return (document.querySelector(".nonexistent")); }`)
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	raw := result.Value.JSON("", "")
	if raw != "null" {
		t.Errorf("querySelector for nonexistent should return null, got %q", raw)
	}
}

func TestAssert_TruthyFail_False(t *testing.T) {
	page := navigateTo(t, "/")
	result, err := page.Eval(`() => { return (false); }`)
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	raw := result.Value.JSON("", "")
	if raw != "false" {
		t.Errorf("false should be false, got %q", raw)
	}
}

func TestAssert_TruthyFail_Zero(t *testing.T) {
	page := navigateTo(t, "/")
	result, err := page.Eval(`() => { return (0); }`)
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	raw := result.Value.JSON("", "")
	if raw != "0" {
		t.Errorf("0 should be 0, got %q", raw)
	}
}

func TestAssert_TruthyFail_EmptyString(t *testing.T) {
	page := navigateTo(t, "/")
	result, err := page.Eval(`() => { return (""); }`)
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	raw := result.Value.JSON("", "")
	if raw != `""` {
		t.Errorf("empty string should have JSON repr '\"\"', got %q", raw)
	}
}

func TestAssert_EqualityPass_Title(t *testing.T) {
	page := navigateTo(t, "/")
	result, err := page.Eval(`() => { return (document.title); }`)
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	actual := result.Value.Str()
	if actual != "Test Page" {
		t.Errorf("expected 'Test Page', got %q", actual)
	}
}

func TestAssert_EqualityPass_Count(t *testing.T) {
	page := navigateTo(t, "/")
	result, err := page.Eval(`() => { return (document.querySelectorAll("button").length); }`)
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	raw := result.Value.JSON("", "")
	if raw != "2" {
		t.Errorf("expected 2 buttons, got %q", raw)
	}
}

func TestAssert_EqualityFail_WrongTitle(t *testing.T) {
	page := navigateTo(t, "/")
	result, err := page.Eval(`() => { return (document.title); }`)
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	actual := result.Value.Str()
	if actual == "Wrong Title" {
		t.Error("title should NOT equal 'Wrong Title'")
	}
}

func TestAssert_EqualityPass_BoolString(t *testing.T) {
	page := navigateTo(t, "/")
	result, err := page.Eval(`() => { return (1 === 1); }`)
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	raw := result.Value.JSON("", "")
	if raw != "true" {
		t.Errorf("1 === 1 should produce 'true', got %q", raw)
	}
}

func TestAssert_ValueFormatting_MatchesJSCommand(t *testing.T) {
	// Verify that the value formatting used by assert matches what rodney js outputs
	page := navigateTo(t, "/")

	tests := []struct {
		expr     string
		expected string
	}{
		{`document.title`, "Test Page"},   // string unquoted
		{`1 + 2`, "3"},                    // number
		{`true`, "true"},                  // boolean
		{`null`, "null"},                  // null
		{`document.querySelectorAll("button").length`, "2"}, // number from DOM
	}

	for _, tt := range tests {
		js := fmt.Sprintf(`() => { return (%s); }`, tt.expr)
		result, err := page.Eval(js)
		if err != nil {
			t.Fatalf("eval %q failed: %v", tt.expr, err)
		}

		v := result.Value
		raw := v.JSON("", "")
		var actual string
		switch {
		case raw == "null" || raw == "undefined":
			actual = raw
		case raw == "true" || raw == "false":
			actual = raw
		case len(raw) > 0 && raw[0] == '"':
			actual = v.Str()
		case len(raw) > 0 && (raw[0] == '{' || raw[0] == '['):
			actual = v.JSON("", "  ")
		default:
			actual = raw
		}

		if actual != tt.expected {
			t.Errorf("expr %q: expected %q, got %q (raw=%q)", tt.expr, tt.expected, actual, raw)
		}
	}
}

// =====================
// assert --message tests
// =====================

func TestParseAssertArgs_ExprOnly(t *testing.T) {
	expr, expected, message := parseAssertArgs([]string{"document.title"})
	if expr != "document.title" {
		t.Errorf("expr = %q, want %q", expr, "document.title")
	}
	if expected != nil {
		t.Errorf("expected should be nil, got %q", *expected)
	}
	if message != "" {
		t.Errorf("message should be empty, got %q", message)
	}
}

func TestParseAssertArgs_ExprAndExpected(t *testing.T) {
	expr, expected, message := parseAssertArgs([]string{"document.title", "Dashboard"})
	if expr != "document.title" {
		t.Errorf("expr = %q, want %q", expr, "document.title")
	}
	if expected == nil || *expected != "Dashboard" {
		t.Errorf("expected = %v, want %q", expected, "Dashboard")
	}
	if message != "" {
		t.Errorf("message should be empty, got %q", message)
	}
}

func TestParseAssertArgs_MessageLong(t *testing.T) {
	expr, expected, message := parseAssertArgs([]string{"document.title", "--message", "Page title check"})
	if expr != "document.title" {
		t.Errorf("expr = %q, want %q", expr, "document.title")
	}
	if expected != nil {
		t.Errorf("expected should be nil for truthy with --message, got %q", *expected)
	}
	if message != "Page title check" {
		t.Errorf("message = %q, want %q", message, "Page title check")
	}
}

func TestParseAssertArgs_MessageShort(t *testing.T) {
	expr, expected, message := parseAssertArgs([]string{"document.title", "-m", "Title check"})
	if expr != "document.title" {
		t.Errorf("expr = %q, want %q", expr, "document.title")
	}
	if expected != nil {
		t.Errorf("expected should be nil, got %q", *expected)
	}
	if message != "Title check" {
		t.Errorf("message = %q, want %q", message, "Title check")
	}
}

func TestParseAssertArgs_EqualityWithMessage(t *testing.T) {
	expr, expected, message := parseAssertArgs([]string{"document.title", "Dashboard", "--message", "Wrong page"})
	if expr != "document.title" {
		t.Errorf("expr = %q, want %q", expr, "document.title")
	}
	if expected == nil || *expected != "Dashboard" {
		t.Errorf("expected = %v, want %q", expected, "Dashboard")
	}
	if message != "Wrong page" {
		t.Errorf("message = %q, want %q", message, "Wrong page")
	}
}

func TestParseAssertArgs_MessageBeforeExpr(t *testing.T) {
	// --message can appear anywhere; positional args still work
	expr, expected, message := parseAssertArgs([]string{"-m", "Check", "document.title", "Home"})
	if expr != "document.title" {
		t.Errorf("expr = %q, want %q", expr, "document.title")
	}
	if expected == nil || *expected != "Home" {
		t.Errorf("expected = %v, want %q", expected, "Home")
	}
	if message != "Check" {
		t.Errorf("message = %q, want %q", message, "Check")
	}
}

func TestFormatAssertFail_TruthyNoMessage(t *testing.T) {
	got := formatAssertFail("null", nil, "")
	if got != "fail: got null" {
		t.Errorf("got %q, want %q", got, "fail: got null")
	}
}

func TestFormatAssertFail_TruthyWithMessage(t *testing.T) {
	got := formatAssertFail("null", nil, "User should be logged in")
	if got != "fail: User should be logged in (got null)" {
		t.Errorf("got %q, want %q", got, "fail: User should be logged in (got null)")
	}
}

func TestFormatAssertFail_EqualityNoMessage(t *testing.T) {
	expected := "Dashboard"
	got := formatAssertFail("Task Tracker", &expected, "")
	want := `fail: got "Task Tracker", expected "Dashboard"`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatAssertFail_EqualityWithMessage(t *testing.T) {
	expected := "Dashboard"
	got := formatAssertFail("Task Tracker", &expected, "Wrong page loaded")
	want := `fail: Wrong page loaded (got "Task Tracker", expected "Dashboard")`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ======================
// cmdJS stdin tests
// ======================

// setupCmdJSState navigates to path on the test server, writes a state.json
// in a temp dir pointing at that page, and restores activeStateDir on cleanup.
func setupCmdJSState(t *testing.T, path string) {
	t.Helper()

	tmpDir := t.TempDir()
	oldStateDir := activeStateDir
	activeStateDir = tmpDir
	t.Cleanup(func() { activeStateDir = oldStateDir })

	page := env.browser.MustPage(env.server.URL + path)
	page.MustWaitLoad()
	t.Cleanup(func() { page.MustClose() })

	// Find the page's index in the browser's page list.
	pages, err := env.browser.Pages()
	if err != nil {
		t.Fatalf("failed to list pages: %v", err)
	}
	idx := 0
	for i, p := range pages {
		if p.TargetID == page.TargetID {
			idx = i
			break
		}
	}

	if err := saveState(&State{DebugURL: env.debugURL, ActivePage: idx}); err != nil {
		t.Fatalf("saveState: %v", err)
	}
}

// pipeStdin replaces os.Stdin with a pipe containing content and restores it on cleanup.
func pipeStdin(t *testing.T, content string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	if _, err := w.WriteString(content); err != nil {
		t.Fatalf("pipeStdin write: %v", err)
	}
	w.Close()
	oldStdin := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = oldStdin })
}

// captureStdout captures everything written to os.Stdout by fn, trimming trailing whitespace.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = oldStdout
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("captureStdout read: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// TestCmdJS_Stdin_NoArgs verifies that piping an expression to `rodney js`
// with no arguments reads and evaluates it from stdin.
func TestCmdJS_Stdin_NoArgs(t *testing.T) {
	setupCmdJSState(t, "/")
	pipeStdin(t, "document.title\n")
	got := captureStdout(t, func() { cmdJS([]string{}) })
	if got != "Test Page" {
		t.Errorf("expected 'Test Page', got %q", got)
	}
}

// TestCmdJS_Stdin_DashArg verifies that `rodney js -` reads the expression from stdin,
// consistent with the `-` convention used by `rodney file`.
func TestCmdJS_Stdin_DashArg(t *testing.T) {
	setupCmdJSState(t, "/")
	pipeStdin(t, "document.title\n")
	got := captureStdout(t, func() { cmdJS([]string{"-"}) })
	if got != "Test Page" {
		t.Errorf("expected 'Test Page', got %q", got)
	}
}

// TestCmdJS_Stdin_MultiLine verifies multi-line input works — this is exactly what
// a heredoc produces (bash sends the lines as a single stdin stream with newlines).
func TestCmdJS_Stdin_MultiLine(t *testing.T) {
	setupCmdJSState(t, "/")
	// Heredoc-style: expression split across lines with trailing newline.
	// `1 +\n2\n` is trimmed to `1 +\n2` and wrapped in `() => { return (1 +\n2); }`.
	pipeStdin(t, "1 +\n2\n")
	got := captureStdout(t, func() { cmdJS([]string{}) })
	if got != "3" {
		t.Errorf("expected '3', got %q", got)
	}
}

// TestCmdJS_Stdin_TrimsWhitespace verifies that leading/trailing whitespace
// (including the trailing newline added by echo or heredoc) is stripped.
func TestCmdJS_Stdin_TrimsWhitespace(t *testing.T) {
	setupCmdJSState(t, "/")
	pipeStdin(t, "  1 + 2  \n")
	got := captureStdout(t, func() { cmdJS([]string{}) })
	if got != "3" {
		t.Errorf("expected '3', got %q", got)
	}
}

// ======================
// cmdAssert stdin tests
// ======================

// TestCmdAssert_Stdin_NoArgs verifies that piping a JS expression to `rodney assert`
// with no other args reads the expression from stdin.
func TestCmdAssert_Stdin_NoArgs(t *testing.T) {
	pipeStdin(t, "document.title\n")
	got := resolveAssertArgs([]string{})
	if len(got) == 0 || got[0] != "document.title" {
		t.Errorf("expected args[0] == 'document.title', got %v", got)
	}
}

// TestCmdAssert_Stdin_DashArg verifies that `rodney assert -` reads the expression
// from stdin explicitly, matching the `-` convention used by `rodney js` and `rodney file`.
func TestCmdAssert_Stdin_DashArg(t *testing.T) {
	pipeStdin(t, "document.title\n")
	got := resolveAssertArgs([]string{"-"})
	if len(got) == 0 || got[0] != "document.title" {
		t.Errorf("expected args[0] == 'document.title', got %v", got)
	}
}

// TestCmdAssert_Stdin_WithExpected verifies that the expression comes from stdin
// while the expected value still comes from command-line args.
// Equivalent to: echo "document.title" | rodney assert - "Test Page"
func TestCmdAssert_Stdin_WithExpected(t *testing.T) {
	pipeStdin(t, "document.title\n")
	got := resolveAssertArgs([]string{"-", "Test Page"})
	if len(got) != 2 || got[0] != "document.title" || got[1] != "Test Page" {
		t.Errorf("expected [document.title Test Page], got %v", got)
	}
}

// TestCmdAssert_Stdin_WithMessage verifies that the expression comes from stdin
// while the -m flag still comes from command-line args.
// Equivalent to: echo "document.title" | rodney assert - -m "page title"
func TestCmdAssert_Stdin_WithMessage(t *testing.T) {
	pipeStdin(t, "document.title\n")
	got := resolveAssertArgs([]string{"-", "-m", "page title"})
	if len(got) != 3 || got[0] != "document.title" || got[1] != "-m" || got[2] != "page title" {
		t.Errorf("expected [document.title -m page title], got %v", got)
	}
}

// TestCmdAssert_Stdin_FlagsOnly verifies that when only flags are given (no positional)
// and stdin is piped, the expression is prepended from stdin.
func TestCmdAssert_Stdin_FlagsOnly(t *testing.T) {
	pipeStdin(t, "document.title\n")
	got := resolveAssertArgs([]string{"-m", "check"})
	if len(got) != 3 || got[0] != "document.title" || got[1] != "-m" || got[2] != "check" {
		t.Errorf("expected [document.title -m check], got %v", got)
	}
}

// TestCmdAssert_Stdin_Passthrough verifies that normal (non-stdin) args are unchanged.
func TestCmdAssert_Stdin_Passthrough(t *testing.T) {
	got := resolveAssertArgs([]string{"document.title", "Test Page"})
	if len(got) != 2 || got[0] != "document.title" || got[1] != "Test Page" {
		t.Errorf("expected [document.title Test Page], got %v", got)
	}
}

// TestCmdAssert_Stdin_TrimsWhitespace verifies leading/trailing whitespace is stripped
// from the stdin expression (consistent with cmdJS behavior).
func TestCmdAssert_Stdin_TrimsWhitespace(t *testing.T) {
	pipeStdin(t, "  1 + 2  \n")
	got := resolveAssertArgs([]string{"-"})
	if len(got) == 0 || got[0] != "1 + 2" {
		t.Errorf("expected args[0] == '1 + 2', got %v", got)
	}
}

func TestInsecureFlag_WithSelfSignedCert(t *testing.T) {
	// Create HTTPS server with self-signed certificate
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html>
<html><head><title>Secure Test</title></head>
<body><h1>HTTPS Test Page</h1></body></html>`))
	})
	httpsServer := httptest.NewUnstartedServer(mux)
	// Suppress expected TLS handshake errors to keep test output clean
	httpsServer.Config.ErrorLog = log.New(io.Discard, "", 0)
	httpsServer.StartTLS()
	defer httpsServer.Close()

	// Test 1: Browser WITHOUT --ignore-certificate-errors should fail
	t.Run("WithoutInsecureFlag", func(t *testing.T) {
		l := launcher.New().
			Set("no-sandbox").
			Set("disable-gpu").
			Set("single-process").
			Headless(true).
			Leakless(false)

		if bin := os.Getenv("ROD_CHROME_BIN"); bin != "" {
			l = l.Bin(bin)
		}

		u := l.MustLaunch()
		browser := rod.New().ControlURL(u).MustConnect()
		defer browser.MustClose()

		page := browser.MustPage("")
		defer page.MustClose()

		err := page.Navigate(httpsServer.URL)
		if err == nil {
			t.Fatal("expected ERR_CERT_AUTHORITY_INVALID error, but navigation succeeded")
		}
		if !strings.Contains(err.Error(), "ERR_CERT_AUTHORITY_INVALID") {
			t.Errorf("expected ERR_CERT_AUTHORITY_INVALID, got: %v", err)
		}
	})

	// Test 2: Browser WITH --ignore-certificate-errors should succeed
	t.Run("WithInsecureFlag", func(t *testing.T) {
		l := launcher.New().
			Set("no-sandbox").
			Set("disable-gpu").
			Set("single-process").
			Set("ignore-certificate-errors"). // This is what --insecure sets
			Headless(true).
			Leakless(false)

		if bin := os.Getenv("ROD_CHROME_BIN"); bin != "" {
			l = l.Bin(bin)
		}

		u := l.MustLaunch()
		browser := rod.New().ControlURL(u).MustConnect()
		defer browser.MustClose()

		// Try to navigate to HTTPS server with invalid cert
		page := browser.MustPage(httpsServer.URL)
		defer page.MustClose()

		page.MustWaitLoad()
		title := page.MustInfo().Title

		if title != "Secure Test" {
			t.Errorf("expected page to load successfully with title 'Secure Test', got %q", title)
		}
	})
}

// =====================
<<<<<<< HEAD
// logs command tests
// =====================

// collectConsoleMsgs enables the Runtime domain, emits js, collects up to
// maxCount events (or waits timeout), and returns the collected entries.
func collectConsoleMsgs(page *rod.Page, js string, maxCount int, timeout time.Duration) (texts []string, levels []string) {
	var mu sync.Mutex
	done := make(chan struct{})
	var once sync.Once
	closeDone := func() { once.Do(func() { close(done) }) }

	wait := page.EachEvent(func(e *proto.RuntimeConsoleAPICalled) bool {
		mu.Lock()
		texts = append(texts, formatConsoleArgs(e.Args))
		levels = append(levels, consoleTypeToLevel(e.Type))
		n := len(texts)
		mu.Unlock()
		if n >= maxCount {
			closeDone()
			return true // stop
		}
		return false
	})

	(proto.RuntimeEnable{}).Call(page) //nolint
	page.MustEval(js)

	go func() {
		wait()
		closeDone()
	}()

	select {
	case <-done:
	case <-time.After(timeout):
	}
	return
}

func TestLogs_SnapshotCapture(t *testing.T) {
	page := navigateTo(t, "/")

	texts, _ := collectConsoleMsgs(page, `() => {
		console.log("info message from logs test");
		console.warn("warning message from logs test");
		console.error("error message from logs test");
	}`, 3, 3*time.Second)

	if len(texts) < 3 {
		t.Fatalf("expected at least 3 log entries, got %d: %v", len(texts), texts)
	}

	found := false
	for _, text := range texts {
		if strings.Contains(text, "info message from logs test") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'info message from logs test' in entries, got: %v", texts)
	}
}

func TestLogs_ConsoleTypes(t *testing.T) {
	page := navigateTo(t, "/")

	_, levels := collectConsoleMsgs(page, `() => {
		console.warn("warning entry for level test");
		console.error("error entry for level test");
	}`, 2, 3*time.Second)

	levelSet := make(map[string]bool)
	for _, l := range levels {
		levelSet[l] = true
	}

	if !levelSet["warning"] {
		t.Errorf("expected a warning-level entry, got levels: %v", levels)
	}
	if !levelSet["error"] {
		t.Errorf("expected an error-level entry, got levels: %v", levels)
	}
}

func TestLogs_FormatLogLevel(t *testing.T) {
	tests := []struct {
		level    proto.LogLogEntryLevel
		expected string
	}{
		{proto.LogLogEntryLevelVerbose, "verbose"},
		{proto.LogLogEntryLevelInfo, "info"},
		{proto.LogLogEntryLevelWarning, "warning"},
		{proto.LogLogEntryLevelError, "error"},
		{proto.LogLogEntryLevel("custom"), "custom"},
	}
	for _, tt := range tests {
		got := formatLogLevel(tt.level)
		if got != tt.expected {
			t.Errorf("formatLogLevel(%q) = %q, want %q", tt.level, got, tt.expected)
		}
	}
}

func TestLogs_ConsoleTypeToLevel(t *testing.T) {
	tests := []struct {
		ct       proto.RuntimeConsoleAPICalledType
		expected string
	}{
		{proto.RuntimeConsoleAPICalledTypeDebug, "verbose"},
		{proto.RuntimeConsoleAPICalledTypeLog, "info"},
		{proto.RuntimeConsoleAPICalledTypeInfo, "info"},
		{proto.RuntimeConsoleAPICalledTypeWarning, "warning"},
		{proto.RuntimeConsoleAPICalledTypeError, "error"},
		{proto.RuntimeConsoleAPICalledTypeAssert, "error"},
		{proto.RuntimeConsoleAPICalledTypeDir, "info"},
	}
	for _, tt := range tests {
		got := consoleTypeToLevel(tt.ct)
		if got != tt.expected {
			t.Errorf("consoleTypeToLevel(%q) = %q, want %q", tt.ct, got, tt.expected)
		}
	}
}

func TestLogs_ScanLogFile(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "test.ndjson")

	content := `{"level":"info","source":"javascript","text":"hello","timestamp":"2024-01-01T12:00:00.000Z"}
{"level":"warning","source":"javascript","text":"world","timestamp":"2024-01-01T12:00:01.000Z"}
`
	if err := os.WriteFile(logFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write log file: %v", err)
	}

	var lines []string
	scanLogFile(logFile, func(line string) { lines = append(lines, line) })
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(lines), lines)
	}

	var obj struct {
		Level string `json:"level"`
		Text  string `json:"text"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &obj); err != nil {
		t.Fatalf("failed to unmarshal line 0: %v", err)
	}
	if obj.Level != "info" || obj.Text != "hello" {
		t.Errorf("line 0: got level=%q text=%q, want level=%q text=%q", obj.Level, obj.Text, "info", "hello")
	}

	if err := json.Unmarshal([]byte(lines[1]), &obj); err != nil {
		t.Fatalf("failed to unmarshal line 1: %v", err)
	}
	if obj.Level != "warning" || obj.Text != "world" {
		t.Errorf("line 1: got level=%q text=%q, want level=%q text=%q", obj.Level, obj.Text, "warning", "world")
	}
}

// =====================
// discover command tests
// =====================

func handleDiscover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(`<!DOCTYPE html>
<html lang="en">
<head><title>Discover Page</title></head>
<body>
  <h1 data-testid="heading">Dashboard</h1>
  <p data-testid="status">All systems operational</p>
  <input data-testid="search" type="text" placeholder="Search...">
  <textarea data-testid="notes" placeholder="Notes"></textarea>
  <button data-testid="submit-btn">Submit</button>
  <a data-testid="help-link" href="/help">Help</a>
  <select data-testid="filter">
    <option value="all">All</option>
    <option value="active">Active</option>
  </select>
  <div data-testid="hidden-el" style="display:none">Hidden content</div>
  <table data-testid="results-table">
    <thead><tr><th>Name</th><th>Status</th></tr></thead>
    <tbody><tr><td>Item 1</td><td>OK</td></tr><tr><td>Item 2</td><td>Fail</td></tr></tbody>
  </table>
  <span data-custom="custom-val">Custom attr element</span>
</body>
</html>`))
}

func TestDiscover_FindsTestIDElements(t *testing.T) {
	page := navigateTo(t, "/discover")
	entries, err := queryDiscoverEntries(page, "data-testid")
	if err != nil {
		t.Fatalf("queryDiscoverEntries failed: %v", err)
	}
	if len(entries) < 8 {
		t.Fatalf("expected at least 8 entries, got %d", len(entries))
	}
}

func TestDiscover_ButtonAction(t *testing.T) {
	page := navigateTo(t, "/discover")
	entries, err := queryDiscoverEntries(page, "data-testid")
	if err != nil {
		t.Fatalf("queryDiscoverEntries failed: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.ID == "submit-btn" {
			found = true
			if e.Action != "click" {
				t.Errorf("button action should be 'click', got %q", e.Action)
			}
			if e.Tag != "button" {
				t.Errorf("button tag should be 'button', got %q", e.Tag)
			}
			if e.Text != "Submit" {
				t.Errorf("button text should be 'Submit', got %q", e.Text)
			}
		}
	}
	if !found {
		t.Error("submit-btn not found in discover entries")
	}
}

func TestDiscover_InputAction(t *testing.T) {
	page := navigateTo(t, "/discover")
	entries, err := queryDiscoverEntries(page, "data-testid")
	if err != nil {
		t.Fatalf("queryDiscoverEntries failed: %v", err)
	}
	for _, e := range entries {
		if e.ID == "search" {
			if e.Action != "input" {
				t.Errorf("input action should be 'input', got %q", e.Action)
			}
			if e.Text != "Search..." {
				t.Errorf("input text should be placeholder 'Search...', got %q", e.Text)
			}
			return
		}
	}
	t.Error("search input not found in discover entries")
}

func TestDiscover_TextareaAction(t *testing.T) {
	page := navigateTo(t, "/discover")
	entries, err := queryDiscoverEntries(page, "data-testid")
	if err != nil {
		t.Fatalf("queryDiscoverEntries failed: %v", err)
	}
	for _, e := range entries {
		if e.ID == "notes" {
			if e.Action != "input" {
				t.Errorf("textarea action should be 'input', got %q", e.Action)
			}
			return
		}
	}
	t.Error("notes textarea not found in discover entries")
}

func TestDiscover_LinkAction(t *testing.T) {
	page := navigateTo(t, "/discover")
	entries, err := queryDiscoverEntries(page, "data-testid")
	if err != nil {
		t.Fatalf("queryDiscoverEntries failed: %v", err)
	}
	for _, e := range entries {
		if e.ID == "help-link" {
			if e.Action != "click" {
				t.Errorf("link action should be 'click', got %q", e.Action)
			}
			if !strings.Contains(e.Text, "/help") {
				t.Errorf("link text should contain href '/help', got %q", e.Text)
			}
			return
		}
	}
	t.Error("help-link not found in discover entries")
}

func TestDiscover_SelectAction(t *testing.T) {
	page := navigateTo(t, "/discover")
	entries, err := queryDiscoverEntries(page, "data-testid")
	if err != nil {
		t.Fatalf("queryDiscoverEntries failed: %v", err)
	}
	for _, e := range entries {
		if e.ID == "filter" {
			if e.Action != "select" {
				t.Errorf("select action should be 'select', got %q", e.Action)
			}
			if !strings.Contains(e.Text, "All") || !strings.Contains(e.Text, "Active") {
				t.Errorf("select text should list options, got %q", e.Text)
			}
			return
		}
	}
	t.Error("filter select not found in discover entries")
}

func TestDiscover_TableAction(t *testing.T) {
	page := navigateTo(t, "/discover")
	entries, err := queryDiscoverEntries(page, "data-testid")
	if err != nil {
		t.Fatalf("queryDiscoverEntries failed: %v", err)
	}
	for _, e := range entries {
		if e.ID == "results-table" {
			if e.Action != "text" {
				t.Errorf("table action should be 'text', got %q", e.Action)
			}
			if !strings.Contains(e.Text, "Name") || !strings.Contains(e.Text, "Status") {
				t.Errorf("table text should contain headers, got %q", e.Text)
			}
			if !strings.Contains(e.Text, "2 rows") {
				t.Errorf("table text should contain row count, got %q", e.Text)
			}
			return
		}
	}
	t.Error("results-table not found in discover entries")
}

func TestDiscover_HiddenElement(t *testing.T) {
	page := navigateTo(t, "/discover")
	entries, err := queryDiscoverEntries(page, "data-testid")
	if err != nil {
		t.Fatalf("queryDiscoverEntries failed: %v", err)
	}
	for _, e := range entries {
		if e.ID == "hidden-el" {
			if e.Visible {
				t.Error("hidden element should have Visible=false")
			}
			return
		}
	}
	t.Error("hidden-el not found in discover entries")
}

func TestDiscover_CustomAttr(t *testing.T) {
	page := navigateTo(t, "/discover")
	entries, err := queryDiscoverEntries(page, "data-custom")
	if err != nil {
		t.Fatalf("queryDiscoverEntries failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry with data-custom, got %d", len(entries))
	}
	if entries[0].ID != "custom-val" {
		t.Errorf("expected id 'custom-val', got %q", entries[0].ID)
	}
}

func TestDiscover_EmptyPage(t *testing.T) {
	page := navigateTo(t, "/empty")
	entries, err := queryDiscoverEntries(page, "data-testid")
	if err != nil {
		t.Fatalf("queryDiscoverEntries failed: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries on empty page, got %d", len(entries))
	}
}

func TestDiscover_FormatTextGrouping(t *testing.T) {
	page := navigateTo(t, "/discover")
	entries, err := queryDiscoverEntries(page, "data-testid")
	if err != nil {
		t.Fatalf("queryDiscoverEntries failed: %v", err)
	}
	out := formatDiscoverText(entries, "data-testid", "http://example.com/discover")
	if !strings.Contains(out, "Readable:") {
		t.Error("output should contain 'Readable:' group")
	}
	if !strings.Contains(out, "Interactive:") {
		t.Error("output should contain 'Interactive:' group")
	}
	if !strings.Contains(out, "Hidden:") {
		t.Error("output should contain 'Hidden:' group")
	}
	if !strings.Contains(out, "Page: http://example.com/discover") {
		t.Error("output should contain page URL")
	}
}

func TestDiscover_FormatTextCommands(t *testing.T) {
	page := navigateTo(t, "/discover")
	entries, err := queryDiscoverEntries(page, "data-testid")
	if err != nil {
		t.Fatalf("queryDiscoverEntries failed: %v", err)
	}
	out := formatDiscoverText(entries, "data-testid", "")
	if !strings.Contains(out, `rodney click '[data-testid="submit-btn"]'`) {
		t.Errorf("output should suggest click command for button, got:\n%s", out)
	}
	if !strings.Contains(out, `rodney input '[data-testid="search"]'`) {
		t.Errorf("output should suggest input command for text input, got:\n%s", out)
	}
	if !strings.Contains(out, `rodney select '[data-testid="filter"]'`) {
		t.Errorf("output should suggest select command for dropdown, got:\n%s", out)
	}
	if !strings.Contains(out, `rodney text '[data-testid="heading"]'`) {
		t.Errorf("output should suggest text command for heading, got:\n%s", out)
	}
}

func TestDiscover_JSONOutput(t *testing.T) {
	page := navigateTo(t, "/discover")
	entries, err := queryDiscoverEntries(page, "data-testid")
	if err != nil {
		t.Fatalf("queryDiscoverEntries failed: %v", err)
	}
	out, jsonErr := json.MarshalIndent(entries, "", "  ")
	if jsonErr != nil {
		t.Fatalf("JSON marshal failed: %v", jsonErr)
	}
	var parsed []discoverEntry
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("JSON round-trip failed: %v", err)
	}
	if len(parsed) != len(entries) {
		t.Errorf("JSON round-trip: expected %d entries, got %d", len(entries), len(parsed))
	}
}

// =====================
// parseStartFlags tests
// =====================

func TestParseStartFlags_ShowFlag(t *testing.T) {
	flags, err := parseStartFlags([]string{"--show"})
	if err != nil {
		t.Fatalf("--show should be accepted, got error: %v", err)
	}
	if flags.headless {
		t.Error("expected headless=false when --show is passed")
	}
}

func TestParseStartFlags_ShowAndInsecure(t *testing.T) {
	flags, err := parseStartFlags([]string{"--show", "--insecure"})
	if err != nil {
		t.Fatalf("--show --insecure should be accepted, got error: %v", err)
	}
	if flags.headless {
		t.Error("expected headless=false when --show is passed")
	}
	if !flags.ignoreCertErrors {
		t.Error("expected ignoreCertErrors=true when --insecure is passed")
	}
}

func TestParseStartFlags_InsecureOnly(t *testing.T) {
	flags, err := parseStartFlags([]string{"--insecure"})
	if err != nil {
		t.Fatalf("--insecure should be accepted, got error: %v", err)
	}
	if !flags.headless {
		t.Error("expected headless=true (default) when --show is not passed")
	}
	if !flags.ignoreCertErrors {
		t.Error("expected ignoreCertErrors=true when --insecure is passed")
	}
}

func TestParseStartFlags_KShorthand(t *testing.T) {
	flags, err := parseStartFlags([]string{"-k"})
	if err != nil {
		t.Fatalf("-k should be accepted, got error: %v", err)
	}
	if !flags.ignoreCertErrors {
		t.Error("expected ignoreCertErrors=true when -k is passed")
	}
}

func TestParseStartFlags_NoArgs(t *testing.T) {
	flags, err := parseStartFlags([]string{})
	if err != nil {
		t.Fatalf("no args should be accepted, got error: %v", err)
	}
	if !flags.headless {
		t.Error("expected headless=true by default")
	}
	if flags.ignoreCertErrors {
		t.Error("expected ignoreCertErrors=false by default")
	}
}

func TestParseStartFlags_UnknownFlag(t *testing.T) {
	_, err := parseStartFlags([]string{"--bogus"})
	if err == nil {
		t.Fatal("expected error for unknown flag --bogus")
	}
	if !strings.Contains(err.Error(), "unknown flag: --bogus") {
		t.Errorf("expected 'unknown flag: --bogus' in error, got: %v", err)
	}
}
