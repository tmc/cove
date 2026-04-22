package main

import (
	"encoding/hex"
	"testing"
)

func TestGenerateShadowHashData(t *testing.T) {
	password := "testpassword123"

	shd, err := GenerateShadowHashData(password)
	if err != nil {
		t.Fatalf("GenerateShadowHashData failed: %v", err)
	}

	// Verify structure
	if shd.SaltedSHA512PBKDF2 == nil {
		t.Fatal("SaltedSHA512PBKDF2 is nil")
	}

	// Verify salt is 32 bytes
	if len(shd.SaltedSHA512PBKDF2.Salt) != 32 {
		t.Errorf("Salt length = %d, want 32", len(shd.SaltedSHA512PBKDF2.Salt))
	}

	// Verify entropy is 128 bytes
	if len(shd.SaltedSHA512PBKDF2.Entropy) != 128 {
		t.Errorf("Entropy length = %d, want 128", len(shd.SaltedSHA512PBKDF2.Entropy))
	}

	// Verify iterations
	if shd.SaltedSHA512PBKDF2.Iterations != DefaultPBKDF2Iterations {
		t.Errorf("Iterations = %d, want %d", shd.SaltedSHA512PBKDF2.Iterations, DefaultPBKDF2Iterations)
	}

	// Verify the password can be verified
	if !VerifyPassword(password, shd) {
		t.Error("VerifyPassword failed for correct password")
	}

	// Verify wrong password fails
	if VerifyPassword("wrongpassword", shd) {
		t.Error("VerifyPassword succeeded for wrong password")
	}
}

func TestEncodeKCPassword(t *testing.T) {
	tests := []struct {
		password string
	}{
		{"test"},
		{"password123"},
		{""},
		{"a"},
		{"abcdefghijklmnop"}, // longer than key
	}

	for _, tt := range tests {
		encoded := EncodeKCPassword(tt.password)

		// Verify the encoded password has the right length
		// Must be padded to multiple of 11 (key length)
		if len(encoded)%11 != 0 {
			t.Errorf("EncodeKCPassword(%q) length = %d, not multiple of 11", tt.password, len(encoded))
		}

		// Verify minimum length (password + null + padding)
		minLen := len(tt.password) + 1
		if len(encoded) < minLen {
			t.Errorf("EncodeKCPassword(%q) length = %d, want >= %d", tt.password, len(encoded), minLen)
		}

		t.Logf("EncodeKCPassword(%q) = %s", tt.password, hex.EncodeToString(encoded))
	}
}

func TestEncodeKCPasswordKnownValue(t *testing.T) {
	// Test with a known password and its expected XOR result
	// Password "test" + null byte:
	// 't' ^ 0x7D = 0x74 ^ 0x7D = 0x09
	// 'e' ^ 0x89 = 0x65 ^ 0x89 = 0xEC
	// 's' ^ 0x52 = 0x73 ^ 0x52 = 0x21
	// 't' ^ 0x23 = 0x74 ^ 0x23 = 0x57
	// '\0' ^ 0xD2 = 0x00 ^ 0xD2 = 0xD2
	// Then padding...

	encoded := EncodeKCPassword("test")

	// First 5 bytes should be:
	expected := []byte{0x09, 0xEC, 0x21, 0x57, 0xD2}

	for i, exp := range expected {
		if encoded[i] != exp {
			t.Errorf("EncodeKCPassword('test')[%d] = %02x, want %02x", i, encoded[i], exp)
		}
	}
}

func TestGenerateUUID(t *testing.T) {
	uuid, err := GenerateUUID()
	if err != nil {
		t.Fatalf("GenerateUUID failed: %v", err)
	}

	// UUID format: XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX
	if len(uuid) != 36 {
		t.Errorf("UUID length = %d, want 36", len(uuid))
	}

	// Check dashes are in right places
	if uuid[8] != '-' || uuid[13] != '-' || uuid[18] != '-' || uuid[23] != '-' {
		t.Errorf("UUID format invalid: %s", uuid)
	}

	// Generate another and verify they're different
	uuid2, _ := GenerateUUID()
	if uuid == uuid2 {
		t.Error("Two generated UUIDs should not be equal")
	}

	t.Logf("Generated UUID: %s", uuid)
}

