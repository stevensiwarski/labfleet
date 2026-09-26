package fleetctl

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderInventoryFiltersProtectedAndUnowned(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"vmid":900001,"name":"labfleet-test","node":"pve1","type":"qemu","status":"running","tags":"labfleet;disposable"},{"vmid":900005,"name":"labfleet-provisioner","node":"pve1","type":"qemu","tags":"labfleet;disposable"},{"vmid":900003,"name":"labfleet-issue-provisioner","node":"pve1","type":"qemu","status":"running","tags":"labfleet;disposable;issue13"},{"vmid":900004,"name":"labfleet-coding-agent-test","node":"pve1","type":"qemu","status":"running","tags":"labfleet;disposable"},{"vmid":900002,"name":"labfleet-other","node":"pve1","type":"qemu","tags":"labfleet"}]}`))
	}))
	defer s.Close()
	p := &provider{base: s.URL, client: s.Client()}
	got, e := p.inventory(context.Background())
	if e != nil || len(got) != 1 || got[0].VMID != 900001 || !got[0].Owned {
		t.Fatalf("inventory=%+v err=%v", got, e)
	}
}
func TestProviderRejectsUnsafeURLAndInsecureValue(t *testing.T) {
	t.Setenv("PROXMOX_VE_ENDPOINT", "http://user@example.test")
	t.Setenv("PROXMOX_VE_API_TOKEN", "x")
	if _, e := newProviderFromEnv(); e == nil {
		t.Fatal("accepted unsafe URL")
	}
	t.Setenv("PROXMOX_VE_ENDPOINT", "https://example.test")
	t.Setenv("PROXMOX_VE_INSECURE", "perhaps")
	if _, e := newProviderFromEnv(); e == nil {
		t.Fatal("accepted invalid insecure flag")
	}
}
func TestProviderErrorsDoNotExposeBody(t *testing.T) {
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500); w.Write([]byte("secret response")) }))
	defer s.Close()
	p := &provider{base: s.URL, client: s.Client()}
	_, e := p.inventory(context.Background())
	if e == nil || strings.Contains(e.Error(), "secret response") {
		t.Fatalf("unsafe error %v", e)
	}
}
func TestProtectionWireValues(t *testing.T) {
	for _, value := range []string{"0", "1", "false", "true", "null", `"0"`} {
		t.Run(value, func(t *testing.T) {
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "/config") {
					fmt.Fprintf(w, `{"data":{"tags":"labfleet;disposable;test","protection":%s}}`, value)
				} else {
					fmt.Fprint(w, `{"data":[{"vmid":900001,"name":"labfleet-test","node":"pve1","type":"qemu","tags":"labfleet;disposable;test"}]}`)
				}
			}))
			defer s.Close()
			p := &provider{base: s.URL, client: s.Client()}
			err := p.attest(context.Background(), VM{ID: 900001, Name: "labfleet-test", Node: "pve1", Tags: []string{"labfleet", "disposable", "test"}})
			allowed := value == "0" || value == "false"
			if (err == nil) != allowed {
				t.Fatalf("value=%s allowed=%t err=%v", value, allowed, err)
			}
		})
	}
}

func TestProviderProtectedIdentityGuards(t *testing.T) {
	for _, tc := range []struct {
		name      string
		id        int
		protected bool
	}{
		{"labfleet-test", 900001, false}, // Valid control: the fixture passes all other guards.
		{"labfleet-test", 930040, true},  // Only the explicit protected ID differs.
		{"labfleet-provisioner", 900001, true},
		{"labfleet-Provisioner", 900001, true},
		{"labfleet-PROVISIONER", 900001, true},
		{"labfleet-coding-agent", 900001, true},
		{"labfleet-Coding-Agent", 900001, true},
		{"labfleet-CODING-AGENT", 900001, true},
	} {
		t.Run(fmt.Sprintf("%s-%d", tc.name, tc.id), func(t *testing.T) {
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api2/json/cluster/resources":
					fmt.Fprintf(w, `{"data":[{"vmid":%d,"name":%q,"node":"pve1","type":"qemu","tags":"labfleet;disposable"}]}`, tc.id, tc.name)
				case fmt.Sprintf("/api2/json/nodes/pve1/qemu/%d/config", tc.id):
					fmt.Fprint(w, `{"data":{"tags":"labfleet;disposable","protection":0}}`)
				default:
					http.NotFound(w, r)
				}
			}))
			defer s.Close()
			p := &provider{base: s.URL, client: s.Client()}
			nodes, err := p.inventory(context.Background())
			want := 1
			if tc.protected {
				want = 0
			}
			if err != nil || len(nodes) != want {
				t.Fatalf("inventory=%+v err=%v", nodes, err)
			}
			err = p.attest(context.Background(), VM{ID: tc.id, Name: tc.name, Node: "pve1", Tags: []string{"labfleet", "disposable"}})
			if tc.protected {
				if err == nil || err.Error() != "configured VM identity does not match live inventory" {
					t.Fatalf("expected identity guard, got %v", err)
				}
			} else if err != nil {
				t.Fatalf("valid control rejected: %v", err)
			}
		})
	}
}
