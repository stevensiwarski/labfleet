package diagnostics

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"

	"github.com/stevensiwarski/labfleet/internal/runner"
)

type fixtureAddr string

func (a fixtureAddr) Network() string { return "ip" }
func (a fixtureAddr) String() string  { return string(a) }

func TestAddressParsingRejectsNonManagementCandidates(t *testing.T) {
	addrs := []net.Addr{fixtureAddr("192.0.2.10/24"), fixtureAddr("127.0.0.1/8"), fixtureAddr("0.0.0.0/0"), fixtureAddr("224.0.0.1/4"), fixtureAddr("2001:db8::1/64"), fixtureAddr("invalid")}
	if got := usableIPv4Addresses(addrs); !reflect.DeepEqual(got, []string{"192.0.2.10"}) {
		t.Fatalf("unexpected usable addresses: %v", got)
	}
}

func TestRuntimeServiceStates(t *testing.T) {
	previous := commandRunner
	defer func() { commandRunner = previous }()
	for _, tc := range []struct {
		state string
		err   error
		want  Status
	}{
		{"active\n", nil, Pass},
		{"inactive\n", nil, Fail},
		{"failed\n", errors.New("private stderr must not leak"), Fail},
		{"", errors.New("command unavailable"), Fail},
	} {
		f := &fakeRunner{result: runner.Result{Stdout: []byte(tc.state)}, err: tc.err}
		commandRunner = f
		got := serviceCheck(context.Background(), "containerd")
		if got.Status != tc.want {
			t.Fatalf("%q: %+v", tc.state, got)
		}
		if f.command.Name != "systemctl" || !reflect.DeepEqual(f.command.Args, []string{"is-active", "containerd"}) {
			t.Fatalf("unexpected command: %+v", f.command)
		}
		if got.Status == Fail && got.Hint == "" {
			t.Fatal("failed service lacks investigation hint")
		}
	}
}
