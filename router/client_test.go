package router

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseDBValue(t *testing.T) {
	cases := []struct {
		input    string
		expected *float64
	}{
		{"-95dBm", fp(-95)},
		{"10dB", fp(10)},
		{">=-5dB", fp(-5)},
		{"17.5dB", fp(17.5)},
		{"-102.4dBm", fp(-102.4)},
		{"", nil},
		{"abc", nil},
	}

	for _, c := range cases {
		got := ParseDBValue(c.input)
		if c.expected == nil {
			if got != nil {
				t.Errorf("ParseDBValue(%q) = %v; want nil", c.input, *got)
			}
		} else {
			if got == nil || *got != *c.expected {
				t.Errorf("ParseDBValue(%q) = %v; want %v", c.input, got, *c.expected)
			}
		}
	}
}

func TestHuaweiClientParseSignalXML(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<response>
    <rsrp>-95dBm</rsrp>
    <rsrq>-10dB</rsrq>
    <sinr>15dB</sinr>
    <rssi>-65dBm</rssi>
    <cell_id>12345</cell_id>
    <pci>100</pci>
    <band>B3</band>
</response>`

	s, err := parseSignalXML([]byte(xmlData))
	if err != nil {
		t.Fatalf("parseSignalXML returned error: %v", err)
	}

	if s.RSRP == nil || *s.RSRP != -95 {
		t.Errorf("RSRP = %v; want -95", s.RSRP)
	}
	if s.RSRQ == nil || *s.RSRQ != -10 {
		t.Errorf("RSRQ = %v; want -10", s.RSRQ)
	}
	if s.SINR == nil || *s.SINR != 15 {
		t.Errorf("SINR = %v; want 15", s.SINR)
	}
	if s.RSSI == nil || *s.RSSI != -65 {
		t.Errorf("RSSI = %v; want -65", s.RSSI)
	}
	if s.CellID != "12345" {
		t.Errorf("CellID = %s; want 12345", s.CellID)
	}
}

func TestHuaweiClientAuthFlow(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		switch r.URL.Path {
		case "/api/device/signal":
			if r.Header.Get("__RequestVerificationToken") == "" {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><error><code>125002</code><message>auth required</message></error>`))
				return
			}
			w.Header().Set("__RequestVerificationToken", "newtoken123")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><response><rsrp>-88dBm</rsrp><sinr>20dB</sinr></response>`))
		case "/api/webserver/SesTokInfo":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><response><SesInfo>SessionID=fake123</SesInfo><TokInfo>tok456</TokInfo></response>`))
		case "/api/user/login":
			w.Header().Set("__RequestVerificationToken", "tokenafterlogin")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><response><result>OK</result></response>`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewHuaweiClient(server.URL, "admin", "admin")
	sample, err := client.FetchSample()
	if err != nil {
		t.Fatalf("FetchSample() error = %v", err)
	}
	if sample.RSRP == nil || *sample.RSRP != -88 {
		t.Errorf("expected RSRP -88, got %v", sample.RSRP)
	}
}
