package command

import "testing"

func TestCRC32CanonicalVector(t *testing.T) {
	// IEEE check value: "123456789" → 0xCBF43926.
	if got := CRC32([]byte("123456789")); got != 0xCBF43926 {
		t.Errorf("CRC32(123456789) = %#x, want 0xCBF43926", got)
	}
}

func TestCRC32Empty(t *testing.T) {
	if got := CRC32([]byte{}); got != 0 {
		t.Errorf("CRC32(empty) = %#x, want 0", got)
	}
}
