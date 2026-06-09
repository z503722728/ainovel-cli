// Package web 提供 HTTP/SSE API 薄转发层。
// 所有业务逻辑由 host.Host 承载，本层只做 JSON 解析、路由、SSE 格式化。
package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/rules"
)

//go:embed static
var staticFS embed.FS

// Handler 将 HTTP 请求薄转发到 host.Host 方法。
// Web 层不做任何调度决策，不碰 Host 核心逻辑。
type Handler struct {
	rt *host.Host
}

const (
	maxFileReadSize = 10 * 1024 * 1024 // 10 MB — 单次文件读取上限，防止 OOM
	maxScanDepth    = 8                // 目录树递归扫描最大深度
)

// NewHandler 创建 Handler。
func NewHandler(rt *host.Host) *Handler {
	return &Handler{rt: rt}
}

// ── 文件浏览 API 类型 ──

// FileNode 表示目录树中的一个节点（文件或目录）。
type FileNode struct {
	Name     string      `json:"name"`
	Type     string      `json:"type"` // "dir" 或 "file"
	Size     int64       `json:"size,omitempty"`
	Children []*FileNode `json:"children,omitempty"`
}

// ── 路由 ──

// ServeHTTP 实现 http.Handler。
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/" && r.Method == http.MethodGet:
		h.serveStatic(w, r)
	case strings.HasPrefix(r.URL.Path, "/static/") && r.Method == http.MethodGet:
		h.serveStaticFile(w, r)
	case r.URL.Path == "/api/stream" && r.Method == http.MethodGet:
		h.serveSSE(w, r)
	case r.URL.Path == "/api/status" && r.Method == http.MethodGet:
		h.serveStatus(w, r)
	case r.URL.Path == "/api/start" && r.Method == http.MethodPost:
		h.serveStart(w, r)
	case r.URL.Path == "/api/pause" && r.Method == http.MethodPost:
		h.servePause(w, r)
	case r.URL.Path == "/api/resume" && r.Method == http.MethodPost:
		h.serveResume(w, r)
	case r.URL.Path == "/api/steer" && r.Method == http.MethodPost:
		h.serveSteer(w, r)
	case r.URL.Path == "/api/continue" && r.Method == http.MethodPost:
		h.serveContinue(w, r)
	case r.URL.Path == "/api/cocreate" && r.Method == http.MethodPost:
		h.serveCoCreate(w, r)
	case r.URL.Path == "/api/files" && r.Method == http.MethodGet:
		h.serveFilesTree(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/files/") && r.Method == http.MethodGet:
		h.serveFileRead(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/files/") && r.Method == http.MethodPost:
		h.serveFileSave(w, r)
	case r.URL.Path == "/api/projects" && r.Method == http.MethodGet:
		h.serveProjects(w, r)
	case r.URL.Path == "/api/projects" && r.Method == http.MethodPost:
		h.serveProjectCreate(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/projects/") && r.Method == http.MethodPost:
		h.serveProjectSwitch(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/projects/") && r.Method == http.MethodDelete:
		h.serveProjectDelete(w, r)
	case r.URL.Path == "/api/rules" && r.Method == http.MethodGet:
		h.serveRulesBundle(w, r)
	case r.URL.Path == "/api/rules/sources" && r.Method == http.MethodGet:
		h.serveRulesSources(w, r)
	case r.URL.Path == "/api/rules/file" && r.Method == http.MethodGet:
		h.serveRulesFileRead(w, r)
	case r.URL.Path == "/api/rules/file" && r.Method == http.MethodPost:
		h.serveRulesFileWrite(w, r)
	case r.URL.Path == "/api/rules/file" && r.Method == http.MethodDelete:
		h.serveRulesFileDelete(w, r)
	case r.URL.Path == "/api/review-files" && r.Method == http.MethodGet:
		h.serveReviewFiles(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/review-files/") && r.Method == http.MethodGet:
		h.serveReviewFileRead(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/review-files/") && r.Method == http.MethodPost:
		h.serveReviewFileSave(w, r)
	case r.URL.Path == "/api/review/toggle" && r.Method == http.MethodPost:
		h.serveReviewToggle(w, r)
	case r.URL.Path == "/api/review/approve" && r.Method == http.MethodPost:
		h.serveReviewApprove(w, r)
	case r.URL.Path == "/api/review/skip" && r.Method == http.MethodPost:
		h.serveReviewSkip(w, r)
	case r.URL.Path == "/api/review/feedback" && r.Method == http.MethodPost:
		h.serveReviewFeedback(w, r)
	case r.URL.Path == "/api/chapter-review/approve" && r.Method == http.MethodPost:
		h.serveChapterReviewApprove(w, r)
	case r.URL.Path == "/api/chapter-review/skip" && r.Method == http.MethodPost:
		h.serveChapterReviewSkip(w, r)
	case r.URL.Path == "/api/style/current" && r.Method == http.MethodGet:
		h.serveStyleGet(w, r)
	case r.URL.Path == "/api/style/current" && r.Method == http.MethodPost:
		h.serveStyleSet(w, r)
	case r.URL.Path == "/api/styles" && r.Method == http.MethodGet:
		h.serveStylesList(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/styles/") && r.Method == http.MethodGet:
		h.serveStyleFileGet(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/styles/") && r.Method == http.MethodPut:
		h.serveStyleFilePut(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/styles/") && r.Method == http.MethodDelete:
		h.serveStyleFileDelete(w, r)
	default:
		http.NotFound(w, r)
	}
}

// ── 静态页面 ──

func (h *Handler) serveStatic(w http.ResponseWriter, r *http.Request) {
	data, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "index.html not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

func (h *Handler) serveStaticFile(w http.ResponseWriter, r *http.Request) {
	filePath := "static" + strings.TrimPrefix(r.URL.Path, "/static")
	data, err := staticFS.ReadFile(filePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// MIME detection by extension
	ct := "application/octet-stream"
	switch {
	case strings.HasSuffix(r.URL.Path, ".css"):
		ct = "text/css; charset=utf-8"
	case strings.HasSuffix(r.URL.Path, ".js"):
		ct = "application/javascript; charset=utf-8"
	case strings.HasSuffix(r.URL.Path, ".html"):
		ct = "text/html; charset=utf-8"
	case strings.HasSuffix(r.URL.Path, ".json"):
		ct = "application/json"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// ── SSE 流 ──

func (h *Handler) serveSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	ctx := r.Context()

	// 启动时先发当前状态快照
	snap := h.rt.Snapshot()
	snapJSON, _ := json.Marshal(snap)
	fmt.Fprintf(w, "event: status\ndata: %s\n\n", snapJSON)
	flusher.Flush()

	// 定时心跳（每 15 秒），防代理/负载均衡断连
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	eventsCh := h.rt.Events()
	streamCh := h.rt.Stream()
	doneCh := h.rt.Done()

	for {
		select {
		case <-ctx.Done():
			return

		case <-doneCh:
			fmt.Fprintf(w, "event: done\ndata: {}\n\n")
			flusher.Flush()
			return

		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()

		case ev, ok := <-eventsCh:
			if !ok {
				return
			}
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: event\ndata: %s\n\n", data)
			flusher.Flush()

		case delta, ok := <-streamCh:
			if !ok {
				return
			}
			if delta == host.StreamClearSentinel {
				fmt.Fprintf(w, "event: clear\ndata: {}\n\n")
			} else {
				// SSE data 行内不包含换行，用 JSON 字符串包裹以确保安全
				escaped, _ := json.Marshal(delta)
				fmt.Fprintf(w, "data: %s\n\n", escaped)
			}
			flusher.Flush()
		}
	}
}

// ── 状态快照 ──

func (h *Handler) serveStatus(w http.ResponseWriter, r *http.Request) {
	snap := h.rt.Snapshot()
	writeJSON(w, http.StatusOK, snap)
}

// ── 启动创作 ──

func (h *Handler) serveStart(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read body failed"})
		return
	}

	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "prompt is required"})
		return
	}

	if err := h.rt.Start(req.Prompt); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

// ── 暂停 ──

func (h *Handler) servePause(w http.ResponseWriter, r *http.Request) {
	stopped := h.rt.Abort()
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "stopped": stopped})
}

// ── 恢复 ──

func (h *Handler) serveResume(w http.ResponseWriter, r *http.Request) {
	label, err := h.rt.Resume()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if label == "" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "no session to resume"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "started", "label": label})
}

// ── 用户干预 ──

func (h *Handler) serveSteer(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read body failed"})
		return
	}

	var req struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "text is required"})
		return
	}

	h.rt.Steer(req.Text)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ── 继续对话 ──

