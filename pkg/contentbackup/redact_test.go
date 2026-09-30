package contentbackup

import (
	"bytes"
	"testing"
)

func TestMaskLabelKeepsFirstAndLast(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"BAPI", "BXXXI"},
		{"ExampleBrand", "EXXXd"},
		{"examplebrand", "eXXXd"},
		{"EXAMPLEBRAND", "EXXXD"},
		{"example", "eXXXe"},
		{"#relay-openai-2", "#XXX2"},
		{"中", "中XXX中"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := MaskLabel(tc.in); got != tc.want {
			t.Fatalf("MaskLabel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRedactTextBrandsAndIPs(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"bapi token", "call BAPI now", "call BXXXI now"},
		{"brand mixed case", "via ExampleBrand please", "via EXXXd please"},
		{"domain keeps suffix", "https://ai.examplebrand.com/v1", "https://ai.eXXXd.com/v1"},
		{"ipv4 keeps first two and last two digits", "peer 203.0.113.10 ok", "peer 20x.x.xxx.10 ok"},
		{"ipv4 with port", "dial 192.168.1.100:443", "dial 19x.xxx.x.x00:443"},
		{"short ipv4", "from 10.0.0.1", "from 10.x.0.1"},
		{"brand plus ip", "BAPI at 203.0.113.10", "BXXXI at 20x.x.xxx.10"},
		{"ipv6 keeps first two and last two groups", "host 2001:db8:85a3:0:0:8a2e:370:7334", "host 2001:db8:x:x:x:x:370:7334"},
		{"bracket ipv6 with port", "hit [2001:db8::1]:443", "hit [2001:db8:x:x:x:x:0:1]:443"},
		{"unrelated text", "plain chat about cats", "plain chat about cats"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RedactText(tc.in); got != tc.want {
				t.Fatalf("RedactText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRedactChunksJoinsSplitMatches(t *testing.T) {
	chunks := [][]byte{[]byte("use BA"), []byte("PI please")}
	got := RedactChunks(chunks)
	joined := string(bytes.Join(got, nil))
	if joined != "use BXXXI please" {
		t.Fatalf("split BAPI became %q", joined)
	}
}

func TestIsProbeUserText(t *testing.T) {
	if !IsProbeUserText(" hi! ") || !IsProbeUserText("Hello,") || !IsProbeUserText("测试") {
		t.Fatal("common pings must count as probes")
	}
	if IsProbeUserText("please summarize this document") || IsProbeUserText("") {
		t.Fatal("real prompts and empty text must not count as probes")
	}
}
