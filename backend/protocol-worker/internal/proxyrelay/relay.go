package proxyrelay

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

type ChromeSettings struct {
	Server             string
	HostResolverRules  string
	Cleanup            func()
}

func ChromeServer(raw string) (server string, cleanup func(), err error) {
	settings, err := ForChrome(raw)
	if err != nil {
		return "", func() {}, err
	}
	return settings.Server, settings.Cleanup, nil
}

func ForChrome(raw string) (ChromeSettings, error) {
	raw = strings.TrimSpace(raw)
	nop := func() {}
	if raw == "" {
		return ChromeSettings{Cleanup: nop}, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return ChromeSettings{}, fmt.Errorf("代理 URL 无效")
	}
	scheme := strings.ToLower(strings.TrimSuffix(parsed.Scheme, ":"))
	hasAuth := parsed.User != nil && (parsed.User.Username() != "" || func() bool { _, ok := parsed.User.Password(); return ok }())
	if (scheme == "http" || scheme == "https" || scheme == "") && !hasAuth {
		return ChromeSettings{Server: "http://" + parsed.Host, Cleanup: nop}, nil
	}

	dialer, err := upstreamDialer(parsed)
	if err != nil {
		return ChromeSettings{}, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return ChromeSettings{}, err
	}
	done := make(chan struct{})
	go serveSOCKS(ln, dialer, done)
	return ChromeSettings{
		Server:            "socks5://" + ln.Addr().String(),
		HostResolverRules: "MAP * ~NOTFOUND , EXCLUDE 127.0.0.1",
		Cleanup: func() {
			_ = ln.Close()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
			}
		},
	}, nil
}

func isSOCKS(scheme string) bool {
	return scheme == "socks" || scheme == "socks4" || scheme == "socks5" || scheme == "socks5h"
}

func upstreamDialer(parsed *url.URL) (proxy.Dialer, error) {
	timeout := &net.Dialer{Timeout: 20 * time.Second}
	scheme := strings.ToLower(strings.TrimSuffix(parsed.Scheme, ":"))
	if isSOCKS(scheme) {
		var auth *proxy.Auth
		if parsed.User != nil {
			password, _ := parsed.User.Password()
			auth = &proxy.Auth{User: parsed.User.Username(), Password: password}
		}
		return proxy.SOCKS5("tcp", parsed.Host, auth, timeout)
	}
	return httpUpstreamDialer{address: parsed.Host, user: userinfo(parsed), next: timeout}, nil
}

func userinfo(parsed *url.URL) string {
	if parsed.User == nil {
		return ""
	}
	password, _ := parsed.User.Password()
	if parsed.User.Username() == "" && password == "" {
		return ""
	}
	return parsed.User.Username() + ":" + password
}

type httpUpstreamDialer struct {
	address string
	user    string
	next    proxy.Dialer
}

func (d httpUpstreamDialer) Dial(network, addr string) (net.Conn, error) {
	conn, err := d.next.Dial(network, d.address)
	if err != nil {
		return nil, err
	}
	req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", addr, addr)
	if d.user != "" {
		req += "Proxy-Authorization: Basic " + basicAuth(d.user) + "\r\n"
	}
	req += "\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		conn.Close()
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("上游代理 CONNECT %s", resp.Status)
	}
	return conn, nil
}

func basicAuth(userinfo string) string {
	return base64.StdEncoding.EncodeToString([]byte(userinfo))
}

func serveSOCKS(ln net.Listener, dialer proxy.Dialer, done chan struct{}) {
	defer close(done)
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go handleSOCKS(conn, dialer)
	}
}

func handleSOCKS(conn net.Conn, dialer proxy.Dialer) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(45 * time.Second))
	br := bufio.NewReader(conn)
	header := make([]byte, 2)
	if _, err := io.ReadFull(br, header); err != nil || header[0] != 5 {
		return
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(br, methods); err != nil {
		return
	}
	_, _ = conn.Write([]byte{5, 0})
	req := make([]byte, 4)
	if _, err := io.ReadFull(br, req); err != nil || req[0] != 5 || req[1] != 1 {
		return
	}
	var host string
	switch req[3] {
	case 1:
		addr := make([]byte, 4)
		if _, err := io.ReadFull(br, addr); err != nil {
			return
		}
		host = net.IP(addr).String()
	case 3:
		size := make([]byte, 1)
		if _, err := io.ReadFull(br, size); err != nil {
			return
		}
		name := make([]byte, int(size[0]))
		if _, err := io.ReadFull(br, name); err != nil {
			return
		}
		host = string(name)
	case 4:
		addr := make([]byte, 16)
		if _, err := io.ReadFull(br, addr); err != nil {
			return
		}
		host = net.IP(addr).String()
	default:
		return
	}
	portb := make([]byte, 2)
	if _, err := io.ReadFull(br, portb); err != nil {
		return
	}
	port := int(portb[0])<<8 | int(portb[1])
	up, err := dialer.Dial("tcp", net.JoinHostPort(host, fmt.Sprintf("%d", port)))
	if err != nil {
		_, _ = conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer up.Close()
	_, _ = conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	_ = conn.SetDeadline(time.Time{})
	_ = up.SetDeadline(time.Time{})
	errc := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, br); _ = up.Close(); errc <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, up); _ = conn.Close(); errc <- struct{}{} }()
	<-errc
}