func (h *Handler) serveContinue(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read body failed"})
		return
	}

	var req struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "text is required"})
		return
	}

	if err := h.rt.Continue(req.Text); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ── 共创对话 ──

func (h *Handler) serveCoCreate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read body failed"})
		return
	}

	var req struct {
		History []host.CoCreateMessage `json:"history"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if len(req.History) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "history is required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
	defer cancel()

	reply, err := h.rt.CoCreateStream(ctx, req.History, nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, reply)
}

// ── 文件浏览 ──

// novelDir 返回输出目录的绝对路径。
func (h *Handler) novelDir() string {
	return h.rt.Dir()
}

// serveFilesTree 返回输出目录的完整目录树 JSON。
func (h *Handler) serveFilesTree(w http.ResponseWriter, r *http.Request) {
	root := h.novelDir()
	node, err := scanDir(root)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if node == nil {
		writeJSON(w, http.StatusOK, &FileNode{Name: "novel", Type: "dir"})
		return
	}
	writeJSON(w, http.StatusOK, node)
}

// scanDir 递归扫描目录，排除 .git，最大深度 maxScanDepth。
func scanDir(root string) (*FileNode, error) {
	return scanDirDepth(root, 0)
}

func scanDirDepth(root string, depth int) (*FileNode, error) {
	if depth > maxScanDepth {
		return nil, nil // 跳过过深目录，防止无限递归
	}

	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s 不是目录", root)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}

	node := &FileNode{Name: info.Name(), Type: "dir"}
	for _, entry := range entries {
		name := entry.Name()
		if name == ".git" {
			continue
		}
		childPath := filepath.Join(root, name)
		if entry.IsDir() {
			child, err := scanDirDepth(childPath, depth+1)
			if err != nil {
				continue
			}
			if child != nil {
				node.Children = append(node.Children, child)
			}
		} else {
			fi, err := entry.Info()
			if err != nil {
				continue
			}
			node.Children = append(node.Children, &FileNode{
				Name: name,
				Type: "file",
				Size: fi.Size(),
			})
		}
	}
	// 子节点按名称排序：目录在前，文件在后
	sort.Slice(node.Children, func(i, j int) bool {
		if node.Children[i].Type != node.Children[j].Type {
			return node.Children[i].Type == "dir"
		}
		return node.Children[i].Name < node.Children[j].Name
	})
	return node, nil
}

// serveFileRead 读取单个文件内容，自动检测 .json/.md 类型。
func (h *Handler) serveFileRead(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/api/files/")
	if rel == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少文件路径"})
		return
	}

	absPath, err := safeResolve(h.novelDir(), rel)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	fi, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "文件不存在: " + rel})
		} else {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return
	}
	if fi.Size() > maxFileReadSize {
		writeJSON(w, http.StatusRequestEntityTooLarge,
			map[string]string{"error": "文件过大，请使用本地编辑器打开"})
		return
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// 自动检测文件类型
	fileType := "text"
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".json":
		fileType = "json"
	case ".md", ".markdown":
		fileType = "markdown"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"path":    rel,
		"content": string(data),
		"type":    fileType,
	})
}

// serveFileSave 保存文件内容（用于编辑 rules.md 等）。
func (h *Handler) serveFileSave(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/api/files/")
	if rel == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少文件路径"})
		return
	}

	absPath, err := safeResolve(h.novelDir(), rel)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// 确保父目录存在
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "读取 body 失败"})
		return
	}

	if err := os.WriteFile(absPath, body, 0644); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"path":  rel,
		"saved": true,
	})
}

// serveProjects 扫描输出目录的父目录，返回所有子目录（即项目列表）。
// 当前活跃项目通过 name 标记为 "active"。
func (h *Handler) serveProjects(w http.ResponseWriter, r *http.Request) {
	currentDir := h.novelDir()
	parentDir := filepath.Dir(currentDir)
	entries, err := os.ReadDir(parentDir)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, []map[string]string{})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	type project struct {
		Name   string `json:"name"`
		Path   string `json:"path"`
		Active bool   `json:"active,omitempty"`
	}

	currentName := filepath.Base(currentDir)
	var projects []project
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		// 只看是否像小说项目（含 chapters/ / meta/ / outline/ 子目录）
		projDir := filepath.Join(parentDir, entry.Name())
		if !looksLikeProject(projDir) {
			continue
		}
		projects = append(projects, project{
			Name:   entry.Name(),
			Path:   entry.Name(),
			Active: entry.Name() == currentName,
		})
	}

	sort.Slice(projects, func(i, j int) bool {
		return projects[i].Name < projects[j].Name
	})

	if projects == nil {
		projects = []project{}
	}

	writeJSON(w, http.StatusOK, projects)
}

// serveProjectSwitch 切换到指定项目目录。
func (h *Handler) serveProjectSwitch(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/projects/")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少项目名称"})
		return
	}
	parentDir := filepath.Dir(h.rt.Dir())
	newDir := filepath.Join(parentDir, name)
	info, err := os.Stat(newDir)
	if err != nil || !info.IsDir() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "项目不存在: " + name})
		return
	}
	if err := h.rt.SetDir(newDir); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project":  name,
		"switched": true,
	})
}

// looksLikeProject 检查目录是否为有效项目目录。
// 优先检查 .ainovel-project 标记文件（新建项目时创建）；
// 兼容检查 chapters/ / meta/ / outline/ 子目录（旧项目）。
func looksLikeProject(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	name := filepath.Base(dir)
	if strings.HasPrefix(name, ".") {
		return false
	}
	// 新建项目标记文件
	if _, err := os.Stat(filepath.Join(dir, ".ainovel-project")); err == nil {
		return true
	}
	// 兼容旧项目：有 chapters/ / meta/ / outline/ 之一即视为项目
	for _, sub := range []string{"chapters", "meta", "outline"} {
		sinfo, err := os.Stat(filepath.Join(dir, sub))
		if err == nil && sinfo.IsDir() {
			return true
		}
	}
	return false
}

// serveProjectCreate 在输出目录的同级目录下创建新项目。
func (h *Handler) serveProjectCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "项目名不能为空"})
		return
	}
	// 安全检查
	if strings.Contains(name, "/") || strings.Contains(name, "..") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无效的项目名"})
		return
	}

	parentDir := filepath.Dir(h.novelDir())
	newDir := filepath.Join(parentDir, name)
	if err := os.MkdirAll(newDir, 0755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// 创建项目标记文件，使新项目在项目列表中可见
	os.WriteFile(filepath.Join(newDir, ".ainovel-project"), []byte(""), 0644)

	// 切换到新项目
	if err := h.rt.SetDir(newDir); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"project": name,
		"created": true,
		"dir":     newDir,
	})
}

// serveProjectDelete 删除指定项目目录。
func (h *Handler) serveProjectDelete(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/projects/")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少项目名称"})
		return
	}
	// 不允许删除当前项目
	currentName := filepath.Base(h.novelDir())
	if name == currentName {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "不能删除当前活跃项目，请先切换到其他项目"})
		return
	}

	parentDir := filepath.Dir(h.novelDir())
	targetDir := filepath.Join(parentDir, name)

	// 安全检查：确保目标在 parentDir 下
	absParent, _ := filepath.Abs(parentDir)
	absTarget, _ := filepath.Abs(targetDir)
	if !strings.HasPrefix(absTarget, absParent+string(filepath.Separator)) && absTarget != absParent {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无效的项目路径"})
		return
	}

	if err := os.RemoveAll(targetDir); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"project": name,
		"deleted": true,
	})
}

// safeResolve 安全地解析相对路径，拒绝路径穿越（.. 或绝对路径）。
func safeResolve(base, rel string) (string, error) {
	cleanRel := filepath.Clean(rel)
	if filepath.IsAbs(cleanRel) {
		return "", fmt.Errorf("不允许绝对路径: %s", rel)
	}
	if strings.HasPrefix(cleanRel, "..") {
		return "", fmt.Errorf("不允许路径穿越: %s", rel)
	}
	resolved := filepath.Join(base, cleanRel)
	// 二次校验：确保结果仍在 base 之下
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	absResolved, err := filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(absResolved, absBase+string(filepath.Separator)) && absResolved != absBase {
		return "", fmt.Errorf("路径穿越被拒绝: %s", rel)
	}
	return resolved, nil
}

// ── 规则与风格 API ──

// rulesLoadOpts 构造规则加载选项（使用 Host 的 RulesFS 与当前工作目录）。
func (h *Handler) rulesLoadOpts() rules.LoadOptions {
	cwd, _ := os.Getwd()
	return rules.LoadOptions{
		RulesFS:          h.rt.RulesFS(),
		HomeRulesDir:     rules.DefaultHomeRulesDir(),
		ProjectRulesPath: rules.DefaultProjectRulesPath(cwd),
	}
}

// serveRulesBundle 读取当前生效的规则（合并后的 Bundle）返回 JSON。
func (h *Handler) serveRulesBundle(w http.ResponseWriter, r *http.Request) {
	opts := h.rulesLoadOpts()
	layers := rules.Load(opts)
	bundle := rules.Merge(layers)
	writeJSON(w, http.StatusOK, bundle)
}

// serveRulesSources 列出所有规则来源文件路径。
func (h *Handler) serveRulesSources(w http.ResponseWriter, r *http.Request) {
	opts := h.rulesLoadOpts()
	layers := rules.Load(opts)
	var sources []map[string]any
	for _, p := range layers {
		sources = append(sources, map[string]any{
			"source": p.Source,
			"kind":   p.Kind.String(),
		})
	}
	if sources == nil {
		sources = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, sources)
}

// serveRulesFileRead 读取单个规则源文件。
// query: source=project → ./rules.md, source=global → ~/.ainovel/rules/xxx.md (需要 name 参数)
func (h *Handler) serveRulesFileRead(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	name := r.URL.Query().Get("name")

	var path string
	var displayName string
	switch source {
	case "project":
		cwd, _ := os.Getwd()
		path = rules.DefaultProjectRulesPath(cwd)
		displayName = "project rules.md"
	case "global":
		if name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 name 参数"})
			return
		}
		dir := rules.DefaultHomeRulesDir()
		if dir == "" {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "无法解析家目录"})
			return
		}
		// basic safety: only allow .md files from the rules dir
		cleanName := filepath.Base(name)
		if cleanName != name || !strings.HasSuffix(cleanName, ".md") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无效的文件名"})
			return
		}
		path = filepath.Join(dir, cleanName)
		displayName = name
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无效的 source 参数（仅支持 project/global）"})
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, map[string]any{
				"source":  source,
				"name":    displayName,
				"content": "",
				"exists":  false,
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"source":  source,
		"name":    displayName,
		"content": string(data),
		"exists":  true,
	})
}

// serveRulesFileWrite 写入规则源文件。
// POST body 为 raw markdown 内容，保存到对应路径。
func (h *Handler) serveRulesFileWrite(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	name := r.URL.Query().Get("name")

	var path string
	switch source {
	case "project":
		cwd, _ := os.Getwd()
		path = rules.DefaultProjectRulesPath(cwd)
	case "global":
		if name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 name 参数"})
			return
		}
		dir := rules.DefaultHomeRulesDir()
		if dir == "" {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "无法解析家目录"})
			return
		}
		// Ensure the rules directory exists
		if err := os.MkdirAll(dir, 0755); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		cleanName := filepath.Base(name)
		if cleanName != name || !strings.HasSuffix(cleanName, ".md") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无效的文件名"})
			return
		}
		path = filepath.Join(dir, cleanName)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无效的 source 参数（仅支持 project/global）"})
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "读取 body 失败"})
		return
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if err := os.WriteFile(path, body, 0644); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"source": source,
		"name":   filepath.Base(path),
		"saved":  true,
	})
}

// serveRulesFileDelete 删除指定规则源文件。
func (h *Handler) serveRulesFileDelete(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	name := r.URL.Query().Get("name")

	var path string
	switch source {
	case "project":
		cwd, _ := os.Getwd()
		path = rules.DefaultProjectRulesPath(cwd)
	case "global":
		if name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 name 参数"})
			return
		}
		dir := rules.DefaultHomeRulesDir()
		if dir == "" {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "无法解析家目录"})
			return
		}
		cleanName := filepath.Base(name)
		if cleanName != name || !strings.HasSuffix(cleanName, ".md") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无效的文件名"})
			return
		}
		path = filepath.Join(dir, cleanName)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无效的 source 参数（仅支持 project/global）"})
		return
	}

	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			// 文件不存在也视为成功
			writeJSON(w, http.StatusOK, map[string]any{
				"source":  source,
				"name":    filepath.Base(path),
				"deleted": true,
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"source":  source,
		"name":    filepath.Base(path),
		"deleted": true,
	})
}

// serveStyleGet 读取当前 style 设置。
func (h *Handler) serveStyleGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"style": h.rt.Style(),
	})
}

// serveStyleSet 切换 style，保存到 ~/.ainovel/config.json 并更新内存。
func (h *Handler) serveStyleSet(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "读取 body 失败"})
		return
	}

	var req struct {
		Style string `json:"style"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	newStyle := strings.TrimSpace(req.Style)
	if newStyle == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "style 不能为空"})
		return
	}

	// 保存到 ~/.ainovel/config.json
	configPath := bootstrap.DefaultConfigPath()
	if configPath == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "无法解析配置路径"})
		return
	}

	// 读取现有配置（如果存在），更新 style 并写回
	var cfg bootstrap.Config
	if data, err := os.ReadFile(configPath); err == nil {
		cleaned := bootstrap.StripJSONComments(data)
		if err := json.Unmarshal(cleaned, &cfg); err != nil {
			// 文件损坏时使用空配置
			cfg = bootstrap.Config{}
		}
	}
	cfg.Style = newStyle
	if err := bootstrap.SaveConfig(configPath, cfg); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// 更新内存
	h.rt.SetStyle(newStyle)

	writeJSON(w, http.StatusOK, map[string]any{
		"style": newStyle,
		"saved": true,
	})
}

