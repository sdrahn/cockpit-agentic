package journal

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeFormat(t *testing.T) {
	p := filepath.Join(t.TempDir(), "j.sock")
	srv, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: p, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	c, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: p, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	l := &native{name: "pp", conn: c, fallback: &JSON{Name: "pp"}}
	l.Log(ToolCall, "two\nlines", Fields{"TOOL": "zypp__install", "EMPTY": ""})
	buf := make([]byte, 4096)
	n, _ := srv.Read(buf)
	got := buf[:n]
	var want bytes.Buffer
	want.WriteString("MESSAGE\n")
	binary.Write(&want, binary.LittleEndian, uint64(9))
	want.WriteString("two\nlines\n")
	if !bytes.HasPrefix(got, want.Bytes()) {
		t.Errorf("multi-line MESSAGE not length-prefixed: %q", got)
	}
	for _, s := range []string{"MESSAGE_ID=" + MessageIDs[ToolCall] + "\n", "SYSLOG_IDENTIFIER=agent-pp\n", "AGENT_EVENT=tool.call\n", "AGENT_TOOL=zypp__install\n"} {
		if !bytes.Contains(got, []byte(s)) {
			t.Errorf("missing %q", s)
		}
	}
	if bytes.Contains(got, []byte("AGENT_EMPTY")) {
		t.Error("empty field written")
	}
}

func TestJSON(t *testing.T) {
	var b bytes.Buffer
	(&JSON{Name: "pp", W: &b}).Log(ApprovalPending, "waiting", Fields{"APPROVAL_ID": "a-1"})
	var m map[string]string
	if err := json.Unmarshal(b.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m["approval_id"] != "a-1" || m["event"] != "approval.pending" || m["name"] != "pp" {
		t.Errorf("%v", m)
	}
}

func TestEveryEventHasID(t *testing.T) {
	seen := map[string]bool{}
	for e, id := range MessageIDs {
		if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" || seen[id] {
			t.Errorf("%s: bad or duplicate id %q", e, id)
		}
		seen[id] = true
	}
}
