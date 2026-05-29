// keychain.go — Native macOS keychain access via purego Security.framework bindings.
//
// Mirrors the pattern in authorization.go. Used by gateway_token.go to store the
// cove serve master token as a generic password without shelling out to /usr/bin/security.

package main

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	keychainSecLib      uintptr
	keychainCFLib       uintptr
	keychainLibC        uintptr
	keychainInitialized bool

	// Security.framework
	secItemAdd          func(attributes uintptr, result *uintptr) int32
	secItemCopyMatching func(query uintptr, result *uintptr) int32
	secItemDelete       func(query uintptr) int32
	secItemUpdate       func(query uintptr, attributesToUpdate uintptr) int32

	// CoreFoundation.framework
	cfStringCreateWithCString func(allocator uintptr, cStr *byte, encoding uint32) uintptr
	cfDataCreate              func(allocator uintptr, bytes *byte, length int) uintptr
	cfDictionaryCreate        func(allocator uintptr, keys, values *uintptr, numValues int, keyCallbacks, valueCallbacks uintptr) uintptr
	cfRelease                 func(cf uintptr)
	cfDataGetBytePtr          func(d uintptr) *byte
	cfDataGetLength           func(d uintptr) int

	libcMemcpy func(dst *uintptr, src uintptr, n uintptr) uintptr

	// kSec* key globals loaded via Dlsym (CFStringRef constants exported by Security.framework)
	secKeyClass       uintptr // kSecClass
	secKeyService     uintptr // kSecAttrService
	secKeyAccount     uintptr // kSecAttrAccount
	secKeyLabel       uintptr // kSecAttrLabel
	secKeyDescription uintptr // kSecAttrDescription
	secKeyValueData   uintptr // kSecValueData
	secKeyReturnData  uintptr // kSecReturnData
	secClassGenPass   uintptr // kSecClassGenericPassword

	// CoreFoundation globals
	kCFTypeDictKeyCallBacks uintptr
	kCFTypeDictValCallBacks uintptr
	kCFBooleanTrue          uintptr
)

const (
	kCFStringEncodingUTF8 uint32  = 0x08000100
	kCFAllocatorDefault   uintptr = 0

	errSecSuccess       int32 = 0
	errSecItemNotFound  int32 = -25300
	errSecDuplicateItem int32 = -25299
	errSecAuthFailed    int32 = -25293
	errSecNotAvailable  int32 = -25291
	errSecUserCanceled  int32 = -128
)

// errKeychainNotFound is returned by KeychainGetGenericPassword when no item exists.
var errKeychainNotFound = errors.New("keychain item not found")

// errKeychainUnavailable signals the keychain subsystem isn't usable in this process.
var errKeychainUnavailable = errors.New("keychain unavailable")

func initKeychain() {
	if keychainInitialized {
		return
	}
	keychainInitialized = true

	sec, err := purego.Dlopen("/System/Library/Frameworks/Security.framework/Security", purego.RTLD_LAZY)
	if err != nil {
		return
	}
	keychainSecLib = sec

	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY)
	if err != nil {
		return
	}
	keychainCFLib = cf

	libc, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_LAZY)
	if err != nil {
		return
	}
	keychainLibC = libc

	purego.RegisterLibFunc(&secItemAdd, keychainSecLib, "SecItemAdd")
	purego.RegisterLibFunc(&secItemCopyMatching, keychainSecLib, "SecItemCopyMatching")
	purego.RegisterLibFunc(&secItemDelete, keychainSecLib, "SecItemDelete")
	purego.RegisterLibFunc(&secItemUpdate, keychainSecLib, "SecItemUpdate")

	purego.RegisterLibFunc(&cfStringCreateWithCString, keychainCFLib, "CFStringCreateWithCString")
	purego.RegisterLibFunc(&cfDataCreate, keychainCFLib, "CFDataCreate")
	purego.RegisterLibFunc(&cfDictionaryCreate, keychainCFLib, "CFDictionaryCreate")
	purego.RegisterLibFunc(&cfRelease, keychainCFLib, "CFRelease")
	purego.RegisterLibFunc(&cfDataGetBytePtr, keychainCFLib, "CFDataGetBytePtr")
	purego.RegisterLibFunc(&cfDataGetLength, keychainCFLib, "CFDataGetLength")
	purego.RegisterLibFunc(&libcMemcpy, keychainLibC, "memcpy")

	// Load CFStringRef globals by Dlsym then copy the pointer-sized value.
	secKeyClass = loadCFStringGlobal(keychainSecLib, "kSecClass")
	secKeyService = loadCFStringGlobal(keychainSecLib, "kSecAttrService")
	secKeyAccount = loadCFStringGlobal(keychainSecLib, "kSecAttrAccount")
	secKeyLabel = loadCFStringGlobal(keychainSecLib, "kSecAttrLabel")
	secKeyDescription = loadCFStringGlobal(keychainSecLib, "kSecAttrDescription")
	secKeyValueData = loadCFStringGlobal(keychainSecLib, "kSecValueData")
	secKeyReturnData = loadCFStringGlobal(keychainSecLib, "kSecReturnData")
	secClassGenPass = loadCFStringGlobal(keychainSecLib, "kSecClassGenericPassword")

	kCFTypeDictKeyCallBacks = loadAddrGlobal(keychainCFLib, "kCFTypeDictionaryKeyCallBacks")
	kCFTypeDictValCallBacks = loadAddrGlobal(keychainCFLib, "kCFTypeDictionaryValueCallBacks")
	kCFBooleanTrue = loadPtrGlobal(keychainCFLib, "kCFBooleanTrue")
}

