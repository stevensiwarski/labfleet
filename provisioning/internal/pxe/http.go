package pxe

import (
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"syscall"
)

func Handler(c Config) http.Handler {
	allowed := map[string]string{"/boot.ipxe": "boot.ipxe", "/seed/user-data": "user-data", "/seed/meta-data": "meta-data"}
	for _, n := range artifactNames {
		allowed["/"+n] = n
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip, _, e := net.SplitHostPort(r.RemoteAddr)
		if e != nil || net.ParseIP(ip) == nil || !net.ParseIP(ip).Equal(net.ParseIP(c.TargetIP)) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		if path.Clean(r.URL.Path) != r.URL.Path || strings.Contains(r.URL.Path, "..") {
			http.NotFound(w, r)
			return
		}
		name, ok := allowed[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		var filename string
		if name == "user-data" || name == "meta-data" || name == "boot.ipxe" {
			filename = path.Join(c.Output, name)
		} else {
			filename = path.Join(c.Artifacts, name)
		}
		f, err := os.OpenFile(filename, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, name, info.ModTime(), f)
	})
}
