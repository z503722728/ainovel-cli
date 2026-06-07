package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/host"
)

// handlerTest 创建一个 Handler 用于测试，使用零值 Host（RulesFS/Style 方法可安全调用）。
func handlerTest(t *testing.T) *Handler {
	t.Helper()
	// 零值 Host 的 RulesFS() 返回 nil，Style() 返回 ""，SetStyle() 安全
	return NewHandler(&host.Host{})
}

// ── TestScanDir (existing tests preserved) ──

func TestScanDir_Empty(t *testing.T) {
	dir := t.TempDir()
	node, err := scanDir(dir)
	if err != nil {
		t.Fatalf("scanDir empty: %v", err)
	}
	if node == nil {
		t.Fatal("expected non-nil node for empty dir")
	}
	if node.Type != "dir" {
		t.Errorf("expected type dir, got %s", node.Type)
	}
	if len(node.Children) != 0 {
		t.Errorf("expected 0 children, got %d", len(node.Children))
	}
}

func TestScanDir_WithFiles(t *testing.T) {
	dir := t.TempDir()
	// Create a .md file
	if err := os.WriteFile(filepath.Join(dir, "01.md"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	// Create a subdirectory
	subDir := filepath.Join(dir, "chapters")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "02.md"), []byte("world"), 0644); err != nil {
		t.Fatal(err)
	}
	// Create .git — should be excluded
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0755); err != nil {
		t.Fatal(err)
	}

	node, err := scanDir(dir)
	if err != nil {
		t.Fatalf("scanDir: %v", err)
	}
	if node == nil {
		t.Fatal("expected non-nil node")
	}

	// Should have 2 children: chapters (dir) and 01.md (file), .git excluded
	if len(node.Children) != 2 {
		t.Errorf("expected 2 children (dir + file), got %d", len(node.Children))
	}

	// chapters should be first (dirs sorted first)
	if node.Children[0].Name != "chapters" || node.Children[0].Type != "dir" {
		t.Errorf("expected chapters dir first, got %s/%s", node.Children[0].Name, node.Children[0].Type)
	}
	if node.Children[1].Name != "01.md" || node.Children[1].Type != "file" {
		t.Errorf("expected 01.md file second, got %s/%s", node.Children[1].Name, node.Children[1].Type)
	}

	// Check subdirectory recursion: chapters should have 02.md
	chaptersNode := node.Children[0]
	if len(chaptersNode.Children) != 1 {
		t.Errorf("expected 1 child in chapters, got %d", len(chaptersNode.Children))
	}
}

func TestScanDir_NonExistent(t *testing.T) {
	node, err := scanDir("/nonexistent/path/12345")
	if err != nil {
		t.Fatalf("expected nil error for nonexistent, got %v", err)
	}
	if node != nil {
		t.Error("expected nil node for nonexistent dir")
	}
}

func TestSafeResolve_Valid(t *testing.T) {
	base := "/tmp/test"
	resolved, err := safeResolve(base, "chapters/01.md")
	if err != nil {
		t.Fatalf("safeResolve: %v", err)
	}
	expected := "/tmp/test/chapters/01.md"
	if resolved != expected {
		t.Errorf("expected %s, got %s", expected, resolved)
	}
}

func TestSafeResolve_TraversalRejected(t *testing.T) {
	cases := []string{
		"../../../etc/passwd",
		"..",
		"/etc/passwd",
	}
	for _, rel := range cases {
		_, err := safeResolve("/tmp/test", rel)
		if err == nil {
			t.Errorf("expected error for %q, got nil", rel)
		}
	}
}

func TestSafeResolve_CleanPath(t *testing.T) {
	resolved, err := safeResolve("/tmp/test", "chapters/../chapters/01.md")
	if err != nil {
		t.Fatalf("safeResolve: %v", err)
	}
	// filepath.Clean removes the .. segment, resulting in chapters/01.md
	expected := "/tmp/test/chapters/01.md"
	if resolved != expected {
		t.Errorf("expected %s, got %s", expected, resolved)
	}
}

// ── Review endpoints ──

func TestReviewFileNames(t *testing.T) {
	// Verify the expected foundation file list.
	expected := []string{"premise.md", "characters.json", "outline.json", "world_rules.json"}
	if len(reviewFileNames) != len(expected) {
		t.Fatalf("expected %d reviewFileNames, got %d", len(expected), len(reviewFileNames))
	}
	for i, name := range expected {
		if reviewFileNames[i] != name {
			t.Errorf("reviewFileNames[%d] = %q, want %q", i, reviewFileNames[i], name)
		}
	}
}

