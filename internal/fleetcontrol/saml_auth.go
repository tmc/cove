package fleetcontrol

import (
	"bytes"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/beevik/etree"
	dsig "github.com/russellhaering/goxmldsig"
)

type samlAssertionClaims struct {
	ID           string
	Issuer       string
	Subject      string
	Audiences    []string
	NotBefore    time.Time
	NotOnOrAfter time.Time
}

type samlAuthenticatedAssertion struct {
	Binding   samlBindingRecord
	Claims    samlAssertionClaims
	Principal authenticatedPrincipal
}

func (s *Store) authenticateSAMLBearer(token string) (authenticatedPrincipal, bool) {
	data, err := decodeSAMLBearer(token)
	if err != nil {
		return authenticatedPrincipal{}, false
	}
	assertion, ok := s.authenticateSAMLData(data)
	if !ok {
		return authenticatedPrincipal{}, false
	}
	return assertion.Principal, true
}

func (s *Store) authenticateSAMLData(data []byte) (samlAuthenticatedAssertion, bool) {
	now := s.now().UTC()
	assertions := s.validSAMLAssertions(data, now)
	for _, assertion := range assertions {
		if !s.acceptSAMLAssertion(assertion.Binding, assertion.Claims, now) {
			continue
		}
		return assertion, true
	}
	return samlAuthenticatedAssertion{}, false
}

func (s *Store) validSAMLAssertions(data []byte, now time.Time) []samlAuthenticatedAssertion {
	doc := etree.NewDocument()
	if err := doc.ReadFromBytes(data); err != nil || doc.Root() == nil {
		return nil
	}

	s.mu.Lock()
	bindings := s.sortedSAMLBindingsLocked()
	s.mu.Unlock()

	var out []samlAuthenticatedAssertion
	for _, binding := range bindings {
		assertions := validatedSAMLAssertions(doc.Root(), binding.CertificatePEM, now)
		for _, assertion := range assertions {
			claims, err := samlAssertionClaimsFromElement(assertion)
			if err != nil || !samlClaimsMatch(binding, claims, now) {
				continue
			}
			out = append(out, samlAuthenticatedAssertion{
				Binding: binding,
				Claims:  claims,
				Principal: authenticatedPrincipal{
					Actor:     "saml:" + binding.Name,
					Namespace: binding.Namespace,
					Role:      binding.Role,
				},
			})
		}
	}
	return out
}

func decodeSAMLBearer(token string) ([]byte, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("saml bearer token required")
	}
	if len(token) >= len("saml:") && strings.EqualFold(token[:len("saml:")], "saml:") {
		token = strings.TrimSpace(token[len("saml:"):])
	}
	return decodeSAMLMessage(token)
}

func decodeSAMLMessage(token string) ([]byte, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("saml message required")
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
	return nil, fmt.Errorf("saml message is not xml")
}

const (
	samlSessionTokenPrefix = "saml-session:"
	defaultSAMLSessionTTL  = time.Hour
	maxSAMLSessionTTL      = 12 * time.Hour
)

func (s *Store) CreateSAMLSession(req SAMLSessionRequest) (SAMLSessionResult, error) {
	raw := strings.TrimSpace(req.SAMLResponse)
	if raw == "" {
		raw = strings.TrimSpace(req.SAMLAssertion)
	}
	if raw == "" {
		return SAMLSessionResult{}, fmt.Errorf("saml response required")
	}
	data, err := decodeSAMLMessage(raw)
	if err != nil {
		return SAMLSessionResult{}, err
	}
	ttl, err := samlSessionTTL(req.TTL)
	if err != nil {
		return SAMLSessionResult{}, err
	}
	now := s.now().UTC()
	assertions := s.validSAMLAssertions(data, now)
	for _, assertion := range assertions {
		token, err := newSAMLSessionToken()
		if err != nil {
			return SAMLSessionResult{}, err
		}
		result, ok, err := s.createSAMLSessionFromAssertion(now, assertion, strings.TrimSpace(req.RelayState), ttl, token)
		if err != nil {
			return SAMLSessionResult{}, err
		}
		if ok {
			return result, nil
		}
	}
	return SAMLSessionResult{}, fmt.Errorf("saml response did not match any binding")
}