func TestCreateUserPlist(t *testing.T) {
	up, err := CreateUserPlist("testuser", "Test User", "password123", 501, true)
	if err != nil {
		t.Fatalf("CreateUserPlist failed: %v", err)
	}

	// Verify fields
	if len(up.RecordName) != 1 || up.RecordName[0] != "testuser" {
		t.Errorf("RecordName = %v, want [testuser]", up.RecordName)
	}
	if len(up.RealName) != 1 || up.RealName[0] != "Test User" {
		t.Errorf("RealName = %v, want [Test User]", up.RealName)
	}
	if len(up.UniqueID) != 1 || up.UniqueID[0] != "501" {
		t.Errorf("UniqueID = %v, want [501]", up.UniqueID)
	}
	if len(up.NFSHomeDirectory) != 1 || up.NFSHomeDirectory[0] != "/Users/testuser" {
		t.Errorf("NFSHomeDirectory = %v, want [/Users/testuser]", up.NFSHomeDirectory)
	}
	if len(up.ShadowHashData) != 1 {
		t.Errorf("ShadowHashData length = %d, want 1", len(up.ShadowHashData))
	}
	if len(up.GeneratedUID) != 1 || len(up.GeneratedUID[0]) != 36 {
		t.Errorf("GeneratedUID = %v, want UUID", up.GeneratedUID)
	}

	// Verify admin group membership
	hasAdmin := false
	for _, g := range up.GroupMembership {
		if g == "admin" {
			hasAdmin = true
			break
		}
	}
	if !hasAdmin {
		t.Error("Admin user should have admin group membership")
	}
}

func TestEncodeShadowHashData(t *testing.T) {
	shd, err := GenerateShadowHashData("test")
	if err != nil {
		t.Fatalf("GenerateShadowHashData failed: %v", err)
	}

	data, err := EncodeShadowHashData(shd)
	if err != nil {
		t.Fatalf("EncodeShadowHashData failed: %v", err)
	}

	// Binary plist should start with "bplist"
	if len(data) < 6 || string(data[:6]) != "bplist" {
		t.Errorf("EncodeShadowHashData did not produce binary plist, got %x", data[:min(20, len(data))])
	}

	t.Logf("EncodeShadowHashData produced %d bytes", len(data))
}

func TestEncodeUserPlist(t *testing.T) {
	up, err := CreateUserPlist("testuser", "Test User", "password123", 501, false)
	if err != nil {
		t.Fatalf("CreateUserPlist failed: %v", err)
	}

	data, err := EncodeUserPlist(up)
	if err != nil {
		t.Fatalf("EncodeUserPlist failed: %v", err)
	}

	// Binary plist should start with "bplist"
	if len(data) < 6 || string(data[:6]) != "bplist" {
		t.Errorf("EncodeUserPlist did not produce binary plist, got %x", data[:min(20, len(data))])
	}

	t.Logf("EncodeUserPlist produced %d bytes", len(data))
}

func TestCreateLoginWindowPlist(t *testing.T) {
	lwp := CreateLoginWindowPlist("testuser")

	if lwp.AutoLoginUser != "testuser" {
		t.Errorf("AutoLoginUser = %q, want 'testuser'", lwp.AutoLoginUser)
	}
	if lwp.GuestEnabled {
		t.Error("GuestEnabled should be false")
	}
	if lwp.SHOWFULLNAME {
		t.Error("SHOWFULLNAME should be false")
	}
	if lwp.AutoLoginUserScreenLocked {
		t.Error("AutoLoginUserScreenLocked should be false")
	}

	data, err := EncodeLoginWindowPlist(lwp)
	if err != nil {
		t.Fatalf("EncodeLoginWindowPlist failed: %v", err)
	}

	// Binary plist should start with "bplist"
	if len(data) < 6 || string(data[:6]) != "bplist" {
		t.Errorf("EncodeLoginWindowPlist did not produce binary plist")
	}
}