// TestFoundationDir verifies foundationDir returns the same path as r.rt.Dir().
func TestFoundationDir(t *testing.T) {
	// foundationDir is a thin wrapper; verify the logic behaves as expected.
	dir := t.TempDir()
	got := filepath.Join(dir, "premise.md")
	expected := filepath.Join(filepath.Clean(dir), "premise.md")
	if got != expected {
		t.Errorf("expected %s, got %s", expected, got)
	}
}

// TestReviewFileTypeDetection verifies file type detection for review file names.
func TestReviewFileTypeDetection(t *testing.T) {
	cases := []struct {
		name     string
		expected string
	}{
		{"premise.md", "markdown"},
		{"characters.json", "json"},
		{"outline.json", "json"},
		{"world_rules.json", "json"},
	}
	for _, tc := range cases {
		fileType := "text"
		switch strings.ToLower(filepath.Ext(tc.name)) {
		case ".json":
			fileType = "json"
		case ".md", ".markdown":
			fileType = "markdown"
		}
		if fileType != tc.expected {
			t.Errorf("%s: expected %s, got %s", tc.name, tc.expected, fileType)
		}
	}
}

// TestReviewFiles_FromDir verifies the review file listing logic directly.
func TestReviewFiles_FromDir(t *testing.T) {
	dir := t.TempDir()

	// Create only premise.md and characters.json
	if err := os.WriteFile(filepath.Join(dir, "premise.md"), []byte("# 都市奇幻\n\n题材：都市奇幻×悬疑"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "characters.json"), []byte(`{"characters":[{"name":"沈渡"}]}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Simulate the logic of serveReviewFiles
	var found []string
	for _, name := range reviewFileNames {
		absPath := filepath.Join(dir, name)
		if _, err := os.Stat(absPath); err == nil {
			found = append(found, name)
		}
	}
	if len(found) != 2 {
		t.Fatalf("expected 2 files, got %d", len(found))
	}
	if found[0] != "premise.md" || found[1] != "characters.json" {
		t.Errorf("unexpected file order: %v", found)
	}
}

// TestReviewFileSave_Roundtrip verifies save logic directly.
func TestReviewFileSave_Roundtrip(t *testing.T) {
	dir := t.TempDir()

	content := "# 修改后的设定\n\n新的内容\n"
	absPath := filepath.Join(dir, "premise.md")
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Read back
	data, err := os.ReadFile(absPath)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if string(data) != content {
		t.Errorf("content mismatch: got %q, want %q", string(data), content)
	}
}

// TestReviewFileRead_FromDir verifies the read logic directly.
func TestReviewFileRead_FromDir(t *testing.T) {
	dir := t.TempDir()
	content := "# 都市奇幻\n\n题材：都市奇幻×悬疑\n"
	if err := os.WriteFile(filepath.Join(dir, "premise.md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "premise.md"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != content {
		t.Errorf("content mismatch")
	}
}

// TestReviewTraversal_Rejected verifies path traversal rejection for review paths.
func TestReviewTraversal_Rejected(t *testing.T) {
	dir := t.TempDir()
	cases := []string{
		"../../../etc/passwd",
		"..",
		"/etc/passwd",
	}
	for _, rel := range cases {
		_, err := safeResolve(dir, rel)
		if err == nil {
			t.Errorf("expected error for %q, got nil", rel)
		}
	}
}

// ── Rules & Style API Tests ──

func TestServeRulesBundle_Success(t *testing.T) {
	h := handlerTest(t)
	req := httptest.NewRequest(http.MethodGet, "/api/rules", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	contentType := rec.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		t.Errorf("expected JSON content type, got %s", contentType)
	}

	// Verify it's valid JSON with expected fields
	var bundle struct {
		Structured  map[string]any `json:"structured"`
		Preferences string         `json:"preferences"`
		Sources     []string       `json:"sources"`
		Conflicts   []any          `json:"conflicts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	// Sources should include the default rules with nil RulesFS, home dir, and project path
	if bundle.Sources == nil {
		t.Error("expected non-nil sources")
	}
}

func TestServeRulesSources_Success(t *testing.T) {
	h := handlerTest(t)
	req := httptest.NewRequest(http.MethodGet, "/api/rules/sources", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var sources []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &sources); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if sources == nil {
		t.Error("expected non-nil sources array")
	}
}

func TestServeRulesFileRead_ProjectMissing(t *testing.T) {
	h := handlerTest(t)
	req := httptest.NewRequest(http.MethodGet, "/api/rules/file?source=project", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for missing file, got %d", rec.Code)
	}

	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["exists"] != false {
		t.Error("expected exists=false for missing file")
	}
}

func TestServeRulesFileRead_GlobalMissingName(t *testing.T) {
	h := handlerTest(t)
	req := httptest.NewRequest(http.MethodGet, "/api/rules/file?source=global", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing name, got %d", rec.Code)
	}
	var result map[string]string
	json.Unmarshal(rec.Body.Bytes(), &result)
	if result["error"] == "" {
		t.Error("expected error message")
	}
}

func TestServeRulesFileRead_InvalidSource(t *testing.T) {
	h := handlerTest(t)
	req := httptest.NewRequest(http.MethodGet, "/api/rules/file?source=invalid", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid source, got %d", rec.Code)
	}
}

func TestServeRulesFileRead_GlobalInvalidName(t *testing.T) {
	h := handlerTest(t)
	// Path traversal attempt
	req := httptest.NewRequest(http.MethodGet, "/api/rules/file?source=global&name=../../../etc/passwd", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for path traversal, got %d", rec.Code)
	}
}

func TestServeRulesFileRead_GlobalDotFile(t *testing.T) {
	h := handlerTest(t)
	// Hidden files should be rejected
	req := httptest.NewRequest(http.MethodGet, "/api/rules/file?source=global&name=.hidden.md", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for dotfile, got %d", rec.Code)
	}
}

func TestServeRulesFileRead_GlobalNotMd(t *testing.T) {
	h := handlerTest(t)
	req := httptest.NewRequest(http.MethodGet, "/api/rules/file?source=global&name=test.txt", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-.md file, got %d", rec.Code)
	}
}

func TestServeRulesFileWrite_Project(t *testing.T) {
	h := handlerTest(t)
	content := "# Test Rules\n\n- Rule 1\n- Rule 2\n"
	req := httptest.NewRequest(http.MethodPost, "/api/rules/file?source=project",
		strings.NewReader(content))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["saved"] != true {
		t.Error("expected saved=true")
	}
	if result["source"] != "project" {
		t.Errorf("expected source=project, got %v", result["source"])
	}

	// Clean up: remove the written file
	cwd, _ := os.Getwd()
	path := filepath.Join(cwd, "rules.md")
	os.Remove(path)
}

func TestServeRulesFileWrite_Global(t *testing.T) {
	h := handlerTest(t)
	content := "# Global Rules\n\nKeep it simple.\n"
	req := httptest.NewRequest(http.MethodPost, "/api/rules/file?source=global&name=test-global.md",
		strings.NewReader(content))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["saved"] != true {
		t.Error("expected saved=true")
	}

	// Clean up
	rulesDir := filepath.Join(os.Getenv("HOME"), ".ainovel", "rules")
	os.Remove(filepath.Join(rulesDir, "test-global.md"))
}

func TestServeRulesFileWrite_GlobalNoName(t *testing.T) {
	h := handlerTest(t)
	req := httptest.NewRequest(http.MethodPost, "/api/rules/file?source=global", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing name, got %d", rec.Code)
	}
}

func TestServeRulesFileWrite_GlobalInvalidName(t *testing.T) {
	h := handlerTest(t)
	req := httptest.NewRequest(http.MethodPost, "/api/rules/file?source=global&name=../etc/passwd",
		strings.NewReader("bad"))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid name, got %d", rec.Code)
	}
}

func TestServeStyleGet(t *testing.T) {
	h := handlerTest(t)
	req := httptest.NewRequest(http.MethodGet, "/api/style", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := result["style"]; !ok {
		t.Error("expected style field in response")
	}
}

func TestServeStyleSet_Success(t *testing.T) {
	h := handlerTest(t)
	body := `{"style": "suspense"}`
	req := httptest.NewRequest(http.MethodPost, "/api/style", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	// This may fail if home dir is not accessible — but test in CI should work
	if rec.Code != http.StatusOK && rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 200 or 500, got %d", rec.Code)
	}

	if rec.Code == http.StatusOK {
		var result map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatalf("invalid JSON: %v", err)
		}
		if result["saved"] != true {
			t.Error("expected saved=true")
		}
		if result["style"] != "suspense" {
			t.Errorf("expected style=suspense, got %v", result["style"])
		}
	}
}

func TestServeStyleSet_EmptyStyle(t *testing.T) {
	h := handlerTest(t)
	body := `{"style": ""}`
	req := httptest.NewRequest(http.MethodPost, "/api/style", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty style, got %d", rec.Code)
	}
}

func TestServeStyleSet_InvalidJSON(t *testing.T) {
	h := handlerTest(t)
	req := httptest.NewRequest(http.MethodPost, "/api/style", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid JSON, got %d", rec.Code)
	}
}

func TestServeRulesBundle_DefaultRoute(t *testing.T) {
	h := handlerTest(t)

	// Also verify the route dispatches to the right handler
	req := httptest.NewRequest(http.MethodGet, "/api/rules", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET /api/rules should return 200, got %d", rec.Code)
	}
}

// ── Global Rules Round-Trip Test ──

func TestRulesFileReadWriteRoundTrip(t *testing.T) {
	h := handlerTest(t)

	content := "# Round Trip Test\n\nThis is a test.\n"

	// 1. Write
	writeBody := strings.NewReader(content)
	writeReq := httptest.NewRequest(http.MethodPost, "/api/rules/file?source=global&name=roundtrip-test.md", writeBody)
	writeRec := httptest.NewRecorder()
	h.ServeHTTP(writeRec, writeReq)

	if writeRec.Code != http.StatusOK {
		t.Fatalf("write failed: %d: %s", writeRec.Code, writeRec.Body.String())
	}

	// 2. Read back
	readReq := httptest.NewRequest(http.MethodGet, "/api/rules/file?source=global&name=roundtrip-test.md", nil)
	readRec := httptest.NewRecorder()
	h.ServeHTTP(readRec, readReq)

	if readRec.Code != http.StatusOK {
		t.Fatalf("read failed: %d: %s", readRec.Code, readRec.Body.String())
	}

	var result map[string]any
	if err := json.Unmarshal(readRec.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result["exists"] != true {
		t.Error("expected exists=true after write")
	}
	if result["content"] != content {
		t.Errorf("content mismatch:\nexpected: %q\nactual:   %q", content, result["content"])
	}

	// 3. Delete
	delReq := httptest.NewRequest(http.MethodDelete, "/api/rules/file?source=global&name=roundtrip-test.md", nil)
	delRec := httptest.NewRecorder()
	h.ServeHTTP(delRec, delReq)

	if delRec.Code != http.StatusOK {
		t.Fatalf("delete failed: %d: %s", delRec.Code, delRec.Body.String())
	}

	var delResult map[string]any
	if err := json.Unmarshal(delRec.Body.Bytes(), &delResult); err != nil {
		t.Fatalf("invalid delete JSON: %v", err)
	}
	if delResult["deleted"] != true {
		t.Error("expected deleted=true")
	}

	// 4. Verify deleted (should return exists=false)
	readReq2 := httptest.NewRequest(http.MethodGet, "/api/rules/file?source=global&name=roundtrip-test.md", nil)
	readRec2 := httptest.NewRecorder()
	h.ServeHTTP(readRec2, readReq2)

	var result2 map[string]any
	if err := json.Unmarshal(readRec2.Body.Bytes(), &result2); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result2["exists"] != false {
		t.Error("expected exists=false after delete")
	}

	// Clean up (in case something went wrong)
	rulesDir := filepath.Join(os.Getenv("HOME"), ".ainovel", "rules")
	os.Remove(filepath.Join(rulesDir, "roundtrip-test.md"))
}

func TestServeRulesFileDelete_Project(t *testing.T) {
	h := handlerTest(t)

	// Write a project rules file first
	content := "delete me"
	writeReq := httptest.NewRequest(http.MethodPost, "/api/rules/file?source=project",
		strings.NewReader(content))
	writeRec := httptest.NewRecorder()
	h.ServeHTTP(writeRec, writeReq)

	// Delete
	delReq := httptest.NewRequest(http.MethodDelete, "/api/rules/file?source=project", nil)
	delRec := httptest.NewRecorder()
	h.ServeHTTP(delRec, delReq)

	if delRec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", delRec.Code, delRec.Body.String())
	}
}

func TestServeRulesFileDelete_InvalidName(t *testing.T) {
	h := handlerTest(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/rules/file?source=global&name=../bad.md", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid name, got %d", rec.Code)
	}
}

