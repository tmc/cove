package fleetcontrol

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	"github.com/beevik/etree"
	dsig "github.com/russellhaering/goxmldsig"
)

type samlAssertionClaims struct {
	Issuer       string
	Subject      string
	Audiences    []string
	NotBefore    time.Time
	NotOnOrAfter time.Time
}

func (s *Store) authenticateSAMLBearer(token string) (authenticatedPrincipal, bool) {
	data, err := decodeSAMLBearer(token)
	if err != nil {
		return authenticatedPrincipal{}, false
	}
	doc := etree.NewDocument()
	if err := doc.ReadFromBytes(data); err != nil || doc.Root() == nil {
		return authenticatedPrincipal{}, false
	}

	s.mu.Lock()
	now := s.now().UTC()
	bindings := s.sortedSAMLBindingsLocked()
	s.mu.Unlock()

	for _, binding := range bindings {
		assertions := validatedSAMLAssertions(doc.Root(), binding.CertificatePEM, now)
		for _, assertion := range assertions {
			claims, err := samlAssertionClaimsFromElement(assertion)
			if err != nil || !samlClaimsMatch(binding, claims, now) {
				continue
			}
			return authenticatedPrincipal{
				Actor:     "saml:" + binding.Name,
				Namespace: binding.Namespace,
				Role:      binding.Role,
			}, true
		}
	}
	return authenticatedPrincipal{}, false
}

func decodeSAMLBearer(token string) ([]byte, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("saml bearer token required")
	}
	if len(token) >= len("saml:") && strings.EqualFold(token[:len("saml:")], "saml:") {
		token = strings.TrimSpace(token[len("saml:"):])
	}
	if strings.HasPrefix(token, "<") {
		return []byte(token), nil
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		data, err := enc.DecodeString(token)
		if err == nil && bytes.HasPrefix(bytes.TrimSpace(data), []byte("<")) {
			return data, nil
		}
	}
	return nil, fmt.Errorf("saml bearer token is not xml")
}

func validatedSAMLAssertions(root *etree.Element, certPEM string, now time.Time) []*etree.Element {
	cert, err := parseSAMLCertificatePEM(certPEM)
	if err != nil {
		return nil
	}
	store := &dsig.MemoryX509CertificateStore{Roots: []*x509.Certificate{cert}}
	vc := dsig.NewDefaultValidationContext(store)
	vc.IdAttribute = "ID"
	vc.Clock = dsig.NewFakeClockAt(now)

	if samlSignedElementAlgorithmsStrong(root) {
		if signed, err := vc.Validate(root); err == nil {
			if samlElementIs(signed, "Assertion") {
				return []*etree.Element{signed}
			}
			return samlFindElements(signed, "Assertion")
		}
	}

	var out []*etree.Element
	for _, assertion := range samlFindElements(root, "Assertion") {
		if !samlSignedElementAlgorithmsStrong(assertion) {
			continue
		}
		signed, err := vc.Validate(assertion)
		if err == nil && samlElementIs(signed, "Assertion") {
			out = append(out, signed)
		}
	}
	return out
}

func samlSignedElementAlgorithmsStrong(el *etree.Element) bool {
	signature := samlFirstChild(el, "Signature")
	if signature == nil {
		return false
	}
	signedInfo := samlFirstChild(signature, "SignedInfo")
	if signedInfo == nil {
		return false
	}
	method := samlFirstChild(signedInfo, "SignatureMethod")
	if method == nil || !samlSignatureMethodAllowed(samlAttr(method, "Algorithm")) {
		return false
	}
	references := samlChildren(signedInfo, "Reference")
	if len(references) == 0 {
		return false
	}
	for _, reference := range references {
		digest := samlFirstChild(reference, "DigestMethod")
		if digest == nil || !samlDigestMethodAllowed(samlAttr(digest, "Algorithm")) {
			return false
		}
	}
	return true
}

func samlSignatureMethodAllowed(algorithm string) bool {
	switch strings.TrimSpace(algorithm) {
	case dsig.RSASHA256SignatureMethod,
		dsig.RSASHA384SignatureMethod,
		dsig.RSASHA512SignatureMethod,
		dsig.ECDSASHA256SignatureMethod,
		dsig.ECDSASHA384SignatureMethod,
		dsig.ECDSASHA512SignatureMethod:
		return true
	default:
		return false
	}
}

