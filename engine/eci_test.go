package engine

import (
	"testing"
)

func TestECISplit(t *testing.T) {
	// Standard ECI 28-bit: top 20 bits eNB, bottom 8 bits sector
	cases := []struct {
		name       string
		eci        int
		expectedENB int
		expectedSec int
	}{
		{"Example from spec", 12345678, 12345678 >> 8, 12345678 & 0xFF},
		{"Single sector eNB 1", 256, 1, 0},
		{"Zero ECI", 0, 0, 0},
		{"Sector 255", 511, 1, 255},
		{"Libya typical cell", 60600123, 60600123 >> 8, 60600123 & 0xFF},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			enb, sec := ParseECI(c.eci)
			if enb != c.expectedENB || sec != c.expectedSec {
				t.Errorf("ParseECI(%d) = eNB %d, sector %d; want %d, %d",
					c.eci, enb, sec, c.expectedENB, c.expectedSec)
			}
		})
	}
}

func TestECIStringParsing(t *testing.T) {
	enb, sec, err := ParseECIString("12345678")
	if err != nil {
		t.Fatalf("decimal parse error: %v", err)
	}
	if enb != 48225 || sec != 78 {
		t.Errorf("got %d, %d; want 48225, 78", enb, sec)
	}

	// Hex string
	enbHex, secHex, err := ParseECIString("0xBC614E")
	if err != nil {
		t.Fatalf("hex parse error: %v", err)
	}
	if enbHex != 48225 || secHex != 78 {
		t.Errorf("got %d, %d; want 48225, 78", enbHex, secHex)
	}
}
