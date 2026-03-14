// password.go - macOS password hash and auto-login support
//
// # Overview
//
// This file implements macOS password hashing and auto-login credential encoding.
// These are used for direct user plist creation (advanced mode) and auto-login
// configuration.
//
// # Password Hash Format (SALTED-SHA512-PBKDF2)
//
// Since macOS 10.8 (Mountain Lion), passwords are stored using PBKDF2 with SHA-512.
// The hash is stored in the user's plist at:
//
//	/var/db/dslocal/nodes/Default/users/<username>.plist
//
// The ShadowHashData structure contains:
//
//	SALTED-SHA512-PBKDF2:
//	  entropy:    128-byte derived key (PBKDF2 output)
//	  iterations: 45000 (macOS 10.14+ default)
//	  salt:       32 random bytes
//
// The derivation formula is:
//
//	entropy = PBKDF2(password, salt, iterations, keyLength=128, hashFunc=SHA512)
//
// # Security Considerations
//
// The 45000 iteration count provides reasonable protection against offline
// brute-force attacks while maintaining acceptable login performance. This
// value is the macOS default as of macOS 10.14 Mojave.
//
// For VM provisioning, security is less critical since:
//   - The password is already known (provided at injection time)
//   - VMs are typically development/testing environments
//   - The disk image can be accessed directly anyway
//
// # Auto-Login (kcpassword)
//
// macOS auto-login stores the password in /etc/kcpassword using a simple XOR
// cipher. This is intentionally weak because:
//
//  1. Auto-login fundamentally requires recoverable credentials
//  2. Physical access to the machine defeats most protections anyway
//  3. The feature is opt-in and disabled by default
//
// The encoding uses an 11-byte key that repeats:
//
//	Key: 0x7D, 0x89, 0x52, 0x23, 0xD2, 0xBC, 0xDD, 0xEA, 0xA3, 0xB9, 0x1F
//
// The password is XOR'd byte-by-byte with this key, then padded to a multiple
// of 12 bytes with random data (11 bytes of padding + null terminator pattern).
//
// # kcpassword Encoding Algorithm
//
//	func encode(password string) []byte {
//	    key := []byte{0x7D, 0x89, 0x52, 0x23, 0xD2, 0xBC, 0xDD, 0xEA, 0xA3, 0xB9, 0x1F}
//	    result := make([]byte, 0)
//	    for i, b := range []byte(password) {
//	        result = append(result, b ^ key[i % len(key)])
//	    }
//	    // Pad to multiple of 12 bytes
//	    padLen := 12 - (len(result) % 12)
//	    if padLen < 12 {
//	        result = append(result, randomBytes(padLen)...)
//	    }
//	    return result
//	}
//
// # Auto-Login Configuration Files
//
// Two files work together to enable auto-login:
//
//  1. /etc/kcpassword - XOR-encoded password (this file)
//  2. /Library/Preferences/com.apple.loginwindow.plist - Contains:
//     - autoLoginUser: username to auto-login
//     - GuestEnabled: false
//     - SHOWFULLNAME: false
//
// Both files must be owned by root (uid=0) for security, though loginwindow.plist
// is less strict than LaunchDaemon plists.
//
// # User Plist Structure
//
// For direct user creation (bypassing sysadminctl), the user plist contains:
//
//	name:           ["username"]           - Account short name
//	realname:       ["Full Name"]          - Display name
//	uid:            ["501"]                - User ID (501+ for regular users)
//	gid:            ["20"]                 - Primary group (20 = staff)
//	home:           ["/Users/username"]    - Home directory path
//	shell:          ["/bin/zsh"]           - Login shell
//	generateduid:   ["<UUID>"]             - Unique identifier
//	ShadowHashData: <binary plist>         - Password hash (nested plist)
//
// The ShadowHashData is itself a binary plist embedded as a data blob within
// the outer user plist. This nested structure is how macOS stores password
// hashes securely.
//
// # macOS Version Compatibility
//
//	Version         Password Format         Auto-Login
//	macOS 10.8+     SALTED-SHA512-PBKDF2    kcpassword XOR
//	macOS 10.14+    45000 iterations        Same
//	macOS 14/15     Same format             May require FileVault disabled
//
// # Usage Examples
//
// Generate password hash for direct user plist creation:
//
//	shadowHash, err := GenerateShadowHashData("secretpassword")
//	userPlist, err := CreateUserPlist("testuser", "Test User", "secretpassword", 501, true)
//	plistData, err := EncodeUserPlist(userPlist)
//	os.WriteFile("/path/to/user.plist", plistData, 0600)
//
// Encode password for auto-login:
//
//	encoded := EncodeKcpassword("secretpassword")
//	os.WriteFile("/Volumes/Data/private/etc/kcpassword", encoded, 0600)
package main

import (
	"crypto/rand"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/apple/x/plist"
	"golang.org/x/crypto/pbkdf2"
)