func (s *Store) createSAMLSessionFromAssertion(now time.Time, assertion samlAuthenticatedAssertion, relayState string, ttl time.Duration, token string) (SAMLSessionResult, bool, error) {
	record := samlSessionRecord{
		TokenHash: tokenHash(token),
		Binding:   assertion.Binding.Name,
		Subject:   assertion.Claims.Subject,
		Namespace: assertion.Binding.Namespace,
		Role:      assertion.Binding.Role,
		Expires:   now.Add(ttl).UTC(),
		Created:   now,
		Updated:   now,
	}
	if record.TokenHash == "" {
		return SAMLSessionResult{}, false, fmt.Errorf("saml session token required")
	}
	replay := samlReplayRecord{
		Binding:     assertion.Binding.Name,
		AssertionID: assertion.Claims.ID,
		Expires:     assertion.Claims.NotOnOrAfter.Add(time.Minute).UTC(),
	}
	replayKey := samlReplayKey(replay.Binding, replay.AssertionID)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneSAMLReplaysLocked(now)
	s.pruneSAMLSessionRecordsLocked(now)
	if _, ok := s.samlReplays[replayKey]; ok {
		return SAMLSessionResult{}, false, nil
	}
	if _, ok := s.samlSessions[record.TokenHash]; ok {
		return SAMLSessionResult{}, false, fmt.Errorf("saml session token collision")
	}
	s.samlReplays[replayKey] = replay
	s.samlSessions[record.TokenHash] = record
	s.appendAuditLocked(now, AuditEvent{
		Actor:      assertion.Principal.Actor,
		Namespace:  assertion.Binding.Namespace,
		Action:     "saml_session.create",
		TargetType: "saml_session",
		TargetID:   assertion.Binding.Name,
		Fields: map[string]string{
			"binding":     assertion.Binding.Name,
			"expires":     record.Expires.Format(time.RFC3339),
			"relay_state": relayState,
			"role":        assertion.Binding.Role,
			"subject":     assertion.Claims.Subject,
			"ttl_seconds": strconv.FormatInt(int64(ttl/time.Second), 10),
		},
	})
	if err := s.persistLocked(); err != nil {
		return SAMLSessionResult{}, false, err
	}
	return SAMLSessionResult{
		Token:      token,
		Expires:    record.Expires,
		Binding:    publicSAMLBinding(assertion.Binding),
		Subject:    assertion.Claims.Subject,
		RelayState: relayState,
	}, true, nil
}

func (s *Store) authenticateSAMLSession(token string) (authenticatedPrincipal, bool) {
	token = strings.TrimSpace(token)
	if len(token) < len(samlSessionTokenPrefix) || !strings.EqualFold(token[:len(samlSessionTokenPrefix)], samlSessionTokenPrefix) {
		return authenticatedPrincipal{}, false
	}
	hash := tokenHash(token)
	if hash == "" {
		return authenticatedPrincipal{}, false
	}
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.samlSessions[hash]
	if !ok {
		return authenticatedPrincipal{}, false
	}
	record = normalizeSAMLSessionRecord(record)
	if !now.Before(record.Expires) {
		delete(s.samlSessions, hash)
		return authenticatedPrincipal{}, false
	}
	return authenticatedPrincipal{
		Actor:     "saml-session:" + record.Binding,
		Namespace: record.Namespace,
		Role:      record.Role,
	}, true
}

func samlSessionTTL(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultSAMLSessionTTL, nil
	}
	ttl, err := time.ParseDuration(raw)
	if err != nil || ttl <= 0 {
		return 0, fmt.Errorf("saml session ttl must be a positive duration")
	}
	if ttl > maxSAMLSessionTTL {
		return 0, fmt.Errorf("saml session ttl exceeds %s", maxSAMLSessionTTL)
	}
	return ttl, nil
}

func newSAMLSessionToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("create saml session token: %w", err)
	}
	return samlSessionTokenPrefix + base64.RawURLEncoding.EncodeToString(raw[:]), nil
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
	claims.ID = samlAttr(assertion, "ID")
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
	if strings.TrimSpace(claims.ID) == "" {
		return false
	}
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

func (s *Store) acceptSAMLAssertion(binding samlBindingRecord, claims samlAssertionClaims, now time.Time) bool {
	bindingName := strings.TrimSpace(binding.Name)
	assertionID := strings.TrimSpace(claims.ID)
	if bindingName == "" || assertionID == "" || claims.NotOnOrAfter.IsZero() {
		return false
	}
	record := samlReplayRecord{
		Binding:     bindingName,
		AssertionID: assertionID,
		Expires:     claims.NotOnOrAfter.Add(time.Minute).UTC(),
	}
	key := samlReplayKey(record.Binding, record.AssertionID)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneSAMLReplaysLocked(now)
	if _, ok := s.samlReplays[key]; ok {
		return false
	}
	s.samlReplays[key] = record
	if err := s.persistLocked(); err != nil {
		return false
	}
	return true
}

func (s *Store) pruneSAMLReplaysLocked(now time.Time) {
	now = now.UTC()
	for key, record := range s.samlReplays {
		record = normalizeSAMLReplayRecord(record)
		if record.Binding == "" || record.AssertionID == "" || record.Expires.IsZero() || !now.Before(record.Expires) {
			delete(s.samlReplays, key)
		}
	}
}

func (s *Store) pruneSAMLSessionRecordsLocked(now time.Time) {
	now = now.UTC()
	for key, record := range s.samlSessions {
		record = normalizeSAMLSessionRecord(record)
		if record.TokenHash == "" || record.Binding == "" || record.Expires.IsZero() || !now.Before(record.Expires) {
			delete(s.samlSessions, key)
		}
	}
}

func samlReplayKey(binding, assertionID string) string {
	return strings.TrimSpace(binding) + "\x00" + strings.TrimSpace(assertionID)
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
