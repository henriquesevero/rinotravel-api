package argon2id_test

import (
	"errors"
	"strings"
	"testing"

	"rinotravel-api/internal/auth/argon2id"
)

var fastParams = argon2id.Params{MemoryKiB: 8, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32}

func TestHashAndVerify(t *testing.T) {
	h := argon2id.New(fastParams)

	encoded, err := h.Hash("correct horse battery")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}

	ok, err := h.Verify("correct horse battery", encoded)
	if err != nil || !ok {
		t.Errorf("Verify(correct) = %v, %v; want true, nil", ok, err)
	}
	ok, err = h.Verify("wrong password", encoded)
	if err != nil || ok {
		t.Errorf("Verify(wrong) = %v, %v; want false, nil", ok, err)
	}
}

func TestHash_UsesPHCFormatAndFreshSalt(t *testing.T) {
	h := argon2id.New(fastParams)

	first, _ := h.Hash("same password")
	second, _ := h.Hash("same password")

	if !strings.HasPrefix(first, "$argon2id$v=19$m=8,t=1,p=1$") {
		t.Errorf("Hash() = %q, want PHC format carrying the parameters", first)
	}
	if first == second {
		t.Error("two hashes of the same password are identical, salt is not random")
	}
	if strings.Contains(first, "same password") {
		t.Error("hash contains the plaintext password")
	}
}

func TestVerify_UsesParametersFromTheStoredHash(t *testing.T) {
	encoded, err := argon2id.New(fastParams).Hash("rotating parameters")
	if err != nil {
		t.Fatal(err)
	}

	differentConfig := argon2id.New(argon2id.Params{MemoryKiB: 16, Iterations: 2, Parallelism: 1, SaltLength: 16, KeyLength: 32})
	ok, err := differentConfig.Verify("rotating parameters", encoded)
	if err != nil || !ok {
		t.Errorf("Verify() = %v, %v; want true, nil", ok, err)
	}
}

func TestVerify_RejectsMalformedHashes(t *testing.T) {
	valid, _ := argon2id.New(fastParams).Hash("password value")

	tests := map[string]string{
		"empty":             "",
		"not phc":           "plaintext",
		"wrong algorithm":   strings.Replace(valid, "argon2id", "argon2i", 1),
		"wrong version":     strings.Replace(valid, "v=19", "v=16", 1),
		"missing parts":     "$argon2id$v=19$m=8,t=1,p=1$c2FsdA",
		"bad params":        "$argon2id$v=19$m=x,t=1,p=1$c2FsdHNhbHQ$" + strings.Repeat("A", 43),
		"zero memory":       "$argon2id$v=19$m=0,t=1,p=1$c2FsdHNhbHQ$" + strings.Repeat("A", 43),
		"absurd memory":     "$argon2id$v=19$m=4294967295,t=1,p=1$c2FsdHNhbHQ$" + strings.Repeat("A", 43),
		"absurd iterations": "$argon2id$v=19$m=8,t=1000000,p=1$c2FsdHNhbHQ$" + strings.Repeat("A", 43),
		"bad salt encoding": "$argon2id$v=19$m=8,t=1,p=1$!!!$" + strings.Repeat("A", 43),
		"short key":         "$argon2id$v=19$m=8,t=1,p=1$c2FsdHNhbHQ$QUJD",
	}

	for name, encoded := range tests {
		t.Run(name, func(t *testing.T) {
			ok, err := argon2id.New(fastParams).Verify("password value", encoded)
			if ok || !errors.Is(err, argon2id.ErrInvalidHash) {
				t.Errorf("Verify() = %v, %v; want false, ErrInvalidHash", ok, err)
			}
		})
	}
}