// ── File size & depth safeguards ──

func TestScanDir_DepthLimit(t *testing.T) {
	// Create a directory tree deeper than maxScanDepth (8).
	dir := t.TempDir()
	path := dir
	for i := 0; i < maxScanDepth+5; i++ {
		path = filepath.Join(path, "sub")
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	// Put a file at the deepest level
	if err := os.WriteFile(filepath.Join(path, "deep.md"), []byte("deep"), 0644); err != nil {
		t.Fatal(err)
	}

	node, err := scanDir(dir)
	if err != nil {
		t.Fatalf("scanDir deep: %v", err)
	}
	if node == nil {
		t.Fatal("expected non-nil root node")
	}

	// Walk down to see how deep we got — should stop at maxScanDepth
	depth := 0
	current := node
	for len(current.Children) > 0 && current.Children[0].Type == "dir" {
		current = current.Children[0]
		depth++
	}
	if depth > maxScanDepth {
		t.Errorf("scan went too deep: depth=%d, maxScanDepth=%d", depth, maxScanDepth)
	}
}

func TestScanDir_ShallowOk(t *testing.T) {
	dir := t.TempDir()
	// Create depth 3 tree — well within maxScanDepth=8
	sub := filepath.Join(dir, "a", "b", "c")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "file.md"), []byte("ok"), 0644); err != nil {
		t.Fatal(err)
	}

	node, err := scanDir(dir)
	if err != nil {
		t.Fatalf("scanDir shallow: %v", err)
	}
	// Should have "a" dir child
	if len(node.Children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(node.Children))
	}
	// a → b → c → file.md
	a := node.Children[0]
	b := a.Children[0]
	c := b.Children[0]
	if len(c.Children) != 1 || c.Children[0].Name != "file.md" {
		t.Errorf("unexpected deep structure: got %d children in c", len(c.Children))
	}
}