// stylesDir 返回风格文件所在目录。
func (h *Handler) stylesDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".ainovel", "styles")
}

// serveStylesList 列出所有可用风格（内置默认 + 用户自定义文件）。
func (h *Handler) serveStylesList(w http.ResponseWriter, r *http.Request) {
	type styleEntry struct {
		Name     string `json:"name"`
		Label    string `json:"label"`
		IsActive bool   `json:"is_active"`
		Builtin  bool   `json:"builtin,omitempty"`
	}

	builtins := []styleEntry{
		{Name: "default", Label: "默认"},
		{Name: "suspense", Label: "悬疑"},
		{Name: "fantasy", Label: "奇幻"},
		{Name: "romance", Label: "言情"},
	}
	active := h.rt.Style()

	// 标记当前激活
	for i := range builtins {
		if builtins[i].Name == active {
			builtins[i].IsActive = true
		}
		builtins[i].Builtin = true
	}

	// 扫描用户自定义风格文件
	dir := h.stylesDir()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	seen := map[string]bool{"default": true, "suspense": true, "fantasy": true, "romance": true}
	var custom []styleEntry
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".md")
		if seen[name] {
			continue
		}
		label := strings.Title(strings.ReplaceAll(name, "-", " "))
		// 读取自定义 label（如果有的话）
		if labelBytes, err := os.ReadFile(filepath.Join(dir, name+".label")); err == nil {
			label = string(labelBytes)
		}
		custom = append(custom, styleEntry{
			Name:     name,
			Label:    label,
			IsActive: name == active,
		})
		seen[name] = true
	}
	sort.Slice(custom, func(i, j int) bool { return custom[i].Name < custom[j].Name })

	styles := append(builtins, custom...)
	writeJSON(w, http.StatusOK, styles)
}