func samlDigestMethodAllowed(algorithm string) bool {
	switch strings.TrimSpace(algorithm) {
	case "http://www.w3.org/2001/04/xmlenc#sha256",
		"http://www.w3.org/2001/04/xmldsig-more#sha384",
		"http://www.w3.org/2001/04/xmlenc#sha512":
		return true
	default:
		return false
	}
}

func parseSAMLCertificatePEM(certPEM string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(certPEM)))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("parse saml certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

func samlAssertionClaimsFromElement(assertion *etree.Element) (samlAssertionClaims, error) {
	var claims samlAssertionClaims
	claims.Issuer = samlChildText(assertion, "Issuer")
	subject := samlFirstChild(assertion, "Subject")
	if subject != nil {
		claims.Subject = samlChildText(subject, "NameID")
	}
	conditions := samlFirstChild(assertion, "Conditions")
	if conditions == nil {
		return claims, fmt.Errorf("saml assertion conditions required")
	}
	var err error
	if raw := strings.TrimSpace(samlAttr(conditions, "NotBefore")); raw != "" {
		claims.NotBefore, err = parseSAMLTime(raw)
		if err != nil {
			return claims, err
		}
	}
	if raw := strings.TrimSpace(samlAttr(conditions, "NotOnOrAfter")); raw != "" {
		claims.NotOnOrAfter, err = parseSAMLTime(raw)
		if err != nil {
			return claims, err
		}
	}
	for _, restriction := range samlFindElements(conditions, "AudienceRestriction") {
		for _, audience := range samlChildren(restriction, "Audience") {
			if value := strings.TrimSpace(audience.Text()); value != "" {
				claims.Audiences = append(claims.Audiences, value)
			}
		}
	}
	return claims, nil
}

func samlClaimsMatch(binding samlBindingRecord, claims samlAssertionClaims, now time.Time) bool {
	if binding.EntityID != strings.TrimSpace(claims.Issuer) {
		return false
	}
	if claims.Subject == "" {
		return false
	}
	if binding.Subject != "" && binding.Subject != claims.Subject {
		return false
	}
	if !samlAudienceContains(claims.Audiences, binding.Audience) {
		return false
	}
	if claims.NotOnOrAfter.IsZero() {
		return false
	}
	const skew = time.Minute
	if !claims.NotBefore.IsZero() && now.Add(skew).Before(claims.NotBefore) {
		return false
	}
	return now.Before(claims.NotOnOrAfter.Add(skew))
}

func parseSAMLTime(raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, fmt.Errorf("parse saml time: %w", err)
	}
	return t.UTC(), nil
}

func samlAudienceContains(audiences []string, want string) bool {
	want = strings.TrimSpace(want)
	for _, audience := range audiences {
		if strings.TrimSpace(audience) == want {
			return true
		}
	}
	return false
}

func samlChildText(el *etree.Element, tag string) string {
	child := samlFirstChild(el, tag)
	if child == nil {
		return ""
	}
	return strings.TrimSpace(child.Text())
}

func samlFirstChild(el *etree.Element, tag string) *etree.Element {
	for _, child := range el.ChildElements() {
		if samlElementIs(child, tag) {
			return child
		}
	}
	return nil
}

func samlChildren(el *etree.Element, tag string) []*etree.Element {
	var out []*etree.Element
	for _, child := range el.ChildElements() {
		if samlElementIs(child, tag) {
			out = append(out, child)
		}
	}
	return out
}

func samlFindElements(el *etree.Element, tag string) []*etree.Element {
	var out []*etree.Element
	var walk func(*etree.Element)
	walk = func(current *etree.Element) {
		if samlElementIs(current, tag) {
			out = append(out, current)
		}
		for _, child := range current.ChildElements() {
			walk(child)
		}
	}
	walk(el)
	return out
}

func samlElementIs(el *etree.Element, tag string) bool {
	if el == nil {
		return false
	}
	return samlLocalName(el.Tag) == tag
}

func samlAttr(el *etree.Element, key string) string {
	for _, attr := range el.Attr {
		if samlLocalName(attr.Key) == key {
			return strings.TrimSpace(attr.Value)
		}
	}
	return ""
}

func samlLocalName(name string) string {
	if i := strings.LastIndexByte(name, ':'); i >= 0 {
		return name[i+1:]
	}
	return name
}
