package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nazboyko/channel-tongues/internal/config"
)

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestNativeHostStartsTheHelperAndGivesTheCode(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir(), Port: freePort(t)}
	var in bytes.Buffer
	for _, typ := range []string{"ping", "start", "code", "nonsense"} {
		writeNative(&in, map[string]string{"type": typ})
	}
	var listener net.Listener
	defer func() {
		if listener != nil {
			listener.Close()
		}
	}()
	starts := 0
	start := func() error {
		starts++
		var err error
		listener, err = net.Listen("tcp", cfg.Addr())
		return err
	}
	var out bytes.Buffer
	if err := nativeHost(cfg, &in, &out, start); err != nil {
		t.Fatal(err)
	}
	var replies []nativeReply
	for out.Len() > 0 {
		raw, err := readNative(&out)
		if err != nil {
			t.Fatal(err)
		}
		var r nativeReply
		json.Unmarshal(raw, &r)
		replies = append(replies, r)
	}
	if len(replies) != 4 {
		t.Fatalf("replies %+v", replies)
	}
	if !replies[0].OK || replies[0].Running || replies[0].Code != "" {
		t.Fatalf("ping before start: %+v", replies[0])
	}
	if !replies[1].Running || len(replies[1].Code) != 9 || starts != 1 {
		t.Fatalf("start: %+v starts=%d", replies[1], starts)
	}
	if replies[2].Code != replies[1].Code || !replies[2].Running {
		t.Fatalf("code: %+v", replies[2])
	}
	if replies[3].OK || replies[3].Error == "" {
		t.Fatalf("unknown request: %+v", replies[3])
	}
}

func TestNativeHostManifestNamesOnlyOurExtension(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Chrome", "NativeMessagingHosts")
	written, err := installNativeHost([]string{dir})
	if err != nil || len(written) != 1 {
		t.Fatalf("%v %v", written, err)
	}
	b, _ := os.ReadFile(written[0])
	var m struct {
		Name    string   `json:"name"`
		Path    string   `json:"path"`
		Type    string   `json:"type"`
		Origins []string `json:"allowed_origins"`
	}
	json.Unmarshal(b, &m)
	if m.Name != nativeHostName || m.Type != "stdio" || !filepath.IsAbs(m.Path) || len(m.Origins) != 1 || !strings.HasSuffix(m.Origins[0], "cjbeephejaicfaipmjbingogeihdlggd/") {
		t.Fatalf("manifest %+v", m)
	}
}
