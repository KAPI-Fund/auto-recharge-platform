package chromeapi

import "testing"

func TestDesktopChromeUAStaysMacAndMatchesMajor(t *testing.T) {
	ua := desktopChromeUA("151")
	if ua != "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/151.0.0.0 Safari/537.36" {
		t.Fatal(ua)
	}
	if desktopChromeUA("") == "" {
		t.Fatal("empty major")
	}
}
