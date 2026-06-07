package web

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/voocel/ainovel-cli/internal/host"
)

// Server 是 HTTP/SSE Web 服务器。
type Server struct {
	addr    string
	handler *Handler
	srv     *http.Server
	lock    *lockFile
}

// lockFile 防止多实例同时运行。
// 将 PID 和端口写入 ~/.ainovel/ainovel-web.lock，
// 启动时检测已有实例是否存活，存活则拒绝启动。
type lockFile struct {
	path string
}

const lockName = "ainovel-web.lock"

func lockPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("获取用户目录失败: %w", err)
	}
	dir := filepath.Join(home, ".ainovel")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("创建 .ainovel 目录失败: %w", err)
	}
	return filepath.Join(dir, lockName), nil
}

// acquireLock 尝试获取锁。若已有实例运行，返回描述性错误。
func acquireLock(port int) (*lockFile, error) {
	p, err := lockPath()
	if err != nil {
		return nil, err
	}

	if data, err := os.ReadFile(p); err == nil {
		// 锁文件存在，检查进程是否存活
		parts := strings.SplitN(strings.TrimSpace(string(data)), ":", 2)
		pid, perr := strconv.Atoi(parts[0])
		if perr == nil && pid > 0 {
			if proc, err := os.FindProcess(pid); err == nil {
				if err := proc.Signal(syscall.Signal(0)); err == nil {
					existing := ""
					if len(parts) > 1 {
						existing = parts[1]
					}
					return nil, fmt.Errorf(
						"ainovel-web 已在运行（PID %d, 端口 %s）。\n如需重启请先停止已有进程: kill %d",
						pid, existing, pid,
					)
				}
			}
		}
		// 进程已死，清理旧锁文件
		os.Remove(p)
	}

	content := fmt.Sprintf("%d:%d", os.Getpid(), port)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("写入锁文件失败: %w", err)
	}

	return &lockFile{path: p}, nil
}

func (l *lockFile) release() {
	if l != nil {
		os.Remove(l.path)
	}
}

// CheckLock 在不创建 Server 的情况下检查单实例锁。
// 用于在加载模型等昂贵操作之前提前拒绝重复启动。
// addr 格式如 ":8080" 或 "0.0.0.0:8080"。
func CheckLock(addr string) error {
	if addr == "" {
		addr = "0.0.0.0:8080"
	}
	port := 8080
	if _, p, err := net.SplitHostPort(addr); err == nil {
		if v, e := strconv.Atoi(p); e == nil {
			port = v
		}
	}
	l, err := acquireLock(port)
	if err != nil {
		return err
	}
	l.release()
	return nil
}

// NewServer 创建 Server，获取单实例锁。
// 若已有实例运行，返回错误。
// addr 格式如 ":8080" 或 "0.0.0.0:8080"。
func NewServer(rt *host.Host, addr string) (*Server, error) {
	if addr == "" {
		addr = "0.0.0.0:8080"
	}

	port := 8080
	if _, p, err := net.SplitHostPort(addr); err == nil {
		if v, e := strconv.Atoi(p); e == nil {
			port = v
		}
	}

	lock, err := acquireLock(port)
	if err != nil {
		return nil, err
	}

	return &Server{
		addr:    addr,
		handler: NewHandler(rt),
		lock:    lock,
	}, nil
}

// Serve 启动 HTTP 服务并阻塞直到 ctx 取消或 srv 出错。
// 返回时自动释放单实例锁。
func (s *Server) Serve(ctx context.Context) error {
	defer s.lock.release()

	mux := http.NewServeMux()
	mux.Handle("/", s.handler)

	s.srv = &http.Server{
		Addr:    s.addr,
		Handler: mux,
	}

	slog.Info("Web 服务器启动", "module", "web", "addr", s.addr)
	fmt.Printf("\n🌐 Web 服务器已启动: http://%s\n", s.addr)

	errCh := make(chan error, 1)
	go func() {
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

// Addr 返回监听地址。
func (s *Server) Addr() string {
	return s.addr
}

// Shutdown 优雅关闭。
func (s *Server) Shutdown(ctx context.Context) error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Shutdown(ctx)
}
