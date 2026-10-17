package main

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/nazboyko/channel-tongues/internal/config"
	"github.com/nazboyko/channel-tongues/internal/server"
)

// Chrome starts the binary as a native messaging host, with the extension's
// origin as the first argument, so the panel can start the helper and pair
// with it without a terminal.
const nativeHostName = "com.channeltongues.helper"

var extensionOrigin = "chrome-extension://" + server.ExtensionID + "/"

type nativeRequest struct {
	Type string `json:"type"` // ping, start, code
}

type nativeReply struct {
	OK      bool   `json:"ok"`
	Running bool   `json:"running"`
	Code    string `json:"code,omitempty"`
	Error   string `json:"error,omitempty"`
}

// readNative reads one message: a 32-bit length in native byte order
// (little-endian on every platform Chrome runs on) and that many bytes of JSON.
func readNative(r io.Reader) ([]byte, error) {
	var n uint32
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return nil, err
	}
	if n > 1<<20 {
		return nil, errors.New("message too large")
	}
	buf := make([]byte, n)
	_, err := io.ReadFull(r, buf)
	return buf, err
}

func writeNative(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(len(b))); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

func helperRunning(cfg *config.Config) bool {
	conn, err := net.DialTimeout("tcp", cfg.Addr(), 300*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// nativeHost answers the panel until Chrome closes the pipe.
func nativeHost(cfg *config.Config, in io.Reader, out io.Writer, start func() error) error {
	for {
		raw, err := readNative(in)
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil
		}
		if err != nil {
			return err
		}
		var req nativeRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			writeNative(out, nativeReply{Error: "bad request"})
			continue
		}
		reply := nativeReply{OK: true, Running: helperRunning(cfg)}
		switch req.Type {
		case "ping":
		case "start":
			if !reply.Running {
				if err := start(); err != nil {
					reply = nativeReply{Error: "the helper could not start: " + err.Error()}
					break
				}
				for i := 0; i < 50 && !helperRunning(cfg); i++ {
					time.Sleep(100 * time.Millisecond)
				}
				reply.Running = helperRunning(cfg)
				if !reply.Running {
					reply = nativeReply{Error: "the helper did not start; run tongues serve in a terminal to see why"}
					break
				}
			}
			fallthrough
		case "code":
			code, err := server.LoadPairing(cfg.DataDir)
			if err != nil {
				reply = nativeReply{Error: err.Error()}
				break
			}
			reply.Code = code
		default:
			reply = nativeReply{Error: "unknown request"}
		}
		if err := writeNative(out, reply); err != nil {
			return err
		}
	}
}

// startDetachedHelper starts `tongues serve --quit-when-idle` in its own
// session, so it keeps running after Chrome closes this host process.
func startDetachedHelper(cfg *config.Config) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(cfg.DataDir, "helper-output.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "serve", "--quit-when-idle")
	cmd.Dir = filepath.Dir(exe)
	cmd.Stdout, cmd.Stderr = logf, logf
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func runNativeHost(cfg *config.Config, origin string) int {
	if origin != extensionOrigin {
		fmt.Fprintln(os.Stderr, "only the Channel Tongues extension may start this host")
		return 1
	}
	if err := nativeHost(cfg, os.Stdin, os.Stdout, func() error { return startDetachedHelper(cfg) }); err != nil {
		return 1
	}
	return 0
}

// nativeHostDirs are the per-user folders where Chrome and Chromium look for
// native messaging host manifests. Only folders of browsers that exist count.
func nativeHostDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var browsers []string
	switch runtime.GOOS {
	case "darwin":
		base := filepath.Join(home, "Library", "Application Support")
		browsers = []string{filepath.Join(base, "Google", "Chrome"), filepath.Join(base, "Google", "Chrome Beta"), filepath.Join(base, "Chromium")}
	case "linux":
		base := filepath.Join(home, ".config")
		browsers = []string{filepath.Join(base, "google-chrome"), filepath.Join(base, "google-chrome-beta"), filepath.Join(base, "chromium")}
	}
	var out []string
	for _, b := range browsers {
		if fi, err := os.Stat(b); err == nil && fi.IsDir() {
			out = append(out, filepath.Join(b, "NativeMessagingHosts"))
		}
	}
	return out
}

// installNativeHost writes the host manifest that lets the panel start the
// helper. It names this binary and allows only our extension.
func installNativeHost(dirs []string) ([]string, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	manifest, err := json.MarshalIndent(map[string]any{
		"name":            nativeHostName,
		"description":     "Channel Tongues helper",
		"path":            exe,
		"type":            "stdio",
		"allowed_origins": []string{extensionOrigin},
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	var written []string
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return written, err
		}
		path := filepath.Join(dir, nativeHostName+".json")
		if err := os.WriteFile(path, append(manifest, '\n'), 0o644); err != nil {
			return written, err
		}
		written = append(written, path)
	}
	return written, nil
}