// ShadowHashData represents the password hash structure used by macOS
// This is stored in the user's plist at /var/db/dslocal/nodes/Default/users/<username>.plist
type ShadowHashData struct {
	// SALTED-SHA512-PBKDF2 is the modern password hash format used by macOS 10.8+
	SaltedSHA512PBKDF2 *PBKDF2Hash `plist:"SALTED-SHA512-PBKDF2,omitempty"`
}

// PBKDF2Hash represents the PBKDF2 password hash components
type PBKDF2Hash struct {
	Entropy    []byte `plist:"entropy"`
	Iterations int    `plist:"iterations"`
	Salt       []byte `plist:"salt"`
}

// DefaultPBKDF2Iterations is the number of iterations used by modern macOS
// macOS 10.14+ uses around 45000 iterations
const DefaultPBKDF2Iterations = 45000

// GenerateShadowHashData creates a ShadowHashData structure for the given password
// This generates the format used by macOS 10.8+ (SALTED-SHA512-PBKDF2)
func GenerateShadowHashData(password string) (*ShadowHashData, error) {
	// Generate a random 32-byte salt
	salt := make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}

	// Generate the key using PBKDF2 with SHA512
	// macOS uses a 128-byte derived key
	entropy := pbkdf2.Key([]byte(password), salt, DefaultPBKDF2Iterations, 128, sha512.New)

	return &ShadowHashData{
		SaltedSHA512PBKDF2: &PBKDF2Hash{
			Entropy:    entropy,
			Iterations: DefaultPBKDF2Iterations,
			Salt:       salt,
		},
	}, nil
}

// EncodeShadowHashData encodes the ShadowHashData to the binary plist format
// used by macOS user plists (wrapped in an array)
func EncodeShadowHashData(shd *ShadowHashData) ([]byte, error) {
	// macOS stores ShadowHashData as a binary plist wrapped in a single-element array
	data, err := plist.Marshal(shd, plist.FormatBinary)
	if err != nil {
		return nil, fmt.Errorf("marshal shadow hash data: %w", err)
	}
	return data, nil
}

// GenerateUUID generates a UUID string in the format used by macOS
// Format: XXXXXXXX-XXXX-XXXX-XXXX-XXXXXXXXXXXX
func GenerateUUID() (string, error) {
	uuid := make([]byte, 16)
	if _, err := rand.Read(uuid); err != nil {
		return "", fmt.Errorf("generate uuid: %w", err)
	}

	// Set version 4 (random) and variant 1
	uuid[6] = (uuid[6] & 0x0f) | 0x40
	uuid[8] = (uuid[8] & 0x3f) | 0x80

	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(uuid[0:4]),
		hex.EncodeToString(uuid[4:6]),
		hex.EncodeToString(uuid[6:8]),
		hex.EncodeToString(uuid[8:10]),
		hex.EncodeToString(uuid[10:16])), nil
}

// kcpassword encoding key used by macOS for auto-login
var kcpasswordKey = []byte{0x7D, 0x89, 0x52, 0x23, 0xD2, 0xBC, 0xDD, 0xEA, 0xA3, 0xB9, 0x1F}

// EncodeKCPassword encodes a password for /etc/kcpassword auto-login
// The kcpassword file uses a simple XOR cipher with a fixed key
func EncodeKCPassword(password string) []byte {
	// Password must be null-terminated
	passBytes := append([]byte(password), 0)

	// Pad to multiple of key length (11 bytes)
	keyLen := len(kcpasswordKey)
	padLen := keyLen - (len(passBytes) % keyLen)
	if padLen < keyLen {
		passBytes = append(passBytes, make([]byte, padLen)...)
	}

	// XOR with the key
	encoded := make([]byte, len(passBytes))
	for i := range passBytes {
		encoded[i] = passBytes[i] ^ kcpasswordKey[i%keyLen]
	}

	return encoded
}

// UserPlist represents the structure of a macOS user plist
// Stored at /var/db/dslocal/nodes/Default/users/<username>.plist
type UserPlist struct {
	// Authentication authority - required for password login
	AuthenticationAuthority []string `plist:"authentication_authority,omitempty"`
	// Generated UID - unique identifier for the user
	GeneratedUID []string `plist:"generateduid"`
	// Group membership
	GroupMembership []string `plist:"groupMembership,omitempty"`
	// Home directory path
	NFSHomeDirectory []string `plist:"home"`
	// User's full name
	RealName []string `plist:"realname"`
	// Record name (username)
	RecordName []string `plist:"name"`
	// Record type
	RecordType []string `plist:"record_type,omitempty"`
	// Login shell
	Shell []string `plist:"shell"`
	// User ID
	UniqueID []string `plist:"uid"`
	// Primary group ID
	PrimaryGroupID []string `plist:"gid"`
	// Password placeholder (asterisk = use ShadowHashData)
	Passwd []string `plist:"passwd,omitempty"`
	// ShadowHashData - binary plist containing password hash
	ShadowHashData [][]byte `plist:"ShadowHashData,omitempty"`
	// User picture path
	Picture []string `plist:"picture,omitempty"`
	// Admin flag (1 = admin)
	IsHidden []string `plist:"IsHidden,omitempty"`
}

