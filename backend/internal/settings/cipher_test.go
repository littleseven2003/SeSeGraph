package settings

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func TestCipherSealOpenRoundTrip(t *testing.T) {
	cipher := testCipher(t)
	secret := "123456:unit-test-token"

	sealed, err := cipher.Seal(secret)
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	if !IsSealed(sealed) || strings.Contains(sealed, secret) {
		t.Fatalf("密文形态不对: %q", sealed)
	}
	opened, err := cipher.Open(sealed)
	if err != nil || opened != secret {
		t.Fatalf("解密 = %q err=%v，期望原值", opened, err)
	}
	// 空值不加密。
	if empty, err := cipher.Seal(""); err != nil || empty != "" {
		t.Fatalf("空值 = %q err=%v", empty, err)
	}
	// 历史明文原样返回，交给启动清理改写。
	if plain, err := cipher.Open("legacy-plain"); err != nil || plain != "legacy-plain" {
		t.Fatalf("明文透传 = %q err=%v", plain, err)
	}
}

func TestCipherRejectsWrongKeyAndTamper(t *testing.T) {
	cipher := testCipher(t)
	sealed, err := cipher.Seal("111:abc")
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	other, err := NewCipher(bytes.Repeat([]byte{0x11}, 32))
	if err != nil {
		t.Fatalf("构造另一把密钥失败: %v", err)
	}
	if _, err := other.Open(sealed); err == nil {
		t.Fatal("换密钥解密应失败")
	}
	tampered := sealed[:len(sealed)-2] + "ZZ"
	if _, err := cipher.Open(tampered); err == nil {
		t.Fatal("篡改后的密文应解密失败")
	}
	if _, err := NewCipher(bytes.Repeat([]byte{0x11}, 16)); err == nil {
		t.Fatal("16 字节密钥应拒绝")
	}
}

func TestCipherFromEnvAcceptsBase64AndHex(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	for _, encoded := range []string{
		base64.StdEncoding.EncodeToString(key),
		base64.RawURLEncoding.EncodeToString(key),
		hex.EncodeToString(key),
	} {
		lookup := func(name string) (string, bool) {
			if name == "SESEGRAPH_SECRET_KEY" {
				return encoded, true
			}
			return "", false
		}
		cipher, err := CipherFromEnv(lookup)
		if err != nil || cipher == nil {
			t.Fatalf("读取密钥 %q 失败: %v", encoded, err)
		}
	}
	// 未配置时返回空表示可选。
	if cipher, err := CipherFromEnv(func(string) (string, bool) { return "", false }); err != nil || cipher != nil {
		t.Fatalf("未配置 = %v err=%v，期望空", cipher, err)
	}
	// 格式不对要报错，不静默降级。
	if _, err := CipherFromEnv(func(name string) (string, bool) {
		return "not-a-valid-key", name == "SESEGRAPH_SECRET_KEY"
	}); err == nil {
		t.Fatal("非法密钥应报错")
	}
}
