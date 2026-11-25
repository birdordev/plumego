package openstack

import (
	"net/url"
	"testing"
)

func TestNovaConsoleTargetBoundToConfiguredProxy(t *testing.T) {
	proxy, err := url.Parse("https://console.test:6080/novnc/websockify")
	if err != nil {
		t.Fatal(err)
	}
	const token = "fixture-opaque-console-token"
	const viewer = "https://console.test:6080/novnc/vnc_lite.html?"
	for _, input := range []string{
		viewer + "token=" + token,
		viewer + "path=" + url.QueryEscape("?token="+token),
		viewer + "path=" + url.QueryEscape("websockify?token="+token),
		viewer + "path=" + url.QueryEscape("/novnc/websockify?token="+token),
	} {
		got, err := novaConsoleTarget(proxy, input)
		if err != nil || got.Scheme != "https" || got.Host != proxy.Host || got.Path != proxy.Path || got.Query().Get("token") != token {
			t.Fatalf("compatible Nova console rejected: %v", err)
		}
	}
	for _, input := range []string{
		"http://console.test:6080/novnc/vnc_lite.html?token=" + token,
		"https://console.test:6090/novnc/vnc_lite.html?token=" + token,
		"https://foreign.test/novnc/vnc_lite.html?token=" + token,
		"https://user@console.test:6080/novnc/vnc_lite.html?token=" + token,
		"https://console.test:6080/elsewhere/vnc_lite.html?token=" + token,
		viewer + "token=" + token + "&token=" + token,
		viewer + "token=" + token + "#fragment",
		viewer + "path=" + url.QueryEscape("//foreign.test/ws?token="+token),
		viewer + "path=" + url.QueryEscape("../websockify?token="+token),
		viewer + "path=" + url.QueryEscape("websockify?token="+token+"&other=ignored"),
	} {
		if got, err := novaConsoleTarget(proxy, input); err == nil || got != nil {
			t.Fatal("unsafe or ambiguous Nova console URL accepted")
		}
	}
}