// CreateUserPlist creates a complete user plist structure
func CreateUserPlist(username, fullname, password string, uid int, admin bool) (*UserPlist, error) {
	// Generate UUID
	uuid, err := GenerateUUID()
	if err != nil {
		return nil, fmt.Errorf("generate uuid: %w", err)
	}
	uuid = strings.ToUpper(uuid)

	// Generate password hash
	shadowHash, err := GenerateShadowHashData(password)
	if err != nil {
		return nil, fmt.Errorf("generate password hash: %w", err)
	}

	shadowHashData, err := EncodeShadowHashData(shadowHash)
	if err != nil {
		return nil, fmt.Errorf("encode shadow hash: %w", err)
	}

	// Build group membership
	groups := []string{"staff"}
	if admin {
		groups = append(groups, "admin", "_lpadmin")
	}

	// Build authentication authority
	authAuth := []string{
		";ShadowHash;HASHLIST:<SALTED-SHA512-PBKDF2>",
		fmt.Sprintf(";SecureToken;%s", uuid),
	}

	return &UserPlist{
		AuthenticationAuthority: authAuth,
		GeneratedUID:            []string{uuid},
		GroupMembership:         groups,
		NFSHomeDirectory:        []string{fmt.Sprintf("/Users/%s", username)},
		RealName:                []string{fullname},
		RecordName:              []string{username},
		Shell:                   []string{"/bin/zsh"},
		UniqueID:                []string{fmt.Sprintf("%d", uid)},
		PrimaryGroupID:          []string{"20"}, // staff group
		Passwd:                  []string{"********"},
		ShadowHashData:          [][]byte{shadowHashData},
		Picture:                 []string{"/Library/User Pictures/Fun/Smiling Face.heic"},
	}, nil
}

// EncodeUserPlist encodes a UserPlist to binary plist format
func EncodeUserPlist(up *UserPlist) ([]byte, error) {
	return plist.Marshal(up, plist.FormatBinary)
}

// LoginWindowPlist represents com.apple.loginwindow.plist
type LoginWindowPlist struct {
	AutoLoginUser       string `plist:"autoLoginUser,omitempty"`
	GuestEnabled        bool   `plist:"GuestEnabled"`
	SHOWFULLNAME        bool   `plist:"SHOWFULLNAME,omitempty"`
	lastUserName        string `plist:"lastUserName,omitempty"`
	DisableFDEAutoLogin bool   `plist:"DisableFDEAutoLogin,omitempty"`
}

// CreateLoginWindowPlist creates a loginwindow plist for auto-login
func CreateLoginWindowPlist(username string) *LoginWindowPlist {
	return &LoginWindowPlist{
		AutoLoginUser:       username,
		GuestEnabled:        false,
		SHOWFULLNAME:        true,
		DisableFDEAutoLogin: false,
	}
}

// EncodeLoginWindowPlist encodes the loginwindow plist
func EncodeLoginWindowPlist(lwp *LoginWindowPlist) ([]byte, error) {
	return plist.Marshal(lwp, plist.FormatBinary)
}

// HomeDirectories are the standard directories in a user's home folder
var HomeDirectories = []string{
	"Desktop",
	"Documents",
	"Downloads",
	"Library",
	"Library/Preferences",
	"Movies",
	"Music",
	"Pictures",
	"Public",
}

// CreateHomeDirectoryStructure creates the standard home directory structure
func CreateHomeDirectoryStructure(homeDir string, uid, gid int) error {
	for _, dir := range HomeDirectories {
		path := filepath.Join(homeDir, dir)
		if err := os.MkdirAll(path, 0755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
		// Set ownership
		if err := os.Chown(path, uid, gid); err != nil {
			return fmt.Errorf("chown %s: %w", dir, err)
		}
	}

	// Create .CFUserTextEncoding (en_US = 0x0:0x0)
	textEncodingPath := filepath.Join(homeDir, ".CFUserTextEncoding")
	if err := os.WriteFile(textEncodingPath, []byte("0x0:0x0"), 0644); err != nil {
		return fmt.Errorf("create .CFUserTextEncoding: %w", err)
	}
	if err := os.Chown(textEncodingPath, uid, gid); err != nil {
		return fmt.Errorf("chown .CFUserTextEncoding: %w", err)
	}

	// Set home directory ownership
	if err := os.Chown(homeDir, uid, gid); err != nil {
		return fmt.Errorf("chown home: %w", err)
	}

	return nil
}

// VerifyPassword checks whether password matches the given shadow hash data.
func VerifyPassword(password string, shd *ShadowHashData) bool {
	if shd.SaltedSHA512PBKDF2 == nil {
		return false
	}
	derived := pbkdf2.Key(
		[]byte(password),
		shd.SaltedSHA512PBKDF2.Salt,
		shd.SaltedSHA512PBKDF2.Iterations,
		len(shd.SaltedSHA512PBKDF2.Entropy),
		sha512.New,
	)
	if len(derived) != len(shd.SaltedSHA512PBKDF2.Entropy) {
		return false
	}
	for i := range derived {
		if derived[i] != shd.SaltedSHA512PBKDF2.Entropy[i] {
			return false
		}
	}
	return true
}