// loadCFStringGlobal loads a const CFStringRef exported symbol.
func loadCFStringGlobal(lib uintptr, name string) uintptr {
	return loadPtrGlobal(lib, name)
}

// loadAddrGlobal returns the address of a symbol (for struct-valued globals like callbacks).
func loadAddrGlobal(lib uintptr, name string) uintptr {
	addr, err := purego.Dlsym(lib, name)
	if err != nil {
		return 0
	}
	return addr
}

// loadPtrGlobal copies a pointer-sized global, such as kCFBooleanTrue.
func loadPtrGlobal(lib uintptr, name string) uintptr {
	addr, err := purego.Dlsym(lib, name)
	if err != nil || addr == 0 || libcMemcpy == nil {
		return 0
	}
	var v uintptr
	libcMemcpy(&v, addr, unsafe.Sizeof(v))
	return v
}

func keychainAvailableNative() bool {
	initKeychain()
	return secItemAdd != nil && secItemCopyMatching != nil &&
		cfDictionaryCreate != nil && secKeyClass != 0
}

// newCFString creates a CFStringRef from a Go string. Caller must cfRelease it.
func newCFString(s string) (uintptr, error) {
	cstr, err := syscall.BytePtrFromString(s)
	if err != nil {
		return 0, fmt.Errorf("newCFString: %w", err)
	}
	ref := cfStringCreateWithCString(kCFAllocatorDefault, cstr, kCFStringEncodingUTF8)
	if ref == 0 {
		return 0, fmt.Errorf("CFStringCreateWithCString returned nil for %q", s)
	}
	return ref, nil
}

// newCFData creates a CFDataRef from a byte slice. Caller must cfRelease it.
func newCFData(b []byte) uintptr {
	if len(b) == 0 {
		return cfDataCreate(kCFAllocatorDefault, nil, 0)
	}
	return cfDataCreate(kCFAllocatorDefault, &b[0], len(b))
}

// newCFDict builds a CFDictionaryRef from parallel key/value uintptr slices.
// Caller must cfRelease the returned ref.
func newCFDict(keys, values []uintptr) uintptr {
	if len(keys) != len(values) || len(keys) == 0 {
		return 0
	}
	return cfDictionaryCreate(
		kCFAllocatorDefault,
		&keys[0],
		&values[0],
		len(keys),
		kCFTypeDictKeyCallBacks,
		kCFTypeDictValCallBacks,
	)
}

// cfDataBytes extracts a copy of bytes from a CFDataRef.
func cfDataBytes(dataRef uintptr) []byte {
	if dataRef == 0 {
		return nil
	}
	length := cfDataGetLength(dataRef)
	if length <= 0 {
		return []byte{}
	}
	ptr := cfDataGetBytePtr(dataRef)
	if ptr == nil {
		return nil
	}
	out := make([]byte, length)
	copy(out, unsafe.Slice(ptr, length)) //nolint:unsafeptr
	return out
}

