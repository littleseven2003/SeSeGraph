package settings

import (
	"bytes"
	"testing"
)

func testCipher(t *testing.T) *Cipher {
	t.Helper()
	cipher, err := NewCipher(bytes.Repeat([]byte{0x5a}, 32))
	if err != nil {
		t.Fatalf("构造测试加密器失败: %v", err)
	}
	return cipher
}
