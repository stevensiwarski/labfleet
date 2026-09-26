package diagnostics

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
)

func defaultRoute() (string, string, error) {
	f, e := os.Open("/proc/net/route")
	if e != nil {
		return "", "", e
	}
	defer f.Close()
	return parseDefaultRoute(f)
}

func parseDefaultRoute(r io.Reader) (string, string, error) {
	s := bufio.NewScanner(r)
	if !s.Scan() {
		if e := s.Err(); e != nil {
			return "", "", e
		}
		return "", "", errors.New("route header absent")
	}
	header := strings.Fields(s.Text())
	if len(header) < 8 || header[0] != "Iface" || header[1] != "Destination" || header[2] != "Gateway" || header[3] != "Flags" {
		return "", "", errors.New("invalid route header")
	}
	for s.Scan() {
		f := strings.Fields(s.Text())
		if len(f) != len(header) {
			return "", "", errors.New("malformed route row")
		}
		if f[1] != "00000000" || f[7] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(f[3], 16, 32)
		if err != nil {
			return "", "", errors.New("malformed route flags")
		}
		if flags&1 == 0 || flags&2 == 0 {
			continue
		}
		raw, e := strconv.ParseUint(f[2], 16, 32)
		if e != nil {
			return "", "", errors.New("malformed route gateway")
		}
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, uint32(raw))
		ip := net.IP(b).String()
		if net.ParseIP(ip) == nil || ip == "0.0.0.0" {
			return "", "", errors.New("invalid route")
		}
		return f[0], ip, nil
	}
	if e := s.Err(); e != nil {
		return "", "", e
	}
	return "", "", errors.New("default route absent")
}
