package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hunterinvariants/promtact/internal/auth"
	"github.com/hunterinvariants/promtact/internal/witness"
)

// A witness that signs, so the receipt makes the whole round trip: published by
// the gateway, signed by the witness, stored locally, served over the API, and
// verified against the public key without the witness being involved again.
//
// The last step is the one that matters. Everything before it could work
// perfectly while the stored receipt was unverifiable, and nothing would look
// wrong until an auditor tried to use it.
func signingWitness(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating the witness key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var submitted struct {
			ChainIndex int    `json:"chain_index"`
			Head       string `json:"head"`
		}
		_ = json.NewDecoder(r.Body).Decode(&submitted)

		receipt := witness.Receipt{
			ChainIndex:  submitted.ChainIndex,
			Head:        submitted.Head,
			WitnessedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
			KeyID:       "test-w1",
		}
		digest := sha256.Sum256([]byte(receipt.SigningString()))
		rInt, sInt, err := ecdsa.Sign(rand.Reader, key, digest[:])
		if err != nil {
			t.Errorf("signing: %v", err)
			return
		}
		raw := make([]byte, 64)
		rInt.FillBytes(raw[:32])
		sInt.FillBytes(raw[32:])
		receipt.Signature = base64.StdEncoding.EncodeToString(raw)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(receipt)
	}))
	t.Cleanup(server.Close)
	return server, base64.StdEncoding.EncodeToString(der)
}

func TestWitnessReceiptIsStoredAndVerifiesOffline(t *testing.T) {
	witnessServer, publicKey := signingWitness(t)

	app, err := NewWithOptions(Options{
		Users: []auth.UserConfig{{
			Name:      "operator",
			TokenHash: auth.HashToken("secret"),
			Roles:     []string{auth.RoleAdmin},
		}},
		WitnessEndpoint: witnessServer.URL,
		WitnessToken:    "witness-token",
	})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}

	// Something has to be in the chain, or the receipt covers nothing.
	app.recordSystemAudit("test.event", "ok", map[string]string{"detail": "seed"})

	if err := app.PublishAuditAnchor(t.Context()); err != nil {
		t.Fatalf("publishing the anchor: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/audit/receipts", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	app.Routes().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reading receipts: %d %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Receipts []witness.Receipt `json:"receipts"`
		Signed   int               `json:"signed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(payload.Receipts) == 0 {
		t.Fatal("no receipt was stored, so nothing published can be checked later")
	}
	if payload.Signed != len(payload.Receipts) {
		t.Fatalf("%d of %d receipts are signed", payload.Signed, len(payload.Receipts))
	}

	// The point of the exercise: verified here, with the key alone. The witness
	// is not consulted and the server's opinion is not asked for.
	key, err := witness.ParsePublicKey(publicKey)
	if err != nil {
		t.Fatalf("parsing the public key: %v", err)
	}
	result := witness.VerifyAll(payload.Receipts, key)
	if result.Valid == 0 || len(result.Failures) > 0 {
		t.Fatalf("offline verification failed: valid=%d failures=%v", result.Valid, result.Failures)
	}
}

// A witness that does not sign must not produce receipts that look checkable.
// During a rollout this is the normal state, and reporting it as valid would be
// a false assurance at exactly the wrong moment.
func TestUnsignedWitnessProducesAnUnsignedReceipt(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var submitted map[string]any
		_ = json.NewDecoder(r.Body).Decode(&submitted)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"chain_index":  submitted["chain_index"],
			"head":         submitted["head"],
			"witnessed_at": time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		})
	}))
	defer plain.Close()

	app, err := NewWithOptions(Options{
		Users:           []auth.UserConfig{{Name: "operator", TokenHash: auth.HashToken("secret"), Roles: []string{auth.RoleAdmin}}},
		WitnessEndpoint: plain.URL,
	})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	app.recordSystemAudit("test.event", "ok", map[string]string{"detail": "seed"})
	if err := app.PublishAuditAnchor(t.Context()); err != nil {
		t.Fatalf("publishing: %v", err)
	}

	receipts, err := app.store.WitnessReceipts()
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if len(receipts) == 0 {
		t.Fatal("an unsigned witness should still produce a stored receipt")
	}
	for _, receipt := range receipts {
		if receipt.Signed() {
			t.Fatalf("record %d reports a signature the witness never sent", receipt.ChainIndex)
		}
	}
}

// A receipt is immutable once stored. Re-publishing at the same index must not
// let a later answer quietly replace the earlier one - that would hand an
// operator the overwrite the receipt exists to prevent.
func TestStoredReceiptsAreNotOverwritten(t *testing.T) {
	app, err := NewWithOptions(Options{
		Users: []auth.UserConfig{{Name: "operator", TokenHash: auth.HashToken("secret"), Roles: []string{auth.RoleAdmin}}},
	})
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	first := witness.Receipt{ChainIndex: 7, Head: "aaa", WitnessedAt: "2026-08-08T12:00:00.100Z", Signature: "original"}
	second := witness.Receipt{ChainIndex: 7, Head: "bbb", WitnessedAt: "2026-08-08T12:00:01.200Z", Signature: "replacement"}

	if err := app.store.SaveWitnessReceipt(first); err != nil {
		t.Fatalf("saving: %v", err)
	}
	if err := app.store.SaveWitnessReceipt(second); err != nil {
		t.Fatalf("saving again: %v", err)
	}
	stored, err := app.store.WitnessReceipts()
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("expected one receipt for index 7, got %d", len(stored))
	}
	if stored[0].Head != "aaa" || stored[0].Signature != "original" {
		t.Fatalf("the stored receipt was replaced: %+v", stored[0])
	}
}