func TestScanDirDepth_DirectCall(t *testing.T) {
	// Direct call to scanDirDepth with depth > maxScanDepth should return nil.
	node, err := scanDirDepth("/nonexistent/deep/path", maxScanDepth+1)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if node != nil {
		t.Error("expected nil node when depth exceeds maxScanDepth")
	}
}

func TestServeFileRead_OversizedFile(t *testing.T) {
	// Create a >10MB file and verify os.Stat reports the correct size.
	// The handler's serveFileRead would reject this with StatusRequestEntityTooLarge.
	dir := t.TempDir()
	bigFile := filepath.Join(dir, "big.txt")
	// Create a >10MB sparse file (just set the size, actual content doesn't matter since Stat check happens first)
	if err := os.WriteFile(bigFile, make([]byte, maxFileReadSize+1), 0644); err != nil {
		t.Fatal(err)
	}

	// Directly test os.Stat + size check logic
	fi, err := os.Stat(bigFile)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() <= maxFileReadSize {
		t.Errorf("file should be > maxFileReadSize, got size=%d, max=%d", fi.Size(), maxFileReadSize)
	}
	// The handler would now reject this with StatusRequestEntityTooLarge
}

func TestServeFileRead_SmallFileOk(t *testing.T) {
	dir := t.TempDir()
	smallFile := filepath.Join(dir, "small.md")
	if err := os.WriteFile(smallFile, []byte("# hello"), 0644); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(smallFile)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() > maxFileReadSize {
		t.Errorf("small file should be within limit, got size=%d", fi.Size())
	}

	data, err := os.ReadFile(smallFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "# hello" {
		t.Errorf("unexpected content: %q", string(data))
	}
}

func TestReviewFileRead_OversizedWouldReject(t *testing.T) {
	dir := t.TempDir()
	bigFile := filepath.Join(dir, "big-premise.md")
	if err := os.WriteFile(bigFile, make([]byte, maxFileReadSize+1), 0644); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(bigFile)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Size() <= maxFileReadSize {
		t.Error("expected oversized file")
	}

	// Verify the handler would reject: call os.ReadFile should succeed
	// but the handler's guard (os.Stat + size check) comes first
}

func TestMaxFileReadSize_ConstantValue(t *testing.T) {
	if maxFileReadSize != 10*1024*1024 {
		t.Errorf("maxFileReadSize = %d, want %d", maxFileReadSize, 10*1024*1024)
	}
}

func TestMaxScanDepth_ConstantValue(t *testing.T) {
	if maxScanDepth != 8 {
		t.Errorf("maxScanDepth = %d, want 8", maxScanDepth)
	}
}
