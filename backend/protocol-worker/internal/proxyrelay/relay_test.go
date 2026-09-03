package proxyrelay

import (
	"net/url"
	"testing"
)

func TestSocksAuthURLParsesPasswordWithAt(t *testing.T) {
	parsed, err := url.Parse("socks5://user_1-sesstime-120:ZNZxz@@proxy.example.com:2000")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Hostname() != "proxy.example.com" || parsed.Port() != "2000" {
		t.Fatalf("host=%s port=%s", parsed.Hostname(), parsed.Port())
	}
	password, _ := parsed.User.Password()
	if password != "ZNZxz@" {
		t.Fatalf("password=%q", password)
	}
}

func TestChromeServerPassthroughHTTPWithoutAuth(t *testing.T) {
	settings, err := ForChrome("http://127.0.0.1:7897")
	if err != nil {
		t.Fatal(err)
	}
	defer settings.Cleanup()
	if settings.Server != "http://127.0.0.1:7897" {
		t.Fatalf("server=%s", settings.Server)
	}
	if settings.HostResolverRules != "" {
		t.Fatal("http proxy should not rewrite DNS")
	}
}