// KeychainSetGenericPassword stores a generic password. If an item with the same
// service+account already exists it is updated. Label and description are visible
// in Keychain Access and in macOS access prompts.
func KeychainSetGenericPassword(service, account, label, description string, data []byte) error {
	initKeychain()
	if !keychainAvailableNative() {
		return errKeychainUnavailable
	}

	svcRef, err := newCFString(service)
	if err != nil {
		return errKeychainUnavailable
	}
	defer cfRelease(svcRef)

	accRef, err := newCFString(account)
	if err != nil {
		return errKeychainUnavailable
	}
	defer cfRelease(accRef)

	lblRef, err := newCFString(label)
	if err != nil {
		return errKeychainUnavailable
	}
	defer cfRelease(lblRef)

	descRef, err := newCFString(description)
	if err != nil {
		return errKeychainUnavailable
	}
	defer cfRelease(descRef)

	dataRef := newCFData(data)
	if dataRef == 0 {
		return errKeychainUnavailable
	}
	defer cfRelease(dataRef)

	keys := []uintptr{secKeyClass, secKeyService, secKeyAccount, secKeyLabel, secKeyDescription, secKeyValueData}
	vals := []uintptr{secClassGenPass, svcRef, accRef, lblRef, descRef, dataRef}
	dict := newCFDict(keys, vals)
	if dict == 0 {
		return errKeychainUnavailable
	}
	defer cfRelease(dict)

	status := secItemAdd(dict, nil)
	if status == errSecDuplicateItem {
		return keychainUpdate(svcRef, accRef, dataRef)
	}
	if status != errSecSuccess {
		return keychainStatusError("SecItemAdd", status)
	}
	return nil
}

// keychainUpdate calls SecItemUpdate to replace the data of an existing item.
func keychainUpdate(svcRef, accRef, dataRef uintptr) error {
	queryKeys := []uintptr{secKeyClass, secKeyService, secKeyAccount}
	queryVals := []uintptr{secClassGenPass, svcRef, accRef}
	query := newCFDict(queryKeys, queryVals)
	if query == 0 {
		return errKeychainUnavailable
	}
	defer cfRelease(query)

	updateKeys := []uintptr{secKeyValueData}
	updateVals := []uintptr{dataRef}
	update := newCFDict(updateKeys, updateVals)
	if update == 0 {
		return errKeychainUnavailable
	}
	defer cfRelease(update)

	status := secItemUpdate(query, update)
	if status != errSecSuccess {
		return keychainStatusError("SecItemUpdate", status)
	}
	return nil
}

// KeychainGetGenericPassword retrieves stored data. Returns errKeychainNotFound if
// absent, or errKeychainUnavailable for any other error.
func KeychainGetGenericPassword(service, account string) ([]byte, error) {
	initKeychain()
	if !keychainAvailableNative() {
		return nil, errKeychainUnavailable
	}

	svcRef, err := newCFString(service)
	if err != nil {
		return nil, errKeychainUnavailable
	}
	defer cfRelease(svcRef)

	accRef, err := newCFString(account)
	if err != nil {
		return nil, errKeychainUnavailable
	}
	defer cfRelease(accRef)

	keys := []uintptr{secKeyClass, secKeyService, secKeyAccount, secKeyReturnData}
	vals := []uintptr{secClassGenPass, svcRef, accRef, kCFBooleanTrue}
	query := newCFDict(keys, vals)
	if query == 0 {
		return nil, errKeychainUnavailable
	}
	defer cfRelease(query)

	var result uintptr
	status := secItemCopyMatching(query, &result)
	if status == errSecItemNotFound {
		return nil, errKeychainNotFound
	}
	if status != errSecSuccess {
		return nil, keychainStatusError("SecItemCopyMatching", status)
	}
	if result == 0 {
		return nil, errKeychainUnavailable
	}
	defer cfRelease(result)

	return cfDataBytes(result), nil
}

// KeychainDeleteGenericPassword removes the item. Idempotent — returns nil if absent.
func KeychainDeleteGenericPassword(service, account string) error {
	initKeychain()
	if !keychainAvailableNative() {
		return errKeychainUnavailable
	}

	svcRef, err := newCFString(service)
	if err != nil {
		return errKeychainUnavailable
	}
	defer cfRelease(svcRef)

	accRef, err := newCFString(account)
	if err != nil {
		return errKeychainUnavailable
	}
	defer cfRelease(accRef)

	keys := []uintptr{secKeyClass, secKeyService, secKeyAccount}
	vals := []uintptr{secClassGenPass, svcRef, accRef}
	query := newCFDict(keys, vals)
	if query == 0 {
		return errKeychainUnavailable
	}
	defer cfRelease(query)

	status := secItemDelete(query)
	if status == errSecItemNotFound {
		return nil
	}
	if status != errSecSuccess {
		return keychainStatusError("SecItemDelete", status)
	}
	return nil
}

func keychainStatusError(op string, status int32) error {
	switch status {
	case errSecAuthFailed, errSecUserCanceled, errSecNotAvailable:
		return errKeychainUnavailable
	default:
		return fmt.Errorf("%s: OSStatus %d", op, status)
	}
}