// serveStyleFileGet 读取单个风格文件内容。内置风格从嵌入 FS 读取，自定义风格从 ~/.ainovel/styles/ 读取。
func (h *Handler) serveStyleFileGet(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/styles/")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少风格名称"})
		return
	}

	// 内置风格：从嵌入的 assets.Load() 读取
	builtin := map[string]bool{"default": true, "suspense": true, "fantasy": true, "romance": true}
	if builtin[name] {
		embedded := h.rt.EmbeddedStyles()
		if content, ok := embedded[name]; ok {
			writeJSON(w, http.StatusOK, map[string]any{
				"name":    name,
				"content": content,
			})
			return
		}
	}

	path := filepath.Join(h.stylesDir(), name+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "风格不存在: " + name})
		} else {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"name":    name,
		"content": string(data),
	})
}

// serveStyleFilePut 创建或更新风格文件。
func (h *Handler) serveStyleFilePut(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/styles/")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少风格名称"})
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "读取 body 失败"})
		return
	}
	// 解析 JSON，提取 content 和 label 字段
	var req struct {
		Content string `json:"content"`
		Label   string `json:"label,omitempty"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json: " + err.Error()})
		return
	}
	dir := h.stylesDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	path := filepath.Join(dir, name+".md")
	if err := os.WriteFile(path, []byte(req.Content), 0644); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// 保存 label（如果有的话）
	if req.Label != "" {
		labelPath := filepath.Join(dir, name+".label")
		os.WriteFile(labelPath, []byte(req.Label), 0644)
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "saved": true})
}

// serveStyleFileDelete 删除风格文件。
func (h *Handler) serveStyleFileDelete(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/api/styles/")
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少风格名称"})
		return
	}
	path := filepath.Join(h.stylesDir(), name+".md")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusOK, map[string]any{"name": name, "deleted": true})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// 同时删除 label 文件
	os.Remove(filepath.Join(h.stylesDir(), name+".label"))
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "deleted": true})
}

// ── 审查文件 API ──

// ReviewFileInfo 审查面板显示的文件信息。
type ReviewFileInfo struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Preview string `json:"preview"`
	Type    string `json:"type"`
}

// foundationDir 返回 store 根目录（premise.md 等基础文件所在目录）。
func (h *Handler) foundationDir() string {
	return h.rt.Dir()
}

// reviewFileNames 审查面板需要显示的基础文件列表。
var reviewFileNames = []string{
	"premise.md",
	"characters.json",
	"outline.json",
	"world_rules.json",
}

// serveReviewFiles 返回审查面板文件列表（含 150 字预览）。
func (h *Handler) serveReviewFiles(w http.ResponseWriter, r *http.Request) {
	base := h.foundationDir()
	var files []ReviewFileInfo

	for _, name := range reviewFileNames {
		path := name
		absPath := filepath.Join(base, name)
		data, err := os.ReadFile(absPath)
		if err != nil {
			continue // 文件不存在则跳过
		}

		// 自动检测文件类型
		fileType := "text"
		switch strings.ToLower(filepath.Ext(name)) {
		case ".json":
			fileType = "json"
		case ".md", ".markdown":
			fileType = "markdown"
		}

		content := string(data)
		// 取前 150 字符作为预览文本
		preview := content
		runes := []rune(content)
		if len(runes) > 150 {
			preview = string(runes[:150]) + "..."
		}

		files = append(files, ReviewFileInfo{
			Name:    name,
			Path:    path,
			Preview: preview,
			Type:    fileType,
		})
	}

	if files == nil {
		files = []ReviewFileInfo{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"files": files,
	})
}

// serveReviewFileRead 读取单个审查文件的完整内容。
func (h *Handler) serveReviewFileRead(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/api/review-files/")
	if rel == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少文件路径"})
		return
	}

	absPath, err := safeResolve(h.foundationDir(), rel)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	fi, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "文件不存在: " + rel})
		} else {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return
	}
	if fi.Size() > maxFileReadSize {
		writeJSON(w, http.StatusRequestEntityTooLarge,
			map[string]string{"error": "文件过大，请使用本地编辑器打开"})
		return
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// 自动检测文件类型
	fileType := "text"
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".json":
		fileType = "json"
	case ".md", ".markdown":
		fileType = "markdown"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"path":    rel,
		"content": string(data),
		"type":    fileType,
	})
}

// serveReviewFileSave 保存审查文件（premise.md 等）。
func (h *Handler) serveReviewFileSave(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/api/review-files/")
	if rel == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少文件路径"})
		return
	}

	absPath, err := safeResolve(h.foundationDir(), rel)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// 确保父目录存在
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "读取 body 失败"})
		return
	}

	if err := os.WriteFile(absPath, body, 0644); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"path":  rel,
		"saved": true,
	})
}

// ── 审查操作 ──

// serveReviewToggle 切换审阅模式开关，保存到 ~/.ainovel/config.json。
func (h *Handler) serveReviewToggle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}

	configPath := bootstrap.DefaultConfigPath()
	if configPath == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "无法解析配置路径"})
		return
	}

	var cfg bootstrap.Config
	if data, err := os.ReadFile(configPath); err == nil {
		cleaned := bootstrap.StripJSONComments(data)
		if err := json.Unmarshal(cleaned, &cfg); err != nil {
			cfg = bootstrap.Config{}
		}
	}
	cfg.ReviewEnabled = req.Enabled
	if err := bootstrap.SaveConfig(configPath, cfg); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// 同步更新内存中的配置
	h.rt.SetReviewEnabled(req.Enabled)

	writeJSON(w, http.StatusOK, map[string]any{
		"review_enabled": req.Enabled,
		"saved":          true,
	})
}

// serveReviewApprove 审查通过，恢复创作流程。
func (h *Handler) serveReviewApprove(w http.ResponseWriter, r *http.Request) {
	if err := h.rt.ResumeAfterReviewWithAction("approved"); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

// serveReviewSkip 跳过审查，直接恢复创作流程。
func (h *Handler) serveReviewSkip(w http.ResponseWriter, r *http.Request) {
	if err := h.rt.ResumeAfterReviewWithAction("skipped"); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "skipped"})
}

// serveReviewFeedback 审查期间发送反馈：构建完整上下文 + 用户反馈。
func (h *Handler) serveReviewFeedback(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read body failed"})
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "text is required"})
		return
	}
	if err := h.rt.SendReviewFeedback(req.Text); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "feedback_sent"})
}

// ── 章节审查操作 ──

// serveChapterReviewApprove 章节审查通过，继续下一章。
func (h *Handler) serveChapterReviewApprove(w http.ResponseWriter, r *http.Request) {
	if err := h.rt.ResumeAfterReviewWithAction("chapter_approved"); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "chapter_approved"})
}

// serveChapterReviewSkip 跳过章节审查，直接继续下一章。
func (h *Handler) serveChapterReviewSkip(w http.ResponseWriter, r *http.Request) {
	if err := h.rt.ResumeAfterReviewWithAction("chapter_skipped"); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "chapter_skipped"})
}

// ── 工具 ──

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		_ = err
	}
}
